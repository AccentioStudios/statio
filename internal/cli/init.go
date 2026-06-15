package cli

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/accentiostudios/statio/internal/fsutil"
	"github.com/accentiostudios/statio/internal/tailscale"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

//go:embed assets/*.tmpl
var assets embed.FS

func newInitCmd(version string) *cobra.Command {
	cmd := &cobra.Command{Use: "init", Short: "Guided setup for the server, integrations, or repo"}
	cmd.AddCommand(newInitRepoCmd(), newInitServerCmd(), newInitIntegrationsCmd())
	return cmd
}

// render substitutes {{.Key}} placeholders with the given values via plain string
// replacement. It deliberately does NOT use text/template: the assets contain literal
// GitHub Actions expressions (${{ ... }}) that a template engine would try to parse.
func render(name string, data map[string]string) ([]byte, error) {
	b, err := assets.ReadFile("assets/" + name)
	if err != nil {
		return nil, err
	}
	s := string(b)
	for k, v := range data {
		s = strings.ReplaceAll(s, "{{."+k+"}}", v)
	}
	return []byte(s), nil
}

// ---- huh helpers ----

func inputField(title, desc, placeholder string, v *string, required bool) huh.Field {
	in := huh.NewInput().Title(title).Description(desc).Placeholder(placeholder).Value(v)
	if required {
		in = in.Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("requerido")
			}
			return nil
		})
	}
	return in
}

func passwordField(title, desc string, v *string) huh.Field {
	return huh.NewInput().Title(title).Description(desc).EchoMode(huh.EchoModePassword).Value(v).
		Validate(func(s string) error {
			if s == "" {
				return fmt.Errorf("requerido")
			}
			return nil
		})
}

func runForm(fields ...huh.Field) error {
	return huh.NewForm(huh.NewGroup(fields...)).WithTheme(huh.ThemeCharm()).Run()
}

func confirm(title string) (bool, error) {
	var v bool
	err := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title(title).Value(&v))).WithTheme(huh.ThemeCharm()).Run()
	return v, err
}

// ============================ init server ============================

func newInitServerCmd() *cobra.Command {
	var hostname, issuer, configPath, oauthSecretFile, clientID, tailnetAPI string
	var oauthStdin bool
	var keyDays int
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Set up the deploy agent on this server (interactive)",
		Long: "Sets up the agent on THIS machine. It does not ask for any repo — you authorize each\n" +
			"app later with `statio app add`. It takes one bootstrap Tailscale OAuth client and uses\n" +
			"it to join the tailnet and to mint the shared tag:ci auth key CI uses to reach the agent.",
		RunE: func(c *cobra.Command, _ []string) error {
			if issuer == "" {
				issuer = "https://token.actions.githubusercontent.com"
			}
			var clientSecret string

			if interactive() && (hostname == "" || clientID == "") {
				banner("statio · init server", "Configura el agente de deploy en esta máquina")
				if hostname == "" {
					hostname = "statio"
				}
				if err := runForm(
					inputField("Nombre de este servidor",
						"Tailscale le dará la dirección <nombre>.<tu-tailnet>.ts.net, y CI usa esa dirección para enviarle el deploy. Con un solo servidor, 'statio' está bien.",
						"statio", &hostname, true),
				); err != nil {
					return err
				}
				sectionTitle("Credencial de Tailscale (una sola vez)")
				info("Crea UN OAuth client en Tailscale con scopes 'auth_keys' + 'devices', dueño de los")
				info("tags tag:agent y tag:ci. El agente lo usa para unirse a la tailnet y para crear la")
				info("auth key que CI usa para llegar aquí. No vuelves a tocar la consola de Tailscale.")
				info("https://login.tailscale.com/admin/settings/oauth")
				if err := runForm(
					inputField("OAuth client ID", "Identificador del client (no es secreto).", "k123ABC...", &clientID, true),
					passwordField("OAuth client secret", "Empieza con tskey-client-…; se guarda 0600 root, nunca se imprime", &clientSecret),
				); err != nil {
					return err
				}

				sectionTitle("Resumen")
				info("hostname: %s", hostname)
				info("config:   %s", configPath)
				ok, err := confirm("¿Escribir la configuración + unit de systemd y crear la auth key de CI?")
				if err != nil {
					return err
				}
				if !ok {
					warnLine("Cancelado. No se escribió nada.")
					return nil
				}
			} else {
				if hostname == "" || clientID == "" {
					return fmt.Errorf("modo no-interactivo: --hostname y --ts-oauth-client-id (+ el secret por stdin/file) son requeridos")
				}
				b, err := readSecret(oauthStdin, oauthSecretFile, "--ts-oauth-secret")
				if err != nil {
					return err
				}
				clientSecret = string(b)
			}

			if err := writeServerFiles(hostname, issuer, configPath, clientID, clientSecret); err != nil {
				return err
			}
			okLine("Escrito: %s, /etc/statio/secrets/oauth.json y la unit de systemd", configPath)

			// The server mints the shared tag:ci auth key CI uses to reach the agent.
			sectionTitle("Auth key de CI (la crea el servidor)")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			key, err := tailscale.New(tailnetAPI).MintCIKey(ctx, clientID, clientSecret, keyDays)
			if err != nil {
				warnLine("No pude crear la auth key automáticamente: %v", err)
				info("Verifica que el OAuth client tenga scope 'auth_keys' y sea dueño de tag:ci, y reintenta.")
			} else {
				info("Auth key tag:ci creada (reutilizable, efímera, válida %d días). Ponla en GitHub:", keyDays)
				codeBlock("gh secret set STATIO_TS_AUTHKEY --body '" + key + "'")
				info("La misma key sirve para TODOS tus repos. Rótala re-ejecutando 'statio init server'.")
			}

			sectionTitle("Próximos pasos")
			codeBlock(
				"systemctl daemon-reload && systemctl enable --now statio-agent",
				"sudo statio app add <nombre>   # acepta una app y fija su repo firmante (uno por app)",
				"sudo statio init integrations  # NPMplus + Cloudflare + IP pública (opcional)",
			)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&hostname, "hostname", "", "tsnet MagicDNS hostname (no-interactivo)")
	f.StringVar(&clientID, "ts-oauth-client-id", "", "Tailscale OAuth client id (no-interactivo)")
	f.BoolVar(&oauthStdin, "ts-oauth-secret-stdin", false, "leer el OAuth client secret de stdin (no-interactivo)")
	f.StringVar(&oauthSecretFile, "ts-oauth-secret-file", "", "leer el OAuth client secret de un archivo (no-interactivo)")
	f.StringVar(&issuer, "issuer", "", "cosign OIDC issuer")
	f.StringVar(&configPath, "config", "/etc/statio/config.yaml", "config output path")
	f.IntVar(&keyDays, "ci-key-days", 90, "validez (días) de la auth key tag:ci minteada")
	f.StringVar(&tailnetAPI, "tailscale-api", "", "base de la API de Tailscale (para pruebas; default público)")
	return cmd
}

func writeServerFiles(hostname, issuer, configPath, clientID, clientSecret string) error {
	if err := os.MkdirAll("/etc/statio/secrets", 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return err
	}
	oauth := fmt.Sprintf(`{"client_id":%q,"client_secret":%q}`, clientID, clientSecret)
	if err := fsutil.SecureWrite("/etc/statio/secrets/oauth.json", []byte(oauth), 0o600); err != nil {
		return err
	}
	// No cosign.identity here: each app pins its own signer via `statio app add`.
	cfg := fmt.Sprintf(`hostname: %s
listen_port: 443
tailscale:
  oauth_file: /etc/statio/secrets/oauth.json
  tags: [tag:agent]
  state_dir: /var/lib/statio/tsnet
cosign:
  oidc_issuer: %s
  require_tlog: true
  require_sct: true
registry:
  ghcr_auth_file: /etc/statio/secrets/ghcr.json
services_dir: /etc/statio/services
state_dir: /var/lib/statio
log_level: info
`, hostname, issuer)
	if err := fsutil.SecureWrite(configPath, []byte(cfg), 0o600); err != nil {
		return err
	}
	unit, err := render("statio-agent.service.tmpl", map[string]string{"ConfigPath": configPath})
	if err != nil {
		return err
	}
	return os.WriteFile("/etc/systemd/system/statio-agent.service", unit, 0o644)
}

// ======================== init integrations =========================

func newInitIntegrationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integrations",
		Short: "Configure NPMplus + Cloudflare + the pinned public IP (interactive)",
		RunE: func(c *cobra.Command, _ []string) error {
			if !interactive() {
				return fmt.Errorf("init integrations es interactivo; ejecútalo en una terminal")
			}
			banner("statio · init integrations", "Reverse proxy (NPMplus) y DNS (Cloudflare)")

			doNPM, err := confirm("¿Configurar NPMplus (reverse proxy)?")
			if err != nil {
				return err
			}
			if doNPM {
				url, identity, secret := "http://npmplus:81", "statio-agent", ""
				if err := runForm(
					inputField("NPMplus base URL", "Localhost o nombre de red docker", "http://npmplus:81", &url, true),
					inputField("NPMplus API identity", "Usuario dedicado no-admin", "statio-agent", &identity, true),
					passwordField("NPMplus API secret", "Se guarda 0600 root", &secret),
				); err != nil {
					return err
				}
				if err := writeJSONSecret("/etc/statio/secrets/npmplus.json",
					fmt.Sprintf(`{"base_url":%q,"identity":%q,"secret":%q}`, url, identity, secret)); err != nil {
					return err
				}
				okLine("NPMplus configurado")
				sectionTitle("Agrega a /etc/statio/config.yaml")
				codeBlock("npmplus:", "  base_url: "+url, "  credentials_file: /etc/statio/secrets/npmplus.json")
			}

			doCF, err := confirm("¿Configurar Cloudflare (DNS)?")
			if err != nil {
				return err
			}
			if doCF {
				zoneID, apex, ip, token := "", "", "", ""
				if err := runForm(
					inputField("Cloudflare Zone ID", "Dashboard → tu zona → Overview", "", &zoneID, true),
					inputField("Zone apex", "El dominio raíz (ej. example.com)", "example.com", &apex, true),
					inputField("IP pública del servidor", "A donde apuntarán los registros A", "203.0.113.10", &ip, true),
					passwordField("Cloudflare API token", "Scope Zone.DNS:Edit en esta zona; 0600 root", &token),
				); err != nil {
					return err
				}
				if err := writeJSONSecret("/etc/statio/secrets/cloudflare.json",
					fmt.Sprintf(`{"api_token":%q,"zone_id":%q}`, token, zoneID)); err != nil {
					return err
				}
				okLine("Cloudflare configurado")
				sectionTitle("Agrega a /etc/statio/config.yaml")
				codeBlock(
					"cloudflare:",
					"  credentials_file: /etc/statio/secrets/cloudflare.json",
					"  zone_apex: "+apex,
					"dns:",
					"  public_ip: "+ip,
					"  ttl: 1",
					"  proxied: false",
				)
			}
			if !doNPM && !doCF {
				warnLine("Nada que configurar.")
			}
			return nil
		},
	}
	return cmd
}

func writeJSONSecret(path, json string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsutil.SecureWrite(path, []byte(json), 0o600)
}

// ============================ init repo =============================

func newInitRepoCmd() *cobra.Command {
	var target, service, image, actionRef, out, statioOut, branch, workflow string
	var createWorkflow bool
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Prepara tu repo para statio (statio.yaml + cómo llamar al Action) — se corre DENTRO del repo",
		RunE: func(c *cobra.Command, _ []string) error {
			// Auto-detect owner/repo up front (local git read; works for private repos)
			// so the wizard comes prefilled and we can print the exact signing identity.
			owner, repo, repoOK := detectOwnerRepo()
			if repoOK {
				if service == "" {
					service = repo
				}
				if image == "" {
					image = "ghcr.io/" + owner + "/" + repo
				}
			}

			if interactive() && (target == "" || service == "" || image == "") {
				banner("statio · init repo", "Asistente interactivo — se ejecuta DENTRO del repo de tu proyecto (no en el servidor)")
				if repoOK {
					info("Repo detectado: %s/%s — rellené los valores por defecto; solo confirma o edita.", owner, repo)
				}
				if err := runForm(
					inputField("Dirección del servidor (Tailscale)", "El nombre que le pusiste al agente + .<tu-tailnet>.ts.net. Ej: statio.tu-org.ts.net", "statio.<tu-tailnet>.ts.net", &target, true),
					inputField("Service", "Nombre del servicio (debe estar habilitado en el agente con 'statio enable')", "api", &service, true),
					inputField("Image repository", "Repo de tu imagen (debe coincidir con 'statio enable --image')", "ghcr.io/accentiostudios/api", &image, true),
				); err != nil {
					return err
				}
			} else if target == "" || service == "" || image == "" {
				return fmt.Errorf("modo no-interactivo: --target, --service y --image son requeridos")
			}

			// 1. Scaffold statio.yaml (statio's own config), only if missing.
			if _, err := os.Stat(statioOut); os.IsNotExist(err) {
				py, err := render("statio.yaml.tmpl", map[string]string{"Service": service})
				if err != nil {
					return err
				}
				if err := os.WriteFile(statioOut, py, 0o644); err != nil {
					return err
				}
				okLine("Generado %s (edita los servicios/env de tu app)", statioOut)
			} else {
				info("%s ya existe; no se tocó", statioOut)
			}

			// 2. Print the exact cosign identity to paste on the server, from the repo
			//    detected above. Local git read, so it works for private repos too.
			if repoOK {
				ident := buildIdentity(owner, repo, workflow, branch)
				sectionTitle("Identidad de firma — pega esto EN EL SERVIDOR 🖥️")
				info("Detecté tu repo: %s/%s  (rama %s, workflow %s)", owner, repo, branch, workflow)
				info("Identidad cosign: %s", ident)
				info("En el asistente de 'statio init server', en 'Repositorio de GitHub' ingresa: %s/%s", owner, repo)
				info("(o en modo no-interactivo:)")
				codeBlock("statio init server --hostname <nombre> --repo " + owner + "/" + repo + " --branch " + branch + " --ts-oauth-secret-stdin")
			} else {
				info("No pude detectar el repo desde git (¿hay un remote 'origin'?). En el server ingresa tu owner/repo a mano.")
			}

			// 3. Workflow: never modify an existing one. Detect + adapt.
			existing := detectWorkflows()
			switch {
			case len(existing) > 0:
				sectionTitle("Ya tienes CI — agrega statio como un step 💻")
				info("Detecté: %s", strings.Join(existing, ", "))
				info("statio NO toca tu workflow. Agrega este step donde construyes y firmas tu imagen:")
				printSnippet(target, service, image, actionRef)
			default:
				gen := createWorkflow
				if !gen && interactive() {
					ok, err := confirm("No detecté un workflow de CI. ¿Genero un .github/workflows/deploy.yml listo para usar?")
					if err != nil {
						return err
					}
					gen = ok
				}
				if gen {
					if _, err := os.Stat(out); err == nil {
						info("%s ya existe; no se sobreescribe. Usa este step:", out)
						printSnippet(target, service, image, actionRef)
					} else {
						yml, err := render("deploy.yml.tmpl", map[string]string{"Target": target, "Service": service, "Image": image, "ActionRef": actionRef})
						if err != nil {
							return err
						}
						if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
							return err
						}
						if err := os.WriteFile(out, yml, 0o644); err != nil {
							return err
						}
						okLine("Generado %s", out)
					}
				} else {
					sectionTitle("Agrega statio a tu workflow 💻")
					printSnippet(target, service, image, actionRef)
				}
			}

			// 4. Secrets — run from your machine, towards GitHub.
			sectionTitle("Configura estos GitHub secrets (desde tu máquina 💻)")
			codeBlock(
				"gh secret set STATIO_TS_AUTHKEY --body '<la auth key tag:ci que imprimió statio init server>'",
				"gh secret set DATABASE_URL      --body '<el valor de cada env que pide tu statio.yaml>'",
			)
			info("STATIO_TS_AUTHKEY es la misma para todos tus repos; la crea 'statio init server'.")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&target, "target", "", "agent MagicDNS host (no-interactivo)")
	f.StringVar(&service, "service", "", "service name (no-interactivo)")
	f.StringVar(&image, "image", "", "image repository (no-interactivo)")
	f.StringVar(&actionRef, "action-ref", "accentiostudios/statio@v1", "the statio Action ref (Marketplace)")
	f.StringVar(&out, "out", ".github/workflows/deploy.yml", "ruta del workflow (con --create-workflow)")
	f.StringVar(&statioOut, "statio-out", "statio.yaml", "statio.yaml starter output path")
	f.BoolVar(&createWorkflow, "create-workflow", false, "generar un deploy.yml starter (solo si no existe ninguno)")
	f.StringVar(&branch, "branch", "main", "rama de deploy (para la identidad cosign impresa)")
	f.StringVar(&workflow, "workflow", "deploy.yml", "nombre del archivo de workflow (para la identidad impresa)")
	return cmd
}

// printSnippet renders and prints the CI step to paste into the user's own workflow. It is
// printed verbatim (it contains literal ${{ ... }} GitHub expressions).
func printSnippet(target, service, image, actionRef string) {
	snip, err := render("statio-step.snippet.tmpl", map[string]string{
		"Target": target, "Service": service, "Image": image, "ActionRef": actionRef,
	})
	if err != nil {
		warnLine("no pude generar el snippet: %v", err)
		return
	}
	fmt.Println()
	fmt.Println(string(snip))
}

// readSecret reads a secret from stdin or a file (non-interactive paths).
func readSecret(stdin bool, file, flag string) ([]byte, error) {
	if stdin {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		if err != nil {
			return nil, err
		}
		return bytes.TrimRight(b, "\r\n"), nil
	}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		return bytes.TrimRight(b, "\r\n"), nil
	}
	return nil, fmt.Errorf("provide %s-stdin or %s-file", flag, flag)
}

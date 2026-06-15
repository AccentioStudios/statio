package cli

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/accentiostudios/statio/internal/fsutil"
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

func render(name string, data any) ([]byte, error) {
	t, err := template.ParseFS(assets, "assets/"+name)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
	var hostname, identity, issuer, configPath, oauthSecretFile string
	var oauthStdin bool
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Set up the deploy agent on this server (interactive)",
		RunE: func(c *cobra.Command, _ []string) error {
			if issuer == "" {
				issuer = "https://token.actions.githubusercontent.com"
			}
			var oauthSecret string

			if interactive() && (hostname == "" || identity == "") {
				banner("statio · init server", "Configura el agente de deploy en este servidor")
				org, repo, wf, branch := "", "", "deploy.yml", "main"
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
				sectionTitle("Identidad de firma (cosign)")
				info("Solo imágenes firmadas por este workflow se desplegarán.")
				if err := runForm(
					inputField("GitHub org", "Tu organización", "accentiostudios", &org, true),
					inputField("Repositorio", "El repo que despliega (sin la org)", "api", &repo, true),
					inputField("Archivo del workflow", "Dentro de .github/workflows/", "deploy.yml", &wf, true),
					inputField("Branch", "La rama autorizada a desplegar", "main", &branch, true),
				); err != nil {
					return err
				}
				identity = fmt.Sprintf("https://github.com/%s/%s/.github/workflows/%s@refs/heads/%s", trimmed(org), trimmed(repo), trimmed(wf), trimmed(branch))

				sectionTitle("Credencial de Tailscale")
				info("Crea un OAuth client (dueño de tag:agent) en el admin console de Tailscale.")
				info("https://login.tailscale.com/admin/settings/oauth")
				if err := runForm(
					passwordField("OAuth client secret (tag:agent)", "Se guarda 0600 root; nunca se imprime", &oauthSecret),
				); err != nil {
					return err
				}

				sectionTitle("Resumen")
				info("hostname:  %s", hostname)
				info("identity:  %s", identity)
				info("config:    %s", configPath)
				ok, err := confirm("¿Escribir la configuración y la unit de systemd?")
				if err != nil {
					return err
				}
				if !ok {
					warnLine("Cancelado. No se escribió nada.")
					return nil
				}
			} else {
				if hostname == "" || identity == "" {
					return fmt.Errorf("modo no-interactivo: --hostname y --identity son requeridos")
				}
				b, err := readSecret(oauthStdin, oauthSecretFile, "--ts-oauth-secret")
				if err != nil {
					return err
				}
				oauthSecret = string(b)
			}

			if err := writeServerFiles(hostname, issuer, identity, configPath, oauthSecret); err != nil {
				return err
			}
			okLine("Escrito: %s, /etc/statio/secrets/oauth, y la unit de systemd", configPath)
			sectionTitle("Próximos pasos")
			codeBlock(
				"systemctl daemon-reload && systemctl enable --now statio-agent",
				"statio init integrations              # NPMplus + Cloudflare + IP pública (opcional)",
				"statio enable <svc> --image <repo>    # acepta el servicio y fija sus anclas",
				"statio init repo --target "+hostname+".<tailnet>.ts.net --service <svc> --image <repo>",
			)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&hostname, "hostname", "", "tsnet MagicDNS hostname (no-interactivo)")
	f.StringVar(&identity, "identity", "", "cosign certificate identity SAN (no-interactivo)")
	f.StringVar(&issuer, "issuer", "", "cosign OIDC issuer")
	f.StringVar(&configPath, "config", "/etc/statio/config.yaml", "config output path")
	f.BoolVar(&oauthStdin, "ts-oauth-secret-stdin", false, "leer el OAuth secret de stdin (no-interactivo)")
	f.StringVar(&oauthSecretFile, "ts-oauth-secret-file", "", "leer el OAuth secret de un archivo (no-interactivo)")
	return cmd
}

func writeServerFiles(hostname, issuer, identity, configPath, oauthSecret string) error {
	if err := os.MkdirAll("/etc/statio/secrets", 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return err
	}
	if err := fsutil.SecureWrite("/etc/statio/secrets/oauth", []byte(oauthSecret), 0o600); err != nil {
		return err
	}
	cfg := fmt.Sprintf(`hostname: %s
listen_port: 443
tailscale:
  oauth_file: /etc/statio/secrets/oauth
  tags: [tag:agent]
  state_dir: /var/lib/statio/tsnet
cosign:
  oidc_issuer: %s
  identity: %s
  require_tlog: true
  require_sct: true
registry:
  ghcr_auth_file: /etc/statio/secrets/ghcr.json
services_dir: /etc/statio/services
state_dir: /var/lib/statio
log_level: info
`, hostname, issuer, identity)
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
	var target, service, image, actionRef, out, statioOut string
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Generate the GitHub Actions workflow (interactive)",
		RunE: func(c *cobra.Command, _ []string) error {
			if interactive() && (target == "" || service == "" || image == "") {
				banner("statio · init repo", "Genera .github/workflows/deploy.yml")
				if err := runForm(
					inputField("Dirección del servidor (Tailscale)", "El nombre que le pusiste + .<tu-tailnet>.ts.net. Ej: statio.tu-org.ts.net", "statio.<tu-tailnet>.ts.net", &target, true),
					inputField("Service", "Nombre del servicio (debe existir en el agente)", "api", &service, true),
					inputField("Image repository", "Repo de la imagen", "ghcr.io/accentiostudios/api", &image, true),
				); err != nil {
					return err
				}
			} else if target == "" || service == "" || image == "" {
				return fmt.Errorf("modo no-interactivo: --target, --service y --image son requeridos")
			}
			yml, err := render("deploy.yml.tmpl", map[string]string{
				"Target": target, "Service": service, "Image": image, "ActionRef": actionRef,
			})
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

			// Also scaffold statio.yaml in the repo root (skip if it already exists).
			if _, err := os.Stat(statioOut); os.IsNotExist(err) {
				py, err := render("statio.yaml.tmpl", map[string]string{"Service": service})
				if err != nil {
					return err
				}
				if err := os.WriteFile(statioOut, py, 0o644); err != nil {
					return err
				}
				okLine("Generado %s (editá los servicios/env de tu app)", statioOut)
			} else {
				info("%s ya existe; no se tocó", statioOut)
			}

			sectionTitle("Configura estos GitHub secrets (print-to-paste — el CLI nunca maneja un PAT)")
			codeBlock(
				"gh secret set TS_OAUTH_CLIENT_ID     --body '<tailscale ci oauth client id>'",
				"gh secret set TS_OAUTH_CLIENT_SECRET --body '<tailscale ci oauth client secret>'",
			)
			info("El OAuth client de CI debe ser dueño de tag:ci (distinto del de tag:agent).")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&target, "target", "", "agent MagicDNS host (no-interactivo)")
	f.StringVar(&service, "service", "", "service name (no-interactivo)")
	f.StringVar(&image, "image", "", "image repository (no-interactivo)")
	f.StringVar(&actionRef, "action-ref", "accentiostudios/statio/action@v1", "the push composite action ref")
	f.StringVar(&out, "out", ".github/workflows/deploy.yml", "workflow output path")
	f.StringVar(&statioOut, "statio-out", "statio.yaml", "statio.yaml starter output path")
	return cmd
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

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/accentiostudios/statio/internal/fsutil"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// newAppCmd is the `statio app` group: accept and manage the apps allowed to deploy to this
// server. Each app pins its own image repo, its own cosign signer identity (so apps can come
// from different repos/orgs), and its domain allowlists. A signed deploy can only target an
// already-accepted app — standing one up is never a side effect of a payload (invariant #18).
func newAppCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "app", Short: "Manage the apps allowed to deploy to this server"}
	cmd.AddCommand(newAppAddCmd("add [name]", false), newAppListCmd(), newAppRmCmd())
	return cmd
}

// newEnableAliasCmd keeps `statio enable` working as a deprecated alias of `statio app add`.
func newEnableAliasCmd() *cobra.Command {
	c := newAppAddCmd("enable [name]", true)
	c.Hidden = true
	c.Deprecated = "use 'statio app add'"
	return c
}

func newAppAddCmd(use string, _ bool) *cobra.Command {
	var (
		image, servicesDir, stateDir, actionRef, target string
		registries                                      []string
		proxySuffixes, upstreams, dnsSuffixes           []string
		rollback                                        bool
		maxServices                                     int
		repoFlag, workflowFlag, branchFlag, issuer      string
	)
	cmd := &cobra.Command{
		Use:   use,
		Short: "Accept an app: pin its image repo, signer identity and domains",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if issuer == "" {
				issuer = "https://token.actions.githubusercontent.com"
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			// Build the signer identity from flags if given (non-interactive path).
			identity := ""
			if repoFlag != "" {
				owner, repo, err := parseOwnerRepo(repoFlag)
				if err != nil {
					return fmt.Errorf("--repo: %w", err)
				}
				identity = buildIdentity(owner, repo, workflowFlag, branchFlag)
			}

			if interactive() && (name == "" || image == "" || identity == "") {
				banner("statio · app add", "Acepta una app: fija su imagen, su repo firmante y sus dominios")
				registriesCSV := strings.Join(registries, ", ")
				var fields []huh.Field
				if name == "" {
					fields = append(fields, inputField("Nombre de la app", "El slot que CI despliega (ej. api). Letras, números, - o _", "api", &name, true))
				}
				fields = append(fields,
					inputField("Repositorio de la imagen", "El repo EXACTO de tu imagen; CI solo puede desplegar desde aquí (repo-equality). Ej: ghcr.io/tu-org/api", "ghcr.io/tu-org/api", &image, true),
					inputField("Registries permitidos (dependencias)", "Separados por coma. De dónde pueden salir postgres/redis/etc.", "docker.io, ghcr.io", &registriesCSV, true),
				)
				if err := runForm(fields...); err != nil {
					return err
				}
				registries = splitCSV(registriesCSV)

				sectionTitle("¿Quién puede desplegar esta app? (firma cosign)")
				info("El repo + workflow + rama de GitHub que firma los deploys de ESTA app. Cada app puede")
				info("venir de un repo u organización distinta — por eso se pregunta acá, por app.")
				repoInput, wf, branch := repoFlag, workflowFlag, branchFlag
				if wf == "" {
					wf = "deploy.yml"
				}
				if branch == "" {
					branch = "main"
				}
				repoField := huh.NewInput().
					Title("Repositorio de GitHub de esta app").
					Description("owner/repo (no solo la organización). Ej: accentiostudios/api. También puedes pegar la URL.").
					Placeholder("accentiostudios/api").
					Value(&repoInput).
					Validate(func(s string) error {
						if _, _, err := parseOwnerRepo(s); err != nil {
							return fmt.Errorf("escribe owner/repo (ej: accentiostudios/api), no solo la organización")
						}
						return nil
					})
				if err := runForm(repoField); err != nil {
					return err
				}
				if err := runForm(
					inputField("Archivo del workflow", "El .yml en .github/workflows/ del repo que despliega. El que genera 'statio init repo' es deploy.yml.", "deploy.yml", &wf, true),
					inputField("Rama autorizada", "Solo se aceptan deploys desde esta rama del repo. Normalmente main.", "main", &branch, true),
				); err != nil {
					return err
				}
				owner, repo, err := parseOwnerRepo(repoInput)
				if err != nil {
					return fmt.Errorf("repositorio inválido: %w", err)
				}
				identity = buildIdentity(owner, repo, trimmed(wf), trimmed(branch))

				wantDomain, err := confirm("¿Exponer un dominio público (reverse proxy + DNS)?")
				if err != nil {
					return err
				}
				if wantDomain {
					suffix, upstream := "", name
					if err := runForm(
						inputField("Sufijo de dominio permitido", "Solo se aceptan dominios bajo este sufijo (anti-hijack). Ej: example.com", "example.com", &suffix, true),
						inputField("Upstream (contenedor destino)", "El servicio al que apunta el proxy", name, &upstream, true),
					); err != nil {
						return err
					}
					proxySuffixes, dnsSuffixes, upstreams = []string{suffix}, []string{suffix}, []string{upstream}
				}
			}

			if name == "" {
				return fmt.Errorf("falta el nombre de la app (ej. statio app add api)")
			}
			if !validServiceName(name) {
				return fmt.Errorf("nombre de app inválido %q", name)
			}
			if image == "" {
				return fmt.Errorf("--image (repo de tu imagen) es requerido")
			}
			if identity == "" {
				return fmt.Errorf("--repo (identidad firmante de la app) es requerido")
			}

			dir := filepath.Join(servicesDir, name)
			if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
				return err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "apiVersion: statio/v1\nkind: ServiceDeploy\nname: %s\n", name)
			fmt.Fprintf(&b, "signer:\n  oidc_issuer: %s\n  identity: %s\n", issuer, identity)
			fmt.Fprintf(&b, "image:\n  repository: %s\n", image)
			fmt.Fprintf(&b, "max_services: %d\n", maxServices)
			writeList(&b, "registries", registries)
			fmt.Fprintf(&b, "proxy:\n")
			writeListIndented(&b, "allowed_domain_suffixes", proxySuffixes)
			writeListIndented(&b, "allowed_upstream_hosts", upstreams)
			fmt.Fprintf(&b, "dns:\n")
			writeListIndented(&b, "allowed_domain_suffixes", dnsSuffixes)
			fmt.Fprintf(&b, "rollback:\n  enabled: %v\n  env_policy: with-digest\n", rollback)

			path := filepath.Join(dir, "manifest.yaml")
			if err := fsutil.SecureWrite(path, []byte(b.String()), 0o600); err != nil {
				return err
			}
			okLine("App %q aceptada: %s", name, path)
			info("repo de imagen:   %s", image)
			info("identidad firma:  %s", identity)

			if target == "" {
				target = readAudience(stateDir)
			}
			sectionTitle("En tu repo 💻 — agrega este step a tu workflow")
			printSnippet(targetOrPlaceholder(target), name, image, actionRef)
			sectionTitle("GitHub secrets 💻 (desde tu máquina)")
			codeBlock(
				"gh secret set STATIO_TS_AUTHKEY --body '<la auth key que imprimió statio init server>'",
				"gh secret set DATABASE_URL      --body '<el valor de cada env que pide tu statio.yaml>'",
			)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&image, "image", "", "el repo de tu imagen (ancla repo-equality)")
	f.StringVar(&repoFlag, "repo", "", "owner/repo o URL — identidad firmante de esta app")
	f.StringVar(&workflowFlag, "workflow", "deploy.yml", "archivo del workflow (con --repo)")
	f.StringVar(&branchFlag, "branch", "main", "rama autorizada (con --repo)")
	f.StringVar(&issuer, "issuer", "", "cosign OIDC issuer")
	f.StringSliceVar(&registries, "registries", []string{"docker.io", "ghcr.io"}, "registries permitidos para dependencias")
	f.StringSliceVar(&proxySuffixes, "proxy-domain-suffix", nil, "sufijos de dominio permitidos (reverse-proxy)")
	f.StringSliceVar(&upstreams, "proxy-upstream", nil, "contenedores upstream permitidos")
	f.StringSliceVar(&dnsSuffixes, "dns-domain-suffix", nil, "sufijos de dominio permitidos (DNS)")
	f.BoolVar(&rollback, "rollback", true, "rollback automático si falla el health")
	f.IntVar(&maxServices, "max-services", 10, "cap de servicios en un deploy")
	f.StringVar(&servicesDir, "services-dir", "/etc/statio/services", "directorio de servicios")
	f.StringVar(&stateDir, "state-dir", "/var/lib/statio", "directorio de estado (para resolver el target)")
	f.StringVar(&target, "target", "", "MagicDNS del agente para el snippet (default: el detectado)")
	f.StringVar(&actionRef, "action-ref", "accentiostudios/statio@v1", "ref del Action de statio (Marketplace)")
	return cmd
}

func newAppListCmd() *cobra.Command {
	var servicesDir string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the apps accepted on this server",
		RunE: func(c *cobra.Command, _ []string) error {
			entries, err := os.ReadDir(servicesDir)
			if err != nil {
				if os.IsNotExist(err) {
					info("No hay apps aceptadas todavía (corre 'statio app add').")
					return nil
				}
				return err
			}
			found := false
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				if _, err := os.Stat(filepath.Join(servicesDir, e.Name(), "manifest.yaml")); err != nil {
					continue
				}
				found = true
				okLine("%s", e.Name())
			}
			if !found {
				info("No hay apps aceptadas todavía (corre 'statio app add').")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&servicesDir, "services-dir", "/etc/statio/services", "directorio de servicios")
	return cmd
}

func newAppRmCmd() *cobra.Command {
	var servicesDir string
	var yes bool
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove an accepted app (stops accepting its deploys)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			if !validServiceName(name) {
				return fmt.Errorf("nombre de app inválido %q", name)
			}
			dir := filepath.Join(servicesDir, name)
			if _, err := os.Stat(filepath.Join(dir, "manifest.yaml")); err != nil {
				return fmt.Errorf("la app %q no está aceptada", name)
			}
			if !yes && interactive() {
				ok, err := confirm(fmt.Sprintf("¿Quitar la app %q? Dejará de aceptar sus deploys.", name))
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
			}
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			okLine("App %q quitada.", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&servicesDir, "services-dir", "/etc/statio/services", "directorio de servicios")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "no preguntar confirmación")
	return cmd
}

func readAudience(stateDir string) string {
	b, err := os.ReadFile(filepath.Join(stateDir, "audience"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func targetOrPlaceholder(t string) string {
	if t == "" {
		return "statio.<tu-tailnet>.ts.net"
	}
	return t
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func validServiceName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i, r := range s {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if i == 0 && !isAlnum {
			return false
		}
		if !isAlnum && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func writeList(b *strings.Builder, key string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "%s: [%s]\n", key, strings.Join(items, ", "))
}

func writeListIndented(b *strings.Builder, key string, items []string) {
	if len(items) == 0 {
		fmt.Fprintf(b, "  %s: []\n", key)
		return
	}
	fmt.Fprintf(b, "  %s: [%s]\n", key, strings.Join(items, ", "))
}

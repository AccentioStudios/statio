package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/accentiostudios/statio/internal/fsutil"
	"github.com/spf13/cobra"
)

// newEnableCmd implements `statio enable <service>` — the explicit ops act of ACCEPTING a
// service on this server and pinning its security anchors (allowed repo, dependency
// registries, proxy/dns allowlists). A signed deploy can only target an already-accepted
// service; standing up a new service is never a side effect of a payload (invariant #18).
func newEnableCmd() *cobra.Command {
	var (
		image, servicesDir string
		registries         []string
		proxySuffixes      []string
		upstreams          []string
		dnsSuffixes        []string
		rollback           bool
		maxServices        int
	)
	cmd := &cobra.Command{
		Use:   "enable [service]",
		Short: "Accept a service on this server and pin its anchors (ops action)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}

			// Interactive wizard when run without the essentials in a terminal.
			if interactive() && (name == "" || image == "") {
				banner("statio · enable", "Acepta un servicio en este servidor y fija sus anclas de seguridad")
				registriesCSV := strings.Join(registries, ", ")
				if err := runForm(
					inputField("Nombre del servicio", "El slot que CI va a desplegar (ej. api). Letras, números, - o _", "api", &name, true),
					inputField("Repositorio de la imagen", "El repo EXACTO de tu app; CI solo puede desplegar desde aquí (repo-equality). Ej: ghcr.io/tu-org/api", "ghcr.io/tu-org/api", &image, true),
					inputField("Registries permitidos (dependencias)", "Separados por coma. De dónde pueden salir postgres/redis/etc.", "docker.io, ghcr.io", &registriesCSV, true),
				); err != nil {
					return err
				}
				registries = splitCSV(registriesCSV)

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
				return fmt.Errorf("falta el nombre del servicio (ej. statio enable api)")
			}
			if !validServiceName(name) {
				return fmt.Errorf("invalid service name %q", name)
			}
			if image == "" {
				return fmt.Errorf("--image (your app's pinned repository) is required")
			}
			dir := filepath.Join(servicesDir, name)
			if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
				return err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "apiVersion: statio/v1\nkind: ServiceDeploy\nname: %s\n", name)
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
			okLine("Servicio %q habilitado: %s", name, path)
			info("repo pinneado:   %s", image)
			info("registries dep:  %s", strings.Join(registries, ", "))
			sectionTitle("Próximos pasos")
			codeBlock(
				"statio env set "+name+" DATABASE_URL --secret-stdin   # secretos solo de ops (opcional)",
				"# en el repo: statio init repo  → genera statio.yaml + el workflow",
			)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&image, "image", "", "your app's pinned image repository (repo-equality anchor)")
	f.StringSliceVar(&registries, "registries", []string{"docker.io", "ghcr.io"}, "allowed dependency registries")
	f.StringSliceVar(&proxySuffixes, "proxy-domain-suffix", nil, "allowed reverse-proxy domain suffixes")
	f.StringSliceVar(&upstreams, "proxy-upstream", nil, "allowed upstream container names")
	f.StringSliceVar(&dnsSuffixes, "dns-domain-suffix", nil, "allowed DNS domain suffixes")
	f.BoolVar(&rollback, "rollback", true, "enable automatic rollback on failed health")
	f.IntVar(&maxServices, "max-services", 10, "cap on services in a deploy")
	f.StringVar(&servicesDir, "services-dir", "/etc/statio/services", "services directory")
	return cmd
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

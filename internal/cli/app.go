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
		Use:     use,
		Short:   "Accept an app: pin its image repo, signer identity and domains",
		Args:    cobra.MaximumNArgs(1),
		PreRunE: rootPreRun,
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
				banner("statio · app add", "Accept an app: pin its image, its signer repo and its domains")
				registriesCSV := strings.Join(registries, ", ")
				var fields []huh.Field
				if name == "" {
					fields = append(fields, inputField("App name", "The slot CI deploys to (e.g. api). Letters, numbers, - or _", "api", &name, true))
				}
				fields = append(fields,
					inputField("Image repository", "The EXACT repo of your image; CI can only deploy from here (repo-equality). E.g. ghcr.io/your-org/api", "ghcr.io/your-org/api", &image, true),
					inputField("Allowed registries (dependencies)", "Comma-separated. Where postgres/redis/etc. may come from.", "docker.io, ghcr.io", &registriesCSV, true),
				)
				if err := runForm(fields...); err != nil {
					return err
				}
				registries = splitCSV(registriesCSV)

				sectionTitle("Who can deploy this app? (cosign signing)")
				info("The GitHub repo + workflow + branch that signs THIS app's deploys. Each app can")
				info("come from a different repo or organization — that's why it's asked here, per app.")
				repoInput, wf, branch := repoFlag, workflowFlag, branchFlag
				if wf == "" {
					wf = "deploy.yml"
				}
				if branch == "" {
					branch = "main"
				}
				repoField := huh.NewInput().
					Title("This app's GitHub repository").
					Description("owner/repo (not just the organization). E.g. accentiostudios/api. You can also paste the URL.").
					Placeholder("accentiostudios/api").
					Value(&repoInput).
					Validate(func(s string) error {
						if _, _, err := parseOwnerRepo(s); err != nil {
							return fmt.Errorf("enter owner/repo (e.g. accentiostudios/api), not just the organization")
						}
						return nil
					})
				if err := runForm(repoField); err != nil {
					return err
				}
				if err := runForm(
					inputField("Workflow file", "The .yml in .github/workflows/ of the deploying repo. The one 'statio init repo' generates is deploy.yml.", "deploy.yml", &wf, true),
					inputField("Authorized branch", "Only deploys from this branch of the repo are accepted. Usually main.", "main", &branch, true),
				); err != nil {
					return err
				}
				owner, repo, err := parseOwnerRepo(repoInput)
				if err != nil {
					return fmt.Errorf("invalid repository: %w", err)
				}
				identity = buildIdentity(owner, repo, trimmed(wf), trimmed(branch))

				wantDomain, err := confirm("Expose a public domain (reverse proxy + DNS)?")
				if err != nil {
					return err
				}
				if wantDomain {
					suffix, upstream := "", name
					if err := runForm(
						inputField("Allowed domain suffix", "Only domains under this suffix are accepted (anti-hijack). E.g. example.com", "example.com", &suffix, true),
						inputField("Upstream (target container)", "The service the proxy points to", name, &upstream, true),
					); err != nil {
						return err
					}
					proxySuffixes, dnsSuffixes, upstreams = []string{suffix}, []string{suffix}, []string{upstream}
				}
			}

			if name == "" {
				return fmt.Errorf("missing app name (e.g. statio app add api)")
			}
			if !validServiceName(name) {
				return fmt.Errorf("invalid app name %q", name)
			}
			if image == "" {
				return fmt.Errorf("--image (your image repo) is required")
			}
			if identity == "" {
				return fmt.Errorf("--repo (the app's signing identity) is required")
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
			okLine("App %q accepted: %s", name, path)
			info("image repo:       %s", image)
			info("signing identity: %s", identity)

			if target == "" {
				target = readAudience(stateDir)
			}
			sectionTitle("In your repo 💻 — add this step to your workflow")
			printSnippet(targetOrPlaceholder(target), name, image, actionRef)
			sectionTitle("GitHub secrets 💻 — on YOUR machine (gh logged in), not this server")
			info("Use --repo so you needn't be inside the repo (or drop it and run from within it):")
			codeBlock(
				"gh secret set STATIO_TS_AUTHKEY --repo <owner>/<repo> --body '<the key statio init server printed>'",
				"gh secret set DATABASE_URL      --repo <owner>/<repo> --body '<value for each env in your statio.yaml>'",
			)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&image, "image", "", "your image repo (repo-equality anchor)")
	f.StringVar(&repoFlag, "repo", "", "owner/repo or URL — this app's signing identity")
	f.StringVar(&workflowFlag, "workflow", "deploy.yml", "workflow file (with --repo)")
	f.StringVar(&branchFlag, "branch", "main", "authorized branch (with --repo)")
	f.StringVar(&issuer, "issuer", "", "cosign OIDC issuer")
	f.StringSliceVar(&registries, "registries", []string{"docker.io", "ghcr.io"}, "allowed registries for dependencies")
	f.StringSliceVar(&proxySuffixes, "proxy-domain-suffix", nil, "allowed domain suffixes (reverse-proxy)")
	f.StringSliceVar(&upstreams, "proxy-upstream", nil, "allowed upstream containers")
	f.StringSliceVar(&dnsSuffixes, "dns-domain-suffix", nil, "allowed domain suffixes (DNS)")
	f.BoolVar(&rollback, "rollback", true, "automatic rollback if health fails")
	f.IntVar(&maxServices, "max-services", 10, "cap on services in a deploy")
	f.StringVar(&servicesDir, "services-dir", "/etc/statio/services", "services directory")
	f.StringVar(&stateDir, "state-dir", "/var/lib/statio", "state directory (to resolve the target)")
	f.StringVar(&target, "target", "", "agent MagicDNS for the snippet (default: the detected one)")
	f.StringVar(&actionRef, "action-ref", "accentiostudios/statio@v1", "ref of the statio Action (Marketplace)")
	return cmd
}

func newAppListCmd() *cobra.Command {
	var servicesDir string
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List the apps accepted on this server",
		PreRunE: rootPreRun,
		RunE: func(c *cobra.Command, _ []string) error {
			entries, err := os.ReadDir(servicesDir)
			if err != nil {
				if os.IsNotExist(err) {
					info("No apps accepted yet (run 'statio app add').")
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
				info("No apps accepted yet (run 'statio app add').")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&servicesDir, "services-dir", "/etc/statio/services", "services directory")
	return cmd
}

func newAppRmCmd() *cobra.Command {
	var servicesDir string
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <name>",
		Short:   "Remove an accepted app (stops accepting its deploys)",
		Args:    cobra.ExactArgs(1),
		PreRunE: rootPreRun,
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			if !validServiceName(name) {
				return fmt.Errorf("invalid app name %q", name)
			}
			dir := filepath.Join(servicesDir, name)
			if _, err := os.Stat(filepath.Join(dir, "manifest.yaml")); err != nil {
				return fmt.Errorf("app %q is not accepted", name)
			}
			if !yes && interactive() {
				ok, err := confirm(fmt.Sprintf("Remove app %q? It will stop accepting its deploys.", name))
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
			okLine("App %q removed.", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&servicesDir, "services-dir", "/etc/statio/services", "services directory")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
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
		return "statio.<your-tailnet>.ts.net"
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

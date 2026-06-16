package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
			// app add only makes sense once the agent is configured — refuse before init server.
			if _, err := os.Stat(filepath.Join(filepath.Dir(servicesDir), "config.yaml")); err != nil {
				return fmt.Errorf("this server isn't set up yet — run 'sudo statio init server' first")
			}
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
				if name == "" {
					if err := runForm(inputField("App name", "The slot CI deploys to (e.g. api). Letters, numbers, - or _", "api", &name, true)); err != nil {
						return err
					}
				}

				// 1. Ask the signer source repo FIRST — detecting its visibility lets us pre-fill the
				//    branch and infer the image path instead of asking for them blind.
				sectionTitle("Who can deploy this app? (cosign signing)")
				info("The GitHub repo + workflow + branch that signs THIS app's deploys. Each app can")
				info("come from a different repo or organization — that's why it's asked here, per app.")
				repoInput, wf, branch := repoFlag, workflowFlag, branchFlag
				if wf == "" {
					wf = "deploy.yml"
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
				owner, repo, err := parseOwnerRepo(repoInput)
				if err != nil {
					return fmt.Errorf("invalid repository: %w", err)
				}

				// Best-effort detection: public (no auth) → private (gh) → fall back to manual.
				ictx, cancel := context.WithTimeout(c.Context(), 12*time.Second)
				ri := inspectGitHubRepo(ictx, owner, repo)
				cancel()
				switch {
				case ri.Known && ri.Private:
					okLine("Repo %s/%s: PRIVATE (via %s), default branch %q", owner, repo, ri.Source, ri.DefaultBranch)
					info("Private image → run 'docker login ghcr.io' on this server so the agent can pull it.")
				case ri.Known:
					okLine("Repo %s/%s: PUBLIC, default branch %q", owner, repo, ri.DefaultBranch)
				default:
					warnLine("Couldn't auto-detect the repo: %s", ri.Note)
				}
				if branch == "" {
					if branch = ri.DefaultBranch; branch == "" {
						branch = "main"
					}
				}

				// 2. Workflow + branch (the branch is pre-filled with the detected default).
				if err := runForm(
					inputField("Workflow file", "Exact file name of the workflow that builds & signs this app, under .github/workflows/ (just the name, not the path). 'statio init repo' creates deploy.yml; if your CI file has another name, use that.", "deploy.yml", &wf, true),
					inputField("Authorized branch", "Only deploys from this branch of the repo are accepted.", "main", &branch, true),
				); err != nil {
					return err
				}
				identity = buildIdentity(owner, repo, trimmed(wf), trimmed(branch))

				// 3. Image repo — infer GHCR under the same repo, or paste another registry.
				if image == "" {
					useGHCR := true
					if err := huh.NewForm(huh.NewGroup(huh.NewConfirm().
						Title(fmt.Sprintf("Image on GitHub Container Registry under this repo? (%s)", ghcrImage(owner, repo))).
						Description("Yes = GHCR in the same repo (inferred). No = paste a Docker Hub / other registry path.").
						Affirmative("Yes, use ghcr").
						Negative("No, paste another").
						Value(&useGHCR))).WithTheme(huh.ThemeCharm()).Run(); err != nil {
						return err
					}
					if useGHCR {
						image = ghcrImage(owner, repo)
						info("Image repository: %s (needn't exist yet — your CI pushes it)", image)
					} else if err := runForm(inputField("Image repository",
						"Where your CI pushes the image (needn't exist yet). E.g. docker.io/your-org/api",
						"ghcr.io/your-org/api", &image, true)); err != nil {
						return err
					}
				}

				// 4. Dependency containers (Postgres/Redis/…) are optional — only ask for their
				//    registry allowlist when the app actually has sidecars, so a single-container
				//    app doesn't get a registry question right after setting its own image.
				hasDeps, err := confirm("Does this app run extra containers (Postgres, Redis, …) defined in its statio.yaml?")
				if err != nil {
					return err
				}
				if hasDeps {
					registriesCSV := strings.Join(registries, ", ")
					if err := runForm(inputField("Registries those containers may come from",
						"Security allowlist: only these registries can supply the DEPENDENCY images (not your app). Comma-separated. E.g. docker.io, ghcr.io",
						"docker.io, ghcr.io", &registriesCSV, true)); err != nil {
						return err
					}
					registries = splitCSV(registriesCSV)
				}

				// 5. Optional public domain.
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
			if target == "" {
				info("(Couldn't read the agent's address yet — it appears once the agent finishes joining")
				info(" the tailnet. Get it with 'statio status' and replace the target placeholder above.)")
			}
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

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/accentiostudios/statio/internal/deploy"
	"github.com/accentiostudios/statio/internal/fsutil"
	"github.com/accentiostudios/statio/internal/spec"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// newAppCmd is the `statio app` group: accept and manage the apps allowed to deploy to this
// server. Each app pins its own image repo, its own cosign signer identity (so apps can come
// from different repos/orgs), and its domain allowlists. A signed deploy can only target an
// already-accepted app — standing one up is never a side effect of a payload (invariant #18).
func newAppCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "app", Short: "Manage the apps allowed to deploy to this server"}
	cmd.AddCommand(newAppAddCmd("add [name]", false), newAppListCmd(), newAppEditCmd(), newAppRmCmd())
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
					if err := runForm(serviceNameField("App name", "The slot CI deploys to (e.g. api). Lowercase letters, digits and dashes — no underscores.", "api", &name)); err != nil {
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
						serviceNameField("Upstream (target container)", "The service the proxy points to (a service name — usually this app).", name, &upstream),
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
				return fmt.Errorf("invalid app name %q: use lowercase letters, digits and dashes, start with a letter, max 31 chars — no underscores (e.g. %s)", name, strings.ReplaceAll(name, "_", "-"))
			}
			if image == "" {
				return fmt.Errorf("--image (your image repo) is required")
			}
			if identity == "" {
				return fmt.Errorf("--repo (the app's signing identity) is required")
			}

			path, err := writeAppManifest(servicesDir, appManifest{
				name: name, issuer: issuer, identity: identity, image: image,
				registries: registries, maxServices: maxServices,
				proxySuffixes: proxySuffixes, upstreams: upstreams, dnsSuffixes: dnsSuffixes,
				rollback: rollback,
			})
			if err != nil {
				return err
			}
			okLine("App %q accepted: %s", name, path)
			info("image repo:       %s", image)
			info("signing identity: %s", identity)

			if target == "" {
				target = readAudience(stateDir)
			}
			sectionTitle("In your repo 💻 — two things CI needs (run `statio init repo` to scaffold both)")
			info("1) statio.yaml at the repo ROOT — the deploy reads it; without it CI fails with")
			info("   `open statio.yaml: no such file`. Minimal for %q (declare your real ports + env):", name)
			codeBlock(
				"services:",
				"  - name: "+name+"          # must match this app",
				"    ports: [3000]            # the port your app listens on",
				"    env: [DATABASE_URL]      # NAMES only; values come from the workflow env: below",
			)
			info("2) the workflow step (this is also what `statio init repo` writes):")
			printSnippet(targetOrPlaceholder(target), name, image, actionRef)
			if target == "" {
				info("(Couldn't read the agent's address yet — it appears once the agent finishes joining")
				info(" the tailnet. Get it with 'statio status' and replace the target placeholder above.)")
			}

			// STATIO_TS_AUTHKEY is the single shared key `statio init server` minted; we don't
			// re-mint per app. Show the repo filled in so the command is copy-paste ready.
			printAuthKeySecret(repoFromIdentity(identity), "")
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
	var servicesDir, stateDir, actionRef string
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List the accepted apps; pick one to view or edit its config",
		PreRunE: rootPreRun,
		RunE: func(c *cobra.Command, _ []string) error {
			names, err := listAcceptedApps(servicesDir)
			if err != nil {
				return err
			}
			if len(names) == 0 {
				info("No apps accepted yet (run 'statio app add').")
				return nil
			}
			for _, n := range names {
				okLine("%s", n)
			}
			if !interactive() {
				return nil
			}

			// Pick an app, then choose to view or edit it.
			choice := ""
			opts := make([]huh.Option[string], 0, len(names)+1)
			for _, n := range names {
				opts = append(opts, huh.NewOption(n, n))
			}
			opts = append(opts, huh.NewOption("(exit)", ""))
			if err := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().
				Title("Pick an app").Options(opts...).Value(&choice))).
				WithTheme(huh.ThemeCharm()).Run(); err != nil {
				return err
			}
			if choice == "" {
				return nil
			}
			action := "view"
			if err := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().
				Title(fmt.Sprintf("%s — view or edit?", choice)).
				Options(
					huh.NewOption("View config + setup steps", "view"),
					huh.NewOption("Edit config (re-run the wizard)", "edit"),
				).Value(&action))).WithTheme(huh.ThemeCharm()).Run(); err != nil {
				return err
			}
			if action == "edit" {
				return editAppInteractive(servicesDir, stateDir, actionRef, choice)
			}
			return showAppDetails(servicesDir, stateDir, actionRef, choice)
		},
	}
	f := cmd.Flags()
	f.StringVar(&servicesDir, "services-dir", "/etc/statio/services", "services directory")
	f.StringVar(&stateDir, "state-dir", "/var/lib/statio", "state directory (to resolve the target)")
	f.StringVar(&actionRef, "action-ref", "accentiostudios/statio@v1", "ref of the statio Action (Marketplace)")
	return cmd
}

// listAcceptedApps returns the names of all apps with a manifest under servicesDir.
func listAcceptedApps(servicesDir string) ([]string, error) {
	entries, err := os.ReadDir(servicesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(servicesDir, e.Name(), "manifest.yaml")); err == nil {
			names = append(names, e.Name())
		}
	}
	return names, nil
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
			// No name-format check here: rm must be able to remove a legacy app whose name would
			// no longer pass `app add` (e.g. one accepted before the rule was tightened). The
			// manifest-exists check below is the real guard.
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

// appManifest is the set of fields `app add` / `app edit` write to a service manifest.
type appManifest struct {
	name, issuer, identity, image         string
	registries                            []string
	maxServices                           int
	proxySuffixes, upstreams, dnsSuffixes []string
	rollback                              bool
}

// writeAppManifest renders and writes a service manifest. Shared by `app add` and `app edit` so
// both produce byte-identical files.
func writeAppManifest(servicesDir string, m appManifest) (string, error) {
	dir := filepath.Join(servicesDir, m.name)
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: statio/v1\nkind: ServiceDeploy\nname: %s\n", m.name)
	fmt.Fprintf(&b, "signer:\n  oidc_issuer: %s\n  identity: %s\n", m.issuer, m.identity)
	fmt.Fprintf(&b, "image:\n  repository: %s\n", m.image)
	fmt.Fprintf(&b, "max_services: %d\n", m.maxServices)
	writeList(&b, "registries", m.registries)
	fmt.Fprintf(&b, "proxy:\n")
	writeListIndented(&b, "allowed_domain_suffixes", m.proxySuffixes)
	writeListIndented(&b, "allowed_upstream_hosts", m.upstreams)
	fmt.Fprintf(&b, "dns:\n")
	writeListIndented(&b, "allowed_domain_suffixes", m.dnsSuffixes)
	fmt.Fprintf(&b, "rollback:\n  enabled: %v\n  env_policy: with-digest\n", m.rollback)

	path := filepath.Join(dir, "manifest.yaml")
	if err := fsutil.SecureWrite(path, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// parseSignerIdentity reverses buildIdentity: it pulls owner/repo/workflow/branch back out of a
// stored cosign identity URL so `app edit` can pre-fill the wizard with the current values.
func parseSignerIdentity(id string) (owner, repo, workflow, branch string, ok bool) {
	s := strings.TrimPrefix(id, "https://github.com/")
	if s == id {
		return
	}
	at := strings.LastIndex(s, "@refs/heads/")
	if at < 0 {
		return
	}
	branch = s[at+len("@refs/heads/"):]
	left := s[:at] // owner/repo/.github/workflows/<file>
	const marker = "/.github/workflows/"
	mi := strings.Index(left, marker)
	if mi < 0 {
		return
	}
	workflow = left[mi+len(marker):]
	parts := strings.SplitN(left[:mi], "/", 2)
	if len(parts) != 2 {
		return
	}
	owner, repo = parts[0], parts[1]
	ok = owner != "" && repo != "" && workflow != "" && branch != ""
	return
}

// loadAppSeed reads an accepted app's manifest into the editable field set.
func loadAppSeed(servicesDir, name string) (appManifest, error) {
	m, err := deploy.LoadManifest(filepath.Join(servicesDir, name))
	if err != nil {
		return appManifest{}, err
	}
	seed := appManifest{
		name:          m.Name,
		image:         m.Image.Repository,
		registries:    m.Registries,
		maxServices:   m.MaxServices,
		proxySuffixes: m.Proxy.AllowedDomainSuffixes,
		upstreams:     m.Proxy.AllowedUpstreamHosts,
		dnsSuffixes:   m.DNS.AllowedDomainSuffixes,
		rollback:      m.Rollback.Enabled,
	}
	if m.Signer != nil {
		seed.issuer = m.Signer.OIDCIssuer
		seed.identity = m.Signer.Identity
	}
	return seed, nil
}

// showAppDetails prints an accepted app's config and reprints its setup steps (the workflow
// snippet + the GitHub secrets), so the operator can review everything without re-running add.
func showAppDetails(servicesDir, stateDir, actionRef, name string) error {
	seed, err := loadAppSeed(servicesDir, name)
	if err != nil {
		return err
	}
	sectionTitle(fmt.Sprintf("App: %s", name))
	info("image repo:       %s", seed.image)
	info("signing identity: %s", seed.identity)
	if owner, repo, wf, branch, ok := parseSignerIdentity(seed.identity); ok {
		info("  repo %s/%s · workflow %s · branch %s", owner, repo, wf, branch)
	}
	info("dep registries:   %s", strings.Join(seed.registries, ", "))
	if len(seed.proxySuffixes) > 0 || len(seed.dnsSuffixes) > 0 {
		info("proxy domains:    %s", strings.Join(seed.proxySuffixes, ", "))
		info("dns domains:      %s", strings.Join(seed.dnsSuffixes, ", "))
	}

	target := readAudience(stateDir)
	sectionTitle("In your repo 💻 — the workflow step")
	printSnippet(targetOrPlaceholder(target), name, seed.image, actionRef)
	// STATIO_TS_AUTHKEY is the shared key from `init server` (not stored here); show the repo filled.
	printAuthKeySecret(repoFromIdentity(seed.identity), "")
	return nil
}

// editAppInteractive re-runs the wizard for an accepted app with its current values pre-filled,
// and rewrites the manifest. The app can also be renamed (the service dir is moved, preserving its
// secrets). Shared by `app edit` and `app list` → Edit.
func editAppInteractive(servicesDir, stateDir, actionRef, name string) error {
	if !interactive() {
		return fmt.Errorf("app edit is interactive; run it in a terminal")
	}
	seed, err := loadAppSeed(servicesDir, name)
	if err != nil {
		return err
	}
	banner("statio · app edit", fmt.Sprintf("Edit %s — current values are pre-filled; change what you need", name))

	// Allow renaming the slot (validated like any service name). The rename itself is applied
	// below, just before writing — after a warning + confirm.
	newName := name
	if err := runForm(serviceNameField(
		"App name",
		"Rename the slot, or keep it. If changed, your workflow `service:` and statio.yaml `name:` must match the new value.",
		name, &newName)); err != nil {
		return err
	}
	newName = strings.TrimSpace(newName)

	issuer := seed.issuer
	if issuer == "" {
		issuer = "https://token.actions.githubusercontent.com"
	}
	repoInput, wf, branch := "", "deploy.yml", "main"
	if owner, repo, w, b, ok := parseSignerIdentity(seed.identity); ok {
		repoInput, wf, branch = owner+"/"+repo, w, b
	}
	repoField := huh.NewInput().
		Title("This app's GitHub repository").
		Description("owner/repo (the repo that signs this app's deploys)").
		Placeholder("accentiostudios/api").
		Value(&repoInput).
		Validate(func(s string) error {
			if _, _, err := parseOwnerRepo(s); err != nil {
				return fmt.Errorf("enter owner/repo (e.g. accentiostudios/api)")
			}
			return nil
		})
	if err := runForm(repoField); err != nil {
		return err
	}
	if err := runForm(
		inputField("Workflow file", "Exact file name of the workflow that builds & signs this app (under .github/workflows/).", "deploy.yml", &wf, true),
		inputField("Authorized branch", "Only deploys from this branch are accepted.", "main", &branch, true),
	); err != nil {
		return err
	}
	owner, repo, err := parseOwnerRepo(repoInput)
	if err != nil {
		return fmt.Errorf("invalid repository: %w", err)
	}
	identity := buildIdentity(owner, repo, trimmed(wf), trimmed(branch))

	image := seed.image
	if err := runForm(inputField("Image repository", "Where your CI pushes the image (the action builds+pushes here).", "ghcr.io/your-org/api", &image, true)); err != nil {
		return err
	}

	registriesCSV := strings.Join(seed.registries, ", ")
	if err := runForm(inputField("Allowed registries (dependencies)", "Comma-separated allowlist for sidecar images (postgres/redis/…).", "docker.io, ghcr.io", &registriesCSV, true)); err != nil {
		return err
	}
	registries := splitCSV(registriesCSV)

	proxySuffixes, dnsSuffixes, upstreams := seed.proxySuffixes, seed.dnsSuffixes, seed.upstreams
	wantDomain, err := confirm("Expose a public domain (reverse proxy + DNS)?")
	if err != nil {
		return err
	}
	if wantDomain {
		suffix, upstream := "", name
		if len(seed.proxySuffixes) > 0 {
			suffix = seed.proxySuffixes[0]
		}
		if len(seed.upstreams) > 0 {
			upstream = seed.upstreams[0]
		}
		if err := runForm(
			inputField("Allowed domain suffix", "Only domains under this suffix are accepted. E.g. example.com", "example.com", &suffix, true),
			serviceNameField("Upstream (target container)", "The service the proxy points to (a service name — usually this app).", name, &upstream),
		); err != nil {
			return err
		}
		proxySuffixes, dnsSuffixes, upstreams = []string{suffix}, []string{suffix}, []string{upstream}
	} else {
		proxySuffixes, dnsSuffixes, upstreams = nil, nil, nil
	}

	// Apply a rename by moving the whole service dir, which preserves env secrets under
	// <dir>/secrets/. Refuse to clobber a different app; warn + confirm because the old slot
	// stops being accepted and the repo's service:/name: must be updated to match.
	if newName != name {
		newDir := filepath.Join(servicesDir, newName)
		if _, err := os.Stat(newDir); err == nil {
			return fmt.Errorf("can't rename to %q: an app with that name already exists", newName)
		}
		warnLine("Renaming %q → %q changes the deploy slot:", name, newName)
		info("  • update your workflow `service: %s` and statio.yaml `name: %s` to match", newName, newName)
		info("  • the old name %q stops being accepted", name)
		info("  • env set with `statio env` moves with it; GitHub secrets/vars are unaffected")
		ok, err := confirm("Proceed with the rename?")
		if err != nil {
			return err
		}
		if ok {
			if err := os.Rename(filepath.Join(servicesDir, name), newDir); err != nil {
				return fmt.Errorf("rename app dir: %w", err)
			}
			name = newName
		}
	}

	path, err := writeAppManifest(servicesDir, appManifest{
		name: name, issuer: issuer, identity: identity, image: image,
		registries: registries, maxServices: seed.maxServices,
		proxySuffixes: proxySuffixes, upstreams: upstreams, dnsSuffixes: dnsSuffixes,
		rollback: seed.rollback,
	})
	if err != nil {
		return err
	}
	okLine("App %q updated: %s", name, path)
	return showAppDetails(servicesDir, stateDir, actionRef, name)
}

func newAppEditCmd() *cobra.Command {
	var servicesDir, stateDir, actionRef string
	cmd := &cobra.Command{
		Use:     "edit <name>",
		Short:   "Re-run the wizard to change an accepted app's config",
		Args:    cobra.ExactArgs(1),
		PreRunE: rootPreRun,
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			if _, err := os.Stat(filepath.Join(servicesDir, name, "manifest.yaml")); err != nil {
				return fmt.Errorf("app %q is not accepted yet (run 'statio app add %s')", name, name)
			}
			return editAppInteractive(servicesDir, stateDir, actionRef, name)
		},
	}
	f := cmd.Flags()
	f.StringVar(&servicesDir, "services-dir", "/etc/statio/services", "services directory")
	f.StringVar(&stateDir, "state-dir", "/var/lib/statio", "state directory (to resolve the target)")
	f.StringVar(&actionRef, "action-ref", "accentiostudios/statio@v1", "ref of the statio Action (Marketplace)")
	return cmd
}

// repoFromIdentity returns the "owner/repo" carried by a cosign signer identity, or "".
func repoFromIdentity(identity string) string {
	if owner, repo, _, _, ok := parseSignerIdentity(identity); ok {
		return owner + "/" + repo
	}
	return ""
}

// printAuthKeySecret prints the `gh secret set STATIO_TS_AUTHKEY` commands for an app. There is ONE
// shared key (minted once by `statio init server`); per-app isolation is the cosign signer, not this
// key. So we show both ways to store the same key — an org secret (set once, covers every repo in the
// org, including multiple orgs) and a per-repo secret (personal accounts / a single repo) — plus the
// app's env secret. `repoArg` is the app's repo (filled from its signer identity); `keyArg` is the
// key value when known, else a pointer back to `init server` (the key is never persisted).
func printAuthKeySecret(repoArg, keyArg string) {
	if repoArg == "" {
		repoArg = "<owner>/<repo>"
	}
	if keyArg == "" {
		keyArg = "<the key statio init server printed>"
	}
	sectionTitle("GitHub secrets 💻 — on YOUR machine (gh logged in), not this server")
	info("STATIO_TS_AUTHKEY is ONE shared key from `statio init server`. Set it once per org, or per repo:")
	codeBlock(
		"gh secret set STATIO_TS_AUTHKEY --org <your-org> --visibility all --body '"+keyArg+"'",
		"gh secret set STATIO_TS_AUTHKEY --repo "+repoArg+" --body '"+keyArg+"'",
		"gh secret set DATABASE_URL      --repo "+repoArg+" --body '<value for each env in your statio.yaml>'",
	)
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

// validServiceName delegates to the spec's rule so `app add` rejects exactly what a deploy would
// (a DNS-label-safe name: lowercase letter then [a-z0-9-], ≤31 chars, no underscores). Keeping
// these in lockstep prevents accepting an app name that later fails every deploy.
func validServiceName(s string) bool { return spec.ValidServiceName(s) }

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

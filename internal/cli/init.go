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
				return fmt.Errorf("required")
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
				return fmt.Errorf("required")
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
		PreRunE: rootPreRun,
		RunE: func(c *cobra.Command, _ []string) error {
			if issuer == "" {
				issuer = "https://token.actions.githubusercontent.com"
			}
			var clientSecret string

			if interactive() && (hostname == "" || clientID == "") {
				banner("statio · init server", "Set up the deploy agent on this machine")
				if hostname == "" {
					hostname = "statio"
				}
				if err := runForm(
					inputField("Name of this server",
						"Tailscale will give it the address <name>.<your-tailnet>.ts.net, and CI uses that address to send the deploy. With a single server, 'statio' is fine.",
						"statio", &hostname, true),
				); err != nil {
					return err
				}
				sectionTitle("First, set up Tailscale (once, on the web)")
				info("CI reaches this server over Tailscale instead of SSH. Do two steps in the admin")
				info("console, in order — the OAuth client can only own tags that already exist:")
				info("")
				info("1. Access controls — define the tags. Each tag must OWN ITSELF so the OAuth client")
				info("   can register the agent and mint keys (else: 'tags ... not permitted'). Paste & save:")
				codeBlock(`{"tagOwners":{"tag:agent":["autogroup:admin","tag:agent"],"tag:ci":["autogroup:admin","tag:ci"]},"acls":[{"action":"accept","src":["tag:ci"],"dst":["tag:agent:443"]}]}`)
				info("")
				info("2. Settings -> OAuth clients -> Generate (newer consoles: Trust credentials -> New).")
				info("   Choose 'Custom scopes' and enable, both Write:")
				info("     - auth_keys     ->  Keys -> Auth Keys    (lets THIS server mint the CI key)")
				info("     - devices:core  ->  Devices -> Core      (lets the agent register as a node)")
				info("   Enabling Devices -> Core makes Tailscale ask for tags: pick tag:agent and tag:ci.")
				info("   Then copy the client id + secret. https://login.tailscale.com/admin/settings/oauth")
				info("")
				if err := runForm(
					inputField("OAuth client ID", "Client identifier (not secret).", "k123ABC...", &clientID, true),
					passwordField("OAuth client secret", "Starts with tskey-client-…; stored 0600 root, never printed", &clientSecret),
				); err != nil {
					return err
				}

				sectionTitle("Summary")
				info("hostname: %s", hostname)
				info("config:   %s", configPath)
				ok, err := confirm("Write the config + systemd unit and create the CI auth key?")
				if err != nil {
					return err
				}
				if !ok {
					warnLine("Cancelled. Nothing was written.")
					return nil
				}
			} else {
				if hostname == "" || clientID == "" {
					return fmt.Errorf("non-interactive mode: --hostname and --ts-oauth-client-id (+ the secret via stdin/file) are required")
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
			okLine("Wrote: %s, /etc/statio/secrets/oauth.json and the systemd unit", configPath)

			// We wrote the unit and run as root — bring the agent up ourselves instead of
			// telling the user to run systemctl by hand.
			if systemctlAvailable() {
				if err := startAgent(); err != nil {
					warnLine("could not start the agent automatically: %v", err)
					codeBlock("sudo systemctl daemon-reload && sudo systemctl enable --now statio-agent")
				} else {
					okLine("Agent enabled and started (statio-agent)")
					// Wait for the agent to join the tailnet and persist its address, so we can
					// print the exact CI target and 'statio app add' can fill it in for the user.
					info("Waiting for the agent to join the tailnet…")
					if aud := waitAudience("/var/lib/statio", 25*time.Second); aud != "" {
						okLine("Agent address — use this as the CI 'target': %s", aud)
					} else {
						info("Still joining. Its address (the CI 'target') shows in 'statio status' once up.")
					}
				}
			} else {
				info("Start the agent on the server (systemd):")
				codeBlock("sudo systemctl daemon-reload && sudo systemctl enable --now statio-agent")
			}

			// The server mints the shared tag:ci auth key CI uses to reach the agent.
			sectionTitle("CI auth key (minted by the server)")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			key, err := tailscale.New(tailnetAPI).MintCIKey(ctx, clientID, clientSecret, keyDays)
			if err != nil {
				warnLine("Could not create the auth key automatically: %v", err)
				info("Check that the OAuth client has the 'auth_keys' scope and owns tag:ci, then retry.")
			} else {
				info("Auth key tag:ci created (reusable, ephemeral, valid for %d days).", keyDays)
				info("Set it as a GitHub secret — run this on YOUR machine where 'gh' is logged in, NOT")
				info("on this server (there's no repo here). The --repo flag means you needn't be inside it:")
				codeBlock("gh secret set STATIO_TS_AUTHKEY --repo <owner>/<repo> --body '" + key + "'")
				info("Same key for ALL your repos — to set it once for a whole org instead:")
				codeBlock("gh secret set STATIO_TS_AUTHKEY --org <your-org> --visibility all --body '" + key + "'")
				info("Rotate it by re-running 'statio init server'.")
			}

			sectionTitle("Next steps")
			codeBlock(
				"sudo statio app add <name>     # accept an app and pin its signing repo (one per app)",
				"sudo statio init integrations  # NPMplus + Cloudflare + public IP (optional)",
			)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&hostname, "hostname", "", "tsnet MagicDNS hostname (non-interactive)")
	f.StringVar(&clientID, "ts-oauth-client-id", "", "Tailscale OAuth client id (non-interactive)")
	f.BoolVar(&oauthStdin, "ts-oauth-secret-stdin", false, "read the OAuth client secret from stdin (non-interactive)")
	f.StringVar(&oauthSecretFile, "ts-oauth-secret-file", "", "read the OAuth client secret from a file (non-interactive)")
	f.StringVar(&issuer, "issuer", "", "cosign OIDC issuer")
	f.StringVar(&configPath, "config", "/etc/statio/config.yaml", "config output path")
	f.IntVar(&keyDays, "ci-key-days", 90, "validity (days) of the minted tag:ci auth key")
	f.StringVar(&tailnetAPI, "tailscale-api", "", "Tailscale API base (for testing; defaults to public)")
	return cmd
}

// waitAudience polls for the agent's persisted MagicDNS address (written on tailnet join) up to
// the timeout, so init server can print the exact CI target instead of a placeholder. Returns ""
// if it doesn't appear in time (the agent may still be getting approved).
func waitAudience(stateDir string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		if b, err := os.ReadFile(filepath.Join(stateDir, "audience")); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s
			}
		}
		if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(time.Second)
	}
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
		Use:     "integrations",
		Short:   "Configure NPMplus + Cloudflare + the pinned public IP (interactive)",
		PreRunE: rootPreRun,
		RunE: func(c *cobra.Command, _ []string) error {
			if !interactive() {
				return fmt.Errorf("init integrations is interactive; run it in a terminal")
			}
			banner("statio · init integrations", "Reverse proxy (NPMplus) and DNS (Cloudflare)")

			doNPM, err := confirm("Set up NPMplus (reverse proxy)?")
			if err != nil {
				return err
			}
			if doNPM {
				url, identity, secret := "http://npmplus:81", "statio-agent", ""
				if err := runForm(
					inputField("NPMplus base URL", "Localhost or docker network name", "http://npmplus:81", &url, true),
					inputField("NPMplus API identity", "Dedicated non-admin user", "statio-agent", &identity, true),
					passwordField("NPMplus API secret", "Stored 0600 root", &secret),
				); err != nil {
					return err
				}
				if err := writeJSONSecret("/etc/statio/secrets/npmplus.json",
					fmt.Sprintf(`{"base_url":%q,"identity":%q,"secret":%q}`, url, identity, secret)); err != nil {
					return err
				}
				okLine("NPMplus configured")
				sectionTitle("Add to /etc/statio/config.yaml")
				codeBlock("npmplus:", "  base_url: "+url, "  credentials_file: /etc/statio/secrets/npmplus.json")
			}

			doCF, err := confirm("Set up Cloudflare (DNS)?")
			if err != nil {
				return err
			}
			if doCF {
				zoneID, apex, ip, token := "", "", "", ""
				if err := runForm(
					inputField("Cloudflare Zone ID", "Dashboard → your zone → Overview", "", &zoneID, true),
					inputField("Zone apex", "The root domain (e.g. example.com)", "example.com", &apex, true),
					inputField("Server public IP", "Where the A records will point", "203.0.113.10", &ip, true),
					passwordField("Cloudflare API token", "Zone.DNS:Edit scope on this zone; 0600 root", &token),
				); err != nil {
					return err
				}
				if err := writeJSONSecret("/etc/statio/secrets/cloudflare.json",
					fmt.Sprintf(`{"api_token":%q,"zone_id":%q}`, token, zoneID)); err != nil {
					return err
				}
				okLine("Cloudflare configured")
				sectionTitle("Add to /etc/statio/config.yaml")
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
				warnLine("Nothing to configure.")
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
		Short: "Prepare your repo for statio (statio.yaml + how to call the Action) — run INSIDE the repo",
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
				banner("statio · init repo", "Interactive wizard — runs INSIDE your project's repo (not on the server)")
				if repoOK {
					info("Repo detected: %s/%s — I filled in the defaults; just confirm or edit.", owner, repo)
				}
				if err := runForm(
					inputField("Server address (Tailscale)", "The name you gave the agent + .<your-tailnet>.ts.net. E.g. statio.your-org.ts.net", "statio.<your-tailnet>.ts.net", &target, true),
					inputField("Service", "Service name (must be enabled on the agent with 'statio enable')", "api", &service, true),
					inputField("Image repository", "Your image repo (must match 'statio enable --image')", "ghcr.io/accentiostudios/api", &image, true),
				); err != nil {
					return err
				}
			} else if target == "" || service == "" || image == "" {
				return fmt.Errorf("non-interactive mode: --target, --service and --image are required")
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
				okLine("Generated %s (edit your app's services/env)", statioOut)
			} else {
				info("%s already exists; left untouched", statioOut)
			}

			// 2. Print the exact cosign identity to paste on the server, from the repo
			//    detected above. Local git read, so it works for private repos too.
			if repoOK {
				ident := buildIdentity(owner, repo, workflow, branch)
				sectionTitle("Signing identity — paste this ON THE SERVER 🖥️")
				info("Detected your repo: %s/%s  (branch %s, workflow %s)", owner, repo, branch, workflow)
				info("Cosign identity: %s", ident)
				info("In the 'statio init server' wizard, under 'GitHub repository' enter: %s/%s", owner, repo)
				info("(or in non-interactive mode:)")
				codeBlock("statio init server --hostname <name> --repo " + owner + "/" + repo + " --branch " + branch + " --ts-oauth-secret-stdin")
			} else {
				info("Could not detect the repo from git (is there an 'origin' remote?). On the server, enter your owner/repo by hand.")
			}

			// 3. Workflow: never modify an existing one. Detect + adapt.
			existing := detectWorkflows()
			switch {
			case len(existing) > 0:
				sectionTitle("You already have CI — add statio as a step 💻")
				info("Detected: %s", strings.Join(existing, ", "))
				info("statio does NOT touch your workflow. Add this step where you build and sign your image:")
				printSnippet(target, service, image, actionRef)
			default:
				gen := createWorkflow
				if !gen && interactive() {
					ok, err := confirm("No CI workflow detected. Generate a ready-to-use .github/workflows/deploy.yml?")
					if err != nil {
						return err
					}
					gen = ok
				}
				if gen {
					if _, err := os.Stat(out); err == nil {
						info("%s already exists; not overwriting. Use this step:", out)
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
						okLine("Generated %s", out)
					}
				} else {
					sectionTitle("Add statio to your workflow 💻")
					printSnippet(target, service, image, actionRef)
				}
			}

			// 4. Secrets — run from your machine, towards GitHub.
			sectionTitle("Set up these GitHub secrets (from your machine 💻)")
			codeBlock(
				"gh secret set STATIO_TS_AUTHKEY --body '<the tag:ci auth key printed by statio init server>'",
				"gh secret set DATABASE_URL      --body '<the value of each env your statio.yaml requires>'",
			)
			info("STATIO_TS_AUTHKEY is the same for all your repos; it's minted by 'statio init server'.")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&target, "target", "", "agent MagicDNS host (non-interactive)")
	f.StringVar(&service, "service", "", "service name (non-interactive)")
	f.StringVar(&image, "image", "", "image repository (non-interactive)")
	f.StringVar(&actionRef, "action-ref", "accentiostudios/statio@v1", "the statio Action ref (Marketplace)")
	f.StringVar(&out, "out", ".github/workflows/deploy.yml", "workflow path (with --create-workflow)")
	f.StringVar(&statioOut, "statio-out", "statio.yaml", "statio.yaml starter output path")
	f.BoolVar(&createWorkflow, "create-workflow", false, "generate a starter deploy.yml (only if none exists)")
	f.StringVar(&branch, "branch", "main", "deploy branch (for the printed cosign identity)")
	f.StringVar(&workflow, "workflow", "deploy.yml", "workflow file name (for the printed identity)")
	return cmd
}

// printSnippet renders and prints the CI step to paste into the user's own workflow. It is
// printed verbatim (it contains literal ${{ ... }} GitHub expressions).
func printSnippet(target, service, image, actionRef string) {
	snip, err := render("statio-step.snippet.tmpl", map[string]string{
		"Target": target, "Service": service, "Image": image, "ActionRef": actionRef,
	})
	if err != nil {
		warnLine("could not generate the snippet: %v", err)
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

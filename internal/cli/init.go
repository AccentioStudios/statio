package cli

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/accentiostudios/statio/internal/config"
	"github.com/accentiostudios/statio/internal/dns"
	"github.com/accentiostudios/statio/internal/fsutil"
	"github.com/accentiostudios/statio/internal/proxy"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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

// serviceNameField validates live against the service-name rule (a DNS-label-safe token: lowercase
// letter then [a-z0-9-], ≤31 chars, no underscores). Used for the app name and the proxy upstream
// so the wizard rejects in-place exactly what a deploy would reject later.
func serviceNameField(title, desc, placeholder string, v *string) huh.Field {
	return huh.NewInput().Title(title).Description(desc).Placeholder(placeholder).Value(v).
		Validate(func(s string) error {
			if !validServiceName(strings.TrimSpace(s)) {
				return fmt.Errorf("lowercase letters, digits and dashes; start with a letter; max 31; no underscores")
			}
			return nil
		})
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
	var hostname, issuer, oauthSecretFile, clientID string
	var oauthStdin bool
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Configure and start the deploy agent on this server",
		Long: "Configures the agent on this machine, writes its systemd unit, and starts it. It asks\n" +
			"for no repo: you accept each app later with `statio app add`. It takes the agent's\n" +
			"Tailscale OAuth client (tag:agent). CI joins with its own tag:ci client.\n\n" +
			"Guide: https://statio.accentio.dev/tutorials/first-deploy/",
		Example: "  sudo statio init server",
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
						"Tailscale gives it the address <name>.<your-tailnet>.ts.net, and CI sends the deploy to that address. With one server, 'statio' works.",
						"statio", &hostname, true),
				); err != nil {
					return err
				}
				sectionTitle("Step 1 of 2 · Tailscale, in the admin console")
				info("Do these three steps in order, then come back here. An OAuth client can only")
				info("carry a tag that already exists, so the tags come first.")
				info("")
				info("1. Access controls: save this policy. Each tag owns itself, which is what lets")
				info("   its OAuth client register with it.")
				codeBlock(`{"tagOwners":{"tag:agent":["autogroup:admin","tag:agent"],"tag:ci":["autogroup:admin","tag:ci"]},"acls":[{"action":"accept","src":["tag:ci"],"dst":["tag:agent:443"]}]}`)
				info("")
				info("2. Settings -> OAuth clients -> Generate. Pick 'Custom scopes', Write on")
				info("   auth_keys (Keys -> Auth Keys) and on devices:core (Devices -> Core). Assign")
				info("   tag:agent. Copy its id and secret; you paste them below.")
				info("   https://login.tailscale.com/admin/settings/oauth")
				info("")
				info("3. Generate a second client. Custom scopes, Write on auth_keys. Assign tag:ci.")
				info("   Set its id and secret as GitHub secrets, from your machine:")
				codeBlock(
					"gh secret set STATIO_TS_OAUTH_CLIENT_ID --org <your-org> --visibility all --body '<ci client id>'",
					"gh secret set STATIO_TS_OAUTH_SECRET    --org <your-org> --visibility all --body '<ci client secret>'",
				)
				info("")
				info("Full guide: https://statio.accentio.dev/how-to/set-up-tailscale/")
				sectionTitle("Step 2 of 2 · Paste the agent's OAuth client")
				info("Use the client from step 2, the one tagged tag:agent.")
				if err := runForm(
					inputField("Agent OAuth client ID", "The client id, not the secret.", "k123ABC...", &clientID, true),
					passwordField("Agent OAuth client secret", "Starts with tskey-client-. Stored 0600 root, never printed.", &clientSecret),
				); err != nil {
					return err
				}

				sectionTitle("Summary")
				info("hostname: %s", hostname)
				info("config:   %s", defaultConfigPath)
				ok, err := confirm("Write the agent config + systemd unit?")
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

			if err := writeServerFiles(hostname, issuer, clientID, clientSecret); err != nil {
				return err
			}
			okLine("Wrote: %s, /etc/statio/secrets/oauth.json and the systemd unit", defaultConfigPath)

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
						okLine("Agent address (use this as the CI 'target'): %s", aud)
					} else {
						info("Still joining. Its address shows in 'statio status' once it is up.")
					}
				}
			} else {
				info("Start the agent on the server (systemd):")
				codeBlock("sudo systemctl daemon-reload && sudo systemctl enable --now statio-agent")
			}

			// CI does NOT use a key minted here — it joins with its OWN tag:ci OAuth client (step 3
			// above), set as the STATIO_TS_OAUTH_CLIENT_ID + STATIO_TS_OAUTH_SECRET GitHub secrets.
			// Keeping CI on a separate client means CI can never act as the agent; what each repo may
			// deploy is fixed by its cosign signer (set per app in `statio app add`).
			sectionTitle("CI credential")
			info("CI joins with the tag:ci client from step 3. Set STATIO_TS_OAUTH_CLIENT_ID and")
			info("STATIO_TS_OAUTH_SECRET as GitHub secrets if you have not. The agent hands CI no")
			info("key, and the OAuth client secret has no expiry, so nothing needs rotating.")

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

func writeServerFiles(hostname, issuer, clientID, clientSecret string) error {
	if err := os.MkdirAll("/etc/statio/secrets", 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(defaultConfigPath), 0o700); err != nil {
		return err
	}
	// Create the state dir up front: the systemd unit lists it in ReadWritePaths, and systemd
	// fails the service with 226/NAMESPACE if it doesn't exist when the sandbox is set up.
	if err := os.MkdirAll("/var/lib/statio", 0o700); err != nil {
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
services_dir: /etc/statio/services
state_dir: /var/lib/statio
log_level: info
`, hostname, issuer)
	if err := fsutil.SecureWrite(defaultConfigPath, []byte(cfg), 0o600); err != nil {
		return err
	}
	return writeAgentUnit()
}

// agentUnitPath is where the systemd unit lives on a server.
const agentUnitPath = "/etc/systemd/system/statio-agent.service"

// defaultConfigPath is the single, fixed location of the agent config. statio uses one path
// everywhere (there is no --config override), so `upgrade` can re-render the unit without having
// to recover a custom path from it.
const defaultConfigPath = "/etc/statio/config.yaml"

// writeAgentUnit renders the embedded systemd unit and writes it. Shared by `init server` (first
// install) and `statio upgrade` (so unit-level fixes — e.g. a new sandbox address family — reach
// existing servers on upgrade, not only on a fresh init).
func writeAgentUnit() error {
	unit, err := render("statio-agent.service.tmpl", map[string]string{"ConfigPath": defaultConfigPath})
	if err != nil {
		return err
	}
	return os.WriteFile(agentUnitPath, unit, 0o644)
}

// ======================== init integrations =========================

const (
	npmplusSecretPath    = "/etc/statio/secrets/npmplus.json"
	cloudflareSecretPath = "/etc/statio/secrets/cloudflare.json"
)

func newInitIntegrationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integrations",
		Short: "Configure NPMplus, Cloudflare and the pinned public IP",
		Long: "Configures the optional reverse proxy (NPMplus) and DNS (Cloudflare), writing both\n" +
			"into the agent config. Re-run it any time to change one detail: it fills every answer\n" +
			"with the current value.\n\n" +
			"Guide: https://statio.accentio.dev/how-to/add-a-domain/",
		Example: "  sudo statio init integrations",
		PreRunE: rootPreRun,
		RunE: func(c *cobra.Command, _ []string) error {
			if !interactive() {
				return fmt.Errorf("init integrations is interactive; run it in a terminal")
			}
			cfg, err := loadConfigForEdit(defaultConfigPath)
			if err != nil {
				return fmt.Errorf("this server is not set up yet. Run 'sudo statio init server' first (%w)", err)
			}
			banner("statio · init integrations", "Reverse proxy (NPMplus) and DNS (Cloudflare)")
			info("Optional. Set this up to serve an app on a public domain, with TLS through NPMplus")
			info("and a Cloudflare A record. Deploys work without it. Re-run it to edit one value;")
			info("the current settings are filled in below.")

			if err := configureNPMplus(cfg); err != nil {
				return err
			}
			if err := configureCloudflare(cfg); err != nil {
				return err
			}

			if err := saveConfig(defaultConfigPath, cfg); err != nil {
				return err
			}
			okLine("Wrote %s", defaultConfigPath)

			// The agent reads its config once at boot (fail-closed, no hot reload), so a running
			// agent still serves the OLD settings until restarted. Offer to do it right here —
			// a skipped restart is the classic "wizard passed but CI still fails" trap.
			if systemctlAvailable() && agentActive() {
				restart, err := confirm("Restart the agent now to apply the new settings?")
				if err != nil {
					return err
				}
				if restart {
					if err := restartAgent(); err != nil {
						warnLine("could not restart the agent: %v", err)
						codeBlock("sudo systemctl restart statio-agent")
						return nil
					}
					okLine("Agent restarted. The new settings are live.")
				} else {
					warnLine("Not restarted. The running agent keeps the old settings until you run:")
					codeBlock("sudo systemctl restart statio-agent")
				}
			} else {
				info("Restart the agent to apply: sudo systemctl restart statio-agent")
			}
			return nil
		},
	}
	return cmd
}

// configureNPMplus runs the (re-runnable) NPMplus section: it pre-fills from the current config +
// stored secret, asks only what changed, and writes both the secret file and the config fields
// (base_url, credentials_file, upstream_mode, network). Answering "no" disables NPMplus.
func configureNPMplus(cfg *config.Config) error {
	enabled := cfg.NPMplus.BaseURL != ""
	prompt := "Set up NPMplus (reverse proxy)?"
	if enabled {
		prompt = "NPMplus is configured. Keep / edit it?"
	}
	doNPM, err := confirm(prompt)
	if err != nil {
		return err
	}
	if !doNPM {
		if enabled {
			warnLine("Disabling NPMplus (the agent will stop reconciling proxy hosts).")
			cfg.NPMplus = config.NPMplusConfig{UpstreamMode: config.UpstreamModeLoopback}
		}
		return nil
	}

	info("In NPMplus, create a dedicated non-admin user (Users, then Add). Enter that user's login")
	info("email and password below. The agent reaches the NPMplus admin API over localhost.")
	apiURL, identity, curSecret := readNPMplusSecret(npmplusSecretPath)
	if apiURL == "" {
		apiURL = "https://127.0.0.1:81"
	}
	var secret string
	var insecure bool
	for {
		secret = ""
		secretDesc := "That user's password (the API 'secret'). Stored 0600, root-only."
		if curSecret != "" {
			secretDesc = "Leave BLANK to keep the current password. " + secretDesc
		}
		if err := runForm(
			inputField("NPMplus admin API URL", "How the agent reaches NPMplus. The agent runs on the host, so this is host loopback: https://127.0.0.1:81. The scheme is auto-detected.", "https://127.0.0.1:81", &apiURL, true),
			inputField("NPMplus login email", "The dedicated non-admin NPMplus user (the API 'identity').", "statio@your-domain.com", &identity, true),
			huh.NewInput().Title("NPMplus password").Description(secretDesc).EchoMode(huh.EchoModePassword).Value(&secret),
		); err != nil {
			return err
		}
		if secret == "" {
			secret = curSecret // keep existing on a blank answer
		}
		if secret == "" {
			failLine("The NPMplus password is required, and none is stored yet")
			continue
		}

		// Test the credentials NOW — trying every sensible connection variant (as entered,
		// the other scheme, and https with NPMplus's self-signed admin cert on loopback) —
		// so a bad URL/login fails here in the wizard, not later in CI as proxy_unreachable.
		// Whichever variant authenticates is the one that gets configured.
		info("Testing the NPMplus connection…")
		finalURL, ins, err := probeNPMplus(context.Background(), strings.TrimSpace(apiURL), strings.TrimSpace(identity), secret)
		if err == nil {
			if finalURL != strings.TrimSpace(apiURL) {
				info("Connected via %s. Saving that URL.", finalURL)
			}
			apiURL, insecure = finalURL, ins
			okLine("NPMplus reachable and credentials accepted")
			break
		}
		failLine("NPMplus test failed: %v", err)
		retry, cerr := confirm("Retry with different values?")
		if cerr != nil {
			return cerr
		}
		if retry {
			continue
		}
		saveAnyway, cerr := confirm("Save it unverified? A deploy that asks for a domain fails preflight until this works")
		if cerr != nil {
			return cerr
		}
		if !saveAnyway {
			return fmt.Errorf("NPMplus not configured (connection test failed)")
		}
		break
	}

	// Data-plane topology — how NPMplus reaches your APP. The devops decides; statio supports both.
	mode := cfg.NPMplus.UpstreamMode
	if mode == "" {
		mode = config.UpstreamModeLoopback
	}
	if err := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().
		Title("How does NPMplus reach your app?").
		Description("loopback: NPMplus runs on the host network and forwards to 127.0.0.1:<port>. container: NPMplus and your app share a docker network, and it forwards by container name.").
		Options(
			huh.NewOption("loopback, over the host (default)", config.UpstreamModeLoopback),
			huh.NewOption("container, over a shared docker network", config.UpstreamModeContainer),
		).Value(&mode))).WithTheme(huh.ThemeCharm()).Run(); err != nil {
		return err
	}
	network := cfg.NPMplus.Network
	if mode == config.UpstreamModeContainer {
		if err := runForm(serviceNameField("Shared docker network",
			"The docker network NPMplus and your app both join. Attach NPMplus to it yourself; statio attaches your app. For example statio-edge.",
			"statio-edge", &network)); err != nil {
			return err
		}
	} else {
		network = "" // loopback must not carry a network
	}

	if err := writeJSONSecret(npmplusSecretPath,
		fmt.Sprintf(`{"base_url":%q,"identity":%q,"secret":%q}`, apiURL, identity, secret)); err != nil {
		return err
	}
	cfg.NPMplus = config.NPMplusConfig{
		BaseURL: apiURL, CredentialsFile: npmplusSecretPath,
		UpstreamMode: mode, Network: network,
		InsecureSkipVerify: insecure,
	}
	okLine("NPMplus configured (%s mode)", mode)
	return nil
}

// npmplusCandidate is one way to reach the NPMplus admin API the wizard will try.
type npmplusCandidate struct {
	url      string
	insecure bool // accept a self-signed cert (loopback only)
}

// npmplusCandidates expands the operator-entered URL into every sensible connection variant, in
// preference order: exactly as entered first, then the same host on the OTHER scheme (NPMplus
// serves its admin port over HTTPS by default, so an `http://` guess is common), each tried
// strictly before falling back to accepting a self-signed cert — and that fallback only for a
// loopback host (the same rule config validation enforces).
func npmplusCandidates(raw string) []npmplusCandidate {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return []npmplusCandidate{{url: raw}}
	}
	loop := config.IsLoopbackURL(raw)
	var out []npmplusCandidate
	seen := map[npmplusCandidate]bool{}
	add := func(c npmplusCandidate) {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	add(npmplusCandidate{url: raw})
	if u.Scheme == "https" && loop {
		add(npmplusCandidate{url: raw, insecure: true})
	}
	alt := *u
	if u.Scheme == "http" {
		alt.Scheme = "https"
	} else {
		alt.Scheme = "http"
	}
	add(npmplusCandidate{url: alt.String()})
	if alt.Scheme == "https" && loop {
		add(npmplusCandidate{url: alt.String(), insecure: true})
	}
	return out
}

// probeNPMplus tries every candidate until one authenticates (a read-only token mint + host
// list). It returns the WINNING url + whether it needed the self-signed concession — exactly
// what gets persisted, so the tested config and the saved config can never differ.
func probeNPMplus(ctx context.Context, raw, identity, secret string) (string, bool, error) {
	var lastErr error
	for _, cand := range npmplusCandidates(raw) {
		cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		var opts []proxy.Option
		if cand.insecure {
			opts = append(opts, proxy.WithInsecureTLS())
		}
		err := proxy.New(cand.url, identity, secret, opts...).Check(cctx)
		cancel()
		if err == nil {
			return cand.url, cand.insecure, nil
		}
		lastErr = err
	}
	return "", false, lastErr
}

// configureCloudflare runs the (re-runnable) Cloudflare/DNS section, pre-filled from the current
// config + stored secret. Answering "no" disables DNS.
func configureCloudflare(cfg *config.Config) error {
	enabled := cfg.Cloudflare.CredentialsFile != ""
	prompt := "Set up Cloudflare (DNS)?"
	if enabled {
		prompt = "Cloudflare is configured. Keep / edit it?"
	}
	doCF, err := confirm(prompt)
	if err != nil {
		return err
	}
	if !doCF {
		if enabled {
			warnLine("Disabling Cloudflare DNS.")
			cfg.Cloudflare = config.CloudflareConfig{}
			cfg.DNS = config.DNSConfig{}
		}
		return nil
	}
	info("Create a scoped API token at Cloudflare, under My Profile, API Tokens, Create Token.")
	info("Give it Zone.DNS:Edit on this zone alone. Paste it below. A global key does not fit here.")
	curToken, curZoneID := readCloudflareSecret(cloudflareSecretPath)
	zoneID, apex, ip := curZoneID, cfg.Cloudflare.ZoneApex, cfg.DNS.PublicIP
	var token string
	for {
		token = ""
		tokenDesc := "The scoped token (Zone.DNS:Edit). Stored 0600, root-only."
		if curToken != "" {
			tokenDesc = "Leave BLANK to keep the current token. " + tokenDesc
		}
		if err := runForm(
			inputField("Cloudflare Zone ID", "Dashboard, your domain, Overview, right column. A 32-character hex id.", "", &zoneID, true),
			inputField("Zone apex", "The root domain the zone covers, such as example.com.", "example.com", &apex, true),
			inputField("Server public IP", "The public IPv4 the A records point to.", "203.0.113.10", &ip, true),
			huh.NewInput().Title("Cloudflare API token").Description(tokenDesc).EchoMode(huh.EchoModePassword).Value(&token),
		); err != nil {
			return err
		}
		if token == "" {
			token = curToken
		}
		if token == "" {
			failLine("The Cloudflare API token is required, and none is stored yet")
			continue
		}

		// Test the token + zone NOW (a read-only GET of the zone), so a typo fails here in the
		// wizard instead of surfacing later as a degraded deploy or a failed preflight.
		info("Testing the Cloudflare token…")
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := dns.New(token, strings.TrimSpace(zoneID), "").Check(cctx)
		cancel()
		if err == nil {
			okLine("Cloudflare token accepted for the zone")
			break
		}
		failLine("Cloudflare test failed: %v", err)
		retry, cerr := confirm("Retry with different values?")
		if cerr != nil {
			return cerr
		}
		if retry {
			continue
		}
		saveAnyway, cerr := confirm("Save it unverified?")
		if cerr != nil {
			return cerr
		}
		if !saveAnyway {
			return fmt.Errorf("Cloudflare not configured (token test failed)")
		}
		break
	}
	if err := writeJSONSecret(cloudflareSecretPath,
		fmt.Sprintf(`{"api_token":%q,"zone_id":%q}`, token, zoneID)); err != nil {
		return err
	}
	cfg.Cloudflare = config.CloudflareConfig{CredentialsFile: cloudflareSecretPath, ZoneApex: apex}
	if cfg.DNS.TTL == 0 {
		cfg.DNS.TTL = 1
	}
	cfg.DNS.PublicIP = ip
	okLine("Cloudflare configured")
	return nil
}

// loadConfigForEdit reads the agent config WITHOUT applying defaults or validating, so the wizard
// can round-trip it (pre-fill, edit one field, write back). It preserves every setting statio's
// config schema models; YAML comments and any hand-added unknown keys are NOT preserved (the
// round-trip goes through the typed struct), so /etc/statio/config.yaml is managed by statio.
func loadConfigForEdit(path string) (*config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c config.Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// Defaults are normally applied by config.Load at boot; apply the one that affects validation
	// here so a pre-feature config (no upstream_mode) round-trips and validates cleanly.
	if c.NPMplus.UpstreamMode == "" {
		c.NPMplus.UpstreamMode = config.UpstreamModeLoopback
	}
	return &c, nil
}

// saveConfig validates the edited config (fail-closed, the same checks the agent runs at boot) and
// writes it back 0600 root. Validating here means a bad combination (e.g. container mode without a
// network) is caught in the wizard, not at the next agent restart.
func saveConfig(path string, c *config.Config) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("refusing to write an invalid config: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return fsutil.SecureWrite(path, data, 0o600)
}

// readNPMplusSecret best-effort reads the stored NPMplus credential to pre-fill the wizard. The
// secret (password) is returned so a blank answer can keep it; it is never displayed.
func readNPMplusSecret(path string) (url, identity, secret string) {
	var s struct {
		BaseURL  string `json:"base_url"`
		Identity string `json:"identity"`
		Secret   string `json:"secret"`
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s.BaseURL, s.Identity, s.Secret
}

func readCloudflareSecret(path string) (token, zoneID string) {
	var s struct {
		APIToken string `json:"api_token"`
		ZoneID   string `json:"zone_id"`
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s.APIToken, s.ZoneID
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
		Short: "Write statio.yaml and the workflow step, inside your repo",
		Long: "Run this inside your project's repo, not on the server. It writes statio.yaml, prints\n" +
			"the app's cosign signing identity for `statio app add`, and either generates\n" +
			".github/workflows/deploy.yml or prints the step to paste into the workflow you have.\n" +
			"It never edits an existing workflow.\n\n" +
			"Guide: https://statio.accentio.dev/tutorials/first-deploy/",
		Example: "  statio init repo",
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
				banner("statio · init repo", "Runs inside your project's repo, not on the server")
				if repoOK {
					info("Repo detected: %s/%s. The defaults below come from it. Confirm or edit them.", owner, repo)
				}
				if err := runForm(
					inputField("Server address (Tailscale)", "The name you gave the agent, plus .<your-tailnet>.ts.net. For example statio.your-org.ts.net", "statio.<your-tailnet>.ts.net", &target, true),
					inputField("Service", "The service name, accepted on the agent with 'statio app add'.", "api", &service, true),
					inputField("Image repository", "Your image repo. It must match 'statio app add --image'.", "ghcr.io/accentiostudios/api", &image, true),
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
				okLine("Generated %s. Edit its services and env to match your app.", statioOut)
			} else {
				info("%s already exists. Left untouched.", statioOut)
			}

			// 2. Print the exact cosign identity to paste on the server, from the repo
			//    detected above. Local git read, so it works for private repos too.
			if repoOK {
				ident := buildIdentity(owner, repo, workflow, branch)
				sectionTitle("Signing identity · accept this app on the server 🖥️")
				info("Detected your repo: %s/%s  (branch %s, workflow %s)", owner, repo, branch, workflow)
				info("Cosign identity: %s", ident)
				info("On the server, run 'statio app add' and enter this repo when it asks. It derives")
				info("the identity above for you. Without prompts:")
				codeBlock("sudo statio app add " + service + " --repo " + owner + "/" + repo + " --workflow " + workflow + " --branch " + branch + " --image " + image)
			} else {
				info("No repo detected from git. Check for an 'origin' remote. On the server, run 'statio app add' and type your owner/repo by hand.")
			}

			// 3. Workflow: never modify an existing one. Detect + adapt.
			existing := detectWorkflows()
			switch {
			case len(existing) > 0:
				sectionTitle("You already have CI · add statio as a step 💻")
				info("Detected: %s", strings.Join(existing, ", "))
				info("statio leaves your workflow alone. Add this step where you build your image:")
				printSnippet(target, service, image, actionRef)
			default:
				gen := createWorkflow
				if !gen && interactive() {
					ok, err := confirm("No CI workflow found. Generate .github/workflows/deploy.yml?")
					if err != nil {
						return err
					}
					gen = ok
				}
				if gen {
					if _, err := os.Stat(out); err == nil {
						info("%s already exists. Leaving it alone. Use this step:", out)
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
				"gh secret set STATIO_TS_OAUTH_CLIENT_ID --body '<CI tag:ci OAuth client id>'",
				"gh secret set STATIO_TS_OAUTH_SECRET    --body '<CI tag:ci OAuth client secret>'",
				"gh secret set DATABASE_URL              --body '<the value of each env your statio.yaml requires>'",
			)
			info("The two STATIO_TS_OAUTH_* secrets hold CI's own tag:ci OAuth client, created in the")
			info("Tailscale console. The same pair works for every repo, so you can set them org-wide.")
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

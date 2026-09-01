package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/accentiostudios/statio/internal/client"
	"github.com/accentiostudios/statio/internal/deploy"
	"github.com/accentiostudios/statio/internal/spec"
	"github.com/accentiostudios/statio/internal/statiofile"
	"github.com/spf13/cobra"
)

// registryAuthFromEnv reads the registry pull credential the Action forwards via env (never argv):
// STATIO_REGISTRY_USERNAME (defaults to the GitHub actor) + STATIO_REGISTRY_PASSWORD (the run's
// GITHUB_TOKEN). Returns nil when no password is set — the image is public and no auth is sent.
func registryAuthFromEnv() *spec.RegistryAuth {
	pass := os.Getenv("STATIO_REGISTRY_PASSWORD")
	if pass == "" {
		return nil
	}
	user := os.Getenv("STATIO_REGISTRY_USERNAME")
	if user == "" {
		user = "x-access-token" // GHCR accepts any non-empty username with a valid token
	}
	return &spec.RegistryAuth{Username: user, Password: pass}
}

// deployFlags is the shared input set for `deploy` and `preflight`: both read the same
// statio.yaml, build and cosign-sign the SAME payload, and send it to the agent (to /deploy or
// /preflight respectively), so a green preflight proves the exact bytes a deploy would send.
type deployFlags struct {
	target, service, image, digest, statioFile, audience string
	deploySeq                                            int64
	freshness                                            time.Duration
	envs                                                 []string
	timeout                                              time.Duration
}

func bindDeployFlags(cmd *cobra.Command, f *deployFlags, freshnessDefault time.Duration) {
	fl := cmd.Flags()
	fl.StringVar(&f.target, "target", "", "agent MagicDNS host (e.g. statio.tailnet.ts.net); also the signed audience")
	fl.StringVar(&f.service, "service", "", "service slot name (must be accepted on the server)")
	fl.StringVar(&f.image, "image", "", "your app's image repository (no tag/digest)")
	fl.StringVar(&f.digest, "digest", "", "image digest sha256:...")
	fl.StringVar(&f.statioFile, "statio-file", "statio.yaml", "path to the repo's statio.yaml")
	fl.StringVar(&f.audience, "audience", "", "override the signed audience (defaults to --target)")
	fl.Int64Var(&f.deploySeq, "deploy-seq", 0, "monotonic deploy sequence (e.g. github.run_number)")
	fl.DurationVar(&f.freshness, "freshness", freshnessDefault, "how long the signed payload stays valid")
	fl.StringArrayVar(&f.envs, "env", nil, "env override KEY=VALUE (repeatable; or STATIO_ENV_OVERRIDES)")
	fl.DurationVar(&f.timeout, "timeout", 5*time.Minute, "request timeout")
}

// buildEnvelope validates the required inputs, reads statio.yaml, and builds + cosign-signs the
// wire envelope. Shared by deploy and preflight so both send byte-identical signed payloads.
func (f *deployFlags) buildEnvelope(ctx context.Context) ([]byte, error) {
	var missing []string
	if f.target == "" {
		missing = append(missing, "--target (the agent's MagicDNS host)")
	}
	if f.service == "" {
		missing = append(missing, "--service")
	}
	if f.image == "" {
		missing = append(missing, "--image")
	}
	if f.digest == "" {
		missing = append(missing, "--digest")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required input(s): %s. Set them in the statio action's `with:` block", strings.Join(missing, ", "))
	}
	data, err := os.ReadFile(f.statioFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.statioFile, err)
	}
	pf, err := statiofile.Parse(data)
	if err != nil {
		return nil, err
	}
	envMap, err := collectEnv(f.envs)
	if err != nil {
		return nil, err
	}
	audience := f.audience
	if audience == "" {
		audience = f.target
	}
	now := time.Now().UTC()
	in := client.Inputs{
		Service: f.service, Repository: f.image, Digest: f.digest,
		AppIntent: pf.AppIntent(), EnvOverrides: envMap,
		Proxy: pf.ProxyWire(), DNS: pf.DNSWire(),
		Audience: audience, DeploySeq: f.deploySeq,
		IssuedAt: now.Format(time.RFC3339), Expiry: now.Add(f.freshness).Format(time.RFC3339),
	}
	payload, err := client.BuildSpec(in)
	if err != nil {
		return nil, err
	}
	// Forward a short-lived registry pull credential (the agent has no GitHub identity of its
	// own). Sourced from env, NEVER argv. Absent → public image.
	return client.SignAndWrap(ctx, payload, registryAuthFromEnv())
}

func newDeployCmd() *cobra.Command {
	var f deployFlags
	var strict bool
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Sign and send a deploy to the agent. The Action runs this.",
		RunE: func(c *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), f.timeout+60*time.Second)
			defer cancel()
			envelope, err := f.buildEnvelope(ctx)
			if err != nil {
				return err
			}
			res, status, err := client.Deploy(ctx, f.target, envelope, f.timeout)
			if err != nil {
				return err
			}
			printResult(res, status)
			return exitForResult(res, status, strict)
		},
	}
	bindDeployFlags(cmd, &f, 5*time.Minute)
	cmd.Flags().BoolVar(&strict, "strict", false, "treat success_degraded as failure")
	return cmd
}

func newPreflightCmd() *cobra.Command {
	var f deployFlags
	cmd := &cobra.Command{
		Use:   "preflight",
		Short: "Dry-run a deploy: check ports and edge readiness, change nothing",
		Long: "Sends the same signed payload a deploy would, to the agent's read-only /preflight\n" +
			"endpoint. The agent checks it can allocate the app's host port, with no conflict\n" +
			"against another app. When your statio.yaml asks for a public domain, it also checks\n" +
			"NPMplus is configured and reachable and that the domain is allowlisted. It changes\n" +
			"nothing on the server.\n\n" +
			"The Action runs this before every deploy. A failed check exits non-zero.",
		RunE: func(c *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), f.timeout+60*time.Second)
			defer cancel()
			envelope, err := f.buildEnvelope(ctx)
			if err != nil {
				return err
			}
			res, status, err := client.Preflight(ctx, f.target, envelope, f.timeout)
			if err != nil {
				return err
			}
			printResult(res, status)
			return exitForPreflight(res, status)
		},
	}
	bindDeployFlags(cmd, &f, 5*time.Minute)
	return cmd
}

// exitForPreflight maps a preflight report to a process exit: success only when the agent
// returned the preflight_ok state. Anything else (a failed check, or an admission rejection that
// returned no state) is a non-zero exit with the agent's own reason.
func exitForPreflight(res *deploy.Result, status int) error {
	if res.State == deploy.StatePreflightOK {
		return nil
	}
	reason := res.Error
	if reason == "" {
		if s := firstFailedStage(res); s != nil {
			reason = strings.TrimSpace(s.Stage + " " + s.Code + ": " + s.Message)
		}
	}
	if reason == "" {
		reason = fmt.Sprintf("state %q", res.State)
	}
	msg := fmt.Sprintf("preflight failed (HTTP %d): %s", status, reason)
	if hint := deployFailureHint(status); status != http.StatusOK && hint != "" {
		msg += "\n  hint: " + hint
	}
	return errors.New(msg)
}

// collectEnv merges repeatable --env flags with the optional STATIO_ENV_OVERRIDES dotenv
// block (set by the Action from ${{ secrets.* }}). Values never come from argv in CI.
func collectEnv(flags []string) (map[string]string, error) {
	out := map[string]string{}
	add := func(line string) error {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			return nil
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid env entry %q: expected KEY=VALUE", line)
		}
		out[strings.TrimSpace(k)] = v
		return nil
	}
	if block := os.Getenv("STATIO_ENV_OVERRIDES"); block != "" {
		for _, line := range strings.Split(block, "\n") {
			if err := add(line); err != nil {
				return nil, err
			}
		}
	}
	for _, e := range flags {
		if err := add(e); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func printResult(res *deploy.Result, status int) {
	fmt.Printf("state=%s http=%d digest=%s\n", res.State, status, res.Digest)
	if res.Error != "" {
		fmt.Printf("  agent: %s\n", res.Error)
	}
	for _, s := range res.Stages {
		mark := "✓"
		switch s.Status {
		case "failed", "unhealthy":
			mark = "✗"
		case "noop":
			mark = "•"
		}
		line := fmt.Sprintf("  %s %-12s %-8s", mark, s.Stage, s.Status)
		if s.Code != "" {
			line += " [" + s.Code + "]"
		}
		if s.Message != "" {
			line += " " + s.Message
		}
		fmt.Println(line)
		if s.Hint != "" {
			fmt.Printf("      hint: %s\n", s.Hint)
		}
	}
	if res.RolledBackTo != "" {
		fmt.Printf("  ↺ rolled back to %s\n", res.RolledBackTo)
	}
}

func exitForResult(res *deploy.Result, status int, strict bool) error {
	switch res.State {
	case deploy.StateSuccess, deploy.StateNoOp:
		return nil
	case deploy.StateSuccessDegraded:
		if strict {
			return fmt.Errorf("deploy degraded, and --strict is set")
		}
		return nil
	}
	// Failure. Lead with the agent's own reason (its {"error":…} body, or the failed stage), then
	// a status-specific hint — never a bare "state= http=403".
	reason := res.Error
	if reason == "" {
		if s := firstFailedStage(res); s != nil {
			reason = strings.TrimSpace(s.Stage + " " + s.Code + ": " + s.Message)
		}
	}
	if reason == "" {
		reason = fmt.Sprintf("state %q", res.State)
	}
	msg := fmt.Sprintf("deploy rejected by the agent (HTTP %d): %s", status, reason)
	if hint := deployFailureHint(status); hint != "" {
		msg += "\n  hint: " + hint
	}
	return errors.New(msg)
}

func firstFailedStage(res *deploy.Result) *deploy.StageStatus {
	for i := range res.Stages {
		if res.Stages[i].Status == "failed" || res.Stages[i].Status == "unhealthy" {
			return &res.Stages[i]
		}
	}
	return nil
}

// deployFailureHint turns the agent's HTTP status into an actionable next step.
func deployFailureHint(status int) string {
	switch status {
	case http.StatusForbidden: // 403
		return "the agent rejected the signature. The cosign signer pinned by `statio app add` must " +
			"match your workflow's signing identity exactly. owner/repo is case-sensitive. " +
			"(e.g. AccentioStudios, not accentiostudios), plus the workflow file and branch. " +
			"Check it with `statio app list` and fix it with `statio app edit <service>`. " +
			"(Other 403 causes: the deploy's audience ≠ the agent host, or an expired/replayed payload.)"
	case http.StatusNotFound: // 404
		return "that service is not accepted on the server. Run `sudo statio app add <service>`. " +
			"The name must match `service:` in the workflow AND `name:` in statio.yaml."
	case http.StatusConflict: // 409
		return "replayed or old deploy sequence: this payload is older than one already applied."
	case http.StatusRequestEntityTooLarge: // 413
		return "the payload is too large. Check the size of your statio.yaml."
	case http.StatusUnprocessableEntity: // 422
		return "an env value was rejected (a protected key, or a required value missing)."
	case http.StatusBadRequest: // 400
		return "the deploy spec was rejected. Check your statio.yaml and the action inputs."
	default:
		if status >= 500 {
			return "the agent errored. Read `sudo journalctl -u statio-agent` on the server."
		}
		return ""
	}
}

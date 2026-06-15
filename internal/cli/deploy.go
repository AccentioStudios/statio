package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/accentiostudios/statio/internal/client"
	"github.com/accentiostudios/statio/internal/deploy"
	"github.com/accentiostudios/statio/internal/statiofile"
	"github.com/spf13/cobra"
)

func newDeployCmd() *cobra.Command {
	var (
		target, service, image, digest, statioFile, audience string
		deploySeq                                            int64
		freshness                                            time.Duration
		envs                                                 []string
		strict                                               bool
		timeout                                              time.Duration
	)
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Build, cosign-sign, and send a deploy to the agent (used by the GitHub Action)",
		RunE: func(c *cobra.Command, _ []string) error {
			if target == "" || service == "" || image == "" || digest == "" {
				return fmt.Errorf("--target, --service, --image and --digest are required")
			}
			data, err := os.ReadFile(statioFile)
			if err != nil {
				return fmt.Errorf("read %s: %w", statioFile, err)
			}
			pf, err := statiofile.Parse(data)
			if err != nil {
				return err
			}
			envMap, err := collectEnv(envs)
			if err != nil {
				return err
			}
			if audience == "" {
				audience = target
			}
			now := time.Now().UTC()
			in := client.Inputs{
				Service: service, Repository: image, Digest: digest,
				AppIntent: pf.AppIntent(), EnvOverrides: envMap,
				Proxy: pf.ProxyWire(), DNS: pf.DNSWire(),
				Audience: audience, DeploySeq: deploySeq,
				IssuedAt: now.Format(time.RFC3339), Expiry: now.Add(freshness).Format(time.RFC3339),
			}
			payload, err := client.BuildSpec(in)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), timeout+60*time.Second)
			defer cancel()
			envelope, err := client.SignAndWrap(ctx, payload)
			if err != nil {
				return err
			}
			res, status, err := client.Deploy(ctx, target, envelope, timeout)
			if err != nil {
				return err
			}
			printResult(res, status)
			return exitForResult(res, status, strict)
		},
	}
	f := cmd.Flags()
	f.StringVar(&target, "target", "", "agent MagicDNS host (e.g. statio.tailnet.ts.net); also the signed audience")
	f.StringVar(&service, "service", "", "service slot name (must be accepted on the server)")
	f.StringVar(&image, "image", "", "your app's image repository (no tag/digest)")
	f.StringVar(&digest, "digest", "", "image digest sha256:...")
	f.StringVar(&statioFile, "statio-file", "statio.yaml", "path to the repo's statio.yaml")
	f.StringVar(&audience, "audience", "", "override the signed audience (defaults to --target)")
	f.Int64Var(&deploySeq, "deploy-seq", 0, "monotonic deploy sequence (e.g. github.run_number)")
	f.DurationVar(&freshness, "freshness", 5*time.Minute, "how long the signed payload stays valid")
	f.StringArrayVar(&envs, "env", nil, "env override KEY=VALUE (repeatable; or STATIO_ENV_OVERRIDES)")
	f.BoolVar(&strict, "strict", false, "treat success_degraded as failure")
	f.DurationVar(&timeout, "timeout", 5*time.Minute, "deploy timeout")
	return cmd
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
			return fmt.Errorf("invalid env entry %q (want KEY=VALUE)", line)
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
			return fmt.Errorf("deploy degraded (--strict)")
		}
		return nil
	default:
		return fmt.Errorf("deploy failed: state=%s http=%d", res.State, status)
	}
}

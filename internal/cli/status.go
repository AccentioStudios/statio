package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var target, stateDir string
	cmd := &cobra.Command{
		Use:   "status --target <agent-host>",
		Short: "Check a running agent's health over the tailnet",
		Long: "Queries the agent's /status over the tailnet and prints what it returns (its health and\n" +
			"the apps it accepts).\n\n" +
			"Run it from a machine that is ON the tailnet — your laptop or CI. The server's own OS is\n" +
			"NOT a tailnet peer (the agent runs in userspace via tsnet), so you cannot reach it from\n" +
			"the server itself; to check the agent locally on the server use `sudo statio doctor`.",
		Example: "  statio status --target statio.your-tailnet.ts.net",
		RunE: func(c *cobra.Command, _ []string) error {
			if target == "" {
				// On the server we know the agent's host (its persisted audience) — surface it so the
				// error tells you exactly what to run from a tailnet machine.
				if aud := readAudience(stateDir); aud != "" {
					return fmt.Errorf("--target is required. This server's agent is %s — from a machine on the tailnet run:\n  statio status --target %s", aud, aud)
				}
				return fmt.Errorf("--target is required: the agent's MagicDNS host (e.g. statio.your-tailnet.ts.net). Run it from a machine on the tailnet; on the server use `sudo statio doctor`")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+target+"/status", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("could not reach the agent at %s (are you on the tailnet? is the host right?): %w", target, err)
			}
			defer resp.Body.Close()
			io.Copy(os.Stdout, io.LimitReader(resp.Body, 1<<20))
			fmt.Println()
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "the agent's MagicDNS host (e.g. statio.your-tailnet.ts.net)")
	cmd.Flags().StringVar(&stateDir, "state-dir", "/var/lib/statio", "state dir, used only to suggest this server's agent host in errors")
	return cmd
}

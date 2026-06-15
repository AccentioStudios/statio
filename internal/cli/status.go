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
	var target string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Query the agent's /status over the tailnet",
		RunE: func(c *cobra.Command, _ []string) error {
			if target == "" {
				return fmt.Errorf("--target is required")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+target+"/status", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			io.Copy(os.Stdout, io.LimitReader(resp.Body, 1<<20))
			fmt.Println()
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "agent MagicDNS host")
	return cmd
}

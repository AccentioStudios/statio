package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/accentiostudios/statio/internal/audit"
	"github.com/accentiostudios/statio/internal/client"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	var (
		target, stateDir string
		jsonOut          bool
		limit            int
	)
	cmd := &cobra.Command{
		Use:   "logs <service>",
		Short: "Show the deploy audit log for a service (local file, or --target for a remote agent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			svc := args[0]
			var recs []audit.Record
			if target != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				data, status, err := client.GetLogs(ctx, target, svc, 0)
				if err != nil {
					return err
				}
				if status != 200 {
					return fmt.Errorf("agent returned status %d", status)
				}
				if err := json.Unmarshal(data, &recs); err != nil {
					return fmt.Errorf("decode logs: %w", err)
				}
			} else {
				r, err := audit.Tail(filepath.Join(stateDir, "services", svc, "deploy-audit.jsonl"), limit)
				if err != nil {
					return err
				}
				recs = r
			}
			if jsonOut {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(recs)
			}
			renderRecords(c, recs)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&target, "target", "", "fetch from a remote agent over the tailnet instead of the local file")
	f.StringVar(&stateDir, "state-dir", "/var/lib/statio", "state directory (local read)")
	f.BoolVar(&jsonOut, "json", false, "emit raw JSON records")
	f.IntVar(&limit, "limit", 20, "max records to show")
	return cmd
}

func renderRecords(c *cobra.Command, recs []audit.Record) {
	if len(recs) == 0 {
		c.Println("no deploys recorded")
		return
	}
	for _, r := range recs {
		c.Printf("\n%s  seq=%d  %s  (%dms)\n", r.TS, r.DeploySeq, r.Outcome, r.DurationMS)
		if r.Src != "" {
			c.Printf("  from %s  digest %s\n", r.Src, short(r.Digest))
		}
		for _, s := range r.Stages {
			mark := "✓"
			if s.Status == "failed" || s.Status == "unhealthy" {
				mark = "✗"
			}
			line := fmt.Sprintf("  %s %-12s %-8s", mark, s.Stage, s.Status)
			if s.Code != "" {
				line += " [" + s.Code + "]"
			}
			if s.Message != "" {
				line += " " + s.Message
			}
			c.Println(line)
		}
	}
}

func short(digest string) string {
	if len(digest) > 19 {
		return digest[:19] + "…"
	}
	return digest
}

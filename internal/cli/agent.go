package cli

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/accentiostudios/push/internal/agent"
	"github.com/accentiostudios/push/internal/config"
	"github.com/spf13/cobra"
)

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "agent", Short: "Run the deploy agent (server side)"}
	var configPath string
	run := &cobra.Command{
		Use:   "run",
		Short: "Run the agent under systemd (ExecStart)",
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if err := cfg.ValidateSecretPerms(); err != nil {
				return err
			}
			log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)}))
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return agent.New(cfg, log).Run(ctx)
		},
	}
	run.Flags().StringVar(&configPath, "config", "/etc/push/config.yaml", "path to config.yaml")
	cmd.AddCommand(run)
	return cmd
}

func logLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

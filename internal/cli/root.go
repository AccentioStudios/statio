// Package cli wires the cobra command tree for the single `statio` binary.
package cli

import (
	"context"
	"os"

	"github.com/accentiostudios/statio/internal/selfupdate"
	"github.com/spf13/cobra"
)

// Execute builds and runs the root command.
func Execute(version string) error {
	root := &cobra.Command{
		Use:           "statio",
		Short:         "Deploy to a self-hosted server without SSH or a public port",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		// Runs after a successful command; nudges interactive users when a newer
		// release exists (cached, network at most once a day).
		PersistentPostRun: func(c *cobra.Command, _ []string) { maybeNudgeUpdate(c, version) },
	}
	root.SetVersionTemplate("statio {{.Version}}\n")

	// Group the commands in `--help` so the human-facing ones (setup → apps → operate →
	// maintenance) are obvious and the machine-facing ones (agent, deploy) are clearly labelled
	// as not-for-hand-use. Without groups cobra prints one flat alphabetical list.
	root.AddGroup(
		&cobra.Group{ID: "setup", Title: "Setup:"},
		&cobra.Group{ID: "apps", Title: "Apps & config (on the server):"},
		&cobra.Group{ID: "operate", Title: "Operate (from a machine on the tailnet):"},
		&cobra.Group{ID: "maint", Title: "Maintenance:"},
		&cobra.Group{ID: "internal", Title: "Internal (run by systemd & the GitHub Action — not by hand):"},
	)
	grouped := func(id string, c *cobra.Command) *cobra.Command { c.GroupID = id; return c }

	root.AddCommand(
		grouped("setup", newInitCmd(version)),
		grouped("apps", newAppCmd()),
		grouped("apps", newEnvCmd()),
		grouped("operate", newStatusCmd()),
		grouped("operate", newLogsCmd()),
		grouped("maint", newUpgradeCmd(version)),
		grouped("maint", newDoctorCmd(version)),
		grouped("maint", &cobra.Command{
			Use:   "version",
			Short: "Print the statio version",
			Run:   func(c *cobra.Command, _ []string) { c.Println("statio", version) },
		}),
		grouped("internal", newAgentCmd()),
		grouped("internal", newDeployCmd()),
	)
	return root.Execute()
}

// noNudge lists top-level commands that must never trigger the update check:
// long-running/non-interactive (agent, deploy) or already version-aware
// (version, upgrade, doctor).
var noNudge = map[string]bool{
	"agent": true, "deploy": true, "version": true, "upgrade": true, "doctor": true,
}

func maybeNudgeUpdate(c *cobra.Command, current string) {
	if os.Getenv("STATIO_NO_UPDATE_CHECK") != "" || os.Getenv("CI") != "" {
		return
	}
	if !interactive() || !selfupdate.IsVersion(current) || noNudge[topLevel(c)] {
		return
	}
	latest, ok := selfupdate.CachedLatest(context.Background())
	if !ok || !selfupdate.Outdated(current, latest) {
		return
	}
	warnLine("a new version of statio is available: %s → %s", current, latest)
	info("  upgrade with:  statio upgrade")
}

// topLevel returns the name of the command directly under root (e.g. "agent"
// for `statio agent run`).
func topLevel(c *cobra.Command) string {
	for c.HasParent() && c.Parent().HasParent() {
		c = c.Parent()
	}
	return c.Name()
}

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
	root.AddCommand(
		newAgentCmd(),
		newDeployCmd(),
		newStatusCmd(),
		newEnvCmd(),
		newLogsCmd(),
		newEnableCmd(),
		newInitCmd(version),
		newUpgradeCmd(version),
		newDoctorCmd(version),
		&cobra.Command{
			Use:   "version",
			Short: "Print the statio version",
			Run:   func(c *cobra.Command, _ []string) { c.Println("statio", version) },
		},
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
	warnLine("hay una versión nueva de statio: %s → %s", current, latest)
	info("  actualiza con:  statio upgrade")
}

// topLevel returns the name of the command directly under root (e.g. "agent"
// for `statio agent run`).
func topLevel(c *cobra.Command) string {
	for c.HasParent() && c.Parent().HasParent() {
		c = c.Parent()
	}
	return c.Name()
}

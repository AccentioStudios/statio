package cli

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/accentiostudios/statio/internal/selfupdate"
	"github.com/spf13/cobra"
)

// agentUnit is the systemd unit `statio init server` installs on a server.
const agentUnit = "statio-agent"

// agentUnitActive reports whether the agent unit exists and is currently running.
// It is false on non-systemd hosts (e.g. a dev machine running only the CLI), so the
// upgrade never tries to touch a service that isn't there.
func agentUnitActive() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	// `is-active --quiet` exits 0 only when the unit is loaded and running.
	return exec.Command("systemctl", "is-active", "--quiet", agentUnit).Run() == nil
}

func newUpgradeCmd(current string) *cobra.Command {
	var target string
	var checkOnly, assumeYes, noRestart bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Update statio to the latest release (self-update)",
		Long: "Downloads the latest statio release, verifies its checksum and replaces\n" +
			"the current binary. Re-runnable: does nothing if you are already up to date.",
		RunE: func(c *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			tag := target
			if tag == "" {
				latest, err := selfupdate.Latest(ctx)
				if err != nil {
					return fmt.Errorf("could not query the latest version: %w", err)
				}
				tag = latest
			}

			// Already current (only meaningful when comparing against latest).
			if target == "" && selfupdate.IsVersion(current) && !selfupdate.Outdated(current, tag) {
				okLine("statio is already up to date (%s, latest %s)", current, tag)
				return nil
			}

			info("Installed: %s", current)
			info("Target:    %s", tag)
			if checkOnly {
				if selfupdate.Outdated(current, tag) {
					warnLine("a new version is available: %s → %s", current, tag)
					codeBlock("statio upgrade")
				} else {
					okLine("you are up to date")
				}
				return nil
			}
			if !assumeYes && interactive() {
				ok, err := confirm(fmt.Sprintf("Update statio to %s?", tag))
				if err != nil {
					return err
				}
				if !ok {
					info("cancelled.")
					return nil
				}
			}
			if err := selfupdate.Apply(ctx, tag, info); err != nil {
				return err
			}
			okLine("statio updated to %s", tag)

			// On a server the agent runs as systemd; restart it so it loads the new
			// binary. We only touch it when the unit is actually active — on a dev
			// machine (CLI-only) there's nothing to restart.
			switch {
			case noRestart:
				warnLine("if the agent runs as a service, restart it to use the new binary:")
				codeBlock("sudo systemctl restart statio-agent")
			case agentUnitActive():
				if err := exec.Command("systemctl", "restart", agentUnit).Run(); err != nil {
					warnLine("could not restart the agent automatically (are you running as root?): %v", err)
					codeBlock("sudo systemctl restart statio-agent")
				} else {
					okLine("agent restarted (%s) with the new binary", agentUnit)
				}
			default:
				info("(no active %s agent on this machine; nothing to restart)", agentUnit)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "version", "", "specific version to install (e.g. v1.2.3)")
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only check whether a new version is available, without installing")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&noRestart, "no-restart", false, "do not restart the statio-agent after updating")
	return cmd
}

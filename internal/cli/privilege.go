package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

// requireRoot fails fast — before any prompt — when a server command that reads or writes
// system paths (/etc/statio, the systemd unit) runs without root. Without it the wizard would
// ask every question and only blow up at the first mkdir with "permission denied", wasting the
// user's time. It is a no-op on Windows (dev/CI) and when already running as root.
func requireRoot(c *cobra.Command) error {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return nil
	}
	return fmt.Errorf("this command must run as root (it touches %s)\n  re-run with: sudo %s",
		"/etc/statio", c.CommandPath())
}

// rootPreRun is the cobra PreRunE that enforces requireRoot. Attach it to every server-side
// command that reads or writes /etc/statio so it checks privileges before any interaction.
func rootPreRun(c *cobra.Command, _ []string) error { return requireRoot(c) }

// systemctlAvailable reports whether systemd's systemctl is usable on this host, so we only
// drive the service on real Linux servers (a no-op on dev/CI machines).
func systemctlAvailable() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := exec.LookPath("systemctl")
	return err == nil
}

// startAgent reloads systemd and enables+starts the agent unit. `init server` calls it so the
// operator never has to run systemctl by hand — it already wrote the unit and runs as root.
func startAgent() error {
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if err := exec.Command("systemctl", "enable", "--now", agentUnit).Run(); err != nil {
		return fmt.Errorf("enable --now %s: %w", agentUnit, err)
	}
	return nil
}

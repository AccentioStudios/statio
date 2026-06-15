package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/accentiostudios/statio/internal/config"
	"github.com/accentiostudios/statio/internal/selfupdate"
	"github.com/spf13/cobra"
)

// newDoctorCmd is a `flutter doctor`-style environment check: it reports what is
// installed/configured and flags the gaps, without changing anything.
func newDoctorCmd(version string) *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check your environment for common problems",
		RunE: func(c *cobra.Command, _ []string) error {
			banner("statio doctor", "environment diagnostics")
			ok := true

			doctorVersion(version)
			doctorDocker(&ok)
			doctorTool("git", "repo auto-detect in `statio init repo`", false, &ok)
			doctorTool("gh", "push secrets with `gh secret set` (client)", true, &ok)
			doctorTool("cosign", "sign image+payload in CI (not on the server)", true, &ok)
			doctorConfig(configPath, &ok)
			doctorService()
			doctorGitHub()

			fmt.Println()
			if ok {
				okLine("all good")
			} else {
				warnLine("review the items marked with ✗ above")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/statio/config.yaml", "path to the agent's config.yaml")
	return cmd
}

func doctorVersion(version string) {
	line := fmt.Sprintf("statio %s · %s/%s", version, runtime.GOOS, runtime.GOARCH)
	if !selfupdate.IsVersion(version) {
		warnLine("%s (development build — no version check)", line)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	latest, err := selfupdate.Latest(ctx)
	if err != nil {
		okLine("%s", line)
		return
	}
	if selfupdate.Outdated(version, latest) {
		warnLine("%s — a new one is available: %s (run `statio upgrade`)", line, latest)
	} else {
		okLine("%s (latest)", line)
	}
}

func doctorDocker(ok *bool) {
	if out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Output(); err == nil {
		okLine("docker %s — daemon reachable", strings.TrimSpace(string(out)))
		return
	}
	if _, err := exec.LookPath("docker"); err == nil {
		failLine("docker installed but the daemon is not responding (is it running? socket permissions?)")
	} else {
		failLine("docker not found — required on the server")
	}
	*ok = false
}

func doctorTool(name, hint string, optional bool, ok *bool) {
	if _, err := exec.LookPath(name); err != nil {
		if optional {
			warnLine("%s not found — %s", name, hint)
		} else {
			failLine("%s not found — %s", name, hint)
			*ok = false
		}
		return
	}
	ver := ""
	if out, err := exec.Command(name, "--version").Output(); err == nil {
		ver = " " + strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	}
	okLine("%s%s — %s", name, ver, hint)
}

func doctorConfig(path string, ok *bool) {
	if _, err := os.Stat(path); err != nil {
		info("agent config: %s does not exist (normal if this machine is not the server)", path)
		return
	}
	if _, err := config.Load(path); err != nil {
		failLine("config %s invalid: %v", path, err)
		*ok = false
		return
	}
	okLine("agent config: %s (valid)", path)
}

func doctorService() {
	if runtime.GOOS != "linux" {
		return
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return
	}
	out, _ := exec.Command("systemctl", "is-active", "statio-agent").Output()
	switch state := strings.TrimSpace(string(out)); state {
	case "active":
		okLine("statio-agent service: active")
	case "":
		info("statio-agent service: unknown state")
	default:
		info("statio-agent service: %s", state)
	}
}

func doctorGitHub() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := selfupdate.Latest(ctx); err != nil {
		warnLine("GitHub Releases not reachable: %v", err)
		return
	}
	okLine("GitHub Releases reachable")
}

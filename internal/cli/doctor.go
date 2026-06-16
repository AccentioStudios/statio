package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/accentiostudios/statio/internal/config"
	"github.com/accentiostudios/statio/internal/selfupdate"
	"github.com/spf13/cobra"
)

// newDoctorCmd reports what is installed/configured for statio and flags the gaps; with --fix it
// also repairs the safe ones.
func newDoctorCmd(version string) *cobra.Command {
	var configPath string
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check your environment for common problems (and fix some with --fix)",
		RunE: func(c *cobra.Command, _ []string) error {
			banner("statio doctor", "environment diagnostics")
			ok := true
			// Resolve everything we can without root; flag what's left for a sudo re-run.
			root := runtime.GOOS == "windows" || os.Geteuid() == 0
			needsSudo := false

			doctorVersion(version)
			doctorDocker(&ok)
			doctorDockerLogin()
			doctorTool("git", "repo auto-detect in `statio init repo` (client only)", true, &ok)
			doctorGH()
			doctorCosign()
			doctorConfig(configPath, &ok)
			doctorState(fix, root, &ok, &needsSudo)
			doctorGitHub()

			fmt.Println()
			switch {
			case ok:
				okLine("all good")
			case needsSudo:
				warnLine("re-run `sudo statio doctor --fix` to apply the fixes that need root")
			case !fix:
				warnLine("review the items above; `sudo statio doctor --fix` can resolve some automatically")
			default:
				warnLine("some items still need attention — see above")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/statio/config.yaml", "path to the agent's config.yaml")
	cmd.Flags().BoolVar(&fix, "fix", false, "attempt safe auto-fixes for problems doctor can resolve (needs root)")
	return cmd
}

// isServer reports whether this machine has been set up as a statio server (init server ran).
// It only stats paths (no traversal), so it works even as a non-root user.
func isServer() bool {
	for _, p := range []string{"/etc/systemd/system/statio-agent.service", "/etc/statio/config.yaml", "/etc/statio"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// doctorCosign is server-aware: cosign runs in CI (the Action installs it), never on the server,
// so its absence is not a problem to flag here.
func doctorCosign() {
	if isServer() {
		info("cosign: not needed on the server (the Action installs and runs it in CI)")
		return
	}
	if _, err := exec.LookPath("cosign"); err != nil {
		info("cosign: not installed locally — the Action installs it in CI, so this is usually fine")
		return
	}
	okLine("cosign installed")
}

// doctorState checks the server's state dir and agent health, and (with --fix, as root) repairs
// the common failure: /var/lib/statio missing makes systemd fail the unit with 226/NAMESPACE.
// Fixes that need root are deferred (with needsSudo set) instead of attempted, when not root.
func doctorState(fix, root bool, ok, needsSudo *bool) {
	if !isServer() {
		return
	}
	if !root {
		info("server detected — run `sudo statio doctor` for the full check (config, docker login, service need root)")
	}
	const stateDir = "/var/lib/statio"
	missing := false
	if _, err := os.Stat(stateDir); err != nil {
		switch {
		case fix && root:
			if err := os.MkdirAll(stateDir, 0o700); err == nil {
				okLine("fixed: created %s", stateDir)
			} else {
				failLine("%s missing and could not create it: %v", stateDir, err)
				*ok = false
				missing = true
			}
		case fix && !root:
			failLine("%s is missing — re-run `sudo statio doctor --fix` to create it (needs root)", stateDir)
			*ok = false
			*needsSudo = true
			missing = true
		default:
			failLine("%s is missing — the agent crash-loops with 226/NAMESPACE. Run `sudo statio doctor --fix`", stateDir)
			*ok = false
			missing = true
		}
	}

	if runtime.GOOS != "linux" {
		return
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return
	}
	out, _ := exec.Command("systemctl", "is-active", "statio-agent").Output()
	state := strings.TrimSpace(string(out))
	switch state {
	case "active", "active (running)":
		okLine("statio-agent service: active")
	case "":
		info("statio-agent service: unknown state")
	default:
		switch {
		case fix && root && !missing:
			_ = exec.Command("systemctl", "daemon-reload").Run()
			if err := exec.Command("systemctl", "restart", "statio-agent").Run(); err == nil {
				okLine("fixed: restarted statio-agent (verify with `systemctl status statio-agent`)")
			} else {
				warnLine("statio-agent: %s — restart failed: %v (see `journalctl -xeu statio-agent`)", state, err)
				*ok = false
			}
		case fix && !root:
			warnLine("statio-agent service: %s — re-run `sudo statio doctor --fix` to restart it (needs root)", state)
			*ok = false
			*needsSudo = true
		default:
			warnLine("statio-agent service: %s — see `journalctl -xeu statio-agent` (or `sudo statio doctor --fix`)", state)
			*ok = false
		}
	}
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

// doctorDockerLogin reports whether docker is logged in to a registry (GHCR, Docker Hub, or any),
// which the agent needs to pull a private image. It reads docker's config.json auths. On the
// server the agent pulls as root, so run `sudo statio doctor` there to check root's login.
func doctorDockerLogin() {
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == "" {
		// The agent pulls images as root, so when we are root the login that matters is root's
		// (~root/.docker). That's why `sudo statio doctor` reports the agent's real situation.
		home := "/root"
		if runtime.GOOS == "windows" || os.Geteuid() != 0 {
			home, _ = os.UserHomeDir()
		}
		dir = filepath.Join(home, ".docker")
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		info("docker login: none detected — `docker login ghcr.io` lets the agent pull private images")
		return
	}
	var c struct {
		Auths      map[string]json.RawMessage `json:"auths"`
		CredsStore string                     `json:"credsStore"`
	}
	if json.Unmarshal(data, &c) != nil {
		info("docker login: could not parse the docker config")
		return
	}
	if len(c.Auths) > 0 {
		regs := make([]string, 0, len(c.Auths))
		for r := range c.Auths {
			regs = append(regs, r)
		}
		okLine("docker login: %s", strings.Join(regs, ", "))
		return
	}
	if c.CredsStore != "" {
		info("docker login: uses a credential helper (%s) — can't list registries from here", c.CredsStore)
		return
	}
	info("docker login: not logged in to any registry — `docker login ghcr.io` (or docker.io)")
}

// doctorGH checks the gh CLI is installed AND logged in. It probes auth the same way `app add`
// does (as $SUDO_USER when run as root), so the result reflects what private-repo detection sees.
func doctorGH() {
	if _, err := exec.LookPath("gh"); err != nil {
		warnLine("gh not found — needed for `gh secret set` and auto-detecting private repos in `statio app add`")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ghCommand(ctx, "auth", "status").Run(); err != nil {
		warnLine("gh installed but NOT logged in (`gh auth login`) — private-repo detection in `app add` falls back to manual entry")
		return
	}
	okLine("gh installed and logged in")
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
		if os.IsPermission(err) || isServer() {
			info("agent config: %s present but not readable here — run `sudo statio doctor` to validate it", path)
		} else {
			info("agent config: %s does not exist (normal if this machine is not the server)", path)
		}
		return
	}
	if _, err := config.Load(path); err != nil {
		failLine("config %s invalid: %v", path, err)
		*ok = false
		return
	}
	okLine("agent config: %s (valid)", path)
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

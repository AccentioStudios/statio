package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/accentiostudios/statio/internal/config"
	"github.com/accentiostudios/statio/internal/fsutil"
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
			doctorTool("git", "repo auto-detect in `statio init repo` (client only)", true, &ok)
			doctorGH()
			doctorCosign()
			doctorConfig(configPath, fix, root, &ok, &needsSudo)
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
		// Surface WHY it's down. The journal needs root, which is why a hand-run
		// `journalctl` as a normal user shows "No entries" — doctor reads it as root for you.
		if root {
			if cause := lastAgentLog(); cause != "" {
				info("  ↳ last agent log: %s", cause)
			}
		}
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

// lastAgentLog returns the most recent statio-agent message from the journal (root only; the
// system journal is unreadable to a normal user, which is why a hand-run `journalctl` shows
// "No entries"). `-o cat` strips the syslog prefix so we get the agent's raw stderr line.
func lastAgentLog() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	out, err := exec.Command("journalctl", "-u", "statio-agent", "--no-pager", "-n", "30", "-o", "cat").Output()
	if err != nil {
		return ""
	}
	return pickAgentLogLine(string(out))
}

// pickAgentLogLine selects the agent's own error from a `journalctl -o cat` blob. systemd's
// lifecycle lines ("Failed to start…", "statio-agent.service: …", "Stopped…") are interleaved
// with the agent's stderr, and the *last* line is usually systemd's, not the cause. So we prefer
// the last line the binary itself printed (it prefixes fatal errors with "statio: ", see
// cmd/statio/main.go), and only fall back to a non-systemd line if there is none.
func pickAgentLogLine(blob string) string {
	lines := strings.Split(strings.TrimSpace(blob), "\n")
	systemd := func(s string) bool {
		if strings.HasPrefix(s, "statio-agent.service") {
			return true
		}
		for _, p := range []string{
			"Starting ", "Started ", "Stopping ", "Stopped ", "Failed to start",
			"Scheduled restart", "Main process", "Consumed ", "Deactivated ", "--",
		} {
			if strings.HasPrefix(s, p) {
				return true
			}
		}
		return false
	}
	fallback := ""
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "statio:") {
			return s // the binary's own fatal error — the real cause
		}
		if fallback == "" && !systemd(s) {
			fallback = s
		}
	}
	return fallback
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

func doctorConfig(path string, fix, root bool, ok, needsSudo *bool) {
	if _, err := os.Stat(path); err != nil {
		if os.IsPermission(err) || isServer() {
			info("agent config: %s present but not readable here — run `sudo statio doctor` to validate it", path)
		} else {
			info("agent config: %s does not exist (normal if this machine is not the server)", path)
		}
		return
	}
	cfg, err := config.Load(path)
	if err != nil {
		failLine("config %s invalid: %v", path, err)
		*ok = false
		return
	}
	okLine("agent config: %s (valid)", path)
	doctorSecretFiles(cfg, fix, root, ok, needsSudo)
}

// doctorSecretFiles runs the exact check the agent runs at startup (ValidateSecretPerms): every
// secret file the config references must exist and be 0600 root. This is the gap that let a
// crash-loop slip past doctor before — `config.Load` validated structure but not the secret files,
// so doctor said "valid" while the agent died on a missing/insecure secret. With --fix (as root) it
// tightens loose perms; a *missing* secret can't be fabricated, so it points at the init step that
// regenerates it.
func doctorSecretFiles(cfg *config.Config, fix, root bool, ok, needsSudo *bool) {
	files := cfg.SecretFiles()
	if len(files) == 0 {
		return
	}
	allOK := true
	for _, p := range files {
		if _, err := os.Stat(p); err != nil {
			if os.IsNotExist(err) {
				failLine("secret file missing: %s — the agent crash-loops at boot (`stat %s: no such file`). %s", p, p, regenHint(p))
			} else {
				failLine("secret file %s: %v", p, err)
			}
			*ok, allOK = false, false
			continue
		}
		if err := fsutil.CheckPerm(p); err != nil {
			switch {
			case fix && root:
				if e := os.Chmod(p, 0o600); e == nil {
					if e2 := os.Chown(p, 0, 0); e2 == nil && fsutil.CheckPerm(p) == nil {
						okLine("fixed: secured %s (chmod 600, root owner)", p)
						continue
					}
				}
				failLine("secret file %s: %v (auto-fix failed — fix it by hand)", p, err)
				*ok, allOK = false, false
			case fix && !root:
				failLine("secret file %s: %v — re-run `sudo statio doctor --fix` to secure it (needs root)", p, err)
				*ok, allOK, *needsSudo = false, false, true
			default:
				failLine("secret file %s: %v — `sudo statio doctor --fix` can secure it", p, err)
				*ok, allOK = false, false
			}
			continue
		}
	}
	if allOK {
		okLine("agent secret files present and 0600 root")
	}
}

// regenHint maps a secret file to the init step that regenerates it (a real secret can't be
// auto-created — only the wizard knows its contents).
func regenHint(path string) string {
	switch filepath.Base(path) {
	case "oauth.json", "oauth":
		return "Re-run `sudo statio init server` to regenerate it."
	case "npmplus.json", "cloudflare.json":
		return "Re-run `sudo statio init integrations` to regenerate it."
	default:
		return "Re-create it, or re-run the matching `statio init` step."
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

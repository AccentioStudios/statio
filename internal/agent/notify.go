package agent

import "github.com/coreos/go-systemd/v22/daemon"

// notifyReady signals systemd (Type=notify) that the agent is up and serving on the
// tailnet. A no-op when not run under systemd.
func notifyReady() {
	_, _ = daemon.SdNotify(false, daemon.SdNotifyReady)
}

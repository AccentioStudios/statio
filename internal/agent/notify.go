package agent

import (
	"context"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
)

// notifyReady signals systemd (Type=notify) that the agent is up and serving on the
// tailnet. A no-op when not run under systemd.
func notifyReady() {
	_, _ = daemon.SdNotify(false, daemon.SdNotifyReady)
}

// startWatchdog feeds the systemd watchdog (WatchdogSec= in the unit) at half its interval so a
// HEALTHY agent is not killed. Without this, Type=notify + WatchdogSec makes systemd SIGABRT the
// agent every WatchdogSec — it joins the tailnet, then dies ~30s later and crash-loops forever
// (each window briefly shows "active", masking the loop). It is a no-op when the watchdog is not
// enabled (not under systemd, or no WatchdogSec set). The pinger stops when ctx is cancelled.
func startWatchdog(ctx context.Context) {
	interval, err := daemon.SdWatchdogEnabled(false)
	if err != nil || interval <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(interval / 2)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_, _ = daemon.SdNotify(false, daemon.SdNotifyWatchdog)
			}
		}
	}()
}

package cli

import "testing"

func TestPickAgentLogLine(t *testing.T) {
	// A real crash-loop blob (journalctl -o cat): the agent's own "statio:" line is buried among
	// systemd lifecycle lines, and is NOT the last line. We must return it, not systemd's noise.
	blob := `Starting statio deploy agent (tailnet-only)...
statio: tailnet up: tsnet.Up: tsnet: error resolving auth key: oauth authkeys require --advertise-tags
statio-agent.service: Main process exited, code=exited, status=1/FAILURE
statio-agent.service: Failed with result 'exit-code'.
Failed to start statio deploy agent (tailnet-only).
statio-agent.service: Scheduled restart job, restart counter is at 11379.
Stopped statio deploy agent (tailnet-only).`
	want := "statio: tailnet up: tsnet.Up: tsnet: error resolving auth key: oauth authkeys require --advertise-tags"
	if got := pickAgentLogLine(blob); got != want {
		t.Errorf("pickAgentLogLine() = %q, want the agent's statio: line %q", got, want)
	}

	// No agent line at all → fall back to the last non-systemd line (here: none → "").
	onlySystemd := "Starting statio deploy agent (tailnet-only)...\nstatio-agent.service: Failed with result 'exit-code'.\nStopped statio deploy agent (tailnet-only)."
	if got := pickAgentLogLine(onlySystemd); got != "" {
		t.Errorf("pickAgentLogLine(systemd-only) = %q, want empty", got)
	}

	// Empty input → empty output.
	if got := pickAgentLogLine(""); got != "" {
		t.Errorf("pickAgentLogLine(\"\") = %q, want empty", got)
	}
}

func TestRegenHint(t *testing.T) {
	cases := map[string]string{
		"/etc/statio/secrets/oauth.json":      "Re-run `sudo statio init server` to regenerate it.",
		"/etc/statio/secrets/oauth":           "Re-run `sudo statio init server` to regenerate it.",
		"/etc/statio/secrets/npmplus.json":    "Re-run `sudo statio init integrations` to regenerate it.",
		"/etc/statio/secrets/cloudflare.json": "Re-run `sudo statio init integrations` to regenerate it.",
		"/etc/statio/secrets/mystery.json":    "Re-create it, or re-run the matching `statio init` step.",
	}
	for path, want := range cases {
		if got := regenHint(path); got != want {
			t.Errorf("regenHint(%q) = %q, want %q", path, got, want)
		}
	}
}

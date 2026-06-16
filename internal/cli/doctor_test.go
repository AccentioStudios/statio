package cli

import "testing"

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

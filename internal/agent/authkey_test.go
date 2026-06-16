package agent

import "testing"

func TestWithOAuthKeyAttrs(t *testing.T) {
	cases := map[string]string{
		// OAuth client secret → forced persistent + pre-authorized so the agent node isn't reaped.
		"tskey-client-abc123": "tskey-client-abc123?ephemeral=false&preauthorized=true",
		// Pre-minted auth key → untouched (it already carries its own tags/attributes).
		"tskey-auth-xyz": "tskey-auth-xyz",
		// Already carries attributes → leave the operator's choice alone.
		"tskey-client-abc?ephemeral=true": "tskey-client-abc?ephemeral=true",
		// Empty → unchanged.
		"": "",
	}
	for in, want := range cases {
		if got := withOAuthKeyAttrs(in); got != want {
			t.Errorf("withOAuthKeyAttrs(%q) = %q, want %q", in, got, want)
		}
	}
}

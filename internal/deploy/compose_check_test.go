package deploy

import "testing"

func TestStandaloneComposeIsV2(t *testing.T) {
	cases := map[string]bool{
		"2.17.2":  true,
		"v2.38.2": true,
		"2.0.1":   true,
		"1.29.2":  false, // EOL python compose: different CLI surface, must be rejected
		"v1.29.2": false,
		"3.0.0":   false, // unknown future major: don't assume compatibility
		"":        false,
	}
	for v, want := range cases {
		if got := standaloneComposeIsV2(v); got != want {
			t.Errorf("standaloneComposeIsV2(%q) = %v, want %v", v, got, want)
		}
	}
}

package cli

import "testing"

func TestParseOwnerRepo(t *testing.T) {
	ok := map[string][2]string{
		"accentiostudios/api":                          {"accentiostudios", "api"},
		"https://github.com/accentiostudios/api":       {"accentiostudios", "api"},
		"https://github.com/accentiostudios/api.git":   {"accentiostudios", "api"},
		"git@github.com:accentiostudios/api.git":       {"accentiostudios", "api"},
		"ssh://git@github.com/accentiostudios/api.git": {"accentiostudios", "api"},
		"github.com/accentiostudios/api":               {"accentiostudios", "api"},
		"alvarog/mi-api":                               {"alvarog", "mi-api"}, // personal account
		"https://github.com/alvarog/mi-api/":           {"alvarog", "mi-api"},
		"https://user@github.com/org/repo.git":         {"org", "repo"},
	}
	for in, want := range ok {
		o, r, err := parseOwnerRepo(in)
		if err != nil {
			t.Errorf("parseOwnerRepo(%q) error: %v", in, err)
			continue
		}
		if o != want[0] || r != want[1] {
			t.Errorf("parseOwnerRepo(%q) = %q/%q, want %q/%q", in, o, r, want[0], want[1])
		}
	}

	bad := []string{"", "api", "https://github.com/", "https://github.com/owner", "/repo", "owner/"}
	for _, in := range bad {
		if _, _, err := parseOwnerRepo(in); err == nil {
			t.Errorf("parseOwnerRepo(%q) should have errored", in)
		}
	}
}

func TestBuildIdentity(t *testing.T) {
	got := buildIdentity("accentiostudios", "api", "deploy.yml", "main")
	want := "https://github.com/accentiostudios/api/.github/workflows/deploy.yml@refs/heads/main"
	if got != want {
		t.Errorf("buildIdentity = %q, want %q", got, want)
	}
}

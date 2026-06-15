package cli

import "testing"

func TestGhcrImage(t *testing.T) {
	cases := map[string]string{
		"AccentioStudios/bohemian_gym_api": "ghcr.io/accentiostudios/bohemian_gym_api",
		"octocat/Hello-World":              "ghcr.io/octocat/hello-world",
		"lower/already":                    "ghcr.io/lower/already",
	}
	for in, want := range cases {
		owner, repo, err := parseOwnerRepo(in)
		if err != nil {
			t.Fatalf("parseOwnerRepo(%q): %v", in, err)
		}
		if got := ghcrImage(owner, repo); got != want {
			t.Errorf("ghcrImage(%q) = %q, want %q (GHCR requires lowercase)", in, got, want)
		}
	}
}

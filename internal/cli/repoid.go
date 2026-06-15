package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
	repoRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// parseOwnerRepo extracts owner and repo from whatever form a user is likely to paste:
//   - owner/repo
//   - https://github.com/owner/repo[.git]
//   - git@github.com:owner/repo.git   (scp-like ssh)
//   - ssh://git@github.com/owner/repo.git
//   - github.com/owner/repo
//
// "owner" is the GitHub account login — a USER or an ORGANIZATION (GitHub does not
// distinguish them in the URL/identity), so a personal account works the same way.
func parseOwnerRepo(s string) (owner, repo string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", fmt.Errorf("empty")
	}
	// scp-like ssh (git@host:owner/repo) — only when there is no scheme.
	if !strings.Contains(s, "://") {
		if at := strings.Index(s, "@"); at >= 0 {
			if colon := strings.Index(s, ":"); colon > at {
				s = s[colon+1:]
			}
		}
	}
	for _, p := range []string{"https://", "http://", "ssh://", "git://"} {
		s = strings.TrimPrefix(s, p)
	}
	// strip a leading userinfo (e.g. git@github.com/...) before the first slash.
	if at := strings.Index(s, "@"); at >= 0 && at < strings.Index(s+"/", "/") {
		s = s[at+1:]
	}
	for _, h := range []string{"github.com/", "www.github.com/"} {
		s = strings.TrimPrefix(s, h)
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	s = strings.TrimSuffix(s, "/")

	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected owner/repo, got %q", s)
	}
	owner, repo = parts[0], strings.TrimSuffix(parts[1], ".git")
	if !ownerRe.MatchString(owner) {
		return "", "", fmt.Errorf("invalid GitHub owner %q", owner)
	}
	if !repoRe.MatchString(repo) {
		return "", "", fmt.Errorf("invalid repo name %q", repo)
	}
	return owner, repo, nil
}

// buildIdentity composes the cosign keyless certificate identity (SAN) that GitHub Actions
// produces for a workflow run, and that the agent matches EXACTLY.
func buildIdentity(owner, repo, workflow, branch string) string {
	return fmt.Sprintf("https://github.com/%s/%s/.github/workflows/%s@refs/heads/%s", owner, repo, workflow, branch)
}

// detectOwnerRepo reads `git remote get-url origin` (a LOCAL operation — works for private
// repos, no auth/API) and parses owner/repo from it.
func detectOwnerRepo() (owner, repo string, ok bool) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", "", false
	}
	o, r, err := parseOwnerRepo(strings.TrimSpace(string(out)))
	if err != nil {
		return "", "", false
	}
	return o, r, true
}

// detectWorkflows lists existing GitHub Actions workflow files in the current repo.
func detectWorkflows() []string {
	var out []string
	for _, ext := range []string{"*.yml", "*.yaml"} {
		m, _ := filepath.Glob(filepath.Join(".github", "workflows", ext))
		out = append(out, m...)
	}
	return out
}

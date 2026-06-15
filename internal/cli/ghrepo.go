package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// repoInfo is what `app add` could learn about a GitHub repo before accepting the app, so the
// wizard can pre-fill the branch and infer the image path instead of asking blind.
type repoInfo struct {
	Known         bool   // did we successfully read the repo?
	Private       bool   // visibility, when Known
	DefaultBranch string // e.g. "main", when Known
	Source        string // "public" (unauth API) or "gh" (authenticated CLI)
	GHInstalled   bool   // was the gh CLI found on PATH?
	Note          string // user-facing hint when Known is false
}

// inspectGitHubRepo learns whether owner/repo is public or private and its default branch.
// Public repos are read from the unauthenticated GitHub API. A "not found" there means the repo
// is private (or doesn't exist), so it falls back to `gh api`, which needs gh installed and
// logged in. When neither works it returns Known=false with a Note telling the user to type the
// details by hand. Detection is best-effort and never fails the wizard.
func inspectGitHubRepo(ctx context.Context, owner, repo string) repoInfo {
	if r, ok := ghPublicRepo(ctx, owner, repo); ok {
		return r
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return repoInfo{Note: "private or not found, and gh isn't installed here — enter the details by hand"}
	}
	if r, ok := ghCLIRepo(ctx, owner, repo); ok {
		return r
	}
	return repoInfo{GHInstalled: true, Note: "gh is installed but couldn't read the repo (not logged in, or no access) — enter the details by hand"}
}

// ghPublicRepo reads a repo from the unauthenticated GitHub API. A 200 there means it is public.
func ghPublicRepo(ctx context.Context, owner, repo string) (repoInfo, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo), nil)
	if err != nil {
		return repoInfo{}, false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "statio")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return repoInfo{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return repoInfo{}, false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
	}
	if json.Unmarshal(body, &out) != nil {
		return repoInfo{}, false
	}
	return repoInfo{Known: true, Private: out.Private, DefaultBranch: out.DefaultBranch, Source: "public", GHInstalled: true}, true
}

// ghCLIRepo reads a (possibly private) repo through the authenticated gh CLI.
func ghCLIRepo(ctx context.Context, owner, repo string) (repoInfo, bool) {
	out, err := exec.CommandContext(ctx, "gh", "api", fmt.Sprintf("repos/%s/%s", owner, repo)).Output()
	if err != nil {
		return repoInfo{}, false
	}
	var o struct {
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
	}
	if json.Unmarshal(out, &o) != nil {
		return repoInfo{}, false
	}
	return repoInfo{Known: true, Private: o.Private, DefaultBranch: o.DefaultBranch, Source: "gh", GHInstalled: true}, true
}

// ghcrImage returns the GitHub Container Registry path for owner/repo. GHCR requires the path to
// be lowercase, so "AccentioStudios/Api" becomes "ghcr.io/accentiostudios/api".
func ghcrImage(owner, repo string) string {
	return "ghcr.io/" + strings.ToLower(owner) + "/" + strings.ToLower(repo)
}

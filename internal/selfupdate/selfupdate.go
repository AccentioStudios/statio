// Package selfupdate checks GitHub Releases for newer statio versions and
// replaces the running binary in place. It backs `statio upgrade`, the
// `statio doctor` version line, and the background "update available" nudge.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Repo is the GitHub owner/repo that publishes statio releases.
const Repo = "accentiostudios/statio"

const (
	apiLatest = "https://api.github.com/repos/" + Repo + "/releases/latest"
	checkTTL  = 24 * time.Hour
)

// Latest returns the tag_name of the newest published release (e.g. "v1.2.3").
func Latest(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiLatest, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github respondió %s", resp.Status)
	}
	var body struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.Tag == "" {
		return "", fmt.Errorf("respuesta sin tag_name")
	}
	return body.Tag, nil
}

func norm(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	return s
}

// IsVersion reports whether s is a real release version (vX.Y.Z), not "dev".
func IsVersion(s string) bool { return semver.IsValid(norm(s)) }

// Compare returns -1, 0, +1 comparing two version strings (semver order).
func Compare(a, b string) int { return semver.Compare(norm(a), norm(b)) }

// Outdated reports whether latest is strictly newer than current. Non-version
// inputs (e.g. a "dev" build) are never reported as outdated.
func Outdated(current, latest string) bool {
	if !IsVersion(current) || !IsVersion(latest) {
		return false
	}
	return Compare(current, latest) < 0
}

type cacheEntry struct {
	CheckedAt int64  `json:"checked_at"`
	Latest    string `json:"latest"`
}

func cachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "statio", "update-check.json")
}

// CachedLatest returns the newest known release tag, using a 24h on-disk cache
// so the network is hit at most once a day. ok is false on any error — callers
// should treat that as "no info" and stay silent.
func CachedLatest(ctx context.Context) (latest string, ok bool) {
	p := cachePath()
	if p != "" {
		if b, err := os.ReadFile(p); err == nil {
			var c cacheEntry
			if json.Unmarshal(b, &c) == nil && c.Latest != "" &&
				time.Since(time.Unix(c.CheckedAt, 0)) < checkTTL {
				return c.Latest, true
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tag, err := Latest(ctx)
	if err != nil {
		return "", false
	}
	if p != "" {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if b, err := json.Marshal(cacheEntry{CheckedAt: time.Now().Unix(), Latest: tag}); err == nil {
			_ = os.WriteFile(p, b, 0o644)
		}
	}
	return tag, true
}

// Apply downloads the release asset for the given tag, verifies its sha256
// against checksums.txt, and atomically replaces the running executable.
// logf (may be nil) receives human-readable progress lines.
func Apply(ctx context.Context, tag string, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	asset := fmt.Sprintf("statio_%s_%s", runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s", Repo, tag)

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	dir := filepath.Dir(self)

	logf("Descargando %s (%s)…", asset, tag)
	bin, err := download(ctx, base+"/"+asset, 200<<20)
	if err != nil {
		return err
	}
	sums, err := download(ctx, base+"/checksums.txt", 1<<20)
	if err != nil {
		return err
	}
	want := checksumFor(string(sums), asset)
	if want == "" {
		return fmt.Errorf("no se encontró el checksum de %s", asset)
	}
	sum := sha256.Sum256(bin)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum no coincide (esperado %s, obtenido %s)", want, got)
	}

	// Write next to the target, then rename over it (atomic on POSIX; works even
	// while the old binary is running). Requires write access to the install dir.
	tmp, err := os.CreateTemp(dir, ".statio-*.tmp")
	if err != nil {
		return fmt.Errorf("sin permiso de escritura en %s (prueba con sudo): %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, self); err != nil {
		return fmt.Errorf("no se pudo reemplazar %s (prueba con sudo): %w", self, err)
	}
	logf("Instalado en %s", self)
	return nil
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func checksumFor(sums, asset string) string {
	for _, line := range strings.Split(sums, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == asset {
			return f[0]
		}
	}
	return ""
}

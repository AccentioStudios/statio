package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/accentiostudios/push/internal/env"
	"github.com/accentiostudios/push/internal/fsutil"
	"github.com/accentiostudios/push/internal/spec"
)

const composeFileName = "compose.yaml"

// RunSet is the complete set of generated artifacts for one deploy: the compose file and
// each service's rendered env. It is written to tmpfs (/run/push/<slot>) — never persistent
// disk — and snapshotted (also in tmpfs) so a rollback can restore the exact prior set.
type RunSet struct {
	Compose []byte
	Env     map[string][]byte // service name -> rendered KEY=VALUE env
}

// routeEnv builds each service's env file from the flat merged env: the keys the service
// declared (svc.Env) resolved from the merged map, plus its non-secret env_inline literals.
// This is the per-service routing (DATABASE_URL -> api, POSTGRES_PASSWORD -> db).
func routeEnv(intent *spec.AppIntent, merged map[string]string) map[string][]byte {
	out := make(map[string][]byte)
	for i := range intent.Services {
		s := &intent.Services[i]
		if len(s.Env) == 0 && len(s.EnvInline) == 0 {
			continue
		}
		kv := make(map[string]string, len(s.Env)+len(s.EnvInline))
		for k, v := range s.EnvInline {
			kv[k] = v
		}
		for _, k := range s.Env {
			if v, ok := merged[k]; ok {
				kv[k] = v
			}
		}
		out[s.Name] = env.Render(kv)
	}
	return out
}

// runHash binds the whole generated set (compose, which embeds the digest, + every env
// file) for idempotency: an identical set => no-op; any change => recreate.
func runHash(rs RunSet) string {
	h := sha256.New()
	h.Write(rs.Compose)
	names := make([]string, 0, len(rs.Env))
	for n := range rs.Env {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h.Write([]byte{0})
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(rs.Env[n])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeRunSet atomically writes the compose file and per-service env files (0600) into dir,
// creating it (0700). dir is a tmpfs path so secrets never touch persistent disk.
func writeRunSet(dir string, rs RunSet) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := fsutil.SecureWrite(filepath.Join(dir, composeFileName), rs.Compose, 0o600); err != nil {
		return err
	}
	for name, data := range rs.Env {
		if err := fsutil.SecureWrite(filepath.Join(dir, name+".env"), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// readRunSet reads a previously written set (compose.yaml + *.env) back, for rollback.
func readRunSet(dir string) (RunSet, error) {
	var rs RunSet
	c, err := os.ReadFile(filepath.Join(dir, composeFileName))
	if err != nil {
		return rs, err
	}
	rs.Compose = c
	rs.Env = map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return rs, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".env") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return rs, err
		}
		rs.Env[strings.TrimSuffix(e.Name(), ".env")] = b
	}
	return rs, nil
}

// composeUp recreates the service from the generated compose in dir, via an explicit argv
// array (never a shell string), with the project pinned to the slot. It is run WITHOUT
// --env-file: the digest is already baked literally into the compose, so there is no
// interpolation surface. It fails closed if the compose file is missing/empty (e.g. a
// recreate attempted after a reboot cleared tmpfs before a fresh deploy repopulated it).
func composeUp(ctx context.Context, dir, slot string) (string, error) {
	composePath := filepath.Join(dir, composeFileName)
	if fi, err := os.Stat(composePath); err != nil || fi.Size() == 0 {
		return "", fmt.Errorf("compose file missing or empty (redeploy required after reboot)")
	}
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", composePath, "-p", slot, "up", "-d", "--no-build")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Return raw output for the journal only; the pipeline replaces it with a fixed,
		// sanitized message before anything reaches the deploy response (invariant #11/#23).
		return strings.TrimSpace(string(out)), fmt.Errorf("docker compose up failed: %w", err)
	}
	return "", nil
}

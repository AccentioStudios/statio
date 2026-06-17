package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/accentiostudios/statio/internal/spec"
)

// DockerPuller pulls an image by digest via the docker CLI. Pull-by-digest is content
// addressed: docker verifies the manifest hashes the requested digest, so the resolved
// digest equals the requested one (the pipeline still re-checks).
//
// For a PRIVATE image the CI forwards a short-lived pull token in the envelope; Pull writes it to a
// throwaway docker config in a private temp dir and points the `docker pull` child at it via
// DOCKER_CONFIG, then deletes it. Nothing is persisted and no shared `docker login` is needed.
type DockerPuller struct{}

// Pull pulls ref (repository@sha256:...) and returns the resolved digest. auth is nil for a public
// image (anonymous pull) or the transient CI-forwarded credential for a private one.
func (DockerPuller) Pull(ctx context.Context, ref string, auth *spec.RegistryAuth) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "pull", "--quiet", ref)
	if auth != nil && auth.Password != "" {
		dir, cleanup, err := writeEphemeralDockerConfig(ref, auth)
		if err != nil {
			return "", fmt.Errorf("prepare registry credential: %w", err)
		}
		defer cleanup()
		// A private temp DOCKER_CONFIG for THIS pull only; the rest of the env is inherited.
		cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+dir)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("docker pull failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// ref is repo@digest; the digest is authoritative after a successful by-digest pull.
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[i+1:], nil
	}
	return "", nil
}

// writeEphemeralDockerConfig writes a one-host config.json (0600) in a private temp dir and returns
// the dir plus a cleanup func that removes it. The credential never touches persistent storage.
func writeEphemeralDockerConfig(ref string, auth *spec.RegistryAuth) (string, func(), error) {
	host := ref
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	dir, err := os.MkdirTemp("", "statio-pull")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	cfg := map[string]any{"auths": map[string]any{
		host: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(auth.Username + ":" + auth.Password))},
	}}
	data, err := json.Marshal(cfg)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return dir, cleanup, nil
}

package deploy

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// DockerPuller pulls an image by digest via the docker CLI. Pull-by-digest is content
// addressed: docker verifies the manifest hashes the requested digest, so the resolved
// digest equals the requested one (the pipeline still re-checks). Registry auth is
// assumed configured at init via `docker login ghcr.io` from the GHCR creds.
//
// NOTE: the plan prefers the Docker SDK with in-process RegistryAuth to avoid a shared
// docker login. Shelling is the v1 implementation to keep the dependency surface small;
// it is swappable behind the deploy.Puller interface.
type DockerPuller struct{}

// Pull pulls ref (repository@sha256:...) and returns the resolved digest.
func (DockerPuller) Pull(ctx context.Context, ref string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "pull", "--quiet", ref)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("docker pull failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// ref is repo@digest; the digest is authoritative after a successful by-digest pull.
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[i+1:], nil
	}
	return "", nil
}

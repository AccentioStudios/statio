package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// The agent's PRIVATE-image pull credential. The agent process runs sandboxed (ProtectHome=yes
// hides ~/.docker), so its in-process cosign keychain (authn.DefaultKeychain reads
// $DOCKER_CONFIG/config.json) and the shelled-out `docker pull` are pointed at this directory via
// `Environment=DOCKER_CONFIG=/etc/statio/docker` in the unit. Both stages — reading a private
// image's cosign .sig at VERIFY and the docker pull at PULL — read the SAME config.json here.
//
// dockerCfgDir derives the directory from the services dir so it tracks the /etc/statio root the
// unit hardcodes (servicesDir=/etc/statio/services -> /etc/statio/docker) and tests can override it.
func dockerCfgDir(servicesDir string) string {
	return filepath.Join(filepath.Dir(servicesDir), "docker")
}

// registryHostFromImage returns the registry host of an image repo (the first path segment when it
// looks like a host — contains a '.' or ':'); a bare "org/app" defaults to docker.io like Docker.
func registryHostFromImage(image string) string {
	first, _, ok := strings.Cut(image, "/")
	if ok && (strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost") {
		return first
	}
	return "docker.io"
}

// dockerConfigJSON is the minimal subset of ~/.docker/config.json we read/write: the per-registry
// base64 "user:token" auth. We MERGE (never clobber) so several private apps on different
// registries can each keep their credential in the one file the agent reads.
type dockerConfigJSON struct {
	Auths map[string]dockerAuthEntry `json:"auths"`
}

type dockerAuthEntry struct {
	Auth string `json:"auth"`
}

// writeRegistryCred writes (merging) a host credential into <dir>/config.json with 0600 perms.
func writeRegistryCred(dir, host, username, token string) error {
	if username == "" || token == "" {
		return fmt.Errorf("registry credential needs a username and a token")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.json")
	cfg := dockerConfigJSON{Auths: map[string]dockerAuthEntry{}}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &cfg) // a malformed existing file is replaced, not fatal
		if cfg.Auths == nil {
			cfg.Auths = map[string]dockerAuthEntry{}
		}
	}
	cfg.Auths[host] = dockerAuthEntry{Auth: base64.StdEncoding.EncodeToString([]byte(username + ":" + token))}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return nil
}

// ghCredential returns a (username, token) usable to pull from GHCR, derived from the local gh
// login. The token is the gh OAuth token (`gh auth token`); the username is the GitHub login. The
// token must carry the `read:packages` scope — gh's default login does NOT, so the caller surfaces
// `gh auth refresh -s read:packages` on failure.
func ghCredential(ctx context.Context) (user, token string, err error) {
	tb, err := ghCommand(ctx, "auth", "token").Output()
	if err != nil {
		return "", "", fmt.Errorf("gh auth token: %w", err)
	}
	token = strings.TrimSpace(string(tb))
	if token == "" {
		return "", "", fmt.Errorf("gh returned an empty token (is gh logged in here?)")
	}
	ub, err := ghCommand(ctx, "api", "user", "--jq", ".login").Output()
	if err != nil {
		// The token still pulls; GHCR accepts any non-empty username with a valid token.
		return "x-access-token", token, nil
	}
	if u := strings.TrimSpace(string(ub)); u != "" {
		user = u
	} else {
		user = "x-access-token"
	}
	return user, token, nil
}

// provisionGHCRFromGH writes the agent's GHCR pull credential from the local gh login. Returns
// false (no error) when gh is unavailable — the caller then prints a manual hint.
func provisionGHCRFromGH(ctx context.Context, servicesDir, host string) (bool, error) {
	user, token, err := ghCredential(ctx)
	if err != nil {
		return false, err
	}
	if err := writeRegistryCred(dockerCfgDir(servicesDir), host, user, token); err != nil {
		return false, err
	}
	return true, nil
}

// newRegistryCmd is `statio registry` — manage the agent's private-image pull credential. Used by
// `app add` automatically for a private GHCR repo, and standalone to rotate it (the gh token
// rotates when you re-auth) or to set a credential for a non-GHCR registry.
func newRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "registry", Short: "Manage the agent's private-image pull credential"}
	var servicesDir, username string
	fromGH := true
	login := &cobra.Command{
		Use:   "login [host]",
		Short: "Store a pull credential the agent uses for PRIVATE images (default host: ghcr.io)",
		Long: "Writes a read-only registry credential to /etc/statio/docker/config.json, which the\n" +
			"agent reads (via DOCKER_CONFIG) to verify a private image's cosign .sig and pull it.\n\n" +
			"By default it reuses your local gh login (`gh auth token`). That token needs the\n" +
			"`read:packages` scope; add it with `gh auth refresh -s read:packages` if pulls 401.\n" +
			"For a non-GHCR registry, pass --from-gh=false and provide the token via the\n" +
			"STATIO_REGISTRY_TOKEN env var with --username.",
		Args:    cobra.MaximumNArgs(1),
		PreRunE: rootPreRun,
		RunE: func(c *cobra.Command, args []string) error {
			host := "ghcr.io"
			if len(args) == 1 && args[0] != "" {
				host = args[0]
			}
			dir := dockerCfgDir(servicesDir)
			if fromGH && host == "ghcr.io" {
				ok, err := provisionGHCRFromGH(c.Context(), servicesDir, host)
				if err != nil {
					return fmt.Errorf("couldn't derive a GHCR credential from gh: %w\n"+
						"  fix: run `gh auth login` (and `gh auth refresh -s read:packages`) on this server,\n"+
						"  or pass --from-gh=false with STATIO_REGISTRY_TOKEN set", err)
				}
				if ok {
					okLine("Stored a GHCR pull credential for the agent at %s/config.json", dir)
					info("If pulls still 401, the gh token lacks read:packages — run:")
					info("  gh auth refresh -s read:packages   (then re-run this command)")
				}
				return nil
			}
			token := os.Getenv("STATIO_REGISTRY_TOKEN")
			if token == "" {
				return fmt.Errorf("set the token in STATIO_REGISTRY_TOKEN (never on argv) for --from-gh=false")
			}
			if username == "" {
				return fmt.Errorf("--username is required for --from-gh=false")
			}
			if err := writeRegistryCred(dir, host, username, token); err != nil {
				return err
			}
			okLine("Stored a pull credential for %s at %s/config.json", host, dir)
			return nil
		},
	}
	f := login.Flags()
	f.BoolVar(&fromGH, "from-gh", true, "derive a GHCR credential from the local gh login")
	f.StringVar(&username, "username", "", "registry username (for --from-gh=false)")
	f.StringVar(&servicesDir, "services-dir", "/etc/statio/services", "services directory (locates /etc/statio)")
	cmd.AddCommand(login)
	return cmd
}

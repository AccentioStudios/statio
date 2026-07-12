package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/accentiostudios/statio/internal/config"
	"github.com/accentiostudios/statio/internal/deploy"
)

const baseConfigYAML = `hostname: statio
listen_port: 443
tailscale:
  oauth_file: /etc/statio/secrets/oauth.json
  tags: [tag:agent]
  state_dir: /var/lib/statio/tsnet
cosign:
  oidc_issuer: https://token.actions.githubusercontent.com
  require_tlog: true
  require_sct: true
services_dir: /etc/statio/services
state_dir: /var/lib/statio
log_level: info
`

func writeBaseConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(baseConfigYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigForEditDefaultsUpstreamMode(t *testing.T) {
	cfg, err := loadConfigForEdit(writeBaseConfig(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// A pre-feature config has no upstream_mode; it must default to loopback so it validates.
	if cfg.NPMplus.UpstreamMode != config.UpstreamModeLoopback {
		t.Fatalf("upstream_mode = %q, want loopback default", cfg.NPMplus.UpstreamMode)
	}
}

func TestSaveLoadConfigRoundTripIsIdempotent(t *testing.T) {
	path := writeBaseConfig(t)
	cfg, err := loadConfigForEdit(path)
	if err != nil {
		t.Fatal(err)
	}
	// Apply an edit the wizard would make.
	cfg.NPMplus = config.NPMplusConfig{
		BaseURL: "http://127.0.0.1:81", CredentialsFile: npmplusSecretPath,
		UpstreamMode: config.UpstreamModeContainer, Network: "statio-edge",
	}
	if err := saveConfig(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Re-load: every edited field survives, and core fields are preserved.
	got, err := loadConfigForEdit(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.NPMplus.UpstreamMode != config.UpstreamModeContainer || got.NPMplus.Network != "statio-edge" {
		t.Fatalf("npmplus not round-tripped: %+v", got.NPMplus)
	}
	if got.Hostname != "statio" || got.Cosign.OIDCIssuer == "" {
		t.Fatalf("core config lost on round-trip: hostname=%q issuer=%q", got.Hostname, got.Cosign.OIDCIssuer)
	}
	// Saving again produces a byte-identical file (idempotent).
	before, _ := os.ReadFile(path)
	if err := saveConfig(path, got); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("second save was not byte-identical (not idempotent)")
	}
}

func TestReleaseSlotPortFreesAndIsNoOpWhenAbsent(t *testing.T) {
	stateDir := t.TempDir()
	path := filepath.Join(stateDir, "ports.json")

	// No registry yet -> no-op, no error (and no file created).
	if err := releaseSlotPort(stateDir, "api"); err != nil {
		t.Fatalf("release on missing registry should be a no-op: %v", err)
	}

	// Seed a registry with an assignment, then release it.
	reg := &deploy.PortRegistry{Assignments: map[string]int{"api": deploy.PortPoolStart, "web": deploy.PortPoolStart + 1}}
	if err := reg.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := releaseSlotPort(stateDir, "api"); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, err := deploy.LoadPortRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.PortFor("api"); ok {
		t.Fatal("removed app's port was not released back to the pool")
	}
	if _, ok := got.PortFor("web"); !ok {
		t.Fatal("releasing one slot must not drop another slot's assignment")
	}
}

func TestSaveConfigRejectsInvalidCombo(t *testing.T) {
	path := writeBaseConfig(t)
	cfg, _ := loadConfigForEdit(path)
	// container mode without a network is invalid — saveConfig must refuse (fail-closed) before
	// the agent ever boots on it.
	cfg.NPMplus = config.NPMplusConfig{
		BaseURL: "http://127.0.0.1:81", CredentialsFile: npmplusSecretPath,
		UpstreamMode: config.UpstreamModeContainer, // no Network
	}
	if err := saveConfig(path, cfg); err == nil {
		t.Fatal("expected saveConfig to reject container mode without a network")
	}
}

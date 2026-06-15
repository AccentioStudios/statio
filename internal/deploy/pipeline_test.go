package deploy

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/accentiostudios/statio/internal/config"
	"github.com/accentiostudios/statio/internal/env"
	"github.com/accentiostudios/statio/internal/spec"
)

type fakeVerifier struct {
	err    error
	called bool
}

func (f *fakeVerifier) Verify(ctx context.Context, repo, digest string, s EffectiveSigner) error {
	f.called = true
	return f.err
}

type fakePuller struct{ called bool }

func (f *fakePuller) Pull(ctx context.Context, ref string) (string, error) {
	f.called = true
	return ref[strings.Index(ref, "@")+1:], nil // resolved digest
}

var digest = "sha256:" + strings.Repeat("a", 64)

func setup(t *testing.T, extraBaseEnv string) (*Deployer, *fakeVerifier, *fakePuller) {
	t.Helper()
	dir := t.TempDir()
	name := filepath.Base(dir)
	manifest := `apiVersion: statio/v1
kind: ServiceDeploy
name: ` + name + `
image:
  repository: ghcr.io/org/api
deploy:
  compose_file: compose.yaml
  project: api
  services: [api]
health:
  type: none
proxy:
  allowed_domain_suffixes: [example.com]
  allowed_upstream_hosts: [api]
dns:
  allowed_domain_suffixes: [example.com]
rollback:
  enabled: true
  env_policy: with-digest
`
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if extraBaseEnv != "" {
		if err := os.WriteFile(filepath.Join(dir, "env.base.yaml"), []byte(extraBaseEnv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Hostname:   "statio",
		Cosign:     config.CosignConfig{OIDCIssuer: "iss", Identity: "id"},
		Cloudflare: config.CloudflareConfig{ZoneApex: "example.com"},
		DNS:        config.DNSConfig{PublicIP: "203.0.113.10", TTL: 1},
		StateDir:   dir,
	}
	v := &fakeVerifier{}
	p := &fakePuller{}
	d := &Deployer{
		Cfg: cfg, Manifest: m, StatePath: filepath.Join(dir, "state.json"),
		Verifier: v, Puller: p, Resolve: env.ResolveFileSecret, Audience: "statio",
		Now: func() string { return "t0" }, Log: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	return d, v, p
}

func req(t *testing.T, mutate func(r *spec.DeployRequest)) *spec.DeployRequest {
	t.Helper()
	body := `{"apiVersion":"statio/v1","kind":"DeployRequest","service":"api",` +
		`"image":{"repository":"ghcr.io/org/api","digest":"` + digest + `"},` +
		`"audience":"statio","deploy_seq":1,"issued_at":"2026-06-14T12:00:00Z","expiry":"2099-01-01T00:00:00Z",` +
		`"app_intent":{"services":[{"name":"api","ports":[3000],"health":{"path":"/health"}}]}}`
	r, err := spec.Decode(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(r)
	}
	return r
}

func TestRunRejectsRepoMismatch(t *testing.T) {
	d, v, p := setup(t, "")
	r := req(t, func(r *spec.DeployRequest) { r.Image.Repository = "ghcr.io/evil/x" })
	res, _ := d.Run(context.Background(), r)
	if res.State != StateFailure {
		t.Fatalf("want failure, got %s", res.State)
	}
	if v.called || p.called {
		t.Fatal("verify/pull must NOT run after repo-equality failure")
	}
}

func TestRunRejectsProxyDomainNotAllowed(t *testing.T) {
	d, v, _ := setup(t, "")
	r := req(t, func(r *spec.DeployRequest) {
		r.Proxy = &spec.ProxySpec{Enabled: true, Domain: "api.evil.com", UpstreamHost: "api", UpstreamPort: 3000, Scheme: "http"}
	})
	res, _ := d.Run(context.Background(), r)
	if res.State != StateFailure || v.called {
		t.Fatalf("expected admit failure before verify; state=%s verifyCalled=%v", res.State, v.called)
	}
}

func TestRunRejectsDNSDomainNotAllowed(t *testing.T) {
	d, _, _ := setup(t, "")
	r := req(t, func(r *spec.DeployRequest) {
		r.DNS = &spec.DNSSpec{Enabled: true, Domain: "api.other.org"}
	})
	res, _ := d.Run(context.Background(), r)
	if res.State != StateFailure {
		t.Fatalf("expected dns allowlist failure, got %s", res.State)
	}
}

func TestRunRejectsProtectedOverride(t *testing.T) {
	baseEnv := `apiVersion: statio/v1
kind: ServiceEnv
entries:
  - key: SECRET_KEY
    value: server-owned
    protected: true
`
	d, v, _ := setup(t, baseEnv)
	r := req(t, func(r *spec.DeployRequest) { r.EnvOverrides = map[string]string{"SECRET_KEY": "evil"} })
	res, _ := d.Run(context.Background(), r)
	if res.State != StateFailure || v.called {
		t.Fatalf("expected protected-key admit failure before verify; state=%s verifyCalled=%v", res.State, v.called)
	}
}

func TestRunVerifyFailureStopsBeforePull(t *testing.T) {
	d, v, p := setup(t, "")
	v.err = context.Canceled // any verify error
	r := req(t, nil)
	res, _ := d.Run(context.Background(), r)
	if res.State != StateFailure {
		t.Fatalf("want failure, got %s", res.State)
	}
	if !v.called {
		t.Fatal("verify should have been attempted")
	}
	if p.called {
		t.Fatal("pull must NOT run after verify failure")
	}
}

func TestRunRejectsBindingViolations(t *testing.T) {
	// audience mismatch: payload targets a different agent.
	d, v, _ := setup(t, "")
	r := req(t, func(r *spec.DeployRequest) { r.Audience = "other" })
	if res, _ := d.Run(context.Background(), r); res.State != StateFailure || v.called {
		t.Fatalf("audience mismatch must fail before verify; state=%s verify=%v", res.State, v.called)
	}

	// expired: payload's expiry is in the past.
	d, v, _ = setup(t, "")
	r = req(t, func(r *spec.DeployRequest) { r.Expiry = "2000-01-01T00:00:00Z" })
	if res, _ := d.Run(context.Background(), r); res.State != StateFailure || v.called {
		t.Fatalf("expired payload must fail before verify; state=%s verify=%v", res.State, v.called)
	}

	// replay: state already recorded a higher seq.
	d, v, _ = setup(t, "")
	st := &State{Service: "api", LastSeq: 99}
	if err := st.Save(d.StatePath); err != nil {
		t.Fatal(err)
	}
	r = req(t, func(r *spec.DeployRequest) { r.DeploySeq = 5 })
	if res, _ := d.Run(context.Background(), r); res.State != StateFailure || v.called {
		t.Fatalf("replayed (older) seq must fail before verify; state=%s verify=%v", res.State, v.called)
	}
}

func TestInZone(t *testing.T) {
	cases := map[string]bool{"api.example.com": true, "example.com": true, "api.evil.com": false, "exampleXcom": false}
	for fqdn, want := range cases {
		if got := inZone(fqdn, "example.com"); got != want {
			t.Errorf("inZone(%q)=%v want %v", fqdn, got, want)
		}
	}
}

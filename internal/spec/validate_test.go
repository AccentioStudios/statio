package spec

import (
	"strings"
	"testing"
)

func validReq() string {
	return `{
	  "apiVersion": "push/v1",
	  "kind": "DeployRequest",
	  "service": "api",
	  "image": {"repository": "ghcr.io/org/api", "digest": "sha256:` + strings.Repeat("a", 64) + `"},
	  "audience": "push.tail1234.ts.net",
	  "deploy_seq": 12,
	  "issued_at": "2026-06-14T12:00:00Z",
	  "expiry": "2026-06-14T12:05:00Z",
	  "app_intent": {"services": [
	    {"name": "api", "ports": [3000], "health": {"path": "/health"}, "depends_on": ["db"]},
	    {"name": "db", "image": "postgres:16", "volumes": [{"name": "pgdata", "path": "/var/lib/postgresql/data"}]}
	  ]},
	  "env_overrides": {"RELEASE_SHA": "abc123"},
	  "proxy": {"enabled": true, "domain": "api.example.com", "upstream_host": "api", "upstream_port": 3000, "scheme": "http", "ssl": true},
	  "dns": {"enabled": true, "domain": "api.example.com"}
	}`
}

func TestDecodeValid(t *testing.T) {
	r, err := Decode(strings.NewReader(validReq()))
	if err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if r.Service != "api" || r.Image.Digest != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("unexpected decode: %+v", r)
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	body := strings.Replace(validReq(), `"service": "api",`, `"service": "api", "command": "rm -rf /",`, 1)
	if _, err := Decode(strings.NewReader(body)); err == nil {
		t.Fatal("expected unknown-field rejection (closed schema)")
	}
}

func TestDecodeRejectsTrailingData(t *testing.T) {
	if _, err := Decode(strings.NewReader(validReq() + "{}")); err == nil {
		t.Fatal("expected trailing-data rejection")
	}
}

func TestDecodeRejectsTooLarge(t *testing.T) {
	big := strings.Repeat("a", MaxBodyBytes+10)
	body := strings.Replace(validReq(), "abc123", big, 1)
	if _, err := Decode(strings.NewReader(body)); err == nil {
		t.Fatal("expected too-large rejection")
	}
}

func TestValidateDigest(t *testing.T) {
	cases := map[string]bool{
		"sha256:" + strings.Repeat("a", 64): true,
		"latest":                            false,
		"sha256:" + strings.Repeat("a", 63): false,
		"sha256:" + strings.Repeat("A", 64): false, // uppercase
		"":                                  false,
	}
	for digest, want := range cases {
		r := mustReq(t)
		r.Image.Digest = digest
		got := r.Validate() == nil
		if got != want {
			t.Errorf("digest %q: valid=%v want %v", digest, got, want)
		}
	}
}

func TestValidateEnv(t *testing.T) {
	cases := []struct {
		name string
		k, v string
		ok   bool
	}{
		{"ok", "FOO", "bar", true},
		{"dollar-is-data", "FOO", "${RM} $(rm -rf /)", true}, // opaque, never interpolated/exec'd
		{"newline-forgery", "FOO", "bar\nEVIL=1", false},
		{"nul", "FOO", "bar\x00", false},
		{"tab", "FOO", "a\tb", false},
		{"reserved-prefix", "PUSH_IMAGE_DIGEST", "x", false},
		{"lowercase-key", "foo", "x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := mustReq(t)
			r.EnvOverrides = map[string]string{c.k: c.v}
			got := r.Validate() == nil
			if got != c.ok {
				t.Errorf("env %s=%q: valid=%v want %v", c.k, c.v, got, c.ok)
			}
		})
	}
}

func TestValidateProxy(t *testing.T) {
	r := mustReq(t)
	r.Proxy.UpstreamPort = 70000
	if r.Validate() == nil {
		t.Error("expected invalid port rejection")
	}
	r = mustReq(t)
	r.Proxy.Scheme = "ftp"
	if r.Validate() == nil {
		t.Error("expected invalid scheme rejection")
	}
	r = mustReq(t)
	r.Proxy.Domain = "bad domain"
	if r.Validate() == nil {
		t.Error("expected invalid domain rejection")
	}
}

func TestValidateService(t *testing.T) {
	for _, s := range []string{"API", "../etc", "a/b", ""} {
		r := mustReq(t)
		r.Service = s
		if r.Validate() == nil {
			t.Errorf("service %q should be rejected", s)
		}
	}
}

func mustReq(t *testing.T) *DeployRequest {
	t.Helper()
	r, err := Decode(strings.NewReader(validReq()))
	if err != nil {
		t.Fatalf("base request invalid: %v", err)
	}
	return r
}

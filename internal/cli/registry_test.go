package cli

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRegistryHostFromImage(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/accentiostudios/bohemian_gym_api": "ghcr.io",
		"docker.io/library/postgres":               "docker.io",
		"library/postgres":                         "docker.io", // bare -> docker.io
		"postgres":                                 "docker.io",
		"registry.example.com:5000/org/app":        "registry.example.com:5000",
		"localhost/app":                            "localhost",
	}
	for in, want := range cases {
		if got := registryHostFromImage(in); got != want {
			t.Errorf("registryHostFromImage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteRegistryCredMergesAndEncodes(t *testing.T) {
	dir := t.TempDir()

	if err := writeRegistryCred(dir, "ghcr.io", "alice", "tok1"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// A second host must MERGE, not clobber the first.
	if err := writeRegistryCred(dir, "docker.io", "bob", "tok2"); err != nil {
		t.Fatalf("second write: %v", err)
	}

	path := filepath.Join(dir, "config.json")
	if fi, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	} else if perm := fi.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Errorf("config.json perms = %o, want 600", perm)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg dockerConfigJSON
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Auths) != 2 {
		t.Fatalf("want 2 registries after merge, got %d (%v)", len(cfg.Auths), cfg.Auths)
	}
	want := base64.StdEncoding.EncodeToString([]byte("alice:tok1"))
	if cfg.Auths["ghcr.io"].Auth != want {
		t.Errorf("ghcr.io auth = %q, want %q", cfg.Auths["ghcr.io"].Auth, want)
	}

	// Re-writing the same host updates in place (still 2 entries).
	if err := writeRegistryCred(dir, "ghcr.io", "alice", "tok3"); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	raw, _ = os.ReadFile(path)
	cfg = dockerConfigJSON{}
	_ = json.Unmarshal(raw, &cfg)
	if len(cfg.Auths) != 2 {
		t.Errorf("want 2 registries after update, got %d", len(cfg.Auths))
	}
	want = base64.StdEncoding.EncodeToString([]byte("alice:tok3"))
	if cfg.Auths["ghcr.io"].Auth != want {
		t.Errorf("updated ghcr.io auth = %q, want %q", cfg.Auths["ghcr.io"].Auth, want)
	}
}

func TestWriteRegistryCredRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := writeRegistryCred(dir, "ghcr.io", "", "tok"); err == nil {
		t.Error("empty username must be rejected")
	}
	if err := writeRegistryCred(dir, "ghcr.io", "user", ""); err == nil {
		t.Error("empty token must be rejected")
	}
}

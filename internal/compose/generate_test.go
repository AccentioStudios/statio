package compose

import (
	"strings"
	"testing"

	"github.com/accentiostudios/statio/internal/spec"
)

func gen(t *testing.T, in Input) string {
	t.Helper()
	b, err := Generate(in)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return string(b)
}

func TestGenerateMultiService(t *testing.T) {
	in := Input{
		Slot: "api", PrimaryRepo: "ghcr.io/org/api", Digest: "sha256:" + strings.Repeat("a", 64),
		RunDir: "/run/statio/api", AllowedRegistries: []string{"docker.io"},
		Intent: &spec.AppIntent{Services: []spec.Service{
			{Name: "api", Ports: []spec.Port{{Container: 3000, Host: 3000, Protocol: "tcp"}}, Env: []string{"DATABASE_URL"}, DependsOn: []string{"db"}},
			{Name: "db", Image: "postgres:16", Volumes: []spec.Volume{{Name: "pgdata", Path: "/var/lib/postgresql/data"}}},
		}},
	}
	out := gen(t, in)
	for _, want := range []string{
		"ghcr.io/org/api@sha256:",                    // primary baked literally
		"127.0.0.1:3000:3000",                        // loopback only
		"image: postgres:16",                         // dependency as-is
		"statio_api_pgdata:/var/lib/postgresql/data", // namespaced volume
		"/run/statio/api/api.env",                    // env_file
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated compose missing %q\n%s", want, out)
		}
	}
}

// forbiddenKeys must NEVER appear in generated output, even from a hostile intent.
var forbiddenKeys = []string{
	"privileged", "cap_add", "cap_drop", "devices", "network_mode", "pid:", "ipc:",
	"userns_mode", "security_opt", "sysctls", "volumes_from", "group_add", "extra_hosts",
	"driver_opts", "device:", "build:", "0.0.0.0", "::",
}

func TestGenerateNeverEmitsForbiddenKeys(t *testing.T) {
	// A maximally hostile intent: it cannot even REPRESENT the dangerous fields (the spec
	// struct has no such fields), so we assert the rendered output is clean and that the
	// loopback/namespacing/escaping all hold.
	in := Input{
		Slot: "x", PrimaryRepo: "ghcr.io/org/app", Digest: "sha256:" + strings.Repeat("b", 64),
		RunDir: "/run/statio/x", AllowedRegistries: []string{"docker.io"},
		Intent: &spec.AppIntent{Services: []spec.Service{
			{Name: "app", Ports: []spec.Port{{Container: 80, Host: 80, Protocol: "tcp"}},
				Command: []string{"sh", "-c", "echo ${HOME} $(whoami)"}, // $ must be escaped to $$
				Volumes: []spec.Volume{{Name: "data", Path: "/data", ReadOnly: true}}},
		}},
	}
	out := gen(t, in)
	for _, k := range forbiddenKeys {
		if strings.Contains(out, k) {
			t.Errorf("forbidden key %q leaked into generated compose:\n%s", k, out)
		}
	}
	// Every $ must be doubled ($$ = literal $ to compose); no lone $ may survive.
	if strings.Contains(strings.ReplaceAll(out, "$$", ""), "$") {
		t.Errorf("an un-escaped $ survived (interpolation risk):\n%s", out)
	}
	if !strings.Contains(out, "$$") {
		t.Errorf("expected $ escaped to $$ in command:\n%s", out)
	}
	if !strings.Contains(out, "statio_x_data:/data:ro") {
		t.Errorf("read-only namespaced volume missing:\n%s", out)
	}
}

func TestGenerateAllocatedHostPortOverridesDevDeclared(t *testing.T) {
	in := Input{
		Slot: "api", PrimaryRepo: "ghcr.io/org/api", Digest: "sha256:" + strings.Repeat("a", 64),
		RunDir: "/run/statio/api", AllowedRegistries: []string{"docker.io"},
		HostPort: 41000, // statio-allocated; must replace the dev's host side, keep container 3000
		Intent: &spec.AppIntent{Services: []spec.Service{
			{Name: "api", Ports: []spec.Port{{Container: 3000, Host: 3000, Protocol: "tcp"}}},
		}},
	}
	out := gen(t, in)
	if !strings.Contains(out, "127.0.0.1:41000:3000") {
		t.Errorf("expected allocated host port 41000 mapped to container 3000:\n%s", out)
	}
	if strings.Contains(out, "127.0.0.1:3000:3000") {
		t.Errorf("dev-declared host port 3000 should have been overridden:\n%s", out)
	}
}

func TestGenerateContainerModeJoinsExternalNetwork(t *testing.T) {
	// Distinct slot vs service name so the alias assertion proves it's the SLOT, not the service.
	in := Input{
		Slot: "myslot", PrimaryRepo: "ghcr.io/org/api", Digest: "sha256:" + strings.Repeat("a", 64),
		RunDir: "/run/statio/myslot", AllowedRegistries: []string{"docker.io"},
		HostPort: 41000, Network: "statio-edge",
		Intent: &spec.AppIntent{Services: []spec.Service{
			{Name: "app", Ports: []spec.Port{{Container: 3000, Host: 3000, Protocol: "tcp"}}, DependsOn: []string{"db"}},
			{Name: "db", Image: "postgres:16"},
		}},
	}
	out := gen(t, in)
	for _, want := range []string{
		"networks:",      // top-level networks block
		"statio-edge:",   // the external net is declared
		"external: true", // referenced, not owned
		"default: {}",    // app stays on the project default too (so it can reach db)
		"aliases:",       // app answers on the shared net under an alias
		"- myslot",       // the alias is the SLOT name (server-pinned, unique), not the service name "app"
	} {
		if !strings.Contains(out, want) {
			t.Errorf("container-mode compose missing %q:\n%s", want, out)
		}
	}
	// The dependency must NOT join the shared proxy network — only your app is reachable. Bound
	// the search to the services section so the top-level networks declaration isn't swept in.
	servicesOnly := out
	if idx := strings.Index(out, "\nnetworks:"); idx >= 0 {
		servicesOnly = out[:idx]
	}
	dbBlock := servicesOnly[strings.Index(servicesOnly, "db:"):]
	if strings.Contains(dbBlock, "statio-edge") {
		t.Errorf("dependency db should not join the shared proxy network:\n%s", out)
	}
	// network_mode (the dangerous host/container escape) must still never appear.
	if strings.Contains(out, "network_mode") {
		t.Errorf("network_mode must never be emitted:\n%s", out)
	}
}

func TestGenerateLoopbackModeNoNetworks(t *testing.T) {
	in := Input{
		Slot: "api", PrimaryRepo: "ghcr.io/org/api", Digest: "sha256:" + strings.Repeat("a", 64),
		RunDir: "/run/statio/api", AllowedRegistries: []string{"docker.io"},
		HostPort: 41000, // loopback mode: no Network set
		Intent: &spec.AppIntent{Services: []spec.Service{
			{Name: "api", Ports: []spec.Port{{Container: 3000, Host: 3000, Protocol: "tcp"}}},
		}},
	}
	out := gen(t, in)
	if strings.Contains(out, "networks:") {
		t.Errorf("loopback mode must not emit any networks block:\n%s", out)
	}
}

func TestGenerateRejectsDisallowedRegistry(t *testing.T) {
	in := Input{
		Slot: "api", PrimaryRepo: "ghcr.io/org/api", Digest: "sha256:" + strings.Repeat("a", 64),
		RunDir: "/run/statio/api", AllowedRegistries: []string{"ghcr.io"}, // docker.io NOT allowed
		Intent: &spec.AppIntent{Services: []spec.Service{
			{Name: "api"},
			{Name: "db", Image: "postgres:16"}, // docker.io
		}},
	}
	if _, err := Generate(in); err == nil {
		t.Fatal("expected rejection for dependency from a non-allowlisted registry")
	}
}

func TestRegistryOf(t *testing.T) {
	cases := map[string]string{
		"postgres:16":                "docker.io",
		"library/redis:7":            "docker.io",
		"ghcr.io/org/api@sha256:abc": "ghcr.io",
		"registry.gitlab.com/x/y:1":  "registry.gitlab.com",
		"localhost:5000/dev":         "localhost:5000",
	}
	for img, want := range cases {
		if got := registryOf(img); got != want {
			t.Errorf("registryOf(%q)=%q want %q", img, got, want)
		}
	}
}

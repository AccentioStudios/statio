// Package compose generates a docker-compose file from a validated, signed AppIntent.
//
// This is an ALLOWLIST generator, not a denylist: the output is built by marshalling a
// fixed Go struct whose only fields are the safe ones (image, loopback ports, env_file,
// docker-managed volumes, depends_on, command, restart). Dangerous compose keys
// (privileged, cap_add, devices, network_mode, pid/ipc/uts, userns_mode, security_opt,
// sysctls, volumes_from, bind mounts, host-ip ports…) do not exist as fields and so can
// never be emitted, EVEN for a validly-signed-but-malicious AppIntent (invariant #19).
//
// The verified image digest is baked literally into the image line, so the generated file
// contains no ${...} the dev controls and is run WITHOUT --env-file interpolation; app env
// reaches containers only via env_file (literal KEY=VALUE). This makes "an app value
// cannot inject into compose config" structural (invariant #8).
package compose

import (
	"fmt"
	"path"
	"strings"

	"github.com/accentiostudios/statio/internal/spec"
	"gopkg.in/yaml.v3"
)

// Input is everything the generator needs that does NOT come from the dev's AppIntent:
// the server-pinned primary repo, the verified digest, the tmpfs run dir for env files,
// and the dependency-registry allowlist.
type Input struct {
	Slot              string // the accepted service slot (compose project + volume namespace)
	PrimaryRepo       string // server-pinned repo for image-less (your-app) services
	Digest            string // verified image digest (sha256:...)
	RunDir            string // tmpfs dir holding per-service env files, e.g. /run/statio/<slot>
	AllowedRegistries []string
	Intent            *spec.AppIntent
}

// service is the closed set of compose keys we will ever emit. Adding a field here is the
// ONLY way to widen the surface, so the review boundary is this struct.
type service struct {
	Image     string   `yaml:"image"`
	Ports     []string `yaml:"ports,omitempty"`
	EnvFile   []string `yaml:"env_file,omitempty"`
	Volumes   []string `yaml:"volumes,omitempty"`
	DependsOn []string `yaml:"depends_on,omitempty"`
	Command   []string `yaml:"command,omitempty"`
	Restart   string   `yaml:"restart,omitempty"`
}

type file struct {
	Services map[string]service  `yaml:"services"`
	Volumes  map[string]struct{} `yaml:"volumes,omitempty"`
}

// Generate produces the compose YAML for the intent. It re-checks the dependency registry
// allowlist (defense in depth, even though spec.Validate ran) and never copies any field
// outside `service`.
func Generate(in Input) ([]byte, error) {
	if in.Intent == nil || len(in.Intent.Services) == 0 {
		return nil, fmt.Errorf("compose: empty intent")
	}
	out := file{Services: make(map[string]service), Volumes: map[string]struct{}{}}

	for i := range in.Intent.Services {
		s := &in.Intent.Services[i]
		var cs service

		if s.Image == "" {
			// Your app: server-pinned repo + the verified digest, baked literally.
			cs.Image = in.PrimaryRepo + "@" + in.Digest
		} else {
			if err := checkRegistry(s.Image, in.AllowedRegistries); err != nil {
				return nil, err
			}
			cs.Image = s.Image
		}

		for _, p := range s.Ports {
			cs.Ports = append(cs.Ports, renderPort(p))
		}

		if len(s.Env) > 0 || len(s.EnvInline) > 0 {
			cs.EnvFile = []string{path.Join(in.RunDir, s.Name+".env")}
		}

		for _, v := range s.Volumes {
			name := "statio_" + in.Slot + "_" + v.Name
			mount := name + ":" + v.Path
			if v.ReadOnly {
				mount += ":ro"
			}
			cs.Volumes = append(cs.Volumes, mount)
			out.Volumes[name] = struct{}{}
		}

		cs.DependsOn = s.DependsOn

		for _, c := range s.Command {
			// Neutralize compose interpolation structurally: $ -> $$ (literal $).
			cs.Command = append(cs.Command, strings.ReplaceAll(c, "$", "$$"))
		}

		cs.Restart = s.Restart
		if cs.Restart == "" {
			cs.Restart = "unless-stopped"
		}

		out.Services[s.Name] = cs
	}

	if len(out.Volumes) == 0 {
		out.Volumes = nil
	}
	return yaml.Marshal(out)
}

// renderPort always binds to 127.0.0.1 — the host-ip is hard-coded here and never sourced
// from the intent, so a port can never be published on a public interface (#2).
func renderPort(p spec.Port) string {
	s := fmt.Sprintf("127.0.0.1:%d:%d", p.Host, p.Container)
	if p.Protocol == "udp" {
		s += "/udp"
	}
	return s
}

// checkRegistry enforces the dependency-registry allowlist. An empty allowlist means no
// third-party dependencies are permitted (only your app).
func checkRegistry(image string, allowed []string) error {
	reg := registryOf(image)
	for _, a := range allowed {
		if reg == a {
			return nil
		}
	}
	return fmt.Errorf("compose: image %q uses registry %q not in the allowlist", image, reg)
}

// registryOf returns the registry host of an image reference, defaulting to docker.io for
// short names (postgres:16, library/redis).
func registryOf(image string) string {
	ref := image
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	slash := strings.Index(ref, "/")
	if slash < 0 {
		return "docker.io"
	}
	first := ref[:slash]
	if first == "localhost" || strings.ContainsAny(first, ".:") {
		return first
	}
	return "docker.io"
}

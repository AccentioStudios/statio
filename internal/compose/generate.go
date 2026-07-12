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
	// HostPort is the statio-ALLOCATED loopback host port for your app's first published port
	// (see internal/deploy port registry). When > 0 it overrides the dev-declared host side, so
	// statio owns host-port assignment and two slots can never collide. 0 keeps the dev's value
	// (legacy / tests). The agent's health probe and a loopback-mode proxy both target it.
	HostPort int
	// Network, when set, is a shared EXTERNAL docker network that your app additionally joins so
	// NPMplus (attached to the same network by the devops) can reach it by container name —
	// the "container" data-plane mode. Empty keeps your app on the project default network only
	// (the "loopback" mode, where NPMplus reaches it over the host loopback port instead).
	Network string
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
	// Networks attaches the service to named user-defined networks, each with an optional alias
	// set (e.g. the project default plus a shared external proxy network on which the app answers
	// to its slot name). It is NOT network_mode: it can only join named bridge networks, never
	// host/container/none namespaces, so it cannot escape isolation.
	Networks map[string]netAttach `yaml:"networks,omitempty"`
}

// netAttach is one service↔network attachment. The only field we emit is aliases — extra DNS
// names the service answers to ON that network — so the reverse proxy can reach the app by a
// server-controlled, unique name (the slot) rather than the app-author-chosen service name.
type netAttach struct {
	Aliases []string `yaml:"aliases,omitempty"`
}

// networkDef declares a top-level network. The only shape we emit is `external: true`: a
// reference to a network the devops created out-of-band (so statio never owns its lifecycle
// and the name is used verbatim). No driver/driver_opts/ipam can be expressed.
type networkDef struct {
	External bool `yaml:"external"`
}

type file struct {
	Services map[string]service    `yaml:"services"`
	Volumes  map[string]struct{}   `yaml:"volumes,omitempty"`
	Networks map[string]networkDef `yaml:"networks,omitempty"`
}

// Generate produces the compose YAML for the intent. It re-checks the dependency registry
// allowlist (defense in depth, even though spec.Validate ran) and never copies any field
// outside `service`.
func Generate(in Input) ([]byte, error) {
	if in.Intent == nil || len(in.Intent.Services) == 0 {
		return nil, fmt.Errorf("compose: empty intent")
	}
	out := file{Services: make(map[string]service), Volumes: map[string]struct{}{}}
	primaryIdx := primaryServiceIndex(in.Intent)

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

		for j, p := range s.Ports {
			host := p.Host
			// statio owns the host side of your app's first published port: it binds the
			// ALLOCATED loopback port instead of the dev-declared one, so two slots never clash.
			if i == primaryIdx && j == 0 && in.HostPort > 0 {
				host = in.HostPort
			}
			cs.Ports = append(cs.Ports, renderPort(host, p.Container, p.Protocol))
		}

		// container data-plane mode: your app additionally joins the shared external network so
		// NPMplus can reach it — under the SLOT name as a network alias, which is server-pinned and
		// unique, so two tenants on one shared network never collide and the proxy target is never
		// the app-author-chosen service name. Keep it on the project default too (so it can still
		// talk to its own dependencies). Only your app joins — dependencies stay private.
		if i == primaryIdx && in.Network != "" {
			cs.Networks = map[string]netAttach{
				"default":  {},
				in.Network: {Aliases: []string{in.Slot}},
			}
			out.Networks = map[string]networkDef{in.Network: {External: true}}
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
func renderPort(host, container int, protocol string) string {
	s := fmt.Sprintf("127.0.0.1:%d:%d", host, container)
	if protocol == "udp" {
		s += "/udp"
	}
	return s
}

// primaryServiceIndex returns the index of your app — the first image-less service (spec.Validate
// guarantees at least one). statio owns this service's host port and, in container mode, attaches
// it to the shared proxy network. Returns -1 only for an empty intent (Generate already guards).
func primaryServiceIndex(intent *spec.AppIntent) int {
	for i := range intent.Services {
		if intent.Services[i].Image == "" {
			return i
		}
	}
	return -1
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

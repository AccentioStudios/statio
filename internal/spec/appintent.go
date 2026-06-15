package spec

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Caps for the generated-compose intent. These bound the dev-authored surface; the
// server applies a finer max_services from its config on top of MaxServices.
const (
	MaxServices       = 20
	MaxPortsPerSvc    = 20
	MaxVolumesPerSvc  = 20
	MaxCommandItems   = 100
	MaxCommandItemLen = 1024
	MaxHealthPathLen  = 512
)

var (
	svcNameRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	volNameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,40}$`)
	healthPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@%/-]{0,512}$`)
	durationRe   = regexp.MustCompile(`^[0-9]{1,6}(ms|s|m)$`)
	// imageRefRe is a permissive-but-bounded image reference (registry/repo[:tag][@sha256:...]).
	// The agent additionally checks the registry against the server allowlist.
	imageRefRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*(:[A-Za-z0-9][A-Za-z0-9._-]{0,127})?(@sha256:[0-9a-f]{64})?$`)
)

// AppIntent is the dev-authored, signed description of the services to run. The agent
// turns it into a compose file from a FIXED safe template (internal/compose). Every
// field here is a typed scalar/enum/bounded-list; nothing reaches a privileged,
// host-mount, capability, network, or shell position — those simply do not exist.
type AppIntent struct {
	Services []Service `json:"services"`
}

// Service is one container in the deployment.
type Service struct {
	Name string `json:"name"`
	// Image is EMPTY for your app (the agent injects the verified, repo-pinned digest)
	// and REQUIRED for a dependency (postgres, redis…), pinned by digest from an
	// allowlisted registry.
	Image     string            `json:"image,omitempty"`
	Ports     []Port            `json:"ports,omitempty"`
	Env       []string          `json:"env,omitempty"`        // KEY names; values arrive via env_overrides (courier)
	EnvInline map[string]string `json:"env_inline,omitempty"` // non-secret literals committed to the repo
	Health    *Health           `json:"health,omitempty"`     // meaningful only on your app (agent loopback probe)
	DependsOn []string          `json:"depends_on,omitempty"`
	Volumes   []Volume          `json:"volumes,omitempty"`
	Command   []string          `json:"command,omitempty"` // exec-form only (no shell string)
	Restart   string            `json:"restart,omitempty"` // enum: no|on-failure|unless-stopped|always
}

// Port is a published port. It accepts either a bare integer (host==container, tcp) or
// an object {container, host, protocol}. The host-ip is NEVER carried — the generator
// hard-codes 127.0.0.1 so a port can never be published on a public interface.
type Port struct {
	Container int
	Host      int
	Protocol  string
}

// UnmarshalJSON accepts `3000` or `{"container":3000,"host":8080,"protocol":"tcp"}`.
// It enforces DisallowUnknownFields itself because a custom unmarshaler bypasses the
// parent decoder's strictness.
func (p *Port) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		p.Container, p.Host, p.Protocol = n, n, "tcp"
		return nil
	}
	var obj struct {
		Container int    `json:"container"`
		Host      int    `json:"host"`
		Protocol  string `json:"protocol"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obj); err != nil {
		return err
	}
	p.Container = obj.Container
	p.Host = obj.Host
	if p.Host == 0 {
		p.Host = obj.Container
	}
	p.Protocol = obj.Protocol
	if p.Protocol == "" {
		p.Protocol = "tcp"
	}
	return nil
}

// Volume is a Docker-managed named volume mounted at a container path. Only a bare name
// and path are accepted: no driver/driver_opts/device/bind source can be expressed, so a
// "named volume" can never become a host bind mount.
type Volume struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// Health is the readiness probe for your app, run by the agent over loopback.
type Health struct {
	Type        string `json:"type,omitempty"` // http (default if path set) | tcp
	Path        string `json:"path,omitempty"`
	Port        int    `json:"port,omitempty"` // for tcp
	StartPeriod string `json:"start_period,omitempty"`
	Interval    string `json:"interval,omitempty"`
	Timeout     string `json:"timeout,omitempty"`
	Retries     int    `json:"retries,omitempty"`
}

func (a *AppIntent) validate() error {
	if len(a.Services) == 0 {
		return newErr("app_intent", "app_intent.services must not be empty")
	}
	if len(a.Services) > MaxServices {
		return newErr("app_intent", "too many services (max %d)", MaxServices)
	}
	names := make(map[string]bool, len(a.Services))
	primaries := 0
	for i := range a.Services {
		s := &a.Services[i]
		if !svcNameRe.MatchString(s.Name) {
			return newErr("service_name", "service name %q must match %s", s.Name, svcNameRe.String())
		}
		if names[s.Name] {
			return newErr("service_name", "duplicate service name %q", s.Name)
		}
		names[s.Name] = true
		if s.Image == "" {
			primaries++
		} else if !imageRefRe.MatchString(s.Image) {
			return newErr("service_image", "service %q image is not a valid reference", s.Name)
		}
	}
	if primaries == 0 {
		return newErr("app_intent", "at least one service must omit image (your app)")
	}
	// Second pass: cross-field checks now that all names are known.
	for i := range a.Services {
		if err := a.Services[i].validate(names); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validate(names map[string]bool) error {
	if len(s.Ports) > MaxPortsPerSvc {
		return newErr("service_ports", "service %q has too many ports (max %d)", s.Name, MaxPortsPerSvc)
	}
	for _, p := range s.Ports {
		if p.Container < 1 || p.Container > 65535 || p.Host < 1 || p.Host > 65535 {
			return newErr("service_ports", "service %q port out of range 1..65535", s.Name)
		}
		if p.Protocol != "tcp" && p.Protocol != "udp" {
			return newErr("service_ports", "service %q port protocol must be tcp or udp", s.Name)
		}
	}
	for _, k := range s.Env {
		if err := validateEnvKey(k); err != nil {
			return err
		}
	}
	for k, v := range s.EnvInline {
		if err := validateEnvKey(k); err != nil {
			return err
		}
		if len(v) > MaxEnvValueBytes {
			return newErr("env_value", "env_inline %q exceeds %d bytes", k, MaxEnvValueBytes)
		}
		if !utf8.ValidString(v) || strings.IndexFunc(v, isControl) >= 0 {
			return newErr("env_value", "env_inline %q contains a control character or invalid UTF-8", k)
		}
	}
	if len(s.Volumes) > MaxVolumesPerSvc {
		return newErr("service_volumes", "service %q has too many volumes (max %d)", s.Name, MaxVolumesPerSvc)
	}
	for _, v := range s.Volumes {
		if !volNameRe.MatchString(v.Name) {
			return newErr("volume_name", "service %q volume name %q is invalid", s.Name, v.Name)
		}
		if !strings.HasPrefix(v.Path, "/") || strings.IndexFunc(v.Path, isControl) >= 0 {
			return newErr("volume_path", "service %q volume path must be an absolute clean path", s.Name)
		}
	}
	if len(s.Command) > MaxCommandItems {
		return newErr("service_command", "service %q command has too many items", s.Name)
	}
	for _, c := range s.Command {
		if len(c) > MaxCommandItemLen || !utf8.ValidString(c) || strings.IndexFunc(c, isControl) >= 0 {
			return newErr("service_command", "service %q command item is too long or has control chars", s.Name)
		}
	}
	for _, dep := range s.DependsOn {
		if dep == s.Name {
			return newErr("depends_on", "service %q cannot depend on itself", s.Name)
		}
		if !names[dep] {
			return newErr("depends_on", "service %q depends on unknown service %q", s.Name, dep)
		}
	}
	switch s.Restart {
	case "", "no", "on-failure", "unless-stopped", "always":
	default:
		return newErr("service_restart", "service %q restart %q is invalid", s.Name, s.Restart)
	}
	if s.Health != nil {
		if err := s.Health.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (h *Health) validate() error {
	switch h.Type {
	case "", "http":
		if h.Path != "" && (!healthPathRe.MatchString(h.Path) || len(h.Path) > MaxHealthPathLen) {
			return newErr("health_path", "health.path is not a valid url path")
		}
	case "tcp":
		if h.Port < 1 || h.Port > 65535 {
			return newErr("health_port", "health.port must be 1..65535 for a tcp probe")
		}
	default:
		return newErr("health_type", "health.type %q is invalid", h.Type)
	}
	for _, d := range []string{h.StartPeriod, h.Interval, h.Timeout} {
		if d != "" && !durationRe.MatchString(d) {
			return newErr("health_duration", "health duration %q must look like 10s/500ms/2m", d)
		}
	}
	if h.Retries < 0 || h.Retries > 50 {
		return newErr("health_retries", "health.retries must be 0..50")
	}
	return nil
}

func validateEnvKey(k string) error {
	if !envKeyRe.MatchString(k) {
		return newErr("env_key", "env key %q is invalid", k)
	}
	if strings.HasPrefix(k, "PUSH_") {
		return newErr("env_key", "env key %q uses the reserved PUSH_ prefix", k)
	}
	return nil
}

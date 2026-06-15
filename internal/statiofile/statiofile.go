// Package pushfile parses the repo's statio.yaml — the dev-authored, unified description of
// the services to deploy — and converts it into the wire types (spec.AppIntent + proxy/dns).
// It decodes STRICTLY (KnownFields): any field outside the safe allowlist schema is a hard
// error, mirroring the closed-schema discipline of the wire (invariant #1/#19). YAML
// anchors/aliases and multi-document streams are rejected.
package statiofile

import (
	"bytes"
	"fmt"

	"github.com/accentiostudios/statio/internal/spec"
	"gopkg.in/yaml.v3"
)

// File is the whole statio.yaml. Only these fields exist; dangerous compose keys are
// unrepresentable.
type File struct {
	Services []Service  `yaml:"services"`
	Proxy    *ProxySpec `yaml:"proxy"`
	DNS      *DNSSpec   `yaml:"dns"`
}

type Service struct {
	Name      string            `yaml:"name"`
	Image     string            `yaml:"image"`
	Ports     []Port            `yaml:"ports"`
	Env       []string          `yaml:"env"`
	EnvInline map[string]string `yaml:"env_inline"`
	Health    *Health           `yaml:"health"`
	DependsOn []string          `yaml:"depends_on"`
	Volumes   []Volume          `yaml:"volumes"`
	Command   []string          `yaml:"command"`
	Restart   string            `yaml:"restart"`
}

// Port accepts a bare int or {container, host, protocol}.
type Port struct {
	Container int    `yaml:"container"`
	Host      int    `yaml:"host"`
	Protocol  string `yaml:"protocol"`
}

func (p *Port) UnmarshalYAML(n *yaml.Node) error {
	var i int
	if n.Decode(&i) == nil {
		p.Container, p.Host, p.Protocol = i, i, "tcp"
		return nil
	}
	type raw Port
	var r raw
	if err := n.Decode(&r); err != nil {
		return err
	}
	*p = Port(r)
	if p.Host == 0 {
		p.Host = p.Container
	}
	if p.Protocol == "" {
		p.Protocol = "tcp"
	}
	return nil
}

type Volume struct {
	Name     string `yaml:"name"`
	Path     string `yaml:"path"`
	ReadOnly bool   `yaml:"read_only"`
}

type Health struct {
	Type        string `yaml:"type"`
	Path        string `yaml:"path"`
	Port        int    `yaml:"port"`
	StartPeriod string `yaml:"start_period"`
	Interval    string `yaml:"interval"`
	Timeout     string `yaml:"timeout"`
	Retries     int    `yaml:"retries"`
}

type ProxySpec struct {
	Domain       string `yaml:"domain"`
	Upstream     string `yaml:"upstream"`
	UpstreamPort int    `yaml:"upstream_port"`
	Scheme       string `yaml:"scheme"`
	Websockets   bool   `yaml:"websockets"`
	HTTP2        bool   `yaml:"http2"`
	HSTS         bool   `yaml:"hsts"`
	ForceHTTPS   bool   `yaml:"force_https"`
}

type DNSSpec struct {
	Domain string `yaml:"domain"`
}

// Parse decodes statio.yaml strictly.
func Parse(data []byte) (*File, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse statio.yaml: %w", err)
	}
	// Reject a second YAML document (multi-doc smuggling).
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("statio.yaml must contain a single document")
	}
	if len(f.Services) == 0 {
		return nil, fmt.Errorf("statio.yaml: services must not be empty")
	}
	return &f, nil
}

// AppIntent converts the parsed file into the signed wire AppIntent.
func (f *File) AppIntent() *spec.AppIntent {
	out := &spec.AppIntent{}
	for _, s := range f.Services {
		ss := spec.Service{
			Name: s.Name, Image: s.Image, Env: s.Env, EnvInline: s.EnvInline,
			DependsOn: s.DependsOn, Command: s.Command, Restart: s.Restart,
		}
		for _, p := range s.Ports {
			ss.Ports = append(ss.Ports, spec.Port{Container: p.Container, Host: p.Host, Protocol: p.Protocol})
		}
		for _, v := range s.Volumes {
			ss.Volumes = append(ss.Volumes, spec.Volume{Name: v.Name, Path: v.Path, ReadOnly: v.ReadOnly})
		}
		if s.Health != nil {
			ss.Health = &spec.Health{Type: s.Health.Type, Path: s.Health.Path, Port: s.Health.Port,
				StartPeriod: s.Health.StartPeriod, Interval: s.Health.Interval, Timeout: s.Health.Timeout, Retries: s.Health.Retries}
		}
		out.Services = append(out.Services, ss)
	}
	return out
}

// ProxyWire converts the optional proxy block to the wire ProxySpec, or nil.
func (f *File) ProxyWire() *spec.ProxySpec {
	if f.Proxy == nil || f.Proxy.Domain == "" {
		return nil
	}
	p := f.Proxy
	return &spec.ProxySpec{
		Enabled: true, Domain: p.Domain, UpstreamHost: p.Upstream, UpstreamPort: p.UpstreamPort,
		Scheme: orDefault(p.Scheme, "http"), SSL: true, ForceHTTPS: p.ForceHTTPS,
		HTTP2: p.HTTP2, HSTS: p.HSTS, Websockets: p.Websockets,
	}
}

// DNSWire converts the optional dns block to the wire DNSSpec, or nil.
func (f *File) DNSWire() *spec.DNSSpec {
	if f.DNS == nil || f.DNS.Domain == "" {
		return nil
	}
	return &spec.DNSSpec{Enabled: true, Domain: f.DNS.Domain}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

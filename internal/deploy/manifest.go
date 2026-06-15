// Package deploy orchestrates the per-service deploy pipeline: binding -> admit -> verify
// -> pull -> env merge -> generate compose -> recreate -> health -> proxy -> dns -> persist.
// External effects (signature verification, image pull, NPMplus, Cloudflare) are reached
// through interfaces so the orchestration can be unit-tested with fakes.
package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	manifestAPIVersion = "statio/v1"
	manifestKind       = "ServiceDeploy"
)

// ServiceManifest (service.yaml) is the THIN server-side anchor for an accepted service.
// It holds only what a signed payload must NOT be able to assert for itself: the allowed
// primary image repository, the cosign signer override, the dependency-registry allowlist
// and service cap, the proxy/dns allowlists, and rollback policy. The container shape
// (ports, volumes, env, health) comes from the signed app_intent and is turned into a
// compose file by internal/compose — never authored here.
type ServiceManifest struct {
	APIVersion  string         `yaml:"apiVersion"`
	Kind        string         `yaml:"kind"`
	Name        string         `yaml:"name"`
	Signer      *SignerConfig  `yaml:"signer,omitempty"`
	Image       ImageConfig    `yaml:"image"`
	Registries  []string       `yaml:"registries,omitempty"`   // dependency registry allowlist
	MaxServices int            `yaml:"max_services,omitempty"` // cap on app_intent.services
	Proxy       ProxyPolicy    `yaml:"proxy"`
	DNS         DNSPolicy      `yaml:"dns"`
	Rollback    RollbackConfig `yaml:"rollback"`

	dir string // absolute service dir, set on load
}

// SignerConfig overrides the global cosign identity for this service's IMAGE.
type SignerConfig struct {
	OIDCIssuer     string `yaml:"oidc_issuer"`
	Identity       string `yaml:"identity"`
	IdentityRegexp string `yaml:"identity_regexp"`
}

// ImageConfig pins the allowed repository for your-app (the image-less service). The event
// digest references EXACTLY this repo (repo-equality) — the event picks WHICH signed
// digest, never an arbitrary image.
type ImageConfig struct {
	Repository string `yaml:"repository"`
}

// ProxyPolicy bounds what reverse-proxy config a payload may request.
type ProxyPolicy struct {
	AllowedDomainSuffixes []string `yaml:"allowed_domain_suffixes"`
	AllowedUpstreamHosts  []string `yaml:"allowed_upstream_hosts"`
}

// DNSPolicy bounds which domains a payload may point at this server.
type DNSPolicy struct {
	AllowedDomainSuffixes []string `yaml:"allowed_domain_suffixes"`
}

// RollbackConfig controls automatic rollback behavior.
type RollbackConfig struct {
	Enabled   bool   `yaml:"enabled"`
	EnvPolicy string `yaml:"env_policy"`
}

// defaultRegistries is the dependency-registry allowlist applied when service.yaml does
// not set one (postgres/redis from docker.io, app deps from ghcr.io).
var defaultRegistries = []string{"docker.io", "ghcr.io"}

const defaultMaxServices = 10

// LoadManifest reads and validates a service manifest from its directory.
func LoadManifest(serviceDir string) (*ServiceManifest, error) {
	path := filepath.Join(serviceDir, "manifest.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m ServiceManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	m.dir = serviceDir
	if len(m.Registries) == 0 {
		m.Registries = defaultRegistries
	}
	if m.MaxServices == 0 {
		m.MaxServices = defaultMaxServices
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Dir returns the service directory.
func (m *ServiceManifest) Dir() string { return m.dir }

// Validate enforces structural correctness of the anchor.
func (m *ServiceManifest) Validate() error {
	if m.APIVersion != manifestAPIVersion || m.Kind != manifestKind {
		return fmt.Errorf("manifest: expected apiVersion %q kind %q", manifestAPIVersion, manifestKind)
	}
	if m.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	if m.dir != "" && filepath.Base(m.dir) != m.Name {
		return fmt.Errorf("manifest: name %q must equal dir name %q", m.Name, filepath.Base(m.dir))
	}
	if m.Image.Repository == "" {
		return fmt.Errorf("manifest: image.repository is required")
	}
	return nil
}

// CheckProxyDomain reports whether domain is permitted by the proxy allowlist.
func (m *ServiceManifest) CheckProxyDomain(domain string) bool {
	return matchSuffix(domain, m.Proxy.AllowedDomainSuffixes)
}

// CheckUpstreamHost reports whether host is an allowlisted local container.
func (m *ServiceManifest) CheckUpstreamHost(host string) bool {
	if host == "" {
		return true // empty means "default", resolved server-side
	}
	for _, h := range m.Proxy.AllowedUpstreamHosts {
		if h == host {
			return true
		}
	}
	return false
}

// CheckDNSDomain reports whether domain is permitted by the dns allowlist.
func (m *ServiceManifest) CheckDNSDomain(domain string) bool {
	return matchSuffix(domain, m.DNS.AllowedDomainSuffixes)
}

func matchSuffix(domain string, suffixes []string) bool {
	for _, s := range suffixes {
		if domain == s || strings.HasSuffix(domain, "."+s) {
			return true
		}
	}
	return false
}

func isLoopbackHost(h string) bool {
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

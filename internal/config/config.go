// Package config loads and validates the global agent config (/etc/statio/config.yaml).
// Validation is fail-closed: a missing or loose cosign identity, an unanchored identity
// regexp, or a malformed public IP aborts startup rather than weakening a security gate.
package config

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/accentiostudios/statio/internal/fsutil"
	"gopkg.in/yaml.v3"
)

// Config is the parsed global config.
type Config struct {
	Hostname    string           `yaml:"hostname"`
	ListenPort  int              `yaml:"listen_port"`
	Tailscale   TailscaleConfig  `yaml:"tailscale"`
	Cosign      CosignConfig     `yaml:"cosign"`
	Registry    RegistryConfig   `yaml:"registry"`
	NPMplus     NPMplusConfig    `yaml:"npmplus"`
	Cloudflare  CloudflareConfig `yaml:"cloudflare"`
	DNS         DNSConfig        `yaml:"dns"`
	ServicesDir string           `yaml:"services_dir"`
	StateDir    string           `yaml:"state_dir"`
	// RunDir is the tmpfs base (systemd RuntimeDirectory) where per-service compose + env
	// files live. Secrets are written here (RAM), never to persistent disk.
	RunDir   string `yaml:"run_dir"`
	LogLevel string `yaml:"log_level"`
}

// TailscaleConfig configures the embedded tsnet node.
type TailscaleConfig struct {
	OAuthFile string   `yaml:"oauth_file"`
	Tags      []string `yaml:"tags"`
	StateDir  string   `yaml:"state_dir"`
}

// CosignConfig is the default keyless verification policy. A per-service manifest may
// override it. Exactly one of Identity / IdentityRegexp must be set.
type CosignConfig struct {
	OIDCIssuer      string `yaml:"oidc_issuer"`
	Identity        string `yaml:"identity"`
	IdentityRegexp  string `yaml:"identity_regexp"`
	TrustedRootFile string `yaml:"trusted_root_file"`
	RequireTlog     bool   `yaml:"require_tlog"`
	RequireSCT      bool   `yaml:"require_sct"`
}

// RegistryConfig holds the GHCR pull credentials path (for private repos).
type RegistryConfig struct {
	GHCRAuthFile string `yaml:"ghcr_auth_file"`
}

// NPMplusConfig is optional. When BaseURL is empty, reverse-proxy reconciliation is
// disabled (internal-only services).
type NPMplusConfig struct {
	BaseURL         string `yaml:"base_url"`
	CredentialsFile string `yaml:"credentials_file"`
}

// CloudflareConfig is optional. When CredentialsFile is empty, DNS reconciliation is
// disabled. ZoneApex is pinned at init so the agent can enforce fqdn zone-membership
// offline (no Zone:Read at deploy time).
type CloudflareConfig struct {
	CredentialsFile string `yaml:"credentials_file"`
	ZoneApex        string `yaml:"zone_apex"`
}

// DNSConfig holds the record content (the server's pinned public IP) and record policy.
type DNSConfig struct {
	PublicIP string `yaml:"public_ip"`
	TTL      int    `yaml:"ttl"`
	Proxied  bool   `yaml:"proxied"`
}

// Load reads, defaults, and validates the config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.ListenPort == 0 {
		c.ListenPort = 443
	}
	if len(c.Tailscale.Tags) == 0 {
		c.Tailscale.Tags = []string{"tag:agent"}
	}
	if c.Tailscale.StateDir == "" {
		c.Tailscale.StateDir = "/var/lib/statio/tsnet"
	}
	if c.ServicesDir == "" {
		c.ServicesDir = "/etc/statio/services"
	}
	if c.StateDir == "" {
		c.StateDir = "/var/lib/statio"
	}
	if c.RunDir == "" {
		c.RunDir = "/run/statio"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.DNS.TTL == 0 {
		c.DNS.TTL = 1 // Cloudflare "automatic"
	}
	if c.Cosign.OIDCIssuer == "" {
		c.Cosign.OIDCIssuer = "https://token.actions.githubusercontent.com"
	}
}

// Validate enforces the fail-closed invariants.
func (c *Config) Validate() error {
	if c.Hostname == "" {
		return fmt.Errorf("config: hostname is required")
	}
	if err := c.Cosign.validate(); err != nil {
		return err
	}
	if c.DNS.PublicIP != "" && net.ParseIP(c.DNS.PublicIP) == nil {
		return fmt.Errorf("config: dns.public_ip %q is not a valid IP", c.DNS.PublicIP)
	}
	// DNS enabled (cloudflare creds present) requires a pinned IP and zone apex.
	if c.Cloudflare.CredentialsFile != "" {
		if c.DNS.PublicIP == "" {
			return fmt.Errorf("config: cloudflare configured but dns.public_ip is empty")
		}
		if c.Cloudflare.ZoneApex == "" {
			return fmt.Errorf("config: cloudflare configured but zone_apex is empty")
		}
	}
	return nil
}

var anchoredRe = regexp.MustCompile(`^\^.*\$$`)

func (cs *CosignConfig) validate() error {
	if cs.OIDCIssuer == "" {
		return fmt.Errorf("config: cosign.oidc_issuer is required")
	}
	hasID := cs.Identity != ""
	hasRe := cs.IdentityRegexp != ""
	// At most one global identity. Neither is allowed: each accepted service pins its own
	// signer (manifest.signer), so a single server can host apps from many repos/orgs. A
	// service with no signer AND no global identity fails closed at verify time.
	if hasID && hasRe {
		return fmt.Errorf("config: set at most one of cosign.identity or cosign.identity_regexp")
	}
	if hasRe {
		if !anchoredRe.MatchString(cs.IdentityRegexp) {
			return fmt.Errorf("config: cosign.identity_regexp must be anchored with ^ and $")
		}
		if _, err := regexp.Compile(cs.IdentityRegexp); err != nil {
			return fmt.Errorf("config: cosign.identity_regexp does not compile: %w", err)
		}
		// Forbid a wildcard over the owner/repo prefix that would accept any repo.
		if strings.Contains(cs.IdentityRegexp, ".*github.com") || strings.HasPrefix(cs.IdentityRegexp, "^.*") {
			return fmt.Errorf("config: cosign.identity_regexp must not wildcard the owner/repo prefix")
		}
	}
	return nil
}

// ValidateSecretPerms checks that every configured secret file is 0600 root. Called at
// agent startup; fails closed.
func (c *Config) ValidateSecretPerms() error {
	for _, p := range c.secretFiles() {
		if err := fsutil.CheckPerm(p); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) secretFiles() []string {
	var fs []string
	add := func(p string) {
		if p != "" {
			fs = append(fs, p)
		}
	}
	add(c.Tailscale.OAuthFile)
	add(c.Registry.GHCRAuthFile)
	add(c.NPMplus.CredentialsFile)
	add(c.Cloudflare.CredentialsFile)
	return fs
}

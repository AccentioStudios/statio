package config

import "testing"

func TestValidateOK(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("expected valid: %v", err)
	}
}

func TestValidateMissingHostname(t *testing.T) {
	c := valid()
	c.Hostname = ""
	if c.Validate() == nil {
		t.Fatal("expected hostname required")
	}
}

func TestValidateIdentityExclusive(t *testing.T) {
	c := valid()
	c.Cosign.IdentityRegexp = "^https://github.com/org/.+@refs/heads/main$"
	if c.Validate() == nil {
		t.Fatal("expected rejection: both identity and identity_regexp set")
	}
	// regexp only is fine
	c.Cosign.Identity = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("anchored regexp should pass: %v", err)
	}
}

func TestValidateNoGlobalIdentity(t *testing.T) {
	c := valid()
	// No global identity is allowed: each app pins its own signer via `statio app add`.
	c.Cosign.Identity = ""
	c.Cosign.IdentityRegexp = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("missing global identity must be allowed (per-app signers): %v", err)
	}
}

func TestValidateUnanchoredRegexp(t *testing.T) {
	c := valid()
	c.Cosign.Identity = ""
	c.Cosign.IdentityRegexp = "github.com/org/repo"
	if c.Validate() == nil {
		t.Fatal("expected unanchored regexp rejection")
	}
}

func TestValidateWildcardRegexp(t *testing.T) {
	c := valid()
	c.Cosign.Identity = ""
	c.Cosign.IdentityRegexp = "^.*github.com/.+$"
	if c.Validate() == nil {
		t.Fatal("expected owner/repo wildcard rejection")
	}
}

func TestValidateBadPublicIP(t *testing.T) {
	c := valid()
	c.DNS.PublicIP = "not-an-ip"
	if c.Validate() == nil {
		t.Fatal("expected invalid IP rejection")
	}
}

func TestValidateCloudflareNeedsIP(t *testing.T) {
	c := valid()
	c.Cloudflare.CredentialsFile = "/etc/statio/secrets/cloudflare.json"
	c.Cloudflare.ZoneApex = "example.com"
	if c.Validate() == nil {
		t.Fatal("expected cloudflare-without-IP rejection")
	}
	c.DNS.PublicIP = "203.0.113.10"
	if err := c.Validate(); err != nil {
		t.Fatalf("should pass with IP: %v", err)
	}
}

func TestValidateUpstreamModeDefaultsLoopback(t *testing.T) {
	c := valid() // applyDefaults ran
	if c.NPMplus.UpstreamMode != UpstreamModeLoopback {
		t.Fatalf("upstream_mode default = %q, want %q", c.NPMplus.UpstreamMode, UpstreamModeLoopback)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("default loopback should pass: %v", err)
	}
}

func TestValidateUpstreamModeInvalid(t *testing.T) {
	c := valid()
	c.NPMplus.UpstreamMode = "host"
	if c.Validate() == nil {
		t.Fatal("expected rejection of an unknown upstream_mode")
	}
}

func TestValidateContainerModeNeedsNetwork(t *testing.T) {
	c := valid()
	c.NPMplus.UpstreamMode = UpstreamModeContainer
	if c.Validate() == nil {
		t.Fatal("expected container mode without a network to be rejected")
	}
	c.NPMplus.Network = "statio-edge"
	if err := c.Validate(); err != nil {
		t.Fatalf("container mode with a network should pass: %v", err)
	}
}

func TestValidateLoopbackRejectsNetwork(t *testing.T) {
	c := valid()
	c.NPMplus.Network = "statio-edge" // network with loopback is a config mistake
	if c.Validate() == nil {
		t.Fatal("expected loopback+network to be rejected")
	}
}

func TestValidateInsecureSkipVerifyLoopbackOnly(t *testing.T) {
	c := valid()
	c.NPMplus.BaseURL = "https://127.0.0.1:81"
	c.NPMplus.InsecureSkipVerify = true
	if err := c.Validate(); err != nil {
		t.Fatalf("loopback insecure_skip_verify should pass: %v", err)
	}
	// A remote base_url must never skip TLS verification (would open a MITM surface).
	c.NPMplus.BaseURL = "https://npm.example.com:81"
	if c.Validate() == nil {
		t.Fatal("expected remote insecure_skip_verify to be rejected")
	}
}

func TestIsLoopbackURL(t *testing.T) {
	cases := map[string]bool{
		"https://127.0.0.1:81":      true,
		"http://localhost:81":       true,
		"https://[::1]:81":          true,
		"https://npm.example.com":   false,
		"https://192.168.1.5:81":    false,
		"https://127.0.0.1.evil.io": false,
	}
	for u, want := range cases {
		if got := IsLoopbackURL(u); got != want {
			t.Errorf("IsLoopbackURL(%q) = %v, want %v", u, got, want)
		}
	}
}

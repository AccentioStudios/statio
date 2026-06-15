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

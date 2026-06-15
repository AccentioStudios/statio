package deploy

import "testing"

func TestEffectiveSigner(t *testing.T) {
	// Per-service signer fully overrides the global identity.
	m := &ServiceManifest{Signer: &SignerConfig{OIDCIssuer: "iss-svc", Identity: "id-svc"}}
	if s := m.EffectiveSigner("iss-global", "id-global", ""); s.OIDCIssuer != "iss-svc" || s.Identity != "id-svc" {
		t.Errorf("per-service override not applied: %+v", s)
	}

	// No per-service signer → falls back to the global identity.
	none := &ServiceManifest{}
	if s := none.EffectiveSigner("iss-global", "id-global", ""); s.OIDCIssuer != "iss-global" || s.Identity != "id-global" {
		t.Errorf("global fallback failed: %+v", s)
	}

	// Neither set → empty identity (the verifier fails closed; never a wildcard verify).
	if s := none.EffectiveSigner("iss-global", "", ""); s.Identity != "" || s.IdentityRegexp != "" {
		t.Errorf("expected empty identity, got %+v", s)
	}

	// Issuer-only override keeps the global identity (only identity/regexp swap as a pair).
	io := &ServiceManifest{Signer: &SignerConfig{OIDCIssuer: "iss-svc"}}
	if s := io.EffectiveSigner("iss-global", "id-global", ""); s.OIDCIssuer != "iss-svc" || s.Identity != "id-global" {
		t.Errorf("issuer-only override wrong: %+v", s)
	}

	// A per-service regexp replaces the global exact identity.
	re := &ServiceManifest{Signer: &SignerConfig{IdentityRegexp: "^https://github.com/org/.+$"}}
	if s := re.EffectiveSigner("iss-global", "id-global", ""); s.Identity != "" || s.IdentityRegexp == "" {
		t.Errorf("regexp override should clear exact identity: %+v", s)
	}
}

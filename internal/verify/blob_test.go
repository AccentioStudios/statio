package verify

import (
	"context"
	"testing"

	"github.com/accentiostudios/statio/internal/deploy"
)

// These cover the fail-closed guards that run BEFORE any trust-root fetch (no network).
// The full accept/reject-by-signature roundtrip is NOT automated: it needs a live keyless
// cosign signature (OIDC + Fulcio + Rekor), so it is only exercised end-to-end on a real
// deploy. That gap is why the legacy-vs-new bundle-format mismatch shipped undetected —
// SignAndWrap must emit `--new-bundle-format` for bundle.Bundle to parse it (see deploy.go).
func TestVerifyBlobFailsClosed(t *testing.T) {
	v := New(true, true, "")
	goodSigner := deploy.EffectiveSigner{OIDCIssuer: "https://token.actions.githubusercontent.com", Identity: "https://github.com/org/repo/.github/workflows/deploy.yml@refs/heads/main"}

	if err := v.VerifyBlob(context.Background(), nil, []byte(`{}`), goodSigner); err == nil {
		t.Error("empty payload must be rejected")
	}
	if err := v.VerifyBlob(context.Background(), []byte("x"), []byte(`{}`), deploy.EffectiveSigner{}); err == nil {
		t.Error("empty signer identity must be rejected (no wildcard verify)")
	}
	if err := v.VerifyBlob(context.Background(), []byte("x"), []byte(`{}`), deploy.EffectiveSigner{OIDCIssuer: "https://x"}); err == nil {
		t.Error("issuer-only (no subject/regexp) must be rejected")
	}
}

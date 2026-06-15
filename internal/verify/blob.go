package verify

import (
	"bytes"
	"context"
	"fmt"

	"github.com/accentiostudios/push/internal/deploy"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	sgverify "github.com/sigstore/sigstore-go/pkg/verify"
)

// VerifyBlob verifies a cosign keyless signature (carried as a Sigstore bundle) over the
// EXACT payload bytes, against the same trusted identity used for images. It is the
// authenticity gate for the deploy payload (invariant #6/#16): a network-position-only
// attacker cannot forge env/proxy/dns config because they lack a bundle that a Fulcio
// cert with the expected OIDC identity signed.
//
// It shares the trusted root and the tlog/SCT hardening with the image path so the two
// cannot drift: observer timestamps are always required (short-lived Fulcio certs need a
// trusted time), and transparency-log inclusion + SCT are required when the config flags
// are set (default true). No *Unsafe / current-time shortcut is used.
func (v *Verifier) VerifyBlob(ctx context.Context, payload, bundleJSON []byte, signer deploy.EffectiveSigner) error {
	if len(payload) == 0 {
		return fmt.Errorf("empty payload")
	}
	if signer.OIDCIssuer == "" || (signer.Identity == "" && signer.IdentityRegexp == "") {
		// Fail closed: never verify against an empty/wildcard identity.
		return fmt.Errorf("signer identity not configured")
	}
	tm, err := v.trustedMaterial()
	if err != nil {
		return fmt.Errorf("load trusted root: %w", err)
	}

	opts := []sgverify.VerifierOption{sgverify.WithObserverTimestamps(1)}
	if v.requireTlog {
		opts = append(opts, sgverify.WithTransparencyLog(1))
	}
	if v.requireSCT {
		opts = append(opts, sgverify.WithSignedCertificateTimestamps(1))
	}
	sev, err := sgverify.NewSignedEntityVerifier(tm, opts...)
	if err != nil {
		return fmt.Errorf("build blob verifier: %w", err)
	}

	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return fmt.Errorf("parse signature bundle: %w", err)
	}

	certID, err := sgverify.NewShortCertificateIdentity(signer.OIDCIssuer, "", signer.Identity, signer.IdentityRegexp)
	if err != nil {
		return fmt.Errorf("build certificate identity: %w", err)
	}

	policy := sgverify.NewPolicy(
		sgverify.WithArtifact(bytes.NewReader(payload)),
		sgverify.WithCertificateIdentity(certID),
	)
	if _, err := sev.Verify(&b, policy); err != nil {
		return fmt.Errorf("payload signature verification failed: %w", err)
	}
	return nil
}

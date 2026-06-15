// Package verify implements cosign keyless signature verification of an image digest.
// It satisfies deploy.Verifier and is the first hard gate of the pipeline: nothing is
// pulled or executed before a digest's signature is verified against the expected
// GitHub Actions OIDC identity.
//
// Invariants (from the security review): exact identity match by default; tlog and SCT
// thresholds are honored (require flags default true in config); no *Unsafe verifier
// option is ever used; the issuer is pinned exactly. A per-service manifest may override
// the identity, but the config layer forbids an unanchored/owner-wildcard regexp.
//
// cosign v3 takes trust roots via CheckOpts.TrustedMaterial, sourced from the Sigstore
// Public Good TUF repository (sigstore-go). An optional pinned trusted_root.json on disk
// supports offline/air-gapped hosts.
package verify

import (
	"context"
	"fmt"
	"sync"

	"github.com/accentiostudios/statio/internal/deploy"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/sigstore/cosign/v3/pkg/cosign"
	"github.com/sigstore/sigstore-go/pkg/root"
)

// Verifier verifies cosign keyless signatures in-process.
type Verifier struct {
	requireTlog     bool
	requireSCT      bool
	trustedRootPath string // empty => live TUF

	mu      sync.Mutex
	trusted root.TrustedMaterial
}

// New builds a Verifier. When requireTlog/requireSCT are true (the config default), the
// transparency-log inclusion and SCT are mandatory, so a stripped bundle is rejected.
// trustedRootPath, when set, loads a pinned offline trusted_root.json instead of TUF.
func New(requireTlog, requireSCT bool, trustedRootPath string) *Verifier {
	return &Verifier{requireTlog: requireTlog, requireSCT: requireSCT, trustedRootPath: trustedRootPath}
}

func (v *Verifier) trustedMaterial() (root.TrustedMaterial, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.trusted != nil {
		return v.trusted, nil
	}
	var (
		tm  root.TrustedMaterial
		err error
	)
	if v.trustedRootPath != "" {
		tm, err = root.NewTrustedRootFromPath(v.trustedRootPath)
	} else {
		tm, err = root.FetchTrustedRoot()
	}
	if err != nil {
		return nil, err
	}
	v.trusted = tm
	return tm, nil
}

// Verify checks that repository@digest has a cosign signature matching the expected
// signer. It returns nil only on a verified, identity-matched signature.
func (v *Verifier) Verify(ctx context.Context, repository, digest string, signer deploy.EffectiveSigner) error {
	ref, err := name.NewDigest(repository + "@" + digest)
	if err != nil {
		return fmt.Errorf("bad image reference: %w", err)
	}
	tm, err := v.trustedMaterial()
	if err != nil {
		return fmt.Errorf("load trusted root: %w", err)
	}

	co := &cosign.CheckOpts{
		TrustedMaterial: tm,
		Identities: []cosign.Identity{{
			Issuer:        signer.OIDCIssuer,
			Subject:       signer.Identity,
			SubjectRegExp: signer.IdentityRegexp,
		}},
		IgnoreSCT:  !v.requireSCT,
		IgnoreTlog: !v.requireTlog,
	}

	_, bundleVerified, err := cosign.VerifyImageSignatures(ctx, ref, co)
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	if (v.requireTlog || v.requireSCT) && !bundleVerified {
		return fmt.Errorf("signature present but transparency-log/SCT verification did not pass")
	}
	return nil
}

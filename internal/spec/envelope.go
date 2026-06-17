package spec

import (
	"bytes"
	"encoding/json"
	"io"
)

// MaxEnvelopeBytes caps the whole signed envelope (base64 payload + cosign bundle)
// read from the wire BEFORE any parse. It is larger than MaxBodyBytes because the
// inner payload is base64-expanded (~4/3) and the bundle (signature + Fulcio cert +
// Rekor proof) adds a few KiB.
const MaxEnvelopeBytes = 512 * 1024

// Envelope is the outer, signed wire object. The agent verifies the cosign Bundle over
// the EXACT bytes in Payload and then decodes those SAME bytes as a DeployRequest. The
// payload is carried base64-encoded (Go marshals/unmarshals []byte as base64) so the
// signed bytes are transported verbatim — there is never a re-marshal between verify and
// decode, which would break byte-equality (invariant #1/#15).
type Envelope struct {
	// Payload is the raw bytes of the DeployRequest JSON that were signed. base64 on the wire.
	Payload []byte `json:"payload"`
	// Bundle is the cosign keyless bundle (signature + cert + transparency proof), opaque
	// to this package; the verify package interprets it.
	Bundle json.RawMessage `json:"bundle"`
	// Registry is an OPTIONAL, short-lived pull credential the CI forwards so the agent (a
	// separate machine with no GitHub identity) can read a PRIVATE image's cosign .sig and pull
	// it. It is deliberately OUTSIDE the signed Payload: it is a transient capability, not a trust
	// anchor (image integrity comes from the cosign verify + digest pinning, so a wrong token only
	// makes the pull fail — it can never substitute an image). The agent uses it in-memory for one
	// deploy and discards it; it is NEVER persisted, logged, or audited. Absent for public images.
	Registry *RegistryAuth `json:"registry,omitempty"`
}

// RegistryAuth is a username/token pair for one registry, used transiently for a single deploy.
type RegistryAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// DecodeEnvelope reads at most MaxEnvelopeBytes from r and strictly decodes the outer
// envelope. It requires a non-empty Payload and Bundle: a missing/empty bundle is a
// downgrade attempt and must fail closed here (invariant #14). It does NOT verify the
// signature or decode the payload — the caller does that in order:
// DecodeEnvelope -> verify.VerifyBlob(Payload, Bundle) -> DecodeBytes(Payload).
func DecodeEnvelope(r io.Reader) (*Envelope, error) {
	data, err := readCapped(r, MaxEnvelopeBytes)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var e Envelope
	if err := dec.Decode(&e); err != nil {
		return nil, newErr("schema", "invalid envelope: %v", err)
	}
	if dec.More() {
		return nil, newErr("schema", "unexpected trailing data after envelope")
	}
	if len(e.Payload) == 0 {
		return nil, newErr("no_signature", "envelope has an empty payload")
	}
	if len(bytes.TrimSpace(e.Bundle)) == 0 || bytes.Equal(bytes.TrimSpace(e.Bundle), []byte("null")) {
		// Fail closed: an unsigned/stripped request must hit no code path.
		return nil, newErr("no_signature", "envelope has no cosign bundle")
	}
	return &e, nil
}

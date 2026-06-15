// Package spec defines the statio/v1 wire contract: the JSON DeployRequest that the
// GitHub Action (via `statio deploy`) POSTs to the agent over the tailnet, plus the
// closed-schema validation shared by both ends.
//
// This package is the single source of truth for the contract. It is imported by
// the agent (server, authoritative validation) AND by the deploy client (runner,
// fail-fast validation) so the schema can never drift between the two parts.
//
// Security invariant (data-only / no-RCE): every field below is a typed scalar,
// bool, enum, or map-of-literals. No field is ever a shell string, command, raw
// API URL/method/body, executable path, DNS record type, or DNS target IP. Those
// all live in server-side config. See validate.go.
package spec

const (
	// APIVersion is the only accepted apiVersion value.
	APIVersion = "statio/v1"
	// Kind is the only accepted kind value.
	Kind = "DeployRequest"
)

// DeployRequest is the full wire payload (the inner, signed bytes of an Envelope).
// Decoded with DisallowUnknownFields and a hard byte cap; see DecodeBytes.
type DeployRequest struct {
	APIVersion   string            `json:"apiVersion"`
	Kind         string            `json:"kind"`
	Service      string            `json:"service"`
	Image        Image             `json:"image"`
	AppIntent    *AppIntent        `json:"app_intent"`
	EnvOverrides map[string]string `json:"env_overrides,omitempty"`
	Proxy        *ProxySpec        `json:"proxy,omitempty"`
	DNS          *DNSSpec          `json:"dns,omitempty"`

	// Binding fields — signed, and compared by the agent against its OWN pinned config
	// and per-service state (NOT self-asserted). They bind a payload to one target and
	// one moment, closing cross-server/cross-time replay (invariant #17).
	Audience  string `json:"audience"`   // = the target agent's MagicDNS hostname; must equal self
	DeploySeq int64  `json:"deploy_seq"` // monotonic per (server,service); must exceed last applied
	IssuedAt  string `json:"issued_at"`  // RFC3339
	Expiry    string `json:"expiry"`     // RFC3339; agent rejects if now > expiry

	// DeployID is an optional uuid-v4 used for idempotent replay dedupe.
	DeployID string `json:"deploy_id,omitempty"`
}

// Image references the artifact to deploy. The repository is carried so the agent
// can assert it EQUALS the service's server-configured repo (the event only picks
// WHICH signed digest of an allowed image, never an arbitrary image). The digest is
// immutable; tags are rejected.
type Image struct {
	Repository string `json:"repository"`
	Digest     string `json:"digest"`
}

// ProxySpec is the desired reverse-proxy state mapped onto NPMplus API calls. Fields
// that could become config injection (raw nginx) or SSRF (arbitrary upstream) are
// NOT here — they are pinned/allowlisted server-side.
type ProxySpec struct {
	Enabled      bool   `json:"enabled"`
	Domain       string `json:"domain"`
	UpstreamHost string `json:"upstream_host"`
	UpstreamPort int    `json:"upstream_port"`
	Scheme       string `json:"scheme"` // enum: http | https
	SSL          bool   `json:"ssl"`
	ForceHTTPS   bool   `json:"force_https"`
	HTTP2        bool   `json:"http2"`
	HSTS         bool   `json:"hsts"`
	Websockets   bool   `json:"websockets"`
}

// DNSSpec is the desired DNS state. Only the fqdn is event-carried; the record type
// (forced A) and target IP (server's pinned public IP) are server-side config, so an
// event can never repoint DNS off the server's own address.
type DNSSpec struct {
	Enabled bool   `json:"enabled"`
	Domain  string `json:"domain"`
}

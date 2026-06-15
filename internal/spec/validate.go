package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Caps. These are the single authoritative limits shared by both ends.
const (
	// MaxBodyBytes is the hard cap on the whole wire payload, enforced BEFORE decode.
	MaxBodyBytes = 256 * 1024
	// MaxEnvKeys is the max number of env override entries.
	MaxEnvKeys = 100
	// MaxEnvValueBytes is the max size of a single env override value.
	MaxEnvValueBytes = 4096
	// MaxEnvSectionBytes caps the summed size of all env keys+values.
	MaxEnvSectionBytes = 64 * 1024
	// maxHostnameLen is the RFC-1123 hostname length limit.
	maxHostnameLen = 253
)

var (
	serviceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	digestRe  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	envKeyRe  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)
	// repository: a registry path like ghcr.io/org/name. Lowercase, dot/slash/dash/underscore.
	repoRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,253}[a-z0-9]$`)
	labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// ValidationError is a typed validation failure. Code lets the agent map to an HTTP
// status (most codes => 400). It is safe to surface: it never contains a secret value.
type ValidationError struct {
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Code + ": " + e.Message }

func newErr(code, format string, args ...any) *ValidationError {
	return &ValidationError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// DecodeBytes decodes the EXACT bytes that were signed and verified (no re-marshal,
// no canonicalization) as a DeployRequest, with DisallowUnknownFields and the body cap.
// This is the byte-equality discipline (invariant #15): the caller passes the verified
// Envelope.Payload straight here. Anything that is not exactly statio/v1 DeployRequest is
// refused before the agent takes any action.
func DecodeBytes(data []byte) (*DeployRequest, error) {
	if int64(len(data)) > MaxBodyBytes {
		return nil, newErr("too_large", "payload exceeds %d bytes", MaxBodyBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var req DeployRequest
	if err := dec.Decode(&req); err != nil {
		return nil, newErr("schema", "invalid request body: %v", err)
	}
	if dec.More() {
		return nil, newErr("schema", "unexpected trailing data after JSON document")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return &req, nil
}

// Decode reads at most MaxBodyBytes from r and decodes via DecodeBytes. Kept for the
// client's local round-trip validation; the agent uses the Envelope path then DecodeBytes.
func Decode(r io.Reader) (*DeployRequest, error) {
	data, err := readCapped(r, MaxBodyBytes)
	if err != nil {
		return nil, err
	}
	return DecodeBytes(data)
}

// readCapped reads up to max bytes; if the source has more, it errors instead of
// silently truncating.
func readCapped(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, newErr("schema", "read body: %v", err)
	}
	if int64(len(data)) > max {
		return nil, newErr("too_large", "request body exceeds %d bytes", max)
	}
	return data, nil
}

// Validate runs the structural/format validation that is IDENTICAL on both ends.
//
// It deliberately does NOT perform server-side policy checks that need config or the
// service manifest: image repo-equality, domain zone-suffix membership, upstream_host
// container allowlist, and protected-env-key collisions. Those are enforced in the
// agent with access to the server config (see internal/deploy, internal/env).
func (r *DeployRequest) Validate() error {
	if r.APIVersion != APIVersion {
		return newErr("schema", "apiVersion must be %q", APIVersion)
	}
	if r.Kind != Kind {
		return newErr("schema", "kind must be %q", Kind)
	}
	if !serviceRe.MatchString(r.Service) {
		return newErr("service", "service must match %s", serviceRe.String())
	}
	if !repoRe.MatchString(r.Image.Repository) {
		return newErr("repository", "image.repository is not a valid registry path")
	}
	if !digestRe.MatchString(r.Image.Digest) {
		return newErr("digest", "image.digest must be sha256:<64 hex> (tags are rejected)")
	}
	if err := r.validateBinding(); err != nil {
		return err
	}
	if r.AppIntent == nil {
		return newErr("app_intent", "app_intent is required")
	}
	if err := r.AppIntent.validate(); err != nil {
		return err
	}
	if err := validateEnv(r.EnvOverrides); err != nil {
		return err
	}
	if r.Proxy != nil && r.Proxy.Enabled {
		if err := r.Proxy.validate(); err != nil {
			return err
		}
	}
	if r.DNS != nil && r.DNS.Enabled {
		if !isValidHostname(r.DNS.Domain) {
			return newErr("dns_domain", "dns.domain is not a valid hostname")
		}
	}
	return nil
}

// validateBinding checks the FORMAT of the target/freshness fields. The VALUE checks
// that need server state — audience == this agent, deploy_seq > last applied, now <=
// expiry — are enforced by the agent (see internal/agent), not here, because this
// validation is shared with the client and is stateless.
func (r *DeployRequest) validateBinding() error {
	if !isValidHostname(r.Audience) {
		return newErr("audience", "audience must be a valid hostname")
	}
	if r.DeploySeq < 0 {
		return newErr("deploy_seq", "deploy_seq must be non-negative")
	}
	issued, err := time.Parse(time.RFC3339, r.IssuedAt)
	if err != nil {
		return newErr("issued_at", "issued_at must be RFC3339")
	}
	expiry, err := time.Parse(time.RFC3339, r.Expiry)
	if err != nil {
		return newErr("expiry", "expiry must be RFC3339")
	}
	if !expiry.After(issued) {
		return newErr("expiry", "expiry must be after issued_at")
	}
	return nil
}

func validateEnv(env map[string]string) error {
	if len(env) > MaxEnvKeys {
		return newErr("env_count", "too many env overrides (max %d)", MaxEnvKeys)
	}
	total := 0
	for k, v := range env {
		if !envKeyRe.MatchString(k) {
			return newErr("env_key", "env key %q is invalid", k)
		}
		if strings.HasPrefix(k, "STATIO_") {
			return newErr("env_key", "env key %q uses the reserved STATIO_ prefix", k)
		}
		if len(v) > MaxEnvValueBytes {
			return newErr("env_value", "env value for %q exceeds %d bytes", k, MaxEnvValueBytes)
		}
		if !utf8.ValidString(v) {
			return newErr("env_value", "env value for %q is not valid UTF-8", k)
		}
		// Reject all control chars (incl. NUL, tab, newline). A newline could forge
		// an extra KEY=VALUE line in app.env; NUL could truncate the file. This is the
		// load-bearing rule that keeps env values opaque data.
		if i := strings.IndexFunc(v, isControl); i >= 0 {
			return newErr("env_value", "env value for %q contains a control character at byte %d", k, i)
		}
		total += len(k) + len(v)
	}
	if total > MaxEnvSectionBytes {
		return newErr("env_size", "env overrides exceed %d bytes total", MaxEnvSectionBytes)
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func (p *ProxySpec) validate() error {
	if !isValidHostname(p.Domain) {
		return newErr("proxy_domain", "proxy.domain is not a valid hostname")
	}
	if p.UpstreamPort < 1 || p.UpstreamPort > 65535 {
		return newErr("proxy_port", "proxy.upstream_port must be 1..65535")
	}
	if p.Scheme != "http" && p.Scheme != "https" {
		return newErr("proxy_scheme", "proxy.scheme must be http or https")
	}
	// upstream_host format only (the allowlist-to-local-container check is server-side).
	if p.UpstreamHost != "" && !labelRe.MatchString(p.UpstreamHost) {
		return newErr("proxy_upstream", "proxy.upstream_host is not a valid container name")
	}
	return nil
}

// isValidHostname reports whether s is a valid RFC-1123 DNS hostname.
func isValidHostname(s string) bool {
	if s == "" || len(s) > maxHostnameLen {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if !labelRe.MatchString(label) {
			return false
		}
	}
	return true
}

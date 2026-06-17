// Package client implements `statio deploy`: it builds the statio/v1 spec (validating it
// with the SAME internal/spec code the agent uses, so the contract can't drift), then
// POSTs it to the agent over the tailnet.
//
// Transport: the GitHub Action joins the tailnet on the runner host, so a standard HTTPS
// client reaches https://<magicdns-host>/deploy with a valid Tailscale-issued cert. No
// tsnet is needed client-side.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/accentiostudios/statio/internal/deploy"
	"github.com/accentiostudios/statio/internal/spec"
)

// Inputs are the per-deploy parameters (from the Action or operator flags).
type Inputs struct {
	Service      string
	Repository   string
	Digest       string
	AppIntent    *spec.AppIntent
	EnvOverrides map[string]string
	Proxy        *spec.ProxySpec
	DNS          *spec.DNSSpec
	Audience     string // the target agent's MagicDNS hostname (signed)
	DeploySeq    int64
	IssuedAt     string // RFC3339
	Expiry       string // RFC3339
	DeployID     string
}

// BuildSpec assembles and validates the inner (signed) payload bytes. It round-trips
// through spec.DecodeBytes so the client rejects an invalid request before signing. The
// returned bytes are EXACTLY what gets signed and what the agent decodes (byte-equality).
func BuildSpec(in Inputs) ([]byte, error) {
	req := spec.DeployRequest{
		APIVersion:   spec.APIVersion,
		Kind:         spec.Kind,
		Service:      in.Service,
		Image:        spec.Image{Repository: in.Repository, Digest: in.Digest},
		AppIntent:    in.AppIntent,
		EnvOverrides: in.EnvOverrides,
		Proxy:        in.Proxy,
		DNS:          in.DNS,
		Audience:     in.Audience,
		DeploySeq:    in.DeploySeq,
		IssuedAt:     in.IssuedAt,
		Expiry:       in.Expiry,
		DeployID:     in.DeployID,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := spec.DecodeBytes(body); err != nil {
		return nil, fmt.Errorf("invalid deploy spec: %w", err)
	}
	return body, nil
}

// SignAndWrap cosign-signs the payload bytes (keyless, via the cosign CLI which is present
// on the Action runner and picks up the run's ambient OIDC token) and wraps them with the
// resulting Sigstore bundle into the wire envelope. The SAME bytes are signed and carried.
//
// --new-bundle-format is REQUIRED: without it cosign writes the legacy LocalSignedPayload JSON
// ({"base64Signature","cert","rekorBundle"}), which the agent's sigstore-go bundle.Bundle parser
// rejects with `unknown field "base64Signature"`. The flag emits the protobuf Sigstore bundle
// (application/vnd.dev.sigstore.bundle) that bundle.Bundle.UnmarshalJSON expects.
func SignAndWrap(ctx context.Context, payload []byte) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "statio-sign")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	payloadPath := tmp + string(os.PathSeparator) + "payload.json"
	bundlePath := tmp + string(os.PathSeparator) + "bundle.json"
	if err := os.WriteFile(payloadPath, payload, 0o600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "cosign", "sign-blob", "--yes", "--new-bundle-format", "--bundle", bundlePath, payloadPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("cosign sign-blob failed: %w: %s", err, string(out))
	}
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("read signature bundle: %w", err)
	}
	env := spec.Envelope{Payload: payload, Bundle: json.RawMessage(bundle)}
	return json.Marshal(env)
}

// ValidateSpecJSON validates raw inner payload bytes (used for local fail-fast).
func ValidateSpecJSON(body []byte) error {
	_, err := spec.DecodeBytes(body)
	return err
}

// Deploy POSTs the spec to https://<target>/deploy and returns the typed result. A
// non-2xx with a JSON body is still decoded so the caller can surface per-stage status.
func Deploy(ctx context.Context, target string, body []byte, timeout time.Duration) (*deploy.Result, int, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	url := fmt.Sprintf("https://%s/deploy", target)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := &http.Client{Timeout: timeout}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("deploy request to %s failed: %w", target, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var res deploy.Result
	if len(data) > 0 {
		_ = json.Unmarshal(data, &res)
	}
	return &res, resp.StatusCode, nil
}

// GetLogs fetches the redacted audit tail from https://<target>/logs/<service>.
func GetLogs(ctx context.Context, target, service string, timeout time.Duration) ([]byte, int, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	url := fmt.Sprintf("https://%s/logs/%s", target, service)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	hc := &http.Client{Timeout: timeout}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("logs request to %s failed: %w", target, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return data, resp.StatusCode, nil
}

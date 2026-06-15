// Package tailscale is a thin client for the Tailscale REST API. It is used by
// `statio init server` to mint the shared tag:ci auth key that CI uses to reach the agent,
// so the operator never has to create that credential by hand in the Tailscale console.
//
// It does an OAuth client-credentials exchange (the bootstrap OAuth client given to
// init server) and then creates a tagged auth key. Errors never include the client secret
// or the minted key.
package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const apiBase = "https://api.tailscale.com"

// apiMessage extracts Tailscale's human-readable error ("message") from a non-2xx response so
// the operator sees WHY the request failed (wrong tag, expiry too long, missing scope, …)
// instead of a bare status code. It is only ever called on error responses, whose body
// describes the request — it never contains the client secret or the minted key.
func apiMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return e.Message
	}
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "(no response body)"
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// Client talks to the Tailscale API for the default tailnet ("-") of the authenticated client.
type Client struct {
	baseURL string
	hc      *http.Client
}

// New returns a client. An empty baseURL uses the public Tailscale API (tests pass a stub).
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = apiBase
	}
	return &Client{baseURL: baseURL, hc: &http.Client{Timeout: 20 * time.Second}}
}

// Token exchanges an OAuth client id+secret for a short-lived API access token
// (client_credentials grant). The secret is never echoed in errors.
func (c *Client) Token(ctx context.Context, clientID, clientSecret string) (string, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v2/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("tailscale oauth: request failed")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("tailscale oauth: status %d: %s", resp.StatusCode, apiMessage(data))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("tailscale oauth: decode response: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("tailscale oauth: empty access_token")
	}
	return out.AccessToken, nil
}

// AuthKeyOpts configures a minted auth key.
type AuthKeyOpts struct {
	Tags          []string
	Reusable      bool
	Ephemeral     bool
	Preauthorized bool
	ExpirySeconds int
	Description   string
}

// MintAuthKey creates a tagged auth key and returns the key string (tskey-auth-…).
func (c *Client) MintAuthKey(ctx context.Context, token string, o AuthKeyOpts) (string, error) {
	body := map[string]any{
		"capabilities": map[string]any{
			"devices": map[string]any{
				"create": map[string]any{
					"reusable":      o.Reusable,
					"ephemeral":     o.Ephemeral,
					"preauthorized": o.Preauthorized,
					"tags":          o.Tags,
				},
			},
		},
		"expirySeconds": o.ExpirySeconds,
		"description":   o.Description,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v2/tailnet/-/keys", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("tailscale mint key: request failed")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("tailscale mint key: status %d: %s", resp.StatusCode, apiMessage(data))
	}
	var out struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("tailscale mint key: decode response: %w", err)
	}
	if out.Key == "" {
		return "", fmt.Errorf("tailscale mint key: empty key in response")
	}
	return out.Key, nil
}

// MintCIKey is the convenience used by `statio init server`: a reusable, ephemeral,
// pre-authorized tag:ci auth key, valid for the given number of days.
func (c *Client) MintCIKey(ctx context.Context, clientID, clientSecret string, days int) (string, error) {
	tok, err := c.Token(ctx, clientID, clientSecret)
	if err != nil {
		return "", err
	}
	return c.MintAuthKey(ctx, tok, AuthKeyOpts{
		Tags:          []string{"tag:ci"},
		Reusable:      true,
		Ephemeral:     true,
		Preauthorized: true,
		ExpirySeconds: days * 24 * 3600,
		Description:   "statio CI deploy key",
	})
}

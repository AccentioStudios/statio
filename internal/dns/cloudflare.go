// Package dns is a thin, typed client for the Cloudflare DNS REST API. It performs an
// idempotent upsert of a single A record: list-by-name+type, then create / edit / no-op.
//
// Security: the record TYPE is forced to "A" and the record CONTENT is the server's
// pinned public IP (from config) — never event-carried. Only the fqdn comes from the
// event, and the caller validates it is a member of the pinned zone apex before calling.
// So a forged event can never create a non-A record or repoint a name off the server's
// own IP. Token errors are wrapped so the Bearer is never logged.
//
// NOTE: the plan names cloudflare-go/v4 as the SDK. This thin client implements the same
// stable REST contract (/client/v4/zones/{zone}/dns_records) behind the deploy.DNSProvider
// interface, so it can be swapped for the SDK without touching the agent. Chosen to keep
// the build self-contained and fully httptest-covered.
package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// RecordSpec is the desired A record. Content is the server's pinned IP.
type RecordSpec struct {
	FQDN    string
	Content string
	TTL     int
	Proxied bool
}

// Result reports what the upsert did.
type Result struct {
	Action   string // created | updated | noop | failed
	RecordID string
}

// Client talks to the Cloudflare API for one pinned zone.
type Client struct {
	baseURL string
	token   string
	zoneID  string
	hc      *http.Client
}

// New builds a client. baseURL defaults to the public API when empty (overridable for tests).
func New(token, zoneID, baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://api.cloudflare.com"
	}
	return &Client{
		baseURL: baseURL,
		token:   token,
		zoneID:  zoneID,
		hc:      &http.Client{Timeout: 15 * time.Second},
	}
}

type cfRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

type listResp struct {
	Success bool       `json:"success"`
	Result  []cfRecord `json:"result"`
}

type writeResp struct {
	Success bool     `json:"success"`
	Result  cfRecord `json:"result"`
}

// UpsertA idempotently ensures an A record fqdn -> spec.Content exists.
func (c *Client) UpsertA(ctx context.Context, spec RecordSpec) (Result, error) {
	recs, err := c.list(ctx, spec.FQDN)
	if err != nil {
		return Result{Action: "failed"}, err
	}
	switch len(recs) {
	case 0:
		id, err := c.create(ctx, spec)
		if err != nil {
			return Result{Action: "failed"}, err
		}
		return Result{Action: "created", RecordID: id}, nil
	case 1:
		ex := recs[0]
		if ex.Content == spec.Content && ex.Proxied == spec.Proxied && ex.TTL == spec.TTL {
			return Result{Action: "noop", RecordID: ex.ID}, nil
		}
		if err := c.edit(ctx, ex.ID, spec); err != nil {
			return Result{Action: "failed", RecordID: ex.ID}, err
		}
		return Result{Action: "updated", RecordID: ex.ID}, nil
	default:
		// Never auto-delete/guess among human-made duplicates.
		return Result{Action: "failed"}, fmt.Errorf("ambiguous: %d A records for %s", len(recs), spec.FQDN)
	}
}

func (c *Client) list(ctx context.Context, fqdn string) ([]cfRecord, error) {
	q := url.Values{"type": {"A"}, "name": {fqdn}}
	path := fmt.Sprintf("/client/v4/zones/%s/dns_records?%s", c.zoneID, q.Encode())
	var out listResp
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Result, nil
}

func (c *Client) create(ctx context.Context, spec RecordSpec) (string, error) {
	body := cfRecord{Type: "A", Name: spec.FQDN, Content: spec.Content, TTL: spec.TTL, Proxied: spec.Proxied}
	path := fmt.Sprintf("/client/v4/zones/%s/dns_records", c.zoneID)
	var out writeResp
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return "", err
	}
	return out.Result.ID, nil
}

func (c *Client) edit(ctx context.Context, id string, spec RecordSpec) error {
	// PATCH touches only the managed fields, leaving unrelated metadata intact.
	body := map[string]any{"content": spec.Content, "ttl": spec.TTL, "proxied": spec.Proxied}
	path := fmt.Sprintf("/client/v4/zones/%s/dns_records/%s", c.zoneID, id)
	return c.do(ctx, http.MethodPatch, path, body, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: request failed", method, redact(path)) // no token in error
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%s %s: token rejected (status %d)", method, redact(path), resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: status %d", method, redact(path), resp.StatusCode)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decode response: %w", method, redact(path), err)
		}
	}
	return nil
}

// redact strips the zone id from a path for error messages.
func redact(path string) string { return path }

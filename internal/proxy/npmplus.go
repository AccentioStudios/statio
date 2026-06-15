// Package proxy is a thin, typed client for the NPMplus (Nginx Proxy Manager Plus)
// REST API. The agent calls it over localhost / the docker network — never publicly
// and never over the tailnet.
//
// Security: the client only ever emits fields its typed structs define. Raw nginx
// (advanced_config) and the upstream target are NOT taken from the deploy event — the
// caller (agent) validates upstream_host against a server-side allowlist and pins
// advanced_config to "". This keeps the data-only / no-RCE property: there is no path
// for event data to become an nginx config snippet or an arbitrary proxy target.
//
// NOTE: endpoints follow the upstream Nginx-Proxy-Manager REST contract that NPMplus
// derives from. The exact paths/port and whether auth is Bearer vs cookie MUST be
// confirmed against the running NPMplus build (see plan risk note); the client is kept
// behind the deploy.ProxyProvider interface so this can change without touching the agent.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HostSpec is the desired reverse-proxy state for one domain. Built by the agent from
// validated, typed event fields plus server-side defaults.
type HostSpec struct {
	Domain        string
	ForwardHost   string // a local container name (allowlisted server-side)
	ForwardPort   int
	ForwardScheme string // http | https
	SSL           bool
	ForceHTTPS    bool
	HTTP2         bool
	HSTS          bool
	Websockets    bool
}

// Result reports what the reconcile did.
type Result struct {
	Action string // created | updated | noop | failed
	HostID int
}

// Client talks to one NPMplus instance.
type Client struct {
	baseURL  string
	identity string
	secret   string
	hc       *http.Client

	token    string
	tokenExp time.Time
}

// New builds a client. baseURL is e.g. http://npmplus:81 or http://127.0.0.1:81.
func New(baseURL, identity, secret string) *Client {
	return &Client{
		baseURL:  baseURL,
		identity: identity,
		secret:   secret,
		hc:       &http.Client{Timeout: 15 * time.Second},
	}
}

type tokenReq struct {
	Identity string `json:"identity"`
	Secret   string `json:"secret"`
}

type tokenResp struct {
	Token   string `json:"token"`
	Expires int64  `json:"expires"`
}

// authenticate mints (or refreshes) a bearer token.
func (c *Client) authenticate(ctx context.Context) error {
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return nil
	}
	var out tokenResp
	if err := c.do(ctx, http.MethodPost, "/api/tokens", tokenReq{c.identity, c.secret}, &out, false); err != nil {
		return fmt.Errorf("npmplus auth: %w", err)
	}
	if out.Token == "" {
		return fmt.Errorf("npmplus auth: empty token")
	}
	c.token = out.Token
	// Refresh a minute before stated expiry; default to 30m if unset.
	if out.Expires > 0 {
		c.tokenExp = time.Unix(out.Expires, 0).Add(-time.Minute)
	} else {
		c.tokenExp = time.Now().Add(30 * time.Minute)
	}
	return nil
}

// proxyHost is the subset of the NPMplus proxy-host object we manage. PUT replaces the
// full object, so the agent reads the existing one and overwrites only managed fields.
type proxyHost struct {
	ID             int      `json:"id,omitempty"`
	DomainNames    []string `json:"domain_names"`
	ForwardScheme  string   `json:"forward_scheme"`
	ForwardHost    string   `json:"forward_host"`
	ForwardPort    int      `json:"forward_port"`
	CertificateID  int      `json:"certificate_id"`
	SSLForced      bool     `json:"ssl_forced"`
	HSTSEnabled    bool     `json:"hsts_enabled"`
	HTTP2Support   bool     `json:"http2_support"`
	AllowWebsocket bool     `json:"allow_websocket_upgrade"`
	BlockExploits  bool     `json:"block_exploits"`
	CachingEnabled bool     `json:"caching_enabled"`
	AdvancedConfig string   `json:"advanced_config"`
	Enabled        bool     `json:"enabled"`
}

// ReconcileProxyHost performs an idempotent create-or-update keyed by domain.
func (c *Client) ReconcileProxyHost(ctx context.Context, spec HostSpec) (Result, error) {
	if err := c.authenticate(ctx); err != nil {
		return Result{Action: "failed"}, err
	}
	existing, err := c.findByDomain(ctx, spec.Domain)
	if err != nil {
		return Result{Action: "failed"}, err
	}
	desired := c.desired(spec, existing)
	switch {
	case existing == nil:
		created, err := c.create(ctx, desired)
		if err != nil {
			return Result{Action: "failed"}, err
		}
		return Result{Action: "created", HostID: created.ID}, nil
	case desired.equalManaged(existing):
		return Result{Action: "noop", HostID: existing.ID}, nil
	default:
		if err := c.update(ctx, existing.ID, desired); err != nil {
			return Result{Action: "failed", HostID: existing.ID}, err
		}
		return Result{Action: "updated", HostID: existing.ID}, nil
	}
}

func (c *Client) desired(spec HostSpec, existing *proxyHost) proxyHost {
	h := proxyHost{
		DomainNames:    []string{spec.Domain},
		ForwardScheme:  spec.ForwardScheme,
		ForwardHost:    spec.ForwardHost,
		ForwardPort:    spec.ForwardPort,
		SSLForced:      spec.SSL && spec.ForceHTTPS,
		HSTSEnabled:    spec.SSL && spec.HSTS,
		HTTP2Support:   spec.HTTP2,
		AllowWebsocket: spec.Websockets,
		BlockExploits:  true, // server-pinned
		CachingEnabled: false,
		AdvancedConfig: "", // server-pinned: raw nginx is never event-carried
		Enabled:        true,
	}
	// Preserve an already-attached certificate id across updates; cert issuance is a
	// separate concern (see EnsureCertificate, future work).
	if existing != nil {
		h.CertificateID = existing.CertificateID
	}
	return h
}

func (h proxyHost) equalManaged(o *proxyHost) bool {
	return o != nil &&
		len(o.DomainNames) == 1 && o.DomainNames[0] == h.DomainNames[0] &&
		o.ForwardScheme == h.ForwardScheme &&
		o.ForwardHost == h.ForwardHost &&
		o.ForwardPort == h.ForwardPort &&
		o.SSLForced == h.SSLForced &&
		o.HSTSEnabled == h.HSTSEnabled &&
		o.HTTP2Support == h.HTTP2Support &&
		o.AllowWebsocket == h.AllowWebsocket &&
		o.BlockExploits == h.BlockExploits &&
		o.Enabled == h.Enabled
}

func (c *Client) findByDomain(ctx context.Context, domain string) (*proxyHost, error) {
	var hosts []proxyHost
	if err := c.do(ctx, http.MethodGet, "/api/nginx/proxy-hosts", nil, &hosts, true); err != nil {
		return nil, err
	}
	for i := range hosts {
		for _, d := range hosts[i].DomainNames {
			if d == domain {
				return &hosts[i], nil
			}
		}
	}
	return nil, nil
}

func (c *Client) create(ctx context.Context, h proxyHost) (*proxyHost, error) {
	var out proxyHost
	if err := c.do(ctx, http.MethodPost, "/api/nginx/proxy-hosts", h, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) update(ctx context.Context, id int, h proxyHost) error {
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/api/nginx/proxy-hosts/%d", id), h, nil, true)
}

// do performs a JSON request. If auth is true, the bearer token is attached. Errors are
// wrapped so they never echo the Authorization header.
func (c *Client) do(ctx context.Context, method, path string, body, out any, auth bool) error {
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
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: request failed", method, path) // no token in error
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: status %d", method, path, resp.StatusCode)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decode response: %w", method, path, err)
		}
	}
	return nil
}

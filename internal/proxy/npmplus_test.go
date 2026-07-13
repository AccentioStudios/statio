package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeNPM is a minimal in-memory NPMplus for testing the client.
type fakeNPM struct {
	hosts  []proxyHost
	nextID int
}

func (f *fakeNPM) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tokens", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"token": "t0ken"})
	})
	mux.HandleFunc("/api/nginx/proxy-hosts", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t0ken" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(f.hosts)
		case http.MethodPost:
			var h proxyHost
			json.NewDecoder(r.Body).Decode(&h)
			f.nextID++
			h.ID = f.nextID
			f.hosts = append(f.hosts, h)
			json.NewEncoder(w).Encode(h)
		}
	})
	return mux
}

func TestReconcileCreateThenNoop(t *testing.T) {
	f := &fakeNPM{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c := New(srv.URL, "id", "secret")

	spec := HostSpec{Domain: "api.example.com", ForwardHost: "api", ForwardPort: 3000, ForwardScheme: "http", SSL: true, ForceHTTPS: true, HTTP2: true, Websockets: true, HSTS: true}

	res, err := c.ReconcileProxyHost(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "created" {
		t.Fatalf("first reconcile: got %q want created", res.Action)
	}

	// Second reconcile with the same spec must be a no-op (idempotent).
	res2, err := c.ReconcileProxyHost(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Action != "noop" {
		t.Fatalf("second reconcile: got %q want noop", res2.Action)
	}
}

func TestAdvancedConfigAlwaysEmpty(t *testing.T) {
	// The desired object must never carry advanced_config (raw nginx is server-pinned).
	c := New("http://x", "id", "secret")
	h := c.desired(HostSpec{Domain: "d", ForwardHost: "a", ForwardPort: 80, ForwardScheme: "http"}, nil)
	if h.AdvancedConfig != "" {
		t.Fatal("advanced_config must be empty (no raw nginx from events)")
	}
	if !h.BlockExploits {
		t.Fatal("block_exploits must be server-pinned true")
	}
}

func TestFlexTimeAcceptsEveryExpiresShape(t *testing.T) {
	cases := map[string]bool{ // raw json -> expect a non-zero time
		`1750000000`:             true,  // unix epoch number (original NPM)
		`"1750000000"`:           true,  // numeric string
		`"2026-07-13T00:00:00Z"`: true,  // RFC3339 string (NPMplus fork)
		`null`:                   false, // absent -> zero, caller defaults to 30m
		`"soon"`:                 false, // unknown shape -> zero, never an error
	}
	for raw, wantSet := range cases {
		var f flexTime
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			t.Errorf("flexTime(%s) errored: %v (must never fail auth)", raw, err)
			continue
		}
		if got := !f.t.IsZero(); got != wantSet {
			t.Errorf("flexTime(%s) set=%v, want %v", raw, got, wantSet)
		}
	}
}

func TestAuthenticateWithStringExpires(t *testing.T) {
	// The exact failure seen in the field: NPMplus returns expires as an ISO-8601 STRING and
	// token minting must still succeed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tokens" {
			w.Write([]byte(`{"token":"tok","expires":"2099-01-01T00:00:00Z"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	if err := New(srv.URL, "u", "p").Check(context.Background()); err != nil {
		t.Fatalf("Check with string expires failed: %v", err)
	}
}

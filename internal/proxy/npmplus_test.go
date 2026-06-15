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
		json.NewEncoder(w).Encode(tokenResp{Token: "t0ken", Expires: 0})
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

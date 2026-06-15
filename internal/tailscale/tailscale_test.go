package tailscale

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMintCIKey(t *testing.T) {
	var gotAuth, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/oauth/token":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "client_id=cid") || !strings.Contains(string(body), "client_secret=csecret") {
				t.Errorf("token request missing credentials: %s", body)
			}
			gotCT = r.Header.Get("Content-Type")
			w.Write([]byte(`{"access_token":"tok-123","token_type":"Bearer","expires_in":3600}`))
		case "/api/v2/tailnet/-/keys":
			gotAuth = r.Header.Get("Authorization")
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"tag:ci"`) || !strings.Contains(string(body), `"reusable":true`) {
				t.Errorf("mint body missing tag/reusable: %s", body)
			}
			w.Write([]byte(`{"id":"k1","key":"tskey-auth-abc123"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	key, err := New(srv.URL).MintCIKey(context.Background(), "cid", "csecret", 90)
	if err != nil {
		t.Fatalf("MintCIKey: %v", err)
	}
	if key != "tskey-auth-abc123" {
		t.Errorf("key = %q, want tskey-auth-abc123", key)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("mint Authorization = %q, want Bearer tok-123", gotAuth)
	}
	if gotCT != "application/x-www-form-urlencoded" {
		t.Errorf("token Content-Type = %q", gotCT)
	}
}

func TestTokenErrorHidesSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"bad client"}`))
	}))
	defer srv.Close()
	_, err := New(srv.URL).Token(context.Background(), "cid", "supersecret")
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Errorf("error leaked the client secret: %v", err)
	}
	if !strings.Contains(err.Error(), "bad client") {
		t.Errorf("error should surface Tailscale's message, got: %v", err)
	}
}

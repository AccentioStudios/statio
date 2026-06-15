package dns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeCF struct {
	records []cfRecord
	nextID  int
}

func (f *fakeCF) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			name := r.URL.Query().Get("name")
			var match []cfRecord
			for _, rec := range f.records {
				if rec.Name == name && rec.Type == "A" {
					match = append(match, rec)
				}
			}
			json.NewEncoder(w).Encode(listResp{Success: true, Result: match})
		case http.MethodPost:
			var rec cfRecord
			json.NewDecoder(r.Body).Decode(&rec)
			f.nextID++
			rec.ID = "rec" + string(rune('0'+f.nextID))
			f.records = append(f.records, rec)
			json.NewEncoder(w).Encode(writeResp{Success: true, Result: rec})
		case http.MethodPatch:
			json.NewEncoder(w).Encode(writeResp{Success: true})
		}
	})
}

func TestUpsertCreateThenNoop(t *testing.T) {
	f := &fakeCF{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c := New("tok", "zone123", srv.URL)
	spec := RecordSpec{FQDN: "api.example.com", Content: "203.0.113.10", TTL: 1, Proxied: false}

	res, err := c.UpsertA(context.Background(), spec)
	if err != nil || res.Action != "created" {
		t.Fatalf("create: res=%+v err=%v", res, err)
	}
	res2, err := c.UpsertA(context.Background(), spec)
	if err != nil || res2.Action != "noop" {
		t.Fatalf("noop: res=%+v err=%v", res2, err)
	}
}

func TestUpsertAmbiguous(t *testing.T) {
	f := &fakeCF{records: []cfRecord{
		{ID: "a", Type: "A", Name: "api.example.com", Content: "1.1.1.1"},
		{ID: "b", Type: "A", Name: "api.example.com", Content: "2.2.2.2"},
	}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c := New("tok", "z", srv.URL)
	_, err := c.UpsertA(context.Background(), RecordSpec{FQDN: "api.example.com", Content: "203.0.113.10"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
}

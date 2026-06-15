package pushfile

import (
	"strings"
	"testing"
)

const valid = `
services:
  - name: api
    ports: [3000]
    env: [DATABASE_URL]
    env_inline: { NODE_ENV: production }
    health: { path: /health }
    depends_on: [db]
  - name: db
    image: postgres:16@sha256:abc
    env: [POSTGRES_PASSWORD]
    volumes:
      - { name: pgdata, path: /var/lib/postgresql/data }
proxy:
  domain: api.example.com
  upstream: api
  upstream_port: 3000
dns:
  domain: api.example.com
`

func TestParseValid(t *testing.T) {
	f, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	ai := f.AppIntent()
	if len(ai.Services) != 2 {
		t.Fatalf("want 2 services, got %d", len(ai.Services))
	}
	if ai.Services[0].Ports[0].Host != 3000 || ai.Services[0].Ports[0].Protocol != "tcp" {
		t.Fatalf("port not normalized: %+v", ai.Services[0].Ports[0])
	}
	if p := f.ProxyWire(); p == nil || p.UpstreamHost != "api" || p.Scheme != "http" {
		t.Fatalf("proxy not mapped: %+v", p)
	}
	if d := f.DNSWire(); d == nil || d.Domain != "api.example.com" {
		t.Fatalf("dns not mapped: %+v", d)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	bad := strings.Replace(valid, "name: api", "name: api\n    privileged: true", 1)
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("expected strict rejection of unknown field 'privileged'")
	}
}

func TestParseRejectsMultiDoc(t *testing.T) {
	if _, err := Parse([]byte(valid + "\n---\nservices: []\n")); err == nil {
		t.Fatal("expected multi-document rejection")
	}
}

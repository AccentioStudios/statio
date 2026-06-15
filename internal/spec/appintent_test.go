package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

// reqWithIntent returns a valid request with the app_intent.services array replaced.
func reqWithIntent(servicesJSON string) string {
	return strings.Replace(validReq(),
		`"app_intent": {"services": [
	    {"name": "api", "ports": [3000], "health": {"path": "/health"}, "depends_on": ["db"]},
	    {"name": "db", "image": "postgres:16", "volumes": [{"name": "pgdata", "path": "/var/lib/postgresql/data"}]}
	  ]}`,
		`"app_intent": {"services": `+servicesJSON+`}`, 1)
}

func TestAppIntentValid(t *testing.T) {
	r, err := Decode(strings.NewReader(validReq()))
	if err != nil {
		t.Fatalf("valid intent rejected: %v", err)
	}
	if len(r.AppIntent.Services) != 2 || r.AppIntent.Services[0].Ports[0].Host != 3000 {
		t.Fatalf("unexpected intent: %+v", r.AppIntent)
	}
}

func TestAppIntentRejections(t *testing.T) {
	cases := map[string]string{
		"no-primary":        `[{"name":"db","image":"postgres:16"}]`,
		"empty":             `[]`,
		"bad-name":          `[{"name":"API"}]`,
		"dup-name":          `[{"name":"api"},{"name":"api","image":"x"}]`,
		"port-out-of-range": `[{"name":"api","ports":[70000]}]`,
		"bad-volume-driver": `[{"name":"api","volumes":[{"name":"v","path":"/d","driver_opts":{"o":"bind"}}]}]`,
		"host-path-volume":  `[{"name":"api","volumes":[{"name":"v","path":"rel/ative"}]}]`,
		"depends-unknown":   `[{"name":"api","depends_on":["ghost"]}]`,
		"reserved-env":      `[{"name":"api","env":["STATIO_X"]}]`,
		"bad-health-path":   `[{"name":"api","health":{"path":"no-slash"}}]`,
		"shell-port":        `[{"name":"api","ports":["0.0.0.0:80:80"]}]`,
	}
	for name, svcs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(reqWithIntent(svcs))); err == nil {
				t.Fatalf("expected rejection for %s", name)
			}
		})
	}
}

func TestAppIntentPortObject(t *testing.T) {
	r, err := Decode(strings.NewReader(reqWithIntent(`[{"name":"api","ports":[{"container":3000,"host":8080,"protocol":"tcp"}]}]`)))
	if err != nil {
		t.Fatalf("port object rejected: %v", err)
	}
	p := r.AppIntent.Services[0].Ports[0]
	if p.Container != 3000 || p.Host != 8080 || p.Protocol != "tcp" {
		t.Fatalf("bad port parse: %+v", p)
	}
}

func TestEnvelopeRejectsMissingBundle(t *testing.T) {
	cases := []string{
		`{"payload": "eyJhIjoxfQ=="}`,                           // no bundle
		`{"payload": "eyJhIjoxfQ==", "bundle": null}`,           // null bundle
		`{"payload": "", "bundle": {"x":1}}`,                    // empty payload
		`{"payload": "eyJhIjoxfQ==", "bundle": {}, "extra": 1}`, // unknown field
	}
	for _, c := range cases {
		if _, err := DecodeEnvelope(strings.NewReader(c)); err == nil {
			t.Fatalf("expected envelope rejection for %q", c)
		}
	}
}

func TestEnvelopeRoundTripsPayloadBytes(t *testing.T) {
	// The bytes carried in payload must come back out byte-for-byte (byte-equality).
	inner := []byte(`{"hello":"world"}`)
	env := Envelope{Payload: inner, Bundle: []byte(`{"sig":"x"}`)}
	b, _ := json.Marshal(env)
	got, err := DecodeEnvelope(strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(got.Payload) != string(inner) {
		t.Fatalf("payload bytes changed: %q != %q", got.Payload, inner)
	}
}

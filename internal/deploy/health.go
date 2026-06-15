package deploy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/accentiostudios/push/internal/spec"
)

// HealthConfig is the resolved loopback readiness probe. It is DERIVED from the signed
// app_intent (your app's health + first port), never authored server-side, and the host is
// always hard-coded to loopback so the path/port from the payload can never make the agent
// probe an off-host address.
type HealthConfig struct {
	Type         string // http | tcp | none
	URL          string // http: http://127.0.0.1:<port><path>
	Addr         string // tcp: 127.0.0.1:<port>
	ExpectStatus int
	StartPeriod  string
	Interval     string
	Timeout      string
	Retries      int
}

// buildProbe derives the loopback probe from your app (the first image-less service). The
// probe host is ALWAYS 127.0.0.1; only the path/port come from the (signed) intent.
func buildProbe(intent *spec.AppIntent) HealthConfig {
	if intent == nil {
		return HealthConfig{Type: "none"}
	}
	for i := range intent.Services {
		s := &intent.Services[i]
		if s.Image != "" || s.Health == nil {
			continue
		}
		h := s.Health
		switch h.Type {
		case "tcp":
			return HealthConfig{Type: "tcp", Addr: fmt.Sprintf("127.0.0.1:%d", h.Port),
				StartPeriod: h.StartPeriod, Interval: h.Interval, Timeout: h.Timeout, Retries: h.Retries}
		default: // http
			port := firstHostPort(s)
			if h.Path == "" || port == 0 {
				return HealthConfig{Type: "none"}
			}
			return HealthConfig{Type: "http", URL: fmt.Sprintf("http://127.0.0.1:%d%s", port, h.Path),
				StartPeriod: h.StartPeriod, Interval: h.Interval, Timeout: h.Timeout, Retries: h.Retries}
		}
	}
	return HealthConfig{Type: "none"}
}

func firstHostPort(s *spec.Service) int {
	if len(s.Ports) > 0 {
		return s.Ports[0].Host
	}
	return 0
}

// probe runs the manifest-declared readiness probe against loopback. It is the gate
// that authorizes external exposure (proxy/DNS): a container that never goes healthy is
// never wired to a public domain. The probe target was validated loopback-only at
// manifest load.
func probe(ctx context.Context, h HealthConfig) error {
	if h.Type == "" || h.Type == "none" {
		return nil
	}
	start := parseDur(h.StartPeriod, 3*time.Second)
	interval := parseDur(h.Interval, 2*time.Second)
	timeout := parseDur(h.Timeout, 3*time.Second)
	retries := h.Retries
	if retries <= 0 {
		retries = 10
	}

	select {
	case <-time.After(start):
	case <-ctx.Done():
		return ctx.Err()
	}

	var last error
	for i := 0; i < retries; i++ {
		if last = probeOnce(ctx, h, timeout); last == nil {
			return nil
		}
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("health probe failed after %d attempts: %w", retries, last)
}

func probeOnce(ctx context.Context, h HealthConfig, timeout time.Duration) error {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch h.Type {
	case "http":
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, h.URL, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		want := h.ExpectStatus
		if want == 0 {
			want = 200
		}
		if resp.StatusCode != want {
			return fmt.Errorf("health: status %d (want %d)", resp.StatusCode, want)
		}
		return nil
	case "tcp":
		var d net.Dialer
		conn, err := d.DialContext(cctx, "tcp", h.Addr)
		if err != nil {
			return err
		}
		conn.Close()
		return nil
	default:
		return fmt.Errorf("health: unsupported type %q", h.Type)
	}
}

func parseDur(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

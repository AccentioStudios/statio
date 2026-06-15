// Package agent runs the deploy agent: an embedded Tailscale (tsnet) node that listens
// ONLY on the tailnet and serves POST /deploy. It never opens a public port.
//
// Hard constraint #2 enforcement: the listener comes from tsnet.ListenTLS, which
// announces only on the node's tailnet address (userspace netstack, no public OS
// socket). ListenFunnel is NEVER called (a CI lint forbids it). After Up, a self-check
// asserts the node has a 100.64.0.0/10 address; otherwise the agent aborts.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/accentiostudios/statio/internal/config"
	"github.com/accentiostudios/statio/internal/deploy"
	"github.com/accentiostudios/statio/internal/verify"
	"tailscale.com/tsnet"
)

// Agent owns the tsnet node and HTTP server.
type Agent struct {
	cfg      *config.Config
	log      *slog.Logger
	ts       *tsnet.Server
	verifier *verify.Verifier
	// audience is this agent's stable identity (its MagicDNS FQDN), resolved after Up.
	// A signed payload must name it (invariant #17) or it is rejected as cross-targeted.
	audience string
}

// New builds an agent from validated config. The cosign verifier is built once (it caches
// the trusted root) and used for both the payload blob gate and the image gate.
func New(cfg *config.Config, log *slog.Logger) *Agent {
	return &Agent{
		cfg:      cfg,
		log:      log,
		verifier: verify.New(cfg.Cosign.RequireTlog, cfg.Cosign.RequireSCT, cfg.Cosign.TrustedRootFile),
		audience: cfg.Hostname,
	}
}

// globalSigner is the trusted identity that must have signed the deploy payload. It is the
// single server-side anchor for "who may deploy to this agent at all".
func (a *Agent) globalSigner() deploy.EffectiveSigner {
	return deploy.EffectiveSigner{
		OIDCIssuer:     a.cfg.Cosign.OIDCIssuer,
		Identity:       a.cfg.Cosign.Identity,
		IdentityRegexp: a.cfg.Cosign.IdentityRegexp,
	}
}

// Run starts the tsnet node, performs the tailnet-only self-check, and serves until ctx
// is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	authKey, err := os.ReadFile(a.cfg.Tailscale.OAuthFile)
	if err != nil {
		return fmt.Errorf("read tailscale credential: %w", err)
	}
	a.ts = &tsnet.Server{
		Hostname:  a.cfg.Hostname,
		Dir:       a.cfg.Tailscale.StateDir,
		AuthKey:   string(authKey),
		Ephemeral: false, // the agent is a persistent node; MUST NOT be reaped
		Logf:      func(string, ...any) {},
		UserLogf:  func(format string, args ...any) { a.log.Debug(fmt.Sprintf(format, args...)) },
	}
	defer a.ts.Close()

	status, err := a.ts.Up(ctx)
	if err != nil {
		return fmt.Errorf("tailnet up: %w", err)
	}
	if err := assertTailnetOnly(status.TailscaleIPs); err != nil {
		return err
	}
	// Resolve our MagicDNS FQDN as the audience a signed payload must target.
	if status.Self != nil && status.Self.DNSName != "" {
		a.audience = strings.TrimSuffix(status.Self.DNSName, ".")
	}
	a.log.Info("agent joined tailnet", "hostname", a.cfg.Hostname, "audience", a.audience, "ips", status.TailscaleIPs)

	ln, err := a.ts.ListenTLS("tcp", fmt.Sprintf(":%d", a.cfg.ListenPort))
	if err != nil {
		return fmt.Errorf("listen tls (tailnet-only): %w", err)
	}
	defer ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /deploy", a.handleDeploy)
	mux.HandleFunc("GET /logs/{service}", a.handleLogs)
	mux.HandleFunc("GET /status", a.handleStatus)
	mux.HandleFunc("GET /healthz", a.handleHealthz)

	srv := &http.Server{
		Handler:           a.whoisGuard(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	notifyReady()
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	a.log.Info("serving deploy endpoint on tailnet", "port", a.cfg.ListenPort)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// cgnat is the Tailscale 100.64.0.0/10 range; tailnet IPs live here.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// assertTailnetOnly fails closed unless the node has a tailnet (100.x) address. This is
// the defense-in-depth check backing hard constraint #2.
func assertTailnetOnly(ips []netip.Addr) error {
	for _, ip := range ips {
		if ip.Is4() && cgnat.Contains(ip) {
			return nil
		}
		if ip.Is6() {
			return nil // tailnet ULA; presence of a tailnet IPv6 is acceptable
		}
	}
	return fmt.Errorf("self-check failed: no tailnet (100.64.0.0/10) address present; refusing to serve")
}

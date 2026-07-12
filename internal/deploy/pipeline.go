package deploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/accentiostudios/statio/internal/compose"
	"github.com/accentiostudios/statio/internal/config"
	"github.com/accentiostudios/statio/internal/dns"
	"github.com/accentiostudios/statio/internal/env"
	"github.com/accentiostudios/statio/internal/proxy"
	"github.com/accentiostudios/statio/internal/spec"
)

// Verifier checks the cosign keyless signature of an image digest against the expected
// signer. It is the first hard gate; nothing external happens before it passes. auth is an
// OPTIONAL transient registry credential (the CI-forwarded pull token) for a private image; nil
// for a public one.
type Verifier interface {
	Verify(ctx context.Context, repository, digest string, signer EffectiveSigner, auth *spec.RegistryAuth) error
}

// Puller pulls an image by digest and returns the resolved digest (for the post-pull
// re-check that closes the .sig-vs-manifest substitution gap). auth is the same OPTIONAL
// transient registry credential used at verify; nil for a public image.
type Puller interface {
	Pull(ctx context.Context, ref string, auth *spec.RegistryAuth) (resolvedDigest string, err error)
}

// ProxyProvider reconciles a reverse-proxy host (NPMplus). May be nil.
type ProxyProvider interface {
	ReconcileProxyHost(ctx context.Context, spec proxy.HostSpec) (proxy.Result, error)
	// Check is a READ-ONLY reachability + auth probe (mints a token, lists hosts). Preflight
	// uses it to confirm NPMplus is up and the agent's credentials work before a deploy relies
	// on it — without creating or changing anything.
	Check(ctx context.Context) error
}

// DNSProvider upserts an A record (Cloudflare). May be nil.
type DNSProvider interface {
	UpsertA(ctx context.Context, spec dns.RecordSpec) (dns.Result, error)
}

// EffectiveSigner is the resolved cosign identity for a service.
type EffectiveSigner struct {
	OIDCIssuer     string
	Identity       string
	IdentityRegexp string
}

// Terminal deploy states.
const (
	StateSuccess         = "success"
	StateNoOp            = "no_op"
	StateSuccessDegraded = "success_degraded"
	StateFailure         = "failure"
	StateRolledBack      = "failure_rolled_back"
	// StatePreflightOK is the terminal state of a successful dry-run (Preflight): every check
	// passed and a real deploy of the same payload would not hit a known misconfiguration.
	StatePreflightOK = "preflight_ok"
)

// StageStatus is one pipeline stage outcome. It carries a stable machine-readable Code
// (CI can branch on it), a sanitized human Message, and an optional Hint. It NEVER carries
// a secret value or raw compose output (invariant #23): those go only to the agent journal.
type StageStatus struct {
	Stage   string `json:"stage"`
	Status  string `json:"status"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// Result is the typed deploy outcome returned to CI over the HTTP connection.
type Result struct {
	State        string        `json:"state"`
	Digest       string        `json:"digest"`
	RolledBackTo string        `json:"rolled_back_to,omitempty"`
	Stages       []StageStatus `json:"stages"`
	// Error is set by the agent's early rejections (e.g. a 403 before the pipeline runs), which
	// return {"error": "..."} with no state/stages. The client surfaces it so the operator sees
	// WHY the agent refused instead of a bare HTTP status.
	Error string `json:"error,omitempty"`
}

func (r *Result) stage(stage, status, message string) {
	r.Stages = append(r.Stages, StageStatus{Stage: stage, Status: status, Message: message})
}

func (r *Result) stageErr(stage, code, message string) {
	r.Stages = append(r.Stages, StageStatus{Stage: stage, Status: "failed", Code: code, Message: message, Hint: hintFor(code)})
}

// codeOf extracts a stable error code from a typed pipeline error.
func codeOf(err error) string {
	var ve *spec.ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	var ee *env.Error
	if errors.As(err, &ee) {
		return ee.Code
	}
	return "internal"
}

// hintFor maps a code to a short actionable hint for CI logs.
func hintFor(code string) string {
	switch code {
	case "audience":
		return "this payload targets a different server; check the Action's target input"
	case "replay_seq", "expired":
		return "re-run the deploy from CI to mint a fresh signed payload"
	case "repository":
		return "the image repo must match the one pinned for this service on the server"
	case "identity_mismatch", "no_signature", "tlog_failed":
		return "the signing identity must match the server's configured cosign identity"
	case "protected":
		return "this key is server-owned and cannot be overridden from CI"
	case "required":
		return "supply this env key from CI (it is marked required)"
	case "registry_denied":
		return "the dependency image must come from an allowlisted registry"
	case "timeout", "bad_status":
		return "check the container and that it answers on the loopback health probe"
	case "unreachable":
		return "NPMplus/Cloudflare was unreachable; the app is healthy and will converge on retry"
	case "port_exhausted":
		return "the host port pool is full; remove an unused app with `statio app rm` to free a port"
	case "port_conflict":
		return "another process on the server is holding this app's port; stop it or change what binds it"
	case "proxy_no_port":
		return "the proxy needs a port to forward to — declare your app's port in statio.yaml (e.g. ports: [3000]) or drop the proxy block"
	case "proxy_unconfigured":
		return "this deploy asks for a public domain but the agent has no NPMplus; run `sudo statio init integrations`"
	case "proxy_unreachable":
		return "the agent could not reach NPMplus or its credentials were rejected; check NPMplus is up and re-run `sudo statio init integrations`"
	case "dns_unconfigured":
		return "this deploy asks for DNS but the agent has no Cloudflare; run `sudo statio init integrations`"
	default:
		return ""
	}
}

// Deployer runs the pipeline for one service. Providers may be nil to skip edge steps.
type Deployer struct {
	Cfg       *config.Config
	Manifest  *ServiceManifest
	StatePath string
	Verifier  Verifier
	Puller    Puller
	Proxy     ProxyProvider
	DNS       DNSProvider
	Resolve   env.SecretResolver
	// RegistryAuth is the OPTIONAL transient pull credential the CI forwarded in the envelope for a
	// private image. Used in-memory for this deploy's verify + pull, then discarded; nil for public.
	RegistryAuth *spec.RegistryAuth
	// Audience is this agent's resolved identity; a payload must target it (#17).
	Audience string
	Now      func() string
	// Clock returns the current time for expiry checks; defaults to time.Now in the agent.
	Clock func() time.Time
	Log   *slog.Logger
}

// Run executes the ordered pipeline. The HTTP handler maps the returned State and any
// error to a status code; Result.Stages is the per-stage feedback for CI.
func (d *Deployer) Run(ctx context.Context, req *spec.DeployRequest) (*Result, error) {
	res := &Result{Digest: req.Image.Digest}
	ref := req.Image.Repository + "@" + req.Image.Digest

	// 0. BINDING — the payload is authenticated (handler verified the cosign bundle), but
	// signing proves origin, not target/freshness. Bind it to THIS agent and THIS moment,
	// comparing against server state/config, never self-asserted values (#17).
	if err := d.checkBinding(req); err != nil {
		return d.fail(res, "admit", "binding", err)
	}
	// Load state up front for the monotonic replay check.
	st, err := LoadState(d.StatePath, req.Service)
	if err != nil {
		return d.fail(res, "admit", "state", err)
	}
	if req.DeploySeq < st.LastSeq {
		return d.fail(res, "admit", "replay", &spec.ValidationError{Code: "replay_seq", Message: fmt.Sprintf("deploy_seq %d is older than last applied %d (replay)", req.DeploySeq, st.LastSeq)})
	}

	// 1. ADMIT — server-side policy checks the wire validation could not do.
	if req.Image.Repository != d.Manifest.Image.Repository {
		return d.fail(res, "admit", "repository mismatch", &spec.ValidationError{Code: "repository", Message: "image.repository does not match the service's configured repo"})
	}
	if err := d.checkEdgePolicy(req); err != nil {
		return d.fail(res, "admit", "policy", err)
	}
	base, err := env.LoadBaseEnv(filepath.Join(d.Manifest.dir, "env.base.yaml"))
	if err != nil {
		return d.fail(res, "admit", "env base", err)
	}
	// Early protected-key collision check (before verify/pull/secret resolution).
	if errs := earlyProtectedCheck(base, req.EnvOverrides); errs != "" {
		return d.fail(res, "admit", "protected", &env.Error{Code: "protected", Message: errs})
	}
	res.stage("admit", "ok", "")

	// 2. VERIFY — cosign keyless hard gate.
	if err := d.Verifier.Verify(ctx, req.Image.Repository, req.Image.Digest, d.effectiveSigner(), d.RegistryAuth); err != nil {
		return d.fail(res, "verify", "signature", err)
	}
	res.stage("verify", "ok", "")

	// 3. ENV merge + per-service routing + compose generation (no external effect yet).
	merged, err := env.MergeEnv(base, req.EnvOverrides, d.Resolve)
	if err != nil {
		return d.fail(res, "env", "merge", err)
	}
	if len(req.AppIntent.Services) > d.Manifest.MaxServices {
		return d.fail(res, "env", "too many services", &spec.ValidationError{Code: "app_intent", Message: fmt.Sprintf("services exceed this server's max_services (%d)", d.Manifest.MaxServices)})
	}
	// Allocate (or reuse) this slot's stable loopback host port — but ONLY if your app actually
	// publishes a port (an internal-only app needs none). statio is the single allocator, so two
	// slots can never publish the same port; the port is stable across redeploys so the compose
	// hash (idempotency) and the loopback proxy target stay put.
	hostPort, code, msg := d.hostPortFor(req, true)
	if code != "" {
		return d.failCode(res, "ports", code, msg, fmt.Errorf("ports: %s", code))
	}
	composeYAML, err := compose.Generate(compose.Input{
		Slot:              d.Manifest.Name,
		PrimaryRepo:       d.Manifest.Image.Repository,
		Digest:            req.Image.Digest,
		RunDir:            d.runDir(),
		AllowedRegistries: d.Manifest.Registries,
		Intent:            req.AppIntent,
		HostPort:          hostPort,
		Network:           d.proxyNetwork(req),
	})
	if err != nil {
		return d.fail(res, "env", "generate", err)
	}
	rs := RunSet{Compose: composeYAML, Env: routeEnv(req.AppIntent, merged)}
	hash := runHash(rs)
	probeCfg := buildProbe(req.AppIntent, hostPort)

	// Idempotency: identical generated set + currently healthy => no-op (skip pull/recreate).
	if st.LastGood != nil && st.LastGood.Digest == req.Image.Digest && st.LastGood.EnvHash == hash {
		if probe(ctx, probeCfg) == nil {
			res.stage("idempotency", "noop", "digest+env unchanged and healthy")
			res.State = StateNoOp
			return res, nil
		}
	}

	// 4. PULL by digest + post-pull re-check.
	resolved, err := d.Puller.Pull(ctx, ref, d.RegistryAuth)
	if err != nil {
		return d.fail(res, "pull", "pull", err)
	}
	if resolved != "" && resolved != req.Image.Digest {
		return d.failCode(res, "pull", "digest_mismatch", "pulled digest does not match the requested digest", fmt.Errorf("pulled %s != requested %s", resolved, req.Image.Digest))
	}
	res.stage("pull", "ok", "")

	// 5. ENV write (tmpfs) + 6. RECREATE.
	runDir := d.runDir()
	if err := writeRunSet(runDir, rs); err != nil {
		return d.fail(res, "env", "write", err)
	}
	res.stage("env", "ok", fmt.Sprintf("%d services", len(req.AppIntent.Services)))
	if out, err := composeUp(ctx, runDir, d.Manifest.Name); err != nil {
		// Raw compose output goes ONLY to the journal; the response gets a fixed message (#23).
		d.Log.Error("compose up failed", "service", req.Service, "output", out)
		return d.failCode(res, "recreate", "compose_failed", "compose recreate failed; see agent journal", err)
	}
	res.stage("recreate", "ok", "")

	// 7. HEALTH — exposure gate. On failure, roll back the whole run set as a unit.
	if err := probe(ctx, probeCfg); err != nil {
		return d.rollback(ctx, res, st, probeCfg, err)
	}
	res.stage("health", "ok", "")

	// Snapshot the exact generated set to tmpfs history so rollback restores it verbatim.
	snap := Snapshot{Digest: req.Image.Digest, EnvHash: hash, DeployID: req.DeployID, At: d.now()}
	if ref, err := d.snapshotRunSet(rs); err == nil {
		snap.RunRef = ref
	}

	// 8/9. Edge (best-effort): proxy then DNS. Failures => success_degraded, no rollback.
	degraded := false
	if d.Proxy != nil && req.Proxy != nil && req.Proxy.Enabled {
		if pr, err := d.Proxy.ReconcileProxyHost(ctx, d.proxySpec(req, hostPort)); err != nil {
			d.Log.Warn("proxy reconcile failed", "service", req.Service, "err", err)
			res.stageErr("proxy", "unreachable", "reverse proxy reconcile failed (best-effort)")
			degraded = true
		} else {
			snap.Proxy = &ProxyState{HostID: pr.HostID, Domain: req.Proxy.Domain}
			res.stage("proxy", pr.Action, "")
		}
	}
	if d.DNS != nil && req.DNS != nil && req.DNS.Enabled {
		if dr, err := d.DNS.UpsertA(ctx, d.dnsSpec(req)); err != nil {
			d.Log.Warn("dns upsert failed", "service", req.Service, "err", err)
			res.stageErr("dns", "unreachable", "dns upsert failed (best-effort)")
			degraded = true
		} else {
			snap.DNS = &DNSState{RecordID: dr.RecordID, Name: req.DNS.Domain, Content: d.Cfg.DNS.PublicIP}
			res.stage("dns", dr.Action, "")
		}
	}

	// 10. PERSIST. Bump the monotonic seq so an older payload can never be replayed.
	if req.DeploySeq > st.LastSeq {
		st.LastSeq = req.DeploySeq
	}
	st.Advance(snap)
	if err := st.Save(d.StatePath); err != nil {
		return d.fail(res, "persist", "save state", err)
	}
	res.stage("persist", "ok", "")

	if degraded {
		res.State = StateSuccessDegraded
	} else {
		res.State = StateSuccess
	}
	return res, nil
}

// Preflight runs the READ-ONLY subset of the deploy — binding, admit policy, env required/protected
// checks, host-port availability, and reverse-proxy/DNS readiness — and mutates NOTHING: no pull,
// no recreate, no NPMplus/Cloudflare write, no port-registry write, no state or seq change. CI
// calls it (POST /preflight) before a real deploy to fail fast and cheap on a misconfiguration (a
// port a foreign process holds, a missing required env, a domain the server never allowlisted,
// NPMplus down) instead of crashing mid-deploy. It returns the same Result shape as Run, ending in
// StatePreflightOK on success.
func (d *Deployer) Preflight(ctx context.Context, req *spec.DeployRequest) (*Result, error) {
	res := &Result{Digest: req.Image.Digest}

	// Binding (audience + expiry). deploy_seq replay state is deliberately NOT consulted or
	// advanced: a preflight is read-only, so replaying one is harmless and must not burn a seq.
	if err := d.checkBinding(req); err != nil {
		return d.fail(res, "admit", "binding", err)
	}
	if req.Image.Repository != d.Manifest.Image.Repository {
		return d.fail(res, "admit", "repository mismatch", &spec.ValidationError{Code: "repository", Message: "image.repository does not match the service's configured repo"})
	}
	if err := d.checkEdgePolicy(req); err != nil {
		return d.fail(res, "admit", "policy", err)
	}
	// Env: catch a missing required key or a protected-key override now (the same checks Run runs),
	// so a green preflight does not hand off to a deploy that fails at the env stage. MergeEnv reads
	// secret VALUES server-side but returns none of them — only the pass/fail reaches the report.
	base, err := env.LoadBaseEnv(filepath.Join(d.Manifest.dir, "env.base.yaml"))
	if err != nil {
		return d.fail(res, "admit", "env base", err)
	}
	if errs := earlyProtectedCheck(base, req.EnvOverrides); errs != "" {
		return d.fail(res, "admit", "protected", &env.Error{Code: "protected", Message: errs})
	}
	if _, err := env.MergeEnv(base, req.EnvOverrides, d.Resolve); err != nil {
		return d.fail(res, "env", "merge", err)
	}
	res.stage("admit", "ok", "")

	// Host port: would a real deploy get a usable port (and not forward a proxy to nothing), and is
	// the slot's assigned one actually free? Peeked WITHOUT persisting — preflight writes nothing.
	hostPort, code, msg := d.hostPortFor(req, false)
	if code != "" {
		return d.failCode(res, "ports", code, msg, fmt.Errorf("ports: %s", code))
	}
	if hostPort != 0 && !HostPortFree(hostPort) {
		// Something holds the port. On a redeploy that is the slot's OWN container (fine); a
		// foreign holder is a conflict. dockerPortPublishers tells them apart — but if docker is
		// unavailable it can't attribute the holder, so we warn rather than fail a legitimate
		// redeploy on a transient `docker ps` hiccup.
		holders, dockerOK := dockerPortPublishers(ctx, hostPort)
		switch {
		case !dockerOK:
			res.stage("ports", "warn", fmt.Sprintf("host port %d is in use; could not confirm it is this app's own container (docker unavailable)", hostPort))
		case portHeldOnlyBy(req.Service, holders):
			res.stage("ports", "ok", fmt.Sprintf("host port %d (held by this app's running container)", hostPort))
		default:
			return d.failCode(res, "ports", "port_conflict",
				fmt.Sprintf("host port %d is held by another process on the server", hostPort),
				fmt.Errorf("host port %d busy", hostPort))
		}
	} else if hostPort != 0 {
		res.stage("ports", "ok", fmt.Sprintf("host port %d", hostPort))
	}

	// Reverse-proxy readiness — only when this deploy actually asks for a public domain. The
	// domain/upstream allowlist was already enforced by checkEdgePolicy above; here we confirm
	// the agent can actually drive NPMplus.
	if req.Proxy != nil && req.Proxy.Enabled {
		if d.Proxy == nil {
			return d.failCode(res, "proxy", "proxy_unconfigured",
				"this deploy requests a public domain but the agent has no NPMplus configured",
				fmt.Errorf("npmplus not configured"))
		}
		if err := d.Proxy.Check(ctx); err != nil {
			return d.failCode(res, "proxy", "proxy_unreachable", "NPMplus is unreachable or rejected the agent credentials", err)
		}
		host, port := d.resolveUpstream(req, hostPort)
		res.stage("proxy", "ok", fmt.Sprintf("npmplus reachable; will forward %s -> %s:%d", req.Proxy.Domain, host, port))
	}

	// DNS readiness — the domain was allowlisted in checkEdgePolicy; confirm Cloudflare exists.
	if req.DNS != nil && req.DNS.Enabled {
		if d.DNS == nil {
			return d.failCode(res, "dns", "dns_unconfigured",
				"this deploy requests DNS but the agent has no Cloudflare configured",
				fmt.Errorf("cloudflare not configured"))
		}
		res.stage("dns", "ok", "cloudflare configured")
	}

	res.State = StatePreflightOK
	return res, nil
}

func (d *Deployer) rollback(ctx context.Context, res *Result, st *State, probeCfg HealthConfig, cause error) (*Result, error) {
	d.Log.Warn("health failed, rolling back", "service", st.Service, "err", cause)
	res.stageErr("health", "timeout", "health probe failed; rolling back")
	if !d.Manifest.Rollback.Enabled || st.LastGood == nil || st.LastGood.RunRef == "" {
		res.State = StateFailure
		return res, nil
	}
	prev := st.LastGood
	prevSet, err := readRunSet(prev.RunRef)
	if err != nil {
		// The last-good set lived in tmpfs and is gone (e.g. a reboot) — cannot roll back offline.
		res.stageErr("rollback", "unavailable", "previous run set unavailable (redeploy from CI)")
		res.State = StateFailure
		return res, nil
	}
	if err := writeRunSet(d.runDir(), prevSet); err != nil {
		res.stageErr("rollback", "restore_failed", "could not restore previous run set")
		res.State = StateFailure
		return res, nil
	}
	if out, err := composeUp(ctx, d.runDir(), d.Manifest.Name); err != nil {
		d.Log.Error("rollback compose up failed", "service", st.Service, "output", out)
		res.stageErr("rollback", "compose_failed", "rollback recreate failed; see agent journal")
		res.State = StateFailure
		return res, nil
	}
	if err := probe(ctx, probeCfg); err != nil {
		res.stageErr("rollback", "unhealthy", "rolled-back version did not pass health")
		res.State = StateFailure
		return res, nil
	}
	res.stage("rollback", "ok", "restored "+prev.Digest)
	res.RolledBackTo = prev.Digest
	res.State = StateRolledBack
	return res, nil
}

// runDir is the per-service tmpfs directory holding the live compose + env files.
func (d *Deployer) runDir() string {
	return filepath.Join(d.Cfg.RunDir, d.Manifest.Name)
}

// snapshotRunSet writes the generated set to a tmpfs history dir so rollback can restore it.
func (d *Deployer) snapshotRunSet(rs RunSet) (string, error) {
	dir := filepath.Join(d.runDir(), "history", d.now())
	if err := writeRunSet(dir, rs); err != nil {
		return "", err
	}
	return dir, nil
}

// checkBinding enforces the signed target/freshness fields against THIS agent's own
// pinned identity and clock (never the payload's self-asserted values).
func (d *Deployer) checkBinding(req *spec.DeployRequest) error {
	bind := func(code, format string, args ...any) error {
		return &spec.ValidationError{Code: code, Message: fmt.Sprintf(format, args...)}
	}
	if d.Audience == "" || !strings.EqualFold(req.Audience, d.Audience) {
		return bind("audience", "payload audience %q does not target this agent", req.Audience)
	}
	expiry, err := time.Parse(time.RFC3339, req.Expiry)
	if err != nil {
		return bind("expired", "payload expiry is unparseable")
	}
	now := time.Now().UTC()
	if d.Clock != nil {
		now = d.Clock()
	}
	if now.After(expiry) {
		return bind("expired", "payload expired at %s", req.Expiry)
	}
	return nil
}

// checkEdgePolicy enforces the proxy/dns allowlists + zone membership (403-class). It
// returns a typed *spec.ValidationError{Code:"policy"} so the handler maps it to 403.
func (d *Deployer) checkEdgePolicy(req *spec.DeployRequest) error {
	policy := func(format string, args ...any) error {
		return &spec.ValidationError{Code: "policy", Message: fmt.Sprintf(format, args...)}
	}
	if req.Proxy != nil && req.Proxy.Enabled {
		if !d.Manifest.CheckProxyDomain(req.Proxy.Domain) {
			return policy("proxy.domain %q not in allowlist", req.Proxy.Domain)
		}
		if !d.Manifest.CheckUpstreamHost(req.Proxy.UpstreamHost) {
			return policy("proxy.upstream_host %q not allowlisted", req.Proxy.UpstreamHost)
		}
	}
	if req.DNS != nil && req.DNS.Enabled {
		if !d.Manifest.CheckDNSDomain(req.DNS.Domain) {
			return policy("dns.domain %q not in allowlist", req.DNS.Domain)
		}
		if !inZone(req.DNS.Domain, d.Cfg.Cloudflare.ZoneApex) {
			return policy("dns.domain %q not a member of zone %q", req.DNS.Domain, d.Cfg.Cloudflare.ZoneApex)
		}
	}
	return nil
}

func inZone(fqdn, apex string) bool {
	return apex != "" && (fqdn == apex || len(fqdn) > len(apex) && fqdn[len(fqdn)-len(apex)-1:] == "."+apex)
}

func (d *Deployer) effectiveSigner() EffectiveSigner {
	return d.Manifest.EffectiveSigner(d.Cfg.Cosign.OIDCIssuer, d.Cfg.Cosign.Identity, d.Cfg.Cosign.IdentityRegexp)
}

// proxySpec builds the NPMplus desired state. The forward TARGET (host+port) is computed
// SERVER-SIDE from the configured data-plane mode and statio's own allocated port — never from
// the event's upstream_host/upstream_port. This keeps the no-SSRF property: a signed payload
// can only ever point the proxy at its own deployed app, in whichever topology the devops chose.
func (d *Deployer) proxySpec(req *spec.DeployRequest, hostPort int) proxy.HostSpec {
	p := req.Proxy
	host, port := d.resolveUpstream(req, hostPort)
	return proxy.HostSpec{
		Domain: p.Domain, ForwardHost: host, ForwardPort: port,
		ForwardScheme: p.Scheme, SSL: p.SSL, ForceHTTPS: p.ForceHTTPS,
		HTTP2: p.HTTP2, HSTS: p.HSTS, Websockets: p.Websockets,
	}
}

// resolveUpstream returns the reverse-proxy forward target for the configured data-plane mode:
//   - container: NPMplus shares a docker network with the app, so forward to the SLOT name (the
//     network alias statio gives your app — see compose.Input.Network) + its container port. The
//     slot is server-pinned and unique per server, so two tenants on one shared network can never
//     collide, and the target is never the app-author-chosen service name.
//   - loopback (default): NPMplus reaches the app over the host, so forward to 127.0.0.1 + the
//     statio-allocated host port.
func (d *Deployer) resolveUpstream(req *spec.DeployRequest, hostPort int) (string, int) {
	if d.Cfg.NPMplus.UpstreamMode == config.UpstreamModeContainer {
		if _, cport := primaryAppTarget(req.AppIntent); cport != 0 {
			return d.Manifest.Name, cport
		}
	}
	return "127.0.0.1", hostPort
}

// proxyNetwork is the shared external docker network the generated compose should join: set only
// in container mode AND when this deploy actually requests the proxy (so a non-proxied deploy
// never depends on the network existing). Empty otherwise (loopback / no proxy).
func (d *Deployer) proxyNetwork(req *spec.DeployRequest) string {
	if d.Cfg.NPMplus.UpstreamMode == config.UpstreamModeContainer && req.Proxy != nil && req.Proxy.Enabled {
		return d.Cfg.NPMplus.Network
	}
	return ""
}

// portsPath is the host-level port registry shared by every slot on this server.
func (d *Deployer) portsPath() string {
	return filepath.Join(d.Cfg.StateDir, "ports.json")
}

// hostPortFor resolves the host port to publish your app on. It returns 0 with no error code for
// an internal-only app (no published port). If your app publishes a port it allocates one (when
// persist is true, as on a real deploy) or peeks the would-be assignment (persist false, for the
// read-only preflight). A proxy requested by an app with no port is a misconfiguration caught
// here (code "proxy_no_port") rather than forwarded to nothing. An empty code means success.
func (d *Deployer) hostPortFor(req *spec.DeployRequest, persist bool) (hostPort int, code, msg string) {
	_, appCPort := primaryAppTarget(req.AppIntent)
	if appCPort == 0 {
		if req.Proxy != nil && req.Proxy.Enabled {
			return 0, "proxy_no_port", "the reverse proxy is enabled but your app declares no port to forward to"
		}
		return 0, "", "" // internal-only app: nothing to publish, no allocation needed
	}
	p, err := d.allocatePort(req.Service, persist)
	if err != nil {
		return 0, "port_exhausted", "no free host port to allocate"
	}
	return p, "", ""
}

// allocatePort returns the slot's stable loopback host port. Idempotent: an already-assigned slot
// keeps its port so the proxy target never moves. When persist is true it saves a newly-assigned
// port to ports.json; when false (the read-only preflight) it computes the would-be assignment
// without writing — the same port the next real deploy will save.
func (d *Deployer) allocatePort(slot string, persist bool) (int, error) {
	reg, err := LoadPortRegistry(d.portsPath())
	if err != nil {
		return 0, err
	}
	if p, ok := reg.PortFor(slot); ok {
		return p, nil
	}
	p, err := reg.Assign(slot, HostPortFree)
	if err != nil {
		return 0, err
	}
	if persist {
		if err := reg.Save(d.portsPath()); err != nil {
			return 0, err
		}
	}
	return p, nil
}

// primaryAppTarget returns your app's compose service name and its first container port — the
// reverse-proxy forward target in container mode. ("", 0) if the intent has no app or no port.
func primaryAppTarget(intent *spec.AppIntent) (string, int) {
	if intent == nil {
		return "", 0
	}
	for i := range intent.Services {
		s := &intent.Services[i]
		if s.Image != "" {
			continue
		}
		if len(s.Ports) == 0 {
			return s.Name, 0
		}
		return s.Name, s.Ports[0].Container
	}
	return "", 0
}

func (d *Deployer) dnsSpec(req *spec.DeployRequest) dns.RecordSpec {
	return dns.RecordSpec{FQDN: req.DNS.Domain, Content: d.Cfg.DNS.PublicIP, TTL: d.Cfg.DNS.TTL, Proxied: d.Cfg.DNS.Proxied}
}

func (d *Deployer) now() string {
	if d.Now != nil {
		return d.Now()
	}
	return "" // tests inject a deterministic clock; runtime sets one in the agent
}

// fail records a failed stage with a code derived from the (typed) error.
func (d *Deployer) fail(res *Result, stage, message string, err error) (*Result, error) {
	res.stageErr(stage, codeOf(err), message)
	res.State = StateFailure
	return res, err
}

// failCode records a failed stage with an explicit code (for non-typed errors like a
// compose recreate failure, whose raw output must not reach the response).
func (d *Deployer) failCode(res *Result, stage, code, message string, err error) (*Result, error) {
	res.stageErr(stage, code, message)
	res.State = StateFailure
	return res, err
}

func earlyProtectedCheck(base *env.BaseEnv, overrides map[string]string) string {
	protected := base.ProtectedKeys()
	var hit []string
	for k := range overrides {
		if protected[k] {
			hit = append(hit, k)
		}
	}
	if len(hit) == 0 {
		return ""
	}
	return "event tried to override protected keys: " + strings.Join(hit, ", ")
}

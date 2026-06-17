package agent

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/accentiostudios/statio/internal/audit"
	"github.com/accentiostudios/statio/internal/deploy"
	"github.com/accentiostudios/statio/internal/dns"
	"github.com/accentiostudios/statio/internal/env"
	"github.com/accentiostudios/statio/internal/proxy"
	"github.com/accentiostudios/statio/internal/spec"
)

func (a *Agent) handleDeploy(w http.ResponseWriter, r *http.Request) {
	// 1. Envelope-first, fail-closed: a missing/empty bundle is rejected here (#14).
	env, err := spec.DecodeEnvelope(r.Body)
	if err != nil {
		writeJSON(w, statusForError(err), map[string]string{"error": err.Error()})
		return
	}
	// 2. Decode the payload to learn WHICH service it targets — still UNTRUSTED at this point.
	//    Reading the service name only selects which per-service signer to verify against; an
	//    attacker can NAME a service but cannot forge that service's signature. The exact same
	//    bytes are verified in step 4, so byte-equality holds (#15) and nothing is acted on yet.
	req, err := spec.DecodeBytes(env.Payload)
	if err != nil {
		writeJSON(w, statusForError(err), map[string]string{"error": err.Error()})
		return
	}
	svcDir := filepath.Join(a.cfg.ServicesDir, req.Service)
	if fi, err := os.Stat(svcDir); err != nil || !fi.IsDir() {
		// No auto-provision: a signed payload can only target an already-accepted service (#18).
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown service"})
		return
	}
	m, err := deploy.LoadManifest(svcDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "service misconfigured"})
		return
	}
	// 3. Resolve THIS service's signer (per-service manifest override, else the optional global).
	//    This is what lets one server accept deploys from many different repos/orgs.
	signer := m.EffectiveSigner(a.cfg.Cosign.OIDCIssuer, a.cfg.Cosign.Identity, a.cfg.Cosign.IdentityRegexp)
	// 4. Verify the cosign bundle over the EXACT payload bytes against that signer, BEFORE any
	//    effect (#15/#16). Empty identity fails closed in VerifyBlob. After this passes, `req`
	//    (decoded from the same verified bytes) is trusted.
	if err := a.verifier.VerifyBlob(r.Context(), env.Payload, env.Bundle, signer); err != nil {
		a.log.Warn("payload signature rejected", "service", req.Service, "err", err)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "payload signature verification failed"})
		return
	}
	// The optional registry credential rides on the envelope (not the signed payload) and is used
	// transiently for this deploy's verify + pull only — never logged, audited, or persisted.
	d, err := a.buildDeployer(m, env.Registry)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "agent misconfigured"})
		return
	}
	start := time.Now()
	res, runErr := d.Run(r.Context(), req)
	status := http.StatusOK
	if runErr != nil {
		status = statusForError(runErr)
		// The response carries only a sanitized stage message (invariant #23); the RAW error
		// (e.g. a registry 401 reading the cosign .sig, or "no matching signatures") must reach
		// the journal or a failed deploy is undiagnosable from the server side.
		a.log.Warn("deploy pipeline failed", "service", req.Service, "state", res.State, "http", status, "err", runErr)
	}
	a.writeAudit(req, res, signer.Identity, callerFrom(r.Context()), time.Since(start))
	writeJSON(w, status, res)
}

// writeAudit appends a redacted per-deploy record. res.Stages is already sanitized (no
// secret values / raw output), so the record is safe to persist and serve (#24).
func (a *Agent) writeAudit(req *spec.DeployRequest, res *deploy.Result, identity, src string, dur time.Duration) {
	stages := make([]audit.Stage, 0, len(res.Stages))
	for _, s := range res.Stages {
		stages = append(stages, audit.Stage{Stage: s.Stage, Status: s.Status, Code: s.Code, Message: s.Message, Hint: s.Hint})
	}
	rec := audit.Record{
		TS:         time.Now().UTC().Format(time.RFC3339),
		Service:    req.Service,
		DeploySeq:  req.DeploySeq,
		DeployID:   req.DeployID,
		Identity:   identity,
		Src:        src,
		Digest:     req.Image.Digest,
		Outcome:    res.State,
		DurationMS: dur.Milliseconds(),
		Stages:     stages,
	}
	if err := audit.Append(a.auditPath(req.Service), rec); err != nil {
		a.log.Warn("audit append failed", "service", req.Service, "err", err)
	}
}

func (a *Agent) auditPath(service string) string {
	return filepath.Join(a.cfg.StateDir, "services", service, "deploy-audit.jsonl")
}

func (a *Agent) buildDeployer(m *deploy.ServiceManifest, reg *spec.RegistryAuth) (*deploy.Deployer, error) {
	var proxyP deploy.ProxyProvider
	if a.cfg.NPMplus.BaseURL != "" && a.cfg.NPMplus.CredentialsFile != "" {
		c, err := loadNPMplusCreds(a.cfg.NPMplus.CredentialsFile)
		if err != nil {
			return nil, err
		}
		proxyP = proxy.New(a.cfg.NPMplus.BaseURL, c.Identity, c.Secret)
	}
	var dnsP deploy.DNSProvider
	if a.cfg.Cloudflare.CredentialsFile != "" {
		c, err := loadCloudflareCreds(a.cfg.Cloudflare.CredentialsFile)
		if err != nil {
			return nil, err
		}
		dnsP = dns.New(c.APIToken, c.ZoneID, "")
	}

	return &deploy.Deployer{
		Cfg:          a.cfg,
		Manifest:     m,
		StatePath:    filepath.Join(a.cfg.StateDir, "services", m.Name, "state.json"),
		Verifier:     a.verifier,
		Puller:       deploy.DockerPuller{},
		Proxy:        proxyP,
		DNS:          dnsP,
		Resolve:      env.ConfinedResolver(filepath.Join(m.Dir(), "secrets")),
		RegistryAuth: reg,
		Audience:     a.audience,
		Now:          func() string { return time.Now().UTC().Format(time.RFC3339Nano) },
		Clock:        func() time.Time { return time.Now().UTC() },
		Log:          a.log,
	}, nil
}

// handleLogs serves the redacted audit tail for a service (read-only). It is tailnet-only
// and WhoIs-gated like every route; the records carry no secrets (#24).
func (a *Agent) handleLogs(w http.ResponseWriter, r *http.Request) {
	svc := r.PathValue("service")
	if !serviceNameOK(svc) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad service name"})
		return
	}
	recs, err := audit.Tail(a.auditPath(svc), 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read audit log"})
		return
	}
	writeJSON(w, http.StatusOK, recs)
}

// serviceNameOK guards the path parameter (no traversal) before it reaches a file path.
func serviceNameOK(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func (a *Agent) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"hostname":     a.cfg.Hostname,
		"api_versions": []string{spec.APIVersion},
	})
}

func (a *Agent) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// statusForError maps a typed pipeline error to an HTTP status. Unknown errors are 500.
func statusForError(err error) int {
	var ve *spec.ValidationError
	if errors.As(err, &ve) {
		switch ve.Code {
		case "too_large":
			return http.StatusRequestEntityTooLarge
		case "repository", "policy", "audience", "expired", "no_signature":
			return http.StatusForbidden
		case "replay_seq":
			return http.StatusConflict
		default:
			return http.StatusBadRequest
		}
	}
	var ee *env.Error
	if errors.As(err, &ee) {
		switch ee.Code {
		case "protected", "required":
			return http.StatusUnprocessableEntity
		default:
			return http.StatusBadRequest
		}
	}
	return http.StatusInternalServerError
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

---
title: Architecture
description: How statio works inside — the two parts, the deploy pipeline, the signed wire contract, and the security model.
---

This is the technical reference: how statio is built, how a deploy flows end to end, the signed wire
contract, and the security model. For the usage walkthrough, see [Getting started](/getting-started/).

## 1. Two parts, one contract

statio is two parts in one repo, tied together by the `statio/v1` contract (`internal/spec`):

| Part | Where it runs | What it is |
|------|---------------|------------|
| **Server** | your server | the `statio` binary as `statio agent run` under systemd, plus `statio init` / `statio env` |
| **Action** | a GitHub Actions runner | `action.yml` (at the repo root) — downloads the **same** binary and runs `statio deploy` |

Because the Action downloads and runs the same binary, validation lives in **one Go codebase**, used
by the agent (authoritative) and by `statio deploy` (fail-fast). The contract can't drift.

**Embedded Tailscale (`tsnet`).** The agent imports `tailscale.com/tsnet`: the `statio` binary embeds
a full Tailscale node (userspace WireGuard). On the server you install **no `tailscaled` and no CLI**
— the agent joins the tailnet and exposes its endpoint only there. State persists in
`/var/lib/statio/tsnet`. Containers on the host are not on the tailnet (they don't need to be: the
agent talks to NPMplus over localhost and pulls images over normal HTTPS).

**What Tailscale is for — and isn't.** Tailscale carries **only the deploy signal**: CI (an
ephemeral `tag:ci` node, joined with CI's own `tag:ci` OAuth client) reaches the agent privately to
send the signed payload. It replaces SSH and means the agent has no public inbound port. statio does **not** serve your app over Tailscale — there
is no `tailscale serve`. Your app's **public traffic** takes a separate, ordinary path: it hits a
reverse proxy (NPMplus) on the server's public `80/443`, which forwards to the container on loopback
(`127.0.0.1`). So three paths exist, only one of them being Tailscale:

| Path | Transport | Direction |
|------|-----------|-----------|
| Deploy signal (CI → agent) | **Tailscale** (private, WireGuard) | inbound to the agent, tailnet-only |
| Image pull (agent → GHCR) | normal HTTPS | outbound |
| Your app's traffic (users → app) | normal HTTP/HTTPS via NPMplus on `80/443` | inbound to the proxy |

## 2. The four hard constraints

Non-negotiable. Every decision preserves them:

1. **No SSH**, ever.
2. The agent **listens only on the tailnet** (100.x), never on a public interface.
3. **No central broker of our own.** The central pieces (registry, Tailscale control plane, Sigstore
   TUF) are operated by third parties.
4. **No polling.** The agent receives a direct, real-time push.

Additional stance: images by **immutable digest**; authenticity by **cosign signature** (not by the
transport); the event carries **data, never scripts** — it is not remote code execution.

## 3. The deploy pipeline

Per service, under a `flock`, the agent runs an ordered pipeline:

```
1. admit       decode envelope + closed schema + allowlists + repo-equality + binding checks
2. verify      cosign signature over the exact payload bytes   ← HARD GATE (before any effect)
3. idempotency same digest + env, already healthy → no-op
4. pull        docker pull @digest + re-check (resolved digest == requested)
5. env         write the per-service env files (tmpfs, 0600)
6. recreate    generate compose + docker compose up -d (argv, never a shell string)
7. health      loopback probe                                  ← EXPOSURE GATE
8. proxy       NPMplus upsert (best-effort)
9. dns         Cloudflare upsert A record (best-effort)
10. persist    advance last-good (digest + env + edge) → respond with per-stage status
```

`verify` and `pull` are hard gates; `proxy`/`dns` are **best-effort** (a Cloudflare/NPMplus blip never
takes down a healthy container). **Health runs before the edge**, so a broken deploy is never exposed
and a rollback stays edge-neutral.

Terminal states (in the HTTP response, with no secrets):

| State | Meaning |
|-------|---------|
| `success` | verified, pulled, healthy, edge OK or not requested |
| `no_op` | same digest + same env, already healthy |
| `success_degraded` | container healthy, but proxy/dns failed (converges on retry) |
| `failure_rolled_back` | health failed → reverted image + env to the last good |
| `failure` | a hard gate failed (admit/verify/pull) → nothing changed |

## 4. The signed wire contract

CI sends a signed **envelope**; the agent verifies it before decoding anything:

```jsonc
// Envelope — capped at 512 KiB before any parse
{
  "payload": "<base64 of the EXACT signed DeployRequest bytes>",
  "bundle":  { /* cosign keyless signature + Fulcio cert + Rekor proof */ }
}
```

A missing or empty `bundle` is a downgrade attempt and **fails closed** — there is no unsigned path.
The agent decodes the payload to **peek** the `service` name (still untrusted — that only selects
*which* app's signer to check), looks up that app's manifest, then verifies the cosign bundle over the
**exact same `payload` bytes** against that app's signer, and only then acts on the decoded request.
Naming a service you don't control gets you nowhere: you can't forge that app's signature. The bytes
are never re-marshalled between verify and decode (byte-equality):

```jsonc
{
  "apiVersion": "statio/v1",
  "kind": "DeployRequest",
  "service": "api",
  "image": { "repository": "ghcr.io/accentiostudios/api", "digest": "sha256:<64hex>" },
  "app_intent": { "services": [ /* see §5 */ ] },
  "env_overrides": { "DATABASE_URL": "…" },     // values, from GitHub Secrets (courier)
  "proxy": { "enabled": true, "domain": "api.example.com", "upstream_host": "api", "upstream_port": 3000, "scheme": "http", "ssl": true },
  "dns":   { "enabled": true, "domain": "api.example.com" },

  // Binding fields — signed, and compared by the agent against its OWN config/state:
  "audience":   "statio.tailnet.ts.net",        // must equal the agent's own hostname
  "deploy_seq": 1234,                            // monotonic; must exceed the last applied
  "issued_at":  "2026-06-15T12:00:00Z",
  "expiry":     "2026-06-15T12:05:00Z"          // rejected if now > expiry
}
```

Decoded with `DisallowUnknownFields`. Every field is a scalar/bool/enum/map-of-literals — **nothing**
reaches a command, shell, template, URL, host, or path position. The binding fields close
cross-server and replay attacks: a payload is bound to one target and one moment.

**Event-carried vs server-side:**

| Carried by the event (data) | Held by the server (config/secrets) |
|---|---|
| `service`, `image.digest`, `app_intent`, `env_overrides`, `proxy`/`dns` fields, binding fields | allowed image repo (equality), cosign identity, env base + protected keys, registry/domain/upstream allowlists, NPMplus creds, Cloudflare token + zone, **public IP**, DNS record type (forced `A`) |

## 5. Generated compose (app_intent)

The agent never runs a compose file from the repo. **`statio.yaml` replaces `docker-compose.yml`**:
you describe services in `statio.yaml`, the agent turns the resulting `app_intent` into a compose
file from a **fixed, safe template** (`internal/compose`) on the server, and any compose file in your
repo is ignored — the two are never both used. The dangerous fields — `privileged`, `cap_add`,
`network_mode`, host bind-mounts, `devices`, `sysctls`, … — simply **don't exist** in the schema, so
they can't be expressed.

- Each `service` is your app (no `image:` → the verified, repo-pinned digest is injected) or a
  dependency (`image:` pinned by digest, from a registry on the server allowlist).
- **Ports** publish on `127.0.0.1` only — the host IP is hard-coded by the generator, never carried, so
  a port can't land on a public interface. A service with no ports stays on the internal network.
- **Volumes** are Docker-managed named volumes (name + path only): no driver/device/bind source, so a
  "named volume" can never become a host bind mount.
- **Env** lists key *names*; **env_inline** holds non-secret literals; **command** is exec-form only
  (no shell string); **health** is your app's loopback probe.
- Caps bound the surface: ≤20 services (the server applies a finer `max_services`), ≤20 ports/volumes
  per service, bounded command and health-path lengths.

## 6. Security model

Invariants that **don't get undone** (several are test-enforced):

1. **Data-only channel / no-RCE.** Closed schema + caps; nothing from the event reaches a command
   position.
2. **`ListenFunnel` forbidden + lint.** `go test` fails if `.ListenFunnel(` or `net.Listen(` appears,
   plus a `100.64.0.0/10` self-check at startup.
3. **Separate tailnet clients for agent and CI.** The agent joins the tailnet with its own
   `tag:agent` OAuth client (`auth_keys`+`devices`); CI joins with a **separate** `tag:ci` OAuth
   client (`auth_keys`). CI's client can't register a `tag:agent` node, so **CI can never act as the
   agent**.
4. **Exact cosign identity, per app.** Each accepted app pins its own signer (manifest); a regexp is
   only allowed if anchored and without a wildcard over owner/repo; fail-closed if no identity matches.
5. **Post-pull digest re-check + repo-equality** (the event picks *which* signed digest of an allowed
   image, never an arbitrary image).
6. **Signed envelope mandatory.** A missing bundle → 403, fail-closed; no unsigned path exists.
7. **Byte-equality.** Verify exactly the bytes that get decoded; no re-marshal in between.
8. **Target + freshness binding.** `audience` must equal the agent's hostname; `deploy_seq` is
   monotonic and persisted; `expiry` is short — all fail-closed.
9. **Server-side anchors aren't dissolved.** Allowed repo, signer identity, accepted service,
   zone/IP, upstream and registry allowlists, protected keys — compared against server config, never
   self-asserted by the payload. **No auto-provisioning** of new services.
10. **No newline/NUL/control chars** in env values; **secrets stay out of logs/argv/responses**.
11. **WhoIs fail-closed**; tlog/SCT required; secret perms validated at startup.
12. **`docker.sock` = root-equivalent** (inherent): an agent exploit before the cosign gate is host
    root. The cosign gate + the systemd sandbox are the compensating controls.

### 6.1 Each app pins its own signer

`init server` only brings up the agent (Tailscale + the OIDC issuer) — it pins **no** repo. Each app
is accepted separately with `statio app add`, which records that app's **cosign signer**
(owner/repo/workflow/branch) in its manifest. So one server can host apps from many different repos
and even organizations, each independently anchored.

When a deploy arrives the agent peeks the (untrusted) `service` name only to select that app's
signer, then verifies the signed payload against it (§4). A signed deploy can therefore only (a)
target an app you already accepted — it can never stand one up (no auto-provisioning) — and (b) be
signed by *that app's* repo. If one repo's CI is compromised, the blast radius is that one app.

### 6.2 Footguns of the signing identity

The identity is matched **exactly**. The three mistakes that cause `verify` to fail:

- **Case**: `owner`/`repo` must match GitHub character-for-character.
- **Branch**: only the configured branch deploys (`@refs/heads/<branch>`).
- **Workflow**: the file name (`deploy.yml`) must match; a reusable workflow changes the certificate
  identity.

### 6.3 Identity-compromise runbook

Each app's signer signs **both its image and its payload**. If that repo/workflow is compromised, an
attacker can sign code + config **for that one app**. Mitigations and response:

- **Prevent:** branch protection + required reviews on each app's signing repo/ref; watch every deploy
  (`statio logs`).
- **Rotate a leaked secret** (without waiting for CI): `statio env set <svc> KEY --secret-stdin` on the
  server (the ops base is the break-glass path).
- **Rotate an app's signer:** re-run `statio app add <app>` with the new repo (or edit its manifest
  `signer`). Manifests are read per deploy, so it takes effect on the next deploy — no restart.
- **Revoke CI's tailnet access:** the `tag:ci` OAuth client **secret doesn't expire** (only the
  short-lived access token it generates does, auto-refreshed), so there is nothing to rotate on a
  schedule. To cut off CI, delete or regenerate the `tag:ci` OAuth client in the Tailscale console and
  update the `STATIO_TS_OAUTH_CLIENT_ID` / `STATIO_TS_OAUTH_SECRET` secrets.
- **Bounded by design:** even with a signed payload, DNS points only at your pinned IP, the upstream
  and domains are allowlisted, and the generated compose can't escalate to host root. The blast radius
  is "that app's own repo deployed something".

### 6.6 The CI client and the agent client (separation)

CI joins the tailnet with its **own** `tag:ci` OAuth client (`auth_keys` scope), kept separate from
the agent's `tag:agent` client. CI's client only lets it *reach* the agent; it grants no deploy power
on its own (the cosign signer is the real gate), so the **same CI client safely serves every repo**.
Because CI's client is scoped to `tag:ci`, it **can't register a `tag:agent` node** — CI can never
impersonate the agent, even if a workflow is compromised. The OAuth client **secret doesn't expire**
(only the short-lived access token it mints does, auto-refreshed), so there is nothing to rotate on a
schedule; to revoke CI access, delete or regenerate the `tag:ci` client in the Tailscale console.

### 6.4 Secrets at-rest (the honest claim)

The agent runs as root with `docker.sock`, so it's root-equivalent. CI values are **RAM-only**
(`/run/statio`, tmpfs) and don't appear in logs or the response, but there's **no magic at-rest
encryption**: `docker inspect` shows them to local root. The real protection is that GitHub Secrets is
the store, the channel is signed, and nothing touches persistent disk.

### 6.5 Private repos and Rekor

`statio init repo`'s auto-detect reads the **local** git remote, so it works with private repos (no
auth, no API). The image and code stay private. But keyless signing records the *identity*
(owner/repo/workflow) in the public transparency log (Rekor): the repo **name** becomes public even if
the repo is private.

**Pulling a private image.** The agent is a separate machine with no GitHub identity of its own, so
to read a private image's cosign `.sig` at *verify* and pull it at *pull* it needs a registry
credential. Rather than store one on the server, the **Action forwards the run's short-lived token**
(the same `GITHUB_TOKEN` it pushes with) inside the deploy envelope — alongside, **not inside**, the
signed payload. The agent uses it in memory for that one deploy's verify + pull (a throwaway
`DOCKER_CONFIG` for the `docker pull` child, in-process `RegistryClientOpts` for cosign) and discards
it; it is never logged, audited, or persisted. It is a transient **capability, not a trust anchor**:
integrity comes from the cosign verify + digest pinning, so a wrong/absent token can only make the
pull fail, never substitute an image. Nothing is stored on the server, and the token auto-rotates
every run (it expires when the job ends). Needs `packages: write` (which implies read) in the
workflow `permissions`. A public image sends no token.

## 7. Server-side anchors (`statio app add`)

`statio app add` writes a per-app manifest under `/etc/statio/services/<app>/` pinning: the **cosign
signer** (owner/repo/workflow/branch — who may deploy this app), the allowed image **repository**
(repo-equality), the dependency **registry allowlist**, the proxy/dns **domain suffix allowlists** and
**upstream allowlist**, `max_services`, and the rollback policy. A deploy is compared against these —
it can never widen them. (`statio enable` is a deprecated alias of `statio app add`.)

## 8. Env courier & tmpfs

Secret values live in GitHub Secrets, travel inside the signed envelope (`env_overrides`), and the
agent writes them to **`/run/statio/<svc>/<svc>.env`** on tmpfs (RAM). Two files keep interpolation
structurally impossible: a small `interp.env` (only the image digest, for `${…}` interpolation) and the
literal `app.env` consumed via `env_file:`. A secret containing `${…}` is just a byte in `app.env`; it
never reaches the compose interpolation parser. Rollback restores the rendered `app.env` together with
the digest, as one unit.

## 9. Code layout

| Package | Responsibility |
|---------|----------------|
| `internal/spec` | the `statio/v1` contract: envelope + DeployRequest + app_intent, closed validation (shared by both parts) |
| `internal/agent` | tsnet server, `POST /deploy` handler, 100.x self-check, WhoIs guard, lint |
| `internal/verify` | cosign keyless verify (image + blob), verify-before-act |
| `internal/compose` | the safe compose generator (allowlist template) |
| `internal/deploy` | the pipeline, state/rollback, health, puller |
| `internal/env` | hybrid env merge (protected/required, two-file, no newline/NUL) |
| `internal/proxy` / `internal/dns` | typed NPMplus / Cloudflare clients (idempotent upsert) |
| `internal/audit` | redacted append-only JSONL deploy log (`statio logs`) |
| `internal/config` | global config + fail-closed validation + perms |
| `internal/client` | `statio deploy` (builds, signs, and posts the envelope) |
| `internal/cli` | the cobra tree + `huh` wizards |

```sh
go build ./...
go test ./...     # spec, env, config, proxy, dns, deploy, agent (lint), selfupdate
go vet ./...
```

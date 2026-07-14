---
name: statio-setup
description: Configure, deploy and troubleshoot statio (deploy-to-your-own-server via GitHub Actions + Tailscale, no SSH). Use when a user asks to set up statio in a repo, add statio.yaml, wire the GitHub Action, configure the server agent / NPMplus / Cloudflare, or when a statio deploy or preflight fails with any stage/code error (compose_unavailable, disk_full, port_conflict, proxy_unreachable, audience, replay_seq, 403/404/409 from the agent).
---

# statio — agent guide

Written terse, for agents. Every command exact. Trust this file over guesses.

## What statio is

Deploy signed docker image to user's own server. `git push` → CI builds + cosign-signs → agent on server (reached over Tailscale, no SSH, no public port) verifies signature → generates safe compose → recreates container. Reverse proxy (NPMplus) + DNS (Cloudflare) optional, agent-driven.

Two halves:
- **Repo side** (user's project): `statio.yaml` + one workflow step + GitHub secrets.
- **Server side** (root over SSH): `statio init server`, `statio app add <name>`, optional `statio init integrations`.

Full human docs: https://statio.accentio.dev/ (source: `website/src/content/docs/` in the statio repo).

## Repo setup — exact steps

1. Scaffold: run `statio init repo` inside the repo (interactive; prints everything). Manual below if CLI absent.

2. `statio.yaml` at repo ROOT. Minimal:

```yaml
services:
  - name: my-api              # must match the slot accepted on the server (statio app add)
    ports: [3000]             # port the app LISTENS on. statio assigns the HOST port itself (41000+ pool)
    env: [DATABASE_URL, API_KEY]   # NAMES only. Values NEVER here.
    env_inline: { NODE_ENV: production }   # non-secret literals only
    health: { path: /health } # loopback probe; failing health = rollback

proxy: { domain: api.example.com }   # OPTIONAL. ROOT level. Domain only — agent computes forward target
dns:   { domain: api.example.com }   # OPTIONAL. ROOT level. Needs Cloudflare configured on server
```

HARD RULES:
- `proxy:`/`dns:` at ROOT (column 0), NOT inside a service. Parser is strict (`field proxy not found in type statiofile.Service` = user indented it inside the service).
- Service `name`: lowercase, digits, dashes. NO underscores. Max 31 chars.
- No compose keys exist (privileged, volumes host-bind, network_mode…) — schema is closed on purpose. Don't try.
- Host port is statio-owned. `ports: [3000]` = container port; host side auto-assigned, stable per app, published on 127.0.0.1 only.

3. Workflow step (paste into existing CI, or `statio init repo --create-workflow`):

```yaml
permissions:
  contents: read
  packages: write     # push image to GHCR
  id-token: write     # cosign keyless signing — REQUIRED, deploy 403s without it

steps:
  - uses: accentiostudios/statio@v1
    with:
      target: statio.<tailnet>.ts.net       # agent MagicDNS host = signed audience
      service: my-api                       # slot name, must match statio.yaml name AND server slot
      image: ghcr.io/<owner>/<repo>         # must match server's `statio app add --image`
      ts-oauth-client-id: ${{ secrets.STATIO_TS_OAUTH_CLIENT_ID }}
      ts-oauth-secret: ${{ secrets.STATIO_TS_OAUTH_SECRET }}
      env: |
        DATABASE_URL=${{ secrets.DATABASE_URL }}
        API_KEY=${{ secrets.API_KEY }}
```

4. Secrets: `gh secret set STATIO_TS_OAUTH_CLIENT_ID/STATIO_TS_OAUTH_SECRET` (CI's tag:ci Tailscale OAuth client — same pair for every repo, can be org-wide) + one per app env value.

ENV MODEL (users confuse this constantly):
- `statio.yaml env:` = NAMES (routing + allowlist per service; committed).
- workflow `env:` block = VALUES from GitHub secrets (courier; masked).
- Server-side alternative for stable secrets: `sudo statio env set <app> KEY --secret-stdin` — persists on server, merged every deploy, no workflow line needed (name still declared in statio.yaml).
- A declared name with NO value anywhere = key silently absent in container → app crashes at runtime (e.g. Mailgun "Parameter key is required"). Check `gh secret list` when an app boot-loops on missing config.

## Server setup — exact steps (root)

```sh
curl -fsSL https://statio.accentio.dev/install.sh | sudo sh
sudo statio init server          # Tailscale tag:agent OAuth client; starts systemd agent
sudo statio app add my-api       # accept app: pins image repo + cosign signer (owner/repo/workflow/branch) + domain allowlist
sudo statio init integrations    # OPTIONAL: NPMplus + Cloudflare. Idempotent, re-run to edit one value
sudo statio upgrade              # update binary (restarts agent itself)
```

`init integrations` NPMplus notes:
- Control plane URL is loopback: `https://127.0.0.1:81` (NPMplus admin serves HTTPS with SELF-SIGNED cert by default — wizard auto-detects scheme, tries all variants, saves the one that authenticates + `insecure_skip_verify` flag, loopback-only).
- Wizard TESTS credentials live (NPMplus token mint, Cloudflare zone GET). Failure = retry form or save-unverified.
- Data-plane mode: `loopback` (NPMplus on host net → forwards to 127.0.0.1:<assigned port>; default) or `container` (shared docker network → forwards to slot-name alias). Devops picks.
- Wizard offers agent restart at the end. AGENT READS CONFIG ONLY AT BOOT — config change without restart = old settings live = "wizard passed but CI still fails".
- TLS cert for the proxied domain: statio does NOT issue it. User requests it in NPMplus UI (Certbot tab) after first deploy creates the host.

## Pipeline + reading failures

Preflight (read-only dry-run, runs automatically in the Action before deploy):
`runtime → admit → ports → proxy → dns` → `preflight_ok`. Fails fast, mutates nothing.

Deploy: `admit → verify → pull → env → recreate → health → proxy → dns → persist`.
- verify/pull = hard gates. proxy/dns = best-effort (`success_degraded`, converges on retry).
- health fail = automatic rollback to last good.
- Raw compose/docker output NEVER reaches CI (sanitization invariant). It is in the agent journal:
  `sudo journalctl -u statio-agent -n 80 --no-pager | grep -B2 -A15 "failed"`

## Error code → fix table

| Code / symptom | Cause | Fix |
|---|---|---|
| `field proxy not found in type statiofile.Service` | proxy: indented inside service | Move `proxy:`/`dns:` to column 0 of statio.yaml |
| 404 `unknown service` | slot not accepted on server, or name mismatch | `sudo statio app add <name>`; name must match workflow `service:` AND statio.yaml `name:` |
| 403 signature / `identity_mismatch` | cosign signer ≠ pinned owner/repo/workflow/branch. Case-sensitive owner/repo | `statio app list` → `statio app edit <app>`. Check workflow has `id-token: write` |
| 403 `audience` | payload targets another server | Fix Action `target:` |
| 409 `replay_seq` / `expired` | old/reused payload | Re-run the job from CI (fresh run_number) |
| `compose_unavailable` | docker compose v2 missing FOR THE AGENT. Gotcha: plugin in `/root/.docker/cli-plugins` is INVISIBLE to the agent (systemd ProtectHome) even though `sudo docker compose version` works | System-wide install: `apt-get install docker-compose-v2` (or `docker-compose-plugin`), or copy plugin to `/usr/local/lib/docker/cli-plugins/`. Verify `sudo docker compose version`. statio ≥0.2.6 also accepts standalone `docker-compose` v2 on PATH |
| `disk_full` (preflight floor or mid-deploy ENOSPC) | server disk full | `df -h /` → `docker system prune -f`, `docker image prune -af`, `journalctl --vacuum-size=200M` |
| `port_conflict` | foreign process holds the app's assigned host port | Find holder (`sudo ss -ltnp \| grep <port>`), stop it |
| `port_exhausted` | 41000–48999 pool full | `sudo statio app rm <unused>` frees its port |
| `proxy_unconfigured` / `dns_unconfigured` | statio.yaml asks proxy/dns; server has no NPMplus/Cloudflare | `sudo statio init integrations`, or drop the block from statio.yaml |
| `proxy_unreachable` | agent can't reach NPMplus or creds rejected. Common: agent not restarted after wizard; NPMplus admin is https self-signed and config predates auto-detect | Upgrade statio + re-run `sudo statio init integrations` (tests live, offers restart) |
| `proxy_no_port` | proxy requested but app declares no port | Add `ports: [<port>]` or remove proxy block |
| `protected` (422) | CI tried to override a server-protected env key | Remove key from workflow env, or unprotect server-side |
| `required` (422) | server-marked required env key missing from CI | Add `KEY=${{ secrets.KEY }}` to workflow env |
| `registry_denied` | dependency image from non-allowlisted registry | `statio app edit` → registries allowlist |
| health `timeout` + rollback | app doesn't answer on `health.path` over loopback | Check app listens on the declared container port, path returns 200. Probe hits the statio-ASSIGNED host port |
| App boots then crashes on missing config | env NAME declared, VALUE never provided (unset GitHub secret expands to empty silently) | `gh secret list`; set the secret, or `sudo statio env set <app> KEY` server-side |
| Container named `api-api-1` (doubled) | Compose `<project>-<service>-<n>`; project=slot, service=statio.yaml name | Cosmetic. Shorter `name:` if it bothers |
| `success_degraded` persists | NPMplus/Cloudflare unreachable at deploy time | Fix integration; next deploy converges. App itself is healthy |
| Agent won't start / `no tailnet address` | Tailscale ACL tags not self-owned, or OAuth client wrong scopes | tagOwners: each tag owns itself; agent client scopes `auth_keys`+`devices:core` |

## Troubleshooting flow (when user is lost)

1. Read the CI stage output — every ✗ has `[code]` + hint. Map via table above.
2. Code says "see agent journal" → run on server: `sudo journalctl -u statio-agent -n 80 --no-pager`.
3. Server sanity: `statio doctor`, `sudo docker compose version`, `df -h /`, `systemctl status statio-agent`.
4. Config sanity: `statio app list` (slot, image repo, signer, domains) vs workflow inputs vs statio.yaml.
5. Changed `/etc/statio/config.yaml` or ran the wizard? RESTART THE AGENT: `sudo systemctl restart statio-agent`.
6. Re-run the CI job (each run mints fresh seq/expiry — never re-POST an old payload).

## Maintenance rule (for agents editing the statio repo)

This skill mirrors statio behavior. Any change to: statio.yaml schema, Action inputs, CLI wizards, pipeline stages/codes/hints, or server config keys → UPDATE THIS FILE in the same commit, same as `website/src/content/docs/`.

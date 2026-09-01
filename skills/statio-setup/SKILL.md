---
name: statio-setup
description: Configure, deploy and troubleshoot statio (deploy-to-your-own-server via GitHub Actions + Tailscale, no SSH). Use when a user asks to set up statio in a repo, add statio.yaml, wire the GitHub Action, configure the server agent / NPMplus / Cloudflare, or when a statio deploy or preflight fails with any stage/code error (compose_unavailable, disk_full, port_conflict, proxy_unreachable, audience, replay_seq, 403/404/409 from the agent).
---

# statio, agent guide

Written terse, for agents. Every command exact. Trust this file over guesses.

## What statio is

Deploy a signed docker image to the user's own server. `git push` → CI builds and cosign-signs → the
agent on the server (reached over Tailscale, no SSH, no public port) verifies the signature,
generates a safe compose file, recreates the container. Reverse proxy (NPMplus) and DNS (Cloudflare)
are optional and agent-driven.

Two halves:

- **Repo side** (the user's project): `statio.yaml`, one workflow step, GitHub secrets.
- **Server side** (root over SSH): `statio init server`, `statio app add <name>`, optional
  `statio init integrations`.

Human docs: https://statio.accentio.dev/ (source: `website/src/content/docs/` in the statio repo).
The docs follow Diátaxis: `tutorials/first-deploy`, `how-to/*`, `reference/*`, `explanation/*`.

## Repo setup, exact steps

1. Scaffold: run `statio init repo` inside the repo (interactive, prints everything). Manual steps
   below when the CLI is absent.

2. `statio.yaml` at the repo ROOT. Minimal:

```yaml
services:
  - name: my-api              # must match the slot accepted on the server (statio app add)
    ports: [3000]             # port the app LISTENS on. statio assigns the HOST port itself (41000+ pool)
    env: [DATABASE_URL, API_KEY]   # NAMES only. Values NEVER here.
    env_inline: { NODE_ENV: production }   # non-secret literals only
    health: { path: /health } # loopback probe; failing health = rollback

proxy: { domain: api.example.com }   # OPTIONAL. ROOT level. Domain only, the agent computes the forward target
dns:   { domain: api.example.com }   # OPTIONAL. ROOT level. Needs Cloudflare configured on the server
```

HARD RULES:

- `proxy:` and `dns:` sit at ROOT (column 0), never inside a service. The parser is strict:
  `field proxy not found in type statiofile.Service` means the user indented it inside the service.
- Service `name`: lowercase, digits, dashes. NO underscores. 31 chars max.
- No compose keys exist (privileged, host bind volumes, network_mode). The schema is closed on
  purpose. Don't try.
- The host port belongs to statio. `ports: [3000]` is the container port; the host side is
  auto-assigned, stable per app, published on 127.0.0.1 only.
- Full field list: https://statio.accentio.dev/reference/statio-yaml/

3. Workflow step (paste into existing CI, or `statio init repo --create-workflow`):

```yaml
permissions:
  contents: read
  packages: write     # push image to GHCR
  id-token: write     # cosign keyless signing. REQUIRED, the deploy 403s without it

steps:
  - uses: accentiostudios/statio@v1
    with:
      target: statio.<tailnet>.ts.net       # agent MagicDNS host = signed audience
      service: my-api                       # slot name, must match statio.yaml name AND the server slot
      image: ghcr.io/<owner>/<repo>         # must match the server's `statio app add --image`
      ts-oauth-client-id: ${{ secrets.STATIO_TS_OAUTH_CLIENT_ID }}
      ts-oauth-secret: ${{ secrets.STATIO_TS_OAUTH_SECRET }}
      env: |
        DATABASE_URL=${{ secrets.DATABASE_URL }}
        API_KEY=${{ secrets.API_KEY }}
```

4. Secrets: `gh secret set STATIO_TS_OAUTH_CLIENT_ID` and `STATIO_TS_OAUTH_SECRET` (CI's tag:ci
   Tailscale OAuth client, the same pair for every repo, can be org-wide), plus one per app env value.

ENV MODEL (users confuse this constantly):

- `statio.yaml env:` holds NAMES (routing and allowlist per service, committed).
- The workflow `env:` block holds VALUES from GitHub secrets (courier, masked).
- Server-side alternative for stable secrets: `sudo statio env set <app> KEY --secret-stdin`. It
  persists on the server and merges into every deploy, with no workflow line. The name still has to
  be declared in statio.yaml.
- A declared name with NO value anywhere means the key is absent in the container and the app crashes
  at runtime (for example Mailgun's "Parameter key is required"). Check `gh secret list` when an app
  boot-loops on missing config.

## Server setup, exact steps (root)

```sh
curl -fsSL https://statio.accentio.dev/install.sh | sudo sh
sudo statio init server          # Tailscale tag:agent OAuth client; starts the systemd agent
sudo statio app add my-api       # accept the app: pins image repo + cosign signer (owner/repo/workflow/branch) + domain allowlist
sudo statio init integrations    # OPTIONAL: NPMplus + Cloudflare. Idempotent, re-run to edit one value
sudo statio upgrade              # update the binary (restarts the agent itself)
```

`init integrations`, NPMplus notes:

- The control plane URL is loopback: `https://127.0.0.1:81`. The NPMplus admin serves HTTPS with a
  SELF-SIGNED cert by default. The wizard auto-detects the scheme, tries every variant, and saves the
  one that authenticates plus an `insecure_skip_verify` flag, loopback only.
- The wizard TESTS credentials live (an NPMplus token mint, a Cloudflare zone GET). On failure it
  offers a retry or an unverified save.
- Data-plane mode: `loopback` (NPMplus on the host network forwards to 127.0.0.1:<assigned port>,
  the default) or `container` (a shared docker network, forwards to the slot-name alias). Devops
  picks.
- The wizard offers an agent restart at the end. THE AGENT READS ITS CONFIG ONLY AT BOOT. A config
  change with no restart leaves the old settings live, which reads as "the wizard passed and CI still
  fails".
- statio does NOT issue the TLS cert for a proxied domain. The user requests it in the NPMplus UI
  (Certbot tab) after the first deploy creates the host.

## Pipeline and reading failures

Preflight (read-only dry run, runs automatically in the Action before the deploy):
`runtime → admit → ports → proxy → dns` → `preflight_ok`. Fails fast, mutates nothing.

Deploy: `admit → verify → pull → env → recreate → health → proxy → dns → persist`.

- verify and pull are hard gates. proxy and dns are best effort (`success_degraded`, converges on
  retry).
- A health failure rolls back to the last good image and env.
- Raw compose and docker output NEVER reaches CI (sanitization invariant). It is in the agent
  journal: `sudo journalctl -u statio-agent -n 80 --no-pager | grep -B2 -A15 "failed"`

## Error code to fix table

| Code / symptom | Cause | Fix |
|---|---|---|
| `field proxy not found in type statiofile.Service` | proxy: indented inside a service | Move `proxy:` and `dns:` to column 0 of statio.yaml |
| 404 `unknown service` | slot not accepted on the server, or a name mismatch | `sudo statio app add <name>`. The name must match the workflow `service:` AND the statio.yaml `name:` |
| 403 signature / `identity_mismatch` | the cosign signer differs from the pinned owner/repo/workflow/branch. Owner and repo are case-sensitive | `statio app list`, then `statio app edit <app>`. Check the workflow has `id-token: write` |
| 403 `audience` | the payload targets another server | Fix the Action's `target:` |
| 409 `replay_seq` / `expired` | an old or reused payload | Re-run the job from CI (fresh run_number) |
| `compose_unavailable` | docker compose v2 missing FOR THE AGENT. Gotcha: a plugin in `/root/.docker/cli-plugins` is INVISIBLE to the agent (systemd ProtectHome) even though `sudo docker compose version` works | Install system-wide: `apt-get install docker-compose-v2` (or `docker-compose-plugin`), or copy the plugin to `/usr/local/lib/docker/cli-plugins/`. Verify with `sudo docker compose version`. statio 0.2.6 and later also accept a standalone `docker-compose` v2 on PATH |
| `disk_full` (preflight floor or mid-deploy ENOSPC) | the server disk is full | `df -h /`, then `docker system prune -f`, `docker image prune -af`, `journalctl --vacuum-size=200M` |
| `port_conflict` | a foreign process holds the app's assigned host port | Find the holder (`sudo ss -ltnp \| grep <port>`), stop it |
| `port_exhausted` | the 41000 to 48999 pool is full | `sudo statio app rm <unused>` frees its port |
| `proxy_unconfigured` / `dns_unconfigured` | statio.yaml asks for proxy or dns; the server has no NPMplus or Cloudflare | `sudo statio init integrations`, or drop the block from statio.yaml |
| `proxy_unreachable` | the agent cannot reach NPMplus, or the creds were rejected. Common: the agent was not restarted after the wizard; the NPMplus admin is https self-signed and the config predates auto-detect | Upgrade statio, re-run `sudo statio init integrations` (tests live, offers the restart) |
| `proxy_no_port` | a proxy was requested and the app declares no port | Add `ports: [<port>]` or remove the proxy block |
| `protected` (422) | CI tried to override a server-protected env key | Remove the key from the workflow env, or unprotect it server-side |
| `required` (422) | a server-required env key is missing from CI | Add `KEY=${{ secrets.KEY }}` to the workflow env |
| `registry_denied` | a dependency image comes from a non-allowlisted registry | `statio app edit`, widen the registries allowlist |
| health `timeout` plus rollback | the app does not answer on `health.path` over loopback | Check the app listens on the declared container port and the path returns 200. The probe hits the statio-ASSIGNED host port |
| App boots then crashes on missing config | the env NAME is declared and the VALUE was never provided (an unset GitHub secret expands to empty) | `gh secret list`. Set the secret, or `sudo statio env set <app> KEY` server-side |
| Container named `api-api-1` (doubled) | Compose names `<project>-<service>-<n>`. The project is the slot, the service is the statio.yaml name | Cosmetic. Use a shorter `name:` |
| `success_degraded` persists | NPMplus or Cloudflare was unreachable at deploy time | Fix the integration. The next deploy converges. The app itself is healthy |
| Agent will not start / `no tailnet address` | Tailscale ACL tags not self-owned, or the OAuth client has the wrong scopes | tagOwners: each tag owns itself. The agent client needs `auth_keys` and `devices:core` |

Full table with links: https://statio.accentio.dev/reference/error-codes/

## Troubleshooting flow (when the user is lost)

1. Read the CI stage output. Every ✗ carries a `[code]` and a hint. Map it with the table above.
2. When the code says to read the agent journal, run on the server:
   `sudo journalctl -u statio-agent -n 80 --no-pager`.
3. Server sanity: `statio doctor`, `sudo docker compose version`, `df -h /`,
   `systemctl status statio-agent`.
4. Config sanity: `statio app list` (slot, image repo, signer, domains) against the workflow inputs
   and statio.yaml.
5. Changed `/etc/statio/config.yaml` or ran the wizard? RESTART THE AGENT:
   `sudo systemctl restart statio-agent`.
6. Re-run the CI job. Each run mints a fresh seq and expiry, so never re-POST an old payload.

## Maintenance rule (for agents editing the statio repo)

This skill mirrors statio's behaviour. Any change to the statio.yaml schema, the Action inputs, the
CLI wizards, the pipeline stages, codes or hints, or the server config keys means UPDATING THIS FILE
in the same commit, the same as `website/src/content/docs/`. The error table here and
`website/src/content/docs/reference/error-codes.md` carry the same facts and change together.

Docs style for that repo: Diátaxis for structure (tutorial, how-to, reference, explanation, never
mixed), plain prose for wording (no em dash joining clauses, bold for UI labels only, name the actor,
no adverbs).

---
title: Error codes
description: Every code a deploy or preflight can return, its cause, and the fix.
sidebar:
  order: 7
prev: false
next: false
---

Each failure carries a stable code in brackets and a hint. Raw docker and compose output stays in
`journalctl -u statio-agent` and never reaches CI.

For the order in which to look, see
[diagnose a failed deploy](/how-to/diagnose-a-failed-deploy/).

## Signature and binding

| Code | Cause | Fix |
|---|---|---|
| `403 [no_signature]` | The envelope carried no cosign bundle. | Add `permissions: id-token: write` to the job. |
| `403 [identity_mismatch]` | The signing identity differs from the app's pinned signer. Owner and repo are case-sensitive. | `sudo statio app list`, then `sudo statio app edit <app>`. Compare owner, repo, workflow file, branch. |
| `403 [audience]` | The payload targets another server. | Fix the Action's `target` input. |
| `409 [replay_seq]` | The sequence number does not exceed the last applied one. | Re-run the job from CI. |
| `409 [expired]` | The payload arrived after its expiry. | Re-run the job from CI. |
| `404 unknown service` | No app of that name is accepted on this server. | `sudo statio app add <name>`. The name must match the workflow's `service:` and the `name:` in `statio.yaml`. |

## Schema and env

| Code | Cause | Fix |
|---|---|---|
| `field proxy not found in type statiofile.Service` | `proxy:` or `dns:` is indented inside a service. | Move both blocks to column 0. |
| `422 [protected]` | CI tried to override a key marked `--protected`. | Remove it from the workflow's `env:`, or unprotect it server-side. |
| `422 [required]` | A key marked `--required` was missing. | Add `KEY=${{ secrets.KEY }}` to the workflow's `env:`. |
| `[env_count]`, `[env_size]`, `[env_value]`, `[env_key]` | An env override broke a limit. | See the limits in the [wire contract](/reference/wire-contract/). |
| `[registry_denied]` | A dependency image comes from a registry outside the allowlist. | `sudo statio app edit <app>` and widen the registry list. |

## Runtime and host

| Code | Cause | Fix |
|---|---|---|
| `[compose_unavailable]` | The agent finds no `docker compose` v2. A plugin in `/root/.docker/cli-plugins` stays invisible to the agent under systemd `ProtectHome`, even when `sudo docker compose version` works. | Install it system-wide: `apt-get install docker-compose-v2` or `docker-compose-plugin`, or copy the plugin to `/usr/local/lib/docker/cli-plugins/`. statio 0.2.6 and later also accept a standalone `docker-compose` v2 on `PATH`. |
| `[disk_full]` | The disk hit the preflight floor, or the deploy hit ENOSPC. | `df -h /`, then `docker system prune -f`, `docker image prune -af`, `journalctl --vacuum-size=200M`. |
| `[port_conflict]` | A process outside statio holds this app's allocated host port. | `sudo ss -ltnp \| grep <port>`, then stop the holder. |
| `[port_exhausted]` | The 41000 to 48999 pool is full. | `sudo statio app rm <unused-app>` frees its port. |
| `[compose_failed]`, `[internal]` | The recreate failed. | `sudo journalctl -u statio-agent -n 80 --no-pager`, search for `deploy pipeline failed`. |

## Proxy and DNS

| Code | Cause | Fix |
|---|---|---|
| `[proxy_unconfigured]`, `[dns_unconfigured]` | `statio.yaml` asks for a domain and the server has no NPMplus or no Cloudflare. | `sudo statio init integrations`, or drop the block. |
| `[proxy_unreachable]` | The agent cannot reach NPMplus, or NPMplus rejected the credentials. Common cause: the agent was not restarted after the wizard. | `sudo statio upgrade`, re-run `sudo statio init integrations`, accept the restart. |
| `[proxy_no_port]` | The app asks for a proxy and declares no port. | Add `ports: [<port>]`, or drop the `proxy:` block. |
| `[proxy_domain]`, `[dns_domain]` | The domain falls outside the app's allowed suffix. | `sudo statio app edit <app>` and set the suffix. |
| `success_degraded` that persists | NPMplus or Cloudflare was unreachable at deploy time. | Fix the integration. The next deploy converges. The container is healthy meanwhile. |

## Health and rollback

| Code | Cause | Fix |
|---|---|---|
| `[timeout]` followed by `failure_rolled_back` | The app does not answer on `health.path` over loopback. | Check the app listens on the declared container port and returns 200 on that path. |
| The app boots, then crashes on missing config | An env name is declared and no value was ever provided. An unset GitHub secret expands to an empty string. | `gh secret list`, then set it, or set it server-side with `sudo statio env set <app> KEY`. |

## Setup

| Symptom | Cause | Fix |
|---|---|---|
| `Unit statio-agent.service not found` | The agent was never set up here. Installing the binary creates no service. | `sudo statio init server`, then `sudo systemctl enable --now statio-agent`. |
| `requested tags … are invalid or not permitted` | The Tailscale tags do not own themselves. | In `tagOwners`, each tag lists itself. See [set up Tailscale](/how-to/set-up-tailscale/). |
| The agent will not start, `no tailnet address` | Wrong OAuth scopes, or the node awaits approval. | The agent's client needs `auth_keys` and `devices:core` on `tag:agent`. |
| `gh secret set` says `not a git repository` | You ran it on the server, or outside a repo. | Run it on your machine inside the repo, or pass `--repo owner/repo`, or `--org <org> --visibility all`. |
| The container is named `api-api-1` | Compose names containers `<project>-<service>-<n>`. The project is the slot, the service is the `name:` in `statio.yaml`. | Cosmetic. Use a shorter `name:`. |

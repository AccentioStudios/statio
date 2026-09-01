---
title: Server files and anchors
description: What each statio command writes on the server, and what a deploy is compared against.
sidebar:
  order: 6
---

## Paths

| Path | Written by | Contents |
|---|---|---|
| `/etc/statio/config.yaml` | `statio init server`, `statio init integrations` | Agent config, mode `0600`, owned by root. Read at boot. |
| `/etc/statio/secrets/` | the init wizards | The Tailscale OAuth client, the NPMplus login, the Cloudflare token. Permission-checked at startup. |
| `/etc/statio/services/<app>/` | `statio app add`, `statio app edit` | One manifest per accepted app. Read on every deploy. |
| `/var/lib/statio/tsnet` | the agent | Tailscale node state. |
| `/var/lib/statio/ports.json` | the agent | The host-port registry, one stable port per slot. |
| `/run/statio/<svc>/` | the agent | `app.env` and `interp.env` on tmpfs, mode `0600`. Cleared on reboot. |
| the audit log | the agent | One redacted JSONL record per deploy, read with `statio logs`. |

The agent reads `/etc/statio/config.yaml` at boot only. After an edit or a wizard run, restart it:

```sh
sudo systemctl restart statio-agent
```

Manifests under `/etc/statio/services/` are read per deploy, so `statio app edit` takes effect on the
next push.

## What an app manifest pins

`statio app add` writes these. A deploy is compared against them and can never widen them.

| Anchor | Meaning |
|---|---|
| cosign signer | Owner, repo, workflow file and branch. Whoever may deploy this app. |
| image repository | Compared for equality against the payload's repository. |
| registry allowlist | Where dependency images may come from. |
| proxy domain suffix | Which domains this app may claim on the reverse proxy. |
| dns domain suffix | Which domains this app may claim in DNS. |
| upstream allowlist | Which forward targets the proxy may use. |
| `max_services` | A per-app cap below the wire limit of 20. |
| rollback policy | What the agent does when health fails. |

## Host ports

statio allocates one host port per accepted app from the range **41000 to 48999**, records it in
`ports.json`, and keeps it stable across deploys. Ports publish on `127.0.0.1`.

A port held by a foreign process answers `[port_conflict]`. An exhausted range answers
`[port_exhausted]`; free one with `sudo statio app rm <app>`.

## Global config keys

`statio init integrations` writes these. Both blocks are optional and a deploy works without them.

| Block | Keys |
|---|---|
| NPMplus | admin API URL, login email and password, data-plane mode (`loopback` or `container`), shared network name in `container` mode, `insecure_skip_verify` for the self-signed admin certificate on loopback |
| Cloudflare | API token scoped to `Zone.DNS:Edit` on one zone, the zone, the pinned public IP |

The agent forces the DNS record type to `A` and its target to the pinned public IP, so no event
repoints DNS elsewhere.

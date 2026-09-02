---
title: Wire contract
description: The signed envelope, the DeployRequest fields, and the limits the agent enforces.
sidebar:
  order: 5
prev: false
next: false
---

CI sends an envelope. The agent verifies it before it acts on anything inside.

## Envelope

```jsonc
{
  "payload": "<base64 of the exact signed DeployRequest bytes>",
  "bundle":  { /* cosign keyless signature, Fulcio certificate, Rekor proof */ }
}
```

The agent caps the body at **256 KiB** before it parses anything. A missing or empty `bundle` answers
`403`, since no unsigned path exists.

The agent decodes the payload to read the `service` name, which stays untrusted and only selects
which app's signer to check. It then verifies the bundle over the same `payload` bytes and acts on
the decoded request. The bytes are never re-marshalled between verify and decode.

## DeployRequest

```jsonc
{
  "apiVersion": "statio/v1",
  "kind": "DeployRequest",
  "service": "api",
  "image": { "repository": "ghcr.io/your-org/api", "digest": "sha256:<64 hex>" },
  "app_intent": { "services": [ /* the statio.yaml services */ ] },
  "env_overrides": { "DATABASE_URL": "…" },
  "proxy": { "enabled": true, "domain": "api.example.com", "upstream_host": "api", "upstream_port": 3000, "scheme": "http", "ssl": true },
  "dns":   { "enabled": true, "domain": "api.example.com" },

  "audience":   "statio.your-tailnet.ts.net",
  "deploy_seq": 1234,
  "issued_at":  "2026-06-15T12:00:00Z",
  "expiry":     "2026-06-15T12:05:00Z"
}
```

The agent decodes with unknown fields rejected. Every field is a scalar, a boolean, an enum, or a map
of literals.

### Binding fields

The agent compares each of these against its own config and state, and fails closed.

| Field | Check |
|---|---|
| `audience` | Equals the agent's own hostname. Mismatch answers `403 [audience]`. |
| `deploy_seq` | Exceeds the last applied value. The Action sets it from `github.run_number`. Mismatch answers `409 [replay_seq]`. |
| `issued_at` | The moment CI signed the payload. |
| `expiry` | Minutes after `issued_at`. A later arrival answers `409 [expired]`. |

## Limits

| Limit | Value |
|---|---|
| Envelope body | 256 KiB, checked before decode |
| Env override keys | 100 |
| Env override value | 4096 bytes |
| Env section, keys and values summed | 64 KiB |
| Services | 20, and the server may set a lower `max_services` |
| Ports per service | 20 |
| Volumes per service | 20 |
| Command items | 100, at 1024 bytes each |
| Health path | 512 characters |

## Patterns

| Field | Pattern |
|---|---|
| Service name, wire | `^[a-z0-9][a-z0-9_-]{0,63}$` |
| Service name, app_intent | `^[a-z][a-z0-9-]{0,30}$` |
| Digest | `^sha256:[0-9a-f]{64}$` |
| Env key | `^[A-Z_][A-Z0-9_]{0,127}$` |
| Image repository | `^[a-z0-9][a-z0-9._/-]{0,253}[a-z0-9]$` |
| Volume name | `^[a-z0-9][a-z0-9_-]{0,40}$` |
| Duration | `^[0-9]{1,6}(ms\|s\|m)$` |

Env values carry no newline, NUL, or control character.

## Generated compose

The agent turns `app_intent` into a compose file from a fixed template in `internal/compose`.

- A service with no `image:` receives the verified, repository-pinned digest. A service with an
  `image:` is a dependency, pinned by digest, from a registry on the server allowlist.
- Ports publish on `127.0.0.1`. The generator hard-codes that host IP and never reads it from the
  event. A service with no ports stays on the internal network.
- In `container` proxy mode the app also joins a named external bridge network, so NPMplus reaches it
  by name. `network_mode` stays unavailable.
- Volumes are Docker-managed named volumes, a name and a path, with no driver, device, or bind
  source.
- `env` lists key names, `env_inline` holds non-secret literals, `command` is exec form, and `health`
  is the loopback probe.

## Two env files

The agent writes the deploy's env to `/run/statio/<svc>/`, on tmpfs, and splits it in two:

| File | Contents | Read by |
|---|---|---|
| `interp.env` | the image digest alone | compose, for `${…}` interpolation |
| `app.env` | the literal values | the container, through `env_file:` |

A secret containing `${…}` is a byte in `app.env` and never reaches the compose interpolation parser.
A rollback restores `app.env` and the digest together, as one unit.

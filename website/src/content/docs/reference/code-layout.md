---
title: Code layout
description: What lives in each package of the statio repo.
sidebar:
  order: 8
prev: false
next: false
---

| Path | Responsibility |
|---|---|
| `cmd/statio` | The binary entrypoint. |
| `internal/spec` | The `statio/v1` contract: envelope, DeployRequest, app_intent, closed validation. Shared by the agent and the client. |
| `internal/agent` | The tsnet server, the `POST /deploy` and `/preflight` handlers, the `100.x` self-check, the WhoIs guard, the listener lint. |
| `internal/verify` | Keyless cosign verification of the image and the blob, before any effect. |
| `internal/compose` | The compose generator and its allowlist template. |
| `internal/deploy` | The pipeline, state and rollback, health, the puller, port allocation. |
| `internal/env` | The env merge: protected and required keys, the two-file split, the newline and NUL rejection. |
| `internal/proxy` | The typed NPMplus client, with idempotent upserts. |
| `internal/dns` | The typed Cloudflare client, with idempotent upserts. |
| `internal/audit` | The redacted, append-only JSONL deploy log behind `statio logs`. |
| `internal/config` | The global config, its fail-closed validation, and file permission checks. |
| `internal/statiofile` | The strict `statio.yaml` parser and its conversion to the wire types. |
| `internal/client` | `statio deploy`: builds, signs, and posts the envelope. |
| `internal/cli` | The cobra command tree and the `huh` wizards. |
| `internal/selfupdate` | `statio upgrade` and the daily version check. |
| `action/` | The composite GitHub Action that runs in CI. |
| `website/` | This documentation site, built with Astro Starlight. |
| `skills/statio-setup/` | The agent-facing setup and troubleshooting skill. |

## Checks

```sh
go build ./...
go vet ./...
go test ./...
gofmt -l .
```

`go test` covers the spec, env, config, proxy, dns and deploy packages, the agent listener lint, and
selfupdate. `gofmt -l` must print nothing.

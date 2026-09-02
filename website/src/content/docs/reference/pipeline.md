---
title: Deploy pipeline
description: The ordered stages of a deploy, the gates, and every terminal state.
sidebar:
  order: 4
prev: false
next: false
---

The agent runs one pipeline per service, under a `flock`, in this order.

| # | Stage | What the agent does |
|---|---|---|
| 1 | `admit` | Decodes the envelope. Checks the closed schema, the allowlists, repository equality, and the binding fields. |
| 2 | `verify` | Verifies the cosign signature over the exact payload bytes. Hard gate, before any effect. |
| 3 | `idempotency` | Same digest and same env, already healthy, so it stops with `no_op`. |
| 4 | `pull` | `docker pull` by digest, then re-checks that the resolved digest equals the requested one. Hard gate. |
| 5 | `env` | Writes the per-service env files to tmpfs, mode `0600`. |
| 6 | `recreate` | Generates the compose file and runs `docker compose up -d` as argv, never as a shell string. |
| 7 | `health` | Probes the app over loopback. Exposure gate. |
| 8 | `proxy` | Upserts the host in NPMplus. Best effort. |
| 9 | `dns` | Upserts the `A` record in Cloudflare. Best effort. |
| 10 | `persist` | Advances the last-good record, the digest with its env and edge state, and answers with per-stage status. |

Between `admit` and `recreate` the agent allocates this slot's host port from a host-level registry
in `ports.json`. statio is the only allocator, so two slots never publish the same host port.

`verify` and `pull` are hard gates: a failure there changes nothing. `proxy` and `dns` are best
effort, so an outage at Cloudflare or NPMplus leaves a healthy container running. `health` runs
before the edge, so a broken deploy never reaches the public.

## Preflight

A preflight runs `admit`, port availability, and proxy and DNS readiness, then stops. It pulls
nothing, recreates nothing, writes no edge state, and changes no state at all.

CI posts the same signed payload to `/preflight`, so it passes the same signature gate as a deploy
and is no anonymous probe.

Its stages: `runtime`, `admit`, `ports`, `proxy`, `dns`.

## Terminal states

| State | Meaning |
|---|---|
| `success` | Verified, pulled, healthy, edge applied or not requested. |
| `no_op` | Same digest and env, already healthy. |
| `success_degraded` | The container is healthy, and proxy or dns failed. Converges on the next deploy. |
| `failure_rolled_back` | Health failed, so the agent restored the previous image and env. |
| `failure` | A hard gate failed at `admit`, `verify`, or `pull`. Nothing changed. |
| `preflight_ok` | A preflight passed every check. No deploy happened. |

The Action treats `success_degraded` as a pass. Set its `strict` input to `true` to fail the job
instead.

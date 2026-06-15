---
title: Troubleshooting
description: Common deploy failures and what to check.
sidebar:
  order: 3
---

Every failure carries a stable `code` plus a `hint`. The raw detail (compose output) stays only in
`journalctl -u statio-agent`, never in the response to CI.

| Symptom | What to check |
|---------|---------------|
| Agent won't start (`no tailnet address`) | The `tag:agent` OAuth client, and that the node is approved in Tailscale. |
| Deploy `403` `[audience]` | The payload targets another server: check the Action's `target`. |
| Deploy `403` `[no_signature]` / `[identity_mismatch]` | Missing bundle, or the signing identity doesn't match the configured one (owner/repo/workflow/branch). |
| Deploy `409` `[replay_seq]` or `[expired]` | Stale/reused payload: re-run the deploy from CI. |
| Deploy `422` `[protected]` / `[required]` | You tried to override a `--protected` key, or a `--required` key is missing. |
| `[registry_denied]` | A dependency uses a registry outside the allowlist (`statio enable --registries`). |
| `success_degraded` that won't clear | NPMplus or Cloudflare unreachable. Check `statio init integrations` and retry. |
| `[timeout]` and it reverts | The app doesn't answer on the health path (loopback). Check the container. |

For the meaning of each pipeline stage and state, see the
[architecture](/architecture/#3-the-deploy-pipeline).

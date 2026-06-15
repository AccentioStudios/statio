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
| `systemctl restart statio-agent` → `Unit statio-agent.service not found` | The agent isn't set up on this server yet — installing the binary doesn't create the service. Run `sudo statio init server` (it writes the systemd unit), then `sudo systemctl enable --now statio-agent`. |
| Agent won't start (`no tailnet address`) | The Tailscale OAuth client (scopes `auth_keys`+`devices`, owns `tag:agent`+`tag:ci`), and that the node is approved. |
| Deploy `403` `[audience]` | The payload targets another server: check the Action's `target`. |
| Deploy `403` `[no_signature]` / `[identity_mismatch]` | Missing bundle, or the signing identity doesn't match the app's signer (owner/repo/workflow/branch from `statio app add`). |
| Deploy `409` `[replay_seq]` or `[expired]` | Stale/reused payload: re-run the deploy from CI. |
| Deploy `422` `[protected]` / `[required]` | You tried to override a `--protected` key, or a `--required` key is missing. |
| `[registry_denied]` | A dependency uses a registry outside the allowlist (`statio app add --registries`). |
| `success_degraded` that won't clear | NPMplus or Cloudflare unreachable. Check `statio init integrations` and retry. |
| `[timeout]` and it reverts | The app doesn't answer on the health path (loopback). Check the container. |

For the meaning of each pipeline stage and state, see the
[architecture](/architecture/#3-the-deploy-pipeline).

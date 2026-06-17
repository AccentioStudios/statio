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
| `init server` → `requested tags … are invalid or not permitted` (and the agent fails to start) | The tags aren't self-owned. In `tagOwners`, each tag must list **itself** (`"tag:ci": ["autogroup:admin", "tag:ci"]`) — an OAuth client may only mint a key for, or register a device with, a tag owned by a tag it carries. Fix the ACL and re-run `sudo statio init server`. |
| `gh secret set` → `not a git repository` / `could not determine base repo` | You ran it on the **server** (or outside a repo). The secret lives in GitHub, not on the server: run it on your machine with `--repo owner/repo`, or `cd` into the repo and drop the flag, or set it once for the org with `--org <org> --visibility all`. |
| Agent won't start (`no tailnet address`) | The Tailscale OAuth client (scopes `auth_keys`+`devices:core`, owns `tag:agent`+`tag:ci`), that the tags are self-owned in `tagOwners`, and that the node is approved. |
| Deploy `403` `[audience]` | The payload targets another server: check the Action's `target`. |
| Deploy `403` `[no_signature]` / `[identity_mismatch]` | Missing bundle, or the signing identity doesn't match the app's signer (owner/repo/workflow/branch from `statio app add`). |
| Deploy `500` at `verify` `[internal] signature` (image in a **private** repo) | The agent can't read the image's cosign `.sig` — it has no registry credential. Run `sudo statio registry login ghcr.io` on the server (the `gh` token needs `read:packages`: `gh auth refresh -s read:packages`). Then `sudo statio upgrade` if the unit predates `DOCKER_CONFIG`, and re-deploy. The raw cause is in `sudo journalctl -u statio-agent` (look for `deploy pipeline failed`). |
| Deploy `409` `[replay_seq]` or `[expired]` | Stale/reused payload: re-run the deploy from CI. |
| Deploy `422` `[protected]` / `[required]` | You tried to override a `--protected` key, or a `--required` key is missing. |
| `[registry_denied]` | A dependency uses a registry outside the allowlist (`statio app add --registries`). |
| `success_degraded` that won't clear | NPMplus or Cloudflare unreachable. Check `statio init integrations` and retry. |
| `[timeout]` and it reverts | The app doesn't answer on the health path (loopback). Check the container. |

For the meaning of each pipeline stage and state, see the
[architecture](/architecture/#3-the-deploy-pipeline).

---
title: Diagnose a failed deploy
description: Work from the CI output down to the agent journal, in order.
sidebar:
  order: 9
prev: false
next:
  link: /reference/error-codes/
  label: Error codes
---

Follow these steps in order. Each one narrows the search before the next.

## 1. Read the failing stage in CI

The Action prints one line per stage. The failing line carries a code in brackets and a hint:

```
✓ admit   ✗ verify   [identity_mismatch] the signing identity does not match this app's signer
```

Look the code up in [error codes](/reference/error-codes/). Most failures end here.

## 2. Read the agent journal

The agent never sends raw docker or compose output to CI. When the code tells you to read the
journal, run this on the server:

```sh
sudo journalctl -u statio-agent -n 80 --no-pager
```

Search it for `deploy pipeline failed`.

## 3. Check the server

```sh
sudo statio doctor
sudo docker compose version
df -h /
systemctl status statio-agent
```

`doctor` covers the config, the secret files, the state directory and the service. A missing
`docker compose` shows up as `[compose_unavailable]`, a full disk as `[disk_full]`.

## 4. Compare the three places a name appears

A name mismatch produces `404 unknown service`. The same string has to appear in all three:

| Where | Field |
|---|---|
| Server | `statio app list`, the slot name |
| Workflow | the `service:` input |
| Repo | the `name:` of the service in `statio.yaml` |

`statio app list` also shows the pinned image repository and the cosign signer. Compare them against
the workflow's `image:` and against the repo, workflow file and branch that actually ran.

## 5. Restart the agent after a config change

The agent reads `/etc/statio/config.yaml` at boot. If you edited it or re-ran
`statio init integrations` without accepting the restart, the running agent still uses the old
settings:

```sh
sudo systemctl restart statio-agent
```

## 6. Re-run the job

Re-run the workflow from CI. Each run mints a fresh sequence number and expiry, which is why a
re-sent old payload fails with `409 [replay_seq]` or `409 [expired]`.

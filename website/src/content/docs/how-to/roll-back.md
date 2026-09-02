---
title: Roll back a deploy
description: Redeploy an earlier digest, and what the agent does on a failed health check.
sidebar:
  order: 3
prev: false
next: false
---

## Roll back by hand

Every deploy pins an explicit digest, so a rollback is a deploy of the earlier digest.

1. Find the digest you want. `statio logs api` lists one line per deploy, each with its digest.
2. Open the repo's **Actions** tab, pick the `deploy` workflow, and choose **Run workflow**.
3. Paste the digest into the `digest` input and run it.

CI signs a fresh payload carrying the old digest. The agent treats it as a new deploy, so the
sequence check passes.

The generated `deploy.yml` ships this input. If you wrote your own workflow, add it:

```yaml
on:
  workflow_dispatch:
    inputs:
      digest:
        description: "Existing image digest to redeploy"
        required: false
```

and pass it through:

```yaml
with:
  digest: ${{ inputs.digest }}
```

## What the agent does on its own

When a new deploy fails its health check, the agent restores the previous image and its env together,
as one unit, and answers `failure_rolled_back`. The snapshot lives in `/run`, on tmpfs, so it
survives until the server reboots.

After a reboot the agent has no snapshot to restore. Redeploy from CI.

## Why nothing broke publicly

The agent probes health before it touches the proxy or DNS, so a broken deploy never reaches the
edge, and a rollback leaves the edge untouched.

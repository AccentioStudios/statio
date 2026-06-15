---
title: Rollback
description: Manual rollback by digest, and automatic rollback on a failed health check.
sidebar:
  order: 3
---

## Manual rollback

Every deploy carries an explicit digest, so rolling back is just deploying the old digest. From
the **Actions → Run workflow** tab, set the `digest` input (the generated `deploy.yml` supports it).
CI signs a fresh payload with the old digest — valid and not a replay.

## Automatic rollback

If a new deploy doesn't pass the health check, the rollback is **automatic**: the agent restores
the last healthy version (image + env together, as one unit). The snapshot lives in RAM (`/run`),
so auto-rollback works within the same boot.

After a reboot there is no offline rollback — you redeploy from CI. That's the courier model: CI
is the source of truth for what runs.

:::note
The health probe runs **before** the edge (proxy/DNS) is touched, so a broken deploy is never
exposed publicly, and a rollback is edge-neutral.
:::

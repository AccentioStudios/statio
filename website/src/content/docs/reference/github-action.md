---
title: GitHub Action
description: The accentiostudios/statio Action — full workflow, inputs, versioning and where it lives.
sidebar:
  order: 1
---

statio ships a GitHub Action, published on the Marketplace as **`accentiostudios/statio@v1`**. It
installs the pinned `statio` binary, installs cosign, joins your tailnet as an ephemeral `tag:ci`
node, signs the deploy payload with the run's OIDC identity, and sends the signed envelope to your
agent.

## Minimal usage

Add this step **after** you build, push and sign your image:

```yaml
- uses: accentiostudios/statio@v1
  with:
    target: statio.your-tailnet.ts.net          # the agent's MagicDNS host (= signed audience)
    service: api                                # must be accepted on the server (statio app add)
    image: ghcr.io/accentiostudios/api          # must match `statio app add --image`
    digest: ${{ steps.build.outputs.digest }}
    ts-authkey: ${{ secrets.STATIO_TS_AUTHKEY }}  # minted by `statio init server`
    env: |                                      # optional per-deploy env, from GitHub Secrets
      DATABASE_URL=${{ secrets.DATABASE_URL }}
```

:::caution
Your job **must** grant `permissions: id-token: write` so cosign can sign the image and the payload
keyless via the run's OIDC identity. Without it, the agent rejects the deploy.
:::

## Full workflow

A complete `.github/workflows/deploy.yml` — build, push, sign, deploy. This is exactly what
`statio init repo` generates when your repo has no CI yet:

```yaml
name: deploy
on:
  push:
    branches: [main]
  workflow_dispatch:
    inputs:
      digest:
        description: "Existing image digest to (re)deploy — use for rollback"
        required: false

# id-token: keyless cosign (OIDC); packages: push to GHCR; contents: checkout.
permissions:
  id-token: write
  packages: write
  contents: read

concurrency:
  group: deploy-${{ github.ref }}
  cancel-in-progress: false

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/setup-buildx-action@v3
      - id: build
        uses: docker/build-push-action@v7
        with:
          context: .
          push: true
          tags: ghcr.io/accentiostudios/api:${{ github.sha }}
      - uses: sigstore/cosign-installer@v3
      # Sign the immutable digest (never a mutable tag), keyless via the run's OIDC identity.
      - run: cosign sign --yes ghcr.io/accentiostudios/api@${{ steps.build.outputs.digest }}
      # statio reads statio.yaml, signs the payload with the SAME OIDC identity, and sends the
      # signed envelope. The agent verifies it before acting.
      - uses: accentiostudios/statio@v1
        with:
          target: statio.your-tailnet.ts.net
          service: api
          image: ghcr.io/accentiostudios/api
          digest: ${{ inputs.digest || steps.build.outputs.digest }}
          ts-authkey: ${{ secrets.STATIO_TS_AUTHKEY }}
          env: |
            DATABASE_URL=${{ secrets.DATABASE_URL }}
```

Already have a workflow? Don't copy the whole file — `statio init repo` prints just the step to add
to your existing one, and never touches your file.

## Inputs

| Input | Required | What it is |
|---|---|---|
| `target` | yes | The agent's MagicDNS host (e.g. `statio.your-tailnet.ts.net`). It is the **signed audience** — the deploy is bound to that server. |
| `service` | yes | The app slot; must be accepted on the server (`statio app add`). |
| `image` | yes | Your image repository; must match `statio app add --image` (repo-equality). |
| `digest` | yes | The digest to deploy (`steps.build.outputs.digest`, or an old one for rollback). |
| `env` | no | Per-deploy overrides, `KEY=${{ secrets.KEY }}` lines. GitHub masks them. |
| `statio-file` | no | Path to `statio.yaml` (default `statio.yaml`). |
| `ts-authkey` | yes | The Tailscale **`tag:ci`** auth key minted by `statio init server` (the `STATIO_TS_AUTHKEY` secret). |
| `statio-version` | no | Binary version to download (default `v1`; a bare major, exact `vX.Y.Z`, or `latest`). |
| `timeout` | no | Deploy timeout (default `5m`). |
| `strict` | no | Treat `success_degraded` as a failure (default `false`). |

`deploy_seq` (anti-replay) is set by the Action itself from `github.run_number` — don't configure it.

## Versioning

`@v1` is a **moving major tag**: it always points at the latest stable `1.x` release. Pin an exact
version with `@v0.1.0` if you prefer reproducible builds. The `statio-version` input keeps the
downloaded binary in lockstep with the wire schema.

## Where the Action lives

The Action is **not a separate repo**. Its `action.yml` lives at the root of
[`accentiostudios/statio`](https://github.com/accentiostudios/statio); the same repo ships the CLI,
the agent and the binary releases. That's why a single `uses: accentiostudios/statio@v1` is all you
need — no extra install step for the tool itself.

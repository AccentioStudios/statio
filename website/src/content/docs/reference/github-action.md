---
title: GitHub Action
description: The accentiostudios/statio Action — full workflow, inputs, versioning and where it lives.
sidebar:
  order: 1
---

statio ships a GitHub Action, published on the Marketplace as **`accentiostudios/statio@v1`**. One
step does the whole pipeline: it **builds and pushes your image**, **cosign-signs it** keyless,
installs the pinned `statio` binary, joins your tailnet as an ephemeral `tag:ci` node, **signs the
deploy payload** with the same OIDC identity, and sends the signed envelope to your agent — no
separate `build-push`, `cosign-installer` or `cosign sign` steps to wire up.

## Minimal usage

```yaml
- uses: actions/checkout@v4
- uses: accentiostudios/statio@v1
  with:
    target: statio.your-tailnet.ts.net          # the agent's MagicDNS host (= signed audience)
    service: api                                # must be accepted on the server (statio app add)
    image: ghcr.io/accentiostudios/api          # the action builds+pushes here; the agent runs it
    ts-oauth-client-id: ${{ secrets.STATIO_TS_OAUTH_CLIENT_ID }}  # CI's tag:ci OAuth client
    ts-oauth-secret: ${{ secrets.STATIO_TS_OAUTH_SECRET }}
    env: |                                      # optional per-deploy env, from GitHub Secrets
      DATABASE_URL=${{ secrets.DATABASE_URL }}
```

:::caution
Your job **must** grant `permissions: id-token: write` (keyless cosign), `packages: write` (push to
GHCR) and `contents: read`. Without `id-token: write` the agent rejects the deploy.
:::

Already build the image in your own steps? Pass `digest: ${{ steps.build.outputs.digest }}` and the
action **skips building** (it still signs + deploys). Add `sign: false` if you also sign it yourself.

## Full workflow

A complete `.github/workflows/deploy.yml` — exactly what `statio init repo` generates when your repo
has no CI yet:

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
      # One step builds + pushes + signs the image, then signs the payload and deploys.
      - uses: accentiostudios/statio@v1
        with:
          target: statio.your-tailnet.ts.net
          service: api
          image: ghcr.io/accentiostudios/api
          digest: ${{ inputs.digest }}          # empty on push → the action builds; set to redeploy
          ts-oauth-client-id: ${{ secrets.STATIO_TS_OAUTH_CLIENT_ID }}
          ts-oauth-secret: ${{ secrets.STATIO_TS_OAUTH_SECRET }}
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
| `image` | yes | Your image repository; the action builds + pushes here, and it must match `statio app add --image` (repo-equality). |
| `digest` | no | Deploy this exact digest and **skip building**. Leave empty to let the action build & push (`steps.build.outputs.digest` from your own build, or an old digest for rollback). |
| `dockerfile` | no | Dockerfile path when the action builds (default `Dockerfile`). |
| `context` | no | Build context when the action builds (default `.`). |
| `image-tag` | no | Tag to push the built image under (default `${{ github.sha }}`; the deployed reference is always the digest). |
| `sign` | no | cosign-sign the image (default `true`). Set `false` only if you sign it yourself. |
| `registry-username` / `registry-password` | no | Registry login when the action pushes. Default to the GitHub actor + `GITHUB_TOKEN` (GHCR). Set both for Docker Hub / other registries. |
| `env` | no | Per-deploy overrides, `KEY=${{ secrets.KEY }}` lines. GitHub masks them. |
| `statio-file` | no | Path to `statio.yaml` (default `statio.yaml`). |
| `ts-oauth-client-id` | yes | CI's `tag:ci` OAuth client id (the `STATIO_TS_OAUTH_CLIENT_ID` secret). |
| `ts-oauth-secret` | yes | CI's `tag:ci` OAuth client secret (the `STATIO_TS_OAUTH_SECRET` secret). |
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

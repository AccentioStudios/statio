---
title: GitHub Action
description: Inputs, permissions, and the workflow statio generates.
sidebar:
  order: 3
prev: false
next: false
---

statio publishes one Action on the Marketplace, `accentiostudios/statio@v1`. A single step builds and
pushes your image, signs it with cosign, installs the pinned `statio` binary, joins your tailnet as an
ephemeral `tag:ci` node, signs the deploy payload with the same identity, and sends the envelope to
your agent.

The `action.yml` lives at the root of
[`accentiostudios/statio`](https://github.com/accentiostudios/statio), the same repo that ships the
CLI, the agent and the releases.

## Permissions

The job needs all three:

```yaml
permissions:
  id-token: write   # keyless cosign signing. Without it the agent answers 403.
  packages: write   # push the image to GHCR. Also covers pulling a private image.
  contents: read    # checkout
```

## Minimal usage

```yaml
- uses: actions/checkout@v4
- uses: accentiostudios/statio@v1
  with:
    target: statio.your-tailnet.ts.net
    service: api
    image: ghcr.io/your-org/api
    ts-oauth-client-id: ${{ secrets.STATIO_TS_OAUTH_CLIENT_ID }}
    ts-oauth-secret: ${{ secrets.STATIO_TS_OAUTH_SECRET }}
    env: |
      DATABASE_URL=${{ secrets.DATABASE_URL }}
```

## Inputs

| Input | Required | Meaning |
|---|---|---|
| `target` | yes | The agent's MagicDNS host, such as `statio.your-tailnet.ts.net`. It is the signed audience, which binds the deploy to that server. |
| `service` | yes | The app slot, accepted on the server with `statio app add`. |
| `image` | yes | Your image repository. The action builds and pushes here, and it must equal the app's `--image`. |
| `ts-oauth-client-id` | yes | CI's `tag:ci` OAuth client id. |
| `ts-oauth-secret` | yes | CI's `tag:ci` OAuth client secret. |
| `digest` | no | Deploy this exact digest and skip the build. Use it for a rollback, or with your own build step. |
| `dockerfile` | no | Dockerfile path when the action builds. Default `Dockerfile`. |
| `context` | no | Build context when the action builds. Default `.`. |
| `image-tag` | no | Tag to push under. Default `${{ github.sha }}`. The deployed reference is always the digest. |
| `sign` | no | Sign the image with cosign. Default `true`. Set it to `false` only when you sign it yourself. |
| `registry-username` | no | Registry login when the action pushes. Defaults to the GitHub actor, for GHCR. |
| `registry-password` | no | Registry token when the action pushes. Defaults to `GITHUB_TOKEN`, for GHCR. |
| `env` | no | Per-deploy values, one `KEY=${{ secrets.KEY }}` per line. GitHub masks them. |
| `statio-file` | no | Path to `statio.yaml`. Default `statio.yaml`. |
| `statio-version` | no | Binary version to download. A bare major such as `v1`, an exact `vX.Y.Z`, or `latest`. |
| `timeout` | no | Deploy timeout. Default `5m`. |
| `strict` | no | Treat `success_degraded` as a failure. Default `false`. |
| `preflight` | no | Run the read-only readiness check first. Default `true`. |

The Action sets `deploy_seq` itself, from `github.run_number`.

## Preflight

The preflight step runs after the tailnet join and before the deploy, sending the same signed payload
to the agent's `/preflight` endpoint. It verifies the app's host port is free, and, when
`statio.yaml` asks for a public domain, that NPMplus is configured, reachable, and allows the domain.
It changes nothing. A failure stops the job before the deploy touches the server.

## Building elsewhere

Pass `digest: ${{ steps.build.outputs.digest }}` and the action skips the build, then signs and
deploys. Add `sign: false` when you sign the image in your own step too.

## Generated workflow

`statio init repo` writes this when the repo has no CI:

```yaml
name: deploy
on:
  push:
    branches: [main]
  workflow_dispatch:
    inputs:
      digest:
        description: "Existing image digest to redeploy. Use it to roll back."
        required: false

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
      - uses: accentiostudios/statio@v1
        with:
          target: statio.your-tailnet.ts.net
          service: api
          image: ghcr.io/your-org/api
          digest: ${{ inputs.digest }}
          ts-oauth-client-id: ${{ secrets.STATIO_TS_OAUTH_CLIENT_ID }}
          ts-oauth-secret: ${{ secrets.STATIO_TS_OAUTH_SECRET }}
          env: |
            DATABASE_URL=${{ secrets.DATABASE_URL }}
```

With CI already in place, `statio init repo` prints the step alone and leaves your file untouched.

## Versioning

`@v1` moves with every stable `1.x` release. Pin `@v0.2.6` for reproducible runs. The
`statio-version` input keeps the downloaded binary in step with the wire schema.

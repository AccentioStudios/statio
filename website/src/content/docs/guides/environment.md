---
title: Environment variables
description: How secrets flow from GitHub Secrets to your container — and what stays server-side.
sidebar:
  order: 2
---

Values live in **GitHub Secrets**; your `statio.yaml` only **routes** which key goes to which
service. The agent writes them to `/run/statio/<svc>/<service>.env` on **tmpfs (RAM)** and passes
them to the container — never to persistent disk.

## Declare keys, not values

```yaml
# statio.yaml (in your repo): declare the key NAMES per service
services:
  - name: api
    env: [DATABASE_URL, JWT_SECRET]        # values come from GitHub Secrets
    env_inline: { NODE_ENV: production }    # non-secret literals, committed here
```

The workflow maps each key to its secret (GitHub masks them in logs):

```yaml
with:
  env: |
    DATABASE_URL=${{ secrets.DATABASE_URL }}
    JWT_SECRET=${{ secrets.JWT_SECRET }}
```

- **Secret values** → GitHub Secrets, passed via the Action's `env:` input.
- **Non-secret config** → `env_inline` in `statio.yaml`.

## Ops-only secrets (server-side base)

A server-side base exists for secrets CI should never see:

```sh
sudo statio env set api OPS_ONLY --secret-stdin --protected   # CI cannot overwrite it
sudo statio env set api MUST_HAVE --required                  # CI must provide it, or 422
```

`--protected` keys can't be overridden by a deploy; `--required` keys must be supplied or the
deploy fails with `422`.

## At-rest honesty

The agent runs as root with access to `docker.sock`, so it's root-equivalent. CI values are
**RAM-only** and don't appear in logs or the response, but there is **no magic at-rest
encryption**: `docker inspect` shows them to local root. The real protection is that GitHub
Secrets is the store, the channel is signed, and nothing touches persistent disk. See the
[security model](/architecture/#6-security-model) for the full reasoning.

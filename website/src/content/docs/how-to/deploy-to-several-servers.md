---
title: Deploy to several servers
description: Send the same repo to staging and production, each with its own agent.
sidebar:
  order: 6
prev: false
next: false
---

statio works per server. Each server runs its own agent, with its own hostname, and CI picks one by
setting `target`.

## 1. Set up the second server

Run the same two steps you ran on the first:

```sh
curl -fsSL https://statio.accentio.dev/install.sh | sudo sh
sudo statio init server
sudo statio app add api
```

`init server` prints that agent's address. It differs from the first one.

## 2. Give the workflow a target per environment

```yaml
jobs:
  staging:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: accentiostudios/statio@v1
        with:
          target: statio-staging.your-tailnet.ts.net
          service: api
          image: ghcr.io/your-org/api
          ts-oauth-client-id: ${{ secrets.STATIO_TS_OAUTH_CLIENT_ID }}
          ts-oauth-secret: ${{ secrets.STATIO_TS_OAUTH_SECRET }}
```

Both jobs use the same CI OAuth client and the same image. Only `target` changes.

The agent compares `target` against its own hostname and rejects a payload addressed elsewhere with
`403 [audience]`, so a wrong target fails loudly instead of deploying to the wrong box.

## 3. Read each server's history

```sh
statio logs api --target statio-staging.your-tailnet.ts.net
statio logs api --target statio.your-tailnet.ts.net
```

Servers keep separate state and never coordinate.

---
title: Multiple services & servers
description: Run dependencies alongside your app, and deploy to more than one server.
sidebar:
  order: 4
---

## Multiple services

A single `statio.yaml` describes all the containers in a deployment: your app plus dependencies
like Postgres or Redis. Your app is the service **without** an `image:` (the signed digest is
injected); dependencies carry an `image:` pinned by digest from an allowlisted registry.

```yaml
services:
  - name: api                         # your app — no image:
    ports: [3000]
    env: [DATABASE_URL]
    depends_on: [db]
    health: { path: /health }
  - name: db                          # dependency
    image: postgres:16@sha256:...     # from an allowlisted registry; pin by digest
    env: [POSTGRES_PASSWORD]
    volumes:
      - { name: pgdata, path: /var/lib/postgresql/data }
```

Run `statio app add` once per app service on the server. Dependencies don't need it — they're
confined by the generated compose template and the registry allowlist. A service without `ports`
stays on the internal compose network only (e.g. Postgres is never published).

## Multiple servers

statio is per-server. Run `statio init server` on each one; every agent has its own hostname (which
is its signed `audience`) and CI chooses the `target`. Servers don't coordinate — the same repo can
deploy to staging and production by changing the Action's `target`.

## History & auditing

Each deploy appends a redacted record to the server-side audit log:

```sh
statio logs api                                    # timeline per deploy (on the server)
statio logs api --target statio.<tailnet>.ts.net   # remote, over the tailnet (redacted)
```

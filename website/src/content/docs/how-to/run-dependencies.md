---
title: Run a database alongside your app
description: Add Postgres, Redis or any other container to the same deployment.
sidebar:
  order: 6
---

One `statio.yaml` describes every container in a deployment. Your app is the service with no
`image:`, since the agent injects the signed digest. A dependency carries its own `image:`, pinned by
digest.

## Declare the dependency

```yaml
services:
  - name: api
    ports: [3000]
    env: [DATABASE_URL]
    depends_on: [db]
    health: { path: /health }
  - name: db
    image: postgres:16@sha256:...
    env: [POSTGRES_PASSWORD]
    volumes:
      - { name: pgdata, path: /var/lib/postgresql/data }
```

Rules the agent enforces:

- Pin the dependency by digest, from a registry on the server's allowlist. A registry outside it
  fails with `[registry_denied]`. Widen the list with `sudo statio app edit <app>`.
- A service with no `ports` stays on the internal compose network. Leave Postgres without `ports` and
  nothing on the host reaches it.
- Volumes are Docker-managed named volumes. You give a name and a path, never a host directory.

## Pass the dependency's secrets

`POSTGRES_PASSWORD` follows the same route as any other key. Declare the name here, then either map
it in the workflow or set it server-side:

```sh
sudo statio env set api POSTGRES_PASSWORD --secret-stdin --protected
```

## Reach it from your app

Compose puts every service of one deployment on the same network, so your app reaches the database by
service name:

```
DATABASE_URL=postgresql://app:secret@db:5432/appdb
```

## What you do not run

Run `statio app add` once per app, not per dependency. The generated compose template and the
registry allowlist confine dependencies already.

---
title: statio.yaml
description: Every field of the repo's deploy description, with its type and limits.
sidebar:
  order: 1
---

`statio.yaml` sits at the root of your repo and describes the containers of one deployment. The
parser rejects any field outside this schema, along with YAML anchors, aliases, and a second
document.

statio generates the compose file from this. A `docker-compose.yml` in your repo is ignored.

## Top level

| Field | Type | Required | Notes |
|---|---|---|---|
| `services` | list of [Service](#service) | yes | At least one, at most 20. The server may set a lower `max_services`. |
| `proxy` | [Proxy](#proxy) | no | Column 0, never inside a service. |
| `dns` | [DNS](#dns) | no | Column 0, never inside a service. |

## Service

| Field | Type | Notes |
|---|---|---|
| `name` | string | Required. Matches `^[a-z][a-z0-9-]{0,30}$`: lowercase, digits and dashes, first character a letter, 31 characters max. No underscores. |
| `image` | string | Omit it for your own app; the agent injects the verified digest. Set it for a dependency, pinned as `repo:tag@sha256:…`, from a registry on the server allowlist. |
| `ports` | list of int or [Port](#port) | The port your app listens on inside the container. statio allocates the host port. 20 per service max. |
| `env` | list of string | Key names only. Each matches `^[A-Z_][A-Z0-9_]{0,127}$`. |
| `env_inline` | map of string to string | Non-secret literals, committed. 4096 bytes per value. |
| `health` | [Health](#health) | Loopback probe. A failure rolls the deploy back. |
| `depends_on` | list of string | Names of other services in this file. |
| `volumes` | list of [Volume](#volume) | Docker-managed named volumes. 20 per service max. |
| `command` | list of string | Exec form only. 100 items, 1024 bytes each. |
| `restart` | string | Compose restart policy. |

### Port

A bare integer sets the container port and lets statio pick the host port:

```yaml
ports: [3000]
```

The long form takes the same fields explicitly:

| Field | Type | Notes |
|---|---|---|
| `container` | int | The port inside the container. |
| `host` | int | Defaults to `container`. statio owns the host side and publishes on `127.0.0.1` only. |
| `protocol` | string | Defaults to `tcp`. |

### Health

| Field | Type | Notes |
|---|---|---|
| `type` | string | Probe type. |
| `path` | string | Must start with `/`, 512 characters max. |
| `port` | int | Defaults to the service's first port. |
| `start_period` | duration | Digits plus `ms`, `s`, or `m`. |
| `interval` | duration | Same format. |
| `timeout` | duration | Same format. |
| `retries` | int | Attempts before the agent rolls back. |

### Volume

| Field | Type | Notes |
|---|---|---|
| `name` | string | Matches `^[a-z0-9][a-z0-9_-]{0,40}$`. A Docker-managed volume name, never a host path. |
| `path` | string | Mount point inside the container. |
| `read_only` | bool | Defaults to `false`. |

## Proxy

| Field | Type | Notes |
|---|---|---|
| `domain` | string | Required to enable the block. Must fall under a suffix the server allows. |
| `upstream` | string | Forward host. The agent computes this itself; leave it unset. |
| `upstream_port` | int | Forward port. The agent computes this itself; leave it unset. |
| `scheme` | string | Defaults to `http`. |
| `websockets` | bool | Defaults to `false`. |
| `http2` | bool | Defaults to `false`. |
| `hsts` | bool | Defaults to `false`. |
| `force_https` | bool | Defaults to `false`. |

## DNS

| Field | Type | Notes |
|---|---|---|
| `domain` | string | Required to enable the block. The agent writes an `A` record at your pinned public IP. |

## Fields that do not exist

`privileged`, `cap_add`, `network_mode`, `devices`, `sysctls`, host bind mounts, and every other
compose key outside this page. The parser rejects them, which is what stops a deploy from asking for
host privileges. See [how statio works](/explanation/how-statio-works/).

## Example

```yaml
services:
  - name: api
    ports: [3000]
    env: [DATABASE_URL, JWT_SECRET]
    env_inline: { NODE_ENV: production }
    depends_on: [db]
    health: { path: /health, retries: 5 }
  - name: db
    image: postgres:16@sha256:...
    env: [POSTGRES_PASSWORD]
    volumes:
      - { name: pgdata, path: /var/lib/postgresql/data }

proxy: { domain: api.example.com }
dns:   { domain: api.example.com }
```

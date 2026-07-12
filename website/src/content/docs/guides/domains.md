---
title: Add a domain
description: Wire a reverse proxy (NPMplus) and DNS (Cloudflare) into the same deploy.
sidebar:
  order: 1
---

statio can configure a reverse proxy (NPMplus) and a DNS record (Cloudflare) as part of a deploy.
The agent does it with its **own** credentials — the workflow never sees them.

## 1. Configure the integrations (server, once)

On the server, run the integrations wizard. It writes the settings straight into the agent config
(no hand-editing) and is **idempotent** — re-run it any time to change a single detail and it
pre-fills every answer with the current value:

```sh
sudo statio init integrations    # asks for NPMplus and Cloudflare, step by step
```

For NPMplus it asks two separate things, because they are two separate network paths:

- **Admin API URL (control plane)** — how the *agent* reaches NPMplus to configure it. The agent
  runs on the host, so this is almost always host loopback: `http://127.0.0.1:81`.
- **Data-plane mode** — how *NPMplus* reaches your *app*. You pick the topology:

  | Mode | How NPMplus reaches your app | When |
  |---|---|---|
  | **loopback** (default) | forwards to `127.0.0.1:<port>` on the host | NPMplus runs on the host network — the common case |
  | **container** | forwards to your app by its **slot name** (a unique network alias statio assigns) over a shared docker network | NPMplus and your app run in the same docker network |

  In **container** mode it also asks for the shared docker **network** name (e.g. `statio-edge`).
  NPMplus must already be attached to it; statio adds your app to it on deploy.

statio works in **both** topologies. You choose; the agent always computes the forward target
itself, so a deploy can never point the proxy anywhere else.

:::note[statio owns the host port]
You declare the port your app **listens on** (`ports: [3000]`); statio allocates a stable host
port per app and binds it on `127.0.0.1` only. Two apps can never collide on the same host port —
the silent "port is already allocated" crash is gone. The port is stable across redeploys, so a
proxy entry never breaks under it.
:::

## 2. Allow the domain when you enable the service

The allowed domain is a **server-side anchor**. In `sudo statio app add`, answer **yes** to "Expose
a public domain?" and enter the suffix. The non-interactive form:

```sh
sudo statio app add api --image ghcr.io/accentiostudios/api --repo accentiostudios/api \
  --proxy-domain-suffix example.com \
  --dns-domain-suffix example.com
```

A domain is only accepted if it falls under an allowed suffix (anti-hijack).

## 3. Declare the domain in your statio.yaml

You only declare the **domain** — the agent resolves the forward target (the loopback port, or your
app's container name) from the server-side mode:

```yaml
proxy: { domain: api.example.com }
dns:   { domain: api.example.com }
```

On the next deploy, the agent creates or updates the proxy host in NPMplus and the DNS A record
pointing at your server. If NPMplus or Cloudflare fail, the deploy still stays healthy (state
`success_degraded`) and converges on retry — a blip in the edge never takes down a healthy
container.

## 4. Check it before you deploy (preflight)

The Action runs a **preflight** before every deploy (`preflight: true` by default): a read-only
check that fails fast — *without changing anything* — if the host port is taken by another process,
or if your `statio.yaml` asks for a public domain the server isn't set up for (NPMplus unconfigured
or unreachable, or the domain not allowlisted). Run it by hand from CI with:

```sh
statio preflight --target <agent> --service api --image ghcr.io/org/api --digest sha256:…
```

A failing check is a non-zero exit with the exact reason and a hint, so you fix the server config
before the deploy ever mutates it.

:::note
The DNS record type is forced to `A` and its target is your server's pinned public IP — an event
can never repoint DNS off your own address. The TLS cert is issued in NPMplus (Let's Encrypt).
:::

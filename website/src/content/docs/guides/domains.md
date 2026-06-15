---
title: Add a domain
description: Wire a reverse proxy (NPMplus) and DNS (Cloudflare) into the same deploy.
sidebar:
  order: 1
---

statio can configure a reverse proxy (NPMplus) and a DNS record (Cloudflare) as part of a deploy.
The agent does it with its **own** credentials — the workflow never sees them.

## 1. Configure the integrations (server, once)

On the server, run the integrations wizard and paste the lines it prints into
`/etc/statio/config.yaml`:

```sh
sudo statio init integrations    # asks for NPMplus and Cloudflare, step by step
```

## 2. Allow the domain when you enable the service

The allowed domain is a **server-side anchor**. In `sudo statio app add`, answer **yes** to "Expose
a public domain?" and enter the suffix. The non-interactive form:

```sh
sudo statio app add api --image ghcr.io/accentiostudios/api --repo accentiostudios/api \
  --proxy-domain-suffix example.com --proxy-upstream api \
  --dns-domain-suffix example.com
```

A domain is only accepted if it falls under an allowed suffix (anti-hijack).

## 3. Declare the domain in your statio.yaml

```yaml
proxy: { domain: api.example.com, upstream: api, upstream_port: 3000 }
dns:   { domain: api.example.com }
```

On the next deploy, the agent creates or updates the proxy host in NPMplus and the DNS A record
pointing at your server. If NPMplus or Cloudflare fail, the deploy still stays healthy (state
`success_degraded`) and converges on retry — a blip in the edge never takes down a healthy
container.

:::note
The DNS record type is forced to `A` and its target is your server's pinned public IP — an event
can never repoint DNS off your own address. The TLS cert is issued in NPMplus (Let's Encrypt).
:::

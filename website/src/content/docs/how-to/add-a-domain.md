---
title: Add a domain
description: Serve an app over HTTPS through NPMplus, with a Cloudflare DNS record.
sidebar:
  order: 3
---

The agent configures the reverse proxy and the DNS record during a deploy, with credentials you give
it once. The workflow never sees them.

## 1. Configure the integrations on the server

```sh
sudo statio init integrations
```

The wizard writes both settings into the agent config. Re-run it any time to change one value: it
fills every answer with the current one.

For NPMplus it asks two separate things:

- **Admin API URL**, how the agent reaches NPMplus. The agent runs on the host, so this is loopback:
  `https://127.0.0.1:81`.
- **Data-plane mode**, how NPMplus reaches your app:

  | Mode | Forward target | Pick it when |
  |---|---|---|
  | `loopback` (default) | `127.0.0.1:<port>` on the host | NPMplus runs on the host network |
  | `container` | your app's slot name over a shared docker network | NPMplus and your app share a docker network |

In `container` mode the wizard also asks for the shared network name, such as `statio-edge`. Attach
NPMplus to that network yourself; statio attaches your app on deploy.

The agent reads its config at boot, so accept the restart the wizard offers at the end.

## 2. Allow the domain suffix

The server decides which domains an app may claim. Run the app wizard and answer **yes** to
"Expose a public domain?", or pass the suffixes as flags:

```sh
sudo statio app edit api --proxy-domain-suffix example.com --dns-domain-suffix example.com
```

The agent rejects a deploy that asks for a domain outside the allowed suffix.

## 3. Declare the domain in statio.yaml

```yaml
proxy: { domain: api.example.com }
dns:   { domain: api.example.com }
```

Both blocks sit at the root of the file, at column 0, never inside a service.

## 4. Push

On the next deploy the agent creates or updates the proxy host in NPMplus and the A record in
Cloudflare, after the container passes its health check.

## 5. Issue the TLS certificate

statio creates the proxy host. Request the certificate in the NPMplus UI, under that host's SSL tab,
once the first deploy has created it.

## Check it before you deploy

The Action runs a read-only preflight before every deploy. Run the same check by hand:

```sh
statio preflight --target statio.your-tailnet.ts.net --service api \
  --image ghcr.io/your-org/api --digest sha256:…
```

It fails with `[proxy_unconfigured]` when the server has no NPMplus, and with `[proxy_unreachable]`
when it cannot reach it. See [error codes](/reference/error-codes/).

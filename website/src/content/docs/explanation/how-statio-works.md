---
title: How statio works
description: The two halves, the one contract between them, and why a deploy carries data instead of scripts.
sidebar:
  order: 2
---

statio is one binary that plays two roles. On your server it runs as an agent under systemd. On a
GitHub runner it runs as a client that builds, signs and sends. Both halves live in one repo and
share one Go codebase.

| Half | Where it runs | What it is |
|---|---|---|
| Server | your server | `statio agent run` under systemd, plus `statio init` and `statio env` |
| Action | a GitHub Actions runner | `action.yml` at the repo root, which downloads the same binary and runs `statio deploy` |

## One contract, one implementation

The two halves speak the `statio/v1` contract, defined once in `internal/spec`. The agent validates a
payload as the authority. The client validates the same payload before sending, to fail fast in CI.
Both run the same code, so the contract cannot drift between them. A schema change lands in one place
and both sides get it.

That is also why the Action pins a binary version: the wire schema and the binary travel together.

## A deploy carries data

The message CI sends describes what to run. It contains a service name, an image digest, key names
and values, a domain, and a few binding fields. Every field is a scalar, a boolean, an enum, or a map
of literals.

Nothing in it reaches a command, a shell, a template, a URL, a host, or a path position. The agent
decodes it with unknown fields rejected, then builds a compose file from a fixed template of its own.
The dangerous compose keys, `privileged`, `cap_add`, `network_mode`, host bind mounts, `devices`,
`sysctls`, have no field in the schema, so a payload cannot ask for them.

A deploy is therefore not remote code execution. It is a request to run one image, by digest, under
constraints the server already agreed to.

## Where authority lives

Two lists divide the system. What the event carries, and what the server holds.

| Carried by the event | Held by the server |
|---|---|
| service name, image digest, service definitions, env values, proxy and dns domains, binding fields | allowed image repository, cosign signer, env base and protected keys, registry and domain allowlists, NPMplus credentials, Cloudflare token and zone, public IP, DNS record type |

The event picks among things the server already allows. It never widens them. A deploy cannot create
an app, point DNS at another address, or pull from a registry the server never approved.

The [security model](/explanation/security-model/) covers the invariants that hold this in place. The
[wire contract](/reference/wire-contract/) documents the message field by field.

## What runs where

The agent talks to Docker over `docker.sock`, to NPMplus over loopback, and to Cloudflare over
HTTPS. It holds every credential for those. The workflow holds none of them, which is why adding a
domain changes nothing in your repo beyond two lines of YAML.

For the network paths and the reasoning behind them, see
[why Tailscale instead of SSH](/explanation/why-tailscale-not-ssh/).

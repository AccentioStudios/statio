---
title: Why Tailscale instead of SSH
description: What the private network carries, what it does not, and the constraints behind the choice.
sidebar:
  order: 2
prev:
  link: /explanation/how-statio-works/
  label: How statio works
next:
  link: /explanation/security-model/
  label: Security model
---

Most self-hosted deploys end with a CI job holding an SSH key to a production server. That key is a
standing credential with a shell behind it. Anyone who reads it owns the box.

statio replaces that key with a network boundary. CI joins a private Tailscale network as an
ephemeral node, sends one signed message, and disappears. The server exposes its deploy endpoint on
that network only.

## Four constraints

Every design decision in statio preserves these:

1. No SSH.
2. The agent listens on the tailnet address only, never on a public interface.
3. No broker of our own. The central pieces belong to third parties: the registry, the Tailscale
   control plane, the Sigstore transparency log.
4. No polling. The agent receives a push.

The last one matters for cost and for latency. A polling agent asks a server every few seconds
whether anything changed, which turns a deploy into a wait. A pushed deploy starts the moment CI
signs it.

## Tailscale carries one thing

The private network carries the deploy signal. It carries nothing else.

| Path | Transport | Direction |
|---|---|---|
| CI to agent, the deploy signal | Tailscale, WireGuard | inbound to the agent, on the tailnet only |
| Agent to the registry, the image pull | HTTPS | outbound |
| Users to your app | HTTP and HTTPS through the reverse proxy on 80 and 443 | inbound to the proxy |

Your app's public traffic takes the third path, the ordinary one, the same path it would take with no
statio at all. statio runs no `tailscale serve`. Your users need no Tailscale account.

The agent embeds Tailscale rather than shelling out to it. The binary imports `tailscale.com/tsnet`,
which is a full node in userspace, so the server needs no `tailscaled` daemon and no Tailscale CLI.
Node state lives in `/var/lib/statio/tsnet`. Your containers stay off the tailnet, since they have no
reason to be on it: the agent reaches the proxy over loopback and pulls images over normal HTTPS.

## Two clients, not one

CI and the agent join with separate OAuth clients, carrying separate tags.

The agent's client owns `tag:agent` and holds two scopes: `auth_keys` to mint the key it joins with,
and `devices:core` to register itself as a lasting node. CI's client owns `tag:ci` and holds
`auth_keys` alone.

A compromised workflow therefore cannot register a node as `tag:agent`. CI can reach the agent and
nothing more. That separation is why one CI client serves every repo in an organisation safely: the
credential grants reach, never deploy rights. The
[cosign signer](/explanation/security-model/) decides what a repo may deploy.

The OAuth client secret has no expiry. Only the access token it mints is short-lived, and the client
refreshes that on its own. To cut CI off, delete the `tag:ci` client in the Tailscale console.

## What Tailscale does not protect

The network is a boundary, not a trust anchor. A payload that arrives over the tailnet is still
unsigned bytes until the agent verifies its cosign bundle. If Tailscale were the only control, anyone
who joined the tailnet could deploy. The signature is what makes the deploy safe; the network is what
makes the port unnecessary.

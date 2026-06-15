<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)"  srcset="statio-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="statio-light.svg">
    <img alt="Statio" src="statio-light.svg" width="96">
  </picture>
</p>

<p align="center">Deploy to your own server with a <code>git push</code> — no SSH, no open ports.</p>

<p align="center">
  <a href="https://statio.accentio.dev/"><b>Documentation</b></a> ·
  <a href="https://statio.accentio.dev/getting-started/">Getting started</a> ·
  <a href="https://statio.accentio.dev/architecture/">Architecture</a>
</p>

---

## Introduction

**Statio** ships your Docker image to your own server with a `git push`. Your GitHub Actions
workflow builds and signs the image; a lightweight **agent** on your server receives it, verifies
the signature, and recreates the container.

```
git push ─▶ CI: build + sign ─▶ agent on your server ─▶ deployed
```

> **Tailscale is only the deploy channel.** CI reaches the agent over a private Tailscale network —
> this replaces SSH, so the agent opens no inbound deploy port. statio does **not** serve your app
> over Tailscale (no `tailscale serve`): public traffic goes the normal way, through a reverse proxy
> (NPMplus) on `80/443`.

What you get:

- **No SSH and no deploy port.** The deploy channel rides a private Tailscale network; the agent
  isn't reachable from the internet.
- **Only what your CI signed gets deployed.** Cosign keyless verification before anything runs.
- **Domain and DNS in the same deploy.** Reverse proxy (NPMplus) and Cloudflare, optional.
- **Just another GitHub Actions step.** No brittle deploy scripts.

> Full docs, guides and the security model live at
> **[statio.accentio.dev](https://statio.accentio.dev/)**.

---

## Installation

On the server, a single command:

```sh
curl -fsSL https://statio.accentio.dev/install.sh | sudo sh
```

It detects your OS/arch, downloads the binary from GitHub Releases, verifies the checksum, and
installs it to `/usr/local/bin/statio`.

**Updating:** run the same command again (it only updates when a newer version exists) or use the
built-in self-update:

```sh
statio upgrade            # download + verify the checksum, replace the binary, restart the agent
statio upgrade --check    # only report whether a new version exists
```

On a server, `statio upgrade` (and re-running the installer) restart the `statio-agent` service
automatically when it's running, so the new binary takes effect right away — pass `--no-restart` to
skip it. The CLI also nudges you when a new version is available. To diagnose your environment:
`statio doctor`.

You also need **Docker** on the server and a **Tailscale** account (the free plan is enough). In
CI you install nothing: the Action downloads the binary.

---

## Quick Start

Setup touches **two places**. Each command is tagged:

- 🖥️ **On your server** — the Linux VPS, over SSH, as root.
- 💻 **On your machine** — inside your project's repo.

### Step 0 · Tailscale (once, on the web)

Two steps, in order — the OAuth client can only own tags that already exist.

**1. Define the tags.** Under *Access controls*, paste this ACL (creates `tag:agent` + `tag:ci`,
lets only `tag:ci` reach the agent on one port):

```json
{
  "tagOwners": { "tag:agent": ["autogroup:admin"], "tag:ci": ["autogroup:admin"] },
  "acls": [ { "action": "accept", "src": ["tag:ci"], "dst": ["tag:agent:443"] } ],
  "ssh": []
}
```

**2. Create one OAuth client** at *Settings → OAuth clients → Generate* (newer consoles: *Trust
credentials → New credential*). Pick **Custom scopes** and enable, both **Write**: `auth_keys`
(**Keys → Auth Keys**) and `devices:core` (**Devices → Core**). Enabling *Devices → Core* makes
Tailscale ask for **tags** — pick `tag:agent` and `tag:ci`. Copy its **client id** + **secret** for
`init server`. The server uses it to join the tailnet *and* to mint the `tag:ci` key CI needs — you
never create that key by hand. ([Full step-by-step with the exact UI](https://statio.accentio.dev/getting-started/#step-0--tailscale-once-on-the-web).)

### On your server 🖥️

```sh
sudo statio init server     # configure + start the agent, mint the shared CI auth key (paste the OAuth client)
sudo statio app add api     # accept an app: image repo + its GitHub signer + domains
```

`init server` enables and starts the `statio-agent` service for you, then prints a
`gh secret set STATIO_TS_AUTHKEY …` command (one secret for all repos). `app add` accepts each app —
apps can come from different repos/orgs, each pinning its own signer. Both are interactive wizards —
run them without flags.

### On your machine 💻

```sh
statio init repo            # creates statio.yaml + prints the workflow step to add
git push                    # CI builds, signs, and deploys
```

Set the secrets the workflow needs (`gh secret set STATIO_TS_AUTHKEY …` and your app's env), then push.

> The full step-by-step — including the workflow snippet, domains, environment variables and
> rollback — is in the [Getting started guide](https://statio.accentio.dev/getting-started/).

---

## Documentation

- **[Getting started](https://statio.accentio.dev/getting-started/)** — the full setup walkthrough.
- **[Guides](https://statio.accentio.dev/guides/domains/)** — domains, environment variables, rollback, multiple services.
- **[Reference](https://statio.accentio.dev/reference/github-action/)** — the GitHub Action inputs and the CLI commands.
- **[Architecture](https://statio.accentio.dev/architecture/)** — the security model, deploy pipeline and wire contract.
- **[Contributing](CONTRIBUTING.md)** — how to build, test and propose changes.

---

<p align="center"><sub>Made by accentiostudios.</sub></p>

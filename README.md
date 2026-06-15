<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)"  srcset="statio-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="statio-light.svg">
    <img alt="Statio" src="statio-light.svg" width="96">
  </picture>
</p>

<p align="center">Deploy to your own server with a <code>git push</code> — no SSH, no open ports.</p>

<p align="center">
  <a href="https://accentiostudios.github.io/statio/"><b>Documentation</b></a> ·
  <a href="https://accentiostudios.github.io/statio/getting-started/">Getting started</a> ·
  <a href="https://accentiostudios.github.io/statio/architecture/">Architecture</a>
</p>

---

## Introduction

**Statio** ships your Docker image to your own server with a `git push`. Your GitHub Actions
workflow builds and signs the image; a lightweight **agent** on your server —connected over a
private Tailscale network— receives it, verifies the signature, and recreates the container.

```
git push ─▶ CI: build + sign ─▶ statio/action ─▶ agent on your server ─▶ deployed
```

What you get:

- **No SSH and no open ports.** The server exposes nothing to the internet.
- **Only what your CI signed gets deployed.** Cosign keyless verification before anything runs.
- **Domain and DNS in the same deploy.** Reverse proxy (NPMplus) and Cloudflare, optional.
- **Just another GitHub Actions step.** No brittle deploy scripts.

> Full docs, guides and the security model live at
> **[accentiostudios.github.io/statio](https://accentiostudios.github.io/statio/)**.

---

## Installation

On the server, a single command:

```sh
curl -fsSL https://raw.githubusercontent.com/accentiostudios/statio/main/install.sh | sudo sh
```

It detects your OS/arch, downloads the binary from GitHub Releases, verifies the checksum, and
installs it to `/usr/local/bin/statio`.

**Updating:** run the same command again (it only updates when a newer version exists) or use the
built-in self-update:

```sh
statio upgrade            # download the latest, verify the checksum, replace the binary
statio upgrade --check    # only report whether a new version exists
```

After updating on the server, restart the agent: `sudo systemctl restart statio-agent`. The CLI
also nudges you when a new version is available. To diagnose your environment: `statio doctor`.

You also need **Docker** on the server and a **Tailscale** account (the free plan is enough). In
CI you install nothing: the Action downloads the binary.

---

## Quick Start

Setup touches **two places**. Each command is tagged:

- 🖥️ **On your server** — the Linux VPS, over SSH, as root.
- 💻 **On your machine** — inside your project's repo.

### Step 0 · Tailscale (once, on the web)

In the [Tailscale admin console](https://login.tailscale.com/admin/settings/oauth) create **two
OAuth clients**: one tagged `tag:agent` (for the server) and one tagged `tag:ci` (for CI). Paste
this ACL under *Access controls*:

```json
{
  "tagOwners": { "tag:agent": ["autogroup:admin"], "tag:ci": ["autogroup:admin"] },
  "acls": [ { "action": "accept", "src": ["tag:ci"], "dst": ["tag:agent:443"] } ],
  "ssh": []
}
```

This is the only manual step: only `tag:ci` can talk to the agent, and only on one port.

### On your server 🖥️

```sh
sudo statio init server     # configure the agent (signing identity, Tailscale)
sudo statio enable          # accept a service and pin its security anchors
sudo systemctl daemon-reload && sudo systemctl enable --now statio-agent
```

Both `init server` and `enable` are interactive wizards — run them without flags.

### On your machine 💻

```sh
statio init repo            # creates statio.yaml + prints the workflow step to add
git push                    # CI builds, signs, and deploys
```

Configure the secrets the workflow needs (`gh secret set TS_OAUTH_CLIENT_ID …`), then push.

> The full step-by-step — including the workflow snippet, domains, environment variables and
> rollback — is in the [Getting started guide](https://accentiostudios.github.io/statio/getting-started/).

---

## Documentation

- **[Getting started](https://accentiostudios.github.io/statio/getting-started/)** — the full setup walkthrough.
- **[Guides](https://accentiostudios.github.io/statio/guides/domains/)** — domains, environment variables, rollback, multiple services.
- **[Reference](https://accentiostudios.github.io/statio/reference/github-action/)** — the GitHub Action inputs and the CLI commands.
- **[Architecture](https://accentiostudios.github.io/statio/architecture/)** — the security model, deploy pipeline and wire contract.
- **[Contributing](CONTRIBUTING.md)** — how to build, test and propose changes.

---

<p align="center"><sub>Made by accentiostudios.</sub></p>

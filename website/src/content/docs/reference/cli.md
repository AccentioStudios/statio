---
title: CLI
description: The statio command-line reference.
sidebar:
  order: 2
---

`statio --help` groups the commands the same way this page does — **Setup**, **Apps & config**,
**Operate**, **Maintenance**, and **Internal** (run by systemd & the Action, not by hand).

```sh
# Setup — on the server, then in your repo
statio init server          # wizard: configure + start the agent (CI joins with its own tag:ci OAuth client)
statio init integrations    # wizard: NPMplus + Cloudflare + public IP
statio init repo            # wizard: statio.yaml + how to call the Action

# Apps & config — on the server
statio app add [name]       # wizard: accept an app — image repo, signer identity, domains
statio app list             # list apps; pick one to view its config + setup steps, or edit it
statio app edit <name>      # re-run the wizard (current values pre-filled) to change an app
statio app rm <name>        # stop accepting an app's deploys
statio env set <svc> KEY=VALUE [--protected] [--required]
statio env set <svc> KEY --secret-stdin          # ops secret via stdin
statio env list <svc>
statio env rm  <svc> KEY

# Operate — from a machine ON the tailnet (your laptop or CI), NOT the server
statio status --target <agent-host>              # the agent's health + the apps it accepts
statio logs <svc> [--target <agent-host>]        # deploy audit log (local file, or a remote agent)

# Maintenance
statio upgrade [--check] [--no-restart]          # self-update (verifies the checksum)
statio doctor [--fix]                            # environment diagnostics (and safe auto-fixes)
statio version                                   # or: statio --version

# Internal — run by systemd & the GitHub Action, not by hand
statio agent run --config <path>
statio deploy ...
```

The wizards (`init server`, `init integrations`, `init repo`, `app add`, `app edit`) are
interactive: run them without flags and they guide you. `app list` is interactive too — it lets you
pick an app and then view its config (with the workflow snippet and secrets) or re-run the wizard to
edit it. In CI/scripts they accept flags and secrets via `--*-stdin`; the
Action uses the flag form automatically.

### Private images

Nothing to configure on the server. The agent has **no GitHub identity**, so the Action forwards the
run's short-lived token (the same `GITHUB_TOKEN` it pushes with) inside the signed deploy; the agent
uses it only to read the image's cosign signature and pull it, in memory, then discards it. Keep
`packages: write` in the workflow `permissions`. The token is per-run and expires when the job ends —
no credential is ever stored on the server.

## Checking a running agent

Two different checks, for two different places:

- **On the server** → `sudo statio doctor`. The server's own OS is **not** a tailnet peer (the agent
  runs in userspace via tsnet), so you can't reach the agent from the server itself — `doctor` checks
  it locally instead (service state, config, the agent's last log line if it's down).
- **From your laptop or CI** (a machine **on the tailnet**) → `statio status --target <agent-host>`
  queries the agent's `/status` over the tailnet and prints its health and the apps it accepts. The
  `<agent-host>` is the agent's MagicDNS name (e.g. `statio.your-tailnet.ts.net`) — the address
  `init server` printed. `statio logs <svc> --target <agent-host>` fetches a service's deploy history
  the same way.

## Self-update & diagnostics

- `statio upgrade` downloads the latest release, verifies its sha256 against `checksums.txt`,
  replaces the running binary in place, and restarts the `statio-agent` service when it's active
  (so the new binary takes effect immediately). `--no-restart` skips that; `--check` only reports
  whether a newer version exists.
- `statio doctor` checks your environment: binary version vs latest, Docker
  daemon, git, gh **and whether it's logged in**, cosign (only
  relevant in CI), the agent config **and the secret files it references**, the **state dir** and the
  service, and GitHub reachability. It runs the *same secret-file check the agent runs at boot*, so a
  missing or world-readable secret is caught here instead of as a silent crash-loop — and when the
  service is down it prints the agent's last log line (read as root) so you see *why*. On a server run
  it with **`sudo statio doctor`** for the full picture — the config, the secret files and the service
  all need root, while the gh check is still done as the user you sudo from. `statio doctor --fix` resolves what it safely can on its own (create a missing state dir,
  tighten a loose secret's perms, restart a crash-looping agent) and tells you which remaining fixes
  need a `sudo` re-run. A *missing* secret it can't fabricate — it points you at the `init` step that
  regenerates it.
- The CLI also nudges you when a newer version exists. Disable it with `STATIO_NO_UPDATE_CHECK=1`.

## Server install & update

```sh
curl -fsSL https://statio.accentio.dev/install.sh | sudo sh
```

Re-running the installer updates only when a newer version exists, and restarts the `statio-agent`
service when it's running. `STATIO_VERSION=vX.Y.Z` pins a version; `STATIO_BINDIR=/usr/bin` changes
the install dir.

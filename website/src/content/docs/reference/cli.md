---
title: CLI
description: The statio command-line reference.
sidebar:
  order: 2
---

```sh
statio init server          # wizard: configure the agent
statio init integrations    # wizard: NPMplus + Cloudflare + public IP
statio init repo            # wizard: statio.yaml + how to call the Action
statio enable [svc]         # wizard: accept a service and pin its anchors

statio env set <svc> KEY=VALUE [--protected] [--required]
statio env set <svc> KEY --secret-stdin          # ops secret via stdin
statio env list <svc>
statio env rm  <svc> KEY

statio deploy ...           # used by the Action (not by hand)
statio logs <svc> [--target HOST]                # audit log (local or remote)
statio status --target HOST                      # agent status
statio upgrade [--check]                         # self-update (verifies the checksum)
statio doctor                                    # environment diagnostics
statio version                                   # or: statio --version
```

The wizards (`init server`, `init integrations`, `init repo`, `enable`) are interactive: run them
without flags and they guide you. In CI/scripts they accept flags and secrets via `--*-stdin`; the
Action uses the flag form automatically.

## Self-update & diagnostics

- `statio upgrade` downloads the latest release, verifies its sha256 against `checksums.txt`, and
  replaces the running binary in place. `statio upgrade --check` only reports whether a newer
  version exists. After updating on a server, restart the agent:
  `sudo systemctl restart statio-agent`.
- `statio doctor` checks your environment (binary version vs latest, Docker, git, gh, cosign, the
  agent config and service, GitHub reachability) — like `flutter doctor`.
- The CLI also nudges you when a newer version exists. Disable it with `STATIO_NO_UPDATE_CHECK=1`.

## Server install & update

```sh
curl -fsSL https://statio.accentio.dev/install.sh | sudo sh
```

Re-running the installer updates only when a newer version exists. `STATIO_VERSION=vX.Y.Z` pins a
version; `STATIO_BINDIR=/usr/bin` changes the install dir.

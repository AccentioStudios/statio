---
title: CLI
description: The statio command-line reference.
sidebar:
  order: 2
---

```sh
statio init server          # wizard: configure the agent + mint the shared CI auth key
statio init integrations    # wizard: NPMplus + Cloudflare + public IP
statio init repo            # wizard: statio.yaml + how to call the Action

statio app add [name]       # wizard: accept an app — image repo, signer identity, domains
statio app list             # list the accepted apps
statio app rm <name>        # stop accepting an app's deploys

statio env set <svc> KEY=VALUE [--protected] [--required]
statio env set <svc> KEY --secret-stdin          # ops secret via stdin
statio env list <svc>
statio env rm  <svc> KEY

statio deploy ...           # used by the Action (not by hand)
statio logs <svc> [--target HOST]                # audit log (local or remote)
statio status --target HOST                      # agent status
statio upgrade [--check] [--no-restart]          # self-update (verifies the checksum)
statio doctor                                    # environment diagnostics
statio version                                   # or: statio --version
```

The wizards (`init server`, `init integrations`, `init repo`, `app add`) are interactive: run them
without flags and they guide you. In CI/scripts they accept flags and secrets via `--*-stdin`; the
Action uses the flag form automatically. (`statio enable` is a deprecated alias of `statio app add`.)

## Self-update & diagnostics

- `statio upgrade` downloads the latest release, verifies its sha256 against `checksums.txt`,
  replaces the running binary in place, and restarts the `statio-agent` service when it's active
  (so the new binary takes effect immediately). `--no-restart` skips that; `--check` only reports
  whether a newer version exists.
- `statio doctor` checks your environment (binary version vs latest, Docker, git, gh, cosign, the
  agent config and service, GitHub reachability) — like `flutter doctor`.
- The CLI also nudges you when a newer version exists. Disable it with `STATIO_NO_UPDATE_CHECK=1`.

## Server install & update

```sh
curl -fsSL https://statio.accentio.dev/install.sh | sudo sh
```

Re-running the installer updates only when a newer version exists, and restarts the `statio-agent`
service when it's running. `STATIO_VERSION=vX.Y.Z` pins a version; `STATIO_BINDIR=/usr/bin` changes
the install dir.

---
title: Install and upgrade statio
description: Install the binary on a server, update it, and check the environment.
sidebar:
  order: 8
prev: false
next: false
---

## Install

```sh
curl -fsSL https://statio.accentio.dev/install.sh | sudo sh
```

The installer detects your OS and architecture, downloads the binary from GitHub Releases, verifies
its checksum, and writes it to `/usr/local/bin/statio`.

Two variables change what it does:

```sh
STATIO_VERSION=v0.2.6 curl -fsSL https://statio.accentio.dev/install.sh | sudo sh   # pin a version
STATIO_BINDIR=/usr/bin curl -fsSL https://statio.accentio.dev/install.sh | sudo sh  # change the target dir
```

## Upgrade

```sh
sudo statio upgrade
```

It downloads the latest release, verifies its sha256 against `checksums.txt`, replaces the running
binary, and restarts the `statio-agent` service when that service is active.

```sh
sudo statio upgrade --check        # report the available version and exit
sudo statio upgrade --no-restart   # replace the binary, leave the service alone
```

Re-running the installer does the same thing and upgrades only when a newer version exists.

The CLI checks for a newer release once a day and prints a line when it finds one. Silence it with
`STATIO_NO_UPDATE_CHECK=1`.

## Check the environment

```sh
sudo statio doctor
```

It reports the binary version against the latest release, the Docker daemon and its registry login,
git, `gh` and its login, cosign, the agent config and the secret files it points at, the state
directory, the service, and GitHub reachability. When the service is down it prints the agent's last
log line.

```sh
sudo statio doctor --fix
```

`--fix` creates a missing state directory, tightens loose permissions on a secret file, and restarts
a crash-looping agent. It cannot recreate a missing secret; for that it names the `init` command to
re-run.

Run it with `sudo` on a server. The config, the secret files and the service status all need root.
Without `sudo` it checks what it can and names what it skipped.

## Upgrade the Action

The workflow pins `accentiostudios/statio@v1`, a tag that moves with every stable `1.x` release, so
CI picks up new versions on its own. Pin an exact release with `@v0.2.6` when you want reproducible
runs, and keep the `statio-version` input in step with it.

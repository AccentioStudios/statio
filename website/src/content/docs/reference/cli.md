---
title: CLI
description: Every statio command, where you run it, and its flags.
sidebar:
  order: 2
prev: false
next: false
---

`statio --help` groups the commands the way this page does.

Setup and app commands run on the server, as root. `status` and `logs --target` run from a machine on
the tailnet, such as your laptop or a CI runner. The server's own OS is not a tailnet peer, since the
agent runs Tailscale in userspace, so `statio status` cannot query the agent from the server itself.
Use `statio doctor` there.

## Setup

| Command | Where | What it does |
|---|---|---|
| `statio init server` | server | Writes the agent config and the systemd unit, then enables and starts `statio-agent`. |
| `statio init integrations` | server | Configures NPMplus, Cloudflare, and the pinned public IP. Idempotent. |
| `statio init repo` | your repo | Writes `statio.yaml` and prints or generates the workflow. |

### statio init server

| Flag | Default | Meaning |
|---|---|---|
| `--hostname` | detected | The tsnet MagicDNS hostname. |
| `--ts-oauth-client-id` | | The agent's Tailscale OAuth client id. |
| `--ts-oauth-secret-stdin` | `false` | Read the client secret from stdin. |
| `--ts-oauth-secret-file` | | Read the client secret from a file. |
| `--issuer` | | The cosign OIDC issuer. |

### statio init repo

| Flag | Default | Meaning |
|---|---|---|
| `--target` | | The agent's MagicDNS host, for the printed snippet. |
| `--service` | | The service name. |
| `--image` | | The image repository. |
| `--branch` | `main` | The deploy branch, for the printed cosign identity. |
| `--workflow` | `deploy.yml` | The workflow file name, for the printed identity. |
| `--create-workflow` | `false` | Generate a starter `deploy.yml` when none exists. |
| `--out` | `.github/workflows/deploy.yml` | Where to write it. |
| `--statio-out` | `statio.yaml` | Where to write the starter `statio.yaml`. |
| `--action-ref` | `accentiostudios/statio@v1` | The Action ref used in the snippet. |

`init repo` never edits an existing workflow. It prints the step for you to paste.

## Apps and config, on the server

| Command | What it does |
|---|---|
| `statio app add [name]` | Accepts an app: its image repository, its cosign signer, its allowed domains. |
| `statio app list` | Lists accepted apps, then lets you view one's config or edit it. |
| `statio app edit <name>` | Re-runs the wizard with the current values filled in. |
| `statio app rm <name>` | Stops accepting an app's deploys and frees its host port. |
| `statio env set <svc> KEY=VALUE` | Sets a server-side base env key. |
| `statio env set <svc> KEY --secret-stdin` | Reads the value from stdin and stores a secret reference. |
| `statio env list <svc>` | Lists base keys, with secret and protected values redacted. |
| `statio env rm <svc> KEY` | Removes a base key. |

### statio app add

| Flag | Default | Meaning |
|---|---|---|
| `--image` | | The image repository, compared for equality on every deploy. |
| `--repo` | | `owner/repo` or a URL. The app's signing identity. |
| `--workflow` | `deploy.yml` | The workflow file of the signing identity. |
| `--branch` | `main` | The branch allowed to deploy. |
| `--issuer` | | The cosign OIDC issuer. |
| `--registries` | `docker.io,ghcr.io` | Registries allowed for dependency images. |
| `--proxy-domain-suffix` | | Domain suffixes this app may claim on the proxy. |
| `--proxy-upstream` | | Allowed upstream containers. |
| `--dns-domain-suffix` | | Domain suffixes this app may claim in DNS. |
| `--rollback` | `true` | Roll back when the health check fails. |
| `--max-services` | `10` | Cap on services in one deploy. |
| `--services-dir` | `/etc/statio/services` | Where manifests live. |
| `--state-dir` | `/var/lib/statio` | Where the port registry lives. |

`app rm` takes `-y` to skip its confirmation.

### statio env set

| Flag | Meaning |
|---|---|
| `--secret-stdin` | Read the value from stdin and store a secret reference. |
| `--protected` | CI cannot override this key. A deploy that tries fails with `422 [protected]`. |
| `--required` | A deploy must supply this key, or it fails with `422 [required]`. |

## Operate, from a machine on the tailnet

| Command | What it does |
|---|---|
| `statio status --target <host>` | Queries the agent's `/status` and prints its health and the apps it accepts. |
| `statio logs <svc>` | Reads the local deploy audit log on the server. |
| `statio logs <svc> --target <host>` | Fetches a remote agent's log over the tailnet, redacted. |

| Flag | Default | Meaning |
|---|---|---|
| `--target` | | The agent's MagicDNS host, such as `statio.your-tailnet.ts.net`. |
| `--limit` | `20` | Records to show. |
| `--json` | `false` | Emit raw JSON records. |
| `--state-dir` | `/var/lib/statio` | Where the local log lives. |

## Maintenance

| Command | What it does |
|---|---|
| `statio upgrade` | Downloads the latest release, verifies its checksum, replaces the binary, restarts the agent when it runs. |
| `statio doctor` | Checks the environment and reports what is missing. |
| `statio version` | Prints the version. Same as `statio --version`. |

| Flag | Command | Meaning |
|---|---|---|
| `--check` | `upgrade` | Report whether a newer version exists, and install nothing. |
| `--no-restart` | `upgrade` | Replace the binary and leave the service alone. |
| `--version` | `upgrade` | Install an exact version, such as `v1.2.3`. |
| `-y` | `upgrade` | Skip the confirmation. |
| `--fix` | `doctor` | Apply the safe repairs: create a missing state directory, tighten a secret's permissions, restart a crash-looping agent. Needs root. |
| `--config` | `doctor` | Path to the agent's `config.yaml`. |

`statio doctor` runs the same secret-file check the agent runs at boot, so a missing or world-readable
secret shows up here instead of as a crash loop. When the service is down it prints the agent's last
log line. Run it with `sudo` on a server for the full picture.

The CLI checks for a newer release once a day and prints a line when it finds one. Set
`STATIO_NO_UPDATE_CHECK=1` to silence it.

## Internal, run by systemd and the Action

| Command | Run by |
|---|---|
| `statio agent run --config <path>` | systemd |
| `statio deploy …` | the GitHub Action |
| `statio preflight …` | the GitHub Action |

### statio deploy and statio preflight

Both take the same inputs. `preflight` posts the same signed payload to the agent's read-only
`/preflight` endpoint, which checks the host port and the proxy and DNS readiness and changes nothing.

| Flag | Default | Meaning |
|---|---|---|
| `--target` | | The agent's MagicDNS host. Also the signed audience. |
| `--service` | | The service slot, accepted on the server. |
| `--image` | | The image repository, with no tag or digest. |
| `--digest` | | The image digest, `sha256:…`. |
| `--statio-file` | `statio.yaml` | Path to the repo's `statio.yaml`. |
| `--audience` | `--target` | Override the signed audience. |
| `--strict` | `false` | Treat `success_degraded` as a failure. |

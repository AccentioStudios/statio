<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)"  srcset="statio-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="statio-light.svg">
    <img alt="Statio" src="statio-light.svg" width="96">
  </picture>
</p>

<p align="center">Deploy to your own server with a <code>git push</code>. No SSH, no open ports.</p>

<p align="center">
  <a href="https://statio.accentio.dev/"><b>Documentation</b></a> ·
  <a href="https://statio.accentio.dev/tutorials/first-deploy/">Deploy your first app</a> ·
  <a href="https://statio.accentio.dev/explanation/how-statio-works/">How it works</a>
</p>

---

## What statio does

**Statio** ships your Docker image to your own server with a `git push`. Your GitHub Actions
workflow builds and signs the image. An agent on your server receives it, checks the signature
against the repo you authorised, and recreates the container.

```
git push ─▶ CI builds and signs ─▶ agent on your server ─▶ deployed
```

- **No SSH and no deploy port.** CI reaches the agent over a private Tailscale network. The agent
  is not reachable from the internet.
- **Signed deploys only.** The agent verifies a keyless cosign signature before it runs anything.
- **Domain and DNS in the same deploy.** The agent configures NPMplus and Cloudflare with
  credentials the workflow never sees.
- **One Actions step.** Build, sign and deploy together. No deploy scripts.

Tailscale carries the deploy signal alone. Your app's public traffic takes the ordinary path,
through a reverse proxy on 80 and 443.

## Install

On the server:

```sh
curl -fsSL https://statio.accentio.dev/install.sh | sudo sh
```

The installer detects your OS and architecture, downloads the binary from GitHub Releases, verifies
its checksum, and writes it to `/usr/local/bin/statio`. You also need Docker on the server and a
Tailscale account. In CI you install nothing; the Action downloads the binary.

```sh
sudo statio upgrade   # update the binary and restart the agent
sudo statio doctor    # check the environment
```

## Set it up

Three commands, once each:

```sh
sudo statio init server     # on the server: configure and start the agent
sudo statio app add api     # on the server: accept an app and pin its signer
statio init repo            # in your repo: write statio.yaml and the workflow step
```

Then set the two Tailscale secrets plus your app's secrets, and push. The
[tutorial](https://statio.accentio.dev/tutorials/first-deploy/) covers every step, including the
Tailscale console.

## Documentation

- **[Deploy your first app](https://statio.accentio.dev/tutorials/first-deploy/)** takes you from an
  empty server to a working push.
- **[How-to guides](https://statio.accentio.dev/how-to/add-a-domain/)** cover domains, secrets,
  rollback, dependencies and several servers.
- **[Reference](https://statio.accentio.dev/reference/statio-yaml/)** documents `statio.yaml`, the
  CLI, the Action, the pipeline and every error code.
- **[Explanation](https://statio.accentio.dev/explanation/how-statio-works/)** covers the design and
  the [security model](https://statio.accentio.dev/explanation/security-model/).
- **[Contributing](CONTRIBUTING.md)** covers building, testing and proposing changes.

---

<p align="center"><sub>Made by accentiostudios.</sub></p>

---
title: Getting started
description: From zero to a git push that deploys — server setup, the workflow step, and your first deploy.
---

Setup touches **two places**. Each command is tagged:

- 🖥️ **On your server** — the Linux VPS, over SSH, as root.
- 💻 **On your machine** — inside your project's repo.

## Install the binary

On the server:

```sh
curl -fsSL https://statio.accentio.dev/install.sh | sudo sh
```

It detects your OS/arch, downloads the binary from GitHub Releases, verifies the checksum, and
installs it to `/usr/local/bin/statio`. You also need **Docker** on the server and a **Tailscale**
account (the free plan is enough).

## Step 0 · Tailscale (once, on the web)

Tailscale is the **private channel CI uses to reach the agent** — it replaces SSH, so the agent
never opens a public deploy port. It is **not** how your app is served (that's your reverse proxy on
`80/443`).

Do these two steps in order — the OAuth client can only own tags that already exist, so the tags
come first.

### 1. Define the tags (Access Controls)

Open **Access controls** in the admin console and paste this ACL. It creates `tag:agent` (the
agent) and `tag:ci` (CI), and allows only `tag:ci` to reach the agent, on a single port:

```json
{
  "tagOwners": {
    "tag:agent": ["autogroup:admin", "tag:agent"],
    "tag:ci":    ["autogroup:admin", "tag:ci"]
  },
  "acls": [ { "action": "accept", "src": ["tag:ci"], "dst": ["tag:agent:443"] } ],
  "ssh": []
}
```

:::caution[Each tag must own itself]
Note the tags appear in **their own** `tagOwners` list (`tag:agent` owns `tag:agent`). An OAuth
client may only register a device with — or mint an auth key for — a tag that is *owned by one of
the tags the client carries*. Without self-ownership, `init server` fails with
`requested tags … are invalid or not permitted` (and the agent can't register). `autogroup:admin`
stays so you can still assign the tags by hand.
:::

### 2. Create the OAuth client (with those tags)

Go to **Settings → [OAuth clients](https://login.tailscale.com/admin/settings/oauth) → Generate
OAuth client** (newer consoles group this under **Trust credentials → New credential**). Pick
**Custom scopes** and enable exactly these two, both **Write**:

| Tailscale scope | Where to find it in the UI | Why |
|---|---|---|
| `auth_keys`    | **Keys → Auth Keys → Write** | lets the server mint the shared `tag:ci` key for CI |
| `devices:core` | **Devices → Core → Write**   | lets the agent register itself as a node and carry its tag |

Enabling **Devices → Core** makes Tailscale require you to pick **tags** — choose `tag:agent` and
`tag:ci` (a credential can only mint keys for tags it owns, and the agent joins as `tag:agent`).

Generate it, and copy the **client id** and **secret** — you paste them into `statio init server`
next. The server uses this one client both to join the tailnet *and* to mint the `tag:ci` key CI
needs, so you never create that key by hand.

This is the only manual Tailscale step.

## Part A — On your server 🖥️

Two steps: `init server` brings up the agent, and `app add` accepts each app you'll deploy.

### A1 · Configure the agent 🖥️

```sh
sudo statio init server
```

It asks only for the server name and the **Tailscale OAuth client** (the id + secret from Step 0)
— **no repo here**. It writes the agent config, **enables and starts the `statio-agent` service**,
and **mints the shared `tag:ci` auth key** CI uses to reach it, printing a ready-to-paste command:

```
  Server name        › statio
  OAuth client ID    › k123ABC...
  OAuth client secret › ••••••••••••••••

  ✓ gh secret set STATIO_TS_AUTHKEY --repo <owner>/<repo> --body 'tskey-auth-…'
```

Run that `gh secret set` **on your machine, not on the server** — there's no repo on the server, so
`gh` there fails with *"not a git repository"*. The `--repo owner/repo` flag lets you run it from
anywhere; or `cd` into the repo and drop the flag. It's **one secret, reused by every repo** that
deploys here, so you can also set it once for the whole org:

```sh
gh secret set STATIO_TS_AUTHKEY --org <your-org> --visibility all --body 'tskey-auth-…'
```

(Rotate it later by re-running `statio init server`.)

### A2 · Accept an app 🖥️

You authorize each app — and which GitHub repo may deploy it — with `statio app add`. Apps can come
from **different repos and even different organizations**; each pins its own signer.

```sh
sudo statio app add api
```

The wizard asks for the **GitHub repo first** and detects the rest from it:

```
  App name                   › api
  This app's GitHub repo     › accentiostudios/api    # detected: PUBLIC, default branch main
  Workflow file / Branch     › deploy.yml / main      # branch pre-filled from the repo
  Image on GHCR (this repo)? › Yes → ghcr.io/accentiostudios/api   # inferred; needn't exist yet
  Allowed registries (deps)  › docker.io, ghcr.io
  Expose a public domain?    › no
```

After you enter the repo, `app add` looks it up: **public** repos are read from the GitHub API with
no auth; **private** ones need `gh` installed and logged in on the server (else it just asks you to
type the branch by hand). From that it pre-fills the default branch and, if you say the image lives
on GHCR under the same repo, infers `ghcr.io/<owner>/<repo>` (lowercased) so you don't paste a URL —
or pick "No" to paste a Docker Hub / other registry path.

That repo + workflow + branch becomes this app's **cosign signing identity**:
`https://github.com/<owner>/<repo>/.github/workflows/<file>@refs/heads/<branch>` — matched exactly
(see [Footguns of the signing identity](/architecture/#62-footguns-of-the-signing-identity)). Run
`app add` again for a second app from another repo/org.

A signed deploy can only target an app you already accepted — it can never stand one up, and it can
only deploy what *its* repo signed (full reasoning:
[per-app signers](/architecture/#61-each-app-pins-its-own-signer)).

:::tip[First time? The image doesn't exist yet — that's fine]
You set **where CI will push** the image, not an image that already exists. Two different things are
asked: the **image repository** (`ghcr.io/<org>/<app>` — a registry path) and **this app's GitHub
repository** (`<owner>/<app>` — the source repo that signs deploys). Your first `git push` is what
builds the image, pushes it, signs it and deploys it. The repo only needs a `Dockerfile` and the
workflow `statio init repo` sets up.
:::

:::note
Image in a **private** repo? Once, on the server: `docker login ghcr.io` (the agent pulls the image
using the host's Docker login).
:::

The non-interactive form, for scripts:

```sh
sudo statio app add api --image ghcr.io/accentiostudios/api \
  --repo accentiostudios/api --branch main \
  --proxy-domain-suffix example.com --dns-domain-suffix example.com
```

### A3 · Verify the agent 🖥️

`init server` already enabled and started the `statio-agent` service. Confirm it's up:

```sh
systemctl status statio-agent      # active (running)
statio status                      # the agent's own view
```

## Part B — In your repo 💻

This part runs on your machine, inside your project's repo — not on the server.

### B1 · Prepare the repo 💻

```sh
statio init repo
```

In your repo, this:

- creates `statio.yaml` if it doesn't exist (your app's config),
- detects your repo and prints the exact signing identity to use in `statio app add` (Part A),
- checks whether you already have CI: if you **have a workflow**, it gives you a *snippet* to paste
  (it never touches your file); if you **don't**, it offers to generate a `.github/workflows/deploy.yml`.

`statio.yaml` describes your app — it's the source of truth for the deploy:

```yaml
services:
  - name: api                    # your app: no `image:` → your signed image is injected
    ports: [3000]                # → published only on 127.0.0.1:3000
    env: [DATABASE_URL]          # the NAME only; the value comes from CI
    env_inline: { NODE_ENV: production }
    health: { path: /health }
proxy: { domain: api.example.com, upstream: api, upstream_port: 3000 }
dns:   { domain: api.example.com }
```

:::note[statio.yaml replaces docker-compose.yml]
You don't write a compose file. The agent **generates** the compose from `statio.yaml` (from a
fixed, safe template) on the server — a `docker-compose.yml` in your repo is ignored. You can't use
both: `statio.yaml` is the single source of truth. This is what guarantees the deploy can't request
`privileged`, host bind-mounts, or host networking (those fields don't exist in `statio.yaml`).
:::

The workflow step goes where you build and sign your image. It's what `statio init repo` prints
when you already have CI:

```yaml
permissions:
  id-token: write        # REQUIRED: cosign signs the image + payload (keyless OIDC)
  packages: write
  contents: read

# ...your build + push of the image, leaving the digest in steps.build.outputs.digest...
- uses: sigstore/cosign-installer@v3
- run: cosign sign --yes ghcr.io/accentiostudios/api@${{ steps.build.outputs.digest }}

- uses: accentiostudios/statio@v1
  with:
    target:  statio.your-tailnet.ts.net        # the agent's hostname (= signed audience)
    service: api                               # must be accepted on the server (statio app add)
    image:   ghcr.io/accentiostudios/api       # must match `statio app add --image`
    digest:  ${{ steps.build.outputs.digest }}
    env: |                                     # one KEY=${{ secrets.KEY }} per env in your statio.yaml
      DATABASE_URL=${{ secrets.DATABASE_URL }}
    ts-authkey: ${{ secrets.STATIO_TS_AUTHKEY }}   # the key minted by `statio init server`
```

statio never modifies your workflow: if you already have one, you paste the step; if not, it
generates the full `deploy.yml`. The Action inputs are in the [reference](/reference/github-action/).

### B2 · Configure the secrets 💻

From your machine, towards GitHub:

```sh
gh secret set STATIO_TS_AUTHKEY --body '<the auth key statio init server printed>'
gh secret set DATABASE_URL      --body 'postgresql://app:...@db:5432/appdb'
```

`STATIO_TS_AUTHKEY` is the same for every repo (the server minted it once). `DATABASE_URL` and any
other app secret follow the rule: `statio.yaml` declares the **names**; the Action's `env:` block
gives them their **value** from `${{ secrets.* }}`. Anything non-secret goes in `env_inline`.

### B3 · Deploy 💻

```sh
git push
```

CI builds and signs the image, signs the payload (the same keyless identity), and sends the
envelope. The agent verifies it, pulls the image, generates the compose from your `statio.yaml`,
and recreates the containers. Per-stage status shows in the Action logs; history is in
`statio logs api`.

:::note
`statio init repo`'s auto-detect reads your local git remote, so it works with private repos. The
image and code stay private, but keyless signing records the repo *identity* in a public log
(Rekor): the repo **name** becomes public. See [Private repos and Rekor](/architecture/#65-private-repos-and-rekor).
:::

## Next steps

- [Add a domain](/guides/domains/) — reverse proxy + DNS.
- [Environment variables](/guides/environment/) — how secrets flow from GitHub to the container.
- [Rollback](/guides/rollback/) — manual and automatic.
- [GitHub Action reference](/reference/github-action/) and [CLI reference](/reference/cli/).

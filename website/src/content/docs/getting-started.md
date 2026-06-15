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
curl -fsSL https://raw.githubusercontent.com/accentiostudios/statio/main/install.sh | sudo sh
```

It detects your OS/arch, downloads the binary from GitHub Releases, verifies the checksum, and
installs it to `/usr/local/bin/statio`. You also need **Docker** on the server and a **Tailscale**
account (the free plan is enough).

## Step 0 · Tailscale (once, on the web)

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

## Part A — On your server 🖥️

Two steps: `init server` brings up the agent (the server's base) and `enable` accepts each
service you'll deploy.

### A1 · Configure the agent 🖥️

```sh
sudo statio init server
```

The wizard asks for the essentials. The key field is the **signing identity**: it defines which
GitHub workflow may deploy to this server.

```
  Server name          › statio
  GitHub repository    › accentiostudios/api      # owner/repo or the URL
  Workflow file        › deploy.yml
  Branch               › main
  OAuth client secret  › ••••••••••••••••
```

From those fields it builds the identity
`https://github.com/<owner>/<repo>/.github/workflows/<file>@refs/heads/<branch>`. The **owner** is
your user or organization (on GitHub it's the same field): a personal account uses its username,
`your-user/my-api`.

:::note
The identity is matched **exactly** — case, branch and workflow filename must all match, or the
deploy fails at `verify`. See [Footguns of the signing identity](/statio/architecture/#62-footguns-of-the-signing-identity).
If you already have the repo handy, `statio init repo` (Part B) prints the exact identity ready to paste.
:::

### A2 · Enable the service 🖥️

```sh
sudo statio enable
```

`enable` accepts a service and pins its security anchors: the allowed image repository, the
dependency registries, and the domains. The wizard asks:

```
  Service name               › api
  Image repository           › ghcr.io/accentiostudios/api   # the EXACT repo (repo-equality)
  Allowed registries (deps)  › docker.io, ghcr.io
  Expose a public domain?    › no
```

It is separate from `init server` on purpose: a signed deploy **can only deploy to a service you
already accepted with `enable`**, never create a new one. If your CI is compromised, the attacker
is bounded to what you enabled. (Full reasoning in
[Why enable is separate from init server](/statio/architecture/#61-why-enable-is-separate-from-init-server).)

:::note
Image in a **private** repo? Once, on the server: `docker login ghcr.io` (the agent pulls the image
using the host's Docker login).
:::

The non-interactive form, for scripts/CI:

```sh
sudo statio enable api --image ghcr.io/accentiostudios/api \
  --proxy-domain-suffix example.com --dns-domain-suffix example.com

# secrets only ops should see (optional — most come from GitHub Secrets):
sudo statio env set api SOME_OPS_SECRET --secret-stdin --protected
```

### A3 · Start the agent 🖥️

```sh
sudo systemctl daemon-reload && sudo systemctl enable --now statio-agent
```

## Part B — In your repo 💻

This part runs on your machine, inside your project's repo — not on the server.

### B1 · Prepare the repo 💻

```sh
statio init repo
```

In your repo, this:

- creates `statio.yaml` if it doesn't exist (your app's config),
- detects your signing identity and prints it, ready to paste in Part A,
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
    service: api                               # must be enabled on the server
    image:   ghcr.io/accentiostudios/api       # must match `statio enable`
    digest:  ${{ steps.build.outputs.digest }}
    env: |                                     # one KEY=${{ secrets.KEY }} per env in your statio.yaml
      DATABASE_URL=${{ secrets.DATABASE_URL }}
    ts-oauth-client-id: ${{ secrets.TS_OAUTH_CLIENT_ID }}
    ts-oauth-secret:    ${{ secrets.TS_OAUTH_CLIENT_SECRET }}
```

statio never modifies your workflow: if you already have one, you paste the step; if not, it
generates the full `deploy.yml`. The Action inputs are in the [reference](/statio/reference/github-action/).

### B2 · Configure the secrets 💻

From your machine, towards GitHub:

```sh
gh secret set TS_OAUTH_CLIENT_ID     --body '<tailscale tag:ci oauth client id>'
gh secret set TS_OAUTH_CLIENT_SECRET --body '<tailscale tag:ci oauth client secret>'
gh secret set DATABASE_URL           --body 'postgresql://app:...@db:5432/appdb'
```

The rule: `statio.yaml` declares the **names** of the keys; the Action's `env:` block gives them
their **value** from `${{ secrets.* }}`. Anything non-secret goes in `env_inline`.

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
(Rekor): the repo **name** becomes public. See [Private repos and Rekor](/statio/architecture/#65-private-repos-and-rekor).
:::

## Next steps

- [Add a domain](/statio/guides/domains/) — reverse proxy + DNS.
- [Environment variables](/statio/guides/environment/) — how secrets flow from GitHub to the container.
- [Rollback](/statio/guides/rollback/) — manual and automatic.
- [GitHub Action reference](/statio/reference/github-action/) and [CLI reference](/statio/reference/cli/).

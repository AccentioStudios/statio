---
title: Deploy your first app
description: From an empty server to a git push that deploys, in seven steps.
sidebar:
  order: 1
prev: false
next:
  link: /how-to/add-a-domain/
  label: Add a domain
---

By the end of this tutorial you push a commit and your app runs on your own server, in a container,
behind a signature your CI produced. It takes about twenty minutes.

You will set up a Tailscale network, install statio on a server, start its agent, accept one app,
prepare your repo, and push.

## Before you start

You need four things:

- A Linux server you can reach over SSH as root, with Docker installed.
- A [Tailscale](https://login.tailscale.com/start) account. The free plan covers this tutorial.
- A GitHub repo with a `Dockerfile` that builds your app.
- The [`gh` CLI](https://cli.github.com/) on your machine, logged in.

Your app must listen on a port and answer on a health path. This tutorial uses port `3000` and
`/health`. Change both to match your app.

Every command below is tagged:

- 🖥️ runs on your server, over SSH, as root.
- 💻 runs on your machine, inside your repo.

## Step 1. Create the Tailscale tags and clients

CI reaches the agent over a private Tailscale network instead of SSH. Set that up once, in the
Tailscale admin console.

Open **Access controls** and replace the policy with this:

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

Save it. Each tag lists itself as its own owner, which is what lets an OAuth client register a
device with that tag.

Now open **Settings → [OAuth clients](https://login.tailscale.com/admin/settings/oauth) → Generate
OAuth client** and create two clients, in this order:

1. Name it `statio-agent`. Pick **Custom scopes**, check **Write** on `auth_keys` (Keys → Auth Keys)
   and on `devices:core` (Devices → Core). When Tailscale asks for tags, pick `tag:agent`. Copy the
   id and the secret it shows you. You paste them in step 3.
2. Name it `statio-ci`. Pick **Custom scopes**, check **Write** on `auth_keys` only. Assign
   `tag:ci`. Copy the id and the secret. You paste them in step 6.

The secret appears once. Keep both pairs in your password manager before you close the page.

## Step 2. Install statio on the server 🖥️

```sh
curl -fsSL https://statio.accentio.dev/install.sh | sudo sh
```

The installer detects your OS and architecture, downloads the binary from GitHub Releases, checks
its checksum, and writes it to `/usr/local/bin/statio`. Confirm it:

```sh
statio version
```

## Step 3. Start the agent 🖥️

```sh
sudo statio init server
```

The wizard asks for a server name and for the agent's Tailscale OAuth client. Paste the `statio-agent`
id and secret from step 1:

```
  Server name         › statio
  OAuth client ID     › k123ABC...
  OAuth client secret › ••••••••••••••••

  ✓ agent configured and started
  ✓ Agent address (use this as the CI 'target'): statio.your-tailnet.ts.net
```

Write down that address. Step 5 asks for it.

Check the agent came up:

```sh
sudo statio doctor
```

Every line reports `ok`. If one does not, run `sudo statio doctor --fix` and read what it prints.

## Step 4. Accept your app 🖥️

The agent deploys an app only after you accept it by name:

```sh
sudo statio app add api
```

Answer the wizard:

```
  App name                   › api
  This app's GitHub repo     › your-org/api
  Workflow file / Branch     › deploy.yml / main
  Image on GHCR (this repo)? › Yes
  Extra containers (DB/…)?   › no
  Expose a public domain?    › no
```

Say **no** to the public domain. You add one later, in
[Add a domain](/how-to/add-a-domain/).

The image does not exist yet. You are telling the agent where CI will push it.

## Step 5. Prepare your repo 💻

Change into your project on your machine and run:

```sh
statio init repo
```

It writes `statio.yaml` and generates `.github/workflows/deploy.yml`. Open `statio.yaml` and make it
match your app:

```yaml
services:
  - name: api
    ports: [3000]
    env: [DATABASE_URL]
    env_inline: { NODE_ENV: production }
    health: { path: /health }
```

`ports` is the port your app listens on inside the container. `env` lists key names, never values.

Open `.github/workflows/deploy.yml` and set `target` to the agent address from step 3:

```yaml
      - uses: accentiostudios/statio@v1
        with:
          target: statio.your-tailnet.ts.net
          service: api
          image: ghcr.io/your-org/api
          ts-oauth-client-id: ${{ secrets.STATIO_TS_OAUTH_CLIENT_ID }}
          ts-oauth-secret: ${{ secrets.STATIO_TS_OAUTH_SECRET }}
          env: |
            DATABASE_URL=${{ secrets.DATABASE_URL }}
```

## Step 6. Set the GitHub secrets 💻

Paste the `statio-ci` id and secret from step 1, plus one secret per key your `statio.yaml` declares:

```sh
gh secret set STATIO_TS_OAUTH_CLIENT_ID --body 'k456DEF...'
gh secret set STATIO_TS_OAUTH_SECRET    --body 'tskey-client-...'
gh secret set DATABASE_URL              --body 'postgresql://app:...@db:5432/appdb'
```

## Step 7. Push 💻

```sh
git add statio.yaml .github/workflows/deploy.yml
git commit -m "add statio deploy"
git push
```

Open the Actions tab and watch the run. The step prints one line per stage:

```
✓ admit    ✓ verify    ✓ pull    ✓ env    ✓ recreate    ✓ health    ✓ persist
success
```

Your app is running. Confirm it from the server:

```sh
sudo docker ps
statio logs api
```

## What you built

CI builds your image, pushes it to GHCR, and signs it with the run's own identity. It then signs a
deploy payload and sends it to your agent over the Tailscale network. The agent checks the signature
against the repo you named in step 4, pulls the image by digest, writes the env to RAM, generates a
compose file, starts the container, and probes `/health`. A failed probe restores the previous
version.

Your server opens no deploy port and accepts no SSH from CI.

## Next

- [Add a domain](/how-to/add-a-domain/) so the public reaches your app over HTTPS.
- [Pass secrets](/how-to/pass-secrets/) from GitHub to the container.
- [Roll back](/how-to/roll-back/) to an earlier digest.
- [Why Tailscale instead of SSH](/explanation/why-tailscale-not-ssh/) explains the choice you just
  used.

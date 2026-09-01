---
title: Accept another app
description: Authorise a second app on a server, from any repo or organisation.
sidebar:
  order: 2
---

One server hosts as many apps as you accept. Each app pins its own repo, so apps can come from
different organisations. Run this on the server.

## Accept the app

```sh
sudo statio app add worker
```

The wizard asks for the GitHub repo first and derives the rest:

```
  App name                   › worker
  This app's GitHub repo     › your-org/worker
  Workflow file / Branch     › deploy.yml / main
  Image on GHCR (this repo)? › Yes, ghcr.io/your-org/worker
  Extra containers (DB/…)?   › no
  Expose a public domain?    › no
```

For a **public** repo, `app add` reads the default branch from the GitHub API with no credentials.
For a **private** repo, install `gh` on the server and log in as the user you `sudo` from, since
root has no `gh` login. Without it, type the branch by hand.

## Accept it without prompts

```sh
sudo statio app add worker --image ghcr.io/your-org/worker \
  --repo your-org/worker --branch main --workflow deploy.yml
```

## Point the repo at the same server

In the second repo, run `statio init repo`, then set the workflow's `target` to the same agent
address and its `service` to `worker`.

## Change or remove an app

```sh
sudo statio app list          # pick one to view or edit
sudo statio app edit worker   # re-runs the wizard with current values filled in
sudo statio app rm worker     # stops accepting its deploys and frees its host port
```

The agent reads an app's signer on every deploy, so an edit takes effect on the next push with no
restart.

---
title: Pass secrets to a container
description: Route a value from GitHub Secrets into your app, or set one server-side.
sidebar:
  order: 4
---

`statio.yaml` declares key names. The values come from GitHub Secrets, or from a server-side base you
set by hand. For what the agent does with them, see the
[security model](/explanation/security-model/).

## Route a GitHub secret into the container

Declare the name in `statio.yaml`, per service:

```yaml
services:
  - name: api
    env: [DATABASE_URL, JWT_SECRET]
    env_inline: { NODE_ENV: production }
```

Map each name to its secret in the workflow:

```yaml
with:
  env: |
    DATABASE_URL=${{ secrets.DATABASE_URL }}
    JWT_SECRET=${{ secrets.JWT_SECRET }}
```

Set the secrets:

```sh
gh secret set DATABASE_URL --body 'postgresql://app:...@db:5432/appdb'
```

Non-secret literals belong in `env_inline`, committed with your code.

## Set a value on the server instead

Use this for a value CI should never carry, or to rotate a leaked secret without a push:

```sh
sudo statio env set api OPS_TOKEN --secret-stdin --protected   # CI cannot override it
sudo statio env set api MUST_HAVE --required                   # CI must supply it
sudo statio env list api                                       # values stay redacted
sudo statio env rm api OPS_TOKEN
```

A deploy that overrides a `--protected` key fails with `422 [protected]`. A deploy missing a
`--required` key fails with `422 [required]`.

The name still has to appear in the service's `env:` list for the key to reach the container.

## When a key arrives empty

An unset GitHub secret expands to an empty string, so the key reaches the container with no value and
your app crashes on start. Check with `gh secret list`, then set it, or set it server-side with
`statio env set`.

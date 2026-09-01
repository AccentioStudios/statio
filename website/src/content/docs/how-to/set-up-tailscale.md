---
title: Set up Tailscale
description: Create the tags and the two OAuth clients that CI and the agent join with.
sidebar:
  order: 1
---

Do this once per tailnet, before the first `statio init server`. Everything happens in the Tailscale
admin console.

For the reasoning behind two separate clients, see
[why Tailscale instead of SSH](/explanation/why-tailscale-not-ssh/).

## 1. Define the tags

Open **Access controls** and save this policy:

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

Each tag lists itself in its own `tagOwners` entry. Without that, `statio init server` fails with
`requested tags … are invalid or not permitted`.

## 2. Create the agent's client

Go to **Settings → [OAuth clients](https://login.tailscale.com/admin/settings/oauth) → Generate
OAuth client**. Newer consoles call this **Trust credentials → New credential**.

| Field | Value |
|---|---|
| Scopes | **Custom scopes**, Write on `auth_keys` and on `devices:core` |
| Tag | `tag:agent` |

Its id and secret go into `sudo statio init server`.

## 3. Create CI's client

Generate a second client:

| Field | Value |
|---|---|
| Scopes | **Custom scopes**, Write on `auth_keys` |
| Tag | `tag:ci` |

Its id and secret become two GitHub secrets. Set them once for the whole organisation and every repo
inherits them:

```sh
gh secret set STATIO_TS_OAUTH_CLIENT_ID --org your-org --visibility all --body 'k456DEF...'
gh secret set STATIO_TS_OAUTH_SECRET    --org your-org --visibility all --body 'tskey-client-...'
```

Per repo, drop the `--org` and `--visibility` flags.

## Rotate CI's access

Delete or regenerate the `tag:ci` client in the console, then set the two GitHub secrets to the new
values. The client secret has no expiry, so it needs no scheduled rotation.

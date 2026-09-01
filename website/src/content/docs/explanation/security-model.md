---
title: Security model
description: The invariants that hold, what each one blocks, and the limits statio does not claim to solve.
sidebar:
  order: 3
---

statio's security rests on one idea: the agent trusts a signature, never a network position and never
the sender. Everything below serves that.

## The invariants

Several of these carry a test that fails the build when someone undoes them.

1. **Data-only channel.** A closed schema with size caps. Nothing from the event reaches a command
   position.
2. **No public listener.** `go test` fails when `.ListenFunnel(` or `net.Listen(` appears in the
   agent, and the agent checks at startup that its address sits in `100.64.0.0/10`.
3. **Separate tailnet clients.** The agent joins with a `tag:agent` client, CI with a `tag:ci`
   client. CI cannot register a `tag:agent` node, so CI cannot act as the agent.
4. **An exact cosign identity per app.** Each accepted app pins its own signer. A regexp is allowed
   only when anchored and without a wildcard over the owner or repo. No match means no deploy.
5. **Digest re-check after the pull**, plus repository equality. The event selects which signed
   digest of an allowed image to run, never an arbitrary image.
6. **A signed envelope, always.** A missing bundle answers 403. No unsigned path exists.
7. **Byte equality.** The agent verifies the exact bytes it decodes, with no re-marshalling between
   the two.
8. **Target and freshness binding.** `audience` must equal the agent's own hostname, `deploy_seq`
   must exceed the last one applied, and `expiry` is minutes away. Each one fails closed.
9. **Server-side anchors stay.** The allowed repository, the signer, the accepted service, the zone
   and IP, the upstream and registry allowlists, and the protected keys are compared against server
   config. The payload asserts none of them. No deploy creates an app.
10. **Clean env values.** No newline, NUL, or control character reaches an env value, and secrets stay
    out of logs, argv, and responses.
11. **Fail-closed identity lookup**, a required transparency log entry, and secret file permissions
    validated at startup.
12. **`docker.sock` is root-equivalent.** This one is a limit rather than a defence, and the section
    below covers it.

## Each app pins its own signer

`statio init server` brings up the agent and pins no repo. Every app is accepted separately with
`statio app add`, which records that app's cosign signer, the owner, repo, workflow file and branch,
in a manifest under `/etc/statio/services/<app>/`.

One server therefore hosts apps from many repos and many organisations, each anchored on its own.

When a deploy arrives, the agent reads the service name from the payload while still distrusting it.
That name selects which app's signer to check. The agent then verifies the signature over the payload
bytes against that signer. Naming an app you do not control gets an attacker nowhere, since they
cannot produce that app's signature.

The blast radius of a compromised repo is that repo's own app.

### Three ways the identity fails to match

The agent matches the signing identity exactly. Three mistakes produce `403 [identity_mismatch]`:

- **Case.** The owner and repo must match GitHub character for character.
- **Branch.** Only the configured branch deploys, as `@refs/heads/<branch>`.
- **Workflow.** The file name must match. A reusable workflow changes the certificate identity.

## If a signing repo is compromised

An app's signer signs both its image and its payload, so whoever controls that repo's workflow can
deploy code and config for that one app.

What to do:

- **Prevent.** Protect the branch, require reviews on the signing ref, and watch deploys with
  `statio logs`.
- **Rotate a leaked secret without waiting for CI.** `sudo statio env set <app> KEY --secret-stdin`
  on the server. The server-side base is the break-glass path.
- **Rotate the app's signer.** Re-run `sudo statio app edit <app>` with the new repo. The agent reads
  manifests per deploy, so the change lands on the next push with no restart.
- **Cut CI off the tailnet.** Delete or regenerate the `tag:ci` OAuth client, then update the two
  GitHub secrets.

What holds even with a valid signature: DNS points at your pinned IP, domains stay inside the
allowlisted suffix, the generated compose cannot escalate to host root, and the proxy's forward target
is computed by the agent from the port it allocated, never read from the event. An attacker with that
repo deploys something to that repo's own app, and nothing further.

## Secrets at rest

The agent runs as root and holds `docker.sock`, which makes it root-equivalent on the host. That is
inherent to recreating containers, and statio states it rather than working around it.

Values from CI live in `/run/statio`, on tmpfs, in RAM. They stay out of logs, out of argv, and out of
the response to CI. Nothing touches persistent disk.

There is no at-rest encryption, and claiming otherwise would be theatre: `docker inspect` shows those
values to local root, which the agent already is. The protection that does hold is that GitHub Secrets
is the store, the channel is signed, and the values never land on disk.

The compensating controls for `docker.sock` are the cosign gate, which runs before any effect, and the
systemd sandbox around the agent.

## Private repos and the transparency log

`statio init repo` reads your local git remote, so it works in a private repo with no API call and no
credentials. Your code and your image stay private.

Keyless signing records the identity, owner, repo and workflow, in Rekor, the public transparency log.
The repository name becomes public even when the repository is private. Weigh that before adopting
keyless signing for a repo whose existence is itself confidential.

### How the agent pulls a private image

The agent is a separate machine with no GitHub identity, so it needs a credential to read a private
image's cosign signature and pull it.

Rather than store one on the server, the Action forwards the run's own short-lived token inside the
envelope, alongside the signed payload rather than inside it. The agent uses it in memory for that one
deploy, through a throwaway `DOCKER_CONFIG` for the pull and an in-process registry client for cosign,
then drops it. Nothing logs it, audits it, or writes it down.

The token is a capability, not a trust anchor. Integrity comes from the signature and the digest, so a
wrong or absent token makes the pull fail and can never substitute an image. The token expires when
the job ends, so it rotates every run. The workflow needs `packages: write`, which implies read. A
public image sends no token at all.

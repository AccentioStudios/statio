---
title: Contributing
description: Build statio, run the checks CI runs, and open a change.
---

statio is open source. [`CONTRIBUTING.md`](https://github.com/accentiostudios/statio/blob/main/CONTRIBUTING.md)
in the repo is the full version; this page carries the steps.

## Build

```sh
git clone https://github.com/accentiostudios/statio
cd statio
go build ./...
go build -o statio ./cmd/statio
```

You need Go at the version pinned in `go.mod`, Docker to exercise the agent, and a Tailscale account
for an end-to-end run.

## Run the checks

CI runs these four. All must pass:

```sh
go build ./...
go vet ./...
go test ./...
gofmt -l .        # prints nothing
```

## Open a change

1. Branch off `main`.
2. Write commit messages in English, in [Conventional Commits](https://www.conventionalcommits.org)
   form, with an optional scope.
3. Describe what changes, why, and how you tested it.
4. Keep the build green.

Standard Go, formatted with `gofmt`. Justify a new dependency in the PR. Comments and documentation
in English.

## Changes that touch security

Verification, payload parsing, compose generation and secret handling carry invariants that several
tests enforce. When your change touches one of those, say in the PR how it preserves the invariants
listed in the [security model](/explanation/security-model/).

Found a vulnerability? Report it privately through GitHub, under **Security → Report a
vulnerability**. Do not open a public issue.

## Where things live

The [code layout](/reference/code-layout/) reference maps every package.

Two files mirror statio's behaviour and go stale silently. Update them in the same commit as the code
they describe: the pages under `website/src/content/docs/`, and `skills/statio-setup/SKILL.md`.

## Cutting a release

Maintainers tag a version. Pushing to `main` publishes no release.

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

GoReleaser builds the linux and darwin binaries for amd64 and arm64, signs `checksums.txt` with
keyless cosign, and publishes the GitHub Release with the checksum file, its signature and its
certificate. `install.sh` and `statio upgrade` read that release. The moving `v1` tag is repointed so
`uses: accentiostudios/statio@v1` tracks it.

This site publishes from `.github/workflows/docs.yml`, on every push to `main` that touches
`website/`.

---
title: Contributing
description: How to build, test, and propose changes to statio.
---

statio is open source. The canonical guide lives in
[`CONTRIBUTING.md`](https://github.com/accentiostudios/statio/blob/main/CONTRIBUTING.md); the
essentials are below.

## Build & test

```sh
git clone https://github.com/accentiostudios/statio
cd statio
go build ./...

# the same checks CI runs — all must be green:
go vet ./...
go test ./...
gofmt -l .        # must not list any file
```

You'll want **Go** (the version is pinned in `go.mod`), **Docker** to test the agent, and optionally a
**Tailscale** account for end-to-end testing.

## Style

- Standard Go, formatted with `gofmt`. Avoid new dependencies unless necessary.
- Comments and documentation in **English**.
- Commit messages in **English**, [Conventional Commits](https://www.conventionalcommits.org)
  (`feat:`, `fix:`, `docs:`…, with an optional scope).

## Security

If your change touches verification, payload parsing, compose generation or secret handling, explain
in the PR how it preserves the invariants in the [security model](/statio/architecture/#6-security-model).
Found a vulnerability? Don't open a public issue — use GitHub's private reporting
(**Security → Report a vulnerability**).

## Releases (maintainers)

Releases are cut with a tag; GoReleaser builds and signs the binaries and publishes the GitHub
Release, and the moving `v1` tag is repointed so `uses: accentiostudios/statio@v1` tracks the latest.

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

The documentation site (this site) is published by `.github/workflows/docs.yml` on every push to
`main` that touches `website/`.

# Contributing to statio

Thanks for wanting to help. This guide covers how to get the project running, the style we follow,
and how to propose changes.

## Requirements

- **Go** — the version is pinned in [`go.mod`](go.mod); use that or newer.
- **Docker** — to test the agent locally.
- **git**, and optionally a **Tailscale** account for end-to-end testing.

## Build

```sh
git clone https://github.com/accentiostudios/statio
cd statio
go build ./...                    # build everything
go build -o statio ./cmd/statio   # the single binary
```

## Before opening a PR

Run the same checks CI runs; everything must be green:

```sh
go build ./...
go vet ./...
go test ./...
gofmt -l .        # must not list any file
```

## Style

- Standard Go, formatted with `gofmt`. Avoid new dependencies unless necessary; justify them in
  the PR.
- Comments and documentation in **English**.
- Commit messages in **English**, [Conventional Commits](https://www.conventionalcommits.org)
  format (`feat:`, `fix:`, `docs:`, `refactor:`, `chore:`…, with an optional scope).

## Code layout

- `cmd/statio` — the binary entrypoint.
- `internal/` — all the code: `cli`, `agent`, `deploy`, `compose`, `verify`, `spec`, `selfupdate`…
- `action/` — the GitHub composite Action that runs in CI.
- `docs/architecture.md` / the [Architecture docs](https://statio.accentio.dev/architecture/)
  — architecture, deploy pipeline, wire contract and security model. **Read it before touching** the
  agent, the verifier or the compose generator.
- `website/` — the documentation site (Astro Starlight). See its pages under
  `website/src/content/docs/`.

## Security

statio has an explicit security model (cosign signing, server-side anchors, the invariants
documented in the [Architecture docs](https://statio.accentio.dev/architecture/)). If
your change touches verification, payload parsing, compose generation or secret handling, **explain
in the PR how it preserves those invariants**.

Found a vulnerability? Don't open a public issue: use GitHub's private reporting
(**Security → Report a vulnerability**) or contact the maintainers privately.

## Proposing a change

1. Fork and create a branch off `main`.
2. Use Conventional Commits.
3. Open a PR with a clear description: **what** changes, **why**, and **how you tested it**.
4. CI must pass.

## Releases (maintainers)

Releases are cut with a tag — pushing to `main` publishes nothing:

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

That triggers GoReleaser ([`.github/workflows/release.yml`](.github/workflows/release.yml)): it
builds the linux/darwin × amd64/arm64 binaries, signs them with cosign keyless, and publishes the
GitHub Release with `checksums.txt`. `install.sh` and `statio upgrade` consume that release.

The documentation site is published separately by
[`.github/workflows/docs.yml`](.github/workflows/docs.yml) on every push to `main` that touches
`website/`.

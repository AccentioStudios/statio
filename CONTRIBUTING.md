# Contributing to statio

## Requirements

- **Go**, at the version pinned in [`go.mod`](go.mod) or newer.
- **Docker**, to exercise the agent locally.
- **git**, plus a **Tailscale** account for an end-to-end run.

## Build

```sh
git clone https://github.com/accentiostudios/statio
cd statio
go build ./...                    # everything
go build -o statio ./cmd/statio   # the single binary
```

## Before opening a PR

CI runs these four. All must pass:

```sh
go build ./...
go vet ./...
go test ./...
gofmt -l .        # prints nothing
```

## Style

- Standard Go, formatted with `gofmt`. Justify a new dependency in the PR.
- Comments and documentation in English.
- Commit messages in English, [Conventional Commits](https://www.conventionalcommits.org) form
  (`feat:`, `fix:`, `docs:`, `refactor:`, `chore:`), with an optional scope.

## Docs style

Two filters, and prose has to pass both.

- **[Diátaxis](https://diataxis.fr/) decides where a page goes.** A tutorial teaches by doing and
  offers no choices. A how-to gets one job done and links elsewhere for the reasoning. Reference is
  for looking a fact up. Explanation carries every why. The most common mistake is mixing: a how-to
  full of reasoning is explanation in disguise, and the reasoning belongs in `explanation/`.
- **Plain prose.** No em dash joining clauses. Bold marks UI labels and first-mention terms, never
  emphasis. Name the actor in each sentence. State what a thing is instead of what it is not. Cut
  adverbs and throat-clearing openers.

## Proposing a change

1. Fork and branch off `main`.
2. Use Conventional Commits.
3. Open a PR describing what changes, why, and how you tested it.
4. Keep CI green.

## Code layout

- `cmd/statio` is the binary entrypoint.
- `internal/` holds the code: `cli`, `agent`, `deploy`, `compose`, `verify`, `spec`, `selfupdate`.
- `action/` is the composite GitHub Action that runs in CI.
- `website/` is the documentation site (Astro Starlight), under `website/src/content/docs/`.
- `skills/statio-setup/SKILL.md` is the agent-facing setup and troubleshooting skill, written for AI
  coding agents.

The [code layout reference](https://statio.accentio.dev/reference/code-layout/) maps every package.

Read the [security model](https://statio.accentio.dev/explanation/security-model/) and the
[wire contract](https://statio.accentio.dev/reference/wire-contract/) before you touch the agent, the
verifier or the compose generator.

Two things mirror statio's behaviour and go stale without a word. Update them in the same commit as
the code they describe: the pages under `website/src/content/docs/`, and
`skills/statio-setup/SKILL.md`. That covers a change to the `statio.yaml` schema, the Action inputs,
the CLI wizards, the pipeline stages, error codes or hints, and the server config keys.

## Security

Verification, payload parsing, compose generation and secret handling carry invariants, several of
them test-enforced. When your change touches one, say in the PR how it preserves the invariants
listed in the [security model](https://statio.accentio.dev/explanation/security-model/).

Found a vulnerability? Report it privately through GitHub, under **Security → Report a
vulnerability**, or contact the maintainers. Do not open a public issue.

## Releases (maintainers)

A tag cuts a release. Pushing to `main` publishes nothing:

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

That runs GoReleaser ([`.github/workflows/release.yml`](.github/workflows/release.yml)). It builds
the linux and darwin binaries for amd64 and arm64, signs `checksums.txt` with keyless cosign, and
publishes the GitHub Release with the checksum file, its `.sig` and its `.pem`, so anyone can verify
it with `cosign verify-blob`. `install.sh` and `statio upgrade` read that release.

The documentation site publishes separately, from
[`.github/workflows/docs.yml`](.github/workflows/docs.yml), on every push to `main` that touches
`website/`.

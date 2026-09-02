# CLAUDE.md

Guidance for AI agents working in this repo.

## What statio is

One Go binary with two roles. On a server it runs as an agent under systemd. On a GitHub runner the
same binary runs as the client that builds, signs and sends a deploy. Both halves share
`internal/spec`, so the wire contract cannot drift.

A deploy carries data, never scripts. The agent verifies a keyless cosign signature before any
effect, then generates the compose file itself from a fixed template.

## Checks

Run all four before claiming anything works. CI runs the same:

```sh
go build ./...
go vet ./...
go test ./...
gofmt -l .        # must print nothing
```

Docs site:

```sh
cd website && npm run build
```

## Hard rules

These carry tests that fail the build when broken. Do not work around them.

- **No public listener.** `.ListenFunnel(` and `net.Listen(` are forbidden in the agent. A lint test
  greps for them. The agent self-checks its address is in `100.64.0.0/10` at startup.
- **Closed schema.** Every wire and `statio.yaml` field is a scalar, bool, enum, or map of literals.
  Adding a field that could reach a command, shell, template, URL, host, or path position breaks the
  data-only invariant. The dangerous compose keys stay unrepresentable.
- **Verify before act.** The cosign gate runs before the pull, the env write, and the recreate.
  Never move an effect above it, and never re-marshal the payload between verify and decode.
- **Server-side anchors.** The allowed image repository, the signer, the accepted app, the zone and
  IP, and the allowlists live in server config. A payload compares against them and never widens
  them. No deploy creates an app.
- **Secrets stay out of logs, argv and responses.** Values from CI live on tmpfs under `/run/statio`.
- **Error codes are API.** The `[code]` tokens in `internal/deploy/pipeline.go` are matched by tests,
  by `reference/error-codes`, and by `skills/statio-setup/SKILL.md`. Renaming one is a breaking
  change.

Read [the security model](https://statio.accentio.dev/explanation/security-model/) and
[the wire contract](https://statio.accentio.dev/reference/wire-contract/) before touching
`internal/agent`, `internal/verify`, `internal/spec` or `internal/compose`.

## Layout

| Path | What |
|---|---|
| `cmd/statio` | entrypoint |
| `internal/spec` | the `statio/v1` contract, shared by both halves |
| `internal/agent` | tsnet server, `/deploy` and `/preflight` handlers, listener lint |
| `internal/verify` | keyless cosign verification |
| `internal/compose` | the safe compose generator |
| `internal/deploy` | pipeline, state, rollback, health, ports |
| `internal/cli` | cobra tree and huh wizards |
| `internal/statiofile` | the strict `statio.yaml` parser |
| `action/`, `action.yml` | the composite GitHub Action |
| `website/` | the docs site (Astro Starlight) |
| `skills/statio-setup/` | the agent-facing setup and troubleshooting skill |

## Writing rules

Both filters apply to every word a user reads: the site, the README, CLI strings, Action input
descriptions, and installer output.

**[Diátaxis](https://diataxis.fr/) decides where a page goes.**

| Mode | Directory | Rule |
|---|---|---|
| Tutorial | `website/src/content/docs/tutorials/` | One path, no options, no rationale. It has to work start to finish. |
| How-to | `.../how-to/` | Steps toward one goal. Reasoning moves out and gets a link. |
| Reference | `.../reference/` | Lookup: tables and facts, no persuasion. |
| Explanation | `.../explanation/` | Every why lives here. |

Mixing is the common failure. A how-to full of reasoning is explanation in disguise.

**Prose rules.**

- No em dash joining clauses. Use a comma, a colon, or a new sentence.
- Bold marks UI labels and first-mention terms. It never marks emphasis.
- Name the actor: the agent verifies, CI signs, you run. The one exception is a security claim where
  the passive states a property of the system.
- State what a thing is, never "not X, it's Y".
- No adverbs, no throat-clearing openers, no pull-quote endings.

**CLI strings.** `Short` is one imperative line under 60 characters with no trailing period. `Long`
says what the command does and where you run it. `Example` goes on every command a person types.
Wizard prose stays in tutorial mode and links to the matching guide instead of explaining inline.
Error hints state one action plus the command.

**Docs navigation.** Starlight's footer follows sidebar order, which fights the Diátaxis reading
path, so pages set `prev` and `next` in frontmatter. How-to and reference pages set both to `false`,
since neither is a sequence. The tutorial points at `add-a-domain`, and the explanation pages chain
in order.

## Keep in sync, same commit

Three places describe the same behaviour and go stale without a word. A change to the `statio.yaml`
schema, the Action inputs, the CLI wizards, the pipeline stages, the error codes or hints, or the
server config keys updates all of them together:

1. the code,
2. `website/src/content/docs/`,
3. `skills/statio-setup/SKILL.md`.

## Git

- Conventional Commits, in English, with an optional scope.
- Never squash. Merge commits or a rebase that keeps every commit.
- Ask before any history rewrite: force push, hard reset, rebase of published work.
- No `Co-Authored-By: Claude` trailer, ever.

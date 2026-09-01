# Docs and CLI: stop-slop prose, Diátaxis structure

Date: 2026-09-01
Status: approved

## Goal

Every word a user reads, on the site, in the README, and out of the CLI, follows two filters:
Diátaxis for structure, stop-slop for prose.

## Problem

The docs mix modes. `getting-started` is a tutorial carrying explanation (why tags own themselves,
why one CI client serves every repo, at-rest honesty) and reference (input tables). `guides/domains`
is a how-to carrying the loopback-vs-container topology explanation. `architecture` welds
explanation (§2, §6) to reference (§4 wire contract, §9 code layout). No explanation section exists.

The prose carries the AI tells: em dashes joining clauses in almost every paragraph, bold used for
emphasis 80+ times, "not X, it's Y" contrasts, adverbs, throat-clearing openers.

## Structure

The sidebar becomes the four Diátaxis modes.

| New page | Source |
|---|---|
| `tutorials/first-deploy` | `getting-started`, steps only |
| `how-to/set-up-tailscale` | getting-started Step 0 |
| `how-to/add-an-app` | getting-started A2 |
| `how-to/add-a-domain` | `guides/domains` |
| `how-to/pass-secrets` | `guides/environment` |
| `how-to/roll-back` | `guides/rollback` |
| `how-to/run-dependencies` | `guides/multi-service` part 1 |
| `how-to/deploy-to-several-servers` | `guides/multi-service` part 2 |
| `how-to/upgrade-statio` | install and upgrade prose from README and `reference/cli` |
| `how-to/diagnose-a-failed-deploy` | SKILL.md troubleshooting flow |
| `reference/cli` | `reference/cli`, facts only |
| `reference/statio-yaml` | new, schema currently scattered across four pages |
| `reference/github-action` | `reference/github-action` |
| `reference/pipeline` | architecture §3 |
| `reference/wire-contract` | architecture §4, §5 |
| `reference/server-config` | architecture §7, §8 |
| `reference/error-codes` | `reference/troubleshooting` plus the SKILL.md table |
| `reference/code-layout` | architecture §9 |
| `explanation/how-statio-works` | architecture §1 |
| `explanation/why-tailscale-not-ssh` | architecture §2 and the repeated channel note |
| `explanation/security-model` | architecture §6 |
| `contributing` | unchanged position, split into steps and rationale |

`architecture/` and `getting-started/` stop existing. Astro `redirects` covers every old URL.
Fragment anchors such as `/architecture/#6-security-model` cannot survive a server redirect, so every
in-repo link that uses one gets rewritten: CONTRIBUTING, SKILL.md, README, the guides, CLI hints.

## Prose rules

Applied to docs and CLI strings alike.

- No em dash joining clauses. A comma, a colon, or a new sentence.
- Bold marks UI labels and first-mention terms. It stops marking emphasis.
- Every sentence names its actor: the agent verifies, CI signs, you run. One exception: a security
  claim where the passive states a property of the system.
- No "not X, it's Y" contrasts. State Y.
- No adverbs, no throat-clearing openers.
- Mode discipline: a tutorial page has no options and no rationale, a how-to page has steps and a
  link to the explanation, a reference page is lookup, every why lands in `explanation/`.

## CLI

- `Short:` one imperative line, no trailing period, under 60 characters. `Long:` says what the
  command does and where you run it. `Example:` on every command a person types by hand.
- Wizard prose in `internal/cli/init.go` and `internal/cli/app.go` becomes tutorial mode: numbered
  steps, one path, no rationale. The `init server` lines explaining tag self-ownership collapse into
  the steps plus a link to `how-to/set-up-tailscale`.
- Error hints become how-to: one imperative action and the command. The `[code]` tokens keep their
  exact spelling, since tests and `reference/error-codes` match on them.

## Other surfaces

- README shrinks to a landing page: what statio is, install, one link per mode.
- CONTRIBUTING splits build, test and release steps from the security-invariant rationale.
- SKILL.md keeps its agent-facing terseness, gains the prose rules and the new URLs.
- `action.yml` input descriptions and `install.sh` output follow the prose rules.
- `docs/architecture.md` repoints to `explanation/how-statio-works`.

## Out of scope

A prose linter in CI. Any change to error codes, flag names, or command names.

## Verification

`go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .`, and an Astro production build to
catch broken internal links.

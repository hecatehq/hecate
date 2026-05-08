# Contributing to Hecate

Thanks for taking the time. Hecate is operator-grade software — the kind
of thing teams put behind a port and trust with policy, budget, and
provider credentials. Contributions land easier when they read like that.

## Where to start

- **Working with an AI assistant** (Claude Code, Codex, Cursor, etc.): the orientation entry is [`AGENTS.md`](AGENTS.md). The canonical, vendor-neutral instruction layer is [`docs-ai/`](docs-ai/README.md). Tool-specific files ([`CLAUDE.md`](CLAUDE.md), [`.cursor/rules/`](.cursor/rules/)) are thin adapters that point there.
- **Working without an AI assistant**: read [`AGENTS.md`](AGENTS.md) for the codebase map and runtime invariants, then [`docs/development.md`](docs/development.md) for local build / hot-reload / make targets.

The `docs-ai/` tree mirrors the operating loop:

| Directory | What's there |
|---|---|
| [`docs-ai/core/`](docs-ai/core/) | Project context, engineering standards, workflow, verification ladders |
| [`docs-ai/tasks/`](docs-ai/tasks/) | Planning, implementation, debugging, refactoring, code-review shapes |
| [`docs-ai/skills/`](docs-ai/skills/) | Area depth (`backend/`, `ui/`, `providers/`) and posture skills (`architect/` for plan-first, `tester/` for evidence-over-assumptions, `devops/` for delivery surfaces) |

## Verification

Run before opening a PR:

| Surface | Command | When |
|---|---|---|
| Backend build | `go build ./...` | Always |
| Backend tests | `go test -race -timeout 10m ./...` | Required for runtime/backend changes |
| Backend focused | `go test -race -count=1 ./<package>/...` | During iteration |
| UI typecheck | `cd ui && bun run typecheck` | After any UI edit |
| UI tests | `cd ui && bun run test` | Before committing UI changes |
| E2E | `go test -tags e2e ./e2e/...` | When touching the api → orchestrator → providers chain |

The race suite is the floor for runtime/backend changes — not a
nice-to-have. UI tests use `bun run test` (never `bun test`, which skips
the testing-library DOM setup).

Slash-command shortcuts available in Claude Code: `/race` and
`/test-affected`. See [`.claude/commands/`](.claude/commands/).

## Commits

[Conventional Commits](https://www.conventionalcommits.org/). Use
`feat`, `fix`, `test`, `docs`, `chore`, or `refactor` with a scope.

- Subject under ~72 chars, focused on *what changed*.
- Body (separated by blank line) explains the *why*, rejected
  alternatives, and migration notes when relevant.
- Pure-markdown changes: append `[skip ci]` to the subject (CI's
  `paths-ignore` already catches `**/*.md`; the marker is
  belt-and-suspenders).
- Agent-doc-only updates (anything under `docs-ai/`, `AGENTS.md`,
  `CLAUDE.md`, `.cursor/rules/`, `.claude/commands/`) use
  `chore(agent):`.
- **No plan, phase, or release labels** in commit messages or code
  comments. No `P0`, `Phase 2`, `#15`, `Milestone N`. The plan lives
  in chat; the repo record is permanent.

The full discipline is in [`docs-ai/core/workflow.md`](docs-ai/core/workflow.md).

## Pull requests

Keep them coherent. One logical change per PR; if a behavior change
sneaks into a refactor, split. State the verification you ran in the
description — see [`docs-ai/tasks/code-review.md`](docs-ai/tasks/code-review.md)
for the rubric reviewers will apply.

Beta-scope work starts from current `master` on a feature/refactor/docs branch
and lands through a reviewed PR. Do not implement beta features directly on
`master`; reserve direct `master` commits for release mechanics or urgent tiny
corrections that a maintainer explicitly requests. The current beta gate lives
in [`docs/beta-roadmap.md`](docs/beta-roadmap.md).

## Repo policy

Shared agent guidance is repository-owned and committed. There is no
`.local` override layer and no personal customization tier. If a rule
belongs in agent context, it lives under [`docs-ai/`](docs-ai/README.md), in the
open, under version control.

## License

By contributing, you agree your contributions will be licensed under
the project's [LICENSE](LICENSE).

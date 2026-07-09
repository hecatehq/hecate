# Verification

How "done" is determined. Treat the floors as floors, not nice-to-haves.

## Pre-PR rule

Do not create a PR, push an update to an existing PR, mark a PR ready for
review, or ask for merge until the verification for every touched
implementation surface has passed locally:

- Touch `ui/`, `website/`, `.ts`, `.tsx`, `.js`, `.jsx`, CSS, Vitest, or
  Playwright files: run the UI checks listed below.
- Touch Go files, Go modules, backend config, or e2e helpers: run the Go
  checks listed below.
- Touch both frontend and backend surfaces: run both ladders.

Docs-only and agent-guidance-only changes do not require TypeScript or Go
tests, but still need the relevant docs checks when formatting, links, or
screenshots are affected. If a required check cannot run, say why before
creating or updating the PR and call out the residual risk.

Agent-guidance changes (`AGENTS.md`, `CLAUDE.md`, `docs-ai/**`, or scoped
`AGENTS.md` files) should also run `just agent-docs-check`. `just docs-check`
includes it.

Before changing PR state, explicitly identify the related tests for the diff:
neighboring unit tests, integration tests for crossed seams, UI view tests, and
e2e tests for real-binary behavior. Run the relevant set locally first; if a
test cannot be run, say which one and why in the PR/update summary.

Also explicitly check documentation before changing PR state. If behavior,
operator workflow, public API, runtime events, configuration, agent guidance, or
architecture shape changed, update the relevant user docs, AI guidance, runtime
reference, and related Mermaid diagrams in the same change. If no docs need to
change, say that in the PR/update summary instead of leaving it implicit.

Written production code needs automated coverage in the same change. New
behavior gets a new test, bug fixes get a regression test, and refactors keep
the existing behavior tests passing before and after the reshape. If automated
coverage is genuinely impractical, document the reason and the manual smoke
that covers the risk.

## Backend verification ladder

| Step          | Command                                                | When                                                                         |
| ------------- | ------------------------------------------------------ | ---------------------------------------------------------------------------- |
| Format check  | `just go-format-check`                                 | Before claiming done for Go edits; also covered by `just format-check` / CI  |
| Build         | `go build ./...`                                       | Always, before claiming done                                                 |
| Vet           | `go vet ./...` or targeted packages                    | Go backend/runtime changes; use targeted vet during iteration                |
| Focused tests | `go test -race -count=1 ./<package>/...`               | During iteration                                                             |
| Race suite    | `go test -race -timeout 10m ./...` or `just test-race` | **Floor** for runtime/backend changes                                        |
| E2E           | `go test -tags e2e ./e2e/...`                          | When the change crosses the api → orchestrator → providers/sandbox/mcp chain |

Use `just format` when you want the repo-managed local auto-format pass: Go
source via `gofmt -s`, UI and website via Oxfmt, and Markdown / `.mdc` docs via
Oxfmt. Review the resulting diff like any other code change.

Run `go vet` for Go changes before claiming done. Targeted vet is fine while
iterating, for example `go vet ./internal/api ./internal/gateway`; use
`go vet ./...` for broad refactors, release prep, or changes that cross many
packages.

The race suite is the floor — not a nice-to-have — for any change that touches `internal/gateway`, `internal/router`, `internal/providers`, `internal/orchestrator`, `internal/sandbox`, retention/state wiring, or other request execution paths.

E2E build tags: `//go:build e2e` is always required, plus optional `ollama` and `docker` sub-tags. Use `PROVIDER_FAKE_KIND=local` for synthetic local-model scenarios.

## UI verification ladder

| Step              | Command                         | When                                                                  |
| ----------------- | ------------------------------- | --------------------------------------------------------------------- |
| Type check        | `cd ui && bun run typecheck`    | First sanity check after any `.ts`/`.tsx` edit (`tsc -b`, fast)       |
| Lint              | `cd ui && bun run lint`         | Before claiming done; also covered by `just ui-lint` and CI           |
| Format check      | `cd ui && bun run format:check` | Before claiming done; also covered by `just ui-format-check` and CI   |
| Repo format check | `just format-check`             | Mixed Go / UI / website / docs changes; mirrors CI format gates       |
| Docs format check | `just docs-format-check`        | Markdown / `.mdc` changes; also covered by `just verify` and Links CI |
| Tests             | `cd ui && bun run test`         | Before claiming done — vitest                                         |
| Watch mode        | `cd ui && bun run test:watch`   | During iteration                                                      |
| Snapshot update   | `cd ui && bun run test -- -u`   | When a UI shape change is intentional                                 |
| Format            | `cd ui && bun run format`       | Intentional formatting-only cleanup; review the diff                  |
| Repo format       | `just format`                   | Local auto-format for Go, UI, website, and docs                       |

**Never `bun test`** — it skips the testing-library DOM setup and panics on `document[isPrepared]`. Always `bun run test`.

UI linting uses Oxc (`oxlint`) with type-aware TypeScript checks via
`oxlint-tsgolint`. Formatting uses Oxfmt (`oxfmt`); keep formatter churn
separate from behavior changes unless the requested task is explicitly a
formatting cleanup. The shared `.oxlintrc.json` enables React, accessibility,
Vitest, import, TypeScript, Unicorn, and Oxc rules; rule disables should stay
specific and justified by current app architecture or tracked migration debt.
Markdown and `.mdc` docs are also formatted with Oxfmt through
`just docs-format-check` / `just docs-format`. Lychee still owns link and
fragment validation; Oxfmt only normalizes text formatting.

When updating snapshots, review the diff carefully. Accidental snapshot churn is the most common silent regression vector.

## Test layer choice (quick guide)

Full matrix in [`../skills/tester/SKILL.md`](../skills/tester/SKILL.md).

- **Unit**: data-to-UI transformation, wire-shape passthrough, error classification, storage scoping, retry/failover decisions, streaming wire shape, agent_loop tool dispatch, MCP tool registration, conditional rendering of critical states.
- **E2E**: api → orchestrator → providers/sandbox/mcp chain end-to-end; subprocess lifecycle; startup/config semantics; new SSE event sequences operators rely on; public HTTP contract changes that downstream SDKs see.

## Done criteria — backend

- Build passes (`go build ./...`).
- Vet passes for touched Go packages, or `go vet ./...` for broad changes.
- Race suite passes for runtime/backend work.
- Inbound and outbound wire shapes are tested independently.
- New env knobs documented in `.env.example` AND the relevant page under
  `docs/operator/`, `docs/runtime/`, `docs/contributor/`, or `docs/design/`.
- Error paths return the right HTTP status with a useful message.
- OTel attributes are populated for new spans.
- New metric labels are emitted through `internal/telemetry` guardrails, with tests for closed-set and free-form dimensions when adding a new label.
- New event types follow the event-protocol taxonomy and appear in `docs/runtime/events.md` with payload shape.

## Done criteria — UI

- `bun run typecheck` clean.
- `bun run lint` clean.
- `bun run format:check` clean.
- `bun run test` clean.
- Main workflow obvious in seconds; current state visible; labels read like product UI.
- Loading, empty, and error states are explicit.
- Layout works at mobile and desktop widths.
- Tests cover the risky logic.

## Diff review

After edits, run `git status --short && git diff --stat` and confirm the change is cohesive. No accidental drift, no unrelated formatting, no half-staged hunks.

## Manual verification

Required when automated tests cannot fake the real-world failure mode. Examples:

- SSE reconnect behavior under network blips.
- Sandbox network egress allowlist (real subprocess + real network).
- OTel exporter wiring against a real collector.
- UI focus management, scroll restoration, multi-tab drag-and-drop.

When manual smoke is needed, state the steps in the change summary so the operator can replay them. "Looks fine" is not a manual smoke report — name what was checked and what was not.

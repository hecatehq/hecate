# Verification

How "done" is determined. Treat the floors as floors, not nice-to-haves.

## Backend verification ladder

| Step | Command | When |
|---|---|---|
| Build | `go build ./...` | Always, before claiming done |
| Focused tests | `/test-affected` (or `go test -race -count=1 ./<package>/...`) | During iteration |
| Race suite | `go test -race -timeout 10m ./...` (or `/race`) | **Floor** for runtime/backend changes |
| E2E | `go test -tags e2e ./e2e/...` | When the change crosses the api → orchestrator → providers/sandbox/mcp chain |

The race suite is the floor — not a nice-to-have — for any change that touches `internal/gateway`, `internal/router`, `internal/providers`, `internal/orchestrator`, `internal/sandbox`, retention/state wiring, or other request execution paths.

E2E build tags: `//go:build e2e` is always required, plus optional `ollama` and `docker` sub-tags. Use `PROVIDER_FAKE_KIND=local` to skip pricebook preflight on synthetic models.

## UI verification ladder

| Step | Command | When |
|---|---|---|
| Type check | `cd ui && bun run typecheck` | First sanity check after any `.ts`/`.tsx` edit (`tsgo -b`, fast) |
| Tests | `cd ui && bun run test` | Before claiming done — vitest |
| Watch mode | `cd ui && bun run test:watch` | During iteration |
| Snapshot update | `cd ui && bun run test -- -u` | When a UI shape change is intentional |

**Never `bun test`** — it skips the testing-library DOM setup and panics on `document[isPrepared]`. Always `bun run test`.

When updating snapshots, review the diff carefully. Accidental snapshot churn is the most common silent regression vector.

## Test layer choice (quick guide)

Full matrix in [`../skills/tester/SKILL.md`](../skills/tester/SKILL.md).

- **Unit**: data-to-UI transformation, wire-shape passthrough, error classification, tenant scoping, retry/failover decisions, streaming wire shape, agent_loop tool dispatch, MCP tool registration, conditional rendering of critical states.
- **E2E**: api → orchestrator → providers/sandbox/mcp chain end-to-end; subprocess lifecycle; startup/config semantics; new SSE event sequences operators rely on; public HTTP contract changes that downstream SDKs see.

## Done criteria — backend

- Build passes (`go build ./...`).
- Race suite passes for runtime/backend work.
- Inbound and outbound wire shapes are tested independently.
- New env knobs documented in `.env.example` AND the relevant `docs/<feature>.md`.
- Error paths return the right HTTP status with a useful message.
- OTel attributes are populated for new spans.
- New event types appear in `docs/events.md` with payload shape.

## Done criteria — UI

- `bun run typecheck` clean.
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

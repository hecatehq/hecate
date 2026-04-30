# Project context

Hecate is an open-source AI gateway and agent-task runtime. A single Go binary embeds the React operator UI, mediates OpenAI- and Anthropic-shaped client traffic to upstream LLM providers, runs queued `agent_loop` tasks with policy and approval gates, and emits OpenTelemetry traces for everything it does. It is tenant-aware, deny-by-default, runtime-aware, and storage-tiered (memory / sqlite / postgres). Every endpoint, config knob, and error message exists to answer five operator questions: what did the gateway just decide, why, what did it cost, what happens on the next failure, and where is the trace.

## Repository layout

```
cmd/hecate/                gateway binary entry
cmd/sandboxd/              out-of-process sandbox executor

pkg/types/                 public types (ChatRequest, Message, ContentBlock, ...)
                             — no internal/ imports

internal/api/              inbound HTTP shapes + handlers
                             OpenAIChatMessage, OpenAIMessageContent (uppercase)
internal/providers/        outbound HTTP per provider (openai, anthropic)
                             openAIChatMessage, openAIMessageContent (lowercase)
                             — same JSON shape as api/, deliberate duplication
internal/orchestrator/     task runtime (queue, runner, agent_loop, sandbox)
internal/sandbox/          policy + sandboxd boundary
internal/taskstate/        task / run / step / artifact / approval persistence
internal/storage/          postgres + sqlite client wrappers
internal/retention/        retention worker (subsystems: traces, budget, audit, cache, turn_events)
internal/mcp/              stdio MCP server (read tools + write tools)

ui/                        React/Vite operator UI (embedded via //go:embed ui/dist)
e2e/                       binary-startup tests, build tag e2e (sub-tags: ollama, docker)
docs/                      long-form references (canonical product/runtime docs)

.claude/                   Claude Code adapter (slash commands, settings)
.cursor/                   Cursor adapter (.mdc rule files)
ai/                        canonical, vendor-neutral agent instruction layer (this directory)
```

## Architecture rings

The codebase has three concentric rings; cross-ring imports go inward only:

- **`pkg/types/`** — public types, no `internal/` imports. The wire-shape contract.
- **`internal/api/`** — inbound HTTP shapes + handlers. Translates HTTP requests into internal types; never touches providers directly.
- **`internal/providers/`** — outbound HTTP per provider (OpenAI-compat, Anthropic). Translates internal types to provider wire shapes. Never imports `internal/api/`.
- **`internal/orchestrator/`** — task runtime (queue, runner, `agent_loop`, sandbox boundary). Sits above providers, called by api.
- **`internal/<feature>/`** — gateway services (governor, router, cache, retention, taskstate, mcp, …). Each owns one concern.

The api↔providers parallel-struct duplication (`OpenAIChatMessage` ↔ `openAIChatMessage`) is **intentional**. It keeps `internal/providers/` free of `internal/api/` imports and lets the wire shapes evolve independently. See [`../skills/providers/SKILL.md`](../skills/providers/SKILL.md) for full reasoning.

## Storage tier rule

Every backend-bound concern (cache, taskstate, chatstate, governor, retention history) ships with three tiers, mirrored exactly:

- `memory` — in-process, default, perfect for `go test` and `make dev`.
- `sqlite` — single-file persistence via `modernc.org/sqlite` (no CGO).
- `postgres` — production scale via `pgx`.

When adding a new persisted thing, mirror all three. Add a `<thing>_test.go` that runs against memory and sqlite (postgres is structurally identical SQL — covered transitively).

## Toolchain pins

- **Go**: see `go.mod` for the exact pinned version. CGO is not used; `modernc.org/sqlite` is the pure-Go sqlite driver.
- **UI package manager**: Bun (pinned via `packageManager` in `ui/package.json`). The lockfile is `bun.lock`; there is no `package-lock.json`. Use `bun install`, `bun run <script>`, `bun add <pkg>`, `bun x <tool>`. Do not introduce npm/yarn/pnpm lockfiles or workflow steps.
- **UI stack**: React 19, TypeScript, Vite, Vitest + Testing Library + jsdom. Plain CSS with design tokens in `ui/src/styles.css` — no CSS-in-JS, no utility-class framework.
- **Critical command distinction**: `bun run test` ≠ `bun test`. The latter skips the testing-library DOM setup and panics with `document[isPrepared]` errors. Always `bun run test` (which dispatches to vitest).

## Risky and sensitive areas

These earn extra scrutiny; changes here are not drive-by territory.

- **Sandbox boundary** (`internal/sandbox`, `cmd/sandboxd`) — out-of-process tool execution. A buggy tool must not be able to crash the gateway.
- **Approval lifecycle** (`internal/taskstate`, `awaiting_approval`) — pre-execution and mid-loop approvals halt the run. New gates use the same `TaskApproval` shape.
- **Retention worker** (`internal/retention`) — high-cardinality history sweep. Subsystems: `traces`, `budget`, `audit`, `cache`, `turn_events`. Persisted things must mirror.
- **Cost ledger** — all money is `int64` micro-USD (`1_000_000` = `$1`). Never `float64`.
- **Tenant scoping** — automatic once the request has a tenant principal. New endpoints must respect it; admin-path bypass is forbidden.

## Canonical docs index

Long-form references live in `docs/`. Update them in the same change as the code, not as a follow-up.

| Doc | Covers |
|---|---|
| [`docs/architecture.md`](../../docs/architecture.md) | Request flow, lease semantics, storage tier matrix |
| [`docs/agent-runtime.md`](../../docs/agent-runtime.md) | `agent_loop` tools, four-layer system prompt, cost model, retry-from-turn |
| [`docs/runtime-api.md`](../../docs/runtime-api.md) | Task / run / step / approval endpoints, queue + lease |
| [`docs/events.md`](../../docs/events.md) | Every event type at `/v1/events` with payload shapes |
| [`docs/telemetry.md`](../../docs/telemetry.md) | OTel spans + metrics, OTLP wiring, status and gaps |
| [`docs/providers.md`](../../docs/providers.md) | Provider catalog, configuration |
| [`docs/tenants.md`](../../docs/tenants.md) | Multi-tenant opt-in: roles, modes, storage |
| [`docs/mcp.md`](../../docs/mcp.md) | MCP server: tools, transport, configure |
| [`docs/deployment.md`](../../docs/deployment.md) | Compose profiles, image pinning, lost-token recovery |
| [`docs/development.md`](../../docs/development.md) | Local build, testing, screenshot tooling, `[skip ci]` convention |

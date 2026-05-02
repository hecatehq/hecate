# Project context

Hecate is an open-source AI gateway and agent-task runtime. A single Go binary embeds the React operator UI, mediates OpenAI- and Anthropic-shaped client traffic to upstream LLM providers, runs queued `agent_loop` tasks with policy and approval gates, and emits OpenTelemetry traces for everything it does. It is tenant-aware, deny-by-default, runtime-aware, and storage-tiered (memory / sqlite / postgres). Every endpoint, config knob, and error message exists to answer five operator questions: what did the gateway just decide, why, what did it cost, what happens on the next failure, and where is the trace.

## Repository layout

```
cmd/gateway/               gateway binary entry

pkg/types/                 public types (ChatRequest, Message, ContentBlock, ...)
                             — no internal/ imports

internal/api/              inbound HTTP shapes + handlers
                             OpenAIChatMessage, OpenAIMessageContent (uppercase)
internal/providers/        outbound HTTP per provider (openai, anthropic)
                             openAIChatMessage, openAIMessageContent (lowercase)
                             — same JSON shape as api/, deliberate duplication
internal/orchestrator/     task runtime (queue, runner, agent_loop, sandbox)
internal/sandbox/          per-call sh subprocess: policy validation, rlimits,
                             env sanitisation, output cap, optional
                             bwrap/sandbox-exec wrapper
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

- **Sandbox boundary** (`internal/sandbox/`) — per-call `sh` subprocess spawned directly from the gateway after policy validation, env sanitisation, output cap, and a wall-clock timeout (Layer 1). On Linux with `bwrap` installed and on macOS, the call is additionally wrapped by `bwrap` / `sandbox-exec` for filesystem and network confinement (Layer 2 — auto-detected at startup via `internal/sandbox/wrapper.go`, no opt-in flag). No separate `sandboxd` daemon — the safety properties run inline. CPU / FD / address-space caps are *not* applied per-call (`setrlimit` would shrink the long-running gateway) — operators who need them run under systemd or in a container with `--cpus` / `--memory` flags. New tool kinds follow the same `internal/sandbox/` shape. See `docs/sandbox.md` for the layer model and `docs/agent-runtime.md` for the network-egress policy that sits on top.
- **Approval lifecycle** (`internal/taskstate`, `awaiting_approval`) — pre-execution and mid-loop approvals halt the run. New gates use the same `TaskApproval` shape.
- **Retention worker** (`internal/retention`) — high-cardinality history sweep. Subsystems: `traces`, `budget`, `audit`, `cache`, `turn_events`. Persisted things must mirror.
- **Cost ledger** — all money is `int64` micro-USD (`1_000_000` = `$1`). Never `float64`.
- **Tenant scoping** — automatic once the request has a tenant principal. New endpoints must respect it; admin-path bypass is forbidden. Multi-tenant *management* (Tenants/Keys tabs, tenant API keys) is opt-in via `GATEWAY_MULTI_TENANT=true`; the published Docker image defaults to single-user. Three observability surfaces have a tenant-readable mirror under `/v1/*` (`/v1/runtime/stats`, `/v1/traces`, `/v1/requests`) — these share the private body with the matching `/admin/*` handler.
- **Bootstrap-token handshake** — `GET /v1/bootstrap-token` is loopback-only and same-origin-fenced (Origin host must match Host *or* be a loopback hostname so Vite dev `localhost:5173` -> `127.0.0.1:8765` works). Refuses when `GATEWAY_AUTH_TOKEN` is operator-supplied. Wire envelope is `{object: "bootstrap_token", data: {token}}` like the rest of the API — UI clients that read top-level fields will silently fail.

## Which doc answers which question

Long-form references live in `docs/`. Update them in the same change as the
code, not as a follow-up. Don't restate their content here — link and move on.

| Question | Doc |
|---|---|
| How does a request flow through the gateway? What are the storage tiers? | [`docs/architecture.md`](../../docs/architecture.md) |
| What `agent_loop` tools exist? What is the four-layer system prompt? Cost model? | [`docs/agent-runtime.md`](../../docs/agent-runtime.md) |
| What are the task / run / step / approval HTTP endpoints? | [`docs/runtime-api.md`](../../docs/runtime-api.md) |
| What does this SSE event payload look like? | [`docs/events.md`](../../docs/events.md) |
| What OTel spans and metrics does the gateway emit? | [`docs/telemetry.md`](../../docs/telemetry.md) |
| How do I configure a provider? What providers are supported? | [`docs/providers.md`](../../docs/providers.md) |
| How does multi-tenant work? What are the roles and modes? | [`docs/tenants.md`](../../docs/tenants.md) |
| How do I configure MCP? What tools does the server expose? | [`docs/mcp.md`](../../docs/mcp.md) |
| How do I deploy? What are the Compose profiles? | [`docs/deployment.md`](../../docs/deployment.md) |
| How do I build and test locally? What does `[skip ci]` mean? | [`docs/development.md`](../../docs/development.md) |
| What sandbox isolation layers are shipped? How do namespaces work? | [`docs/sandbox.md`](../../docs/sandbox.md) |

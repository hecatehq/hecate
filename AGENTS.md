# Hecate

Open-source AI gateway and agent-task runtime. A single Go binary mediates
OpenAI- and Anthropic-shaped client traffic to upstream providers, runs
queued `agent_loop` tasks behind policy and approval gates, and emits
OpenTelemetry traces. Single-user, single-process, deny-by-default,
storage-tiered (memory / sqlite). Binds to 127.0.0.1 by default;
no auth — the threat model is "trust your own machine." The React
operator UI is embedded via `//go:embed ui/dist`.

This file is the orientation entry — the codebase map, the runtime
invariants, and the gotchas that bite often. It is what an agent
(Claude Code, Codex, Cursor, or human) reaches for when starting work
on this repo. Conventions, workflow, verification, and longer-form
guidance live in [`ai/`](ai/README.md).

## Where guidance lives

| Surface | What it carries |
|---|---|
| [`ai/`](ai/README.md) | Canonical agent guidance — project context, conventions, workflow, verification, task shapes, area + posture skills |
| `AGENTS.md` (this) and `ui/AGENTS.md`, `internal/providers/AGENTS.md` | Codebase map per area |
| [`CLAUDE.md`](CLAUDE.md) | Thin Claude Code adapter pointing to `ai/` |
| [`.cursor/rules/`](.cursor/rules/) | Thin Cursor adapter pointing to `ai/` |
| [`.claude/commands/`](.claude/commands/) | Slash commands: `/race`, `/typecheck`, `/test-affected` |
| [`docs/`](docs/) | Long-form references (architecture, runtime API, events, telemetry) |

When in doubt: read [`ai/core/project-context.md`](ai/core/project-context.md) and [`ai/core/workflow.md`](ai/core/workflow.md).

## Codebase map

```
cmd/gateway/            gateway binary entry

pkg/types/              public types (ChatRequest, Message, ContentBlock, ...)
                          — no internal/ imports

ui/                     React/Vite operator UI, embedded via //go:embed ui/dist
tauri/                  native desktop app (Tauri 2.x); wraps hecate as a sidecar,
                          webview loads http://127.0.0.1:{port}/ served by the gateway
scripts/
  release.ts            cut a release: pre-flight, goreleaser snapshot, Tauri
                          version stamp, tag, push  (`bun scripts/release.ts vX.Y.Z`)
  stamp-version.ts      stamp Tauri version files to current git tag / TAURI_VERSION
e2e/                    binary-startup tests; build tag e2e (sub-tags: ollama, docker)
docs/                   long-form references (architecture, runtime API, events, ...)
ai/                     canonical agent guidance (this file points there for depth)

internal/
  api/                  inbound HTTP shapes + handlers (OpenAIChatMessage, uppercase)
  providers/            outbound HTTP per provider (openAIChatMessage, lowercase)
                          — same JSON shape as api/, deliberate duplication
  gateway/              top-level request orchestration: governor → router → cache → provider
  router/               provider/model selection, failover, retry, circuit
  governor/             policy + budget + rate-limit decisions; tenant cost ledger
  policy/               approval policy + provider/model allowlists
  catalog/, models/     provider catalog + model registry
  cache/                exact + semantic response cache
  billing/              pricebook + invoice/usage rollups (cost tables live here)
  orchestrator/         task runtime: queue, runner, agent_loop, sandbox boundary
  sandbox/              per-call sh subprocess: policy validation, env sanitisation,
                          output cap + timeout, auto-detected bwrap/sandbox-exec wrapper
  taskstate/            task / run / step / artifact / approval persistence
  chatstate/            chat-completion conversation persistence
  storage/              postgres + sqlite client wrappers
  retention/            retention worker (subsystems: traces, budget, audit, cache, turn_events)
  mcp/                  stdio MCP server (read tools + write tools)
  controlplane/         tenants, API keys, settings (admin surface state)
  auth/                 bearer-token + tenant-key authentication
  ratelimit/            per-key request limits
  requestscope/         per-request principal + tracing context
  config/, bootstrap/   env-driven config + startup wiring
  secrets/              env-var and file-based secret resolution
  telemetry/            OTel exporter wiring + span helpers
  profiler/             pprof endpoints + runtime stats
  version/              build-time version stamp
```

**Architecture rings** (cross-ring imports inward only):

```
pkg/types/  ←  internal/api/  ←  internal/providers/
                     ↑
              internal/orchestrator/  (sits above api, drives runs through providers)
```

The api↔providers parallel-struct duplication (`OpenAIChatMessage` ↔ `openAIChatMessage`) is intentional — it keeps `internal/providers/` free of `internal/api/` imports. Full rationale: [`ai/skills/providers/SKILL.md`](ai/skills/providers/SKILL.md).

**Storage tier rule**: every backend-bound concern mirrors three tiers —
memory (default), sqlite (`modernc.org/sqlite`, no CGO), postgres (`pgx`).
When adding a new persisted thing, mirror all three.

## Runtime invariants

Non-negotiable rules of the system. Read them before writing code that
touches request handling, persistence, or tool execution.

- **No auth.** Every request is processed as the operator. The gateway binds to `127.0.0.1` by default; the threat model is "trust your own machine." Bind elsewhere only behind a reverse proxy or firewall.
- **Sandbox is per-call subprocess, applied inline.** Shell, file, and git tool calls spawn a fresh `sh` from inside the gateway after policy validation + env sanitisation + output cap + wall-clock timeout. On Linux with `bwrap` installed and on macOS, the call is additionally wrapped by `bwrap` / `sandbox-exec` for filesystem and network confinement (auto-detected at startup). No separate sandbox daemon, no per-call rlimits (those would shrink the long-running gateway). New tools follow the same pattern.
- **Approvals are blocking.** Pre-execution and mid-loop approvals halt the run; the run record persists in `awaiting_approval` until resolved. New gates use the `TaskApproval` shape.
- **Events are appended, not mutated.** Every state transition writes a `run_event` with a monotonic sequence. The SSE stream replays from `after_sequence`. New event types must follow the event-protocol v1 taxonomy (`run.*`, `turn.*`, `tool.*`, `policy.*`, `gap.*`, `error.*`) and be documented in `docs/events.md`.
- **Cost is `int64` micro-USD.** Never `float64` for money — pricebook, budgets, ledger all stay integer (`1_000_000` = `$1`).
- **OTel is first-class.** Every request gets a trace ID surfaced in the `X-Trace-Id` response header and persisted on the run record. New code paths add spans, not just log lines.

## Conventions (in brief)

Full standards: [`ai/core/engineering-standards.md`](ai/core/engineering-standards.md).

- **Comments explain *why***, not what. State the trade-off.
- **Pointer vs value for optional fields**: pointer when zero is a valid
  distinct value (`Seed *int`, `ParallelToolCalls *bool`); value with
  `omitempty` when zero == API default (`PresencePenalty float64`).
- **`json.RawMessage`** for forward-compat passthrough fields.
- **Test naming**: `TestPackage_Behavior`. Table-driven where the variant set is obvious.
- **No emojis**, no plan/phase labels in commit messages or comments.
- **Conventional Commits**; `chore(agent):` for agent-doc-only changes. Don't auto-commit — propose a message and let the operator merge.

## Verification

Full ladder: [`ai/core/verification.md`](ai/core/verification.md).

- **Race suite is the floor for runtime/backend changes**: `go test -race -timeout 10m ./...` (or `/race`). Race builds are large; if your default `$GOCACHE` is on a small volume, point it at the repo: `GOCACHE="$(pwd)/.gocache" go test -race ...`.
- **Iteration**: `/test-affected` for narrow runs.
- **E2E**: `go test -tags e2e ./e2e/...`. Build tag `e2e` always required; sub-tags `ollama`, `docker` opt in. `PROVIDER_FAKE_KIND=local` skips pricebook preflight on synthetic models.
- **UI**: `cd ui && bun run typecheck` then `bun run test`. Never `bun test` (skips testing-library DOM setup).

## Recipes

| Task | Where |
|---|---|
| Add a passthrough wire field (the seven-step chain — most-redone task here) | [`ai/skills/providers/SKILL.md`](ai/skills/providers/SKILL.md) |
| Add an MCP tool / persisted run-event type / test helper cheat-sheet | [`ai/skills/backend/SKILL.md`](ai/skills/backend/SKILL.md) |
| UI recipes (SSE-driven state field, paired pickers, snapshot refresh) | [`ai/skills/ui/SKILL.md`](ai/skills/ui/SKILL.md) |
| Native desktop app (sidecar lifecycle, bundling, Tauri commands) | [`ai/skills/tauri/SKILL.md`](ai/skills/tauri/SKILL.md) |
| Cut a release tag | `bun scripts/release.ts vX.Y.Z` — checks worktree, snapshot dry-run, stamps Tauri versions, tags, pushes. Full procedure: [`ai/tasks/release.md`](ai/tasks/release.md) |
| Stamp Tauri version files | `bun scripts/stamp-version.ts` (or `make tauri-version`) — syncs Cargo.toml, package.json, tauri.conf.json to current git tag |

## Gotchas

- **`bun run test` ≠ `bun test`.** The latter skips the testing-library DOM setup and panics with `document[isPrepared]`. Always `bun run test` (which dispatches to vitest).
- **modernc/sqlite TIME-as-text format**: the driver writes `time.Time` using Go's default `time.Time.String()` format, which doesn't lex-compare with RFC3339Nano cutoffs and silently breaks the retention sweep. Always write timestamps as `t.UTC().Format(time.RFC3339Nano)` when the column is TEXT (see `internal/taskstate/sqlite.go` `AppendRunEvent`).
- **OpenAI/openAI parallel structs are intentional**: don't unify. Mirror fields when adding on either side.
- **Streaming `wireReq` plumbing**: when adding a passthrough field, plumb it into BOTH `Chat` and `ChatStream` `wireReq` constructions in `internal/providers/openai.go`. Forgetting one is the most common provider bug — non-stream tests pass; the field silently drops in production for any client using `stream: true`.
- **Capability-cache seeding** for provider tests: seed `cachedCaps` and `capsExpiry` to skip the discovery path. Snippet in [`ai/skills/providers/SKILL.md`](ai/skills/providers/SKILL.md).
- **Pricebook preflight** in tests: `PROVIDER_FAKE_KIND=local` for synthetic models in e2e.
- **mermaid `loop` is a reserved keyword**: don't use it as a sequence-diagram participant name. Use `Agent` or similar.
- **CodeQL CWE-190**: don't compute `make([]T, 0, len(x)+N)` with arithmetic — use plain `len(x)` and let `append` grow.
- **Env-PRECONFIGURED gate**: `PROVIDER_<NAME>_API_KEY` / `_BASE_URL` only auto-import into the CP store when `PROVIDER_<NAME>_PRECONFIGURED=1` is also set. E2E helpers (`hecateServer`, `startHecateProcess`) funnel through `autoPreconfiguredEnv` to inject the gate; new e2e spawn helpers must do the same or routed requests 400 with `no provider supports model …`.
- **`:8765` collisions across launches**: `make dev` / `make run` / `make serve` now run `make stop` first so a stale `./hecate` from another shell never blocks a relaunch (or a `docker run -p 8765:8765 …`). New scripts that spawn the binary should call `make stop` (or replicate the `lsof -ti:8765 | xargs kill` step).
- **API response envelope**: every `/v1/*` and `/admin/*` GET returns `{object, data}`. Don't write a UI client that reads top-level fields — the bootstrap-token regression burned a release tag because the UI did `payload.token` instead of `payload.data.token`.

## Canonical docs

| Doc | Covers |
|---|---|
| [`docs/architecture.md`](docs/architecture.md) | Request flow, lease semantics, storage tier matrix |
| [`docs/agent-runtime.md`](docs/agent-runtime.md) | `agent_loop` tools, four-layer system prompt, cost model, retry-from-turn |
| [`docs/runtime-api.md`](docs/runtime-api.md) | Task / run / step / approval endpoints, queue + lease |
| [`docs/events.md`](docs/events.md) | Every event type at `/v1/events` with payload shapes |
| [`docs/telemetry.md`](docs/telemetry.md) | OTel spans + metrics, OTLP wiring, status & gaps |
| [`docs/providers.md`](docs/providers.md) | Provider catalog, configuration |
| [`docs/mcp.md`](docs/mcp.md) | MCP server: tools, transport, configure |
| [`docs/deployment.md`](docs/deployment.md) | Compose profiles, image pinning, lost-token recovery |
| [`docs/development.md`](docs/development.md) | Local build, testing, screenshot tooling, `[skip ci]` convention |
| [`docs/desktop-app.md`](docs/desktop-app.md) | Native Tauri 2.x app: distribution, current state, roadmap, footguns |

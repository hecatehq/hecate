# Hecate

Open-source local AI runtime console. Hecate routes OpenAI- and
Anthropic-shaped client traffic to upstream providers, runs queued
`agent_loop` tasks behind policy and approval gates, supervises ACP coding
agents, and emits OpenTelemetry traces. Local-first, deny-by-default, storage-tiered
(memory / sqlite). Binds to 127.0.0.1 by default and is intended to run as
a local operator console. The React operator UI is embedded via
`//go:embed ui/dist`.

This file is the orientation entry — the codebase map, the runtime
invariants, and the gotchas that bite often. It is what an agent
(Claude Code, Codex, Cursor, or human) reaches for when starting work
on this repo. Conventions, workflow, verification, and longer-form
guidance live in [`docs-ai/`](docs-ai/README.md).

## Where guidance lives

| Surface                                                               | What it carries                                                                                                                      |
| --------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| [`docs-ai/`](docs-ai/README.md)                                       | Canonical provider-neutral agent guidance — project context, conventions, workflow, verification, task shapes, area + posture skills |
| `AGENTS.md` (this) and `ui/AGENTS.md`, `internal/providers/AGENTS.md` | Codebase map per area                                                                                                                |
| [`CLAUDE.md`](CLAUDE.md)                                              | Claude Code compatibility shim importing `AGENTS.md`; no standalone rules                                                            |
| [`docs-ai/skills/README.md`](docs-ai/skills/README.md)                | Canonical skill set used by every agent                                                                                              |
| [`docs-ai/core/agent-guidance.md`](docs-ai/core/agent-guidance.md)    | Source-of-truth policy for keeping agent guidance provider-neutral                                                                   |
| [`docs/`](docs/)                                                      | Long-form references (architecture, runtime API, events, telemetry)                                                                  |

When in doubt: read [`docs-ai/core/project-context.md`](docs-ai/core/project-context.md) and [`docs-ai/core/workflow.md`](docs-ai/core/workflow.md).

## Codebase map

```
cmd/hecate/            main runtime entry: gateway service, embedded UI, MCP subcommand
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
docs-ai/                canonical agent guidance (this file points there for depth)

internal/
  api/                  inbound HTTP shapes + handlers (OpenAIChatMessage, uppercase)
  providers/            outbound HTTP per provider (openAIChatMessage, lowercase)
                          — same JSON shape as api/, deliberate duplication
  gateway/              top-level request orchestration: governor → router → provider
  router/               provider/model selection, failover, retry, circuit
  governor/             policy + route gates + append-only usage events
  policy/               approval policy + provider/model allowlists
  catalog/, models/     provider catalog + model registry
  modelcaps/            shared model capability table (streaming, tools, vision, …)
  orchestrator/         task runtime: queue, runner, agent_loop, sandbox boundary
  workspacefs/          shared workspace path resolver for Hecate-mediated
                          file/search/write operations
  processrunner/        bounded local subprocess seam: cwd, env, timeout,
                          streaming output, output caps
  gitrunner/            Git-specific runner used by Hecate-owned Git helpers
  sandbox/              policy validation + OS isolation wrapper for tool
                          subprocesses; shell calls dispatch through ProcessRunner
                          and broad git_exec still runs through this executor
  taskstate/            task / run / step / artifact / approval persistence
  agentadapters/        ACP/process adapters for Codex, Claude Code, Cursor
  eventprotocol/        agent-runtime event protocol v1 envelopes (API-facing shape)
  chat/                 chat transcript persistence (memory / sqlite)
  projects/             durable project identity store (memory / sqlite)
  storage/              sqlite client wrappers
  retention/            retention worker (subsystems: traces, usage_events, audit, provider_history, turn_events, agent_chat_approvals)
  mcp/                  stdio MCP server (read tools + write tools)
  controlplane/         providers, secrets, settings state
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

The api↔providers parallel-struct duplication (`OpenAIChatMessage` ↔ `openAIChatMessage`) is intentional — it keeps `internal/providers/` free of `internal/api/` imports. Full rationale: [`docs-ai/skills/providers/SKILL.md`](docs-ai/skills/providers/SKILL.md).

**Storage tier rule**: every backend-bound concern mirrors two tiers —
memory (default) and sqlite (`modernc.org/sqlite`, no CGO).
When adding a new persisted thing, mirror both.

## Runtime invariants

Non-negotiable rules of the system. Read them before writing code that
touches request handling, persistence, or tool execution.

- **Local operator boundary.** Every request is processed as the operator. The gateway binds to `127.0.0.1` by default; bind elsewhere only behind a reverse proxy, firewall, or equivalent access control.
- **WorkspaceFS / runners are the workspace boundary.** Hecate-mediated file/search/write operations resolve paths through `internal/workspacefs`. Shell commands go through the sandbox executor and `internal/processrunner`; Hecate-owned Git helper calls go through `internal/gitrunner` where they do not need the broad `git_exec` shell-shaped interface. Avoid raw `os.*` path access, raw `exec.Command`, or direct `git` subprocesses for workspace-bound behavior unless you are inside those seams or writing a narrowly scoped test.
- **Sandbox is per-call and applied inline.** Tool subprocesses run after policy validation + env sanitisation + output cap + wall-clock timeout. On Linux with `bwrap` installed and on macOS, the call is additionally wrapped by `bwrap` / `sandbox-exec` for filesystem and network confinement (auto-detected at startup). No separate sandbox daemon, no per-call rlimits (those would shrink the long-running gateway). New workspace tools follow WorkspaceFS / ProcessRunner / GitRunner as appropriate.
- **Approvals are blocking.** Pre-execution and mid-loop approvals halt the run; the run record persists in `awaiting_approval` until resolved. New gates use the `TaskApproval` shape.
- **Events are appended, not mutated.** Every state transition writes a `run_event` with a monotonic sequence. The SSE stream replays from `after_sequence`. New event types must follow the event-protocol v1 taxonomy (`run.*`, `turn.*`, `tool.*`, `policy.*`, `gap.*`, `error.*`) and be documented in `docs/events.md`.
- **Cost is `int64` micro-USD when present.** Never `float64` for money. Hecate records usage events for visibility; it does not enforce global spend controls.
- **OTel is first-class.** Every request gets a trace ID surfaced in the `X-Trace-Id` response header and persisted on the run record. New code paths add spans, not just log lines.

## Conventions (in brief)

Full standards: [`docs-ai/core/engineering-standards.md`](docs-ai/core/engineering-standards.md).

- **Comments explain _why_**, not what. State the trade-off.
- **Pointer vs value for optional fields**: pointer when zero is a valid
  distinct value (`Seed *int`, `ParallelToolCalls *bool`); value with
  `omitempty` when zero == API default (`PresencePenalty float64`).
- **`json.RawMessage`** for forward-compat passthrough fields.
- **Test naming**: `TestPackage_Behavior`. Table-driven where the variant set is obvious.
- **No emojis**, no plan/phase labels in commit messages or comments.
- **Conventional Commits**; `chore(agent):` for agent-doc-only changes. Don't auto-commit — propose a message and let the operator merge.

## Verification

Full ladder: [`docs-ai/core/verification.md`](docs-ai/core/verification.md).

- **Race suite is the floor for runtime/backend changes**: `go test -race -timeout 10m ./...` or `just test-race`. Race builds are large; if your default `$GOCACHE` is on a small volume, point it at the repo: `GOCACHE="$(pwd)/.gocache" go test -race ...`.
- **Vet Go changes**: run `go vet` on touched packages during iteration; use `go vet ./...` for broad backend changes or release prep.
- **Iteration**: run focused `go test` / `bun run test` commands for touched areas.
- **E2E**: `go test -tags e2e ./e2e/...`. Build tag `e2e` always required; sub-tags `ollama`, `docker` opt in. `PROVIDER_FAKE_KIND=local` is useful for synthetic local-model scenarios.
- **UI**: `cd ui && bun run typecheck` then `bun run test`. Never `bun test` (skips testing-library DOM setup).

## Recipes

| Task                                                                        | Where                                                                                                                                                                            |
| --------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Add a passthrough wire field (the seven-step chain — most-redone task here) | [`docs-ai/skills/providers/SKILL.md`](docs-ai/skills/providers/SKILL.md)                                                                                                         |
| Add an MCP tool / persisted run-event type / test helper cheat-sheet        | [`docs-ai/skills/backend/SKILL.md`](docs-ai/skills/backend/SKILL.md)                                                                                                             |
| UI recipes (SSE-driven state field, paired pickers, snapshot refresh)       | [`docs-ai/skills/ui/SKILL.md`](docs-ai/skills/ui/SKILL.md)                                                                                                                       |
| Native desktop app (sidecar lifecycle, bundling, Tauri commands)            | [`docs-ai/skills/tauri/SKILL.md`](docs-ai/skills/tauri/SKILL.md)                                                                                                                 |
| Cut a release tag                                                           | `bun scripts/release.ts vX.Y.Z` — checks worktree, snapshot dry-run, stamps Tauri versions, tags, pushes. Full procedure: [`docs-ai/tasks/release.md`](docs-ai/tasks/release.md) |
| Stamp Tauri version files                                                   | `bun scripts/stamp-version.ts` (or `just tauri-version`) — syncs Cargo.toml, package.json, tauri.conf.json to current git tag                                                    |

## Gotchas

- **`bun run test` ≠ `bun test`.** The latter skips the testing-library DOM setup and panics with `document[isPrepared]`. Always `bun run test` (which dispatches to vitest).
- **modernc/sqlite TIME-as-text format**: the driver writes `time.Time` using Go's default `time.Time.String()` format, which doesn't lex-compare with RFC3339Nano cutoffs and silently breaks the retention sweep. Always write timestamps as `t.UTC().Format(time.RFC3339Nano)` when the column is TEXT (see `internal/taskstate/sqlite.go` `AppendRunEvent`).
- **OpenAI/openAI parallel structs are intentional**: don't unify. Mirror fields when adding on either side.
- **Streaming `wireReq` plumbing**: when adding a passthrough field, plumb it into BOTH `Chat` and `ChatStream` `wireReq` constructions in `internal/providers/openai.go`. Forgetting one is the most common provider bug — non-stream tests pass; the field silently drops in production for any client using `stream: true`.
- **Capability-cache seeding** for provider tests: seed `cachedCaps` and `capsExpiry` to skip the discovery path. Snippet in [`docs-ai/skills/providers/SKILL.md`](docs-ai/skills/providers/SKILL.md).
- **Synthetic local providers** in tests: `PROVIDER_FAKE_KIND=local` for synthetic models in e2e.
- **mermaid `loop` is a reserved keyword**: don't use it as a sequence-diagram participant name. Use `Agent` or similar.
- **CodeQL CWE-190**: don't compute `make([]T, 0, len(x)+N)` with arithmetic — use plain `len(x)` and let `append` grow.
- **Env-PRECONFIGURED gate**: `PROVIDER_<NAME>_API_KEY` / `_BASE_URL` only auto-import into the CP store when `PROVIDER_<NAME>_PRECONFIGURED=1` is also set. E2E helpers (`hecateServer`, `startHecateProcess`) funnel through `autoPreconfiguredEnv` to inject the gate; new e2e spawn helpers must do the same or routed requests 400 with `no provider supports model …`.
- **`:8765` collisions across launches**: `just dev` / `just run` / `just serve` now run `just stop` first so a stale `./hecate` from another shell never blocks a relaunch (or a `docker run -p 8765:8765 …`). New scripts that spawn the binary should call `just stop` (or replicate the `lsof -ti:8765 | xargs kill` step).
- **API response envelope**: every Hecate-native `/hecate/v1/*` GET returns `{object, data}`. Compatibility endpoints (`/v1/models`, `/v1/chat/completions`, `/v1/messages`) keep provider-shaped contracts. Don't write a UI client that reads top-level fields — always read `payload.data.<field>` for Hecate-native endpoints and make test fixtures mirror the real envelope.

## Canonical docs

| Doc                                                                  | Covers                                                                                        |
| -------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| [`docs/architecture.md`](docs/architecture.md)                       | Request flow, lease semantics, storage tier matrix                                            |
| [`docs/agent-runtime.md`](docs/agent-runtime.md)                     | `agent_loop` tools, system prompt layers, cost model, retry-from-turn                         |
| [`docs/runtime-api.md`](docs/runtime-api.md)                         | Task / run / step / approval endpoints, queue + lease                                         |
| [`docs/events.md`](docs/events.md)                                   | Every event type at `/hecate/v1/events` with payload shapes                                   |
| [`docs/telemetry.md`](docs/telemetry.md)                             | OTel spans + metrics, OTLP wiring, status & gaps                                              |
| [`docs/security.md`](docs/security.md)                               | Local-first threat model, workspace safety, approvals, secrets, advisories                    |
| [`docs/providers.md`](docs/providers.md)                             | Provider catalog, configuration                                                               |
| [`docs/mcp.md`](docs/mcp.md)                                         | MCP server: tools, transport, configure                                                       |
| [`docs/external-agent-adapters.md`](docs/external-agent-adapters.md) | Hecate as an ACP client/operator: Chats runs Codex, Claude Code, Cursor Agent, and Grok Build |
| [`docs/deployment.md`](docs/deployment.md)                           | Compose profiles, image pinning, lost-token recovery                                          |
| [`docs/development.md`](docs/development.md)                         | Local build, testing, screenshot tooling, `[skip ci]` convention                              |
| [`docs/desktop-app.md`](docs/desktop-app.md)                         | Native Tauri 2.x app: distribution, current state, roadmap, footguns                          |

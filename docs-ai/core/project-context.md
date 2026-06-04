# Project context

Hecate is an open-source local AI runtime console. The main Go runtime runs the local HTTP service, embeds the React operator UI, routes OpenAI- and Anthropic-shaped client traffic to upstream LLM providers, runs Hecate Chat tools-on turns through visible `agent_loop` tasks, supervises external coding-agent adapters from Chats, runs queued `agent_loop` tasks with policy and approval gates, and emits OpenTelemetry traces for everything it does. Hecate is local-first, deny-by-default, runtime-aware, and storage-tiered (memory / sqlite). Every endpoint, config knob, and error message exists to answer five operator questions: what did Hecate just decide, why, what did it cost, what happens on the next failure, and where is the trace.

Hecate's own boundary is the runtime/control plane: projects, chats, tasks,
providers, supervised external agents, approvals, tool policy, storage, and
observability. Higher-level assistant behavior should be built from those
primitives through agent profiles, presets, project memory, and context
assembly rather than hidden prompt glue or provider-specific shortcuts.
Model-backed assistant turns should carry a small context-inspector packet:
execution mode, route/workspace metadata, source provenance, and visible
transcript counts. Do not store full prompt bodies, raw transcript text, file
contents, or adapter-private prompt packing in that packet.

## Repository layout

```
cmd/hecate/               main runtime entry: gateway service, embedded UI, MCP subcommand

pkg/types/                 public types (ChatRequest, Message, ContentBlock, ...)
                             — no internal/ imports

internal/api/              inbound HTTP shapes + handlers
                             OpenAIChatMessage, OpenAIMessageContent (uppercase)
internal/providers/        outbound HTTP per provider (openai, anthropic)
                             openAIChatMessage, openAIMessageContent (lowercase)
                             — same JSON shape as api/, deliberate duplication
internal/orchestrator/     task runtime (queue, runner, agent_loop, sandbox)
internal/workspacefs/      shared workspace path resolver for file/search/write
                             operations owned by Hecate
internal/processrunner/    bounded local subprocess seam: cwd, env, timeout,
                             streaming output, output caps
internal/gitrunner/        Git-specific runner for Hecate-owned Git helpers
internal/sandbox/          policy validation + OS isolation wrapper for tool
                             subprocesses; shell uses ProcessRunner, broad git_exec
                             still runs through this executor
internal/taskstate/        task / run / step / artifact / approval persistence
internal/storage/          sqlite client wrappers
internal/retention/        retention worker (subsystems: traces, usage_events, audit,
                             provider_history, turn_events,
                             chat_approvals)
internal/mcp/              stdio MCP server (read tools + write tools)
internal/agentadapters/    ACP/process adapters for Codex, Claude Code, Cursor,
                             Grok Build
internal/chat/        chat transcript persistence and runtime linkage
internal/modelcaps/        model tool-capability merge logic and defaults

ui/                        React/Vite operator UI (embedded via //go:embed ui/dist)
e2e/                       binary-startup tests, build tag e2e (sub-tags: ollama, docker)
docs/                      long-form references (canonical product/runtime docs)

CLAUDE.md                  Claude Code compatibility shim importing AGENTS.md
docs-ai/                   canonical, provider-neutral agent instruction layer (this directory)
```

## Architecture rings

The codebase has three concentric rings; cross-ring imports go inward only:

- **`pkg/types/`** — public types, no `internal/` imports. The wire-shape contract.
- **`internal/api/`** — inbound HTTP shapes + handlers. Translates HTTP requests into internal types; never touches providers directly.
- **`internal/providers/`** — outbound HTTP per provider (OpenAI-compat, Anthropic). Translates internal types to provider wire shapes. Never imports `internal/api/`.
- **`internal/orchestrator/`** — task runtime (queue, runner, `agent_loop`, sandbox boundary). Sits above providers, called by api.
- **`internal/<feature>/`** — gateway services (governor, router, retention, taskstate, mcp, …). Each owns one concern.

The api↔providers parallel-struct duplication (`OpenAIChatMessage` ↔ `openAIChatMessage`) is **intentional**. It keeps `internal/providers/` free of `internal/api/` imports and lets the wire shapes evolve independently. See [`../skills/providers/SKILL.md`](../skills/providers/SKILL.md) for full reasoning.

## Storage tier rule

Every backend-bound concern (taskstate, chat, approvals, governor, retention history) ships with two tiers, mirrored exactly:

- `memory` — in-process, default, perfect for `go test` and `just dev`.
- `sqlite` — single-file persistence via `modernc.org/sqlite` (no CGO).

When adding a new persisted thing, mirror both. Add a `<thing>_test.go` that runs against memory and sqlite.

## Toolchain pins

- **Go**: see `go.mod` for the exact pinned version. CGO is not used; `modernc.org/sqlite` is the pure-Go sqlite driver.
- **Task runner**: just. Use `just <recipe>` for repo-level build/test/dev flows; do not add Makefile targets or document `make ...` commands.
- **UI / website package manager**: Bun (pinned via `packageManager` in
  `ui/package.json` and `website/package.json`). Lockfiles are `bun.lock`; there
  is no `package-lock.json`. Use `bun install`, `bun run <script>`,
  `bun add <pkg>`, `bun x <tool>`. Do not introduce npm/yarn/pnpm lockfiles or
  workflow steps.
- **Native app toolchain**: Rust + Cargo via rustup for Tauri work (`tauri/`, `just tauri-*`). Backend and UI-only work should not require Cargo.
- **UI stack**: React 19, TypeScript, Vite, Vitest + Testing Library + jsdom. Plain CSS with design tokens in `ui/src/styles.css` — no CSS-in-JS, no utility-class framework.
- **Critical command distinction**: `bun run test` ≠ `bun test`. The latter skips the testing-library DOM setup and panics with `document[isPrepared]` errors. Always `bun run test` (which dispatches to vitest).

## Risky and sensitive areas

These earn extra scrutiny; changes here are not drive-by territory.

- **Workspace and subprocess boundary** (`internal/workspacefs/`, `internal/processrunner/`, `internal/gitrunner/`, `internal/sandbox/`) — Hecate-mediated file/search/write operations resolve paths through WorkspaceFS. Shell commands go through the sandbox executor and ProcessRunner; Hecate-owned Git helpers use GitRunner where they do not need the broad `git_exec` shell-shaped interface. The sandbox layer still applies policy validation, env sanitisation, output caps, wall-clock timeout, and optional `bwrap` / `sandbox-exec` OS isolation (auto-detected at startup via `internal/sandbox/wrapper.go`, no opt-in flag). No separate `sandboxd` daemon — the safety properties run inline. CPU / FD / address-space caps are _not_ applied per-call (`setrlimit` would shrink the long-running gateway) — operators who need them run under systemd or in a container with `--cpus` / `--memory` flags. New workspace tool kinds use WorkspaceFS / ProcessRunner / GitRunner as appropriate rather than raw filesystem, process, or git calls. See `docs/runtime/sandbox.md` for the layer model and `docs/runtime/agent-runtime.md` for the network-egress policy that sits on top.
- **Approval lifecycle** (`internal/taskstate`, `awaiting_approval`) — pre-execution and mid-loop approvals halt the run. New gates use the same `TaskApproval` shape.
- **Retention worker** (`internal/retention`) — high-cardinality history sweep. Subsystems: `trace_snapshots`, `usage_events`, `audit_events`, `provider_history`, `turn_events`, `chat_approvals`. Persisted things must mirror.
- **Usage/cost fields** — money fields are `int64` micro-USD (`1_000_000` = `$1`) when present. Never `float64`; Hecate records usage events for visibility, not spend enforcement.
- **No auth layer.** Every request is processed as the operator. The gateway binds to `127.0.0.1` by default; bind elsewhere only behind a reverse proxy or firewall.

## Which doc answers which question

Long-form references live in `docs/`. Update them in the same change as the
code, not as a follow-up. Don't restate their content here — link and move on.

| Question                                                                      | Doc                                                                                                                                                                                              |
| ----------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| How does a request flow through the gateway? What are the storage tiers?      | [`docs/contributor/architecture.md`](../../docs/contributor/architecture.md)                                                                                                                     |
| What `agent_loop` tools exist? What are the system prompt layers? Cost model? | [`docs/runtime/agent-runtime.md`](../../docs/runtime/agent-runtime.md)                                                                                                                           |
| What are the task / run / step / approval HTTP endpoints?                     | [`docs/runtime/runtime-api.md`](../../docs/runtime/runtime-api.md)                                                                                                                               |
| What does this SSE event payload look like?                                   | [`docs/runtime/events.md`](../../docs/runtime/events.md)                                                                                                                                         |
| What OTel spans and metrics does the gateway emit?                            | [`docs/runtime/telemetry.md`](../../docs/runtime/telemetry.md)                                                                                                                                   |
| How do I configure a provider? What providers are supported?                  | [`docs/operator/providers.md`](../../docs/operator/providers.md)                                                                                                                                 |
| How do I configure MCP? What tools does the server expose?                    | [`docs/runtime/mcp.md`](../../docs/runtime/mcp.md)                                                                                                                                               |
| How do Hecate Chat segments and model capabilities work?                      | [`docs/runtime/chat-sessions.md`](../../docs/runtime/chat-sessions.md), [`docs/design/accepted/hecate-chat-model-capabilities.md`](../../docs/design/accepted/hecate-chat-model-capabilities.md) |
| How do external-agent adapters work?                                          | [`docs/runtime/external-agent-adapters.md`](../../docs/design/accepted/external-agent-adapters.md)                                                                                               |
| How do I deploy? What are the Compose profiles?                               | [`docs/operator/deployment.md`](../../docs/operator/deployment.md)                                                                                                                               |
| How do I build and test locally? What does `[skip ci]` mean?                  | [`docs/contributor/development.md`](../../docs/contributor/development.md)                                                                                                                       |
| What sandbox isolation layers are shipped? How do namespaces work?            | [`docs/runtime/sandbox.md`](../../docs/runtime/sandbox.md)                                                                                                                                       |

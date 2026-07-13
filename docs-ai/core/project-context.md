# Project context

Hecate is an open-source local AI operations console for supervised agent work
and project orchestration. The main Go runtime runs the local HTTP service,
embeds the React operator UI, routes OpenAI- and Anthropic-shaped client traffic
to upstream LLM providers, runs Hecate Chat tools-on turns through visible
`agent_loop` tasks, supervises External Agents from Chats, manages projects,
roles, assignments, handoffs, context, memory candidates, approvals, artifacts,
usage, and emits OpenTelemetry traces for everything it does. Hecate is
local-first in the operational sense: the runtime and UI run on the operator's
machine by default, Hecate-owned state is local in local deployments
(`memory` / `sqlite`) and can use Postgres for hosted/cloud-runtime
deployments, and the gateway binds to 127.0.0.1 by default. It is not
local-only; it can route to cloud providers and supervise external coding-agent
CLIs with their own accounts. Every endpoint, config knob, and error message
exists to answer five operator questions: what did Hecate just decide, why,
what did it cost, what happens on the next failure, and where is the trace.

Hecate's own boundary is the runtime/control plane: projects, chats, tasks,
providers, supervised external agents, approvals, tool policy, storage, and
observability. Higher-level assistant behavior should be built from those
primitives through agent presets, runtime profiles, project memory, and context
assembly rather than hidden prompt glue or provider-specific shortcuts.
The Projects surface is the operator cockpit for coordinating project-scoped
teams of agents: project roles describe who should do the work, assignments and
handoffs record collaboration state, Tasks and Chats remain the execution
surfaces, project memory/context carries reviewed knowledge forward, and the
project skills registry exposes local `SKILL.md` metadata for roles and agent
presets without granting tools, injecting bodies, or executing skill scripts.
The operator UI can manage agent presets and pick registered project skills for
roles/presets; those selections remain metadata until launch-time resolution.
Projects V1 is a usable local cockpit substrate, not a Planner/Manager agent:
new project work should improve setup, inspection, evidence, and deliberate
operator actions unless a separate proposal changes that boundary.
Agent preset responses include immutable built-in presets such as
`implementation`, `planning`, and `review_qa`; those built-ins are selectable
defaults, not persisted rows or operator-editable project memory.
Project roots are concrete checkouts, not project identity: one project can span
the main checkout and linked Git worktrees, while newly discovered worktree roots
stay inactive until the operator enables them. Context discovery must not treat
nested `.worktrees`, `.claude/worktrees`, or other nested Git checkouts as
inherited guidance for the parent root.
Work items and assignments may select a concrete project root; launch resolution
uses assignment root, then work-item root, then project default root, then the
first active root. Worktree creation is an explicit operator action and is
bounded to a direct child of the selected base root's `.worktrees/` directory
in V1; do not create or assume sibling/nested checkout paths outside registered
roots.
Preset memory/source policies now control whether assignment context packets
mark project memory and source metadata active, visible-only, or omitted. Native
project assignments can include bounded project memory and portable `AGENTS.md`
workspace-instruction bodies only when the resolved preset explicitly includes
them; host-specific guidance files and skill bodies remain metadata-only.
Project-linked Hecate Chat uses a bounded project prelude in the chat system
prompt and records that policy in the context packet; project context-source
file bodies, host-specific guidance file bodies, and `SKILL.md` bodies are not
loaded into chat prompts in V1. External Agent chat and External Agent
project-assignment starts record project metadata for inspection, but Hecate
does not inject project memory bodies, source bodies, or skill bodies into
adapter prompts; the adapter owns private prompt packing inside its native
session.
Cairnline is the sole production authority for portable Projects coordination.
The embedded Cairnline service stores project identity, roots, context-source
and skill metadata, roles, work items, assignments, collaboration artifacts,
handoffs, accepted project memory, memory candidates, and Project Assistant
proposal records. Hecate keeps the `/hecate/v1/projects*` API and operator UI as
its facade over that state; do not add a second Hecate-native portable store,
backend selector, mirror, migration route, or fallback authority.

Hecate still owns execution concerns: Agent Presets, provider/model defaults,
task and External Agent dispatch, approvals, sandbox and workspace operations,
runtime references, context snapshots, traces, and the operator shell.
Assignment launch and preflight therefore combine Cairnline coordination state
with Hecate runtime policy. The shared launch-plan seam validates preset surface
compatibility; native assignment tasks snapshot the preset id and tools posture
and enforce write/network posture through task sandbox fields. A tools-disabled
snapshot runs as a supervised model-only task: it exposes no native, Project
Assistant, or MCP tools, starts no MCP host, and rejects unexpected calls before
dispatch. Preset-backed native
HTTP/search tools fail closed when that snapshot disables network access;
read-only tasks omit and reject broad shell, Git, file-write, and interactive
terminal surfaces while retaining structured inspection and proposal-only
edits. Legacy/manual tasks without a tools snapshot keep their prior tool
behavior, and tasks without a preset snapshot keep their prior native
network-tool behavior, so do not infer policy from an absent snapshot or a
zero-valued sandbox flag. Persist
task/run or chat-session references in the Hecate project-runtime overlay,
while assignment lifecycle state remains in Cairnline. Linked External Agent
reconciliation follows the same split.

`internal/cairnlinebridge` is the live mapping boundary between Cairnline's
agent-neutral coordination model and Hecate's API/runtime views. Keep the bridge
free of Hecate execution authority: Cairnline assignment metadata is intent,
not permission to bypass Hecate approvals, sandboxing, model policy, or adapter
policy. A separately installed Cairnline connector may be added later, but the
current runtime embeds Cairnline and does not expose transition or sidecar
diagnostic routes.
Model-backed assistant turns should carry a small context-inspector packet:
execution mode, route/workspace metadata, source provenance, and visible
transcript counts. Do not store full prompt bodies, raw transcript text, file
contents, or adapter-private prompt packing in that packet.
Hosted/cloud-runtime work keeps the local architecture but changes the request
boundary. New Hecate-native HTTP routes must be classified in
`internal/api/remote_runtime_policy.go` as remote-safe or local-only; the route
coverage test guards the registration list. External Agent subprocesses
launched from a cloud-identified request must use the cloud process-env helper
so personal CLI login homes and broad local auth-token envs are not inherited.

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
internal/storage/          SQLite/Postgres SQL clients + dialect helpers
internal/retention/        retention worker (subsystems: traces, usage_events, audit,
                             provider_history, turn_events,
                             chat_approvals)
internal/mcp/              stdio MCP server (read tools + write tools)
internal/agentadapters/    ACP/process adapters for Codex, Claude Code, Cursor,
                             Grok Build
internal/chat/        chat transcript persistence and runtime linkage
internal/modelcaps/        model tool-capability merge logic and defaults
internal/cairnlinebridge/  live Cairnline-to-Hecate mapping and coordination adapter

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

Every Hecate-owned backend-bound concern (taskstate, chat, approvals, governor,
retention history) ships with mirrored tiers:

- `memory` — in-process, default, perfect for `go test` and `just dev`.
- `sqlite` — single-file local persistence via `modernc.org/sqlite` (no CGO).
- `postgres` — hosted/cloud-runtime persistence via `pgx`.

When adding a new persisted thing, mirror memory and both SQL backends unless
the operator explicitly scopes the change differently. Add a `<thing>_test.go`
that runs against memory and SQLite, and add or extend the opt-in Postgres
smoke when real Postgres behavior matters.

Portable Projects coordination is the explicit exception: Cairnline owns its
SQLite-backed graph. Do not add Hecate memory/SQLite/Postgres copies of
Cairnline-owned project records. Hecate project-runtime overlays still follow
Hecate's normal storage-tier rule.

When adding or moving a backend selector, also update the two selector guards:
`internal/config/config_test.go` for validation/DSN fan-out and
`cmd/hecate/banner_test.go` for runtime SQL-client requirements. If the task
queue backend changes, update the closed telemetry label set and metric test so
Postgres remains observable as `hecate.queue.backend=postgres`.

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
- **No built-in multi-user auth layer in local mode.** Every local-mode request
  is processed as the operator. The gateway binds to `127.0.0.1` by default;
  bind elsewhere only behind a reverse proxy or firewall. Remote runtime mode
  trusts remote identity headers only after the internal runtime secret is
  validated. Remote runtime mode disables local model providers unless
  `HECATE_REMOTE_ALLOW_LOCAL_PROVIDERS=1` is set for an intentionally isolated
  sidecar deployment. The provider gate is based on `kind=local`, not URL
  destination inspection.

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
| How do External Agents work?                                                  | [`docs/runtime/external-agents.md`](../../docs/runtime/external-agents.md)                                                                                                                       |
| How do I deploy? What are the Compose profiles?                               | [`docs/operator/deployment.md`](../../docs/operator/deployment.md)                                                                                                                               |
| How do I build and test locally? What does `[skip ci]` mean?                  | [`docs/contributor/development.md`](../../docs/contributor/development.md)                                                                                                                       |
| What sandbox isolation layers are shipped? How do namespaces work?            | [`docs/runtime/sandbox.md`](../../docs/runtime/sandbox.md)                                                                                                                                       |

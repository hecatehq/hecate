# External Agent Adapters — Candidate (RFC)

> **Status:** accepted for alpha MVP. Adapter discovery, Agent Chat,
> memory/SQLite persistence, long-lived ACP sessions, live streaming,
> cancellation, raw diagnostics, and workspace diff capture are implemented.
> Each prompt has stable run metadata on the assistant message, and each chat
> records the native ACP session id reused across turns. API shape may still
> change before a stable release.
> **Related:** [ACP bridge](../acp.md), [Runtime API](../runtime-api.md),
> [Agent runtime](../agent-runtime.md), [Agent event protocol](event-protocol-v1.md).
> **Owner:** see [`AGENTS.md`](../../AGENTS.md).

This RFC defines how Hecate should let an operator chat with external coding
agents such as Codex CLI, Claude Code, Cursor Agent, and future agent CLIs
without pretending those agents are model providers.

The core distinction:

| Concept | Examples | What Hecate controls |
|---|---|---|
| Model provider | OpenAI, Anthropic, Ollama, LM Studio | Request routing, cache, pricebook, provider health, model choice |
| Agent adapter | Codex ACP, Claude ACP, Cursor Agent ACP, future ACP-capable coding agents | Process lifecycle, workspace, prompt/session flow, output capture, artifacts |
| Protocol adapter | ACP, MCP, OpenAI-compatible HTTP, Anthropic Messages | How another system talks to or from Hecate |

Providers answer LLM calls. Agent adapters drive coding-agent loops.

## Problem

Hecate already has two strong surfaces:

- **Chats** — model/provider conversations routed through the gateway.
- **Tasks** — durable agent/runtime work with approvals, events, artifacts, and
  workspace state.

Using Hecate with Codex, Claude Code, or Cursor Agent needs a third shape that
is conversation-first like Chats but runtime-aware like Tasks. A user wants to
type in Hecate and get a response from Codex, Claude Code, or Cursor Agent,
while Hecate still records what happened, captures output, and eventually shows
patches/artifacts.

Putting Codex, Claude Code, or Cursor Agent in the provider/model dropdown would be wrong:

- They are full agents, not models.
- They carry their own tool loop and permission model.
- They may own their own credentials and provider routing.
- Their costs may be externally managed or opaque to Hecate.

## Goals

- Add a product and backend seam for **Agent Chat** alongside Model Chat.
- Support Codex, Claude, and Cursor Agent through ACP-capable adapters first.
- Keep provider/model routing unchanged.
- Let Hecate supervise external agent sessions: start, stream, cancel,
  timeout, capture exit status.
- Store enough run/session state that UI and future clients can replay the
  conversation.
- Capture ACP updates as runtime output first, then richer structured events as
  the adapter surface matures.
- Normalize ACP output into readable transcript text without discarding the raw
  diagnostic stream needed for future debugging.
- Capture workspace diff / patch artifacts after a run when the workspace is a
  Git repo.
- Use ACP for outbound external-agent sessions when an adapter is available.

## Non-goals

- Do not make Codex, Claude Code, or Cursor Agent fake providers.
- Do not add a second one-shot CLI compatibility layer while the project is
  still alpha.
- Do not claim exact cost accounting for external agents until the adapter can
  report it.
- Do not build a plugin marketplace or broad agent-runtime SDK yet.
- Do not support remote multi-user agent sessions in this RFC.

## Recommended Shape

Start with **ACP session adapters**.

```text
Hecate Chats
  -> Target: Agent
  -> Agent adapter: Codex / Claude Code / Cursor Agent
  -> Workspace
  -> Prompt
  -> Native ACP session
  -> Streamed output + artifacts
```

The implementation keeps one adapter process and one native ACP session alive
per Hecate Agent Chat session. Each prompt becomes the next ACP turn in that
session.

## UI Model

Chats should gain a target switch:

```text
Target: Model | Agent
```

When `Model` is selected, the existing provider/model controls remain.

When `Agent` is selected, the primary controls become:

```text
Agent: Hecate Native | Codex | Claude Code | Cursor Agent
Workspace: /path/to/repo
Prompt: message
```

The conversation transcript should remain one surface. Runtime metadata should
show that this response came from an agent adapter, not a provider/model route:

```text
Codex · external agent
Workspace: /Users/.../hecate
Cost: external / unknown
Patch: 3 files changed
```

## Backend Model

Introduce an adapter package without coupling it to provider routing:

```text
internal/agentadapters/
  adapter.go
  registry.go
  process/
    runner.go
  codex/
    adapter.go
  claude/
    adapter.go
```

Candidate interface:

```go
type Adapter interface {
    ID() string
    DisplayName() string
    Check(ctx context.Context) (Status, error)
    Run(ctx context.Context, req RunRequest) (<-chan Event, error)
}
```

Candidate request:

```go
type RunRequest struct {
    SessionID        string
    MessageID        string
    Workspace        string
    Prompt           string
    Timeout          time.Duration
    Environment      map[string]string
}
```

Candidate event shape:

```go
type Event struct {
    Type      string
    Text      string
    ExitCode  *int
    Error     string
    OccurredAt time.Time
}
```

The adapter package should not import `internal/api`. API handlers translate
HTTP shapes into adapter requests.

## API Options

Two options are plausible.

| Option | Shape | Pros | Cons |
|---|---|---|---|
| Add agent mode to chat sessions | Extend `/v1/chat/sessions` with `target_type=model|agent` | One user-facing Chats surface; easier history | Risks mixing model-provider and agent-runtime semantics too early |
| Add explicit agent-chat API | `/v1/agent-chat/sessions/*` | Clean boundary; easy to change during alpha | UI has to bridge two chat APIs |

Recommendation for alpha: **explicit agent-chat API** first. Once behavior is
stable, Chats UI can render both model-chat and agent-chat sessions behind one
experience.

Implemented MVP endpoints:

```text
GET  /v1/agent-adapters
GET  /v1/agent-chat/sessions
POST /v1/agent-chat/sessions
GET  /v1/agent-chat/sessions/{id}
GET  /v1/agent-chat/sessions/{id}/stream
POST /v1/agent-chat/sessions/{id}/messages
POST /v1/agent-chat/sessions/{id}/cancel
DELETE /v1/agent-chat/sessions/{id}
```

Message creation is still a blocking POST for the submitted prompt, but clients
can subscribe to the session SSE stream first to receive partial output while
the external process is running. History is memory-backed by default and SQLite
backed when `GATEWAY_CHAT_SESSIONS_BACKEND=sqlite`. The store also keeps the
native ACP session id. On the next prompt after a gateway restart, Hecate passes
that id to the adapter through ACP `session/load` when the adapter advertises
load-session support; otherwise it creates a fresh native session and keeps the
Hecate transcript intact.

## Process Adapter Behavior

For a single message:

1. Validate the adapter exists and is available on `PATH`.
2. Validate the workspace path.
3. Build a sanitized environment. Do not pass gateway secrets by default.
4. Spawn the adapter command in the workspace.
5. Feed the prompt via the most reliable CLI mechanism for that adapter.
6. Stream ACP output as events.
7. Enforce timeout and cancellation.
8. Capture exit status and final error.
9. If the workspace is a Git repo, capture `git diff --stat` and `git diff`
   as artifacts.

Adapter configs:

```text
codex:
  command: codex
  args: [...]
  prompt_mode: stdin or arg

claude_code:
  command: claude
  args: [...]
  prompt_mode: stdin or arg

cursor_agent:
  command: cursor-agent
  args: [...]
  prompt_mode: stdin or arg
```

Exact command lines should be discovered from the installed CLI behavior before
locking the implementation. Zed is intentionally not listed here because it is
an ACP/editor client rather than a headless external agent process.

## Relationship To ACP

ACP is useful in two directions:

```text
Zed / JetBrains -> ACP -> Hecate
Hecate -> ACP -> Codex / Claude / Cursor Agent
```

The inbound bridge (`cmd/hecate-acp`) lets editor agent panels talk to Hecate.
The outbound adapter layer lets Hecate talk to ACP-capable external coding
agents. They share protocol vocabulary but stay separate processes and code
paths.

## Observability

Agent adapter runs should emit Hecate runtime events. Initial event names can be
simple and explicit:

```text
agent_adapter.started
agent_adapter.output_delta
agent_adapter.completed
agent_adapter.failed
agent_adapter.cancelled
agent_adapter.patch_captured
```

When the event-protocol candidate stabilizes, these should map into typed
`run.*`, `assistant.*`, `tool.*`, `artifact.*`, and `error.*` envelopes instead
of becoming a permanent parallel taxonomy.

OTel spans should include:

- `hecate.agent_adapter.id`
- `hecate.agent_adapter.command`
- `hecate.workspace.path`
- `hecate.run.id`
- `hecate.session.id`
- exit code / failure class

Do not log prompts by default outside existing debug/redaction rules.

## Security And Policy

External agent adapters are high-risk because they run third-party CLIs that may
themselves execute tools.

First-version safety rules:

- Require an explicit workspace path.
- Validate and canonicalize the workspace directory before storing a session.
- Use sanitized env by default.
- Do not pass provider API keys unless the adapter config explicitly opts in.
- Enforce timeout and cancellation.
- Capture output with the same output-size limits used by task tools.
- Mark cost as `external` / `unknown` unless the adapter reports structured
  usage.
- Make the UI visibly distinguish external-agent output from provider/model
  output.

Open policy question: should external agents run through the same
`internal/sandbox` wrapper as shell tools, or through a separate process runner
that can support interactive stdio better? The first implementation should
prefer reuse where possible, but not at the cost of a broken chat stream.

## Acceptance Criteria For First Implementation

- [x] `GET /v1/agent-adapters` reports built-in adapter definitions and availability.
- [x] Hecate can run Codex, Claude Code, or Cursor Agent prompts through a supervised ACP session.
- [x] Output is captured and displayed in the UI.
- [x] Raw ACP diagnostics are retained when normalized text hides adapter quirks.
- [x] Chats show structured activity markers for start/running/output/files-changed/final failure states.
- [x] Timeout marks the run failed with a stable error.
- [x] Final response and raw output are replayable after refresh, and durable across gateway restarts when SQLite chat sessions are enabled.
- [x] Workspace diff is captured when the workspace is a Git repo.
- [x] Docs clearly state cost is external/unknown for these adapters.
- [x] Streaming output reaches the UI while the process is still running.
- [x] Cancellation signals the ACP turn and marks the session/run cancelled.
- [x] Session history is durable across gateway restarts when the chat-session backend is SQLite.
- [ ] Dedicated patch review/apply/revert UX for captured diffs.
- [ ] Deeper adapter-specific structured mappers for ACP tool/terminal output.
- [ ] Decision on whether Agent Chat converges onto full Hecate Tasks/Runs.

## Open Questions

- Which non-interactive CLI invocation is stable across supported agent CLIs?
- Should Agent Chat eventually converge with Tasks so external-agent prompts
  get durable run events, approvals, and artifacts from the same substrate?
- How much of the external process environment should be configurable by the
  operator?
- Should Hecate allow these adapters in the desktop app by default, given the
  external CLI dependency and filesystem access?

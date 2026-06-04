# External Agent Adapters — Candidate (RFC)

> **Status:** accepted; partially implemented alpha baseline.
> **Current source of truth:** [External agent adapters](../../runtime/external-agent-adapters.md),
> [Chat sessions](../../runtime/chat-sessions.md), and [Runtime API](../../runtime/runtime-api.md).
> **Next action:** keep improving adapter-specific structured mapping, patch
> review UX, and convergence with task-runtime primitives.

This RFC defines how Hecate should let an operator chat with external coding
agents such as Codex CLI, Claude Code, Cursor Agent, Grok Build, and future agent CLIs
without pretending those agents are model providers.

The core distinction:

| Concept          | Examples                                                                                  | What Hecate controls                                                            |
| ---------------- | ----------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| Model provider   | OpenAI, Anthropic, Ollama, LM Studio                                                      | Request routing, usage reporting, provider health, model choice                 |
| Agent adapter    | Codex ACP, Claude ACP, Cursor Agent ACP, Grok Build ACP, future ACP-capable coding agents | Process lifecycle, workspace, prompt/session flow, output capture, diff capture |
| Protocol adapter | ACP, MCP, OpenAI-compatible HTTP, Anthropic Messages                                      | How another system talks to or from Hecate                                      |

Providers answer LLM calls. Agent adapters drive coding-agent loops.

## Problem

Hecate already has two strong surfaces:

- **Chats** — model/provider conversations routed through the gateway.
- **Tasks** — durable agent/runtime work with approvals, events, artifacts, and
  workspace state.

Using Hecate with Codex, Claude Code, Cursor Agent, or Grok Build needs a third shape that
is conversation-first like Chats but runtime-aware like Tasks. A user wants to
type in Hecate and get a response from Codex, Claude Code, Cursor Agent, or Grok Build,
while Hecate still records what happened, captures output, and eventually shows
diffs and later artifacts.

Putting Codex, Claude Code, Cursor Agent, or Grok Build in the provider/model dropdown would be wrong:

- They are full agents, not models.
- They carry their own tool loop and permission model.
- They may own their own credentials and provider routing.
- Their costs may be externally managed or opaque to Hecate.

## Goals

- Add a product and backend seam for **External Agent chat** alongside
  Hecate-owned chat.
- Support Codex, Claude, Cursor Agent, and Grok Build through ACP-capable adapters first.
- Keep provider/model routing unchanged.
- Let Hecate supervise external agent sessions: start, stream, cancel,
  timeout, capture exit status.
- Store enough run/session state that UI and future clients can replay the
  conversation.
- Capture ACP updates as runtime output first, then richer structured events as
  the adapter surface matures.
- Normalize ACP output into readable transcript text without discarding the raw
  diagnostic stream needed for future debugging.
- Capture workspace diff after a run when the workspace is a Git repo.
- Use ACP for outbound external-agent sessions when an adapter is available.

## Non-goals

- Do not make Codex, Claude Code, Cursor Agent, or Grok Build fake providers.
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
  -> Target: External Agent
  -> Agent adapter: Codex / Claude Code / Cursor Agent / Grok Build
  -> Workspace
  -> Prompt
  -> Native ACP session
  -> Streamed output + captured diff
```

The implementation keeps one adapter process and one native ACP session alive
per External Agent chat session. Each prompt becomes the next ACP turn in that
session.

## UI Model

Chats exposes one agent picker. `Hecate` is the first option and keeps the
provider/model controls plus the tools toggle for direct model chat vs Hecate
Agent task execution. External adapters are separate agent choices in the same
picker:

```text
Agent: Hecate | Codex | Claude Code | Cursor Agent
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

The adapter code lives behind `internal/agentadapters/` without coupling to
provider routing or `internal/api` request structs:

```text
internal/agentadapters/
  acp_session.go
  approvals.go
  errors.go
  probe.go
  registry.go
  version.go
```

The current runtime shape is ACP-first:

- `registry.go` declares built-in adapters, direct commands, managed launcher
  metadata, tested version ranges, and lightweight auth hints.
- `probe.go` performs the explicit "can this adapter really start?" check by
  spawning the adapter, completing ACP `initialize`, opening a no-op session,
  and classifying the result.
- `acp_session.go` owns the long-lived adapter process, native ACP session,
  prompt turns, streaming update normalization, cancellation, shutdown, usage
  updates, raw diagnostics, and Git diff capture.
- `approvals.go` maps ACP `RequestPermission` into Hecate's external-agent
  approval rows, grants, REST/SSE surfaces, and OTel metrics.

API handlers translate HTTP shapes into adapter/session manager calls. The
adapter package remains independent from provider routing: external coding
agents are not model providers, and provider APIs are not threaded into this
path unless a future adapter explicitly opts in.

## API Shape

The shipped alpha shape uses one chat-session API:
`/hecate/v1/chat/sessions/*`. A session is owned by `agent_id`: `hecate` for
Hecate-owned chat, or an external adapter id such as `codex`, `claude_code`, or
`cursor_agent`. Individual messages record the execution mode that produced
them. This replaced the earlier model-vs-agent session-type sketch from this
RFC; there is no separate live target selector field.

Implemented MVP endpoints:

```text
GET  /hecate/v1/agent-adapters
POST /hecate/v1/agent-adapters/{id}/probe
GET  /hecate/v1/agent-adapters/{id}/health
POST /hecate/v1/agent-adapters/{id}/refresh-launcher
GET  /hecate/v1/chat/sessions
POST /hecate/v1/chat/sessions
GET  /hecate/v1/chat/sessions/{id}
GET  /hecate/v1/chat/sessions/{id}/stream
POST /hecate/v1/chat/sessions/{id}/messages
GET  /hecate/v1/chat/sessions/{id}/messages/{message_id}/files
GET  /hecate/v1/chat/sessions/{id}/messages/{message_id}/files/{path}
POST /hecate/v1/chat/sessions/{id}/messages/{message_id}/revert
POST /hecate/v1/chat/sessions/{id}/cancel
DELETE /hecate/v1/chat/sessions/{id}
GET  /hecate/v1/chat/sessions/{id}/approvals
GET  /hecate/v1/chat/sessions/{id}/approvals/{approval_id}
POST /hecate/v1/chat/sessions/{id}/approvals/{approval_id}/resolve
POST /hecate/v1/chat/sessions/{id}/approvals/{approval_id}/cancel
GET  /hecate/v1/chat/grants
DELETE /hecate/v1/chat/grants/{grant_id}
```

Message creation is still a blocking POST for the submitted prompt, but clients
can subscribe to the session SSE stream first to receive partial output while
the external process is running. History follows `HECATE_BACKEND`; `sqlite`
persists sessions across restarts. The store also keeps the native ACP session
id. On the next prompt after a gateway restart, Hecate passes that id to the
adapter through ACP `session/load` when the adapter advertises load-session
support; otherwise it creates a fresh native session and keeps the Hecate
transcript intact.

## Adapter Session Behavior

When an External Agent chat session is created:

1. Resolve the adapter through a direct ACP command or a Hecate-managed launcher.
   Codex and Claude can use local `npx` managed launchers; Cursor currently
   comes from `cursor-agent acp`.
2. Validate and canonicalize the workspace path.
3. Build a sanitized process environment. Gateway/provider secrets are not
   forwarded by default.
4. Spawn the ACP adapter in the selected workspace.
5. Complete ACP `initialize` and `session/new`.
6. Store the native ACP session id on the Hecate chat session.

For each prompt in that chat, Hecate:

- Sends the next ACP turn.
- Normalizes ACP updates into transcript text, structured activity records, raw
  diagnostics, usage telemetry, and approval requests.
- Enforces timeout, cancellation, turn ceilings, wall-clock limits, and idle
  cleanup.
- Captures `git diff --stat` and `git diff --binary` onto the assistant message
  when the workspace is a Git repo.

For later prompts in the same External Agent chat session, Hecate reuses the same
adapter process and native ACP session. If the gateway restarts and SQLite chat
storage is enabled, Hecate keeps the transcript and saved native session id. On
the next prompt it asks the adapter to load that native session when the adapter
advertises load-session support; otherwise it starts a fresh native ACP session
and keeps the Hecate-side transcript intact.

Managed launchers are intentionally local and operator-controlled. Hecate writes
small wrapper scripts into the user cache directory or `HECATE_AGENT_ADAPTERS_DIR`,
refreshes one adapter on demand, and removes stale launcher scripts at startup
when the built-in adapter list changes.

## Relationship To ACP

Hecate uses ACP as an outbound adapter protocol:

```text
Hecate -> ACP -> Codex / Claude / Cursor Agent
```

The adapter layer lets Hecate talk to ACP-capable external coding agents while
keeping provider routing and Hecate-owned task runs separate.

## Observability

External Agent chat currently has three observability surfaces:

- The per-session SSE stream emits typed `session_update`,
  `approval.requested`, and `approval.resolved` events.
- Assistant messages carry stable run metadata (`run_id`, timestamps, duration,
  trace ids, native session id), structured activity records, raw ACP
  diagnostics, usage updates, and captured diff data.
- OpenTelemetry spans and metrics cover `chat.run`, adapter probe
  outcomes, approval request/resolve paths, approval timeout/grant counters,
  cancellation reasons, output byte counts, and diff-capture state.

Important attributes include:

- `hecate.agent_adapter.id`
- `hecate.agent_adapter.command`
- `hecate.agent_adapter.driver.kind`
- `hecate.agent_adapter.native_session.id`
- `hecate.chat.session.id`
- `hecate.workspace.path`
- `hecate.run.id`
- `hecate.agent_adapter.output.bytes`
- `hecate.agent_adapter.diff.captured`

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

Current limitation: external adapters run as trusted subprocesses in the
selected workspace. They are not the same as Hecate task-runtime sandboxed tool
calls. This is intentional for alpha: Codex, Claude Code, Cursor Agent, and Grok Build are
long-lived interactive processes with their own auth, caches, child processes,
and ACP stdio/session lifecycle. Reusing the task-runtime per-call sandbox is
not a drop-in fit.

## Acceptance Criteria For First Implementation

- [x] `GET /hecate/v1/agent-adapters` reports built-in adapter definitions and availability.
- [x] Hecate can run Codex, Claude Code, Cursor Agent, or Grok Build prompts through a supervised ACP session.
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
- [x] Adapter readiness can distinguish missing binaries, auth/billing failures, and versions outside Hecate's tested range.
- [x] Operator approvals are prompt-first by default and visible through REST, SSE, Connections grants, and Chats review UI.
- [x] Optional turn, wall-clock, and idle guardrails protect long-lived external-agent sessions.

## Future Enhancements

- Fuller patch review UX for captured diffs: side-by-side hunks, batch
  selection, and richer artifact history. The current Chats UI can inspect and
  revert already-applied Git paths and is sufficient for alpha stability.
- Deeper adapter-specific structured mappers for ACP tool output. The current
  generic mapper plus raw diagnostics is sufficient for alpha stability.
- Decide which task-runtime primitives External Agent chat should reuse without
  pretending Hecate owns the external agent runtime.

## Open Questions

- Should External Agent chat reuse task-runtime primitives for artifacts, event
  history, retention, and trace correlation while keeping Codex, Claude Code,
  and Cursor as opaque supervised runtimes?
- How much of the external process environment should be configurable by the
  operator?
- Should Hecate eventually offer optional process containment for external
  adapters? Not a near-term requirement. If it happens, it should be a separate
  design for long-lived ACP subprocesses, not reuse of the task-runtime
  per-call sandbox.
- Which adapter-specific ACP update shapes deserve first-class UI mapping next?

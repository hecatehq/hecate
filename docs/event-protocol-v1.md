# Agent Event Protocol — v1 Candidate (RFC)

> **Status:** draft / RFC. The v1 envelope is implemented for task run event
> list and cross-run event endpoints. Payload schemas remain candidate-stage.
> Not stable until Hecate starts publishing semver-backed API guarantees.
> **Supersedes (when stable):** ad-hoc events documented in [`events.md`](events.md).
> **Owner:** see [`AGENTS.md`](../AGENTS.md).

This document proposes the typed event stream that the agent runtime will emit
and that every frontend (CLI, web UI, ACP server, IDE plugin) can consume once
the candidate is implemented and stabilized. It is intentionally **not** a
locked frontend contract yet.

The goal is a schema that is **versioned**, **append-only**, **monotonic per
run**, and **kind-discriminated**. Events should carry enough information for a
renderer to draw the agent's state without re-querying the runtime for routine
context.

## Scope and stability

This RFC covers **agent-runtime runs** only: `agent_loop`, shell, git, and file
runs that already have a `run_id`. It does not define the event contract for
ad-hoc chat completions that are not represented as runtime runs. If Hecate
later wants chat completions on this protocol, the chat path should first mint a
run-like execution id or use a separate chat-event protocol.

The frontend-safe v1 candidate is deliberately smaller than the full design:

| Status | Event groups |
|---|---|
| Candidate core | `run.*`, `turn.*`, `assistant.text_*`, `assistant.final_answer`, generic `tool.*`, `tool.shell.*`, `approval.*`, `cost.*`, `policy.*`, `error.*`, `gap.*` |
| Depends on artifact RFC | `artifact.*`, `tool.edit.*`, `tool.multi_edit.*`, artifact ids on terminal tool events |
| Experimental / not v1 core | `assistant.thinking_*`, streamed tool-input deltas, sub-agent fan-out, multi-modal output, conversation branching |

Frontend work should only depend on the **candidate core** until the
implementation ships golden fixtures and contract tests. Experimental events may
exist behind flags, but frontends must treat them as optional.

Candidate-core fixture examples live in
[`docs/fixtures/events/v1/core/`](fixtures/events/v1/core/). They are validated
by `internal/eventprotocol` tests and are intended as golden inputs for early
CLI, web, ACP, and IDE prototypes.

The draft envelope schema lives at
[`docs/schemas/events/v1/envelope.schema.json`](schemas/events/v1/envelope.schema.json).
Payload-specific schemas are intentionally deferred until runtime emitters are
implemented.

Implementation note: Hecate maps persisted task run events to this envelope at
the API boundary. The agent loop emits `turn.started`,
`assistant.text_complete`, `assistant.tool_call_proposed`, and
`assistant.final_answer`; the shell executor emits the first typed tool slice
(`tool.invoked`, `tool.started`, `tool.shell.*`, and terminal `tool.*` events).
Some candidate-core payloads remain RFC-only until their runtime emitters land.

Before this document can be called v1 stable:

- The candidate core must be implemented end-to-end for at least one real tool
  family.
- JSON Schema or generated Go/TypeScript types must exist for the envelope and
  candidate-core event payloads.
- Golden event fixtures must be checked in and used by web/CLI/ACP tests.
- The artifact-storage RFC must be at least candidate-stable before `artifact.*`
  events are promoted into v1 core.

## Why a typed protocol

Today's run events are JSON blobs with a `type` string and a free-form payload. That works for a single web UI written in lockstep with the runtime, but it breaks down the moment you have:

- A CLI rendering markdown + tool-call boxes.
- A web UI showing diffs with apply/revert buttons.
- An ACP server feeding Zed's agent panel.
- A JetBrains plugin doing the same for IntelliJ.
- A future "background tasks" surface in macOS notification center.

Each of those needs the same information shaped differently. If the runtime
emits unstructured events, every renderer guesses, and bugs in one don't show up
until they hit the others. A typed event schema should become the gateway's
promise: "here is exactly what happened, in a shape any frontend can rely on."

## Envelope

Every event on the wire is a single JSON object:

```json
{
  "schema_version": "1",
  "event_id": "evt_01JXMZH...",
  "run_id": "run_01JXMZH...",
  "task_id": "task_01JXMZH...",
  "session_id": "chat_01JXMZH...",
  "sequence": 42,
  "occurred_at": "2026-05-03T10:23:45.123Z",
  "type": "tool.shell.output_chunk",
  "data": { "...": "shape determined by `type`" }
}
```

| Field | Type | Notes |
|---|---|---|
| `schema_version` | string | Currently `"1"`. Bumped on breaking changes; additive changes within `1.x` add fields without bumping. |
| `event_id` | ULID | Globally unique. Sortable by creation. Stable across replay. |
| `run_id` | ULID | Always present for this protocol. Anchors the event to a single agent-runtime run. |
| `task_id` | ULID | Present when the run belongs to a task. |
| `session_id` | ULID | Present when the run belongs to a chat session. |
| `sequence` | uint64 | Monotonic per `run_id`. Starts at 0. Resumable streams use this as the cursor. |
| `occurred_at` | RFC3339 nano | Server clock. Not user-trustworthy; use `sequence` for ordering. |
| `type` | string | Dotted name. See taxonomy below. Always lowercase, snake_case segments. |
| `data` | object | Type-specific payload. Always an object, never null. May be `{}` for events with no payload. |

### Wire transport

- **SSE.** `GET /v1/tasks/{task_id}/runs/{run_id}/events?after_sequence=N` returns `text/event-stream`. Each `data:` chunk is one envelope. `id:` mirrors `sequence` so the browser's `EventSource` reconnects with `Last-Event-ID` for free.
- **Bulk fetch.** `GET /v1/tasks/{task_id}/runs/{run_id}/events?after_sequence=N&limit=500` returns `{"object":"list","data":[<envelope>,…]}` for non-streaming consumers (CLI replay, audit export).
- **Cross-run feed.** `GET /v1/events/stream?…` is unchanged; envelopes are the same shape, just from many runs.

The shorter `/v1/runs/{run_id}/events` alias is a possible future convenience,
but it is not part of the candidate contract until implemented.

### Ordering and replay invariants

1. **Per-run monotonic.** Within a single `run_id`, `sequence` is gap-free and strictly increasing. A consumer that has seen `sequence=N` is guaranteed never to receive an earlier event for that run.
2. **No across-run ordering.** Across runs, only `event_id` (ULID time prefix) approximates ordering, and only at second resolution. Don't rely on it.
3. **Replay-stable.** Re-fetching `/v1/tasks/{task_id}/runs/{run_id}/events?after_sequence=0` returns the same envelopes byte-for-byte. Hashes stay stable.
4. **At-least-once on streams.** SSE consumers should dedupe by `event_id`. Bulk fetch is exactly-once.
5. **Backfill window.** Events older than the retention window may be pruned; a `gap.events_pruned` event marks where. New consumers must handle a starting sequence > 0.

## Versioning rules

- `schema_version` is a string, currently `"1"`.
- **After v1 is stable, additive evolution within v1** means new event types, new optional fields on `data`, and new optional envelope fields. No `schema_version` bump.
- **Breaking changes** require `"2"` and a flag-day migration. v1 and v2 streams may coexist on different endpoints during transition.
- **Removed fields** are never repurposed; they're tombstoned in this doc.
- **Renames are breaking.** Add the new name, deprecate the old one for at least one minor release, then remove on the next major.
- **Stable `type` strings are forever.** Once an event type graduates into the stable contract, add new types instead of renaming it.

A frontend that doesn't recognize a `type` should display the event as opaque (raw JSON in a debug pane), not error. New tool kinds will arrive without a frontend update.

## Event taxonomy

Top-level groups, by category:

| Group | Purpose |
|---|---|
| [`run.*`](#run-lifecycle) | Run-level lifecycle: queued, started, finished. |
| [`turn.*`](#turn-lifecycle) | Per-turn boundaries inside an `agent_loop` run. |
| [`assistant.*`](#assistant-output) | Streaming model output: text, thinking, tool-call proposals, final answer. |
| [`user.*`](#user-input) | Operator/user input into the conversation. |
| [`tool.*`](#tool-calls) | Tool invocation lifecycle. Subgroups per tool kind. |
| [`approval.*`](#approvals) | Approval requests + resolutions. |
| [`artifact.*`](#artifacts) | Patch/file/output artifact lifecycle. Experimental until artifact storage is candidate-stable. |
| [`cost.*`](#cost) | Token + USD totals as they accrue. |
| [`policy.*`](#policy) | Policy gate decisions. |
| [`error.*`](#errors) | Recoverable + unrecoverable errors not tied to a tool. |
| [`gap.*`](#gaps) | Stream-integrity markers (pruned events, missing turns). |

The remainder of the doc gives `data` shapes for each.

## Run lifecycle

`run.queued` — run has been accepted, awaiting a worker lease.

```json
{
  "type": "run.queued",
  "data": {
    "kind": "agent_loop",
    "model": "claude-sonnet-4-5",
    "provider": "anthropic",
    "workspace_path": "/tmp/hecate-workspaces/task_xyz/run_abc",
    "workspace_mode": "clone",
    "prior_run_id": null
  }
}
```

`run.started` — a worker has claimed the run.

```json
{
  "type": "run.started",
  "data": {
    "worker_id": "worker-3",
    "lease_until": "2026-05-03T10:24:15.000Z"
  }
}
```

`run.finished` — terminal success.

```json
{
  "type": "run.finished",
  "data": {
    "final_status": "completed",
    "turns": 7,
    "cost_micros_usd": 12_400,
    "duration_ms": 48_310
  }
}
```

`run.failed` — terminal failure. Includes a stable error code; the human-readable reason is in `error.message`.

```json
{
  "type": "run.failed",
  "data": {
    "code": "model_unreachable",
    "message": "anthropic.com returned 529 after 3 retries",
    "retriable": true,
    "turns": 2
  }
}
```

`run.cancelled` — operator-requested cancel.

```json
{
  "type": "run.cancelled",
  "data": { "by": "operator", "reason": "" }
}
```

`run.resumed_from_event` — emitted as the **first event of the new run** when an agent resumes from a checkpoint.

```json
{
  "type": "run.resumed_from_event",
  "data": {
    "from_run_id": "run_01JXMZ...",
    "from_sequence": 187,
    "prior_cost_micros_usd": 8_900
  }
}
```

`run.checkpoint_saved` — the runtime persisted enough state to resume from this point. Used by the UI's "branch from here" feature.

```json
{
  "type": "run.checkpoint_saved",
  "data": { "checkpoint_id": "ckpt_01JXMZ..." }
}
```

## Turn lifecycle

`turn.started` — a new turn begins (model call about to fire).

```json
{
  "type": "turn.started",
  "data": {
    "turn_index": 3,
    "model": "claude-sonnet-4-5",
    "provider": "anthropic",
    "input_tokens_estimate": 4_217
  }
}
```

`turn.completed` — turn finished. Costs are authoritative here; the per-`assistant.*` events don't carry final totals.

```json
{
  "type": "turn.completed",
  "data": {
    "turn_index": 3,
    "input_tokens": 4_201,
    "output_tokens": 312,
    "cached_input_tokens": 0,
    "cost_micros_usd": 4_120,
    "duration_ms": 6_540,
    "tool_calls": 2,
    "stop_reason": "tool_use"
  }
}
```

`turn.failed` — turn-scoped failure (model timeout, parse error). The run may continue (retry) or fail.

```json
{
  "type": "turn.failed",
  "data": {
    "turn_index": 3,
    "code": "model_timeout",
    "message": "no response in 60s",
    "will_retry": true
  }
}
```

## Assistant output

Streaming model output. Frontends render these incrementally.

`assistant.text_delta` — a chunk of streamed text.

```json
{
  "type": "assistant.text_delta",
  "data": {
    "turn_index": 3,
    "block_index": 0,
    "delta": "I'll start by reading the budget package."
  }
}
```

`assistant.text_complete` — final text of a content block. The full text is `concat(deltas)`; this event lets a frontend render the finalized string and discard the partials.

```json
{
  "type": "assistant.text_complete",
  "data": {
    "turn_index": 3,
    "block_index": 0,
    "text": "I'll start by reading the budget package."
  }
}
```

`assistant.thinking_delta` / `assistant.thinking_complete` are **experimental**.
They use the same shape as `text_*`, but for reasoning-model thinking blocks.
They are not part of the v1 candidate core because visibility, retention, and
provider-policy rules are still unsettled. Implementations that experiment with
these events must gate them behind config and keep them hidden by default.

`assistant.tool_call_proposed` — model emitted a tool-call request. The tool hasn't run yet; this is the agent's *intent*. The corresponding `tool.invoked` event follows once the runtime decides to dispatch.

```json
{
  "type": "assistant.tool_call_proposed",
  "data": {
    "turn_index": 3,
    "tool_call_id": "call_01JXMZ...",
    "tool_name": "edit_file",
    "input": {
      "path": "internal/budget/governor.go",
      "old_text": "func budgetKeyForRequest(scope types.RequestScope) string {",
      "new_text": "func budgetKeyForRequest(scope types.RequestScope, key string) string {"
    }
  }
}
```

`assistant.final_answer` — the model signalled it's done (no more tool calls). May coexist with a final `assistant.text_complete`. The renderer can use this to mark the conversation as quiescent.

```json
{
  "type": "assistant.final_answer",
  "data": {
    "turn_index": 7,
    "summary": "Refactored budget keying to include the API key. 3 files changed, tests pass."
  }
}
```

## User input

`user.message` — operator/user posted a message.

```json
{
  "type": "user.message",
  "data": {
    "turn_index": 0,
    "text": "Refactor the budget package to use generics."
  }
}
```

`user.attachment` — file/image attached to a turn.

```json
{
  "type": "user.attachment",
  "data": {
    "turn_index": 0,
    "kind": "image",
    "mime": "image/png",
    "artifact_id": "art_01JXMZ..."
  }
}
```

## Tool calls

Tools are the protocol's most active surface. Every tool emits the same lifecycle events (with kind-specific payloads):

```
tool.invoked       — runtime accepted the call, about to execute
tool.started       — execution began (or, for some tools, equivalent to invoked)
tool.{kind}.*      — kind-specific progress events
tool.completed     — terminal success
tool.failed        — terminal failure
tool.cancelled     — operator cancel
tool.timed_out     — wall-clock or output cap exceeded
```

`tool.invoked` and the terminal `tool.completed` / `tool.failed` are emitted for every tool kind. The kind-specific events between them differ.

### Generic tool envelope

Every tool event carries a `tool_call_id` (matches the model's request id) and a `tool_name` so a renderer can correlate them.

```json
{
  "type": "tool.invoked",
  "data": {
    "tool_call_id": "call_01JXMZ...",
    "tool_name": "shell_exec",
    "kind": "shell",
    "turn_index": 3
  }
}
```

```json
{
  "type": "tool.completed",
  "data": {
    "tool_call_id": "call_01JXMZ...",
    "tool_name": "shell_exec",
    "kind": "shell",
    "duration_ms": 312,
    "summary": "exited with status 0",
    "result_artifact_id": "art_01JXMZ..."
  }
}
```

`summary` is a one-line, human-readable digest. `result_artifact_id` is optional
until artifact storage is candidate-stable; frontends must handle terminal tool
events that have no artifact pointer.

### `tool.shell.*`

Subprocess execution. Streams stdout/stderr; exits with a code.

```json
{ "type": "tool.shell.command", "data": {
    "tool_call_id": "call_01JXMZ...",
    "argv": ["go", "test", "./internal/budget/..."],
    "cwd": "/tmp/hecate-workspaces/.../run_abc",
    "env_keys": ["PATH", "HOME"],
    "sandbox_layer": "bwrap"
}}

{ "type": "tool.shell.output_chunk", "data": {
    "tool_call_id": "call_01JXMZ...",
    "stream": "stdout",
    "data": "ok  \tgithub.com/hecate/agent-runtime/internal/budget\t0.231s\n",
    "byte_offset": 0
}}

{ "type": "tool.shell.exited", "data": {
    "tool_call_id": "call_01JXMZ...",
    "exit_code": 0,
    "signal": null,
    "stdout_bytes": 3_120,
    "stderr_bytes": 0,
    "truncated": false
}}
```

`output_chunk` events are coalesced server-side (no per-byte spam). Frontends
append to a buffer. Once artifact storage is candidate-stable, the full output
is also persisted as a `command_output` artifact and referenced by the terminal
`tool.completed`.

### `tool.edit.*` and `tool.multi_edit.*`

File-edit tools are **experimental** until artifact storage is candidate-stable.
The intended model is that edits become first-class artifacts; the events here
describe the lifecycle and the actual diff content lives in the artifact.

```json
{ "type": "tool.edit.proposed", "data": {
    "tool_call_id": "call_01JXMZ...",
    "path": "internal/budget/governor.go",
    "patch_artifact_id": "art_01JXMZ...",
    "summary": "1 hunk, +1/-1 line",
    "auto_apply": true
}}

{ "type": "tool.edit.applied", "data": {
    "tool_call_id": "call_01JXMZ...",
    "patch_artifact_id": "art_01JXMZ...",
    "applied_at": "2026-05-03T10:23:46.001Z"
}}

{ "type": "tool.edit.reverted", "data": {
    "patch_artifact_id": "art_01JXMZ...",
    "reverted_by": "operator",
    "reason": "tests failed"
}}
```

`tool.multi_edit.*` carries `patch_artifact_ids: [...]` for the atomic batch.

### `tool.glob.*` and `tool.grep.*`

Pure read tools — completion-only, no streaming.

```json
{ "type": "tool.glob.completed", "data": {
    "tool_call_id": "call_01JXMZ...",
    "pattern": "internal/**/*_test.go",
    "matches": ["internal/budget/governor_test.go", "..."],
    "match_count": 47,
    "truncated": false
}}

{ "type": "tool.grep.completed", "data": {
    "tool_call_id": "call_01JXMZ...",
    "pattern": "budgetKeyForRequest",
    "files_searched": 312,
    "matches": [
      { "path": "internal/budget/governor.go", "line": 84, "text": "func budgetKeyForRequest(scope ..."}
    ],
    "match_count": 3,
    "truncated": false
}}
```

### `tool.file_read.*` / `tool.file_write.*`

```json
{ "type": "tool.file_read.completed", "data": {
    "tool_call_id": "call_01JXMZ...",
    "path": "internal/budget/governor.go",
    "size_bytes": 8_124,
    "lines": 312,
    "snippet_artifact_id": "art_01JXMZ..."
}}

{ "type": "tool.file_write.proposed", "data": {
    "tool_call_id": "call_01JXMZ...",
    "path": "internal/budget/generics.go",
    "patch_artifact_id": "art_01JXMZ...",
    "summary": "new file, 42 lines",
    "auto_apply": false
}}
```

A `file_write` of an existing path emits `tool.edit.proposed` (it's an edit). Only true new-file creation uses `file_write.proposed`.

### `tool.todo_write.updated`

The conversation's sticky todo list. Overwrites the prior list in full.

```json
{ "type": "tool.todo_write.updated", "data": {
    "tool_call_id": "call_01JXMZ...",
    "todos": [
      { "content": "Read budget package",       "status": "completed" },
      { "content": "Add generic key type",      "status": "in_progress" },
      { "content": "Update governor_test.go",   "status": "pending" }
    ]
}}
```

Frontends render this as a sticky panel that updates in place.

### `tool.http.*` and `tool.web_fetch.*` / `tool.web_search.*`

```json
{ "type": "tool.http.requested", "data": {
    "tool_call_id": "call_01JXMZ...",
    "method": "GET",
    "url": "https://api.example.com/v1/foo",
    "header_keys": ["Authorization", "Accept"]
}}

{ "type": "tool.http.response", "data": {
    "tool_call_id": "call_01JXMZ...",
    "status": 200,
    "duration_ms": 412,
    "body_artifact_id": "art_01JXMZ...",
    "body_truncated": false
}}
```

`tool.web_fetch.*` and `tool.web_search.*` follow the same shape with extra fields (`extracted_text_artifact_id`, search-result hits, etc.).

### MCP-dispatched tools

Calls to external MCP servers use the generic tool taxonomy. The server name comes from the tool name's prefix (`mcp__filesystem__read_text_file` → server `filesystem`) and is carried in MCP-specific payload fields.

```json
{ "type": "tool.completed", "data": {
    "tool_call_id": "call_01JXMZ...",
    "tool_name": "mcp__filesystem__read_text_file",
    "kind": "mcp",
    "mcp_server": "filesystem",
    "mcp_tool": "read_text_file",
    "result": "dispatched",
    "duration_ms": 88
}}

{ "type": "policy.tool_blocked", "data": {
    "tool_call_id": "call_01JXMZ...",
    "tool_name": "mcp__github__delete_repo",
    "kind": "mcp",
    "mcp_server": "github",
    "mcp_tool": "delete_repo",
    "result": "blocked",
    "reason": "approval_policy=block"
}}
```

## Approvals

Approvals are blocking. The runtime emits a request, pauses the run, and waits for resolution before continuing.

```json
{ "type": "approval.requested", "data": {
    "approval_id": "appr_01JXMZ...",
    "tool_call_id": "call_01JXMZ...",
    "kind": "shell_exec",
    "summary": "rm -rf node_modules",
    "policy_reason": "shell_exec is gated by all_tools policy"
}}

{ "type": "approval.resolved", "data": {
    "approval_id": "appr_01JXMZ...",
    "decision": "approved",
    "by": "operator",
    "comment": "",
    "scope": "once"
}}

{ "type": "approval.timed_out", "data": {
    "approval_id": "appr_01JXMZ...",
    "after_seconds": 300
}}
```

`scope` is optional and currently advisory. The v1 candidate core records the
decision that unblocked the run; persistent "always allow" policy is a separate
permission-store feature and must not be inferred from this event alone.

## Artifacts

Artifacts are **first-class persisted objects** the agent produced or
referenced. This section is design intent, not v1 candidate core. The event
stream may carry pointers (`artifact_id`), but frontends must not require them
until [`artifact-storage-v1.md`](artifact-storage-v1.md) is candidate-stable.

This is the single most important architectural call in the protocol. Without it, every diff has to be re-derived from `git status`, every command output is lost on tab refresh, every fetched URL has to be re-fetched.

### Artifact lifecycle events

```json
{ "type": "artifact.created", "data": {
    "artifact_id": "art_01JXMZ...",
    "kind": "patch",
    "size_bytes": 412,
    "summary": "internal/budget/governor.go: 1 hunk, +1/-1",
    "created_by_event_id": "evt_01JXMZ..."
}}
```

```json
{ "type": "artifact.referenced", "data": {
    "artifact_id": "art_01JXMZ...",
    "by_event_id": "evt_01JXMZ...",
    "relation": "consumed"
}}
```

```json
{ "type": "artifact.updated", "data": {
    "artifact_id": "art_01JXMZ...",
    "field": "status",
    "old_value": "proposed",
    "new_value": "applied"
}}
```

### Artifact kinds

| Kind | Body shape | Used by |
|---|---|---|
| `patch` | unified diff (text/x-diff) | `tool.edit.*`, `tool.multi_edit.*`, `tool.file_write.*` |
| `file_snapshot` | raw file bytes + path + revision | `tool.file_read.*` (large files); checkpoints |
| `command_output` | stdout + stderr + exit metadata (text/plain) | `tool.shell.*` |
| `fetched_resource` | response body + headers + URL | `tool.http.*`, `tool.web_fetch.*` |
| `search_results` | structured JSON | `tool.web_search.*` |
| `image` | binary + mime | `user.attachment` (image inputs) |
| `tool_result_blob` | opaque bytes + mime | `tool.completed` with `kind=mcp` for non-text MCP results |

### Patch artifact (the important one)

```json
GET /v1/artifacts/art_01JXMZH...
{
  "object": "artifact",
  "data": {
    "id": "art_01JXMZH...",
    "kind": "patch",
    "status": "applied",
    "target_path": "internal/budget/governor.go",
    "base_revision": "sha256:8a4f...",
    "diff": "--- a/internal/budget/governor.go\n+++ b/internal/budget/governor.go\n@@ -84,7 +84,7 @@\n-func budgetKeyForRequest(scope types.RequestScope) string {\n+func budgetKeyForRequest(scope types.RequestScope, key string) string {\n",
    "stats": { "additions": 1, "deletions": 1, "hunks": 1 },
    "created_at": "2026-05-03T10:23:45.500Z",
    "applied_at": "2026-05-03T10:23:46.001Z",
    "reverted_at": null,
    "produced_by_run_id": "run_01JXMZ...",
    "produced_by_tool_call_id": "call_01JXMZ..."
  }
}
```

`status` transitions are append-only events: `proposed → (applied | rejected) → (reverted)?`. A revert produces a new artifact (the inverse patch); the original's `status` stays `applied` with `reverted_at` set.

### Why this matters

Once edits are artifacts:
- **Review UX is trivial.** A frontend lists `kind=patch, run_id=X, status=proposed` and renders apply/reject buttons.
- **"What did the agent change?" is a single query.** No `git diff` arithmetic.
- **Revert is a real operation.** The runtime owns the inverse, not the frontend.
- **Replay is possible.** Re-apply a checkpoint's patches against a fresh worktree.
- **CI integration.** A PR-bot can fetch all `applied` patches from a run and submit them upstream.

## Cost

Cost events come on every turn boundary; budget events come when thresholds are crossed.

```json
{ "type": "cost.tick", "data": {
    "cumulative_input_tokens": 12_400,
    "cumulative_output_tokens": 1_120,
    "cumulative_cost_micros_usd": 14_320,
    "task_budget_micros_usd": 50_000,
    "task_budget_remaining_micros_usd": 35_680
}}

{ "type": "cost.budget_warning", "data": {
    "threshold_pct": 80,
    "cumulative_cost_micros_usd": 40_120,
    "task_budget_micros_usd": 50_000
}}

{ "type": "cost.budget_exceeded", "data": {
    "cumulative_cost_micros_usd": 50_240,
    "task_budget_micros_usd": 50_000,
    "action": "halted"
}}
```

## Policy

Emitted when the gateway's policy/governor layer makes a decision visible to the run.

```json
{ "type": "policy.tool_blocked", "data": {
    "tool_call_id": "call_01JXMZ...",
    "tool_name": "shell_exec",
    "reason": "no_network policy denied egress",
    "policy_id": "default-shell"
}}

{ "type": "policy.model_rewrote", "data": {
    "from_model": "gpt-5",
    "to_model": "gpt-5-mini",
    "reason": "rewrite rule: cost-cap"
}}
```

## Errors

Run-level errors that aren't tied to a single tool call. Tool-call failures emit `tool.failed`; this group is for everything else.

```json
{ "type": "error.tool_unavailable", "data": {
    "tool_name": "web_search",
    "reason": "no_provider_configured"
}}

{ "type": "error.model_capability_missing", "data": {
    "model": "llama3.1:8b",
    "missing_capability": "tool_use",
    "fallback_strategy": "prompt_simulated_json"
}}

{ "type": "error.upstream", "data": {
    "provider": "anthropic",
    "status": 529,
    "message": "anthropic returned overloaded_error",
    "retriable": true,
    "attempt": 2,
    "max_attempts": 3
}}
```

## Gaps

Stream-integrity markers. A frontend that sees one of these knows there's history it can't recover.

```json
{ "type": "gap.events_pruned", "data": {
    "first_pruned_sequence": 0,
    "last_pruned_sequence": 142,
    "pruned_at": "2026-05-03T09:00:00Z",
    "reason": "retention_window"
}}

{ "type": "gap.run_disconnected", "data": {
    "since_sequence": 312,
    "since_at": "2026-05-03T10:23:00Z",
    "reason": "worker_lease_expired"
}}
```

## Design decisions for the v1 candidate

These decisions keep the first frontend-facing contract small enough to
implement and test.

1. **Redaction happens in the runtime before emit.** Frontends should never be
   responsible for hiding secrets. Events that include possibly-sensitive input
   must carry redacted values plus a `redacted_keys` or `redacted_paths` list
   when useful for debugging.
2. **Approvals stay read-via-stream, write-via-REST.** The event stream reports
   approval requests and resolutions. Approval decisions continue through the
   existing REST endpoint. No websocket/write-side stream in v1 candidate.
3. **Reasoning/thinking content is experimental and hidden by default.** It can
   exist behind config for local experiments, but core frontends cannot depend
   on it.
4. **Artifact references are optional until artifact storage stabilizes.**
   Generic tool lifecycle events must still render without artifact ids.

## Experimental extensions

Streamed tool input, reasoning/thinking blocks, sub-agent fan-out, multi-modal
output, conversation branching, and approval write-side transport are outside
the v1 candidate core. They live in
[`event-protocol-experimental.md`](event-protocol-experimental.md) until they
earn a separate RFC or graduate into a later protocol version.

## Implementation status

Hecate already wraps persisted run events in the v1 envelope on the per-run
event endpoints and the cross-run feed. The implemented typed event slice is:

- `run.started`, `run.finished`, `run.failed`, `run.cancelled`,
  `run.resumed_from_event`
- `turn.started`, `turn.completed`
- `assistant.text_complete`, `assistant.tool_call_proposed`,
  `assistant.final_answer`
- `tool.invoked`, `tool.started`, `tool.shell.command`,
  `tool.shell.output_chunk`, `tool.shell.exited`, `tool.file.patch`,
  `tool.file.reverted`, `tool.completed`, `tool.failed`,
  `tool.timed_out`, `policy.tool_blocked`
- `approval.requested`, `approval.resolved`
- `gap.run_disconnected`

The remaining normalization work is intentionally narrow:

- Add a typed queue-throttle event if task-level concurrency throttling becomes
  an operator-visible runtime event.
- Add payload-specific schemas or generated Go/TypeScript types for the
  implemented candidate-core events.
- Keep golden fixtures in sync with the runtime and use them from frontend or
  ACP tests before treating the candidate as stable.

## What this unlocks

When the remaining candidate-core work is implemented:

- The CLI can render any tool call without knowing what tool kind it is — generic `tool.invoked → tool.completed` framing handles the unknowns; rich rendering kicks in for known kinds.
- Once artifact storage is candidate-stable, the web UI's diff review can use artifact endpoints directly; no more re-deriving diffs from filesystem state.
- An ACP server is a thin translator from this stream to ACP's wire format.
- Hooks (`PreToolUse`, `PostToolUse`) get a stable contract: they receive the `tool.invoked` / `tool.completed` event verbatim.
- Later sub-agent work becomes tractable: the parent run can point at child run ids while each child keeps the same stream shape.
- Audit becomes a database query: "what shell commands did this agent run?" is `SELECT WHERE type='tool.shell.exited' AND run_id=X`.

## Next steps

1. **Land this doc** as a draft RFC. Review the candidate-core vs experimental split before implementation starts.
2. **Prototype the envelope + a single tool family** (`tool.shell.*`) end-to-end in a feature branch. Wire one CLI command to consume it, end-to-end.
3. **Generate schemas/types + golden fixtures** for the candidate core.
4. **Land artifact storage separately** before promoting `artifact.*` or edit
   events into the stable core.
5. **Migrate existing emitters** one tool family at a time behind an explicit
   experimental flag.
6. **Cut over the web UI** only after the schema is exercised by at least one
   non-web consumer.
7. **Mark v1.0 stable** when candidate-core events are implemented, typed,
   fixture-tested, and consumed by all in-tree frontends that need them.

Estimated wall-clock for the runtime side: 4-6 weeks of focused work, dependent on the artifact-storage subsystem (which is the highest-risk unknown).

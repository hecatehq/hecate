# Runtime API Notes

Hecate exposes a coding-runtime API surface under `/hecate/v1/tasks` for client-orchestrated agents. The runtime is durable: a run survives process restarts, can be resumed from a terminal state, and is leased to one worker at a time so two replicas can share a queue without stepping on each other.

For the high-level execution flow (lease semantics, sandbox boundary, event sequence), see [`architecture.md`](../contributor/architecture.md#task-runtime-flow). For the LLM-driven `agent_loop` execution kind specifically (tools, approval gating, cost tracking, retry-from-turn semantics), see [`agent-runtime.md`](agent-runtime.md).

> Contributing here? Start at [`AGENTS.md`](../../AGENTS.md) for the codebase map and runtime invariants; conventions, workflow, and verification ladders live under [`docs-ai/`](../../docs-ai/README.md).

## API namespaces

Hecate serves three intentionally separate HTTP surfaces:

| Namespace      | Purpose                                                                                                                                                                                                                                                 |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/v1/*`        | Provider-compatible protocol ingress. These paths stay OpenAI- or Anthropic-shaped so existing SDKs can point at Hecate without learning Hecate-specific URLs. Today that means `GET /v1/models`, `POST /v1/chat/completions`, and `POST /v1/messages`. |
| `/hecate/v1/*` | Hecate-native product API: tasks, Hecate Chat sessions, External Agent integrations, settings, usage, traces, events, and system operations. Operator UI, MCP tools, and Hecate-aware clients should use this namespace.                                |
| `/healthz`     | Unversioned process liveness for local scripts, desktop sidecars, and load balancers. It is intentionally tiny and not wrapped in the normal `{object,data}` API envelope.                                                                              |

OTLP collector/export endpoints keep their standard protocol paths
(`/v1/traces`, `/v1/metrics`, `/v1/logs`) when Hecate is configured to export to
an OpenTelemetry collector. Those are not Hecate product resources. Hecate's
local trace lookup for the operator UI is `GET /hecate/v1/traces`.

Set `HECATE_RUNTIME_TOKEN` to require Hecate-aware clients to send
`X-Hecate-Runtime-Token` on `/hecate/v1/*`. This protects the Hecate-native
control plane, including Hecate-native chat and task routes that can spend
configured provider credentials. It does not apply to provider-compatible
`/v1/*` paths or `/healthz`. The operator UI sends the header when
`hecate.runtimeToken` is present in `sessionStorage` or `localStorage`; the MCP
server reads the same value from its `HECATE_RUNTIME_TOKEN` environment.

Set `HECATE_INFERENCE_TOKEN` to require a shared token on the
provider-compatible inference routes: `GET /v1/models`,
`POST /v1/chat/completions`, and `POST /v1/messages`. Clients may send it as
either `Authorization: Bearer <token>` or `x-api-key: <token>`, so standard
OpenAI- and Anthropic-shaped SDK configuration continues to work. This token
does not wrap `/hecate/v1/*`, `/healthz`, static UI assets, or OTLP collector
paths such as `/v1/traces`, `/v1/metrics`, and `/v1/logs`. The operator UI
sends it to local provider-compatible paths when `hecate.inferenceToken` is
present in `sessionStorage` or `localStorage`.

When `HECATE_REMOTE_RUNTIME_MODE=1`, `/healthz` remains the unauthenticated
liveness probe and every other path requires trusted trusted proxy headers:
`X-Hecate-Remote-Runtime-Secret`, `X-Hecate-Remote-Actor-ID`,
`X-Hecate-Remote-Org-ID`, `X-Hecate-Remote-Project-ID`, and
`X-Hecate-Remote-Runtime-ID`. Valid remote identity is attached to request
context, exposed from `GET /hecate/v1/whoami` as `data.remote_identity`, added to
the top-level HTTP span attributes, and accepted in place of the local
runtime/inference shared tokens. Remote mode rejects local-only endpoints for
workspace picker/open, reset-data, shutdown, MCP probe, plugin-registry
management, agent-adapter authenticate, and local provider and MCP registry discovery. Hecate-native `/hecate/v1/*` routes are explicitly
classified for remote mode, and route coverage tests fail when a new registered
route is not marked remote-safe or local-only.

Remote mode disables local model providers by default. In that default posture,
local presets are omitted, `kind=local` provider creates/updates are rejected,
env-preconfigured local providers are skipped, and existing local provider rows
are not loaded into the runtime provider registry. Set
`HECATE_REMOTE_ALLOW_LOCAL_PROVIDERS=1` only for a private hosted runtime whose
local model server is intentionally inside that runtime's isolation boundary.
This provider policy is kind-based: Hecate blocks providers marked
`kind=local`, but does not URL-filter custom `kind=cloud` `base_url`
destinations. Operators should enforce egress and private-endpoint policy
outside the runtime when they need destination-level controls.

Legacy Hecate-native `/v1/*` and `/admin/*` paths are intentionally not kept as
compatibility shims in this alpha branch. Unknown API-shaped paths return 404
rather than falling through to the embedded UI shell.

## Error envelope

Hecate-native JSON errors use one stable envelope:

```json
{
  "error": {
    "type": "route_impossible",
    "message": "route request: no provider available",
    "user_message": "No configured provider can serve this request.",
    "operator_action": "Open Connections to inspect readiness checks, discover models, or enable a routable provider.",
    "request_id": "req_...",
    "trace_id": "..."
  }
}
```

- `type` is the stable machine code. Operator UI and automation should branch
  on this field, not raw text.
- `message` is the detailed gateway/runtime message. It may include provider or
  router wording.
- `user_message` is the short operator-facing summary.
- `operator_action` is the recommended next step.
- `request_id` and `trace_id` are included when the runtime has already created
  trace state. They mirror `X-Request-Id` / `X-Trace-Id` and let clients open
  `GET /hecate/v1/traces?request_id=...` directly from an error surface.
- Runtime-specific fields may be attached when they help repair the failure.
  Examples: `task_id`, `latest_run_id`, and `run_status` for a busy Hecate Chat
  task; `provider`, `model`, and `capabilities` for tool-capability failures;
  `limit_ms` / `turns_used` for session guardrails.

Common Hecate-native error types:

| Type                                   | Status | Meaning                                                                          |
| -------------------------------------- | -----: | -------------------------------------------------------------------------------- |
| `invalid_request`                      |    400 | Request JSON, query parameters, or required fields are invalid.                  |
| `not_found`                            |    404 | The requested Hecate resource does not exist.                                    |
| `conflict`                             |    409 | The resource changed state or the requested transition is not valid now.         |
| `gateway_error`                        |    500 | Hecate failed before it could classify the failure more specifically.            |
| `rate_limit_exceeded`                  |    429 | The local gateway rate limiter rejected the request.                             |
| `model_not_configured`                 |    422 | The selected model is stale or not currently reported by the selected provider.  |
| `chat.agent_session_busy`              |    409 | A Hecate Chat task-backed loop is queued, running, or awaiting approval.         |
| `chat.model_capability_required`       |    422 | A task-backed Hecate Chat turn was requested, but the model is not tool-capable. |
| `chat.workspace_required`              |    400 | Task-backed Hecate Chat or External Agent chat needs a workspace path.           |
| `chat.session_limit_exceeded`          |    422 | The chat turn limit was reached.                                                 |
| `chat.session_duration_limit_exceeded` |    422 | The chat wall-clock limit was reached.                                           |
| `chat.session_idle_timeout`            |    422 | The chat was idle beyond the configured timeout.                                 |

OpenAI-compatible and Anthropic-compatible ingress paths keep their protocol
shape, but gateway-classified failures also include the same
`user_message` / `operator_action` / correlation fields inside their `error`
object when available.

## Contents

- [API namespaces](#api-namespaces)
- [Error envelope](#error-envelope)
- [Core resources](#core-resources)
  - [Task fields](#task-fields)
  - [Run fields](#run-fields)
- [Lifecycle endpoints](#lifecycle-endpoints)
  - [Resume semantics](#resume-semantics)
  - [Retry-from-turn-N semantics](#retry-from-turn-n-semantics)
- [Execution detail endpoints](#execution-detail-endpoints)
- [Approval endpoints](#approval-endpoints)
  - [Approval kinds](#approval-kinds)
  - [Approval policy configuration](#approval-policy-configuration)
- [Event and stream endpoints](#event-and-stream-endpoints)
- [Queue execution model](#queue-execution-model)
- [Runtime backend and queue configuration](#runtime-backend-and-queue-configuration)
- [Usage endpoints](#usage-endpoints)
- [Health and discovery endpoints](#health-and-discovery-endpoints)
- [Plugin registry endpoints](#plugin-registry-endpoints)
- [Agent profile endpoints](#agent-profile-endpoints)
- [Project endpoints](#project-endpoints)
- [Project Assistant endpoints](#project-assistant-endpoints)
- [Chat session endpoints](#chat-session-endpoints)
- [Rate-limit headers on chat / messages](#rate-limit-headers-on-chat--messages)

## Core resources

- `task`
- `task_run`
- `task_step`
- `task_artifact`
- `task_approval`
- `task_run_event`

### Task fields

The `task` resource accepts these fields on `POST /hecate/v1/tasks`:

- `execution_kind` — one of `shell`, `git`, `file`, `agent_loop`
- `prompt` — the user-facing prompt; required for `agent_loop`, optional description for the others
- `project_id` — optional owning project id. Empty / omitted creates an unprojected task; `GET /hecate/v1/tasks?project_id=` lists only unprojected tasks
- `system_prompt` — per-task agent prompt (narrowest of the three-layer composition); `agent_loop` only
- `shell_command` / `git_command` / `file_path` / `file_content` / `file_operation` — execution-kind-specific
- `working_directory` — absolute path; required when `workspace_mode=in_place`
- `workspace_mode` — `""` / `"persistent"` / `"ephemeral"` (clone behavior, default) or `"in_place"` (run directly in `working_directory`); see [`agent-runtime.md`](agent-runtime.md#workspace-modes)
- `repo` / `base_branch` — alternate source for the workspace clone
- `sandbox_allowed_root` / `sandbox_read_only` / `sandbox_network` — sandbox policy for shell / git / file kinds; see [`sandbox.md`](sandbox.md) for the full policy and isolation model
- `requested_provider` / `requested_model` — pin the LLM (`agent_loop`); empty falls back to gateway default
- `budget_micros_usd` — per-task cost ceiling in micro-USD; `0` disables
- `mcp_servers` — `agent_loop`-only array of external MCP server configs whose tools join the LLM's tool catalog under `mcp__<name>__<tool>` aliases. Each entry picks one transport (stdio: `command` + optional `args` / `env`; HTTP: `url` + optional `headers`), and may set `approval_policy` (`auto` / `require_approval` / `block`). Capped per-task by `HECATE_TASK_MAX_MCP_SERVERS_PER_TASK`. Full schema, secret handling, and lifecycle in [`mcp.md#hecate-as-mcp-client`](mcp.md#hecate-as-mcp-client).
- `priority` / `timeout_ms`

Task responses may also include `workspace_system_prompt_policy`. Empty /
omitted means the normal workspace `CLAUDE.md` / `AGENTS.md` prompt layer is
eligible. `exclude` means the runner skips that compatibility layer for the
task; native project assignments set this so profile context-source policy
controls any workspace-instruction body inclusion.

Task responses also include `work_item_id` and `assignment_id` when a
project-work assignment created the task. These fields are inspection links only
and do not replace the task's generic `origin_kind` / `origin_id` fields.

`execution_profile` applies task-create defaults:

| Profile        | Defaults                                                                                                                                                              |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `repo_local`   | `execution_kind=agent_loop`, `workspace_mode=persistent`, `working_directory=.`, `timeout_ms=120000`                                                                  |
| `coding_agent` | Same as `repo_local`, plus `timeout_ms=300000` and a coding-oriented system prompt that nudges the model toward read-before-edit and `file_edit` for targeted changes |

### Run fields

`task_run` carries the cost figures the operator UI surfaces:

- `project_id` / `work_item_id` / `assignment_id` — project inspection links
  snapped onto new runs from the parent task. Older alpha rows may still derive
  these links from the parent task or run context-packet refs when the run
  payload predates direct run linkage.
- `total_cost_micros_usd` — this run's LLM spend (after routing).
- `prior_cost_micros_usd` — cumulative spend of every prior run in this run's resume chain. Cumulative-across-task = `prior + total`.
- `model` / `provider` / `provider_kind` — what was actually used (after routing). May differ from the task's `requested_*` when the operator picked auto. Agent-loop runs preserve these fields for both streaming and non-streaming model turns.

## Lifecycle endpoints

- `POST /hecate/v1/tasks`
- `GET /hecate/v1/tasks` — optional `project_id` query scopes the list. Pass an empty value (`?project_id=`) for unprojected tasks only.
- `GET /hecate/v1/tasks/{id}`
- `DELETE /hecate/v1/tasks/{id}`
- `POST /hecate/v1/tasks/{id}/start` — returns `422 model_not_configured` when an `agent_loop` task has no `requested_model` set. No run is created.
- `POST /hecate/v1/tasks/{id}/runs/{run_id}/retry`
- `POST /hecate/v1/tasks/{id}/runs/{run_id}/resume`
- `POST /hecate/v1/tasks/{id}/runs/{run_id}/continue`
- `POST /hecate/v1/tasks/{id}/runs/{run_id}/retry-from-turn`
- `POST /hecate/v1/tasks/{id}/runs/{run_id}/cancel`

### Resume semantics

- resume is allowed when the source run is terminal (`completed`, `failed`, or `cancelled`)
- resume creates a new run attempt (new `run_id`) rather than mutating the original run
- the new run reuses the prior run workspace when available, so file state carries forward
- optional payload: `{"reason":"..."}` to annotate the resume request
- resumed executions include checkpoint context (source run id, last completed step, last event sequence) in step input so executors/tools can continue from the prior boundary
- for `agent_loop` runs, the saved `agent_conversation` artifact is hydrated as the starting message history — the loop continues from where it left off rather than re-running prior turns
- the new run inherits the chain's cumulative cost via `PriorCostMicrosUSD`, so the per-task ceiling holds across the full chain

### Continue semantics

`POST /hecate/v1/tasks/{id}/runs/{run_id}/continue` body:

```json
{ "prompt": "follow-up instruction" }
```

- only valid for terminal `agent_loop` runs that produced an `agent_conversation` artifact
- creates a new run for the same task, hydrates the source conversation, appends the supplied user prompt, then resumes the loop
- used by ACP/editor sessions where one editor conversation maps to one durable Hecate task and each user prompt becomes the next Hecate run
- returns 409 when the source run is still active, and 400 for non-agent tasks, empty prompts, or missing/malformed conversation artifacts

### Retry-from-turn-N semantics

`POST /hecate/v1/tasks/{id}/runs/{run_id}/retry-from-turn` body:

```json
{ "turn": 2, "reason": "explore alternative" }
```

- only valid on `agent_loop` runs that produced an `agent_conversation` artifact
- `turn` must be in `[1, count(assistant turns)]`; out-of-range turns return 400
- creates a new run whose conversation is truncated to right before the Nth assistant message; the LLM re-issues that turn from the prior context
- step indices on the new run restart at 1 (semantically a fresh run that happens to share prior context, not a continuation)
- see [`agent-runtime.md`](agent-runtime.md#retry-and-resume) for the full flow

## Execution detail endpoints

- `GET /hecate/v1/tasks/{id}/runs`
- `GET /hecate/v1/tasks/{id}/runs/{run_id}`
- `GET /hecate/v1/tasks/{id}/runs/{run_id}/context`
- `GET /hecate/v1/tasks/{id}/runs/{run_id}/steps`
- `GET /hecate/v1/tasks/{id}/runs/{run_id}/steps/{step_id}`
- `GET /hecate/v1/tasks/{id}/runs/{run_id}/artifacts`
- `GET /hecate/v1/tasks/{id}/runs/{run_id}/artifacts/{artifact_id}`
- `GET /hecate/v1/tasks/{id}/artifacts`
- `GET /hecate/v1/tasks/{id}/runs/{run_id}/patches`
- `GET /hecate/v1/tasks/{id}/runs/{run_id}/patches/{artifact_id}`
- `POST /hecate/v1/tasks/{id}/runs/{run_id}/patches/{artifact_id}/apply`
- `POST /hecate/v1/tasks/{id}/runs/{run_id}/patches/{artifact_id}/revert`

`patches` is a review-focused projection over `patch` artifacts. File-writing tools create patches with `status=applied`; `file_edit` and `apply_patch` can also create `status=proposed` patches when called with `propose=true`. The apply endpoint writes the proposed after-content only when the current file still matches the captured before-content, then emits `tool.file.applied`. The revert endpoint restores the before-content captured in Hecate's patch artifact and updates the patch to `status=reverted`. Before reverting, Hecate verifies that the current file still matches the artifact's after-content (or is still present/absent as expected for create/delete patches). If it drifted, the endpoint returns `409 Conflict`, leaves the workspace unchanged, and emits no `tool.file.reverted` event. Repeated reverts of an already-reverted patch are clean no-ops. Reverting a new-file patch removes the file. Reverting emits `tool.file.reverted` on the run-event stream.

Revert is also conflict-checked. Before touching the workspace, Hecate verifies
that the current file still matches the patch artifact's captured after-content.
If the operator or another agent changed or removed the file after the patch was
applied, revert returns `409 conflict`, leaves the workspace unchanged, and keeps
the patch artifact in `applied`.

`GET /hecate/v1/tasks/{id}/runs/{run_id}/context` returns the context packet
snapshot for a task run. Hecate first returns a run-owned packet when the run
persisted one directly, then falls back to a linked Hecate Chat assistant
message packet for task-backed chat runs. Native project-assignment starts now
persist a run-owned packet, and resume/retry chains carry the latest stored
packet forward onto the new run with updated task/run refs. Older or unrelated
runs can still return `404 not_found` when no stored or linked packet exists.

## Approval endpoints

- `GET /hecate/v1/tasks/{id}/approvals`
- `GET /hecate/v1/tasks/{id}/approvals/{approval_id}`
- `POST /hecate/v1/tasks/{id}/approvals/{approval_id}/resolve`

### Approval kinds

The `kind` field on a `task_approval` is one of:

- `shell_command` — pre-execution gate for `execution_kind=shell` tasks
- `git_exec` — pre-execution gate for `execution_kind=git` tasks
- `file_write` — pre-execution gate for `execution_kind=file` tasks
- `network_egress` — pre-execution gate when `sandbox_network=true`
- `agent_loop_tool_call` — mid-loop gate when an `agent_loop` run calls a gated tool (`shell_exec`, `http_request`, etc.). The reason text lists the tools the agent wants to use. See [`agent-runtime.md`](agent-runtime.md#approval-gating) for the full flow.

Resolve payload: `{"decision": "approve" | "reject", "note": "..."}`.

Approval resolution is owned by the task runtime so approval, run, task, step, and run-event state transition together:

- `approve` marks the pending approval `approved`, emits `approval.resolved`, requeues the same run (`queued`) and task (`queued`), and emits `run.queued`. For `agent_loop_tool_call`, the loop dispatches the approved tool calls without re-calling the LLM.
- `reject` marks the pending approval `rejected`, emits `approval.resolved`, and terminalizes the run/task as `cancelled` with `last_error: "approval rejected"`. Any awaiting approval step is cancelled, and the runtime emits `run.cancelled` and `task.updated`.
- Cancelling an `awaiting_approval` run cancels the run/task, cancels pending approvals with `resolved_by: "system"`, cancels the awaiting approval step, and emits the same terminal run/task events. Resolving that approval afterward returns `409 conflict`.
- Resolving a non-pending approval returns `409 conflict`; cancelling an already-terminal run is a no-op and returns the current terminal run.

### Approval policy configuration

`HECATE_TASK_APPROVAL_POLICIES` (default `shell_exec,git_exec,file_write`) is a comma-separated allowlist of which approval gates are active across the task runtime. It controls both pre-execution gates on `shell` / `git` / `file` tasks **and** mid-loop gates inside `agent_loop` runs — same env var, same names. Recognized values:

| Value            | Effect                                                                                                                                                                                                                                                                                          |
| ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `shell_exec`     | Gate `execution_kind=shell` task creates and `agent_loop` `shell_exec` tool calls.                                                                                                                                                                                                              |
| `git_exec`       | Gate `execution_kind=git` task creates and `agent_loop` `git_exec` / `git_status` / `git_diff` tool calls.                                                                                                                                                                                      |
| `file_write`     | Gate `execution_kind=file` task creates and `agent_loop` `file_write` / `file_edit` / `apply_patch` tool calls.                                                                                                                                                                                 |
| `network_egress` | Gate task creates that opt into `sandbox_network=true` and `agent_loop` `http_request` tool calls.                                                                                                                                                                                              |
| `read_file`      | Gate `agent_loop` `read_file` / `grep` / `glob` / `artifact_read` tool calls. Useful when operators want visibility into every file, search, or persisted artifact the agent reads, not just what it writes.                                                                                    |
| `all_tools`      | Gate every agent tool call (`shell_exec`, `git_exec`, `git_status`, `git_diff`, `file_write`, `file_edit`, `apply_patch`, `read_file`, `grep`, `glob`, `artifact_read`, `list_dir`, `http_request`, `draft_project_proposal`) and all pre-execution task gates. Short-circuits to the full set. |

Unknown policy names are rejected at startup with a clear error. Empty value disables every gate (use only in trusted environments). For per-MCP-server gating in `agent_loop` runs, see `approval_policy` on `mcp_servers` entries in [`mcp.md#approval-policy`](mcp.md#approval-policy).

## Event and stream endpoints

### Per-run events

- `GET /hecate/v1/tasks/{id}/runs/{run_id}/events?after_sequence=<n>`
- `POST /hecate/v1/tasks/{id}/runs/{run_id}/events`
- `GET /hecate/v1/tasks/{id}/runs/{run_id}/stream?after_sequence=<n>`

The JSON list returns agent event protocol v1 envelopes:
`schema_version`, `event_id`, `task_id`, `run_id`, `sequence`,
`occurred_at`, `type`, and `data`.

Stream resume also supports `Last-Event-ID`. Each per-run SSE frame carries the
current run state, steps, artifacts, activity, and approvals scoped to that run
so the operator UI can drive approval banners and progress surfaces without a
separate refetch (`TaskRunStreamEventData.Approvals`). The frame's `event_type`
mirrors the persisted event that produced the state refresh.
The frame is a read-time projection over the append-only run-event log and live
run storage. When the stream emits a projected live snapshot without a newer
persisted event, the SSE `id` and `data.sequence` stay on the latest persisted
cursor instead of creating a new `run_event` row.

The frame also includes a normalized `activity` array for clients that want a
coding-agent-style timeline without reconstructing it from raw steps and
artifacts. Activity item types include `thinking`, `tool_call`, `patch`,
`changed_files`, `final_answer`, `project_assistant_proposal`, `approval`, and
`run_result`. Approval
activities carry `approval_id` and `needs_action` when a user decision is
pending. The operator UI uses this same array in both Task Detail and Hecate
Chat transcript projections; clients should treat it as the compact timeline
surface and use raw steps/artifacts/events only for deeper inspection. Task
Detail may expose the raw `TaskActivityItem` fields behind an advanced
disclosure, but Chats should favor the compact projection. For MCP Apps tool
calls, task-backed chat activities may include `mcp_app` with the captured
`ui://` resource URI, MIME type, HTML, resource/tool metadata, tool arguments,
and MCP `CallToolResult`; clients can render it inline or ignore it and fall
back to the normal text activity.

`mcp_app` is optional and appears only when Hecate could associate a tool call
with an MCP Apps HTML resource:

```json
{
  "resource_uri": "ui://weather/dashboard",
  "mime_type": "text/html;profile=mcp-app",
  "html": "<!doctype html>...",
  "html_truncated": false,
  "tool_name": "mcp__weather__get_weather",
  "tool_input": { "city": "Lisbon" },
  "tool_result": {
    "content": [{ "type": "text", "text": "72F" }],
    "structuredContent": { "temperature": "72F" }
  },
  "resource_meta": {
    "ui": {
      "csp": { "resourceDomains": ["https://cdn.example.com"] },
      "prefersBorder": true
    }
  },
  "tool_meta": {
    "ui": {
      "resourceUri": "ui://weather/dashboard",
      "visibility": ["model", "app"]
    }
  }
}
```

The operator UI renders the iframe directly in the assistant message body and
keeps the normal collapsed activity row below it as audit metadata. `ui://`
values are MCP resource identifiers, not browser URLs; clients that render apps
should treat `html` as the captured resource body and apply their own sandboxing
and CSP policy. If app resource capture fails, `error` may be present and `html`
may be absent.

### Public events feed

For external dashboards (Grafana, Slack notifiers, audit log shippers) that want one subscription instead of per-run polling:

- `GET /hecate/v1/events?event_type=<csv>&task_id=<id>&after_sequence=<n>&limit=<n>` — paginated JSON list with cursor-based pagination
- `GET /hecate/v1/events/stream?event_type=<csv>` — long-lived SSE feed; reconnect via `Last-Event-ID`

Both endpoints emit the same v1 event envelopes as the per-run event list.
Filters AND together; within a slice (`event_type` is comma-separated) the match
is OR. `after_sequence` is the event sequence cursor, strictly greater.

### Event types

The full catalog of event types — including payload shapes, when each fires, and per-event extras — lives in [`events.md`](events.md). Highlights:

- `run.*` lifecycle (`run.created` / `run.queued` / `run.started` / `run.finished` / `run.failed` / `run.cancelled`)
- typed `tool.*` events for in-run tool lifecycle detail
- `approval.requested` / `approval.resolved` for human-gating flows
- `turn.completed` for per-LLM-turn cost/tokens summaries in `agent_loop` runs
- `run.resumed_from_event` for resume / retry-from-turn chains

## Queue execution model

When a run is queued, workers consume it through a claim/lease protocol:

1. enqueue `task_id` + `run_id`
2. worker claims with a time-bound lease
3. worker heartbeats to extend lease while work is running
4. worker `ack`s on success/terminal handling or `nack`s to requeue
5. expired leases can be reclaimed by another worker

```mermaid
sequenceDiagram
    participant Caller
    participant API
    participant Queue
    participant Worker
    participant Store
    Caller->>API: POST /hecate/v1/tasks/:id/start
    API->>Queue: enqueue(task_id, run_id)
    Worker->>Queue: claim(worker_id, lease)
    Queue-->>Worker: claim_id, run_id
    Worker->>Store: set run status=running
    loop while running
      Worker->>Queue: extend_lease(claim_id)
    end
    alt completed
      Worker->>Queue: ack(claim_id)
    else retryable / throttled
      Worker->>Queue: nack(claim_id, reason)
    end
```

## Runtime backend and queue configuration

- `HECATE_BACKEND=memory|sqlite|postgres` controls all Hecate-owned durable state,
  including tasks, the task queue, projects, project memory, chats, usage
  events, and settings.
- `HECATE_POSTGRES_URL=postgres://...` or `DATABASE_URL=postgres://...` is
  required when `HECATE_BACKEND=postgres`. Optional Postgres knobs:
  `HECATE_POSTGRES_TABLE_PREFIX`, `HECATE_POSTGRES_MAX_OPEN_CONNS`, and
  `HECATE_POSTGRES_MAX_IDLE_CONNS`.
- `HECATE_TASK_QUEUE_WORKERS=<int>`
- `HECATE_TASK_QUEUE_BUFFER=<int>`
- `HECATE_TASK_QUEUE_LEASE_SECONDS=<int>`
- `HECATE_TASK_MAX_CONCURRENT_PER_TENANT=<int>` (`0` disables the limit)
- `HECATE_TASK_RECONCILE_INTERVAL=<duration>` (default `30s`; Go duration string — e.g. `"1m"`; how often the periodic reconciler scans for stalled runs; runs stuck in `running` longer than 3× `HECATE_TASK_QUEUE_LEASE_SECONDS` are automatically re-queued and emit `gap.run_disconnected` with `reason=worker_lease_expired`)
- `HECATE_TASK_MAX_MCP_SERVERS_PER_TASK=<int>` (default `16`; caps `mcp_servers` entries on `agent_loop` task creates; `0` disables the check)
- `HECATE_TASK_MCP_CLIENT_CACHE_MAX_ENTRIES=<int>` (default `256`; soft cap on the gateway-wide MCP client cache; LRU-idle eviction kicks in at the cap, with fail-open when every entry is in use)
- `HECATE_TASK_MCP_CLIENT_CACHE_PING_INTERVAL=<duration>` (default `60s`; how often the cache pings each idle cached upstream to detect wedged subprocesses; `0` disables the proactive health check, leaving only reactive eviction in `Pool.Call`)
- `HECATE_TASK_MCP_CLIENT_CACHE_PING_TIMEOUT=<duration>` (default `5s`; per-ping deadline; failure or timeout evicts the entry)

When `HECATE_BACKEND=sqlite` or `postgres`,
tasks/runs/steps/approvals/artifacts/run-events are persisted and the stream
replay cursor is durable across restarts. Workers claim queue items with
renewable leases, so pending runs survive process restarts and can be recovered
when a lease expires.

For `agent_loop`-specific knobs (max turns, system-prompt layers, HTTP policy for the `http_request` tool), see [`agent-runtime.md`](agent-runtime.md#configuration-knobs).

`GET /hecate/v1/system/stats` also reports queue health fields including queue depth, queue capacity, worker count, and `queue_backend`.

The response also surfaces `agent_adapter_approval_mode` — the configured mode for the external-agent approval coordinator: `"auto"`, `"prompt"`, or `"deny"`. Operators surface a danger banner in the UI when this is `"auto"` since every agent `RequestPermission` is permitted without review. Empty when the gateway was built without an approval coordinator (legacy configs / test fixtures).

The same payload includes `rtk_available` and optional `rtk_path` so the UI can offer the per-chat **Compact command output** toggle only when the optional `rtk` helper is installed in the gateway process `PATH`. Hecate never enables RTK automatically; new chats default to compact output off.

`GET /hecate/v1/system/mcp/cache` returns a snapshot of the shared MCP client cache:

```json
{
  "object": "mcp_cache_stats",
  "data": {
    "checked_at": "2026-04-29T01:00:00.123Z",
    "configured": true,
    "entries": 4,
    "in_use": 1,
    "idle": 3
  }
}
```

`configured: false` means no cache is wired (the deploy explicitly disabled it via `Handler.SetMCPClientCache(nil)`); the counter fields are present but zero so operator UIs can render a "no cache" cell instead of error-handling. `in_use` is the **sum** of refcounts across all entries (an entry held by two concurrent runs counts as 2), not the number of entries with at least one acquirer; `idle` is the count of entries with refcount=0. See [`mcp.md`](mcp.md#lifecycle-and-caching) for the underlying contract.

`GET /hecate/v1/mcp/registry/servers` searches an MCP Registry server list. It defaults to the official registry at `https://registry.modelcontextprotocol.io`; pass `registry_url` to target a private registry. Supported query parameters mirror the read-only registry API: `search`, `cursor`, `limit` (default 30, capped at 100), `updated_since` (RFC3339), `version`, and `include_deleted`. The endpoint is local-only: non-loopback sockets and forwarded-client headers are rejected before the outbound registry request.

```json
GET /hecate/v1/mcp/registry/servers?search=weather&limit=10

→ 200
{
  "object": "mcp_registry_servers",
  "data": {
    "registry_url": "https://registry.modelcontextprotocol.io",
    "servers": [
      {
        "server": {
          "name": "io.github/example/weather",
          "title": "Weather",
          "description": "Forecasts",
          "version": "1.0.0",
          "remotes": [
            {
              "type": "streamable-http",
              "url": "https://weather.example/mcp",
              "headers": [
                {"name": "Authorization", "isRequired": true, "isSecret": true}
              ]
            }
          ],
          "packages": [
            {
              "registryType": "npm",
              "identifier": "@example/weather",
              "runtimeHint": "npx",
              "transport": {"type": "stdio"}
            }
          ],
          "_meta": {"publisher": "example"}
        },
        "_meta": {"rank": 1},
        "install_hints": [
          {
            "source": "remote",
            "transport": "streamable-http",
            "supported": true,
            "url": "https://weather.example/mcp",
            "required_secrets": ["MCP_AUTHORIZATION"],
            "hecate_config": {
              "name": "weather",
              "url": "https://weather.example/mcp",
              "headers": {"Authorization": "$MCP_AUTHORIZATION"}
            }
          },
          {
            "source": "package",
            "transport": "stdio",
            "supported": false,
            "registry_type": "npm",
            "identifier": "@example/weather",
            "runtime_hint": "npx",
            "unsupported_reason": "package entries require an operator-chosen local runtime command before Hecate can probe them"
          }
        ]
      }
    ],
    "next_cursor": "cursor-2",
    "count": 1
  }
}
```

Registry discovery does not install packages, spawn servers, or call `tools/list`; it only returns catalog metadata and Hecate-specific connection hints. Use `POST /hecate/v1/mcp/probe` on a selected config to inspect the live tool catalog before committing it to a task.

`POST /hecate/v1/mcp/probe` is the dry-run discovery surface for an MCP server config. It accepts a single MCP server entry (same shape as one item in the task-create `mcp_servers` array — `name` defaults to `probe` when omitted), brings the server up the way an `agent_loop` run would (same secret resolution, same uncached spawn path), calls `tools/list`, and tears it down. Returns the upstream's tool catalog so operators can confirm the config before committing it to a task. The endpoint is local-only: non-loopback sockets and forwarded-client headers are rejected before command handling.

```json
POST /hecate/v1/mcp/probe
{
  "command": "bunx",
  "args": ["--bun", "@modelcontextprotocol/server-filesystem", "/workspace"]
}

→ 200
{
  "object": "mcp_probe",
  "data": {
    "tools": [
      {
        "name": "get_weather",
        "description": "...",
        "input_schema": {...},
        "_meta": {
          "ui": {
            "resourceUri": "ui://weather/dashboard",
            "visibility": ["model", "app"]
          }
        },
        "ui_resource_uri": "ui://weather/dashboard",
        "ui_visibility": ["model", "app"],
        "model_visible": true
      },
      {
        "name": "refresh_dashboard",
        "input_schema": {...},
        "_meta": {
          "ui": {
            "resourceUri": "ui://weather/dashboard",
            "visibility": ["app"]
          }
        },
        "ui_resource_uri": "ui://weather/dashboard",
        "ui_visibility": ["app"],
        "model_visible": false
      }
    ]
  }
}
```

Tool names come back un-namespaced — the operator wants to see what the upstream itself calls them, not the gateway's runtime alias. MCP Apps metadata is preserved when present: `_meta` is the raw upstream object, `ui_resource_uri` and `ui_visibility` are derived convenience fields, and `model_visible: false` means the tool is app-only and will not be shown to the agent-loop model. Bounded by a 10-second deadline; a stuck upstream surfaces as a 400 with the diagnostic rather than wedging the request.

`POST /hecate/v1/system/reset-data` resets local operator state without restarting the gateway. It deletes chat sessions, projects, project memory entries and candidates, project work-coordination rows, plugin registry records, agent profiles, tasks, configured providers, policy rules, and saved external-agent approval grants. Chat sessions are deleted through the normal chat-delete path first, so live external-agent sessions are asked to delete their native ACP session before their rows disappear. If an adapter does not support `session/delete`, Hecate falls back to `session/close` and still tears down the owned process. When SQLite or Postgres is configured, it then clears remaining Hecate-prefixed database table rows while preserving schemas. Workspace files and external CLI auth files are not touched. The endpoint is local-only and blocked in remote runtime mode: non-loopback sockets and forwarded-client headers are rejected.

```json
→ 200
{
  "object": "system_reset",
  "data": {
    "projects_deleted": 1,
    "project_work_rows_deleted": 3,
    "plugins_deleted": 1,
    "agent_profiles_deleted": 1,
    "chat_sessions_deleted": 2,
    "tasks_deleted": 1,
    "providers_deleted": 1,
    "policy_rules_deleted": 1,
    "agent_approval_grants_deleted": 1,
    "database_rows_deleted": 8
  }
}
```

If a running chat does not settle before the bounded close wait, or a standalone task still has an active run, the endpoint returns `409 conflict`; retry after the chat finishes cancelling or cancel the active task first.

`POST /hecate/v1/system/shutdown` requests an orderly process shutdown. The desktop app uses this from its window-close confirmation flow so the gateway runs the same drain path `SIGINT`/`SIGTERM` take (retention cancel, runner drain — including MCP subprocess teardown, then HTTP server shutdown) instead of being SIGKILL'd by the child-process handle. Empty body, returns `202` and an `object: "system_shutdown"` ack; the signal fires asynchronously after a short delay so the response can flush before the listener tears down. Clients that need to observe the gateway actually exiting should poll `/healthz` until it stops responding (the desktop app uses a 12-second deadline). The endpoint is local-only: non-loopback sockets and forwarded-client headers are rejected.

The shipped `cmd/hecate` binary wires `Handler.SetQuitFunc` unconditionally, so the endpoint is available in every standard deployment (Tauri sidecar, Docker, systemd) from the gateway's local network namespace. In Docker, call it from inside the container or use the normal orchestrator stop path (`docker stop`, Compose, systemd, Kubernetes); requests through a published port usually arrive from a non-loopback bridge address and are rejected. Returns `503` with `error.code = "gateway_error"` when the endpoint is not wired; this path is reached only by test harnesses or custom embedders that build a `Handler` without calling `SetQuitFunc`.

## Usage endpoints

Hecate records usage for operator visibility, not global spend enforcement.
Cloud-provider calls may include measured tokens and known or provider-reported
cost. Local-provider rows are still recorded as usage events, but the Usage UI
hides them from the cloud-spend table because they do not consume cloud-provider
tokens. External-agent usage remains agent-reported and is surfaced on chat
messages when the agent provides it.

### `GET /hecate/v1/usage/summary`

Returns the cumulative known/reported spend for a usage bucket. In the local
single-operator shape, clients usually call this without query parameters and
read the global bucket.

Query parameters:

| Name       | Meaning                                                                   |
| ---------- | ------------------------------------------------------------------------- |
| `scope`    | `global` (default) or `provider`. Unknown values fall back to `global`.   |
| `provider` | Provider id when `scope=provider`.                                        |
| `key`      | Explicit internal usage key. Intended for diagnostics, not normal UI use. |

```json
GET /hecate/v1/usage/summary
→ 200
{
  "object": "usage_summary",
  "data": {
    "key": "global",
    "scope": "global",
    "backend": "sqlite",
    "used_micros_usd": 1600,
    "used_usd": "$0.001600"
  }
}
```

### `GET /hecate/v1/usage/events`

Returns recent append-only usage rows, newest first. The UI uses these rows to
show cloud-provider tokens and known/reported cost. The endpoint is intentionally
read-only.

Query parameters:

| Name    | Meaning                                                                 |
| ------- | ----------------------------------------------------------------------- |
| `limit` | Maximum rows to return. Defaults to the configured usage history limit. |

```json
GET /hecate/v1/usage/events?limit=20
→ 200
{
  "object": "usage_events",
  "data": [
    {
      "type": "usage",
      "scope": "provider",
      "provider": "openai",
      "model": "gpt-5.4-mini",
      "request_id": "req_...",
      "amount_micros_usd": 1600,
      "amount_usd": "$0.001600",
      "prompt_tokens": 920,
      "completion_tokens": 280,
      "total_tokens": 1200,
      "timestamp": "2026-05-14T10:00:00Z"
    }
  ]
}
```

## Health and discovery endpoints

### `GET /healthz`

Liveness probe. Returns `200` with the gateway's current time and version. Suitable for sidecar health checks, Kubernetes `livenessProbe` / `readinessProbe`, and Docker Compose `healthcheck`.

```json
GET /healthz
→ 200
{
  "status": "ok",
  "time": "2026-04-29T12:34:56Z",
  "version": "0.0.0-dev"
}
```

The endpoint is intentionally cheap: it doesn't touch the database, providers, or queue. A `200` here means "the process is up and serving HTTP," not "every backend is healthy." For deeper signal use `GET /hecate/v1/system/stats`.

### `GET /hecate/v1/providers/presets`

Provider catalog the UI's task-create form uses to render the provider picker. Each entry carries the operator-facing display name, the kind (`cloud` / `local`), the protocol Hecate speaks to it, the `BASE_URL` / `API_KEY` env-var pattern (so the UI can show which `PROVIDER_<NAME>_*` variables to set), and a short `env_snippet` ready to paste into `.env`.

```json
GET /hecate/v1/providers/presets
→ 200
{
  "object": "provider_presets",
  "data": [
    {
      "id": "openai",
      "name": "OpenAI",
      "kind": "cloud",
      "protocol": "openai",
      "base_url": "https://api.openai.com/v1",
      "api_key_env": "OPENAI_API_KEY",
      "docs_url": "https://platform.openai.com/docs",
      "description": "OpenAI's Responses + Chat Completions API.",
      "env_snippet": "OPENAI_API_KEY=your_api_key_here"
    },
    ...
  ]
}
```

The list is built from `config.BuiltInProviders()` — see [`docs/operator/providers.md`](../operator/providers.md) for the full catalog and OpenAI-compatible custom-endpoint flow.

### `GET /hecate/v1/providers/status`

Runtime provider readiness snapshot. The UI uses this endpoint to explain
whether a configured provider can receive traffic right now and why it may be
skipped by routing.

Pass `refresh=true` or `refresh=1` when the operator explicitly asks to
refresh provider discovery. Normal reads keep using the provider capability
cache; explicit refresh bypasses the completed cache while still sharing any
same-provider discovery request already in flight.

```json
GET /hecate/v1/providers/status
→ 200
{
  "object": "provider_status",
  "data": [
    {
      "name": "ollama",
      "kind": "local",
      "status": "healthy",
      "healthy": true,
      "base_url": "http://127.0.0.1:11434/v1",
      "models": ["llama3.1:8b"],
      "model_count": 1,
      "credential_state": "not_required",
      "credential_ready": true,
      "routing_ready": true,
      "readiness": {
        "status": "ok",
        "reason": "ready",
        "message": "Provider \"ollama\" is ready for routing."
      },
      "readiness_checks": [
        {
          "name": "credentials",
          "status": "ok",
          "reason": "not_required",
          "message": "No credentials are required for this provider."
        },
        {
          "name": "models",
          "status": "ok",
          "reason": "models_discovered",
          "message": "1 model discovered."
        },
        {
          "name": "health",
          "status": "ok",
          "reason": "healthy",
          "message": "Provider health checks are passing."
        },
        {
          "name": "routing",
          "status": "ok",
          "reason": "routable",
          "message": "Provider is eligible for routing."
        }
      ]
    }
  ]
}
```

`readiness` is the compact provider-level answer for cards and tables:
`status` is `ok`, `warning`, `blocked`, or `unknown`; `reason` is stable enough
for UI branching; `message` is safe to show directly to the operator; and
`operator_action` appears when there is a repair step.

`readiness_checks` is the canonical operator-facing checklist. It prevents
clients from guessing readiness by combining unrelated raw fields. Check names
are currently `credentials`, `models`, `health`, and `routing`; statuses use the
same `ok` / `warning` / `blocked` / `unknown` set. `reason` is stable enough for
UI branching, while `message` is safe to show directly to the operator.
When a check needs operator action, `operator_action` carries the canonical
repair step; clients should prefer it over deriving their own copy from
`reason`. For example `credential_missing` includes "add or rotate
credentials", `no_models` includes "start the provider and load at least one
model", and `provider_rate_limited` includes "wait for cooldown or route
elsewhere".

`routing_ready=false` means the router currently skips the provider. The
matching `routing_blocked_reason` and the `reason` on the
`readiness_checks[]` item whose `name` is `routing` use the same vocabulary as
route diagnostics: `credential_missing`, `provider_disabled`,
`provider_rate_limited`, `circuit_open`, `provider_unhealthy`, and `no_models`.
Other checks use reason values scoped to that check, such as
`default_model_only` for model-discovery fallback, `discovery_failed` when the
provider could not return a model list, `self_referential` when a provider URL
points back to Hecate, `provider_slow` when a latency-degraded provider remains
routable, or `not_required` for local providers that do not need credentials.

The trace inspector reuses the same vocabulary in route candidates. A selected
candidate is paired with the route reason (`requested_model`, `pinned_provider`,
`provider_default_model`, etc.); skipped candidates carry `skip_reason` values
such as `policy_denied`, `provider_rate_limited`,
`provider_less_stable`, or `provider_unavailable`. This keeps the operator
debugging path consistent: Connections explains whether a route is possible now,
and Observability explains how a specific request moved through the candidates.

### `GET /hecate/v1/settings/providers/local-discovery`

Advisory discovery for the Connections view's **Add provider → Local** catalog.
The gateway checks whether the expected provider command is on `PATH` and
probes each unique default local endpoint once. Shared endpoints, such as the
`llama.cpp` / `LocalAI` default `127.0.0.1:8080/v1`, are only called once and
then reused for every matching preset card.

This endpoint is local-only and returns `403` in remote runtime mode. Hosted
runtimes also disable local provider presets and `kind=local` providers unless
launched with `HECATE_REMOTE_ALLOW_LOCAL_PROVIDERS=1`.

```json
GET /hecate/v1/settings/providers/local-discovery
→ 200
{
  "object": "local_provider_discovery",
  "data": [
    {
      "preset_id": "ollama",
      "name": "Ollama",
      "base_url": "http://127.0.0.1:11434/v1",
      "probe_url": "http://127.0.0.1:11434/api/tags",
      "status": "running",
      "command": "ollama",
      "command_available": true,
      "command_path": "/opt/homebrew/bin/ollama",
      "http_available": true,
      "model_count": 2,
      "models": ["llama3.1:8b", "qwen2.5:7b"]
    }
  ]
}
```

`status` is one of:

- `running` — the HTTP probe returned 2xx.
- `installed` — the command is present on `PATH`, but the default HTTP
  endpoint did not respond.
- `not_detected` — neither the command nor the default HTTP endpoint was found.

This endpoint does not create or mutate provider records. It is a UX helper for
the picker and is local-only: non-loopback sockets, forwarded-client headers,
and remote runtime mode are rejected. Routing readiness still comes from
`GET /hecate/v1/providers/status` after the operator adds a provider.

### `GET /v1/models`

Lists models currently known to configured providers. Each row includes Hecate
metadata under `metadata`, including the effective model capability snapshot
used by the Chats target picker.

Pass `refresh=true` or `refresh=1` for an explicit operator refresh. Without
that query parameter, the endpoint keeps normal provider discovery cache
behavior.

```json
GET /v1/models
→ 200
{
  "object": "list",
  "data": [
    {
      "id": "qwen2.5-coder",
      "object": "model",
      "owned_by": "ollama",
      "metadata": {
        "provider": "ollama",
        "provider_kind": "local",
        "default": false,
        "discovery_source": "provider",
        "capabilities": {
          "tool_calling": "unknown",
          "streaming": true,
          "max_context_tokens": 32768,
          "source": "provider"
        },
        "readiness": {
          "provider": "ollama",
          "matched_provider": "ollama",
          "model": "qwen2.5-coder",
          "ready": true,
          "status": "ok",
          "reason": "model_available",
          "message": "Provider \"ollama\" reports model \"qwen2.5-coder\".",
          "routing_ready": true,
          "provider_status": "healthy"
        }
      }
    }
  ]
}
```

`capabilities.tool_calling` is one of `unknown`, `none`, `basic`, or
`parallel`. Task-backed Hecate Chat requires a known tool-capable value
(`basic` or `parallel`). When tools are on but the selected model is
`unknown` or `none`, the operator UI keeps normal chat available by sending the
turn as direct model chat and showing a compact capability hint. Local/custom
OpenAI-compatible providers often report `unknown`; Ollama models are enriched
from the native `/api/show` capability list when available. Tool usage is a
per-chat setting; model capability metadata is observed from provider/catalog
data rather than edited globally.

`metadata.readiness` is the backend-owned provider/model readiness snapshot for
that discovered row. Chats should use it before sending instead of inferring
routability from model names alone: a model can appear in discovery while its
provider is credential-blocked, circuit-open, disabled, or otherwise not
routable. When `ready=false`, show `message` and `operator_action` directly and
use `reason`, `provider_status`, `provider_blocked_reason`, and
`suggested_models` for compact diagnostics.

### `GET /hecate/v1/agent-adapters`

External coding-agent catalog. This is the first discovery surface for
External Agent chats: it reports the agent runtimes Hecate knows how to
supervise and whether their command can be found. This endpoint is deliberately
cheap so the app can render startup state without spawning coding-agent CLIs.
Use `POST /hecate/v1/agent-adapters/{id}/probe` for live version, auth,
capability, and launch-control discovery.

```json
GET /hecate/v1/agent-adapters
→ 200
{
  "object": "agent_adapters",
  "data": [
    {
      "id": "codex",
      "name": "Codex",
      "kind": "acp",
      "command": "codex-acp-adapter",
      "available": true,
      "status": "available",
      "path": "/Users/alice/.local/bin/codex-acp-adapter",
      "cost_mode": "external",
      "supported_range": ">=0.1.0",
      "version_outside_range": false,
      "supports_authenticate": true,
      "supports_logout": true,
      "auth_status": "unknown",
      "capabilities": [
        {
          "id": "prompt_session",
          "name": "sessions",
          "status": "supported"
        },
        {
          "id": "permissions",
          "name": "permissions",
          "status": "supported"
        },
        {
          "id": "terminal_rpc",
          "name": "terminal RPC",
          "status": "operator_opt_in"
        }
      ],
      "credential_modes": [
        {
          "id": "local_login",
          "name": "Local CLI login",
          "remote_allowed": false
        },
        {
          "id": "api_key",
          "name": "API key",
          "remote_allowed": true,
          "env_keys": ["OPENAI_API_KEY", "CODEX_API_KEY"]
        }
      ]
    },
    {
      "id": "grok_build",
      "name": "Grok Build",
      "kind": "acp",
      "command": "grok",
      "args": ["agent", "stdio"],
      "available": true,
      "status": "available",
      "path": "/Users/alice/.local/bin/grok",
      "cost_mode": "external",
      "docs_url": "https://docs.x.ai/build/cli/headless-scripting#acp",
      "supported_range": ">=0.1.0",
      "supports_authenticate": false,
      "supports_logout": false,
      "auth_status": "unknown"
    },
    {
      "id": "cursor_agent",
      "name": "Cursor Agent",
      "kind": "acp",
      "command": "cursor-agent",
      "args": ["acp"],
      "available": true,
      "status": "available",
      "path": "/Users/alice/.local/bin/cursor-agent",
      "cost_mode": "external",
      "supported_range": ">=0.1.0",
      "version_outside_range": false,
      "supports_authenticate": false,
      "supports_logout": false,
      "auth_status": "unknown"
    },
    {
      "id": "claude_code",
      "name": "Claude Code",
      "kind": "acp",
      "command": "claude-code-acp-adapter",
      "available": false,
      "status": "missing",
      "error": "exec: \"claude-code-acp-adapter\": executable file not found in $PATH",
      "cost_mode": "external",
      "supported_range": ">=0.1.0",
      "supports_authenticate": true,
      "supports_logout": true,
      "auth_status": "unknown",
      "capabilities": [
        {
          "id": "prompt_session",
          "name": "sessions",
          "status": "supported"
        },
        {
          "id": "permissions",
          "name": "permissions",
          "status": "supported"
        },
        {
          "id": "terminal_rpc",
          "name": "terminal RPC",
          "status": "operator_opt_in"
        }
      ],
      "claude_code_cli": {
        "available": true,
        "command": "/Users/alice/.local/bin/claude",
        "executable_path": "/Users/alice/.local/bin/claude"
      }
    }
  ]
}
```

`adapter_version` and `agent_version` are omitted from the catalog response.
They are populated by the explicit probe response after Hecate starts the ACP
adapter and runs the live diagnostics. `version_outside_range` remains `false`
until a probed version is known to fall outside `supported_range`.

`auth_status` is `unknown` on the cheap catalog path unless a dev or remote
runtime override can classify it without spawning a CLI. Use `POST
/hecate/v1/agent-adapters/{id}/probe` for the full ACP handshake and login /
billing classification.

`supports_authenticate` and `supports_logout` tell clients whether Hecate can
call ACP `authenticate` or `logout` for this adapter. UIs should use these
catalog fields instead of hard-coding adapter IDs; the actions are currently
enabled for the standalone Go Codex and Claude Code adapters.

`capabilities` is Hecate's catalog-level ACP contract for the row. It describes
the features Hecate knows how to supervise for that adapter family, such as
sessions, structured activity, cancellation, permission approvals, MCP server
handoff, config options, terminal callbacks, authenticate, and logout. Capability
`status` is one of:

| Status              | Meaning                                                                   |
| ------------------- | ------------------------------------------------------------------------- |
| `supported`         | Hecate supports the surface for this adapter family.                      |
| `adapter_dependent` | Hecate will use the surface only when the live ACP adapter advertises it. |
| `operator_opt_in`   | Hecate supports the surface only behind an explicit operator setting.     |
| `not_supported`     | Hecate should not show or invoke this surface.                            |

Probe results remain authoritative for live ACP Initialize features. For
example, a probed adapter can turn `supports_authenticate` or `supports_logout`
off even when the catalog expected them.

`credential_modes` describes how the adapter can authenticate. `local_login`
means operator-local CLI/browser login state and is not sufficient for remote
runtime requests by default. Remote runtime mode normally accepts only rows where
`remote_allowed=true` and one listed `env_keys` value is present in the runtime
environment. A single-user personal remote runtime may opt into runtime-local
login state with `HECATE_PERSONAL_REMOTE_EXTERNAL_AGENT_LOGINS=1`; then
`remote_credential_mode` may report `local_login`, but the `hecate_remote` build
tag still strips local-login modes entirely. In remote runtime mode, catalog rows
include `remote_credential_mode`, `remote_credential_ok`, and
`remote_credential_hint` when applicable; adapters without allowed credentials
are reported as `available=false`, `auth_status="unauthenticated"` before
Hecate attempts command discovery.

These are **external agents**, not model providers. They run ACP-compatible
coding agents under Hecate supervision; cost is reported as `external`
until an agent can supply structured usage.

ACP terminal callbacks are not advertised by default. Operators must set
`HECATE_AGENT_ADAPTER_TERMINALS=1` before External Agent sessions include
`clientCapabilities.terminal=true`; remote runtime mode also requires
`HECATE_REMOTE_ALLOW_ACP_TERMINALS=1`. Approved `terminal/create` requests are
scoped to the selected workspace and routed through the External Agent approval
coordinator before command spawn.

`config_options` are omitted from the catalog response. Hecate returns
launch-control options on explicit probe responses and prepared chat sessions,
where it is acceptable to run the adapter's help/model discovery or consume the
ACP session's own controls. Values prefixed with `__hecate_no_` are explicit
"not selected" sentinels. Some options are optional; launch-model options can
be required by the adapter definition and cause `400 chat.model_required` at
session creation until a real value is selected. Agent-owned ACP model state
appears on the prepared chat session and is updated with ACP
`session/set_model`.

### `POST /hecate/v1/agent-adapters/{id}/probe`

Re-runs discovery for one adapter, then performs the same end-to-end ACP probe
as `/health`. The response includes the fresh catalog row plus the health
result, so UIs can update a single Connections row after the operator logs in or
installs a missing dependency.

```json
POST /hecate/v1/agent-adapters/codex/probe
→ 200
{
  "object": "agent_adapter_probe",
  "data": {
    "adapter": {
      "id": "codex",
      "name": "Codex",
      "kind": "acp",
      "command": "codex-acp-adapter",
      "available": true,
      "status": "available",
      "supports_authenticate": true,
      "supports_logout": true,
      "auth_status": "ok"
    },
    "health": {
      "adapter_id": "codex",
      "status": "ready",
      "stage": "ready",
      "capabilities_known": true,
      "supports_authenticate": true,
      "supports_logout": true,
      "supports_load_session": true,
      "auth_methods": [
        {
          "id": "agent-login",
          "kind": "agent",
          "name": "Agent login"
        }
      ],
      "duration_ms": 412
    }
  }
}
```

Status codes:

- `200 OK` when the adapter id is registered; `health.status` carries
  `ready`, `not_installed`, `auth_required`, or `error`.
- `404 not_found` when the adapter id is not registered.

When ACP `Initialize` succeeds, `health.capabilities_known` is true and the
probe response uses the live capabilities advertised by the adapter to refresh
`data.adapter.supports_authenticate` and `data.adapter.supports_logout`.
Catalog discovery remains the offline fallback before a probe runs. Hecate only
marks `supports_authenticate` true for ACP auth method `agent-login`, because
that is the method the local `/authenticate` endpoint invokes.

### `GET /hecate/v1/agent-adapters/{id}/health`

Probes a single adapter end-to-end and classifies the outcome so operators can
distinguish "binary missing" from "binary on PATH but auth failing" without
reading raw error text. The probe does spawn → ACP `Initialize` → ACP
`NewSession` against a temporary workspace → terminate; it never issues a
chat prompt.

```json
GET /hecate/v1/agent-adapters/codex/health
→ 200
{
  "object": "agent_adapter_health",
  "data": {
    "adapter_id": "codex",
    "status": "auth_required",
    "stage": "initialize",
    "path": "/Users/alice/.local/bin/codex-acp-adapter",
    "error": "Authentication required",
    "hint": "Adapter started but failed authentication. Try the adapter's CLI login flow or set its API-key env var.",
    "capabilities_known": true,
    "supports_authenticate": true,
    "supports_logout": true,
    "supports_load_session": true,
    "auth_methods": [
      {
        "id": "agent-login",
        "kind": "agent",
        "name": "Agent login"
      }
    ],
    "duration_ms": 412
  }
}
```

`status` is one of:

- `ready` — spawn + Initialize + NewSession all succeeded.
- `not_installed` — binary not on PATH.
- `auth_required` — process started but Initialize or NewSession failed with
  an auth-shaped error (`Authentication required`, `Please log in`, `API key`,
  `Credit balance is too low`, `401`, `403`, …).
- `error` — anything else. `error` and `stderr` carry the verbatim diagnostic
  so the operator can act on it. Timeout and deadline diagnostics stay in this
  bucket with a hint to retry from Connections after resolving stuck CLI,
  browser, or login prompts.

`stage` reports which step in the sequence completed (on success) or failed (on
error): `lookup` / `spawn` / `initialize` / `new_session` / `ready`.

If ACP `Initialize` succeeds, the health payload also includes
`capabilities_known`, `supports_authenticate`, `supports_logout`,
`supports_load_session`, and a non-secret `auth_methods` summary. These fields
can be present even when `status` is `auth_required` because auth failures may
occur after `Initialize` during `NewSession`. Env-var names and terminal env
payloads are intentionally not exposed through health responses.

Status codes:

- `200 OK` with the typed result on every classification (`ready`,
  `not_installed`, `auth_required`, `error`). The probe completing
  successfully is itself a 200; the agent status lives in the body.
- `404 not_found` when the adapter id is not registered.

The probe creates and immediately abandons a fresh ACP session, so agents that
bill on session creation will see one no-op session per call. Agents that bill
on prompt completion see no charge.

### `POST /hecate/v1/agent-adapters/{id}/authenticate`

Asks one registered ACP adapter to start its own local login flow. Hecate
spawns the adapter, performs ACP `Initialize`, verifies that the adapter
advertised agent auth method id `agent-login`, calls ACP `authenticate` with
that method id, and then terminates the process. This is an adapter-account
action: it does not create or mutate Hecate chat sessions, approvals, or
transcripts. Remote runtime mode treats this as local-only; hosted deployments
should use the adapter's declared remote-safe env-key credential modes instead.

```json
POST /hecate/v1/agent-adapters/codex/authenticate
→ 200
{
  "object": "agent_adapter_authenticate",
  "data": {
    "adapter_id": "codex",
    "status": "authenticated",
    "method_id": "agent-login",
    "path": "/Users/alice/.local/bin/codex-acp-adapter",
    "duration_ms": 328
  }
}
```

Status codes:

- `200 OK` when the adapter accepted ACP `authenticate`.
- `404 not_found` when the adapter id is not registered.
- `502 chat.adapter_unavailable` when the adapter binary cannot start,
  initialize, does not advertise ACP `agent-login`, or cannot complete ACP
  `authenticate`.

### `POST /hecate/v1/agent-adapters/{id}/logout`

Asks one registered ACP adapter to clear its own account/session state. Hecate
spawns the adapter, performs ACP `Initialize`, verifies that the adapter
advertised ACP `auth.logout`, calls ACP `logout`, and then terminates the
process. This is an adapter-account action: it does not delete Hecate chat
sessions, close live adapter sessions, revoke approvals, or mutate transcripts.

```json
POST /hecate/v1/agent-adapters/codex/logout
→ 200
{
  "object": "agent_adapter_logout",
  "data": {
    "adapter_id": "codex",
    "status": "logged_out",
    "path": "/Users/alice/.local/bin/codex-acp-adapter",
    "duration_ms": 328
  }
}
```

Status codes:

- `200 OK` when the adapter accepted ACP `logout`.
- `404 not_found` when the adapter id is not registered.
- `502 chat.adapter_unavailable` when the adapter binary cannot start,
  initialize, does not advertise ACP `auth.logout`, or cannot complete ACP
  `logout`.

## Plugin registry endpoints

The plugin registry is a local catalog and policy-review surface. Registry rows
store a raw Hecate-native manifest plus normalized capabilities, requested
permissions, auth-binding requests, and validated MCP-server mount candidates.
They do not run plugin code, start MCP servers, mount tools, call external
providers, or grant secrets.
`manifest_digest` is a SHA-256 digest of Hecate's canonicalized manifest JSON,
so semantically equivalent manifests share the same digest even when caller
formatting differs.

The initial routes are local-only in remote-runtime mode:

- `GET /hecate/v1/plugins`
- `GET /hecate/v1/plugins/{id}`
- `POST /hecate/v1/plugins/install-local`
- `PATCH /hecate/v1/plugins/{id}`
- `GET /hecate/v1/plugins/{id}/health`

`POST /hecate/v1/plugins/install-local` accepts either a manifest directly or
an object with `manifest` and optional `source_ref`. The current slice records
the manifest JSON sent by the operator/client; it does not read arbitrary local
paths or execute package lifecycle hooks.

```json
POST /hecate/v1/plugins/install-local
{
  "source_ref": "/plugins/github/plugin.json",
  "manifest": {
    "schema_version": "hecate.plugin.v0",
    "id": "github",
    "name": "GitHub",
    "version": "0.1.0",
    "permissions": ["network:github.com", "secret:github_token"],
    "capabilities": {
      "connectors": [
        {
          "id": "issues",
          "display_name": "Issues",
          "auth": [{ "name": "github_token", "kind": "token" }]
        }
      ],
      "mcp_servers": [
        {
          "id": "github-mcp",
          "name": "github",
          "display_name": "GitHub MCP",
          "transport": "stdio",
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-github"],
          "env": { "GITHUB_TOKEN": "$GITHUB_TOKEN" },
          "approval_policy": "require_approval"
        }
      ],
      "slash_commands": [{ "name": "github" }]
    }
  }
}
```

MCP server capabilities may use the inline shape above or place the same fields
inside `config`. The registry normalizes either shape into an `mcp_server`
response object. Exactly one of `command` or `url` must be set; `transport`, if
present, must match (`stdio` for `command`, `http` for `url`). `env` and
`headers` values must be whole `$VAR_NAME` references; `env` keys must be valid
process environment names and `headers` keys must be valid HTTP header names.
Literal values and manifest-provided encrypted blobs are rejected so plugin
registry records do not become a credential store. Unknown MCP server
capability fields are rejected in this native v0 schema; compatibility
importers should translate or drop host-specific fields such as `cwd` or
`timeout` before installing a Hecate manifest.

`approval_policy` is recorded as requested mount metadata only. When explicit
mounting into profiles or task/chat starts lands, Hecate's own approval policy
remains authoritative; a plugin-declared `auto` value must not downgrade a
Hecate-owned requirement for approval.

```json
→ 200
{
  "object": "plugin",
  "data": {
    "id": "github",
    "name": "GitHub",
    "version": "0.1.0",
    "source_kind": "local_path",
    "source_ref": "/plugins/github/plugin.json",
    "manifest_schema_version": "hecate.plugin.v0",
    "manifest_digest": "sha256:...",
    "requested_permissions": [
      { "value": "network:github.com", "classification": "advisory" },
      { "value": "secret:github_token", "classification": "advisory" }
    ],
    "registry_state": "valid",
    "enabled": false,
    "capabilities": [
      {
        "id": "issues",
        "kind": "connector",
        "display_name": "Issues",
        "enabled": true
      },
      {
        "id": "github-mcp",
        "kind": "mcp_server",
        "display_name": "GitHub MCP",
        "enabled": true,
        "mcp_server": {
          "name": "github",
          "transport": "stdio",
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-github"],
          "env": { "GITHUB_TOKEN": "$GITHUB_TOKEN" },
          "approval_policy": "require_approval"
        }
      },
      {
        "id": "github",
        "kind": "slash_command",
        "display_name": "/github",
        "enabled": true
      }
    ],
    "auth": [
      {
        "capability_id": "issues",
        "requested_name": "github_token",
        "kind": "token",
        "status": "unknown"
      }
    ],
    "installed_at": "2026-06-18T10:00:00Z",
    "updated_at": "2026-06-18T10:00:00Z"
  }
}
```

`PATCH /hecate/v1/plugins/{id}` currently toggles only registry metadata:

```json
{
  "enabled": true,
  "capabilities": {
    "issues": { "enabled": false }
  }
}
```

`GET /hecate/v1/plugins/{id}/health` reports manifest review status:
unsupported permissions, unresolved secret-binding requests, disabled
capabilities, and slash-command collisions. It does not call external services.

## Agent profile endpoints

Agent profiles are reusable runtime postures for project work, Hecate Chat,
task-backed runs, and external-agent launches. They describe defaults and
constraints such as instructions, surface, provider/model hints, tool/write/
network posture, approval policy, project-memory policy, context-source
policy, skill ids, and external-agent options. `skill_ids` resolve against the
selected project's skills registry when project work starts. Hecate snapshots
resolved/skipped skill metadata and warnings into the context packet, but it
does not install skills, execute scripts, grant tools, or inject `SKILL.md`
bodies from an agent profile.

Hecate also exposes an immutable built-in profile catalog. Built-ins are
returned by list/get requests with `built_in: true`, can be selected by project
or role defaults, and are resolved at assignment launch without being persisted
as `agent_profiles` rows. `POST`, `PATCH`, and `DELETE` against built-in ids
return `409 conflict`.
If an older persisted row uses a now-reserved built-in id, list/get responses
prefer the immutable built-in and suppress the stored duplicate.

Built-in profile ids:

```text
project_assignment
planning
architecture
implementation
frontend_implementation
design_review
reliability_ops
documentation
review_qa
safe_external_review
```

Built-in profiles are portable runtime postures and intentionally avoid
project-specific `skill_ids`. Bind discovered project skills through roles or
custom profiles so missing project-local skills do not create warnings in every
project.

Profile responses use the normal Hecate envelope:

```json
GET /hecate/v1/agent-profiles
→ 200
{
  "object": "agent_profiles",
  "data": [
    {
      "id": "prof_...",
      "name": "Backend implementer",
      "description": "Go runtime work",
      "instructions": "Prefer narrow, tested patches.",
      "surface": "hecate_task",
      "provider_hint": "anthropic",
      "model_hint": "claude-sonnet-4",
      "execution_profile": "coding_agent",
      "tools_enabled": true,
      "writes_allowed": true,
      "network_allowed": false,
      "approval_policy": "require",
      "project_memory_policy": "visible_only",
      "context_source_policy": "include_enabled",
      "skill_ids": ["backend", "providers"],
      "external_agent_kind": "codex",
      "external_agent_options": { "effort": "high" },
      "built_in": false,
      "created_at": "2026-06-08T12:00:00Z",
      "updated_at": "2026-06-08T12:00:00Z"
    }
  ]
}
```

Supported endpoints:

- `GET /hecate/v1/agent-profiles`
- `POST /hecate/v1/agent-profiles`
- `GET /hecate/v1/agent-profiles/{id}`
- `PATCH /hecate/v1/agent-profiles/{id}`
- `DELETE /hecate/v1/agent-profiles/{id}`

Enums:

| Field                   | Values                                                  |
| ----------------------- | ------------------------------------------------------- |
| `surface`               | `hecate_chat`, `hecate_task`, `external_agent`, `any`   |
| `approval_policy`       | `inherit`, `require`, `block`, `allow`                  |
| `project_memory_policy` | `inherit`, `include`, `visible_only`, `exclude`         |
| `context_source_policy` | `inherit`, `include_enabled`, `visible_only`, `exclude` |

Project assignment starts resolve profiles in this order: role default,
project default, built-in `project_assignment` fallback. The start path
snapshots the resolved profile, provider/model hints, execution profile,
memory policy, context-source policy, skill ids, and warnings into the task/run
context packet. For native project assignments, `project_memory_policy=include`
marks enabled project memory active and includes bounded memory bodies in the
assignment task system prompt. `visible_only` and `inherit` keep enabled memory
as inspect-only context, and `exclude` omits memory records from the packet. For
context sources, `context_source_policy=include_enabled` marks enabled source
metadata active and includes bounded portable `AGENTS.md` workspace-instruction
bodies. `visible_only` and `inherit` keep sources inspect-only, and `exclude`
omits them. Host-specific guidance files remain metadata-only for Hecate prompt
context, and `SKILL.md` bodies are never included by these policies. If the
assignment route uses a cloud provider, included project memory and `AGENTS.md`
bodies are sent to that provider as normal task prompt content.

## Project endpoints

Projects are the durable Hecate identity for a work area: code, research,
writing, design, ops, planning, support, or any other operator-coordinated
effort. A project can exist without a workspace root. When local files or code
matter, it can remember one or more concrete workspace roots and future defaults
such as provider, model, agent profile, tools posture, workspace mode, system
prompt, compact command-output preference, and trusted context-source metadata.

The project catalog implementation is intentionally lightweight:
`GET`/`POST`/`PATCH`/`DELETE /hecate/v1/projects` work, and
`HECATE_BACKEND=sqlite` or `postgres` persists them. Chat sessions can carry an optional
`project_id` so the operator UI can group history by project. Opening chat from
a project-work assignment creates a project-scoped Hecate Chat session and
pre-fills the editable composer with a concise launch-context draft; the draft
is not submitted automatically. Projects can also remember context-source
metadata (`path`, `kind`, `title`, `format`, `scope`, `trust_label`,
`source_category`, arbitrary string metadata, and whether the source is
enabled). Chat message context packets include enabled sources as itemized
`workspace_guidance` metadata for inspection, but Hecate does not inject those
files into prompts yet. Projects also have a project-scoped skills registry
for local `SKILL.md` metadata discovered from `.agents/skills`,
`.hecate/skills`, and enabled guidance-linked local skill roots. The registry
stores ids, title/description metadata, path, root, status, trust label, and
warnings; it does not store or return skill bodies. Project work-coordination
endpoints can persist roles, work items, assignments, and collaboration
artifacts under a project. Assignments may record links to existing task runs
or chat messages, but creating an assignment does not start a task, open a
chat, inject context, or dispatch any agent. Project handoffs are structured
project-scoped records for passing context and a recommended next action from
one assignment/run/chat to another role or assignment. They can link artifacts,
memory entries, and context references, but they do not launch follow-up work
by themselves. Work-item `reviewer_role_ids` are follow-through hints for
review handoffs: the Projects cockpit can prefill a request-review handoff to a
reviewer role, but creating the target assignment and starting it remain
explicit operator actions.
Project memory entries are structured project-scoped records with
Markdown-compatible `body` text; they are not Markdown files, and they are
written only through explicit operator API/UI actions. Enabled project memory
entries appear as itemized chat context-packet metadata with their
`trust_label`, but Hecate still does not perform automatic memory extraction,
embeddings, retrieval ranking, or source-content injection.
Project setup readiness, project health, and Project Operations are read-only
cockpit projections over these existing records. They help the operator choose
the next surface to open, but they do not create durable project records or
start supervised work.

### `GET /hecate/v1/projects`

Lists projects ordered by recent activity, then update/create time.

```json
GET /hecate/v1/projects
→ 200
{
  "object": "projects",
  "data": [
    {
      "id": "proj_...",
      "name": "Hecate",
      "description": "Gateway and agent runtime",
      "roots": [
        {
          "id": "root_...",
          "path": "/Users/alice/src/hecate",
          "kind": "git",
          "git_remote": "git@github.com:hecatehq/hecate.git",
          "git_branch": "master",
          "active": true,
          "created_at": "2026-05-20T12:00:00Z",
          "updated_at": "2026-05-20T12:00:00Z"
        }
      ],
      "context_sources": [
        {
          "id": "ctxsrc_...",
          "kind": "doc",
          "title": "README",
          "path": "README.md",
          "enabled": true,
          "format": "",
          "scope": "",
          "trust_label": "",
          "source_category": "",
          "metadata": {},
          "created_at": "2026-05-20T12:00:00Z",
          "updated_at": "2026-05-20T12:00:00Z"
        }
      ],
      "default_root_id": "root_...",
      "default_provider": "ollama",
      "default_model": "qwen2.5-coder",
      "default_agent_profile": "implementation",
      "default_tools_enabled": true,
      "default_workspace_mode": "in_place",
      "default_system_prompt": "Prefer small, reviewable patches.",
      "default_compact_tool_output": false,
      "created_at": "2026-05-20T12:00:00Z",
      "updated_at": "2026-05-20T12:00:00Z",
      "last_opened_at": "2026-05-20T12:30:00Z"
    }
  ]
}
```

### `POST /hecate/v1/projects`

Creates a project. `name` is required. Root `id` values are optional; Hecate
generates `root_...` IDs for roots that omit them. If `default_root_id` is
empty and at least one root is supplied, the first root becomes the default.
When supplied, `default_root_id` must match one of the supplied roots.
Context source `id` values are optional; Hecate generates `ctxsrc_...` IDs for
sources that omit them. Context sources are project source metadata:
workspace guidance discovered from roots, operator-added URLs, local paths,
notes, tickets, design files, source docs, or other external references.
Their `path` field is the source locator. For note-style sources, clients may
use a stable locator such as `note:research-goals` and store the note text in
`metadata.note`. Source metadata is visible to Project Assistant and context
inspectors, but Hecate does not fetch URLs, execute sources, or blindly inject
source bodies into prompts. Assignment prompt inclusion is still governed by
profile context-source policy and currently only includes bounded portable
workspace guidance (`kind: "workspace_instruction"`, `format: "agents_md"`).
Clients that render `path` as a link must validate the scheme first; Hecate
stores operator-provided locators as-is.
Project names are unique across the local project catalog, and root/workspace
paths are unique across all projects. Duplicate project names or root paths
return `409 conflict`.

Projects may be created without a workspace by omitting both `workspace_path`
and `roots`; this is the normal shape for planning, research, writing, ops, or
design projects that do not start from local files. For the common
one-workspace case, send `workspace_path` and optionally `workspace_kind`;
Hecate creates one active root and makes it the default root. For advanced
multi-root setup, send `roots` directly.
`workspace_path` cannot be combined with `roots` or an explicit
`default_root_id`.

```json
POST /hecate/v1/projects
{
  "name": "Hecate",
  "description": "Gateway and agent runtime",
  "workspace_path": "/Users/alice/src/hecate",
  "workspace_kind": "git",
  "context_sources": [
    {
      "kind": "url",
      "title": "Design brief",
      "path": "https://example.invalid/design",
      "enabled": true,
      "format": "url",
      "trust_label": "operator_source",
      "source_category": "operator_source",
      "metadata": { "note": "Reviewed by the operator." }
    },
    {
      "kind": "note",
      "title": "Research goals",
      "path": "note:research-goals",
      "enabled": true,
      "format": "text",
      "trust_label": "operator_source",
      "source_category": "operator_source",
      "metadata": { "note": "Prioritize sources with concrete user evidence." }
    }
  ],
  "default_provider": "ollama",
  "default_model": "qwen2.5-coder",
  "default_tools_enabled": true,
  "default_workspace_mode": "in_place"
}

→ 201
{
  "object": "project",
  "data": {
    "id": "proj_...",
    "name": "Hecate",
    "roots": [
      {
        "id": "root_...",
        "path": "/Users/alice/src/hecate",
        "kind": "git",
        "active": true
      }
    ],
    "default_root_id": "root_..."
  }
}
```

### `GET /hecate/v1/projects/{id}`

Returns one project or `404 not_found`.

### `PATCH /hecate/v1/projects/{id}`

Updates project metadata and defaults. Fields are optional. When `roots` is
present, it replaces the full root list; this remains available for
compatibility and admin-style bulk replacement. Operator root edits should use
the root-specific endpoints below so a single root create, update, or delete is
typed and auditable.
When `context_sources` is present, it replaces the full source-metadata list;
this remains available for compatibility and admin-style bulk replacement.
Operator source edits should use the source-specific endpoints below so a
single source create, update, or delete is typed and auditable.
When `default_root_id` is supplied, it must match the replacement root list or,
if `roots` is omitted, one of the existing roots. Renames and root replacements
preserve the same catalog uniqueness rules as creation: duplicate project names
or root paths return `409 conflict`.

```json
PATCH /hecate/v1/projects/proj_...
{
  "name": "Hecate runtime",
  "last_opened_at": "2026-05-20T12:45:00Z",
  "default_compact_tool_output": true
}
```

### `POST /hecate/v1/projects/{id}/roots`

Creates one project-root metadata record and returns the updated project. This
does not create a directory, create a Git worktree, scan the workspace, or
launch work. Use `POST /roots/worktrees` when Hecate should explicitly create a
Git worktree.

If `id` is omitted, the server generates one. Duplicate root IDs return
`409 conflict`; invalid root metadata returns `400 invalid_request`.

```json
POST /hecate/v1/projects/proj_.../roots
{
  "path": "/Users/alice/src/hecate",
  "kind": "git",
  "git_branch": "main",
  "active": true
}

→ 201
{
  "object": "project",
  "data": {
    "id": "proj_...",
    "roots": [
      {
        "id": "root_...",
        "path": "/Users/alice/src/hecate",
        "kind": "git",
        "git_branch": "main",
        "active": true
      }
    ],
    "default_root_id": "root_..."
  }
}
```

### `PATCH /hecate/v1/projects/{id}/roots/{root_id}`

Replaces one project-root metadata record and returns the updated project. The
path `root_id` is authoritative; any `id` in the request body is ignored for
the updated record. The payload is the full replacement metadata, so omitted
optional fields clear or default through the normal project-root validation
rules.

Missing projects or roots return `404 not_found`; invalid root metadata returns
`400 invalid_request`.

```json
PATCH /hecate/v1/projects/proj_.../roots/root_...
{
  "path": "/Users/alice/src/hecate/.worktrees/feature-a",
  "kind": "git_worktree",
  "git_branch": "feature/a",
  "active": true
}
```

### `DELETE /hecate/v1/projects/{id}/roots/{root_id}`

Deletes one project-root metadata record and returns the updated project so
clients can reconcile against the server state. Deleting a root only removes
the project metadata reference; it does not delete local folders, Git
worktrees, branches, files, work items, assignments, chats, tasks, or
external-agent runs. If the deleted root was the default root, the store
normalizes the updated project to the first remaining root or to no default
when no roots remain.

```json
DELETE /hecate/v1/projects/proj_.../roots/root_...
→ 200
{
  "object": "project",
  "data": {
    "id": "proj_...",
    "roots": []
  }
}
```

### `POST /hecate/v1/projects/{id}/context-sources`

Creates one context-source metadata record and returns the updated project. The
request is explicit metadata only: Hecate does not read file bodies, fetch URLs,
promote memory, inject prompt context, or change profile policy.

If `id` is omitted, the server generates one. Duplicate source IDs return
`409 conflict`; invalid source metadata returns `400 invalid_request`.

```json
POST /hecate/v1/projects/proj_.../context-sources
{
  "kind": "url",
  "title": "Launch brief",
  "path": "https://example.invalid/brief",
  "enabled": true,
  "format": "url",
  "trust_label": "operator_source",
  "source_category": "operator_source",
  "metadata": {
    "note": "Reviewed source for launch planning."
  }
}

→ 201
{
  "object": "project",
  "data": {
    "id": "proj_...",
    "context_sources": [
      {
        "id": "ctxsrc_...",
        "kind": "url",
        "title": "Launch brief",
        "path": "https://example.invalid/brief",
        "enabled": true
      }
    ]
  }
}
```

### `PATCH /hecate/v1/projects/{id}/context-sources/{source_id}`

Replaces one context-source metadata record and returns the updated project.
The path `source_id` is authoritative; any `id` in the request body is ignored
for the updated record. The payload is the full replacement metadata, so
omitted optional fields clear or default through the normal project-source
validation rules.

Missing projects or sources return `404 not_found`; invalid source metadata
returns `400 invalid_request`.

```json
PATCH /hecate/v1/projects/proj_.../context-sources/ctxsrc_...
{
  "kind": "doc",
  "title": "Architecture notes",
  "path": "docs/architecture.md",
  "enabled": true,
  "format": "markdown",
  "trust_label": "operator_source",
  "source_category": "operator_source"
}
```

### `DELETE /hecate/v1/projects/{id}/context-sources/{source_id}`

Deletes one context-source metadata record and returns the updated project so
clients can reconcile against the server state. Deleting a source only removes
the project metadata reference; it does not delete local files, remote content,
memory records, work items, assignments, chats, tasks, or external-agent runs.

```json
DELETE /hecate/v1/projects/proj_.../context-sources/ctxsrc_...
→ 200
{
  "object": "project",
  "data": {
    "id": "proj_...",
    "context_sources": []
  }
}
```

### `POST /hecate/v1/projects/{id}/roots/discover`

Refreshes Git metadata for active project roots and discovers linked Git
worktrees for the same repository. This is an explicit operator action. It
does not create branches, create worktrees, delete roots, change
`default_root_id`, or start work.

Discovered linked worktrees are appended to `roots` with:

- `kind: "git_worktree"`
- `git_branch` from `git worktree list --porcelain`
- `git_remote` from `origin` when configured
- `active: false` by default

Inactive discovered roots are visible to the operator but are not scanned for
workspace guidance or used for assignment launch until the operator enables
them or makes one the default root. Existing roots are matched by path; their
IDs and active state are preserved while branch/remote metadata is refreshed.

```json
POST /hecate/v1/projects/proj_.../roots/discover
→ 200
{
  "object": "project",
  "data": {
    "id": "proj_...",
    "roots": [
      {
        "id": "root_main",
        "path": "/Users/alice/src/cynic",
        "kind": "git",
        "git_remote": "git@github.com:owner/cynic.git",
        "git_branch": "main",
        "active": true
      },
      {
        "id": "root_...",
        "path": "/Users/alice/src/cynic/.claude/worktrees/fix-array-sort",
        "kind": "git_worktree",
        "git_remote": "git@github.com:owner/cynic.git",
        "git_branch": "fix-array-sort",
        "active": false
      }
    ],
    "default_root_id": "root_main"
  }
}
```

### `POST /hecate/v1/projects/{id}/roots/worktrees`

Creates a linked Git worktree from an existing project root and appends the
created checkout as a project root. This is an explicit operator action. V1
constrains the target path to a direct child of the selected base root's
`.worktrees/` directory so Hecate does not create sibling, nested, or arbitrary
filesystem workspaces through this endpoint.

Request fields:

- `branch` is required and becomes the new worktree branch.
- `base_root_id` selects the Git root to run `git worktree add` from; omitted
  means project default root, then first active root, then first root.
- `start_point` is optional and is passed to Git after the target path.
- `path` is optional. Relative paths resolve under the base root and must be a
  direct child of `.worktrees/`.
- `active` defaults to `true`.
- `set_default` makes the new root the project's `default_root_id`.

```json
POST /hecate/v1/projects/proj_.../roots/worktrees
{
  "base_root_id": "root_main",
  "branch": "feature/project-roots",
  "start_point": "origin/main",
  "active": true,
  "set_default": true
}

→ 201
{
  "object": "project",
  "data": {
    "id": "proj_...",
    "default_root_id": "root_...",
    "roots": [
      {
        "id": "root_...",
        "path": "/Users/alice/src/hecate/.worktrees/feature-project-roots",
        "kind": "git_worktree",
        "git_branch": "feature/project-roots",
        "active": true
      }
    ]
  }
}
```

### `POST /hecate/v1/projects/{id}/context-sources/discover`

Discovers workspace guidance metadata from active absolute project roots and
merges it into `context_sources`. Discovery is an explicit operator action: it
does not read discovered file bodies into prompts and does not change Hecate
policy, approvals, sandboxing, or profile settings.

V1 enables portable `AGENTS.md` sources as `kind=workspace_instruction`,
`format=agents_md`, and `trust_label=workspace_guidance`. Host-specific files
are labelled for visibility but remain metadata-only: `CLAUDE.md`,
`.claude/CLAUDE.md`, `GEMINI.md`, `.cursor/rules`, `.github/instructions`,
`.devin/rules`, `.windsurf/rules`, and `.gemini/commands`.

Discovery skips common vendor/build directories such as `.git`, `node_modules`,
`vendor`, `dist`, `build`, `.next`, `.turbo`, `.cache`, `target`, and
`coverage`. It also skips nested Git checkouts plus `.worktrees` and
`.claude/worktrees` under an active root. Linked worktrees should be added as
explicit project roots through root discovery; their guidance is discovered only
when that root is active.
Existing sources are matched by `(kind,path)` plus root metadata so operator
disabled state and source IDs are preserved on rediscovery.

```json
POST /hecate/v1/projects/proj_.../context-sources/discover
→ 200
{
  "object": "project",
  "data": {
    "id": "proj_...",
    "context_sources": [
      {
        "id": "ctxsrc_...",
        "kind": "workspace_instruction",
        "title": "AGENTS.md",
        "path": "AGENTS.md",
        "enabled": true,
        "format": "agents_md",
        "scope": "workspace",
        "trust_label": "workspace_guidance",
        "source_category": "workspace_guidance",
        "metadata": { "root_id": "root_..." }
      }
    ]
  }
}
```

### `DELETE /hecate/v1/projects/{id}`

Deletes the project catalog entry, its roots, and chat sessions scoped to that
project. It also deletes project memory entries, memory candidates, and project work-coordination
rows for that project. Project-scoped External Agent chats are deleted through
the normal chat-delete path, so Hecate asks the adapter to delete the native ACP
session where supported. This does not delete workspace files. Unprojected
chats and chats scoped to other projects stay untouched. Assignment links to
task/chat IDs are metadata only; the linked tasks or unprojected chat sessions
are not deleted through assignment cleanup.

The response reports the scoped records Hecate cleaned up:

```json
{
  "object": "project_delete",
  "data": {
    "project_id": "proj_...",
    "project_name": "Launch operations",
    "chat_sessions_deleted": 2,
    "project_work_rows_deleted": 8,
    "project_skills_deleted": 1,
    "memory_entries_deleted": 3,
    "memory_candidates_deleted": 4
  }
}
```

### Project Memory

Project memory is explicit operator-approved context. Hecate never writes these
entries automatically from chat, task, handoff, or generated output; generated
or external text must be reviewed and saved by the operator before it becomes
memory. Agents, chats, tasks, and project-work surfaces may create memory
candidates, but candidates are review records only. They do not participate in
context packets and do not create durable memory until the operator explicitly
promotes one.

Memory entry fields:

| Field         | Meaning                                                                |
| ------------- | ---------------------------------------------------------------------- |
| `id`          | Stable `mem_...` entry id.                                             |
| `scope`       | `"project"` in this release.                                           |
| `project_id`  | Owning project id.                                                     |
| `title`       | Operator-facing label.                                                 |
| `body`        | Markdown-compatible text stored on the structured record.              |
| `trust_label` | Context trust label such as `operator_memory` or `generated_summary`.  |
| `source_kind` | Provenance category such as `operator`, `handoff`, or `runtime_state`. |
| `source_id`   | Optional source artifact/chat/message/handoff id.                      |
| `enabled`     | Disabled entries remain saved but are excluded from active context.    |
| `created_at`  | UTC RFC3339Nano timestamp.                                             |
| `updated_at`  | UTC RFC3339Nano timestamp.                                             |

#### `GET /hecate/v1/projects/{id}/memory`

Lists enabled project memory entries by default. Pass
`include_disabled=true` to inspect disabled entries too.

```json
GET /hecate/v1/projects/proj_.../memory?include_disabled=true
→ 200
{
  "object": "project_memory",
  "data": [
    {
      "id": "mem_...",
      "scope": "project",
      "project_id": "proj_...",
      "title": "Commit style",
      "body": "Use conventional commits.",
      "trust_label": "operator_memory",
      "source_kind": "operator",
      "enabled": true,
      "created_at": "2026-06-04T10:00:00Z",
      "updated_at": "2026-06-04T10:00:00Z"
    }
  ]
}
```

#### `POST /hecate/v1/projects/{id}/memory`

Creates a project memory entry. `title` and `body` are required. `trust_label`
defaults to `operator_memory`, `source_kind` defaults to `operator`, and
`enabled` defaults to `true`. A duplicate generated entry id returns
`409 conflict`.

```json
POST /hecate/v1/projects/proj_.../memory
{
  "title": "Review posture",
  "body": "Keep generated summaries labelled.",
  "trust_label": "operator_memory",
  "source_kind": "operator"
}

→ 201
{
  "object": "project_memory_entry",
  "data": {
    "id": "mem_...",
    "scope": "project",
    "project_id": "proj_...",
    "title": "Review posture",
    "body": "Keep generated summaries labelled.",
    "trust_label": "operator_memory",
    "source_kind": "operator",
    "enabled": true,
    "created_at": "2026-06-04T10:00:00Z",
    "updated_at": "2026-06-04T10:00:00Z"
  }
}
```

#### `PATCH /hecate/v1/projects/{id}/memory/{memory_id}`

Updates title, body, trust/provenance fields, or enabled state. `id`, `scope`,
and `project_id` are immutable.

#### `DELETE /hecate/v1/projects/{id}/memory/{memory_id}`

Deletes the memory entry. Historical chat context packets that already
snapshotted the entry are not rewritten.

#### `GET /hecate/v1/projects/{id}/memory/candidates`

Lists pending memory candidates by default. Pass `include_resolved=true` to
include promoted and rejected candidates, or `status=pending|promoted|rejected`
to filter explicitly.

Candidates are review artifacts, not durable memory. Operators should inspect
the candidate body, suggested trust/source fields, and `source_refs` before
promotion. `source_refs` point back to evidence such as task runs, handoffs,
chat messages, or artifacts so the UI can show where the candidate came from
without injecting unapproved text into future context packets.

```json
GET /hecate/v1/projects/proj_.../memory/candidates
→ 200
{
  "object": "project_memory_candidates",
  "data": [
    {
      "id": "memcand_...",
      "project_id": "proj_...",
      "title": "Generated summary",
      "body": "Keep generated context labelled until reviewed.",
      "suggested_kind": "note",
      "suggested_trust_label": "generated_summary",
      "suggested_source_kind": "task_output",
      "suggested_source_id": "run_...",
      "source_refs": [{ "kind": "task_run", "id": "run_..." }],
      "status": "pending",
      "created_at": "2026-06-04T10:00:00Z",
      "updated_at": "2026-06-04T10:00:00Z"
    }
  ]
}
```

#### `POST /hecate/v1/projects/{id}/memory/candidates`

Creates a review candidate from a manual payload or a known source reference.
`title` and `body` are required. Candidates default to
`suggested_trust_label="generated_summary"` and
`suggested_source_kind="generated"` so generated/external text stays
lower-trust unless the operator changes it during promotion.

#### `POST /hecate/v1/projects/{id}/memory/candidates/{candidate_id}/promote`

Promotes a pending candidate into a durable project memory entry. The request
may include edited `title`, `body`, `trust_label`, `source_kind`, `source_id`,
and `enabled`; omitted fields use the candidate's suggested values. Promotion
sets the candidate status to `promoted` and records `promoted_memory_id`.
Only the created memory entry participates in future project-memory context
selection; the candidate remains a provenance/audit record.
Promoting an already-promoted candidate is idempotent when the linked
`promoted_memory_id` still exists and returns the existing promoted candidate.
Promoting a rejected candidate returns `409 conflict`.

The response is `{ "object": "project_memory_candidate", "data": ... }`. The
created memory entry is returned by the normal project memory list/get flows.

#### `POST /hecate/v1/projects/{id}/memory/candidates/{candidate_id}/reject`

Rejects or dismisses a pending candidate without creating durable memory. The
optional request body is `{ "reason": "..." }`. Rejecting an already resolved
candidate returns `409 conflict`.

### Project Work Coordination

Project work coordination is the durable substrate for future project-team
orchestration. It records project-scoped agent roles, work items, assignment
metadata, and collaboration artifacts. It does not add a new execution runtime:
existing Tasks and Chats remain the execution surfaces. Assignment creation
records intended or already-linked execution metadata; the separate native
assignment start endpoint can create and start a Hecate-owned `agent_loop` task
for `driver_kind="hecate_task"` assignments.
Create requests that supply an existing project-scoped ID return `409 conflict`
instead of overwriting the existing record.

Role list responses merge built-in roles with project custom roles. Built-ins
are listable but immutable and are not seeded as duplicate project rows. The
built-in role ids and default profile mappings are:

| Role id              | Default driver | Default profile           |
| -------------------- | -------------- | ------------------------- |
| `product_manager`    | `hecate_task`  | `planning`                |
| `architect`          | `hecate_task`  | `architecture`            |
| `software_developer` | `hecate_task`  | `implementation`          |
| `frontend_engineer`  | `hecate_task`  | `frontend_implementation` |
| `designer`           | `hecate_task`  | `design_review`           |
| `sre`                | `hecate_task`  | `reliability_ops`         |
| `tech_writer`        | `hecate_task`  | `documentation`           |
| `reviewer_qa`        | `hecate_task`  | `review_qa`               |

Supported work-item statuses are `backlog`, `ready`, `running`, `review`,
`blocked`, `done`, and `cancelled`. Supported assignment statuses are `queued`,
`running`, `awaiting_approval`, `completed`, `failed`, and `cancelled`.
Supported assignment driver kinds are `hecate_task` and `external_agent`.
Assignment start dispatches `hecate_task` assignments through the native task
runtime and prepares `external_agent` assignments as supervised External Agent
chat sessions.
Work items and assignments may carry `root_id` to select a concrete project
root. Launch workspace resolution uses assignment `root_id`, then work-item
`root_id`, then project `default_root_id`, then the first active project root.
Work items may also carry `reviewer_role_ids`; these identify roles that are
appropriate targets for review handoffs and do not grant permissions, enforce
policy, or auto-dispatch review work.
Supported collaboration artifact kinds are `brief`, `handoff`, `review`,
`decision_note`, and `evidence_link`.
Supported structured handoff statuses are `pending`, `accepted`, `superseded`,
and `dismissed`.

Assignment responses are projected from linked canonical task/run state when
`execution_ref.kind="task_run"` and `execution_ref.task_id` /
`execution_ref.run_id` point at a Hecate task run. If
`execution_ref.task_id` is present and `execution_ref.run_id` is empty, Hecate
uses that task's `latest_run_id` when available. The stored assignment row is
coordination metadata; task/run reads do not mutate the task, run, or
assignment rows. Run statuses map directly into assignment statuses:

| Task/run status     | Project assignment status |
| ------------------- | ------------------------- |
| `queued`            | `queued`                  |
| `running`           | `running`                 |
| `awaiting_approval` | `awaiting_approval`       |
| `completed`         | `completed`               |
| `failed`            | `failed`                  |
| `cancelled`         | `cancelled`               |

If the linked task/run is missing, Hecate keeps the stored assignment status
and marks the nested `execution` summary as `missing`. If a linked run is older
than a newer explicit terminal assignment update, the assignment keeps that
explicit project-work terminal status instead of being overwritten by stale
runtime state. The `execution` summary may include `task_status`, `run_status`,
projected `status`, pending approval count, step/approval/artifact counts,
model/provider, last error, run timestamps, and trace ID.

External Agent assignments use the linked project-scoped chat session as their
canonical execution state when `execution_ref.kind="chat_session"` and
`execution_ref.chat_session_id` points at a Chat in the same project.
Assignment reads project the latest assistant-message status first, then the
session status, so a stale session summary cannot keep a completed assistant
turn in the active bucket. Chat-backed projections update
`execution_ref.status`, `execution_ref.message_id`, `execution_ref.trace_id`,
and assignment `completed_at` when available; they do not include a task-run
`execution` summary. Missing chats, chat-store lookup errors, or cross-project
chat links are treated as missing execution refs instead of exposing foreign
chat metadata.

When an External Agent chat turn reaches `completed`, `failed`, or
`cancelled`, Hecate also performs a best-effort reconciliation pass that
updates linked project assignment rows. This makes the durable assignment
status catch up with the chat outcome without making the chat response fail if
project metadata reconciliation is temporarily unavailable.

Assignments include `execution_ref`, the canonical compact execution link for
UI clients and API callers. It prefers projected execution data when available
and falls back to stored links, with `kind` set to `task_run`, `chat_session`,
or `context_snapshot`. The richer `execution` summary remains for detail views;
raw top-level assignment link fields are not part of the alpha contract.

Work-item list and detail responses apply the same conservative rollup over
projected assignment statuses: any active linked assignment (`queued`,
`running`, or `awaiting_approval`) makes the work item `running`; all
assignments `completed` makes it `done`; all assignments `cancelled` makes it
`cancelled`; any failed assignment, or any cancelled assignment mixed with a
non-cancelled assignment, makes it `blocked`. Otherwise the stored work-item
status is returned.

The Projects UI also exposes an operator closeout action for selected work
items. The read-only
`GET /hecate/v1/projects/{id}/work-items/{work_item_id}/readiness` contract is
the shared closeout authority for Project Operations, selected-work detail, and
`PATCH` requests that set `status="done"`: it derives blockers from
assignments, completion evidence, handoffs, and review follow-up without
mutating project state. The guided action still uses the normal work-item
`PATCH` path only after the operator clicks Mark done. Hecate does not
auto-mutate stored work-item status from review verdicts, handoffs, or
assignment rollups without an explicit operator update. Updates that do not mark
the work item done continue to use the normal edit flow while closeout blockers
remain.

#### `GET /hecate/v1/projects/{id}/activity`

Returns a read-only project activity inbox for the operator cockpit. The
response is bounded and deterministic: Hecate composes existing project work
items, assignments, projected task/run execution summaries, linked chat/task
identifiers, and recent collaboration artifact signals without mutating any
project-work or task rows.

Activity Inbox stays focused on live assignment buckets. The compact
Needs Attention header uses the server-owned project health endpoint below so
clients do not rederive a second attention cascade from activity, memory,
skills, and project defaults.

The top-level envelope follows the Hecate-native convention:

Activity items are exposed through `data.recent` and the `data.buckets`
collections. There is no top-level `data.items` list.

```json
{
  "object": "project_activity",
  "data": {
    "project_id": "proj_...",
    "summary": {
      "work_item_count": 3,
      "assignment_count": 5,
      "active_count": 1,
      "blocked_count": 2,
      "completed_count": 2,
      "recent_count": 1
    },
    "buckets": {
      "blocked": [
        {
          "id": "asgn_...",
          "project_id": "proj_...",
          "work_item": {
            "id": "work_...",
            "title": "Backend substrate",
            "status": "running",
            "priority": "high"
          },
          "assignment": {
            "id": "asgn_...",
            "project_id": "proj_...",
            "work_item_id": "work_...",
            "role_id": "software_developer",
            "driver_kind": "hecate_task",
            "status": "awaiting_approval",
            "task_id": "task_...",
            "run_id": "run_...",
            "execution": {
              "task_id": "task_...",
              "run_id": "run_...",
              "task_status": "running",
              "run_status": "awaiting_approval",
              "status": "awaiting_approval",
              "pending_approval_count": 1
            },
            "created_at": "2026-06-03T12:00:00Z",
            "updated_at": "2026-06-03T12:01:00Z"
          },
          "role": {
            "id": "software_developer",
            "project_id": "proj_...",
            "name": "Software Developer",
            "built_in": true
          },
          "status": "awaiting_approval",
          "blocking_signal": "awaiting_approval",
          "status_summary": "1 approval pending",
          "linked_task_id": "task_...",
          "linked_run_id": "run_...",
          "linked_chat_id": "chat_...",
          "linked_chat": {
            "id": "chat_...",
            "title": "Backend substrate - External implementer",
            "agent_id": "codex",
            "driver_kind": "acp",
            "native_session_id": "native_...",
            "status": "running",
            "latest_message_id": "msg_...",
            "latest_role": "assistant",
            "latest_status": "completed",
            "message_count": 2,
            "updated_at": "2026-06-03T12:02:00Z"
          },
          "artifact_summary": {
            "count": 1,
            "latest_kind": "handoff",
            "latest_title": "Backend handoff",
            "latest_at": "2026-06-03T12:03:00Z",
            "assignment_id": "asgn_..."
          },
          "handoff_summary": {
            "count": 1,
            "pending_count": 1,
            "accepted_count": 0,
            "latest_status": "pending",
            "latest_title": "QA handoff",
            "latest_at": "2026-06-03T12:04:00Z",
            "assignment_id": "asgn_...",
            "target_role_id": "reviewer_qa"
          },
          "recent_artifacts": [
            {
              "id": "art_...",
              "project_id": "proj_...",
              "work_item_id": "work_...",
              "assignment_id": "asgn_...",
              "kind": "handoff",
              "title": "Backend handoff",
              "body": "Ready for review.",
              "created_at": "2026-06-03T12:03:00Z",
              "updated_at": "2026-06-03T12:03:00Z"
            }
          ],
          "recent_handoffs": [
            {
              "id": "handoff_...",
              "project_id": "proj_...",
              "work_item_id": "work_...",
              "source_assignment_id": "asgn_...",
              "source_chat_session_id": "chat_...",
              "source_message_id": "msg_...",
              "target_role_id": "reviewer_qa",
              "title": "QA handoff",
              "summary": "Implementation is ready for review.",
              "recommended_next_action": "Create a queued QA assignment.",
              "status": "pending",
              "provenance_kind": "agent_draft",
              "trust_label": "operator_reviewed",
              "created_at": "2026-06-03T12:04:00Z",
              "updated_at": "2026-06-03T12:04:00Z",
              "status_changed_at": "2026-06-03T12:04:00Z"
            }
          ],
          "updated_at": "2026-06-03T12:04:00Z"
        }
      ],
      "active": [],
      "completed": [],
      "recent": []
    },
    "recent": []
  }
}
```

`blocking_signal` is the compact V1 operator signal. Known values are
`awaiting_approval`, `failed`, `cancelled`, `not_started`, `running`,
`completed`, and `stale_unknown`. `not_started` means a queued assignment has
no linked task, run, or chat identifiers. `stale_unknown` covers missing linked
task/run/chat records, run-only links without enough task context, unknown
status values, wrong-project linked chats, and transient linked-chat lookup
failures.
Rows are sorted by most recent assignment/work/artifact update, then assignment
ID. Each bucket is capped at 20 rows; `recent` mirrors
`buckets.recent`. The example above leaves the mirrored recent arrays empty for
brevity; real responses include the same item shape there when `recent_count`
is non-zero. `artifact_summary.assignment_id` is present only when the latest
summarized artifact is assignment-scoped; work-item-level artifacts omit it.
`linked_chat` is present when an assignment points at a Chat or External Agent
session. It is a compact activity projection, not a full chat transcript:
clients use it for session status, last-message status/error,
adapter/session identity, and missing linked-session diagnostics, then open the
chat endpoint for full transcript state. When a handoff is created from an
assignment, clients may copy the assignment, run, chat, message, and context
refs into the handoff source fields so later follow-up assignments preserve
provenance without auto-dispatching work.
An `idle` linked chat with a running/queued external assignment is a prepared
session waiting for the operator's first or next turn, not stale execution.
`handoff_summary` and `recent_handoffs` are activity projections over
structured handoff records attached to the same work item and, when present,
the same source or target assignment. Handoffs that are not assignment-linked
are still available from the handoff list/detail endpoints; V1 does not create
standalone activity rows for them.

#### `GET /hecate/v1/projects/{id}/health`

Returns a read-only Project attention summary for the selected project. The
endpoint derives compact operator rows from existing project state: missing
provider/model defaults, missing active roots, missing agent profile refs,
project skill registry conflicts or unresolved/disabled referenced skills,
pending handoffs, review artifacts that need a follow-up path, stale or missing
linked assignment execution, failed/cancelled External Agent assignments,
empty memory/context posture, and pending memory candidates.

This endpoint does not create tasks, runs, chats, assignments, handoffs,
proposals, memory entries, or memory candidates. It is a bounded projection over
durable project state; durable changes still go through the typed project,
memory, work, role, skill, and Project Assistant apply endpoints.

The five-item cap is applied after the server's attention derivation order. The
summary reports returned, available, omitted, and limit counts so clients can
show when lower-priority attention rows are hidden.

```json
{
  "object": "project_health",
  "data": {
    "project_id": "proj_...",
    "generated_at": "2026-06-20T12:00:00Z",
    "summary": {
      "attention_count": 2,
      "available_attention_count": 2,
      "omitted_attention_count": 0,
      "attention_limit": 5,
      "missing_defaults": true,
      "missing_project_root": false,
      "enabled_memory_count": 0,
      "saved_memory_count": 1,
      "enabled_context_source_count": 0,
      "pending_memory_candidate_count": 1,
      "promoted_memory_candidate_count": 0,
      "rejected_memory_candidate_count": 0,
      "pending_handoff_count": 1,
      "accepted_handoff_count": 0,
      "superseded_handoff_count": 0,
      "dismissed_handoff_count": 0,
      "review_follow_up_count": 0,
      "blocked_review_count": 0,
      "changes_requested_review_count": 0,
      "stale_or_unknown_assignment_count": 0
    },
    "attention": [
      {
        "id": "proj_...:defaults",
        "project_id": "proj_...",
        "title": "Provider/model defaults missing",
        "detail": "Native project starts and assignment chats need a default provider and model.",
        "status": "awaiting_approval",
        "action": {
          "type": "open_project_settings",
          "project_id": "proj_..."
        }
      },
      {
        "id": "memcand_...:memory-candidate",
        "project_id": "proj_...",
        "title": "Memory candidate pending review",
        "detail": "Remember project convention - generated_summary",
        "status": "awaiting_approval",
        "action": {
          "type": "review_memory_candidate",
          "project_id": "proj_...",
          "candidate_id": "memcand_..."
        },
        "candidate_id": "memcand_..."
      }
    ]
  }
}
```

Attention rows route to existing surfaces through a typed `action` object.
Supported action types are shared with Project Operations where they overlap:
`open_project_settings`, `open_memory_review`, `open_profiles`, `open_roles`,
`open_skills`, `open_work_item`, `open_task`, `open_activity_bucket`, and
`review_memory_candidate`. The top-level `work_item_id`, `task_id`, `run_id`,
`chat_id`, `candidate_id`, and `bucket` fields remain row metadata for compact
buttons and labels; clients should use `action` as the authority for row
activation. Clients should not mutate project state directly from a row; they
open the referenced surface and use that surface's explicit typed mutation.

#### `GET /hecate/v1/projects/{id}/setup-readiness`

Returns a read-only Project setup readiness projection for the selected
project. The endpoint decides whether the project should show first-run
onboarding, whether setup has started, and whether the empty project is ready
for first-work creation. It derives those flags from existing project roots,
defaults, context sources, custom roles, skills metadata, project memory, memory
candidates, and work items.

The endpoint does not create tasks, runs, chats, proposals, roles, work items,
memory entries, memory candidates, or skills. Checklist actions route the
operator to existing typed surfaces: Project Settings, Project Assistant setup,
or explicit work-item creation.

```json
{
  "object": "project_setup_readiness",
  "data": {
    "project_id": "proj_...",
    "generated_at": "2026-06-20T12:00:00Z",
    "show_onboarding": true,
    "setup_started": false,
    "first_work_ready": false,
    "summary": {
      "work_item_count": 0,
      "role_count": 0,
      "skill_count": 0,
      "enabled_context_source_count": 0,
      "saved_memory_count": 0,
      "pending_memory_candidate_count": 0,
      "has_purpose": true,
      "has_active_root": true,
      "missing_defaults": false
    },
    "primary_action": {
      "type": "bootstrap_project",
      "project_id": "proj_...",
      "label": "Set up project"
    },
    "checks": [
      {
        "id": "workspace_source",
        "label": "Workspace source",
        "detail": "/Users/alice/src/hecate",
        "status": "ready"
      },
      {
        "id": "first_work_item",
        "label": "First work item",
        "detail": "Create the first reviewable task after setup.",
        "status": "todo",
        "action": {
          "type": "create_work_item",
          "project_id": "proj_...",
          "label": "Create work"
        }
      }
    ]
  }
}
```

Known setup action types are `bootstrap_project`, `create_work_item`, and
`open_project_settings`. Clients should dispatch on `action.type` and validate
`project_id` before routing the operator. A stale or unsupported action should
surface a refresh error rather than performing a guessed fallback.

#### `GET /hecate/v1/projects/{id}/operations/brief`

Returns a read-only Project Operations brief for the selected project. The
brief is an operator-facing triage layer over existing project state: launch
defaults, the project activity buckets, work items without assignments, pending
handoffs, pending memory candidates, review artifacts that need follow-up,
missing completion evidence, and work items ready for closeout. When none of
those operations exists, the server may return a low-priority `open_latest_work`
orientation item so the operator can reopen the most recently updated work item
without clients deriving their own fallback cascade. It is bounded and
deterministic; it does not create tasks, runs, chats, assignments, handoffs,
proposals, memory entries, or memory candidates, and it never starts queued
work.
The eight-item cap is applied after sorting by priority, explicit operation
kind urgency, recency, and stable ID, so truncation is part of the operator
priority policy. The summary reports the returned item count, available item
count before the cap, omitted item count, and current item limit so clients can
show when lower-priority operations are hidden.

Each item has a `kind` that explains why it appears and an `action` that is the
typed client routing contract. Clients should dispatch on `action.type`, not on
`kind` or `target.surface`. `target` remains descriptive context for existing
Hecate surfaces (`project_settings`, `work`, or `memory`) and for summaries
copied from the existing project APIs.

Known action types are:

- `open_project_settings`
- `open_work_item`
- `open_assignment_preflight`
- `open_memory_review`
- `draft_project_proposal`

Actions do not directly mutate project state. `open_assignment_preflight` opens
the existing assignment launch preflight before any start. `draft_project_proposal`
uses `action.request` and the normal Project Assistant draft/propose/apply flow,
where the operator reviews the typed proposal before any durable mutation.

```json
{
  "object": "project_operations_brief",
  "data": {
    "project_id": "proj_...",
    "generated_at": "2026-06-20T12:00:00Z",
    "summary": {
      "item_count": 2,
      "available_item_count": 2,
      "omitted_item_count": 0,
      "item_limit": 8,
      "high_count": 1,
      "medium_count": 1,
      "low_count": 0,
      "pending_memory_candidate_count": 3,
      "pending_handoff_count": 1
    },
    "items": [
      {
        "id": "approve_assignment:proj_...:asgn_...",
        "kind": "approve_assignment",
        "priority": "high",
        "title": "Review pending approval: Backend substrate",
        "detail": "1 approval pending",
        "action_label": "Open approval",
        "status": "awaiting_approval",
        "target": {
          "surface": "work",
          "project_id": "proj_...",
          "work_item_id": "work_...",
          "assignment_id": "asgn_...",
          "activity_bucket": "blocked"
        },
        "action": {
          "type": "open_work_item",
          "project_id": "proj_...",
          "work_item_id": "work_...",
          "assignment_id": "asgn_...",
          "activity_bucket": "blocked"
        }
      },
      {
        "id": "review_memory_candidates:proj_...",
        "kind": "review_memory_candidates",
        "priority": "medium",
        "title": "Review memory candidates",
        "detail": "Promote, edit, or reject pending memory candidates before they become stale.",
        "action_label": "Review memory",
        "status": "pending",
        "target": {
          "surface": "memory",
          "project_id": "proj_..."
        },
        "action": {
          "type": "open_memory_review",
          "project_id": "proj_..."
        },
        "metadata": {
          "candidate_count": "3"
        }
      }
    ]
  }
}
```

The response is a convenience contract for operator workspaces, not a new
durable planner. Project Activity remains the source for live assignment
buckets, Project Assistant remains the proposal author, and assignment start
still goes through preflight and explicit operator confirmation.
Items are capped after sorting by priority, then an explicit operation-kind
urgency rank, then recency, then id. This keeps blocked or waiting work ahead of
setup gaps, handoffs ahead of memory triage, and active/completion hygiene below
operator-gated work when the brief has more candidates than it can show.

#### `GET /hecate/v1/projects/{id}/skills`

Lists persisted project skills. These are project metadata records, not loaded
runtime instructions. Bodies are never returned.

```json
{
  "object": "project_skills",
  "data": [
    {
      "id": "backend",
      "project_id": "proj_...",
      "title": "Backend",
      "description": "Build backend changes.",
      "path": ".hecate/skills/backend/SKILL.md",
      "root_id": "root_...",
      "format": "skill_md",
      "suggested_tools": ["git.diff", "file.read"],
      "required_permissions": {
        "tools": true,
        "writes": false,
        "network": false
      },
      "enabled": true,
      "status": "available",
      "trust_label": "workspace_skill",
      "source_context_source_ids": ["ctx_agents"],
      "warnings": [],
      "discovered_at": "2026-06-10T12:00:00Z",
      "created_at": "2026-06-10T12:00:00Z",
      "updated_at": "2026-06-10T12:00:00Z"
    }
  ]
}
```

#### `POST /hecate/v1/projects/{id}/skills/discover`

Refreshes the project skills registry from active absolute project roots.
Discovery scans:

- `.agents/skills/*/SKILL.md`
- `.hecate/skills/*/SKILL.md`
- local skill roots explicitly linked from enabled `AGENTS.md` or `CLAUDE.md`
  context sources.

Discovery ignores nested worktree containers such as `.worktrees` and
`.claude/worktrees` when reading guidance-linked skill roots. Add a linked
worktree as its own project root when the operator wants it represented.

Only safe metadata is parsed from bounded `SKILL.md` files: frontmatter
`name`/`title`, `description`, optional `hecate.suggested_tools`, optional
`hecate.required_permissions.{tools,writes,network}`, then H1/title fallback and
directory id. Suggested-tool lists are normalized, de-duplicated, capped before
storage/API exposure, and summarized in operator-facing text. Duplicate ids
become `status: "conflict"` records with warnings. Previously persisted skills
not found in the latest discovery become `status: "missing"`. Operator edits to
`enabled`, `title`, `description`, and `trust_label` are preserved across
rediscovery.

Skill capability metadata is advisory. Skills do not grant tools, writes,
network access, approval bypasses, script execution, or memory writes. During
assignment launch planning, Hecate compares resolved project skills with the
resolved agent profile and surfaces mismatches as launch-readiness and
context-inspector warnings for operator review.

#### `PATCH /hecate/v1/projects/{id}/skills/{skill_id}`

Updates operator-owned skill metadata:

```json
{
  "enabled": false,
  "title": "Backend Lead",
  "description": "Operator-curated backend posture.",
  "trust_label": "workspace_skill"
}
```

Returns `{ "object": "project_skill", "data": { ... } }`.

Skill status values are `available`, `missing`, `invalid`, and `conflict`.

#### `GET /hecate/v1/projects/{id}/roles`

Lists built-in roles plus custom roles for the project.

```json
{
  "object": "project_roles",
  "data": [
    {
      "id": "architect",
      "project_id": "proj_...",
      "name": "Architect",
      "description": "Owns technical direction, boundaries, and system trade-offs.",
      "default_driver_kind": "hecate_task",
      "default_agent_profile": "architecture",
      "built_in": true
    },
    {
      "id": "role_...",
      "project_id": "proj_...",
      "name": "Release captain",
      "description": "Coordinates release work.",
      "instructions": "Keep release notes current.",
      "default_driver_kind": "hecate_task",
      "default_provider": "ollama",
      "default_model": "ministral-3:latest",
      "default_agent_profile": "implementation",
      "skill_ids": ["release"],
      "built_in": false,
      "created_at": "2026-06-03T12:00:00Z",
      "updated_at": "2026-06-03T12:00:00Z"
    }
  ]
}
```

#### `POST /hecate/v1/projects/{id}/roles`

Creates a custom role. `name` is required. `id` is optional; Hecate generates a
`role_...` ID when omitted. Built-in role IDs cannot be created, updated, or
deleted as custom roles. Role defaults are execution hints: `default_driver_kind`
can seed new assignment driver kind, and `default_provider`, `default_model`,
and `default_agent_profile` can seed native task/chat launches before project
defaults are used. Provider, model, and profile hints are stored as supplied
and are not validated against the live provider catalog when the role is saved;
stale or unroutable values fail later when an assignment or chat launch uses
them. `skill_ids` are references to the project skills registry. Missing,
disabled, or conflicting skills warn at assignment start; they do not block the
assignment.

```json
{
  "name": "Release captain",
  "description": "Coordinates release work.",
  "instructions": "Keep release notes current.",
  "default_driver_kind": "hecate_task",
  "default_provider": "ollama",
  "default_model": "ministral-3:latest",
  "default_agent_profile": "implementation",
  "skill_ids": ["release"]
}
```

Returns `{ "object": "project_role", "data": { ... } }`.

#### `PATCH /hecate/v1/projects/{id}/roles/{role_id}`

Updates a custom role's `name`, `description`, `instructions`, `skill_ids`, or
role default execution hints. Built-in roles return `409 conflict`.

#### `DELETE /hecate/v1/projects/{id}/roles/{role_id}`

Deletes a custom role. Built-in roles return `409 conflict`.

#### `GET /hecate/v1/projects/{id}/work-items`

Lists work items for the project. Each item includes projected assignment
summaries in `assignments` when assignments exist, so callers can render list
status/count signals without issuing one assignment request per work item.
The nested assignment objects use the same shape as
`GET /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments`.

#### `POST /hecate/v1/projects/{id}/work-items`

Creates a project-scoped work item. `title` is required. `status` defaults to
`backlog`; `priority` defaults to `normal` and accepts `low`, `normal`, `high`,
or `urgent`.

```json
{
  "title": "Backend substrate",
  "brief": "Persist coordination metadata only.",
  "status": "ready",
  "priority": "high",
  "owner_role_id": "software_developer",
  "root_id": "root_...",
  "reviewer_role_ids": ["architect", "reviewer_qa"]
}
```

Returns:

```json
{
  "object": "project_work_item",
  "data": {
    "id": "work_...",
    "project_id": "proj_...",
    "title": "Backend substrate",
    "brief": "Persist coordination metadata only.",
    "status": "ready",
    "priority": "high",
    "owner_role_id": "software_developer",
    "root_id": "root_...",
    "reviewer_role_ids": ["architect", "reviewer_qa"],
    "created_at": "2026-06-03T12:00:00Z",
    "updated_at": "2026-06-03T12:00:00Z"
  }
}
```

#### `GET /hecate/v1/projects/{id}/work-items/{work_item_id}`

Returns one work item or `404 not_found`.

#### `GET /hecate/v1/projects/{id}/work-items/{work_item_id}/readiness`

Returns the read-only closeout readiness contract for one work item. This is
the same server-side decision path used by Project Operations when it surfaces
review follow-up, missing completion evidence, or closeout-ready items.

The response does not create, update, or delete project records. `ready=true`
means the selected-work detail may enable the guided Mark done action; the
operator still applies the durable `status="done"` mutation through
`PATCH /hecate/v1/projects/{id}/work-items/{work_item_id}`.

```json
{
  "object": "project_work_item_readiness",
  "data": {
    "project_id": "proj_...",
    "work_item_id": "work_...",
    "ready": false,
    "status": "blocked",
    "title": "Closeout is blocked",
    "detail": "Resolve the listed assignment, evidence, handoff, or review follow-up items before marking this work done.",
    "blockers": [
      "1 completed assignment is missing evidence",
      "Review follow-up \"Architecture review\" is not triaged"
    ],
    "warnings": [],
    "assignment_count": 1,
    "completed_assignments": 1,
    "review_follow_up_count": 1,
    "review_follow_ups": [
      {
        "artifact_id": "artifact_...",
        "title": "Architecture review",
        "status": "needs_path",
        "blocker": "Review follow-up \"Architecture review\" is not triaged",
        "reviewed_assignment_id": "asgn_...",
        "review_verdict": "changes_requested",
        "review_risk": "medium"
      }
    ],
    "missing_evidence_assignment_ids": ["asgn_..."]
  }
}
```

`review_follow_ups` lists review artifacts that still need an explicit
follow-up path. Clients should use this server field for closeout notices and
Project Assistant draft shortcuts instead of deriving readiness from raw
artifact fields. `review_follow_up_artifact_ids` may also be present for compact
artifact-id consumers.

#### `PATCH /hecate/v1/projects/{id}/work-items/{work_item_id}`

Updates `title`, `brief`, `status`, `priority`, `owner_role_id`, `root_id`, or
`reviewer_role_ids`. An empty `root_id` clears the work-item root override.
When `status` is set to `done`, the server validates the same closeout readiness
contract returned by
`GET /hecate/v1/projects/{id}/work-items/{work_item_id}/readiness`. If blockers
remain, the update returns `409 conflict` with a `readiness` object in the error
body. Other work-item edits are not blocked by closeout readiness.

#### `DELETE /hecate/v1/projects/{id}/work-items/{work_item_id}`

Deletes the work item and its assignments and collaboration artifacts.

#### `GET /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments`

Lists assignment metadata for a work item.

#### `POST /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments`

Creates an assignment metadata record. `role_id` is required. `driver_kind`
defaults to `hecate_task`. Optional execution links are stored under
`execution_ref` only.

```json
{
  "role_id": "software_developer",
  "root_id": "root_...",
  "driver_kind": "hecate_task",
  "execution_ref": {
    "kind": "task_run",
    "task_id": "task_...",
    "run_id": "run_...",
    "context_snapshot_id": "ctx_..."
  }
}
```

Returns:

```json
{
  "object": "project_assignment",
  "data": {
    "id": "asgn_...",
    "project_id": "proj_...",
    "work_item_id": "work_...",
    "role_id": "software_developer",
    "root_id": "root_...",
    "driver_kind": "hecate_task",
    "status": "queued",
    "execution_ref": {
      "kind": "task_run",
      "task_id": "task_...",
      "run_id": "run_...",
      "status": "queued"
    },
    "execution": {
      "task_id": "task_...",
      "run_id": "run_...",
      "task_status": "queued",
      "run_status": "queued",
      "status": "queued"
    },
    "created_at": "2026-06-03T12:00:00Z",
    "updated_at": "2026-06-03T12:00:00Z"
  }
}
```

#### `PATCH /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}`

Updates assignment status, role, `root_id`, link fields, `started_at`, or
`completed_at`. An empty `root_id` clears the assignment root override. It does
not mutate or start the linked Task or Chat.

#### `DELETE /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}`

Deletes the assignment metadata record and collaboration artifacts attached to
that assignment. It does not delete or cancel a linked Task, Run, Chat session,
or external-agent execution.

#### `GET /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/context`

Returns the best available context packet for the assignment. Hecate resolves
linked Task/Run packets first, then falls back to the assignment-stored packet
created by an External Agent start, then to a linked Chat `chat_session_id` +
`message_id` from `execution_ref` when present. Unstarted assignments, rows
without a stored packet or execution link, or older runs that predate snapshots return
`404 not_found`. The Projects cockpit uses this endpoint for the assignment
`Inspect context` action so operators can inspect the resolved profile, launch
instructions, memory, project sources, work context, runtime refs, and skipped
or inspect-only items without reopening the raw task or chat transcript.

#### `GET /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/launch-readiness`

Returns a typed, read-only assignment launch readiness projection. The endpoint
does not create a Task, Run, Chat session, memory entry, artifact, or assignment
update. Hecate verifies the same launch shape used by preflight/start: project,
work item, assignment, and role identity; queued/startable status; stored driver
support; active execution; workspace/root resolution; profile and skills
resolution; native provider/model readiness; and External Agent adapter/options
resolution.

The response envelope is:

```json
{
  "object": "project_assignment_launch_readiness",
  "data": {
    "project_id": "proj_123",
    "work_item_id": "work_123",
    "assignment_id": "asgn_123",
    "ready": false,
    "status": "blocked",
    "title": "Launch is blocked",
    "detail": "Resolve the listed launch blockers before starting or preparing this assignment.",
    "blockers": ["No routable provider reports model \"dogfood-model\"."],
    "warnings": [],
    "driver_kind": "hecate_task",
    "workspace": "/Users/alice/dev/hecate",
    "root_id": "root_main",
    "provider": "",
    "model": "dogfood-model",
    "execution_profile": "implementation",
    "profile_posture": {
      "id": "implementation",
      "name": "Implementation",
      "source": "role_default",
      "tools_enabled": true,
      "writes_allowed": true,
      "network_allowed": false,
      "approval_policy": "require",
      "project_memory_policy": "include",
      "context_source_policy": "include_enabled"
    },
    "model_readiness": {
      "ready": false,
      "status": "blocked",
      "reason": "model_not_discovered",
      "message": "No routable provider reports model \"dogfood-model\".",
      "operator_action": "Pick one of the discovered models."
    }
  }
}
```

Native Hecate Task assignments may include `model_readiness`, using the same
reason and repair vocabulary as `metadata.readiness` on `/v1/models`. External
Agent assignments include `external_agent_id`, `external_agent`, and
`session_title` when the adapter/options resolve. Assignments with a resolved
profile include `profile_posture`, a read-only summary of the selected profile's
tools, writes, network, approval, memory, and context-source posture. `ready=false`
is the UI gate for `Start assignment`, `Prepare chat`, and `Start from handoff`;
operators must still confirm the separate start mutation after reviewing preflight.

#### `GET /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/preflight`

Returns a launch context packet for a queued assignment without creating or
mutating a Task, Run, Chat session, memory entry, artifact, or assignment
record. Hecate performs the same launch-shape validation used by assignment
start: project/work/assignment/role lookup, stored driver support, active
execution checks, workspace resolution, resolved profile, provider/model hints
for native assignments, External Agent adapter/options for external-agent
assignments, skill metadata resolution, and prompt-context policy metadata.
The packet includes a `project_work` / `project_root` item describing the
selected root, path, Git branch/remote when known, and whether the root came
from an assignment override, work-item default, or project default/fallback.

The response is a normal `context_packet` envelope with assignment refs only;
task, run, chat session, and message refs remain empty because preflight is
inspect-only. The packet includes a `runtime` / `launch_preflight` item with
`included=false` describing what will be created on confirm. For native Hecate
task assignments, the packet can also include a `runtime` / `launch_readiness`
item when the gateway is wired. That item is operator evidence, not the UI
authority; clients should gate launch confirmation with the typed
`/launch-readiness` projection rather than parsing context item copy.

The Projects cockpit uses this endpoint before `Start assignment`, `Prepare
chat`, and `Start from handoff` so the operator can review the effective launch
context before dispatch. The UI disables confirmation when
`/launch-readiness` reports blockers and offers repair actions for Project
Settings, Roles, Agent Profiles, and Connections. Project-local actions repair
defaults that feed assignment resolution; Connections remains the
provider/model readiness surface. `POST /start` remains the authoritative
mutation path and the task runner/gateway still performs the actual route
checks during execution.

Unqueued, terminal, already-linked, unsupported, misconfigured, or invalid
assignments return the same operator-facing error classes as start would use,
without causing launch side effects.

#### `POST /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/start`

Starts a project assignment through its stored driver. The request body is
optional. When present, `driver_kind` must match the stored assignment driver.

```json
{
  "driver_kind": "hecate_task"
}
```

For `driver_kind="hecate_task"`, starting verifies that the project, work item,
assignment, and role exist, then
creates a normal Task with `execution_kind="agent_loop"`,
`origin_kind="project_work_item"`, and `origin_id` set to the work item ID. The
task response also exposes `work_item_id` and `assignment_id` for direct
inspection, and the created run snapshots `project_id`, `work_item_id`, and
`assignment_id` directly on the run payload. The
task title, prompt, and system prompt are composed from a visible launch-context
block covering project, work item, assignment, role, execution hints, role
defaults, project defaults, and any profile-activated prompt context. Project
assignment tasks set `workspace_system_prompt_policy="exclude"` so the legacy
root `CLAUDE.md` / `AGENTS.md` compatibility layer cannot bypass profile
context-source policy. Role default provider/model/profile override
project defaults for the backing task when configured; project
provider/model/workspace settings remain the fallback. Provider and model
defaults are route hints, so catalog/routing validation happens during task
start instead of role save. Preflight snapshots the selected model's current
readiness so the operator can fix stale provider/model defaults before a task
and run are created. The workspace root must
resolve to an absolute existing project root before a task is created; missing
or defaultless roots return `400 invalid_request`. A missing model returns
`422 model_not_configured`.

The endpoint then starts the task through the canonical task runner, so normal
task approvals, queueing, run events, artifacts, and SSE inspection apply. On
success it also persists a structured context packet on the created run, updates
`execution_ref.context_snapshot_id` to that packet id, then updates the
assignment with `execution_ref.task_id`, latest `execution_ref.run_id`, status,
and timestamps before returning the updated assignment:

The persisted context packet records the resolved profile and applies its
project memory/context-source policies. For native assignments, it also records
the same `runtime` / `launch_readiness` metadata captured by preflight so later
run-context inspection can show what provider/model readiness looked like when
the assignment was started. `include` / `include_enabled` make the
enabled project records active in the packet and add a `prompt_context`
instructions item summarizing what was loaded into the native assignment prompt.
`visible_only` / `inherit` keep records inspect-only, and `exclude` omits them
so memory bodies are not snapshotted. Prompt context is capped at 12 KiB total,
2 KiB per memory entry, and 8 KiB per source body. Only enabled
`workspace_instruction` sources with `format="agents_md"` are body-loaded through
WorkspaceFS; host-specific sources remain metadata-only and produce inspector
warnings when skipped. If the resolved provider is a cloud route, included
memory and workspace-instruction bodies leave the local machine as part of the
model request.

```json
{
  "object": "project_assignment",
  "data": {
    "id": "asgn_...",
    "project_id": "proj_...",
    "work_item_id": "work_...",
    "role_id": "software_developer",
    "driver_kind": "hecate_task",
    "status": "queued",
    "execution_ref": {
      "kind": "task_run",
      "task_id": "task_...",
      "run_id": "run_...",
      "context_snapshot_id": "ctx_...",
      "status": "queued"
    },
    "execution": {
      "task_id": "task_...",
      "run_id": "run_...",
      "task_status": "queued",
      "run_status": "queued",
      "status": "queued"
    },
    "created_at": "2026-06-03T12:00:00Z",
    "updated_at": "2026-06-03T12:00:01Z",
    "started_at": "2026-06-03T12:00:01Z"
  }
}
```

For `driver_kind="external_agent"`, starting prepares a supervised External
Agent chat session without sending the assignment prompt. The resolved Agent
Profile must name an `external_agent_kind` such as `codex`, `claude_code`,
`cursor_agent`, or `grok_build`; profile `external_agent_options` may set
Hecate-owned launch controls for that adapter. Hecate validates the project
workspace root, creates and prepares the chat session through the External Agent
supervisor, stores a project assignment context packet on the assignment, and
links `execution_ref.chat_session_id` plus
`execution_ref.context_snapshot_id`. Task-run fields and `message_id` remain
empty until the operator sends a turn in the linked chat.

```json
{
  "object": "project_assignment",
  "data": {
    "id": "asgn_...",
    "project_id": "proj_...",
    "work_item_id": "work_...",
    "role_id": "software_developer",
    "driver_kind": "external_agent",
    "status": "running",
    "execution_ref": {
      "kind": "chat_session",
      "chat_session_id": "chat_...",
      "context_snapshot_id": "ctx_...",
      "status": "running"
    },
    "created_at": "2026-06-03T12:00:00Z",
    "updated_at": "2026-06-03T12:00:01Z",
    "started_at": "2026-06-03T12:00:01Z"
  }
}
```

Repeated starts for an assignment that already has an active execution return
`409` with the current assignment envelope and do not create another task/run.
Assignments in terminal states (`completed`, `failed`, `cancelled`) also return
`409`. If task creation succeeds but task start fails, Hecate keeps the
assignment's `execution_ref.task_id`, marks the assignment `failed`, and returns
an error so the operator can inspect the linked task instead of losing the
partial state.

#### `GET /hecate/v1/projects/{id}/handoffs`

Lists structured handoff records for the project. Optional query parameters:
`work_item_id=<id>` and `status=<pending|accepted|superseded|dismissed>`.
Responses use `{ "object": "project_handoffs", "data": [...] }`.

#### `GET /hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs`

Lists structured handoff records for one work item. Handoffs are sorted by
latest update time, newest first.

```json
{
  "object": "project_handoffs",
  "data": [
    {
      "id": "handoff_...",
      "project_id": "proj_...",
      "work_item_id": "work_...",
      "source_assignment_id": "asgn_...",
      "source_run_id": "run_...",
      "source_chat_session_id": "chat_...",
      "source_message_id": "msg_...",
      "target_role_id": "reviewer_qa",
      "target_assignment_id": "asgn_review_...",
      "target_work_item_id": "work_followup_...",
      "title": "QA handoff",
      "summary": "The implementation is ready for review.",
      "recommended_next_action": "Review the changed files and run the focused checks.",
      "linked_artifact_ids": ["art_..."],
      "linked_memory_ids": ["mem_..."],
      "context_refs": ["ctx_..."],
      "status": "pending",
      "provenance_kind": "agent_draft",
      "trust_label": "operator_reviewed",
      "created_by_role_id": "software_developer",
      "created_at": "2026-06-03T12:00:00Z",
      "updated_at": "2026-06-03T12:00:00Z",
      "status_changed_at": "2026-06-03T12:00:00Z"
    }
  ]
}
```

#### `POST /hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs`

Creates a handoff. `title`, `summary`, and `recommended_next_action` are
required. `status` defaults to `pending`, `provenance_kind` defaults to
`operator`, and `trust_label` defaults to `operator_reviewed`. Source/target
assignment IDs, if supplied, must belong to the same work item. Linked artifact
IDs, memory IDs, and context refs are stored as references only; creating a
handoff does not write memory, inject context, start a task, or open a chat.
The Projects UI can use a handoff's target role/work-item hints to create a
queued follow-up assignment. That operation remains operator-controlled: the UI
creates the assignment, records `target_assignment_id` on the handoff, and marks
the handoff accepted, but it does not start the assignment automatically. Source
assignment/run/chat/message/context refs remain on the handoff as provenance
rather than being copied into the new assignment as if they were the new
assignment's own execution links.

```json
{
  "source_assignment_id": "asgn_...",
  "target_role_id": "reviewer_qa",
  "title": "QA handoff",
  "summary": "The implementation is ready for review.",
  "recommended_next_action": "Create a queued QA assignment and run focused UI tests.",
  "linked_artifact_ids": ["art_..."],
  "linked_memory_ids": ["mem_..."],
  "context_refs": ["ctx_..."],
  "provenance_kind": "agent_draft",
  "trust_label": "operator_reviewed",
  "created_by_role_id": "software_developer"
}
```

Returns `{ "object": "project_handoff", "data": { ... } }`.

#### `PATCH /hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs/{handoff_id}`

Updates handoff refs, target hints, text fields, linked IDs, provenance/trust
metadata, or `status`. Status changes update `status_changed_at`.

#### `POST /hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs/{handoff_id}/status`

Transitions only the handoff status. The body is `{ "status": "accepted" }`
where status is one of `pending`, `accepted`, `superseded`, or `dismissed`.
Accepting a handoff records operator intent; it does not automatically start a
linked assignment.

#### `DELETE /hecate/v1/projects/{id}/work-items/{work_item_id}/handoffs/{handoff_id}`

Deletes the handoff record. It does not delete linked artifacts, memory
entries, tasks, runs, chats, work items, or assignments.

#### `GET /hecate/v1/projects/{id}/work-items/{work_item_id}/artifacts`

Lists collaboration artifacts attached to a work item.

#### `POST /hecate/v1/projects/{id}/work-items/{work_item_id}/artifacts`

Creates a collaboration artifact. `kind` and `body` are required.
`assignment_id` is optional; when supplied it must refer to an assignment on
the same work item.

The Projects cockpit uses `kind="review"` artifacts to record reviewer outcomes
after a review assignment. In V1 the cockpit exposes this action only for
assignments whose role appears in the work item's `reviewer_role_ids`; callers
can still create any collaboration artifact through this endpoint. The current
V1 body is Markdown-compatible text with verdict, risk, summary, verification,
and follow-up sections, and review artifacts may also carry structured review
metadata:

- `reviewed_assignment_id` — optional assignment being reviewed. When supplied
  it must refer to an assignment on the same work item.
- `review_verdict` — optional `approved`, `changes_requested`, `blocked`, or
  `risk`.
- `review_risk` — optional `low`, `medium`, `high`, or `unknown`.
- `review_follow_up_required` — optional boolean used by Projects attention
  surfaces.

The review verdict/risk enum values are validated by the runtime and mirrored
by the Projects UI picker.

Hecate records these fields for filtering and operator triage, but does not
mutate work-item status or auto-dispatch follow-up work from the verdict.
Operators can create a separate handoff from the review artifact when follow-up
is needed. The UI may also offer a shortcut that creates the handoff and queued
follow-up assignment together, but it still records the handoff first and does
not start the assignment automatically.

The Projects cockpit uses `kind="evidence_link"` artifacts to attach generic
external or local evidence to a work item. Evidence links are intentionally not
GitHub- or code-specific: a link can point at a source document, research
artifact, ticket, deployment, pull request, design file, meeting note, or any
other operator-provided reference. Evidence link metadata is stored only as
project provenance; Hecate does not fetch the URL, grant access to the external
system, or treat the provider as policy authority. Evidence links may carry:

- `evidence_source_kind` — free-form source category such as `source_document`,
  `pull_request`, `ticket`, `deployment`, `design_file`, or `meeting_note`.
- `evidence_url` — optional URL or locator string. Hecate stores this
  operator-provided value as-is; clients must validate the scheme before
  rendering it as a clickable link.
- `evidence_external_id` — optional external identifier when a URL is not the
  best reference.
- `evidence_provider` — optional source system label such as `github`, `figma`,
  `jira`, `docs`, or `local`.
- `evidence_trust_label` — provenance/trust label, defaulting to
  `operator_provided`.

An evidence link requires `body` plus either `evidence_url` or
`evidence_external_id`. Non-evidence artifacts clear these evidence fields on
write.

```json
{
  "kind": "handoff",
  "assignment_id": "asgn_...",
  "title": "Backend handoff",
  "body": "Store and API are ready for UI wiring.",
  "author_role_id": "software_developer"
}
```

## Project Assistant endpoints

Project Assistant is the API-first assistant-action foundation for project
operations. It does not expose an open-ended chat persona. Clients can ask the
server to draft a reviewable proposal from project context, or submit a typed,
allowlisted proposal directly. Operators inspect the exact changes, then
explicitly apply the proposal. Apply revalidates current server state before
mutating projects, chats, project work, or memory candidates.

Apply is a human-gated operation. Do not wire it as a direct model-callable tool
without an explicit blocking operator confirmation. Before mutating durable
stores, apply preflights the remaining typed actions against current project,
work, handoff, memory-candidate, and chat targets, including explicit resources
created earlier in the same proposal. Multi-action apply is sequential and
resumable within the current process: on a failure the error includes
`apply_status`, `failed_action_index`, `total_action_count`,
`committed_action_count`, `resume_action_index`, and `partial_result`.
`apply_status` matches `partial_result.status`: `blocked_before_apply` or
`partial_due_to_runtime_failure`. Successful apply results use `status:
applied`. A `blocked_before_apply` result means preflight blocked the current
apply attempt before writing another action. A
`partial_due_to_runtime_failure` result means the unchanged proposal id can
resume after the committed actions. Changing the action set or reapplying a
fully applied proposal returns `409 conflict`.

Endpoints:

- `POST /hecate/v1/project-assistant/context`
- `POST /hecate/v1/project-assistant/draft`
- `POST /hecate/v1/project-assistant/propose`
- `POST /hecate/v1/project-assistant/apply`
- `POST /hecate/v1/chat/sessions/{id}/project-assistant/draft`

`context` returns the v0 item-limited and body-budgeted project packet plus the
inspectable Auto role/driver selection that `draft` will use. It includes
project context-source metadata, but not source file bodies. Memory and
memory-candidate bodies are truncated at per-body byte limits and carry returned
byte counts, original byte counts, truncation flags, and cheap token estimates.
`draft` creates proposal data only; it does not create a chat message, task,
run, assignment, or external agent session. `draft_mode` defaults to
`deterministic`; `draft_mode: "bootstrap"` deterministically proposes memory
candidates from enabled guidance-source metadata and project roles from enabled
available project-skill registry records; `draft_mode: "model"`
can use the project default model or explicit request model to author typed
proposal actions, but those actions are
still project-scoped, allowlisted, server-validated, and explicitly applied by
the operator. Model-backed drafts use the normal model gateway path and send the
item-limited, body-budgeted context packet, including accepted memory and
pending memory-candidate excerpts, to the selected local or cloud provider
route. The packet is body-budgeted but not yet provider-tokenizer fitted.
Project Assistant assignment proposals create unstarted queued assignments and
cannot carry `task_id`, `run_id`, `chat_session_id`, `message_id`, or
`context_snapshot_id` links. Deterministic assignment drafts preserve the
selected work item's `root_id` in the proposal patch when present, so
branch/worktree selection survives the proposal/apply step. See
[`project-assistant.md`](project-assistant.md) for the context and draft
requests, proposal schema, supported action kinds, confirmation behavior, and
safety model.
`draft_mode: "review_follow_up"` is a deterministic selected-work shortcut that
requires `review_artifact_id` and returns a proposal to create a follow-up
handoff, create a queued assignment, and link the handoff to that assignment.
It does not start the assignment or mutate project work before apply.

`POST /hecate/v1/chat/sessions/{id}/project-assistant/draft` is the Chat
handoff variant for project-linked Hecate Chat sessions. The request body
accepts the deterministic draft fields `request`, optional `work_item_id`,
optional `role_id`, and optional `driver_kind`; Hecate derives the project from
the chat session and rejects unprojected or external-agent sessions. The
endpoint always uses deterministic drafting and returns
`project_assistant.proposal` data only. The operator UI exposes it through the
compact `Draft proposal` composer action and the Hecate-owned
`/proposal <request>` slash command. It does not call the model-backed draft
path, append chat messages, create project records, or apply the proposal; UI
clients should hand the response to the Projects Project Assistant review/apply
surface. Clients may carry local source metadata, such as the request text and
chat session id, for review UI navigation; that metadata is not part of the
proposal apply payload.

Task-backed project-linked Hecate Chat may also produce
`project_assistant_proposal` artifacts when the model calls the
`draft_project_proposal` agent-loop tool. The artifact content is JSON with the
linked `project_id`, optional `source_chat_session_id`, original request, and
embedded Project Assistant `proposal`. The normalized chat activity may carry
proposal title/action-count detail so clients can render a compact review row.
It is a review handoff only; clients open it in Projects and still apply through
`/hecate/v1/project-assistant/apply`.

## Chat session endpoints

### `GET /hecate/v1/chat/sessions`

Lists chat sessions. Chat sessions use the process-wide storage backend
selected by `HECATE_BACKEND`. They are the alpha transcript surface for Hecate
Chat and External Agent sessions. A session has a stable `agent_id` that
chooses the chat owner:

- `agent_id="hecate"` — Hecate owns the chat. Individual turns set
  `execution_mode="hecate_task"` and `tools_enabled` to choose between a normal
  provider/model call (`tools_enabled=false`) and a visible `agent_loop` task
  with Hecate tools, task approvals, artifacts, and OTel
  (`tools_enabled=true`). Hecate Chat sessions may opt into
  RTK command-output compaction with `rtk_enabled=true`; shell and git tool
  calls then launch as `rtk sh -lc <command>` while keeping Hecate approvals,
  policy validation, sandboxing, limits, and timeouts in place.
- `agent_id="codex"`, `"claude_code"`, `"cursor_agent"`, `"grok_build"`, or another
  registered adapter id — the external adapter owns the native session while
  Hecate supervises lifecycle, transcript, diagnostics, and external-agent
  approvals. Turns use `execution_mode="external_agent"`.

`HECATE_BACKEND=sqlite` or `postgres` persists the entire chat state bundle: sessions,
messages, **and** the operator-facing
approval rows + grants documented under
`/hecate/v1/chat/sessions/{id}/approvals` and `/hecate/v1/chat/grants`. They all
move together so chat state can't go split-brain. On startup the gateway
runs a reconcile pass that flips any approvals stuck in `pending` from a prior
process to `status=timed_out` with `path=startup_reconcile` — process-local
waiters can't be resurrected, so the operator UI never sees an actionable
"pending" row that nothing is actually blocked on.

Resolved approvals are pruned by the retention worker
(`HECATE_RETENTION_CHAT_APPROVALS_*`, default 30d / 10k). Operator-
authored grants are NOT subject to that retention — only their own
`expires_at` drives deletion, so explicit operator intent outlives normal
retention windows.

The same per-session SSE stream (`GET /hecate/v1/chat/sessions/{id}/stream`)
also carries approval lifecycle events so frontends don't have to poll. Two
event types are emitted in addition to normal chat session updates:

```
event: approval.requested
data: {
  "approval_id":   "appr_01JX...",
  "session_id":    "chat_01JX...",
  "adapter_id":    "codex",
  "tool_kind":     "file_write",
  "tool_name":     "Edit src/foo.go",
  "scope_choices": ["once","session","workspace_tool","adapter_tool"],
  "created_at":    "2026-05-04T10:23:45.123Z",
  "expires_at":    "2026-05-04T10:28:45.123Z"
}

event: approval.resolved
data: {
  "approval_id":     "appr_01JX...",
  "session_id":      "chat_01JX...",
  "status":          "approved",
  "decision":        "approve",
  "scope":           "session",
  "path":            "operator",
  "selected_option": "allow_always_for_session",
  "resolved_at":     "2026-05-04T10:24:01.000Z"
}
```

Frontends switch on the `path` field of `approval.resolved` to render the
disposition: `operator` (explicit decision), `grant` (pre-existing grant
short-circuited the prompt), `default_mode` (`auto`/`deny` mode resolved
without operator), `timeout` (prompt-mode timeout fired), or
`request_cancelled` (the request context died — session shutdown, adapter
teardown, HTTP context cancellation, process stop). `request_cancelled` is
operationally distinct from `operator`: nobody clicked anything, the request
just died.

Backpressure: per-subscriber buffers are bounded (16 events). On overflow,
approval events are **dropped** rather than blocking the coordinator. A
slow operator UI catches up by re-fetching `/approvals?status=pending` on
reconnect. Replay across restart is not supported in this slice.

```json
GET /hecate/v1/chat/sessions
→ 200
{
  "object": "chat_sessions",
  "data": [
    {
      "id": "chat_...",
      "title": "Hecate Chat",
      "project_id": "proj_...",
      "agent_id": "hecate",
      "provider": "ollama",
      "model": "qwen2.5-coder",
      "capabilities": {
        "tool_calling": "basic",
        "streaming": true,
        "source": "provider"
      },
      "status": "completed",
      "rtk_enabled": true,
      "turns_used": 3,
      "max_turns_per_session": 50,
      "session_started_at": "2026-05-03T12:00:00Z",
      "max_session_duration_ms": 7200000,
      "idle_timeout_ms": 1800000,
      "message_count": 2,
      "created_at": "2026-05-03T12:00:00Z",
      "updated_at": "2026-05-03T12:00:08Z"
    }
  ]
}
```

### `POST /hecate/v1/chat/sessions`

Creates a chat session. `agent_id` chooses the session owner:

- `hecate` (default) creates a Hecate Chat. Subsequent turns send
  `execution_mode="hecate_task"` with `tools_enabled` set per turn
  (`tools_enabled=true` for tool-backed runs, `tools_enabled=false` for
  direct model chat).
- Any registered external-agent id, such as `codex`, `claude_code`,
  `cursor_agent`, or `grok_build`, creates an External Agent chat and requires
  `workspace`.

Hecate Chat sessions may be created as empty shells before a model or workspace
is chosen. Hecate validates the selected model when the first message is sent.
When `provider` is omitted on a model-backed turn, Hecate routes across
configured providers that expose the selected model.

`project_id` is optional. When supplied, it must reference an existing project
or Hecate returns `404 not_found`. Project-scoped sessions are still normal
chat sessions, but deleting the project later deletes those project-scoped
transcripts as part of the project cleanup.

`title` is optional session metadata. The Projects UI uses it when launching a
chat from a project-work assignment so the empty chat shell is named after the
work item and role before the first turn. The launch-context draft itself lives
only in the client composer until the operator edits and submits it through
`POST /hecate/v1/chat/sessions/{id}/messages`; creating the session does not
create a user message or run.

For Hecate Chat sessions, `rtk_enabled` records the chat's command-output
compaction preference. It is only applied when a future turn runs through the
task-backed `hecate_task` execution mode; direct model turns never execute
local commands.

When `workspace` is provided, it must be an operator-controlled local
directory. Hecate validates and canonicalizes the path before a tool-backed or
external-agent run starts, so later runs use the resolved directory instead of
failing only after execution starts.

For external-agent `agent_id` values, session creation also starts or restores
the native ACP session immediately. Clients may include `config_options`
selected from the agent catalog when a catalog row exposes Hecate-managed
launch controls; Hecate validates required launch options and uses them when
starting the agent process. After the ACP session exists, agent-owned
`config_options` are returned with the session so clients can render them before
the first prompt. If the agent reports ACP `initialize.agentInfo`, Hecate returns
the trimmed implementation metadata as `agent_info` on the chat session and on
assistant messages produced by that session. If the adapter binary is missing,
unauthenticated, missing a required launch option, or fails its ACP handshake,
session creation fails and Hecate removes the empty chat record.

External Agent session creation may also include `mcp_servers`, using the same
stdio/HTTP server shape as task-create `mcp_servers`. Hecate stores the server
list on the chat session and passes it to the adapter during ACP `session/new`
or `session/load`; support beyond transport propagation depends on the selected
adapter and upstream agent. Hecate-owned chats do not accept session-level
`mcp_servers`; pass them on `POST /hecate/v1/chat/sessions/{id}/messages` so
the backing task segment records the exact server set for that run.
If an ACP agent advertises native slash commands with
`available_commands_update`, Hecate stores the latest command metadata on the
session as `available_commands`. Clients send those commands back as ordinary
prompt text, for example `/web agent client protocol`; there is no separate ACP
execute-command RPC.

```json
POST /hecate/v1/chat/sessions
{
  "agent_id": "hecate",
  "project_id": "proj_...",
  "provider": "ollama",
  "model": "qwen2.5-coder",
  "title": "Hecate Chat"
}

→ 200
{
  "object": "chat_session",
  "data": {
    "id": "chat_...",
    "title": "Hecate Chat",
    "project_id": "proj_...",
    "agent_id": "hecate",
    "provider": "ollama",
    "model": "qwen2.5-coder",
    "capabilities": {
      "tool_calling": "basic",
      "streaming": true,
      "source": "provider"
    },
    "status": "idle",
    "rtk_enabled": false,
    "turns_used": 0,
    "session_started_at": "2026-05-03T12:00:00Z",
    "messages": []
  }
}
```

External Agent creation with ACP session controls:

```json
POST /hecate/v1/chat/sessions
{
  "agent_id": "grok_build",
  "workspace": "/Users/alice/src/my-app",
  "mcp_servers": [
    {
      "name": "filesystem",
      "command": "node",
      "args": ["./mcp-filesystem-server.js"],
      "approval_policy": "require_approval"
    }
  ],
  "config_options": [
    {
      "id": "model",
      "name": "Model",
      "category": "model",
      "type": "select",
      "source": "acp_model",
      "current_value": "chosen-model-id"
    }
  ]
}

→ 200
{
  "object": "chat_session",
  "data": {
    "id": "chat_...",
    "agent_id": "grok_build",
    "workspace": "/Users/alice/src/my-app",
    "driver_kind": "acp",
    "agent_info": {
      "name": "grok-build-acp-adapter",
      "title": "Grok Build ACP Adapter",
      "version": "0.1.0-alpha.1"
    },
    "status": "idle",
    "mcp_servers": [
      {
        "name": "filesystem",
        "command": "node",
        "args": ["./mcp-filesystem-server.js"],
        "approval_policy": "require_approval"
      }
    ],
    "config_options": [
      {
        "id": "model",
        "name": "Model",
        "category": "model",
        "type": "select",
        "source": "acp_model",
        "current_value": "chosen-model-id"
      }
    ],
    "available_commands": [
      {
        "name": "web",
        "description": "Search the web",
        "input_hint": "query"
      }
    ],
    "messages": []
  }
}
```

### `GET /hecate/v1/chat/sessions/{id}`

Returns the full session transcript, including user messages and assistant
messages produced by the backing runtime. Hecate-owned sessions include
`provider`, `model`, and the current capability snapshot; once a tools-on turn
creates a backing task, they also include `task_id` and `latest_run_id`.
Individual chat messages carry the durable runtime snapshot:
`execution_mode`, derived `turn_kind`, `segment_id`, optional `task_id`,
optional `run_id`, provider/model, and capabilities. Frontends should prefer
message-level `turn_kind` (`direct_model`, `hecate_task`, or `external_agent`)
for UI routing and keep `execution_mode` / `tools_enabled` as compatibility
fields. If tools are re-enabled after a direct model segment, Hecate creates a
new task-backed segment in the same transcript; older messages keep their
original runtime/model/task snapshots.

The response also includes a derived `segments` array. Messages remain the
durable source of truth; segments are a render helper that groups contiguous
turns with the same `segment_id` so clients can show transcript boundaries such
as "tools off with smollm2" → "tools on with qwen2.5-coder". Each segment
contains its derived `turn_kind`, `execution_mode`, provider/model snapshot,
optional `task_id`, latest run id, status, message count, and first/last
timestamps.

Hecate-owned sessions may include `context_summary` after automatic or manual
context compaction. The summary is operator-visible metadata for older
transcript context and includes `content`, `message_count`,
`through_message_id`, `compacted_at`, and `strategy`. `strategy` is
`semantic_transcript_summary` when Hecate used the session provider/model to
summarize the older transcript window, and `deterministic_transcript_summary`
when it used the bounded transcript-line fallback. Original messages remain in
`messages`; future direct model turns inject the summary as background context
and send newer messages in full.

External Agent sessions may also include `config_options`, a normalized
projection of ACP session configuration options reported by the agent during
`session/new`, `session/load`, or `session/set_config_option`, merged with any
Hecate-managed launch controls that affected the agent process. Because
Hecate starts the ACP session during chat creation, clients can usually show
session controls before the first prompt. Catalog launch controls can be shown
even earlier from `GET /hecate/v1/agent-adapters`. Common `category` values
include `model`, `mode`, and `thought_level`, but clients must handle missing
or custom categories.

External Agent sessions may also include `available_commands`, the latest ACP
available slash command list advertised by the agent. Each item has `name`,
optional `description`, and optional `input_hint`. The `name` is agent-owned;
clients should render it as a slash command hint but submit the chosen command
as normal prompt text.

External Agent sessions and assistant messages may include `agent_info`, the
adapter-reported ACP implementation metadata from `initialize.agentInfo`.

Hecate-owned sessions may include `context_summary` when older direct-model
chat transcript turns have been compacted. The original messages remain in the
durable transcript; `context_summary` tells clients and later model calls how
many older messages were summarized, the last summarized message id, and when
the summary was produced. Hecate injects that summary as system context before
the retained recent transcript on later direct-model turns.

### `PATCH /hecate/v1/chat/sessions/{id}`

Renames a chat session. This is shared by Hecate Chat
(`agent_id="hecate"`) and External Agent sessions. The title is
metadata only; it does not change the prompt history, workspace, provider/model,
or ACP native session.

```json
PATCH /hecate/v1/chat/sessions/chat_...
{
  "title": "Review release notes"
}

→ 200
{
  "object": "chat_session",
  "data": {
    "id": "chat_...",
    "title": "Review release notes",
    "agent_id": "hecate",
    "status": "completed",
    "messages": []
  }
}
```

### `POST /hecate/v1/chat/sessions/{id}/config-options/{config_id}`

Updates one ACP session configuration option for an active External Agent
session. Body:

```json
{
  "value": "smart"
}
```

`value` may be a string select value or a boolean. Hecate forwards the change to
the active adapter with ACP `session/set_config_option`, persists the adapter's
returned full `config_options` list on the chat session, publishes a session
snapshot, and returns the updated session response. If the native ACP session
has been closed or is not active, the endpoint returns
`409 chat.session_not_running`; create a new External Agent chat or retry
after the session has been restored.

### `PATCH /hecate/v1/chat/sessions/{id}/settings`

Updates Hecate-owned chat settings for future turns. This endpoint currently
accepts `rtk_enabled` for `agent_id="hecate"` sessions. External Agent sessions
reject it with `chat.runtime_mismatch` because Codex, Claude Code, Cursor
Agent, Grok Build, and other ACP adapters own their own command execution.

When an existing Hecate Chat session already has a backing task, the task
record is updated too so later continued runs inherit the same setting.
Running turns are not mutated mid-flight.

```json
PATCH /hecate/v1/chat/sessions/chat_.../settings
{
  "rtk_enabled": true
}

→ 200
{
  "object": "chat_session",
  "data": {
    "id": "chat_...",
    "agent_id": "hecate",
    "rtk_enabled": true
  }
}
```

### `POST /hecate/v1/chat/sessions/{id}/compact`

Compacts older transcript context for a Hecate-owned chat session and returns
the updated session. This endpoint is deterministic and local to Hecate; it
does not call a model. It summarizes older user/assistant turns into
`context_summary` while retaining the newest messages in full for later turns.

External Agent sessions reject this endpoint with `409 chat.runtime_mismatch`
because their native runtimes own their own context windows. If there are not
enough older messages to compact, Hecate returns `400 invalid_request`.

```json
POST /hecate/v1/chat/sessions/chat_.../compact

→ 200
{
  "object": "chat_session",
  "data": {
    "id": "chat_...",
    "agent_id": "hecate",
    "context_summary": {
      "content": "- User: previous request\n- Assistant: previous answer",
      "message_count": 2,
      "through_message_id": "msg_...",
      "compacted_at": "2026-06-16T10:00:00Z"
    }
  }
}
```

### `POST /hecate/v1/chat/sessions/{id}/messages`

Sends the submitted prompt to the session's backing runtime and appends both
the user message and assistant output.

`POST` also accepts per-turn overrides:

- `execution_mode` — `hecate_task` or `external_agent`. Hecate Chat sessions
  use `hecate_task` (tools-on/off is set separately via `tools_enabled`);
  External Agent sessions always use `external_agent`.
- `tools_enabled` (boolean) — per-turn tools-on/off signal for Hecate Chat
  sessions. `true` opts into the tool-backed `agent_loop` path; `false`
  dispatches the prompt directly to the selected model without creating a
  task. When omitted, Hecate treats Hecate-owned turns as tools-on unless model
  capabilities require a tools-off direct model fallback.
- `provider` / `model` — used for tools-off turns and new task-backed
  Hecate Chat segments. Existing task-backed segments continue with their
  saved model snapshot until the operator turns tools off or starts a new
  task-backed segment.
- `system_prompt` — applied to tools-off turns and new task-backed Hecate Chat
  segments. When the chat is linked to a project, Hecate prepends hidden
  project workflow guidance and bounded project context before the operator
  prompt. That guidance uses the same project/role/skill/active-work/memory
  vocabulary as project assignment launch context and keeps Chat conversational
  while telling the model to treat project-planning intent as proposal-only
  Project Assistant work; it does not grant direct project mutation rights.
  Task-backed project-linked Hecate Chat exposes the bounded
  `draft_project_proposal` tool for that proposal-only path; direct model turns
  receive the guidance but cannot call tools.
  Skill entries are metadata only and do not inject `SKILL.md` bodies. If the
  selected model routes to a cloud provider, the bounded project prompt context
  is sent through the normal model gateway route like any other chat prompt.
- `workspace` — required when starting a task-backed Hecate Chat turn
  (`tools_enabled=true`) on a session that does not already have a workspace.
- `mcp_servers` — optional per-turn external MCP server configs for Hecate
  Chat tool-backed turns. Same shape and validation as task-create
  `mcp_servers`; when present, Hecate starts a fresh task-backed segment so the
  server set is explicit for that run. MCP Apps resources returned by those
  tools render through `activities[].mcp_app`.

For `tools_enabled=false` on a Hecate Chat session, Hecate calls the normal
gateway path and stores the user/assistant messages without creating a Task.
Project-linked direct model turns receive the same project workflow guidance
in their system prompt, but they still cannot run tools or mutate project
state.
For `execution_mode="external_agent"`, Hecate sends the prompt to the
session's native ACP session. For `tools_enabled=true` on a Hecate Chat
session, the first tool-enabled prompt creates a visible `agent_loop` task and
starts it; follow-up prompts continue the latest terminal run when the
immediately previous segment was also task-backed. If the previous segment was
direct model chat (tools off), Hecate starts a fresh task-backed segment in
the same transcript.

Only one task-backed segment can be active in a Hecate Chat session at a time.
If the latest backing task is queued, running, or awaiting approval, **all** new
turns on that chat are rejected with `409 chat.agent_session_busy`,
including tools-off (`tools_enabled=false`) turns. Operators should wait for the
task to finish, resolve the pending approval, or cancel/stop the active run
before sending another prompt. The operator UI layers a local composer queue on
top of that API contract: prompts submitted while a run is busy are held in a
client-side FIFO and posted only after the active task reaches a terminal
state. Queue entries are scoped to the chat session that created them so a
prompt cannot drain into a different transcript after the operator switches
sessions. That queue is intentionally not durable until each prompt is
submitted.

Clients can block obvious stale selections by combining `/v1/models` with
`/hecate/v1/providers/status`, but the server remains authoritative. If a stale
provider/model selection slips through, Hecate Chat returns
`422 model_not_configured` with provider readiness fields, suggested replacement
models, and an `operator_action` repair hint in the error details.

The response returns after the backing turn finishes, times out, is cancelled,
or fails. For live output while the turn is running, subscribe to the session
stream before posting the message. Task-backed Hecate Chat turns update the running
assistant message's `content` when the backing task's model route supports
streaming; non-streaming providers still publish the final assistant content
when the run finishes. External Agent turns continue to publish normalized
adapter output as it arrives.

Before starting the adapter, Hecate enforces optional chat guardrails:
`HECATE_CHAT_MAX_TURNS_PER_SESSION`,
`HECATE_CHAT_MAX_SESSION_DURATION`, and
`HECATE_CHAT_IDLE_TIMEOUT`. Each returns HTTP 422 with a stable
`error.type` when exceeded:
`chat.session_limit_exceeded`,
`chat.session_duration_limit_exceeded`, or
`chat.session_idle_timeout`.

```json
POST /hecate/v1/chat/sessions/chat_.../messages
{
  "content": "Review the current diff and suggest fixes."
}

→ 200
{
  "object": "chat_session",
  "data": {
    "id": "chat_...",
    "status": "completed",
    "messages": [
      {
        "id": "msg_...",
        "role": "user",
        "content": "Review the current diff and suggest fixes."
      },
      {
        "id": "msg_...",
        "run_id": "agent_run_...",
        "request_id": "req_...",
        "trace_id": "d4c5...",
        "span_id": "8f3a...",
        "role": "assistant",
        "content": "...",
        "raw_output": "...",
        "agent_id": "codex",
        "agent_name": "Codex",
        "driver_kind": "acp",
        "native_session_id": "session_...",
        "agent_info": {
          "name": "codex-acp-adapter",
          "title": "Codex ACP Adapter",
          "version": "0.1.0-alpha.28"
        },
        "status": "completed",
        "cost_mode": "external",
        "workspace": "/Users/alice/project",
        "diff_stat": "...",
        "started_at": "2026-05-03T12:00:01Z",
        "completed_at": "2026-05-03T12:00:08Z",
        "duration_ms": 7000,
        "activities": [
          {
            "type": "started",
            "status": "completed",
            "title": "Starting external agent",
            "detail": "Codex in /Users/alice/project",
            "created_at": "2026-05-03T12:00:01Z"
          },
          {
            "type": "files_changed",
            "status": "completed",
            "title": "Files changed",
            "detail": "2 files changed",
            "created_at": "2026-05-03T12:00:08Z"
          },
          {
            "type": "completed",
            "status": "completed",
            "title": "Final answer",
            "created_at": "2026-05-03T12:00:08Z"
          }
        ]
      }
    ]
  }
}
```

Each adapter response gets a stable `run_id` plus start/end timestamps and
duration so clients can correlate streamed updates, final output, and future
artifacts without treating the assistant message id as the runtime identity.
It also stores `request_id`, `trace_id`, and `span_id`; use
`GET /hecate/v1/traces?request_id=<request_id>` to inspect the OTel-shaped
`chat.run` span for that prompt.
Task-backed Hecate Chat messages also include a `timing` object derived from
the backing run's task steps, approvals, and run events:

```json
{
  "total_ms": 12400,
  "queue_ms": 120,
  "model_ms": 8500,
  "tool_ms": 700,
  "approval_wait_ms": 2000,
  "overhead_ms": 1080,
  "turn_count": 2,
  "tool_count": 1,
  "bottleneck": "model",
  "bottleneck_ms": 8500
}
```

`overhead_ms` is the remainder after queue/model/tool/approval buckets and
covers gateway orchestration, artifact projection, polling cadence, and final
transcript rendering. It is intentionally named as overhead rather than a fake
artifact duration because Hecate does not yet record artifact-write spans for
every task artifact.
`content` is the normalized transcript that should be shown by default.
`raw_output` preserves raw ACP update JSON for diagnostics when an adapter emits
surprising structured output. `driver_kind` and `native_session_id` identify the
underlying ACP session reused across turns in the Hecate chat. `activities` is
the structured progress model for the Chats UI: it records lifecycle markers
such as starting, running, output, files changed, failed, cancelled, and final
answer. Task-backed MCP Apps tool calls can include `activities[].mcp_app` so
the UI can render the captured `text/html;profile=mcp-app` resource inline
while retaining the text fallback. Failures from the ACP adapter are still
represented as assistant
messages with `"status": "failed"` and `error` so the transcript stays intact.
Transport or request validation failures still use the normal Hecate error
envelope.

Chat execution errors:

| Status | `error.type`                     | Meaning                                                                                                                                                                                     |
| ------ | -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `400`  | `chat.workspace_required`        | Task-backed Hecate Chat turns and External Agent sessions need a selected workspace path before the first turn.                                                                             |
| `400`  | `chat.model_required`            | Hecate Chat needs an explicit selected model before direct model or task-backed turns, or an External Agent adapter requires a launch model before session start.                           |
| `400`  | `chat.agent_id_invalid`          | The requested session owner is not `hecate` and does not match a registered external-agent adapter.                                                                                         |
| `400`  | `chat.execution_mode_invalid`    | The requested turn execution mode is not one of `hecate_task` or `external_agent`.                                                                                                          |
| `400`  | `chat.runtime_mismatch`          | The request tried to run a turn through a runtime that does not match the existing session type.                                                                                            |
| `400`  | `chat.adapter_not_found`         | The selected external-agent adapter is not registered.                                                                                                                                      |
| `409`  | `chat.agent_session_busy`        | The backing task run is queued, running, or awaiting approval. Resolve/cancel the active run before sending another prompt, even for tools-off turns in the same Hecate Chat session.       |
| `409`  | `chat.session_stopping`          | The session is still cancelling or closing; retry after it settles.                                                                                                                         |
| `409`  | `chat.session_not_running`       | A stop request was issued when no run was active.                                                                                                                                           |
| `422`  | `model_not_configured`           | The selected model is not currently reported by the selected provider. Choose a discovered model or refresh/fix provider discovery.                                                         |
| `422`  | `chat.model_capability_required` | A task-backed Hecate Chat turn was explicitly requested, but the selected model is not known to support tools. Continue with direct model chat or choose a model that reports tool support. |

Client note: browser/operator clients may queue a prompt locally when they
receive or predict `chat.agent_session_busy`, but the server still
accepts only one active task-backed turn per Hecate Chat session.

### `GET /hecate/v1/chat/sessions/{id}/messages/{message_id}/context`

Returns the persisted context packet snapshot for an assistant message:
operator UIs render project-linked Hecate Chat packets with an explicit
project-prelude note, while the wire packet remains the same itemized
`context_packet` shape.

```json
GET /hecate/v1/chat/sessions/chat_.../messages/msg_.../context
→ 200
{
  "object": "context_packet",
  "data": {
    "id": "ctx_...",
    "version": "chat.context.v1",
    "execution_mode": "hecate_task",
    "provider": "ollama",
    "model": "llama3.1:8b",
    "execution_profile": "chat_agent",
    "workspace": "/workspace/hecate",
    "system_prompt_included": true,
    "message_count": 3,
    "refs": {
      "session_id": "chat_...",
      "message_id": "msg_...",
      "project_id": "proj_..."
    },
    "sources": [
      {
        "kind": "project",
        "label": "Hecate",
        "detail": "proj_...",
        "trust": "project"
      },
      {
        "kind": "transcript",
        "label": "Chat transcript",
        "detail": "3 chat messages including this turn",
        "trust": "operator"
      }
    ],
    "items": [
      {
        "section": "project",
        "kind": "project",
        "trust_level": "runtime_state",
        "origin": "proj_...",
        "title": "Hecate",
        "included": true,
        "inclusion_reason": "Project linked to this chat session"
      },
      {
        "section": "runtime",
        "kind": "transcript",
        "trust_level": "runtime_state",
        "origin": "chat.transcript",
        "title": "Chat transcript",
        "body": "3 chat messages including this turn",
        "included": true,
        "inclusion_reason": "Visible terminal transcript count for this turn"
      }
    ]
  }
}
```

Existing top-level fields and `sources` remain for older clients. Newer clients
should prefer `items` plus `refs` for trust-labelled, provenance-aware
inspection. Each item carries a stable `section` value so later inspectors can
group without inferring from `kind`. Current packets intentionally avoid storing
full system prompts, raw transcript text, source file contents, or
external-agent private prompt packing. Project assignment packets may include
project memory bodies because memory entries are first-class inspectable
context; source file bodies are represented by `BodyRef` plus `prompt_context`
summaries rather than copied into the packet.

Operator UI note: the current React console renders these packets as a compact
"what the agent saw" inspector. Chats expose it inline on assistant transcript
rows; Task Detail and Project assignment detail expose it behind an
`Inspect context` modal. The UI groups rows by `section` using labels such as
Profile, Instructions, Skills, Memory, Project sources, Work context, and
Runtime evidence; keeps trust labels on each item; falls back to legacy `sources` when
`items` are absent; and uses operator-facing copy such as `Not captured` when a
snapshot does not expose the full system prompt text.

Section values currently used by the runtime are:

- `instructions` for system-prompt, prompt-context, and instruction-layer metadata
- `skills` for resolved, skipped, or chat-visible project skill metadata; `SKILL.md` bodies are not included
- `memory` for project memory entries
- `workspace` for the selected workspace path
- `project` for project identity metadata
- `project_work` for chat-visible active work metadata and assignment launch work-item, assignment, role, execution-hint, handoff, and artifact-reference metadata
- `sources` for enabled project context-source metadata such as `workspace_doc` and `project_notes`
- `runtime` for transcript counts, task-runtime metadata, and external-agent session metadata

`included=true` means the item was part of the prepared context for that
message or run. `included=false` means the item is related inspectable metadata
that V1 did not inject into the runtime context. Hecate-owned project chat
packets use explicit `prompt_context` items to distinguish bounded project
prelude content from visible-only project source metadata. External Agent chat
and External Agent project-assignment packets mark project memory/source
records as visible-only because Hecate records metadata for inspection but does
not inject those bodies into adapter prompts. Native project-assignment packets
can mark project memory and eligible source metadata included only when the
resolved profile policy activated bounded prompt inclusion; handoffs and
artifact refs remain metadata-only.

Legacy packets can omit `id`, `execution_profile`, `refs`, or `section`. The
server backfills obvious request-scoped refs and default sections where it can,
but clients should render missing fields defensively.

### `GET /hecate/v1/chat/sessions/{id}/messages/{message_id}/files`

Returns a structured file list for a chat assistant message that captured
a workspace diff. The data is derived from the stored `diff` first, then falls
back to `diff_stat` when only the stat text is available.

```json
GET /hecate/v1/chat/sessions/chat_.../messages/msg_.../files
→ 200
{
  "object": "chat_changed_files",
  "data": [
    {
      "path": "src/foo.go",
      "additions": 12,
      "deletions": 3,
      "status": "modified"
    }
  ]
}
```

`status` is best-effort: `modified`, `added`, `deleted`, `renamed`, or
`binary`. Messages without a captured diff return an empty list.

### `GET /hecate/v1/chat/sessions/{id}/messages/{message_id}/files/{path}`

Returns the stored unified diff block for one changed file. Encode the path as
a URL path component (`encodeURIComponent(path)` in browser clients).

```json
GET /hecate/v1/chat/sessions/chat_.../messages/msg_.../files/src%2Ffoo.go
→ 200
{
  "object": "chat_changed_file_diff",
  "data": {
    "path": "src/foo.go",
    "additions": 12,
    "deletions": 3,
    "status": "modified",
    "diff": "diff --git a/src/foo.go b/src/foo.go\n..."
  }
}
```

Status codes:

- `200 OK` with the per-file diff.
- `404 not_found` when the session, message, or file path is unknown.

### `POST /hecate/v1/chat/sessions/{id}/messages/{message_id}/revert`

Reverts workspace changes captured by a chat assistant message. This is
only available for Git workspaces and only for paths present in the stored
agent-message diff; Hecate rejects arbitrary paths. Pass a non-empty `paths`
array to revert selected files, or an empty array to revert every file in the
captured diff.

```json
POST /hecate/v1/chat/sessions/chat_.../messages/msg_.../revert
{
  "paths": ["src/foo.go"]
}

→ 200
{
  "object": "chat_revert",
  "data": {
    "reverted": true,
    "paths": ["src/foo.go"],
    "diff_stat": "README.md | 1 +",
    "files": [
      {
        "path": "README.md",
        "additions": 1,
        "deletions": 0,
        "status": "modified"
      }
    ]
  }
}
```

After a successful revert, Hecate refreshes the message's stored `diff` and
`diff_stat` for the originally captured path set, appends a `files_reverted`
activity, and publishes an updated chat session snapshot. Non-Git
workspaces return `400 invalid_request` with a human-readable limitation.

### `GET /hecate/v1/chat/sessions/{id}/workspace-diff`

Returns the current Git diff for the chat session's selected workspace. This is
live working-tree state, not the captured diff from any assistant message.
The operator UI renders this as a Review tab: a changed-file list where each
file expands to its own rich diff, plus copy/discard actions for the full patch
or a single file.

```json
GET /hecate/v1/chat/sessions/chat_.../workspace-diff
→ 200
{
  "object": "chat_workspace_diff",
  "data": {
    "workspace": "/Users/alice/project",
    "diff_stat": "README.md | 1 +",
    "diff": "diff --git a/README.md b/README.md\n...",
    "has_changes": true,
    "files": [
      {
        "path": "README.md",
        "additions": 1,
        "deletions": 0,
        "status": "modified"
      }
    ]
  }
}
```

Sessions without a workspace return an empty diff response. Non-Git
workspaces return `400 invalid_request`.

### `GET /hecate/v1/chat/sessions/{id}/workspace-diff/files/{path}`

Returns the live unified diff for one file currently present in the session
workspace diff. Encode the path as a URL path component.

```json
GET /hecate/v1/chat/sessions/chat_.../workspace-diff/files/README.md
→ 200
{
  "object": "chat_workspace_file_diff",
  "data": {
    "path": "README.md",
    "additions": 1,
    "deletions": 0,
    "status": "modified",
    "diff": "diff --git a/README.md b/README.md\n..."
  }
}
```

The path must appear in the current workspace diff; Hecate rejects arbitrary
paths.

### `GET /hecate/v1/chat/sessions/{id}/workspace-files`

Returns the current file tree for the chat session's selected workspace. This
surface is intentionally separate from `workspace-diff`: clients can browse and
search the full workspace without mixing unchanged files into the changed-file
review flow.

The operator UI renders this as a **Files** tab. The tree is collapsed by
default, while search expands matching directories.

```json
GET /hecate/v1/chat/sessions/chat_.../workspace-files
→ 200
{
  "object": "chat_workspace_files",
  "data": {
    "workspace": "/Users/alice/project",
    "files": [
      {
        "path": "docs",
        "name": "docs",
        "kind": "directory"
      },
      {
        "path": "README.md",
        "name": "README.md",
        "kind": "file",
        "status": "modified",
        "size_bytes": 2048
      }
    ],
    "truncated": false
  }
}
```

Sessions without a workspace return an empty file list. The response may set
`truncated: true` when the workspace has more entries than the UI should render
eagerly.

### `POST /hecate/v1/chat/sessions/{id}/workspace-diff/revert`

Restores selected tracked files from the current Git workspace diff. Pass a
non-empty `paths` array to restore selected files, or an empty array to restore
every currently changed tracked file.

```json
POST /hecate/v1/chat/sessions/chat_.../workspace-diff/revert
{
  "paths": ["README.md"]
}

→ 200
{
  "object": "chat_workspace_diff",
  "data": {
    "workspace": "/Users/alice/project",
    "has_changes": false,
    "files": []
  }
}
```

Only paths present in the current diff can be restored. The endpoint refreshes
and returns the live workspace diff after Git restore completes.

### `GET /hecate/v1/chat/sessions/{id}/stream`

Streams live chat session snapshots as Server-Sent Events. This is an
in-process live feed, not the durable task-event log: session history remains in
the configured chat-session backend, while the stream fans out updates from the
currently running gateway process.

```text
event: snapshot
data: {"object":"chat_session","data":{...}}

event: done
data: {"object":"chat_session","data":{"status":"completed",...}}
```

Clients should subscribe before sending a message so they can receive live
updates. For External Agent sessions, snapshots include partial ACP output from
the adapter. For task-backed Hecate Chat turns, snapshots can include partial
assistant text from the backing task's streamed model turn plus projected task
activity.
Projected task activity uses the same compact vocabulary as Task Detail:
tool calls, approvals, changed files, final-answer artifacts, terminal state,
and a low-level Details group. The stream stays open for an idle or previously
completed session and closes after it observes a new running message reach a
terminal status (`completed`, `failed`, or `cancelled`).

### `POST /hecate/v1/chat/sessions/{id}/cancel`

Cancels the currently running ACP turn for the session.

```json
POST /hecate/v1/chat/sessions/chat_.../cancel
{}
```

Returns `202` when a running turn was signalled. If the session is not
currently running, the endpoint returns `409 invalid_request`.

### `POST /hecate/v1/chat/sessions/{id}/close`

Closes the native ACP agent session while keeping the Hecate chat history.
If a turn is currently running, Hecate cancels and waits briefly before closing
the external session. This uses ACP `session/close` and does not delete the
provider-side native session from the adapter's session list.

### `DELETE /hecate/v1/chat/sessions/{id}`

Deletes a chat session from the configured chat-session backend.
If the session has an active native ACP agent process, Hecate asks the adapter
to delete the native session with ACP `session/delete` and terminates the owned
process as part of deletion. If the adapter does not support `session/delete`,
Hecate falls back to `session/close` before tearing down the process.
If the session is a task-backed Hecate Chat with a non-terminal backing run,
Hecate cancels that run before removing the chat transcript. The backing Task
record remains available from Tasks.

### `POST /hecate/v1/workspace-dialog`

Opens a local folder picker from the gateway process and returns the selected
workspace path. The browser cannot safely expose absolute folder paths on its
own, so this endpoint is intentionally local-runtime-oriented.

```json
POST /hecate/v1/workspace-dialog
{}

→ 200
{
  "object": "workspace_dialog",
  "data": {
    "path": "/Users/alice/project",
    "branch": "main"
  }
}
```

The local gateway uses a cross-platform native-dialog helper for folder
selection. If the host has no usable dialog backend, the endpoint returns
`501` and the UI falls back to a manual path entry. If the operator cancels
the dialog, the endpoint returns `200` with an empty path and the UI keeps the
workspace unchanged. Hecate allows only one folder picker at a time; concurrent
requests return `409 conflict`.

### `POST /hecate/v1/workspace-open`

Opens a validated local workspace directory in an editor, terminal, or file
manager from the gateway process. This is the browser fallback for the Chats
header open-workspace menu; the Tauri app uses its native command path instead.

```json
POST /hecate/v1/workspace-open
{
  "path": "/Users/alice/project",
  "target": "cursor"
}

→ 200
{
  "object": "workspace_open",
  "data": {
    "path": "/Users/alice/project",
    "target": "cursor"
  }
}
```

`target` is one of `vscode`, `vscode_insiders`, `cursor`, `zed`, `finder`,
`terminal`, `iterm2`, or `xcode`. The endpoint accepts only loopback clients,
canonicalizes `path`, requires it to be a directory, and returns `403` for
non-local callers so a remotely hosted Hecate cannot unexpectedly launch apps
on the server machine.

`GET /hecate/v1/whoami` also reports
`capabilities.local_providers_allowed`. Local mode reports `true`; cloud
runtime mode reports `false` by default and `true` only when
`HECATE_REMOTE_ALLOW_LOCAL_PROVIDERS=1` is explicitly set.

Hecate does not expose an embedded terminal over the runtime API. Operators who
need direct host access should use their normal local terminal or the deployment
platform's administrative shell; Hecate-owned command execution belongs in the
task-runtime APIs where approvals, timeouts, output caps, and sandbox policies
can apply.

## Rate-limit headers on chat / messages

Every response from `POST /v1/chat/completions` and `POST /v1/messages` carries three rate-limit headers, regardless of whether rate limiting is enabled (the headers are zero-value when off):

| Header                  | Type         | Meaning                                                       |
| ----------------------- | ------------ | ------------------------------------------------------------- |
| `X-RateLimit-Limit`     | int          | Steady-state refill rate (`HECATE_RATE_LIMIT_RPM`).           |
| `X-RateLimit-Remaining` | int          | Tokens still available in the bucket. Decrements per request. |
| `X-RateLimit-Reset`     | Unix seconds | When the bucket will be full again.                           |

Over-limit requests get `429 Too Many Requests` with the standard error envelope and `code: "rate_limit_exceeded"`. See [Deployment: Rate limiting](../operator/deployment.md#rate-limiting) for the env-var knobs.

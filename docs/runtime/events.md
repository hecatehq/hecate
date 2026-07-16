# Hecate Event Catalog

Reference for every event Hecate emits to its persisted run-event log, surfaced via:

- `GET /hecate/v1/tasks/{id}/runs/{run_id}/events` — per-run JSON list
- `GET /hecate/v1/tasks/{id}/runs/{run_id}/stream` — per-run SSE feed
- `GET /hecate/v1/events` — paginated cross-run list with cursor pagination
- `GET /hecate/v1/events/stream` — long-lived cross-run SSE feed

> Contributing here? Start at [`AGENTS.md`](../../AGENTS.md) for the codebase map and runtime invariants; conventions, workflow, and verification ladders live under [`docs-ai/`](../../docs-ai/README.md).

These are **persisted events** (rows in the `task_state_run_events` table). They are a different stream from OTel spans — spans live in your tracing backend; events live in the gateway's storage tier and are subscriber-friendly. See [telemetry.md](telemetry.md) for OTel.

## Contents

- [Quick reference](#quick-reference)
- [Runtime snapshot payloads](#runtime-snapshot-payloads)
- [Project handoff activity](#project-handoff-activity)
- [Run lifecycle](#run-lifecycle)
- [Approvals](#approvals)
- [Agent loop](#agent-loop)
- [Typed shell tool events](#typed-shell-tool-events)
- [MCP](#mcp)
- [Housekeeping](#housekeeping)
- [Subscribing tips](#subscribing-tips)
- [Related docs](#related-docs)

## Quick reference

| Event type                     | Group                   | When                                                                                 |
| ------------------------------ | ----------------------- | ------------------------------------------------------------------------------------ |
| `run.created`                  | Run lifecycle           | A run record is persisted (status `queued` or `awaiting_approval`)                   |
| `run.queued`                   | Run lifecycle           | Run is enqueued for execution (also re-emitted on resume)                            |
| `run.awaiting_approval`        | Run lifecycle           | A pre-execution approval gate exists; the run is parked                              |
| `run.started`                  | Run lifecycle           | Worker claimed and started executing                                                 |
| `run.finished`                 | Run lifecycle           | Execution finished successfully                                                      |
| `run.failed`                   | Run lifecycle           | Execution failed                                                                     |
| `run.cancelled`                | Run lifecycle           | Run was cancelled (operator or system)                                               |
| `run.resumed_from_event`       | Run lifecycle           | Run is a continuation of a prior run                                                 |
| `gap.run_disconnected`         | Gap / recovery          | Runtime continuity was broken and Hecate recovered by re-queueing or starting fresh  |
| `turn.started`                 | Agent loop              | An `agent_loop` LLM turn is about to call the model                                  |
| `assistant.text_complete`      | Agent loop              | Assistant text content for a turn is available                                       |
| `assistant.tool_call_proposed` | Agent loop              | Assistant proposed a tool call before runtime dispatch                               |
| `assistant.final_answer`       | Agent loop              | Assistant ended the loop without more tool calls                                     |
| `approval.requested`           | Approvals               | An approval gate was created (pre-execution or mid-loop)                             |
| `approval.resolved`            | Approvals               | Operator or system resolved an approval gate                                         |
| `turn.completed`               | Agent loop              | One LLM round-trip in an `agent_loop` run finished                                   |
| `tool.invoked`                 | Typed shell tool events | Shell executor accepted a tool call or direct shell task                             |
| `tool.started`                 | Typed shell tool events | Shell execution is about to start                                                    |
| `tool.shell.command`           | Typed shell tool events | Shell command, cwd, timeout, and sandbox layer selected                              |
| `tool.shell.output_chunk`      | Typed shell tool events | Incremental stdout/stderr chunk from the shell process                               |
| `tool.shell.exited`            | Typed shell tool events | Shell process reported exit metadata                                                 |
| `tool.file.patch`              | Typed file tool events  | A file-writing tool produced an inspectable unified diff artifact                    |
| `tool.file.applied`            | Typed file tool events  | A proposed patch artifact was applied by an operator                                 |
| `tool.file.reverted`           | Typed file tool events  | A previously applied patch artifact was reverted by an operator                      |
| `tool.completed`               | Tool events             | Shell execution or MCP tool call completed                                           |
| `tool.failed`                  | Tool events             | Shell execution or MCP tool call failed                                              |
| `tool.cancelled`               | Typed shell tool events | Shell execution was cancelled                                                        |
| `tool.timed_out`               | Typed shell tool events | Shell execution exceeded its timeout                                                 |
| `policy.tool_blocked`          | Policy                  | A tool call was blocked before execution                                             |
| `task.updated`                 | Housekeeping            | Task metadata changed (e.g. cancellation flushed)                                    |
| `snapshot`                     | Housekeeping            | Internal per-run state-sync frame                                                    |
| `external.event`               | Caller-driven           | Default type for events posted via `POST /hecate/v1/tasks/{id}/runs/{run_id}/events` |

## Wire envelope

JSON list and cross-run SSE endpoints return the agent event protocol v1
envelope:

| Field               | Notes                                                                       |
| ------------------- | --------------------------------------------------------------------------- |
| `schema_version`    | Currently `"1"`                                                             |
| `event_id`          | Stable opaque event id; generated from timestamp + persisted event identity |
| `task_id`, `run_id` | Task/run correlation                                                        |
| `sequence`          | Persisted cursor; pass back as `after_sequence`                             |
| `occurred_at`       | RFC3339Nano UTC                                                             |
| `type`              | One of the event strings in this catalog                                    |
| `data`              | Event-specific JSON object                                                  |

Per-run state SSE (`/hecate/v1/tasks/{id}/runs/{run_id}/stream`) emits
`TaskRunStreamEventData` snapshots optimized for the operator UI. Its
`event_type` field mirrors the persisted event that produced the snapshot.
The stream is a projection over the append-only event log plus current run
storage. Persisted snapshot payloads carry the current stream shape when they
are written; older alpha rows replay as they were stored rather than being
normalized or migrated.
The stream endpoint itself is read-only: live projection frames are not appended
to `run_events`.

## Project handoff activity

Structured project handoffs are not persisted to the task run-event log in V1.
Creating, accepting, superseding, dismissing, or deleting a handoff mutates the
project-work handoff record and updates project activity projections returned
from `GET /hecate/v1/projects/{id}/activity` as `handoff_summary` and
`recent_handoffs` on matching assignment/work-item activity rows. The public
`/hecate/v1/events` feeds remain scoped to task/run events.

## Runtime snapshot payloads

Most events written through the runner event helper (`emitRunEvent`) automatically merge three keys into the persisted `data` map:

| Key         | Type             | Notes                                                               |
| ----------- | ---------------- | ------------------------------------------------------------------- |
| `run`       | `TaskRun`        | Full run record at emit time — id, status, model, costs, timestamps |
| `steps`     | `[]TaskStep`     | Every step recorded for this run so far                             |
| `artifacts` | `[]TaskArtifact` | Every artifact recorded for this run so far                         |

The per-run state SSE decoder can use those keys to reconstruct complete
operator snapshots without a separate fetch. Public event-list and cross-run-feed
responses intentionally strip these runtime snapshot keys and return compact,
protocol-shaped `data` payloads instead.

Store-level terminal transitions keep `run.finished`, `run.failed`,
`run.cancelled`, `task.updated`, and system-generated `approval.resolved` rows
compact, with only their event-specific keys. Operator approve/reject
transitions instead persist `approval.resolved` with authoritative `run`,
`steps`, and `artifacts` snapshot keys in the same transaction as the approval
and run/task mutation; an approved transition's `run.queued` row is
snapshot-bearing too. Public event envelopes still strip those snapshot keys.
The per-run stream projector rebuilds final state from storage when a compact
terminal row does not carry a snapshot.

Caller-driven events (`POST /hecate/v1/tasks/.../events`) instead serialize the rebuilt stream state under a `snapshot` key. The per-run stream projector honors both shapes; public event envelopes strip `snapshot` for the same reason they strip `run` / `steps` / `artifacts`.

The internal persisted row is compact and is mapped to the public envelope on
read:

| Column              | Notes                                           |
| ------------------- | ----------------------------------------------- |
| `sequence`          | Monotonic cursor; pass back as `after_sequence` |
| `task_id`, `run_id` | Both required                                   |
| `event_type`        | One of the strings in this catalog              |
| `event_data`        | JSON map of keys above                          |
| `created_at`        | RFC3339Nano UTC                                 |

## Run lifecycle

### `run.created`

Fires when a new run record is persisted. Status will be `queued` or, if a
pre-execution approval is required, `awaiting_approval`. Resume metadata is
reported by `run.resumed_from_event`, not this event.

### `run.resumed_from_event`

The resume marker on the _new_ run, emitted after `run.created`.

| Extra key               | Type     | Notes                                         |
| ----------------------- | -------- | --------------------------------------------- |
| `from_run_id`           | `string` | Source run id                                 |
| `from_sequence`         | `int64`  | Source event sequence when known              |
| `reason`                | `string` | Operator-supplied rationale                   |
| `retry_from_turn`       | `int`    | Present on retry-from-turn-N                  |
| `prior_cost_micros_usd` | `int64`  | Cost carried into the new run from prior runs |

### `run.awaiting_approval`

A pre-execution approval gate is required (the task config has an approval policy that matched). The run sits in `awaiting_approval` until an operator resolves the gate. Payload carries no extras beyond the auto-merged state.

### `run.queued`

The run is on the queue. Emitted immediately after `run.created` for a fresh run, and again after a paused run is resumed.

| Extra key | Type   | Notes                                                                       |
| --------- | ------ | --------------------------------------------------------------------------- |
| `resume`  | `bool` | Present and `true` on the resume re-queue path; absent on the initial queue |

### `run.started`

A worker claimed the run and started executing. For resumed runs the payload carries hydration cursors.

| Extra key                    | Type     | Notes                                          |
| ---------------------------- | -------- | ---------------------------------------------- |
| `resume_from_run_id`         | `string` | Source run id (resume only)                    |
| `resume_from_step_id`        | `string` | Step the resume picks up after (resume only)   |
| `resume_from_event_sequence` | `int64`  | Event sequence at resume cutover (resume only) |

### `run.finished` / `run.failed`

Terminal status emit. Successful runs emit `run.finished`; failed runs emit
`run.failed`. Public event envelopes expose `data.final_status="completed"` for
`run.finished`; raw persisted terminal payloads carry the `status` / `error`
extras below.

| Extra key | Type     | Notes                                                     |
| --------- | -------- | --------------------------------------------------------- |
| `status`  | `string` | `completed` for `run.finished`; `failed` for `run.failed` |
| `error`   | `string` | Empty for `run.finished`; populated for `run.failed`      |

### `run.cancelled`

The run was cancelled before it could complete. May arrive while the run is still queued (cancellation skipped execution) or while running (cooperative cancel).

| Extra key | Type     | Notes                                                 |
| --------- | -------- | ----------------------------------------------------- |
| `reason`  | `string` | Cancellation reason (operator note or system message) |

### `gap.run_disconnected`

Runtime continuity was broken and Hecate recovered the run automatically. This fires in three situations:

- On gateway boot, when a previous process left a `queued` or `running` run behind and Hecate re-queues it.
- During periodic background reconciliation, when a run is stuck in `running` longer than 3x the queue lease duration and Hecate re-queues it.
- During resume, when checkpoint hydration fails and Hecate starts fresh instead of silently pretending the checkpoint was used.

| Extra key            | Type     | Notes                                                                                     |
| -------------------- | -------- | ----------------------------------------------------------------------------------------- |
| `reason`             | `string` | `boot_reconcile`, `worker_lease_expired`, or `resume_checkpoint_unavailable`              |
| `action`             | `string` | `requeued` or `start_fresh`                                                               |
| `message`            | `string` | Diagnostic message, present for checkpoint hydration failures                             |
| `prior_status`       | `string` | Status before reconciliation (e.g. `running`)                                             |
| `recovered_status`   | `string` | Status after reconciliation (typically `queued`)                                          |
| `recovery_strategy`  | `string` | `"requeue"` — boot-time scan; `"periodic_requeue"` — periodic background reconciler fired |
| `stale_threshold_ms` | `int64`  | Periodic reconciliation threshold, present for `worker_lease_expired`                     |

## Approvals

### `approval.requested`

Two emit sites:

- **Pre-execution gate** — task policy matched before the run started; the run is parked in `awaiting_approval`.
- **Mid-loop gate** — the agent loop tried a tool call (`shell_exec`, `git_exec`, etc.) gated by `HECATE_TASK_APPROVAL_POLICIES` and paused.

Both shapes share these fields.

| Extra key       | Type     | Notes                                                                                                                                                                                                                     |
| --------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `approval_id`   | `string` | The new approval record id                                                                                                                                                                                                |
| `kind`          | `string` | Approval type. One of `shell_command`, `git_exec`, `file_write`, `network_egress` (pre-execution gates), or `agent_loop_tool_call` (mid-loop gate). See [`runtime-api.md#approval-kinds`](runtime-api.md#approval-kinds). |
| `status`        | `string` | `pending` at creation                                                                                                                                                                                                     |
| `policy_reason` | `string` | Human-readable policy reason that caused the gate                                                                                                                                                                         |
| `requested_by`  | `string` | Principal that created the approval, when known                                                                                                                                                                           |
| `step_id`       | `string` | Present for step-scoped approvals                                                                                                                                                                                         |

### `approval.resolved`

The gate reached a terminal decision. After approve, the run re-queues; after
reject, the run and task terminate `cancelled` with
`last_error="approval rejected"`; after cancellation, the run terminates
`cancelled` and the approval record is no longer actionable.

| Extra key     | Type     | Notes                                                             |
| ------------- | -------- | ----------------------------------------------------------------- |
| `approval_id` | `string` | Resolved approval id                                              |
| `decision`    | `string` | `approved`, `rejected`, or `cancelled`                            |
| `by`          | `string` | Principal or subsystem that resolved the approval                 |
| `comment`     | `string` | Operator- or system-supplied resolution note                      |
| `scope`       | `string` | Currently `once`; persistent always-allow is separate policy work |
| `kind`        | `string` | Approval type                                                     |
| `status`      | `string` | Mirrors `decision` for compatibility with approval records        |

## Agent loop

Fresh LLM turns follow this persisted-event shape: `turn.started`, zero or more
`assistant.text_complete` / `assistant.tool_call_proposed` events,
optionally `assistant.final_answer`, then one `turn.completed` emitted from the
turn-cost record. Resume-after-approval dispatches do not call the model again,
so they do not emit a new `turn.started` or `turn.completed`; the approved tool
calls continue from the assistant message already saved in the conversation
artifact.

### `turn.started`

Emitted immediately before an `agent_loop` LLM request. Resume-after-approval
turns that only dispatch already-approved tool calls do not emit this event
because no model call is made.

| Extra key               | Type     | Notes                                                                                                    |
| ----------------------- | -------- | -------------------------------------------------------------------------------------------------------- |
| `turn_index`            | `int`    | 1-indexed turn number within this run                                                                    |
| `model`                 | `string` | Requested model for this run                                                                             |
| `provider`              | `string` | Provider hint when pinned by the task                                                                    |
| `input_tokens_estimate` | `int`    | Cheap local estimate for operator/debug rendering; provider usage remains authoritative after completion |

### `assistant.text_complete`

Emitted when the assistant response carries text content. Hecate does not yet
stream internal agent-loop text deltas into the persisted event stream, so this
is the full text block for the turn.

| Extra key     | Type     | Notes                                             |
| ------------- | -------- | ------------------------------------------------- |
| `turn_index`  | `int`    | 1-indexed turn number within this run             |
| `block_index` | `int`    | Currently `0`; reserved for multi-block rendering |
| `text`        | `string` | Assistant text                                    |

### `assistant.tool_call_proposed`

Emitted once per assistant tool call before policy gates or runtime dispatch.
The later `approval.*`, `tool.*`, and `policy.*` events describe what Hecate
did with that proposal.

| Extra key      | Type     | Notes                                                               |
| -------------- | -------- | ------------------------------------------------------------------- |
| `turn_index`   | `int`    | 1-indexed turn number within this run                               |
| `tool_call_id` | `string` | Provider tool-call id                                               |
| `tool_name`    | `string` | Requested tool name                                                 |
| `input`        | `object` | Parsed tool arguments when valid JSON, otherwise `{ "raw": "..." }` |

### `assistant.final_answer`

Emitted when the assistant response contains no tool calls and the agent loop
can finish.

| Extra key    | Type     | Notes                                 |
| ------------ | -------- | ------------------------------------- |
| `turn_index` | `int`    | 1-indexed turn number within this run |
| `summary`    | `string` | Final assistant text                  |

### `turn.completed`

Emitted once per LLM round-trip in an `agent_loop` run. The richest cost-tracking payload in the catalog.

| Extra key                         | Type     | Notes                                                                                              |
| --------------------------------- | -------- | -------------------------------------------------------------------------------------------------- |
| `turn_index`                      | `int`    | 1-indexed turn number within this run                                                              |
| `step_id`                         | `string` | The assistant model step produced this turn                                                        |
| `cost_micros_usd`                 | `int64`  | This turn's LLM spend in micro-USD                                                                 |
| `run_cumulative_cost_micros_usd`  | `int64`  | Running total across this run only                                                                 |
| `task_cumulative_cost_micros_usd` | `int64`  | Running total across the entire resume chain (this run + every prior run via `PriorCostMicrosUSD`) |
| `tool_calls`                      | `int`    | Tool calls the assistant emitted on this turn                                                      |

The per-turn figure is also stamped on the matching model step's `OutputSummary.cost_micros_usd` so the run-replay UI surfaces it without subscribing here. See [agent-runtime.md](agent-runtime.md#cost-tracking) for the full cost model.

These rows are the only event type pruned by the retention worker (`turn_events` subsystem) — they accumulate fast on long agent runs. Other event types are kept indefinitely. See `HECATE_RETENTION_TURN_EVENTS_*` in `.env.example`.

## Typed shell tool events

These events implement the shell-tool portion of the draft
[agent event protocol v1 candidate](../design/candidates/event-protocol-v1.md). They are emitted by
the shared shell executor for both direct `execution_kind=shell` tasks and
`agent_loop` `shell_exec` tool calls. The old `step.*` and `artifact.*`
persisted run events are no longer emitted; step and artifact records still
persist as state, and subscribers should use typed lifecycle events plus the
auto-merged snapshots on each emitted event.

### `tool.invoked` / `tool.started`

Generic shell tool lifecycle markers.

| Extra key                           | Type     | Notes                                                                                  |
| ----------------------------------- | -------- | -------------------------------------------------------------------------------------- |
| `tool_call_id`                      | `string` | Model tool-call id for `agent_loop`; direct shell tasks fall back to the shell step id |
| `tool_name`                         | `string` | Usually `shell_exec` for agent-loop tool calls or `shell` for direct shell tasks       |
| `kind`                              | `string` | Always `shell` for these shell-tool events                                             |
| `hecate.sandbox.wrapper.kind`       | `string` | Detected OS isolation wrapper                                                          |
| `hecate.sandbox.network.enabled`    | `bool`   | Whether this task allowed sandbox network access                                       |
| `hecate.sandbox.read_only`          | `bool`   | Whether the sandbox policy is read-only                                                |
| `hecate.sandbox.output_limit.bytes` | `int64`  | Effective combined stdout/stderr output cap                                            |

### `tool.shell.command`

The normalized command execution plan.

| Extra key                       | Type       | Notes                                                                           |
| ------------------------------- | ---------- | ------------------------------------------------------------------------------- |
| `tool_call_id`                  | `string`   | Correlates with the generic lifecycle events                                    |
| `argv`                          | `[]string` | Effective shell invocation shape, currently `["sh", "-lc", <command>]`          |
| `cwd`                           | `string`   | Resolved working directory passed to the sandbox executor                       |
| `env_keys`                      | `[]string` | Sanitized environment keys the event intentionally exposes                      |
| `sandbox_layer`                 | `string`   | Detected OS isolation wrapper: `bwrap`, `sandbox-exec`, or `none`               |
| `timeout_ms`                    | `int`      | Wall-clock timeout for the command                                              |
| `command_string`                | `string`   | Human-readable shell command string                                             |
| `hecate.tool.working_directory` | `string`   | Working directory as an OTel-shaped event attribute; not promoted to span attrs |
| `hecate.tool.timeout_ms`        | `int`      | Timeout as an OTel-shaped event/span attribute                                  |

### `tool.shell.output_chunk`

Incremental process output.

| Extra key      | Type     | Notes                                       |
| -------------- | -------- | ------------------------------------------- |
| `tool_call_id` | `string` | Correlates with the command event           |
| `stream`       | `string` | `stdout` or `stderr`                        |
| `data`         | `string` | Raw chunk text                              |
| `byte_offset`  | `int`    | Offset within that stream before this chunk |

### `tool.shell.exited`

Process exit metadata. This event is skipped when sandbox policy denies the
command before a child process starts.

| Extra key                      | Type             | Notes                                                                          |
| ------------------------------ | ---------------- | ------------------------------------------------------------------------------ |
| `tool_call_id`                 | `string`         | Correlates with the command event                                              |
| `exit_code`                    | `int`            | Process exit code, or `-1` when the process did not produce a normal exit code |
| `signal`                       | `string \| null` | Reserved for future signal reporting; currently `null`                         |
| `stdout_bytes`                 | `int`            | Final stdout byte count                                                        |
| `stderr_bytes`                 | `int`            | Final stderr byte count                                                        |
| `truncated`                    | `bool`           | True when the sandbox output cap stopped the command                           |
| `hecate.tool.exit_code`        | `int`            | Exit code as an OTel-shaped event/span attribute                               |
| `hecate.tool.stdout.bytes`     | `int`            | Stdout byte count as an OTel-shaped event/span attribute                       |
| `hecate.tool.stderr.bytes`     | `int`            | Stderr byte count as an OTel-shaped event/span attribute                       |
| `hecate.tool.timed_out`        | `bool`           | True when the command hit its wall-clock timeout                               |
| `hecate.tool.cancelled`        | `bool`           | True when execution was cancelled by context                                   |
| `hecate.tool.output_truncated` | `bool`           | True when the sandbox output cap stopped the command                           |

### `tool.completed` / `tool.failed` / `tool.cancelled` / `tool.timed_out`

Terminal shell lifecycle marker.

| Extra key                      | Type     | Notes                                                        |
| ------------------------------ | -------- | ------------------------------------------------------------ |
| `tool_call_id`                 | `string` | Correlates with prior shell events                           |
| `tool_name`                    | `string` | Same value as `tool.invoked`                                 |
| `kind`                         | `string` | Always `shell`                                               |
| `duration_ms`                  | `int64`  | Wall-clock duration from shell step start to terminal result |
| `summary`                      | `string` | Human-readable terminal summary                              |
| `error`                        | `string` | Present on failed/cancelled/timed-out shell executions       |
| `after_ms`                     | `int`    | Present on `tool.timed_out`                                  |
| `hecate.tool.exit_code`        | `int`    | Exit code as an OTel-shaped event/span attribute             |
| `hecate.tool.stdout.bytes`     | `int`    | Stdout byte count as an OTel-shaped event/span attribute     |
| `hecate.tool.stderr.bytes`     | `int`    | Stderr byte count as an OTel-shaped event/span attribute     |
| `hecate.tool.timed_out`        | `bool`   | True when the command hit its wall-clock timeout             |
| `hecate.tool.cancelled`        | `bool`   | True when execution was cancelled by context                 |
| `hecate.tool.output_truncated` | `bool`   | True when the sandbox output cap stopped the command         |

## Policy tool blocks

### `policy.tool_blocked`

Emitted when Hecate deliberately refuses a tool call without contacting its
execution target. Current producers are:

- any native or namespaced MCP call returned for a task whose Agent Preset
  snapshot explicitly disables tools (`policy=agent_preset_tools`);
- a native `http_request` or `web_search` call returned by an upstream even
  though its Agent Preset snapshot disables network;
- a broad native process, direct-write, or terminal call returned for a
  read-only task; and
- an MCP tool whose server config has `approval_policy=block`.

The event is an audit signal rather than a runtime failure. Its task step uses
`status=completed`, `phase=policy`, and `result=denied`, while the agent still
receives a tool-error result and may choose a permitted path on its next turn.
Common payload fields are:

| Extra key      | Type     | Notes                                                                               |
| -------------- | -------- | ----------------------------------------------------------------------------------- |
| `tool_call_id` | `string` | Assistant tool-call correlation id                                                  |
| `tool_name`    | `string` | Native or namespaced MCP tool name                                                  |
| `kind`         | `string` | `builtin` for native policy, `mcp` for a namespaced MCP attempt                     |
| `result`       | `string` | Always `blocked`                                                                    |
| `policy`       | `string` | Policy key on the corresponding task step                                           |
| `reason`       | `string` | The runtime, Agent Preset, task sandbox, or MCP-server policy that refused the call |

## MCP

Generic `tool.*` and `policy.*` events form the audit trail for namespaced MCP
tool attempts in `agent_loop` runs. Together they cover configured calls that
reach an upstream (including upstream-side tool errors), protocol failures,
MCP-server policy blocks, and preset-wide blocks before MCP startup. A blocked
attempt may be derived solely from an unexpected `mcp__<server>__<tool>` name
returned by the model; its namespace fields do not by themselves prove that a
server or tool was configured. See [mcp.md](mcp.md#hecate-as-mcp-client) for the
underlying configuration and policy model.

MCP events carry the same shared payload shape:

| Extra key      | Type     | Notes                                                                                                                                                                         |
| -------------- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `tool_call_id` | `string` | Correlates with the assistant tool call                                                                                                                                       |
| `tool_name`    | `string` | Full OpenAI-compatible tool name, e.g. `mcp__filesystem__read_file`                                                                                                           |
| `kind`         | `string` | Always `mcp` for namespaced MCP tool events                                                                                                                                   |
| `mcp_server`   | `string` | Parsed `<server>` segment of `mcp__<server>__<tool>`. Normally a configured alias; preset-wide blocks may record an unexpected, unconfigured namespace returned by the model. |
| `mcp_tool`     | `string` | Parsed `<tool>` segment. For configured calls this is the upstream tool name; preset-wide blocks may record an unexpected value returned by the model.                        |
| `result`       | `string` | One of `dispatched`, `tool_error`, `failed`, `blocked` — finer-grained than the event-type split                                                                              |
| `duration_ms`  | `int64`  | Wall-clock processing time from dispatcher entry to local denial or upstream result                                                                                           |
| `error`        | `string` | Present on `tool.failed`, `policy.tool_blocked`, and when applicable on `tool.completed` with `result=tool_error`                                                             |
| `reason`       | `string` | Present on `policy.tool_blocked`                                                                                                                                              |
| `policy`       | `string` | Present on `policy.tool_blocked`: `mcp_approval_policy` or `agent_preset_tools`                                                                                               |

### `tool.completed` for MCP

Emitted on every dispatch that reached the upstream MCP server, regardless of whether the upstream returned `is_error=false` (clean success) or `is_error=true` (tool-level failure with diagnostic text). The `result` payload key disambiguates the two: `dispatched` for clean, `tool_error` for upstream-marked failures. Operators chart `tool_error / (dispatched + tool_error)` to spot servers that are answering but unhappy.

### `tool.failed` for MCP

Protocol-level failure before a result was in hand: transport closed, RPC error, unknown-tool routing miss. The agent loop forwards the diagnostic as a tool-error message to the LLM (the run does not fail), but the event is the audit signal a dashboard would alert on.

### `policy.tool_blocked` for MCP

Emitted for either of two hard-refusal paths:

- the matching task `mcp_servers[]` entry has `approval_policy=block`
  (`policy=mcp_approval_policy`); or
- the task's Agent Preset snapshot explicitly disables every tool
  (`policy=agent_preset_tools`).

The upstream is never contacted; the LLM sees a tool error suggesting it pick
a different path. This is distinct from `tool.failed` so operators can alert on
failed execution without their pages firing on a legitimate policy block. It
is also distinct from `approval.requested`: neither policy pauses the run.

## Typed file tool events

### `tool.file.patch`

Emitted when `execution_kind=file` or an `agent_loop` file-writing tool (`file_write` / `file_edit` / `apply_patch`) writes or proposes a file change. Hecate stores an inline `patch` artifact containing a unified diff of the before/after file contents, then emits this event so operator UIs and CLIs can render the edit without re-running `git diff` against a moving workspace.

| Extra key                          | Type     | Notes                                                                                    |
| ---------------------------------- | -------- | ---------------------------------------------------------------------------------------- |
| `tool_call_id`                     | `string` | Assistant tool call id, or the file step id for direct file tasks                        |
| `tool_name`                        | `string` | `file_write` / `file_edit` / `apply_patch` for agent tools; `file` for direct file tasks |
| `kind`                             | `string` | Always `file`                                                                            |
| `operation`                        | `string` | `write`, `append`, `propose`, or `apply_patch` section kind (`add`, `update`, `delete`)  |
| `path`                             | `string` | Resolved path written by the sandbox                                                     |
| `artifact_id`                      | `string` | Patch artifact id                                                                        |
| `bytes_written`                    | `int`    | Bytes written by the file operation                                                      |
| `diff_bytes`                       | `int64`  | Patch body size                                                                          |
| `before_existed`                   | `bool`   | Whether the file existed before the write                                                |
| `artifact_status`                  | `string` | `applied`, `proposed`, or `reverted`                                                     |
| `hecate.tool.file.operation`       | `string` | File operation as an OTel-shaped event/span attribute                                    |
| `hecate.tool.file.bytes_written`   | `int`    | Bytes written as an OTel-shaped event/span attribute                                     |
| `hecate.tool.file.diff_bytes`      | `int64`  | Patch size as an OTel-shaped event/span attribute                                        |
| `hecate.tool.file.before_existed`  | `bool`   | Pre-write existence as an OTel-shaped event/span attribute                               |
| `hecate.tool.file.artifact_status` | `string` | Patch artifact status as an OTel-shaped event/span attribute                             |

### `tool.file.applied`

Emitted when an operator calls the patch apply endpoint for a proposed patch artifact. The file is written from Hecate's own patch artifact and the artifact status changes from `proposed` to `applied`. Apply is conflict-checked: if the target file no longer matches the captured before-content, the endpoint returns `409` and does not write.

| Extra key         | Type     | Notes                  |
| ----------------- | -------- | ---------------------- |
| `artifact_id`     | `string` | Patch artifact id      |
| `path`            | `string` | Workspace path written |
| `artifact_status` | `string` | `applied`              |

### `tool.file.reverted`

Emitted when an operator calls the patch revert endpoint. The file is restored from Hecate's own patch artifact and the artifact status changes from `applied` to `reverted`. The revert endpoint first verifies that the current file still matches the patch artifact's captured after-content; if the file drifted, the request returns `409 conflict` and no revert event is emitted.

If the current workspace file no longer matches the patch artifact's expected after-content (or the file exists when reverting a patch that created a new file), the revert endpoint returns `409 Conflict`, leaves the workspace unchanged, and does not emit `tool.file.reverted`.

| Extra key         | Type     | Notes                                                                                         |
| ----------------- | -------- | --------------------------------------------------------------------------------------------- |
| `artifact_id`     | `string` | Patch artifact id                                                                             |
| `path`            | `string` | Workspace path restored or removed                                                            |
| `artifact_status` | `string` | `reverted`                                                                                    |
| `before_existed`  | `bool`   | Whether revert restored old content (`true`) or removed a file created by the patch (`false`) |

## Housekeeping

### `task.updated`

Emitted when task-scoped metadata changed in a way that affects the run's view (e.g. cancellation flush, resume reset). The auto-merged `run` reflects post-update state. No extra keys.

### `snapshot`

Legacy alpha builds wrote one of these when the per-run stream detected a state change between heartbeats. Current builds keep the stream endpoint read-only, so new live projection frames are not persisted as `snapshot` events. Existing rows remain distinguishable from real lifecycle events by `type=snapshot` in JSON event lists and `event_type=snapshot` in per-run state SSE; the `data.snapshot` key holds the rebuilt `TaskRunStreamEventData` JSON.

| Extra key  | Type     | Notes                                                            |
| ---------- | -------- | ---------------------------------------------------------------- |
| `snapshot` | `object` | Full `TaskRunStreamEventData` — run, steps, artifacts, approvals |

### `external.event`

The default event type when a caller posts to `POST /hecate/v1/tasks/{id}/runs/{run_id}/events` without specifying `type`. Use this to integrate human-in-the-loop signals or external systems into the run timeline without inventing new event-type strings.

| Extra key  | Type     | Notes                                              |
| ---------- | -------- | -------------------------------------------------- |
| `step_id`  | `string` | Optional caller-supplied step correlation          |
| `status`   | `string` | Optional caller-supplied status hint               |
| `note`     | `string` | Optional caller-supplied note                      |
| `snapshot` | `object` | Auto-injected stream state at the moment of append |

Callers can also pass an arbitrary `data` map alongside; those keys are merged into the event's `data` field at the same level as the auto-injected ones.

## Subscribing tips

- **Filtering** — `event_type` accepts a comma-separated allowlist; multiple values OR within the slice. `task_id` is a single id (not csv). Filters AND across types.
- **Cursor pagination** — every response carries `next_after_sequence`; pass it back as `after_sequence` on the next call. `after_sequence` is strictly-greater, so a client passes the last sequence it saw.
- **Reconnect** — both SSE feeds support resume via the `Last-Event-ID` header (id is the global sequence).

## Related docs

- [runtime-api.md](runtime-api.md#public-events-feed) — endpoint shape, query params, access model
- [agent-runtime.md](agent-runtime.md#cost-tracking) — cost-model details for `turn.completed`
- [telemetry.md](telemetry.md) — OTel spans / metrics (a different stream from this catalog)
- [architecture.md](../contributor/architecture.md) — where events fit in the request lifecycle

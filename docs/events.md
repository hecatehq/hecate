# Hecate Event Catalog

Reference for every event Hecate emits to its persisted run-event log, surfaced via:

- `GET /v1/tasks/{id}/runs/{run_id}/stream` — per-run SSE feed
- `GET /v1/events` — paginated cross-run list with cursor pagination
- `GET /v1/events/stream` — long-lived cross-run SSE feed

> Contributing here? Start at [`AGENTS.md`](../AGENTS.md) for the codebase map and runtime invariants; conventions, workflow, and verification ladders live under [`ai/`](../ai/README.md).

These are **persisted events** (rows in the `task_state_run_events` table). They are a different stream from OTel spans — spans live in your tracing backend; events live in the gateway's storage tier and are subscriber-friendly. See [telemetry.md](telemetry.md) for OTel.

## Contents

- [Quick reference](#quick-reference)
- [Common payload structure](#common-payload-structure)
- [Run lifecycle](#run-lifecycle)
- [Approvals](#approvals)
- [Agent loop](#agent-loop)
- [Typed shell tool events](#typed-shell-tool-events)
- [MCP](#mcp)
- [Housekeeping](#housekeeping)
- [Subscribing tips](#subscribing-tips)
- [Related docs](#related-docs)

## Quick reference

| Event type | Group | When |
|---|---|---|
| `run.created` | Run lifecycle | A run record is persisted (status `queued` or `awaiting_approval`) |
| `run.queued` | Run lifecycle | Run is enqueued for execution (also re-emitted on resume) |
| `run.awaiting_approval` | Run lifecycle | A pre-execution approval gate exists; the run is parked |
| `run.running` | Run lifecycle | Worker claimed and started executing |
| `run.finished` | Run lifecycle | Pre-resume checkpoint after a paused phase finishes |
| `run.completed` | Run lifecycle | Execution finished successfully |
| `run.failed` | Run lifecycle | Execution failed |
| `run.cancelled` | Run lifecycle | Run was cancelled (operator or system) |
| `run.resumed` | Run lifecycle | Run is a continuation of a prior run |
| `run.resume_requested` | Run lifecycle | Marker on the *prior* run that a resume started |
| `run.throttled_concurrency` | Run lifecycle | Run held back by global concurrency limit |
| `run.resume_checkpoint_failed` | Run lifecycle | Resume hydration failed; run will start fresh |
| `run.reconciled_restart_requeued` | Run lifecycle | Stalled run recovered and re-queued by reconciler (boot-time scan or periodic background check) |
| `approval.requested` | Approvals | An approval gate was created (pre-execution or mid-loop) |
| `approval.approved` | Approvals | Operator approved a gate |
| `approval.rejected` | Approvals | Operator rejected a gate (terminates the run) |
| `agent.turn.completed` | Agent loop | One LLM round-trip in an `agent_loop` run finished |
| `tool.invoked` | Typed shell tool events | Shell executor accepted a tool call or direct shell task |
| `tool.started` | Typed shell tool events | Shell execution is about to start |
| `tool.shell.command` | Typed shell tool events | Shell command, cwd, timeout, and sandbox layer selected |
| `tool.shell.output_chunk` | Typed shell tool events | Incremental stdout/stderr chunk from the shell process |
| `tool.shell.exited` | Typed shell tool events | Shell process reported exit metadata |
| `tool.completed` | Typed shell tool events | Shell execution completed successfully |
| `tool.failed` | Typed shell tool events | Shell execution failed |
| `tool.cancelled` | Typed shell tool events | Shell execution was cancelled |
| `tool.timed_out` | Typed shell tool events | Shell execution exceeded its timeout |
| `orchestrator.mcp.tool.dispatched` | MCP | Agent loop dispatched a tool call to an external MCP server (`is_error=false` OR upstream `is_error=true`) |
| `orchestrator.mcp.tool.failed` | MCP | Protocol-level failure (transport closed, RPC error, unknown tool) before a result was returned |
| `orchestrator.mcp.tool.blocked` | MCP | The configured `approval_policy=block` short-circuited the call before it reached the upstream |
| `task.updated` | Housekeeping | Task metadata changed (e.g. cancellation flushed) |
| `snapshot` | Housekeeping | Per-run SSE handler periodically writes a state snapshot |
| `external.event` | Caller-driven | Default type for events posted via `POST /v1/tasks/{id}/runs/{run_id}/events` |

## Common payload structure

Every event written by the orchestrator (`emitRunEvent`) automatically merges three keys into its `data` map:

| Key | Type | Notes |
|---|---|---|
| `run` | `TaskRun` | Full run record at emit time — id, status, model, costs, timestamps |
| `steps` | `[]TaskStep` | Every step recorded for this run so far |
| `artifacts` | `[]TaskArtifact` | Every artifact recorded for this run so far |

Subscribers can therefore reconstruct a complete state snapshot from any single event without a separate fetch — at the cost of payload size. Only `agent.turn.completed` adds extra cost-specific keys on top; the others list event-specific keys below.

Caller-driven events (`POST /v1/tasks/.../events`) instead serialize the rebuilt stream state under a `snapshot` key. The decoder in the per-run SSE handler honors both shapes.

The persisted column shape is the same for every event:

| Column | Notes |
|---|---|
| `sequence` | Monotonic global cursor; pass back as `after_sequence` |
| `task_id`, `run_id` | Both required |
| `event_type` | One of the strings in this catalog |
| `event_data` | JSON map of keys above |
| `request_id`, `trace_id` | Correlation handles into OTel + gateway logs |
| `created_at` | RFC3339Nano UTC |

## Run lifecycle

### `run.created`

Fires when a new run record is persisted. Status will be `queued` or, if a pre-execution approval is required, `awaiting_approval`.

| Extra key | Type | Notes |
|---|---|---|
| `resumed_from_run_id` | `string` | Set when this run continues a prior run via resume / retry-from-turn |
| `resume_reason` | `string` | Operator-supplied resume rationale |
| `retry_from_turn` | `int` | Set on retry-from-turn-N runs; the (1-indexed) turn the new run begins at |

### `run.resume_requested`

Marker emitted on the *prior* run when a new resumed run is created. Use this to thread "this run was continued as X" affordances in dashboards.

| Extra key | Type | Notes |
|---|---|---|
| `new_run_id` | `string` | The id of the resumed run |
| `reason` | `string` | Operator-supplied rationale |
| `retry_from_turn` | `int` | Present on retry-from-turn-N |

### `run.resumed`

The mirror event on the *new* run, emitted right after `run.created`.

| Extra key | Type | Notes |
|---|---|---|
| `resumed_from_run_id` | `string` | Source run id |
| `reason` | `string` | Operator-supplied rationale |
| `retry_from_turn` | `int` | Present on retry-from-turn-N |

### `run.awaiting_approval`

A pre-execution approval gate is required (the task config has an approval policy that matched). The run sits in `awaiting_approval` until an operator resolves the gate. Payload carries no extras beyond the auto-merged state.

### `run.queued`

The run is on the queue. Emitted immediately after `run.created` for a fresh run, and again after a paused run is resumed.

| Extra key | Type | Notes |
|---|---|---|
| `resume` | `bool` | Present and `true` on the resume re-queue path; absent on the initial queue |

### `run.running`

A worker claimed the run and started executing. For resumed runs the payload carries hydration cursors.

| Extra key | Type | Notes |
|---|---|---|
| `resume_from_run_id` | `string` | Source run id (resume only) |
| `resume_from_step_id` | `string` | Step the resume picks up after (resume only) |
| `resume_from_event_sequence` | `int64` | Event sequence at resume cutover (resume only) |

### `run.finished`

The orchestrator emits this when a paused phase wraps up before re-queuing for the next phase. **It does NOT mean the run is terminal** — see `run.completed` / `run.failed` / `run.cancelled` for that.

| Extra key | Type | Notes |
|---|---|---|
| `status` | `string` | The run's status at the phase boundary |

### `run.completed` / `run.failed`

Terminal status emit. The exact event type is `run.<status>` where `<status>` is the run's terminal status — currently `completed` or `failed`.

| Extra key | Type | Notes |
|---|---|---|
| `error` | `string` | Empty for `completed`; populated for `failed` |

### `run.cancelled`

The run was cancelled before it could complete. May arrive while the run is still queued (cancellation skipped execution) or while running (cooperative cancel).

| Extra key | Type | Notes |
|---|---|---|
| `reason` | `string` | Cancellation reason (operator note or system message) |

### `run.throttled_concurrency`

The run was held back because the global concurrent-run limit was already hit. The runner re-tries claim later; this event surfaces the throttle for observability.

| Extra key | Type | Notes |
|---|---|---|
| `limit` | `int` | The configured max-concurrent value |

### `run.resume_checkpoint_failed`

A resume attempted to hydrate the prior run's conversation but the checkpoint blob was unreadable / corrupt. The run continues without the prior context (effectively a fresh start).

| Extra key | Type | Notes |
|---|---|---|
| `error` | `string` | Why hydration failed |

### `run.reconciled_restart_requeued`

The reconciler recovered a stalled run and pushed it back onto the queue. This fires in two situations: on gateway boot (scanning for runs left in `running` state from a previous process), and during periodic background reconciliation (runs stuck in `running` longer than 3× the queue lease duration). Use `recovery_strategy` to distinguish the two. Use this event to detect runs that were saved automatically vs. ones the operator manually requeued.

| Extra key | Type | Notes |
|---|---|---|
| `prior_status` | `string` | Status before reconciliation (e.g. `running`) |
| `recovered_status` | `string` | Status after reconciliation (typically `queued`) |
| `recovery_strategy` | `string` | `"requeue"` — boot-time scan; `"periodic_requeue"` — periodic background reconciler fired |

## Approvals

### `approval.requested`

Two emit sites:

- **Pre-execution gate** — task policy matched before the run started; the run is parked in `awaiting_approval`.
- **Mid-loop gate** — the agent loop tried a tool call (`shell_exec`, `git_exec`, etc.) gated by `GATEWAY_TASK_APPROVAL_POLICIES` and paused.

Both shapes share these fields. The mid-loop variant uses `approval_kind` instead of `kind` (legacy naming difference; both are equivalent strings like `agent_loop_tool_call`).

| Extra key | Type | Notes |
|---|---|---|
| `approval_id` | `string` | The new approval record id |
| `kind` / `approval_kind` | `string` | Approval type. One of `shell_command`, `git_exec`, `file_write`, `network_egress` (pre-execution gates), or `agent_loop_tool_call` (mid-loop gate). See [`runtime-api.md#approval-kinds`](runtime-api.md#approval-kinds). |
| `status` | `string` | `pending` at creation |

### `approval.approved` / `approval.rejected`

The operator (or admin) resolved the gate. Event type is `approval.<status>`. After approve, the run re-queues; after reject, the run terminates `failed`.

| Extra key | Type | Notes |
|---|---|---|
| `approval_id` | `string` | Resolved approval id |
| `kind` | `string` | Approval type |
| `status` | `string` | Mirrors the event-type suffix |
| `note` | `string` | Operator-supplied resolution note |

## Agent loop

### `agent.turn.completed`

Emitted once per LLM round-trip in an `agent_loop` run. The richest cost-tracking payload in the catalog.

| Extra key | Type | Notes |
|---|---|---|
| `turn` | `int` | 1-indexed turn number within this run |
| `step_id` | `string` | The assistant model step produced this turn |
| `cost_micros_usd` | `int64` | This turn's LLM spend in micro-USD |
| `run_cumulative_cost_micros_usd` | `int64` | Running total across this run only |
| `task_cumulative_cost_micros_usd` | `int64` | Running total across the entire resume chain (this run + every prior run via `PriorCostMicrosUSD`) |
| `tool_call_count` | `int` | Tool calls the assistant emitted on this turn |

The per-turn figure is also stamped on the matching model step's `OutputSummary.cost_micros_usd` so the run-replay UI surfaces it without subscribing here. See [agent-runtime.md](agent-runtime.md#cost-tracking) for the full cost model.

These rows are the only event type pruned by the retention worker (`turn_events` subsystem) — they accumulate fast on long agent runs. Other event types are kept indefinitely. See `GATEWAY_RETENTION_TURN_EVENTS_*` in `.env.example`.

## Typed shell tool events

These events are the first implemented slice of the draft
[agent event protocol v1 candidate](event-protocol-v1.md). They are emitted by
the shared shell executor for both direct `execution_kind=shell` tasks and
`agent_loop` `shell_exec` tool calls. The old `step.*` and `artifact.*`
persisted run events are no longer emitted; step and artifact records still
persist as state, and subscribers should use typed lifecycle events plus the
auto-merged snapshots on each emitted event.

### `tool.invoked` / `tool.started`

Generic shell tool lifecycle markers.

| Extra key | Type | Notes |
|---|---|---|
| `tool_call_id` | `string` | Model tool-call id for `agent_loop`; direct shell tasks fall back to the shell step id |
| `tool_name` | `string` | Usually `shell_exec` for agent-loop tool calls or `shell` for direct shell tasks |
| `kind` | `string` | Always `shell` for this implemented slice |

### `tool.shell.command`

The normalized command execution plan.

| Extra key | Type | Notes |
|---|---|---|
| `tool_call_id` | `string` | Correlates with the generic lifecycle events |
| `argv` | `[]string` | Effective shell invocation shape, currently `["sh", "-lc", <command>]` |
| `cwd` | `string` | Resolved working directory passed to the sandbox executor |
| `env_keys` | `[]string` | Sanitized environment keys the event intentionally exposes |
| `sandbox_layer` | `string` | Detected OS isolation wrapper: `bwrap`, `sandbox-exec`, or `none` |
| `timeout_ms` | `int` | Wall-clock timeout for the command |
| `command_string` | `string` | Human-readable shell command string |

### `tool.shell.output_chunk`

Incremental process output.

| Extra key | Type | Notes |
|---|---|---|
| `tool_call_id` | `string` | Correlates with the command event |
| `stream` | `string` | `stdout` or `stderr` |
| `data` | `string` | Raw chunk text |
| `byte_offset` | `int` | Offset within that stream before this chunk |

### `tool.shell.exited`

Process exit metadata. This event is skipped when sandbox policy denies the
command before a child process starts.

| Extra key | Type | Notes |
|---|---|---|
| `tool_call_id` | `string` | Correlates with the command event |
| `exit_code` | `int` | Process exit code, or `-1` when the process did not produce a normal exit code |
| `signal` | `string \| null` | Reserved for future signal reporting; currently `null` |
| `stdout_bytes` | `int` | Final stdout byte count |
| `stderr_bytes` | `int` | Final stderr byte count |
| `truncated` | `bool` | True when the sandbox output cap stopped the command |

### `tool.completed` / `tool.failed` / `tool.cancelled` / `tool.timed_out`

Terminal shell lifecycle marker.

| Extra key | Type | Notes |
|---|---|---|
| `tool_call_id` | `string` | Correlates with prior shell events |
| `tool_name` | `string` | Same value as `tool.invoked` |
| `kind` | `string` | Always `shell` |
| `duration_ms` | `int64` | Wall-clock duration from shell step start to terminal result |
| `summary` | `string` | Human-readable terminal summary |
| `error` | `string` | Present on failed/cancelled/timed-out shell executions |
| `after_ms` | `int` | Present on `tool.timed_out` |

## MCP

Three events form the audit trail for external MCP tool calls in `agent_loop` runs. Together they cover every dispatch outcome the loop's MCP dispatcher produces — successful calls (including upstream-side tool errors), protocol failures, and policy-blocked calls. See [mcp.md](mcp.md#hecate-as-mcp-client) for the underlying configuration and policy model.

All three carry the same shared payload shape:

| Extra key | Type | Notes |
|---|---|---|
| `server` | `string` | Operator-chosen alias from the task's `mcp_servers` config (the `<server>` segment of `mcp__<server>__<tool>`) |
| `tool` | `string` | Un-namespaced upstream tool name |
| `result` | `string` | One of `dispatched`, `tool_error`, `failed`, `blocked` — finer-grained than the event-type split |
| `duration_ms` | `int64` | Wall-clock from dispatch start to result-in-hand |
| `error` | `string` | Present on `orchestrator.mcp.tool.failed` and (when applicable) `orchestrator.mcp.tool.dispatched` with `result=tool_error` |

### `orchestrator.mcp.tool.dispatched`

Emitted on every dispatch that reached the upstream MCP server, regardless of whether the upstream returned `is_error=false` (clean success) or `is_error=true` (tool-level failure with diagnostic text). The `result` payload key disambiguates the two: `dispatched` for clean, `tool_error` for upstream-marked failures. Operators chart `tool_error / (dispatched + tool_error)` to spot servers that are answering but unhappy.

### `orchestrator.mcp.tool.failed`

Protocol-level failure before a result was in hand: transport closed, RPC error, unknown-tool routing miss. The agent loop forwards the diagnostic as a tool-error message to the LLM (the run does not fail), but the event is the audit signal a dashboard would alert on.

### `orchestrator.mcp.tool.blocked`

The task's `approval_policy=block` short-circuited the call. The upstream was never contacted; the LLM saw a tool error suggesting it pick a different path. Distinct from `orchestrator.mcp.tool.failed` so operators can alert on `failed` without their pages firing on the (legitimate) block path. Distinct from `approval.requested` because block doesn't pause the run — it's a hard refusal, not a gate.

## Housekeeping

### `task.updated`

Emitted when task-scoped metadata changed in a way that affects the run's view (e.g. cancellation flush, resume reset). The auto-merged `run` reflects post-update state. No extra keys.

### `snapshot`

The per-run SSE handler writes one of these every time it detects a state change between heartbeats. Subscribers reconnecting via `Last-Event-ID` rely on these to backfill state. Distinguishable from real lifecycle events by the leading `event_type=snapshot`; the `data.snapshot` key holds the rebuilt `TaskRunStreamEventData` JSON.

| Extra key | Type | Notes |
|---|---|---|
| `snapshot` | `object` | Full `TaskRunStreamEventData` — run, steps, artifacts, approvals |

### `external.event`

The default event type when a caller posts to `POST /v1/tasks/{id}/runs/{run_id}/events` without specifying an `event_type`. Use this to integrate human-in-the-loop signals or external systems into the run timeline without inventing new event-type strings.

| Extra key | Type | Notes |
|---|---|---|
| `step_id` | `string` | Optional caller-supplied step correlation |
| `status` | `string` | Optional caller-supplied status hint |
| `note` | `string` | Optional caller-supplied note |
| `snapshot` | `object` | Auto-injected stream state at the moment of append |

Callers can also pass an arbitrary `data` map alongside; those keys are merged into the event's `data` field at the same level as the auto-injected ones.

## Subscribing tips

- **Filtering** — `event_type` accepts a comma-separated allowlist; multiple values OR within the slice. `task_id` is a single id (not csv). Filters AND across types.
- **Cursor pagination** — every response carries `next_after_sequence`; pass it back as `after_sequence` on the next call. `after_sequence` is strictly-greater, so a client passes the last sequence it saw.
- **Reconnect** — both SSE feeds support resume via the `Last-Event-ID` header (id is the global sequence).

## Related docs

- [runtime-api.md](runtime-api.md#public-events-feed) — endpoint shape, query params, auth
- [agent-runtime.md](agent-runtime.md#cost-tracking) — cost-model details for `agent.turn.completed`
- [telemetry.md](telemetry.md) — OTel spans / metrics (a different stream from this catalog)
- [architecture.md](architecture.md) — where events fit in the request lifecycle

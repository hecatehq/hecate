---
name: hecate-backend
description: Use when working on the Hecate Go backend — gateway, agent runtime, providers, sandbox, storage. Keeps backend work aligned with Hecate's "operator-grade control plane, runtime-aware" thesis.
---

# Hecate backend skill

Use this skill for any work outside `ui/`. The React UI has its own skill at [`../ui/SKILL.md`](../ui/SKILL.md). For the `internal/providers/` package specifically, also reach for [`../providers/SKILL.md`](../providers/SKILL.md) — it owns the api↔providers boundary and the seven-step "add a wire field" chain.

## Canonical guidance lives here

Don't duplicate. This skill is the backend lens; the rules themselves live in:

- [`../../core/project-context.md`](../../core/project-context.md) — repo layout, rings, storage tiers, toolchain pins, risky areas.
- [`../../core/engineering-standards.md`](../../core/engineering-standards.md) — field-shape rules, parallel-struct rule, anti-patterns.
- [`../../core/workflow.md`](../../core/workflow.md) — operating loop, planning triggers, commit etiquette.
- [`../../core/verification.md`](../../core/verification.md) — verification ladders, race-suite floor, done criteria.

## Product lens

The backend should feel like:

- A single-process gateway control plane.
- A deny-by-default policy enforcer.
- A runtime-aware proxy that explains its decisions.
- A debugging surface — every request leaves a trace, every cost is itemized, every approval is logged.

It should not feel like:

- A thin pass-through with marketing on top.
- A configurable framework where you bring your own everything.
- A research demo that works in one provider's happy path.

Default to operator confidence: clear status, clear errors, deterministic state, no surprises on restart.

## Engineering thesis

Calm, durable, and explicit. Code should age well — the runtime is supposed to live for years, not iterations.

Prefer one gateway process, one port, embedded UI (`//go:embed ui/dist`); deterministic startup with env-driven config; backend tier choice surfaced as a config knob, never inferred; explicit error wrapping with cause chains; standard library first, well-known third party second, novel deps last.

## Operator priorities

Every endpoint, every config knob, every error message should answer:

1. What did the gateway just decide?
2. Why did it decide that?
3. What did it cost / how long did it take?
4. What happens if it fails next time — retry, fallback, fail?
5. How do I find the trace for this in OTel?

When choosing between "elegant" and "operationally explicit," choose explicit.

## Hecate-specific backend rules

- **No auth layer.** Every request is processed as the operator, and the gateway binds to `127.0.0.1` by default. Do not add token/tenant assumptions back into new endpoints.
- **Workspace-bound IO uses shared seams.** Hecate-mediated file/search/write operations go through `internal/workspacefs`. Shell commands go through the sandbox executor and `internal/processrunner`; Hecate-owned Git helpers use `internal/gitrunner` where they do not need the broad `git_exec` shell-shaped interface. Avoid raw `os.Open`, `os.ReadFile`, `os.WriteFile`, `os.Stat`, `filepath.WalkDir`, raw `exec.Command`, or direct `git` subprocesses for workspace-bound behavior. Raw OS/process APIs are fine for config/data-dir/platform plumbing and narrowly scoped tests; say why when the distinction is not obvious.
- **Sandbox is per-call subprocess, applied inline.** Shell tool calls and broad `git_exec` calls run through the sandbox executor after policy validation + env sanitisation + output cap + wall-clock timeout. On Linux with `bwrap` installed and on macOS, the call is additionally wrapped by `bwrap` / `sandbox-exec` for fs+net confinement (auto-detected at startup, exposed on `/healthz` under `sandbox.os_isolation`). No separate sandbox daemon, no per-call rlimits — operators who want CPU/FD/memory caps run the gateway under systemd or in a container with `--cpus` / `--memory` flags. New tools follow WorkspaceFS / ProcessRunner / GitRunner as appropriate.
- **Approvals are blocking.** Pre-execution and mid-loop approvals halt the run; the run record persists in `awaiting_approval` until resolved. New gates use the same `TaskApproval` shape.
- **Events are appended, not mutated.** Every state transition writes a `run_event` with a monotonic sequence. The SSE stream replays from `after_sequence`. New event types must follow the event-protocol v1 taxonomy (`run.*`, `turn.*`, `tool.*`, `policy.*`, `gap.*`, `error.*`) and be documented in `docs/events.md`.
- **Cost is in micro-USD when present.** Money fields stay `int64` in micro-USD (`1_000_000` = $1). Never `float64` for money. The gateway records usage events for visibility; it does not enforce global spend controls.
- **OTel is first-class.** Every request gets a trace ID surfaced in the response header (`X-Trace-Id`) and persisted on the run record. New code paths add spans, not just log lines.
- **Metric labels are guarded.** Record metrics through `internal/telemetry` helpers and normalizers. Closed-set dimensions collapse unknown values to `other`; free-form dimensions must reject control characters and oversized labels. Put raw commands, paths, stdout/stderr snippets, and adapter diagnostics in spans, logs, or persisted events — never metric labels.

## Backend recipes

### Add a passthrough field end-to-end

The seven-step chain spans `pkg/types/` → `internal/api/` → `internal/providers/` and tests at every layer. Canonical version: [`../providers/SKILL.md`](../providers/SKILL.md). Forgetting to plumb the field into the streaming `wireReq` is the most common bug.

### Add an MCP tool

`internal/mcp/server/tools.go`:

1. Append a `s.RegisterTool(...)` call in `RegisterDefaultTools` with `Annotations` set (`ReadOnlyHint`, `DestructiveHint`, `IdempotentHint` as appropriate).
2. Add a `<name>Handler` returning `ToolHandler` further down.
3. Update the `docs/mcp.md` tool table.
4. Tests in `internal/mcp/server/tools_test.go` using the `fakeGateway` helper.

### Change task-run streaming

`GET /hecate/v1/tasks/{id}/runs/{run_id}/stream` has two seams:

1. `internal/api/task_run_stream_projector.go` maps persisted run events plus
   live task storage into `TaskRunStreamEventData`.
2. `internal/api/task_run_stream_writer.go` writes the SSE frames.

Keep the stream contract forward-moving: persisted snapshot payloads should
carry the current `TaskRunStreamEventData` shape when they are written, and
older alpha rows can replay as they were stored. Do not mutate historical
`run_event` rows; the event log is append-only. The stream endpoint is
read-only; it may emit projected live frames with the latest persisted sequence,
but must not append synthetic `snapshot` events. Handler changes should stay
focused on request setup, polling, and cancellation.

### Change chat-session / ACP adapter behavior

Chat sessions separate ownership from turn execution:

1. `agent_id="hecate"` owns built-in Hecate Chat sessions. Each turn chooses
   `execution_mode="direct_model"` for a plain gateway/router call or
   `execution_mode="hecate_task"` for task-backed tools.
2. `agent_id` values such as `codex`, `claude_code`, or `cursor_agent` own
   External Agent sessions. Their turns use `execution_mode="external_agent"`
   and point at one supervised adapter session.

For Hecate-owned task turns, the first prompt creates the task; follow-ups
continue the latest terminal run through the task runtime while the immediately
previous segment was also task-backed. Re-enabling tools after a direct model
segment creates a new task-backed segment in the same transcript. While a
task-backed segment is queued, running, or awaiting approval, the entire Hecate
Chat session is busy: direct model turns are rejected too, so one transcript
cannot race a live task loop against a separate model call. The browser UI may
queue a prompt locally while busy, but the backend contract remains one active
task-backed turn per session.

External Agent has two live/persistence layers:

1. `internal/chat` stores the Hecate transcript and native ACP session id
   in memory or sqlite.
2. `internal/agentadapters` owns the live ACP/process session manager.

Task-backed Hecate Chat additionally uses `internal/orchestrator`,
`internal/taskstate`, and `internal/modelcaps`. Do not add a second lightweight
tool-loop runtime; reuse task approvals, run events, artifacts, patch review,
and OTel. When adding live-output behavior, stream through the existing
gateway/provider path where possible and publish snapshots through the chat
live stream; do not fork a second chat-only event stream for Hecate-owned
tools.

Native `agent_loop` code is intentionally split by responsibility:

- `executor_agent_loop.go` is the control-flow spine. Keep it focused on turn
  progression, resume detection, final answer, approval gate, tool dispatch,
  and ceiling checks.
- `executor_agent_loop_chat.go` owns a fresh LLM turn: request construction,
  streaming capture, route capture, assistant events, thinking step, turn cost,
  and conversation snapshot.
- `executor_agent_loop_run_state.go` owns run assembly: next step index,
  steps/artifacts, resolved route, per-turn cost records, and final
  `ExecutionResult` accounting.
- `executor_agent_loop_conversation.go`, `executor_agent_loop_approval_gate.go`,
  and `executor_agent_loop_tools.go` own conversation persistence, approval
  decisions, and tool dispatch. Prefer extending those seams over re-growing
  the main `Execute` loop.

When changing this path:

1. Keep `docs/rfcs/hecate-chat-model-capabilities.md` and
   `docs/runtime-api.md` aligned when changing task-backed Hecate Chat or capability
   behavior.
2. Keep provider/model readiness contracts aligned across
   `docs/providers.md`, `docs/chat-sessions.md`, and `docs/runtime-api.md`.
   A stale selected model should fail with the stable API contract
   (`model_not_configured`) if it reaches the server, but UI clients are
   expected to preflight against `/v1/models` plus
   `/hecate/v1/providers/status` and block send with actionable diagnostics.
3. Keep `docs/external-agent-adapters.md` aligned for operator-visible
   behavior such as launchers, env sanitisation, persistence, raw diagnostics,
   guardrails, auth/readiness probes, and troubleshooting.
4. Add focused tests in `internal/agentadapters/*_test.go` for ACP/process
   protocol behavior and `internal/api/server_test.go` for HTTP/session
   persistence behavior. Guardrail changes should cover both the HTTP 422
   envelope and the session snapshot fields the UI consumes.
5. If the change touches model-capability precedence, add or update tests in
   `internal/modelcaps` and the `/v1/models` API tests.
6. If the change touches approval/grant durability, startup reconcile, or
   cmd/hecate store wiring, add or run the binary e2e approval smokes:
   `go test -tags e2e -run 'TestApproval' ./e2e`.
7. Run the race suite. Long-lived adapter sessions are runtime code, not just
   a UI convenience.

### Add a persisted run-event type

1. Pick an event-protocol name from the existing taxonomy before adding a new dotted name. Prefer generic families such as `tool.*`, `policy.*`, `gap.*`, and `error.*` with specific details in `data` over subsystem-specific names.
2. Write normal run events through the event recorder path: orchestrator code uses `r.emitRunEvent(...)`, and HTTP/API-owned writes use `internal/runtimeevents.Recorder`. Avoid direct `store.AppendRunEvent` calls outside storage tests and the store-level terminal transition path (`ApplyRunTerminalTransition`), where the event write must remain in the same transaction as the terminal state mutation.
3. Put shared event payload shapes in `internal/runtimeevents` as small builder functions that return `map[string]any`; reuse existing builders such as `ApprovalRequested`, `ApprovalResolved`, `TurnCompleted`, `PatchApplied`, and `PatchReverted` instead of recreating key sets inline.
4. In orchestrator code, call `r.emitRunEvent(ctx, taskID, runID, "your.event.type", ..., extraDataMap)` at the right life-cycle moment. Emit the event **before** handing off to the queue — see the emit-before-enqueue gotcha above.
5. Document the event and its payload in `docs/events.md`.
6. If high-cardinality, wire into `internal/retention/retention.go` as a new subsystem (see `turn_events` for the pattern).

### Add a start-time validation error (HTTP 422)

For errors that should surface before a run is created (bad config, missing required field):

1. Define a sentinel error in `internal/orchestrator/runner.go`: `var ErrMyThing = errors.New("my_thing")`.
2. Return it (wrapped is fine; use `errors.Is`) from `startTaskWithOptions` before any run is created.
3. In `internal/api/handler_tasks.go` `HandleStartTask`, add an `errors.Is(err, orchestrator.ErrMyThing)` branch that returns `apiError(http.StatusUnprocessableEntity, "my_thing", err.Error())`.
4. Add the error code to `internal/api/error_mapping.go` if it has an OTel span status implication.
5. Test via `tasks.mustRequestStatus(http.StatusUnprocessableEntity, ...)` in `internal/api/server_test.go`.

## Test helper cheat-sheet

| Helper                                                 | File                                               | Use for                                                                                                                                        |
| ------------------------------------------------------ | -------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| `testRoundTripperFunc`                                 | `internal/providers/provider_test_helpers_test.go` | Stub HTTP transport for provider tests                                                                                                         |
| `newAnthropicTestProvider`                             | `internal/providers/tooluse_test.go`               | Anthropic provider with cached caps (skips discovery)                                                                                          |
| `newTestHTTPHandler` / `*WithConfig` / `*ForProviders` | `internal/api/server_test.go`                      | In-process gateway handler                                                                                                                     |
| `fakeUpstreamCapturing`                                | `e2e/gateway_test.go`                              | E2E: capture what gateway forwarded to upstream                                                                                                |
| `hecateServer`                                         | `e2e/gateway_test.go`                              | E2E: spawn the real binary on a free port                                                                                                      |
| `startHecateProcess`                                   | `e2e/ollama_test.go`                               | E2E: shared hecate binary for the Ollama suite (TestMain-driven)                                                                               |
| `autoPreconfiguredEnv`                                 | `e2e/gateway_test.go`                              | Inject `PROVIDER_<NAME>_PRECONFIGURED=1` for every `PROVIDER_<NAME>_*` env var; both spawn helpers call it so test sites don't repeat the gate |

## Backend gotchas

- **Emit run events before enqueue, not after.** The in-memory queue dispatches synchronously: calling `enqueueRun` can cause a worker to claim the job and emit `run.started` before the preceding lifecycle event is persisted if the emit comes after. Use the lifecycle helpers (`emitRunQueuedAndEnqueue`, `requeueDisconnectedRun`) so `run.queued` / `gap.run_disconnected` are written before handing work to the queue. Queue pointer, worker lifetime, lease heartbeat, and in-flight job bookkeeping live behind `runQueueCoordinator`; keep new queue/lease behavior inside that seam.
- **The task-run SSE stream wakes on store mutations, not just events.** `HandleTaskRunStream` subscribes to a per-run wake bus embedded in the store (`internal/taskstate/notify.go`) rather than polling. Steps, artifacts, and run-status changes persist _without_ emitting a `run_event`, so the store signals the bus on every run-scoped write (`signalRun`), not only on `AppendRunEvent` — a new run-scoped mutation that forgets to signal stalls the live stream until the 15s heartbeat re-reads. The `SubscribeRun` capability is optional (type-asserted, not on the `Store` interface); backends that lack it fall back to polling.
- **modernc/sqlite TIME-as-text format** — the driver writes `time.Time` using Go's default `time.Time.String()` format (`2026-04-28 02:37:38.4524 +0000 UTC`), which doesn't lex-compare with RFC3339Nano cutoffs and breaks the retention sweep silently. Always write timestamps as `t.UTC().Format(time.RFC3339Nano)` explicitly when the column is TEXT (see `internal/taskstate/sqlite.go` `AppendRunEvent`).
- **Capability cache seeding** for provider tests — see [`../providers/SKILL.md`](../providers/SKILL.md) for the snippet. Without it the discovery path panics on a nil request body.
- **Synthetic local providers** — use `PROVIDER_FAKE_KIND=local` for e2e scenarios that should not require a real cloud provider.
- **Env-PRECONFIGURED gate for e2e providers** — env-supplied provider credentials (`PROVIDER_<NAME>_API_KEY` / `_BASE_URL`) only auto-import into the settings store when `PROVIDER_<NAME>_PRECONFIGURED=1` is also set. Both e2e spawn helpers funnel through `autoPreconfiguredEnv` so tests don't have to repeat it. New e2e helpers that bypass `hecateServer` / `startHecateProcess` need the same call; otherwise routed requests 400 with `no provider supports model …`.

## Done criteria

See [`../../core/verification.md`](../../core/verification.md). Before filing a
PR that touches Go/backend files, run the relevant Go checks: targeted or broad
`go vet`, affected `go test` packages, and the race suite for runtime paths. If
UI/TypeScript files changed too, run the UI ladder as well. Race suite is the
floor for runtime/backend work, not a nice-to-have.

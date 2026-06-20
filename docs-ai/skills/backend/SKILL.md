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
- **Events are appended, not mutated.** Every state transition writes a `run_event` with a monotonic sequence. The SSE stream replays from `after_sequence`. New event types must follow the event-protocol v1 taxonomy (`run.*`, `turn.*`, `tool.*`, `policy.*`, `gap.*`, `error.*`) and be documented in `docs/runtime/events.md`.
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
3. Update the `docs/runtime/mcp.md` tool table.
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

### Change task APIs

Task HTTP handlers should stay thin: parse path/query/body, choose the
HTTP status/error envelope, and render response DTOs. Task creation,
task/run loading, active-run conflict checks, approval resolution dispatch,
resume budget raising, and runner calls live behind
`internal/taskapp.Application`. Keep that seam on `taskapp` command structs
rather than HTTP request DTOs, and route known app sentinels / validation
wrappers through `writeAppError` mapping slices before adding handler-local
switch blocks. Extend the seam before adding more store/runner orchestration
directly to handlers.

Application packages should use shared app-layer wrappers from
`internal/apperrors` for validation/conflict classes, while preserving any
package-local helper aliases (`taskapp.Validation`, `providerapp.Conflict`,
etc.) that keep call sites readable. HTTP status-code decisions remain in
`internal/api` mapping helpers; use shared app-error mapping helpers for
validation/conflict/sentinel/fallback cases (`validationAppErrorMapping`,
`conflictAppErrorMapping`, `sentinelAppErrorMapping`,
`writeAppErrorWithFallback`) before adding package-specific switches. Do not
import API response types into app packages.

API handler app-wiring helpers (`taskApplication`, `chatApplication`,
`providerApplication`, `projectWorkApplication`, `modelApplication`) live in
`internal/api/applications.go`. Keep constructors there instead of scattering
dependency wiring through feature handlers.

### Do not regress cleaned-up runtime seams

Recent refactors deliberately removed handler-owned lifecycle logic and alpha
compatibility glue. Do not reintroduce it as a defensive fallback.

- HTTP handlers parse, map, and render. They should not directly own task,
  chat, provider, project-work, queue, approval, or event lifecycle decisions
  once an app/runtime seam exists.
- Extend `taskapp`, `chatapp`, `providerapp`, `projectworkapp`, or
  `runtimeevents` before adding parallel store/runner/event code in
  `internal/api` or `internal/orchestrator`.
- New non-terminal run-event writes go through `internal/runtimeevents`.
  Terminal run transitions stay in `taskstate.ApplyRunTerminalTransition`
  because the event write must be atomic with state mutation.
- Project assignment runtime links are canonical through `execution_ref` and
  project activity projections. Do not restore raw `task_id` / `run_id` /
  `chat_session_id` fallback contracts.
- Assignment preflight is inspect-only. `POST /start` remains authoritative and
  must keep its own conflict/state checks even when the UI preflights first.

### Change project work APIs

Project Work HTTP handlers follow the same app-layer rule. Role, work-item,
assignment command shaping, task-backed assignment start state transitions, and
external-agent session start / cleanup live behind
`internal/projectworkapp.Application`; handlers parse request DTOs, build
API-specific context packets, project response DTOs, and map known
project-work/app errors through `writeAppError`. Extend that app seam before
adding more project-work store, task runner, chat store, or external-agent
runner orchestration to `handler_project_work.go`. Keep app-layer dependencies
narrow: define the minimal store/runner interfaces the command needs instead
of accepting broad subsystem stores by habit.

Project assignment launch planning belongs in `internal/projectworkapp`.
Workspace resolution, profile/skill resolution, provider/model defaults,
External Agent adapter/option validation, assignment task construction, and
preflight/start launch-plan parity should stay behind that app seam.
`internal/api/handler_project_assignment_launch.go` owns HTTP parsing, error
mapping, response projection, and API-local context-packet assembly. Do not add
launch planning or start orchestration back to the broad
`handler_project_work.go`, and do not resolve provider/model/profile/workspace
or External Agent adapter/options separately for preview and dispatch.

Project activity is a read/projection surface with split ownership:
`internal/projectworkapp` owns assignment execution refs, task/run and
chat-session projection, External Agent assignment reconciliation, blocking
signals, bucket/status summaries, stale/missing detection, and canonical linked
runtime ids. `internal/api/handler_project_activity.go` owns HTTP response DTOs,
linked-chat loading/rendering, and artifact/handoff grouping.
Do not rebuild assignment activity decisions in UI or handlers from raw
`task_id`/`run_id`/`chat_session_id`; use the `execution_ref` / projection seams.

Project Assistant HTTP handlers call `internal/projectassistantapp.Application`
for context, draft, propose, and apply commands. Keep service construction,
store/LLM wiring, and in-process partial-apply progress behind that cached app
boundary. `internal/projectassistant` owns proposal-domain behavior: context
building, deterministic/model/bootstrap drafting, action validation, and
confirmed apply semantics. Keep that package split by responsibility:
`service.go` is the facade/DTO home, `proposal_validation.go` owns action
shape and fingerprint contracts, `proposal_apply.go` owns the confirmed apply
loop and dispatch, and `action_handlers.go` owns durable mutation handlers.
Handlers should parse/render/map errors only; do not rebuild Project Assistant
stores or call `projectassistant.NewService` directly
from API code.

### Change chat-session / ACP adapter behavior

Chat sessions separate ownership from turn execution:

1. `agent_id="hecate"` owns built-in Hecate Chat sessions. Each turn chooses
   `execution_mode="hecate_task"`. `tools_enabled=false` records a plain
   gateway/router call; `tools_enabled=true` records a task-backed tools turn.
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
   in memory, SQLite, or Postgres.
2. `internal/agentadapters` owns the live ACP/process session manager.

Adapter action visibility uses a two-step contract. The built-in registry is
the offline fallback for expected support; after an explicit probe,
ACP `Initialize` capabilities are authoritative for that adapter row. Keep
`ProbeResult.CapabilitiesKnown` explicit so a successful initialize with no
auth/logout support can override stale static flags. Hecate's local
`authenticate` endpoint calls ACP method `agent-login` after Initialize, so only
that agent auth method should set `supports_authenticate=true`; other auth
methods may be surfaced as non-secret health diagnostics without enabling the
button. Keep action execution aligned with the same live capability contract:
do not call ACP `authenticate` unless `agent-login` was advertised, and do not
call ACP `logout` unless `agentCapabilities.auth.logout` was advertised. Remote
runtime mode blocks local ACP `authenticate`; hosted runs authenticate adapters
through declared remote-safe env-key credential modes.

Hecate owns the ACP process/session boundary, not provider-specific adapter
implementation parity. Tests in this repository should use the repo-local fake
ACP peer to cover probing, auth/logout, session prepare/load, config options,
commands, usage, structured activity mapping, auth-required prompt errors,
native session reload/recovery, and run output. Do not import standalone adapter
source modules or
`acp-adapter-kit` into Hecate just to test Codex/Claude adapter behavior; that
parity belongs in the adapter repositories, with optional release-binary smokes
(`just test-acp-release-smoke`) when packaging drift needs coverage.

Chat session lifecycle orchestration starts in `internal/chatapp.Application`.
Session create, external-agent prepare, native session metadata persistence,
native close/delete, adapter config option writes, Hecate Chat settings, and
cleanup after prepare/update failure belong there. Session reads/rename and
message admission / dispatch planning also belong in `chatapp` so handlers do
not re-own transcript validation or execution-mode branching. Handlers keep
HTTP parsing, live-run cancellation, workspace validation, model/profile
resolution, live publish, and response rendering. Extend that app seam before
adding more chat store, task-store, or adapter-runner orchestration to
`handler_chat.go`, and keep dependencies narrow to the methods the command
needs.

Chat turn terminal status/output classification lives in
`internal/api/handler_chat_turn_execution.go`. Extend that helper and its
focused tests before reintroducing inline direct-model or External Agent
success/failure/cancel classification in the handlers.

Chat context endpoints use `internal/chatcontext` for pure context-packet
lookup/decode, normalization, cloning, and marshaling helpers. Keep larger
project/context assembly close to the API until it has a narrow dependency
shape; move pure packet operations into `chatcontext` instead of duplicating
JSON decode, reference merging, or transcript scans. Compose refs with
`chatcontext.ChatMessageRefs`, `TaskRunRefs`, `ProjectAssignmentRefs`, and
`MergeRefs`; do not hand-roll ad hoc `chat.ContextRefs` structs in new
call sites unless you are constructing the packet body itself.

### Change provider settings APIs

Provider settings HTTP handlers follow the app-layer rule too. Provider
settings status aggregation, policy-rule commands, provider create/update/delete,
duplicate/base-URL guards, provider id derivation, API key rotate/clear, and
dynamic provider-runtime dispatch live behind `internal/providerapp.Application`.
Handlers parse request DTOs, attach the settings actor to context, render
`SettingsProviderRecord`, and map known provider-app validation/conflict errors
through `writeAppError`.

Keep providerapp dependencies narrow: it needs a control-plane snapshot reader
and the small provider runtime interface for `Upsert`, `RotateSecret`,
`DeleteCredential`, and `Delete`. It should not import API DTOs or renderer
helpers.

Task-backed Hecate Chat additionally uses `internal/orchestrator`,
`internal/taskstate`, and `internal/modelcaps`. Do not add a second lightweight
tool-loop runtime; reuse task approvals, run events, artifacts, patch review,
and OTel. When adding live-output behavior, stream through the existing
gateway/provider path where possible and publish snapshots through the chat
live stream; do not fork a second chat-only event stream for Hecate-owned
tools.

Task-backed Hecate Chat task creation/continuation is isolated behind
`internal/api`'s `hecateAgentTaskOrchestrator`. Extend that seam when changing
how chat turns create backing tasks, continue terminal runs, or stamp run
context packets; keep the HTTP handler focused on request parsing, chat message
persistence, live publishing, and response rendering.

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

1. Keep `docs/design/accepted/hecate-chat-model-capabilities.md` and
   `docs/runtime/runtime-api.md` aligned when changing task-backed Hecate Chat or capability
   behavior.
2. Keep provider/model readiness contracts aligned across
   `docs/operator/providers.md`, `docs/runtime/chat-sessions.md`, and `docs/runtime/runtime-api.md`.
   A stale selected model should fail with the stable API contract
   (`model_not_configured`) if it reaches the server, but UI clients are
   expected to preflight against `/v1/models` plus
   `/hecate/v1/providers/status` and block send with actionable diagnostics.
   Model listing, refresh selection, capability resolution, and readiness-error
   wrapping live in `internal/modelapp`; API handlers should render DTOs and
   map `modelapp.ReadinessError` into the existing error envelope.
3. Keep `docs/runtime/external-agents.md` aligned for operator-visible
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
3. Put shared event names and payload shapes in `internal/runtimeevents`. Event names use `runtimeevents.Event...` constants; payload shapes use small builder functions that return `map[string]any`. Reuse existing builders such as `ApprovalRequested`, `ApprovalResolved`, `TurnCompleted`, `PatchApplied`, and `PatchReverted` instead of recreating key sets inline.
4. In orchestrator code, call `r.emitRunEvent(ctx, taskID, runID, runtimeevents.EventYourName.String(), ..., extraDataMap)` at the right life-cycle moment. Emit the event **before** handing off to the queue — see the emit-before-enqueue gotcha above.
5. Document the event and its payload in `docs/runtime/events.md`.
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

- **Emit run events before enqueue, not after.** The in-memory queue dispatches synchronously: calling `enqueueRun` can cause a worker to claim the job and emit `run.started` before the preceding lifecycle event is persisted if the emit comes after. Use the lifecycle helpers (`emitRunQueuedAndEnqueue`, `requeueDisconnectedRun`) so `run.queued` / `gap.run_disconnected` are written before handing work to the queue. Queue pointer, worker lifetime, lease heartbeat, and in-flight job bookkeeping live behind `runQueueCoordinator`; claimed-run loading, start transition, resume checkpoint, and ack live behind `claimedRunProcessor`; claimed-run executor dispatch and failure/cancel finalization live behind `claimedRunExecution`; successful execution-result persistence lives behind `executionResultPersister`. Terminal transition input builders live in `runner_terminal_builders.go`; extend those instead of rebuilding `terminalRunTransition` at call sites. Keep new queue/lease behavior inside those seams.
- **The task-run SSE stream wakes on store mutations, not just events.** `HandleTaskRunStream` subscribes to a per-run wake bus embedded in the store (`internal/taskstate/notify.go`) rather than polling. Steps, artifacts, and run-status changes persist _without_ emitting a `run_event`, so the store signals the bus on every run-scoped write (`signalRun`), not only on `AppendRunEvent` — a new run-scoped mutation that forgets to signal stalls the live stream until the 15s heartbeat re-reads. The `SubscribeRun` capability is optional (type-asserted, not on the `Store` interface); backends that lack it fall back to polling.
- **SQL timestamp storage** — SQLite TEXT timestamps must be written as
  `t.UTC().Format(time.RFC3339Nano)` when lexical ordering matters. Postgres
  does not accept empty-string timestamps; SQL stores that pass `time.Time`
  values should use the shared `storage.TimestampColumn*` helpers instead of
  ad-hoc TEXT columns.
- **Storage selector coverage** — when adding a persisted surface or moving a
  surface between backend selectors, update `internal/config/config_test.go`,
  `cmd/hecate/banner_test.go`, and the opt-in `cmd/hecate/postgres_smoke_test.go`.
  Queue backend changes must also update `internal/telemetry/metric_labels.go`
  and `internal/telemetry/metrics_test.go` so hosted Postgres stays visible in
  OTel instead of collapsing to `other`.
- **Remote runtime endpoint policy** — every new `/hecate/v1/*` route must be
  classified in `internal/api/remote_runtime_policy.go` as remote-safe or
  local-only. Unknown Hecate-native paths fail closed in remote mode, and
  `TestRemoteRuntimeEndpointPolicyCoversRegisteredHecateRoutes` guards the
  registered route list.
- **Remote external-agent env** — adapter subprocesses started from
  cloud-identified requests must go through `prepareAdapterProcessEnv`, not
  direct `os.Environ()` filtering. Remote mode uses an ephemeral home and only
  the declared remote-safe credential keys plus runtime essentials.
- **Remote local-provider policy** — remote runtime mode disables `kind=local`
  providers by default. Keep preset filtering, settings validation, env import,
  and runtime-manager reload in sync; `HECATE_REMOTE_ALLOW_LOCAL_PROVIDERS=1` is
  the explicit sidecar opt-in. This is a provider-kind policy, not URL
  destination filtering; egress restrictions belong in the deployment boundary.
- **Capability cache seeding** for provider tests — see [`../providers/SKILL.md`](../providers/SKILL.md) for the snippet. Without it the discovery path panics on a nil request body.
- **Synthetic local providers** — use `PROVIDER_FAKE_KIND=local` for e2e scenarios that should not require a real cloud provider.
- **Env-PRECONFIGURED gate for e2e providers** — env-supplied provider credentials (`PROVIDER_<NAME>_API_KEY` / `_BASE_URL`) only auto-import into the settings store when `PROVIDER_<NAME>_PRECONFIGURED=1` is also set. Both e2e spawn helpers funnel through `autoPreconfiguredEnv` so tests don't have to repeat it. New e2e helpers that bypass `hecateServer` / `startHecateProcess` need the same call; otherwise routed requests 400 with `no provider supports model …`.

## Done criteria

See [`../../core/verification.md`](../../core/verification.md). Before filing a
PR or pushing an update to one that touches Go/backend files, run the relevant
Go checks: targeted or broad `go vet`, affected `go test` packages, and the
race suite for runtime paths. Add or update tests for production-code changes
in the same PR update. If UI/TypeScript files changed too, run the UI ladder as
well. Race suite is the floor for runtime/backend work, not a nice-to-have.

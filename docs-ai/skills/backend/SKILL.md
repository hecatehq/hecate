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
- **Approvals are blocking and resolve atomically.** Pre-execution and mid-loop
  approvals halt the run; the run record persists in `awaiting_approval` until
  resolved. New gates use the same `TaskApproval` shape. Commit the pending
  decision, awaiting run/task transition, mandatory lifecycle events, and
  rejection child cleanup in one taskstate transition across memory, SQLite,
  and Postgres. Never split approval resolution across a low-level approval
  update and a later runner or handler mutation.
- **Task run starts are one storage transition.** Keep the runner's per-task
  start gate around preflight and workspace provisioning, and commit the
  authoritative no-active-run check, monotonic budget raise, next run number,
  task projection, and run insert through
  `taskstate.ApplyRunStartTransition`. Memory locks, SQLite's immediate write
  transaction, and Postgres' task-row lock are the cross-backend authority;
  app-layer active-run checks are only early feedback. Cancellation is
  two-phase: persist the terminal winner, drain the executor, then replay
  child cleanup without another terminal event or overwriting a newer run's
  authoritative task projection. Boot recovery must cursor
  through every pending-run page rather than treating a page size as a cap.
- **Events are appended, not mutated.** Every state transition writes a `run_event` with a monotonic sequence. The SSE stream replays from `after_sequence`. New event types must follow the event-protocol v1 taxonomy (`run.*`, `turn.*`, `tool.*`, `policy.*`, `gap.*`, `error.*`) and be documented in `docs/runtime/events.md`.
- **Chat message idempotency is a storage commit boundary.** The optional
  `client_request_id` on Hecate-native chat message creation is scoped to one
  session. Reserve and compare its one-way payload fingerprint through
  `chatapp`, then commit the key in the same store transaction as the user
  transcript row before the submitted turn's provider, Task, or ACP dispatch.
  Admission-time semantic compaction is separate provider work and may precede
  that commit, so `chatapp` owns a pending-reservation heartbeat across all
  pre-commit work. Renew at intervals no greater than one-third of the store's
  stale-owner window,
  stop and join the heartbeat before commit/release, and conditionally renew
  once more immediately before the atomic commit. Renewal must validate the
  pending state, owner token, and payload fingerprint; losing ownership fails
  closed before any backing-turn dispatch. Same-key retries load
  the current authoritative session; changed payloads fail closed with
  `chat.client_request_conflict`. Do not keep this guarantee in handler maps,
  store request bodies or MCP secrets in the ledger, or ship it for only one of
  memory/SQLite/Postgres. Keyed HTTP responses expose only
  `message_request.replay` plus the committed user-message id; never render the
  key, fingerprint, copied payload, or internal lease token. Once admission
  reaches a keyed commit, give that atomic user-row/key write its own fresh
  bounded `context.WithoutCancel` window; unkeyed writes remain request-bound.
  A SQL commit must materialize its return value before `Commit` succeeds, not
  issue a request-bound post-commit read that can report a false failure. After
  commit, use fresh bounded persistence windows for the initial assistant row,
  Hecate task/run ownership links, and terminal assistant/session state; never
  hold one across a long turn. Direct model and ACP execution stay request-bound.
  The task-backed Hecate Chat watcher is server-owned after commit and waits on
  its own bounded context plus the explicit live-run cancel hook, so browser
  disconnect does not abandon a queued/running task or strand its transcript.
  The 30-minute chat-run ceiling ends that watcher and terminalizes the chat
  turn; it does not cancel the orchestrator-owned Task. A Task that remains
  active, including one awaiting approval, stays visible and independently
  cancellable through the Task runtime.
- **Cross-store destructive cleanup has one admission gate.** Every
  Hecate-owned chat-session creation path reserves the project-existence check
  through durable creation and ownership linking, including External Agent
  chats launched from project assignments. Project deletion (native or
  Cairnline authority) closes that admission before waiting for
  reserved creates, and advance the mutation epoch at both edges so requests
  arriving before or during cleanup fail instead of creating afterward. The
  gate is process-wide and intentionally pauses all chat creates during a
  project deletion, including project-free and other-project creates. Keep this
  coordination in the API composition layer; do not duplicate Cairnline
  project identity or pretend the independent stores share a transaction.
  Project deletion invokes the `chatapp` orphan-attachment sweep before it
  lists project chats, so a retry repairs transcript-first partial deletes
  before the remaining project chat cleanup. That live sweep may only compare
  attachment session IDs with authoritative transcript rows and delete missing
  owners; pending-claim reconciliation remains a startup or turn-owner concern.
  Cairnline-authoritative deletion may remove portable identity first; any
  rollback import after Hecate cleanup fails must contain only the deleted
  project's portable graph, never a full snapshot that could overwrite
  unrelated concurrent edits. All project-scoped facade mutations also share
  one process-local keyed fence in API composition. Ordinary writes are shared
  admissions held through the Cairnline write, Hecate cleanup or compatibility
  shadow, and response decision. Deletion acquires the process-wide
  destructive-state closure first, closes the project key, waits for admitted
  writes, and keeps both closures through rollback; new same-project writes
  fail with a conflict. Multi-project writes declare their complete key set up
  front; the gate normalizes, deduplicates, sorts, and admits that set
  atomically. Nested calls may use any leased subset but must not incrementally
  add another key. Pathless Project Assistant draft/propose/apply requests use
  `projectassistant.ProposalProjectIDs`, include the current project of a moved
  chat, and retain the complete lease through proposal ledgers, compatibility
  mirrors, and the response decision. Different project keys remain
  independent. Chat terminal/idle assignment reconciliation is best-effort and must use nonblocking project
  admission: project deletion can own the key while it waits for that active
  chat handler to clear its run, so blocking reconciliation before `clearRun`
  would form a lock cycle. A skipped projection is retried by the next terminal
  or strict read reconciliation.
  Canonicalize Project Assistant proposals before deriving those keys. An
  omitted `create_project` patch id uses an explicit target id or, for a
  pathless action, a deterministic proposal-id/action-index id. Persist, return,
  and apply that exact typed action set so ledger fingerprints and direct
  same-proposal retries remain stable; repeated canonicalization is idempotent.
  Project-scoped manual Task creation also holds the keyed fence across the
  Cairnline-backed existence check and durable Hecate Task creation; a
  same-project delete returns the create as `409`. This is runtime admission,
  not Hecate-owned project storage.
  Existing project/chat admission gates do not make online system reset safe.
  The live reset endpoint returns `409 conflict` before mutation until Hecate has one
  reversible runtime-wide quiescer covering write-capable HTTP reads/writes,
  task queue/reconcile/finalizer work, retention, ACP callbacks and approval
  timers, gateway usage/health finalizers, and every Cairnline open. Do not wire
  the cleanup helper back to HTTP or claim success from partial fencing. A
  future quiescer must close admission, drain and generation-fence delayed
  writers, run cleanup, advance its epoch, and reopen.
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
- New non-terminal run-event writes go through `internal/runtimeevents`, except
  approval resolution events that are committed by the taskstate approval/run
  transition itself. Terminal run transitions stay in
  `taskstate.ApplyRunTerminalTransition` because their event writes and any
  approval decision or child cleanup must be atomic with state mutation.
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
Handlers should parse/render/map errors and perform API-composition admission
only; typed action-scope extraction and durable primary-scope validation stay
in `internal/projectassistant`. Do not rebuild Project Assistant stores or call
`projectassistant.NewService` directly from API code.

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

Hecate-owned chat sessions also persist workspace posture. Normalize only
`persistent`, `ephemeral`, and `in_place`; preserve omitted legacy requests as
`in_place`. Snapshot the effective value onto every backing task, reject a
different value after `task_id` exists, and serialize mode mutation through the
same exclusive live-run admission as message turns. Update the
session/message/context workspace to the generated run path for managed execution. External Agent
sessions always remain `in_place` because the ACP adapter owns the selected
workspace. Cairnline Project defaults are coordination intent supplied at chat
creation, never runtime permission or a reason to add parallel Project storage.

External Agent has two live/persistence layers:

1. `internal/chat` stores the Hecate transcript and native ACP session id
   in memory, SQLite, or Postgres. It also stores the adapter-reported
   ACP `initialize.agentInfo` projection as `agent_info`; do not invent a
   parallel implementation-metadata shape.
2. `internal/agentadapters` owns the live ACP/process session manager.

Native-session recovery crosses both layers and must remain fail-closed.
Provider-specific adapters may classify an exact prompt failure as
`native_session_missing`, but they must not retry or replace the id. Hecate may
replace only when the persisted pre-turn transcript proves there was no
successful or ambiguous assistant turn. Keep that transcript-derived authority
behind `internal/chatapp`; HTTP handlers pass its typed decision into the live
adapter manager instead of reimplementing transcript policy. Keep the failed turn, replacement
reservation, fresh ACP start, durable native-id callback, and one prompt retry
serialized. The callback is the commit point: after it succeeds, install the
fresh in-memory session even if request cancellation arrives, but check
cancellation before redisclosing the prompt. Rebuild attachment blocks through
the normal staging path. Load failures for durable or unknown session scopes and
unknown or wrapped prompt-error shapes do not silently create a fresh session.
A classified prompt failure also requires the exact command-bridge outer
start/failed-finish pair and no provider update, inner tool, diff, or unknown
record. Historical raw evidence uses the same exact pair (or the narrowly
recognized process-command-not-found shape); a private raw-withheld marker needs
one matching failed outer-command activity, and unknown activity statuses fail
closed.
A registry-declared process-scoped adapter may replace an unloadable id, but it
must keep the fresh session behind the start reservation and invoke the same
durable callback before publishing it or sending its first prompt. Every return
path after callback success must carry the committed replacement id, including
shutdown, so terminal settlement cannot restore stale durable state.

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
call ACP `logout` unless `agentCapabilities.auth.logout` was advertised.
Keep probe timeouts short, but do not reuse the probe timeout for
operator-triggered `authenticate`: native login flows may open browser or
terminal UI and need a longer window. Remote runtime mode blocks local ACP
`authenticate`; hosted runs authenticate adapters through declared remote-safe
env-key credential modes. Probe/auth helper clients that advertise filesystem
callbacks must pass their temporary workspace into the callback implementation;
otherwise adapters can fail inside Initialize/auth/logout when they use a
capability Hecate claimed to support.

ACP terminal callbacks are a command-execution surface. Keep
`clientCapabilities.terminal` false unless `HECATE_AGENT_ADAPTER_TERMINALS=1`
is configured, require `HECATE_REMOTE_ALLOW_ACP_TERMINALS=1` in remote runtime
mode, and route `terminal/create` through the External Agent approval
coordinator before spawning anything. Terminal callback lifecycle should remain
operator-visible through External Agent chat activities (`type="terminal"`):
record command/cwd/status/exit output previews against the exact assistant
message that created the terminal, and
reuse retained terminal output for ACP tool-call terminal refs when available,
instead of adding an operator-facing embedded terminal API/UI. Session shutdown
cleanup should mark unreleased ACP terminals as cancelled and retain their
bounded output preview before removing them from the client terminal map. Keep
terminal spawning on the same safety path as one-shot shell execution:
`LocalWorkspace.OpenTerminal` must reject policy-invalid commands before spawn
and apply the sandbox OS wrapper when one is available. It must also own the
whole process unit: a dedicated Unix process group or a Windows kill-on-close
Job Object attached before user code runs. Completion means the group/job is
empty and stdout/stderr have drained, not merely that the command leader exited.
Every ACP terminal must acquire its own shared `workspacecoord` writer lease
before spawn and keep it after the creating turn settles until the watcher
confirms completion. A close deadline may return early after best-effort force
termination, but it must not release the lease. A turn-scoped writer lease alone
is insufficient because ACP terminal handles may outlive the turn that created
them. For the same reason, never resolve terminal completion through
`currentTurn()` or an `OnActivity` stream coalescer that closes when `Run`
returns. Capture the turn's durable `OnTerminalActivity` sink on create; that
sink must remain concurrency-safe through terminal exit or session shutdown and
must be bound to immutable session/message ownership. The callback itself must
only enqueue into Hecate's per-session settlement dispatcher and return
promptly. Stream, turn-final, and detached terminal mutations share that
serializer so store read/modify/write updates and live snapshots cannot reorder
or overwrite one another. `OnTerminalClosed` is the exact lifetime boundary:
it follows the terminal's authoritative final activity and no transcript
callback may run afterward. Never fall back from `OnTerminalActivity` to the
ordinary Run-scoped `OnActivity` callback.

Destructive chat close, delete, and idle cleanup must first close
the chat lifecycle and drain counted operations. Once the destructive token is
held, install that exact lifecycle closure as settlement-dispatcher owner before
cancelling an active run. This lets a detached terminal job that lost ordinary
epoch admission settle without head-of-line blocking the turn's synchronous
final write. Close the native session while the owner is installed, then seal
and drain the dispatcher before clearing or deleting transcript state. Every
abort path must relinquish the owner through an ordered barrier before lifecycle
admission reopens. A drain deadline returns `stopping` and retains the lifecycle
fence until the worker actually exits; it must never delete first and allow a
late full-session publish afterward.

If terminal process/output drain exceeds the ACP session-close deadline, emit
one authoritative cancelled activity, detach the durable callbacks, and invoke
`OnTerminalClosed`. The watcher still owns process/output drain and workspace
lease release, but its later exit must not re-enter transcript persistence.

On Unix, preserve ordinary background jobs, `nohup`, and `disown` because they
remain in the owned group. Reject known actual session/process-group detachers,
unsafe wrapper/job-control/alias/eval forms, inline interpreter escapes, and
matching interactive shell/interpreter input. Keep stdin classification exact:
script and inline-command stdin is data unless a force-interactive mode remains
active; explicit shell stdin (`sh`/`bash -`) is code. Preserve up to the bounded
64 KiB syntactically incomplete shell suffix across writes, then fail closed;
discard only newline-terminated prefixes that parse and pass policy. Interpreter
state should retain fixed-size raw-identifier and whitespace-compacted tails,
not cumulative REPL history. Never truncate away an unfinished logical command
before validating its later suffix. Parse clustered job-control/background flags
rather than checking only their first rune. This parser is a best-effort
supervision guard, not hard containment; user docs must name
sourced/generated/encoded code, arbitrary wrappers, custom binaries/syscalls,
and external service managers as residual trusted-subprocess risk.

Operator terminal sessions live in `internal/terminalapp` and are exposed only
through local-only, opt-in runtime API handlers. Do not put terminal process
lifecycle in HTTP handlers, and do not enable operator terminals in remote
runtime mode. Acquire the shared `workspacecoord` writer lease immediately
before process spawn and retain it until exit is observed; release and shutdown
must not open a destructive-mutation race when terminal close fails or races the
exit watcher. New operator-terminal behavior needs app-layer tests, API
loopback/forwarded-header tests, shutdown cleanup coverage, and docs in
`docs/operator/security.md` / `docs/runtime/runtime-api.md`.

Workspace discard is a cross-runtime ownership boundary. API composition must
create one `workspacecoord.Registry` and share that exact instance with the
orchestrator runner, External Agent chat dispatch, task-patch handlers, operator
terminal application, and destructive chat workspace mutations. Registry keys
are canonical paths with symlinks resolved. Darwin and Windows overlap checks
conservatively case-fold components so existing and future case aliases cannot
split one physical coordination domain; accept conservative conflicts for
case-only paths on a case-sensitive Darwin volume. Treat equal and
ancestor/descendant roots as overlapping in both directions because a writer at
the parent can change a child; independent siblings may proceed concurrently.
Acquire a writer lease before task provisioning creates, clones, copies, or
truncates any workspace content, retain task start admission through durable run
creation, and acquire again for the complete execution attempt. After admission,
provision isolated destinations through stable root-relative handles and a
private staging directory; use exclusive no-follow creates, verify directory
identity before commit, and fail closed on every path swap. Also lease an
External Agent ACP turn before user-message append through final terminal
persistence, each live ACP terminal from immediately before spawn until exit is
observed, task-patch apply/revert from its final precondition through
artifact/event persistence, and an operator terminal from immediately before
spawn until exit is observed. A task worker may wait behind a short exclusive
closure; request-bound mutation surfaces should return a conflict instead of
writing through it.

Generated-workspace provisioning must not turn a path check into later raw
`os.*` writes. Open and identity-check the nearest existing managed-root
ancestor before mutation, create missing components and populate a private
staging directory through stable root-relative handles, then place and verify
the completed directory. Git clone may use a private `0700` temporary stage,
but its result must enter the managed root through that same confined copy and
placement path.

The current-workspace discard handler must preserve this order:

1. Validate the client revision and reviewed path set without mutating files.
2. Close and drain the owning `agentChatLive` session lifecycle, then acquire an
   exclusive overlapping-root closure from the shared workspace registry.
3. Reread the authoritative session and complete the durable active-owner scan
   for non-terminal task runs and other active chat sessions on overlapping
   roots. Paginate task runs until exhaustion; a page size is not a safety
   limit.
4. Capture a fresh complete raw **unstaged tracked** `DiffSnapshot` (index →
   worktree) through GitRunner's scoped passive view and compare its revision
   byte-for-byte with the reviewed token. Before returning a snapshot, inspect
   the scoped index state and fail closed if any staged change exists.
5. Reserve Git's conventional real-index lock, recheck scoped staged state,
   prove that the reviewed patch's old side still applies to the live index
   baseline, and conditionally reverse-apply selected paths from that exact raw
   snapshot while the reservation is held. Map index contention, a committed or
   staged baseline change, or a patch that no longer applies to `409` without
   changing any selected file, then refresh the live snapshot after success.

Never authorize discard from diff stats, trimmed output, a capped prefix, a
stored message diff, or a second independently generated patch. The
conditional reverse apply must preserve unrelated and non-overlapping later
edits while rejecting overlapping drift atomically. The registry is
process-local coordination, not a distributed lock. The transient Git index
reservation coordinates with well-behaved Git index writers only; it does not
exclude direct filesystem or non-cooperating index writes. The durable-owner
scan catches persisted queued/recovered Hecate work, and conditional apply
protects against overlapping edits from another process or an external editor;
retain every fail-closed layer and document the residual boundary accurately.

The current discard contract intentionally covers only the scoped
index-to-worktree patch for tracked files. It does not cover staged index
changes or untracked-file deletion. A staged-only workspace must never produce
the deterministic empty-patch revision, and mixed staged/unstaged state must
never issue authority for only the visible layer. Map either case to
`422 invalid_request` before returning a diff or attempting discard, with an
operator action to unstage the scoped changes and refresh/review again. Keep the
staged-state check inside the same hardened, nested-workspace-scoped Git seam;
staged changes elsewhere in the surrounding checkout must not block a nested
workspace. Full two-layer staged review/discard requires an explicit typed
contract and is a separate product slice.

Native `agent_loop` terminal tools (`terminal_open`, `terminal_write`,
`terminal_read`, `terminal_wait`, `terminal_kill`) live behind the
agent-loop dispatcher and must use `LocalWorkspace.OpenTerminal`, not raw
`exec.Command`. Keep their handles run-scoped, preserve open handles across
same-run `awaiting_approval` requeues, and give every handle its own shared
`workspacecoord` writer lease through process-unit and output drain; the
attempt-scoped runner lease is not sufficient because it is released while an
approval is pending. Close retained handles on terminal run completion and
global runtime shutdown, gate them through the existing `shell_exec` /
`all_tools` approval policy, and preserve the same process-unit drain and Unix
detachment contract described above.
Add focused orchestrator tests plus an e2e fake-provider run whenever changing
native terminal tool behavior.

Native `agent_loop` web search is optional. Keep provider clients in
`internal/websearch`, wire them through `orchestrator.Config.WebSearch`, and
advertise `web_search` only when a provider is configured. `network_egress`
must gate both `http_request` and configured `web_search`; result discovery
does not fetch result URLs, which still belongs to `http_request` and its
HTTP policy. When adding or changing a provider, pin the provider-specific
HTTP contract with `internal/websearch` unit tests and keep the fake-provider
e2e path covering the shared `web_search` dispatcher behavior.

Hecate owns the ACP runtime/session boundary, not provider-specific adapter
implementation parity. Hecate may import the owned Go adapter modules and the
provider-neutral adapter kit for its production embedded runtime. Behavioral
unit tests should still use the repo-local fake ACP peer to cover probing,
auth/logout, session prepare/load, config options,
commands, usage, structured activity mapping, auth-required prompt errors,
native session reload/recovery, and run output. Provider-specific command,
stream, and auth parity belongs in the adapter repositories. Add focused Hecate
integration tests for the embedded transport, exact provider path, host-owned
environment, private file links, and shutdown lifecycle. Keep probe
clients honest: any ACP client capability the probe advertises should be
implemented by the probe client against its temporary workspace rather than
returning "not supported" during `session/new`.
Use opt-in real CLI smokes for both embedded and direct runtimes when a change
needs verification against authenticated vendor CLIs. Keep real
provider prompts minimal and outside the default test ladder. Preserve the
shared smoke's privately staged text-file turn so every supported adapter proves
that Hecate's file-input boundary works against the authenticated vendor CLI.

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

Chat attachment bodies are a separate Hecate-owned storage concern. Keep validated,
session-scoped bytes in `internal/chatattachments`; persist only immutable
metadata on `chat.Message`. Upload/content handlers may parse and render HTTP,
but ownership checks, message admission, draft deletion rules, and session
cleanup belong behind `chatapp`. Keep the Handler-scoped attachment upload
admission gate nonblocking and bounded: acquire before multipart body reads or
mode-specific file/image validation, fail with the stable saturation response
when full, and defer release immediately after acquisition. Give the upload
body read its own bounded socket deadline through `http.ResponseController`, retain body-close fallback
for synthetic/custom bodies, and map expiry to the stable upload-timeout
response before releasing the permit. Clear the deadline after a successful
body read, and close an expired-deadline HTTP/1 connection rather than reusing
it. Do not add a global server read timeout: chat and task streams are
intentionally long-lived.

Keep attachment content delivery behind its own nonblocking Handler-scoped
gate. Acquire before the store lookup, hold through integrity validation and
the full body write, and return the stable typed 429 before hydration when the
gate is full. Give the body write a route-local `http.ResponseController`
deadline instead of a global server write timeout. Snapshot the chat lifecycle
and register the lookup/write as an operation before hydration so destructive
session cleanup waits for an admitted response and stale snapshots fail closed.
If the response transport cannot establish that route-local write deadline,
fail closed before committing the image `200` response and close the
connection. Do not silently fall back to an unbounded body write while holding
the content admission permit and session lifecycle operation.
Enforce both per-session and aggregate retained body quotas atomically across
memory, SQLite, and Postgres; cross-session Postgres creates require a shared
quota lock before the session lock. Provider hydration is transient and bounded:
never place binary/base64 content in transcript JSON, SSE, traces, logs,
metrics, or the UI's persisted busy-message queue. Image-bearing attachment
turns on the direct-model path are Tools-off Hecate Chat only and must set an internal capability
requirement that admits only a currently routable, explicitly supported initial
route. Guard every turn that may hydrate current or historical image bodies with
the separate nonblocking, process-wide image-turn gate. Acquire its permit before
attachment claim, historical `Get`, or base64 expansion; reject saturation with
the stable typed 429 response before transcript mutation; and hold the permit
through provider serialization and provider return. Routes that will certainly
omit every image and ordinary text-only turns must remain outside this gate.

External Agent turns may claim the same Hecate-owned attachment records. Carry
their text and hydrated files through the typed `agentadapters.PromptInput`
contract; do not flatten them into prompt prose or disclose attachment bodies to
probe/status paths. Validate size, digest, filename, and media type again at the
ACP boundary, then admit rich blocks against the live `InitializeResponse` held
by that exact `acpSession`: supported raster files use `Image` only when
`promptCapabilities.image=true`; otherwise files use `Resource` only when
`embeddedContext=true`. The baseline `ResourceLink` fallback stages an exact
read-only per-turn file in a private temporary directory and attempts to remove
the whole directory when `session/prompt` settles. Rich blocks also share a conservative
768 KiB encoded wire budget below the supported adapters' 1 MiB message cap.
Measure the actual JSON content-block size cumulatively, including prompt text,
base64 expansion, and text escaping; stage an overflow file as a `ResourceLink`
even when its rich capability is advertised. Reject prompt text that alone
exceeds the budget, and preflight raw payload/base64 size before allocating a
rich block. Guard file-bearing External turns with their own process-wide
nonblocking two-slot admission from before claim through synchronous ACP
return; reject saturation before transcript mutation without blocking text-only
turns. Check cancellation before prompt normalization/construction and again
immediately before `session/prompt` so an accepted Stop cannot send a prompt
that was already cancelled. ACP `fs/read_text_file` may read only the exact
staged path while that turn is live; never broaden WorkspaceFS or allow writes
anywhere in the staging namespace. Register a body-free deny namespace before
dispatch, add every quarantine alias before rename, and retain that fence across
later callbacks and turns until cleanup proves removal. Compare absolute,
file-URI, and workspace-relative spellings against lexical and canonical roots.
Hold one shared namespace fence through the complete WorkspaceFS read/write
fallback; quarantine alias registration takes its exclusive side before rename
and reuses one pending candidate across failed attempts. Keep platform handling
fail closed. Darwin/Linux must retain and verify the canonical temporary parent,
ancestors, stage, and children; reject untrusted ownership, writable non-sticky
directories, and extended ACLs; create and remove relative to retained handles;
remove/read back ACL state before bytes; verify `0700`/`0600`; and seal to
`0500`/`0400`. Darwin must require `MNT_LOCAL` on every verified handle before
using mode/native ACL semantics. Linux must `fstatfs` every verified handle,
allow only ext2/3/4, XFS, Btrfs, tmpfs, overlayfs, ramfs, or F2FS, and accept
POSIX ACL xattr `ENOTSUP` only on that allowlist; network, FUSE, and unknown
models fail closed. Other Unix builds reject resource-link staging. Windows must
reject remote/reparse resolution and untrusted delete/delete-child/DACL/owner
control on retained parents and ancestors. Supply the current-user owner and
protected current-user-plus-LocalSystem inheritable DACL in the atomic
parent-relative directory create, read back that exact shape before children,
then create children relative to the stage and verify each empty inherited DACL
before bytes. Replace write-capable construction handles with read/identity-only
retained handles before dispatch, using share modes compatible with ordinary
readers that do not grant write sharing. Reacquire mutation access to the exact
parent-relative directory only for post-settlement cleanup. Because the sealed
DACL is read-only, first use a share-compatible owner-`WRITE_DAC` identity
handle to verify that DACL and restore the private full-control DACL. Preserve
that intermediate state across retries, then acquire the mutation handle; fail
cleanup if a restrictive live reader prevents that upgrade. Do not introduce a
create-then-protect window.

At settlement, clear body references first, move the retained stage to a fresh
random quarantine name relative to its retained parent, and remove children
relative to retained handles. Retry the complete quarantine/prepare/remove
transition with bounded backoff; never chmod or replace a DACL through an
unverified pathname, and preserve retained identity after failure so cleanup is
retryable. Record successful removal before proving it: once removal is issued,
use a separate bounded proof-only retry state and never restart pathname-based
quarantine or permission work. If synchronous cleanup exhausts, transfer the
exact stage object and its pre-reserved capacity to the single process-owned
janitor; never return from a production path while dropping the last
identity/guard reference. Bound failed stages to four per session and all
file-turn reservations plus failed stages to 16 process-wide. Reject further
file-bearing turns when either bound is full without blocking text-only work,
and wake cleanup again after agent-process termination. The process owner must
outlive retired sessions and use one worker, so repeated session churn cannot
accumulate goroutines or retained handles.
Close per-session turn admission before shutdown snapshots the active owner.
Register that owner before workspace-diff capture or any staging, propagate its
cancel context through capture, and drain it again after process termination
before treating the cleanup-backlog snapshot as complete. Cleanup waiters must
recheck the process owner's authoritative per-session count after every change
notification. Never report a clean close while a full turn can still add
cleanup ownership.
After the final handle-bound staged-file identity audit, recheck the turn
context immediately before `conn.Prompt`; cancellation must clear prompt files
and run the same synchronous cleanup-or-retain path without sending
`session/prompt`. Capture bounded adapter stderr only through initialization,
initial session creation/load, and selected model/config setup. On success,
zero the capture under its writer lock and switch subsequent process writes to
discard before any file-bearing prompt can run.
A body-free immutable alias redactor must outlive the bytes. Apply it to
full/split output, activities, stop reasons, errors, typed approval payloads,
available commands, config-option updates and direct config-write responses,
and originating late-terminal previews. Retain the alias set for the ACP session
lifetime so delayed permission requests and typed updates remain covered after
cleanup proof, and never copy their original wire records into a later turn's
raw diagnostics. Redact only human-facing command/config fields; drop an entry
if sanitizing it would change a protocol identifier or value. Reject approval
sanitization that changes protocol identifiers. Native close/delete failures may
log only a fixed classification
and numeric RPC code, never peer-controlled message or data. Complete-alias and
ordinary accumulated-chunk redaction is accidental-disclosure defense, not DLP
against a selected agent that deliberately transforms or segments a path into
unrelated short records.
Withhold raw ACP diagnostics for staged turns when present because arbitrary
chunks cannot be reconstructed safely. Document the protected remnant path on
exhausted cleanup and the explicit limitation: the stage is not isolation from
another process running as the same OS user, which can alter owner-controlled
permissions/DACLs or inspect a discovered path.
Persist the attachment claim with the user message before dispatch and keep
bytes/base64/private staging paths out of transcript JSON, SSE, traces, logs,
and error responses.
When bytes are hydrated, keep retries on that provider and disable cross-provider
failover. Rehydrate historical images only for the same configured provider
recorded on the original turn; provider changes and unresolved Auto routing get
omission markers. Resolve provider identity before model/capability admission
with one shared precedence: exact canonical runtime name, then a unique
normalized canonical name, then a unique alias. Ambiguous matches fail closed;
catalog order must never choose the route. Resolve against every configured
provider, including disabled or zero-model entries that do not produce model
rows. Resolve and persist an opaque provider generation with the canonical
name. Managed-provider generations must be stable across unchanged reloads and
change on endpoint/account/configuration/credential mutation or
delete-and-recreate; derive them only from non-secret control-plane generation
and dispatch configuration. Providers without a durable non-secret generation
must receive process-scoped registry identities. Never hash credentials into an
identity, expose an identity through public APIs/telemetry/errors, or treat a
legacy missing identity as equivalent. Once image bytes are hydrated, pin the
route decision to both canonical name and generation, then compare both with a
fresh live-registry lookup immediately before dispatch so same-name replacement,
removal, normalized-name takeover, or alias takeover cannot retarget them. When
a provider call fails after receiving bytes, return internal metadata for the
attempted provider/model/generation and trace so the durable transcript records
the disclosure boundary. Do not stamp an attachment-bearing user row with a
provider generation during admission: only attempted-call metadata may make its
bytes eligible for later history hydration, and pre-call failures must leave the
user-row generation empty. At the final gateway dispatch boundary, a tools-on task must
atomically persist the exact resolved provider/model/generation beside its
opaque `InputRef` before provider I/O. Keep that final-dispatch marker separate
from admission: its first model may be governor-rewritten, while recovery and
same-input retries must replay the persisted exact route. This may-disclose
recovery fence is not transcript disclosure metadata. Preserve it through stale
state transitions, cancellation, and requeueing, and fail closed when it cannot
be written. Durable attachment claims use the intended user-message id
as a fence: exact transcript metadata links,
absence releases, deleted owners purge, and conflicts remain fail-closed. Do
the idempotent linked transition again before returning a same-payload keyed
replay, so a committed user row repairs a claim left pending by consecutive
finalization/reconciliation store failures without redispatching the turn. Do
not infer provider-native capability provenance merely because model discovery
was provider-backed, and do not impose Hecate's strict attachment requirement
on ordinary provider-compatible `/v1` rich-content passthrough. That ingress
must still accept exactly one JSON value only, cap the encoded body at 32 MiB,
and apply a route-local 60-second body-read deadline with real socket and
synthetic-body coverage. When normalized compatibility content contains an
image, set `NoProviderFailover` without setting `ImageInput`: custom-provider
passthrough remains available, same-provider retries remain possible, and the
image cannot be disclosed to a second configured provider implicitly. Treat
that fence as provider-instance-bound and revalidate it immediately before
streaming and non-streaming dispatch. Provider HTTP/SSE error messages and
error-type fields are untrusted: route them through `internal/safetext` before
clients, logs, traces, health, telemetry, or persistence.

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
2. Write normal run events through the event recorder path: orchestrator code uses `r.emitRunEvent(...)`, and HTTP/API-owned writes use `internal/runtimeevents.Recorder`. Avoid direct `store.AppendRunEvent` calls outside storage tests and store-level run transitions, where terminal events or approval-resolution events must remain in the same transaction as their state mutation.
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

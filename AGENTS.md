# Hecate

Open-source local AI operations console for supervised agent work. Hecate
combines a model gateway, Hecate Chat, queued `agent_loop` tasks, External Agent
supervision, projects, memory, approvals, artifacts, usage, and OpenTelemetry
traces into one operator surface. Local-first here means the runtime and UI run
on the operator's machine by default, Hecate-owned state is local in local
deployments (`memory` / `sqlite`) and can use Postgres for hosted/cloud-runtime
deployments, and the gateway binds to 127.0.0.1 by default; Hecate can still
route to cloud providers and supervise external coding-agent CLIs with their own
accounts. The React operator UI is embedded via `//go:embed ui/dist`.

This file is the orientation entry — the codebase map, the runtime
invariants, and the gotchas that bite often. It is what an agent
(Claude Code, Codex, Cursor, or human) reaches for when starting work
on this repo. Conventions, workflow, verification, and longer-form
guidance live in [`docs-ai/`](docs-ai/README.md).

## Where guidance lives

| Surface                                                                                                           | What it carries                                                                                                                      |
| ----------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| [`docs-ai/`](docs-ai/README.md)                                                                                   | Canonical provider-neutral agent guidance — project context, conventions, workflow, verification, task shapes, area + posture skills |
| `AGENTS.md` (this) and `ui/AGENTS.md`, `internal/providers/AGENTS.md`                                             | Codebase map per area                                                                                                                |
| [`CLAUDE.md`](CLAUDE.md)                                                                                          | Claude Code compatibility shim importing `AGENTS.md`; no standalone rules                                                            |
| [`.github/copilot-instructions.md`](.github/copilot-instructions.md) and `.github/instructions/*.instructions.md` | GitHub Copilot compatibility shims pointing back to `AGENTS.md` and `docs-ai/`                                                       |
| [`docs-ai/skills/README.md`](docs-ai/skills/README.md)                                                            | Canonical skill set used by every agent                                                                                              |
| [`docs-ai/core/agent-guidance.md`](docs-ai/core/agent-guidance.md)                                                | Source-of-truth policy for keeping agent guidance provider-neutral                                                                   |
| [`docs/`](docs/)                                                                                                  | Long-form references (architecture, runtime API, events, telemetry)                                                                  |

When in doubt: read [`docs-ai/core/project-context.md`](docs-ai/core/project-context.md) and [`docs-ai/core/workflow.md`](docs-ai/core/workflow.md).

## Codebase map

```
cmd/hecate/            main runtime entry: gateway service, embedded UI, MCP subcommand
pkg/types/              public types (ChatRequest, Message, ContentBlock, ...)
                          — no internal/ imports

ui/                     React/Vite operator UI, embedded via //go:embed ui/dist
tauri/                  native desktop app (Tauri 2.x); wraps hecate as a sidecar,
                          webview loads http://127.0.0.1:{port}/ served by the gateway
scripts/
  release.ts            cut a release: pre-flight, goreleaser snapshot, Tauri
                          version stamp, tag, push  (`bun scripts/release.ts vX.Y.Z`)
  stamp-version.ts      stamp Tauri version files to current git tag / TAURI_VERSION
e2e/                    binary-startup tests; build tag e2e (sub-tags: ollama, docker)
docs/                   long-form references (architecture, runtime API, events, ...)
docs-ai/                canonical agent guidance (this file points there for depth)

internal/
  api/                  inbound HTTP shapes + handlers (OpenAIChatMessage, uppercase)
  apperrors/            shared app-layer validation/conflict wrappers used by
                          taskapp/chatapp/providerapp without coupling them to HTTP
  taskapp/              task lifecycle application layer used by API handlers:
                          creation defaults, load helpers, active-run guards,
                          approval resolution dispatch, runner calls
  taskschedule/         Task Schedule validation, CRUD semantics, durable
                          occurrence claiming/renewal, and due-dispatch loop
  providers/            outbound HTTP per provider (openAIChatMessage, lowercase)
                          — same JSON shape as api/, deliberate duplication
  gateway/              top-level request orchestration: governor → router → provider
  router/               provider/model selection, failover, retry, circuit
  governor/             policy + route gates + append-only usage events
  policy/               approval policy + provider/model allowlists
  catalog/, models/     provider catalog + model registry
  modelapp/             model listing, refresh, capability resolution, readiness
                          errors for API/chat callers
  modelcaps/            shared model capability table (streaming, tools, vision, …)
  modelprobe/           generation-bound manual tool-support verification state,
                          lease coordination, and safe outcome projection
  orchestrator/         task runtime: queue, runner, agent_loop, sandbox boundary
  codeintel/            native read-only code intelligence: fixed allowlisted LSP
                          and ast-grep subprocesses, protocol/result bounds,
                          workspace-confined normalization, process-tree cleanup
  workspacefs/          shared workspace path resolver for Hecate-mediated
                          file/search/write operations
  processrunner/        bounded local subprocess seam: cwd, env, timeout,
                          streaming output, output caps
  gitrunner/            Git-specific runner used by Hecate-owned Git helpers
  sandbox/              policy validation + OS isolation wrapper for tool
                          subprocesses; shell calls dispatch through ProcessRunner
                          and broad git_exec still runs through this executor
  taskstate/            task / run / step / artifact / approval persistence
  taskruncoord/         process-scoped origin-run admission and deletion fence
  workspacecoord/       process-scoped canonical workspace writer admission
                          and overlapping-root destructive-mutation closure
  agentadapters/        ACP/process adapters for Codex, Claude Code, Cursor
  eventprotocol/        agent-runtime event protocol v1 envelopes (API-facing shape)
  chat/                 chat transcript persistence (memory / sqlite / postgres)
  chatattachments/      session-scoped attachment bodies kept outside transcript JSON
  chatapp/              chat-session application layer used by API handlers:
                          create, external-agent prepare, native session cleanup,
                          session reads/rename, config option writes, Hecate
                          Chat settings, message admission/dispatch planning
  chatcontext/          pure context-packet lookup/decode/normalize helpers and
                          canonical ref builders shared by API context endpoints
  projects/             Hecate project view/domain types; portable persistence
                          belongs to embedded Cairnline
  projectapp/           project lifecycle application layer used by API handlers:
                          project delete cascade boundaries and cleanup authority
  projectskills/        Hecate project-skill view/discovery helpers; Cairnline
                          owns persisted metadata (no body injection or execution)
  projectwork/          project roles, work items, assignments, handoffs, and
                          collaboration artifact view/domain types
  projectworkapp/       project work application layer used by API handlers:
                          command shaping, id defaults, driver defaults,
                          store error boundaries, execution refs, activity
                          projection/status signals
  cairnlinebridge/      live mapping between Cairnline's portable coordination
                          model and Hecate project/runtime views
  projectruntime/       Hecate-owned task/chat refs, context snapshots, and
                          runtime overlays for Cairnline assignments
  projectassistant/     Project Assistant proposal domain: context building,
                          deterministic/model/bootstrap drafting, validation,
                          confirmed apply semantics
  projectassistantapp/  Project Assistant application layer used by API
                          handlers: cached service boundary, store/LLM wiring,
                          context/draft/propose/apply commands
  providerapp/          settings provider application layer used by API handlers:
                          settings status, policy rules, provider
                          create/update/delete, API key rotate/clear
  storage/              SQLite/Postgres SQL clients + dialect helpers
  retention/            retention worker (subsystems: traces, usage_events, audit, provider_history, model_call_events, chat_approvals)
  mcp/                  stdio MCP server (read tools + write tools)
  controlplane/         providers, secrets, settings state
  ratelimit/            per-key request limits
  requestscope/         per-request principal + tracing context
  config/, bootstrap/   env-driven config + startup wiring
  secrets/              env-var and file-based secret resolution
  telemetry/            OTel exporter wiring + span helpers
  profiler/             pprof endpoints + runtime stats
  version/              build-time version stamp
```

**Architecture rings** (cross-ring imports inward only):

```
pkg/types/  ←  internal/api/  ←  internal/providers/
                     ↑
              internal/orchestrator/  (sits above api, drives runs through providers)
```

The api↔providers parallel-struct duplication (`OpenAIChatMessage` ↔ `openAIChatMessage`) is intentional — it keeps `internal/providers/` free of `internal/api/` imports. Full rationale: [`docs-ai/skills/providers/SKILL.md`](docs-ai/skills/providers/SKILL.md).

**Storage tier rule**: every Hecate-owned backend-bound concern mirrors the
configured storage tiers — `memory` (fast/default), `sqlite`
(`modernc.org/sqlite`, no CGO, local durable), and `postgres` (`pgx`,
hosted/cloud durable). When adding a new Hecate-owned persisted thing, mirror
memory plus both SQL backends unless the operator explicitly scopes the work
differently. Portable Projects coordination is the exception: Cairnline owns
that SQLite-backed graph. Do not add Hecate-native copies, mirrors, backend
selectors, or migration routes for Cairnline-owned records.

## Runtime invariants

Non-negotiable rules of the system. Read them before writing code that
touches request handling, persistence, or tool execution.

- **Local operator boundary.** Every request is processed as the operator. The gateway binds to `127.0.0.1` by default; bind elsewhere only behind a reverse proxy, firewall, or equivalent access control.
- **Remote runtime route policy is explicit.** New Hecate-native `/hecate/v1/*` routes must be classified in `internal/api/remote_runtime_policy.go` as remote-safe or local-only. Unknown Hecate-native paths fail closed in remote mode, and the route coverage test must catch new registered routes; do not rely on a method-only denylist.
- **Remote local providers are opt-in.** Remote runtime mode disables `kind=local` model providers by default: presets are hidden, settings create/update rejects them, env-preconfigured local providers are skipped, and runtime-manager reload ignores existing local rows. Only `HECATE_REMOTE_ALLOW_LOCAL_PROVIDERS=1` enables an intentionally isolated sidecar. The policy is kind-based, not URL-based; custom `kind=cloud` provider destinations are controlled by the surrounding deployment/network boundary.
- **WorkspaceFS / runners are the workspace boundary.** Hecate-mediated file/search/write operations resolve paths through `internal/workspacefs`. Shell commands go through the sandbox executor and `internal/processrunner`; Hecate-owned Git helper calls go through `internal/gitrunner` where they do not need the broad `git_exec` shell-shaped interface. Native semantic/structural inspection belongs in `internal/codeintel`, which alone owns the fixed-argv LSP/ast-grep protocol, provider-trust, and process-tree semantics. Avoid raw `os.*` path access, raw `exec.Command`, or direct `git` subprocesses for workspace-bound behavior unless you are inside those seams or writing a narrowly scoped test.
- **Sandbox is per-call and applied inline.** Tool subprocesses run after policy validation + env sanitisation + output cap + wall-clock timeout. On Linux with `bwrap` installed and on macOS, the call is additionally wrapped by `bwrap` / `sandbox-exec` for filesystem and network confinement (auto-detected at startup). No separate sandbox daemon, no per-call rlimits (those would shrink the long-running gateway). New workspace tools follow WorkspaceFS / ProcessRunner / GitRunner / CodeIntel as appropriate.
- **Long-lived terminals own their process unit through drain.** `LocalWorkspace.OpenTerminal` must place Unix commands in a dedicated process group and Windows commands in a kill-on-close Job Object before user code can run. Wait/Close completion and workspace-writer lease release require the owned group/job to be empty and stdout/stderr pumps to drain; a caller deadline may return early, but must force best-effort termination and never release the lease early. Preserve ordinary Unix background jobs, `nohup`, and `disown` because they remain in the group. Reject known actual group/session escapes (including unsafe wrapper, job-control, alias/eval, inline-interpreter, and interactive-code forms) before execution. That Unix validation is static best effort: document sourced/generated/encoded code, arbitrary wrappers, custom syscalls/binaries, and external service managers as residual trusted-subprocess risk rather than claiming hard descendant containment.
- **Agent Preset launch posture must reach execution.** Project assignment launch plans validate that the resolved preset surface matches the selected driver. Native Hecate tasks snapshot the preset id and `tools_enabled`, map `writes_allowed` to the task's read-only sandbox flag, and map `network_allowed` to the task network flag. An explicit tools-disabled snapshot produces an empty native/MCP catalog, starts no MCP host, and fails closed on every unexpected tool call. Preset-backed native HTTP/search tools are not advertised and fail closed when the snapshot disables network. Read-only tasks omit and fail closed on broad shell, Git, file-write, and interactive-terminal surfaces while retaining structured inspection and proposal-only edits. Structured Git inspection must stay passive: use GitRunner's immutable read-only/offline metadata view, preserve safe platform settings such as `core.longpaths`, resolve nested workspaces against the repository top-level while scoping paths and results back to the workspace, disable optional index locks, lazy fetch, fsmonitor helpers, external diff/text conversion, and submodule recursion, resolve effective conversion attributes through bounded NUL-safe Git input/output, and fail closed when a scoped path has an effective or ambiguous content-conversion filter. Do not infer preset tool or native-network policy from absent snapshots or zero-valued sandbox flags on legacy/manual tasks. Keep this resolution in `projectworkapp`; Cairnline coordination intent never grants runtime permission.
- **Approvals are blocking and resolve atomically.** Pre-execution and mid-loop
  approvals halt the run; the run record persists in `awaiting_approval` until
  resolved. New gates use the `TaskApproval` shape. Approval or rejection must
  commit the pending decision, awaiting run/task transition, required run
  events, and rejection child cleanup through one taskstate transition with
  memory/SQLite/Postgres parity. Do not split approval mutation from run/task
  mutation in handlers or runners, and do not use a low-level approval update
  as a lifecycle shortcut.
- **Run start and cancellation own their concurrency boundaries.** Start,
  retry, resume, continue, and retry-from-model-call serialize per task in the runner,
  then use `taskstate.ApplyRunStartTransition` as the memory/SQLite/Postgres
  authority for the no-active-run check, monotonic budget raise, run number,
  task projection, and run insert. Do not split those writes back into
  read/check/update calls. Scheduled starts renew their claim at no more than
  one-third of its stale-owner window while provisioning, then atomically
  commit the Run, Task projection, occurrence result, initial lifecycle events,
  and any required pre-execution approval. Queue handoff after that commit must
  be idempotent and enter targeted reconciliation on failure. Task deletion
  uses the same store concurrency boundary, rejects every non-terminal Run,
  and cascades only after that check in one transaction. Cancellation persists its terminal winner before
  draining the executor, then reapplies child cleanup without duplicating the
  terminal event so a late step, streaming artifact, or approval cannot remain
  actionable on a cancelled run. That replay must preserve the authoritative
  task projection because a newer run may already own it. Boot reconciliation scans queued/running runs
  with a stable cursor until exhaustion; a page limit is not a recovery limit.
- **Events are appended, not mutated.** Every state transition writes a `run_event` with a monotonic sequence. The SSE stream replays from `after_sequence`. New event types must follow the event-protocol v1 taxonomy (`run.*`, `model.call.*`, `assistant.*`, `tool.*`, `policy.*`, `gap.*`, `error.*`) and be documented in `docs/runtime/events.md`.
- **Chat message idempotency commits before backing-turn dispatch.** `POST /hecate/v1/chat/sessions/{id}/messages` may carry a session-scoped `client_request_id`. Keep reservation, payload-fingerprint comparison, renewal lifecycle, and the atomic user-row commit behind `chatapp` and the chat store; do not implement handler-only or process-only deduplication. `chatapp` renews an owned pending reservation at intervals no greater than one-third of the stale-owner window across all pre-commit work, including semantic compaction, and verifies ownership once more immediately before commit. Same-key/same-payload retries return a freshly loaded authoritative session, repair any pending attachment claim for the already-committed user row before reporting success, and never redispatch backing work; changed-payload reuse fails closed with `chat.client_request_conflict`, and the key ledger stores no prompt, MCP secret, or attachment body. Preserve memory/SQLite/Postgres parity and ensure renewal loss or stale-owner takeover makes the displaced owner fail before the submitted turn's provider, task, or ACP dispatch. Admission-time semantic compaction is separate provider work and may precede that commit. Once an External Agent user row and running assistant are durable, the ACP turn uses a bounded server-owned context rather than the message request context. Browser or SSE disconnect ends only the waiter; the registered live-turn cancel hook, close/delete, and shutdown remain authoritative cancellation paths. The turn deadline remains authoritative but terminalizes an expired turn as failed. Stream, native-session replacement, approval, and terminal writes use the turn owner or fresh bounded persistence windows. Do not claim in-flight resume across a Hecate process restart; startup reconciliation still marks the running assistant interrupted.
- **Hecate Chat workspace posture is session-owned and task-enforced.** Hecate-owned sessions persist `persistent`, `ephemeral`, or `in_place`; backing tasks must receive the same value. Omitted API values retain the legacy `in_place` contract, while the operator UI explicitly chooses a safe managed-workspace default or the linked Cairnline Project default. A session may change posture only before any backing task exists. Every non-empty Hecate settings mutation uses a distinct exclusive admission that conflicts with turns, participates in lifecycle drain, invalidates pre-mutation turn snapshots, and is never exposed as a cancellable Chat Turn. SQL full-session read/modify/writes must share the chat-link serialization boundary and take a PostgreSQL session-row lock so stale metadata cannot erase a committed task/workspace link; PostgreSQL operations that touch both records must lock the session before messages. Managed Task Runs must atomically rebind the session, both message projections, and context packets to the generated Run workspace; review/files fail closed if a durable chat-origin task exists without that link, log store diagnostics internally, and return fixed operator-safe lookup errors. An exhausted or ambiguous link write must be reread before cancellation; a confirmed failure terminalizes the assistant, retains durable chat-origin cancellation recovery, blocks another turn while any unlinked origin Task Run remains active, and makes same-key replay return that terminal transcript without redispatch. Every recovery read uses a fresh bounded persistence context. When provider/model/MCP changes require a new task segment, reuse the already-managed root instead of cloning away its unstaged or untracked work. External Agent sessions always remain `in_place`; ACP adapters own their selected workspace directly. Do not turn Cairnline coordination defaults into runtime permission or duplicate them in a Hecate Projects store.
- **Ambiguous attachment uploads never auto-retry.** Attachment uploads do not yet have client idempotency keys or a draft-list recovery API. Treat network failures and every 5xx upload response as ambiguous: restore the exact local prompt and Files, preserve newer composer edits, delete only acknowledged drafts, and warn that one hidden copy may remain without exposing attachment ids or raw proxy bodies. Unlinked drafts become reclaimable after 24 hours only when a later upload runs reclamation; do not describe this as automatic wall-clock expiry. Chat deletion is the immediate cleanup path.
- **Pre-admission Chat Stop is a local turn fence.** Before the message POST hands any direct-model, task-backed, or External Agent turn to its runtime, Stop must synchronously cancel the exact chat-turn generation and abort its in-flight session create or file upload where possible. Cancellation ownership is an opaque monotonic token captured by that turn; every release must present the same token, so an older same-session unwind cannot cancel or clear a newer generation. A submitted implicit first turn may project busy/Stop while its session ID is unknown only when that exact turn owns the detached composer; preparatory creation without a submitted turn must not expose Stop. Pass one `AbortSignal` through session creation and upload, and recheck the local fence immediately before message dispatch even when a proxy or test double ignores it. If an ignored abort returns a known create acknowledgement, keep and bind the message-empty session shell and its create-time title metadata; never claim metadata rollback, and never continue to message, provider, task, or ACP dispatch after Stop wins. Withhold server cancellation until a non-empty session ID is bound and an authoritative busy snapshot confirms admission. Keep the local cancellation projection until the stopped submit unwinds, restore the exact prompt and Files when present, clean up only acknowledged drafts, and preserve the ambiguous-upload warning for an upload whose outcome cannot be proved.
- **ACP file disclosure closes at the final send boundary.** After staging External Agent files, complete the handle-bound identity audit and then recheck the turn context immediately before `session/prompt`; cancellation must clear in-memory prompt files and synchronously remove or transfer cleanup ownership without sending the resource link. Keep every body-free stage and quarantine namespace denied across later callbacks and turns until handle-bound cleanup proves removal. Resolve absolute, file-URI, and workspace-relative callback spellings against lexical and canonical roots. Hold the namespace read fence through the complete WorkspaceFS read/write fallback; alias registration takes its exclusive side before rename and reuses one pending quarantine name across failures. Retain body-free alias redactors for the ACP session lifetime; redact typed command/config display fields, suppress their unsanitized raw records, and drop an entry rather than changing a protocol identifier or value. Capture bounded adapter stderr only while initialization, session creation/load, and selected model/config setup can still fail. Once setup succeeds, zero that buffer under its writer lock and make every later process write a discard before any prompt can carry file data or staging paths. Native close/delete failures may log only a fixed classification and numeric RPC code, never peer-controlled message or data.
- **Destructive chat mutations fence the full session lifecycle.** Session delete, project delete, native-session close, and idle close must advance and hold the per-session `agentChatLive` lifecycle generation. Snapshot it before the first session read; snapshots are leases that every caller must release so inactive lifecycle state can be reclaimed. Every direct-model Turn, task-backed Turn, and External Agent Turn must register against that snapshot before message append, attachment claim/body hydration, or provider/task/ACP dispatch. Attachment persistence, attachment content lookup/write, and external-agent config writes are counted operations that recheck the snapshot immediately before mutation or hydration. Destructive closures serialize with each other, wait for admitted operations, and reread the authoritative session after acquiring ownership before using task/native fields. Advance the generation again on release so delayed work cannot reuse stale handles after admission reopens. Chat deletion must also close the shared `taskruncoord` origin gate, wait for every admitted Start/Retry/Resume/Continue/retry-from-model-call or approval-resolution mutation, cancel every non-terminal Task whose durable `origin_kind="chat"` and `origin_id` match the session, and hold that gate through durable session deletion. Keep Task history, commit successful owner deletion before releasing the fence, validate chat-origin existence on later execution mutations (including after restart), reclaim committed in-memory gate state only for an origin kind with durable validation, and fail the delete closed if cancellation cannot be confirmed.
- **Workspace discard coordinates every Hecate-owned writer.** Bind discard authority to the revision of one complete raw, scoped **unstaged tracked** Git patch (index → worktree) captured through GitRunner's passive view; never authorize from a trimmed, truncated, stat-only, or historical message diff. Before issuing any revision, fail closed when the scoped workspace has staged changes, whether staged-only or mixed with unstaged edits: the API returns `422 invalid_request`, and the operator must unstage and review again. Never represent staged-only state as a clean empty patch. This discard surface also does not authorize untracked-file deletion. Close and drain the owning session lifecycle first, then acquire the one shared process-local `workspacecoord` exclusive lease for the canonical workspace. Equality and ancestor/descendant roots overlap; siblings do not. Task provisioning/start admission and execution, External Agent turns and each live ACP terminal, native agent-loop terminals, task-patch apply/revert, and operator terminals must acquire writer leases from that same registry for the full period in which they can mutate files. After both closures are held, reread the session and scan durable non-terminal task runs and active chats for overlapping roots before recomputing the exact snapshot. Before mutation, reserve Git's conventional real-index lock, recheck scoped staged state and the reviewed patch's live index baseline, and conditionally reverse-apply that reviewed raw patch: index contention, committed or staged baseline changes, or overlapping later edits return `409` without mutation, while unrelated or non-overlapping edits survive. The registry is process-local and not a distributed lock; the transient index reservation coordinates only with well-behaved Git index writers. Keep the durable-owner scan, index reservation, and conditional apply as independent fail-closed protections and never claim replica-wide atomic exclusion.
- **Every Hecate chat-creation path uses the cross-store mutation gate.** Direct chat creation and project-assignment External Agent launch must snapshot the API mutation epoch at handler entry, then reserve creation through durable session creation, agent preparation, and assignment ownership linking. Project deletion closes that process-wide admission before cleanup. Before project chat cleanup, invoke the `chatapp` live orphan-attachment sweep so retries remove bodies left by transcript-first partial deletes; this sweep may compare attachment session IDs with authoritative transcript rows and delete missing owners, but must not reconcile pending claims. When Cairnline-authoritative deletion removes portable identity before Hecate cleanup, scope any rollback snapshot to that deleted project graph so unrelated concurrent Cairnline edits cannot be overwritten. Every project-scoped facade mutation must also hold shared admission on every affected process-local project key through Cairnline authority, Hecate cleanup/shadow work, and its response decision; deletion acquires the process-wide destructive closure first, closes that project key, waits admitted mutations, and holds both closures through rollback. Pathless Project Assistant draft/propose/apply flows derive and validate their complete scope before writing, including both sides of a chat-project move, and retain that lease through proposal ledgers and mirrors. Multi-project admissions normalize, deduplicate, sort, and acquire the complete key set atomically; nested calls may reuse a subset, but incremental expansion is forbidden. New same-project mutations fail with a conflict while deletion owns the key, and unrelated projects remain concurrent. Keep both gates in API composition, cover native and embedded Cairnline authority, and never recreate portable project identity in Hecate storage. The live system-reset endpoint must return `409 conflict` before mutation until a reversible runtime-wide quiescer can close and drain every write-capable HTTP request, task worker/reconciler/finalizer, retention pass, ACP callback, gateway finalizer, and Cairnline open. Existing project/chat gates or the already-quiesced cleanup helper are not authorization to enable online reset.
- **Cost is `int64` micro-USD when present.** Never `float64` for money. Hecate records usage events for visibility; it does not enforce global spend controls.
- **OTel is first-class.** Every request gets a trace ID surfaced in the `X-Trace-Id` response header and persisted on the run record. New code paths add spans, not just log lines.

## Conventions (in brief)

Full standards: [`docs-ai/core/engineering-standards.md`](docs-ai/core/engineering-standards.md).

- **Comments explain _why_**, not what. State the trade-off.
- **Pointer vs value for optional fields**: pointer when zero is a valid
  distinct value (`Seed *int`, `ParallelToolCalls *bool`); value with
  `omitempty` when zero == API default (`PresencePenalty float64`).
- **`json.RawMessage`** for forward-compat passthrough fields.
- **Test naming**: `TestPackage_Behavior`. Table-driven where the variant set is obvious.
- **No emojis**, no plan/phase labels in commit messages or comments.
- **Conventional Commits**; `chore(agent):` for agent-doc-only changes. Don't auto-commit — propose a message and let the operator merge.

## Verification

Full ladder: [`docs-ai/core/verification.md`](docs-ai/core/verification.md).

- **Race suite is the floor for runtime/backend changes**: `go test -race -timeout 10m ./...` or `just test-race`. Race builds are large; if your default `$GOCACHE` is on a small volume, point it at the repo: `GOCACHE="$(pwd)/.gocache" go test -race ...`.
- **Vet Go changes**: run `go vet` on touched packages during iteration; use `go vet ./...` for broad backend changes or release prep.
- **Iteration**: run focused `go test` / `bun run test` commands for touched areas.
- **Before creating or updating PRs**: identify and run the related tests for
  every touched surface first. If a required check cannot run, say why in the
  PR/update summary.
- **PR readiness includes coverage and docs**: before opening or updating a PR,
  confirm production-code changes added or updated the right tests, and confirm
  user/AI/runtime docs and related diagrams are updated or explicitly unchanged.
- **Written production code needs tests**: new behavior gets new tests, bug
  fixes get regression tests, and refactors keep behavior tests passing before
  and after the reshape.
- **E2E**: `go test -tags e2e ./e2e/...`. Build tag `e2e` always required; sub-tags `ollama`, `docker` opt in. `PROVIDER_FAKE_KIND=local` is useful for synthetic local-model scenarios.
- **UI**: `cd ui && bun run typecheck` then `bun run test`. Never `bun test` (skips testing-library DOM setup).

## Recipes

| Task                                                                        | Where                                                                                                                                                                               |
| --------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Add a passthrough wire field (the seven-step chain — most-redone task here) | [`docs-ai/skills/providers/SKILL.md`](docs-ai/skills/providers/SKILL.md)                                                                                                            |
| Add an MCP tool / persisted run-event type / test helper cheat-sheet        | [`docs-ai/skills/backend/SKILL.md`](docs-ai/skills/backend/SKILL.md)                                                                                                                |
| UI recipes (SSE-driven state field, paired pickers, snapshot refresh)       | [`docs-ai/skills/ui/SKILL.md`](docs-ai/skills/ui/SKILL.md)                                                                                                                          |
| Native desktop app (sidecar lifecycle, bundling, Tauri commands)            | [`docs-ai/skills/tauri/SKILL.md`](docs-ai/skills/tauri/SKILL.md)                                                                                                                    |
| Cut a release tag                                                           | `bun scripts/release.ts vX.Y.Z` — checks worktree, snapshot dry-run, stamps Tauri versions, tags, pushes. Full procedure: [`docs-ai/tasks/release.md`](docs/contributor/release.md) |
| Stamp Tauri version files                                                   | `bun scripts/stamp-version.ts` (or `just tauri-version`) — syncs Cargo.toml, package.json, tauri.conf.json to current git tag                                                       |

## Gotchas

- **`bun run test` ≠ `bun test`.** The latter skips the testing-library DOM setup and panics with `document[isPrepared]`. Always `bun run test` (which dispatches to vitest).
- **SQL timestamp storage**: SQLite TEXT timestamps must be written as
  `t.UTC().Format(time.RFC3339Nano)` when lexical ordering matters. Postgres
  does not accept empty-string timestamps; SQL stores that pass `time.Time`
  values should use the shared `storage.TimestampColumn*` helpers instead of
  ad-hoc TEXT columns.
- **OpenAI/openAI parallel structs are intentional**: don't unify. Mirror fields when adding on either side.
- **Streaming `wireReq` plumbing**: when adding a passthrough field, plumb it into BOTH `Chat` and `ChatStream` `wireReq` constructions in `internal/providers/openai.go`. Forgetting one is the most common provider bug — non-stream tests pass; the field silently drops in production for any client using `stream: true`.
- **Capability-cache seeding** for provider tests: seed `cachedCaps` and `capsExpiry` to skip the discovery path. Snippet in [`docs-ai/skills/providers/SKILL.md`](docs-ai/skills/providers/SKILL.md).
- **Synthetic local providers** in tests: `PROVIDER_FAKE_KIND=local` for synthetic models in e2e.
- **mermaid `loop` is a reserved keyword**: don't use it as a sequence-diagram participant name. Use `Agent` or similar.
- **CodeQL CWE-190**: don't compute `make([]T, 0, len(x)+N)` with arithmetic — use plain `len(x)` and let `append` grow.
- **Env-PRECONFIGURED gate**: `PROVIDER_<NAME>_API_KEY` / `_BASE_URL` only auto-import into the CP store when `PROVIDER_<NAME>_PRECONFIGURED=1` is also set. E2E helpers (`hecateServer`, `startHecateProcess`) funnel through `autoPreconfiguredEnv` to inject the gate; new e2e spawn helpers must do the same or routed requests 400 with `no provider supports model …`.
- **`:8765` collisions across launches**: `just dev` / `just run` / `just serve` now run `just stop` first so a stale `./hecate` from another shell never blocks a relaunch (or a `docker run -p 8765:8765 …`). New scripts that spawn the binary should call `just stop` (or replicate the `lsof -ti:8765 | xargs kill` step).
- **Alpha cleanup is intentional**: do not restore removed compatibility glue, legacy fallback chains, or handler-owned orchestration unless the operator explicitly asks for backwards compatibility. Use the canonical app seams, `execution_ref`, `turn_kind`, and view-model adapters documented in `docs-ai/`.
- **API response envelope**: every Hecate-native `/hecate/v1/*` GET returns `{object, data}`. Compatibility endpoints (`/v1/models`, `/v1/chat/completions`, `/v1/messages`) keep provider-shaped contracts. Don't write a UI client that reads top-level fields — always read `payload.data.<field>` for Hecate-native endpoints and make test fixtures mirror the real envelope.

## Canonical docs

| Doc                                                                    | Covers                                                                                         |
| ---------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| [`docs/contributor/architecture.md`](docs/contributor/architecture.md) | Request flow, lease semantics, storage tier matrix                                             |
| [`docs/runtime/agent-runtime.md`](docs/runtime/agent-runtime.md)       | `agent_loop` tools, system prompt layers, cost model, retry-from-model-call                    |
| [`docs/runtime/runtime-api.md`](docs/runtime/runtime-api.md)           | Task / run / step / approval endpoints, queue + lease                                          |
| [`docs/runtime/events.md`](docs/runtime/events.md)                     | Every event type at `/hecate/v1/events` with payload shapes                                    |
| [`docs/runtime/telemetry.md`](docs/runtime/telemetry.md)               | OTel spans + metrics, OTLP wiring, status & gaps                                               |
| [`docs/operator/security.md`](docs/operator/security.md)               | Local-first threat model, workspace safety, approvals, secrets, advisories                     |
| [`docs/operator/providers.md`](docs/operator/providers.md)             | Provider catalog, configuration                                                                |
| [`docs/runtime/mcp.md`](docs/runtime/mcp.md)                           | MCP server: tools, transport, configure                                                        |
| [`docs/runtime/external-agents.md`](docs/runtime/external-agents.md)   | External Agents: Hecate supervises Codex, Claude Code, Cursor Agent, and Grok Build from Chats |
| [`docs/operator/deployment.md`](docs/operator/deployment.md)           | Compose profiles, image pinning, lost-token recovery                                           |
| [`docs/contributor/development.md`](docs/contributor/development.md)   | Local build, testing, screenshot tooling, `[skip ci]` convention                               |
| [`docs/operator/desktop-app.md`](docs/operator/desktop-app.md)         | Native Tauri 2.x app: distribution, current state, roadmap, footguns                           |

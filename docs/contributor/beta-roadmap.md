# Pre-1.0 roadmap

Hecate ships stable `v0.x.y` releases while pre-1.0 work lands incrementally.
`v1.0.0` is a quality gate after core runtime contracts, project orchestration,
view-by-view UX polish, and cleanup/refactoring are complete.

`master` stays the protected integration and release branch. Pre-1.0 work
happens on feature branches forked from current `master`, lands through PRs,
and is released only after it merges.

## Core Runtime First

| Area                     | v1 bar                                                                                                                                                                                                                                                                |
| ------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Error contracts          | Hecate-native errors across gateway, model chat, Hecate Chat, External Agent, Tasks, approvals, storage, retention, and provider readiness return stable `type` values, correct HTTP statuses, trace IDs when available, friendly operator messages, and raw details. |
| Provider/model readiness | Provider readiness is the canonical setup contract: credential state, discovery state, discovered model count, selected-model validity, route blocking reason, tool capability, last probe/result, and repair action.                                                 |
| Routing explainability   | Route reports persist and expose selected route, skipped candidates, skip reasons, failover path, cache path, policy decision, provider latency/error state, and final error.                                                                                         |
| Task runtime hardening   | Queue, lease, running, awaiting approval, approved/rejected, cancelled, failed, completed, retry, resume, stale worker recovery, and shutdown behavior are audited and tested.                                                                                        |
| Project orchestration    | Projects provide the operator cockpit for project-scoped agent teams: roles, work items, assignments, native task launches, structured handoffs, activity health, memory candidates, and context readiness stay linked without replacing Tasks or Chats.              |
| Storage and retention    | Memory/SQLite/Postgres parity is verified for providers, chats, tasks, approvals, grants, retention pruning, startup reconcile, and schema migration safety.                                                                                                          |
| OpenTelemetry            | Route choice/skip, provider failure, cache hit/miss, task lifecycle, approval lifecycle, chat segment lifecycle, external adapter behavior, retention, rate limits, and readiness probes emit useful spans, metrics, or logs.                                         |
| Endpoint stability       | Hecate-native endpoints stay under `/hecate/v1/*`; provider-compatible endpoints stay under `/v1/*`; tests and docs checks prevent old `/admin/*` and accidental Hecate-native `/v1/*` routes from returning.                                                         |

## Runtime-wide reset quiescence (open)

The live `POST /hecate/v1/system/reset-data` endpoint returns a stable
`409 conflict` before mutation. Online reset stays disabled until one
top-level, reversible quiescence protocol can prove that every durable writer
has stopped, run the cleanup under an explicit reset context, advance its
generation, and reopen normal admission.

That protocol must cover all of the following, not only HTTP mutation routes or
project/chat gates:

- write-capable HTTP requests, including provider-health and project reads with
  reconciliation or open-time migration side effects;
- task queue claims, boot/periodic reconciliation, heartbeats, enqueue/ack, run
  execution, approvals, artifacts, events, and terminal finalizers;
- retention passes, pruning, and retention-history appends;
- External Agent ACP callbacks, stream/final settlement, approval and grant
  timers, available-command updates, terminal callbacks, and idle sweeping;
- every embedded Cairnline service open plus assignment/project reconciliation,
  runtime overlays, and compatibility projections;
- gateway usage, provider-health recovery, route/failover history, and response
  finalizers.

The quiescer must use reversible admission plus drain/join and epoch fencing so
delayed work from the old generation cannot write after reopen. It must not hold
full ACP turns, model calls, SSE streams, or ordinary request leases while
waiting on external work. Deterministic memory/SQLite/Postgres race tests must
cover pause, cancellation, cleanup failure, generation advance, and reopen.
Existing runner/session shutdown hooks are irreversible process-shutdown tools,
and the current project/chat gates cover only their own ownership boundaries.

## UX By View

| View          | v1 bar                                                                                                                                                                                                                                                                                                                                                                                                                          |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Chats         | Hecate Chat, External Agent, and direct model chat have clear segment boundaries, explicit managed-workspace/current-folder posture, queued prompts, busy state, task/trace/run links, approvals, markdown/code rendering, run activity grouping, changed-files review, bounded image/file inputs with explicit disclosure and download behavior, model/tool capability state, stale-model repair, and refresh/resume accuracy. |
| Connections   | Provider setup is self-explanatory: readiness cards, discovered/running/installed states, credential repair, duplicate endpoint handling, route blocking reasons, model discovery failures, local provider discovery, and optimistic edits/deletes where safe.                                                                                                                                                                  |
| Projects      | Projects is the orchestration cockpit: project identity, defaults, roles, work items, assignments, handoffs, activity inbox, needs-attention triage, timeline/decision log, and memory/context inspection are compact, responsive, and connected to launch/review actions.                                                                                                                                                      |
| Tasks         | Task Detail is the canonical deep-debug view: clear run timeline, grouped advanced activity, approval cards, stdout/stderr/artifacts, retry/resume/cancel explanations, patch review, and chat-origin links.                                                                                                                                                                                                                    |
| Observability | The UI answers "what happened?" without JSON archaeology: request history, route report, trace viewer, skipped providers, policy denial, usage, cache path, provider failure, and final outcome.                                                                                                                                                                                                                                |
| Settings      | Settings stays focused on retention and OTel/export knobs when needed. Provider readiness and External Agent setup/grants live in Connections.                                                                                                                                                                                                                                                                                  |
| Usage         | Usage clearly separates Hecate-measured cloud-provider tokens and known/reported cost from adapter-reported external-agent usage. There are no global spend controls.                                                                                                                                                                                                                                                           |
| Desktop app   | Before v1, decide whether the desktop surface enters v1 with the rest of Hecate or remains explicitly experimental per platform. macOS Apple Silicon is signed, notarized, and launch-tested; Linux and Windows bundles are CI-built but not yet manually exercised on real machines.                                                                                                                                           |

## Cleanup And Refactoring

- **Shared transcript/activity primitives**: model chat, Hecate Chat, External
  Agent chat, and Task Detail converge on one shared transcript/activity
  component family.
- **Canonical readiness model**: one provider/model readiness type feeds API,
  UI, docs, tests, and trace/telemetry labels.
- **Task/chat boundaries**: Tasks remain canonical for full run history and
  patch review; Hecate Chat projections stay accurate and linked. One chat can
  have many historical segments but only one active task-backed loop.
- **Project orchestration boundaries**: Projects coordinate roles,
  assignments, handoffs, memory, and activity health; Project Assistant drafts
  bounded proposals; the orchestrator executes approved work. Projects do not
  become a separate execution engine or hosted project-management system.
- **Docs structure**: operator docs, runtime references, contributor docs,
  design records, and `docs-ai` guidance describe the same product and same
  caveats.
- **Remove stale legacy**: old endpoint names, old `/gateway` language,
  one-binary claims, stale screenshots, obsolete scripts, dead fixtures, and
  duplicate UI rendering paths are removed.
- **Release automation**: stable releases stay boring: docs drift checks,
  screenshots, links, Docker/native/binary parity, Tauri version stamp,
  release-doc refresh, and recovery instructions.

## Branching And Release Workflow

- Create a feature branch from current `master` for every pre-1.0 slice, for
  example `feature/provider-readiness-contracts` or
  `refactor/shared-transcript-activity`.
- Rebase feature branches onto `origin/master` before opening or updating PRs.
- Merge only reviewed PRs into `master`; do not implement pre-1.0 features
  directly on `master`.
- Use direct `master` commits only for release mechanics or urgent tiny
  corrections that are explicitly requested, such as version stamps,
  release-reference docs, or failed-release recovery.
- Cut stable releases from `master` after completed PR slices merge and
  `just verify` passes.
- Do not create a long-lived stabilization branch unless the v1 gate later needs a
  freeze window. Until then, `master` remains the stable release train and
  integration branch.

## v1 Gate

Hecate can move from stable pre-1.0 releases to `v1.0.0` only when all of these
are true:

- Runtime errors are stable and friendly across the main surfaces.
- Provider/model readiness can explain and repair common setup failures.
- Routing decisions are inspectable in traces/UI.
- Hecate Chat supports mixed direct/model and tools-on task-backed segments
  reliably, with workspace posture persisted and enforced by the backing task.
- Projects can coordinate project-scoped work through roles, assignments,
  handoffs, activity health, timeline/decision signals, and memory/context
  inspection without hiding the canonical Task/Chat execution records.
- External Agent sessions have approvals, readiness, diagnostics, guardrails,
  diff inspect/revert, and trusted-subprocess warnings.
- Task runtime lifecycle is tested for approval, cancel, retry/resume, stale
  worker recovery, and shutdown.
- Memory/SQLite/Postgres persistence and retention boundaries are tested and documented.
- OTel covers the important runtime decisions.
- UI polish passes are complete for Chats, Connections, Projects, Tasks,
  Observability, and Settings.
- README, known limitations, runtime API docs, release docs, screenshots, and
  `docs-ai` guidance all describe the same product.
- Latest stable release passes `just verify`, release workflow, links workflow,
  and desktop release matrix.

## Test Plan

| Layer             | Coverage                                                                                                                                                                                                                                                            |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Go unit/API tests | Error contracts, provider readiness, route reports, task lifecycle, storage parity, retention, observed model capabilities, approvals, endpoint namespace checks.                                                                                                   |
| Go e2e tests      | Docker startup, OTLP smoke, ACP smoke, approval persistence/reconcile, provider readiness scenarios, task retry/resume/cancel, release-critical startup paths.                                                                                                      |
| UI unit tests     | Shared transcript components, friendly errors, readiness cards, chat segment state, workspace posture, queued prompts, approvals, task links, provider repair states.                                                                                               |
| Playwright e2e    | First-run provider onboarding, stale selected model repair, Hecate Chat tools on/off/on, task approval in Chats, refresh during running/awaiting approval, External Agent file input + approval + diff inspect/revert, provider readiness repair, trace/task links. |
| Release checks    | `just verify`, links, screenshots, Docker image pull/run, release asset presence, Tauri matrix, README release links.                                                                                                                                               |

## Assumptions

- Stable releases continue normally while this work is in progress.
- `v1.0.0` is a quality gate, not a marketing rename for the next release.
- No feature work lands directly on `master`; work is done in feature branches
  and merged through PRs.
- No compatibility shims are required for pre-1.0 endpoint/API changes unless
  explicitly decided later.
- Hecate remains single-operator shaped for beta, with local and remote
  deployments supported; multi-node coordination stays out of scope.
- External Agent CLIs remain trusted subprocesses for beta; Hecate supervises
  and warns, but does not sandbox their internals.

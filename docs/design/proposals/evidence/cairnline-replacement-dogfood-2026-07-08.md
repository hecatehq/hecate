# Cairnline Replacement Dogfood Rehearsal — 2026-07-08

Evidence for [`cairnline-portable-project-coordination.md`](../cairnline-portable-project-coordination.md).
Branch `feat/cairnline-mirror-observability` (commit `3c668c6`). All flows were
driven through the HTTP API against a locally built `cmd/hecate` gateway. This
rehearsal exercises armed embedded replacement mode (Cairnline authoritative for
all portable project-coordination families) and uses the new
`mirror_write_health` observability as evidence.

## Summary verdict

**What works.** Armed embedded replacement mode is functional end to end. Every
portable write family (projects, roots, roles, work-items, assignments,
artifacts, handoffs, memory, memory-candidates) creates, updates, and — in live
authoritative mode — deletes/renames correctly through the Cairnline-first write
path, with responses labelled `read_backend: "cairnline"`. Strict embedded reads
serve the portable read model. Assignment status transitions, launch readiness,
preflight context packets, and native `hecate_task` assignment start all work
once a default model is configured. With a default model set,
`replacement_ready` reaches **`true`** with `cutover_ready: true` and all six
gates ready. The new `mirror_write_health` observability was demonstrated
catching, blocking on, and recovering from a real induced mirror failure.

**What breaks / is lossy.** Four fidelity gaps are proven below:

1. **Lossy execution-ref mapping** — Cairnline's `assignments` schema collapses
   the execution ref to `run_id` + `context_snapshot_id`; `task_id` and `kind`
   are dropped from the portable row and survive only in Hecate's
   `hecate_project_assignment_runtime` overlay. A true cutover that drops the
   overlay loses `task_id`.
2. **Context packets are not portable through the Cairnline read path** — the
   Cairnline read-model launch packet for an assignment omits project memory;
   only Hecate's native `hecate_task` preflight packet injects memory.
3. **`awaiting_approval` is not representable** — the bridge clamps
   `awaiting_approval` to Cairnline's `running`; the portable status vocabulary
   has no awaiting-approval state.
4. **Additive-only is a property of the sync-rehearsal path, not live writes** —
   in live authoritative mode deletes/renames DID propagate correctly (no stale
   rows). The additive/refresh limitation applies to the standalone snapshot
   `POST /cairnline/sync` path.

**Also proven:** the readiness gates are necessary but **not sufficient** —
`replacement_ready: true` was reached while fidelity gaps 1–3 remain, because no
gate inspects execution-ref fidelity, context-packet portability, or status-enum
representability.

## Setup

| Setting | Full-mode run | Observability run |
| --- | --- | --- |
| `HECATE_ADDRESS` | `127.0.0.1:8899` | `127.0.0.1:8899` |
| `HECATE_BACKEND` | `sqlite` | `sqlite` |
| `HECATE_PROJECTS_COORDINATION_BACKEND` | `cairnline` | `cairnline` |
| `HECATE_PROJECTS_CAIRNLINE_CONNECTOR` | `embedded` | `embedded` |
| `HECATE_PROJECTS_CAIRNLINE_READ_SOURCE` | `embedded` | `auto` |
| `HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE` | `embedded` (armed) | `disabled` |
| `HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY` | `all-portable` | `project-roles,project-memory,project-collaboration` (partial) |
| Model provider | stub OpenAI-compatible server (`mock-model`) for start flows | none |

Env-gate source of truth: `internal/config/config.go:576-585` and validation at
`:669-691`. Armed embedded replacement mode requires the four gates in lockstep:
`COORDINATION_BACKEND=cairnline` + `CONNECTOR=embedded` + `READ_SOURCE=embedded`
+ `REPLACEMENT_MODE=embedded`, which implies all portable write-authority
switchpoints (`config.go:293-312`).

The observability run uses **partial** write authority on purpose: see
"Observability evidence" for why full all-portable authority cannot exercise the
`mirror_write_health` shadow-mirror path.

## Per-family results

Driven against project `Atlas Gateway Hardening` (`proj_e961…`) in full armed
mode. "Mirrored OK" = the record is present and correct in the embedded
Cairnline SQLite DB. "Read parity" = the record reads back correctly through the
strict embedded read routes.

| Family | Create | Update | Delete | Mirrored OK | Read parity | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| projects | ✅ 201 | ✅ 200 (desc + rename) | ✅ 200 (victim) | ✅ | ✅ | `read_backend: cairnline` |
| roots | ✅ 201 | — | — | ✅ | ✅ | Hecate still owns Git/workspace scan |
| roles | ✅ 201 (×3) | ✅ 200 | ✅ 204 (temp) | ✅ | ✅ | built-in roles listable/immutable |
| work-items | ✅ 201 | ✅ 200 (status transitions) | ✅ 204 (stale) | ✅ | ✅ | statuses backlog/ready/running/review/blocked exercised |
| assignments | ✅ 201 | ✅ 200 (status) | ✅ 204 | ✅ (partial) | ✅ | execution-ref lossy — see Probe 1 |
| artifacts | ✅ 201 (decision_note, evidence_link, review) | n/a (immutable) | n/a | ✅ | ✅ | update/delete absent by design |
| handoffs | ✅ 201 | ✅ 200 (status→accepted) | — | ✅ | ✅ | |
| memory | ✅ 201 | ✅ 200 | ✅ 204 | ✅ | ✅ | |
| memory-candidates | ✅ 201 | promote ✅ / reject ✅ | — | ✅ | ✅ | promotion creates durable memory |

Assignment-start flows: `hecate_task` native start succeeded through the task
runtime (produced `task_id`/`run_id`/`trace_id`, model `mock-model`).
`external_agent` assignment start requires an agent preset with
`external_agent_kind`; without a configured external adapter it stops at
preflight (`external-agent assignment requires an agent preset with
external_agent_kind`). A real `awaiting_approval` run could not be forced: the
sandbox reports `os_isolation: none` (no `bwrap`) and no approval policy is
configured, so the stubbed shell tool auto-completed. The `awaiting_approval`
mapping gap is instead proven at the persistence layer (Probe 3).

## Weak-spot probes

### Probe 1 — Lossy execution-ref mapping

Assignment `asgn_1bd3…` was created with a full ref
`{kind: task_run, task_id, run_id, context_snapshot_id}`. The raw Cairnline row
keeps only `run_id` and `context_snapshot_id`; `task_id` and `kind` are dropped
from the portable schema:

```
# Cairnline assignments row (execution_ref, context_snapshot_id):
('run_dogfood_0001', 'ctx_dogfood_0001')

# task_id survives ONLY in Hecate overlay hecate_project_assignment_runtime:
{"kind":"task_run","task_id":"task_dogfood_0001","run_id":"run_dogfood_0001","context_snapshot_id":"ctx_dogfood_0001"}

# API response reassembles the full ref from the overlay (looks complete):
{"kind":"task_run","task_id":"task_dogfood_0001","run_id":"run_dogfood_0001","context_snapshot_id":"ctx_dogfood_0001","status":"queued","missing":true}
```

Cairnline `assignments` schema has a single `execution_ref TEXT` column plus
`context_snapshot_id TEXT` — there is no `task_id` column. Fidelity is currently
preserved only because Hecate keeps a parallel runtime overlay row. A migration
that retires the overlay loses `task_id` and `kind`. The `"missing":true` flag
also shows the projected task/run does not exist (synthetic ids), which is
correct behavior, not the gap.

### Probe 2 — Dropped context packets (project memory not portable)

Two project memory entries exist (`Timeout invariant`, `Retry ceiling`). The
assignment context packet served through the Cairnline read path
(`GET …/assignments/{id}/context`) contains no memory items:

```
item kinds: project, work_item, assignment, project_root, role, cairnline_assignment_context(included=false)
mentions "Timeout invariant": False   mentions "Retry ceiling": False
```

By contrast the native Hecate `hecate_task` preflight packet
(`GET …/assignments/{id}/preflight`, which runs Hecate's own launch composer)
DOES inject both memory entries:

```
item kinds: …, memory, memory, launch_readiness, cairnline_launch_packet, …
mentions "Timeout invariant": True   mentions "Retry ceiling": True
```

The Cairnline read-model launch packet marks its assignment-context row
`included: false` ("Read-model preview only … no task/chat execution snapshot").
Project memory bodies flow into a runnable prompt only via Hecate's native
launch path, not the portable Cairnline context read.

### Probe 3 — `awaiting_approval` status mapping

`PATCH` an assignment to `status: awaiting_approval`, then read it back — it is
`running`:

```
PATCH status=awaiting_approval  →  readback status = "running"
```

Root cause, `internal/cairnlinebridge/bridge.go:401-413`:

```go
func AssignmentStatus(status string) string {
    switch strings.TrimSpace(status) {
    case projectwork.AssignmentStatusRunning, projectwork.AssignmentStatusAwaitingApproval:
        return cairnline.AssignmentRunning        // awaiting_approval collapses into running
    ...
```

Cairnline's status vocabulary (`cairnline/internal/core/types.go`) has no
awaiting-approval state (it exposes `awaiting_review`). An assignment blocked on
a Hecate approval is indistinguishable from a running one in the portable store.
The Activity Inbox `blocking_signal=awaiting_approval` and pending-approval
counts are Hecate-runtime projections layered on top; the portable row cannot
carry that state on its own.

### Probe 4 — Additive-only snapshot import vs live deletes

In **live armed replacement mode** deletes and renames propagate correctly — no
stale rows:

```
DELETE victim project     → cairnline projects row count for victim = 0
DELETE work item (stale)  → cairnline work_items row count = 0
DELETE role (temp)        → cairnline roles row count = 0
PATCH  project rename      → cairnline projects.name = "Atlas Gateway Hardening (renamed)"
```

So the weak spot as literally stated ("deleting/renaming leaves a stale row") is
**not reproduced** on the live authoritative write path — that path issues real
Cairnline deletes/updates. The additive/refresh concern is a property of the
standalone snapshot rehearsal path `POST /hecate/v1/projects/cairnline/sync`,
which "replaces the same embedded sync database after the Hecate snapshots have
been loaded" (runtime-api.md) — i.e. a full refresh keyed off the current Hecate
graph rather than an incremental import. A Hecate-authoritative source with a
subsequently-diverged Cairnline target would only reconcile on a full re-sync.

## Observability evidence

**Why full all-portable authority cannot exercise `mirror_write_health`.** With
a family's write authority enabled, the mutation is an *authoritative* Cairnline
write: if it fails, the request fails (`500 gateway_error`) and never reaches the
best-effort shadow-mirror hook that the health tracker instruments. Confirmed by
making the embedded DB immutable (`chattr +i`) under full mode:

```
create work-item (all-portable authority) → 500 gateway_error
  "migrate sqlite: attempt to write a readonly database (8)"
```

The `mirror_write_health` tracker only records on the `mirrorProject*ToCairnline`
best-effort path (`handler_project_cairnline_mirror_health.go`), which runs when
a family is Hecate-authoritative and Cairnline is a compatibility shadow — i.e.
the transitional dual-write posture, not full replacement.

**Induced failure under partial authority.** Second run: authority for
roles/memory/collaboration only, so `work-items` is a best-effort shadow family.
A healthy write records success; then the embedded DB was made immutable and two
`work-items` creates were issued — both returned **201** (Hecate authoritative
succeeded) while the Cairnline mirror silently failed. The observability caught
it:

```json
// GET /hecate/v1/projects/backend-status  → data.mirror_write_health
{
 "total_failure_count": 2,
 "drifting_families": ["work-items"],
 "families": [
  {"family":"projects","failure_count":0,"drifting":false,"last_success_at":"…"},
  {"family":"work-items","failure_count":2,
   "last_error":"migrate sqlite: attempt to write a readonly database (8)",
   "last_failed_operation":"project_work_item_create",
   "last_failure_at":"2026-07-08T12:10:50Z","last_success_at":"2026-07-08T12:10:35Z",
   "drifting":true}
 ]
}
```

The `mirror-write-health` replacement gate flipped to block:

```
mirror-write-health gate: ready=false status=drifting
  "Cairnline shadow-mirror writes recorded 2 failure(s) … these write families
   have not mirrored successfully since their last failure: work-items."
```

**Recovery.** After clearing immutability, a single successful `work-items`
mirror write cleared the drift while retaining the cumulative failure count:

```
work-items now: drifting=false failure_count=2 last_success_at=2026-07-08T12:11:11Z
mirror-write-health gate: ready=true status=recovered
```

This is exactly the before/after the observability change targets: previously
these two failures were only `slog.Warn` lines; now operators see per-family
failure counts, last error/operation, drifting families, and a gate that blocks
`replacement_ready` while drift is outstanding.

## Final `replacement_ready` verdict

Captured in full armed embedded replacement mode after configuring a default
model on the project:

```json
{
  "configured_backend": "cairnline",
  "authoritative_backend": "cairnline",
  "replacement_mode": "embedded",
  "replacement_mode_armed": true,
  "replacement_ready": true,
  "mirror_write_health": { "total_failure_count": 0 },
  "replacement_gates": [
    {"id":"read-routes","ready":true,"status":"ready"},
    {"id":"strict-embedded-read-smoke","ready":true,"status":"verified"},
    {"id":"write-authority-switchpoints","ready":true,"status":"ready"},
    {"id":"mirror-write-health","ready":true,"status":"healthy"},
    {"id":"migration-and-rollback","ready":true,"status":"ready"},
    {"id":"embedded-replacement-mode","ready":true,"status":"armed"}
  ],
  "migration_rehearsal": { "status":"verified", "cutover_ready": true, "authoritative": true }
}
```

Note: before a default model was configured, `strict-embedded-read-smoke`
reported `failed` ("project assignment start requires a default model") because
the smoke's assignment-preflight check needs a resolvable model
(`internal/projectworkapp/launch_plan.go:164-166`, model resolved from
role/project defaults then `Router.DefaultModel`). This makes
`replacement_ready` for embedded assignment-bearing projects contingent on a
routable default model, not just on portable-state fidelity.

## Gaps proven by this rehearsal (do NOT build here — follow-ups)

- **One-way migration-cutover path.** `write_adapter_gaps` and
  `migration_blockers` both still list `migration-cutover`; Probe 1 shows
  `task_id`/`kind` live only in a Hecate overlay, and Probe 4 shows the sync
  path is a full refresh. A durable, overlay-independent cutover that preserves
  the full execution ref and reconciles deletes incrementally is needed but out
  of scope. Evidence: `differences`/`id_differences` in mirror-parity show
  `hecate: 0` for every family in replacement mode (authoritative rows have
  moved to Cairnline; the snapshot-parity comparator has no Hecate side to
  diff), so parity as currently computed is not a cutover-fidelity signal.
- **MCP error codes.** The sidecar/MCP contract paths (`sidecar-*` smokes) were
  not exercised (no standalone Cairnline MCP process). The proposal's Future
  Test Plan calls for MCP contract tests incl. conflict/claim races; typed
  sidecar error-code coverage is needed but out of scope here.
- **Approvals-enum change.** Probe 3 proves the portable status vocabulary
  cannot represent `awaiting_approval` (clamped to `running`). Faithful
  supervised-approval semantics in the portable store would require an
  approvals/status enum extension in Cairnline — needed but out of scope.
- **Context-packet portability.** Probe 2 shows project memory does not flow
  through the Cairnline read-model launch packet. Making the portable launch
  packet carry memory/context bodies (or a documented decision that it never
  will, keeping Hecate's native composer authoritative for runnable prompts) is
  a follow-up.

## Artifacts

Raw request/response captures live in the session scratchpad
(`…/scratchpad/evidence/`), including `30-backend-status-final-fullmode.json`
(final verdict), `33-backend-status-fault.json` / `34-backend-status-recovered.json`
(observability lifecycle), and per-family create/update/delete responses.

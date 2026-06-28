# Projects

> **Status:** accepted; Projects V1 local cockpit substrate implemented.

Current source of truth: [Agent runtime](../../runtime/agent-runtime.md), [Chat sessions](../../runtime/chat-sessions.md), [Architecture](../../contributor/architecture.md)

Projects V1 is the durable local cockpit for project-scoped work: roots,
defaults, memory/context, skills metadata, roles, work items, assignments,
handoffs, reviews, evidence, activity, and context inspection. Remaining
near-term work should be beta hardening and dogfood-driven UX polish. Planner /
Manager agents, runbooks, browser QA, automatic memory promotion, skill-body
injection, and team project management should be handled as separate proposals
instead of expanding this foundation document.

## Summary

Hecate should distinguish **Projects** from **Workspaces**.

A project is the durable Hecate identity for a codebase or work area. It owns memory scopes, chat/task grouping, default runtime choices, trusted context sources, and agent-profile defaults. A workspace is a concrete filesystem root used by one chat, task, run, or external-agent session.

Today Hecate often uses a raw workspace path as both identity and runtime location. That works for early local flows, but it becomes confusing once we add durable memory, imported contexts, multiple checkouts of the same repo, temporary task workspaces, editor-owned workspaces, and future assistant layers.

## Problem

`workspace` currently carries too many meanings:

- The directory where a task or agent is allowed to read/write.
- The UI label for where a chat is happening.
- The implicit scope for memories or instructions.
- The thing future agent profiles would likely attach to.
- Sometimes a source checkout, sometimes a temporary clone, sometimes an in-place working tree.

Raw paths are not stable enough to be the durable identity:

- A repo can move on disk.
- The same repo can have multiple clones.
- A task can run in an isolated clone while the user thinks of it as the same project.
- Native app, web, Docker, and editor-owned ACP workspaces can expose different paths for the same logical work.
- Future project memory should not silently split because the path changed.

## Terminology

| Term           | Meaning                                                                                                                                                      |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Project        | Durable Hecate object representing a codebase or work area. Identified by `project_id`. Owns defaults, memories, history grouping, and context sources.      |
| Workspace      | Concrete filesystem root used for execution. A project can have one or more workspaces over time.                                                            |
| Project root   | A saved checkout/workspace path for a project. Roots can represent the main checkout, a linked Git worktree, an editor-owned workspace, or a temporary root. |
| Chat           | Conversation attached to an optional project and, when running, a concrete workspace.                                                                        |
| Task           | Durable runtime object attached to an optional project and a concrete workspace mode.                                                                        |
| Run            | One execution attempt under a task. Runs never define project identity by themselves.                                                                        |
| Agent profile  | Reusable agent configuration for Hecate Chat or an external agent: model/adapter controls, tools, memory sources, system instructions, and safety posture.   |
| Preset         | Template for creating or updating a project default or agent profile. Presets are not used directly at runtime once applied.                                 |
| Context packet | A snapshot of what Hecate assembled for a model/agent call, including project and workspace metadata.                                                        |

## Goals

- Add stable project identity independent of raw filesystem paths.
- Make project memory a first-class durable scope.
- Give Hecate Chat, Tasks, and External Agents a shared grouping model.
- Coordinate project-scoped agent teams through roles, assignments, handoffs,
  project activity, and reviewed memory/context without replacing Tasks or
  Chats as execution surfaces.
- Keep workspace modes explicit: in-place, isolated clone, temporary workspace, editor-owned workspace.
- Treat branches and Git worktrees as concrete root metadata, not project
  identity. A project can span the main checkout and linked worktrees while
  preserving one memory/context/work history.
- Let project defaults feed new chats and tasks: provider, model, agent profile, tools, command-output compaction, approval posture, workspace mode, and system prompt where applicable.
- Let context assembly use project-level sources: project instructions, selected docs, saved memories, and trusted files.
- Let Hecate Chat and external-agent chats share project memory when their active agent profile opts into it.
- Make UI history clearer: “these chats/tasks belong to this project,” not just “these happened under similar-looking paths.”

## Non-goals

- Hosted multi-tenant project management.
- Team permissions, sharing, or organization policy.
- Replacing task workspaces or sandboxing.
- Automatically cloning or syncing repositories.
- Importing private memory from external agents.
- Synchronizing external-agent private memory into Hecate memory.
- Treating a project as a billing/accounting boundary.

## Cairnline Extraction

Cairnline is the future portable coordination core for Projects-like state. It
is not the active Hecate Projects backend yet. Hecate currently remains the
runtime authority for project APIs, UI, task execution, approvals, external
agent supervision, traces, and context-packet rendering.

The current `internal/cairnlinebridge` package is a replacement-readiness proof.
It can load Hecate project state from the current project/profile/skills/work
stores, then seed a memory-backed or SQLite-backed Cairnline service from
Hecate project-shaped records:

- project identity, roots, default root, project default profile/execution
  posture references, and context-source provenance metadata including format,
  scope, source category, and trusted metadata labels;
- agent profiles and project/role execution posture;
- skills metadata, roles, work items, and root-scoped assignments;
- assignment-scoped collaboration evidence links, reviews, handoffs with
  source/target refs and linked artifacts/memory/context, accepted memory
  entries, memory candidates with decision state, and portable Project
  Assistant proposal ledger records with root/default-root actions, warnings,
  apply results, and attempts.

This bridge proves the portable Cairnline model can receive the core
coordination graph and produce assignment launch packets with the expected
metadata. The experimental read model also reports how many seeded assignments
can produce a portable launch packet plus any packet warnings or
per-assignment packet errors. It also exercises Cairnline's read-only closeout
readiness, project operations brief, and project activity projection against
seeded Hecate work state. It deliberately does not switch storage, proxy live
API requests, replace Hecate task/external-agent execution, migrate existing
local data, or make Cairnline authoritative.

For operator-triggered experiments, Hecate exposes local-only Cairnline bridge
endpoints. `GET /hecate/v1/projects/backend-status` reports the configured
coordination backend, whether the Cairnline read adapter can project the full
current Hecate project graph, the live read-route families currently projected
from Cairnline, the remaining write-adapter gap families, and whether Cairnline
is actually authoritative. It also reports `replacement_ready`,
`replacement_gates`, and `write_switchpoints` so operator tools can distinguish
ready read routes from strict embedded probe work, non-authoritative live
mirrors, still-Hecate-owned dispatch, and missing migration/rollback authority.
Today, `HECATE_PROJECTS_COORDINATION_BACKEND=cairnline` is a
replacement-readiness intent flag only: when the current stores are fully wired
it reports `cairnline_read_routes_ready`, and the live project activity inbox
plus project list/detail, setup readiness, health, skills, memory entries,
memory candidates, roles, work-item list/detail, assignment-list,
assignment-context, launch-readiness, assignment-preflight, artifact-list,
handoff-list, Project Assistant context and proposal reads, closeout readiness,
and operations brief can use the Cairnline read model for portable setup/work
state. Configured read routes prefer the embedded Cairnline mirror database
when it already contains the requested project or proposal record; if the
mirror database, project row, or proposal record is missing, they fall back to
the snapshot-seeded in-memory bridge projection.
`HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=snapshot` forces that snapshot-seeded
bridge; `HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded` requires the embedded
mirror database and requested project row or proposal record so
replacement-readiness gaps fail loudly during dogfood. Activity, work-item
list/detail,
assignment-list, and operations brief reads render work items, assignments,
roles, artifacts, and handoffs from the Cairnline service records, then overlay
Hecate-only runtime refs/timestamps where Hecate still owns execution.
Project identity and some compatibility scaffolding still come from Hecate
until Cairnline becomes authoritative. Project Assistant draft generation also
uses the Cairnline-projected
context so preview and proposal assembly stay aligned, while the proposal
ledger and apply remain Hecate-owned. Project list/detail reconstruct default
profile and execution posture from Cairnline project/execution-profile records
where available. Launch-readiness and assignment preflight read
project/work/assignment/role coordination records from Cairnline when
configured, then apply Hecate runtime validation. Native assignment preflight
and start context packets can append inspect-only `cairnline_launch_packet`
evidence so replacement reviews can compare Hecate's authoritative launch
context with the portable launch packet Cairnline can build for the same
assignment. Hecate stores remain authoritative, Hecate-specific runtime
enrichment and setup/action wording remain in Hecate, and the Cairnline-backed
operations brief uses Cairnline activity/service rows plus Hecate cockpit action
helpers so operator-facing actions stay parity-checked with the native route.
Assignment-start is still a Hecate-native dispatch mutation, committed
assignment-start results are best-effort mirrored for replacement evidence, and
other live Projects reads/writes still use the Hecate-native API.
`GET /hecate/v1/projects/{id}/cairnline/read-model` seeds an in-memory
Cairnline service from the current Hecate stores and returns the portable
operations brief and activity projection without writing files.
`GET /hecate/v1/projects/{id}/cairnline/embedded-read-model` opens the
existing embedded Cairnline mirror database and returns the same portable
operations brief, activity projection, and launch-packet coverage without
loading Hecate stores, creating the database, or repairing drift. It is the
stricter read-side replacement-readiness probe because it verifies the live
mirror can serve project projections directly.
`GET /hecate/v1/projects/{id}/cairnline/parity-report` compares Hecate's
native cockpit counts with that Cairnline read model and returns explicit
differences for raw graph counts including execution-profile defaults,
rendered work-item route shape including embedded assignments, collaboration
artifact/handoff route shape including artifact-kind and handoff-status counts,
activity, rendered cockpit operations including action-kind counts, and the
Project Assistant proposal ledger, plus portable launch-packet coverage, so
import coverage, work-item projection drift, review/evidence/handoff projection
drift, bucket/status semantics, operator-action drift, portable ledger coverage,
and assignment packet coverage can be fixed before any backend switch.
`GET /hecate/v1/projects/{id}/cairnline/embedded-parity-report` performs the
same cockpit comparison against the existing embedded Cairnline mirror database
instead of the snapshot-seeded in-memory service. It is the stricter live-read
replacement probe: a match means the current mirror DB can serve the same
operator-facing project projection counts for that project.
`POST /hecate/v1/projects/cairnline/sync` writes a refreshable embedded
Cairnline SQLite database for the full Hecate Projects graph under Hecate's
data directory. It is a deterministic migration rehearsal and durable service
boundary proof; the response compares Hecate snapshot counts with the written
Cairnline database, including launch-packet coverage/warnings/errors, and also
compares normalized record ID sets plus semantic record-content digests,
including stable assignment launch-packet digests, so same-count/different-record
and same-ID/wrong-field drift are visible. It reports count-level, ID-set, and
content-digest differences. It is not a dual-write path and does not make
Cairnline authoritative.
When `HECATE_PROJECTS_COORDINATION_BACKEND=cairnline` is configured, live
project identity, metadata, root create/update/delete/discovery/worktree-creation,
context-source create/update/delete/discovery, and project-default mutations
still commit to Hecate stores first, then best-effort mirror into the embedded
Cairnline database through their identity/metadata/root/source/default seams. This mirror is
replacement-readiness evidence only: Hecate remains authoritative and mirror
failures are logged.
Project skill discovery and metadata updates follow the same non-authoritative
mirror pattern for metadata-only skill records; the mirror never loads, injects,
or executes `SKILL.md` bodies.
Project role and work-item create/update/delete routes also mirror coordination
metadata into Cairnline after Hecate commits; when a mirrored role references
an agent profile, the mirror also seeds that profile metadata and execution
posture so the role can validate in Cairnline. Assignment create/update/delete
routes mirror coordination metadata and lifecycle status after Hecate commits;
assignment-start remains a Hecate-owned write gap because dispatch still carries
runtime coupling that needs a narrower switch point, but successful start
results and start-side conflict/cleanup states are best-effort mirrored after
Hecate commits them. Linked external-agent chat reconciliation also mirrors the
committed assignment status/ref when Hecate updates the linked assignment from a
chat session. Collaboration artifact creation, including generic artifacts,
evidence links, and reviews, and handoff create/update/delete routes also mirror
portable collaboration metadata after Hecate commits. Project memory entry and
memory-candidate create/update/resolve
routes mirror accepted memory and reviewable candidate state after Hecate
commits, including disabled state, provenance, status reason, and promoted
memory references. Global agent-profile create/update/delete routes also
mirror portable profile metadata and execution posture after Hecate commits;
the delete mirror removes only the profile record because execution-profile
posture can be shared. Project Assistant draft/propose/apply routes mirror the
proposal ledger and committed apply side effects after Hecate commits proposal
records and apply attempts.
`POST /hecate/v1/projects/{id}/cairnline/export` writes a refreshable Cairnline
SQLite export under Hecate's data directory. Both use the same bridge and are
useful for inspecting replacement parity, but they are still proofs, not the
live Projects backend.

The first non-authoritative write-adapter seams exist in `cairnlinebridge` for
project identity, embedded roots, context sources, project defaults,
project-level execution-profile cleanup, agent-profile upserts/deletes with
execution-posture upsert, project skill metadata upserts,
role upserts/deletes with role-level execution-profile cleanup, work-item
upserts/deletes, assignment metadata upsert/delete plus lifecycle-status sync,
and project memory entry/candidate upserts and deletes. Create-if-missing seams
exist for generic collaboration artifacts, evidence links, and reviews because
Hecate and Cairnline both expose those as record/list contracts today; handoffs
have upsert/delete coverage because they are mutable coordination records. The
skill seam preserves operator-disabled state and discovered-at provenance while
remaining metadata-only; it never loads, injects, or executes `SKILL.md` bodies.
The memory seam preserves accepted-memory fields, disabled state, candidate
provenance, and resolved candidate state including Hecate-owned promoted memory
IDs. The assignment seam updates existing role/root/profile/driver/context
metadata while preserving Cairnline lifecycle transitions through claim,
progress, and completion methods. These seams prove the Cairnline service can
accept Hecate's project/root/source, skill, role, work-item, assignment,
collaboration, handoff, and memory mutation shapes, but live API routes still
write Hecate stores first. Live mirror seams reported as
`project-identity-live-mirror` covers project create/delete;
`project-metadata-live-mirror` covers project name/description metadata updates
without replacing roots or sources; `project-roots-live-mirror` covers direct
root create/update/delete, root list replacement, root discovery, and
worktree-root creation mutations through Cairnline's root-level API;
`project-context-sources-live-mirror` covers direct context-source
create/update/delete, context-source list replacement, and discovery mutations
through Cairnline's source-level API;
`project-defaults-live-mirror` covers default-only project updates through
Cairnline's project-defaults seam while preserving mirrored roots and context
sources;
`agent-profiles-live-mirror` covers global
agent-profile create/update/delete metadata; `project-skills-live-mirror`
covers project skill discovery/update metadata; `project-roles-live-mirror`,
`project-work-items-live-mirror`, `project-assignments-live-mirror`,
`project-assignment-start-result-live-mirror`,
`project-assignment-chat-reconcile-live-mirror`,
`project-collaboration-live-mirror`, and `project-handoffs-live-mirror` cover
durable coordination, committed assignment-start/reconciliation results, and
collaboration records; `project-memory-live-mirror` and
`project-memory-candidates-live-mirror` cover accepted memory and reviewable
memory-candidate records; and
`project-assistant-proposal-ledger-live-mirror` and
`project-assistant-apply-side-effects-live-mirror` cover Project Assistant
proposal records, apply-attempt history, and committed apply side effects before
assignment-start/runtime handoff. None are write authority. Backend status
reports these proofs as
`write_adapter_seams`, while `write_adapter_gaps`
remains the machine-readable live-route stop list until route switch points,
atomic promotion semantics, and migration/rollback semantics are implemented.

Current read-model parity includes queued-assignment attention semantics:
Hecate and Cairnline both count a queued assignment as blocked/attention rather
than active work. The parity report remains valuable for finding later drift,
but this known bucket mismatch is closed.

Hecate is ready to replace its internal Projects backend with Cairnline only
after these gates are met:

- Cairnline has durable storage and MCP/API parity for Hecate's project, role,
  profile, context-source provenance metadata, skill, work item, assignment,
  artifact, handoff, accepted-memory, memory-candidate, and assistant-proposal
  ledger flows.
- Hecate has feature-flagged adapters that can run all read/write Projects
  flows against Cairnline without UI-local fallback state. The first live read
  routes are project list/detail, setup readiness, health, skills, memory
  entries, memory candidates, roles, activity, work-item list/detail, assignment
  lists, assignment context, launch-readiness, assignment preflight, artifact
  lists, handoff lists, Project Assistant context/proposal reads, closeout
  readiness, and operations brief. Draft generation may use the
  Cairnline-projected context, but proposal ledger writes and apply authority
  remain Hecate-owned while committed assistant side effects are mirrored as
  non-authoritative Cairnline replacement evidence. Assignment preflight/start
  packets may carry non-authoritative
  Cairnline launch-packet evidence, but assignment-start remains a Hecate-owned
  write gap; committed start and linked-chat reconciliation results may be
  mirrored only as replacement evidence. Backend-status `write_adapter_seams`
  lists non-authoritative proof
  coverage; `write_adapter_gaps` remains the machine-readable stop list for
  mutation families that still need live switch points before authority can
  move.
- Import/export or migration covers existing Hecate local stores and can be
  rolled back during alpha; the embedded Cairnline sync database proves a
  durable all-project seed with count-level, ID-set, record-content, stable
  launch-packet content parity, and strict embedded configured-route smoke
  across representative project, setup, health, skill, memory, role, work,
  collaboration, assistant-context, activity, and operations route families
  before it becomes a write path.
- Context packets, setup/health/operations summaries, activity projections, and
  closeout gates match current Hecate behavior or have documented intentional
  differences.
- Dogfooding covers at least one real Hecate development project from project
  creation through assignment, evidence, review, handoff, and closeout.

## Data Model

Sketch:

```go
type Project struct {
    ID              string // proj_...
    Name            string
    Roots           []ProjectRoot
    ContextSources  []ProjectContextSource
    DefaultRootID   string
    RepoURL         string
    DefaultBranch   string
    DefaultProvider string
    DefaultModel    string
    DefaultAgentProfile string
    SourcePresetID string

    DefaultToolsEnabled      *bool
    DefaultWorkspaceMode     string
    DefaultSystemPrompt      string
    DefaultCompactToolOutput *bool

    CreatedAt    time.Time
    UpdatedAt    time.Time
    LastOpenedAt time.Time
}

type ProjectRoot struct {
    ID        string
    Path      string
    Kind      string // local, git, git_worktree, editor_owned, temporary
    GitRemote string
    GitBranch string
    Active    bool
}

type ProjectContextSource struct {
    ID      string // ctxsrc_...
    Kind    string // doc, policy, memory, external
    Title   string
    Path    string
    Enabled bool
}
```

Rules:

- `project_id` is nullable during migration.
- Existing chats and tasks without a project remain valid.
- First implementation lets the operator create projects explicitly and attach
  new chat sessions to the selected project.
- First implementation stores context-source metadata only. It does not read
  those files, inject them into prompts, or create memory entries.
- Operators can rename projects later.
- Multiple roots can map to one project, but one root should not silently attach to many projects without operator confirmation.
- Git worktrees are roots, not separate projects by default. Root discovery can
  register linked worktrees for visibility, but newly discovered worktree roots
  start inactive so context discovery and assignment launch stay scoped to the
  operator-selected roots.

## API Shape

Proposed Hecate-native endpoints:

```text
GET    /hecate/v1/projects
POST   /hecate/v1/projects
GET    /hecate/v1/projects/{project_id}
PATCH  /hecate/v1/projects/{project_id}
DELETE /hecate/v1/projects/{project_id}
GET    /hecate/v1/projects/{project_id}/activity
```

Chats and tasks should expose project linkage directly:

```json
{
  "id": "chat_...",
  "project_id": "proj_...",
  "workspace": "/Users/me/dev/hecate",
  "workspace_mode": "in_place"
}
```

Project activity is a convenience aggregation over existing chats, tasks, runs, approvals, and usage. It should not replace those canonical APIs.

## Memory Relationship

Project memory should be the default durable memory scope.

Memory layers:

| Layer               | Persistence   | Scope                       | Promotion                    |
| ------------------- | ------------- | --------------------------- | ---------------------------- |
| Global memory       | Durable       | Whole local Hecate instance | Explicit only                |
| Project memory      | Durable       | One `project_id`            | Explicit save from chat/task |
| Chat/session memory | Session-local | One chat/session            | Never auto-promoted          |
| Current context     | Per request   | One model/agent call        | Not memory                   |

This keeps short-lived conversation facts out of project memory unless the
operator explicitly saves them or promotes a pending memory candidate.

Agent profiles decide whether project memory is used for a given agent. For
example, a Hecate Chat profile can inject project memory into the provider
prompt, while a Claude Code profile can send the same project memory only if the
ACP adapter exposes a safe instruction/config surface. If no such surface
exists, Hecate can still show the project memory in the context inspector as
operator-side notes. Those notes are structured project-scoped memory entries
with Markdown-compatible bodies, not Markdown files as the default durable
storage format.

External memory providers should plug in behind the Hecate memory service and
be selected by agent profile. Projects define the durable scope; profiles define
which local/external memory sources participate for a specific agent.

## Profiles And Presets

Projects, profiles, and presets have separate jobs:

| Object        | Job                                                                                                                                                                  | Runtime role                                                             |
| ------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| Project       | Durable identity for a codebase/work area. Owns defaults, history grouping, and project memory.                                                                      | Active runtime scope.                                                    |
| Agent profile | Saved configuration for an agent in a project or globally: Hecate/Codex/Claude/Cursor, model/adapter controls, tools, approvals, memory sources, system prompt, RTK. | Active runtime configuration.                                            |
| Preset        | Reusable template such as "code review", "implementation", "docs", "safe external agent", "fast local model", or local MCP toolsets.                                 | Applied to create/update a profile or project default; not a live scope. |

In other words: a project can choose a default agent profile, and a profile can
be created from a preset. After application, Hecate should persist the resolved
profile/settings. Presets stay authoring-time templates; profiles are runtime
configuration; projects are durable work scopes. Context packets may record
`source_preset_id` for audit, but the runtime should not depend on a mutable
preset staying unchanged.

Local MCP exposure should use the same preset vocabulary rather than a separate
taxonomy. The initial built-in MCP toolset presets are `readonly`, `operator`,
`observability`, `security`, and `support`; see
[`mcp.md`](../../runtime/mcp.md#local-scenarios-and-built-in-presets) for their intended
scope and security posture.

## Context Relationship

Context assembly should include both project and workspace metadata:

- `project_id`: stable identity for memory/defaults/history.
- `workspace`: concrete execution path.
- `workspace_mode`: in-place, isolated clone, temporary, or editor-owned.
- `project_context`: saved memories, project instructions, selected docs, and trusted repo guidance.
- `agent_profile_context`: selected memory sources, profile instructions, and adapter/model controls.
- `workspace_context`: files, diffs, tool output, and runtime artifacts from the concrete workspace.

This prevents future context systems from confusing “same path today” with “same durable project.”
Today, chat message context packets already snapshot enabled project
context-source metadata for Hecate Chat, direct model turns, and External Agent
turns. The snapshot is provenance-only: it records configured source paths and
labels as itemized `workspace_guidance` context metadata, but does not read or
inject file contents.

## UI Shape

The Projects UI should stay lightweight but operational:

- Show project identity in the Chats sidebar and Chat settings when present.
- Group chat history by selected project, while keeping **No project** valid.
- Let “New Hecate chat” and External Agent chats attach to the selected project.
- Show project identity in Task detail once task linkage exists.
- Show project roots/worktrees with branch and active/default status so the
  operator knows which checkout an assignment will use.
- Let future “Use model” and “Use external agent” flows attach to the same project when started from the same workspace.
- Let agent profiles expose whether project memory is injected, visible only,
  or disabled for that agent.
- Show compact activity and needs-attention surfaces in the project cockpit that
  derive operational status from existing activity, assignment execution
  rollups, handoff summaries, project defaults, memory entries, memory
  candidates, context-source metadata, agent profile references, and project
  skills registry status.
- Keep the project details surface focused on defaults, memory, trusted docs,
  activity, and assignment drill-downs.
- Use the cockpit as the first screen for project orchestration. The project
  header owns project identity plus global actions: Needs Attention, Roles,
  Project Settings, and refresh. Needs Attention is a compact dropdown of
  actionable rows, not a second health dashboard.
- Keep the Projects index as a fixed left panel for now. Do not add a collapsed
  mini-rail or persist a collapsed Projects state until the operator workflow
  calls for a clearer navigation pattern.
- Keep the cockpit workspace tabbed by operator intent: Work Coordination,
  Timeline / Decision Log, and Memory / Context. Work Coordination owns work
  items, assignment launch, handoffs, and selected work detail. It should use
  one Work Queue with All / activity filters instead of separate Activity Inbox
  and Work Items lists. Timeline /
  Decision Log owns project story and durable decisions. Memory / Context owns
  saved entries, candidates, and context sources.
- Treat the selected work item as one card. The work title, brief,
  assignments, collaboration artifacts, and handoffs are one work coordination
  object with internal sections, not separate dashboard panels.
- Show assignment execution evidence close to the assignment row using
  canonical `execution_ref` and activity linked ids: task, run, chat, message,
  context snapshot, trace, provider/model, counts, and missing/stale warnings.
  Keep this as compact provenance; the full Context Inspector remains the
  place to inspect the persisted packet sections the agent actually saw.
- Show handoff source evidence separately from target assignment evidence.
  Source assignment/run/chat/message/context refs explain provenance; target
  assignment refs explain the follow-up work. Accepting or linking a handoff
  still records operator intent only and must not auto-dispatch work.
- Open Project Settings as the same right-side inspector pattern used by Chat
  settings, with the same right-panel width behavior. The project header stays
  above the workspace/settings split so the inspector starts below the header,
  not beside it. Do not use a modal for routine project defaults.

Avoid turning Projects into a heavy project-management product. This is a runtime identity and context boundary first.

## Authority Model

Projects is the operator cockpit for project-scoped work. It coordinates
identity, roles, assignments, handoffs, memory, guidance, activity, approvals,
artifacts, and context inspection, but it is not itself an execution engine.

The product layers should stay separate:

| Layer             | Scope               | Owns                                                                                                   |
| ----------------- | ------------------- | ------------------------------------------------------------------------------------------------------ |
| Operator          | Human control plane | Final authority over project setup, proposal apply, assignment start, approvals, cancellation, memory. |
| Project Assistant | One project         | Bounded proposals for setup, work items, assignments, handoffs, and memory candidates.                 |
| Planner           | Future project plan | Backlog, milestones, dependencies, roles, and context-bundle proposals.                                |
| Manager           | Future project run  | Active-state monitoring, blocker detection, sequencing suggestions, follow-up proposals.               |
| Orchestrator      | Runtime execution   | Approved task/agent coordination, waits, approvals, event emission, and state transitions.             |

Project Assistant can help create a new project or draft a follow-up assignment.
In the Projects UI, Bootstrap is project onboarding: it prepares setup proposals
by refreshing workspace guidance and project skills, without inheriting nested
worktree containers as parent-root input. It does not auto-start work, run
agents, or write durable memory directly.
For a selected work item, the guided first-assignment action also uses the
Project Assistant proposal rail. It drafts a queued assignment proposal from the
work item owner/default role and preserves the selected work root, but applying
the proposal and starting the assignment remain separate operator actions.
Planner and Manager are future proposal/monitoring layers. The orchestrator is
the runtime coordinator that executes approved work through Tasks, External
Agents, approvals, artifacts, traces, and events.

## Storage Plan

The first implementation adds `internal/projects/` with memory and SQLite
stores, plus `GET`/`POST`/`PATCH`/`DELETE /hecate/v1/projects`.

This landed as a foundation plus chat grouping: project records and roots can be
persisted, trusted context-source metadata can be attached to a project, and
chat sessions can carry `project_id`. Chat context packets snapshot enabled
project context-source metadata as itemized provenance. Project-scoped memory
entries now persist as structured records with Markdown-compatible bodies and
explicit trust/provenance labels; enabled entries are visible as chat
context-packet items, but writes remain operator-driven. Memory candidates now
provide a project-scoped review queue for generated/chat/task output;
candidates are `pending`, `promoted`, or `rejected` and do not become durable
memory until the operator promotes them with any edits. Project work
assignments can now start native Tasks linked back via `origin_kind` /
`origin_id`, and `GET /hecate/v1/projects/{id}/activity` exposes a read-only
project activity inbox over work items, assignments, linked task/run/chat ids,
status signals, recent collaboration artifacts, and structured handoff signals.
Structured project handoffs now persist as operator-controlled records that can
carry source assignment/run/chat refs, target role or assignment hints,
recommended next action text, linked artifact IDs, linked memory IDs, context
refs, provenance labels, and `pending` / `accepted` / `superseded` /
`dismissed` status. A handoff may help the operator create or start a follow-up
assignment, but the handoff record itself does not dispatch another agent.
Queued assignment launches now show a launch preflight context packet before
dispatch; confirming the preview starts the native task or prepares the External
Agent chat, while preflight itself creates no task, run, chat session, artifact,
memory entry, or assignment update.
Assignment launch readiness is a separate server-owned projection at
`GET /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/launch-readiness`.
The Projects UI uses its typed `ready`, `blockers`, resolved workspace/profile
hints, and provider/model readiness fields to gate explicit start/prepare
confirmation, while the preflight context packet remains inspectable evidence
rather than client-parsed authority.
Project roots now model concrete checkouts for assignment execution: work items
and assignments can select a root by `root_id`, and launch resolution uses
assignment root, work-item root, project default root, then first active root.
Operators can explicitly create linked Git worktrees from an existing project
root; V1 registers the created checkout as a project root and constrains the
target to a direct child of the base root's `.worktrees/` directory so Hecate
does not create arbitrary sibling or nested workspaces.
The Projects UI also derives a compact project timeline / decision log from
activity rows, structured handoffs, collaboration artifacts, project memory
entries, and memory candidates; explicit decisions are only shown when existing
`decision_note` artifacts are present. The Projects cockpit keeps Activity
Inbox focused on live assignment buckets from the activity response. Needs
Attention is derived by the server project health endpoint from existing
project defaults, roots, profile/skill refs, handoffs, review artifacts,
assignment execution links, memory candidates, and memory/context metadata so
operators can separate actionable setup gaps, blocked or stale assignments,
pending handoffs, memory review work, and context readiness without adding a
separate persisted health model. Needs Attention and Project Operations share a
typed `action` routing contract for follow-through into existing Project
Settings, Work Coordination, Memory/Context, preflight, selected-work
follow-through, task, activity bucket, or Project Assistant proposal flows.
Projection-specific fields remain display metadata and should not become a
second client routing authority.
Review follow-up, missing completion evidence, and closeout-ready work items
open the existing selected-work detail surface; the brief does not persist a
plan, mark work done, or start work.
Project setup readiness is also a server-owned projection: onboarding,
setup-started, and first-work-ready states come from
`GET /hecate/v1/projects/{id}/setup-readiness`, while checklist actions route
to Project Settings, Project Assistant setup, or explicit work-item creation.
Clients should not recreate setup readiness from local context-source, memory,
role, or work-item heuristics.
The selected-work detail card now reflects the same read-only backend closeout
readiness contract as Project Operations, so Mark done is enabled from the
server-owned assignment/evidence/handoff/review-follow-up decision rather than
a separate client cascade.
Assignment rows now render compact execution evidence from canonical
assignment/activity refs, while the Context Inspector renders the full persisted
launch packet with Profile, Instructions, Skills, Memory, Project sources, Work
context, and Runtime evidence groups. Task and run records now carry direct
project/work/assignment linkage when created from project-scoped surfaces;
profiles and presets are not linked to `project_id` yet.

Persist `project_id` on:

- Chat sessions.
- Chat-completion sessions where a workspace/project is selected.
- Tasks and runs.
- Context packets.
- Memory entries.
- Future presets/profiles.

SQLite migration should be additive first:

- New `projects` table.
- New `project_roots` table.
- New `project_context_sources` table for metadata-only source references.
- Nullable `project_id` columns on existing tables.

Because Hecate has no stable users yet, later cleanup can remove legacy path-derived compatibility once the new model settles.

## Implementation Plan

1. Add project store and API basics. Done for memory + SQLite CRUD.
2. Add UI list/create/rename/delete basics. Done.
3. Add `project_id` to chat sessions.
4. Add `project_id` to tasks and runs. Done for task records, run records, task
   lists, run responses, project assignments, and project-linked Hecate Chat
   task-backed turns.
5. Thread project identity into chat context packets. Done for itemized project context-source metadata.
6. Add project-scoped memory. Done.
7. Add agent-profile memory-source selection. Done for profile memory/source
   policy snapshots, bounded native-assignment prompt inclusion, and
   project-linked Hecate Chat prelude inspection.
8. Move relevant defaults from ad hoc chat/task state into project defaults.
   Partial: provider, model, workspace mode, and agent profile defaults are
   project defaults; assignment starts resolve role/project/fallback profiles,
   including immutable built-in profile defaults.
9. Add project activity aggregation. Done for the read-only V1 inbox.
10. Add structured handoffs. Done for memory + SQLite store parity, API, UI
    actions, and activity projection signals.
11. Add project-root selection and explicit worktree creation. Done for
    work-item/assignment root overrides, launch preflight/start snapshots, and
    operator-triggered Git worktree creation under `.worktrees/`.
12. Update docs, screenshots, and e2e coverage. Partial: docs, focused UI/API
    tests, and an API-level project journey regression are updated; broad UI
    end-to-end project journeys remain beta-hardening work.

## V1 Closure Boundary

Projects V1 is considered structurally complete when an operator can:

- Create a rootless or workspace-backed project without treating every project
  as a GitHub/code project.
- Run project setup to discover guidance and skills metadata, then review the
  proposed memory/role changes before applying them.
- Configure project defaults, roles, profiles, skills, provider/model posture,
  memory/source policies, roots, and worktrees explicitly.
- Create a work item, draft or manually create assignments, start Hecate Task
  or External Agent execution, inspect launch context, record evidence/reviews,
  hand off to another role, and close the work item deliberately.
- See actionable project activity and health without a separate persisted
  health model.

Remaining Projects V1 hardening:

- Keep the browser-level project journey test representative as setup,
  assignment, evidence, and closeout flows evolve.
- Keep polishing onboarding and first-work UI so setup is the obvious path and
  manual forms remain available but secondary.
- Continue dogfooding Hecate development through a Hecate project and capture
  only concrete gaps as follow-up issues.

Out of scope for this document and Projects V1:

- Planner / Manager agents that autonomously propose sequencing across many
  work items.
- Workflow runbooks, browser QA capture, design-review automation, or
  production-risk review modes.
- Automatic memory writes, automatic handoff dispatch, remote skill install,
  skill-body prompt injection, or host-specific guidance-body injection.
- Multi-operator/team project-management semantics, GitHub issue sync, or
  non-local permission models.

## Test Plan

- Unit tests for memory and SQLite project store parity.
- API tests for CRUD, root attachment, rename, and deletion constraints.
- Migration tests for adding nullable `project_id` to existing data.
- Chat tests that new Hecate and External Agent chat sessions attach to the
  selected project.
- Task tests that task/run records preserve `project_id`.
- Memory tests that project memory appears only for matching `project_id`.
- Profile tests proving Hecate Chat and external-agent profiles can opt into the
  same project memory without sharing private adapter memory.
- API journey: create project, discover guidance/skills, add memory, create and
  start assignment, inspect context, create handoff/follow-up assignment.
- UI journeys: create rootless and workspace-backed projects, run setup, create
  work, draft/start assignment, inspect context, record review/evidence,
  complete closeout, and verify no-project/new-project onboarding states. A
  browser-level Projects journey now covers create project -> setup proposal ->
  first work -> assignment draft/start -> evidence -> closeout.

## Open Questions

- Should project identity ever be inferred from Git remote, or only from
  explicit operator selection?
- How should project defaults interact with per-chat overrides?
- Should imported Claude/Codex contexts create projects automatically?
- Should a project have one preferred workspace mode or separate defaults for Hecate Chat, Tasks, and External Agents?
- Which agent profiles should opt into project memory by default?
- What structured fields are needed for review/evidence querying once the V1
  markdown-body artifact model is not enough?

# Projects

> **Status:** accepted; orchestration foundation in progress.

Current source of truth: [Agent runtime](../../runtime/agent-runtime.md), [Chat sessions](../../runtime/chat-sessions.md), [Architecture](../../contributor/architecture.md)

Next action: make Projects the operational cockpit for project-scoped agent
teams: finish task/run project linkage, wire agent profiles into defaults and
memory-source selection, and harden the activity/health/timeline UI with
end-to-end project journeys.

## Summary

Hecate should distinguish **Projects** from **Workspaces**.

A project is the durable Hecate identity for a codebase or work area. It owns memory scopes, chat/task grouping, default runtime choices, trusted context sources, and future agent-profile defaults. A workspace is a concrete filesystem root used by one chat, task, run, or external-agent session.

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

| Term           | Meaning                                                                                                                                                    |
| -------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Project        | Durable Hecate object representing a codebase or work area. Identified by `project_id`. Owns defaults, memories, history grouping, and context sources.    |
| Workspace      | Concrete filesystem root used for execution. A project can have one or more workspaces over time.                                                          |
| Chat           | Conversation attached to an optional project and, when running, a concrete workspace.                                                                      |
| Task           | Durable runtime object attached to an optional project and a concrete workspace mode.                                                                      |
| Run            | One execution attempt under a task. Runs never define project identity by themselves.                                                                      |
| Agent profile  | Reusable agent configuration for Hecate Chat or an external agent: model/adapter controls, tools, memory sources, system instructions, and safety posture. |
| Preset         | Template for creating or updating a project default or agent profile. Presets are not used directly at runtime once applied.                               |
| Context packet | A snapshot of what Hecate assembled for a model/agent call, including project and workspace metadata.                                                      |

## Goals

- Add stable project identity independent of raw filesystem paths.
- Make project memory a first-class durable scope.
- Give Hecate Chat, Tasks, and External Agents a shared grouping model.
- Coordinate project-scoped agent teams through roles, assignments, handoffs,
  project activity, and reviewed memory/context without replacing Tasks or
  Chats as execution surfaces.
- Keep workspace modes explicit: in-place, isolated clone, temporary workspace, editor-owned workspace.
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
    Kind      string // local, isolated_clone, editor_owned, temporary
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
- Let future “Use model” and “Use external agent” flows attach to the same project when started from the same workspace.
- Let agent profiles expose whether project memory is injected, visible only,
  or disabled for that agent.
- Show compact activity and needs-attention surfaces in the project cockpit that
  derive operational status from existing activity, assignment execution
  rollups, handoff summaries, project defaults, memory entries, memory
  candidates, and context-source metadata.
- Keep the project details surface focused on defaults, memory, trusted docs,
  activity, and assignment drill-downs.
- Use the cockpit as the first screen for project orchestration: activity inbox,
  health, timeline/decision log, memory/context inspection, and selected
  assignment detail should answer "what needs attention, what happened, and
  what can I launch or review next" without a separate project-management
  surface.

Avoid turning Projects into a heavy project-management product. This is a runtime identity and context boundary first.

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
The Projects UI also derives a compact project timeline / decision log from
activity rows, structured handoffs, collaboration artifacts, project memory
entries, and memory candidates; explicit decisions are only shown when existing
`decision_note` artifacts are present. The Projects cockpit derives Activity
Inbox and Needs Attention surfaces from that activity response plus project
defaults, handoff summaries, memory candidates, and memory/context metadata so
operators can separate live assignment buckets from actionable setup gaps,
waiting approvals, blocked or stale assignments, pending handoffs, memory review
work, missing provider/model defaults, and context readiness without adding a
separate persisted health model. Broader task `project_id` scoping, profiles,
and presets are not linked to `project_id` yet.

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
4. Add `project_id` to tasks. Partial: project work assignments can start
   linked native tasks via origin metadata; broader task/run scoping remains
   future.
5. Thread project identity into chat context packets. Done for itemized project context-source metadata.
6. Add project-scoped memory. Done.
7. Add agent-profile memory-source selection.
8. Move relevant defaults from ad hoc chat/task state into project defaults.
   Partial: provider, model, workspace mode, and agent profile defaults are
   project defaults; profile-driven activation remains future.
9. Add project activity aggregation. Done for the read-only V1 inbox.
10. Add structured handoffs. Done for memory + SQLite store parity, API, UI
    actions, and activity projection signals.
11. Update docs, screenshots, and e2e coverage. Partial: docs and focused UI/API
    tests are updated; broad end-to-end project journeys remain beta-hardening
    work.

## Near-Term Plan

The next project-orchestration slices are:

1. Finish durable task/run project linkage so native tasks, project assignments,
   and chat-origin work all expose the same `project_id` boundary.
2. Wire agent profiles into project defaults and memory-source selection so a
   role/assignment can launch with a known provider/model/profile/context
   posture instead of only a provider/model/workspace default.
3. Add focused end-to-end project journeys: create project, set defaults, add
   memory, create work item, create/start assignment, resolve approval or
   failure, inspect activity health, and follow a handoff.
4. Keep tightening the Projects cockpit UI around operator decisions: no hidden
   recommendations, no separate health persistence, and no automatic memory
   promotion.

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
- E2E: create project from workspace, start chat, create task-backed turn, refresh, verify chat/task grouping.

## Open Questions

- Should a single filesystem root be allowed in multiple projects?
- Should project identity be inferred from Git remote by default, or only from explicit user selection?
- How should project defaults interact with per-chat overrides?
- Should imported Claude/Codex contexts create projects automatically?
- Should a project have one preferred workspace mode or separate defaults for Hecate Chat, Tasks, and External Agents?
- Which agent profiles should opt into project memory by default?

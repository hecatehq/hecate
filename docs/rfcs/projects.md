# Projects

Status: Proposed

Current source of truth: [Agent runtime](../agent-runtime.md), [Chat sessions](../chat-sessions.md), [Architecture](../architecture.md)

Next action: introduce a durable `project_id` model before project-scoped memory, agent profiles, and richer context assembly.

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
```

Rules:

- `project_id` is nullable during migration.
- Existing chats and tasks without a project remain valid.
- First implementation may auto-create a path-derived project when a user starts a chat or task from a local workspace.
- Operators can rename a path-derived project later.
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

This keeps short-lived conversation facts out of project memory unless the operator or assistant explicitly saves them.

Agent profiles decide whether project memory is used for a given agent. For
example, a Hecate Chat profile can inject project memory into the provider
prompt, while a Claude Code profile can send the same project memory only if the
ACP adapter exposes a safe instruction/config surface. If no such surface
exists, Hecate can still show the project memory in the context inspector as
operator-side notes.

External memory providers should plug in behind the Hecate memory service and
be selected by agent profile. Projects define the durable scope; profiles define
which local/external memory sources participate for a specific agent.

## Profiles And Presets

Projects, profiles, and presets have separate jobs:

| Object        | Job                                                                                                                                                                  | Runtime role                                                             |
| ------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| Project       | Durable identity for a codebase/work area. Owns defaults, history grouping, and project memory.                                                                      | Active runtime scope.                                                    |
| Agent profile | Saved configuration for an agent in a project or globally: Hecate/Codex/Claude/Cursor, model/adapter controls, tools, approvals, memory sources, system prompt, RTK. | Active runtime configuration.                                            |
| Preset        | Reusable template such as "code review", "implementation", "docs", "safe external agent", or "fast local model".                                                     | Applied to create/update a profile or project default; not a live scope. |

In other words: a project can choose a default agent profile, and a profile can
be created from a preset. After application, Hecate should persist the resolved
profile/settings. Context packets may record `source_preset_id` for audit, but
the runtime should not depend on a mutable preset staying unchanged.

## Context Relationship

Context assembly should include both project and workspace metadata:

- `project_id`: stable identity for memory/defaults/history.
- `workspace`: concrete execution path.
- `workspace_mode`: in-place, isolated clone, temporary, or editor-owned.
- `project_context`: saved memories, project instructions, selected docs, and trusted repo guidance.
- `agent_profile_context`: selected memory sources, profile instructions, and adapter/model controls.
- `workspace_context`: files, diffs, tool output, and runtime artifacts from the concrete workspace.

This prevents future context systems from confusing “same path today” with “same durable project.”

## UI Shape

Initial UI should stay lightweight:

- Show project identity in Chat settings and Task detail when present.
- Group chat/task history by project once projects exist.
- Let “New Hecate chat” inherit project defaults.
- Let “Use model” and “Use external agent” attach to the same project when started from the same workspace.
- Let agent profiles expose whether project memory is injected, visible only,
  or disabled for that agent.
- Add a project details surface later for defaults, memory, trusted docs, and recent activity.

Avoid turning Projects into a heavy project-management product. This is a runtime identity and context boundary first.

## Storage Plan

Add a new `internal/projects/` package with memory and SQLite stores.

Persist `project_id` on:

- Agent Chat sessions.
- Chat-completion sessions where a workspace/project is selected.
- Tasks and runs.
- Context packets.
- Memory entries.
- Future presets/profiles.

SQLite migration should be additive first:

- New `projects` table.
- New `project_roots` table.
- Nullable `project_id` columns on existing tables.

Because Hecate has no stable users yet, later cleanup can remove legacy path-derived compatibility once the new model settles.

## Implementation Plan

1. Add project store, API, and UI list/create basics.
2. Auto-create path-derived projects for local workspaces.
3. Add `project_id` to chat sessions and tasks.
4. Thread project identity into context packets.
5. Add project-scoped memory.
6. Add agent-profile memory-source selection.
7. Move relevant defaults from ad hoc chat/task state into project defaults.
8. Add project activity aggregation.
9. Update docs, screenshots, and e2e coverage.

## Test Plan

- Unit tests for memory and SQLite project store parity.
- API tests for CRUD, root attachment, rename, and deletion constraints.
- Migration tests for adding nullable `project_id` to existing data.
- Chat tests that new Hecate Chat sessions inherit project defaults.
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

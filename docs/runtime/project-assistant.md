# Project Assistant

Project Assistant is Hecate's API-first foundation for supervised project
operations. It is intentionally narrow: a client proposes typed actions, Hecate
validates them, the operator inspects the exact change set, and apply
revalidates current server state before any durable mutation.

It is not a broad chat persona in v0. Hecate Chat can call the same proposal and
apply API later, but the action system lives in core so every surface follows
the same validation, confirmation, and audit rules.

## Current decisions

Project Assistant v0 is a project-level proposal and review surface, not a
replacement for simple chat. The Projects UI owns a compact composer above the
workspace tabs; Chats remains the place for conversational turns. A future real
assistant may offer a chat-like project surface, but durable changes should
still land as typed proposals that the operator reviews and applies.

The current composer is intentionally deterministic. `Auto` for `Run as`
resolves to the selected work item's owner role, then the first loaded project
role. `Auto` for `Via` resolves to the selected role's default driver, then
`hecate_task`. No model is choosing a delegate yet. When Hecate grows a real
project assistant loop, it can use richer project context to recommend or select
roles and drivers, but that decision still needs to be inspectable before work
is queued or launched.

The UI supports both project-level planning and selected-work context. With a
selected work item, the draft queues an assignment for that work. Without a
selected work item, the draft creates a new project work item. In both cases
`Draft proposal` creates reviewable data only; it does not create a chat, task,
run, assignment, or agent session.

## Authority boundary

Project Assistant is a proposal author, not the project orchestrator. It can
help set up a project, attach roots, draft work items, queue assignments, draft
handoffs, and create memory candidates, but it does not own runtime execution
or ongoing project supervision.

The intended authority ladder is:

| Layer             | Responsibility                                                                 |
| ----------------- | ------------------------------------------------------------------------------ |
| Operator          | Final authority. Reviews, applies, starts, cancels, and approves durable work. |
| Project Assistant | Produces one bounded project-scoped proposal when asked.                       |
| Planner           | Future layer that turns goals and project state into backlog/plan proposals.   |
| Manager           | Future layer that monitors active work and proposes next actions or gates.     |
| Orchestrator      | Executes approved coordination through tasks, agents, approvals, and events.   |

Project Assistant may propose orchestration-shaped work, such as "create an
implementation assignment, then create a QA assignment", but applying that
proposal only creates durable project records. Starting assignments, waiting on
approvals, routing handoffs, and coordinating multiple agents belong to the
orchestrator after explicit operator action.

Project Assistant is also distinct from any future personal assistant. Project
Assistant is scoped to one project and its project stores. A personal assistant
would be operator-scoped across projects and would need a separate privacy and
permission model.

## Safety model

- Project Assistant actions are typed and allowlisted.
- Durable or destructive actions require explicit operator confirmation.
- "No project" remains valid. Assistant proposals may suggest creating or
  selecting a project, but they do not force every chat into a project.
- The assistant never writes project stores directly. Proposals are structured
  data; apply revalidates ids, current state, and project/workspace boundaries
  server-side.
- Memory actions create memory candidates only. They never create durable
  memory entries directly.
- Assistant code does not perform raw filesystem, shell, or Git access.
  Workspace-bound behavior must use WorkspaceFS, ProcessRunner, GitRunner, or
  existing task-runtime paths.
- Proposals, traces, artifacts, and memory candidates must not store secrets.
- Apply is human-gated. Do not expose `/project-assistant/apply` as a direct
  model-callable tool; chat or agent integrations must route durable mutations
  through an explicit blocking operator confirmation first.

Apply is sequential across existing stores. A proposal id plus its canonical
action set is the in-process progress boundary. If action N fails after earlier
actions have already mutated durable stores, the API returns the partial action
results and `failed_action_index`. Retrying the exact same proposal resumes at
the next unapplied action. Retrying the same proposal id with a changed action
set returns `409 conflict`, and retrying a fully applied proposal also returns
`409 conflict`.

Future versions may persist proposal ids server-side so reviewed actions,
confirmation, and resumable progress survive process restarts. The v0 API keeps
that shape possible without requiring it.

## Endpoints

### `POST /hecate/v1/project-assistant/propose`

Validates a candidate proposal and returns a server-shaped proposal id,
warnings, confirmation posture, and trace id.

```json
POST /hecate/v1/project-assistant/propose
{
  "title": "Create project",
  "summary": "Create a Hecate project with the current workspace root.",
  "actions": [
    {
      "kind": "create_project",
      "target": { "project_id": "proj_hecate" },
      "patch": {
        "name": "Hecate",
        "description": "Local AI operations console",
        "workspace_path": "/Users/alice/src/hecate",
        "workspace_kind": "git"
      },
      "reason": "Track chats, project work, and context under one project."
    }
  ]
}
→ 200
{
  "object": "project_assistant.proposal",
  "data": {
    "id": "pa_...",
    "title": "Create project",
    "summary": "Create a Hecate project with the current workspace root.",
    "actions": [],
    "warnings": [],
    "requires_confirmation": true,
    "trace_id": "..."
  }
}
```

Unknown action kinds return `400 invalid_request`.

### `POST /hecate/v1/project-assistant/apply`

Applies a previously proposed change set. Requests must send the proposal and
`confirm: true` when `requires_confirmation` is true.

```json
POST /hecate/v1/project-assistant/apply
{
  "proposal": {
    "id": "pa_...",
    "title": "Create project",
    "summary": "Create a Hecate project and attach the current workspace root.",
    "actions": [],
    "warnings": [],
    "requires_confirmation": true
  },
  "confirm": true
}
→ 200
{
  "object": "project_assistant.apply_result",
  "data": {
    "proposal_id": "pa_...",
    "applied": true,
    "actions": [
      {
        "kind": "create_project",
        "id": "proj_hecate",
        "data": {
          "project_id": "proj_hecate"
        }
      }
    ]
  }
}
```

Repeated apply attempts for the same proposal id return `409 conflict`.
Stale ids, missing projects, missing chats, missing work items, or missing
memory candidates return `404 not_found` or `409 conflict` depending on the
state transition.

When a multi-action apply fails after earlier actions were committed, the error
includes progress metadata:

```json
{
  "error": {
    "type": "not_found",
    "message": "project assistant apply failed at action 1: project assistant target not found: project \"proj_missing\"",
    "failed_action_index": 1,
    "partial_result": {
      "proposal_id": "pa_...",
      "applied": false,
      "actions": [
        {
          "kind": "create_project",
          "id": "proj_hecate",
          "data": {
            "project_id": "proj_hecate"
          }
        }
      ]
    }
  }
}
```

Retry the unchanged proposal after fixing the missing target to resume from the
first unapplied action. If the client changes `actions[]` while reusing the same
proposal id, apply returns `409 conflict` so the operator can refresh the
proposal instead of unknowingly applying a different change set.

## Action shape

Every action has the same envelope:

```json
{
  "kind": "move_chat_session",
  "target": {
    "chat_session_id": "chat_...",
    "project_id": "proj_..."
  },
  "patch": {},
  "reason": "Keep this chat with the project it discusses."
}
```

- `kind` selects the allowlisted operation.
- `target` identifies the existing resource or requested id.
- `patch` carries the proposed new fields.
- `reason` is operator-facing context shown before apply.

## Supported action kinds

| Kind                      | Store path used        | Notes                                                                                                                                   |
| ------------------------- | ---------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `create_project`          | `internal/projects`    | Creates a project with optional explicit id, `workspace_path`, roots, and defaults. Omit workspace fields for a workspace-less project. |
| `update_project`          | `internal/projects`    | Updates metadata and whole-list roots/context-source fields.                                                                            |
| `attach_project_root`     | `internal/projects`    | Adds a root to an existing project.                                                                                                     |
| `remove_project_root`     | `internal/projects`    | Removes a root from an existing project.                                                                                                |
| `set_project_defaults`    | `internal/projects`    | Updates provider/model/profile/tools/workspace/system-prompt defaults.                                                                  |
| `move_chat_session`       | `internal/chat`        | Moves exactly one chat session into a project or back to no project.                                                                    |
| `create_work_item`        | `internal/projectwork` | Creates a project-scoped work item; does not start a task.                                                                              |
| `update_work_item`        | `internal/projectwork` | Updates one existing work item.                                                                                                         |
| `create_assignment`       | `internal/projectwork` | Creates an assignment for existing project work.                                                                                        |
| `create_handoff`          | `internal/projectwork` | Creates a handoff record; does not launch follow-up work.                                                                               |
| `create_memory_candidate` | `internal/memory`      | Creates a candidate with provenance; never a durable memory entry.                                                                      |

## UI contract

The first visible UI should stay small and inspectable:

- start from a compact request composer, not a wide operational form;
- place the composer at the top of the project workspace because the assistant
  is project-scoped, above workspace tabs and tab panels even when it uses the
  selected work item as context;
- keep route controls contextual, with an automatic choice for the common path;
- show proposal cards with exact actions before apply, either inline or behind
  an inspect affordance;
- show `Apply` and `Dismiss`;
- require explicit confirmation for durable/destructive proposals;
- show stale-state failures as "State changed, refresh proposal";
- keep Chat integration as a later caller of the same API, not a second action
  system.

The Projects cockpit exposes this contract at the top of the project workspace.
V0 uses a composer-style request box that drafts typed proposals from the
selected project/work item. Later Hecate Chat can call the same proposal API,
but durable mutations should still stop at the same explicit review/apply card.
Applying a proposal always calls `/project-assistant/apply` with `confirm: true`
after the operator reviews the action rows. A successful apply refreshes the
project list, project work, selected work-item detail, and project memory.
`404 not_found` and `409 conflict` apply responses are treated as stale review
state: refresh the current work view and draft a fresh proposal instead of
retrying blindly.

Drafting a proposal does not create a chat session, task, run, or assignment.
Applying a proposal may create durable project records such as work items or
queued assignments, but a queued assignment still does not start execution.
Task/chat execution starts only through the assignment start flow, which may
then attach `task_id`, `run_id`, or `chat_session_id` links to the assignment.
For `external_agent` assignments, "start" means "prepare the linked chat":
Hecate creates and prepares the External Agent session, stores the assignment
context packet, and links `chat_session_id`, but it does not append a visible
chat message or send the first prompt. The operator sends that first prompt
from Chats after reviewing the prepared session.

Project-launched Hecate chat drafts reuse an existing matching 0-message idle
chat instead of creating another empty chat row. Once the operator sends a
message, the transcript is no longer reusable and a later launch may create a
new chat.

# Project Assistant

Project Assistant is Hecate's API-first foundation for supervised project
operations. It is intentionally narrow: a client proposes typed actions, Hecate
validates them, the operator inspects the exact change set, and apply
revalidates current server state before any durable mutation.

It is not a broad chat persona in v0. Hecate Chat can call the same proposal and
apply API later, but the action system lives in core so every surface follows
the same validation, confirmation, and audit rules.

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

- show proposal cards with exact actions before apply;
- show `Apply`, `Dismiss`, and `Inspect`;
- require explicit confirmation for durable/destructive proposals;
- show stale-state failures as "State changed, refresh proposal";
- keep Chat integration as a later caller of the same API, not a second action
  system.

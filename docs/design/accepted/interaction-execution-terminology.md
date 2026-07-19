# Interaction and Execution Terminology

> **Status:** accepted and implemented.
> **Current source of truth:** [Chat sessions](../../runtime/chat-sessions.md),
> [Agent runtime](../../runtime/agent-runtime.md), [Runtime API](../../runtime/runtime-api.md),
> and [Events](../../runtime/events.md).

## Decision

Hecate keeps **Chats** and **Tasks** as separate top-level surfaces. They serve
different operator intents:

- Chats is the conversational history and interaction surface.
- Tasks is the execution, supervision, retry, and evidence surface.

The two surfaces may link to the same work, but they are not alternate names
for the same object. A tools-enabled Hecate Chat turn can create or continue a
Task and its Run. A direct-model or External Agent turn does not create a
Hecate Task. Tasks can also exist without a source Chat.

## Canonical Vocabulary

### Interaction

| Term     | Meaning                                                                                                                   |
| -------- | ------------------------------------------------------------------------------------------------------------------------- |
| Chat     | A durable user-visible conversation. The API and storage object is a `chat_session`; **session** is technical vocabulary. |
| Turn     | One user submission and its lifecycle through a terminal outcome in a Chat, identified by a stable `turn_id`.             |
| Message  | A durable user, assistant, system, or tool communication within a Chat.                                                   |
| Activity | Progress or execution detail presented inside a Chat turn without pretending to be another conversational message.        |

### Execution

| Term       | Meaning                                                                                                                  |
| ---------- | ------------------------------------------------------------------------------------------------------------------------ |
| Task       | Durable execution intent and configuration. It groups related Runs.                                                      |
| Run        | One execution episode under a Task. Start, retry, resume, or continuation can create a Run; approval requeue is not one. |
| Model call | One request/response round trip between Hecate's agent loop and a model.                                                 |
| Tool call  | One requested tool invocation and its result.                                                                            |
| Step       | A numbered runtime progress unit recorded on a Run.                                                                      |
| Event      | An immutable lifecycle or telemetry record.                                                                              |
| Artifact   | Durable output or evidence produced by a Run.                                                                            |

## Relationships

The supported relationships are deliberately asymmetric:

- A direct-model Chat turn belongs only to its Chat.
- An External Agent Chat turn belongs to a Hecate Chat and an adapter-owned
  native session; it does not imply a Hecate Task or Run.
- The first tools-enabled turn in a Hecate Chat segment creates a Task and a
  Run. Consecutive tools-enabled turns continue that Task with new Runs.
- Switching away from tools-enabled execution ends that segment. Re-enabling
  tools creates a new Task-backed segment instead of silently reviving the old
  Task.
- Retrying or resuming from Tasks creates execution history; it does not invent
  a new Chat turn.
- A standalone Task has no source Chat.

The user and assistant messages created for one submitted Chat turn share its
`turn_id`; assistant context packets repeat that value as `refs.turn_id`. A
task-backed turn additionally carries `task_id` and `run_id`, where `run_id`
identifies the exact backing Task Run. Direct-model and External Agent turns do
not use `run_id`.

When both objects exist, navigation preserves their exact identity: Chat links
target a Chat and, when known, a Message; Task links target a Task and, when
known, a Run. A Run with a persisted Chat source exposes that canonical
Chat/Turn/Message identity as `source_ref`; clients do not reconstruct it by
matching transcript `task_id` / `run_id` fields. Retry and resume Runs retain
the source reference without creating a new Chat Turn.

## Naming Rules

- Product navigation and headings use **Chats** and **Tasks**.
- User-facing conversational lifecycle language uses **turn**.
- The agent loop never uses **turn** for an LLM round trip; it uses
  **model call**. Events use `model.call.*` and payloads use
  `model_call_index`. Counts and indices are always Run-local; inherited
  conversation context does not create Task-global model-call numbering.
- **Session** remains valid in API, storage, and adapter integration names, but
  it is not the primary product label for a Chat.
- **Thread** is not a Hecate product object. It may appear only when quoting or
  integrating with a system that owns that term.
- A Run is not called a turn, and a Chat turn is not called a Run.
- `turn_id` is the only Chat Turn identity. `run_id` is the Task Run identity
  and is never read as a fallback for a missing `turn_id`.

This is a clean contract. Removed agent-loop `turn.*`, `turn_index`,
retry-from-turn, and max-turn names have no aliases or dual-read behavior.

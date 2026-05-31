# Hecate Chat and Model Capabilities

> **Status:** accepted; partially implemented alpha direction.
> **Current source of truth:** [Chat sessions](../chat-sessions.md),
> [Agent runtime](../agent-runtime.md), and [Runtime API](../runtime-api.md).
> **Next action:** implement workspace modes, named agent profiles, automatic
> capability probes, and broader e2e hardening.
>
> **Terminology note:** this RFC was written while "Hecate Agent" was the
> proposed product label for Hecate-owned tools-on chat. Current UI and
> operator docs use **Hecate Chat** as the product surface and describe
> tools-on turns as task-backed Hecate Chat segments. Older "Hecate Agent"
> wording below is design history unless the current docs say otherwise.

## Summary

Chats exposes one agent picker. Hecate-owned execution is nested inside the
Hecate Chat choice as a tools-enabled mode:

| UI surface                   | Runtime mode   | Who owns execution                                                                                               |
| ---------------------------- | -------------- | ---------------------------------------------------------------------------------------------------------------- |
| Hecate, tools off            | Model          | A selected provider/model answers directly through the gateway.                                                  |
| Hecate, tools on             | Hecate Agent   | Hecate creates and continues a visible `agent_loop` task with Hecate tools, approvals, artifacts, and telemetry. |
| Codex / Claude Code / Cursor | External Agent | The adapter owns the native session; Hecate supervises it.                                                       |

Use **Hecate Agent** as the product name. "Model + tools" is only an
internal shorthand for the implementation idea; it should not appear in the UI
as a target name.

The baseline implementation ships one built-in Hecate Agent mode: selected
provider/model, shared workspace, Hecate `agent_loop` tools, task approvals,
task artifacts, per-call sandboxing, OpenTelemetry, and visible Tasks
integration.

That core bridge is not the finish line. Hecate Agent should become a
chat-native way to operate the task runtime, not a thin launcher that sends the
operator away to Tasks for every serious action. The next implementation
requirements after the baseline bridge are:

- run activity rendered in Chats from task-run events _(implemented)_
- task approvals resolved directly from Chats _(implemented)_
- streamed assistant text for task-backed Hecate Agent turns _(implemented)_
- local composer queueing while a backing task is busy _(implemented)_
- task workspace modes exposed in Hecate Chat setup
- named agent profiles
- richer automatic capability detection with visible status

## Why

Hecate originally had two chat-like surfaces:

- **Model chat** for direct OpenAI-/Anthropic-shaped provider traffic.
- **Agent Chat** for external coding-agent adapters.

That left a gap: Hecate already has a native `agent_loop` runtime with tools,
approvals, artifacts, resumable runs, and telemetry, but there was no chat UX
that used it directly. Hecate Agent fills that gap without creating a second
lightweight tool-loop runtime.

The session model follows the continuity users expect from Claude Code, Codex,
and Cursor: one chat session maps to one visible conversation, while each
message records the runtime segment that produced it. For Hecate Agent, a
tools-enabled segment is one Hecate-owned task. The first prompt in a
tools-enabled segment creates the task and starts the first run; follow-up
prompts continue the latest terminal run on the same task until the operator
switches back to direct model chat.

The staged implementation keeps runtime boundaries explicit. Once a Hecate
Agent chat has a backing task, its provider/model picker renders the session
snapshot read-only while tools are enabled. Turning tools off uses the normal
direct model chat path and the current draft provider/model selection. Agent
chat messages now carry their own `execution_mode`, `segment_id`, provider/model
snapshot, capability snapshot, and task/run linkage so a single transcript can
explain mixed direct-model and task-backed stretches without relying on the
current header state. Re-enabling tools after direct-model turns creates a new
task-backed segment in the same chat instead of rewriting or resuming the older
task segment.

## Non-goals

- Do not merge external agents into the provider/model list.
- Do not call the product target "model + tools."
- Do not create a second runtime beside `agent_loop` for Hecate-owned tools.
- Do not solve endpoint namespace/versioning here. The endpoint-versioning RFC
  can rename the current `/v1/...` Hecate endpoints later.

## Model Capability Registry

Hecate needs to know whether a selected model can call tools before it offers
Hecate Agent. The first capability record is deliberately small:

```ts
type ModelCapabilities = {
  tool_calling: "unknown" | "none" | "basic" | "parallel";
  streaming?: boolean;
  max_context_tokens?: number;
  source: "unknown" | "catalog" | "provider";
};
```

Capability sources merge with this precedence:

1. **Provider-discovered capabilities**: provider-native metadata wins when
   available (for example Ollama's native `/api/show` capability list).
2. **Catalog default**: Hecate-shipped metadata for known provider/model
   families.
3. **Provider-discovered existence**: provider says the model exists but not
   necessarily what it can do.
4. **Unknown local/custom default**: local/custom models default to
   `tool_calling="unknown"` until proven otherwise.

`GET /v1/models` includes the effective capability snapshot in
`metadata.capabilities` so the UI can render badges and decide whether a
tools-on Hecate Chat turn should create a task-backed segment or fall back to
direct model chat. The operator chooses tools per chat; there is no global
model-capability override UI or API in the current implementation.

### Automatic probing

Automatic probing is required before this feature can feel stable for local
and custom providers. This is a product requirement, but it must be controlled,
observable, and never surprising.

Automatic probes should:

1. Run only for models that are configured and routable.
2. Use a small, deterministic tool-calling probe request with a harmless tool
   schema.
3. Never execute a tool or mutate a workspace.
4. Respect a per-provider cooldown so the gateway does not probe every page
   load.
5. Persist results as provider/catalog metadata with timestamp, provider, model, probe
   status, and any safe error summary.
6. Surface probe state in Connections and the model picker: unknown, testing,
   tools supported, no tools, failed.
7. Let operators disable automatic probes globally or per provider/model.

A failed probe must not override a provider-native capability result.

## Hecate Agent Sessions

Chat sessions use a stable `agent_id` for ownership, while each message
records the execution mode that produced that turn:

```ts
type ChatAgentID = "hecate" | "codex" | "claude_code" | "cursor_agent" | string;
type ChatExecutionMode = "hecate_task" | "external_agent";
```

Hecate Chat sessions also store:

- `agent_profile_id`
- `task_id`
- `latest_run_id`
- `provider`
- `model`
- `capabilities`
- `workspace`
- `workspace_branch`

Each message carries its own runtime snapshot: `execution_mode`, `segment_id`,
provider/model, capabilities, workspace, and optional task/run linkage. The
session response also exposes derived segment metadata so the UI can render
clear transcript boundaries when a chat moves from direct model turns to
tools-on task-backed turns and back again.

External Agent sessions store their adapter id in `agent_id`. Hecate Chat
sessions use `agent_id="hecate"`.

### Agent profiles

Agent profiles are saved runtime configurations for Hecate Chat or an external
agent. They should not be confused with presets, which are authoring-time
templates, or with External Agent adapters, which are supervised subprocesses.

A profile defines:

- display name and description
- default provider/model or provider/model policy
- default workspace mode
- optional system prompt layer
- allowed tools and MCP servers
- approval policy defaults
- cost, turn, timeout, and network guardrails
- optional model capability requirements

The initial built-in profile is the current default Hecate Chat tools-on behavior:
selected provider/model, selected workspace, `agent_loop`, task approvals,
artifacts, sandboxed tool calls, and OTel. Operators can later create named
profiles such as "Reviewer", "Builder", or "SRE" without changing the chat
session model.

Sessions snapshot the selected profile id and effective profile settings at
creation time. Profile edits should not silently rewrite historical sessions.

### First prompt

For `execution_mode="hecate_task"` the first user message:

1. Validates that the selected model is known tool-capable
   (`tool_calling="basic"` or `parallel`).
2. Applies the selected Hecate Agent profile.
3. Creates a visible task with `execution_kind="agent_loop"`.
4. Marks the task origin as `origin_kind="chat"` and
   `origin_id=<chat_session_id>`.
5. Uses an execution profile such as `chat_agent`.
6. Starts the task run with the first user message as the prompt.
7. Stores `task_id` and `latest_run_id` on the chat session.

### Follow-up prompts

Follow-up messages continue the latest terminal run through the existing
`ContinueAgentTask` path. They do not create a new task per message.

If the latest backing run is `queued`, `running`, or `awaiting_approval`, the
message endpoint returns:

```text
409 chat.agent_session_busy
```

The UI should point the operator to the active task/run or approval.
The operator UI may also keep a local FIFO for prompts typed while the backing
task is busy. That queue is a UX layer above the API invariant: queued prompts
are not persisted until the UI submits them after the active task settles.

If the selected model is not known to support tools and the client still
requests `execution_mode="hecate_task"`, the message endpoint returns:

```text
422 chat.model_capability_required
```

The API copy is:

```text
Tools are unavailable for this model. Send as direct model chat or choose a tool-capable model.
```

The operator UI should normally avoid this server error: when the selected
model's tool support is `unknown` or `none`, Hecate Chat keeps the composer
sendable and starts a direct model segment instead. The UI should use a compact
capability hint plus the model picker, not a global "enable tools" override.

## Workspace Modes

Hecate Agent must expose the same workspace mode choices that matter for
Tasks. The chat setup should not hide this behind an implementation default.

The UI should support:

| Mode             | Meaning                                                                                                             | Default                                                                     |
| ---------------- | ------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| Shared workspace | Run directly in the selected local workspace.                                                                       | Default for Hecate Chat tools-on segments while the product is local-first. |
| Isolated clone   | Create an isolated task workspace from a repo/base branch when available.                                           | Optional when repo metadata is configured.                                  |
| Read-only review | Allow reads and analysis, block writes unless the operator explicitly switches mode or approves a writable profile. | Useful for reviewer profiles.                                               |

The session stores both the chosen workspace path and workspace mode. The
backing task should receive the same mode fields as a task created directly
from Tasks, so approvals, patch review, artifacts, and OTel do not fork.

When a workspace mode cannot be honored, the message endpoint should fail
before starting the run with a stable error and operator-facing copy.

## Run Activity In Chats

Tasks remains canonical for task execution, but Hecate Agent Chats must render
the live run activity directly. Operators should not need to switch to Tasks
just to understand whether the agent is thinking, waiting, running a tool, or
blocked on approval.

Chats should subscribe to the backing task-run stream and project task events
into the shared transcript/activity UI:

| Task-run source     | Chat rendering                                            |
| ------------------- | --------------------------------------------------------- |
| `run.*`             | run started, queued, completed, failed, cancelled         |
| `turn.*`            | thinking / model turn progress                            |
| `tool.*`            | tool started, running, output, failed                     |
| `approval.*`        | approval requested/resolved                               |
| `artifact.*`        | final answer, patch, stdout/stderr, conversation snapshot |
| `gap.*` / `error.*` | stream gap or runtime error                               |

This is projection, not a second event system. The source of truth stays the
task run event log and task artifacts.

Chat rows should preserve the same top-to-bottom conversation order as Model
and External Agent chats. Run activity is attached to the active assistant turn
or shown in a collapsible run details block below it. The primary activity
list should stay quiet: model turns, tool calls, approvals, files changed, and
the final outcome are first-class. Raw artifacts, stdout/stderr files, and
other low-level task internals belong under an expandable **Details** group.

## Approval Flow In Chats

Hecate Agent approvals should be resolvable from Chats as well as Tasks.

Approval UI requirements:

- show pending task approvals for the backing task/run in the active chat
- render the approval kind, command/tool name, workspace, and risk summary
- offer Approve / Deny with optional note
- link to the full Task approval view for deeper inspection
- update from task SSE events without polling-only behavior
- keep approval decisions persisted in the existing task approval store

This must reuse task approvals. Do not create a new Hecate Agent chat approval
store. External Agent approvals continue using the external-adapter approval
coordinator because those requests come from ACP adapters, not Hecate's native
task runtime.

## UI Contract

The Chats agent picker is:

```text
Hecate | Codex | Claude Code | Cursor
```

`Hecate` contains a tools toggle:

- **tools off** — direct provider/model chat. It keeps today's route/cost/cache
  / trace metadata and model-chat persistence.
- **tools on** — Hecate Agent. This is the default. The selected
  provider/model enters Hecate's native task runtime when it is known to
  support tools; otherwise the next prompt falls back to direct model chat with
  a visible capability hint.

### Hecate Agent

Shows:

- provider picker
- model picker
- per-chat instructions editor, sent as the task system prompt for tools-on
  turns and locked after the first message
- workspace selector
- workspace mode selector
- profile selector
- tools on/off switch
- per-assistant-turn backing Task/run links
- live run activity from task-run events
- pending task approvals with Approve / Deny actions

Task-backed sends are disabled unless a workspace is selected. If the selected
model is not known to support tools (`tool_calling` is `unknown` or `none`),
Hecate Chat should keep normal chat available: the next send starts a direct
model segment, the tools indicator reports that tools are unavailable for the
selected model, and repair copy points to choosing a known tool-capable model.
Provider-discovered capability data, such as Ollama's native model metadata,
should narrow the unknown state without introducing a global per-model tools
override.

When a task-backed Hecate Chat segment is running, provider/model controls are
locked to that segment's snapshot and the chat composer treats the whole session
as busy. Operators can turn tools off while waiting, but direct model sends are
blocked until the backing task finishes, is cancelled, or reaches an approval
the operator resolves. The busy composer should keep **Open task** and **Stop**
close to the input so the operator does not need to hunt for the canonical Task
view. After the active task settles, tools-off sends create normal direct model
segments; turning tools on again creates a new task-backed segment instead of
mutating the older task.

On browser refresh or reconnect, the UI should hydrate from the persisted
Agent Chat session plus its backing task/run snapshot. Active task-backed
segments must come back as running, awaiting approval, completed, cancelled, or
failed without requiring a fresh prompt.

Task-backed Hecate Chat uses the task runtime, so approvals, artifacts,
diff/patch review, workspace modes, retry/resume, and OTel should come from
Tasks rather than a new parallel agent runtime.

### External Agent

Keeps the Codex / Claude Code / Cursor Agent / Grok Build flow. It remains unsandboxed and
adapter-owned; Hecate supervises the session, records transcript/diagnostics,
and exposes external-agent approvals.

## Storage

Memory and SQLite must persist:

- agent profiles
- selected `agent_profile_id`
- `agent_id`
- task-backed Hecate Chat task/run linkage fields on chat sessions
- per-message `execution_mode`, `segment_id`, provider/model, capability
  snapshot, and task/run linkage
- derived API segment metadata from the persisted message snapshots
- workspace mode snapshot on task-backed Hecate Chat sessions

The effective capability record is a snapshot at session creation time. Model
metadata can change later, but a running task-backed Hecate Chat segment should
keep the capability record it was created with for audit/debugging.

Profile settings and workspace mode should also be snapshotted onto the
session or backing task. Historical sessions should explain what was actually
run even if the profile changes later.

## Testing

Minimum coverage:

- Capability precedence: provider-discovered metadata > catalog > provider
  existence > unknown local default.
- `/v1/models` includes capability metadata.
- Task-backed Hecate Chat first message creates a visible task/run.
- Task-backed Hecate Chat follow-up continues the same task after the latest run is
  terminal.
- Busy backing runs return `409 chat.agent_session_busy`.
- Explicit `hecate_task` requests against models with disabled tools return
  `422 chat.model_capability_required`.
- Memory/SQLite parity for new session fields.
- UI target picker, tools on/off switches, tools-unavailable direct fallback,
  and task/run links.
- Task-backed Hecate Chat run activity projection from task-run SSE into Chats.
- Task-backed Hecate Chat task approval banner in Chats, including approve, reject, and a
  link to the backing Task.
- Busy-state UX and local queued-prompt behavior in Chats.
- Workspace mode selection and task creation parity.
- Agent profile CRUD, profile selection, session snapshotting, and built-in
  default profile behavior.
- Richer provider-native capability detection, cooldown, and failure behavior.

## Implementation Status

Done in the core bridge:

- agent picker exposes Hecate plus built-in external adapters, with a tools
  toggle inside Hecate for direct model chat vs. Hecate Agent execution
- Hecate Agent creates and continues visible `agent_loop` tasks
- provider/catalog model capability snapshots choose whether tools-on turns run
  through the task runtime or fall back to direct model chat
- chat sessions store task/run linkage
- Tasks labels chat-origin tasks and links back to Chats; Hecate Agent
  assistant turns link back to their backing Task/run
- backing task-run activity is projected into Hecate Agent chat transcripts
- Chats and Task Detail share the same compact transcript activity renderer
  for tool calls, approvals, changed files, final-answer artifacts, and
  low-level Details grouping
- pending task approvals can be approved or rejected from the Hecate Agent
  chat banner while Tasks remains canonical
- streamed assistant text from the backing task updates the chat transcript
  before the run reaches a terminal state when the provider route supports SSE
- tools-off direct model turns and Hecate Agent turns share one Agent Chat
  transcript using `execution_mode="hecate_task"` with `tools_enabled=false`
  or `tools_enabled=true` message snapshots
- turning tools back on after a direct model segment creates a new task-backed
  segment in the same transcript
- Hecate Chat queues prompts locally while the active task-backed segment is
  busy and submits them after the run or approval reaches a terminal state
- each assistant turn exposes user-friendly task and trace links in the message
  header

Still required for a complete Hecate Chat tools-on experience:

- workspace modes in the chat setup
- named agent profiles
- automatic capability probing
- broader e2e/product hardening around workspace modes, profiles, automatic
  capability detection, and mixed long-running sessions

## Recommended Next Work

The missing stable-scope pieces should land in this order:

1. **Workspace modes.** Expose the same workspace choices that Tasks supports,
   store the selected mode on the session/task, and fail early when a requested
   mode cannot be honored.
2. **Agent profiles.** Add named profiles for model policy,
   workspace mode, system prompt, tools/MCP, approvals, and guardrails. Store a
   snapshot on each session so history remains explainable.
3. **Automatic probing.** Add bounded, visible capability probes for configured
   models so local/custom providers can become eligible without manual edits.
   Probes must not execute tools or mutate workspaces, and provider-native
   capability metadata remains the stronger source when it is available.
4. **E2E hardening.** Extend the existing browser paths to cover workspace
   modes, profiles, automatic capability detection, refresh/reconnect edges,
   and long mixed chats with queued prompts.

## Future Work

- Auto-compatible model routing by profile capability requirements.
- Richer capability dimensions: structured output, reasoning/thinking,
  multimodal inputs, cache-control, context-window confidence.
- Tool schema fidelity: distinguish basic tool calling from strict or
  provider-native tool schemas, forced tool choice, nested JSON schema support,
  `enum` / `required` reliability, and parallel-tool behavior. This should
  become both a capability dimension and a probe target after the first stable
  Hecate Agent path.
- Keep endpoint examples aligned with the implemented `/hecate/v1/...`
  namespace.

## Decision Bias

Prefer explicit capability records over magic. Hecate should not infer
"agentness" from a model name when a provider can give stronger metadata. It
should know what a model can do and show that clearly to the operator. During
alpha, task-backed tools-on execution requires a known tool-capable value
(`basic` or `parallel`); unknown local/custom models stay visibly unknown and
fall back to direct model chat until provider metadata or a safe probe marks
them tool-capable. Before stable, automatic probing should make that unknown
state much rarer without silently overwriting provider-native facts.

# Hecate Agent Chats and Model Capabilities

> **Status:** accepted; the baseline chat-to-task bridge has landed, including
> chat-visible run activity and task approval resolution. Stable Hecate Agent
> still requires workspace modes, profiles, automatic probing, and broader e2e.
> **Related:** [Chat sessions](../chat-sessions.md),
> [External agent adapters](external-agent-adapters.md),
> [Agent runtime](../agent-runtime.md), [Runtime API](../runtime-api.md).
> **Owner:** see [`AGENTS.md`](../../AGENTS.md).

## Summary

Chats exposes two top-level targets, with Hecate-owned agent execution nested
inside Hecate Chat as a tools-enabled mode:

| UI surface | Runtime mode | Who owns execution |
|---|---|---|
| Hecate Chat, tools off | Model | A selected provider/model answers directly through the gateway. |
| Hecate Chat, tools on | Hecate Agent | Hecate creates and continues a visible `agent_loop` task with Hecate tools, approvals, artifacts, and telemetry. |
| External Agent | External Agent | Codex, Claude Code, Cursor Agent, or another adapter owns the native session; Hecate supervises it. |

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
- task workspace modes exposed in the Hecate Agent chat setup
- named Hecate Agent profiles
- automatic capability probing with explicit operator control

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
chat messages now carry their own `runtime_kind`, `segment_id`, provider/model
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
  source: "unknown" | "catalog" | "provider" | "probe" | "operator_override";
};
```

Capability sources merge with this precedence:

1. **Operator override**: explicit setting in Hecate wins.
2. **Manual probe result**: operator records a known test result.
3. **Catalog default**: Hecate-shipped metadata for known provider/model
   families.
4. **Provider-discovered existence**: provider says the model exists but not
   necessarily what it can do.
5. **Unknown local/custom default**: local/custom models default to
   `tool_calling="unknown"` until proven otherwise.

Manual controls are explicit:

- `PUT /v1/model-capabilities/overrides`
- `DELETE /v1/model-capabilities/overrides?provider=...&model=...`
- `POST /v1/model-capabilities/probes`

`GET /v1/models` includes the effective capability snapshot in
`metadata.capabilities` so the UI can render badges and disable Hecate Agent
when tool support is unknown or absent.

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
5. Persist results as `source="probe"` with timestamp, provider, model, probe
   status, and any safe error summary.
6. Surface probe state in Settings and the model picker: unknown, testing,
   tools supported, no tools, failed.
7. Let operators disable automatic probes globally or per provider/model.

Manual override still wins over automatic probe. A failed probe must not
overwrite an explicit operator override.

## Hecate Agent Sessions

Agent Chat sessions gain a `runtime_kind`:

```ts
type AgentChatRuntimeKind = "model" | "agent" | "external_agent";
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

Each message carries its own runtime snapshot: `runtime_kind`, `segment_id`,
provider/model, capabilities, workspace, and optional task/run linkage. The
session response also exposes derived segment metadata so the UI can render
clear transcript boundaries when a chat moves from direct model turns to
Hecate Agent tool-backed turns and back again.

External Agent sessions keep their adapter fields. Existing sessions without
`runtime_kind` default to `external_agent` when `adapter_id` is present and
`model` otherwise.

### Agent profiles

Hecate Agent profiles are named presets for Hecate-owned agent execution. They
should not be confused with External Agent adapters.

A profile defines:

- display name and description
- default provider/model or provider/model policy
- default workspace mode
- optional system prompt layer
- allowed tools and MCP servers
- approval policy defaults
- cost, turn, timeout, and network guardrails
- optional model capability requirements

The initial built-in profile is the current default Hecate Agent behavior:
selected provider/model, selected workspace, `agent_loop`, task approvals,
artifacts, sandboxed tool calls, and OTel. Operators can later create named
profiles such as "Reviewer", "Builder", or "SRE" without changing the chat
session model.

Sessions snapshot the selected profile id and effective profile settings at
creation time. Profile edits should not silently rewrite historical sessions.

### First prompt

For `runtime_kind="agent"` the first user message:

1. Validates that tools are not explicitly disabled for the selected model
   (`tool_calling!="none"`). Unknown models are allowed by default for now;
   Settings provides the operator-facing tools on/off switch.
2. Applies the selected Hecate Agent profile.
3. Creates a visible task with `execution_kind="agent_loop"`.
4. Marks the task origin as `origin_kind="agent_chat"` and
   `origin_id=<agent_chat_session_id>`.
5. Uses an execution profile such as `chat_agent`.
6. Starts the task run with the first user message as the prompt.
7. Stores `task_id` and `latest_run_id` on the chat session.

### Follow-up prompts

Follow-up messages continue the latest terminal run through the existing
`ContinueAgentTask` path. They do not create a new task per message.

If the latest backing run is `queued`, `running`, or `awaiting_approval`, the
message endpoint returns:

```text
409 agent_chat.agent_session_busy
```

The UI should point the operator to the active task/run or approval.

If tools are explicitly disabled for the selected model, the message endpoint returns:

```text
422 agent_chat.model_capability_required
```

The UI copy is:

```text
Tools are disabled for this model. Turn tools off for direct model chat or enable tools in Settings.
```

## Workspace Modes

Hecate Agent must expose the same workspace mode choices that matter for
Tasks. The chat setup should not hide this behind an implementation default.

The UI should support:

| Mode | Meaning | Default |
|---|---|---|
| Shared workspace | Run directly in the selected local workspace. | Default for Hecate Agent chats while the product is local-first. |
| Isolated clone | Create an isolated task workspace from a repo/base branch when available. | Optional when repo metadata is configured. |
| Read-only review | Allow reads and analysis, block writes unless the operator explicitly switches mode or approves a writable profile. | Useful for reviewer profiles. |

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

| Task-run source | Chat rendering |
|---|---|
| `run.*` | run started, queued, completed, failed, cancelled |
| `turn.*` | thinking / model turn progress |
| `tool.*` | tool started, running, output, failed |
| `approval.*` | approval requested/resolved |
| `artifact.*` | final answer, patch, stdout/stderr, conversation snapshot |
| `gap.*` / `error.*` | stream gap or runtime error |

This is projection, not a second event system. The source of truth stays the
task run event log and task artifacts.

Chat rows should preserve the same top-to-bottom conversation order as Model
and External Agent chats. Run activity is attached to the active assistant turn
or shown in a collapsible run details block below it.

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

The Chats target picker is:

```text
Hecate Chat | External Agent
```

`Hecate Chat` contains a tools toggle:

- **tools off** — direct provider/model chat. It keeps today's route/cost/cache
  / trace metadata and model-chat persistence.
- **tools on** — Hecate Agent. This is the default. The selected
  provider/model enters Hecate's native task runtime unless the model is
  explicitly marked "tools off" in Settings.

### Hecate Agent

Shows:

- provider picker
- model picker
- workspace selector
- workspace mode selector
- profile selector
- tools on/off switch
- per-assistant-turn backing Task/run links
- live run activity from task-run events
- pending task approvals with Approve / Deny actions

Send is disabled unless a workspace is selected. If the selected model has
`tool_calling="none"`, the tools-on send path is disabled and the operator can
either turn tools off for direct model chat or enable tools in Settings.

When a Hecate Agent task-backed segment is running, provider/model controls are
locked to that segment's snapshot. Operators can turn tools off to use direct
model chat in the same transcript. If they later turn tools on again, Hecate
creates a new task-backed segment instead of mutating the older task.

Hecate Agent uses the task runtime, so approvals, artifacts, diff/patch review,
workspace modes, retry/resume, and OTel should come from Tasks rather than a
new parallel agent runtime.

### External Agent

Keeps the Codex / Claude Code / Cursor Agent flow. It remains unsandboxed and
adapter-owned; Hecate supervises the session, records transcript/diagnostics,
and exposes external-agent approvals.

## Storage

Memory and SQLite must persist:

- capability overrides
- manual probe results
- automatic probe results
- Hecate Agent profiles
- selected `agent_profile_id`
- `runtime_kind`
- Hecate Agent task/run linkage fields on agent-chat sessions
- per-message `runtime_kind`, `segment_id`, provider/model, capability
  snapshot, and task/run linkage
- derived API segment metadata from the persisted message snapshots
- workspace mode snapshot on Hecate Agent sessions

The effective capability record is a snapshot at session creation time. Model
metadata can change later, but a running Hecate Agent session should keep the
capability record it was created with for audit/debugging.

Profile settings and workspace mode should also be snapshotted onto the
session or backing task. Historical sessions should explain what was actually
run even if the profile changes later.

## Testing

Minimum coverage:

- Capability precedence: override > probe > catalog > provider existence >
  unknown local default.
- `/v1/models` includes capability metadata.
- Override/probe endpoints persist and affect `/v1/models`.
- Hecate Agent first message creates a visible task/run.
- Hecate Agent follow-up continues the same task after the latest run is
  terminal.
- Busy backing runs return `409 agent_chat.agent_session_busy`.
- Explicitly disabled tools return `422 agent_chat.model_capability_required`.
- Memory/SQLite parity for capability records and new session fields.
- UI target picker, tools on/off switches, disabled Hecate Agent send state, and
  task/run links.
- Hecate Agent run activity projection from task-run SSE into Chats.
- Hecate Agent task approval banner in Chats, including approve, reject, and a
  link to the backing Task.
- Workspace mode selection and task creation parity.
- Agent profile CRUD, profile selection, session snapshotting, and built-in
  default profile behavior.
- Automatic probe scheduling, cooldown, persistence, disabled-probe behavior,
  and override precedence.

## Implementation Status

Done in the core bridge:

- target picker exposes Hecate Chat and External Agent, with a tools toggle
  inside Hecate Chat for direct model chat vs. Hecate Agent execution
- Hecate Agent creates and continues visible `agent_loop` tasks
- model capability snapshots gate only explicitly disabled tools
- manual probe records and operator overrides persist
- chat sessions store task/run linkage
- Tasks labels chat-origin tasks and links back to Chats; Hecate Agent
  assistant turns link back to their backing Task/run
- backing task-run activity is projected into Hecate Agent chat transcripts
- pending task approvals can be approved or rejected from the Hecate Agent
  chat banner while Tasks remains canonical
- direct model turns and Hecate Agent turns share one Agent Chat transcript
  using `runtime_kind="model"` / `runtime_kind="agent"` message
  snapshots
- turning tools back on after a direct model segment creates a new task-backed
  segment in the same transcript

Still required for a complete Hecate Agent experience:

- workspace modes in the chat setup
- named Hecate Agent profiles
- automatic capability probing
- full e2e covering provider setup, capability detection, Hecate Agent run,
  approval from Chats, final answer, and follow-up continuation

## Recommended Next Work

The missing stable-scope pieces should land in this order:

1. **Workspace modes.** Expose the same workspace choices that Tasks supports,
   store the selected mode on the session/task, and fail early when a requested
   mode cannot be honored.
2. **Agent profiles.** Add named Hecate Agent presets for model policy,
   workspace mode, system prompt, tools/MCP, approvals, and guardrails. Store a
   snapshot on each session so history remains explainable.
3. **Automatic probing.** Add bounded, visible capability probes for configured
   models so local/custom providers can become eligible without manual edits.
   Overrides still win, and probes must not execute tools or mutate workspaces.
4. **E2E hardening.** Cover provider setup, capability detection, chat-side task
   approval, final answer, and same-task follow-up continuation in one browser
   path.

## Future Work

- Auto-compatible model routing by profile capability requirements.
- Richer capability dimensions: structured output, reasoning/thinking,
  multimodal inputs, cache-control, context-window confidence.
- Tool schema fidelity: distinguish basic tool calling from strict or
  provider-native tool schemas, forced tool choice, nested JSON schema support,
  `enum` / `required` reliability, and parallel-tool behavior. This should
  become both a capability dimension and a probe target after the first stable
  Hecate Agent path.
- Convergence with endpoint-versioning (`/hecate/v1/...`) once that RFC lands.

## Decision Bias

Prefer explicit capability records over magic. Hecate should not infer
"agentness" from a model name. It should know what a model can do, show that
clearly to the operator, and only enable Hecate-owned agent execution when the
selected model is known to support the required tools.

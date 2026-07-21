# Hecate Chat and Model Capabilities

> **Status:** accepted; partially implemented alpha direction.
> **Current source of truth:** [Chat sessions](../../runtime/chat-sessions.md),
> [Agent runtime](../../runtime/agent-runtime.md), and [Runtime API](../../runtime/runtime-api.md).
> **Next action:** implement automatic capability probes and broaden e2e/product
> hardening. Named Agent Presets are now wired into Hecate Chat as a deliberately
> narrow immutable runtime snapshot, and manual tool-support verification is
> implemented.
>
> **Terminology note:** this design record was written while "Hecate Agent" was the
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

- Task Run activity rendered in Chats from Task Run events _(implemented)_
- task approvals resolved directly from Chats _(implemented)_
- streamed assistant text for task-backed Hecate Agent turns _(implemented)_
- local composer queueing while a backing task is busy _(implemented)_
- task workspace modes exposed in Hecate Chat setup _(implemented)_
- named Agent Presets consumed by Hecate Chat setup _(implemented as a narrow
  immutable snapshot)_
- richer automatic capability detection with visible status

## Why

Hecate originally had two chat-like surfaces:

- **Model chat** for direct OpenAI-/Anthropic-shaped provider traffic.
- **External Agent chat** for supervised external coding agents.

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
- Do not solve endpoint namespace/versioning here. The endpoint-versioning design record
  can rename the current `/v1/...` Hecate endpoints later.

## Model Capability Registry

Hecate needs to know whether a selected model can call tools before it offers
Hecate Agent. The first capability record is deliberately small:

```ts
type ModelCapabilities = {
  tool_calling: "unknown" | "none" | "basic" | "parallel";
  image_input: "unknown" | "none" | "supported";
  streaming?: boolean;
  max_context_tokens?: number;
  source: "unknown" | "catalog" | "provider" | "mixed";
  tool_verification?: {
    status: "testing" | "supported" | "unsupported" | "inconclusive";
    checked_at: string;
    expires_at: string;
    reason?: string;
  };
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

Model discovery provenance is not capability provenance. Seeing a model in a
provider's `/models` response does not make Hecate's name-based/default
capability inference provider-native. `source="mixed"` means at least one
effective dimension came from provider metadata while another still comes from
catalog inference. For Auto, the pre-route snapshot contains only guarantees
shared by the currently routable matching routes; the completed direct turn
updates its message and session snapshots from the route the gateway actually
used.

`GET /v1/models` includes the effective capability snapshot in
`metadata.capabilities` so the UI can render badges and decide whether a
tools-on Hecate Chat turn should create a task-backed segment or fall back to
direct model chat. The operator chooses tools per chat; there is no global
model-capability override UI or API in the current implementation.

Image input uses the same precedence with a stricter dispatch rule:
image-bearing turns require `image_input="supported"`; `unknown` and `none`
are ineligible. Provider-native Fireworks and Ollama metadata win when
available, with conservative catalog inference for known cloud model families.
Hecate-owned attachment turns set an explicit internal request requirement, and
the gateway router admits only an explicitly supported initial route. Once
image bytes are hydrated, dispatch is pinned to the canonical provider name and
opaque generation resolved during admission. The executor revalidates both
against the live registry immediately before dispatch; same-instance retries
remain available but cross-provider failover is disabled. Removal,
same-name replacement, normalized-name takeover, and alias takeover therefore
fail without retargeting bytes. A failed call that reached an upstream retains
its attempted provider/model/generation and trace metadata internally.
Provider identity admission includes configured providers that currently report
no model rows, so alias collisions remain fail-closed. Historical bytes are
rehydrated only for the configured provider generation recorded on the original
turn; legacy missing generations are omitted. Provider-compatible
`/v1` rich-content requests do not set the Hecate requirement and retain normal
upstream passthrough semantics; this matters for custom providers whose
discovery API does not report image support. Image-bearing compatibility
requests still disable cross-provider failover, revalidate the selected opaque
provider instance immediately before dispatch, and use the bounded 32 MiB,
60-second compatibility ingress contract.
Hecate-owned Chat accepts staged PNG/JPEG/WebP attachments with Tools off or
with task-backed Tools on. The task-backed path persists only an opaque input
reference and hydrates the image immediately before its fenced agent-loop
dispatch. External Agent/ACP sessions accept bounded arbitrary files and
resolve them against live ACP image/embedded-resource capabilities, with a
private per-turn `resource_link` as the baseline fallback; that path does not
weaken direct-model image-capability admission.

### Explicit tool-support verification

The first verification path is deliberately manual. In **Connections**, a
ready model whose effective `tool_calling` value is `unknown` can offer
**Verify tool support**. This keeps a potentially billable provider request out
of page loads, model refreshes, and Chat send paths. It is not available for
Auto routes, unavailable models, or models that already have a known
provider/catalog capability.

One operator action makes at most one bounded request to the exact configured
provider, model, and opaque provider configuration generation. Hecate supplies
a fixed one-message prompt, a fixed harmless `hecate_capability_probe` function
schema, and a forced choice of that function. It observes whether the provider
returns that function call; it does not parse or execute its arguments, invoke
a Hecate tool, start a task, or access a workspace, chat prompt, attachment, or
External Agent session. The request never retries or fails over to another
provider. It can incur the selected provider's normal model-request charge.

The durable result is only a safe observation: status, checked/expiry times,
and a bounded reason code. It contains no prompt or response text, tool
arguments, provider endpoint, provider generation, or credentials. A matching
active result is reused so repeated clicks do not create another provider call;
concurrent verification requests coalesce. A provider replacement or
configuration change makes the old observation inapplicable.

For an otherwise unknown capability only:

- `supported` projects `tool_calling="basic"`.
- an explicit tool-schema rejection projects `tool_calling="none"`.
- `testing` or `inconclusive` leaves `tool_calling="unknown"`.

Provider-native and catalog-known capabilities remain authoritative. A manual
observation is shown as `tool_verification` provenance but must never override
an effective `none`, `basic`, or `parallel` value. In particular, an
inconclusive network, authentication, rate-limit, timeout, policy, or provider
failure is not proof that the model lacks tool support.

For every tools-on Hecate task, a matching `supported` observation may satisfy
only an otherwise-unknown **tool** requirement. It stays bound to the same
exact provider name, model, and generation until the proof expires, with
failover disabled; it neither proves `image_input` nor makes an Auto route
eligible. An attachment turn keeps its separate explicit image-support gate.
Final dispatch rechecks those fences so a queued run, retry, or delayed stream
cannot outlive the proof.

Automatic capability verification is future work, not an implication of this
manual action. Any proposal for it needs separate cost, consent, cooldown, and
operator-control decisions; it must preserve the same exact-route, no-tool-
execution, and provider-native-precedence boundaries.

## Hecate Agent Sessions

Chat sessions use a stable `agent_id` for ownership, while each message
records the execution mode that produced that turn:

```ts
type ChatAgentID = "hecate" | "codex" | "claude_code" | "cursor_agent" | string;
type ChatExecutionMode = "hecate_task" | "external_agent";
```

Hecate Chat sessions also store:

- an optional immutable `agent_preset` snapshot
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

### Agent Presets

Agent Presets are Hecate-owned runtime posture. The implemented Chat slice is
not a second profile resolver and does not make an External Agent's native
configuration portable through Hecate.

At Hecate Chat creation, an operator may select a preset with
`surface=hecate_chat` or `surface=any`. Hecate freezes this Chat-safe subset on
the session:

- id and display name
- provider/model hints
- instructions
- execution profile
- tools, writes, and network posture

An omitted selection preserves the existing Chat defaults. Provider/model hints
fill only omitted create-time values; an explicit operator choice wins. The
snapshot is returned on the session and never re-resolved, so later preset
edits or deletion do not rewrite a transcript or a backing Task.

Preset instructions are composed after the project prelude when present and
before the per-chat operator instructions. A tools-disabled snapshot locks the
Chat to direct model turns. For a permitted tools-on turn, the task captures the
snapshot id and tools setting, uses the non-empty execution profile, maps
`writes_allowed=false` to a read-only sandbox, and maps `network_allowed` to
the Task network setting.

This alpha slice deliberately excludes workspace-mode defaults, project-memory
and context-source policy, project skills, browser evidence, MCP servers,
approval-policy defaults, cost/turn/timeout guardrails, and External Agent
options. It also does not create or change Cairnline project, role, assignment,
or handoff records. Those remain separate Hecate or Cairnline contracts.

### First prompt

For `execution_mode="hecate_task"` the first user message:

1. Validates that the selected model is known tool-capable
   (`tool_calling="basic"` or `parallel`).
2. Uses the session's frozen Hecate Chat preset snapshot when one was selected.
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

The UI supports:

| Operator choice   | Stored mode                 | Meaning                                                                                                              |
| ----------------- | --------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| Managed workspace | `persistent` or `ephemeral` | Run task-backed turns in a separate Hecate-managed workspace. Both currently use clone/copy provisioning.            |
| Current folder    | `in_place`                  | Run directly in the selected folder. Tools write to the operator's live working tree, so this is an explicit opt-in. |

Read-only review is an Agent Preset/runtime-policy concern rather than a
workspace-provisioning mode. A future Git-worktree provisioner may implement
Managed workspace for eligible repositories without changing this stable
operator contract.

The session stores both the chosen workspace path and workspace mode. The
backing task receives the same mode field as a task created directly from
Tasks, so approvals, patch review, artifacts, and OTel do not fork. Managed
runs update the session to the generated execution path so later review and
continuation inspect the workspace the agent actually changed. A later task
segment reuses that runtime-owned path instead of recloning a dirty Git tree.
Changing mode is allowed before task-backed work exists and locked after the
first backing task is created; the operator UI pauses sends while the mutation
is unresolved.

When a workspace mode cannot be honored, the message endpoint should fail
before starting the run with a stable error and operator-facing copy.

## Task Run Activity In Chats

Tasks remains canonical for task execution, but Hecate Agent Chats must render
the live backing Task Run activity directly. Operators should not need to switch to Tasks
just to understand whether the agent is thinking, waiting, running a tool, or
blocked on approval.

Chats should subscribe to the backing Task Run stream and project Task events
into the shared transcript/activity UI:

| Task Run source     | Chat rendering                                            |
| ------------------- | --------------------------------------------------------- |
| `run.*`             | Run started, queued, completed, failed, cancelled         |
| `model.call.*`      | thinking / model-call progress                            |
| `tool.*`            | tool started, running, output, failed                     |
| `approval.*`        | approval requested/resolved                               |
| `artifact.*`        | final answer, patch, stdout/stderr, conversation snapshot |
| `gap.*` / `error.*` | stream gap or runtime error                               |

This is projection, not a second event system. The source of truth stays the
Task Run event log and Task artifacts.

Chat rows should preserve the same top-to-bottom conversation order as Model
and External Agent Chats. For a task-backed Turn, backing Task Run activity is
attached to the active assistant Turn or shown in a collapsible Task Run details
block below it. The primary activity list should stay quiet: model calls, tool
calls, approvals, files changed, and
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
- per-assistant-turn backing Task/Run links
- live Task Run activity from Task Run events
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

Memory, SQLite, and Postgres must persist:

- agent presets
- the selected Hecate Chat preset snapshot
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
- Manual tool-support verification for an unknown, ready configured model:
  exact-route request fencing, one-call/coalescing behavior, safe projection
  into `/v1/models`, and Connections result states.
- Task-backed Hecate Chat Task Run activity projection from Task Run SSE into Chats.
- Task-backed Hecate Chat task approval banner in Chats, including approve, reject, and a
  link to the backing Task.
- Busy-state UX and local queued-prompt behavior in Chats.
- Workspace mode selection and task creation parity.
- Agent Preset CRUD, Hecate Chat surface validation, selection, immutable
  session snapshotting, tools-disabled locking, and task posture mapping.
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
- backing Task Run activity is projected into Hecate Agent Chat transcripts
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
- Hecate Chat stages image bodies outside transcript JSON, persists immutable
  attachment metadata on direct-model user turns, renders previews through the
  normal Hecate-native API path (including the optional runtime-token guard),
  and transiently hydrates bounded image history only for explicitly
  image-capable routes
- Hecate Chat persists `persistent`, `ephemeral`, or `in_place` workspace
  posture, exposes Managed workspace versus Current folder in chat settings,
  snapshots it onto backing tasks, and locks posture after task-backed work
  exists; External Agent sessions remain in-place ACP workspaces
- Hecate Chat setup selects `hecate_chat` / `any` Agent Presets and freezes a
  Chat-safe runtime snapshot; preset instructions, provider/model hints, and
  task posture are applied without importing project, browser, MCP, or
  External Agent behavior
- Connections lets an operator explicitly verify tool support for a ready,
  otherwise-unknown provider/model. The result is generation-bound, safe to
  inspect in `metadata.capabilities.tool_verification`, and only projects onto
  an unknown effective tool capability.

Still required for a complete Hecate Chat tools-on experience:

- automatic capability probing or another explicit product decision for
  provider/model verification beyond the manual operator action
- broader e2e/product hardening around workspace modes, profiles, automatic
  capability detection, and mixed long-running sessions

## Recommended Next Work

The missing stable-scope pieces should land in this order:

1. **Automatic probing.** Add bounded, visible capability probes for configured
   models so local/custom providers can become eligible without manual edits.
   Probes must not execute tools or mutate workspaces, and must preserve manual
   verification's exact-route, consent, cost, cooldown, disable-control, and
   provider-native-precedence boundaries.
2. **E2E hardening.** Extend the existing browser paths to cover workspace
   modes, profiles, automatic capability detection, refresh/reconnect edges,
   and long mixed chats with queued prompts.
3. **Broader Chat preset scope.** Design and implement any additional preset
   fields explicitly instead of inheriting project-assignment or External Agent
   behavior into Chat by default.

## Future Work

- Auto-compatible model routing by profile capability requirements.
- Richer capability dimensions: structured output, reasoning/thinking, audio
  and document inputs, cache-control, context-window confidence.
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
fall back to direct model chat until provider metadata or an explicit safe
verification marks them tool-capable. A future automatic scheme must not
silently overwrite provider-native facts or surprise an operator with a paid
provider request.

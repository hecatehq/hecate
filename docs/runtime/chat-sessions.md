# Chat sessions

All chat persistence in Hecate today goes through chat sessions under
`/hecate/v1/chat/sessions`. The same store backs two session-owner categories
in the Chats workspace: Hecate-owned chats and supervised External Agent
sessions (Codex, Claude Code, Cursor Agent). Hecate-owned chats can contain direct
model turns and task-backed tools-on turns with a backing `agent_loop` task — see
[agent-runtime.md](agent-runtime.md) for the runtime.

The Chats workspace has one shell and an agent picker. **Hecate** is always
first and covers both direct model chat and Hecate-owned agent execution: the
tools toggle decides whether a prompt stays as a direct provider/model turn or
enters the native agent task runtime. Codex, Claude Code, Cursor Agent, and Grok Build entries in
the same picker create **External Agent** sessions.

Chats may also belong to a **Project**. Projects are optional durable identities
for a work area, not only a codebase; **No project** remains a valid chat scope.
When a project is selected in the Chats sidebar, new Hecate and External Agent
chat sessions are created with that `project_id`, and the chat list shows only
chats for the active project. When an open chat is linked to a project, the chat
header exposes a compact project shortcut that selects that project and opens
the Projects workspace. Project-linked Hecate Chat also exposes a compact
composer action that deterministically drafts a Project Assistant proposal from
the current message and hands it to the Projects review surface. The same path
is available as the Hecate-owned `/proposal <request>` chat command for
operators who prefer a slash-command flow. Project-shaping commands such as
`/plan`, `/work`, `/handoff`, and `/review` use the same proposal boundary:
selecting one only inserts the command scaffold, and submitting it drafts from
the text after the command. That handoff is proposal data only; it does not send
a chat message, create project records, call the model-backed draft path, or
apply the proposal.
When tools are on, project-linked Hecate Chat can also let the model call the
Hecate-owned `draft_project_proposal` tool from ordinary natural-language
planning intent. That produces a `project_assistant_proposal` task artifact on
the backing run and shows a compact transcript activity with the proposal title,
action count, and a **Review in Projects** action. It is still proposal data
only: no mirrored Project Assistant chat is created, and apply remains an
explicit Projects action.
Deleting a project also deletes its project-scoped chat transcripts.
Unprojected chats and chats in other projects stay untouched. The Projects
review card preserves the originating request and chat session id for operator
inspection and exposes an **Open source chat** action.

Hecate Chat treats model/provider readiness as part of composition, not a
send-time surprise. If no configured provider has routable models, the empty
state points at provider setup or local runtime discovery. If models exist but
the currently selected model is no longer reported by the selected provider
(for example after changing Ollama models), the composer is blocked with the
selected model, provider route, discovered-model count, health, and next steps
before any request is sent. Existing transcripts show the full readiness card
near the composer with an **Open Connections** action; empty chats show a compact
version in the empty state that still includes the discovered-model count,
health/blocking/error diagnostics, and short remediation steps. The compact card
is intentionally not just a warning — it should be enough to choose a discovered
model, accept a backend-suggested replacement when one is available, refresh
local provider discovery, or jump to Connections for the full readiness
checklist. Suggested replacement models should be offered as explicit repairs:
switch to the backend-suggested provider/model pair, or keep the current route
and choose another model from the picker. Do not silently widen a stale route
back to a hidden provider fallback.

The backend owns the readiness wording. `/hecate/v1/providers/status` returns a
provider-level `readiness` summary plus detailed `readiness_checks`, and
`/v1/models` adds `metadata.readiness` for every discovered provider/model row.
The UI should prefer those fields over local guesswork whenever they are
present; client-side inference is only a fallback for stale sessions or older
payloads.

The chat setup surface has one repair contract shared by the empty state, the
composer notice, and disabled-send copy. When a prompt cannot be sent, the UI
should pick one primary operator action: **Go to Connections**, **Choose
workspace**, **Enable tools**, **Use suggested model**, or **Open setup** for a
coding-agent integration. Avoid adding local one-off blockers in the Chat view; put
new send blockers behind the shared readiness resolver so the same reason and
CTA are visible before and after the transcript has messages.

Connections owns the provider repair workflow that backs those chat actions.
If the chat CTA sends an operator to Connections, the summary card should show
the same root cause and a concrete first action: add a provider, open the
blocked provider, or refresh provider/model discovery after the operator starts
a local runtime or fixes an upstream account.

Hecate Chat also has one per-chat **Instructions** field. With tools off, the
instructions are sent as the direct model turn's `system_prompt`. With tools
on, the same text becomes the per-task system prompt for the Hecate-owned
`agent_loop` task, layered under the global, tenant, and workspace
`AGENTS.md` / `CLAUDE.md` prompts. Once a chat has messages the field is locked
so historical segments keep the instructions they were created with; start a
new chat to change them. External Agent chats do not use this field because
Codex, Claude Code, Cursor Agent, and Grok Build own their own
prompt/configuration surface; external-agent model, reasoning, and mode
controls appear near the message composer when the agent exposes them.
Hecate-managed launch controls can appear before the first External Agent chat
session exists when a local agent requires startup choices; the message input
itself appears only after the chat session has been created.
External-agent context and reported cost are intentionally shown in the active
chat, not the Usage workspace, because those values are agent-reported and
only meaningful alongside the session that produced them.

Assistant turns may also expose a collapsed **context** inspector. This is a
metadata snapshot that answers "what kind of context did this turn use?" without
storing the prompt body. The packet records execution mode, provider/model when
Hecate owns routing, workspace path, whether a system prompt was included, the
visible transcript message count for that turn, the legacy high-level
`sources`, and itemized `items` with `kind`, `trust_level`, `origin`, `title`,
optional `body` / `body_ref`, `included`, and `inclusion_reason`. Current items
cover system prompt presence, prompt-policy notes, transcript count, enabled
project source metadata (workspace guidance, URLs, notes, local paths, or
external references), enabled project skill metadata, current active project
work metadata, accepted project memory, workspace path metadata, Hecate
task-runtime state, and external-agent session metadata. Hecate-owned project
chat packets mark bounded project prelude content as included, but mark project
context-source file bodies as visible-only because those bodies are not loaded
into chat prompts. External Agent packets mark project memory and source records
as visible-only and explicitly note that Hecate can show adapter metadata and
transcript rows it receives but cannot inspect or control the agent's private
prompt packing. Context packets deliberately do not persist full system prompts,
raw transcript text, file contents, source bodies, `SKILL.md` bodies, or
agent-private prompt packing. The message count is
an operator-facing transcript count, not a provider token count or a guarantee
that every counted message was packed into the provider or agent prompt. Context
packets are snapshots on assistant messages; changing project context sources,
skills, or work records later does not rewrite old message packets.
For project-linked Hecate Chat packets, the inspector also calls out the
bounded project prelude explicitly so operators can distinguish project
guidance from ordinary transcript/runtime metadata and see the metadata-only
root / skill-body boundary.

Long Hecate-owned model chats compact older transcript context before a direct
model turn once the compactable transcript reaches the automatic threshold.
`/compact` runs the same compaction path manually. When the session has a
routable provider/model, Hecate asks that model to produce a structured semantic
summary of the older transcript window and stores it as `context_summary` with
`strategy: "semantic_transcript_summary"`. If that model call is unavailable,
empty, or fails, Hecate falls back to the deterministic transcript summary:
one bounded line per compacted message, capped from the oldest side, with
`strategy: "deterministic_transcript_summary"`. Original chat messages remain
stored; future Hecate-owned model turns inject the summary as background and
send newer transcript messages in full.

External Agent sessions may expose ACP-advertised `available_commands` on the
session snapshot. These are agent-native slash command hints, not Hecate
project commands; selecting one still sends ordinary prompt text to the
external agent. The operator UI may show these hints while the composer starts
with `/`; choosing a hint only inserts the slash command text. Hecate-owned
chat commands are separate local shortcuts and must still route through
Hecate's proposal, validation, and operator-apply boundaries.
The composer picker labels command provenance as **External Agent** for
ACP-advertised external commands, **Project** for Hecate-owned project proposal
shortcuts, and **Hecate** for local navigation/runtime shortcuts.

### Hecate-owned chat commands

Hecate-owned commands are local UI shortcuts in Hecate Chat. They are not ACP
commands and they do not run when submitted to an External Agent chat. Project
commands draft Project Assistant proposals; navigation commands open Hecate UI
surfaces without sending a chat message.

Project-linked Hecate Chat also receives a bounded Hecate-owned prompt prelude
with project-root metadata, role hints, enabled skill metadata, active work,
and accepted project memory excerpts. Roots and skills are metadata only: the
chat path does not read root files or inject `SKILL.md` bodies. When Cairnline
is the configured read source, that prelude and its context-packet metadata are
assembled from the active embedded or sidecar Cairnline project graph rather
than requiring a native Hecate project row. External Agent sessions may share
the same project filter and sidebar scope, but their prompt packing remains
agent-owned and does not receive this Hecate prelude.

| Command               | Available when                          | Behavior                                                                    |
| --------------------- | --------------------------------------- | --------------------------------------------------------------------------- |
| `/proposal <request>` | Hecate Chat is linked to a project      | Drafts a Project Assistant proposal from the request.                       |
| `/plan <request>`     | Hecate Chat is linked to a project      | Drafts a Project Assistant plan proposal from the request.                  |
| `/work <request>`     | Hecate Chat is linked to a project      | Drafts a project-work proposal from the request.                            |
| `/handoff <request>`  | Hecate Chat is linked to a project      | Drafts a project handoff proposal from the request.                         |
| `/review <request>`   | Hecate Chat is linked to a project      | Drafts a project review proposal from the request.                          |
| `/diff`               | Hecate Chat has a workspace             | Opens the workspace changes panel.                                          |
| `/model`              | Hecate Chat is open                     | Opens chat settings focused on provider/model controls.                     |
| `/settings`           | Hecate Chat is open                     | Opens the chat settings panel.                                              |
| `/status`             | Hecate Chat is open                     | Opens the chat settings/status details panel.                               |
| `/task`               | Hecate Chat is open                     | Opens the active task when one exists; otherwise opens the Tasks workspace. |
| `/project`            | Hecate Chat is linked to a project      | Opens the linked project in Projects.                                       |
| `/connections`        | Hecate Chat is open with app navigation | Opens Connections for provider/model and External Agent setup.              |
| `/context`            | Hecate Chat is open                     | Opens the chat context/settings panel without sending a chat message.       |
| `/compact`            | Hecate Chat is open                     | Compacts older Hecate-owned transcript context without sending a message.   |

Submitting a project command without text after the command returns a composer
notice and does not draft a proposal. Unknown slash commands in Hecate Chat are
submitted as ordinary chat text, matching the normal composer path.

Hecate Chat settings also own the **Tools** toggle and the optional **Compact
command output** toggle. Tools decides whether future turns stay as direct
model calls or enter the Hecate task runtime. If tools are on but the selected
model is known not to support tool-calling, Hecate keeps the chat usable by
sending the turn as direct model chat and showing that state in the chat header.
Compact command output is
per-chat RTK support. It is off by default; if `rtk` is installed in the
gateway process `PATH`, Hecate suggests enabling it during new-chat onboarding.
When enabled, future shell/git tool calls in task-backed turns launch as
`rtk sh -lc <command>`. Hecate still performs its own approval, policy,
sandbox, timeout, and output-limit checks; RTK only changes the command output
shape the model sees. Task/run activity carries the resulting argv and
`hecate.sandbox.rtk.enabled` flag so debugging can confirm whether a command
actually used RTK. When compact output is enabled, telemetry also carries
`hecate.sandbox.rtk.command.before` and `hecate.sandbox.rtk.command.after` so
operators can compare the command Hecate validated with the argv that RTK
wrapped.

The operator UI's **Hecate** agent choice uses chat sessions under
`/hecate/v1/chat/sessions` for both tools-off direct model turns and tools-on
task-backed turns. Session ownership is stable (`agent_id="hecate"`), and
every Hecate-side message persists as `execution_mode="hecate_task"` —
the tools-on/off axis is recorded on each message's `tools_enabled` boolean
instead of split across two execution-mode values:

- **Model** segments (`tools_enabled=false`) call the gateway/router directly
  and store user/assistant messages without creating Tasks.
- **Task-backed Hecate Chat** segments map a tools-on stretch of a chat to one
  visible `agent_loop` task. The first tool-enabled prompt creates the task;
  follow-up prompts continue the latest terminal run when the previous segment
  was also task-backed. If tools are re-enabled after a direct model segment,
  Hecate creates a new task-backed segment in the same transcript.
  While a task-backed segment is queued, running, or awaiting approval, the
  whole Hecate Chat session is busy: direct model sends are blocked too, so one
  transcript cannot race a live task loop against a separate model turn. The
  composer shows the busy state with **Open task** and **Stop** actions so the
  operator can jump to the canonical Task view or cancel the active loop.
  If the operator submits another prompt while the active run is still busy,
  the UI keeps it in a local **Queued next** FIFO and submits it automatically
  after the run or approval reaches a terminal state. Queued prompts preserve
  the originating chat session plus the selected runtime/model/workspace
  snapshot from the moment they were queued, so switching to another chat cannot
  drain a prompt into the wrong transcript. They can be edited or removed while
  waiting. They are persisted in browser-local operator storage until submitted,
  removed, or pruned because the backing chat session was deleted.
  Chats projects the backing run activity into the transcript, links each
  assistant turn back to its backing Task/run, and can approve/reject pending
  task approvals inline. Low-level artifacts stay under transcript **Details**,
  while Tasks remains the canonical run/artifact view. On refresh, the UI
  rehydrates the active Hecate Chat from the persisted session/task snapshot so
  queued, running, and awaiting-approval states stay visible without sending a
  new prompt.
  If a task-backed MCP tool returns an MCP Apps resource, the transcript renders
  the captured app iframe directly in the assistant message body and keeps the
  compact tool activity row collapsed below it as audit metadata.
  If the project-linked agent loop drafts a Project Assistant proposal, the
  transcript shows a `Project Assistant proposal` activity with proposal
  title/action-count metadata and a **Review in Projects** action. The linked
  artifact is the handoff; Chats does not create a second Project Assistant chat
  thread.
  Deleting a Hecate Chat cancels any non-terminal backing task run before the
  transcript is removed; the backing Task record remains in Tasks for audit and
  artifact history.
  When the backing provider supports streaming, the running assistant message
  updates from the task conversation artifact before the task run completes.

  API responses include derived `turn_kind` on messages and transcript
  segments. Clients should prefer `turn_kind` (`direct_model`, `hecate_task`,
  or `external_agent`) for UI routing and keep `execution_mode` /
  `tools_enabled` as durable compatibility fields.

- **External Agent** sessions map one chat session to one supervised ACP
  session such as Codex, Claude Code, Cursor Agent, or Grok Build. Composer
  controls may be ACP-owned session options or Hecate-managed launch options;
  they stay separate from Hecate provider/model routing.

External Agent sessions persist Hecate's operator-facing shell plus the
agent-owned native session handle. Listing chat sessions does not start or
reattach agents. Opening a single External Agent session, or subscribing to
its stream, attempts to load the stored ACP session handle so Hecate can refresh
agent controls before the next prompt. If the agent cannot restore that
native session, the transcript still opens from Hecate's store; the next send or
agent setup action can start a fresh native session and keep the shell intact.
Opening a chat never silently replaces the stored native session handle with a
fresh agent session.

The chat session API shape used by the operator UI is in
[`runtime-api.md`](runtime-api.md#get-hecatev1chatsessions), and external-agent
behavior is in [External Agents](external-agents.md).

## Activity rendering

Hecate uses one compact activity vocabulary across Hecate Chat transcripts and
Task Detail. This is deliberate: an operator should see the same story whether
they stay in Chats or open the canonical Task/run view.

Chat titles are operator metadata and can be renamed from the Chats sidebar.
Renaming works the same way for Hecate Chat, direct model turns, and External
Agent sessions: it only changes the visible session title, not the transcript,
workspace, runtime segment, provider/model snapshot, or agent-owned native
session.

The shared renderer keeps the high-signal path visible:

- model turns / thinking
- tool calls
- approval requested / approved / rejected / cancelled
- workspace changes
- final answer
- terminal run state

Lower-level task artifacts, raw output markers, and internal bookkeeping are
grouped under **Details**. Chats keeps those details collapsed by default so the
conversation stays readable; Task Detail opens the activity section by default
because that view is already a run-inspection surface. Task Detail can also
show a per-row **Advanced** disclosure with raw activity metadata such as
step/artifact/approval ids, tool kind, path, timestamp, and summary payload.
Repetitive command rows are collapsed into a single **Ran N commands** group;
expanding that group shows the commands and any captured output in one layer so
operators do not have to click through nested output disclosures. Command and
read-context rows keep raw output out of the compact row and show normalized
line breaks inside the output card.
Workspace changes have one primary surface: the per-turn file badge and the
workspace changes panel. The panel has two jobs: **Review** shows the current
changed-file list with collapsible rich diffs under each file, while **Files**
shows the full workspace tree separately and keeps folders collapsed until the
operator expands or searches. Transcript activity may mention that workspace
changes exist, but it should not duplicate raw patches or render a second diff
viewer when the richer workspace diff surface is available.
For failed tool rows, Task Detail also previews stdout/stderr artifacts captured
for the same tool step, including an explicit empty-stream note when stderr was
captured but contained no bytes. Artifacts from other steps are intentionally
not linked into that row, so a failed command cannot appear to have output from
an unrelated tool call.
When a tool row fails, Chats may show its own **Advanced** disclosure with
capped previews of the backing Task's non-empty stdout/stderr artifacts plus an
**Open task output** escape hatch for the full capture. Empty streams stay
hidden there; open the Task view when you need to confirm whether stderr was
captured but empty.

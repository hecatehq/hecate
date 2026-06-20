# External Agents

Hecate can supervise external coding-agent CLIs from **Chats**. Today that means
Codex, Claude Code, Cursor Agent, and Grok Build through ACP sessions launched
next to the Hecate runtime.

External agents are not Hecate model providers and they are not inside the
`hecate` process. Hecate starts the agent bridge as the operator's OS user, sends
prompts over ACP, records transcript/diagnostics, handles approvals, and shows
Git diffs. The model gateway path is not involved; `/v1` provider routing and
Hecate model credentials stay separate.

With `HECATE_BACKEND=sqlite` or `postgres`, Hecate keeps the transcript and the
agent's native ACP session id. After restart, the next prompt asks the agent to
`session/load` that native session when supported. If the adapter cannot load it,
Hecate starts a fresh native session and keeps the Hecate transcript.

The bundled Go Codex and Claude Code adapters use command-backed sessions today.
Both support in-memory `session/load` / `session/resume` / `session/fork` while
the adapter process is alive, and later prompt commands receive a bounded
transcript prelude for multi-turn continuity. Claude Code adapter
`v0.1.0-alpha.12` also uses Claude-native UUID session ids with
`claude --session-id`, so Hecate can reload a stored Claude native session id
after an adapter process restart. Codex does not yet claim vendor-native
durable history across adapter process restarts; if a load is stale, Hecate
falls back to a fresh native session. Hecate treats `codex-acp-adapter` versions
older than `v0.1.0-alpha.17` and `claude-code-acp-adapter` versions older than
`v0.1.0-alpha.19` as outside the tested range because those older releases lack
the current continuity, permission-control, structured stream, session metadata,
external MCP handoff surface, and ACP authenticate/logout mapping. The
`v0.1.0-alpha.8` adapters added supported Codex and Claude Code JSON stream
translation into ACP assistant-message, thought, tool-call, tool-result, and
usage updates, so Hecate can render External Agent activity without exposing raw
JSONL output in the chat transcript. The `v0.1.0-alpha.9` adapters added
config-option update notifications and richer command-backed `session/list`
metadata. The `v0.1.0-alpha.10` adapters also publish ACP
`session_info_update` notifications with command-backed session title and
updated-time metadata. Codex adapter `v0.1.0-alpha.11` adds the advertised
`/review` command backed by `codex review --uncommitted`, and
`v0.1.0-alpha.12` adds an ACP config option that maps normal Codex turns to
`codex exec --search` when enabled, and `v0.1.0-alpha.13` propagates
session-level stdio/HTTP MCP server config into Codex CLI config overrides.
Codex adapter `v0.1.0-alpha.14` also advertises `/init` through the normal
`codex exec` prompt path so operators can ask Codex to inspect the workspace and
create or update repository agent instructions from Hecate.
Codex adapter `v0.1.0-alpha.15` classifies more provider-native tool updates as
ACP tool kinds, including file, web/fetch, MCP, image, plan, TODO, goal, and
review updates, so the External Agent transcript can show those activities more
clearly.
Codex adapter `v0.1.0-alpha.16` maps ACP `logout` to the native `codex logout`
command.
Codex adapter `v0.1.0-alpha.17` maps ACP `authenticate` to the native
`codex login` command.
Claude Code adapter `v0.1.0-alpha.11` adds command-backed stdio/HTTP MCP server
config propagation into Claude `--mcp-config`, and `v0.1.0-alpha.12` adds
Claude-native `--session-id` reload after adapter restarts. Claude Code adapter
`v0.1.0-alpha.13` advertises `/init` through the normal `claude --print` prompt
path so operators can ask Claude Code to inspect the workspace and create or
update `CLAUDE.md` from Hecate. Claude Code adapter `v0.1.0-alpha.14` also
advertises `/review`, `/code-review`, and `/security-review` as normal prompt
commands backed by Claude Code's native slash-command handling.
Claude Code adapter `v0.1.0-alpha.15` exposes Claude Code's
`bypassPermissions` permission mode as an ACP session config option for
operators who intentionally want the full-access Claude Code boundary.
Claude Code adapter `v0.1.0-alpha.16` classifies more provider-native tool
updates as ACP tool kinds, including web/fetch, task, and memory updates, so the
External Agent transcript can show those activities more clearly.
Claude Code adapter `v0.1.0-alpha.17` advertises additional prompt-backed
commands (`/compact`, `/debug`, `/run`, and `/verify`) so Hecate can show them
in the External Agent command picker.
Claude Code adapter `v0.1.0-alpha.18` maps ACP `logout` to the native
`claude auth logout` command.
Claude Code adapter `v0.1.0-alpha.19` maps ACP `authenticate` to the native
`claude /login` command.

## Supported External Agents

| External agent | How Hecate starts it      | Local auth mode                                            | Remote-safe auth mode                            |
| -------------- | ------------------------- | ---------------------------------------------------------- | ------------------------------------------------ |
| Codex          | `codex-acp-adapter`       | Operator-owned Codex CLI auth visible to the adapter       | `OPENAI_API_KEY` or `CODEX_API_KEY`              |
| Claude Code    | `claude-code-acp-adapter` | Operator-owned Claude Code login visible to Claude Code    | `ANTHROPIC_API_KEY`                              |
| Cursor Agent   | `cursor-agent acp`        | Operator-owned Cursor Agent auth visible to `cursor-agent` | `CURSOR_API_KEY`                                 |
| Grok Build     | `grok agent ... stdio`    | Operator-owned Grok login visible to `grok`                | `XAI_API_KEY` or Hecate's `PROVIDER_XAI_API_KEY` |

The Docker runtime image includes the supported agent CLIs and ACP adapters so
local/self-host Docker deployments can use External Agents without installing
those binaries into the container at runtime. Bare binary and desktop
deployments use whatever agent CLIs and Go ACP adapter binaries are installed on
the operator's machine.

## Credential and account boundaries

The selected external agent owns its model/runtime/account relationship:

- Hecate does not call Claude Code SDK/API, Codex SDK/API, Cursor Agent APIs,
  or Grok/xAI APIs directly for External Agent sessions.
- Hecate does not proxy, pool, resell, or bypass external-agent credentials,
  accounts, credits, or vendor policy.
- Hecate does not store External Agent credentials. The local agent process reads the
  operator's local CLI login files and environment.
- Local CLI login files remain owned by the upstream CLI. Do not copy them
  between users or machines.
- Remote runtimes use vendor-supported API-key style credentials by default.
  Runtime-local browser login state is only considered when explicitly enabled
  for a one-person personal remote runtime.
- External Agent vendors may restrict or forbid shared/account-delegated use of
  browser or CLI login state. Hecate does not interpret, bypass, or relax those
  terms; check the vendor-supported auth mode for the way you deploy.

When `HECATE_REMOTE_RUNTIME_MODE=1`, External Agent launches fail closed unless
the selected adapter has a declared remote-safe credential mode and the matching
environment variable is present. The bundled CLIs do not make personal
browser login state sufficient for remote mode by default. Runtime-local CLI
login state is ignored for this decision unless the personal remote opt-in below
is enabled. The runtime accepts API-key style
credentials for Codex (`OPENAI_API_KEY` / `CODEX_API_KEY`), Claude Code
(`ANTHROPIC_API_KEY`), Cursor (`CURSOR_API_KEY`), and Grok Build (`XAI_API_KEY`,
or `PROVIDER_XAI_API_KEY` bridged to `XAI_API_KEY` only for Grok). Auth-token env
vars that represent local CLI login state, such as `CODEX_AUTH_TOKEN` or
`ANTHROPIC_AUTH_TOKEN`, are local-only for this policy. Remote-mode adapter
processes also get an ephemeral `HOME` / XDG config directory instead of the
runtime process home.

Single-user personal remote runtimes can deliberately opt into runtime-local
External Agent login state with:

```bash
HECATE_PERSONAL_REMOTE_EXTERNAL_AGENT_LOGINS=1
```

Use it only when one human owns the runtime boundary, the runtime home/XDG
directories are on that runtime's persistent volume, and logins are created
inside the runtime, for example from Hecate Terminal or SSH. Keep it unset
unless that ownership boundary is true. Use API keys, team/project credentials,
enterprise tokens, or future vendor auth flows for anything beyond personal
remote use. The `hecate_remote` build tag still strips local-login credential
modes entirely.

This is the same practical boundary used by ACP-capable editors such as
[Zed](https://zed.dev/docs/ai/external-agents): the client supervises a local
agent bridge, while authentication and billing stay with the provider.

## Quick start from the operator UI

1. Start Hecate and open **Chats**.
2. Pick **Codex**, **Claude Code**, **Cursor Agent**, or **Grok Build** from the
   agent picker.
3. Choose the workspace directory the external agent is allowed to work in.
4. If model, reasoning, or mode controls appear above the message composer,
   choose the values you want. Some controls are launch-time choices and some
   appear after the ACP session is prepared.
5. Click **New chat**. Hecate starts the agent session immediately. The
   message input appears after that session exists, while launch controls can be
   shown earlier so required values can be selected first.
6. If the agent row is amber/red, open **Connections**. The probe performs
   a real spawn + ACP handshake + temporary
   no-op session, so it catches missing auth, billing/subscription issues,
   unsupported versions, and missing or unsupported binaries before a prompt
   fails.
7. Send a small smoke prompt:

   ```text
   Show me git status and summarize what changed.
   ```

External Agent chats do not use Hecate model providers or Hecate's task
sandbox. The selected external agent owns its model/runtime/account relationship;
Hecate supervises the local process, approvals, transcript, diagnostics, traces,
guardrails, and Git diff review.

External Agent controls have two sources:

- **Launch controls** come from Hecate's agent catalog and can appear before a
  concrete chat session exists. Hecate passes the selected values as process
  arguments when it starts or restarts the external agent.
- **ACP session controls** come from the agent during `session/new`,
  `session/load`, or `session/set_config_option`. Hecate surfaces model,
  reasoning, mode, and similar selectors in the composer after the External
  Agent chat exists. If an agent reports ACP model state from `session/new` or
  `session/load`, Hecate surfaces that state as the model control and applies
  model changes with ACP `session/set_model`. Chat settings are reserved for
  session details and agent-provided text/instruction settings.

The controls are agent-defined: Codex / Claude Code / Cursor Agent / Grok
Build decide which model, mode, or reasoning selectors exist and what the labels
mean. The selected external agent is fixed for that chat; start another chat to use a
different agent. Hecate stores the latest reported control state with the chat
session and sends ACP-owned changes back with ACP `session/set_config_option`;
it does not translate those controls into Hecate provider/model settings.

External Agent chat sessions can also carry session-level `mcp_servers`
configuration. Hecate persists the configured stdio/HTTP server list with the
chat session and passes it to the adapter during ACP `session/new` and
`session/load`. That is a transport handoff, not Hecate-owned MCP dispatch:
the selected external agent decides whether and how those MCP servers are
exposed inside its own runtime. Hecate-owned Chat tool turns use per-message
`mcp_servers` instead, because each tool-backed turn creates or continues an
explicit task segment with its own recorded server set.

In the operator UI, expand **MCP servers** in the new External Agent composer
controls before the session is created. Session-level MCP config is fixed at
ACP session start; existing chats show the configured servers in Chat settings.

ACP agents may also advertise **available slash commands** with
`available_commands_update`. Hecate stores the latest advertised command
metadata on the chat session as `available_commands` so clients can render
agent-native command hints. Commands are still submitted as normal
`session/prompt` text such as `/web agent client protocol`; ACP does not define
a separate execute-command RPC. The operator UI can surface the advertised list
when the composer starts with `/`, but choosing an item only inserts the command
text. These are external-agent-native commands, not Hecate-owned project
mutations. Hecate Chat shortcuts such as `/proposal` are separate local UI
commands, not ACP commands. Project-shaping shortcuts such as `/plan`, `/work`,
`/handoff`, and `/review` should remain intent hints that go through the usual
proposal, validation, and operator-apply boundaries instead of directly
mutating project records.
The composer picker labels ACP-advertised commands as **External Agent**.
Hecate-owned local shortcuts use **Project** for proposal-oriented project
commands and **Hecate** for local navigation/runtime commands.

Check discovery:

```sh
curl -s http://127.0.0.1:8765/hecate/v1/agent-adapters | jq
```

Discovery reports command availability, tested version range, and lightweight
auth hints (`auth_status`: `ok`, `unauthenticated`, `billing`, or `unknown`).
When an ACP adapter binary is separate from the coding-agent CLI, Hecate reports
both versions: `adapter_version` for the bridge and `agent_version` for the
underlying agent (`codex`, `claude`, `cursor-agent`, `grok`).

Manual setup stays in the upstream CLIs:

| External agent | Sign-in command      |
| -------------- | -------------------- |
| Codex          | `codex login`        |
| Claude Code    | `claude /login`      |
| Cursor Agent   | `cursor-agent login` |
| Grok Build     | `grok login`         |

If an agent readiness check reports an auth failure, run the matching command
in Terminal, then return to Connections and test the agent again.
If it reports a timeout or `context deadline exceeded`, the adapter did not
finish startup or ACP session creation inside the probe window. Hecate did not
send a prompt; close any stuck browser or login prompt, fix the CLI if needed,
then retry from Connections.

Connections refreshes agent readiness when opened. You can also
call the probe endpoint for a full spawn + ACP handshake + no-op session check:

![Connections — external agent readiness checks and durable approval grants](../screenshots/connections-external-agents.png)

```sh
curl -X POST http://127.0.0.1:8765/hecate/v1/agent-adapters/codex/probe | jq
```

Codex and Claude Code use standalone Go ACP adapter binaries backed by the
operator's local vendor CLI. Cursor and Grok ship ACP mode inside the vendor CLI
itself. The selected adapter command and the underlying vendor CLI must be
installed and visible on `PATH`. Hecate does not pin an external-agent model by
default. When an ACP agent reports model state, the agent-provided model list and
current model become the chat model control.

## Setup checks

External Agent chat does not use Hecate model providers. It needs the selected
coding-agent to be authenticated, and the direct ACP command to be visible to
Hecate.

Use this order when troubleshooting:

1. **Discovery** — `GET /hecate/v1/agent-adapters` tells you whether Hecate can
   find the adapter command and whether the installed version is inside
   Hecate's tested range.
2. **Probe** — `POST /hecate/v1/agent-adapters/{id}/probe` actually starts the
   agent bridge and opens a temporary ACP session. This is the best "will it run?"
   check.
3. **Chat run** — send a real prompt only after discovery/probe are green. If
   the agent still fails, open the message's raw diagnostics disclosure; the
   normalized transcript is for reading, raw ACP output is for debugging.

### Codex ACP

```sh
command -v codex-acp-adapter
command -v codex
curl -s http://127.0.0.1:8765/hecate/v1/agent-adapters | jq '.data[] | select(.id=="codex")'
```

If `available` is true, Hecate can start the Go Codex ACP adapter. The adapter
uses the local `codex` CLI for command-backed prompt execution, so both
`codex-acp-adapter` and `codex` should be visible to the Hecate process.

### Claude ACP

```sh
command -v claude-code-acp-adapter
command -v claude
curl -s http://127.0.0.1:8765/hecate/v1/agent-adapters | jq '.data[] | select(.id=="claude_code")'
```

If `available` is true, Hecate can start the Go Claude Code ACP adapter. The
adapter uses the local `claude` CLI for command-backed prompt execution, so both
`claude-code-acp-adapter` and `claude` should be visible to the Hecate process.

### Direct CLI adapters

```sh
command -v cursor-agent
cursor-agent acp --help
cursor-agent login
command -v grok
grok login
```

Headless and hosted environments can authenticate through vendor-supported API keys:

```sh
export OPENAI_API_KEY=...
export ANTHROPIC_API_KEY=...
export CURSOR_API_KEY=...
export XAI_API_KEY=...
```

In local mode, Hecate passes only the matching credential family to each adapter
process: Codex receives `CODEX_` / `OPENAI_`, Claude Code receives `CLAUDE_` /
`ANTHROPIC_`, Cursor Agent receives `CURSOR_`, and Grok Build receives `XAI_`.
Provider or gateway-scoped secrets are not shared across adapters. In remote
runtime mode this narrows further to the declared remote-safe env keys plus
runtime essentials such as `PATH`, locale, temp, and certificate variables.
The Hecate-managed ACP `authenticate` action is local-only; hosted runtimes use
these declared env-key credential modes instead of starting an interactive CLI
login.

If discovery cannot find a direct CLI adapter, install the vendor CLI and
restart Hecate from an environment where the command is on `PATH`. If a run
fails with an authentication-required message, authenticate the CLI in the same
environment that starts Hecate.

## Manual smoke

1. Start Hecate:

   ```sh
   just dev
   ```

2. Open **Chats** and choose Codex, Claude Code, Cursor Agent, or Grok Build
   from the agent picker.

3. Choose an available external agent.

4. Choose a workspace directory from the folder button. If the native folder
   dialog is not available, Hecate falls back to a manual path entry. Hecate
   stores the canonical path and shows the full path plus Git branch in the
   shell status bar.

5. Send a prompt, for example:

   ```text
   Show me git status and summarize what changed.
   ```

6. Confirm the assistant message shows:
   - structured activity markers such as starting, running, files changed, or failed
   - normalized transcript text
   - per-block thinking entries when the agent streams ACP
     `agent_thought_chunk` updates (chunks sharing a `messageId` collapse into
     one row; a new `messageId` starts a fresh row)
   - per-file `file_change` rows for each completed mutating tool call (kind =
     edit / delete / move) in addition to the end-of-turn diff-stat aggregate
   - context usage in the shell status bar when the agent reports it
   - a file-count badge when the turn reports workspace changes
   - the header workspace-changes button opens the current workspace panel:
     **Review** shows changed files with collapsible rich diffs, while **Files**
     shows the full workspace tree separately
   - raw ACP diagnostics under the inline diagnostic disclosure when they differ from the normalized transcript

## Runtime behavior

Each External Agent chat session maps to one native ACP session. Hecate starts or
restores the selected agent process when the chat is created, creates the ACP
session before the first prompt, and reuses it for later prompts in the same
External Agent chat session. The external agent is fixed for the chat session;
start a new External Agent chat to choose another agent.
The visible chat title can be renamed from the Chats sidebar without changing
the ACP native session, workspace, or agent selection.
Hecate-owned chats are different: their provider/model selection can change
between direct model turns and new task-backed segments in one transcript.

Project assignments can also prepare External Agent sessions. Starting a
`driver_kind="external_agent"` assignment from Projects creates and prepares the
linked External Agent chat session, records assignment/profile/workspace context,
and stores the session link on the assignment. It does not append a visible chat
message, create a `message_id`, or send the assignment prompt automatically; the
operator stays in control of the first turn from the linked chat. Project
assignment and activity rows project the linked chat's latest assistant-message
status, session status, adapter identity, and missing-session diagnostics so the
Projects cockpit can show follow-through state without embedding the full chat
transcript. When the External Agent turn settles, Hecate also best-effort
reconciles the linked assignment row to the chat outcome, including the
assistant `message_id` and terminal status. Handoffs created from these
assignments can carry the source assignment, chat session, message, run, and
context refs for provenance. If the work item declares reviewer roles, the
Projects cockpit can prefill a review handoff from the External Agent
assignment, but accepting the handoff, creating the follow-up assignment, and
starting that assignment remain operator-controlled.

Hecate validates the workspace before creating a session, sanitizes the
environment passed to the ACP agent process, applies timeout/cancel behavior,
captures ACP updates with an output cap, and records Git diff / diff stat after
each turn. External agents are still trusted subprocesses in the
selected workspace; this is not equivalent to the task runtime sandbox.
Read [Security](../operator/security.md) for the full runtime-boundary and workspace-safety
model.

On Hecate shutdown, active External Agent turns are cancelled first. Hecate waits
briefly for the ACP turn to drain, asks the native ACP session to close, and
then kills the owned agent process group if it is still alive. This keeps app
quit / restart from leaving Codex, Claude Code, Cursor Agent, or Grok Build
processes behind.
Operators can also close an External Agent chat session manually to release the external
agent process while keeping the Hecate chat history. Deleting a chat performs
the same release step and then removes the persisted history.

Every prompt also gets OTel-shaped observability. The message response includes
`request_id`, `trace_id`, and `span_id`, and `GET
/hecate/v1/traces?request_id=<request_id>` shows the `chat.run` span with adapter
identity, workspace, status, duration, output byte counts, and diff-capture
state. Approval gating adds two more spans:
`agent_adapter.approval.request` covers the coordinator's decision (grant
short-circuit, mode default, or prompt-mode wait) and carries
`hecate.agent_adapter.approval.path` once the path is known;
`agent_adapter.approval.resolve` wraps the operator's decision-application
path with `decision` and `scope` attributes.

Durable approval grants are part of the chat-session storage bundle. When
`HECATE_BACKEND=sqlite` or `postgres`, grants survive Hecate server restarts and
are listed from `GET /hecate/v1/chat/grants`; the operator can revoke them from
Connections. Pending approvals from a dead process are not
replayed as actionable prompts — startup reconcile marks them `timed_out` with
`path=startup_reconcile` before Hecate accepts traffic.

![Chats workspace with an external-agent file-write approval waiting for operator review](../screenshots/chat-agent-approval.png)

![Agent approval modal with ACP options, scope choices, and audit note](../screenshots/chat-agent-approval-modal.png)

## Approval mode

`HECATE_AGENT_ADAPTER_APPROVAL_MODE` controls how Hecate responds to ACP
`RequestPermission` from external agents. Three values:

- `prompt` (default) — Hecate records a pending row and waits for an
  operator decision via the Chats workspace banner / modal or the
  `/hecate/v1/chat/sessions/{id}/approvals` REST surface. Without an operator
  reviewing within `HECATE_AGENT_ADAPTER_APPROVAL_TIMEOUT` (default 5m), the
  approval times out and the adapter receives ACP `Cancelled`.
- `auto` — every agent request is permitted without review. Surfaces a red
  danger banner across every Chats session because every agent call runs
  unsupervised. Useful only for headless / CI usage where no operator is
  available; never the right setting for interactive use.
- `deny` — every agent request is refused.

## Runtime guardrails

### Per-session turn ceiling

`HECATE_CHAT_MAX_TURNS_PER_SESSION` caps the number of user→assistant
round-trips per chat session. When a session reaches the ceiling,
`POST /hecate/v1/chat/sessions/{id}/messages` returns HTTP 422:

```json
{
  "error": {
    "type": "chat.session_limit_exceeded",
    "message": "session has reached the 50-turn limit; start a new session to continue",
    "limit": 50,
    "turns_used": 50
  }
}
```

| Setting                                | Behavior                            |
| -------------------------------------- | ----------------------------------- |
| `HECATE_CHAT_MAX_TURNS_PER_SESSION=0`  | Unlimited (default)                 |
| `HECATE_CHAT_MAX_TURNS_PER_SESSION=50` | Enforce 50-turn ceiling per session |

When a limit is set, the chat header shows a `{turns_used}/{max} turns` badge.
The badge turns amber when the ceiling is reached.

Turns are counted per session, not per workspace — starting a new session in
the same workspace resets the counter.

### Wall-clock and idle limits

Two optional time-based limits protect long-lived ACP sessions from turning
into invisible background processes:

| Setting                               | Behavior                                                  |
| ------------------------------------- | --------------------------------------------------------- |
| `HECATE_CHAT_MAX_SESSION_DURATION=0s` | Unlimited wall-clock age (default)                        |
| `HECATE_CHAT_MAX_SESSION_DURATION=2h` | Reject new turns once the session is at least 2 hours old |
| `HECATE_CHAT_IDLE_TIMEOUT=0s`         | No idle sweeper (default)                                 |
| `HECATE_CHAT_IDLE_TIMEOUT=1h`         | Auto-close idle sessions after 1 hour without updates     |

When the wall-clock limit is exceeded, `POST
/hecate/v1/chat/sessions/{id}/messages` returns HTTP 422 with
`chat.session_duration_limit_exceeded`.

When the idle limit is exceeded, the background sweeper cancels the session,
clears the native ACP handle, and appends an `interrupted` activity to the last
assistant message when one exists. If the operator sends a new prompt before
the sweeper has closed the stale session, the request returns HTTP 422 with
`chat.session_idle_timeout`; start a new chat to continue.

## Stable alpha scope

External agents are stable enough for alpha use when the operator
accepts the trusted-subprocess model: Codex, Claude Code, Cursor Agent, and Grok Build run as
their own runtimes in the selected workspace while Hecate supervises lifecycle,
approvals, output capture, diagnostics, observability, guardrails, captured
turn diffs, current workspace diff review, and full workspace file browsing.

## Future enhancements

- Patch review is intentionally "already applied" for now. Hecate captures
  per-turn diff data in the transcript and exposes a separate current-workspace
  Git diff panel that can inspect or discard selected tracked paths from the
  live working tree. A fuller review surface with side-by-side hunks, richer
  batch selection, untracked-file handling, and artifact history is still
  future work.
- Adapter-specific ACP mappers can make Codex, Claude Code, Cursor Agent, and Grok Build progress
  updates prettier over time. The current generic mapper plus raw diagnostics is
  sufficient for alpha stability.
- External Agent chat is a lightweight API around opaque external runtimes.
  Future work may reuse more task-runtime primitives for artifacts, event
  history, retention, and trace correlation, but Hecate should not pretend it
  owns the Codex / Claude Code / Cursor Agent / Grok Build runtime loop.

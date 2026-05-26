# External agent adapters

Hecate can supervise external coding-agent CLIs from **Chats**. Today that means
Codex, Claude Code, Cursor Agent, and Grok Build through local ACP adapters.

External agents are not Hecate model providers and they are not inside the
`hecate` process. Hecate starts the adapter as the operator's OS user, sends
prompts over ACP, records transcript/diagnostics, handles approvals, and shows
Git diffs. The model gateway path is not involved; `/v1` provider routing and
Hecate model credentials stay separate.

With `HECATE_BACKEND=sqlite`, Hecate keeps the transcript and the
adapter's native ACP session id. After restart, the next prompt asks the adapter
to `session/load` that native session when supported. If the adapter cannot load
it, Hecate starts a fresh native session and keeps the Hecate transcript.

## Supported adapters

| Adapter      | How Hecate starts it                                                                                                      | Auth expected by the underlying agent                              |
| ------------ | ------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| Codex        | Hecate-managed launcher for `@zed-industries/codex-acp` via local `npx`; direct `codex-acp` also works                    | Operator-owned Codex auth visible to the adapter                   |
| Claude Code  | Hecate-managed launcher for `@agentclientprotocol/claude-agent-acp` via local `npx`; direct `claude-agent-acp` also works | Operator-owned Claude Code / Anthropic auth visible to Claude Code |
| Cursor Agent | `cursor-agent acp`                                                                                                        | Operator-owned Cursor Agent auth visible to `cursor-agent`         |
| Grok Build   | `grok agent ... stdio`                                                                                                    | Operator-owned Grok auth or `XAI_API_KEY` visible to `grok`        |

## Credential and account boundaries

The selected external agent owns its model/runtime/account relationship:

- Hecate does not call Claude Code SDK/API, Codex SDK/API, Cursor Agent APIs,
  or Grok/xAI APIs directly for External Agent sessions.
- Hecate does not proxy, pool, resell, or bypass external-agent credentials,
  subscriptions, credits, or vendor policy.
- Hecate does not store External Agent credentials. The adapter reads the
  operator's local CLI login files and environment.
- Local CLI login files remain owned by the upstream CLI. Do not copy them
  between users or machines.
- Hosted or multi-user Hecate deployments must not collect or share personal
  subscription tokens. Use vendor-supported team/project/API-key controls for
  shared automation.

This is the same practical boundary used by ACP-capable editors such as
[Zed](https://zed.dev/docs/ai/external-agents): the client supervises a local
adapter process, while authentication and billing stay with the provider.

## Quick start from the operator UI

1. Start Hecate and open **Chats**.
2. Pick **Codex**, **Claude Code**, **Cursor Agent**, or **Grok Build** from the
   agent picker.
3. Choose the workspace directory the external agent is allowed to work in.
4. If model, reasoning, or mode controls appear above the message composer,
   choose the values you want. Some controls are launch-time choices and some
   appear after the ACP session is prepared.
5. Click **New chat**. Hecate starts the adapter session immediately. The
   message input appears after that session exists, while launch controls can be
   shown earlier so required values can be selected first.
6. If the adapter row is amber/red, open **Connections**. The probe performs
   a real spawn + ACP handshake + temporary
   no-op session, so it catches missing auth, billing/subscription issues,
   unsupported versions, and broken managed launchers before a prompt fails.
7. Send a small smoke prompt:

   ```text
   Show me git status and summarize what changed.
   ```

External Agent chats do not use Hecate model providers or Hecate's task
sandbox. The selected adapter owns its model/runtime/account relationship;
Hecate supervises the local process, approvals, transcript, diagnostics, traces,
guardrails, and Git diff review.

External Agent controls have two sources:

- **Launch controls** come from Hecate's adapter catalog and can appear before a
  concrete chat session exists. Hecate passes the selected values as process
  arguments when it starts or restarts the adapter.
- **ACP session controls** come from the adapter during `session/new`,
  `session/load`, or `session/set_config_option`. Hecate surfaces them in the
  composer and chat settings after the External Agent chat exists. If an
  adapter reports ACP model state from `session/new` or `session/load`, Hecate
  surfaces that state as the model control and applies model changes with ACP
  `session/set_model`.

The controls are adapter-defined: Codex / Claude Code / Cursor Agent / Grok
Build decide which model, mode, or reasoning selectors exist and what the labels
mean. The selected adapter is fixed for that chat; start another chat to use a
different agent. Hecate stores the latest reported control state with the chat
session and sends ACP-owned changes back with ACP `session/set_config_option`;
it does not translate those controls into Hecate provider/model settings.

Check discovery:

```sh
curl -s http://127.0.0.1:8765/hecate/v1/agent-adapters | jq
```

Discovery reports command availability, tested version range, and lightweight
auth hints (`auth_status`: `ok`, `unauthenticated`, `billing`, or `unknown`).
When an ACP bridge or launcher is separate from the coding-agent CLI, Hecate
reports both versions: `adapter_version` for the bridge and `agent_version` for
the underlying agent (`codex`, `claude`, `cursor-agent`, `grok`). Managed package
adapters avoid package-manager execution during passive discovery; their
`adapter_version` is populated only after an explicit adapter test.

Manual setup stays in the upstream CLIs:

| Adapter      | Sign-in command      |
| ------------ | -------------------- |
| Codex        | `codex login`        |
| Claude Code  | `claude /login`      |
| Cursor Agent | `cursor-agent login` |
| Grok Build   | `grok login`         |

If an adapter readiness check reports an auth failure, run the matching command
in Terminal, then return to Connections and test the adapter again.
If it reports a timeout or `context deadline exceeded`, the adapter did not
finish startup or ACP session creation inside the probe window. Hecate did not
send a prompt; close any stuck browser or login prompt, fix the CLI if needed,
then retry from Connections.

Connections refreshes adapter readiness when opened. You can also
call the probe endpoint for a full spawn + ACP handshake + no-op session check:

![Connections — adapter readiness checks and durable approval grants](screenshots/connections-external-agents.png)

```sh
curl -X POST http://127.0.0.1:8765/hecate/v1/agent-adapters/codex/probe | jq
```

Some adapters run through Hecate-managed launchers when the direct ACP command
is missing and `npx` is available; Hecate creates those launchers in the
operator cache directory and runs the official ACP npm package from there.
Other adapters ship ACP mode inside the vendor CLI, so that CLI must be
installed and visible on `PATH`. Hecate does not pin an external-agent model by
default. When an ACP adapter reports model state, the adapter-provided model
list and current model become the chat model control.

By default the managed launcher directory is the user cache location:

```text
<user-cache>/hecate/agent-adapters
```

Set `HECATE_AGENT_ADAPTERS_DIR` only if you want to override that location for
development, Docker volume mapping, or a packaged desktop build. Hecate removes
stale managed-launcher scripts at startup when their adapter no longer exists.
To force-refresh one managed launcher after changing Node/npm managers:

```sh
curl -X POST http://127.0.0.1:8765/hecate/v1/agent-adapters/codex/refresh-launcher | jq
```

## Setup checks

External Agent chat does not use Hecate model providers. It needs the selected
coding-agent to be authenticated, and either a direct ACP command or a managed
package runner to be visible to Hecate.

Use this order when troubleshooting:

1. **Discovery** — `GET /hecate/v1/agent-adapters` tells you whether Hecate can
   find a direct command or managed launcher and whether the installed version
   is inside Hecate's tested range.
2. **Probe** — `POST /hecate/v1/agent-adapters/{id}/probe` actually starts the
   adapter and opens a temporary ACP session. This is the best "will it run?"
   check.
3. **Chat run** — send a real prompt only after discovery/probe are green. If
   the adapter still fails, open the message's raw diagnostics disclosure; the
   normalized transcript is for reading, raw ACP output is for debugging.

### Codex ACP

```sh
command -v npx
curl -s http://127.0.0.1:8765/hecate/v1/agent-adapters | jq '.data[] | select(.id=="codex")'
```

If `available` is true, Hecate can start Codex ACP. The first run may fetch and
cache the official `@zed-industries/codex-acp` package through `npx`.

If Hecate reports that the managed launcher is unavailable, install Node/npm or
start Hecate from an environment where `npx` is available. Hecate also checks
common operator locations such as Volta, mise/asdf shims, Homebrew, and
`/usr/bin`.

### Claude ACP

```sh
command -v npx
curl -s http://127.0.0.1:8765/hecate/v1/agent-adapters | jq '.data[] | select(.id=="claude_code")'
```

If `available` is true, Hecate can start Claude ACP. The first run may fetch and
cache the official `@agentclientprotocol/claude-agent-acp` package through
`npx`.

If Hecate reports that the managed launcher is unavailable, install Node/npm or
start Hecate from an environment where `npx` is available. Hecate also checks
common operator locations such as Volta, mise/asdf shims, Homebrew, and
`/usr/bin`.

### Direct CLI adapters

```sh
command -v cursor-agent
cursor-agent acp --help
cursor-agent login
command -v grok
grok login
```

Headless environments can authenticate through vendor-supported API keys:

```sh
export CURSOR_API_KEY=...
export XAI_API_KEY=...
```

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

3. Choose an available adapter.

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
   - per-block thinking entries when the adapter streams ACP `agent_thought_chunk` updates (chunks sharing a `messageId` collapse into one row; a new `messageId` starts a fresh row)
   - per-file `file_change` rows for each completed mutating tool call (kind = edit / delete / move) in addition to the end-of-turn diff-stat aggregate
   - context usage in the shell status bar when the adapter reports it
   - captured workspace diff under the inline diff disclosure when files changed
   - raw ACP diagnostics under the inline diagnostic disclosure when they differ from the normalized transcript

## Runtime behavior

Each External Agent chat session maps to one native ACP session. Hecate starts or
restores the selected adapter process when the chat is created, creates the ACP
session before the first prompt, and reuses it for later prompts in the same
External Agent chat session. The adapter is fixed for the chat session; start a
new External Agent chat to choose another adapter.
The visible chat title can be renamed from the Chats sidebar without changing
the ACP native session, workspace, or adapter selection.
Hecate-owned chats are different: their provider/model selection can change
between direct model turns and new task-backed segments in one transcript.

Hecate validates the workspace before creating a session, sanitizes the
environment passed to the ACP adapter process, applies timeout/cancel behavior,
captures ACP updates with an output cap, and records Git diff / diff stat after
each turn. External agent adapters are still trusted subprocesses in the
selected workspace; this is not equivalent to the task runtime sandbox.
Read [Security](security.md) for the full runtime-boundary and workspace-safety
model.

On Hecate shutdown, active External Agent turns are cancelled first. Hecate waits
briefly for the ACP turn to drain, asks the native ACP session to close, and
then kills the owned adapter process group if it is still alive. This keeps app
quit / restart from leaving Codex, Claude Code, Cursor Agent, or Grok Build
adapter processes behind.
Operators can also close an External Agent chat session manually to release the external
adapter process while keeping the Hecate chat history. Deleting a chat performs
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

Durable approval grants are part of the chat-session SQLite bundle. When
`HECATE_BACKEND=sqlite`, grants survive Hecate server restarts and are listed
from `GET /hecate/v1/chat/grants`; the operator can revoke them from
Connections. Pending approvals from a dead process are not
replayed as actionable prompts — startup reconcile marks them `timed_out` with
`path=startup_reconcile` before Hecate accepts traffic.

![Chats workspace with an external-agent file-write approval waiting for operator review](screenshots/chat-agent-approval.png)

![Agent approval modal with ACP options, scope choices, and audit note](screenshots/chat-agent-approval-modal.png)

## Approval mode

`HECATE_AGENT_ADAPTER_APPROVAL_MODE` controls how Hecate responds to ACP
`RequestPermission` from external adapters. Three values:

- `prompt` (default) — Hecate records a pending row and waits for an
  operator decision via the Chats workspace banner / modal or the
  `/hecate/v1/chat/sessions/{id}/approvals` REST surface. Without an operator
  reviewing within `HECATE_AGENT_ADAPTER_APPROVAL_TIMEOUT` (default 5m), the
  approval times out and the adapter receives ACP `Cancelled`.
- `auto` — every adapter request is permitted without review. Surfaces a red
  danger banner across every Chats session because every adapter call runs
  unsupervised. Useful only for headless / CI usage where no operator is
  available; never the right setting for interactive use.
- `deny` — every adapter request is refused.

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

External agent adapters are stable enough for alpha use when the operator
accepts the trusted-subprocess model: Codex, Claude Code, Cursor Agent, and Grok Build run as
their own runtimes in the selected workspace while Hecate supervises lifecycle,
approvals, output capture, diagnostics, observability, guardrails, and Git diff
inspect/revert.

## Future enhancements

- Patch review is intentionally "already applied" for now. Hecate captures
  diff data, exposes structured changed-file / per-file diff APIs, and the
  Chats UI can inspect or revert captured Git paths. A fuller review surface
  with side-by-side hunks, batch selection, and richer artifact history is
  still future work.
- Adapter-specific ACP mappers can make Codex, Claude Code, Cursor Agent, and Grok Build progress
  updates prettier over time. The current generic mapper plus raw diagnostics is
  sufficient for alpha stability.
- External Agent chat is a lightweight API around opaque external runtimes.
  Future work may reuse more task-runtime primitives for artifacts, event
  history, retention, and trace correlation, but Hecate should not pretend it
  owns the Codex / Claude Code / Cursor Agent / Grok Build runtime loop.

# External agent adapters: Hecate as an ACP client

Hecate can run external coding-agent CLIs from the **Chats** view. This is for
using Codex, Claude Code, Cursor Agent, and later similar tools through the same
operator console used for model chat.

External agents are not model providers. They are long-lived ACP agent sessions
running in a selected workspace. Hecate supervises the adapter process, forwards
prompts over ACP, records the normalized transcript plus raw ACP updates, and
captures timing, workspace branch, and Git diff. Cost is still reported as
`external`; when an adapter emits ACP `usage_update`, Hecate also records the
reported context-window usage and optional adapter-reported cost for display.

Chat transcripts are durable when `GATEWAY_CHAT_SESSIONS_BACKEND=sqlite`.
Hecate also stores the native ACP session id. After a gateway or native-app
restart, the next prompt asks the adapter to `session/load` that native session
when the adapter advertises ACP load-session support. If the adapter cannot load
the saved id, Hecate starts a fresh native session and keeps the existing
Hecate transcript.

## Relationship to the ACP bridge

ACP appears in Hecate in two directions:

| Direction | What Hecate does | Where to read |
|---|---|---|
| **Hecate as an ACP client/operator** | Launches and supervises external ACP adapters from **Chats → External Agent**. This is the flow documented here. | This page |
| **Hecate as an ACP agent** | Exposes Hecate's task runtime to external editor ACP hosts through `hecate-acp`. | [ACP bridge](acp.md) |

The two flows share the ACP protocol vocabulary, but they do not share a
process model. Agent Chat owns the external adapter process. Editor ACP hosts
own the `hecate-acp` bridge process.

## Supported adapters

| Adapter | How Hecate starts it | Auth expected by the underlying agent |
|---|---|---|
| Codex | Hecate-managed launcher for `@zed-industries/codex-acp` via local `npx`; direct `codex-acp` also works | Codex CLI / adapter login or config |
| Claude Code | Hecate-managed launcher for `@agentclientprotocol/claude-agent-acp` via local `npx`; direct `claude-agent-acp` also works | Adapter-visible Claude auth: `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`, `ANTHROPIC_API_KEY`, or `ANTHROPIC_AUTH_TOKEN` |
| Cursor Agent | `cursor-agent acp` | `cursor-agent login` or `CURSOR_API_KEY` |

## Quick start from the operator UI

1. Start Hecate and open **Chats**.
2. Pick **Codex**, **Claude Code**, or **Cursor Agent** from the agent picker.
3. Choose the workspace directory the external agent is allowed to work in.
4. Click **New chat**. Hecate starts the adapter session immediately so
   adapter-owned controls such as model, reasoning, or mode can appear before
   the first prompt.
5. If the adapter row is amber/red, open **Connections**. The probe performs
   a real spawn + ACP handshake + temporary
   no-op session, so it catches missing auth, billing/subscription issues,
   unsupported versions, and broken managed launchers before a prompt fails.
6. Send a small smoke prompt:

   ```text
   Show me git status and summarize what changed.
   ```

External Agent chats are intentionally separate from Hecate Chat. They do not
use Hecate model providers or Hecate's task sandbox. The selected adapter owns
its model/runtime/subscription; Hecate supervises the process, approvals,
transcript, diagnostics, traces, guardrails, and Git diff review.

When an adapter supports ACP session configuration, Hecate surfaces those
controls in the chat header as soon as the External Agent chat is created. The
controls are adapter-defined: Codex / Claude Code / Cursor decide which model,
mode, or reasoning selectors exist and what the labels mean. The selected
adapter is fixed for that chat; start another chat to use a different agent.
Hecate stores the latest reported control state with the chat session and sends
changes back with ACP `session/set_config_option`; it does not translate those
controls into Hecate provider/model settings.

Check discovery:

```sh
curl -s http://127.0.0.1:8765/hecate/v1/agent-adapters | jq
```

Discovery reports command availability, tested version range, and lightweight
auth hints (`auth_status`: `ok`, `unauthenticated`, `billing`, or `unknown`).

Claude Code has one important wrinkle: a normal interactive `claude /login`
or `~/.claude.json` config can make the standalone Claude Code CLI work while
the ACP adapter still reports `Authentication required`. If adapter readiness
reports an auth failure and you want to use your Claude subscription instead of an API key, open
Connections, run `claude setup-token`, paste the printed token
into the Claude Code guided setup card, and click **Save**. Hecate
stores the token in the local control-plane secret store and injects it only
into `claude-agent-acp` as `CLAUDE_CODE_OAUTH_TOKEN`. `ANTHROPIC_API_KEY` and
`ANTHROPIC_AUTH_TOKEN` also work when you want to supply Anthropic credentials
directly.
Connections refreshes adapter readiness when opened. You can also
call the probe endpoint for a full spawn + ACP handshake + no-op session check:

![Connections — adapter readiness checks and durable approval grants](screenshots/connections-external-agents.png)

```sh
curl -X POST http://127.0.0.1:8765/hecate/v1/agent-adapters/codex/probe | jq
```

For Codex and Claude, Hecate does not require `codex-acp` or
`claude-agent-acp` to be installed on `PATH`. If the direct command is missing
but `npx` is available, Hecate creates a small launcher in the operator cache
directory and runs the official ACP npm package from there. Cursor still
requires the Cursor Agent CLI because its ACP mode is shipped by `cursor-agent`.

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

For development and e2e smoke tests, force discovery states without changing
your machine:

```sh
just dev-no-agent-adapters
just dev-agent-adapters 'claude_code=missing,codex=available,cursor_agent=missing'
```

Those recipes set `GATEWAY_AGENT_ADAPTER_DISCOVERY_OVERRIDES`. The override is
intentionally discovery-only: it lets Connections and Chats render missing /
available states, but it does not create fake adapter processes or make a chat
send succeed.

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

### Cursor Agent

```sh
command -v cursor-agent
cursor-agent acp --help
cursor-agent login
```

Cursor can also authenticate through:

```sh
export CURSOR_API_KEY=...
```

If a run fails with `Authentication required. Please run 'agent login' first, or
set CURSOR_API_KEY environment variable.`, authenticate Cursor Agent in the same
environment that starts Hecate.

## Manual smoke

1. Start Hecate:

   ```sh
   just dev
   ```

2. Open **Chats** and switch the target from **Hecate Chat** to
   **External Agent**.

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

Each Agent Chat session maps to one native ACP session. Hecate starts the
selected adapter process the first time the chat sends a prompt, creates the ACP
session, and reuses it for later prompts in the same Hecate chat. The adapter is
fixed for the chat session; start a new Agent Chat to choose another adapter.
The visible chat title can be renamed from the Chats sidebar without changing
the ACP native session, workspace, or adapter selection.
Model Chats are different: their provider/model selection is per request and
can change inside one session.

Hecate validates the workspace before creating a session, sanitizes the
environment passed to the ACP adapter process, applies timeout/cancel behavior,
captures ACP updates with an output cap, and records Git diff / diff stat after
each turn. External agent adapters are still trusted subprocesses in the
selected workspace; this is not equivalent to the task runtime sandbox.
Read [Security](security.md) for the full runtime-boundary and workspace-safety
model.

On Hecate shutdown, active Agent Chat turns are cancelled first. Hecate waits
briefly for the ACP turn to drain, asks the native ACP session to close, and
then kills the owned adapter process group if it is still alive. This keeps app
quit / restart from leaving Codex, Claude, or Cursor adapter processes behind.
Operators can also close an Agent Chat session manually to release the external
adapter process while keeping the Hecate chat history. Deleting a chat performs
the same release step and then removes the persisted history.

Every prompt also gets OTel-shaped observability. The message response includes
`request_id`, `trace_id`, and `span_id`, and `GET
/hecate/v1/traces?request_id=<request_id>` shows the `agent_chat.run` span with adapter
identity, workspace, status, duration, output byte counts, and diff-capture
state. Approval gating adds two more spans:
`agent_adapter.approval.request` covers the coordinator's decision (grant
short-circuit, mode default, or prompt-mode wait) and carries
`hecate.agent_adapter.approval.path` once the path is known;
`agent_adapter.approval.resolve` wraps the operator's decision-application
path with `decision` and `scope` attributes.

Durable approval grants are part of the Agent Chat SQLite bundle. When
`GATEWAY_CHAT_SESSIONS_BACKEND=sqlite`, grants survive gateway restarts and are
listed from `GET /hecate/v1/agent-chat/grants`; the operator can revoke them from
Connections. Pending approvals from a dead process are not
replayed as actionable prompts — startup reconcile marks them `timed_out` with
`path=startup_reconcile` before the gateway accepts traffic.

![Chats workspace with an external-agent file-write approval waiting for operator review](screenshots/chat-agent-approval.png)

![Agent approval modal with ACP options, scope choices, and audit note](screenshots/chat-agent-approval-modal.png)

## Approval mode and the alpha → prompt migration

`GATEWAY_AGENT_ADAPTER_APPROVAL_MODE` controls how the gateway responds to ACP
`RequestPermission` from external adapters. Three values:

- `prompt` (default) — the gateway records a pending row and waits for an
  operator decision via the Chats workspace banner / modal or the
  `/hecate/v1/agent-chat/sessions/{id}/approvals` REST surface. Without an operator
  reviewing within `GATEWAY_AGENT_ADAPTER_APPROVAL_TIMEOUT` (default 5m), the
  approval times out and the adapter receives ACP `Cancelled`.
- `auto` — every adapter request is permitted without review. Surfaces a red
  danger banner across every Chats session because every adapter call runs
  unsupervised. Useful only for headless / CI usage where no operator is
  available; never the right setting for interactive use.
- `deny` — every adapter request is refused.

**Alpha → prompt migration.** Through the alpha cycle the effective default
was `auto`; from this release the default is `prompt`. Operators who relied
on the old auto-approve behaviour — typically headless / CI flows where no
operator UI is connected — must explicitly set
`GATEWAY_AGENT_ADAPTER_APPROVAL_MODE=auto`. Without this, the first adapter
request in a new chat will block for the full timeout and then surface as
`Cancelled` to the adapter, looking like an inert hang.

## Runtime guardrails

### Per-session turn ceiling

`GATEWAY_AGENT_CHAT_MAX_TURNS_PER_SESSION` caps the number of user→assistant
round-trips per agent-chat session. When a session reaches the ceiling,
`POST /hecate/v1/agent-chat/sessions/{id}/messages` returns HTTP 422:

```json
{
  "error": {
    "type": "agent_chat.session_limit_exceeded",
    "message": "session has reached the 50-turn limit; start a new session to continue",
    "limit": 50,
    "turns_used": 50
  }
}
```

| Setting | Behavior |
|---|---|
| `GATEWAY_AGENT_CHAT_MAX_TURNS_PER_SESSION=0` | Unlimited (default) |
| `GATEWAY_AGENT_CHAT_MAX_TURNS_PER_SESSION=50` | Enforce 50-turn ceiling per session |

When a limit is set, the chat header shows a `{turns_used}/{max} turns` badge.
The badge turns amber when the ceiling is reached.

Turns are counted per session, not per workspace — starting a new session in
the same workspace resets the counter.

### Wall-clock and idle limits

Two optional time-based limits protect long-lived ACP sessions from turning
into invisible background processes:

| Setting | Behavior |
|---|---|
| `GATEWAY_AGENT_CHAT_MAX_SESSION_DURATION=0s` | Unlimited wall-clock age (default) |
| `GATEWAY_AGENT_CHAT_MAX_SESSION_DURATION=2h` | Reject new turns once the session is at least 2 hours old |
| `GATEWAY_AGENT_CHAT_IDLE_TIMEOUT=0s` | No idle sweeper (default) |
| `GATEWAY_AGENT_CHAT_IDLE_TIMEOUT=1h` | Auto-close idle sessions after 1 hour without updates |

When the wall-clock limit is exceeded, `POST
/hecate/v1/agent-chat/sessions/{id}/messages` returns HTTP 422 with
`agent_chat.session_duration_limit_exceeded`.

When the idle limit is exceeded, the background sweeper cancels the session,
clears the native ACP handle, and appends an `interrupted` activity to the last
assistant message when one exists. If the operator sends a new prompt before
the sweeper has closed the stale session, the request returns HTTP 422 with
`agent_chat.session_idle_timeout`; start a new chat to continue.

## Stable alpha scope

External agent adapters are stable enough for alpha use when the operator
accepts the trusted-subprocess model: Codex, Claude Code, and Cursor run as
their own runtimes in the selected workspace while Hecate supervises lifecycle,
approvals, output capture, diagnostics, observability, guardrails, and Git diff
inspect/revert.

## Future enhancements

- Patch review is intentionally "already applied" for now. Hecate captures
  diff data, exposes structured changed-file / per-file diff APIs, and the
  Chats UI can inspect or revert captured Git paths. A fuller review surface
  with side-by-side hunks, batch selection, and richer artifact history is
  still future work.
- Adapter-specific ACP mappers can make Codex, Claude Code, and Cursor progress
  updates prettier over time. The current generic mapper plus raw diagnostics is
  sufficient for alpha stability.
- Agent Chat is a lightweight API around opaque external runtimes. Future work
  may reuse task-runtime primitives for artifacts, event history, retention,
  and trace correlation, but Hecate should not pretend it owns the Codex /
  Claude / Cursor runtime loop.

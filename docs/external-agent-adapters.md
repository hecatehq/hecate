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
| **Hecate as an ACP client/operator** | Launches and supervises external ACP adapters from **Chats → Agent**. This is the flow documented here. | This page |
| **Hecate as an ACP agent** | Exposes Hecate's task runtime to external editor ACP hosts through `hecate-acp`. | [ACP bridge](acp.md) |

The two flows share the ACP protocol vocabulary, but they do not share a
process model. Agent Chat owns the external adapter process. Editor ACP hosts
own the `hecate-acp` bridge process.

## Supported adapters

| Adapter | How Hecate starts it | Auth expected by the underlying agent |
|---|---|---|
| Codex | Hecate-managed launcher for `@zed-industries/codex-acp` via local `npx`; direct `codex-acp` also works | Codex CLI / adapter login or config |
| Claude Code | Hecate-managed launcher for `@agentclientprotocol/claude-agent-acp` via local `npx`; direct `claude-agent-acp` also works | Claude agent / adapter login or config |
| Cursor Agent | `cursor-agent acp` | `cursor-agent login` or `CURSOR_API_KEY` |

Check discovery:

```sh
curl -s http://127.0.0.1:8765/v1/agent-adapters | jq
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
development or a packaged desktop build.

## Setup checks

Agent Chat does not use Hecate model providers. It needs the selected
coding-agent to be authenticated, and either a direct ACP command or a managed
package runner to be visible to Hecate.

### Codex ACP

```sh
command -v npx
curl -s http://127.0.0.1:8765/v1/agent-adapters | jq '.data[] | select(.id=="codex")'
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
curl -s http://127.0.0.1:8765/v1/agent-adapters | jq '.data[] | select(.id=="claude_code")'
```

If `available` is true, Hecate can start Claude ACP. The first run may fetch and
cache the official `@agentclientprotocol/claude-agent-acp` package through
`npx`.

If Hecate reports that the managed launcher is unavailable, install Node/npm or
start Hecate from an environment where `npx` is available.

Hecate strips `ANTHROPIC_*` provider variables from the Claude Code adapter
environment. Claude Code subscription login is file-backed, and forwarding
provider API variables can make the ACP runner use Console credits instead of
the `/login` managed key shown by `claude /status`.

If Claude reports `Credit balance is too low`, run `claude /status` from the
same workspace and confirm it is using the account you expect. Hecate preserves
the raw adapter error in diagnostics, but shows a friendlier usage-limit message
in Chats.

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
   make dev
   ```

2. Open **Chats** and switch the target from **Model** to **Agent**.

3. Choose an available adapter.

4. Choose a workspace directory from the folder button, or use **paste path**
   when the native folder dialog is not available. Hecate stores the canonical
   path and shows the full path plus Git branch in the shell status bar.

5. Send a prompt, for example:

   ```text
   Show me git status and summarize what changed.
   ```

6. Confirm the assistant message shows:
   - structured activity markers such as starting, running, files changed, or failed
   - normalized transcript text
   - context usage in the shell status bar when the adapter reports it
   - captured workspace diff under the inline diff disclosure when files changed
   - raw ACP diagnostics under the inline diagnostic disclosure when they differ from the normalized transcript

## Runtime behavior

Each Agent Chat session maps to one native ACP session. Hecate starts the
selected adapter process the first time the chat sends a prompt, creates the ACP
session, and reuses it for later prompts in the same Hecate chat. The adapter is
fixed for the chat session; start a new Agent Chat to choose another adapter.
Model Chats are different: their provider/model selection is per request and
can change inside one session.

Hecate validates the workspace before creating a session, sanitizes the
environment passed to the ACP adapter process, applies timeout/cancel behavior,
captures ACP updates with an output cap, and records Git diff / diff stat after
each turn. External agent adapters are still trusted subprocesses in the
selected workspace; this is not equivalent to the task runtime sandbox.

Every prompt also gets OTel-shaped observability. The message response includes
`request_id`, `trace_id`, and `span_id`, and `GET
/v1/traces?request_id=<request_id>` shows the `agent_chat.run` span with adapter
identity, workspace, status, duration, output byte counts, and diff-capture
state.

## Troubleshooting

| Symptom | What to check |
|---|---|
| Codex or Claude adapter is missing | Hecate could not find the direct ACP command or a local `npx` runner. Install Node/npm, or make sure Volta/mise/asdf/Homebrew is visible to the process that starts Hecate. |
| Cursor adapter is missing | `cursor-agent` is not visible to Hecate. Install Cursor Agent and restart Hecate after changing shell/runtime managers such as Volta. |
| Cursor says authentication is required | Run `cursor-agent login` or set `CURSOR_API_KEY` in the environment that starts Hecate. |
| Output looks strange | Open the message's raw output disclosure. The visible transcript is normalized from ACP updates, but raw update JSON is retained for adapter debugging. |
| Run hangs | Use the Stop button. Hecate sends ACP cancellation and marks the run `cancelled`. |
| Diff is empty | The workspace may not be a Git repo, or the adapter did not change files. |

## Current gaps

- Patch apply/revert UX is not implemented yet. Hecate captures and displays
  diff data inline, but does not provide a dedicated patch action surface.
- ACP terminal reverse-RPC is not implemented yet. Adapters that require the
  editor/client to own terminal execution will receive a clear unsupported
  response.
- Agent Chat is a lightweight API, not yet a full Task/Run. Converging it onto
  Tasks would unlock durable events, approvals, artifacts, policy, and richer
  OpenTelemetry correlation.

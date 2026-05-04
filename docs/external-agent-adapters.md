# External agent adapters

Hecate can run external coding-agent CLIs from the **Chats** view. This is for
using Codex, Claude Code, Cursor Agent, and later similar tools through the same
operator console used for model chat.

External agents are not model providers. They are long-lived ACP agent sessions
running in a selected workspace. Hecate supervises the adapter process, forwards
prompts over ACP, records the normalized transcript plus raw ACP updates, and
captures timing, workspace branch, and Git diff. Cost is still reported as
`external` unless a future adapter supplies structured usage.

## Supported adapters

| Adapter | Command Hecate starts | Auth expected by the underlying agent |
|---|---|---|
| Codex | `codex-acp` | Codex CLI / adapter login or config |
| Claude Code | `claude-agent-acp` | Claude agent / adapter login or config |
| Cursor Agent | `cursor-agent acp` | `cursor-agent login` or `CURSOR_API_KEY` |

Check discovery:

```sh
curl -s http://127.0.0.1:8765/v1/agent-adapters | jq
```

An adapter with `"available": false` is not on `PATH` and cannot be selected
until the command is installed or reachable from the gateway process environment.

## Setup checks

Agent Chat does not use Hecate model providers. It only needs the selected
coding-agent CLI to be installed, authenticated, and visible to the process that
started Hecate.

### Codex ACP

```sh
command -v codex-acp
codex-acp --help
```

If Hecate reports `exec: "codex-acp": executable file not found in $PATH`,
install the Codex ACP adapter or start Hecate from an environment where
`codex-acp` is on `PATH`.

### Claude ACP

```sh
command -v claude-agent-acp
claude-agent-acp --help
```

If Hecate reports `exec: "claude-agent-acp": executable file not found in
$PATH`, install the Claude ACP adapter or restart Hecate after your
shell/runtime manager updates `PATH`.

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
| Adapter is missing | The ACP adapter command is not visible to the gateway process `PATH`. Restart Hecate after changing shell/runtime managers such as Volta. |
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

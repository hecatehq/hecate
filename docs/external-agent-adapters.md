# External agent adapters

Hecate can run external coding-agent CLIs from the **Chats** view. This is for
dogfooding Codex, Claude Code, Cursor Agent, and later similar tools through the
same operator console used for model chat.

External agents are not model providers. They are supervised local processes
running in a selected workspace. Hecate records their transcript, raw output,
status, timing, workspace branch, and captured Git diff, but cost is still
reported as `external` unless a future adapter supplies structured usage.

## Supported adapters

| Adapter | Command | Auth expected by the CLI |
|---|---|---|
| Codex | `codex` | Codex CLI login / config |
| Claude Code | `claude` | Claude Code login / config |
| Cursor Agent | `cursor-agent` | `cursor-agent login` or `CURSOR_API_KEY` |

Check discovery:

```sh
curl -s http://127.0.0.1:8765/v1/agent-adapters | jq
```

An adapter with `"available": false` is not on `PATH` and cannot be selected
until the command is installed or reachable from the gateway process environment.

## Manual smoke

1. Start Hecate:

   ```sh
   make dev
   ```

2. Open **Chats** and switch the target from **Model** to **Agent**.

3. Choose an available adapter.

4. Choose a workspace directory from the folder button. Hecate stores the
   canonical path and shows the full path plus Git branch in the shell status
   bar.

5. Send a prompt, for example:

   ```text
   Show me git status and summarize what changed.
   ```

6. Confirm the assistant message shows:
   - structured activity markers such as starting, running, files changed, or failed
   - normalized transcript text
   - captured workspace diff under the inline diff disclosure when files changed
   - raw process output under the inline diagnostic disclosure when it differs from the normalized transcript

## Runtime behavior

Each prompt runs the selected adapter once. The adapter is fixed for the chat
session; start a new Agent Chat to choose another adapter. Model Chats are
different: their provider/model selection is per request and can change inside
one session.

Hecate validates the workspace before creating a session, sanitizes the
environment passed to the subprocess, applies timeout/cancel behavior, captures
stdout and stderr with an output cap, and records Git diff / diff stat after the
run. External CLIs are still trusted subprocesses in the selected workspace;
this is not equivalent to the task runtime sandbox.

Every prompt also gets OTel-shaped observability. The message response includes
`request_id`, `trace_id`, and `span_id`, and `GET
/v1/traces?request_id=<request_id>` shows the `agent_chat.run` span with adapter
identity, workspace, status, duration, output byte counts, and diff-capture
state.

## Troubleshooting

| Symptom | What to check |
|---|---|
| Adapter is missing | The command is not visible to the gateway process `PATH`. Restart Hecate after changing shell/runtime managers such as Volta. |
| Cursor says authentication is required | Run `cursor-agent login` or set `CURSOR_API_KEY` in the environment that starts Hecate. |
| Output looks strange | Open the message's raw process output disclosure. The visible transcript is normalized, but raw stdout/stderr is retained for adapter debugging. |
| Run hangs | Use the Stop button. Hecate cancels the process context and marks the run `cancelled`. |
| Diff is empty | The workspace may not be a Git repo, or the adapter did not change files. |

## Current gaps

- Patch apply/revert UX is not implemented yet. Hecate captures and displays
  diff data inline, but does not provide a dedicated patch action surface.
- Claude Code and Cursor Agent currently use mostly text-output mapping. Codex
  has JSONL normalization, and adapter-specific structured mappers should be
  expanded as their CLI output contracts stabilize.
- Agent Chat is a lightweight API, not yet a full Task/Run. Converging it onto
  Tasks would unlock durable events, approvals, artifacts, policy, and richer
  OpenTelemetry correlation.

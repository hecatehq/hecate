# ACP bridge

Hecate has an early ACP bridge binary at `cmd/hecate-acp`. It starts a
newline-delimited JSON-RPC stdio loop, advertises gateway models during
`initialize`, creates coding-agent tasks from `session/prompt`, and forwards
run stream snapshots as `session/update` notifications.

The bridge is for editor-agent integrations. It is not a replacement for the
gateway API; it is a thin adapter that lets an ACP client talk to Hecate's
existing task runtime.

Protocol reference: [Agent Client Protocol](https://agentclientprotocol.com/).

## Contents

- [Current status](#current-status)
- [Distribution and lifecycle](#distribution-and-lifecycle)
- [Zed setup](#zed-setup)
- [JetBrains setup](#jetbrains-setup)
- [Native app setup](#native-app-setup)
- [Other ACP clients](#other-acp-clients)
- [Session model](#session-model)
- [Configuration](#configuration)
- [Smoke test](#smoke-test)

## Current status

Implemented:

- Stdio JSON-RPC loop and parse/invalid-request responses.
- `initialize`, `session/new`, `session/prompt`, and `session/cancel`.
- Gateway model discovery during initialize.
- Task creation/start for the first prompt.
- Multi-prompt continuation through the existing task/run chain.
- Run cancellation.
- Run-event stream mapping to `session/update`.
- Editor approval round-trip through `session/request_permission`.

Not implemented yet:

- Editor-owned workspace calls.
- Registry packaging for a specific editor.
- Headless compatibility tests against a real Zed or JetBrains ACP host.

## Distribution and lifecycle

`hecate-acp` ships as a separate binary in the Go release tarballs, next to
`gateway`. It is not bundled as a Tauri sidecar.

That split is deliberate:

- The desktop app owns the gateway lifecycle: launch Hecate, start `gateway`,
  open the UI, stop the process when the app exits.
- ACP clients own the bridge lifecycle: Zed, JetBrains, or another ACP host
  starts `hecate-acp` over stdio and stops it when the editor session ends.

Install the bridge somewhere stable and point the editor's ACP configuration at
that executable. The bridge talks to a running Hecate gateway via
`HECATE_GATEWAY_URL`.

## Zed setup

Zed can run custom ACP agents from `settings.json` under `agent_servers`.
Hecate is not in the public ACP registry yet, so use a custom server entry that
starts `hecate-acp` over stdio.

Official reference: [Zed External Agents](https://zed.dev/docs/ai/external-agents).

1. Build or install the bridge:

   ```sh
   make build-acp
   ```

   For local development, the command path is the repo-local
   `./hecate-acp`. For releases, unpack the Go tarball and use the absolute
   path to the `hecate-acp` binary inside it.

2. Start Hecate separately:

   ```sh
   make dev
   ```

   The bridge expects a running gateway at `http://127.0.0.1:8765` unless
   `HECATE_GATEWAY_URL` overrides it.

3. Add a custom agent server to Zed settings:

   ```json
   {
     "agent_servers": {
       "Hecate": {
         "type": "custom",
         "command": "/absolute/path/to/hecate-acp",
         "args": [],
         "env": {
           "HECATE_GATEWAY_URL": "http://127.0.0.1:8765"
         }
       }
     }
   }
   ```

4. Open Zed's Agent panel, start a new external-agent thread, and select
   **Hecate** from the agent picker.

5. If startup fails, open Zed's ACP log view from the command palette with
   `dev: open acp logs`. Also verify the gateway directly:

   ```sh
   curl -s http://127.0.0.1:8765/healthz
   ```

## JetBrains setup

JetBrains IDEs support custom ACP agents through AI Assistant. Hecate is not in
the JetBrains ACP registry yet, so add it manually in `~/.jetbrains/acp.json`.

Official reference: [JetBrains Agent Client Protocol documentation](https://www.jetbrains.com/help/ai-assistant/acp.html).

1. Build or install the bridge:

   ```sh
   make build-acp
   ```

2. Start Hecate separately:

   ```sh
   make dev
   ```

3. In the IDE, open **AI Chat**, choose **Add Custom Agent**, and edit the
   generated `~/.jetbrains/acp.json`:

   ```json
   {
     "default_mcp_settings": {
       "use_custom_mcp": true,
       "use_idea_mcp": false
     },
     "agent_servers": {
       "Hecate": {
         "command": "/absolute/path/to/hecate-acp",
         "args": [],
         "env": {
           "HECATE_GATEWAY_URL": "http://127.0.0.1:8765"
         }
       }
     }
   }
   ```

4. Select **Hecate** in AI Chat and start a prompt.

5. If startup fails, use **Get ACP Logs** from the AI Chat menu. JetBrains also
   has extended ACP logging behind the `llm.agent.extended.logging` registry
   key, but those logs may include sensitive chat content. Hecate's bridge still
   talks to the same gateway, so also verify:

   ```sh
   curl -s http://127.0.0.1:8765/healthz
   ```

Current JetBrains limitation: ACP-compatible agents are not supported in WSL.

## Native app setup

The native desktop app can run the gateway for the operator UI, but it does not
bundle or launch `hecate-acp` for editors. ACP clients still need a separate
`hecate-acp` binary and a `HECATE_GATEWAY_URL` pointing at a running gateway.

Important current limitation: the desktop app starts its gateway sidecar on a
free port chosen at launch, such as `http://127.0.0.1:52341`. That port is not
stable and is not yet exposed through an app command, config file, or registry
entry for other processes to consume.

For a reliable editor setup today, run a fixed-port gateway separately and point
both the editor bridge and your browser at it:

```sh
make dev
curl -s http://127.0.0.1:8765/healthz
```

Then configure Zed or JetBrains with:

```json
{
  "HECATE_GATEWAY_URL": "http://127.0.0.1:8765"
}
```

You can keep the native app closed for this mode, or use it separately for UI
testing. Running both the native app sidecar and a fixed-port development
gateway is safe because the native app chooses a different free port.

For local development only, you can connect to the native app's current sidecar
port after launch:

1. Open the native Hecate app.
2. Open the gateway log from the Hecate menu, or inspect running gateway
   processes.
3. Find the `127.0.0.1:<port>` used by that launch.
4. Set `HECATE_GATEWAY_URL` in the editor ACP config to that exact URL.

That workaround must be repeated after every app restart. A future native-app
integration should expose the active gateway URL in a stable place or launch the
ACP bridge as an app-managed sidecar.

## Other ACP clients

Any ACP host that can spawn a local command and speak JSON-RPC over stdio should
be able to launch `hecate-acp`, but only Zed and JetBrains are documented here
as first-class manual setup targets for now.

Known ACP client surfaces in the wider ecosystem include:

| Client | Status | Notes |
|---|---|---|
| Zed | Native | Custom `agent_servers` in `settings.json`, plus ACP registry support. |
| JetBrains IDEs | Native via AI Assistant | Custom agents in `~/.jetbrains/acp.json`, plus curated registry support. |
| Neovim | Plugin | ACP support through plugins such as CodeCompanion / avante.nvim. |
| Emacs | Plugin | ACP support through `agent-shell`. |
| Obsidian | Plugin | ACP side-panel plugin for agent threads. |
| marimo | Native / app-level | Listed in the ACP ecosystem for notebook-style workflows. |
| Browser / web apps | Library / adapter | Possible through ACP client libraries, but Hecate has not tested this path. |

Treat anything outside Zed and JetBrains as "bring your own host config" until
Hecate has smoke tests or packaged examples for that client.

## Session model

For alpha, one ACP session maps to one durable Hecate `agent_loop` task after the first prompt:

1. `initialize` calls the gateway's model-discovery surface and advertises available models.
2. `session/new` creates only bridge-local session state.
3. The first `session/prompt` creates and starts the Hecate task.
4. Later prompts call `POST /v1/tasks/{id}/runs/{run_id}/continue`, which hydrates the saved conversation, appends the new user message, and starts the next run.

The gateway remains the source of truth. The bridge does not invent runtime
state that Hecate did not emit.

## Configuration

| Variable | Default | Meaning |
|---|---:|---|
| `HECATE_GATEWAY_URL` | `http://127.0.0.1:8765` | Gateway base URL the bridge talks to. |
| `HECATE_AGENT_NAME` | `Hecate` | Agent display name advertised during initialize. |
| `HECATE_WORKSPACE_MODE` | `hecate-owned` | Future workspace ownership mode. |
| `HECATE_APPROVAL_ROUTE` | `editor` | `editor` sends approval gates to ACP `session/request_permission`; other values leave approvals for the Hecate operator UI. |

## Smoke test

Run the gateway, then send an initialize request to the bridge:

```sh
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"0.1","clientCapabilities":{"permissions":{}}}}' \
  | go run ./cmd/hecate-acp
```

The response should include `availableModels` populated from Hecate's
`/v1/models` endpoint.

The automated smoke lives in `e2e/acp-smoke.ts` and is wired into
`make verify-alpha`.

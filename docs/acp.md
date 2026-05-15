# ACP bridge: Hecate as an editor agent

Hecate has an early ACP bridge binary at `cmd/hecate-acp`. It starts a
newline-delimited JSON-RPC stdio loop, advertises gateway models during
`initialize`, creates coding-agent tasks from `session/prompt`, and forwards
run stream snapshots as `session/update` notifications.

The bridge is for editor-agent integrations. It is not a replacement for the
gateway API; it is a thin adapter that lets an ACP client talk to Hecate's
existing task runtime.

Protocol reference: [Agent Client Protocol](https://agentclientprotocol.com/).

## ACP directions in Hecate

ACP appears in Hecate in two different places:

| Direction | What Hecate does | User-facing surface | Doc |
|---|---|---|---|
| **Hecate as an ACP agent** | `hecate-acp` is launched by an editor ACP host and translates editor sessions into Hecate task-runtime work. | Zed, JetBrains, VS Code/Cursor extensions, other ACP hosts. | This page |
| **Hecate as an ACP client/operator** | The Chats view launches ACP-compatible coding-agent adapters such as Codex and Claude Code, then supervises their local process/session. | Hecate **Chats** agent picker. | [External agent adapters](external-agent-adapters.md) |

This is similar to the MCP documentation split by direction, but ACP currently
uses two separate pages because the operator jobs are different: editor setup vs
chatting with local coding-agent CLIs from inside Hecate.

## Contents

- [ACP directions in Hecate](#acp-directions-in-hecate)
- [Current status](#current-status)
- [Distribution and lifecycle](#distribution-and-lifecycle)
- [Gateway launch options](#gateway-launch-options)
- [Zed setup](#zed-setup)
- [JetBrains setup](#jetbrains-setup)
- [VS Code / Cursor setup](#vs-code--cursor-setup)
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

- End-to-end editor-owned workspace routing. The bridge negotiates the
  `editor-owned` workspace mode with the client at `initialize` (see
  [Configuration](#configuration)), and the workspace abstraction has both
  `LocalWorkspace` and `ACPWorkspace` implementations — but the gateway-side
  orchestrator still uses the local workspace in every mode. Wiring the
  bridge↔gateway transport that routes `fs/*` and `terminal/*` reverse-RPC
  through to the editor's filesystem and terminal panes lands in a follow-up
  RFC.
- Registry packaging for a specific editor.
- Headless compatibility tests against a real Zed or JetBrains ACP host.
- Durable editor-side session reattachment after bridge restart. The gateway
  task state is durable when SQLite task storage is enabled, but the ACP host
  still starts a fresh stdio bridge process.

TODO:

- Add Hecate to ACP registries once the bridge has been manually smoke-tested
  against real hosts and the launch/config story is stable. Until then, use the
  custom local agent setup below.

## Distribution and lifecycle

`hecate-acp` ships as a separate binary in the Go release tarballs, next to
`hecate`. The Docker image and native desktop app also include it, so each
distribution shape carries the same gateway plus ACP bridge pair. ACP hosts
still launch the bridge themselves over stdio.

That split is deliberate:

- The desktop app owns the gateway lifecycle: launch Hecate, start `hecate`,
  open the UI, stop the process when the app exits.
- ACP clients own the bridge lifecycle: Zed, JetBrains, or another ACP host
  starts `hecate-acp` over stdio and stops it when the editor session ends.

Install the bridge somewhere stable and point the editor's ACP configuration at
that executable. The bridge talks to a running Hecate gateway. If
`HECATE_GATEWAY_URL` is set, it wins; otherwise the bridge checks
`hecate.runtime.json` from the gateway data dir, then the native app data dir,
then falls back to `http://127.0.0.1:8765`.

Prefer `HECATE_GATEWAY_URL` when a parent process or launcher already knows the
gateway URL. The runtime file is intentionally only an ephemeral discovery
fallback for independently launched ACP hosts.

## Gateway launch options

ACP hosts always start `hecate-acp`; they do not start the gateway. Start
Hecate first using whichever distribution fits your setup:

| Setup | How to start Hecate | Bridge config |
|---|---|---|
| Native app | Open the Hecate desktop app | Omit `HECATE_GATEWAY_URL`; the bridge discovers the dynamic sidecar URL from `hecate.runtime.json`. |
| Release tarball | Run `./hecate` from the unpacked tarball | Usually omit `HECATE_GATEWAY_URL`; default fallback is `http://127.0.0.1:8765`. Set it only if you changed the port or URL. |
| Source checkout | Run `just dev` | Usually omit `HECATE_GATEWAY_URL`; the bridge checks `.data/hecate.runtime.json` and then falls back to `http://127.0.0.1:8765`. |
| Docker / Compose | Run `docker compose up` or `docker run -p 8765:8765 ...` | Set `HECATE_GATEWAY_URL=http://127.0.0.1:8765` in the editor config unless the bridge is running inside the same container. |
| Reverse proxy | Run Hecate behind your proxy | Set `HECATE_GATEWAY_URL` to the proxy URL. |

The runtime discovery file contains the active `base_url`, for example:

```json
{
  "base_url": "http://127.0.0.1:52341",
  "listen_addr": "127.0.0.1:52341",
  "pid": 12345,
  "updated_unix": 1770000000
}
```

For the native app, this file lives in the platform app data directory:

- macOS: `~/Library/Application Support/sh.hecate.app/hecate.runtime.json`
- Linux: `~/.local/share/sh.hecate.app/hecate.runtime.json`
- Windows: `%APPDATA%\sh.hecate.app\hecate.runtime.json`

For source, tarball, and Docker runs, it lives under `GATEWAY_DATA_DIR`
(`.data/hecate.runtime.json` by default for local runs, `/data/hecate.runtime.json`
inside the Docker image).

## Zed setup

Zed can run custom ACP agents from `settings.json` under `agent_servers`.
Hecate is not in the public ACP registry yet, so use a custom server entry that
starts `hecate-acp` over stdio.

Official reference: [Zed External Agents](https://zed.dev/docs/ai/external-agents).

1. Install the bridge:

   ```sh
   just build-acp
   ```

   For local development, the command path is the repo-local
   `./hecate-acp`. For releases, unpack the Go tarball and use the absolute
   path to the `hecate-acp` binary inside it. The native app also bundles the
   bridge next to the app executable with Tauri's platform suffix, such as
   `hecate-acp-aarch64-apple-darwin`.

2. Start Hecate using one of the [gateway launch options](#gateway-launch-options).

3. Add a custom agent server to Zed settings. For native app, source checkout,
   or default tarball usage, omit `HECATE_GATEWAY_URL` and let the bridge
   discover the gateway:

   ```json
   {
     "agent_servers": {
       "Hecate": {
         "type": "custom",
         "command": "/absolute/path/to/hecate-acp",
         "args": []
       }
     }
   }
   ```

   If you run Hecate through Docker, a non-default port, or a reverse proxy,
   set the URL explicitly:

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

1. Install the bridge:

   ```sh
   just build-acp
   ```

   For releases, use the `hecate-acp` binary from the tarball or native app
   bundle.

2. Start Hecate using one of the [gateway launch options](#gateway-launch-options).

3. In the IDE, open **AI Chat**, choose **Add Custom Agent**, and edit the
   generated `~/.jetbrains/acp.json`. For native app, source checkout, or
   default tarball usage, omit `HECATE_GATEWAY_URL`:

   ```json
   {
     "default_mcp_settings": {
       "use_custom_mcp": true,
       "use_idea_mcp": false
     },
     "agent_servers": {
       "Hecate": {
         "command": "/absolute/path/to/hecate-acp",
         "args": []
       }
     }
   }
   ```

   If the gateway is reachable only through Docker port publishing, a custom
   port, or a reverse proxy, include `HECATE_GATEWAY_URL`:

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

## VS Code / Cursor setup

VS Code and Cursor can use the same Hecate ACP configuration when an ACP client
extension is installed. This is extension-backed host support, not a native
Hecate integration.

Useful references:

- [ACP VS Code extension](https://marketplace.visualstudio.com/items?itemName=strato-space.acp-plugin)

1. Install an ACP client extension.

   In VS Code, install an ACP client extension from the Visual Studio
   Marketplace. In Cursor, install the same extension if it is available in
   Cursor's extension marketplace, or install a VSIX manually if needed.

2. Install the bridge:

   ```sh
   just build-acp
   ```

3. Start Hecate using one of the [gateway launch options](#gateway-launch-options).

4. Add Hecate to the editor settings using the same `agent_servers` shape used
   by Zed. For native app, source checkout, or default tarball usage, omit
   `HECATE_GATEWAY_URL`:

   ```json
   {
     "agent_servers": {
       "Hecate": {
         "type": "custom",
         "command": "/absolute/path/to/hecate-acp",
         "args": []
       }
     }
   }
   ```

   Add `HECATE_GATEWAY_URL` only when the gateway URL cannot be discovered or
   is not `http://127.0.0.1:8765`.

5. Open the ACP extension's chat view, select **Hecate**, and start a prompt.

6. If startup fails, verify both sides:

   ```sh
   curl -s http://127.0.0.1:8765/healthz
   /absolute/path/to/hecate-acp --version
   ```

Current status: this path is expected to work because it uses the same stdio ACP
bridge and `agent_servers` convention, but Hecate has not smoke-tested VS Code
or Cursor as ACP hosts yet.

## Other ACP clients

Any ACP host that can spawn a local command and speak JSON-RPC over stdio should
be able to launch `hecate-acp`, but only the hosts above have manual setup
examples in this document.

Known ACP client surfaces in the wider ecosystem include:

| Client | Status | Notes |
|---|---|---|
| Zed | Native | Custom `agent_servers` in `settings.json`, plus ACP registry support. |
| JetBrains IDEs | Native via AI Assistant | Custom agents in `~/.jetbrains/acp.json`, plus curated registry support. |
| VS Code | Extension | ACP client extensions can spawn `hecate-acp` with `agent_servers`; not smoke-tested by Hecate yet. |
| Cursor | Extension | Same config as VS Code when an ACP client extension is available or installed from VSIX; not smoke-tested by Hecate yet. |
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
4. Later prompts call `POST /hecate/v1/tasks/{id}/runs/{run_id}/continue`, which hydrates the saved conversation, appends the new user message, and starts the next run.

The gateway remains the source of truth. The bridge does not invent runtime
state that Hecate did not emit.

This is separate from Agent Chat persistence. Chats that run Codex/Claude/Cursor
from inside Hecate persist their own transcript and native ACP session id in
the Agent Chat store. See [External agent adapters](external-agent-adapters.md)
for that behavior.

## Configuration

| Variable | Default | Meaning |
|---|---:|---|
| `HECATE_GATEWAY_URL` | auto-discover, then `http://127.0.0.1:8765` | Gateway base URL the bridge talks to. Set explicitly to bypass runtime-state discovery. |
| `HECATE_AGENT_NAME` | `Hecate` | Agent display name advertised during initialize. |
| `HECATE_WORKSPACE_MODE` | `auto` | Workspace ownership. `auto` (default) follows ACP capability negotiation. `hecate-owned` forces the gateway host to own file writes and terminal execution. `editor-owned` requires the editor to own them via reverse-RPC and fails the handshake when the editor cannot. |
| `HECATE_APPROVAL_ROUTE` | `editor` | `editor` sends approval gates to ACP `session/request_permission`; other values leave approvals for the Hecate operator UI. |

`HECATE_WORKSPACE_MODE` resolves at `initialize` time. In `auto` mode the bridge
picks `editor-owned` when the editor declares `clientCapabilities.fs.readTextFile`,
`clientCapabilities.fs.writeTextFile`, **and** `clientCapabilities.terminal`;
otherwise it falls back to `hecate-owned`. Setting the variable to `editor-owned`
explicitly is the same check, but the bridge fails `initialize` instead of
silently falling back when those capabilities are missing — useful when an
operator wants to guarantee writes flow through the editor's review UI.

**Status:** capability negotiation is wired today, but the bridge↔gateway
transport that would route reverse-RPC end-to-end is not yet implemented. In
`editor-owned` mode the bridge negotiates the capability with the editor but
the orchestrator still uses the local workspace. The transport piece lands in
a follow-up RFC.

This is separate from [External agent adapters](external-agent-adapters.md),
where Hecate itself launches Codex / Claude / Cursor-style adapters from
Chats.

### Bridge telemetry

`hecate-acp` can export OTel traces independently from the gateway. It uses
`hecate-acp` as its default `service.name`, even when it inherits collector
endpoint settings from the gateway environment.

| Variable | Meaning |
|---|---|
| `HECATE_ACP_OTEL_TRACES_ENABLED` | Enable bridge trace export. Falls back to `GATEWAY_OTEL_TRACES_ENABLED`. |
| `HECATE_ACP_OTEL_ENDPOINT` / `HECATE_ACP_OTEL_TRACES_ENDPOINT` | Shared or trace-specific collector endpoint. Falls back to `GATEWAY_OTEL_ENDPOINT` / `GATEWAY_OTEL_TRACES_ENDPOINT`. |
| `HECATE_ACP_OTEL_TRANSPORT` / `HECATE_ACP_OTEL_TRACES_TRANSPORT` | `http` or `grpc`. Falls back to the gateway OTel transport variables. |
| `HECATE_ACP_OTEL_HEADERS` / `HECATE_ACP_OTEL_TRACES_HEADERS` | Comma-separated `key=value` headers for OTLP export. |
| `HECATE_ACP_OTEL_SERVICE_NAME` | Optional service-name override. Defaults to `hecate-acp`. |

When enabled, the bridge emits `acp.rpc` spans for JSON-RPC calls and
`acp.gateway.request` spans for gateway HTTP calls. It also injects W3C
`traceparent` / `baggage` into gateway requests so editor ACP activity can be
stitched into Hecate gateway traces.

## Smoke test

Run the gateway, then send an initialize request to the bridge:

```sh
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"0.1","clientCapabilities":{"permissions":{}}}}' \
  | go run ./cmd/hecate-acp
```

The response should include `availableModels` populated from Hecate's
`/v1/models` endpoint.

For an editor-local test before registry packaging exists:

1. Start Hecate using one of the [gateway launch options](#gateway-launch-options).
2. Build or install `hecate-acp`.
3. Add it as a custom local ACP agent in Zed, JetBrains, VS Code, or Cursor
   using the examples above.
4. Start a new agent session and send a simple prompt such as `say hello`.
5. If the editor fails to start the bridge, run:

   ```sh
   /absolute/path/to/hecate-acp --version
   curl -s http://127.0.0.1:8765/healthz
   ```

The automated smoke lives in `e2e/acp-smoke.ts` and is wired into
`just verify`.

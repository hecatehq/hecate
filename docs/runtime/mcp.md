# MCP integration

Hecate participates in MCP (Model Context Protocol) on both sides:

1. **Hecate as MCP server** — exposes Hecate's task, chat-session, and observability surfaces to MCP-aware clients (Claude Desktop, Cursor, Zed). Operators control the agent runtime from inside their editor.
2. **Hecate as MCP client** — `agent_loop` tasks can configure external MCP servers whose tools become callable to the LLM alongside Hecate's built-ins. Lets an agent use, say, the GitHub MCP server (to open PRs) or the filesystem MCP server (typed file access) without baking those into Hecate itself.

Both sides speak [MCP spec](https://modelcontextprotocol.io/) `2025-11-25`.

## Contents

- [Hecate as MCP server](#hecate-as-mcp-server) — expose Hecate to Claude Desktop / Cursor / Zed
  - [What's available](#whats-available)
  - [Resources](#resources)
  - [Prompts](#prompts)
  - [Local scenarios and built-in presets](#local-scenarios-and-built-in-presets)
  - [Configure it](#configure-it)
  - [Verify it locally](#verify-it-locally)
  - [Behavior notes](#behavior-notes)
  - [Spec compliance](#spec-compliance)
- [Hecate as MCP client](#hecate-as-mcp-client) — call external MCP servers from `agent_loop` tasks
  - [Configuration](#configuration)
  - [Transports](#transports)
  - [Secrets in `env` and `headers`](#secrets-in-env-and-headers)
  - [Approval policy](#approval-policy)
  - [Tool namespacing](#tool-namespacing)
  - [Lifecycle and caching](#lifecycle-and-caching)
  - [Resource limits](#resource-limits)
  - [Shutdown](#shutdown)
  - [Error handling](#error-handling)
  - [End-to-end examples](#end-to-end-examples)

---

## Hecate as MCP server

The server runs as a subcommand of the `gateway` binary on stdio, talking back to a running gateway over its public REST API. Operators add it to their MCP client's config, and the agent runtime surfaces become callable from inside the editor.
When the gateway is started with `HECATE_RUNTIME_TOKEN`, set the same environment variable on the MCP server entry so it can send `X-Hecate-Runtime-Token` to `/hecate/v1/*`.

### What's available

Seven tools — four reads and three writes:

| Tool                       | Kind                            | Description                                                                                                                             |
| -------------------------- | ------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `list_tasks`               | read                            | Recent agent tasks: id, title, status, execution kind, step count                                                                       |
| `get_task_status`          | read                            | Detailed status of one task by id, including its latest run                                                                             |
| `summarize_recent_traffic` | read                            | Aggregated request stats: by-provider breakdown, error rate, avg latency                                                                |
| `search_traces`            | read                            | Search recent trace summaries by text, or fetch one exact trace by `request_id` with span/event details                                 |
| `create_task`              | write                           | Queue a new `agent_loop` task with optional title / working_directory / model / provider / budget. Returns the new task id              |
| `resolve_approval`         | write (destructive)             | Approve or reject a pending approval gate (pre-execution or mid-loop). Approve resumes; reject cancels the run with `approval rejected` |
| `cancel_run`               | write (destructive, idempotent) | Cancel an in-flight task run. Cooperative — the worker stops at the next safe checkpoint                                                |

Together the write tools turn the MCP surface into an operator-grade control plane: list tasks → see approvals → approve/reject → create new tasks → cancel runaway runs without leaving the editor.

### Resources

Hecate also exposes read-only MCP resources for clients that support attaching
server-provided context directly to a prompt:

| Resource / template            | MIME type          | Description                                                                                   |
| ------------------------------ | ------------------ | --------------------------------------------------------------------------------------------- |
| `hecate://tasks/recent`        | `application/json` | Recent task records, capped at 30, with status, execution kind, step count, and latest run id |
| `hecate://tasks/{task_id}`     | `application/json` | Detailed status for one task by id                                                            |
| `hecate://traces/recent`       | `application/json` | Recent trace summaries, capped at 100, with status, latency, trace id, and route metadata     |
| `hecate://traces/{request_id}` | `application/json` | Detailed trace spans and route metadata for one gateway request id                            |

The two exact `recent` resources are returned from `resources/list`; the
parameterized task and trace forms are advertised via
`resources/templates/list`. `resources/read` returns the same Hecate-native
`{data: ...}` envelope shape operators see over HTTP, formatted as JSON text.

### Prompts

MCP clients that render server prompts as slash commands can use these
workflow templates:

| Prompt              | Arguments                                | Description                                                              |
| ------------------- | ---------------------------------------- | ------------------------------------------------------------------------ |
| `create_agent_task` | `prompt` (required), `working_directory` | Guides the client to queue a new `agent_loop` task with `create_task`    |
| `investigate_task`  | `task_id` (required)                     | Inspect one task and summarize state, latest run, approvals, or failures |
| `investigate_trace` | `request_id` (required)                  | Inspect one trace and explain routing, latency, status, and span clues   |
| `operator_briefing` | none                                     | Produce a short handoff from recent tasks and recent traffic             |

Streamable HTTP transport for the server side is tracked on the roadmap. The client-side direction — Hecate consuming external MCP servers — is shipped; see ["Hecate as MCP client"](#hecate-as-mcp-client) below.

### Local scenarios and built-in presets

The MCP server is a local operator surface. It is useful when an MCP-aware
assistant should see or control Hecate without bypassing Hecate's own runtime,
approval, and audit model.

Hecate should ship a small set of built-in local MCP toolset presets as agent
profile support matures. These presets are templates for which Hecate tools are
exposed through the MCP server; after a preset is applied, the resolved profile
configuration should be persisted like any other profile setting.

| Preset          | Scenario                                | Tool exposure                                                                                                                  | Security posture                                                                                    |
| --------------- | --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| `readonly`      | Let an editor assistant inspect Hecate. | Read-only task status, traffic summary, system health, recent errors, and workspace/Git context.                               | Safe default. No state-changing tools.                                                              |
| `operator`      | Run Hecate from an editor.              | Current control-plane surface: list/get tasks, search traces, summarize traffic, create tasks, resolve approvals, cancel runs. | Local stdio only. Writes and destructive tools rely on MCP annotations and Hecate approval gates.   |
| `observability` | Investigate runtime behavior.           | Traffic, traces, recent errors, provider health, queue depth, and per-run diagnostics.                                         | Read-heavy. Trace bodies and request metadata must follow Hecate's redaction settings.              |
| `security`      | Review local safety posture.            | Security warnings, grants, credential/config health, bootstrap-key health, bind-address checks, revoke grants.                 | Read-heavy with narrowly guarded writes. Revocation is destructive and should require confirmation. |
| `support`       | Prepare a bug report or support bundle. | Version info, redacted config summary, recent errors, adapter/provider probes, redacted support bundle.                        | Must redact secrets and sensitive request bodies before returning or writing bundle contents.       |

The server currently behaves closest to `operator`: all seven initial tools are
registered, with MCP annotations distinguishing read-only, write, destructive,
and idempotent operations. Preset-scoped tool selection is roadmap work.

Remote or Streamable HTTP MCP server mode should wait for authentication,
client identity, per-client capability scoping, and audit events. Until then,
the intended deployment model is local stdio inside the operator boundary.

#### Behavioral hints

Each tool declares MCP `annotations` so clients know whether to auto-approve invocations:

- Reads have `readOnlyHint: true` — auto-approve safe.
- `create_task` has no destructive hint (creating new state ≠ destroying existing).
- `resolve_approval` has `destructiveHint: true` — irreversible decision, expect the client to prompt.
- `cancel_run` has `destructiveHint: true, idempotentHint: true` — destructive but safe to retry.

### Configure it

The MCP server is a stdio subprocess. One environment variable controls where it talks:

| Variable          | Default                 | Notes                             |
| ----------------- | ----------------------- | --------------------------------- |
| `HECATE_BASE_URL` | `http://127.0.0.1:8765` | URL of the running Hecate gateway |

#### Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or the Windows / Linux equivalent:

```json
{
  "mcpServers": {
    "hecate": {
      "command": "hecate",
      "args": ["mcp", "serve"],
      "env": {
        "HECATE_BASE_URL": "http://127.0.0.1:8765"
      }
    }
  }
}
```

Restart Claude Desktop. The connector should appear in the tools menu; mention `@hecate` in a conversation to invoke a tool.

#### Cursor / Zed / other MCP clients

Same shape. Cursor's `~/.cursor/mcp.json` and Zed's MCP settings both accept the `command` / `args` / `env` format above.

### Verify it locally

A quick smoke test without an MCP client:

```bash
printf '%s\n%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"smoke","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | hecate mcp serve
```

Expected output: two JSON-RPC responses on stdout (initialize result + tools list). The startup line `hecate mcp serve: started on stdio, talking to ...` goes to stderr, which the protocol channel ignores.

### Behavior notes

- **Tool errors are not protocol errors.** When the upstream gateway is unreachable or returns a 5xx, the tool's `CallToolResult` carries `isError: true` with the error text in the content block. The MCP envelope itself stays a successful JSON-RPC response — that's what the spec requires, and it's also what clients render meaningfully.
- **Pure-Go hecate subcommand.** The MCP server has no extra dependencies; it runs from the hecate binary you already have, dispatched by the first arg.

### Spec compliance

- **Protocol version**: `2025-11-25` (current MCP revision). We track the breaking-change-free surface and adopt the additive bits that improve client UX (`title`, `annotations`, server `description`, input-validation-as-tool-error). Negotiation downgrades to whatever the client speaks.
- **Transport**: stdio with newline-delimited JSON-RPC 2.0 messages. Streamable HTTP is on the roadmap.
- **Capabilities declared**: `tools`, `resources`, and `prompts`. Sampling, elicitation, and the new task primitive land later.

---

## Hecate as MCP client

> The other half of the MCP integration: external MCP servers as tool sources for `agent_loop` tasks. The section above (Hecate as MCP server) is independent — read either standalone.

An `agent_loop` task configures one or more external MCP servers, the agent loop brings them up at run start, and their tools become callable by the LLM alongside Hecate's built-ins (`shell_exec`, `git_exec`, `file_write`, `file_edit`, `read_file`, `list_dir`, `http_request`).

A server vending tool `read_file` under the operator-chosen alias `filesystem` shows up to the LLM as `mcp__filesystem__read_file`. The double-underscore is the namespace separator; the LLM picks the namespaced name and Hecate routes the call back to the right upstream.

### Configuration

External servers live on the task's `mcp_servers` field at create time:

```json
POST /hecate/v1/tasks
{
  "execution_kind": "agent_loop",
  "prompt": "Read README.md and summarize.",
  "mcp_servers": [
    {
      "name": "fs",
      "command": "bunx",
      "args": ["--bun", "@modelcontextprotocol/server-filesystem", "/workspace"],
      "approval_policy": "auto"
    }
  ]
}
```

`name` is the operator-chosen alias used to namespace the server's tools. Each server entry produces one MCP client; the agent loop hands the LLM a merged tool catalog (built-ins + every server's tools) on every turn.

The same shape is reachable from the UI under "New task → Agent loop → MCP SERVERS → Add MCP server".

### Transports

Each entry uses **exactly one** transport. The gateway rejects configs that set both or neither.

| Transport | Required  | Optional      |
| --------- | --------- | ------------- |
| **stdio** | `command` | `args`, `env` |
| **HTTP**  | `url`     | `headers`     |

**stdio** — for local servers (`npx`, `bunx`, `uvx`, a binary on PATH):

```json
{
  "name": "fs",
  "command": "bunx",
  "args": ["--bun", "@modelcontextprotocol/server-filesystem", "/workspace"]
}
```

**HTTP** — for remote / cloud MCP servers, using the [Streamable HTTP](https://spec.modelcontextprotocol.io/specification/basic/transports/) protocol. Hecate handles both `application/json` (single response) and `text/event-stream` (SSE, multi-frame) responses, and threads the `Mcp-Session-Id` header across requests.

```json
{
  "name": "github",
  "url": "https://api.example.com/mcp",
  "headers": { "Authorization": "Bearer $GITHUB_TOKEN" }
}
```

### Secrets in `env` and `headers`

Values in `env` (stdio) and `headers` (HTTP) are stored in one of three forms. Same rules apply to both:

| Form                  | Example            | Behavior                                                                                                                                                   |
| --------------------- | ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Process-env reference | `$GITHUB_TOKEN`    | Resolved from Hecate's process environment at subprocess spawn time. The reference is what's stored on the task; the token itself never hits the database. |
| Encrypted literal     | `enc:<base64>`     | AES-GCM encrypted with the startup-resolved control-plane key. Decrypted at spawn time.                                                                    |
| Bare literal          | `secret-token-xyz` | Stored as-is. Acceptable for non-secret values.                                                                                                            |

Behavior of the API layer:

- **On create**: any value that is NOT a `$VAR_NAME` reference and NOT already `enc:<base64>` gets auto-encrypted to `enc:...` if a settings encryption key is configured, or stored bare if not.
- **On render** (`GET /hecate/v1/tasks/...`): `$VAR_NAME` values come back verbatim; everything else (encrypted ciphertext, bare literals) is replaced with `[redacted]`. Stored secrets cannot leak through the task API.

The control-plane key comes from `HECATE_CONTROL_PLANE_SECRET_KEY` when set,
or from the bootstrap key file otherwise. See
[`security.md`](../operator/security.md#bootstrap-key-today) for the local storage model.

If a value arrives as `enc:...` and no settings encryption key is configured, the run fails fast at spawn time with a clear error rather than forwarding ciphertext to the subprocess.

Stdio MCP subprocesses inherit only runtime essentials from the Hecate process
environment (`PATH`, home/temp/locale/user variables, Windows app-data paths,
custom CA bundle paths, and common toolchain shims). Provider keys, Hecate
bootstrap/control-plane secrets, telemetry headers, proxy variables, and other
gateway-scoped secrets are not inherited implicitly. Put any server-specific
credential or proxy setting in that server's `env` map as a `$VAR_NAME`
reference or encrypted literal so the boundary is explicit.

### MCP Apps extension

When Hecate connects to an external MCP server, its initialize request advertises
the MCP Apps extension:

```json
{
  "capabilities": {
    "extensions": {
      "io.modelcontextprotocol/ui": {
        "mimeTypes": ["text/html;profile=mcp-app"]
      }
    }
  }
}
```

That lets servers expose tools with `_meta.ui.resourceUri` links to `ui://`
resources and `_meta.ui.visibility` declarations. `ui://` is the MCP Apps
resource URI scheme: it is not fetched by the browser or the model directly.
Hecate resolves it through MCP `resources/read`, captures the returned
`text/html;profile=mcp-app` resource, and then renders that HTML inside the
local chat host. Hecate preserves the raw `_meta` object, `structuredContent`,
tool-result `_meta`, resource descriptor `_meta`, and resource-content `_meta`
in its MCP client layer. The dry-run probe returns the raw `_meta` plus
`ui_resource_uri`, `ui_visibility`, and `model_visible` so operators can inspect
MCP Apps-aware servers before using them in a task.

Agent-loop model exposure follows the MCP Apps visibility rule:

- Missing visibility defaults to model-visible.
- Tools whose `_meta.ui.visibility` omits `"model"` stay visible in probe output
  but are hidden from the LLM tool catalog and cannot be dispatched by the
  agent loop.
- The deprecated flat `_meta["ui/resourceUri"]` field is still recognized when
  deriving `ui_resource_uri`.

When an agent-loop tool call uses a model-visible MCP Apps tool, Hecate calls
`resources/read` for the tool's `ui_resource_uri`, stores the returned HTML
resource on the tool activity, and renders it inline in Hecate Chat as a
sandboxed iframe. The chat host injects a restrictive CSP from
`resource._meta.ui.csp`, performs the Apps JSON-RPC initialization handshake,
and sends the captured tool arguments plus `CallToolResult` via
`ui/notifications/tool-input` and `ui/notifications/tool-result`.

The first renderer is intentionally read-mostly: apps can `ping`, read the
embedded resource, report their preferred size, and receive the initial
tool-input/tool-result payloads. Follow-up `tools/call` requests from historical
chat views are rejected with a clear unsupported error because the per-run MCP
server connection may already be closed by the time an operator reopens the
transcript.

#### Try the local app demo in Chat

The repo includes a dependency-free stdio MCP Apps server at
`examples/mcp-weather-app-server.mjs`. It exposes one `show_weather` tool with
`_meta.ui.resourceUri = "ui://demo/weather"` and returns a tiny
`text/html;profile=mcp-app` weather dashboard resource.

The Chat composer does not yet have MCP server rows, so send a Hecate Chat turn
through the API. Replace `SESSION_ID` with a Hecate Chat session ID, and add
`X-Hecate-Runtime-Token` if your local runtime requires it:

```bash
curl -sS \
  -H 'content-type: application/json' \
  -X POST http://127.0.0.1:8765/hecate/v1/chat/sessions/SESSION_ID/messages \
  -d '{
    "execution_mode": "hecate_task",
    "tools_enabled": true,
    "workspace": "/absolute/path/to/a/workspace",
    "provider": "openai",
    "model": "gpt-4o-mini",
    "content": "Call mcp__weather__show_weather with location Barcelona, then show the result.",
    "mcp_servers": [
      {
        "name": "weather",
        "command": "node",
        "args": ["/absolute/path/to/hecate/examples/mcp-weather-app-server.mjs"]
      }
    ]
  }'
```

When the model calls the tool, the Chat transcript should show the weather app
frame directly in the assistant message body. The compact activity row below it
is still useful audit metadata (`completed · 1 tool`), but the app itself should
not require expanding a disclosure.

#### Test MCP Apps support

Useful focused checks while developing MCP Apps support:

```bash
go test ./internal/mcp/... ./internal/orchestrator ./internal/api ./internal/taskapp
cd ui && bun run test -- src/features/transcript/TranscriptMessageRow.test.tsx
```

The Go tests cover initialize capabilities, tool `_meta` passthrough,
`structuredContent`, `resources/read`, visibility filtering, app-resource
capture on tool calls, registry discovery, and chat activity projection. The UI
test pins inline rendering, sandbox attributes, the Apps JSON-RPC host bridge,
tool-input/tool-result delivery, iframe resizing, CSP source filtering, and the
no-empty-iframe error fallback.

### Approval policy

`approval_policy` gates how tool calls dispatch. Per-server, not per-tool.

| Value                        | Behavior                                                                                                                                                                                                                                                                                                                  |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `auto` (default — omittable) | Tool calls dispatch immediately.                                                                                                                                                                                                                                                                                          |
| `require_approval`           | Every tool call to this server pauses the agent loop. The run goes to `awaiting_approval` with a pending approval record; the operator approves or rejects via `POST /hecate/v1/tasks/{id}/approvals/{approval_id}/resolve`; the same run resumes from the saved conversation and dispatches the previously-pending call. |
| `block`                      | Never dispatch. The agent loop returns a tool error to the LLM ("blocked by policy") so the model picks a different path on the next turn. Distinct from `require_approval` — block is a hard refusal, not a pause. The run does NOT go to `awaiting_approval`.                                                           |

The pause-and-resume machinery is the same the gateway already uses for built-in `shell_exec` gating; MCP gating reuses it without changing the runner or resume path.

Per-tool granularity (e.g. allow read tools on a server while gating write tools) is on the roadmap; for now, gate the whole server or split your task across multiple server entries with different policies.

### Tool namespacing

A server named `github` vending `create_pr` surfaces as `mcp__github__create_pr` in the tool catalog. The split is `mcp__<server>__<tool>`. Tool names that themselves contain `__` round-trip correctly: `mcp__weird__double__under` parses as server=`weird`, tool=`double__under` — only the FIRST `__` after the server segment is treated as the separator.

### Lifecycle and caching

Hecate maintains a shared client cache so multiple tasks targeting the same upstream pay the spawn cost once.

- **Cache key**: SHA-256 over the resolved transport fields (`Command`+`Args`+`Env` for stdio, `URL`+`Headers` for HTTP) AFTER secret resolution. The operator-chosen `name` is intentionally NOT in the key — two tasks aliasing the same upstream as `fs` and `filesystem` share one subprocess.
- **Idle TTL**: 5 minutes. Entries with refcount=0 evict after this; in-use entries are never evicted regardless of age.
- **Reaper interval**: 30 seconds.
- **Reactive eviction**: a transport-closed error from a tool call drops the entry so the next task respawns, instead of being handed back the same dead client.
- **Proactive health check**: a background loop pings each idle cached client every `HECATE_TASK_MCP_CLIENT_CACHE_PING_INTERVAL` (default 60s, `0` disables). Failure or `HECATE_TASK_MCP_CLIENT_CACHE_PING_TIMEOUT` (default 5s) deadline-exceeded evicts the entry. Catches wedged subprocesses (event-loop deadlock, tight CPU loop) before the next real tool call hits the wall — reactive eviction only fires AFTER a call has already failed. In-use entries are never pinged (the active call's response would race with the ping reply on the same channel).
- **Max-entries soft cap**: 256 by default (`HECATE_TASK_MCP_CLIENT_CACHE_MAX_ENTRIES`). On a fresh insert that would push the cache over the cap, the least-recently-used IDLE entry is evicted first. If every entry is in-use, the over-cap insert is allowed (rejecting an Acquire would break a legitimate run; TTL eviction catches up once anything goes idle). Set to `0` to disable the cap.
- **Live snapshot**: `GET /hecate/v1/system/mcp/cache` returns the current `{entries, in_use, idle}` plus a `configured` boolean (false on deploys without a cache). Surfaced on the observability view alongside queue depth and worker count.
- **HTTP-transport seam**: every HTTP MCP client the cache spawns shares one `*http.Client`. Default-constructed (5-minute timeout, Go's `http.DefaultTransport`) but overridable in code via `SharedClientCacheOptions.HTTPClient` for deploys that need a corporate proxy, mTLS, alternate `DialContext`, or different timeouts. Stdio servers ignore this; uncached pools (built via `NewPool` rather than `NewPoolWithCache`) keep the prior per-transport default.
- **Dry-run probe**: `POST /hecate/v1/mcp/probe` brings a single MCP server up exactly the way a task would, calls `tools/list`, and returns the catalog without touching the cache. Lets operators confirm a config (and discover what tools an upstream vends) before committing it to a task. See [`runtime-api.md`](runtime-api.md#runtime-backend-and-queue-configuration) for the request/response shape.

### Registry discovery

`GET /hecate/v1/mcp/registry/servers` queries the read-only MCP Registry REST API. It defaults to `https://registry.modelcontextprotocol.io` and forwards the registry's list/search filters: `search`, `cursor`, `limit`, `updated_since`, `version`, and `include_deleted`. `registry_url` can point at a private registry, but the endpoint is local-only: non-loopback sockets and forwarded-client headers are rejected before Hecate performs any outbound fetch.

The response keeps registry server metadata intact (`server`, `_meta`) and adds Hecate-specific `install_hints`. A `streamable-http` remote with a URL gets a ready-to-probe `hecate_config` containing `name`, `url`, and header placeholders such as `$MCP_AUTHORIZATION`. Package entries and legacy `sse` remotes remain visible for discovery, but Hecate does not treat them as one-click runnable configs. Operators still review the selected config and can use `POST /hecate/v1/mcp/probe` to inspect the actual tool catalog before adding it to a task.

Registry discovery is API-only for the initial MCP Apps PR. The UI follow-up
should present registry search results, install hints, required secret
placeholders, and a probe-before-use review step together instead of silently
adding servers to Chat.

### Resource limits

| Knob                                       | Default | What it does                                                                                                                                                                                                                    |
| ------------------------------------------ | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `HECATE_TASK_MAX_MCP_SERVERS_PER_TASK`     | `16`    | Per-task cap on `mcp_servers` entries. The gateway rejects creates that exceed this with a 400 — protects the worker from a single misconfigured task spawning hundreds of subprocesses before failing. `0` disables the check. |
| `HECATE_TASK_MCP_CLIENT_CACHE_MAX_ENTRIES` | `256`   | Gateway-wide soft cap on cached MCP clients. See "Lifecycle and caching" above for the LRU-idle eviction contract. `0` disables.                                                                                                |

### Shutdown

On `SIGTERM`/`SIGINT` the gateway runs a 10-second graceful shutdown:

1. The runner cancels every in-flight agent loop. Each loop's `defer host.Close()` releases its cached clients back to the cache.
2. The cache closes every cached `Client`, which tears down stdio subprocesses (cooperative on EOF) and HTTP connections.
3. The HTTP server drains pending requests and exits.

Order matters and is enforced by the handler: runner first, cache second. Closing the cache before the runner drained would yank live subprocesses out from under in-flight runs.

### Error handling

| Failure                                                         | What you see                                                                                                                                                                           |
| --------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Subprocess can't spawn (`npx not found`, exec error)            | Run fails at start. `last_error` carries the spawn diagnostic.                                                                                                                         |
| `initialize` handshake fails                                    | Run fails at start. For stdio servers, the error message includes the captured stderr — usually pinpoints missing deps, bad args, or auth failures the upstream prints before exiting. |
| Tool call returns `isError: true`                               | The agent loop forwards the upstream's error text as a tool message with `is_error: true`. The LLM gets a chance to retry or pick a different tool; the run does NOT fail.             |
| Transport closed mid-run (subprocess died, HTTP server hung up) | The cache evicts the entry; the call returns a transport error to the loop, which surfaces it as a tool error on the next turn. The next task respawns.                                |
| `enc:` value cannot be decrypted with the startup-resolved key  | Run fails fast at spawn time with a clear error.                                                                                                                                       |

Bring-up (initialize + tools/list) gets one bounded retry with a 500ms backoff, rebuilding the transport from scratch between attempts. Absorbs ordinary flakiness — slow-booting subprocess, brief network blip, transient 5xx — without hiding real failures: a permanent broken config (missing binary, bad args, auth rejected) fails twice and surfaces the same diagnostic, just delayed by ~500ms. Cancellation aborts the retry promptly rather than waiting out the backoff, so runner shutdown stays responsive.

### End-to-end examples

**Filesystem server, no gating** — the simplest config:

```json
{
  "execution_kind": "agent_loop",
  "prompt": "Read README.md and summarize.",
  "mcp_servers": [
    {
      "name": "fs",
      "command": "bunx",
      "args": ["--bun", "@modelcontextprotocol/server-filesystem", "/workspace"]
    }
  ]
}
```

**HTTP server with bearer token, gated** — the operator approves every call:

```json
{
  "execution_kind": "agent_loop",
  "prompt": "Open a PR for branch feat/x.",
  "mcp_servers": [
    {
      "name": "github",
      "url": "https://api.example.com/mcp",
      "headers": { "Authorization": "Bearer $GITHUB_TOKEN" },
      "approval_policy": "require_approval"
    }
  ]
}
```

The `$GITHUB_TOKEN` reference resolves from Hecate's process environment at spawn time; set it in your deployment's secret manager. `require_approval` ensures the operator sees and OKs every PR-create call before it lands.

**Two servers, mixed gating** — auto-allow the read-only filesystem, gate the destructive github surface:

```json
{
  "execution_kind": "agent_loop",
  "prompt": "Read CHANGELOG.md, then open a PR if today's section is empty.",
  "mcp_servers": [
    {
      "name": "fs",
      "command": "bunx",
      "args": ["--bun", "@modelcontextprotocol/server-filesystem", "/workspace"]
    },
    {
      "name": "github",
      "url": "https://api.example.com/mcp",
      "headers": { "Authorization": "Bearer $GITHUB_TOKEN" },
      "approval_policy": "require_approval"
    }
  ]
}
```

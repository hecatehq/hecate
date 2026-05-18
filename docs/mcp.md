# MCP integration

Hecate participates in MCP (Model Context Protocol) on both sides:

1. **Hecate as MCP server** — exposes Hecate's task, chat-session, and observability surfaces to MCP-aware clients (Claude Desktop, Cursor, Zed). Operators control the agent runtime from inside their editor.
2. **Hecate as MCP client** — `agent_loop` tasks can configure external MCP servers whose tools become callable to the LLM alongside Hecate's built-ins. Lets an agent use, say, the GitHub MCP server (to open PRs) or the filesystem MCP server (typed file access) without baking those into Hecate itself.

Both sides speak [MCP spec](https://modelcontextprotocol.io/) `2025-11-25`.

## Contents

- [Hecate as MCP server](#hecate-as-mcp-server) — expose Hecate to Claude Desktop / Cursor / Zed
  - [What's available](#whats-available)
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

### What's available

Six tools — three reads and three writes:

| Tool                       | Kind                            | Description                                                                                                                 |
| -------------------------- | ------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| `list_tasks`               | read                            | Recent agent tasks: id, title, status, execution kind, step count                                                           |
| `get_task_status`          | read                            | Detailed status of one task by id, including its latest run                                                                 |
| `summarize_recent_traffic` | read                            | Aggregated request stats: by-provider breakdown, error rate, avg latency                                                    |
| `create_task`              | write                           | Queue a new `agent_loop` task with optional title / working_directory / model / provider / budget. Returns the new task id  |
| `resolve_approval`         | write (destructive)             | Approve or reject a pending approval gate (pre-execution or mid-loop). Approve resumes; reject terminates the run as failed |
| `cancel_run`               | write (destructive, idempotent) | Cancel an in-flight task run. Cooperative — the worker stops at the next safe checkpoint                                    |

Together the write tools turn the MCP surface into an operator-grade control plane: list tasks → see approvals → approve/reject → create new tasks → cancel runaway runs without leaving the editor.

`search_traces` and Streamable HTTP transport for the server side are tracked on the roadmap. The client-side direction — Hecate consuming external MCP servers — is shipped; see ["Hecate as MCP client"](#hecate-as-mcp-client) below.

### Local scenarios and built-in presets

The MCP server is a local operator surface. It is useful when an MCP-aware
assistant should see or control Hecate without bypassing Hecate's own runtime,
approval, and audit model.

Hecate should ship a small set of built-in local MCP toolset presets as agent
profile support matures. These presets are templates for which Hecate tools are
exposed through the MCP server; after a preset is applied, the resolved profile
configuration should be persisted like any other profile setting.

| Preset          | Scenario                                | Tool exposure                                                                                                   | Security posture                                                                                    |
| --------------- | --------------------------------------- | --------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| `readonly`      | Let an editor assistant inspect Hecate. | Read-only task status, traffic summary, system health, recent errors, and workspace/Git context.                | Safe default. No state-changing tools.                                                              |
| `operator`      | Run Hecate from an editor.              | Current control-plane surface: list/get tasks, summarize traffic, create tasks, resolve approvals, cancel runs. | Local stdio only. Writes and destructive tools rely on MCP annotations and Hecate approval gates.   |
| `observability` | Investigate runtime behavior.           | Traffic, traces, recent errors, provider health, queue depth, and per-run diagnostics.                          | Read-heavy. Trace bodies and request metadata must follow Hecate's redaction settings.              |
| `security`      | Review local safety posture.            | Security warnings, grants, credential/config health, bootstrap-key health, bind-address checks, revoke grants.  | Read-heavy with narrowly guarded writes. Revocation is destructive and should require confirmation. |
| `support`       | Prepare a bug report or support bundle. | Version info, redacted config summary, recent errors, adapter/provider probes, redacted support bundle.         | Must redact secrets and sensitive request bodies before returning or writing bundle contents.       |

The server currently behaves closest to `operator`: all six initial tools are
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
      "args": ["mcp-server"],
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
  | hecate mcp-server
```

Expected output: two JSON-RPC responses on stdout (initialize result + tools list). The startup line `hecate mcp-server: started on stdio, talking to ...` goes to stderr, which the protocol channel ignores.

### Behavior notes

- **Tool errors are not protocol errors.** When the upstream gateway is unreachable or returns a 5xx, the tool's `CallToolResult` carries `isError: true` with the error text in the content block. The MCP envelope itself stays a successful JSON-RPC response — that's what the spec requires, and it's also what clients render meaningfully.
- **Pure-Go hecate subcommand.** The MCP server has no extra dependencies; it runs from the hecate binary you already have, dispatched by the first arg.

### Spec compliance

- **Protocol version**: `2025-11-25` (current MCP revision). We track the breaking-change-free surface and adopt the additive bits that improve client UX (`title`, `annotations`, server `description`, input-validation-as-tool-error). Negotiation downgrades to whatever the client speaks.
- **Transport**: stdio with newline-delimited JSON-RPC 2.0 messages. Streamable HTTP is on the roadmap.
- **Capabilities declared**: `tools` only. Resources, prompts, sampling, elicitation, and the new task primitive land later.

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

The control-plane key comes from `GATEWAY_CONTROL_PLANE_SECRET_KEY` when set,
or from the bootstrap key file otherwise. See
[`security.md`](security.md#bootstrap-key-today) for the local storage model.

If a value arrives as `enc:...` and no settings encryption key is configured, the run fails fast at spawn time with a clear error rather than forwarding ciphertext to the subprocess.

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
- **Proactive health check**: a background loop pings each idle cached client every `GATEWAY_TASK_MCP_CLIENT_CACHE_PING_INTERVAL` (default 60s, `0` disables). Failure or `GATEWAY_TASK_MCP_CLIENT_CACHE_PING_TIMEOUT` (default 5s) deadline-exceeded evicts the entry. Catches wedged subprocesses (event-loop deadlock, tight CPU loop) before the next real tool call hits the wall — reactive eviction only fires AFTER a call has already failed. In-use entries are never pinged (the active call's response would race with the ping reply on the same channel).
- **Max-entries soft cap**: 256 by default (`GATEWAY_TASK_MCP_CLIENT_CACHE_MAX_ENTRIES`). On a fresh insert that would push the cache over the cap, the least-recently-used IDLE entry is evicted first. If every entry is in-use, the over-cap insert is allowed (rejecting an Acquire would break a legitimate run; TTL eviction catches up once anything goes idle). Set to `0` to disable the cap.
- **Live snapshot**: `GET /hecate/v1/system/mcp/cache` returns the current `{entries, in_use, idle}` plus a `configured` boolean (false on deploys without a cache). Surfaced on the observability view alongside queue depth and worker count.
- **HTTP-transport seam**: every HTTP MCP client the cache spawns shares one `*http.Client`. Default-constructed (5-minute timeout, Go's `http.DefaultTransport`) but overridable in code via `SharedClientCacheOptions.HTTPClient` for deploys that need a corporate proxy, mTLS, alternate `DialContext`, or different timeouts. Stdio servers ignore this; uncached pools (built via `NewPool` rather than `NewPoolWithCache`) keep the prior per-transport default.
- **Dry-run probe**: `POST /hecate/v1/mcp/probe` brings a single MCP server up exactly the way a task would, calls `tools/list`, and returns the catalog without touching the cache. Lets operators confirm a config (and discover what tools an upstream vends) before committing it to a task. See [`runtime-api.md`](runtime-api.md#runtime-backend-and-queue-configuration) for the request/response shape.

### Resource limits

| Knob                                        | Default | What it does                                                                                                                                                                                                                    |
| ------------------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GATEWAY_TASK_MAX_MCP_SERVERS_PER_TASK`     | `16`    | Per-task cap on `mcp_servers` entries. The gateway rejects creates that exceed this with a 400 — protects the worker from a single misconfigured task spawning hundreds of subprocesses before failing. `0` disables the check. |
| `GATEWAY_TASK_MCP_CLIENT_CACHE_MAX_ENTRIES` | `256`   | Gateway-wide soft cap on cached MCP clients. See "Lifecycle and caching" above for the LRU-idle eviction contract. `0` disables.                                                                                                |

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

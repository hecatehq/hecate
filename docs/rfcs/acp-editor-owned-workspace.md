# ACP editor-owned workspace transport

> **Status:** proposed; not implemented.
> **Current source of truth:** [ACP bridge](../acp.md) for today's bridge
> behavior and `workspace.Workspace` / `ACPWorkspace` for the shipped
> workspace abstraction.
> **Next action:** revisit after terminal distribution and ACP bridge packaging
> decisions settle.

## The problem

The orchestrator runs in the main `hecate` process. The ACP
dispatcher runs in the `hecate-acp` bridge process — spawned by the
editor over stdio, talking HTTP REST to the gateway for forward calls
(`POST /hecate/v1/tasks/...`). When `HECATE_WORKSPACE_MODE` resolves to
`editor-owned`, the orchestrator must call `fs/write_text_file`,
`terminal/create`, and friends on the editor — but those reverse-RPC
calls have to originate from the gateway and arrive at the bridge,
which currently has no inbound channel from the gateway. HTTP REST
goes one way; there is no return path.

A naive fix is to add a return path (a WebSocket per session, an SSE
stream, etc.) so the gateway can push reverse-RPC envelopes into the
bridge, which then writes them onto the editor's stdio. That works,
but it cuts the boundary _through_ the ACP protocol stack: the
dispatcher and the workspace that consumes its `Call` end up on
opposite sides of a process boundary, talking through three protocols
(stdio JSON-RPC, REST, WebSocket JSON-RPC). Adding any new
reverse-RPC method touches two processes.

This RFC proposes the inversion: move the ACP dispatcher into the
gateway, make `hecate-acp` a stdio↔socket relay, and let the
orchestrator's `ACPWorkspace` call `Dispatcher.Call` as an in-process
function call.

## Goals

In rough priority order:

1. **End-to-end editor-owned workspaces work.** When an ACP session
   resolves to `editor-owned`, the orchestrator's `fs/*` and
   `terminal/*` traffic actually reaches the editor's filesystem
   and terminal panes.
2. **ACP protocol logic has exactly one home.** Today it's split
   between `internal/acp/dispatcher.go` (bridge) and
   `internal/workspace/acp.go` (gateway-orchestrator). After this RFC,
   both live in the gateway.
3. **`hecate-acp` becomes a thin relay.** ~100 LOC of stdio↔socket
   plumbing. No dispatcher, no `GatewayClient`, no model discovery —
   those move into the gateway, where they belong.
4. **No regression in the local path.** `HECATE_WORKSPACE_MODE=auto`
   without editor capabilities still resolves to `hecate-owned` and
   uses the local workspace exactly as today.
5. **"Gateway is source of truth" gets stronger.** The session state,
   the dispatcher, the orchestrator, and the workspace all coexist in
   one process. The bridge is just a connector.

## Non-goals for v1

- **Multiple editors per gateway.** Each relay connection still owns
  one ACP session; the gateway accepts multiple connections, but
  cross-session features (shared task pools, multi-editor approval
  routing) are out of scope.
- **Durable editor-side session reattachment.** Already deferred in
  [docs/acp.md](../acp.md); not addressed by this RFC.
- **Editor-owned mode for non-ACP runtimes.** External adapters
  (Codex, Claude Code, Cursor) keep their existing subprocess model.
- **A new auth model for the gateway.** This RFC reuses whatever auth
  the gateway already enforces for its existing routes.

## Architectural overview

### Today

```
   ┌──────────────────────┐
   │      Editor (Zed)    │
   └──────────┬───────────┘
              │  ACP JSON-RPC (stdio)
              ▼
   ┌──────────────────────┐
   │  hecate-acp (bridge) │
   │  - Dispatcher        │
   │  - SessionStore      │
   │  - GatewayClient HTTP│
   └──────────┬───────────┘
              │  HTTP REST (POST /hecate/v1/tasks/...)
              ▼
   ┌──────────────────────┐
   │     hecate gateway   │
   │  - REST API          │
   │  - Orchestrator      │
   │  - LocalWorkspace    │
   │  - ACPWorkspace      │  ← exists but unreachable from
   └──────────────────────┘    the dispatcher
```

### After this RFC

```
   ┌──────────────────────┐
   │      Editor (Zed)    │
   └──────────┬───────────┘
              │  ACP JSON-RPC (stdio)
              ▼
   ┌──────────────────────┐
   │  hecate-acp (relay)  │
   │  stdin  ─► socket out│
   │  stdout ◄─ socket in │
   └──────────┬───────────┘
              │  raw byte stream (WebSocket on the gateway's HTTP port)
              ▼
   ┌──────────────────────────┐
   │       hecate gateway     │
   │  - REST API              │
   │  - ACP listener (new)    │
   │    └─► Dispatcher per    │
   │        accepted conn     │
   │  - Orchestrator          │
   │  - LocalWorkspace        │
   │  - ACPWorkspace ─────────┼─► Dispatcher.Call (in-proc)
   └──────────────────────────┘
```

The relay carries JSON-RPC envelope bytes verbatim; it never parses
them. The gateway-side dispatcher behaves exactly like the bridge's
dispatcher does today, except its inputs and outputs travel over the
relay socket instead of `os.Stdin` / `os.Stdout`.

## Design decisions

### Transport: WebSocket on the existing HTTP listener

The relay needs a bidirectional byte stream between the bridge and the
gateway, both on the same host. Three options:

| Option                                      | Pros                                                                                                                                                                                                      | Cons                                                                                                                                                      |
| ------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **WebSocket upgrade on existing HTTP port** | Same port, no new firewall surface, auth reuses the existing middleware, cross-platform with one Go library (`gorilla/websocket` or `nhooyr/websocket`), framing is "lines in / lines out" once upgraded. | One more transport on top of HTTP/1.1 keepalive; framing is per-message which we treat as per-line.                                                       |
| **Unix Domain Socket**                      | "Real" IPC, slightly lower overhead, no TLS concerns.                                                                                                                                                     | Windows AF_UNIX support is Windows-10+ but tooling is awkward; separate code path from existing HTTP server; needs a discovery file with the socket path. |
| **Raw TCP loopback on a separate port**     | Simplest framing.                                                                                                                                                                                         | Needs port discovery, separate firewall/listener, separate auth, easy to confuse with the REST port.                                                      |

**Choice: WebSocket on the existing HTTP listener.** New route
`POST /hecate/v1/acp/connect` (or similar — final path bikeshed
welcome) that upgrades. The bridge writes one ACP JSON-RPC envelope
per WS message; the gateway reads one envelope per message. No
intermediate framing protocol, no port discovery, no second listener
to bind and audit.

### Per-connection dispatcher

The current `acp.Dispatcher` is "one per bridge process" — it tracks
`initialized` as a single bool because there is exactly one ACP
session per process today. After this RFC, the gateway accepts
multiple relay connections, each carrying one ACP session.

**Choice: one `Dispatcher` per accepted WS connection.** The gateway
spawns a fresh `Dispatcher` when a relay connects, runs it for the
lifetime of that connection, and tears it down when the WS closes.
The dispatcher's existing internal state (pending permissions, pending
calls, session store) moves with it — each connection has its own.

This means session-store sharing across connections is _not_ a thing
in v1. If a relay disconnects and reconnects, the new connection
starts fresh. (Durable reattachment was a non-goal above.)

### In-process `GatewayClient`

Today `cmd/hecate-acp/main.go` defines a `gatewayHTTPClient` that
implements `acp.GatewayClient` against the REST API. After this RFC,
the dispatcher runs in the gateway and the REST hop is wasteful — we
should call the underlying services directly.

**Choice: define an in-process `GatewayClient` adapter in
`internal/api` (or a new `internal/acp/gateway/inproc.go`) that
implements the same interface but binds straight to the gateway's
task service, provider catalog, approval engine, and event bus.** The
bridge's HTTP implementation goes away entirely once the bridge is no
longer a separate ACP process.

The dispatcher continues to depend on the `GatewayClient` interface,
not the concrete adapter, so the existing tests keep their seams.

### Auth

The gateway already enforces auth on its routes (token, header, or
whatever the operator configures). The relay connection authenticates
through the same mechanism — typically a local token read from
`hecate.runtime.json` or `HECATE_GATEWAY_TOKEN` — attached as a header
during the WS upgrade. No new auth model.

For native-app installs the runtime json already lives next to the
gateway data dir; the bridge already reads it for URL discovery. We
just add a `token` field next to `base_url` and the bridge picks it up
during connect.

For loopback-only development (`just dev` without a configured token)
the gateway accepts the upgrade without a token, matching its existing
REST behavior.

### Lifecycle

- **Bridge starts** → opens stdio to the editor, opens WS to the
  gateway, starts two goroutines pumping bytes in each direction.
- **Editor sends** `initialize` → bytes flow through to the gateway,
  the gateway-side dispatcher processes it, response flows back
  through the same WS, bridge writes it to stdout.
- **Editor closes stdin or sends `session/cancel`** → bridge writes
  the close signal, gateway dispatcher tears down the session, WS
  closes.
- **Gateway shuts down** → WS closes, bridge sees the socket close,
  exits with a non-zero status so the editor knows the session ended.
- **WS drops mid-call** → gateway dispatcher tears down its state
  cleanly; any pending `Dispatcher.Call` returns
  `context.Canceled` or a connection-closed error to the orchestrator,
  which translates to a session/update error.

### Backward compatibility

The wire-level behavior the editor sees is identical to today:
newline-delimited JSON-RPC over stdio, same methods, same envelopes.
The editor configuration that runs `hecate-acp` over stdio does not
change. The default in the absence of `HECATE_WORKSPACE_MODE` stays
`auto`, which resolves to `hecate-owned` for editors that don't
advertise the required client capabilities — same as today.

## Phasing

Each phase is independently mergeable.

### Phase 1: gateway-side ACP listener

Add `POST /hecate/v1/acp/connect` (or chosen path) with WebSocket
upgrade. Add the in-process `GatewayClient` adapter. Add a per-
connection dispatcher runner that reads one envelope per WS message,
dispatches via the existing `acp.Dispatcher`, and writes responses
back as WS messages.

At the end of phase 1:

- The gateway exposes an ACP entrypoint over WS.
- The bridge is unchanged; nothing actually uses the new entrypoint.
- A new test (`internal/api/acp_websocket_test.go`) drives the
  listener end-to-end with a synthetic WS client.

### Phase 2: hecate-acp as relay

Delete the bridge-side dispatcher, session store, and HTTP gateway
client. Replace `cmd/hecate-acp/main.go` with a relay: WS dial,
bidirectional pumps, error and EOF handling. Drop the OTel tracing
hooks that span the bridge — they move to the gateway-side
dispatcher.

At the end of phase 2:

- The bridge is ~100 LOC.
- `internal/acp/dispatcher.go` and friends live entirely in the
  gateway's call graph.
- `docs/acp.md` updates to describe the new shape.

### Phase 3: wire `ACPWorkspace`

When a session resolves to `editor-owned`, the orchestrator picks
`ACPWorkspace` (already implemented) instead of `LocalWorkspace`,
parameterized with the connection's dispatcher as its `Caller`.

At the end of phase 3:

- `fs/write_text_file` and `terminal/*` reverse-RPC actually reaches
  the editor.
- E2E test against a fake editor that exercises one end-to-end file
  write via the orchestrator.

## Risks and open questions

- **WS framing semantics.** Are we one-envelope-per-message or one-
  envelope-per-line? Probably one-per-message — simpler, no scanner
  state. Document and pin it in phase 1.
- **Latency.** WS adds a small overhead vs raw stdio. Worth measuring
  in phase 2; if it's a concern, UDS becomes the fallback option.
- **Token rotation.** Long-lived WS connections vs short-lived
  tokens. Out of scope for v1; document the expected operator
  behavior (restart bridge after token rotation).
- **Multi-host setups.** Today the bridge always runs on the same
  host as the gateway (discovery code in `cmd/hecate-acp/main.go`
  assumes loopback). This RFC doesn't change that. Cross-host bridges
  remain a future RFC if there's demand.

## What this RFC does not commit to

- A final URL for the WS endpoint. `/hecate/v1/acp/connect` is the
  current candidate.
- A specific WebSocket library. `nhooyr/websocket` and
  `gorilla/websocket` both work; pick the one that matches the
  gateway's existing transitive deps.
- The shape of the in-process `GatewayClient` adapter beyond "it
  satisfies the existing interface."

The implementation phases are expected to refine all three.

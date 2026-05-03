# ACP bridge

Hecate has an early ACP bridge binary at `cmd/hecate-acp`. It starts a
newline-delimited JSON-RPC stdio loop, advertises gateway models during
`initialize`, creates coding-agent tasks from `session/prompt`, and forwards
run stream snapshots as `session/update` notifications.

## Current status

- Implemented: stdio JSON-RPC loop, parse/invalid-request responses,
  `initialize`, `session/new`, `session/prompt`, `session/cancel`,
  gateway model discovery, task creation/start, run cancellation, run-event
  stream mapping, optional `HECATE_API_KEY` / `HECATE_AUTH_TOKEN` forwarding.
- Not implemented yet: approval round-trip, editor-owned workspace calls,
  true multi-prompt continuation inside a single Hecate task.

For alpha, each `session/prompt` creates a fresh `coding_agent` task while
preserving the editor-facing ACP session ID. Hecate needs a dedicated
"append prompt to task conversation" runtime endpoint before ACP sessions can
map perfectly to one durable Hecate task.

## Configuration

| Variable | Default | Meaning |
|---|---:|---|
| `HECATE_GATEWAY_URL` | `http://127.0.0.1:8765` | Gateway base URL the bridge talks to. |
| `HECATE_API_KEY` | empty | Optional tenant API key sent as `x-api-key`. |
| `HECATE_AUTH_TOKEN` | empty | Optional bearer token for admin/operator deployments. |
| `HECATE_AGENT_NAME` | `Hecate` | Agent display name advertised during initialize. |
| `HECATE_WORKSPACE_MODE` | `hecate-owned` | Future workspace ownership mode. |
| `HECATE_APPROVAL_ROUTE` | `editor` | Future approval routing mode. |

## Smoke test

Run the gateway, then send an initialize request to the bridge:

```sh
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"0.1","clientCapabilities":{"permissions":{}}}}' \
  | go run ./cmd/hecate-acp
```

The response should include `availableModels` populated from Hecate's
`/v1/models` endpoint.

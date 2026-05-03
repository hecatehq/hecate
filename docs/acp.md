# ACP bridge

Hecate has an early ACP bridge binary at `cmd/hecate-acp`. It is intentionally
small right now: the bridge starts a newline-delimited JSON-RPC stdio loop,
handles `initialize`, calls the local Hecate gateway's `GET /v1/models`, and
advertises those models to an ACP-capable editor.

## Current status

- Implemented: stdio JSON-RPC loop, parse/invalid-request responses,
  `initialize`, gateway model discovery, optional `HECATE_API_KEY` /
  `HECATE_AUTH_TOKEN` forwarding.
- Not implemented yet: `session/new`, `session/prompt`, `session/cancel`,
  event streaming, approval round-trip, editor-owned workspace calls.

Session methods return structured JSON-RPC errors for now instead of silently
pretending to support agent execution.

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

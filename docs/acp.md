# ACP bridge

Hecate has an early ACP bridge binary at `cmd/hecate-acp`. It starts a
newline-delimited JSON-RPC stdio loop, advertises gateway models during
`initialize`, creates coding-agent tasks from `session/prompt`, and forwards
run stream snapshots as `session/update` notifications.

## Current status

- Implemented: stdio JSON-RPC loop, parse/invalid-request responses,
  `initialize`, `session/new`, `session/prompt`, `session/cancel`,
  gateway model discovery, task creation/start, run cancellation, run-event
  stream mapping, editor approval round-trip, multi-prompt continuation,
  optional `HECATE_API_KEY` / `HECATE_AUTH_TOKEN` forwarding.
- Not implemented yet: editor-owned workspace calls.

For alpha, one ACP session maps to one durable Hecate `agent_loop` task after
the first prompt. The first `session/prompt` creates and starts the task.
Later prompts call `POST /v1/tasks/{id}/runs/{run_id}/continue`, which
hydrates the saved conversation, appends the new user message, and starts the
next run.

## Configuration

| Variable | Default | Meaning |
|---|---:|---|
| `HECATE_GATEWAY_URL` | `http://127.0.0.1:8765` | Gateway base URL the bridge talks to. |
| `HECATE_API_KEY` | empty | Optional tenant API key sent as `x-api-key`. |
| `HECATE_AUTH_TOKEN` | empty | Optional bearer token for admin/operator deployments. |
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

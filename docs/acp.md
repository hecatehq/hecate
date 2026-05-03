# ACP bridge

Hecate has an early ACP bridge binary at `cmd/hecate-acp`. It starts a
newline-delimited JSON-RPC stdio loop, advertises gateway models during
`initialize`, creates coding-agent tasks from `session/prompt`, and forwards
run stream snapshots as `session/update` notifications.

The bridge is for editor-agent integrations. It is not a replacement for the
gateway API; it is a thin adapter that lets an ACP client talk to Hecate's
existing task runtime.

## Contents

- [Current status](#current-status)
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
- Optional `HECATE_API_KEY` / `HECATE_AUTH_TOKEN` forwarding.

Not implemented yet:

- Editor-owned workspace calls.
- Registry packaging for a specific editor.
- Headless compatibility tests against a real Zed or JetBrains ACP host.

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

The automated smoke lives in `e2e/acp-smoke.ts` and is wired into
`make verify-alpha`.

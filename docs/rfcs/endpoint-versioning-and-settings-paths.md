# Endpoint Versioning and Settings Paths

> **Status:** accepted and implemented for alpha. This document records the
> breaking namespace cleanup that moved Hecate-native endpoints under
> `/hecate/v1/*` before Hecate starts treating endpoint paths as stable.
> **Related:** [Runtime API](../runtime-api.md),
> [Deployment](../deployment.md), [Telemetry](../telemetry.md).
> **Owner:** see [`AGENTS.md`](../../AGENTS.md).

Before this migration, Hecate mixed three different API stories in one path
space:

- OpenAI-compatible endpoints under `/v1/*`.
- Anthropic-compatible endpoints under `/v1/*`.
- Hecate-native runtime, settings, task, chat, and observability endpoints under
  both `/v1/*` and legacy `/admin/*`.

That was fine while the project was still moving quickly, but it created a bad
stable contract. A client looking at old `/v1/tasks` and
`/v1/chat/completions` should not have to guess which `/v1` means
"OpenAI-shaped compatibility" and which `/v1` means "Hecate product API." The
legacy `/admin/*` naming was also misleading in single-user, local-first Hecate:
there is no role-separated admin API. The operator is the user.

This RFC defines the stable split:

- Keep provider-compatible ingress at the paths those clients expect:
  `/v1/chat/completions`, `/v1/messages`, and `/v1/models`.
- Move every Hecate-native product endpoint under `/hecate/v1/*`.
- Replace `/admin/control-plane/*` with `/hecate/v1/settings/*`.

## Implementation Status

Implemented in the alpha endpoint migration:

- Hecate-native task, event, trace, chat, agent-chat, adapter, MCP, workspace,
  settings, provider status, cost, system, retention, and observability routes
  moved to `/hecate/v1/*`.
- OpenAI-/Anthropic-compatible ingress stayed on `/v1/chat/completions`,
  `/v1/messages`, and `/v1/models`.
- `/healthz` stayed unversioned and intentionally cheap.
- The operator UI, API client, docs, and release checks were updated to the new
  paths without compatibility shims.
- `server.go` groups provider-compatible, Hecate-native, and health/OTLP
  routes separately so future additions land in the right namespace.

Follow-up work before stable:

- Keep this RFC as the naming source of truth when adding new endpoints. New
  Hecate-native routes should default to `/hecate/v1/*`; provider-compatible
  routes should stay in `/v1/*` only when they intentionally mimic an external
  provider API.
- Add or update focused route tests whenever a new route family is added, so
  old `/admin/*` or Hecate-native `/v1/*` paths do not creep back in.
- Keep screenshots, README snippets, ACP examples, MCP tools, and docs-ai
  recipes aligned whenever endpoint examples change.
- Revisit whether trace list and trace lookup should remain a single
  `/hecate/v1/traces` resource before declaring the trace API stable.

## Goals

- Remove the misleading `/admin/*` namespace before paths become stable.
- Reserve `/v1/*` for provider-compatible ingress and compatibility-shaped
  model listing.
- Use `/hecate/v1/*` as the single Hecate-native product API prefix.
- Use `settings` for operator-configured state.
- Use `system`, `costs`, and `observability` for runtime facts instead of
  overloading `settings`.
- Keep OpenAI/Anthropic-compatible gateway endpoints unchanged.
- Avoid compatibility shims during alpha. This is intentionally breaking.

## Non-goals

- Do not redesign response envelopes.
- Do not merge Agent Chat and Task endpoints.
- Do not change OpenAI-compatible `/v1/chat/completions`,
  Anthropic-compatible `/v1/messages`, or compatibility-shaped `/v1/models`.
- Do not introduce auth or role-based admin semantics.
- Do not rename handler packages in this RFC unless implementation needs it.

## Naming Rules

| Rule | Rationale |
|---|---|
| `/v1/*` is reserved for provider-compatible ingress. | This lets OpenAI/Anthropic-shaped clients keep their expected paths without mixing them with Hecate-native resources. |
| `/hecate/v1/*` is the only Hecate-native product API prefix. | Stable Hecate clients get an unambiguous namespace and version. |
| `/healthz` stays unversioned. | Health checks are infrastructure conventions, not product API contracts. |
| `/hecate/v1/settings/*` is for operator configuration. | Providers, policy rules, retention, and local provider onboarding are settings. |
| `/hecate/v1/system/*` is for runtime/system facts and maintenance. | Runtime stats, retention history, and MCP cache state are not settings. |
| `/hecate/v1/usage/*` is for usage summaries and events. | Usage history is product state but not general settings. |
| `/hecate/v1/observability/*` is for debug lists and history. | Request history and trace lists are operational views. |

### Why `/hecate/v1`, not `/v1/hecate`?

Both shapes would work, but `/hecate/v1/*` better communicates the boundary:

- `/v1/*` remains the compatibility namespace for provider-shaped clients.
- `/hecate/*` is the Hecate product namespace.
- `/hecate/v1/*` leaves room for a future `/hecate/v2/*` without disturbing
  compatibility clients that only know `/v1/chat/completions`.

The trade-off is a slightly longer native path, but the clarity is worth it
before stable.

## Endpoint Map

### Keep As-Is

| Area | Endpoint | Decision |
|---|---|---|
| Health | `GET /healthz` | Keep unversioned. |
| Gateway | `POST /v1/chat/completions` | Keep OpenAI-compatible shape. |
| Gateway | `POST /v1/messages` | Keep Anthropic-compatible shape. |
| Models | `GET /v1/models` | Keep compatibility-shaped model list. |

### Hecate Runtime

| Current | Proposed | Notes |
|---|---|---|
| `GET /v1/whoami` | `GET /hecate/v1/whoami` | Hecate session/runtime identity, not provider-compatible API. |
| `/v1/chat/sessions*` | `/hecate/v1/chat/sessions*` | Model-chat persistence is Hecate-native UI/runtime state. |
| `/v1/agent-adapters*` | `/hecate/v1/agent-adapters*` | External-agent adapter discovery, health, probe, launcher refresh. |
| `/v1/agent-chat*` | `/hecate/v1/agent-chat*` | Agent-chat sessions, approvals, grants, diffs, and streams. |
| `/v1/tasks*` | `/hecate/v1/tasks*` | Native task/runtime API. |
| `/v1/events*` | `/hecate/v1/events*` | Native task/run event stream. |
| `GET /v1/traces` | `GET /hecate/v1/traces` | Hecate trace lookup/listing. See observability note below. |
| `POST /v1/mcp/probe` | `POST /hecate/v1/mcp/probe` | Hecate MCP diagnostic endpoint. |
| `POST /v1/workspace-dialog` | `POST /hecate/v1/workspace-dialog` | Local UI/native bridge endpoint. |

### Provider Discovery and Status

| Current | Proposed | Notes |
|---|---|---|
| `GET /v1/provider-presets` | `GET /hecate/v1/providers/presets` | Group provider presets with provider surfaces. |
| `GET /admin/providers` | `GET /hecate/v1/providers/status` | Runtime provider health/model state. |
| `GET /admin/providers/history` | `GET /hecate/v1/providers/history` | Provider health/failover history. |

### Settings

| Current | Proposed | Notes |
|---|---|---|
| `GET /admin/control-plane` | `GET /hecate/v1/settings` | Snapshot of configured providers, policy, retention, and related settings. |
| `GET /admin/control-plane/providers/local-discovery` | `GET /hecate/v1/settings/providers/local-discovery` | Onboarding/config discovery, not runtime status. |
| `POST /admin/control-plane/providers` | `POST /hecate/v1/settings/providers` | Create configured provider. |
| `PATCH /admin/control-plane/providers/{id}` | `PATCH /hecate/v1/settings/providers/{id}` | Update configured provider. |
| `DELETE /admin/control-plane/providers/{id}` | `DELETE /hecate/v1/settings/providers/{id}` | Delete configured provider. |
| `PUT /admin/control-plane/providers/{id}/api-key` | `PUT /hecate/v1/settings/providers/{id}/api-key` | Store/update provider credential. |
| `POST /admin/control-plane/policy-rules` | `POST /hecate/v1/settings/policy-rules` | Upsert policy rule. |
| `POST /admin/control-plane/policy-rules/delete` | `DELETE /hecate/v1/settings/policy-rules/{id}` | Prefer an explicit delete route over action-body delete. |

### Usage

| Current | Proposed | Notes |
|---|---|---|
| `GET /admin/budget` | Removed | Global budget/accounting was removed. |
| `POST /admin/budget/topup` | Removed | Operator budget mutation was removed. |
| `POST /admin/budget/limit` | Removed | Operator budget mutation was removed. |
| `POST /admin/budget/reset` | Removed | Operator budget mutation was removed. |
| `GET /admin/accounts/summary` | Removed | Avoid legacy multi-account naming in single-user mode. |
| n/a | `GET /hecate/v1/usage/summary` | Current usage totals for the selected scope. |
| n/a | `GET /hecate/v1/usage/events` | Append-only usage events, retention-managed. |

### System

| Current | Proposed | Notes |
|---|---|---|
| `GET /admin/runtime/stats` | `GET /hecate/v1/system/stats` | Runtime and telemetry health snapshot. |
| `GET /admin/retention/runs` | `GET /hecate/v1/system/retention/runs` | Retention history. |
| `POST /admin/retention/run` | `POST /hecate/v1/system/retention/run` | Manual retention sweep. |
| `GET /admin/mcp/cache` | `GET /hecate/v1/system/mcp/cache` | Runtime cache state. |

### Observability

| Current | Proposed | Notes |
|---|---|---|
| `GET /admin/requests` | `GET /hecate/v1/observability/requests` | Request history/debug list. |
| `GET /admin/traces` | `GET /hecate/v1/traces` | Prefer one trace resource for lookup and list. |
| `GET /v1/traces?request_id=...` | `GET /hecate/v1/traces?request_id=...` | Move to Hecate namespace. |

## Open Questions

### Should trace listing merge into `/hecate/v1/traces`?

Before the endpoint migration, Hecate had two trace endpoints:

- `GET /v1/traces?request_id=...` for lookup.
- `GET /admin/traces` for list.

Preferred stable shape:

```text
GET /hecate/v1/traces?request_id=...
GET /hecate/v1/traces?limit=100
```

This keeps trace data in one resource. If implementation gets awkward, use
`GET /hecate/v1/observability/traces` as an explicit list endpoint and keep
`/hecate/v1/traces` for lookup.

### Should `settings` include runtime status?

No. `settings` should be limited to configured state and onboarding helpers.
Provider health, runtime stats, retention history, and request history are facts
about a running process, not settings.

### Should old `/admin/*` paths remain as shims?

No, not before stable. Hecate is still alpha and not following semver-backed API
compatibility yet. Keeping shims would double the route and docs surface and
make the stable contract less clear.

## Implementation Checklist

The migration followed this checklist:

1. Renamed Go route registrations in `internal/api/server.go`.
2. Renamed UI API client paths in `ui/src/lib/api.ts`.
3. Updated UI tests and Playwright fixtures.
4. Updated docs: `runtime-api.md`, `providers.md`, `telemetry.md`,
   `deployment.md`, `development.md`, README/docs index, and RFC examples that
   referred to old paths.
5. Ran targeted Go handler tests plus UI typecheck/unit/e2e.
6. Did not add compatibility shims.

## Migration Note

This is a breaking alpha cleanup. Operators should update any local scripts from
`/admin/*` and Hecate-native `/v1/*` endpoints to the `/hecate/v1/*` paths
above. OpenAI/Anthropic-compatible clients keep using `/v1/chat/completions`,
`/v1/messages`, and `/v1/models`. The UI and official docs should move in the
same commit so no supported surface points at the old routes.

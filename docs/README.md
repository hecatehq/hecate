# Hecate documentation

Long-form references for operators, integrators, and contributors. The [project README](../README.md) is the on-ramp; everything below is depth on a specific concern.

## Run it

| Doc | Read this when |
|---|---|
| [Deployment](deployment.md) | You're past Quick Start. Image pinning, compose profiles, binary install, lost-token recovery, single-user vs multi-tenant flags, storage tiers, rate limits. |
| [Providers](providers.md) | Adding a provider, browsing the preset catalog, custom OpenAI-compatible endpoints, env-vs-UI lifecycle, health and circuit-breaker behavior. |
| [Tenants and API keys](tenants.md) | You want more than one consumer of the gateway. Opt-in feature: roles, scopes, observability mirrors, what flips on when `GATEWAY_MULTI_TENANT=true`. |
| [Known limitations](known-limitations.md) | Before treating Hecate as production-stable. Plain-language list of what's still alpha. |

## Use it

| Doc | Read this when |
|---|---|
| [Runtime API](runtime-api.md) | Building a client against `/v1/tasks/*`. Lifecycle, approvals, run streaming, queue + lease semantics, health/discovery endpoints, bootstrap-token handshake. |
| [Agent runtime](agent-runtime.md) | Configuring `agent_loop` runs. Built-in tools, four-layer system prompt, approval gates, cost ceiling, retry-from-turn. |
| [Events](events.md) | Consuming `/v1/events` or per-run SSE. Catalog of every event type with payload shape and when it fires. |
| [MCP integration](mcp.md) | Wiring Hecate as an MCP server (Claude Desktop / Cursor / Zed) or attaching external MCP servers as tools to `agent_loop`. |

## Observe it

| Doc | Read this when |
|---|---|
| [Telemetry](telemetry.md) | OTLP traces / metrics / logs, response headers, local trace view, runtime-stats endpoints, retention worker subsystems. |

## Build it

| Doc | Read this when |
|---|---|
| [Architecture](architecture.md) | Internals of the gateway request flow and the task-runtime queue / lease / sandbox boundary. |
| [Development](development.md) | Building from source: Go + Bun toolchain, UI hot reload, the test ladder, screenshot tooling. |
| [Release](release.md) | Cutting a release tag. Versioning policy, alpha gate, image build, recovery if CI fails. |

## See also

- [`AGENTS.md`](../AGENTS.md) — codebase map and runtime invariants for contributors.
- [`ai/`](../ai/README.md) — vendor-neutral agent guidance (skills, conventions, workflow, verification).
- [`.env.example`](../.env.example) — practical first-run environment knobs.

# Hecate documentation

Long-form references for operators, integrators, and contributors. The [project README](../README.md) is the on-ramp; everything below is depth on a specific concern.

## Start here

Pick your role and read in order — each path is three to five docs.

**I'm running Hecate** (operator / self-hoster)
1. [Deployment](deployment.md) — image pinning, storage tiers, single-user vs multi-tenant flags, lost-token recovery.
2. [Providers](providers.md) — add your first provider, understand the preset catalog and health checks.
3. [Telemetry](telemetry.md) — wire up OTLP, read the local trace view, set retention windows.
4. [Known limitations](known-limitations.md) — what's still alpha before you depend on it.

**I'm building against Hecate** (integrator / SDK consumer)
1. [Runtime API](runtime-api.md) — task lifecycle, approvals, SSE streaming, bootstrap-token handshake.
2. [Agent runtime](agent-runtime.md) — `agent_loop` configuration, built-in tools, cost ceiling, retry-from-turn.
3. [Events](events.md) — every event type, payload shape, and when it fires.
4. [MCP integration](mcp.md) — wire Hecate as an MCP server or attach external MCP servers to agent tasks.
5. [Chat sessions](chat-sessions.md) — the two-stream model behind `/v1/chat/sessions` and history replay.

**I'm changing Hecate** (human contributor)
1. [`AGENTS.md`](../AGENTS.md) — codebase map and runtime invariants. Read this first.
2. [Architecture](architecture.md) — gateway request flow and the task-runtime queue / lease / sandbox boundary.
3. [Development](development.md) — Go + Bun toolchain, UI hot reload, the test ladder.
4. [`ai/`](../ai/README.md) — conventions, workflow, verification ladders, skill index.

**I'm an AI agent working on Hecate**
1. [`AGENTS.md`](../AGENTS.md) — orientation, codebase map, runtime invariants, gotchas.
2. [`ai/README.md`](../ai/README.md) — vendor-neutral instruction layer: conventions, workflow, verification ladders, skill index.
3. Pick the skill for your change area — backend, UI, providers, architect, tester, devops.

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
| [Chat sessions](chat-sessions.md) | The flat-message + provider-call model behind `/v1/chat/sessions`, the operator UI's chat surface, and history replay across model/provider switches. |
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

# Hecate documentation

Long-form references for operators, integrators, and contributors. The [project README](../README.md) is the on-ramp; everything below is depth on a specific concern.

## Start here

Pick your role and read in order — each path is three to five docs.

**I'm running Hecate on my laptop** (the primary supported mode today)
1. [Desktop app](desktop-app.md) — `.dmg` / `.deb` / `.AppImage` / `.msi` install, current state, footguns, roadmap.
2. [Deployment](deployment.md) — Docker, image pinning, binary install, storage tiers.
3. [Providers](providers.md) — add your first provider, understand the preset catalog and health checks.
4. [Known limitations](known-limitations.md) — what's still alpha before you depend on it.

**I'm building against Hecate** (integrator / SDK consumer)
1. [Runtime API](runtime-api.md) — task lifecycle, approvals, SSE streaming.
2. [Agent runtime](agent-runtime.md) — `agent_loop` configuration, built-in tools, cost ceiling, retry-from-turn.
3. [Events](events.md) — every event type, payload shape, and when it fires.
4. [MCP integration](mcp.md) — wire Hecate as an MCP server or attach external MCP servers to agent tasks.
5. [Chat sessions](chat-sessions.md) — the two-stream model behind `/v1/chat/sessions` and history replay.

**I'm changing Hecate** (human contributor)
1. [Architecture](architecture.md) — gateway request flow and the task-runtime queue / lease / sandbox boundary.
2. [Development](development.md) — Go + Bun toolchain, UI hot reload, the test ladder.
3. [`ai/`](../ai/README.md) — conventions, workflow, verification ladders, skill index.
4. [Release](release.md) — when you're cutting a tag: alpha gate, what CI produces, recovery.

**I'm an AI agent working on Hecate**
1. [`AGENTS.md`](../AGENTS.md) — orientation, codebase map, runtime invariants, gotchas.
2. [`ai/README.md`](../ai/README.md) — vendor-neutral instruction layer: conventions, workflow, verification ladders, skill index.
3. Pick the skill for your change area — backend, UI, providers, architect, tester, devops.

## Run it

| Doc | Read this when |
|---|---|
| [Deployment](deployment.md) | Local container / scripted deploy. Image pinning, binary install, storage tiers, rate limits. |
| [Desktop app](desktop-app.md) | Personal use on your laptop. Distribution bundles, first-launch footguns (Gatekeeper / SmartScreen), platform data dirs, roadmap. |
| [Providers](providers.md) | Adding a provider, browsing the preset catalog, custom OpenAI-compatible endpoints, env-vs-UI lifecycle, health and circuit-breaker behavior. |
| [Known limitations](known-limitations.md) | Before treating Hecate as production-stable. Plain-language list of what's still alpha. |

## Use it

| Doc | Read this when |
|---|---|
| [Runtime API](runtime-api.md) | Building a client against `/v1/tasks/*`. Lifecycle, approvals, run streaming, queue + lease semantics, health/discovery endpoints. |
| [Agent runtime](agent-runtime.md) | Configuring `agent_loop` runs. Built-in tools, three-layer system prompt, approval gates, cost ceiling, retry-from-turn. |
| [Sandbox](sandbox.md) | Per-call `sh` subprocess: policy validation, rlimits, env sanitisation, output cap; auto-detected `bwrap` (Linux) / `sandbox-exec` (macOS) wrapping for filesystem and network confinement. |
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
| [ACP bridge](acp.md) | Current status of the experimental ACP stdio bridge for editor integrations. |
| [Development](development.md) | Building from source: Go + Bun toolchain, UI hot reload, the test ladder, screenshot tooling. |
| [Release](release.md) | Cutting a release tag. Versioning policy, alpha gate, image build, recovery if CI fails. |

## RFCs

These documents describe future contracts. They are useful for design review,
but they are not implemented or stable frontend dependencies yet.

| Doc | Read this when |
|---|---|
| [Agent event protocol v1 candidate](event-protocol-v1.md) | Designing the typed event stream for CLI, web, ACP, and IDE consumers. |
| [Agent event protocol experimental extensions](event-protocol-experimental.md) | Parking future event ideas such as thinking blocks, sub-agents, multimodal output, branching, and write-side approval transport. |
| [Artifact storage v1 candidate](artifact-storage-v1.md) | Designing persisted command output, patches, fetched resources, and artifact retention. |

## See also

- [`AGENTS.md`](../AGENTS.md) — codebase map and runtime invariants for contributors.
- [`ai/`](../ai/README.md) — vendor-neutral agent guidance (skills, conventions, workflow, verification).
- [`.env.example`](../.env.example) — practical first-run environment knobs.

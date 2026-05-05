# Hecate documentation

The [project README](../README.md) is the product on-ramp. This directory is
the reference shelf: how to run Hecate, integrate with it, observe it, and
change it without tripping over alpha edges.

## Start Here

Pick the path closest to what you are doing.

| You are... | Read in this order |
|---|---|
| Running Hecate locally | [Desktop app](desktop-app.md), [Deployment](deployment.md), [Providers](providers.md), [Known limitations](known-limitations.md) |
| Calling Hecate from a client | [Runtime API](runtime-api.md), [Agent runtime](agent-runtime.md), [Events](events.md), [Chat sessions](chat-sessions.md) |
| Building or using coding-agent integrations | [External agent adapters](external-agent-adapters.md), [ACP bridge](acp.md), [Runtime API](runtime-api.md), [Events](events.md), [MCP integration](mcp.md) |
| Changing the codebase | [Architecture](architecture.md), [Development](development.md), [`docs-ai/`](../docs-ai/README.md), [Release](release.md) |
| Working as an AI agent | [`AGENTS.md`](../AGENTS.md), [`docs-ai/README.md`](../docs-ai/README.md), then the relevant `docs-ai/skills/*/SKILL.md` |

## Operator Docs

| Doc | What it answers |
|---|---|
| [Deployment](deployment.md) | Docker, binary install, image pinning, storage backends, rate limits, lost-token recovery. |
| [Desktop app](desktop-app.md) | Native bundles, first-launch warnings, platform data dirs, sidecar lifecycle, roadmap. |
| [Providers](providers.md) | Built-in provider presets, OpenAI-compatible custom endpoints, credentials, model discovery, health, circuit breaking. |
| [Known limitations](known-limitations.md) | The honest alpha boundary: API/schema stability, sandbox limits, desktop gaps, deployment scope. |

## Runtime And Integration Docs

| Doc | What it answers |
|---|---|
| [Runtime API](runtime-api.md) | `/v1/tasks/*`, approvals, run streaming, queue/lease semantics, health/discovery endpoints. |
| [Agent runtime](agent-runtime.md) | `agent_loop` configuration, built-in tools, stdout/stderr handling, system prompt layers, approvals, cost ceiling, retry-from-turn. |
| [Events](events.md) | Implemented event names, payloads, stdout/stderr stream chunks, and when each is emitted. Use this for today's `/v1/events` consumers. |
| [Chat sessions](chat-sessions.md) | Conversation persistence model behind the Chats UI and provider/model switching. |
| [External agent adapters](external-agent-adapters.md) | Hecate as an ACP client/operator: use Codex, Claude Code, and Cursor Agent from Chats; install checks, persistence, troubleshooting, current gaps. |
| [MCP integration](mcp.md) | Hecate as an MCP server and external MCP servers as task tools. |
| [ACP bridge](acp.md) | Hecate as an ACP agent for editor panels. Host setup, gateway discovery, session model, smoke test, and current gaps. |
| [Sandbox](sandbox.md) | Per-call subprocess execution, policy validation, env sanitisation, output cap, timeout, and OS wrappers. |

## Observability Docs

| Doc | What it answers |
|---|---|
| [Telemetry](telemetry.md) | OpenTelemetry traces, metrics, logs, response headers, local trace view, runtime stats, retention. |

## Contributor Docs

| Doc | What it answers |
|---|---|
| [Architecture](architecture.md) | Gateway flow, orchestrator responsibilities, task-runtime queue/lease model, agent turn cycle, storage tiers. |
| [Development](development.md) | Go + Bun + just + Rust/Cargo setup, local dev, test ladder, screenshot tooling, package map. |
| [Release](release.md) | Versioning, verification gate, release script, image build, recovery, release-note shape. |
| [`docs-ai/`](../docs-ai/README.md) | Vendor-neutral agent guidance: workflow, verification, skills, task recipes. |

## Candidate Contracts

These are design contracts in progress. They are useful for review and for
early frontend/client experiments, but they are not semver-backed API promises
yet.

| Doc | Status |
|---|---|
| [RFC index](rfcs/README.md) | All candidate and experimental design contracts. |
| [Agent event protocol v1 candidate](rfcs/event-protocol-v1.md) | Candidate envelope exists; payload schemas and stability guarantees are still in progress. |
| [Agent event protocol experimental extensions](rfcs/event-protocol-experimental.md) | Parking lot for future event groups such as thinking blocks, sub-agents, multimodal output, and branching. |
| [Artifact storage v1 candidate](rfcs/artifact-storage-v1.md) | Candidate shape for persisted command output, patches, fetched resources, and artifact retention. |
| [External agent adapters candidate](rfcs/external-agent-adapters.md) | Candidate shape for chatting with Codex, Claude Code, Cursor Agent, and future coding-agent CLIs through Hecate. |

## External Entry Points

- [`.env.example`](../.env.example) — minimal first-run environment knobs.
- [`AGENTS.md`](../AGENTS.md) — contributor and AI-agent orientation.
- [`CONTRIBUTING.md`](../CONTRIBUTING.md) — contribution workflow.

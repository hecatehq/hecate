# Hecate documentation

The [project README](../README.md) is the product on-ramp. This directory is
the reference shelf: how to run Hecate, integrate with it, observe it, and
change it without tripping over alpha edges.

## Start Here

Pick the path closest to what you are doing.

| You are... | Read in this order |
|---|---|
| Running Hecate locally | [Desktop app](desktop-app.md), [Deployment](deployment.md), [Security](security.md), [Providers](providers.md), [Chat sessions](chat-sessions.md), [Known limitations](known-limitations.md) |
| Calling Hecate from a client | [Runtime API](runtime-api.md), [Chat sessions](chat-sessions.md), [Agent runtime](agent-runtime.md), [Events](events.md) |
| Building or using coding-agent integrations | [External agent adapters](external-agent-adapters.md), [ACP bridge](acp.md), [Runtime API](runtime-api.md), [Events](events.md), [MCP integration](mcp.md) |
| Changing the codebase | [Architecture](architecture.md), [Development](development.md), [Alpha-to-beta roadmap](beta-roadmap.md), [`docs-ai/`](../docs-ai/README.md), [Release](release.md) |
| Working as an AI agent | [`AGENTS.md`](../AGENTS.md), [`docs-ai/README.md`](../docs-ai/README.md), then the relevant `docs-ai/skills/*/SKILL.md` |

## Operator Docs

| Doc | What it answers |
|---|---|
| [Deployment](deployment.md) | Docker, binary install, image pinning, storage backends, rate limits, lost-token recovery. |
| [Desktop app](desktop-app.md) | Native bundles, first-launch warnings, platform data dirs, sidecar lifecycle, roadmap. |
| [Security](security.md) | Local-first threat model, runtime boundaries, workspace safety, approvals, secrets, and advisory handling. |
| [Providers](providers.md) | Built-in provider presets, OpenAI-compatible custom endpoints, credentials, model discovery, health, circuit breaking. |
| [Chat sessions](chat-sessions.md) | Hecate Chat transcript segments, tools on/off behavior, task-backed turns, queued prompts, approvals in Chats, and shared activity rendering. |
| [Known limitations](known-limitations.md) | The honest alpha boundary: API/schema stability, sandbox limits, desktop gaps, deployment scope. |

## Runtime And Integration Docs

| Doc | What it answers |
|---|---|
| [Runtime API](runtime-api.md) | `/hecate/v1/tasks/*`, `/hecate/v1/agent-chat/*`, approvals, run streaming, queue/lease semantics, health/discovery endpoints. |
| [Agent runtime](agent-runtime.md) | `agent_loop` configuration, built-in tools, stdout/stderr handling, system prompt layers, approvals, cost ceiling, retry-from-turn. |
| [Events](events.md) | Implemented event names, payloads, stdout/stderr stream chunks, and when each is emitted. Use this for today's `/hecate/v1/events` consumers. |
| [Chat sessions](chat-sessions.md) | Conversation persistence model behind the Chats UI, Hecate Chat segments, provider/model switching, queued prompts, and external-agent sessions. |
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
| [Alpha-to-beta roadmap](beta-roadmap.md) | Beta gate, core runtime work, view-by-view UX order, cleanup/refactoring, and branch/release workflow. |
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
| [Hecate Chat and model capabilities](rfcs/unified-chats-and-model-capabilities.md) | Accepted alpha direction for Hecate Chat tools on/off segments, model capability metadata, profiles, and future probes. |
| [Terminal / CLI distribution](rfcs/terminal-distribution.md) | Candidate shape for a terminal-first install with `hecate`, `hecate-acp`, and a future first-class TUI. |

## External Entry Points

- [`.env.example`](../.env.example) — minimal first-run environment knobs.
- [`SECURITY.md`](../SECURITY.md) — supported versions and vulnerability reporting.
- [`AGENTS.md`](../AGENTS.md) — contributor and AI-agent orientation.
- [`CONTRIBUTING.md`](../CONTRIBUTING.md) — contribution workflow.

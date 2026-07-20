# Hecate Documentation

The [project README](../README.md) is the product on-ramp. This directory is
the reference shelf for operators, integrators, contributors, and design work.

Docs are organized by audience and stability:

- [Operator guides](operator/) describe how to run and configure Hecate.
- [Runtime references](runtime/) describe implemented APIs, events, adapters,
  sandbox behavior, and observability.
- [Contributor docs](contributor/) describe the architecture, development
  workflow, release process, and beta roadmap.
- [Design records](design/) describe proposed, accepted, candidate,
  implemented, and parked architecture direction.

## Start Here

| You are...                                  | Read in this order                                                                                                                                                                                                                       |
| ------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Running Hecate locally                      | [Desktop app](operator/desktop-app.md), [Projects](operator/projects.md), [Deployment](operator/deployment.md), [Security](operator/security.md), [Providers](operator/providers.md), [Known limitations](operator/known-limitations.md) |
| Calling Hecate from a client                | [Runtime API](runtime/runtime-api.md), [Chat sessions](runtime/chat-sessions.md), [Agent runtime](runtime/agent-runtime.md), [Events](runtime/events.md)                                                                                 |
| Connecting an ACP client to Hecate          | [ACP agent](runtime/acp.md), [Agent runtime](runtime/agent-runtime.md), [Events](runtime/events.md)                                                                                                                                      |
| Building or using coding-agent integrations | [External Agents](runtime/external-agents.md), [ACP agent](runtime/acp.md), [Runtime API](runtime/runtime-api.md), [Events](runtime/events.md), [MCP integration](runtime/mcp.md)                                                        |
| Changing the codebase                       | [Architecture](contributor/architecture.md), [Development](contributor/development.md), [Beta roadmap](contributor/beta-roadmap.md), [`docs-ai/`](../docs-ai/README.md), [Release](contributor/release.md)                               |
| Planning future behavior                    | [Design records](design/), especially the relevant lifecycle bucket before implementation starts.                                                                                                                                        |
| Working as an AI agent                      | [`AGENTS.md`](../AGENTS.md), [`docs-ai/README.md`](../docs-ai/README.md), then the relevant `docs-ai/skills/*/SKILL.md`.                                                                                                                 |

## Operator Guides

| Doc                                                            | What it answers                                                                                                |
| -------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| [Deployment](operator/deployment.md)                           | Docker, binary install, image pinning, storage backends, rate limits, lost-token recovery.                     |
| [Desktop app](operator/desktop-app.md)                         | Native bundles, tested-platform status, first-launch warnings, platform data dirs, sidecar lifecycle, roadmap. |
| [Projects](operator/projects.md)                               | Project setup, roots/worktrees, assignments, reviews, handoffs, evidence, and V1 boundaries.                   |
| [Security](operator/security.md)                               | Local-first threat model, runtime boundaries, workspace safety, approvals, secrets, and advisory handling.     |
| [Providers](operator/providers.md)                             | Built-in provider presets, custom endpoints, credentials, model discovery, health, circuit breaking.           |
| [Desktop updater signing](operator/desktop-updater-signing.md) | Tauri updater signing key custody and release integration.                                                     |
| [macOS signing](operator/macos-signing.md)                     | Developer ID, notarization, and maintainer-side macOS release signing setup.                                   |
| [Known limitations](operator/known-limitations.md)             | The honest alpha boundary: API/schema stability, sandbox limits, desktop gaps, deployment scope.               |

## Runtime References

| Doc                                           | What it answers                                                                                                                              |
| --------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| [Runtime API](runtime/runtime-api.md)         | `/hecate/v1/tasks/*`, Task Schedules, `/hecate/v1/chat/*`, approvals, Run streaming, queue/lease semantics, health/discovery endpoints.      |
| [Agent runtime](runtime/agent-runtime.md)     | `agent_loop` configuration, built-in tools, stdout/stderr handling, system prompt layers, approvals, cost ceiling, retry-from-model-call.    |
| [Chat sessions](runtime/chat-sessions.md)     | Hecate Chat transcript segments, tools on/off behavior, task-backed turns, queued prompts, approvals, context packets, External Agent chats. |
| [External Agents](runtime/external-agents.md) | Codex, Claude Code, Cursor Agent, and Grok Build from Chats; install checks, credential boundaries, persistence, troubleshooting.            |
| [ACP agent](runtime/acp.md)                   | `hecate acp serve`: use Hecate's native task runtime from an ACP-capable editor or client over local stdio.                                  |
| [Events](runtime/events.md)                   | Implemented event names, payloads, stdout/stderr stream chunks, and when each is emitted.                                                    |
| [MCP integration](runtime/mcp.md)             | Hecate as an MCP server and external MCP servers as task tools.                                                                              |
| [Sandbox](runtime/sandbox.md)                 | Per-call subprocess execution, policy validation, env sanitisation, output cap, timeout, and OS wrappers.                                    |
| [Telemetry](runtime/telemetry.md)             | OpenTelemetry traces, metrics, logs, response headers, local trace view, runtime stats, retention.                                           |

## Contributor Docs

| Doc                                         | What it answers                                                                                                                   |
| ------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| [Architecture](contributor/architecture.md) | Gateway flow, orchestrator and scheduler responsibilities, task-runtime queue/lease model, agent model-call cycle, storage tiers. |
| [Development](contributor/development.md)   | Go + Bun + just + Rust/Cargo setup, local dev, test ladder, screenshot tooling, package map.                                      |
| [Release](contributor/release.md)           | Versioning, verification gate, release script, image build, recovery, release-note shape.                                         |
| [Beta roadmap](contributor/beta-roadmap.md) | Beta gate, core runtime work, view-by-view UX order, cleanup/refactoring, and branch/release workflow.                            |
| [`docs-ai/`](../docs-ai/README.md)          | Vendor-neutral agent guidance: workflow, verification, skills, task recipes.                                                      |

## Design Records

Design records are not runtime contracts. They say what is proposed, accepted,
candidate-shaped, already implemented, or intentionally parked.

| Bucket                             | Meaning                                                                                    |
| ---------------------------------- | ------------------------------------------------------------------------------------------ |
| [Proposals](design/proposals/)     | Direction is written down, but implementation has not started or is only partial.          |
| [Accepted](design/accepted/)       | Direction is agreed for alpha, with implementation either partial or ongoing.              |
| [Candidates](design/candidates/)   | Some implementation exists, but the wire/payload shape is not stable.                      |
| [Implemented](design/implemented/) | Work landed; the record remains as design history. Current behavior lives in runtime docs. |
| [Parking lot](design/parking-lot/) | Future or experimental ideas that should not drive implementation by themselves.           |

Start with the [design index](design/) before changing projects, context,
memory, workflow runbooks, event contracts, artifact contracts, external-agent
adapters, or other cross-cutting runtime behavior.

## External Entry Points

- [`.env.example`](../.env.example) — minimal first-run environment knobs.
- [`SECURITY.md`](../SECURITY.md) — supported versions and vulnerability reporting.
- [`AGENTS.md`](../AGENTS.md) — contributor and AI-agent orientation.
- [`CONTRIBUTING.md`](../CONTRIBUTING.md) — contribution workflow.

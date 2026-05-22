# Hecate RFCs

Design contracts in this directory are product and architecture notes. Some are
active proposals, some are accepted alpha direction, and some are implemented
records kept for context. They are not semver-backed API promises unless the
implemented runtime docs say so.

Implemented runtime behavior lives in the main docs:

- [Events](../events.md) — event names and payloads emitted today.
- [Runtime API](../runtime-api.md) — current task/run/approval endpoints.
- [Chat sessions](../chat-sessions.md) — current Hecate Chat and External
  Agent session behavior.
- [External agent adapters](../external-agent-adapters.md) — current Codex,
  Claude Code, and Cursor operator flow.

## Status Labels

| Status             | Meaning                                                                                  |
| ------------------ | ---------------------------------------------------------------------------------------- |
| Proposed           | Direction is written down, but implementation has not started.                           |
| Accepted           | Direction is agreed for alpha, with implementation either partial or ongoing.            |
| Candidate          | Some implementation exists, but the wire/payload shape is not stable.                    |
| Implemented record | Work landed; the RFC remains as design history. Current behavior lives in the main docs. |
| Parking lot        | Future or experimental ideas that should not drive implementation by themselves.         |

## Accepted / In Progress

| RFC                                                                     | Status                                                                                                                                                                                                                                                                              | Next action                                                                                                        |
| ----------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| [Hecate Chat and model capabilities](hecate-chat-model-capabilities.md) | Accepted. Tools on/off segments, task-backed Hecate Chat turns, queued prompts, observed model capability metadata, and shared transcript primitives exist. The RFC still contains some "Hecate Agent" design-history wording; current UI/docs call this Hecate Chat with tools on. | Implement workspace modes, named agent profiles, richer automatic capability detection, and broader e2e hardening. |
| [External agent adapters](external-agent-adapters.md)                   | Accepted. Codex, Claude Code, Cursor Agent, readiness, guardrails, approvals, diagnostics, ACP controls, streaming, cancellation, and diff inspect/revert have alpha coverage.                                                                                                      | Keep improving adapter-specific mapping, patch review UX, and convergence with task-runtime primitives.            |
| [Projects](projects.md)                                                 | Accepted. The project store and CRUD API foundation exist with memory and SQLite backends. Chats, tasks, memory, profiles, and context packets are not linked to `project_id` yet.                                                                                                  | Attach chats/tasks to projects, then layer context packets and project-scoped memory on top.                       |

## Active Proposals

| RFC                                                                                       | Status                                                                                               | Next action                                                                                        |
| ----------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| [Migration CLI](migration-cli.md)                                                         | Proposed. No dedicated migration/rollback CLI exists yet.                                            | Design `hecate migrate` around the current SQLite migration packages.                              |
| [Context assembly and injection boundaries](context-assembly-and-injection-boundaries.md) | Proposed. Hecate does not yet persist an inspectable "what did the model see?" context packet.       | Implement context-packet snapshots before memory or summarization work.                            |
| [Agent memory](agent-memory.md)                                                           | Proposed. No durable operator-authored memory primitive exists yet.                                  | Build after context packets so memory inclusion is visible and auditable.                          |
| [LLM context window management](llm-context-window-management.md)                         | Proposed. Hecate still needs token estimation, context warnings/caps, and optional fitting policies. | Use context packets as the estimator input; keep trust decisions in context assembly.              |
| [Import external chat history](import-external-chat-history.md)                           | Proposed. Import from Claude Code and Codex transcripts is not implemented.                          | Keep as-is until import work starts.                                                               |
| [Embeddings](embeddings.md)                                                               | Proposed. OpenAI-compatible embeddings routing is not implemented.                                   | Refresh provider/capability references before implementation.                                      |
| [Provider response extensions](provider-response-extensions.md)                           | Proposed. Vendor-specific response extras are still not preserved end-to-end.                        | Use when adding Perplexity citations, DeepSeek/xAI reasoning content, or Gemini citation metadata. |

## Candidate Contracts

| RFC                                             | Status                                                                                                                                       | Next action                                                                           |
| ----------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| [Agent event protocol v1](event-protocol-v1.md) | Candidate. The envelope exists for task-run event APIs; payload schemas are not stable.                                                      | Align event names/payloads with current task/chat artifacts before calling v1 stable. |
| [Artifact storage v1](artifact-storage-v1.md)   | Candidate / partially superseded. Task artifacts and chat diff inspect/revert exist, but this RFC is broader than the shipped alpha surface. | Rewrite before exposing a standalone artifact API.                                    |

## Implemented Records

| RFC                                                                   | Status                                                                                                                                  | Next action                                                              |
| --------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| [External Agent approval loop v1](external-agent-approval-loop-v1.md) | Implemented alpha baseline. Prompt-first approvals, REST/SSE events, durable grants, startup reconcile, UI review, and telemetry exist. | Keep as design history unless the approval contract changes before beta. |

## Parking Lot

| RFC                                                             | Status                                                                                                  | Next action                                                                    |
| --------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| [Agent event protocol extensions](event-protocol-extensions.md) | Parking lot. Future event groups such as thinking blocks, sub-agents, multimodal output, and branching. | Promote individual ideas into a proposed RFC only when implementation is near. |

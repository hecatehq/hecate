# Hecate RFCs

RFCs are product and architecture design records. They capture direction,
trade-offs, and candidate contracts before behavior is stable enough for the
main runtime docs.

Implemented runtime behavior lives in:

- [Runtime API](../runtime-api.md) for current task, run, approval, project,
  chat, and memory endpoints.
- [Agent runtime](../agent-runtime.md) for `agent_loop`, tools, cost ceilings,
  retry/resume, and sandbox policy.
- [Chat sessions](../chat-sessions.md) for Hecate Chat, task-backed turns,
  context packets, and External Agent sessions.
- [Events](../events.md) for event names and payloads emitted today.
- [External agent adapters](../external-agent-adapters.md) for current Codex,
  Claude Code, Cursor Agent, and Grok Build operator flows.

## How To Read This Directory

Start with the track that matches the change you are making, then open the
individual RFCs in the listed order. Do not treat an RFC as a shipped API
contract unless the current runtime docs say the same behavior exists.

| Track                                                | Read first                                                                                     | Why it matters                                                                 |
| ---------------------------------------------------- | ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| [Projects, context, and memory](#projects-context-and-memory) | [Projects](projects.md), [Context assembly](context-assembly-and-injection-boundaries.md)       | Durable identity, context provenance, memory scope, and prompt injection rules. |
| [Workflow runbooks](#workflow-runbooks)              | [Workflow runbooks v0](workflow-runbooks-v0.md), [Agent memory](agent-memory.md)                | Named modes, evidence artifacts, browser QA, and approved lesson promotion.     |
| [Agent and chat runtime](#agent-and-chat-runtime)    | [Hecate Chat and model capabilities](hecate-chat-model-capabilities.md), [External agents](external-agent-adapters.md) | Hecate Chat, tools, model capability metadata, and supervised coding agents.    |
| [Runtime contracts](#runtime-contracts)              | [Agent event protocol v1](event-protocol-v1.md), [Artifact storage v1](artifact-storage-v1.md)  | Candidate event/artifact shapes that may affect clients.                       |
| [Platform and CLI](#platform-and-cli)                | [CLI structure](cli-structure.md), [Migration CLI](migration-cli.md)                            | Operator command shape and storage migration direction.                         |
| [Future research](#future-research)                  | [Autoresearch](autoresearch.md), [Embeddings](embeddings.md)                                    | Experiments that should not drive implementation without a fresh review.        |

## Status Labels

| Status             | Meaning                                                                                  |
| ------------------ | ---------------------------------------------------------------------------------------- |
| Proposed           | Direction is written down, but implementation has not started.                           |
| Accepted           | Direction is agreed for alpha, with implementation either partial or ongoing.            |
| Candidate          | Some implementation exists, but the wire/payload shape is not stable.                    |
| Implemented record | Work landed; the RFC remains as design history. Current behavior lives in the main docs. |
| Parking lot        | Future or experimental ideas that should not drive implementation by themselves.         |

## Architecture Tracks

### Projects, Context, And Memory

These RFCs are intentionally layered. Preserve this order when adding features:

1. [Projects](projects.md) provide durable identity for a codebase or work area.
2. Agent profiles describe reusable runtime behavior for Hecate Chat or an
   external agent. Presets are templates that create or update profiles/project
   defaults; they are not runtime identity.
3. [Context assembly](context-assembly-and-injection-boundaries.md) turns chat,
   project, profile, workflow, memory, workspace, runtime, and collaboration
   inputs into inspectable context packets.
4. [Agent memory](agent-memory.md) stores operator-approved durable context,
   with project memory as the default shared scope.
5. [Context window management](llm-context-window-management.md) fits context
   packets into model limits without changing trust labels or source authority.

Workflow runs, project-team assignments, handoffs, reviews, and decision notes
should enter model calls through context packets first. Durable memory and
automated summarization come later so Hecate can always answer what each agent
or workflow saw and why.

### Workflow Runbooks

Workflow runbooks are named, typed task patterns such as `review`,
`investigate`, `qa`, `ship`, `security-audit`, and `design-review`. They should
not create a second memory or prompt system. The intended flow is:

```mermaid
flowchart LR
    A["Project"] --> B["Context assembly"]
    B --> C["Workflow run"]
    C --> D["Evidence artifacts"]
    D --> E["Memory candidates"]
    E --> F["Operator approval"]
    F --> G["Project memory"]
```

The v0 proposal is [Workflow runbooks v0](workflow-runbooks-v0.md).

### Agent And Chat Runtime

| RFC                                                                     | Status                                                                                                                                                                                                                                                                                         | Next action                                                                                                        |
| ----------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| [Hecate Chat and model capabilities](hecate-chat-model-capabilities.md) | Accepted. Tools on/off segments, task-backed Hecate Chat turns, queued prompts, observed model capability metadata, and shared transcript primitives exist. The RFC still contains some "Hecate Agent" design-history wording; current UI/docs call this Hecate Chat with tools on.            | Implement workspace modes, named agent profiles, richer automatic capability detection, and broader e2e hardening. |
| [External agent adapters](external-agent-adapters.md)                   | Accepted. Codex, Claude Code, Cursor Agent, readiness, guardrails, approvals, diagnostics, ACP controls, streaming, cancellation, and diff inspect/revert have alpha coverage.                                                                                                                 | Keep improving adapter-specific mapping, patch review UX, and convergence with task-runtime primitives.            |

### Runtime Contracts

| RFC                                             | Status                                                                                                                                       | Next action                                                                           |
| ----------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| [Agent event protocol v1](event-protocol-v1.md) | Candidate. The envelope exists for task-run event APIs; payload schemas are not stable.                                                      | Align event names/payloads with current task/chat artifacts before calling v1 stable. |
| [Artifact storage v1](artifact-storage-v1.md)   | Candidate / partially superseded. Task artifacts and chat diff inspect/revert exist, but this RFC is broader than the shipped alpha surface. | Rewrite before exposing a standalone artifact API.                                    |

### Platform And CLI

| RFC                                             | Status                                                                                                  | Next action                                                                                             |
| ----------------------------------------------- | ------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| [CLI structure](cli-structure.md)               | Proposed. Bare `hecate` should become the terminal operator UI; runtime launch moves to `serve`.        | Land the structured command package, `hecate serve`, `hecate ui`, and `hecate mcp serve` first.         |
| [Migration CLI](migration-cli.md)               | Proposed. No dedicated migration/rollback CLI exists yet.                                              | Design `hecate migrate` around the current SQLite migration packages.                                   |
| [Provider response extensions](provider-response-extensions.md) | Proposed. Vendor-specific response extras are still not preserved end-to-end.                           | Use when adding Perplexity citations, DeepSeek/xAI reasoning content, or Gemini citation metadata.      |

### Future Research

| RFC                                                             | Status                                                                                                  | Next action                                                                    |
| --------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| [Autoresearch](autoresearch.md)                                 | Proposed. Bounded command/metric/check experiment loop for local code research; not implemented.        | Prototype a files-first companion runner against one conformance workflow.     |
| [Import external chat history](import-external-chat-history.md) | Proposed. Import from Claude Code and Codex transcripts is not implemented.                             | Keep as-is until import work starts.                                           |
| [Embeddings](embeddings.md)                                     | Proposed. OpenAI-compatible embeddings routing is not implemented.                                      | Refresh provider/capability references before implementation.                  |
| [Agent event protocol extensions](event-protocol-extensions.md) | Parking lot. Future event groups such as thinking blocks, sub-agents, multimodal output, and branching. | Promote individual ideas into a proposed RFC only when implementation is near. |

## Full Catalog

| RFC                                                                                       | Status                  | Track                         |
| ----------------------------------------------------------------------------------------- | ----------------------- | ----------------------------- |
| [Projects](projects.md)                                                                   | Accepted                | Projects, context, and memory |
| [Context assembly and injection boundaries](context-assembly-and-injection-boundaries.md) | Proposed; partial       | Projects, context, and memory |
| [Agent memory](agent-memory.md)                                                           | Proposed; partial       | Projects, context, and memory |
| [LLM context window management](llm-context-window-management.md)                         | Proposed                | Projects, context, and memory |
| [Workflow runbooks v0](workflow-runbooks-v0.md)                                           | Proposed                | Workflow runbooks             |
| [Hecate Chat and model capabilities](hecate-chat-model-capabilities.md)                   | Accepted                | Agent and chat runtime        |
| [External agent adapters](external-agent-adapters.md)                                     | Accepted                | Agent and chat runtime        |
| [External Agent approval loop v1](external-agent-approval-loop-v1.md)                     | Implemented record      | Agent and chat runtime        |
| [Agent event protocol v1](event-protocol-v1.md)                                           | Candidate               | Runtime contracts             |
| [Artifact storage v1](artifact-storage-v1.md)                                             | Candidate               | Runtime contracts             |
| [CLI structure](cli-structure.md)                                                         | Proposed                | Platform and CLI              |
| [Migration CLI](migration-cli.md)                                                         | Proposed                | Platform and CLI              |
| [Provider response extensions](provider-response-extensions.md)                           | Proposed                | Platform and CLI              |
| [Autoresearch](autoresearch.md)                                                           | Proposed                | Future research               |
| [Import external chat history](import-external-chat-history.md)                           | Proposed                | Future research               |
| [Embeddings](embeddings.md)                                                               | Proposed                | Future research               |
| [Agent event protocol extensions](event-protocol-extensions.md)                           | Parking lot             | Future research               |
| [RFC audit, 2026-05-17](AUDIT-2026-05-17.md)                                              | Historical audit record | Maintenance                   |

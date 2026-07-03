# Design Records

Design records capture product and architecture direction before it becomes
stable runtime documentation. They are not API promises unless the current
runtime references describe the same behavior.

Implemented behavior lives in:

- [Runtime API](../runtime/runtime-api.md)
- [Agent runtime](../runtime/agent-runtime.md)
- [Chat sessions](../runtime/chat-sessions.md)
- [External Agent integrations](accepted/external-agent-integrations.md)
- [Events](../runtime/events.md)
- [Security](../operator/security.md)

## Lifecycle Buckets

| Bucket                      | Meaning                                                                                    |
| --------------------------- | ------------------------------------------------------------------------------------------ |
| [Proposals](proposals/)     | Direction is written down, but implementation has not started or is only partial.          |
| [Accepted](accepted/)       | Direction is agreed for alpha, with implementation either partial or ongoing.              |
| [Candidates](candidates/)   | Some implementation exists, but the wire/payload shape is not stable.                      |
| [Implemented](implemented/) | Work landed; the record remains as design history. Current behavior lives in runtime docs. |
| [Parking lot](parking-lot/) | Future or experimental ideas that should not drive implementation by themselves.           |

Use the path as a signal. If a record moves from `proposals/` to `accepted/`,
or from `accepted/` to `implemented/`, update this index and the record header
in the same change.

## Architecture Tracks

### Projects, Context, And Memory

Read these in order:

1. [Projects](accepted/projects.md)
2. [Context assembly and injection boundaries](proposals/context-assembly-and-injection-boundaries.md)
3. [Agent memory](proposals/agent-memory.md)
4. [Workspace instructions, skills, and profiles](proposals/workspace-instructions-skills-and-profiles.md)
5. [Run evidence and portable memory](proposals/run-evidence-and-portable-memory.md)
6. [LLM context window management](proposals/llm-context-window-management.md)
7. [Cairnline: portable project coordination](proposals/cairnline-portable-project-coordination.md)

The invariant: projects provide identity, context assembly decides what enters
a model or adapter call, memory is operator-approved durable context, and window
management fits an already-labelled context packet into a model limit.
Cairnline is a future extraction path for the project coordination substrate,
not current Hecate runtime behavior.

### Workflow Runbooks

Workflow runbooks are named task patterns such as `review`, `investigate`,
`qa`, `ship`, `security-audit`, and `design-review`.

Start with [Workflow runbooks v0](proposals/workflow-runbooks-v0.md), then read
[Agent memory](proposals/agent-memory.md) for the memory-candidate promotion
flow and [Context assembly](proposals/context-assembly-and-injection-boundaries.md)
for the context packet boundary.

### Workspace Instructions, Skills, And Agent Presets

Workspace instructions, reusable skills, Hecate Agent Presets, portable project
roles, desired-agent hints, and runbooks are deliberately separate concepts.
Start with
[Workspace instructions, skills, and profiles](proposals/workspace-instructions-skills-and-profiles.md)
before changing Agent Presets, skill registry support, or workspace `AGENTS.md`
discovery.

### Agent And Chat Runtime

| Record                                                                            | Bucket      | Notes                                                                                                       |
| --------------------------------------------------------------------------------- | ----------- | ----------------------------------------------------------------------------------------------------------- |
| [Hecate Chat and model capabilities](accepted/hecate-chat-model-capabilities.md)  | Accepted    | Hecate Chat, tools on/off segments, observed capability metadata, profiles, future detection.               |
| [External Agent integrations](accepted/external-agent-integrations.md)            | Accepted    | Codex, Claude Code, Cursor Agent, Grok Build, ACP controls, approvals, readiness, diagnostics, diff review. |
| [ADK and A2A alignment](proposals/adk-a2a-alignment.md)                           | Proposal    | ADK concepts as design input, A2A as a future protocol adapter for Hecate and remote agents.                |
| [External Agent approval loop v1](implemented/external-agent-approval-loop-v1.md) | Implemented | Prompt-first approvals, durable grants, startup reconcile, UI review, telemetry.                            |

### Runtime Contracts

| Record                                                     | Bucket    | Notes                                                                            |
| ---------------------------------------------------------- | --------- | -------------------------------------------------------------------------------- |
| [Agent event protocol v1](candidates/event-protocol-v1.md) | Candidate | Envelope exists for task-run event APIs; payload stability is still in progress. |
| [Artifact storage v1](candidates/artifact-storage-v1.md)   | Candidate | Broader than the shipped task artifacts and chat diff inspect/revert surface.    |

### Extension Surface

| Record                                                  | Bucket   | Notes                                                                                      |
| ------------------------------------------------------- | -------- | ------------------------------------------------------------------------------------------ |
| [Plugin architecture](proposals/plugin-architecture.md) | Proposal | Hecate-native plugins, connectors, MCP capabilities, skills, slash commands, and evidence. |

### Platform, CLI, And Provider Surface

| Record                                                                    | Bucket   | Notes                                                                   |
| ------------------------------------------------------------------------- | -------- | ----------------------------------------------------------------------- |
| [CLI structure](proposals/cli-structure.md)                               | Proposal | Terminal operator UI and `serve` split.                                 |
| [Migration CLI](proposals/migration-cli.md)                               | Proposal | Dedicated migration and rollback CLI direction.                         |
| [Provider response extensions](proposals/provider-response-extensions.md) | Proposal | Vendor-specific response extras such as citations or reasoning content. |

### Future Research

| Record                                                                      | Bucket      | Notes                                                                                  |
| --------------------------------------------------------------------------- | ----------- | -------------------------------------------------------------------------------------- |
| [Autoresearch](proposals/autoresearch.md)                                   | Proposal    | Bounded command/metric/check experiment loop for local code research.                  |
| [Import external chat history](proposals/import-external-chat-history.md)   | Proposal    | Import Claude Code and Codex transcripts into Hecate-visible history.                  |
| [Embeddings](proposals/embeddings.md)                                       | Proposal    | OpenAI-compatible embeddings routing.                                                  |
| [Agent event protocol extensions](parking-lot/event-protocol-extensions.md) | Parking lot | Future event groups such as thinking blocks, sub-agents, multimodal output, branching. |

## Full Catalog

| Record                                                                                                  | Bucket             | Track                         |
| ------------------------------------------------------------------------------------------------------- | ------------------ | ----------------------------- |
| [Projects](accepted/projects.md)                                                                        | Accepted           | Projects, context, and memory |
| [Context assembly and injection boundaries](proposals/context-assembly-and-injection-boundaries.md)     | Proposal; partial  | Projects, context, and memory |
| [Agent memory](proposals/agent-memory.md)                                                               | Proposal; partial  | Projects, context, and memory |
| [Workspace instructions, skills, and profiles](proposals/workspace-instructions-skills-and-profiles.md) | Proposal           | Projects, context, and memory |
| [Run evidence and portable memory](proposals/run-evidence-and-portable-memory.md)                       | Proposal           | Projects, context, and memory |
| [LLM context window management](proposals/llm-context-window-management.md)                             | Proposal           | Projects, context, and memory |
| [Cairnline: portable project coordination](proposals/cairnline-portable-project-coordination.md)        | Proposal           | Projects, context, and memory |
| [Workflow runbooks v0](proposals/workflow-runbooks-v0.md)                                               | Proposal           | Workflow runbooks             |
| [Hecate Chat and model capabilities](accepted/hecate-chat-model-capabilities.md)                        | Accepted           | Agent and chat runtime        |
| [External Agent integrations](accepted/external-agent-integrations.md)                                  | Accepted           | Agent and chat runtime        |
| [ADK and A2A alignment](proposals/adk-a2a-alignment.md)                                                 | Proposal           | Agent and chat runtime        |
| [External Agent approval loop v1](implemented/external-agent-approval-loop-v1.md)                       | Implemented record | Agent and chat runtime        |
| [Agent event protocol v1](candidates/event-protocol-v1.md)                                              | Candidate          | Runtime contracts             |
| [Artifact storage v1](candidates/artifact-storage-v1.md)                                                | Candidate          | Runtime contracts             |
| [Plugin architecture](proposals/plugin-architecture.md)                                                 | Proposal           | Extension surface             |
| [CLI structure](proposals/cli-structure.md)                                                             | Proposal           | Platform and CLI              |
| [Migration CLI](proposals/migration-cli.md)                                                             | Proposal           | Platform and CLI              |
| [Provider response extensions](proposals/provider-response-extensions.md)                               | Proposal           | Platform and CLI              |
| [Autoresearch](proposals/autoresearch.md)                                                               | Proposal           | Future research               |
| [Import external chat history](proposals/import-external-chat-history.md)                               | Proposal           | Future research               |
| [Embeddings](proposals/embeddings.md)                                                                   | Proposal           | Future research               |
| [Agent event protocol extensions](parking-lot/event-protocol-extensions.md)                             | Parking lot        | Future research               |

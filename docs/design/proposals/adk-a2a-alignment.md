# ADK And A2A Alignment

> **Status:** proposed.
> **Current source of truth:** [Agent runtime](../../runtime/agent-runtime.md),
> [External Agents](../../runtime/external-agents.md),
> [MCP integration](../../runtime/mcp.md),
> [Chat sessions](../../runtime/chat-sessions.md), and
> [Workspace instructions, skills, and profiles](workspace-instructions-skills-and-profiles.md)
> for today's runtime, adapter, tool, chat, context, and profile behavior.
> **Next action:** prototype an A2A protocol-adapter spike around an Agent
> Card, task/run mapping, and streaming projection before adding any ADK or
> A2A SDK dependency.

Hecate should align more closely with the emerging ADK and A2A vocabulary, but
without giving up the local-first runtime and operator-control model that make
Hecate useful. The practical split:

- **ADK is a source of reusable concepts and optional interop targets.** Hecate
  should borrow the useful vocabulary around agents, sessions, state, tools,
  memory, artifacts, workflows, evaluation, and developer inspection. It should
  not replace `agent_loop` with ADK or turn ADK into the core runtime.
- **A2A is a candidate protocol adapter.** Hecate should treat A2A like ACP,
  MCP, OpenAI-compatible HTTP, and Anthropic Messages: a way for another system
  to talk to or from Hecate, not a provider or replacement execution model.

## External Alignment

Facts, accessed 2026-06-08:

- ADK describes itself as an open-source framework for building, debugging,
  evaluating, and deploying agents across Python, TypeScript, Go, Java, and
  Kotlin. Source: <https://adk.dev/>.
- ADK's core concepts include agents, tools, callbacks, sessions/state, memory,
  artifacts, code execution, planning, models, events, and runners. Source:
  <https://adk.dev/get-started/about/>.
- ADK's A2A docs recommend A2A when communicating with standalone services,
  agents maintained by other teams, cross-language systems, or components that
  need a strong formal contract; local sub-agents are preferred for in-process
  helper behavior. Source: <https://adk.dev/a2a/intro/>.
- ADK Go can expose an ADK agent through an A2A launcher and generate an Agent
  Card for the server. Source:
  <https://adk.dev/a2a/quickstart-exposing-go/>.
- A2A is documented as an open standard for communication and collaboration
  between independent, often opaque, agents. Source:
  <https://a2a-protocol.org/latest/>.
- A2A's core concepts include Agent Cards, Tasks, Messages, Parts, Artifacts,
  `contextId`, HTTP transport, JSON-RPC payloads, and standard auth through
  HTTP headers. Source:
  <https://a2a-protocol.org/latest/topics/key-concepts/>.
- A2A treats MCP as complementary: MCP is agent-to-tool, while A2A is
  agent-to-agent collaboration. Source:
  <https://a2a-protocol.org/latest/topics/a2a-and-mcp/>.
- A2A supports SSE for long-running tasks and push notifications for
  disconnected scenarios. Source:
  <https://a2a-protocol.org/latest/topics/streaming-and-async/>.
- Official A2A SDKs exist for multiple languages, including Go. Source:
  <https://a2a-protocol.org/latest/sdk/>.

## Fit With Hecate

Hecate already separates the pieces that ADK and A2A tend to bundle together:

| Hecate concept   | Current role                                                                                                                      | ADK / A2A alignment                                                                                   |
| ---------------- | --------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Model provider   | OpenAI, Anthropic, Ollama, LM Studio, and compatible backends answer LLM calls.                                                   | Keep separate. ADK and A2A agents are not model providers.                                            |
| Native runtime   | `agent_loop` runs Hecate-owned tool loops with approvals, artifacts, events, cost accounting, and sandboxed tool calls.           | Borrow ADK concepts for runtime policy, workflow graphs, and evaluation; do not replace this runtime. |
| Agent adapter    | ACP-backed Codex, Claude Code, Cursor Agent, Grok Build, and future opaque coding agents.                                         | A remote A2A agent can become another External Agent target.                                          |
| Protocol adapter | ACP, MCP, OpenAI-compatible HTTP, Anthropic Messages.                                                                             | A2A belongs here.                                                                                     |
| MCP              | Hecate as an MCP server and as an MCP client for external tools.                                                                  | Keep MCP as the agent-to-tool/control-plane protocol. A2A should not displace MCP.                    |
| Agent Preset     | Saved runtime posture: model/provider hints, tools, approvals, skills, memory/context activation, and system/preset instructions. | Align preset fields with ADK's agent/tool/session vocabulary where useful.                            |
| Runbook          | Named workflow pattern with inputs, evidence, approvals, and stop conditions.                                                     | Borrow ADK graph/workflow and evaluation ideas as Hecate-native runbooks.                             |

This keeps the accepted external-agent distinction intact: providers answer LLM
calls, agent adapters drive coding-agent loops, and protocol adapters define
how systems talk to Hecate.

## Reusable ADK Ideas

Hecate can adopt several ADK-shaped ideas without importing the ADK runtime.

### Agent Vocabulary

ADK's `Agent`, `Tool`, `Runner`, `Session`, `State`, `Memory`, and `Artifact`
terms are useful names for ideas Hecate already has. Hecate should use this
vocabulary where it clarifies public docs and UI labels, while preserving
Hecate-specific storage and API shapes.

Suggested mapping:

| ADK concept      | Hecate-native shape                                                                             |
| ---------------- | ----------------------------------------------------------------------------------------------- |
| Agent            | Hecate Agent Preset, Hecate-owned `agent_loop`, or External Agent adapter depending on context. |
| Tool             | Built-in task tool or namespaced MCP tool.                                                      |
| Runner           | Orchestrator plus task runner.                                                                  |
| Session          | Chat session, task context, or External Agent native session metadata.                          |
| State            | Context packet, run state, session metadata, and runtime records.                               |
| Memory           | Operator-approved Hecate memory scoped by project/profile.                                      |
| Artifact         | Hecate task artifact, chat diff artifact, or future portable artifact storage record.           |
| Workflow / graph | Hecate runbook steps and evidence requirements.                                                 |
| Evaluation       | Hecate replay/eval suites for `agent_loop`, chat, and External Agent turns.                     |

### Agent Presets

Agent Presets should be easy to compare to an ADK agent definition:

- name, description, and intended use
- model/provider or adapter hint
- system/profile instruction
- enabled skills
- tool and MCP posture
- approval policy
- memory/context source policy
- runtime limits
- evaluation fixtures or smoke prompts

Profiles remain Hecate-owned. They should not become ADK config files, but a
future export/import bridge can translate the portable subset.

### Workflow Graphs And Runbooks

ADK's graph and workflow-agent direction maps well to Hecate runbooks:
deterministic checkpoints around adaptive model calls. Hecate should borrow
these patterns as runbook metadata:

- sequential, parallel, and loop steps
- explicit route conditions
- human-input gates
- expected artifacts
- stop conditions
- evidence requirements
- replay/evaluation datasets

The execution substrate should remain Hecate tasks/runs at first. A new
workflow engine should wait until runbook experiments prove they need more than
task metadata and artifacts.

### Evaluation And Replay

ADK's built-in evaluation emphasis is worth adopting more directly. Hecate
should grow eval fixtures for:

- agent-loop tool selection and final-answer quality
- approval-pause/resume behavior
- External Agent transcript normalization
- A2A protocol mapping
- runbook outputs and evidence completeness
- context packet stability across profile or project changes

These should use Hecate's existing fake providers, fake adapters, task stores,
and UI fixtures before adding external eval services.

## A2A Adoption Shape

A2A should enter Hecate as a protocol adapter in slices.

### Slice 1: Hecate Agent Card

Expose a Hecate Agent Card for local or authenticated clients.

Candidate endpoints:

```text
GET /.well-known/agent-card.json
GET /hecate/v1/a2a/agent-card
```

The `/.well-known` endpoint should be disabled by default or minimal until the
security posture is accepted. The Hecate-native endpoint can require the same
runtime token used by other local control-plane APIs.

The card should describe Hecate-level skills such as:

- create a supervised Hecate task
- continue a Hecate Chat session
- inspect task/run status
- stream task progress
- list recent tasks
- cancel a run
- resolve an approval
- inspect trace summaries

Avoid publishing sensitive workspace paths, provider names, custom MCP server
details, or private project metadata in an unauthenticated card.

### Slice 2: A2A Server Adapter

Implement a local/authenticated A2A JSON-RPC adapter backed by existing Hecate
APIs and stores.

Candidate method mapping:

| A2A method             | Hecate behavior                                                                            |
| ---------------------- | ------------------------------------------------------------------------------------------ |
| `SendMessage`          | Create or continue a Hecate task/chat turn and return either a message or a task snapshot. |
| `SendStreamingMessage` | Start the same work and project existing task/chat SSE updates into A2A stream events.     |
| `GetTask`              | Return a run/task snapshot using A2A task state and artifact shapes.                       |
| `ListTasks`            | Return recent Hecate runs/tasks scoped by context or status.                               |
| `CancelTask`           | Cancel the mapped Hecate run.                                                              |
| `SubscribeToTask`      | Reattach to a non-terminal run stream using Hecate's replayable event sequence.            |

V1 should skip A2A push notifications. Webhook callbacks introduce SSRF,
authentication, replay, and lifecycle concerns that Hecate does not need for
local operator flows.

### Slice 3: Remote A2A Agents As External Agents

Connections can later accept a directly configured A2A Agent Card URL:

```text
Hecate Chats
  -> Target: External Agent
  -> Protocol: A2A
  -> Remote Agent Card URL
  -> Prompt
  -> A2A task/message stream
```

Rules:

- Direct configuration first. No broad registry or domain crawling in V1.
- Fetch and display the Agent Card during readiness checks.
- Store auth configuration explicitly and redact it through the existing
  control-plane secret model.
- Treat the remote agent as opaque, the same way ACP-backed agents are opaque.
- Do not forward Hecate provider credentials by default.
- Do not grant workspace access unless the operator explicitly configures a
  workspace-sharing mechanism.
- Surface remote A2A cost as `external` / `unknown` unless structured usage is
  reported and mapped.

This lets Hecate consume ADK-built agents through A2A without adopting ADK
itself.

### Slice 4: Optional SDK Reuse

Before implementing A2A by hand, audit the official Go SDK and its transitive
dependencies. The current Hecate Go toolchain is new enough for the public
`a2a-go` requirement noted in its README, but compatibility is only one part
of the decision.

Adopt an SDK only if it helps with:

- protocol type correctness
- Agent Card parsing/validation
- JSON-RPC and SSE edge cases
- conformance tests
- future gRPC support without duplicating protocol code

Do not adopt an SDK if it forces:

- a second runtime model
- hosted/multi-tenant assumptions
- uncontrolled auth behavior
- broad dependency churn
- loss of Hecate's existing event, approval, or artifact semantics

## Semantic Mapping

A2A's lifecycle should map to Hecate without pretending the two models are the
same.

| A2A concept      | Hecate mapping                                                                                                               |
| ---------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| Agent Card       | Hecate capability document for local/authenticated clients, or remote External Agent descriptor when consumed.               |
| AgentSkill       | Hecate actions such as create task, continue chat, inspect run, cancel run, resolve approval, inspect traces.                |
| `contextId`      | Hecate chat session id, project-scoped conversation id, or future task context id.                                           |
| `taskId`         | Prefer Hecate run id for protocol identity; include Hecate task id as metadata.                                              |
| Task             | A snapshot of one unit of work. Terminal A2A tasks are immutable; Hecate retries/follow-ups should produce new A2A task ids. |
| Message          | Chat or task turn message.                                                                                                   |
| Part             | Hecate content block or artifact part.                                                                                       |
| Artifact         | Hecate task artifact, final answer, stdout/stderr artifact, file/diff artifact, or future portable artifact record.          |
| `input-required` | Hecate `awaiting_approval` or explicit clarification-needed state, depending on source.                                      |
| `auth-required`  | Provider, adapter, MCP, or remote-agent auth failure surfaced as a stable state/error.                                       |
| SSE stream       | Existing task-run or chat-session SSE projected into A2A `TaskStatusUpdateEvent` and `TaskArtifactUpdateEvent`.              |

Status mapping sketch:

| Hecate status       | A2A task state      |
| ------------------- | ------------------- |
| `queued`            | submitted / working |
| `running`           | working             |
| `awaiting_approval` | input-required      |
| `completed`         | completed           |
| `cancelled`         | canceled            |
| `failed`            | failed              |
| approval rejected   | rejected            |

The exact enum spelling should follow the current A2A SDK/spec at
implementation time.

## Security Posture

The A2A surface must keep Hecate's local operator boundary.

- Default bind remains loopback.
- Public discovery is disabled or minimal by default.
- Detailed Agent Cards require authentication.
- Runtime-token and future client identity checks apply before state-changing
  methods.
- Every A2A write maps to existing Hecate authorization, policy, sandbox, and
  approval paths.
- Remote A2A agents do not receive provider secrets, MCP secrets, workspace
  paths, project memory, or private context unless explicitly configured.
- Push notifications are deferred until there is a separate SSRF and webhook
  authentication design.
- A2A protocol payloads must be logged and retained under the same redaction
  rules as chat/task payloads.

## Non-goals

- Replacing `agent_loop` with ADK.
- Moving provider routing into ADK.
- Treating remote A2A agents as model providers.
- Removing ACP support for local coding-agent CLIs.
- Removing MCP support for tools and editor control-plane integrations.
- Publishing a public A2A registry or marketplace.
- Supporting hosted multi-user A2A semantics in the first version.
- Implementing A2A push notifications in the first version.

## Implementation Plan

1. **Dependency audit.** Inspect `a2a-go`, ADK Go, and their dependency trees.
   Decide whether Hecate should use an SDK or define a narrow internal adapter.
2. **Protocol fixtures.** Add JSON fixtures for Agent Cards, `SendMessage`,
   streaming status updates, artifact updates, cancellation, and auth errors.
3. **Agent Card endpoint.** Add a token-protected Hecate Agent Card endpoint
   with conservative skills and no sensitive workspace/project data.
4. **Server adapter spike.** Implement `SendMessage`, `GetTask`, `CancelTask`,
   and `SendStreamingMessage` against existing task/chat APIs in a narrow
   package.
5. **Stream projection.** Reuse existing task-run/chat SSE projection. Do not
   fork a second event source.
6. **Remote A2A readiness.** Add a direct-config remote Agent Card probe in
   Connections without broad discovery.
7. **Remote A2A External Agent.** Treat a configured remote A2A service as an
   External Agent target, with transcript, diagnostics, cancellation, and
   `external` usage semantics.
8. **ADK interop smoke.** Use a tiny ADK A2A sample as a manual or optional e2e
   smoke to prove Hecate can consume ADK-built agents through A2A.

## Open Questions

- Should the public-looking `/.well-known/agent-card.json` endpoint exist at all
  before hosted/multi-user hardening, or should Hecate start only with
  `/hecate/v1/a2a/agent-card`?
- Should A2A `taskId` be the Hecate run id, or a new protocol-specific id that
  references both task id and run id?
- Should a Hecate Chat session and a Hecate task share one A2A `contextId`
  namespace?
- How should Hecate represent approval grants and repeated approvals in A2A
  without leaking internal policy details?
- Which artifacts should be exposed to A2A clients by default, and which should
  require explicit operator selection?
- Is the official A2A Go SDK stable enough for Hecate's release cadence, or is
  a narrow internal JSON-RPC implementation safer for the first spike?
- What minimal ADK-compatible export/import shape would help users without
  implying Hecate is an ADK runtime?

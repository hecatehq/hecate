# Cairnline: Portable Project Coordination

> **Status:** proposed.
> **Current source of truth:** [Projects](../accepted/projects.md),
> [Runtime API](../../runtime/runtime-api.md),
> [MCP](../../runtime/mcp.md), [External Agents](../../runtime/external-agents.md),
> [Context assembly and injection boundaries](context-assembly-and-injection-boundaries.md),
> [Agent memory](agent-memory.md), and
> [Workspace instructions, skills, and profiles](workspace-instructions-skills-and-profiles.md)
> for today's Hecate project, context, memory, skills, and agent-supervision
> behavior.
> **Next action:** use Cairnline beside Hecate and begin embed experiments, while
> Hecate remains the richer incubator for cockpit and orchestration behavior.

Hecate Projects V1 has grown into a useful local coordination substrate:
durable project identity, roots, roles, work items, assignments, evidence,
reviews, handoffs, memory candidates, skills metadata, setup, context
inspection, and closeout readiness. Those concepts are not inherently
Hecate-specific. They can also help any MCP-capable agent coordinate project
work without adopting Hecate's model gateway, task runtime, External Agent
supervisor, approval implementation, or operator UI.

This proposal documents a design-first extraction path for **Cairnline**, a
standalone, agent-neutral project coordination server exposed over MCP. A local
standalone scaffold exists at [hecatehq/cairnline](https://github.com/hecatehq/cairnline),
including a public embeddable Go API, but this remains a proposal for Hecate
integration and long-term extraction boundaries, not current Hecate runtime
behavior.

## Summary

The standalone product should be:

> Cairnline is a local-first coordination server for human and AI work. It
> provides durable project identity, work coordination, context metadata,
> evidence, reviews, handoffs, and memory candidates without assuming any
> specific agent host can be launched or supervised.

Hecate remains the incubator until the portable contracts stabilize. The
standalone implementation targets Go and SQLite, ships an MCP stdio server
first, and keeps Project Assistant as an optional proposal module layered on top
of core coordination state.

The core rule:

> Assignment is coordination. Execution is capability-dependent.

The server can queue, claim, resolve context, record evidence, track status,
and emit launch packets. It must not assume it can launch agents. Hecate or
another orchestrator may later launch and supervise compatible agents.

## Replacement Readiness

Cairnline is ready for **side-by-side dogfood** and small Hecate embed
experiments once Hecate imports the public root package
`github.com/hecatehq/cairnline`. It is not ready to replace Hecate's Projects
backend yet.

Hecate should replace its internal Projects stores with Cairnline only after:

- Cairnline covers Hecate's current project, role, profile, skill, work item,
  assignment, evidence, review, handoff, memory-candidate, root, and closeout
  flows.
- Hecate has an adapter from Hecate task / External Agent execution records to
  Cairnline assignment coordination records.
- Hecate can migrate or import/export existing local project state.
- Workspace root, worktree, evidence-link, and source-locator boundaries have
  a dedicated security review in the Cairnline API.
- At least one real Hecate project has been dogfooded end to end through
  Cairnline-backed coordination.
- Hecate's Projects UI can consume Cairnline state without losing current
  onboarding, attention, context-inspection, review, handoff, or closeout
  behavior.

The intended order is therefore:

```text
side-by-side MCP server
-> embedded read/write experiment
-> parity adapter behind a feature flag
-> state migration/import-export
-> replacement after dogfood
```

## Product Boundary

Cairnline owns coordination state:

| Concept           | Portable meaning                                                                                 |
| ----------------- | ------------------------------------------------------------------------------------------------ |
| Project           | Durable identity for any body of work, not necessarily code, GitHub, or a folder.                |
| Root / workspace  | Optional concrete filesystem location used only when local files matter.                         |
| Role              | Project-native responsibility such as architect, implementer, reviewer, researcher, or operator. |
| Agent profile     | Portable behavior and context policy for an agent.                                               |
| Execution profile | Optional host/runtime-specific hints such as model, provider, tools, writes, network, approvals. |
| Skill metadata    | Referenced capability/instruction metadata only; no body injection or execution in core.         |
| Work item         | Reviewable unit of work.                                                                         |
| Assignment        | Durable coordination record binding work item, role, profile, and desired execution mode.        |
| Evidence/review   | Structured collaboration artifacts attached to work or assignment state.                         |
| Handoff           | Structured transfer from one role, agent, or work context to another.                            |
| Memory candidate  | Proposed durable memory awaiting explicit approval.                                              |
| Context snapshot  | Inspectable record of project/work/profile/source metadata assembled for an agent-facing action. |

Hecate-specific runtime concerns stay outside the portable core:

- model gateway and provider routing
- Hecate Task runtime and `agent_loop`
- Hecate's approval implementation
- External Agent supervisor internals
- trace viewer and OpenTelemetry UI
- Hecate operator shell/chrome
- provider credentials, browser cookies, secrets, and external-agent private
  memory

## Assignment And Execution Ladder

The standalone server should support a ladder of execution modes instead of a
single launch model:

```text
manual
-> mcp_pull
-> external_adapter
-> orchestrated
```

### Manual

The project records the work, role, context, and expected output. The operator
manually starts an agent or human workflow and later records evidence, review,
handoff, or completion state. This supports agents with no MCP support.

### MCP Pull

An MCP-capable agent connects to the server, asks for compatible work, claims an
assignment, reads its context, performs work in its own host, records evidence
or handoffs, and completes or fails the assignment. This is the first portable
agent path and should be the standalone server's primary V0 value.

### External Adapter

An orchestrator with agent adapters can create or claim an assignment, resolve
context, and launch a compatible external agent. The Projects server records
execution refs and state transitions, but adapter lifecycle remains outside
core. Hecate can be the first orchestrator implementation.

### Orchestrated

A richer orchestrator coordinates multiple agents, assignments, reviews, and
handoffs across a project. Cairnline should provide the durable
coordination state; orchestration policy and launch authority belong to the
orchestrator.

## Minimal MCP Surface

The V0 MCP surface should be small, explicit, and tool-permission friendly:

```text
projects.list
projects.get
projects.create
projects.update

roles.list
roles.create
roles.update

profiles.list
profiles.create
profiles.update

execution_profiles.list
execution_profiles.create
execution_profiles.update

skills.list
skills.discover
skills.update

work_items.list
work_items.create
work_items.update
work_items.closeout_readiness

assignments.list
assignments.create
assignments.claim
assignments.context
assignments.launch_packet
assignments.update_status
assignments.complete

evidence.record
reviews.record
handoffs.create
memory_candidates.create

assistant.propose   optional
assistant.apply     optional, confirmed actions only
```

Resources can expose read-mostly views such as project summaries, assignment
context packets, closeout readiness, and recent evidence. Mutating tools should
be separately permissionable by the MCP host.

## Minimum Data Shapes

These shapes are sketches for the extraction contract, not final Hecate API
types.

```ts
Assignment {
  id
  project_id
  work_item_id
  role_id
  root_id?
  profile_id?
  execution_profile_id?
  execution_mode: "manual" | "mcp_pull" | "external_adapter" | "orchestrated"
  status: "queued" | "claimed" | "running" | "awaiting_review" | "completed" | "failed" | "cancelled"
  desired_agent?: {
    kind: "codex" | "claude" | "cursor" | "hecate" | "human" | "any"
    skill_ids?: string[]
  }
  claimed_by?
  execution_ref?
  context_snapshot_id?
}
```

```ts
Role {
  id
  project_id
  name
  description
  instructions
  default_profile_id?
  default_skill_ids[]
  default_execution_mode?
}
```

```ts
AgentProfile {
  id
  name
  description
  instructions
  context_policy
  memory_policy
  source_policy
  skill_ids[]
}
```

```ts
ExecutionProfile {
  id
  agent_kind?: "codex" | "claude" | "cursor" | "hecate" | "human" | "any"
  model_hint?
  provider_hint?
  tools_policy?
  writes_policy?
  network_policy?
  approval_policy?
  adapter_options?
}
```

Profiles deliberately split into portable behavior/context policy and optional
execution/runtime hints. A generic MCP client can use the portable profile and
ignore runtime hints it does not understand. An orchestrator can interpret an
execution profile through its own policy layer.

## Project Assistant

Project Assistant should not be mandatory core. It belongs in the future repo
as an optional proposal engine:

- `assistant.propose` suggests changes such as first work items, assignments,
  roles, handoffs, or memory candidates.
- `assistant.apply` applies confirmed actions only.
- Core validates and applies durable mutations.
- Assistant output is reviewable proposal data, not hidden execution.

The assistant must not automatically promote memory, auto-dispatch handoffs, or
launch agents. Those remain explicit operator or orchestrator actions.

## Implementation Direction

### 1. Proposal In Hecate

- Add this design record in Hecate's proposal bucket.
- Link it from the Projects/context track in the design index.
- Keep the record clear that this is future direction, not current runtime
  behavior.

### 2. Contract Extraction Pass

- Identify Hecate-specific fields in `projects`, `projectwork`,
  `projectskills`, `projectassistant`, agent profiles, and context packets.
- Separate portable coordination concepts from Hecate runtime execution
  concepts.
- Define stable MCP tool names, resource names, IDs, status transitions, and
  context packet vocabulary.
- Decide whether Hecate embeds the portable core or talks to it as an MCP
  server after the first standalone release.

### 3. Cairnline Repo V0

- Repo: [hecatehq/cairnline](https://github.com/hecatehq/cairnline).
- Runtime: Go.
- Storage: SQLite first.
- Transport: MCP stdio server first.
- Public embedding surface: root Go package
  `github.com/hecatehq/cairnline`.
- Scope: core coordination state plus MCP tools/resources and a stable
  embeddable service facade.
- Exclusions: no web UI, no model gateway, no Hecate task runtime, no
  external-agent launcher, no hosted team permissions.

### 4. Hecate Integration

- Hecate can initially continue using its internal Projects implementation.
- Hecate can use the MCP server for side-by-side interoperability and the
  public Go package for controlled embed experiments.
- After V0 stabilizes, Hecate may embed the portable core as its Projects
  backend or talk to the MCP server as a separate local coordination process.
- Hecate remains the richer cockpit and orchestrator for supervised Hecate
  Tasks and External Agents.
- Hecate integration tests should start only after Hecate consumes the
  standalone core or server.

### 5. Assistant Module

- Add as a separate package/toolset after the core coordination server is
  usable.
- Keep deterministic proposal/apply mechanics first.
- Add model-assisted drafting only after proposal validation and audit behavior
  are stable.

## Security And Boundaries

- The standalone server is local-first and single-operator by default.
- MCP tools mutate durable project state, so host tool permissions matter.
- Workspace roots are optional and must be path-confined when used.
- Skill metadata is not permission to execute tools or inject `SKILL.md`
  bodies.
- Evidence URLs and source locators are stored as operator-provided metadata;
  clients validate schemes before rendering them as links.
- Secrets, cookies, provider credentials, and external-agent private memory are
  out of scope for core Projects.
- Orchestrators must not treat assignment metadata as authorization to bypass
  their own approvals, sandboxing, network policy, or write policy.
- Context snapshots are audit/evidence records. They are not durable memory
  until an operator promotes memory candidates.

## Future Test Plan

Standalone implementation should include:

- Core store tests for projects, roles, profiles, work items, assignments,
  artifacts, handoffs, and memory candidates.
- SQLite migration and persistence tests.
- MCP contract tests for list, create, update, claim, context, and complete
  flows.
- Rootless project journey: create project, create ownerless work item, record
  evidence, close.
- MCP pull journey: create assignment, compatible agent claims, reads context,
  records evidence, completes.
- Manual launch-packet journey for non-MCP agents.
- Conflict tests for assignment claim races.
- Permission and path-confinement tests for workspace roots.
- Hecate integration tests only after Hecate starts consuming the standalone
  core or server.

## Assumptions

- Extraction is design-first, not immediate code movement.
- First standalone implementation is Go plus SQLite.
- Project Assistant is optional, not mandatory core.
- Hecate-specific runtime concepts stay out of the portable core.
- The first interoperability value is MCP pull and manual modes; full
  orchestration comes later.

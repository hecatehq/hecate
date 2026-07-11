# Cairnline: Portable Project Coordination

> **Status:** implemented boundary; retained as the extraction design. Hecate
> now embeds Cairnline as the sole portable Projects coordination authority.
> The accepted Projects design and Runtime API docs are the source of truth for
> current behavior.
> **Current source of truth:** [Projects](../accepted/projects.md),
> [Runtime API](../../runtime/runtime-api.md),
> [MCP](../../runtime/mcp.md), [External Agents](../../runtime/external-agents.md),
> [Context assembly and injection boundaries](context-assembly-and-injection-boundaries.md),
> [Agent memory](agent-memory.md), and
> [Workspace instructions, skills, and profiles](workspace-instructions-skills-and-profiles.md)
> for today's Hecate project, context, memory, skills, and agent-supervision
> behavior.
> **Next action:** preserve the authority split while Hecate evolves as the
> richer cockpit and orchestrator; consider a separately installed connector
> only after the embedded contract is stable.

Hecate Projects V1 has grown into a useful local coordination substrate:
durable project identity, roots, roles, work items, assignments, evidence,
reviews, handoffs, memory candidates, skills metadata, setup, context
inspection, and closeout readiness. Those concepts are not inherently
Hecate-specific. They can also help any MCP-capable agent coordinate project
work without adopting Hecate's model gateway, task runtime, External Agent
supervisor, approval implementation, or operator UI.

This proposal documents a design-first extraction path for **Cairnline**, a
standalone, agent-neutral project coordination server exposed over MCP.
[hecatehq/cairnline](https://github.com/hecatehq/cairnline) now ships the
standalone server and public embeddable Go API, and Hecate imports that package
as its Projects coordination authority. This record preserves the extraction
boundaries and long-term product shape.

## Summary

The standalone product should be:

> Cairnline is a local-first coordination server for human and AI work. It
> provides durable project identity, work coordination, context metadata,
> evidence, reviews, handoffs, and memory candidates without assuming any
> specific agent host can be launched or supervised.

Cairnline uses Go and SQLite, ships an MCP stdio server and embeddable service,
and keeps Project Assistant as an optional proposal module layered on top of
core coordination state.

The core rule:

> Assignment is coordination. Execution is capability-dependent.

The server can queue, claim, resolve context, record evidence, track status,
and emit launch packets. It must not assume it can launch agents. Hecate or
another orchestrator may later launch and supervise compatible agents.

## Implementation Status

The extraction is complete for Hecate's production Projects authority. Hecate
embeds Cairnline and uses it as the sole portable coordination store behind the
existing Projects API and UI. Hecate no longer exposes the temporary backend
selector, dual-write mirror, parity dashboard, migration/rollback endpoints, or
sidecar smoke routes used while proving the boundary.

The live split is:

```text
Cairnline
  project identity · roots · sources · skills · roles
  work items · assignments · artifacts · handoffs
  accepted project memory · memory candidates · assistant proposals

Hecate
  Agent Presets · provider/model/runtime policy
  tasks · External Agent sessions · approvals · sandbox
  runtime refs · context snapshots · traces · operator UI
```

No alpha data migration is required. New project coordination state is created
in Cairnline; disposable pre-cutover dogfood state may be reset. A separately
installed Cairnline connector remains future work and must preserve the same
authority split.

## Product Boundary

Cairnline owns coordination state:

| Concept          | Portable meaning                                                                                         |
| ---------------- | -------------------------------------------------------------------------------------------------------- |
| Project          | Durable identity for any body of work, not necessarily code, GitHub, or a folder.                        |
| Root / workspace | Optional concrete filesystem location used only when local files matter.                                 |
| Role             | Project-native responsibility such as architect, implementer, reviewer, researcher, or operator.         |
| Desired agent    | Portable hint about who or what should claim work, for example `codex`, `claude`, `human`, or `any`.     |
| Skill metadata   | Referenced capability/instruction metadata only; no body injection or execution in core.                 |
| Work item        | Reviewable unit of work.                                                                                 |
| Assignment       | Durable coordination record binding work item, role, desired agent metadata, and desired execution mode. |
| Evidence/review  | Structured collaboration artifacts attached to work or assignment state.                                 |
| Handoff          | Structured transfer from one role, agent, or work context to another.                                    |
| Memory candidate | Proposed durable memory awaiting explicit approval.                                                      |
| Context snapshot | Inspectable record of project/work/source metadata assembled for an agent-facing action.                 |

Hecate-specific runtime concerns stay outside the portable core:

- model gateway and provider routing
- Hecate Task runtime and `agent_loop`
- Hecate's approval implementation
- External Agent supervisor internals
- trace viewer and OpenTelemetry UI
- Hecate operator shell/chrome
- provider credentials, browser cookies, secrets, and external-agent private
  memory
- agent presets, runtime profiles, provider/model settings, sandbox policy, and
  launch permissions

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

skills.list
skills.discover
skills.update

work_items.list
work_items.create
work_items.update
work_items.closeout_readiness

assignments.list
assignments.create
assignments.update
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
  execution_mode: "manual" | "mcp_pull" | "external_adapter" | "orchestrated"
  status: "queued" | "claimed" | "running" | "awaiting_approval" | "awaiting_review" | "completed" | "failed" | "cancelled"
  desired_agent?: {
    kind: "codex" | "claude" | "cursor" | "hecate" | "human" | "any"
    skill_ids?: string[]
  }
  claimed_by?
  execution_ref?: {
    kind?
    task_id?
    run_id?
    session_id?
    trace_id?
    pending_approvals?
  }
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
  default_skill_ids[]
  default_execution_mode?
}
```

Cairnline should not expose agent presets or runtime profiles as portable MCP
concepts. Hecate can map Cairnline roles, desired-agent hints, and skill ids to
Hecate-owned Agent Presets and runtime launch policy at its orchestration
boundary.

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
  `projectskills`, `projectassistant`, Agent Presets, and context packets.
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

- Hecate embeds Cairnline's Go service and uses its SQLite store as the sole
  portable Projects coordination authority.
- Hecate preserves its `/hecate/v1/projects*` API and Projects UI as a native
  facade, so operator workflows do not depend on MCP client semantics.
- The bridge maps Cairnline records into Hecate API views and combines them with
  Hecate-owned Agent Presets, launch policy, task/chat references, context
  snapshots, approvals, and traces.
- Assignment start remains a Hecate orchestrator action. Cairnline records
  intent and lifecycle state; Hecate validates and performs task, External
  Agent, workspace, and Git side effects.
- Temporary native-backend, mirror, parity, migration, rollback, and sidecar
  diagnostic surfaces are removed from the production contract.
- A future separately installed Cairnline connector may replace the embedded
  service boundary without moving Hecate runtime authority into Cairnline.

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

- Core store tests for projects, roles, work items, assignments,
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

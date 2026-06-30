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
  assignment, generic artifact, evidence, review, handoff, memory-candidate,
  assistant-proposal ledger and committed apply side effects, root, and
  closeout flows.
- Cairnline skill discovery remains compatible with Hecate's current
  `.agents/skills`, `.hecate/skills`, and enabled guidance-linked local skill
  roots while keeping Cairnline-native `.cairnline/skills` available for
  standalone projects.
- Hecate project-level and role-level default profile/provider/model posture is
  preserved as Cairnline project/role profile and execution-profile defaults,
  so assignment launch packets do not silently fall back to agent-profile
  execution hints when a project or role is more specific; Hecate's
  replacement-readiness parity report counts execution profiles so this
  posture mapping is observable.
- Hecate has an adapter from Hecate task / External Agent execution records to
  Cairnline assignment coordination records.
- Hecate can migrate or import/export existing local project state.
- Workspace root, worktree, evidence-link, and source-locator boundaries keep a
  documented security boundary in the Cairnline API. The current Cairnline
  package documents that boundary and tests unsafe guidance-path rejection for
  skill discovery; future local-read or locator-opening behavior needs the same
  level of review before becoming authoritative.
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
- Current Hecate sidecar experiments can probe a standalone Cairnline MCP
  command for the current portable Projects backend tool contract via
  `POST /hecate/v1/projects/cairnline/sidecar-probe` and can connect a cached
  sidecar MCP client via `POST /hecate/v1/projects/cairnline/sidecar-connect`.
  Hecate can also call read-only `projects.list`, `projects.get`,
  `assignments.context`, `assignments.launch_packet`, and the portable
  coordination list tools through local-only sidecar smoke endpoints to verify
  typed `structuredContent` for project list/detail,
  profile/skill/role/work/assignment list, assignment-context, and launch-packet
  contracts. Hecate also has an explicit confirmed sidecar lifecycle smoke that
  exercises `assignments.next`, claim, `update_status`, launch-packet read, and
  complete against the standalone sidecar database only, plus an explicit
  confirmed sidecar write smoke that creates, lists, updates, gets, deletes, and
  verifies deletion of a temporary rootless standalone Cairnline project, and an
  explicit confirmed setup smoke that creates, updates, lists, deletes, and
  verifies typed root and context-source metadata on a temporary standalone
  Cairnline project, plus an explicit confirmed work smoke that creates typed
  role, work-item, assignment, assignment-context, and launch-packet metadata on
  a temporary standalone Cairnline project, plus an explicit confirmed
  collaboration smoke that records and verifies typed artifact, evidence,
  review, and handoff metadata on a temporary standalone Cairnline project,
  plus an explicit confirmed memory smoke that creates and verifies accepted
  memory, promotes one memory candidate, rejects/deletes another candidate, and
  cleans up a temporary standalone Cairnline project, plus an explicit
  confirmed Project Assistant smoke that creates and verifies a temporary
  proposal ledger record, verifies unconfirmed apply returns
  `needs_confirmation`, applies it with explicit confirmation, verifies the
  created role/work/assignment side effects, and cleans up the temporary
  standalone Cairnline project. This remains mostly
  contract/client-lifecycle/read-shape and standalone mutation evidence. The
  narrow live-route exception is
  `HECATE_PROJECTS_CAIRNLINE_CONNECTOR=sidecar` plus
  `HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=sidecar`, which routes only project
  list/detail, setup-readiness, health, and project skill list reads through
  the cached standalone MCP client. Other live Projects reads, writes, mirrors,
  and write-authority switchpoints do not route through the sidecar yet.
- Current Hecate embed experiments can serve project list/detail, setup
  readiness, health, skills, memory, memory candidates, roles, work items,
  assignment lists, assignment context, launch-readiness, assignment preflight,
  generic/evidence/review artifact lists, handoff lists, Project Assistant
  context/proposal reads, activity, closeout readiness, and operations brief
  reads from a Cairnline-seeded read model while Hecate stores remain
  authoritative. `HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=auto` prefers the
  embedded mirror database when it already contains the requested project and
  otherwise uses the snapshot-seeded bridge; `snapshot` forces the bridge; and
  `embedded` requires a populated embedded mirror so read-route drift fails
  loudly during replacement-readiness dogfood.
- In configured Hecate embed mode, activity, work-item list/detail,
  assignment-list, and operations brief reads now render work items,
  assignments, roles, artifacts, and handoffs from Cairnline service records,
  then overlay Hecate-only runtime refs/timestamps while Hecate still owns
  execution. Outside the explicit sidecar project list/detail, setup-readiness,
  health, and project skill list read source, project identity and some
  compatibility scaffolding remain Hecate-owned until Cairnline becomes
  authoritative.
- Project Assistant draft generation can use the same Cairnline-projected
  context as the inspect endpoint, so proposal assembly is exercised against the
  portable read model while proposal persistence and apply remain Hecate-owned.
- Hecate launch-readiness and assignment preflight can read Cairnline
  project/work/assignment/role coordination records before applying Hecate-owned
  runtime validation. Native assignment preflight/start context packets can
  append inspect-only Cairnline launch-packet evidence when the read adapter is
  active, so operators can compare portable launch-packet coverage with
  Hecate's authoritative dispatch context before cutover. Hecate can also
  mirror committed assignment-start results as replacement evidence, but this
  does not make assignment-start Cairnline-backed.
- The Hecate parity report compares raw graph counts, derived activity and
  operations counts, rendered work-item route shape including embedded
  assignments, collaboration artifact/handoff route-shape counts, Project
  Assistant proposal-ledger counts, and launch-packet coverage before any
  backend switch.
- The Hecate backend-status endpoint exposes the live Cairnline read-route
  coverage, non-authoritative bridge write seams, and remaining live-route
  write-adapter gap families as structured fields, plus explicit
  `replacement_gates` and `write_switchpoints`, so replacement readiness can be
  tracked without parsing warning prose. These fields are diagnostic only:
  `replacement_ready=false` until read parity, strict embedded mirror probes,
  authoritative write switchpoints, and migration/rollback gates are ready.
- `HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY=project-memory` enables Hecate's
  first disabled-by-default Cairnline write-authority switchpoint: accepted
  project memory entry create/update/delete commits to embedded Cairnline first
  and then shadows back into Hecate-native memory stores.
  `HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY=project-memory,memory-candidates`
  also makes memory-candidate create/promote/reject Cairnline-first; the
  `memory-candidates` switch requires `project-memory` because promotion creates
  accepted project memory. Additional opt-in switchpoints can make
  project create (`project-identity`), metadata/default-only project PATCHes
  (`project-metadata-defaults`), direct root CRUD (`project-roots`),
  direct context-source CRUD
  (`project-context-sources`), collaboration/handoff routes
  (`project-collaboration`), metadata-only skill discovery/update
  (`project-skills`), role mutations (`project-roles`), work-item mutations
  (`project-work-items`), assignment record mutations (`project-assignments`),
  and global agent-profile mutations (`agent-profiles`) commit to Cairnline
  first and then shadow back into Hecate-native compatibility stores.
  Agent-profile authority writes
  Cairnline's separate portable profile and execution-posture records before
  shadowing Hecate's combined profile row. `project-identity` makes project
  create/delete commit portable identity, initial roots, context sources,
  launch defaults, and project identity removal to Cairnline first before
  shadowing Hecate's compatibility project row. Delete restores the Cairnline
  snapshot if Hecate compatibility cleanup fails. Git worktree creation side effects,
  last-opened-only updates, mixed
  metadata/root/source replacement PATCHes, and assignment start/dispatch
  remain Hecate-owned until later cutover slices. Root and context-source list
  replacement can move with the `project-roots` and `project-context-sources`
  switchpoints. Discovered root record replacement can move with
  `project-roots`, worktree-created root records can move with `project-roots`,
  and discovered context-source record replacement can move with
  `project-context-sources`, while Hecate still performs the Git/workspace
  scans and Git worktree creation for its operator UI.
- Hecate has a non-authoritative bridge write seam for project identity,
  embedded roots, root discovery/worktree-created root records, direct root
  create/update/delete, context-source discovery, direct context-source
  create/update/delete, project defaults, and project-level execution-profile
  cleanup. Hecate also has a non-authoritative project skill metadata upsert
  seam that preserves operator-disabled state and provenance without loading or
  executing skill bodies, agent-profile upsert/delete seams, role/work-item
  upsert seams, assignment metadata upsert/delete plus lifecycle-status sync,
  committed start-result mirror, linked-chat reconciliation mirror,
  create-if-missing generic artifact/evidence/review seams, handoff
  upsert/delete seams, plus accepted-memory and memory-candidate seams that
  preserve metadata, disabled state, provenance, resolved candidate state, and
  promoted memory IDs. Accepted memory, memory-candidate review,
  collaboration/handoff, metadata-only skill discovery/update, global
  agent-profile, role, work-item, and assignment-record flows can additionally
  run as opt-in Cairnline-first switchpoints above. The project
  identity/root
  discovery/worktree-creation/context-source discovery seam, the root-level
  direct root mutation seam, the source-level direct context-source mutation
  seam, the global agent-profile seam, the
  metadata-only project-skill discovery/update seam, the
  role/work-item/assignment coordination seams, the assignment start/reconcile
  result seams, the collaboration artifact create seam, the handoff mutation
  seam, the memory-candidate seam, and accepted memory when its write-authority
  switchpoint is disabled, plus the Project Assistant proposal-ledger seam, are
  now wired as best-effort live mirrors into the embedded Cairnline DB when the
  Cairnline backend is configured. Hecate still commits first and remains
  authoritative for any mutation family whose opt-in Cairnline write-authority
  switchpoint is not enabled; Project Assistant confirmed apply uses enabled
  role/work-item/assignment/handoff authority seams and remains a
  mixed-authority blocker for project/default/chat/memory/runtime side effects
  even when the proposal-ledger switchpoint is enabled. Role
  mirrors also seed referenced agent-profile metadata/execution posture when the
  profile store is available.
  Assignment-start dispatch is still a Hecate-owned write
  gap. Artifact/evidence/review update/delete semantics are absent because
  Hecate currently records those as immutable collaboration artifacts. Route
  switch points, atomic promotion semantics, migration, rollback, and the rest
  of the mutation families must land before Cairnline can become authoritative.
- Hecate can write a refreshable embedded Cairnline SQLite sync database for
  the full Projects graph as a migration rehearsal before any dual-write or
  authoritative write adapter exists. The sync response compares aggregate
  Hecate snapshot counts, normalized record ID sets, and semantic
  record-content digests with the embedded database and reports count-level,
  ID-set, and content-digest differences. Hecate also uses strict embedded
  configured-route smoke tests after sync and live-mirror parity to prove normal
  project, setup, health, skill, memory, role, work, collaboration, assistant
  context, activity, and operations reads can run from the embedded database.
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

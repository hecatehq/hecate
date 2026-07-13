# Projects

Projects are durable coordination spaces inside Hecate. Use them when you want
history, memory, roles, assignments, evidence, handoffs, and context inspection
to stay attached to one body of work over time.

A project does not have to be a GitHub project, a code repository, or even a
folder on disk. Rootless projects work for planning, research, writing, ops, and
design. Add a local folder only when Hecate should discover workspace guidance,
register local skills, or launch work against files.

## First Setup

1. Create a project with a clear name and optional purpose.
2. Attach a local folder only if the project starts from files.
3. Set provider, model, Agent Preset, memory, and source defaults when the
   project should launch Hecate Chat, Hecate Tasks, or External Agents.
4. Run **Set up project** to discover portable workspace guidance and local
   skill metadata, then review the proposed memory candidates and role changes
   before applying them.
5. Create the first work item once the setup checklist looks right.

Setup is reviewable. Hecate may propose roles or memory candidates, but the
operator applies or dismisses them. Setup does not launch agents, write project
memory automatically, install skills, or inject skill bodies into prompts.
The onboarding checklist is backed by
`GET /hecate/v1/projects/{id}/setup-readiness`, a read-only server projection
over project defaults, roots, context sources, memory, memory candidates,
skills, roles, and work items. Use the checklist for missing settings or
first-work creation; use the primary **Set up project** action for discovery
and role/memory suggestions.
When setup context exists and the project has no work items yet, first-work
creation opens with a draft title, brief, and owner role for operator review.

Project names and concrete root paths are unique in the Projects coordination store.

## Work Flow

For each work item:

1. Draft the first assignment from the owner role, or create one manually.
2. Inspect launch readiness and launch context before starting work when
   defaults, roots, or provider/model posture are uncertain.
3. Start a Hecate Task or External Agent assignment.
4. Record evidence, reviews, and handoffs as the work moves between roles.
5. Close the work item only after assignments and review follow-up are clear.

Assignments keep their own execution records. Projects coordinate the work, but
Tasks, Chats, and External Agents remain the execution surfaces.
Starting a Hecate Task assignment requires the task runtime. Preparing an
External Agent assignment uses the agent-chat adapter runtime and does not
require the Hecate task runtime to be configured.

The project header's **Needs Attention** menu is server-derived from
`GET /hecate/v1/projects/{id}/health`. It surfaces compact setup and operations
signals such as missing defaults, missing roots, Agent Preset or skill
reference issues, pending handoffs, review follow-up, stale assignment links,
empty memory/context posture, and pending memory candidates. The menu also
shows the server summary counts for setup, memory, context, and work follow-up
so the operator can see why the project needs attention before opening a
specific item. The menu opens existing surfaces only; it does not create
records, launch agents, or write memory. Like Project Operations, Needs
Attention rows use a typed server-provided `action` object for follow-through;
compact row fields are display metadata, not a second routing authority.

The default **Overview** starts with Project Operations when the server finds
actionable project state: missing launch defaults, pending approvals, blocked
or stale assignments, queued assignments that need preflight, pending handoffs,
memory candidates awaiting review, review artifacts that need follow-up,
missing completion evidence, work items ready for closeout, or work items that
need their first assignment. When no actionable work remains, the server can
offer a low-priority latest-work operation so the operator can reopen the most
recently updated work item without the client inventing a separate fallback
order. Each operation routes to an existing surface such as Project Settings,
Work, Memory, or assignment preflight.
Clients route these items through the server-provided `action.type`; `kind`
explains why the item appears, while `action` is the follow-through contract.
Draftable operations seed the normal Project Assistant proposal rail; the
operator still reviews and applies typed changes before Hecate creates durable
project records.
After a project mutation, the client reloads Overview activity, health, setup
readiness, and operations from the server. These are refreshed projections, not
locally maintained project state, and a response for a previously selected
project must not replace the current project's Overview.
The client clears the previous operation while that refresh is in flight. If
setup readiness cannot be confirmed before the first work item exists, Projects
shows an explicit retry state instead of assuming setup is complete. If work
already exists, a projection failure is announced without hiding that work, and
the operator can use **Refresh project work** to retry.
When there is no server-backed operation to show, Overview keeps only compact
activity continuity; clients should not derive a second actionable next-action
cascade from project state.
The operations brief is intentionally compact. The server sorts operations by
operator urgency before applying its item cap, and the response summary reports
how many operations were available, returned, and omitted so the UI can show
when lower-priority work is hidden by the cap.

Memory/Context source edits use typed server mutations per source. Adding,
editing, or deleting a source changes project source metadata only; it does not
read local files, fetch remote content, write memory, or change launch context
policy until the operator separately chooses a launch or Agent Preset posture
that includes enabled sources.

Use the top Project Operations action for the single most useful operator step,
then jump to blocked, active, recent, or memory-review work from the activity
summary before drilling into the full Work queue. Review follow-up and closeout operations
open selected-work detail. Closeout readiness is the server contract shared by
Project Operations and selected-work detail; the operator still creates
follow-up paths or marks work done explicitly from that surface.
Assignment launch readiness is also server-backed. Before `Start assignment`,
`Prepare chat`, or `Start from handoff`, the detail view loads
`GET /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/launch-readiness`
and uses its typed `ready`/`blockers` fields as the launch gate. The separate
preflight context packet remains inspectable evidence for the operator; it is
not parsed by the client as the readiness authority.

Preset surface is part of readiness: a native Hecate Task assignment accepts
presets for `hecate_task` or `any`, while an External Agent assignment accepts
`external_agent` or `any`. A mismatch blocks launch instead of treating the
preset as a loose hint. For native assignments, Hecate also turns the selected
preset's tools/write/network posture into the created task's runtime policy.
After launch, Task Detail shows the snapshotted preset id, effective tools,
file access, and network state in **Run overview**. Editing the preset later
does not change an existing task's retries or resumes. Read-only assignment tasks keep structured
inspection and proposal-only patch tools, but Hecate omits and rejects broad
shell, Git, direct-write, and interactive-terminal tools. A network-disabled
preset snapshot similarly omits and rejects native HTTP/search without changing
legacy or manually created task behavior. A tools-disabled snapshot exposes no
native or MCP catalog, starts no MCP host, and denies any unexpected tool call.

External Agent CLIs remain trusted local subprocesses. Their preset
write/network fields are visible launch posture, not a Hecate sandbox around
the vendor CLI; use the adapter's own controls and review the workspace diff.

For a new work item with no assignments or artifacts yet, the detail view starts
with a guided prepare action. Hecate can draft the first assignment from the
work item role and context, but the operator still reviews and applies the
proposal before execution records are created.

After a work item has activity, use its Add strip to attach more assignments,
evidence, or handoffs without scanning each section header for separate actions.

## V1 Stop Line

Projects V1 is good enough for Hecate dogfooding when an operator can:

- create a rootless planning/research/design project and manage manual work
  without attaching a workspace;
- create a workspace-backed code project, run setup, review proposed memory and
  roles, then create the first work item;
- inspect assignment context, start supervised work, record evidence or review
  artifacts, create handoffs, and close work only after blockers are clear.

Prefer dogfooding and small friction fixes over adding new Projects concepts
until those journeys break down in real Hecate development work.

## Roots And Worktrees

Project roots are concrete workspace paths, not project identity. A single
project can include a main checkout and linked Git worktrees. Work items and
assignments may select a root; launch resolution uses assignment root, then
work-item root, then project default root, then the first active root.

Root edits use typed server mutations per root. Adding, editing, or deleting a
root changes project metadata only; it does not create or delete folders,
branches, files, assignments, chats, tasks, or external-agent runs. Worktree
creation remains a separate explicit action.

Worktree creation is an explicit operator action. In V1, Hecate creates
worktrees under the selected root's `.worktrees/` directory and does not create
sibling workspaces outside the registered project root.

## Cairnline Coordination Boundary

Hecate's Projects API and cockpit use the embedded [Cairnline](https://github.com/hecatehq/cairnline) coordination store. Cairnline owns the portable project graph: project identity, roots, context-source and skill metadata, roles, work items, assignments, evidence, reviews, handoffs, accepted project memory, memory candidates, and Project Assistant proposal records.

Hecate owns the host-specific layer around that graph: Agent Presets, provider and model selection, task and External Agent launch, workspace and Git side effects, approvals, sandboxing, chats, execution references, context snapshots, traces, and the operator UI. Starting an assignment resolves Cairnline coordination intent into Hecate runtime behavior; Cairnline records never bypass Hecate policy.

The Hecate facade remains at `/hecate/v1/projects*`, so the Projects UI does not depend on Cairnline's transport. Hecate stores embedded Cairnline state under its local data directory. There is no Hecate-native coordination backend, backend selector, mirror, migration endpoint, or rollback gate. Alpha dogfood records created by the removed native store are intentionally not migrated.

Cairnline itself remains agent-neutral and can also be used directly over MCP by other agent hosts. A separately installed Cairnline connector is future work; Hecate currently uses the embedded Go package.

## What Projects V1 Does Not Do

- It does not replace issue trackers or team project-management systems.
- It does not infer every project from Git remotes automatically.
- It does not auto-promote memory, auto-dispatch handoffs, or launch work from
  setup without operator review.
- It does not treat local skill metadata as permission to run tools or load
  `SKILL.md` bodies into prompts.
- It does not make External Agent private memory part of Hecate project memory.

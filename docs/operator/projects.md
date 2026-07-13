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

## Open, Copy, And Return To Project Work

Hecate uses stable browser paths for its workspaces: `/chats`, `/projects`,
`/tasks`, `/connections`, `/observability`, `/usage`, and `/settings`. Switching
workspaces updates the address and removes project-only details from any
non-Projects path.

Projects addresses become more specific as you move into the cockpit:

| Destination      | Address                                                        |
| ---------------- | -------------------------------------------------------------- |
| Projects         | `/projects`                                                    |
| Project Overview | `/projects?project=<project_id>`                               |
| Work queue       | `/projects?project=<project_id>&view=work`                     |
| Exact work item  | `/projects?project=<project_id>&view=work&work=<work_item_id>` |
| Timeline         | `/projects?project=<project_id>&view=timeline`                 |
| Memory           | `/projects?project=<project_id>&view=memory`                   |
| Skills           | `/projects?project=<project_id>&view=skills`                   |

Overview is the default, so it does not add `view=overview`. If Work has no
item in the address, Hecate selects a valid current or first item and replaces
the address with its exact identifier. The project named in the address takes
priority over the project or workspace you last opened.

In a browser, open the project, view, and work item you want, then copy the
address from the browser. Reload restores that destination. Back and Forward
return through workspace, project, tab, and work-item choices without creating
duplicate project records. A shared link works only for someone connected to
the same Hecate runtime and Cairnline project store; it is not a portable export
and does not grant permission to launch or change work.

Hecate keeps an exact link in place while it checks the project catalog. If the
project does not exist in this runtime, the cockpit shows **Project not found**
and does not open another project. If the project exists but the work item does
not, Work remains open with a notice and the valid queue; choose another item
explicitly. New projects that still need onboarding always open the guided
Overview, even if an old link names Work, Memory, Timeline, or Skills.

The Tauri desktop app uses the same paths inside its webview, but V1 does not
show an address bar, provide a native **Copy link** action, or register an
operating-system deep link. Copy/share entry is therefore a browser workflow;
use Hecate's browser address on the target runtime when a restorable link is
needed.

## Work Flow

For each work item:

1. Draft the first assignment from the owner role, or add one directly.
2. Choose who does the work: **Human**, **Hecate Task**, or **External Agent**.
3. Start Human work directly, or inspect launch readiness and context before
   starting an execution-backed assignment.
4. Use the selected work item's **Next action** to record evidence, resolve a
   review, decide a handoff, or return to the assignment that needs attention.
5. Review closeout and mark the work done only after the readiness checks are
   clear.

Assignments keep their own execution records. Projects coordinate the work, but
Tasks, Chats, and External Agents remain the execution surfaces.
Starting a Hecate Task assignment requires the task runtime. Preparing an
External Agent assignment uses the agent-chat adapter runtime and does not
require the Hecate task runtime to be configured.
Human assignments create neither runtime. **Start work** claims the portable
assignment for the Hecate operator and records its Cairnline start time;
**Mark complete** records Cairnline completion. **Resume work** returns work
that is waiting for review to active progress. Advanced assignment editing can
record review, failure, or cancellation explicitly, but responsibility,
destination, and workspace stay fixed once Human work starts. A workspace is
optional, so the same flow works for rootless research, planning, design, and
administrative projects. Failed and cancelled assignments currently block
closeout; Cairnline does not yet define a retry or supersession transition, so
Projects does not fabricate one locally.
Human-only projects do not report a missing workspace as a Health setup gap;
workspace guidance remains relevant only when an agent-backed assignment needs
launch prerequisites.

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
summary before drilling into the full Work queue. Review follow-up and closeout
operations open selected-work detail and focus the named assignment, review, or
handoff when the server provides that target in the typed action. If that exact
record disappeared before detail loaded, Hecate focuses the selected work item,
announces the stale record, and offers **Refresh work** instead of falling back
to a different assignment or artifact. The recovery remains in place while a
refresh is running or fails, and only clears when the exact record appears or a
loaded operations brief no longer requests it. The selected work item's **Next
action** keeps the exact operation chosen from Overview; when that operation is
resolved and disappears from a loaded brief, the rail advances to the next
server-ordered operation for the item. Direct work selection starts with that
same server order. The client does not derive another priority from blocker
text or section order.
Routine queue creation and Project Assistant drafting remain secondary while
that guided action is available.

Closeout readiness is the server contract shared by Project Operations and
selected-work detail. Its structured assignment, review, and open-handoff
references let the UI open the right follow-through without guessing from
display copy. The operator still records evidence, applies a follow-up
proposal, decides a handoff, or closes work explicitly from the existing
surface. If no project operation currently promotes that work item, its
closeout checks remain visible without inventing another primary action.

Assignment launch readiness is also server-backed. Before `Start assignment`,
or `Prepare chat` for Hecate Task and External Agent work, the detail view loads
`GET /hecate/v1/projects/{id}/work-items/{work_item_id}/assignments/{assignment_id}/launch-readiness`
and uses its typed `ready`/`blockers` fields as the launch gate. The separate
preflight context packet remains inspectable evidence for the operator; it is
not parsed by the client as the readiness authority.
Human work does not use launch readiness or preflight because it has no runtime
to configure or dispatch.

Each assignment is presented as an execution story. The leading action follows
the current assignment state: **Start work** and **Mark complete** for Human
work, **Record review** while Human work waits for review, **Review & start** or
**Review & prepare chat** while an agent assignment is queued, **Open
task/chat** while running, **Review in task** for pending approval, **Inspect
task/chat** after failure or cancellation, and a review action after completion
when the work item has a suitable reviewer role. **Resume work** remains a
secondary action while Human work waits for review. The
Assigned, Started, current, and Finished milestones use timestamps actually
recorded by Cairnline or the linked Hecate runtime. If no start or finish time
exists, Projects says so instead of using the assignment's general update time
as execution history.

If a Human start was interrupted after Cairnline saved the operator claim but
before work began, the story says **Starting** and offers **Finish starting**.
Only a pristine same-operator claim can use this recovery; a prepared or
competing claim remains blocked without offering progress actions. Editing is
hidden after Human work reaches a terminal outcome. Marking Human work failed
or cancelled requires a second, explicit confirmation; choosing a progress
change resets unsaved destination edits so the two changes remain reviewable.
Cancelling work that never started keeps the start time empty.

Hecate currently receives Cairnline's review-waiting and approval-waiting states
through the same assignment status. Projects says **Review task** and uses
neutral review copy unless the linked runtime reports at least one pending
approval. A task/chat action means a real execution link exists; **Start related
chat** is a separate supporting action that creates a new draft.

Pending approvals, errors, missing runtime links, and missing prepared External
Agent chats stay visible. Open **Execution details** for task/run/chat IDs,
provider/model, root, readiness, Context Inspector, and canonical evidence.
The selected assignment projection is authoritative for those facts and
actions; the separately refreshed activity inbox does not fill in or override
them. These are supporting facts; Tasks, Chats, and External Agents remain the
execution authorities.

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
evidence, or handoffs without scanning each section header for separate
actions. A next action for missing evidence opens the same evidence form with
the relevant assignment already selected. The routine path asks for the title,
link, external reference, assignment, and summary; source classification,
provider, and trust details remain under **Advanced source details**.
Assignment choices use project role names and plain status labels. While an
evidence, review, or handoff record is being saved, its dialog cannot be
dismissed or submitted again. After reconciliation, keyboard focus moves to
the saved artifact or surviving handoff instead of falling out of the cockpit.

Recording a review now requires the operator to choose a verdict and write the
outcome for the named source assignment before saving. The form separately
identifies the **Review assignment** that is authoring the result. A result that needs follow-up
returns to the normal reviewable Project Assistant proposal flow; it does not
create or start work automatically.

Creating a handoff starts with its title, summary, recommended next action,
source assignment, and target role. Existing target links, execution references,
linked evidence or memory, context references, and provenance stay available
under **Advanced links and provenance**. A pending handoff must still be
accepted, dismissed, superseded, or linked to follow-up work by the operator.
Opening a linked assignment moves focus to its assignment story; the handoff
does not provide a second launch control. Every handoff create, edit, status
decision, or deletion reloads closeout readiness so the next action reflects
the authoritative handoff state immediately. Routine artifact and handoff rows
use product labels such as **Evidence**, **Document**, and **Operator reviewed**;
assignment choices use role display names, while storage vocabulary remains in
advanced forms and API documentation.

When readiness reports that closeout is ready, **Review closeout** opens a
confirmation summarizing completed assignments, unresolved review follow-ups,
and open handoffs. **Mark work done** records the explicit closeout decision; it
does not delete assignments, linked Tasks or Chats, reviews, evidence, or
handoffs. A server conflict leaves the work open, shows the failure in the
confirmation, and automatically reloads its readiness checks. The manual
**Refresh** action remains available. Once closeout is recorded, the work item
becomes a read-only record: operators can still inspect and refresh its history,
but edit, add, launch, handoff, review, and deletion controls are no longer
offered. Persisted `done` or `cancelled` status keeps that read-only posture even
when a later readiness refresh is temporarily unavailable.

## Supporting Project Surfaces

Use **Memory** to review suggested guidance before managing the rest of the
project's saved context. A pending suggestion is the surface's primary action.
Saved memory remains visible as a compact title and body summary, while its
classification, provenance, and edit controls open under details. Resolved
suggestions and **Sources** stay collapsed until needed. Adding or editing a
source still changes metadata only; it does not fetch or inject the source.
**Find from folders** is available only when the project has an active folder
with a nonblank path.

Use **Skills** as a status-first registry. Each row keeps its enabled state,
availability, and warnings visible; editing, source paths, trust, tools, and
suggested access stay under **Settings and source**. Rootless projects can
inspect, edit, enable, and disable existing registered skills normally. **Find
skills** remains unavailable until an active folder exists because a folderless
discovery pass cannot safely describe what is present on disk.

Project Settings describes launch behavior without changing its stored value:

- **Isolated copy (recommended)** preserves an unset workspace setting and
  keeps launches away from direct writes to the attached folder.
- **Isolated copy (ephemeral setting)** and **Isolated copy (persistent
  setting)** preserve those explicit settings separately.
- **Attached folder (writes directly)** is the only choice that launches in
  place.

Unknown existing settings remain selectable so saving another default does not
silently change workspace behavior. At narrow widths, Project Settings replaces
the workspace body with a full-width inspector. Focus moves to the Settings
heading; **Back** or a successful save returns to the exact project control that
opened it, including after the project refreshes. Settings and its navigation
stay locked while a save is in progress so later edits are not lost. Timeline,
Memory, Skills, sources, roots, roles, and Agent Presets remain supporting
surfaces. They do not create a second project state outside Cairnline or launch
work without operator confirmation.

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

**Default folder** and **Active** are separate Cairnline settings. Making the
default folder inactive does not select a different default; choose another
default explicitly when that is the intended launch fallback. A folder added
and selected in the same Settings edit becomes the default only after Cairnline
returns its portable root ID.

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

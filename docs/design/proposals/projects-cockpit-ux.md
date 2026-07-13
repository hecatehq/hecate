# Projects Cockpit UX

> **Status:** Proposal with the overview-first, work-item execution,
> assignment-destination, follow-through, and supporting-surface hierarchy
> slices implemented. Shareable navigation remains.
>
> **Current source of truth:** [Projects](../../operator/projects.md),
> [Projects design](../accepted/projects.md), and the Hecate
> `/hecate/v1/projects*` facade.

## UX Audit

The Projects implementation has strong coordination contracts but presents too
many of them at once. `ProjectsView` correctly owns data loading and mutations,
while `ProjectWorkspaceView` composes setup, Project Assistant, operations,
activity, the work queue, and selected-work detail. Before this redesign, ready
projects opened directly on that whole Work surface.

Concrete issues from the current UI and browser behavior:

- There was no project Overview. Project Assistant, Project Operations, Resume,
  queue filters, the work list, and selected detail all appeared in the default
  path and competed to be the next action.
- Resume derived another priority from activity even though the ordered
  operations brief is the server authority for the best operator action.
- Long workspace-tab labels required four 148px tracks and horizontal overflow.
- At a 390px viewport, the fixed 220px project index left roughly 120px for the
  workspace, reducing routine copy to one-word lines.
- The committed Projects screenshot predates Project Operations and no longer
  represents the current information hierarchy.
- Browser tests cover setup, rootless work, assignment launch, evidence, and
  closeout, but not a default overview or narrow navigation.
- Assignment forms exposed implementation-shaped role/driver language, and the
  Hecate facade did not preserve Cairnline's `manual` destination for human
  work.
- Completed assignments could make a work item look done before the operator
  resolved evidence, review follow-up, or an open handoff.
- Review, handoff, evidence, and closeout controls were spread across several
  sections, so a selected work item could present several equally prominent
  actions.
- Overview could open the right work item but discard the exact assignment,
  review, or handoff target. That forced another scan, especially at 390px.
- Review and handoff forms exposed advanced record details in the routine path,
  and closeout could be committed without a final confirmation.
- Memory mixed pending review, saved guidance, source management, and raw
  provenance into one dense surface with several competing header actions.
- Skills rendered every registered skill as an always-open editor, so status
  and warnings were harder to scan than paths, trust labels, and runtime needs.
- Project Settings exposed stored workspace-mode values instead of product
  language and squeezed the project workspace beside a fixed-width inspector at
  390px.
- Rootless projects offered file discovery even though skills discovery without
  an active folder can mark previously registered skills missing.

Existing strengths stay intact: rootless creation is first-class, setup and
assistant changes remain reviewable, launch preflight is explicit, evidence is
generic provenance, and review, handoff, memory promotion, and closeout remain
operator-controlled.

## Design Thesis

- **Visual thesis:** calm technical hierarchy with the server's next action as
  the only accent.
- **Content plan:** workspace navigation, next action, activity continuity, then
  the full work surface.
- **Interaction thesis:** overview actions route to the exact existing surface;
  activity controls navigate rather than invent priority; the project index
  stacks above the workspace at narrow widths.

## Target Information Architecture

```mermaid
flowchart LR
  P["Projects index"] --> G{"Project state"}
  G -->|"New or incomplete"| O["Guided setup"]
  G -->|"Ready"| V["Overview · default"]
  V --> N["Single best next action"]
  V --> S["Activity snapshot"]
  N --> W["Work"]
  S --> W
  W --> D["Selected work item"]
  D --> F["Single follow-through action"]
  F --> X["Assignment execution stories"]
  F --> R["Evidence · review · handoff · closeout"]
  R --> C["Explicit closeout confirmation"]
  C --> Q["Read-only completed record"]
  V --> T["Timeline"]
  V --> M["Memory"]
  V --> K["Skills"]
  V --> I["Settings inspectors"]
```

Overview uses setup readiness, the first ordered operation, and activity counts
already loaded through Hecate. Work continues to own the queue, selected work
item, Project Assistant, assignments, evidence, handoffs, review, and closeout.
Within selected work, the first server-ordered operation for that item becomes
its one follow-through action and routes to the exact record. Timeline, Memory,
Skills, Roles, Agent Presets, roots, sources, and runtime detail stay supporting
surfaces.

## Operator Journey

```mermaid
flowchart TD
  A["Add project"] --> B["Name + purpose"]
  B --> C{"Needs local files?"}
  C -->|"No"| D["Rootless project"]
  C -->|"Yes"| E["Attach workspace"]
  D --> F["Guided setup"]
  E --> F
  F --> G["Overview"]
  G --> H["Take the recommended action"]
  H --> I["Create or open work"]
  I --> J["Choose who does the work"]
  J --> K{"Destination"}
  K -->|"Human"| KH["Start work directly"]
  K -->|"Hecate Task or External Agent"| KA["Review launch context and approve start"]
  KH --> L["Track progress, approvals, failures, and evidence"]
  KA --> L
  L --> M["Follow the selected work item's next action"]
  M --> N{"What is needed?"}
  N -->|"Evidence"| NE["Record evidence for the named assignment"]
  N -->|"Review"| NR["Record verdict or plan follow-up"]
  N -->|"Handoff"| NH["Accept, dismiss, or link follow-up work"]
  N -->|"Ready"| O["Review and confirm closeout"]
  NE --> M
  NR --> M
  NH --> M
  O --> P["Inspect the completed record"]
  P --> G
```

## Reviewable Slices

1. **Overview-first shell:** default to Overview, show the server's first
   operation as the primary action, keep activity as navigation, move the full
   queue/detail to Work, shorten tabs, repair narrow layout, and add focused
   journey coverage.
2. **Work-item execution story:** reshape dense assignment rows into a readable
   execution timeline with technical evidence behind disclosure.
3. **Assignment destinations:** expose plain Human, Hecate Task, and External
   Agent choices after the Hecate facade faithfully maps Cairnline's portable
   `manual` execution mode.
4. **Review, handoff, and closeout rail:** make follow-through legible without
   auto-dispatching or auto-closing work.
5. **Supporting inspectors and navigation:** progressively disclose memory,
   skills, context, and runtime detail; make Settings responsive and preserve
   exact launch semantics; then add shareable project/work navigation and
   broader accessibility coverage.

Slices 1 through 4 and the supporting-surface half of slice 5 are implemented.
Slice 1 rearranges existing server
projections and action routing. Slice 2 reshapes each assignment into a
state-driven story. Slice 3 adds Human as a faithful facade label for
Cairnline's `manual` execution mode, with direct Start work, Resume work, and
Mark complete actions backed by Cairnline claim/status/completion transitions.
Slice 4 adds one server-directed follow-through action to selected work, exact
assignment/review/handoff focus, progressive evidence and handoff forms, an
explicit closeout confirmation, and a read-only completed state. It also keeps
work-item closure explicit: completed assignments no longer make an open work
item appear closed. The implemented part of slice 5 puts pending memory review
before saved guidance, collapses resolved history and technical metadata,
turns Skills into a status-first registry, gives Timeline semantic structure,
and makes Project Settings a full-width narrow inspector. Workspace behavior
labels preserve the exact stored mode: an unset value remains the recommended
isolated copy, `ephemeral` and `persistent` remain distinct isolated settings,
and only `in_place` is described as writing directly to the attached folder.
File-backed discovery is unavailable without an active nonblank root, while
rootless projects retain normal memory and skill management.
None of the slices add local project lifecycle state or inferred execution
events.

## Work-Item Follow-Through Rail

```mermaid
flowchart TD
  A["Select work item"] --> B["Use its first server-ordered operation"]
  B --> C{"Typed target"}
  C -->|"Assignment"| D["Focus assignment and its current action"]
  C -->|"Missing evidence"| E["Open evidence form for that assignment"]
  C -->|"Review artifact"| F["Draft a reviewable follow-up"]
  C -->|"Open handoff"| G["Focus handoff for an operator decision"]
  C -->|"Ready for closeout"| H["Open closeout confirmation"]
  D --> K["Refresh authoritative operations"]
  E --> K
  F --> K
  G --> K
  K --> B
  H --> I["Operator marks work done"]
  I --> J["Read-only completed record"]
```

The rail never parses blocker copy or invents urgency. It uses the operation's
typed assignment, review artifact, or handoff identifier and checks that target
against the structured closeout readiness response. The same identifiers carry
Overview navigation into selected work, where focus lands on the intended
record. If no server operation promotes the selected item, its closeout checks
remain available without creating another primary action.
At narrow widths the rail stacks its action below the explanation, routine
forms collapse to one column, and focused records scroll into view with a
visible keyboard-focus target.

## Assignment Execution Story

```mermaid
flowchart LR
  Q["Assigned"] --> S["Started · when recorded"]
  S --> C["Current server/runtime state"]
  C --> F["Finished · when recorded"]
  C --> A{"Best operator action"}
  A -->|"Queued"| L["Review launch"]
  A -->|"Queued Human work"| H["Start work"]
  A -->|"Running Human work"| HC["Mark complete"]
  A -->|"Interrupted Human start"| HI["Finish starting"]
  A -->|"Human work waiting for review"| HR["Record review · Resume is secondary"]
  A -->|"Running"| T["Open task or chat"]
  A -->|"Pending approval evidence"| P["Review in task"]
  A -->|"Review state only"| U["Review task"]
  A -->|"Failed"| I["Inspect execution"]
  A -->|"Completed"| R["Request or record review"]
  C --> D["Execution details · disclosed"]
  D --> E["IDs · provider/model · root · context · evidence"]
```

The execution rail uses only recorded `created_at`, `started_at`, and
`completed_at`/runtime `finished_at` timestamps. It presents the current status
as a snapshot when no transition time exists and never treats `updated_at` as
execution history. Pending approvals, failures, missing runtime links, and an
unprepared External Agent chat remain visible outside the disclosure. Blocked
closeout guidance follows the assignments it describes; ready and completed
closeout stays promoted near the work brief. Because the current Hecate facade
also maps Cairnline's `awaiting_review` to `awaiting_approval`, the cockpit uses
neutral review language unless a linked runtime reports a pending approval.

## Verified Screen States

The implemented slices were exercised in the running Hecate UI with deterministic
fixtures for the Cairnline-backed Hecate facade at desktop and 390px widths.
Empty, guided-setup, setup-unavailable, loading, active, blocked,
approval-review, interrupted-start, completed, failed, cancelled, evidence, and
closeout states are covered by focused component and journey tests. Failure and
cancellation use an explicit second confirmation, including for keyboard
submission. A prepared Human claim is shown as blocked rather than as a
recoverable interrupted start, and queued progress changes are saved separately
from destination edits.

The deterministic follow-through journey verifies exact assignment, handoff,
review-artifact, and closeout focus against same-work decoys; narrow-width
containment before test-driven scrolling; explicit closeout; and the durable
read-only completed state. Focus requests for records that disappeared fail
closed to the selected work item with an announced refresh action. Unexpected
fixture routes fail the journey instead of receiving generic success data.

The supporting-surface journey verifies pending and saved memory hierarchy,
collapsed source and skill details, memory creation, skill editing through the
existing mutations, a clean Settings form, focus restoration, and horizontal
containment at desktop and 390px widths. Focused component tests also cover
loading, empty, error, pending-review, resolved-history, editing, and
save-pending states.

![Read-first project memory at desktop width](../../screenshots/projects-memory.jpg)

![Project Settings at narrow width](../../screenshots/projects-settings-narrow.jpg)

Regenerate these images with
`HECATE_CAPTURE_PROJECTS_SUPPORTING=1 bunx playwright test e2e/projects.spec.ts -g "Projects supporting surfaces stay read-first"`
from `ui/`.

![Work-item closeout confirmation at desktop width](../../screenshots/projects-follow-through.jpg)

![Focused handoff at narrow width](../../screenshots/projects-follow-through-narrow.jpg)

Regenerate these images with
`HECATE_CAPTURE_PROJECTS_FOLLOW_THROUGH=1 bunx playwright test e2e/projects.spec.ts -g "Projects follow-through journey"`
from `ui/`.

Regenerate the two Overview images from the deterministic browser journey with
`HECATE_CAPTURE_PROJECTS_OVERVIEW=1 bunx playwright test e2e/projects.spec.ts -g "default ready-project home"` from `ui/`.
This targeted journey is the canonical generator for these two JPGs; the general
documentation capture script continues to own `projects.png`.

![Projects overview at desktop width](../../screenshots/projects-overview.jpg)

![Projects overview at narrow width](../../screenshots/projects-overview-narrow.jpg)

Regenerate the assignment execution images from the deterministic full Projects
journey with
`HECATE_CAPTURE_PROJECTS_EXECUTION=1 bunx playwright test e2e/projects.spec.ts -g "Projects journey"`
from `ui/`.

![Assignment execution at desktop width](../../screenshots/projects-work-execution.jpg)

![Assignment execution at narrow width](../../screenshots/projects-work-execution-narrow.jpg)

Regenerate the rootless Human assignment images from the deterministic browser
journey with
`HECATE_CAPTURE_PROJECTS_HUMAN=1 bunx playwright test e2e/projects.spec.ts -g "Projects Human assignment journey"`
from `ui/`.

![Human assignment at desktop width](../../screenshots/projects-human-assignment.jpg)

![Human assignment at narrow width](../../screenshots/projects-human-assignment-narrow.jpg)

## Contract Stop Lines

- Cairnline remains the sole portable coordination authority. The UI uses only
  Hecate's facade and never reconstructs portable state.
- Operations route through the server-provided `action.type`; `kind`, target
  metadata, and client activity are not alternate priority authorities.
- Selected-work follow-through preserves the exact server operation the
  operator chose, including when several operations target the same work item;
  after that operation disappears from a loaded authoritative brief, the rail
  advances in server order. Direct work selection also starts in server order.
  It may validate and focus a typed assignment, review artifact, or handoff
  target, but it must not rank local blockers or parse display copy into
  actions. A missing exact target remains guarded across pending or failed
  refreshes until the record appears or the loaded brief removes the target.
- Closeout readiness exposes `missing_evidence_assignment_ids`,
  `review_follow_up_artifact_ids`, and `open_handoff_ids` for exact routing.
  Cairnline remains the authority for those records and their readiness; Hecate
  does not reconstruct them from rendered blocker text.
- A completed assignment is not a completed work item. Only the explicit
  operator closeout transition produces the read-only `done` state.
- Health remains a secondary inspector because a root can be optional for
  coordination even when health reports that launches need one.
- Human is a product label for Cairnline's `manual` execution mode, not a
  second identity or assignment store. V1 does not add named assignees or due
  dates. A root remains optional, and direct progress actions use the
  Cairnline-authoritative assignment lifecycle.
- Cairnline `awaiting_review` is not yet distinct in Hecate's assignment view.
  The execution story therefore says review, not approval, unless Hecate has a
  positive pending-approval count. Review artifacts, handoffs, and closeout
  follow-up remain the honest review surfaces.
- Failed and cancelled assignments are terminal closeout blockers in Cairnline,
  which does not yet expose retry or supersession. This slice keeps that outcome
  visible and does not offer a misleading local recovery action.
- An execution timeline may show current Cairnline milestones and Hecate runtime
  events, but must not invent a portable transition history Cairnline does not
  store.
- Task reconciliation advances only from a Task and latest Run whose project,
  work-item, and assignment links match the portable row. Old resumed runs and
  cross-assignment links are evidence, not lifecycle authority.

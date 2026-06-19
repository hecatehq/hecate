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
3. Set provider, model, profile, memory, and source defaults when the project
   should launch Hecate Chat, Hecate Tasks, or External Agents.
4. Run **Set up project** to discover portable workspace guidance and local
   skill metadata, then review the proposed memory candidates and role changes
   before applying them.
5. Create the first work item once the setup checklist looks right.

Setup is reviewable. Hecate may propose roles or memory candidates, but the
operator applies or dismisses them. Setup does not launch agents, write project
memory automatically, install skills, or inject skill bodies into prompts.

## Work Flow

For each work item:

1. Draft the first assignment from the owner role, or create one manually.
2. Inspect the launch context before starting work when defaults, roots, or
   provider/model posture are uncertain.
3. Start a Hecate Task or External Agent assignment.
4. Record evidence, reviews, and handoffs as the work moves between roles.
5. Close the work item only after assignments and review follow-up are clear.

Assignments keep their own execution records. Projects coordinate the work, but
Tasks, Chats, and External Agents remain the execution surfaces.

The Work Coordination tab starts with a deterministic next action and compact
resume summary. Use the next action for the single most useful operator step,
then jump to blocked, active, recent, or memory-review work from the resume
summary before drilling into the full work queue. Review artifacts that require
follow-up are surfaced as closeout blockers with direct follow-up creation
actions.

For a new work item with no assignments or artifacts yet, the detail view starts
with a guided prepare action. Hecate can draft the first assignment from the
work item role and context, but the operator still reviews and applies the
proposal before execution records are created.

After a work item has activity, use its Add strip to attach more assignments,
evidence, or handoffs without scanning each section header for separate actions.

## Roots And Worktrees

Project roots are concrete workspace paths, not project identity. A single
project can include a main checkout and linked Git worktrees. Work items and
assignments may select a root; launch resolution uses assignment root, then
work-item root, then project default root, then the first active root.

Worktree creation is an explicit operator action. In V1, Hecate creates
worktrees under the selected root's `.worktrees/` directory and does not create
sibling workspaces outside the registered project root.

## What Projects V1 Does Not Do

- It does not replace issue trackers or team project-management systems.
- It does not infer every project from Git remotes automatically.
- It does not auto-promote memory, auto-dispatch handoffs, or launch work from
  setup without operator review.
- It does not treat local skill metadata as permission to run tools or load
  `SKILL.md` bodies into prompts.
- It does not make External Agent private memory part of Hecate project memory.

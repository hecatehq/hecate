# Planning

How to write a plan when [`../core/workflow.md`](../core/workflow.md) says "stop and propose a plan first."

## When a plan is required

- Cross-package wire-field changes (the seven-step chain — see [`../skills/providers/SKILL.md`](../skills/providers/SKILL.md)).
- New persisted things — must mirror memory + SQLite + Postgres tiers.
- New HTTP endpoints (provider-compatible `/v1/...` or Hecate-native `/hecate/v1/...`).
- New approval policies or new sandbox capabilities.
- New persistent UI surfaces (inspector, side rail, dashboard block, summary panel).
- Substantive refactors that cross ring boundaries or touch the api↔providers seam.

## Plan structure

1. **Problem framing** — one paragraph. What is broken or missing, and what would "fixed" look like to an operator.
2. **Constraints and assumptions** — bullets. Surface what the plan takes as given (existing code, conventions, performance, compatibility, operator model).
3. **Options considered** — for non-trivial design choices, one to three options with concrete pros and cons. Tables work well here.
4. **Recommendation** — one option called out. State the trade-off being accepted.
5. **Acceptance criteria** — specific, verifiable. "The race suite passes; the new event appears in `docs/runtime/events.md`; the UI snapshot reflects the new prop" beats "tested and documented".
6. **Risks and mitigations** — what could go wrong; what catches it.
7. **Migration and rollback** — when relevant. Env knobs, schema changes, wire-shape compat, UI affordance toggles, rollback path.

## Anti-patterns

- Plans that recap the obvious (file paths, the existence of tests, the names of well-known packages).
- Plans that list every file ahead of time without explaining the _why_ — that is a task list, not a plan.
- Plans that name implementation phases as "Phase 1 / Phase 2 / Milestone X" — those labels are not allowed in commits or comments either, and they tend to leak. Sequence the work in prose if sequencing matters.
- Plans that defer all decisions ("we'll figure out X during implementation"). Decide what can be decided.
- Plans that don't name what's being given up.

## Output format

Short prose, scannable. Tables for option comparisons. Code blocks only for concrete signatures, schemas, or wire shapes. The plan is the brief that an implementation skill or another agent picks up — give it enough to proceed without further context.

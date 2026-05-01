# Workflow

The default operating loop, when to stop and plan, and how to propose commits.

## Default loop

1. **Understand the task.** Restate it in your own words if non-trivial. Resolve ambiguity before coding.
2. **Gather context.** Read the files involved; run small probes (`grep`, `ls`, opening a neighboring test). Don't jump straight into edits.
3. **Plan before non-trivial changes.** See "When to stop and propose a plan first" below. Substantial backend changes (new wire fields, cross-package ripple, new endpoints) and substantial UI changes (new persistent surfaces, new interaction patterns) require a written plan first. Format: [`../tasks/planning.md`](../tasks/planning.md).
4. **Implement in coherent steps.** Minimal, scoped changes. Avoid drive-by edits that bloat the diff.
5. **Update docs and diagrams.** For every `.md` file touched (or whose subject matter changed), check whether any Mermaid diagrams in that file — and in directly related docs (e.g. `architecture.md` when changing the task runtime) — still accurately reflect the change. Update stale diagrams in the same commit as the code change, not as a follow-up.
6. **Verify.** Run the relevant ladder from [`verification.md`](verification.md). State exactly what was run.
7. **Summarize.** What changed, what risks remain, what the operator should know — including manual smoke steps if any.

## When to ask clarifying questions

- Scope is ambiguous.
- Acceptance criteria are unstated or vague.
- A product decision is implicit (UX choice, API shape change, default behavior).
- The change is destructive (deletion, schema migration, data backfill).
- Multiple plausible interpretations exist and they would lead to materially different implementations.

Ask once, concisely, and proceed. Do not stall on questions that can be answered by reading the code.

## When to avoid broad refactors

- The task is narrow and a refactor would inflate the diff.
- Behavior changes are mixed in — refactor and behavior change must not share a commit.
- The area being changed has no test coverage to pin behavior across the refactor.

If a refactor is the right move, split it into its own change first and rebuild the feature on top.

## When to stop and propose a plan first

- Adding a new wire field. The seven-step chain spans `pkg/types/` → `internal/api/` → `internal/providers/` and tests at every layer. See [`../skills/providers/SKILL.md`](../skills/providers/SKILL.md).
- Adding a new persisted thing — must mirror memory + sqlite + postgres tiers and the relevant retention subsystem.
- Adding a new persistent UI surface (inspector, side rail, dashboard block, summary panel). Requires explicit user approval first.
- Adding a new approval policy or sandbox capability.
- Anything that changes startup, config-loading semantics, or the public HTTP contract.
- Cross-package refactors that touch the api↔providers boundary or the storage tier interface.

## When to split work

- One logical change accumulates more than a handful of files for unrelated reasons.
- The verification scope balloons (e.g. needs both UI typecheck/test and backend race suite for behavior the operator will see in stages).
- A behavior change is sneaking into a refactor.

## Commit etiquette

**Don't auto-commit.** After every change, propose a Conventional Commits message in a fenced code block; the operator merges. This is a default behavior, not an opt-in.

**Format**: `type(scope): subject`, optional body separated by a blank line. Subject under ~72 chars; body wrapped at ~72.

**Types**: `feat`, `fix`, `test`, `docs`, `chore`, `refactor`.

**Use `chore(agent):`** for agent-doc-only updates (anything under `ai/`, `AGENTS.md`, `CLAUDE.md`, `.cursor/rules/`, `.claude/commands/`). UI agent docs (`ui/AGENTS.md`, `ui/SKILL.md`) also go through `chore(...)`, not `docs(...)`.

**Pure-markdown changes** append `[skip ci]` to the subject. The CI workflow's `paths-ignore` already catches `**/*.md`; the marker is belt-and-suspenders.

**No plan, phase, or release labels in commit messages or code comments.** No `P0`, no `Phase 2`, no `#15`, no `Milestone N`. The plan lives in chat; the repo record is permanent. This applies equally to commit subjects, commit bodies, and code comments.

**Subject focuses on *what changed*.** Body explains *why*, the rejected alternatives, migration/compat notes, and follow-ups when relevant.

**Trivial changes** (typo fixes, dependency bumps) may be subject-only. Substantial changes carry a body — the operator reading the diff in 6 months needs the context.

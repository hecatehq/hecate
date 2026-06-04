# Hecate agent instructions

Canonical, provider-neutral agent instruction layer for the Hecate repo.
Agent entry points (`AGENTS.md` and the `CLAUDE.md` compatibility import) point
here — the substance lives in this directory.

## Layout

```
docs-ai/
  README.md                       this file — load order and quick rules
  core/
    project-context.md            what Hecate is, repo layout, rings, storage tiers, toolchain pins, risky areas
    engineering-standards.md      project-wide coding/style standards (backend + UI)
    workflow.md                   default operating loop, planning triggers, commit etiquette
    verification.md               build/test ladders, done criteria, manual smoke expectations
    agent-guidance.md             source-of-truth policy for AGENTS, Claude, Cursor, and other adapters
  tasks/
    planning.md                   how to write a plan when "stop and plan first" fires
    implementation.md             how to implement once a plan exists
    debugging.md                  how to debug deliberately
    maintenance.md                recurring upkeep: checks, branches, docs drift, dependencies
    refactoring.md                how to reshape code without changing behavior
    code-review.md                review rubric and output format
    release.md                    cut a release tag (pre-flight, snapshot, gate, recovery)
  skills/
    README.md                     canonical skill registry; same list for every agent
    backend/SKILL.md              Go backend skill (anything outside ui/ and tauri/)
    ui/SKILL.md                   React UI skill (ui/)
    tauri/SKILL.md                native desktop app skill (tauri/): Rust layer, sidecar
                                  lifecycle, platform bundling, gateway↔webview integration
    providers/SKILL.md            internal/providers/ skill (parallel-struct boundary, seven-step chain)
    architect/SKILL.md            posture skill: plan-first for substantial changes
    tester/SKILL.md               posture skill: test strategy and verification reporting
    devops/SKILL.md               posture skill: delivery surfaces and rollback paths
    maintenance/SKILL.md          posture skill: recurring upkeep, docs drift, branch/worktree hygiene
```

## What to load

Load the skill for your area first from the canonical registry,
[`skills/README.md`](skills/README.md), then `core/project-context.md` if you
need repo layout, ring rules, or risky-area guidance. `core/engineering-standards.md`
and `core/workflow.md` are reference — reach for them when you hit a style or
commit question.

| Task                                        | Load first                                        | Also load                                         |
| ------------------------------------------- | ------------------------------------------------- | ------------------------------------------------- |
| Backend — any Go outside `ui/` and `tauri/` | `skills/backend/SKILL.md`                         | `core/project-context.md`, `core/verification.md` |
| Provider adapter (`internal/providers/`)    | `skills/providers/SKILL.md`                       | `skills/backend/SKILL.md`                         |
| React UI (`ui/`)                            | `skills/ui/SKILL.md`                              | `core/project-context.md`, `core/verification.md` |
| Native desktop (`tauri/`)                   | `skills/tauri/SKILL.md`                           | `core/project-context.md`, `core/verification.md` |
| Substantial change — plan first             | `skills/architect/SKILL.md` + `tasks/planning.md` | `core/project-context.md`                         |
| Debugging                                   | `tasks/debugging.md`                              | skill for the relevant area                       |
| Maintenance / cleanup                       | `skills/maintenance/SKILL.md`                     | `tasks/maintenance.md`, `core/verification.md`    |
| Code review                                 | `tasks/code-review.md`                            | skill for the relevant area                       |
| Test strategy / coverage                    | `skills/tester/SKILL.md`                          | `core/verification.md`                            |
| Delivery / CI / env vars / schema           | `skills/devops/SKILL.md`                          | `core/verification.md`                            |
| Release / tag                               | `tasks/release.md`                                | `core/verification.md`                            |

## Adapter Policy

`docs-ai/` is the source of truth. Tracked adapter files are only discovery
shims. `CLAUDE.md` imports `AGENTS.md` and must not duplicate rules,
checklists, or skill content. The canonical skill set is
[`skills/README.md`](skills/README.md). Provider-specific directories such as
`.claude/` and `.cursor/` are intentionally not tracked.

Details and update rules: [`core/agent-guidance.md`](core/agent-guidance.md).

## Core rules (always in force)

- **Don't auto-commit.** Propose a Conventional Commits message; operator merges.
- **Docs in the same change.** New env var → `.env.example` + `docs/<feature>.md`. New event type → event-protocol taxonomy check + `docs/runtime/events.md`. Not as a follow-up.
- **Race suite is the floor** for backend/runtime changes — not a nice-to-have. Use `just test-race` or `go test -race -timeout 10m ./...`.
- **No plan labels** (`Phase 1`, `P0`, `#15`, `Milestone N`) in commit messages or code comments.
- **Probe before assuming paths.** `grep`, `ls`, `go build` before writing file paths from memory. Wrong paths compound.
- **Build early, build often.** `go build ./...` before the first edit (confirm the tree is clean) and after each logical step.
- **Beta-scope work gets a branch.** Start from current `master`, use a
  feature/refactor/docs branch, and land through PR. Direct `master` commits are
  for release mechanics or explicitly requested urgent corrections only.

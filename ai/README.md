# Hecate agent instructions

Canonical agent instruction layer for the Hecate repo. Tool-specific entry
points (`CLAUDE.md`, `AGENTS.md`, `.cursor/rules/`) are thin adapters that
point here — the substance lives in this directory.

## Layout

```
ai/
  README.md                       this file — load order and quick rules
  core/
    project-context.md            what Hecate is, repo layout, rings, storage tiers, toolchain pins, risky areas
    engineering-standards.md      project-wide coding/style standards (backend + UI)
    workflow.md                   default operating loop, planning triggers, commit etiquette
    verification.md               build/test ladders, done criteria, manual smoke expectations
  tasks/
    planning.md                   how to write a plan when "stop and plan first" fires
    implementation.md             how to implement once a plan exists
    debugging.md                  how to debug deliberately
    refactoring.md                how to reshape code without changing behavior
    code-review.md                review rubric and output format
    release.md                    cut a release tag (pre-flight, snapshot, gate, recovery)
  skills/
    backend/SKILL.md              Go backend skill (anything outside ui/ and tauri/)
    ui/SKILL.md                   React UI skill (ui/)
    tauri/SKILL.md                native desktop app skill (tauri/): Rust layer, sidecar
                                  lifecycle, platform bundling, gateway↔webview integration
    providers/SKILL.md            internal/providers/ skill (parallel-struct boundary, seven-step chain)
    architect/SKILL.md            posture skill: plan-first for substantial changes
    tester/SKILL.md               posture skill: test strategy and verification reporting
    devops/SKILL.md               posture skill: delivery surfaces and rollback paths
```

## What to load

Load the skill for your area first, then `core/project-context.md` if you
need repo layout, ring rules, or risky-area guidance. `core/engineering-standards.md`
and `core/workflow.md` are reference — reach for them when you hit a style or
commit question.

| Task | Load first | Also load |
|---|---|---|
| Backend — any Go outside `ui/` and `tauri/` | `skills/backend/SKILL.md` | `core/project-context.md`, `core/verification.md` |
| Provider adapter (`internal/providers/`) | `skills/providers/SKILL.md` | `skills/backend/SKILL.md` |
| React UI (`ui/`) | `skills/ui/SKILL.md` | `core/project-context.md`, `core/verification.md` |
| Native desktop (`tauri/`) | `skills/tauri/SKILL.md` | `core/project-context.md`, `core/verification.md` |
| Substantial change — plan first | `skills/architect/SKILL.md` + `tasks/planning.md` | `core/project-context.md` |
| Debugging | `tasks/debugging.md` | skill for the relevant area |
| Code review | `tasks/code-review.md` | skill for the relevant area |
| Test strategy / coverage | `skills/tester/SKILL.md` | `core/verification.md` |
| Delivery / CI / env vars / schema | `skills/devops/SKILL.md` | `core/verification.md` |
| Release / tag | `tasks/release.md` | `core/verification.md` |

## Core rules (always in force)

- **Don't auto-commit.** Propose a Conventional Commits message; operator merges.
- **Docs in the same change.** New env var → `.env.example` + `docs/<feature>.md`. New event type → `docs/events.md`. Not as a follow-up.
- **Race suite is the floor** for backend/runtime changes — not a nice-to-have. Use `/race` or `go test -race -timeout 10m ./...`.
- **No plan labels** (`Phase 1`, `P0`, `#15`, `Milestone N`) in commit messages or code comments.
- **Probe before assuming paths.** `grep`, `ls`, `go build` before writing file paths from memory. Wrong paths compound.
- **Build early, build often.** `go build ./...` before the first edit (confirm the tree is clean) and after each logical step.

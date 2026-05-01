# Hecate agent instructions

This directory is the canonical, vendor-neutral instruction layer for working on the Hecate repository. It is shared by Claude Code, Codex, Cursor, and any other agentic coding tool. Tool-specific entry points (`CLAUDE.md`, `AGENTS.md`, `.cursor/rules/`) are thin adapters that point here; the substance lives in this directory.

## Layout

```
ai/
  README.md                       this file
  core/
    project-context.md            what Hecate is, repo layout, rings, storage tiers, toolchain pins
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

## Where to start

- **First time in this repo**: read [`core/project-context.md`](core/project-context.md), then [`core/workflow.md`](core/workflow.md).
- **Backend work** (anything outside `ui/` and `tauri/`): also read [`skills/backend/SKILL.md`](skills/backend/SKILL.md).
- **UI work** (`ui/`): also read [`skills/ui/SKILL.md`](skills/ui/SKILL.md).
- **Native desktop app** (`tauri/`): also read [`skills/tauri/SKILL.md`](skills/tauri/SKILL.md).
- **Provider adapters** (`internal/providers/`): also read [`skills/providers/SKILL.md`](skills/providers/SKILL.md) — the canonical home for the seven-step "add a wire field" chain.
- **Planning a substantial change**: see [`skills/architect/SKILL.md`](skills/architect/SKILL.md) and [`tasks/planning.md`](tasks/planning.md).
- **Reviewing code** (yours or another agent's): see [`tasks/code-review.md`](tasks/code-review.md).

## Relationship to other agent surfaces

- `/AGENTS.md` and `/ui/AGENTS.md` and `/internal/providers/AGENTS.md` — the codebase map and Codex-discoverable entry points. They point here for conventions, workflow, and longer-form recipes.
- `/CLAUDE.md` — thin Claude Code adapter that points here.
- `/.cursor/rules/` — thin Cursor adapter that points here.
- `/.claude/commands/*.md` — slash commands (`/race`, `/typecheck`, `/test-affected`).

This directory is the canonical source. Those are adapters.

## Repo policy

Shared agent guidance is repository-owned and committed. There is no `.local` override layer and no personal customization tier. If a rule belongs in agent context, it lives here, in the open, under version control.

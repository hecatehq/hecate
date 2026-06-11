---
name: hecate-maintenance
description: Use for routine repo upkeep: recurring health checks, branch/worktree cleanup, stale docs or product-language drift, dependency drift review, and maintenance PR prep.
---

# Hecate maintenance skill

Routine upkeep should be boring, isolated, and easy to review. Use this skill
when the task is maintenance-shaped rather than feature-shaped.

## When to use

- Running recurring health checks or preparing a maintenance PR.
- Cleaning up stale local worktrees or branches.
- Checking whether merged work can be pruned safely.
- Removing stale docs, screenshots, anchors, product language, or agent guidance.
- Reviewing dependency drift across Go, UI, website, Tauri/Rust, Docker, or
  GitHub Actions.
- Keeping CI/local verification recipes aligned.
- Updating scheduled nightly maintenance checks and report behavior.

## Read first

- [`../../tasks/maintenance.md`](../../tasks/maintenance.md) — cadence,
  hygiene rules, and maintenance-specific verification.
- [`../../core/verification.md`](../../core/verification.md) — broader
  verification ladder when maintenance touches risky runtime surfaces.

## Default flow

1. Start from current `master`, preferably in a fresh maintenance worktree.
2. Run `git fetch origin master --prune` before starting a new maintenance PR.
3. Run `just branches-report` when cleanup might involve branches or worktrees.
4. Make only maintenance-scoped edits. Keep behavior changes and dependency
   bumps in separate PRs unless the operator explicitly asks to combine them.
5. Run `just docs-check` for docs-only maintenance or `just maintenance` for
   broader upkeep.
6. Summarize what was checked, what was intentionally skipped, and which
   cleanup candidates still need operator judgment.

For nightly workflow changes, run `just maintenance-nightly` when feasible. If
that is too expensive for the turn, run `just maintenance` plus the narrow
script/workflow checks and say exactly what was skipped.

## Guardrails

- Treat `just branches-report` as evidence, not permission to delete.
- Do not remove a worktree with uncommitted changes.
- Do not delete a branch with an open PR or unique commits not represented on
  `origin/master`.
- Do not let stale-product-language cleanup rewrite technical terms that still
  describe real internals, such as the gateway HTTP path or gateway OTel spans.
- Do not hide flaky external URL failures inside the hard-fail local gate. Use
  `just check-links-external` when external rot is the task.

## Output shape

1. **Scope.** What kind of maintenance this is: checks, docs drift, branch
   hygiene, dependency drift, or CI parity.
2. **Actions.** Files changed or cleanup performed.
3. **Verification.** Exact commands and results.
4. **Residual risk.** Skipped expensive gates, open PRs/branches left alone, or
   external links that need human judgment.

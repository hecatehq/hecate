# Hecate Skill Set

This is the canonical, provider-neutral skill set for Hecate. Every agent
should use this same list. Do not create Claude-specific, Cursor-specific,
Codex-specific, or editor-specific copies of these skills.

Each skill is a directory with one `SKILL.md`. Add new skills here, then update
this registry in the same change.

| Skill                                 | Use When                                                                                  |
| ------------------------------------- | ----------------------------------------------------------------------------------------- |
| [`architect`](architect/SKILL.md)     | Planning substantial changes before code, especially cross-package or architectural work. |
| [`backend`](backend/SKILL.md)         | Working on Go backend/runtime code outside `ui/` and `tauri/`.                            |
| [`devops`](devops/SKILL.md)           | Reviewing delivery, CI, env vars, migrations, rollback, and observability surfaces.       |
| [`maintenance`](maintenance/SKILL.md) | Recurring upkeep, docs drift, branch/worktree hygiene, and dependency checks.             |
| [`providers`](providers/SKILL.md)     | Working in `internal/providers/` or crossing the api↔providers wire boundary.             |
| [`tauri`](tauri/SKILL.md)             | Working on the native desktop app under `tauri/`.                                         |
| [`tester`](tester/SKILL.md)           | Choosing test layers, auditing coverage, or reporting verification evidence.              |
| [`ui`](ui/SKILL.md)                   | Working on the React operator UI under `ui/`.                                             |

## Agent Usage

1. Start at [`../README.md`](../README.md).
2. Pick the relevant skill from the table above.
3. Load only the supporting core/task docs that skill points to.

If an agent product supports native skills, configure it locally to read these
same `docs-ai/skills/*/SKILL.md` files. Do not commit tool-specific skill
adapters to the repo.

# Agent Guidance Surfaces

Hecate keeps one provider-neutral instruction layer for agents:
[`docs-ai/`](../README.md). Tracked entry points exist only to help agents find
that layer.

## Source Of Truth

`docs-ai/` owns the substance:

- `core/` — project context, engineering standards, workflow, verification,
  and this adapter policy.
- `tasks/` — reusable task playbooks such as planning, debugging, maintenance,
  refactoring, code review, and release.
- `skills/` — Hecate-owned area and posture skills, indexed in
  [`../skills/README.md`](../skills/README.md). These are not Claude-only,
  Cursor-only, or Codex-only; any agent or human can read and follow them.

If guidance applies to more than one tool, put it in `docs-ai/`. Do not add a
Claude-only, Cursor-only, or Codex-only copy of the same rule.

## Adapter Files

Tracked adapter files may contain only:

- A direct compatibility import such as `@AGENTS.md`.
- A short pointer to `docs-ai/README.md`.
- A codebase map for a scoped area, such as `ui/AGENTS.md`.
- Compatibility notes needed for an agent product to find `docs-ai/`.

Adapter files must not carry their own copies of planning triggers,
verification ladders, review rubrics, architecture rules, or area skill
content. Link to the canonical file instead. Provider-specific directories such
as `.claude/` and `.cursor/` are ignored local state and must not contain tracked
repo guidance.

## Current Adapters

| Surface                        | Role                                                                       |
| ------------------------------ | -------------------------------------------------------------------------- |
| `AGENTS.md`                    | Root orientation and codebase map for agents that auto-load AGENTS files.  |
| `ui/AGENTS.md`                 | UI directory map; points to `docs-ai/skills/ui/SKILL.md`.                  |
| `internal/providers/AGENTS.md` | Provider package map; points to `docs-ai/skills/providers/SKILL.md`.       |
| `CLAUDE.md`                    | Claude Code compatibility shim importing `AGENTS.md`; no standalone rules. |

Local tool state files under directories such as `.claude/` or `.cursor/` may
exist for per-machine permissions, sessions, or editor state. They are ignored
by Git, are not guidance surfaces, and must not define repository rules.

## Updating Guidance

When adding or changing agent guidance:

1. Update the canonical `docs-ai/` file first.
2. Update adapter files only when discovery, command names, or links change.
3. If a skill is added, moved, or removed, update
   [`../skills/README.md`](../skills/README.md) in the same change.
4. Do not add tracked `.claude/` or `.cursor/` files.
5. Run `just agent-docs-check` for adapter consistency.
6. Run `just docs-format-check`; run `just check-links` when links changed.

Agent-doc-only changes use the `chore(agent):` commit scope.

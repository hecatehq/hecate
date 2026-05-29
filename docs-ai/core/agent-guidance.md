# Agent Guidance Surfaces

Hecate keeps one provider-neutral instruction layer for agents:
[`docs-ai/`](../README.md). Tool-specific files exist only so individual
agent products can discover that layer.

## Source Of Truth

`docs-ai/` owns the substance:

- `core/` — project context, engineering standards, workflow, verification,
  and this adapter policy.
- `tasks/` — reusable task playbooks such as planning, debugging, maintenance,
  refactoring, code review, and release.
- `skills/` — Hecate-owned area and posture skills. These are not
  Claude-only; any agent or human can read and follow them.

If guidance applies to more than one tool, put it in `docs-ai/`. Do not add a
Claude-only, Cursor-only, or Codex-only copy of the same rule.

## Adapter Files

Adapter files may contain only:

- A short pointer to `docs-ai/README.md`.
- Tool-specific discovery plumbing, such as `.claude/skills` symlinks or
  Cursor `.mdc` frontmatter.
- Tool-specific command wrappers, such as Claude Code slash commands, when
  they delegate to canonical docs.
- Tool permission/configuration baselines, such as `.claude/settings.json`.

Adapter files must not carry their own copies of planning triggers,
verification ladders, review rubrics, architecture rules, or area skill
content. Link to the canonical file instead.

## Current Adapters

| Surface                        | Role                                                                                      |
| ------------------------------ | ----------------------------------------------------------------------------------------- |
| `AGENTS.md`                    | Root orientation and codebase map for agents that auto-load AGENTS files.                 |
| `ui/AGENTS.md`                 | UI directory map; points to `docs-ai/skills/ui/SKILL.md`.                                 |
| `internal/providers/AGENTS.md` | Provider package map; points to `docs-ai/skills/providers/SKILL.md`.                      |
| `CLAUDE.md`                    | Thin Claude Code launcher; no standalone project rules.                                   |
| `.claude/skills/*`             | Symlink adapters to `docs-ai/skills/*` for Claude Code skill discovery.                   |
| `.claude/commands/*`           | Claude Code command wrappers that delegate to canonical verification guidance.            |
| `.claude/settings.json`        | Shared Claude Code permission baseline only; not an instruction override layer.           |
| `.cursor/rules/*.mdc`          | Thin Cursor launcher rules that point at `docs-ai/` files; no duplicated rule checklists. |

Local tool state files, for example `.claude/settings.local.json`, may exist
for per-machine permissions or sessions. They are not guidance surfaces and
must not define repository rules.

## Updating Guidance

When adding or changing agent guidance:

1. Update the canonical `docs-ai/` file first.
2. Update adapter files only when discovery, command names, or links change.
3. Keep `.claude/skills/*` in one-to-one sync with `docs-ai/skills/*`.
4. Run `just agent-docs-check` for adapter consistency.
5. Run `just docs-format-check`; run `just check-links` when links changed.

Agent-doc-only changes use the `chore(agent):` commit scope.

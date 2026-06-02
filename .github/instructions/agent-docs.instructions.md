---
applyTo: "AGENTS.md,CLAUDE.md,docs-ai/**,.github/copilot-instructions.md,.github/instructions/**"
---

# Agent Guidance Docs

Agent guidance is provider-neutral. Keep `docs-ai/` as the source of truth and
use adapter files only to help a specific product discover that guidance.

Before editing this area, read:

- `docs-ai/core/agent-guidance.md`
- `docs-ai/README.md`
- `docs-ai/skills/README.md`

Rules:

- Do not add tracked `.claude/` or `.cursor/` guidance.
- Keep `CLAUDE.md` as the compatibility shim importing `AGENTS.md`.
- Keep GitHub Copilot files short; they must point to `docs-ai/` rather than
  duplicating canonical skills or workflow rules.
- When adding, moving, or deleting a skill, update `docs-ai/skills/README.md`.
- Run `just agent-docs-check`, `just docs-format-check`, and `just check-links`
  when links change.

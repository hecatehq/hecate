---
applyTo: "ui/**"
---

# Operator UI

For UI work, read `ui/AGENTS.md` and `docs-ai/skills/ui/SKILL.md` before
editing.

High-signal rules:

- Build for an operator console: dense, calm, scannable workflows over
  marketing-style composition.
- Reuse existing primitives, design tokens, API helpers, and test setup.
- Keep TypeScript mirrors in sync with Go API response shapes.
- Hecate-native endpoints use `{object, data}` envelopes; UI clients should
  read `payload.data`.
- Verify with `cd ui && bun run typecheck && bun run test`; never use
  `bun test`.

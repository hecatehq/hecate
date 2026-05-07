# Hecate UI

React/Vite operator UI under `ui/`. Sibling to the root [`AGENTS.md`](../AGENTS.md), which covers the Go side.

The substance for UI work — product lens, layout rules, conventions,
build/test commands, gotchas, recipes — lives in the canonical UI skill:
[`../docs-ai/skills/ui/SKILL.md`](../docs-ai/skills/ui/SKILL.md). Read it before
making UI changes.

## At a glance

```
ui/src/
  app/                    shell, mode switch, top-level orchestration
  features/
    runs/                 TasksView, TaskDetail, NewTaskSlideOver
    chats/                ChatView
    transcript/           reusable Chats transcript rendering pieces
    overview/             ConnectYourClient, ObservabilityView
    settings/             SettingsView, PricebookTab
    providers/            ProvidersView
    shared/               primitives, pickers, overlays; ui.tsx is a compatibility barrel
  lib/                    api.ts (incl. streamTaskRun SSE), markdown.ts, runtime-utils.ts
  types/runtime.ts        TypeScript mirrors of the Go API types — keep in lockstep
  styles.css              design tokens, .dropdown-menu rule, animations
```

## Build / test

| Command | Use for |
|---|---|
| `bun run typecheck` | Fast type check after any edit (`tsgo -b` under the hood) |
| `bun run test` | Vitest run before committing — never `bun test` (skips testing-library DOM setup) |
| `bun run test:watch` | Iteration |
| `bun run dev` | Vite dev server on `:5173` proxying API to `:8765` |

Claude Code shortcut: `/test-affected` from the repo root when Go packages are touched. For UI work, run `bun run typecheck` and `bun run test` directly.

## Where to go for depth

- Conventions (match existing design, no duplicate summary surfaces, stable provider ordering, short tab labels, etc.) — [`../docs-ai/skills/ui/SKILL.md`](../docs-ai/skills/ui/SKILL.md).
- Accessibility baseline (semantic controls, keyboard paths, focus management, contrast, reduced motion) — [`../docs-ai/skills/ui/SKILL.md`](../docs-ai/skills/ui/SKILL.md).
- Test patterns (the `setup()` helper) — [`../docs-ai/skills/ui/SKILL.md`](../docs-ai/skills/ui/SKILL.md).
- UI gotchas (dropdown clipping, `bun test` vs `bun run test`, stale task IDs, snapshot churn) — [`../docs-ai/skills/ui/SKILL.md`](../docs-ai/skills/ui/SKILL.md).
- Recipes (SSE-driven state field, paired pickers, snapshot refresh) — [`../docs-ai/skills/ui/SKILL.md`](../docs-ai/skills/ui/SKILL.md).
- Project-wide rules (Conventional Commits, `chore(...)` for agent-doc updates, no emojis, no plan labels) — [`../docs-ai/core/workflow.md`](../docs-ai/core/workflow.md), [`../docs-ai/core/engineering-standards.md`](../docs-ai/core/engineering-standards.md).

## Canonical product docs (UI-relevant)

- [`../docs/architecture.md`](../docs/architecture.md) — request flow, what the UI is observing.
- [`../docs/runtime-api.md`](../docs/runtime-api.md) — task / run / approval endpoints the UI calls.
- [`../docs/events.md`](../docs/events.md) — every `/hecate/v1/events` event type with payload shapes.
- [`../docs/development.md`](../docs/development.md) — UI hot reload, screenshot tooling.

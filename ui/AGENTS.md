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
    connections/          ConnectionsPanel — providers, model capabilities, external agents
    providers/            provider catalog/editor components used by Connections
    projects/             ProjectsView top-level orchestration plus extracted
                          workspace/detail/timeline/health/memory/skills panels
    settings/             SettingsView — local data cleanup / retention controls
    usage/                UsageView
    shared/               primitives, pickers, overlays; ui.tsx is a compatibility barrel
  lib/                    api.ts (incl. streamTaskRun SSE), markdown.ts, provider/readiness helpers
  types/                  TypeScript mirrors of Go API types — keep in lockstep
  styles.css              design tokens, .dropdown-menu rule, animations
```

## Build / test

| Command              | Use for                                                                           |
| -------------------- | --------------------------------------------------------------------------------- |
| `bun run typecheck`  | Fast type check after any edit (`tsgo -b` under the hood)                         |
| `bun run test`       | Vitest run before committing — never `bun test` (skips testing-library DOM setup) |
| `bun run test:watch` | Iteration                                                                         |
| `bun run dev`        | Vite dev server on `:5173` proxying API to `:8765`                                |
| `bun run test:e2e`   | Playwright with Vite's API proxy disabled; mock each API route explicitly         |

Canonical verification rules live in
[`../docs-ai/core/verification.md`](../docs-ai/core/verification.md). For UI
work, run `bun run typecheck` and `bun run test`.

## Where to go for depth

- Conventions (match existing design, no duplicate summary surfaces, stable provider ordering, short tab labels, etc.) — [`../docs-ai/skills/ui/SKILL.md`](../docs-ai/skills/ui/SKILL.md).
- Accessibility baseline (semantic controls, keyboard paths, focus management, contrast, reduced motion) — [`../docs-ai/skills/ui/SKILL.md`](../docs-ai/skills/ui/SKILL.md).
- Test patterns (the `setup()` helper) — [`../docs-ai/skills/ui/SKILL.md`](../docs-ai/skills/ui/SKILL.md).
- UI gotchas (dropdown clipping, `bun test` vs `bun run test`, stale task IDs, snapshot churn) — [`../docs-ai/skills/ui/SKILL.md`](../docs-ai/skills/ui/SKILL.md).
- Recipes (SSE-driven state field, paired pickers, snapshot refresh) — [`../docs-ai/skills/ui/SKILL.md`](../docs-ai/skills/ui/SKILL.md).
- Project-wide rules (Conventional Commits, `chore(...)` for agent-doc updates, no emojis, no plan labels) — [`../docs-ai/core/workflow.md`](../docs-ai/core/workflow.md), [`../docs-ai/core/engineering-standards.md`](../docs-ai/core/engineering-standards.md).

## Projects UI map

Keep `ProjectsView.tsx` as the parent page for data loading, selected-project
controller wiring, and mutation dispatch. Add project rendering to the extracted
surface that owns it:

- `ProjectWorkspaceView.tsx` — selected-project shell, onboarding, workspace
  tabs, work inbox, and empty blocks.
- `ProjectWorkItemDetail.tsx` — work item detail, assignment rows,
  handoff-linked starts, launch preflight, and chat-draft request shaping.
- `ProjectTimelinePanel.tsx` — timeline and decision-log rows.
- `ProjectHealthPanel.tsx` — attention and health popovers.
- `ProjectMemoryPanel.tsx` / `ProjectSkillsPanel.tsx` — memory/context review
  and project skill registry surfaces.
- `useProjectViewController.ts` / `useProjectAssistantController.ts` —
  selected-project shell state and Project Assistant request orchestration.

New project UI code needs focused tests next to the component/helper it changes.
Update `ProjectsView.test.tsx` when parent loading, controller wiring, or
mutation orchestration changes.

## Canonical product docs (UI-relevant)

- [`../docs/contributor/architecture.md`](../docs/contributor/architecture.md) — request flow, what the UI is observing.
- [`../docs/runtime/runtime-api.md`](../docs/runtime/runtime-api.md) — task / run / approval endpoints the UI calls.
- [`../docs/runtime/events.md`](../docs/runtime/events.md) — every `/hecate/v1/events` event type with payload shapes.
- [`../docs/contributor/development.md`](../docs/contributor/development.md) — UI hot reload, screenshot tooling.

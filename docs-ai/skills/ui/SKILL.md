---
name: hecate-ui
description: Use when working on the Hecate operator UI in `ui/`. Keeps frontend work aligned with Hecate's operator-console workflows, runtime-debugging focus, and React/Vite stack.
---

# Hecate UI skill

Use this skill for any work inside `ui/`. Backend work uses [`../backend/SKILL.md`](../backend/SKILL.md).

## Canonical guidance lives here

- [`../../core/project-context.md`](../../core/project-context.md) — toolchain pin (Bun, React 19, Vite, Vitest), `bun run test` ≠ `bun test` warning.
- [`../../core/engineering-standards.md`](../../core/engineering-standards.md) — type-name mirroring with the Go side, design-token discipline, anti-patterns.
- [`../../core/workflow.md`](../../core/workflow.md) — operating loop, planning triggers, commit etiquette (UI agent-doc updates use `chore:`).
- [`../../core/verification.md`](../../core/verification.md) — UI verification ladder, snapshot review, done criteria.

## Product lens

The Hecate UI should feel like:

- An operator console.
- A runtime control surface.
- A debugging and inspection tool.

It should not feel like:

- A generic SaaS dashboard full of cards.
- A landing page with product-marketing copy.
- A toy chat surface without production context.

Default to utility, orientation, and workflow clarity.

## Visual thesis

Calm, technical, and deliberate. Dense enough to be useful, sparse enough to scan. Strong hierarchy, minimal chrome, one clear accent system.

Prefer restrained layout, strong type hierarchy, clear sectioning without over-carding, obvious status and health states, meaningful spacing over decorative surfaces.

Avoid card mosaics, oversized hero copy, decorative gradients behind routine product UI, multiple accent colors fighting for attention, visual noise that hides runtime state.

## UX priorities

Every screen should answer the following quickly:

1. What system am I looking at?
2. What is its current state?
3. What can I do here?
4. What changed or failed?
5. Where do I go next?

When choosing between "pretty" and "operationally clear," choose clarity.

## Accessibility baseline

Accessibility is part of the design pass, not a cleanup pass. Every UI change
should preserve keyboard, screen-reader, focus, contrast, and motion ergonomics
unless there is an explicit product reason and a documented follow-up.

- Use semantic HTML first: buttons for actions, anchors for navigation, labels
  for form controls, tables for real tables, headings that reflect structure.
- Every interactive control needs a visible focus state and a keyboard path.
  Dropdowns, dialogs, popovers, and slideovers must support Escape / outside
  dismissal where appropriate and return focus to the trigger.
- Icon-only controls need accessible names. Status chips and color-coded states
  need text, not color alone.
- Modal and dialog work must include focus management, `aria-modal`, labelled
  titles, and non-trapping escape paths.
- Keep contrast readable in the dark operator theme. Muted text can be quiet,
  but it still needs to be legible against panels and borders.
- Respect reduced-motion expectations. Motion should orient; avoid relying on
  animation to convey state.
- Add or update tests for accessibility-sensitive behavior when changing UI
  primitives: role/name queries, focus movement, disabled states, and keyboard
  interactions are preferred over brittle DOM selectors.

## Information architecture

Organize the UI around operator jobs, not components:

- Understand local runtime readiness and current configuration.
- Inspect providers and models.
- Run and compare requests.
- Inspect trace and runtime metadata.
- Manage providers, pricing, retention, and policy-adjacent state.
- Inspect budget state and controls.

Views are task-shaped, not component-shaped. If a page mixes too many concerns, split it.

## Section responsibilities

Each section has exactly one job: orient, inspect, compare, edit, or confirm. If a section tries to explain the whole system at once, break it apart.

## Hecate-specific UI rules

- Do not add auth, tenant, or account-management UI unless the product model
  changes again.
- Provider and model selection exposes local and cloud distinctions clearly.
- In Chats, keep the two top-level targets distinct: **Hecate Chat** and
  **External Agent**. Hecate Chat owns provider/model selection; its tools
  toggle switches between direct Model chat and Hecate Agent task execution.
  Adapter/workspace/native session diagnostics belong to External Agent.
- Hecate Agent sessions store a workspace plus backing `task_id` /
  `latest_run_id`. New UI affordances should show per-turn task links in the
  transcript. Chats may resolve pending task approvals inline; Tasks remains
  canonical for artifacts, retry/resume, full event history, and patch review.
  While a task-backed segment is active, the whole Hecate Chat session is busy:
  keep provider/model controls locked to the segment snapshot. If the operator
  submits another prompt, queue it locally in the composer and send it after
  the task finishes, is stopped, or reaches a terminal approval outcome. Do not
  pretend that local queue is durable before the message is submitted.
- Use the shared `features/transcript` primitives for runtime storytelling.
  Task Detail and Hecate Chat should share `TranscriptActivityTimeline` labels
  and Details grouping instead of growing separate task/activity renderers.
  Task Detail may add Task-specific advanced disclosures, but keep that debug
  layer out of Chats unless the operator explicitly asks for it.
- External Agent sessions store their workspace and native ACP session id. New
  UI affordances should preserve that continuity instead of treating every
  prompt as a one-off subprocess.
- Model capability badges gate Hecate Agent sends. Explicitly tools-off models
  should offer an inline enable/override path; unknown local/custom models
  should explain how to record a manual probe result or operator override in
  Settings, not silently run as an agent.
- Stale selected-model readiness is a composition blocker, not a post-send
  error toast. If the selected model is not in the current model picker for the
  selected route, hide/disable send and show the selected model, provider route,
  discovered-model count, health, blocked-by, last-error, and repair steps.
  The empty-state "compact" version may be shorter, but it must still include
  discovered-model count and at least a short remediation list; don't reduce it
  to a dead-end warning. If the backend provides a suggested replacement model,
  expose it as an action and reset the provider route to "All providers" before
  selecting it, because the suggested model may belong to a different provider.
- Agent Chat readiness belongs in Connections and in the picker
  diagnostics: distinguish missing binaries, auth/billing problems, unsupported
  versions, and managed-launcher issues without sending users to raw logs first.
  To smoke-test missing/available adapter states without uninstalling local
  tools, use `just dev-no-agent-adapters` or `just dev-agent-adapters
  'claude_code=missing,codex=available'`. The override is discovery-only; do
  not write tests that expect a forced-available adapter to run a real session.
- Agent Chat usage is adapter-reported. Show it as helpful telemetry with the
  "reported by adapter · not enforced by Hecate" caveat, never as Hecate-enforced
  billing.
- Agent Chat file changes are already applied to the selected workspace when
  Hecate captures them. UI copy should say "inspect" / "revert" / "keep", not
  "apply", unless the backend grows a true staged-artifact flow.
- **Stable provider ordering.** Do not sort provider lists by health, blocked state, or availability unless explicitly asked. Fixed alphabetical/preset order within each section.
- Runtime metadata first-class, not tucked in debug crumbs.
- Trace and failure details readable without scanning raw JSON first.
- Cost, cache, routing, and retry behavior visible in plain language.
- Dangerous or privileged actions visually separated from routine actions.
- Short tab labels OK for navigation; the active view uses a more descriptive section header inside the page.
- **No duplicate summary surfaces.** If data is already visible on the page, prefer hierarchy fixes, clearer labels, or progressive disclosure over adding a second persistent surface that restates the same state.
- Docs-only updates to `ui/AGENTS.md` and `ui/SKILL.md` use `chore:`, not `docs:` (project-wide rule in [`../../core/workflow.md`](../../core/workflow.md); restated here because it's UI-doc-adjacent).

## Layout guidance

Default app layout: top-level shell + primary navigation/mode switch + main workspace + optional secondary inspector. Layout primitives over card wrappers.

Cards only when the card itself is the interaction boundary:

- A selectable provider target.
- A provider credential or pricing record with contained actions.
- A focused result panel that benefits from separation.

**Do not add a new persistent inspector, side rail, dashboard block, or summary panel without explicit user approval first.** Improve the existing workspace before expanding the surface area.

## Copy guidance

Product UI copy, not marketing copy.

Good labels: Session, Chats, Provider Routing, Runtime Output, Trace, Budget State, Policy.

Good supporting copy explains scope, freshness, operator impact, and the next action. Bad supporting copy is hype, mood statements, abstract claims, or a repeated explanation of what Hecate is.

## Motion guidance

Motion supports orientation, not decoration. Allowed: view transitions that help users understand context shifts; subtle reveal of runtime output or trace detail; emphasis for status changes or async loading. Avoid ornamental motion or large attention-grabbing effects in core workflows.

## Code organization

```
src/
  app/                  app shell, top-level orchestration, route/mode switching
  features/
    runs/               TasksView, TaskDetail, NewTaskSlideOver, TaskList — agent task list + run replay (the headline UI)
    chats/              ChatView — interactive chat against the gateway
    transcript/         reusable transcript pieces for Chats and Task Detail: markdown, message rows, activity timeline, file diff review
    overview/           ConnectYourClient, ObservabilityView — request ledger + trace drilldown + Codex/Claude Code setup
    connections/        ConnectionsPanel — provider readiness, model capabilities, external-agent setup/grants
    settings/           SettingsView, PricebookTab — pricing, retention, non-connection configuration
    providers/          ProvidersView — detailed provider catalog/editor
    shared/             primitives, pickers, overlays; ui.tsx is a compatibility barrel
  lib/
    api.ts              fetch wrappers + streamTaskRun (SSE consumer)
    markdown.ts, provider-utils.ts, runtime-utils.ts
  types/
    runtime.ts          TypeScript mirrors of Go API types — keep in lockstep with pkg/types/ and internal/api/
  test/                 shared test setup
  styles.css            design tokens, .dropdown-menu rule, animations
```

There is no `src/components/`. Reusable primitives live in `src/features/shared/ui.tsx`; feature-specific components live with their feature.

When a file gets crowded, split by responsibility, not arbitrary line count: view shell vs data hooks; presentation vs transport; domain formatting vs generic utilities.

## State and data rules

- Keep remote data shapes close to the API contracts.
- Normalize only where the UI benefits from it.
- Make loading, empty, and error states explicit.
- Prefer derived display helpers over inline formatting logic scattered across JSX.
- Avoid giant top-level components that fetch, normalize, render, and mutate everything at once.

## Build / test commands

| Command | What it does | When to use |
|---|---|---|
| `bun run typecheck` | `tsgo -b` — fast type check, no test execution | First sanity check after edits |
| `bun run test` | `vitest run` — full test suite | Before committing |
| `bun run test:watch` | watch mode | During iteration |
| `bun run dev` (or `just ui-dev` from repo root) | Vite dev server on `:5173`, proxying API to `:8765` | Live UI iteration alongside `just dev` |

**Never `bun test`** — it skips testing-library DOM setup and panics with `document[isPrepared]`. Always `bun run test`.

## Test patterns

```tsx
function setup(overrides: Partial<React.ComponentProps<typeof TaskDetail>> = {}) {
  const props: React.ComponentProps<typeof TaskDetail> = {
    /* sane defaults including new fields like streamTurnCosts: new Map() */
    ...overrides,
  };
  const user = userEvent.setup();
  return { props, user, render: () => render(<TaskDetail {...props} />) };
}
```

When the Go side adds a required prop (e.g. `streamTurnCosts`), update the `setup` helper in the affected `*.test.tsx` files first — TypeScript will surface every test that needs the new value.

## UI gotchas

- **`.dropdown-menu` has `left: 0`** baked into the `.dropdown-menu` rule in `styles.css`. When using `useFloatingDropdownStyle` with `align="right"`, the hook explicitly sets `left: "auto"` to override. Don't remove that — without it the dropdown stretches viewport-wide.
- **Slideover overflow clipping** — dropdowns inside `<NewTaskSlideOver>` get clipped by the slideover's overflow. Always use `useFloatingDropdownStyle` (which uses `position: fixed` to escape) for any dropdown that might appear inside a panel. See `ProviderPicker` / `ModelPicker` in `shared/ui.tsx` for the pattern.
- **404 on stale task IDs** — `localStorage` may hold a task ID from a prior gateway boot (memory backend resets on restart). `TasksView` drops the dead row from the list and re-loads. Don't propagate the 404 as an error toast.
- **`render1()` + `render2()` in the same `it` block** — don't. React Testing Library cleanup runs between tests, not within. Split into two `it`s if you need fresh mounts.
- **Cost-ceiling banner** — gates on `run.otel_status_message === "cost_ceiling_exceeded"` (the specific string). A regression that drops or rewords that string silently breaks the "Raise ceiling & resume" affordance.
- **Every gateway response is `{object, data}`** — `lib/api.ts` clients must read `payload.data.<field>`, not `payload.<field>`. When mocking, copy the real wire shape, not the fields you happen to need; fixtures that skip the envelope hide production bugs.
- **No auth surfaces in alpha** — do not reintroduce token gates, tenant tabs,
  or key-management tabs unless the product model changes again.

## UI recipes

### Add a new SSE-driven UI state field

1. Add the field to `types/runtime.ts` `TaskRunStreamEventData` (matching the Go `TaskRunStreamEventData` shape exactly).
2. Accumulate it in `TasksView` — new `useState`, populate inside `streamTaskRun`'s `onPayload` callback, reset in `resetRunDetail`.
3. Drill via props to `TaskDetail` and any consumer.
4. Add to the `setup` defaults in affected `*.test.tsx` files.
5. Add a focused test asserting the prop reaches the rendered output (see `TaskDetail.test.tsx` `falls back to streamTurnCosts...` for a template).

### Add a paired provider+model picker

Reuse `ProviderPicker` + `ModelPicker` from `features/shared/ui.tsx`. Pass `modelWarnings` to surface capability hints (e.g. "model lacks tool-calling"). Both pickers use `useFloatingDropdownStyle` — drop them into a slideover with no extra wrapping.

### Refresh a snapshot test

Run `bun run test -- -u` to update committed snapshots. Review the diff carefully — accidental snapshot churn is the most common silent regression vector.

## Done criteria

See [`../../core/verification.md`](../../core/verification.md).

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

## Consistency guardrails

Before adding or changing UI, find the closest existing precedent and reuse it unless the product behavior truly differs. Hecate's UX should feel like one operator console, not a set of unrelated experiments.

- Reuse shared primitives for recurring patterns: `DropdownPicker`, `BrandAvatar`, `CopyableID`, `TranscriptActivityTimeline`, badges, inline errors, and modal/slide-over chrome.
- Keep button height, dropdown rhythm, icon treatment, metadata labels, empty-state spacing, and hover/focus affordances consistent across Chats, Tasks, Connections, Observability, and Usage.
- Long machine identifiers belong behind compact labels with tooltip + copy affordances. Prefer full context in Details or Task Detail over leaking raw ids into list rows.
- If a screen already has an empty, loading, repair, or onboarding precedent, extend that component/copy path instead of adding a second parallel state.
- When making Hecate Chat and External Agent behavior more similar, preserve their real runtime differences: Hecate owns task-backed tools; external agents own ACP-native sessions.
- Add role/name-based tests for shared controls and view-level tests for every state that previously regressed. E2E should cover cross-view navigation, onboarding, and setup/repair flows when a bug came from app composition.
- Before filing a PR or pushing a PR update that touches UI/TypeScript, run the
  UI verification ladder: `cd ui && bun run typecheck`, `bun run lint`,
  `bun run format:check`, and `bun run test`. Add same-change tests for
  production UI code, and targeted Playwright coverage when the change is a
  workflow, routing, onboarding, or regression fix. If Go files changed too,
  run the backend ladder as well.

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
- Manage Connections, local cleanup, and policy-adjacent state.
- Inspect usage events and reported cost where available.

Views are task-shaped, not component-shaped. If a page mixes too many concerns, split it.

## Section responsibilities

Each section has exactly one job: orient, inspect, compare, edit, or confirm. If a section tries to explain the whole system at once, break it apart.

## Hecate-specific UI rules

- Do not add auth, tenant, or account-management UI unless the product model
  changes again.
- Provider and model selection exposes local and cloud distinctions clearly.
- In Chats, use the shared agent-picker shell. **Hecate** is the built-in
  choice and owns provider/model selection; its tools toggle switches between
  direct model chat and Hecate-owned task execution. Codex, Claude Code,
  Cursor, and Grok Build choices create External Agent sessions where
  adapter/workspace/native session diagnostics belong.
- Hecate-owned chats store provider/model state on the session and durable
  runtime snapshots on each message. Tools-on turns create or continue a backing
  task and should show per-turn task links in the transcript. Chats may resolve
  pending task approvals inline; Tasks remains canonical for artifacts,
  retry/resume, full event history, and patch review. While a task-backed
  segment is active, the whole Hecate Chat session is busy: keep
  provider/model controls locked to the segment snapshot. If the operator
  submits another prompt, queue it locally in the composer and send it after
  the task finishes, is stopped, or reaches a terminal approval outcome. Do not
  pretend that local queue is durable before the message is submitted.
- Use the shared `features/transcript` primitives for runtime storytelling.
  Task Detail and Hecate Chat should share `TranscriptActivityTimeline` labels
  and Details grouping instead of growing separate task/activity renderers.
  Task Detail may add Task-specific advanced disclosures, but keep that debug
  layer out of Chats unless the operator explicitly asks for it.
- Transcript rows should have one source of truth for noisy details. Do not
  repeat captured command/read output both in the compact row and in the output
  preview card. Do not render raw captured diffs in the transcript when the
  message already has a workspace-changes badge or the side-panel diff viewer.
  Group repetitive command rows into a single expandable summary that reveals
  commands and captured output together.
- Keep workspace review and workspace browsing as separate UI surfaces. The
  workspace changes panel's Review tab owns changed-file diffs, copy, and
  discard actions; the Files tab owns the full workspace tree. Keep the full
  tree collapsed by default and expand matching directories only when the
  operator searches or opens them. When the workspace panel is visible, refresh
  it after an active chat/agent turn settles so the operator sees live changes
  without pressing Refresh; keep the explicit Refresh action for manual
  rechecks and recovery.
- External Agent sessions store their workspace and native ACP session id. New
  UI affordances should preserve that continuity instead of treating every
  prompt as a one-off subprocess.
- Model capability badges are guidance and guardrails for tools, not a reason
  to hide plain chat. Hecate Chat should remain available when a model is
  routable; task-backed tools-on turns require `tool_calling="basic"` or
  `parallel`. Unknown local/custom models should show a clear capability
  indicator, fall back to direct model chat rather than failing the whole
  transcript, and suggest choosing a known tool-capable model for task-backed
  turns. Put the fallback state in the chat header/status line, not as a noisy
  composer warning. Connections shows observed provider/catalog capability
  metadata, not a global "tools on/off" override editor.
- Stale selected-model readiness is a composition blocker, not a post-send
  error toast. If the selected model is not in the current model picker for the
  selected route, hide/disable send and show the selected model, provider route,
  discovered-model count, health, blocked-by, last-error, and repair steps.
  The empty-state "compact" version may be shorter, but it must still include
  discovered-model count and at least a short remediation list; don't reduce it
  to a dead-end warning. If the backend provides a suggested replacement model,
  expose it as an action that switches to the suggested provider/model pair
  explicitly; don't silently widen a stale route back to a hidden fallback.
- Chat send blockers should flow through `resolveChatSetupRepairState` in
  `ui/src/lib/chat-setup-readiness.ts`. Keep the empty state, composer notice,
  and disabled-send copy aligned there instead of adding one-off branches to
  `ChatView`.
- Projects UI keeps top-level data loading, selected-project orchestration, and
  mutation dispatch in `ProjectsView`; do not add large rendering branches back
  to that parent. Project selection and persisted right-panel width live in
  `useProjectViewController`, and Project Assistant orchestration lives in
  `useProjectAssistantController`.
- Project workspace UI is split by surface. The selected-project shell,
  onboarding, workspace tabs, work inbox, and empty blocks live behind
  `ProjectWorkspaceView`. Work item detail, assignment rows, handoff-linked
  start controls, launch preflight state, and chat-draft shaping live in
  `ProjectWorkItemDetail`. Timeline/decision-log rows live in
  `ProjectTimelinePanel`; attention/health popovers live in
  `ProjectHealthPanel`; project memory/context review lives in
  `ProjectMemoryPanel`; project skill registry UI lives in `ProjectSkillsPanel`.
  Keep rendering and form tests colocated with the component that owns the
  surface, and add parent-page tests only when loading, controller wiring, or
  mutation orchestration changes.
- Project defaults/root editing lives in `ProjectSettingsPanel` plus
  `CreateProjectWorktreeModal`. Agent profile and project role editing lives in
  `ProfilesModal` and `RolesModal`, with shared profile/role form mapping in
  `projectProfilesRoles.ts`. Work item, assignment, and handoff editing lives in
  `ProjectWorkItemModals`, `ProjectAssignmentModals`, and `ProjectHandoffModal`,
  with payload/ref shaping in `projectWorkForms.ts`; keep project state loading
  and mutations in the parent page. Shared project UI string/id helpers live in
  `projectUtils.ts`; display-label helpers live in `projectDisplay.ts`; status
  option lists and form-safe status normalization live in `projectWorkForms.ts`.
- External Agent readiness belongs in Connections and in the picker
  diagnostics: distinguish missing binaries, auth/billing problems, unsupported
  versions, and managed-launcher issues without sending users to raw logs first.
  To smoke-test adapter states without uninstalling local tools, use
  `just dev-no-agent-adapters` or
  `just dev-agent-adapters 'claude_code=no_auth,codex=ready,cursor_agent=app_missing'`.
  These fixture env vars are test/development-only and intentionally absent from
  `.env.example`; do not write tests that expect a forced-ready adapter to run a
  real session.
- External Agent usage is adapter-reported. Show it as helpful telemetry with the
  "reported by adapter · not enforced by Hecate" caveat, never as Hecate-enforced
  billing.
- External Agent file changes are already applied to the selected workspace when
  Hecate captures them. UI copy should say "inspect" / "revert" / "keep", not
  "apply", unless the backend grows a true staged-artifact flow.
- Project assignment UI reads canonical backend contracts. `turn_kind` is the
  chat turn discriminator, and project assignment runtime links come from
  `execution_ref` / activity linked ids. Do not infer task-backed, direct-model,
  or external-agent state from legacy `execution_mode`, `tools_enabled`, raw
  `task_id`, raw `run_id`, or `chat_session_id` fields. Project rows, activity,
  health, and timeline surfaces should use
  `ui/src/features/projects/projectAssignmentViewModels.ts` instead of
  reconstructing status/link logic in components. Keep compact assignment row
  evidence separate from the full Context Inspector: the row summarizes
  canonical refs and warnings, while the inspector renders the persisted packet
  sections the agent actually saw.
- Do not add UI "compatibility" fallback chains for contracts that the backend
  has already made canonical. If a current feature appears to need old raw
  fields or inferred state, update the view model / API contract deliberately
  instead of rebuilding the removed fallback in a component.
- Project assignment launch controls must review
  `/assignments/{assignment_id}/preflight` before dispatch. Use the shared
  Context Inspector panel for the launch packet, then call start only from the
  operator's confirm action. This applies to normal assignment rows and
  handoff-linked starts.
- Reviewer follow-through stays handoff-based. A request-review action may
  prefill a handoff to a work item's `reviewer_role_ids` and carry source
  assignment/run/chat/context refs, but creating the follow-up assignment and
  starting it remain separate operator actions.
- Review outcomes are `kind="review"` collaboration artifacts. The V1 cockpit
  entry point is an assignment whose role appears in the work item's
  `reviewer_role_ids`; work without configured reviewer roles should surface
  setup guidance instead of a generic record-review button. Keep the record
  action separate from follow-up handoff creation; review artifacts may offer an
  explicit follow-up assignment action, but it must create/link the handoff
  first and leave execution start as a separate operator action. Review
  artifacts can carry structured
  `review_verdict`, `review_risk`, `review_follow_up_required`, and
  `reviewed_assignment_id` fields for triage, but the UI must not mark work
  done, blocked, or dispatched from those fields without an explicit operator
  action.
- Work closeout is explicit operator state. The Projects UI may compute and
  display closeout readiness from completed assignments, pending handoffs, and
  review follow-up artifacts, but marking a work item `done` must stay a
  deliberate operator action through the work-item update path.
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
- A provider credential or readiness record with contained actions.
- A focused result panel that benefits from separation.

**Do not add a new persistent inspector, side rail, dashboard block, or summary panel without explicit user approval first.** Improve the existing workspace before expanding the surface area.

## Copy guidance

Product UI copy, not marketing copy.

Good labels: Session, Chats, Provider Routing, Runtime Output, Trace, Usage, Policy.

Good supporting copy explains scope, freshness, operator impact, and the next action. Bad supporting copy is hype, mood statements, abstract claims, or a repeated explanation of what Hecate is.

## Motion guidance

Motion supports orientation, not decoration. Allowed: view transitions that help users understand context shifts; subtle reveal of runtime output or trace detail; emphasis for status changes or async loading. Avoid ornamental motion or large attention-grabbing effects in core workflows.

## Code organization

```
src/
  app/                  app shell, top-level orchestration, route/mode switching
    AppShell.tsx        the chrome (nav, theme, header) — consumes slice hooks directly
    App.tsx             mounts slice providers + <RootEffects />
    state/              the canonical state surface — every UI piece reads from here
      runtime.tsx         health, session, RTK availability, copy-command transient
      chat.tsx            chat sessions, composer state, in-flight machinery
      projects.tsx        project list, active project scope, create/rename/delete
      providersAndModels.tsx  provider status, presets, model catalog, agent adapters
      approvals.tsx       pending approvals + agent-chat grants
      retention.tsx       retention runs + subsystems
      usage.tsx           cost summary + recent events
      settings.tsx        server config snapshot + settings error + notice banner
      derived.ts          cross-slice derived selectors (useChatTarget, useRuntimeDerivedState, ...)
      rootEffects.ts      <RootEffects /> — dashboard-load, RTK-sync, queued-message-drain
      coordinators/
        chat.ts           submission + lifecycle + targeting + files + approvals (the big one)
        providers.ts      provider CRUD + readiness actions
        dashboard.ts      loadDashboard + refreshes
        settings.ts       runSettingsMutation + setNoticeMessage
        agentAdapters.ts  adapter credential + probe ops
        policy.ts         policy rule CRUD
        retention.ts      runRetention (wires the slice's Result to the notice banner)
        wired.ts          useWiredXActions — composes the cross-slice param graph once per view
        overrides.tsx     test-only CoordinatorOverridesContext for action stubs
  features/
    runs/               TasksView, TaskDetail, NewTaskSlideOver — agent task list + run replay (the headline UI)
    chats/              ChatView, ChatSidebar, ChatComposer, ChatHeader, ChatTranscript, ChatSettingsPanel, HecateTaskApprovalsBanner, ...
    projects/           ProjectsView, ProjectScopePanel, projectInsights — project identity, cockpit, memory/context, work coordination
    transcript/         reusable transcript pieces for Chats and Task Detail
    overview/           ConnectYourClient, ObservabilityView — request history + trace drilldown
    connections/        ConnectionsPanel — provider readiness, external-agent setup/grants
    settings/           SettingsView — local data cleanup / retention controls
    providers/          provider catalog/editor components used by Connections
    shared/             primitives, pickers, overlays; ui.ts is a compatibility barrel
    usage/              UsageView
  lib/
    api.ts              fetch wrappers + streamTaskRun (SSE consumer)
    persistedState.ts   useState wrapper that mirrors to localStorage with a schema guard
    format.ts, markdown.ts, provider-utils.ts, runtime-utils.ts
  types/                TypeScript mirrors of Go API types — keep in lockstep with pkg/types/ and internal/api/
    chat.ts, task.ts, provider.ts, model.ts, agent-adapter.ts, trace.ts, usage.ts, retention.ts, runtime.ts
  test/                 shared test setup
    runtime-console-test-composer.ts   test-only composer that aggregates slices + coordinators into the legacy {state, actions} shape
    runtime-console-fixture.ts         default fixture state + action stubs
    runtime-console-render.tsx         withRuntimeConsole(ui, fixture) wraps in slice providers seeded with fixture state
  styles.css            design tokens, .dropdown-menu rule, animations
```

There is no `src/components/`. Reusable primitives live in `src/features/shared/`; feature-specific components live with their feature.

When a file gets crowded, split by responsibility, not arbitrary line count: view shell vs data hooks; presentation vs transport; domain formatting vs generic utilities.

## State + action architecture

Views read slice state and call coordinator actions **directly** — there is no facade hook. The shape is:

- **Slice hooks** (`useRuntime`, `useChat`, `useSettings`, `useProvidersAndModels`, `useApprovals`, `useRetention`, `useUsage`) — each owns a `useReducer`-backed state slice and exposes `{state, actions}`. Views call them and destructure only the fields they use.
- **Coordinator hooks** (`useChatActions`, `useProviderActions`, `useDashboardActions`, `useSettingsActions`, `useAgentAdapterActions`, `usePolicyActions`, `useRetentionActions`) — each owns the cross-slice action implementations for a domain. Most coordinators take a small parameter bag for cross-coordinator wiring; the **`wired.ts`** hooks (`useWiredSettingsActions`, `useWiredDashboardActions`, `useWiredProviderActions`, `useWiredPolicyActions`) resolve that wiring once and are what views typically call.
- **Derived selectors** (`derived.ts`) — `useChatTarget`, `useRuntimeDerivedState`, `useNewChatAgentID` — for cross-slice values that don't belong in any single slice's state.
- **Root effects** (`rootEffects.ts`) — `<RootEffects />` is mounted once in `App.tsx` and owns all cross-slice effects (dashboard-load on mount, approvals catch-up on session switch, RTK-sync, notice auto-dismiss, provider/model default cascade, queued-message-drain). Don't add cross-slice effects inside views.

A typical view looks like:

```tsx
function MyView() {
  const chat = useChat(); // slice state + actions
  const { providers } = useProvidersAndModels().state;
  const chatTarget = useChatTarget(); // derived selector
  const { submitChat } = useChatActions({ chatTarget, setNoticeMessage });
  // ... or, more often, the wired variant that resolves the param bag for you:
  const { runSettingsMutation } = useWiredSettingsActions();
  return <div>...</div>;
}
```

Tests use `withRuntimeConsole(ui, fixture)` from `src/test/runtime-console-render.tsx` — it mounts all slice providers seeded with fixture state and an overrides context for action stubs. The 2284-LOC composition regression suite at `src/test/runtime-console-composition.test.tsx` exercises slices + coordinators end-to-end via the test-only `runtime-console-test-composer.ts`. Per-view tests don't need to touch the composer.

## State and data rules

- Keep remote data shapes close to the API contracts.
- Normalize only where the UI benefits from it.
- Make loading, empty, and error states explicit.
- Prefer derived display helpers over inline formatting logic scattered across JSX.
- Avoid giant top-level components that fetch, normalize, render, and mutate everything at once.

## Build / test commands

| Command                                         | What it does                                        | When to use                                      |
| ----------------------------------------------- | --------------------------------------------------- | ------------------------------------------------ |
| `bun run typecheck`                             | `tsgo -b` — fast type check, no test execution      | First sanity check after edits                   |
| `bun run lint`                                  | Type-aware Oxc lint checks                          | Before committing                                |
| `bun run format:check`                          | Oxfmt formatting check                              | Before committing                                |
| `just format-check`                             | Go + UI + website + docs formatting check           | Mixed-surface PRs or CI format failures          |
| `just docs-format-check`                        | Oxfmt Markdown / `.mdc` formatting check            | When UI changes update docs or screenshots       |
| `bun run test`                                  | `vitest run` — full test suite                      | Before committing                                |
| `bun run test:watch`                            | watch mode                                          | During iteration                                 |
| `bun run format`                                | Oxfmt source formatting                             | Formatting-only cleanup or after formatter drift |
| `just format`                                   | Local auto-format for Go, UI, website, and docs     | Fix formatter drift before pushing               |
| `bun run dev` (or `just ui-dev` from repo root) | Vite dev server on `:5173`, proxying API to `:8765` | Live UI iteration alongside `just dev`           |

**Never `bun test`** — it skips testing-library DOM setup and panics with `document[isPrepared]`. Always `bun run test`.

Playwright starts Vite with `VITE_DISABLE_API_PROXY=1`. E2E specs should mock
every API route they depend on; missing mocks should fail quietly as 404s, not
fall through to the Vite dev proxy and spam `ECONNREFUSED` for `:8765`.

Oxc config lives at repo root in `.oxlintrc.json` and is shared by the UI and
website. UI and website lint scripts run `oxlint --type-aware`, backed by the
`oxlint-tsgolint` package. The config enables the React, JSX accessibility,
Vitest, import, TypeScript, Unicorn, and Oxc rule families, with current
legacy-noise rules disabled explicitly. Do not loosen the config casually; if a
rule is noisy, name the specific rule and why it is disabled.

Markdown and `.mdc` docs are formatted by Oxfmt through `just docs-format` /
`just docs-format-check`. Keep lychee for link and anchor validation; Oxfmt
does not replace link checking.

Do not mix broad Oxfmt churn into a feature diff unless the task is explicitly a
formatting pass. When only UI source drifted, run `bun run format`; when the PR
touches multiple surfaces or CI reports a format failure, run `just format`,
then review the mechanical diff separately from behavior changes.

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
- **Task refresh belongs to the selected task header** — the Tasks list is for
  selection and creation. Keep task/run refresh with the selected task header,
  next to run controls, so it matches Chat and Project header action placement.
- **Runtime activity output stays flat when it is standalone** — stdout/stderr
  artifacts with no primary tool row should render inline as output previews.
  Failed tools keep their richer diagnostics view; don't wrap simple output in
  nested `Artifacts -> Output -> preview` disclosures.
- **Project cockpit action ownership** — project-global actions live in the
  project header. Needs Attention is a dropdown, Project Settings opens the
  shared right-side inspector pattern, and Work Coordination / Timeline /
  Memory / Skills are workspace tabs. Needs Attention rows should route to the
  matching operator surface: settings for setup gaps, Memory for context and
  candidates, Skills for project skill registry issues, Profiles/Roles for
  broken references, and Work Coordination for assignment/activity issues.
  Assignment launch preflight must keep Connections as the provider/model
  readiness repair surface while linking project-local defaults back to Project
  Settings, Roles, and Agent Profiles. Work with Project Assistant Bootstrap
  through one reviewable proposal path: UI helpers may refresh guidance/skills
  first, but they should still call the normal draft/apply flow rather than
  mutating setup directly. Work Coordination uses one Work Queue with All /
  activity filters plus one selected work-item card; don't split the same work
  state across a separate Activity Inbox, Work Items list, and detail card.
  Keep the Projects index as a fixed left panel; don't add or restore a
  collapsed mini-rail until the navigation pattern is redesigned.
- **`render1()` + `render2()` in the same `it` block** — don't. React Testing Library cleanup runs between tests, not within. Split into two `it`s if you need fresh mounts.
- **Cost-ceiling banner** — gates on `run.otel_status_message === "cost_ceiling_exceeded"` (the specific string). A regression that drops or rewords that string silently breaks the "Raise ceiling & resume" affordance.
- **Every gateway response is `{object, data}`** — `lib/api.ts` clients must read `payload.data.<field>`, not `payload.<field>`. When mocking, copy the real wire shape, not the fields you happen to need; fixtures that skip the envelope hide production bugs.
- **Chat snapshots must preserve per-message reference identity.** Transcript rows are memoized, so a snapshot that rebuilds every message object re-renders the whole transcript. `reconcileChatSession` (in `app/state`) reuses unchanged message objects across snapshots, and `projectVisibleMessage` (in `features/chats/ChatTranscript.tsx`) caches its projection in a `WeakMap` keyed by message identity — keep both in the path. Replacing the messages array wholesale, or remapping it through a fresh `.map` each render, silently defeats the memoization and the transcript goes back to re-rendering on every token.
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

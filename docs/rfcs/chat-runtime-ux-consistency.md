# Chat Runtime UX Consistency

Status: working plan

Hecate Chat and External Agent chat should feel like one chat surface, not two
adjacent products. Their runtime boundaries still matter: Hecate Chat uses
Hecate-owned provider calls, task runs, sandboxed tools, approvals, artifacts,
and OTel. External Agent chat runs Codex, Claude Code, Cursor Agent, or another
ACP adapter as a trusted subprocess in the selected workspace. The UI should
make those differences visible only where they change operator decisions.

## Goals

- Make the chat shell consistent across Hecate and External Agent chats.
- Use one settings panel with common sections and runtime-specific sections.
- Render activity with one shared timeline model, regardless of whether the
  source is a task run or an ACP adapter.
- Make approvals feel native in Chats for both runtimes.
- Use one repair/onboarding pattern for "cannot send yet" states.
- Back the final model with focused UI tests, e2e coverage, and docs.

## Chat Shell Consistency

- New chat creation should behave consistently. When enough required setup is
  available, both Hecate and External Agent new-chat actions create a persisted
  session immediately and show it in the sidebar. If required setup is missing,
  the shell opens a draft/repair state instead of creating an invalid session.
- Sidebar rows should share the same visual grammar: runtime icon, title,
  message count, optional runtime marker, and status.
- Empty states should use the same layout and tone across runtimes, with repair
  actions embedded only when the user cannot send yet.
- Header copy should describe the active runtime in human language, not raw
  IDs. Debug IDs remain available but secondary.
- Workspace-required states should look the same for Hecate tools and External
  Agents.

## Shared Runtime Settings Panel

One panel should serve both runtimes.

Common sections:

- Session context: runtime, workspace, status, message count, task/run/native
  IDs when relevant.
- Usage: Hecate-controlled provider usage and adapter-reported usage should both
  be visible when available. Labels must say whether values are measured by
  Hecate or reported by the adapter.

Hecate-specific sections:

- Tools on/off.
- RTK compact command output.
- System prompt / agent instructions.
- Model/provider snapshot for task-backed turns.

External-agent-specific sections:

- ACP adapter config options.
- External-agent RTK setup instructions when supported.
- System prompt / instructions if the adapter exposes a real config option or
  ACP surface for it. Do not fake a system prompt when the adapter cannot apply
  it.
- Adapter-reported context and cost, clearly marked as not enforced by Hecate.

## Unified Activity Timeline

Map task events and ACP adapter updates into one view model before rendering.
Rows should be top-to-bottom and grouped by operator meaning:

- model/assistant turn
- tool started/completed/failed
- approval requested/resolved
- files changed/artifact
- cancellation/failure
- final answer/output

Low-level rows and raw payloads belong under "Details". Tool rows should prefer
human-readable command/path summaries over opaque call IDs. When output exists,
the row should expose a collapsed preview/link instead of only saying
"1 output".

## Approval UX Parity

- Hecate task approvals and ACP approvals should use the same banner/card tone.
- Approve/reject should optimistically remove the pending card.
- Resolved approvals should not remain clickable.
- "Open task" appears only for Hecate task-backed approvals.
- External approvals stay actionable in the chat without pretending they have a
  task.

## Repair And Onboarding Parity

Use one repair pattern for all blocked send states:

- No provider/model: go to Connections or add detected local providers.
- Missing workspace: choose workspace.
- External adapter not installed/authenticated: show adapter setup.
- Model cannot use tools: turn tools off or update model capability in
  Connections.
- Provider down/no route: show the readiness reason and the next repair action.

## Test And Docs Plan

- Unit tests for new-session creation, settings panel sections per runtime,
  usage visibility, and repair-state copy.
- UI tests for approval cards and timeline row mapping.
- E2E tests for Hecate tools on/off/on, External Agent onboarding, workspace
  missing states, and refresh/reconnect during active runs.
- Docs update for the final model: one chat shell, runtime-specific execution,
  shared settings, and where to debug task vs adapter internals.


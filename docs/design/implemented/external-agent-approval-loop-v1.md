# External-Adapter Approval Loop — Implemented Design Record

> **Status:** implemented record for alpha. Wire shape, persistence, SSE, UI,
> grants management, telemetry, and migration docs have landed.
> **Current source of truth:** [Runtime API](../../runtime/runtime-api.md),
> [External Agent integrations](../accepted/external-agent-integrations.md), and
> [Chat sessions](../../runtime/chat-sessions.md).
> **Next action:** keep as design history unless the approval contract changes
> before beta.

This design record defines how Hecate handles the `RequestPermission` reverse-RPC that
external ACP adapters (Codex, Claude Code, Cursor Agent, Grok Build, future ACP
CLIs) emit when they want to do something the operator should sign off on —
write a file, run a shell command, hit the network, take a destructive action.
Earlier alpha builds auto-selected the first allow option in the adapter's
option list. The current alpha records approvals, defaults to prompt mode,
streams pending and resolved events to Chats, persists durable grants, and makes
explicit `auto` mode visible as a danger state.

The goal of v1 is **operator control without forcing the operator to click
through every adapter call**. Mid-1990s permission dialogs are correct but
unusable. The shape that works is "ask once, remember the operator's choice for
a scoped duration, surface what's been granted."

Implementation gates:

- Wire shape on `/hecate/v1/chat/sessions/{id}/approvals` is finalized for
  alpha. _(Status: implemented)_
- Persisted decision scopes (session / adapter+tool / workspace+tool) are
  agreed and have memory + sqlite schemas. _(Status: implemented)_
- ACP permission options' free-form `id` and `name` fields are mapped to
  Hecate's stable decision shape without losing adapter intent. _(Status: implemented)_
- A timeout-driven behavior is decided so an unattended Hecate
  doesn't deadlock indefinitely on a forgotten dialog. _(Status: implemented:
  prompt-mode timeout returns ACP `Cancelled` and records `path=timeout`.)_
- At least one adapter path is covered end-to-end. _(Status: partially
  implemented: binary e2e covers startup reconcile and durable grant
  persistence; real-adapter allow/deny/timeout smoke remains pre-stable work.)_

## Why the auto-approve stub is a real risk

The current `acpChatClient.RequestPermission` (`internal/agentadapters/acp_session.go`):

```go
func (c *acpChatClient) RequestPermission(_ context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
    for _, option := range params.Options {
        if option.Kind == acp.PermissionOptionKindAllowOnce || option.Kind == acp.PermissionOptionKindAllowAlways {
            return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: ...}}, nil
        }
    }
    if len(params.Options) > 0 {
        return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: ...}}, nil
    }
    return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: ...}}, nil
}
```

For every adapter request, Hecate picks the first allow option in the list —
including `allow_always`, which the operator never sees, never authored, and
can't audit. Codex / Claude Code / Cursor / Grok Build can:

- write or replace any file in the configured workspace,
- shell out to anything on `PATH`,
- fetch arbitrary URLs,
- propose `rm -rf` and have it auto-confirmed.

The Hecate UI advertises an "operator-controlled" tool. This design record is what makes
that claim true.

## Design decisions

### One approval surface, two contexts

Hecate already has a `task_approval` model for the native runtime (see
[`runtime-api.md`](../../runtime/runtime-api.md)). External adapters don't reuse it
verbatim — the wire payload is different, the lifecycle is different, and the
audience (one operator chat) is narrower than tasks. v1 ships a parallel
**`chat_approval`** entity with the same shape so frontends can render
them with one chrome:

```
chat_approval
  id              ULID
  session_id      ULID  (FK → chat_sessions)
  message_id      ULID  (the prompt turn that triggered it)
  adapter_id      string ("codex" | "claude_code" | "cursor_agent" | …)
  status          string ("pending" | "approved" | "denied" | "timed_out" | "cancelled")
  acp_payload     json   (the original RequestPermissionRequest, verbatim)
  acp_options     json   ([{option_id, kind, name}, …] from the adapter)
  selected_option string (filled when status=approved/denied; null otherwise)
  scope           string ("once" | "session" | "workspace_tool" | "adapter_tool")
  decision_note   string (optional operator note)
  created_at      __design record3339__ nano
  resolved_at     __design record3339__ nano (null while pending)
  expires_at      __design record3339__ nano (when the timeout fires)
  request_id      string  (matches assistant message)
  trace_id        string
  span_id         string
```

A future "everything is a Task" convergence (see the Agent Chat → Tasks
migration notes in [`external-agent-integrations.md`](../accepted/external-agent-integrations.md))
collapses this back into `task_approval` with a discriminator. v1 doesn't try
to land that yet.

### Scope ladder

When the operator resolves an approval, they pick a **scope** alongside the
decision. v1 supports four scopes, in increasing breadth:

| Scope            | Re-prompts when…                                                                                     | ACP option this maps to                                      |
| ---------------- | ---------------------------------------------------------------------------------------------------- | ------------------------------------------------------------ |
| `once`           | every subsequent matching request                                                                    | explicitly selected `allow_once`-style option                |
| `session`        | re-asked next session, but auto-applied within this one                                              | `allow_always` (when adapter scopes "always" to its session) |
| `workspace_tool` | re-asked for the same `(adapter, tool_name)` in a different workspace                                | Hecate-side approval grant                                   |
| `adapter_tool`   | re-asked for a different tool, but auto-applied for this `(adapter, tool_name)` pair across sessions | Hecate-side approval grant                                   |

Decisions broader than `once` persist in a new `chat_approval_grants`
table:

```
chat_approval_grants
  id              ULID
  scope           string ("session" | "workspace_tool" | "adapter_tool")
  adapter_id      string
  tool_kind       string  (extracted from acp_payload — see "Tool kind extraction")
  workspace       string  (canonical workspace path; null unless scope=workspace_tool)
  session_id      ULID    (only for scope=session; null otherwise)
  decision        string  ("approve" | "deny")
  granted_by      string  (operator user-agent fingerprint or "operator")
  granted_at      __design record3339__ nano
  expires_at      __design record3339__ nano (null = no expiry; v1 default = 7d for session, 90d for workspace_tool, 30d for adapter_tool — configurable)
```

When a new `RequestPermission` arrives, the session manager checks the grants
table from most-specific to broadest (session → workspace_tool → adapter_tool).
A matching grant short-circuits the operator prompt; the resolution still gets
recorded as an `chat_approval` row with `status=approved/denied` and
`scope` of the matching grant, so the audit trail stays complete.

### Tool-kind extraction

Hecate records a normalized `tool_kind` for grant matching and telemetry. The
adapter's raw tool name stays on the approval row for display/audit, but grants
use the stable kind so "allow this adapter's MCP tools" does not depend on a
vendor-specific tool label.

Extraction order:

1. Use ACP `tool_call.kind` when it maps to Hecate's closed taxonomy.
2. If the typed kind is missing or `other`, infer from the adapter title.
3. Fall back to `other`.

Current mapping:

```
ACP kind / title hint         → tool_kind
────────────────────────────────────────
edit / write / patch / apply  → file_write
read / open / view            → file_read
execute / run / shell / bash  → shell_exec
fetch / http / request        → network
move / rename                 → file_move
delete / remove               → file_delete
search / grep / find          → search
think                         → think
mcp                           → mcp
*                             → other
```

The title heuristic is intentionally lenient and only runs after the typed ACP
kind. MCP titles such as `docs/search` still record `tool_kind=mcp` when the
adapter supplies `kind=mcp`; without that typed signal, a title containing the
word `MCP` also maps to `mcp`.

### Default behavior

Operator prompt is the default. A config var remains for explicit batch /
smoke-test modes:

```
HECATE_AGENT_ADAPTER_APPROVAL_MODE=auto|prompt|deny
                                       │     │     └─ auto-deny everything (smoke tests, audit mode)
                                       │     └─ ask the operator (default)
                                       └─ auto-approve everything; danger mode, explicit only
```

Mode contract:

- `auto` is available only as an explicit danger mode for batch / CI usage and
  local smoke tests.
- `deny` is useful for audit mode and unattended runs where adapter tool use
  must not proceed.
- Startup and the UI should clearly label `auto` as unsafe when enabled.

### Timeout policy

Adapters block on `RequestPermission` indefinitely; a tab-closed operator
shouldn't wedge a session forever. Each pending approval gets an
`expires_at` of `now + HECATE_AGENT_ADAPTER_APPROVAL_TIMEOUT` (default
`5m`). When the deadline passes, the approval transitions to `timed_out` and
Hecate replies to the adapter with an ACP `Cancelled` outcome.

The 5-minute default matches operator-attention reality — long enough for a
context switch, short enough that an abandoned session doesn't hold an adapter
process forever. Configurable per session via a future
`POST /hecate/v1/chat/sessions/{id}` field; v1 keeps it global.

When `mode=prompt` and timeout fires, the resulting cancellation is the
operator's signal: the chat shows "Approval timed out — adapter cancelled
the action." The adapter is free to retry the action on the next prompt.

## API surface

All endpoints are served by the gateway and covered by Hecate's existing access
model: loopback by default and same-origin checks for browser requests. If the
operator binds the gateway elsewhere, they must provide the network access
controls around it.

### Pending list

```
GET /hecate/v1/chat/sessions/{id}/approvals
GET /hecate/v1/chat/sessions/{id}/approvals?status=pending
```

Returns approvals for the session. Filterable by `status`. Default sort:
oldest pending first.

```json
{
  "object": "list",
  "data": [
    {
      "id": "appr_01JX...",
      "session_id": "chat_01JX...",
      "message_id": "msg_01JX...",
      "adapter_id": "codex",
      "status": "pending",
      "tool_kind": "file_write",
      "acp_payload": {
        /* verbatim RequestPermissionRequest */
      },
      "acp_options": [
        { "option_id": "allow_once", "kind": "allow_once", "name": "Allow once" },
        {
          "option_id": "allow_always_for_session",
          "kind": "allow_always",
          "name": "Allow for this session"
        },
        { "option_id": "deny_once", "kind": "reject_once", "name": "Deny" }
      ],
      "scope_choices": ["once", "session", "workspace_tool", "adapter_tool"],
      "created_at": "2026-05-04T10:23:45.123Z",
      "expires_at": "2026-05-04T10:28:45.123Z",
      "request_id": "req_01JX...",
      "trace_id": "..."
    }
  ]
}
```

### Single approval

```
GET /hecate/v1/chat/sessions/{id}/approvals/{approval_id}
```

### Resolve

```
POST /hecate/v1/chat/sessions/{id}/approvals/{approval_id}/resolve
{
  "decision": "approve",          // "approve" | "deny"
  "scope":    "session",          // "once" | "session" | "workspace_tool" | "adapter_tool"
  "selected_option": "allow_always_for_session",  // optional; the adapter's option_id
  "note":     "checked the diff manually"         // optional
}
```

Returns the resolved approval (`status=approved | denied`, `resolved_at` set).

If `selected_option` is omitted, Hecate resolves only when the adapter option
list contains exactly one normalized option for the chosen decision. Ambiguous
option lists return a `409 conflict` with the available options so the frontend
can ask the operator to choose explicitly. If no matching option exists, Hecate
falls back to the ACP `Cancelled` outcome.

### Cancel (operator-initiated)

```
POST /hecate/v1/chat/sessions/{id}/approvals/{approval_id}/cancel
```

Operator-driven cancellation that resolves to ACP `Cancelled`. Different from
`status=denied`, which selects a deny option. Useful when the operator wants
the adapter to "back off and ask again later."

### List grants

```
GET /hecate/v1/chat/grants?adapter_id=codex&scope=adapter_tool
DELETE /hecate/v1/chat/grants/{grant_id}
```

So the operator can see and revoke "always allow" decisions they made earlier.
Listing on `chat_approval_grants`; deletion is hard (no soft-delete —
the existing approvals already record the audit trail).

## Stream integration

Pending approvals surface on the existing chat SSE stream:

```
GET /hecate/v1/chat/sessions/{id}/stream
```

emits a new event type:

```
event: approval.requested
data: { "approval_id": "appr_01JX...", "tool_kind": "file_write", "summary": "Edit src/foo.ts (3 changes)" }
```

When the approval is resolved (by operator action, by a matching grant, or by
timeout), the stream emits:

```
event: approval.resolved
data: { "approval_id": "appr_01JX...", "decision": "approve", "scope": "once", "by": "operator" }
```

This is enough for the UI to show a banner, modal, or sticky toast without
polling.

## UI surface (sketch — not part of the wire contract)

For one frontend concretely:

- **Pending banner** at the top of the chat pane: "Codex wants to edit
  `src/foo.ts`. [Review]"
- **Modal** on click, showing:
  - Adapter name + workspace
  - The ACP `tool_call` body verbatim (with diff preview when available)
  - The adapter's option list (rendered as the operator's primary choices)
  - A scope selector ("just this time / this session / always for Codex's
    file_write / always for file_write in this workspace")
  - Optional note field
- **Approve / Deny / Cancel** buttons; the chosen ACP option_id is the
  primary button text ("Allow once" or whatever the adapter named it)
- **Grants management** in Connections: list of every
  `chat_approval_grants` row with revoke buttons

Frontends are free to render this differently. The wire contract is the
contract.

## Telemetry

New OTel instruments under `hecate.agent_adapter.approval.*`:

| Instrument                                    | Type            | Labels                                                                | Meaning                                                                                                                                                      |
| --------------------------------------------- | --------------- | --------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `hecate.agent_adapter.approval.requested`     | counter         | `adapter`, `tool_kind`, `mode`                                        | Approval requests received from adapters.                                                                                                                    |
| `hecate.agent_adapter.approval.resolved`      | counter         | `adapter`, `tool_kind`, `mode`, `decision`, `scope`, `path`, `status` | How approvals get resolved — by operator, by a pre-existing grant, by the configured default mode (`auto` / `deny`), by timeout, or by request cancellation. |
| `hecate.agent_adapter.approval.timed_out`     | counter         | `adapter`, `tool_kind`, `mode`                                        | Approvals that hit the prompt-mode timeout. Dedicated counter so dashboards can alert without joining `resolved` on `path=timeout`.                          |
| `hecate.agent_adapter.approval.duration`      | histogram       | same labels as `resolved`                                             | Time from request to resolution.                                                                                                                             |
| `hecate.agent_adapter.approval.grants_active` | up-down counter | none                                                                  | Current count of active durable grants. Seeded from the live store at startup, incremented on grant create, decremented on grant delete.                     |

Spans:

- `agent_adapter.approval.request` — wraps the RequestPermission handler;
  carries adapter id, session id, tool kind, mode, and the final path once
  known.
- `agent_adapter.approval.resolve` — wraps the resolve endpoint; carries
  approval id, decision, scope, and loaded adapter/session/tool context.

Existing chat-message log lines get `approval_id` when an approval gated
the turn, so log → trace → approval correlation is one-hop.

## Persistence

New tables in the SQLite schema (`chat_approvals`,
`chat_approval_grants`). Memory-backend equivalent stores them in
process-local maps and discards on restart — same as the existing
chat memory store. The retention worker gets a new
`chat_approvals` subsystem with default `MAX_AGE=30d`,
`MAX_COUNT=10000` (mirrors `task_approval` retention).

Resolved grants survive longer than approvals: a grant is the operator's
authored intent, not just a historical record. v1 retains grants indefinitely
unless `expires_at` is set or the operator deletes them. The retention worker
only removes expired grants.

## Open questions

These need resolution before this draft can become v1.0 stable.

1. **Scope of `session` for resumed chats.** When a chat resumes via ACP
   `session/load`, does a session-scoped grant from the previous
   incarnation carry over? Probably yes (the operator's mental model is
   "this conversation," not "this process"), but adapters might disagree —
   ACP `allow_always` semantics are spec'd per-native-session, not
   per-Hecate-chat.
   - Status: open

2. **Tool-kind extraction reliability.** `tool_call.kind` is now the primary
   signal and title matching is fallback-only. The Go adapters preserve MCP
   permission requests as `kind=mcp`, and Hecate records them as `tool_kind=mcp`
   for grant matching.
   - Status: resolved for current Codex / Claude Code adapters; keep provider
     fixtures for new adapters.

3. **What happens to in-flight grants when an adapter is removed from
   `BuiltIns`?** Probably orphan and surface in Connections as "stale grants
   for unknown adapter `legacy_codex`." Confirm and document.
   - Status: open

4. **Selector UX for `selected_option`.** Some adapters emit a custom
   "Allow with edits" option that opens a free-text edit field; others
   emit only allow/deny. v1's `POST /resolve` accepts a fixed
   `selected_option`; we don't have a wire shape for "operator tweaked
   the proposed file content before approving." Out of scope or required?
   - Status: open

5. **`mode=prompt` vs unattended runs.** A scheduled or background Hecate
   in `prompt` mode waits until `HECATE_AGENT_ADAPTER_APPROVAL_TIMEOUT`,
   then returns ACP `Cancelled` with `path=timeout`. Should repeated
   timeouts reduce to `mode=deny` for that session to avoid repeated waits?
   Per-session opt-in?
   - Status: partially resolved; timeout is implemented, adaptive unattended
     policy remains open.

6. **Grant export / import.** An operator with grant lists on machine A
   wants the same grants on machine B. The simplest thing: operator copies
   `~/.config/hecate/chat-grants.json`. v1 doesn't ship export
   tooling; verify that's OK with at least one operator.
   - Status: open

7. **Cross-session grant revocation race.** Operator revokes a grant while
   an adapter is mid-`RequestPermission` that would have matched. Does
   the in-flight call use the old grant or the new state? Probably the
   new state (consistency over latency), but document.
   - Status: open

## Implementation status

1. **Land the wire shape, persistence, and prompt-mode default** behind
   `HECATE_AGENT_ADAPTER_APPROVAL_MODE`. The default is `prompt`; explicit
   `auto` and `deny` modes still record approvals with `path=default_mode`.
   _(Done for alpha.)_

2. **Wire the SSE `approval.requested` / `approval.resolved` events**
   before enabling the UI flow. Frontends start consuming the contract.
   _(Done for alpha.)_

3. **Land the operator UI** — pending banner + modal + grants management.
   Operators get the new prompt flow by default.
   _(Done for alpha.)_

4. **Document explicit `auto` mode.** `mode=auto` is opt-in and labeled unsafe.
   _(Done for alpha.)_

5. **Stable.** Telemetry confirms operators are using the surface;
   timeout rates and deny rates are healthy. _(Still open.)_

The original implementation estimate was 2.5–3 weeks. The alpha slice is now
implemented; stable readiness depends on real-adapter soak, timeout / deny-rate
telemetry, and resolving the remaining open questions above.

## What this unlocks

When this lands:

- The "operator-controlled" claim in the README is true.
- Adapters can ship riskier capabilities (network, broader file scopes)
  because the operator gates them per-tool.
- A future audit/compliance story has the data it needs: every adapter
  action that asked for permission has an approval row with timestamps,
  decision, scope, and trace IDs.
- The Agent Chat → Tasks convergence has a concrete adapter for how to
  unify the two approval surfaces (drop `chat_approvals` into
  `task_approvals` with a discriminator; the rest of the model already
  matches).
- An "auto-approve" mode for batch / CI usage stays explicit and gated
  by an env var, never a silent default.

## Next steps

1. Soak the prompt-mode flow with real adapters and watch
   `hecate.agent_adapter.approval.timed_out`, deny rate, and
   `grants_active`.
2. Add real-adapter smoke coverage for allow / deny / always-scope / timeout
   once the adapters expose stable enough fixtures.
3. Resolve the remaining open questions: tool-kind reliability, stale grants
   for removed adapters, custom selected-option UX, unattended repeated
   timeouts, grant export/import, and revocation races.
4. Decide whether Agent Chat approvals remain a parallel store or converge into
   the native task approval model before declaring the adapter subsystem stable.

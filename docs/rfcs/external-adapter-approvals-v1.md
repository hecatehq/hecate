# External-Adapter Approval Loop — v1 Candidate (RFC)

> **Status:** draft / RFC. Not implemented. Not stable.
> **Depends on:** [`external-agent-adapters.md`](external-agent-adapters.md) — the broader Agent Chat surface this approval loop plugs into.
> **Related:** [Runtime API](../runtime-api.md) — task-runtime approvals, whose persistence shape this RFC reuses.
> **Owner:** see [`AGENTS.md`](../../AGENTS.md).

This RFC defines how Hecate handles the `RequestPermission` reverse-RPC that
external ACP adapters (Codex, Claude Code, Cursor Agent, future ACP CLIs) emit
when they want to do something the operator should sign off on — write a file,
run a shell command, hit the network, take a destructive action. Today Hecate
auto-selects the first allow option in the adapter's option list every time;
this is the largest user-visible safety gap for the External Agent Adapters
subsystem and blocks calling that subsystem stable.

The goal of v1 is **operator control without forcing the operator to click
through every adapter call**. Mid-1990s permission dialogs are correct but
unusable. The shape that works is "ask once, remember the operator's choice for
a scoped duration, surface what's been granted."

Before this document can be treated as candidate-stable:

- Wire shape on `/v1/agent-chat/sessions/{id}/approvals` is finalized. _(Status: open)_
- Persisted decision scopes (session / adapter+tool / workspace+tool) are
  agreed and have a sqlite schema. _(Status: open)_
- ACP permission options' free-form `id` and `name` fields are mapped to
  Hecate's stable decision shape without losing adapter intent. _(Status: open)_
- A timeout-driven default behavior is decided so an unattended Hecate
  doesn't deadlock indefinitely on a forgotten dialog. _(Status: open)_
- At least one adapter (Codex *or* Claude Code) is wired end-to-end with a
  smoke test covering allow / deny / always-allow / timeout. _(Status: open)_

Flip each gate from `open` → `resolved (#NNN)` as work lands so this list
stays the source of truth.

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
can't audit. Codex / Claude Code / Cursor can:

- write or replace any file in the configured workspace,
- shell out to anything on `PATH`,
- fetch arbitrary URLs,
- propose `rm -rf` and have it auto-confirmed.

The Hecate UI advertises an "operator-controlled" tool. This RFC is what makes
that claim true.

## Design decisions

### One approval surface, two contexts

Hecate already has a `task_approval` model for the native runtime (see
[`runtime-api.md`](../runtime-api.md)). External adapters don't reuse it
verbatim — the wire payload is different, the lifecycle is different, and the
audience (one operator chat) is narrower than tasks. v1 ships a parallel
**`agent_chat_approval`** entity with the same shape so frontends can render
them with one chrome:

```
agent_chat_approval
  id              ULID
  session_id      ULID  (FK → agent_chat_sessions)
  message_id      ULID  (the prompt turn that triggered it)
  adapter_id      string ("codex" | "claude_code" | "cursor_agent" | …)
  status          string ("pending" | "approved" | "denied" | "timed_out" | "cancelled")
  acp_payload     jsonb  (the original RequestPermissionRequest, verbatim)
  acp_options     jsonb  ([{option_id, kind, name}, …] from the adapter)
  selected_option string (filled when status=approved/denied; null otherwise)
  scope           string ("once" | "session" | "adapter_tool" | "workspace_tool")
  decision_note   string (optional operator note)
  created_at      RFC3339 nano
  resolved_at     RFC3339 nano (null while pending)
  expires_at      RFC3339 nano (when the timeout fires)
  request_id      string  (matches assistant message)
  trace_id        string
  span_id         string
```

A future "everything is a Task" convergence (see the Agent Chat → Tasks
migration notes in [`external-agent-adapters.md`](external-agent-adapters.md))
collapses this back into `task_approval` with a discriminator. v1 doesn't try
to land that yet.

### Scope ladder

When the operator resolves an approval, they pick a **scope** alongside the
decision. v1 supports four scopes, in increasing breadth:

| Scope | Re-prompts when… | ACP option this maps to |
|---|---|---|
| `once` | every subsequent matching request | `allow_once` (or first allow option) |
| `session` | re-asked next session, but auto-applied within this one | `allow_always` (when adapter scopes "always" to its session) |
| `adapter_tool` | re-asked for a different tool, but auto-applied for this `(adapter, tool_name)` pair across sessions | (no native ACP equivalent — Hecate-side cache) |
| `workspace_tool` | re-asked for the same `(adapter, tool_name)` in a different workspace | (no native ACP equivalent — Hecate-side cache) |

Decisions broader than `once` persist in a new `agent_chat_approval_grants`
table:

```
agent_chat_approval_grants
  id              ULID
  scope           string ("session" | "adapter_tool" | "workspace_tool")
  adapter_id      string
  tool_kind       string  (extracted from acp_payload — see "Tool kind extraction")
  workspace       string  (canonical workspace path; null for adapter_tool)
  session_id      ULID    (only for scope=session; null otherwise)
  decision        string  ("approve" | "deny")
  granted_by      string  (operator user-agent fingerprint or "operator")
  granted_at      RFC3339 nano
  expires_at      RFC3339 nano (null = no expiry; v1 default = 7d for session, 30d for adapter_tool, 90d for workspace_tool — configurable)
```

When a new `RequestPermission` arrives, the session manager checks the grants
table in scope-narrowest-first order (session → adapter_tool → workspace_tool).
A matching grant short-circuits the operator prompt; the resolution still gets
recorded as an `agent_chat_approval` row with `status=approved/denied` and
`scope` of the matching grant, so the audit trail stays complete.

### Tool-kind extraction

ACP's `RequestPermissionRequest` doesn't carry a clean tool name. v1 derives
`tool_kind` from the embedded `tool_call.tool_name` field (always present in
the ACP shape we've observed across Codex / Claude Code / Cursor) and falls
back to the option `kind` when the tool field is missing. The mapping:

```
tool_call.tool_name → tool_kind
─────────────────────────────────
write_file, edit_file, str_replace, multi_edit  → file_write
read_file, read_text_file                       → file_read
shell, bash, run_command, execute               → shell_exec
fetch, http_get, web_search                     → network
*                                               → other
```

Adapters can emit tool names not in this list; the fallback is the literal
adapter-supplied name, recorded as `tool_kind=<adapter>:<raw>`. Frontends
render it as "unknown tool: …" so the operator sees what the adapter is
actually asking for, not a sanitized lie.

### Default behavior + migration knob

Today's behavior is auto-approve. Switching to operator-prompt is a behavior
change for existing chats. v1 ships behind a config var:

```
GATEWAY_AGENT_ADAPTER_APPROVAL_MODE=auto|prompt|deny
                                       │     │     └─ auto-deny everything (smoke tests, audit mode)
                                       │     └─ ask the operator (default for v1.0+)
                                       └─ today's behavior, default for v1.0-rc
```

Migration plan:

- v1.0-rc: default `auto`, `prompt` is opt-in. Operator UI surfaces the
  approvals tab even in `auto` mode (read-only) so users see what's being
  auto-approved and can audit.
- v1.0: default `prompt`. Operators who want the old behavior set
  `GATEWAY_AGENT_ADAPTER_APPROVAL_MODE=auto` explicitly.
- v1.1+: `auto` becomes a "danger" mode with a startup warning banner.

### Timeout policy

Adapters block on `RequestPermission` indefinitely; a tab-closed operator
shouldn't wedge a session forever. Each pending approval gets an
`expires_at` of `now + GATEWAY_AGENT_ADAPTER_APPROVAL_TIMEOUT` (default
`5m`). When the deadline passes, the approval transitions to `timed_out` and
Hecate replies to the adapter with an ACP `Cancelled` outcome.

The 5-minute default matches operator-attention reality — long enough for a
context switch, short enough that an abandoned session doesn't hold an adapter
process forever. Configurable per session via a future
`POST /v1/agent-chat/sessions/{id}` field; v1 keeps it global.

When `mode=prompt` and timeout fires, the resulting cancellation is the
operator's signal: the chat shows "Approval timed out — adapter cancelled
the action." The adapter is free to retry the action on the next prompt.

## API surface

All endpoints are loopback-only and same-origin enforced (Hecate's existing
threat model).

### Pending list

```
GET /v1/agent-chat/sessions/{id}/approvals
GET /v1/agent-chat/sessions/{id}/approvals?status=pending
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
      "acp_payload": { /* verbatim RequestPermissionRequest */ },
      "acp_options": [
        { "option_id": "allow_once", "kind": "allow_once", "name": "Allow once" },
        { "option_id": "allow_always_for_session", "kind": "allow_always", "name": "Allow for this session" },
        { "option_id": "deny_once", "kind": "reject_once", "name": "Deny" }
      ],
      "scope_choices": ["once", "session", "adapter_tool", "workspace_tool"],
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
GET /v1/agent-chat/sessions/{id}/approvals/{approval_id}
```

### Resolve

```
POST /v1/agent-chat/sessions/{id}/approvals/{approval_id}/resolve
{
  "decision": "approve",          // "approve" | "deny"
  "scope":    "session",          // "once" | "session" | "adapter_tool" | "workspace_tool"
  "selected_option": "allow_always_for_session",  // optional; the adapter's option_id
  "note":     "checked the diff manually"         // optional
}
```

Returns the resolved approval (`status=approved | denied`, `resolved_at` set).

If `selected_option` is omitted, Hecate picks the canonical option for the
decision: the first `allow_*` option for `approve`, the first `reject_*` for
`deny`. If neither shape exists, Hecate falls back to the ACP `Cancelled`
outcome.

### Cancel (operator-initiated)

```
POST /v1/agent-chat/sessions/{id}/approvals/{approval_id}/cancel
```

Operator-driven cancellation that resolves to ACP `Cancelled`. Different from
`status=denied`, which selects a deny option. Useful when the operator wants
the adapter to "back off and ask again later."

### List grants

```
GET /v1/agent-chat/grants?adapter_id=codex&scope=adapter_tool
DELETE /v1/agent-chat/grants/{grant_id}
```

So the operator can see and revoke "always allow" decisions they made earlier.
Listing on `agent_chat_approval_grants`; deletion is hard (no soft-delete —
the existing approvals already record the audit trail).

## Stream integration

Pending approvals surface on the existing chat SSE stream:

```
GET /v1/agent-chat/sessions/{id}/stream
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
- **Grants management** in Settings → External Agents: list of every
  `agent_chat_approval_grants` row with revoke buttons

Frontends are free to render this differently. The wire contract is the
contract.

## Telemetry

New OTel instruments under `hecate.agent_adapter.approval.*`:

| Instrument | Type | Labels | Meaning |
|---|---|---|---|
| `hecate.agent_adapter.approval.requested_total` | counter | `adapter`, `tool_kind` | Approval requests received from adapters. |
| `hecate.agent_adapter.approval.resolved_total` | counter | `adapter`, `tool_kind`, `decision`, `scope`, `path` (`operator` / `grant` / `default_mode`) | How approvals get resolved — by operator, by a pre-existing grant, or by the configured default mode. |
| `hecate.agent_adapter.approval.timed_out_total` | counter | `adapter`, `tool_kind` | Approvals that hit the timeout. |
| `hecate.agent_adapter.approval.duration_ms` | histogram | `adapter`, `tool_kind`, `path` | Time from request to resolution. Buckets: `[100, 500, 1k, 5k, 30k, 60k, 300k]`. |
| `hecate.agent_adapter.approval.grants_active` | gauge | `scope` | Current count of active grants. |

Spans:

- `agent_adapter.approval.request` — wraps the RequestPermission handler;
  carries `adapter.id`, `tool_kind`, `mode`, `path`.
- `agent_adapter.approval.resolve` — wraps the resolve endpoint; carries
  `decision`, `scope`.

Existing chat-message log lines get `approval_id` when an approval gated
the turn, so log → trace → approval correlation is one-hop.

## Persistence

New tables in the SQLite schema (`agent_chat_approvals`,
`agent_chat_approval_grants`). Memory-backend equivalent stores them in
process-local maps and discards on restart — same as the existing
agent-chat memory store. The retention worker gets a new
`agent_chat_approvals` subsystem with default `MAX_AGE=30d`,
`MAX_COUNT=10000` (mirrors `task_approval` retention).

Open question: should resolved grants survive longer than approvals?
Probably yes — a grant is the operator's authored intent, not just a
historical record. v1 retains grants indefinitely unless `expires_at` is
set or the operator deletes them; the retention worker doesn't touch them
by default.

## Open questions

These need resolution before this draft can become v1.0 stable.

1. **Scope of `session` for resumed chats.** When a chat resumes via ACP
   `session/load`, does a session-scoped grant from the previous
   incarnation carry over? Probably yes (the operator's mental model is
   "this conversation," not "this process"), but adapters might disagree —
   ACP `allow_always` semantics are spec'd per-native-session, not
   per-Hecate-chat.
   - Status: open

2. **Tool-kind extraction reliability.** `tool_call.tool_name` isn't a
   strict ACP-required field. If an adapter sends a `RequestPermission`
   without it, all grants degrade to `tool_kind=other`, which makes the
   `adapter_tool` scope coarser than operators expect. Should we require
   adapters to emit `tool_name`? Push for an ACP spec change?
   - Status: open

3. **What happens to in-flight grants when an adapter is removed from
   `BuiltIns`?** Probably orphan and surface in Settings as "stale grants
   for unknown adapter `legacy_codex`." Confirm and document.
   - Status: open

4. **Selector UX for `selected_option`.** Some adapters emit a custom
   "Allow with edits" option that opens a free-text edit field; others
   emit only allow/deny. v1's `POST /resolve` accepts a fixed
   `selected_option`; we don't have a wire shape for "operator tweaked
   the proposed file content before approving." Out of scope or required?
   - Status: open

5. **`mode=prompt` vs unattended runs.** A scheduled or background Hecate
   in `prompt` mode would block forever if no operator is around. Should
   `mode=prompt` reduce to `mode=deny` after N consecutive timeouts to
   avoid wedging adapter processes? Per-session opt-in?
   - Status: open

6. **Grant export / import.** An operator with grant lists on machine A
   wants the same grants on machine B. The simplest thing: operator copies
   `~/.config/hecate/agent-chat-grants.json`. v1 doesn't ship export
   tooling; verify that's OK with at least one operator.
   - Status: open

7. **Cross-session grant revocation race.** Operator revokes a grant while
   an adapter is mid-`RequestPermission` that would have matched. Does
   the in-flight call use the old grant or the new state? Probably the
   new state (consistency over latency), but document.
   - Status: open

## Migration plan

1. **Land the wire shape, persistence, and `mode=auto` retention** behind
   `GATEWAY_AGENT_ADAPTER_APPROVAL_MODE` (defaults to current
   auto-approve behavior). No UI yet. Approvals are recorded but
   auto-resolved with `path=default_mode`. Audit-only.

2. **Wire the SSE `approval.requested` / `approval.resolved` events**
   without changing default behavior. Frontends start consuming.

3. **Land the operator UI** — pending banner + modal + grants management.
   Default still `auto`. Operators who set `mode=prompt` get the new
   experience.

4. **Flip default to `mode=prompt`.** Release notes call out the
   behavior change. `mode=auto` becomes opt-in.

5. **Stable.** Telemetry confirms operators are using the surface;
   timeout rates and deny rates are healthy.

Estimated wall-clock: 2.5–3 weeks. The persistence + wire shape is
~3 days; the SSE integration is ~2 days; the UI is ~1 week (modal +
grants management); migration + tests + docs is ~3 days. Default flip is
a follow-up.

## What this unlocks

When this lands:

- The "operator-controlled" claim in the README is true.
- Adapters can ship riskier capabilities (network, broader file scopes)
  because the operator gates them per-tool.
- A future audit/compliance story has the data it needs: every adapter
  action that asked for permission has an approval row with timestamps,
  decision, scope, and trace IDs.
- The Agent Chat → Tasks convergence has a concrete adapter for how to
  unify the two approval surfaces (drop `agent_chat_approvals` into
  `task_approvals` with a discriminator; the rest of the model already
  matches).
- An "auto-approve" mode for batch / CI usage stays explicit and gated
  by an env var, not a silent default.

## Next steps

1. Land this RFC. Solicit feedback on the open questions, especially #2
   (tool-kind extraction) and #5 (unattended-mode behavior).
2. Implement persistence + auto-mode telemetry as the smallest first slice
   (no behavior change for users, full audit shape recorded).
3. SSE event integration; first frontend (web UI) consumes.
4. Operator UI — pending banner + modal + grants management.
5. Default flip to `prompt`. Release notes + migration guidance.

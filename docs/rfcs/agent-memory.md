# Agent Memory

> **Status:** design notes. Not implemented. Captures the proposal
> for a cross-session, operator-authored memory primitive that
> persists relevant context across chats with Hecate Agent and
> across `agent_loop` task runs.
> **Depends on:** two existing system-prompt mechanisms — the
> three-layer composition for `agent_loop` task runs
> (global → workspace `CLAUDE.md`/`AGENTS.md` → per-task) built in
> `internal/api/system_prompt.go` via `buildSystemPromptResolver` and
> consumed through the `orchestrator.SystemPromptResolver` interface,
> and the session-level prompt path for model chats handled by
> `applySessionSystemPrompt` in `internal/api/handler_chat.go`. The
> two surfaces inject memory differently; see "Injection mechanics"
> below.

Operators currently re-establish the same context every chat: their
role, the codebase paths they care about, conventions they want the
assistant to follow, the way they want answers shaped. Even within a
single workspace, every new Hecate Chat or `agent_loop` task run
starts cold. The system prompt is shared across runs, but it is a
single global string — operators can't carve it into reusable
entries scoped to specific situations.

OpenAI and Anthropic ship vendor-locked memory features. Hecate
operators can't depend on them and can't take their memory with them
when they switch providers.

## Goals

In rough priority order:

1. **Operator-authored memory entries** survive across chats and
   across `agent_loop` task runs. An entry written today is in the
   conversation tomorrow without re-typing it.
2. **Scoped activation.** An entry can target every chat, only one
   workspace, only one agent kind (Hecate Agent vs `agent_loop`
   tasks), or some intersection. Not every entry should fire on
   every conversation.
3. **Fully visible.** The operator can see every active memory
   entry in a Settings tab and per-chat. No "the assistant
   remembered something but I can't see what" behavior.
4. **Provider-agnostic.** Same memory entries work across OpenAI,
   Anthropic, Gemini, etc. No vendor lock-in.
5. **Encrypted at rest** when `GATEWAY_CONTROL_PLANE_SECRET_KEY`
   is set, mirroring how persisted provider API keys are protected.

## Non-goals (v1)

- **Auto-extraction from past chats.** The "this looks important,
  I'll remember it" behavior every vendor's memory feature does is
  the worst kind of memory: surprising, opaque, and easy to abuse.
  v1 is operator-authored only. Auto-extraction can be a separate
  RFC after eval scaffolding exists.
- **Semantic recall.** Vector DB territory. v1 is exact-match
  scope rules + plain text, prepended verbatim. Semantic retrieval
  ("memories relevant to *this question*") is a future RFC.
- **External-agent integration.** Codex, Claude Code, and Cursor
  Agent each have their own settings layer outside Hecate's
  control. We can't inject memory into their conversations without
  ACP standardizing a memory primitive, which it hasn't. v1 covers
  Hecate-controlled surfaces only: Hecate Chat (tools-off + Hecate
  Agent) and `agent_loop` task runs.
- **Cross-tenant sharing.** Hecate is single-user; shared memory
  across operators isn't a real use case. If multi-user lands, this
  RFC's scope model maps cleanly onto per-user partitions.
- **Memory editing by the LLM.** The LLM never writes to memory.
  Only the operator does. (Future RFC could add an opt-in
  "remember this" tool that requires explicit operator approval
  per write, but not v1.)

## Constraints

- **Single-user mode.** All memory entries are owned by the local
  operator. No principal model.
- **Local-first.** Entries persist in the same SQLite database as
  the rest of operator state. No cloud sync.
- **Operator-controlled.** Every byte of memory the LLM sees is
  something the operator typed. No background extraction, no
  auto-import from past conversations in v1.
- **Provider-neutral.** Memory entries are plain text. Providers
  receive them as part of the system prompt, not via any vendor's
  memory API.

## Data model

A memory entry is title + body + scope:

```go
// pkg/types/memory.go (new)
type MemoryEntry struct {
    ID        string          // mem_<hex>
    Title     string          // operator-facing label, ~60 chars
    Body      string          // the content prepended to system prompt
    Scope     MemoryScope     // global | workspace | agent_kind | composite
    ScopeKey  string          // e.g. workspace path; "" for global
    AgentKind string          // "" | "hecate_chat" | "agent_loop"
    Enabled   bool            // toggle without delete
    CreatedAt time.Time
    UpdatedAt time.Time
}

type MemoryScope string

const (
    MemoryScopeGlobal       MemoryScope = "global"
    MemoryScopeWorkspace    MemoryScope = "workspace"
    MemoryScopeAgentKind    MemoryScope = "agent_kind"
    MemoryScopeComposite    MemoryScope = "composite" // workspace + agent_kind
)
```

Scope semantics, evaluated when preparing a chat or task run:

| Entry scope | Activates when |
|---|---|
| `global` | Always, every chat / task run |
| `workspace` (`scope_key=/path`) | Active workspace path matches `/path` |
| `agent_kind` (`agent_kind="agent_loop"`) | Current surface matches |
| `composite` | Both `scope_key` and `agent_kind` match |

`Enabled=false` excludes the entry from injection but preserves it
for future re-enable. Cheaper than delete-and-recreate when an
operator wants to A/B with and without a memory.

## Storage

New table in `internal/chatstate/sqlite.go` (or a new
`internal/memory/` package — see open questions):

```sql
CREATE TABLE IF NOT EXISTS memory_entries (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    body        BLOB NOT NULL,        -- AES-GCM ciphertext when settings key set
    scope       TEXT NOT NULL,        -- global|workspace|agent_kind|composite
    scope_key   TEXT NOT NULL DEFAULT '',
    agent_kind  TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memory_scope ON memory_entries (scope, scope_key, agent_kind);
```

Encryption: `body` is encrypted with the same `secrets.AESGCMCipher`
used for stored provider API keys, when `GATEWAY_CONTROL_PLANE_SECRET_KEY`
is configured. When unset, `body` is stored as plain UTF-8 (matching
the existing pattern for provider keys).

## API surface

Five routes on `/hecate/v1/memory`:

```
GET    /hecate/v1/memory                     # list (paginated)
POST   /hecate/v1/memory                     # create
GET    /hecate/v1/memory/{id}                # get one
PATCH  /hecate/v1/memory/{id}                # update title/body/scope/enabled
DELETE /hecate/v1/memory/{id}                # delete
```

Plus one runtime introspection endpoint:

```
GET    /hecate/v1/memory/active?workspace=/path&agent_kind=agent_loop
```

Returns the ordered list of memory entries that *would* be injected
for a given workspace + agent kind. Used by the per-chat indicator
in the UI ("3 memory entries active in this chat") so operators
always know what's in scope.

Response shape mirrors today's `{object, data}` Hecate-native
envelope.

## Injection mechanics

Hecate has two different system-prompt paths today; memory hooks
into both, but the surrounding layers differ. Stable order in both
cases: matching entries sorted by `(created_at, id)` ascending —
deterministic, operator-controllable via timestamps if reordering
matters.

Format inside the prompt is consistent across both paths:

```
# Memory: <title>
<body>
```

Plain markdown headers separate entries. Most providers respect
markdown structure in system prompts; the rare ones that don't
still receive readable context.

### `agent_loop` task runs (three-layer composition)

`internal/api/system_prompt.go`'s `buildSystemPromptResolver`
builds the resolver that `agent_loop` runs through the
`orchestrator.SystemPromptResolver` interface. Memory entries
become a new layer between global and workspace:

```
[GATEWAY_TASK_AGENT_SYSTEM_PROMPT]                  ← global
[matching memory entries, in stable order]         ← new
[workspace CLAUDE.md / AGENTS.md]                  ← workspace
[per-task system prompt from Task.SystemPrompt]    ← per-task
```

Existing layers are unchanged; memory enters as a fourth
narrowest-but-broader-than-workspace tier. Hecate Chat segments
that run tools-on (Hecate Agent) flow through the task runtime
and pick this path automatically.

### Hecate Chat model-chat segments (session-level prompt)

Tools-off model chat (and any other consumer of `/v1/chat/completions`
the gateway proxies for an operator session) goes through
`applySessionSystemPrompt` in `internal/api/handler_chat.go`. That
function prepends the persisted `ChatSessionRecord.SystemPrompt`
as a single `system` message at the head of the request's
`Messages`. There is no workspace layer and no per-task layer here.

Memory injection on this path: the matching entries are concatenated
(same `# Memory: <title>` headers) and prepended *before* the
session system prompt, as a single additional `system` message:

```
system: [matching memory entries, in stable order]   ← new
system: [ChatSessionRecord.SystemPrompt]             ← existing
user/assistant/...                                   ← conversation
```

Two `system` messages back-to-back is well-supported by every
provider Hecate routes to today; the alternative (concatenating
into one `system` message) is also fine but loses the visual
boundary in trace inspectors.

### Anthropic prompt caching

For both paths, when the request goes to Anthropic the memory
block is wrapped in a `cache_control` marker. Memory changes
infrequently and is identical across runs that share the same
scope, so cache hits are high. Pairs with the planned
context-window-management RFC (TODO: link once that file lands;
the cache-marker mechanics live in that companion RFC).

When the request goes elsewhere, no markers — the block is just
text.

## UI surface

### Settings tab — Memory

New tab next to Pricebook / Retention. List of all memory entries
with:

- Title (inline-editable)
- Body (multiline editor; markdown preview)
- Scope picker (global / workspace / agent_kind / composite) with
  appropriate fields revealed
- Enabled toggle
- Last updated timestamp
- Delete (with confirm)
- New-entry form

Tests against existing patterns for Settings-tab CRUD (Pricebook is
the closest analog).

### Per-chat indicator

In Hecate Chat header (and external-agent chat header — see future
work), small badge: **"3 memories active"**. Click expands to a
panel listing the matching entries, each with title + scope. Each
entry has a "View in Settings" link. No edit-from-chat in v1 (keeps
the chat surface focused; Settings is the source of truth).

The indicator queries `GET /hecate/v1/memory/active` with the
current workspace and agent kind so it always reflects what just
went into the system prompt.

### Task Detail

Task runs that consumed memory entries record their IDs as a span
attribute (`hecate.task.memory_entries=mem_a,mem_b,mem_c`) and
expose them as a row in the task run header. Operator can click to
see the entries that influenced this run, even if they've since
been edited or deleted (provenance via the IDs preserved in the
trace).

## End-to-end scope

| Layer | Files | Lines (rough) |
|---|---|---|
| Types | `pkg/types/memory.go`, `internal/api/openai.go` (response types) | ~80 |
| Storage | new `internal/memory/` package (or table in chatstate), memory store + sqlite | ~400 |
| API handlers | `internal/api/handler_memory.go` + routes | ~250 |
| Injection | wiring in handler_chat.go + executor_agent_loop.go's prompt composer | ~150 |
| UI types | `ui/src/types/runtime.ts` | ~30 |
| UI Settings tab | `ui/src/features/settings/MemoryTab.tsx` + tests | ~500 |
| UI per-chat indicator | small component in Hecate Chat header + tests | ~150 |
| Task Detail provenance row | `ui/src/features/runs/TaskDetail.tsx` change | ~60 |
| Tests at every layer | provider, storage, handler, injection, UI | ~600 |
| Docs | new `docs/memory.md`; cross-link from `chat-sessions.md`, `agent-runtime.md`; `known-limitations.md` update | ~150 |

Total: ~2400 lines, ~6 PRs across api/storage/UI.

## Phasing

| PR | Scope | Size |
|---|---|---|
| 1 | Types + storage layer (memory store interface, memory/sqlite, no API or UI yet) | ~480 |
| 2 | API handlers + runtime introspection endpoint | ~280 |
| 3 | System-prompt injection in Hecate Chat + `agent_loop`; Task Detail provenance trace attribute | ~210 |
| 4 | UI types + Settings Memory tab CRUD | ~530 |
| 5 | Per-chat indicator + Task Detail provenance row | ~210 |
| 6 | Docs + `known-limitations.md` update if applicable | ~150 |

PRs 1–3 give API consumers a working memory primitive without UI.
PRs 4–5 expose it to the operator. PR 6 documents.

PR 1 is shippable and useful for headless operators / scripted
workflows even without UI. PR 4 is where most operators see the
feature.

## Open questions

- **New package or table in chatstate?** Memory is logically a
  sibling of chats, not a property of them. Probably its own
  package `internal/memory/` with its own SQLite store, registered
  in the migration registry alongside the other nine packages.
  Defaulting to a separate package for separation of concerns.
- **Scope model: rich enough?** Three predefined scopes
  (global / workspace / agent_kind) plus composite. Free-form tags
  on top would let operators do "all backend chats" or "Python
  projects." Probably v2; v1 covers 80% of the use cases without
  the freeform-string UX problem.
- **Memory size limit?** Without a cap, a verbose operator can
  inflate every system prompt to context-overflow. Initial
  proposal: soft warn at 5,000 chars per entry, hard cap at
  20,000. Operator-overridable via a flag. Cross-references
  context window RFC.
- **Per-message provenance.** Beyond the trace attribute on task
  runs, should each chat message record which memory entries
  influenced it? Useful for "the assistant said something weird,
  what was in scope?" forensics. Adds a JSON column to message
  rows; not free. Probably v1.5.
- **Export / import.** Operators who switch machines want their
  memory to come along. Plain JSON export of the table is trivial
  to add; defer until requested.
- **Memory in external-agent chats.** Codex / Claude Code / Cursor
  have their own settings/memory layers outside Hecate's control.
  We can't reach them without an ACP extension that doesn't exist
  yet. v1 documents this as a known gap; if ACP adds a memory
  primitive, Hecate's memory entries become a candidate sync
  source.
- **Auto-extraction from past chats.** Real product feature with
  eval requirements (precision/recall on what should be
  remembered, hallucination guardrails). Out of scope for v1; would
  be its own RFC after manual memory has shipped and operators
  have a baseline.

## Risks

1. **Memory inflates context silently.** Adding 5 entries × 2,000
   chars = 10,000 chars (~2,500 tokens) before the actual
   conversation starts. Pairs with the context-management RFC: the
   token estimate there must count memory contributions.
2. **Operator forgets a memory entry is enabled** and gets
   surprising answers. Mitigation: per-chat indicator (always-on,
   click to inspect), and Settings tab is the canonical source of
   truth. Disable beats delete.
3. **Memory drift across providers.** Different providers respect
   markdown structure in system prompts differently. Most major
   providers handle headers fine; the rare ones still see plain
   text. Mitigation: keep the format simple (markdown headers +
   body); avoid relying on any specific structure.
4. **Encrypted body breaks operator backup tools.** Operators who
   `cp .data/hecate.db backup.db` get an encrypted body. Without
   the settings key the body is unrecoverable. Mitigation: same
   pattern as provider API keys — operators who set the key are
   responsible for backing it up. Document explicitly.
5. **Memory + agent_loop interaction.** Memory is in the system
   prompt at task-run start; mid-run it can't change. If an
   operator edits a memory entry mid-run, the change applies to
   the next run, not this one. Document this; otherwise expect
   "I changed memory but the task didn't pick it up" reports.

## Acceptance criteria

When this RFC is implemented:

- An operator can create, edit, scope, enable, disable, and delete
  memory entries from a Settings tab.
- Memory entries scoped to "global" appear in every Hecate Chat
  conversation and every `agent_loop` task run.
- Memory entries scoped to a workspace path appear only when the
  active workspace matches.
- Memory entries scoped to an `agent_kind` appear only when the
  current surface matches.
- The per-chat indicator shows the number of active memory entries
  and lets the operator inspect them without leaving the chat.
- Memory bodies are encrypted at rest when
  `GATEWAY_CONTROL_PLANE_SECRET_KEY` is set; left as plain UTF-8
  otherwise.
- Task runs record the IDs of consumed memory entries as a span
  attribute, surfaced in Task Detail.
- An operator's memory works identically against OpenAI, Anthropic,
  and any other configured provider — provider-neutrality verified
  by integration tests across at least two providers.
- The known-limitations doc gains an entry for "memory is
  operator-authored only; no auto-extraction" so the contract is
  explicit.

## Cross-reference

The companion RFC for **LLM context window management** (not yet
written) handles token estimation, soft thresholds, hard caps,
truncation, and Anthropic prompt caching. Memory entries inflate
context, so:

- The token estimator MUST count memory contributions in its
  pre-flight estimate.
- The Anthropic cache-marker piece in the context-management RFC
  SHOULD wrap the memory block when caching is on, since memory
  changes infrequently relative to conversation turns.

These two features are designed to compose cleanly. Either can ship
first; the other gains its hook on top.

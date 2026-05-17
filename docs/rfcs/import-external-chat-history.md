# Import External Chat History

> **Status:** proposed; not implemented.
> **Current source of truth:** [Chat sessions](../chat-sessions.md) and
> [External agent adapters](../external-agent-adapters.md) for today's session
> store and transcript model.
> **Next action:** keep as-is until import work starts.

Operators run Claude Code and Codex CLI outside Hecate and accumulate
weeks of transcripts that already contain the searchable substance:
how they explored a codebase, what fix attempts failed, which tool
calls produced the artifact they're looking for. Both tools persist
JSONL session files on disk (`~/.claude/projects/<slug>/<uuid>.jsonl`,
`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`). Today Hecate has no
way to read them.

The result: an operator who's already adopted Hecate as the
supervision surface for *new* agent work has to flip back to `grep
-r ~/.claude/projects` to look up *old* work. Two surfaces, two
mental models, two sets of paste-into-issue ergonomics.

This RFC scopes a one-shot import (not live mirroring), the schema
mapping, and the smallest UI surface that makes imported transcripts
useful without confusing them with live sessions.

## Goals

In rough priority order:

1. **One-shot import** of Claude Code and Codex JSONL transcripts
   into the agent-chat store. An imported session shows up in the
   Chats list, opens in the same transcript view, and is searchable
   alongside live sessions.
2. **Read-only by construction.** An imported session can never
   accept a new turn, resume, or be the target of a tool call. The
   resume path simply doesn't exist for these records.
3. **Provenance preserved.** Every imported session carries its
   source tool, the original session id, the original transcript
   path, and the import timestamp. Operators can trace a Hecate
   record back to the on-disk file it came from.
4. **Faithful turn boundaries.** A user→assistant exchange in the
   source becomes one user message + one assistant message in
   Hecate, with tool calls captured as `Activity` rows on the
   assistant message. The transcript view doesn't have to be told
   the source is special.
5. **Bulk + selective.** "Import everything from `~/.claude/projects/`"
   for the default case; a single-file path argument for the
   "I want this one specific session" case.
6. **Idempotent.** Re-running the import doesn't duplicate sessions.
   The `(source_tool, native_session_id)` pair is the dedupe key.

## Non-goals (v1)

- **Live mirroring or watch-mode.** v1 is one-shot. A future RFC
  could add an inotify/FSEvents watcher that ingests new sessions
  as they finish, but the cost/value isn't there yet — operators
  who *want* live supervision should run the agent through
  Hecate's external-agent-adapter path, which already does this
  properly.
- **Editing imported transcripts.** No "edit this past message"
  affordance. The on-disk JSONL is the source of truth; Hecate
  imports a snapshot and that snapshot is frozen.
- **Resuming an imported session.** "Continue this Codex chat in
  Hecate" sounds attractive but reopens a hard problem: the
  external CLI's tool loop, sandbox, approval policy, and provider
  credentials are *not* Hecate's. Forking imported transcripts
  into a live session is a separate feature, possibly a separate
  RFC, possibly never.
- **Cross-tool merging.** A Claude Code session and a Codex session
  that happened to discuss the same workspace stay separate
  records. Reconciling them is a UI search problem, not an import
  problem.
- **Memory auto-extraction.** Tempting (the transcripts are
  exactly the corpus a "remember this" feature would want), but
  out of scope per [agent-memory](agent-memory.md)'s explicit
  non-goal #1.
- **Cost/usage reconstruction.** Source transcripts may carry token
  counts, but imported history does not have enough provider context
  to reconstruct reliable costs. v1 leaves `Usage` empty for imported messages and
  surfaces "no cost data — imported" in the UI rather than
  guessing.
- **Tool-call replay.** Imported `Activity` rows are descriptive,
  not actionable. No "Run this command again" button.

## Constraints

- **Local-first.** The transcripts already live on the operator's
  disk. Import is a local file read; no cloud round-trip, no
  upload step. The endpoint accepts a path or directory, not an
  uploaded blob.
- **Single-user.** All imports are owned by the local operator.
  No principal model, no per-user partitions.
- **Idempotency via natural key.** Source tools assign stable session
  ids; we trust them. `(source_tool, native_session_id)` is unique
  in the imported set. Re-running an import updates nothing if the
  on-disk file hasn't changed (size + mtime check); replaces the
  Hecate record if it has.
- **No schema fork.** Imported sessions live in the same
  `agent_chat_sessions` and `agent_chat_messages` tables as live
  sessions. Activities are kept on the message row in the existing
  `activities` JSON column — no separate activities table exists
  today and this RFC does not add one. A small set of columns gets
  added to `agent_chat_sessions`; no parallel schema.

## Source formats

### Claude Code

Path: `~/.claude/projects/<workspace-slug>/<session-uuid>.jsonl`,
where `<workspace-slug>` is the absolute project path with `/`
replaced by `-` (e.g. `-Users-chicoxyzzy-dev-hecate`).

One JSON record per line. Top-level `type` discriminates:

| `type` | Shape | Maps to |
|---|---|---|
| `user` | `{message: {role: "user", content: <string|blocks>}}` | new `Message{Role:"user"}` |
| `assistant` | `{message: {role: "assistant", content: [<blocks>]}}` | new `Message{Role:"assistant"}`, blocks → `Activity` rows |
| `system` | `{content: <string>}` | first message in the session with `Role: "system"`. The `Session` struct has no `SystemPrompt` field today; storing as a leading message keeps the import additive — no schema or struct change required. |
| `attachment` | `{path, mime_type, ...}` | `Activity{Type:"attachment"}` on the next user message |
| `queue-operation` | enqueue/dequeue marker | ignored |
| `ai-title` | session title set by Claude Code itself | `Session.Title` |
| `last-prompt` | bookkeeping | ignored |

Assistant content blocks include `text`, `tool_use`, and
`tool_result` shapes. `tool_use` becomes `Activity{Type:"tool_call",
Title:<tool_name>, Detail:<input_json>}`; `tool_result` attaches as
`ArtifactPreview` on the matching tool_call activity (joined by
`tool_use_id`).

### Codex CLI

Path: `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`. One record per
line. Top-level `type` discriminates:

| `type` | Shape | Maps to |
|---|---|---|
| `session_meta` | `{payload: {id, timestamp, cwd, originator, cli_version, model_provider, base_instructions, git: {commit_hash, branch, repository_url}}}` | `Session{NativeSessionID, Workspace, Provider, ...}`. `base_instructions.text` becomes the leading `Role: "system"` message (same treatment as Claude Code's `system` record above). |
| `event_msg` | `{payload: {type: "task_started"|"task_complete"|...}}` | timing fields on `Session.Timing` |
| `response_item` | `{payload: {type: "message", role, content: [{type, text}]}}` | `Message` |
| `response_item` (function_call) | `{payload: {type: "function_call", name, arguments}}` | `Activity{Type:"tool_call"}` |
| `response_item` (function_call_output) | matching call output | `ArtifactPreview` on the prior tool_call activity |
| `response_item` (reasoning) | `{payload: {type: "reasoning", summary: [...]}}` | `Activity{Type:"thinking"}` (omit body in v1; just the summary) |

Codex's session id is a UUIDv7 from `session_meta.payload.id`. The
filename also encodes it but we read the meta record to be safe.

### Format drift

Both tools change their JSONL shape between minor versions. The
parser must be permissive: unknown `type` values become
`Activity{Type:"unknown", Detail:<raw_json>}` with the raw line
preserved. We don't fail the whole file on one weird record. A
warning count is surfaced in the import response so operators can
notice schema drift and file an issue.

## Mapping to Hecate's model

```
SourceFile (one .jsonl)
  └─ Session (1)
      ├─ workspace              ← Claude Code workspace-slug → abs path / Codex 'session_meta.cwd'
      ├─ provider/model         ← Codex 'session_meta.model_provider' / Claude Code: best-effort from system blocks
      ├─ source_tool            ← 'claude_code' | 'codex'
      ├─ native_session_id      ← session UUID
      ├─ source_file_path       ← absolute path
      ├─ source_file_size       ← bytes (idempotency check)
      ├─ source_file_mtime      ← last-modified (idempotency check)
      ├─ imported_at            ← time.Now()
      └─ Messages (N+1)
          ├─ Role: "system"    Content: source 'system' / 'base_instructions' text (only if present)
          ├─ Role: "user"      Content: text from user record
          └─ Role: "assistant" Content: text blocks joined
              └─ activities (JSON column on the message row, no separate table):
                  ├─ tool_call (one per tool invocation)
                  └─ thinking  (Codex reasoning summaries; Claude Code thinking blocks if present)
```

The mapping is lossy in two known places:

1. **Multi-turn within one source record.** Claude Code occasionally
   collapses several user inputs into one `user` record (queued
   prompts during a streaming response). v1 keeps the collapse —
   one Hecate `user` message per source record, separator preserved
   in content. A future version could split.
2. **Tool-result orphans.** A `tool_result` whose matching
   `tool_use` got truncated (interrupted session) becomes a free-
   floating `Activity{Type:"tool_result_orphan"}` rather than being
   dropped.

## Data model

Five new columns on `agent_chat_sessions`, added through the
existing additive-migration pattern in `internal/agentchat/sqlite.go`
(repeated `ensureSessionColumn` calls — Hecate has no standalone
SQL migration files; see [migration-cli](migration-cli.md) for the
convention):

```go
{name: "source_tool",       definition: "TEXT NOT NULL DEFAULT ''"},
{name: "source_file_path",  definition: "TEXT NOT NULL DEFAULT ''"},
{name: "source_file_size",  definition: "INTEGER NOT NULL DEFAULT 0"},
{name: "source_file_mtime", definition: "TIMESTAMP"},
{name: "imported_at",       definition: "TIMESTAMP"},
```

Plus one partial unique index on the imported subset, created
alongside the existing `messagesIndex` / `sessionsIndex` block.
Its name is derived from the (possibly prefixed) `sessionsTable`
the same way the existing indexes derive theirs — `tablePrefix`
flows through `SQLiteClient.TableName`, so a hard-coded literal
`agent_chat_sessions` would collide or mismatch in test / multi-
instance setups:

```go
sessionsSourceIndex := strings.Trim(s.sessionsTable, `"`) + "_source_idx"
```

```sql
CREATE UNIQUE INDEX IF NOT EXISTS "<sessionsSourceIndex>"
  ON <s.sessionsTable> (source_tool, native_session_id)
  WHERE source_tool != '';
```

Why a partial index: live sessions leave `source_tool=''`, so the
new uniqueness constraint only covers rows written by the import
path. `agent_chat_sessions` does not currently have a unique
constraint on `(adapter_id, native_session_id)` for live sessions,
and this RFC does not propose adding one — live sessions can
plausibly share that pair across reconnects, while imported rows
are derived from immutable on-disk files where the pair is stable.

The Go `Session` struct gains:

```go
type Session struct {
  // ... existing fields ...
  SourceTool      string    // "" for live sessions
  SourceFilePath  string
  SourceFileSize  int64
  SourceFileMTime time.Time
  ImportedAt      time.Time
}
```

`Session.IsImported()` is sugar for `s.SourceTool != ""`. Every
write path that resumes / streams / cancels a session checks this
and refuses with a structured error. Single chokepoint, single test.

## API surface

Two new endpoints under `/hecate/v1/agent-chat/imports/`:

### `POST /hecate/v1/agent-chat/imports/scan`

Body:
```json
{
  "sources": [
    {"tool": "claude_code", "root": "~/.claude/projects"},
    {"tool": "codex",       "root": "~/.codex/sessions"}
  ],
  "since": "2026-01-01T00:00:00Z"
}
```

`sources` defaults to both tools at their default paths if omitted.
`since` filters by file mtime; defaults to "30 days ago".

Response:
```json
{
  "candidates": [
    {
      "tool": "claude_code",
      "path": "/abs/path/to.jsonl",
      "native_session_id": "f2ea6177-...",
      "size_bytes": 184320,
      "mtime": "2026-04-22T02:56:20Z",
      "preview_title": "Check architecture and search for legacy code",
      "already_imported": false
    },
    ...
  ],
  "warnings": ["unreadable: /…/x.jsonl: permission denied"]
}
```

Pure read; no writes. Lets the UI render a picker before committing.

### `POST /hecate/v1/agent-chat/imports/apply`

Body:
```json
{
  "items": [
    {"tool": "claude_code", "path": "/abs/path/to.jsonl"},
    {"tool": "codex",       "path": "/abs/path/to/rollout-*.jsonl"}
  ]
}
```

Response:
```json
{
  "imported": [
    {"session_id": "agc_…", "native_session_id": "f2ea6177-…", "messages": 24, "warnings": 0}
  ],
  "skipped": [
    {"path": "…", "reason": "already imported, source unchanged"}
  ],
  "failed": [
    {"path": "…", "error": "parse error at line 178: unexpected token"}
  ]
}
```

Streaming progress over `/hecate/v1/agent-chat/imports/stream` (SSE)
for the bulk case where the operator might be importing 200+
sessions. Same envelope as existing chat streams.

### Read path

Imported sessions surface through the existing
`GET /hecate/v1/agent-chat/sessions[/:id]` with the new fields
included. A query filter `?source_tool=claude_code|codex|live`
lets the UI partition the list. Default is "all".

## UI surface

One new entry point: **Settings → Chats → Import history**. Opens a
modal:

1. Tool list with detected file counts ("`~/.claude/projects/`:
   142 sessions" / "`~/.codex/sessions/`: 38 sessions").
2. Date filter ("Last 30 days" / "Last 90 days" / "All").
3. **Scan** → table of candidates with title, workspace, date,
   size, "already imported" badge.
4. Bulk-select + **Import**. Progress streams over SSE.

In the existing Chats list, imported sessions render with:

- A small "imported" badge next to the title.
- The source tool icon (Claude Code logo / Codex logo).
- Read-only banner at the top of the transcript: "Imported from
  `<path>` on `<date>`. This session can't be resumed." The compose
  bar is hidden, not just disabled.

The transcript view is otherwise unchanged. Tool-call activities,
diffs, attachments — all render through the same components live
sessions use.

## Phasing

| Phase | Scope | Done when |
|---|---|---|
| 1 | Storage shape + Claude Code parser + scan endpoint | A scan against `~/.claude/projects/` returns candidates without writing. |
| 2 | Apply endpoint + idempotency + read path | Imported Claude Code sessions render in the existing transcript view, read-only. |
| 3 | Codex parser | Same flow works for `~/.codex/sessions/`. |
| 4 | UI modal | Operator path is end-to-end. |
| 5 | Bulk progress over SSE | 100+ session import doesn't lock the UI. |

Phases 1–2 are the meaningful unit; 3–5 are mechanical follow-ups.

## Open questions

- **Workspace inference for Claude Code.** The folder name is the
  abs path with `/` → `-`. Reversing that is unambiguous (replace
  every `-` after the leading one with `/`) only as long as no real
  path component contains `-`. Real-world paths do
  (`my-project/`). Options: (a) accept the lossy decode, (b) read
  the `cwd` field if Claude Code records one in the session header
  (it does, in newer versions), (c) ask the operator to confirm
  for ambiguous cases. Lean toward (b) with (a) as fallback;
  prompt the operator only if both fail.
- **Diff capture.** Codex sessions can include git status snapshots
  in `event_msg` payloads. Worth turning into `Activity{Type:"diff"}`
  rows? Cheap to do; the UI already renders diff activities. Default
  yes; trivial to gate behind a flag if it gets noisy.
- **System-prompt storage.** Claude Code system blocks are huge
  (full operator instructions, plus the inserted skills, plus the
  model card). Storing them verbatim per session bloats the DB by
  ~10 MB per 100 sessions. Options: (a) verbatim, (b) hash + dedupe
  table, (c) drop. (b) is the right answer; the agent-memory RFC
  needs the same primitive. Gate v1 on (a) and follow up.
- **PII / secret scrubbing.** Imported transcripts may contain API
  keys, paths, credentials the operator pasted into Claude Code. Do
  we scrub on import or trust the local-only contract? Scrubbing
  changes content irreversibly; not scrubbing means a leak surface
  on the SQLite DB. Lean: don't scrub (consistent with the rest of
  Hecate's local-first stance), document the risk in
  [`Security`](../security.md).
- **What counts as the system prompt for Codex.** The
  `session_meta.base_instructions.text` is the right anchor, but
  Codex also injects skill / plugin / app instructions as
  subsequent `developer`-role messages. v1 stores the meta text as
  the leading `Role: "system"` message and rolls developer-role
  messages into the transcript as additional `Role: "system"`
  messages in source order. Lossiness: the UI can't easily
  distinguish "operator's system prompt" from "Codex framework
  injected this." Acceptable for v1; a `MessageChannel` enum is a
  follow-up.
- **CLI vs UI entry point.** A `hecate import-history --tool=codex
  --root=…` command is a one-day add and useful for batch / cron
  imports. Defer to v2 unless an operator asks; UI covers the
  expected case.

## Migration / rollback

- Forward: additive only — five new columns + one partial unique
  index on `agent_chat_sessions`, applied through the existing
  `ensureSessionColumn` pattern. No data rewrite. Old code reads
  the new columns and ignores them (default-empty); new code reads
  them and treats `source_tool != ""` rows as imported.
- Rollback: per the [migration-cli](migration-cli.md) RFC,
  Hecate's recovery path for additive migrations is restore from
  snapshot, not down-migration. There's nothing destructive to
  undo. If the operator restores a snapshot taken before the
  import columns were added, the new columns simply re-appear
  on next startup; any imported rows in a rolled-back snapshot
  are orphaned read-only records (no resume path code exists in
  the rolled-back binary because it was built before this RFC).

## Out of scope but worth noting

- **Other agent CLIs** (Cursor Agent, Aider, Continue). Cursor's
  history is in the editor's local state; Aider writes a Markdown
  log, not JSONL. Each is a separate parser and the value tier is
  much lower. Add when a real operator asks.
- **Re-export.** "Export this Hecate session as Claude Code JSONL"
  is the inverse problem. Useful for round-tripping into Claude
  Code's own search; not relevant here.
- **Search across imported and live transcripts.** Already covered
  by the existing chat-search work; imported sessions inherit it
  for free once they land in the same tables.

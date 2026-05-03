# Artifact Storage — v1 Candidate (RFC)

> **Status:** draft / RFC. Not implemented. Not stable. Not yet a frontend contract.
> **Depends on:** [`event-protocol-v1.md`](event-protocol-v1.md) — artifacts are referenced by `artifact_id` from event payloads.
> **Owner:** see [`AGENTS.md`](../AGENTS.md).

This document proposes how the agent runtime stores, fetches, and prunes the
persisted byte blobs the [event protocol](event-protocol-v1.md) calls
**artifacts** — patches, command outputs, fetched URL bodies, file snapshots,
image attachments. Artifacts are first-class objects with a stable id, a
content-addressable hash, a kind-specific lifecycle, and an HTTP API.

This is the highest-risk unknown the event protocol RFC flagged. Settling it
unblocks the Edit/MultiEdit tool families and the CLI/web/IDE consumers that
need to render diffs without re-deriving state from `git status`.

Before this document can be treated as candidate-stable:

- The auth model must match the rest of `/v1`.
- The storage design must fit Hecate's current backend model: memory for
  ephemeral/dev state and SQLite for durable local state.
- Patch-review endpoint shapes must be decided before any diff-review UI
  depends on artifact status transitions.
- At least `command_output` must be implemented end-to-end with retention and
  contract fixtures.

## Why first-class artifacts

Today, when an agent edits a file, the change exists only as filesystem state in the workspace. There is no record of what the patch looked like before it landed, no way to apply/revert/replay it, no review surface, no audit trail. Every consumer that wants to ask "what did the agent change?" has to run `git diff` against a moving target.

First-class artifacts flip that:

- **Diffs are queryable.** "Show me every patch this run produced" is `WHERE kind='patch' AND run_id=X`. No filesystem walk.
- **Apply/revert/replay are real operations.** A patch in `proposed` status can be reviewed, then transitioned to `applied` (gateway writes it) or `rejected`. A revert produces an inverse patch as a new artifact, leaving the original's audit trail intact.
- **Multi-frontend rendering is trivial.** The CLI, web UI, and ACP server all call `GET /v1/artifacts/{id}` for the body — there's no second source of truth they have to reconcile.
- **Audit becomes a database query.** Compliance: "what shell commands did agent X run, and what did they output?" is one SQL select.
- **Replay against a fresh worktree.** A run's checkpoint plus its applied patches reconstruct the workspace exactly. Useful for debugging and CI integration.

Without artifacts, the protocol is a stream of pointers to nowhere. With them, it's the foundation for every coding-agent UX.

## Storage architecture

A two-tier body layout behind Hecate's current storage-backend model:
**metadata and small bodies live in the configured artifact backend; large
bodies spill to object-like blob storage**. For the single-user local path, that
blob storage is the filesystem under `GATEWAY_DATA_DIR`. Consumers never see
the split.

```
+---------------------- gateway process -----------------------+
|                                                              |
|  POST /v1/artifacts ─────► artifact service                  |
|                                  │                            |
|                                  ▼                            |
|                          +---------------+                    |
|                          | artifact index|                    |
|                          | + inline body |                    |
|                          | (< 16KB)      |                    |
|                          +---------------+                    |
|                                  │                            |
|                                  ▼ (size ≥ 16KB)              |
|                  ${data_dir}/artifacts/ab/cd/art_01JX...      |
|                          (gzip-compressed bytes)              |
|                                                               |
+---------------------------------------------------------------+
```

Why this shape:

- **Database-only storage** works for indexing and small artifacts but degrades
  on large command outputs and fetched resources.
- **Filesystem-only storage** is fine for blobs but wastes inode + open(2) on
  the thousands of tiny patches a long-running agent can produce.
- **Hybrid** keeps small artifacts hot in the index while making large bodies
  streamable from disk or future object storage.

Backend expectations:

| Backend | Candidate behavior |
|---|---|
| `memory` | Metadata + bodies in process memory. Test/dev only. |
| `sqlite` | Metadata + small bodies in SQLite; large bodies on local filesystem. |

Distributed storage is intentionally out of scope for this candidate. If Hecate
reintroduces a networked durable backend later, artifact bodies should move to a
shared blob/object store rather than node-local filesystem paths.

Inline cutoff: **16 KiB** raw bytes (before compression). Below: stored in `body_inline BLOB`. Above: written to `${data_dir}/artifacts/${id[:2]}/${id[2:4]}/${id}` and compressed with gzip. Two-level fanout to avoid 100k+ files in one directory.

## Schema

```sql
CREATE TABLE artifacts (
    id              TEXT PRIMARY KEY,           -- ULID, e.g. art_01JXMZH...
    kind            TEXT NOT NULL,              -- patch | command_output | fetched_resource | ...
    status          TEXT NOT NULL,              -- created | proposed | applied | rejected | reverted
    sha256          TEXT NOT NULL,              -- of raw bytes (verification + future dedupe)
    size_bytes      INTEGER NOT NULL,           -- raw, uncompressed
    mime_type       TEXT NOT NULL,              -- text/x-diff, text/plain, application/json, etc.
    encoding        TEXT NOT NULL,              -- identity | gzip
    body_inline     BLOB,                       -- non-null when size_bytes < 16384
    storage_path    TEXT,                       -- non-null when body_inline IS NULL
    metadata_json   TEXT NOT NULL,              -- kind-specific JSON, see below
    pinned          INTEGER NOT NULL DEFAULT 0, -- 0 | 1 — operator-flagged, never auto-pruned
    created_at      TEXT NOT NULL,              -- RFC3339 nano
    updated_at      TEXT NOT NULL,
    -- Provenance: who produced this artifact?
    run_id          TEXT,                       -- nullable for ad-hoc creates
    task_id         TEXT,
    session_id      TEXT,
    tool_call_id    TEXT,
    -- Lifecycle timestamps (only relevant for kinds with status transitions):
    proposed_at     TEXT,
    applied_at      TEXT,
    reverted_at     TEXT,
    rejected_at     TEXT,
    -- Soft-delete marker for retention worker; rows here are pruned next pass.
    deleted_at      TEXT
);

CREATE INDEX artifacts_run_id     ON artifacts(run_id)     WHERE deleted_at IS NULL;
CREATE INDEX artifacts_task_id    ON artifacts(task_id)    WHERE deleted_at IS NULL;
CREATE INDEX artifacts_session_id ON artifacts(session_id) WHERE deleted_at IS NULL;
CREATE INDEX artifacts_kind_status ON artifacts(kind, status) WHERE deleted_at IS NULL;
CREATE INDEX artifacts_sha256     ON artifacts(sha256)    WHERE deleted_at IS NULL;
CREATE INDEX artifacts_created_at ON artifacts(created_at) WHERE deleted_at IS NULL;
CREATE INDEX artifacts_pinned     ON artifacts(pinned, created_at) WHERE deleted_at IS NULL AND pinned = 0;
```

Notes:

- `id` is a ULID prefixed with `art_` to match the event protocol's id conventions.
- `sha256` is over the **raw** bytes, not the compressed form. Used for integrity verification on read and (future) dedupe.
- `encoding` is the on-storage encoding, not the wire encoding. Always `identity` for inline bodies (compression overhead exceeds savings under 16 KiB).
- `metadata_json` is enforced by the artifact service per kind (see [Kinds](#kinds)). The DB itself doesn't validate; the service does.
- `deleted_at` enables soft-delete: the retention worker marks rows for pruning, then a separate sweep frees inline blobs and unlinks filesystem paths. Lets a fast prune-mark + lazy free pattern.

## Kinds

Each kind has fixed `mime_type`, `metadata_json` schema, and lifecycle.

### `patch`

A unified diff against a single file path.

```json
{
  "kind": "patch",
  "mime_type": "text/x-diff",
  "metadata_json": {
    "target_path": "internal/budget/governor.go",
    "base_revision": "sha256:8a4f2c...",
    "stats": { "additions": 1, "deletions": 1, "hunks": 1 },
    "produced_by_tool_call_id": "call_01JXMZ..."
  }
}
```

Lifecycle: `proposed → (applied | rejected) → (reverted)?`.

A revert produces a **new** artifact (the inverse patch), referencing the original via `metadata_json.reverted_from_artifact_id`. The original keeps `status=applied, reverted_at=<ts>` so the audit trail is intact.

### `command_output`

stdout + stderr from a `tool.shell.*` invocation.

```json
{
  "kind": "command_output",
  "mime_type": "text/plain; charset=utf-8",
  "metadata_json": {
    "argv": ["go", "test", "./..."],
    "exit_code": 0,
    "signal": null,
    "stdout_bytes": 3120,
    "stderr_bytes": 0,
    "truncated": false,
    "duration_ms": 4210
  }
}
```

Lifecycle: `created`. No transitions.

The body interleaves stdout and stderr in arrival order; metadata's `stdout_bytes` / `stderr_bytes` totals let consumers split if needed. (Open question: should we store them as separate streams instead? See [Open questions](#open-questions).)

### `fetched_resource`

Body of an HTTP response from `tool.http.*` or `tool.web_fetch.*`.

```json
{
  "kind": "fetched_resource",
  "mime_type": "<from upstream Content-Type>",
  "metadata_json": {
    "url": "https://api.example.com/v1/foo",
    "method": "GET",
    "status": 200,
    "request_header_keys": ["Authorization", "Accept"],
    "response_header_keys": ["Content-Type", "X-Request-Id"],
    "duration_ms": 412,
    "truncated": false
  }
}
```

Lifecycle: `created`.

Headers are stored as **key lists, not values**, by default — values may contain secrets (auth tokens, signed URLs). A future operator-toggle could store full values for debugging. See [Open questions](#open-questions).

### `file_snapshot`

A point-in-time copy of a file's contents. Used by `tool.file_read.*` for large files (so the model's response can quote them without re-reading), and by checkpoints (so resumes can reconstruct workspace state).

```json
{
  "kind": "file_snapshot",
  "mime_type": "<inferred>",
  "metadata_json": {
    "path": "internal/budget/governor.go",
    "revision": "sha256:8a4f2c...",
    "lines": 312,
    "snapshot_reason": "tool_read | checkpoint"
  }
}
```

Lifecycle: `created`.

### `search_results`

Structured output from `tool.web_search.*`.

```json
{
  "kind": "search_results",
  "mime_type": "application/json",
  "metadata_json": {
    "query": "go generics tutorial",
    "provider": "duckduckgo",
    "result_count": 12,
    "fetched_at": "2026-05-03T10:23:45Z"
  }
}
```

Body is JSON `[{"title", "url", "snippet"}, …]`.

### `image`

User-attached or model-emitted image.

```json
{
  "kind": "image",
  "mime_type": "image/png",
  "metadata_json": {
    "width": 1024,
    "height": 768,
    "source": "user_upload | model_output"
  }
}
```

Lifecycle: `created`.

### `tool_result_blob`

Catch-all for opaque MCP results that don't fit other kinds. Body is whatever bytes the MCP server returned; mime type is best-effort.

```json
{
  "kind": "tool_result_blob",
  "mime_type": "<server-declared>",
  "metadata_json": {
    "mcp_server": "filesystem",
    "tool_name": "mcp__filesystem__read_text_file",
    "is_error": false
  }
}
```

Lifecycle: `created`.

## Status transitions

Only `patch` artifacts have non-trivial transitions:

```
   create
     │
     ▼
 proposed ──── operator rejects ──► rejected   (terminal)
     │
     │ runtime applies (auto or after approval)
     ▼
  applied ──── operator reverts ──► applied + reverted_at
                                          │
                                          └── new artifact created (inverse patch, status=applied)
```

All other kinds are `created` and stay there. The `status` column is still present for uniformity (frontends filter on it without checking kind).

Transitions are persisted as `artifact.updated` events (see event protocol). The HTTP API to drive them is `PATCH /v1/artifacts/{id}` (runtime-internal — see [API](#api-surface)).

## Size limits and capping

Three caps, all configurable. Defaults chosen to keep a typical workstation under 1 GiB after a week of active use.

| Cap | Env | Default | Behavior on hit |
|---|---|---|---|
| Per-artifact bytes | `GATEWAY_ARTIFACTS_MAX_BYTES` | `10485760` (10 MiB) | Body truncated to limit; producing tool event carries `truncated: true` and `original_size_bytes`. |
| Per-task aggregate | `GATEWAY_ARTIFACTS_MAX_PER_TASK_BYTES` | `104857600` (100 MiB) | New artifact creation rejected with `429 quota_exceeded`; `gap.artifact_capped` event emitted. |
| Per-data-dir aggregate | `GATEWAY_ARTIFACTS_MAX_TOTAL_BYTES` | `5368709120` (5 GiB) | Retention worker prunes oldest unpinned artifacts ahead of schedule. |

The per-artifact cap is the most user-visible: long shell commands get their output snipped. The truncation strategy is "head-keep" (preserve the start, drop the tail), since the start usually has the command and early output a model needs to interpret what happened. Reversible via a `GATEWAY_ARTIFACTS_TRUNCATE_STRATEGY=tail|head|both` knob (head = keep head, default).

## Compression and encoding

Stored bodies use one of two encodings:

- `identity` — raw bytes. Always for inline (< 16 KiB). Always for already-compressed content (images).
- `gzip` — used for filesystem-stored bodies whose mime type is text-shaped (`text/*`, `application/json`, `application/x-yaml`, `text/x-diff`).

Wire encoding is **identity by default**. Frontends that opt in via `Accept-Encoding: gzip` get the on-storage compressed body straight through (no decompress + recompress); others get a fresh decompress.

The artifact service maintains a small allowlist of mime types that compress; binary kinds skip gzip entirely.

## API surface

All endpoints are loopback-only and same-origin enforced (single-user mode). Documented under `/v1/artifacts`.

### Create

```
POST /v1/artifacts
Idempotency-Key: <tool_call_id or arbitrary nonce>
Content-Type: multipart/form-data; boundary=...

--boundary
Content-Disposition: form-data; name="metadata"
Content-Type: application/json

{
  "kind": "patch",
  "mime_type": "text/x-diff",
  "metadata": { "target_path": "...", "base_revision": "...", "stats": {...} },
  "run_id": "run_01JX...",
  "task_id": "task_01JX...",
  "tool_call_id": "call_01JX..."
}

--boundary
Content-Disposition: form-data; name="body"
Content-Type: text/x-diff

--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,3 @@
-old
+new
--boundary--
```

→ `201 Created`

```json
{
  "object": "artifact",
  "data": { "id": "art_01JX...", "kind": "patch", "status": "proposed", ... }
}
```

`Idempotency-Key`: when present, repeated POSTs with the same key return the existing artifact (matched by `tool_call_id` + `kind`). Lets tool implementations retry on transient errors safely.

JSON-only body for small artifacts: an alternate form with `Content-Type: application/json` and `body_base64` field is supported when the body is ≤ 1 MiB. Avoids multipart for shell-script tool implementations.

### Fetch metadata

```
GET /v1/artifacts/{id}
```

→ `200 OK`

```json
{
  "object": "artifact",
  "data": {
    "id": "art_01JX...",
    "kind": "patch",
    "status": "applied",
    "sha256": "...",
    "size_bytes": 412,
    "mime_type": "text/x-diff",
    "metadata": { "target_path": "...", ... },
    "pinned": false,
    "created_at": "...",
    "updated_at": "...",
    "applied_at": "...",
    "run_id": "run_01JX...",
    "task_id": "task_01JX...",
    "tool_call_id": "call_01JX..."
  }
}
```

Note: no body. Use `/raw` to fetch bytes.

### Fetch body

```
GET /v1/artifacts/{id}/raw
Accept-Encoding: gzip       # optional
Range: bytes=0-1023         # optional, range support for large blobs
```

→ `200 OK` (or `206 Partial Content` for ranges)

```
Content-Type: text/x-diff
Content-Length: 412
ETag: "sha256:8a4f2c..."

--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,3 @@
...
```

`ETag` is the sha256 prefix; clients can If-None-Match for cache validation.

### Update status (runtime-internal)

```
PATCH /v1/artifacts/{id}
Content-Type: application/json

{ "status": "applied" }
```

→ `200 OK` with the updated artifact.

Allowed transitions are enforced server-side. The candidate decision is:
**runtime-owned status transitions only**. External clients, including the web
UI, do not call this endpoint directly. They drive patch review through
higher-level runtime endpoints such as `POST /v1/patches/{id}/apply` or
approval resolution; those endpoints perform workspace checks and then update
the artifact internally.

The exact higher-level patch-review endpoints are still out of scope for this
RFC, but direct UI-driven `PATCH /v1/artifacts/{id}` is not part of the
candidate contract.

### Pin / unpin

```
PATCH /v1/artifacts/{id}
{ "pinned": true }
```

`pinned: true` exempts the artifact from auto-pruning. Operator-driven only; runtime never pins.

### Delete

```
DELETE /v1/artifacts/{id}
```

Allowed only for `pinned=false` artifacts. Soft-delete: marks `deleted_at`, retention worker frees bytes on next pass. `404` if the artifact is already deleted; `409` if pinned.

### List

```
GET /v1/tasks/{task_id}/runs/{run_id}/artifacts
GET /v1/tasks/{task_id}/artifacts
GET /v1/artifacts?kind=patch&status=proposed&limit=100
```

The shorter `/v1/runs/{run_id}/artifacts` alias is a possible future
convenience, but it is not part of the candidate contract until implemented.

Returns paginated artifact metadata (no bodies). Filterable by `kind`, `status`, `task_id`, `session_id`, `pinned`, `created_after`, `created_before`. Sortable by `created_at` (default desc).

```json
{
  "object": "list",
  "data": [{ "id": "art_01JX...", "kind": "patch", ... }, ...],
  "has_more": true,
  "next_cursor": "art_01JX..."
}
```

Cursor is the `id` of the last item in the current page (ULID id makes it a natural sort key).

## Idempotency

Tool implementations may retry artifact creation on transient errors (network blip, sqlite busy, etc.). The runtime supports two idempotency layers:

1. **`Idempotency-Key` header on POST.** When present, the artifact service looks up `(tool_call_id, kind)` first; on hit, returns the existing artifact (200, not 201). Keys live in the same row as the artifact (`tool_call_id` column already serves the lookup).

2. **sha256 fast-path.** If a POST's body sha256 matches an existing artifact's sha256 within the same `(run_id, kind)` scope, the existing artifact is returned. Dedupes when a tool re-emits the same patch on retry without setting `Idempotency-Key`.

Neither dedupe attempts cross-run sharing in v1 — a patch produced by run A is independent of a byte-identical patch from run B. Cross-run dedupe is a v2 storage optimization (`refcount` column) deferred until there's evidence it matters.

## Retention

Artifacts inherit the project's existing retention worker pattern. New subsystem name: `artifacts`. New env-var prefix: `GATEWAY_RETENTION_ARTIFACTS_*`.

| Env | Default | Effect |
|---|---|---|
| `GATEWAY_RETENTION_ARTIFACTS_MAX_AGE` | `168h` (7 days) | Unpinned artifacts older than this are marked `deleted_at`. |
| `GATEWAY_RETENTION_ARTIFACTS_MAX_COUNT` | `100000` | When unpinned count exceeds, oldest are pruned first. |
| `GATEWAY_RETENTION_ARTIFACTS_MAX_TOTAL_BYTES` | `5368709120` (5 GiB) | When sum of `size_bytes` exceeds, oldest unpinned pruned until under. |

Pruning order on each pass:

1. Artifacts whose parent run was pruned by the `runs` subsystem.
2. Unpinned artifacts past `MAX_AGE`.
3. Unpinned artifacts when `count > MAX_COUNT` (oldest first).
4. Unpinned artifacts when `total_bytes > MAX_TOTAL_BYTES` (oldest first).

Pinned artifacts: never auto-pruned. Operator must DELETE explicitly or unpin first.

Two-phase prune:

- **Mark phase** (cheap, frequent): `UPDATE artifacts SET deleted_at = now WHERE ...`. Atomic, one transaction.
- **Free phase** (expensive, periodic): for each `deleted_at IS NOT NULL` row, unlink filesystem path (if any), delete row. Throttled to avoid I/O spikes.

This decouples "stop counting toward quotas" from "actually free disk space," so the operator-visible cap is responsive even when the disk is busy.

`run.finished` and `run.failed` events trigger an immediate per-run prune sweep for `command_output` artifacts older than 1 hour, regardless of the global age. Rationale: most command output is interesting only while a run is in flight; once it's terminal, the metadata in `turn.completed` carries the operator-relevant summary. (Off by default — see [Open questions](#open-questions).)

## Auth and access

Artifacts may contain command output, file contents, patches, fetched HTTP
bodies, image inputs, and model/tool results. They must inherit the same auth
and same-origin rules as the runtime APIs that produced them.

Candidate rules:

- `/v1/artifacts/*` requires the same bearer/auth mode as `/v1/tasks/*`.
- Same-origin middleware still applies for browser callers.
- Admin principals can read all artifacts.
- Tenant principals can read artifacts for tasks/runs they are allowed to read.
- Artifact bodies are never exposed through unauthenticated static-file serving.

Single-user desktop mode can make this feel transparent by using the generated
local admin token, but the API contract should not special-case artifacts as
"no bearer auth".

## Observability (OTel)

New metrics (all under `hecate.artifacts.*`):

| Instrument | Type | Labels | Meaning |
|---|---|---|---|
| `hecate.artifacts.created_total` | counter | `kind` | Artifact create calls. |
| `hecate.artifacts.bytes_stored_total` | counter | `kind`, `encoding` | Raw bytes added to storage. |
| `hecate.artifacts.pruned_total` | counter | `kind`, `reason` | Reasons: `age`, `count_cap`, `bytes_cap`, `parent_run_pruned`, `manual_delete`. |
| `hecate.artifacts.bytes_freed_total` | counter | `kind`, `reason` | Bytes released by prune. |
| `hecate.artifacts.storage_bytes` | gauge | `location` | Current bytes in `inline` and `filesystem`. Sampled by retention worker. |
| `hecate.artifacts.count` | gauge | `kind`, `status` | Current count by kind and status. |
| `hecate.artifacts.size_bytes` | histogram | `kind` | Size distribution of created artifacts. Buckets: `[256, 1k, 4k, 16k, 64k, 256k, 1m, 4m, 16m]`. |
| `hecate.artifacts.fetch_duration_ms` | histogram | `path` (`metadata` / `raw`), `cache_hit` | Latency of GET endpoints. |

Spans:

- `artifact.create` — wraps the POST handler.
- `artifact.fetch` — wraps GET; carries `artifact.id` and `artifact.kind` attributes.
- `artifact.prune_pass` — wraps a single retention sweep; carries pruned counts by reason.

Existing log events get a new tag for artifact-creating tool calls: `artifact_id` lands alongside `tool_call_id` in `tool.completed` log records, so a single trace can pivot from "tool ran" to "here's its output blob."

## Migration / compatibility

There's no existing artifact storage to migrate from. Greenfield.

But there is **existing tool-emitted state** that needs a one-shot migration to artifacts when the new tool implementations land:

- Existing `command_output` data is currently embedded in run-event payloads (large strings inline). After cutover, new emits go to artifacts; old run events keep the inline form.
- No backfill: rather than rewriting old run events, the migration is forward-only. Old events keep working with their inline data; new ones reference artifacts. Frontends handle both for at least one minor release.

This avoids a heavy migration script and lets the cutover land behind a flag
(`GATEWAY_ARTIFACTS_ENABLED=true`) while the schema is still experimental.

## Open questions

These need design before this draft can become v1.0 stable:

1. **stdout vs stderr separation.** Today's plan stores them interleaved with byte counts in metadata. Should `command_output` be two artifacts (one per stream) instead? Pro: clean separation, easier filtering. Con: doubles the artifact count for every shell call, breaks the "tool call → one output blob" mental model.

2. **HTTP header value capture.** `request_header_keys` and `response_header_keys` store key names only (values may contain secrets). Should we have an opt-in `GATEWAY_ARTIFACTS_CAPTURE_HEADER_VALUES=true` for debugging environments? With redaction of well-known auth keys (`Authorization`, `Cookie`, `X-Api-Key`, etc.)?

3. **`command_output` short-TTL.** The "1-hour post-finished prune for command_output" idea trades disk for replayability. Off by default seems right, but is the knob worth shipping at all if no one will tune it?

4. **Cross-run sha256 dedupe.** Deferred to v2. Needs a `refcount` column and careful prune semantics. Worth doing if patches turn out to be repeated frequently (e.g., the same lint fix across many files); useless if every patch is unique.

5. **Patch-review endpoint shape.** Status-transition authority is runtime-owned,
   but the higher-level apply/reject/revert endpoints still need exact request
   and response shapes.

6. **Streaming POST.** Large artifacts (multi-megabyte command outputs) currently require buffering the full body before POST. Should the API support a streaming form — `POST /v1/artifacts` with `Transfer-Encoding: chunked`, then a subsequent `PATCH` to mark complete? Probably yes, but adds non-trivial complexity. Defer until a real consumer needs it.

7. **Garbage in inline blobs.** sqlite VACUUM is required to actually reclaim space from deleted inline blobs. Should the retention worker run `VACUUM INCREMENTAL` after large prune passes? Pro: bounded disk. Con: blocking writes during VACUUM. Probably yes with a backoff policy.

## Next steps

1. **Land this doc** as a draft RFC alongside the event-protocol RFC. Solicit feedback on the open questions, especially #1 (stream separation) and #5 (patch-review endpoint shape).
2. **Implement the schema + service in a feature branch.** Single-package Go module under `internal/artifacts/`, with memory and SQLite backends behind the existing storage abstractions.
3. **Wire `tool.shell.*` to produce `command_output` artifacts** — the smallest end-to-end slice that exercises create, fetch, list, retain. Behind `GATEWAY_ARTIFACTS_ENABLED`.
4. **Add the retention subsystem.** Mark + free phases, OTel metrics, env vars in `.env.example`.
5. **Migrate one tool family at a time** — shell first, then file_read, then patch (which unblocks Edit/MultiEdit). Each migration is one PR.
6. **Cut over the web UI** for that family; verify both old and new code paths in parallel for a release.
7. **Mark v1.0 stable** when all in-tree tool families are migrated, auth/storage behavior is implemented across memory and SQLite, and the open questions are resolved.

Estimated wall-clock for the runtime side: 3-4 weeks for the core service + retention + OTel; tool-family migrations are 2-3 days each on top, parallelizable. The risk concentration is in the retention worker's free phase under high churn; everything else is well-trodden.

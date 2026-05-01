# Semantic cache

Hecate's semantic cache sits after the exact cache in the gateway request pipeline. Where the exact cache requires a byte-for-byte match on the full request, the semantic cache uses vector similarity to return a prior response when an incoming prompt is *close enough* to one already seen. Both caches short-circuit the provider call entirely and set `X-Runtime-Cache: true` on the response.

> Contributing here? Start at [`AGENTS.md`](../AGENTS.md) for the codebase map and runtime invariants; conventions, workflow, and verification ladders live under [`ai/`](../ai/README.md).

## Contents

- [How it works](#how-it-works)
- [Enabling it](#enabling-it)
- [Embedders](#embedders)
- [Storage backends](#storage-backends)
- [Tuning](#tuning)
- [Admin endpoints](#admin-endpoints)
- [Observability](#observability)
- [Limitations](#limitations)

## How it works

For every cache miss on the exact cache, Hecate:

1. Extracts the text of the last user message (up to `GATEWAY_SEMANTIC_CACHE_MAX_TEXT_CHARS` characters).
2. Calls the configured embedder to produce a vector for that text.
3. Queries the semantic store for the nearest stored vector above `GATEWAY_SEMANTIC_CACHE_MIN_SIMILARITY`.
4. On a hit: returns the stored response, increments hit counters, emits a `gateway.cache.semantic` span with `hecate.cache.hit=true`.
5. On a miss: calls the provider, stores the response vector for future lookups.

The similarity threshold is cosine distance. The default `0.92` is deliberately high — a low threshold produces surprising cache hits on unrelated prompts. Start high and lower carefully once you can inspect hits via traces.

## Enabling it

The semantic cache is **disabled by default**. Enable it with:

```env
GATEWAY_SEMANTIC_CACHE_ENABLED=true
```

With no other changes, the in-memory backend and `local_simple` embedder are used — suitable for development and light experimentation. For production use, configure a real embedder and the Postgres backend (see below).

## Embedders

Two embedder strategies are available, selected via `GATEWAY_SEMANTIC_CACHE_EMBEDDER`:

### `local_simple` (default)

A deterministic bag-of-words embedder built into the gateway binary. No external calls, no API key. It produces consistent vectors across restarts so memory-backend hits survive for the process lifetime.

**Use for:** development, local testing, environments with no external embedder access.

**Not for:** semantic-quality workloads. It does not understand synonyms, paraphrases, or meaning — only token overlap. Two prompts with identical tokens in different orders score very differently.

### `openai_compatible`

Calls any OpenAI-compatible `/v1/embeddings` endpoint. Configure it with:

```env
GATEWAY_SEMANTIC_CACHE_EMBEDDER=openai_compatible
GATEWAY_SEMANTIC_CACHE_EMBEDDER_PROVIDER=openai          # or any configured provider name
GATEWAY_SEMANTIC_CACHE_EMBEDDER_MODEL=text-embedding-3-small
GATEWAY_SEMANTIC_CACHE_EMBEDDER_BASE_URL=                # leave empty to use the provider's base URL
GATEWAY_SEMANTIC_CACHE_EMBEDDER_API_KEY=                 # leave empty to use the provider's key
GATEWAY_SEMANTIC_CACHE_EMBEDDER_TIMEOUT=30s
```

`EMBEDDER_PROVIDER` resolves credentials and base URL from the gateway's configured provider record — set it to the name of any provider already registered in the control plane. `EMBEDDER_BASE_URL` and `EMBEDDER_API_KEY` override the provider's values when set explicitly.

**Use for:** production workloads where semantic quality matters.

**Cost note:** every cache miss generates one embedding call. At high request rates, embedding costs can be significant — monitor `GATEWAY_SEMANTIC_CACHE_EMBEDDER_PROVIDER`'s usage ledger.

## Storage backends

| Backend | Persistent | Notes |
|---|---|---|
| `memory` | No (rebuilds on restart) | Default everywhere. Fine for ≲10k entries. |
| `postgres` | Yes | Uses `pgvector`. Supports HNSW / IVFFlat indexing for >100k entries. Requires `POSTGRES_DSN`. |
| `sqlite` | — | **Not supported.** See [why](#why-no-sqlite). |

Switch to Postgres for just this subsystem while keeping everything else on SQLite:

```env
GATEWAY_SEMANTIC_CACHE_BACKEND=postgres
POSTGRES_DSN=postgres://user:pass@host/db
```

All other subsystems continue to use their own backends unchanged.

### Why no SQLite

Indexed vector similarity in SQLite requires the `sqlite-vec` extension, which is a native C library. Hecate uses `modernc.org/sqlite` — a pure-Go translation of SQLite that cannot load native extensions — to keep the single-static-binary build story. Switching to a CGO or Wazero driver would add significant build complexity. Until that trade-off changes, use `memory` for single-node deploys and `postgres` when persistence matters.

## Tuning

| Env var | Default | What it controls |
|---|---|---|
| `GATEWAY_SEMANTIC_CACHE_ENABLED` | `false` | Master switch |
| `GATEWAY_SEMANTIC_CACHE_BACKEND` | `memory` | Storage backend (`memory` or `postgres`) |
| `GATEWAY_SEMANTIC_CACHE_TTL` | `24h` | Entry TTL; expired entries are evicted on next lookup |
| `GATEWAY_SEMANTIC_CACHE_MIN_SIMILARITY` | `0.92` | Cosine similarity threshold for a hit (0–1, higher = stricter) |
| `GATEWAY_SEMANTIC_CACHE_MAX_ENTRIES` | `10000` | Memory backend only: max entries before LRU eviction |
| `GATEWAY_SEMANTIC_CACHE_MAX_TEXT_CHARS` | `8000` | Prompt text truncated to this before embedding |
| `GATEWAY_SEMANTIC_CACHE_EMBEDDER` | `local_simple` | Embedder strategy (`local_simple` or `openai_compatible`) |

**Similarity threshold guidance:**

- `0.95+` — very strict; only near-identical phrasings hit. Low hit rate, high precision.
- `0.92` — default; catches common rephrasings of the same question.
- `0.85–0.90` — aggressive; may return responses for loosely related prompts. Inspect hits in traces before lowering here.
- Below `0.85` — not recommended without careful workload-specific validation.

## Admin endpoints

Two admin-gated endpoints expose live cache state without needing access to traces or metrics.

### `GET /admin/semantic-cache`

Returns configuration and a live non-expired entry count:

```json
{
  "object": "semantic_cache_status",
  "data": {
    "checked_at": "2026-04-29T01:00:00.123Z",
    "configured": true,
    "enabled": true,
    "backend": "memory",
    "entries": 42,
    "max_entries": 10000,
    "default_ttl_sec": 86400,
    "min_similarity": 0.92,
    "max_text_chars": 8000
  }
}
```

`configured: false` when the cache is not wired (disabled in config). All numeric fields are zero in that case.

### `GET /admin/semantic-cache/entries`

Lists cached entries newest-first. Supports `limit` (default `50`, max `500`) and `offset` query parameters for pagination:

```
GET /admin/semantic-cache/entries?limit=50&offset=0
```

```json
{
  "object": "semantic_cache_entries",
  "data": [
    {
      "namespace": "model:gpt-4o-mini|provider:openai|tenant:anonymous",
      "text_snippet": "user: Explain Go channels and goroutines.",
      "stored_at": "2026-04-29T01:00:00Z",
      "expires_at": "2026-04-30T01:00:00Z"
    }
  ]
}
```

`text_snippet` is the first 200 characters of the indexed prompt text. The `namespace` encodes the tenant, provider, and canonical model — three `|`-separated `key:value` pairs. Returns an empty array (not an error) when the cache is unconfigured or empty.

Both endpoints are also surfaced in the operator UI under **Admin → Semantic Cache**.

## Observability

Every semantic cache lookup produces a `gateway.cache.semantic` span. Key attributes:

| Attribute | Meaning |
|---|---|
| `hecate.cache.hit` | `true` on a hit, `false` on a miss |
| `hecate.cache.type` | `"semantic"` |
| `hecate.semantic.strategy` | Embedder name (`local_simple`, `openai_compatible`) |
| `hecate.semantic.index_type` | Storage index type (`memory`, `hnsw`, `ivfflat`) |
| `hecate.semantic.similarity` | Cosine similarity score of the matched entry (hit only) |

The retention worker prunes the semantic cache store under the `semantic_cache` subsystem — see [`telemetry.md`](telemetry.md#retention-spans) and `GATEWAY_RETENTION_SEMANTIC_CACHE_*` in `.env.example`.

## Limitations

- **`local_simple` is not a semantic-quality benchmark.** It works on token overlap, not meaning. Use `openai_compatible` for any workload where prompt phrasing varies meaningfully.
- **Cache hits are optimization hints, not correctness guarantees.** A high-similarity match is not identical to the original prompt. Keep semantic cache behavior visible in traces for latency-sensitive or correctness-critical workloads.
- **No SQLite backend.** See [why](#why-no-sqlite).
- **Memory backend does not survive restarts.** The vector index rebuilds from scratch on process restart; there is no warm-up period.

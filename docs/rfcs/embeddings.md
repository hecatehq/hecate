# Embeddings

> **Status:** design notes. Not implemented. Captures the proposal
> for a provider-routed `/v1/embeddings` endpoint, the optional
> `Embedder` interface on provider adapters, the model-catalog and
> capability-filter changes required to admit embedding-class
> models, and the local-runtime toggle needed to serve embeddings
> from a bundled `llama-server` child.
> **Depends on:** the existing OpenAI-compat router that lives at
> `internal/api/handler.go` (the `HandleChatCompletions` /
> `HandleModels` path) and the provider adapter interface in
> `internal/providers/provider.go`. The embeddings surface
> parallels the chat surface — same routing decisions, same
> credential model, same usage-recording path — but with a
> different request/response shape and a different per-provider
> price.

Hecate today routes only chat-completion traffic. Operators who
want to run a retrieval pipeline (Hecate Chat memory, RAG over
their own docs, gbrain-style external brains) have to bypass the
gateway and call OpenAI / Voyage / Gemini / a local
`llama-server --embeddings` instance directly. That means:

- The operator's API keys travel outside Hecate. Hecate's
  encrypted-at-rest secret store stops being the source of truth
  the moment embeddings enter the picture.
- Embedding usage never appears in the cost dashboard. Operators
  can audit Hecate-routed chat spend and have no signal on the
  parallel embedding spend that's often half their bill.
- Local-model bundling can't help — the llama-server child Hecate
  spawns has no `--embeddings` flag, so even a bundled GGUF won't
  serve embedding queries.
- Every external memory/retrieval tool that points at "an
  OpenAI-compatible base URL" fails the moment it tries the
  embeddings endpoint, even when chat works.

This RFC scopes the work to close that gap with the smallest
possible surface change — one new route, one optional interface
on the adapter, one capability flag, one llama-server arg.

## Goals

In rough priority order:

1. **`POST /v1/embeddings` works end-to-end** for the providers
   that ship embedding models, with the OpenAI request/response
   shape verbatim. Existing tools and SDKs that point at Hecate
   for chat continue to "just work" when they switch to
   embeddings.
2. **Provider-routed**, not provider-specific. Routing decisions
   (which adapter, which credential, which retry policy) reuse
   the chat-side machinery wherever possible.
3. **Usage and cost recorded** through the same `Usage` /
   `CostBreakdown` pipeline chat uses. Operators see embedding
   tokens alongside chat tokens in the dashboard, with the
   provider-specific per-million rate applied.
4. **Local-model parity.** The bundled `llama-server` runtime
   can serve embeddings, gated by a per-model capability flag
   in the registry — embedding-tuned GGUFs (`nomic-embed-text`,
   `bge-small`, etc.) launch with `--embeddings`; chat-tuned
   GGUFs don't.
5. **Capability-aware model listing.** `/v1/models` continues to
   reflect what each provider can do, but the existing
   embedding-class filter in `internal/modelcaps/modelcaps.go`
   stops *dropping* embedding models — they pass through with
   `capability: embedding` instead.

## Non-goals (v1)

- **Multimodal embeddings** (Voyage's `voyage-multimodal-3`,
  Cohere multimodal). The wire shape is similar but the request
  body has image parts; defer until text embeddings prove out.
- **Reranking** (`/v1/rerank`). Different endpoint, different
  response shape, only Voyage and Cohere ship it. Parking lot.
- **Embedding-time dimensionality reduction beyond what the
  provider exposes natively.** Matryoshka via the OpenAI
  `dimensions` field is supported because the wire-level field
  exists; we don't build our own PCA layer.
- **Provider failover for embeddings.** Embeddings indexed with
  one model are not portable to another — silently failing over
  would corrupt the operator's vector store. Surface the upstream
  error; let the operator decide.
- **Caching at the gateway.** Embeddings are pure functions of
  the input, and content-addressed caching would be a real win,
  but it touches request hashing, eviction policy, and storage
  sizing — material enough to be its own RFC.
- **Streaming.** Embeddings APIs don't stream; the response is a
  single JSON envelope. No SSE path.

## Constraints

- **OpenAI wire shape is the contract.** Requests:
  `{model, input, dimensions?, user?, encoding_format?}`. Input
  is either a string, an array of strings, or an array of int
  arrays (pre-tokenized — rare; punt to a not-supported error).
  Responses:
  `{object: "list", data: [{object, index, embedding}], model,
  usage: {prompt_tokens, total_tokens}}`. Anything in
  `encoding_format=base64` mode returns base64-encoded float
  arrays — supported because some SDKs default to it.
- **No vector-space mixing across providers.** A vector from
  `text-embedding-3-large@1536` is meaningless in `voyage-3-large`
  space. The router MUST NOT silently pick a different provider
  than the one the operator named. Failover behavior is
  off-by-default and out of scope for v1.
- **Anthropic, DeepSeek, Groq, Perplexity have no embeddings
  API.** Their adapters MUST NOT implement `Embedder`. The
  router returns `400 model_unsupported_capability` when an
  operator asks for embeddings against one of their models.
- **Same credential model as chat.** Reuse the existing
  `ProviderSecret` storage and the per-provider `Validator` and
  `CredentialReporter` interfaces. No new secret slots.
- **Same usage-recording path as chat.** `Usage{PromptTokens,
  TotalTokens, CompletionTokens=0}` flows through the same
  `internal/usage` aggregator and the same `internal/cost`
  pricer; price-book entries grow a per-provider per-model
  `embeddings_per_million_usd` column.

## API surface

Public, OpenAI-compatible:

| Method | Path | Body shape |
|---|---|---|
| `POST` | `/v1/embeddings` | `{model, input, dimensions?, user?, encoding_format?}` |
| `GET`  | `/v1/models` | unchanged, but each model now carries a `capability` field — `chat` (default) or `embedding` |

Hecate-native (operator-facing):

| Method | Path | Notes |
|---|---|---|
| `GET` | `/hecate/v1/providers/{id}/embedding-models` | Lists the provider's embedding-class models, with default dimensions and per-million-token price. Drives the operator UI's "Default embedding model" picker. |

No new endpoints for cost or usage — the existing aggregators
get embedding rows for free once the recording path is wired.

### Request validation

- `model` is required. Lookup uses the same `(provider, model)`
  resolution as `/v1/chat/completions`.
- `input` accepts string or `[]string`. Pre-tokenized inputs
  (`[][]int`) return `400 input_format_unsupported`.
- `dimensions` is forwarded verbatim to the upstream provider.
  We don't validate the value here — the provider rejects
  invalid dims with its own error and that error surfaces to
  the operator.
- `encoding_format` is `float` or `base64`. Default `float`.
- Model resolution rejects `capability != embedding` models
  with `400 model_unsupported_capability`. Same error code
  fires when the resolved model belongs to an adapter that
  doesn't implement `Embedder`.

### Response shape

OpenAI-compatible verbatim:

```json
{
  "object": "list",
  "data": [
    { "object": "embedding", "index": 0, "embedding": [0.1, 0.2, ...] }
  ],
  "model": "text-embedding-3-small",
  "usage": { "prompt_tokens": 12, "total_tokens": 12 }
}
```

Token counts come from the upstream provider response when
available, else from a count of the input bytes divided by 4
(rough heuristic, documented behavior, never used for billing —
billing uses upstream-reported tokens or zero).

## Provider adapter shape

A new optional interface in `internal/providers/provider.go`,
parallel to `Streamer`:

```go
// Embedder is an optional interface providers may implement to
// support OpenAI-compat embeddings. Adapters that don't implement
// it surface as `capability: chat` only; the router returns
// 400 model_unsupported_capability for embedding requests against
// their models.
type Embedder interface {
    Embed(ctx context.Context, req types.EmbeddingRequest) (*types.EmbeddingResponse, error)
}
```

New request/response types in `pkg/types/`:

```go
type EmbeddingRequest struct {
    Model          string
    Input          []string // canonicalized — single-string requests become a 1-element slice at the API layer
    Dimensions     *int
    EncodingFormat string   // "float" (default) or "base64"
    User           string
}

type EmbeddingResponse struct {
    Model      string
    Embeddings [][]float32 // index-aligned with EmbeddingRequest.Input
    Usage      Usage       // PromptTokens + TotalTokens; CompletionTokens always 0
}
```

Adapters implement the OpenAI wire call in their own translation
layer. For providers that already use the OpenAI shape verbatim
(OpenAI itself, Together, DeepSeek-OpenAI-compat, any LiteLLM
proxy in front of anything else), `openai.go`'s implementation
is essentially a renamed copy of the existing chat path with the
endpoint string changed. For native shapes (Voyage, Gemini), the
adapter does the JSON translation in both directions.

### Provider coverage matrix (v1)

| Provider | Embeddings? | Adapter path | Default model | Native dims |
|---|---|---|---|---|
| OpenAI | yes | `internal/providers/openai.go` | `text-embedding-3-small` | 1536 |
| Voyage AI | yes | new `internal/providers/voyage.go` | `voyage-3` | 1024 |
| Google Gemini | yes | extend Gemini adapter | `text-embedding-004` | 768 |
| Azure OpenAI | yes | extend OpenAI adapter (same shape, different base URL) | deployment-named | 1536 |
| llama.cpp `llama-server` | yes (local) | extend local-models runtime | per-installed-model | per-installed-model |
| Anthropic | no | no `Embedder` impl | — | — |
| DeepSeek | no | no `Embedder` impl | — | — |
| Groq | no | no `Embedder` impl | — | — |
| Perplexity | no | no `Embedder` impl | — | — |

Voyage is a new adapter. Justified because Voyage is what
Anthropic's docs recommend for Claude RAG workflows, and Hecate
operators running Anthropic chat will overwhelmingly want Voyage
for the embeddings half of the same pipeline.

## Model catalog / capability filter

`internal/modelcaps/modelcaps.go:76` currently *drops* models
whose key contains `embedding`. That filter moves from "drop"
to "tag":

- Existing chat-class models keep `capability: chat` (default,
  unchanged for callers).
- Models matched by the embedding-name heuristic get
  `capability: embedding` and pass through `/v1/models`.
- The same key list extends to known reranker names but those
  are filtered out (we don't ship a reranker route in v1).

The capability tag rides through `pkg/types.ModelCapability`,
flows into the `/v1/models` list response, and gates the
embeddings router's resolution step. The model-capability
override table (operator can flip `capability: chat` ↔
`capability: embedding` for unrecognized custom models) lands
alongside, since "is this a chat or embedding model" is now
operator-visible state.

## Local models (`llama-server`)

`internal/llamacpp/runtime_process.go` spawns the child with:

```
llama-server -m <model> --host 127.0.0.1 --port <p> -c <ctx>
```

For embedding-class models, append `--embeddings`. The flag is
chosen by a per-model capability bit in
`controlplane.InstalledModel.Capabilities.Embeddings` (new
field, defaults to false for backwards compatibility). The
operator sets the flag at install time:

- Curated catalog entries for embedding GGUFs ship with
  `embeddings: true` baked in (`nomic-embed-text`,
  `bge-small-en-v1.5`, etc.).
- Paste-URL installs default to `embeddings: false`. Operator
  can flip the flag in the model row's settings if they know
  their GGUF is an embedding model.

The proxy doesn't change. `llama-server` with `--embeddings`
exposes `POST /v1/embeddings` on the same loopback port that
`POST /v1/chat/completions` already lives on, so the
existing reverse-proxy passes through transparently. The model
peek (in `internal/llamacpp/proxy.go`) is unchanged — embedding
requests carry a `model` field too.

A given child can serve EITHER chat or embeddings, not both.
Mixing those in one process is possible but rare in practice
(most embedding GGUFs aren't chat-tuned and vice versa).
Operators run two children if they need both.

## Billing & cost-tracking

Price-book extension:

- `internal/cost/pricebook.json` grows a per-provider per-model
  `embeddings_per_million_usd` field. Unset → cost = 0 (free /
  local / unknown), same as the chat fallback.
- `Usage{PromptTokens=N, TotalTokens=N, CompletionTokens=0}` is
  the canonical shape; the cost aggregator treats
  `CompletionTokens=0 && CapabilityKind=embedding` as an
  embedding row and applies the embedding rate.

UI surfaces:

- The Cost dashboard's per-model breakdown gets a `kind` column
  with `chat` / `embedding` values.
- The Connections / Providers card surfaces the configured
  default embedding model alongside the default chat model when
  the provider implements `Embedder`.

## Phasing

1. **v1.0** — adapter interface, OpenAI adapter, `/v1/embeddings`
   route, capability filter, basic tests. Single provider, ship
   the framework.
2. **v1.1** — Voyage adapter (new file), Gemini adapter (extend
   existing).
3. **v1.2** — local-model integration: per-model `embeddings`
   capability bit, runtime args toggle, curated catalog entries
   for `nomic-embed-text` + `bge-small`.
4. **v1.3** — cost-pricing, dashboard surface, operator-facing
   `/hecate/v1/providers/{id}/embedding-models` listing.
5. **v2** (deferred) — reranking, multimodal embeddings, caching,
   failover.

Each phase is independently mergeable; the v1.0 shape is the
contract everyone downstream depends on.

## Open questions

- **Tokenization fallback when the provider doesn't report
  tokens.** Some local models report `prompt_tokens=0`. Do we
  estimate via tiktoken-style heuristic (and risk drift) or
  pass through and trust the upstream count even when 0? Lean
  toward pass-through; billing for zero is correct for local
  models.
- **Voyage adapter ownership.** Voyage's API is OpenAI-shaped but
  with proprietary fields (`input_type` for query vs document,
  reranker pairing). New adapter or extend OpenAI? Lean toward
  new adapter — the `input_type` field has retrieval-quality
  implications that we want to expose intentionally, not
  shoehorn into a config parameter on a shared adapter.
- **Should `/v1/models` filter by capability?** Today
  `/v1/models` returns everything. Once embedding models are
  there, some downstream tools that walk the list expecting
  chat models will choke. Add `?capability=chat` query param
  (default `chat` for backwards compatibility); operators who
  want everything pass `?capability=*`.
- **Encoding-format pass-through correctness.** OpenAI's
  `encoding_format=base64` returns each vector as a base64
  string of little-endian float32 bytes. If we decode at the
  adapter boundary into `[]float32` and the operator asked for
  base64 in their original request, we'd re-encode wastefully.
  Worth threading the requested format through to the response
  builder.
- **Should the embeddings router log inputs?** Inputs are often
  PII-bearing (raw user text, code, emails). Log-level rules
  same as chat: structured event names with input length, but
  not input contents.

## Risks

- **Vector-space corruption from accidental failover.** Mitigated
  by failover-off-by-default. If failover lands in v2, the
  failover target MUST be a model-of-record alias in the
  registry, not a heuristic match.
- **Embedding-class model heuristic drift.** Today's filter
  matches on `"embedding"` substring. New providers ship models
  with names that don't contain the word (Voyage's
  `voyage-multilingual-2`). The capability tag needs an explicit
  per-provider override list rather than a single regex.
- **Local-model OOM on dual-resident chat + embedding children.**
  LRU cap is per-model, not per-kind. Operator running both a
  20GB chat model and a 4GB embedding model needs
  `HECATE_LOCAL_MODELS_MAX_RESIDENT >= 2`. Documented but not
  enforced.
- **Price-book drift for new providers.** Voyage and Gemini
  update their per-million prices independently; the price-book
  is committed code, not a fetched table. Stale pricing produces
  misleading cost dashboards. Acceptable; document the freshness
  policy in `docs/cost.md`.

## Acceptance criteria

- `POST /v1/embeddings` with `{model: "text-embedding-3-small",
  input: "hello world"}` returns an OpenAI-compatible
  `data: [{embedding: [...]}]` envelope when the operator has
  an OpenAI provider configured.
- `POST /v1/embeddings` with `{model: "claude-sonnet-4-5", ...}`
  returns `400 model_unsupported_capability` with a stable error
  code.
- `POST /v1/embeddings` with `{input: ["a", "b", "c"]}` returns
  three embeddings in index order, with `usage.prompt_tokens`
  summing across all three inputs.
- The Cost dashboard's per-model breakdown shows an `embedding`
  row distinct from the `chat` row for the same provider,
  priced at the provider's embeddings rate.
- A bundled `llama-server` child launched with
  `embeddings: true` answers `/v1/embeddings` through the local
  proxy at `/hecate/internal/llamacpp/v1/embeddings`.
- A bundled `llama-server` child launched with
  `embeddings: false` returns the llama-server's own 501 for
  embedding requests (no Hecate-side error mapping needed).
- `/v1/models?capability=embedding` returns only embedding-class
  models; `?capability=chat` returns the existing list;
  `?capability=*` returns everything.

## Cross-reference

- Provider adapter recipe: `internal/providers/AGENTS.md` — the
  seven-step "add a wire field" chain extends to "add an
  Embedder method." Update that recipe alongside v1.0.
- gbrain integration note: once v1.3 lands, add
  `docs/integrations/gbrain.md` documenting how to point gbrain
  at Hecate via `OPENAI_BASE_URL` / `LITELLM_BASE_URL`.
- Local-models RFC: `docs/rfcs/local-models-llamacpp.md` — the
  per-model `embeddings` capability bit is a forward-compatible
  extension of the v2 catalog shape; documents the wire format
  change.

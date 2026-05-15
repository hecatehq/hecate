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
   reflect what each provider can do, with embedding models
   tagged via a new `kind: "embedding"` field on each entry's
   `metadata.capabilities` block. The existing
   `internal/modelcaps/modelcaps.go` heuristic — which currently
   marks embedding-named models with `tool_calling: "none"` but
   doesn't distinguish them as a different kind — extends to
   set `kind: "embedding"` on the same rows.

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
  usage: {prompt_tokens, total_tokens}}`. v1.0 ships only
  `encoding_format=float` — see "Encoding format" below.
- **Pinned routing — exact provider/model resolution, no
  failover.** Hecate's chat router today supports default-route
  fallback (`provider_default_model_failover`) and health-driven
  failover. Both are explicitly disabled for embeddings. The
  contract: the resolved `(provider, model)` pair from the
  incoming request is the only target. If that provider is
  unhealthy, the request fails with the upstream error — Hecate
  never substitutes a different provider, because a vector from
  `text-embedding-3-large@1536` is meaningless in
  `voyage-3-large` space and silent failover would corrupt
  downstream vector stores. Routing semantics are codified in
  "Routing semantics" below.
- **Anthropic, DeepSeek, Groq, Perplexity have no embeddings
  API.** Their adapters MUST NOT implement `Embedder`. The
  router returns `400 model_unsupported_capability` when an
  operator asks for embeddings against one of their models.
- **Same credential model as chat.** Reuse the existing
  `ProviderSecret` storage and the per-provider `Validator` and
  `CredentialReporter` interfaces. No new secret slots.
- **Same usage-recording path as chat.** Each embedding response
  emits a `governor.UsageEvent` with `Usage{PromptTokens,
  TotalTokens, CompletionTokens=0}` and `CostMicros` pre-computed
  by the adapter (same shape chat uses today). No new
  operator-editable pricing surface — embedding cost metadata lives
  alongside chat cost metadata inside each adapter. See "Usage and
  cost recording" below.

## API surface

Public, OpenAI-compatible:

| Method | Path | Body shape |
|---|---|---|
| `POST` | `/v1/embeddings` | `{model, input, dimensions?, user?, encoding_format?}` |
| `GET`  | `/v1/models` | unchanged shape; each model's `metadata.capabilities` block grows a new `kind` field (`"chat"` default, `"embedding"` for embedding-class models). See "Model catalog / capability shape" below. |

No new Hecate-native endpoints — the operator UI's embedding-model
picker reads from `/v1/models` filtered client-side on
`metadata.capabilities.kind == "embedding"`.

### Routing semantics

The embeddings router parallels the chat router but with strict
constraints:

1. **Exact resolution.** Given an inbound `model` string, the
   router resolves a single `(provider, model)` pair by exact
   match against the model registry. If the operator's request
   includes provider hinting via `metadata.provider` (already
   used by the chat path), that hint is honored verbatim.
2. **No default-model fallback.** The chat router's
   `provider_default_model_failover` path — where an unknown
   model falls through to the provider's configured default —
   does NOT apply. Embeddings with an unknown model id return
   `404 model_not_configured` with a stable error code.
3. **No health-based provider failover.** If the resolved
   provider's health is degraded or its circuit is open, the
   request fails with the upstream error code (mapped from
   `provider.health` state). Hecate does not route to a
   different provider, even one that shares the same model
   name.
4. **Capability gate.** If the resolved provider's adapter
   doesn't implement `Embedder`, the request returns
   `400 model_unsupported_capability` before any upstream call.

These constraints land as a separate `routeEmbedding` function
in `internal/router/`, not as a flag on the existing chat router
— sharing the same router and adding "off switches" for the
embedding path would risk regression to chat-side failover when
a future change lands.

### Request validation

- `model` is required. Unknown model id → `404 model_not_configured`.
- `input` accepts string or `[]string`. Pre-tokenized inputs
  (`[][]int`) return `400 input_format_unsupported`.
- `dimensions` is forwarded verbatim to the upstream provider.
  We don't validate the value here — the provider rejects
  invalid dims with its own error and that error surfaces to
  the operator.
- `encoding_format` accepts the string `"float"` only in v1.0.
  See "Encoding format" below.
- Resolved model whose adapter doesn't implement `Embedder`
  returns `400 model_unsupported_capability`. Same error code
  fires when the resolved model has
  `metadata.capabilities.kind != "embedding"` in the registry.

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

### Encoding format

v1.0 ships `encoding_format=float` only. Requests with
`encoding_format=base64` (the secondary OpenAI mode some SDKs
default to) return `400 invalid_request` with a clear message
pointing at the `float` mode.

Rationale: base64 round-tripping requires either (a) preserving
the raw upstream bytes from the adapter all the way to the
response builder, or (b) decoding to `[]float32` and re-encoding
on the way out — wasteful, and the choice of which to do depends
on whether the upstream itself shipped base64. The wire-shape
contract is cleaner if the internal adapter type is uniformly
`[][]float32` and base64 lands as a follow-up in v1.1 once the
"preserve the upstream bytes" path is plumbed end-to-end.

v1.1 (deferred — see Phasing) revisits this: either widen
`EmbeddingResponse.Embeddings` to `[]EmbeddingVector` where the
vector struct carries both the float slice and an optional raw
byte buffer, or split the adapter return into two methods.

## Provider adapter shape

A new optional interface in `internal/providers/provider.go`,
parallel to `Streamer`:

```go
// Embedder is an optional interface providers may implement to
// support OpenAI-compat embeddings. The embeddings router
// returns 400 model_unsupported_capability when the resolved
// provider doesn't implement Embedder, or when the resolved
// model's metadata.capabilities.kind is anything other than
// "embedding".
type Embedder interface {
    Embed(ctx context.Context, req types.EmbeddingRequest) (*types.EmbeddingResponse, error)
}
```

New request/response types in `pkg/types/`:

```go
type EmbeddingRequest struct {
    Model      string
    Input      []string // canonicalized — single-string requests become a 1-element slice at the API layer
    Dimensions *int
    User       string
}

type EmbeddingResponse struct {
    Model      string
    Embeddings [][]float32 // index-aligned with EmbeddingRequest.Input
    Usage      Usage       // PromptTokens + TotalTokens; CompletionTokens always 0
}
```

`EncodingFormat` is intentionally not on the request type for
v1.0 because only `float` is supported (see "Encoding format"
above). v1.1 introduces it.

Adapters implement the OpenAI wire call in their own translation
layer. For providers that already use the OpenAI shape verbatim
(OpenAI itself, Together, DeepSeek-OpenAI-compat, an
OpenAI-compatible base URL behind any proxy), `openai.go`'s
implementation is essentially a renamed copy of the existing
chat path with the endpoint string changed. For native shapes
(Voyage, Gemini), the adapter does the JSON translation in both
directions.

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

## Model catalog / capability shape

`internal/modelcaps/modelcaps.go:76` currently sets
`tool_calling: "none"` for models whose key contains
`"embedding"` or `"whisper"` — but it does not otherwise mark
them as a different kind of model. They flow through
`/v1/models` exactly like chat models, just with tools disabled.
The chat router would happily try to call them, and the only
reason it doesn't is that provider adapters fail at upstream
call time.

This RFC extends `pkg/types.ModelCapabilities` with a new field:

```go
type ModelCapabilities struct {
    Kind             string `json:"kind,omitempty"` // "chat" (default) or "embedding"; new field, additive
    ToolCalling      string `json:"tool_calling,omitempty"`
    Streaming        bool   `json:"streaming,omitempty"`
    MaxContextTokens int    `json:"max_context_tokens,omitempty"`
    Source           string `json:"source,omitempty"`
}
```

The field is additive, omitempty-tagged, and defaults to `"chat"`
when absent — every existing consumer keeps working. The
`/v1/models` response surfaces it under
`metadata.capabilities.kind` (same path operators already use
for `tool_calling`), so no new top-level field on the model
envelope.

`modelcaps.go` updates: the same name-substring branch that
currently sets `ToolCalling = ToolCallingNone` also sets
`Kind = "embedding"`. The set of recognized substrings stays
identical for v1.0 (`"embedding"`, `"whisper"` — though whisper
is audio-transcription, not embedding; it stays under
`tool_calling: none` but with `kind: "chat"` since it returns
chat-shaped responses). New providers that ship embedding
models whose names don't match the heuristic (Voyage's
`voyage-multilingual-2`) get explicit per-provider lookup in
the adapter; the heuristic is the fallback, not the source of
truth.

The model-capability override table (already operator-visible
via `/hecate/v1/model-capabilities/overrides`) extends to allow
flipping `kind` for custom local models that the heuristic
mis-classifies.

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

## Usage and cost recording

Current builds keep append-only usage events only. Cost numbers are computed
by each provider adapter inline and attached to the response as
`CostMicrosUSD`; the operator UI's Usage view reads them per-event from
`governor.UsageEvent` rows.

For embeddings, the same shape applies:

- Each `Embedder.Embed` implementation computes `CostMicros`
  per request — the per-million-token rate lives next to the
  adapter's chat-side pricing helper (no central config file
  to update). For local providers (llama-server), `CostMicros`
  is always 0.
- The handler emits a `governor.UsageEvent` after each
  successful embedding response, exactly as the chat handler
  does. `Usage.PromptTokens` carries the embedding token
  count; `Usage.CompletionTokens` is always 0; the event's
  `Model` field carries the resolved embedding model id so
  the operator's Usage view can group by it.
- The `governor.UsageEvent` shape doesn't change. The Usage
  view groups rows by `Model`; embedding rows are
  distinguishable from chat rows by the model id alone
  (operators already see `text-embedding-3-small` rows in the
  same list as `gpt-4o`). No new column is needed for v1.0.

UI surface (v1.3): the Usage view already filters by model name
prefix. Adding a "kind" facet — derived from
`metadata.capabilities.kind` on the model — gives operators a
single-click split between embedding and chat spend without a
schema change. Out of scope for v1.0; mentioned here so the
phasing doesn't lose track of it.

## Phasing

1. **v1.0** — adapter interface, OpenAI adapter, `/v1/embeddings`
   route, `routeEmbedding` with pinned/no-failover semantics,
   `ModelCapabilities.Kind` field + `modelcaps.go` update,
   per-event `governor.UsageEvent` recording, basic tests.
   Single provider, ship the framework. `encoding_format=float`
   only.
2. **v1.1** — Voyage adapter (new file), Gemini adapter (extend
   existing), `encoding_format=base64` (preserves upstream bytes
   end-to-end).
3. **v1.2** — local-model integration: per-`InstalledModel`
   `embeddings` capability bit, runtime args toggle, curated
   catalog entries for `nomic-embed-text` + `bge-small`.
4. **v1.3** — Usage view "kind" facet for split-by-embedding-vs-chat
   filtering. Operator UI's "Default embedding model" picker on
   the Providers card.
5. **v2** (deferred) — reranking, multimodal embeddings, caching.
   Failover stays permanently off for embeddings; it is NOT a
   future phase.

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
- **Should `/v1/models` filter by `metadata.capabilities.kind`?**
  Today `/v1/models` returns everything and downstream tools
  walk the list. Adding embedding models to the list MAY surprise
  tools that assume chat-only. Two options: (a) no filter, tools
  inspect `metadata.capabilities.kind` themselves (zero risk of
  backwards-incompat); (b) add a `?kind=chat` query param,
  default unfiltered. Lean (a) — the metadata is already there
  and the convention is "filter client-side."
- **Should the embeddings router log inputs?** Inputs are often
  PII-bearing (raw user text, code, emails). Log-level rules
  same as chat: structured event names with input length, but
  not input contents.

## Risks

- **Vector-space corruption from accidental failover.** Mitigated
  by `routeEmbedding` being a separate function from the chat
  router with no failover branches. The Risks list does not
  re-open this — failover stays permanently off, not "off by
  default."
- **Embedding-class model heuristic drift.** Today's filter
  matches on `"embedding"` substring. New providers ship models
  with names that don't contain the word (Voyage's
  `voyage-multilingual-2`). The `kind` field needs an explicit
  per-provider override list inside each adapter; the substring
  heuristic in `modelcaps.go` is the unknown-provider fallback,
  not the source of truth for known providers.
- **Local-model OOM on dual-resident chat + embedding children.**
  LRU cap is per-model, not per-kind. Operator running both a
  20GB chat model and a 4GB embedding model needs
  `HECATE_LOCAL_MODELS_MAX_RESIDENT >= 2`. Documented but not
  enforced.
- **Per-adapter pricing drift.** Each adapter owns its own
  per-million-token rates. Voyage and Gemini update their prices
  independently. Stale rates produce misleading Usage events.
  Acceptable; freshness policy is the same one chat-side pricing
  already follows.

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
- The Usage view shows a row for each embedding call with the
  embedding model id (e.g. `text-embedding-3-small`),
  `CompletionTokens=0`, and a positive `CostMicros` matching the
  provider's per-million-token rate.
- A bundled `llama-server` child launched with
  `embeddings: true` answers `/v1/embeddings` through the local
  proxy at `/hecate/internal/llamacpp/v1/embeddings`.
- A bundled `llama-server` child launched with
  `embeddings: false` returns the llama-server's own 501 for
  embedding requests (no Hecate-side error mapping needed).
- `/v1/models` includes embedding-class models with
  `metadata.capabilities.kind == "embedding"`; chat-class models
  carry `kind` empty or `"chat"`.
- A request to `/v1/embeddings` with a degraded primary
  provider returns the upstream-mapped error (e.g.
  `provider_unhealthy`), NOT a successful response from a
  different provider — even if a different configured provider
  could serve the same model name.

## Cross-reference

- Provider adapter recipe: `internal/providers/AGENTS.md` — the
  seven-step "add a wire field" chain extends to "add an
  Embedder method." Update that recipe alongside v1.0.
- gbrain integration note: once v1.3 lands, add
  `docs/integrations/gbrain.md` documenting how to point gbrain
  at Hecate via the OpenAI-compatible base URL (`OPENAI_BASE_URL`).
- Local-models RFC: `docs/rfcs/local-models-llamacpp.md` — the
  per-model `embeddings` capability bit is a forward-compatible
  extension of the v2 catalog shape; documents the wire format
  change.

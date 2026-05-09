# Provider Response Extensions

> **Status:** design notes. Not implemented. Architecture and field
> shapes captured here before commitment so a future implementer can
> pick this up without re-deriving the trade-offs.
> **Depends on:** nothing. The api↔providers parallel-struct
> convention in [`docs-ai/skills/providers/SKILL.md`](../../docs-ai/skills/providers/SKILL.md)
> is referenced throughout.

Several OpenAI-compatible providers Hecate routes to return fields
beyond the OpenAI standard chat-completion shape. Today
`internal/providers/openai.go`'s response struct decodes only the
stock OpenAI fields, so vendor extras are silently dropped on the
floor before reaching the api layer or the operator UI.

Listed in [`docs/known-limitations.md`](../known-limitations.md) under
"Provider Lifecycle":

> Provider-specific response extensions are not all preserved yet.
> For example, Perplexity's `citations` and `search_results` fields
> are currently consumed by the upstream adapter but not forwarded
> through Hecate's normalized chat response.

Same gap applies to:

- DeepSeek (R1, V3): `reasoning_content` per message; cache hit/miss
  tokens distinct from OpenAI's `cached_tokens`.
- xAI (Grok-3 Mini, Grok-4): `reasoning_content` per message; Live
  Search variants return citations alongside choices.
- Gemini (OpenAI shim): `safety_ratings` per choice; sometimes
  `citation_metadata`.
- OpenAI (o1, o3): `completion_tokens_details.reasoning_tokens`
  inside usage. This one is now stock OpenAI rather than a vendor
  extension and should land in the base response struct.

## Why not just add fields to the OpenAI structs

The OpenAI-compat adapter (`internal/providers/openai.go`) is shared
across ~10 providers. Adding citation / reasoning fields to the
adapter's response struct as "the Perplexity / DeepSeek / xAI ones"
pollutes a deliberately vendor-neutral layer. Once one vendor extra
lands inline, the next four follow, and the struct stops describing
"what OpenAI returns" and starts describing "the union of every
preset's wire shape."

The api↔providers parallel-struct convention exists specifically to
keep these layers from accreting that kind of vendor-coupled state.

## Architecture

A `ResponseExtensionDecoder` interface; one implementation per
provider preset that has extensions; nil-safe registration in the
shared OpenAI-compat adapter.

```go
// internal/providers/extensions.go
type ResponseExtensionDecoder interface {
    Decode(raw json.RawMessage, resp *types.ChatResponse) error
}
```

Contract for implementations:

- Run AFTER the stock OpenAI decoder. They MUST only fill fields the
  stock decoder didn't set, never overwrite them. (One documented
  exception: DeepSeek's `prompt_cache_hit_tokens` may populate
  `Usage.CachedPromptTokens` when the stock `cached_tokens` field
  was zero — DeepSeek doesn't return both.)
- Tolerate missing/null fields silently. Vendors return different
  subsets per model and per request; a missing `citations` array on
  a non-Sonar Perplexity model is not an error.
- Pure JSON → struct. No outbound calls, no logging side effects.

Registry in `internal/providers/extensions_registry.go`:

```go
func decoderFor(presetID string) ResponseExtensionDecoder {
    switch strings.ToLower(strings.TrimSpace(presetID)) {
    case "perplexity":
        return perplexityResponseDecoder{}
    case "deepseek":
        return deepseekResponseDecoder{}
    case "xai":
        return xaiResponseDecoder{}
    case "gemini":
        return geminiResponseDecoder{}
    }
    return nil // stock OpenAI + everything else → no extra decode
}
```

`OpenAICompatibleProvider` constructor calls `decoderFor` once and
stores the result (nil-safe). Both non-streaming `Chat` and streaming
`ChatStream` invoke `decoder.Decode(rawBody, &resp)` after the stock
decode if non-nil.

## Generic types

Concept-named, not vendor-named. Three providers populate citations
today; more will tomorrow.

```go
// pkg/types/chat.go (mirrored in internal/api/openai.go per the
// parallel-struct convention; all omitempty on the wire)

// On ChatMessage:
ReasoningContent string

// On Usage:
ReasoningTokens int

// On ChatResponse:
Citations     []string
SearchResults []SearchResult

type SearchResult struct {
    Title string
    URL   string
    Date  string
}
```

`omitempty` everywhere so non-extension providers produce zero diff
in their wire output.

## Per-provider decoder catalog

### Perplexity (`internal/providers/perplexity.go`)

- `citations[]` (top-level) → `ChatResponse.Citations`
- `search_results[]{title, url, date}` (top-level) → `ChatResponse.SearchResults`

### DeepSeek (`internal/providers/deepseek.go`)

- `choices[].message.reasoning_content` → `Choice.Message.ReasoningContent`
- `usage.prompt_cache_hit_tokens` → `Usage.CachedPromptTokens`
  (only when stock `cached_tokens` is zero; DeepSeek doesn't return both)

### xAI (`internal/providers/xai.go`)

- `choices[].message.reasoning_content` → `Choice.Message.ReasoningContent`
- `citations[]` (Live Search variants) → `ChatResponse.Citations`

### Gemini (`internal/providers/gemini.go`)

- `choices[].citation_metadata.citation_sources[].uri` → `ChatResponse.Citations`
  (flattened across choices)
- **Skipped:** `safety_ratings`. No clean cross-provider equivalent
  yet; design after a second safety-data provider lands.

### OpenAI: not an extension

`completion_tokens_details.reasoning_tokens` (o1, o3) is now stock
OpenAI. Decoding it as an extension would mean other providers using
o1-shaped models miss it. Add `CompletionTokensDetails` to the base
`openAIUsage` struct mirroring `PromptTokensDetails`. One-line change,
applies to all providers.

## End-to-end scope

The provider-side extension layer alone makes the data available at
`/v1/chat/completions`. Persisting it across sessions and rendering
it in the operator UI is additional work that depends on it.

| Layer | Files | Lines (rough) |
|---|---|---|
| Provider extensions | `pkg/types/`, `internal/api/`, `internal/providers/` (5 new files) | ~640 |
| Persistence | `internal/chatstate/sqlite.go` (additive migration), memory store parity, api serialization | ~300-400 |
| UI types | `ui/src/types/runtime.ts` | ~30 |
| UI components | `ReasoningBlock.tsx`, `Citations.tsx`, `SearchResults.tsx` + tests | ~600-700 |
| MessageRow + TaskDetail integration | `ChatView`'s `MessageRow.tsx`, possibly `TaskDetail.tsx` | ~100-150 |
| Docs | drop the citations bullet in `known-limitations.md`; add a "Vendor response extensions" section to `docs/providers.md` | ~30 |

Total: ~1700 lines, ~18-20 commits across 4-5 PRs.

### Phasing

Each PR is mergeable on its own; later PRs cleanly stack:

1. **Provider extensions** — interface, registry, four decoders,
   types in `pkg/types` + `internal/api`. API consumers see the new
   fields immediately. No persistence, no UI.
2. **Persistence** — `chatstate` schema + memory/sqlite parity for
   the new fields. Required before the UI can re-render them on chat
   reload.
3. **UI types + components** — runtime.ts additions, standalone
   `ReasoningBlock` / `Citations` / `SearchResults` components with
   tests. Not yet rendered anywhere.
4. **ChatView integration** — wire components into `MessageRow`.
   ChatView only.
5. **(optional) TaskDetail integration** — same components in Task
   transcript. Opens the ChatView ↔ TaskDetail convergence question
   the beta-roadmap defers; do this only if the convergence is also
   on the table.

## Streaming: deferred

The most common bug per
[`docs-ai/skills/providers/SKILL.md`](../../docs-ai/skills/providers/SKILL.md)
is "forgot to plumb a field into the streaming `wireReq`." The mirror
applies for response decoding. Streaming extensions add:

- `delta.reasoning_content` chunks (DeepSeek, xAI) — stream
  incrementally alongside `delta.content`.
- Citations on the terminal chunk (Perplexity, xAI Live Search).
- Need a parallel `StreamingResponseExtensionDecoder` interface or
  a chunk-shaped variant on the same interface.

UX cost of deferring: the first non-streaming version renders
citations / reasoning *after* the stream completes — operators see a
visible "snap-in" at the end of each response. Tolerable in v1 with
a follow-up note in `known-limitations.md`. Not free.

## UI rendering choices (open)

`ReasoningContent` rendering shape needs a design call before we
build the component:

- **(a) Hidden-by-default `<details>` block above content.**
  Matches ChatGPT's "thinking" expand and Claude's "thinking" UI.
  Default unfolded for streaming, folded after completion. Most
  conventional.
- **(b) Muted faded inline text above content.** Always visible,
  italic / dimmed style. Heavier visual weight.
- **(c) On-hover tooltip.** Lightest weight, hardest to read at
  length.

Defaulting to (a) unless someone advocates differently.

`SearchResults` rendering also has a cardinality choice:

- **Full card grid** — title, favicon, URL, date per result. ~150
  lines of component code, looks like Perplexity's own UI.
- **Footnote-style numbered list** — `[1] title — url`. Bare-bones,
  fits in a chat transcript without dominating it.

Defaulting to footnote-style for v1; full card grid as a follow-up
when there's a second consumer.

## Risks

1. **Decoder applies to wrong provider via preset id mismatch** —
   e.g. an operator names their custom OpenAI-compat preset
   `"perplexity"` and inherits the citations decoding. Mitigation:
   exact-string lowercase match in the registry; tests pin the
   mapping. Operators using the OpenAI-compat custom flow with
   arbitrary names hit nil → no extra decode.
2. **Vendors silently change wire shape** — Perplexity could
   rename `citations` to `references`. Decoders would silently
   produce empty results. Mitigation: each decoder test pins the
   wire shape against a real fixture; a future Hecate upgrade
   surfaces "vendor moved" as a failing test rather than a runtime
   regression.
3. **Schema migration** — new columns in `chat_session_messages`
   and the provider-call table. Standard `ensureSessionColumn`
   forward-compat pattern, but it IS a schema change that
   `known-limitations.md` warns operators to back up data before.
4. **Strict OpenAI-client rejection of extra fields** — any client
   strictly validating the response schema and rejecting unknown
   fields would break when proxying Perplexity-backed calls. Real
   OpenAI clients are lenient; if a strict consumer appears, gate
   the extension fields behind an opt-in header.

## Acceptance criteria

When this RFC is implemented end-to-end, the following are true:

- A Perplexity-backed `/v1/chat/completions` response carries
  `citations` and `search_results` end-to-end through the api wire,
  the persisted store, and the operator UI.
- A DeepSeek-backed response carries `reasoning_content` per
  message and a corrected cached-prompt-token count.
- An xAI Grok-3 Mini response carries `reasoning_content`; an xAI
  Live Search response carries `citations`.
- A Gemini response carries `citations` (flattened from
  `citation_metadata`).
- A non-extension provider's response is byte-identical (modulo
  ordering) before and after the change. Verified by an end-to-end
  test against a stock OpenAI fixture.
- `docs/known-limitations.md`'s "Provider-specific response
  extensions" bullet about citations is dropped.

## Open questions

- Streaming-extension interface shape. Probably a separate method
  on the same interface (`DecodeStream(chunk, *types.ChatStreamChunk)`)
  rather than a separate interface; design when streaming is in scope.
- Whether `Usage.ReasoningTokens` should be priced at the same rate
  as completion tokens (current behavior) or split into its own
  pricebook dimension. Today the answer is "treat as completion";
  a separate RFC governs pricebook dimensions.
- Whether to surface the `safety_ratings` Gemini returns. Useful for
  content moderation but no cross-provider equivalent. Park until a
  second safety-rating provider arrives.
- Whether the four decoders should live in a `provider_decoders/`
  subpackage rather than as flat `*.go` files in `internal/providers/`.
  Subpackage is cleaner if the count grows past five; flat is fine
  for now.

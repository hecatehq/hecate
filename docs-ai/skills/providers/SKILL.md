---
name: hecate-providers
description: Use when working in `internal/providers/` — outbound HTTP adapters to LLM upstreams (OpenAI-compat, Anthropic). Owns the api↔providers parallel-struct boundary and the seven-step "add a wire field" chain.
---

# Hecate providers skill

Use this skill for work in `internal/providers/` and any change that crosses the api↔providers boundary. Backend-wide rules are in [`../backend/SKILL.md`](../backend/SKILL.md); field-shape rules in [`../../core/engineering-standards.md`](../../core/engineering-standards.md); the race-suite floor in [`../../core/verification.md`](../../core/verification.md).

## Layout

```
provider.go               Provider / Streamer / Capabilities interfaces
openai.go                 OpenAI-compat adapter (real OpenAI, Together, Groq, Ollama, vLLM)
openai_transcription.go   Explicit OpenAI-compatible audio transcription adapter
anthropic.go              Native Anthropic Messages API adapter
runtime_manager.go        provider catalog + protocol → adapter dispatch
capabilities_cache.go     /v1/models discovery + TTL cache
discovery_policy.go       when to discover, when to use static caps
health.go                 provider health probe + circuit
mutable_registry.go       control-plane mutation surface
```

Test helpers live in `provider_test_helpers_test.go` and `tooluse_test.go` (the `newAnthropicTestProvider` helper is there, not in `anthropic_test.go`).

## The capital/lowercase parallel-struct rule

`internal/api/openai.go` defines `OpenAIChatMessage`, `OpenAIMessageContent`, `OpenAIContentBlock` (capital).
This package defines `openAIChatMessage`, `openAIMessageContent`, `openAIContentBlock` (lowercase).

**Same JSON shape, two packages, intentional.** Keeps `internal/providers/` free of `internal/api/` imports — the wire shapes evolve independently. When you add a field on one side, mirror it on the other.

The polymorphic content type (`UnmarshalJSON` / `MarshalJSON` for string-or-array-or-null) is duplicated for the same reason. Don't try to share — the duplication is the contract. Sharing would re-introduce the import.

## The seven-step chain

Adding a passthrough wire field. This is the most-redone task in the providers package; the chain is canonical and lives here.

> **Plan first.** Wire-field additions cross `pkg/types/`, `internal/api/`, `internal/providers/`, and tests at every layer — exactly the cross-package ripple that triggers a written plan per [`../../core/workflow.md`](../../core/workflow.md). Use the shape in [`../../tasks/planning.md`](../../tasks/planning.md) (problem framing → constraints → recommendation → acceptance criteria → migration notes) before starting on step 1.

1. **`pkg/types/chat.go`** — add the field to `ChatRequest` with a comment explaining the pointer-vs-value choice (see [`../../core/engineering-standards.md`](../../core/engineering-standards.md)).
2. **`internal/api/openai.go`** — add the field to `OpenAIChatCompletionRequest` with `json:"x,omitempty"`.
3. **`internal/api/handler_chat.go`** — copy the field through in `normalizeChatRequest`'s return value.
4. **`internal/providers/openai.go`** — add the field to `openAIChatCompletionRequest` (this package's lowercase parallel struct).
5. **Same file: plumb in BOTH `Chat` and `ChatStream` `wireReq` constructions.** Forgetting one is the most common bug — `ChatStream` builds its own `wireReq` (grep for the second `wireReq :=` in `openai.go`) and is not the same as the non-stream one. Non-stream tests pass; the field silently drops in production for any client using `stream: true`.
6. **`internal/providers/anthropic.go`** — add a case to `warnUnsupportedFieldsDropped` with a hint (the field name, its value, and the right Anthropic-side equivalent — or a note that there is none).
7. **Tests at each layer.**
   - `openai_test.go` — passthrough + `omitempty` (table-driven; see `TestOpenAIProviderForwardsTier2Passthroughs` for the template).
   - `anthropic_test.go` — drop-not-leaked (single test asserting field absent on Anthropic wire).

## Capability cache + tests

Provider tests **must** seed `cachedCaps` or the discovery path will try to call `/v1/models` against the test transport with a nil request body and panic on JSON decode:

```go
provider.cachedCaps = Capabilities{
    Name: "openai", Kind: KindCloud,
    DefaultModel: "gpt-4o-mini",
    Models:       []string{"gpt-4o-mini"},
}
provider.capsExpiry = time.Now().Add(time.Minute)
```

Alternative: the test transport can return an empty 200 for any request that isn't `/v1/chat/completions` (see `TestOpenAIProviderForwardsResponseFormat`). Use whichever fits.

## Cross-provider translation

When a caller's request hits a provider whose protocol doesn't natively support a field:

- **Translatable** (semantic equivalent exists): translate. Examples:
  - OpenAI `tool_choice: "required"` ↔ Anthropic `{"type":"any"}`.
  - OpenAI `image_url` block ↔ Anthropic `image` block with `source`.
- **Not translatable**: log-and-drop with a per-field warning hint, never silently discard.

The Anthropic adapter centralizes warn-and-drop in `warnUnsupportedFieldsDropped` (`anthropic.go`). Each entry names the field, includes the value, and points the operator at the right Anthropic-side equivalent (or notes there is none). Add new dropped fields here, not as scattered per-call warnings.

## Streaming-specific gotchas

- **`translateAnthropicSSE` (`anthropic.go`)** consumes Anthropic SSE and emits OpenAI-format chunks. The `usageSnapshot` accumulator captures input + cache tokens at `message_start` and updates `output_tokens` at every `message_delta`. The final usage chunk uses `anthropicUsageToTypes` to map to OpenAI's flat shape with `prompt_tokens_details.cached_tokens` for cache hits.
- **`translateOpenAIToAnthropicSSE` (in `internal/api/handler_messages.go`)** is the reverse direction. Both directions need to stay in sync when streaming-related fields are added.
- **Streaming error frames are failures, not content or EOF success.** OpenAI `data: {"error":...}` and Anthropic `event: error` frames must return a typed `UpstreamError`. Sanitize both the message and constrained type through `internal/safetext`; never copy provider error frames verbatim into client SSE, logs, traces, health, telemetry, or persistence. Do not rewrite ordinary assistant content.
- **Tool-call streaming** — OpenAI streams `function.arguments` as partial JSON in `delta.tool_calls`; Anthropic streams the same as `input_json_delta`. Both translators handle this — pin tests when modifying.

## Prompt caching (Anthropic-specific)

### Send side — automatic `cache_control` markers

The Anthropic adapter auto-attaches `cache_control: {"type":"ephemeral"}` markers so the static prefix (system prompt + tools catalog) is reused across turns. Two helpers — `applyAnthropicSystemCacheMarker` and `applyAnthropicToolsCacheMarker` in `anthropic.go` — run during request preparation:

- The `system` field's last block and the last entry in `tools` each get a marker, but **only if the tail entry doesn't already carry caller-supplied `cache_control`**. Caller intent wins on the tail; earlier caller markers anywhere else in the same section are preserved unchanged.
- Anthropic caps requests at four `cache_control` breakpoints. The adapter does not currently count or enforce that limit — auto-attach can contribute up to two markers on top of whatever the caller set. A caller already placing three or more markers can exceed the cap and be rejected upstream. If we add cross-provider request-shaping helpers later, this is where to put a breakpoint counter.
- Globally toggleable via `HECATE_PROVIDER_ANTHROPIC_CACHE_ENABLED` (default `true`). Applied uniformly by `Protocol == "anthropic"` regardless of how the provider was added (env, Connections workspace, programmatic). The runtime manager exposes `SetGlobalAnthropicCacheDisabled` for tests.
- Operator-facing doc: [`docs/operator/providers.md`](../../../docs/operator/providers.md#anthropic-prompt-caching). Keep it in sync if you change the helpers' contract.

### Response side — three-bucket usage

`anthropicUsage` captures three buckets:

- `input_tokens` — fresh tokens.
- `cache_read_input_tokens` → `Usage.CachedPromptTokens`.
- `cache_creation_input_tokens` → folded into `Usage.PromptTokens`; this at least counts the tokens for usage reporting — the prior adapter dropped them entirely.

If Hecate later needs adapter-specific cache-cost reporting, split `cache_creation_input_tokens` back into its own `Usage` field. The trade-off is documented in the comment on `anthropicUsage`.

## Audio transcription boundary

Audio transcription is an optional `Transcriber` capability, not a chat model
capability. Advertise it only when `TranscriptionPath` and
`DefaultTranscriptionModel` are both explicit. Built-in defaults currently
exist for OpenAI, Groq, and LocalAI; env-configured OpenAI-compatible providers
may opt in explicitly. Do not infer support from `/v1/models` or from the
provider speaking the OpenAI chat protocol.

`dictationapp` resolves one named provider and carries its opaque
`ProviderInstanceIdentity` to the call. Re-read the registry and compare that
identity immediately before audio disclosure. Transcription path and model are
part of the non-secret configuration fingerprint; credentials are not. There
is no Auto route and no cross-provider failover. Keep audio/transcript bodies
out of logs, traces, metrics, health, usage, and persistence; tests should pin
multipart path/auth/fields and the replacement-before-disclosure fence.

## Common bugs to watch for

- **Forgot to plumb a field into the streaming `wireReq`.** Request works in non-stream tests, drops the field in production for any client using `stream: true`. Step 5 of the seven-step chain — `ChatStream` in `openai.go` builds its own `wireReq` and is not the same as the non-stream one.
- **Capital/lowercase struct mix-up.** Wrote a test against `openAIChatMessage` but built the request using `OpenAIChatMessage`. Compiles in their respective packages; doesn't catch the actual JSON-shape drift.
- **Silently passing through unknown content blocks to Anthropic.** Sending `{"type":"image_url"}` to Anthropic 400s upstream because Anthropic only knows `image`. Always translate or drop, never pass through unknown types.
- **CodeQL CWE-190.** `make([]T, 0, len(x)+N)` is flagged as integer-overflow risk. Use plain `len(x)` and let `append` grow.

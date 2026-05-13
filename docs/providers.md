# Providers

Hecate uses a vendor-neutral provider layer at the runtime boundary. It treats OpenAI-compatible upstreams and the Anthropic Messages API as first-class paths — every other supported model lives behind one of those two protocols.

> Contributing here? Start at [`AGENTS.md`](../AGENTS.md) for the codebase map and runtime invariants; provider-package depth (the seven-step "add a wire field" chain, the api↔providers parallel-struct rule, capability cache seeding, streaming gotchas) lives in [`docs-ai/skills/providers/SKILL.md`](../docs-ai/skills/providers/SKILL.md).

![Connections view — populated table with health, endpoint, credentials, and models columns](screenshots/providers.png)

## Contents

- [Providers vs. clients](#providers-vs-clients)
- [Adding a provider](#adding-a-provider)
- [Built-in presets](#built-in-presets)
- [Env-configured providers](#env-configured-providers)
- [Anthropic prompt caching](#anthropic-prompt-caching)
- [Settings API](#settings-api)
- [Health and circuit breaking](#health-and-circuit-breaking)

## Providers vs. clients

- **Clients** call Hecate. Codex, Claude Code, OpenAI SDKs, Anthropic SDKs, curl scripts, and internal tools are supported as long as they speak Hecate's OpenAI-compatible or Anthropic-compatible gateway endpoints.
- **Connections** are upstream model backends Hecate calls. The gateway ships with a preset catalog of common cloud and local backends, and the operator adds them explicitly via the Connections view.

## Adding a provider

The Connections view starts empty:

![Empty Connections view — Add provider CTA](screenshots/providers-empty.png)

Click **Add provider** to open the modal:

![Add provider modal — Local preset catalog with detected runtime status](screenshots/providers-presets.png)


1. Pick **Cloud** or **Local** at the top.
2. Click a preset (e.g. Anthropic, OpenAI, Ollama) — or click **Custom** to point Hecate at any OpenAI-compatible endpoint.
3. Fill in the form:
   - **Name** is locked to the preset name; Custom lets you choose.
   - **Endpoint URL** is shown for local and custom providers.
   - **API Key** is shown for cloud and custom-cloud providers; stored encrypted at rest with `GATEWAY_CONTROL_PLANE_SECRET_KEY`.
4. Click **Add provider**.

The Local tab also runs a lightweight discovery check before you choose a
preset. Hecate checks whether the expected command is on `PATH` (`ollama`,
`lms`, `llama-server`, `local-ai` / `localai`) and probes each unique local
HTTP endpoint once. The preset cards then show:

- **Running** — the local HTTP API responded; model count is shown when the
  provider returned one.
- **Installed** — the command is available, but the HTTP server is not running
  yet.
- **Not detected** — no command on `PATH` and no response from the default
  endpoint.

`llamacpp` and `localai` share `127.0.0.1:8080` by default, so Hecate sends one
HTTP request and reuses that result for both cards. The signal is advisory:
adding the provider still uses the configured endpoint URL, and routing health
continues to come from the normal `/hecate/v1/providers/status` probes.

A provider you add is immediately routable. There is no separate enable/disable toggle — to take a provider out of rotation, delete it.

![Connections view populated with three providers — Health, Endpoint, Credentials, Models columns](screenshots/providers.png)

### Multiple instances

The same preset can be added more than once by setting a `custom_name` disambiguator. The provider ID is derived from `slugify(name + custom_name)`, so "Anthropic" plus `custom_name=EU` slugs to `anthropic-eu` and coexists with the plain `anthropic` instance. Two providers may not share the same `base_url`; the second add is rejected with HTTP 409 and the existing provider's name in the error message.

### Editing a provider

Click any row in the providers table to open the edit modal:

- **Cloud / custom-cloud** — rotate the API key, or delete it (the provider record stays so model discovery resumes once a new key is saved).
- **Local / custom-local** — change the endpoint URL. Save URL is disabled when the new value matches the current one.

The edit modal also shows live runtime status: health, route readiness, model count, last check time, and a collapsible diagnostics section with the last error, error class, latency, and totals.

### Deleting a provider

Each row has a trash button. Clicking it confirms via a browser dialog and then removes the provider record and its credential.

## Built-in presets

The gateway ships with thirteen provider presets. None of them are auto-added — operators pick from the catalog when adding a provider.

### Cloud presets

| ID | Name | Default base URL |
|---|---|---|
| `anthropic` | Anthropic | `https://api.anthropic.com/v1` |
| `deepseek` | DeepSeek | `https://api.deepseek.com/v1` |
| `gemini` | Google Gemini | `https://generativelanguage.googleapis.com/v1beta/openai` |
| `groq` | Groq | `https://api.groq.com/openai/v1` |
| `mistral` | Mistral | `https://api.mistral.ai/v1` |
| `openai` | OpenAI | `https://api.openai.com/v1` |
| `perplexity` | Perplexity | `https://api.perplexity.ai` |
| `together_ai` | Together AI | `https://api.together.xyz/v1` |
| `xai` | xAI | `https://api.x.ai/v1` |

### Local presets

| ID | Name | Default base URL |
|---|---|---|
| `llamacpp` | llama.cpp | `http://127.0.0.1:8080/v1` |
| `lmstudio` | LM Studio | `http://127.0.0.1:1234/v1` |
| `localai` | LocalAI | `http://127.0.0.1:8080/v1` |
| `ollama` | Ollama | `http://127.0.0.1:11434/v1` |

`llamacpp` and `localai` share the same default port (`127.0.0.1:8080`); only one of them can be added to the gateway at a time, since the base URL conflict is rejected at create time. Operators who run both should change the port on one of them via the Custom flow or `PROVIDER_*_BASE_URL`.

## Env-configured providers

Setting `PROVIDER_<NAME>_API_KEY`, `PROVIDER_<NAME>_BASE_URL`, or `PROVIDER_<NAME>_DEFAULT_MODEL` in the environment seeds the runtime registry so the provider becomes reachable for routing, and is also auto-imported into the persisted Connections view so operators can see and manage it through the UI. On subsequent boots the auto-import skips any provider that already exists in the Connections view, so operator edits made via the UI are never overwritten by environment values.

Env vars are convenient for first-run bootstrapping in `.env` / Docker compose; the Connections view is the source of truth thereafter.

```bash
PROVIDER_ANTHROPIC_API_KEY=sk-ant-...
PROVIDER_OPENAI_API_KEY=sk-...
PROVIDER_OPENAI_DEFAULT_MODEL=gpt-4o-mini
PROVIDER_PERPLEXITY_API_KEY=pplx-...
```

Perplexity's Sonar API is OpenAI Chat Completions-compatible but uses a
provider-specific endpoint layout: Hecate sends chat traffic to
`https://api.perplexity.ai/chat/completions` and model discovery to
`https://api.perplexity.ai/v1/models`. Perplexity-specific response extension
fields such as `citations` and `search_results` are not forwarded yet; the
normalized assistant text, model, and token usage are.

## Anthropic prompt caching

Hecate automatically attaches `cache_control: {"type":"ephemeral"}`
markers to outbound Anthropic Messages API requests so the static
prefix (system prompt + tools catalog) is reused across turns. The
billing impact is asymmetric: the first turn writes the cache and
Anthropic returns those tokens as `cache_creation_input_tokens`
(charged at ~1.25× the base input rate); subsequent turns reuse
the prefix and Anthropic returns them as `cache_read_input_tokens`
(charged at ~0.1× the base rate). The win lands on turn 2 onwards.
Markers are applied to:

- the last block of the `system` field (lifted to a block list
  when the caller sends a string system prompt — `cache_control`
  requires the block shape)
- the last entry in `tools`

Caller intent wins on the tail: when the last block of `system`
or the last entry in `tools` already carries a caller-supplied
`cache_control`, Hecate skips the auto-marker for that section
and leaves the caller's boundary in place. Earlier caller-supplied
markers elsewhere in the same section are always preserved
unchanged, so an operator who has placed their own breakpoints
keeps them either way.

Anthropic accepts up to four `cache_control` breakpoints per
request, and Hecate does not currently count or enforce that
limit — its auto-attach can contribute up to two markers (one
on the `system` tail, one on the `tools` tail) on top of whatever
the caller already set. Callers placing three or more of their
own markers should either leave room for the auto-markers or
place a `cache_control` on the `system` and `tools` tails to
suppress Hecate's auto-attach for those sections.

Reads show up in the response as non-zero
`cache_read_input_tokens`, which Hecate plumbs through as
`Usage.CachedPromptTokens` and prices via the cache-read rate
when the pricebook has one. Cache writes
(`cache_creation_input_tokens`) currently fold into
`Usage.PromptTokens` at the fresh-input rate; the pricebook
doesn't yet have a separate cache-write rate, so first-turn cost
is undercounted by ~20% per cache-write token until that lands.

The behavior is controlled by `GATEWAY_PROVIDER_ANTHROPIC_CACHE_ENABLED`
(default `true`). The toggle is global — every Anthropic-protocol
provider, however it was added (env, Connections view, programmatic),
inherits the same value. Operators flip it to `false` for cost-tier
comparisons or to debug a suspected cache-related issue. The
Anthropic adapter is the only place this knob applies; non-Anthropic
providers are unaffected.

## Settings API

Every UI action maps to a Hecate-native settings endpoint:

- `POST /hecate/v1/settings/providers` — add a provider. Body `{name, kind, protocol, base_url?, api_key?, custom_name?, preset_id?}`.
- `GET /hecate/v1/settings/providers/local-discovery` — probe local presets
  for command presence and default endpoint availability. Used by the Add
  provider modal before a provider is created.
- `DELETE /hecate/v1/settings/providers/{id}` — remove it.
- `PATCH /hecate/v1/settings/providers/{id}` — partial update; accepts `base_url`, `name`, and `custom_name`.
- `PUT /hecate/v1/settings/providers/{id}/api-key` — set the API key (empty `key` clears it).

The full surface lives in [`runtime-api.md`](runtime-api.md) and is implemented in [`internal/api/handler_settings.go`](../internal/api/handler_settings.go). Useful for terraforming a fleet of gateways from a single config source of truth.

## Health and circuit breaking

Each provider has a per-process health tracker. After a configurable threshold of consecutive retryable failures the breaker opens — the router skips that provider and falls over to the next eligible one. After a cooldown, a half-open probe lets a single request through; if it succeeds, the breaker closes and normal traffic resumes. Upstream `429 Too Many Requests` responses cool a provider down immediately so later requests stop hammering a rate-limited backend and can fail over cleanly.

When `GATEWAY_PROVIDER_HEALTH_LATENCY_DEGRADED_THRESHOLD` is set to a positive duration, successful calls that take at-or-above that latency mark the provider `degraded` with health reason `latency` instead of `healthy`. Degraded providers remain routable, but the router scores them behind healthy peers and route diagnostics surface them as `provider_slow` with the last observed latency.

Within the same health tier, the router now also prefers the more stable provider: fewer recent retryable failures, fewer rate limits/timeouts/server errors, then lower observed latency. When a healthy candidate loses on that dimension, route diagnostics surface it as `provider_less_stable` instead of silently dropping it from the route report.

The current snapshot lives at `GET /hecate/v1/providers/status`. A short persisted event history now also lives at `GET /hecate/v1/providers/history`, with optional `provider` and `limit` query params. History rows are operator-facing state transitions such as:

- `success`
- `slow_success`
- `failure`
- `cooldown_opened`
- `cooldown_recovered`
- `failover_triggered`
- `failover_selected`

Each row includes the resulting health status, error class, last observed latency, current failure counters, and correlation fields like `request_id` and `trace_id` so operators can answer whether a provider is transiently failing, rate-limited, just getting slow over time, or repeatedly losing traffic during failover.

Failover rows now also capture:

- `reason` — the failover cause or phase, such as `provider_retry_exhausted`, `preflight_price_missing`, `budget_denied`, `policy_denied`, or `candidate_selected`
- `route_reason` — why that provider/model candidate was in play
- `peer_provider` / `peer_model` — the adjacent provider on the failover edge
- `peer_route_reason` — why the next or previous candidate was in play
- `health_status` / `peer_health_status` — the runtime health snapshot around the failover
- `attempt_count` — retry attempts exhausted before failover when applicable
- `estimated_micros_usd` — estimated preflight cost when the runtime had one

The history store is configurable with:

- `GATEWAY_PROVIDER_HISTORY_BACKEND` — `memory` or `sqlite`
- `GATEWAY_PROVIDER_HISTORY_LIMIT` — default page size for `/hecate/v1/providers/history`

The Connections view shows the current state on each card:

- 🟢 **Healthy** — recent successful traffic
- 🟡 **Degraded / half-open** — recent failures, probing for recovery
- 🔴 **Open** — circuit open, requests skip this provider entirely
- ⚪ **Unknown** — no traffic yet to evaluate

Health state is in-process and resets on restart by design — durable health tracking would re-include known-broken upstreams that recovered while the gateway was down.

`GET /hecate/v1/providers/status` is the operator diagnostics surface for provider
readiness. In addition to raw health and discovery fields, each provider
returns:

- `credential_ready` — whether credentials are configured or not required
- `routing_ready` — whether the router can currently send traffic to it
- `routing_blocked_reason` — stable reason when routing is blocked, such as `credential_missing`, `provider_disabled`, `provider_rate_limited`, `circuit_open`, `provider_unhealthy`, or `no_models`
- `readiness` — compact provider-level summary for cards/tables with `status`, `reason`, operator-facing `message`, and optional `operator_action`
- `readiness_checks` — a normalized checklist for `credentials`, `models`, `health`, and `routing`. Each check has `status` (`ok`, `warning`, `blocked`, or `unknown`), `reason`, an operator-facing `message`, and an optional `operator_action` repair step. Non-routing checks can use scoped reasons such as `default_model_only`, `discovery_failed`, `self_referential`, or `provider_slow`.
- `model_count`, `discovery_source`, `last_checked_at`, and `last_error` for model-discovery freshness and failure context
- `last_error_class`, `open_until`, `last_latency_ms`, `consecutive_failures`, `timeouts`, `server_errors`, `rate_limits`, `total_successes`, and `total_failures` for richer health debugging

The UI keeps the checklist message verbatim and shows `operator_action` as the
short **Next** hint when the backend provides one. That means
`credential_missing` points directly at credential setup, `no_models` points at
starting/pulling a local model, `self_referential` points at fixing a base URL
that loops back to Hecate, and `provider_rate_limited` points at waiting or
routing elsewhere. The raw diagnostic fields remain available in the provider
details disclosure.

For stale selected models, Hecate Chat returns
`422 model_not_configured` with the same readiness vocabulary: the selected
provider/model, a stable reason such as `provider_missing`,
`provider_not_ready`, or `model_not_discovered`, suggested replacement models,
and an `operator_action` that can be shown directly in the UI.

Route reports in the trace inspector reuse the same readiness vocabulary when
they explain why a provider/model candidate was skipped.

The Chats workspace consumes the same readiness model at composition time. A
provider can be configured and healthy while the selected model is still not
routable, usually because discovery reports a different local model set or a
cloud account does not expose that model. `/v1/models` includes
`metadata.readiness` on each discovered provider/model row so Chats can block
before sending when a discovered model is still not usable because credentials,
health, or routing are blocked. In that case Chats shows the selected model,
provider route, backend readiness message, and next steps, and links the
operator back to Connections for the full checklist. If the backend supplies a
suggested replacement model, Chats can offer it as a one-click repair and
switch the route back to **All providers** so cross-provider fallbacks are not
accidentally pinned to the stale provider. The same contract applies in the
empty-chat state: compact readiness copy must still show the discovered-model
count plus the highest-signal health/block/error diagnostics and a short repair
path, so operators are not forced to send a doomed prompt or inspect raw
provider JSON.

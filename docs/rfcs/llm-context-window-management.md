# LLM Context Window Management

> **Status:** design notes. Not implemented. Captures the framework
> for token estimation, threshold visibility, hard caps, truncation
> policy, and summarization that Hecate needs so chat and agent_loop
> calls fail safely (and cheaply) as conversations grow.
> **Depends on:** the existing usage-event plumbing in
> `internal/governor/`, the system-prompt composition in
> `internal/api/system_prompt.go`, and the not-yet-implemented
> [agent-memory](agent-memory.md) primitive (memory entries inflate
> context).
> **Related:** Anthropic prompt cache markers ship separately in PR #59;
> this RFC integrates with that work but does not re-design it.

Today an operator running a long Hecate Chat or an `agent_loop` task
with `GATEWAY_TASK_AGENT_LOOP_MAX_TURNS=8` (the default) and a few
`read_file` results will silently exceed the model's context window.
The gateway hands the whole transcript upstream; the upstream errors
with a provider-specific `context_length_exceeded` (or worse, a
generic 4xx); the operator sees a mid-stream failure with no
preceding warning. Nothing in Hecate counts tokens, sets a threshold,
or warns ahead of time.

`GATEWAY_TASK_AGENT_LOOP_MAX_TURNS` is a runaway-loop guard, not a
context guard — it bounds *iterations*, not *tokens*. An agent doing
3 huge file reads can exceed 100K tokens in 4 turns; another doing
50 small command checks stays under 20K. They need orthogonal safety
nets.

This RFC scopes the smallest framework that closes the gap, the
phased path to ship it, and the open design choices the implementer
will need to settle.

## Goals

In rough priority order:

1. **Visibility.** Pre-flight token estimate per call surfaced as a
   trace attribute (`hecate.context.tokens_in`); the projected
   running total vs. the model limit surfaced as a runtime header
   (`X-Runtime-Context-Used: projected/model_limit`). No more silent
   context-overflow failures.
2. **Soft warning.** Structured warning when the conversation
   approaches the model's context limit (default 80%). Doesn't
   block the call; gives operators advance notice.
3. **Hard cap.** Configurable per-conversation token budget that
   refuses calls before they hit the upstream limit. Returns a
   structured error the operator can see, not an opaque upstream
   4xx.
4. **Cost-side relief.** Anthropic prompt-cache marker hooks
   (already shipped via PR #59) wrap memory entries when the
   agent-memory primitive lands.
5. **Capability-side relief.** Optional truncation policy when
   over budget. Default OFF; opt-in via env knob. Whatever policy
   we ship, operators will report regressions on the use case it
   mishandles, so the right default is "current behavior preserved
   unless operator chooses otherwise."
6. **Future: summarization.** Side-call to a (configurable, often
   local) model to compress older turns when threshold is hit.
   Real product feature, not v1; framed here so the policy hook is
   ready when summarization arrives.

## Non-goals

- **External-agent adapter context management.** Codex, Claude
  Code, and Cursor Agent each make their own LLM calls outside
  Hecate's control. We don't see their requests; we can't manage
  their context. Out of scope by physics, not policy. ACP would
  need to standardize a context-management primitive first.
- **Replacing provider-side safety nets.** OpenAI's
  `max_completion_tokens`, Anthropic's `max_tokens` — these still
  apply server-side. Hecate's caps complement them; they don't
  substitute.
- **Cost vs capability conflation.** This RFC is honest about which
  problem each piece solves. Caching + summarization fix *cost*.
  Truncation + hard cap fix *capability*. Visibility serves both.
- **Per-model fine-grained tokenizers** for non-OpenAI providers.
  ±5–10% error from a tiktoken-based approximation is acceptable
  for an 80% threshold. Closing the gap means per-provider exact
  count_tokens API calls (paid + slow). Not worth it.
- **Vector-DB-backed semantic compression** ("only include the
  three most relevant earlier turns"). That's a future RFC if
  summarization isn't enough.

## Architecture

Per-conversation token tracking with per-call telemetry emission:

```
       ┌────────────────────────────────────────────────────┐
       │ Conversation prepare                               │
       │                                                    │
       │   estimate = tokens(messages + system +            │
       │              tools + memory)                       │
       │   projected = persisted_running_tokens + estimate  │
       │   model_limit = lookupModelContextLimit(model)     │
       │                                                    │
       │   if projected > 0.95 * model_limit:               │
       │       ┌──── if truncation enabled ──┐              │
       │       │  apply policy → re-estimate │              │
       │       │  → projected = persisted +  │              │
       │       │    new_estimate             │              │
       │       └─────────────────────────────┘              │
       │       ┌──── if still > 0.95 ────────┐              │
       │       │  if hard_cap enabled:       │              │
       │       │      refuse → 422           │              │
       │       └─────────────────────────────┘              │
       │   if projected > 0.80 * model_limit:               │
       │       emit structured warning to trace + header    │
       │                                                    │
       │   set runtime header: X-Runtime-Context-Used:      │
       │       projected/model_limit                        │
       │   set span attr: hecate.context.tokens_in          │
       │   set span attr: hecate.context.fraction           │
       │                                                    │
       │   ──────── after upstream call succeeds ─────────  │
       │   persisted_running_tokens = projected             │
       └────────────────────────────────────────────────────┘
```

Two values are deliberately distinct: `persisted_running_tokens`
is the durable per-conversation total carried in the chat-session
or task-run row; `projected` is `persisted + estimate`, computed
fresh per call and used for both threshold checks and the runtime
header. The persisted value updates only on a successful upstream
response (see "Risks" — failed-and-retried calls must not double-count).

Where the estimate runs:
- **Hecate Chat / model chat**: in `internal/api/handler_chat.go`'s
  request preparation, before the provider call dispatches.
- **`agent_loop` task runs**: in
  `internal/orchestrator/executor_agent_loop.go`'s per-turn loop,
  before each upstream call.

Both paths converge on a single estimation helper in (proposed)
`internal/contextbudget/` so the logic isn't duplicated.

## Tokenizer choice

**Default: `github.com/pkoukk/tiktoken-go`** — the de-facto Go port
of OpenAI's tiktoken. ~10K stars, actively maintained, used by
LangChain Go bindings and most Go-based gateways. Tracks upstream
tokenizer updates (cl100k_base for older OpenAI, o200k_base for the
GPT-4o family).

For non-OpenAI providers (Anthropic, Gemini, DeepSeek, xAI,
local), we use the same tiktoken-go encoder with cl100k_base. ±5–10%
error vs. each vendor's actual tokenizer. That's fine for an 80%
threshold — the warning still fires within the right ballpark, and
the hard cap still catches real overflows. The cost of being exact
(vendor-specific count_tokens API calls per request) outweighs the
benefit at this layer of accuracy.

### Open: vendor or stay on upstream

`tiktoken-go` is a third-party dependency. Two postures:

- **(a) Direct dependency** (default proposal). Track upstream
  releases via `go get`. Smallest maintenance burden today.
- **(b) Vendor a copy** under `internal/contextbudget/tiktoken/`,
  pin a known-good version. Insulates from upstream churn at the
  cost of pulling our own copy along when upstream ships fixes.
- **(c) Write our own**. Reimplement BPE encoding + ship the
  encoding tables. The encoding tables for cl100k_base and
  o200k_base are large (~MB each), the BPE algorithm is real but
  not exotic, and we'd be on the hook for tracking every new
  OpenAI model with a new tokenizer. ~weeks of work for the
  marginal control-gain.

**Recommendation:** start with (a). Re-evaluate to (b) only if
upstream becomes unmaintained or ships a breaking change we can't
absorb. (c) is over-engineering at our accuracy target.

## Per-conversation tracking, per-call telemetry

Every gateway call to a provider emits two telemetry signals:

- **Span attribute** `hecate.context.tokens_in` (estimated, this
  call only) and `hecate.context.fraction` (running total /
  model_limit).
- **Runtime header** `X-Runtime-Context-Used: NNNN/MMMMM` on the
  response so SDK consumers can render a context-usage meter without
  re-estimating client-side. Matches the existing `X-Runtime-*`
  convention (`X-Runtime-Provider`, `X-Runtime-Model`, etc.).

The running total persists on the conversation:

- For Hecate Chat: tracked on `ChatSessionRecord`; new column
  `running_context_tokens INTEGER` migrated additively in
  `internal/chatstate/sqlite.go`.
- For `agent_loop` task runs: tracked on `TaskRun`; similar
  additive migration in `internal/taskstate/sqlite.go`.

Hard cap and warn threshold both evaluate against the running
total, not the per-call estimate.

## Per-model context limits

Hecate doesn't currently know that gpt-4o-mini is 128K and o1 is
200K. New file `internal/contextbudget/model_limits.go` with a
hardcoded table:

```go
var modelContextLimits = map[string]int{
    "gpt-4o":         128_000,
    "gpt-4o-mini":    128_000,
    "o1":             200_000,
    "o3-mini":        200_000,
    "claude-3-5-sonnet-latest": 200_000,
    "claude-3-5-haiku-latest":  200_000,
    "claude-3-opus":  200_000,
    "gemini-1.5-pro": 1_000_000,
    "gemini-1.5-flash": 1_000_000,
    "deepseek-chat":  64_000,
    "deepseek-reasoner": 64_000,
    // … see implementation
}
```

When the model isn't in the table, fall back to a structured
warning ("`unknown_model_context_limit`") and use a conservative
default (32K). Operators override the default per-model in the
model capability registry, which gains an optional
`context_window_tokens` field. The value can come from a static catalog,
provider discovery, or an operator override.

Lookup precedence:

1. Operator capability override (per-model)
2. Hardcoded table
3. Conservative fallback (32K) + structured warning

## Threshold semantics

Three levels:

| Level | Default | Trigger | Effect |
|---|---|---|---|
| **Soft warn** | 80% of model limit | Running total exceeds | Structured warning in trace + `X-Runtime-Context-Warning` header. Call proceeds. |
| **Hard cap** | 95% of model limit | Running total + estimated next-call exceeds | Call refused. Returns 422 with structured body: `type=context.budget_exceeded`, includes `tokens_in`, `tokens_limit`, `model`. |
| **Per-task budget** | unset (0) | Operator-configured `GATEWAY_TASK_AGENT_LOOP_MAX_CONTEXT_TOKENS`. When set, takes precedence over the model-limit-derived cap. | Same 422 shape. |

The `context.*` prefix is intentional. Hecate's existing `error.type`
values follow two conventions: plain snake_case for top-level
gateway errors (`rate_limit_exceeded`, `gateway_error`,
`invalid_request`) and `<subsystem>.<reason>` for subsystem-scoped
errors that will plausibly grow siblings — `agent_chat.session_limit_exceeded`,
`agent_chat.session_duration_limit_exceeded` are the precedent in
`internal/api/response.go`. Context-window failures will plausibly
add `context.truncation_failed`, `context.summarization_failed`,
and similar variants as later phases ship, so the dot-prefixed
form matches the subsystem-scoped convention rather than treating
this single error in isolation.

Both percentage thresholds are configurable via env knobs:

```
GATEWAY_CONTEXT_WARN_THRESHOLD=0.80
GATEWAY_CONTEXT_HARD_CAP_THRESHOLD=0.95
```

## Truncation policy (opt-in)

Default OFF. Operators opt in via:

```
GATEWAY_TASK_AGENT_LOOP_TRUNCATION=drop_oldest
GATEWAY_TASK_AGENT_LOOP_TRUNCATION_KEEP_RECENT=4
```

Three policies in scope:

- **`drop_oldest`**: drop oldest user/assistant pairs until under
  threshold. Preserves system + last `KEEP_RECENT` pairs.
  Simplest. Loses original context.
- **`drop_tool_intermediates`**: drop oldest *tool_call /
  tool_result* messages while keeping user/assistant text intact.
  Better for QA-style transcripts; breaks if later turns reference
  earlier tool output.
- **`summarize_then_drop`**: side-call to a configurable
  summarization model; replace dropped messages with a single
  `system` message containing the summary. Most useful, most
  complex (see next section).

Truncation runs only on `agent_loop` task runs initially; Hecate
Chat operators get hard-cap or do their own message editing
client-side. Adding truncation to the chat path is a phase-2
extension once the policy primitives prove out in tasks.

## Summarization (deferred to a later phase)

Real product feature with eval requirements: precision/recall on
"did the summary preserve enough?", latency (the side call adds
~500ms before the main call), cost (paying for summary).

**For local-only deployments, the cost concern goes away** — the
summarization model can be a local Ollama / LM Studio model; no
per-token charge. This makes summarization a friction-free default
for local-first operators.

For cloud deployments, the operator picks a cheap "summarization
model" (Haiku for Anthropic, gpt-4o-mini for OpenAI, etc.). Knobs:

```
GATEWAY_TASK_AGENT_LOOP_SUMMARIZATION_MODEL=
GATEWAY_TASK_AGENT_LOOP_SUMMARIZATION_KEEP_RECENT=4
GATEWAY_TASK_AGENT_LOOP_SUMMARIZATION_TRIGGER_THRESHOLD=0.75
```

The summarized form replaces dropped older messages:

```
system (frozen, untouched)
system: "Earlier in this conversation: <model-generated summary>"
user/assistant turn (N-3)
user/assistant turn (N-2)
user/assistant turn (N-1)
user (current)
```

Phase: ships AFTER the visibility + hard-cap pieces are in
production. Doesn't block the rest of the framework.

## Memory-aware

The [agent-memory RFC](agent-memory.md) introduces operator-authored
memory entries injected into the system prompt. Memory entries
contribute tokens; the budget MUST count them.

Concretely:

- The estimator helper takes the resolved system prompt
  (post-injection of memory + workspace files + per-task) plus the
  message list plus tool definitions, and counts the whole thing.
- Memory entries that are large enough to risk inflating context
  trigger the same warn/cap mechanics as long conversations.
- The agent-memory RFC's per-entry size limit (5K soft / 20K hard)
  exists partly to keep this estimator's ceiling reasonable.

## Cache marker integration

Anthropic prompt-cache markers shipped via PR #59 (system prompt +
tools entries). Two extensions this RFC anticipates but doesn't
build:

- **Memory block**: when the agent-memory RFC is implemented, the
  injection code wraps the memory block in the same
  `cache_control: {"type": "ephemeral"}` marker. Memory changes
  infrequently, so cache hits are high.
- **Conversation prefix**: long stable conversation prefixes (e.g.
  the system prompt + first 3 user/assistant pairs) are also
  cacheable. Applying the marker dynamically based on stability is
  a phase-3 enhancement.

This RFC declares the integration points; the actual changes ride
in the relevant feature PRs.

## Cost vs capability framing

Two different operator motivations for context-window work:

| Motivation | Failure mode today | Pieces that fix it |
|---|---|---|
| **Cost** | "the conversation succeeds but bills more than expected" | Caching (PR #59), summarization, memory size limits |
| **Capability** | "the conversation fails mid-call when upstream rejects 200K tokens" | Visibility, hard cap, truncation |

The RFC is honest that neither piece solves both problems. An
operator who only cares about cost should turn on caching +
summarization. One who cares about capability should set hard caps.
Most operators will turn on visibility, hard caps, and
caching; summarization is opt-in for those willing to accept its
trade-offs.

## Phasing

| PR | Scope | Size |
|---|---|---|
| 1 | `internal/contextbudget/` package: tokenizer (tiktoken-go), estimator, model-limit table. Unit-tested in isolation. No wiring. | ~400 |
| 2 | Per-call telemetry: span attribute + `X-Runtime-Context-Used` runtime header in `handler_chat.go` and `executor_agent_loop.go`. Visibility-only, no caps. | ~250 |
| 3 | Per-conversation running-total persistence: additive migrations on `chat_sessions` and `task_runs`; running total updated per call. | ~300 |
| 4 | Soft warn threshold (default 80%) — structured warning in trace, `X-Runtime-Context-Warning` runtime header. Operator-configurable threshold. | ~200 |
| 5 | Hard cap (default 95% derived; per-task budget knob). Returns 422 with structured `context.budget_exceeded` body. | ~250 |
| 6 | Truncation policy: `drop_oldest` and `drop_tool_intermediates`. Opt-in via env knob. `agent_loop` only. | ~400 |
| 7 | Summarization policy. Side-call architecture. Configurable model. Local-model friction-free default for local-only operators. | ~600 |
| 8 | Docs: drop the relevant `known-limitations.md` entries; add `docs/context-budget.md` operator guide. | ~150 |

Total: ~2550 lines, 8 PRs. PRs 1–5 close the visibility +
capability gap. PR 6 is opt-in policy. PR 7 is the cost-side relief
(deferrable). PR 8 documents.

PRs 1–5 alone would close the immediate operator pain. The rest is
incremental.

## Open questions

- **Vendor tiktoken-go or stay on upstream.** Recommendation:
  upstream until proven painful. Re-evaluate per release.
- **Is `context.fraction` the right name** for the running-total /
  model-limit ratio? Operators might prefer `context.percent_used`.
  Bikeshed; defer to implementer taste.
- **Truncation in Hecate Chat** (not just `agent_loop`). Phase-2
  extension once the policy primitives prove out. Open: should
  Hecate Chat get an inline "[earlier messages truncated]" notice
  in the transcript so operators can see what happened?
- **Per-message provenance for truncation.** Drop-policy actions
  could be recorded as a span event ("dropped 12 tool_result
  messages") so operators can audit. Useful but adds a logging
  surface; defer to implementer.
- **Streaming**: token estimation mid-stream is hard. The estimator
  runs pre-flight, before stream starts; the response stream
  doesn't refine the estimate (the response side updates the
  running total once complete). Acceptable for v1; phase-2
  improvement.
- **Capability field for context_window_tokens.** Schema/API change to
  the model capability store. Probably worth doing alongside PR 1 since
  the model-limit lookup is the new dependency.
- **What to do when `model` isn't set on a call.** Some clients
  don't set model in the request when the gateway's default applies.
  The estimator needs to know which model to look up. Resolve via
  the same router-decision logic that picks the actual upstream
  model.

## Risks

1. **Tokenizer drift on non-OpenAI providers.** Anthropic's
   tokenizer differs from tiktoken; estimates will be ±5–10% off.
   Mitigation: thresholds are 80%/95%, not 99% — the slop is
   absorbed by the headroom.
2. **Hard cap false-positives.** Operator hits the cap mid-task,
   the run fails, they're confused. Mitigation: structured 422
   with `tokens_in`, `tokens_limit`, model name; runtime header
   `X-Runtime-Context-Used`; operator-tunable threshold; documented in
   `docs/context-budget.md`.
3. **Truncation breaks user expectations** on the use case the
   chosen policy mishandles. Mitigation: opt-in default; operators
   choose. Documentation explicit about each policy's
   trade-offs.
4. **Summarization summary loses tool-call context.** If a turn
   later in the conversation references "the file we read above"
   and that turn is now in the summary, the model may hallucinate
   contents. Mitigation: keep tool_call/tool_result pairs intact
   when possible (`drop_tool_intermediates` is the cleaner
   variant); summarization is opt-in; eval scaffolding before
   shipping.
5. **Capability schema change** for `context_window_tokens` ripples
   to the model capability store. Mitigation: field is nullable; null
   means "use the hardcoded fallback"; existing rows don't need
   migration.
6. **Per-conversation tracking is wrong on retried calls.** A
   failed-and-retried call shouldn't double-count tokens.
   Mitigation: running total updates on success only (or on the
   final attempt of a retry sequence); failed calls are recorded
   separately for billing.

## Acceptance criteria

When this RFC is implemented end-to-end:

- A long Hecate Chat or `agent_loop` task run that approaches the
  model's context limit produces a structured warning in the trace
  and an `X-Runtime-Context-Warning` header *before* the upstream
  errors.
- A run that would exceed the hard cap is refused with a structured
  422 (`context.budget_exceeded`) instead of a mid-stream upstream
  error.
- An operator can see `hecate.context.tokens_in` and
  `hecate.context.fraction` on every span and `X-Runtime-Context-Used`
  on every response.
- An operator can opt into truncation (`drop_oldest` or
  `drop_tool_intermediates`) per-deployment via env knob; default
  remains OFF.
- An operator running entirely against local models can opt into
  summarization without per-token cost concerns.
- Memory entries (when the agent-memory RFC is implemented)
  contribute to the running token total and trigger warns / caps
  appropriately.
- The relevant `known-limitations.md` entries about silent context
  failures are dropped or rewritten.

## Cross-references

- [Agent memory](agent-memory.md) — memory entries inflate context;
  the estimator MUST count them. Memory size limits in that RFC
  exist partly to keep this estimator's ceiling reasonable.
- [Provider response extensions](provider-response-extensions.md) —
  `Usage.ReasoningTokens` is one piece of context-cost accounting
  this framework integrates with on the response side.
- PR #59 (Anthropic prompt cache markers) — already-shipped code
  that this RFC anticipates extending in phase-3 to cover memory
  blocks and stable conversation prefixes.

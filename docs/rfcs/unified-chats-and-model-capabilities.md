# Unified Chats and Model Capabilities — Exploratory RFC

> **Status:** exploratory. This document describes a target product and
> architecture direction; it is not implemented behavior.
> **Related:** [Chat sessions](../chat-sessions.md),
> [External agent adapters](external-agent-adapters.md),
> [Agent runtime](../agent-runtime.md), [Runtime API](../runtime-api.md).
> **Owner:** see [`AGENTS.md`](../../AGENTS.md).

Hecate currently exposes model chat and agent chat as adjacent but different
surfaces. That distinction helped the alpha move quickly, but it should not
become the long-term mental model. Operators should be able to start one chat
and choose how Hecate executes each turn: direct model, model plus Hecate tools,
Hecate-managed agent profile, or external agent adapter.

The missing bridge is a first-class **model capability registry**. Hecate needs
to know which models can call tools, stream, emit reasoning/thinking blocks,
return structured output, accept vision inputs, and support long context before
the UI can safely offer agent-like behavior for model chats.

## Problem

There are two useful but currently separate chat modes:

- **Model chat** routes directly through providers. It is good for plain
  conversation and provider testing, but it does not expose Hecate's tool loop
  as a first-class option.
- **Agent chat** runs an external agent adapter such as Codex, Claude Code, or
  Cursor Agent. It has workspace, session, run, approval, diff, and diagnostics
  concepts, but it is not the same thing as selecting a model.

This creates confusing product boundaries:

- A tool-capable model can behave like a small agent, but the UI treats it as
  plain chat.
- External agents are not model providers, but they live in the same Chats
  workspace and compete for the same operator attention.
- Future external agentic frameworks should fit without turning the provider
  picker into a junk drawer.
- Hecate cannot route "use tools" requests safely until it knows whether the
  selected model supports tool calling.

## Goals

- Make Chats one unified workspace with execution targets instead of separate
  mental worlds.
- Add model capability metadata that can drive UI controls, routing, and agent
  profile validation.
- Let operators use either a direct model or a Hecate-managed agent profile that
  selects compatible models through gateway rules.
- Keep external agents as agent adapters, not fake providers.
- Preserve provider routing, cost tracking, OTel, approvals, and workspace
  semantics as Hecate-owned concerns where Hecate owns the loop.
- Leave room for future external agentic frameworks without binding the design
  to one framework.

## Non-goals

- Do not merge external agents into the provider/model list.
- Do not claim every local model's capabilities can be known from `/models`.
- Do not require all model providers to expose the same capability discovery
  surface.
- Do not replace the durable task runtime in this RFC.
- Do not design a plugin marketplace or broad agent SDK here.

## Execution Targets

Chats should expose a small number of execution targets:

| Target | Meaning | Who owns the loop |
|---|---|---|
| Direct model | One provider/model answers the turn directly. | Provider model only |
| Model + tools | Hecate runs a lightweight tool loop around a tool-capable model. | Hecate |
| Hecate agent profile | Hecate runs a named agent configuration with tools, workspace, approvals, and model policy. | Hecate |
| External agent | Codex, Claude Code, Cursor Agent, or another adapter owns the native agent session. | External adapter, supervised by Hecate |

The important distinction is ownership. If Hecate owns the loop, Hecate can
enforce model capability requirements, approval policy, workspace behavior,
artifacts, cost accounting, and telemetry. If an external adapter owns the loop,
Hecate supervises process/session lifecycle and records what the adapter emits,
but Hecate does not pretend the adapter is a model.

## Model Capability Registry

Hecate should add a capability record per provider/model:

```ts
type ModelCapabilities = {
  tool_calling: "none" | "basic" | "parallel";
  structured_output: "none" | "json_schema" | "provider_native" | "tool_emulated";
  reasoning: "none" | "effort" | "token_budget" | "provider_native";
  vision: boolean;
  streaming: boolean;
  max_context_tokens?: number;
  source: "catalog" | "provider" | "probe" | "operator_override";
};
```

These fields should be additive. More capability dimensions can be added later
without changing the core model.

### Capability Sources

Capability data should be layered because no single source is reliable for all
providers:

1. **Catalog default** — Hecate ships known capability metadata for common cloud
   models and first-party presets.
2. **Provider discovery** — provider endpoints tell Hecate which models exist
   and sometimes expose useful metadata.
3. **Capability probe** — Hecate can optionally send a tiny tool-call or
   structured-output request and validate the model's behavior.
4. **Operator override** — the UI lets the operator mark a model as supporting
   or not supporting a capability, especially for local providers.

The final capability record should keep the winning `source` so the UI can show
confidence:

```text
tools: supported · catalog
tools: unknown · local provider
tools: supported · operator override
```

## What Other Frameworks Do

Most agent frameworks combine static metadata, provider-specific adapters, and
operator overrides.

- **LangChain** exposes model features such as tool calling, structured output,
  multimodality, and reasoning at the integration/model layer. Strategy
  selection can choose provider-native structured output when available and
  fall back to tool-based output otherwise.
- **Gateway-style routers** tend to keep model metadata near their cost and
  routing tables, then expose helpers for capability checks and provider model
  discovery.
- **Provider SDKs** remain the source of truth for exact semantics. OpenAI,
  Anthropic, and local OpenAI-compatible servers differ in how they advertise
  tool calling, reasoning/thinking, structured output, and model lists.

The lesson for Hecate: do not rely on one universal provider response. Keep a
small Hecate-owned capability model, fill it from multiple sources, and let
operators override local/unknown cases.

## Agent Profiles

Hecate should let users create **agent profiles**. A profile is not a model.
It is a named execution policy that can select one or more compatible models.

Example:

```yaml
name: Builder
runtime: hecate_tool_loop
model_policy:
  preferred:
    - anthropic/claude-sonnet
    - openai/gpt-5.4
    - ollama/qwen-local
  requires:
    tool_calling: basic
    max_context_tokens: 32000
  fallback: any_compatible
tools:
  - shell
  - files
  - git
approvals:
  shell: prompt
  file_write: prompt
workspace:
  required: true
```

This gives Hecate a clean answer to "use the best available model for this
agent": the gateway can route only among models whose capabilities satisfy the
profile's requirements.

## UI Direction

Chats should keep one transcript but expose an execution target picker.

```text
Chat with: Direct model | Hecate agent | External agent
```

### Direct Model

```text
Provider/model: [Ollama / llama3.1]
Tools: Off / On if supported
Reasoning: Off / Low / Medium / High if supported
```

If the selected model does not support tools, the Tools control should be
disabled with useful copy:

```text
Tools unavailable for this model.
Pick a tool-capable model or create an agent profile with compatible routing.
```

### Hecate Agent

```text
Agent: Builder / Reviewer / Researcher / Custom
Model policy: Auto compatible / Specific model
Workspace: required
Tools: files, shell, git
Approvals: prompt for shell and writes
```

The UI should show capability badges for the resolved model:

```text
claude-sonnet · tools · reasoning · 200k context
```

If auto-routing picks a different model because the preferred one lacks tools,
the UI should say so explicitly.

### External Agent

```text
Agent: Codex / Claude Code / Cursor Agent
Workspace: required
Runtime: external adapter
```

External agent sessions should keep their current unsandboxed/workspace
disclosures. They are not model capability consumers unless the adapter itself
reports model metadata in a future protocol.

## Routing Implications

The router should accept capability requirements in addition to provider/model
preferences:

```json
{
  "model_policy": {
    "preferred": ["anthropic/claude-sonnet", "openai/gpt-5.4"],
    "requires": {
      "tool_calling": "basic",
      "reasoning": "any"
    },
    "fallback": "any_compatible"
  }
}
```

The router should reject early when no configured provider/model can satisfy
the capability requirements. This should produce a user-facing error such as:

```text
No configured model supports tool calling. Add a capable model or disable tools.
```

## Storage and API Shape

Likely additions:

- `GET /v1/models` includes optional `capabilities`.
- `GET /v1/settings/model-capabilities` returns overrides and source details.
- `PATCH /v1/settings/model-capabilities/{provider}/{model}` stores operator
  overrides.
- Agent profile endpoints store model policy, tools, approval policy, and
  workspace requirements.

Model capability records should be persisted in the settings backend when they
come from operator overrides. Catalog-derived and provider-discovered values can
be recomputed.

## Open Questions

- Should capability probing happen automatically, or only when the operator asks
  Hecate to "test this model"?
- Should local provider models default to `tool_calling=unknown` or
  `tool_calling=none` until proven otherwise?
- How much of Hecate's durable task runtime should the lightweight
  "model + tools" mode reuse?
- Should agent profiles be exposed as first-class API objects before the UI
  lands, or should the first implementation keep profiles UI-local?
- Should reasoning/thinking support be a single capability, or provider-specific
  sub-capabilities because OpenAI, Anthropic, and local models expose it so
  differently?

## Recommended Implementation Order

1. Add model capability types and catalog defaults for known built-in providers.
2. Surface capabilities in `/v1/models` and the provider/model picker.
3. Add operator overrides for local/unknown models.
4. Add capability-aware UI affordances: disabled tools toggle, reasoning toggle,
   capability badges.
5. Add agent profiles with model-policy requirements.
6. Route Hecate-owned tool loops only to models satisfying the profile.
7. Revisit whether direct model chat with tools should become a lightweight
   Hecate agent profile internally.

## Decision Bias

Prefer small, explicit capability records over magic. Hecate should not infer
"agentness" from a model name. It should know what a model can do, show that
clearly to the operator, and use that information to decide which execution
targets are available.

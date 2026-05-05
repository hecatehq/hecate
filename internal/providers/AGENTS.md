# Hecate providers package

Outbound HTTP adapters from the gateway to LLM upstreams (OpenAI-compat,
Anthropic). Sibling to the root [`AGENTS.md`](../../AGENTS.md), which
covers cross-cutting backend conventions.

The substance for provider work — the seven-step "add a wire field"
chain, the api↔providers parallel-struct rule, capability cache seeding,
cross-provider translation, streaming gotchas, prompt caching — lives
in the canonical providers skill: [`../../docs-ai/skills/providers/SKILL.md`](../../docs-ai/skills/providers/SKILL.md).
Read it before making changes here.

## At a glance

```
provider.go               Provider / Streamer / Capabilities interfaces
openai.go                 OpenAI-compat adapter (real OpenAI, Together, Groq, Ollama, vLLM)
anthropic.go              Native Anthropic Messages API adapter
runtime_manager.go        provider catalog + protocol → adapter dispatch
capabilities_cache.go     /v1/models discovery + TTL cache
discovery_policy.go       when to discover, when to use static caps
health.go                 provider health probe + circuit
mutable_registry.go       control-plane mutation surface
```

Test helpers live in `provider_test_helpers_test.go` and `tooluse_test.go`
(the `newAnthropicTestProvider` helper is there, not in `anthropic_test.go`).

## Where to go for depth

- The seven-step "add a wire field" chain — [`../../docs-ai/skills/providers/SKILL.md`](../../docs-ai/skills/providers/SKILL.md).
- Capital/lowercase parallel-struct rule (full reasoning) — same skill; one-paragraph version in [`../../docs-ai/core/engineering-standards.md`](../../docs-ai/core/engineering-standards.md).
- Capability cache seeding snippet — [`../../docs-ai/skills/providers/SKILL.md`](../../docs-ai/skills/providers/SKILL.md).
- Cross-provider translation patterns — [`../../docs-ai/skills/providers/SKILL.md`](../../docs-ai/skills/providers/SKILL.md).
- Streaming gotchas (`translateAnthropicSSE`, `translateOpenAIToAnthropicSSE`, tool-call streaming) — [`../../docs-ai/skills/providers/SKILL.md`](../../docs-ai/skills/providers/SKILL.md).
- Prompt caching (Anthropic three-bucket usage) — [`../../docs-ai/skills/providers/SKILL.md`](../../docs-ai/skills/providers/SKILL.md).
- Common bugs in this package — [`../../docs-ai/skills/providers/SKILL.md`](../../docs-ai/skills/providers/SKILL.md).
- Backend-wide rules — [`../../docs-ai/skills/backend/SKILL.md`](../../docs-ai/skills/backend/SKILL.md).
- Race-suite verification floor for runtime/backend changes — [`../../docs-ai/core/verification.md`](../../docs-ai/core/verification.md).

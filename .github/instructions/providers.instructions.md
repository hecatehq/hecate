---
applyTo: "internal/providers/**,internal/api/openai.go,internal/api/anthropic.go,internal/api/handler_messages.go,pkg/types/chat.go"
---

# Provider Boundary

For provider work, read `internal/providers/AGENTS.md` and
`docs-ai/skills/providers/SKILL.md` before editing.

High-signal rules:

- The API-side uppercase structs and provider-side lowercase structs are
  intentionally duplicated. Do not unify them.
- When adding a passthrough field, mirror the full chain across public types,
  API structs, provider structs, both `Chat` and `ChatStream`, and tests.
- Translate or explicitly drop unsupported cross-provider fields.
- Seed capability caches in provider tests when discovery is not the behavior
  under test.
- Streaming paths need dedicated tests; non-stream tests do not prove streaming
  wire plumbing works.

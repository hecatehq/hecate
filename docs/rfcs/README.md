# Hecate RFCs

Design contracts in this directory are candidate or experimental. They help us
review protocol direction before it becomes a semver-backed API promise.

Implemented runtime behavior lives in the main docs:

- [Events](../events.md) — event names and payloads emitted today.
- [Runtime API](../runtime-api.md) — current task/run/approval endpoints.
- [ACP bridge](../acp.md) — implemented-but-experimental editor bridge status.
- [Chat sessions](../chat-sessions.md) — current Hecate Chat and External
  Agent session behavior.
- [External agent adapters](../external-agent-adapters.md) — current Codex,
  Claude Code, and Cursor operator flow.

## Current RFCs

| RFC | Status |
|---|---|
| [Agent event protocol v1 candidate](event-protocol-v1.md) | Candidate envelope exists; payload schemas and stability guarantees are still in progress. |
| [Agent event protocol experimental extensions](event-protocol-experimental.md) | Parking lot for future event groups such as thinking blocks, sub-agents, multimodal output, and branching. |
| [Artifact storage v1 candidate](artifact-storage-v1.md) | Candidate shape for persisted command output, patches, fetched resources, and artifact retention. Task artifacts and chat diff inspect/revert exist today; the RFC remains broader than the shipped alpha surface. |
| [External agent adapters candidate](external-agent-adapters.md) | Partially implemented alpha baseline for Codex, Claude Code, Cursor Agent, readiness, guardrails, approvals, diagnostics, and diff inspect/revert. The RFC remains useful for convergence and future adapter depth. |
| [External-adapter approval loop v1 candidate](external-adapter-approvals-v1.md) | Implemented alpha baseline: prompt-first approval mode, REST/SSE events, durable grants, startup reconcile, retention, UI review, and telemetry. Stable status still depends on real-adapter soak and convergence decisions. |
| [Hecate Chat and model capabilities](unified-chats-and-model-capabilities.md) | Accepted and partially implemented alpha direction for Hecate Chat tools on/off segments, Hecate-owned task execution, External Agent separation, and tool-capability metadata. Profiles, workspace modes, and automatic probes remain future work. |
| [Endpoint versioning and settings paths](endpoint-versioning-and-settings-paths.md) | Accepted and implemented alpha route split: provider-compatible `/v1/*` ingress, Hecate-native `/hecate/v1/*`, and `/admin/*` removal. |
| [Provider response extensions](provider-response-extensions.md) | Design notes for preserving vendor-specific response fields (Perplexity citations, DeepSeek/xAI reasoning content, Gemini citation metadata) end-to-end through api/persistence/UI. Not implemented. |
| [Migration CLI](migration-cli.md) | Design notes for `hecate migrate {status,apply,snapshot,restore,verify}` — operator tooling for backup, rollback, and schema visibility. Closes the "no migration CLI or rollback workflow" known limitation. Not implemented. |
| [Terminal / CLI distribution](terminal-distribution.md) | Design notes for a terminal-first install with `hecate`, `hecate-acp`, and a future first-class TUI. Release archives already ship the binary pair; the TUI and terminal setup commands are not implemented. |
| [Agent memory](agent-memory.md) | Design notes for cross-session, operator-authored memory entries that persist across Hecate Chat sessions and `agent_loop` task runs. Provider-neutral, scoped (global / workspace / agent kind), no auto-extraction in v1. Not implemented. |
| [LLM context window management](llm-context-window-management.md) | Design notes for token estimation, soft warn / hard cap thresholds, optional truncation and summarization, and per-conversation budget tracking. Closes the silent-context-overflow gap; Hecate-controlled surfaces only (external adapters out of scope). Not implemented. |
| [Import external chat history](import-external-chat-history.md) | Design notes for one-shot ingest of Claude Code (`~/.claude/projects/*/*.jsonl`) and Codex CLI (`~/.codex/sessions/**/rollout-*.jsonl`) transcripts into the existing agent-chat store as read-only, searchable, attributable sessions. Idempotent via `(source_tool, native_session_id)`; no resume, no edit, no live mirror in v1. Not implemented. |
| [Embeddings](embeddings.md) | Design notes for `POST /v1/embeddings` end-to-end: optional `Embedder` interface on provider adapters, a separate `routeEmbedding` with pinned routing and no failover, OpenAI / Voyage / Gemini / Azure coverage, llama-server `--embeddings` per-model toggle, per-event `governor.UsageEvent` recording, new `ModelCapabilities.Kind` field for catalog discrimination. Anthropic / DeepSeek / Groq / Perplexity stay opt-out. v1.0 ships `float` encoding only — `base64` deferred to v1.1. No failover, no caching, no reranking, no multimodal. Not implemented. |
| [Chat runtime UX consistency](chat-runtime-ux-consistency.md) | Working plan. The shared shell/settings/activity baseline has landed; use this RFC for the remaining parity work around repair/onboarding, approval polish, and runtime-specific edge cases. |
| [ACP editor-owned workspace transport](acp-editor-owned-workspace.md) | Design notes for routing ACP reverse-RPC (`fs/*`, `terminal/*`, `session/request_permission`) from the gateway-side orchestrator back to the editor. Proposes moving the ACP dispatcher into the gateway and rewriting `hecate-acp` as a thin stdio↔WebSocket relay so `ACPWorkspace.Call` is an in-process function call. Phases: gateway-side listener, bridge-as-relay, then wire `ACPWorkspace`. The `workspace.Workspace` abstraction and capability negotiation it depends on landed in [PR #107](https://github.com/hecatehq/hecate/pull/107). Not implemented. |
| [Local models — Hecate-managed llama.cpp runtime](local-models-llamacpp.md) | Bundled `llama-server` sidecar, HuggingFace-sourced curated catalog with paste-URL escape hatch, restart-on-switch lifecycle, and a gateway-internal proxy that surfaces installed models through `/v1/models`. **v1 implemented** (macOS arm64 desktop bundle + headless gateway). **v2 implemented** in the same branch: HuggingFace browse / search panel, gated-repo HF tokens, multi-model LRU keep-warm runtime, headless lazy-download for the `llama-server` binary. Linux / Windows bundles are still out of scope. |

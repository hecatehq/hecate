# Hecate RFCs

Design contracts in this directory are candidate or experimental. They help us
review protocol direction before it becomes a semver-backed API promise.

Implemented runtime behavior lives in the main docs:

- [Events](../events.md) — event names and payloads emitted today.
- [Runtime API](../runtime-api.md) — current task/run/approval endpoints.
- [ACP bridge](../acp.md) — implemented-but-experimental editor bridge status.

## Current RFCs

| RFC | Status |
|---|---|
| [Agent event protocol v1 candidate](event-protocol-v1.md) | Candidate envelope exists; payload schemas and stability guarantees are still in progress. |
| [Agent event protocol experimental extensions](event-protocol-experimental.md) | Parking lot for future event groups such as thinking blocks, sub-agents, multimodal output, and branching. |
| [Artifact storage v1 candidate](artifact-storage-v1.md) | Candidate shape for persisted command output, patches, fetched resources, and artifact retention. |
| [External agent adapters candidate](external-agent-adapters.md) | Candidate shape for chatting with Codex, Claude Code, Cursor Agent, and future coding-agent CLIs through Hecate. |
| [External-adapter approval loop v1 candidate](external-adapter-approvals-v1.md) | Operator-controlled `RequestPermission` handling for external ACP adapters. Replaces the current auto-approve stub. |
| [Hecate Chat and model capabilities](unified-chats-and-model-capabilities.md) | Accepted alpha direction for Hecate Chat tools on/off segments, Hecate-owned task execution, External Agent separation, and tool-capability metadata. |
| [Endpoint versioning and settings paths](endpoint-versioning-and-settings-paths.md) | Accepted and implemented alpha route split: provider-compatible `/v1/*` ingress, Hecate-native `/hecate/v1/*`, and `/admin/*` removal. |
| [Provider response extensions](provider-response-extensions.md) | Design notes for preserving vendor-specific response fields (Perplexity citations, DeepSeek/xAI reasoning content, Gemini citation metadata) end-to-end through api/persistence/UI. Not implemented. |
| [Migration CLI](migration-cli.md) | Design notes for `hecate migrate {status,apply,snapshot,restore,verify}` — operator tooling for backup, rollback, and schema visibility. Closes the "no migration CLI or rollback workflow" known limitation. Not implemented. |
| [Agent memory](agent-memory.md) | Design notes for cross-session, operator-authored memory entries that persist across Hecate Chat sessions and `agent_loop` task runs. Provider-neutral, scoped (global / workspace / agent kind), no auto-extraction in v1. Not implemented. |
| [LLM context window management](llm-context-window-management.md) | Design notes for token estimation, soft warn / hard cap thresholds, optional truncation and summarization, and per-conversation budget tracking. Closes the silent-context-overflow gap; Hecate-controlled surfaces only (external adapters out of scope). Not implemented. |
| [Observability redesign](observability-redesign.md) | Design notes for splitting the Observability workspace into Live ("is the system OK right now?") and Inspect ("what happened with this request?") tabs, plus cross-surface drill-down from chats / agent-chat runs / task runs to the producing trace. Tier 1 bugfix + structural split lands first as a separate PR; this RFC is the redesign that follows. Not implemented. |

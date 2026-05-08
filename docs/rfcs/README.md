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

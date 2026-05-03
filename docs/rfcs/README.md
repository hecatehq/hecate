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

# Run Evidence And Portable Memory

> **Status:** proposed; not implemented.
> **Current source of truth:** [Events](../../runtime/events.md),
> [Agent runtime](../../runtime/agent-runtime.md),
> [Chat sessions](../../runtime/chat-sessions.md),
> [Security](../../operator/security.md), and
> [Agent memory](agent-memory.md) for today's behavior.
> **Next action:** prototype hash-chained run evidence on top of existing
> task-run events before designing export/import or memory branching.

Hecate already has the right primitive for supervised agent work: every run is
local, operator-visible, approval-gated, and described by append-only events.
This record captures the next sufficient layer: make important run mutations
tamper-evident, make approvals behave like scoped capabilities, make runs
replayable from checkpoints, and make project memory portable without turning
Hecate into an opaque self-learning system.

The goal is not a new runtime substrate. The goal is a small set of operator
features that reinforce Hecate's existing local-first boundary.

## Design Posture

1. **Evidence before autonomy.** Preserve what happened, who approved it, what
   changed, and which context was sent before adding more automatic behavior.
2. **Capabilities over broad trust.** Store approvals and durable grants as
   scoped, revocable rights rather than coarse "allowed" flags.
3. **Replay over mystique.** A completed run should be reconstructable from
   events, checkpoints, artifacts, approvals, trace IDs, and diffs.
4. **Portable local state.** Operators should be able to export the relevant
   state for a project or run without leaking secrets or relying on a cloud
   account.
5. **Operator-approved memory.** Memory writes, merges, and branch promotion
   remain explicit operator actions.

## Goals

1. **Hash-chained run evidence.** Record an integrity chain over run events and
   high-risk mutations so a later inspector can detect missing, reordered, or
   modified evidence.
2. **Scoped approval capabilities.** Represent approvals and durable grants as
   rights such as `read:path`, `write:path`, `exec:command_prefix`,
   `mcp:server.tool`, or `adapter:session.option`, with expiry and revocation.
3. **Checkpointed replay.** Save compact run checkpoints at stable boundaries
   and reconstruct a human-readable timeline from a checkpoint plus subsequent
   events.
4. **Portable Hecate bundles.** Export a project/run package containing
   metadata, transcripts, memory entries, evidence records, artifacts, and
   trace references, while excluding secrets by default.
5. **Memory branches.** Let operators trial memory changes during a run, inspect
   the resulting context impact, then merge, edit, or discard them.
6. **Hybrid retrieval path.** Leave room for lexical, structured, vector, and
   graph-backed memory retrieval without making semantic recall a prerequisite
   for v1.

## Non-goals

- Building a hypervisor, kernel, container runtime, or alternate sandbox.
- Automatically mutating durable memory from transcripts or tool output.
- Treating vector search as trusted context without provenance and operator
  controls.
- Replacing OpenTelemetry, run events, task artifacts, or existing approval
  storage.
- Bundling provider credentials, external-agent private state, or unredacted
  secrets into portable exports.
- Creating a large built-in agent catalog. Hecate should stay focused on
  supervision, evidence, and controlled execution.

## Proposed Primitives

### Run Evidence Chain

Add an evidence record for each event or mutation that should be auditable:

```go
type RunEvidenceRecord struct {
    ID           string
    TaskID       string
    RunID        string
    Sequence     int64
    Kind         string
    SubjectID    string
    SubjectHash  string
    PreviousHash string
    Hash         string
    CreatedAt    time.Time
}
```

Candidate `Kind` values:

| Kind                 | Subject                                                  |
| -------------------- | -------------------------------------------------------- |
| `run_event`          | Persisted `TaskRunEvent` canonical JSON.                 |
| `approval_requested` | `TaskApproval` request body and policy reason.           |
| `approval_resolved`  | Approval decision, operator, note, and resolved time.    |
| `artifact_created`   | Artifact metadata plus content hash.                     |
| `artifact_reverted`  | Revert metadata plus before/after content hashes.        |
| `grant_created`      | External-agent durable grant scope and expiry.           |
| `grant_revoked`      | Revocation reason and parent grant reference.            |
| `context_snapshot`   | Context packet metadata and item hashes, not raw secret. |

The chain should be deterministic over canonical JSON. Store the hash and
previous hash in both memory and SQLite tiers. The evidence chain does not
replace events; it verifies their integrity.

### Approval Capabilities

Approvals and durable grants should converge on a capability-shaped internal
model:

```go
type ApprovalCapability struct {
    ID        string
    ParentID  string
    TaskID    string
    RunID     string
    Subject   string
    Rights    []string
    Scope     map[string]string
    ExpiresAt *time.Time
    RevokedAt *time.Time
    CreatedBy string
    CreatedAt time.Time
}
```

Rules:

1. A child capability cannot broaden the parent's rights.
2. Expiry and revocation apply to descendants.
3. Capabilities are visible in approval review and external-agent connection
   settings.
4. Capability checks remain advisory to the existing sandbox and runner seams;
   they do not replace WorkspaceFS, ProcessRunner, GitRunner, or adapter
   validation.

### Checkpointed Replay

Use checkpoints to make long runs inspectable and resumable without replaying
everything from sequence zero.

Checkpoint candidates:

- run queued/started;
- before model turn;
- after model turn;
- before approval wait;
- after approval resolution;
- before patch apply/revert;
- terminal run state.

The replay product should answer:

- which model/adapter/tool acted;
- what context labels were sent;
- which approval or grant authorized the action;
- which artifacts, files, or memory entries changed;
- which trace ID and span IDs provide runtime diagnostics.

### Portable Bundle

A portable bundle is an operator export, not a runtime dependency. Suggested
shape:

```text
manifest.json
project.json
chats/*.json
runs/*.json
events/*.jsonl
evidence/*.jsonl
approvals/*.jsonl
artifacts/...
memory/*.jsonl
traces/refs.jsonl
redactions.json
```

Default exclusions:

- provider API keys and encrypted secret material;
- external-agent private credentials or private memory;
- raw prompt fragments marked secret;
- filesystem paths outside the selected workspace unless explicitly approved.

The first implementation can be JSON plus content-addressed artifact files.
Signing, compression, and import conflict resolution can come later.

### Memory Branches

Memory branches let a run propose or test durable context changes without
changing active project memory immediately.

States:

| State       | Meaning                                                              |
| ----------- | -------------------------------------------------------------------- |
| `draft`     | Candidate exists only for review or the current run.                 |
| `active`    | Candidate is included in an experimental context packet.             |
| `merged`    | Operator promoted it into durable memory.                            |
| `discarded` | Operator rejected it; future context assembly ignores it by default. |

Memory branches should reuse the existing memory-candidate promotion posture:
the model can suggest, but the operator decides.

### Retrieval Graph

Hecate can build a lightweight local graph from existing records:

- project;
- chat session;
- task run;
- external-agent session;
- memory entry;
- artifact;
- approval/grant;
- touched file;
- tool call;
- provider/model.

This graph should initially power inspection and filtering, not automatic
planning. Later, it can improve memory retrieval by combining exact scope
matching, SQLite FTS, embeddings, and relationship hops.

## Suggested Implementation Order

1. Add evidence records for `TaskRunEvent` append paths only.
2. Extend evidence coverage to approvals, artifacts, and durable grants.
3. Add an evidence verifier for one run and expose status in task-run detail.
4. Add checkpoint metadata and replay helpers for completed runs.
5. Add portable export for a single run.
6. Add project-level bundle export/import without secrets.
7. Add memory branches on top of memory candidates.
8. Add hybrid retrieval only after memory provenance and context inspection are
   solid.

## Open Questions

- Should evidence records live in `taskstate` or a new package shared by task,
  chat, memory, and external-agent grants?
- Should failed attempts to write evidence fail the mutation, or should Hecate
  persist the mutation and mark the evidence chain degraded?
- Which fields need redaction before hashing, and which should hash raw bytes
  while rendering only redacted views?
- How should imports reconcile IDs when a project already exists locally?
- Should bundle signing use an operator-local key, an app-generated key, or no
  signing in v1?

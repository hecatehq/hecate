# Agent Event Protocol — Experimental Extensions

> **Status:** draft / design notes. Not implemented. Not stable. Do not build frontend contracts against this file.
> **Depends on:** [`event-protocol-v1.md`](event-protocol-v1.md).

This file holds event-protocol ideas that are probably useful, but not safe to
lock into the v1 candidate core yet. Keeping them here lets the core protocol
stay small enough to implement while preserving the product direction for CLI,
web, ACP, and IDE surfaces.

The rule of thumb: if a frontend would need special UX, special permissions, or
provider-specific behavior to render an event correctly, it starts here rather
than in the v1 candidate.

## Streamed Tool Input

Some providers are moving toward streamed tool-use input deltas. If Hecate
needs to expose that, the likely shape is:

```json
{
  "type": "assistant.tool_call_input_delta",
  "data": {
    "turn_index": 3,
    "tool_call_id": "call_01JXMZ...",
    "delta": "{\"path\":\"internal/"
  }
}
```

Open design questions:

- Do frontends need to render partial tool input, or is final
  `assistant.tool_call_proposed` enough?
- How does redaction work when the secret appears across multiple chunks?
- Should the runtime buffer and redact before emitting, even if that removes
  true streaming?

Candidate default: do not emit streamed tool input. Emit only the final redacted
tool proposal.

## Reasoning / Thinking Blocks

Reasoning content is provider-specific, may be sensitive, and may be billed
differently from normal output. It should not be part of the v1 candidate core.

Possible event names:

```text
assistant.thinking_delta
assistant.thinking_complete
```

Candidate default:

- Hidden/off by default.
- Runtime decides whether the provider response is allowed to surface.
- Frontends render it only when explicitly present and allowed.

Open design questions:

- Is the setting global, per-provider, per-model, or per-run?
- Should thinking be persisted, streamed-only, or never stored?
- How should OTel/logs treat thinking content?

## Sub-Agent Fan-Out

If a parent run spawns child runs, there are two plausible shapes:

- Parent stream emits `child_run.started` and links to the child stream.
- Parent stream interleaves child events with a `child_run_id` field.

Candidate default: link, do not interleave. Each run keeps its own monotonic
sequence and backpressure behavior.

Open design questions:

- What should a CLI show by default: collapsed child summaries or live nested
  streams?
- How does cancellation propagate from parent to children?
- Are child costs included in parent `cost.tick`, or only linked?

## Multi-Modal Output

Images, rich media, and binary model/tool outputs should reference artifacts,
not inline bytes.

Possible event:

```json
{
  "type": "assistant.image_block",
  "data": {
    "turn_index": 4,
    "artifact_id": "art_01JXMZ...",
    "mime": "image/png",
    "summary": "Generated architecture sketch"
  }
}
```

Candidate default: defer until artifact storage is candidate-stable.

## Conversation Branching

Branching is a product primitive, not just an event shape. Hecate already has
resume and retry-from-turn semantics; branch events should wait until the UI/CLI
needs a first-class branch graph.

Possible event:

```json
{
  "type": "run.branched_from",
  "data": {
    "from_run_id": "run_01JXMZ...",
    "from_sequence": 87,
    "reason": "operator explored alternate edit"
  }
}
```

Candidate default: keep using explicit run fields such as `prior_run_id` and
retry/resume endpoints. Add branch-specific events later.

## Approval Transport

The v1 candidate keeps approvals read-via-stream and write-via-REST. A future
websocket or bidirectional stream could reduce connection count, but it also
turns the event protocol into a command protocol.

Candidate default: no write side in the event stream.

Open design questions:

- Does ACP need lower-latency approval decisions than REST can provide?
- Does a bidirectional protocol complicate audit/replay semantics?
- How do reconnects dedupe approval decisions?

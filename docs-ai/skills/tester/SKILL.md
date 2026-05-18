---
name: hecate-tester
description: Use when choosing test layers, designing regression coverage, auditing test gaps, or reporting verification results. Pushes for evidence, not assumptions.
---

# Hecate tester skill

Test-strategy responses. Push for evidence, not assumptions.

## When to use

Every change that adds or modifies behavior. Also:

- Bug fix — a regression test pinning the fix is required.
- Coverage audit — what's tested, what isn't, what's risky.
- Refactor with risky surface — pin behavior before reshaping.
- SSE / streaming behavior — partial output, mid-stream cancel, reconnect.
- Sandbox-boundary changes — subprocess lifecycle, network egress.

## Test layer choice matrix

| Layer           | Use for                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Unit**        | Pure data transformation. Wire-shape passthrough. Error classification. Retry/failover decisions. Streaming wire shape (per-event SSE translation, usage accumulation). `agent_loop` tool dispatch. MCP tool registration. UI data-to-prop transformation. Conditional rendering of critical states. Form-input parsing and submit-payload shaping.                                                                                                                                           |
| **Integration** | Multi-package wiring within the gateway. Examples: governor + usage store; router + retention worker; orchestrator + taskstate.                                                                                                                                                                                                                                                                                                                                                               |
| **E2E**         | `e2e/` directory, build tag `e2e`. Required when behavior depends on the real binary running: api → orchestrator → providers chain end-to-end; subprocess lifecycle (sandbox, mcp stdio host); startup or config-loading semantics; durable sqlite behavior across restart; new SSE event sequences operators rely on; public HTTP contract changes that downstream SDKs see. UI e2e via Playwright when a journey spans multiple operator screens or depends on the real gateway responding. |

Unit tests prove the seams. Integration tests prove pairs of seams hold. E2E tests prove they fit together for real.

Verification ladders, race-suite floor, and the `bun run test` ≠ `bun test` warning are in [`../../core/verification.md`](../../core/verification.md).

## When to add new tests vs update existing

- **New behavior** → new test.
- **Bug fix** → new regression test pinning the fix. Don't just modify an existing test — that hides the regression in the diff and makes future bisecting harder.
- **Refactor with no behavior change** → tests stay the same. If they need rewriting to keep passing, the refactor changed behavior. Stop and reframe.

## Edge-case checklist

- Empty input.
- Concurrent input — race detector enabled (`-race`).
- Provider failure paths — rate-limited, auth-failed, unavailable, partial.
- Streaming partial output and mid-stream cancellation.
- Approval pause and resume around tool calls.
- Durable approval/grant behavior across process restart when SQLite is involved.
- SSE reconnect via `Last-Event-ID`.

## Regression checklist

- The bug's repro now passes.
- The race suite still passes for runtime/backend changes.
- UI typecheck and tests still pass for UI changes.
- Snapshot diffs reviewed manually — no accidental churn.

## Output shape

1. **Layer recommendation.** Unit / integration / e2e, with rationale tied to what's actually being tested.
2. **Specific test cases to add.** `file:test name`, with the assertion in plain language.
3. **Edge cases the operator might miss.** From the checklist above; pick the ones that fit.
4. **Verification ladder to run.** The exact commands.

## Bias

State **exactly what was run** (command line, not summary). State the **result** (pass/fail, with the failing line if any). State **what was NOT verified** and why — if anything risky was skipped, name it. Surface **manual smoke steps** when automation can't reach the failure mode (SSE reconnect under network blips, sandbox network egress, OTel exporter wiring, focus management, multi-tab DnD).

"Tested and passes" is not a verification report. Name what was run.

import { describe, expect, it } from "vitest";

import { buildSpanWaterfall, parseISOWithSubMs } from "./runtime-trace";
import type { TraceSpanRecord } from "../types/runtime";

// Helpers for the synthetic-trace fixtures. `at(ms)` returns an ISO
// string `ms` after the trace anchor; `span(...)` builds a minimal
// TraceSpanRecord. The bug-shaped tests intentionally feed bad data
// (unparseable starts, end-before-start, multiple roots, cycles) and
// assert the waterfall renders something sensible rather than blowing
// the whole picture apart on a single bad row.

const T0 = "2026-04-21T10:00:00.000Z";
const at = (ms: number) => new Date(Date.parse(T0) + ms).toISOString();

function span(p: Partial<TraceSpanRecord> & Pick<TraceSpanRecord, "span_id" | "name">): TraceSpanRecord {
  return {
    trace_id: "t",
    span_id: p.span_id,
    name: p.name,
    start_time: p.start_time,
    end_time: p.end_time,
    parent_span_id: p.parent_span_id,
    status_code: p.status_code,
    attributes: p.attributes,
    events: p.events,
    kind: p.kind,
  };
}

describe("buildSpanWaterfall", () => {
  it("returns an empty waterfall for empty input", () => {
    const wf = buildSpanWaterfall([]);
    expect(wf.spans).toEqual([]);
    expect(wf.totalMs).toBe(0);
    expect(wf.phases).toEqual([]);
  });

  it("emits spans in pre-order DFS so children always follow their parent", () => {
    // Tree:
    //   root (0..400)
    //     ├─ A (10..200)
    //     │   └─ A1 (50..150)
    //     └─ B (220..380)
    // Expected order: root, A, A1, B
    const spans: TraceSpanRecord[] = [
      span({ span_id: "root", name: "gateway.request", start_time: at(0), end_time: at(400) }),
      span({ span_id: "B", name: "gateway.usage", parent_span_id: "root", start_time: at(220), end_time: at(380) }),
      span({ span_id: "A", name: "provider.openai", parent_span_id: "root", start_time: at(10), end_time: at(200) }),
      span({ span_id: "A1", name: "provider.openai.parse", parent_span_id: "A", start_time: at(50), end_time: at(150) }),
    ];
    const wf = buildSpanWaterfall(spans);
    expect(wf.spans.map((s) => s.span.span_id)).toEqual(["root", "A", "A1", "B"]);
  });

  it("positions child spans at their real sub-ms offsets when timestamps have microsecond precision", () => {
    // OTel exporters using time.RFC3339Nano write sub-ms precision.
    // Date.parse truncates anything past 3 fractional digits, which
    // made child spans separated by < 1ms all collapse to startMs=0
    // — the waterfall rendered them stacked on the left edge instead
    // of at their real offsets within the parent. The fix is
    // parseISOWithSubMs.
    //
    // Trace spans 4.328ms total so the totalMs Math.max(..., 1) floor
    // doesn't dominate the assertions; child offsets are 0.043 /
    // 0.926 / 2.442 ms — all sub-1ms-truncation territory pre-fix.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "root", name: "gateway.request", start_time: "2026-05-11T06:14:05.428427Z", end_time: "2026-05-11T06:14:05.432755Z" }),
      span({ span_id: "parse", name: "gateway.request.parse", parent_span_id: "root", start_time: "2026-05-11T06:14:05.428470Z", end_time: "2026-05-11T06:14:05.428470Z" }),
      span({ span_id: "gov", name: "gateway.governor", parent_span_id: "root", start_time: "2026-05-11T06:14:05.429353Z", end_time: "2026-05-11T06:14:05.432755Z" }),
      span({ span_id: "rtr", name: "gateway.router", parent_span_id: "root", start_time: "2026-05-11T06:14:05.430869Z", end_time: "2026-05-11T06:14:05.430908Z" }),
    ];
    const wf = buildSpanWaterfall(spans);
    const byID = Object.fromEntries(wf.spans.map((s) => [s.span.span_id, s]));
    // Each child's startMs must be its own offset, NOT zero.
    expect(byID.parse.startMs).toBeCloseTo(0.043, 2);
    expect(byID.gov.startMs).toBeCloseTo(0.926, 2);
    expect(byID.rtr.startMs).toBeCloseTo(2.442, 2);
    // And the trace total spans the parent's real duration.
    expect(wf.totalMs).toBeCloseTo(4.328, 2);
  });

  it("parseISOWithSubMs preserves microsecond precision", () => {
    // Date.parse drops past 3 digits — both of these would return the
    // same ms-precision base. parseISOWithSubMs must distinguish them.
    const a = parseISOWithSubMs("2026-05-11T06:14:05.428427Z");
    const b = parseISOWithSubMs("2026-05-11T06:14:05.429339Z");
    expect(b - a).toBeCloseTo(0.912, 3);
    // Same instant expressed with a non-Z timezone parses identically.
    const offset = parseISOWithSubMs("2026-05-11T08:14:05.428427+02:00");
    expect(offset).toBeCloseTo(a, 3);
    // Unparseable input falls through to Date.parse's NaN.
    expect(Number.isNaN(parseISOWithSubMs("not-a-timestamp"))).toBe(true);
    // Plain ms-precision input still works, same as Date.parse.
    expect(parseISOWithSubMs("2026-05-11T06:14:05.428Z"))
      .toBe(Date.parse("2026-05-11T06:14:05.428Z"));
  });

  it("scales valid sub-ms traces to their real latest end, not a 1ms floor", () => {
    const spans: TraceSpanRecord[] = [
      span({
        span_id: "root",
        name: "gateway.request",
        start_time: "2026-05-11T06:14:05.428000Z",
        end_time: "2026-05-11T06:14:05.428090Z",
      }),
      span({
        span_id: "router",
        name: "gateway.router",
        parent_span_id: "root",
        start_time: "2026-05-11T06:14:05.428050Z",
        end_time: "2026-05-11T06:14:05.428090Z",
      }),
    ];
    const wf = buildSpanWaterfall(spans);
    expect(wf.totalMs).toBeCloseTo(0.09, 3);
    const root = wf.spans.find((s) => s.span.span_id === "root")!;
    expect(root.startMs + root.durMs).toBeCloseTo(wf.totalMs, 3);
  });

  it("flags spans that have at least one child via hasChildren", () => {
    // Trace from the dev server: gateway.request is the root and the
    // other three are its direct children. Only the root should have
    // hasChildren=true; the leaves stay false. This drives the
    // outline-vs-filled bar style in SpanWaterfall.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "root", name: "gateway.request", start_time: at(0), end_time: at(912) }),
      span({ span_id: "parse", name: "gateway.request.parse", parent_span_id: "root", start_time: at(43), end_time: at(43) }),
      span({ span_id: "gov", name: "gateway.governor", parent_span_id: "root", start_time: at(126), end_time: at(912) }),
      span({ span_id: "rtr", name: "gateway.router", parent_span_id: "root", start_time: at(442), end_time: at(481) }),
    ];
    const wf = buildSpanWaterfall(spans);
    const byID = Object.fromEntries(wf.spans.map((s) => [s.span.span_id, s]));
    expect(byID.root.hasChildren).toBe(true);
    expect(byID.parse.hasChildren).toBe(false);
    expect(byID.gov.hasChildren).toBe(false);
    expect(byID.rtr.hasChildren).toBe(false);
  });

  it("emits a child after its parent in DFS order even when clock skew puts the child's start earlier", () => {
    // Even though A's start (-50) is earlier than root's (0), A must
    // still render below root because of the parent→child relationship.
    // The previous (startMs, depth) sort orphaned A above its parent.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "root", name: "gateway.request", start_time: at(0), end_time: at(400) }),
      span({ span_id: "A", name: "provider.openai", parent_span_id: "root", start_time: at(-50), end_time: at(380) }),
    ];
    const wf = buildSpanWaterfall(spans);
    expect(wf.spans.map((s) => s.span.span_id)).toEqual(["root", "A"]);
  });

  it("does not let one unparseable timestamp blow up t0/totalMs for all other spans", () => {
    // The previous implementation defaulted bad start_time to 0, then
    // Math.min(...) picked 0 as t0. Every other span's startMs became
    // ~1.7e12 — sub-pixel everywhere except the bad span's own row.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "root", name: "gateway.request", start_time: at(0), end_time: at(400) }),
      span({ span_id: "good", name: "provider.openai", parent_span_id: "root", start_time: at(50), end_time: at(380) }),
      span({ span_id: "bad", name: "tool.shell", parent_span_id: "root", start_time: "not-a-date", end_time: "also-not" }),
    ];
    const wf = buildSpanWaterfall(spans);
    expect(wf.totalMs).toBe(400);
    const good = wf.spans.find((s) => s.span.span_id === "good")!;
    expect(good.startMs).toBe(50);
    expect(good.durMs).toBe(330);
    expect(good.unknownTiming).toBe(false);
    const bad = wf.spans.find((s) => s.span.span_id === "bad")!;
    expect(bad.unknownTiming).toBe(true);
    expect(Number.isNaN(bad.startMs)).toBe(true);
  });

  it("flags spans with valid start but unparseable end as unknownTiming, not 0ms", () => {
    // The previous code defaulted endRaw to startRaw when the
    // end_time string was bad, silently producing a 0ms span. The
    // doc comment said "missing/unparseable start_time *or*
    // end_time" should be flagged — code now matches doc. Both
    // startMs and durMs are NaN so a caller that forgets to check
    // unknownTiming can't accidentally position a bar at a known
    // start with an unknown width.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "root", name: "gateway.request", start_time: at(0), end_time: at(400) }),
      span({ span_id: "halfBad", name: "tool.shell", parent_span_id: "root", start_time: at(50), end_time: "garbage" }),
    ];
    const wf = buildSpanWaterfall(spans);
    const halfBad = wf.spans.find((s) => s.span.span_id === "halfBad")!;
    expect(halfBad.unknownTiming).toBe(true);
    expect(Number.isNaN(halfBad.startMs)).toBe(true);
    expect(Number.isNaN(halfBad.durMs)).toBe(true);
  });

  it("includes negative-duration span starts in totalMs so they don't render off-scale", () => {
    // Without including starts in totalMs, a clock-skew span with
    // start=200ms and end=150ms in a trace whose latest valid end is
    // 150ms would render at leftPct=200/150=133% — past the right
    // edge of the bar column. totalMs covers max(starts, ends) so
    // the renderer always has a valid range for any span's position.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "root", name: "gateway.request", start_time: at(0), end_time: at(150) }),
      span({ span_id: "skewLate", name: "tool.shell", parent_span_id: "root", start_time: at(200), end_time: at(150) }),
    ];
    const wf = buildSpanWaterfall(spans);
    expect(wf.totalMs).toBe(200);
    const skewLate = wf.spans.find((s) => s.span.span_id === "skewLate")!;
    expect(skewLate.startMs).toBe(200);
    expect(skewLate.negativeDuration).toBe(true);
  });

  it("flags negative-duration spans rather than silently clamping to 1ms", () => {
    // Codex-like sidecar with subprocess clock drift can emit
    // end_time < start_time. The previous code clamped this to a
    // 1ms tick visually indistinguishable from a real one.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "root", name: "gateway.request", start_time: at(0), end_time: at(400) }),
      span({ span_id: "skewed", name: "tool.shell", parent_span_id: "root", start_time: at(100), end_time: at(50) }),
    ];
    const wf = buildSpanWaterfall(spans);
    const skewed = wf.spans.find((s) => s.span.span_id === "skewed")!;
    expect(skewed.negativeDuration).toBe(true);
    expect(skewed.durMs).toBe(-50);
    expect(skewed.unknownTiming).toBe(false);
  });

  it("computes critical path via longest-descent DP, not longest single child", () => {
    // root has two children:
    //   short_with_long_chain: 60ms own, descendant chain adds 200ms (260ms total)
    //   long_alone:            80ms own, no descendants            (80ms total)
    //
    // The previous "longest single child" heuristic picked
    // long_alone (80 > 60). The DP picks short_with_long_chain
    // (260 > 80). The critical chain runs through that branch.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "root", name: "gateway.request", start_time: at(0), end_time: at(400) }),
      span({ span_id: "short_with_long_chain", name: "router.select", parent_span_id: "root", start_time: at(0), end_time: at(60) }),
      span({ span_id: "deep1", name: "provider.openai", parent_span_id: "short_with_long_chain", start_time: at(10), end_time: at(200) }),
      span({ span_id: "deep2", name: "provider.openai.parse", parent_span_id: "deep1", start_time: at(20), end_time: at(190) }),
      span({ span_id: "long_alone", name: "gateway.usage", parent_span_id: "root", start_time: at(60), end_time: at(140) }),
    ];
    const wf = buildSpanWaterfall(spans);
    const critIDs = wf.spans.filter((s) => s.critical).map((s) => s.span.span_id).sort();
    expect(critIDs).toEqual(["deep1", "deep2", "root", "short_with_long_chain"].sort());
  });

  it("highlights the critical path of every root in a multi-root trace", () => {
    // Two unrelated subtrees (retention pruned the common ancestor).
    // Both their longest-descent chains must be marked, not just the
    // first root's.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "rootA", name: "gateway.request", start_time: at(0), end_time: at(400) }),
      span({ span_id: "A1", name: "provider.openai", parent_span_id: "rootA", start_time: at(10), end_time: at(380) }),
      span({ span_id: "rootB", name: "gateway.request", start_time: at(500), end_time: at(900) }),
      span({ span_id: "B1", name: "provider.anthropic", parent_span_id: "rootB", start_time: at(510), end_time: at(880) }),
    ];
    const wf = buildSpanWaterfall(spans);
    const critIDs = new Set(wf.spans.filter((s) => s.critical).map((s) => s.span.span_id));
    expect(critIDs.has("rootA")).toBe(true);
    expect(critIDs.has("A1")).toBe(true);
    expect(critIDs.has("rootB")).toBe(true);
    expect(critIDs.has("B1")).toBe(true);
  });

  it("treats spans with a missing parent in the visible set as roots", () => {
    // The parent span was pruned by retention or never sampled. Its
    // children should still render rather than disappear.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "orphan", name: "provider.openai", parent_span_id: "missing", start_time: at(10), end_time: at(380) }),
    ];
    const wf = buildSpanWaterfall(spans);
    expect(wf.spans).toHaveLength(1);
    expect(wf.spans[0].depth).toBe(0);
    expect(wf.spans[0].critical).toBe(true); // sole root
  });

  it("survives a parent_span_id cycle without infinite recursion", () => {
    // Corrupt-data guard. Two spans claiming each other as parent.
    // The cycle guards in depthOf and descent should clamp gracefully
    // and let buildSpanWaterfall return.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "A", name: "spanA", parent_span_id: "B", start_time: at(0), end_time: at(100) }),
      span({ span_id: "B", name: "spanB", parent_span_id: "A", start_time: at(0), end_time: at(100) }),
    ];
    const wf = buildSpanWaterfall(spans);
    expect(wf.spans).toHaveLength(2);
    // Depth assignment under a cycle is "best effort, don't loop"; we
    // just assert it terminated and produced finite depths.
    for (const s of wf.spans) {
      expect(Number.isFinite(s.depth)).toBe(true);
    }
  });

  it("falls back to a 1ms total when every span has unparseable timestamps", () => {
    // Edge case: trace store mangled every ISO timestamp. We don't
    // throw, we don't divide-by-zero — we render a degenerate
    // waterfall that at least lists the spans.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "A", name: "spanA", start_time: "garbage", end_time: "garbage" }),
      span({ span_id: "B", name: "spanB", parent_span_id: "A", start_time: "garbage", end_time: "garbage" }),
    ];
    const wf = buildSpanWaterfall(spans);
    expect(wf.totalMs).toBe(1);
    expect(wf.spans).toHaveLength(2);
    for (const s of wf.spans) {
      expect(s.unknownTiming).toBe(true);
      expect(Number.isNaN(s.startMs)).toBe(true);
    }
  });

  it("preserves the existing 'long span on root's longest descent is critical' contract", () => {
    // Sanity: the basic three-span shape from runtime-utils.test.ts
    // keeps working — root + a long provider call + a short usage
    // call, only the long one is critical.
    const spans: TraceSpanRecord[] = [
      span({ span_id: "root", name: "gateway.request", start_time: at(0), end_time: at(400) }),
      span({ span_id: "long", name: "provider.openai", parent_span_id: "root", start_time: at(50), end_time: at(380) }),
      span({ span_id: "short", name: "gateway.usage", parent_span_id: "root", start_time: at(385), end_time: at(395) }),
    ];
    const wf = buildSpanWaterfall(spans);
    const long = wf.spans.find((s) => s.span.span_id === "long")!;
    const short = wf.spans.find((s) => s.span.span_id === "short")!;
    expect(long.critical).toBe(true);
    expect(short.critical).toBe(false);
  });
});

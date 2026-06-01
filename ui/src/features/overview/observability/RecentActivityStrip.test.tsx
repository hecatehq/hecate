import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";

import type { TraceListItem } from "../../../types/trace";

import { RecentActivityStrip } from "./RecentActivityStrip";

function trace(p: Partial<TraceListItem> & Pick<TraceListItem, "request_id">): TraceListItem {
  return {
    request_id: p.request_id,
    started_at: p.started_at ?? new Date().toISOString(),
    span_count: p.span_count ?? 1,
    duration_ms: p.duration_ms,
    status_code: p.status_code,
    status_message: p.status_message,
    route: p.route,
  } as TraceListItem;
}

describe("RecentActivityStrip", () => {
  it("renders nothing when there are no traces", () => {
    const { container } = render(<RecentActivityStrip traces={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it("renders one dot per trace", () => {
    const traces = [
      trace({ request_id: "a", duration_ms: 100, status_code: "ok" }),
      trace({ request_id: "b", duration_ms: 200, status_code: "error" }),
      trace({ request_id: "c", duration_ms: 300, status_code: "ok" }),
    ];
    const { container } = render(<RecentActivityStrip traces={traces} />);
    // The dots are the only spans whose title carries request metadata.
    const dots = container.querySelectorAll('span[title*="/"]');
    expect(dots.length).toBe(3);
  });

  it("computes p50/p95 from durations and counts errors", () => {
    // Latencies: 10, 20, 30, 40, 50 → p50 picks index ceil(5*0.5)-1=2 (30ms);
    // p95 picks index ceil(5*0.95)-1=4 (50ms).
    const traces = [
      trace({ request_id: "a", duration_ms: 30, status_code: "ok" }),
      trace({ request_id: "b", duration_ms: 50, status_code: "error" }),
      trace({ request_id: "c", duration_ms: 10, status_code: "ok" }),
      trace({ request_id: "d", duration_ms: 40, status_code: "ok" }),
      trace({ request_id: "e", duration_ms: 20, status_code: "ok" }),
    ];
    const { container } = render(<RecentActivityStrip traces={traces} />);
    const text = container.textContent || "";
    expect(text).toMatch(/p50.*30ms/);
    expect(text).toMatch(/p95.*50ms/);
    expect(text).toMatch(/errors.*1.*\/.*5/);
  });

  it("uses request-level latency overrides for summary and dot tooltips", () => {
    const traces = [
      trace({ request_id: "a", duration_ms: 1, status_code: "ok" }),
      trace({ request_id: "b", duration_ms: 2, status_code: "ok" }),
    ];
    const latencyByRequestID = new Map([
      ["a", 100],
      ["b", 300],
    ]);
    const { container } = render(
      <RecentActivityStrip traces={traces} latencyByRequestID={latencyByRequestID} />,
    );
    const text = container.textContent || "";
    expect(text).toMatch(/p50.*100ms/);
    expect(text).toMatch(/p95.*300ms/);
    expect(container.querySelector('span[title*="a · —/— · 100ms"]')).toBeTruthy();
  });

  it("shows recovered count when a trace has fallback_from", () => {
    const traces = [
      trace({
        request_id: "a",
        duration_ms: 100,
        status_code: "ok",
        route: { fallback_from: "openai" },
      }),
      trace({ request_id: "b", duration_ms: 200, status_code: "ok" }),
    ];
    const { container } = render(<RecentActivityStrip traces={traces} />);
    expect(container.textContent).toMatch(/recovered.*1/);
  });

  it("uses derived provider/model labels in dot tooltips when route fields are missing", () => {
    const traces = [trace({ request_id: "req-1", duration_ms: 25, status_code: "ok" })];
    const labelsByRequestID = new Map([
      ["req-1", { provider: "ollama", model: "ministral-3:latest" }],
    ]);

    const { container } = render(
      <RecentActivityStrip traces={traces} labelsByRequestID={labelsByRequestID} />,
    );

    expect(container.querySelector('span[title*="ollama/ministral-3:latest"]')).toBeTruthy();
  });

  it("does not count error+fallback_from as recovered (the fallback also failed)", () => {
    // A trace with both status_code="error" and fallback_from means
    // the primary failed AND the fallback failed. That's a double-
    // fault, not a recovery — the dot is red, the error count
    // increments, the recovered count does not.
    const traces = [
      trace({
        request_id: "a",
        duration_ms: 100,
        status_code: "error",
        route: { fallback_from: "openai" },
      }),
      trace({
        request_id: "b",
        duration_ms: 200,
        status_code: "ok",
        route: { fallback_from: "openai" },
      }),
    ];
    const { container } = render(<RecentActivityStrip traces={traces} />);
    const text = container.textContent || "";
    expect(text).toMatch(/errors.*1.*\/.*2/);
    // Only the second trace recovered; the first double-faulted.
    expect(text).toMatch(/recovered.*1/);
    expect(text).not.toMatch(/recovered.*2/);
  });
});

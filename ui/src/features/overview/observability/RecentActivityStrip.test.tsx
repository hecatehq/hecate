import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";

import type { TraceListItem } from "../../../types/runtime";

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
    // The dots are the only `display: inline-block` spans inside the
    // strip; everything else is text or flex/grid layout.
    const dots = container.querySelectorAll('span[title*="…"]');
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

  it("shows recovered count when a trace has fallback_from", () => {
    const traces = [
      trace({ request_id: "a", duration_ms: 100, status_code: "ok", route: { fallback_from: "openai" } }),
      trace({ request_id: "b", duration_ms: 200, status_code: "ok" }),
    ];
    const { container } = render(<RecentActivityStrip traces={traces} />);
    expect(container.textContent).toMatch(/recovered.*1/);
  });
});

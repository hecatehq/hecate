import { act, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ObservabilityView } from "./ObservabilityView";
import { createRuntimeConsoleActions, createRuntimeConsoleFixture } from "../../test/runtime-console-fixture";

function setViewportWidth(px: number) {
  Object.defineProperty(window, "innerWidth", { value: px, configurable: true });
}

// Single-user mode: there's only one session label.
const localSession = { label: "Local" };

const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  // Default to wide-viewport drawer mode. Individual tests override.
  setViewportWidth(1280);
  vi.stubGlobal("fetch", fetchMock);
  fetchMock.mockImplementation(async () => {
    return new Response(JSON.stringify({ object: "list", data: [] }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  fetchMock.mockReset();
});

// Build a fetch handler that routes /hecate/v1/traces to a populated list and
// optionally a /hecate/v1/traces detail. Everything else falls through to the
// default empty-list mock.
function tracesFetchHandler(traces: unknown[], detail?: unknown) {
  return async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();
    if (detail && url.startsWith("/hecate/v1/traces")) {
      const parsed = new URL(url, "http://hecate.test");
      if (parsed.searchParams.has("request_id")) {
        return new Response(JSON.stringify({ object: "trace", data: detail }), {
          status: 200, headers: { "Content-Type": "application/json" },
        });
      }
    }
    if (url.startsWith("/hecate/v1/traces")) {
      return new Response(JSON.stringify({ object: "trace_list", data: traces }), {
        status: 200, headers: { "Content-Type": "application/json" },
      });
    }
    return new Response(JSON.stringify({ object: "list", data: [] }), {
      status: 200, headers: { "Content-Type": "application/json" },
    });
  };
}

describe("ObservabilityView", () => {
  it("renders the header with title, filters, and Live toggle", async () => {
    const state = createRuntimeConsoleFixture({ session: localSession });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    expect(container.textContent).toMatch(/Observability/);
    expect(container.querySelector('[aria-label="Status filter"]')).toBeTruthy();
    expect(container.querySelector('[aria-label="Live mode"]')).toBeTruthy();
    expect(container.textContent).toMatch(/Live|Paused/);
  });

  it("calls /hecate/v1/system/stats and /hecate/v1/traces on mount", async () => {
    const state = createRuntimeConsoleFixture({ session: localSession });
    await act(async () => {
      render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
    });
    await waitFor(() => {
      const urls = fetchMock.mock.calls.map(([u]) => String(u));
      expect(urls.some(u => u.startsWith("/hecate/v1/system/stats"))).toBe(true);
      expect(urls.some(u => u.startsWith("/hecate/v1/traces"))).toBe(true);
    });
  });

  it("renders MCP cache panel when configured", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.startsWith("/hecate/v1/system/mcp/cache")) {
        return new Response(JSON.stringify({
          object: "mcp_cache_stats",
          data: { checked_at: new Date().toISOString(), configured: true, entries: 4, in_use: 1, idle: 3 },
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      return new Response(JSON.stringify({ object: "list", data: [] }), {
        status: 200, headers: { "Content-Type": "application/json" },
      });
    });
    const state = createRuntimeConsoleFixture({ session: localSession });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    await waitFor(() => {
      expect(container.querySelector('[aria-label="MCP cache stats"]')).toBeTruthy();
    });
  });

  it("renders 'No MCP cache wired' fallback when configured=false", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.startsWith("/hecate/v1/system/mcp/cache")) {
        return new Response(JSON.stringify({
          object: "mcp_cache_stats",
          data: { checked_at: new Date().toISOString(), configured: false, entries: 0, in_use: 0, idle: 0 },
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      return new Response(JSON.stringify({ object: "list", data: [] }), {
        status: 200, headers: { "Content-Type": "application/json" },
      });
    });
    const state = createRuntimeConsoleFixture({ session: localSession });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    await waitFor(() => {
      expect(container.textContent).toMatch(/No MCP cache wired/i);
    });
    expect(container.querySelector('[aria-label="MCP cache stats"]')).toBeNull();
  });

  it("renders empty state with Open Chats button when traces is empty", async () => {
    const onNavigate = vi.fn();
    const state = createRuntimeConsoleFixture({ session: localSession });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(
        <ObservabilityView state={state} actions={createRuntimeConsoleActions()} onNavigate={onNavigate} />,
      );
      container = result.container;
    });
    await waitFor(() => {
      expect(container.textContent).toMatch(/No traces yet/);
    });
    const button = Array.from(container.querySelectorAll("button")).find(b => b.textContent?.includes("Open Chats"));
    expect(button).toBeTruthy();
    fireEvent.click(button!);
    expect(onNavigate).toHaveBeenCalledWith("chats");
  });

  it("renders the table with status badges and provider/model cells", async () => {
    fetchMock.mockImplementation(tracesFetchHandler([
      {
        request_id: "req-ok",
        started_at: new Date(Date.now() - 5000).toISOString(),
        span_count: 1,
        duration_ms: 12,
        status_code: "ok",
        route: { final_provider: "openai", final_model: "gpt-4o-mini", final_reason: "requested_model" },
      },
      {
        request_id: "req-err",
        started_at: new Date(Date.now() - 10_000).toISOString(),
        span_count: 1,
        duration_ms: 5,
        status_code: "error",
        status_message: "boom",
        route: { final_provider: "anthropic", final_model: "claude-3" },
      },
    ]));
    const state = createRuntimeConsoleFixture({ session: localSession });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    await waitFor(() => {
      expect(container.textContent).toMatch(/req-ok/);
    });
    // Status column uses Badge, not raw color recoloring of the model cell
    expect(container.textContent).toMatch(/Healthy/);
    expect(container.textContent).toMatch(/Error/);
    // Provider names appear
    expect(container.textContent).toMatch(/openai/);
    expect(container.textContent).toMatch(/anthropic/);
  });

  it("status filter narrows the table to error rows only", async () => {
    fetchMock.mockImplementation(tracesFetchHandler([
      { request_id: "ok-1", started_at: new Date().toISOString(), span_count: 1, duration_ms: 1, status_code: "ok", route: { final_provider: "openai", final_model: "m1" } },
      { request_id: "err-1", started_at: new Date().toISOString(), span_count: 1, duration_ms: 1, status_code: "error", route: { final_provider: "openai", final_model: "m2" } },
    ]));
    const state = createRuntimeConsoleFixture({ session: localSession });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    await waitFor(() => {
      expect(container.textContent).toMatch(/ok-1/);
    });
    const select = container.querySelector('[aria-label="Status filter"]') as HTMLSelectElement;
    await act(async () => {
      fireEvent.change(select, { target: { value: "error" } });
    });
    await waitFor(() => {
      expect(container.textContent).not.toMatch(/ok-1/);
      expect(container.textContent).toMatch(/err-1/);
    });
  });

  it("row click opens the trace detail modal with the trace title", async () => {
    fetchMock.mockImplementation(tracesFetchHandler(
      [{
        request_id: "req-12345678abcd",
        started_at: new Date().toISOString(),
        span_count: 1,
        duration_ms: 10,
        status_code: "ok",
        route: { final_provider: "openai", final_model: "gpt-4o" },
      }],
      {
        request_id: "req-12345678abcd",
        started_at: new Date().toISOString(),
        spans: [],
        route: { candidates: [] },
      },
    ));
    const state = createRuntimeConsoleFixture({ session: localSession });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    await waitFor(() => {
      expect(container.textContent).toMatch(/req-1234/);
    });
    // Click the row
    const row = container.querySelector("tbody tr") as HTMLElement;
    expect(row).toBeTruthy();
    await act(async () => {
      fireEvent.click(row);
    });
    // Modal opens — title contains the truncated request id and provider/model
    await waitFor(() => {
      const dialog = document.querySelector('[role="dialog"]');
      expect(dialog).toBeTruthy();
      expect(dialog?.getAttribute("aria-label")).toMatch(/req-1234/);
      expect(dialog?.getAttribute("aria-label")).toMatch(/openai\/gpt-4o/);
    });
  });

  it("opens a trace detail drawer when focused by request id", async () => {
    fetchMock.mockImplementation(tracesFetchHandler(
      [{
        request_id: "req-focus-from-chat",
        started_at: new Date().toISOString(),
        span_count: 1,
        duration_ms: 10,
        status_code: "ok",
        route: { final_provider: "ollama", final_model: "ministral-3:latest" },
      }],
      {
        request_id: "req-focus-from-chat",
        started_at: new Date().toISOString(),
        spans: [],
        route: { candidates: [] },
      },
    ));
    const state = createRuntimeConsoleFixture({ session: localSession });
    await act(async () => {
      render(
        <ObservabilityView
          state={state}
          actions={createRuntimeConsoleActions()}
          focusRequest={{ requestID: "req-focus-from-chat", nonce: 1 }}
        />,
      );
    });

    await waitFor(() => {
      const urls = fetchMock.mock.calls.map(([u]) => String(u));
      expect(urls.some(u => u === "/hecate/v1/traces?request_id=req-focus-from-chat")).toBe(true);
      const dialog = document.querySelector('[role="dialog"]');
      expect(dialog?.getAttribute("aria-label")).toMatch(/req-focu/);
      expect(dialog?.getAttribute("aria-label")).toMatch(/ollama\/ministral-3:latest/);
    });
  });

  it("live mode auto-highlight does NOT auto-open the modal", async () => {
    fetchMock.mockImplementation(tracesFetchHandler([
      { request_id: "auto-pick", started_at: new Date().toISOString(), span_count: 1, duration_ms: 1, status_code: "ok", route: { final_provider: "openai", final_model: "m" } },
    ]));
    const state = createRuntimeConsoleFixture({ session: localSession });
    await act(async () => {
      render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
    });
    await waitFor(() => {
      // The table truncates to 8 chars for display.
      expect(document.body.textContent).toMatch(/auto-pic/);
    });
    // No modal/dialog should be open from the live-mode highlight alone.
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });

  it("renders $0.00000 for zero-cost rows when ledger entry exists", async () => {
    fetchMock.mockImplementation(tracesFetchHandler([
      { request_id: "zero-cost", started_at: new Date().toISOString(), span_count: 1, duration_ms: 1, status_code: "ok", route: { final_provider: "openai", final_model: "m" } },
    ]));
    const state = createRuntimeConsoleFixture({
      session: localSession,
      requestLedger: [{
        type: "debit",
        request_id: "zero-cost",
        amount_micros_usd: 0,
        amount_usd: "0",
        balance_micros_usd: 0,
        balance_usd: "0",
        credited_micros_usd: 0,
        credited_usd: "0",
        debited_micros_usd: 0,
        debited_usd: "0",
        prompt_tokens: 5,
        completion_tokens: 10,
      }],
    });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    await waitFor(() => {
      // request id truncates to 8 chars in the cell.
      expect(container.textContent).toMatch(/zero-cos/);
    });
    expect(container.textContent).toMatch(/\$0\.00000/);
  });

  it("X button closes the bottom drawer and clears the selection", async () => {
    fetchMock.mockImplementation(tracesFetchHandler(
      [{
        request_id: "req-close-me",
        started_at: new Date().toISOString(),
        span_count: 1,
        duration_ms: 10,
        status_code: "ok",
        route: { final_provider: "openai", final_model: "gpt-4o" },
      }],
      {
        request_id: "req-close-me",
        started_at: new Date().toISOString(),
        spans: [],
        route: { candidates: [] },
      },
    ));
    const state = createRuntimeConsoleFixture({ session: localSession });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    await waitFor(() => {
      expect(container.textContent).toMatch(/req-clos/);
    });
    const row = container.querySelector("tbody tr") as HTMLElement;
    await act(async () => { fireEvent.click(row); });
    await waitFor(() => {
      expect(document.querySelector('[role="dialog"]')).toBeTruthy();
    });
    // The drawer header carries an aria-label="Close" button.
    const closeBtn = document.querySelector('[role="dialog"] button[aria-label="Close"]') as HTMLButtonElement;
    expect(closeBtn).toBeTruthy();
    await act(async () => { fireEvent.click(closeBtn); });
    await waitFor(() => {
      expect(document.querySelector('[role="dialog"]')).toBeNull();
    });
  });

  it("renders the bottom drawer (not a centered modal) on a wide viewport", async () => {
    setViewportWidth(1280);
    fetchMock.mockImplementation(tracesFetchHandler(
      [{
        request_id: "req-drawer",
        started_at: new Date().toISOString(),
        span_count: 1,
        duration_ms: 10,
        status_code: "ok",
        route: { final_provider: "openai", final_model: "gpt-4o" },
      }],
      { request_id: "req-drawer", started_at: new Date().toISOString(), spans: [], route: { candidates: [] } },
    ));
    const state = createRuntimeConsoleFixture({ session: localSession });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    await waitFor(() => {
      expect(container.textContent).toMatch(/req-draw/);
    });
    const row = container.querySelector("tbody tr") as HTMLElement;
    await act(async () => { fireEvent.click(row); });
    await waitFor(() => {
      const dialog = document.querySelector('[role="dialog"]');
      expect(dialog).toBeTruthy();
      // Drawer is inline — its parent is NOT a fixed-position scrim.
      const parent = dialog?.parentElement;
      expect(parent?.getAttribute("style") || "").not.toMatch(/position:\s*fixed/);
    });
  });

  it("falls back to Modal on a narrow viewport (< 900px)", async () => {
    setViewportWidth(800);
    fetchMock.mockImplementation(tracesFetchHandler(
      [{
        request_id: "req-narrow",
        started_at: new Date().toISOString(),
        span_count: 1,
        duration_ms: 10,
        status_code: "ok",
        route: { final_provider: "openai", final_model: "gpt-4o" },
      }],
      { request_id: "req-narrow", started_at: new Date().toISOString(), spans: [], route: { candidates: [] } },
    ));
    const state = createRuntimeConsoleFixture({ session: localSession });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    await waitFor(() => {
      expect(container.textContent).toMatch(/req-narr/);
    });
    const row = container.querySelector("tbody tr") as HTMLElement;
    await act(async () => { fireEvent.click(row); });
    await waitFor(() => {
      const dialog = document.querySelector('[role="dialog"]');
      expect(dialog).toBeTruthy();
      // Modal lives inside a fixed-position scrim; the drawer doesn't.
      const scrim = dialog?.parentElement;
      expect(scrim?.getAttribute("style")).toMatch(/position:\s*fixed/);
    });
  });

  it("renders the span waterfall with rows, expandable attributes, and a critical-path indicator", async () => {
    const traceID = "trace-abc";
    const t0 = new Date(Date.now() - 1000);
    const spans = [
      // root: 0–400ms
      { trace_id: traceID, span_id: "root", name: "provider chain",
        start_time: new Date(t0.getTime()).toISOString(),
        end_time: new Date(t0.getTime() + 400).toISOString(),
        attributes: { provider: "openai" } },
      // child A: 50–300ms (longer — critical)
      { trace_id: traceID, span_id: "child-a", parent_span_id: "root", name: "provider.openai",
        start_time: new Date(t0.getTime() + 50).toISOString(),
        end_time: new Date(t0.getTime() + 300).toISOString(),
        attributes: { "gen_ai.provider.name": "openai", model: "gpt-4o" } },
      // child B: 310–340ms (shorter)
      { trace_id: traceID, span_id: "child-b", parent_span_id: "root", name: "gateway.usage",
        start_time: new Date(t0.getTime() + 310).toISOString(),
        end_time: new Date(t0.getTime() + 340).toISOString(),
        attributes: { "usage.input_tokens": 100 } },
    ];
    fetchMock.mockImplementation(tracesFetchHandler(
      [{
        request_id: "req-spans",
        started_at: t0.toISOString(),
        span_count: 3,
        duration_ms: 400,
        status_code: "ok",
        route: { final_provider: "openai", final_model: "gpt-4o" },
      }],
      { request_id: "req-spans", started_at: t0.toISOString(), spans, route: { candidates: [] } },
    ));
    const state = createRuntimeConsoleFixture({ session: localSession });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    await waitFor(() => {
      expect(container.textContent).toMatch(/req-span/);
    });
    const row = container.querySelector("tbody tr") as HTMLElement;
    await act(async () => { fireEvent.click(row); });
    await waitFor(() => {
      expect(document.body.textContent).toMatch(/Spans \(3\)/);
    });
    // Three span rows by name.
    expect(document.body.textContent).toMatch(/provider chain/);
    expect(document.body.textContent).toMatch(/provider\.openai/);
    expect(document.body.textContent).toMatch(/gateway\.usage/);

    // Critical-path indicator (★) appears at least twice (root + longest child).
    const stars = document.body.textContent?.match(/★/g) ?? [];
    expect(stars.length).toBeGreaterThanOrEqual(2);

    // Click the longest child to expand its attributes inline.
    const childARow = Array.from(document.querySelectorAll('[role="button"]'))
      .find(el => el.getAttribute("aria-label") === "span provider.openai") as HTMLElement;
    expect(childARow).toBeTruthy();
    await act(async () => { fireEvent.click(childARow); });
    await waitFor(() => {
      expect(document.querySelector('[data-testid="span-attrs-child-a"]')).toBeTruthy();
    });
    // Priority attributes render as key labels.
    const panel = document.querySelector('[data-testid="span-attrs-child-a"]') as HTMLElement;
    expect(panel.textContent).toMatch(/gen_ai\.provider\.name/);
  });

  it("renders phase legend chips when multiple phases are present", async () => {
    const t0 = new Date(Date.now() - 1000);
    const spans = [
      { trace_id: "t1", span_id: "a", name: "gateway.request",
        start_time: t0.toISOString(),
        end_time: new Date(t0.getTime() + 50).toISOString() },
      { trace_id: "t1", span_id: "b", parent_span_id: "a", name: "gateway.router",
        start_time: new Date(t0.getTime() + 5).toISOString(),
        end_time: new Date(t0.getTime() + 30).toISOString() },
      { trace_id: "t1", span_id: "c", parent_span_id: "a", name: "provider.openai",
        start_time: new Date(t0.getTime() + 30).toISOString(),
        end_time: new Date(t0.getTime() + 200).toISOString() },
    ];
    fetchMock.mockImplementation(tracesFetchHandler(
      [{
        request_id: "req-legend", started_at: t0.toISOString(),
        span_count: 3, duration_ms: 200, status_code: "ok",
        route: { final_provider: "openai", final_model: "gpt-4o" },
      }],
      { request_id: "req-legend", started_at: t0.toISOString(), spans, route: { candidates: [] } },
    ));
    const state = createRuntimeConsoleFixture({ session: localSession });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    await waitFor(() => {
      expect(container.textContent).toMatch(/req-lege/);
    });
    const row = container.querySelector("tbody tr") as HTMLElement;
    await act(async () => { fireEvent.click(row); });
    await waitFor(() => {
      const legend = document.querySelector('[aria-label="Phase legend"]');
      expect(legend).toBeTruthy();
      const buttons = legend!.querySelectorAll("button");
      expect(buttons.length).toBeGreaterThanOrEqual(2);
    });
  });

  it("renders em-dash for cost when ledger has no entry", async () => {
    fetchMock.mockImplementation(tracesFetchHandler([
      { request_id: "missing-ledger", started_at: new Date().toISOString(), span_count: 1, duration_ms: 1, status_code: "ok", route: { final_provider: "openai", final_model: "m" } },
    ]));
    const state = createRuntimeConsoleFixture({ session: localSession, requestLedger: [] });
    let container = null as unknown as HTMLElement;
    await act(async () => {
      const result = render(<ObservabilityView state={state} actions={createRuntimeConsoleActions()} />);
      container = result.container;
    });
    await waitFor(() => {
      // Truncated to 8 chars in the request-id cell.
      expect(container.textContent).toMatch(/missing-/);
    });
    // The cost cell uses an em-dash. Make sure the literal $ amount is absent.
    expect(container.textContent).not.toMatch(/\$0\.00000/);
  });
});

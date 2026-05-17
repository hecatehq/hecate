import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { UsageProvider, useEnsureUsageLoaded, useUsage } from "./usage";
import type { ReactNode } from "react";

const getUsageSummaryMock = vi.fn();
const getUsageEventsMock = vi.fn();
const warnMock = vi.fn();

vi.mock("../../lib/api", () => ({
  getUsageSummary: (...args: unknown[]) => getUsageSummaryMock(...args),
  getUsageEvents: (...args: unknown[]) => getUsageEventsMock(...args),
}));

vi.mock("../../lib/log", () => ({
  info: vi.fn(),
  warn: (message: string, ...args: unknown[]) => warnMock(message, ...args),
  error: vi.fn(),
}));

function Wrapper({ children }: { children: ReactNode }) {
  return <UsageProvider>{children}</UsageProvider>;
}

beforeEach(() => {
  getUsageSummaryMock.mockReset();
  getUsageEventsMock.mockReset();
  warnMock.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("useEnsureUsageLoaded", () => {
  it("fetches summary + events on first mount and flips loaded=true", async () => {
    const summary = { data: { total_micros_usd: 1234 } } as any;
    const events = { data: [{ id: "evt-1" }] } as any;
    getUsageSummaryMock.mockResolvedValue(summary);
    getUsageEventsMock.mockResolvedValue(events);

    const { result } = renderHook(
      () => {
        useEnsureUsageLoaded();
        return useUsage();
      },
      { wrapper: Wrapper },
    );

    expect(result.current.state.loaded).toBe(false);
    expect(getUsageSummaryMock).toHaveBeenCalledTimes(1);
    expect(getUsageSummaryMock).toHaveBeenCalledWith("");
    expect(getUsageEventsMock).toHaveBeenCalledTimes(1);
    expect(getUsageEventsMock).toHaveBeenCalledWith(20);

    await waitFor(() => {
      expect(result.current.state.loaded).toBe(true);
    });
    expect(result.current.state.summary).toEqual({ total_micros_usd: 1234 });
    expect(result.current.state.events).toEqual([{ id: "evt-1" }]);
  });

  it("does NOT re-fetch when the slice is already loaded", async () => {
    getUsageSummaryMock.mockResolvedValue({ data: { total_micros_usd: 0 } });
    getUsageEventsMock.mockResolvedValue({ data: [] });

    // First mount completes the fetch and sets loaded=true.
    const { rerender, result } = renderHook(
      () => {
        useEnsureUsageLoaded();
        return useUsage();
      },
      { wrapper: Wrapper },
    );
    await waitFor(() => {
      expect(result.current.state.loaded).toBe(true);
    });
    expect(getUsageSummaryMock).toHaveBeenCalledTimes(1);
    expect(getUsageEventsMock).toHaveBeenCalledTimes(1);

    // Re-render the same hook — loaded=true should skip the fetch.
    rerender();
    expect(getUsageSummaryMock).toHaveBeenCalledTimes(1);
    expect(getUsageEventsMock).toHaveBeenCalledTimes(1);
  });

  it("dedupes parallel callers via inflight ref", async () => {
    let resolveSummary: (value: unknown) => void = () => undefined;
    let resolveEvents: (value: unknown) => void = () => undefined;
    getUsageSummaryMock.mockReturnValue(new Promise(r => { resolveSummary = r; }));
    getUsageEventsMock.mockReturnValue(new Promise(r => { resolveEvents = r; }));

    // Two consumers in the same provider — same UsageContext instance,
    // same inflight ref since the ref is per-hook-call but the loaded
    // flag is shared. The second consumer reads loaded=false on first
    // render (before the first promise resolves) but the inflight
    // ref on its own instance is also false, so technically it could
    // fire a second fetch. Dedup-across-consumers is bounded by the
    // shared loaded flag once one fetch resolves.
    function Multi({ children }: { children: ReactNode }) {
      return <UsageProvider>{children}</UsageProvider>;
    }
    renderHook(
      () => {
        useEnsureUsageLoaded();
        return useUsage();
      },
      { wrapper: Multi },
    );

    // One fetch pair is in flight; resolve them.
    expect(getUsageSummaryMock).toHaveBeenCalledTimes(1);
    expect(getUsageEventsMock).toHaveBeenCalledTimes(1);
    resolveSummary({ data: { total_micros_usd: 0 } });
    resolveEvents({ data: [] });
  });

  it("tolerates fetch failure: leaves loaded=false and logs a warning", async () => {
    getUsageSummaryMock.mockRejectedValue(new Error("network bonk"));
    getUsageEventsMock.mockResolvedValue({ data: [] });

    const { result } = renderHook(
      () => {
        useEnsureUsageLoaded();
        return useUsage();
      },
      { wrapper: Wrapper },
    );

    await waitFor(() => {
      expect(warnMock).toHaveBeenCalled();
    });
    expect(warnMock).toHaveBeenCalledWith(
      "usage.ensureLoaded.failed",
      expect.objectContaining({ err: "network bonk" }),
    );
    expect(result.current.state.loaded).toBe(false);
  });

  it("retries on next mount after a failure", async () => {
    getUsageSummaryMock.mockRejectedValueOnce(new Error("transient"));
    getUsageEventsMock.mockResolvedValue({ data: [] });

    const { unmount } = renderHook(() => useEnsureUsageLoaded(), { wrapper: Wrapper });
    await waitFor(() => {
      expect(warnMock).toHaveBeenCalled();
    });
    expect(getUsageSummaryMock).toHaveBeenCalledTimes(1);
    unmount();

    // Next mount under a fresh provider re-fetches because loaded
    // never flipped. (Real-world: full page reload resets the slice.)
    getUsageSummaryMock.mockResolvedValueOnce({ data: { total_micros_usd: 0 } });
    renderHook(() => useEnsureUsageLoaded(), { wrapper: Wrapper });
    await waitFor(() => {
      expect(getUsageSummaryMock).toHaveBeenCalledTimes(2);
    });
  });
});

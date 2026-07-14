import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  useProjectPassiveRefresh,
  type ProjectPassiveRefreshRequest,
} from "./useProjectPassiveRefresh";

function RefreshHarness({
  refresh,
  catalogIntervalMs,
  operationalIntervalMs,
}: {
  refresh: (request: ProjectPassiveRefreshRequest) => Promise<void> | void;
  catalogIntervalMs?: number;
  operationalIntervalMs?: number;
}) {
  useProjectPassiveRefresh(refresh, { catalogIntervalMs, operationalIntervalMs });
  return <button type="button">Keep focus</button>;
}

function returnWindowToForeground() {
  window.dispatchEvent(new Event("blur"));
  window.dispatchEvent(new Event("focus"));
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("useProjectPassiveRefresh", () => {
  it("refreshes after a real return and preserves operator focus", async () => {
    const refresh = vi.fn(async () => undefined);
    render(<RefreshHarness refresh={refresh} />);
    const focusTarget = screen.getByRole("button", { name: "Keep focus" });
    focusTarget.focus();

    void act(() => window.dispatchEvent(new Event("focus")));
    expect(refresh).not.toHaveBeenCalled();

    act(returnWindowToForeground);

    await waitFor(() =>
      expect(refresh).toHaveBeenCalledWith({ includeCatalog: true, reason: "foreground" }),
    );
    expect(focusTarget).toHaveFocus();
  });

  it("polls operational state while visible and refreshes the catalog more slowly", async () => {
    vi.useFakeTimers();
    const refresh = vi.fn(async () => undefined);
    render(<RefreshHarness refresh={refresh} catalogIntervalMs={30} operationalIntervalMs={10} />);

    await act(async () => vi.advanceTimersByTimeAsync(10));
    expect(refresh).toHaveBeenLastCalledWith({ includeCatalog: false, reason: "interval" });

    await act(async () => vi.advanceTimersByTimeAsync(20));
    expect(refresh).toHaveBeenLastCalledWith({ includeCatalog: true, reason: "interval" });
  });

  it("pauses polling while hidden and coalesces visibility with focus", async () => {
    vi.useFakeTimers();
    let visibilityState: DocumentVisibilityState = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibilityState);
    const refresh = vi.fn(async () => undefined);
    render(<RefreshHarness refresh={refresh} operationalIntervalMs={10} />);

    act(() => {
      visibilityState = "hidden";
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await act(async () => vi.advanceTimersByTimeAsync(30));
    expect(refresh).not.toHaveBeenCalled();

    act(() => {
      visibilityState = "visible";
      document.dispatchEvent(new Event("visibilitychange"));
      window.dispatchEvent(new Event("focus"));
    });

    await act(async () => Promise.resolve());
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(refresh).toHaveBeenLastCalledWith({ includeCatalog: true, reason: "foreground" });
  });

  it("queues one merged read when the operator returns during an active refresh", async () => {
    let releaseFirstRefresh!: () => void;
    const firstRefresh = new Promise<void>((resolve) => {
      releaseFirstRefresh = resolve;
    });
    const refresh = vi
      .fn<(request: ProjectPassiveRefreshRequest) => Promise<void>>()
      .mockImplementationOnce(async () => firstRefresh)
      .mockResolvedValue(undefined);
    render(<RefreshHarness refresh={refresh} />);

    act(returnWindowToForeground);
    await waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));

    act(returnWindowToForeground);
    expect(refresh).toHaveBeenCalledTimes(1);

    await act(async () => {
      releaseFirstRefresh();
      await firstRefresh;
    });
    await waitFor(() => expect(refresh).toHaveBeenCalledTimes(2));
    expect(refresh).toHaveBeenLastCalledWith({ includeCatalog: true, reason: "foreground" });
  });
});

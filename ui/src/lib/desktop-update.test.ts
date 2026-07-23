import { StrictMode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  DESKTOP_UPDATE_INSTALL_STALL_MS,
  DESKTOP_UPDATE_POLL_INTERVAL_MS,
  useDesktopUpdate,
} from "./desktop-update";

const checkMock = vi.fn();
vi.mock("@tauri-apps/plugin-updater", () => ({
  check: () => checkMock(),
}));

const logWarnMock = vi.fn();
vi.mock("./log", () => ({
  info: vi.fn(),
  warn: (message: string, ...args: unknown[]) => logWarnMock(message, ...args),
  error: vi.fn(),
}));

const invokeMock = vi.fn().mockResolvedValue(undefined);
vi.mock("@tauri-apps/api/core", () => ({
  invoke: (command: string, args?: unknown) => invokeMock(command, args),
}));

const relaunchMock = vi.fn().mockResolvedValue(undefined);
vi.mock("@tauri-apps/plugin-process", () => ({
  relaunch: () => relaunchMock(),
}));

let nativeMenuListener: (() => void) | null = null;
const originalUserAgent = navigator.userAgent;
const listenMock = vi.fn(async (_eventName: string, listener: () => void) => {
  nativeMenuListener = listener;
  return () => {
    nativeMenuListener = null;
  };
});
vi.mock("@tauri-apps/api/event", () => ({
  listen: (eventName: string, listener: () => void) => listenMock(eventName, listener),
}));

function updateFixture(
  overrides: Partial<{
    currentVersion: string;
    version: string;
    date: string;
    body: string;
    close: () => Promise<void> | void;
    downloadAndInstall: (onEvent?: (event: unknown) => void) => Promise<void>;
  }> = {},
) {
  return {
    currentVersion: "0.3.0-alpha.1",
    version: "0.3.0-alpha.2",
    downloadAndInstall: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

function enterTauriRuntime() {
  (globalThis as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = {};
}

function exitTauriRuntime() {
  delete (globalThis as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
}

function enterMobileTauriRuntime() {
  enterTauriRuntime();
  Object.defineProperty(navigator, "userAgent", {
    configurable: true,
    value: "HecateMobile",
  });
}

beforeEach(() => {
  checkMock.mockReset();
  logWarnMock.mockReset();
  invokeMock.mockReset();
  invokeMock.mockResolvedValue(undefined);
  relaunchMock.mockReset();
  relaunchMock.mockResolvedValue(undefined);
  listenMock.mockClear();
  nativeMenuListener = null;
  sessionStorage.clear();
  exitTauriRuntime();
  Object.defineProperty(navigator, "userAgent", { configurable: true, value: originalUserAgent });
});

afterEach(() => {
  exitTauriRuntime();
  Object.defineProperty(navigator, "userAgent", { configurable: true, value: originalUserAgent });
  vi.useRealTimers();
});

describe("useDesktopUpdate", () => {
  it("stays inert outside the Tauri runtime", async () => {
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update).toBeNull());
    expect(checkMock).not.toHaveBeenCalled();
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it("stays inert when a mobile-marked Tauri bridge is injected", async () => {
    enterMobileTauriRuntime();

    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update).toBeNull());
    expect(checkMock).not.toHaveBeenCalled();
    expect(invokeMock).not.toHaveBeenCalled();
    expect(listenMock).not.toHaveBeenCalled();
  });

  it("maps current version, published date, and release notes from Tauri", async () => {
    enterTauriRuntime();
    checkMock.mockResolvedValue(
      updateFixture({
        date: "2026-07-19T08:30:00Z",
        body: "- Safer desktop updates\n- Local system typography",
      }),
    );

    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update).not.toBeNull());
    expect(result.current.update).toEqual({
      currentVersion: "0.3.0-alpha.1",
      version: "0.3.0-alpha.2",
      publishedAt: "2026-07-19T08:30:00Z",
      notes: "- Safer desktop updates\n- Local system typography",
    });
    expect(result.current.manualCheck).toBeNull();
    expect(result.current.lastCheckedAt).toEqual(expect.any(Number));
  });

  it("keeps automatic no-update checks quiet", async () => {
    enterTauriRuntime();
    checkMock.mockResolvedValue(null);

    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(checkMock).toHaveBeenCalledTimes(1));
    expect(result.current.update).toBeNull();
    expect(result.current.manualCheck).toBeNull();
    expect(result.current.lastCheckedAt).toEqual(expect.any(Number));
  });

  it("logs automatic check failures without surfacing operator state", async () => {
    enterTauriRuntime();
    checkMock.mockRejectedValue(new Error("network down"));

    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(logWarnMock).toHaveBeenCalled());
    expect(result.current.update).toBeNull();
    expect(result.current.manualCheck).toBeNull();
    expect(logWarnMock.mock.calls[0]?.[0]).toContain("desktop updater check failed");
  });

  it("dismisses an update for the session and releases its Tauri resource", async () => {
    enterTauriRuntime();
    const close = vi.fn().mockResolvedValue(undefined);
    checkMock.mockResolvedValue(updateFixture({ close }));
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update).not.toBeNull());
    act(() => result.current.dismiss());

    expect(result.current.update).toBeNull();
    expect(result.current.manualCheck).toBeNull();
    expect(sessionStorage.getItem("hecate.update.dismissed")).toBe("1");
    await waitFor(() => expect(close).toHaveBeenCalledTimes(1));
  });

  it("skips automatic checks after a session dismissal", async () => {
    enterTauriRuntime();
    sessionStorage.setItem("hecate.update.dismissed", "1");

    renderHook(() => useDesktopUpdate());

    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(checkMock).not.toHaveBeenCalled();
  });

  it("downloads an update then relaunches the desktop app", async () => {
    enterTauriRuntime();
    let resolveDownload: (() => void) | null = null;
    const downloadAndInstall = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveDownload = resolve;
        }),
    );
    checkMock.mockResolvedValue(updateFixture({ downloadAndInstall }));
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update).not.toBeNull());
    act(() => {
      void result.current.installAndRestart();
    });
    await waitFor(() => expect(result.current.installing).toBe(true));
    expect(result.current.installPhase).toBe("downloading");
    expect(downloadAndInstall).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveDownload?.();
      await Promise.resolve();
    });
    await waitFor(() => expect(relaunchMock).toHaveBeenCalledTimes(1));
  });

  it("synchronously rejects a second install click before React rerenders", async () => {
    enterTauriRuntime();
    const downloadAndInstall = vi.fn(() => new Promise<void>(() => undefined));
    checkMock.mockResolvedValue(updateFixture({ downloadAndInstall }));
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update).not.toBeNull());
    act(() => {
      void result.current.installAndRestart();
      void result.current.installAndRestart();
    });

    expect(downloadAndInstall).toHaveBeenCalledTimes(1);
  });

  it("checks only once under React StrictMode", async () => {
    enterTauriRuntime();
    checkMock.mockResolvedValue(null);

    renderHook(() => useDesktopUpdate(), { wrapper: StrictMode });

    await waitFor(() => expect(checkMock).toHaveBeenCalled());
    expect(checkMock).toHaveBeenCalledTimes(1);
  });

  it("derives determinate progress from updater events", async () => {
    enterTauriRuntime();
    let onEvent: ((event: unknown) => void) | null = null;
    const downloadAndInstall = vi.fn((callback?: (event: unknown) => void) => {
      onEvent = callback ?? null;
      return new Promise<void>(() => undefined);
    });
    checkMock.mockResolvedValue(updateFixture({ downloadAndInstall }));
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update).not.toBeNull());
    act(() => {
      void result.current.installAndRestart();
    });
    await waitFor(() => expect(onEvent).not.toBeNull());

    act(() => {
      onEvent?.({ event: "Started", data: { contentLength: 1000 } });
      onEvent?.({ event: "Progress", data: { chunkLength: 250 } });
    });
    await waitFor(() => expect(result.current.progress).toBe(0.25));
    act(() => onEvent?.({ event: "Finished" }));
    await waitFor(() => expect(result.current.progress).toBe(1));
    expect(result.current.installPhase).toBe("finishing");
  });

  it("keeps the macOS install watchdog", async () => {
    vi.useFakeTimers();
    enterTauriRuntime();
    let onEvent: ((event: unknown) => void) | null = null;
    const downloadAndInstall = vi.fn((callback?: (event: unknown) => void) => {
      onEvent = callback ?? null;
      return new Promise<void>(() => undefined);
    });
    checkMock.mockResolvedValue(updateFixture({ downloadAndInstall }));
    const { result } = renderHook(() => useDesktopUpdate());

    await vi.waitFor(() => expect(result.current.update).not.toBeNull());
    act(() => {
      void result.current.installAndRestart();
    });
    await vi.waitFor(() => expect(onEvent).not.toBeNull());
    act(() => onEvent?.({ event: "Finished" }));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(DESKTOP_UPDATE_INSTALL_STALL_MS);
    });
    await vi.waitFor(() => expect(relaunchMock).toHaveBeenCalledTimes(1));
    expect(result.current.installPhase).toBe("restarting");
  });

  it("retains a failed download for a safe install retry", async () => {
    enterTauriRuntime();
    const downloadAndInstall = vi.fn().mockRejectedValue(new Error("disk full"));
    checkMock.mockResolvedValue(updateFixture({ downloadAndInstall }));
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update).not.toBeNull());
    await act(async () => {
      await result.current.installAndRestart();
    });

    expect(result.current.update).not.toBeNull();
    expect(result.current.installing).toBe(false);
    expect(result.current.installFailure).toBe("install");
  });

  it("distinguishes a restart failure and retries only the restart", async () => {
    enterTauriRuntime();
    relaunchMock
      .mockRejectedValueOnce(new Error("restart denied"))
      .mockResolvedValueOnce(undefined);
    const downloadAndInstall = vi.fn().mockResolvedValue(undefined);
    const close = vi.fn().mockResolvedValue(undefined);
    checkMock.mockResolvedValue(updateFixture({ close, downloadAndInstall }));
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update).not.toBeNull());
    await act(async () => {
      await result.current.installAndRestart();
    });

    expect(downloadAndInstall).toHaveBeenCalledTimes(1);
    expect(result.current.installFailure).toBe("restart");
    expect(result.current.restartReady).toBe(true);
    await waitFor(() => expect(close).toHaveBeenCalledTimes(1));
    await act(async () => {
      await result.current.retryRestart();
    });
    expect(downloadAndInstall).toHaveBeenCalledTimes(1);
    expect(relaunchMock).toHaveBeenCalledTimes(2);
  });

  it("preserves restart-only recovery when the native menu requests another check", async () => {
    enterTauriRuntime();
    relaunchMock.mockRejectedValue(new Error("restart denied"));
    const downloadAndInstall = vi.fn().mockResolvedValue(undefined);
    checkMock.mockResolvedValue(updateFixture({ downloadAndInstall }));
    let pendingCheckReads = 0;
    invokeMock.mockImplementation((command: string) => {
      if (command === "take_pending_desktop_update_check") {
        pendingCheckReads += 1;
        return Promise.resolve(pendingCheckReads === 2);
      }
      return Promise.resolve(undefined);
    });
    const onManualCheck = vi.fn();
    const { result } = renderHook(() => useDesktopUpdate({ onManualCheck }));

    await waitFor(() => expect(result.current.update).not.toBeNull());
    await waitFor(() => expect(nativeMenuListener).not.toBeNull());
    await act(async () => {
      await result.current.installAndRestart();
    });
    expect(result.current.installFailure).toBe("restart");
    expect(result.current.restartReady).toBe(true);

    act(() => nativeMenuListener?.());

    await waitFor(() => expect(onManualCheck).toHaveBeenCalledTimes(1));
    expect(checkMock).toHaveBeenCalledTimes(1);
    expect(result.current.installFailure).toBe("restart");
    expect(result.current.restartReady).toBe(true);
    expect(result.current.manualCheck).toBeNull();
  });

  it("restores the install retry when a stalled update later fails verification", async () => {
    vi.useFakeTimers();
    enterTauriRuntime();
    relaunchMock.mockRejectedValue(new Error("restart denied"));
    let rejectDownload: ((error: Error) => void) | null = null;
    let onEvent: ((event: unknown) => void) | null = null;
    const downloadAndInstall = vi.fn((callback?: (event: unknown) => void) => {
      onEvent = callback ?? null;
      return new Promise<void>((_resolve, reject) => {
        rejectDownload = reject;
      });
    });
    checkMock.mockResolvedValue(updateFixture({ downloadAndInstall }));
    const { result } = renderHook(() => useDesktopUpdate());

    await vi.waitFor(() => expect(result.current.update).not.toBeNull());
    act(() => {
      void result.current.installAndRestart();
    });
    await vi.waitFor(() => expect(onEvent).not.toBeNull());
    act(() => onEvent?.({ event: "Finished" }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(DESKTOP_UPDATE_INSTALL_STALL_MS);
    });
    await vi.waitFor(() => expect(result.current.installFailure).toBe("restart"));

    await act(async () => {
      rejectDownload?.(new Error("late install failure"));
      await Promise.resolve();
    });
    expect(relaunchMock).toHaveBeenCalledTimes(1);
    await vi.waitFor(() => expect(result.current.installFailure).toBe("install"));
    expect(result.current.restartReady).toBe(false);
  });

  it("keeps a late install failure when the watchdog restart fails afterward", async () => {
    vi.useFakeTimers();
    enterTauriRuntime();
    let rejectRestart: ((error: Error) => void) | null = null;
    relaunchMock.mockImplementation(
      () =>
        new Promise<void>((_resolve, reject) => {
          rejectRestart = reject;
        }),
    );
    let rejectDownload: ((error: Error) => void) | null = null;
    let onEvent: ((event: unknown) => void) | null = null;
    const downloadAndInstall = vi.fn((callback?: (event: unknown) => void) => {
      onEvent = callback ?? null;
      return new Promise<void>((_resolve, reject) => {
        rejectDownload = reject;
      });
    });
    checkMock.mockResolvedValue(updateFixture({ downloadAndInstall }));
    const { result } = renderHook(() => useDesktopUpdate());

    await vi.waitFor(() => expect(result.current.update).not.toBeNull());
    act(() => {
      void result.current.installAndRestart();
    });
    await vi.waitFor(() => expect(onEvent).not.toBeNull());
    act(() => onEvent?.({ event: "Finished" }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(DESKTOP_UPDATE_INSTALL_STALL_MS);
    });
    await vi.waitFor(() => expect(relaunchMock).toHaveBeenCalledTimes(1));

    await act(async () => {
      rejectDownload?.(new Error("late install failure"));
      await Promise.resolve();
    });
    await vi.waitFor(() => expect(result.current.installFailure).toBe("install"));
    await act(async () => {
      rejectRestart?.(new Error("restart denied"));
      await Promise.resolve();
    });
    expect(result.current.installFailure).toBe("install");
  });

  it("runs the steady-state interval", async () => {
    vi.useFakeTimers();
    enterTauriRuntime();
    checkMock.mockResolvedValue(null);
    renderHook(() => useDesktopUpdate());

    await vi.waitFor(() => expect(checkMock).toHaveBeenCalledTimes(1));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(DESKTOP_UPDATE_POLL_INTERVAL_MS);
    });
    expect(checkMock).toHaveBeenCalledTimes(2);
  });

  it("opens durable manual state and bypasses session dismissal", async () => {
    enterTauriRuntime();
    sessionStorage.setItem("hecate.update.dismissed", "1");
    checkMock.mockResolvedValue(null);
    const onManualCheck = vi.fn();
    const { result } = renderHook(() => useDesktopUpdate({ onManualCheck }));

    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(checkMock).not.toHaveBeenCalled();
    await act(async () => {
      await result.current.checkNow();
    });

    expect(onManualCheck).toHaveBeenCalledTimes(1);
    expect(result.current.manualCheck?.phase).toBe("up-to-date");
    expect(sessionStorage.getItem("hecate.update.dismissed")).toBeNull();
    act(() => result.current.clearManualCheck());
    expect(result.current.manualCheck).toBeNull();
  });

  it("keeps manual feedback when it lands on an in-flight automatic check", async () => {
    enterTauriRuntime();
    let resolveCheck: (value: null) => void = () => undefined;
    const pending = new Promise<null>((resolve) => {
      resolveCheck = resolve;
    });
    checkMock.mockReturnValue(pending);
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(checkMock).toHaveBeenCalledTimes(1));
    act(() => {
      void result.current.checkNow();
    });
    expect(checkMock).toHaveBeenCalledTimes(1);
    await act(async () => {
      resolveCheck(null);
      await pending;
    });

    await waitFor(() => expect(result.current.manualCheck?.phase).toBe("up-to-date"));
  });

  it("does not revive manual state after the details dialog clears a pending check", async () => {
    enterTauriRuntime();
    let resolveCheck: (value: null) => void = () => undefined;
    const pending = new Promise<null>((resolve) => {
      resolveCheck = resolve;
    });
    checkMock.mockReturnValue(pending);
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(checkMock).toHaveBeenCalledTimes(1));
    act(() => {
      void result.current.checkNow();
      result.current.clearManualCheck();
    });
    await act(async () => {
      resolveCheck(null);
      await pending;
    });
    expect(result.current.manualCheck).toBeNull();
  });

  it("opens manual state from the native menu event", async () => {
    enterTauriRuntime();
    let resolveCheck: (value: null) => void = () => undefined;
    const pending = new Promise<null>((resolve) => {
      resolveCheck = resolve;
    });
    checkMock.mockReturnValue(pending);
    let pendingCheckReads = 0;
    invokeMock.mockImplementation((command: string) => {
      if (command === "take_pending_desktop_update_check") {
        pendingCheckReads += 1;
        return Promise.resolve(pendingCheckReads === 2);
      }
      return Promise.resolve(undefined);
    });
    const onManualCheck = vi.fn();
    const { result } = renderHook(() => useDesktopUpdate({ onManualCheck }));

    await waitFor(() =>
      expect(listenMock).toHaveBeenCalledWith("hecate:check-for-updates", expect.any(Function)),
    );
    await waitFor(() =>
      expect(invokeMock).toHaveBeenCalledWith("take_pending_desktop_update_check", undefined),
    );
    act(() => nativeMenuListener?.());

    await waitFor(() => expect(onManualCheck).toHaveBeenCalledTimes(1));
    expect(result.current.manualCheck?.phase).toBe("checking");
    await act(async () => {
      resolveCheck(null);
      await pending;
    });
    await waitFor(() => expect(result.current.manualCheck?.phase).toBe("up-to-date"));
  });

  it("consumes a native update-check request queued during splash startup", async () => {
    enterTauriRuntime();
    let resolveCheck: (value: null) => void = () => undefined;
    const pending = new Promise<null>((resolve) => {
      resolveCheck = resolve;
    });
    checkMock.mockReturnValue(pending);
    invokeMock.mockImplementation((command: string) => {
      if (command === "take_pending_desktop_update_check") return Promise.resolve(true);
      return Promise.resolve(undefined);
    });
    const onManualCheck = vi.fn();
    const { result } = renderHook(() => useDesktopUpdate({ onManualCheck }));

    await waitFor(() => expect(onManualCheck).toHaveBeenCalledTimes(1));
    expect(result.current.manualCheck?.phase).toBe("checking");
    await act(async () => {
      resolveCheck(null);
      await pending;
    });
    await waitFor(() => expect(result.current.manualCheck?.phase).toBe("up-to-date"));
  });

  it("releases stale update resources after a successful no-update refresh", async () => {
    enterTauriRuntime();
    const close = vi.fn().mockResolvedValue(undefined);
    checkMock.mockResolvedValueOnce(updateFixture({ close })).mockResolvedValueOnce(null);
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update).not.toBeNull());
    await act(async () => {
      await result.current.checkNow();
    });

    expect(result.current.update).toBeNull();
    await waitFor(() => expect(close).toHaveBeenCalledTimes(1));
  });

  it("does not replace the installing resource when a manual refresh settles late", async () => {
    enterTauriRuntime();
    let resolveRefresh: (value: ReturnType<typeof updateFixture>) => void = () => undefined;
    const refresh = new Promise<ReturnType<typeof updateFixture>>((resolve) => {
      resolveRefresh = resolve;
    });
    const initialDownload = vi.fn(() => new Promise<void>(() => undefined));
    const initial = updateFixture({
      version: "0.3.0-alpha.2",
      downloadAndInstall: initialDownload,
    });
    const replacementClose = vi.fn().mockResolvedValue(undefined);
    const replacement = updateFixture({ version: "0.3.0-alpha.3", close: replacementClose });
    checkMock.mockResolvedValueOnce(initial).mockReturnValueOnce(refresh);
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update?.version).toBe("0.3.0-alpha.2"));
    act(() => {
      void result.current.checkNow();
    });
    await waitFor(() => expect(result.current.checking).toBe(true));
    act(() => {
      void result.current.installAndRestart();
    });
    expect(initialDownload).toHaveBeenCalledTimes(1);
    await act(async () => {
      resolveRefresh(replacement);
      await refresh;
    });

    expect(result.current.update?.version).toBe("0.3.0-alpha.2");
    await waitFor(() => expect(replacementClose).toHaveBeenCalledTimes(1));
  });

  it("does not resurrect a dismissed update when a refresh settles late", async () => {
    enterTauriRuntime();
    let resolveRefresh: (value: ReturnType<typeof updateFixture>) => void = () => undefined;
    const refresh = new Promise<ReturnType<typeof updateFixture>>((resolve) => {
      resolveRefresh = resolve;
    });
    const initial = updateFixture();
    const replacementClose = vi.fn().mockResolvedValue(undefined);
    const replacement = updateFixture({ version: "0.3.0-alpha.3", close: replacementClose });
    checkMock.mockResolvedValueOnce(initial).mockReturnValueOnce(refresh);
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update).not.toBeNull());
    act(() => {
      void result.current.checkNow();
      result.current.dismiss();
    });
    await act(async () => {
      resolveRefresh(replacement);
      await refresh;
    });

    expect(result.current.update).toBeNull();
    expect(result.current.dismissed).toBe(true);
    await waitFor(() => expect(replacementClose).toHaveBeenCalledTimes(1));
  });

  it("syncs the native update badge and clears it after dismissal", async () => {
    enterTauriRuntime();
    checkMock.mockResolvedValue(updateFixture());
    const { result } = renderHook(() => useDesktopUpdate());

    await waitFor(() => expect(result.current.update).not.toBeNull());
    await waitFor(() =>
      expect(invokeMock).toHaveBeenCalledWith("set_update_badge", { visible: true }),
    );
    invokeMock.mockClear();
    act(() => result.current.dismiss());
    await waitFor(() =>
      expect(invokeMock).toHaveBeenCalledWith("set_update_badge", { visible: false }),
    );
  });
});

import { StrictMode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DESKTOP_UPDATE_POLL_INTERVAL_MS, useDesktopUpdate } from "./desktop-update";

// The hook dynamically imports @tauri-apps/plugin-updater inside
// the Tauri runtime check. We mock the module so the tests don't
// pull the real plugin (which expects __TAURI_INTERNALS__ wired
// into the host) and so each test can drive check() to return
// whatever shape it needs.
const checkMock = vi.fn();
vi.mock("@tauri-apps/plugin-updater", () => ({
  check: () => checkMock(),
}));

// The log helper used by the hook routes through plugin-log inside
// Tauri. We spy on warn() to assert it's invoked on failures
// without spinning up the real plugin.
const logWarnMock = vi.fn();
vi.mock("./log", () => ({
  info: vi.fn(),
  warn: (message: string, ...args: unknown[]) => logWarnMock(message, ...args),
  error: vi.fn(),
}));

// The hook calls invoke("set_update_badge", ...) on every
// transition into / out of the "update available" state. We mock
// the core module so tests don't hit a real Tauri bridge that
// isn't there.
const invokeMock = vi.fn().mockResolvedValue(undefined);
vi.mock("@tauri-apps/api/core", () => ({
  invoke: (cmd: string, args?: unknown) => invokeMock(cmd, args),
}));

function enterTauriRuntime() {
  // Stamp the marker isTauriRuntime() looks for. cleanup in afterEach.
  (globalThis as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = {};
}

function exitTauriRuntime() {
  delete (globalThis as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
}

beforeEach(() => {
  checkMock.mockReset();
  logWarnMock.mockReset();
  invokeMock.mockReset();
  invokeMock.mockResolvedValue(undefined);
  sessionStorage.clear();
  exitTauriRuntime();
});

afterEach(() => {
  exitTauriRuntime();
  vi.useRealTimers();
});

describe("useDesktopUpdate", () => {
  it("does not call check() outside the Tauri runtime", async () => {
    const { result } = renderHook(() => useDesktopUpdate());
    // Yield once so any pending effect work would have fired.
    await waitFor(() => expect(result.current.update).toBeNull());
    expect(checkMock).not.toHaveBeenCalled();
  });

  it("surfaces the available version when check() returns an update", async () => {
    enterTauriRuntime();
    checkMock.mockResolvedValue({
      version: "0.1.0-alpha.24",
      downloadAndInstall: vi.fn(),
    });
    const { result } = renderHook(() => useDesktopUpdate());
    await waitFor(() => expect(result.current.update).not.toBeNull());
    expect(result.current.update).toEqual({ version: "0.1.0-alpha.24" });
    expect(result.current.installing).toBe(false);
    expect(result.current.lastCheckResult).toBe("update");
  });

  it("returns null when check() resolves with no update", async () => {
    enterTauriRuntime();
    checkMock.mockResolvedValue(null);
    const { result } = renderHook(() => useDesktopUpdate());
    await waitFor(() => expect(checkMock).toHaveBeenCalled());
    expect(result.current.update).toBeNull();
    // Automatic check + no update: no transient banner.
    expect(result.current.lastCheckResult).toBeNull();
  });

  it("logs check() failures via plugin-log warn() and stays inert", async () => {
    enterTauriRuntime();
    checkMock.mockRejectedValue(new Error("network down"));
    const { result } = renderHook(() => useDesktopUpdate());
    await waitFor(() => expect(checkMock).toHaveBeenCalled());
    await waitFor(() => expect(logWarnMock).toHaveBeenCalled());
    expect(result.current.update).toBeNull();
    expect(result.current.installing).toBe(false);
    expect(logWarnMock.mock.calls[0]?.[0]).toContain("desktop updater check failed");
  });

  it("dismiss() hides the update for the session and writes sessionStorage", async () => {
    enterTauriRuntime();
    checkMock.mockResolvedValue({
      version: "0.1.0-alpha.24",
      downloadAndInstall: vi.fn(),
    });
    const { result } = renderHook(() => useDesktopUpdate());
    await waitFor(() => expect(result.current.update).not.toBeNull());
    act(() => result.current.dismiss());
    expect(result.current.update).toBeNull();
    expect(sessionStorage.getItem("hecate.update.dismissed")).toBe("1");
  });

  it("skips the automatic check entirely when sessionStorage marks the update dismissed", async () => {
    enterTauriRuntime();
    sessionStorage.setItem("hecate.update.dismissed", "1");
    const { result } = renderHook(() => useDesktopUpdate());
    // Give the effect a tick — if the dismiss check is honored,
    // checkMock should never be invoked at all.
    await new Promise((r) => setTimeout(r, 10));
    expect(checkMock).not.toHaveBeenCalled();
    expect(result.current.update).toBeNull();
  });

  it("installAndRestart() toggles installing and calls downloadAndInstall", async () => {
    enterTauriRuntime();
    let resolveDownload: (() => void) | null = null;
    const downloadAndInstall = vi.fn(
      () => new Promise<void>((resolve) => { resolveDownload = resolve; }),
    );
    checkMock.mockResolvedValue({
      version: "0.1.0-alpha.24",
      downloadAndInstall,
    });
    const { result } = renderHook(() => useDesktopUpdate());
    await waitFor(() => expect(result.current.update).not.toBeNull());
    act(() => { void result.current.installAndRestart(); });
    await waitFor(() => expect(result.current.installing).toBe(true));
    expect(downloadAndInstall).toHaveBeenCalledTimes(1);
    // Resolve the download — installing flips back only on error;
    // on success the plugin relaunches and the renderer terminates.
    // We simulate the success path by resolving without exception.
    act(() => { resolveDownload?.(); });
  });

  it("only calls check() once under React StrictMode (no double-fire on dev remount)", async () => {
    enterTauriRuntime();
    checkMock.mockResolvedValue(null);
    renderHook(() => useDesktopUpdate(), { wrapper: StrictMode });
    await waitFor(() => expect(checkMock).toHaveBeenCalled());
    // StrictMode would invoke the effect twice without the
    // checkedRef guard. The guard is what keeps this at 1.
    expect(checkMock).toHaveBeenCalledTimes(1);
  });

  it("derives progress from plugin events", async () => {
    enterTauriRuntime();
    let onEventCb: ((e: unknown) => void) | null = null;
    const downloadAndInstall = vi.fn((cb?: (e: unknown) => void) => {
      onEventCb = cb ?? null;
      return new Promise<void>(() => { /* never resolves */ });
    });
    checkMock.mockResolvedValue({
      version: "0.1.0-alpha.24",
      downloadAndInstall,
    });
    const { result } = renderHook(() => useDesktopUpdate());
    await waitFor(() => expect(result.current.update).not.toBeNull());
    act(() => { void result.current.installAndRestart(); });
    await waitFor(() => expect(onEventCb).not.toBeNull());

    // Started fires the total length; Progress events accumulate.
    act(() => { onEventCb?.({ event: "Started", data: { contentLength: 1000 } }); });
    act(() => { onEventCb?.({ event: "Progress", data: { chunkLength: 250 } }); });
    await waitFor(() => expect(result.current.progress).toBe(0.25));
    act(() => { onEventCb?.({ event: "Progress", data: { chunkLength: 500 } }); });
    await waitFor(() => expect(result.current.progress).toBe(0.75));
  });

  it("re-runs check() on the steady-state interval", async () => {
    vi.useFakeTimers();
    enterTauriRuntime();
    checkMock.mockResolvedValue(null);
    renderHook(() => useDesktopUpdate());
    await vi.waitFor(() => expect(checkMock).toHaveBeenCalledTimes(1));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(DESKTOP_UPDATE_POLL_INTERVAL_MS);
    });
    expect(checkMock).toHaveBeenCalledTimes(2);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(DESKTOP_UPDATE_POLL_INTERVAL_MS);
    });
    expect(checkMock).toHaveBeenCalledTimes(3);
  });

  it("checkNow() bypasses sessionStorage dismiss and reruns the check", async () => {
    enterTauriRuntime();
    sessionStorage.setItem("hecate.update.dismissed", "1");
    checkMock.mockResolvedValue({
      version: "0.1.0-alpha.25",
      downloadAndInstall: vi.fn(),
    });
    const { result } = renderHook(() => useDesktopUpdate());
    // Initial automatic check is skipped by the dismiss flag.
    await new Promise((r) => setTimeout(r, 10));
    expect(checkMock).not.toHaveBeenCalled();

    await act(async () => {
      await result.current.checkNow();
    });
    expect(checkMock).toHaveBeenCalledTimes(1);
    expect(result.current.update).toEqual({ version: "0.1.0-alpha.25" });
    // checkNow also clears the dismiss marker so subsequent auto
    // checks fire again — the user explicitly asked to look again.
    expect(sessionStorage.getItem("hecate.update.dismissed")).toBeNull();
  });

  it("checkNow() with no update surfaces transient up-to-date feedback", async () => {
    vi.useFakeTimers();
    enterTauriRuntime();
    checkMock.mockResolvedValue(null);
    const { result } = renderHook(() => useDesktopUpdate());
    await vi.waitFor(() => expect(checkMock).toHaveBeenCalled());
    expect(result.current.lastCheckResult).toBeNull();

    await act(async () => {
      await result.current.checkNow();
    });
    expect(result.current.lastCheckResult).toBe("up-to-date");

    // Transient feedback clears after the timeout.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(result.current.lastCheckResult).toBeNull();
  });

  it("invokes set_update_badge(true) when an update is detected and clears on dismiss", async () => {
    enterTauriRuntime();
    checkMock.mockResolvedValue({
      version: "0.1.0-alpha.31",
      downloadAndInstall: vi.fn(),
    });
    const { result } = renderHook(() => useDesktopUpdate());
    await waitFor(() => expect(result.current.update).not.toBeNull());
    // Effect fires the badge invocation after the update lands.
    await waitFor(() =>
      expect(invokeMock).toHaveBeenCalledWith("set_update_badge", { visible: true }),
    );
    invokeMock.mockClear();
    act(() => result.current.dismiss());
    await waitFor(() =>
      expect(invokeMock).toHaveBeenCalledWith("set_update_badge", { visible: false }),
    );
  });

  it("does not invoke set_update_badge outside the Tauri runtime", async () => {
    checkMock.mockResolvedValue({
      version: "0.1.0-alpha.31",
      downloadAndInstall: vi.fn(),
    });
    renderHook(() => useDesktopUpdate());
    await new Promise((r) => setTimeout(r, 10));
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it("checkNow() with a failing check surfaces transient error feedback and logs", async () => {
    vi.useFakeTimers();
    enterTauriRuntime();
    checkMock.mockRejectedValue(new Error("timeout"));
    const { result } = renderHook(() => useDesktopUpdate());
    await vi.waitFor(() => expect(checkMock).toHaveBeenCalled());

    await act(async () => {
      await result.current.checkNow();
    });
    expect(result.current.lastCheckResult).toBe("error");
    expect(logWarnMock).toHaveBeenCalled();
  });
});

import { StrictMode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useDesktopUpdate } from "./desktop-update";

// The hook dynamically imports @tauri-apps/plugin-updater inside
// the Tauri runtime check. We mock the module so the tests don't
// pull the real plugin (which expects __TAURI_INTERNALS__ wired
// into the host) and so each test can drive check() to return
// whatever shape it needs.
const checkMock = vi.fn();
vi.mock("@tauri-apps/plugin-updater", () => ({
  check: () => checkMock(),
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
  sessionStorage.clear();
  exitTauriRuntime();
});

afterEach(() => {
  exitTauriRuntime();
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
  });

  it("returns null when check() resolves with no update", async () => {
    enterTauriRuntime();
    checkMock.mockResolvedValue(null);
    const { result } = renderHook(() => useDesktopUpdate());
    await waitFor(() => expect(checkMock).toHaveBeenCalled());
    expect(result.current.update).toBeNull();
  });

  it("swallows check() failures and stays inert", async () => {
    enterTauriRuntime();
    checkMock.mockRejectedValue(new Error("network down"));
    const { result } = renderHook(() => useDesktopUpdate());
    await waitFor(() => expect(checkMock).toHaveBeenCalled());
    expect(result.current.update).toBeNull();
    expect(result.current.installing).toBe(false);
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

  it("skips the check entirely when sessionStorage marks the update dismissed", async () => {
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
});

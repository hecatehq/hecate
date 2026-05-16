import { afterEach, describe, expect, it, vi } from "vitest";

import { installTauriDocumentMarkers, installTauriEditShortcutFallback } from "./App";

describe("installTauriDocumentMarkers", () => {
  const originalPlatform = navigator.platform;
  afterEach(() => {
    Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
    delete document.documentElement.dataset.tauri;
    delete document.documentElement.dataset.tauriOs;
    Object.defineProperty(navigator, "platform", { configurable: true, value: originalPlatform });
  });

  it("marks the document only inside Tauri", () => {
    const cleanup = installTauriDocumentMarkers();

    expect(document.documentElement.dataset.tauri).toBeUndefined();
    cleanup();
  });

  it("cleans up the desktop marker", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});

    const cleanup = installTauriDocumentMarkers();

    expect(document.documentElement.dataset.tauri).toBe("true");
    cleanup();
    expect(document.documentElement.dataset.tauri).toBeUndefined();
  });

  it("surfaces the macOS platform so App.css can reserve traffic-light room", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(navigator, "platform", { configurable: true, value: "MacIntel" });

    const cleanup = installTauriDocumentMarkers();

    expect(document.documentElement.dataset.tauriOs).toBe("macos");
    cleanup();
    expect(document.documentElement.dataset.tauriOs).toBeUndefined();
  });

  it("omits the OS marker on Linux / Windows where the native titlebar sits outside the webview", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(navigator, "platform", { configurable: true, value: "Linux x86_64" });

    const cleanup = installTauriDocumentMarkers();

    expect(document.documentElement.dataset.tauri).toBe("true");
    expect(document.documentElement.dataset.tauriOs).toBeUndefined();
    cleanup();
  });
});

describe("installTauriEditShortcutFallback", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
    Reflect.deleteProperty(document, "execCommand");
    document.body.innerHTML = "";
  });

  it("does not intercept browser shortcuts outside Tauri", () => {
    Object.defineProperty(document, "execCommand", { configurable: true, value: vi.fn(() => true) });
    const execCommand = vi.spyOn(document, "execCommand").mockReturnValue(true);
    const cleanup = installTauriEditShortcutFallback();
    const input = document.createElement("input");
    input.value = "hello";
    document.body.append(input);
    input.focus();

    input.dispatchEvent(new KeyboardEvent("keydown", { key: "c", ctrlKey: true, bubbles: true }));

    expect(execCommand).not.toHaveBeenCalled();
    cleanup();
  });

  it("forwards native copy shortcuts to focused editable fields inside Tauri", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(document, "execCommand", { configurable: true, value: vi.fn(() => true) });
    const execCommand = vi.spyOn(document, "execCommand").mockReturnValue(true);
    const cleanup = installTauriEditShortcutFallback();
    const input = document.createElement("input");
    input.value = "hello";
    document.body.append(input);
    input.focus();
    input.select();

    input.dispatchEvent(new KeyboardEvent("keydown", { key: "c", ctrlKey: true, bubbles: true }));

    expect(execCommand).toHaveBeenCalledWith("copy");
    cleanup();
  });

  it("does not intercept non-text input shortcuts inside Tauri", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(document, "execCommand", { configurable: true, value: vi.fn(() => true) });
    const execCommand = vi.spyOn(document, "execCommand").mockReturnValue(true);
    const cleanup = installTauriEditShortcutFallback();
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    document.body.append(checkbox);
    checkbox.focus();
    const event = new KeyboardEvent("keydown", { key: "c", ctrlKey: true, bubbles: true, cancelable: true });

    checkbox.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(false);
    expect(execCommand).not.toHaveBeenCalled();
    cleanup();
  });
});

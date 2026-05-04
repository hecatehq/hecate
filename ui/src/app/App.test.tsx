import { afterEach, describe, expect, it, vi } from "vitest";

import { installTauriEditShortcutFallback } from "./App";

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

import { afterEach, describe, expect, it } from "vitest";

import { isTauriOnMacOS, isTauriRuntime } from "./tauri";

const originalPlatform = navigator.platform;

afterEach(() => {
  Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
  Reflect.deleteProperty(window, "__TAURI__");
  Object.defineProperty(navigator, "platform", { configurable: true, value: originalPlatform });
});

describe("isTauriRuntime", () => {
  it("returns true when __TAURI_INTERNALS__ is present", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    expect(isTauriRuntime()).toBe(true);
  });

  it("returns true when __TAURI__ is present (withGlobalTauri)", () => {
    Reflect.set(window, "__TAURI__", {});
    expect(isTauriRuntime()).toBe(true);
  });

  it("returns false in a regular browser tab", () => {
    expect(isTauriRuntime()).toBe(false);
  });
});

describe("isTauriOnMacOS", () => {
  it("returns true inside Tauri on macOS", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(navigator, "platform", { configurable: true, value: "MacIntel" });
    expect(isTauriOnMacOS()).toBe(true);
  });

  it("returns false inside Tauri on Linux", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(navigator, "platform", { configurable: true, value: "Linux x86_64" });
    expect(isTauriOnMacOS()).toBe(false);
  });

  it("returns false inside Tauri on Windows", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(navigator, "platform", { configurable: true, value: "Win32" });
    expect(isTauriOnMacOS()).toBe(false);
  });

  it("returns false in a browser on macOS (no Tauri runtime)", () => {
    Object.defineProperty(navigator, "platform", { configurable: true, value: "MacIntel" });
    expect(isTauriOnMacOS()).toBe(false);
  });
});

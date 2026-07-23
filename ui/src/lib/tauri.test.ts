import { afterEach, describe, expect, it } from "vitest";

import {
  desktopHost,
  isMobileTauriRuntime,
  isTauriOnMacOS,
  isTauriRuntime,
  MOBILE_TAURI_USER_AGENT_MARKER,
} from "./tauri";

const originalPlatform = navigator.platform;
const originalUserAgent = navigator.userAgent;

afterEach(() => {
  Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
  Reflect.deleteProperty(window, "__TAURI__");
  Object.defineProperty(navigator, "platform", { configurable: true, value: originalPlatform });
  Object.defineProperty(navigator, "userAgent", { configurable: true, value: originalUserAgent });
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

  it("does not classify a mobile-marked injected bridge as desktop Tauri", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(navigator, "userAgent", {
      configurable: true,
      value: `WebView ${MOBILE_TAURI_USER_AGENT_MARKER}`,
    });

    expect(isMobileTauriRuntime()).toBe(true);
    expect(isTauriRuntime()).toBe(false);
    expect(desktopHost()).toBeNull();
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

describe("desktopHost", () => {
  it("returns the supported Tauri host without changing browser behavior", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});

    Object.defineProperty(navigator, "platform", { configurable: true, value: "MacIntel" });
    expect(desktopHost()).toBe("macos");
    Object.defineProperty(navigator, "platform", { configurable: true, value: "Win32" });
    expect(desktopHost()).toBe("windows");
    Object.defineProperty(navigator, "platform", { configurable: true, value: "Linux x86_64" });
    expect(desktopHost()).toBe("linux");
  });

  it("returns null outside Tauri", () => {
    Object.defineProperty(navigator, "platform", { configurable: true, value: "MacIntel" });
    expect(desktopHost()).toBeNull();
  });
});

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  canUseDesktopCloudConnection,
  getDesktopCloudConnectionStatus,
  signOutDesktopCloudConnection,
  startDesktopCloudConnection,
  stopDesktopCloudConnection,
} from "./cloud-connection";

const invokeMock = vi.fn();

vi.mock("@tauri-apps/api/core", () => ({
  invoke: (cmd: string, args?: unknown) => invokeMock(cmd, args),
}));

afterEach(() => {
  Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
  Reflect.deleteProperty(window, "__TAURI__");
  invokeMock.mockReset();
});

describe("desktop cloud connection bridge", () => {
  it("is unavailable outside the desktop runtime", async () => {
    expect(canUseDesktopCloudConnection()).toBe(false);

    await expect(getDesktopCloudConnectionStatus()).rejects.toThrow(
      "only available in the desktop app",
    );
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it("loads normalized status from Tauri", async () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    invokeMock.mockResolvedValueOnce({
      available: true,
      phase: "connected",
      running: true,
      authorizing: false,
      signed_in: true,
      gateway_ready: true,
      auto_start_enabled: true,
      account_email: "alice@example.com",
      cloud_url: "https://console.hecatehq.com",
      base_url: "http://127.0.0.1:54321",
      message: "Remote access is on.",
      last_error: null,
    });

    await expect(getDesktopCloudConnectionStatus()).resolves.toEqual({
      available: true,
      phase: "connected",
      running: true,
      authorizing: false,
      signed_in: true,
      gateway_ready: true,
      auto_start_enabled: true,
      account_email: "alice@example.com",
      cloud_url: "https://console.hecatehq.com",
      base_url: "http://127.0.0.1:54321",
      message: "Remote access is on.",
      last_error: null,
    });
    expect(invokeMock).toHaveBeenCalledWith("cloud_connection_status", undefined);
  });

  it("starts, stops, and signs out through fixed native commands", async () => {
    Reflect.set(window, "__TAURI__", {});
    invokeMock
      .mockResolvedValueOnce({
        available: true,
        running: true,
        gateway_ready: true,
        auto_start_enabled: true,
        message: "Connected",
      })
      .mockResolvedValueOnce({
        available: true,
        running: false,
        gateway_ready: true,
        auto_start_enabled: false,
        message: "Disconnected",
      })
      .mockResolvedValueOnce({
        available: true,
        running: false,
        gateway_ready: true,
        auto_start_enabled: false,
        message: "Signed out",
      });

    await startDesktopCloudConnection();
    await stopDesktopCloudConnection();
    await signOutDesktopCloudConnection();

    expect(invokeMock).toHaveBeenNthCalledWith(1, "cloud_connection_start", undefined);
    expect(invokeMock).toHaveBeenNthCalledWith(2, "cloud_connection_stop", undefined);
    expect(invokeMock).toHaveBeenNthCalledWith(3, "cloud_connection_sign_out", undefined);
  });
});

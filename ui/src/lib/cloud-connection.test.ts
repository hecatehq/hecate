import { afterEach, describe, expect, it, vi } from "vitest";

import {
  canUseDesktopCloudConnection,
  getDesktopCloudConnectionStatus,
  getDesktopCloudRuntimeConnections,
  openDesktopCloudRuntime,
  signInDesktopCloudAccount,
  signOutDesktopCloudConnection,
  startDesktopCloudConnection,
  startDesktopCloudRuntime,
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

function connectionStatus(overrides: Record<string, unknown> = {}) {
  return {
    available: true,
    restoring: false,
    phase: "disconnected",
    running: false,
    authorizing: false,
    signed_in: false,
    gateway_ready: true,
    auto_start_enabled: false,
    account_email: null,
    cloud_url: "https://console.hecatehq.com",
    base_url: "http://127.0.0.1:54321",
    message: "Remote access is off.",
    last_error: null,
    ...overrides,
  };
}

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
    invokeMock.mockResolvedValueOnce(
      connectionStatus({
        phase: "connected",
        running: true,
        signed_in: true,
        auto_start_enabled: true,
        account_email: "alice@example.com",
        message: "Remote access is on.",
      }),
    );

    await expect(getDesktopCloudConnectionStatus()).resolves.toEqual({
      available: true,
      restoring: false,
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

  it("does not expose removed browser verification-code fields", async () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    invokeMock.mockResolvedValueOnce(
      connectionStatus({
        phase: "authorizing",
        authorizing: true,
        message: "Approve sign-in in your browser.",
        verification_code: "ABCD-EFGH",
      }),
    );

    const status = await getDesktopCloudConnectionStatus();
    expect(status).toMatchObject({
      phase: "authorizing",
      authorizing: true,
    });
    expect(status).not.toHaveProperty("verification_code");
  });

  it("starts, stops, and signs out through fixed native commands", async () => {
    Reflect.set(window, "__TAURI__", {});
    invokeMock
      .mockResolvedValueOnce(
        connectionStatus({
          phase: "connected",
          signed_in: true,
          running: true,
          auto_start_enabled: true,
          message: "Connected",
        }),
      )
      .mockResolvedValueOnce(connectionStatus({ signed_in: true, message: "Disconnected" }))
      .mockResolvedValueOnce(connectionStatus({ message: "Signed out" }));

    await startDesktopCloudConnection();
    await stopDesktopCloudConnection();
    await signOutDesktopCloudConnection();

    expect(invokeMock).toHaveBeenNthCalledWith(1, "cloud_connection_start", undefined);
    expect(invokeMock).toHaveBeenNthCalledWith(2, "cloud_connection_stop", undefined);
    expect(invokeMock).toHaveBeenNthCalledWith(3, "cloud_connection_sign_out", undefined);
  });

  it("starts account-only sign-in without enabling this computer", async () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    invokeMock.mockResolvedValueOnce(
      connectionStatus({
        phase: "authorizing",
        authorizing: true,
        message: "Approve sign-in in your browser.",
      }),
    );

    await expect(signInDesktopCloudAccount()).resolves.toMatchObject({
      phase: "authorizing",
      auto_start_enabled: false,
    });
    expect(invokeMock).toHaveBeenCalledWith("cloud_account_sign_in", undefined);
  });

  it("rejects malformed native status instead of guessing defaults", async () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    invokeMock.mockResolvedValueOnce({ available: true, phase: "surprise" });

    await expect(getDesktopCloudConnectionStatus()).rejects.toThrow("invalid status");
  });
});

describe("desktop cloud runtime bridge", () => {
  it("normalizes the safe runtime connection projection", async () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    invokeMock.mockResolvedValueOnce([
      {
        id: "runtime_1",
        kind: "hosted_runtime",
        org_id: "org_1",
        project_id: "project_1",
        name: "Production",
        status: "online",
        reachable: true,
        can_start: false,
        remote_enabled: false,
        version: "0.5.0-alpha.5",
        capabilities: ["browser_proxy"],
        last_seen_at: "2026-07-31T12:00:00Z",
        browser_open_path: "/must-not-cross-ipc",
      },
      {
        id: "host_1",
        kind: "desktop_host",
        org_id: "org_1",
        name: "Studio Mac",
        status: "offline",
        reachable: false,
        can_start: false,
        remote_enabled: true,
        capabilities: [],
      },
    ]);

    await expect(getDesktopCloudRuntimeConnections()).resolves.toEqual([
      {
        id: "runtime_1",
        kind: "hosted_runtime",
        org_id: "org_1",
        project_id: "project_1",
        name: "Production",
        status: "online",
        reachable: true,
        can_start: false,
        remote_enabled: false,
        version: "0.5.0-alpha.5",
        capabilities: ["browser_proxy"],
        last_seen_at: "2026-07-31T12:00:00Z",
      },
      {
        id: "host_1",
        kind: "desktop_host",
        org_id: "org_1",
        project_id: null,
        name: "Studio Mac",
        status: "offline",
        reachable: false,
        can_start: false,
        remote_enabled: true,
        version: null,
        capabilities: [],
        last_seen_at: null,
      },
    ]);
    expect(invokeMock).toHaveBeenCalledWith("cloud_runtime_connections", undefined);
  });

  it("rejects an invalid connection row instead of rendering partial authority", async () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    invokeMock.mockResolvedValueOnce([
      {
        id: "runtime_1",
        kind: "unexpected",
        org_id: "org_1",
        name: "Production",
        status: "online",
        reachable: true,
        can_start: false,
        remote_enabled: false,
        capabilities: [],
      },
    ]);

    await expect(getDesktopCloudRuntimeConnections()).rejects.toThrow("invalid runtime connection");
  });

  it("starts and opens only the selected connection through native commands", async () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    invokeMock
      .mockResolvedValueOnce({
        connection_id: "runtime_1",
        name: "Production",
        status: "starting",
        reachable: false,
        message: "Hecate Cloud is starting this runtime.",
      })
      .mockResolvedValueOnce({
        connection_id: "runtime_1",
        name: "Production",
        message: "Opened Production in a new Hecate window.",
        open_url: "https://must-not-cross-ipc.example",
      });

    await expect(startDesktopCloudRuntime(" runtime_1 ")).resolves.toEqual({
      connection_id: "runtime_1",
      name: "Production",
      status: "starting",
      reachable: false,
      message: "Hecate Cloud is starting this runtime.",
    });
    await expect(openDesktopCloudRuntime("runtime_1")).resolves.toEqual({
      connection_id: "runtime_1",
      name: "Production",
      message: "Opened Production in a new Hecate window.",
    });
    expect(invokeMock).toHaveBeenNthCalledWith(1, "cloud_runtime_start", {
      connectionId: "runtime_1",
    });
    expect(invokeMock).toHaveBeenNthCalledWith(2, "cloud_runtime_open", {
      connectionId: "runtime_1",
    });
  });

  it("rejects empty connection identifiers before invoking native code", async () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});

    await expect(startDesktopCloudRuntime("  ")).rejects.toThrow(
      "Choose a valid Hecate connection",
    );
    await expect(openDesktopCloudRuntime("")).rejects.toThrow("Choose a valid Hecate connection");
    expect(invokeMock).not.toHaveBeenCalled();
  });
});

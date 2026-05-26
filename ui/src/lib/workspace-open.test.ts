import { afterEach, describe, expect, it, vi } from "vitest";

import {
  canOpenWorkspaceFromUI,
  openWorkspaceTarget,
  workspaceOpenTargets,
} from "./workspace-open";
import { openWorkspaceTargetViaAPI } from "./api";

const invokeMock = vi.fn().mockResolvedValue(undefined);

vi.mock("@tauri-apps/api/core", () => ({
  invoke: (cmd: string, args?: unknown) => invokeMock(cmd, args),
}));

vi.mock("./api", () => ({
  openWorkspaceTargetViaAPI: vi.fn(async () => undefined),
}));

const originalPlatform = navigator.platform;

afterEach(() => {
  Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
  Reflect.deleteProperty(window, "__TAURI__");
  Object.defineProperty(navigator, "platform", { configurable: true, value: originalPlatform });
  invokeMock.mockReset();
  invokeMock.mockResolvedValue(undefined);
  vi.mocked(openWorkspaceTargetViaAPI).mockReset();
  vi.mocked(openWorkspaceTargetViaAPI).mockResolvedValue(undefined);
});

describe("workspace open targets", () => {
  it("is available outside the desktop runtime through the local gateway", () => {
    expect(canOpenWorkspaceFromUI()).toBe(true);
  });

  it("includes macOS app targets on macOS hosts", () => {
    Object.defineProperty(navigator, "platform", { configurable: true, value: "MacIntel" });

    expect(workspaceOpenTargets().map((target) => target.id)).toEqual([
      "vscode",
      "vscode_insiders",
      "cursor",
      "zed",
      "finder",
      "terminal",
      "iterm2",
      "xcode",
    ]);
  });

  it("uses generic desktop targets off macOS", () => {
    Object.defineProperty(navigator, "platform", { configurable: true, value: "Linux x86_64" });

    expect(workspaceOpenTargets().map((target) => target.id)).toEqual([
      "vscode",
      "vscode_insiders",
      "cursor",
      "zed",
      "finder",
      "terminal",
    ]);
  });

  it("invokes the native open command", async () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});

    await openWorkspaceTarget("/Users/alice/dev/hecate", "terminal");

    expect(invokeMock).toHaveBeenCalledWith("open_workspace_target", {
      path: "/Users/alice/dev/hecate",
      target: "terminal",
    });
    expect(openWorkspaceTargetViaAPI).not.toHaveBeenCalled();
  });

  it("calls the local gateway outside the desktop runtime", async () => {
    await openWorkspaceTarget("/Users/alice/dev/hecate", "vscode");

    expect(openWorkspaceTargetViaAPI).toHaveBeenCalledWith("/Users/alice/dev/hecate", "vscode");
    expect(invokeMock).not.toHaveBeenCalled();
  });
});

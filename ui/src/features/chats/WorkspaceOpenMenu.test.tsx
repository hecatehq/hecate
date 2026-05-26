import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { openWorkspaceTarget } from "../../lib/workspace-open";

import { WorkspaceOpenMenu } from "./WorkspaceOpenMenu";

vi.mock("../../lib/workspace-open", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/workspace-open")>();
  return {
    ...actual,
    openWorkspaceTarget: vi.fn(async () => undefined),
  };
});

const originalPlatform = navigator.platform;

afterEach(() => {
  Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
  Reflect.deleteProperty(window, "__TAURI__");
  Object.defineProperty(navigator, "platform", { configurable: true, value: originalPlatform });
  localStorage.removeItem("hecate.workspaceOpen.defaultTarget");
  vi.mocked(openWorkspaceTarget).mockReset();
  vi.mocked(openWorkspaceTarget).mockResolvedValue(undefined);
});

describe("WorkspaceOpenMenu", () => {
  it("renders outside the desktop runtime so the browser can ask the local gateway", () => {
    render(<WorkspaceOpenMenu workspacePath="/Users/alice/dev/hecate" />);

    expect(screen.getByRole("button", { name: "Open workspace in VS Code" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Choose workspace opener" })).toBeTruthy();
  });

  it("opens the first target by default", async () => {
    render(<WorkspaceOpenMenu workspacePath="/Users/alice/dev/hecate" />);

    await userEvent.click(screen.getByRole("button", { name: "Open workspace in VS Code" }));

    expect(openWorkspaceTarget).toHaveBeenCalledWith("/Users/alice/dev/hecate", "vscode");
    expect(localStorage.getItem("hecate.workspaceOpen.defaultTarget")).toBe("vscode");
  });

  it("opens the selected workspace target in the desktop runtime", async () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(navigator, "platform", { configurable: true, value: "MacIntel" });

    render(<WorkspaceOpenMenu workspacePath="/Users/alice/dev/hecate" />);

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Choose workspace opener" }));
    await user.click(screen.getByRole("menuitem", { name: /Terminal/ }));

    expect(openWorkspaceTarget).toHaveBeenCalledWith("/Users/alice/dev/hecate", "terminal");
    expect(localStorage.getItem("hecate.workspaceOpen.defaultTarget")).toBe("terminal");
    await waitFor(() => {
      expect(screen.queryByRole("menu")).toBeNull();
    });
  });

  it("uses the last selected target as the next default", async () => {
    localStorage.setItem("hecate.workspaceOpen.defaultTarget", "cursor");

    render(<WorkspaceOpenMenu workspacePath="/Users/alice/dev/hecate" />);

    await userEvent.click(screen.getByRole("button", { name: "Open workspace in Cursor" }));

    expect(openWorkspaceTarget).toHaveBeenCalledWith("/Users/alice/dev/hecate", "cursor");
  });

  it("puts the saved target first in the picker", async () => {
    localStorage.setItem("hecate.workspaceOpen.defaultTarget", "terminal");

    render(<WorkspaceOpenMenu workspacePath="/Users/alice/dev/hecate" />);

    await userEvent.click(screen.getByRole("button", { name: "Choose workspace opener" }));

    const items = screen.getAllByRole("menuitem");
    expect(items[0]).toHaveTextContent("Terminal");
  });

  it("renders theme-aware target icons in the desktop runtime", async () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(navigator, "platform", { configurable: true, value: "MacIntel" });

    render(<WorkspaceOpenMenu workspacePath="/Users/alice/dev/hecate" />);

    await userEvent.click(screen.getByRole("button", { name: "Choose workspace opener" }));

    const menu = within(screen.getByRole("menu"));
    expect(menu.getByTestId("workspace-open-icon-vscode").querySelector("svg")).toBeTruthy();
    expect(menu.getByTestId("workspace-open-icon-cursor").querySelector("svg")).toBeTruthy();
    expect(menu.getByTestId("workspace-open-icon-xcode").querySelector("svg")).toBeTruthy();
    expect(menu.getByTestId("workspace-open-icon-terminal")).toHaveStyle({
      background: "var(--mono-icon)",
      color: "var(--bg0)",
    });
    expect(menu.getByTestId("workspace-open-icon-terminal").querySelector("svg")).toHaveStyle({
      filter: "var(--mono-icon-filter)",
    });
    expect(menu.getByTestId("workspace-open-icon-finder")).toHaveStyle({
      color: "var(--t1)",
    });
  });

  it("surfaces launcher errors without closing the menu", async () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    vi.mocked(openWorkspaceTarget).mockRejectedValueOnce(new Error("Terminal is not installed"));

    render(<WorkspaceOpenMenu workspacePath="/workspaces/hecate" />);

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Choose workspace opener" }));
    await user.click(screen.getByRole("menuitem", { name: /Terminal/ }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Terminal is not installed");
    expect(screen.getByRole("menu")).toBeTruthy();
  });
});

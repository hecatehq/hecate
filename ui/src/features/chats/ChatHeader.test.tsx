import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ChatHeader } from "./ChatHeader";

function renderChatHeader(overrides: Partial<Parameters<typeof ChatHeader>[0]> = {}) {
  const props: Parameters<typeof ChatHeader>[0] = {
    sidebarOpen: true,
    onOpenSidebar: vi.fn(),
    brand: "hecate",
    fallback: "H",
    title: "Hecate chat",
    subline: "Tools on",
    sublineHoverTitle: "Tools on",
    isAgentChat: true,
    isExternalAgentChat: false,
    showWorkspaceButton: true,
    workspacePath: "/Users/alice/dev/hecate",
    workspaceDialogOpen: false,
    workspaceChangesOpen: false,
    embeddedTerminalEnabled: true,
    terminalOpen: false,
    chatSettingsOpen: false,
    onChooseWorkspace: vi.fn(),
    onToggleWorkspaceChanges: vi.fn(),
    onToggleTerminal: vi.fn(),
    onToggleChatSettings: vi.fn(),
    activeChatSession: null,
    ...overrides,
  };
  return {
    ...render(<ChatHeader {...props} />),
    props,
  };
}

describe("ChatHeader", () => {
  it("shows the embedded terminal action only when a workspace is selected", () => {
    const { rerender, props } = renderChatHeader();

    expect(screen.getByRole("button", { name: "Terminal" })).toBeTruthy();

    rerender(<ChatHeader {...props} workspacePath="" />);

    expect(screen.queryByRole("button", { name: "Terminal" })).toBeNull();
  });

  it("toggles the embedded terminal from the header action", async () => {
    const onToggleTerminal = vi.fn();
    renderChatHeader({ onToggleTerminal });

    await userEvent.setup().click(screen.getByRole("button", { name: "Terminal" }));

    expect(onToggleTerminal).toHaveBeenCalledTimes(1);
  });

  it("hides the embedded terminal action when the backend capability is disabled", () => {
    renderChatHeader({ embeddedTerminalEnabled: false });

    expect(screen.queryByRole("button", { name: "Terminal" })).toBeNull();
  });
});

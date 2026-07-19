import { render, screen } from "@testing-library/react";
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
    isRemoteRuntime: false,
    showWorkspaceButton: true,
    workspacePath: "/Users/alice/dev/hecate",
    workspaceDialogOpen: false,
    workspaceChangesOpen: false,
    chatSettingsOpen: false,
    onChooseWorkspace: vi.fn(),
    onToggleWorkspaceChanges: vi.fn(),
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
  it("keeps workspace actions in the chat header without owning the global terminal", () => {
    const { rerender, props } = renderChatHeader();

    expect(screen.queryByRole("button", { name: "Terminal" })).toBeNull();
    expect(screen.getByRole("button", { name: "Workspace changes" })).toBeTruthy();

    rerender(<ChatHeader {...props} workspacePath="" />);

    expect(screen.queryByRole("button", { name: "Terminal" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Workspace changes" })).toBeNull();
  });

  it("hides local workspace opener controls in remote runtime", () => {
    renderChatHeader({ isRemoteRuntime: true });

    expect(screen.getByRole("button", { name: "Workspace changes" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Open workspace in/i })).toBeNull();
    expect(screen.queryByRole("button", { name: "Choose workspace opener" })).toBeNull();
  });

  it("labels the External Agent message budget as Chat turns", () => {
    renderChatHeader({
      isExternalAgentChat: true,
      activeChatSession: {
        id: "chat_1",
        title: "Codex chat",
        agent_id: "codex",
        status: "idle",
        workspace: "/tmp/hecate",
        turns_used: 3,
        max_turns_per_session: 10,
        messages: [],
      },
    });

    expect(screen.getByText("3/10 Chat turns")).toHaveAttribute("title", "3 of 10 Chat turns used");
  });
});

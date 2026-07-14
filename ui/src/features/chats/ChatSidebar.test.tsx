import { act, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { useChat } from "../../app/state/chat";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";

import { ChatSidebar } from "./ChatSidebar";
import type { ChatAgentOptionID } from "./ChatAgentControls";

function AtomicCreationHarness({
  onCreateChat,
  onSelectSession,
}: {
  onCreateChat: (agentID: ChatAgentOptionID, projectID: string) => void;
  onSelectSession: (sessionID: string) => void;
}) {
  const chat = useChat();
  return (
    <ChatSidebar
      isAgentChat
      onSelectSession={onSelectSession}
      onCreateChat={(agentID, projectID) => {
        if (chat.actions.beginChatCreation() === null) return;
        onCreateChat(agentID, projectID);
      }}
      onOpenAgentSetup={() => undefined}
    />
  );
}

describe("ChatSidebar new-chat creation", () => {
  it("atomically ignores a second same-tick creation intent before deselecting", () => {
    const onCreateChat = vi.fn();
    const onSelectSession = vi.fn();
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "chat_existing",
      activeChatSession: {
        id: "chat_existing",
        agent_id: "hecate",
        status: "idle",
        messages: [],
      } as any,
    });
    const actions = createRuntimeConsoleActions();

    render(
      withRuntimeConsole(
        <AtomicCreationHarness onCreateChat={onCreateChat} onSelectSession={onSelectSession} />,
        { state, actions },
      ),
    );

    const newChatButton = screen.getByRole("button", { name: "New Hecate chat" });
    act(() => {
      newChatButton.click();
      newChatButton.click();
    });

    expect(onSelectSession).toHaveBeenCalledTimes(1);
    expect(onSelectSession).toHaveBeenCalledWith("");
    expect(onCreateChat).toHaveBeenCalledTimes(1);
    expect(onCreateChat).toHaveBeenCalledWith("hecate", "");
  });

  it("explains the conditions for restoring a matching unsent draft", () => {
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "",
      activeChatSession: null,
      message: "",
      recoverableComposerDraft: {
        id: 1,
        content: "Continue the launch",
        scope: {
          projectID: "",
          agentID: "hecate",
          provider: "auto",
          model: "gpt-4o-mini",
          workspace: "",
        },
      },
    });

    render(
      withRuntimeConsole(
        <ChatSidebar
          isAgentChat
          onSelectSession={() => undefined}
          onCreateChat={() => undefined}
          onOpenAgentSetup={() => undefined}
        />,
        { state, actions: createRuntimeConsoleActions() },
      ),
    );

    expect(
      screen.getByText(
        "A previous unsent draft is saved. Start a matching new chat with an empty composer to restore it.",
      ),
    ).toBeTruthy();
  });

  it("does not offer restore instructions for the recovery already visible in the composer", () => {
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "",
      activeChatSession: null,
      message: "Continue the launch",
      recoverableComposerDraft: {
        id: 2,
        content: "Continue the launch",
        scope: {
          projectID: "",
          agentID: "hecate",
          provider: "auto",
          model: "gpt-4o-mini",
          workspace: "",
        },
      },
      activeRecoverableComposerDraftID: 2,
    });

    render(
      withRuntimeConsole(
        <ChatSidebar
          isAgentChat
          onSelectSession={() => undefined}
          onCreateChat={() => undefined}
          onOpenAgentSetup={() => undefined}
        />,
        { state, actions: createRuntimeConsoleActions() },
      ),
    );

    expect(screen.queryByText(/A previous unsent draft is saved/)).toBeNull();
  });

  it("locks both halves of the new-chat control while creation is pending", () => {
    const state = createRuntimeConsoleFixture({ chatCreating: true });

    render(
      withRuntimeConsole(
        <ChatSidebar
          isAgentChat
          onSelectSession={() => undefined}
          onCreateChat={() => undefined}
          onOpenAgentSetup={() => undefined}
        />,
        { state, actions: createRuntimeConsoleActions() },
      ),
    );

    expect(screen.getByRole("button", { name: "New Hecate chat" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Choose agent for new chat" })).toBeDisabled();
  });
});

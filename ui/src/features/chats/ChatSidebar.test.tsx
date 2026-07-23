import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useChat } from "../../app/state/chat";
import { getAgentPresets } from "../../lib/api";
import {
  queuedChatDeletedSessionStorageKey,
  queuedChatMessageStorageKey,
  queuedChatMessagesV2MarkerStorageKey,
} from "../../app/state/queuedChatStorage";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";

import { ChatSidebar } from "./ChatSidebar";
import type { ChatAgentOptionID } from "./ChatAgentControls";

vi.mock("../../lib/api", () => ({
  getAgentPresets: vi.fn(),
}));

const mockedGetAgentPresets = vi.mocked(getAgentPresets);

function AtomicCreationHarness({
  onCreateChat,
  onSelectSession,
}: {
  onCreateChat: (agentID: ChatAgentOptionID, projectID: string, agentPresetID?: string) => void;
  onSelectSession: (sessionID: string) => Promise<boolean>;
}) {
  const chat = useChat();
  return (
    <ChatSidebar
      isAgentChat
      onSelectSession={onSelectSession}
      onCreateChat={(agentID, projectID, agentPresetID) => {
        if (chat.actions.beginChatCreation() === null) return;
        if (agentPresetID) {
          onCreateChat(agentID, projectID, agentPresetID);
          return;
        }
        onCreateChat(agentID, projectID);
      }}
      onChooseWorkspace={() => undefined}
      onOpenAgentSetup={() => undefined}
    />
  );
}

describe("ChatSidebar new-chat creation", () => {
  beforeEach(() => {
    window.localStorage.clear();
    mockedGetAgentPresets.mockReset();
  });
  afterEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
  });
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

  it("loads Hecate-compatible presets on demand and includes the chosen preset in creation", async () => {
    mockedGetAgentPresets.mockResolvedValue({
      object: "agent_presets",
      data: [
        {
          id: "chat_review",
          name: "Chat review",
          surface: "hecate_chat",
          tools_enabled: false,
          writes_allowed: false,
          network_allowed: false,
          approval_policy: "inherit",
          project_memory_policy: "inherit",
          context_source_policy: "inherit",
        },
        {
          id: "any_work",
          name: "Any work",
          surface: "any",
          tools_enabled: true,
          writes_allowed: true,
          network_allowed: true,
          approval_policy: "inherit",
          project_memory_policy: "inherit",
          context_source_policy: "inherit",
        },
        {
          id: "task_only",
          name: "Task only",
          surface: "hecate_task",
          tools_enabled: true,
          writes_allowed: true,
          network_allowed: true,
          approval_policy: "inherit",
          project_memory_policy: "inherit",
          context_source_policy: "inherit",
        },
      ],
    });
    const onCreateChat = vi.fn();
    render(
      withRuntimeConsole(
        <ChatSidebar
          isAgentChat
          onSelectSession={async () => true}
          onCreateChat={onCreateChat}
          onChooseWorkspace={() => undefined}
          onOpenAgentSetup={() => undefined}
        />,
        { state: createRuntimeConsoleFixture(), actions: createRuntimeConsoleActions() },
      ),
    );

    const presetSelect = screen.getByRole("combobox", {
      name: "Agent preset for new Hecate chat",
    });
    expect(mockedGetAgentPresets).not.toHaveBeenCalled();
    fireEvent.focus(presetSelect);

    await waitFor(() => expect(mockedGetAgentPresets).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByRole("option", { name: "Chat review" })).toBeTruthy());
    expect(screen.getByRole("option", { name: "Any work" })).toBeTruthy();
    expect(screen.queryByRole("option", { name: "Task only" })).toBeNull();

    fireEvent.change(presetSelect, { target: { value: "chat_review" } });
    fireEvent.click(screen.getByRole("button", { name: "New Hecate chat" }));
    expect(onCreateChat).toHaveBeenCalledWith("hecate", "", "chat_review");
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
          agentPresetID: "",
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
          onSelectSession={async () => true}
          onCreateChat={() => undefined}
          onChooseWorkspace={() => undefined}
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
          agentPresetID: "",
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
          onSelectSession={async () => true}
          onCreateChat={() => undefined}
          onChooseWorkspace={() => undefined}
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
          onSelectSession={async () => true}
          onCreateChat={() => undefined}
          onChooseWorkspace={() => undefined}
          onOpenAgentSetup={() => undefined}
        />,
        { state, actions: createRuntimeConsoleActions() },
      ),
    );

    expect(screen.getByRole("button", { name: "New Hecate chat" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Choose agent for new chat" })).toBeDisabled();
  });

  it("reconstructs an already-deleted chat cleanup surface after remount", () => {
    const queued = {
      id: "queued-cleanup",
      session_id: "chat-deleted",
      project_id: "",
      content: "recover this queued prompt",
      execution_mode: "hecate_task" as const,
      tools_enabled: false,
      provider_filter: "openai" as const,
      model: "gpt-4o-mini",
      workspace: "",
      system_prompt: "",
      agent_id: "hecate",
      created_at: "2026-07-14T10:00:00Z",
      delivery_state: "reconcile_required" as const,
      delivery_storage_epoch: "0",
      delivery_storage_revision: "cleanup-revision",
      delivery_storage_failed: true,
    };
    const queueKey = queuedChatMessageStorageKey(
      queued.id,
      queued.delivery_storage_epoch,
      queued.delivery_storage_revision,
    );
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(queueKey, JSON.stringify(queued));
    window.localStorage.setItem(
      queuedChatDeletedSessionStorageKey(queued.session_id),
      "deleted:v1:0:cleanup-required",
    );
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (key === queueKey) return;
      originalRemoveItem(key);
    });
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "",
      activeChatSession: null,
      chatSessions: [],
      queuedChatMessages: [queued],
    });
    const renderSidebar = () =>
      render(
        withRuntimeConsole(
          <ChatSidebar
            isAgentChat
            onSelectSession={async () => true}
            onCreateChat={() => undefined}
            onChooseWorkspace={() => undefined}
            onOpenAgentSetup={() => undefined}
          />,
          { state, actions: createRuntimeConsoleActions() },
        ),
      );

    const first = renderSidebar();
    expect(screen.getByRole("button", { name: "Chat Deleted chat cleanup required" })).toBeTruthy();
    first.unmount();

    renderSidebar();
    act(() => {
      screen.getByRole("button", { name: "Chat Deleted chat cleanup required" }).click();
    });
    expect(screen.getByText(/Retry browser cleanup for this already-deleted chat/)).toBeTruthy();
  });

  it("keeps the cleanup recovery surface when local storage becomes unavailable", () => {
    const queued = {
      id: "queued-storage-unavailable",
      session_id: "chat-storage-unavailable",
      project_id: "",
      content: "preserve this queued prompt",
      execution_mode: "hecate_task" as const,
      tools_enabled: false,
      provider_filter: "openai" as const,
      model: "gpt-4o-mini",
      workspace: "",
      system_prompt: "",
      agent_id: "hecate",
      created_at: "2026-07-14T10:00:00Z",
      delivery_state: "reconcile_required" as const,
      delivery_storage_epoch: "0",
      delivery_storage_revision: "storage-unavailable-revision",
      delivery_storage_failed: true,
    };
    window.localStorage.setItem(queuedChatMessagesV2MarkerStorageKey, "1");
    window.localStorage.setItem(
      queuedChatDeletedSessionStorageKey(queued.session_id),
      "deleted:v1:0:storage-unavailable",
    );
    const queueKey = queuedChatMessageStorageKey(
      queued.id,
      queued.delivery_storage_epoch,
      queued.delivery_storage_revision,
    );
    window.localStorage.setItem(queueKey, JSON.stringify(queued));
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      if (key === queueKey) return;
      originalRemoveItem(key);
    });
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "",
      activeChatSession: null,
      chatSessions: [],
      queuedChatMessages: [queued],
    });

    render(
      withRuntimeConsole(
        <ChatSidebar
          isAgentChat
          onSelectSession={async () => true}
          onCreateChat={() => undefined}
          onChooseWorkspace={() => undefined}
          onOpenAgentSetup={() => undefined}
        />,
        { state, actions: createRuntimeConsoleActions() },
      ),
    );
    expect(screen.getByRole("button", { name: "Chat Deleted chat cleanup required" })).toBeTruthy();

    const storageGetter = vi.spyOn(window, "localStorage", "get").mockImplementation(() => {
      throw new DOMException("storage denied", "SecurityError");
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Search chats" }), {
      target: { value: "cleanup" },
    });

    expect(screen.getByRole("button", { name: "Chat Deleted chat cleanup required" })).toBeTruthy();
    storageGetter.mockRestore();
  });
});

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ChatView } from "./ChatView";
import {
  discoverLocalProviders,
  draftChatProjectAssistant,
  getTaskRunArtifact,
} from "../../lib/api";
import { readProjectAssistantChatHandoff } from "../../lib/project-assistant-chat-handoff";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";
import type { ProjectRecord } from "../../types/project";

const originalNavigatorClipboardDescriptor = Object.getOwnPropertyDescriptor(
  navigator,
  "clipboard",
);

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    discoverLocalProviders: vi.fn(async () => ({ object: "local_provider_discovery", data: [] })),
    draftChatProjectAssistant: vi.fn(async () => ({
      object: "project_assistant.proposal",
      data: {
        id: "pa_chat",
        title: "Plan next project work",
        summary: "Create a work item from chat.",
        actions: [
          {
            kind: "create_work_item",
            target: { project_id: "proj_1" },
            patch: { project_id: "proj_1", title: "Plan next project work" },
          },
        ],
        requires_confirmation: true,
      },
    })),
    getTaskRunArtifact: vi.fn(async () => ({
      object: "task_artifact",
      data: {
        id: "artifact_project_proposal",
        task_id: "task_1",
        run_id: "run_1",
        kind: "project_assistant_proposal",
        name: "Project Assistant proposal",
        mime_type: "application/json",
        storage_kind: "inline",
        status: "ready",
        content_text: JSON.stringify({
          object: "project_assistant.chat_proposal",
          project_id: "proj_1",
          source_chat_session_id: "s1",
          request: "Plan next project work",
          proposal_id: "pa_artifact",
          action_count: 1,
          proposal: {
            id: "pa_artifact",
            title: "Plan next project work",
            summary: "Create a work item from chat.",
            actions: [
              {
                kind: "create_work_item",
                target: { project_id: "proj_1" },
                patch: { project_id: "proj_1", title: "Plan next project work" },
              },
            ],
            requires_confirmation: true,
          },
        }),
      },
    })),
  };
});

afterEach(() => {
  localStorage.removeItem("hecate.chat.rightPanelWidth");
  sessionStorage.removeItem("hecate.projectAssistant.chatDraft");
  if (originalNavigatorClipboardDescriptor) {
    Object.defineProperty(navigator, "clipboard", originalNavigatorClipboardDescriptor);
  } else {
    delete (navigator as unknown as Record<string, unknown>).clipboard;
  }
  vi.mocked(discoverLocalProviders).mockReset();
  vi.mocked(discoverLocalProviders).mockResolvedValue({
    object: "local_provider_discovery",
    data: [],
  });
  vi.mocked(draftChatProjectAssistant).mockReset();
  vi.mocked(draftChatProjectAssistant).mockResolvedValue({
    object: "project_assistant.proposal",
    data: {
      id: "pa_chat",
      title: "Plan next project work",
      summary: "Create a work item from chat.",
      actions: [
        {
          kind: "create_work_item",
          target: { project_id: "proj_1" },
          patch: { project_id: "proj_1", title: "Plan next project work" },
        },
      ],
      requires_confirmation: true,
    },
  });
  vi.mocked(getTaskRunArtifact).mockReset();
  vi.mocked(getTaskRunArtifact).mockResolvedValue({
    object: "task_artifact",
    data: {
      id: "artifact_project_proposal",
      task_id: "task_1",
      run_id: "run_1",
      kind: "project_assistant_proposal",
      name: "Project Assistant proposal",
      mime_type: "application/json",
      storage_kind: "inline",
      status: "ready",
      content_text: JSON.stringify({
        object: "project_assistant.chat_proposal",
        project_id: "proj_1",
        source_chat_session_id: "s1",
        request: "Plan next project work",
        proposal_id: "pa_artifact",
        action_count: 1,
        proposal: {
          id: "pa_artifact",
          title: "Plan next project work",
          summary: "Create a work item from chat.",
          actions: [
            {
              kind: "create_work_item",
              target: { project_id: "proj_1" },
              patch: { project_id: "proj_1", title: "Plan next project work" },
            },
          ],
          requires_confirmation: true,
        },
      }),
    },
  });
});

function setup(stateOverrides: Record<string, any> = {}, actionOverrides = {}) {
  const hasActiveSessionIDOverride = Object.prototype.hasOwnProperty.call(
    stateOverrides,
    "activeChatSessionID",
  );
  const hasActiveSessionOverride = Object.prototype.hasOwnProperty.call(
    stateOverrides,
    "activeChatSession",
  );
  const activeChatSessionID = hasActiveSessionIDOverride
    ? stateOverrides.activeChatSessionID
    : hasActiveSessionOverride
      ? (stateOverrides.activeChatSession?.id ?? "")
      : "chat_1";
  const provider =
    typeof stateOverrides.providerFilter === "string" && stateOverrides.providerFilter !== "auto"
      ? stateOverrides.providerFilter
      : "openai";
  const model = typeof stateOverrides.model === "string" ? stateOverrides.model : "gpt-4o-mini";
  const chatTarget = stateOverrides.chatTarget ?? "agent";
  const isExternalChat = chatTarget === "external_agent";
  const agentID =
    typeof stateOverrides.agentAdapterID === "string" ? stateOverrides.agentAdapterID : "codex";
  const activeChatSession = hasActiveSessionOverride
    ? stateOverrides.activeChatSession
    : activeChatSessionID
      ? ({
          id: activeChatSessionID,
          agent_id: isExternalChat ? agentID : "hecate",
          driver_kind: isExternalChat ? "acp" : undefined,
          execution_mode: isExternalChat ? "external_agent" : "hecate_task",
          tools_enabled: isExternalChat
            ? undefined
            : stateOverrides.defaultChatToolsEnabled !== false,
          title: "New chat",
          provider: isExternalChat ? undefined : provider,
          model: isExternalChat ? undefined : model,
          capabilities: { tool_calling: "basic" },
          status: "idle",
          workspace: stateOverrides.agentWorkspace,
          messages: [],
        } as any)
      : null;
  const state = createRuntimeConsoleFixture({
    agentWorkspace: "/tmp/hecate",
    activeChatSessionID,
    activeChatSession,
    providerScopedModels: [
      {
        id: "gpt-4o-mini",
        owned_by: "openai",
        metadata: { provider: "openai", provider_kind: "cloud" },
      },
    ],
    ...stateOverrides,
  });
  const actions = { ...createRuntimeConsoleActions(), ...actionOverrides };
  return { state, actions };
}

function expectBefore(before: Element, after: Element) {
  expect(before.compareDocumentPosition(after) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
}

function mockTextareaScrollHeight(textarea: HTMLTextAreaElement, initial: number) {
  let scrollHeight = initial;
  Object.defineProperty(textarea, "scrollHeight", {
    configurable: true,
    get: () => scrollHeight,
  });
  return (next: number) => {
    scrollHeight = next;
  };
}

describe("ChatView input", () => {
  it("renders Hecate first in the unified agent picker", async () => {
    const { state, actions } = setup({
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: false,
          status: "missing",
          cost_mode: "external",
        },
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-code-acp-adapter",
          available: false,
          status: "missing",
          cost_mode: "external",
        },
        {
          id: "cursor_agent",
          name: "Cursor Agent",
          kind: "acp",
          command: "cursor-agent",
          available: false,
          status: "missing",
          cost_mode: "external",
        },
        {
          id: "grok_build",
          name: "Grok Build",
          kind: "acp",
          command: "grok",
          available: false,
          status: "missing",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByRole("button", { name: "New Hecate chat" })).toBeTruthy();
    const picker = screen.getByRole("button", { name: "Choose agent for new chat" });

    const user = userEvent.setup();
    await user.click(picker);
    const options = screen.getAllByRole("option");
    expect(options.map((option) => option.textContent?.replace(/\s+/g, " ").trim())).toEqual([
      "Hecatelocal",
      "Codexsetup",
      "Claude Codesetup",
      "Cursor Agentsetup",
      "Grok Buildsetup",
    ]);
  }, 10_000);

  it("waits for a Grok Build ACP session before showing session config controls", async () => {
    const createChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentAdapterID: "grok_build",
        newChatAgentID: "grok_build",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "",
        activeChatSession: null,
        agentAdapters: [
          {
            id: "grok_build",
            name: "Grok Build",
            kind: "acp",
            command: "grok",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { createChatSession },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /New Grok Build chat/i }));

    expect(createChatSession).toHaveBeenCalledWith({ agentID: "grok_build", projectID: "" });
    expect(screen.queryByRole("button", { name: "Model" })).toBeNull();
  });

  it("creates a Grok Build chat before the launch model is selected", async () => {
    const createChatSession = vi.fn(async () => undefined);
    const selectChatSession = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentAdapterID: "grok_build",
        newChatAgentID: "grok_build",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "",
        activeChatSession: null,
        agentAdapters: [
          {
            id: "grok_build",
            name: "Grok Build",
            kind: "acp",
            command: "grok",
            available: true,
            status: "available",
            cost_mode: "external",
            config_options: [
              {
                id: "model",
                name: "Model",
                category: "model",
                type: "select",
                current_value: "__hecate_no_model_selected__",
                options: [
                  { value: "__hecate_no_model_selected__", name: "Pick a model" },
                  { value: "grok-latest", name: "Grok Latest" },
                ],
              },
            ],
          },
        ],
      },
      { createChatSession, selectChatSession },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "New Grok Build chat" }));

    expect(createChatSession).toHaveBeenCalledWith({ agentID: "grok_build", projectID: "" });
    expect(selectChatSession).toHaveBeenCalledWith("");
  });

  it("keeps external-agent chat creation blocked until a workspace is selected", async () => {
    const createChatSession = vi.fn(async () => undefined);
    const chooseAgentWorkspace = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentAdapterID: "grok_build",
        newChatAgentID: "grok_build",
        agentWorkspace: "",
        activeChatSessionID: "",
        activeChatSession: null,
        agentAdapters: [
          {
            id: "grok_build",
            name: "Grok Build",
            kind: "acp",
            command: "grok",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { createChatSession, chooseAgentWorkspace },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    const newChatButton = screen.getByRole("button", { name: "New Grok Build chat" });
    expect(newChatButton).toBeDisabled();
    await user.click(newChatButton);

    expect(
      screen.getByText("Choose a workspace in the chat view before starting agent chats."),
    ).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Choose workspace" }));
    expect(chooseAgentWorkspace).toHaveBeenCalled();
    expect(createChatSession).not.toHaveBeenCalled();
  });

  it("uses the selected project root as the workspace for new external-agent chats", async () => {
    const createChatSession = vi.fn(async () => undefined);
    const project: ProjectRecord = {
      id: "proj_1",
      name: "Hecate",
      roots: [
        {
          id: "root_1",
          path: "/workspace/hecate",
          kind: "workspace",
          active: true,
          created_at: "2026-05-29T00:00:00Z",
          updated_at: "2026-05-29T00:00:00Z",
        },
      ],
      default_root_id: "root_1",
      created_at: "2026-05-29T00:00:00Z",
      updated_at: "2026-05-29T00:00:00Z",
    };
    const { state, actions } = setup(
      {
        activeProjectID: "proj_1",
        projects: [project],
        chatTarget: "external_agent",
        agentAdapterID: "grok_build",
        newChatAgentID: "grok_build",
        agentWorkspace: "",
        activeChatSessionID: "",
        activeChatSession: null,
        agentAdapters: [
          {
            id: "grok_build",
            name: "Grok Build",
            kind: "acp",
            command: "grok",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { createChatSession },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    const newChatButton = screen.getByRole("button", { name: "New Grok Build chat" });
    expect(newChatButton).not.toBeDisabled();
    expect(
      screen.queryByText("Choose a workspace in the chat view before starting agent chats."),
    ).toBeNull();

    await user.click(newChatButton);

    expect(createChatSession).toHaveBeenCalledWith({ agentID: "grok_build", projectID: "proj_1" });
  });

  it("allows direct Hecate model chats before a workspace is selected", async () => {
    const createChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        agentWorkspace: "",
        activeChatSessionID: "",
        activeChatSession: null,
      },
      { createChatSession },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    const newChatButton = screen.getByRole("button", { name: "New Hecate chat" });
    expect(newChatButton).not.toBeDisabled();
    expect(
      screen.queryByText("Choose a workspace in the chat view before starting agent chats."),
    ).toBeNull();

    await user.click(newChatButton);

    expect(createChatSession).toHaveBeenCalledWith({ agentID: "hecate", projectID: "" });
  });

  it("allows Hecate chat creation without a workspace even when tools are enabled", async () => {
    const createChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: true,
        agentWorkspace: "",
        activeChatSessionID: "",
        activeChatSession: null,
      },
      { createChatSession },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    const newChatButton = screen.getByRole("button", { name: "New Hecate chat" });
    expect(newChatButton).not.toBeDisabled();
    expect(
      screen.queryByText("Choose a workspace in the chat view before starting agent chats."),
    ).toBeNull();

    await user.click(newChatButton);

    expect(createChatSession).toHaveBeenCalledWith({ agentID: "hecate", projectID: "" });
  });

  it("shows Grok Build session-owned model and thinking controls", async () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "codex",
      activeChatSessionID: "chat_grok",
      activeChatSession: {
        id: "chat_grok",
        agent_id: "grok_build",
        title: "Grok work",
        workspace: "/tmp/hecate",
        status: "idle",
        config_options: [
          {
            id: "web_search",
            name: "Web search",
            type: "select",
            current_value: "auto",
            options: [
              { value: "off", name: "Off" },
              { value: "auto", name: "Auto" },
            ],
          },
          {
            id: "verbosity",
            name: "Verbosity",
            type: "select",
            current_value: "normal",
            options: [
              { value: "normal", name: "Normal" },
              { value: "detailed", name: "Detailed" },
            ],
          },
          {
            id: "model",
            name: "Model",
            type: "select",
            current_value: "grok-code-fast-1",
            options: [
              { value: "grok-code-fast-1", name: "Grok Code Fast 1" },
              { value: "grok-code-pro-1", name: "Grok Code Pro 1" },
            ],
          },
          {
            id: "thinking_level",
            name: "Level of thinking",
            type: "select",
            current_value: "medium",
            options: [
              { value: "low", name: "Low" },
              { value: "medium", name: "Medium" },
              { value: "high", name: "High" },
            ],
          },
          {
            id: "approval_mode",
            name: "Approval mode",
            type: "select",
            current_value: "ask",
            options: [
              { value: "ask", name: "Ask" },
              { value: "auto", name: "Auto" },
            ],
          },
        ],
        messages: [],
      } as any,
      agentAdapters: [
        {
          id: "grok_build",
          name: "Grok Build",
          kind: "acp",
          command: "grok",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent(
      "Grok Code Fast 1",
    );
    expect(screen.getByRole("button", { name: "Level of thinking" })).toHaveTextContent("Medium");
    expect(screen.queryByRole("button", { name: "Verbosity" })).toBeNull();
    expect(screen.getByRole("textbox", { name: "Message" })).toBeTruthy();
  });

  it("toggles Hecate Chat between direct model chat and tool-backed agent mode", async () => {
    const setChatToolsEnabled = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: true,
        providerScopedModels: [
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: {
              provider: "openai",
              provider_kind: "cloud",
              capabilities: { tool_calling: "basic", streaming: true, source: "catalog" },
            },
          },
        ],
      },
      { setChatToolsEnabled },
    );
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /settings/i }));
    expect(screen.getByText("Mode")).toBeTruthy();
    expect(screen.getByText("Tools")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Tools on" }));
    expect(setChatToolsEnabled).toHaveBeenCalledWith(false);

    const directState = setup(
      { ...state, defaultChatToolsEnabled: false },
      { setChatToolsEnabled },
    ).state;
    rerender(withRuntimeConsole(<ChatView />, { state: directState, actions }));
    expect(screen.getByRole("button", { name: "Tools off" })).toHaveTextContent("off");

    await user.click(screen.getByRole("button", { name: "Tools off" }));
    expect(setChatToolsEnabled).toHaveBeenCalledWith(true);
  });

  it("keeps tools off for an existing Hecate session when the next turn is direct model chat", async () => {
    const setChatToolsEnabled = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        // Per-session pin: this session was explicitly toggled to
        // tools-off, which takes precedence over both message-derived
        // hints and the user default.
        chatToolsEnabledBySessionID: new Map([["chat_1", false]]),
        providerScopedModels: [
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: {
              provider: "openai",
              provider_kind: "cloud",
              capabilities: { tool_calling: "basic" },
            },
          },
        ],
        activeChatSessionID: "chat_1",
        activeChatSession: {
          id: "chat_1",
          execution_mode: "hecate_task",
          title: "Repo work",
          provider: "openai",
          model: "gpt-4o-mini",
          capabilities: { tool_calling: "basic" },
          workspace: "/workspace",
          status: "completed",
          messages: [],
        } as any,
      },
      { setChatToolsEnabled },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /settings/i }));

    expect(screen.getByRole("button", { name: "Tools off" })).toHaveTextContent("off");
    await user.click(screen.getByRole("button", { name: "Tools off" }));
    expect(setChatToolsEnabled).toHaveBeenCalledWith(true);
  });

  it("shows editable system prompt instructions in chat settings before the first message", async () => {
    const setSystemPrompt = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        systemPrompt: "Prefer small, reviewable diffs.",
        providerScopedModels: [
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: {
              provider: "openai",
              provider_kind: "cloud",
              capabilities: { tool_calling: "basic", streaming: true, source: "catalog" },
            },
          },
        ],
      },
      { setSystemPrompt },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /settings/i }));

    expect(screen.getByText("SYSTEM PROMPT / AGENT INSTRUCTIONS")).toBeTruthy();
    const editor = screen.getByRole("textbox", { name: "System prompt / agent instructions" });
    expect(editor).toHaveValue("Prefer small, reviewable diffs.");
    fireEvent.change(editor, { target: { value: "Use short patches." } });
    expect(setSystemPrompt).toHaveBeenLastCalledWith("Use short patches.");
  });

  it("keeps Hecate system prompt visible when the active session is Hecate-backed", async () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      systemPrompt: "Keep explanations short.",
      activeChatSessionID: "chat_hecate",
      activeChatSession: {
        id: "chat_hecate",
        execution_mode: "hecate_task",
        title: "Repo work",
        task_id: "task_hecate_123456",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/Users/alice/dev/hecate",
        status: "completed",
        messages: [],
      } as any,
      model: "qwen2.5-coder",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Chat settings" }));

    expect(screen.getByText("System prompt")).toBeTruthy();
    expect(screen.getByRole("textbox", { name: "System prompt / instructions" })).toHaveValue(
      "Keep explanations short.",
    );
  });

  it("shows per-chat settings and toggles compact command output", async () => {
    const setHecateRTKEnabled = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        activeChatSessionID: "chat_1",
        activeChatSession: {
          id: "chat_1",
          execution_mode: "hecate_task",
          title: "Repo work",
          task_id: "task_hecate_123456",
          provider: "ollama",
          model: "qwen2.5-coder",
          rtk_enabled: false,
          workspace: "/Users/alice/dev/hecate",
          status: "completed",
          messages: [
            {
              id: "msg_user",
              role: "user",
              content: "show git status",
              created_at: "2026-05-01T10:00:00Z",
            },
          ],
        } as any,
        hecateRTKEnabled: false,
        hecateRTKAvailable: true,
        hecateRTKPath: "/usr/local/bin/rtk",
        providerFilter: "ollama",
        model: "qwen2.5-coder",
      },
      { setHecateRTKEnabled },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /settings/i }));

    expect(screen.getByText("Chat settings")).toBeTruthy();
    expect(screen.getByText("Compact command output")).toBeTruthy();
    expect(screen.getByText("Session context")).toBeTruthy();
    expect(screen.queryByText("Runtime debug")).toBeNull();
    expect(screen.getByText("Provider")).toBeTruthy();
    expect(screen.queryByText("All providers")).toBeNull();
    expect(screen.getByText("Workspace")).toBeTruthy();
    expect(screen.getByText("/Users/alice/dev/hecate")).toBeTruthy();
    expect(screen.getByText("Status")).toBeTruthy();
    expect(screen.getByText("completed")).toBeTruthy();
    expect(screen.getByText("Messages")).toBeTruthy();
    expect(screen.getByText("1")).toBeTruthy();
    expect(screen.getAllByText(/rtk sh -lc/i).length).toBeGreaterThan(0);
    expect(screen.getByText(/usr\/local\/bin\/rtk/i)).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Compact command output off" }));

    expect(setHecateRTKEnabled).toHaveBeenCalledWith(true);
  });

  it("does not show the RTK onboarding hint after RTK is explicitly turned off in settings", async () => {
    const setHecateRTKEnabled = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        message: "",
        hecateRTKEnabled: true,
        hecateRTKAvailable: true,
        hecateRTKPath: "/usr/local/bin/rtk",
        providerScopedModels: [
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: {
              provider: "openai",
              provider_kind: "cloud",
              capabilities: { tool_calling: "basic", streaming: true, source: "catalog" },
            },
          },
        ],
      },
      { setHecateRTKEnabled },
    );
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "New Hecate chat" }));
    await user.click(screen.getByRole("button", { name: "Chat settings" }));
    await user.click(screen.getByRole("button", { name: "Compact command output on" }));
    expect(setHecateRTKEnabled).toHaveBeenCalledWith(false);

    rerender(
      withRuntimeConsole(<ChatView />, { state: { ...state, hecateRTKEnabled: false }, actions }),
    );
    await user.click(screen.getByRole("button", { name: "Chat settings" }));

    expect(screen.queryByText("Compact command output is available")).toBeNull();
  });

  it("does not expose Hecate instructions for External Agent chats", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "codex",
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByText("SYSTEM PROMPT / AGENT INSTRUCTIONS")).toBeNull();
    expect(screen.queryByText("SYSTEM PROMPT / INSTRUCTIONS")).toBeNull();
  });

  it("does not leak external-agent controls into an empty Hecate chat shell", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "codex",
      activeChatSessionID: "chat_hecate_empty",
      activeChatSession: {
        id: "chat_hecate_empty",
        agent_id: "hecate",
        title: "Hecate Chat",
        provider: "",
        model: "",
        status: "idle",
        messages: [],
      } as any,
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Hecate Chat")).toBeTruthy();
    expect(screen.queryByText("GPT-5.5")).toBeNull();
    expect(screen.queryByText("reasoning")).toBeNull();
    expect(screen.queryByText("mode")).toBeNull();
    expect(screen.queryByText("External agents run as your OS user")).toBeNull();
  });

  it("surfaces agent-provided instructions in external-agent chat settings", async () => {
    const setChatConfigOption = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentAdapterID: "codex",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          agent_id: "codex",
          title: "Codex work",
          workspace: "/Users/alice/dev/hecate",
          status: "idle",
          config_options: [
            {
              id: "system_prompt",
              name: "System prompt",
              description: "Instructions applied by the agent.",
              category: "instructions",
              type: "text",
              current_value: "Be concise.",
            },
            {
              id: "model",
              name: "Model",
              category: "model",
              type: "select",
              current_value: "fast",
              options: [{ value: "fast", name: "Fast" }],
            },
            {
              id: "reasoning_effort",
              name: "Reasoning",
              category: "reasoning",
              type: "select",
              current_value: "low",
              options: [{ value: "low", name: "Low" }],
            },
          ],
          messages: [],
        } as any,
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { setChatConfigOption },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Chat settings" }));

    expect(screen.getByText("Agent settings")).toBeTruthy();
    expect(screen.getByText("Instructions applied by the agent.")).toBeTruthy();
    const panel = screen.getByRole("complementary", { name: "Chat settings panel" });
    expect(within(panel).queryByText("Model")).toBeNull();
    expect(within(panel).queryByText("Reasoning")).toBeNull();
    const editor = screen.getByRole("textbox", { name: "System prompt / instructions" });
    expect(editor).toHaveValue("Be concise.");

    await user.clear(editor);
    await user.type(editor, "Prefer short answers.");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(setChatConfigOption).toHaveBeenCalledWith(
      "a1",
      "system_prompt",
      "Prefer short answers.",
    );
  });

  it("disables the send button when message is empty", () => {
    const { state, actions } = setup({ message: "" });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    fireEvent.click(screen.getByRole("button", { name: /new .* chat/i }));
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });

  it("enables the send button when message has content", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      message: "hello",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(false);
  });

  it("does not render a manual composer panel resize handle", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      message: "hello",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByRole("separator", { name: "Resize chat composer panel" })).toBeNull();
  });

  it("grows and shrinks the message box as lines are added or removed", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      message: "hello",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    const setScrollHeight = mockTextareaScrollHeight(textarea, 80);

    fireEvent.input(textarea);
    const grownHeight = Number.parseFloat(textarea.style.height);
    expect(grownHeight).toBeGreaterThan(60);
    expect(textarea.style.overflowY).toBe("hidden");

    setScrollHeight(70);
    fireEvent.input(textarea);
    expect(Number.parseFloat(textarea.style.height)).toBeLessThan(grownHeight);
    expect(textarea.style.overflowY).toBe("hidden");
  });

  it("caps the message box at ten lines and makes it scrollable", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      message: Array.from({ length: 12 }, (_, index) => `line ${index + 1}`).join("\n"),
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    const setScrollHeight = mockTextareaScrollHeight(textarea, 400);

    fireEvent.input(textarea);
    const cappedHeight = Number.parseFloat(textarea.style.height);
    expect(cappedHeight).toBeLessThan(240);
    expect(textarea.style.overflowY).toBe("auto");

    setScrollHeight(220);
    fireEvent.input(textarea);
    expect(Number.parseFloat(textarea.style.height)).toBe(cappedHeight);
    expect(textarea.style.overflowY).toBe("auto");
  });

  it("keeps Hecate Chat composer editable but blocks send until a model is selected", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      model: "",
      message: "hello",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    expect(textarea.disabled).toBe(false);
    expect(textarea.placeholder).toMatch(/^Message…/);
    const send = screen.getByRole("button", { name: "Send message" }) as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });

  it("keeps model composer editable but blocks send when no provider is configured", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      message: "hello",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText("No model provider configured")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Open Connections/i })).toBeTruthy();
    const textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    expect(textarea.disabled).toBe(false);
    const send = screen.getByRole("button", { name: "Send message" }) as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });

  it("blocks sending when the selected model is not discovered by the selected provider", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      providerFilter: "ollama",
      model: "llama3.1:8b",
      message: "hello",
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "ollama",
            name: "Ollama",
            preset_id: "ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: false,
          },
        ],
        policy_rules: [],
        events: [],
      },
      providers: [
        {
          name: "ollama",
          kind: "local",
          healthy: true,
          status: "degraded",
          base_url: "http://127.0.0.1:11434/v1",
          models: ["qwen2.5:7b"],
          model_count: 1,
          routing_blocked_reason: "no discovered route",
          last_error: "model discovery returned no llama3.1:8b",
        },
      ],
      providerScopedModels: [
        {
          id: "qwen2.5:7b",
          owned_by: "ollama",
          metadata: { provider: "ollama", provider_kind: "local" },
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(
      screen.getAllByText("Selected model is not available from this provider").length,
    ).toBeGreaterThan(0);
    expect(
      screen.getAllByText(/Ollama is configured, but it does not currently report "llama3.1:8b"/)
        .length,
    ).toBeGreaterThan(0);
    expect(screen.getAllByText("Selected model").length).toBeGreaterThan(0);
    expect(screen.getAllByText("llama3.1:8b").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Discovered models").length).toBeGreaterThan(0);
    expect(screen.getAllByText("1").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Health").length).toBeGreaterThan(0);
    expect(screen.getAllByText("degraded").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Blocked by").length).toBeGreaterThan(0);
    expect(screen.getAllByText("no discovered route").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Last error").length).toBeGreaterThan(0);
    expect(screen.getAllByText("model discovery returned no llama3.1:8b").length).toBeGreaterThan(
      0,
    );
    expect(screen.getAllByText("Start the local provider app or server.").length).toBeGreaterThan(
      0,
    );
    expect(
      screen.getAllByText(
        "Pull or load llama3.1:8b in that provider, or pick one of its discovered models.",
      ).length,
    ).toBeGreaterThan(0);
    const textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    expect(textarea.disabled).toBe(false);
    const send = screen.getByRole("button", { name: "Send message" }) as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });

  it("shows stale selected-model readiness on existing transcripts", async () => {
    const onNavigate = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      providerFilter: "ollama",
      model: "llama3.1:8b",
      message: "hello",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        title: "Existing chat",
        execution_mode: "hecate_task",
        tools_enabled: false,
        status: "completed",
        provider: "ollama",
        model: "llama3.1:8b",
        messages: [
          {
            id: "m1",
            role: "user",
            content: "hi",
            execution_mode: "hecate_task",
            tools_enabled: false,
            created_at: "2026-04-20T00:00:00Z",
          },
        ],
      },
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "ollama",
            name: "Ollama",
            preset_id: "ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: false,
          },
        ],
        policy_rules: [],
        events: [],
      },
      providers: [
        {
          name: "ollama",
          kind: "local",
          healthy: true,
          status: "healthy",
          base_url: "http://127.0.0.1:11434/v1",
          models: ["qwen2.5:7b"],
          model_count: 1,
        },
      ],
      providerScopedModels: [
        {
          id: "qwen2.5:7b",
          owned_by: "ollama",
          metadata: { provider: "ollama", provider_kind: "local" },
        },
      ],
    });
    render(withRuntimeConsole(<ChatView onNavigate={onNavigate} />, { state, actions }));

    expect(screen.getByText("Selected model is not available from this provider")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Open Connections" })).toBeTruthy();
    const textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    expect(textarea.disabled).toBe(false);

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Open Connections" }));
    expect(onNavigate).toHaveBeenCalledWith("connections");
  });

  it("offers the backend-suggested model as a one-click repair", async () => {
    const setModel = vi.fn();
    const setProviderFilter = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        providerFilter: "anthropic",
        model: "claude-sonnet-4-6",
        message: "hello",
        settingsConfig: {
          backend: "memory",
          providers: [
            {
              id: "anthropic",
              name: "Anthropic",
              preset_id: "anthropic",
              kind: "cloud",
              protocol: "anthropic",
              base_url: "https://api.anthropic.com/v1",
              credential_configured: false,
            },
          ],
          policy_rules: [],
          events: [],
        },
        providerScopedModels: [
          {
            id: "claude-sonnet-4-6",
            owned_by: "anthropic",
            metadata: {
              provider: "anthropic",
              provider_kind: "cloud",
              readiness: {
                ready: false,
                status: "blocked",
                reason: "credential_missing",
                message: "Anthropic needs credentials before this model can route.",
                suggested_models: ["gpt-4o-mini"],
              },
            },
          },
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: { provider: "openai", provider_kind: "cloud" },
          },
        ],
      },
      { setModel, setProviderFilter },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getAllByRole("button", { name: "Use gpt-4o-mini" })[0]);

    expect(setProviderFilter).toHaveBeenCalledWith("auto");
    expect(setModel).toHaveBeenCalledWith("gpt-4o-mini");
  });

  it("opens Connections from the model empty state", async () => {
    const onNavigate = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView onNavigate={onNavigate} />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /Open Connections/i }));

    expect(onNavigate).toHaveBeenCalledWith("connections");
  });

  it("keeps configured-provider model discovery repair compact in the empty state", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      providerFilter: "ollama",
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "ollama",
            name: "Ollama",
            preset_id: "ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: false,
          },
        ],
        policy_rules: [],
        events: [],
      },
      providers: [
        {
          name: "ollama",
          kind: "local",
          healthy: true,
          status: "healthy",
          base_url: "http://127.0.0.1:11434/v1",
          models: [],
          model_count: 0,
          readiness_checks: [
            {
              name: "credentials",
              status: "ok",
              reason: "not_required",
              message: "No credentials are required for this provider.",
            },
            {
              name: "models",
              status: "blocked",
              reason: "no_models",
              message: "No models were discovered and no default model is configured.",
            },
            {
              name: "routing",
              status: "blocked",
              reason: "no_models",
              message: "Routing is blocked because no models are available.",
            },
          ],
        },
      ],
      providerScopedModels: [],
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("No routable model")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Open Connections/i })).toBeTruthy();
    expect(screen.queryByText("No models discovered")).toBeNull();
    expect(screen.queryByText("Routing is blocked because no models are available.")).toBeNull();
    expect(screen.queryByText("Detected locally")).toBeNull();
    expect(screen.queryByRole("button", { name: /Add selected/i })).toBeNull();
  });

  it("quick-adds all installed local providers from the model empty state", async () => {
    vi.mocked(discoverLocalProviders).mockResolvedValueOnce({
      object: "local_provider_discovery",
      data: [
        {
          preset_id: "ollama",
          name: "Ollama",
          base_url: "http://127.0.0.1:11434/v1",
          probe_url: "http://127.0.0.1:11434/api/tags",
          status: "installed",
          command: "ollama",
          command_available: true,
          command_path: "/usr/local/bin/ollama",
          http_available: false,
          model_count: 0,
          models: [],
        },
        {
          preset_id: "lmstudio",
          name: "LM Studio",
          base_url: "http://127.0.0.1:1234/v1",
          probe_url: "http://127.0.0.1:1234/v1/models",
          status: "running",
          command: "lms",
          command_available: true,
          command_path: "/Users/alice/.lmstudio/bin/lms",
          http_available: true,
          model_count: 1,
          models: ["qwen2.5"],
        },
      ],
    });
    const createProvider = vi.fn(async () => undefined);
    const loadDashboard = vi.fn(async () => undefined);
    const onNavigate = vi.fn();
    const setProviderFilter = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
        providerPresets: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            description: "",
          },
          {
            id: "lmstudio",
            name: "LM Studio",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:1234/v1",
            description: "",
          },
        ],
        providerScopedModels: [],
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { createProvider, loadDashboard, setProviderFilter },
    );
    render(withRuntimeConsole(<ChatView onNavigate={onNavigate} />, { state, actions }));

    const user = userEvent.setup();
    const quickAdd = await screen.findByRole("button", { name: /Add selected/i });
    const connectionsActions = screen.getAllByRole("button", { name: "Open Connections" });
    expect(connectionsActions).toHaveLength(1);
    expect(screen.getByText("Ollama")).toBeTruthy();
    expect(screen.getByText("LM Studio")).toBeTruthy();
    await user.click(connectionsActions[0]);
    expect(onNavigate).toHaveBeenCalledWith("connections");
    await user.click(quickAdd);

    expect(createProvider).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({
        name: "Ollama",
        preset_id: "ollama",
        base_url: "http://127.0.0.1:11434/v1",
        kind: "local",
        protocol: "openai",
      }),
      { refresh: false },
    );
    expect(createProvider).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        name: "LM Studio",
        preset_id: "lmstudio",
        base_url: "http://127.0.0.1:1234/v1",
        kind: "local",
        protocol: "openai",
      }),
      { refresh: false },
    );
    expect(loadDashboard).toHaveBeenCalledTimes(1);
    expect(setProviderFilter).toHaveBeenCalledWith("lmstudio");
  });

  it("quick-adds only selected local providers", async () => {
    vi.mocked(discoverLocalProviders).mockResolvedValueOnce({
      object: "local_provider_discovery",
      data: [
        {
          preset_id: "ollama",
          name: "Ollama",
          base_url: "http://127.0.0.1:11434/v1",
          probe_url: "http://127.0.0.1:11434/api/tags",
          status: "running",
          command: "ollama",
          command_available: true,
          command_path: "/usr/local/bin/ollama",
          http_available: true,
          model_count: 1,
          models: ["llama3.1:8b"],
        },
        {
          preset_id: "lmstudio",
          name: "LM Studio",
          base_url: "http://127.0.0.1:1234/v1",
          probe_url: "http://127.0.0.1:1234/v1/models",
          status: "running",
          command: "lms",
          command_available: true,
          command_path: "/Users/alice/.lmstudio/bin/lms",
          http_available: true,
          model_count: 1,
          models: ["qwen2.5"],
        },
      ],
    });
    const createProvider = vi.fn(async () => undefined);
    const loadDashboard = vi.fn(async () => undefined);
    const setProviderFilter = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
        providerPresets: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            description: "",
          },
          {
            id: "lmstudio",
            name: "LM Studio",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:1234/v1",
            description: "",
          },
        ],
        providerScopedModels: [],
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { createProvider, loadDashboard, setProviderFilter },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    expect(await screen.findByRole("button", { name: "Deselect Ollama" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await user.click(screen.getByRole("button", { name: "Deselect LM Studio" }));
    await user.click(screen.getByRole("button", { name: /Add selected/i }));

    expect(createProvider).toHaveBeenCalledTimes(1);
    expect(createProvider).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "Ollama",
        preset_id: "ollama",
      }),
      { refresh: false },
    );
    expect(loadDashboard).toHaveBeenCalledTimes(1);
    expect(setProviderFilter).toHaveBeenCalledWith("ollama");
  });

  it("shows one-click local provider onboarding from Hecate Chat", async () => {
    vi.mocked(discoverLocalProviders).mockResolvedValueOnce({
      object: "local_provider_discovery",
      data: [
        {
          preset_id: "ollama",
          name: "Ollama",
          base_url: "http://127.0.0.1:11434/v1",
          probe_url: "http://127.0.0.1:11434/api/tags",
          status: "running",
          command: "ollama",
          command_available: true,
          command_path: "/usr/local/bin/ollama",
          http_available: true,
          model_count: 1,
          models: ["qwen2.5-coder"],
        },
      ],
    });
    const createProvider = vi.fn(async () => undefined);
    const loadDashboard = vi.fn(async () => undefined);
    const setProviderFilter = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        providerFilter: "lmstudio",
        agentWorkspace: "/tmp/hecate",
        settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
        providerPresets: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            description: "",
          },
        ],
        providerScopedModels: [],
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { createProvider, loadDashboard, setProviderFilter },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(await screen.findByText("Detected locally")).toBeTruthy();
    expect(await screen.findByRole("button", { name: "Deselect Ollama" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /Add selected/i }));

    await waitFor(() => {
      expect(createProvider).toHaveBeenCalledWith(
        expect.objectContaining({
          name: "Ollama",
          preset_id: "ollama",
          base_url: "http://127.0.0.1:11434/v1",
        }),
        { refresh: false },
      );
    });
    await waitFor(() => expect(loadDashboard).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(setProviderFilter).toHaveBeenCalledWith("ollama"));
  });

  it("quick-add skips duplicate local provider endpoints", async () => {
    vi.mocked(discoverLocalProviders).mockResolvedValueOnce({
      object: "local_provider_discovery",
      data: [
        {
          preset_id: "llamacpp",
          name: "llama.cpp",
          base_url: "http://127.0.0.1:8080/v1",
          probe_url: "http://127.0.0.1:8080/v1/models",
          status: "running",
          command: "llama-server",
          command_available: true,
          command_path: "/usr/local/bin/llama-server",
          http_available: true,
          model_count: 1,
          models: ["local-model"],
        },
        {
          preset_id: "localai",
          name: "LocalAI",
          base_url: "http://127.0.0.1:8080/v1",
          probe_url: "http://127.0.0.1:8080/v1/models",
          status: "running",
          command: "local-ai",
          command_available: true,
          command_path: "/usr/local/bin/local-ai",
          http_available: true,
          model_count: 1,
          models: ["local-model"],
        },
      ],
    });
    const createProvider = vi.fn(async () => undefined);
    const loadDashboard = vi.fn(async () => undefined);
    const setProviderFilter = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
        providerPresets: [
          {
            id: "llamacpp",
            name: "llama.cpp",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:8080/v1",
            description: "",
          },
          {
            id: "localai",
            name: "LocalAI",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:8080/v1",
            description: "",
          },
        ],
        providerScopedModels: [],
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { createProvider, loadDashboard, setProviderFilter },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Add selected/i }));

    await waitFor(() => expect(createProvider).toHaveBeenCalledTimes(1));
    await waitFor(() => {
      expect(createProvider).toHaveBeenCalledWith(
        expect.objectContaining({
          name: "llama.cpp",
          preset_id: "llamacpp",
          base_url: "http://127.0.0.1:8080/v1",
        }),
        { refresh: false },
      );
    });
    await waitFor(() => expect(loadDashboard).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(setProviderFilter).toHaveBeenCalledWith("llamacpp"));
  });

  it("quick-add refreshes dashboard after partial provider creation failures", async () => {
    vi.mocked(discoverLocalProviders).mockResolvedValueOnce({
      object: "local_provider_discovery",
      data: [
        {
          preset_id: "ollama",
          name: "Ollama",
          base_url: "http://127.0.0.1:11434/v1",
          probe_url: "http://127.0.0.1:11434/api/tags",
          status: "running",
          command: "ollama",
          command_available: true,
          command_path: "/usr/local/bin/ollama",
          http_available: true,
          model_count: 1,
          models: ["llama3.1:8b"],
        },
        {
          preset_id: "lmstudio",
          name: "LM Studio",
          base_url: "http://127.0.0.1:1234/v1",
          probe_url: "http://127.0.0.1:1234/v1/models",
          status: "running",
          command: "lms",
          command_available: true,
          command_path: "/Users/alice/.lmstudio/bin/lms",
          http_available: true,
          model_count: 1,
          models: ["qwen2.5"],
        },
      ],
    });
    const createProvider = vi.fn(async (params: unknown) => {
      if ((params as { preset_id?: string }).preset_id === "lmstudio") {
        throw new Error("LM Studio endpoint already exists");
      }
    });
    const loadDashboard = vi.fn(async () => undefined);
    const setProviderFilter = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
        providerPresets: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            description: "",
          },
          {
            id: "lmstudio",
            name: "LM Studio",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:1234/v1",
            description: "",
          },
        ],
        providerScopedModels: [],
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { createProvider, loadDashboard, setProviderFilter },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Add selected/i }));

    await waitFor(() => expect(createProvider).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(loadDashboard).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(setProviderFilter).toHaveBeenCalledWith("ollama"));
    expect(screen.getByText("LM Studio endpoint already exists")).toBeTruthy();
  });

  it("shows a first-run setup state when providers and agents are unavailable", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      message: "hello",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: false,
          status: "missing",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText("Nothing runnable yet")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Open Connections/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: "New Hecate chat" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Choose agent for new chat" })).toBeTruthy();
    const textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    expect(textarea.disabled).toBe(false);
    const send = screen.getByRole("button", { name: "Send message" }) as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });

  it("uses hosted-runtime setup copy instead of local provider discovery in remote mode", async () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
      providerScopedModels: [],
      agentAdapters: [],
      sessionInfo: {
        role: "operator",
        remote_identity: {
          actor_id: "actor_1",
          org_id: "org_1",
          project_id: "proj_1",
          runtime_id: "rt_1",
        },
      },
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(await screen.findByText("Hosted runtime")).toBeTruthy();
    expect(screen.getByText(/Add an API-key provider or agent credential/i)).toBeTruthy();
    expect(screen.queryByText("Detected locally")).toBeNull();
    expect(screen.queryByText(/request was blocked/i)).toBeNull();
    expect(discoverLocalProviders).not.toHaveBeenCalled();
  });

  it("enables Hecate Chat tools when tools are not explicitly disabled for the model", async () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      message: "inspect this repo",
      agentWorkspace: "/tmp/hecate",
      settingsConfig: {
        backend: "memory",
        providers: [{ id: "ollama", name: "Ollama", kind: "local", credential_configured: true }],
        policy_rules: [],
        events: [],
      },
      providerFilter: "ollama",
      model: "qwen2.5-coder",
      providerScopedModels: [
        {
          id: "qwen2.5-coder",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
          },
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /settings/i }));
    expect(screen.getByRole("button", { name: "Tools on" })).toBeTruthy();
    expect(
      screen.getByText(/task runtime, approvals, artifacts, and sandboxed tool calls/),
    ).toBeTruthy();
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(false);
  });

  it("keeps provider and model pickers editable after a task-backed Hecate Chat segment completes", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      message: "continue",
      agentWorkspace: "/tmp/hecate",
      providerFilter: "ollama",
      model: "smollm2:135m",
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: true,
          },
          {
            id: "openai",
            name: "OpenAI",
            kind: "cloud",
            protocol: "openai",
            base_url: "https://api.openai.com/v1",
            credential_configured: true,
          },
        ],
        policy_rules: [],
        events: [],
      },
      providerPresets: [
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
          description: "",
        },
        {
          id: "openai",
          name: "OpenAI",
          kind: "cloud",
          protocol: "openai",
          base_url: "https://api.openai.com/v1",
          description: "",
        },
      ],
      providerScopedModels: [
        {
          id: "qwen2.5-coder",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
          },
        },
        {
          id: "smollm2:135m",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "unknown", streaming: true, source: "provider" },
          },
        },
      ],
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByRole("button", { name: "Fixed provider: Ollama" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Fixed model: qwen2.5-coder" })).toBeNull();
    expect(screen.getByLabelText("Hecate message controls")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Model picker: smollm2:135m" })).toBeTruthy();
  });

  it("uses shared composer dropdown controls for editable Hecate provider and model selection", async () => {
    const setProviderFilter = vi.fn();
    const setModel = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        message: "continue",
        agentWorkspace: "/tmp/hecate",
        providerFilter: "ollama",
        model: "smollm2:135m",
        settingsConfig: {
          backend: "memory",
          providers: [
            {
              id: "ollama",
              name: "Ollama",
              kind: "local",
              protocol: "openai",
              base_url: "http://127.0.0.1:11434/v1",
              credential_configured: true,
            },
            {
              id: "openai",
              name: "OpenAI",
              kind: "cloud",
              protocol: "openai",
              base_url: "https://api.openai.com/v1",
              credential_configured: true,
            },
          ],
          policy_rules: [],
          events: [],
        },
        providerPresets: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            description: "",
          },
          {
            id: "openai",
            name: "OpenAI",
            kind: "cloud",
            protocol: "openai",
            base_url: "https://api.openai.com/v1",
            description: "",
          },
        ],
        providerScopedModels: [
          {
            id: "qwen2.5-coder",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
            },
          },
          {
            id: "smollm2:135m",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              capabilities: { tool_calling: "unknown", streaming: true, source: "provider" },
            },
          },
        ],
        activeChatSessionID: "chat_1",
        activeChatSession: {
          id: "chat_1",
          execution_mode: "hecate_task",
          title: "Repo work",
          task_id: "task_hecate_123456",
          latest_run_id: "run_hecate_abcdef",
          provider: "ollama",
          model: "qwen2.5-coder",
          capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { setChatTarget: vi.fn(), setProviderFilter, setModel },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const controls = screen.getByLabelText("Hecate message controls");
    const provider = within(controls).getByRole("button", { name: "Provider picker: Ollama" });
    expect(provider).toHaveTextContent("provider");
    expect(provider).toHaveTextContent("Ollama");
    await userEvent.click(provider);
    expect(screen.queryByRole("option", { name: /All providers/ })).toBeNull();
    await userEvent.click(screen.getByRole("option", { name: /OpenAI/ }));
    expect(setProviderFilter).toHaveBeenCalledWith("openai");

    const model = within(controls).getByRole("button", { name: "Model picker: smollm2:135m" });
    expect(model).toHaveTextContent("model");
    expect(model).toHaveTextContent("smollm2:135m");
    await userEvent.click(model);
    const filter = screen.getByRole("textbox", { name: "Filter models..." });
    await userEvent.type(filter, "qwen");
    expect(screen.getByRole("option", { name: /qwen2.5-coder/ })).toBeTruthy();
    expect(screen.queryByRole("option", { name: /smollm2:135m/ })).toBeNull();
    await userEvent.click(screen.getByRole("option", { name: /qwen2.5-coder/ }));
    expect(setModel).toHaveBeenCalledWith("qwen2.5-coder");
    expect(actions.setChatTarget).not.toHaveBeenCalled();
  });

  it("keeps tools off when model selection changes to a tool-capable model", async () => {
    const setChatTarget = vi.fn();
    const setModel = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "continue",
        providerFilter: "ollama",
        model: "smollm2:135m",
        settingsConfig: {
          backend: "memory",
          providers: [
            {
              id: "ollama",
              name: "Ollama",
              kind: "local",
              protocol: "openai",
              base_url: "http://127.0.0.1:11434/v1",
              credential_configured: true,
            },
          ],
          policy_rules: [],
          events: [],
        },
        providerPresets: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            description: "",
          },
        ],
        providerScopedModels: [
          {
            id: "qwen2.5-coder",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
            },
          },
          {
            id: "smollm2:135m",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              capabilities: { tool_calling: "none", streaming: true, source: "provider" },
            },
          },
        ],
      },
      { setChatTarget, setModel },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const controls = screen.getByLabelText("Hecate message controls");
    await userEvent.click(
      within(controls).getByRole("button", { name: "Model picker: smollm2:135m" }),
    );
    await userEvent.click(screen.getByRole("option", { name: /qwen2.5-coder/ }));

    expect(setModel).toHaveBeenCalledWith("qwen2.5-coder");
    expect(setChatTarget).not.toHaveBeenCalledWith("agent");
  });

  it("keeps the catalog provider label while the Hecate composer is busy", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      chatLoading: true,
      agentWorkspace: "/tmp/hecate",
      providerFilter: "local-ollama",
      model: "qwen2.5-coder",
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "local-ollama",
            name: "ollama",
            preset_id: "ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: true,
          },
        ],
        policy_rules: [],
        events: [],
      },
      providerPresets: [
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
          description: "",
        },
      ],
      providerScopedModels: [
        {
          id: "qwen2.5-coder",
          owned_by: "local-ollama",
          metadata: {
            provider: "local-ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
          },
        },
      ],
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        provider: "local-ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const controls = screen.getByLabelText("Hecate message controls");
    const provider = within(controls).getByRole("button", { name: "Provider picker: Ollama" });
    expect(provider.textContent).toContain("Ollama");
    expect(provider.textContent).not.toContain("ollama");
  });

  it("locks provider and model while a task-backed Hecate Chat segment is active", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      message: "continue",
      agentWorkspace: "/tmp/hecate",
      providerFilter: "ollama",
      model: "smollm2:135m",
      settingsConfig: {
        backend: "memory",
        providers: [{ id: "ollama", name: "Ollama", kind: "local", credential_configured: true }],
        policy_rules: [],
        events: [],
      },
      providerPresets: [
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
          description: "",
        },
      ],
      providerScopedModels: [
        {
          id: "smollm2:135m",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "unknown", streaming: true, source: "provider" },
          },
        },
      ],
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
        workspace: "/tmp/hecate",
        status: "running",
        messages: [],
      } as any,
    });
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));

    const fixedProvider = screen.getByRole("button", {
      name: "Fixed provider: Ollama",
    }) as HTMLButtonElement;
    const fixedModel = screen.getByRole("button", {
      name: "Fixed model: qwen2.5-coder",
    }) as HTMLButtonElement;
    expect(fixedProvider.disabled).toBe(true);
    expect(fixedModel.disabled).toBe(true);
    expect(screen.queryByText("smollm2:135m")).toBeNull();
    expect(screen.queryByText(/Tools are unavailable for this model/)).toBeNull();
    expect(screen.getByRole("button", { name: "Queue message" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Stop active task" })).toBeTruthy();
    const activeRunStatus = screen.getByLabelText("Active run status");
    expect(activeRunStatus).toHaveTextContent(/Hecate Chat is working/);
    expectBefore(screen.getByLabelText("Message"), activeRunStatus);

    rerender(
      withRuntimeConsole(<ChatView />, {
        state: { ...state, defaultChatToolsEnabled: false },
        actions,
      }),
    );
    expect(document.querySelector('[aria-label="Fixed provider: Ollama"]')).toBeTruthy();
    expect(document.querySelector('[aria-label="Fixed model: qwen2.5-coder"]')).toBeTruthy();
    expect(document.querySelector('[aria-label="Model picker: smollm2:135m"]')).toBeNull();
    expect(screen.getByRole("button", { name: "Stop active task" })).toBeTruthy();
    expect(screen.getByText(/Hecate Chat is working/)).toBeTruthy();
  });

  it("locks controls to the active task segment even when the session root is direct chat", () => {
    const onOpenTask = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      message: "continue",
      agentWorkspace: "/tmp/hecate",
      providerFilter: "ollama",
      model: "smollm2:135m",
      settingsConfig: {
        backend: "memory",
        providers: [{ id: "ollama", name: "Ollama", kind: "local", credential_configured: true }],
        policy_rules: [],
        events: [],
      },
      providerPresets: [
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
          description: "",
        },
      ],
      providerScopedModels: [
        {
          id: "smollm2:135m",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "unknown", streaming: true, source: "provider" },
          },
        },
      ],
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        tools_enabled: false,
        title: "Mixed chat",
        provider: "ollama",
        model: "smollm2:135m",
        workspace: "/tmp/hecate",
        status: "running",
        segments: [
          {
            id: "model:first",
            turn_kind: "direct_model",
            execution_mode: "hecate_task",
            tools_enabled: false,
            provider: "ollama",
            model: "smollm2:135m",
            status: "completed",
            message_count: 2,
          },
          {
            id: "task:task_hecate_123456",
            turn_kind: "hecate_task",
            execution_mode: "hecate_task",
            provider: "ollama",
            model: "qwen2.5-coder",
            task_id: "task_hecate_123456",
            latest_run_id: "run_hecate_abcdef",
            status: "running",
            message_count: 1,
          },
        ],
        messages: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView onOpenTask={onOpenTask} />, { state, actions }));

    expect(screen.getByRole("button", { name: "Fixed provider: Ollama" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Fixed model: qwen2.5-coder" })).toBeTruthy();
    expect(screen.queryByText("smollm2:135m")).toBeNull();
    expect(screen.getByRole("button", { name: "Stop active task" })).toBeTruthy();
    expect(screen.getByText(/New messages will queue/)).toBeTruthy();
    screen.getByRole("button", { name: "Open task" }).click();
    expect(onOpenTask).toHaveBeenCalledWith("task_hecate_123456", "run_hecate_abcdef");
  });

  it("renders queued messages with a remove action", async () => {
    const removeQueuedChatMessage = vi.fn();
    const updateQueuedChatMessage = vi.fn();
    const user = userEvent.setup();
    const { state, actions } = setup(
      {
        activeChatSessionID: "chat_1",
        queuedChatMessages: [
          {
            id: "queued_1",
            session_id: "chat_1",
            content: "run tests after this",
            execution_mode: "hecate_task",
            provider_filter: "ollama",
            model: "qwen2.5-coder",
            workspace: "/workspace",
            system_prompt: "",
            agent_id: "hecate",
            created_at: "2026-04-20T00:00:00Z",
          },
        ],
      },
      { removeQueuedChatMessage, updateQueuedChatMessage },
    );

    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByLabelText("Queued messages")).toBeTruthy();
    const queuedInput = screen.getByLabelText("Queued message 1");
    expect(queuedInput).toHaveValue("run tests after this");
    fireEvent.change(queuedInput, { target: { value: "run unit tests after this" } });
    expect(updateQueuedChatMessage).toHaveBeenLastCalledWith(
      "queued_1",
      "run unit tests after this",
    );
    await user.click(screen.getByRole("button", { name: "Remove queued message 1" }));
    expect(removeQueuedChatMessage).toHaveBeenCalledWith("queued_1");
  });

  it("only renders queued messages for the active agent chat", () => {
    const { state, actions } = setup({
      activeChatSessionID: "chat_active",
      queuedChatMessages: [
        {
          id: "queued_active",
          session_id: "chat_active",
          content: "send this here",
          execution_mode: "hecate_task",
          provider_filter: "ollama",
          model: "qwen2.5-coder",
          workspace: "/workspace",
          system_prompt: "",
          agent_id: "codex",
          created_at: "2026-04-20T00:00:00Z",
        },
        {
          id: "queued_other",
          session_id: "chat_other",
          content: "not in this chat",
          execution_mode: "hecate_task",
          provider_filter: "ollama",
          model: "qwen2.5-coder",
          workspace: "/workspace",
          system_prompt: "",
          agent_id: "codex",
          created_at: "2026-04-20T00:00:00Z",
        },
      ],
    });

    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByLabelText("Queued messages")).toBeTruthy();
    expect(screen.getByLabelText("Queued message 1")).toHaveValue("send this here");
    expect(screen.queryByDisplayValue("not in this chat")).toBeNull();
  });

  it("shows the tools sandbox reminder only when task-backed tools are available", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      providerScopedModels: [
        {
          id: "qwen2.5-coder",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
          },
        },
      ],
      model: "qwen2.5-coder",
    });
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText(/Tools use task approvals and per-call sandboxing/)).toBeTruthy();

    rerender(
      withRuntimeConsole(<ChatView />, {
        state: { ...state, defaultChatToolsEnabled: false },
        actions,
      }),
    );
    expect(screen.queryByText(/Tools use task approvals and per-call sandboxing/)).toBeNull();
  });

  it("keeps Hecate Chat sendable when tools are explicitly unavailable for the model", async () => {
    const submitChat = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        message: "inspect this repo",
        agentWorkspace: "/tmp/hecate",
        settingsConfig: {
          backend: "memory",
          providers: [{ id: "ollama", name: "Ollama", kind: "local", credential_configured: true }],
          policy_rules: [],
          events: [],
        },
        providerFilter: "ollama",
        model: "llama3.1:8b",
        providerScopedModels: [
          {
            id: "llama3.1:8b",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              capabilities: { tool_calling: "none", streaming: true, source: "provider" },
            },
          },
        ],
      },
      { submitChat },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByText(/Tools are unavailable for this model/)).toBeNull();
    expect(screen.queryByRole("button", { name: "Enable tools" })).toBeNull();
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(false);

    const user = userEvent.setup();
    await user.click(send);
    expect(submitChat).toHaveBeenCalled();
  });

  it("keeps Hecate Chat sendable when local model tool support is unknown", () => {
    const submitChat = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        message: "tell a joke",
        agentWorkspace: "/tmp/hecate",
        settingsConfig: {
          backend: "memory",
          providers: [{ id: "ollama", name: "Ollama", kind: "local", credential_configured: true }],
          policy_rules: [],
          events: [],
        },
        providerFilter: "ollama",
        model: "smollm2:135m",
        providerScopedModels: [
          {
            id: "smollm2:135m",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              capabilities: { tool_calling: "unknown", streaming: true, source: "provider" },
            },
          },
        ],
      },
      { submitChat },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByText(/Tools are unavailable for this model/)).toBeNull();
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(false);
  });

  it("opens the backing task from the Hecate Chat assistant turn, not the header", async () => {
    const onOpenTask = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          {
            id: "m1",
            turn_kind: "hecate_task",
            execution_mode: "hecate_task",
            segment_id: "task:task_hecate_123456",
            task_id: "task_hecate_123456",
            role: "user",
            content: "inspect this repo",
            created_at: "2026-05-03T10:00:00Z",
          },
          {
            id: "m2",
            turn_kind: "hecate_task",
            execution_mode: "hecate_task",
            segment_id: "task:task_hecate_123456",
            task_id: "task_hecate_123456",
            run_id: "run_hecate_abcdef",
            role: "assistant",
            content: "Done.",
            status: "completed",
            cost_mode: "hecate",
            created_at: "2026-05-03T10:00:01Z",
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView onOpenTask={onOpenTask} />, { state, actions }));
    const user = userEvent.setup();
    expect(screen.queryByRole("button", { name: /^Task task_hecate_/i })).toBeNull();
    expect(screen.getByText("Run hecate_abcde")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: /Open Task hecate_/i }));
    expect(onOpenTask).toHaveBeenCalledWith("task_hecate_123456", "run_hecate_abcdef");
  });

  it("does not borrow the session task link for direct model messages", () => {
    const onOpenTask = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        tools_enabled: false,
        title: "Mixed chat",
        task_id: "task_latest",
        latest_run_id: "run_latest",
        provider: "ollama",
        model: "ministral-3:latest",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          {
            id: "m1",
            execution_mode: "hecate_task",
            tools_enabled: false,
            segment_id: "model:direct",
            role: "user",
            content: "tell a joke",
            created_at: "2026-05-03T10:00:00Z",
          },
          {
            id: "m2",
            execution_mode: "hecate_task",
            tools_enabled: false,
            segment_id: "model:direct",
            run_id: "model_run_1",
            trace_id: "trace_1",
            role: "assistant",
            content: "Direct answer.",
            status: "completed",
            model: "ministral-3:latest",
            created_at: "2026-05-03T10:00:01Z",
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView onOpenTask={onOpenTask} />, { state, actions }));

    expect(screen.getByText("Direct answer.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Open Task latest/i })).toBeNull();
    expect(onOpenTask).not.toHaveBeenCalled();
  });

  it("renders explicit Hecate Chat segment dividers when tools switch", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Mixed chat",
        task_id: "task_second",
        latest_run_id: "run_second",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "completed",
        segments: [
          {
            id: "model:first",
            turn_kind: "direct_model",
            execution_mode: "hecate_task",
            tools_enabled: false,
            provider: "ollama",
            model: "smollm2:135m",
            status: "completed",
            message_count: 2,
          },
          {
            id: "task:task_first",
            turn_kind: "hecate_task",
            execution_mode: "hecate_task",
            provider: "ollama",
            model: "qwen2.5-coder",
            task_id: "task_first",
            latest_run_id: "run_first",
            status: "completed",
            message_count: 2,
          },
          {
            id: "model:second",
            turn_kind: "direct_model",
            execution_mode: "hecate_task",
            tools_enabled: false,
            provider: "ollama",
            model: "smollm2:135m",
            status: "completed",
            message_count: 2,
          },
        ],
        messages: [
          {
            id: "m1",
            turn_kind: "direct_model",
            execution_mode: "hecate_task",
            tools_enabled: false,
            segment_id: "model:first",
            role: "user",
            content: "answer directly",
            created_at: "2026-05-03T10:00:00Z",
          },
          {
            id: "m2",
            turn_kind: "direct_model",
            execution_mode: "hecate_task",
            tools_enabled: false,
            segment_id: "model:first",
            role: "assistant",
            content: "Direct answer.",
            status: "completed",
            model: "smollm2:135m",
            created_at: "2026-05-03T10:00:01Z",
          },
          {
            id: "m3",
            turn_kind: "hecate_task",
            execution_mode: "hecate_task",
            segment_id: "task:task_first",
            task_id: "task_first",
            role: "user",
            content: "use tools",
            created_at: "2026-05-03T10:01:00Z",
          },
          {
            id: "m4",
            turn_kind: "hecate_task",
            execution_mode: "hecate_task",
            segment_id: "task:task_first",
            task_id: "task_first",
            run_id: "run_first",
            role: "assistant",
            content: "Tool answer.",
            status: "completed",
            model: "qwen2.5-coder",
            created_at: "2026-05-03T10:01:01Z",
          },
          {
            id: "m5",
            turn_kind: "direct_model",
            execution_mode: "hecate_task",
            tools_enabled: false,
            segment_id: "model:second",
            role: "user",
            content: "back to direct",
            created_at: "2026-05-03T10:02:00Z",
          },
          {
            id: "m6",
            turn_kind: "direct_model",
            execution_mode: "hecate_task",
            tools_enabled: false,
            segment_id: "model:second",
            role: "assistant",
            content: "Direct again.",
            status: "completed",
            model: "smollm2:135m",
            created_at: "2026-05-03T10:02:01Z",
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getAllByLabelText("Tools off segment using smollm2:135m")).toHaveLength(2);
    expect(screen.getByLabelText("Tools on segment using qwen2.5-coder")).toBeTruthy();
    expect(screen.getByText("Task first")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Open Task second/i })).toBeNull();
    expect(screen.getAllByRole("button", { name: /Open Task first/i })).toHaveLength(1);
    expect(screen.getAllByText(/direct model chat/)).toHaveLength(2);
    expect(screen.getByLabelText("Tools on segment using qwen2.5-coder").children[1]).toHaveStyle({
      background: "var(--bg2)",
    });
  });

  it("renders projected Hecate Chat task run activity in the transcript", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          {
            id: "m1",
            role: "user",
            content: "inspect this repo",
            created_at: "2026-05-03T10:00:00Z",
          },
          {
            id: "m2",
            run_id: "run_hecate_abcdef",
            role: "assistant",
            content: "Done.",
            status: "completed",
            cost_mode: "hecate",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              {
                id: "legacy-task-run-running",
                type: "task_running",
                status: "running",
                title: "Task run running",
                detail: "run_hecate_abcdef",
              },
              {
                id: "hecate_task_run:run_hecate_abcdef",
                type: "task_run",
                status: "running",
                title: "Backing task",
                detail: "running · run_hecate_abcdef",
              },
              {
                id: "task:step:model",
                type: "thinking",
                status: "completed",
                kind: "model",
                title: "Agent turn 1",
                detail: "completed",
              },
              {
                id: "task:step:shell",
                type: "tool_call",
                status: "completed",
                kind: "shell",
                title: "shell_exec",
                detail: "completed",
              },
              {
                id: "task:run:terminal",
                type: "run_result",
                status: "completed",
                title: "Run completed",
              },
              { type: "completed", status: "completed", title: "Final answer" },
            ],
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("completed · 1 tool")).toBeTruthy();
    expect(screen.getByText("Thinking")).toBeTruthy();
    expect(screen.getByText("Ran shell")).toBeTruthy();
    expect(screen.getByText("Backing task")).toBeTruthy();
    expect(screen.queryByText("Agent turn 1")).toBeNull();
    expect(screen.getByText("shell_exec")).toBeTruthy();
    expect(screen.queryByText("Task run running")).toBeNull();
    expect(screen.queryByText("Run completed")).toBeNull();
  });

  it("resolves projected Hecate Chat task approvals from the chat banner", async () => {
    const resolveTaskApproval = vi.fn(async () => true);
    const onOpenTask = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        activeChatSessionID: "chat_1",
        activeChatSession: {
          id: "chat_1",
          execution_mode: "hecate_task",
          title: "Repo work",
          task_id: "task_hecate_123456",
          latest_run_id: "run_hecate_abcdef",
          provider: "ollama",
          model: "qwen2.5-coder",
          workspace: "/tmp/hecate",
          status: "awaiting_approval",
          messages: [
            {
              id: "m1",
              role: "user",
              content: "echo lol please",
              created_at: "2026-05-03T10:00:00Z",
            },
            {
              id: "m2",
              run_id: "run_hecate_abcdef",
              role: "assistant",
              content: "",
              status: "awaiting_approval",
              cost_mode: "hecate",
              created_at: "2026-05-03T10:00:01Z",
              activities: [
                {
                  id: "task:step:step_approval",
                  type: "approval",
                  status: "awaiting_approval",
                  kind: "approval",
                  title: "Awaiting approval — turn 1",
                  detail:
                    "Agent requested tools that require approval: shell_exec - awaiting_approval",
                  approval_id: "appr_123",
                  needs_action: true,
                  created_at: "2026-05-03T10:00:02Z",
                },
              ],
            },
          ],
        } as any,
      },
      { resolveTaskApproval },
    );
    render(withRuntimeConsole(<ChatView onOpenTask={onOpenTask} />, { state, actions }));

    expect(screen.getByTestId("hecate-task-approval-banner")).toBeTruthy();
    expect(screen.getByText("Approval required")).toBeTruthy();
    expect(screen.getByText("Shell execution")).toBeTruthy();
    expect(screen.getAllByText("Waiting for approval").length).toBeGreaterThan(0);

    const user = userEvent.setup();
    await user.click(screen.getAllByRole("button", { name: "Open task" })[0]!);
    expect(onOpenTask).toHaveBeenCalledWith("task_hecate_123456", "run_hecate_abcdef");

    await user.click(screen.getByRole("button", { name: /Approve Shell execution/i }));
    expect(resolveTaskApproval).toHaveBeenCalledWith("task_hecate_123456", "appr_123", {
      decision: "approve",
    });
  });

  it("does not keep stale resolved Hecate Chat task approvals actionable", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [
          {
            id: "m2",
            run_id: "run_hecate_abcdef",
            role: "assistant",
            content: "",
            status: "running",
            cost_mode: "hecate",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              {
                id: "task:step:step_approval",
                type: "approval",
                status: "approved",
                kind: "agent_loop_tool_call",
                title: "Awaiting approval — turn 1",
                detail: "Agent requested tools that require approval: shell_exec - approved",
                approval_id: "appr_123",
                needs_action: true,
              },
            ],
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByTestId("hecate-task-approval-banner")).toBeNull();
    expect(screen.queryByRole("button", { name: /Approve Agent tool call/i })).toBeNull();
  });

  it("calls setMessage as user types", async () => {
    const setMessage = vi.fn();
    // Start with empty message so the assertion sees only what we typed.
    const { state, actions } = setup(
      { chatTarget: "agent", defaultChatToolsEnabled: false, message: "" },
      { setMessage },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    const ta = screen.getByPlaceholderText(/Message/i) as HTMLTextAreaElement;
    const user = userEvent.setup();
    await user.type(ta, "h");
    expect(setMessage).toHaveBeenCalledWith("h");
  });

  it("suggests external-agent slash commands and inserts the selected command", async () => {
    const setMessage = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentAdapterID: "claude_code",
        message: "/",
        activeChatSession: {
          id: "chat_commands",
          title: "Agent commands",
          agent_id: "claude_code",
          driver_kind: "acp",
          execution_mode: "external_agent",
          status: "idle",
          workspace: "/tmp/hecate",
          available_commands: [
            { name: "web", description: "Search the web", input_hint: "query" },
            { name: "plan", description: "Create a plan" },
          ],
          messages: [],
        },
      },
      { setMessage },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const textarea = screen.getByRole("textbox", { name: "Message" });
    const picker = screen.getByRole("combobox", { name: "Message command picker" });
    const commands = screen.getByRole("listbox", { name: "Message commands" });
    expect(picker).toHaveAttribute("aria-expanded", "true");
    expect(textarea).toHaveAttribute("aria-controls", commands.id);
    expect(picker).toHaveAttribute("aria-controls", commands.id);
    expect(within(commands).getByRole("option", { name: "Insert /web command" })).toHaveTextContent(
      "/web",
    );
    expect(within(commands).getByRole("option", { name: "Insert /web command" })).toHaveTextContent(
      "External Agent",
    );
    expect(within(commands).getByRole("option", { selected: true })).toHaveAttribute(
      "aria-label",
      "Insert /web command",
    );
    expect(textarea).toHaveAttribute(
      "aria-activedescendant",
      within(commands).getByRole("option", { selected: true }).id,
    );
    expect(within(commands).getByText("Search the web")).toBeTruthy();

    const user = userEvent.setup();
    await user.click(within(commands).getByRole("option", { name: "Insert /web command" }));

    expect(setMessage).toHaveBeenLastCalledWith("/web ");
  });

  it("filters external-agent slash commands by typed prefix", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      message: "/w",
      activeChatSession: {
        id: "chat_commands",
        title: "Agent commands",
        agent_id: "claude_code",
        driver_kind: "acp",
        execution_mode: "external_agent",
        status: "idle",
        workspace: "/tmp/hecate",
        available_commands: [
          { name: "web", description: "Search the web" },
          { name: "plan", description: "Create a plan" },
        ],
        messages: [],
      },
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const commands = screen.getByRole("listbox", { name: "Message commands" });
    expect(within(commands).getByRole("option", { name: "Insert /web command" })).toBeTruthy();
    expect(within(commands).queryByRole("option", { name: "Insert /plan command" })).toBeNull();
  });

  it("renders every matching external-agent slash command in a scrollable picker", () => {
    const availableCommands = Array.from({ length: 9 }, (_, index) => ({
      name: `cmd${index + 1}`,
      description: `Command ${index + 1}`,
    }));
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      message: "/",
      activeChatSession: {
        id: "chat_commands",
        title: "Agent commands",
        agent_id: "claude_code",
        driver_kind: "acp",
        execution_mode: "external_agent",
        status: "idle",
        workspace: "/tmp/hecate",
        available_commands: availableCommands,
        messages: [],
      },
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const commands = screen.getByRole("listbox", { name: "Message commands" });
    expect(within(commands).getAllByRole("option")).toHaveLength(availableCommands.length);
    expect(within(commands).getByRole("option", { name: "Insert /cmd9 command" })).toBeTruthy();
    expect(commands).toHaveStyle({ overflowY: "auto" });
    expect(commands.getAttribute("style")).toContain("max-height:");
    expect(commands.getAttribute("style")).toContain("scrollbar-gutter: stable");
  });

  it("keeps slash command wheel scrolling inside the picker", () => {
    const availableCommands = Array.from({ length: 9 }, (_, index) => ({
      name: `cmd${index + 1}`,
      description: `Command ${index + 1}`,
    }));
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      message: "/",
      activeChatSession: {
        id: "chat_commands",
        title: "Agent commands",
        agent_id: "claude_code",
        driver_kind: "acp",
        execution_mode: "external_agent",
        status: "idle",
        workspace: "/tmp/hecate",
        available_commands: availableCommands,
        messages: [],
      },
    });
    const bubbled = vi.fn();
    document.body.addEventListener("wheel", bubbled);
    try {
      render(withRuntimeConsole(<ChatView />, { state, actions }));

      fireEvent.wheel(screen.getByRole("listbox", { name: "Message commands" }));

      expect(bubbled).not.toHaveBeenCalled();
    } finally {
      document.body.removeEventListener("wheel", bubbled);
    }
  });

  it("scrolls the active slash command into view during keyboard navigation", () => {
    const originalScrollIntoViewDescriptor = Object.getOwnPropertyDescriptor(
      HTMLElement.prototype,
      "scrollIntoView",
    );
    const scrollIntoView = vi.fn();
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
      configurable: true,
      value: scrollIntoView,
    });
    try {
      const availableCommands = Array.from({ length: 9 }, (_, index) => ({
        name: `cmd${index + 1}`,
        description: `Command ${index + 1}`,
      }));
      const { state, actions } = setup({
        chatTarget: "external_agent",
        agentAdapterID: "claude_code",
        message: "/",
        activeChatSession: {
          id: "chat_commands",
          title: "Agent commands",
          agent_id: "claude_code",
          driver_kind: "acp",
          execution_mode: "external_agent",
          status: "idle",
          workspace: "/tmp/hecate",
          available_commands: availableCommands,
          messages: [],
        },
      });
      render(withRuntimeConsole(<ChatView />, { state, actions }));

      const textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
      const commands = screen.getByRole("listbox", { name: "Message commands" });
      scrollIntoView.mockClear();

      fireEvent.keyDown(textarea, { key: "ArrowDown" });

      expect(within(commands).getByRole("option", { selected: true })).toHaveAttribute(
        "aria-label",
        "Insert /cmd2 command",
      );
      expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest" });
    } finally {
      if (originalScrollIntoViewDescriptor) {
        Object.defineProperty(
          HTMLElement.prototype,
          "scrollIntoView",
          originalScrollIntoViewDescriptor,
        );
      } else {
        delete (HTMLElement.prototype as unknown as Record<string, unknown>).scrollIntoView;
      }
    }
  });

  it("dismisses external-agent slash command suggestions with Escape", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      message: "/",
      activeChatSession: {
        id: "chat_commands",
        title: "Agent commands",
        agent_id: "claude_code",
        driver_kind: "acp",
        execution_mode: "external_agent",
        status: "idle",
        workspace: "/tmp/hecate",
        available_commands: [
          { name: "web", description: "Search the web" },
          { name: "plan", description: "Create a plan" },
        ],
        messages: [],
      },
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const textarea = screen.getByRole("textbox", { name: "Message" });
    const picker = screen.getByRole("combobox", { name: "Message command picker" });
    expect(screen.getByRole("listbox", { name: "Message commands" })).toBeTruthy();

    fireEvent.keyDown(textarea, { key: "Escape" });

    expect(screen.queryByRole("listbox", { name: "Message commands" })).toBeNull();
    expect(picker).toHaveAttribute("aria-expanded", "false");
  });

  it("selects external-agent slash commands with Enter without sending", () => {
    const setMessage = vi.fn();
    const submitChat = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentAdapterID: "claude_code",
        message: "/",
        activeChatSession: {
          id: "chat_commands",
          title: "Agent commands",
          agent_id: "claude_code",
          driver_kind: "acp",
          execution_mode: "external_agent",
          status: "idle",
          workspace: "/tmp/hecate",
          available_commands: [
            { name: "web", description: "Search the web" },
            { name: "plan", description: "Create a plan" },
          ],
          messages: [],
        },
      },
      { setMessage, submitChat },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    fireEvent.keyDown(textarea, { key: "ArrowDown" });
    const commands = screen.getByRole("listbox", { name: "Message commands" });
    expect(within(commands).getByRole("option", { selected: true })).toHaveAttribute(
      "aria-label",
      "Insert /plan command",
    );
    expect(textarea).toHaveAttribute(
      "aria-activedescendant",
      within(commands).getByRole("option", { selected: true }).id,
    );
    fireEvent.keyDown(textarea, { key: "Enter" });

    expect(setMessage).toHaveBeenLastCalledWith("/plan ");
    expect(submitChat).not.toHaveBeenCalled();
  });

  it("suggests Hecate utility slash commands for Hecate Chat", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      message: "/",
      activeChatSession: {
        id: "chat_hecate",
        title: "Hecate",
        agent_id: "hecate",
        execution_mode: "hecate_task",
        status: "idle",
        workspace: "/tmp/hecate",
        available_commands: [{ name: "plan", description: "Create a plan" }],
        messages: [],
      },
    });
    render(withRuntimeConsole(<ChatView onNavigate={() => undefined} />, { state, actions }));

    const commands = screen.getByRole("listbox", { name: "Message commands" });
    expect(
      within(commands).getByRole("option", { name: "Insert /diff command" }),
    ).toHaveTextContent("Hecate");
    expect(within(commands).getByRole("option", { name: "Insert /model command" })).toBeTruthy();
    expect(within(commands).getByRole("option", { name: "Insert /settings command" })).toBeTruthy();
    expect(within(commands).getByRole("option", { name: "Insert /status command" })).toBeTruthy();
    expect(within(commands).getByRole("option", { name: "Insert /context command" })).toBeTruthy();
    expect(within(commands).getByRole("option", { name: "Insert /compact command" })).toBeTruthy();
    expect(within(commands).getByRole("option", { name: "Insert /task command" })).toBeTruthy();
    expect(
      within(commands).getByRole("option", { name: "Insert /connections command" }),
    ).toBeTruthy();
    expect(within(commands).queryByRole("option", { name: "Insert /proposal command" })).toBeNull();
  });

  it("suggests the project proposal slash command for project-linked Hecate chats", async () => {
    const setMessage = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "/",
        activeChatSession: {
          id: "chat_project_commands",
          title: "Project chat",
          agent_id: "hecate",
          execution_mode: "hecate_task",
          project_id: "proj_1",
          status: "idle",
          workspace: "/tmp/hecate",
          messages: [],
        },
      },
      { setMessage },
    );
    render(withRuntimeConsole(<ChatView onNavigate={() => undefined} />, { state, actions }));

    const commands = screen.getByRole("listbox", { name: "Message commands" });
    expect(
      within(commands).getByRole("option", { name: "Insert /proposal command" }),
    ).toHaveTextContent("/proposal");
    expect(
      within(commands).getByRole("option", { name: "Insert /proposal command" }),
    ).toHaveTextContent("Project");
    expect(within(commands).getByText("Draft a Project Assistant proposal")).toBeTruthy();

    const user = userEvent.setup();
    await user.click(within(commands).getByRole("option", { name: "Insert /proposal command" }));

    expect(setMessage).toHaveBeenLastCalledWith("/proposal ");
  });

  it("does not add Hecate project commands to External Agent command hints", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      message: "/",
      activeChatSession: {
        id: "external_project_commands",
        title: "External project chat",
        agent_id: "claude_code",
        driver_kind: "acp",
        execution_mode: "external_agent",
        project_id: "proj_1",
        status: "idle",
        workspace: "/tmp/hecate",
        available_commands: [{ name: "web", description: "Search the web" }],
        messages: [],
      },
    });
    render(withRuntimeConsole(<ChatView onNavigate={() => undefined} />, { state, actions }));

    const commands = screen.getByRole("listbox", { name: "Message commands" });
    expect(within(commands).getByRole("option", { name: "Insert /web command" })).toBeTruthy();
    expect(within(commands).queryByRole("option", { name: "Insert /proposal command" })).toBeNull();
  });

  it("drafts a Project Assistant proposal with /proposal without sending chat", async () => {
    const selectProject = vi.fn(async () => undefined);
    const setMessage = vi.fn();
    const submitChat = vi.fn(async () => undefined);
    const onNavigate = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "/proposal Plan next project work",
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          agent_id: "hecate",
          title: "Project chat",
          execution_mode: "hecate_task",
          project_id: "proj_1",
          provider: "openai",
          model: "gpt-4o-mini",
          status: "idle",
          workspace: "/tmp/hecate",
          messages: [],
        },
      },
      { selectProject, setMessage, submitChat },
    );
    render(withRuntimeConsole(<ChatView onNavigate={onNavigate} />, { state, actions }));

    fireEvent.submit(screen.getByRole("textbox", { name: "Message" }).closest("form")!);

    await waitFor(() => {
      expect(draftChatProjectAssistant).toHaveBeenCalledWith("s1", {
        request: "Plan next project work",
      });
    });
    expect(submitChat).not.toHaveBeenCalled();
    expect(selectProject).toHaveBeenCalledWith("proj_1");
    expect(onNavigate).toHaveBeenCalledWith("projects");
    expect(setMessage).toHaveBeenCalledWith("");
    expect(readProjectAssistantChatHandoff()).toMatchObject({
      project_id: "proj_1",
      request: "Plan next project work",
      source_session_id: "s1",
      proposal: { id: "pa_chat" },
    });
  });

  it("drafts a Project Assistant proposal with /plan without sending chat", async () => {
    const selectProject = vi.fn(async () => undefined);
    const submitChat = vi.fn(async () => undefined);
    const onNavigate = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "/plan Split the project assistant work",
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          agent_id: "hecate",
          title: "Project chat",
          execution_mode: "hecate_task",
          project_id: "proj_1",
          provider: "openai",
          model: "gpt-4o-mini",
          status: "idle",
          workspace: "/tmp/hecate",
          messages: [],
        },
      },
      { selectProject, submitChat },
    );
    render(withRuntimeConsole(<ChatView onNavigate={onNavigate} />, { state, actions }));

    fireEvent.submit(screen.getByRole("textbox", { name: "Message" }).closest("form")!);

    await waitFor(() => {
      expect(draftChatProjectAssistant).toHaveBeenCalledWith("s1", {
        request: "Split the project assistant work",
      });
    });
    expect(submitChat).not.toHaveBeenCalled();
    expect(selectProject).toHaveBeenCalledWith("proj_1");
    expect(onNavigate).toHaveBeenCalledWith("projects");
  });

  it("routes /work through the Project Assistant proposal boundary", async () => {
    const submitChat = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "/work Add a nightly maintenance check",
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          agent_id: "hecate",
          title: "Project chat",
          execution_mode: "hecate_task",
          project_id: "proj_1",
          provider: "openai",
          model: "gpt-4o-mini",
          status: "idle",
          workspace: "/tmp/hecate",
          messages: [],
        },
      },
      { submitChat },
    );
    render(withRuntimeConsole(<ChatView onNavigate={() => undefined} />, { state, actions }));

    fireEvent.submit(screen.getByRole("textbox", { name: "Message" }).closest("form")!);

    await waitFor(() => {
      expect(draftChatProjectAssistant).toHaveBeenCalledWith("s1", {
        request: "Create project work from this chat request:\n\nAdd a nightly maintenance check",
      });
    });
    expect(submitChat).not.toHaveBeenCalled();
  });

  it("opens workspace changes with /diff without sending chat", () => {
    const setMessage = vi.fn();
    const submitChat = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "/diff",
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          agent_id: "hecate",
          title: "Project chat",
          execution_mode: "hecate_task",
          provider: "openai",
          model: "gpt-4o-mini",
          status: "idle",
          workspace: "/tmp/hecate",
          messages: [],
        },
      },
      { setMessage, submitChat },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    fireEvent.submit(screen.getByRole("textbox", { name: "Message" }).closest("form")!);

    expect(screen.getByLabelText("Workspace changes panel")).toBeTruthy();
    expect(setMessage).toHaveBeenCalledWith("");
    expect(submitChat).not.toHaveBeenCalled();
  });

  it("opens chat settings with /model without sending chat", () => {
    const setMessage = vi.fn();
    const submitChat = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "/model",
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          agent_id: "hecate",
          title: "Project chat",
          execution_mode: "hecate_task",
          provider: "openai",
          model: "gpt-4o-mini",
          status: "idle",
          workspace: "/tmp/hecate",
          messages: [],
        },
      },
      { setMessage, submitChat },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    fireEvent.submit(screen.getByRole("textbox", { name: "Message" }).closest("form")!);

    expect(screen.getByLabelText("Chat settings panel")).toBeTruthy();
    expect(setMessage).toHaveBeenCalledWith("");
    expect(submitChat).not.toHaveBeenCalled();
  });

  it("opens chat context with /context without sending chat", () => {
    const setMessage = vi.fn();
    const submitChat = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "/context",
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          agent_id: "hecate",
          title: "Project chat",
          execution_mode: "hecate_task",
          provider: "openai",
          model: "gpt-4o-mini",
          status: "idle",
          workspace: "/tmp/hecate",
          context_summary: {
            message_count: 12,
            through_message_id: "msg_12",
            strategy: "semantic_transcript_summary",
            content: "- User: old request",
          },
          messages: [],
        },
      },
      { setMessage, submitChat },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    fireEvent.submit(screen.getByRole("textbox", { name: "Message" }).closest("form")!);

    expect(screen.getByLabelText("Chat settings panel")).toBeTruthy();
    expect(screen.getByText("Compacted")).toBeTruthy();
    expect(screen.getByText("12 messages (semantic)")).toBeTruthy();
    expect(setMessage).toHaveBeenCalledWith("");
    expect(submitChat).not.toHaveBeenCalled();
  });

  it("compacts chat context with /compact without sending chat", async () => {
    const compactChatSession = vi.fn(async () => true);
    const setMessage = vi.fn();
    const submitChat = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "/compact",
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          agent_id: "hecate",
          title: "Project chat",
          execution_mode: "hecate_task",
          provider: "openai",
          model: "gpt-4o-mini",
          status: "idle",
          workspace: "/tmp/hecate",
          messages: [],
        },
      },
      { compactChatSession, setMessage, submitChat },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    fireEvent.submit(screen.getByRole("textbox", { name: "Message" }).closest("form")!);

    await waitFor(() => expect(compactChatSession).toHaveBeenCalledWith("s1"));
    expect(setMessage).toHaveBeenCalledWith("");
    expect(submitChat).not.toHaveBeenCalled();
  });

  it("opens the active Hecate task with /task", () => {
    const onOpenTask = vi.fn();
    const setMessage = vi.fn();
    const submitChat = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: true,
        message: "/task",
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          agent_id: "hecate",
          title: "Project chat",
          execution_mode: "hecate_task",
          provider: "openai",
          model: "gpt-4o-mini",
          status: "running",
          workspace: "/tmp/hecate",
          segments: [
            {
              id: "task:task_1",
              turn_kind: "hecate_task",
              execution_mode: "hecate_task",
              task_id: "task_1",
              latest_run_id: "run_1",
              status: "running",
              message_count: 1,
            },
          ],
          messages: [],
        },
      },
      { setMessage, submitChat },
    );
    render(withRuntimeConsole(<ChatView onOpenTask={onOpenTask} />, { state, actions }));

    fireEvent.submit(screen.getByRole("textbox", { name: "Message" }).closest("form")!);

    expect(onOpenTask).toHaveBeenCalledWith("task_1", "run_1");
    expect(setMessage).toHaveBeenCalledWith("");
    expect(submitChat).not.toHaveBeenCalled();
  });

  it("submits external-agent slash commands as ordinary prompt text", () => {
    const submitChat = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentAdapterID: "claude_code",
        message: "/plan keep this in Claude",
        activeChatSession: {
          id: "chat_commands",
          title: "Agent commands",
          agent_id: "claude_code",
          driver_kind: "acp",
          execution_mode: "external_agent",
          status: "idle",
          workspace: "/tmp/hecate",
          available_commands: [{ name: "plan", description: "Create a plan" }],
          messages: [],
        },
      },
      { submitChat },
    );
    render(withRuntimeConsole(<ChatView onNavigate={() => undefined} />, { state, actions }));

    fireEvent.submit(screen.getByRole("textbox", { name: "Message" }).closest("form")!);

    expect(submitChat).toHaveBeenCalled();
    expect(draftChatProjectAssistant).not.toHaveBeenCalled();
  });

  it("requires text after the /proposal command", () => {
    const submitChat = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "/proposal",
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          agent_id: "hecate",
          title: "Project chat",
          execution_mode: "hecate_task",
          project_id: "proj_1",
          provider: "openai",
          model: "gpt-4o-mini",
          status: "idle",
          workspace: "/tmp/hecate",
          messages: [],
        },
      },
      { submitChat },
    );
    render(withRuntimeConsole(<ChatView onNavigate={() => undefined} />, { state, actions }));

    fireEvent.submit(screen.getByRole("textbox", { name: "Message" }).closest("form")!);

    expect(draftChatProjectAssistant).not.toHaveBeenCalled();
    expect(submitChat).not.toHaveBeenCalled();
  });

  it("browses previous user messages with ArrowUp and ArrowDown", () => {
    const setMessage = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "",
        activeChatSessionID: "chat_history",
        activeChatSession: {
          id: "chat_history",
          title: "History",
          messages: [
            { id: "u1", role: "user", content: "first prompt", created_at: "2026-05-01T10:00:00Z" },
            {
              id: "a1",
              role: "assistant",
              content: "first answer",
              created_at: "2026-05-01T10:00:01Z",
            },
            {
              id: "u2",
              role: "user",
              content: "second prompt",
              created_at: "2026-05-01T10:00:02Z",
            },
          ],
          provider_calls: [],
        },
      },
      { setMessage },
    );
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));

    let textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange(0, 0);
    fireEvent.keyDown(textarea, { key: "ArrowUp" });
    expect(setMessage).toHaveBeenLastCalledWith("second prompt");

    const latestState = { ...state, message: "second prompt" };
    rerender(withRuntimeConsole(<ChatView />, { state: latestState, actions }));
    textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange("second prompt".length, "second prompt".length);
    fireEvent.keyDown(textarea, { key: "ArrowUp" });
    expect(setMessage).toHaveBeenLastCalledWith("first prompt");

    const oldestState = { ...state, message: "first prompt" };
    rerender(withRuntimeConsole(<ChatView />, { state: oldestState, actions }));
    textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange("first prompt".length, "first prompt".length);
    fireEvent.keyDown(textarea, { key: "ArrowDown" });
    expect(setMessage).toHaveBeenLastCalledWith("second prompt");

    rerender(withRuntimeConsole(<ChatView />, { state: latestState, actions }));
    textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange("second prompt".length, "second prompt".length);
    fireEvent.keyDown(textarea, { key: "ArrowDown" });
    expect(setMessage).toHaveBeenLastCalledWith("");
  });

  it("keeps normal ArrowUp navigation inside multiline composer text", () => {
    const setMessage = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "line one\nline two",
        activeChatSessionID: "chat_history",
        activeChatSession: {
          id: "chat_history",
          title: "History",
          messages: [
            {
              id: "u1",
              role: "user",
              content: "previous prompt",
              created_at: "2026-05-01T10:00:00Z",
            },
          ],
          provider_calls: [],
        },
      },
      { setMessage },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange(5, 5);
    fireEvent.keyDown(textarea, { key: "ArrowUp" });
    expect(setMessage).not.toHaveBeenCalled();
  });

  it("restores pending composer text after browsing history", () => {
    const setMessage = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        message: "pending question",
        activeChatSessionID: "chat_history",
        activeChatSession: {
          id: "chat_history",
          title: "History",
          messages: [
            {
              id: "u1",
              role: "user",
              content: "previous prompt",
              created_at: "2026-05-01T10:00:00Z",
            },
          ],
          provider_calls: [],
        },
      },
      { setMessage },
    );
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));

    let textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange("pending question".length, "pending question".length);
    fireEvent.keyDown(textarea, { key: "ArrowUp" });
    expect(setMessage).toHaveBeenLastCalledWith("previous prompt");

    rerender(
      withRuntimeConsole(<ChatView />, {
        state: { ...state, message: "previous prompt" },
        actions,
      }),
    );
    textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange("previous prompt".length, "previous prompt".length);
    fireEvent.keyDown(textarea, { key: "ArrowDown" });
    expect(setMessage).toHaveBeenLastCalledWith("pending question");
  });
});

describe("ChatView Enter switch", () => {
  it("renders the segmented Enter/⌘+Enter or Ctrl+Enter switch", () => {
    const { state, actions } = setup();
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    // The switch is one of the toggle buttons in the input toolbar.
    const buttons = screen.getAllByRole("button");
    const labels = buttons.map((b) => b.textContent?.trim()).filter(Boolean);
    const hasEnterToggle = labels.some(
      (l) => l === "↵ to send" || /^[⌘+|Ctrl+]+↵ to send$/.test(l!),
    );
    expect(hasEnterToggle).toBe(true);
  });
});

describe("ChatView chats sidebar", () => {
  function daysAgo(days: number): string {
    const date = new Date();
    date.setDate(date.getDate() - days);
    return date.toISOString();
  }

  it("shows 'No chats yet' when chatSessions is empty", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      chatSessions: [],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText(/No chats yet/i)).toBeTruthy();
  });

  it("renders one row per chat with title", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      chatSessions: [
        {
          id: "s1",
          title: "First chat",
          message_count: 4,
          provider_call_count: 2,
          updated_at: daysAgo(0),
        } as any,
        {
          id: "s2",
          title: "Second chat",
          message_count: 2,
          provider_call_count: 1,
          updated_at: daysAgo(10),
        } as any,
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText("Today")).toBeTruthy();
    expect(screen.getByText("Older")).toBeTruthy();
    expect(screen.getByText("First chat")).toBeTruthy();
    expect(screen.getByText("Second chat")).toBeTruthy();
  });

  it("labels empty idle assistant chats as drafts", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      chatSessions: [
        {
          id: "a1",
          title: "Plan next work for hecate",
          agent_id: "hecate",
          status: "idle",
          message_count: 0,
          updated_at: daysAgo(0),
        } as any,
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    const row = screen.getByRole("button", { name: /^Chat Plan next work for hecate/ });
    expect(within(row).getByText("draft")).toBeTruthy();
    expect(within(row).queryByText("0 msg")).toBeNull();
    expect(within(row).queryByText("idle")).toBeNull();
  });

  it("filters chat history by title and route metadata", async () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      chatSessions: [
        {
          id: "s1",
          title: "Budget check",
          execution_mode: "hecate_task",
          tools_enabled: false,
          status: "completed",
          provider: "anthropic",
          message_count: 4,
          updated_at: daysAgo(0),
        } as any,
        {
          id: "s2",
          title: "Release notes cleanup",
          execution_mode: "hecate_task",
          tools_enabled: false,
          status: "completed",
          provider: "openai",
          message_count: 2,
          updated_at: daysAgo(0),
        } as any,
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Search chats"), "anthropic");
    expect(screen.getByText("Budget check")).toBeTruthy();
    expect(screen.queryByText("Release notes cleanup")).toBeNull();
  });

  it("filters agent history by adapter and status metadata", async () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      chatSessions: [
        {
          id: "a1",
          title: "Codex refactor",
          agent_id: "codex",
          status: "completed",
          message_count: 4,
          updated_at: daysAgo(0),
        } as any,
        {
          id: "a2",
          title: "Cursor repro",
          agent_id: "cursor_agent",
          status: "failed",
          message_count: 2,
          updated_at: daysAgo(0),
        } as any,
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Search chats"), "failed");
    expect(screen.getByText("Cursor repro")).toBeTruthy();
    expect(screen.queryByText("Codex refactor")).toBeNull();
  });

  it("allows renaming agent chats from the sidebar", async () => {
    const renameChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        chatSessions: [
          {
            id: "a1",
            title: "Codex refactor",
            agent_id: "codex",
            status: "completed",
            message_count: 4,
            updated_at: daysAgo(0),
          } as any,
        ],
      },
      { renameChatSession },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Rename chat Codex refactor" }));
    const input = screen.getByDisplayValue("Codex refactor");
    await user.clear(input);
    await user.type(input, "Docs cleanup{Enter}");

    expect(renameChatSession).toHaveBeenCalledWith("a1", "Docs cleanup");
  });

  it("calls selectChatSession when clicking a chat row", async () => {
    const selectChatSession = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        chatSessions: [
          { id: "s1", title: "Pick me", message_count: 0, provider_call_count: 0 } as any,
        ],
      },
      { selectChatSession },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    const user = userEvent.setup();
    await user.click(screen.getByText("Pick me"));
    expect(selectChatSession).toHaveBeenCalledWith("s1");
  });

  it("calls selectChatSession when pressing Enter or Space on a focused chat row", async () => {
    const selectChatSession = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        chatSessions: [
          { id: "s1", title: "Pick me", message_count: 0, provider_call_count: 0 } as any,
        ],
      },
      { selectChatSession },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    const user = userEvent.setup();
    const row = screen.getByRole("button", { name: /^Chat Pick me$/ });
    row.focus();
    await user.keyboard("{Enter}");
    expect(selectChatSession).toHaveBeenLastCalledWith("s1");
    await user.keyboard(" ");
    expect(selectChatSession).toHaveBeenLastCalledWith("s1");
  });
});

describe("ChatView external-agent target", () => {
  it("shows the unsandboxed external-agent reminder in agent mode only", () => {
    const { state, actions } = setup({ chatTarget: "external_agent" });
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText(/External agents run as your OS user/)).toBeTruthy();

    const modelState = setup({ chatTarget: "agent", defaultChatToolsEnabled: false }).state;
    rerender(withRuntimeConsole(<ChatView />, { state: modelState, actions }));
    expect(screen.queryByText(/External agents run as your OS user/)).toBeNull();
  });

  it("does not show provider setup actions when agent chat has no available CLI", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      message: "run codex",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
      agentAdapterID: "codex",
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: false,
          status: "missing",
          error: "exec: codex-acp-adapter not found",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Codex is unavailable")).toBeTruthy();
    expect(screen.getByText(/could not start Codex/)).toBeTruthy();
    expect(screen.getAllByText("Codex").length).toBeGreaterThan(0);
    expect(
      screen.getByText(/Install Codex CLI plus the Codex ACP adapter, then sign in with Codex/),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: /Install/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Auth/ })).toBeTruthy();
    expect(screen.getByText(/codex-acp-adapter not found/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Open Connections/i })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Add selected/i })).toBeNull();
  });

  it("renders external agent controls and keeps agent choice scoped to new chats", async () => {
    const setChatTarget = vi.fn();
    const setAgentAdapterID = vi.fn();
    const setNewChatAgent = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentAdapterID: "codex",
        agentWorkspace: "/tmp/hecate",
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
          {
            id: "claude_code",
            name: "Claude Code",
            kind: "acp",
            command: "claude-code-acp-adapter",
            available: false,
            status: "missing",
            cost_mode: "external",
          },
        ],
        chatSessions: [
          {
            id: "a1",
            title: "Codex work",
            agent_id: "codex",
            workspace: "/tmp/hecate",
            status: "completed",
            message_count: 2,
          } as any,
        ],
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Codex work",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          config_options: [
            {
              id: "model",
              name: "Model",
              category: "model",
              type: "select",
              current_value: "fast",
              options: [
                { value: "fast", name: "Fast" },
                { value: "smart", name: "Smart" },
              ],
            },
            {
              id: "auto_approve",
              name: "Auto approve",
              category: "mode",
              type: "boolean",
              current_bool: false,
            },
          ],
          messages: [
            { id: "m1", role: "user", content: "review this", created_at: "2026-05-03T10:00:00Z" },
            {
              id: "m2",
              run_id: "agent_run_c4",
              request_id: "req_codex_123456",
              trace_id: "0123456789abcdef0123456789abcdef",
              role: "assistant",
              content: "Looks good.",
              raw_output: `{"sessionId":"native_codex_1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Looks good."}}}`,
              agent_id: "codex",
              agent_name: "Codex",
              driver_kind: "acp",
              native_session_id: "native_codex_1",
              status: "completed",
              cost_mode: "external",
              diff_stat:
                "README.md | 2 +-\nui/src/features/chats/ChatView.tsx | 12 +++++++---\n2 files changed, 10 insertions(+), 4 deletions(-)",
              diff: "diff --git a/README.md b/README.md",
              created_at: "2026-05-03T10:00:01Z",
              activities: [
                {
                  type: "started",
                  status: "completed",
                  title: "Starting external agent",
                  detail: "Codex ACP session started",
                },
                {
                  id: "plan:0:Inspect",
                  type: "plan",
                  status: "completed",
                  kind: "high",
                  title: "Inspect changes",
                },
                {
                  id: "plan:1:Summarize",
                  type: "plan",
                  status: "in_progress",
                  kind: "medium",
                  title: "Summarize result",
                },
                {
                  id: "tool:call_1",
                  type: "tool_call",
                  status: "completed",
                  kind: "execute",
                  title: "git diff --stat",
                  detail: "README.md:12",
                },
                { type: "completed", status: "completed", title: "Final answer" },
              ],
            },
          ],
        } as any,
      },
      {
        setChatTarget,
        setAgentAdapterID,
        setNewChatAgent,
        setChatConfigOption: vi.fn(async () => true),
      },
    );
    const onOpenTrace = vi.fn();
    render(withRuntimeConsole(<ChatView onOpenTrace={onOpenTrace} />, { state, actions }));

    expect(screen.queryByDisplayValue("/tmp/hecate")).toBeNull();
    expect(screen.getByRole("button", { name: "Workspace: /tmp/hecate" })).toBeTruthy();
    expect(screen.getAllByText("Codex work").length).toBeGreaterThan(0);
    expect(screen.getByText("Codex session · Completed · /tmp/hecate")).toBeTruthy();
    expect(screen.getByLabelText("External agent message controls")).toBeTruthy();
    const modelPicker = screen.getByRole("button", { name: "Model" });
    expect(modelPicker).toHaveTextContent("Fast");
    await userEvent.click(modelPicker);
    await userEvent.click(screen.getByRole("option", { name: /Smart/ }));
    expect(actions.setChatConfigOption).toHaveBeenCalledWith("a1", "model", "smart");
    const modeToggle = screen.getByRole("button", { name: /mode: off/i });
    await userEvent.click(modeToggle);
    expect(actions.setChatConfigOption).toHaveBeenCalledWith("a1", "auto_approve", true);
    expect(screen.getByText("Looks good.")).toBeTruthy();
    expect(screen.queryByText(/ACP native_codex/)).toBeNull();
    expect(screen.getByTitle(/Native session native_codex_1/)).toBeTruthy();
    const traceButton = screen.getByRole("button", { name: /Open Trace req_code/i });
    expect(traceButton).toBeTruthy();
    expect(screen.queryByText("Starting external agent")).toBeNull();
    expect(screen.getByText("completed · 1/2 plan · 1 tool")).toBeTruthy();
    expect(screen.getByText("Inspect changes")).toBeTruthy();
    expect(screen.getByText("Summarize result")).toBeTruthy();
    expect(screen.getByText("Ran command")).toBeTruthy();
    expect(screen.getByText("README.md:12")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Workspace changes" })).toBeTruthy();
    const changedFilesButton = screen.getByRole("button", { name: "Open 2 files" });
    expect(changedFilesButton).toHaveAttribute(
      "title",
      "Workspace changes · 2 files changed, 10 insertions(+), 4 deletions(-)",
    );
    expect(screen.queryByText("2 files changed, 10 insertions(+), 4 deletions(-)")).toBeNull();
    expect(screen.getByText("raw agent output · 1 line")).toBeTruthy();
    expect(screen.getAllByText("completed").length).toBeGreaterThan(0);
    const user = userEvent.setup();
    await user.click(changedFilesButton);
    expect(await screen.findByLabelText("Workspace changes panel")).toBeTruthy();
    await user.click(traceButton);
    expect(onOpenTrace).toHaveBeenCalledWith("req_codex_123456");
    expect(screen.getAllByText("Codex").length).toBeGreaterThan(0);
    const agentPicker = screen.getByRole("button", { name: "Choose agent for new chat" });
    expect(screen.getByRole("button", { name: "New Hecate chat" })).toBeTruthy();
    await user.click(agentPicker);
    const claudeOption = screen.getByRole("option", { name: /Claude Code/ });
    expect(claudeOption).not.toHaveAttribute("aria-disabled");
    await user.click(claudeOption);
    expect(setAgentAdapterID).not.toHaveBeenCalled();

    await user.click(agentPicker);
    const hecateOption = screen.getByRole("option", { name: /Hecate/ });
    expect(hecateOption).not.toHaveAttribute("aria-disabled", "true");
    await user.click(hecateOption);
    expect(setNewChatAgent).toHaveBeenCalledWith("hecate");
    expect(setChatTarget).not.toHaveBeenCalled();
  });

  it("does not open multiple workspace folder dialogs from repeated clicks", async () => {
    let resolveDialog: (value: boolean) => void = () => {};
    const chooseAgentWorkspace = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          resolveDialog = resolve;
        }),
    );
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Codex work",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "idle",
          messages: [],
        } as any,
      },
      { chooseAgentWorkspace },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const workspaceButton = screen.getByRole("button", { name: "Workspace: /tmp/hecate" });
    fireEvent.click(workspaceButton);
    fireEvent.click(workspaceButton);

    expect(chooseAgentWorkspace).toHaveBeenCalledTimes(1);
    expect(workspaceButton).toBeDisabled();

    resolveDialog(true);
    await waitFor(() => expect(workspaceButton).not.toBeDisabled());
  });

  it("allows choosing an agent before an agent chat is created", async () => {
    const setNewChatAgent = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        newChatAgentID: "codex",
        agentAdapterID: "codex",
        activeChatSessionID: "",
        activeChatSession: null,
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
          {
            id: "claude_code",
            name: "Claude Code",
            kind: "acp",
            command: "claude-code-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { setNewChatAgent },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Choose agent for new chat" }));
    await user.click(screen.getByText("Claude Code"));
    expect(setNewChatAgent).toHaveBeenCalledWith("claude_code");
  });

  it("checks available external agents when Chats opens", async () => {
    const probeAgentAdapter = vi.fn(async () => null);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        newChatAgentID: "codex",
        agentAdapterID: "codex",
        activeChatSessionID: "",
        activeChatSession: null,
        agentWorkspace: "/tmp/hecate",
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
            auth_status: "unknown",
          },
        ],
      },
      { probeAgentAdapter },
    );

    render(withRuntimeConsole(<ChatView />, { state, actions }));

    await waitFor(() => expect(probeAgentAdapter).toHaveBeenCalledWith("codex"));
    expect(probeAgentAdapter).toHaveBeenCalledTimes(1);
  });

  it("shows Claude Code local auth repair when the agent reports auth required", async () => {
    const onNavigate = vi.fn();
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      agentWorkspace: "/tmp/hecate",
      message: "inspect repo",
      agentAdapters: [
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-code-acp-adapter",
          available: true,
          status: "available",
          auth_status: "unauthenticated",
          auth_error: "Run `claude /login` in Terminal.",
          cost_mode: "external",
        },
      ],
      agentAdapterHealthByID: new Map([
        [
          "claude_code",
          {
            adapter_id: "claude_code",
            status: "auth_required",
            stage: "new_session",
            duration_ms: 120,
            hint: "Run `claude /login` in Terminal.",
          },
        ],
      ]),
    });
    render(withRuntimeConsole(<ChatView onNavigate={onNavigate} />, { state, actions }));

    expect(screen.getByText("Set up Claude Code")).toBeTruthy();
    expect(screen.getByText(/ANTHROPIC_API_KEY/)).toBeTruthy();

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Open setup" }));
    expect(onNavigate).toHaveBeenCalledWith("connections");
    expect(sessionStorage.getItem("hecate.connectionsFocus")).toBe(
      "external-agent-auth-setup-claude_code",
    );
  });

  it("shows Claude Code setup repair in empty sessions without a token form", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      agentWorkspace: "/tmp/hecate",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        title: "Claude work",
        agent_id: "claude_code",
        agent_name: "Claude Code",
        workspace: "/tmp/hecate",
        messages: [],
        status: "idle",
        turns_used: 0,
        max_turns_per_session: 0,
      },
      agentAdapters: [
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-code-acp-adapter",
          available: true,
          status: "available",
          auth_status: "unauthenticated",
          auth_error: "Run `claude /login` in Terminal.",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Set up Claude Code")).toBeTruthy();
    expect(screen.getByText(/ANTHROPIC_API_KEY/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
  });

  it("shows Claude Code setup until the adapter probe verifies auth", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-code-acp-adapter",
          available: true,
          status: "available",
          auth_status: "unauthenticated",
          cost_mode: "external",
        },
      ],
      agentAdapterHealthByID: new Map([
        [
          "claude_code",
          { adapter_id: "claude_code", status: "auth_required", stage: "ready", duration_ms: 120 },
        ],
      ]),
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Set up Claude Code")).toBeTruthy();
  });

  it("does not show Claude Code setup after the adapter probe verifies auth", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-code-acp-adapter",
          available: true,
          status: "available",
          auth_status: "unknown",
          cost_mode: "external",
        },
      ],
      agentAdapterHealthByID: new Map([
        [
          "claude_code",
          { adapter_id: "claude_code", status: "ready", stage: "ready", duration_ms: 120 },
        ],
      ]),
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByText("Set up Claude Code")).toBeNull();
  });

  it("shows a waiting state for a running agent before transcript output arrives", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
      activeChatSessionID: "a1",
      activeChatSession: {
        id: "a1",
        title: "Running work",
        agent_id: "codex",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [
          { id: "m1", role: "user", content: "status", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "",
            agent_id: "codex",
            agent_name: "Codex",
            status: "running",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              {
                type: "running",
                status: "running",
                title: "Running",
                detail: "Waiting for ACP output",
              },
            ],
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Waiting for agent output...")).toBeTruthy();
    expect(screen.getAllByText("running").length).toBeGreaterThan(0);
  });

  it("shows transient agent narration as live assistant text while a run is active", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
      activeChatSessionID: "a1",
      activeChatSession: {
        id: "a1",
        title: "Inspect diff",
        agent_id: "codex",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [
          { id: "m1", role: "user", content: "show diff", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content:
              "I’ll check the current worktree diff and summarize the changed files plus the important hunks.",
            agent_id: "codex",
            agent_name: "Codex",
            status: "running",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              {
                type: "running",
                status: "running",
                title: "Running",
                detail: "Waiting for ACP output",
              },
            ],
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(
      screen.getByText(
        "I’ll check the current worktree diff and summarize the changed files plus the important hunks.",
      ),
    ).toBeTruthy();
    expect(
      screen
        .getByText(
          "I’ll check the current worktree diff and summarize the changed files plus the important hunks.",
        )
        .parentElement?.querySelector("[aria-hidden='true']"),
    ).toBeTruthy();
    expect(screen.queryByText("Waiting for agent output...")).toBeNull();
  });

  it("renders agent-reported usage below completed agent messages and in chat settings", async () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
      activeChatSessionID: "a1",
      activeChatSession: {
        id: "a1",
        title: "Usage check",
        agent_id: "codex",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          { id: "m1", role: "user", content: "status", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "Done.",
            agent_id: "codex",
            agent_name: "Codex",
            status: "completed",
            created_at: "2026-05-03T10:00:01Z",
            usage: {
              context_size: 200000,
              context_used: 42000,
              reported_cost_amount: "0.1234",
              reported_cost_currency: "USD",
            },
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("0.1234 USD")).toBeTruthy();
    expect(screen.getByText("42000/200000 context")).toBeTruthy();
    expect(screen.getByText("reported usage · not enforced by Hecate")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Chat settings" }));
    expect(screen.getByText("Reported usage")).toBeTruthy();
    expect(screen.getByText("42,000 / 200,000")).toBeTruthy();
    expect(screen.getAllByText("0.1234 USD").length).toBeGreaterThan(1);
    expect(screen.getByText(/Hecate does not enforce external-agent billing/i)).toBeTruthy();
  });

  it("renders Hecate-measured usage in chat settings", async () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      agentWorkspace: "/tmp/hecate",
      activeChatSessionID: "h1",
      activeChatSession: {
        id: "h1",
        execution_mode: "hecate_task",
        title: "Hecate work",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          { id: "m1", role: "user", content: "status", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "Done.",
            status: "completed",
            provider: "ollama",
            model: "qwen2.5-coder",
            created_at: "2026-05-03T10:00:01Z",
            usage: {
              context_size: 128000,
              context_used: 16000,
              reported_cost_amount: "0.002",
              reported_cost_currency: "USD",
            },
          },
        ],
      } as any,
      model: "qwen2.5-coder",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    await userEvent.click(screen.getByRole("button", { name: "Chat settings" }));

    expect(screen.getByText("Usage")).toBeTruthy();
    expect(screen.getByText("16,000 / 128,000")).toBeTruthy();
    expect(screen.getAllByText("0.002 USD").length).toBeGreaterThan(0);
    expect(screen.getByText(/Measured by Hecate/i)).toBeTruthy();
  });

  it("shows the current workspace diff in the workspace changes panel", async () => {
    const writeText = vi.fn(() => Promise.resolve());
    const getChatWorkspaceDiff = vi.fn(async () => ({
      workspace: "/tmp/hecate",
      diff_stat: "README.md | 1 +\ndocs/guide.md | 1 +\n2 files changed, 2 insertions(+)",
      diff: [
        "diff --git a/README.md b/README.md",
        "index 1111111..2222222 100644",
        "--- a/README.md",
        "+++ b/README.md",
        "@@ -1 +1 @@",
        "-old readme",
        "+current workspace line",
        "diff --git a/docs/guide.md b/docs/guide.md",
        "index 3333333..4444444 100644",
        "--- a/docs/guide.md",
        "+++ b/docs/guide.md",
        "@@ -1 +1 @@",
        "-old guide",
        "+guide line",
      ].join("\n"),
      has_changes: true,
      files: [
        { path: "README.md", additions: 1, deletions: 0, status: "modified" },
        { path: "docs/guide.md", additions: 1, deletions: 0, status: "modified" },
      ],
    }));
    const getChatWorkspaceFileDiff = vi.fn(async (_sessionID: string, path: string) =>
      path === "docs/guide.md"
        ? {
            path: "docs/guide.md",
            additions: 1,
            deletions: 0,
            status: "modified",
            diff: [
              "diff --git a/docs/guide.md b/docs/guide.md",
              "index 3333333..4444444 100644",
              "--- a/docs/guide.md",
              "+++ b/docs/guide.md",
              "@@ -1 +1 @@",
              "-old guide",
              "+current guide line",
            ].join("\n"),
          }
        : {
            path: "README.md",
            additions: 1,
            deletions: 0,
            status: "modified",
            diff: [
              "diff --git a/README.md b/README.md",
              "index 1111111..2222222 100644",
              "--- a/README.md",
              "+++ b/README.md",
              "@@ -1 +1 @@",
              "-old readme",
              "+current file line",
            ].join("\n"),
          },
    );
    const revertChatWorkspaceFiles = vi.fn(async () => ({
      workspace: "/tmp/hecate",
      diff_stat: "",
      diff: "",
      has_changes: false,
      files: [],
    }));
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review files",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [
            { id: "m1", role: "user", content: "change docs", created_at: "2026-05-03T10:00:00Z" },
            {
              id: "m2",
              role: "assistant",
              content: "Updated the docs.",
              agent_id: "codex",
              agent_name: "Codex",
              status: "completed",
              diff_stat: "old.txt | 1 +\n1 file changed, 1 insertion(+)",
              diff: [
                "diff --git a/old.txt b/old.txt",
                "index 5555555..6666666 100644",
                "--- a/old.txt",
                "+++ b/old.txt",
                "@@ -1 +1 @@",
                "-old line",
                "+captured line",
              ].join("\n"),
              created_at: "2026-05-03T10:00:01Z",
            },
          ],
        } as any,
      },
      { getChatWorkspaceDiff, getChatWorkspaceFileDiff, revertChatWorkspaceFiles },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    expect(screen.getByText("1 file")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Workspace changes" }));

    expect(getChatWorkspaceDiff).toHaveBeenCalledWith("a1");
    expect(await screen.findByRole("region", { name: "Workspace review" })).toBeTruthy();
    expect(await screen.findByRole("region", { name: "Diff README.md" })).toBeTruthy();
    expect(screen.getByTitle("/tmp/hecate")).toBeTruthy();
    expect((await screen.findAllByText("2 files changed, 2 insertions(+)")).length).toBeGreaterThan(
      0,
    );
    const workspacePanel = screen.getByLabelText("Workspace changes panel");
    expect(screen.getByRole("button", { name: "Expand diff docs/guide.md" })).toBeTruthy();
    const diffCallsBeforeRefresh = getChatWorkspaceDiff.mock.calls.length;
    await user.click(within(workspacePanel).getByRole("button", { name: "Refresh" }));
    await waitFor(() =>
      expect(getChatWorkspaceDiff.mock.calls.length).toBeGreaterThan(diffCallsBeforeRefresh),
    );
    expect(screen.getByRole("button", { name: "Expand diff docs/guide.md" })).toBeTruthy();
    expect(getChatWorkspaceFileDiff).toHaveBeenCalledWith("a1", "README.md");
    const readmePreview = await screen.findByTestId("workspace-file-diff-preview");
    expect(readmePreview).toHaveStyle({ overflow: "hidden" });
    expect(readmePreview).not.toHaveStyle({ height: "min(42vh, 480px)" });
    expect(readmePreview).not.toHaveAttribute("data-preview-height");
    expect(readmePreview.style.contain).toBe("");
    expect(document.querySelectorAll("diffs-container.diff-viewer-file").length).toBeGreaterThan(0);
    await user.type(screen.getByLabelText("Search changed files"), "guide");
    const changedFilesList = within(workspacePanel).getByLabelText("Changed files");
    expect(within(changedFilesList).queryByText("README.md")).toBeNull();
    expect(
      within(changedFilesList).getByRole("button", {
        name: "Expand diff docs/guide.md",
      }),
    ).toBeTruthy();
    await user.clear(screen.getByLabelText("Search changed files"));
    await user.click(screen.getByRole("button", { name: "Copy complete workspace patch" }));
    await waitFor(() =>
      expect(writeText).toHaveBeenCalledWith(expect.stringContaining("diff --git a/README.md")),
    );
    await user.click(screen.getByRole("button", { name: "Copy diff README.md" }));
    await waitFor(() =>
      expect(writeText).toHaveBeenCalledWith(expect.stringContaining("+current file line")),
    );
    await user.click(screen.getByRole("button", { name: "Expand diff docs/guide.md" }));
    expect(getChatWorkspaceFileDiff).not.toHaveBeenCalledWith("a1", "docs/guide.md");
    expect((await screen.findAllByTestId("workspace-file-diff-preview")).length).toBeGreaterThan(0);
    expect(screen.getByLabelText("Diff docs/guide.md")).toBeTruthy();
    expect(document.querySelectorAll("diffs-container.diff-viewer-file").length).toBeGreaterThan(0);
    expect(screen.getByLabelText("Workspace changes panel").textContent).not.toContain(
      "captured line",
    );

    await user.click(screen.getByRole("button", { name: "Discard docs/guide.md" }));
    expect(revertChatWorkspaceFiles).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Confirm discard docs/guide.md" }));
    expect(revertChatWorkspaceFiles).toHaveBeenCalledWith("a1", ["docs/guide.md"]);
    expect(await screen.findByText("The current workspace is clean.")).toBeTruthy();
  });

  it("shows a loading empty state while the current workspace diff is still loading", async () => {
    let resolveDiff!: (value: {
      workspace: string;
      diff_stat: string;
      diff: string;
      has_changes: boolean;
      files: any[];
    }) => void;
    const getChatWorkspaceDiff = vi.fn(
      () =>
        new Promise<{
          workspace: string;
          diff_stat: string;
          diff: string;
          has_changes: boolean;
          files: any[];
        }>((resolve) => {
          resolveDiff = resolve;
        }),
    );
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review loading",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { getChatWorkspaceDiff },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    await userEvent.click(screen.getByRole("button", { name: "Workspace changes" }));

    expect(await screen.findByText("Loading changed files...")).toBeTruthy();
    expect(screen.queryByText("No changed files found.")).toBeNull();

    resolveDiff({
      workspace: "/tmp/hecate",
      diff_stat: "",
      diff: "",
      has_changes: false,
      files: [],
    });
    expect(await screen.findByText("The current workspace is clean.")).toBeTruthy();
  });

  it("shows an explicit empty diff preview when a selected file has no text patch", async () => {
    const getChatWorkspaceDiff = vi.fn(async () => ({
      workspace: "/tmp/hecate",
      diff_stat: "docs/screenshots/chat.png | Bin 100 -> 120 bytes\n1 file changed",
      diff: "",
      has_changes: true,
      files: [
        {
          path: "docs/screenshots/chat.png",
          additions: 0,
          deletions: 0,
          status: "modified",
        },
      ],
    }));
    const getChatWorkspaceFileDiff = vi.fn(async () => ({
      path: "docs/screenshots/chat.png",
      additions: 0,
      deletions: 0,
      status: "modified",
      diff: "",
    }));
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review binary file",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { getChatWorkspaceDiff, getChatWorkspaceFileDiff },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Workspace changes" }));

    await user.click(screen.getByRole("button", { name: "Expand diff docs/screenshots/chat.png" }));
    expect(
      await screen.findByRole("region", { name: "Diff docs/screenshots/chat.png" }),
    ).toBeTruthy();
    expect(getChatWorkspaceFileDiff).toHaveBeenCalledWith("a1", "docs/screenshots/chat.png");
    expect(await screen.findByText(/No text diff was captured for this file/)).toBeTruthy();
  });

  it("selects the first changed file that can show a text diff", async () => {
    const binaryFiles = Array.from({ length: 18 }, (_, index) => ({
      path: `docs/screenshots/chat-${index}.png`,
      additions: 0,
      deletions: 0,
      status: "modified",
    }));
    const readmeDiff = [
      "diff --git a/README.md b/README.md",
      "index 1111111..2222222 100644",
      "--- a/README.md",
      "+++ b/README.md",
      "@@ -1 +1 @@",
      "-old readme",
      "+readme text diff",
    ].join("\n");
    const getChatWorkspaceDiff = vi.fn(async () => ({
      workspace: "/tmp/hecate",
      diff_stat:
        "docs/screenshots/chat.png | Bin 100 -> 120 bytes\nREADME.md | 1 +\n2 files changed, 1 insertion(+)",
      diff: readmeDiff,
      has_changes: true,
      files: [
        ...binaryFiles,
        {
          path: "README.md",
          additions: 0,
          deletions: 0,
          status: "modified",
        },
      ],
    }));
    const getChatWorkspaceFileDiff = vi.fn(async (_sessionID: string, path: string) => ({
      path,
      additions: 0,
      deletions: 0,
      status: "modified",
      diff: path === "README.md" ? readmeDiff : "",
    }));
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review mixed files",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { getChatWorkspaceDiff, getChatWorkspaceFileDiff },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Workspace changes" }));

    expect(await screen.findByRole("region", { name: "Diff README.md" })).toBeTruthy();
    expect(getChatWorkspaceFileDiff).toHaveBeenCalledWith("a1", "README.md");
    expect(getChatWorkspaceFileDiff).not.toHaveBeenCalledWith("a1", "docs/screenshots/chat-0.png");
    expect(screen.getByTestId("workspace-file-diff-preview")).toBeTruthy();
    expect(document.querySelectorAll("diffs-container.diff-viewer-file").length).toBeGreaterThan(0);
  });

  it("does not probe file diffs when the workspace diff response only has file metadata", async () => {
    const files = Array.from({ length: 32 }, (_, index) => ({
      path: `src/file-${index}.ts`,
      additions: 1,
      deletions: 0,
      status: "modified",
    }));
    const getChatWorkspaceDiff = vi.fn(async () => ({
      workspace: "/tmp/hecate",
      diff_stat: "32 files changed, 32 insertions(+)",
      diff: "",
      has_changes: true,
      files,
    }));
    const getChatWorkspaceFileDiff = vi.fn(async (_sessionID: string, path: string) => ({
      path,
      additions: 1,
      deletions: 0,
      status: "modified",
      diff: [
        `diff --git a/${path} b/${path}`,
        "index 1111111..2222222 100644",
        `--- a/${path}`,
        `+++ b/${path}`,
        "@@ -1 +1 @@",
        "-old line",
        "+new line",
      ].join("\n"),
    }));
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review metadata only",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { getChatWorkspaceDiff, getChatWorkspaceFileDiff },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Workspace changes" }));

    expect(await screen.findByRole("button", { name: "Expand diff src/file-0.ts" })).toBeTruthy();
    expect(getChatWorkspaceFileDiff).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Expand diff src/file-0.ts" }));
    expect(await screen.findByRole("region", { name: "Diff src/file-0.ts" })).toBeTruthy();
    expect(getChatWorkspaceFileDiff).toHaveBeenCalledTimes(1);
    expect(getChatWorkspaceFileDiff).toHaveBeenCalledWith("a1", "src/file-0.ts");
  });

  it("shows the full workspace file tree separately from the review diff list", async () => {
    const getChatWorkspaceDiff = vi.fn(async () => ({
      workspace: "/tmp/hecate",
      diff_stat:
        "ui/src/features/chats/ChatView.tsx | 1 +\nui/src/features/chats/ChatView.test.tsx | 1 +\n2 files changed, 2 insertions(+)",
      diff: "",
      has_changes: true,
      files: [
        {
          path: "ui/src/features/chats/ChatView.tsx",
          additions: 1,
          deletions: 0,
          status: "modified",
        },
        {
          path: "ui/src/features/chats/ChatView.test.tsx",
          additions: 1,
          deletions: 0,
          status: "modified",
        },
      ],
    }));
    const getChatWorkspaceFileDiff = vi.fn(async (_sessionID: string, path: string) => ({
      path,
      additions: 1,
      deletions: 0,
      status: "modified",
      diff: [
        `diff --git a/${path} b/${path}`,
        "index 1111111..2222222 100644",
        `--- a/${path}`,
        `+++ b/${path}`,
        "@@ -1 +1 @@",
        "-old line",
        "+new line",
      ].join("\n"),
    }));
    const getChatWorkspaceFiles = vi.fn(async () => ({
      workspace: "/tmp/hecate",
      files: [
        { path: "ui", name: "ui", kind: "directory" },
        { path: "ui/src", name: "src", kind: "directory" },
        { path: "ui/src/features", name: "features", kind: "directory" },
        { path: "ui/src/features/chats", name: "chats", kind: "directory" },
        {
          path: "ui/src/features/chats/ChatView.tsx",
          name: "ChatView.tsx",
          kind: "file",
          status: "modified",
          size_bytes: 1234,
        },
        {
          path: "ui/src/features/chats/ChatView.test.tsx",
          name: "ChatView.test.tsx",
          kind: "file",
          status: "modified",
          size_bytes: 2345,
        },
        {
          path: "docs/development.md",
          name: "development.md",
          kind: "file",
          status: "untracked",
          size_bytes: 3456,
        },
      ],
    }));
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review nested files",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { getChatWorkspaceDiff, getChatWorkspaceFileDiff, getChatWorkspaceFiles },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Workspace changes" }));

    const review = within(await screen.findByLabelText("Workspace review"));
    expect(
      review.getByRole("button", {
        name: "Expand diff ui/src/features/chats/ChatView.tsx",
      }),
    ).toBeTruthy();
    expect(
      review.queryByRole("button", { name: "Collapse folder ui/src/features/chats" }),
    ).toBeNull();
    expect(getChatWorkspaceFileDiff).not.toHaveBeenCalled();
    await user.click(
      review.getByRole("button", {
        name: "Expand diff ui/src/features/chats/ChatView.tsx",
      }),
    );
    expect(getChatWorkspaceFileDiff).toHaveBeenCalledWith(
      "a1",
      "ui/src/features/chats/ChatView.tsx",
    );

    await user.click(screen.getByRole("tab", { name: "Files" }));
    expect(getChatWorkspaceFiles).toHaveBeenCalledWith("a1");
    const fileTreeElement = await screen.findByLabelText("Workspace file tree");
    expect(fileTreeElement).toHaveStyle({ overflowY: "auto" });
    expect(screen.queryByRole("tree", { name: "Workspace file tree" })).toBeNull();
    expect(screen.queryAllByRole("treeitem")).toHaveLength(0);
    const fileTree = within(fileTreeElement);
    expect(
      fileTree.getByRole("button", { name: "Expand folder ui/src/features/chats" }),
    ).toBeTruthy();
    expect(fileTree.queryByRole("button", { name: "Collapse folder ui" })).toBeNull();
    expect(fileTree.queryByText("ChatView.tsx")).toBeNull();

    await user.click(fileTree.getByRole("button", { name: "Expand folder ui/src/features/chats" }));
    expect(
      fileTree.getByRole("button", { name: "Collapse folder ui/src/features/chats" }),
    ).toBeTruthy();
    expect(fileTree.getByText("ChatView.tsx")).toBeTruthy();
    expect(fileTree.getByText("ChatView.test.tsx")).toBeTruthy();

    await user.type(screen.getByLabelText("Search workspace files"), "development");
    expect(fileTree.queryByText("ChatView.tsx")).toBeNull();
    expect(fileTree.getByText("development.md")).toBeTruthy();
  });

  it("shows a clean current-diff state even when the transcript has no captured changes", async () => {
    const getChatWorkspaceDiff = vi.fn(async () => ({
      workspace: "/tmp/hecate",
      diff_stat: "",
      diff: "",
      has_changes: false,
      files: [],
    }));
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review files",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { getChatWorkspaceDiff },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    const changes = screen.getByRole("button", { name: "Workspace changes" });
    expect(changes).not.toBeDisabled();
    expect(changes).toHaveAttribute("title", "Show current workspace diff");
    await user.click(changes);

    expect(await screen.findByText("The current workspace is clean.")).toBeTruthy();
  });

  it("keeps settings and workspace changes in the same resizable right panel", async () => {
    const getChatWorkspaceDiff = vi.fn(async () => ({
      workspace: "/tmp/hecate",
      diff_stat: "",
      diff: "",
      has_changes: false,
      files: [],
    }));
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review files",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { getChatWorkspaceDiff },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Chat settings" }));
    const settingsPanel = screen.getByLabelText("Chat settings panel");
    expect(settingsPanel).toHaveStyle({ width: "380px" });

    const handle = screen.getByRole("separator", { name: "Resize right panel" });
    fireEvent.pointerDown(handle, { clientX: 800, pointerId: 1 });
    fireEvent.pointerMove(handle, { clientX: 740, pointerId: 1 });
    expect(settingsPanel).toHaveStyle({ width: "440px" });
    expect(localStorage.getItem("hecate.chat.rightPanelWidth")).toBe("440");

    await user.click(screen.getByRole("button", { name: "Workspace changes" }));
    expect(await screen.findByLabelText("Workspace changes panel")).toHaveStyle({
      width: "440px",
    });
  });

  it("restores the saved right panel width", async () => {
    localStorage.setItem("hecate.chat.rightPanelWidth", "432");
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      activeChatSessionID: "a1",
      activeChatSession: {
        id: "a1",
        title: "Review files",
        agent_id: "codex",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    await userEvent.click(screen.getByRole("button", { name: "Chat settings" }));

    expect(screen.getByLabelText("Chat settings panel")).toHaveStyle({ width: "432px" });
  });

  it("surfaces current workspace diff load failures", async () => {
    const getChatWorkspaceDiff = vi.fn(async () => null);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review all",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { getChatWorkspaceDiff },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Workspace changes" }));

    expect(await screen.findByText("Could not load the current workspace diff.")).toBeTruthy();
  });

  it("clears workspace diff loading state when the diff request rejects", async () => {
    const getChatWorkspaceDiff = vi.fn(async () => {
      throw new Error("diff failed");
    });
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review all",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { getChatWorkspaceDiff },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Workspace changes" }));

    expect(await screen.findByText("Could not load the current workspace diff.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Refresh" })).not.toBeDisabled();
    expect(screen.queryByText("Refreshing...")).toBeNull();
  });

  it("clears workspace file diff loading state when a file diff request rejects", async () => {
    const getChatWorkspaceDiff = vi.fn(async () => ({
      workspace: "/tmp/hecate",
      diff_stat: "README.md | 1 +\n1 file changed, 1 insertion(+)",
      diff: "",
      has_changes: true,
      files: [{ path: "README.md", additions: 1, deletions: 0, status: "modified" }],
    }));
    const getChatWorkspaceFileDiff = vi.fn(async () => {
      throw new Error("file diff failed");
    });
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review files",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { getChatWorkspaceDiff, getChatWorkspaceFileDiff },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Workspace changes" }));
    await user.click(screen.getByRole("button", { name: "Expand diff README.md" }));

    expect(await screen.findByText("Could not load that file diff.")).toBeTruthy();
    expect(screen.queryByText("Loading diff...")).toBeNull();
    expect(screen.getByRole("button", { name: "Collapse diff README.md" })).toBeTruthy();
  });

  it("clears workspace discard state when revert rejects", async () => {
    const getChatWorkspaceDiff = vi.fn(async () => ({
      workspace: "/tmp/hecate",
      diff_stat: "README.md | 1 +\n1 file changed, 1 insertion(+)",
      diff: [
        "diff --git a/README.md b/README.md",
        "index 1111111..2222222 100644",
        "--- a/README.md",
        "+++ b/README.md",
        "@@ -1 +1 @@",
        "-old readme",
        "+current file line",
      ].join("\n"),
      has_changes: true,
      files: [{ path: "README.md", additions: 1, deletions: 0, status: "modified" }],
    }));
    const getChatWorkspaceFileDiff = vi.fn(async () => ({
      path: "README.md",
      additions: 1,
      deletions: 0,
      status: "modified",
      diff: [
        "diff --git a/README.md b/README.md",
        "index 1111111..2222222 100644",
        "--- a/README.md",
        "+++ b/README.md",
        "@@ -1 +1 @@",
        "-old readme",
        "+current file line",
      ].join("\n"),
    }));
    const revertChatWorkspaceFiles = vi.fn(async () => {
      throw new Error("revert failed");
    });
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review files",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { getChatWorkspaceDiff, getChatWorkspaceFileDiff, revertChatWorkspaceFiles },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Workspace changes" }));
    await screen.findByRole("button", { name: "Discard README.md" });
    await user.click(screen.getByRole("button", { name: "Discard README.md" }));
    await user.click(screen.getByRole("button", { name: "Confirm discard README.md" }));

    expect(await screen.findByText("Could not discard those workspace changes.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Discard README.md" })).not.toBeDisabled();
    expect(screen.queryByText("Discarding...")).toBeNull();
  });

  it("disables stop and shows cancelling feedback after stop is requested", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      chatLoading: true,
      chatCancelling: true,
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
      activeChatSessionID: "a1",
      activeChatSession: {
        id: "a1",
        title: "Stopping work",
        agent_id: "codex",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const stop = screen.getByRole("button", { name: "Stop external agent" }) as HTMLButtonElement;
    expect(stop.disabled).toBe(true);
    expect(stop.title).toBe("Stopping...");
    expect(screen.getByText("Stopping...")).toBeTruthy();
  });

  it("shows stop controls for a restored running external-agent session", async () => {
    const cancelAgentChat = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        chatLoading: false,
        agentWorkspace: "/tmp/hecate",
        agentAdapters: [
          {
            id: "grok_build",
            name: "Grok Build",
            kind: "acp",
            command: "grok",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Running work",
          agent_id: "grok_build",
          driver_kind: "acp",
          workspace: "/tmp/hecate",
          status: "idle",
          segments: [
            {
              id: "seg_1",
              turn_kind: "external_agent",
              execution_mode: "external_agent",
              workspace: "/tmp/hecate",
              status: "running",
              message_count: 2,
            },
          ],
          messages: [],
        } as any,
      },
      { cancelAgentChat },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    expect(screen.getByRole("button", { name: "Stop external agent" })).toBeTruthy();
    expect(screen.getByText("External Agent is working. New messages will queue.")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Stop external agent" }));
    expect(cancelAgentChat).toHaveBeenCalledTimes(1);
  });

  it("renders failed agent runs as an error notice with raw diagnostics separate", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-code-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
      activeChatSessionID: "a1",
      activeChatSession: {
        id: "a1",
        title: "Failed work",
        agent_id: "claude_code",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "failed",
        messages: [
          { id: "m1", role: "user", content: "status", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "Claude Code usage limit: credit balance is too low",
            raw_output: `{"code":-32603,"message":"Internal error: Credit balance is too low"}`,
            error: "Claude Code usage limit: credit balance is too low",
            agent_id: "claude_code",
            agent_name: "Claude Code",
            status: "failed",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              {
                type: "failed",
                status: "failed",
                title: "Failed",
                detail: "Claude Code usage limit: credit balance is too low",
              },
            ],
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("agent run failed")).toBeTruthy();
    expect(
      screen.getAllByText("Claude Code usage limit: credit balance is too low").length,
    ).toBeGreaterThan(0);
    expect(screen.getByText("raw agent output · 1 line")).toBeTruthy();
    expect(screen.getAllByText("failed").length).toBeGreaterThan(0);
  });

  it("opens the workspace picker action from the folder button", async () => {
    const chooseAgentWorkspace = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "",
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { chooseAgentWorkspace },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByTitle("Choose workspace folder"));
    expect(chooseAgentWorkspace).toHaveBeenCalled();
  });

  it("allows pasting a workspace path when the folder dialog is unavailable", async () => {
    const chooseAgentWorkspace = vi.fn(async () => false);
    const setAgentWorkspace = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "",
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { chooseAgentWorkspace, setAgentWorkspace },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByTitle("Choose workspace folder"));
    await user.type(screen.getByPlaceholderText("/Users/alice/dev/project"), "/workspaces/hecate");
    await user.click(screen.getByRole("button", { name: "Use" }));

    expect(setAgentWorkspace).toHaveBeenCalledWith("/workspaces/hecate");
  });

  it("uses typed workspace entry instead of the local picker in remote runtime", async () => {
    const chooseAgentWorkspace = vi.fn(async () => true);
    const setAgentWorkspace = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "",
        sessionInfo: {
          role: "operator",
          remote_identity: {
            actor_id: "actor_1",
            org_id: "org_1",
            project_id: "proj_1",
            runtime_id: "rt_1",
          },
        },
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { chooseAgentWorkspace, setAgentWorkspace },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByTitle("Set workspace path"));
    expect(chooseAgentWorkspace).not.toHaveBeenCalled();

    const input = screen.getByPlaceholderText("/workspace") as HTMLInputElement;
    expect(input.value).toBe("/workspace");
    await user.clear(input);
    await user.type(input, "/workspace/project");
    await user.click(screen.getByRole("button", { name: "Use" }));

    expect(setAgentWorkspace).toHaveBeenCalledWith("/workspace/project");
  });

  it("keeps the workspace changes button enabled for current git diff checks", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      activeChatSession: {
        id: "chat_1",
        agent_id: "codex",
        driver_kind: "acp",
        execution_mode: "external_agent",
        title: "Codex chat",
        workspace: "/tmp/hecate",
        status: "idle",
        messages: [],
      } as any,
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const changes = screen.getByRole("button", { name: "Workspace changes" });
    expect(changes).not.toBeDisabled();
    expect(changes).toHaveAttribute("title", "Show current workspace diff");
  });

  it("requires a workspace before sending to an external agent", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      message: "run codex",
      agentWorkspace: "",
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });

  it("explains why Hecate Chat cannot send with tools before workspace selection", async () => {
    const chooseAgentWorkspace = vi.fn(async () => "/tmp/hecate");
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        message: "inspect repo",
        agentWorkspace: "",
        providerScopedModels: [
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: {
              provider: "openai",
              provider_kind: "cloud",
              capabilities: { tool_calling: "basic", streaming: true, source: "catalog" },
            },
          },
        ],
      },
      { chooseAgentWorkspace },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText(/Hecate uses the workspace as the working directory/)).toBeTruthy();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Choose workspace" }));
    expect(chooseAgentWorkspace).toHaveBeenCalled();
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });
});

describe("ChatView model target", () => {
  it("announces markdown task-list checkbox state", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSessionID: "s1",
      activeChatSession: {
        id: "s1",
        title: "Tasks",
        messages: [{ id: "m1", sequence: 1, role: "assistant", content: "- [x] done\n- [ ] todo" }],
        provider_calls: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByRole("img", { name: "Completed task" })).toBeTruthy();
    expect(screen.getByRole("img", { name: "Incomplete task" })).toBeTruthy();
  });

  it("keeps provider and model pickers editable for an active model chat", async () => {
    const setProviderFilter = vi.fn();
    const setModel = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        providerFilter: "openai",
        model: "gpt-4o-mini",
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          title: "Model switching",
          messages: [],
          provider_calls: [],
        } as any,
        settingsConfig: {
          providers: [
            { id: "anthropic", name: "Anthropic", kind: "cloud", credential_configured: true },
            { id: "openai", name: "OpenAI", kind: "cloud", credential_configured: true },
          ],
        } as any,
        providerPresets: [
          { id: "anthropic", name: "Anthropic", kind: "cloud" },
          { id: "openai", name: "OpenAI", kind: "cloud" },
        ] as any,
        providerScopedModels: [
          {
            id: "claude-sonnet-4-20250514",
            owned_by: "anthropic",
            metadata: { provider: "anthropic", provider_kind: "cloud" },
          },
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: { provider: "openai", provider_kind: "cloud" },
          },
          {
            id: "gpt-4.1-mini",
            owned_by: "openai",
            metadata: { provider: "openai", provider_kind: "cloud" },
          },
        ],
      },
      { setProviderFilter, setModel },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    const providerPicker = screen.getByRole("button", { name: /OpenAI/i }) as HTMLButtonElement;
    expect(providerPicker.disabled).toBe(false);
    await user.click(providerPicker);
    await user.click(screen.getByText("Anthropic"));
    expect(setProviderFilter).toHaveBeenCalledWith("anthropic");

    const modelPicker = screen.getByRole("button", { name: /gpt-4o-mini/i }) as HTMLButtonElement;
    expect(modelPicker.disabled).toBe(false);
    await user.click(modelPicker);
    await user.click(screen.getByText("gpt-4.1-mini"));
    expect(setModel).toHaveBeenCalledWith("gpt-4.1-mini");
  });
});

describe("ChatView error display", () => {
  it("renders chatError using InlineError styling", () => {
    const { state, actions } = setup({ chatError: "Provider returned 500" });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText(/Provider returned 500/)).toBeTruthy();
  });

  it("hides model-required errors when the empty repair state already explains the fix", async () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
      providerScopedModels: [],
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
      chatError: "Choose a model before starting this chat.",
      chatErrorCode: "chat.model_required",
      chatErrorStatus: 400,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(await screen.findByText("No model provider configured")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Open Connections/i })).toBeTruthy();
    expect(screen.queryByText("Model required")).toBeNull();
    expect(screen.queryByText("400 · chat.model_required")).toBeNull();
    expect(screen.queryByText("Choose a model before starting this chat.")).toBeNull();
  });

  it("hides unavailable-model route errors when provider onboarding already explains the fix", async () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
      providerScopedModels: [],
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
      chatError: 'No routable provider reports model "ministral-3:latest".',
      chatErrorCode: "model_not_configured",
      chatErrorStatus: 422,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(await screen.findByText("No model provider configured")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Open Connections/i })).toBeTruthy();
    expect(screen.queryByText("Selected model is unavailable")).toBeNull();
    expect(screen.queryByText("422 · model_not_configured")).toBeNull();
    expect(screen.queryByText(/No routable provider reports model/)).toBeNull();
  });

  it("hides unavailable-model route errors when the empty state uses the broader setup copy", async () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
      providerScopedModels: [],
      agentAdapters: [],
      chatError: 'No routable provider reports model "ministral-3:latest".',
      chatErrorCode: "model_not_configured",
      chatErrorStatus: 422,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(await screen.findByText("Nothing runnable yet")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Open Connections/i })).toBeTruthy();
    expect(screen.queryByText("Selected model is unavailable")).toBeNull();
    expect(screen.queryByText("422 · model_not_configured")).toBeNull();
    expect(screen.queryByText(/No routable provider reports model/)).toBeNull();
  });

  it("keeps model-required errors visible while pending tool calls hide the empty repair state", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
      providerScopedModels: [],
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
      pendingToolCalls: [{ id: "call_1", name: "lookup", arguments: "{}", result: "" }],
      chatError: "Choose a model before starting this chat.",
      chatErrorCode: "chat.model_required",
      chatErrorStatus: 400,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Model required")).toBeTruthy();
    expect(screen.getByText("400 · chat.model_required")).toBeTruthy();
    expect(screen.getByText("Choose a model before starting this chat.")).toBeTruthy();
    expect(screen.queryByText("No model provider configured")).toBeNull();
  });

  it("keeps model-required errors visible after the chat already has transcript context", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
      providerScopedModels: [],
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        agent_id: "hecate",
        execution_mode: "hecate_task",
        title: "Repo work",
        provider: "ollama",
        model: "ministral-3:latest",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          {
            id: "m1",
            role: "user",
            content: "show status",
            created_at: "2026-05-03T10:00:00Z",
          },
        ],
      } as any,
      chatError: "Choose a model before starting this chat.",
      chatErrorCode: "chat.model_required",
      chatErrorStatus: 400,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Model required")).toBeTruthy();
    expect(screen.getByText("400 · chat.model_required")).toBeTruthy();
    expect(screen.getByText("Choose a model before starting this chat.")).toBeTruthy();
  });

  it("renders operator guidance for stable gateway error codes", () => {
    const openTrace = vi.fn();
    const { state, actions } = setup({
      chatError: "Incorrect API key provided",
      chatErrorAction: "Rotate the provider key in Connections, then test readiness again.",
      chatErrorCode: "provider_auth_failed",
      chatErrorRequestID: "req_1234567890abcdef",
      chatErrorStatus: 502,
      chatErrorTraceID: "trace_abcdef1234567890",
    });
    render(withRuntimeConsole(<ChatView onOpenTrace={openTrace} />, { state, actions }));
    expect(screen.getByText("Provider credentials failed")).toBeTruthy();
    expect(screen.getByText("502 · provider_auth_failed")).toBeTruthy();
    expect(screen.getByText(/Rotate the provider key in Connections/)).toBeTruthy();
    expect(screen.getByText("req_123456")).toBeTruthy();
    expect(screen.getByText("trace_abcd")).toBeTruthy();
    screen.getByRole("button", { name: "Open trace" }).click();
    expect(openTrace).toHaveBeenCalledWith("req_1234567890abcdef");
  });

  it("renders workspace-required as guidance instead of a red error", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      chatError: "Choose a workspace before using Hecate Chat tools or External Agent.",
      chatErrorAction: "Choose a workspace, or turn tools off for direct model chat.",
      chatErrorCode: "chat.workspace_required",
      chatErrorStatus: 400,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const panel = screen.getByText("Choose a workspace").closest('[role="status"]');
    expect(panel).toBeTruthy();
    expectBefore(screen.getByLabelText("Message"), panel!);
    expect(panel).toHaveTextContent(
      "Choose a workspace before using Hecate Chat tools or External Agent.",
    );
    expect(panel).toHaveTextContent("Choose a workspace, or turn tools off for direct model chat.");
    expect(panel).not.toHaveTextContent("400");
    expect(panel).not.toHaveTextContent("chat.workspace_required");
  });
});

describe("ChatView session title", () => {
  it("shows the chat empty state without composer when no chat is selected", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      chatSessions: [],
      activeChatSessionID: "",
      activeChatSession: null,
      message: "",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    // New users land directly on the chat canvas with its empty
    // state, but the composer waits for a real session selection.
    // (Sidebar still shows "No chats yet" — that's a different surface.)
    expect(screen.queryByText(/Start your first .* chat from the sidebar/)).toBeNull();
    expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
  });

  it("shows a passive new-chat canvas when chat history exists but none is active", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      chatSessions: [
        {
          id: "s1",
          title: "Previous chat",
          message_count: 2,
          provider_call_count: 0,
          updated_at: "2026-05-18T00:00:00Z",
        } as any,
      ],
      activeChatSessionID: "",
      activeChatSession: null,
      message: "",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByText("No chat selected")).toBeNull();
    expect(screen.queryByText("New chat")).toBeNull();
    expect(screen.queryByRole("button", { name: "Chat settings" })).toBeNull();
    expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
  });

  it("does not show the session header for an unselected draft chat", async () => {
    const createChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        activeChatSessionID: "",
        activeChatSession: null,
        message: "",
        agentWorkspace: "/tmp/hecate",
      },
      { createChatSession },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /New Hecate chat/i }));

    expect(createChatSession).toHaveBeenCalled();
    expect(screen.queryByText("New chat")).toBeNull();
    expect(screen.queryByRole("button", { name: "Chat settings" })).toBeNull();
    expect(screen.queryByTitle("Choose workspace folder")).toBeNull();
  });

  it("shows a detached launch draft while creation is pending and blocks sending", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSessionID: "",
      activeChatSession: null,
      chatCreating: true,
      chatLoading: false,
      message: "Scoped project launch",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByText("Existing conversation")).toBeNull();
    expect(screen.getByRole("textbox", { name: "Message" })).toHaveValue("Scoped project launch");
    expect(screen.getByRole("textbox", { name: "Message" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Send message" })).toBeDisabled();
    expect(screen.getByRole("status", { name: "Starting chat" })).toHaveTextContent(
      "Starting chat…",
    );
    expect(screen.getByRole("status", { name: "Starting chat" })).not.toHaveAttribute("aria-busy");
    expect(screen.queryByRole("button", { name: "Queue message" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Stop active task" })).toBeNull();
    expect(screen.queryByText(/Hecate Chat is working/)).toBeNull();
  });

  it("does not project an unrelated pending creation as work in the selected chat", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSessionID: "chat_idle",
      activeChatSession: {
        id: "chat_idle",
        title: "Idle selected chat",
        agent_id: "hecate",
        status: "idle",
        messages: [],
        provider: "openai",
        model: "gpt-4o-mini",
      } as any,
      chatCreating: true,
      chatLoading: false,
      message: "",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByRole("status", { name: "Starting chat" })).toHaveTextContent(
      "Starting chat…",
    );
    expect(screen.queryByRole("button", { name: "Stop active task" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Queue message" })).toBeNull();
    expect(screen.queryByText(/Hecate Chat is working/)).toBeNull();
  });

  it("keeps the selected live turn visible while another chat is being prepared", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSessionID: "chat_running",
      activeChatSession: {
        id: "chat_running",
        title: "Running selected chat",
        agent_id: "hecate",
        status: "idle",
        messages: [],
        provider: "openai",
        model: "gpt-4o-mini",
      } as any,
      chatCreating: true,
      chatLoading: true,
      chatTurnActive: true,
      chatTurnSessionID: "chat_running",
      message: "Queue during preparation",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByRole("status", { name: "Starting chat" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Queue message" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Stop active task" })).toBeInTheDocument();
    expect(screen.getByText(/Hecate Chat is working/)).toBeInTheDocument();
  });

  it("queues for a selected idle chat while another session owns the live turn", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSessionID: "chat_next",
      activeChatSession: {
        id: "chat_next",
        title: "Next selected chat",
        agent_id: "hecate",
        status: "idle",
        messages: [],
        provider: "openai",
        model: "gpt-4o-mini",
      } as any,
      chatLoading: true,
      chatTurnActive: true,
      chatTurnSessionID: "chat_background",
      message: "Queue behind the other chat",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByRole("button", { name: "Queue message" })).toBeEnabled();
    expect(screen.getByText("Another chat is working. Messages here will queue.")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Stop active task" })).toBeNull();
    expect(screen.queryByText(/Hecate Chat is working/)).toBeNull();
  });

  it("keeps a detached draft blocked while another session owns the live turn", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSessionID: "",
      activeChatSession: null,
      chatLoading: true,
      chatTurnActive: true,
      chatTurnSessionID: "chat_background",
      message: "Keep this detached draft",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByRole("button", { name: "Send message" })).toBeDisabled();
    expect(
      screen.getByText("Another chat is working. This draft will stay here."),
    ).toBeInTheDocument();
  });

  it("locks the composer only while an implicit submit is allocating its session", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSessionID: "",
      activeChatSession: null,
      chatCreating: true,
      chatTurnActive: true,
      chatTurnSessionID: "",
      chatLoading: true,
      message: "",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const composer = screen.getByRole("textbox", { name: "Message" });
    composer.focus();
    expect(composer).toHaveAttribute("readonly");
    expect(composer).toHaveAttribute("aria-disabled", "true");
    expect(composer).toHaveFocus();
    expect(screen.getByLabelText("Hecate message controls")).toBeDisabled();
    expect(screen.getByRole("status", { name: "Starting chat" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Stop active task" })).toBeNull();
    expect(screen.queryByText(/Hecate Chat is working/)).toBeNull();
  });

  it("keeps an explicitly prepared draft editable while its route controls stay fixed", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSessionID: "",
      activeChatSession: null,
      chatCreating: true,
      chatTurnActive: false,
      chatTurnSessionID: "",
      chatLoading: false,
      message: "Editable prepared draft",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByRole("textbox", { name: "Message" })).not.toHaveAttribute("readonly");
    expect(screen.getByLabelText("Hecate message controls")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Send message" })).toBeDisabled();
  });

  it("allows an existing chat to queue while another turn is still allocating a session", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSessionID: "chat_selected_during_allocation",
      activeChatSession: {
        id: "chat_selected_during_allocation",
        title: "Selected during allocation",
        agent_id: "hecate",
        status: "idle",
        messages: [],
        provider: "openai",
        model: "gpt-4o-mini",
      } as any,
      chatCreating: true,
      chatTurnActive: true,
      chatTurnSessionID: "",
      chatLoading: true,
      message: "Queue in selected chat",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByRole("textbox", { name: "Message" })).not.toHaveAttribute("readonly");
    expect(screen.getByLabelText("Hecate message controls")).not.toBeDisabled();
    expect(screen.getByRole("button", { name: "Queue message" })).toBeEnabled();
    expect(screen.getByText("Another chat is working. Messages here will queue.")).toBeVisible();
  });

  it("freezes detached external-agent route controls during implicit allocation", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "grok_build",
      newChatAgentID: "grok_build",
      agentWorkspace: "/tmp/hecate",
      activeChatSessionID: "",
      activeChatSession: null,
      chatCreating: true,
      chatTurnActive: true,
      chatTurnSessionID: "",
      chatLoading: true,
      agentAdapters: [
        {
          id: "grok_build",
          name: "Grok Build",
          kind: "acp",
          command: "grok",
          available: true,
          status: "available",
          cost_mode: "external",
          config_options: [
            {
              id: "model",
              name: "Model",
              category: "model",
              type: "select",
              current_value: "grok-latest",
              options: [{ value: "grok-latest", name: "Grok Latest" }],
            },
          ],
        },
      ],
      message: "",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByLabelText("External agent message controls")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Model" })).toBeDisabled();
    expect(screen.getByRole("textbox", { name: "Message" })).toHaveAttribute("readonly");
  });

  it("offers a saved unsent message without replacing the current draft", async () => {
    const user = userEvent.setup();
    const restoreSavedComposerDraft = vi.fn(() => true);
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSessionID: "chat_with_saved_message",
      activeChatSession: {
        id: "chat_with_saved_message",
        title: "Chat with saved message",
        agent_id: "hecate",
        status: "idle",
        messages: [],
        provider: "openai",
        model: "gpt-4o-mini",
      } as any,
      savedComposerDraftsBySessionID: new Map([["chat_with_saved_message", ["Failed prompt A"]]]),
      message: "Current draft B",
    });
    actions.restoreSavedComposerDraft = restoreSavedComposerDraft;
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(
      screen.getByText(
        "An unsent message is saved for this chat. Restoring it keeps your current draft saved.",
      ),
    ).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Restore saved message" }));
    expect(restoreSavedComposerDraft).toHaveBeenCalledWith("chat_with_saved_message");
    expect(
      screen.getByText("Saved message restored. Your previous draft is still saved."),
    ).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Message" })).toHaveFocus());
  });

  it("keeps a detached launch draft editable after creation fails", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSessionID: "",
      activeChatSession: null,
      chatLoading: false,
      chatError: "Creation unavailable",
      message: "Edited scoped launch",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByRole("textbox", { name: "Message" })).toHaveValue("Edited scoped launch");
    expect(screen.getByRole("button", { name: "Send message" })).toBeEnabled();
  });

  it("withholds the composer while the selected chat record is still loading", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      activeChatSessionID: "chat_target",
      activeChatSession: {
        id: "chat_previous",
        title: "Previous chat",
        agent_id: "hecate",
        status: "idle",
        messages: [
          {
            id: "previous-message",
            role: "user",
            content: "Previous chat transcript must stay hidden",
            created_at: "2026-04-20T00:00:00Z",
          },
        ],
      } as any,
      message: "Target launch context",
      pendingToolCalls: [
        {
          id: "previous-tool",
          name: "previous_session_tool",
          arguments: "{}",
          result: "",
        },
      ],
    });

    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const loading = screen.getByRole("status", { name: "Loading selected chat" });
    expect(loading).toHaveAttribute("aria-busy", "true");
    expect(loading).toHaveAttribute("aria-live", "polite");
    expect(within(loading).getByText("Loading chat…")).toBeTruthy();
    expect(screen.queryByText("Previous chat")).toBeNull();
    expect(screen.queryByText("Previous chat transcript must stay hidden")).toBeNull();
    expect(screen.queryByText("previous_session_tool")).toBeNull();
    expect(screen.queryByText("Ready when you are")).toBeNull();
    expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Send message" })).toBeNull();
  });

  it("shows the active session's title", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSession: {
        id: "s1",
        title: "Hello world",
        messages: [],
        provider_calls: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText("Hello world")).toBeTruthy();
  });

  it("opens the linked project from the chat header", async () => {
    const selectProject = vi.fn(async () => undefined);
    const onNavigate = vi.fn();
    const project: ProjectRecord = {
      id: "proj_1",
      name: "Hecate",
      roots: [],
      created_at: "2026-05-29T00:00:00Z",
      updated_at: "2026-05-29T00:00:00Z",
    };
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        projects: [project],
        activeChatSession: {
          id: "s1",
          agent_id: "hecate",
          title: "Project chat",
          project_id: "proj_1",
          messages: [],
          provider_calls: [],
        } as any,
      },
      { selectProject },
    );
    render(withRuntimeConsole(<ChatView onNavigate={onNavigate} />, { state, actions }));

    await userEvent.click(screen.getByRole("button", { name: "Open project: Hecate" }));

    expect(selectProject).toHaveBeenCalledWith("proj_1");
    expect(onNavigate).toHaveBeenCalledWith("projects");
  });

  it("drafts a Project Assistant proposal from a project-linked Hecate chat message", async () => {
    const selectProject = vi.fn(async () => undefined);
    const setMessage = vi.fn();
    const onNavigate = vi.fn();
    const project: ProjectRecord = {
      id: "proj_1",
      name: "Hecate",
      roots: [],
      created_at: "2026-05-29T00:00:00Z",
      updated_at: "2026-05-29T00:00:00Z",
    };
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        message: "Plan next project work",
        projects: [project],
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          agent_id: "hecate",
          title: "Project chat",
          project_id: "proj_1",
          provider: "openai",
          model: "gpt-4o-mini",
          status: "idle",
          workspace: "/tmp/hecate",
          messages: [],
          provider_calls: [],
        } as any,
      },
      { selectProject, setMessage },
    );
    render(withRuntimeConsole(<ChatView onNavigate={onNavigate} />, { state, actions }));

    const user = userEvent.setup();
    await user.click(
      screen.getByRole("button", { name: "Draft Project Assistant proposal from message" }),
    );

    await waitFor(() => {
      expect(draftChatProjectAssistant).toHaveBeenCalledWith("s1", {
        request: "Plan next project work",
      });
    });
    expect(selectProject).toHaveBeenCalledWith("proj_1");
    expect(onNavigate).toHaveBeenCalledWith("projects");
    expect(setMessage).toHaveBeenCalledWith("");
    expect(readProjectAssistantChatHandoff()).toMatchObject({
      project_id: "proj_1",
      request: "Plan next project work",
      source_session_id: "s1",
      proposal: { id: "pa_chat" },
    });
  });

  it("opens a Project Assistant proposal artifact from the chat transcript", async () => {
    const selectProject = vi.fn(async () => undefined);
    const onNavigate = vi.fn();
    const project: ProjectRecord = {
      id: "proj_1",
      name: "Hecate",
      roots: [],
      created_at: "2026-05-29T00:00:00Z",
      updated_at: "2026-05-29T00:00:00Z",
    };
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        projects: [project],
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          agent_id: "hecate",
          title: "Project chat",
          project_id: "proj_1",
          provider: "openai",
          model: "gpt-4o-mini",
          status: "idle",
          workspace: "/tmp/hecate",
          messages: [
            {
              id: "m_user",
              role: "user",
              content: "Plan next project work",
              created_at: "2026-05-29T10:00:00Z",
            },
            {
              id: "m_assistant",
              role: "assistant",
              content: "I drafted a proposal for review in Projects.",
              created_at: "2026-05-29T10:00:02Z",
              execution_mode: "hecate_task",
              task_id: "task_1",
              run_id: "run_1",
              activities: [
                {
                  id: "activity_proposal",
                  type: "project_assistant_proposal",
                  kind: "project_assistant_proposal",
                  status: "ready",
                  title: "Project Assistant proposal",
                  detail: "Plan next project work - 1 action - ready for review",
                  artifact_id: "artifact_project_proposal",
                },
              ],
            },
          ],
          provider_calls: [],
        } as any,
      },
      { selectProject },
    );
    render(withRuntimeConsole(<ChatView onNavigate={onNavigate} />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByText(/1 proposal ready/));
    await user.click(screen.getByText("Proposal"));
    await user.click(screen.getByRole("button", { name: "Review in Projects" }));

    await waitFor(() => {
      expect(getTaskRunArtifact).toHaveBeenCalledWith(
        "task_1",
        "run_1",
        "artifact_project_proposal",
      );
    });
    expect(selectProject).toHaveBeenCalledWith("proj_1");
    expect(onNavigate).toHaveBeenCalledWith("projects");
    expect(readProjectAssistantChatHandoff()).toMatchObject({
      project_id: "proj_1",
      request: "Plan next project work",
      source_session_id: "s1",
      proposal: { id: "pa_artifact" },
    });
  });

  it("does not show the project shortcut for unprojected chats", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      activeChatSession: {
        id: "s1",
        agent_id: "hecate",
        title: "Unprojected chat",
        messages: [],
        provider_calls: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView onNavigate={() => undefined} />, { state, actions }));

    expect(screen.queryByRole("button", { name: /Open project/i })).toBeNull();
  });

  it("shows the active chat runtime identity below the title", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      providerFilter: "ollama",
      model: "qwen2.5-coder",
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "ollama",
            name: "Ollama",
            preset_id: "ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: false,
          },
        ],
        policy_rules: [],
        events: [],
      },
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/Users/alice/dev/hecate",
        status: "completed",
        messages: [],
      } as any,
    });

    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Repo work")).toBeTruthy();
    expect(screen.getByText("Tools on · /Users/alice/dev/hecate")).toBeTruthy();
  });
});

describe("ChatView New chat button", () => {
  it("focuses the message textarea after clicking New chat", async () => {
    // The button starts a fresh chat; the operator's next move
    // is almost always to type. Auto-focusing the textarea saves a
    // click and matches the muscle-memory pattern from chat clients.
    const createChatSession = vi.fn();
    const { state, actions } = setup(
      { activeChatSessionID: "", activeChatSession: null },
      { createChatSession },
    );
    const user = userEvent.setup();
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));
    await user.click(screen.getByRole("button", { name: /new .* chat/i }));
    expect(createChatSession).toHaveBeenCalled();
    const nextState = setup({
      ...state,
      activeChatSessionID: "chat_new",
      activeChatSession: {
        id: "chat_new",
        agent_id: "hecate",
        execution_mode: "hecate_task",
        tools_enabled: false,
        title: "New chat",
        provider: "openai",
        model: "gpt-4o-mini",
        capabilities: { tool_calling: "basic" },
        status: "idle",
        messages: [],
      } as any,
    }).state;
    rerender(withRuntimeConsole(<ChatView />, { state: nextState, actions }));
    const textarea = await screen.findByPlaceholderText(/^Message…/i);
    await waitFor(() => expect(document.activeElement).toBe(textarea));
  });
});

describe("ChatView session focus", () => {
  it("focuses the message textarea when a sidebar chat row is clicked", async () => {
    // Focus is applied on EXPLICIT user actions only — the New-chat
    // button onClick and chat-row onClick. The activeChatSessionID
    // effect deliberately does NOT focus, because data-load (chats
    // arriving from the API) also drives that transition and stealing
    // focus on load would hijack normal page navigation.
    const selectChatSession = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        chatSessions: [
          { id: "s2", title: "Pick me", message_count: 0, provider_call_count: 0 } as any,
        ],
      },
      { selectChatSession },
    );
    const user = userEvent.setup();
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));
    // Move focus elsewhere to detect the jump.
    const searchInput = screen.getByRole("textbox", { name: "Search chats" });
    searchInput.focus();
    expect(document.activeElement).toBe(searchInput);
    // Click the chat row — the only user-driven chat switch.
    await user.click(screen.getByText("Pick me"));
    const nextState = setup({
      ...state,
      activeChatSessionID: "s2",
      activeChatSession: { id: "s2", title: "Pick me", messages: [], provider_calls: [] } as any,
    }).state;
    rerender(withRuntimeConsole(<ChatView />, { state: nextState, actions }));
    const textarea = screen.getByPlaceholderText(/^Message…/i);
    await waitFor(() => expect(document.activeElement).toBe(textarea));
    expect(selectChatSession).toHaveBeenCalledWith("s2");
  });

  it("does NOT focus the textarea when activeChatSessionID changes from data-load", async () => {
    // Initial-load and API-driven session arrivals must not steal
    // focus — page-level shortcuts depend on it. Asserts the negative.
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      activeChatSessionID: "",
    });
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));
    const searchInput = screen.getByRole("textbox", { name: "Search chats" });
    searchInput.focus();
    const next = { ...state, activeChatSessionID: "s1" };
    rerender(withRuntimeConsole(<ChatView />, { state: next, actions }));
    // Focus must STAY on the search input — the effect should not have
    // jumped to the textarea on a programmatic ID transition.
    expect(document.activeElement).toBe(searchInput);
  });
});

describe("ChatView history pagination", () => {
  it("does not show the legacy model-history pagination action for unified Hecate Chat", () => {
    const loadMoreChatSessions = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        chatSessionsHasMore: true,
        chatSessions: [
          { id: "s1", title: "First page", message_count: 1, provider_call_count: 1 } as any,
        ],
      },
      { loadMoreChatSessions },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByRole("button", { name: "Load earlier chats" })).toBeNull();
    expect(loadMoreChatSessions).not.toHaveBeenCalled();
  });

  it("does not show the legacy search pagination action for unified Hecate Chat", async () => {
    const loadMoreChatSessions = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        defaultChatToolsEnabled: false,
        chatSessionsHasMore: true,
        chatSessions: [
          { id: "s1", title: "First page", message_count: 1, provider_call_count: 1 } as any,
        ],
      },
      { loadMoreChatSessions },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.type(screen.getByRole("textbox", { name: "Search chats" }), "older match");
    expect(screen.queryByRole("button", { name: "Search earlier chats" })).toBeNull();
    expect(loadMoreChatSessions).not.toHaveBeenCalled();
  });
});

// External-agent approval surfaces in the Chats workspace. These tests
// confirm the banner / modal wiring; the component-level behavior
// (overflow stack, broad-scope confirm) is covered in
// AgentApprovalBanner.test.tsx and AgentApprovalModal.test.tsx.
describe("ChatView agent approvals", () => {
  it("renders the auto-mode danger banner when the gateway runs in auto", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterApprovalMode: "auto",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByTestId("agent-approval-auto-banner")).toBeTruthy();
  });

  it("does not render the auto-mode banner when in prompt mode", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterApprovalMode: "prompt",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.queryByTestId("agent-approval-auto-banner")).toBeNull();
  });

  it("hides the auto-mode banner when in model chat target (it's an agent-only concern)", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      defaultChatToolsEnabled: false,
      agentAdapterApprovalMode: "auto",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.queryByTestId("agent-approval-auto-banner")).toBeNull();
  });

  it("renders the pending banner with rows scoped to the active session and opens the modal on Review", async () => {
    const sessionID = "a1";
    const pending = new Map<string, any>([
      [
        sessionID,
        [
          {
            approval_id: "ap-1",
            session_id: sessionID,
            adapter_id: "codex",
            tool_kind: "fs",
            tool_name: "write_file",
            created_at: "2026-04-21T10:00:00Z",
            expires_at: "2026-04-21T10:05:00Z",
          },
        ],
      ],
      [
        "other-session",
        [
          {
            approval_id: "ap-2",
            session_id: "other-session",
            adapter_id: "codex",
            tool_kind: "exec",
            created_at: "2026-04-21T10:00:00Z",
            expires_at: "2026-04-21T10:05:00Z",
          },
        ],
      ],
    ]);
    const getChatApproval = vi.fn(async () => null); // modal opens, fetch returns null → renders error
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        activeChatSessionID: sessionID,
        activeChatSession: {
          id: sessionID,
          title: "S1",
          agent_id: "codex",
          workspace: "/tmp",
          status: "running",
        } as any,
        pendingApprovalsBySessionID: pending,
        chatSessions: [
          {
            id: sessionID,
            title: "S1",
            agent_id: "codex",
            status: "running",
            message_count: 0,
          } as any,
        ],
      },
      { getChatApproval },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    // Only the active session's pending row is visible — banner must
    // not bleed approvals from other sessions.
    const reviews = screen.getAllByTestId("agent-approval-banner-review");
    expect(reviews).toHaveLength(1);

    const user = userEvent.setup();
    await user.click(reviews[0]!);
    // The modal mounts and asks for the full row.
    expect(getChatApproval).toHaveBeenCalledWith(sessionID, "ap-1");
  });

  it("does not carry an external approval modal into Hecate Chat", async () => {
    const sessionID = "external-approval-session";
    const pending = new Map<string, any>([
      [
        sessionID,
        [
          {
            approval_id: "ap-external",
            session_id: sessionID,
            adapter_id: "codex",
            tool_kind: "fs",
            tool_name: "write_file",
            created_at: "2026-04-21T10:00:00Z",
            expires_at: "2026-04-21T10:05:00Z",
          },
        ],
      ],
    ]);
    const getChatApproval = vi.fn(async () => null);
    const { state: externalState, actions } = setup(
      {
        chatTarget: "external_agent",
        activeChatSessionID: sessionID,
        activeChatSession: {
          id: sessionID,
          title: "Codex",
          agent_id: "codex",
          workspace: "/tmp",
          status: "running",
        } as any,
        pendingApprovalsBySessionID: pending,
        chatSessions: [
          {
            id: sessionID,
            title: "Codex",
            agent_id: "codex",
            status: "running",
            message_count: 0,
          } as any,
        ],
      },
      { getChatApproval },
    );
    const view = render(withRuntimeConsole(<ChatView />, { state: externalState, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByTestId("agent-approval-banner-review"));
    await waitFor(() => expect(getChatApproval).toHaveBeenCalledWith(sessionID, "ap-external"));

    const { state: hecateState } = setup(
      {
        chatTarget: "agent",
        activeChatSessionID: "hecate-session",
        activeChatSession: {
          id: "hecate-session",
          title: "Hecate",
          execution_mode: "hecate_task",
          workspace: "/tmp",
          status: "completed",
        } as any,
        pendingApprovalsBySessionID: pending,
      },
      { getChatApproval },
    );
    view.rerender(withRuntimeConsole(<ChatView />, { state: hecateState, actions }));

    expect(getChatApproval).toHaveBeenCalledTimes(1);
  });
});

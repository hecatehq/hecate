import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ChatSettingsPanel } from "./ChatSettingsPanel";

const baseProps = {
  showHecateControls: false,
  toolsEnabled: false,
  toolsDisabledForModel: false,
  rtkEnabled: false,
  rtkAvailable: false,
  rtkPath: "",
  workspaceMode: "persistent" as const,
  workspaceModeLocked: false,
  workspaceModeStartedFromProject: false,
  workspaceModePending: false,
  externalAgentID: "codex",
  taskID: "",
  agentName: "Codex",
  model: "",
  provider: "",
  workspace: "/tmp/hecate",
  status: "idle",
  messageCount: 0,
  contextSummary: undefined,
  agentUsage: null,
  usageSource: "adapter" as const,
  instructionsAvailable: false,
  isHecateAgentChat: false,
  instructionsLocked: false,
  systemPrompt: "",
  onToolsChange: vi.fn(),
  onRTKChange: vi.fn(),
  onWorkspaceModeChange: vi.fn(),
  onConfigOptionChange: vi.fn(async () => true),
  onSystemPromptChange: vi.fn(),
  onCopyCommand: vi.fn(),
};

describe("ChatSettingsPanel external-agent MCP servers", () => {
  it("renders adapter-reported implementation metadata", () => {
    render(
      <ChatSettingsPanel
        {...baseProps}
        externalSession={{
          id: "chat_1",
          title: "Codex chat",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "idle",
          agent_info: {
            name: "codex-acp-adapter",
            title: "Codex ACP Adapter",
            version: "0.1.0-alpha.28",
          },
        }}
      />,
    );

    expect(screen.getByText("Implementation")).toBeTruthy();
    expect(screen.getByText("Codex ACP Adapter 0.1.0-alpha.28")).toBeTruthy();
  });

  it("renders configured stdio and HTTP MCP servers", () => {
    render(
      <ChatSettingsPanel
        {...baseProps}
        externalSession={{
          id: "chat_1",
          title: "Codex chat",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "idle",
          mcp_servers: [
            {
              name: "filesystem",
              command: "mcp-fs",
              args: ["--root", "/tmp/hecate"],
              env: { TOKEN: "$MCP_TOKEN" },
            },
            {
              name: "remote",
              url: "https://mcp.example.com/mcp",
              headers: { Authorization: "Bearer $MCP_TOKEN" },
            },
          ],
        }}
      />,
    );

    expect(screen.getByText("MCP servers")).toBeTruthy();
    expect(screen.getByText("filesystem")).toBeTruthy();
    expect(screen.getByText("mcp-fs --root /tmp/hecate")).toBeTruthy();
    expect(screen.getByText("TOKEN")).toBeTruthy();
    expect(screen.getByText("remote")).toBeTruthy();
    expect(screen.getByText("https://mcp.example.com/mcp")).toBeTruthy();
    expect(screen.getByText("Authorization")).toBeTruthy();
  });
});

describe("ChatSettingsPanel Hecate workspace execution", () => {
  it("explains when chat messages create or continue a linked Task", () => {
    const { rerender } = render(
      <ChatSettingsPanel
        {...baseProps}
        showHecateControls
        toolsEnabled
        usageSource="hecate"
        externalSession={null}
      />,
    );

    expect(
      screen.getByText(
        "Create or continue a linked Task with tools, approvals, artifacts, and sandboxed tool calls.",
      ),
    ).toBeTruthy();

    rerender(
      <ChatSettingsPanel
        {...baseProps}
        showHecateControls
        toolsEnabled={false}
        usageSource="hecate"
        externalSession={null}
      />,
    );

    expect(
      screen.getByText(
        "Send the next message directly to the selected provider/model. This does not create a Task or use local tools.",
      ),
    ).toBeTruthy();
  });

  it("shows the frozen preset and prevents tools from being re-enabled when it is direct-chat only", () => {
    render(
      <ChatSettingsPanel
        {...baseProps}
        showHecateControls
        toolsEnabled={false}
        usageSource="hecate"
        externalSession={null}
        agentPreset={{
          id: "chat_review",
          name: "Chat review",
          execution_profile: "review",
          tools_enabled: false,
          writes_allowed: false,
          network_allowed: false,
        }}
      />,
    );

    expect(screen.getByText("Agent preset")).toBeTruthy();
    expect(screen.getByText("Chat review")).toBeTruthy();
    expect(screen.getByText(/Frozen when this chat was created/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Tools off" })).toBeDisabled();
    expect(screen.getByText(/frozen Agent Preset disables local tools/i)).toBeTruthy();
  });

  it("explains isolated execution and changes the workspace mode", () => {
    const onWorkspaceModeChange = vi.fn();
    render(
      <ChatSettingsPanel
        {...baseProps}
        showHecateControls
        workspaceMode="persistent"
        usageSource="hecate"
        onWorkspaceModeChange={onWorkspaceModeChange}
        externalSession={null}
      />,
    );

    const select = screen.getByRole("combobox", { name: "Workspace mode" });
    expect(select).toHaveValue("persistent");
    expect(screen.getAllByRole("option")).toHaveLength(2);
    expect(screen.getByText(/selected source folder stays untouched/i)).toBeTruthy();

    fireEvent.change(select, { target: { value: "in_place" } });
    expect(onWorkspaceModeChange).toHaveBeenCalledWith("in_place");
  });

  it("locks the execution posture after a task-backed turn", () => {
    render(
      <ChatSettingsPanel
        {...baseProps}
        showHecateControls
        workspaceMode="in_place"
        workspaceModeLocked
        usageSource="hecate"
        externalSession={null}
      />,
    );

    const select = screen.getByRole("combobox", { name: "Workspace mode" });
    expect(select).toBeDisabled();
    expect(select).toHaveAccessibleDescription(/locked after the first task-backed turn/i);
    expect(screen.getByText(/locked after the first task-backed turn/i)).toBeTruthy();
    expect(screen.getByText(/tools write directly/i)).toBeTruthy();
  });

  it("keeps a linked Project default editable until task work starts", () => {
    render(
      <ChatSettingsPanel
        {...baseProps}
        showHecateControls
        workspaceModeStartedFromProject
        usageSource="hecate"
        externalSession={null}
      />,
    );

    const select = screen.getByRole("combobox", { name: "Workspace mode" });
    expect(select).toBeEnabled();
    expect(select).toHaveAccessibleDescription(/started from the linked project default/i);
  });

  it("renders legacy ephemeral intent as the single Managed product choice", () => {
    render(
      <ChatSettingsPanel
        {...baseProps}
        showHecateControls
        workspaceMode="ephemeral"
        usageSource="hecate"
        externalSession={null}
      />,
    );

    const select = screen.getByRole("combobox", { name: "Workspace mode" });
    expect(select).toHaveValue("persistent");
    expect(select).toHaveAccessibleDescription(/retains ephemeral intent/i);
    expect(screen.queryByRole("option", { name: /ephemeral/i })).toBeNull();
  });

  it("disables workspace execution while the requested mode is pending", () => {
    render(
      <ChatSettingsPanel
        {...baseProps}
        showHecateControls
        workspaceMode="in_place"
        workspaceModePending
        mutationsDisabled
        usageSource="hecate"
        externalSession={null}
      />,
    );

    const select = screen.getByRole("combobox", { name: "Workspace mode" });
    expect(select).toBeDisabled();
    expect(select).toHaveValue("in_place");
    expect(select).toHaveAccessibleDescription(/sending is paused/i);
    expect(screen.getByRole("status")).toHaveTextContent(/saving workspace execution/i);
    expect(screen.getByRole("button", { name: "Tools off" })).toBeDisabled();
  });
});

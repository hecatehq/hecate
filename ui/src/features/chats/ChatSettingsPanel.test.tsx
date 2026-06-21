import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ChatSettingsPanel } from "./ChatSettingsPanel";

const baseProps = {
  showHecateControls: false,
  toolsEnabled: false,
  toolsDisabledForModel: false,
  rtkEnabled: false,
  rtkAvailable: false,
  rtkPath: "",
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

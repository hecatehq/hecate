import { describe, expect, it } from "vitest";

import {
  agentApprovalScopeDescription,
  agentApprovalScopeLabel,
  agentApprovalToolKindLabel,
  agentApprovalToolLabel,
} from "./agent-approval-labels";

describe("agent approval labels", () => {
  it("humanizes known tool kinds and leaves unknown kinds readable", () => {
    expect(agentApprovalToolKindLabel("file_write")).toBe("file write");
    expect(agentApprovalToolKindLabel("shell_exec")).toBe("shell command");
    expect(agentApprovalToolKindLabel("mcp")).toBe("MCP tool");
    expect(agentApprovalToolKindLabel("custom_tool")).toBe("custom tool");
  });

  it("combines the normalized kind label with the adapter tool name", () => {
    expect(agentApprovalToolLabel({ tool_kind: "mcp", tool_name: "docs/search" })).toBe(
      "MCP tool · docs/search",
    );
    expect(agentApprovalToolLabel({ tool_kind: "file_write", tool_name: "apply_patch" })).toBe(
      "file write · apply_patch",
    );
  });

  it("names broad MCP scopes as MCP-wide grants", () => {
    expect(agentApprovalScopeLabel("workspace_tool", "mcp")).toBe("workspace MCP tools");
    expect(agentApprovalScopeLabel("adapter_tool", "mcp")).toBe("agent MCP tools");
    expect(agentApprovalScopeDescription("adapter_tool", "mcp")).toContain(
      "Every future MCP tool request",
    );
  });
});

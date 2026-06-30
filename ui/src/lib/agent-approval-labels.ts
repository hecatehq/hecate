import { agentTerminalToolTitle, isAgentTerminalToolName } from "./agent-terminal-tools";

type ApprovalToolLike = {
  tool_kind?: string;
  tool_name?: string;
};

const TOOL_KIND_LABELS: Record<string, string> = {
  file_write: "file write",
  file_read: "file read",
  shell_exec: "shell command",
  network: "network request",
  http_request: "network request",
  web_search: "web search",
  file_move: "file move",
  file_delete: "file delete",
  search: "search",
  think: "thinking",
  terminal: "terminal tool",
  mcp: "MCP tool",
  other: "other tool",
};

export function agentApprovalToolKindLabel(toolKind?: string): string {
  const trimmed = (toolKind ?? "").trim();
  if (!trimmed) return "tool";
  if (isAgentTerminalToolName(trimmed)) return "terminal tool";
  return TOOL_KIND_LABELS[trimmed] ?? trimmed.replaceAll("_", " ");
}

export function agentApprovalToolLabel(item: ApprovalToolLike): string {
  const kindLabel = agentApprovalToolKindLabel(item.tool_kind);
  const toolName = item.tool_name?.trim();
  const terminalToolLabel = agentTerminalToolTitle(toolName);
  if (terminalToolLabel) return `terminal tool · ${terminalToolLabel}`;
  return toolName ? `${kindLabel} · ${toolName}` : kindLabel;
}

export function agentApprovalScopeLabel(scope: string, toolKind?: string): string {
  switch (scope) {
    case "workspace_tool":
      return toolKind === "mcp" ? "workspace MCP tools" : "workspace tool kind";
    case "adapter_tool":
      return toolKind === "mcp" ? "agent MCP tools" : "agent tool kind";
    default:
      return scope.replaceAll("_", " ");
  }
}

export function agentApprovalScopeDescription(scope: string, toolKind?: string): string {
  const requestKind = agentApprovalToolKindLabel(toolKind);
  switch (scope) {
    case "once":
      return "Only this pending request will use the selected ACP option.";
    case "session":
      return `Matching ${requestKind} requests in this external-agent chat session can reuse this decision.`;
    case "workspace_tool":
      return `Matching ${requestKind} requests in this workspace can reuse this decision.`;
    case "adapter_tool":
      return `Every future ${requestKind} request from this agent can reuse this decision.`;
    default:
      return "";
  }
}

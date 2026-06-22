const TERMINAL_TOOL_LABELS = {
  terminal_open: {
    operation: "open",
    rowTitle: "Terminal open",
    activityTitle: "Opened terminal",
  },
  terminal_write: {
    operation: "write",
    rowTitle: "Terminal write",
    activityTitle: "Wrote to terminal",
  },
  terminal_read: {
    operation: "read",
    rowTitle: "Terminal read",
    activityTitle: "Read terminal",
  },
  terminal_wait: {
    operation: "wait",
    rowTitle: "Terminal wait",
    activityTitle: "Waited for terminal",
  },
  terminal_kill: {
    operation: "kill",
    rowTitle: "Terminal kill",
    activityTitle: "Killed terminal",
  },
} as const;

export type AgentTerminalToolName = keyof typeof TERMINAL_TOOL_LABELS;

const TERMINAL_TOOL_NAMES = new Set<string>(Object.keys(TERMINAL_TOOL_LABELS));
const TERMINAL_TOOL_MENTION_RE = /\bterminal_(?:open|write|read|wait|kill)\b/i;

export function isAgentTerminalToolName(value?: string): value is AgentTerminalToolName {
  return TERMINAL_TOOL_NAMES.has((value ?? "").trim());
}

export function agentTerminalToolOperation(value?: string): string | undefined {
  const name = (value ?? "").trim();
  return isAgentTerminalToolName(name) ? TERMINAL_TOOL_LABELS[name].operation : undefined;
}

export function agentTerminalToolTitle(value?: string): string | undefined {
  const name = (value ?? "").trim();
  return isAgentTerminalToolName(name) ? TERMINAL_TOOL_LABELS[name].rowTitle : undefined;
}

export function agentTerminalToolActivityTitle(value?: string): string | undefined {
  const name = (value ?? "").trim();
  return isAgentTerminalToolName(name) ? TERMINAL_TOOL_LABELS[name].activityTitle : undefined;
}

export function hasAgentTerminalToolMention(value?: string): boolean {
  return TERMINAL_TOOL_MENTION_RE.test(value ?? "");
}

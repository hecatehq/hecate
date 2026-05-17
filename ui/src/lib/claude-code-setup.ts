import type { AgentAdapterSetupCommandStatus } from "../types/agent-adapter";

export function claudeCodeSetupTokenCommand(status?: AgentAdapterSetupCommandStatus): string {
  return `${claudeCodeCommandPrefix(status)} setup-token`;
}

function claudeCodeCommandPrefix(status?: AgentAdapterSetupCommandStatus): string {
  if (!status?.available) {
    return "npx -y @anthropic-ai/claude-code";
  }

  const command = status.command?.trim();
  if (!command) {
    return "claude";
  }

  const executable = firstCommandToken(command);
  const binary = executable.split(/[\\/]/).pop()?.toLowerCase();

  if (binary === "claude") {
    return "claude";
  }

  if (binary === "npx") {
    return "npx -y @anthropic-ai/claude-code";
  }

  return command;
}

function firstCommandToken(command: string): string {
  return command.split(/\s+/)[0] ?? command;
}

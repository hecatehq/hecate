import { describe, expect, it } from "vitest";

import {
  agentTerminalToolActivityTitle,
  agentTerminalToolOperation,
  agentTerminalToolTitle,
  hasAgentTerminalToolMention,
  isAgentTerminalToolName,
} from "./agent-terminal-tools";

describe("agent terminal tools", () => {
  it("recognizes and labels native terminal tool names", () => {
    expect(isAgentTerminalToolName("terminal_open")).toBe(true);
    expect(agentTerminalToolOperation("terminal_open")).toBe("open");
    expect(agentTerminalToolTitle("terminal_open")).toBe("Terminal open");
    expect(agentTerminalToolActivityTitle("terminal_wait")).toBe("Waited for terminal");
  });

  it("finds terminal tool mentions in approval text", () => {
    expect(
      hasAgentTerminalToolMention(
        "Agent requested tools that require approval: terminal_write - awaiting_approval",
      ),
    ).toBe(true);
    expect(hasAgentTerminalToolMention("shell_exec - awaiting_approval")).toBe(false);
    expect(hasAgentTerminalToolMention("terminal_opened is not a tool name")).toBe(false);
  });
});

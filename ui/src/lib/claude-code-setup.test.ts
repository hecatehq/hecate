import { describe, expect, it } from "vitest";
import { claudeCodeSetupTokenCommand } from "./claude-code-setup";

describe("claudeCodeSetupTokenCommand", () => {
  it("uses the global claude command when lookup found a claude binary", () => {
    expect(claudeCodeSetupTokenCommand({
      available: true,
      command: "/Users/example/.volta/bin/claude",
      executable_path: "/Users/example/.volta/bin/claude",
    })).toBe("claude setup-token");
  });

  it("uses the global npx command when only the npx fallback is available", () => {
    expect(claudeCodeSetupTokenCommand({
      available: true,
      command: "/opt/homebrew/bin/npx -y @anthropic-ai/claude-code",
      executable_path: "/opt/homebrew/bin/npx",
    })).toBe("npx -y @anthropic-ai/claude-code setup-token");
  });

  it("keeps a custom command path when no standard launcher was detected", () => {
    expect(claudeCodeSetupTokenCommand({
      available: true,
      command: "/opt/hecate/bin/claude-code-wrapper",
      executable_path: "/opt/hecate/bin/claude-code-wrapper",
    })).toBe("/opt/hecate/bin/claude-code-wrapper setup-token");
  });
});

import { describe, expect, it } from "vitest";

import type { ChatSessionRecord } from "../../types/chat";
import { pendingHecateTaskApprovals } from "./HecateTaskApprovalsBanner";

function session(overrides: Partial<ChatSessionRecord> = {}): ChatSessionRecord {
  return {
    id: "chat-1",
    title: "Task-backed chat",
    task_id: "task-1",
    latest_run_id: "run-1",
    workspace: "/workspace",
    status: "awaiting_approval",
    messages: [],
    ...overrides,
  };
}

describe("pendingHecateTaskApprovals", () => {
  it("classifies native terminal tool approval reasons as terminal tools", () => {
    const approvals = pendingHecateTaskApprovals(
      session({
        messages: [
          {
            id: "msg-1",
            role: "assistant",
            content: "",
            activities: [
              {
                id: "task:approval:ap-1",
                type: "approval",
                title: "agent_loop_tool_call",
                kind: "agent_loop_approval",
                status: "awaiting_approval",
                needs_action: true,
                detail:
                  "Agent requested tools that require approval: terminal_open - awaiting_approval",
              },
            ],
          },
        ],
      }),
    );

    expect(approvals).toHaveLength(1);
    expect(approvals[0].kind).toBe("terminal_tool");
    expect(approvals[0].detail).toBe("terminal_open");
  });

  it("classifies native web search approval reasons as network egress", () => {
    const approvals = pendingHecateTaskApprovals(
      session({
        messages: [
          {
            id: "msg-1",
            role: "assistant",
            content: "",
            activities: [
              {
                id: "task:approval:ap-1",
                type: "approval",
                title: "agent_loop_tool_call",
                kind: "agent_loop_approval",
                status: "awaiting_approval",
                needs_action: true,
                detail:
                  "Agent requested tools that require approval: web_search - awaiting_approval",
              },
            ],
          },
        ],
      }),
    );

    expect(approvals).toHaveLength(1);
    expect(approvals[0].kind).toBe("network_egress");
    expect(approvals[0].detail).toBe("web_search");
  });
});

import { createElement } from "react";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { ChatSessionRecord } from "../../types/chat";
import { HecateTaskApprovalsBanner, pendingHecateTaskApprovals } from "./HecateTaskApprovalsBanner";

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
                action_summary: [
                  "terminal_open command details withheld (command_bytes=18)",
                  "file_write write path=out.txt content_bytes=2",
                ],
                action_summary_incomplete: true,
              },
            ],
          },
        ],
      }),
    );

    expect(approvals).toHaveLength(1);
    expect(approvals[0].kind).toBe("terminal_tool");
    expect(approvals[0].detail).toBe("terminal_open");
    expect(approvals[0].actionSummary).toEqual([
      "terminal_open command details withheld (command_bytes=18)",
      "file_write write path=out.txt content_bytes=2",
    ]);
    expect(approvals[0].actionSummaryIncomplete).toBe(true);
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

describe("HecateTaskApprovalsBanner", () => {
  it("shows the ordered action bundle and blocks approval when the safe summary is incomplete", () => {
    render(
      createElement(HecateTaskApprovalsBanner, {
        approvals: [
          {
            approvalID: "ap-1",
            title: "agent_loop_tool_call",
            kind: "shell_command",
            actionSummary: [
              "shell_exec command details withheld (command_bytes=9)",
              "file_write write path=out.txt content_bytes=2",
            ],
            actionSummaryIncomplete: true,
          },
        ],
        taskID: "task-1",
        busyID: "",
        onResolve: vi.fn(),
      }),
    );

    const actions = screen.getByRole("list", { name: "Pending actions" });
    expect(within(actions).getAllByRole("listitem")).toHaveLength(2);
    expect(within(actions).getByText("file_write write path=out.txt content_bytes=2")).toBeTruthy();
    expect(
      screen.getByText(/calls or details were omitted or could not be summarized safely/i),
    ).toBeTruthy();
    const reviewRegion = screen.getByRole("region", {
      name: /Review pending actions for Shell execution/i,
    });
    expect(reviewRegion.style.maxHeight).toBe("144px");
    expect(reviewRegion.style.overflowY).toBe("auto");
    expect(screen.getByRole("button", { name: /Approve Shell execution/i })).toBeDisabled();
  });

  it("enables inline approval when every pending action has a reviewable summary", () => {
    render(
      createElement(HecateTaskApprovalsBanner, {
        approvals: [
          {
            approvalID: "ap-complete",
            title: "agent_loop_tool_call",
            kind: "shell_command",
            actionSummary: ["shell_exec command details withheld (command_bytes=9)"],
          },
        ],
        taskID: "task-1",
        busyID: "",
        onResolve: vi.fn(),
      }),
    );

    expect(screen.getByRole("button", { name: /Approve Shell execution/i })).not.toBeDisabled();
  });

  it("blocks inline approval when a legacy activity has no reviewable bundle", () => {
    render(
      createElement(HecateTaskApprovalsBanner, {
        approvals: [
          {
            approvalID: "ap-legacy",
            title: "agent_loop_tool_call",
            kind: "shell_command",
          },
        ],
        taskID: "task-1",
        busyID: "",
        onResolve: vi.fn(),
      }),
    );

    expect(screen.getByText(/review the complete pending actions before approving/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Approve Shell execution/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /Reject Shell execution/i })).not.toBeDisabled();
  });
});

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  AgentChatActivityRecord,
  AgentChatChangedFileDiffRecord,
  AgentChatChangedFileRecord,
  AgentChatTimingRecord,
  AgentChatUsageRecord,
} from "../../types/runtime";
import { TranscriptMessageRow } from "./TranscriptMessageRow";

const baseProps = {
  id: "m1",
  role: "assistant" as const,
  model: "gpt-4o",
  content: "hello",
  time: "10:01",
  onCopy: () => {},
  copied: false,
};

describe("TranscriptMessageRow", () => {
  it("renders assistant content as markdown", () => {
    render(<TranscriptMessageRow {...baseProps} content="**bold** and `code`" />);
    expect(screen.getByText("bold").tagName).toBe("STRONG");
    expect(screen.getByText("code").tagName).toBe("CODE");
  });

  it("renders the badge when supplied", () => {
    render(<TranscriptMessageRow {...baseProps} badge="running" />);
    expect(screen.getByText("running")).toBeInTheDocument();
  });

  it("renders an agent run failure notice when badge=failed and an error message is present", () => {
    render(<TranscriptMessageRow {...baseProps} badge="failed" error="adapter exited 1" />);
    expect(screen.getByText("agent run failed")).toBeInTheDocument();
    expect(screen.getByText("adapter exited 1")).toBeInTheDocument();
  });

  it("renders an agent run cancelled notice when badge=cancelled", () => {
    render(<TranscriptMessageRow {...baseProps} badge="cancelled" content="user pressed stop" />);
    expect(screen.getByText("agent run cancelled")).toBeInTheDocument();
    expect(screen.getByText("user pressed stop")).toBeInTheDocument();
  });

  it("shows the waiting-for-output indicator when assistant has no content but a running activity", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "running" },
    ];
    render(<TranscriptMessageRow {...baseProps} content="" activities={activities} />);
    expect(screen.getByText(/Waiting for agent output/)).toBeInTheDocument();
  });

  it("shows the waiting-for-output indicator for in-progress plan-only activity", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "plan", title: "Check the diff", status: "in_progress" },
    ];
    render(<TranscriptMessageRow {...baseProps} content="" activities={activities} />);
    expect(screen.getByText(/Waiting for agent output/)).toBeInTheDocument();
  });

  it("renders the user role label and U avatar for role=user", () => {
    render(<TranscriptMessageRow {...baseProps} role="user" content="hi there" />);
    expect(screen.getByText("You")).toBeInTheDocument();
    expect(screen.getByText("U")).toBeInTheDocument();
    expect(screen.getByText("hi there")).toBeInTheDocument();
  });

  it("shows token + cost meta when promptTokens > 0", () => {
    render(
      <TranscriptMessageRow
        {...baseProps}
        promptTokens={1234}
        completionTokens={56}
        costUsd="0.00123"
      />,
    );
    expect(screen.getByText(/1234↑ 56↓/)).toBeInTheDocument();
    expect(screen.getByText(/\$0\.00123/)).toBeInTheDocument();
  });

  it("invokes onCopy with id+content when the copy button is clicked", async () => {
    const onCopy = vi.fn();
    const user = userEvent.setup();
    render(<TranscriptMessageRow {...baseProps} onCopy={onCopy} />);
    await user.click(screen.getByRole("button"));
    expect(onCopy).toHaveBeenCalledWith("m1", "hello");
  });

  it("renders task and trace header links as compact debug actions", async () => {
    const onOpenTask = vi.fn();
    const onOpenTrace = vi.fn();
    const user = userEvent.setup();
    render(
      <TranscriptMessageRow
        {...baseProps}
        runtimeMeta="Run run_123 · 2.0s"
        taskLink={{ label: "Task task_123", onClick: onOpenTask }}
        traceLink={{ label: "Trace req_1234", onClick: onOpenTrace }}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Open Task task_123" }));
    await user.click(screen.getByRole("button", { name: "Open Trace req_1234" }));

    expect(onOpenTask).toHaveBeenCalledTimes(1);
    expect(onOpenTrace).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Run run_123 · 2.0s")).toBeInTheDocument();
  });

  it("renders the agent usage line when adapter-reported usage is present", () => {
    const usage: AgentChatUsageRecord = {
      reported_cost_amount: "0.42",
      reported_cost_currency: "USD",
      context_used: 12000,
      context_size: 200000,
    };
    render(<TranscriptMessageRow {...baseProps} agentUsage={usage} />);
    expect(screen.getByText(/0\.42 USD/)).toBeInTheDocument();
    expect(screen.getByText(/12000\/200000 context/)).toBeInTheDocument();
    expect(screen.getByText(/reported by adapter/)).toBeInTheDocument();
  });

  it("hides the agent usage line when all usage fields are empty/zero", () => {
    const usage: AgentChatUsageRecord = {
      reported_cost_amount: "",
      reported_cost_currency: "",
      context_used: 0,
      context_size: 0,
    };
    render(<TranscriptMessageRow {...baseProps} agentUsage={usage} />);
    expect(screen.queryByText(/reported by adapter/)).toBeNull();
  });

  it("renders the Hecate Agent timing summary when timing is present", () => {
    const timing: AgentChatTimingRecord = {
      total_ms: 12_400,
      queue_ms: 120,
      model_ms: 8_500,
      tool_ms: 700,
      approval_wait_ms: 2_000,
      overhead_ms: 1_080,
      turn_count: 2,
      tool_count: 1,
      bottleneck: "model",
      bottleneck_ms: 8_500,
    };
    render(<TranscriptMessageRow {...baseProps} agentTiming={timing} />);
    expect(screen.getByLabelText("Hecate Agent timing summary")).toBeInTheDocument();
    expect(screen.getByText(/bottleneck · model 8\.5s/)).toBeInTheDocument();
    expect(screen.getByText(/total 12s/)).toBeInTheDocument();
    expect(screen.getByText(/2 turns · 1 tool/)).toBeInTheDocument();
  });

  it("links failed tools to related stdout and stderr artifacts", async () => {
    const onOpenTask = vi.fn();
    const user = userEvent.setup();
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "git_exec (failed)", status: "failed", kind: "git", detail: "git_exec - failed" },
      { type: "artifact", title: "git-stdout.txt", status: "ready", artifact_id: "artifact_stdout", artifact_size_bytes: 42, artifact_preview: "diff --git a/README.md b/README.md\n+hello" },
      { type: "artifact", title: "git-stderr.txt", status: "ready", artifact_id: "artifact_stderr", artifact_size_bytes: 19, artifact_preview: "fatal: not a git repository" },
      { type: "failed", title: "Run failed", status: "failed", terminal: true },
    ];

    render(
      <TranscriptMessageRow
        {...baseProps}
        activities={activities}
        taskLink={{ label: "Task task_123", onClick: onOpenTask }}
      />,
    );

    await user.click(screen.getByText(/1 failed tool/));
    await user.click(screen.getByText("Advanced"));
    expect(screen.getByText(/Preview the related run output/)).toBeInTheDocument();
    expect(screen.getByText(/\+hello/)).toBeInTheDocument();
    expect(screen.getByText("fatal: not a git repository")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Open task output" }));
    expect(onOpenTask).toHaveBeenCalledTimes(1);
  });

  it("does not link empty stderr artifacts from failed tools", async () => {
    const user = userEvent.setup();
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "git_exec (failed)", status: "failed", kind: "git", detail: "git_exec - failed" },
      { type: "artifact", title: "git-stdout.txt", status: "ready", artifact_id: "artifact_stdout", artifact_size_bytes: 42, artifact_preview: "stdout details" },
      { type: "artifact", title: "git-stderr.txt", status: "ready", artifact_id: "artifact_stderr", artifact_size_bytes: 0 },
    ];

    render(
      <TranscriptMessageRow
        {...baseProps}
        activities={activities}
        taskLink={{ label: "Task task_123", onClick: vi.fn() }}
      />,
    );

    await user.click(screen.getByText(/1 failed tool/));
    await user.click(screen.getByText("Advanced"));
    expect(screen.getByText("stdout details")).toBeInTheDocument();
    expect(screen.queryByText("Preview unavailable in this snapshot.")).toBeNull();
  });

  it("renders the diff review section when diff metadata is present", () => {
    const onListFiles: (sid: string, mid: string) => Promise<AgentChatChangedFileRecord[]> = vi.fn(async () => []);
    const onGetFileDiff: (sid: string, mid: string, p: string) => Promise<AgentChatChangedFileDiffRecord | null> = vi.fn(async () => null);
    const onRevertFiles: (sid: string, mid: string, ps: string[]) => Promise<boolean> = vi.fn(async () => true);

    render(
      <TranscriptMessageRow
        {...baseProps}
        agentSessionID="s1"
        diffStat="src/foo.ts | 3 +-"
        onListAgentFiles={onListFiles}
        onGetAgentFileDiff={onGetFileDiff}
        onRevertAgentFiles={onRevertFiles}
      />,
    );
    expect(screen.getByTestId("agent-diff-review")).toBeInTheDocument();
  });

  it("renders the raw adapter output details when rawOutput differs from content", () => {
    render(<TranscriptMessageRow {...baseProps} content="final answer" rawOutput="I'll do this. final answer" />);
    expect(screen.getByText(/raw adapter output/)).toBeInTheDocument();
  });

  it("does not render the raw adapter output details when rawOutput equals content", () => {
    render(<TranscriptMessageRow {...baseProps} content="final answer" rawOutput="final answer" />);
    expect(screen.queryByText(/raw adapter output/)).toBeNull();
  });
});

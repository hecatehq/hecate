import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { AgentChatActivityRecord } from "../../types/runtime";
import { TranscriptActivityTimeline, DiffStatList, formatDiffStatSummary } from "./TranscriptActivityTimeline";

describe("formatDiffStatSummary", () => {
  it("returns the 'N files changed' line when present", () => {
    const stat = "src/foo.ts | 3 +-\nsrc/bar.ts | 2 +-\n2 files changed, 4 insertions(+), 1 deletion(-)";
    expect(formatDiffStatSummary(stat)).toMatch(/2 files? changed/);
  });

  it("falls back to the first line when no summary is present", () => {
    const stat = "src/foo.ts | 3 +-";
    expect(formatDiffStatSummary(stat)).toBe("src/foo.ts | 3 +-");
  });

  it("returns an empty string for empty input", () => {
    expect(formatDiffStatSummary("")).toBe("");
  });
});

describe("DiffStatList", () => {
  it("renders one row per changed file with its change column", () => {
    const stat = "src/foo.ts | 3 +-\nREADME.md | 1 +\n2 files changed, 3 insertions(+), 1 deletion(-)";
    render(<DiffStatList diffStat={stat} />);
    expect(screen.getByText("src/foo.ts")).toBeInTheDocument();
    expect(screen.getByText("README.md")).toBeInTheDocument();
    expect(screen.getByText("3 +-")).toBeInTheDocument();
    expect(screen.getByText("1 +")).toBeInTheDocument();
  });

  it("renders the summary line at the bottom", () => {
    const stat = "src/foo.ts | 3 +-\n1 file changed, 2 insertions(+), 1 deletion(-)";
    render(<DiffStatList diffStat={stat} />);
    expect(screen.getByText(/1 file changed/)).toBeInTheDocument();
  });

  it("falls back to summary-only render when no per-file rows match the format", () => {
    const stat = "1 file changed, 2 insertions(+), 1 deletion(-)";
    render(<DiffStatList diffStat={stat} />);
    expect(screen.getByText(/1 file changed/)).toBeInTheDocument();
  });
});

describe("TranscriptActivityTimeline", () => {
  it("renders nothing when activities is empty", () => {
    const { container } = render(<TranscriptActivityTimeline activities={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the summary with running status when no terminal activity is present", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "running", kind: "fs" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText(/working/)).toBeInTheDocument();
  });

  it("renders the summary with running status for in-progress plan-only activity", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "plan", title: "Inspect the branch", status: "in_progress" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText(/working/)).toBeInTheDocument();
  });

  it("renders the terminal status in the summary when a completed activity exists", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "completed" },
      { type: "completed", title: "Final answer", status: "completed" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText(/completed/)).toBeInTheDocument();
  });

  it("preserves operator-expanded completed details across rerenders", async () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "completed" },
      { type: "completed", title: "Final answer", status: "completed" },
    ];
    const user = userEvent.setup();
    const { rerender } = render(<TranscriptActivityTimeline activities={activities} />);
    const summary = screen.getByText(/completed/);
    const details = summary.closest("details");
    expect(details?.open).toBe(false);

    await user.click(summary);
    expect(details?.open).toBe(true);

    rerender(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText(/completed/).closest("details")?.open).toBe(true);
  });

  it("renders plan items with their plan-status indicators", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "plan", title: "Step 1", status: "completed" },
      { type: "plan", title: "Step 2", status: "in_progress" },
      { type: "plan", title: "Step 3", status: "pending" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText("Step 1")).toBeInTheDocument();
    expect(screen.getByText("Step 2")).toBeInTheDocument();
    expect(screen.getByText("Step 3")).toBeInTheDocument();
  });

  it("renders Hecate tool calls with operator-facing labels and detail", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "completed", kind: "fs", detail: "src/index.ts" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText("Read file")).toBeInTheDocument();
    expect(screen.getByText("tool")).toBeInTheDocument();
    expect(screen.getByText("src/index.ts")).toBeInTheDocument();
  });

  it("removes duplicate tool details that repeat title and status", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "git_exec", status: "completed", kind: "tool", detail: "git_exec - completed" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText("Ran git")).toBeInTheDocument();
    expect(screen.queryByText("git_exec - completed")).toBeNull();
  });

  it("includes changed files in the summary and expanded activity list when diffStat is supplied", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "completed" },
    ];
    render(<TranscriptActivityTimeline activities={activities} diffStat="src/foo.ts | 3 +-\n1 file changed, 2 insertions(+), 1 deletion(-)" />);
    expect(screen.getByText(/files changed/)).toBeInTheDocument();
    expect(screen.getByText("Files changed")).toBeInTheDocument();
    expect(screen.getByText("1 file changed, 2 insertions(+), 1 deletion(-)")).toBeInTheDocument();
  });

  it("hides the 'started' activity when a terminal activity has appeared", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "started", title: "Started" },
      { type: "tool_call", title: "read_file", status: "completed" },
      { type: "completed", title: "Final answer" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.queryByText("Started")).toBeNull();
  });

  it("groups internal task artifacts under Details", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "git_exec", status: "completed", kind: "git" },
      { type: "artifact", title: "git-stdout.txt", status: "ready" },
      { type: "artifact", title: "git-stderr.txt", status: "ready" },
      { type: "changed_files", title: "git-changes.json", status: "ready" },
      { type: "final_answer", title: "agent-final-answer.txt", status: "ready" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText("Ran git")).toBeInTheDocument();
    expect(screen.getByText("Details · 4 items")).toBeInTheDocument();
    expect(screen.getByText("git-stdout.txt")).toBeInTheDocument();
    expect(screen.getByText("git-stderr.txt")).toBeInTheDocument();
    expect(screen.getByText("git-changes.json")).toBeInTheDocument();
    expect(screen.getByText("agent-final-answer.txt")).toBeInTheDocument();
  });

  it("hides internal agent-loop approval markers from operator-facing rows", () => {
    const activities: AgentChatActivityRecord[] = [
      {
        type: "approval",
        title: "builtin.agent_loop_approval",
        status: "approved",
        detail: "builtin.agent_loop_approval - approved",
      },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText("Approval granted")).toBeInTheDocument();
    expect(screen.queryByText(/builtin\.agent_loop/)).toBeNull();
  });

  it("summarizes Hecate Agent task internals without duplicate terminal rows", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "git_exec", status: "completed", kind: "git", detail: "git_exec - completed" },
      { type: "task_run", title: "Backing task", status: "completed", detail: "completed · task_123 · run_456" },
      { type: "thinking", title: "Agent turn 1", status: "completed", detail: "builtin.agent_loop_llm - completed" },
      { type: "thinking", title: "Agent turn 2", status: "completed", detail: "builtin.agent_loop_llm - completed" },
      { type: "run_result", title: "Run completed", status: "completed", detail: "completed" },
      { type: "completed", title: "Final answer", status: "completed" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);

    expect(screen.getByText("Ran git")).toBeInTheDocument();
    expect(screen.getByText("Backing task")).toBeInTheDocument();
    expect(screen.getByText("Thinking")).toBeInTheDocument();
    expect(screen.getByText("2 model turns completed")).toBeInTheDocument();
    expect(screen.queryByText("Agent turn 1")).toBeNull();
    expect(screen.queryByText("Agent turn 2")).toBeNull();
    expect(screen.queryByText("Run completed")).toBeNull();
    expect(screen.queryByText("git_exec - completed")).toBeNull();
  });

  it("renders expanded activity rows in chronological order instead of grouping tools first", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "started", title: "Starting Hecate Agent", status: "running" },
      { type: "task_run", title: "Backing task", status: "running", detail: "running · task_123 · run_456" },
      { type: "thinking", title: "Agent turn 1", status: "completed", detail: "builtin.agent_loop_llm - completed" },
      { type: "tool_call", title: "shell_exec", status: "completed", kind: "tool", detail: "shell_exec - completed" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);

    const labels = screen.getAllByText(/Starting agent|Backing task|Thinking|Ran shell/)
      .map(node => node.textContent);
    expect(labels).toEqual(["Starting agent", "Backing task", "Thinking", "Ran shell"]);
  });

  it("keeps external-agent activity rows chronological too", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "started", title: "Starting external agent", status: "running" },
      { type: "running", title: "Running", status: "running" },
      { type: "plan", title: "Inspect the repository", status: "in_progress" },
      { type: "tool_call", title: "git status", status: "completed", kind: "command" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);

    const labels = screen.getAllByText(/Starting external agent|Running|Inspect the repository|git status/)
      .map(node => node.textContent);
    expect(labels).toEqual(["Starting external agent", "Running", "Inspect the repository", "git status"]);
  });
});

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
    expect(screen.getByText(/running/)).toBeInTheDocument();
  });

  it("renders the summary with running status for in-progress plan-only activity", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "plan", title: "Inspect the branch", status: "in_progress" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText(/running/)).toBeInTheDocument();
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

  it("renders tool calls with their kind prefix and detail", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "completed", kind: "fs", detail: "src/index.ts" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText("read_file")).toBeInTheDocument();
    expect(screen.getByText("fs")).toBeInTheDocument();
    expect(screen.getByText("src/index.ts")).toBeInTheDocument();
  });

  it("includes 'files changed' in the summary when diffStat is supplied", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "completed" },
    ];
    render(<TranscriptActivityTimeline activities={activities} diffStat="src/foo.ts | 3 +-" />);
    expect(screen.getByText(/files changed/)).toBeInTheDocument();
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
});

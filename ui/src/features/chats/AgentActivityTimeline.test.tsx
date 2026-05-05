import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { AgentChatActivityRecord } from "../../types/runtime";
import { ActivityTimeline, DiffStatList, formatDiffStatSummary } from "./AgentActivityTimeline";

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

describe("ActivityTimeline", () => {
  it("renders nothing when activities is empty", () => {
    const { container } = render(<ActivityTimeline activities={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the summary with running status when no terminal activity is present", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "running", kind: "fs" },
    ];
    render(<ActivityTimeline activities={activities} />);
    expect(screen.getByText(/running/)).toBeInTheDocument();
  });

  it("renders the terminal status in the summary when a completed activity exists", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "completed" },
      { type: "completed", title: "Final answer", status: "completed" },
    ];
    render(<ActivityTimeline activities={activities} />);
    expect(screen.getByText(/completed/)).toBeInTheDocument();
  });

  it("renders plan items with their plan-status indicators", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "plan", title: "Step 1", status: "completed" },
      { type: "plan", title: "Step 2", status: "in_progress" },
      { type: "plan", title: "Step 3", status: "pending" },
    ];
    render(<ActivityTimeline activities={activities} />);
    expect(screen.getByText("Step 1")).toBeInTheDocument();
    expect(screen.getByText("Step 2")).toBeInTheDocument();
    expect(screen.getByText("Step 3")).toBeInTheDocument();
  });

  it("renders tool calls with their kind prefix and detail", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "completed", kind: "fs", detail: "src/index.ts" },
    ];
    render(<ActivityTimeline activities={activities} />);
    expect(screen.getByText("read_file")).toBeInTheDocument();
    expect(screen.getByText("fs")).toBeInTheDocument();
    expect(screen.getByText("src/index.ts")).toBeInTheDocument();
  });

  it("includes 'files changed' in the summary when diffStat is supplied", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "completed" },
    ];
    render(<ActivityTimeline activities={activities} diffStat="src/foo.ts | 3 +-" />);
    expect(screen.getByText(/files changed/)).toBeInTheDocument();
  });

  it("hides the 'started' activity when a terminal activity has appeared", () => {
    const activities: AgentChatActivityRecord[] = [
      { type: "started", title: "Started" },
      { type: "tool_call", title: "read_file", status: "completed" },
      { type: "completed", title: "Final answer" },
    ];
    render(<ActivityTimeline activities={activities} />);
    expect(screen.queryByText("Started")).toBeNull();
  });
});

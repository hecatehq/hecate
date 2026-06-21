import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { ChatActivityRecord } from "../../types/chat";
import {
  TranscriptActivityTimeline,
  DiffStatList,
  formatDiffStatSummary,
} from "./TranscriptActivityTimeline";

describe("formatDiffStatSummary", () => {
  it("returns the 'N files changed' line when present", () => {
    const stat =
      "src/foo.ts | 3 +-\nsrc/bar.ts | 2 +-\n2 files changed, 4 insertions(+), 1 deletion(-)";
    expect(formatDiffStatSummary(stat)).toMatch(/2 files? changed/);
  });

  it("falls back to a compact changed-file count when no summary is present", () => {
    const stat = "src/foo.ts | 3 +-";
    expect(formatDiffStatSummary(stat)).toBe("1 changed file");
  });

  it("returns an empty string for empty input", () => {
    expect(formatDiffStatSummary("")).toBe("");
  });
});

describe("DiffStatList", () => {
  it("renders one row per changed file with its change column", () => {
    const stat =
      "src/foo.ts | 3 +-\nREADME.md | 1 +\n2 files changed, 3 insertions(+), 1 deletion(-)";
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
    const activities: ChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "running", kind: "fs" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText(/working/)).toBeInTheDocument();
  });

  it("renders the summary with running status for in-progress plan-only activity", () => {
    const activities: ChatActivityRecord[] = [
      { type: "plan", title: "Inspect the branch", status: "in_progress" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText(/working/)).toBeInTheDocument();
  });

  it("renders the terminal status in the summary when a completed activity exists", () => {
    const activities: ChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "completed" },
      { type: "completed", title: "Final answer", status: "completed" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText(/completed/)).toBeInTheDocument();
  });

  it("preserves operator-expanded completed details across rerenders", async () => {
    const activities: ChatActivityRecord[] = [
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

  it("dedupes earlier terminal rows when a later terminal row exists", () => {
    // The Hecate Chat run path emits two terminal-shaped rows on a
    // successful completion: a synced `task_run` mirror surfacing
    // as `run_result` (the fixture uses title "Run finished" so it
    // bypasses isTerminalRunSummary's `/^run (completed|failed|cancelled)$/`
    // filter, which strips the literal "run completed" titles), and
    // an explicit `Activity{Type: status, Title: finalChatActivityTitle(status)}`
    // appended by the agent-chat handler at turn end. Without
    // dedupe the operator sees two side-by-side terminal rows for
    // one run; the timeline should keep only one. Earlier
    // rows that match `isTerminalRunSummary` were already dropped,
    // but type-only collisions (e.g. type=completed title="Done")
    // survived prior to the dedupe rule.
    const activities: ChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "completed" },
      { type: "run_result", title: "Run finished", status: "completed" },
      { type: "completed", title: "Done", status: "completed" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.queryByText("Run finished")).toBeNull();
    expect(screen.getAllByText("Done")).toHaveLength(1);
  });

  it("prefers a terminal=true diagnostic row over a generic terminal row when both are present", () => {
    // The synced `task_run` mirror carries `terminal: true` AND a
    // detail like "LLM call failed on turn 3" — informative
    // diagnostic the operator wants to see. The agent-chat
    // handler also appends a generic `Activity{Type: "failed",
    // Title: "Failed"}` at turn end. When both are on the timeline,
    // the diagnostic row must win — naïvely keeping "the latest
    // terminal row" would drop it in favour of the bare-bones
    // generic row that surfaces no useful detail.
    // Title chosen to avoid `isTerminalRunSummary`'s regex
    // (`/^run (completed|failed|cancelled)$/i`), which would strip
    // the row before the dedupe-pick step ran. Real diagnostic rows
    // typically carry richer titles like "LLM call failed on turn 3"
    // anyway.
    const activities: ChatActivityRecord[] = [
      { type: "tool_call", title: "shell_exec", status: "failed" },
      {
        type: "run_result",
        title: "LLM call failed on turn 3",
        status: "failed",
        terminal: true,
        detail: "rate limit exceeded",
      },
      { type: "failed", title: "Failed", status: "failed" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText("LLM call failed on turn 3")).toBeInTheDocument();
    // The generic "Failed" row title must NOT appear as a timeline row
    // (the word "failed" still surfaces inside the timeline summary
    // status text, which is fine — that's not a row).
    const failedAsRowTitle = screen
      .queryAllByText("Failed")
      .filter((node) => !node.closest("summary"));
    expect(failedAsRowTitle).toHaveLength(0);
  });

  it("renders plan items with their plan-status indicators", () => {
    const activities: ChatActivityRecord[] = [
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
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "read_file",
        status: "completed",
        kind: "fs",
        detail: "src/index.ts",
      },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText("Read file")).toBeInTheDocument();
    expect(screen.getByText("tool")).toBeInTheDocument();
    expect(screen.getByText("src/index.ts")).toBeInTheDocument();
  });

  it("renders terminal activities with a terminal summary and prefix", () => {
    const activities: ChatActivityRecord[] = [
      {
        id: "terminal:term_123",
        type: "terminal",
        status: "completed",
        kind: "execute",
        title: "Terminal command",
        detail: "sh -c 'go test ./...' · cwd /repo · exit code 0",
      },
      { type: "completed", title: "Final answer", status: "completed" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText(/completed · 1 terminal/)).toBeInTheDocument();
    expect(screen.getByText("terminal")).toBeInTheDocument();
    expect(screen.getByText("Terminal command")).toBeInTheDocument();
    expect(screen.getByText("sh -c 'go test ./...' · cwd /repo · exit code 0")).toBeInTheDocument();
  });

  it("removes duplicate tool details that repeat title and status", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "git_exec",
        status: "completed",
        kind: "tool",
        detail: "git_exec - completed",
      },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText("Ran git")).toBeInTheDocument();
    expect(screen.queryByText("git_exec - completed")).toBeNull();
  });

  it("humanizes failed tool titles with status suffixes and marks the summary", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "git_exec (failed)",
        status: "failed",
        kind: "git",
        detail: "git_exec - failed",
      },
      { type: "completed", title: "Run completed", status: "completed" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText(/completed · 1 failed tool/)).toBeInTheDocument();
    expect(screen.getByText("Ran git")).toBeInTheDocument();
    expect(screen.queryByText("git_exec - failed")).toBeNull();
  });

  it("humanizes opaque external-agent tool call ids", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_YLnXdDBfBhiiQnC46sCy8NzM",
        status: "completed",
        kind: "execute",
        detail: "execute",
      },
      {
        type: "tool_call",
        title: "call_MGCYNWm0EHPZwWuQ4QmcNgU5",
        status: "completed",
        kind: "read",
        detail: "read",
      },
      { type: "cancelled", title: "Cancelled", status: "cancelled" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText("Ran command")).toBeInTheDocument();
    expect(screen.getByText("Read context")).toBeInTheDocument();
    expect(screen.getByText("execute · tool YLnXdDBf")).toBeInTheDocument();
    expect(screen.getByText("read · tool MGCYNWm0")).toBeInTheDocument();
    expect(screen.queryByText(/call_YLnXd/)).toBeNull();
    expect(screen.getByText("stopped before the run finished")).toBeInTheDocument();
  });

  it("prefers adapter-provided command details over opaque tool ids", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_ERrtqCoyxGRidDjwpaR9OZEX",
        status: "failed",
        kind: "execute",
        detail: 'execute · /bin/zsh -lc "go test ./..."',
      },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText("Ran command")).toBeInTheDocument();
    expect(screen.getByText('execute · /bin/zsh -lc "go test ./..."')).toBeInTheDocument();
    expect(screen.queryByText(/tool ERrtqCoy/)).toBeNull();
  });

  it("uses adapter detail hints for opaque edit tool rows", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "toolu_01X4Kr7tteNtaP6emRf7ULtM",
        status: "completed",
        detail: "edit · 1 diff",
      },
    ];

    render(<TranscriptActivityTimeline activities={activities} />);

    expect(screen.getByText("Edited file")).toBeInTheDocument();
    expect(screen.getByText("edit · 1 diff")).toBeInTheDocument();
    expect(screen.queryByText(/toolu_01X4/)).toBeNull();
  });

  it("keeps captured read output out of the inline activity row", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_read",
        status: "completed",
        kind: "read",
        detail:
          'read · output: 1><h1 align="center">Hecate</h1> 2><img src="docs/assets/logo.png">',
      },
    ];

    render(<TranscriptActivityTimeline activities={activities} />);

    expect(screen.getByText("Read context")).toBeInTheDocument();
    expect(screen.queryByText("read · output captured")).toBeNull();
    expect(screen.queryByText(/h1 align/)).toBeNull();
    expect(screen.queryByText(/docs\/assets\/logo/)).toBeNull();
  });

  it("summarizes noisy generic command activity and reveals command details together", async () => {
    const user = userEvent.setup();
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_1",
        status: "completed",
        kind: "execute",
        detail: "execute · output: commit abc123 Author: Someone",
      },
      {
        type: "tool_call",
        title: "call_2",
        status: "completed",
        kind: "execute",
        detail: "execute · output: 1871",
      },
      {
        type: "tool_call",
        title: "call_3",
        status: "completed",
        kind: "execute",
        detail: "execute",
      },
      {
        type: "tool_call",
        title: "call_4",
        status: "failed",
        kind: "execute",
        detail: "execute · output: command failed",
      },
      { type: "completed", title: "Run completed", status: "completed" },
    ];

    render(
      <TranscriptActivityTimeline
        activities={activities}
        renderAdvancedActivity={(activity) =>
          activity.type === "tool_call" ? <div>Tool output for {activity.title}</div> : undefined
        }
      />,
    );

    expect(screen.getByText(/completed · 1 failed tool/)).toBeInTheDocument();
    expect(screen.getByText("Ran 4 commands")).toBeInTheDocument();
    expect(screen.getByText("1 failed · output captured")).toBeInTheDocument();
    expect(screen.queryByText("Ran command")).toBeNull();
    expect(screen.queryByText(/commit abc123/)).toBeNull();
    expect(screen.queryByText(/Tool output for call_1/)).toBeNull();

    await user.click(screen.getByText("Commands"));

    expect(screen.getAllByText("Ran command")).toHaveLength(4);
    expect(screen.queryByText(/commit abc123/)).toBeNull();
    expect(screen.getByText("Tool output for call_1")).toBeInTheDocument();
    expect(screen.getByText("Tool output for call_4")).toBeInTheDocument();
    expect(screen.queryByText("Output")).toBeNull();
  });

  it("treats completed commands with fatal output as failed for transcript tone", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_1",
        status: "completed",
        kind: "execute",
        detail: "execute · output: commit abc123",
      },
      {
        type: "tool_call",
        title: "call_2",
        status: "completed",
        kind: "execute",
        detail: "execute · output: fatal: ambiguous argument 'main..HEAD'",
      },
      {
        type: "tool_call",
        title: "call_3",
        status: "completed",
        kind: "execute",
        detail: "execute",
      },
      {
        type: "tool_call",
        title: "call_4",
        status: "completed",
        kind: "execute",
        detail: "execute · output: ok",
      },
      { type: "completed", title: "Run completed", status: "completed" },
    ];

    render(<TranscriptActivityTimeline activities={activities} />);

    expect(screen.getByText(/completed · 1 failed tool/)).toBeInTheDocument();
    expect(screen.getByText("1 failed · output captured")).toBeInTheDocument();
  });

  it("treats output-captured fatal command details as failed for transcript tone", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_1",
        status: "completed",
        kind: "execute",
        detail: "execute · output captured · ok",
      },
      {
        type: "tool_call",
        title: "call_2",
        status: "completed",
        kind: "execute",
        detail: "execute · output captured · fatal: ambiguous argument 'origin/main..HEAD'",
      },
      {
        type: "tool_call",
        title: "call_3",
        status: "completed",
        kind: "execute",
        detail: "execute",
      },
      {
        type: "tool_call",
        title: "call_4",
        status: "completed",
        kind: "execute",
        detail: "execute · output captured · ok",
      },
      { type: "completed", title: "Run completed", status: "completed" },
    ];

    render(<TranscriptActivityTimeline activities={activities} />);

    expect(screen.getByText(/completed · 1 failed tool/)).toBeInTheDocument();
    expect(screen.getByText("1 failed · output captured")).toBeInTheDocument();
  });

  it("renders Grok session read failures as adapter context warnings", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_read",
        status: "failed",
        kind: "read",
        detail:
          "read · output: Failed to read file: /Users/operator/.grok/sessions/%2FUsers%2Foperator%2Fdev%2Fhecate/session.json",
      },
      { type: "completed", title: "Run completed", status: "completed" },
    ];

    render(<TranscriptActivityTimeline activities={activities} />);

    expect(screen.getByText("Could not read context")).toBeInTheDocument();
    expect(screen.getByText("adapter session file was unavailable")).toBeInTheDocument();
    expect(screen.queryByText(/\.grok\/sessions/)).toBeNull();
  });

  it("describes failed tools as interrupted when the run is cancelled", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "call_one",
        status: "failed",
        kind: "execute",
        detail: "execute",
      },
      {
        type: "tool_call",
        title: "call_two",
        status: "failed",
        kind: "execute",
        detail: "execute",
      },
      { type: "cancelled", title: "Cancelled", status: "cancelled" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText(/cancelled · 2 interrupted tools/)).toBeInTheDocument();
  });

  it("includes workspace diff snapshots in the summary without duplicating a timeline row", () => {
    const activities: ChatActivityRecord[] = [
      { type: "tool_call", title: "read_file", status: "completed" },
    ];
    render(
      <TranscriptActivityTimeline
        activities={activities}
        diffStat="src/foo.ts | 3 +-\n1 file changed, 2 insertions(+), 1 deletion(-)"
      />,
    );
    expect(screen.getByText(/workspace diff snapshot/)).toBeInTheDocument();
    expect(screen.queryByText("Workspace diff snapshot")).toBeNull();
    expect(screen.queryByText("1 file changed, 2 insertions(+), 1 deletion(-)")).toBeNull();
  });

  it("does not duplicate backend files_changed rows when diffStat is supplied", () => {
    const activities: ChatActivityRecord[] = [
      { type: "tool_call", title: "git_exec", status: "completed" },
      {
        type: "files_changed",
        title: "Files changed",
        status: "completed",
        detail: "2 files changed, 72 insertions(+), 3 deletions(-)",
      },
    ];
    render(
      <TranscriptActivityTimeline
        activities={activities}
        diffStat="ui/src/a.tsx | 54 ++++++++++++++++++\nui/src/b.tsx | 21 +++++++--\n2 files changed, 72 insertions(+), 3 deletions(-)"
      />,
    );

    expect(screen.queryByText("Workspace diff snapshot")).toBeNull();
    expect(screen.queryByText("2 files changed, 72 insertions(+), 3 deletions(-)")).toBeNull();
  });

  it("hides the 'started' activity when a terminal activity has appeared", () => {
    const activities: ChatActivityRecord[] = [
      { type: "started", title: "Started" },
      { type: "tool_call", title: "read_file", status: "completed" },
      { type: "completed", title: "Final answer" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.queryByText("Started")).toBeNull();
  });

  it("groups internal task artifacts under Details", () => {
    const activities: ChatActivityRecord[] = [
      { type: "tool_call", title: "git_exec", status: "completed", kind: "git" },
      {
        type: "artifact",
        title: "git-stdout.txt",
        status: "ready",
        artifact_preview: "stdout",
      },
      {
        type: "artifact",
        title: "git-stderr.txt",
        status: "ready",
        artifact_preview: "stderr",
      },
      { type: "changed_files", title: "git-changes.json", status: "ready" },
      { type: "final_answer", title: "agent-final-answer.txt", status: "ready" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.getByText("Ran git")).toBeInTheDocument();
    expect(screen.getByText("Output and artifacts · 4 items")).toBeInTheDocument();
    expect(screen.getByText("git-stdout.txt")).toBeInTheDocument();
    expect(screen.getByText("git-stderr.txt")).toBeInTheDocument();
    expect(screen.getByText("git-changes.json")).toBeInTheDocument();
    expect(screen.getByText("agent-final-answer.txt")).toBeInTheDocument();
  });

  it("hides output-only rows without a captured preview", () => {
    const activities: ChatActivityRecord[] = [
      { type: "output", title: "stderr", status: "ready", artifact_size_bytes: 0 },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.queryByText("stderr · 0b")).toBeNull();
    expect(screen.queryByText(/Output/)).toBeNull();
  });

  it("summarizes project proposal activities as ready proposals", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "project_assistant_proposal",
        kind: "project_assistant_proposal",
        title: "Project Assistant proposal",
        detail: "Plan next project work - 1 action - ready for review",
        status: "ready",
      },
    ];

    render(<TranscriptActivityTimeline activities={activities} />);

    expect(screen.getByText(/1 proposal ready/)).toBeInTheDocument();
    expect(screen.getByText("Project Assistant proposal")).toBeInTheDocument();
    expect(
      screen.getByText("Plan next project work - 1 action - ready for review"),
    ).toBeInTheDocument();
  });

  it("hides output artifacts without captured previews", () => {
    const activities: ChatActivityRecord[] = [
      { type: "artifact", title: "ACP output", status: "ready", detail: "stdout · 1 line" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);
    expect(screen.queryByText("Output and artifacts · 1 item")).toBeNull();
    expect(screen.queryByText("ACP output")).toBeNull();
  });

  it("hides internal agent-loop approval markers from operator-facing rows", () => {
    const activities: ChatActivityRecord[] = [
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

  it("summarizes Hecate Chat task internals without duplicate terminal rows", () => {
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "git_exec",
        status: "completed",
        kind: "git",
        detail: "git_exec - completed",
      },
      {
        type: "task_run",
        title: "Backing task",
        status: "completed",
        detail: "completed · task_123 · run_456",
      },
      {
        type: "thinking",
        title: "Agent turn 1",
        status: "completed",
        detail: "builtin.agent_loop_llm - completed",
      },
      {
        type: "thinking",
        title: "Agent turn 2",
        status: "completed",
        detail: "builtin.agent_loop_llm - completed",
      },
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
    const activities: ChatActivityRecord[] = [
      { type: "started", title: "Starting Hecate Chat tools", status: "running" },
      {
        type: "task_run",
        title: "Backing task",
        status: "running",
        detail: "running · task_123 · run_456",
      },
      {
        type: "thinking",
        title: "Agent turn 1",
        status: "completed",
        detail: "builtin.agent_loop_llm - completed",
      },
      {
        type: "tool_call",
        title: "shell_exec",
        status: "completed",
        kind: "tool",
        detail: "shell_exec - completed",
      },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);

    const labels = screen
      .getAllByText(/Starting agent|Backing task|Thinking|Ran shell/)
      .map((node) => node.textContent);
    expect(labels).toEqual(["Starting agent", "Backing task", "Thinking", "Ran shell"]);
  });

  it("uses operator-facing order for Hecate task rows with identical timestamps", () => {
    const at = "2026-05-07T20:00:00Z";
    const activities: ChatActivityRecord[] = [
      {
        type: "tool_call",
        title: "git_exec (failed)",
        status: "failed",
        kind: "git",
        detail: "git_exec - failed",
        created_at: at,
      },
      {
        type: "thinking",
        title: "Agent turn 1",
        status: "completed",
        detail: "builtin.agent_loop_llm - completed",
        created_at: at,
      },
      {
        type: "task_run",
        title: "Backing task",
        status: "failed",
        detail: "failed",
        created_at: at,
      },
      {
        type: "failed",
        title: "LLM call failed on turn 2: timeout",
        status: "failed",
        terminal: true,
        created_at: at,
      },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);

    const labels = screen
      .getAllByText(/Backing task|Thinking|Ran git|LLM call failed on turn 2/)
      .map((node) => node.textContent);
    expect(labels).toEqual([
      "Backing task",
      "Thinking",
      "Ran git",
      "LLM call failed on turn 2: timeout",
    ]);
  });

  it("hides generic terminal summaries but keeps diagnostic failed rows", () => {
    const activities: ChatActivityRecord[] = [
      { type: "tool_call", title: "git_exec", status: "failed", kind: "git" },
      { type: "failed", title: "Run failed", status: "failed", terminal: true },
      {
        type: "run_result",
        title: "LLM call failed on turn 2: timeout",
        status: "failed",
        terminal: true,
      },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);

    expect(screen.queryByText("Run failed")).toBeNull();
    expect(screen.getByText("LLM call failed on turn 2: timeout")).toBeInTheDocument();
  });

  it("keeps external-agent activity rows chronological too", () => {
    const activities: ChatActivityRecord[] = [
      { type: "started", title: "Starting external agent", status: "running" },
      { type: "running", title: "Running", status: "running" },
      { type: "plan", title: "Inspect the repository", status: "in_progress" },
      { type: "tool_call", title: "git status", status: "completed", kind: "command" },
    ];
    render(<TranscriptActivityTimeline activities={activities} />);

    const labels = screen
      .getAllByText(/Starting external agent|Running|Inspect the repository|git status/)
      .map((node) => node.textContent);
    expect(labels).toEqual([
      "Starting external agent",
      "Running",
      "Inspect the repository",
      "git status",
    ]);
  });
});

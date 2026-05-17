import { describe, expect, it } from "vitest";

import type { AgentChatActivityRecord } from "../../types/runtime";

import {
  activityDisplay,
  activityLinePrefix,
  activityStatusColor,
  compactAgentActivities,
  compactDetailActivities,
  detailSummaryLabel,
  fileChangesActivity,
  formatDiffStatSummary,
  isActiveAgentActivity,
  isOutputArtifactActivity,
  isTerminalActivity,
  orderVisibleActivities,
  parseDiffStatRows,
  pickTerminalActivityIndex,
  terminalAgentActivity,
  terminalStatusLabel,
} from "./transcriptActivityHelpers";

function activity(overrides: Partial<AgentChatActivityRecord> & Pick<AgentChatActivityRecord, "type" | "title">): AgentChatActivityRecord {
  return {
    status: "completed",
    ...overrides,
  } as AgentChatActivityRecord;
}

describe("formatDiffStatSummary", () => {
  it("returns the 'N files changed' line when present", () => {
    const stat = "src/foo.ts | 3 +-\nsrc/bar.ts | 2 +-\n2 files changed, 4 insertions(+), 1 deletion(-)";
    expect(formatDiffStatSummary(stat)).toMatch(/^2 files changed/);
  });

  it("matches the singular 'file changed' form", () => {
    expect(formatDiffStatSummary("foo.ts | 3 +-\n1 file changed, 2 insertions(+)")).toMatch(/^1 file changed/);
  });

  it("falls back to the first non-empty line when no summary is present", () => {
    expect(formatDiffStatSummary("src/foo.ts | 3 +-")).toBe("src/foo.ts | 3 +-");
  });

  it("returns empty string for empty input", () => {
    expect(formatDiffStatSummary("")).toBe("");
    expect(formatDiffStatSummary("   ")).toBe("");
  });

  it("handles the escaped-\\n separator the backend sometimes emits", () => {
    const stat = "foo.ts | 3 +-\\nbar.ts | 2 +-\\n2 files changed, 5 insertions(+)";
    expect(formatDiffStatSummary(stat)).toMatch(/^2 files changed/);
  });
});

describe("parseDiffStatRows", () => {
  it("splits each non-summary line into path + change columns", () => {
    const stat = "src/foo.ts | 3 +-\nsrc/bar.ts | 2 +-\n2 files changed, 4 insertions(+)";
    expect(parseDiffStatRows(stat)).toEqual([
      { path: "src/foo.ts", change: "3 +-" },
      { path: "src/bar.ts", change: "2 +-" },
    ]);
  });

  it("drops lines that don't fit the 'path | change' shape", () => {
    expect(parseDiffStatRows("garbage line\nfoo.ts | 1 +")).toEqual([
      { path: "foo.ts", change: "1 +" },
    ]);
  });

  it("returns an empty array when only a summary line is present", () => {
    expect(parseDiffStatRows("1 file changed, 2 insertions(+)")).toEqual([]);
  });

  it("trims path and change whitespace", () => {
    expect(parseDiffStatRows("  foo.ts  |  3 +-  ")).toEqual([
      { path: "foo.ts", change: "3 +-" },
    ]);
  });
});

describe("fileChangesActivity", () => {
  it("packages a diffStat into a synthetic files_changed activity", () => {
    const built = fileChangesActivity("foo.ts | 3 +-\n1 file changed");
    expect(built.id).toBe("hecate-agent:files-changed");
    expect(built.type).toBe("files_changed");
    expect(built.status).toBe("completed");
    expect(built.title).toBe("Files changed");
    expect(built.detail).toMatch(/^1 file changed/);
  });
});

describe("isTerminalActivity", () => {
  it("returns true when the row has terminal: true regardless of type", () => {
    expect(isTerminalActivity(activity({ type: "tool_call", title: "Ran tool", terminal: true }))).toBe(true);
  });

  it("returns true for type 'completed' / 'failed' / 'cancelled' / 'run_result' even without the flag", () => {
    expect(isTerminalActivity(activity({ type: "completed", title: "Final answer" }))).toBe(true);
    expect(isTerminalActivity(activity({ type: "failed", title: "Failed" }))).toBe(true);
    expect(isTerminalActivity(activity({ type: "cancelled", title: "Cancelled" }))).toBe(true);
    expect(isTerminalActivity(activity({ type: "run_result", title: "Run result" }))).toBe(true);
  });

  it("returns false for non-terminal types without the flag", () => {
    expect(isTerminalActivity(activity({ type: "tool_call", title: "Ran tool" }))).toBe(false);
    expect(isTerminalActivity(activity({ type: "thinking", title: "Agent turn 1" }))).toBe(false);
  });
});

describe("pickTerminalActivityIndex", () => {
  it("prefers the latest terminal:true row over a generic terminal-shaped row", () => {
    const items = [
      activity({ type: "completed", title: "Final answer" }),
      activity({ type: "run_result", title: "Failed", terminal: true }),
    ];
    expect(pickTerminalActivityIndex(items)).toBe(1);
  });

  it("falls back to the latest terminal-shaped row when no flagged row exists", () => {
    const items = [
      activity({ type: "completed", title: "Final answer" }),
      activity({ type: "cancelled", title: "Cancelled" }),
    ];
    expect(pickTerminalActivityIndex(items)).toBe(1);
  });

  it("returns -1 when no terminal-shaped row exists", () => {
    expect(pickTerminalActivityIndex([
      activity({ type: "tool_call", title: "Ran tool" }),
    ])).toBe(-1);
  });
});

describe("compactAgentActivities", () => {
  it("drops hidden types (artifact / changed_files / final_answer / output)", () => {
    const items = [
      activity({ type: "tool_call", title: "Ran tool" }),
      activity({ type: "artifact", title: "Snapshot" }),
      activity({ type: "output", title: "stdout" }),
      activity({ type: "final_answer", title: "Answer" }),
      activity({ type: "changed_files", title: "Files" }),
    ];
    expect(compactAgentActivities(items)).toEqual([items[0]]);
  });

  it("drops generic 'run completed/failed/cancelled' summary rows", () => {
    const items = [
      activity({ type: "tool_call", title: "Ran tool" }),
      activity({ type: "run_result", title: "Run completed" }),
    ];
    expect(compactAgentActivities(items)).toEqual([items[0]]);
  });

  it("dedupes earlier terminal-shaped rows when a later terminal row exists", () => {
    const items = [
      activity({ type: "completed", title: "Final answer" }),
      activity({ type: "tool_call", title: "Ran tool" }),
      activity({ type: "failed", title: "Failed", terminal: true }),
    ];
    const out = compactAgentActivities(items);
    expect(out.map(r => r.type)).toEqual(["tool_call", "failed"]);
  });

  it("collapses repeated 'thinking' agent-turn rows into a single 'model_turns' summary", () => {
    const items = [
      activity({ type: "thinking", title: "Agent turn 1" }),
      activity({ type: "thinking", title: "Agent turn 2" }),
      activity({ type: "thinking", title: "Agent turn 3" }),
    ];
    const out = compactAgentActivities(items);
    expect(out).toHaveLength(1);
    expect(out[0].type).toBe("model_turns");
    expect(out[0].detail).toMatch(/3 model turns/);
  });

  it("keeps only the last task_run row per session", () => {
    const items = [
      activity({ type: "task_run", title: "Task run pending", status: "running" }),
      activity({ type: "task_run", title: "Task run completed", status: "completed" }),
    ];
    expect(compactAgentActivities(items)).toEqual([items[1]]);
  });

  it("keeps only the latest approval row per approval_id", () => {
    const items = [
      activity({ type: "approval", title: "Approval", status: "pending", approval_id: "ap_1" }),
      activity({ type: "approval", title: "Approval", status: "approved", approval_id: "ap_1" }),
    ];
    expect(compactAgentActivities(items)).toEqual([items[1]]);
  });
});

describe("compactDetailActivities", () => {
  it("keeps only the detail-bucket types (artifact / changed_files / final_answer / output)", () => {
    const items = [
      activity({ type: "tool_call", title: "Ran tool" }),
      activity({ type: "artifact", title: "Snapshot" }),
      activity({ type: "output", title: "stdout" }),
      activity({ type: "changed_files", title: "Files" }),
    ];
    expect(compactDetailActivities(items, false).map(r => r.type))
      .toEqual(["artifact", "output", "changed_files"]);
  });

  it("drops changed_files rows when diffStat is provided (it'd duplicate)", () => {
    const items = [
      activity({ type: "artifact", title: "Snapshot" }),
      activity({ type: "changed_files", title: "Files" }),
    ];
    expect(compactDetailActivities(items, true).map(r => r.type)).toEqual(["artifact"]);
  });
});

describe("orderVisibleActivities", () => {
  it("orders by created_at ascending when timestamps are present", () => {
    const items = [
      activity({ type: "tool_call", title: "B", created_at: "2026-01-01T00:00:02Z" }),
      activity({ type: "tool_call", title: "A", created_at: "2026-01-01T00:00:01Z" }),
    ];
    expect(orderVisibleActivities(items).map(r => r.title)).toEqual(["A", "B"]);
  });

  it("falls back to phase order when timestamps tie", () => {
    // started=0 < running=1 < task_run=2 < plan=3 < thinking=4 < approval=5 < tool_call=6
    const time = "2026-01-01T00:00:00Z";
    const items = [
      activity({ type: "tool_call", title: "tool", created_at: time }),
      activity({ type: "started", title: "started", created_at: time }),
      activity({ type: "approval", title: "approval", created_at: time }),
    ];
    expect(orderVisibleActivities(items).map(r => r.type)).toEqual(["started", "approval", "tool_call"]);
  });

  it("uses insertion order as the final tiebreaker", () => {
    const time = "2026-01-01T00:00:00Z";
    const items = [
      activity({ type: "tool_call", title: "first", created_at: time }),
      activity({ type: "tool_call", title: "second", created_at: time }),
    ];
    expect(orderVisibleActivities(items).map(r => r.title)).toEqual(["first", "second"]);
  });
});

describe("activityDisplay", () => {
  it("renders 'Waiting for approval' for a pending approval", () => {
    expect(activityDisplay(activity({ type: "approval", title: "Approval", status: "pending" })).title)
      .toBe("Waiting for approval");
  });

  it("renders 'Approval granted' once approved", () => {
    expect(activityDisplay(activity({ type: "approval", title: "Approval", status: "approved" })).title)
      .toBe("Approval granted");
  });

  it("humanizes a known tool_call name", () => {
    expect(activityDisplay(activity({ type: "tool_call", title: "shell_exec" })).title).toBe("Ran shell");
  });

  it("falls back to 'Used tool' for opaque call ids", () => {
    expect(activityDisplay(activity({ type: "tool_call", title: "call_abc123def456" })).title).toBe("Used tool");
  });

  it("renders the model-turn summary for collapsed model_turns rows", () => {
    expect(activityDisplay(activity({ type: "model_turns", title: "Thinking", detail: "3 model turns completed" }))).toEqual({
      title: "Thinking",
      detail: "3 model turns completed",
    });
  });

  it("renders the files_changed summary directly", () => {
    expect(activityDisplay(activity({ type: "files_changed", title: "Files changed", detail: "2 files changed" }))).toEqual({
      title: "Files changed",
      detail: "2 files changed",
    });
  });

  it("renders 'Starting agent' for the canonical start-row title", () => {
    expect(activityDisplay(activity({ type: "started", title: "Starting Hecate Agent" })).title).toBe("Starting agent");
  });

  it("renders 'Backing task' for task_run rows with a human status", () => {
    const out = activityDisplay(activity({ type: "task_run", title: "Task run", status: "running" }));
    expect(out.title).toBe("Backing task");
    expect(out.detail).toMatch(/running/);
  });

  it("passes through unknown activity titles unchanged", () => {
    expect(activityDisplay(activity({ type: "custom_kind", title: "Custom title", detail: "and detail" }))).toEqual({
      title: "Custom title",
      detail: "and detail",
    });
  });
});

describe("activityLinePrefix", () => {
  it("returns the prefix for known types", () => {
    expect(activityLinePrefix(activity({ type: "tool_call", title: "x" }))).toBe("tool");
    expect(activityLinePrefix(activity({ type: "thinking", title: "x" }))).toBe("model");
    expect(activityLinePrefix(activity({ type: "model_turns", title: "x" }))).toBe("model");
    expect(activityLinePrefix(activity({ type: "approval", title: "x" }))).toBe("approval");
  });

  it("returns undefined for other types", () => {
    expect(activityLinePrefix(activity({ type: "files_changed", title: "x" }))).toBeUndefined();
    expect(activityLinePrefix(activity({ type: "task_run", title: "x" }))).toBeUndefined();
  });
});

describe("isOutputArtifactActivity", () => {
  it("matches stdout / stderr / git-stdout / git-stderr in title, detail, or kind", () => {
    expect(isOutputArtifactActivity(activity({ type: "artifact", title: "stdout snapshot" }))).toBe(true);
    expect(isOutputArtifactActivity(activity({ type: "artifact", title: "Capture", detail: "stderr stream" }))).toBe(true);
    expect(isOutputArtifactActivity(activity({ type: "artifact", title: "Capture", kind: "git-stdout" }))).toBe(true);
  });

  it("returns false for unrelated artifacts", () => {
    expect(isOutputArtifactActivity(activity({ type: "artifact", title: "Snapshot of diff" }))).toBe(false);
  });
});

describe("terminalAgentActivity", () => {
  it("returns the picked terminal row", () => {
    const items = [
      activity({ type: "tool_call", title: "Ran tool" }),
      activity({ type: "completed", title: "Done" }),
    ];
    expect(terminalAgentActivity(items)?.type).toBe("completed");
  });

  it("returns undefined when there's no terminal-shaped row", () => {
    expect(terminalAgentActivity([activity({ type: "tool_call", title: "Ran tool" })])).toBeUndefined();
  });
});

describe("terminalStatusLabel", () => {
  it("maps known terminal statuses to their lowercase label", () => {
    expect(terminalStatusLabel("completed")).toBe("completed");
    expect(terminalStatusLabel("failed")).toBe("failed");
    expect(terminalStatusLabel("cancelled")).toBe("cancelled");
  });

  it("falls back to 'details' when the status is missing", () => {
    expect(terminalStatusLabel()).toBe("details");
  });

  it("returns the raw status for unknown values", () => {
    expect(terminalStatusLabel("paused")).toBe("paused");
  });
});

describe("detailSummaryLabel", () => {
  it("labels output-only buckets as 'Output'", () => {
    expect(detailSummaryLabel([activity({ type: "output", title: "stdout" })])).toBe("Output · 1 item");
  });

  it("labels artifact-only buckets as 'Artifacts'", () => {
    expect(detailSummaryLabel([
      activity({ type: "artifact", title: "snapshot" }),
      activity({ type: "final_answer", title: "answer" }),
    ])).toBe("Artifacts · 2 items");
  });

  it("labels mixed buckets as 'Output and artifacts'", () => {
    expect(detailSummaryLabel([
      activity({ type: "output", title: "stdout" }),
      activity({ type: "artifact", title: "snapshot" }),
    ])).toBe("Output and artifacts · 2 items");
  });

  it("falls back to 'More details' when neither category matches", () => {
    expect(detailSummaryLabel([activity({ type: "changed_files", title: "Files" })])).toBe("More details · 1 item");
  });
});

describe("activityStatusColor", () => {
  it("maps failed to red", () => {
    expect(activityStatusColor("failed")).toBe("var(--red)");
  });

  it("maps cancelled / awaiting_approval / pending / proposed to amber", () => {
    expect(activityStatusColor("cancelled")).toBe("var(--amber)");
    expect(activityStatusColor("awaiting_approval")).toBe("var(--amber)");
    expect(activityStatusColor("pending")).toBe("var(--amber)");
    expect(activityStatusColor("proposed")).toBe("var(--amber)");
  });

  it("maps running / in_progress to teal", () => {
    expect(activityStatusColor("running")).toBe("var(--teal)");
    expect(activityStatusColor("in_progress")).toBe("var(--teal)");
  });

  it("falls back to green for completed and unknown statuses", () => {
    expect(activityStatusColor("completed")).toBe("var(--green)");
    expect(activityStatusColor("something_new")).toBe("var(--green)");
    expect(activityStatusColor()).toBe("var(--green)");
  });
});

describe("isActiveAgentActivity", () => {
  it("returns true for any active status", () => {
    for (const status of ["running", "in_progress", "awaiting_approval", "pending"]) {
      expect(isActiveAgentActivity(activity({ type: "tool_call", title: "x", status }))).toBe(true);
    }
  });

  it("returns true when needs_action is set even if status is not active", () => {
    expect(isActiveAgentActivity(activity({ type: "approval", title: "x", status: "completed", needs_action: true }))).toBe(true);
  });

  it("returns false for completed / failed / cancelled when needs_action is false", () => {
    expect(isActiveAgentActivity(activity({ type: "tool_call", title: "x", status: "completed" }))).toBe(false);
    expect(isActiveAgentActivity(activity({ type: "tool_call", title: "x", status: "failed" }))).toBe(false);
    expect(isActiveAgentActivity(activity({ type: "tool_call", title: "x", status: "cancelled" }))).toBe(false);
  });
});

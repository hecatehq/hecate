import { describe, expect, it } from "vitest";

import type {
  TaskActivityRecord,
  TaskArtifactRecord,
  TaskRecord,
  TaskRunEventRecord,
} from "../../types/task";

import {
  approvalCommandPreview,
  artifactHasBytes,
  buildOutputActivityIndex,
  describeApprovalKind,
  describeRunEvent,
  describeRunEventRecord,
  describeRunEventNote,
  failedToolOutputArtifacts,
  isOutputArtifactActivity,
  isVisibleArtifactBadge,
  isVisibleRunEvent,
  nonInternalKind,
  outputActivityStream,
  splitNamespacedToolName,
  STEP_STATUS_COLOR,
  stepColor,
  summaryNumber,
  summaryString,
  taskActivityAdvancedRows,
  taskActivityArtifactPreview,
  taskActivityArtifactSize,
  taskActivitySubtitle,
  taskActivityTitle,
  taskActivityToTranscriptActivity,
  taskBadgeProps,
  taskBadgeStatus,
  taskRunOutcome,
} from "./taskDetailHelpers";

function activity(
  overrides: Partial<TaskActivityRecord> & Pick<TaskActivityRecord, "type">,
): TaskActivityRecord {
  return {
    id: overrides.id ?? `a_${overrides.type}`,
    ...overrides,
  } as TaskActivityRecord;
}

function task(overrides: Partial<TaskRecord> = {}): TaskRecord {
  return {
    id: "t_1",
    title: "Task",
    prompt: "do thing",
    status: "running",
    ...overrides,
  } as TaskRecord;
}

function artifact(
  overrides: Partial<TaskArtifactRecord> & Pick<TaskArtifactRecord, "kind">,
): TaskArtifactRecord {
  return {
    id: "ar_1",
    task_id: "t_1",
    run_id: "r_1",
    ...overrides,
  } as TaskArtifactRecord;
}

function runEvent(overrides: Partial<TaskRunEventRecord> = {}): TaskRunEventRecord {
  return {
    schema_version: "1",
    event_id: "evt_1",
    task_id: "task_1",
    run_id: "run_1",
    sequence: 1,
    occurred_at: "2026-05-28T00:00:00Z",
    type: "run.started",
    data: {},
    ...overrides,
  };
}

describe("stepColor", () => {
  it("returns the color matching each known status", () => {
    expect(stepColor("completed")).toBe(STEP_STATUS_COLOR.completed);
    expect(stepColor("running")).toBe(STEP_STATUS_COLOR.running);
    expect(stepColor("awaiting_approval")).toBe(STEP_STATUS_COLOR.awaiting_approval);
    expect(stepColor("failed")).toBe(STEP_STATUS_COLOR.failed);
    expect(stepColor("cancelled")).toBe(STEP_STATUS_COLOR.cancelled);
  });

  it("falls back to t3 for unknown status", () => {
    expect(stepColor("queued")).toBe("var(--t3)");
    expect(stepColor("")).toBe("var(--t3)");
  });

  it("renders a denied result as policy amber even with completed status", () => {
    expect(stepColor("completed", "denied")).toBe("var(--amber)");
  });
});

describe("splitNamespacedToolName", () => {
  it("splits 'mcp__server__tool' into its server and tool parts", () => {
    expect(splitNamespacedToolName("mcp__github__create_pr")).toEqual({
      server: "github",
      tool: "create_pr",
    });
  });

  it("honors only the first separator after the server segment", () => {
    expect(splitNamespacedToolName("mcp__weird__double__under")).toEqual({
      server: "weird",
      tool: "double__under",
    });
  });

  it("returns null for non-namespaced names", () => {
    expect(splitNamespacedToolName("shell_exec")).toBeNull();
    expect(splitNamespacedToolName(undefined)).toBeNull();
  });

  it("returns null when server or tool segment is empty", () => {
    expect(splitNamespacedToolName("mcp__")).toBeNull();
    expect(splitNamespacedToolName("mcp____tool")).toBeNull();
    expect(splitNamespacedToolName("mcp__server__")).toBeNull();
  });
});

describe("taskBadgeStatus", () => {
  it("maps 'completed' to 'done' and 'awaiting_approval' to 'awaiting'", () => {
    expect(taskBadgeStatus("completed")).toBe("done");
    expect(taskBadgeStatus("awaiting_approval")).toBe("awaiting");
  });

  it("passes through other statuses unchanged", () => {
    expect(taskBadgeStatus("running")).toBe("running");
    expect(taskBadgeStatus("failed")).toBe("failed");
  });
});

describe("taskBadgeProps", () => {
  it("labels approval rejection separately from generic cancellation", () => {
    expect(taskBadgeProps("cancelled", "approval rejected")).toEqual({
      status: "cancelled",
      label: "rejected",
    });
    expect(taskBadgeProps("cancelled", "operator cancelled")).toEqual({ status: "cancelled" });
  });
});

describe("taskRunOutcome", () => {
  it("describes approval rejection as an intentional stopped outcome", () => {
    expect(taskRunOutcome("cancelled", "approval rejected")).toEqual({
      label: "Outcome",
      value: "Approval rejected",
      tone: "warning",
      detail: "The run was stopped because the pending approval was rejected.",
    });
  });

  it("keeps failed runs on the last-error path", () => {
    expect(taskRunOutcome("failed", "provider unavailable")).toEqual({
      label: "Last error",
      value: "provider unavailable",
      tone: "error",
    });
  });

  it("describes generic cancellation separately from approval rejection", () => {
    expect(taskRunOutcome("cancelled", "operator stopped run")).toEqual({
      label: "Reason",
      value: "operator stopped run",
      tone: "warning",
    });
  });
});

describe("approvalCommandPreview", () => {
  it("formats a git command as 'git <command>'", () => {
    expect(
      approvalCommandPreview(
        task({
          execution_kind: "git",
          git_command: "push --force",
        }),
      ),
    ).toBe("git push --force");
  });

  it("returns the raw shell command when present", () => {
    expect(
      approvalCommandPreview(
        task({
          shell_command: "rm -rf /tmp/foo",
        }),
      ),
    ).toBe("rm -rf /tmp/foo");
  });

  it("formats a file write as '<op> <path>' with 'write' as the default op", () => {
    expect(
      approvalCommandPreview(
        task({
          file_path: "/tmp/foo.txt",
        }),
      ),
    ).toBe("write /tmp/foo.txt");
    expect(
      approvalCommandPreview(
        task({
          file_operation: "delete",
          file_path: "/tmp/foo.txt",
        }),
      ),
    ).toBe("delete /tmp/foo.txt");
  });

  it("returns empty string when nothing useful is set", () => {
    expect(approvalCommandPreview(task())).toBe("");
  });

  it("ignores git_command unless execution_kind is 'git'", () => {
    expect(approvalCommandPreview(task({ git_command: "status" }))).toBe("");
  });
});

describe("describeRunEvent", () => {
  it("returns the canonical label + tone for known event types", () => {
    expect(describeRunEvent("run.started")).toEqual({ label: "Started", tone: "running" });
    expect(describeRunEvent("run.failed")).toEqual({ label: "Failed", tone: "failed" });
    expect(describeRunEvent("tool.completed")).toEqual({ label: "Tool done", tone: "done" });
    expect(describeRunEvent("policy.tool_blocked")).toEqual({
      label: "Tool blocked",
      tone: "warn",
    });
    expect(describeRunEvent("approval.requested")).toEqual({
      label: "Approval asked",
      tone: "awaiting",
    });
  });

  it("humanizes unknown event types by un-underscoring the type name", () => {
    expect(describeRunEvent("custom.my_event_type")).toEqual({
      label: "custom.my event type",
      tone: "queued",
    });
  });
});

describe("describeRunEventRecord", () => {
  it("labels approval resolution by the actual decision", () => {
    const event = runEvent({
      type: "approval.resolved",
      data: { decision: "rejected" },
    });

    expect(describeRunEventRecord(event)).toEqual({
      label: "Approval rejected",
      tone: "awaiting",
    });
  });

  it("labels approval-rejected cancellation separately from generic cancellation", () => {
    const event = runEvent({
      type: "run.cancelled",
      data: { reason: "approval rejected" },
    });

    expect(describeRunEventRecord(event)).toEqual({
      label: "Approval rejected",
      tone: "awaiting",
    });
    expect(describeRunEventRecord(runEvent({ type: "run.cancelled", data: {} }))).toEqual({
      label: "Cancelled",
      tone: "failed",
    });
  });
});

describe("isVisibleRunEvent", () => {
  it("hides snapshot and run.snapshot events", () => {
    expect(isVisibleRunEvent({ type: "snapshot" } as Parameters<typeof isVisibleRunEvent>[0])).toBe(
      false,
    );
    expect(
      isVisibleRunEvent({ type: "run.snapshot" } as Parameters<typeof isVisibleRunEvent>[0]),
    ).toBe(false);
  });

  it("returns true for any other type", () => {
    expect(
      isVisibleRunEvent({ type: "run.started" } as Parameters<typeof isVisibleRunEvent>[0]),
    ).toBe(true);
  });
});

describe("describeRunEventNote", () => {
  it("returns null when data is absent", () => {
    expect(describeRunEventNote({})).toBeNull();
  });

  it("returns null when neither retry_from_turn nor reason is present", () => {
    expect(describeRunEventNote({ data: { other: "x" } })).toBeNull();
    expect(describeRunEventNote({ data: { reason: "   " } })).toBeNull();
  });

  it("renders 'turn N' when retry_from_turn is a number", () => {
    expect(describeRunEventNote({ data: { retry_from_turn: 3 } })).toBe("turn 3");
  });

  it("renders the trimmed reason on its own", () => {
    expect(describeRunEventNote({ data: { reason: "  retried after timeout  " } })).toBe(
      "retried after timeout",
    );
  });

  it("joins turn and reason with em-dash separator", () => {
    expect(
      describeRunEventNote({ data: { retry_from_turn: 2, reason: "operator branched" } }),
    ).toBe("turn 2 — operator branched");
  });
});

describe("describeApprovalKind", () => {
  it("returns the canonical label for known kinds", () => {
    expect(describeApprovalKind("shell_command")).toBe("Shell execution");
    expect(describeApprovalKind("git_exec")).toBe("Git execution");
    expect(describeApprovalKind("file_write")).toBe("File write");
    expect(describeApprovalKind("network_egress")).toBe("Network egress");
    expect(describeApprovalKind("agent_loop_tool_call")).toBe("Agent tool call");
  });

  it("humanizes unknown kinds by replacing underscores with spaces", () => {
    expect(describeApprovalKind("custom_thing")).toBe("custom thing");
  });
});

describe("buildOutputActivityIndex", () => {
  it("keeps only artifact activities that look like output streams", () => {
    const items = [
      activity({ id: "1", type: "artifact", title: "stdout", artifact_id: "ar_1" }),
      activity({ id: "2", type: "tool_call", tool_name: "shell" }),
      activity({ id: "3", type: "artifact", title: "snapshot" }),
    ];
    const idx = buildOutputActivityIndex(items);
    expect(idx.all.map((a) => a.id)).toEqual(["1"]);
  });

  it("dedupes by artifact_id (or id when artifact_id is absent)", () => {
    const items = [
      activity({ id: "1", type: "artifact", title: "stdout", artifact_id: "ar_x" }),
      activity({ id: "2", type: "artifact", title: "stdout", artifact_id: "ar_x" }),
    ];
    expect(buildOutputActivityIndex(items).all).toHaveLength(1);
  });

  it("indexes output activities by step_id when present", () => {
    const items = [
      activity({ id: "1", type: "artifact", title: "stdout", step_id: "s_1" }),
      activity({ id: "2", type: "artifact", title: "stderr", step_id: "s_1" }),
      activity({ id: "3", type: "artifact", title: "stdout", step_id: "s_2" }),
    ];
    const idx = buildOutputActivityIndex(items);
    expect(idx.byStepID.get("s_1")?.map((a) => a.id)).toEqual(["1", "2"]);
    expect(idx.byStepID.get("s_2")?.map((a) => a.id)).toEqual(["3"]);
  });

  it("skips the byStepID grouping when step_id is missing", () => {
    const items = [activity({ id: "1", type: "artifact", title: "stdout" })];
    expect(buildOutputActivityIndex(items).byStepID.size).toBe(0);
  });
});

describe("failedToolOutputArtifacts", () => {
  const outputs = buildOutputActivityIndex([
    activity({ id: "o1", type: "artifact", title: "stdout", step_id: "s_a" }),
    activity({ id: "o2", type: "artifact", title: "stderr" }),
  ]);

  it("returns nothing for non-failed tool calls", () => {
    expect(
      failedToolOutputArtifacts(activity({ type: "tool_call", status: "completed" }), outputs),
    ).toEqual([]);
    expect(
      failedToolOutputArtifacts(activity({ type: "approval", status: "failed" }), outputs),
    ).toEqual([]);
  });

  it("scopes to the failing tool call's step_id when present", () => {
    expect(
      failedToolOutputArtifacts(
        activity({ type: "tool_call", status: "failed", step_id: "s_a" }),
        outputs,
      )?.map((a) => a.id),
    ).toEqual(["o1"]);
  });

  it("falls back to all output artifacts when the step is unknown", () => {
    expect(
      failedToolOutputArtifacts(activity({ type: "tool_call", status: "failed" }), outputs).map(
        (a) => a.id,
      ),
    ).toEqual(["o1", "o2"]);
  });
});

describe("outputActivityStream", () => {
  it("returns 'stdout' / 'stderr' based on the combined kind/title/path label", () => {
    expect(outputActivityStream(activity({ type: "artifact", title: "stdout snapshot" }))).toBe(
      "stdout",
    );
    expect(outputActivityStream(activity({ type: "artifact", kind: "git-stderr" }))).toBe("stderr");
    expect(outputActivityStream(activity({ type: "artifact", path: "/tmp/stdout-1.log" }))).toBe(
      "stdout",
    );
  });

  it("prefers stderr over stdout when both labels match", () => {
    expect(
      outputActivityStream(
        activity({
          type: "artifact",
          title: "stderr capture",
          kind: "stdout-style",
        }),
      ),
    ).toBe("stderr");
  });

  it("returns empty string when nothing matches", () => {
    expect(outputActivityStream(activity({ type: "artifact", title: "snapshot of diff" }))).toBe(
      "",
    );
  });
});

describe("isOutputArtifactActivity", () => {
  it("delegates to outputActivityStream", () => {
    expect(isOutputArtifactActivity(activity({ type: "artifact", title: "stdout" }))).toBe(true);
    expect(
      isOutputArtifactActivity(activity({ type: "artifact", title: "snapshot of diff" })),
    ).toBe(false);
  });
});

describe("taskActivityAdvancedRows", () => {
  it("emits one row per non-empty field present", () => {
    const rows = taskActivityAdvancedRows(
      activity({
        type: "tool_call",
        status: "completed",
        occurred_at: "2026-05-17T00:00:00Z",
        tool_name: "shell",
        step_id: "s_1",
      }),
    );
    expect(rows.map((r) => r.label)).toContain("type");
    expect(rows.map((r) => r.label)).toContain("status");
    expect(rows.map((r) => r.label)).toContain("occurred");
    expect(rows.map((r) => r.label)).toContain("tool");
    expect(rows.map((r) => r.label)).toContain("step");
  });

  it("emits 'needs action' and 'terminal' only for truthy booleans", () => {
    const both = taskActivityAdvancedRows(
      activity({
        type: "approval",
        needs_action: true,
        terminal: true,
      }),
    );
    expect(both.find((r) => r.label === "needs action")?.value).toBe("yes");
    expect(both.find((r) => r.label === "terminal")?.value).toBe("yes");

    const neither = taskActivityAdvancedRows(
      activity({
        type: "approval",
        needs_action: false,
        terminal: false,
      }),
    );
    expect(neither.find((r) => r.label === "needs action")).toBeUndefined();
    expect(neither.find((r) => r.label === "terminal")).toBeUndefined();
  });

  it("emits a multiline summary row when summary has any keys", () => {
    const rows = taskActivityAdvancedRows(
      activity({
        type: "tool_call",
        summary: { tokens: 10, kind: "shell" },
      }),
    );
    const summaryRow = rows.find((r) => r.label === "summary");
    expect(summaryRow?.multiline).toBe(true);
    expect(summaryRow?.value).toContain('"tokens": 10');
  });

  it("omits the summary row when summary is missing or empty", () => {
    const noSummary = taskActivityAdvancedRows(activity({ type: "tool_call" }));
    expect(noSummary.find((r) => r.label === "summary")).toBeUndefined();

    const emptySummary = taskActivityAdvancedRows(activity({ type: "tool_call", summary: {} }));
    expect(emptySummary.find((r) => r.label === "summary")).toBeUndefined();
  });
});

describe("taskActivityToTranscriptActivity", () => {
  it("maps an activity to the transcript shape", () => {
    const out = taskActivityToTranscriptActivity(
      activity({
        id: "a_1",
        type: "tool_call",
        status: "completed",
        tool_name: "shell_exec",
        occurred_at: "2026-05-17T00:00:00Z",
        summary: { size_bytes: 42, content_preview: "hello world\n" },
      }),
    );
    expect(out.id).toBe("a_1");
    expect(out.type).toBe("tool_call");
    expect(out.status).toBe("completed");
    expect(out.title).toBe("shell_exec");
    expect(out.created_at).toBe("2026-05-17T00:00:00Z");
    expect(out.artifact_size_bytes).toBe(42);
    expect(out.artifact_preview).toBe("hello world");
  });

  it("rewrites status to 'awaiting_approval' when the activity needs action", () => {
    const out = taskActivityToTranscriptActivity(
      activity({
        type: "approval",
        status: "pending",
        needs_action: true,
      }),
    );
    expect(out.status).toBe("awaiting_approval");
  });

  it("preserves a policy denial and exposes its reason", () => {
    const out = taskActivityToTranscriptActivity(
      activity({
        type: "tool_call",
        status: "denied",
        terminal: true,
        tool_name: "shell_exec",
        summary: { reason: "writes are disabled by the resolved agent preset" },
      }),
    );
    expect(out.status).toBe("denied");
    expect(out.title).toBe("Blocked shell_exec");
    expect(out.detail).toBe("writes are disabled by the resolved agent preset");
    expect(out.terminal).toBeUndefined();
  });

  it("reserves transcript terminal semantics for run results", () => {
    expect(
      taskActivityToTranscriptActivity(
        activity({ type: "thinking", status: "completed", terminal: true }),
      ).terminal,
    ).toBeUndefined();
    expect(
      taskActivityToTranscriptActivity(
        activity({ type: "run_result", status: "completed", terminal: true }),
      ).terminal,
    ).toBe(true);
  });
});

describe("taskActivityArtifactSize", () => {
  it("returns the numeric summary size when present", () => {
    expect(
      taskActivityArtifactSize(activity({ type: "artifact", summary: { size_bytes: 100 } })),
    ).toBe(100);
  });

  it("returns undefined when size_bytes is missing or non-numeric", () => {
    expect(taskActivityArtifactSize(activity({ type: "artifact" }))).toBeUndefined();
    expect(
      taskActivityArtifactSize(activity({ type: "artifact", summary: { size_bytes: "100" } })),
    ).toBeUndefined();
  });
});

describe("taskActivityArtifactPreview", () => {
  it("returns the preview string with trailing CR/LF stripped", () => {
    expect(
      taskActivityArtifactPreview(
        activity({
          type: "artifact",
          summary: { content_preview: "hello world\r\n" },
        }),
      ),
    ).toBe("hello world");
  });

  it("returns undefined when preview is missing, blank, or non-string", () => {
    expect(taskActivityArtifactPreview(activity({ type: "artifact" }))).toBeUndefined();
    expect(
      taskActivityArtifactPreview(
        activity({ type: "artifact", summary: { content_preview: "   \n" } }),
      ),
    ).toBeUndefined();
    expect(
      taskActivityArtifactPreview(activity({ type: "artifact", summary: { content_preview: 42 } })),
    ).toBeUndefined();
  });
});

describe("artifactHasBytes", () => {
  it("returns true when size_bytes is a positive number", () => {
    expect(artifactHasBytes(artifact({ kind: "stdout", size_bytes: 1 }))).toBe(true);
  });

  it("returns false when size_bytes is zero", () => {
    expect(artifactHasBytes(artifact({ kind: "stdout", size_bytes: 0 }))).toBe(false);
  });

  it("falls back to content_text when size_bytes is missing", () => {
    expect(artifactHasBytes(artifact({ kind: "stdout", content_text: "hi" }))).toBe(true);
    expect(artifactHasBytes(artifact({ kind: "stdout" }))).toBe(false);
  });
});

describe("taskActivityTitle", () => {
  it("renders approval titles based on status / needs_action", () => {
    expect(taskActivityTitle(activity({ type: "approval", needs_action: true }))).toBe(
      "Waiting for approval",
    );
    expect(taskActivityTitle(activity({ type: "approval", status: "pending" }))).toBe(
      "Waiting for approval",
    );
    expect(taskActivityTitle(activity({ type: "approval", status: "awaiting_approval" }))).toBe(
      "Waiting for approval",
    );
    expect(taskActivityTitle(activity({ type: "approval", status: "approved" }))).toBe(
      "Approval granted",
    );
    expect(taskActivityTitle(activity({ type: "approval", status: "rejected" }))).toBe(
      "Approval rejected",
    );
    expect(taskActivityTitle(activity({ type: "approval", status: "denied" }))).toBe(
      "Approval rejected",
    );
    expect(taskActivityTitle(activity({ type: "approval", status: "other" }))).toBe("Approval");
  });

  it("renders the canonical type label for artifact / changed_files / final_answer / patch", () => {
    expect(taskActivityTitle(activity({ type: "artifact" }))).toBe("Artifact");
    expect(
      taskActivityTitle(activity({ type: "artifact", title: "git-stdout.txt", kind: "stdout" })),
    ).toBe("Output");
    expect(
      taskActivityTitle(
        activity({
          type: "artifact",
          title: "agent-conversation.json",
          kind: "agent_conversation",
        }),
      ),
    ).toBe("Agent conversation");
    expect(taskActivityTitle(activity({ type: "changed_files" }))).toBe("Changed files");
    expect(taskActivityTitle(activity({ type: "final_answer" }))).toBe("Final answer");
    expect(taskActivityTitle(activity({ type: "patch" }))).toBe("Patch");
  });

  it("prefers tool_name then title then path for tool_call rows", () => {
    expect(taskActivityTitle(activity({ type: "tool_call", tool_name: "shell" }))).toBe("shell");
    expect(taskActivityTitle(activity({ type: "tool_call", title: "T", path: "P" }))).toBe("T");
    expect(taskActivityTitle(activity({ type: "tool_call", path: "P" }))).toBe("P");
    expect(taskActivityTitle(activity({ type: "tool_call" }))).toBe("tool");
  });

  it("falls back to title / tool_name / path / humanized type for unknown types", () => {
    expect(taskActivityTitle(activity({ type: "weird", title: "Wat" }))).toBe("Wat");
    expect(taskActivityTitle(activity({ type: "my_event" }))).toBe("my event");
  });
});

describe("taskActivitySubtitle", () => {
  it("joins parts with ' · ' separators", () => {
    expect(
      taskActivitySubtitle(
        activity({
          type: "approval",
          summary: { reason: "limit exceeded" },
          kind: "shell_command",
        }),
      ),
    ).toBe("limit exceeded · shell_command");
  });

  it("hides internal builtin.agent_loop_* kinds", () => {
    expect(
      taskActivitySubtitle(
        activity({
          type: "approval",
          summary: { reason: "auto-resolved" },
          kind: "builtin.agent_loop_tool",
        }),
      ),
    ).toBe("auto-resolved");
  });

  it("renders tool_call rows as 'path · command'", () => {
    expect(
      taskActivitySubtitle(
        activity({
          type: "tool_call",
          path: "/tmp/foo",
          summary: { command: "ls" },
        }),
      ),
    ).toBe("/tmp/foo · ls");
  });

  it("renders the reason before policy-denied tool details", () => {
    expect(
      taskActivitySubtitle(
        activity({
          type: "tool_call",
          status: "denied",
          path: "/tmp/foo",
          summary: {
            command: "rm /tmp/foo",
            reason: "writes are disabled by the resolved agent preset",
          },
        }),
      ),
    ).toBe("writes are disabled by the resolved agent preset · /tmp/foo · rm /tmp/foo");
  });

  it("hides the 'ready' status on artifact-shaped rows", () => {
    expect(
      taskActivitySubtitle(
        activity({
          type: "artifact",
          path: "foo.txt",
          status: "ready",
        }),
      ),
    ).toBe("foo.txt");
    expect(
      taskActivitySubtitle(
        activity({
          type: "artifact",
          path: "foo.txt",
          status: "stored",
        }),
      ),
    ).toBe("foo.txt · stored");
  });

  it("returns undefined when no parts remain", () => {
    expect(taskActivitySubtitle(activity({ type: "tool_call" }))).toBeUndefined();
  });
});

describe("summaryString / summaryNumber", () => {
  it("returns the trimmed string value at the given key", () => {
    expect(summaryString(activity({ type: "x", summary: { k: "  v  " } }), "k")).toBe("v");
  });

  it("returns an empty string when the value is missing or non-string", () => {
    expect(summaryString(activity({ type: "x" }), "k")).toBe("");
    expect(summaryString(activity({ type: "x", summary: { k: 42 } }), "k")).toBe("");
  });

  it("returns the numeric value at the given key only when finite", () => {
    expect(summaryNumber(activity({ type: "x", summary: { k: 7 } }), "k")).toBe(7);
    expect(summaryNumber(activity({ type: "x", summary: { k: Infinity } }), "k")).toBeUndefined();
    expect(summaryNumber(activity({ type: "x", summary: { k: "7" } }), "k")).toBeUndefined();
    expect(summaryNumber(activity({ type: "x" }), "k")).toBeUndefined();
  });
});

describe("nonInternalKind", () => {
  it("hides any builtin.agent_loop_* kind", () => {
    expect(nonInternalKind("builtin.agent_loop_tool_call")).toBe("");
    expect(nonInternalKind("builtin.agent_loop_approval")).toBe("");
  });

  it("returns the trimmed kind for everything else", () => {
    expect(nonInternalKind("  shell_command  ")).toBe("shell_command");
    expect(nonInternalKind(undefined)).toBe("");
  });
});

describe("isVisibleArtifactBadge", () => {
  it("hides stdout / stderr / agent_conversation artifacts", () => {
    expect(isVisibleArtifactBadge(artifact({ kind: "stdout" }))).toBe(false);
    expect(isVisibleArtifactBadge(artifact({ kind: "stderr" }))).toBe(false);
    expect(isVisibleArtifactBadge(artifact({ kind: "agent_conversation" }))).toBe(false);
    expect(isVisibleArtifactBadge(artifact({ kind: "browser_evidence" }))).toBe(false);
  });

  it("shows other artifact kinds", () => {
    expect(isVisibleArtifactBadge(artifact({ kind: "snapshot" }))).toBe(true);
    expect(isVisibleArtifactBadge(artifact({ kind: "patch" }))).toBe(true);
  });
});

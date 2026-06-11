import { describe, expect, it } from "vitest";

import {
  toProjectActivityAssignmentExecutionViewModel,
  toProjectAssignmentExecutionViewModel,
} from "./projectAssignmentViewModels";
import type { ProjectActivityItemRecord, ProjectAssignmentRecord } from "../../types/project";

describe("projectAssignmentViewModels", () => {
  it("prefers canonical execution_ref over legacy assignment fields", () => {
    const execution = toProjectAssignmentExecutionViewModel({
      id: "assign_1",
      project_id: "proj_1",
      work_item_id: "work_1",
      role_id: "developer",
      driver_kind: "hecate_task",
      status: "queued",
      task_id: "task_legacy",
      run_id: "run_legacy",
      execution_ref: {
        kind: "task_run",
        task_id: "task_ref",
        run_id: "run_ref",
        status: "running",
        pending_approval_count: 2,
        trace_id: "trace_ref",
      },
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    });

    expect(execution.kind).toBe("task_run");
    expect(execution.taskID).toBe("task_ref");
    expect(execution.runID).toBe("run_ref");
    expect(execution.status).toBe("running");
    expect(execution.pendingApprovalCount).toBe(2);
    expect(execution.traceID).toBe("trace_ref");
    expect(execution.hasTaskRun).toBe(true);
  });

  it("falls back to projected execution before raw links", () => {
    const execution = toProjectAssignmentExecutionViewModel({
      id: "assign_1",
      project_id: "proj_1",
      work_item_id: "work_1",
      role_id: "developer",
      driver_kind: "hecate_task",
      status: "queued",
      task_id: "task_raw",
      run_id: "run_raw",
      execution: {
        task_id: "task_projected",
        run_id: "run_projected",
        status: "completed",
        missing: true,
      },
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    });

    expect(execution.kind).toBe("task_run");
    expect(execution.taskID).toBe("task_projected");
    expect(execution.runID).toBe("run_projected");
    expect(execution.status).toBe("completed");
    expect(execution.missing).toBe(true);
  });

  it("uses activity linked refs ahead of assignment refs", () => {
    const assignment: ProjectAssignmentRecord = {
      id: "assign_1",
      project_id: "proj_1",
      work_item_id: "work_1",
      role_id: "developer",
      driver_kind: "external_agent",
      status: "running",
      chat_session_id: "chat_assignment",
      message_id: "msg_assignment",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    };
    const item = {
      id: "activity_1",
      project_id: "proj_1",
      work_item: {
        id: "work_1",
        title: "Build",
        status: "active",
        priority: "normal",
      },
      assignment,
      role: {
        id: "developer",
        project_id: "proj_1",
        name: "Developer",
        built_in: false,
      },
      status: "running",
      blocking_signal: "running",
      status_summary: "running",
      linked_chat_id: "chat_activity",
      linked_message_id: "msg_activity",
      artifact_summary: { count: 0 },
      updated_at: "2026-01-01T00:00:00Z",
    } satisfies ProjectActivityItemRecord;

    const execution = toProjectActivityAssignmentExecutionViewModel(item);

    expect(execution.kind).toBe("chat_session");
    expect(execution.chatSessionID).toBe("chat_activity");
    expect(execution.messageID).toBe("msg_activity");
    expect(execution.hasChatSession).toBe(true);
  });

  it("infers context-only assignments", () => {
    const execution = toProjectAssignmentExecutionViewModel({
      id: "assign_1",
      project_id: "proj_1",
      work_item_id: "work_1",
      role_id: "reviewer",
      driver_kind: "hecate_task",
      status: "queued",
      context_snapshot_id: "ctx_1",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    });

    expect(execution.kind).toBe("context_snapshot");
    expect(execution.contextSnapshotID).toBe("ctx_1");
    expect(execution.hasContextSnapshot).toBe(true);
    expect(execution.hasAnyLink).toBe(true);
  });
});

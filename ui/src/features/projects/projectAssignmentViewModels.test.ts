import { describe, expect, it } from "vitest";

import {
  toProjectActivityAssignmentExecutionViewModel,
  toProjectActivityItemViewModel,
  toProjectAssignmentExecutionViewModel,
} from "./projectAssignmentViewModels";
import type { ProjectActivityItemRecord, ProjectAssignmentRecord } from "../../types/project";

describe("projectAssignmentViewModels", () => {
  it("uses canonical execution_ref for task-run links", () => {
    const execution = toProjectAssignmentExecutionViewModel({
      id: "assign_1",
      project_id: "proj_1",
      work_item_id: "work_1",
      role_id: "developer",
      driver_kind: "hecate_task",
      status: "queued",
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

  it("does not reconstruct runtime links from execution summaries", () => {
    const execution = toProjectAssignmentExecutionViewModel({
      id: "assign_1",
      project_id: "proj_1",
      work_item_id: "work_1",
      role_id: "developer",
      driver_kind: "hecate_task",
      status: "queued",
      execution: {
        task_id: "task_projected",
        run_id: "run_projected",
        status: "completed",
        missing: true,
      },
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    });

    expect(execution.kind).toBe("none");
    expect(execution.taskID).toBe("");
    expect(execution.runID).toBe("");
    expect(execution.status).toBe("queued");
    expect(execution.missing).toBe(false);
    expect(execution.hasAnyLink).toBe(false);
  });

  it("uses activity linked refs ahead of assignment refs", () => {
    const assignment: ProjectAssignmentRecord = {
      id: "assign_1",
      project_id: "proj_1",
      work_item_id: "work_1",
      role_id: "developer",
      driver_kind: "external_agent",
      status: "running",
      execution_ref: {
        kind: "chat_session",
        chat_session_id: "chat_assignment",
        message_id: "msg_assignment",
        status: "running",
      },
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

  it("uses explicit context-only execution refs", () => {
    const execution = toProjectAssignmentExecutionViewModel({
      id: "assign_1",
      project_id: "proj_1",
      work_item_id: "work_1",
      role_id: "reviewer",
      driver_kind: "hecate_task",
      status: "queued",
      execution_ref: {
        kind: "context_snapshot",
        context_snapshot_id: "ctx_1",
        status: "queued",
      },
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    });

    expect(execution.kind).toBe("context_snapshot");
    expect(execution.contextSnapshotID).toBe("ctx_1");
    expect(execution.hasContextSnapshot).toBe(true);
    expect(execution.hasAnyLink).toBe(true);
  });

  it("builds activity item view models from canonical execution refs", () => {
    const item = {
      id: "activity_1",
      project_id: "proj_1",
      work_item: {
        id: "work_1",
        title: "Build",
        status: "running",
        priority: "normal",
      },
      assignment: {
        id: "assign_1",
        project_id: "proj_1",
        work_item_id: "work_1",
        role_id: "developer",
        driver_kind: "hecate_task",
        status: "queued",
        execution_ref: {
          kind: "task_run",
          task_id: "task_1",
          run_id: "run_1",
          status: "awaiting_approval",
          pending_approval_count: 2,
        },
        execution: {
          started_at: "2026-01-01T00:00:00Z",
          finished_at: "2026-01-01T00:03:00Z",
        },
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:01:00Z",
      },
      role: {
        id: "developer",
        project_id: "proj_1",
        name: "Developer",
        built_in: true,
      },
      status: "awaiting_approval",
      blocking_signal: "awaiting_approval",
      status_summary: "2 approval pending",
      linked_task_id: "task_1",
      linked_run_id: "run_1",
      artifact_summary: { count: 0 },
      handoff_summary: { count: 0 },
      updated_at: "2026-01-01T00:01:00Z",
    } satisfies ProjectActivityItemRecord;

    const view = toProjectActivityItemViewModel(item);

    expect(view.execution.taskID).toBe("task_1");
    expect(view.execution.pendingApprovalCount).toBe(2);
    expect(view.status).toBe("awaiting_approval");
    expect(view.blockingSignal).toBe("awaiting_approval");
    expect(view.bucket).toBe("blocked");
    expect(view.startedAt).toBe("2026-01-01T00:00:00Z");
    expect(view.finishedAt).toBe("2026-01-01T00:03:00Z");
  });
});

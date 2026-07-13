import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectMemoryRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { ProjectTimelinePanel } from "./ProjectTimelinePanel";

function project(overrides: Partial<ProjectRecord> = {}): ProjectRecord {
  return {
    id: "proj_1",
    name: "Hecate",
    roots: [],
    default_provider: "openai",
    default_model: "gpt-5",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function role(overrides: Partial<ProjectWorkRoleRecord> = {}): ProjectWorkRoleRecord {
  return {
    id: "developer",
    project_id: "proj_1",
    name: "Developer",
    default_provider: "openai",
    default_model: "gpt-5",
    built_in: false,
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function workItem(overrides: Partial<ProjectWorkItemRecord> = {}): ProjectWorkItemRecord {
  return {
    id: "work_1",
    project_id: "proj_1",
    title: "Extract timeline",
    brief: "Move timeline presentation.",
    status: "running",
    priority: "normal",
    owner_role_id: "developer",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-13T09:00:00Z",
    ...overrides,
  };
}

function assignment(overrides: Partial<ProjectAssignmentRecord> = {}): ProjectAssignmentRecord {
  return {
    id: "assign_1",
    project_id: "proj_1",
    work_item_id: "work_1",
    role_id: "developer",
    driver_kind: "hecate_task",
    status: "running",
    execution_ref: {
      kind: "task_run",
      task_id: "task_123",
      run_id: "run_123",
      status: "running",
    },
    execution: {
      provider: "openai",
      model: "gpt-5",
      status: "running",
    },
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-13T09:00:00Z",
    ...overrides,
  };
}

function activityItem(): ProjectActivityItemRecord {
  const item = workItem();
  const assigned = assignment();
  return {
    id: "activity_1",
    project_id: "proj_1",
    work_item: {
      id: item.id,
      title: item.title,
      status: item.status,
      priority: item.priority,
    },
    assignment: assigned,
    role: role(),
    status: "running",
    blocking_signal: "running",
    status_summary: "Task is running",
    linked_task_id: "task_123",
    linked_run_id: "run_123",
    artifact_summary: { count: 0 },
    updated_at: "2026-06-13T09:00:00Z",
  };
}

function activity(): ProjectActivityData {
  const item = activityItem();
  return {
    project_id: "proj_1",
    summary: {
      work_item_count: 1,
      assignment_count: 1,
      active_count: 1,
      blocked_count: 0,
      completed_count: 0,
      recent_count: 1,
    },
    buckets: {
      active: [item],
      blocked: [],
      completed: [],
      recent: [],
    },
    recent: [],
  };
}

function decisionArtifact(
  overrides: Partial<ProjectCollaborationArtifactRecord> = {},
): ProjectCollaborationArtifactRecord {
  return {
    id: "artifact_decision",
    project_id: "proj_1",
    work_item_id: "work_1",
    kind: "decision_note",
    title: "Use panel extraction",
    body: "Keep the parent view focused on orchestration.",
    author_role_id: "architect",
    created_at: "2026-06-13T08:00:00Z",
    updated_at: "2026-06-13T08:00:00Z",
    ...overrides,
  };
}

function memoryEntry(overrides: Partial<ProjectMemoryRecord> = {}): ProjectMemoryRecord {
  return {
    id: "mem_1",
    scope: "project",
    project_id: "proj_1",
    title: "UI lane",
    body: "Keep project panels extracted.",
    trust_label: "operator_memory",
    source_kind: "operator",
    enabled: true,
    created_at: "2026-06-13T07:00:00Z",
    updated_at: "2026-06-13T07:00:00Z",
    ...overrides,
  };
}

describe("ProjectTimelinePanel", () => {
  it("renders projected timeline rows and delegates row actions", async () => {
    const handlers = {
      onEditMemory: vi.fn(),
      onOpenChat: vi.fn(),
      onOpenTask: vi.fn(),
      onSelectWorkItem: vi.fn(),
    };
    const entry = memoryEntry();

    render(
      <ProjectTimelinePanel
        activity={activity()}
        artifacts={[decisionArtifact()]}
        handoffs={[]}
        memoryCandidates={[]}
        memoryEntries={[entry]}
        project={project()}
        roles={[role()]}
        workItems={[workItem()]}
        {...handlers}
      />,
    );

    expect(screen.getByRole("heading", { level: 1, name: "Timeline" })).toBeTruthy();
    expect(screen.getByText("3 story items from work, memory, and collaboration.")).toBeTruthy();
    expect(screen.getByText("Extract timeline")).toBeTruthy();
    expect(screen.getAllByText("Use panel extraction")).toHaveLength(2);
    expect(screen.getByText("Context memory: UI lane")).toBeTruthy();
    expect(screen.getByText("Decision", { selector: "span" })).toBeTruthy();

    await userEvent.click(screen.getAllByText("Technical references", { selector: "summary" })[0]!);
    expect(screen.getByText("run run_123")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Open timeline task task_123" }));
    await userEvent.click(
      screen.getByRole("button", { name: "Open timeline chat for Extract timeline" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Inspect" }));
    await userEvent.click(
      screen.getByRole("button", { name: "Show decision details for Use panel extraction" }),
    );

    expect(handlers.onOpenTask).toHaveBeenCalledWith("task_123", "run_123");
    expect(handlers.onOpenChat).toHaveBeenCalledWith(
      expect.objectContaining({
        projectID: "proj_1",
        provider: "openai",
        model: "gpt-5",
      }),
    );
    expect(handlers.onEditMemory).toHaveBeenCalledWith(entry);
    expect(handlers.onSelectWorkItem).toHaveBeenCalledWith("work_1");
  });

  it("renders empty guidance when no project story exists", () => {
    render(
      <ProjectTimelinePanel
        activity={null}
        artifacts={[]}
        handoffs={[]}
        memoryCandidates={[]}
        memoryEntries={[]}
        project={project()}
        roles={[]}
        workItems={[]}
        onEditMemory={vi.fn()}
        onSelectWorkItem={vi.fn()}
      />,
    );

    expect(
      screen.getByText(
        "No timeline entries yet. Assignments, memory changes, and collaboration artifacts will appear here.",
      ),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "No decision notes yet. Recorded collaboration decisions will appear here without creating new records automatically.",
      ),
    ).toBeTruthy();
  });
});

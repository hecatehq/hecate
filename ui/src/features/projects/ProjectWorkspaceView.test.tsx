import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ProjectAssignmentRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
} from "../../types/project";
import {
  ProjectWorkspaceView,
  summarizeAssignments,
  type ProjectWorkspaceViewProps,
} from "./ProjectWorkspaceView";

vi.mock("./ProjectAssistantPanel", () => ({
  ProjectAssistantPanel: () => <div>Assistant panel</div>,
}));

function project(overrides: Partial<ProjectRecord> = {}): ProjectRecord {
  return {
    id: "proj_1",
    name: "Hecate",
    roots: [],
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function workItem(overrides: Partial<ProjectWorkItemRecord> = {}): ProjectWorkItemRecord {
  return {
    id: "work_1",
    project_id: "proj_1",
    title: "Extract workspace",
    brief: "Move shell and tabs.",
    status: "ready",
    priority: "normal",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
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
    status: "queued",
    execution_ref: {
      kind: "task_run",
      status: "queued",
    },
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function assistant() {
  return {
    apply: vi.fn(),
    applyResult: null,
    bootstrap: vi.fn(),
    bootstrapPending: false,
    context: null,
    contextError: "",
    contextStatus: "idle",
    dismiss: vi.fn(),
    error: "",
    inspectContext: vi.fn(),
    proposal: null,
    propose: vi.fn(),
    status: "idle",
  } as unknown as ProjectWorkspaceViewProps["assistant"];
}

function renderWorkspace(overrides: Partial<ProjectWorkspaceViewProps> = {}) {
  const handlers = {
    onActivityBucketChange: vi.fn(),
    onAddAssignment: vi.fn(),
    onAddEvidenceLink: vi.fn(),
    onAddHandoff: vi.fn(),
    onAddHandoffFromAssignment: vi.fn(),
    onAddHandoffFromReviewArtifact: vi.fn(),
    onAddReviewArtifactFromAssignment: vi.fn(),
    onAddReviewHandoffFromAssignment: vi.fn(),
    onCreateAssignmentFromReviewArtifact: vi.fn(),
    onCreateAssignmentFromHandoff: vi.fn(),
    onCreateWork: vi.fn(),
    onCloseWorkItem: vi.fn(),
    onDeleteAssignment: vi.fn(),
    onDeleteHandoff: vi.fn(),
    onDeleteMemory: vi.fn(),
    onDeleteWorkItem: vi.fn(),
    onDiscoverContextSources: vi.fn(),
    onDiscoverProjectSkills: vi.fn(),
    onEditAssignment: vi.fn(),
    onEditHandoff: vi.fn(),
    onEditMemory: vi.fn(),
    onEditWorkItem: vi.fn(),
    onManageProfiles: vi.fn(),
    onManageRoles: vi.fn(),
    onNewMemory: vi.fn(),
    onOpenChat: vi.fn(),
    onOpenConnections: vi.fn(),
    onOpenSettings: vi.fn(),
    onOpenTask: vi.fn(),
    onPromoteCandidate: vi.fn(),
    onRefreshMemory: vi.fn(),
    onRefreshProjectSkills: vi.fn(),
    onRefreshWorkItem: vi.fn(),
    onRejectCandidate: vi.fn(),
    onSelectWorkItem: vi.fn(),
    onSetHandoffStatus: vi.fn(),
    onStartAssignment: vi.fn(),
    onStartHandoff: vi.fn(),
    onUpdateProjectSkill: vi.fn(),
    onWorkspaceTabChange: vi.fn(),
  };
  const props: ProjectWorkspaceViewProps = {
    activity: null,
    activityBucket: "all",
    activityByAssignmentID: new Map(),
    artifacts: [],
    artifactActionID: "",
    assignmentErrors: {},
    assignments: [],
    assistant: assistant(),
    detailError: "",
    detailLoadState: "idle",
    discoveringContext: false,
    discoveringSkills: false,
    handoffActionID: "",
    handoffError: "",
    handoffs: [],
    hasWorkItemDetail: false,
    memoryCandidates: [],
    memoryEntries: [],
    memoryError: "",
    memoryLoadState: "idle",
    project: project(),
    projectEmptyDetail: "Choose a project.",
    projectEmptyTitle: "No project selected",
    projectNeedsOnboarding: false,
    projectSkills: [],
    rejectingCandidateID: "",
    roleByID: new Map(),
    roles: [],
    selectedWorkItem: null,
    selectedWorkItemID: "",
    closingWorkItemID: "",
    skillsError: "",
    skillsLoadState: "idle",
    startingAssignmentID: "",
    updatingSkillID: "",
    workError: "",
    workItemSummaries: {},
    workItems: [],
    workLoadState: "idle",
    workspaceTab: "work",
    ...handlers,
    ...overrides,
  };

  render(<ProjectWorkspaceView {...props} />);
  return { handlers, props };
}

describe("ProjectWorkspaceView", () => {
  it("renders onboarding and delegates setup actions", async () => {
    const assistantState = assistant();
    const { handlers } = renderWorkspace({
      assistant: assistantState,
      project: project({ name: "Console" }),
      projectNeedsOnboarding: true,
    });

    expect(screen.getByText("Set up Console")).toBeTruthy();
    expect(screen.getByText("Workspace root")).toBeTruthy();
    expect(screen.getByText("Missing")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Bootstrap project" }));
    await userEvent.click(screen.getByRole("button", { name: "Create work" }));
    await userEvent.click(screen.getByRole("button", { name: "Project settings" }));

    expect(assistantState.bootstrap).toHaveBeenCalledTimes(1);
    expect(handlers.onCreateWork).toHaveBeenCalledTimes(1);
    expect(handlers.onOpenSettings).toHaveBeenCalledTimes(1);
  });

  it("renders workspace tabs and delegates tab changes", async () => {
    const item = workItem();
    const { handlers } = renderWorkspace({
      workItems: [item],
      workItemSummaries: { [item.id]: summarizeAssignments([assignment()]) },
    });

    expect(screen.getByText("Assistant panel")).toBeTruthy();
    expect(screen.getByRole("tab", { name: /Work Coordination/ })).toBeTruthy();

    await userEvent.click(screen.getByRole("tab", { name: /Timeline \/ Decision Log/ }));
    await userEvent.click(screen.getByRole("tab", { name: /Memory \/ Context/ }));
    await userEvent.click(screen.getByRole("tab", { name: /Skills/ }));

    expect(handlers.onWorkspaceTabChange).toHaveBeenNthCalledWith(1, "timeline");
    expect(handlers.onWorkspaceTabChange).toHaveBeenNthCalledWith(2, "memory");
    expect(handlers.onWorkspaceTabChange).toHaveBeenNthCalledWith(3, "skills");
  });

  it("renders project empty state when nothing is selected", () => {
    renderWorkspace({
      project: null,
      projectEmptyTitle: "No project selected",
      projectEmptyDetail: "Choose a project from the list.",
    });

    expect(screen.getByText("No project selected")).toBeTruthy();
    expect(screen.getByText("Choose a project from the list.")).toBeTruthy();
  });

  it("summarizes assignment statuses for work item rows", () => {
    expect(
      summarizeAssignments([
        assignment({ status: "queued", execution_ref: { kind: "task_run", status: "queued" } }),
        assignment({
          id: "assign_2",
          status: "completed",
          execution_ref: { kind: "task_run", status: "completed" },
        }),
        assignment({
          id: "assign_3",
          status: "failed",
          execution_ref: { kind: "task_run", status: "failed" },
        }),
      ]),
    ).toEqual({
      assignmentCount: 3,
      activeCount: 1,
      failedCount: 1,
      completedCount: 1,
    });
  });
});

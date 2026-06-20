import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectAssignmentRecord,
  ProjectMemoryCandidateRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
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

function role(overrides: Partial<ProjectWorkRoleRecord> = {}): ProjectWorkRoleRecord {
  return {
    id: "developer",
    project_id: "proj_1",
    name: "Developer",
    built_in: false,
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function activity(overrides: Partial<ProjectActivityData> = {}): ProjectActivityData {
  const item = workItem({ id: "work_blocked", title: "Fix blocked launch" });
  const blocked = activityItem({
    assignment: assignment({
      id: "assign_blocked",
      work_item_id: item.id,
      status: "queued",
      execution_ref: { kind: "none" },
    }),
    blocking_signal: "not_started",
    id: "assign_blocked",
    status: "queued",
    status_summary: "not started",
    updated_at: "2026-06-13T00:00:00Z",
    work_item: {
      id: item.id,
      title: item.title,
      status: item.status,
      priority: item.priority,
    },
  });
  return {
    project_id: "proj_1",
    summary: {
      work_item_count: 1,
      assignment_count: 1,
      active_count: 0,
      blocked_count: 1,
      completed_count: 0,
      recent_count: 1,
    },
    buckets: {
      active: [],
      blocked: [blocked],
      completed: [],
      recent: [blocked],
    },
    recent: [blocked],
    ...overrides,
  };
}

function activityItem(
  overrides: Partial<ProjectActivityItemRecord> = {},
): ProjectActivityItemRecord {
  const assign = assignment({
    id: "assign_blocked",
    work_item_id: "work_blocked",
    status: "queued",
    execution_ref: { kind: "none" },
  });
  return {
    id: assign.id,
    project_id: "proj_1",
    work_item: {
      id: "work_blocked",
      title: "Fix blocked launch",
      status: "ready",
      priority: "normal",
    },
    assignment: assign,
    role: role(),
    status: "queued",
    blocking_signal: "not_started",
    status_summary: "not started",
    artifact_summary: { count: 0 },
    updated_at: "2026-06-13T00:00:00Z",
    ...overrides,
  };
}

function memoryCandidate(
  overrides: Partial<ProjectMemoryCandidateRecord> = {},
): ProjectMemoryCandidateRecord {
  return {
    id: "memcand_1",
    project_id: "proj_1",
    title: "Project lesson",
    body: "Remember the launch flow.",
    status: "pending",
    suggested_kind: "note",
    suggested_trust_label: "operator_review",
    suggested_source_kind: "dogfood",
    created_at: "2026-06-13T00:00:00Z",
    updated_at: "2026-06-13T00:00:00Z",
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
    onDraftDefaultAssignment: vi.fn(),
    onPreparedAssignmentPreflightOpened: vi.fn(),
    onCreateAssignmentFromReviewArtifact: vi.fn(),
    onCreateAssignmentFromHandoff: vi.fn(),
    onCreateWork: vi.fn(),
    onCloseWorkItem: vi.fn(),
    onDeleteAssignment: vi.fn(),
    onDeleteHandoff: vi.fn(),
    onDeleteMemory: vi.fn(),
    onDeleteSource: vi.fn(),
    onDeleteWorkItem: vi.fn(),
    onDiscoverContextSources: vi.fn(),
    onDiscoverProjectSkills: vi.fn(),
    onEditAssignment: vi.fn(),
    onEditHandoff: vi.fn(),
    onEditMemory: vi.fn(),
    onEditSource: vi.fn(),
    onEditWorkItem: vi.fn(),
    onManageProfiles: vi.fn(),
    onManageRoles: vi.fn(),
    onNewMemory: vi.fn(),
    onNewSource: vi.fn(),
    onOpenChat: vi.fn(),
    onOpenConnections: vi.fn(),
    onOpenSettings: vi.fn(),
    onOperationAction: vi.fn(),
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
    draftingDefaultAssignment: false,
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
    operationsBrief: null,
    operationsBriefError: "",
    operationsBriefLoadState: "idle",
    projectSkills: [],
    preparingAssignmentID: "",
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

  const result = render(<ProjectWorkspaceView {...props} />);
  return { handlers, props, ...result };
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
    expect(screen.getByText("Workspace source")).toBeTruthy();
    expect(screen.getByText("Optional; attach files when this project needs them.")).toBeTruthy();
    expect(screen.getByText("optional")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Add purpose" }));
    await userEvent.click(screen.getByRole("button", { name: "Set defaults" }));
    expect(screen.queryByRole("button", { name: "Review setup" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Set up" })).toBeNull();
    const firstWorkCheck = screen.getByRole("group", { name: "First work item" });
    await userEvent.click(within(firstWorkCheck).getByRole("button", { name: "Create work" }));
    await userEvent.click(screen.getByRole("button", { name: "Set up project" }));
    await userEvent.click(screen.getByRole("button", { name: "Project settings" }));

    expect(assistantState.bootstrap).toHaveBeenCalledTimes(1);
    expect(handlers.onCreateWork).toHaveBeenCalledTimes(1);
    expect(handlers.onOpenSettings).toHaveBeenCalledTimes(3);
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

  it("renders project operations brief items", async () => {
    const operationItem = {
      id: "start_queued_assignment:proj_1:asgn_1",
      kind: "start_queued_assignment",
      priority: "high",
      title: "Review queued assignment: Build cockpit UI",
      detail: "Open launch preflight before starting this assignment.",
      action_label: "Review start",
      status: "not_started",
      target: {
        surface: "work",
        project_id: "proj_1",
        work_item_id: "work_1",
        assignment_id: "asgn_1",
        activity_bucket: "blocked",
      },
      action: {
        type: "open_assignment_preflight",
        project_id: "proj_1",
        work_item_id: "work_1",
        assignment_id: "asgn_1",
        activity_bucket: "blocked",
      },
      updated_at: "2026-06-13T00:00:00Z",
    } as const;
    const { handlers } = renderWorkspace({
      operationsBrief: {
        project_id: "proj_1",
        generated_at: "2026-06-13T00:00:00Z",
        summary: {
          item_count: 1,
          high_count: 1,
          medium_count: 0,
          low_count: 0,
          pending_memory_candidate_count: 0,
          pending_handoff_count: 0,
        },
        items: [operationItem],
      },
    });

    const operations = screen.getByRole("region", { name: "Project operations" });
    expect(within(operations).getByText("Review queued assignment: Build cockpit UI")).toBeTruthy();
    await userEvent.click(within(operations).getByRole("button", { name: /Review start/ }));
    expect(handlers.onOperationAction).toHaveBeenCalledWith(operationItem);
  });

  it("renders a resume summary and delegates resume actions", async () => {
    const item = workItem({ id: "work_blocked", title: "Fix blocked launch" });
    const { handlers } = renderWorkspace({
      activity: activity(),
      memoryCandidates: [memoryCandidate()],
      workItems: [item],
    });

    const resume = screen.getByRole("region", { name: "Project resume" });
    expect(within(resume).getByText("1 assignment needs attention")).toBeTruthy();
    expect(within(resume).getByText("Queued assignment is ready to start.")).toBeTruthy();

    await userEvent.click(within(resume).getByRole("button", { name: /Blocked/ }));
    await userEvent.click(within(resume).getByRole("button", { name: /Recent/ }));
    await userEvent.click(within(resume).getByRole("button", { name: /Memory/ }));
    await userEvent.click(within(resume).getByRole("button", { name: "Continue here" }));

    expect(handlers.onActivityBucketChange).toHaveBeenNthCalledWith(1, "blocked");
    expect(handlers.onActivityBucketChange).toHaveBeenNthCalledWith(2, "recent");
    expect(handlers.onWorkspaceTabChange).toHaveBeenCalledWith("memory");
    expect(handlers.onSelectWorkItem).toHaveBeenCalledWith("work_blocked");
  });

  it("prioritizes active, memory, latest work, and empty resume states", async () => {
    const activeItem = activityItem({
      assignment: assignment({
        id: "assign_active",
        work_item_id: "work_active",
        status: "running",
      }),
      blocking_signal: "running",
      id: "assign_active",
      status: "running",
      work_item: {
        id: "work_active",
        title: "Continue execution",
        status: "ready",
        priority: "normal",
      },
    });
    const candidate = memoryCandidate();
    const latest = workItem({
      id: "work_latest",
      title: "Polish project onboarding",
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-14T00:00:00Z",
    });

    const { handlers, props, rerender } = renderWorkspace({
      activity: activity({
        summary: {
          work_item_count: 1,
          assignment_count: 1,
          active_count: 1,
          blocked_count: 0,
          completed_count: 0,
          recent_count: 1,
        },
        buckets: {
          active: [activeItem],
          blocked: [],
          completed: [],
          recent: [activeItem],
        },
        recent: [activeItem],
      }),
    });

    let resume = screen.getByRole("region", { name: "Project resume" });
    expect(within(resume).getByText("1 assignment in progress")).toBeTruthy();
    expect(
      within(resume).getByText("An assignment is in progress; inspect or continue it."),
    ).toBeTruthy();

    await userEvent.click(within(resume).getByRole("button", { name: "Continue here" }));
    expect(handlers.onSelectWorkItem).toHaveBeenCalledWith("work_active");

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        activity={null}
        memoryCandidates={[candidate]}
        workItems={[]}
      />,
    );
    resume = screen.getByRole("region", { name: "Project resume" });
    expect(within(resume).getByText("1 memory candidate to review")).toBeTruthy();

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        activity={null}
        memoryCandidates={[]}
        workItems={[latest]}
      />,
    );
    resume = screen.getByRole("region", { name: "Project resume" });
    expect(within(resume).getByText("Resume Polish project onboarding")).toBeTruthy();
    await userEvent.click(within(resume).getByRole("button", { name: "Continue here" }));
    expect(handlers.onSelectWorkItem).toHaveBeenCalledWith("work_latest");

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        activity={null}
        memoryCandidates={[]}
        workItems={[]}
      />,
    );
    resume = screen.getByRole("region", { name: "Project resume" });
    expect(within(resume).getByText("No project work in motion")).toBeTruthy();
    expect(
      within(resume).getByText("Create a work item when there is something to coordinate."),
    ).toBeTruthy();
  });

  it("does not derive local next actions when operations brief has no items", () => {
    const latest = workItem({
      id: "work_latest",
      title: "Polish project onboarding",
      created_at: "2026-06-12T00:00:00Z",
      updated_at: "2026-06-14T00:00:00Z",
    });
    renderWorkspace({
      activity: activity(),
      memoryCandidates: [memoryCandidate()],
      operationsBrief: {
        project_id: "proj_1",
        generated_at: "2026-06-13T00:00:00Z",
        summary: {
          item_count: 0,
          high_count: 0,
          medium_count: 0,
          low_count: 0,
          pending_memory_candidate_count: 0,
          pending_handoff_count: 0,
        },
        items: [],
      },
      workItems: [latest],
    });

    expect(screen.queryByRole("region", { name: "Project next action" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Project operations" })).toBeNull();
    expect(screen.getByRole("region", { name: "Project resume" })).toBeTruthy();
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

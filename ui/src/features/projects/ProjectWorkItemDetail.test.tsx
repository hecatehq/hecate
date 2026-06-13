import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ContextPacketRecord } from "../../types/context";
import type {
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectHandoffRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { ProjectWorkItemDetail, type ProjectWorkItemDetailProps } from "./ProjectWorkItemDetail";
import { getProjectAssignmentPreflight } from "../../lib/api";

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    getProjectAssignmentContext: vi.fn(),
    getProjectAssignmentPreflight: vi.fn(),
  };
});

const getProjectAssignmentPreflightMock = vi.mocked(getProjectAssignmentPreflight);

function project(overrides: Partial<ProjectRecord> = {}): ProjectRecord {
  return {
    id: "proj_1",
    name: "Hecate",
    roots: [
      {
        id: "root_main",
        path: "/workspace/hecate",
        kind: "workspace",
        active: true,
        created_at: "2026-06-12T00:00:00Z",
        updated_at: "2026-06-12T00:00:00Z",
      },
    ],
    default_provider: "openai",
    default_model: "gpt-5",
    default_agent_profile: "default",
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
    description: "Build product changes.",
    instructions: "Keep changes tested.",
    default_driver_kind: "hecate_task",
    default_provider: "anthropic",
    default_model: "claude-opus-4-5",
    default_agent_profile: "default",
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
    title: "Decompose project UI",
    brief: "Move work item detail into a focused component.",
    status: "ready",
    priority: "high",
    owner_role_id: "developer",
    root_id: "root_main",
    reviewer_role_ids: ["architect"],
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
    root_id: "root_main",
    driver_kind: "hecate_task",
    status: "queued",
    execution_ref: {
      kind: "task_run",
      task_id: "task_123",
      run_id: "run_123",
      status: "queued",
    },
    execution: {
      task_id: "task_123",
      run_id: "run_123",
      status: "queued",
      provider: "openai",
      model: "gpt-5",
      step_count: 2,
      artifact_count: 1,
    },
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-13T09:00:00Z",
    ...overrides,
  };
}

function handoff(overrides: Partial<ProjectHandoffRecord> = {}): ProjectHandoffRecord {
  return {
    id: "handoff_1",
    project_id: "proj_1",
    work_item_id: "work_1",
    source_assignment_id: "assign_source_1234567890",
    source_run_id: "run_source_1234567890",
    target_role_id: "developer",
    title: "Follow-up review",
    summary: "Check the extracted component.",
    recommended_next_action: "Start the follow-up assignment.",
    context_refs: ["ctx_1234567890"],
    status: "pending",
    provenance_kind: "operator",
    trust_label: "operator_memory",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-13T09:00:00Z",
    status_changed_at: "2026-06-13T09:00:00Z",
    ...overrides,
  };
}

function artifact(
  overrides: Partial<ProjectCollaborationArtifactRecord> = {},
): ProjectCollaborationArtifactRecord {
  return {
    id: "art_review",
    project_id: "proj_1",
    work_item_id: "work_1",
    assignment_id: "assign_1",
    kind: "review",
    title: "Architect review",
    body: "Verdict: Changes requested",
    author_role_id: "architect",
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
    ...overrides,
  };
}

function preflightPacket(): ContextPacketRecord {
  return {
    id: "ctx_preflight",
    items: [
      {
        kind: "launch_readiness",
        title: "Ready",
        trust_level: "runtime",
        origin: "hecate",
        included: true,
        metadata: { ready: "true" },
      },
    ],
  };
}

function renderDetail(overrides: Partial<ProjectWorkItemDetailProps> = {}) {
  const record = project();
  const developer = role();
  const roleByID = new Map([[developer.id, developer]]);
  const assign = assignment();
  const handlers = {
    onAddAssignment: vi.fn(),
    onAddHandoff: vi.fn(),
    onAddHandoffFromAssignment: vi.fn(),
    onAddHandoffFromReviewArtifact: vi.fn(),
    onAddReviewArtifactFromAssignment: vi.fn(),
    onAddReviewHandoffFromAssignment: vi.fn(),
    onCreateAssignmentFromReviewArtifact: vi.fn(),
    onCreateAssignmentFromHandoff: vi.fn(),
    onDeleteAssignment: vi.fn(),
    onDeleteHandoff: vi.fn(),
    onDeleteWorkItem: vi.fn(),
    onCloseWorkItem: vi.fn(),
    onEditAssignment: vi.fn(),
    onEditHandoff: vi.fn(),
    onEditWorkItem: vi.fn(),
    onManageProfiles: vi.fn(),
    onManageRoles: vi.fn(),
    onOpenChat: vi.fn(),
    onOpenConnections: vi.fn(),
    onOpenSettings: vi.fn(),
    onOpenTask: vi.fn(),
    onRefresh: vi.fn(),
    onStartAssignment: vi.fn(),
    onStartHandoff: vi.fn(),
    onSetHandoffStatus: vi.fn(),
  };
  const props: ProjectWorkItemDetailProps = {
    activityByAssignmentID: new Map(),
    assignments: [assign],
    artifacts: [],
    artifactActionID: "",
    handoffActionID: "",
    handoffError: "",
    handoffs: [],
    assignmentErrors: {},
    detailError: "",
    loading: false,
    project: record,
    roleByID,
    closingWorkItemID: "",
    startingAssignmentID: "",
    workItem: workItem(),
    ...handlers,
    ...overrides,
  };

  render(<ProjectWorkItemDetail {...props} />);
  return { props, handlers, assignment: assign, project: record, role: developer };
}

describe("ProjectWorkItemDetail", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getProjectAssignmentPreflightMock.mockResolvedValue({
      object: "context_packet",
      data: preflightPacket(),
    });
  });

  it("renders assignment runtime links and delegates row actions", async () => {
    const { handlers, assignment: assign } = renderDetail();

    expect(screen.getByRole("article", { name: "Decompose project UI work item" })).toBeTruthy();
    expect(screen.getByText("Developer")).toBeTruthy();
    expect(screen.getByText("2 steps")).toBeTruthy();
    expect(screen.getByText("1 artifacts")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Open task" }));
    await userEvent.click(screen.getByRole("button", { name: "Open chat" }));
    await userEvent.click(
      screen.getByRole("button", { name: "Create handoff from assignment assign_1" }),
    );

    expect(handlers.onOpenTask).toHaveBeenCalledWith("task_123", "run_123");
    expect(handlers.onOpenChat).toHaveBeenCalledWith(
      expect.objectContaining({
        projectID: "proj_1",
        provider: "openai",
        model: "gpt-5",
        title: "Decompose project UI - Developer",
      }),
    );
    expect(handlers.onOpenChat.mock.calls[0][0].draft).toContain("Assignment:");
    expect(handlers.onAddHandoffFromAssignment).toHaveBeenCalledWith(assign, undefined);
  });

  it("delegates reviewer handoff requests to the configured reviewer role", async () => {
    const developer = role();
    const reviewer = role({ id: "architect", name: "Architect reviewer" });
    const assign = assignment();
    const item = workItem({ reviewer_role_ids: ["architect"] });
    const { handlers } = renderDetail({
      assignments: [assign],
      roleByID: new Map([
        [developer.id, developer],
        [reviewer.id, reviewer],
      ]),
      workItem: item,
    });

    await userEvent.click(
      screen.getByRole("button", { name: "Request review for assignment assign_1" }),
    );

    expect(handlers.onAddReviewHandoffFromAssignment).toHaveBeenCalledWith(
      assign,
      reviewer,
      undefined,
    );
  });

  it("only exposes review recording for assignments owned by reviewer roles", async () => {
    const developer = role();
    const reviewer = role({ id: "architect", name: "Architect reviewer" });
    const developerAssignment = assignment({ id: "assign_dev", role_id: "developer" });
    const reviewerAssignment = assignment({ id: "assign_review", role_id: "architect" });
    const { handlers } = renderDetail({
      assignments: [developerAssignment, reviewerAssignment],
      roleByID: new Map([
        [developer.id, developer],
        [reviewer.id, reviewer],
      ]),
      workItem: workItem({ reviewer_role_ids: ["architect"] }),
    });

    expect(
      screen.queryByRole("button", { name: "Record review for assignment assign_dev" }),
    ).toBeNull();
    await userEvent.click(
      screen.getByRole("button", { name: "Record review for assignment assign_review" }),
    );

    expect(handlers.onAddReviewArtifactFromAssignment).toHaveBeenCalledWith(reviewerAssignment);
  });

  it("guides setup when a work item has no reviewer roles", async () => {
    const item = workItem({ reviewer_role_ids: [] });
    const { handlers } = renderDetail({
      workItem: item,
    });

    expect(screen.getByText("No reviewer roles configured")).toBeTruthy();
    expect(screen.getByText(/Add at least one reviewer role/)).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Edit reviewers" }));
    await userEvent.click(screen.getByRole("button", { name: "Manage roles" }));

    expect(handlers.onEditWorkItem).toHaveBeenCalledWith(item);
    expect(handlers.onManageRoles).toHaveBeenCalledTimes(1);
  });

  it("guides setup when configured reviewer roles are missing", () => {
    renderDetail({
      workItem: workItem({ reviewer_role_ids: ["missing_reviewer"] }),
    });

    expect(screen.getByText("Reviewer role reference missing")).toBeTruthy();
    expect(screen.getByText(/missing_reviewer/)).toBeTruthy();
  });

  it("shows closeout blockers while assignments are active", () => {
    renderDetail({
      assignments: [assignment({ status: "running", execution_ref: { kind: "none" } })],
    });

    expect(screen.getByText("Closeout is blocked")).toBeTruthy();
    expect(screen.getByText("1 assignment is still active")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Mark done" })).toBeDisabled();
  });

  it("delegates mark-done when closeout is ready", async () => {
    const item = workItem();
    const { handlers } = renderDetail({
      assignments: [assignment({ status: "completed", execution_ref: { kind: "none" } })],
      workItem: item,
    });

    expect(screen.getByText("Ready to mark done")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Mark done" }));

    expect(handlers.onCloseWorkItem).toHaveBeenCalledWith(item);
  });

  it("shows already-done closeout state without a mark-done action", () => {
    renderDetail({
      assignments: [assignment({ status: "failed", execution_ref: { kind: "none" } })],
      workItem: workItem({ status: "done" }),
    });

    expect(screen.getByText("Work item is done")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Mark done" })).toBeNull();
  });

  it("loads launch preflight before starting an assignment", async () => {
    const { handlers } = renderDetail();

    await userEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(getProjectAssignmentPreflightMock).toHaveBeenCalledWith("proj_1", "work_1", "assign_1");

    await userEvent.click(await screen.findByRole("button", { name: "Start assignment" }));

    expect(handlers.onStartAssignment).toHaveBeenCalledWith(
      expect.objectContaining({ id: "assign_1" }),
    );
  });

  it("renders handoff actions and delegates status changes", async () => {
    const pendingHandoff = handoff();
    const { handlers } = renderDetail({
      assignments: [],
      handoffs: [pendingHandoff],
    });

    expect(screen.getByText("Follow-up review")).toBeTruthy();
    expect(screen.getByLabelText("Source evidence")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Accept" }));
    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    await userEvent.click(screen.getByRole("button", { name: "Supersede" }));
    await userEvent.click(screen.getByRole("button", { name: "Create follow-up assignment" }));

    expect(handlers.onSetHandoffStatus).toHaveBeenNthCalledWith(1, pendingHandoff, "accepted");
    expect(handlers.onSetHandoffStatus).toHaveBeenNthCalledWith(2, pendingHandoff, "dismissed");
    expect(handlers.onSetHandoffStatus).toHaveBeenNthCalledWith(3, pendingHandoff, "superseded");
    expect(handlers.onCreateAssignmentFromHandoff).toHaveBeenCalledWith(pendingHandoff);
  });

  it("delegates direct follow-up assignment creation from review artifacts", async () => {
    const reviewArtifact = artifact();
    const { handlers } = renderDetail({
      assignments: [],
      artifacts: [reviewArtifact],
    });

    await userEvent.click(
      screen.getByRole("button", {
        name: "Create follow-up assignment from review artifact art_review",
      }),
    );

    expect(handlers.onCreateAssignmentFromReviewArtifact).toHaveBeenCalledWith(reviewArtifact);
  });

  it("renders structured review artifact outcome badges", () => {
    renderDetail({
      assignments: [],
      artifacts: [
        artifact({
          review_verdict: "changes_requested",
          review_risk: "medium",
          review_follow_up_required: true,
        }),
      ],
    });

    expect(screen.getByText("Changes requested")).toBeTruthy();
    expect(screen.getByText("risk Medium")).toBeTruthy();
    expect(screen.getByText("follow-up required")).toBeTruthy();
  });

  it("disables review artifact follow-up actions while an assignment shortcut is pending", () => {
    renderDetail({
      assignments: [],
      artifacts: [artifact()],
      artifactActionID: "art_review",
    });

    expect(
      screen.getByRole("button", { name: "Create follow-up from review artifact art_review" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", {
        name: "Create follow-up assignment from review artifact art_review",
      }),
    ).toBeDisabled();
  });
});

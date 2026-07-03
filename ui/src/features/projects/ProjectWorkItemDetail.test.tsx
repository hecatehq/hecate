import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ContextPacketRecord } from "../../types/context";
import type {
  ProjectAssignmentLaunchReadinessRecord,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectHandoffRecord,
  ProjectRecord,
  ProjectWorkItemReadinessRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { ProjectWorkItemDetail, type ProjectWorkItemDetailProps } from "./ProjectWorkItemDetail";
import { getProjectAssignmentLaunchReadiness, getProjectAssignmentPreflight } from "../../lib/api";

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    getProjectAssignmentContext: vi.fn(),
    getProjectAssignmentLaunchReadiness: vi.fn(),
    getProjectAssignmentPreflight: vi.fn(),
  };
});

const getProjectAssignmentLaunchReadinessMock = vi.mocked(getProjectAssignmentLaunchReadiness);
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

function closeoutReadiness(
  overrides: Partial<ProjectWorkItemReadinessRecord> = {},
): ProjectWorkItemReadinessRecord {
  return {
    project_id: "proj_1",
    work_item_id: "work_1",
    ready: true,
    status: "ready",
    title: "Ready to mark done",
    detail:
      "Assignments, evidence, handoffs, and review follow-up are clear. The operator can mark this work item done.",
    blockers: [],
    warnings: [],
    assignment_count: 1,
    completed_assignments: 1,
    review_follow_up_count: 0,
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
        kind: "launch_preflight",
        title: "Launch preflight",
        trust_level: "runtime",
        origin: "project_assignment.preflight",
        included: false,
        body: "Preview only: no task, run, chat session, memory entry, artifact, or assignment update has been created.",
      },
    ],
  };
}

function launchReadiness(
  overrides: Partial<ProjectAssignmentLaunchReadinessRecord> = {},
): ProjectAssignmentLaunchReadinessRecord {
  return {
    project_id: "proj_1",
    work_item_id: "work_1",
    assignment_id: "assign_1",
    generated_at: "2026-06-20T12:00:00Z",
    ready: true,
    status: "ready",
    title: "Ready to start assignment",
    detail: "Launch checks are clear.",
    blockers: [],
    warnings: [],
    driver_kind: "hecate_task",
    workspace: "/workspace/hecate",
    root_id: "root_main",
    provider: "openai",
    model: "gpt-5",
    execution_profile: "implementation",
    profile_posture: {
      id: "implementation",
      name: "Implementation",
      source: "role_default",
      tools_enabled: true,
      writes_allowed: true,
      network_allowed: false,
      approval_policy: "require",
      project_memory_policy: "include",
      context_source_policy: "include_enabled",
    },
    model_readiness: {
      ready: true,
      status: "ok",
      provider: "openai",
      model: "gpt-5",
    },
    ...overrides,
  };
}

function renderDetail(overrides: Partial<ProjectWorkItemDetailProps> = {}) {
  const record = project();
  const developer = role();
  const roleByID = new Map([[developer.id, developer]]);
  const assign = assignment();
  const handlers = {
    onAddAssignment: vi.fn(),
    onAddEvidenceLink: vi.fn(),
    onAddHandoff: vi.fn(),
    onAddHandoffFromAssignment: vi.fn(),
    onAddHandoffFromReviewArtifact: vi.fn(),
    onAddReviewArtifactFromAssignment: vi.fn(),
    onAddReviewHandoffFromAssignment: vi.fn(),
    onDraftDefaultAssignment: vi.fn(),
    onCreateAssignmentFromReviewArtifact: vi.fn(),
    onCreateAssignmentFromHandoff: vi.fn(),
    onPreparedAssignmentPreflightOpened: vi.fn(),
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
    draftingDefaultAssignment: false,
    preparingAssignmentID: "",
    loading: false,
    project: record,
    roleByID,
    closingWorkItemID: "",
    closeoutReadiness: closeoutReadiness(),
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
    getProjectAssignmentLaunchReadinessMock.mockResolvedValue({
      object: "project_assignment_launch_readiness",
      data: launchReadiness(),
    });
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

  it("guides pristine work items through assignment proposal drafting", async () => {
    const architect = role({ id: "architect", name: "Architect" });
    const item = workItem({ owner_role_id: "architect", reviewer_role_ids: [] });
    const { handlers } = renderDetail({
      assignments: [],
      artifacts: [],
      handoffs: [],
      roleByID: new Map([[architect.id, architect]]),
      workItem: item,
    });

    expect(screen.getByRole("region", { name: "Start work" })).toBeTruthy();
    expect(screen.getByText("Let Hecate prepare the first step")).toBeTruthy();
    expect(screen.queryByText("No reviewer roles configured")).toBeNull();
    expect(screen.queryByRole("button", { name: "Mark done" })).toBeNull();
    expect(screen.queryByText("No assignments recorded yet.")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Prepare next step" }));
    await userEvent.click(screen.getByText("Add manually"));
    const manualActions = screen.getByRole("group", { name: "Manual work item actions" });
    await userEvent.click(within(manualActions).getByRole("button", { name: "Assignment" }));
    await userEvent.click(within(manualActions).getByRole("button", { name: "Evidence" }));
    await userEvent.click(within(manualActions).getByRole("button", { name: "Handoff" }));

    expect(handlers.onDraftDefaultAssignment).toHaveBeenCalledWith(item);
    expect(handlers.onAddAssignment).toHaveBeenCalledTimes(1);
    expect(handlers.onAddEvidenceLink).toHaveBeenCalledTimes(1);
    expect(handlers.onAddHandoff).toHaveBeenCalledTimes(1);
  });

  it("routes pristine work items without roles to role setup", async () => {
    const { handlers } = renderDetail({
      assignments: [],
      artifacts: [],
      handoffs: [],
      roleByID: new Map(),
      workItem: workItem({ owner_role_id: "", reviewer_role_ids: [] }),
    });

    expect(screen.getByText("Add a role before assigning work")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Manage roles" }));

    expect(screen.queryByRole("button", { name: /Queue/ })).toBeNull();
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
      closeoutReadiness: closeoutReadiness({
        ready: false,
        status: "blocked",
        title: "Closeout is blocked",
        detail:
          "Resolve the listed assignment, evidence, handoff, or review follow-up items before marking this work done.",
        blockers: ["1 assignment is still active"],
        completed_assignments: 0,
      }),
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

  it("groups manual record creation for active work items", async () => {
    const { handlers } = renderDetail({
      assignments: [assignment({ status: "completed", execution_ref: { kind: "none" } })],
    });

    const addActions = screen.getByRole("region", { name: "Add to work item" });
    expect(within(addActions).getByText("Add")).toBeTruthy();
    expect(within(addActions).getByText(/Add an assignment, source evidence/)).toBeTruthy();

    await userEvent.click(within(addActions).getByRole("button", { name: "Add assignment" }));
    await userEvent.click(within(addActions).getByRole("button", { name: "Add evidence" }));
    await userEvent.click(within(addActions).getByRole("button", { name: "Add handoff" }));

    expect(handlers.onAddAssignment).toHaveBeenCalledTimes(1);
    expect(handlers.onAddEvidenceLink).toHaveBeenCalledTimes(1);
    expect(handlers.onAddHandoff).toHaveBeenCalledTimes(1);
  });

  it("shows already-done closeout state without a mark-done action", () => {
    renderDetail({
      assignments: [assignment({ status: "failed", execution_ref: { kind: "none" } })],
      closeoutReadiness: closeoutReadiness({
        ready: false,
        status: "done",
        title: "Work item is done",
        detail: "This work item has already been marked done by the operator.",
        blockers: [],
        completed_assignments: 0,
      }),
      workItem: workItem({ status: "done" }),
    });

    expect(screen.getByText("Work item is done")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Mark done" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Add to work item" })).toBeNull();
  });

  it("loads launch preflight before starting an assignment", async () => {
    const { handlers } = renderDetail();

    await userEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(getProjectAssignmentLaunchReadinessMock).toHaveBeenCalledWith(
      "proj_1",
      "work_1",
      "assign_1",
    );
    expect(getProjectAssignmentPreflightMock).toHaveBeenCalledWith("proj_1", "work_1", "assign_1");

    const preflight = await screen.findByRole("dialog", {
      name: "Assignment assign_1 launch preflight",
    });
    const posture = within(preflight).getByRole("region", { name: "Resolved launch posture" });
    expect(within(posture).getByText("Launch posture")).toBeTruthy();
    expect(within(posture).getByText("Hecate task")).toBeTruthy();
    expect(within(posture).getByText("/workspace/hecate")).toBeTruthy();
    expect(within(posture).getByText("root_main")).toBeTruthy();
    expect(within(posture).getByText("openai / gpt-5")).toBeTruthy();
    expect(within(posture).getByText("implementation")).toBeTruthy();

    await userEvent.click(within(preflight).getByRole("button", { name: "Start assignment" }));

    expect(handlers.onStartAssignment).toHaveBeenCalledWith(
      expect.objectContaining({ id: "assign_1" }),
    );
  });

  it("shows server-backed launch readiness inline before opening preflight", async () => {
    renderDetail();

    const readiness = screen.getByRole("region", { name: "Assignment launch readiness" });
    expect(within(readiness).getByText("not checked")).toBeTruthy();

    await userEvent.click(within(readiness).getByRole("button", { name: "Check readiness" }));

    expect(getProjectAssignmentLaunchReadinessMock).toHaveBeenCalledWith(
      "proj_1",
      "work_1",
      "assign_1",
    );
    expect(await within(readiness).findByText("ready")).toBeTruthy();
    expect(within(readiness).getByText("Hecate task")).toBeTruthy();
    expect(within(readiness).getByText("/workspace/hecate")).toBeTruthy();
    expect(within(readiness).getByText("openai / gpt-5")).toBeTruthy();
    expect(within(readiness).getByText("implementation")).toBeTruthy();
    expect(within(readiness).getByText("tools on · writes on · network off")).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("marks missing launch presets in the posture preview", async () => {
    getProjectAssignmentLaunchReadinessMock.mockResolvedValueOnce({
      object: "project_assignment_launch_readiness",
      data: launchReadiness({
        execution_profile: "missing_profile",
        profile_posture: {
          id: "missing_profile",
          name: "missing_profile",
          source: "project_default",
          missing: true,
          tools_enabled: false,
          writes_allowed: false,
          network_allowed: false,
        },
        warnings: [
          'Referenced agent preset "missing_profile" was not found; using stored preset id as execution_profile hint.',
        ],
      }),
    });
    renderDetail();

    const readiness = screen.getByRole("region", { name: "Assignment launch readiness" });
    await userEvent.click(within(readiness).getByRole("button", { name: "Check readiness" }));

    expect(await within(readiness).findByText("missing_profile (preset missing)")).toBeTruthy();
    expect(within(readiness).getByText("tools off · writes off · network off")).toBeTruthy();
  });

  it("shows External Agent launch posture before preparing chat", async () => {
    getProjectAssignmentLaunchReadinessMock.mockResolvedValueOnce({
      object: "project_assignment_launch_readiness",
      data: launchReadiness({
        driver_kind: "external_agent",
        provider: "",
        model: "",
        external_agent: "Codex",
        external_agent_id: "codex",
        session_title: "Implementation follow-up",
        warnings: [
          "Project skill Review (review) declares network enabled, but resolved preset implementation has network disabled.",
        ],
      }),
    });
    renderDetail({
      assignments: [assignment({ driver_kind: "external_agent", execution_ref: { kind: "none" } })],
    });

    await userEvent.click(screen.getByRole("button", { name: "Prepare chat" }));
    const preflight = await screen.findByRole("dialog", {
      name: "Assignment assign_1 launch preflight",
    });
    const posture = within(preflight).getByRole("region", { name: "Resolved launch posture" });

    expect(within(posture).getAllByText("External Agent").length).toBeGreaterThan(0);
    expect(within(posture).getByText("Codex (codex)")).toBeTruthy();
    expect(within(posture).getByText("Implementation follow-up")).toBeTruthy();
    expect(within(posture).getByText("tools on · writes on · network off")).toBeTruthy();
    expect(
      within(preflight).getByRole("status", { name: "Launch readiness warnings" }),
    ).toBeTruthy();
    expect(within(preflight).getByText(/Project skill Review/)).toBeTruthy();
    expect(within(posture).queryByText("openai / gpt-5")).toBeNull();
  });

  it("opens launch preflight when a prepared assignment becomes visible", async () => {
    const preparedAssignment = assignment({ id: "assign_prepared" });
    const { handlers } = renderDetail({
      assignments: [preparedAssignment],
      preparingAssignmentID: "assign_prepared",
    });

    expect(
      await screen.findByRole("dialog", {
        name: "Assignment assign_prepared launch preflight",
      }),
    ).toBeTruthy();
    expect(getProjectAssignmentPreflightMock).toHaveBeenCalledWith(
      "proj_1",
      "work_1",
      "assign_prepared",
    );
    expect(getProjectAssignmentLaunchReadinessMock).toHaveBeenCalledWith(
      "proj_1",
      "work_1",
      "assign_prepared",
    );
    expect(handlers.onPreparedAssignmentPreflightOpened).toHaveBeenCalledWith("assign_prepared");
    expect(handlers.onStartAssignment).not.toHaveBeenCalled();
  });

  it("blocks assignment launch confirmation from typed readiness", async () => {
    getProjectAssignmentLaunchReadinessMock.mockResolvedValueOnce({
      object: "project_assignment_launch_readiness",
      data: launchReadiness({
        ready: false,
        status: "blocked",
        title: "Launch is blocked",
        detail: "Resolve launch blockers before starting this assignment.",
        blockers: ['No routable provider reports model "dogfood-model".'],
        model: "dogfood-model",
        model_readiness: {
          ready: false,
          status: "blocked",
          provider: "auto",
          model: "dogfood-model",
          reason: "model_not_discovered",
          message: 'No routable provider reports model "dogfood-model".',
          operator_action: "Pick one of the discovered models.",
        },
      }),
    });
    const { handlers } = renderDetail();

    await userEvent.click(screen.getByRole("button", { name: "Start" }));
    const preflight = await screen.findByRole("dialog", {
      name: "Assignment assign_1 launch preflight",
    });

    expect(within(preflight).getByText("Provider/model not ready")).toBeTruthy();
    expect(within(preflight).getByRole("region", { name: "Resolved launch posture" })).toBeTruthy();
    expect(within(preflight).getByRole("status").textContent).toContain(
      'No routable provider reports model "dogfood-model"',
    );
    expect(within(preflight).getByRole("button", { name: "Start assignment" })).toBeDisabled();
    expect(handlers.onStartAssignment).not.toHaveBeenCalled();
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

  it("delegates follow-up assignment drafting from review artifacts", async () => {
    const reviewArtifact = artifact();
    const { handlers } = renderDetail({
      assignments: [],
      artifacts: [reviewArtifact],
    });

    await userEvent.click(
      screen.getByRole("button", {
        name: "Draft follow-up assignment from review artifact art_review",
      }),
    );

    expect(handlers.onCreateAssignmentFromReviewArtifact).toHaveBeenCalledWith(reviewArtifact.id);
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
    expect(screen.getAllByText("follow-up required")).toHaveLength(1);
  });

  it("surfaces server readiness review follow-up as a closeout notice", async () => {
    const reviewArtifact = artifact({
      review_verdict: "blocked",
      review_follow_up_required: true,
    });
    const { handlers } = renderDetail({
      artifacts: [reviewArtifact],
      closeoutReadiness: closeoutReadiness({
        ready: false,
        status: "blocked",
        title: "Closeout is blocked",
        detail: "Resolve the listed review follow-up items before marking this work done.",
        blockers: ['Review follow-up "Architect review" is not triaged'],
        review_follow_up_count: 1,
        review_follow_up_artifact_ids: [reviewArtifact.id],
        review_follow_ups: [
          {
            artifact_id: reviewArtifact.id,
            title: "Architect review",
            status: "needs_path",
            blocker: 'Review follow-up "Architect review" is not triaged',
            review_verdict: "blocked",
          },
        ],
      }),
    });

    const notice = screen.getByRole("region", { name: "Review follow-up required" });
    expect(within(notice).getByText("Architect review")).toBeTruthy();
    expect(within(notice).getByText("follow-up required")).toBeTruthy();

    await userEvent.click(within(notice).getByRole("button", { name: "Draft follow-up" }));

    expect(handlers.onCreateAssignmentFromReviewArtifact).toHaveBeenCalledWith(reviewArtifact.id);
  });

  it("does not derive review follow-up notice from client artifact fields", () => {
    renderDetail({
      artifacts: [
        artifact({
          id: "art_review",
          review_verdict: "blocked",
          review_follow_up_required: true,
        }),
      ],
      closeoutReadiness: closeoutReadiness({
        review_follow_up_count: 0,
        review_follow_up_artifact_ids: [],
      }),
    });

    expect(screen.queryByRole("region", { name: "Review follow-up required" })).toBeNull();
  });

  it("renders evidence link metadata and delegates evidence creation", async () => {
    const { handlers } = renderDetail({
      assignments: [],
      artifacts: [
        artifact({
          id: "art_evidence",
          kind: "evidence_link",
          title: "Source document",
          body: "Research source for this work.",
          evidence_source_kind: "source_document",
          evidence_url: "https://example.invalid/source",
          evidence_external_id: "DOC-42",
          evidence_provider: "docs",
          evidence_trust_label: "operator_provided",
        }),
      ],
    });

    expect(screen.getByText("source_document")).toBeTruthy();
    expect(screen.getByText("operator_provided")).toBeTruthy();
    expect(screen.getByRole("link", { name: "https://example.invalid/source" })).toBeTruthy();
    expect(screen.getByText("provider docs · external DOC-42")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Add evidence" }));

    expect(handlers.onAddEvidenceLink).toHaveBeenCalledTimes(1);
  });

  it("renders unsafe evidence locators as text instead of links", () => {
    renderDetail({
      assignments: [],
      artifacts: [
        artifact({
          id: "art_unsafe_evidence",
          kind: "evidence_link",
          title: "Operator locator",
          body: "Operator-provided locator.",
          evidence_source_kind: "source_document",
          evidence_url: "javascript:alert(1)",
        }),
      ],
    });

    expect(screen.getByText("javascript:alert(1)")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "javascript:alert(1)" })).toBeNull();
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
        name: "Draft follow-up assignment from review artifact art_review",
      }),
    ).toBeDisabled();
  });
});

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ContextPacketRecord } from "../../types/context";
import type {
  ProjectAssignmentLaunchReadinessRecord,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectHandoffRecord,
  ProjectOperationsBriefItem,
  ProjectRecord,
  ProjectWorkItemReadinessRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import {
  ProjectWorkItemDetail,
  projectOperationRequestsFocusTarget,
  type ProjectWorkItemDetailProps,
} from "./ProjectWorkItemDetail";
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
        title: "Launch details",
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
      browser_evidence_status: "enabled",
      browser_allowed: true,
      browser_allowed_origins: ["https://qa.example.test"],
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
    onAddResponsibility: vi.fn(),
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
    onSetAssignmentStatus: vi.fn(),
    onEditAssignment: vi.fn(),
    onEditHandoff: vi.fn(),
    onEditWorkItem: vi.fn(),
    onManagePresets: vi.fn(),
    onManageRoles: vi.fn(),
    onOpenChat: vi.fn(),
    onOpenConnections: vi.fn(),
    onOpenSettings: vi.fn(),
    onOpenTask: vi.fn(),
    onOpenWorkItem: vi.fn(),
    onRefresh: vi.fn(),
    onStartAssignment: vi.fn(),
    onSetHandoffStatus: vi.fn(),
  };
  const props: ProjectWorkItemDetailProps = {
    activityByAssignmentID: new Map(),
    assistantProposalOpen: false,
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
    startingAssignmentIDs: new Set<string>(),
    workItem: workItem(),
    ...handlers,
    ...overrides,
  };

  const view = render(<ProjectWorkItemDetail {...props} />);
  return {
    ...view,
    props,
    handlers,
    assignment: assign,
    project: record,
    role: developer,
  };
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

  it("keeps a detail-load failure visible without exposing stale work controls", async () => {
    const { handlers } = renderDetail({
      detailError: "Failed to load selected work item detail.",
      loading: false,
      workItem: null,
    });

    const unavailable = screen.getByRole("region", {
      name: "Work item unavailable",
    });
    expect(within(unavailable).getByText("Work item unavailable")).toBeTruthy();
    expect(
      within(unavailable).getByText("Refresh project work to try loading this item again."),
    ).toBeTruthy();
    expect(within(unavailable).getByRole("alert")).toHaveTextContent(
      "Failed to load selected work item detail.",
    );
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();

    await userEvent.click(within(unavailable).getByRole("button", { name: "Retry" }));
    expect(handlers.onRefresh).toHaveBeenCalledTimes(1);
  });

  it("announces a pending detail load", () => {
    renderDetail({ detailError: "", loading: true, workItem: null });

    const status = screen.getByRole("status");
    expect(status).toHaveAttribute("aria-live", "polite");
    expect(status).toHaveAttribute("aria-atomic", "true");
    expect(status).not.toHaveAttribute("aria-busy");
    expect(within(status).getByText("Loading detail…")).toBeTruthy();
    expect(
      within(status).getByText("Loading assignments and collaboration artifacts."),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  });

  it("describes pending closeout checks in operator language", () => {
    renderDetail({ closeoutReadiness: null, loading: true });

    expect(
      screen.getAllByText("Checking assignments, reviews, evidence, and handoffs."),
    ).not.toHaveLength(0);
    expect(screen.queryByText(/operations contract/i)).toBeNull();
  });

  it("treats null detail lists as empty", () => {
    renderDetail({
      assignments: null as unknown as ProjectAssignmentRecord[],
      artifacts: null as unknown as ProjectCollaborationArtifactRecord[],
      handoffs: null as unknown as ProjectHandoffRecord[],
    });

    expect(screen.getByRole("article", { name: "Decompose project UI work item" })).toBeTruthy();
    expect(screen.getByText("Choose who does this work")).toBeTruthy();
    expect(screen.queryByText("No assignments recorded yet.")).toBeNull();
  });

  it("keeps every concurrently starting assignment disabled", () => {
    const first = assignment();
    const second = assignment({
      id: "assign_2",
      execution: undefined,
      execution_ref: undefined,
    });
    renderDetail({
      assignments: [first, second],
      startingAssignmentIDs: new Set([first.id, second.id]),
    });

    const startingButtons = screen.getAllByRole("button", { name: /Starting/ });
    expect(startingButtons).toHaveLength(2);
    for (const button of startingButtons) expect(button).toBeDisabled();
  });

  it("routes cross-work handoff targets without duplicating or launching the assignment", async () => {
    const targetAssignmentID = "assign_target";
    const { handlers } = renderDetail({
      assignments: [],
      handoffs: [
        handoff({
          target_assignment_id: targetAssignmentID,
          target_work_item_id: "work_target",
        }),
      ],
    });

    expect(screen.queryByRole("button", { name: "Accept and create follow-up" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Start from handoff" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Open target work" }));

    expect(handlers.onOpenWorkItem).toHaveBeenCalledWith("work_target");
  });

  it("keeps handoff decisions independent from the linked assignment launch", () => {
    const target = assignment();
    renderDetail({
      assignments: [target],
      handoffs: [handoff({ target_assignment_id: target.id })],
      startingAssignmentIDs: new Set([target.id]),
    });

    const handoffActions = screen.getByRole("group", {
      name: "Follow-up review handoff",
    });
    for (const name of ["Edit", "Delete", "Accept", "Dismiss", "Supersede"]) {
      expect(within(handoffActions).getByRole("button", { name })).toBeEnabled();
    }
    expect(
      within(handoffActions).getByRole("button", {
        name: "Open linked assignment",
      }),
    ).toBeEnabled();
    expect(screen.getByRole("button", { name: /Starting/ })).toBeDisabled();
  });

  it("does not offer follow-up assignments for closed handoffs", () => {
    const dismissedTarget = assignment({
      id: "assign_dismissed",
      driver_kind: "manual",
    });
    const supersededTarget = assignment({ id: "assign_superseded" });
    renderDetail({
      assignments: [dismissedTarget, supersededTarget],
      handoffs: [
        handoff({
          id: "handoff_dismissed",
          title: "Dismissed follow-up",
          status: "dismissed",
          target_assignment_id: dismissedTarget.id,
        }),
        handoff({
          id: "handoff_superseded",
          title: "Superseded follow-up",
          status: "superseded",
          target_assignment_id: supersededTarget.id,
        }),
      ],
    });

    expect(screen.queryByRole("button", { name: "Create follow-up assignment" })).toBeNull();
    const dismissed = screen.getByRole("group", {
      name: "Dismissed follow-up handoff",
    });
    const superseded = screen.getByRole("group", {
      name: "Superseded follow-up handoff",
    });
    expect(within(dismissed).queryByRole("button", { name: "Start work" })).toBeNull();
    expect(within(superseded).queryByRole("button", { name: "Start from handoff" })).toBeNull();
  });

  it("treats null closeout lists as empty", () => {
    renderDetail({
      closeoutReadiness: {
        ...closeoutReadiness({
          ready: false,
          status: "blocked",
          title: "Closeout is blocked",
          blockers: ["1 assignment is still active"],
        }),
        review_follow_ups: null as unknown as ProjectWorkItemReadinessRecord["review_follow_ups"],
        warnings: null as unknown as string[],
      },
    });

    expect(screen.getByRole("region", { name: "Work closeout" })).toBeTruthy();
    expect(screen.getByText("1 assignment is still active")).toBeTruthy();
  });

  it("keeps blocked closeout after the assignment execution story", () => {
    renderDetail({
      closeoutReadiness: closeoutReadiness({
        ready: false,
        status: "blocked",
        title: "Closeout is blocked",
        blockers: ["1 assignment is still active"],
        completed_assignments: 0,
      }),
    });

    const executionStory = screen.getByRole("article", {
      name: "Developer assignment execution assign_1",
    });
    const closeout = screen.getByRole("region", { name: "Work closeout" });
    expect(executionStory.compareDocumentPosition(closeout)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });

  it("renders assignment runtime links and delegates row actions", async () => {
    const { handlers, assignment: assign } = renderDetail();

    expect(screen.getByRole("article", { name: "Decompose project UI work item" })).toBeTruthy();
    expect(screen.getByText("Developer")).toBeTruthy();
    await userEvent.click(screen.getByText("Execution details"));
    expect(screen.getByText("2 steps")).toBeTruthy();
    expect(screen.getByText("1 artifact")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Open task" }));
    await userEvent.click(screen.getByRole("button", { name: "Start related chat" }));
    await userEvent.click(
      screen.getByRole("button", {
        name: "Create handoff from assignment assign_1",
      }),
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

  it("reopens a prepared External Agent chat with reload-safe launch context", async () => {
    const externalAssignment = assignment({
      driver_kind: "external_agent",
      status: "running",
      started_at: "2026-06-13T09:00:00Z",
      execution_ref: {
        kind: "chat_session",
        chat_session_id: "chat_external",
        context_snapshot_id: "ctx_external",
        status: "running",
      },
      execution: undefined,
    });
    const { handlers } = renderDetail({ assignments: [externalAssignment] });

    await userEvent.click(screen.getByRole("button", { name: "Continue in chat" }));

    expect(handlers.onOpenChat).toHaveBeenCalledWith(
      expect.objectContaining({
        projectID: "proj_1",
        chatSessionID: "chat_external",
      }),
    );
    expect(handlers.onOpenChat.mock.calls[0]?.[0].draft).toContain("Launch context");
    expect(handlers.onOpenChat.mock.calls[0]?.[0].draft).toContain("Assignment:");
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
      screen.getByRole("button", {
        name: "Request review for assignment assign_1",
      }),
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
    const developerAssignment = assignment({
      id: "assign_dev",
      role_id: "developer",
    });
    const reviewerAssignment = assignment({
      id: "assign_review",
      role_id: "architect",
    });
    const { handlers } = renderDetail({
      assignments: [developerAssignment, reviewerAssignment],
      roleByID: new Map([
        [developer.id, developer],
        [reviewer.id, reviewer],
      ]),
      workItem: workItem({ reviewer_role_ids: ["architect"] }),
    });

    expect(
      screen.queryByRole("button", {
        name: "Record review for assignment assign_dev",
      }),
    ).toBeNull();
    await userEvent.click(
      screen.getByRole("button", {
        name: "Record review for assignment assign_review",
      }),
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

  it("guides pristine work items into an explicit assignment", async () => {
    const architect = role({ id: "architect", name: "Architect" });
    const item = workItem({
      owner_role_id: "architect",
      reviewer_role_ids: [],
    });
    const { handlers } = renderDetail({
      assignments: [],
      artifacts: [],
      handoffs: [],
      roleByID: new Map([[architect.id, architect]]),
      workItem: item,
    });

    expect(screen.getByRole("region", { name: "Start work" })).toBeTruthy();
    expect(screen.getByText("Choose who does this work")).toBeTruthy();
    expect(screen.queryByText("No reviewer roles configured")).toBeNull();
    expect(screen.queryByRole("button", { name: "Mark done" })).toBeNull();
    expect(screen.queryByText("No assignments recorded yet.")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Assign work" }));
    const moreOptions = screen.getByText("More options");
    expect(moreOptions).toHaveStyle({
      color: "var(--t2)",
      fontSize: "11px",
      minHeight: "28px",
      padding: "6px 2px",
    });
    await userEvent.click(moreOptions);
    const moreActions = screen.getByRole("group", {
      name: "More work item actions",
    });
    await userEvent.click(
      within(moreActions).getByRole("button", { name: "Draft with Project Assistant" }),
    );
    await userEvent.click(within(moreActions).getByRole("button", { name: "Record evidence" }));
    await userEvent.click(within(moreActions).getByRole("button", { name: "Create handoff" }));

    expect(handlers.onAddAssignment).toHaveBeenCalledTimes(1);
    expect(handlers.onDraftDefaultAssignment).toHaveBeenCalledWith(item);
    expect(handlers.onAddEvidenceLink).toHaveBeenCalledTimes(1);
    expect(handlers.onAddHandoff).toHaveBeenCalledTimes(1);
  });

  it("adds a responsibility before assigning roleless pristine work", async () => {
    const { handlers } = renderDetail({
      assignments: [],
      artifacts: [],
      handoffs: [],
      roleByID: new Map(),
      workItem: workItem({ owner_role_id: "", reviewer_role_ids: [] }),
    });

    expect(screen.getByText("Add a responsibility")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Add responsibility" }));
    await userEvent.click(screen.getByText("More options"));
    const moreActions = screen.getByRole("group", { name: "More work item actions" });
    expect(within(moreActions).queryByRole("button", { name: "Assign work" })).toBeNull();
    expect(
      within(moreActions).queryByRole("button", { name: "Draft with Project Assistant" }),
    ).toBeNull();
    await userEvent.click(within(moreActions).getByRole("button", { name: "Record evidence" }));
    await userEvent.click(within(moreActions).getByRole("button", { name: "Create handoff" }));

    expect(screen.queryByRole("button", { name: /Queue/ })).toBeNull();
    expect(handlers.onAddResponsibility).toHaveBeenCalledTimes(1);
    expect(handlers.onManageRoles).not.toHaveBeenCalled();
    expect(handlers.onAddAssignment).not.toHaveBeenCalled();
    expect(handlers.onAddEvidenceLink).toHaveBeenCalledTimes(1);
    expect(handlers.onAddHandoff).toHaveBeenCalledTimes(1);
  });

  it("keeps the pristine start action secondary while a proposal awaits approval", () => {
    const architect = role({ id: "architect", name: "Architect" });
    renderDetail({
      assistantProposalOpen: true,
      assignments: [],
      artifacts: [],
      handoffs: [],
      roleByID: new Map([[architect.id, architect]]),
      workItem: workItem({
        owner_role_id: architect.id,
        reviewer_role_ids: [],
      }),
    });

    expect(screen.getByRole("button", { name: "Assign work" })).toHaveClass("btn-ghost");
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
    expect(screen.queryByRole("button", { name: "Mark done" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Review closeout" })).toBeNull();
  });

  it.each(["done", "cancelled"] as const)(
    "keeps persisted %s work read-only when readiness is stale",
    (status) => {
      const reviewArtifact = artifact({
        review_follow_up_required: true,
        review_verdict: "changes_requested",
      });
      renderDetail({
        assignments: [
          assignment({
            status: "completed",
            execution_ref: { kind: "none", status: "completed" },
          }),
        ],
        artifacts: [reviewArtifact],
        handoffs: [handoff()],
        closeoutReadiness: closeoutReadiness({
          ready: false,
          status: "blocked",
          title: "Closeout is blocked",
          blockers: ["Completion evidence is missing"],
          missing_evidence_assignment_ids: ["assign_1"],
          open_handoff_ids: ["handoff_1"],
          review_follow_up_count: 1,
          review_follow_up_artifact_ids: [reviewArtifact.id],
          review_follow_ups: [
            {
              artifact_id: reviewArtifact.id,
              title: reviewArtifact.title ?? "Review follow-up",
              status: "needs_path",
            },
          ],
        }),
        workItem: workItem({ status }),
      });

      expect(screen.queryByRole("button", { name: "Add evidence" })).toBeNull();
      expect(screen.queryByRole("button", { name: "Draft follow-up" })).toBeNull();
      expect(screen.queryByRole("button", { name: "Accept" })).toBeNull();
      expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
    },
  );

  it("focuses exact structured work targets after detail records load", () => {
    const targetAssignment = assignment({ id: "assignment_target" });
    const targetArtifact = artifact({ id: "artifact_target" });
    const targetHandoff = handoff({
      id: "handoff_target",
      title: "Target handoff",
    });
    const onFocusTargetHandled = vi.fn();
    const view = renderDetail({
      assignments: [assignment({ id: "assignment_decoy" }), targetAssignment],
      artifacts: [artifact({ id: "artifact_decoy" }), targetArtifact],
      handoffs: [handoff({ id: "handoff_decoy" }), targetHandoff],
      focusTarget: { artifactID: targetArtifact.id, workItemID: "work_1" },
      onFocusTargetHandled,
    });

    expect(document.activeElement).toHaveAttribute("id", "project-work-artifact-artifact_target");
    expect(onFocusTargetHandled).toHaveBeenCalledTimes(1);

    view.rerender(
      <ProjectWorkItemDetail
        {...view.props}
        focusTarget={{ handoffID: targetHandoff.id, workItemID: "work_1" }}
      />,
    );
    expect(document.activeElement).toHaveAttribute("id", "project-work-handoff-handoff_target");

    view.rerender(
      <ProjectWorkItemDetail
        {...view.props}
        focusTarget={{
          assignmentID: targetAssignment.id,
          workItemID: "work_1",
        }}
      />,
    );
    expect(document.activeElement).toHaveAttribute(
      "id",
      "project-work-assignment-assignment_target",
    );

    view.rerender(
      <ProjectWorkItemDetail
        {...view.props}
        focusTarget={{ operationKind: "close_work_item", workItemID: "work_1" }}
      />,
    );
    expect(document.activeElement).toHaveAttribute("id", "project-work-closeout");
  });

  it("announces a stale exact target and focuses the selected work item without using a decoy", async () => {
    const onFocusTargetHandled = vi.fn();
    const { handlers } = renderDetail({
      assignments: [assignment({ id: "assignment_decoy" })],
      focusTarget: {
        artifactID: "artifact_removed",
        assignmentID: "assignment_decoy",
        workItemID: "work_1",
      },
      onFocusTargetHandled,
    });

    expect(document.activeElement).toHaveAttribute("id", "project-work-item-work_1");
    expect(
      screen.getByText(
        "The requested record is no longer available. Showing the selected work item instead.",
      ),
    ).toHaveTextContent(
      "The requested record is no longer available. Showing the selected work item instead.",
    );
    expect(document.activeElement).not.toHaveAttribute(
      "id",
      "project-work-assignment-assignment_decoy",
    );
    expect(onFocusTargetHandled).toHaveBeenCalledTimes(1);
    await userEvent.click(screen.getByRole("button", { name: "Refresh work" }));
    expect(handlers.onRefresh).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(
        screen.queryByText(
          "The requested record is no longer available. Showing the selected work item instead.",
        ),
      ).toBeNull(),
    );
    expect(document.activeElement).toHaveAttribute("id", "project-work-item-work_1");
  });

  it("keeps stale-target recovery in control until authoritative refresh succeeds", async () => {
    let resolveRefresh: (result: boolean) => void = () => {};
    const onRefresh = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          resolveRefresh = resolve;
        }),
    );
    renderDetail({
      assignments: [assignment({ id: "assignment_decoy" })],
      focusTarget: {
        assignmentID: "assignment_removed",
        workItemID: "work_1",
      },
      onRefresh,
    });

    fireEvent.click(screen.getByRole("button", { name: "Refresh work" }));
    expect(screen.getByRole("button", { name: "Refreshing work…" })).toBeDisabled();
    expect(screen.getByText(/The requested record is no longer available/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Record evidence" })).toBeNull();

    await act(async () => {
      resolveRefresh(false);
    });

    await waitFor(() => expect(screen.getByRole("button", { name: "Refresh work" })).toBeEnabled());
    expect(screen.getByText(/The requested record is no longer available/)).toBeTruthy();
  });

  it("stays fail-closed when refreshed operations still target a missing record", async () => {
    const removedAssignmentID = "assignment_removed";
    renderDetail({
      assignments: [assignment({ id: "assignment_decoy" })],
      focusTarget: { assignmentID: removedAssignmentID, workItemID: "work_1" },
      onRefresh: vi.fn().mockResolvedValue(true),
      operation: {
        id: `record_completion_evidence:proj_1:${removedAssignmentID}`,
        kind: "record_completion_evidence",
        priority: "high",
        title: "Record missing evidence",
        detail: "The assignment should exist before evidence is recorded.",
        action_label: "Open work",
        target: {
          surface: "work",
          project_id: "proj_1",
          work_item_id: "work_1",
          assignment_id: removedAssignmentID,
        },
        action: {
          type: "open_work_item",
          project_id: "proj_1",
          work_item_id: "work_1",
          assignment_id: removedAssignmentID,
        },
      },
    });

    await userEvent.click(screen.getByRole("button", { name: "Refresh work" }));

    await waitFor(() => expect(screen.getByRole("button", { name: "Refresh work" })).toBeEnabled());
    expect(screen.getByText(/The requested record is no longer available/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Record evidence" })).toBeNull();
  });

  it("uses the header refresh and clears a stale target when the server operation changes", async () => {
    let resolveRefresh: (result: boolean) => void = () => {};
    const onRefresh = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          resolveRefresh = resolve;
        }),
    );
    const staleCloseoutOperation: ProjectOperationsBriefItem = {
      id: "close_work_item:proj_1:work_1",
      kind: "close_work_item",
      priority: "low",
      title: "Close work",
      detail: "Review closeout.",
      action_label: "Open closeout",
      target: { surface: "work", project_id: "proj_1", work_item_id: "work_1" },
      action: { type: "open_work_item", project_id: "proj_1", work_item_id: "work_1" },
    };
    const staleArtifactOperation: ProjectOperationsBriefItem = {
      id: "review_follow_up:proj_1:artifact_removed",
      kind: "review_follow_up",
      priority: "high",
      title: "Review removed artifact",
      detail: "The artifact no longer exists.",
      action_label: "Open review",
      target: {
        surface: "work",
        project_id: "proj_1",
        work_item_id: "work_1",
        artifact_id: "artifact_removed",
      },
      action: {
        type: "open_work_item",
        project_id: "proj_1",
        work_item_id: "work_1",
        artifact_id: "artifact_removed",
      },
    };
    const nextOperation: ProjectOperationsBriefItem = {
      id: "record_completion_evidence:proj_1:assignment_replacement_missing",
      kind: "record_completion_evidence",
      priority: "high",
      title: "Record current evidence",
      detail: "The next server operation replaced closeout.",
      action_label: "Open work",
      target: {
        surface: "work",
        project_id: "proj_1",
        work_item_id: "work_1",
        assignment_id: "assignment_replacement_missing",
      },
      action: {
        type: "open_work_item",
        project_id: "proj_1",
        work_item_id: "work_1",
        assignment_id: "assignment_replacement_missing",
      },
    };
    const view = renderDetail({
      assignments: [assignment({ id: "assignment_decoy", status: "completed" })],
      closeoutReadiness: closeoutReadiness({
        ready: false,
        status: "blocked",
        title: "Closeout is blocked",
        missing_evidence_assignment_ids: ["assignment_decoy"],
      }),
      focusTarget: { artifactID: "artifact_removed", workItemID: "work_1" },
      onRefresh,
      operation: staleArtifactOperation,
    });

    expect(
      projectOperationRequestsFocusTarget(staleCloseoutOperation, {
        operationKind: "close_work_item",
        workItemID: "work_1",
      }),
    ).toBe(true);
    expect(
      projectOperationRequestsFocusTarget(nextOperation, {
        operationKind: "close_work_item",
        workItemID: "work_1",
      }),
    ).toBe(false);
    expect(screen.getByText(/The requested record is no longer available/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(screen.getByRole("button", { name: "Refreshing…" })).toBeDisabled();
    view.rerender(
      <ProjectWorkItemDetail
        {...view.props}
        focusTarget={null}
        onRefresh={onRefresh}
        operation={nextOperation}
      />,
    );

    await act(async () => resolveRefresh(true));

    await waitFor(() =>
      expect(screen.queryByText(/The requested record is no longer available/)).toBeNull(),
    );
    expect(screen.getByText("Next action unavailable")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Refresh work" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Record evidence" })).toBeNull();
  });

  it("confirms closeout before delegating mark-done", async () => {
    const item = workItem();
    const developer = role();
    const reviewer = role({ id: "architect", name: "Architect reviewer" });
    const { handlers } = renderDetail({
      assignments: [
        assignment({
          status: "completed",
          execution_ref: {
            kind: "task_run",
            task_id: "task_done",
            status: "completed",
          },
        }),
      ],
      roleByID: new Map([
        [developer.id, developer],
        [reviewer.id, reviewer],
      ]),
      workItem: item,
    });

    expect(screen.getByText("Ready to mark done")).toBeTruthy();
    const work = screen.getByRole("article", {
      name: "Decompose project UI work item",
    });
    expect(work.querySelectorAll(".btn-primary")).toHaveLength(0);
    expect(
      screen.getByRole("button", {
        name: "Request review for assignment assign_1",
      }),
    ).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Review closeout" }));
    const dialog = screen.getByRole("dialog", { name: "Review closeout" });
    expect(handlers.onCloseWorkItem).not.toHaveBeenCalled();

    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(handlers.onCloseWorkItem).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "Review closeout" }));
    await userEvent.click(
      within(screen.getByRole("dialog", { name: "Review closeout" })).getByRole("button", {
        name: "Mark work done",
      }),
    );

    expect(handlers.onCloseWorkItem).toHaveBeenCalledWith(item);
  });

  it("locks closeout confirmation while the decision is being recorded", async () => {
    const item = workItem();
    const view = renderDetail({ workItem: item });

    await userEvent.click(screen.getByRole("button", { name: "Review closeout" }));
    view.rerender(<ProjectWorkItemDetail {...view.props} closingWorkItemID={item.id} />);

    const dialog = screen.getByRole("dialog", { name: "Review closeout" });
    expect(within(dialog).getByRole("button", { name: "Marking work done…" })).toBeDisabled();
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(within(dialog).getByRole("button", { name: "Close" })).toBeDisabled();
    await userEvent.keyboard("{Escape}");
    expect(screen.getByRole("dialog", { name: "Review closeout" })).toBeTruthy();
    await userEvent.click(dialog.parentElement as HTMLElement);
    expect(screen.getByRole("dialog", { name: "Review closeout" })).toBeTruthy();
  });

  it("restores focus to the surviving next-action region after closeout succeeds", async () => {
    const item = workItem();
    const view = renderDetail({ workItem: item });

    await userEvent.click(screen.getByRole("button", { name: "Review closeout" }));
    view.rerender(
      <ProjectWorkItemDetail
        {...view.props}
        closeoutReadiness={closeoutReadiness({
          ready: false,
          status: "done",
          title: "Work item is done",
        })}
        workItem={workItem({ status: "done" })}
      />,
    );

    expect(screen.queryByRole("dialog", { name: "Review closeout" })).toBeNull();
    expect(document.activeElement).toHaveAttribute("id", "project-work-follow-through");
    expect(document.activeElement).toHaveTextContent("Work closed");
  });

  it("closes a pending closeout decision when the selected work item changes", async () => {
    const view = renderDetail();

    await userEvent.click(screen.getByRole("button", { name: "Review closeout" }));
    expect(screen.getByRole("dialog", { name: "Review closeout" })).toBeTruthy();

    view.rerender(
      <ProjectWorkItemDetail
        {...view.props}
        closeoutReadiness={closeoutReadiness({ work_item_id: "work_2" })}
        workItem={workItem({ id: "work_2", title: "Verify the next slice" })}
      />,
    );

    expect(screen.queryByRole("dialog", { name: "Review closeout" })).toBeNull();
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
      artifacts: [artifact()],
      handoffs: [handoff()],
      closeoutReadiness: closeoutReadiness({
        ready: false,
        status: "done",
        title: "Work item is done",
        detail: "This work item has already been marked done by the operator.",
        blockers: [],
        completed_assignments: 0,
      }),
      workItem: workItem({ status: "review" }),
    });

    expect(screen.getByText("Work item is done")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Mark done" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Add to work item" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Accept" })).toBeNull();
    expect(screen.queryByRole("button", { name: /Draft follow-up/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Record review/ })).toBeNull();
    expect(screen.getByRole("button", { name: "Refresh" })).toBeEnabled();
    expect(screen.getByText("Architect review")).toBeTruthy();
    expect(screen.getByText("Follow-up review")).toBeTruthy();
  });

  it("loads launch preflight before starting an assignment", async () => {
    const { handlers } = renderDetail();

    await userEvent.click(screen.getByRole("button", { name: "Review & start" }));
    expect(getProjectAssignmentLaunchReadinessMock).toHaveBeenCalledWith(
      "proj_1",
      "work_1",
      "assign_1",
    );
    expect(getProjectAssignmentPreflightMock).toHaveBeenCalledWith("proj_1", "work_1", "assign_1");

    const preflight = await screen.findByRole("dialog", {
      name: "Assignment assign_1 launch details",
    });
    const posture = within(preflight).getByRole("region", {
      name: "Resolved launch posture",
    });
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

    await userEvent.click(screen.getByText("Execution details"));
    const readiness = screen.getByRole("region", {
      name: "Assignment launch readiness",
    });
    expect(within(readiness).getByText("not checked")).toBeTruthy();
    expect(
      within(readiness).getByText(
        "Check the work destination, workspace, preset, and target before reviewing launch details.",
      ),
    ).toBeTruthy();

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
    expect(within(readiness).getByText("Enabled · https://qa.example.test")).toBeTruthy();
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

    await userEvent.click(screen.getByText("Execution details"));
    const readiness = screen.getByRole("region", {
      name: "Assignment launch readiness",
    });
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
        profile_posture: {
          id: "implementation",
          name: "Implementation",
          source: "role_default",
          tools_enabled: true,
          writes_allowed: true,
          network_allowed: false,
          browser_evidence_status: "not_applicable",
          browser_allowed: false,
          browser_allowed_origins: [],
        },
        external_agent: "Codex",
        external_agent_id: "codex",
        session_title: "Implementation follow-up",
        warnings: [
          "Project skill Review (review) declares network enabled, but resolved preset implementation has network disabled.",
        ],
      }),
    });
    renderDetail({
      assignments: [
        assignment({
          driver_kind: "external_agent",
          execution_ref: { kind: "none" },
        }),
      ],
    });

    await userEvent.click(screen.getByRole("button", { name: "Review & prepare chat" }));
    const preflight = await screen.findByRole("dialog", {
      name: "Assignment assign_1 launch details",
    });
    const posture = within(preflight).getByRole("region", {
      name: "Resolved launch posture",
    });

    expect(within(posture).getAllByText("External Agent").length).toBeGreaterThan(0);
    expect(within(posture).getByText("Codex (codex)")).toBeTruthy();
    expect(within(posture).getByText("Implementation follow-up")).toBeTruthy();
    expect(within(posture).getByText("tools on · writes on · network off")).toBeTruthy();
    expect(within(posture).getByText("Not available for External Agent assignments")).toBeTruthy();
    expect(
      within(preflight).getByRole("status", {
        name: "Launch readiness warnings",
      }),
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
        name: "Assignment assign_prepared launch details",
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

    await userEvent.click(screen.getByRole("button", { name: "Review & start" }));
    const preflight = await screen.findByRole("dialog", {
      name: "Assignment assign_1 launch details",
    });

    expect(within(preflight).getByText("Provider/model not ready")).toBeTruthy();
    expect(
      within(preflight).getByRole("region", {
        name: "Resolved launch posture",
      }),
    ).toBeTruthy();
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
    expect(screen.getByText("Source Operator")).toBeTruthy();
    expect(screen.getByText("Operator memory")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Accept" }));
    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    await userEvent.click(screen.getByRole("button", { name: "Supersede" }));
    await userEvent.click(screen.getByRole("button", { name: "Accept and create follow-up" }));

    expect(handlers.onSetHandoffStatus).toHaveBeenNthCalledWith(1, pendingHandoff, "accepted");
    expect(handlers.onSetHandoffStatus).toHaveBeenNthCalledWith(2, pendingHandoff, "dismissed");
    expect(handlers.onSetHandoffStatus).toHaveBeenNthCalledWith(3, pendingHandoff, "superseded");
    expect(handlers.onCreateAssignmentFromHandoff).toHaveBeenCalledWith(pendingHandoff);
  });

  it("announces the active handoff mutation and disables competing decisions", () => {
    const activeHandoff = handoff();
    const waitingHandoff = handoff({ id: "handoff_2", title: "Release handoff" });
    renderDetail({
      assignments: [],
      handoffActionID: activeHandoff.id,
      handoffs: [activeHandoff, waitingHandoff],
    });

    const active = screen.getByRole("group", { name: "Follow-up review handoff" });
    expect(active).toHaveAttribute("aria-busy", "true");
    expect(within(active).queryByRole("status")).toBeNull();
    expect(screen.getByRole("status", { name: "Handoff update status" })).toHaveTextContent(
      "Updating handoff…",
    );
    const waiting = screen.getByRole("group", { name: "Release handoff handoff" });
    expect(waiting).not.toHaveAttribute("aria-busy");
    for (const button of [
      ...within(active).getAllByRole("button"),
      ...within(waiting).getAllByRole("button"),
    ]) {
      expect(button).toBeDisabled();
    }
    expect(screen.getByRole("button", { name: "Add handoff" })).toBeDisabled();
  });

  it("announces a handoff save that has no existing row", () => {
    renderDetail({
      assignments: [],
      handoffActionID: "new",
      handoffs: [handoff()],
    });

    expect(screen.getByRole("status", { name: "Handoff update status" })).toHaveTextContent(
      "Saving handoff…",
    );
    expect(screen.getByRole("group", { name: "Follow-up review handoff" })).not.toHaveAttribute(
      "aria-busy",
    );
    expect(screen.getByRole("button", { name: "Add handoff" })).toBeDisabled();
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

    const notice = screen.getByRole("region", {
      name: "Review follow-up required",
    });
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

    const evidenceArtifact = screen.getByRole("group", {
      name: "Source document Evidence artifact",
    });
    expect(within(evidenceArtifact).getByText("Evidence")).toBeTruthy();
    expect(within(evidenceArtifact).getByText("Document")).toBeTruthy();
    expect(within(evidenceArtifact).getByText("Operator provided")).toBeTruthy();
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
      screen.getByRole("button", {
        name: "Create follow-up from review artifact art_review",
      }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", {
        name: "Draft follow-up assignment from review artifact art_review",
      }),
    ).toBeDisabled();
  });
});

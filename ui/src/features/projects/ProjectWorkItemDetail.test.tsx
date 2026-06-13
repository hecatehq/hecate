import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ContextPacketRecord } from "../../types/context";
import type {
  ProjectAssignmentRecord,
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
    onAddReviewHandoffFromAssignment: vi.fn(),
    onCreateAssignmentFromHandoff: vi.fn(),
    onDeleteAssignment: vi.fn(),
    onDeleteHandoff: vi.fn(),
    onDeleteWorkItem: vi.fn(),
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
    handoffActionID: "",
    handoffError: "",
    handoffs: [],
    assignmentErrors: {},
    detailError: "",
    loading: false,
    project: record,
    roleByID,
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
});

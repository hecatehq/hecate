import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectAssignmentRecord,
  ProjectMemoryCandidateRecord,
  ProjectOperationsBriefItem,
  ProjectRecord,
  ProjectSetupReadiness,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import {
  ProjectWorkspaceView,
  summarizeAssignments,
  type ProjectWorkspaceViewProps,
} from "./ProjectWorkspaceView";

vi.mock("./ProjectAssistantPanel", () => ({
  ProjectAssistantPanel: ({
    primaryEmphasis,
    showHeader = true,
  }: {
    primaryEmphasis: boolean;
    showHeader?: boolean;
  }) => (
    <section
      aria-label="Assistant panel"
      data-header={String(showHeader)}
      data-primary={String(primaryEmphasis)}
    >
      Assistant panel
    </section>
  ),
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
    bootstrapRecoveryAvailable: false,
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

function setupReadiness(overrides: Partial<ProjectSetupReadiness> = {}): ProjectSetupReadiness {
  return {
    project_id: "proj_1",
    generated_at: "2026-06-20T00:00:00Z",
    show_onboarding: true,
    setup_started: false,
    first_work_ready: false,
    summary: {
      work_item_count: 0,
      role_count: 0,
      skill_count: 0,
      enabled_context_source_count: 0,
      saved_memory_count: 0,
      pending_memory_candidate_count: 0,
      has_purpose: false,
      has_active_root: false,
      missing_defaults: true,
    },
    primary_action: {
      type: "create_work_item",
      project_id: "proj_1",
      label: "Create first work",
    },
    checks: [
      {
        id: "purpose",
        label: "Project purpose",
        detail: "Add a short purpose.",
        status: "todo",
        action: {
          type: "open_project_settings",
          project_id: "proj_1",
          label: "Add purpose",
        },
      },
      {
        id: "workspace_source",
        label: "Workspace source",
        detail: "Optional; attach files when this project needs them.",
        status: "optional",
        optional: true,
      },
      {
        id: "launch_defaults",
        label: "Provider and model",
        detail: "Not set",
        status: "todo",
        action: {
          type: "open_project_settings",
          project_id: "proj_1",
          label: "Set defaults",
        },
      },
      {
        id: "sources_memory",
        label: "Sources and memory",
        detail: "Attach a workspace when files matter, or add sources later.",
        status: "todo",
        action: {
          type: "bootstrap_project",
          project_id: "proj_1",
          label: "Set up project",
        },
      },
      {
        id: "roles",
        label: "Roles",
        detail: "Set up project can suggest roles from skills.",
        status: "todo",
        action: {
          type: "bootstrap_project",
          project_id: "proj_1",
          label: "Set up project",
        },
      },
      {
        id: "first_work_item",
        label: "First work item",
        detail: "Create the first reviewable work item.",
        status: "todo",
        action: {
          type: "create_work_item",
          project_id: "proj_1",
          label: "Create work",
        },
      },
    ],
    ...overrides,
  };
}

function rootedSetupReadiness(): ProjectSetupReadiness {
  const readiness = setupReadiness();
  return {
    ...readiness,
    summary: {
      ...readiness.summary,
      has_active_root: true,
    },
    primary_action: {
      type: "bootstrap_project",
      project_id: "proj_1",
      label: "Set up project",
    },
    checks: readiness.checks.map((check) =>
      check.id === "workspace_source"
        ? {
            ...check,
            detail: "/workspace/console",
            optional: false,
            status: "ready",
          }
        : check,
    ),
  };
}

function renderWorkspace(overrides: Partial<ProjectWorkspaceViewProps> = {}) {
  const handlers = {
    onActivityBucketChange: vi.fn(),
    onAddAssignment: vi.fn(),
    onAddResponsibility: vi.fn(),
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
    onSetAssignmentStatus: vi.fn(),
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
    onManagePresets: vi.fn(),
    onManageRoles: vi.fn(),
    onNavigateWorkspaceTab: vi.fn(),
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
    onSetupReadinessAction: vi.fn(),
    onUpdateProjectSkill: vi.fn(),
    onWorkspaceTabChange: vi.fn(),
  };
  const props: ProjectWorkspaceViewProps = {
    activity: null,
    activityBucket: "all",
    activityByAssignmentID: new Map(),
    activityLoadState: "loaded",
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
    projectSetupError: "",
    projectSetupPending: false,
    projectSetupReadiness: null,
    overviewError: "",
    operationsBrief: null,
    operationsBriefError: "",
    operationsBriefLoadState: "idle",
    projectSkills: [],
    preparingAssignmentID: "",
    rejectingCandidateID: "",
    roleByID: new Map(),
    roles: [],
    selectedWorkItem: null,
    selectedWorkItemReadiness: null,
    selectedWorkItemID: "",
    closingWorkItemID: "",
    skillsError: "",
    skillsLoadState: "idle",
    startingAssignmentIDs: new Set<string>(),
    updatingSkillID: "",
    workError: "",
    workItemSummaries: {},
    workItems: [],
    workLoadState: "idle",
    workspaceTab: "overview",
    ...handlers,
    ...overrides,
  };

  const result = render(<ProjectWorkspaceView {...props} />);
  return { handlers, props, ...result };
}

function dispatchModifiedClickWithoutFollowing(
  element: HTMLElement,
  init: MouseEventInit,
): boolean | undefined {
  let componentPrevented: boolean | undefined;
  document.addEventListener(
    "click",
    (event) => {
      componentPrevented = event.defaultPrevented;
      event.preventDefault();
    },
    { once: true },
  );
  fireEvent.click(element, init);
  return componentPrevented;
}

describe("ProjectWorkspaceView", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/");
  });

  it("renders one guided onboarding action with supporting setup details", async () => {
    const { handlers } = renderWorkspace({
      project: project({ name: "Console" }),
      projectNeedsOnboarding: true,
      projectSetupReadiness: rootedSetupReadiness(),
    });

    expect(screen.getByRole("heading", { level: 1, name: "Set up Console" })).toHaveStyle({
      margin: "8px 0px 0px",
    });
    const onboarding = screen.getByRole("region", {
      name: "Project onboarding",
    });
    expect(onboarding).toHaveClass("project-workspace-onboarding");
    expect(onboarding.querySelector(".project-workspace-onboarding-copy")).toBeTruthy();
    expect(within(onboarding).getByRole("button", { name: "Set up project" })).toHaveClass(
      "btn-primary",
    );
    const setupDetails = within(onboarding).getByText("Setup details").closest("details");
    expect(setupDetails).toHaveClass("project-workspace-onboarding-details");
    expect(setupDetails).not.toHaveAttribute("open");
    await userEvent.click(within(onboarding).getByRole("button", { name: "Set up project" }));

    await userEvent.click(within(onboarding).getByText("Setup details"));
    expect(screen.getByText("Workspace source")).toBeTruthy();
    expect(screen.getByText("/workspace/console")).toBeTruthy();
    const createWork = within(onboarding).getByRole("button", {
      name: "Create work: First work item",
    });
    expect(createWork).toHaveClass("btn-ghost");
    await userEvent.click(createWork);
    await userEvent.click(screen.getByRole("button", { name: "Project settings" }));

    expect(handlers.onSetupReadinessAction).toHaveBeenCalledTimes(2);
    expect(handlers.onSetupReadinessAction).toHaveBeenNthCalledWith(1, {
      type: "bootstrap_project",
      project_id: "proj_1",
      label: "Set up project",
    });
    expect(handlers.onSetupReadinessAction).toHaveBeenNthCalledWith(2, {
      type: "create_work_item",
      project_id: "proj_1",
      label: "Create work",
    });
    expect(handlers.onOpenSettings).toHaveBeenCalledTimes(1);
  });

  it("starts a rootless project with the server-backed first-work action", async () => {
    const { handlers } = renderWorkspace({
      project: project({ name: "Research notes" }),
      projectNeedsOnboarding: true,
      projectSetupReadiness: setupReadiness(),
    });

    const onboarding = screen.getByRole("region", { name: "Project onboarding" });
    expect(
      within(onboarding).getByRole("heading", { name: "Create the first work item" }),
    ).toBeTruthy();
    expect(
      within(onboarding).getByText(
        "This project can start without local setup. Add files, sources, roles, and runtime defaults when the work needs them.",
      ),
    ).toBeTruthy();
    const createWork = within(onboarding).getByRole("button", { name: "Create first work" });
    expect(createWork).toHaveClass("btn-primary");

    await userEvent.click(within(onboarding).getByText("Setup details"));
    expect(
      within(onboarding).queryByRole("button", { name: "Create work: First work item" }),
    ).toBeNull();
    await userEvent.click(createWork);

    expect(handlers.onSetupReadinessAction).toHaveBeenCalledOnce();
    expect(handlers.onSetupReadinessAction).toHaveBeenCalledWith({
      type: "create_work_item",
      project_id: "proj_1",
      label: "Create first work",
    });
  });

  it("keeps retry primary for an unknown setup failure and surfaces first work", async () => {
    const { handlers } = renderWorkspace({
      assistant: {
        ...assistant(),
        error: "Project setup could not reach the discovery service.",
      },
      projectNeedsOnboarding: true,
      projectSetupReadiness: rootedSetupReadiness(),
    });

    const onboarding = screen.getByRole("region", { name: "Project onboarding" });
    expect(within(onboarding).getByRole("alert")).toHaveTextContent(
      "Project setup could not reach the discovery service.",
    );
    expect(
      within(onboarding).getByText(
        "Setup did not complete. Retry setup, or start coordinating work without it.",
      ),
    ).toBeTruthy();
    const retrySetup = within(onboarding).getByRole("button", { name: "Retry setup" });
    expect(retrySetup).toHaveClass("btn-primary");
    const createWorkInstead = within(onboarding).getByRole("button", {
      name: "Create first work instead",
    });
    expect(createWorkInstead).toHaveClass("btn-ghost");
    await userEvent.click(within(onboarding).getByText("Setup details"));
    expect(within(onboarding).getAllByRole("button", { name: /create.*work/i })).toHaveLength(1);

    await userEvent.click(retrySetup);
    expect(retrySetup).toHaveFocus();
    await userEvent.click(createWorkInstead);

    expect(handlers.onSetupReadinessAction).toHaveBeenNthCalledWith(1, {
      type: "bootstrap_project",
      project_id: "proj_1",
      label: "Set up project",
    });
    expect(handlers.onSetupReadinessAction).toHaveBeenNthCalledWith(2, {
      type: "create_work_item",
      project_id: "proj_1",
      label: "Create work",
    });
  });

  it("promotes first work only for the typed no-input setup outcome", async () => {
    const { handlers } = renderWorkspace({
      assistant: {
        ...assistant(),
        bootstrapRecoveryAvailable: true,
        error: "No enabled guidance sources or local skill files were found.",
      },
      projectNeedsOnboarding: true,
      projectSetupReadiness: rootedSetupReadiness(),
    });

    const onboarding = screen.getByRole("region", { name: "Project onboarding" });
    expect(
      within(onboarding).getByText(
        "Setup found no guidance or skills to apply. Start coordinating work now, or add setup inputs and retry.",
      ),
    ).toBeTruthy();
    const createWork = within(onboarding).getByRole("button", {
      name: "Create first work instead",
    });
    expect(createWork).toHaveClass("btn-primary");
    const retrySetup = within(onboarding).getByRole("button", { name: "Retry setup" });
    expect(retrySetup).toHaveClass("btn-ghost");

    await userEvent.click(retrySetup);

    expect(createWork).toHaveFocus();
    expect(handlers.onSetupReadinessAction).toHaveBeenCalledWith({
      type: "bootstrap_project",
      project_id: "proj_1",
      label: "Set up project",
    });
  });

  it("uses ready-state overview guidance after setup is applied", () => {
    renderWorkspace({
      projectSetupReadiness: setupReadiness({
        first_work_ready: true,
        setup_started: true,
        show_onboarding: false,
      }),
    });

    expect(
      screen.getByText("Create the first reviewable work item to begin coordinating progress."),
    ).toBeTruthy();
    expect(
      screen.queryByText("Finish setup and create the first reviewable work item."),
    ).toBeNull();
  });

  it("renders workspace tabs and delegates tab changes", async () => {
    const item = workItem();
    const { handlers } = renderWorkspace({
      workItems: [item],
      workItemSummaries: { [item.id]: summarizeAssignments([assignment()]) },
    });

    expect(screen.queryByText("Assistant panel")).toBeNull();
    expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute(
      "href",
      "/projects?project=proj_1",
    );
    expect(screen.getByRole("tab", { name: "Overview" })).toHaveStyle({
      textDecoration: "none",
    });
    expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: /Work/ })).toHaveAttribute(
      "href",
      "/projects?project=proj_1&view=work",
    );
    expect(screen.getByRole("tab", { name: /Timeline/ })).toHaveAttribute(
      "href",
      "/projects?project=proj_1&view=timeline",
    );
    expect(screen.getByRole("heading", { level: 1, name: "Project Overview" })).toBeTruthy();
    expect(screen.getByRole("region", { name: "Project workspace content" })).toBeTruthy();

    await userEvent.click(screen.getByRole("tab", { name: /Work/ }));
    await userEvent.click(screen.getByRole("tab", { name: /Timeline/ }));
    await userEvent.click(screen.getByRole("tab", { name: /Memory/ }));
    await userEvent.click(screen.getByRole("tab", { name: /Skills/ }));

    expect(handlers.onWorkspaceTabChange).toHaveBeenNthCalledWith(1, "work");
    expect(handlers.onWorkspaceTabChange).toHaveBeenNthCalledWith(2, "timeline");
    expect(handlers.onWorkspaceTabChange).toHaveBeenNthCalledWith(3, "memory");
    expect(handlers.onWorkspaceTabChange).toHaveBeenNthCalledWith(4, "skills");
  });

  it("leaves modified project-tab clicks to the native link", () => {
    const { handlers } = renderWorkspace();
    const timelineTab = screen.getByRole("tab", { name: "Timeline" });

    const componentPrevented = dispatchModifiedClickWithoutFollowing(timelineTab, {
      button: 0,
      ctrlKey: true,
    });

    expect(componentPrevented).toBe(false);
    expect(handlers.onWorkspaceTabChange).not.toHaveBeenCalled();
  });

  it("renders work-item destinations as links and intercepts only plain clicks", () => {
    const item = workItem();
    const { handlers } = renderWorkspace({
      selectedWorkItemID: item.id,
      workItems: [item],
      workspaceTab: "work",
    });
    const itemLink = screen.getByRole("link", { name: `Open work item ${item.title}` });

    expect(itemLink).toHaveAttribute("href", `/projects?project=proj_1&view=work&work=${item.id}`);
    expect(itemLink).toHaveAttribute("aria-current", "page");
    expect(fireEvent.click(itemLink, { button: 0 })).toBe(false);
    expect(handlers.onSelectWorkItem).toHaveBeenCalledWith(item.id);

    handlers.onSelectWorkItem.mockClear();
    expect(dispatchModifiedClickWithoutFollowing(itemLink, { button: 0, metaKey: true })).toBe(
      false,
    );
    expect(handlers.onSelectWorkItem).not.toHaveBeenCalled();
  });

  it("keeps the idle project assistant collapsed after selected work", () => {
    const item = workItem({ owner_role_id: "developer", reviewer_role_ids: [] });
    renderWorkspace({
      hasWorkItemDetail: true,
      roleByID: new Map([["developer", role()]]),
      roles: [role()],
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      workItems: [item],
      workLoadState: "loaded",
      workspaceTab: "work",
    });

    const disclosure = screen
      .getByText("Project Assistant", { selector: "span" })
      .closest("details");
    const selectedWork = screen.getByRole("region", { name: "Selected work item" });
    expect(disclosure).not.toHaveAttribute("open");
    expect(screen.getByRole("region", { name: "Assistant panel", hidden: true })).toHaveAttribute(
      "data-header",
      "false",
    );
    expect(selectedWork.compareDocumentPosition(disclosure!)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    expect(screen.getByRole("region", { name: "Work coordination" })).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Project overview" })).toBeNull();
  });

  it.each([
    [
      "proposal",
      {
        proposal: {
          id: "proposal_1",
          title: "Assign work",
          summary: "",
          actions: [],
          requires_confirmation: true,
        },
      },
    ],
    ["error", { error: "Drafting failed." }],
    ["context", { contextStatus: "loading" }],
    [
      "apply result",
      { applyResult: { proposal_id: "proposal_1", status: "applied", applied: true, actions: [] } },
    ],
  ])("opens the project assistant for %s attention", (_label, assistantState) => {
    const item = workItem();
    renderWorkspace({
      assistant: {
        ...assistant(),
        ...(assistantState as Partial<NonNullable<ProjectWorkspaceViewProps["assistant"]>>),
      },
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      workItems: [item],
      workspaceTab: "work",
    });

    expect(
      screen.getByText("Project Assistant", { selector: "span" }).closest("details"),
    ).toHaveAttribute("open");
  });

  it("opens the project assistant when a proposal arrives from collapsed idle state", () => {
    const item = workItem();
    const { props, rerender } = renderWorkspace({
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      workItems: [item],
      workspaceTab: "work",
    });
    const disclosure = screen
      .getByText("Project Assistant", { selector: "span" })
      .closest("details")!;
    expect(disclosure).not.toHaveAttribute("open");

    rerender(
      <ProjectWorkspaceView
        {...props}
        assistant={{
          ...assistant(),
          proposal: {
            id: "proposal_1",
            title: "Assign work",
            summary: "",
            actions: [],
            requires_confirmation: true,
          },
        }}
      />,
    );

    expect(disclosure).toHaveAttribute("open");
  });

  it("reopens collapsed context when a new proposal arrives", async () => {
    const item = workItem();
    const inspectedContext = {
      ...assistant(),
      context: {} as NonNullable<ProjectWorkspaceViewProps["assistant"]["context"]>,
      contextStatus: "loaded" as const,
    };
    const { props, rerender } = renderWorkspace({
      assistant: inspectedContext,
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      workItems: [item],
      workspaceTab: "work",
    });
    const disclosure = screen
      .getByText("Project Assistant", { selector: "span" })
      .closest("details")!;
    const summary = within(disclosure)
      .getByText("Project Assistant", { selector: "span" })
      .closest("summary")!;
    expect(disclosure).toHaveAttribute("open");
    await userEvent.click(summary);
    expect(disclosure).not.toHaveAttribute("open");

    rerender(
      <ProjectWorkspaceView
        {...props}
        assistant={{
          ...inspectedContext,
          proposal: {
            id: "proposal_from_chat",
            title: "Assign work",
            summary: "",
            actions: [],
            requires_confirmation: true,
          },
        }}
      />,
    );

    expect(disclosure).toHaveAttribute("open");
  });

  it.each([
    [
      "proposal",
      {
        proposal: {
          id: "proposal_1",
          title: "Assign work",
          summary: "",
          actions: [],
          requires_confirmation: true,
        },
      },
    ],
    ["error", { error: "Drafting failed." }],
  ])("allows settled %s attention to be collapsed", async (_label, assistantState) => {
    const item = workItem();
    renderWorkspace({
      assistant: {
        ...assistant(),
        ...(assistantState as Partial<NonNullable<ProjectWorkspaceViewProps["assistant"]>>),
      },
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      workItems: [item],
      workspaceTab: "work",
    });

    const disclosure = screen
      .getByText("Project Assistant", { selector: "span" })
      .closest("details")!;
    const summary = within(disclosure)
      .getByText("Project Assistant", { selector: "span" })
      .closest("summary")!;
    expect(disclosure).toHaveAttribute("open");
    expect(summary).toHaveAttribute("aria-disabled", "false");
    await userEvent.click(summary);
    expect(disclosure).not.toHaveAttribute("open");
  });

  it.each([
    ["drafting", { status: "proposing" }],
    ["context inspection", { contextStatus: "loading" }],
  ])("keeps the project assistant open during %s", async (_label, assistantState) => {
    const item = workItem();
    renderWorkspace({
      assistant: {
        ...assistant(),
        ...(assistantState as Partial<NonNullable<ProjectWorkspaceViewProps["assistant"]>>),
      },
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      workItems: [item],
      workspaceTab: "work",
    });

    const disclosure = screen
      .getByText("Project Assistant", { selector: "span" })
      .closest("details")!;
    const summary = within(disclosure)
      .getByText("Project Assistant", { selector: "span" })
      .closest("summary")!;
    expect(disclosure).toHaveAttribute("open");
    expect(summary).toHaveAttribute("aria-disabled", "true");
    await userEvent.click(summary);
    expect(disclosure).toHaveAttribute("open");
  });

  it.each(["done", "cancelled"] as const)(
    "removes proposal controls from persisted %s work",
    (status) => {
      const item = workItem({ status });
      renderWorkspace({
        selectedWorkItem: item,
        selectedWorkItemID: item.id,
        workItems: [item],
        workspaceTab: "work",
      });

      expect(screen.queryByText("Assistant panel")).toBeNull();
    },
  );

  it("links tab panels and supports roving keyboard navigation", async () => {
    const { handlers } = renderWorkspace();
    const overviewTab = screen.getByRole("tab", { name: "Overview" });
    const workTab = screen.getByRole("tab", { name: /Work/ });
    const overviewPanel = screen.getByRole("tabpanel", { name: "Overview" });

    expect(overviewTab).toHaveAttribute("tabindex", "0");
    expect(workTab).toHaveAttribute("tabindex", "-1");
    expect(overviewTab).toHaveAttribute("aria-controls", overviewPanel.id);
    expect(workTab).not.toHaveAttribute("aria-controls");
    expect(overviewPanel).toHaveAttribute("aria-labelledby", overviewTab.id);

    overviewTab.focus();
    await userEvent.keyboard("{ArrowRight}");

    expect(workTab).toHaveFocus();
    expect(handlers.onWorkspaceTabChange).toHaveBeenCalledWith("work");

    handlers.onWorkspaceTabChange.mockClear();
    await userEvent.keyboard(" ");
    expect(handlers.onWorkspaceTabChange).toHaveBeenCalledWith("work");
  });

  it("gives the active Work view a top-level heading", () => {
    renderWorkspace({ workspaceTab: "work" });

    expect(screen.getByRole("heading", { level: 1, name: "Work Queue" })).toBeTruthy();
  });

  it("renders project operations brief items", async () => {
    const operationItem = {
      id: "start_queued_assignment:proj_1:asgn_1",
      kind: "start_queued_assignment",
      priority: "high",
      title: "Review queued assignment: Build cockpit UI",
      detail: "Review launch details before starting this assignment.",
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

    const operations = screen.getByRole("region", {
      name: "Project operations",
    });
    expect(within(operations).getByRole("status")).toHaveAttribute("aria-busy", "false");
    expect(within(operations).getByText("Review queued assignment: Build cockpit UI")).toBeTruthy();
    await userEvent.click(within(operations).getByRole("button", { name: /Review start/ }));
    expect(handlers.onOperationAction).toHaveBeenCalledWith(operationItem);
  });

  it("promotes only the server-targeted assignment launch after direct work selection", () => {
    const item = workItem();
    const target = assignment({ id: "assign_target" });
    const decoy = assignment({ id: "assign_decoy" });
    const operationItem: ProjectOperationsBriefItem = {
      id: `start_queued_assignment:proj_1:${target.id}`,
      kind: "start_queued_assignment",
      priority: "high",
      title: "Review queued assignment",
      detail: "Open launch checks before starting this assignment.",
      action_label: "Review start",
      target: {
        surface: "work",
        project_id: "proj_1",
        work_item_id: item.id,
        assignment_id: target.id,
      },
      action: {
        type: "open_assignment_preflight",
        project_id: "proj_1",
        work_item_id: item.id,
        assignment_id: target.id,
      },
    };

    renderWorkspace({
      assignments: [decoy, target],
      hasWorkItemDetail: true,
      operationsBrief: {
        project_id: "proj_1",
        generated_at: "2026-07-13T12:00:00Z",
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
      operationsBriefLoadState: "loaded",
      roleByID: new Map([["developer", role()]]),
      roles: [role()],
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      selectedWorkItemReadiness: {
        project_id: "proj_1",
        work_item_id: item.id,
        ready: false,
        status: "blocked",
        title: "Assignment is waiting",
        detail: "Review launch checks before starting.",
        blockers: ["A queued assignment has not started"],
        warnings: [],
        assignment_count: 2,
        completed_assignments: 0,
        review_follow_up_count: 0,
      },
      workItems: [item],
      workLoadState: "loaded",
      workspaceTab: "work",
    });

    const targetStory = screen.getByRole("article", {
      name: `Developer assignment execution ${target.id}`,
    });
    const decoyStory = screen.getByRole("article", {
      name: `Developer assignment execution ${decoy.id}`,
    });
    expect(within(targetStory).getByRole("button", { name: "Review & start" })).toHaveClass(
      "btn-primary",
    );
    expect(within(decoyStory).getByRole("button", { name: "Review & start" })).toHaveClass(
      "btn-ghost",
    );
    expect(screen.getByText("Assistant panel")).toHaveAttribute("data-primary", "false");
    expect(screen.queryByRole("region", { name: "Next work item action" })).toBeNull();
  });

  it.each([
    [
      "effective status changed",
      assignment({
        id: "assign_stale",
        status: "queued",
        execution_ref: { kind: "task_run", status: "running" },
      }),
    ],
    [
      "destination changed",
      assignment({
        id: "assign_stale",
        driver_kind: "manual",
        execution_ref: { kind: "none", status: "queued" },
      }),
    ],
  ])("fails closed when a preflight target's %s", (_label, staleAssignment) => {
    const item = workItem();
    const operationItem: ProjectOperationsBriefItem = {
      id: `start_queued_assignment:proj_1:${staleAssignment.id}`,
      kind: "start_queued_assignment",
      priority: "high",
      title: "Review queued assignment",
      detail: "Open launch checks before starting this assignment.",
      action_label: "Review start",
      target: {
        surface: "work",
        project_id: "proj_1",
        work_item_id: item.id,
        assignment_id: staleAssignment.id,
      },
      action: {
        type: "open_assignment_preflight",
        project_id: "proj_1",
        work_item_id: item.id,
        assignment_id: staleAssignment.id,
      },
    };

    renderWorkspace({
      assignments: [staleAssignment],
      hasWorkItemDetail: true,
      operationsBrief: {
        project_id: "proj_1",
        generated_at: "2026-07-13T12:00:00Z",
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
      operationsBriefLoadState: "loaded",
      roleByID: new Map([["developer", role()]]),
      roles: [role()],
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      selectedWorkItemReadiness: {
        project_id: "proj_1",
        work_item_id: item.id,
        ready: false,
        status: "blocked",
        title: "Assignment is waiting",
        detail: "Refresh changed assignment state.",
        blockers: ["Assignment state changed"],
        warnings: [],
        assignment_count: 1,
        completed_assignments: 0,
        review_follow_up_count: 0,
      },
      workItems: [item],
      workLoadState: "loaded",
      workspaceTab: "work",
    });

    const followThrough = screen.getByRole("region", { name: "Next work item action" });
    expect(within(followThrough).getByText("Next action unavailable")).toBeTruthy();
    expect(within(followThrough).getByRole("button", { name: "Refresh work" })).toHaveClass(
      "btn-primary",
    );
    expect(screen.getByText("Assistant panel")).toHaveAttribute("data-primary", "false");
    for (const routineAction of screen.queryAllByRole("button", { name: "Start work" })) {
      expect(routineAction).toHaveClass("btn-ghost");
    }
  });

  it("matches the selected work operation from the authoritative action only", () => {
    const item = workItem();
    renderWorkspace({
      assignments: [assignment()],
      hasWorkItemDetail: true,
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
        items: [
          {
            id: "mismatched:work_1",
            kind: "record_completion_evidence",
            priority: "high",
            title: "Descriptive target for the selected work item",
            detail: "The action points elsewhere.",
            action_label: "Open work",
            target: {
              surface: "work",
              project_id: "proj_1",
              work_item_id: "work_1",
              assignment_id: "assign_1",
            },
            action: {
              type: "open_work_item",
              project_id: "proj_1",
              work_item_id: "work_2",
              assignment_id: "assign_1",
            },
          },
        ],
      },
      roleByID: new Map([["developer", role()]]),
      roles: [role()],
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      workItems: [item],
      workLoadState: "loaded",
      workspaceTab: "work",
    });

    const followThrough = screen.getByRole("region", {
      name: "Next work item action",
    });
    expect(within(followThrough).getByText("Review closeout checks")).toBeTruthy();
    expect(
      within(followThrough).queryByText("Descriptive target for the selected work item"),
    ).toBeNull();
  });

  it("advances to the next server operation when the selected operation is resolved", () => {
    const item = workItem({ status: "review" });
    const targetAssignment = assignment({
      id: "assign_target",
      status: "completed",
    });
    const evidenceOperation: ProjectOperationsBriefItem = {
      id: "record_completion_evidence:proj_1:assign_target",
      kind: "record_completion_evidence",
      priority: "high",
      title: "Record evidence for the selected assignment",
      detail: "Leave a reviewable source before closeout.",
      action_label: "Open work",
      target: {
        surface: "work",
        project_id: "proj_1",
        work_item_id: item.id,
        assignment_id: targetAssignment.id,
      },
      action: {
        type: "open_work_item",
        project_id: "proj_1",
        work_item_id: item.id,
        assignment_id: targetAssignment.id,
      },
    };
    const closeoutOperation: ProjectOperationsBriefItem = {
      id: "close_work_item:proj_1:work_1",
      kind: "close_work_item",
      priority: "low",
      title: "Review closeout for Extract workspace",
      detail: "The server reports that closeout checks are clear.",
      action_label: "Open work",
      target: { surface: "work", project_id: "proj_1", work_item_id: item.id },
      action: {
        type: "open_work_item",
        project_id: "proj_1",
        work_item_id: item.id,
      },
    };
    const brief = (items: ProjectOperationsBriefItem[]) => ({
      project_id: "proj_1",
      generated_at: "2026-07-13T12:00:00Z",
      summary: {
        item_count: items.length,
        high_count: items.filter((operation) => operation.priority === "high").length,
        medium_count: 0,
        low_count: items.filter((operation) => operation.priority === "low").length,
        pending_memory_candidate_count: 0,
        pending_handoff_count: 0,
      },
      items,
    });
    const view = renderWorkspace({
      assignments: [targetAssignment],
      hasWorkItemDetail: true,
      operationsBrief: brief([evidenceOperation]),
      operationsBriefLoadState: "loaded",
      roleByID: new Map([["developer", role()]]),
      roles: [role()],
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      selectedWorkItemOperationID: evidenceOperation.id,
      selectedWorkItemReadiness: {
        project_id: "proj_1",
        work_item_id: item.id,
        ready: false,
        status: "blocked",
        title: "Closeout is blocked",
        detail: "Record completion evidence.",
        blockers: ["Evidence is missing"],
        warnings: [],
        assignment_count: 1,
        completed_assignments: 1,
        review_follow_up_count: 0,
        missing_evidence_assignment_ids: [targetAssignment.id],
      },
      workItems: [item],
      workLoadState: "loaded",
      workspaceTab: "work",
    });

    let followThrough = screen.getByRole("region", {
      name: "Next work item action",
    });
    expect(within(followThrough).getByText(evidenceOperation.title)).toBeTruthy();
    expect(within(followThrough).getByRole("button", { name: "Record evidence" })).toBeTruthy();

    view.rerender(
      <ProjectWorkspaceView
        {...view.props}
        operationsBrief={brief([closeoutOperation])}
        selectedWorkItemReadiness={{
          project_id: "proj_1",
          work_item_id: item.id,
          ready: true,
          status: "ready",
          title: "Ready to mark done",
          detail: "Closeout checks are clear.",
          blockers: [],
          warnings: [],
          assignment_count: 1,
          completed_assignments: 1,
          review_follow_up_count: 0,
        }}
      />,
    );

    followThrough = screen.getByRole("region", {
      name: "Next work item action",
    });
    expect(within(followThrough).getByText(closeoutOperation.title)).toBeTruthy();
    expect(within(followThrough).getByRole("button", { name: "Review closeout" })).toBeTruthy();
  });

  it("fails closed for a direct operation whose typed record is not loaded", () => {
    const item = workItem({ status: "review" });
    const missingAssignmentID = "assign_missing";
    const missingTargetOperation: ProjectOperationsBriefItem = {
      id: `record_completion_evidence:proj_1:${missingAssignmentID}`,
      kind: "record_completion_evidence",
      priority: "high",
      title: "Record evidence for missing assignment",
      detail: "The operation target is not in loaded work detail.",
      action_label: "Open work",
      target: {
        surface: "work",
        project_id: "proj_1",
        work_item_id: item.id,
        assignment_id: missingAssignmentID,
      },
      action: {
        type: "open_work_item",
        project_id: "proj_1",
        work_item_id: item.id,
        assignment_id: missingAssignmentID,
      },
    };
    renderWorkspace({
      assignments: [],
      artifacts: [],
      handoffs: [],
      hasWorkItemDetail: true,
      operationsBrief: {
        project_id: "proj_1",
        generated_at: "2026-07-13T12:00:00Z",
        summary: {
          item_count: 1,
          high_count: 1,
          medium_count: 0,
          low_count: 0,
          pending_memory_candidate_count: 0,
          pending_handoff_count: 0,
        },
        items: [missingTargetOperation],
      },
      operationsBriefLoadState: "loaded",
      roleByID: new Map([["developer", role()]]),
      roles: [role()],
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      selectedWorkItemReadiness: {
        project_id: "proj_1",
        work_item_id: item.id,
        ready: false,
        status: "blocked",
        title: "Closeout is blocked",
        detail: "Evidence is missing.",
        blockers: ["Evidence is missing"],
        warnings: [],
        assignment_count: 1,
        completed_assignments: 1,
        review_follow_up_count: 0,
        missing_evidence_assignment_ids: [missingAssignmentID],
      },
      workItems: [item],
      workLoadState: "loaded",
      workspaceTab: "work",
    });

    const followThrough = screen.getByRole("region", { name: "Next work item action" });
    expect(within(followThrough).getByText("Next action unavailable")).toBeTruthy();
    expect(within(followThrough).getByRole("button", { name: "Refresh work" })).toHaveClass(
      "btn-primary",
    );
    expect(screen.getByRole("button", { name: "Assign work" })).toHaveClass("btn-ghost");
  });

  it("hides proposal controls when authoritative readiness says work is done", () => {
    const item = workItem({ status: "review" });
    renderWorkspace({
      assignments: [assignment({ status: "completed" })],
      hasWorkItemDetail: true,
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      selectedWorkItemReadiness: {
        project_id: "proj_1",
        work_item_id: item.id,
        ready: false,
        status: "done",
        title: "Work item is done",
        detail: "The operator closed this work item.",
        blockers: [],
        warnings: [],
        assignment_count: 1,
        completed_assignments: 1,
        review_follow_up_count: 0,
      },
      workItems: [item],
      workLoadState: "loaded",
      workspaceTab: "work",
    });

    expect(screen.queryByText("Assistant panel")).toBeNull();
    expect(screen.getByText("Work closed")).toBeTruthy();
  });

  it("renders explicit overview loading state", () => {
    renderWorkspace({
      activityLoadState: "loading",
      operationsBriefLoadState: "loading",
      workItems: [workItem()],
      workLoadState: "loaded",
    });

    const overview = screen.getByRole("region", { name: "Project overview" });
    const operationsStatus = within(
      within(overview).getByRole("region", { name: "Project operations" }),
    ).getByRole("status");
    expect(operationsStatus).toHaveAttribute("aria-live", "polite");
    expect(operationsStatus).toHaveAttribute("aria-atomic", "true");
    expect(operationsStatus).toHaveAttribute("aria-busy", "true");
    expect(within(overview).getByText("Loading operations…")).toBeTruthy();
    expect(
      within(overview).getByText(
        "Checking project work, memory candidates, handoffs, and launch defaults.",
      ),
    ).toBeTruthy();
    const activitySummary = within(overview).getByRole("region", {
      name: "Project activity summary",
    });
    const activityStatus = within(activitySummary).getByRole("status");
    expect(activityStatus).toHaveAttribute("aria-live", "polite");
    expect(activityStatus).toHaveAttribute("aria-atomic", "true");
    expect(activityStatus).toHaveAttribute("aria-busy", "true");
    expect(within(activitySummary).getByText("Updating activity…")).toBeTruthy();
    expect(
      within(activitySummary).getByText("Checking assignment progress and blockers."),
    ).toHaveStyle({ color: "var(--t2)" });
    expect(within(overview).queryByText("No project work yet")).toBeNull();
    expect(within(overview).queryByText(/Create a work item/)).toBeNull();
    expect(within(overview).queryByRole("button", { name: /Blocked/ })).toBeNull();
    expect(within(activitySummary).getByRole("button", { name: "View work" })).toBeTruthy();
  });

  it("holds the guided shell while project setup is still loading", () => {
    renderWorkspace({ projectSetupPending: true, workLoadState: "loading" });

    const loading = screen.getByRole("region", {
      name: "Project setup loading",
    });
    expect(loading).toHaveAttribute("aria-busy", "true");
    expect(within(loading).getByRole("status")).toHaveTextContent("Loading project…");
    expect(within(loading).getByText("Loading project…")).toBeTruthy();
    expect(screen.queryByRole("tab", { name: "Overview" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Project onboarding" })).toBeNull();
  });

  it("fails closed with a retry when project setup status is unavailable", async () => {
    const { handlers } = renderWorkspace({
      projectSetupError: "Failed to load project setup status.",
    });

    const unavailable = screen.getByRole("region", {
      name: "Project setup unavailable",
    });
    expect(within(unavailable).getByRole("alert")).toHaveTextContent(
      "Failed to load project setup status.",
    );
    expect(screen.queryByRole("tab", { name: "Overview" })).toBeNull();

    await userEvent.click(within(unavailable).getByRole("button", { name: "Retry" }));
    expect(handlers.onRefreshWorkItem).toHaveBeenCalledTimes(1);
  });

  it("announces a failed overview projection refresh", () => {
    renderWorkspace({
      overviewError: "Project coordination status could not be refreshed.",
      workItems: [workItem()],
    });

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Project coordination status could not be refreshed.",
    );
  });

  it("distinguishes an overview load failure from an empty project", () => {
    renderWorkspace({
      activityLoadState: "error",
      overviewError: "Project coordination is temporarily unavailable.",
      workItems: [workItem()],
      workLoadState: "loaded",
    });

    const overview = screen.getByRole("region", { name: "Project overview" });
    const activitySummary = within(overview).getByRole("region", {
      name: "Project activity summary",
    });
    const activityStatus = within(activitySummary).getByRole("status");
    expect(activityStatus).toHaveAttribute("aria-busy", "false");
    expect(within(activitySummary).getByText("Activity unavailable")).toBeTruthy();
    expect(within(activitySummary).getByText("Refresh project work to try again.")).toBeTruthy();
    expect(screen.getByText("Project coordination is temporarily unavailable.")).toBeTruthy();
    expect(within(overview).queryByText("No project work yet")).toBeNull();
    expect(within(activitySummary).queryByRole("button", { name: /Blocked/ })).toBeNull();
    expect(within(activitySummary).getByRole("button", { name: "View work" })).toBeTruthy();
  });

  it("uses projected work count when the work list is unavailable", () => {
    const projectedActivity = activity({
      summary: {
        work_item_count: 2,
        assignment_count: 0,
        active_count: 0,
        blocked_count: 0,
        completed_count: 0,
        recent_count: 0,
      },
      buckets: { active: [], blocked: [], completed: [], recent: [] },
      recent: [],
    });
    const { handlers, props, rerender } = renderWorkspace({
      activity: projectedActivity,
      activityLoadState: "loaded",
      workItems: [],
      workLoadState: "error",
    });

    const summary = screen.getByRole("region", {
      name: "Project activity summary",
    });
    expect(within(summary).getByText("2 work items")).toBeTruthy();
    expect(within(summary).getByText("Project activity reports current work.")).toBeTruthy();
    expect(within(summary).queryByText("No project work yet")).toBeNull();
    expect(screen.getByRole("tab", { name: /Work/ })).toHaveTextContent("2");

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        activity={projectedActivity}
        activityLoadState="loaded"
        workItems={[]}
        workLoadState="error"
        workspaceTab="work"
      />,
    );
    expect(
      screen.getByText("Work items are unavailable. Refresh project work to try again."),
    ).toBeTruthy();
    expect(screen.queryByText("No work items for this project.")).toBeNull();
  });

  it("keeps activity filters non-authoritative while the projection updates or fails", () => {
    const item = workItem();
    const { handlers, props, rerender } = renderWorkspace({
      activity: null,
      activityBucket: "blocked",
      activityLoadState: "loading",
      workItems: [item],
      workLoadState: "loaded",
      workspaceTab: "work",
    });

    let filters = screen.getByLabelText("Work activity filters");
    expect(within(filters).getByRole("button", { name: "Show all work items" })).toHaveTextContent(
      "1",
    );
    expect(within(filters).getByRole("button", { name: "Show all work items" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(
      within(filters).queryByRole("button", {
        name: "Show blocked assignments",
      }),
    ).toBeNull();
    let status = within(filters).getByRole("status");
    expect(status).toHaveAttribute("aria-live", "polite");
    expect(status).toHaveAttribute("aria-atomic", "true");
    expect(status).not.toHaveAttribute("aria-busy");
    expect(status).toHaveTextContent("Updating assignment activity…");
    expect(screen.queryByText("No activity is recorded for this project yet.")).toBeNull();
    expect(screen.queryByText("No blocked assignments for this project.")).toBeNull();

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        activity={null}
        activityBucket="blocked"
        activityLoadState="error"
        workItems={[item]}
        workLoadState="loaded"
        workspaceTab="work"
      />,
    );

    filters = screen.getByLabelText("Work activity filters");
    status = within(filters).getByRole("status");
    expect(status).not.toHaveAttribute("aria-busy");
    expect(status).toHaveTextContent(
      "Assignment activity unavailable. Refresh project work to try again.",
    );
    expect(
      within(filters).queryByRole("button", {
        name: "Show blocked assignments",
      }),
    ).toBeNull();
    expect(screen.queryByText("No activity is recorded for this project yet.")).toBeNull();
    expect(screen.queryByText("No blocked assignments for this project.")).toBeNull();

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        activity={activity()}
        activityBucket="blocked"
        activityLoadState="loaded"
        workItems={[item]}
        workLoadState="loaded"
        workspaceTab="work"
      />,
    );

    filters = screen.getByLabelText("Work activity filters");
    expect(within(filters).getByRole("button", { name: "Show all work items" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    expect(
      within(filters).getByRole("button", { name: "Show blocked assignments" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("returns focus to the work queue when a refreshed filter removes the focused row", () => {
    const item = workItem({ title: "Move between buckets" });
    const activeItem = activityItem({
      id: "assign_active",
      assignment: assignment({
        id: "assign_active",
        work_item_id: item.id,
        status: "running",
        execution_ref: { kind: "task_run", status: "running" },
      }),
      blocking_signal: undefined,
      status: "running",
      status_summary: "running",
      work_item: {
        id: item.id,
        title: item.title,
        status: item.status,
        priority: item.priority,
      },
    });
    const activeProjection = activity({
      summary: {
        work_item_count: 1,
        assignment_count: 1,
        active_count: 1,
        blocked_count: 0,
        completed_count: 0,
        recent_count: 1,
      },
      buckets: { active: [activeItem], blocked: [], completed: [], recent: [activeItem] },
      recent: [activeItem],
    });
    const { handlers, props, rerender } = renderWorkspace({
      activity: activeProjection,
      activityBucket: "active",
      activityLoadState: "loaded",
      workItems: [item],
      workLoadState: "loaded",
      workspaceTab: "work",
    });
    const focusedRow = screen.getByRole("link", { name: "Open work item Move between buckets" });
    focusedRow.focus();

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        activity={{
          ...activeProjection,
          summary: {
            ...activeProjection.summary,
            active_count: 0,
            completed_count: 1,
          },
          buckets: { active: [], blocked: [], completed: [activeItem], recent: [activeItem] },
        }}
        activityBucket="active"
        workItems={[item]}
        workLoadState="loaded"
        workspaceTab="work"
      />,
    );

    const queue = screen.getByRole("region", { name: "Work queue" });
    expect(queue).toHaveFocus();
    const message =
      "Move between buckets is no longer in this work view. Focus returned to the work queue.";
    const firstAnnouncement = within(queue).getByText(message);

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        activity={activeProjection}
        activityBucket="active"
        workItems={[item]}
        workLoadState="loaded"
        workspaceTab="work"
      />,
    );
    screen.getByRole("link", { name: "Open work item Move between buckets" }).focus();
    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        activity={{
          ...activeProjection,
          summary: {
            ...activeProjection.summary,
            active_count: 0,
            completed_count: 1,
          },
          buckets: { active: [], blocked: [], completed: [activeItem], recent: [activeItem] },
        }}
        activityBucket="active"
        workItems={[item]}
        workLoadState="loaded"
        workspaceTab="work"
      />,
    );

    expect(queue).toHaveFocus();
    expect(within(queue).getByText(message)).not.toBe(firstAnnouncement);
  });

  it("returns focus to the work queue when refreshed authority removes selected work", () => {
    const item = workItem({ title: "Remove selected work" });
    const { handlers, props, rerender } = renderWorkspace({
      hasWorkItemDetail: true,
      selectedWorkItem: item,
      selectedWorkItemID: item.id,
      workItems: [item],
      workLoadState: "loaded",
      workspaceTab: "work",
    });
    within(screen.getByRole("region", { name: "Selected work item" }))
      .getByRole("button", { name: "Add responsibility" })
      .focus();

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        hasWorkItemDetail={false}
        selectedWorkItem={null}
        selectedWorkItemID=""
        workItems={[]}
        workLoadState="loaded"
        workspaceTab="work"
      />,
    );

    expect(screen.getByRole("region", { name: "Work queue" })).toHaveFocus();
    expect(
      screen.getByText(
        "Remove selected work is no longer available. Focus returned to the work queue.",
      ),
    ).toBeTruthy();
  });

  it("keeps detail focus during deliberate cross-work selection while the previous work remains", () => {
    const source = workItem({ id: "work_source", title: "Prepare handoff" });
    const target = workItem({ id: "work_target", title: "Receive handoff" });
    const { handlers, props, rerender } = renderWorkspace({
      hasWorkItemDetail: true,
      selectedWorkItem: source,
      selectedWorkItemID: source.id,
      workItems: [source, target],
      workLoadState: "loaded",
      workspaceTab: "work",
    });
    const responsibilityButton = within(
      screen.getByRole("region", { name: "Selected work item" }),
    ).getByRole("button", { name: "Add responsibility" });
    responsibilityButton.focus();

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        hasWorkItemDetail
        selectedWorkItem={target}
        selectedWorkItemID={target.id}
        workItems={[source, target]}
        workLoadState="loaded"
        workspaceTab="work"
      />,
    );

    expect(responsibilityButton).toHaveFocus();
    expect(screen.getByRole("heading", { level: 2, name: "Receive handoff" })).toBeTruthy();
    expect(screen.queryByText(/is no longer available\. Focus returned/)).toBeNull();
  });

  it("explains compact project operations limits", () => {
    const operationItems = Array.from({ length: 8 }, (_, index) => ({
      id: `prepare_first_assignment:proj_1:work_${index}`,
      kind: "prepare_first_assignment",
      priority: "medium",
      title: `Prepare first assignment: Work ${index}`,
      detail: "This work item has no queued or running assignments yet.",
      action_label: "Draft assignment",
      target: {
        surface: "work",
        project_id: "proj_1",
        work_item_id: `work_${index}`,
      },
      action: {
        type: "draft_project_proposal",
        project_id: "proj_1",
        work_item_id: `work_${index}`,
        request: `Queue an assignment for Work ${index}`,
      },
    }));
    renderWorkspace({
      operationsBrief: {
        project_id: "proj_1",
        generated_at: "2026-06-13T00:00:00Z",
        summary: {
          item_count: 8,
          available_item_count: 9,
          omitted_item_count: 1,
          item_limit: 8,
          high_count: 0,
          medium_count: 8,
          low_count: 0,
          pending_memory_candidate_count: 0,
          pending_handoff_count: 0,
        },
        items: operationItems,
      },
    });

    const operations = screen.getByRole("region", {
      name: "Project operations",
    });
    expect(
      within(operations).getByText(
        "Showing 4 of 9 operations; 5 lower-priority operations are hidden (1 capped by the server).",
      ),
    ).toBeTruthy();
  });

  it("returns focus to Overview when a refreshed operation action disappears", () => {
    const operation: ProjectOperationsBriefItem = {
      id: "prepare_first_assignment:proj_1:work_1",
      kind: "prepare_first_assignment",
      priority: "medium",
      title: "Prepare first assignment",
      detail: "This work item needs an assignment.",
      action_label: "Draft assignment",
      target: { surface: "work", project_id: "proj_1", work_item_id: "work_1" },
      action: {
        type: "draft_project_proposal",
        project_id: "proj_1",
        work_item_id: "work_1",
        request: "Queue an assignment",
      },
    };
    const brief = {
      project_id: "proj_1",
      generated_at: "2026-06-13T00:00:00Z",
      summary: {
        item_count: 1,
        high_count: 0,
        medium_count: 1,
        low_count: 0,
        pending_memory_candidate_count: 0,
        pending_handoff_count: 0,
      },
      items: [operation],
    };
    const { handlers, props, rerender } = renderWorkspace({ operationsBrief: brief });
    const action = screen.getByRole("button", {
      name: "Draft assignment: Prepare first assignment",
    });
    action.focus();

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        operationsBrief={{
          ...brief,
          summary: { ...brief.summary, item_count: 0, medium_count: 0 },
          items: [],
        }}
      />,
    );

    expect(screen.getByRole("tab", { name: /Overview/ })).toHaveFocus();
    const message = "Project operations changed. Focus returned to the Overview tab.";
    const firstAnnouncement = screen.getByText(message);

    rerender(<ProjectWorkspaceView {...props} {...handlers} operationsBrief={brief} />);
    screen.getByRole("button", { name: "Draft assignment: Prepare first assignment" }).focus();
    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        operationsBrief={{
          ...brief,
          summary: { ...brief.summary, item_count: 0, medium_count: 0 },
          items: [],
        }}
      />,
    );

    expect(screen.getByRole("tab", { name: /Overview/ })).toHaveFocus();
    expect(screen.getByText(message)).not.toBe(firstAnnouncement);
  });

  it("returns focus to Overview when a refreshed operation keeps its label but changes target", () => {
    const operation: ProjectOperationsBriefItem = {
      id: "prepare_first_assignment:proj_1:work_1",
      kind: "prepare_first_assignment",
      priority: "medium",
      title: "Prepare first assignment",
      detail: "This work item needs an assignment.",
      action_label: "Draft assignment",
      target: { surface: "work", project_id: "proj_1", work_item_id: "work_1" },
      action: {
        type: "draft_project_proposal",
        project_id: "proj_1",
        work_item_id: "work_1",
        request: "Queue an assignment",
      },
    };
    const brief = {
      project_id: "proj_1",
      generated_at: "2026-06-13T00:00:00Z",
      summary: {
        item_count: 1,
        high_count: 0,
        medium_count: 1,
        low_count: 0,
        pending_memory_candidate_count: 0,
        pending_handoff_count: 0,
      },
      items: [operation],
    };
    const { handlers, props, rerender } = renderWorkspace({ operationsBrief: brief });
    screen.getByRole("button", { name: "Draft assignment: Prepare first assignment" }).focus();

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        operationsBrief={{
          ...brief,
          items: [
            {
              ...operation,
              target: { ...operation.target, work_item_id: "work_2" },
              action: {
                ...operation.action,
                work_item_id: "work_2",
                request: "Queue a different assignment",
              },
            },
          ],
        }}
      />,
    );

    expect(screen.getByRole("tab", { name: /Overview/ })).toHaveFocus();
    expect(
      screen.getByText("Project operations changed. Focus returned to the Overview tab."),
    ).toBeTruthy();
  });

  it("renders an activity summary and routes activity into work", async () => {
    const item = workItem({ id: "work_blocked", title: "Fix blocked launch" });
    const { handlers } = renderWorkspace({
      activity: activity(),
      memoryCandidates: [memoryCandidate()],
      workItems: [item],
    });

    const resume = screen.getByRole("region", {
      name: "Project activity summary",
    });
    expect(
      within(resume).getByText("Assignments: 0 active · 1 blocked · 0 completed"),
    ).toBeTruthy();

    await userEvent.click(within(resume).getByRole("button", { name: /Blocked/ }));
    await userEvent.click(within(resume).getByRole("button", { name: /Recent/ }));
    await userEvent.click(within(resume).getByRole("button", { name: /Memory/ }));
    await userEvent.click(within(resume).getByRole("button", { name: "View work" }));

    expect(handlers.onActivityBucketChange).toHaveBeenNthCalledWith(1, "blocked");
    expect(handlers.onActivityBucketChange).toHaveBeenNthCalledWith(2, "recent");
    expect(handlers.onNavigateWorkspaceTab).toHaveBeenCalledWith("memory");
    expect(handlers.onNavigateWorkspaceTab).toHaveBeenCalledWith("work");
    expect(handlers.onWorkspaceTabChange).not.toHaveBeenCalled();
    expect(handlers.onSelectWorkItem).not.toHaveBeenCalled();
  });

  it("summarizes active, memory, latest work, and empty activity states", async () => {
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

    let resume = screen.getByRole("region", {
      name: "Project activity summary",
    });
    expect(
      within(resume).getByText("Assignments: 1 active · 0 blocked · 0 completed"),
    ).toBeTruthy();

    await userEvent.click(within(resume).getByRole("button", { name: "View work" }));
    expect(handlers.onNavigateWorkspaceTab).toHaveBeenCalledWith("work");
    expect(handlers.onSelectWorkItem).not.toHaveBeenCalled();

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        activity={null}
        memoryCandidates={[candidate]}
        workItems={[]}
      />,
    );
    resume = screen.getByRole("region", { name: "Project activity summary" });
    expect(within(resume).getByText("No project work yet")).toBeTruthy();
    expect(within(resume).getByRole("button", { name: /Memory/ })).toBeTruthy();

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        activity={null}
        memoryCandidates={[]}
        workItems={[latest]}
      />,
    );
    resume = screen.getByRole("region", { name: "Project activity summary" });
    expect(within(resume).getByText("1 work item")).toBeTruthy();
    await userEvent.click(within(resume).getByRole("button", { name: "View work" }));
    expect(handlers.onNavigateWorkspaceTab).toHaveBeenCalledWith("work");
    expect(handlers.onSelectWorkItem).not.toHaveBeenCalled();

    rerender(
      <ProjectWorkspaceView
        {...props}
        {...handlers}
        activity={null}
        memoryCandidates={[]}
        workItems={[]}
      />,
    );
    resume = screen.getByRole("region", { name: "Project activity summary" });
    expect(within(resume).getByText("No project work yet")).toBeTruthy();
    expect(
      within(resume).getByText("Create a work item when there is something to coordinate."),
    ).toBeTruthy();
  });

  it("shows completed work without turning activity into a primary action", () => {
    renderWorkspace({
      activity: activity({
        summary: {
          work_item_count: 1,
          assignment_count: 1,
          active_count: 0,
          blocked_count: 0,
          completed_count: 1,
          recent_count: 1,
        },
        buckets: { active: [], blocked: [], completed: [], recent: [] },
        recent: [],
      }),
      workItems: [workItem()],
    });

    const summary = screen.getByRole("region", {
      name: "Project activity summary",
    });
    expect(
      within(summary).getByText("Assignments: 0 active · 0 blocked · 1 completed"),
    ).toBeTruthy();
    expect(within(summary).queryByRole("button", { name: /Completed/ })).toBeNull();
    expect(within(summary).queryByRole("button", { name: /View work/ })).toHaveClass("btn-ghost");
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
    expect(screen.getByRole("region", { name: "Project activity summary" })).toBeTruthy();
  });

  it("treats a null operations brief item list as empty", () => {
    renderWorkspace({
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
        items: null as unknown as ProjectOperationsBriefItem[],
      },
    });

    expect(screen.queryByRole("region", { name: "Project operations" })).toBeNull();
    expect(screen.getByRole("region", { name: "Project activity summary" })).toBeTruthy();
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
        assignment({
          status: "queued",
          execution_ref: { kind: "task_run", status: "queued" },
        }),
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

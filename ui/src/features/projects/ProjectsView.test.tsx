import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ProvidersAndModelsProvider } from "../../app/state/providersAndModels";
import { ProjectsProvider } from "../../app/state/projects";
import {
  createProjectAssignment,
  createProjectWorkRole,
  createProjectWorkItem,
  deleteProjectAssignment,
  deleteProjectWorkRole,
  deleteProjectWorkItem,
  getProjectActivity,
  getProjectAssignments,
  getProjectCollaborationArtifacts,
  getProjectWorkItem,
  getProjectWorkItems,
  getProjectWorkRoles,
  startProjectAssignment,
  updateProject,
  updateProjectAssignment,
  updateProjectWorkRole,
  updateProjectWorkItem,
} from "../../lib/api";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import launchContextContractRaw from "../../test/fixtures/launch-context-v1-contract.json";
import { withRuntimeConsole } from "../../test/runtime-console-render";
import type {
  ProjectAssignmentRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { ProjectsView } from "./ProjectsView";

type LaunchContextContract = {
  sections: string[];
  fields: Record<string, string[]>;
};

const launchContextContract = launchContextContractRaw as LaunchContextContract;

function emptyActivityData() {
  return {
    project_id: "",
    summary: {
      work_item_count: 0,
      assignment_count: 0,
      active_count: 0,
      blocked_count: 0,
      completed_count: 0,
      recent_count: 0,
    },
    buckets: {
      active: [],
      blocked: [],
      completed: [],
      recent: [],
    },
    recent: [],
  };
}

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    getProjectActivity: vi.fn(async () => ({
      object: "project_activity",
      data: emptyActivityData(),
    })),
    getProjectWorkRoles: vi.fn(async () => ({ object: "project_roles", data: [] })),
    getProjectWorkItems: vi.fn(async () => ({ object: "project_work_items", data: [] })),
    getProjectWorkItem: vi.fn(async () => ({ object: "project_work_item", data: null })),
    getProjectAssignments: vi.fn(async () => ({ object: "project_assignments", data: [] })),
    getProjectCollaborationArtifacts: vi.fn(async () => ({
      object: "project_collaboration_artifacts",
      data: [],
    })),
    startProjectAssignment: vi.fn(async () => ({ object: "project_assignment", data: null })),
    createProjectWorkItem: vi.fn(async () => ({ object: "project_work_item", data: null })),
    createProjectAssignment: vi.fn(async () => ({ object: "project_assignment", data: null })),
    createProjectWorkRole: vi.fn(async () => ({ object: "project_role", data: null })),
    updateProjectWorkRole: vi.fn(async () => ({ object: "project_role", data: null })),
    deleteProjectWorkRole: vi.fn(async () => undefined),
    updateProjectWorkItem: vi.fn(async () => ({ object: "project_work_item", data: null })),
    deleteProjectWorkItem: vi.fn(async () => undefined),
    updateProjectAssignment: vi.fn(async () => ({ object: "project_assignment", data: null })),
    deleteProjectAssignment: vi.fn(async () => undefined),
    updateProject: vi.fn(async () => ({ object: "project", data: null })),
  };
});

const project: ProjectRecord = {
  id: "proj_1",
  name: "Hecate",
  roots: [
    {
      id: "root_1",
      path: "/Users/alice/dev/hecate",
      kind: "git",
      git_branch: "main",
      active: true,
      created_at: "2026-06-01T10:00:00Z",
      updated_at: "2026-06-01T10:00:00Z",
    },
  ],
  default_provider: "ollama",
  default_model: "qwen2.5-coder",
  created_at: "2026-06-01T10:00:00Z",
  updated_at: "2026-06-01T11:00:00Z",
};

const role: ProjectWorkRoleRecord = {
  id: "software_developer",
  project_id: "proj_1",
  name: "Software developer",
  description: "Owns implementation work.",
  instructions: "Keep changes reviewable.",
  default_driver_kind: "hecate_task",
  default_provider: "anthropic",
  default_model: "claude-sonnet-4",
  default_agent_profile: "implementation",
  built_in: true,
};

const workItem: ProjectWorkItemRecord = {
  id: "work_1",
  project_id: "proj_1",
  title: "Build cockpit UI",
  brief: "Expose project work and native starts.",
  status: "ready",
  priority: "high",
  owner_role_id: "software_developer",
  reviewer_role_ids: ["reviewer_qa"],
  created_at: "2026-06-02T10:00:00Z",
  updated_at: "2026-06-02T11:00:00Z",
};

const hecateAssignment: ProjectAssignmentRecord = {
  id: "asgn_1",
  project_id: "proj_1",
  work_item_id: "work_1",
  role_id: "software_developer",
  driver_kind: "hecate_task",
  status: "queued",
  task_id: "task_1",
  run_id: "run_1",
  execution: {
    task_id: "task_1",
    run_id: "run_1",
    status: "awaiting_approval",
    task_status: "running",
    run_status: "awaiting_approval",
    pending_approval_count: 2,
    step_count: 4,
    artifact_count: 1,
    provider: "ollama",
    model: "qwen2.5-coder",
  },
  created_at: "2026-06-02T10:00:00Z",
  updated_at: "2026-06-02T11:00:00Z",
  started_at: "2026-06-02T10:30:00Z",
};

function resetProjectWorkMocks() {
  vi.mocked(getProjectActivity).mockResolvedValue({
    object: "project_activity",
    data: {
      project_id: project.id,
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
        blocked: [
          {
            id: hecateAssignment.id,
            project_id: project.id,
            work_item: {
              id: workItem.id,
              title: workItem.title,
              status: "running",
              priority: workItem.priority,
            },
            assignment: hecateAssignment,
            role,
            status: "awaiting_approval",
            blocking_signal: "awaiting_approval",
            status_summary: "2 approval pending",
            linked_task_id: "task_1",
            linked_run_id: "run_1",
            artifact_summary: { count: 1, latest_kind: "handoff", latest_title: "Runtime notes" },
            recent_artifacts: [
              {
                id: "art_1",
                project_id: project.id,
                work_item_id: workItem.id,
                assignment_id: hecateAssignment.id,
                kind: "handoff",
                title: "Runtime notes",
                body: "Approval is waiting.",
                created_at: "2026-06-02T11:05:00Z",
                updated_at: "2026-06-02T11:05:00Z",
              },
            ],
            updated_at: "2026-06-02T11:05:00Z",
          },
        ],
        completed: [],
        recent: [],
      },
      recent: [],
    },
  });
  vi.mocked(getProjectWorkRoles).mockResolvedValue({ object: "project_roles", data: [role] });
  vi.mocked(getProjectWorkItems).mockResolvedValue({
    object: "project_work_items",
    data: [{ ...workItem, assignments: [hecateAssignment] }],
  });
  vi.mocked(getProjectWorkItem).mockResolvedValue({
    object: "project_work_item",
    data: workItem,
  });
  vi.mocked(getProjectAssignments).mockResolvedValue({
    object: "project_assignments",
    data: [hecateAssignment],
  });
  vi.mocked(getProjectCollaborationArtifacts).mockResolvedValue({
    object: "project_collaboration_artifacts",
    data: [],
  });
  vi.mocked(startProjectAssignment).mockResolvedValue({
    object: "project_assignment",
    data: { ...hecateAssignment, status: "running" },
  });
  vi.mocked(createProjectWorkItem).mockResolvedValue({
    object: "project_work_item",
    data: { ...workItem, id: "work_new", title: "New cockpit work" },
  });
  vi.mocked(createProjectAssignment).mockResolvedValue({
    object: "project_assignment",
    data: { ...hecateAssignment, id: "asgn_new", status: "queued", execution: undefined },
  });
  vi.mocked(createProjectWorkRole).mockResolvedValue({
    object: "project_role",
    data: {
      id: "role_frontend_custom",
      project_id: "proj_1",
      name: "Frontend implementer",
      built_in: false,
    },
  });
  vi.mocked(updateProjectWorkRole).mockResolvedValue({
    object: "project_role",
    data: {
      id: "role_frontend_custom",
      project_id: "proj_1",
      name: "Frontend implementer",
      default_driver_kind: "external_agent",
      default_provider: "anthropic",
      default_model: "claude-sonnet-4",
      default_agent_profile: "safe_external_review",
      built_in: false,
    },
  });
  vi.mocked(deleteProjectWorkRole).mockResolvedValue(undefined);
  vi.mocked(updateProjectWorkItem).mockResolvedValue({
    object: "project_work_item",
    data: { ...workItem, title: "Edited cockpit UI", status: "review", priority: "urgent" },
  });
  vi.mocked(deleteProjectWorkItem).mockResolvedValue(undefined);
  vi.mocked(updateProjectAssignment).mockResolvedValue({
    object: "project_assignment",
    data: { ...hecateAssignment, role_id: "software_developer", status: "running" },
  });
  vi.mocked(deleteProjectAssignment).mockResolvedValue(undefined);
  vi.mocked(updateProject).mockResolvedValue({
    object: "project",
    data: {
      ...project,
      default_provider: "ollama",
      default_model: "ministral-3:latest",
      default_workspace_mode: "in_place",
    },
  });
}

function directWrapper(initialState: Parameters<typeof ProjectsProvider>[0]["initialState"]) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <ProvidersAndModelsProvider>
        <ProjectsProvider initialState={initialState}>{children}</ProjectsProvider>
      </ProvidersAndModelsProvider>
    );
  };
}

function expectLaunchContextContract(text: string) {
  const sectionLabels = launchContextContract.sections.map((section) =>
    section === "Project" ? "Project:" : section,
  );
  for (const section of sectionLabels) {
    expect(text).toContain(section);
  }
  for (const field of Object.values(launchContextContract.fields).flat()) {
    expect(text).toContain(`- ${field}:`);
  }
}

afterEach(() => {
  window.localStorage.clear();
  vi.mocked(getProjectActivity).mockReset();
  vi.mocked(getProjectWorkRoles).mockReset();
  vi.mocked(getProjectWorkItems).mockReset();
  vi.mocked(getProjectWorkItem).mockReset();
  vi.mocked(getProjectAssignments).mockReset();
  vi.mocked(getProjectCollaborationArtifacts).mockReset();
  vi.mocked(startProjectAssignment).mockReset();
  vi.mocked(createProjectWorkItem).mockReset();
  vi.mocked(createProjectAssignment).mockReset();
  vi.mocked(createProjectWorkRole).mockReset();
  vi.mocked(updateProjectWorkRole).mockReset();
  vi.mocked(deleteProjectWorkRole).mockReset();
  vi.mocked(updateProjectWorkItem).mockReset();
  vi.mocked(deleteProjectWorkItem).mockReset();
  vi.mocked(updateProjectAssignment).mockReset();
  vi.mocked(deleteProjectAssignment).mockReset();
  vi.mocked(updateProject).mockReset();
});

describe("ProjectsView index", () => {
  it("renders project rows with roots and defaults", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    render(<ProjectsView />, {
      wrapper: directWrapper({ projects: [project] }),
    });

    expect(screen.getByRole("button", { name: "Open project Hecate" })).toBeTruthy();
    expect(screen.getByText("/Users/alice/dev/hecate")).toBeTruthy();
    expect(screen.getByText("ollama / qwen2.5-coder")).toBeTruthy();
    expect((await screen.findAllByText("Build cockpit UI")).length).toBeGreaterThan(0);
  });

  it("renders empty, loading, and error states for the project index", () => {
    const empty = render(<ProjectsView />, {
      wrapper: directWrapper({ projects: [] }),
    });
    expect(screen.getByText("No projects yet")).toBeTruthy();
    empty.unmount();

    render(<ProjectsView />, {
      wrapper: directWrapper({ projects: [], loading: true }),
    });
    expect(screen.getByText("Loading projects…")).toBeTruthy();
    cleanup();

    render(<ProjectsView />, {
      wrapper: directWrapper({ projects: [], error: "project list failed" }),
    });
    expect(screen.getByText("project list failed")).toBeTruthy();
  });

  it("uses existing project actions for create, rename, and delete", async () => {
    resetProjectWorkMocks();
    const user = userEvent.setup();
    const actions = {
      ...createRuntimeConsoleActions(),
      createProjectFromFolder: vi.fn(async () => project),
      renameProject: vi.fn(async () => undefined),
      deleteProject: vi.fn(async () => true),
      selectProject: vi.fn(async () => undefined),
    };
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions }));

    await user.click(screen.getByRole("button", { name: "Add" }));
    expect(actions.createProjectFromFolder).toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Rename project Hecate" }));
    const renameInput = screen.getByLabelText("Rename Hecate");
    await user.type(renameInput, " workspace");
    expect(renameInput).toHaveValue("Hecate workspace");
    expect(actions.selectProject).not.toHaveBeenCalled();
    fireEvent.change(renameInput, {
      target: { value: "Hecate console" },
    });
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(actions.renameProject).toHaveBeenCalledWith(project.id, "Hecate console");

    await user.click(screen.getByRole("button", { name: "Delete project Hecate" }));
    expect(
      screen.getByText(/Workspace files and the git repository are not deleted/i),
    ).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Delete project record" }));
    expect(actions.deleteProject).toHaveBeenCalledWith(project.id);
  });
});

describe("ProjectsView cockpit", () => {
  it("loads work items after selecting a project", async () => {
    resetProjectWorkMocks();
    const state = createRuntimeConsoleFixture({ projects: [project] });
    const actions = {
      ...createRuntimeConsoleActions(),
      selectProject: vi.fn(async () => undefined),
    };
    render(withRuntimeConsole(<ProjectsView />, { state, actions }));

    await userEvent.click(screen.getByRole("button", { name: "Open project Hecate" }));

    await waitFor(() => {
      expect(getProjectWorkItems).toHaveBeenCalledWith(project.id);
    });
    expect(actions.selectProject).toHaveBeenCalledWith(project.id);
    expect((await screen.findAllByText("Build cockpit UI")).length).toBeGreaterThan(0);
  });

  it("clears stale work item selection before switching projects", async () => {
    resetProjectWorkMocks();
    const secondProject: ProjectRecord = {
      ...project,
      id: "proj_2",
      name: "Apollo",
      roots: [
        {
          ...project.roots[0],
          id: "root_2",
          path: "/Users/alice/dev/apollo",
        },
      ],
    };
    const secondWorkItem: ProjectWorkItemRecord = {
      ...workItem,
      id: "work_2",
      project_id: "proj_2",
      title: "Build Apollo cockpit",
      brief: "Show Apollo project work.",
    };
    vi.mocked(getProjectWorkItems).mockImplementation(async (projectID) => ({
      object: "project_work_items",
      data:
        projectID === secondProject.id
          ? [{ ...secondWorkItem, assignments: [] }]
          : [{ ...workItem, assignments: [hecateAssignment] }],
    }));
    vi.mocked(getProjectWorkItem).mockImplementation(async (projectID, workItemID) => ({
      object: "project_work_item",
      data:
        projectID === secondProject.id && workItemID === secondWorkItem.id
          ? secondWorkItem
          : workItem,
    }));
    vi.mocked(getProjectAssignments).mockImplementation(async (projectID) => ({
      object: "project_assignments",
      data: projectID === secondProject.id ? [] : [hecateAssignment],
    }));
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project, secondProject],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByText("Expose project work and native starts.")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Open project Apollo" }));

    expect(await screen.findByText("Show Apollo project work.")).toBeTruthy();
    expect(getProjectWorkItem).toHaveBeenCalledWith(secondProject.id, secondWorkItem.id);
    expect(getProjectWorkItem).not.toHaveBeenCalledWith(secondProject.id, workItem.id);
  });

  it("uses projected work-item assignments for list summaries without per-item requests", async () => {
    resetProjectWorkMocks();
    const secondWorkItem: ProjectWorkItemRecord = {
      ...workItem,
      id: "work_2",
      title: "Write project docs",
    };
    const emptyWorkItem: ProjectWorkItemRecord = {
      ...workItem,
      id: "work_3",
      title: "Plan empty lane",
    };
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [
        { ...workItem, assignments: [hecateAssignment] },
        {
          ...secondWorkItem,
          assignments: [{ ...hecateAssignment, id: "asgn_2", work_item_id: secondWorkItem.id }],
        },
        emptyWorkItem,
      ],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    const firstRow = await screen.findByRole("button", {
      name: "Open work item Build cockpit UI",
    });
    const secondRow = await screen.findByRole("button", {
      name: "Open work item Write project docs",
    });
    const emptyRow = await screen.findByRole("button", {
      name: "Open work item Plan empty lane",
    });
    expect(within(firstRow).queryByText("1 assignment")).toBeTruthy();
    expect(within(secondRow).getByText("1 assignment")).toBeTruthy();
    expect(within(emptyRow).queryByText(/assignment/)).toBeNull();
    await waitFor(() => {
      expect(getProjectAssignments).toHaveBeenCalledTimes(1);
    });
  });

  it("preserves the selected work item when refreshing project work", async () => {
    resetProjectWorkMocks();
    const secondWorkItem: ProjectWorkItemRecord = {
      ...workItem,
      id: "work_2",
      title: "Write project docs",
      brief: "Document the project workflow.",
    };
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [
        { ...workItem, assignments: [hecateAssignment] },
        { ...secondWorkItem, assignments: [] },
      ],
    });
    vi.mocked(getProjectWorkItem).mockImplementation(async (_projectID, workItemID) => ({
      object: "project_work_item",
      data: workItemID === secondWorkItem.id ? secondWorkItem : workItem,
    }));
    vi.mocked(getProjectAssignments).mockImplementation(async (_projectID, workItemID) => ({
      object: "project_assignments",
      data: workItemID === secondWorkItem.id ? [] : [hecateAssignment],
    }));
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    const secondRow = await screen.findByRole("button", {
      name: "Open work item Write project docs",
    });
    await userEvent.click(secondRow);
    expect(await screen.findByText("Document the project workflow.")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Refresh project work" }));

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Open work item Write project docs" }),
      ).toHaveAttribute("aria-current", "true");
    });
    expect(await screen.findByText("Document the project workflow.")).toBeTruthy();
  });

  it("shows selected work item assignments and projected execution state", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByText("Expose project work and native starts.")).toBeTruthy();
    const detail = screen.getByLabelText("Selected work item");
    expect(within(detail).getByText("Software developer")).toBeTruthy();
    expect(within(detail).getAllByText("approval").length).toBeGreaterThan(0);
    expect(within(detail).getAllByText("2 approval pending").length).toBeGreaterThan(0);
    expect(within(detail).getByText("4 steps")).toBeTruthy();
    expect(within(detail).getByText("ollama / qwen2.5-coder")).toBeTruthy();
  });

  it("renders project activity inbox states and actions", async () => {
    resetProjectWorkMocks();
    const onOpenTask = vi.fn();
    const onOpenChat = vi.fn();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(
      withRuntimeConsole(<ProjectsView onOpenTask={onOpenTask} onOpenChat={onOpenChat} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    expect(await screen.findByText("Activity Inbox")).toBeTruthy();
    expect(
      screen.getByText(/1 assignments across 1 work items; newest 20 per bucket/),
    ).toBeTruthy();
    expect(screen.getAllByText("2 approval pending").length).toBeGreaterThan(0);
    expect(screen.getByText("handoff: Runtime notes")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Task" }));
    expect(onOpenTask).toHaveBeenCalledWith("task_1", "run_1");

    await userEvent.click(screen.getByRole("button", { name: "Chat" }));
    expect(onOpenChat).toHaveBeenCalledWith(
      expect.objectContaining({
        projectID: project.id,
        model: "qwen2.5-coder",
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "Details" }));
    expect(screen.getByRole("button", { name: "Open work item Build cockpit UI" })).toHaveAttribute(
      "aria-current",
      "true",
    );
  });

  it("starts not-started assignments from the activity inbox", async () => {
    resetProjectWorkMocks();
    const notStartedAssignment: ProjectAssignmentRecord = {
      ...hecateAssignment,
      id: "asgn_not_started",
      task_id: "",
      run_id: "",
      status: "queued",
      execution: undefined,
      started_at: undefined,
    };
    vi.mocked(getProjectActivity).mockResolvedValue({
      object: "project_activity",
      data: {
        project_id: project.id,
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
          blocked: [
            {
              id: notStartedAssignment.id,
              project_id: project.id,
              work_item: {
                id: workItem.id,
                title: workItem.title,
                status: "ready",
                priority: workItem.priority,
              },
              assignment: notStartedAssignment,
              role,
              status: "queued",
              blocking_signal: "not_started",
              status_summary: "not started",
              artifact_summary: { count: 0 },
              updated_at: "2026-06-02T11:00:00Z",
            },
          ],
          completed: [],
          recent: [],
        },
        recent: [],
      },
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Start" }));

    expect(startProjectAssignment).toHaveBeenCalledWith(
      project.id,
      workItem.id,
      notStartedAssignment.id,
    );
  });

  it("opens chat from an assignment using the projected model", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const onOpenChat = vi.fn();
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(
      withRuntimeConsole(<ProjectsView onOpenChat={onOpenChat} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    await userEvent.click(await screen.findByRole("button", { name: "Open chat" }));

    expect(onOpenChat).toHaveBeenCalledWith(
      expect.objectContaining({
        projectID: project.id,
        provider: "ollama",
        model: "qwen2.5-coder",
        title: "Build cockpit UI - Software developer",
      }),
    );
    const request = onOpenChat.mock.calls[0]?.[0];
    expectLaunchContextContract(request.draft);
    expect(request.draft).toContain("Launch context");
    expect(request.draft).toContain("Project: Hecate (proj_1)");
    expect(request.draft).toContain("- Title: Build cockpit UI");
    expect(request.draft).toContain("- Status: awaiting_approval");
    expect(request.draft).toContain("- Driver: hecate_task");
    expect(request.draft).toContain("- Name: Software developer");
    expect(request.draft).toContain("- Provider: ollama");
    expect(request.draft).toContain("- Model: qwen2.5-coder");
    expect(request.draft).toContain(
      "Role defaults: driver=hecate_task, provider=anthropic, model=claude-sonnet-4, profile=implementation",
    );
    expect(request.draft).toContain("Linked runtime ids:");
    expect(request.draft).toContain("task=task_1, run=run_1");
    expect(request.draft).toContain("Request:\n- ");
  });

  it("indents multiline launch-context values in assignment chat drafts", async () => {
    resetProjectWorkMocks();
    const multilineRole: ProjectWorkRoleRecord = {
      ...role,
      description: "Owns implementation work.\nCoordinates with review.",
      instructions: "Keep changes reviewable.\nCall out risks.",
    };
    const multilineWorkItem: ProjectWorkItemRecord = {
      ...workItem,
      brief: "Expose project work and native starts.\nKeep the first launch editable.",
    };
    vi.mocked(getProjectWorkRoles).mockResolvedValue({
      object: "project_roles",
      data: [multilineRole],
    });
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...multilineWorkItem, assignments: [hecateAssignment] }],
    });
    vi.mocked(getProjectWorkItem).mockResolvedValue({
      object: "project_work_item",
      data: multilineWorkItem,
    });
    window.localStorage.setItem("hecate.project", project.id);
    const onOpenChat = vi.fn();
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(
      withRuntimeConsole(<ProjectsView onOpenChat={onOpenChat} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    await userEvent.click(await screen.findByRole("button", { name: "Open chat" }));

    const request = onOpenChat.mock.calls[0]?.[0];
    expect(request.draft).toContain(
      "- Brief: Expose project work and native starts.\n  Keep the first launch editable.",
    );
    expect(request.draft).toContain(
      "- Description: Owns implementation work.\n  Coordinates with review.",
    );
    expect(request.draft).toContain("- Instructions: Keep changes reviewable.\n  Call out risks.");
  });

  it("opens chat from an assignment using role defaults when no run is linked", async () => {
    resetProjectWorkMocks();
    const unstartedAssignment: ProjectAssignmentRecord = {
      ...hecateAssignment,
      task_id: "",
      run_id: "",
      execution: undefined,
      status: "queued",
      started_at: undefined,
    };
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...workItem, assignments: [unstartedAssignment] }],
    });
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [unstartedAssignment],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const onOpenChat = vi.fn();
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(
      withRuntimeConsole(<ProjectsView onOpenChat={onOpenChat} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    await userEvent.click(await screen.findByRole("button", { name: "Open chat" }));

    expect(onOpenChat).toHaveBeenCalledWith(
      expect.objectContaining({
        projectID: project.id,
        provider: "anthropic",
        model: "claude-sonnet-4",
        title: "Build cockpit UI - Software developer",
      }),
    );
    const request = onOpenChat.mock.calls[0]?.[0];
    expect(request.draft).toContain("- Status: queued");
    expect(request.draft).toContain("- Provider: anthropic");
    expect(request.draft).toContain("- Model: claude-sonnet-4");
    expect(request.draft).not.toContain("Linked runtime ids:");
  });

  it("creates work items from the Projects cockpit", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Work" }));
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "New cockpit work" },
    });
    fireEvent.change(screen.getByLabelText("Brief"), {
      target: { value: "Make project work creatable in the UI." },
    });
    fireEvent.change(screen.getByLabelText("Priority"), {
      target: { value: "urgent" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Create work item" }));

    expect(createProjectWorkItem).toHaveBeenCalledWith(project.id, {
      title: "New cockpit work",
      brief: "Make project work creatable in the UI.",
      status: "ready",
      priority: "urgent",
      owner_role_id: "software_developer",
    });
  });

  it("edits and deletes work items from the selected detail", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    const detail = screen.getByLabelText("Selected work item");
    expect(await within(detail).findByText("Expose project work and native starts.")).toBeTruthy();

    await userEvent.click(within(detail).getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Edited cockpit UI" },
    });
    fireEvent.change(screen.getByLabelText("Status"), {
      target: { value: "review" },
    });
    fireEvent.change(screen.getByLabelText("Priority"), {
      target: { value: "urgent" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Save work item" }));

    expect(updateProjectWorkItem).toHaveBeenCalledWith(project.id, workItem.id, {
      title: "Edited cockpit UI",
      brief: "Expose project work and native starts.",
      status: "review",
      priority: "urgent",
      owner_role_id: "software_developer",
      reviewer_role_ids: ["reviewer_qa"],
    });

    await userEvent.click(within(detail).getByRole("button", { name: "Delete" }));
    expect(
      screen.getByText(/Linked tasks, runs, chats, workspace files, and git history/i),
    ).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Delete work item" }));

    expect(deleteProjectWorkItem).toHaveBeenCalledWith(project.id, workItem.id);
  });

  it("adds assignments from the selected work item", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Assignment" }));
    fireEvent.change(screen.getByLabelText("Driver"), {
      target: { value: "external_agent" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Add assignment" }));

    expect(createProjectAssignment).toHaveBeenCalledWith(project.id, workItem.id, {
      role_id: "software_developer",
      driver_kind: "external_agent",
    });
  });

  it("uses a role default driver when adding assignments", async () => {
    const externalRole: ProjectWorkRoleRecord = {
      id: "role_external",
      project_id: project.id,
      name: "External reviewer",
      default_driver_kind: "external_agent",
      built_in: false,
    };
    resetProjectWorkMocks();
    vi.mocked(getProjectWorkRoles).mockResolvedValue({
      object: "project_roles",
      data: [role, externalRole],
    });
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Assignment" }));
    fireEvent.change(screen.getByLabelText("Role"), {
      target: { value: "role_external" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Add assignment" }));

    expect(createProjectAssignment).toHaveBeenCalledWith(project.id, workItem.id, {
      role_id: "role_external",
      driver_kind: "external_agent",
    });
  });

  it("creates custom roles with execution defaults", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Roles" }));
    const dialog = screen.getByRole("dialog", { name: "Project roles" });
    await userEvent.click(within(dialog).getByRole("button", { name: /New custom role/i }));
    fireEvent.change(within(dialog).getByLabelText("Name"), {
      target: { value: "Frontend implementer" },
    });
    fireEvent.change(within(dialog).getByLabelText("Description"), {
      target: { value: "Builds UI" },
    });
    fireEvent.change(within(dialog).getByLabelText("Instructions"), {
      target: { value: "Use existing UI primitives." },
    });
    fireEvent.change(within(dialog).getByLabelText("Default driver"), {
      target: { value: "hecate_task" },
    });
    fireEvent.change(within(dialog).getByLabelText("Default profile"), {
      target: { value: "implementation" },
    });
    fireEvent.change(within(dialog).getByLabelText("Default provider"), {
      target: { value: "ollama" },
    });
    fireEvent.change(within(dialog).getByLabelText("Default model"), {
      target: { value: "ministral-3:latest" },
    });
    await userEvent.click(within(dialog).getByRole("button", { name: "Create role" }));

    expect(createProjectWorkRole).toHaveBeenCalledWith(project.id, {
      name: "Frontend implementer",
      description: "Builds UI",
      instructions: "Use existing UI primitives.",
      default_driver_kind: "hecate_task",
      default_provider: "ollama",
      default_model: "ministral-3:latest",
      default_agent_profile: "implementation",
    });
    await waitFor(() => {
      expect(within(dialog).getByRole("button", { name: "Save role" })).toBeTruthy();
    });
    expect(within(dialog).getByRole("button", { name: "Delete role" })).toBeTruthy();
  });

  it("edits and deletes assignments from the selected work item", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Edit assignment asgn_1" }));
    fireEvent.change(screen.getByLabelText("Status"), {
      target: { value: "running" },
    });
    fireEvent.change(screen.getByLabelText("Driver"), {
      target: { value: "external_agent" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Save assignment" }));

    expect(updateProjectAssignment).toHaveBeenCalledWith(
      project.id,
      workItem.id,
      hecateAssignment.id,
      {
        role_id: "software_developer",
        driver_kind: "external_agent",
        status: "running",
        task_id: "task_1",
        run_id: "run_1",
        chat_session_id: "",
        message_id: "",
        context_snapshot_id: "",
      },
    );

    await userEvent.click(screen.getByRole("button", { name: "Delete assignment asgn_1" }));
    expect(
      screen.getByText(/Linked tasks, runs, chats, and external-agent executions/i),
    ).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Delete assignment" }));

    expect(deleteProjectAssignment).toHaveBeenCalledWith(
      project.id,
      workItem.id,
      hecateAssignment.id,
    );
  });

  it("updates project defaults needed by native starts", async () => {
    resetProjectWorkMocks();
    const projectWithUpdatedDefaults = {
      ...project,
      default_model: "ministral-3:latest",
    };
    window.localStorage.setItem("hecate.project", projectWithUpdatedDefaults.id);
    const state = createRuntimeConsoleFixture({
      projects: [projectWithUpdatedDefaults],
      activeProjectID: projectWithUpdatedDefaults.id,
      providers: [
        {
          name: "ollama",
          kind: "local",
          healthy: true,
          status: "healthy",
          credential_state: "not_required",
        },
      ],
      providerPresets: [
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
        },
      ],
      models: [
        {
          id: "qwen2.5-coder",
          owned_by: "ollama",
          metadata: { provider: "ollama", provider_kind: "local" },
        },
        {
          id: "ministral-3:latest",
          owned_by: "ollama",
          metadata: { provider: "ollama", provider_kind: "local" },
        },
      ],
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Defaults" }));
    expect(screen.getByRole("button", { name: /Ollama/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Model picker: ministral-3:latest/i })).toBeTruthy();
    expect(screen.queryByLabelText("Provider ID")).toBeNull();
    expect(screen.queryByLabelText("Model")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: /Model picker/i }));
    await userEvent.click(await screen.findByText("qwen2.5-coder"));
    expect(screen.getByRole("dialog", { name: "Project defaults" })).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Save defaults" }));

    expect(updateProject).toHaveBeenCalledWith(projectWithUpdatedDefaults.id, {
      default_provider: "ollama",
      default_model: "qwen2.5-coder",
      default_workspace_mode: "in_place",
    });
  });

  it("starts native Hecate assignments and refreshes detail state", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    const queuedAssignment = {
      ...hecateAssignment,
      status: "running",
      execution: { ...hecateAssignment.execution, status: "queued" },
    };
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [queuedAssignment],
    });
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    await userEvent.click(await screen.findByRole("button", { name: "Start" }));

    expect(startProjectAssignment).toHaveBeenCalledWith(
      project.id,
      workItem.id,
      queuedAssignment.id,
    );
    await waitFor(() => {
      expect(getProjectWorkItem).toHaveBeenCalledTimes(2);
    });
  });

  it("renders finished-only assignment timestamps without a blank started label", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [
        {
          ...hecateAssignment,
          status: "completed",
          started_at: undefined,
          completed_at: "2026-06-02T12:00:00Z",
          execution: {
            ...hecateAssignment.execution,
            status: "completed",
            started_at: undefined,
            finished_at: "2026-06-02T12:00:00Z",
          },
        },
      ],
    });
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByText(/^Finished /)).toBeTruthy();
    expect(screen.queryByText(/^Started\s*$/)).toBeNull();
  });

  it("does not expose native start for external-agent assignments", async () => {
    resetProjectWorkMocks();
    window.localStorage.setItem("hecate.project", project.id);
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [
        {
          ...hecateAssignment,
          id: "asgn_external",
          driver_kind: "external_agent",
          status: "queued",
          execution: undefined,
        },
      ],
    });
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    render(withRuntimeConsole(<ProjectsView />, { state, actions: createRuntimeConsoleActions() }));

    expect(await screen.findByText("Start in Chats")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Start" })).toBeNull();
  });
});

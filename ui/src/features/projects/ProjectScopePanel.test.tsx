import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useChat } from "../../app/state/chat";
import { ProjectsProvider, useProjects } from "../../app/state/projects";
import { createProject, getProjects, updateProject } from "../../lib/api";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";
import type { ProjectDeleteRecord, ProjectRecord } from "../../types/project";
import { ProjectScopePanel } from "./ProjectScopePanel";

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    createProject: vi.fn(),
    getProjects: vi.fn(),
    updateProject: vi.fn(),
  };
});

function wrapper(initialState: Parameters<typeof ProjectsProvider>[0]["initialState"]) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <ProjectsProvider initialState={initialState}>{children}</ProjectsProvider>;
  };
}

function renderPanel(initialState: Parameters<typeof ProjectsProvider>[0]["initialState"]) {
  return render(
    <ProjectScopePanel
      beginProjectDelete={() => 1}
      deleteMessage={(project) => `Delete ${project.name}?`}
      emptyHint="No projects yet."
      finishProjectDelete={() => undefined}
      noProjectDetail="No project selected."
    />,
    { wrapper: wrapper(initialState) },
  );
}

function CrossSurfaceHarness({
  onCurrentProjectSelected,
  onOldProjectSelected,
}: {
  onCurrentProjectSelected: (projectID: string, project: ProjectRecord | null) => void;
  onOldProjectSelected: (projectID: string, project: ProjectRecord | null) => void;
}) {
  const [surface, setSurface] = useState<"old" | "current">("old");
  const projects = useProjects();
  return (
    <>
      <button onClick={() => setSurface("current")}>Switch surface</button>
      <output aria-label="Active project">{projects.activeProjectID || "None"}</output>
      <output aria-label="Project catalog">
        {projects.state.projects.map((project) => project.name).join(", ") || "None"}
      </output>
      <ProjectScopePanel
        beginProjectDelete={() => 1}
        key={surface}
        deleteMessage={(project) => `Delete ${project.name}?`}
        emptyHint="No projects yet."
        finishProjectDelete={() => undefined}
        noProjectDetail="No project selected."
        onProjectSelected={surface === "old" ? onOldProjectSelected : onCurrentProjectSelected}
      />
    </>
  );
}

function SelectionSequenceHarness({
  onProjectSelected,
}: {
  onProjectSelected: (projectID: string, project: ProjectRecord | null) => Promise<boolean>;
}) {
  const projects = useProjects();
  return (
    <>
      <output aria-label="Active project sequence">{projects.activeProjectID || "None"}</output>
      <ProjectScopePanel
        beginProjectDelete={() => 1}
        deleteMessage={(item) => `Delete ${item.name}?`}
        emptyHint="No projects yet."
        finishProjectDelete={() => undefined}
        noProjectDetail="No project selected."
        onProjectSelected={onProjectSelected}
      />
    </>
  );
}

const project: ProjectRecord = {
  id: "project_scope",
  name: "Scope project",
  roots: [],
  created_at: "2026-07-13T10:00:00Z",
  updated_at: "2026-07-13T10:00:00Z",
};

function ReservedProjectScopePanel() {
  const chat = useChat();
  const projects = useProjects();
  const [turnAttempt, setTurnAttempt] = useState("not tried");
  return (
    <>
      <button
        type="button"
        onClick={() =>
          chat.actions.setPendingChatAttachments([
            {
              id: "scope-late-draft",
              file: new File(["image"], "scope-late.png", { type: "image/png" }),
            },
          ])
        }
      >
        Attach scope image
      </button>
      <button
        type="button"
        onClick={() =>
          setTurnAttempt(
            chat.actions.beginChatAttachmentTurn("scope-chat", 1) === null ? "blocked" : "started",
          )
        }
      >
        Try scope image submission
      </button>
      <button
        type="button"
        onClick={() =>
          projects.actions.setProjects((current) =>
            current.filter((item) => item.id !== project.id),
          )
        }
      >
        Remove deleted scope project from state
      </button>
      <span data-testid="scope-draft-count">{chat.state.pendingChatAttachments.length}</span>
      <span data-testid="scope-turn-attempt">{turnAttempt}</span>
      <ProjectScopePanel
        noProjectDetail="Unprojected"
        emptyHint="No projects"
        canChangeProjectScope={() => !chat.actions.chatOwnershipMutationBlockReason()}
        beginProjectDelete={chat.actions.beginChatOwnershipMutation}
        finishProjectDelete={chat.actions.finishChatOwnershipMutation}
        deleteMessage={(item) => <>Delete {item.name}?</>}
      />
    </>
  );
}

function CatalogOwnershipHarness({
  registerBlock,
}: {
  registerBlock: (block: () => void) => void;
}) {
  const [blocked, setBlocked] = useState(false);
  const projects = useProjects();
  registerBlock(() => setBlocked(true));
  return (
    <>
      <button type="button" onClick={() => void projects.actions.loadProjects()}>
        Reload project catalog
      </button>
      <output aria-label="Project catalog">
        {projects.state.projects.map((item) => item.name).join(", ") || "None"}
      </output>
      <ProjectScopePanel
        noProjectDetail="Unprojected"
        emptyHint="No projects"
        canChangeProjectScope={() => !blocked}
        beginProjectDelete={() => 1}
        finishProjectDelete={() => undefined}
        deleteMessage={(item) => <>Delete {item.name}?</>}
      />
    </>
  );
}

describe("ProjectScopePanel catalog recovery", () => {
  beforeEach(() => {
    vi.mocked(createProject).mockReset();
    vi.mocked(getProjects).mockReset();
    vi.mocked(updateProject).mockReset();
  });

  it("keeps operation feedback visible and offers an accessible catalog retry", async () => {
    let resolveRetry!: (value: { object: "projects"; data: [] }) => void;
    vi.mocked(getProjects).mockReturnValue(
      new Promise<{ object: "projects"; data: [] }>((resolve) => {
        resolveRetry = resolve;
      }),
    );
    const user = userEvent.setup();
    renderPanel({
      projects: [],
      loaded: false,
      catalogError: "raw catalog failure",
      error: "Project rename failed.",
    });

    expect(screen.getByText("Project rename failed.")).toBeTruthy();
    expect(screen.getByText("Projects could not be loaded.")).toBeTruthy();
    expect(screen.queryByText("raw catalog failure")).toBeNull();

    const retryButton = screen.getByRole("button", { name: "Retry" });
    retryButton.focus();
    await user.click(retryButton);
    const retryingButton = screen.getByRole("button", { name: "Retrying…" });
    expect(retryingButton).toBe(retryButton);
    expect(retryingButton).toHaveAttribute("aria-disabled", "true");
    expect(retryingButton).toHaveFocus();

    await act(async () => {
      resolveRetry({ object: "projects", data: [] });
    });

    expect(await screen.findByText("Projects loaded.")).toBeTruthy();
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: /Retry/ })).toBeNull();
      expect(screen.getByRole("button", { name: "Expand projects" })).toHaveFocus();
    });
    expect(screen.getByText("Project rename failed.")).toBeTruthy();
  });

  it("preserves catalog recovery when scope ownership changes during retry", async () => {
    let resolveRetry!: (value: { object: "projects"; data: ProjectRecord[] }) => void;
    vi.mocked(getProjects).mockReturnValue(
      new Promise<{ object: "projects"; data: ProjectRecord[] }>((resolve) => {
        resolveRetry = resolve;
      }),
    );
    let blockScopeChanges: () => void = () => undefined;
    const user = userEvent.setup();
    render(<CatalogOwnershipHarness registerBlock={(block) => (blockScopeChanges = block)} />, {
      wrapper: wrapper({
        projects: [project],
        loaded: true,
        catalogError: "Projects are unavailable.",
      }),
    });

    const retryButton = screen.getByRole("button", { name: "Retry" });
    retryButton.focus();
    await user.click(retryButton);
    await waitFor(() => expect(getProjects).toHaveBeenCalledTimes(1));
    act(blockScopeChanges);
    await act(async () => {
      resolveRetry({ object: "projects", data: [] });
    });

    await waitFor(() => expect(retryButton).toHaveAccessibleName("Retry"));
    expect(retryButton).toHaveFocus();
    expect(screen.getByLabelText("Project catalog")).toHaveTextContent(project.name);
    expect(screen.getByText("Projects could not be loaded.")).toBeTruthy();
    expect(screen.queryByText("Projects loaded.")).toBeNull();

    await user.click(retryButton);
    expect(getProjects).toHaveBeenCalledTimes(1);
  });

  it("clears a prior success announcement when the catalog later fails", async () => {
    vi.mocked(getProjects)
      .mockResolvedValueOnce({ object: "projects", data: [project] })
      .mockRejectedValueOnce(new Error("catalog unavailable again"));
    const user = userEvent.setup();
    render(<CatalogOwnershipHarness registerBlock={() => undefined} />, {
      wrapper: wrapper({
        projects: [project],
        loaded: true,
        catalogError: "Projects are unavailable.",
      }),
    });

    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("Projects loaded.")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Reload project catalog" }));
    expect(await screen.findByText("Projects could not be loaded.")).toBeTruthy();
    await waitFor(() => expect(screen.queryByText("Projects loaded.")).toBeNull());
  });

  it("does not join an externally owned foreground catalog recovery", async () => {
    let resolveRecovery!: (value: { object: "projects"; data: ProjectRecord[] }) => void;
    vi.mocked(getProjects).mockReturnValue(
      new Promise<{ object: "projects"; data: ProjectRecord[] }>((resolve) => {
        resolveRecovery = resolve;
      }),
    );
    const user = userEvent.setup();
    render(<CatalogOwnershipHarness registerBlock={() => undefined} />, {
      wrapper: wrapper({
        projects: [project],
        loaded: true,
        catalogError: "Projects are unavailable.",
      }),
    });

    await user.click(screen.getByRole("button", { name: "Reload project catalog" }));
    const retryingButton = await screen.findByRole("button", { name: "Retrying…" });
    expect(retryingButton).toHaveAttribute("aria-disabled", "true");
    await user.click(retryingButton);
    expect(retryingButton).toHaveFocus();
    expect(getProjects).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveRecovery({ object: "projects", data: [project] });
    });
    await waitFor(() => expect(screen.queryByRole("button", { name: /Retry/ })).toBeNull());
    expect(screen.getByRole("button", { name: "Expand projects" })).toHaveFocus();
  });

  it("lets only the latest deferred scope callback commit project selection", async () => {
    const projectA = { ...project, id: "project_a", name: "Project A" };
    const projectB = { ...project, id: "project_b", name: "Project B" };
    let resolveA: ((accepted: boolean) => void) | undefined;
    let resolveB: ((accepted: boolean) => void) | undefined;
    const onProjectSelected = vi.fn(
      (projectID: string) =>
        new Promise<boolean>((resolve) => {
          if (projectID === projectA.id) resolveA = resolve;
          if (projectID === projectB.id) resolveB = resolve;
        }),
    );
    vi.mocked(updateProject).mockImplementation(async (projectID) => ({
      object: "project",
      data: projectID === projectA.id ? projectA : projectB,
    }));
    const user = userEvent.setup();
    render(
      <ProjectsProvider initialState={{ projects: [projectA, projectB], loaded: true }}>
        <SelectionSequenceHarness onProjectSelected={onProjectSelected} />
      </ProjectsProvider>,
    );

    await user.click(screen.getByRole("button", { name: "Expand projects" }));
    await user.click(screen.getByRole("button", { name: "Project Project A" }));
    await user.click(screen.getByRole("button", { name: "Project Project B" }));
    expect(screen.getByLabelText("Active project sequence")).toHaveTextContent("None");

    await act(async () => resolveB?.(true));
    await waitFor(() =>
      expect(screen.getByLabelText("Active project sequence")).toHaveTextContent(projectB.id),
    );
    await act(async () => resolveA?.(true));

    expect(screen.getByLabelText("Active project sequence")).toHaveTextContent(projectB.id);
    expect(updateProject).toHaveBeenCalledTimes(1);
    expect(updateProject).toHaveBeenCalledWith(projectB.id, expect.any(Object));
  });

  it("lets the create dialog exclusively announce its operation failure", async () => {
    vi.mocked(createProject).mockRejectedValue(new Error("create failed"));
    const user = userEvent.setup();
    renderPanel({
      projects: [],
      loaded: false,
      catalogError: "catalog failed",
    });

    await user.click(screen.getByRole("button", { name: "Add project" }));
    await user.type(screen.getByPlaceholderText("Project name"), "Keep this draft");
    await user.click(screen.getByRole("button", { name: "Create project" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("create failed");
    expect(screen.getAllByText("create failed")).toHaveLength(1);
    expect(screen.getByText("Projects could not be loaded.")).toBeTruthy();
  });

  it("submits create once and keeps its pending result in the dialog", async () => {
    let rejectCreate!: (reason: Error) => void;
    vi.mocked(createProject).mockReturnValue(
      new Promise<{ object: "project"; data: ProjectRecord }>((_resolve, reject) => {
        rejectCreate = reject;
      }),
    );
    const user = userEvent.setup();
    renderPanel({ projects: [], loaded: true });

    await user.click(screen.getByRole("button", { name: "Add project" }));
    const dialog = screen.getByRole("dialog", { name: "Create project" });
    await user.type(within(dialog).getByLabelText("Name"), "Keep this draft");
    const form = within(dialog).getByLabelText("Name").closest("form") as HTMLFormElement;
    await act(async () => {
      form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });

    expect(createProject).toHaveBeenCalledTimes(1);
    expect(within(dialog).getByRole("button", { name: "Close" })).toBeDisabled();
    expect(within(dialog).getByRole("button", { name: "Creating..." })).toHaveFocus();
    await user.keyboard("{Escape}");
    fireEvent.click(dialog.parentElement as HTMLElement);
    expect(screen.getByRole("dialog", { name: "Create project" })).toBe(dialog);

    await act(async () => {
      rejectCreate(new Error("create failed"));
    });

    expect(await within(dialog).findByRole("alert")).toHaveTextContent("create failed");
    expect(within(dialog).getByRole("button", { name: "Close" })).toBeEnabled();
    expect(within(dialog).getByRole("button", { name: "Create project" })).toHaveFocus();
  });

  it("keeps a stale success in the catalog without selecting over a newer surface", async () => {
    const oldProject = {
      id: "proj_old",
      name: "Old request",
      roots: [],
      created_at: "2026-07-13T10:00:00Z",
      updated_at: "2026-07-13T10:00:00Z",
    } satisfies ProjectRecord;
    let resolveOldCreate!: (value: { object: "project"; data: ProjectRecord }) => void;
    let rejectCurrentCreate!: (reason: Error) => void;
    vi.mocked(createProject)
      .mockReturnValueOnce(
        new Promise<{ object: "project"; data: ProjectRecord }>((resolve) => {
          resolveOldCreate = resolve;
        }),
      )
      .mockReturnValueOnce(
        new Promise<{ object: "project"; data: ProjectRecord }>((_resolve, reject) => {
          rejectCurrentCreate = reject;
        }),
      );
    const onOldProjectSelected = vi.fn();
    const onCurrentProjectSelected = vi.fn();
    const user = userEvent.setup();
    render(
      <ProjectsProvider initialState={{ projects: [], loaded: true }}>
        <CrossSurfaceHarness
          onCurrentProjectSelected={onCurrentProjectSelected}
          onOldProjectSelected={onOldProjectSelected}
        />
      </ProjectsProvider>,
    );

    await user.click(screen.getByRole("button", { name: "Add project" }));
    await user.type(screen.getByLabelText("Name"), "Old request");
    const oldForm = screen.getByLabelText("Name").closest("form") as HTMLFormElement;
    await act(async () => {
      oldForm.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      screen.getByRole("button", { name: "Switch surface" }).click();
    });
    await user.click(screen.getByRole("button", { name: "Add project" }));
    await user.type(screen.getByLabelText("Name"), "Current request");
    const currentForm = screen.getByLabelText("Name").closest("form") as HTMLFormElement;
    await act(async () => {
      currentForm.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });

    await act(async () => {
      resolveOldCreate({ object: "project", data: oldProject });
    });

    expect(screen.getByLabelText("Project catalog")).toHaveTextContent("Old request");
    expect(screen.getByLabelText("Active project")).toHaveTextContent("None");
    expect(onOldProjectSelected).not.toHaveBeenCalled();
    expect(onCurrentProjectSelected).not.toHaveBeenCalled();
    const currentDialog = screen.getByRole("dialog", { name: "Create project" });
    expect(within(currentDialog).queryByRole("alert")).toBeNull();
    expect(within(currentDialog).getByRole("button", { name: "Creating..." })).toHaveFocus();

    await act(async () => {
      rejectCurrentCreate(new Error("current create failed"));
    });
    expect(await within(currentDialog).findByRole("alert")).toHaveTextContent(
      "current create failed",
    );
  });

  it("ignores a stale failure while the newer surface completes its own create", async () => {
    const currentProject = {
      id: "proj_current",
      name: "Current request",
      roots: [],
      created_at: "2026-07-13T10:00:00Z",
      updated_at: "2026-07-13T10:00:00Z",
    } satisfies ProjectRecord;
    let rejectOldCreate!: (reason: Error) => void;
    let resolveCurrentCreate!: (value: { object: "project"; data: ProjectRecord }) => void;
    vi.mocked(createProject)
      .mockReturnValueOnce(
        new Promise<{ object: "project"; data: ProjectRecord }>((_resolve, reject) => {
          rejectOldCreate = reject;
        }),
      )
      .mockReturnValueOnce(
        new Promise<{ object: "project"; data: ProjectRecord }>((resolve) => {
          resolveCurrentCreate = resolve;
        }),
      );
    vi.mocked(updateProject).mockResolvedValue({ object: "project", data: currentProject });
    const onOldProjectSelected = vi.fn();
    const onCurrentProjectSelected = vi.fn();
    const user = userEvent.setup();
    render(
      <ProjectsProvider initialState={{ projects: [], loaded: true }}>
        <CrossSurfaceHarness
          onCurrentProjectSelected={onCurrentProjectSelected}
          onOldProjectSelected={onOldProjectSelected}
        />
      </ProjectsProvider>,
    );

    await user.click(screen.getByRole("button", { name: "Add project" }));
    await user.type(screen.getByLabelText("Name"), "Old request");
    const oldForm = screen.getByLabelText("Name").closest("form") as HTMLFormElement;
    await act(async () => {
      oldForm.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      screen.getByRole("button", { name: "Switch surface" }).click();
    });
    await user.click(screen.getByRole("button", { name: "Add project" }));
    await user.type(screen.getByLabelText("Name"), "Current request");
    const currentForm = screen.getByLabelText("Name").closest("form") as HTMLFormElement;
    await act(async () => {
      currentForm.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });

    await act(async () => {
      rejectOldCreate(new Error("stale create failed"));
    });
    const currentDialog = screen.getByRole("dialog", { name: "Create project" });
    expect(within(currentDialog).queryByRole("alert")).toBeNull();
    expect(within(currentDialog).getByRole("button", { name: "Creating..." })).toHaveFocus();

    await act(async () => {
      resolveCurrentCreate({ object: "project", data: currentProject });
    });

    await waitFor(() => {
      expect(screen.queryByRole("dialog", { name: "Create project" })).toBeNull();
    });
    expect(screen.getByLabelText("Project catalog")).toHaveTextContent("Current request");
    expect(screen.getByLabelText("Active project")).toHaveTextContent(currentProject.id);
    expect(onOldProjectSelected).not.toHaveBeenCalled();
    expect(onCurrentProjectSelected).toHaveBeenCalledWith(currentProject.id, currentProject);
  });

  it("keeps a delayed selection failure outside an open create dialog", async () => {
    const project: ProjectRecord = {
      id: "proj_1",
      name: "Hecate",
      roots: [],
      created_at: "2026-07-13T10:00:00Z",
      updated_at: "2026-07-13T10:00:00Z",
    };
    let rejectSelection!: (reason: Error) => void;
    vi.mocked(updateProject).mockReturnValue(
      new Promise<{ object: "project"; data: ProjectRecord }>((_resolve, reject) => {
        rejectSelection = reject;
      }),
    );
    const user = userEvent.setup();
    renderPanel({ projects: [project], loaded: true });

    await user.click(screen.getByRole("button", { name: "Expand projects" }));
    await user.click(screen.getByRole("button", { name: "Project Hecate" }));
    await user.click(screen.getByRole("button", { name: "Add project" }));
    await act(async () => {
      rejectSelection(new Error("select failed"));
    });

    const dialog = screen.getByRole("dialog", { name: "Create project" });
    expect(screen.getAllByText("select failed")).toHaveLength(1);
    expect(within(dialog).queryByRole("alert")).toBeNull();
    expect(within(dialog).queryByText("select failed")).toBeNull();
  });
});

describe("ProjectScopePanel destructive chat ownership", () => {
  it("does not open project creation while an image draft owns the chat scope", async () => {
    const user = userEvent.setup();
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    const actions = createRuntimeConsoleActions();
    render(withRuntimeConsole(<ReservedProjectScopePanel />, { state, actions }));

    await user.click(screen.getByRole("button", { name: "Attach scope image" }));
    expect(screen.getByTestId("scope-draft-count")).toHaveTextContent("1");

    await user.click(screen.getByRole("button", { name: "Add project" }));

    expect(screen.queryByRole("dialog", { name: "Create project" })).toBeNull();
  });

  it("does not select a created project when image ownership changes during creation", async () => {
    const createdProject = {
      id: "project_created_late",
      name: "Created while attaching",
      roots: [],
      created_at: "2026-07-13T11:00:00Z",
      updated_at: "2026-07-13T11:00:00Z",
    } satisfies ProjectRecord;
    let finishCreate: ((value: ProjectRecord | null) => void) | undefined;
    const createProject = vi.fn(
      () =>
        new Promise<ProjectRecord | null>((resolve) => {
          finishCreate = resolve;
        }),
    );
    const selectProject = vi.fn(async () => undefined);
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    const actions = { ...createRuntimeConsoleActions(), createProject, selectProject };
    const user = userEvent.setup();
    render(withRuntimeConsole(<ReservedProjectScopePanel />, { state, actions }));

    await user.click(screen.getByRole("button", { name: "Add project" }));
    await user.type(screen.getByLabelText("Name"), createdProject.name);
    await user.click(screen.getByRole("button", { name: "Create project" }));
    await waitFor(() => expect(createProject).toHaveBeenCalledTimes(1));

    await user.click(screen.getByRole("button", { name: "Attach scope image" }));
    expect(screen.getByTestId("scope-draft-count")).toHaveTextContent("1");

    await act(async () => {
      finishCreate?.(createdProject);
    });

    await waitFor(() => {
      expect(screen.queryByRole("dialog", { name: "Create project" })).toBeNull();
    });
    expect(selectProject).not.toHaveBeenCalledWith(createdProject.id);
    expect(screen.getByTestId("scope-draft-count")).toHaveTextContent("1");
  });

  it("holds the shared reservation through a deferred project delete", async () => {
    const user = userEvent.setup();
    let finishDelete: ((value: ProjectDeleteRecord | null) => void) | undefined;
    const deleteProject = vi.fn(
      () =>
        new Promise<ProjectDeleteRecord | null>((resolve) => {
          finishDelete = resolve;
        }),
    );
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
    });
    const actions = { ...createRuntimeConsoleActions(), deleteProject };
    render(withRuntimeConsole(<ReservedProjectScopePanel />, { state, actions }));

    await user.click(screen.getByRole("button", { name: "Expand projects" }));
    await user.click(screen.getByRole("button", { name: "Project Scope project" }));
    await user.click(screen.getByRole("button", { name: "Delete project Scope project" }));
    await user.click(screen.getByRole("button", { name: "Delete project" }));
    await waitFor(() => expect(deleteProject).toHaveBeenCalledTimes(1));

    const dialog = screen.getByRole("dialog", { name: "Delete project" });
    const pendingButton = screen.getByRole("button", { name: "Working…" });
    expect(pendingButton).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    await user.keyboard("{Escape}");
    fireEvent.click(dialog.parentElement as HTMLElement);
    fireEvent.click(pendingButton);
    expect(deleteProject).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("dialog", { name: "Delete project" })).toBe(dialog);

    await user.click(screen.getByRole("button", { name: "Attach scope image" }));
    await user.click(screen.getByRole("button", { name: "Try scope image submission" }));
    expect(screen.getByTestId("scope-draft-count")).toHaveTextContent("0");
    expect(screen.getByTestId("scope-turn-attempt")).toHaveTextContent("blocked");

    dialog.focus();
    expect(dialog).toHaveFocus();
    await act(async () => {
      fireEvent.click(
        screen.getByRole("button", { name: "Remove deleted scope project from state" }),
      );
      finishDelete?.({
        project_id: project.id,
        project_name: project.name,
        chat_sessions_deleted: 0,
        project_work_rows_deleted: 0,
        project_skills_deleted: 0,
        memory_entries_deleted: 0,
        memory_candidates_deleted: 0,
      });
    });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Delete project" })).toBeNull(),
    );
    expect(screen.getByRole("button", { name: "Collapse projects" })).toHaveFocus();

    await user.click(screen.getByRole("button", { name: "Attach scope image" }));
    expect(screen.getByTestId("scope-draft-count")).toHaveTextContent("1");
  });
});

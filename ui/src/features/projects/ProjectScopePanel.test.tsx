import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ProjectsProvider, useProjects } from "../../app/state/projects";
import { createProject, getProjects, updateProject } from "../../lib/api";
import type { ProjectRecord } from "../../types/project";
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
      deleteMessage={(project) => `Delete ${project.name}?`}
      emptyHint="No projects yet."
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
        key={surface}
        deleteMessage={(project) => `Delete ${project.name}?`}
        emptyHint="No projects yet."
        noProjectDetail="No project selected."
        onProjectSelected={surface === "old" ? onOldProjectSelected : onCurrentProjectSelected}
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
    fireEvent.submit(form);
    fireEvent.submit(form);

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
    fireEvent.submit(screen.getByLabelText("Name").closest("form") as HTMLFormElement);
    fireEvent.click(screen.getByRole("button", { name: "Switch surface" }));
    await user.click(screen.getByRole("button", { name: "Add project" }));
    await user.type(screen.getByLabelText("Name"), "Current request");
    fireEvent.submit(screen.getByLabelText("Name").closest("form") as HTMLFormElement);

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
    fireEvent.submit(screen.getByLabelText("Name").closest("form") as HTMLFormElement);
    fireEvent.click(screen.getByRole("button", { name: "Switch surface" }));
    await user.click(screen.getByRole("button", { name: "Add project" }));
    await user.type(screen.getByLabelText("Name"), "Current request");
    fireEvent.submit(screen.getByLabelText("Name").closest("form") as HTMLFormElement);

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

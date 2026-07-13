import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ProjectsProvider } from "../../app/state/projects";
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

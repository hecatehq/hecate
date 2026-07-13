import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ProjectRecord } from "../../types/project";
import {
  useProjectSelectionController,
  useStoredRightPanelWidth,
} from "./useProjectViewController";

function project(id: string, name = id): ProjectRecord {
  return {
    id,
    name,
    roots: [],
    created_at: "2026-06-12T00:00:00Z",
    updated_at: "2026-06-12T00:00:00Z",
  };
}

describe("useStoredRightPanelWidth", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("uses the default width when storage is empty or invalid", () => {
    localStorage.setItem("hecate.chat.rightPanelWidth", "not-a-number");

    const { result } = renderHook(() => useStoredRightPanelWidth());

    expect(result.current.rightPanelWidth).toBe(380);
  });

  it("persists width updates", () => {
    const { result } = renderHook(() => useStoredRightPanelWidth());

    act(() => {
      result.current.setRightPanelWidth(520);
    });

    expect(result.current.rightPanelWidth).toBe(520);
    expect(localStorage.getItem("hecate.chat.rightPanelWidth")).toBe("520");
  });
});

describe("useProjectSelectionController", () => {
  it("selects the active project and opens a new project through the host action", () => {
    const selectProject = vi.fn();
    const onProjectChange = vi.fn();
    const projects = [project("proj_a"), project("proj_b")];
    const { result } = renderHook(() =>
      useProjectSelectionController({
        activeProjectID: "proj_b",
        onProjectChange,
        projects,
        selectProject,
      }),
    );

    expect(result.current.selectedProjectID).toBe("proj_b");
    expect(result.current.selectedProject?.id).toBe("proj_b");

    act(() => {
      result.current.openProject("proj_a");
    });

    expect(result.current.selectedProjectID).toBe("proj_a");
    expect(onProjectChange).toHaveBeenCalledTimes(1);
    expect(selectProject).toHaveBeenCalledWith("proj_a");

    act(() => result.current.clearSelectedProject("proj_b"));
    expect(result.current.selectedProjectID).toBe("proj_a");
    expect(onProjectChange).toHaveBeenCalledTimes(1);
  });

  it("keeps a valid current selection when there is no active project", () => {
    const selectProject = vi.fn();
    const onProjectChange = vi.fn();
    const projects = [project("proj_a"), project("proj_b")];
    const { result, rerender } = renderHook(
      ({ activeProjectID }: { activeProjectID: string }) =>
        useProjectSelectionController({
          activeProjectID,
          onProjectChange,
          projects,
          selectProject,
        }),
      { initialProps: { activeProjectID: "proj_a" } },
    );

    expect(result.current.selectedProjectID).toBe("proj_a");

    rerender({ activeProjectID: "proj_b" });
    expect(result.current.selectedProjectID).toBe("proj_b");
    expect(onProjectChange).toHaveBeenCalledTimes(1);

    rerender({ activeProjectID: "" });

    expect(result.current.selectedProjectID).toBe("proj_b");
    expect(onProjectChange).toHaveBeenCalledTimes(1);
  });

  it("clears the selection when the project list becomes empty", () => {
    const selectProject = vi.fn();
    const onProjectChange = vi.fn();
    const { result, rerender } = renderHook(
      ({ projects }: { projects: ProjectRecord[] }) =>
        useProjectSelectionController({
          activeProjectID: "proj_a",
          onProjectChange,
          projects,
          selectProject,
        }),
      { initialProps: { projects: [project("proj_a")] } },
    );

    expect(result.current.selectedProjectID).toBe("proj_a");

    rerender({ projects: [] });

    expect(result.current.selectedProjectID).toBe("");
    expect(onProjectChange).toHaveBeenCalledTimes(1);
  });

  it("resets project-scoped interactions when clearing or replacing a selection", () => {
    const selectProject = vi.fn();
    const onProjectChange = vi.fn();
    const { result, rerender } = renderHook(
      ({ projects }: { projects: ProjectRecord[] }) =>
        useProjectSelectionController({
          activeProjectID: "",
          onProjectChange,
          projects,
          selectProject,
        }),
      { initialProps: { projects: [project("proj_a"), project("proj_b")] } },
    );

    expect(result.current.selectedProjectID).toBe("proj_a");
    act(() => result.current.clearSelectedProject());
    expect(result.current.selectedProjectID).toBe("");
    expect(onProjectChange).toHaveBeenCalledTimes(2);

    rerender({ projects: [project("proj_b")] });
    expect(result.current.selectedProjectID).toBe("proj_b");
    expect(onProjectChange).toHaveBeenCalledTimes(3);
  });
});

import { useCallback, useEffect, useMemo, useState } from "react";

import type { ProjectRecord } from "../../types/project";

const RIGHT_PANEL_WIDTH_KEY = "hecate.chat.rightPanelWidth";
const DEFAULT_RIGHT_PANEL_WIDTH = 380;

export function useStoredRightPanelWidth() {
  const [rightPanelWidth, setRightPanelWidthState] = useState(readStoredRightPanelWidth);
  const setRightPanelWidth = useCallback((width: number) => {
    setRightPanelWidthState(width);
    rememberRightPanelWidth(width);
  }, []);
  return { rightPanelWidth, setRightPanelWidth };
}

export type ProjectSelectionControllerArgs = {
  activeProjectID: string;
  onProjectChange?: () => void;
  projects: ProjectRecord[];
  selectProject: (projectID: string) => Promise<void> | void;
};

export function useProjectSelectionController({
  activeProjectID,
  onProjectChange,
  projects,
  selectProject,
}: ProjectSelectionControllerArgs) {
  const [selectedProjectID, setSelectedProjectID] = useState(activeProjectID);
  const selectedProject = useMemo(
    () => projects.find((project) => project.id === selectedProjectID) ?? null,
    [projects, selectedProjectID],
  );

  useEffect(() => {
    if (projects.length === 0) {
      setSelectedProjectID("");
      return;
    }
    if (activeProjectID) {
      setSelectedProjectID(activeProjectID);
      return;
    }
    setSelectedProjectID((current) =>
      current && projects.some((project) => project.id === current)
        ? current
        : projects[0]?.id || "",
    );
  }, [activeProjectID, projects]);

  const openProject = useCallback(
    (projectID: string) => {
      if (projectID !== selectedProjectID) {
        onProjectChange?.();
      }
      setSelectedProjectID(projectID);
      void selectProject(projectID);
    },
    [onProjectChange, selectProject, selectedProjectID],
  );

  const clearSelectedProject = useCallback(() => {
    setSelectedProjectID("");
  }, []);

  return {
    clearSelectedProject,
    openProject,
    selectedProject,
    selectedProjectID,
  };
}

function readStoredRightPanelWidth(): number {
  try {
    const value = Number.parseInt(localStorage.getItem(RIGHT_PANEL_WIDTH_KEY) ?? "", 10);
    return Number.isFinite(value) && value > 0 ? value : DEFAULT_RIGHT_PANEL_WIDTH;
  } catch {
    return DEFAULT_RIGHT_PANEL_WIDTH;
  }
}

function rememberRightPanelWidth(width: number) {
  try {
    localStorage.setItem(RIGHT_PANEL_WIDTH_KEY, String(width));
  } catch {
    // Best-effort preference only.
  }
}

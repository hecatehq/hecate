import { useCallback, useEffect, useMemo, useRef, useState } from "react";

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
  const selectedProjectIDRef = useRef(activeProjectID);
  const lastActiveProjectIDRef = useRef(activeProjectID);
  const pendingActiveProjectIDRef = useRef(activeProjectID);
  const onProjectChangeRef = useRef(onProjectChange);
  onProjectChangeRef.current = onProjectChange;
  const selectedProject = useMemo(
    () => projects.find((project) => project.id === selectedProjectID) ?? null,
    [projects, selectedProjectID],
  );

  useEffect(() => {
    const currentProjectID = selectedProjectIDRef.current;
    const activeProjectChanged = activeProjectID !== lastActiveProjectIDRef.current;
    if (activeProjectChanged) {
      lastActiveProjectIDRef.current = activeProjectID;
      pendingActiveProjectIDRef.current = activeProjectID;
    }
    const hasProject = (projectID: string) =>
      Boolean(projectID && projects.some((project) => project.id === projectID));
    const pendingActiveProjectID = pendingActiveProjectIDRef.current;
    let nextProjectID = currentProjectID;
    if (projects.length === 0) {
      nextProjectID = "";
    } else if (pendingActiveProjectID && hasProject(pendingActiveProjectID)) {
      nextProjectID = pendingActiveProjectID;
      pendingActiveProjectIDRef.current = "";
    } else if (!hasProject(currentProjectID)) {
      nextProjectID = hasProject(activeProjectID) ? activeProjectID : projects[0]?.id || "";
    }
    if (nextProjectID === currentProjectID) return;
    onProjectChangeRef.current?.();
    selectedProjectIDRef.current = nextProjectID;
    setSelectedProjectID(nextProjectID);
  }, [activeProjectID, projects]);

  const openProject = useCallback(
    (projectID: string) => {
      if (projectID !== selectedProjectIDRef.current) {
        onProjectChangeRef.current?.();
        selectedProjectIDRef.current = projectID;
        setSelectedProjectID(projectID);
      }
      void selectProject(projectID);
    },
    [selectProject],
  );

  const clearSelectedProject = useCallback((expectedProjectID?: string) => {
    if (expectedProjectID && selectedProjectIDRef.current !== expectedProjectID) return;
    if (!selectedProjectIDRef.current) return;
    onProjectChangeRef.current?.();
    selectedProjectIDRef.current = "";
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

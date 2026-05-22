// projects slice: optional project context for the console shell.
// Projects group future work, defaults, and roots, but "No project"
// remains a first-class state. Chats and tasks must keep working
// without a selected project.

import { createContext, useCallback, useContext, useMemo, useReducer, type ReactNode } from "react";

import { applyOverride, CoordinatorOverridesContext } from "./coordinators/overrides";
import {
  chooseWorkspaceDirectory,
  createProject as createProjectRequest,
  deleteProject as deleteProjectRequest,
  getProjects as getProjectsRequest,
  updateProject as updateProjectRequest,
} from "../../lib/api";
import { parseStoredString, usePersistedState } from "../../lib/persistedState";
import type { ProjectRecord } from "../../types/project";

const ACTIVE_PROJECT_STORAGE_KEY = "hecate.project";

export type ProjectsState = {
  projects: ProjectRecord[];
  loading: boolean;
  error: string;
};

type SetStateAction<T> = T | ((prev: T) => T);

export type ProjectsActions = {
  setProjects: (next: SetStateAction<ProjectRecord[]>) => void;
  setLoading: (value: boolean) => void;
  setError: (value: string) => void;
  setActiveProjectID: (id: string) => void;
  loadProjects: () => Promise<void>;
  selectProject: (id: string) => Promise<void>;
  createProjectFromFolder: () => Promise<ProjectRecord | null>;
  renameProject: (id: string, name: string) => Promise<void>;
  deleteProject: (id: string) => Promise<boolean>;
};

type ProjectsContextValue = {
  state: ProjectsState;
  activeProjectID: string;
  activeProject: ProjectRecord | null;
  actions: ProjectsActions;
};

type Action =
  | { type: "projects/set"; next: SetStateAction<ProjectRecord[]> }
  | { type: "loading/set"; value: boolean }
  | { type: "error/set"; value: string };

const initialState: ProjectsState = {
  projects: [],
  loading: false,
  error: "",
};

function resolve<T>(prev: T, next: SetStateAction<T>): T {
  return typeof next === "function" ? (next as (prev: T) => T)(prev) : next;
}

function reducer(state: ProjectsState, action: Action): ProjectsState {
  switch (action.type) {
    case "projects/set":
      return { ...state, projects: resolve(state.projects, action.next) };
    case "loading/set":
      return state.loading === action.value ? state : { ...state, loading: action.value };
    case "error/set":
      return state.error === action.value ? state : { ...state, error: action.value };
    default:
      return state;
  }
}

const ProjectsContext = createContext<ProjectsContextValue | null>(null);

export function ProjectsProvider({
  children,
  initialState: seededState,
}: {
  children: ReactNode;
  initialState?: Partial<ProjectsState>;
}) {
  const [state, dispatch] = useReducer(
    reducer,
    seededState ? { ...initialState, ...seededState } : initialState,
  );
  const [activeProjectID, setActiveProjectIDState] = usePersistedState<string>(
    ACTIVE_PROJECT_STORAGE_KEY,
    parseStoredString,
    "",
    { shouldRemove: (value) => value === "" },
  );

  const activeProject = useMemo(
    () => state.projects.find((project) => project.id === activeProjectID) ?? null,
    [activeProjectID, state.projects],
  );

  const setProjects = useCallback(
    (next: SetStateAction<ProjectRecord[]>) => dispatch({ type: "projects/set", next }),
    [],
  );
  const setLoading = useCallback((value: boolean) => dispatch({ type: "loading/set", value }), []);
  const setError = useCallback((value: string) => dispatch({ type: "error/set", value }), []);
  const setActiveProjectID = useCallback(
    (id: string) => setActiveProjectIDState(id.trim()),
    [setActiveProjectIDState],
  );

  const loadProjects = useCallback(async () => {
    dispatch({ type: "loading/set", value: true });
    try {
      const payload = await getProjectsRequest();
      const items = payload.data ?? [];
      dispatch({ type: "projects/set", next: items });
      dispatch({ type: "error/set", value: "" });
      if (activeProjectID && !items.some((item) => item.id === activeProjectID)) {
        setActiveProjectIDState("");
      }
    } catch (error) {
      dispatch({
        type: "error/set",
        value: error instanceof Error ? error.message : "Failed to load projects.",
      });
    } finally {
      dispatch({ type: "loading/set", value: false });
    }
  }, [activeProjectID, setActiveProjectIDState]);

  const selectProject = useCallback(
    async (id: string) => {
      const nextID = id.trim();
      setActiveProjectIDState(nextID);
      dispatch({ type: "error/set", value: "" });
      if (!nextID) return;
      try {
        const payload = await updateProjectRequest(nextID, {
          last_opened_at: new Date().toISOString(),
        });
        dispatch({ type: "projects/set", next: (current) => upsertProject(current, payload.data) });
      } catch (error) {
        dispatch({
          type: "error/set",
          value: error instanceof Error ? error.message : "Failed to update project.",
        });
      }
    },
    [setActiveProjectIDState],
  );

  const createProjectFromFolder = useCallback(async (): Promise<ProjectRecord | null> => {
    dispatch({ type: "error/set", value: "" });
    try {
      const workspace = await chooseWorkspaceDirectory();
      const root = workspace.data.path.trim();
      if (!root) return null;
      const payload = await createProjectRequest({
        name: projectNameFromPath(root),
        roots: [
          {
            path: root,
            kind: "local",
            git_branch: workspace.data.branch || undefined,
            active: true,
          },
        ],
      });
      dispatch({ type: "projects/set", next: (current) => upsertProject(current, payload.data) });
      setActiveProjectIDState(payload.data.id);
      return payload.data;
    } catch (error) {
      dispatch({
        type: "error/set",
        value: error instanceof Error ? error.message : "Failed to create project.",
      });
      return null;
    }
  }, [setActiveProjectIDState]);

  const renameProject = useCallback(async (id: string, name: string) => {
    const projectID = id.trim();
    const nextName = name.trim();
    if (!projectID || !nextName) return;
    dispatch({ type: "error/set", value: "" });
    try {
      const payload = await updateProjectRequest(projectID, { name: nextName });
      dispatch({ type: "projects/set", next: (current) => upsertProject(current, payload.data) });
    } catch (error) {
      dispatch({
        type: "error/set",
        value: error instanceof Error ? error.message : "Failed to rename project.",
      });
    }
  }, []);

  const deleteProject = useCallback(
    async (id: string): Promise<boolean> => {
      const projectID = id.trim();
      if (!projectID) return false;
      dispatch({ type: "error/set", value: "" });
      try {
        await deleteProjectRequest(projectID);
        dispatch({
          type: "projects/set",
          next: (current) => current.filter((project) => project.id !== projectID),
        });
        if (activeProjectID === projectID) {
          setActiveProjectIDState("");
        }
        return true;
      } catch (error) {
        dispatch({
          type: "error/set",
          value: error instanceof Error ? error.message : "Failed to delete project.",
        });
        return false;
      }
    },
    [activeProjectID, setActiveProjectIDState],
  );

  const actions = useMemo<ProjectsActions>(
    () => ({
      setProjects,
      setLoading,
      setError,
      setActiveProjectID,
      loadProjects,
      selectProject,
      createProjectFromFolder,
      renameProject,
      deleteProject,
    }),
    [
      setProjects,
      setLoading,
      setError,
      setActiveProjectID,
      loadProjects,
      selectProject,
      createProjectFromFolder,
      renameProject,
      deleteProject,
    ],
  );

  const value = useMemo<ProjectsContextValue>(
    () => ({
      state,
      activeProjectID,
      activeProject,
      actions,
    }),
    [actions, activeProject, activeProjectID, state],
  );

  return <ProjectsContext.Provider value={value}>{children}</ProjectsContext.Provider>;
}

export function useProjects(): ProjectsContextValue {
  const ctx = useContext(ProjectsContext);
  if (!ctx) {
    throw new Error("useProjects must be used inside a <ProjectsProvider>");
  }
  const overrides = useContext(CoordinatorOverridesContext);
  return {
    state: ctx.state,
    activeProjectID: ctx.activeProjectID,
    activeProject: ctx.activeProject,
    actions: applyOverride(ctx.actions, overrides?.projectsSlice),
  };
}

function upsertProject(projects: ProjectRecord[], project: ProjectRecord): ProjectRecord[] {
  const index = projects.findIndex((item) => item.id === project.id);
  if (index === -1) return [project, ...projects];
  const next = projects.slice();
  next[index] = project;
  return next;
}

function projectNameFromPath(path: string): string {
  const trimmed = path.replace(/\/+$/, "");
  const segments = trimmed.split("/").filter(Boolean);
  return segments.at(-1) || "Untitled project";
}

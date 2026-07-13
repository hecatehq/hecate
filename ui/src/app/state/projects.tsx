// projects slice: optional project context for the console shell.
// Projects group future work, defaults, and roots, but "No project"
// remains a first-class state. Chats and tasks must keep working
// without a selected project.

import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useReducer,
  useRef,
  type ReactNode,
} from "react";

import { applyOverride, CoordinatorOverridesContext } from "./coordinators/overrides";
import {
  createProject as createProjectRequest,
  deleteProject as deleteProjectRequest,
  getProjects as getProjectsRequest,
  updateProject as updateProjectRequest,
} from "../../lib/api";
import { parseStoredString, usePersistedState } from "../../lib/persistedState";
import type { CreateProjectPayload, ProjectDeleteRecord, ProjectRecord } from "../../types/project";

const ACTIVE_PROJECT_STORAGE_KEY = "hecate.project";

export type ProjectsState = {
  projects: ProjectRecord[];
  loading: boolean;
  loaded: boolean;
  catalogError: string;
  createError: string;
  error: string;
};

type SetStateAction<T> = T | ((prev: T) => T);

export type ProjectsActions = {
  setProjects: (next: SetStateAction<ProjectRecord[]>) => void;
  setLoading: (value: boolean) => void;
  setError: (value: string) => void;
  setCreateError: (value: string) => void;
  setActiveProjectID: (id: string) => void;
  loadProjects: () => Promise<void>;
  selectProject: (id: string) => Promise<void>;
  createProject: (payload: CreateProjectPayload) => Promise<ProjectRecord | null>;
  renameProject: (id: string, name: string) => Promise<void>;
  deleteProject: (id: string) => Promise<ProjectDeleteRecord | null>;
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
  | { type: "loaded/set"; value: boolean }
  | { type: "catalog-error/set"; value: string }
  | { type: "create-error/set"; value: string }
  | { type: "error/set"; value: string };

const initialState: ProjectsState = {
  projects: [],
  loading: false,
  loaded: false,
  catalogError: "",
  createError: "",
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
    case "loaded/set":
      return state.loaded === action.value ? state : { ...state, loaded: action.value };
    case "catalog-error/set":
      return state.catalogError === action.value ? state : { ...state, catalogError: action.value };
    case "create-error/set":
      return state.createError === action.value ? state : { ...state, createError: action.value };
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

  const activeProjectIDRef = useRef(activeProjectID);
  // A catalog GET may finish after a Cairnline-backed mutation. Only
  // let a snapshot captured after the latest mutation replace the list.
  const projectsMutationSequenceRef = useRef(0);
  const loadProjectsInFlightRef = useRef<Promise<void> | null>(null);

  const setProjects = useCallback((next: SetStateAction<ProjectRecord[]>) => {
    projectsMutationSequenceRef.current += 1;
    dispatch({ type: "projects/set", next });
  }, []);
  const setLoading = useCallback((value: boolean) => dispatch({ type: "loading/set", value }), []);
  const setError = useCallback((value: string) => dispatch({ type: "error/set", value }), []);
  const setCatalogError = useCallback(
    (value: string) => dispatch({ type: "catalog-error/set", value }),
    [],
  );
  const setCreateError = useCallback(
    (value: string) => dispatch({ type: "create-error/set", value }),
    [],
  );
  const setActiveProjectID = useCallback(
    (id: string) => {
      const nextID = opaqueRecordID(id);
      activeProjectIDRef.current = nextID;
      setActiveProjectIDState(nextID);
    },
    [setActiveProjectIDState],
  );

  const loadProjects = useCallback(() => {
    if (loadProjectsInFlightRef.current) return loadProjectsInFlightRef.current;
    const request = (async () => {
      setCatalogError("");
      dispatch({ type: "loading/set", value: true });
      try {
        let mutationSequence = projectsMutationSequenceRef.current;
        let payload = await getProjectsRequest();
        while (mutationSequence !== projectsMutationSequenceRef.current) {
          mutationSequence = projectsMutationSequenceRef.current;
          payload = await getProjectsRequest();
        }
        const items = payload.data ?? [];
        dispatch({ type: "projects/set", next: items });
        dispatch({ type: "loaded/set", value: true });
        setCatalogError("");
        const currentActiveProjectID = activeProjectIDRef.current;
        if (currentActiveProjectID && !items.some((item) => item.id === currentActiveProjectID)) {
          setActiveProjectID("");
        }
      } catch (error) {
        setCatalogError(error instanceof Error ? error.message : "Failed to load projects.");
      } finally {
        dispatch({ type: "loading/set", value: false });
      }
    })();
    loadProjectsInFlightRef.current = request;
    void request.finally(() => {
      if (loadProjectsInFlightRef.current === request) {
        loadProjectsInFlightRef.current = null;
      }
    });
    return request;
  }, [setActiveProjectID, setCatalogError]);

  const selectProject = useCallback(
    async (id: string) => {
      const nextID = opaqueRecordID(id);
      setActiveProjectID(nextID);
      dispatch({ type: "error/set", value: "" });
      if (!nextID) return;
      try {
        const payload = await updateProjectRequest(nextID, {
          last_opened_at: new Date().toISOString(),
        });
        setProjects((current) => upsertProject(current, payload.data));
      } catch (error) {
        dispatch({
          type: "error/set",
          value: error instanceof Error ? error.message : "Failed to update project.",
        });
      }
    },
    [setActiveProjectID, setProjects],
  );

  const createProject = useCallback(
    async (payload: CreateProjectPayload): Promise<ProjectRecord | null> => {
      const name = payload.name.trim();
      if (!name) return null;
      setCreateError("");
      try {
        const created = await createProjectRequest({
          ...payload,
          name,
          description: payload.description?.trim() || undefined,
        });
        setProjects((current) => upsertProject(current, created.data));
        setActiveProjectID(created.data.id);
        return created.data;
      } catch (error) {
        setCreateError(error instanceof Error ? error.message : "Failed to create project.");
        return null;
      }
    },
    [setActiveProjectID, setCreateError, setProjects],
  );

  const renameProject = useCallback(
    async (id: string, name: string) => {
      const projectID = opaqueRecordID(id);
      const nextName = name.trim();
      if (!projectID || !nextName) return;
      dispatch({ type: "error/set", value: "" });
      try {
        const payload = await updateProjectRequest(projectID, { name: nextName });
        setProjects((current) => upsertProject(current, payload.data));
      } catch (error) {
        dispatch({
          type: "error/set",
          value: error instanceof Error ? error.message : "Failed to rename project.",
        });
      }
    },
    [setProjects],
  );

  const deleteProject = useCallback(
    async (id: string): Promise<ProjectDeleteRecord | null> => {
      const projectID = opaqueRecordID(id);
      if (!projectID) return null;
      dispatch({ type: "error/set", value: "" });
      try {
        const payload = await deleteProjectRequest(projectID);
        setProjects((current) => current.filter((project) => project.id !== projectID));
        if (activeProjectIDRef.current === projectID) {
          setActiveProjectID("");
        }
        return payload.data;
      } catch (error) {
        dispatch({
          type: "error/set",
          value: error instanceof Error ? error.message : "Failed to delete project.",
        });
        return null;
      }
    },
    [setActiveProjectID, setProjects],
  );

  const actions = useMemo<ProjectsActions>(
    () => ({
      setProjects,
      setLoading,
      setError,
      setCreateError,
      setActiveProjectID,
      loadProjects,
      selectProject,
      createProject,
      renameProject,
      deleteProject,
    }),
    [
      setProjects,
      setLoading,
      setError,
      setCreateError,
      setActiveProjectID,
      loadProjects,
      selectProject,
      createProject,
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

function opaqueRecordID(value: string): string {
  return value.trim() ? value : "";
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

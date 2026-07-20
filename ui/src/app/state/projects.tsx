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
  error: string;
};

export type ProjectCatalogLoadOptions = {
  background?: boolean;
  /**
   * Marks every catalog snapshot already in flight as older than a
   * server-side mutation. Coalesced readers will refetch before applying.
   */
  invalidateInFlightSnapshot?: boolean;
  onError?: (message: string) => void;
  shouldApply?: () => boolean;
};

export type ProjectCatalogLoadResult =
  | { status: "applied" }
  | { status: "failed"; message: string }
  | { status: "superseded" };

type SetStateAction<T> = T | ((prev: T) => T);

export type ProjectsActions = {
  setProjects: (next: SetStateAction<ProjectRecord[]>) => void;
  setLoading: (value: boolean) => void;
  setError: (value: string) => void;
  setActiveProjectID: (id: string) => void;
  loadProjects: (options?: ProjectCatalogLoadOptions) => Promise<ProjectCatalogLoadResult>;
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
  | { type: "error/set"; value: string };

const initialState: ProjectsState = {
  projects: [],
  loading: false,
  loaded: false,
  catalogError: "",
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
  const loadProjectsInFlightRef = useRef<{
    background: boolean;
    participants: ProjectCatalogLoadOptions[];
    request: Promise<ProjectCatalogLoadResult>;
    token: object;
  } | null>(null);

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
  const setActiveProjectID = useCallback(
    (id: string) => {
      const nextID = opaqueRecordID(id);
      activeProjectIDRef.current = nextID;
      setActiveProjectIDState(nextID);
    },
    [setActiveProjectIDState],
  );

  const loadProjects = useCallback(
    function loadProjects(
      options: ProjectCatalogLoadOptions = {},
    ): Promise<ProjectCatalogLoadResult> {
      if (options.invalidateInFlightSnapshot) {
        projectsMutationSequenceRef.current += 1;
      }
      const background = Boolean(options.background);
      const inFlight = loadProjectsInFlightRef.current;
      if (inFlight) {
        if (background !== inFlight.background) {
          return inFlight.request.then(() => loadProjects(options));
        }
        inFlight.participants.push(options);
        return inFlight.request;
      }
      const token = {};
      const participants = [options];
      const request = Promise.resolve().then(async (): Promise<ProjectCatalogLoadResult> => {
        if (!background) {
          dispatch({ type: "loading/set", value: true });
        }

        let result: ProjectCatalogLoadResult = { status: "superseded" };
        let items: ProjectRecord[] = [];
        let loadFailed = false;
        let loadError: unknown;
        try {
          let mutationSequence = projectsMutationSequenceRef.current;
          let payload = await getProjectsRequest();
          while (mutationSequence !== projectsMutationSequenceRef.current) {
            mutationSequence = projectsMutationSequenceRef.current;
            payload = await getProjectsRequest();
          }
          items = projectCatalogItems(payload);
        } catch (error) {
          loadFailed = true;
          loadError = error;
        }
        try {
          let canApply = true;
          for (const participant of participants) {
            if (participant.shouldApply && !participant.shouldApply()) {
              canApply = false;
            }
          }
          if (canApply) {
            if (loadFailed) {
              result = {
                status: "failed",
                message: projectCatalogErrorMessage(loadError),
              };
            } else {
              result = { status: "applied" };
            }
          }
        } catch (error) {
          result = {
            status: "failed",
            message: projectCatalogErrorMessage(error),
          };
        } finally {
          if (loadProjectsInFlightRef.current?.token === token) {
            loadProjectsInFlightRef.current = null;
          }
        }

        if (result.status === "applied") {
          dispatch({ type: "projects/set", next: items });
          dispatch({ type: "loaded/set", value: true });
          setCatalogError("");
          const currentActiveProjectID = activeProjectIDRef.current;
          if (currentActiveProjectID && !items.some((item) => item.id === currentActiveProjectID)) {
            setActiveProjectID("");
          }
        } else if (result.status === "failed") {
          if (!background) setCatalogError(result.message);
        }

        if (!background) dispatch({ type: "loading/set", value: false });
        if (result.status === "failed") {
          for (const participant of participants) {
            try {
              participant.onError?.(result.message);
            } catch {
              // Recovery observers must not change the typed load result or
              // prevent another coalesced caller from receiving the failure.
            }
          }
        }

        return result;
      });
      loadProjectsInFlightRef.current = { background, participants, request, token };
      return request;
    },
    [setActiveProjectID, setCatalogError],
  );

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
      try {
        const created = await createProjectRequest({
          ...payload,
          name,
          description: payload.description?.trim() || undefined,
        });
        setProjects((current) => upsertProject(current, created.data));
        return created.data;
      } catch (error) {
        throw error instanceof Error ? error : new Error("Failed to create project.");
      }
    },
    [setProjects],
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
        throw error instanceof Error ? error : new Error("Failed to delete project.");
      }
    },
    [setActiveProjectID, setProjects],
  );

  const actions = useMemo<ProjectsActions>(
    () => ({
      setProjects,
      setLoading,
      setError,
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

function projectCatalogItems(payload: unknown): ProjectRecord[] {
  if (!payload || typeof payload !== "object") {
    throw new Error("Project catalog response was invalid.");
  }
  const data = (payload as { data?: unknown }).data;
  if (!Array.isArray(data) || !data.every(isProjectRecord)) {
    throw new Error("Project catalog response was invalid.");
  }
  return data;
}

function isProjectRecord(value: unknown): value is ProjectRecord {
  if (!isUnknownRecord(value)) return false;
  if (
    !isNonBlankStringField(value, "id") ||
    !isNonBlankStringField(value, "name") ||
    !isNonBlankStringField(value, "created_at") ||
    !isNonBlankStringField(value, "updated_at") ||
    !Array.isArray(value.roots) ||
    !value.roots.every(isProjectRootRecord)
  ) {
    return false;
  }
  if (!optionalStringFieldsAreValid(value, PROJECT_OPTIONAL_STRING_FIELDS)) return false;
  if (!optionalBooleanFieldsAreValid(value, PROJECT_OPTIONAL_BOOLEAN_FIELDS)) return false;
  return (
    value.context_sources === undefined ||
    (Array.isArray(value.context_sources) &&
      value.context_sources.every(isProjectContextSourceRecord))
  );
}

const PROJECT_OPTIONAL_STRING_FIELDS = [
  "description",
  "default_root_id",
  "default_provider",
  "default_model",
  "default_agent_profile",
  "default_workspace_mode",
  "default_system_prompt",
  "last_opened_at",
] as const;

const PROJECT_OPTIONAL_BOOLEAN_FIELDS = [
  "default_tools_enabled",
  "default_compact_tool_output",
] as const;

function isProjectRootRecord(value: unknown): boolean {
  if (!isUnknownRecord(value)) return false;
  return (
    isNonBlankStringField(value, "id") &&
    isNonBlankStringField(value, "path") &&
    isStringField(value, "kind") &&
    typeof value.active === "boolean" &&
    isStringField(value, "created_at") &&
    isStringField(value, "updated_at") &&
    optionalStringFieldsAreValid(value, ["git_remote", "git_branch"])
  );
}

function isProjectContextSourceRecord(value: unknown): boolean {
  if (!isUnknownRecord(value)) return false;
  return (
    isNonBlankStringField(value, "id") &&
    isStringField(value, "kind") &&
    isStringField(value, "path") &&
    typeof value.enabled === "boolean" &&
    isStringField(value, "created_at") &&
    isStringField(value, "updated_at") &&
    optionalStringFieldsAreValid(value, [
      "title",
      "format",
      "scope",
      "trust_label",
      "source_category",
    ]) &&
    (value.metadata === undefined || isStringRecord(value.metadata))
  );
}

function isUnknownRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function isNonBlankStringField(value: Record<string, unknown>, field: string): boolean {
  const fieldValue = value[field];
  return typeof fieldValue === "string" && Boolean(fieldValue.trim());
}

function isStringField(value: Record<string, unknown>, field: string): boolean {
  return typeof value[field] === "string";
}

function optionalStringFieldsAreValid(
  value: Record<string, unknown>,
  fields: readonly string[],
): boolean {
  return fields.every((field) => value[field] === undefined || typeof value[field] === "string");
}

function optionalBooleanFieldsAreValid(
  value: Record<string, unknown>,
  fields: readonly string[],
): boolean {
  return fields.every((field) => value[field] === undefined || typeof value[field] === "boolean");
}

function isStringRecord(value: unknown): boolean {
  return isUnknownRecord(value) && Object.values(value).every((entry) => typeof entry === "string");
}

function projectCatalogErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message.trim()) return error.message.trim();
  return "Failed to load projects.";
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

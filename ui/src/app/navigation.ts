import type { WorkspaceID } from "./AppShell";

export const WORKSPACE_PATHS: Record<WorkspaceID, string> = {
  chats: "/chats",
  projects: "/projects",
  runs: "/tasks",
  connections: "/connections",
  overview: "/observability",
  usage: "/usage",
  settings: "/settings",
};

export type ProjectNavigationView = "overview" | "work" | "timeline" | "memory" | "skills";

export type ProjectNavigationState = {
  projectID: string | null;
  view: ProjectNavigationView;
  workItemID: string | null;
};

export type ParsedNavigationURL = {
  workspace: WorkspaceID | null;
  project: ProjectNavigationState | null;
  canonicalURL: string;
  isCanonical: boolean;
};

export type ProjectNavigationDestination = {
  projectID?: string | null;
  view?: ProjectNavigationView;
  workItemID?: string | null;
};

export type NavigationURLInput = string | Pick<Location, "pathname" | "search" | "hash">;

const NAVIGATION_BASE_URL = "http://hecate.local";
const PROJECT_QUERY_KEYS = ["project", "view", "work"] as const;
const PROJECT_QUERY_VIEWS = new Set<ProjectNavigationView>([
  "work",
  "timeline",
  "memory",
  "skills",
]);
const WORKSPACE_BY_PATH = new Map<string, WorkspaceID>(
  Object.entries(WORKSPACE_PATHS).map(([workspace, path]) => [path, workspace as WorkspaceID]),
);

export function parseNavigationURL(input: NavigationURLInput): ParsedNavigationURL {
  const current = toURL(input);
  const canonical = new URL(current.href);
  const workspace = workspaceFromPath(current.pathname);

  if (workspace === null) {
    return {
      workspace: null,
      project: null,
      canonicalURL: relativeURL(canonical),
      isCanonical: true,
    };
  }

  canonical.pathname = WORKSPACE_PATHS[workspace];
  if (workspace !== "projects") {
    clearProjectQuery(canonical.searchParams);
    return parsedResult(current, canonical, workspace, null);
  }

  const projectID = opaqueQueryValue(current.searchParams, "project");
  const workItemID = projectID ? opaqueQueryValue(current.searchParams, "work") : null;
  const rawView = current.searchParams.get("view");
  const view = projectID ? (workItemID ? "work" : projectView(rawView)) : "overview";
  const project = { projectID, view, workItemID } satisfies ProjectNavigationState;

  writeProjectQuery(canonical.searchParams, project);
  return parsedResult(current, canonical, workspace, project);
}

export function workspaceNavigationURL(input: NavigationURLInput, workspace: WorkspaceID): string {
  const destination = toURL(input);
  destination.pathname = WORKSPACE_PATHS[workspace];
  clearProjectQuery(destination.searchParams);
  return relativeURL(destination);
}

export function projectNavigationURL(
  input: NavigationURLInput,
  destination: ProjectNavigationDestination = {},
): string {
  const url = toURL(input);
  const projectID = opaqueValue(destination.projectID);
  const workItemID = projectID ? opaqueValue(destination.workItemID) : null;
  const project: ProjectNavigationState = {
    projectID,
    view: projectID ? (workItemID ? "work" : (destination.view ?? "overview")) : "overview",
    workItemID,
  };

  url.pathname = WORKSPACE_PATHS.projects;
  writeProjectQuery(url.searchParams, project);
  return relativeURL(url);
}

export function navigationURLsEqual(left: NavigationURLInput, right: NavigationURLInput): boolean {
  return relativeURL(toURL(left)) === relativeURL(toURL(right));
}

function parsedResult(
  current: URL,
  canonical: URL,
  workspace: WorkspaceID,
  project: ProjectNavigationState | null,
): ParsedNavigationURL {
  const canonicalURL = relativeURL(canonical);
  return {
    workspace,
    project,
    canonicalURL,
    isCanonical: canonicalURL === relativeURL(current),
  };
}

function workspaceFromPath(pathname: string): WorkspaceID | null {
  const normalizedPath = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  return WORKSPACE_BY_PATH.get(normalizedPath) ?? null;
}

function projectView(rawView: string | null): ProjectNavigationView {
  return rawView && PROJECT_QUERY_VIEWS.has(rawView as ProjectNavigationView)
    ? (rawView as ProjectNavigationView)
    : "overview";
}

function opaqueQueryValue(params: URLSearchParams, key: string): string | null {
  return opaqueValue(params.get(key));
}

function opaqueValue(value: string | null | undefined): string | null {
  return value === null || value === undefined || value === "" ? null : value;
}

function writeProjectQuery(params: URLSearchParams, project: ProjectNavigationState): void {
  writeOptionalQueryValue(params, "project", project.projectID);

  if (project.view === "overview") {
    params.delete("view");
  } else {
    params.set("view", project.view);
  }

  writeOptionalQueryValue(params, "work", project.workItemID);
}

function writeOptionalQueryValue(params: URLSearchParams, key: string, value: string | null): void {
  if (value === null) {
    params.delete(key);
  } else {
    params.set(key, value);
  }
}

function clearProjectQuery(params: URLSearchParams): void {
  for (const key of PROJECT_QUERY_KEYS) {
    params.delete(key);
  }
}

function toURL(input: NavigationURLInput): URL {
  if (typeof input === "string") {
    return new URL(input, NAVIGATION_BASE_URL);
  }
  return new URL(`${input.pathname}${input.search}${input.hash}`, NAVIGATION_BASE_URL);
}

function relativeURL(url: URL): string {
  return `${url.pathname}${url.search}${url.hash}`;
}

import type { ProjectRecord } from "../types/project";

type ProjectWorkspaceRoot = {
  id?: string;
  path: string;
  active?: boolean;
};

export function projectByID(projects: ProjectRecord[], projectID: string): ProjectRecord | null {
  const id = projectID.trim();
  if (!id) return null;
  return projects.find((project) => project.id === id) ?? null;
}

export function projectDefaultWorkspace(project: ProjectRecord | null | undefined): string {
  if (!project) return "";
  return projectDefaultWorkspaceFromRoots(project.roots, project.default_root_id);
}

export function projectDefaultWorkspaceFromRoots(
  roots: ProjectWorkspaceRoot[],
  defaultRootID?: string,
): string {
  const defaultRoot = defaultRootID ? roots.find((root) => root.id === defaultRootID) : undefined;
  const root = defaultRoot ?? roots.find((item) => item.active) ?? roots[0];
  return root?.path.trim() ?? "";
}

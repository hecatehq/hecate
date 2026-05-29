import type { ProjectRecord } from "../types/project";

export function projectByID(projects: ProjectRecord[], projectID: string): ProjectRecord | null {
  const id = projectID.trim();
  if (!id) return null;
  return projects.find((project) => project.id === id) ?? null;
}

export function projectDefaultWorkspace(project: ProjectRecord | null | undefined): string {
  if (!project) return "";
  const defaultRoot = project.default_root_id
    ? project.roots.find((root) => root.id === project.default_root_id)
    : undefined;
  const root = defaultRoot ?? project.roots.find((item) => item.active) ?? project.roots[0];
  return root?.path.trim() ?? "";
}

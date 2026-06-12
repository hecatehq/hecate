import type { ProjectRecord, ProjectRootPayload, ProjectRootRecord } from "../../types/project";

export type ProjectDefaultsForm = {
  provider: string;
  model: string;
  defaultAgentProfile: string;
  workspaceMode: string;
  defaultRootID: string;
  roots: ProjectRootPayload[];
};

export type CreateWorktreeForm = {
  baseRootID: string;
  branch: string;
  startPoint: string;
  path: string;
  active: boolean;
  setDefault: boolean;
};

export function projectDefaultsFormFromProject(project: ProjectRecord): ProjectDefaultsForm {
  return {
    provider: project.default_provider ?? "",
    model: project.default_model ?? "",
    defaultAgentProfile: project.default_agent_profile ?? "",
    workspaceMode: project.default_workspace_mode || "in_place",
    defaultRootID: project.default_root_id || project.roots[0]?.id || "",
    roots: project.roots.map(projectRootPayloadFromRecord),
  };
}

function projectRootPayloadFromRecord(root: ProjectRootRecord): ProjectRootPayload {
  const payload: ProjectRootPayload = {
    id: root.id,
    path: root.path,
    kind: root.kind,
    active: root.active,
  };
  if (root.git_remote) payload.git_remote = root.git_remote;
  if (root.git_branch) payload.git_branch = root.git_branch;
  return payload;
}

export function projectRootOptionLabel(root: ProjectRootPayload | ProjectRootRecord) {
  const parts = [root.path];
  if (root.git_branch) parts.push(`git:${root.git_branch}`);
  if (root.kind) parts.push(root.kind);
  return parts.join(" · ");
}

export function projectRootSummary(root: ProjectRootPayload | ProjectRootRecord) {
  const parts = [
    root.active ? "active" : "inactive",
    root.kind || "root",
    root.git_branch ? `git:${root.git_branch}` : "",
  ].filter(Boolean);
  return parts.join(" · ");
}

export function normalizeWorkspaceMode(value: string) {
  if (value === "persistent" || value === "ephemeral") return value;
  return "in_place";
}

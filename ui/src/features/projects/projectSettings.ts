import type {
  CreateProjectPayload,
  ProjectRecord,
  ProjectRootPayload,
  ProjectRootRecord,
} from "../../types/project";

export type CreateProjectForm = {
  name: string;
  description: string;
  rootPath: string;
  rootGitBranch: string;
};

export type ProjectDefaultsForm = {
  provider: string;
  model: string;
  defaultAgentPreset: string;
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

const unsavedRootFormKeyPrefix = "unsaved-root:";

export function createProjectPayloadFromForm(form: CreateProjectForm): CreateProjectPayload {
  const rootPath = form.rootPath.trim();
  const payload: CreateProjectPayload = {
    name: form.name.trim(),
  };
  const description = form.description.trim();
  if (description) payload.description = description;
  if (rootPath) {
    payload.roots = [
      {
        path: rootPath,
        kind: "local",
        git_branch: form.rootGitBranch.trim() || undefined,
        active: true,
      },
    ];
  }
  return payload;
}

export function projectDefaultsFormFromProject(project: ProjectRecord): ProjectDefaultsForm {
  return {
    provider: project.default_provider ?? "",
    model: project.default_model ?? "",
    defaultAgentPreset: project.default_agent_profile ?? "",
    workspaceMode: project.default_workspace_mode ?? "",
    defaultRootID: project.default_root_id || project.roots[0]?.id || "",
    roots: project.roots.map(projectRootPayloadFromRecord),
  };
}

export function projectRootPayloadFromRecord(root: ProjectRootRecord): ProjectRootPayload {
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

export function projectRootPayloadsEqual(
  a: ProjectRootPayload | ProjectRootRecord,
  b: ProjectRootPayload | ProjectRootRecord,
) {
  return (
    (a.id ?? "").trim() === (b.id ?? "").trim() &&
    a.path.trim() === b.path.trim() &&
    (a.kind ?? "").trim() === (b.kind ?? "").trim() &&
    (a.git_remote ?? "").trim() === (b.git_remote ?? "").trim() &&
    (a.git_branch ?? "").trim() === (b.git_branch ?? "").trim() &&
    Boolean(a.active) === Boolean(b.active)
  );
}

export function projectRootFormKey(root: ProjectRootPayload | ProjectRootRecord) {
  return root.id?.trim() || `${unsavedRootFormKeyPrefix}${root.path.trim()}`;
}

export function isUnsavedProjectRootFormKey(value: string) {
  return value.startsWith(unsavedRootFormKeyPrefix);
}

export function projectRootOptionLabel(root: ProjectRootPayload | ProjectRootRecord) {
  const parts = [root.path];
  if (root.git_branch) parts.push(root.git_branch);
  if (root.kind) parts.push(projectRootKindLabel(root.kind));
  return parts.join(" · ");
}

export function projectRootSummary(root: ProjectRootPayload | ProjectRootRecord) {
  const parts = [
    root.active ? "active" : "inactive",
    projectRootKindLabel(root.kind),
    root.git_branch || "",
  ].filter(Boolean);
  return parts.join(" · ");
}

function projectRootKindLabel(kind: string | undefined) {
  if (kind === "git_worktree") return "worktree";
  if (kind === "workspace") return "workspace folder";
  if (kind === "local") return "local folder";
  return kind?.replaceAll("_", " ") || "folder";
}

export function normalizeWorkspaceMode(value: string) {
  return value.trim();
}

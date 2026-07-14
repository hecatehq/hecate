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

const persistedRootFormKeyPrefix = "persisted-root:";
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
  const defaultRoot =
    project.roots.find((root) => root.id === project.default_root_id) ?? project.roots[0];
  return {
    provider: project.default_provider ?? "",
    model: project.default_model ?? "",
    defaultAgentPreset: project.default_agent_profile ?? "",
    workspaceMode: project.default_workspace_mode ?? "",
    defaultRootID: defaultRoot ? projectRootFormKey(defaultRoot) : "",
    roots: project.roots.map(projectRootPayloadFromRecord),
  };
}

export function projectDefaultsFormsEqual(a: ProjectDefaultsForm, b: ProjectDefaultsForm) {
  return (
    a.provider.trim() === b.provider.trim() &&
    a.model.trim() === b.model.trim() &&
    a.defaultAgentPreset.trim() === b.defaultAgentPreset.trim() &&
    a.workspaceMode.trim() === b.workspaceMode.trim() &&
    a.defaultRootID.trim() === b.defaultRootID.trim() &&
    a.roots.length === b.roots.length &&
    a.roots.every((root, index) => {
      const other = b.roots[index];
      return Boolean(other && projectRootPayloadsEqual(root, other));
    })
  );
}

export function rebaseProjectDefaultsForm(
  current: ProjectDefaultsForm,
  previous: ProjectDefaultsForm,
  next: ProjectDefaultsForm,
): ProjectDefaultsForm {
  const consumedNextRoots = new Set<ProjectRootPayload>();
  const roots = current.roots.flatMap((currentRoot) => {
    const previousRoot = findMatchingRoot(previous.roots, currentRoot);
    const nextRoot =
      findMatchingRoot(next.roots, currentRoot) ??
      (previousRoot ? findMatchingRoot(next.roots, previousRoot) : undefined);
    if (!previousRoot) {
      if (!nextRoot) return [currentRoot];
      consumedNextRoots.add(nextRoot);
      return [
        {
          ...nextRoot,
          active: currentRoot.active ?? nextRoot.active,
        },
      ];
    }
    if (!nextRoot) {
      return [];
    }
    consumedNextRoots.add(nextRoot);
    return [rebaseProjectRoot(currentRoot, previousRoot, nextRoot)];
  });
  for (const nextRoot of next.roots) {
    if (!consumedNextRoots.has(nextRoot)) roots.push(nextRoot);
  }

  const currentDefaultRoot = current.roots.find(
    (root) => projectRootFormKey(root) === current.defaultRootID,
  );
  const rebasedCurrentDefaultRoot = currentDefaultRoot
    ? findMatchingRoot(roots, currentDefaultRoot)
    : undefined;
  const defaultRootChanged = current.defaultRootID.trim() !== previous.defaultRootID.trim();

  return {
    provider: rebaseProjectDefault(current.provider, previous.provider, next.provider),
    model: rebaseProjectDefault(current.model, previous.model, next.model),
    defaultAgentPreset: rebaseProjectDefault(
      current.defaultAgentPreset,
      previous.defaultAgentPreset,
      next.defaultAgentPreset,
    ),
    workspaceMode: rebaseProjectDefault(
      current.workspaceMode,
      previous.workspaceMode,
      next.workspaceMode,
    ),
    defaultRootID:
      defaultRootChanged && rebasedCurrentDefaultRoot
        ? projectRootFormKey(rebasedCurrentDefaultRoot)
        : next.defaultRootID,
    roots,
  };
}

function rebaseProjectDefault(current: string, previous: string, next: string) {
  return current.trim() === previous.trim() ? next : current;
}

function rebaseProjectRoot(
  current: ProjectRootPayload,
  previous: ProjectRootPayload,
  next: ProjectRootPayload,
) {
  // Root discovery owns filesystem and Git metadata. Activation is the only
  // root field edited here, so it is the only local change carried forward.
  return {
    ...next,
    active: Boolean(current.active) === Boolean(previous.active) ? next.active : current.active,
  };
}

function findMatchingRoot(roots: ProjectRootPayload[], target: ProjectRootPayload) {
  const targetID = target.id?.trim();
  if (targetID) {
    const sameID = roots.find((root) => root.id?.trim() === targetID);
    if (sameID) return sameID;
  }
  const targetPath = target.path.trim();
  return roots.find((root) => root.path.trim() === targetPath);
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
  const rootID = root.id?.trim();
  return rootID
    ? `${persistedRootFormKeyPrefix}${rootID}`
    : `${unsavedRootFormKeyPrefix}${root.path.trim()}`;
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

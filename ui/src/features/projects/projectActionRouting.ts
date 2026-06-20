import type {
  ProjectActivityBucketKey,
  ProjectHealthAttention,
  ProjectOperationsBriefAction,
  ProjectOperationsBriefItem,
  ProjectSetupReadinessAction,
} from "../../types/project";

export type ProjectActionRoute =
  | { kind: "bootstrap_project" }
  | { kind: "create_work_item" }
  | { kind: "draft_project_proposal"; request: string; workItemID?: string }
  | { kind: "error"; message: string }
  | { kind: "none" }
  | { kind: "open_activity_bucket"; bucket: ProjectActivityBucketKey }
  | {
      kind: "open_assignment_preflight";
      assignmentID: string;
      bucket?: ProjectActivityBucketKey;
      workItemID?: string;
    }
  | { kind: "open_memory_review" }
  | { kind: "open_profiles" }
  | { kind: "open_project_settings" }
  | { kind: "open_roles" }
  | { kind: "open_skills" }
  | { kind: "open_task"; taskID: string; runID?: string }
  | { kind: "open_work_item"; bucket?: ProjectActivityBucketKey; workItemID: string }
  | { kind: "review_memory_candidate"; candidateID: string };

export function routeProjectOperationAction(
  item: ProjectOperationsBriefItem,
  selectedProjectID: string,
): ProjectActionRoute {
  const action = item.action;
  if (!action?.type) {
    return {
      kind: "error",
      message: "Project operation is missing an action. Refresh project work and try again.",
    };
  }
  if (action.project_id && selectedProjectID && action.project_id !== selectedProjectID) {
    return {
      kind: "error",
      message: "Project operation target changed. Refresh project work and try again.",
    };
  }
  if (action.type === "draft_project_proposal") {
    const request = action.request?.trim();
    if (!request) {
      return {
        kind: "error",
        message: "Project operation is missing a Project Assistant draft request.",
      };
    }
    return { kind: "draft_project_proposal", request, workItemID: action.work_item_id };
  }
  switch (action.type) {
    case "open_project_settings":
      return { kind: "open_project_settings" };
    case "open_memory_review":
      return { kind: "open_memory_review" };
    case "open_assignment_preflight":
      return routeProjectAssignmentPreflight(action);
    case "open_work_item":
      return routeProjectWorkTarget(action);
    default:
      return {
        kind: "error",
        message: "Project operation action is not supported. Refresh project work and try again.",
      };
  }
}

export function routeProjectSetupAction(
  action: ProjectSetupReadinessAction | undefined,
  selectedProjectID: string,
): ProjectActionRoute {
  if (!action?.type) {
    return { kind: "error", message: "Project setup action is missing. Refresh the project." };
  }
  if (action.project_id && selectedProjectID && action.project_id !== selectedProjectID) {
    return { kind: "error", message: "Project setup target changed. Refresh the project." };
  }
  switch (action.type) {
    case "bootstrap_project":
      return { kind: "bootstrap_project" };
    case "create_work_item":
      return { kind: "create_work_item" };
    case "open_project_settings":
      return { kind: "open_project_settings" };
    default:
      return {
        kind: "error",
        message: "Project setup action is not supported. Refresh the project.",
      };
  }
}

export function routeProjectHealthAttention(
  item: ProjectHealthAttention,
  options: { hasMemoryCandidate?: boolean; selectedProjectID?: string } = {},
): ProjectActionRoute {
  if (
    item.project_id &&
    options.selectedProjectID &&
    item.project_id !== options.selectedProjectID
  ) {
    return {
      kind: "error",
      message: "Project attention target changed. Refresh project work and try again.",
    };
  }
  if (item.action === "settings" || item.id.endsWith(":defaults")) {
    return { kind: "open_project_settings" };
  }
  if (item.action === "skills") return { kind: "open_skills" };
  if (item.action === "profiles") return { kind: "open_profiles" };
  if (item.action === "roles") return { kind: "open_roles" };
  if (item.candidate_id) {
    return options.hasMemoryCandidate
      ? { kind: "review_memory_candidate", candidateID: item.candidate_id }
      : { kind: "open_memory_review" };
  }
  if (item.work_item_id) {
    return {
      kind: "open_work_item",
      workItemID: item.work_item_id,
      bucket: projectActivityBucket(item.bucket),
    };
  }
  if (item.task_id) {
    return { kind: "open_task", taskID: item.task_id, runID: item.run_id };
  }
  const bucket = projectActivityBucket(item.bucket);
  if (bucket) return { kind: "open_activity_bucket", bucket };
  if (item.action === "memory" || item.id.endsWith(":context")) {
    return { kind: "open_memory_review" };
  }
  return { kind: "none" };
}

function routeProjectAssignmentPreflight(action: ProjectOperationsBriefAction): ProjectActionRoute {
  if (!action.assignment_id) {
    return {
      kind: "error",
      message: "Project operation is missing an assignment preflight target.",
    };
  }
  return {
    kind: "open_assignment_preflight",
    assignmentID: action.assignment_id,
    bucket: projectActivityBucket(action.activity_bucket),
    workItemID: action.work_item_id,
  };
}

function routeProjectWorkTarget(action: ProjectOperationsBriefAction): ProjectActionRoute {
  if (!action.work_item_id) {
    return {
      kind: "error",
      message: "Project operation is missing a work item target.",
    };
  }
  return {
    kind: "open_work_item",
    bucket: projectActivityBucket(action.activity_bucket),
    workItemID: action.work_item_id,
  };
}

export function projectActivityBucket(value?: string): ProjectActivityBucketKey | undefined {
  switch (value) {
    case "all":
    case "active":
    case "blocked":
    case "completed":
    case "recent":
      return value;
    default:
      return undefined;
  }
}

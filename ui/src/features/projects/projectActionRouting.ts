import type {
  ProjectActivityBucketKey,
  ProjectAction,
  ProjectHealthAttention,
  ProjectOperationsBriefItem,
  ProjectSetupReadinessAction,
} from "../../types/project";

export const PROJECT_ATTENTION_STALE_MESSAGE =
  "Project attention target changed. Refresh project work and try again.";

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
  | { kind: "open_agent_presets" }
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
  return routeProjectAction(item.action, {
    missingMessage: "Project operation is missing an action. Refresh project work and try again.",
    selectedProjectID,
    source: "Project operation",
    staleMessage: "Project operation target changed. Refresh project work and try again.",
    unsupportedMessage:
      "Project operation action is not supported. Refresh project work and try again.",
  });
}

export function routeProjectAction(
  action: ProjectAction | undefined,
  {
    hasMemoryCandidate,
    missingMessage,
    selectedProjectID,
    source,
    staleMessage,
    unsupportedMessage,
  }: {
    hasMemoryCandidate?: boolean;
    missingMessage: string;
    selectedProjectID?: string;
    source: string;
    staleMessage: string;
    unsupportedMessage: string;
  },
): ProjectActionRoute {
  if (!action?.type) {
    return {
      kind: "error",
      message: missingMessage,
    };
  }
  if (action.project_id && selectedProjectID && action.project_id !== selectedProjectID) {
    return {
      kind: "error",
      message: staleMessage,
    };
  }
  if (action.type === "draft_project_proposal") {
    const request = action.request?.trim();
    if (!request) {
      return {
        kind: "error",
        message: `${source} is missing a Project Assistant draft request.`,
      };
    }
    return { kind: "draft_project_proposal", request, workItemID: action.work_item_id };
  }
  switch (action.type) {
    case "open_project_settings":
      return { kind: "open_project_settings" };
    case "open_memory_review":
      return { kind: "open_memory_review" };
    case "open_agent_presets":
      return { kind: "open_agent_presets" };
    case "open_roles":
      return { kind: "open_roles" };
    case "open_skills":
      return { kind: "open_skills" };
    case "open_assignment_preflight":
      return routeProjectAssignmentPreflight(action, source);
    case "open_work_item":
      return routeProjectWorkTarget(action, source);
    case "open_task":
      return routeProjectTaskTarget(action, source);
    case "open_activity_bucket":
      return routeProjectActivityBucket(action, source);
    case "review_memory_candidate":
      return routeProjectMemoryCandidate(action, hasMemoryCandidate, source);
    default:
      return {
        kind: "error",
        message: unsupportedMessage,
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
  return routeProjectAction(item.action, {
    hasMemoryCandidate: options.hasMemoryCandidate,
    missingMessage:
      "Project attention item is missing an action. Refresh project work and try again.",
    selectedProjectID: options.selectedProjectID,
    source: "Project attention item",
    staleMessage: PROJECT_ATTENTION_STALE_MESSAGE,
    unsupportedMessage:
      "Project attention action is not supported. Refresh project work and try again.",
  });
}

function routeProjectAssignmentPreflight(
  action: ProjectAction,
  source: string,
): ProjectActionRoute {
  if (!action.assignment_id) {
    return {
      kind: "error",
      message: `${source} is missing an assignment preflight target.`,
    };
  }
  return {
    kind: "open_assignment_preflight",
    assignmentID: action.assignment_id,
    bucket: projectActivityBucket(action.activity_bucket),
    workItemID: action.work_item_id,
  };
}

function routeProjectWorkTarget(action: ProjectAction, source: string): ProjectActionRoute {
  if (!action.work_item_id) {
    return {
      kind: "error",
      message: `${source} is missing a work item target.`,
    };
  }
  return {
    kind: "open_work_item",
    bucket: projectActivityBucket(action.activity_bucket),
    workItemID: action.work_item_id,
  };
}

function routeProjectTaskTarget(action: ProjectAction, source: string): ProjectActionRoute {
  if (!action.task_id) {
    return {
      kind: "error",
      message: `${source} is missing a task target.`,
    };
  }
  return { kind: "open_task", taskID: action.task_id, runID: action.run_id };
}

function routeProjectActivityBucket(action: ProjectAction, source: string): ProjectActionRoute {
  const bucket = projectActivityBucket(action.activity_bucket);
  if (!bucket) {
    return {
      kind: "error",
      message: `${source} is missing an activity bucket target.`,
    };
  }
  return { kind: "open_activity_bucket", bucket };
}

function routeProjectMemoryCandidate(
  action: ProjectAction,
  hasMemoryCandidate: boolean | undefined,
  source: string,
): ProjectActionRoute {
  if (!action.candidate_id) {
    return {
      kind: "error",
      message: `${source} is missing a memory candidate target.`,
    };
  }
  return hasMemoryCandidate
    ? { kind: "review_memory_candidate", candidateID: action.candidate_id }
    : { kind: "open_memory_review" };
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

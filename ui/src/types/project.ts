export type ProjectRootRecord = {
  id: string;
  path: string;
  kind: string;
  git_remote?: string;
  git_branch?: string;
  active: boolean;
  created_at: string;
  updated_at: string;
};

export type ProjectContextSourceRecord = {
  id: string;
  kind: string;
  title?: string;
  path: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

export type ProjectRecord = {
  id: string;
  name: string;
  description?: string;
  roots: ProjectRootRecord[];
  context_sources?: ProjectContextSourceRecord[];
  default_root_id?: string;
  default_provider?: string;
  default_model?: string;
  default_agent_profile?: string;
  default_tools_enabled?: boolean;
  default_workspace_mode?: string;
  default_system_prompt?: string;
  default_compact_tool_output?: boolean;
  created_at: string;
  updated_at: string;
  last_opened_at?: string;
};

export type ProjectsResponse = {
  object: string;
  data: ProjectRecord[];
};

export type ProjectResponse = {
  object: string;
  data: ProjectRecord;
};

export type ProjectRootPayload = {
  id?: string;
  path: string;
  kind?: string;
  git_remote?: string;
  git_branch?: string;
  active?: boolean;
};

export type ProjectContextSourcePayload = {
  id?: string;
  kind?: string;
  title?: string;
  path: string;
  enabled?: boolean;
};

export type CreateProjectPayload = {
  name: string;
  description?: string;
  roots?: ProjectRootPayload[];
  context_sources?: ProjectContextSourcePayload[];
  default_root_id?: string;
  default_provider?: string;
  default_model?: string;
  default_agent_profile?: string;
  default_tools_enabled?: boolean;
  default_workspace_mode?: string;
  default_system_prompt?: string;
  default_compact_tool_output?: boolean;
};

export type UpdateProjectPayload = Partial<CreateProjectPayload> & {
  last_opened_at?: string;
};

export type ProjectWorkRoleRecord = {
  id: string;
  project_id: string;
  name: string;
  description?: string;
  instructions?: string;
  default_driver_kind?: ProjectAssignmentDriverKind | string;
  default_provider?: string;
  default_model?: string;
  default_agent_profile?: string;
  built_in: boolean;
  created_at?: string;
  updated_at?: string;
};

export type CreateProjectWorkRolePayload = {
  id?: string;
  name: string;
  description?: string;
  instructions?: string;
  default_driver_kind?: ProjectAssignmentDriverKind | string;
  default_provider?: string;
  default_model?: string;
  default_agent_profile?: string;
};

export type UpdateProjectWorkRolePayload = Partial<CreateProjectWorkRolePayload>;

export type ProjectWorkItemStatus =
  | "backlog"
  | "ready"
  | "running"
  | "review"
  | "blocked"
  | "done"
  | "cancelled";

export type ProjectWorkItemPriority = "low" | "normal" | "high" | "urgent";

export type ProjectWorkItemRecord = {
  id: string;
  project_id: string;
  title: string;
  brief?: string;
  status: ProjectWorkItemStatus | string;
  priority: ProjectWorkItemPriority | string;
  owner_role_id?: string;
  reviewer_role_ids?: string[];
  assignments?: ProjectAssignmentRecord[];
  created_at: string;
  updated_at: string;
};

export type CreateProjectWorkItemPayload = {
  id?: string;
  title: string;
  brief?: string;
  status?: ProjectWorkItemStatus | string;
  priority?: ProjectWorkItemPriority | string;
  owner_role_id?: string;
  reviewer_role_ids?: string[];
};

export type UpdateProjectWorkItemPayload = Partial<CreateProjectWorkItemPayload>;

export type ProjectAssignmentStatus =
  | "queued"
  | "running"
  | "awaiting_approval"
  | "completed"
  | "failed"
  | "cancelled";

export type ProjectAssignmentDriverKind = "hecate_task" | "external_agent";

export type ProjectAssignmentExecutionSummary = {
  task_id?: string;
  run_id?: string;
  task_status?: string;
  run_status?: string;
  status?: ProjectAssignmentStatus | string;
  pending_approval_count?: number;
  step_count?: number;
  approval_count?: number;
  artifact_count?: number;
  model?: string;
  provider?: string;
  last_error?: string;
  started_at?: string;
  finished_at?: string;
  trace_id?: string;
  missing?: boolean;
};

export type ProjectAssignmentRecord = {
  id: string;
  project_id: string;
  work_item_id: string;
  role_id: string;
  driver_kind: ProjectAssignmentDriverKind | string;
  status: ProjectAssignmentStatus | string;
  task_id?: string;
  run_id?: string;
  chat_session_id?: string;
  message_id?: string;
  context_snapshot_id?: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  completed_at?: string;
  execution?: ProjectAssignmentExecutionSummary;
};

export type CreateProjectAssignmentPayload = {
  id?: string;
  role_id: string;
  driver_kind?: ProjectAssignmentDriverKind | string;
  status?: ProjectAssignmentStatus | string;
  task_id?: string;
  run_id?: string;
  chat_session_id?: string;
  message_id?: string;
  context_snapshot_id?: string;
  started_at?: string;
  completed_at?: string;
};

export type UpdateProjectAssignmentPayload = Partial<CreateProjectAssignmentPayload>;

export type ProjectCollaborationArtifactKind = "brief" | "handoff" | "review" | "decision_note";

export type ProjectCollaborationArtifactRecord = {
  id: string;
  project_id: string;
  work_item_id: string;
  assignment_id?: string;
  kind: ProjectCollaborationArtifactKind | string;
  title?: string;
  body: string;
  author_role_id?: string;
  created_at: string;
  updated_at: string;
};

export type ProjectActivitySignal =
  | "awaiting_approval"
  | "failed"
  | "not_started"
  | "running"
  | "completed"
  | "stale_unknown"
  | (string & {});

export type ProjectActivityWorkItemRecord = {
  id: string;
  title: string;
  status: ProjectWorkItemStatus | string;
  priority: ProjectWorkItemPriority | string;
};

export type ProjectActivityArtifactSummary = {
  count: number;
  latest_kind?: string;
  latest_title?: string;
  latest_at?: string;
  assignment_id?: string;
};

export type ProjectActivityItemRecord = {
  id: string;
  project_id: string;
  work_item: ProjectActivityWorkItemRecord;
  assignment: ProjectAssignmentRecord;
  role: ProjectWorkRoleRecord;
  status: ProjectAssignmentStatus | string;
  blocking_signal: ProjectActivitySignal;
  status_summary: string;
  linked_task_id?: string;
  linked_run_id?: string;
  linked_chat_id?: string;
  linked_message_id?: string;
  recent_artifacts?: ProjectCollaborationArtifactRecord[];
  artifact_summary: ProjectActivityArtifactSummary;
  updated_at: string;
};

export type ProjectActivitySummary = {
  work_item_count: number;
  assignment_count: number;
  active_count: number;
  blocked_count: number;
  completed_count: number;
  recent_count: number;
};

export type ProjectActivityBuckets = {
  active: ProjectActivityItemRecord[];
  blocked: ProjectActivityItemRecord[];
  completed: ProjectActivityItemRecord[];
  recent: ProjectActivityItemRecord[];
};

export type ProjectActivityData = {
  project_id: string;
  summary: ProjectActivitySummary;
  buckets: ProjectActivityBuckets;
  recent: ProjectActivityItemRecord[];
};

export type ProjectActivityResponse = {
  object: string;
  data: ProjectActivityData;
};

export type ProjectWorkRolesResponse = {
  object: string;
  data: ProjectWorkRoleRecord[];
};

export type ProjectWorkRoleResponse = {
  object: string;
  data: ProjectWorkRoleRecord;
};

export type ProjectWorkItemsResponse = {
  object: string;
  data: ProjectWorkItemRecord[];
};

export type ProjectWorkItemResponse = {
  object: string;
  data: ProjectWorkItemRecord;
};

export type ProjectAssignmentsResponse = {
  object: string;
  data: ProjectAssignmentRecord[];
};

export type ProjectAssignmentResponse = {
  object: string;
  data: ProjectAssignmentRecord;
};

export type ProjectCollaborationArtifactsResponse = {
  object: string;
  data: ProjectCollaborationArtifactRecord[];
};

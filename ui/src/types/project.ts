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
  format?: string;
  scope?: string;
  trust_label?: string;
  source_category?: string;
  metadata?: Record<string, string>;
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

export type ProjectAssistantAction = {
  kind: string;
  target?: Record<string, string>;
  patch?: Record<string, unknown>;
  reason?: string;
};

export type ProjectAssistantProposal = {
  id: string;
  title: string;
  summary: string;
  actions: ProjectAssistantAction[];
  warnings?: string[];
  requires_confirmation: boolean;
  trace_id?: string;
};

export type ProjectAssistantActionResult = {
  kind: string;
  id?: string;
  data?: Record<string, string>;
};

export type ProjectAssistantApplyResult = {
  proposal_id: string;
  applied: boolean;
  actions: ProjectAssistantActionResult[];
};

export type ProjectAssistantProposalResponse = {
  object: string;
  data: ProjectAssistantProposal;
};

export type ProjectAssistantApplyResponse = {
  object: string;
  data: ProjectAssistantApplyResult;
};

export type ProjectAssistantContextSelection = {
  role_id?: string;
  role_name?: string;
  role_source?: string;
  driver_kind: string;
  driver_source: string;
  reason: string;
};

export type ProjectAssistantContextBudget = {
  memory_body_max_bytes: number;
  memory_candidate_body_max_bytes: number;
  body_original_bytes: number;
  body_returned_bytes: number;
  body_tokens_estimate: number;
  body_truncated_count: number;
};

export type ProjectAssistantContextProjectRoot = {
  id: string;
  path: string;
  kind: string;
  git_remote?: string;
  git_branch?: string;
  active: boolean;
};

export type ProjectAssistantContextProject = {
  id: string;
  name: string;
  description?: string;
  roots?: ProjectAssistantContextProjectRoot[];
  context_sources?: ProjectContextSourceRecord[];
  default_root_id?: string;
  default_provider?: string;
  default_model?: string;
  default_agent_profile?: string;
  default_workspace_mode?: string;
  created_at: string;
  updated_at: string;
};

export type ProjectAssistantContextWorkItem = {
  id: string;
  title: string;
  brief?: string;
  status: string;
  priority?: string;
  owner_role_id?: string;
  root_id?: string;
  reviewer_role_ids?: string[];
  created_at: string;
  updated_at: string;
};

export type ProjectAssistantContextRole = {
  id: string;
  name: string;
  description?: string;
  default_driver_kind?: string;
  default_provider?: string;
  default_model?: string;
  default_agent_profile?: string;
  skill_ids?: string[];
  built_in: boolean;
  created_at: string;
  updated_at: string;
};

export type ProjectAssistantContextSkill = {
  id: string;
  title: string;
  description?: string;
  path: string;
  root_id?: string;
  format: string;
  enabled: boolean;
  status: string;
  trust_label: string;
  source_context_source_ids?: string[];
  warnings?: string[];
  discovered_at: string;
  created_at: string;
  updated_at: string;
};

export type ProjectAssistantContextAssignment = {
  id: string;
  work_item_id: string;
  role_id: string;
  root_id?: string;
  driver_kind: string;
  status: string;
  execution_ref?: ProjectAssignmentExecutionRefRecord;
  created_at: string;
  updated_at: string;
  started_at?: string;
  completed_at?: string;
};

export type ProjectAssistantContextMemory = {
  id: string;
  title: string;
  body: string;
  body_original_bytes: number;
  body_returned_bytes: number;
  body_tokens_estimate: number;
  body_truncated: boolean;
  trust_label: string;
  source_kind: string;
  source_id?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

export type ProjectAssistantContextMemoryCandidate = {
  id: string;
  title: string;
  body: string;
  body_original_bytes: number;
  body_returned_bytes: number;
  body_tokens_estimate: number;
  body_truncated: boolean;
  suggested_kind?: string;
  suggested_trust_label?: string;
  suggested_source_kind?: string;
  suggested_source_id?: string;
  source_refs?: Array<{
    kind: string;
    id: string;
    title?: string;
    url?: string;
  }>;
  status: "pending" | "promoted" | "rejected" | (string & {});
  status_reason?: string;
  promoted_memory_id?: string;
  created_at: string;
  updated_at: string;
};

export type ProjectAssistantContextActivity = {
  kind: string;
  id: string;
  title: string;
  status?: string;
  updated_at: string;
};

export type ProjectAssistantContextRecord = {
  project: ProjectAssistantContextProject;
  request: string;
  selected_work?: ProjectAssistantContextWorkItem;
  roles: ProjectAssistantContextRole[];
  skills?: ProjectAssistantContextSkill[];
  assignments?: ProjectAssistantContextAssignment[];
  memory?: ProjectAssistantContextMemory[];
  memory_candidates?: ProjectAssistantContextMemoryCandidate[];
  recent_activity?: ProjectAssistantContextActivity[];
  budget: ProjectAssistantContextBudget;
  selection: ProjectAssistantContextSelection;
};

export type ProjectAssistantContextResponse = {
  object: string;
  data: ProjectAssistantContextRecord;
};

export type ProjectAssistantProposePayload = {
  id?: string;
  title?: string;
  summary?: string;
  actions: ProjectAssistantAction[];
};

export type ProjectAssistantContextPayload = {
  project_id: string;
  work_item_id?: string;
  request: string;
  role_id?: string;
  driver_kind?: string;
};

export type ProjectAssistantDraftMode = "deterministic" | "model" | "bootstrap";

export type ProjectAssistantDraftPayload = ProjectAssistantContextPayload & {
  draft_mode?: ProjectAssistantDraftMode;
  provider?: string;
  model?: string;
};

export type ProjectAssistantApplyPayload = {
  proposal: ProjectAssistantProposal;
  confirm?: boolean;
};

export type ProjectMemoryRecord = {
  id: string;
  scope: "project" | (string & {});
  project_id: string;
  title: string;
  body: string;
  trust_label: string;
  source_kind: string;
  source_id?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

export type ProjectMemoryCandidateSourceRefRecord = {
  kind: string;
  id: string;
  title?: string;
  url?: string;
};

export type ProjectMemoryCandidateRecord = {
  id: string;
  project_id: string;
  title: string;
  body: string;
  suggested_kind?: string;
  suggested_trust_label: string;
  suggested_source_kind: string;
  suggested_source_id?: string;
  source_refs?: ProjectMemoryCandidateSourceRefRecord[];
  status: "pending" | "promoted" | "rejected" | (string & {});
  status_reason?: string;
  promoted_memory_id?: string;
  created_at: string;
  updated_at: string;
};

export type ProjectMemoryResponse = {
  object: string;
  data: ProjectMemoryRecord;
};

export type ProjectMemoryListResponse = {
  object: string;
  data: ProjectMemoryRecord[];
};

export type ProjectMemoryCandidateResponse = {
  object: string;
  data: ProjectMemoryCandidateRecord;
};

export type ProjectMemoryCandidateListResponse = {
  object: string;
  data: ProjectMemoryCandidateRecord[];
};

export type CreateProjectMemoryPayload = {
  title: string;
  body: string;
  trust_label?: string;
  source_kind?: string;
  source_id?: string;
  enabled?: boolean;
};

export type UpdateProjectMemoryPayload = Partial<CreateProjectMemoryPayload>;

export type CreateProjectMemoryCandidatePayload = {
  title: string;
  body: string;
  suggested_kind?: string;
  suggested_trust_label?: string;
  suggested_source_kind?: string;
  suggested_source_id?: string;
  source_refs?: ProjectMemoryCandidateSourceRefRecord[];
};

export type PromoteProjectMemoryCandidatePayload = {
  title?: string;
  body?: string;
  trust_label?: string;
  source_kind?: string;
  source_id?: string;
  enabled?: boolean;
};

export type RejectProjectMemoryCandidatePayload = {
  reason?: string;
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
  format?: string;
  scope?: string;
  trust_label?: string;
  source_category?: string;
  metadata?: Record<string, string>;
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

export type CreateProjectWorktreeRootPayload = {
  base_root_id?: string;
  branch: string;
  path?: string;
  start_point?: string;
  active?: boolean;
  set_default?: boolean;
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
  skill_ids?: string[];
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
  skill_ids?: string[];
};

export type UpdateProjectWorkRolePayload = Partial<CreateProjectWorkRolePayload>;

export type ProjectSkillRecord = {
  id: string;
  project_id: string;
  title: string;
  description?: string;
  path: string;
  root_id?: string;
  format: string;
  enabled: boolean;
  status: "available" | "missing" | "invalid" | "conflict" | (string & {});
  trust_label: string;
  source_context_source_ids?: string[];
  warnings?: string[];
  discovered_at: string;
  created_at: string;
  updated_at: string;
};

export type UpdateProjectSkillPayload = {
  title?: string;
  description?: string;
  enabled?: boolean;
  trust_label?: string;
};

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
  root_id?: string;
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
  root_id?: string;
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

export type ProjectAssignmentExecutionRefRecord = {
  kind: "task_run" | "chat_session" | "context_snapshot" | "none" | string;
  task_id?: string;
  run_id?: string;
  chat_session_id?: string;
  message_id?: string;
  context_snapshot_id?: string;
  status?: ProjectAssignmentStatus | string;
  pending_approval_count?: number;
  trace_id?: string;
  missing?: boolean;
};

export type ProjectAssignmentRecord = {
  id: string;
  project_id: string;
  work_item_id: string;
  role_id: string;
  root_id?: string;
  driver_kind: ProjectAssignmentDriverKind | string;
  status: ProjectAssignmentStatus | string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  completed_at?: string;
  execution_ref?: ProjectAssignmentExecutionRefRecord;
  execution?: ProjectAssignmentExecutionSummary;
};

export type CreateProjectAssignmentPayload = {
  id?: string;
  role_id: string;
  root_id?: string;
  driver_kind?: ProjectAssignmentDriverKind | string;
  status?: ProjectAssignmentStatus | string;
  execution_ref?: ProjectAssignmentExecutionRefRecord;
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

export type CreateProjectCollaborationArtifactPayload = {
  id?: string;
  assignment_id?: string;
  kind: ProjectCollaborationArtifactKind | string;
  title?: string;
  body: string;
  author_role_id?: string;
};

export type ProjectHandoffStatus = "pending" | "accepted" | "superseded" | "dismissed";

export type ProjectHandoffRecord = {
  id: string;
  project_id: string;
  work_item_id: string;
  source_assignment_id?: string;
  source_run_id?: string;
  source_chat_session_id?: string;
  source_message_id?: string;
  target_role_id?: string;
  target_assignment_id?: string;
  target_work_item_id?: string;
  title: string;
  summary: string;
  recommended_next_action: string;
  linked_artifact_ids?: string[];
  linked_memory_ids?: string[];
  context_refs?: string[];
  status: ProjectHandoffStatus | string;
  provenance_kind: string;
  trust_label: string;
  created_by_role_id?: string;
  created_at: string;
  updated_at: string;
  status_changed_at: string;
};

export type CreateProjectHandoffPayload = {
  id?: string;
  source_assignment_id?: string;
  source_run_id?: string;
  source_chat_session_id?: string;
  source_message_id?: string;
  target_role_id?: string;
  target_assignment_id?: string;
  target_work_item_id?: string;
  title: string;
  summary: string;
  recommended_next_action: string;
  linked_artifact_ids?: string[];
  linked_memory_ids?: string[];
  context_refs?: string[];
  status?: ProjectHandoffStatus | string;
  provenance_kind?: string;
  trust_label?: string;
  created_by_role_id?: string;
};

export type UpdateProjectHandoffPayload = Partial<CreateProjectHandoffPayload>;

export type ProjectActivitySignal =
  | "awaiting_approval"
  | "failed"
  | "cancelled"
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

export type ProjectActivityHandoffSummary = {
  count: number;
  pending_count?: number;
  accepted_count?: number;
  latest_status?: ProjectHandoffStatus | string;
  latest_title?: string;
  latest_at?: string;
  assignment_id?: string;
  target_role_id?: string;
  target_work_item_id?: string;
};

export type ProjectActivityLinkedChatRecord = {
  id: string;
  title?: string;
  agent_id?: string;
  driver_kind?: string;
  native_session_id?: string;
  status?: string;
  latest_message_id?: string;
  latest_role?: string;
  latest_status?: string;
  latest_error?: string;
  message_count?: number;
  created_at?: string;
  updated_at?: string;
  missing?: boolean;
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
  linked_chat?: ProjectActivityLinkedChatRecord;
  linked_message_id?: string;
  recent_artifacts?: ProjectCollaborationArtifactRecord[];
  artifact_summary: ProjectActivityArtifactSummary;
  recent_handoffs?: ProjectHandoffRecord[];
  handoff_summary?: ProjectActivityHandoffSummary;
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

export type ProjectSkillsResponse = {
  object: string;
  data: ProjectSkillRecord[];
};

export type ProjectSkillResponse = {
  object: string;
  data: ProjectSkillRecord;
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

export type ProjectCollaborationArtifactResponse = {
  object: string;
  data: ProjectCollaborationArtifactRecord;
};

export type ProjectHandoffsResponse = {
  object: string;
  data: ProjectHandoffRecord[];
};

export type ProjectHandoffResponse = {
  object: string;
  data: ProjectHandoffRecord;
};

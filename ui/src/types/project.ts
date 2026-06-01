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

export type ProjectRecord = {
  id: string;
  name: string;
  description?: string;
  roots: ProjectRootRecord[];
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

export type CreateProjectPayload = {
  name: string;
  description?: string;
  roots?: ProjectRootPayload[];
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

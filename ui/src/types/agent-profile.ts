export type AgentProfileSurface = "any" | "hecate_chat" | "hecate_task" | "external_agent";

export type AgentProfileRecord = {
  id: string;
  name: string;
  description?: string;
  instructions?: string;
  surface: AgentProfileSurface | string;
  provider_hint?: string;
  model_hint?: string;
  execution_profile?: string;
  tools_enabled: boolean;
  writes_allowed: boolean;
  network_allowed: boolean;
  approval_policy: "inherit" | "require" | "block" | "allow" | string;
  project_memory_policy: "inherit" | "include" | "visible_only" | "exclude" | string;
  context_source_policy: "inherit" | "include_enabled" | "visible_only" | "exclude" | string;
  skill_ids?: string[];
  external_agent_kind?: string;
  external_agent_options?: Record<string, string>;
  created_at?: string;
  updated_at?: string;
};

export type AgentProfileResponse = {
  object: string;
  data: AgentProfileRecord;
};

export type AgentProfilesResponse = {
  object: string;
  data: AgentProfileRecord[];
};

export type CreateAgentProfilePayload = {
  id?: string;
  name: string;
  description?: string;
  instructions?: string;
  surface?: string;
  provider_hint?: string;
  model_hint?: string;
  execution_profile?: string;
  tools_enabled?: boolean;
  writes_allowed?: boolean;
  network_allowed?: boolean;
  approval_policy?: string;
  project_memory_policy?: string;
  context_source_policy?: string;
  skill_ids?: string[];
  external_agent_kind?: string;
  external_agent_options?: Record<string, string>;
};

export type UpdateAgentProfilePayload = Partial<CreateAgentProfilePayload>;

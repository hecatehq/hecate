export type AgentPresetSurface = "any" | "hecate_chat" | "hecate_task" | "external_agent";

export type AgentPresetRecord = {
  id: string;
  name: string;
  description?: string;
  instructions?: string;
  surface: AgentPresetSurface | string;
  provider_hint?: string;
  model_hint?: string;
  execution_profile?: string;
  tools_enabled: boolean;
  writes_allowed: boolean;
  network_allowed: boolean;
  // Native Hecate task browser evidence only. External Agents do not receive
  // this capability. Optional preserves compatibility with older runtimes.
  browser_allowed?: boolean;
  browser_allowed_origins?: string[];
  approval_policy: "inherit" | "require" | "block" | "allow" | string;
  project_memory_policy: "inherit" | "include" | "visible_only" | "exclude" | string;
  context_source_policy: "inherit" | "include_enabled" | "visible_only" | "exclude" | string;
  skill_ids?: string[];
  external_agent_kind?: string;
  external_agent_options?: Record<string, string>;
  built_in?: boolean;
  created_at?: string;
  updated_at?: string;
};

export type AgentPresetResponse = {
  object: string;
  data: AgentPresetRecord;
};

export type AgentPresetsResponse = {
  object: string;
  data: AgentPresetRecord[];
};

export type CreateAgentPresetPayload = {
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
  browser_allowed?: boolean;
  browser_allowed_origins?: string[];
  approval_policy?: string;
  project_memory_policy?: string;
  context_source_policy?: string;
  skill_ids?: string[];
  external_agent_kind?: string;
  external_agent_options?: Record<string, string>;
};

export type UpdateAgentPresetPayload = Partial<CreateAgentPresetPayload>;

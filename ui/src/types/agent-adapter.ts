import type { ChatConfigOptionRecord } from "./chat";

export type AgentAdapterRecord = {
  id: string;
  name: string;
  kind: string;
  command: string;
  args?: string[];
  embedded?: boolean;
  available: boolean;
  status: string;
  path?: string;
  error?: string;
  description?: string;
  cost_mode?: string;
  docs_url?: string;
  adapter_version?: string;
  agent_version?: string;
  supported_range?: string;
  version_outside_range?: boolean;
  supports_authenticate: boolean;
  supports_logout: boolean;
  auth_status?: "ok" | "unauthenticated" | "billing" | "unknown" | string;
  auth_error?: string;
  credential_modes?: AgentAdapterCredentialMode[];
  remote_credential_mode?: string;
  remote_credential_ok?: boolean;
  remote_credential_hint?: string;
  config_options?: ChatConfigOptionRecord[];
  capabilities?: AgentAdapterCapability[];
  claude_code_cli?: AgentAdapterSetupCommandStatus;
};

export type AgentAdapterCapability = {
  id: string;
  name?: string;
  description?: string;
  status: "supported" | "adapter_dependent" | "operator_opt_in" | "not_supported" | string;
};

export type AgentAdapterCredentialMode = {
  id: string;
  name?: string;
  description?: string;
  remote_allowed: boolean;
  env_keys?: string[];
};

export type AgentAdapterResponse = {
  object: string;
  data: AgentAdapterRecord[];
};

export type AgentAdapterSetupCommandStatus = {
  available: boolean;
  command?: string;
  executable_path?: string;
};

// AgentAdapterHealthRecord mirrors agentadapters.ProbeResult. Passive GET
// health reads can return "unverified"; explicit POST probes return "ready",
// "not_installed", "auth_required", or "error". The UI folds this into
// setup, sign-in, billing, or issue states for display.
export type AgentAdapterHealthRecord = {
  adapter_id: string;
  status: string;
  stage: string;
  path?: string;
  error?: string;
  stderr?: string;
  hint?: string;
  agent_info?: AgentAdapterAgentInfoRecord;
  capabilities_known?: boolean;
  supports_authenticate?: boolean;
  supports_logout?: boolean;
  supports_load_session?: boolean;
  auth_methods?: AgentAdapterAuthMethodRecord[];
  duration_ms: number;
};

export type AgentAdapterAgentInfoRecord = {
  name?: string;
  title?: string;
  version?: string;
};

export type AgentAdapterAuthMethodRecord = {
  id: string;
  kind: string;
  name?: string;
  description?: string;
};

export type AgentAdapterHealthResponse = {
  object: string;
  data: AgentAdapterHealthRecord;
};

export type AgentAdapterProbeResponse = {
  object: string;
  data: {
    adapter: AgentAdapterRecord;
    health: AgentAdapterHealthRecord;
  };
};

export type AgentAdapterLogoutRecord = {
  adapter_id: string;
  status: "logged_out" | string;
  path?: string;
  duration_ms: number;
};

export type AgentAdapterLogoutResponse = {
  object: string;
  data: AgentAdapterLogoutRecord;
};

export type AgentAdapterAuthenticateRecord = {
  adapter_id: string;
  status: "authenticated" | string;
  method_id: string;
  path?: string;
  duration_ms: number;
};

export type AgentAdapterAuthenticateResponse = {
  object: string;
  data: AgentAdapterAuthenticateRecord;
};

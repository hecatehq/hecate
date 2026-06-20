import type { ChatConfigOptionRecord } from "./chat";

export type AgentAdapterRecord = {
  id: string;
  name: string;
  kind: string;
  command: string;
  args?: string[];
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
  supports_logout: boolean;
  auth_status?: "ok" | "unauthenticated" | "billing" | "unknown" | string;
  auth_error?: string;
  credential_modes?: AgentAdapterCredentialMode[];
  remote_credential_mode?: string;
  remote_credential_ok?: boolean;
  remote_credential_hint?: string;
  config_options?: ChatConfigOptionRecord[];
  claude_code_cli?: AgentAdapterSetupCommandStatus;
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

// AgentAdapterHealthRecord mirrors agentadapters.ProbeResult. Returned
// by GET /hecate/v1/agent-adapters/{id}/health. The status string is one of
// "ready" | "not_installed" | "auth_required" | "error"; the UI folds
// this into setup, sign-in, billing, or issue states for display.
export type AgentAdapterHealthRecord = {
  adapter_id: string;
  status: string;
  stage: string;
  path?: string;
  error?: string;
  stderr?: string;
  hint?: string;
  duration_ms: number;
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

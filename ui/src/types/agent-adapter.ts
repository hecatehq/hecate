export type AgentAdapterRecord = {
  id: string;
  name: string;
  kind: string;
  command: string;
  args?: string[];
  managed?: boolean;
  managed_package?: string;
  available: boolean;
  status: string;
  path?: string;
  error?: string;
  description?: string;
  cost_mode?: string;
  docs_url?: string;
  version?: string;
  supported_range?: string;
  version_outside_range?: boolean;
  auth_status?: "ok" | "unauthenticated" | "billing" | "unknown" | string;
  auth_error?: string;
  credential_configured?: boolean;
  credential_preview?: string;
  claude_code_cli?: AgentAdapterSetupCommandStatus;
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
// "ready" | "not_installed" | "auth_required" | "error"; the UI uses
// it to colour status chips (green / amber / red / red) and to drive
// the adapter status panel in Connections.
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

export type AgentAdapterCredentialResponse = {
  object: string;
  data: {
    adapter_id: string;
    name: string;
    configured: boolean;
    preview?: string;
  };
};

export type PluginPermissionRecord = {
  value: string;
  classification: "advisory" | "enforced" | "unsupported" | string;
};

export type PluginCapabilityRecord = {
  id: string;
  kind: string;
  display_name: string;
  requested_permissions?: PluginPermissionRecord[];
  enabled: boolean;
  mcp_server?: PluginMCPServerRecord;
  warnings?: string[];
};

export type PluginMCPServerRecord = {
  name: string;
  transport: "stdio" | "http" | string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  approval_policy?: "auto" | "require_approval" | "block" | string;
};

export type PluginAuthBindingRecord = {
  capability_id?: string;
  requested_name: string;
  kind: string;
  status: "unknown" | "configured" | "expired" | "error" | string;
  secret_ref?: string;
  warnings?: string[];
};

export type PluginRecord = {
  id: string;
  name: string;
  description?: string;
  version: string;
  source_kind: string;
  source_ref?: string;
  manifest_schema_version: string;
  manifest_digest: string;
  requested_permissions?: PluginPermissionRecord[];
  registry_state: "valid" | "invalid" | "unsupported" | string;
  enabled: boolean;
  warnings?: string[];
  capabilities?: PluginCapabilityRecord[];
  auth?: PluginAuthBindingRecord[];
  installed_at: string;
  updated_at: string;
};

export type PluginsResponse = {
  object: string;
  data: PluginRecord[];
};

export type PluginResponse = {
  object: string;
  data: PluginRecord;
};

export type InstallLocalPluginPayload = {
  manifest: unknown;
  source_ref?: string;
};

export type UpdatePluginPayload = {
  enabled?: boolean;
  capabilities?: Record<string, { enabled?: boolean }>;
};

export type PluginHealthRecord = {
  plugin_id: string;
  registry_state: string;
  warnings?: string[];
  unsupported_permissions?: string[];
  unresolved_secret_bindings?: string[];
  disabled_capabilities?: string[];
  command_collisions?: Array<{ command: string; plugin_ids: string[] }>;
};

export type PluginHealthResponse = {
  object: string;
  data: PluginHealthRecord;
};

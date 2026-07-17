export type ProviderRecord = {
  name: string;
  kind: string;
  base_url?: string;
  credential_state?: "configured" | "missing" | "not_required" | "unknown";
  credential_ready?: boolean;
  healthy: boolean;
  status: string;
  routing_ready?: boolean;
  routing_blocked_reason?: string;
  default_model?: string;
  models?: string[];
  model_count?: number;
  discovery_source?: string;
  refreshed_at?: string;
  last_checked_at?: string;
  last_error?: string;
  last_error_class?: string;
  open_until?: string;
  last_latency_ms?: number;
  consecutive_failures?: number;
  total_successes?: number;
  total_failures?: number;
  timeouts?: number;
  server_errors?: number;
  rate_limits?: number;
  readiness?: ProviderReadinessSummaryRecord;
  readiness_checks?: ProviderReadinessCheckRecord[];
};

export type ProviderReadinessStatus = "ok" | "warning" | "blocked" | "unknown";

export type ProviderReadinessSummaryRecord = {
  status?: ProviderReadinessStatus;
  reason?: string;
  message?: string;
  operator_action?: string;
};

export type ProviderReadinessCheckRecord = {
  name: string;
  status: ProviderReadinessStatus;
  reason?: string;
  message?: string;
  operator_action?: string;
};

export type ProviderStatusResponse = {
  object: string;
  data: ProviderRecord[];
};

export type ProviderPresetRecord = {
  id: string;
  name: string;
  kind: string;
  protocol: string;
  base_url: string;
  api_key_env?: string;
  api_version?: string;
  default_model?: string;
  docs_url?: string;
  description?: string;
  env_snippet?: string;
};

export type ProviderPresetResponse = {
  object: string;
  data: ProviderPresetRecord[];
};

export type LocalProviderDiscoveryRecord = {
  preset_id: string;
  name: string;
  base_url: string;
  probe_url: string;
  status: "running" | "installed" | "not_detected" | "error" | "unknown";
  command?: string;
  command_available: boolean;
  command_path?: string;
  http_available: boolean;
  model_count?: number;
  models?: string[];
  error?: string;
};

export type LocalProviderDiscoveryResponse = {
  object: string;
  data: LocalProviderDiscoveryRecord[];
};

export type ConfiguredProviderRecord = {
  id: string;
  name: string;
  preset_id?: string;
  account_id?: string;
  // custom_name is an optional operator-supplied disambiguator that
  // appears alongside name in the providers table. Used to tell two
  // instances of the same preset apart ("Anthropic" + "Prod" vs
  // "Anthropic" + "Dev"). Empty when not set.
  custom_name?: string;
  kind: string;
  protocol: string;
  base_url: string;
  api_version?: string;
  default_model?: string;
  explicit_fields?: string[];
  inherited_fields?: string[];
  credential_configured: boolean;
  credential_source?: "env" | "vault";
};

export type ConfiguredPolicyRuleRecord = {
  id: string;
  action: string;
  reason?: string;
  providers?: string[];
  provider_kinds?: string[];
  models?: string[];
  route_reasons?: string[];
  min_prompt_tokens?: number;
  min_estimated_cost_micros_usd?: number;
  rewrite_model_to?: string;
};

export type ConfiguredAuditEventRecord = {
  timestamp?: string;
  actor: string;
  action: string;
  target_type: string;
  target_id: string;
  detail?: string;
};

// Host-safe readiness for the optional native browser evidence runtime. The
// API intentionally omits executable paths and diagnostic details.
export type BrowserEvidenceRuntimeReadiness = {
  available: boolean;
  status: "ready" | "not_configured" | "local_only" | "unavailable" | string;
  message: string;
  operator_action?: string;
};

export type ConfiguredStateResponse = {
  object: string;
  data: {
    backend: string;
    providers: ConfiguredProviderRecord[];
    policy_rules: ConfiguredPolicyRuleRecord[];
    events: ConfiguredAuditEventRecord[];
    browser_evidence?: BrowserEvidenceRuntimeReadiness;
  };
};

export type ProviderFilter = "auto" | string;

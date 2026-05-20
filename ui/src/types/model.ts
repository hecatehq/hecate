import type { ProviderReadinessStatus } from "./provider";

export type ModelRecord = {
  id: string;
  owned_by: string;
  metadata?: {
    provider?: string;
    provider_kind?: string;
    default?: boolean;
    discovery_source?: string;
    capabilities?: ModelCapabilitiesRecord;
    readiness?: ModelReadinessRecord;
  };
};

export type ModelCapabilitiesRecord = {
  tool_calling?: "unknown" | "none" | "basic" | "parallel" | string;
  streaming?: boolean;
  max_context_tokens?: number;
  source?: "unknown" | "catalog" | "provider" | string;
  note?: string;
  updated_at?: string;
};

export type ModelReadinessRecord = {
  provider?: string;
  matched_provider?: string;
  model?: string;
  ready: boolean;
  status?: ProviderReadinessStatus;
  reason?: string;
  message?: string;
  operator_action?: string;
  routing_ready?: boolean;
  provider_status?: string;
  provider_blocked_reason?: string;
  suggested_models?: string[];
};

export type ModelResponse = {
  object: string;
  data: ModelRecord[];
};

export type ModelFilter = "all" | "local" | "cloud";

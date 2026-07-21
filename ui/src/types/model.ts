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
  // An explicit, operator-triggered tool-call diagnostic. This stays
  // separate from catalog/provider capability provenance so the UI can show
  // why an otherwise-unknown route is now eligible for task-backed tools.
  tool_verification?: ToolCapabilityVerificationRecord;
  image_input?: "unknown" | "none" | "supported" | string;
  streaming?: boolean;
  max_context_tokens?: number;
  source?: "unknown" | "catalog" | "provider" | "mixed" | string;
  note?: string;
  updated_at?: string;
};

export type ToolCapabilityVerificationRecord = {
  status?: "testing" | "supported" | "unsupported" | "inconclusive" | string;
  checked_at?: string;
  expires_at?: string;
  reason?: string;
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

export type ModelToolCapabilityProbeResponse = {
  object: string;
  data: {
    provider: string;
    model: string;
    capabilities: ModelCapabilitiesRecord;
    verification?: ToolCapabilityVerificationRecord;
    trace_id?: string;
    performed: boolean;
  };
};

export type ModelFilter = "all" | "local" | "cloud";

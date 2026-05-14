// Routing & health: human-readable labels and tone classifiers for the
// gateway's routing decisions and provider health states. Used by the
// observability views to render route reasons, candidate health, and
// provider status without sprinkling switch statements through React.

import type { ProviderRecord, RuntimeHeaders, TraceResponse } from "../types/runtime";

export type TraceRouteRecord = TraceResponse["data"]["route"];
type TraceRouteCandidate = NonNullable<NonNullable<TraceRouteRecord>["candidates"]>[number];

export function describeRouteReason(reason?: string): string {
  if (!reason) {
    return "No route reason";
  }

  const suffixes: string[] = [];
  let base = reason;
  if (base.endsWith("_half_open_recovery")) {
    base = base.slice(0, -"_half_open_recovery".length);
    suffixes.push("recovery probe");
  }
  if (base.endsWith("_degraded")) {
    base = base.slice(0, -"_degraded".length);
    suffixes.push("degraded provider");
  }
  if (base.endsWith("_failover")) {
    base = base.slice(0, -"_failover".length);
    suffixes.push("after failover");
  }

  const labels: Record<string, string> = {
    global_default_model: "Global default model",
    pinned_provider: "Pinned provider",
    pinned_provider_model: "Pinned provider and model",
    provider_default_model: "Provider default model",
    requested_model: "Requested model",
  };

  const label = labels[base] ?? titleizeIdentifier(base);
  if (suffixes.length === 0) {
    return label;
  }
  return `${label} ${suffixes.join(", ")}`;
}

export function describeRouteSkipReason(reason?: string): string {
  if (!reason) {
    return "";
  }
  const labels: Record<string, string> = {
    policy_denied: "Policy denied",
    provider_not_found: "Provider missing",
    route_denied: "Route denied",
    provider_retry_exhausted: "Retry exhausted",
    provider_unavailable: "Provider unavailable",
    provider_slow: "Slower than peers",
    provider_less_stable: "Recent failures",
    provider_rate_limited: "Cooling down after upstream 429",
  };
  return labels[reason] ?? titleizeIdentifier(reason);
}

export function describeRouteCandidateOutcome(candidate: TraceRouteCandidate): string {
  switch (candidate.outcome) {
    case "selected":
      return "Selected route";
    case "completed":
      return "Completed route";
    case "failed":
      return "Failed attempt";
    case "denied":
      return "Denied";
    case "skipped":
      return "Skipped";
    default:
      return candidate.outcome ? titleizeIdentifier(candidate.outcome) : "Candidate";
  }
}

export function explainRouteCandidate(candidate: TraceRouteCandidate): string {
  if (candidate.outcome === "selected" || candidate.outcome === "completed") {
    const reason = candidate.reason ? describeRouteReason(candidate.reason) : "";
    return reason ? `Chosen because ${reason.toLowerCase()}.` : "Chosen by the router.";
  }
  if (candidate.skip_reason) {
    return `Skipped because ${describeRouteSkipReason(candidate.skip_reason).toLowerCase()}.`;
  }
  if (candidate.outcome === "failed") {
    return candidate.detail || "Provider attempt failed after selection.";
  }
  if (candidate.policy_reason) {
    return candidate.policy_reason;
  }
  return candidate.detail || "Considered by the router.";
}

export function describeRoutingBlockedReason(reason?: string): string {
  if (!reason) {
    return "Routing blocked";
  }
  const labels: Record<string, string> = {
    credential_missing: "Missing credentials",
    provider_disabled: "Provider disabled",
    circuit_open: "Circuit open",
    provider_rate_limited: "Cooling down after upstream 429",
    provider_unhealthy: "Provider unhealthy",
    no_models: "No discovered models",
  };
  return labels[reason] ?? titleizeIdentifier(reason);
}

export function describeCredentialState(state?: string): string {
  switch (state) {
    case "configured":
      return "Configured";
    case "missing":
      return "Missing";
    case "not_required":
      return "Not required";
    default:
      return state ? titleizeIdentifier(state) : "Unknown";
  }
}

export function describeHealthErrorClass(kind?: string): string {
  switch (kind) {
    case "rate_limit":
      return "Upstream rate limit";
    case "timeout":
      return "Timeout";
    case "server_error":
      return "Server error";
    case "other":
      return "Other error";
    default:
      return kind ? titleizeIdentifier(kind) : "Unknown";
  }
}

export function routeOutcomeTone(outcome?: string): "healthy" | "warning" | "danger" | "neutral" {
  switch (outcome) {
    case "selected":
    case "completed":
      return "healthy";
    case "failed":
      return "danger";
    case "denied":
    case "skipped":
      return "warning";
    default:
      return "neutral";
  }
}

export function healthStatusTone(status?: string): "healthy" | "warning" | "danger" | "neutral" {
  switch (status) {
    case "healthy":
      return "healthy";
    case "degraded":
    case "half_open":
      return "warning";
    case "open":
    case "unhealthy":
      return "danger";
    default:
      return "neutral";
  }
}

export function describeHealthStatus(status?: string): string {
  switch (status) {
    case "half_open":
      return "Recovery probe";
    case "open":
      return "Circuit open";
    case "degraded":
      return "Degraded";
    case "healthy":
      return "Healthy";
    case "unhealthy":
      return "Unhealthy";
    default:
      return "Unknown health";
  }
}

export function providerStatusTone(provider?: ProviderRecord): "healthy" | "warning" | "danger" | "neutral" {
  if (!provider) {
    return "neutral";
  }
  if (!provider.healthy && provider.status === "healthy") {
    return "warning";
  }
  return healthStatusTone(provider.status);
}

export function findProvider(providers: ProviderRecord[], providerName?: string): ProviderRecord | null {
  if (!providerName) {
    return null;
  }
  return providers.find((provider) => provider.name === providerName) ?? null;
}

export function countRouteHealthStatuses(route?: TraceRouteRecord | null): { healthy: number; warning: number; danger: number } {
  const summary = { healthy: 0, warning: 0, danger: 0 };

  for (const candidate of route?.candidates ?? []) {
    const tone = healthStatusTone(candidate.health_status);
    if (tone === "healthy") {
      summary.healthy += 1;
    } else if (tone === "warning") {
      summary.warning += 1;
    } else if (tone === "danger") {
      summary.danger += 1;
    }
  }

  return summary;
}

export function describeRouteRecovery(route?: TraceRouteRecord | null, runtimeHeaders?: RuntimeHeaders | null): string {
  const selectedCandidate = route?.candidates?.find((candidate) => candidate.outcome === "selected");
  const fallbackFrom = runtimeHeaders?.fallbackFrom || route?.fallback_from;

  if (selectedCandidate?.health_status === "half_open") {
    return "Recovered via half-open provider probe";
  }

  if (fallbackFrom) {
    return `Failed over from ${fallbackFrom}`;
  }

  if ((route?.failovers?.length ?? 0) > 0) {
    return "Recovered after one or more failover hops";
  }

  return "No recovery path needed";
}

function titleizeIdentifier(value: string): string {
  return value
    .split("_")
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

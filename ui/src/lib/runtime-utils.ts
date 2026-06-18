// runtime-utils.ts is the compatibility aggregator for the lib/runtime-* family.
// Each domain (trace, routing, usage) lives in its own file
// now; this module re-exports them so existing call sites and the
// runtime-utils.test.ts suite keep working without churn. Genuine
// cross-domain helpers (CSV parsing, model filters, relative time)
// remain here because they don't fit any of the more focused files.

import type { ModelFilter, ModelRecord } from "../types/model";
import type { ConfiguredProviderRecord, ProviderFilter } from "../types/provider";
import type { SessionResponse } from "../types/runtime";
import { providerAliasesForKey, providerKeyMatches } from "./provider-utils";

type SessionInfo = SessionResponse["data"] | null | undefined;

export function parseCSV(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export function filterModelsByKind(models: ModelRecord[], filter: ModelFilter): ModelRecord[] {
  switch (filter) {
    case "local":
      return models.filter((entry) => entry.metadata?.provider_kind === "local");
    case "cloud":
      return models.filter((entry) => entry.metadata?.provider_kind === "cloud");
    default:
      return models;
  }
}

export function filterModelsByProvider(
  models: ModelRecord[],
  provider: ProviderFilter,
  configuredProviders: ConfiguredProviderRecord[] = [],
): ModelRecord[] {
  if (provider === "auto") {
    return models;
  }
  const aliases = providerAliasesForKey(provider, configuredProviders);
  return models.filter((entry) => {
    const providerKey = entry.metadata?.provider || entry.owned_by;
    return providerKeyMatches(providerKey, aliases);
  });
}

export function isRemoteRuntimeSession(sessionInfo: SessionInfo): boolean {
  return Boolean(sessionInfo?.remote_identity);
}

// formatRelativeTime renders an ISO timestamp as a short relative
// string ("2s ago", "5m ago", "3h ago"), falling back to the locale
// short date when the timestamp is older than 24h.
export function formatRelativeTime(iso: string): { label: string; iso: string } {
  if (!iso) return { label: "—", iso: "" };
  const parsed = Date.parse(iso);
  if (!Number.isFinite(parsed)) return { label: iso, iso };
  const diffMs = Date.now() - parsed;
  // Future timestamps (clock skew) clamp to "just now" so we never
  // show a negative duration.
  if (diffMs < 0) return { label: "just now", iso };
  const sec = Math.floor(diffMs / 1000);
  if (sec < 60) return { label: `${sec}s ago`, iso };
  const min = Math.floor(sec / 60);
  if (min < 60) return { label: `${min}m ago`, iso };
  const hr = Math.floor(min / 60);
  if (hr < 24) return { label: `${hr}h ago`, iso };
  return { label: new Date(parsed).toLocaleDateString(), iso };
}

// ─── Barrel re-exports ───────────────────────────────────────────────────────

export type { TraceTimelineItem, WaterfallSpan, TraceWaterfall } from "./runtime-trace";
export {
  buildTraceTimeline,
  findModelInTrace,
  findProviderInTrace,
  formatTraceAttributeKey,
  formatTraceAttributeValue,
  tracePhaseFromSpan,
  buildSpanWaterfall,
  tracePhaseFromEvent,
  traceStatusBadge,
} from "./runtime-trace";

export type { TraceRouteRecord } from "./runtime-routing";
export {
  describeRouteReason,
  describeRouteSkipReason,
  describeRouteCandidateOutcome,
  describeRoutingBlockedReason,
  explainRouteCandidate,
  describeCredentialState,
  describeHealthErrorClass,
  routeOutcomeTone,
  healthStatusTone,
  describeHealthStatus,
  providerStatusTone,
  findProvider,
  countRouteHealthStatuses,
  describeRouteRecovery,
} from "./runtime-routing";

export { describeUsageScope } from "./runtime-usage";

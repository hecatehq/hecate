import type { BudgetRecord, ModelFilter, ModelRecord, ProviderFilter, ProviderRecord, RuntimeHeaders, TraceEventRecord, TraceListItem, TraceResponse, TraceSpanRecord } from "../types/runtime";

export function usdToMicros(value: string): number {
  const parsed = Number.parseFloat(value);
  if (!Number.isFinite(parsed) || parsed < 0) {
    return Number.NaN;
  }
  return Math.round(parsed * 1_000_000);
}

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

export function filterModelsByProvider(models: ModelRecord[], provider: ProviderFilter): ModelRecord[] {
  if (provider === "auto") {
    return models;
  }
  return models.filter((entry) => entry.metadata?.provider === provider);
}

export type TraceTimelineItem = {
  name: string;
  timestamp: string;
  offsetMs: number;
  offsetLabel: string;
  spanName: string;
  spanKind: string;
  phase:
    | "request"
    | "routing"
    | "cache"
    | "provider"
    | "governor"
    | "usage"
    | "cost"
    | "response"
    | "queue"
    | "orchestration"
    | "tool"
    | "approval"
    | "artifact"
    | "retention"
    | "agent_chat"
    | "other";
  attributes?: Record<string, unknown>;
};

export function buildTraceTimeline(spans: TraceSpanRecord[], traceStartedAt?: string): TraceTimelineItem[] {
  const flattened: TraceTimelineItem[] = [];
  const startSource = traceStartedAt || spans[0]?.start_time || "";
  const startMs = Date.parse(startSource);

  for (const span of spans) {
    for (const event of span.events ?? []) {
      const currentMs = Date.parse(event.timestamp);
      const offsetMs = Number.isFinite(startMs) && Number.isFinite(currentMs) ? Math.max(0, currentMs - startMs) : 0;
      flattened.push({
        name: event.name,
        timestamp: event.timestamp,
        offsetMs,
        offsetLabel: `${offsetMs} ms`,
        spanName: span.name,
        spanKind: span.kind || "internal",
        phase: tracePhaseFromEvent(event.name),
        attributes: event.attributes,
      });
    }
  }

  flattened.sort((left, right) => Date.parse(left.timestamp) - Date.parse(right.timestamp));
  return flattened;
}

export function findModelInTrace(spans: TraceSpanRecord[], provider?: string): string {
  const normalizedProvider = provider?.trim();
  const candidates: Array<{ priority: number; timestamp: number; model: string }> = [];

  for (const span of spans) {
    for (const event of span.events ?? []) {
      const attrs = event.attributes ?? {};
      if (normalizedProvider) {
        const eventProvider = traceStringAttr(attrs, "gen_ai.provider.name");
        if (eventProvider && eventProvider !== normalizedProvider) {
          continue;
        }
      }

      const responseModel = traceStringAttr(attrs, "gen_ai.response.model");
      if (responseModel) {
        candidates.push({ priority: 3, timestamp: Date.parse(event.timestamp), model: responseModel });
      }

      const requestModel = traceStringAttr(attrs, "gen_ai.request.model");
      if (requestModel) {
        const priority = event.name === "provider.call.finished" || event.name === "router.candidate.selected" ? 2 : 1;
        candidates.push({ priority, timestamp: Date.parse(event.timestamp), model: requestModel });
      }
    }
  }

  candidates.sort((left, right) => {
    if (left.priority !== right.priority) {
      return right.priority - left.priority;
    }
    const leftTime = Number.isFinite(left.timestamp) ? left.timestamp : 0;
    const rightTime = Number.isFinite(right.timestamp) ? right.timestamp : 0;
    return rightTime - leftTime;
  });

  return candidates[0]?.model ?? "";
}

function traceStringAttr(attrs: Record<string, unknown>, key: string): string {
  const value = attrs[key];
  return typeof value === "string" ? value.trim() : "";
}

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
    budget_denied: "Budget denied",
    policy_denied: "Policy denied",
    preflight_price_missing: "Missing price",
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

function titleizeIdentifier(value: string): string {
  return value
    .split("_")
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
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

export function formatTraceAttributeKey(value: string): string {
  return value.replaceAll("_", " ");
}

export function formatTraceAttributeValue(value: unknown): string {
  if (value === null || value === undefined) {
    return "n/a";
  }
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return JSON.stringify(value);
}

export function describeCachePath(runtimeHeaders?: RuntimeHeaders | null): { title: string; detail: string; tone: "healthy" | "warning" | "neutral" } {
  if (!runtimeHeaders) {
    return {
      title: "No runtime metadata",
      detail: "Run a request to inspect exact cache, semantic lookup, and provider execution details.",
      tone: "neutral",
    };
  }

  if (runtimeHeaders.cache === "true" && runtimeHeaders.cacheType === "semantic") {
    return {
      title: "Semantic cache hit",
      detail: runtimeHeaders.semanticStrategy
        ? `Matched via ${runtimeHeaders.semanticStrategy} with similarity ${runtimeHeaders.semanticSimilarity || "n/a"} using ${runtimeHeaders.semanticIndex || "unknown"} indexing.`
        : "A semantic match was returned for this request.",
      tone: "healthy",
    };
  }

  if (runtimeHeaders.cache === "true") {
    return {
      title: "Exact cache hit",
      detail: "The response was returned from exact request cache without an upstream provider call.",
      tone: "healthy",
    };
  }

  if (runtimeHeaders.semanticStrategy) {
    return {
      title: "Semantic lookup executed",
      detail: `No cache hit was returned, but semantic lookup metadata came back from ${runtimeHeaders.semanticStrategy}.`,
      tone: "warning",
    };
  }

  return {
    title: "Provider execution path",
    detail: "This request went through normal routing and provider execution without a cache hit.",
    tone: "neutral",
  };
}

export type TraceRouteRecord = TraceResponse["data"]["route"];
export type SemanticCacheInsight = {
  title: string;
  tone: "healthy" | "warning" | "danger" | "neutral";
  summary: string;
  detail: string;
  strategy: string;
  index: string;
  similarity: string;
  scope: string;
  writebackStatus: string;
  writebackTone: "healthy" | "warning" | "danger" | "neutral";
  writebackDetail: string;
};

function findTraceEvent(spans: TraceSpanRecord[], name: string): TraceEventRecord | null {
  for (let spanIndex = spans.length - 1; spanIndex >= 0; spanIndex -= 1) {
    const events = spans[spanIndex]?.events ?? [];
    for (let eventIndex = events.length - 1; eventIndex >= 0; eventIndex -= 1) {
      const event = events[eventIndex];
      if (event?.name === name) {
        return event;
      }
    }
  }

  return null;
}

function traceAttributeAsString(event: TraceEventRecord | null, key: string): string {
  const value = event?.attributes?.[key];
  if (value === null || value === undefined || value === "") {
    return "";
  }
  return String(value);
}

function formatSimilarity(value: string): string {
  if (!value) {
    return "n/a";
  }
  const parsed = Number.parseFloat(value);
  if (!Number.isFinite(parsed)) {
    return value;
  }
  return `${(parsed * 100).toFixed(1)}%`;
}

export function buildSemanticCacheInsight(
  runtimeHeaders?: RuntimeHeaders | null,
  spans: TraceSpanRecord[] = [],
): SemanticCacheInsight | null {
  const lookupStarted = findTraceEvent(spans, "semantic_cache.lookup_started");
  const miss = findTraceEvent(spans, "semantic_cache.miss");
  const hit = findTraceEvent(spans, "semantic_cache.hit");
  const storeFinished = findTraceEvent(spans, "semantic_cache.store_finished");
  const storeFailed = findTraceEvent(spans, "semantic_cache.store_failed");

  const cacheType = runtimeHeaders?.cacheType || "";
  const hasSemanticHeaders = Boolean(runtimeHeaders?.semanticStrategy || runtimeHeaders?.semanticIndex || runtimeHeaders?.semanticSimilarity);
  const hasSemanticTrace = Boolean(lookupStarted || miss || hit || storeFinished || storeFailed);
  if (cacheType !== "semantic" && !hasSemanticHeaders && !hasSemanticTrace) {
    return null;
  }

  const strategy =
    runtimeHeaders?.semanticStrategy ||
    traceAttributeAsString(hit, "hecate.semantic.strategy") ||
    "unknown";
  const index =
    runtimeHeaders?.semanticIndex ||
    traceAttributeAsString(hit, "hecate.semantic.index_type") ||
    "n/a";
  const similarityRaw =
    runtimeHeaders?.semanticSimilarity ||
    traceAttributeAsString(hit, "hecate.semantic.similarity");
  const scope =
    traceAttributeAsString(hit, "hecate.semantic.scope") ||
    traceAttributeAsString(miss, "hecate.semantic.scope") ||
    traceAttributeAsString(lookupStarted, "hecate.semantic.scope") ||
    "default";

  if (cacheType === "semantic" || hit) {
    return {
      title: "Semantic cache hit",
      tone: "healthy",
      summary: "Matched a prior response and skipped upstream execution.",
      detail: `The gateway reused a semantic match from scope ${scope}.`,
      strategy,
      index,
      similarity: formatSimilarity(similarityRaw),
      scope,
      writebackStatus: "Writeback not needed",
      writebackTone: "neutral",
      writebackDetail: "This request resolved from semantic cache, so no new semantic entry needed to be stored.",
    };
  }

  if (storeFailed) {
    return {
      title: "Semantic lookup miss",
      tone: "warning",
      summary: "Lookup missed, provider execution continued, and semantic writeback failed.",
      detail: `The request searched semantic scope ${scope} before falling through to provider execution.`,
      strategy,
      index,
      similarity: formatSimilarity(similarityRaw),
      scope,
      writebackStatus: "Writeback failed",
      writebackTone: "danger",
      writebackDetail: "The runtime attempted to persist this response for future semantic reuse, but the store operation failed.",
    };
  }

  if (storeFinished) {
    return {
      title: "Semantic lookup miss",
      tone: "warning",
      summary: "Lookup missed, provider execution continued, and the new response was stored.",
      detail: `The request searched semantic scope ${scope} before falling through to provider execution.`,
      strategy,
      index,
      similarity: formatSimilarity(similarityRaw),
      scope,
      writebackStatus: "Writeback stored",
      writebackTone: "healthy",
      writebackDetail: "The runtime persisted the final response for future semantic matches.",
    };
  }

  if (miss || lookupStarted || hasSemanticHeaders) {
    return {
      title: "Semantic lookup executed",
      tone: "warning",
      summary: "Lookup ran before normal provider execution.",
      detail: `The gateway checked semantic scope ${scope} and did not return a cached answer.`,
      strategy,
      index,
      similarity: formatSimilarity(similarityRaw),
      scope,
      writebackStatus: "No writeback signal",
      writebackTone: "neutral",
      writebackDetail: "No semantic writeback event was captured for this request.",
    };
  }

  return null;
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

export function budgetConsumedPercent(budget?: BudgetRecord | null): number {
  if (!budget || budget.credited_micros_usd <= 0) {
    return 0;
  }
  return Math.max(0, Math.min(100, Math.round((budget.debited_micros_usd / budget.credited_micros_usd) * 100)));
}

// WaterfallSpan is the per-span shape the ObservabilityView drawer
// renders as a horizontal bar in the trace waterfall. Depth is derived
// from the parent_span_id chain (purely UI-side); critical = on the
// longest single child chain rooted at the trace root.
export type WaterfallSpan = {
  span: TraceSpanRecord;
  startMs: number;
  durMs: number;
  depth: number;
  phase: TraceTimelineItem["phase"];
  hasError: boolean;
  critical: boolean;
};

export type TraceWaterfall = {
  spans: WaterfallSpan[];
  totalMs: number;
  phases: TraceTimelineItem["phase"][];
};

// tracePhaseFromSpan classifies a span's phase from its name. Mirrors
// tracePhaseFromEvent's prefix mapping but uses the span name instead
// of an event name — the legend in the waterfall reads off these.
export function tracePhaseFromSpan(name: string): TraceTimelineItem["phase"] {
  const lower = name.toLowerCase();
  if (lower.includes("agent_chat")) return "agent_chat";
  if (lower.includes("retention")) return "retention";
  if (lower.includes("queue")) return "queue";
  if (lower.includes("approval")) return "approval";
  if (lower.includes("artifact")) return "artifact";
  if (lower.includes("step") || lower.includes("tool")) return "tool";
  if (lower.includes("orchestrator") || lower.startsWith("task.")) return "orchestration";
  if (lower.includes("request") || lower.endsWith(".parse")) return "request";
  if (lower.includes("router") || lower.includes("route")) return "routing";
  if (lower.includes("cache") || lower.includes("semantic")) return "cache";
  if (lower.includes("provider")) return "provider";
  if (lower.includes("governor")) return "governor";
  if (lower.includes("cost")) return "cost";
  if (lower.includes("usage")) return "usage";
  if (lower.includes("response")) return "response";
  return "other";
}

// buildSpanWaterfall computes the data shape the drawer's waterfall
// renders. Spans are ordered by start_offset_ms; depth comes from the
// parent_span_id chain (root = depth 0). The critical-path is the
// longest single child chain by duration starting at the root.
export function buildSpanWaterfall(spans: TraceSpanRecord[]): TraceWaterfall {
  if (!spans || spans.length === 0) return { spans: [], totalMs: 0, phases: [] };

  const parsed = spans.map((s) => {
    const start = s.start_time ? Date.parse(s.start_time) : 0;
    const end = s.end_time ? Date.parse(s.end_time) : start;
    return { span: s, start: Number.isFinite(start) ? start : 0, end: Number.isFinite(end) ? end : 0 };
  });
  const t0 = Math.min(...parsed.map((p) => p.start));
  const totalMs = Math.max(...parsed.map((p) => p.end - t0), 1);

  // depth via parent_span_id chain
  const byID = new Map<string, TraceSpanRecord>();
  for (const s of spans) byID.set(s.span_id, s);
  const depthCache = new Map<string, number>();
  function depthOf(id: string, seen: Set<string> = new Set()): number {
    if (depthCache.has(id)) return depthCache.get(id)!;
    if (seen.has(id)) return 0;
    seen.add(id);
    const node = byID.get(id);
    const parent = node?.parent_span_id;
    const d = parent && byID.has(parent) ? depthOf(parent, seen) + 1 : 0;
    depthCache.set(id, d);
    return d;
  }

  // children index for critical-path walk
  const children = new Map<string, TraceSpanRecord[]>();
  let root: TraceSpanRecord | null = null;
  for (const s of spans) {
    if (!s.parent_span_id || !byID.has(s.parent_span_id)) {
      // First top-level span is the root for critical-path purposes.
      if (!root) root = s;
    } else {
      const arr = children.get(s.parent_span_id) ?? [];
      arr.push(s);
      children.set(s.parent_span_id, arr);
    }
  }
  const criticalIDs = new Set<string>();
  function walkCritical(node: TraceSpanRecord | null) {
    if (!node) return;
    criticalIDs.add(node.span_id);
    const kids = children.get(node.span_id) ?? [];
    if (kids.length === 0) return;
    let longest: TraceSpanRecord | null = null;
    let longestDur = -1;
    for (const k of kids) {
      const ks = k.start_time ? Date.parse(k.start_time) : 0;
      const ke = k.end_time ? Date.parse(k.end_time) : ks;
      const dur = Number.isFinite(ke) && Number.isFinite(ks) ? ke - ks : 0;
      if (dur > longestDur) {
        longestDur = dur;
        longest = k;
      }
    }
    walkCritical(longest);
  }
  walkCritical(root);

  const out: WaterfallSpan[] = parsed
    .map((p) => ({
      span: p.span,
      startMs: Math.max(0, p.start - t0),
      durMs: Math.max(p.end - p.start, 1),
      depth: depthOf(p.span.span_id),
      phase: tracePhaseFromSpan(p.span.name),
      hasError: p.span.status_code === "error" || (p.span.attributes?.["error"] != null && p.span.attributes?.["error"] !== ""),
      critical: criticalIDs.has(p.span.span_id),
    }))
    .sort((a, b) => a.startMs - b.startMs || a.depth - b.depth);

  const phases: TraceTimelineItem["phase"][] = [];
  for (const s of out) if (!phases.includes(s.phase)) phases.push(s.phase);

  return { spans: out, totalMs, phases };
}

export function tracePhaseFromEvent(name: string): TraceTimelineItem["phase"] {
  if (name.startsWith("agent_chat.")) {
    return "agent_chat";
  }
  if (name.startsWith("retention.")) {
    return "retention";
  }
  if (name.startsWith("queue.")) {
    return "queue";
  }
  if (name.startsWith("orchestrator.approval.") || name.startsWith("policy.")) {
    return "approval";
  }
  if (name.startsWith("orchestrator.artifact.")) {
    return "artifact";
  }
  if (name.startsWith("orchestrator.step.") || name.startsWith("tool.")) {
    return "tool";
  }
  if (name.startsWith("orchestrator.")) {
    return "orchestration";
  }
  if (name.startsWith("request.")) {
    return "request";
  }
  if (name.startsWith("router.")) {
    return "routing";
  }
  if (name.startsWith("cache.") || name.startsWith("semantic.")) {
    return "cache";
  }
  if (name.startsWith("provider.")) {
    return "provider";
  }
  if (name.startsWith("governor.")) {
    return "governor";
  }
  if (name.startsWith("cost.")) {
    return "cost";
  }
  if (name.startsWith("usage.")) {
    return "usage";
  }
  if (name.startsWith("response.")) {
    return "response";
  }
  return "other";
}

export function describeBudgetScope(budget?: BudgetRecord | null): string {
  if (!budget) {
    return "No scope";
  }

  const parts = [budget.scope];
  if (budget.provider) {
    parts.push(`provider ${budget.provider}`);
  }
  return parts.join(" / ");
}

export function budgetWarningTone(triggered: boolean): "healthy" | "warning" | "neutral" {
  return triggered ? "warning" : "neutral";
}

// traceStatusBadge collapses a TraceListItem's status fields into the
// Badge primitives the table uses. Mirrors resolveHealthBadge in the
// providers view: ok → healthy, error → down, recovered (fallback
// took over) → degraded with a "Recovered" label, otherwise a generic
// degraded badge derived from the route reason or a fallback "Issue".
export function traceStatusBadge(item: TraceListItem): { status: string; label: string } {
  if (item.status_code === "error") {
    return { status: "down", label: "Error" };
  }
  if (item.route?.fallback_from) {
    return { status: "degraded", label: "Recovered" };
  }
  if (item.status_code === "ok") {
    return { status: "healthy", label: "Healthy" };
  }
  // No status_code at all (in-flight) — show a degraded "Issue" badge
  // derived from the route reason if we have one, otherwise the
  // generic fallback. This mirrors the spirit of resolveHealthBadge,
  // which surfaces a specific reason when it can.
  if (item.route?.final_reason) {
    return { status: "degraded", label: describeRouteReason(item.route.final_reason) };
  }
  return { status: "degraded", label: "Issue" };
}

// formatRelativeTime renders an ISO timestamp as a short relative
// string ("2s ago", "5m ago", "3h ago"), falling back to the locale
// short date when the timestamp is older than 24h. Returns the
// original ISO alongside so callers can surface it as a title
// tooltip without re-parsing.
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

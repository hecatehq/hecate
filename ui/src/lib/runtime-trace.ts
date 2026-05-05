// Trace timeline + waterfall: the data model the observability drawer
// renders. buildTraceTimeline flattens span events into a timestamp-
// ordered list; buildSpanWaterfall computes the per-span waterfall row
// data including critical-path classification.

import type { TraceListItem, TraceSpanRecord } from "../types/runtime";
import { describeRouteReason } from "./runtime-routing";

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

// WaterfallSpan annotates a span with the data the drawer renders:
// startMs/durMs are normalized to the trace start; depth is computed
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

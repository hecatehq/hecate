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
// longest descent path from this span's root (DP over child chains,
// summing own duration with the longest descent under any child).
//
// unknownTiming / negativeDuration mark spans whose timestamps were
// missing/unparseable or whose end_time is before start_time (clock
// skew or partial trace). The renderer must handle these explicitly:
// `startMs` and `durMs` are NaN for unknownTiming, and `durMs` is
// preserved as-is (possibly negative or zero) for negativeDuration so
// the UI can render a "?" marker rather than silently showing a 1ms
// dot indistinguishable from a real one.
export type WaterfallSpan = {
  span: TraceSpanRecord;
  startMs: number;
  durMs: number;
  depth: number;
  phase: TraceTimelineItem["phase"];
  hasError: boolean;
  critical: boolean;
  unknownTiming: boolean;
  negativeDuration: boolean;
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
// renders. Spans are emitted in pre-order DFS (parent immediately
// followed by descendants, siblings sorted by start time) so the
// nested visual hierarchy survives clock skew that would otherwise
// place a child ahead of its parent under a (startMs, depth) sort.
//
// `critical` marks the longest-descent chain from each root, computed
// via DP: descent(node) = ownDur + max(descent(child)). This handles
// the case the previous "longest single child by duration" heuristic
// got wrong — a shorter child whose subtree contains a longer chain.
//
// Spans with unparseable start_time or end_time are flagged
// `unknownTiming: true` (NaN startMs/durMs) and excluded from t0 /
// totalMs so a single bad timestamp can't blow the whole waterfall to
// the right. Negative durations (clock skew across hosts/processes)
// are preserved as-is and flagged `negativeDuration: true` so the
// renderer can mark them rather than silently clamping to 1ms.
export function buildSpanWaterfall(spans: TraceSpanRecord[]): TraceWaterfall {
  if (!spans || spans.length === 0) return { spans: [], totalMs: 0, phases: [] };

  // Parse timestamps. Track start and end validity separately. Two
  // failure modes get distinct flags downstream:
  //   - either timestamp missing/unparseable → `unknownTiming: true`
  //     (the renderer can't trust the position OR the width)
  //   - both parsed but end < start → `negativeDuration: true`
  //     (clock skew; position is OK, duration label is "?")
  // The t0 computation can include start-valid-end-bad spans
  // (their start contributes to the trace anchor) but those rows
  // still get `unknownTiming: true` because we can't draw their bar.
  type Parsed = {
    span: TraceSpanRecord;
    start: number;       // NaN if unparseable
    end: number;         // NaN if unparseable
    startValid: boolean;
    endValid: boolean;
    durMs: number;       // end - start, or NaN if either is bad
  };
  const parsed: Parsed[] = spans.map((s) => {
    const startRaw = s.start_time ? Date.parse(s.start_time) : NaN;
    const endRaw = s.end_time ? Date.parse(s.end_time) : NaN;
    const startValid = Number.isFinite(startRaw);
    const endValid = Number.isFinite(endRaw);
    const start = startValid ? startRaw : NaN;
    const end = endValid ? endRaw : NaN;
    const durMs = startValid && endValid ? end - start : NaN;
    return { span: s, start, end, startValid, endValid, durMs };
  });

  // Cache parsed values by span_id so the sibling sort comparators and
  // the descent DP don't re-parse ISO timestamps for every comparison.
  // On a 200-span trace the previous code did O(span × log(siblings))
  // Date.parse calls; this drops it to O(span).
  const parsedByID = new Map<string, Parsed>();
  for (const p of parsed) parsedByID.set(p.span.span_id, p);

  // Compute t0/totalMs from spans with parseable starts. End-bad
  // spans still contribute their start to t0; they just don't
  // contribute to totalMs (their end is unknown). If every span is
  // bad (rare but possible when the trace store mangles ISO
  // timestamps), fall back to a 1ms total so percentages stay finite.
  //
  // Negative-duration spans (clock skew across hosts: end < start)
  // get their *start* counted into totalMs as well — without that,
  // a span starting at +200ms in a trace that otherwise ends at
  // +150ms would render at leftPct=133%, off-scale to the right.
  // Including the start keeps the renderable area covering every
  // bar's position.
  const startValidParsed = parsed.filter((p) => p.startValid);
  const t0 = startValidParsed.length > 0 ? Math.min(...startValidParsed.map((p) => p.start)) : 0;
  const endValidParsed = parsed.filter((p) => p.startValid && p.endValid);
  const totalMs = startValidParsed.length > 0
    ? Math.max(
      ...endValidParsed.map((p) => p.end - t0),
      ...startValidParsed.map((p) => p.start - t0),
      1,
    )
    : 1;

  const byID = new Map<string, TraceSpanRecord>();
  for (const s of spans) byID.set(s.span_id, s);

  // Children index keyed on parent span id. Spans whose parent is
  // missing from the visible set are treated as roots so an orphan
  // subtree (parent pruned by retention, partial trace) still
  // renders rather than disappearing.
  const children = new Map<string, TraceSpanRecord[]>();
  const roots: TraceSpanRecord[] = [];
  for (const s of spans) {
    if (!s.parent_span_id || !byID.has(s.parent_span_id)) {
      roots.push(s);
    } else {
      const arr = children.get(s.parent_span_id) ?? [];
      arr.push(s);
      children.set(s.parent_span_id, arr);
    }
  }
  // Sort siblings by start time so DFS order matches wall-clock
  // order within each subtree. Reads from the parsed cache rather
  // than re-parsing ISO timestamps. Siblings with unparseable starts
  // sort last (NaN comparisons fall through to 0, but we guard
  // explicitly).
  const sortByStart = (a: TraceSpanRecord, b: TraceSpanRecord): number => {
    const sa = parsedByID.get(a.span_id)?.start ?? NaN;
    const sb = parsedByID.get(b.span_id)?.start ?? NaN;
    if (Number.isFinite(sa) && Number.isFinite(sb)) return sa - sb;
    if (Number.isFinite(sa)) return -1;
    if (Number.isFinite(sb)) return 1;
    return 0;
  };
  for (const [, kids] of children) kids.sort(sortByStart);
  roots.sort(sortByStart);

  // depthOf walks parent_span_id; cycle guard via `seen` set returns
  // 0 if a cycle is detected (corrupt data — shouldn't happen, but
  // we'd rather render than throw).
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

  // descent: longest chain (in ms, including own duration) from this
  // span down. Reads its own duration from the parsed cache rather
  // than re-parsing. Memoized; cycle-guarded via the visiting set.
  const descentCache = new Map<string, number>();
  const visiting = new Set<string>();
  function descent(id: string): number {
    const cached = descentCache.get(id);
    if (cached !== undefined) return cached;
    if (visiting.has(id)) return 0; // cycle: terminate at this node
    visiting.add(id);
    const ownDurRaw = parsedByID.get(id)?.durMs ?? 0;
    // NaN or negative durations don't contribute to a chain length.
    const ownDur = Number.isFinite(ownDurRaw) && ownDurRaw > 0 ? ownDurRaw : 0;
    let maxChild = 0;
    for (const k of children.get(id) ?? []) {
      const d = descent(k.span_id);
      if (d > maxChild) maxChild = d;
    }
    visiting.delete(id);
    const total = ownDur + maxChild;
    descentCache.set(id, total);
    return total;
  }

  // Walk longest-descent chain from every root. Multi-root traces
  // (orphaned subtrees) get every root highlighted so we don't drop
  // them silently.
  const criticalIDs = new Set<string>();
  for (const root of roots) {
    let node: TraceSpanRecord | null = root;
    const walkSeen = new Set<string>();
    while (node && !walkSeen.has(node.span_id)) {
      walkSeen.add(node.span_id);
      criticalIDs.add(node.span_id);
      const kids: TraceSpanRecord[] = children.get(node.span_id) ?? [];
      if (kids.length === 0) break;
      let best: TraceSpanRecord | null = null;
      let bestD = -1;
      for (const k of kids) {
        const d: number = descent(k.span_id);
        if (d > bestD) { bestD = d; best = k; }
      }
      node = best;
    }
  }

  const waterfallByID = new Map<string, WaterfallSpan>();
  for (const p of parsed) {
    // unknownTiming covers any span we can't reliably draw a bar for —
    // either start or end (or both) was missing/unparseable. The
    // renderer treats these the same: render the row, mark the bar
    // slot with "?", don't trust the position.
    const unknownTiming = !p.startValid || !p.endValid;
    // negativeDuration is the narrower clock-skew case — both
    // timestamps parsed but end < start. Bar is positioned but the
    // duration is "?" rather than a misleading "-50ms" tick.
    const negativeDuration = !unknownTiming && p.durMs < 0;
    waterfallByID.set(p.span.span_id, {
      span: p.span,
      // Both startMs and durMs are NaN for unknownTiming rows — the
      // doc contract is "no usable timing"; even start-OK / end-bad
      // gets NaN startMs so callers don't accidentally render a bar
      // at a known position with an unknown width.
      startMs: unknownTiming ? NaN : Math.max(0, p.start - t0),
      durMs: unknownTiming ? NaN : p.durMs,
      depth: depthOf(p.span.span_id),
      phase: tracePhaseFromSpan(p.span.name),
      hasError: p.span.status_code === "error"
        || (p.span.attributes?.["error"] != null && p.span.attributes?.["error"] !== ""),
      critical: criticalIDs.has(p.span.span_id),
      unknownTiming,
      negativeDuration,
    });
  }

  // Pre-order DFS emit: parent, then descendants. Cycle guard via
  // `emitted` set so a corrupt parent_span_id loop doesn't infinite-
  // recurse, and so a span reachable from multiple roots doesn't
  // appear twice.
  const out: WaterfallSpan[] = [];
  const emitted = new Set<string>();
  function emit(node: TraceSpanRecord) {
    if (emitted.has(node.span_id)) return;
    emitted.add(node.span_id);
    const w = waterfallByID.get(node.span_id);
    if (w) out.push(w);
    for (const k of children.get(node.span_id) ?? []) emit(k);
  }
  for (const r of roots) emit(r);
  // Sweep up any spans not reachable from a root (shouldn't happen
  // given roots is built as "no valid parent", but defensive).
  for (const p of parsed) {
    if (!emitted.has(p.span.span_id)) {
      const w = waterfallByID.get(p.span.span_id);
      if (w) out.push(w);
    }
  }

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

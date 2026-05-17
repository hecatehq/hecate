// TraceDetail is the drawer body. Six sections stacked vertically:
// status grid, span waterfall, route summary, route candidates,
// event flow, and a diagnostics dump for the expanded span. Pure
// composition over the smaller components in this directory plus
// the shared Badge primitive.

import type { TraceTimelineItem, TraceWaterfall } from "../../../lib/runtime-trace";
import {
  describeRouteReason,
  formatTraceAttributeKey,
  formatTraceAttributeValue,
  traceStatusBadge,
} from "../../../lib/runtime-utils";
import type { TraceListItem, TraceResponse } from "../../../types/trace";
import type { UsageEventsResponse } from "../../../types/usage";
import { Badge } from "../../shared/ui";

import { RouteCandidates } from "./RouteCandidates";
import { SpanWaterfall } from "./SpanWaterfall";

type UsageEntry = NonNullable<UsageEventsResponse["data"]>[number];

type TraceDetailProps = {
  selectedID: string;
  selectedTrace?: TraceListItem;
  selectedProvider?: string;
  selectedModel?: string;
  usage?: UsageEntry;
  traceDetail: TraceResponse["data"] | null;
  traceFetching: boolean;
  waterfall: TraceWaterfall;
  traceTimeline: TraceTimelineItem[];
  expandedSpanID: string | null;
  setExpandedSpanID: (id: string | null) => void;
  phaseFilter: TraceTimelineItem["phase"] | null;
  setPhaseFilter: (p: TraceTimelineItem["phase"] | null) => void;
};

export function TraceDetail({
  selectedTrace, selectedProvider, selectedModel, usage, traceDetail, traceFetching,
  waterfall, traceTimeline, expandedSpanID, setExpandedSpanID,
  phaseFilter, setPhaseFilter,
}: TraceDetailProps) {
  const status = selectedTrace ? traceStatusBadge(selectedTrace) : null;
  const tokens = usage
    ? `${usage.prompt_tokens ?? 0} / ${usage.completion_tokens ?? 0}`
    : "—";
  const cost = usage?.amount_usd
    ? `$${Number.parseFloat(usage.amount_usd).toFixed(5)}`
    : usage ? "$0.00000" : "—";
  const latency = selectedTrace?.duration_ms != null ? `${selectedTrace.duration_ms}ms` : "—";

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
      {/* Live status grid */}
      <div style={{
        display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(120px, 1fr))", gap: 12,
        padding: 12, border: "1px solid var(--border)", borderRadius: "var(--radius-sm)",
      }}>
        <div>
          <div className="kicker" style={{ marginBottom: 4 }}>Status</div>
          {status
            ? <Badge status={status.status} label={status.label} />
            : <span style={{ fontSize: 12, color: "var(--t3)" }}>—</span>}
        </div>
        <div>
          <div className="kicker" style={{ marginBottom: 4 }}>Latency</div>
          <div style={{ fontSize: 12, color: "var(--t0)", fontFamily: "var(--font-mono)" }}>{latency}</div>
        </div>
        <div>
          <div className="kicker" style={{ marginBottom: 4 }}>Provider</div>
          <div style={{ fontSize: 12, color: selectedProvider ? "var(--t0)" : "var(--t3)", fontFamily: "var(--font-mono)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {selectedProvider || "—"}
          </div>
        </div>
        <div>
          <div className="kicker" style={{ marginBottom: 4 }}>Model</div>
          <div style={{ fontSize: 12, color: selectedModel ? "var(--t0)" : "var(--t3)", fontFamily: "var(--font-mono)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {selectedModel || "—"}
          </div>
        </div>
        <div>
          <div className="kicker" style={{ marginBottom: 4 }}>Tokens</div>
          <div style={{ fontSize: 12, color: "var(--t0)", fontFamily: "var(--font-mono)" }}>{tokens}</div>
        </div>
        <div>
          <div className="kicker" style={{ marginBottom: 4 }}>Cost</div>
          <div style={{ fontSize: 12, color: "var(--t0)", fontFamily: "var(--font-mono)" }}>{cost}</div>
        </div>
      </div>

      {/* Span waterfall — the centerpiece. */}
      <SpanWaterfall
        waterfall={waterfall}
        expandedSpanID={expandedSpanID}
        setExpandedSpanID={setExpandedSpanID}
        phaseFilter={phaseFilter}
        setPhaseFilter={setPhaseFilter}
        traceFetching={traceFetching}
        hasTraceDetail={!!traceDetail}
      />

      {/* Route summary */}
      {selectedTrace?.route && (
        <div style={{ padding: "10px 12px", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)" }}>
          <div className="kicker" style={{ marginBottom: 8 }}>Route summary</div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 12, alignItems: "center" }}>
            {selectedTrace.route.final_provider && (
              <span style={{ fontSize: 11, fontFamily: "var(--font-mono)", color: "var(--t1)" }}>
                <span style={{ color: "var(--t3)" }}>provider </span>{selectedTrace.route.final_provider}
              </span>
            )}
            {selectedTrace.route.final_model && (
              <span style={{ fontSize: 11, fontFamily: "var(--font-mono)", color: "var(--t1)" }}>
                <span style={{ color: "var(--t3)" }}>model </span>{selectedTrace.route.final_model}
              </span>
            )}
            {selectedTrace.route.final_reason && (
              <Badge status="queued" label={describeRouteReason(selectedTrace.route.final_reason)} />
            )}
            {selectedTrace.route.fallback_from && (
              <span style={{ fontSize: 11, color: "var(--amber)", fontFamily: "var(--font-mono)" }}>
                ↳ from {selectedTrace.route.fallback_from}
              </span>
            )}
          </div>
        </div>
      )}

      {/* Route candidates */}
      <RouteCandidates traceDetail={traceDetail} selectedTrace={selectedTrace} />

      {/* Event flow */}
      {traceTimeline.length > 0 && (
        <div style={{ padding: "10px 12px", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", overflow: "hidden" }}>
          <div className="kicker" style={{ marginBottom: 8 }}>Event flow</div>
          <div
            data-testid="trace-event-flow"
            aria-label="Trace event flow"
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 8,
              maxHeight: "min(320px, 42vh)",
              overflowY: "auto",
              paddingRight: 4,
            }}>
            {traceTimeline.map((event, index) => (
              <div key={`${event.timestamp}-${event.name}-${index}`} style={{ display: "grid", gridTemplateColumns: "56px 92px 1fr", gap: 10, alignItems: "start" }}>
                <div style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>{event.offsetLabel}</div>
                <Badge
                  status={
                    event.phase === "provider" ? "healthy" :
                    event.phase === "queue" ? "queued" :
                    event.phase === "routing" ? "ok" :
                    event.phase === "response" ? "done" :
                    "disabled"
                  }
                  label={event.phase}
                />
                <div>
                  <div style={{ fontSize: 12, color: "var(--t0)", marginBottom: 4 }}>{event.name}</div>
                  {event.name === "governor.model_rewrite" ? (
                    <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                      {event.attributes?.["gen_ai.request.model.original"] != null && event.attributes?.["gen_ai.request.model.rewritten"] != null && (
                        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t1)" }}>
                          {String(event.attributes["gen_ai.request.model.original"])} → {String(event.attributes["gen_ai.request.model.rewritten"])}
                        </span>
                      )}
                      <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                        {event.attributes?.["hecate.policy.rule_id"] != null && (
                          <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                            rule <span style={{ color: "var(--t1)" }}>{String(event.attributes["hecate.policy.rule_id"])}</span>
                          </span>
                        )}
                        {event.attributes?.["hecate.policy.action"] != null && (
                          <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                            action <span style={{ color: "var(--t1)" }}>{String(event.attributes["hecate.policy.action"])}</span>
                          </span>
                        )}
                        {event.attributes?.["hecate.policy.reason"] != null && (
                          <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t2)", fontStyle: "italic" }}>
                            {String(event.attributes["hecate.policy.reason"])}
                          </span>
                        )}
                      </div>
                    </div>
                  ) : event.attributes && Object.keys(event.attributes).length > 0 && (
                    <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                      {Object.entries(event.attributes).slice(0, 4).map(([key, value]) => (
                        <span key={key} style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                          {formatTraceAttributeKey(key)} <span style={{ color: "var(--t1)" }}>{formatTraceAttributeValue(value)}</span>
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Diagnostics — collapsed raw span dump for the expanded span. */}
      {expandedSpanID && (() => {
        const ws = waterfall.spans.find(s => s.span.span_id === expandedSpanID);
        if (!ws) return null;
        const attrs = ws.span.attributes ?? {};
        const attrEntries = Object.entries(attrs).filter(([, v]) => v != null && v !== "");
        return (
          <details>
            <summary style={{ fontSize: 11, color: "var(--t2)", cursor: "pointer", userSelect: "none" }}>
              Diagnostics
            </summary>
            <div style={{ marginTop: 8, padding: 10, background: "var(--bg2)", borderRadius: "var(--radius-sm)" }}>
              <div className="kicker" style={{ marginBottom: 6 }}>{ws.span.name}</div>
              <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
                {[
                  ["span_id",  ws.span.span_id],
                  // ws.startMs / ws.durMs are NaN for unknownTiming
                  // spans and durMs may be negative for clock-skewed
                  // ones. Render "?" rather than "+NaNms" / "-50ms"
                  // so the diagnostics dump doesn't produce
                  // misleading numeric strings.
                  ["start",    Number.isFinite(ws.startMs) ? `+${ws.startMs}ms` : "?"],
                  ["duration", Number.isFinite(ws.durMs) && ws.durMs >= 0 ? `${ws.durMs}ms` : "?"],
                  ["status",   ws.span.status_code],
                ].filter(([,v]) => v).map(([k, v]) => (
                  <div key={k} style={{ display: "flex", gap: 8 }}>
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", width: 60, flexShrink: 0 }}>{k}</span>
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--teal)" }}>{v}</span>
                  </div>
                ))}
                {attrEntries.length > 0 && (
                  <div style={{ marginTop: 4, borderTop: "1px solid var(--border)", paddingTop: 4, display: "flex", flexDirection: "column", gap: 2 }}>
                    {attrEntries.map(([k, v]) => (
                      <div key={k} style={{ display: "flex", gap: 8 }}>
                        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", width: 60, flexShrink: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={k}>{k.split(".").pop()}</span>
                        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t1)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{String(v)}</span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
          </details>
        );
      })()}

      {!traceFetching && !traceDetail && (
        <div style={{ fontSize: 12, color: "var(--t3)" }}>No trace detail available.</div>
      )}
    </div>
  );
}

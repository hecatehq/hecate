// SpanWaterfall renders the trace drawer's centerpiece: the per-span
// waterfall with sticky ruler, phase-legend filter chips, depth indent,
// critical-path star, and an expandable attribute panel per row. The
// data model lives in `lib/runtime-trace.ts` (`buildSpanWaterfall`);
// this file is the React surface only.

import type { TraceTimelineItem, TraceWaterfall, WaterfallSpan } from "../../../lib/runtime-trace";

import { ATTR_PRIORITY_KEYS, PHASE_LABEL, phaseColor } from "./styles";

type SpanWaterfallProps = {
  waterfall: TraceWaterfall;
  expandedSpanID: string | null;
  setExpandedSpanID: (id: string | null) => void;
  phaseFilter: TraceTimelineItem["phase"] | null;
  setPhaseFilter: (p: TraceTimelineItem["phase"] | null) => void;
  traceFetching: boolean;
  hasTraceDetail: boolean;
};

export function SpanWaterfall({
  waterfall, expandedSpanID, setExpandedSpanID,
  phaseFilter, setPhaseFilter, traceFetching, hasTraceDetail,
}: SpanWaterfallProps) {
  const { spans, totalMs, phases } = waterfall;

  if (spans.length === 0) {
    return (
      <div style={{ padding: "10px 12px", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)" }}>
        <div className="kicker-lg" style={{ marginBottom: 8, fontSize: 12, fontWeight: 500, color: "var(--t1)" }}>Spans</div>
        <div style={{ fontSize: 12, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
          {traceFetching && !hasTraceDetail ? "loading…" : "Span data not available for this trace."}
        </div>
      </div>
    );
  }

  const ticks = [0, Math.round(totalMs / 3), Math.round((2 * totalMs) / 3), totalMs];
  // Whether any span is on the critical path. Drives the legend chip
  // — no point showing "★ critical path" when no row has the star.
  const hasCriticalPath = spans.some(s => s.critical);

  return (
    <div style={{ padding: "10px 12px", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)" }}>
      {/* Header — count + total */}
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 8, flexWrap: "wrap" }}>
        <span className="kicker-lg" style={{ fontSize: 12, fontWeight: 500, color: "var(--t0)", letterSpacing: "0.04em", textTransform: "uppercase" }}>
          Spans ({spans.length}) · total {totalMs} ms
        </span>
        {(phases.length > 1 || hasCriticalPath) && (
          <div role="group" aria-label="Phase legend" style={{ display: "flex", gap: 4, flexWrap: "wrap", marginLeft: "auto" }}>
            {phases.map(p => {
              const active = phaseFilter === p;
              return (
                <button
                  key={p}
                  onClick={() => setPhaseFilter(active ? null : p)}
                  aria-pressed={active}
                  style={{
                    background: active ? "var(--bg3)" : "transparent",
                    border: `1px solid ${active ? "var(--teal)" : "var(--border)"}`,
                    borderRadius: "var(--radius-sm)",
                    padding: "2px 6px",
                    cursor: "pointer",
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 4,
                    fontFamily: "var(--font-mono)",
                    fontSize: 10,
                    color: "var(--t1)",
                  }}>
                  <span style={{ display: "inline-block", width: 8, height: 8, borderRadius: 2, background: phaseColor(p) }} />
                  {PHASE_LABEL[p]}
                </button>
              );
            })}
            {hasCriticalPath && (
              <span
                title="Spans on the critical path — the longest dependency chain through this trace."
                style={{
                  border: "1px solid var(--border)",
                  borderRadius: "var(--radius-sm)",
                  padding: "2px 6px",
                  display: "inline-flex",
                  alignItems: "center",
                  gap: 4,
                  fontFamily: "var(--font-mono)",
                  fontSize: 10,
                  color: "var(--t2)",
                }}>
                <span style={{ color: "var(--amber)" }}>★</span>
                critical path
              </span>
            )}
          </div>
        )}
      </div>

      {/* Sticky ruler. Three things needed for it to render reliably
        on top of subsequent span rows:
         - `bg2` (not `bg1`) so the ruler has visible contrast against
           the surrounding card and so its background actually
           occludes the row that scrolls under it. `bg1` is the same
           color as the card it's sitting in.
         - `isolation: isolate` creates a stacking context so the
           absolute-positioned bar children inside SpanRow can't
           paint above the sticky ruler. Without it, a high-z-index
           descendant of a sibling could land on top.
         - `marginBottom: 6` separates the ruler from the first span
           row — without it, the row's amber critical-path border
           visually merges with the ruler's own bottom border. */}
      <div style={{
        position: "sticky", top: 0, zIndex: 5,
        background: "var(--bg2)",
        isolation: "isolate",
        display: "grid",
        gridTemplateColumns: "240px 1fr 60px",
        gap: 8,
        // paddingRight matches the SpanRow grid below so the time-axis
        // ticks line up with bar-column edges. Without this the
        // ruler's "1fr" is 4px wider than each row's "1fr" and the
        // rightmost tick label drifts past the bar ends.
        padding: "6px 4px 6px 0",
        marginBottom: 6,
        borderBottom: "1px solid var(--border)",
      }}>
        <div />
        <div style={{ position: "relative", height: 12 }}>
          {ticks.map((t, i) => (
            <span
              key={i}
              style={{
                position: "absolute",
                left: `${(t / totalMs) * 100}%`,
                transform: i === 0 ? "translateX(0)" : i === ticks.length - 1 ? "translateX(-100%)" : "translateX(-50%)",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: "var(--t2)",
              }}>{t}ms</span>
          ))}
        </div>
        <div />
      </div>

      {/* Span rows */}
      <div style={{ display: "flex", flexDirection: "column" }}>
        {spans.map((ws) => (
          <SpanRow
            key={ws.span.span_id}
            ws={ws}
            totalMs={totalMs}
            isExpanded={expandedSpanID === ws.span.span_id}
            isDimmed={phaseFilter !== null && ws.phase !== phaseFilter}
            onToggle={() => setExpandedSpanID(expandedSpanID === ws.span.span_id ? null : ws.span.span_id)}
          />
        ))}
      </div>
    </div>
  );
}

function SpanRow({
  ws, totalMs, isExpanded, isDimmed, onToggle,
}: {
  ws: WaterfallSpan;
  totalMs: number;
  isExpanded: boolean;
  isDimmed: boolean;
  onToggle: () => void;
}) {
  // unknownTiming: span had missing/unparseable timestamps. We render
  // a tiny 1%-wide indicator at left:0 (not a meaningful position —
  // see the comment in runtime-trace.ts) plus a "?" duration label,
  // so the row is visible without silently misleading the operator
  // about position or width.
  // negativeDuration: both timestamps parsed but end < start (clock
  // skew or partial trace). The bar IS positioned at the span's
  // real start but the duration label is "?" rather than a
  // misleading "-50ms" or a silently-clamped 1ms tick.
  const leftPct = ws.unknownTiming ? 0 : (ws.startMs / totalMs) * 100;
  const widthPct = ws.unknownTiming || ws.negativeDuration
    ? 1
    : Math.max((ws.durMs / totalMs) * 100, 0.5);
  const color = phaseColor(ws.phase, ws.span);
  const opacity = isDimmed ? 0.3 : 1;
  // Duration label inside the bar when wide enough, otherwise to its
  // right. Parent spans (hasChildren) render as a thin outlined
  // bracket — no room for an inside label, so the duration falls back
  // to the right column.
  const labelInside = widthPct > 12 && !ws.unknownTiming && !ws.negativeDuration && !ws.hasChildren;
  const durLabel = ws.unknownTiming || ws.negativeDuration ? "?" : `${Math.max(ws.durMs, 0).toFixed(0)}ms`;
  const barTone = ws.unknownTiming || ws.negativeDuration ? "var(--t3)" : (ws.hasError ? "var(--red)" : color);

  return (
    <div>
      <div
        onClick={onToggle}
        role="button"
        tabIndex={0}
        aria-label={`span ${ws.span.name}`}
        aria-expanded={isExpanded}
        onKeyDown={e => {
          if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onToggle(); }
        }}
        style={{
          height: 22,
          display: "grid",
          gridTemplateColumns: "240px 1fr 60px",
          gap: 8,
          alignItems: "center",
          cursor: "pointer",
          background: isExpanded ? "var(--bg2)" : "transparent",
          borderLeft: ws.critical ? "2px solid var(--amber)" : "2px solid transparent",
          opacity,
          paddingRight: 4,
        }}>
        {/* Span name column with depth indent + status dot */}
        <div style={{ display: "flex", alignItems: "center", gap: 6, paddingLeft: 6 + ws.depth * 12, overflow: "hidden" }}>
          <span
            aria-label={ws.hasError ? "error" : "ok"}
            style={{
              flexShrink: 0,
              width: 6, height: 6, borderRadius: "50%",
              background: ws.hasError ? "var(--red)" : "var(--green)",
            }}
          />
          <span
            title={ws.span.name}
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 12,
              color: ws.hasError ? "var(--red)" : "var(--t1)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              flex: 1,
            }}>
            {ws.span.name}
          </span>
          {ws.critical && (
            <span title="critical path" style={{ color: "var(--amber)", fontSize: 10, flexShrink: 0 }}>★</span>
          )}
        </div>

        {/* Bar column. Parent spans (hasChildren) render as a thin
            outlined bracket so the child rows below show their real
            offsets without competing with an always-fully-covering
            parent bar — see hasChildren in runtime-trace.ts.
            Unknown-timing and negative-duration spans always render
            as filled markers regardless: the parent/child distinction
            isn't meaningful when the timing is unusable. */}
        <div style={{ position: "relative", height: 14 }}>
          <div
            style={{
              position: "absolute",
              left: `${leftPct}%`,
              width: `${widthPct}%`,
              minWidth: 2,
              height: ws.hasChildren && !ws.unknownTiming && !ws.negativeDuration ? 6 : "100%",
              top: ws.hasChildren && !ws.unknownTiming && !ws.negativeDuration ? 4 : 0,
              background: ws.hasChildren && !ws.unknownTiming && !ws.negativeDuration ? "transparent" : barTone,
              border: ws.hasChildren && !ws.unknownTiming && !ws.negativeDuration ? `1px solid ${barTone}` : "none",
              borderRadius: 2,
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-end",
              paddingRight: 4,
              boxSizing: "border-box",
            }}>
            {labelInside && (
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t0)" }}>
                {durLabel}
              </span>
            )}
          </div>
        </div>

        {/* Right-side label */}
        <div
          title={ws.unknownTiming
            ? "missing or unparseable timestamps"
            : ws.negativeDuration
              ? "end_time is before start_time (clock skew or partial trace)"
              : undefined}
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: ws.unknownTiming || ws.negativeDuration ? "var(--amber)" : "var(--t1)",
            textAlign: "right",
            whiteSpace: "nowrap",
          }}>
          {labelInside ? "" : durLabel}
          {ws.hasError ? " · ERR" : ""}
        </div>
      </div>

      {isExpanded && <SpanAttributePanel ws={ws} />}
    </div>
  );
}

function SpanAttributePanel({ ws }: { ws: WaterfallSpan }) {
  const attrs = ws.span.attributes ?? {};
  const present = Object.entries(attrs).filter(([, v]) => v != null && v !== "");
  const priority = present.filter(([k]) => ATTR_PRIORITY_KEYS.includes(k));
  const rest = present.filter(([k]) => !ATTR_PRIORITY_KEYS.includes(k));

  return (
    <div
      data-testid={`span-attrs-${ws.span.span_id}`}
      style={{
        marginLeft: 12 + ws.depth * 12,
        marginTop: 2,
        marginBottom: 6,
        padding: "8px 10px",
        background: "var(--bg2)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
      }}>
      {priority.length === 0 && rest.length === 0 ? (
        <div style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
          No attributes recorded for this span.
        </div>
      ) : (
        <>
          <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
            {priority.map(([k, v]) => (
              <div key={k} style={{ display: "flex", gap: 8 }}>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", width: 140, flexShrink: 0 }}>{k}</span>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t1)", overflow: "hidden", textOverflow: "ellipsis" }}>{String(v)}</span>
              </div>
            ))}
          </div>
          {rest.length > 0 && (
            <details style={{ marginTop: priority.length ? 6 : 0 }}>
              <summary style={{ fontSize: 10, color: "var(--t2)", cursor: "pointer", userSelect: "none", fontFamily: "var(--font-mono)" }}>
                {rest.length} more attribute{rest.length === 1 ? "" : "s"}
              </summary>
              <div style={{ marginTop: 4, display: "flex", flexDirection: "column", gap: 2 }}>
                {rest.map(([k, v]) => (
                  <div key={k} style={{ display: "flex", gap: 8 }}>
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", width: 140, flexShrink: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={k}>{k}</span>
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t1)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{String(v)}</span>
                  </div>
                ))}
              </div>
            </details>
          )}
        </>
      )}
    </div>
  );
}

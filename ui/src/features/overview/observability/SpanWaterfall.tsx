import { Fragment } from "react";

// SpanWaterfall renders the trace drawer's centerpiece: the per-span
// waterfall with a DevTools-style ruler, phase-legend filter chips,
// depth indent, and an expandable attribute panel per row. The
// data model lives in `lib/runtime-trace.ts` (`buildSpanWaterfall`);
// this file is the React surface only.

import type { TraceTimelineItem, TraceWaterfall, WaterfallSpan } from "../../../lib/runtime-trace";

import { ATTR_PRIORITY_KEYS, PHASE_LABEL, phaseColor } from "./styles";

const WATERFALL_COLUMNS = "minmax(240px, 30%) minmax(360px, 1fr) 72px";
const WATERFALL_COLUMN_GAP = 12;

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

  const ticks = waterfallTicks(totalMs);

  return (
    <div style={{ padding: "10px 12px", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)" }}>
      {/* Header — count + total */}
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 8, flexWrap: "wrap" }}>
        <span className="kicker-lg" style={{ fontSize: 12, fontWeight: 500, color: "var(--t0)", letterSpacing: "0.04em", textTransform: "uppercase" }}>
          Spans ({spans.length}) · total {totalMs} ms
        </span>
        {phases.length > 1 && (
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
          </div>
        )}
      </div>

      <div
        data-testid="span-waterfall-scroll"
        style={{
          maxHeight: "min(420px, 52vh)",
          overflowY: "auto",
          overflowX: "hidden",
          borderTop: "1px solid var(--border)",
        }}>
        <WaterfallRuler ticks={ticks} />

        {/* Span rows */}
        <div style={{ display: "flex", flexDirection: "column" }}>
          {spans.map((ws) => (
            <SpanRow
              key={ws.span.span_id}
              ws={ws}
              totalMs={totalMs}
              ticks={ticks}
              isExpanded={expandedSpanID === ws.span.span_id}
              isDimmed={phaseFilter !== null && ws.phase !== phaseFilter}
              onToggle={() => setExpandedSpanID(expandedSpanID === ws.span.span_id ? null : ws.span.span_id)}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

function WaterfallRuler({ ticks }: { ticks: WaterfallTick[] }) {
  return (
    // Ruler sticks only inside the waterfall scroller. If it sticks
    // to the whole trace drawer, it floats at the top after the
    // waterfall itself has scrolled away.
    <div style={{
      position: "sticky", top: 0, zIndex: 5,
      background: "var(--bg2)",
      isolation: "isolate",
      display: "grid",
      gridTemplateColumns: WATERFALL_COLUMNS,
      columnGap: WATERFALL_COLUMN_GAP,
      padding: "6px 0",
      marginBottom: 6,
      borderBottom: "1px solid var(--border)",
    }}>
      <div />
      <div style={{
        position: "relative",
        height: 18,
        borderTop: "1px solid var(--border)",
        borderBottom: "1px solid var(--border)",
        background: "linear-gradient(180deg, color-mix(in srgb, var(--bg3) 65%, transparent), transparent)",
      }}>
        {ticks.map((t) => (
          <Fragment key={t.pct}>
            <span
              aria-hidden="true"
              style={{
                position: "absolute",
                left: `${t.pct}%`,
                top: 0,
                bottom: 0,
                width: 1,
                background: t.edge ? "var(--border)" : "color-mix(in srgb, var(--border) 55%, transparent)",
              }}
            />
            <span
              data-testid="span-waterfall-tick"
              style={{
                position: "absolute",
                left: `${t.pct}%`,
                top: -1,
                transform: t.pct === 0 ? "translateX(0)" : t.pct === 100 ? "translateX(-100%)" : "translateX(-50%)",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: "var(--t2)",
                lineHeight: "18px",
                whiteSpace: "nowrap",
              }}>{t.label}</span>
          </Fragment>
        ))}
      </div>
      <div />
    </div>
  );
}

function SpanRow({
  ws, totalMs, ticks, isExpanded, isDimmed, onToggle,
}: {
  ws: WaterfallSpan;
  totalMs: number;
  ticks: WaterfallTick[];
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
  // right.
  const labelInside = widthPct > 12 && !ws.unknownTiming && !ws.negativeDuration;
  const durLabel = ws.unknownTiming || ws.negativeDuration ? "?" : formatWaterfallMs(Math.max(ws.durMs, 0));
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
          minHeight: 24,
          display: "grid",
          gridTemplateColumns: WATERFALL_COLUMNS,
          columnGap: WATERFALL_COLUMN_GAP,
          alignItems: "center",
          cursor: "pointer",
          background: isExpanded ? "var(--bg2)" : "transparent",
          opacity,
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
        </div>

        {/* Bar column. Every row uses the same filled-bar treatment,
            which is closer to browser DevTools and avoids making
            parent spans look like selected/outlined objects. */}
        <div style={{ position: "relative", height: 18, overflow: "hidden" }}>
          {ticks.map((t, i) => (
            <span
              key={i}
              aria-hidden="true"
              style={{
                position: "absolute",
                left: `${t.pct}%`,
                top: 0,
                bottom: 0,
                width: 1,
                background: t.edge ? "color-mix(in srgb, var(--border) 75%, transparent)" : "color-mix(in srgb, var(--border) 28%, transparent)",
              }}
            />
          ))}
          <div
            data-testid={`span-waterfall-bar-${ws.span.span_id}`}
            style={{
              position: "absolute",
              left: `${leftPct}%`,
              width: `${widthPct}%`,
              minWidth: 2,
              height: "100%",
              top: 0,
              background: barTone,
              border: "none",
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

type WaterfallTick = {
  pct: number;
  label: string;
  edge: boolean;
};

function waterfallTicks(totalMs: number): WaterfallTick[] {
  const safeTotal = Number.isFinite(totalMs) && totalMs > 0 ? totalMs : 1;
  return [0, 0.25, 0.5, 0.75, 1].map((ratio) => {
    const value = safeTotal * ratio;
    return {
      pct: ratio * 100,
      label: formatWaterfallMs(value),
      edge: ratio === 0 || ratio === 1,
    };
  });
}

function formatWaterfallMs(value: number): string {
  if (!Number.isFinite(value)) return "?";
  const abs = Math.abs(value);
  if (abs === 0) return "0ms";
  if (abs < 1) return `${trimFixed(value, 2)}ms`;
  if (abs < 10) return `${trimFixed(value, 1)}ms`;
  return `${Math.round(value)}ms`;
}

function trimFixed(value: number, digits: number): string {
  return value.toFixed(digits).replace(/\.?0+$/, "");
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

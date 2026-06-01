// RecentActivityStrip is a tiny "is the system OK right now?"
// summary that sits above the recent-traces table. One row of
// status dots — one per visible trace, color-coded — gives the
// operator the visual rhythm of recent requests; a stat line
// underneath gives p50/p95 latency and the error count.
//
// No bucketing, no time axis. The table below is the detail view;
// this strip is just the rhythm. Derived entirely from the
// already-loaded recent-traces feed — no new endpoint, no new
// polling.

import type { TraceListItem } from "../../../types/trace";

type Props = {
  traces: TraceListItem[];
  labelsByRequestID?: Map<string, { provider?: string; model?: string }>;
  latencyByRequestID?: Map<string, number>;
};

export function RecentActivityStrip({ traces, labelsByRequestID, latencyByRequestID }: Props) {
  if (traces.length === 0) return null;

  // Oldest → newest left to right matches "what just happened ←
  // earlier" reading order. The trace feed comes back newest first
  // so we reverse for the strip; the table below keeps newest first.
  const ordered = traces.slice().reverse();
  const durations = traces
    .map((t) => traceLatency(t, latencyByRequestID))
    .filter((d): d is number => typeof d === "number" && d >= 0)
    .sort((a, b) => a - b);
  const p50 = percentile(durations, 0.5);
  const p95 = percentile(durations, 0.95);
  const errorCount = traces.filter((t) => t.status_code === "error").length;
  // Recovered = a fallback was used AND the final result wasn't an
  // error. A trace with both status_code="error" and fallback_from
  // means the fallback ALSO failed; that's not a recovery, it's a
  // double-fault, and gets counted only as an error.
  const recoveredCount = traces.filter(
    (t) => t.route?.fallback_from && t.status_code !== "error",
  ).length;

  return (
    <div
      role="group"
      aria-label="Recent activity"
      style={{
        padding: "10px 12px",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        marginBottom: 10,
        display: "flex",
        flexDirection: "column",
        gap: 6,
      }}
    >
      <div style={{ display: "flex", flexWrap: "wrap", gap: 2, alignItems: "center" }}>
        {ordered.map((t) => {
          // Map dot color from raw fields rather than reusing
          // traceStatusBadge — that helper paints missing status_code
          // (in-flight traces) as "degraded"/amber, which would
          // visually conflate them with recovered traces in this
          // strip. Here, in-flight stays gray.
          const color = dotColor(t);
          const label = labelsByRequestID?.get(t.request_id);
          const provider = label?.provider || t.route?.final_provider || "—";
          const model = label?.model || t.route?.final_model || "—";
          const latency = traceLatency(t, latencyByRequestID);
          return (
            <span
              key={t.request_id}
              title={`${t.request_id} · ${provider}/${model}${latency != null ? ` · ${latency}ms` : ""}`}
              style={{
                display: "inline-block",
                width: 6,
                height: 6,
                borderRadius: "50%",
                background: color,
              }}
            />
          );
        })}
      </div>
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: 12,
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          color: "var(--t2)",
        }}
      >
        <span>
          <span style={{ color: "var(--t3)" }}>p50</span> {formatMs(p50)}
        </span>
        <span>
          <span style={{ color: "var(--t3)" }}>p95</span> {formatMs(p95)}
        </span>
        <span>
          <span style={{ color: "var(--t3)" }}>errors</span>{" "}
          <span style={{ color: errorCount > 0 ? "var(--red)" : "var(--t2)" }}>{errorCount}</span> /{" "}
          {traces.length}
        </span>
        {recoveredCount > 0 && (
          <span>
            <span style={{ color: "var(--t3)" }}>recovered</span>{" "}
            <span style={{ color: "var(--amber)" }}>{recoveredCount}</span>
          </span>
        )}
      </div>
    </div>
  );
}

function traceLatency(
  t: TraceListItem,
  latencyByRequestID?: Map<string, number>,
): number | undefined {
  return latencyByRequestID?.get(t.request_id) ?? t.duration_ms;
}

function dotColor(t: TraceListItem): string {
  if (t.status_code === "error") return "var(--red)";
  if (t.route?.fallback_from) return "var(--amber)";
  if (t.status_code === "ok") return "var(--green)";
  return "var(--t3)"; // in-flight or unknown
}

// percentile picks the ceil-indexed value at q on a sorted ascending
// array. Returns NaN for empty input so the formatter can render
// "—" instead of "0ms" (zero is meaningfully different from "no
// duration data" — operators read it as the system being instant).
function percentile(sorted: number[], q: number): number {
  if (sorted.length === 0) return NaN;
  const idx = Math.min(sorted.length - 1, Math.ceil(q * sorted.length) - 1);
  return sorted[Math.max(0, idx)];
}

function formatMs(v: number): string {
  if (!Number.isFinite(v)) return "—";
  if (v >= 1000) return `${(v / 1000).toFixed(1)}s`;
  return `${Math.round(v)}ms`;
}

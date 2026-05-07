import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { getMCPCacheStats, getRecentTraces, getRuntimeStats, getTrace } from "../../lib/api";
import {
  buildSpanWaterfall,
  buildTraceTimeline,
  describeHealthStatus,
  describeRouteCandidateOutcome,
  describeRouteReason,
  describeRouteSkipReason,
  explainRouteCandidate,
  formatRelativeTime,
  formatTraceAttributeKey,
  formatTraceAttributeValue,
  healthStatusTone,
  routeOutcomeTone,
  traceStatusBadge,
  type TraceTimelineItem,
  type WaterfallSpan,
} from "../../lib/runtime-utils";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import type { MCPCacheStatsResponse, RuntimeStatsResponse, TraceListItem, TraceResponse, TraceSpanRecord } from "../../types/runtime";
import { Badge, Icon, Icons, Modal, ModelPicker, ProviderPicker, Toggle } from "../shared/ui";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
  // Optional escape hatch the empty-state "Open Chats" button uses.
  // AppShell wires it to onSelectWorkspace; in tests it's omitted and
  // the button no-ops.
  onNavigate?: (workspace: "chats" | "providers" | "runs" | "overview" | "costs" | "settings") => void;
  focusRequest?: { requestID: string; nonce: number } | null;
};

// 900px chosen because at narrower widths the inline split between
// table and drawer feels too cramped — the table only has so many
// columns it can shed before unreadable. Below that we fall back to
// the centered Modal we used to ship.
const DRAWER_BREAKPOINT_PX = 900;

const PROVIDER_COLORS: Record<string, string> = {
  anthropic:   "var(--brand-anthropic)",
  openai:      "var(--brand-openai)",
  gemini:      "var(--brand-gemini)",
  mistral:     "var(--brand-mistral)",
  groq:        "var(--brand-groq)",
  deepseek:    "var(--teal)",
  perplexity:  "var(--teal)",
  together_ai: "var(--t2)",
  xai:         "var(--t0)",
  ollama:      "var(--teal)",
  lmstudio:    "var(--t2)",
  llamacpp:    "var(--t2)",
  localai:     "var(--t2)",
};

function providerColor(id: string): string {
  return PROVIDER_COLORS[id.toLowerCase()] ?? "var(--teal)";
}

// phaseColor maps the phase classification to an existing token. The
// provider phase additionally pulls the brand color from the span's
// `gen_ai.provider.name` attribute when present so the bar visually
// matches the row in the table.
function phaseColor(phase: TraceTimelineItem["phase"], span?: TraceSpanRecord): string {
  if (phase === "provider") {
    const attr = span?.attributes?.["gen_ai.provider.name"];
    if (typeof attr === "string" && attr) {
      const c = PROVIDER_COLORS[attr.toLowerCase()];
      if (c) return c;
    }
    return "var(--brand-anthropic)";
  }
  switch (phase) {
    case "request":  return "var(--teal)";
    case "routing":  return "var(--amber)";
    case "governor": return "var(--brand-mistral)";
    case "cost":     return "var(--t2)";
    case "usage":    return "var(--t2)";
    case "queue":    return "var(--amber)";
    case "orchestration": return "var(--t2)";
    case "tool":     return "var(--brand-openai)";
    case "approval": return "var(--brand-anthropic)";
    case "artifact": return "var(--green)";
    case "retention": return "var(--t3)";
    case "agent_chat": return "var(--brand-openai)";
    case "response": return "var(--teal)";
    default:         return "var(--t3)";
  }
}

const PHASE_LABEL: Record<TraceTimelineItem["phase"], string> = {
  request: "request", routing: "routing",
  provider: "provider", governor: "governor", usage: "usage",
  cost: "cost", response: "response", queue: "queue",
  orchestration: "orchestration", tool: "tool", approval: "approval",
  artifact: "artifact", retention: "retention", agent_chat: "agent chat",
  other: "other",
};

type StatCardProps = { label: string; value: string | number; sub?: string; highlight?: boolean };
function StatCard({ label, value, sub, highlight }: StatCardProps) {
  return (
    <div className="card" style={{ padding: "12px 14px", minWidth: 110 }}>
      <div className="kicker" style={{ color: "var(--t2)", marginBottom: 6 }}>{label}</div>
      <div style={{ fontSize: 22, fontWeight: 600, fontFamily: "var(--font-mono)", color: highlight ? "var(--amber)" : "var(--t0)", lineHeight: 1 }}>{value}</div>
      {sub && <div style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)", marginTop: 4 }}>{sub}</div>}
    </div>
  );
}

// thStyle mirrors the Providers table header — uppercase 11px t2 with
// the same horizontal padding so the two views share visual chrome.
const thStyle: CSSProperties = {
  padding: "6px 12px",
  textAlign: "left",
  fontSize: 11,
  fontWeight: 500,
  color: "var(--t2)",
  letterSpacing: "0.04em",
  textTransform: "uppercase",
  whiteSpace: "nowrap",
};

const tdBase: CSSProperties = {
  padding: "8px 12px",
  fontSize: 12,
  fontFamily: "var(--font-mono)",
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

type StatusFilter = "all" | "healthy" | "error";

function CopyableID({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={e => {
        e.stopPropagation();
        navigator.clipboard?.writeText(text).catch(() => {});
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      }}
      title={text}
      style={{
        background: "none", border: "none", padding: 0, cursor: "pointer",
        fontFamily: "var(--font-mono)", fontSize: 11,
        color: copied ? "var(--green)" : "var(--teal)",
        display: "inline-flex", alignItems: "center", gap: 4,
        overflow: "hidden", textOverflow: "ellipsis", maxWidth: "100%",
      }}>
      <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{text.slice(0, 8)}…</span>
      <Icon d={copied ? Icons.check : Icons.copy} size={11} />
    </button>
  );
}

export function ObservabilityView({ state, onNavigate, focusRequest }: Props) {
  const [runtimeStats, setRuntimeStats] = useState<RuntimeStatsResponse["data"] | null>(null);
  const [mcpCacheStats, setMCPCacheStats] = useState<MCPCacheStatsResponse["data"] | null>(null);
  const [traces, setTraces] = useState<TraceListItem[]>([]);
  const [liveMode, setLiveMode] = useState(true);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  // drawerOpen is intentionally decoupled from selectedID so live mode
  // can advance the highlight without slamming the drawer open.
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [expandedSpanID, setExpandedSpanID] = useState<string | null>(null);
  const [phaseFilter, setPhaseFilter] = useState<TraceTimelineItem["phase"] | null>(null);
  const [traceDetail, setTraceDetail] = useState<TraceResponse["data"] | null>(null);
  const [traceFetching, setTraceFetching] = useState(false);
  const traceRetryRef = useRef<ReturnType<typeof window.setInterval> | null>(null);

  // Pick the layout mode once on mount. A live resize listener would
  // make the layout reactive but it'd also rip the drawer out from
  // under an operator mid-inspection on a window snap; the tradeoff
  // isn't worth it.
  const [useDrawer] = useState<boolean>(() =>
    typeof window === "undefined" ? true : window.innerWidth >= DRAWER_BREAKPOINT_PX,
  );

  // Filter pickers
  const [providerFilter, setProviderFilter] = useState<string>("auto");
  const [modelFilter, setModelFilter] = useState<string>("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");

  const loadStats = useCallback(async () => {
    try {
      const res = await getRuntimeStats();
      setRuntimeStats(res.data);
    } catch { /* silently ignore */ }
    try {
      const res = await getMCPCacheStats();
      setMCPCacheStats(res.data);
    } catch { /* silently ignore */ }
  }, []);

  const loadTraces = useCallback(async () => {
    try {
      const res = await getRecentTraces(50);
      setTraces(res.data ?? []);
    } catch { /* silently ignore */ }
  }, []);

  useEffect(() => {
    void loadStats();
    const interval = window.setInterval(() => void loadStats(), 10000);
    return () => window.clearInterval(interval);
  }, [loadStats]);

  useEffect(() => {
    void loadTraces();
    const interval = window.setInterval(() => void loadTraces(), 4000);
    return () => window.clearInterval(interval);
  }, [loadTraces]);

  // Filter traces before deriving the live-mode auto-selection so the
  // highlight tracks what the operator is actually looking at.
  const filteredTraces = useMemo(() => {
    return traces.filter(t => {
      if (providerFilter !== "auto" && t.route?.final_provider !== providerFilter) return false;
      if (modelFilter && t.route?.final_model !== modelFilter) return false;
      if (statusFilter === "healthy" && t.status_code === "error") return false;
      if (statusFilter === "error" && t.status_code !== "error") return false;
      return true;
    });
  }, [traces, providerFilter, modelFilter, statusFilter]);

  const ledgerByRequest = useMemo(() => {
    const out = new Map<string, NonNullable<typeof state.requestLedger>[number]>();
    for (const entry of state.requestLedger ?? []) {
      if (entry.request_id) out.set(entry.request_id, entry);
    }
    return out;
  }, [state.requestLedger]);

  // In live mode, auto-highlight the newest visible request. The drawer
  // does NOT auto-open — opens only on explicit click.
  useEffect(() => {
    if (!liveMode || filteredTraces.length === 0) return;
    const newest = filteredTraces[0];
    if (newest?.request_id) {
      setSelectedID(id => id === newest.request_id ? id : newest.request_id);
    }
  }, [liveMode, filteredTraces]);

  const fetchTraceDetail = useCallback((reqId: string) => {
    setTraceFetching(true);
    getTrace(reqId)
      .then(res => setTraceDetail(res.data))
      .catch(() => setTraceDetail(null))
      .finally(() => setTraceFetching(false));
  }, []);

  // Fetch detail when the drawer opens or the selected ID changes
  // while open. Closing cancels the retry timer.
  useEffect(() => {
    if (traceRetryRef.current) {
      window.clearInterval(traceRetryRef.current);
      traceRetryRef.current = null;
    }
    if (!drawerOpen || !selectedID) { setTraceDetail(null); return; }
    setTraceDetail(null);
    fetchTraceDetail(selectedID);
    traceRetryRef.current = window.setInterval(() => {
      setTraceDetail(prev => {
        if (prev?.spans?.length) {
          if (traceRetryRef.current) window.clearInterval(traceRetryRef.current);
          return prev;
        }
        fetchTraceDetail(selectedID);
        return prev;
      });
    }, 2000);
    return () => {
      if (traceRetryRef.current) window.clearInterval(traceRetryRef.current);
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedID, drawerOpen]);

  const stats = runtimeStats;
  const selectedTrace = traces.find(t => t.request_id === selectedID);
  const waterfall = useMemo(
    () => buildSpanWaterfall(traceDetail?.spans ?? []),
    [traceDetail?.spans],
  );
  const traceTimeline = traceDetail?.spans?.length ? buildTraceTimeline(traceDetail.spans, traceDetail.started_at) : [];

  const providerOptions = useMemo(() => {
    const configured = state.settingsConfig?.providers ?? [];
    if (configured.length > 0) {
      return configured.map(c => ({
        id: c.id,
        name: state.providerPresets.find(pr => pr.id === c.id)?.name || c.name || c.id,
        kind: c.kind,
      }));
    }
    return state.providers.filter(p => p.name).map(p => ({
      id: p.name,
      name: state.providerPresets.find(pr => pr.id === p.name)?.name || p.name,
      kind: p.kind,
    }));
  }, [state.settingsConfig, state.providers, state.providerPresets]);

  const drawerTitle = selectedTrace
    ? `${(selectedTrace.request_id || "").slice(0, 8)}… · ${selectedTrace.route?.final_provider || "—"}/${selectedTrace.route?.final_model || "—"}`
    : selectedID ? selectedID.slice(0, 8) + "…" : "";

  const closeDrawer = () => {
    setDrawerOpen(false);
    setSelectedID(null);
    setExpandedSpanID(null);
    setPhaseFilter(null);
  };

  const openTraceForRow = (reqID: string) => {
    setLiveMode(false);
    setSelectedID(reqID);
    setDrawerOpen(true);
    setExpandedSpanID(null);
    setPhaseFilter(null);
  };

  useEffect(() => {
    const requestID = focusRequest?.requestID?.trim();
    if (!requestID) return;
    openTraceForRow(requestID);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusRequest?.nonce]);

  // The top region (header + stats + table) shrinks when the drawer is
  // open. Both regions flex to ~50% with internal scroll.
  const drawerActive = useDrawer && drawerOpen && !!selectedID;

  const detailContent = (selectedID && (
    <TraceDetail
      selectedID={selectedID}
      selectedTrace={selectedTrace}
      ledger={ledgerByRequest.get(selectedID)}
      traceDetail={traceDetail}
      traceFetching={traceFetching}
      waterfall={waterfall}
      traceTimeline={traceTimeline}
      expandedSpanID={expandedSpanID}
      setExpandedSpanID={setExpandedSpanID}
      phaseFilter={phaseFilter}
      setPhaseFilter={setPhaseFilter}
    />
  )) || null;

  return (
    <div style={{ height: "100%", overflow: "hidden", display: "flex", flexDirection: "column" }}>
      <div style={{
        flex: drawerActive ? "1 1 50%" : "1 1 100%",
        minHeight: 0,
        overflowY: "auto",
        padding: 16,
      }}>

        {/* Header */}
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 20 }}>
          <span style={{ fontSize: 14, fontWeight: 500, color: "var(--t0)" }}>Observability</span>

          <div style={{ marginLeft: "auto", display: "flex", alignItems: "center", gap: 8 }}>
            <ProviderPicker
              value={providerFilter}
              onChange={setProviderFilter}
              options={providerOptions}
              includeAuto
            />
            <ModelPicker
              value={modelFilter}
              onChange={setModelFilter}
              models={state.providerScopedModels}
              presets={state.providerPresets}
              showProvider={providerFilter === "auto"}
            />
            <select
              className="input"
              aria-label="Status filter"
              value={statusFilter}
              onChange={e => setStatusFilter(e.target.value as StatusFilter)}
              style={{ fontSize: 11, padding: "4px 8px", height: 28 }}>
              <option value="all">All</option>
              <option value="healthy">Healthy</option>
              <option value="error">Error</option>
            </select>
            <Toggle on={liveMode} onChange={setLiveMode} ariaLabel="Live mode" />
            <span style={{ fontSize: 11, color: liveMode ? "var(--teal)" : "var(--t3)" }}>
              {liveMode ? "Live" : "Paused"}
            </span>
          </div>
        </div>

        {/* Stat strip */}
        {(stats || mcpCacheStats) && (
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap", marginBottom: 20 }} aria-label="Runtime stats">
            {stats && (
              <>
                <StatCard label="queue depth" value={stats.queue_depth} sub={stats.queue_capacity ? `cap ${stats.queue_capacity}` : undefined} highlight={stats.queue_depth > 0} />
                <StatCard label="workers" value={stats.worker_count} />
                <StatCard label="in-flight" value={stats.in_flight_jobs} highlight={stats.in_flight_jobs > 0} />
                <StatCard label="running" value={stats.running_runs} highlight={stats.running_runs > 0} />
                {stats.queued_runs > 0 && <StatCard label="queued" value={stats.queued_runs} highlight />}
                {stats.awaiting_approval_runs > 0 && <StatCard label="awaiting approval" value={stats.awaiting_approval_runs} highlight />}
                {stats.store_backend && <StatCard label="store" value={stats.store_backend} />}
              </>
            )}
            {mcpCacheStats && (
              !mcpCacheStats.configured ? (
                <div className="card" style={{ padding: "12px 14px", display: "flex", alignItems: "center", fontSize: 11, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
                  No MCP cache wired
                </div>
              ) : (
                <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }} aria-label="MCP cache stats">
                  <StatCard label="mcp entries" value={mcpCacheStats.entries} />
                  <StatCard label="mcp in-use" value={mcpCacheStats.in_use} highlight={mcpCacheStats.in_use > 0} />
                  <StatCard label="mcp idle" value={mcpCacheStats.idle} />
                </div>
              )
            )}
          </div>
        )}

        {/* Recent requests label */}
        <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: 10 }}>Recent requests</div>

        {/* Table */}
        {filteredTraces.length > 0 ? (
          <div style={{ border: "1px solid var(--border)", borderRadius: "var(--radius)", overflow: "hidden" }}>
            <table style={{ width: "100%", borderCollapse: "collapse", tableLayout: "fixed" }}>
              <colgroup>
                <col style={{ width: "9%" }} />
                <col style={{ width: "9%" }} />
                <col style={{ width: "12%" }} />
                <col style={{ width: "16%" }} />
                <col style={{ width: "7%" }} />
                <col style={{ width: "8%" }} />
                <col style={{ width: "17%" }} />
                <col style={{ width: "9%" }} />
                <col style={{ width: "9%" }} />
                <col style={{ width: "4%" }} />
              </colgroup>
              <thead>
                <tr style={{ borderBottom: "1px solid var(--border)", background: "var(--bg2)" }}>
                  <th style={thStyle}>Time</th>
                  <th style={thStyle}>Status</th>
                  <th style={thStyle}>Provider</th>
                  <th style={thStyle}>Model</th>
                  <th style={{ ...thStyle, textAlign: "right" }}>Latency</th>
                  <th style={{ ...thStyle, textAlign: "right" }}>Cost</th>
                  <th style={thStyle}>Reason</th>
                  <th style={thStyle}>Fallback</th>
                  <th style={thStyle}>Request ID</th>
                  <th style={thStyle}></th>
                </tr>
              </thead>
              <tbody>
                {filteredTraces.map((t, i) => {
                  const isSel = selectedID === t.request_id;
                  const isLast = i === filteredTraces.length - 1;
                  const status = traceStatusBadge(t);
                  const ledger = ledgerByRequest.get(t.request_id);
                  const cost = ledger?.amount_usd
                    ? `$${Number.parseFloat(ledger.amount_usd).toFixed(5)}`
                    : ledger
                      ? "$0.00000"
                      : "—";
                  const reason = t.route?.final_reason
                    ? describeRouteReason(t.route.final_reason)
                    : t.status_code === "error" && t.status_message
                      ? t.status_message
                      : "—";
                  const time = formatRelativeTime(t.started_at || "");
                  const provider = t.route?.final_provider || "";
                  return (
                    <tr
                      key={t.request_id}
                      onClick={() => openTraceForRow(t.request_id)}
                      style={{
                        cursor: "pointer",
                        background: isSel ? "var(--teal-bg)" : "transparent",
                        borderBottom: isLast ? undefined : "1px solid var(--border)",
                      }}>
                      <td style={{ ...tdBase, color: "var(--t3)" }} title={time.iso}>{time.label}</td>
                      <td style={tdBase}><Badge status={status.status} label={status.label} /></td>
                      <td style={tdBase}>
                        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                          {provider ? (
                            <>
                              <span style={{
                                width: 18, height: 18, borderRadius: "var(--radius-sm)",
                                background: "var(--bg3)", border: "1px solid var(--border)",
                                display: "inline-flex", alignItems: "center", justifyContent: "center",
                                fontFamily: "var(--font-mono)", fontSize: 10, fontWeight: 600,
                                color: providerColor(provider), flexShrink: 0,
                              }}>
                                {provider[0]?.toUpperCase()}
                              </span>
                              <span style={{ fontSize: 12, color: "var(--t1)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{provider}</span>
                            </>
                          ) : <span style={{ color: "var(--t3)" }}>—</span>}
                        </div>
                      </td>
                      <td style={{ ...tdBase, color: "var(--t0)" }}>{t.route?.final_model || "—"}</td>
                      <td style={{ ...tdBase, color: "var(--t1)", textAlign: "right" }}>
                        {t.duration_ms != null ? `${t.duration_ms}ms` : "—"}
                      </td>
                      <td style={{ ...tdBase, color: "var(--t1)", textAlign: "right" }}>{cost}</td>
                      <td style={{ ...tdBase, color: "var(--t2)" }} title={reason}>{reason}</td>
                      <td style={{ ...tdBase, color: t.route?.fallback_from ? "var(--amber)" : "var(--t3)" }}>
                        {t.route?.fallback_from ? `↳ ${t.route.fallback_from}` : "—"}
                      </td>
                      <td style={tdBase} onClick={e => e.stopPropagation()}>
                        <CopyableID text={t.request_id} />
                      </td>
                      <td style={tdBase}></td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <div style={{
            display: "flex", flexDirection: "column", alignItems: "center",
            justifyContent: "center", padding: "48px 16px", textAlign: "center",
          }}>
            <div style={{ fontSize: 14, color: "var(--t1)", fontWeight: 500 }}>No traces yet</div>
            <div style={{ fontSize: 12, color: "var(--t3)", marginTop: 6 }}>
              Send a chat completion to see traces here
            </div>
            <button
              className="btn btn-primary btn-sm"
              style={{ marginTop: 16, display: "flex", alignItems: "center", gap: 4 }}
              onClick={() => onNavigate?.("chats")}>
              <Icon d={Icons.chat} size={13} /> Open Chats
            </button>
          </div>
        )}

      </div>

      {/* Bottom drawer (wide viewports) — inline panel, not portal. */}
      {drawerActive && (
        <div
          role="dialog"
          aria-label={drawerTitle}
          style={{
            flex: "1 1 50%",
            minHeight: 0,
            display: "flex",
            flexDirection: "column",
            borderTop: "1px solid var(--border)",
            background: "var(--bg1)",
          }}>
          <div style={{ padding: "10px 14px", borderBottom: "1px solid var(--border)", background: "var(--bg2)", display: "flex", alignItems: "center", gap: 8 }}>
            <span style={{
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              fontWeight: 500,
              color: "var(--teal)",
              letterSpacing: "0.04em",
              textTransform: "uppercase",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              flex: 1,
            }}>{drawerTitle}</span>
            {selectedID && <CopyableID text={selectedID} />}
            <button
              className="btn btn-ghost btn-sm"
              aria-label="Close"
              title="Close (Esc)"
              onClick={closeDrawer}
              style={{ padding: "3px 6px" }}>
              <Icon d={Icons.x} size={14} />
            </button>
          </div>
          <div style={{ flex: 1, minHeight: 0, overflowY: "auto", padding: 14 }}>
            {detailContent}
          </div>
        </div>
      )}

      {/* Narrow viewport fallback — Modal. */}
      {!useDrawer && drawerOpen && selectedID && (
        <Modal
          title={drawerTitle}
          onClose={closeDrawer}
          footer={null}
          width={760}>
          {detailContent}
        </Modal>
      )}
    </div>
  );
}

// ─── TraceDetail ─────────────────────────────────────────────────────────────

type LedgerEntry = NonNullable<RuntimeConsoleViewModel["state"]["requestLedger"]>[number];

type TraceDetailProps = {
  selectedID: string;
  selectedTrace?: TraceListItem;
  ledger?: LedgerEntry;
  traceDetail: TraceResponse["data"] | null;
  traceFetching: boolean;
  waterfall: ReturnType<typeof buildSpanWaterfall>;
  traceTimeline: ReturnType<typeof buildTraceTimeline>;
  expandedSpanID: string | null;
  setExpandedSpanID: (id: string | null) => void;
  phaseFilter: TraceTimelineItem["phase"] | null;
  setPhaseFilter: (p: TraceTimelineItem["phase"] | null) => void;
};

function TraceDetail({
  selectedTrace, ledger, traceDetail, traceFetching,
  waterfall, traceTimeline, expandedSpanID, setExpandedSpanID,
  phaseFilter, setPhaseFilter,
}: TraceDetailProps) {
  const status = selectedTrace ? traceStatusBadge(selectedTrace) : null;
  const tokens = ledger
    ? `${ledger.prompt_tokens ?? 0} / ${ledger.completion_tokens ?? 0}`
    : "—";
  const cost = ledger?.amount_usd
    ? `$${Number.parseFloat(ledger.amount_usd).toFixed(5)}`
    : ledger ? "$0.00000" : "—";
  const latency = selectedTrace?.duration_ms != null ? `${selectedTrace.duration_ms}ms` : "—";

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
      {/* Live status grid */}
      <div style={{
        display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 12,
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
        <div style={{ padding: "10px 12px", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)" }}>
          <div className="kicker" style={{ marginBottom: 8 }}>Event flow</div>
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
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
                  ["start",    `+${ws.startMs}ms`],
                  ["duration", `${ws.durMs}ms`],
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

// ─── SpanWaterfall ───────────────────────────────────────────────────────────

const ATTR_PRIORITY_KEYS = [
  "provider", "gen_ai.provider.name",
  "model", "gen_ai.request.model", "gen_ai.response.model",
  "status_code", "error",
  "usage.input_tokens", "gen_ai.usage.input_tokens",
  "usage.output_tokens", "gen_ai.usage.output_tokens",
  "route.skip_reason", "route.fallback_from",
];

type SpanWaterfallProps = {
  waterfall: ReturnType<typeof buildSpanWaterfall>;
  expandedSpanID: string | null;
  setExpandedSpanID: (id: string | null) => void;
  phaseFilter: TraceTimelineItem["phase"] | null;
  setPhaseFilter: (p: TraceTimelineItem["phase"] | null) => void;
  traceFetching: boolean;
  hasTraceDetail: boolean;
};

function SpanWaterfall({
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

      {/* Sticky ruler */}
      <div style={{
        position: "sticky", top: 0, zIndex: 1, background: "var(--bg1)",
        display: "grid",
        gridTemplateColumns: "240px 1fr 60px",
        gap: 8,
        padding: "4px 0",
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
                color: "var(--t3)",
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
  const leftPct = (ws.startMs / totalMs) * 100;
  const widthPct = Math.max((ws.durMs / totalMs) * 100, 0.5);
  const color = phaseColor(ws.phase, ws.span);
  const opacity = isDimmed ? 0.3 : 1;
  // Duration label inside the bar when wide enough, otherwise to its right.
  const labelInside = widthPct > 12;

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

        {/* Bar column */}
        <div style={{ position: "relative", height: 14 }}>
          <div
            style={{
              position: "absolute",
              left: `${leftPct}%`,
              width: `${widthPct}%`,
              minWidth: 2,
              height: "100%",
              background: ws.hasError ? "var(--red)" : color,
              borderRadius: 2,
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-end",
              paddingRight: 4,
              boxSizing: "border-box",
            }}>
            {labelInside && (
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t0)" }}>
                {ws.durMs}ms
              </span>
            )}
          </div>
        </div>

        {/* Right-side label */}
        <div style={{
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          color: "var(--t1)",
          textAlign: "right",
          whiteSpace: "nowrap",
        }}>
          {labelInside ? "" : `${ws.durMs}ms`}
          {ws.hasError ? " · CRIT" : ""}
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

// ─── RouteCandidates ─────────────────────────────────────────────────────────

function RouteCandidates({
  traceDetail, selectedTrace,
}: {
  traceDetail: TraceResponse["data"] | null;
  selectedTrace?: TraceListItem;
}) {
  type Candidate = NonNullable<NonNullable<TraceListItem["route"]>["candidates"]>[number];
  const candidates: Candidate[] = traceDetail?.route?.candidates ?? selectedTrace?.route?.candidates ?? [];
  if (candidates.length === 0) return null;
  const selected = candidates.find((c) => c.outcome === "selected" || c.outcome === "completed");
  const skipped = candidates.filter((c) => c.outcome === "skipped").length;
  const failed = candidates.filter((c) => c.outcome === "failed").length;
  return (
    <div style={{ padding: "10px 12px", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)" }}>
      <div className="kicker" style={{ marginBottom: 6 }}>Route decision</div>
      <div style={{ marginBottom: 8, display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}>
        <span style={{ fontSize: 12, color: "var(--t1)" }}>
          {selected?.provider
            ? `Selected ${selected.provider}/${selected.model || "provider default"}`
            : "No provider selected"}
        </span>
        {skipped > 0 && <Badge status="warn" label={`${skipped} skipped`} />}
        {failed > 0 && <Badge status="error" label={`${failed} failed`} />}
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
          {candidates.length} candidate{candidates.length === 1 ? "" : "s"}
        </span>
      </div>
      {candidates.map((c, i) => {
        const outcomeTone = routeOutcomeTone(c.outcome);
        const outcomeStatus =
          outcomeTone === "healthy" ? "done" :
          outcomeTone === "danger" ? "error" :
          outcomeTone === "warning" ? "warn" :
          "disabled";
        const healthTone = healthStatusTone(c.health_status);
        const healthStatus =
          healthTone === "healthy" ? "healthy" :
          healthTone === "danger" ? "error" :
          healthTone === "warning" ? "warn" :
          "disabled";
        return (
          <div key={`${c.provider || "provider"}-${c.model || "model"}-${c.outcome || "candidate"}-${c.index ?? i}`} style={{ padding: "8px 0", borderBottom: i === candidates.length - 1 ? undefined : "1px solid var(--border)" }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t0)", flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {c.provider}/{c.model || "no model"}
              </span>
              <Badge status={outcomeStatus} label={describeRouteCandidateOutcome(c)} />
            </div>
            <div style={{ marginBottom: 6, fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
              {explainRouteCandidate(c)}
            </div>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
              {c.skip_reason && <Badge status="warn" label={describeRouteSkipReason(c.skip_reason)} />}
              {c.health_status && <Badge status={healthStatus} label={describeHealthStatus(c.health_status)} />}
              {c.reason && (
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                  reason <span style={{ color: "var(--t1)" }}>{describeRouteReason(c.reason)}</span>
                </span>
              )}
              {c.latency_ms != null && c.latency_ms > 0 && (
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                  latency <span style={{ color: "var(--t1)" }}>{c.latency_ms}ms</span>
                </span>
              )}
              {c.estimated_usd && (
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                  est <span style={{ color: "var(--t1)" }}>{c.estimated_usd}</span>
                </span>
              )}
            </div>
            {c.detail && (
              <div style={{ marginTop: 6, fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
                {c.detail}
              </div>
            )}
            {(c.policy_rule_id || c.policy_action || c.policy_reason) && (
              <div style={{ marginTop: 6, display: "flex", flexWrap: "wrap", gap: 10 }}>
                {c.policy_rule_id && (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                    rule <span style={{ color: "var(--t1)" }}>{c.policy_rule_id}</span>
                  </span>
                )}
                {c.policy_action && (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                    action <span style={{ color: "var(--t1)" }}>{c.policy_action}</span>
                  </span>
                )}
                {c.policy_reason && (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t2)", fontStyle: "italic" }}>
                    {c.policy_reason}
                  </span>
                )}
              </div>
            )}
            {(c.failover_from || c.failover_to || c.attempt || c.retry_count) && (
              <div style={{ marginTop: 6, display: "flex", flexWrap: "wrap", gap: 10 }}>
                {c.attempt != null && (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                    attempt <span style={{ color: "var(--t1)" }}>{c.attempt}</span>
                  </span>
                )}
                {c.retry_count != null && (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                    retries <span style={{ color: "var(--t1)" }}>{c.retry_count}</span>
                  </span>
                )}
                {c.failover_from && c.failover_to && (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                    failover <span style={{ color: "var(--t1)" }}>{c.failover_from}</span> → <span style={{ color: "var(--t1)" }}>{c.failover_to}</span>
                  </span>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

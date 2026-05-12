// ObservabilityView is the top-level shell for the Observability
// workspace: header + filter chips, runtime stat strip, recent-traces
// table, and the inline drawer (or modal at narrow widths) wired to
// the trace detail. Per-section components live under
// `./observability/`; this file is orchestration only — state, polling
// effects, filter computation, and layout.

import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";

import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import { getMCPCacheStats, getRecentTraces, getRuntimeStats, getTrace } from "../../lib/api";
import {
  buildSpanWaterfall,
  buildTraceTimeline,
  findModelInTrace,
  findProviderInTrace,
  type TraceTimelineItem,
} from "../../lib/runtime-trace";
import {
  describeRouteReason,
  formatRelativeTime,
  traceStatusBadge,
} from "../../lib/runtime-utils";
import type {
  MCPCacheStatsResponse,
  ModelRecord,
  RuntimeStatsResponse,
  TraceListItem,
  TraceResponse,
} from "../../types/runtime";
import { focusDropdownItem, focusInitialDropdownItem } from "../shared/dropdownKeyboard";
import { useFloatingDropdownStyle } from "../shared/useFloatingDropdownStyle";
import { Badge, BrandAvatar, Icon, Icons, Modal, ModelPicker, ProviderPicker, Toggle } from "../shared/ui";

import { CopyableID } from "./observability/CopyableID";
import { RecentActivityStrip } from "./observability/RecentActivityStrip";
import { StatCard } from "./observability/StatCard";
import { TraceDetail } from "./observability/TraceDetail";
import {
  DRAWER_BREAKPOINT_PX,
  type StatusFilter,
  tdBase,
  thStyle,
} from "./observability/styles";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
  // Optional escape hatch the empty-state "Open Chats" button uses.
  // AppShell wires it to onSelectWorkspace; in tests it's omitted and
  // the button no-ops.
  onNavigate?: (workspace: "chats" | "providers" | "runs" | "overview" | "costs" | "settings") => void;
  focusRequest?: { requestID: string; nonce: number } | null;
};

type TraceGroup = {
  entry: TraceListItem;
  siblings: number;
  earliestMs?: number;
  latestMs?: number;
  latencyMs?: number;
};

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
  // Trace-detail polling fires every 2s while waiting for spans. We cap
  // attempts so a trace that's been retention-pruned, dropped by the OTel
  // buffer, or otherwise will-never-arrive doesn't poll forever — at one
  // request per 2s it's not load-bearing for the gateway, but it produces
  // a steady drip of 200-with-empty-spans responses an operator can't
  // see and can't stop. We mirror traceDetail to a ref so the interval
  // callback can read the latest value without smuggling side effects
  // through a state updater (which StrictMode double-invokes for
  // debugging — the previous shape over-counted attempts in dev).
  const traceRetryAttemptsRef = useRef(0);
  const traceDetailRef = useRef<TraceResponse["data"] | null>(null);
  // Counts retries only — the initial fetch when the drawer opens
  // is separate. Total network calls in the worst case: 1 initial
  // + TRACE_RETRY_LIMIT retries. With TRACE_RETRY_LIMIT=5 that's 6
  // fetches total, the last firing ~10s after the drawer opened.
  const TRACE_RETRY_LIMIT = 5;

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

  const ledgerByRequest = useMemo(() => {
    const out = new Map<string, NonNullable<typeof state.requestLedger>[number]>();
    for (const entry of state.requestLedger ?? []) {
      if (entry.request_id) out.set(entry.request_id, entry);
    }
    return out;
  }, [state.requestLedger]);

  const traceProvider = useCallback(
    (trace: TraceListItem) => trace.route?.final_provider
      || (traceDetail?.request_id === trace.request_id ? findProviderInTrace(traceDetail.spans ?? []) : "")
      || ledgerByRequest.get(trace.request_id)?.provider
      || "",
    [ledgerByRequest, traceDetail],
  );
  const traceModel = useCallback(
    (trace: TraceListItem) => trace.route?.final_model
      || (traceDetail?.request_id === trace.request_id ? findModelInTrace(traceDetail.spans ?? [], traceProvider(trace)) : "")
      || ledgerByRequest.get(trace.request_id)?.model
      || "",
    [ledgerByRequest, traceDetail, traceProvider],
  );

  // Filter traces before deriving the live-mode auto-selection so the
  // highlight tracks what the operator is actually looking at.
  const filteredTraces = useMemo(() => {
    return traces.filter(t => {
      if (providerFilter !== "auto" && traceProvider(t) !== providerFilter) return false;
      if (modelFilter && !modelMatchesFilter(traceModel(t), modelFilter)) return false;
      if (statusFilter === "healthy" && t.status_code === "error") return false;
      if (statusFilter === "error" && t.status_code !== "error") return false;
      return true;
    });
  }, [traces, providerFilter, modelFilter, statusFilter, traceProvider, traceModel]);

  // One user-facing request can fan out into multiple internal traces
  // (route attempts, provider call, auxiliary calls — all sharing
  // request_id but with distinct trace_id). Showing each trace as its
  // own row gives the operator five visually-identical rows for one
  // chat send. Collapse to one row per request_id, keeping the most
  // span-rich entry as the representative (usually the provider
  // call), and surface the sibling count as a "+N" badge so the
  // operator can tell when a request had multiple traces. The detail
  // panel still operates on the representative trace — drill-down to
  // sibling traces is a separate feature.
  const groupedTraces = useMemo(() => {
    const statusRank = (code?: string) => code === "error" ? 2 : code === "ok" ? 1 : 0;
    // A trace whose `route.final_provider` is set carries the actual
    // routing context (the provider that ran the call). A sibling
    // with empty route info is usually a route-attempt sub-trace that
    // didn't actually serve the request. When several traces share a
    // request_id, prefer the route-carrying one as the representative
    // — otherwise the drawer header shows "request" or "—/—" even
    // though we know which provider handled the chat.
    const hasRoute = (t: typeof filteredTraces[number]) =>
      !!(traceProvider(t) || traceModel(t));
    const byID = new Map<string, TraceGroup>();
    for (const t of filteredTraces) {
      const existing = byID.get(t.request_id);
      if (!existing) {
        byID.set(t.request_id, applyTraceWindow({ entry: t, siblings: 0 }, t));
        continue;
      }
      existing.siblings += 1;
      applyTraceWindow(existing, t);
      // Decision order: route info > span count > status (errors win).
      const incomingHasRoute = hasRoute(t);
      const haveHasRoute = hasRoute(existing.entry);
      if (incomingHasRoute && !haveHasRoute) {
        existing.entry = t;
        continue;
      }
      if (!incomingHasRoute && haveHasRoute) {
        continue;
      }
      const incoming = t.span_count ?? 0;
      const have = existing.entry.span_count ?? 0;
      if (incoming > have || (incoming === have && statusRank(t.status_code) > statusRank(existing.entry.status_code))) {
        existing.entry = t;
      }
    }
    return Array.from(byID.values()).sort((a, b) => {
      const ta = a.entry.started_at ? Date.parse(a.entry.started_at) : 0;
      const tb = b.entry.started_at ? Date.parse(b.entry.started_at) : 0;
      return tb - ta;
    });
  }, [filteredTraces, traceProvider, traceModel]);

  const traceLabelsByRequestID = useMemo(() => {
    const out = new Map<string, { provider?: string; model?: string }>();
    for (const group of groupedTraces) {
      const provider = traceProvider(group.entry);
      const model = traceModel(group.entry);
      if (provider || model) {
        out.set(group.entry.request_id, { provider, model });
      }
    }
    return out;
  }, [groupedTraces, traceProvider, traceModel]);
  const traceLatencyByRequestID = useMemo(() => {
    const out = new Map<string, number>();
    for (const group of groupedTraces) {
      if (typeof group.latencyMs === "number") {
        out.set(group.entry.request_id, group.latencyMs);
      }
    }
    return out;
  }, [groupedTraces]);

  // In live mode, auto-highlight the newest visible request. The drawer
  // does NOT auto-open — opens only on explicit click. Track by grouped
  // request_id so the highlight matches what's actually visible in the
  // table after dedup.
  useEffect(() => {
    if (!liveMode || groupedTraces.length === 0) return;
    const newest = groupedTraces[0]?.entry;
    if (newest?.request_id) {
      setSelectedID(id => id === newest.request_id ? id : newest.request_id);
    }
  }, [liveMode, groupedTraces]);

  const fetchTraceDetail = useCallback((reqId: string) => {
    setTraceFetching(true);
    getTrace(reqId)
      .then(res => {
        traceDetailRef.current = res.data;
        setTraceDetail(res.data);
      })
      .catch(() => {
        traceDetailRef.current = null;
        setTraceDetail(null);
      })
      .finally(() => setTraceFetching(false));
  }, []);

  // Fetch detail when the drawer opens or the selected ID changes
  // while open. Closing cancels the retry timer.
  useEffect(() => {
    if (traceRetryRef.current) {
      window.clearInterval(traceRetryRef.current);
      traceRetryRef.current = null;
    }
    if (!drawerOpen || !selectedID) {
      traceDetailRef.current = null;
      setTraceDetail(null);
      return;
    }
    traceDetailRef.current = null;
    setTraceDetail(null);
    traceRetryAttemptsRef.current = 0;
    fetchTraceDetail(selectedID);
    traceRetryRef.current = window.setInterval(() => {
      // Side effects live outside any state updater so StrictMode's
      // double-invoke doesn't double-count attempts. Reads traceDetail
      // via the ref so we don't depend on closing over stale state.
      const current = traceDetailRef.current;
      if (current?.spans?.length) {
        if (traceRetryRef.current) {
          window.clearInterval(traceRetryRef.current);
          traceRetryRef.current = null;
        }
        return;
      }
      if (traceRetryAttemptsRef.current >= TRACE_RETRY_LIMIT) {
        if (traceRetryRef.current) {
          window.clearInterval(traceRetryRef.current);
          traceRetryRef.current = null;
        }
        return;
      }
      traceRetryAttemptsRef.current += 1;
      fetchTraceDetail(selectedID);
    }, 2000);
    return () => {
      if (traceRetryRef.current) window.clearInterval(traceRetryRef.current);
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedID, drawerOpen]);

  const stats = normalizeRuntimeStats(runtimeStats);
  // Resolve via groupedTraces so the drawer header/stats mirror the
  // representative entry the table actually chose for the row. Falling
  // back to the raw traces array would return the first match — usually
  // a different sibling than the one shown in the row, so the table
  // row and the drawer header would disagree on latency/status.
  const selectedTrace = groupedTraces.find(g => g.entry.request_id === selectedID)?.entry
    ?? traces.find(t => t.request_id === selectedID);
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

  const modelOptions = useMemo<ModelRecord[]>(() => {
    const byID = new Map<string, ModelRecord>();
    const add = (id?: string, provider?: string) => {
      const trimmed = id?.trim();
      if (!trimmed) return;
      if (providerFilter !== "auto" && provider && provider !== providerFilter) return;
      byID.set(trimmed, {
        id: trimmed,
        owned_by: provider || "observed",
        metadata: provider ? { provider } : undefined,
      });
    };

    for (const trace of traces) {
      add(traceModel(trace), traceProvider(trace));
    }
    for (const entry of state.requestLedger ?? []) {
      add(entry.model, entry.provider);
    }
    for (const model of state.providerScopedModels) {
      add(model.id, model.metadata?.provider);
    }

    return Array.from(byID.values()).sort((a, b) => a.id.localeCompare(b.id));
  }, [providerFilter, state.providerScopedModels, state.requestLedger, traceModel, traceProvider, traces]);

  const drawerTitle = (() => {
    if (!selectedTrace) return selectedID ?? "";
    const prov = traceProvider(selectedTrace);
    const model = traceModel(selectedTrace);
    if (prov || model) return `${prov || "—"}/${model || "—"}`;
    // No provider was selected — show the rejected candidate (if any)
    // and label the trace as a route-only attempt so the dash-pair
    // header doesn't read as missing data. Detailed candidate
    // breakdown still lives in the Route Summary section below.
    // `provider` is optional on the candidate type, so fall back to a
    // dash rather than letting "undefined" reach the rendered header.
    const rejected = selectedTrace.route?.candidates?.find(c => c.outcome === "skipped");
    if (rejected) return `No provider selected (tried ${rejected.provider || "—"})`;
    return "Request";
  })();
  const drawerDialogLabel = selectedID ? `${drawerTitle} · Request ${selectedID}` : drawerTitle;

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
            <Toggle on={liveMode} onChange={setLiveMode} ariaLabel="Live mode" />
            <span style={{ fontSize: 11, color: liveMode ? "var(--teal)" : "var(--t3)", width: 42, display: "inline-block" }}>
              {liveMode ? "Live" : "Paused"}
            </span>
          </div>
        </div>

        {/* Runtime health cards */}
        {(stats || mcpCacheStats) && (
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap", marginBottom: 20 }} aria-label="Runtime stats">
            {stats && (
              <>
                <StatCard
                  label="Queue depth"
                  value={stats.queue_depth > 0 ? stats.queue_depth : "Idle"}
                  sub={stats.queue_capacity ? `capacity ${stats.queue_capacity}` : undefined}
                  help="Tasks waiting to be claimed by a task runner."
                  highlight={stats.queue_depth > 0}
                  status={stats.queue_depth > 0 ? "active" : "idle"}
                />
                <StatCard
                  label="Task runners"
                  value={stats.worker_count}
                  sub={stats.worker_count === 1 ? "slot available" : "slots available"}
                  help="Configured runner slots that can claim queued tasks."
                />
                <StatCard
                  label="Active jobs"
                  value={stats.in_flight_jobs > 0 ? stats.in_flight_jobs : "Idle"}
                  help="Task jobs currently claimed by runner slots."
                  highlight={stats.in_flight_jobs > 0}
                  status={stats.in_flight_jobs > 0 ? "active" : "idle"}
                />
                <StatCard
                  label="Running task runs"
                  value={stats.running_runs > 0 ? stats.running_runs : "Idle"}
                  help="Task runs currently executing."
                  highlight={stats.running_runs > 0}
                  status={stats.running_runs > 0 ? "active" : "idle"}
                />
                <StatCard
                  label="Queued runs"
                  value={stats.queued_runs > 0 ? stats.queued_runs : "None"}
                  help="Runs persisted as queued but not yet executing."
                  highlight={stats.queued_runs > 0}
                />
                <StatCard
                  label="Awaiting approval"
                  value={stats.awaiting_approval_runs > 0 ? stats.awaiting_approval_runs : "None"}
                  help="Runs paused on an operator approval gate."
                  highlight={stats.awaiting_approval_runs > 0}
                />
                {stats?.store_backend && (
                  <StatCard
                    label="Runtime store"
                    value={describeStoreBackend(stats.store_backend)}
                    help="Persistence backend for runtime state."
                  />
                )}
              </>
            )}
            {mcpCacheStats && (
              mcpCacheStats.configured ? (
                <StatCard
                  label="MCP cache"
                  value={`${mcpCacheStats.entries} entries`}
                  sub={`${mcpCacheStats.in_use} active · ${mcpCacheStats.idle} idle`}
                  help="Cached MCP clients available for reuse."
                  highlight={mcpCacheStats.in_use > 0}
                />
              ) : (
                <StatCard
                  label="MCP cache"
                  value="Disabled"
                  help="No MCP cache is configured for this gateway."
                />
              )
            )}
          </div>
        )}

        {/* Recent requests filters */}
        <div style={{
          display: "flex",
          alignItems: "center",
          gap: 10,
          justifyContent: "space-between",
          marginBottom: 10,
        }}>
          <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>Recent requests</div>
          <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap", justifyContent: "flex-end" }}>
            <ProviderPicker
              value={providerFilter}
              onChange={(value) => {
                setProviderFilter(value);
                setModelFilter("");
              }}
              options={providerOptions}
              includeAuto
            />
            <ModelPicker
              value={modelFilter}
              onChange={setModelFilter}
              models={modelOptions}
              presets={state.providerPresets}
              showProvider={providerFilter === "auto"}
              includeAll
              triggerWidth={220}
            />
            <StatusFilterPicker value={statusFilter} onChange={setStatusFilter} />
          </div>
        </div>

        {/* Activity strip — status dots + p50/p95/errors over the
            visible traces. Sits above the table so the operator
            gets a "is this OK right now?" answer before parsing
            individual rows. */}
        <RecentActivityStrip
          traces={groupedTraces.map(g => g.entry)}
          labelsByRequestID={traceLabelsByRequestID}
          latencyByRequestID={traceLatencyByRequestID}
        />

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
                <col style={{ width: "20%" }} />
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
                  <th style={thStyle}>Route</th>
                  <th style={thStyle}>Request ID</th>
                  <th style={thStyle}></th>
                </tr>
              </thead>
              <tbody>
                {groupedTraces.map((group, i) => {
                  const t = group.entry;
                  const isSel = selectedID === t.request_id;
                  const isLast = i === groupedTraces.length - 1;
                  const status = traceStatusBadge(t);
                  const ledger = ledgerByRequest.get(t.request_id);
                  const cost = ledger?.amount_usd
                    ? `$${Number.parseFloat(ledger.amount_usd).toFixed(5)}`
                    : ledger
                      ? "$0.00000"
                      : "—";
                  const routeLabel = routeSummaryLabel(t);
                  const time = formatRelativeTime(t.started_at || "");
                  const provider = traceProvider(t);
                  const model = traceModel(t);
                  const latency = group.latencyMs ?? t.duration_ms;
                  // When the router skipped every candidate, the
                  // provider/model cells would otherwise just read "—".
                  // Show the rejected candidate (muted) with a tooltip
                  // so the operator can tell at-a-glance that the
                  // request DID attempt routing — the request didn't
                  // simply have no provider/model context. Require at
                  // least a provider name on the candidate: the runtime
                  // type marks both provider and model as optional, so
                  // a half-populated entry shouldn't render as "tried
                  // (empty)".
                  const rejected = !provider && !model
                    ? t.route?.candidates?.find(c => c.outcome === "skipped" && !!c.provider)
                    : undefined;
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
                              <BrandAvatar brand={provider} fallback={provider} size={18} />
                              <span style={{ fontSize: 12, color: "var(--t1)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{provider}</span>
                            </>
                          ) : rejected ? (
                            <span
                              style={{ fontSize: 12, color: "var(--t3)", fontStyle: "italic", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                              title={`No candidate accepted. Tried: ${rejected.provider}${rejected.skip_reason ? ` — ${rejected.skip_reason.replace(/_/g, " ")}` : ""}`}>
                              {rejected.provider}
                            </span>
                          ) : <span style={{ color: "var(--t3)" }}>—</span>}
                        </div>
                      </td>
                      <td style={{ ...tdBase, color: "var(--t0)" }}>
                        {model
                          ? model
                          : rejected?.model
                            ? <span style={{ color: "var(--t3)", fontStyle: "italic" }} title="route skipped — see Provider tooltip">{rejected.model}</span>
                            : "—"}
                      </td>
                      <td style={{ ...tdBase, color: "var(--t1)", textAlign: "right" }}>
                        {latency != null ? `${latency}ms` : "—"}
                      </td>
                      <td style={{ ...tdBase, color: "var(--t1)", textAlign: "right" }}>{cost}</td>
                      <td
                        style={{
                          ...tdBase,
                          color: routeLabel.tone === "fallback" ? "var(--amber)" : routeLabel.tone === "empty" ? "var(--t3)" : "var(--t2)",
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                        }}
                        title={routeLabel.title}>
                        {routeLabel.label}
                      </td>
                      <td style={tdBase} onClick={e => e.stopPropagation()}>
                        <div style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
                          <CopyableID text={t.request_id} compact />
                          {group.siblings > 0 && (
                            <span
                              title={`This request produced ${group.siblings + 1} traces; showing the one with the most spans.`}
                              style={{
                                fontFamily: "var(--font-mono)", fontSize: 10,
                                color: "var(--t3)",
                                background: "var(--bg3)",
                                border: "1px solid var(--border)",
                                borderRadius: "var(--radius-sm)",
                                padding: "0 4px",
                                lineHeight: 1.4,
                              }}>+{group.siblings}</span>
                          )}
                        </div>
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
          aria-label={drawerDialogLabel}
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
          ariaLabel={drawerDialogLabel}
          onClose={closeDrawer}
          footer={null}
          width={760}>
          {detailContent}
        </Modal>
      )}
    </div>
  );
}

const STATUS_FILTER_OPTIONS: Array<{ value: StatusFilter; label: string }> = [
  { value: "all", label: "All" },
  { value: "healthy", label: "Healthy" },
  { value: "error", label: "Error" },
];

function StatusFilterPicker({ value, onChange }: { value: StatusFilter; onChange: (value: StatusFilter) => void }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const floatingStyle = useFloatingDropdownStyle(triggerRef, open, "right");
  const label = STATUS_FILTER_OPTIONS.find((option) => option.value === value)?.label ?? "All";

  useEffect(() => {
    const handler = (event: MouseEvent) => {
      const target = event.target as Node;
      if (ref.current && ref.current.contains(target)) return;
      if (target instanceof HTMLElement && target.closest(".dropdown-menu-floating")) return;
      setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, []);

  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => focusInitialDropdownItem(menuRef.current));
    return () => cancelAnimationFrame(frame);
  }, [open]);

  function closeMenu() {
    setOpen(false);
    triggerRef.current?.focus();
  }

  function onMenuKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      closeMenu();
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp" || event.key === "Home" || event.key === "End") {
      event.preventDefault();
      focusDropdownItem(menuRef.current, event.key);
    }
  }

  return (
    <div className="dropdown-wrap" ref={ref}>
      <button
        ref={triggerRef}
        type="button"
        aria-label={`Status filter: ${label}`}
        aria-expanded={open}
        aria-haspopup="listbox"
        className="btn btn-ghost btn-sm"
        onClick={() => setOpen((current) => !current)}
        style={{ fontFamily: "var(--font-mono)", fontSize: 11, gap: 5, color: "var(--t1)", width: 110 }}>
        <span style={{ flex: 1, minWidth: 0, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", textAlign: "left" }}>
          {label}
        </span>
        <Icon d={Icons.chevD} size={11} />
      </button>
      {open && floatingStyle && (
        <div
          ref={menuRef}
          role="listbox"
          className="dropdown-menu dropdown-menu-floating"
          onKeyDown={onMenuKeyDown}
          style={{ ...floatingStyle, minWidth: 110 }}>
          {STATUS_FILTER_OPTIONS.map((option) => (
            <button
              key={option.value}
              type="button"
              data-dropdown-item
              data-selected={value === option.value ? "true" : undefined}
              role="option"
              aria-selected={value === option.value}
              className={`dropdown-item ${value === option.value ? "selected" : ""}`}
              onClick={() => {
                onChange(option.value);
                closeMenu();
              }}>
              <span style={{ flex: 1, fontFamily: "var(--font-mono)", fontSize: 12, textAlign: "left" }}>
                {option.label}
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function normalizeRuntimeStats(stats: RuntimeStatsResponse["data"] | null): RuntimeStatsResponse["data"] | null {
  if (!stats || typeof stats.queue_depth !== "number" || typeof stats.worker_count !== "number") {
    return null;
  }
  return stats;
}

function applyTraceWindow(
  group: TraceGroup,
  trace: TraceListItem,
): TraceGroup {
  if (typeof trace.duration_ms !== "number" || trace.duration_ms < 0) {
    return group;
  }
  const startMs = trace.started_at ? Date.parse(trace.started_at) : NaN;
  if (!Number.isFinite(startMs)) {
    group.latencyMs ??= trace.duration_ms;
    return group;
  }

  const endMs = startMs + trace.duration_ms;
  group.earliestMs = group.earliestMs == null ? startMs : Math.min(group.earliestMs, startMs);
  group.latestMs = group.latestMs == null ? endMs : Math.max(group.latestMs, endMs);
  group.latencyMs = Math.max(0, Math.round(group.latestMs - group.earliestMs));
  return group;
}

function routeSummaryLabel(trace: TraceListItem): { label: string; title: string; tone: "default" | "fallback" | "empty" } {
  if (trace.route?.fallback_from) {
    const label = `Fallback from ${trace.route.fallback_from}`;
    return {
      label,
      title: trace.route.final_reason
        ? `${label}; ${describeRouteReason(trace.route.final_reason)}`
        : label,
      tone: "fallback",
    };
  }
  if (trace.route?.final_reason) {
    const label = describeRouteReason(trace.route.final_reason);
    return { label, title: label, tone: "default" };
  }
  if (trace.status_code === "error" && trace.status_message) {
    return { label: trace.status_message, title: trace.status_message, tone: "default" };
  }
  return { label: "No route", title: "No route summary was recorded for this request.", tone: "empty" };
}

function modelMatchesFilter(model: string, filter: string): boolean {
  const normalizedModel = normalizeModelFilterValue(model);
  const normalizedFilter = normalizeModelFilterValue(filter);
  if (!normalizedModel || !normalizedFilter) return false;
  if (normalizedModel === normalizedFilter) return true;

  // Some inventories carry provider-qualified IDs while traces usually
  // record the served model name. Keep filtering forgiving so choosing
  // "ollama/ministral-3:latest" still matches a trace whose final_model
  // is "ministral-3:latest".
  const suffixes = [
    `/${normalizedModel}`,
    `::${normalizedModel}`,
    `:${normalizedModel}`,
    ` ${normalizedModel}`,
  ];
  return suffixes.some((suffix) => normalizedFilter.endsWith(suffix));
}

function normalizeModelFilterValue(value: string): string {
  return value.trim().toLowerCase();
}

function describeStoreBackend(backend: string): string {
  return backend === "memory" ? "Memory store" : backend;
}

import { useEffect, useState, type CSSProperties } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import { providerFleetRepairHint, providerReadinessMeaning, providerRepairActionLabel } from "../../lib/provider-readiness";
import { resolvedBaseURL } from "../../lib/provider-utils";
import { describeHealthErrorClass, describeRoutingBlockedReason } from "../../lib/runtime-utils";
import { ProviderReadinessChecklist, ProviderReadinessSummary } from "../shared/ProviderReadiness";
import { Badge, BrandAvatar, ConfirmModal, Icon, Icons, Modal } from "../shared/ui";
import { ConnectionsPanel } from "../connections/ConnectionsPanel";
import { AddProviderModal } from "./AddProviderModal";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
};

const PROVIDER_POLL_INTERVAL_MS = 30_000;

// Health is the runtime status only — circuit state, last probe outcome.
// Credentials are tracked in a separate column so a freshly-saved key
// doesn't have to wait for the next probe to flip the cell.
function resolveHealthBadge(
  rt: RuntimeConsoleViewModel["state"]["providers"][number] | undefined,
  credentialConfigured: boolean,
): { status: string; label: string } {
  if (!rt) return { status: "disabled", label: "Pending" };
  const status = rt.status;
  if (status === "open" || status === "unhealthy") return { status: "down", label: "Down" };
  if (status === "degraded" || status === "half_open") return { status: "degraded", label: "Degraded" };
  // routing_ready=false on an otherwise-healthy provider means something
  // upstream of routing isn't satisfied — usually no_models, sometimes
  // credential_missing on the very first poll. Surface the specific reason
  // instead of the generic "Blocked" label so the operator doesn't have
  // to open the detail panel to find out why.
  //
  // Suppress credential_missing when the CP store already has the key —
  // the runtime's blocked-reason lags by one health probe, so without this
  // the Health cell contradicts the Credentials cell for ~30s after a
  // save. Once the next probe runs, routing_ready flips to true on its own.
  if (rt.routing_ready === false) {
    if (rt.routing_blocked_reason === "credential_missing" && credentialConfigured) {
      return { status: "healthy", label: "Pending probe" };
    }
    return { status: "degraded", label: describeRoutingBlockedReason(rt.routing_blocked_reason) };
  }
  if (status === "healthy" || rt.healthy) return { status: "healthy", label: "Healthy" };
  return { status: "disabled", label: status || "Unknown" };
}

// Credentials reads from the CP store (cp.credential_configured), not the
// runtime status — the store is the source of truth and updates instantly
// after a save, while the runtime's credential_state field lags one cycle.
function resolveCredentialBadge(
  kind: string,
  cp: { credential_configured?: boolean; credential_source?: string } | undefined,
): { status: string; label: string } {
  if (kind === "local") return { status: "ok", label: "Not required" };
  if (cp?.credential_configured) {
    return { status: "ok", label: cp.credential_source === "env" ? "From env" : "Configured" };
  }
  return { status: "warn", label: "Missing" };
}

export function ProvidersView({ state, actions }: Props) {
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [pendingKey, setPendingKey] = useState("");
  const [pendingURL, setPendingURL] = useState("");
  const [pendingName, setPendingName] = useState("");
  const [pendingCustomName, setPendingCustomName] = useState("");
  const [addProviderOpen, setAddProviderOpen] = useState(false);
  const [deleteConfirmID, setDeleteConfirmID] = useState<string | null>(null);

  // Auto-poll model discovery only when there's something to discover for —
  // either at least one configured provider, or the Add modal is open
  // (so a freshly-added provider's models surface immediately). Otherwise
  // /hecate/v1/providers/status + /v1/models are no-op network calls; skip them.
  const hasProviders = (state.settingsConfig?.providers?.length ?? 0) > 0;
  const shouldPoll = hasProviders || addProviderOpen;
  useEffect(() => {
    if (!shouldPoll) return;
    const id = setInterval(() => {
      void actions.refreshProviders();
    }, PROVIDER_POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [actions.refreshProviders, shouldPoll]);

  // Reset pending key when the selected provider changes.
  useEffect(() => {
    setPendingKey("");
  }, [selectedID]);

  // Reset pendingURL and pendingName when selected provider changes.
  useEffect(() => {
    if (selectedID && selectedConfig) {
      setPendingURL(resolvedBaseURL(selectedID, selectedConfig ?? undefined, state.providerPresets));
      setPendingName(selectedConfig.name || selectedID);
      setPendingCustomName(selectedConfig.custom_name ?? "");
    } else {
      setPendingURL("");
      setPendingName("");
      setPendingCustomName("");
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedID]);

  // Dedupe by id at the source. The backend rejects duplicate IDs at
  // create time, so this should already be unique — but a stale fetch
  // overlapping a create can briefly surface the same id twice in
  // settingsConfig.providers, and React's "same key" warning fires before
  // the next render reconciles. First write wins.
  const configuredProviders = (() => {
    const seen = new Set<string>();
    const out: NonNullable<typeof state.settingsConfig>["providers"] = [];
    for (const p of state.settingsConfig?.providers ?? []) {
      if (seen.has(p.id)) continue;
      seen.add(p.id);
      out.push(p);
    }
    return out;
  })();
  const statusByName = new Map(state.providers.map(p => [p.name, p]));
  const configuredByID = new Map(configuredProviders.map(p => [p.id, p]));

  const presetOrder = new Map(state.providerPresets.map((p, i) => [p.id, i]));
  const stableSort = (a: string, b: string) => {
    const ai = presetOrder.get(a) ?? 999;
    const bi = presetOrder.get(b) ?? 999;
    return ai !== bi ? ai - bi : a.localeCompare(b);
  };

  const cloudIDs = configuredProviders.filter(p => p.kind === "cloud").map(p => p.id).sort(stableSort);
  const localIDs = configuredProviders.filter(p => p.kind !== "cloud").map(p => p.id).sort(stableSort);
  const readyProviderCount = configuredProviders.filter(provider => isProviderRoutingReady(provider, statusByName.get(provider.id))).length;
  const blockedProviderCount = configuredProviders.filter(provider => isProviderNeedsAttention(provider, statusByName.get(provider.id))).length;
  const discoveredModelCount = state.models.length || configuredProviders.reduce((sum, provider) => {
    const status = statusByName.get(provider.id);
    return sum + (status?.model_count ?? status?.models?.length ?? 0);
  }, 0);
  const fleetRepair = providerFleetRepairHint(configuredProviders, statusByName);
  const nextReadinessStep = resolveNextReadinessStep(fleetRepair);
  const readinessMeaning = providerReadinessMeaning({
    configuredCount: configuredProviders.length,
    readyCount: readyProviderCount,
    blockedCount: blockedProviderCount,
    modelCount: discoveredModelCount,
    repair: fleetRepair,
  });
  const readinessAction = providerRepairButton(fleetRepair);
  const runReadinessAction = () => {
    if (!fleetRepair) return;
    switch (fleetRepair.actionKind) {
      case "add_provider":
        setAddProviderOpen(true);
        break;
      case "open_provider":
        if (fleetRepair.providerID) setSelectedID(fleetRepair.providerID);
        break;
      case "refresh_providers":
        void actions.refreshProviders();
        break;
      case "none":
        break;
    }
  };

  const selectedConfig = selectedID ? configuredByID.get(selectedID) ?? null : null;
  const selectedStatus = selectedID ? statusByName.get(selectedID) : null;
  const selectedPreset = selectedID ? state.providerPresets.find(p => p.id === selectedID) : null;
  const deleteConfirmConfig = deleteConfirmID ? configuredByID.get(deleteConfirmID) ?? null : null;
  const deleteConfirmPreset = deleteConfirmID ? state.providerPresets.find(p => p.id === deleteConfirmID) : null;
  const deleteConfirmName = deleteConfirmConfig
    ? deleteConfirmPreset?.name || deleteConfirmConfig.name || deleteConfirmID || "provider"
    : "provider";

  const selectedHealthCounters = [
    selectedStatus?.consecutive_failures ? `${selectedStatus.consecutive_failures} consecutive failures` : "",
    selectedStatus?.rate_limits ? `${selectedStatus.rate_limits} rate limits` : "",
    selectedStatus?.timeouts ? `${selectedStatus.timeouts} timeouts` : "",
    selectedStatus?.server_errors ? `${selectedStatus.server_errors} server errors` : "",
  ].filter(Boolean).join(" · ");

  // ── Table row renderer ───────────────────────────────────────────────────────

  function renderRow(id: string, isLast = false) {
    const cp = configuredByID.get(id);
    const rt = statusByName.get(id);
    const preset = state.providerPresets.find(p => p.id === id);
    const displayName = preset?.name || cp?.name || id;
    const baseURL = rt?.base_url || resolvedBaseURL(id, cp ?? undefined, state.providerPresets);
    const modelCount = rt?.model_count ?? rt?.models?.length ?? 0;
    const modelBadge = modelCount > 0
      ? null
      : cp && !rt
        ? { status: "degraded", label: "Discovery pending" }
        : { status: "degraded", label: "No models" };
    const protocol = cp?.protocol || preset?.protocol || "—";
    const isSelected = selectedID === id;

    // Credentials: CP store is the source of truth (the secret was just saved
    // there); runtime status lags by one health-check cycle so we don't read it.
    const cred = resolveCredentialBadge(cp?.kind ?? "cloud", cp);
    // Health passes the CP credential state in so it can suppress
    // "Missing credentials" when CP knows we have a key but the runtime
    // hasn't re-probed yet — otherwise the two columns contradict.
    const health = resolveHealthBadge(rt, (cp?.kind === "local") || !!cp?.credential_configured);

    return (
      <tr
        key={id}
        onClick={() => setSelectedID(s => (s === id ? null : id))}
        role="button"
        tabIndex={0}
        aria-label={`Provider ${displayName}`}
        aria-expanded={isSelected}
        onKeyDown={e => {
          if (e.target !== e.currentTarget) return;
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            setSelectedID(s => (s === id ? null : id));
          }
        }}
        style={{
          background: isSelected ? "var(--teal-bg)" : undefined,
          borderBottom: isLast ? undefined : "1px solid var(--border)",
          cursor: "pointer",
        }}>
        {/* Name */}
        <td style={{ padding: "8px 12px" }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <BrandAvatar brand={id} fallback={displayName} size={22} />
            <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>
              {displayName}
            </span>
            {cp?.custom_name && (
              <span style={{
                fontSize: 11, color: "var(--t3)", fontStyle: "italic",
                overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
                minWidth: 0,
              }}>
                {cp.custom_name}
              </span>
            )}
          </div>
        </td>

        {/* Health */}
        <td style={{ padding: "8px 12px", whiteSpace: "nowrap" }}>
          <Badge status={health.status} label={health.label} />
        </td>

        {/* Protocol */}
        <td style={{ padding: "8px 12px", whiteSpace: "nowrap" }}>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>
            {protocol}
          </span>
        </td>

        {/* URL */}
        <td style={{ padding: "8px 12px", maxWidth: 240 }}>
          <span
            title={baseURL || undefined}
            style={{
              fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)",
              overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
              display: "block",
            }}>
            {baseURL || "—"}
          </span>
        </td>

        {/* Credentials */}
        <td style={{ padding: "8px 12px", whiteSpace: "nowrap" }}>
          <Badge status={cred.status} label={cred.label} />
        </td>

        {/* Models */}
        <td
          style={{ padding: "8px 12px", textAlign: "right", whiteSpace: "nowrap" }}
          title={modelCount === 0
            ? cp && !rt
              ? "Hecate has not received a current model-discovery result for this configured provider yet."
              : cp?.kind === "local"
                ? "No models discovered yet. Run `ollama pull <model>` (or your provider's equivalent) to populate this list."
              : "No models returned by /v1/models. Check the API key is correct and that the provider's account has model access."
            : undefined}>
          {modelBadge ? (
            <Badge status={modelBadge.status} label={modelBadge.label} />
          ) : (
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--t0)" }}>
              {modelCount}
            </span>
          )}
        </td>

        {/* Last checked */}
        <td style={{ padding: "8px 12px", whiteSpace: "nowrap" }}>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
            {rt?.last_checked_at
              ? formatProviderTime(rt.last_checked_at)
              : "—"}
          </span>
        </td>

        {/* Delete */}
        <td
          style={{ padding: "8px 12px", textAlign: "right" }}
          onClick={e => e.stopPropagation()}>
          <button
            className="btn btn-ghost btn-sm"
            style={{ padding: "3px 6px", color: "var(--t3)" }}
            title={`Remove ${displayName}`}
            aria-label={`Remove provider ${displayName}`}
            type="button"
            onClick={e => {
              e.stopPropagation();
              setDeleteConfirmID(id);
            }}>
            <Icon d={Icons.trash} size={13} />
          </button>
        </td>
      </tr>
    );
  }

  function renderTable(
    title: string,
    subtitle: string,
    ids: string[],
  ) {
    if (ids.length === 0) return null;
    return (
      <div style={{ marginBottom: 28 }}>
        <div style={{ display: "flex", alignItems: "baseline", gap: 10, marginBottom: 10 }}>
          <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>{title}</span>
          <span style={{ fontSize: 11, color: "var(--t3)" }}>{subtitle}</span>
        </div>
        <div style={{ border: "1px solid var(--border)", borderRadius: "var(--radius)", overflow: "hidden" }}>
          {/* table-layout: fixed + a shared colgroup pin the column
              widths so the cloud and local tables line up vertically.
              Without this each table sizes its columns by content
              independently and the headers don't align across tables. */}
          <table style={{ width: "100%", borderCollapse: "collapse", tableLayout: "fixed" }}>
            <colgroup>
              <col style={{ width: "22%" }} />
              <col style={{ width: "13%" }} />
              <col style={{ width: "9%" }} />
              <col style={{ width: "22%" }} />
              <col style={{ width: "12%" }} />
              <col style={{ width: "8%" }} />
              <col style={{ width: "10%" }} />
              <col style={{ width: "4%" }} />
            </colgroup>
            <thead>
              <tr style={{ borderBottom: "1px solid var(--border)", background: "var(--bg2)" }}>
                <th style={thStyle}>Provider</th>
                <th style={thStyle}>Health</th>
                <th style={thStyle}>Protocol</th>
                <th style={thStyle}>Endpoint</th>
                <th style={thStyle}>Credentials</th>
                <th style={{ ...thStyle, textAlign: "right" }}>Models</th>
                <th style={thStyle}>Last checked</th>
                <th style={{ ...thStyle, textAlign: "right" }}></th>
              </tr>
            </thead>
            <tbody>
              {ids.map((id, i) => renderRow(id, i === ids.length - 1))}
            </tbody>
          </table>
        </div>
      </div>
    );
  }

  // ── Render ───────────────────────────────────────────────────────────────────

  return (
    <div style={{ height: "100%", overflow: "hidden" }}>

      {/* Provider tables */}
      <div style={{ height: "100%", overflowY: "auto", padding: 16 }}>

        {/* Header row */}
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 20 }}>
          <span style={{ fontSize: 14, fontWeight: 500, color: "var(--t0)" }}>Connections</span>
          <button
            className="btn btn-primary btn-sm"
            style={{ marginLeft: "auto" }}
            onClick={() => setAddProviderOpen(true)}>
            Add provider
          </button>
        </div>

        {configuredProviders.length > 0 && (
          <div
            className="card"
            data-testid="connections-readiness-summary"
            style={{ padding: "14px 16px", marginBottom: 24 }}
          >
            <div style={{ display: "flex", alignItems: "flex-start", gap: 12, marginBottom: 12 }}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: 13, fontWeight: 600, color: "var(--t0)", marginBottom: 4 }}>
                  Model provider readiness
                </div>
                <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
                  Checks credentials, discovery, health, routing, and selected-model repair paths before a chat fails.
                </div>
              </div>
              {(nextReadinessStep || readinessAction) && (
                <div style={{ marginLeft: "auto", maxWidth: 460, display: "flex", gap: 10, alignItems: "center", justifyContent: "flex-end" }}>
                  {nextReadinessStep && (
                    <div
                      style={{
                        fontSize: 11,
                        color: nextReadinessStep.tone === "amber" ? "var(--amber)" : "var(--t2)",
                        lineHeight: 1.45,
                        textAlign: "right",
                      }}
                    >
                      {nextReadinessStep.text}
                    </div>
                  )}
                  {readinessAction && (
                    <button
                      type="button"
                      className={readinessAction.tone === "primary" ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"}
                      onClick={runReadinessAction}
                      style={{ whiteSpace: "nowrap" }}
                    >
                      {readinessAction.label}
                    </button>
                  )}
                </div>
              )}
            </div>
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "repeat(auto-fit, minmax(135px, 1fr))",
                gap: 10,
              }}
            >
              <ConnectionStat label="Configured" value={String(configuredProviders.length)} hint="provider records" />
              <ConnectionStat label="Ready" value={String(readyProviderCount)} hint="routing-ready" tone={readyProviderCount > 0 ? "green" : "muted"} />
              <ConnectionStat label="Needs attention" value={String(blockedProviderCount)} hint="blocked providers" tone={blockedProviderCount > 0 ? "amber" : "muted"} />
              <ConnectionStat label="Models" value={String(discoveredModelCount)} hint="discovered" tone={discoveredModelCount > 0 ? "green" : "muted"} />
            </div>
            <div
              data-testid="connections-provider-readiness-meaning"
              style={{
                marginTop: 10,
                fontSize: 11,
                color: readinessMeaning.tone === "amber" ? "var(--amber)" : "var(--t3)",
                lineHeight: 1.45,
              }}
            >
              {readinessMeaning.message}
            </div>
          </div>
        )}

        {configuredProviders.length === 0 ? (
          <div style={{
            display: "flex", flexDirection: "column", alignItems: "center",
            justifyContent: "center", minHeight: 280, gap: 0,
          }}>
            <div style={{ fontSize: 14, color: "var(--t1)", fontWeight: 500 }}>No model providers configured</div>
            <div style={{ fontSize: 12, color: "var(--t3)", marginTop: 6 }}>
              Add a local or cloud provider to start routing requests
            </div>
            <button
              className="btn btn-primary btn-sm"
              style={{ marginTop: 16 }}
              onClick={() => setAddProviderOpen(true)}>
              Add provider
            </button>
          </div>
        ) : (
          <>
            {renderTable(
              "Cloud providers",
              `${cloudIDs.length} configured`,
              cloudIDs,
            )}

            {renderTable(
              "Local inference",
              `${localIDs.length} configured`,
              localIDs,
            )}
          </>
        )}

        <div style={{ marginTop: configuredProviders.length === 0 ? 28 : 8 }}>
          <ConnectionsPanel
            state={state}
            actions={actions}
            showProviderSummary={false}
          />
        </div>
      </div>

      {/* Edit provider modal — opens on row click */}
      {selectedID && selectedConfig && (
        <Modal
          title={`${selectedPreset?.name || selectedConfig.name || selectedID} · ${selectedConfig.kind || "cloud"}`}
          onClose={() => setSelectedID(null)}
          footer={null}
          width={560}>
          <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>

            {/* Header strip: brand initial + base URL */}
            <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
              <BrandAvatar brand={selectedID} fallback={selectedPreset?.name || selectedConfig.name || selectedID} size={32} />
              {selectedConfig.base_url && (
                <span style={{
                  fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)",
                  overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
                  flex: 1, minWidth: 0,
                }}>
                  {selectedConfig.base_url}
                </span>
              )}
            </div>

            {/* Circuit-open banner */}
            {selectedStatus?.status === "open" && selectedStatus.open_until && (
              <div style={{
                padding: "8px 12px", background: "var(--red-bg)",
                borderRadius: "var(--radius-sm)",
                fontSize: 12, color: "var(--red)", lineHeight: 1.4,
              }}>
                Circuit open — will probe at {formatProviderTime(selectedStatus.open_until)}
              </div>
            )}

            {/* Live status grid */}
            <div style={{
              display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 12,
              padding: 12, border: "1px solid var(--border)", borderRadius: "var(--radius-sm)",
            }}>
              {([
                ["Health",   selectedStatus?.status || "unknown"],
                ["Route",    selectedStatus?.routing_ready === false
                  ? describeRoutingBlockedReason(selectedStatus.routing_blocked_reason)
                  : "Ready"],
                ["Models",   selectedStatus?.model_count ?? selectedStatus?.models?.length ?? "—"],
                ["Checked",  selectedStatus?.last_checked_at
                  ? formatProviderTime(selectedStatus.last_checked_at)
                  : "—"],
              ] as [string, string | number][]).map(([label, val]) => (
                <div key={label}>
                  <div className="kicker" style={{ marginBottom: 2 }}>{label}</div>
                  <div style={{
                    fontSize: 12, fontWeight: 500, color: "var(--t0)",
                    fontFamily: "var(--font-mono)",
                    overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
                  }}>
                    {val}
                  </div>
                </div>
              ))}
            </div>

            <ProviderReadinessSummary readiness={selectedStatus?.readiness} />

            <ProviderReadinessChecklist checks={selectedStatus?.readiness_checks ?? []} />

            {/* Editable Name — only for custom providers (no preset_id);
                preset providers keep the catalog name and reach for the
                Custom name field below to disambiguate. */}
            {!selectedConfig.preset_id && (
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                <label className="kicker-lg">Name</label>
                <input
                  className="input"
                  type="text"
                  value={pendingName}
                  onChange={e => setPendingName(e.target.value)}
                  placeholder="My Provider"
                />
                <button
                  className="btn btn-primary btn-sm"
                  style={{ alignSelf: "flex-start" }}
                  disabled={!pendingName.trim() || pendingName === (selectedConfig.name || selectedID)}
                  onClick={() => void actions.setProviderName(selectedID, pendingName)}>
                  <Icon d={Icons.check} size={13} /> Save name
                </button>
              </div>
            )}

            {/* Custom name — operator-supplied disambiguator that
                appears alongside Name in the providers table. Empty
                clears it. */}
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              <label className="kicker-lg" style={{ display: "flex", gap: 6 }}>
                Custom name
                <span style={{ color: "var(--t3)", fontWeight: 400, textTransform: "none" }}>optional</span>
              </label>
              <input
                className="input"
                type="text"
                value={pendingCustomName}
                onChange={e => setPendingCustomName(e.target.value)}
                placeholder="e.g. Prod, Dev, Staging"
              />
              <button
                className="btn btn-primary btn-sm"
                style={{ alignSelf: "flex-start" }}
                disabled={pendingCustomName === (selectedConfig.custom_name ?? "")}
                onClick={() => void actions.setProviderCustomName(selectedID, pendingCustomName)}>
                <Icon d={Icons.check} size={13} /> Save custom name
              </button>
            </div>

            {/* Editable: API key (cloud) or Endpoint URL (local) */}
            {selectedConfig.kind === "local" ? (
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                <label className="kicker-lg">Endpoint URL</label>
                <input
                  className="input"
                  type="text"
                  value={pendingURL}
                  onChange={e => setPendingURL(e.target.value)}
                  placeholder="http://localhost:11434/v1"
                  style={{ fontFamily: "var(--font-mono)" }}
                  autoFocus
                />
                <button
                  className="btn btn-primary btn-sm"
                  style={{ alignSelf: "flex-start" }}
                  disabled={!pendingURL.trim() || pendingURL === resolvedBaseURL(selectedID, selectedConfig ?? undefined, state.providerPresets)}
                  onClick={() => void actions.setProviderBaseURL(selectedID, pendingURL)}>
                  <Icon d={Icons.check} size={13} /> Save URL
                </button>
              </div>
            ) : (
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                <label className="kicker-lg" style={{ display: "flex", gap: 6 }}>
                  API Key
                  {selectedConfig.credential_source === "env" && !pendingKey && (
                    <span style={{ color: "var(--teal)", fontWeight: 400, textTransform: "none" }}>from env</span>
                  )}
                </label>
                <input
                  className="input"
                  type="password"
                  name="hecate-provider-api-key"
                  autoComplete="new-password"
                  autoCorrect="off"
                  spellCheck={false}
                  data-1p-ignore="true"
                  data-lpignore="true"
                  data-form-type="other"
                  placeholder={selectedConfig.credential_configured ? "••••••••" : "sk-…"}
                  value={pendingKey}
                  onChange={e => setPendingKey(e.target.value)}
                  style={{ fontFamily: "var(--font-mono)", letterSpacing: "0.1em" }}
                  autoFocus
                />
                {!selectedConfig.credential_configured && (
                  <div style={{ fontSize: 11, color: "var(--t3)" }}>Stored encrypted at rest. Never logged.</div>
                )}
                <div style={{ display: "flex", gap: 8 }}>
                  <button
                    className="btn btn-primary btn-sm"
                    disabled={!pendingKey.trim()}
                    onClick={() =>
                      void actions.setProviderAPIKey(selectedID, pendingKey).then(() => setPendingKey(""))
                    }>
                    <Icon d={Icons.check} size={13} />
                    {selectedConfig.credential_configured ? "Update API key" : "Save API key"}
                  </button>
                  {selectedConfig.credential_source === "vault" && (
                    <button
                      className="btn btn-danger btn-sm"
                      onClick={() => void actions.setProviderAPIKey(selectedID, "")}>
                      <Icon d={Icons.trash} size={13} /> Delete
                    </button>
                  )}
                </div>
              </div>
            )}

            {/* Diagnostics — only when there's something to show */}
            {(selectedStatus?.last_error ||
              selectedStatus?.last_error_class || selectedStatus?.discovery_source ||
              selectedHealthCounters) && (
              <details>
                <summary style={{ fontSize: 11, color: "var(--t2)", cursor: "pointer", userSelect: "none" }}>
                  Diagnostics
                </summary>
                <div style={{ marginTop: 8, padding: 10, background: "var(--bg2)", borderRadius: "var(--radius-sm)" }}>
                  {selectedStatus?.last_error && (
                    <div style={{ fontSize: 12, color: "var(--red)", lineHeight: 1.45, marginBottom: 6 }}>
                      {selectedStatus.last_error}
                    </div>
                  )}
                  <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                    {selectedStatus?.last_error_class && (
                      <div style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                        error class: <span style={{ color: "var(--t1)" }}>{describeHealthErrorClass(selectedStatus.last_error_class)}</span>
                      </div>
                    )}
                    {selectedStatus?.discovery_source && (
                      <div style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                        discovery: <span style={{ color: "var(--t1)" }}>{selectedStatus.discovery_source}</span>
                      </div>
                    )}
                    {selectedHealthCounters && (
                      <div style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                        {selectedHealthCounters}
                      </div>
                    )}
                    {(selectedStatus?.total_successes != null || selectedStatus?.total_failures != null) && (
                      <div style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                        totals: <span style={{ color: "var(--t1)" }}>
                          {selectedStatus?.total_successes ?? 0} ok · {selectedStatus?.total_failures ?? 0} failed
                        </span>
                      </div>
                    )}
                    {selectedStatus?.last_latency_ms != null && (
                      <div style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                        last latency: <span style={{ color: "var(--t1)" }}>{selectedStatus.last_latency_ms}ms</span>
                      </div>
                    )}
                  </div>
                </div>
              </details>
            )}

            {/* Model list */}
            {selectedStatus?.models && selectedStatus.models.length > 0 && (
              <details open>
                <summary style={{ fontSize: 11, color: "var(--t2)", cursor: "pointer", userSelect: "none" }}>
                  Models ({selectedStatus.models.length})
                </summary>
                <div style={{
                  marginTop: 8, maxHeight: 200, overflowY: "auto",
                  border: "1px solid var(--border)", borderRadius: "var(--radius-sm)",
                  padding: "0 10px",
                }}>
                  {selectedStatus.models.map(m => (
                    <div
                      key={m}
                      style={{
                        display: "flex", alignItems: "center",
                        padding: "5px 0", borderBottom: "1px solid var(--border)",
                      }}>
                      <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--t0)", flex: 1 }}>
                        {m}
                      </span>
                      {m === selectedConfig.default_model && (
                        <span className="badge badge-teal" style={{ fontSize: 9 }}>default</span>
                      )}
                    </div>
                  ))}
                </div>
              </details>
            )}
          </div>
        </Modal>
      )}

      <AddProviderModal
        open={addProviderOpen}
        state={state}
        actions={actions}
        onClose={() => setAddProviderOpen(false)}
      />

      {deleteConfirmID && deleteConfirmConfig && (
        <ConfirmModal
          danger
          title="Remove provider?"
          message={
            <>
              Remove <span style={{ fontWeight: 600, color: "var(--t0)" }}>{deleteConfirmName}</span> from Hecate? Existing chats stay in history, but new requests will stop routing through this provider.
            </>
          }
          confirmLabel="Remove provider"
          onClose={() => setDeleteConfirmID(null)}
          onConfirm={async () => {
            const id = deleteConfirmID;
            setDeleteConfirmID(null);
            if (selectedID === id) setSelectedID(null);
            await actions.deleteProvider(id);
          }}
        />
      )}
    </div>
  );
}

// ── Helpers ───────────────────────────────────────────────────────────────────

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

type ConnectionStatTone = "green" | "amber" | "muted";

function ConnectionStat({
  label,
  value,
  hint,
  tone = "muted",
}: {
  label: string;
  value: string;
  hint: string;
  tone?: ConnectionStatTone;
}) {
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 10,
        padding: "10px 12px",
        background: "rgba(255, 255, 255, 0.015)",
      }}
    >
      <div style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", textTransform: "uppercase", letterSpacing: "0.06em", marginBottom: 5 }}>
        {label}
      </div>
      <div style={{ fontFamily: "var(--font-mono)", fontSize: 18, fontWeight: 700, color: statColor(tone), lineHeight: 1 }}>
        {value}
      </div>
      <div style={{ fontSize: 10, color: "var(--t3)", marginTop: 6 }}>{hint}</div>
    </div>
  );
}

function statColor(tone: ConnectionStatTone): string {
  switch (tone) {
    case "green": return "var(--teal)";
    case "amber": return "var(--amber)";
    case "muted": return "var(--t3)";
  }
}

function isProviderRoutingReady(
  provider: NonNullable<RuntimeConsoleViewModel["state"]["settingsConfig"]>["providers"][number],
  status: RuntimeConsoleViewModel["state"]["providers"][number] | undefined,
): boolean {
  if (status?.readiness?.status) {
    return status.readiness.status === "ok" || status.readiness.status === "warning";
  }
  if (status?.routing_ready != null) return status.routing_ready;
  if (provider.kind === "local") return Boolean(status?.healthy);
  return Boolean(provider.credential_configured && status?.healthy);
}

function isProviderNeedsAttention(
  provider: NonNullable<RuntimeConsoleViewModel["state"]["settingsConfig"]>["providers"][number],
  status: RuntimeConsoleViewModel["state"]["providers"][number] | undefined,
): boolean {
  if (!status) return true;
  if (status?.readiness?.status === "blocked") return true;
  if (provider.kind !== "local" && !provider.credential_configured) return true;
  if (status?.routing_blocked_reason) return true;
  if (status?.status === "open" || status?.status === "unhealthy") return true;
  return false;
}

function resolveNextReadinessStep(
  hint: ReturnType<typeof providerFleetRepairHint>,
): { text: string; tone: ConnectionStatTone } | null {
  if (!hint) return null;
  if (hint.providerID && hint.actionKind === "refresh_providers") return null;
  return {
    tone: hint.tone === "amber" || hint.tone === "red" ? "amber" : "muted",
    text: hint.tone === "muted" ? hint.message : `${hint.message} ${hint.action}`,
  };
}

function providerRepairButton(
  hint: ReturnType<typeof providerFleetRepairHint>,
): { label: string; tone: "primary" | "ghost" } | null {
  if (!hint || hint.tone === "muted") return null;
  const label = providerRepairActionLabel(hint.actionKind);
  if (!label) return null;
  return { label, tone: hint.actionKind === "add_provider" ? "primary" : "ghost" };
}

function formatProviderTime(value: string): string {
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return value;
  return new Date(parsed).toLocaleTimeString();
}

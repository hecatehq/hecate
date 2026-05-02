import { useEffect, useState, type CSSProperties } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import { buildConflictMap, resolvedBaseURL } from "../../lib/provider-utils";
import { describeHealthErrorClass, describeRoutingBlockedReason } from "../../lib/runtime-utils";
import { Badge, Icon, Icons, InlineError, Modal } from "../shared/ui";
import type { ProviderPresetRecord } from "../../types/runtime";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
};

const PRESET_COLORS: Record<string, string> = {
  anthropic:   "var(--brand-anthropic)",
  openai:      "var(--brand-openai)",
  gemini:      "var(--brand-gemini)",
  mistral:     "var(--brand-mistral)",
  groq:        "var(--brand-groq)",
  deepseek:    "var(--teal)",
  together_ai: "var(--t2)",
  xai:         "var(--t0)",
  ollama:      "var(--teal)",
  lmstudio:    "var(--t2)",
  llamacpp:    "var(--t2)",
  localai:     "var(--t2)",
};

function iconColorByID(id: string): string {
  return PRESET_COLORS[id.toLowerCase()] ?? "var(--teal)";
}

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
  // Add-provider flow state
  const [addStep, setAddStep] = useState<"pick" | "form" | null>(null);
  const [addPickTab, setAddPickTab] = useState<"cloud" | "local">("cloud");
  const [addPreset, setAddPreset] = useState<ProviderPresetRecord | null>(null);
  const [addForm, setAddForm] = useState({ name: "", custom_name: "", base_url: "", api_key: "", kind: "cloud", protocol: "openai" });
  const [addError, setAddError] = useState("");
  const [addLoading, setAddLoading] = useState(false);

  // Auto-poll model discovery only when there's something to discover for —
  // either at least one configured provider, or the Add modal is open
  // (so a freshly-added provider's models surface immediately). Otherwise
  // /admin/providers + /v1/models are no-op network calls; skip them.
  const hasProviders = (state.controlPlaneConfig?.providers?.length ?? 0) > 0;
  const shouldPoll = hasProviders || addStep !== null;
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
  // controlPlaneConfig.providers, and React's "same key" warning fires before
  // the next render reconciles. First write wins.
  const configuredProviders = (() => {
    const seen = new Set<string>();
    const out: NonNullable<typeof state.controlPlaneConfig>["providers"] = [];
    for (const p of state.controlPlaneConfig?.providers ?? []) {
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

  const configuredByName = new Map(
    configuredProviders.filter(p => p.base_url).map(p => [p.name, p]),
  );
  const allConfiguredIDs = configuredProviders.map(p => p.id).sort(stableSort);
  const conflictMap = buildConflictMap(allConfiguredIDs, configuredByName, state.providerPresets);

  const cloudIDs = configuredProviders.filter(p => p.kind === "cloud").map(p => p.id).sort(stableSort);
  const localIDs = configuredProviders.filter(p => p.kind !== "cloud").map(p => p.id).sort(stableSort);

  const selectedConfig = selectedID ? configuredByID.get(selectedID) ?? null : null;
  const selectedStatus = selectedID ? statusByName.get(selectedID) : null;
  const selectedPreset = selectedID ? state.providerPresets.find(p => p.id === selectedID) : null;

  function closeAdd() {
    setAddStep(null);
    setAddPickTab("cloud");
    setAddPreset(null);
    setAddForm({ name: "", custom_name: "", base_url: "", api_key: "", kind: "cloud", protocol: "openai" });
    setAddError("");
  }

  async function submitAdd() {
    setAddLoading(true);
    setAddError("");
    try {
      await actions.createProvider({
        name: addForm.name.trim(),
        preset_id: addPreset?.id,
        custom_name: addForm.custom_name.trim() || undefined,
        base_url: addForm.base_url.trim() || undefined,
        api_key: addForm.api_key.trim() || undefined,
        kind: addForm.kind,
        protocol: addForm.protocol,
      });
      closeAdd();
    } catch (e) {
      setAddError(e instanceof Error ? e.message : "Failed to add provider");
    } finally {
      setAddLoading(false);
    }
  }

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
    const conflicts = conflictMap.get(id) ?? [];
    const conflictTitle =
      conflicts.length > 0
        ? `Shares endpoint with ${conflicts.join(", ")} — only one can serve traffic at a time`
        : undefined;
    const baseURL = rt?.base_url || resolvedBaseURL(id, cp ?? undefined, state.providerPresets);
    const modelCount = rt?.model_count ?? rt?.models?.length ?? 0;
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
        onKeyDown={e => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            setSelectedID(s => (s === id ? null : id));
          }
        }}
        style={{
          background: isSelected ? "var(--teal-bg)" : undefined,
          borderBottom: isLast ? undefined : "1px solid var(--border)",
          cursor: "pointer",
          outline: "none",
        }}>
        {/* Name */}
        <td style={{ padding: "8px 12px" }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <div style={{
              width: 22, height: 22, borderRadius: "var(--radius-sm)",
              background: "var(--bg3)", border: "1px solid var(--border)",
              display: "flex", alignItems: "center", justifyContent: "center",
              fontFamily: "var(--font-mono)", fontSize: 11, fontWeight: 600,
              color: iconColorByID(id), flexShrink: 0,
            }}>
              {displayName[0].toUpperCase()}
            </div>
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
            {conflicts.length > 0 && (
              <span
                role="img"
                aria-label={conflictTitle}
                title={conflictTitle}
                style={{ fontSize: 11, color: "var(--amber)", cursor: "help", flexShrink: 0 }}>
                ⚠
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
            ? cp?.kind === "local"
              ? "No models discovered yet. Run `ollama pull <model>` (or your provider's equivalent) to populate this list."
              : "No models returned by /v1/models. Check the API key is correct and that the provider's account has model access."
            : undefined}>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: modelCount > 0 ? "var(--t0)" : "var(--t3)" }}>
            {modelCount > 0 ? modelCount : "—"}
          </span>
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
            onClick={e => {
              e.stopPropagation();
              if (window.confirm(`Remove provider ${displayName}?`)) {
                void actions.deleteProvider(id);
                if (selectedID === id) setSelectedID(null);
              }
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

  // ── Pick step ────────────────────────────────────────────────────────────────

  const localPresets = state.providerPresets.filter(p => p.kind === "local");
  const cloudPresets = state.providerPresets.filter(p => p.kind === "cloud");

  function PickStep() {
    function pickPreset(preset: ProviderPresetRecord) {
      setAddPreset(preset);
      setAddForm({ name: preset.name, custom_name: "", base_url: preset.base_url, api_key: "", kind: preset.kind, protocol: preset.protocol ?? "openai" });
      setAddStep("form");
    }
    function pickCustom(kind: "cloud" | "local") {
      setAddPreset(null);
      setAddForm({ name: "Custom", custom_name: "", base_url: "", api_key: "", kind, protocol: "openai" });
      setAddStep("form");
    }

    function PresetButton({ preset }: { preset: ProviderPresetRecord }) {
      return (
        <button
          className="btn btn-ghost"
          style={{
            minHeight: 60, display: "flex", alignItems: "center", gap: 10,
            textAlign: "left", padding: "10px 12px",
            border: "1px solid var(--border)", borderRadius: "var(--radius)",
          }}
          onClick={() => pickPreset(preset)}>
          <div style={{
            width: 28, height: 28, borderRadius: "var(--radius-sm)",
            background: "var(--bg3)", border: "1px solid var(--border)",
            display: "flex", alignItems: "center", justifyContent: "center",
            fontFamily: "var(--font-mono)", fontSize: 12, fontWeight: 600,
            color: iconColorByID(preset.id), flexShrink: 0,
          }}>
            {preset.name[0].toUpperCase()}
          </div>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
              {preset.name}
            </div>
            {preset.description && (
              <div style={{
                fontSize: 11, color: "var(--t3)", lineHeight: 1.35, marginTop: 2,
                display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical",
                overflow: "hidden",
              }}>
                {preset.description}
              </div>
            )}
          </div>
        </button>
      );
    }

    function CustomButton({ kind }: { kind: "cloud" | "local" }) {
      return (
        <button
          className="btn btn-ghost"
          style={{
            height: 60, display: "flex", alignItems: "center", gap: 10,
            textAlign: "left", padding: "0 12px",
            border: "1px dashed var(--border)", borderRadius: "var(--radius)",
          }}
          onClick={() => pickCustom(kind)}>
          <div style={{
            width: 28, height: 28, borderRadius: "var(--radius-sm)",
            background: "var(--bg3)", border: "1px solid var(--border)",
            display: "flex", alignItems: "center", justifyContent: "center",
            fontSize: 18, color: "var(--t3)", flexShrink: 0,
          }}>
            +
          </div>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>Custom</div>
            <div style={{ fontSize: 11, color: "var(--t3)", textTransform: "uppercase", letterSpacing: "0.04em" }}>openai-compatible</div>
          </div>
        </button>
      );
    }

    function TabButton({ id, label }: { id: "cloud" | "local"; label: string }) {
      const active = addPickTab === id;
      return (
        <button
          type="button"
          className="btn btn-ghost btn-sm"
          onClick={() => setAddPickTab(id)}
          style={{
            padding: "6px 12px", borderRadius: 0,
            borderBottom: `2px solid ${active ? "var(--teal)" : "transparent"}`,
            color: active ? "var(--t0)" : "var(--t2)",
            fontWeight: active ? 500 : 400,
          }}>
          {label}
        </button>
      );
    }

    const presets = addPickTab === "cloud" ? cloudPresets : localPresets;

    // Pin the grid's min-height to the larger tab so the modal doesn't jump
    // when the operator switches between Cloud and Local. Each row is sized
    // by the tallest card in it (descriptions vary), so we estimate via
    // gridAutoRows + a min-height that fits the max row count.
    const cardsPerRow = 3;
    const maxItemCount = Math.max(cloudPresets.length, localPresets.length) + 1; // +1 for Custom
    const maxRows = Math.ceil(maxItemCount / cardsPerRow);
    const rowHeight = 78; // tracks PresetButton minHeight + vertical padding
    const rowGap = 8;
    const gridMinHeight = maxRows * rowHeight + (maxRows - 1) * rowGap;

    return (
      <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
        {/* Tab bar */}
        <div style={{ display: "flex", gap: 4, borderBottom: "1px solid var(--border)", marginBottom: 4 }}>
          <TabButton id="cloud" label="Cloud" />
          <TabButton id="local" label="Local" />
        </div>

        {/* Preset grid + Custom for the active tab */}
        <div style={{
          display: "grid",
          gridTemplateColumns: "repeat(3, minmax(0, 1fr))",
          gridAutoRows: `minmax(${rowHeight}px, auto)`,
          alignContent: "start",
          gap: rowGap,
          minHeight: gridMinHeight,
        }}>
          {presets.map(p => <PresetButton key={p.id} preset={p} />)}
          <CustomButton kind={addPickTab} />
        </div>
      </div>
    );
  }

  // ── Form step ────────────────────────────────────────────────────────────────

  function FormStep() {
    const showURL = addForm.kind === "local" || (!addPreset && addForm.kind === "cloud");
    const showAPIKey = addForm.kind === "cloud";
    const currentBaseURL = resolvedBaseURL(
      addPreset?.id ?? "",
      addPreset ? { id: addPreset.id, name: addPreset.name, kind: addPreset.kind, protocol: addPreset.protocol, base_url: addPreset.base_url, credential_configured: false } : undefined,
      state.providerPresets,
    );
    const saveDisabled = addLoading || !addForm.name.trim();

    // The URL the new provider would actually serve traffic on. For local
    // and "custom cloud" providers that's whatever the operator typed; for
    // a cloud preset it's the preset's fixed base_url. We use this to
    // surface a yellow inline warning when the URL collides with an
    // existing provider — the backend will 409 either way, but a heads-up
    // before submit saves a round-trip.
    const effectiveBaseURL = (showURL ? addForm.base_url.trim() : currentBaseURL).trim();
    const baseURLConflictWith = effectiveBaseURL
      ? configuredProviders.find(p => {
          const url = resolvedBaseURL(p.id, p, state.providerPresets);
          return url && url === effectiveBaseURL;
        })
      : undefined;

    // First editable field gets autofocus on modal open. Custom → Name;
    // cloud preset → API Key (Name + URL are fixed); local preset → URL
    // (Name is fixed, URL may need tweaking from the preset default).
    const focusName   = addPreset === null;
    const focusURL    = addPreset !== null && showURL;
    const focusAPIKey = addPreset !== null && !showURL && showAPIKey;

    return (
      <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
        {/* Breadcrumb back-link to the preset picker. The Back button at
            the bottom of the form is also wired, but a chevron link at
            the top is the conventional "go up a level" affordance and
            stays visible while the operator is filling fields. */}
        <button
          type="button"
          className="btn btn-ghost btn-sm"
          onClick={() => setAddStep("pick")}
          disabled={addLoading}
          style={{
            alignSelf: "flex-start",
            padding: "2px 6px 2px 0",
            color: "var(--t2)",
            display: "flex", alignItems: "center", gap: 4,
          }}>
          <Icon d={Icons.chevL} size={11} />
          <span style={{ fontSize: 11 }}>All providers</span>
        </button>
        <div>
          <label className="kicker-lg" style={{ display: "block", marginBottom: 6 }}>Name</label>
          <input
            className="input"
            type="text"
            value={addForm.name}
            onChange={e => setAddForm(f => ({ ...f, name: e.target.value }))}
            placeholder="My Provider"
            readOnly={addPreset !== null}
            disabled={addPreset !== null}
            title={addPreset !== null ? "Preset names are fixed — use Custom name below to disambiguate two instances of the same preset" : undefined}
            autoFocus={focusName}
          />
        </div>
        {/* Custom name — optional disambiguator. Shown for every provider
            but really earns its keep when the operator is adding a second
            instance of an already-configured preset. */}
        <div>
          <label className="kicker-lg" style={{ display: "block", marginBottom: 6 }}>
            Custom name <span style={{ color: "var(--t3)", fontWeight: 400, textTransform: "none" }}>optional</span>
          </label>
          <input
            className="input"
            type="text"
            value={addForm.custom_name}
            onChange={e => setAddForm(f => ({ ...f, custom_name: e.target.value }))}
            placeholder="e.g. Prod, Dev, Staging"
          />
        </div>
        {showURL && (
          <div>
            <label className="kicker-lg" style={{ display: "block", marginBottom: 6 }}>Endpoint URL</label>
            <input
              className="input"
              type="text"
              value={addForm.base_url}
              onChange={e => setAddForm(f => ({ ...f, base_url: e.target.value }))}
              placeholder={currentBaseURL || "http://localhost:11434/v1"}
              style={{ fontFamily: "var(--font-mono)" }}
              autoFocus={focusURL}
            />
            {baseURLConflictWith && (
              <div
                role="status"
                style={{
                  marginTop: 6,
                  fontSize: 11,
                  color: "var(--amber)",
                  lineHeight: 1.4,
                }}>
                This URL is already used by{" "}
                <span style={{ fontFamily: "var(--font-mono)" }}>
                  {baseURLConflictWith.name || baseURLConflictWith.id}
                </span>
                . Backend will reject.
              </div>
            )}
          </div>
        )}
        {showAPIKey && (
          <div>
            <label className="kicker-lg" style={{ display: "block", marginBottom: 6 }}>API Key</label>
            <input
              className="input"
              type="password"
              value={addForm.api_key}
              onChange={e => setAddForm(f => ({ ...f, api_key: e.target.value }))}
              placeholder="sk-…"
              style={{ fontFamily: "var(--font-mono)", letterSpacing: "0.1em" }}
              autoFocus={focusAPIKey}
            />
          </div>
        )}
        {addError && <InlineError message={addError} />}
        <button
          className="btn btn-primary btn-sm"
          style={{ justifyContent: "center", marginTop: 4 }}
          disabled={saveDisabled}
          onClick={() => void submitAdd()}>
          {addLoading ? "Adding…" : "Add provider"}
        </button>
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
          <span style={{ fontSize: 14, fontWeight: 500, color: "var(--t0)" }}>Providers</span>
          <button
            className="btn btn-primary btn-sm"
            style={{ display: "flex", alignItems: "center", gap: 4, marginLeft: "auto" }}
            onClick={() => setAddStep("pick")}>
            <Icon d={Icons.plus} size={13} />
            Add provider
          </button>
        </div>

        {configuredProviders.length === 0 ? (
          <div style={{
            display: "flex", flexDirection: "column", alignItems: "center",
            justifyContent: "center", height: "calc(100% - 52px)", gap: 0,
          }}>
            <div style={{ fontSize: 14, color: "var(--t1)", fontWeight: 500 }}>No providers configured</div>
            <div style={{ fontSize: 12, color: "var(--t3)", marginTop: 6 }}>
              Add a local or cloud provider to start routing requests
            </div>
            <button
              className="btn btn-primary btn-sm"
              style={{ marginTop: 16, display: "flex", alignItems: "center", gap: 4 }}
              onClick={() => setAddStep("pick")}>
              <Icon d={Icons.plus} size={13} />
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
              <div style={{
                width: 32, height: 32, borderRadius: "var(--radius-sm)",
                background: "var(--bg3)", border: "1px solid var(--border)",
                display: "flex", alignItems: "center", justifyContent: "center",
                fontFamily: "var(--font-mono)", fontSize: 14, fontWeight: 600,
                color: iconColorByID(selectedID), flexShrink: 0,
              }}>
                {(selectedConfig.name || selectedID)[0].toUpperCase()}
              </div>
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

      {/* Add provider modal */}
      {addStep && (
        <Modal
          title={addStep === "pick" ? "Add provider" : addPreset ? addPreset.name : "Custom provider"}
          onClose={closeAdd}
          footer={null}
          width={680}>
          {addStep === "pick" ? <PickStep /> : <FormStep />}
        </Modal>
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

function formatProviderTime(value: string): string {
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return value;
  return new Date(parsed).toLocaleTimeString();
}

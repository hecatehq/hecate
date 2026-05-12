import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import type { AgentAdapterHealthRecord, AgentAdapterRecord, AgentChatGrantRecord, ConfiguredProviderRecord, ModelRecord } from "../../types/runtime";
import { Badge, BrandAvatar, Icon, Icons, InlineError } from "../shared/ui";
import { PricebookTab } from "./PricebookTab";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
};

// Visible settings sub-tabs. Pricing covers per-model pricebook
// entries; Retention triggers and reviews stored-data sweeps; External
// agents lists durable approval grants ("always allow / always deny"
// rules) that survive process restart. Balances and usage live in the
// Costs workspace.
const TABS = ["pricebook", "model_capabilities", "retention", "external_agents"] as const;
type Tab = (typeof TABS)[number];
const TAB_LABELS: Record<Tab, string> = {
  pricebook: "Pricing",
  model_capabilities: "Model capabilities",
  retention: "Retention",
  external_agents: "External agents",
};

const TAB_STORAGE_KEY = "hecate.settingsTab";

export function SettingsView({ state, actions }: Props) {
  // Persist the settings sub-tab so refreshing while on (say) Pricebook
  // returns the operator to Pricebook.
  const [tab, setTabRaw] = useState<Tab>(() => {
    const saved = localStorage.getItem(TAB_STORAGE_KEY);
    if (saved && (TABS as readonly string[]).includes(saved)) return saved as Tab;
    return TABS[0];
  });
  const setTab = (next: Tab) => {
    localStorage.setItem(TAB_STORAGE_KEY, next);
    setTabRaw(next);
  };

  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", overflow: "hidden" }}>
      {/* Tab bar */}
      <div style={{ display: "flex", gap: 2, padding: "0 16px", borderBottom: "1px solid var(--border)", flexShrink: 0 }}>
        {TABS.map(t => (
          <button key={t} type="button"
            onClick={() => setTab(t)}
            style={{
              padding: "7px 12px",
              fontSize: 12,
              fontFamily: "var(--font-mono)",
              background: "none",
              border: "none",
              borderBottom: tab === t ? "2px solid var(--teal)" : "2px solid transparent",
              color: tab === t ? "var(--teal)" : "var(--t2)",
              cursor: "pointer",
              marginBottom: -1,
              textTransform: "uppercase",
              letterSpacing: "0.04em",
            }}>
            {TAB_LABELS[t]}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <div style={{ flex: 1, overflowY: "auto", padding: 16 }}>
        {tab === "pricebook"           && <PricebookTab state={state} actions={actions} />}
        {tab === "model_capabilities" && <ModelCapabilitiesTab state={state} actions={actions} />}
        {tab === "retention"           && <RetentionTab state={state} actions={actions} />}
        {tab === "external_agents"     && <ExternalAgentsTab state={state} actions={actions} />}
      </div>
    </div>
  );
}

function SectionHeader({
  title,
  description,
  meta,
  actions,
}: {
  title: string;
  description?: string;
  meta?: string;
  actions?: ReactNode;
}) {
  return (
    <div style={{ display: "flex", alignItems: "flex-start", gap: 12, marginBottom: 12 }}>
      <div style={{ minWidth: 0 }}>
        <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: description ? 3 : 0 }}>{title}</div>
        {description && (
          <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>{description}</div>
        )}
      </div>
      {meta && (
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)", whiteSpace: "nowrap" }}>{meta}</span>
      )}
      {actions && <div style={{ marginLeft: "auto", display: "flex", gap: 8, alignItems: "center" }}>{actions}</div>}
    </div>
  );
}


// ─── Model capabilities tab ───────────────────────────────────────────────────

function ModelCapabilitiesTab({ state, actions }: Props) {
  const [query, setQuery] = useState("");
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return [...state.models]
      .filter((model) => {
        if (!q) return true;
        const provider = model.metadata?.provider ?? "";
        return model.id.toLowerCase().includes(q) || provider.toLowerCase().includes(q);
      })
      .sort((a, b) => {
        const left = `${a.metadata?.provider ?? ""}/${a.id}`;
        const right = `${b.metadata?.provider ?? ""}/${b.id}`;
        return left.localeCompare(right);
      });
  }, [query, state.models]);

  return (
    <>
      <SectionHeader
        title="Model capabilities"
        description="Control whether Hecate should try tools for each model. Tools are on by default unless a model is explicitly marked off; use the switch when a local/custom model fails tool calls or when you want direct model chat only."
        meta={`${rows.length} model${rows.length === 1 ? "" : "s"}`}
      />

      <div className="card" style={{ padding: "12px 14px", marginBottom: 14 }}>
        <label style={{ display: "block", fontSize: 11, color: "var(--t3)", marginBottom: 6 }} htmlFor="model-capability-search">
          Filter by model or provider
        </label>
        <input
          id="model-capability-search"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="ollama, qwen, gpt..."
          style={{
            width: "100%",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-sm)",
            background: "var(--bg2)",
            color: "var(--t0)",
            fontSize: 12,
            padding: "8px 10px",
          }}
        />
      </div>

      {rows.length === 0 ? (
        <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
          No models discovered yet. Add or start a provider, then refresh the dashboard.
        </div>
      ) : (
        <div className="card" style={{ overflow: "hidden" }} data-testid="model-capabilities-list">
          {rows.map((model, index) => (
            <ModelCapabilityRow
              key={`${model.metadata?.provider ?? "unknown"}:${model.id}`}
              model={model}
              divider={index < rows.length - 1}
              onToolsChange={(enabled) => {
                const provider = model.metadata?.provider ?? "";
                if (!provider) return;
                void actions.upsertModelCapabilityOverride({
                  provider,
                  model: model.id,
                  tool_calling: enabled ? "basic" : "none",
                  streaming: model.metadata?.capabilities?.streaming,
                  max_context_tokens: model.metadata?.capabilities?.max_context_tokens,
                  note: enabled ? "Tools enabled from Settings." : "Tools disabled from Settings.",
                });
              }}
              onClear={() => {
                const provider = model.metadata?.provider ?? "";
                if (!provider) return;
                void actions.deleteModelCapabilityOverride(provider, model.id);
              }}
            />
          ))}
        </div>
      )}
    </>
  );
}

function ModelCapabilityRow({
  model,
  divider,
  onToolsChange,
  onClear,
}: {
  model: ModelRecord;
  divider: boolean;
  onToolsChange: (enabled: boolean) => void;
  onClear: () => void;
}) {
  const capabilities = model.metadata?.capabilities;
  const provider = model.metadata?.provider ?? "unknown";
  const toolCalling = capabilities?.tool_calling ?? "unknown";
  const source = capabilities?.source ?? "unknown";
  const toolsEnabled = toolCalling !== "none";
  const toolTone: ChipTone = toolsEnabled ? "green" : "red";
  const clearDisabled = source !== "operator_override";

  return (
    <div
      data-testid={`model-capability-row-${provider}-${model.id}`}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 12,
        padding: "12px 14px",
        borderBottom: divider ? "1px solid var(--border)" : "none",
      }}
    >
      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginBottom: 3 }}>
          <span style={{ fontSize: 12, fontWeight: 500, color: "var(--t0)" }}>{model.id}</span>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>·</span>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>{provider}</span>
          <span
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              color: chipColor(toolTone),
              textTransform: "uppercase",
              letterSpacing: "0.04em",
            }}
          >
            tools {toolsEnabled ? "on" : "off"}
          </span>
        </div>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
          <span>source <span style={{ color: "var(--t1)" }}>{source}</span></span>
          {capabilities?.streaming !== undefined && <span>streaming <span style={{ color: "var(--t1)" }}>{capabilities.streaming ? "yes" : "no"}</span></span>}
          {capabilities?.max_context_tokens !== undefined && <span>context <span style={{ color: "var(--t1)" }}>{capabilities.max_context_tokens.toLocaleString()}</span></span>}
        </div>
      </div>
      <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "flex-end", alignItems: "center", gap: 6 }}>
        <div
          role="group"
          aria-label={`Tools for ${model.id}`}
          style={{
            display: "inline-flex",
            alignItems: "center",
            border: "1px solid var(--border)",
            borderRadius: "999px",
            overflow: "hidden",
            background: "var(--bg0)",
            height: 30,
          }}
        >
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            aria-pressed={toolsEnabled}
            onClick={() => onToolsChange(true)}
            style={{
              border: 0,
              borderRadius: 0,
              width: 70,
              padding: "4px 0",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              background: toolsEnabled ? "var(--teal-bg)" : "transparent",
              color: toolsEnabled ? "var(--teal)" : "var(--t3)",
              justifyContent: "center",
            }}
          >
            tools on
          </button>
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            aria-pressed={!toolsEnabled}
            onClick={() => onToolsChange(false)}
            style={{
              border: 0,
              borderLeft: "1px solid var(--border)",
              borderRadius: 0,
              width: 70,
              padding: "4px 0",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              background: !toolsEnabled ? "var(--bg3)" : "transparent",
              color: !toolsEnabled ? "var(--t0)" : "var(--t3)",
              justifyContent: "center",
            }}
          >
            tools off
          </button>
        </div>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClear} disabled={clearDisabled}>
          Clear override
        </button>
      </div>
    </div>
  );
}


// ─── Retention tab ────────────────────────────────────────────────────────────

const KNOWN_SUBSYSTEMS = [
  "trace_snapshots",
  "budget_events",
  "audit_events",
  "provider_history",
  "turn_events",
  "agent_chat_approvals",
] as const;

function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function RetentionTab({ state, actions }: Props) {
  const runs = state.retentionRuns ?? [];
  const lastRun = state.retentionLastRun;
  const lastRunResults = lastRun?.results ?? [];

  // Parse CSV state into a local Set for chip toggles
  const selectedSet = new Set(
    state.retentionSubsystems
      .split(",")
      .map(s => s.trim())
      .filter(s => KNOWN_SUBSYSTEMS.includes(s as typeof KNOWN_SUBSYSTEMS[number]))
  );

  function toggleSubsystem(name: string) {
    const next = new Set(selectedSet);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    actions.setRetentionSubsystems([...next].join(","));
  }

  const totalDeleted = lastRunResults.filter(r => !r.skipped).reduce((n, r) => n + (r.deleted ?? 0), 0);
  const maxDeleted = Math.max(1, ...lastRunResults.map(r => r.deleted ?? 0));

  return (
    <>
      <SectionHeader
        title="Retention"
        description="Prune stored traces, budgets, audit events, and cache data."
        meta={`${runs.length} run${runs.length === 1 ? "" : "s"}`}
      />

      {/* Controls */}
      <div className="card" style={{ padding: "14px 16px", marginBottom: 16 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12 }}>
          <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>Subsystems to prune</span>
          <span style={{ fontSize: 11, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
            {selectedSet.size === 0 ? "all" : `${selectedSet.size} selected`}
          </span>
          <button className="btn btn-primary btn-sm" style={{ marginLeft: "auto" }}
            disabled={state.retentionLoading}
            onClick={() => void actions.runRetention()}>
            <Icon d={Icons.refresh} size={13} /> {state.retentionLoading ? "Running…" : "Run now"}
          </button>
        </div>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
          {KNOWN_SUBSYSTEMS.map(name => {
            const active = selectedSet.has(name);
            return (
              <button key={name} type="button" onClick={() => toggleSubsystem(name)}
                style={{
                  padding: "4px 10px",
                  fontFamily: "var(--font-mono)",
                  fontSize: 11,
                  borderRadius: "var(--radius-sm)",
                  border: `1px solid ${active ? "var(--teal-border)" : "var(--border)"}`,
                  background: active ? "var(--teal-bg)" : "var(--bg3)",
                  color: active ? "var(--teal)" : "var(--t2)",
                  cursor: "pointer",
                  transition: "background 0.1s, color 0.1s, border-color 0.1s",
                }}>
                {name}
              </button>
            );
          })}
        </div>
        <div style={{ fontSize: 10, color: "var(--t3)", marginTop: 8 }}>
          No selection = prune all subsystems
        </div>
        {state.retentionError && <div style={{ marginTop: 8 }}><InlineError message={state.retentionError} /></div>}
      </div>

      {/* Last run summary */}
      {lastRun && (
        <div className="card" style={{ padding: "14px 16px", marginBottom: 16 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12 }}>
            <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>Last run</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
              {relativeTime(lastRun.finished_at)}
            </span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>·</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>{lastRun.trigger}</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>·</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: totalDeleted > 0 ? "var(--teal)" : "var(--t3)" }}>
              {totalDeleted} deleted
            </span>
          </div>
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            {lastRunResults.map(r => (
              <div key={r.name} style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: r.skipped ? "var(--t3)" : "var(--t1)", width: 140, flexShrink: 0 }}>
                  {r.name}
                </span>
                {r.skipped ? (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", fontStyle: "italic" }}>skipped</span>
                ) : r.error ? (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--red)" }}>{r.error}</span>
                ) : (
                  <>
                    <div style={{ flex: 1, height: 4, background: "var(--bg3)", borderRadius: 2, overflow: "hidden" }}>
                      <div style={{
                        height: "100%",
                        width: `${Math.round((r.deleted / maxDeleted) * 100)}%`,
                        background: r.deleted > 0 ? "var(--teal)" : "var(--bg3)",
                        borderRadius: 2,
                      }} />
                    </div>
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: r.deleted > 0 ? "var(--teal)" : "var(--t3)", width: 48, textAlign: "right", flexShrink: 0 }}>
                      {r.deleted} del
                    </span>
                  </>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* History */}
      <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: 8 }}>History</div>
      {runs.length > 0 ? (
        <div className="card" style={{ overflow: "hidden" }}>
          {runs.slice(0, 20).map((r, i) => {
            const del = r.results?.filter(s => !s.skipped).reduce((n, s) => n + (s.deleted ?? 0), 0) ?? 0;
            const errored = r.results?.some(s => s.error);
            return (
              <div key={i} style={{ display: "flex", alignItems: "center", gap: 10, padding: "8px 14px", borderBottom: i < Math.min(runs.length, 20) - 1 ? "1px solid var(--border)" : "none" }}>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)", width: 70, flexShrink: 0 }}>{r.trigger}</span>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>{relativeTime(r.finished_at)}</span>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: del > 0 ? "var(--teal)" : "var(--t3)", marginLeft: "auto" }}>
                  {del} deleted
                </span>
                {errored && <Badge status="down" label="error" />}
              </div>
            );
          })}
        </div>
      ) : (
        <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
          No retention runs yet.
        </div>
      )}
    </>
  );
}


// ─── External agents tab ──────────────────────────────────────────────────────

// ExternalAgentsTab lists durable approval grants ("always allow /
// always deny" rules persisted by the approval coordinator) and lets
// the operator revoke them. Grants survive normal retention pruning;
// only ExpiresAt removes them automatically. Revoke is the only
// operator-driven removal path.
//
// Grants and adapter health are lazy-loaded on tab mount — operators
// rarely visit this surface, so we don't fetch on every dashboard
// load. Adapter probes run automatically here because this tab is a
// readiness panel; "Save" validates Claude Code auth before storing
// the token, so no separate Test button is needed.
function ExternalAgentsTab({ state, actions }: Props) {
  const liveAnthropicProvider = findAnthropicProvider(state.settingsConfig?.providers ?? []);
  const [rememberedAnthropicProvider, setRememberedAnthropicProvider] = useState<ConfiguredProviderRecord | null>(liveAnthropicProvider);
  const probedAdapterIDsRef = useRef<Set<string>>(new Set());
  const adapterIDsKey = useMemo(() => state.agentAdapters.map((adapter) => adapter.id).sort().join(","), [state.agentAdapters]);

  useEffect(() => {
    if (liveAnthropicProvider) setRememberedAnthropicProvider(liveAnthropicProvider);
  }, [liveAnthropicProvider]);

  useEffect(() => {
    for (const adapter of state.agentAdapters) {
      if (probedAdapterIDsRef.current.has(adapter.id)) continue;
      probedAdapterIDsRef.current.add(adapter.id);
      void actions.probeAgentAdapter(adapter.id);
    }
    // `actions` is a view-model object and can be re-created by parent
    // renders. Probe only when the adapter ID set changes; the ref prevents
    // duplicate probes for IDs we've already checked in this tab instance.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [adapterIDsKey]);

  useEffect(() => {
    void actions.listAgentChatGrants();
    // Grants are lazy-loaded once when the tab mounts; Refresh handles
    // explicit re-fetches.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // One-shot scroll + highlight when the operator arrived here via
  // the "Open Claude Code setup" button on a failed Claude run.
  // The chat sets `hecate.settingsFocus` in sessionStorage before
  // navigating; we read-and-clear it so subsequent visits to this
  // tab don't re-trigger the scroll.
  //
  // The focus target is validated against a small allowlist before
  // it's interpolated into a DOM lookup — that avoids any selector-
  // injection class from an unexpected sessionStorage value (which
  // could happen via a stale entry, a third-party extension writing
  // into the same key, or a forward-compat token a newer build set
  // that this build doesn't know about). Add new targets to the
  // KNOWN_FOCUS_TARGETS set when more callers wire one in.
  useEffect(() => {
    const KNOWN_FOCUS_TARGETS = new Set(["claude-code-guided-setup"]);
    let focusTarget: string | null = null;
    try {
      const raw = sessionStorage.getItem("hecate.settingsFocus");
      if (raw) sessionStorage.removeItem("hecate.settingsFocus");
      if (raw && KNOWN_FOCUS_TARGETS.has(raw)) focusTarget = raw;
    } catch {
      // sessionStorage unavailable — nothing to focus.
    }
    if (!focusTarget) return;
    const target = focusTarget; // narrow for the inner closure
    // Defer one frame so the card has rendered before we measure
    // it. Track both timers so an unmount mid-flash doesn't leak
    // or run the class-removal against a detached node.
    let removeHandle: number | null = null;
    const startHandle = window.setTimeout(() => {
      const card = document.querySelector(`[data-testid="${target}"]`);
      if (!card) return;
      card.scrollIntoView({ behavior: "smooth", block: "center" });
      // Brief highlight so the operator's eye lands on it. Class is
      // toggled rather than inlined so the styling lives in CSS.
      card.classList.add("settings-focus-flash");
      removeHandle = window.setTimeout(() => {
        card.classList.remove("settings-focus-flash");
      }, 2200);
    }, 0);
    return () => {
      window.clearTimeout(startHandle);
      if (removeHandle !== null) window.clearTimeout(removeHandle);
    };
  }, []);

  const grants = state.agentChatGrants;
  const loading = state.agentChatGrantsLoading;
  const error = state.agentChatGrantsError;

  return (
    <>
      {rememberedAnthropicProvider && (
        <AnthropicProviderKeyCard
          provider={rememberedAnthropicProvider}
          onSave={(key) => actions.setProviderAPIKey(rememberedAnthropicProvider.id, key)}
          onClear={() => actions.setProviderAPIKey(rememberedAnthropicProvider.id, "")}
        />
      )}

      <AdapterStatusSection state={state} actions={actions} />

      <SectionHeader
        title="External agent grants"
        description="Durable “always allow / always deny” rules persisted by the approval coordinator. Revoke removes a grant immediately and doesn't undo decisions already applied to in-flight calls."
        meta={`${grants.length} grant${grants.length === 1 ? "" : "s"}`}
        actions={
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={() => void actions.listAgentChatGrants()}
            disabled={loading}
            data-testid="external-agents-refresh"
          >
            <Icon d={Icons.refresh} size={13} /> {loading ? "Loading…" : "Refresh"}
          </button>
        }
      />

      {error && (
        <div style={{ marginBottom: 12 }}>
          <InlineError message={error} />
        </div>
      )}

      {grants.length === 0 ? (
        <div
          className="card"
          style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}
          data-testid="external-agents-empty"
        >
          {loading ? "Loading grants…" : "No grants yet. Approvals stay scoped to a single call until an operator picks a broader scope."}
        </div>
      ) : (
        <div className="card" style={{ overflow: "hidden" }} data-testid="external-agents-list">
          {grants.map((g, i) => (
            <GrantRow
              key={g.id}
              grant={g}
              divider={i < grants.length - 1}
              onRevoke={() => void actions.deleteAgentChatGrant(g.id)}
            />
          ))}
        </div>
      )}
    </>
  );
}

function findAnthropicProvider(providers: ConfiguredProviderRecord[]): ConfiguredProviderRecord | null {
  return providers.find((provider) => (
    provider.id === "anthropic" ||
    provider.preset_id === "anthropic" ||
    provider.protocol === "anthropic"
  )) ?? null;
}

function AnthropicProviderKeyCard({
  provider,
  onSave,
  onClear,
}: {
  provider: ConfiguredProviderRecord;
  onSave: (key: string) => Promise<void>;
  onClear: () => Promise<void>;
}) {
  const [key, setKey] = useState("");
  const [saving, setSaving] = useState(false);
  const configured = provider.credential_configured;

  async function save() {
    const trimmed = key.trim();
    if (!trimmed) return;
    setSaving(true);
    try {
      await onSave(trimmed);
      setKey("");
    } finally {
      setSaving(false);
    }
  }

  async function clear() {
    setSaving(true);
    try {
      await onClear();
      setKey("");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div
      className="card"
      data-testid="anthropic-provider-key-card"
      style={{
        marginBottom: 24,
        padding: "14px 16px",
        borderColor: configured ? "rgba(0, 191, 179, 0.34)" : "var(--border)",
        background: configured ? "rgba(0, 191, 179, 0.04)" : "var(--bg1)",
      }}
    >
      <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginBottom: 6 }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: "var(--t0)" }}>Anthropic provider key</span>
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: configured ? "var(--teal)" : "var(--t3)",
            textTransform: "uppercase",
            letterSpacing: "0.04em",
          }}
        >
          {configured ? "saved" : "missing"}
        </span>
      </div>
      <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45, marginBottom: 12 }}>
        Used by Hecate Chat and direct Anthropic provider calls through {provider.name || "Anthropic"}. This is separate from the Claude Code adapter token below.
      </div>
      <div style={{ display: "flex", gap: 8 }}>
        <input
          value={key}
          onChange={(event) => setKey(event.target.value)}
          placeholder={configured ? "Paste a new Anthropic API key" : "Paste Anthropic API key"}
          type="password"
          className="input"
          style={{ flex: 1, minWidth: 220 }}
          aria-label="Anthropic API key"
        />
        <button type="button" className="btn btn-primary btn-sm" onClick={() => void save()} disabled={saving || key.trim() === ""}>
          {saving ? "Saving..." : configured ? "Update key" : "Save key"}
        </button>
        {configured && (
          <button type="button" className="btn btn-danger btn-sm" onClick={() => void clear()} disabled={saving}>
            Remove
          </button>
        )}
      </div>
    </div>
  );
}

// AdapterStatusSection lists the configured external-agent adapters.
// The parent tab auto-runs actions.probeAgentAdapter on mount, which
// spawns each adapter, completes the ACP handshake, and returns a
// typed health classification. For Claude Code, that handshake is
// also the auth check: auth failures surface as `auth_required`.
// Probe state is cached on the hook so leaving and returning to the
// tab doesn't lose the last-known status.
//
// The section is read-only otherwise: adapter discovery and
// availability are still owned by the dashboard fan-out's
// /hecate/v1/agent-adapters response. We just surface the additional
// per-adapter "can I actually use this?" check here.
function AdapterStatusSection({ state, actions }: Props) {
  const adapters = state.agentAdapters;
  if (!adapters || adapters.length === 0) {
    return null;
  }
  return (
    <div style={{ marginBottom: 24 }} data-testid="external-agents-adapters">
      <SectionHeader
        title="Adapters"
        description="Checks adapter readiness and auth by starting the adapter, completing the ACP handshake, and creating a session. Auth-required failures show here before a chat fails."
        meta={`${adapters.length} adapter${adapters.length === 1 ? "" : "s"}`}
      />
      <div className="card" style={{ overflow: "hidden" }}>
        {adapters.map((adapter, i) => (
          <AdapterStatusRow
            key={adapter.id}
            adapter={adapter}
            divider={i < adapters.length - 1}
            health={state.agentAdapterHealthByID.get(adapter.id) ?? null}
            loading={Boolean(state.agentAdapterHealthLoadingByID.get(adapter.id))}
            onSaveCredential={(value) => actions.setAgentAdapterCredential(adapter.id, value, "CLAUDE_CODE_OAUTH_TOKEN")}
            onDeleteCredential={() => actions.deleteAgentAdapterCredential(adapter.id, "CLAUDE_CODE_OAUTH_TOKEN")}
            onCopyCommand={() => void actions.copyCommand(claudeCodeSettingsTokenCommand(adapter))}
          />
        ))}
      </div>
    </div>
  );
}

function claudeCodeSettingsTokenCommand(adapter: AgentAdapterRecord): string {
  const command = adapter.claude_code_cli?.command;
  if (command) return `${command} setup-token`;
  return "npx -y @anthropic-ai/claude-code setup-token";
}

function AdapterStatusRow({
  adapter,
  divider,
  health,
  loading,
  onSaveCredential,
  onDeleteCredential,
  onCopyCommand,
}: {
  adapter: AgentAdapterRecord;
  divider: boolean;
  health: AgentAdapterHealthRecord | null;
  loading: boolean;
  onSaveCredential: (value: string) => Promise<boolean>;
  onDeleteCredential: () => Promise<boolean>;
  onCopyCommand: () => void;
}) {
  // Two status sources: the dashboard's /hecate/v1/agent-adapters availability
  // (binary discovery only) and the on-demand probe (full handshake).
  // Probe wins when present — it's a strictly more informative signal.
  const dashboardChip = adapter.available ? null : { tone: "amber" as const, label: "missing" };
  const probeChip = health ? probeStatusChip(health.status) : null;
  const chip = probeChip ?? dashboardChip;
  const probeVerifiedAuth = health?.status === "ready";
  const displayAuthStatus = probeVerifiedAuth ? "ok" : adapter.auth_status;
  const displayAuthError = probeVerifiedAuth ? "" : adapter.auth_error;
  const adapterInstalled = adapter.available && health?.status !== "not_installed";
  const tokenVerified = Boolean(adapter.credential_configured) && probeVerifiedAuth;

  return (
    <div
      data-testid={`external-agents-adapter-${adapter.id}`}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 10,
        padding: "10px 14px",
        borderBottom: divider ? "1px solid var(--border)" : "none",
      }}
    >
      <BrandAvatar brand={adapter.id} fallback={adapter.name} size={28} />
      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginBottom: 2 }}>
          <span style={{ fontSize: 12, fontWeight: 500, color: "var(--t0)" }}>{adapter.name}</span>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>·</span>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>{adapter.id}</span>
          {chip && (
            <span
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: chipColor(chip.tone),
                textTransform: "uppercase",
                letterSpacing: "0.04em",
              }}
            >
              {chip.label}
            </span>
          )}
          {adapter.version_outside_range && (
            <span
              data-testid={`external-agents-adapter-${adapter.id}-version-warning`}
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: chipColor("amber"),
                textTransform: "uppercase",
                letterSpacing: "0.04em",
              }}
            >
              outside tested range
            </span>
          )}
          {displayAuthStatus && displayAuthStatus !== "ok" && (
            <span
              data-testid={`external-agents-adapter-${adapter.id}-auth-warning`}
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: displayAuthStatus === "billing" ? chipColor("red") : chipColor("amber"),
                textTransform: "uppercase",
                letterSpacing: "0.04em",
              }}
              title={displayAuthError || undefined}
            >
              {displayAuthStatus === "billing" ? "billing" : displayAuthStatus === "unauthenticated" ? "auth required" : "auth unknown"}
            </span>
          )}
        </div>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
          {adapter.command && <span>command <span style={{ color: "var(--t1)" }}>{adapter.command}</span></span>}
          {adapter.version && <span>version <span style={{ color: "var(--t1)" }}>{adapter.version}</span></span>}
          {displayAuthStatus && <span>auth <span style={{ color: "var(--t1)" }}>{displayAuthStatus}</span></span>}
          {health?.path && <span>path <span style={{ color: "var(--t1)" }}>{health.path}</span></span>}
          {health?.duration_ms !== undefined && <span>{health.duration_ms} ms</span>}
        </div>
        {health && (health.hint || health.error) && (
          <div
            data-testid={`external-agents-adapter-${adapter.id}-detail`}
            style={{
              marginTop: 6,
              fontSize: 11,
              color: chipColor(probeStatusChip(health.status)?.tone ?? "muted"),
              lineHeight: 1.4,
              wordBreak: "break-word",
            }}
          >
            {health.hint && <div>{health.hint}</div>}
            {health.error && (
              <div style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", marginTop: 2 }}>
                {health.error}
              </div>
            )}
          </div>
        )}
        {!health && displayAuthError && (
          <div
            data-testid={`external-agents-adapter-${adapter.id}-auth-detail`}
            style={{ marginTop: 6, fontSize: 11, color: "var(--t3)", lineHeight: 1.4 }}
          >
            {displayAuthError}
          </div>
        )}
        {adapter.id === "claude_code" && (
          <ClaudeCredentialSetup
            configured={Boolean(adapter.credential_configured)}
            preview={adapter.credential_preview}
            adapterReady={adapterInstalled}
            tokenVerified={tokenVerified}
            cliSignedIn={adapter.auth_status === "ok"}
            health={health ?? undefined}
            onCopyCommand={onCopyCommand}
            onSave={onSaveCredential}
            onDelete={onDeleteCredential}
          />
        )}
      </div>
      {loading && (
        <span
          data-testid={`external-agents-checking-${adapter.id}`}
          style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)", whiteSpace: "nowrap" }}
        >
          checking…
        </span>
      )}
    </div>
  );
}

function ClaudeCredentialSetup({
  configured,
  preview,
  adapterReady,
  tokenVerified,
  cliSignedIn,
  health,
  onCopyCommand,
  onSave,
  onDelete,
}: {
  configured: boolean;
  preview?: string;
  adapterReady: boolean;
  tokenVerified: boolean;
  cliSignedIn: boolean;
  health?: AgentAdapterHealthRecord;
  onCopyCommand: () => void;
  onSave: (value: string) => Promise<boolean>;
  onDelete: () => Promise<boolean>;
}) {
  const [token, setToken] = useState("");
  const [saving, setSaving] = useState(false);
  const [removing, setRemoving] = useState(false);
  const tone = tokenVerified ? "green" : health?.status === "error" ? "red" : "amber";
  const accent = chipColor(tone);
  const border = tokenVerified
    ? "rgba(0, 191, 179, 0.32)"
    : tone === "red"
      ? "rgba(239, 68, 68, 0.3)"
      : "rgba(245, 158, 11, 0.28)";
  const background = tokenVerified
    ? "rgba(0, 191, 179, 0.07)"
    : tone === "red"
      ? "rgba(239, 68, 68, 0.07)"
      : "rgba(245, 158, 11, 0.06)";
  async function save() {
    const trimmed = token.trim();
    if (!trimmed) return;
    setSaving(true);
    try {
      if (await onSave(trimmed)) {
        setToken("");
      }
    } finally {
      setSaving(false);
    }
  }

  async function remove() {
    setRemoving(true);
    try {
      await onDelete();
    } finally {
      setRemoving(false);
    }
  }

  return (
    <div
      data-testid="claude-code-guided-setup"
      style={{
        marginTop: 10,
        padding: 10,
        border: `1px solid ${border}`,
        borderRadius: 10,
        background,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 10, marginBottom: 8 }}>
        <div>
          <div style={{ display: "flex", alignItems: "center", gap: 7, fontSize: 11, fontWeight: 600, color: accent }}>
            <Icon d={tokenVerified ? Icons.check : Icons.keys} size={12} />
            {tokenVerified ? "Claude Code token verified" : configured ? "Claude Code token saved" : "Claude Code guided setup"}
          </div>
          <div style={{ fontSize: 11, color: "var(--t2)", lineHeight: 1.4 }}>
            {tokenVerified
              ? "Hecate has a validated adapter token and can inject it only into Claude ACP sessions. You can still paste a replacement token below."
              : configured
                ? "Hecate has a token saved, but Claude Code auth has not been verified yet."
                : cliSignedIn
                  ? "Claude Code is signed in for normal CLI use, but Hecate still needs its own adapter token. Run claude setup-token and paste the token here."
                  : "Run claude setup-token, paste the token here, then Hecate injects it only into Claude ACP."}
          </div>
          <div style={{ marginTop: 5, display: "flex", flexWrap: "wrap", gap: 8, fontFamily: "var(--font-mono)", fontSize: 10 }}>
            <span style={{ color: adapterReady ? "var(--teal)" : "var(--t3)" }}>
              adapter {adapterReady ? "installed" : "not verified"}
            </span>
            <span style={{ color: tokenVerified ? "var(--teal)" : "var(--amber)" }}>
              token {tokenVerified ? "valid" : configured ? "saved, needs check" : "not saved"}
            </span>
            {cliSignedIn && !tokenVerified && (
              <span style={{ color: "var(--t3)" }}>CLI signed in</span>
            )}
          </div>
        </div>
        {!tokenVerified && (
          <button type="button" className="btn btn-ghost btn-sm" onClick={onCopyCommand}>
            Copy command
          </button>
        )}
      </div>
      {configured && (
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 8, fontSize: 11, color: "var(--t2)" }}>
          <span>Stored token {preview ? <span style={{ fontFamily: "var(--font-mono)", color: "var(--t1)" }}>{preview}</span> : "configured"}</span>
          {tokenVerified && <span style={{ color: "var(--teal)" }}>Token valid.</span>}
          {!tokenVerified && health?.status === "auth_required" && <span style={{ color: "var(--amber)" }}>Token saved, but Claude still reports auth required.</span>}
          {!tokenVerified && health?.status === "error" && <span style={{ color: "var(--red)" }}>Token saved, but the auth check failed.</span>}
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => void remove()} disabled={removing}>
            {removing ? "Removing..." : "Remove"}
          </button>
        </div>
      )}
      <div style={{ display: "flex", gap: 8 }}>
        <input
          value={token}
          onChange={(event) => setToken(event.target.value)}
          placeholder={configured ? "Paste a replacement CLAUDE_CODE_OAUTH_TOKEN" : "Paste CLAUDE_CODE_OAUTH_TOKEN"}
          type="password"
          className="input"
          style={{ flex: 1, minWidth: 180 }}
          aria-label="Claude Code OAuth token"
        />
        <button type="button" className="btn btn-primary btn-sm" onClick={() => void save()} disabled={saving || token.trim() === ""}>
          {saving ? "Checking auth..." : "Save"}
        </button>
      </div>
    </div>
  );
}

type ChipTone = "green" | "amber" | "red" | "muted";

function probeStatusChip(status: string): { tone: ChipTone; label: string } | null {
  switch (status) {
    case "ready":         return { tone: "green", label: "ready" };
    case "auth_required": return { tone: "amber", label: "auth required" };
    case "not_installed": return { tone: "amber", label: "not installed" };
    case "error":         return { tone: "red",   label: "error" };
    default:              return null;
  }
}

function chipColor(tone: ChipTone): string {
  switch (tone) {
    case "green": return "var(--teal)";
    case "amber": return "var(--amber)";
    case "red":   return "var(--red)";
    case "muted": return "var(--t3)";
  }
}

function GrantRow({
  grant,
  divider,
  onRevoke,
}: {
  grant: AgentChatGrantRecord;
  divider: boolean;
  onRevoke: () => void;
}) {
  const [confirmingRevoke, setConfirmingRevoke] = useState(false);
  const decisionTone = grant.decision === "approve"
    ? { color: "var(--teal)", label: "always approve" }
    : grant.decision === "deny"
      ? { color: "var(--red)", label: "always deny" }
      : { color: "var(--t2)", label: grant.decision };
  const expiresLabel = grant.expires_at
    ? `expires ${new Date(grant.expires_at).toLocaleString()}`
    : "no expiry";

  return (
    <div
      data-testid={`external-agents-row-${grant.id}`}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 10,
        padding: "10px 14px",
        borderBottom: divider ? "1px solid var(--border)" : "none",
      }}
    >
      <BrandAvatar brand={grant.adapter_id} fallback={grant.adapter_id} size={26} />
      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginBottom: 2 }}>
          <span style={{ fontSize: 12, fontWeight: 500, color: "var(--t0)" }}>{grant.adapter_id}</span>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>·</span>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t1)" }}>{grant.tool_kind}</span>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: decisionTone.color, textTransform: "uppercase", letterSpacing: "0.04em" }}>
            {decisionTone.label}
          </span>
        </div>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
          <span>scope <span style={{ color: "var(--t1)" }}>{grant.scope}</span></span>
          {grant.workspace && <span>workspace <span style={{ color: "var(--t1)" }}>{grant.workspace}</span></span>}
          {grant.session_id && <span>session <span style={{ color: "var(--t1)" }}>{grant.session_id}</span></span>}
          {grant.granted_by && <span>by <span style={{ color: "var(--t1)" }}>{grant.granted_by}</span></span>}
          <span>{new Date(grant.granted_at).toLocaleString()}</span>
          <span>{expiresLabel}</span>
        </div>
      </div>
      {confirmingRevoke ? (
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <button
            type="button"
            className="btn btn-danger btn-sm"
            onClick={onRevoke}
            title="Confirm revoke"
            data-testid={`external-agents-confirm-revoke-${grant.id}`}
          >
            Revoke?
          </button>
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={() => setConfirmingRevoke(false)}
            title="Cancel revoke"
            data-testid={`external-agents-cancel-revoke-${grant.id}`}
          >
            Cancel
          </button>
        </div>
      ) : (
        <button
          type="button"
          className="btn btn-ghost btn-sm"
          onClick={() => setConfirmingRevoke(true)}
          title="Revoke this grant"
          data-testid={`external-agents-revoke-${grant.id}`}
          style={{ color: "var(--red)" }}
        >
          <Icon d={Icons.trash} size={13} /> Revoke
        </button>
      )}
    </div>
  );
}

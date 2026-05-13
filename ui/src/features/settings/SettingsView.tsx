import { useMemo, useState, type ReactNode } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import type { ModelRecord } from "../../types/runtime";
import { Badge, Icon, Icons, InlineError } from "../shared/ui";
import { PricebookTab } from "./PricebookTab";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
  onNavigate?: (workspace: "providers" | "runs" | "overview" | "settings" | "chats" | "costs") => void;
};

// Visible settings sub-tabs. Connections is now a top-level workspace;
// Settings stays focused on configuration that does not belong to a
// runtime connection surface. Balances and usage live in the Costs
// workspace.
const TABS = ["model_capabilities", "pricebook", "retention"] as const;
type Tab = (typeof TABS)[number];
const TAB_LABELS: Record<Tab, string> = {
  pricebook: "Pricing",
  model_capabilities: "Model capabilities",
  retention: "Retention",
};

const TAB_STORAGE_KEY = "hecate.settingsTab";

export function SettingsView({ state, actions }: Props) {
  // Persist the settings sub-tab so refreshing while on (say) Pricebook
  // returns the operator to Pricebook.
  const [tab, setTabRaw] = useState<Tab>(() => {
    const saved = localStorage.getItem(TAB_STORAGE_KEY);
    if (saved === "external_agents" || saved === "connections") return TABS[0];
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

type ChipTone = "green" | "amber" | "red" | "muted";

function chipColor(tone: ChipTone): string {
  if (tone === "green") return "var(--green)";
  if (tone === "amber") return "var(--amber)";
  if (tone === "red") return "var(--red)";
  return "var(--t3)";
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

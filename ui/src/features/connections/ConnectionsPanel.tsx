import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import { providerFleetRepairHint, providerReadinessMeaning, providerRepairActionLabel } from "../../lib/provider-readiness";
import type { AgentAdapterHealthRecord, AgentAdapterRecord, AgentChatGrantRecord, ConfiguredProviderRecord, ProviderRecord } from "../../types/runtime";
import { BrandAvatar, Icon, Icons, InlineError } from "../shared/ui";
import { ModelCapabilitiesSection } from "./ModelCapabilitiesSection";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
  onNavigate?: (workspace: "providers" | "runs" | "overview" | "settings" | "chats" | "costs") => void;
};

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

// ─── Connections panel ────────────────────────────────────────────────────────

// ConnectionsPanel gathers the external-agent setup surfaces that sit next
// to model-provider CRUD in the Connections workspace. It intentionally
// remains exported for reuse by ProvidersView while Settings stays focused
// on pricing, retention, and other non-connection configuration.
//
// Grants and adapter health are lazy-loaded on panel mount — operators
// rarely visit this surface, so we don't fetch on every dashboard
// load. Adapter probes run automatically here because this panel is a
// readiness panel; "Save" validates Claude Code auth before storing
// the token, so no separate Test button is needed.
export function ConnectionsPanel({
  state,
  actions,
  onNavigate,
  showProviderSummary = true,
}: Props & { showProviderSummary?: boolean }) {
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
  // Chat sets `hecate.connectionsFocus` in sessionStorage before
  // navigating; we read-and-clear it so subsequent visits don't
  // re-trigger the scroll.
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
      const raw = sessionStorage.getItem("hecate.connectionsFocus")
        || sessionStorage.getItem("hecate.settingsFocus");
      if (raw) {
        sessionStorage.removeItem("hecate.connectionsFocus");
        sessionStorage.removeItem("hecate.settingsFocus");
      }
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
      {showProviderSummary && <ModelProviderConnectionsSection state={state} onNavigate={onNavigate} />}

      <ModelCapabilitiesSection state={state} actions={actions} />

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

function ModelProviderConnectionsSection({
  state,
  onNavigate,
}: {
  state: RuntimeConsoleViewModel["state"];
  onNavigate?: Props["onNavigate"];
}) {
  const configuredProviders = state.settingsConfig?.providers ?? [];
  const configuredProviderIDs = new Set(configuredProviders.map((provider) => provider.id));
  const knownStatuses = state.providers.filter((provider) => configuredProviderIDs.has(provider.name));
  const readyProviders = knownStatuses.filter(isProviderReady).length;
  const blockedProviders = knownStatuses.filter(isProviderBlocked).length;
  const modelCount = state.models.length || knownStatuses.reduce((sum, provider) => sum + (provider.model_count ?? provider.models?.length ?? 0), 0);
  const statusByName = new Map(state.providers.map((provider) => [provider.name, provider]));
  const repair = providerFleetRepairHint(configuredProviders, statusByName);
  const repairLabel = repair?.tone === "muted" ? "Ready for chat" : "Next repair";
  const repairButton = providerRepairButtonLabel(repair);
  const readinessSummary = providerReadinessMeaning({
    configuredCount: configuredProviders.length,
    readyCount: readyProviders,
    blockedCount: blockedProviders,
    modelCount,
    repair,
  });

  return (
    <div className="card" style={{ padding: "14px 16px", marginBottom: 24 }} data-testid="connections-model-providers">
      <SectionHeader
        title="Model providers"
        description="Cloud and local model endpoints used by Hecate Chat, direct model chat, routing, pricebook, and cost controls."
        meta={`${configuredProviders.length} configured`}
        actions={
          onNavigate ? (
            <button type="button" className="btn btn-primary btn-sm" onClick={() => onNavigate("providers")}>
              Open Connections
            </button>
          ) : undefined
        }
      />
      {repair && (
        <div
          data-testid="connections-provider-repair"
          style={{
            marginBottom: 12,
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-sm)",
            background: repair.tone === "amber" ? "var(--amber-bg)" : "var(--bg2)",
            padding: "9px 10px",
          }}
        >
          <div style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 3 }}>
            <span style={{
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              letterSpacing: "0.04em",
              textTransform: "uppercase",
              color: repair.tone === "amber" ? "var(--amber)" : "var(--green)",
              whiteSpace: "nowrap",
            }}>
              {repairLabel}
            </span>
            <span style={{ fontSize: 11, fontWeight: 600, color: repair.tone === "amber" ? "var(--amber)" : "var(--t1)" }}>
              {repair.title}
            </span>
            {repairButton && onNavigate && (
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => onNavigate("providers")}
                style={{ marginLeft: "auto", padding: "2px 7px" }}
              >
                {repairButton}
              </button>
            )}
          </div>
          <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>{repair.message}</div>
          <div style={{ marginTop: 5, fontSize: 10, color: repair.tone === "amber" ? "var(--amber)" : "var(--t3)", fontFamily: "var(--font-mono)" }}>
            {repair.tone === "muted" ? "Status" : "Next"} ·{" "}
            <span style={{ color: repair.tone === "muted" ? "var(--t3)" : "var(--amber)" }}>
              {repair.action}
            </span>
          </div>
        </div>
      )}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(135px, 1fr))",
          gap: 10,
        }}
      >
        <ConnectionStat label="Configured" value={String(configuredProviders.length)} hint="provider records" />
        <ConnectionStat label="Ready" value={String(readyProviders)} hint="routing-ready" tone={readyProviders > 0 ? "green" : "muted"} />
        <ConnectionStat label="Needs attention" value={String(blockedProviders)} hint="blocked providers" tone={blockedProviders > 0 ? "amber" : "muted"} />
        <ConnectionStat label="Models" value={String(modelCount)} hint="discovered" />
      </div>
      <div
        data-testid="connections-provider-readiness-meaning"
        style={{
          marginTop: 10,
          fontSize: 11,
          color: readinessSummary.tone === "amber" ? "var(--amber)" : "var(--t3)",
          lineHeight: 1.45,
        }}
      >
        {readinessSummary.message}
      </div>
    </div>
  );
}

function providerRepairButtonLabel(hint: ReturnType<typeof providerFleetRepairHint>): string | null {
  if (!hint || hint.tone === "muted") return null;
  return providerRepairActionLabel(hint.actionKind);
}

function ConnectionStat({
  label,
  value,
  hint,
  tone = "muted",
}: {
  label: string;
  value: string;
  hint: string;
  tone?: ChipTone;
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
      <div style={{ fontFamily: "var(--font-mono)", fontSize: 18, fontWeight: 700, color: chipColor(tone), lineHeight: 1 }}>
        {value}
      </div>
      <div style={{ fontSize: 10, color: "var(--t3)", marginTop: 6 }}>{hint}</div>
    </div>
  );
}

function isProviderReady(provider: ProviderRecord): boolean {
  if (provider.readiness?.status) {
    return provider.readiness.status === "ok" || provider.readiness.status === "warning";
  }
  return Boolean(provider.routing_ready || provider.healthy);
}

function isProviderBlocked(provider: ProviderRecord): boolean {
  if (provider.readiness?.status) {
    return provider.readiness.status === "blocked";
  }
  return Boolean(provider.routing_blocked_reason || (!provider.healthy && provider.status !== "pending"));
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
            onCopyCommand={() => void actions.copyCommand(claudeCodeSetupTokenCommand(adapter))}
          />
        ))}
      </div>
    </div>
  );
}

function claudeCodeSetupTokenCommand(adapter: AgentAdapterRecord): string {
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

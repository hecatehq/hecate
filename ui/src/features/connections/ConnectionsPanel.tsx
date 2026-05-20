import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useApprovals } from "../../app/state/approvals";
import { useChatActions } from "../../app/state/coordinators/chat";
import { useAgentAdapterActions } from "../../app/state/coordinators/agentAdapters";
import {
  useWiredProviderActions,
  useWiredSettingsActions,
} from "../../app/state/coordinators/wired";
import { useChatTarget } from "../../app/state/derived";
import {
  useProvidersAndModels,
  type ProvidersAndModelsState,
} from "../../app/state/providersAndModels";
import { useRuntime } from "../../app/state/runtime";
import { useSettings } from "../../app/state/settings";
import { formatLocaleDateTime } from "../../lib/format";
import {
  providerFleetRepairHint,
  providerReadinessMeaning,
  providerRepairActionLabel,
} from "../../lib/provider-readiness";
import type { AgentAdapterHealthRecord, AgentAdapterRecord } from "../../types/agent-adapter";
import type { ChatGrantRecord } from "../../types/chat";
import type { ModelRecord } from "../../types/model";
import type {
  ConfiguredProviderRecord,
  ConfiguredStateResponse,
  ProviderRecord,
} from "../../types/provider";
import { BrandAvatar, Icon, Icons, InlineError } from "../shared/ui";

type Props = {
  onNavigate?: (
    workspace: "connections" | "runs" | "overview" | "settings" | "chats" | "usage",
  ) => void;
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
        <div
          style={{
            fontSize: 13,
            fontWeight: 500,
            color: "var(--t0)",
            marginBottom: description ? 3 : 0,
          }}
        >
          {title}
        </div>
        {description && (
          <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>{description}</div>
        )}
      </div>
      {meta && (
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            color: "var(--t3)",
            whiteSpace: "nowrap",
          }}
        >
          {meta}
        </span>
      )}
      {actions && (
        <div style={{ marginLeft: "auto", display: "flex", gap: 8, alignItems: "center" }}>
          {actions}
        </div>
      )}
    </div>
  );
}

// ─── Connections panel ────────────────────────────────────────────────────────

// ConnectionsPanel gathers the external-agent setup surfaces that sit next
// to model-provider CRUD in the Connections workspace. It intentionally
// remains exported for reuse by ProvidersView while Settings stays focused
// on retention and other non-connection configuration.
//
// Grants and adapter health are lazy-loaded on panel mount — operators
// rarely visit this surface, so we don't fetch on every dashboard
// load. Adapter probes run automatically here because this panel is a
// readiness panel; explicit probe actions let operators re-check local
// CLI auth after installing or signing in to an external agent.
export function ConnectionsPanel({
  onNavigate,
  showProviderSummary = true,
}: Props & { showProviderSummary?: boolean }) {
  const settings = useSettings();
  const providersAndModels = useProvidersAndModels();
  const approvals = useApprovals();
  const runtime = useRuntime();
  const providerActions = useWiredProviderActions();
  const { actions: settingsActions } = useWiredSettingsActions();
  const agentAdapterActions = useAgentAdapterActions({
    setNoticeMessage: settingsActions.setNoticeMessage,
  });
  const chatActions = useChatActions({
    chatTarget: useChatTarget(),
    setNoticeMessage: settingsActions.setNoticeMessage,
  });
  const settingsConfig = settings.state.config;
  const models = providersAndModels.state.models;
  const providers = providersAndModels.state.providers;
  const agentAdapters = providersAndModels.state.agentAdapters;
  const agentAdapterHealthByID = providersAndModels.state.agentAdapterHealthByID;
  const agentAdapterHealthLoadingByID = providersAndModels.state.agentAdapterHealthLoadingByID;
  const chatGrants = approvals.state.grants;
  const chatGrantsLoading = approvals.state.grantsLoading;
  const chatGrantsError = approvals.state.grantsError;
  const liveAnthropicProvider = findAnthropicProvider(settingsConfig?.providers ?? []);
  const [rememberedAnthropicProvider, setRememberedAnthropicProvider] =
    useState<ConfiguredProviderRecord | null>(liveAnthropicProvider);
  const probedAdapterIDsRef = useRef<Set<string>>(new Set());
  const adapterIDsKey = useMemo(
    () =>
      agentAdapters
        .map((adapter) => adapter.id)
        .sort()
        .join(","),
    [agentAdapters],
  );
  const listChatGrants = approvals.actions.loadGrants;
  const probeAgentAdapter = agentAdapterActions.probeAgentAdapter;

  useEffect(() => {
    if (liveAnthropicProvider) setRememberedAnthropicProvider(liveAnthropicProvider);
  }, [liveAnthropicProvider]);

  useEffect(() => {
    for (const adapter of agentAdapters) {
      if (probedAdapterIDsRef.current.has(adapter.id)) continue;
      probedAdapterIDsRef.current.add(adapter.id);
      void probeAgentAdapter(adapter.id);
    }
    // `probeAgentAdapter` is a coordinator action that can be re-created
    // by parent renders. Probe only when the adapter ID set changes; the
    // ref prevents duplicate probes for IDs we've already checked.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [adapterIDsKey]);

  useEffect(() => {
    void listChatGrants();
    // Grants are lazy-loaded once when the tab mounts; Refresh handles
    // explicit re-fetches.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // One-shot scroll + highlight when the operator arrived here via
  // an External Agent setup button on a failed agent run.
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
    const KNOWN_FOCUS_TARGETS = new Set(["external-agent-auth-setup"]);
    let focusTarget: string | null = null;
    try {
      const raw =
        sessionStorage.getItem("hecate.connectionsFocus") ||
        sessionStorage.getItem("hecate.settingsFocus");
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

  // Adapter status uses the runtime slice for copyCommand because
  // clipboard writes are side-effects, not session mutations.
  const copyCommand = runtime.actions.copyCommand;

  return (
    <>
      {showProviderSummary && (
        <ModelProviderConnectionsSection
          settingsConfig={settingsConfig}
          providers={providers}
          models={models}
          onNavigate={onNavigate}
        />
      )}

      {rememberedAnthropicProvider && (
        <AnthropicProviderKeyCard
          provider={rememberedAnthropicProvider}
          onSave={(key) => providerActions.setProviderAPIKey(rememberedAnthropicProvider.id, key)}
          onClear={() => providerActions.setProviderAPIKey(rememberedAnthropicProvider.id, "")}
        />
      )}

      <AdapterStatusSection
        agentAdapters={agentAdapters}
        agentAdapterHealthByID={agentAdapterHealthByID}
        agentAdapterHealthLoadingByID={agentAdapterHealthLoadingByID}
        copyCommand={copyCommand}
        onProbeAdapter={(adapterID) => void probeAgentAdapter(adapterID)}
      />

      <SectionHeader
        title="External agent grants"
        description="Durable “always allow / always deny” rules persisted by the approval coordinator. Revoke removes a grant immediately and doesn't undo decisions already applied to in-flight calls."
        meta={`${chatGrants.length} grant${chatGrants.length === 1 ? "" : "s"}`}
        actions={
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={() => void listChatGrants()}
            disabled={chatGrantsLoading}
            data-testid="external-agents-refresh"
          >
            <Icon d={Icons.refresh} size={13} /> {chatGrantsLoading ? "Loading…" : "Refresh"}
          </button>
        }
      />

      {chatGrantsError && (
        <div style={{ marginBottom: 12 }}>
          <InlineError message={chatGrantsError} />
        </div>
      )}

      {chatGrants.length === 0 ? (
        <div
          className="card"
          style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}
          data-testid="external-agents-empty"
        >
          {chatGrantsLoading
            ? "Loading grants…"
            : "No grants yet. Approvals stay scoped to a single call until an operator picks a broader scope."}
        </div>
      ) : (
        <div className="card" style={{ overflow: "hidden" }} data-testid="external-agents-list">
          {chatGrants.map((g, i) => (
            <GrantRow
              key={g.id}
              grant={g}
              divider={i < chatGrants.length - 1}
              onRevoke={() => void chatActions.deleteChatGrant(g.id)}
            />
          ))}
        </div>
      )}
    </>
  );
}

function ModelProviderConnectionsSection({
  settingsConfig,
  providers,
  models,
  onNavigate,
}: {
  settingsConfig: ConfiguredStateResponse["data"] | null;
  providers: ProviderRecord[];
  models: ModelRecord[];
  onNavigate?: Props["onNavigate"];
}) {
  const configuredProviders = settingsConfig?.providers ?? [];
  const configuredProviderIDs = new Set(configuredProviders.map((provider) => provider.id));
  const knownStatuses = providers.filter((provider) => configuredProviderIDs.has(provider.name));
  const readyProviders = knownStatuses.filter(isProviderReady).length;
  const blockedProviders = knownStatuses.filter(isProviderBlocked).length;
  const modelCount =
    models.length ||
    knownStatuses.reduce(
      (sum, provider) => sum + (provider.model_count ?? provider.models?.length ?? 0),
      0,
    );
  const statusByName = new Map(providers.map((provider) => [provider.name, provider]));
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
    <div
      className="card"
      style={{ padding: "14px 16px", marginBottom: 24 }}
      data-testid="connections-model-providers"
    >
      <SectionHeader
        title="Model providers"
        description="Cloud and local model endpoints used by Hecate Chat, direct model chat, routing, and usage reporting."
        meta={`${configuredProviders.length} configured`}
        actions={
          onNavigate ? (
            <button
              type="button"
              className="btn btn-primary btn-sm"
              onClick={() => onNavigate("connections")}
            >
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
            <span
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                letterSpacing: "0.04em",
                textTransform: "uppercase",
                color: repair.tone === "amber" ? "var(--amber)" : "var(--green)",
                whiteSpace: "nowrap",
              }}
            >
              {repairLabel}
            </span>
            <span
              style={{
                fontSize: 11,
                fontWeight: 600,
                color: repair.tone === "amber" ? "var(--amber)" : "var(--t1)",
              }}
            >
              {repair.title}
            </span>
            {repairButton && onNavigate && (
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => onNavigate("connections")}
                style={{ marginLeft: "auto", padding: "2px 7px" }}
              >
                {repairButton}
              </button>
            )}
          </div>
          <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>{repair.message}</div>
          <div
            style={{
              marginTop: 5,
              fontSize: 10,
              color: repair.tone === "amber" ? "var(--amber)" : "var(--t3)",
              fontFamily: "var(--font-mono)",
            }}
          >
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
        <ConnectionStat
          label="Configured"
          value={String(configuredProviders.length)}
          hint="provider records"
        />
        <ConnectionStat
          label="Ready"
          value={String(readyProviders)}
          hint="routing-ready"
          tone={readyProviders > 0 ? "green" : "muted"}
        />
        <ConnectionStat
          label="Needs attention"
          value={String(blockedProviders)}
          hint="blocked providers"
          tone={blockedProviders > 0 ? "amber" : "muted"}
        />
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

function providerRepairButtonLabel(
  hint: ReturnType<typeof providerFleetRepairHint>,
): string | null {
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
      <div
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          color: "var(--t3)",
          textTransform: "uppercase",
          letterSpacing: "0.06em",
          marginBottom: 5,
        }}
      >
        {label}
      </div>
      <div
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 18,
          fontWeight: 700,
          color: chipColor(tone),
          lineHeight: 1,
        }}
      >
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
  return Boolean(
    provider.routing_blocked_reason || (!provider.healthy && provider.status !== "pending"),
  );
}

function findAnthropicProvider(
  providers: ConfiguredProviderRecord[],
): ConfiguredProviderRecord | null {
  return (
    providers.find(
      (provider) =>
        provider.id === "anthropic" ||
        provider.preset_id === "anthropic" ||
        provider.protocol === "anthropic",
    ) ?? null
  );
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
        <span style={{ fontSize: 12, fontWeight: 600, color: "var(--t0)" }}>
          Anthropic provider key
        </span>
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
        Used by Hecate Chat and direct Anthropic provider calls through{" "}
        {provider.name || "Anthropic"}. This is separate from Claude Code's local CLI sign-in.
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
        <button
          type="button"
          className="btn btn-primary btn-sm"
          onClick={() => void save()}
          disabled={saving || key.trim() === ""}
        >
          {saving ? "Saving..." : configured ? "Update key" : "Save key"}
        </button>
        {configured && (
          <button
            type="button"
            className="btn btn-danger btn-sm"
            onClick={() => void clear()}
            disabled={saving}
          >
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
// typed health classification. That handshake is also the auth
// check: auth failures surface as `auth_required`.
// Probe state is cached on the hook so leaving and returning to the
// tab doesn't lose the last-known status.
//
// The section is read-only otherwise: adapter discovery and
// availability are still owned by the dashboard fan-out's
// /hecate/v1/agent-adapters response. We just surface the additional
// per-adapter "can I actually use this?" check here.
function AdapterStatusSection({
  agentAdapters,
  agentAdapterHealthByID,
  agentAdapterHealthLoadingByID,
  copyCommand,
  onProbeAdapter,
}: {
  agentAdapters: ProvidersAndModelsState["agentAdapters"];
  agentAdapterHealthByID: ProvidersAndModelsState["agentAdapterHealthByID"];
  agentAdapterHealthLoadingByID: ProvidersAndModelsState["agentAdapterHealthLoadingByID"];
  copyCommand: (command: string) => Promise<void>;
  onProbeAdapter: (adapterID: string) => void;
}) {
  if (!agentAdapters || agentAdapters.length === 0) {
    return null;
  }
  return (
    <div style={{ marginBottom: 24 }} data-testid="external-agents-adapters">
      <SectionHeader
        title="Adapters"
        description="Checks adapter readiness and auth by starting the adapter, completing the ACP handshake, and creating a session. Auth-required failures show here before a chat fails."
        meta={`${agentAdapters.length} adapter${agentAdapters.length === 1 ? "" : "s"}`}
      />
      <div className="card" style={{ overflow: "hidden" }}>
        {agentAdapters.map((adapter, i) => (
          <AdapterStatusRow
            key={adapter.id}
            adapter={adapter}
            divider={i < agentAdapters.length - 1}
            health={agentAdapterHealthByID.get(adapter.id) ?? null}
            loading={Boolean(agentAdapterHealthLoadingByID.get(adapter.id))}
            onCopyCommand={(command) => void copyCommand(command)}
            onProbeAdapter={onProbeAdapter}
          />
        ))}
      </div>
    </div>
  );
}

function AdapterStatusRow({
  adapter,
  divider,
  health,
  loading,
  onCopyCommand,
  onProbeAdapter,
}: {
  adapter: AgentAdapterRecord;
  divider: boolean;
  health: AgentAdapterHealthRecord | null;
  loading: boolean;
  onCopyCommand: (command: string) => void;
  onProbeAdapter: (adapterID: string) => void;
}) {
  // Collapse discovery, probe, and auth into one operator-facing state
  // so adapters don't show as both "missing" and "auth unknown".
  const probeVerifiedAuth = health?.status === "ready";
  const displayAuthStatus = probeVerifiedAuth ? "ok" : adapter.auth_status;
  const displayAuthError = probeVerifiedAuth ? "" : adapter.auth_error;
  const loginCommand = adapterLoginCommand(adapter);
  const localAuthNeedsRepair =
    displayAuthStatus === "unauthenticated" || health?.status === "auth_required";
  const showLocalAuthSetup = Boolean(loginCommand) && !probeVerifiedAuth && localAuthNeedsRepair;
  const visibleHealthError = health && shouldShowProbeError(health) ? (health.error ?? "") : "";
  const healthPath = health?.path ?? "";
  const state = adapterStatusState(adapter, health, displayAuthStatus, showLocalAuthSetup);
  const detail = adapterStatusDetail(adapter, health, state, visibleHealthError, displayAuthError);
  const showHealthDetail = Boolean(
    detail && health && !showLocalAuthSetup && (health.hint || visibleHealthError),
  );
  const showAuthDetail = Boolean(detail && !health && !showLocalAuthSetup);
  const showAuthMetadata = Boolean(
    displayAuthStatus && displayAuthStatus === "ok" && !showLocalAuthSetup && !health,
  );
  const showHealthDebugMetadata = state?.kind === "ready" || state?.kind === "issue";
  const showHealthPath = Boolean(
    healthPath && showHealthDebugMetadata && !showLocalAuthSetup && !isDevOverridePath(healthPath),
  );
  const showHealthDuration = Boolean(
    health?.duration_ms !== undefined && showHealthDebugMetadata && !showLocalAuthSetup,
  );

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
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
            ·
          </span>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>
            {adapter.id}
          </span>
          {state && (
            <span
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: chipColor(state.tone),
                textTransform: "uppercase",
                letterSpacing: "0.04em",
              }}
            >
              {state.label}
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
        </div>
        <div
          style={{
            display: "flex",
            flexWrap: "wrap",
            gap: 8,
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--t3)",
          }}
        >
          {adapter.command && (
            <span>
              command <span style={{ color: "var(--t1)" }}>{adapter.command}</span>
            </span>
          )}
          {adapter.version && (
            <span>
              version <span style={{ color: "var(--t1)" }}>{adapter.version}</span>
            </span>
          )}
          {showAuthMetadata && (
            <span>
              auth <span style={{ color: "var(--t1)" }}>{displayAuthStatus}</span>
            </span>
          )}
          {showHealthPath && (
            <span>
              path <span style={{ color: "var(--t1)" }}>{healthPath}</span>
            </span>
          )}
          {showHealthDuration && health?.duration_ms !== undefined && (
            <span>{health.duration_ms} ms</span>
          )}
        </div>
        {showHealthDetail && health && detail && (
          <div
            data-testid={`external-agents-adapter-${adapter.id}-detail`}
            style={{
              marginTop: 6,
              fontSize: 11,
              color: chipColor(detail.tone),
              lineHeight: 1.4,
              wordBreak: "break-word",
            }}
          >
            {detail.message && <div>{detail.message}</div>}
            {visibleHealthError && visibleHealthError !== detail.message && (
              <div
                style={{
                  fontFamily: "var(--font-mono)",
                  fontSize: 10,
                  color: "var(--t3)",
                  marginTop: 2,
                }}
              >
                {visibleHealthError}
              </div>
            )}
          </div>
        )}
        {showAuthDetail && detail && (
          <div
            data-testid={`external-agents-adapter-${adapter.id}-auth-detail`}
            style={{
              marginTop: 6,
              fontSize: 11,
              color: chipColor(detail.tone),
              lineHeight: 1.4,
            }}
          >
            {detail.message}
          </div>
        )}
        {showLocalAuthSetup && loginCommand && (
          <AdapterLocalAuthSetup
            loginCommand={loginCommand}
            onCopyCommand={onCopyCommand}
            onTestAgain={() => onProbeAdapter(adapter.id)}
            testing={loading}
          />
        )}
      </div>
      {loading && (
        <span
          data-testid={`external-agents-checking-${adapter.id}`}
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            color: "var(--t3)",
            whiteSpace: "nowrap",
          }}
        >
          checking…
        </span>
      )}
    </div>
  );
}

function AdapterLocalAuthSetup({
  loginCommand,
  onCopyCommand,
  onTestAgain,
  testing,
}: {
  loginCommand: string;
  onCopyCommand: (command: string) => void;
  onTestAgain: () => void;
  testing: boolean;
}) {
  const accent = chipColor("amber");

  return (
    <div
      data-testid="external-agent-auth-setup"
      style={{
        marginTop: 10,
        padding: 10,
        border: "1px solid rgba(245, 158, 11, 0.28)",
        borderRadius: 10,
        background: "rgba(245, 158, 11, 0.06)",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 10,
        }}
      >
        <div style={{ minWidth: 0 }}>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 7,
              fontSize: 11,
              fontWeight: 600,
              color: accent,
            }}
          >
            <Icon d={Icons.terminal} size={12} />
            Local sign-in
          </div>
          <div style={{ fontSize: 11, color: "var(--t2)", lineHeight: 1.4 }}>
            Run in Terminal, then test again. Hecate uses local CLI auth as your OS user and does
            not store credentials.
          </div>
          <div
            style={{
              marginTop: 8,
              display: "flex",
              flexWrap: "wrap",
              gap: 8,
              alignItems: "center",
            }}
          >
            <code
              style={{
                border: "1px solid var(--border)",
                borderRadius: 6,
                background: "rgba(0, 0, 0, 0.22)",
                color: "var(--t0)",
                fontFamily: "var(--font-mono)",
                fontSize: 11,
                padding: "4px 7px",
              }}
            >
              {loginCommand}
            </code>
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={() => onCopyCommand(loginCommand)}
            >
              Copy command
            </button>
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={onTestAgain}
              disabled={testing}
            >
              {testing ? "Testing..." : "Test again"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function isDevOverridePath(path: string): boolean {
  return path.startsWith("dev-override://");
}

function shouldShowProbeError(health: AgentAdapterHealthRecord): boolean {
  const error = health.error?.trim() ?? "";
  if (!error) return false;
  if (error.includes("HECATE_AGENT_ADAPTER_DEV_OVERRIDES")) return false;
  if (error.startsWith("forced ")) return false;
  return true;
}

type ChipTone = "green" | "amber" | "red" | "muted";
type AdapterStatusKind = "ready" | "sign_in" | "setup" | "billing" | "issue";
type AdapterStatusState = {
  kind: AdapterStatusKind;
  tone: ChipTone;
  label: string;
};
type AdapterStatusDetail = {
  tone: ChipTone;
  message: string;
};

function adapterStatusState(
  adapter: AgentAdapterRecord,
  health: AgentAdapterHealthRecord | null,
  authStatus: string | undefined,
  showLocalAuthSetup: boolean,
): AdapterStatusState | null {
  if (health?.status === "ready") return { kind: "ready", tone: "green", label: "ready" };
  if (isBillingStatus(authStatus, health))
    return { kind: "billing", tone: "amber", label: "billing" };
  if (
    showLocalAuthSetup ||
    authStatus === "unauthenticated" ||
    health?.status === "auth_required"
  ) {
    return { kind: "sign_in", tone: "amber", label: "sign in" };
  }
  if (!adapter.available || health?.status === "not_installed" || isSetupProbe(health)) {
    return { kind: "setup", tone: "muted", label: "not configured" };
  }
  if (health?.status === "error") {
    return { kind: "issue", tone: "amber", label: "needs attention" };
  }
  if (authStatus && authStatus !== "ok" && authStatus !== "unknown") {
    return { kind: "issue", tone: "amber", label: "needs attention" };
  }
  return null;
}

function adapterStatusDetail(
  adapter: AgentAdapterRecord,
  health: AgentAdapterHealthRecord | null,
  state: AdapterStatusState | null,
  visibleHealthError: string,
  authError: string | undefined,
): AdapterStatusDetail | null {
  if (!state) return null;
  if (state.kind === "ready" || state.kind === "sign_in") return null;

  if (state.kind === "setup") {
    return {
      tone: "muted",
      message: `Set up to use: ${health?.hint || authError || adapterSetupHint(adapter)}`,
    };
  }

  const message = health?.hint || authError || visibleHealthError;
  if (!message) return null;
  return { tone: state.tone, message };
}

function isBillingStatus(
  authStatus: string | undefined,
  health: AgentAdapterHealthRecord | null,
): boolean {
  if (authStatus === "billing") return true;
  const text = `${health?.hint ?? ""} ${health?.error ?? ""}`.toLowerCase();
  return Boolean(health?.status === "error" && text.includes("billing"));
}

function isSetupProbe(health: AgentAdapterHealthRecord | null): boolean {
  if (!health || health.status !== "error") return false;
  const text = `${health.hint ?? ""} ${health.error ?? ""}`.toLowerCase();
  return (
    text.includes("app cli missing") ||
    text.includes("command was not found") ||
    text.includes("setup docs:") ||
    text.startsWith("install ")
  );
}

function adapterSetupHint(adapter: AgentAdapterRecord): string {
  if (adapter.managed_package) {
    return `Install Node/npm so Hecate can manage "${adapter.managed_package}", or install ${adapter.command} directly.`;
  }
  if (adapter.docs_url) {
    return `Install ${adapter.name} and ensure ${adapter.command} is on PATH. Setup docs: ${adapter.docs_url}`;
  }
  return `Install ${adapter.name} and ensure ${adapter.command} is on PATH.`;
}

function chipColor(tone: ChipTone): string {
  switch (tone) {
    case "green":
      return "var(--teal)";
    case "amber":
      return "var(--amber)";
    case "red":
      return "var(--red)";
    case "muted":
      return "var(--t3)";
  }
}

function adapterLoginCommand(adapter: AgentAdapterRecord): string {
  switch (adapter.id) {
    case "codex":
      return "codex login";
    case "claude_code":
      return "claude /login";
    case "cursor_agent":
      return "cursor-agent login";
    default:
      return "";
  }
}

function GrantRow({
  grant,
  divider,
  onRevoke,
}: {
  grant: ChatGrantRecord;
  divider: boolean;
  onRevoke: () => void;
}) {
  const [confirmingRevoke, setConfirmingRevoke] = useState(false);
  const decisionTone =
    grant.decision === "approve"
      ? { color: "var(--teal)", label: "always approve" }
      : grant.decision === "deny"
        ? { color: "var(--red)", label: "always deny" }
        : { color: "var(--t2)", label: grant.decision };
  const expiresLabel = grant.expires_at
    ? `expires ${formatLocaleDateTime(grant.expires_at)}`
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
          <span style={{ fontSize: 12, fontWeight: 500, color: "var(--t0)" }}>
            {grant.adapter_id}
          </span>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>
            ·
          </span>
          <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t1)" }}>
            {grant.tool_kind}
          </span>
          <span
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              color: decisionTone.color,
              textTransform: "uppercase",
              letterSpacing: "0.04em",
            }}
          >
            {decisionTone.label}
          </span>
        </div>
        <div
          style={{
            display: "flex",
            flexWrap: "wrap",
            gap: 8,
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--t3)",
          }}
        >
          <span>
            scope <span style={{ color: "var(--t1)" }}>{grant.scope}</span>
          </span>
          {grant.workspace && (
            <span>
              workspace <span style={{ color: "var(--t1)" }}>{grant.workspace}</span>
            </span>
          )}
          {grant.session_id && (
            <span>
              session <span style={{ color: "var(--t1)" }}>{grant.session_id}</span>
            </span>
          )}
          {grant.granted_by && (
            <span>
              by <span style={{ color: "var(--t1)" }}>{grant.granted_by}</span>
            </span>
          )}
          <span>{formatLocaleDateTime(grant.granted_at)}</span>
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

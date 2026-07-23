import { useEffect, useMemo, useState, type ReactNode } from "react";
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
import { agentApprovalToolKindLabel } from "../../lib/agent-approval-labels";
import {
  humanizeProbeError,
  resolveExternalAgentReadiness,
  shouldShowProbeError,
  type ExternalAgentReadinessTone,
} from "../../lib/external-agent-readiness";
import {
  providerFleetRepairHint,
  providerReadinessMeaning,
  providerRepairActionLabel,
} from "../../lib/provider-readiness";
import { isRemoteRuntimeSession } from "../../lib/runtime-utils";
import type {
  AgentAdapterCapability,
  AgentAdapterHealthRecord,
  AgentAdapterRecord,
} from "../../types/agent-adapter";
import type { ChatGrantRecord } from "../../types/chat";
import type { ModelRecord } from "../../types/model";
import type {
  ConfiguredProviderRecord,
  ConfiguredStateResponse,
  ProviderRecord,
} from "../../types/provider";
import { BrandAvatar, Icon, Icons, InlineError } from "../shared/ui";
import { DictationReadinessSection } from "./DictationReadinessSection";

type Props = {
  onNavigate?: (
    workspace: "connections" | "tasks" | "overview" | "settings" | "chats" | "usage",
  ) => void;
  onAddProvider?: () => void;
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
// Grants are lazy-loaded on panel mount — operators rarely visit this surface,
// so we don't fetch them on every dashboard load. Adapter discovery stays
// passive; optional diagnostics disclose that they execute the installed app.
export function ConnectionsPanel({
  onNavigate,
  onAddProvider,
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
  const [agentAdapterLogoutLoadingByID, setAgentAdapterLogoutLoadingByID] = useState<
    Map<string, true>
  >(() => new Map());
  const [agentAdapterAuthenticateLoadingByID, setAgentAdapterAuthenticateLoadingByID] = useState<
    Map<string, true>
  >(() => new Map());
  const [agentAdapterCatalogRefreshing, setAgentAdapterCatalogRefreshing] = useState(false);
  const chatGrants = approvals.state.grants;
  const chatGrantsLoading = approvals.state.grantsLoading;
  const chatGrantsError = approvals.state.grantsError;
  const liveAnthropicProvider = findAnthropicProvider(settingsConfig?.providers ?? []);
  const [rememberedAnthropicProvider, setRememberedAnthropicProvider] =
    useState<ConfiguredProviderRecord | null>(liveAnthropicProvider);
  const connectionsFocusTargets = useMemo(
    () =>
      new Set([
        "connections-dictation",
        ...agentAdapters.map((adapter) => externalAgentSetupFocusTarget(adapter.id)),
      ]),
    [agentAdapters],
  );
  const remoteRuntime = isRemoteRuntimeSession(runtime.state.sessionInfo);
  const listChatGrants = approvals.actions.loadGrants;
  const probeAgentAdapter = agentAdapterActions.probeAgentAdapter;
  const refreshAgentAdapters = agentAdapterActions.refreshAgentAdapters;
  const authenticateAgentAdapter = agentAdapterActions.authenticateAgentAdapter;
  const logoutAgentAdapter = agentAdapterActions.logoutAgentAdapter;

  useEffect(() => {
    if (liveAnthropicProvider) setRememberedAnthropicProvider(liveAnthropicProvider);
  }, [liveAnthropicProvider]);

  useEffect(() => {
    void listChatGrants();
    // Grants are lazy-loaded once when the tab mounts; Refresh handles
    // explicit re-fetches.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function handleRefreshAgentAdapters() {
    if (agentAdapterCatalogRefreshing) return;
    setAgentAdapterCatalogRefreshing(true);
    try {
      await refreshAgentAdapters();
    } finally {
      setAgentAdapterCatalogRefreshing(false);
    }
  }

  // One-shot scroll, focus, and highlight when the operator arrived here via
  // a setup action on a failed agent turn or unavailable dictation control.
  // Chat sets `hecate.connectionsFocus` in sessionStorage before
  // navigating; we read-and-clear it so subsequent visits don't
  // re-trigger the scroll.
  //
  // The focus target is validated against a small allowlist before
  // it's interpolated into a DOM lookup — that avoids any selector-
  // injection class from an unexpected sessionStorage value (which
  // could happen via a stale entry, a third-party extension writing
  // into the same key, or a forward-compat token a newer build set
  // that this build doesn't know about).
  useEffect(() => {
    let focusTarget: string | null = null;
    try {
      for (const key of ["hecate.connectionsFocus", "hecate.settingsFocus"]) {
        const raw = sessionStorage.getItem(key);
        if (raw && connectionsFocusTargets.has(raw)) {
          focusTarget = raw;
          break;
        }
      }
    } catch {
      // sessionStorage unavailable — nothing to focus.
    }
    if (!focusTarget) return;
    const target = focusTarget; // narrow for the inner closure
    // Defer one frame so the card has rendered before we measure and focus
    // it. Track both timers so an unmount mid-flash doesn't leak or run the
    // class-removal against a detached node.
    let removeHandle: number | null = null;
    const startHandle = window.setTimeout(() => {
      const card = document.querySelector(`[data-testid="${target}"]`);
      if (!card) return;
      try {
        sessionStorage.removeItem("hecate.connectionsFocus");
        sessionStorage.removeItem("hecate.settingsFocus");
      } catch {
        // sessionStorage unavailable — focus still works without clearing.
      }
      const reduceMotion =
        typeof window.matchMedia === "function" &&
        window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      card.scrollIntoView({ behavior: reduceMotion ? "auto" : "smooth", block: "center" });
      if (card instanceof HTMLElement) {
        // The dictation card declares tabIndex={-1}; adding the same
        // programmatic focus affordance here keeps existing External Agent
        // setup targets accessible too.
        card.tabIndex = -1;
        card.classList.add("cross-surface-focus-target");
        card.focus({ preventScroll: true });
      }
      if (reduceMotion) return;
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
  }, [connectionsFocusTargets]);

  // Adapter status uses the runtime slice for copyCommand because
  // clipboard writes are side-effects, not session mutations.
  const copyCommand = runtime.actions.copyCommand;

  async function handleLogoutAdapter(adapterID: string) {
    setAgentAdapterLogoutLoadingByID((current) => {
      const next = new Map(current);
      next.set(adapterID, true);
      return next;
    });
    try {
      await logoutAgentAdapter(adapterID);
    } finally {
      setAgentAdapterLogoutLoadingByID((current) => {
        if (!current.has(adapterID)) return current;
        const next = new Map(current);
        next.delete(adapterID);
        return next;
      });
    }
  }

  async function handleAuthenticateAdapter(adapterID: string) {
    setAgentAdapterAuthenticateLoadingByID((current) => {
      const next = new Map(current);
      next.set(adapterID, true);
      return next;
    });
    try {
      await authenticateAgentAdapter(adapterID);
    } finally {
      setAgentAdapterAuthenticateLoadingByID((current) => {
        if (!current.has(adapterID)) return current;
        const next = new Map(current);
        next.delete(adapterID);
        return next;
      });
    }
  }

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

      <DictationReadinessSection
        providerConfigSnapshot={settingsConfig}
        localProviderSetupAvailable={!remoteRuntime}
        onAddProvider={onAddProvider}
      />

      <AdapterStatusSection
        agentAdapters={agentAdapters}
        agentAdapterHealthByID={agentAdapterHealthByID}
        agentAdapterHealthLoadingByID={agentAdapterHealthLoadingByID}
        agentAdapterAuthenticateLoadingByID={agentAdapterAuthenticateLoadingByID}
        agentAdapterLogoutLoadingByID={agentAdapterLogoutLoadingByID}
        remoteRuntime={remoteRuntime}
        catalogRefreshing={agentAdapterCatalogRefreshing}
        copyCommand={copyCommand}
        onRefreshCatalog={() => void handleRefreshAgentAdapters()}
        onProbeAdapter={(adapterID) => void probeAgentAdapter(adapterID)}
        onAuthenticateAdapter={(adapterID) => void handleAuthenticateAdapter(adapterID)}
        onLogoutAdapter={(adapterID) => void handleLogoutAdapter(adapterID)}
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

// AdapterStatusSection lists the configured external agents. Optional
// diagnostics start the runtime, complete an ACP handshake, and classify that
// disposable session; they annotate this view but never gate a later chat.
// Passive discovery and launch availability remain owned by the
// /hecate/v1/agent-adapters catalog, which can be refreshed independently.
function AdapterStatusSection({
  agentAdapters,
  agentAdapterHealthByID,
  agentAdapterHealthLoadingByID,
  agentAdapterAuthenticateLoadingByID,
  agentAdapterLogoutLoadingByID,
  remoteRuntime,
  catalogRefreshing,
  copyCommand,
  onRefreshCatalog,
  onProbeAdapter,
  onAuthenticateAdapter,
  onLogoutAdapter,
}: {
  agentAdapters: ProvidersAndModelsState["agentAdapters"];
  agentAdapterHealthByID: ProvidersAndModelsState["agentAdapterHealthByID"];
  agentAdapterHealthLoadingByID: ProvidersAndModelsState["agentAdapterHealthLoadingByID"];
  agentAdapterAuthenticateLoadingByID: Map<string, true>;
  agentAdapterLogoutLoadingByID: Map<string, true>;
  remoteRuntime: boolean;
  catalogRefreshing: boolean;
  copyCommand: (command: string) => Promise<void>;
  onRefreshCatalog: () => void;
  onProbeAdapter: (adapterID: string) => void;
  onAuthenticateAdapter: (adapterID: string) => void;
  onLogoutAdapter: (adapterID: string) => void;
}) {
  if (!agentAdapters || agentAdapters.length === 0) {
    return null;
  }
  return (
    <div style={{ marginBottom: 24 }} data-testid="external-agents-adapters">
      <SectionHeader
        title="External agents"
        description="Hecate finds installed agents without launching them. Refresh only repeats that passive discovery. New chat re-resolves the app and prepares the real ACP session; the first message verifies any deferred vendor launch and authentication. Optional diagnostics below start a temporary session for troubleshooting."
        meta={`${agentAdapters.length} agent${agentAdapters.length === 1 ? "" : "s"}`}
        actions={
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={onRefreshCatalog}
            disabled={catalogRefreshing}
            aria-label={
              catalogRefreshing
                ? "Refreshing external-agent discovery"
                : "Refresh external-agent discovery without starting agents"
            }
            aria-live="polite"
            title="Refresh installed-agent paths without starting an agent"
          >
            <Icon d={Icons.refresh} size={13} />
            {catalogRefreshing ? "Refreshing…" : "Refresh"}
          </button>
        }
      />
      <div className="card" style={{ overflow: "hidden" }}>
        {agentAdapters.map((adapter, i) => (
          <AdapterStatusRow
            key={adapter.id}
            adapter={adapter}
            remoteRuntime={remoteRuntime}
            divider={i < agentAdapters.length - 1}
            health={agentAdapterHealthByID.get(adapter.id) ?? null}
            loading={Boolean(agentAdapterHealthLoadingByID.get(adapter.id))}
            authenticateLoading={Boolean(agentAdapterAuthenticateLoadingByID.get(adapter.id))}
            logoutLoading={Boolean(agentAdapterLogoutLoadingByID.get(adapter.id))}
            onCopyCommand={(command) => void copyCommand(command)}
            onProbeAdapter={(item) => onProbeAdapter(item.id)}
            onAuthenticateAdapter={(item) => onAuthenticateAdapter(item.id)}
            onLogoutAdapter={(item) => onLogoutAdapter(item.id)}
          />
        ))}
      </div>
    </div>
  );
}

function AdapterStatusRow({
  adapter,
  remoteRuntime,
  divider,
  health,
  loading,
  authenticateLoading,
  logoutLoading,
  onCopyCommand,
  onProbeAdapter,
  onAuthenticateAdapter,
  onLogoutAdapter,
}: {
  adapter: AgentAdapterRecord;
  remoteRuntime: boolean;
  divider: boolean;
  health: AgentAdapterHealthRecord | null;
  loading: boolean;
  authenticateLoading: boolean;
  logoutLoading: boolean;
  onCopyCommand: (command: string) => void;
  onProbeAdapter: (adapter: AgentAdapterRecord) => void;
  onAuthenticateAdapter: (adapter: AgentAdapterRecord) => void;
  onLogoutAdapter: (adapter: AgentAdapterRecord) => void;
}) {
  const readiness = resolveExternalAgentReadiness(adapter, health);
  const loginCommand = readiness.loginCommand;
  const showLocalAuthSetup =
    !remoteRuntime && Boolean(loginCommand) && readiness.kind === "sign_in";
  const showRemoteCredentialSetup =
    remoteRuntime &&
    adapter.remote_credential_ok !== true &&
    Boolean(
      adapter.remote_credential_hint ||
      adapter.remote_credential_mode ||
      adapter.credential_modes?.some((mode) => mode.remote_allowed),
    );
  const visibleHealthError =
    health && shouldShowProbeError(health) ? humanizeProbeError(health.error ?? "") : "";
  const selectedPath = adapter.path || "";
  const diagnosticPath =
    health?.path && health.path !== selectedPath && !isDevOverridePath(health.path)
      ? health.path
      : "";
  const detail = adapterStatusDetail(readiness, visibleHealthError);
  const showHealthDetail = Boolean(
    detail && health && !showLocalAuthSetup && (readiness.detail || visibleHealthError),
  );
  const showAuthDetail = Boolean(detail && !health && !showLocalAuthSetup);
  const showAuthMetadata = Boolean(
    readiness.authStatus && readiness.authStatus === "ok" && !showLocalAuthSetup && !health,
  );
  const showHealthDebugMetadata = readiness.kind === "ready" || readiness.kind === "issue";
  const showSelectedPath = Boolean(selectedPath && !isDevOverridePath(selectedPath));
  const showHealthDuration = Boolean(
    health?.duration_ms !== undefined && showHealthDebugMetadata && !showLocalAuthSetup,
  );
  const showHealthCapabilities = Boolean(
    health?.capabilities_known && showHealthDebugMetadata && !showLocalAuthSetup,
  );
  const agentInfoLabel = formatAgentInfoLabel(health?.agent_info);
  const showAgentInfo = Boolean(agentInfoLabel && showHealthDebugMetadata && !showLocalAuthSetup);
  const showAuthenticateAction =
    !remoteRuntime &&
    adapter.available &&
    adapterAuthenticateSupportedByHecate(adapter, health) &&
    readiness.kind === "sign_in";
  const showLogoutAction =
    !remoteRuntime &&
    adapter.available &&
    readiness.authStatus === "ok" &&
    adapterLogoutSupportedByHecate(adapter, health) &&
    !showAuthenticateAction;
  const showProbeAction =
    adapter.available &&
    !showLocalAuthSetup &&
    !showRemoteCredentialSetup &&
    !showAuthenticateAction;
  const showRemoteAuthPolicy =
    remoteRuntime &&
    adapter.available &&
    (adapterAuthenticateSupportedByHecate(adapter, health) ||
      adapterLogoutSupportedByHecate(adapter, health));

  return (
    <div
      data-testid={`external-agents-adapter-${adapter.id}`}
      style={{
        display: "flex",
        alignItems: "flex-start",
        flexWrap: "wrap",
        gap: 12,
        padding: "12px 14px",
        borderBottom: divider ? "1px solid var(--border)" : "none",
      }}
    >
      <BrandAvatar brand={adapter.id} fallback={adapter.name} size={30} />
      <div style={{ minWidth: 0, flex: "1 1 260px" }}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            flexWrap: "wrap",
            gap: 7,
            marginBottom: 4,
          }}
        >
          <span style={{ fontSize: 13, fontWeight: 600, color: "var(--t0)", lineHeight: 1.25 }}>
            {adapter.name}
          </span>
          <span className={`badge ${badgeClassForTone(readiness.tone)}`}>{readiness.label}</span>
          {adapter.embedded && (
            <span
              className="badge badge-neutral"
              data-testid={`external-agents-adapter-${adapter.id}-embedded`}
            >
              adapter built in
            </span>
          )}
          {adapter.version_outside_range && (
            <span
              className="badge badge-amber"
              data-testid={`external-agents-adapter-${adapter.id}-version-warning`}
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
          <span>
            id <span style={{ color: "var(--t1)" }}>{adapter.id}</span>
          </span>
          {adapter.adapter_version && (
            <span>
              adapter <span style={{ color: "var(--t1)" }}>{adapter.adapter_version}</span>
            </span>
          )}
          {adapter.agent_version && (
            <span>
              agent <span style={{ color: "var(--t1)" }}>{adapter.agent_version}</span>
            </span>
          )}
          {showAuthMetadata && (
            <span>
              auth <span style={{ color: "var(--t1)" }}>{readiness.authStatus}</span>
            </span>
          )}
          {showSelectedPath && (
            <span>
              last discovered path <span style={{ color: "var(--t1)" }}>{selectedPath}</span>
            </span>
          )}
          {diagnosticPath && (
            <span>
              diagnostic path <span style={{ color: "var(--t1)" }}>{diagnosticPath}</span>
            </span>
          )}
          {showHealthDuration && health?.duration_ms !== undefined && (
            <span>{health.duration_ms} ms</span>
          )}
          {showAgentInfo && (
            <span>
              reports <span style={{ color: "var(--t1)" }}>{agentInfoLabel}</span>
            </span>
          )}
          {showHealthCapabilities && health && (
            <span>
              load session{" "}
              <span style={{ color: "var(--t1)" }}>
                {health.supports_load_session === true ? "yes" : "no"}
              </span>
            </span>
          )}
        </div>
        <AdapterCapabilitiesSummary
          adapter={adapter}
          health={health}
          remoteRuntime={remoteRuntime}
        />
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
            adapterID={adapter.id}
            adapterName={adapter.name || adapter.id}
            loginCommand={loginCommand}
            onCopyCommand={onCopyCommand}
            onRunDiagnostics={() => onProbeAdapter(adapter)}
            testing={loading}
          />
        )}
        {showRemoteCredentialSetup && (
          <AdapterRemoteCredentialSetup
            adapter={adapter}
            onCopyCommand={onCopyCommand}
            onRunDiagnostics={() => onProbeAdapter(adapter)}
            testing={loading}
          />
        )}
        {showRemoteAuthPolicy && (
          <div
            data-testid={`external-agents-adapter-${adapter.id}-auth-policy`}
            style={{
              marginTop: 6,
              fontSize: 11,
              color: "var(--t3)",
              lineHeight: 1.4,
            }}
          >
            Local ACP login/logout actions are disabled in remote runtime. Configure remote
            credentials for hosted sessions instead.
          </div>
        )}
      </div>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          flexWrap: "wrap",
          justifyContent: "flex-end",
          gap: 8,
          marginLeft: "auto",
        }}
      >
        {loading && (
          <span
            data-testid={`external-agents-checking-${adapter.id}`}
            role="status"
            aria-live="polite"
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              color: "var(--t3)",
              whiteSpace: "nowrap",
            }}
          >
            diagnosing…
          </span>
        )}
        {showProbeAction && (
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={() => onProbeAdapter(adapter)}
            disabled={loading}
            aria-label={`Run diagnostics for ${adapter.name || adapter.id}; opens a temporary ACP session and may execute the agent app`}
            title={`Opens a temporary ${adapter.name || adapter.id} ACP session without sending a prompt and may execute the agent app`}
            data-testid={`external-agents-test-${adapter.id}`}
          >
            <Icon d={Icons.refresh} size={12} /> {loading ? "Running..." : "Run diagnostics"}
          </button>
        )}
        {showAuthenticateAction && (
          <button
            type="button"
            className="btn btn-primary btn-sm"
            onClick={() => onAuthenticateAdapter(adapter)}
            disabled={authenticateLoading}
            aria-label={`Sign in ${adapter.name || adapter.id}; opens a temporary ACP session`}
            title={`Opens a temporary ACP session and invokes ${adapter.name || adapter.id} sign-in`}
          >
            <Icon d={Icons.keys} size={12} /> {authenticateLoading ? "Signing in..." : "Sign in"}
          </button>
        )}
        {showLogoutAction && (
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={() => onLogoutAdapter(adapter)}
            disabled={logoutLoading}
            aria-label={`Sign out ${adapter.name || adapter.id}; opens a temporary ACP session`}
            title={`Opens a temporary ACP session and invokes ${adapter.name || adapter.id} sign-out`}
          >
            <Icon d={Icons.x} size={12} /> {logoutLoading ? "Signing out..." : "Sign out"}
          </button>
        )}
      </div>
    </div>
  );
}

function AdapterCapabilitiesSummary({
  adapter,
  health,
  remoteRuntime,
}: {
  adapter: AgentAdapterRecord;
  health: AgentAdapterHealthRecord | null;
  remoteRuntime: boolean;
}) {
  const capabilities = visibleAdapterCapabilities(adapter, health);
  const showTerminalPolicy = capabilities.some(
    (capability) => capability.id === "terminal_rpc" && capability.status === "operator_opt_in",
  );
  if (!capabilities.length) return null;
  return (
    <>
      <div
        data-testid={`external-agents-adapter-${adapter.id}-capabilities`}
        aria-label={`${adapter.name || adapter.id} ACP capabilities`}
        style={{ display: "flex", flexWrap: "wrap", gap: 5, marginTop: 7 }}
      >
        {capabilities.map((capability) => {
          const suffix = capabilityStatusSuffix(capability.status);
          return (
            <span
              key={capability.id}
              title={capability.description}
              style={{
                border: "1px solid var(--border)",
                borderRadius: 6,
                color: "var(--t2)",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                lineHeight: 1.25,
                padding: "3px 6px",
                whiteSpace: "nowrap",
              }}
            >
              {capability.name || capability.id}
              {suffix && <span style={{ color: "var(--t3)" }}> · {suffix}</span>}
            </span>
          );
        })}
      </div>
      {showTerminalPolicy && (
        <div
          data-testid={`external-agents-adapter-${adapter.id}-terminal-policy`}
          style={{ marginTop: 5, fontSize: 11, color: "var(--t3)", lineHeight: 1.4 }}
        >
          ACP terminals are disabled until policy flags are enabled:{" "}
          <code>HECATE_AGENT_ADAPTER_TERMINALS=1</code>
          {remoteRuntime && (
            <>
              {" "}
              and <code>HECATE_REMOTE_ALLOW_ACP_TERMINALS=1</code>
            </>
          )}
          <span>. Terminal creation still requires External Agent approval.</span>
        </div>
      )}
    </>
  );
}

function visibleAdapterCapabilities(
  adapter: AgentAdapterRecord,
  health: AgentAdapterHealthRecord | null,
): AgentAdapterCapability[] {
  return (adapter.capabilities ?? [])
    .map((capability) => resolveLiveCapability(capability, health))
    .filter((capability) => capability.status !== "not_supported");
}

function resolveLiveCapability(
  capability: AgentAdapterCapability,
  health: AgentAdapterHealthRecord | null,
): AgentAdapterCapability {
  if (!health?.capabilities_known) return capability;
  if (capability.id === "authenticate") {
    return {
      ...capability,
      status: health.supports_authenticate === true ? "supported" : "not_supported",
    };
  }
  if (capability.id === "logout") {
    return {
      ...capability,
      status: health.supports_logout === true ? "supported" : "not_supported",
    };
  }
  return capability;
}

function capabilityStatusSuffix(status: string): string {
  switch (status) {
    case "adapter_dependent":
      return "if advertised";
    case "operator_opt_in":
      return "opt-in";
    default:
      return "";
  }
}

function adapterLogoutSupportedByHecate(
  adapter: AgentAdapterRecord,
  health: AgentAdapterHealthRecord | null,
): boolean {
  if (health?.capabilities_known) {
    return health.supports_logout === true;
  }
  return adapter.supports_logout === true;
}

function adapterAuthenticateSupportedByHecate(
  adapter: AgentAdapterRecord,
  health: AgentAdapterHealthRecord | null,
): boolean {
  if (health?.capabilities_known) {
    return health.supports_authenticate === true;
  }
  return adapter.supports_authenticate === true;
}

function AdapterRemoteCredentialSetup({
  adapter,
  onCopyCommand,
  onRunDiagnostics,
  testing,
}: {
  adapter: AgentAdapterRecord;
  onCopyCommand: (command: string) => void;
  onRunDiagnostics: () => void;
  testing: boolean;
}) {
  const keys = remoteCredentialKeys(adapter);
  const detail =
    adapter.remote_credential_hint ||
    (keys.length > 0
      ? `Configure ${keys.join(" or ")} for this hosted runtime.`
      : "Configure a remote-safe credential for this hosted runtime.");
  return (
    <div
      data-testid={externalAgentSetupFocusTarget(adapter.id)}
      style={{
        marginTop: 10,
        padding: 10,
        border: "1px solid var(--teal-border)",
        borderRadius: 8,
        background: "var(--teal-bg)",
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
              color: "var(--teal)",
            }}
          >
            <Icon d={Icons.keys} size={12} />
            Hosted credential
          </div>
          <div style={{ fontSize: 11, color: "var(--t2)", lineHeight: 1.4 }}>{detail}</div>
          {keys.length > 0 && (
            <div
              style={{
                marginTop: 8,
                display: "flex",
                flexWrap: "wrap",
                gap: 8,
                alignItems: "center",
              }}
            >
              {keys.map((key) => (
                <button
                  key={key}
                  type="button"
                  className="btn btn-ghost btn-sm"
                  onClick={() => onCopyCommand(key)}
                  title={`Copy ${key}`}
                  style={{ fontFamily: "var(--font-mono)", fontSize: 11 }}
                >
                  {key}
                </button>
              ))}
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={onRunDiagnostics}
                disabled={testing}
                aria-label={`Run diagnostics for ${adapter.name || adapter.id}; opens a temporary ACP session and may execute the agent app`}
                title={`Opens a temporary ${adapter.name || adapter.id} ACP session without sending a prompt and may execute the agent app`}
              >
                {testing ? "Running..." : "Run diagnostics"}
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function AdapterLocalAuthSetup({
  adapterID,
  adapterName,
  loginCommand,
  onCopyCommand,
  onRunDiagnostics,
  testing,
}: {
  adapterID: string;
  adapterName: string;
  loginCommand: string;
  onCopyCommand: (command: string) => void;
  onRunDiagnostics: () => void;
  testing: boolean;
}) {
  const accent = chipColor("amber");

  return (
    <div
      data-testid={externalAgentSetupFocusTarget(adapterID)}
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
            Run in Terminal, then retry the chat. Hecate uses local CLI auth as your OS user and
            does not store credentials. Diagnostics are optional.
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
              onClick={onRunDiagnostics}
              disabled={testing}
              aria-label={`Run diagnostics for ${adapterName}; opens a temporary ACP session and may execute the agent app`}
              title={`Opens a temporary ${adapterName} ACP session without sending a prompt and may execute the agent app`}
            >
              {testing ? "Running..." : "Run diagnostics"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function remoteCredentialKeys(adapter: AgentAdapterRecord): string[] {
  const keys = new Set<string>();
  for (const mode of adapter.credential_modes ?? []) {
    if (!mode.remote_allowed) continue;
    for (const key of mode.env_keys ?? []) {
      if (key.trim()) keys.add(key.trim());
    }
  }
  return [...keys];
}

function isDevOverridePath(path: string): boolean {
  return path.startsWith("dev-override://");
}

function formatAgentInfoLabel(info: AgentAdapterHealthRecord["agent_info"]): string {
  const name = info?.title?.trim() || info?.name?.trim() || "";
  const version = info?.version?.trim() || "";
  if (!name) return version;
  return version ? `${name} ${version}` : name;
}

type ChipTone = ExternalAgentReadinessTone;
type AdapterStatusDetail = {
  tone: ChipTone;
  message: string;
};

function adapterStatusDetail(
  readiness: ReturnType<typeof resolveExternalAgentReadiness>,
  visibleHealthError: string,
): AdapterStatusDetail | null {
  if (readiness.kind === "ready" || readiness.kind === "sign_in") return null;

  if (readiness.kind === "setup") {
    const detail = readiness.detail || readiness.setupHint;
    return {
      tone: readiness.launchBlocked ? "muted" : "amber",
      message: readiness.launchBlocked
        ? `Set up to use: ${detail}`
        : `Last diagnostic: ${detail} This result is advisory; New chat prepares a fresh ACP session and the first message retries any deferred vendor process.`,
    };
  }

  const message = readiness.detail || visibleHealthError;
  if (!message) return null;
  return { tone: readiness.tone, message };
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

function badgeClassForTone(tone: ChipTone): string {
  switch (tone) {
    case "green":
      return "badge-green";
    case "amber":
      return "badge-amber";
    case "red":
      return "badge-red";
    case "muted":
      return "badge-muted";
  }
}

function externalAgentSetupFocusTarget(adapterID: string): string {
  return `external-agent-auth-setup-${adapterID}`;
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
            {agentApprovalToolKindLabel(grant.tool_kind)}
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

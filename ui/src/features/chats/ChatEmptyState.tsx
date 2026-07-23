import { type CSSProperties, useEffect, useState } from "react";

import { type ChatSetupRepairState } from "../../lib/chat-setup-readiness";
import type { SelectedModelIssue } from "../../lib/provider-issues";
import type { AgentAdapterRecord } from "../../types/agent-adapter";
import type { LocalProviderDiscoveryRecord, ProviderPresetRecord } from "../../types/provider";
import { BrandAvatar, Icon, Icons, InlineError } from "../shared/ui";

import { SelectedModelReadinessNotice, repairActionIcon } from "./ChatComposer";

type Props = {
  isAgentChat: boolean;
  isHecateChat: boolean;
  isExternalAgentChat: boolean;
  externalAgentSessionPrepared: boolean;
  isRemoteRuntime: boolean;
  setupRepair: ChatSetupRepairState | null;
  modelRouteUnavailable: boolean;
  selectedModelIssue: SelectedModelIssue | null;
  modelMutationDisabled?: boolean;
  agentRouteUnavailable: boolean;
  nothingRunnable: boolean;
  agentAdapters: AgentAdapterRecord[];
  selectedAgent?: AgentAdapterRecord;
  selectedAgentUnavailable: boolean;
  hasConfiguredProviders: boolean;
  providerPresets: ProviderPresetRecord[];
  quickLocalProviders: LocalProviderDiscoveryRecord[];
  quickLocalLoading: boolean;
  quickLocalError: string;
  quickAddingProviders: boolean;
  onOpenProviders: () => void;
  onUseSuggestedModel: (model: string) => void;
  onChooseWorkspace: () => void;
  onOpenAgentSetup: () => void;
  onQuickAddLocalProviders: (providers: LocalProviderDiscoveryRecord[]) => void;
  onRefreshQuickLocalProviders: () => void;
  onSwitchTarget: (target: "agent" | "external_agent") => void;
  rtkAvailable: boolean;
  rtkPath: string;
  rtkEnabled: boolean;
  showRTKOnboardingHint: boolean;
  onEnableRTK: () => void;
};

export function ChatEmptyState({
  isAgentChat,
  isHecateChat,
  isExternalAgentChat,
  externalAgentSessionPrepared,
  isRemoteRuntime,
  setupRepair,
  modelRouteUnavailable,
  selectedModelIssue,
  modelMutationDisabled = false,
  agentRouteUnavailable,
  nothingRunnable,
  agentAdapters,
  selectedAgent,
  selectedAgentUnavailable,
  hasConfiguredProviders,
  providerPresets,
  quickLocalProviders,
  quickLocalLoading,
  quickLocalError,
  quickAddingProviders,
  onOpenProviders,
  onUseSuggestedModel,
  onChooseWorkspace,
  onOpenAgentSetup,
  onQuickAddLocalProviders,
  onRefreshQuickLocalProviders,
  onSwitchTarget,
  rtkAvailable,
  rtkPath,
  rtkEnabled,
  showRTKOnboardingHint,
  onEnableRTK,
}: Props) {
  const hecateModelUnavailable =
    isHecateChat && (modelRouteUnavailable || Boolean(selectedModelIssue));
  const hasQuickLocalProviderCandidates =
    isHecateChat &&
    !isRemoteRuntime &&
    modelRouteUnavailable &&
    !hasConfiguredProviders &&
    quickLocalProviders.some((discovery) => discovery.preset_id != null);
  const showPrimaryRepairAction =
    Boolean(setupRepair) &&
    !(setupRepair?.kind === "no_provider" && hasQuickLocalProviderCandidates);
  const readyTitle = isExternalAgentChat
    ? externalAgentSessionPrepared
      ? `${selectedAgent?.name || "External agent"} session ready`
      : `${selectedAgent?.name || "External agent"} found`
    : "Ready when you are";
  const readyDetail = isExternalAgentChat
    ? externalAgentSessionPrepared
      ? "Hecate started the installed app and opened its ACP session in this workspace. Send a message when you're ready."
      : "Hecate found the installed app. Creating this chat starts it and opens an ACP session in the selected workspace."
    : "Ask a question, inspect the workspace, or describe the change you want to make.";
  const title =
    isAgentChat && selectedAgentUnavailable
      ? `${selectedAgent?.name || "Selected agent"} is unavailable`
      : isExternalAgentChat && agentRouteUnavailable
        ? "No available coding agent"
        : nothingRunnable
          ? "Connect a model or agent"
          : selectedModelIssue
            ? selectedModelIssue.title
            : setupRepair
              ? setupRepair.title
              : hecateModelUnavailable
                ? "No routable model"
                : readyTitle;
  const detail =
    isAgentChat && selectedAgentUnavailable
      ? `Hecate could not start ${selectedAgent?.name || "the selected agent"} because its CLI is not ready in this environment.`
      : isExternalAgentChat && agentRouteUnavailable
        ? "Hecate did not find any supported coding-agent CLI in the known operator locations."
        : nothingRunnable
          ? "Add a model provider in Connections or set up a supported external agent. Hecate will keep chats, approvals, files, and traces together once something can run."
          : selectedModelIssue
            ? selectedModelIssue.message
            : setupRepair
              ? setupRepair.message
              : hecateModelUnavailable
                ? "Add a provider with discovered models before sending through Hecate."
                : readyDetail;
  const emptyRepairAction = setupRepair;
  const startGuideItems = buildStartGuideItems({
    agentAdapters,
    hasConfiguredProviders,
    isRemoteRuntime,
    modelRouteUnavailable,
    selectedModelIssue,
    setupRepair: emptyRepairAction,
  });

  function runEmptyRepairAction() {
    if (!emptyRepairAction) return;
    switch (emptyRepairAction.action) {
      case "open_connections":
        onOpenProviders();
        return;
      case "use_suggested_model":
        if (emptyRepairAction.suggestedModel) onUseSuggestedModel(emptyRepairAction.suggestedModel);
        return;
      case "choose_workspace":
        onChooseWorkspace();
        return;
      case "open_agent_setup":
        onOpenAgentSetup();
        return;
    }
  }

  return (
    <div style={emptyStateShellStyle}>
      <div style={emptyStateHeaderStyle}>
        <div style={emptyStateKickerStyle}>
          {isExternalAgentChat ? "External agent" : "Hecate chat"}
        </div>
        <div style={emptyStateTitleStyle}>{title}</div>
        <div style={emptyStateDetailStyle}>{detail}</div>
      </div>
      <StartGuide items={startGuideItems} />
      {isAgentChat && (agentRouteUnavailable || selectedAgentUnavailable) && (
        <AgentSetupHints adapters={agentAdapters} selectedID={selectedAgent?.id} />
      )}
      {isHecateChat && selectedModelIssue && (
        <SelectedModelReadinessNotice
          issue={selectedModelIssue}
          compact
          disabled={modelMutationDisabled}
          onUseSuggestedModel={onUseSuggestedModel}
        />
      )}
      {showRTKOnboardingHint &&
        isHecateChat &&
        rtkAvailable &&
        !rtkEnabled &&
        !hecateModelUnavailable &&
        !emptyRepairAction && <RTKOnboardingHint path={rtkPath} onEnable={onEnableRTK} />}
      {(showPrimaryRepairAction ||
        modelRouteUnavailable ||
        selectedModelIssue ||
        agentRouteUnavailable) && (
        <div
          style={{
            display: "flex",
            justifyContent: "center",
            gap: 8,
            marginTop: 14,
            flexWrap: "wrap",
          }}
        >
          {showPrimaryRepairAction && emptyRepairAction && (
            <button
              className="btn btn-primary btn-sm"
              disabled={modelMutationDisabled && emptyRepairAction.action === "use_suggested_model"}
              onClick={runEmptyRepairAction}
              type="button"
              style={{ display: "flex", alignItems: "center", gap: 4 }}
            >
              <Icon d={repairActionIcon(emptyRepairAction)} size={13} />{" "}
              {emptyRepairAction.actionLabel}
            </button>
          )}
          {!emptyRepairAction &&
            (modelRouteUnavailable || selectedModelIssue) &&
            isHecateChat &&
            !hasQuickLocalProviderCandidates && (
              <button
                className="btn btn-primary btn-sm"
                onClick={onOpenProviders}
                type="button"
                style={{ display: "flex", alignItems: "center", gap: 4 }}
              >
                <Icon d={Icons.connections} size={13} /> Open Connections
              </button>
            )}
          {agentRouteUnavailable && !isAgentChat && (
            <button
              className="btn btn-ghost btn-sm"
              onClick={() => onSwitchTarget("external_agent")}
              type="button"
            >
              <Icon d={Icons.terminal} size={13} /> Check agents
            </button>
          )}
          {!agentRouteUnavailable && !isAgentChat && (
            <button
              className="btn btn-ghost btn-sm"
              onClick={() => onSwitchTarget("agent")}
              type="button"
            >
              <Icon d={Icons.terminal} size={13} /> Use agent
            </button>
          )}
        </div>
      )}
      {isHecateChat &&
        modelRouteUnavailable &&
        !hasConfiguredProviders &&
        (isRemoteRuntime ? (
          <HostedRuntimeProviderSetup />
        ) : (
          <QuickLocalProviderAdd
            discoveries={quickLocalProviders}
            error={quickLocalError}
            loading={quickLocalLoading}
            presets={providerPresets}
            adding={quickAddingProviders}
            onOpenProviders={onOpenProviders}
            onAdd={onQuickAddLocalProviders}
            onRefresh={onRefreshQuickLocalProviders}
          />
        ))}
    </div>
  );
}

type StartGuideTone = "ready" | "needed" | "setup" | "optional";

type StartGuideItem = {
  detail: string;
  icon: string | string[];
  label: string;
  status: string;
  tone: StartGuideTone;
};

function StartGuide({ items }: { items: StartGuideItem[] }) {
  return (
    <div aria-label="Start checklist" style={startGuideStyle}>
      {items.map((item, index) => (
        <div
          key={item.label}
          style={{
            ...startGuideRowStyle,
            borderTop: index === 0 ? undefined : "1px solid var(--border)",
          }}
        >
          <span style={startGuideIconStyle}>
            <Icon d={item.icon} size={15} />
          </span>
          <span style={{ minWidth: 0 }}>
            <span style={startGuideLabelStyle}>{item.label}</span>
            <span style={startGuideDetailStyle}>{item.detail}</span>
          </span>
          <span style={{ ...startGuideStatusStyle, ...startGuideToneStyle(item.tone) }}>
            {item.status}
          </span>
        </div>
      ))}
    </div>
  );
}

function buildStartGuideItems({
  agentAdapters,
  hasConfiguredProviders,
  isRemoteRuntime,
  modelRouteUnavailable,
  selectedModelIssue,
  setupRepair,
}: {
  agentAdapters: AgentAdapterRecord[];
  hasConfiguredProviders: boolean;
  isRemoteRuntime: boolean;
  modelRouteUnavailable: boolean;
  selectedModelIssue: SelectedModelIssue | null;
  setupRepair: ChatSetupRepairState | null;
}): StartGuideItem[] {
  const modelNeedsAttention =
    !hasConfiguredProviders || modelRouteUnavailable || Boolean(selectedModelIssue);
  const agentFound = agentAdapters.some((adapter) => adapter.available);
  const personalRemoteAgentLogin = agentAdapters.some(
    (adapter) => adapter.remote_credential_ok && adapter.remote_credential_mode === "local_login",
  );
  const workspaceNeedsAttention = setupRepair?.action === "choose_workspace";
  return [
    {
      detail: isRemoteRuntime
        ? "Add an API-key provider in Connections."
        : "Add a provider, or use detected local model apps.",
      icon: Icons.model,
      label: "Models",
      status: modelNeedsAttention ? "Needed" : "Ready",
      tone: modelNeedsAttention ? "needed" : "ready",
    },
    {
      detail: "Choose a folder when chats need files, diffs, tasks, or project context.",
      icon: Icons.folder,
      label: "Workspace",
      status: workspaceNeedsAttention ? "Needed" : "Optional",
      tone: workspaceNeedsAttention ? "needed" : "optional",
    },
    {
      detail: isRemoteRuntime
        ? personalRemoteAgentLogin
          ? "Use this Mac's configured CLI sign-ins; credentials stay on the Mac."
          : "Configure a remote-safe agent credential in Connections."
        : "Use Codex, Claude Code, Cursor, or Grok Build once ready.",
      icon: Icons.terminal,
      label: "Agents",
      status: agentFound ? "Found" : agentAdapters.length > 0 ? "Setup" : "Unavailable",
      tone: agentFound ? "optional" : agentAdapters.length > 0 ? "setup" : "optional",
    },
  ];
}

function startGuideToneStyle(tone: StartGuideTone): CSSProperties {
  switch (tone) {
    case "ready":
      return {
        background: "var(--green-bg)",
        borderColor: "var(--green-border)",
        color: "var(--green)",
      };
    case "needed":
      return {
        background: "var(--amber-bg)",
        borderColor: "var(--amber-border)",
        color: "var(--amber)",
      };
    case "setup":
      return {
        background: "var(--teal-bg)",
        borderColor: "var(--teal-border)",
        color: "var(--teal)",
      };
    case "optional":
      return {
        background: "var(--bg2)",
        borderColor: "var(--border)",
        color: "var(--t3)",
      };
  }
}

function AgentSetupHints({
  adapters,
  selectedID,
}: {
  adapters: AgentAdapterRecord[];
  selectedID?: string;
}) {
  const ordered = adapters.slice().sort((a, b) => {
    if (a.id === selectedID) return -1;
    if (b.id === selectedID) return 1;
    if (a.available !== b.available) return a.available ? 1 : -1;
    return a.name.localeCompare(b.name);
  });

  if (ordered.length === 0) {
    return (
      <div
        style={{
          margin: "14px auto 0",
          maxWidth: 520,
          borderTop: "1px solid var(--border)",
          paddingTop: 12,
          fontSize: 12,
          color: "var(--t2)",
          lineHeight: 1.5,
        }}
      >
        No external agents are registered by this Hecate build.
      </div>
    );
  }

  return (
    <div
      style={{
        margin: "16px auto 0",
        maxWidth: 620,
        textAlign: "left",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius)",
        background: "var(--bg2)",
        overflow: "hidden",
      }}
    >
      {ordered.map((adapter, index) => {
        const hint = agentSetupHint(adapter);
        return (
          <div
            key={adapter.id}
            style={{
              padding: "10px 12px",
              borderTop: index === 0 ? 0 : "1px solid var(--border)",
              display: "grid",
              gridTemplateColumns: "minmax(120px, 0.7fr) minmax(0, 1.3fr)",
              gap: 10,
              alignItems: "start",
            }}
          >
            <ExternalAgentSetupSummary adapter={adapter} hint={hint} />
          </div>
        );
      })}
    </div>
  );
}

function ExternalAgentSetupSummary({
  adapter,
  hint,
}: {
  adapter: AgentAdapterRecord;
  hint: ReturnType<typeof agentSetupHint>;
}) {
  return (
    <>
      <div style={{ minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <span
            style={{
              color: adapter.available ? "var(--teal)" : "var(--red)",
              display: "inline-flex",
              flexShrink: 0,
            }}
          >
            <Icon d={adapter.available ? Icons.check : Icons.x} size={11} />
          </span>
          <span
            style={{
              fontSize: 12,
              fontWeight: 600,
              color: "var(--t1)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {adapter.name}
          </span>
        </div>
        <div
          style={{
            marginTop: 3,
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--t3)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {hint.label}
        </div>
      </div>
      <div style={{ minWidth: 0, fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
        <div style={{ color: adapter.available ? "var(--teal)" : "var(--t1)" }}>
          {adapter.available ? agentFoundLabel(adapter) : hint.action}
        </div>
        {!adapter.available && hint.commands.length > 0 && (
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 7 }}>
            {hint.commands.map((command) => (
              <CopyCommandLink
                key={command.command}
                command={command.command}
                label={command.label}
              />
            ))}
          </div>
        )}
        {!adapter.available && adapter.error && (
          <div
            style={{
              marginTop: 6,
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              color: "var(--t3)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {adapter.error}
          </div>
        )}
        {hint.note && <div style={{ marginTop: 5, color: "var(--t3)" }}>{hint.note}</div>}
        {adapter.docs_url && (
          <a
            href={adapter.docs_url}
            target="_blank"
            rel="noreferrer"
            style={{
              display: "inline-flex",
              marginTop: 5,
              color: "var(--teal)",
              textDecoration: "none",
            }}
          >
            setup docs
          </a>
        )}
      </div>
    </>
  );
}

function CopyCommandLink({ command, label }: { command: string; label?: string }) {
  async function copyCommand() {
    try {
      await navigator.clipboard?.writeText(command);
    } catch {
      // Clipboard access can be unavailable in tests or locked-down webviews.
    }
  }
  return (
    <button
      type="button"
      onClick={() => void copyCommand()}
      title={`Copy ${command}`}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 5,
        border: 0,
        background: "transparent",
        color: "var(--teal)",
        padding: 0,
        cursor: "pointer",
        fontFamily: "var(--font-mono)",
        fontSize: 11,
      }}
    >
      <span>{label || command}</span>
      <Icon d={Icons.copy} size={11} />
    </button>
  );
}

function agentSetupHint(adapter: AgentAdapterRecord): {
  label: string;
  action: string;
  commands: Array<{ label: string; command: string }>;
  note?: string;
} {
  switch (adapter.id) {
    case "codex":
      return {
        label: "Codex",
        action: "Install Codex CLI, then sign in with Codex.",
        commands: [{ label: "Auth", command: "codex login" }],
        note: "The ACP adapter is built into Hecate; install the provider CLI from its official instructions. Hecate also recognizes the installed macOS app bundle and uses local CLI auth without storing provider tokens.",
      };
    case "claude_code":
      return {
        label: "Claude Code",
        action: "Install Claude Code, then sign in with Claude Code.",
        commands: [{ label: "Auth", command: "claude /login" }],
        note: "The ACP adapter is built into Hecate; install Claude Code from its official instructions. Windows requires the native CLI build. Hecate uses Claude Code's local auth and does not store Claude tokens.",
      };
    case "cursor_agent":
      return {
        label: "Cursor Agent",
        action: "Install the Cursor Agent CLI, then sign in with Cursor Agent.",
        commands: [{ label: "Auth", command: "cursor-agent login" }],
        note: "Hecate does not install Cursor Agent. Use Cursor's CLI installation instructions. The current Windows .cmd-only launcher is not supported because Hecate requires a directly launchable native executable.",
      };
    case "grok_build":
      return {
        label: "Grok Build",
        action: "Install Grok Build, then sign in with Grok.",
        commands: [{ label: "Auth", command: "grok login" }],
        note: "Hecate does not install Grok Build. Use xAI's CLI installation instructions; headless environments can set XAI_API_KEY instead of using browser sign-in.",
      };
    default:
      return {
        label: adapter.command || adapter.id,
        action: "Install the local agent command and test it in Connections.",
        commands: adapter.command
          ? [{ label: "Check", command: `${adapter.command} --version` }]
          : [],
      };
  }
}

function agentFoundLabel(adapter: AgentAdapterRecord): string {
  if (adapter.auth_status === "unauthenticated" || adapter.auth_status === "billing") {
    return adapter.auth_error || `Auth status: ${adapter.auth_status}`;
  }
  if (adapter.agent_version) return `Tested · agent ${adapter.agent_version}`;
  if (adapter.adapter_version) return `Tested · adapter ${adapter.adapter_version}`;
  return "Found · not tested";
}

function RTKOnboardingHint({ path, onEnable }: { path: string; onEnable: () => void }) {
  return (
    <div
      style={{
        margin: "16px auto 0",
        maxWidth: 520,
        border: "1px solid var(--teal-border)",
        borderRadius: "var(--radius)",
        background: "var(--teal-bg)",
        padding: "12px 14px",
        display: "grid",
        gap: 8,
        textAlign: "left",
      }}
    >
      <div
        style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 10 }}
      >
        <div>
          <div style={{ fontSize: 12, fontWeight: 650, color: "var(--teal)" }}>
            Compact command output is available
          </div>
          <div style={{ marginTop: 3, fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
            Hecate found RTK{path ? ` at ${path}` : ""}. Turn it on for this chat now, or change it
            later from Chat settings.
          </div>
        </div>
        <button
          className="btn btn-primary btn-sm"
          type="button"
          onClick={onEnable}
          style={{ flexShrink: 0 }}
        >
          Turn on
        </button>
      </div>
    </div>
  );
}

function HostedRuntimeProviderSetup() {
  return (
    <div
      style={{
        margin: "14px auto 0",
        maxWidth: 640,
        border: "1px solid var(--border)",
        borderRadius: "var(--radius)",
        background: "var(--bg2)",
        padding: 12,
        textAlign: "left",
      }}
    >
      <div
        style={{
          fontSize: 11,
          color: "var(--t2)",
          fontFamily: "var(--font-mono)",
          textTransform: "uppercase",
          letterSpacing: "0.04em",
        }}
      >
        Hosted runtime
      </div>
      <div style={{ fontSize: 12, color: "var(--t3)", lineHeight: 1.45, marginTop: 3 }}>
        Add an API-key provider or agent credential in Connections. This hosted runtime does not
        scan the server for local model apps.
      </div>
    </div>
  );
}

function QuickLocalProviderAdd({
  discoveries,
  error,
  loading,
  presets,
  adding,
  onOpenProviders,
  onAdd,
  onRefresh,
}: {
  discoveries: LocalProviderDiscoveryRecord[];
  error: string;
  loading: boolean;
  presets: ProviderPresetRecord[];
  adding: boolean;
  onOpenProviders: () => void;
  onAdd: (providers: LocalProviderDiscoveryRecord[]) => void;
  onRefresh: () => void;
}) {
  const candidates = discoveries.filter((discovery) => discovery.preset_id != null);
  const candidateKeys = candidates.map(localProviderDiscoveryKey).join("\u0000");
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(
    () => new Set(candidates.map(localProviderDiscoveryKey)),
  );
  useEffect(() => {
    setSelectedKeys(new Set(candidates.map(localProviderDiscoveryKey)));
  }, [candidateKeys]);
  const selectedCandidates = candidates.filter((discovery) =>
    selectedKeys.has(localProviderDiscoveryKey(discovery)),
  );

  if (!loading && !error && candidates.length === 0) return null;

  return (
    <div
      style={{
        margin: "14px auto 0",
        maxWidth: 640,
        border: "1px solid var(--border)",
        borderRadius: "var(--radius)",
        background: "var(--bg2)",
        padding: 12,
        textAlign: "left",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          gap: 10,
          marginBottom: candidates.length > 0 || error ? 12 : 0,
        }}
      >
        <div style={{ flex: 1, minWidth: 0 }}>
          <div
            style={{
              fontSize: 11,
              color: "var(--t2)",
              fontFamily: "var(--font-mono)",
              textTransform: "uppercase",
              letterSpacing: "0.04em",
            }}
          >
            Detected locally
          </div>
          <div style={{ fontSize: 12, color: "var(--t3)", lineHeight: 1.45, marginTop: 3 }}>
            Hecate found local inference tools on this machine. Add them now, then pull or load
            models in the provider app if needed.
          </div>
        </div>
        {loading && (
          <span style={{ fontSize: 11, color: "var(--t3)", paddingTop: 2 }}>Checking…</span>
        )}
        <button
          className="btn btn-ghost btn-sm"
          disabled={loading || adding}
          onClick={onRefresh}
          type="button"
          style={{ padding: "4px 8px", flexShrink: 0 }}
        >
          Check again
        </button>
      </div>
      {error && <InlineError message={error} />}
      {candidates.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))",
              gap: 8,
            }}
          >
            {candidates.map((discovery) => {
              const preset = presets.find((preset) => preset.id === discovery.preset_id);
              const key = localProviderDiscoveryKey(discovery);
              const selected = selectedKeys.has(key);
              const status = localProviderReadiness(discovery);
              const modelCount = discovery.model_count ?? discovery.models?.length ?? 0;
              const detail = discovery.http_available
                ? `${discovery.base_url} · ${modelCount} model${modelCount === 1 ? "" : "s"}`
                : `${discovery.command || "Command"} found${discovery.command_path ? ` · ${discovery.command_path}` : ""}`;
              return (
                <button
                  key={key}
                  type="button"
                  aria-pressed={selected}
                  aria-label={`${selected ? "Deselect" : "Select"} ${preset?.name || discovery.name}`}
                  onClick={() => {
                    setSelectedKeys((current) => {
                      const next = new Set(current);
                      if (next.has(key)) {
                        next.delete(key);
                      } else {
                        next.add(key);
                      }
                      return next;
                    });
                  }}
                  style={{
                    appearance: "none",
                    background: selected ? "var(--teal-bg)" : "transparent",
                    color: "inherit",
                    minHeight: 60,
                    display: "flex",
                    alignItems: "center",
                    gap: 10,
                    border: `1px solid ${selected ? "var(--teal-border)" : "var(--border)"}`,
                    borderRadius: "var(--radius)",
                    cursor: "pointer",
                    padding: "10px 12px",
                    minWidth: 0,
                    textAlign: "left",
                  }}
                >
                  <BrandAvatar
                    brand={discovery.preset_id || discovery.name}
                    fallback={preset?.name || discovery.name}
                    size={28}
                  />
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
                      <div
                        style={{
                          fontSize: 13,
                          fontWeight: 500,
                          color: "var(--t0)",
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                        }}
                      >
                        {preset?.name || discovery.name}
                      </div>
                      <span
                        title={status.title}
                        style={{
                          fontSize: 10,
                          lineHeight: "16px",
                          height: 16,
                          borderRadius: 999,
                          padding: "0 6px",
                          whiteSpace: "nowrap",
                          color: status.color,
                          background: status.background,
                          border: `1px solid ${status.border}`,
                          flexShrink: 0,
                        }}
                      >
                        {status.label}
                      </span>
                    </div>
                    <div
                      title={detail}
                      style={{
                        fontSize: 11,
                        color: "var(--t3)",
                        lineHeight: 1.35,
                        marginTop: 2,
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {detail}
                    </div>
                  </div>
                  <span
                    aria-hidden
                    style={{
                      alignItems: "center",
                      border: `1px solid ${selected ? "var(--teal-border)" : "var(--border)"}`,
                      borderRadius: 999,
                      color: selected ? "var(--teal)" : "var(--t3)",
                      display: "inline-flex",
                      flexShrink: 0,
                      height: 18,
                      justifyContent: "center",
                      width: 18,
                    }}
                  >
                    {selected && <Icon d={Icons.check} size={11} strokeWidth={2} />}
                  </span>
                </button>
              );
            })}
          </div>
          <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 8 }}>
            <span
              style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.4, textAlign: "center" }}
            >
              Selected {selectedCandidates.length} of {candidates.length}. You can edit names and
              URLs later in Connections.
            </span>
            <div style={{ display: "flex", justifyContent: "center", gap: 8, flexWrap: "wrap" }}>
              <button
                className="btn btn-primary btn-sm"
                disabled={adding || selectedCandidates.length === 0}
                onClick={() => onAdd(selectedCandidates)}
                type="button"
                style={{ display: "flex", alignItems: "center" }}
              >
                {adding ? "Adding…" : "Add selected"}
              </button>
              <button
                className="btn btn-ghost btn-sm"
                disabled={adding}
                onClick={onOpenProviders}
                type="button"
              >
                Open Connections
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function localProviderDiscoveryKey(discovery: LocalProviderDiscoveryRecord): string {
  return discovery.preset_id || discovery.base_url || discovery.name;
}

function localProviderReadiness(discovery: LocalProviderDiscoveryRecord): {
  label: string;
  title: string;
  color: string;
  background: string;
  border: string;
} {
  if (discovery.http_available) {
    const models = discovery.model_count
      ? ` · ${discovery.model_count} model${discovery.model_count === 1 ? "" : "s"}`
      : "";
    return {
      label: "Running",
      title: `HTTP probe passed at ${discovery.probe_url}${models}`,
      color: "var(--green)",
      background: "var(--green-bg)",
      border: "var(--green-border)",
    };
  }
  return {
    label: "Installed",
    title: `${discovery.command || "Command"} found${discovery.command_path ? ` at ${discovery.command_path}` : ""}; local HTTP endpoint is not running`,
    color: "var(--amber)",
    background: "var(--amber-bg)",
    border: "var(--amber-border)",
  };
}

const emptyStateShellStyle: CSSProperties = {
  margin: "0 auto",
  maxWidth: 760,
  padding: "clamp(28px, 6vw, 52px) 16px 18px",
  textAlign: "left",
};

const emptyStateHeaderStyle: CSSProperties = {
  margin: "0 auto",
  maxWidth: 620,
  textAlign: "center",
};

const emptyStateKickerStyle: CSSProperties = {
  color: "var(--t3)",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  fontWeight: 650,
  letterSpacing: "0.06em",
  marginBottom: 7,
  textTransform: "uppercase",
};

const emptyStateTitleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: "clamp(18px, 2.4vw, 24px)",
  fontWeight: 720,
  lineHeight: 1.18,
};

const emptyStateDetailStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 12,
  lineHeight: 1.55,
  margin: "8px auto 0",
  maxWidth: 520,
};

const startGuideStyle: CSSProperties = {
  borderBottom: "1px solid var(--border)",
  borderTop: "1px solid var(--border)",
  margin: "20px auto 0",
  maxWidth: 640,
};

const startGuideRowStyle: CSSProperties = {
  alignItems: "center",
  display: "grid",
  gap: 11,
  gridTemplateColumns: "26px minmax(0, 1fr) auto",
  minHeight: 60,
  padding: "11px 0",
};

const startGuideIconStyle: CSSProperties = {
  alignItems: "center",
  border: "1px solid var(--border)",
  borderRadius: 999,
  color: "var(--t2)",
  display: "inline-flex",
  height: 26,
  justifyContent: "center",
  width: 26,
};

const startGuideLabelStyle: CSSProperties = {
  color: "var(--t1)",
  display: "block",
  fontSize: 12,
  fontWeight: 650,
  lineHeight: 1.25,
};

const startGuideDetailStyle: CSSProperties = {
  color: "var(--t3)",
  display: "block",
  fontSize: 11,
  lineHeight: 1.45,
  marginTop: 2,
};

const startGuideStatusStyle: CSSProperties = {
  border: "1px solid var(--border)",
  borderRadius: 999,
  fontSize: 10,
  fontWeight: 650,
  lineHeight: "18px",
  padding: "0 7px",
  whiteSpace: "nowrap",
};

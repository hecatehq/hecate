import { useEffect, useState } from "react";

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
  isCloudRuntime: boolean;
  setupRepair: ChatSetupRepairState | null;
  modelRouteUnavailable: boolean;
  selectedModelIssue: SelectedModelIssue | null;
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
  isCloudRuntime,
  setupRepair,
  modelRouteUnavailable,
  selectedModelIssue,
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
    !isCloudRuntime &&
    modelRouteUnavailable &&
    !hasConfiguredProviders &&
    quickLocalProviders.some((discovery) => discovery.preset_id != null);
  const showPrimaryRepairAction =
    Boolean(setupRepair) &&
    !(setupRepair?.kind === "no_provider" && hasQuickLocalProviderCandidates);
  const readyTitle = isExternalAgentChat
    ? `Ready for ${selectedAgent?.name || "the agent"}`
    : "Ready when you are";
  const readyDetail = isExternalAgentChat
    ? "Describe the task and Hecate will start the selected agent in this workspace."
    : "Ask a question, inspect the workspace, or describe the change you want to make.";
  const title =
    isAgentChat && selectedAgentUnavailable
      ? `${selectedAgent?.name || "Selected agent"} is unavailable`
      : isExternalAgentChat && agentRouteUnavailable
        ? "No available coding agent"
        : nothingRunnable
          ? "Nothing runnable yet"
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
        ? "Hecate did not find any supported coding-agent CLI or managed local runner in the known operator locations."
        : nothingRunnable
          ? "Add a model provider or install a supported coding-agent CLI before sending a message."
          : selectedModelIssue
            ? selectedModelIssue.message
            : setupRepair
              ? setupRepair.message
              : hecateModelUnavailable
                ? "Add a provider with discovered models before sending through Hecate."
                : readyDetail;
  const emptyRepairAction = setupRepair;

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
    <div
      style={{ padding: "28px 16px 18px", maxWidth: 820, margin: "0 auto", textAlign: "center" }}
    >
      <div style={{ fontSize: 13, fontWeight: 600, color: "var(--t1)", marginBottom: 5 }}>
        {title}
      </div>
      <div
        style={{
          fontSize: 12,
          color: "var(--t3)",
          lineHeight: 1.5,
          maxWidth: 430,
          margin: "0 auto",
        }}
      >
        {detail}
      </div>
      {isAgentChat && (agentRouteUnavailable || selectedAgentUnavailable) && (
        <AgentSetupHints adapters={agentAdapters} selectedID={selectedAgent?.id} />
      )}
      {isHecateChat && selectedModelIssue && (
        <SelectedModelReadinessNotice
          issue={selectedModelIssue}
          compact
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
        (isCloudRuntime ? (
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
              color: adapter.available ? "var(--green)" : "var(--red)",
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
        <div style={{ color: adapter.available ? "var(--green)" : "var(--t1)" }}>
          {adapter.available ? agentReadyLabel(adapter) : hint.action}
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
        commands: [
          { label: "Install", command: "npm install -g @openai/codex" },
          { label: "Auth", command: "codex login" },
        ],
      };
    case "claude_code":
      return {
        label: "Claude Code",
        action: "Install Claude Code, then sign in with Claude Code.",
        commands: [
          { label: "Install", command: "npm install -g @anthropic-ai/claude-code" },
          { label: "Auth", command: "claude /login" },
        ],
        note: "Hecate uses Claude Code's local auth; it does not store Claude tokens.",
      };
    case "cursor_agent":
      return {
        label: "Cursor Agent",
        action: "Install Cursor with Agent support, then sign in with Cursor Agent.",
        commands: [{ label: "Auth", command: "cursor-agent login" }],
        note: "Cursor Agent is installed with the Cursor application, not npm.",
      };
    case "grok_build":
      return {
        label: "Grok Build",
        action: "Install Grok Build, then sign in with Grok.",
        commands: [
          { label: "Install", command: "curl -fsSL https://x.ai/cli/install.sh | bash" },
          { label: "Auth", command: "grok login" },
        ],
        note: "Headless environments can set XAI_API_KEY instead of using browser sign-in.",
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

function agentReadyLabel(adapter: AgentAdapterRecord): string {
  if (adapter.auth_status === "unauthenticated" || adapter.auth_status === "billing") {
    return adapter.auth_error || `Auth status: ${adapter.auth_status}`;
  }
  if (adapter.agent_version) return `Ready · agent ${adapter.agent_version}`;
  if (adapter.adapter_version) return `Ready · bridge ${adapter.adapter_version}`;
  return "Ready";
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
          <span style={{ fontSize: 11, color: "var(--t3)", paddingTop: 2 }}>Checking...</span>
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
                {adding ? "Adding..." : "Add selected"}
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

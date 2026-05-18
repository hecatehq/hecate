import { useEffect, useState } from "react";

import { type ChatSetupRepairState } from "../../lib/chat-setup-readiness";
import type { SelectedModelIssue } from "../../lib/provider-issues";
import type { AgentAdapterRecord, AgentAdapterSetupCommandStatus } from "../../types/agent-adapter";
import type { LocalProviderDiscoveryRecord, ProviderPresetRecord } from "../../types/provider";
import { BrandAvatar, Icon, Icons, InlineError } from "../shared/ui";

import { SelectedModelReadinessNotice, repairActionIcon } from "./ChatComposer";
import { AgentSetupHints, ClaudeCodeSetupEmptyPanel } from "./ClaudeCodeSetup";
import type { ClaudeCodePreflightState } from "./ClaudeCodeSetup";

type Props = {
  isAgentChat: boolean;
  isHecateChat: boolean;
  isExternalAgentChat: boolean;
  claudeCodePreflight: ClaudeCodePreflightState | null;
  claudeCodePreflightLoading: boolean;
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
  onSwitchTarget: (target: "model" | "agent" | "external_agent") => void;
  claudeCodeCLI?: AgentAdapterSetupCommandStatus;
  claudeTokenDraft: string;
  claudeTokenSaving: boolean;
  onClaudeTokenDraftChange: (value: string) => void;
  onSaveClaudeCodeToken: () => void;
  onTestClaudeCode: () => void;
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
  claudeCodePreflight,
  claudeCodePreflightLoading,
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
  claudeCodeCLI,
  claudeTokenDraft,
  claudeTokenSaving,
  onClaudeTokenDraftChange,
  onSaveClaudeCodeToken,
  onTestClaudeCode,
  rtkAvailable,
  rtkPath,
  rtkEnabled,
  showRTKOnboardingHint,
  onEnableRTK,
}: Props) {
  const hecateModelUnavailable =
    isHecateChat && (modelRouteUnavailable || Boolean(selectedModelIssue));
  const setupRepairForEmpty = setupRepair?.action === "enable_tools" ? null : setupRepair;
  const readyTitle = isExternalAgentChat
    ? `Ready for ${selectedAgent?.name || "the agent"}`
    : "Ready when you are";
  const readyDetail = isExternalAgentChat
    ? "Describe the task and Hecate will start the selected agent in this workspace."
    : "Ask a question, inspect the workspace, or describe the change you want to make.";
  const title = claudeCodePreflight
    ? "Set up Claude Code"
    : isAgentChat && selectedAgentUnavailable
      ? `${selectedAgent?.name || "Selected agent"} is unavailable`
      : isExternalAgentChat && agentRouteUnavailable
        ? "No available coding agent"
        : nothingRunnable
          ? "Nothing runnable yet"
          : selectedModelIssue
            ? selectedModelIssue.title
            : setupRepairForEmpty
              ? setupRepairForEmpty.title
              : hecateModelUnavailable
                ? "No routable model"
                : readyTitle;
  const detail = claudeCodePreflight
    ? "Claude Code needs its own adapter-visible credential before Hecate can start a session."
    : isAgentChat && selectedAgentUnavailable
      ? `Hecate could not start ${selectedAgent?.name || "the selected agent"} because its CLI is not ready in this environment.`
      : isExternalAgentChat && agentRouteUnavailable
        ? "Hecate did not find any supported coding-agent CLI or local adapter runner in the known operator locations."
        : nothingRunnable
          ? "Add a model provider or install a supported coding-agent CLI before sending a message."
          : selectedModelIssue
            ? selectedModelIssue.message
            : setupRepairForEmpty
              ? setupRepairForEmpty.message
              : hecateModelUnavailable
                ? "Add a provider with discovered models before sending through Hecate."
                : readyDetail;
  const emptyRepairAction =
    setupRepairForEmpty && !claudeCodePreflight ? setupRepairForEmpty : null;

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
      case "enable_tools":
        // Tools-enabled repair is handled by the composer notice, where we
        // can disable the action while capability override writes are busy.
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
      {claudeCodePreflight && (
        <ClaudeCodeSetupEmptyPanel
          state={claudeCodePreflight}
          loading={claudeCodePreflightLoading}
          cliStatus={claudeCodeCLI}
          tokenDraft={claudeTokenDraft}
          tokenSaving={claudeTokenSaving}
          onTokenDraftChange={onClaudeTokenDraftChange}
          onSaveToken={onSaveClaudeCodeToken}
          onTest={onTestClaudeCode}
        />
      )}
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
        !setupRepairForEmpty && <RTKOnboardingHint path={rtkPath} onEnable={onEnableRTK} />}
      {(emptyRepairAction ||
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
          {emptyRepairAction && (
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
          {!emptyRepairAction && (modelRouteUnavailable || selectedModelIssue) && isHecateChat && (
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
          {!modelRouteUnavailable && isAgentChat && (
            <button
              className="btn btn-ghost btn-sm"
              onClick={() => onSwitchTarget("model")}
              type="button"
            >
              <Icon d={Icons.model} size={13} /> Use model
            </button>
          )}
        </div>
      )}
      {isHecateChat && modelRouteUnavailable && !hasConfiguredProviders && (
        <QuickLocalProviderAdd
          discoveries={quickLocalProviders}
          error={quickLocalError}
          loading={quickLocalLoading}
          presets={providerPresets}
          adding={quickAddingProviders}
          onAdd={onQuickAddLocalProviders}
          onRefresh={onRefreshQuickLocalProviders}
        />
      )}
    </div>
  );
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

function QuickLocalProviderAdd({
  discoveries,
  error,
  loading,
  presets,
  adding,
  onAdd,
  onRefresh,
}: {
  discoveries: LocalProviderDiscoveryRecord[];
  error: string;
  loading: boolean;
  presets: ProviderPresetRecord[];
  adding: boolean;
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
            <button
              className="btn btn-primary btn-sm"
              disabled={adding || selectedCandidates.length === 0}
              onClick={() => onAdd(selectedCandidates)}
              type="button"
              style={{ display: "flex", alignItems: "center" }}
            >
              {adding ? "Adding..." : "Add selected"}
            </button>
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

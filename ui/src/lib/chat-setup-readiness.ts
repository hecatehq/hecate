import type { SelectedModelIssue } from "./provider-issues";
import type { ModelRecord } from "../types/model";
import type { ProviderFilter } from "../types/provider";

export type ChatSetupTarget = "model" | "agent" | "external_agent";

export type ChatSetupRepairKind =
  | "no_provider"
  | "no_routable_model"
  | "selected_model_not_ready"
  | "tools_disabled"
  | "workspace_required"
  | "external_agent_unavailable"
  | "claude_code_setup";

export type ChatSetupRepairAction =
  | "open_connections"
  | "choose_workspace"
  | "enable_tools"
  | "use_suggested_model"
  | "open_agent_setup";

export type ChatSetupRepairState = {
  kind: ChatSetupRepairKind;
  title: string;
  message: string;
  action: ChatSetupRepairAction;
  actionLabel: string;
  tone: "amber" | "red";
  suggestedModel?: string;
};

export function resolveChatSetupRepairState({
  target,
  hasConfiguredProviders,
  modelRouteUnavailable,
  selectedModelIssue,
  toolsDisabledForModel,
  workspace,
  selectedAgentName,
  selectedAgentAvailable,
  anyAgentAvailable,
  claudeCodeSetupRequired,
}: {
  target: ChatSetupTarget;
  hasConfiguredProviders: boolean;
  modelRouteUnavailable: boolean;
  selectedModelIssue: SelectedModelIssue | null;
  toolsDisabledForModel: boolean;
  workspace: string;
  selectedAgentName?: string;
  selectedAgentAvailable: boolean;
  anyAgentAvailable: boolean;
  claudeCodeSetupRequired: boolean;
}): ChatSetupRepairState | null {
  if (target === "external_agent") {
    if (!anyAgentAvailable || !selectedAgentAvailable) {
      const agent = selectedAgentName || "Selected agent";
      return {
        kind: "external_agent_unavailable",
        title: anyAgentAvailable ? `${agent} is unavailable` : "No available coding agent",
        message: anyAgentAvailable
          ? `Hecate cannot start ${agent} because its CLI or adapter runner is not ready in this environment.`
          : "Install or connect a supported coding agent before starting an External Agent chat.",
        action: "open_connections",
        actionLabel: "Open Connections",
        tone: "amber",
      };
    }
    if (claudeCodeSetupRequired) {
      return {
        kind: "claude_code_setup",
        title: "Set up Claude Code",
        message:
          "Claude Code needs an adapter-visible setup token before Hecate can start a session.",
        action: "open_agent_setup",
        actionLabel: "Open setup",
        tone: "amber",
      };
    }
    if (!workspace.trim()) {
      return workspaceRepair();
    }
    return null;
  }

  if (modelRouteUnavailable) {
    return {
      kind: hasConfiguredProviders ? "no_routable_model" : "no_provider",
      title: hasConfiguredProviders ? "No routable model" : "No model provider configured",
      message: hasConfiguredProviders
        ? "Hecate can see provider configuration, but no provider currently reports a routable model."
        : "Add a model provider before sending through Hecate.",
      action: "open_connections",
      actionLabel: "Open Connections",
      tone: "amber",
    };
  }

  if (selectedModelIssue) {
    return {
      kind: "selected_model_not_ready",
      title: selectedModelIssue.title,
      message: selectedModelIssue.message,
      action: selectedModelIssue.suggestedModel ? "use_suggested_model" : "open_connections",
      actionLabel: selectedModelIssue.suggestedModel
        ? `Use ${selectedModelIssue.suggestedModel}`
        : "Open Connections",
      tone: "amber",
      suggestedModel: selectedModelIssue.suggestedModel,
    };
  }

  if (target === "agent" && toolsDisabledForModel) {
    return {
      kind: "tools_disabled",
      title: "Tools are unavailable for this model",
      message:
        "You can still chat normally. Hecate will send this turn directly to the selected model unless you enable tool support in Connections.",
      action: "enable_tools",
      actionLabel: "Enable tools",
      tone: "amber",
    };
  }

  if (target === "agent" && !workspace.trim()) {
    return workspaceRepair();
  }

  return null;
}

function workspaceRepair(): ChatSetupRepairState {
  return {
    kind: "workspace_required",
    title: "Choose a workspace",
    message: "Hecate uses the workspace as the working directory for this chat.",
    action: "choose_workspace",
    actionLabel: "Choose workspace",
    tone: "amber",
  };
}

export function modelSelectionHasNoToolCalling({
  models,
  providerFilter,
  model,
}: {
  models: ModelRecord[];
  providerFilter: ProviderFilter;
  model: string;
}): boolean {
  if (!model) return false;
  const matches = models.filter((entry) => {
    if (entry.id !== model) return false;
    if (!providerFilter || providerFilter === "auto") return true;
    return entry.metadata?.provider === providerFilter;
  });
  if (matches.length === 0) return false;
  return matches.every((entry) => entry.metadata?.capabilities?.tool_calling === "none");
}

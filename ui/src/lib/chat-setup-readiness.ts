import type { SelectedModelIssue } from "./provider-issues";
import type { ModelRecord } from "../types/model";
import type { ProviderFilter } from "../types/provider";

export type ChatSetupTarget = "model" | "agent" | "external_agent";

export type ChatSetupRepairKind =
  | "no_provider"
  | "no_routable_model"
  | "selected_model_not_ready"
  | "workspace_required"
  | "external_agent_unavailable"
  | "external_agent_setup";

export type ChatSetupRepairAction =
  | "open_connections"
  | "choose_workspace"
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
  workspace,
  selectedAgentID,
  selectedAgentName,
  selectedAgentAvailable,
  anyAgentAvailable,
  externalAgentSetupRequired,
}: {
  target: ChatSetupTarget;
  hasConfiguredProviders: boolean;
  modelRouteUnavailable: boolean;
  selectedModelIssue: SelectedModelIssue | null;
  workspace: string;
  selectedAgentID?: string;
  selectedAgentName?: string;
  selectedAgentAvailable: boolean;
  anyAgentAvailable: boolean;
  externalAgentSetupRequired: boolean;
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
    if (externalAgentSetupRequired) {
      const agent = selectedAgentName || "Selected agent";
      return {
        kind: "external_agent_setup",
        title: `Set up ${agent}`,
        message: externalAgentSetupMessage(selectedAgentID, agent),
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

  if (target === "agent" && hasConfiguredProviders && !workspace.trim()) {
    return workspaceRepair();
  }

  if (modelRouteUnavailable) {
    return {
      kind: hasConfiguredProviders ? "no_routable_model" : "no_provider",
      title: hasConfiguredProviders ? "No routable model" : "No model provider configured",
      message: hasConfiguredProviders
        ? "Providers are configured, but none currently report a routable model. Start the local provider or refresh Connections after loading a model."
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

  if (target === "agent" && !workspace.trim()) {
    return workspaceRepair();
  }

  return null;
}

function externalAgentSetupMessage(agentID: string | undefined, agent: string): string {
  switch (agentID) {
    case "cursor_agent":
      return "Cursor Agent needs the local CLI installed, available on PATH, and signed in with cursor-agent login before Hecate can start a session.";
    case "grok_build":
      return "Grok Build needs the Grok CLI installed, signed in with grok login, and a model selected before Hecate can start a session.";
    case "claude_code":
      return "Claude Code needs local CLI sign-in before Hecate can start a session.";
    default:
      return `${agent} needs local CLI sign-in before Hecate can start a session.`;
  }
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
  return matches.every(
    (entry) => !toolCallingSupportsTaskMode(entry.metadata?.capabilities?.tool_calling),
  );
}

export function toolCallingSupportsTaskMode(value?: string): boolean {
  return value === "basic" || value === "parallel";
}

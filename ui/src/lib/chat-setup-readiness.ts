import type { SelectedModelIssue } from "./provider-issues";
import type { ExternalAgentReadiness } from "./external-agent-readiness";
import type { ModelRecord } from "../types/model";
import type { ConfiguredProviderRecord, ProviderFilter } from "../types/provider";
import { providerAliasesForKey, providerKeyMatches } from "./provider-utils";

export type ChatSetupTarget = "agent" | "external_agent";

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
  workspaceRequired,
  selectedAgentID,
  selectedAgentName,
  selectedAgentAvailable,
  anyAgentAvailable,
  externalAgentSetupRequired,
  selectedAgentReadiness,
}: {
  target: ChatSetupTarget;
  hasConfiguredProviders: boolean;
  modelRouteUnavailable: boolean;
  selectedModelIssue: SelectedModelIssue | null;
  workspace: string;
  workspaceRequired?: boolean;
  selectedAgentID?: string;
  selectedAgentName?: string;
  selectedAgentAvailable: boolean;
  anyAgentAvailable: boolean;
  externalAgentSetupRequired: boolean;
  selectedAgentReadiness?: ExternalAgentReadiness;
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
        title: externalAgentSetupTitle(agent, selectedAgentReadiness),
        message: externalAgentSetupMessage(selectedAgentID, agent, selectedAgentReadiness),
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

  const shouldRequireWorkspace = workspaceRequired ?? target === "agent";

  if (target === "agent" && shouldRequireWorkspace && hasConfiguredProviders && !workspace.trim()) {
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

  if (target === "agent" && shouldRequireWorkspace && !workspace.trim()) {
    return workspaceRepair();
  }

  return null;
}

function externalAgentSetupTitle(agent: string, readiness?: ExternalAgentReadiness): string {
  switch (readiness?.kind) {
    case "billing":
      return `Check ${agent} billing`;
    case "issue":
      return `Check ${agent}`;
    default:
      return `Set up ${agent}`;
  }
}

function externalAgentSetupMessage(
  agentID: string | undefined,
  agent: string,
  readiness?: ExternalAgentReadiness,
): string {
  if (readiness) {
    switch (readiness.kind) {
      case "sign_in":
        return readiness.signInHint || readiness.detail || `${agent} needs local CLI sign-in.`;
      case "setup":
        return readiness.detail || readiness.setupHint;
      case "billing":
        return (
          readiness.detail ||
          `Check ${agent}'s billing or subscription, then test the adapter again in Connections.`
        );
      case "issue":
        return readiness.detail || `Open Connections and test ${agent} again.`;
      default:
        break;
    }
  }

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
  configuredProviders,
}: {
  models: ModelRecord[];
  providerFilter: ProviderFilter;
  model: string;
  configuredProviders: ConfiguredProviderRecord[];
}): boolean {
  if (!model) return false;
  const providerAliases = providerAliasesForSelection(providerFilter, configuredProviders);
  const matches = models.filter((entry) => {
    if (entry.id !== model) return false;
    return !providerAliases || providerKeyMatches(entry.metadata?.provider, providerAliases);
  });
  if (matches.length === 0) return false;
  return matches.every(
    (entry) => !toolCallingSupportsTaskMode(entry.metadata?.capabilities?.tool_calling),
  );
}

export function toolCallingSupportsTaskMode(value?: string): boolean {
  return value === "basic" || value === "parallel";
}

export type ImageInputCapability = "unknown" | "none" | "supported";

export function modelSelectionImageInputCapability({
  models,
  providerFilter,
  model,
  configuredProviders,
}: {
  models: ModelRecord[];
  providerFilter: ProviderFilter;
  model: string;
  configuredProviders: ConfiguredProviderRecord[];
}): ImageInputCapability {
  if (!model) return "unknown";
  const providerAliases = providerAliasesForSelection(providerFilter, configuredProviders);
  const matches = models
    .filter((entry) => {
      if (entry.id !== model) return false;
      return !providerAliases || providerKeyMatches(entry.metadata?.provider, providerAliases);
    })
    .filter((entry) => {
      const readiness = entry.metadata?.readiness;
      return !readiness || (readiness.ready && readiness.routing_ready !== false);
    });
  if (matches.length === 0) return "unknown";
  const capabilities = matches.map((entry) =>
    normalizeImageInputCapability(entry.metadata?.capabilities?.image_input),
  );
  if (capabilities.some((capability) => capability === "supported")) return "supported";
  if (capabilities.every((capability) => capability === "none")) return "none";
  return "unknown";
}

export function normalizeImageInputCapability(value?: string): ImageInputCapability {
  if (value === "supported" || value === "none") return value;
  return "unknown";
}

function providerAliasesForSelection(
  providerFilter: ProviderFilter,
  configuredProviders: ConfiguredProviderRecord[],
): Set<string> | null {
  if (!providerFilter || providerFilter === "auto") return null;
  return providerAliasesForKey(providerFilter, configuredProviders);
}

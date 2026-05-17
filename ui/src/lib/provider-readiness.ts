import type { ConfiguredProviderRecord, ProviderReadinessCheckRecord, ProviderRecord } from "../types/provider";
import { describeRoutingBlockedReason } from "./runtime-routing";

export type ProviderRepairHint = {
  title: string;
  message: string;
  action: string;
  actionKind: "add_provider" | "open_provider" | "refresh_providers" | "none";
  providerID?: string;
  tone: "muted" | "green" | "amber" | "red";
};

export function providerReadinessMeaning({
  configuredCount,
  readyCount,
  blockedCount,
  modelCount,
  repair,
}: {
  configuredCount: number;
  readyCount: number;
  blockedCount: number;
  modelCount: number;
  repair?: ProviderRepairHint | null;
}): { message: string; tone: "muted" | "amber" } {
  if (configuredCount === 0) {
    return { message: "No model providers are configured yet. Add one provider before starting Hecate Chat.", tone: "amber" };
  }
  if (blockedCount > 0 && repair && repair.tone !== "muted") {
    return { message: `${blockedCount} provider${blockedCount === 1 ? " needs" : "s need"} attention. Next: ${repair?.action || "open the provider list."}`, tone: "amber" };
  }
  if (readyCount === 0) {
    return { message: "Providers exist, but none are ready to route chat requests yet.", tone: "amber" };
  }
  if (modelCount === 0) {
    return { message: "Providers are reachable, but no models have been discovered yet.", tone: "amber" };
  }
  return { message: `${readyCount} provider${readyCount === 1 ? "" : "s"} ready with ${modelCount} discovered model${modelCount === 1 ? "" : "s"}.`, tone: "muted" };
}

export function readinessRecommendation(check: ProviderReadinessCheckRecord): string {
  if (check.operator_action) return check.operator_action;

  switch (check.reason) {
    case "credential_missing":
      return "Add or rotate this provider's API key.";
    case "provider_disabled":
      return check.name === "models"
        ? "Enable the provider before model discovery can run."
        : "Enable the provider when you want Hecate to route to it.";
    case "self_referential":
      return "Change the base URL so it points at the provider, not Hecate.";
    case "discovery_failed":
      return "Check the endpoint, then refresh provider status after the server is reachable.";
    case "default_model_only":
      return "Send a test request or refresh discovery to confirm the default model is real.";
    case "no_models":
      return "Start the provider and pull or load at least one model.";
    case "provider_slow":
      return "Keep it enabled if acceptable, or route to a faster provider.";
    case "provider_rate_limited":
      return "Wait for cooldown or temporarily route to another provider.";
    case "provider_unhealthy":
      return "Inspect the latest health error and provider server logs.";
    case "circuit_open":
      return "Wait for recovery or test the provider after fixing the upstream issue.";
    case "recovery_probe":
      return "Retry once the half-open probe succeeds.";
    default:
      return "";
  }
}

export function providerRepairHint({
  configuredProvider,
  runtimeProvider,
}: {
  configuredProvider?: Pick<ConfiguredProviderRecord, "id" | "name" | "kind" | "credential_configured">;
  runtimeProvider?: ProviderRecord;
}): ProviderRepairHint {
  const name = configuredProvider?.name || runtimeProvider?.name || configuredProvider?.id || "Provider";
  const isLocal = configuredProvider?.kind === "local" || runtimeProvider?.kind === "local";
  const modelCount = runtimeProvider?.model_count ?? runtimeProvider?.models?.length ?? 0;

  if (configuredProvider && !isLocal && !configuredProvider.credential_configured) {
    return {
      title: "Credentials required",
      message: `${name} is configured but cannot route requests until an API key is saved.`,
      action: "Add or rotate the provider API key in Connections.",
      actionKind: "open_provider",
      providerID: configuredProvider.id,
      tone: "amber",
    };
  }

  if (configuredProvider && !runtimeProvider) {
    return {
      title: "No models discovered",
      message: `${name} is configured, but Hecate does not have a current model-discovery result for it yet.`,
      action: isLocal ? "Start the local provider process, pull or load a model, then refresh Connections." : "Confirm the account has model access, then refresh Connections.",
      actionKind: "refresh_providers",
      providerID: configuredProvider.id,
      tone: "amber",
    };
  }

  if (runtimeProvider?.readiness?.status === "blocked") {
    const blockedCheck = firstBlockedReadinessCheck(runtimeProvider.readiness_checks ?? []);
    const actionKind = actionableProviderAction(readinessActionKind(blockedCheck), configuredProvider?.id);
    return {
      title: "Provider blocked",
      message: runtimeProvider.readiness.message || `${name} is not routable right now.`,
      action: runtimeProvider.readiness.operator_action || firstReadinessAction(runtimeProvider.readiness_checks) || "Open Connections and inspect the blocked readiness check.",
      actionKind,
      providerID: configuredProvider?.id,
      tone: "amber",
    };
  }

  const blockedCheck = firstBlockedReadinessCheck(runtimeProvider?.readiness_checks ?? []);
  if (blockedCheck) {
    const actionKind = actionableProviderAction(readinessActionKind(blockedCheck), configuredProvider?.id);
    return {
      title: readinessTitle(blockedCheck),
      message: blockedCheck.message || `${name} has a blocked readiness check.`,
      action: readinessRecommendation(blockedCheck) || "Open Connections and inspect provider readiness.",
      actionKind,
      providerID: configuredProvider?.id,
      tone: "amber",
    };
  }

  if (runtimeProvider?.routing_blocked_reason) {
    const reason = describeRoutingBlockedReason(runtimeProvider.routing_blocked_reason);
    return {
      title: "Routing blocked",
      message: `${name} is configured, but routing is blocked: ${reason}.`,
      action: "Open Connections and inspect routing, health, and discovery details.",
      actionKind: actionableProviderAction("open_provider", configuredProvider?.id),
      providerID: configuredProvider?.id,
      tone: "amber",
    };
  }

  if (runtimeProvider && modelCount === 0 && runtimeProvider.healthy) {
    return {
      title: "No models discovered",
      message: `${name} is reachable, but Hecate has not discovered any models from it yet.`,
      action: isLocal ? "Pull or load a model in the local provider, then refresh Connections." : "Confirm the account has model access, then refresh Connections.",
      actionKind: "refresh_providers",
      providerID: configuredProvider?.id,
      tone: "amber",
    };
  }

  if (runtimeProvider?.status === "open" || runtimeProvider?.status === "unhealthy" || runtimeProvider?.healthy === false) {
    return {
      title: "Provider unavailable",
      message: `${name} is currently down or cooling down after failures.`,
      action: isLocal ? "Start the local provider process and refresh Connections." : "Check the upstream endpoint, credentials, and provider status.",
      actionKind: "refresh_providers",
      providerID: configuredProvider?.id,
      tone: "amber",
    };
  }

  if (configuredProvider || runtimeProvider) {
    return {
      title: "Ready",
      message: `${name} has no provider setup issue that needs repair.`,
      action: "No repair needed.",
      actionKind: "none",
      providerID: configuredProvider?.id,
      tone: "muted",
    };
  }

  return {
    title: "No provider selected",
    message: "Choose or add a provider before sending model traffic.",
    action: "Add a provider in Connections.",
    actionKind: "add_provider",
    tone: "amber",
  };
}

export function providerFleetRepairHint(
  providers: Array<Pick<ConfiguredProviderRecord, "id" | "name" | "kind" | "credential_configured">>,
  statusByName: Map<string, ProviderRecord>,
): ProviderRepairHint | null {
  for (const provider of providers) {
    const hint = providerRepairHint({
      configuredProvider: provider,
      runtimeProvider: statusByName.get(provider.id),
    });
    if (hint.tone !== "muted") return hint;
  }
  if (providers.length > 0) {
    return {
      title: "Ready",
      message: "No configured provider setup issue needs repair.",
      action: "No repair needed.",
      actionKind: "none",
      tone: "muted",
    };
  }
  return {
    title: "No provider configured",
    message: "Add a model provider before starting Hecate Chat.",
    action: "Add a provider in Connections.",
    actionKind: "add_provider",
    tone: "amber",
  };
}

export function providerRepairActionLabel(actionKind: ProviderRepairHint["actionKind"]): string | null {
  switch (actionKind) {
    case "add_provider":
      return "Add provider";
    case "open_provider":
      return "Open provider";
    case "refresh_providers":
      return "Refresh providers";
    case "none":
      return null;
  }
}

function actionableProviderAction(
  actionKind: ProviderRepairHint["actionKind"] | null,
  configuredProviderID?: string,
): ProviderRepairHint["actionKind"] {
  if (actionKind === "open_provider" && !configuredProviderID) {
    return "refresh_providers";
  }
  return actionKind ?? (configuredProviderID ? "open_provider" : "refresh_providers");
}

function firstBlockedReadinessCheck(checks: ProviderReadinessCheckRecord[]): ProviderReadinessCheckRecord | null {
  return checks.find((check) => check.status === "blocked") ?? null;
}

function firstReadinessAction(checks?: ProviderReadinessCheckRecord[]): string {
  if (!checks) return "";
  for (const check of checks) {
    const action = readinessRecommendation(check);
    if (action) return action;
  }
  return "";
}

function readinessActionKind(check: ProviderReadinessCheckRecord | null): ProviderRepairHint["actionKind"] | null {
  switch (check?.reason) {
    case "credential_missing":
    case "provider_disabled":
    case "self_referential":
      return "open_provider";
    case "no_models":
    case "discovery_failed":
    case "provider_unhealthy":
    case "provider_rate_limited":
    case "circuit_open":
    case "recovery_probe":
      return "refresh_providers";
    default:
      return null;
  }
}

function titleizeReadinessName(value: string): string {
  return value
    .split("_")
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function readinessTitle(check: ProviderReadinessCheckRecord): string {
  switch (check.reason) {
    case "credential_missing":
      return "Credentials required";
    case "no_models":
      return "No models discovered";
    case "discovery_failed":
      return "Discovery failed";
    case "provider_unhealthy":
      return "Provider unavailable";
    case "provider_rate_limited":
      return "Provider rate limited";
    case "self_referential":
      return "Invalid endpoint";
    default:
      return titleizeReadinessName(check.name);
  }
}

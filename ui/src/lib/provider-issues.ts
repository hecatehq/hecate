import type { ModelRecord } from "../types/model";
import type { ConfiguredProviderRecord, ProviderRecord } from "../types/provider";

export type LocalProviderIssue = {
  provider: string;
  model: string;
  message: string;
  command?: string;
};

export type SelectedModelIssue = {
  title: string;
  message: string;
  providerLabel: string;
  model: string;
  suggestedModel?: string;
  details: Array<{ label: string; value: string }>;
  steps: string[];
};

export function buildLocalProviderIssue(provider: ProviderRecord): LocalProviderIssue | null {
  if (provider.kind !== "local" || !provider.default_model) {
    return null;
  }
  if (provider.models?.includes(provider.default_model)) {
    return null;
  }

  const issue: LocalProviderIssue = {
    provider: provider.name,
    model: provider.default_model,
    message:
      "This usually means the model is not installed yet, the local runtime is not fully up, or the provider's model discovery endpoint is returning a different set than your env config expects.",
  };
  if (provider.name === "ollama") {
    issue.command = `ollama pull ${provider.default_model}`;
  }
  return issue;
}

export function buildSelectedModelIssue({
  model,
  providerFilter,
  selectableModels,
  configuredProvider,
  runtimeProvider,
}: {
  model: string;
  providerFilter: string;
  selectableModels: ModelRecord[];
  configuredProvider?: ConfiguredProviderRecord;
  runtimeProvider?: ProviderRecord;
}): SelectedModelIssue | null {
  if (!model) {
    return null;
  }
  const matchingModels = selectableModels.filter((entry) => entry.id === model);
  const providerMatches =
    providerFilter === "auto"
      ? matchingModels
      : matchingModels.filter((entry) => entry.metadata?.provider === providerFilter);
  const readinessCandidates = providerMatches.length > 0 ? providerMatches : matchingModels;
  if (readinessCandidates.length > 0) {
    const readyCandidate = readinessCandidates.find(
      (entry) => entry.metadata?.readiness?.ready !== false,
    );
    if (readyCandidate) {
      return null;
    }
    const readiness = readinessCandidates[0]?.metadata?.readiness;
    if (readiness) {
      const providerLabel =
        providerFilter === "auto"
          ? readiness.matched_provider || readiness.provider || "All providers"
          : configuredProvider?.name || runtimeProvider?.name || providerFilter;
      const details = [
        { label: "Selected model", value: model },
        { label: "Provider route", value: providerLabel },
        { label: "Reason", value: readiness.reason || "not ready" },
      ];
      if (readiness.provider_status) {
        details.push({ label: "Health", value: readiness.provider_status });
      }
      if (readiness.provider_blocked_reason) {
        details.push({ label: "Blocked by", value: readiness.provider_blocked_reason });
      }
      if (readiness.suggested_models?.length) {
        details.push({
          label: "Try instead",
          value: readiness.suggested_models.slice(0, 3).join(", "),
        });
      }
      const steps = [
        readiness.operator_action ||
          "Open Connections to inspect readiness and repair the blocked dependency.",
        readiness.suggested_models?.length
          ? `Try ${readiness.suggested_models[0]} from the model picker.`
          : "Refresh Connections after changing credentials, health, or local model availability.",
      ].filter(Boolean);
      return {
        title: "Selected model is not ready",
        message: readiness.message || `The selected model "${model}" is not routable right now.`,
        providerLabel,
        model,
        suggestedModel: readiness.suggested_models?.[0],
        details,
        steps,
      };
    }
  }
  if (matchingModels.length > 0 && providerFilter === "auto") {
    return null;
  }

  const providerLabel =
    providerFilter === "auto"
      ? "All providers"
      : configuredProvider?.name || runtimeProvider?.name || providerFilter;
  const isLocal = configuredProvider?.kind === "local" || runtimeProvider?.kind === "local";
  const modelCount =
    runtimeProvider?.model_count ?? runtimeProvider?.models?.length ?? selectableModels.length;
  const details = [
    { label: "Selected model", value: model },
    { label: "Provider route", value: providerLabel },
    { label: "Discovered models", value: modelCount > 0 ? String(modelCount) : "none" },
  ];
  if (runtimeProvider?.status) {
    details.push({ label: "Health", value: runtimeProvider.status });
  }
  if (runtimeProvider?.routing_blocked_reason) {
    details.push({ label: "Blocked by", value: runtimeProvider.routing_blocked_reason });
  }
  if (runtimeProvider?.last_error) {
    details.push({ label: "Last error", value: runtimeProvider.last_error });
  }

  const title =
    providerFilter === "auto"
      ? "Selected model is not routable"
      : "Selected model is not available from this provider";
  const message =
    providerFilter === "auto"
      ? `No configured provider currently reports "${model}". Pick a discovered model or add a provider that serves it.`
      : `${providerLabel} is configured, but it does not currently report "${model}" in model discovery.`;
  const steps =
    providerFilter === "auto"
      ? [
          "Pick a model that appears in the model picker.",
          "Open Connections to inspect discovery, health, routing readiness, and credential state.",
          "If the model should be served locally, start the local provider and refresh Connections.",
        ]
      : isLocal
        ? [
            "Start the local provider app or server.",
            `Pull or load ${model} in that provider, or pick one of its discovered models.`,
            "Refresh Connections to update the discovered model list.",
          ]
        : [
            "Check provider credentials and account model access.",
            "Pick a model that appears in the model picker, or add a provider that serves this model.",
            "Open Connections to inspect health, discovery, and routing readiness.",
          ];

  return { title, message, providerLabel, model, details, steps };
}

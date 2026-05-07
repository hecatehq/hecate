import type { ConfiguredProviderRecord, ModelRecord, ProviderRecord } from "../types/runtime";

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
  if (selectableModels.some((entry) => entry.id === model)) {
    return null;
  }

  const providerLabel = providerFilter === "auto"
    ? "All providers"
    : configuredProvider?.name || runtimeProvider?.name || providerFilter;
  const isLocal = configuredProvider?.kind === "local" || runtimeProvider?.kind === "local";
  const modelCount = runtimeProvider?.model_count ?? runtimeProvider?.models?.length ?? selectableModels.length;
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

  const title = providerFilter === "auto"
    ? "Selected model is not routable"
    : "Selected model is not available from this provider";
  const message = providerFilter === "auto"
    ? `No configured provider currently reports "${model}". Pick a discovered model or add a provider that serves it.`
    : `${providerLabel} is configured, but it does not currently report "${model}" in model discovery.`;
  const steps = isLocal
    ? [
        "Start the local provider app or server.",
        `Pull or load ${model} in that provider, or pick one of its discovered models.`,
        "Refresh Providers to update the discovered model list.",
      ]
    : [
        "Check provider credentials and account model access.",
        "Pick a model that appears in the model picker, or add a provider that serves this model.",
        "Open Providers to inspect health, discovery, and routing readiness.",
      ];

  return { title, message, providerLabel, model, details, steps };
}

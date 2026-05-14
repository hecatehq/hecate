export type GatewayErrorDiagnostic = {
  title: string;
  action: string;
  tone: "danger" | "warning";
};

const diagnostics: Record<string, GatewayErrorDiagnostic> = {
  provider_auth_failed: {
    title: "Provider credentials failed",
    action: "Update the provider API key or disable the provider until credentials are fixed.",
    tone: "danger",
  },
  provider_rate_limited: {
    title: "Provider rate limited the request",
    action: "Retry later, reduce concurrency, or route to another provider.",
    tone: "warning",
  },
  provider_unavailable: {
    title: "Provider is unavailable",
    action: "Check provider health, local runtime process, base URL, or fail over to another model.",
    tone: "danger",
  },
  unsupported_model: {
    title: "Model is not supported by this route",
    action: "Choose a model listed for the selected provider, or switch provider route back to Auto.",
    tone: "warning",
  },
  route_impossible: {
    title: "No route could serve this request",
    action: "Enable a healthy provider, discover models, or choose a different provider route.",
    tone: "danger",
  },
  rate_limit_exceeded: {
    title: "Gateway rate limit exceeded",
    action: "Wait for the bucket to refill or adjust the per-key rate limit.",
    tone: "warning",
  },
  forbidden: {
    title: "Request blocked by policy",
    action: "Review tenant key scope, provider/model allowlists, and policy rules.",
    tone: "warning",
  },
  "agent_chat.workspace_required": {
    title: "Workspace required",
    action: "Choose a workspace before using Hecate Agent or External Agent.",
    tone: "warning",
  },
  "agent_chat.model_required": {
    title: "Model required",
    action: "Choose a model from the chat header, or add a provider that reports models.",
    tone: "warning",
  },
  model_not_configured: {
    title: "Selected model is unavailable",
    action: "Choose a discovered model, refresh provider status, or open Connections to fix model discovery.",
    tone: "warning",
  },
  "agent_chat.model_capability_required": {
    title: "Tools unavailable for this model",
    action: "Turn tools off for direct model chat, test the model, or enable tool support in Connections.",
    tone: "warning",
  },
  "agent_chat.agent_session_busy": {
    title: "Chat is still working",
    action: "Open the backing task, resolve the approval, or stop the run before sending another message.",
    tone: "warning",
  },
  "agent_chat.runtime_mismatch": {
    title: "Wrong chat runtime",
    action: "Start a new chat or switch back to the runtime that created this session.",
    tone: "warning",
  },
  "agent_chat.runtime_kind_invalid": {
    title: "Unsupported chat runtime",
    action: "Use one of the supported chat modes: model, agent, or external_agent.",
    tone: "warning",
  },
  "agent_chat.adapter_not_found": {
    title: "External agent is unavailable",
    action: "Open Connections and test the external agent adapter, or choose another agent.",
    tone: "warning",
  },
  "agent_chat.session_stopping": {
    title: "Chat is stopping",
    action: "Wait a moment, then retry the action.",
    tone: "warning",
  },
  "agent_chat.session_not_running": {
    title: "No active run",
    action: "Send a new message if you want to start another run.",
    tone: "warning",
  },
};

export function describeGatewayError(code?: string, status?: number): GatewayErrorDiagnostic | null {
  if (code && diagnostics[code]) {
    return diagnostics[code];
  }
  if (status === 401 || status === 403) {
    return diagnostics.forbidden;
  }
  if (status === 429) {
    return diagnostics.rate_limit_exceeded;
  }
  if (status && status >= 500) {
    return {
      title: "Gateway or upstream failed",
      action: "Open Observability for the request trace and inspect provider health.",
      tone: "danger",
    };
  }
  return null;
}

export function formatErrorCode(code?: string, status?: number): string {
  if (code && status) {
    return `${status} · ${code}`;
  }
  if (code) {
    return code;
  }
  if (status) {
    return String(status);
  }
  return "";
}

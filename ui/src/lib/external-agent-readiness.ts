import type { AgentAdapterHealthRecord, AgentAdapterRecord } from "../types/agent-adapter";

export type ExternalAgentReadinessKind =
  | "ready"
  | "unverified"
  | "sign_in"
  | "setup"
  | "billing"
  | "issue";
export type ExternalAgentReadinessTone = "green" | "amber" | "red" | "muted";

export type ExternalAgentReadiness = {
  kind: ExternalAgentReadinessKind;
  tone: ExternalAgentReadinessTone;
  label: string;
  needsRepair: boolean;
  loginCommand: string;
  setupHint: string;
  signInHint: string;
  detail?: string;
  authStatus?: string;
  authError?: string;
  verifiedByProbe: boolean;
};

export function resolveExternalAgentReadiness(
  adapter: AgentAdapterRecord | undefined,
  health: AgentAdapterHealthRecord | null,
): ExternalAgentReadiness {
  if (!adapter) {
    return {
      kind: "setup",
      tone: "muted",
      label: "not configured",
      needsRepair: true,
      loginCommand: "",
      setupHint: "Choose an available external agent in Connections.",
      signInHint: "Choose an available external agent in Connections.",
      verifiedByProbe: false,
    };
  }

  const verifiedByProbe = health?.status === "ready";
  const authStatus = verifiedByProbe ? "ok" : adapter.auth_status;
  const authError = verifiedByProbe ? "" : adapter.auth_error;
  const loginCommand = externalAgentLoginCommand(adapter);
  const setupHint = externalAgentSetupHint(adapter);
  const signInHint = externalAgentSignInHint(adapter);
  const localAuthNeedsRepair =
    authStatus === "unauthenticated" || health?.status === "auth_required";
  const visibleProbeError =
    health && shouldShowProbeError(health) ? humanizeProbeError(health.error ?? "") : "";

  if (verifiedByProbe) {
    return {
      kind: "ready",
      tone: "green",
      label: "ready",
      needsRepair: false,
      loginCommand,
      setupHint,
      signInHint,
      authStatus,
      authError,
      verifiedByProbe,
    };
  }

  if (isBillingStatus(authStatus, health)) {
    return {
      kind: "billing",
      tone: "amber",
      label: "billing",
      needsRepair: true,
      loginCommand,
      setupHint,
      signInHint,
      detail: health?.hint || authError || visibleProbeError || "Check billing or subscription.",
      authStatus,
      authError,
      verifiedByProbe,
    };
  }

  if (localAuthNeedsRepair) {
    return {
      kind: "sign_in",
      tone: "amber",
      label: "sign in",
      needsRepair: true,
      loginCommand,
      setupHint,
      signInHint,
      detail: health?.hint || authError || signInHint,
      authStatus,
      authError,
      verifiedByProbe,
    };
  }

  if (!adapter.available || health?.status === "not_installed" || isSetupProbe(health)) {
    return {
      kind: "setup",
      tone: "muted",
      label: "not configured",
      needsRepair: true,
      loginCommand,
      setupHint,
      signInHint,
      detail: health?.hint || authError || setupHint,
      authStatus,
      authError,
      verifiedByProbe,
    };
  }

  if (
    health?.status === "error" ||
    (authStatus && authStatus !== "ok" && authStatus !== "unknown")
  ) {
    return {
      kind: "issue",
      tone: "amber",
      label: "needs attention",
      needsRepair: true,
      loginCommand,
      setupHint,
      signInHint,
      detail: health?.hint || authError || visibleProbeError || setupHint,
      authStatus,
      authError,
      verifiedByProbe,
    };
  }

  return {
    kind: "unverified",
    tone: "muted",
    label: "not verified",
    needsRepair: false,
    loginCommand,
    setupHint,
    signInHint,
    detail: "Test this agent in Connections to verify local auth before the first prompt.",
    authStatus,
    authError,
    verifiedByProbe,
  };
}

export function externalAgentLoginCommand(adapter: AgentAdapterRecord): string {
  switch (adapter.id) {
    case "codex":
      return "codex login";
    case "claude_code":
      return "claude /login";
    case "cursor_agent":
      return "cursor-agent login";
    case "grok_build":
      return "grok login";
    default:
      return "";
  }
}

export function externalAgentSignInHint(adapter: AgentAdapterRecord): string {
  switch (adapter.id) {
    case "codex":
      return "Run codex login in Terminal, then test the agent again.";
    case "claude_code":
      return "Run claude /login in Terminal, or set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN for the adapter environment.";
    case "cursor_agent":
      return "Run cursor-agent login, or set CURSOR_API_KEY for the adapter environment.";
    case "grok_build":
      return "Run grok login, or set XAI_API_KEY for the adapter environment.";
    default:
      return externalAgentSetupHint(adapter);
  }
}

export function externalAgentSetupHint(adapter: AgentAdapterRecord): string {
  if (adapter.id === "cursor_agent") {
    return "Install Cursor's command-line agent, confirm cursor-agent is on PATH, then run cursor-agent login.";
  }
  if (adapter.id === "grok_build") {
    return "Install the Grok CLI, confirm grok is on PATH, then run grok login. Grok Build also needs a model selected before send.";
  }
  if (adapter.managed_package) {
    return `Install Node/npm so Hecate can manage "${adapter.managed_package}", or install ${adapter.command} directly.`;
  }
  if (adapter.docs_url) {
    return `Install ${adapter.name} and ensure ${adapter.command} is on PATH. Setup docs: ${adapter.docs_url}`;
  }
  return `Install ${adapter.name} and ensure ${adapter.command} is on PATH.`;
}

export function shouldShowProbeError(health: AgentAdapterHealthRecord): boolean {
  const error = health.error?.trim() ?? "";
  if (!error) return false;
  if (error.includes("HECATE_AGENT_ADAPTER_DEV_OVERRIDES")) return false;
  if (error.startsWith("forced ")) return false;
  return true;
}

export function humanizeProbeError(error: string): string {
  const trimmed = error.trim();
  if (!trimmed) return "";
  try {
    const parsed = JSON.parse(trimmed) as {
      message?: unknown;
      data?: { error?: unknown };
    };
    const message = typeof parsed.message === "string" ? parsed.message : "";
    const detail = typeof parsed.data?.error === "string" ? parsed.data.error : "";
    if (message && detail) return `${message}: ${detail}`;
    return detail || message || trimmed;
  } catch {
    return trimmed;
  }
}

function isBillingStatus(
  authStatus: string | undefined,
  health: AgentAdapterHealthRecord | null,
): boolean {
  if (authStatus === "billing") return true;
  const text = `${health?.hint ?? ""} ${health?.error ?? ""}`.toLowerCase();
  return Boolean(health?.status === "error" && text.includes("billing"));
}

function isSetupProbe(health: AgentAdapterHealthRecord | null): boolean {
  // The gateway currently reports setup-shaped probe failures as normalized
  // hint/error text instead of a structured reason. Keep this parser local to
  // the readiness helper until the probe response grows a reason field.
  if (!health || health.status !== "error") return false;
  const text = `${health.hint ?? ""} ${health.error ?? ""}`.toLowerCase();
  return (
    text.includes("app cli missing") ||
    text.includes("command was not found") ||
    text.includes("setup docs:") ||
    text.startsWith("install ")
  );
}

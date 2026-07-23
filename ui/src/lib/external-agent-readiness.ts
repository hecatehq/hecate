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
  launchBlocked: boolean;
  loginCommand: string;
  setupHint: string;
  signInHint: string;
  detail?: string;
  authStatus?: string;
  authError?: string;
  checkedByProbe: boolean;
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
      launchBlocked: true,
      loginCommand: "",
      setupHint: "Choose an available external agent in Connections.",
      signInHint: "Choose an available external agent in Connections.",
      checkedByProbe: false,
    };
  }

  // Diagnostics describe the last disposable ACP session; they never
  // authorize a later process launch or prove that a deferred vendor CLI can
  // authenticate and serve a message. Current passive discovery and remote
  // credential posture are the only client-side launch gates.
  const launchBlocked = !adapter.available || adapter.remote_credential_ok === false;
  const checkedByProbe = health?.status === "ready" && !launchBlocked;
  const authStatus = adapter.auth_status;
  const authError = adapter.auth_error;
  const loginCommand = externalAgentLoginCommand(adapter);
  const setupHint = externalAgentSetupHint(adapter);
  const signInHint = externalAgentSignInHint(adapter);
  const localAuthNeedsRepair =
    authStatus === "unauthenticated" || health?.status === "auth_required";
  const visibleProbeError =
    health && shouldShowProbeError(health) ? humanizeProbeError(health.error ?? "") : "";

  // Remote runtimes report a missing required credential as unavailable too.
  // Preserve the more actionable credential diagnosis instead of presenting
  // that wire shape as a missing local executable.
  if (adapter.remote_credential_ok === false) {
    const remoteCredentialHint =
      adapter.remote_credential_hint ||
      authError ||
      `Configure a remote credential for ${adapter.name}.`;
    return {
      kind: "sign_in",
      tone: "amber",
      label: "credential",
      needsRepair: true,
      launchBlocked,
      loginCommand,
      setupHint,
      signInHint: remoteCredentialHint,
      detail: remoteCredentialHint,
      authStatus,
      authError,
      checkedByProbe,
    };
  }

  if (!adapter.available) {
    return {
      kind: "setup",
      tone: "muted",
      label: "not configured",
      needsRepair: true,
      launchBlocked,
      loginCommand,
      setupHint,
      signInHint,
      detail: adapter.error || setupHint,
      authStatus,
      authError,
      checkedByProbe,
    };
  }

  if (isBillingStatus(authStatus, health)) {
    return {
      kind: "billing",
      tone: "amber",
      label: "billing",
      needsRepair: true,
      launchBlocked,
      loginCommand,
      setupHint,
      signInHint,
      detail: health?.hint || authError || visibleProbeError || "Check billing or subscription.",
      authStatus,
      authError,
      checkedByProbe,
    };
  }

  if (localAuthNeedsRepair) {
    return {
      kind: "sign_in",
      tone: "amber",
      label: "sign in",
      needsRepair: true,
      launchBlocked,
      loginCommand,
      setupHint,
      signInHint,
      detail: health?.hint || authError || signInHint,
      authStatus,
      authError,
      checkedByProbe,
    };
  }

  if (checkedByProbe) {
    return {
      kind: "ready",
      tone: "green",
      label: "checked",
      needsRepair: false,
      launchBlocked,
      loginCommand,
      setupHint,
      signInHint,
      authStatus,
      authError,
      checkedByProbe,
    };
  }

  if (health?.status === "not_installed" || isSetupProbe(health)) {
    return {
      kind: "setup",
      tone: "amber",
      label: "diagnostic",
      needsRepair: true,
      launchBlocked,
      loginCommand,
      setupHint,
      signInHint,
      detail: health?.hint || authError || setupHint,
      authStatus,
      authError,
      checkedByProbe,
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
      launchBlocked,
      loginCommand,
      setupHint,
      signInHint,
      detail: health?.hint || authError || visibleProbeError || setupHint,
      authStatus,
      authError,
      checkedByProbe,
    };
  }

  return {
    kind: "unverified",
    tone: "muted",
    label: "available",
    needsRepair: false,
    launchBlocked,
    loginCommand,
    setupHint,
    signInHint,
    detail:
      "New chat re-resolves the executable and prepares a fresh ACP session. The first message verifies any deferred vendor launch and authentication. Diagnostics are optional.",
    authStatus,
    authError,
    checkedByProbe,
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
      return "Run codex login in Terminal, then retry the chat. Diagnostics in Connections are optional.";
    case "claude_code":
      return "Run claude /login in Terminal, or set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN for the adapter environment, then retry the chat.";
    case "cursor_agent":
      return "Run cursor-agent login, or set CURSOR_API_KEY for the adapter environment, then retry the chat.";
    case "grok_build":
      return "Run grok login, or set XAI_API_KEY for the adapter environment, then retry the chat.";
    default:
      return externalAgentSetupHint(adapter);
  }
}

export function externalAgentSetupHint(adapter: AgentAdapterRecord): string {
  if (adapter.id === "codex") {
    return "Install Codex separately, then run codex login. Hecate checks official CLI locations, supported macOS app bundles, and PATH; the ACP adapter is built in.";
  }
  if (adapter.id === "claude_code") {
    return "Install Claude Code separately, then run claude /login. Hecate checks standard install locations and PATH; the ACP adapter is built in.";
  }
  if (adapter.id === "cursor_agent") {
    return "Install Cursor's command-line agent separately, then run cursor-agent login. Hecate checks its standard Unix location and PATH. The current Windows .cmd-only launcher is not yet supported.";
  }
  if (adapter.id === "grok_build") {
    return "Install the Grok CLI separately, then run grok login. Hecate checks standard CLI locations, the macOS Grok Build app, and PATH. Grok Build also needs a model selected before send.";
  }
  if (adapter.docs_url) {
    return `Install ${adapter.name} separately. Hecate checks standard install locations and PATH. Setup docs: ${adapter.docs_url}`;
  }
  return `Install ${adapter.name} separately and make ${adapter.command} available on PATH.`;
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

import type { AgentPresetRecord } from "../../types/agent-preset";
import type { ChatAgentPresetSnapshotRecord } from "../../types/chat";
import type { BrowserEvidenceRuntimeReadiness } from "../../types/provider";
import { browserReadinessLabel } from "../../lib/browser-readiness";

type BrowserPolicyPreset = Pick<
  AgentPresetRecord | ChatAgentPresetSnapshotRecord,
  "browser_allowed" | "browser_interactions_allowed" | "browser_allowed_origins" | "approval_policy"
>;

export function ChatBrowserPolicySummary({
  preset,
  frozen = false,
  id,
  live = false,
  readiness,
  compact = false,
}: {
  preset?: BrowserPolicyPreset;
  frozen?: boolean;
  id?: string;
  live?: boolean;
  readiness?: BrowserEvidenceRuntimeReadiness;
  compact?: boolean;
}) {
  const evidenceAllowed = preset?.browser_allowed === true;
  const interactionsAllowed = preset?.browser_interactions_allowed === true;
  const anyBrowserAllowed = evidenceAllowed || interactionsAllowed;
  const origins = anyBrowserAllowed
    ? Array.from(
        new Set(
          (preset?.browser_allowed_origins ?? []).map((origin) => origin.trim()).filter(Boolean),
        ),
      )
    : [];

  function grantLabel(granted: boolean, savedValue: boolean | undefined): string {
    if (!preset) return "Disabled (no Work policy)";
    if (frozen && savedValue === undefined) return "Disabled (legacy snapshot)";
    if (!granted) return "Disabled";
    if (preset.approval_policy === "block") {
      return "Configured · blocked by approval policy";
    }
    return compact
      ? "Configured · per-call approval"
      : "Configured · approval required for each call";
  }

  if (!preset && compact) {
    return (
      <div
        id={id}
        role="group"
        aria-label="Browser permissions"
        aria-live={live ? "polite" : undefined}
        style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.45 }}
      >
        Browser permissions are disabled without a Work policy. Tools alone does not grant browser
        access.
      </div>
    );
  }

  return (
    <div
      id={id}
      role="group"
      aria-label="Browser permissions"
      aria-live={live ? "polite" : undefined}
      style={{
        border: "1px solid var(--border)",
        borderRadius: 10,
        background: "var(--bg0)",
        padding: 10,
        display: "grid",
        gap: 7,
        color: "var(--t2)",
        fontSize: 11,
        lineHeight: 1.45,
      }}
    >
      <dl style={{ margin: 0, display: "grid", gap: 5 }}>
        <BrowserPolicyField
          label="Browser evidence"
          value={grantLabel(evidenceAllowed, preset?.browser_allowed)}
          compact={compact}
        />
        <BrowserPolicyField
          label="Browser interaction"
          value={grantLabel(interactionsAllowed, preset?.browser_interactions_allowed)}
          compact={compact}
        />
        <div
          style={{
            display: "grid",
            gridTemplateColumns: compact ? "1fr" : "minmax(104px, auto) 1fr",
            gap: compact ? 2 : 8,
          }}
        >
          <dt style={{ color: "var(--t3)" }}>Allowed origins</dt>
          <dd style={{ margin: 0, minWidth: 0 }}>
            {origins.length > 0 ? (
              <ul aria-label="Allowed browser origins" style={{ margin: 0, paddingLeft: 16 }}>
                {origins.map((origin) => (
                  <li key={origin}>
                    <code style={{ wordBreak: "break-all" }}>{origin}</code>
                  </li>
                ))}
              </ul>
            ) : anyBrowserAllowed ? (
              "None · browser calls remain blocked"
            ) : (
              "None"
            )}
          </dd>
        </div>
      </dl>
      {anyBrowserAllowed && (
        <div style={{ color: readiness?.available ? "var(--teal)" : "var(--t3)" }}>
          <strong>Browser runtime:</strong>{" "}
          {readiness
            ? `${browserReadinessLabel(readiness)} · ${readiness.message}${readiness.operator_action ? ` ${readiness.operator_action}` : ""}`
            : compact
              ? "Not checked · configure a local browser."
              : "Not checked · configure a local browser before using these grants."}
        </div>
      )}
      <div style={{ color: "var(--t3)" }}>
        {compact
          ? "Local runtime only · browser readiness is not implied · Tools alone does not grant access."
          : "Tools alone does not grant browser access. Grants apply only in this local runtime. The policy configuration by itself does not confirm that a local browser is ready."}
      </div>
    </div>
  );
}

function BrowserPolicyField({
  label,
  value,
  compact,
}: {
  label: string;
  value: string;
  compact: boolean;
}) {
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: compact ? "1fr" : "minmax(104px, auto) 1fr",
        gap: compact ? 2 : 8,
      }}
    >
      <dt style={{ color: "var(--t3)" }}>{label}</dt>
      <dd style={{ margin: 0 }}>{value}</dd>
    </div>
  );
}

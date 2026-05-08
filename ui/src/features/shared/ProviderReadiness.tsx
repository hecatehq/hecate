import type { ProviderReadinessCheckRecord } from "../../types/runtime";

export function ProviderReadinessChecklist({ checks }: { checks: ProviderReadinessCheckRecord[] }) {
  if (checks.length === 0) return null;

  return (
    <div style={{ border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", overflow: "hidden" }}>
      <div style={{
        padding: "7px 10px",
        borderBottom: "1px solid var(--border)",
        background: "var(--bg2)",
        fontSize: 10,
        color: "var(--t3)",
        fontFamily: "var(--font-mono)",
        textTransform: "uppercase",
        letterSpacing: "0.04em",
      }}>
        Readiness
      </div>
      <div style={{ display: "flex", flexDirection: "column" }}>
        {checks.map((check, index) => {
          const recommendation = readinessRecommendation(check);
          return (
            <div
              key={check.name}
              style={{
                display: "grid",
                gridTemplateColumns: "120px minmax(0, 1fr)",
                gap: 10,
                padding: "8px 10px",
                borderBottom: index === checks.length - 1 ? undefined : "1px solid var(--border)",
                alignItems: "start",
              }}>
              <div style={{ display: "flex", alignItems: "center", gap: 7, minWidth: 0 }}>
                <span style={{
                  width: 7,
                  height: 7,
                  borderRadius: 999,
                  background: readinessColor(check.status),
                  boxShadow: check.status === "ok" ? "0 0 10px rgba(96, 199, 112, 0.35)" : undefined,
                  flexShrink: 0,
                }} />
                <span style={{
                  fontSize: 11,
                  color: "var(--t1)",
                  fontFamily: "var(--font-mono)",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}>
                  {titleizeReadinessName(check.name)}
                </span>
              </div>
              <div style={{ minWidth: 0 }}>
                <div style={{
                  fontSize: 11,
                  color: readinessTextColor(check.status),
                  lineHeight: 1.45,
                }}>
                  {check.message || describeReadinessStatus(check.status)}
                </div>
                {check.reason && (
                  <div style={{
                    marginTop: 2,
                    fontSize: 10,
                    color: "var(--t3)",
                    fontFamily: "var(--font-mono)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}>
                    {check.reason}
                  </div>
                )}
                {recommendation && (
                  <div style={{ marginTop: 4, fontSize: 10, color: "var(--t2)", lineHeight: 1.45 }}>
                    Next: {recommendation}
                  </div>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

export function CompactProviderReadinessChecks({ checks }: { checks: ProviderReadinessCheckRecord[] }) {
  if (checks.length === 0) return null;

  return (
    <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))", gap: 8, marginTop: 10 }}>
      {checks.map(check => {
        const recommendation = readinessRecommendation(check);
        return (
          <div
            key={check.name}
            title={[check.message, recommendation ? `Next: ${recommendation}` : ""].filter(Boolean).join("\n")}
            style={{
              border: "1px solid var(--border)",
              borderRadius: "var(--radius-sm)",
              background: "var(--bg3)",
              padding: "7px 8px",
              minWidth: 0,
            }}>
            <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
              <span style={{
                width: 7,
                height: 7,
                borderRadius: 999,
                background: readinessColor(check.status),
                flexShrink: 0,
              }} />
              <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)", textTransform: "uppercase", letterSpacing: "0.04em" }}>
                {titleizeReadinessName(check.name)}
              </span>
            </div>
            <div style={{ marginTop: 4, fontSize: 11, color: readinessTextColor(check.status), overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {check.message || check.reason || check.status}
            </div>
            {recommendation && (
              <div style={{ marginTop: 3, fontSize: 10, color: "var(--t3)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                Next: {recommendation}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

export function readinessRecommendation(check: ProviderReadinessCheckRecord): string {
  if (check.operator_action) return check.operator_action;

  switch (check.reason) {
    case "credential_missing":
      return "add or rotate this provider's API key.";
    case "provider_disabled":
      return check.name === "models"
        ? "enable the provider before model discovery can run."
        : "enable the provider when you want Hecate to route to it.";
    case "self_referential":
      return "change the base URL so it points at the provider, not Hecate.";
    case "discovery_failed":
      return "check the endpoint and refresh provider status after the server is reachable.";
    case "default_model_only":
      return "send a test request or refresh discovery to confirm the default model is real.";
    case "no_models":
      return "start the provider and pull/load at least one model.";
    case "provider_slow":
      return "keep it enabled if acceptable, or route to a faster provider.";
    case "provider_rate_limited":
      return "wait for cooldown or temporarily route to another provider.";
    case "provider_unhealthy":
      return "inspect the latest health error and provider server logs.";
    case "circuit_open":
      return "wait for recovery or test the provider after fixing the upstream issue.";
    case "recovery_probe":
      return "retry once the half-open probe succeeds.";
    default:
      return "";
  }
}

function readinessColor(status?: string): string {
  switch (status) {
    case "ok":
      return "var(--green)";
    case "warning":
      return "var(--amber)";
    case "blocked":
      return "var(--red)";
    default:
      return "var(--t3)";
  }
}

function readinessTextColor(status?: string): string {
  switch (status) {
    case "blocked":
      return "var(--red)";
    case "warning":
      return "var(--amber)";
    default:
      return "var(--t2)";
  }
}

function describeReadinessStatus(status?: string): string {
  switch (status) {
    case "ok":
      return "Ready";
    case "warning":
      return "Needs attention";
    case "blocked":
      return "Blocked";
    default:
      return "Unknown";
  }
}

function titleizeReadinessName(value: string): string {
  return value
    .split("_")
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

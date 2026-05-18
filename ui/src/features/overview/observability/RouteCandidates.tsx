// RouteCandidates renders the per-trace router decision strip:
// who got selected, who got skipped, and the policy / failover
// detail for each candidate. Pulls from `traceDetail` first
// (richer) then falls back to `selectedTrace` (the row-summary
// shape from the recent-traces feed).

import {
  describeHealthStatus,
  describeRouteCandidateOutcome,
  describeRouteReason,
  describeRouteSkipReason,
  explainRouteCandidate,
  healthStatusTone,
  routeOutcomeTone,
} from "../../../lib/runtime-utils";
import type { TraceListItem, TraceResponse } from "../../../types/trace";
import { Badge } from "../../shared/ui";

type RouteCandidatesProps = {
  traceDetail: TraceResponse["data"] | null;
  selectedTrace?: TraceListItem;
};

export function RouteCandidates({ traceDetail, selectedTrace }: RouteCandidatesProps) {
  type Candidate = NonNullable<NonNullable<TraceListItem["route"]>["candidates"]>[number];
  const candidates: Candidate[] =
    traceDetail?.route?.candidates ?? selectedTrace?.route?.candidates ?? [];
  if (candidates.length === 0) return null;
  const selected = candidates.find((c) => c.outcome === "selected" || c.outcome === "completed");
  const skipped = candidates.filter((c) => c.outcome === "skipped").length;
  const failed = candidates.filter((c) => c.outcome === "failed").length;
  return (
    <div
      style={{
        padding: "10px 12px",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
      }}
    >
      <div className="kicker" style={{ marginBottom: 6 }}>
        Route decision
      </div>
      <div
        style={{ marginBottom: 8, display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}
      >
        <span style={{ fontSize: 12, color: "var(--t1)" }}>
          {selected?.provider
            ? `Selected ${selected.provider}/${selected.model || "provider default"}`
            : "No provider selected"}
        </span>
        {skipped > 0 && <Badge status="warn" label={`${skipped} skipped`} />}
        {failed > 0 && <Badge status="error" label={`${failed} failed`} />}
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
          {candidates.length} candidate{candidates.length === 1 ? "" : "s"}
        </span>
      </div>
      {candidates.map((c, i) => {
        const outcomeTone = routeOutcomeTone(c.outcome);
        const outcomeStatus =
          outcomeTone === "healthy"
            ? "done"
            : outcomeTone === "danger"
              ? "error"
              : outcomeTone === "warning"
                ? "warn"
                : "disabled";
        const healthTone = healthStatusTone(c.health_status);
        const healthStatus =
          healthTone === "healthy"
            ? "healthy"
            : healthTone === "danger"
              ? "error"
              : healthTone === "warning"
                ? "warn"
                : "disabled";
        return (
          <div
            key={`${c.provider || "provider"}-${c.model || "model"}-${c.outcome || "candidate"}-${c.index ?? i}`}
            style={{
              padding: "8px 0",
              borderBottom: i === candidates.length - 1 ? undefined : "1px solid var(--border)",
            }}
          >
            <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
              <span
                style={{
                  fontFamily: "var(--font-mono)",
                  fontSize: 11,
                  color: "var(--t0)",
                  flex: 1,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {c.provider}/{c.model || "no model"}
              </span>
              <Badge status={outcomeStatus} label={describeRouteCandidateOutcome(c)} />
            </div>
            <div style={{ marginBottom: 6, fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
              {explainRouteCandidate(c)}
            </div>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
              {c.skip_reason && (
                <Badge status="warn" label={describeRouteSkipReason(c.skip_reason)} />
              )}
              {c.health_status && (
                <Badge status={healthStatus} label={describeHealthStatus(c.health_status)} />
              )}
              {c.reason && (
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                  reason <span style={{ color: "var(--t1)" }}>{describeRouteReason(c.reason)}</span>
                </span>
              )}
              {c.latency_ms != null && c.latency_ms > 0 && (
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                  latency <span style={{ color: "var(--t1)" }}>{c.latency_ms}ms</span>
                </span>
              )}
              {c.estimated_usd && (
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}>
                  est <span style={{ color: "var(--t1)" }}>{c.estimated_usd}</span>
                </span>
              )}
            </div>
            {c.detail && (
              <div style={{ marginTop: 6, fontSize: 11, color: "var(--t2)", lineHeight: 1.45 }}>
                {c.detail}
              </div>
            )}
            {(c.policy_rule_id || c.policy_action || c.policy_reason) && (
              <div style={{ marginTop: 6, display: "flex", flexWrap: "wrap", gap: 10 }}>
                {c.policy_rule_id && (
                  <span
                    style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}
                  >
                    rule <span style={{ color: "var(--t1)" }}>{c.policy_rule_id}</span>
                  </span>
                )}
                {c.policy_action && (
                  <span
                    style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}
                  >
                    action <span style={{ color: "var(--t1)" }}>{c.policy_action}</span>
                  </span>
                )}
                {c.policy_reason && (
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 10,
                      color: "var(--t2)",
                      fontStyle: "italic",
                    }}
                  >
                    {c.policy_reason}
                  </span>
                )}
              </div>
            )}
            {(c.failover_from || c.failover_to || c.attempt || c.retry_count) && (
              <div style={{ marginTop: 6, display: "flex", flexWrap: "wrap", gap: 10 }}>
                {c.attempt != null && (
                  <span
                    style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}
                  >
                    attempt <span style={{ color: "var(--t1)" }}>{c.attempt}</span>
                  </span>
                )}
                {c.retry_count != null && (
                  <span
                    style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}
                  >
                    retries <span style={{ color: "var(--t1)" }}>{c.retry_count}</span>
                  </span>
                )}
                {c.failover_from && c.failover_to && (
                  <span
                    style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)" }}
                  >
                    failover <span style={{ color: "var(--t1)" }}>{c.failover_from}</span> →{" "}
                    <span style={{ color: "var(--t1)" }}>{c.failover_to}</span>
                  </span>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

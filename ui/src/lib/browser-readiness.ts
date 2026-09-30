import type { BrowserEvidenceRuntimeReadiness } from "../types/provider";

export function browserReadinessLabel(readiness?: BrowserEvidenceRuntimeReadiness): string {
  if (!readiness) return "Not loaded";
  if (readiness.available) {
    return readiness.status === "working" ? "Working" : "Configured (unverified)";
  }
  if (readiness.status === "local_only") return "Local only";
  if (readiness.status === "not_configured") return "Not configured";
  return "Unavailable";
}

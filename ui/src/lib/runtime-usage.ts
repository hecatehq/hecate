// Usage helpers shared by the Usage view and related tests.

import type { UsageSummaryRecord } from "../types/usage";

export function describeUsageScope(usage?: UsageSummaryRecord | null): string {
  if (!usage) {
    return "No scope";
  }

  const parts = [usage.scope];
  if (usage.provider) {
    parts.push(`provider ${usage.provider}`);
  }
  return parts.join(" / ");
}

// Budget: percent-consumed and scope-formatting helpers for the
// budgets view. Triggered-warning tone is centralized here so the
// observability and budgets surfaces stay in sync.

import type { BudgetRecord } from "../types/runtime";

export function budgetConsumedPercent(budget?: BudgetRecord | null): number {
  if (!budget || budget.credited_micros_usd <= 0) {
    return 0;
  }
  return Math.max(0, Math.min(100, Math.round((budget.debited_micros_usd / budget.credited_micros_usd) * 100)));
}

export function describeBudgetScope(budget?: BudgetRecord | null): string {
  if (!budget) {
    return "No scope";
  }

  const parts = [budget.scope];
  if (budget.provider) {
    parts.push(`provider ${budget.provider}`);
  }
  return parts.join(" / ");
}

export function budgetWarningTone(triggered: boolean): "healthy" | "warning" | "neutral" {
  return triggered ? "warning" : "neutral";
}

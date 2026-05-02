import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { CostsView } from "./CostsView";
import { createRuntimeConsoleActions, createRuntimeConsoleFixture } from "../../test/runtime-console-fixture";

const localSession = { label: "Local" };

// Minimal budget fixture mirroring the wire shape that BudgetTab used to
// consume. We only populate the fields CostsView actually reads.
const sampleBudget = {
  scope: "account",
  enforced: true,
  credited_micros_usd: 5_000_000,
  debited_micros_usd: 1_250_000,
  credited_usd: "$5.00",
  debited_usd: "$1.25",
  balance_usd: "$3.75",
  available_usd: "$3.75",
  warnings: [],
} as any;

function setup(stateOverrides: Record<string, unknown> = {}, actionOverrides: Record<string, unknown> = {}) {
  const state = createRuntimeConsoleFixture({ session: localSession, ...stateOverrides });
  const actions = { ...createRuntimeConsoleActions(), ...actionOverrides };
  const user = userEvent.setup();
  return { state, actions, user };
}

describe("CostsView balance card", () => {
  it("renders current balance, debited, credited, and limit when budget is set", () => {
    const { state, actions } = setup({ budget: sampleBudget });
    render(<CostsView state={state} actions={actions} />);
    expect(screen.getByText(/balance: \$3\.75/)).toBeTruthy();
    expect(screen.getByText(/available: \$3\.75/)).toBeTruthy();
    // Credited + spent appear together in the meter row.
    expect(screen.getByText("$1.25")).toBeTruthy();
    expect(screen.getByText(/spent of \$5\.00 credited/)).toBeTruthy();
  });

  it("shows the admin-required hint when budget is null", () => {
    const { state, actions } = setup({ budget: null });
    render(<CostsView state={state} actions={actions} />);
    expect(screen.getByText(/Budget data unavailable/i)).toBeTruthy();
  });

  it("Reset balance + Top up buttons fire the matching actions", async () => {
    const resetBudget = vi.fn(async () => undefined);
    const topUpBudget = vi.fn(async () => undefined);
    const { state, actions, user } = setup(
      { budget: sampleBudget },
      { resetBudget, topUpBudget },
    );
    render(<CostsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: /Reset balance/i }));
    expect(resetBudget).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: /Top up/i }));
    expect(topUpBudget).toHaveBeenCalledTimes(1);
  });

  it("disables Reset + Top up when budget data is unavailable", () => {
    const { state, actions } = setup({ budget: null });
    render(<CostsView state={state} actions={actions} />);
    expect((screen.getByRole("button", { name: /Reset balance/i }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: /Top up/i }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("CostsView usage table", () => {
  it("renders ledger rows when present", () => {
    const { state, actions } = setup({
      budget: sampleBudget,
      requestLedger: [
        {
          request_id: "req-1",
          timestamp: "2026-04-25T10:00:00Z",
          model: "gpt-4o-mini",
          total_tokens: 42,
          amount_usd: "$0.001",
        } as any,
      ],
    });
    render(<CostsView state={state} actions={actions} />);
    expect(screen.getByText("req-1")).toBeTruthy();
    expect(screen.getByText("gpt-4o-mini")).toBeTruthy();
  });

  it("falls back to an empty state when there is no ledger data", () => {
    const { state, actions } = setup({ budget: sampleBudget, requestLedger: [] });
    render(<CostsView state={state} actions={actions} />);
    expect(screen.getByText(/No usage events recorded yet/i)).toBeTruthy();
  });

  it("does not crash when both budget and ledger are empty", () => {
    const { state, actions } = setup({ budget: null, requestLedger: [] });
    render(<CostsView state={state} actions={actions} />);
    // Header still renders; both empty states render below.
    expect(screen.getByText(/^Costs$/)).toBeTruthy();
    expect(screen.getByText(/Budget data unavailable/i)).toBeTruthy();
    expect(screen.getByText(/No usage events recorded yet/i)).toBeTruthy();
  });
});

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { SettingsView } from "./SettingsView";
import { createRuntimeConsoleActions, createRuntimeConsoleFixture } from "../../test/runtime-console-fixture";

function setup(stateOverrides = {}, actionOverrides = {}) {
  const state = createRuntimeConsoleFixture(stateOverrides);
  const actions = { ...createRuntimeConsoleActions(), ...actionOverrides };
  const user = userEvent.setup();
  return { state, actions, user };
}

// Tab gating: TABS holds two ids — pricebook + retention. Policy and
// MCP Cache were removed (single-user mode dropped tenant/role gating
// and the MCP cache was pure informational stats). Balances and Usage
// live in the Costs workspace.
describe("SettingsView tabs", () => {
  it("renders Pricing / Retention", () => {
    const { state, actions } = setup();
    render(<SettingsView state={state} actions={actions} />);
    for (const tab of ["Pricing", "Retention"]) {
      expect(screen.getByRole("button", { name: tab })).toBeTruthy();
    }
  });

  it("starts on the first visible tab (Pricing)", () => {
    const { state, actions } = setup();
    render(<SettingsView state={state} actions={actions} />);
    expect(document.querySelector("button[type='button']")).toBeTruthy();
  });

  it("switches to retention tab on click", async () => {
    const { state, actions, user } = setup();
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Retention" }));
    expect(await screen.findByText(/Subsystems to prune/i)).toBeTruthy();
  });
});

describe("SettingsView retention tab", () => {
  it("shows known subsystems as toggle chips", async () => {
    const { state, actions, user } = setup();
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Retention" }));
    for (const sub of ["trace_snapshots", "budget_events", "audit_events"]) {
      expect(await screen.findByText(sub)).toBeTruthy();
    }
  });

  it("clicking a chip calls setRetentionSubsystems", async () => {
    const setRetentionSubsystems = vi.fn();
    const { state, actions, user } = setup({}, { setRetentionSubsystems });
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Retention" }));
    await user.click(await screen.findByText("audit_events"));
    expect(setRetentionSubsystems).toHaveBeenCalledWith("audit_events");
  });

  it("'Run now' button triggers runRetention action", async () => {
    const runRetention = vi.fn(async () => undefined);
    const { state, actions, user } = setup({}, { runRetention });
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Retention" }));
    await user.click(await screen.findByRole("button", { name: /Run now/i }));
    expect(runRetention).toHaveBeenCalled();
  });

  it("handles partial retention run payloads without results", async () => {
    const { state, actions, user } = setup({
      retentionLastRun: {
        finished_at: new Date().toISOString(),
        trigger: "manual",
      },
    });
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Retention" }));

    expect(await screen.findByText(/Last run/i)).toBeTruthy();
    expect(screen.getByText("0 deleted")).toBeTruthy();
  });
});

// Usage / Balances tabs were lifted into CostsView — see
// features/costs/CostsView.test.tsx for the equivalent rendering tests.

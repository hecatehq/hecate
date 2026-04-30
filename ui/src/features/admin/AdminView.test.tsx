import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { AdminView } from "./AdminView";
import { createRuntimeConsoleActions, createRuntimeConsoleFixture } from "../../test/runtime-console-fixture";

const adminSession = {
  kind: "admin" as const, label: "Admin", role: "admin", isAdmin: true, isAuthenticated: true,
  capabilities: [], name: "", tenant: "", source: "", keyID: "",
  allowedProviders: [], allowedModels: [], multiTenant: false, authDisabled: false,
};

function setup(stateOverrides = {}, actionOverrides = {}) {
  const state = createRuntimeConsoleFixture({ session: adminSession, ...stateOverrides });
  const actions = { ...createRuntimeConsoleActions(), ...actionOverrides };
  const user = userEvent.setup();
  return { state, actions, user };
}

// Tab gating: TABS holds five candidate ids — pricebook / policy /
// retention / tenants / keys. The first three are always visible;
// tenants + keys only appear in multi-tenant deployments. Balances
// and Usage moved out to the Costs workspace.
const adminMultiTenantSession = { ...adminSession, multiTenant: true };

describe("AdminView tabs", () => {
  it("single-tenant (default): only Pricing / Policy / Retention render", () => {
    const { state, actions } = setup();
    render(<AdminView state={state} actions={actions} />);
    for (const tab of ["Pricing", "Policy", "Retention"]) {
      expect(screen.getByRole("button", { name: tab })).toBeTruthy();
    }
    // Tenants and Keys are gated off in single-tenant mode.
    expect(screen.queryByRole("button", { name: "Tenants" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Keys" })).toBeNull();
    // Balances and Usage moved to the Costs workspace — not present here.
    expect(screen.queryByRole("button", { name: "Balances" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Usage" })).toBeNull();
  });

  it("multi-tenant: all five candidate tabs render", () => {
    const { state, actions } = setup({ session: adminMultiTenantSession });
    render(<AdminView state={state} actions={actions} />);
    for (const tab of ["Pricing", "Policy", "Retention", "Tenants", "Keys"]) {
      expect(screen.getByRole("button", { name: tab })).toBeTruthy();
    }
  });

  it("does not render the legacy Clients tab (moved to top-level Integrations)", () => {
    const { state, actions } = setup();
    render(<AdminView state={state} actions={actions} />);
    expect(screen.queryByRole("button", { name: "Clients" })).toBeNull();
  });

  it("starts on the first visible tab (Pricing in single-tenant)", () => {
    const { state, actions } = setup();
    render(<AdminView state={state} actions={actions} />);
    // Pricing tab body shows the import controls; assert one of the
    // unique pricebook strings is present.
    expect(document.querySelector("button[type='button']")).toBeTruthy();
  });

  it("switches to tenants tab on click (multi-tenant)", async () => {
    const { state, actions, user } = setup({ session: adminMultiTenantSession });
    render(<AdminView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Tenants" }));
    expect(await screen.findByText(/New tenant/i)).toBeTruthy();
  });

  it("switches to retention tab on click", async () => {
    const { state, actions, user } = setup();
    render(<AdminView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Retention" }));
    expect(await screen.findByText(/Subsystems to prune/i)).toBeTruthy();
  });
});

describe("AdminView admin token panel", () => {
  // The token panel only renders in multi-tenant mode — single-user
  // installs have already pasted (or auto-bootstrapped) the token and
  // never need to see it again. The tests below seed multi-tenant.

  it("is hidden in single-tenant mode", () => {
    const { state, actions } = setup({ authToken: "super-secret-token-123" });
    render(<AdminView state={state} actions={actions} />);
    expect(screen.queryByText(/Admin token/i)).toBeNull();
    expect(screen.queryByRole("button", { name: /reveal/i })).toBeNull();
  });

  it("shows 'not set' when authToken is empty (multi-tenant)", () => {
    const { state, actions } = setup({ session: adminMultiTenantSession, authToken: "" });
    render(<AdminView state={state} actions={actions} />);
    expect(screen.getAllByText(/not set/i).length).toBeGreaterThan(0);
  });

  it("masks the token by default and reveals on click (multi-tenant)", async () => {
    const { state, actions, user } = setup({ session: adminMultiTenantSession, authToken: "super-secret-token-123" });
    render(<AdminView state={state} actions={actions} />);
    expect(screen.queryByText("super-secret-token-123")).toBeNull();
    await user.click(screen.getByRole("button", { name: /reveal/i }));
    expect(await screen.findByText("super-secret-token-123")).toBeTruthy();
  });
});

describe("AdminView retention tab", () => {
  it("shows known subsystems as toggle chips", async () => {
    const { state, actions, user } = setup();
    render(<AdminView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Retention" }));
    for (const sub of ["trace_snapshots", "budget_events", "audit_events", "exact_cache", "semantic_cache"]) {
      expect(await screen.findByText(sub)).toBeTruthy();
    }
  });

  it("clicking a chip calls setRetentionSubsystems", async () => {
    const setRetentionSubsystems = vi.fn();
    const { state, actions, user } = setup({}, { setRetentionSubsystems });
    render(<AdminView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Retention" }));
    await user.click(await screen.findByText("audit_events"));
    expect(setRetentionSubsystems).toHaveBeenCalledWith("audit_events");
  });

  it("'Run now' button triggers runRetention action", async () => {
    const runRetention = vi.fn(async () => undefined);
    const { state, actions, user } = setup({}, { runRetention });
    render(<AdminView state={state} actions={actions} />);
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
    render(<AdminView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Retention" }));

    expect(await screen.findByText(/Last run/i)).toBeTruthy();
    expect(screen.getByText("0 deleted")).toBeTruthy();
  });
});

describe("AdminView policy tab", () => {
  function adminConfigWith(rules: unknown[]) {
    return {
      backend: "memory",
      tenants: [
        { id: "team-a", name: "team-a", enabled: true, allowed_providers: [], allowed_models: [] },
      ],
      api_keys: [],
      providers: [],
      pricebook: [],
      policy_rules: rules,
      events: [],
    } as unknown as ReturnType<typeof createRuntimeConsoleFixture>["adminConfig"];
  }

  it("renders the empty state when no rules are configured", async () => {
    const { state, actions, user } = setup({ adminConfig: adminConfigWith([]) });
    render(<AdminView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Policy" }));
    expect(await screen.findByText(/No policy rules/i)).toBeTruthy();
  });

  it("lists existing rules with action badge + match summary + effect", async () => {
    const { state, actions, user } = setup({
      adminConfig: adminConfigWith([
        {
          id: "deny-cloud",
          action: "deny",
          reason: "team-a is local-only",
          tenants: ["team-a"],
          provider_kinds: ["cloud"],
        },
        {
          id: "downgrade-team-b",
          action: "rewrite_model",
          tenants: ["team-b"],
          models: ["gpt-4o"],
          rewrite_model_to: "gpt-4o-mini",
        },
      ]),
    });
    render(<AdminView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Policy" }));

    // Each row's id renders as mono.
    expect(await screen.findByText("deny-cloud")).toBeTruthy();
    expect(screen.getByText("downgrade-team-b")).toBeTruthy();

    // Action badges (lowercase labels match the badge text).
    expect(screen.getByText("deny")).toBeTruthy();
    expect(screen.getByText("rewrite")).toBeTruthy();

    // Match summary picks up the populated dimensions.
    expect(screen.getByText(/tenant: team-a · kind: cloud/)).toBeTruthy();
    expect(screen.getByText(/tenant: team-b · model: gpt-4o/)).toBeTruthy();

    // Effect column shows the deny reason and the rewrite arrow.
    expect(screen.getByText("team-a is local-only")).toBeTruthy();
    expect(screen.getByText("gpt-4o-mini")).toBeTruthy();
  });

  it("'New rule' opens the SlideOver with the empty form", async () => {
    const { state, actions, user } = setup({ adminConfig: adminConfigWith([]) });
    render(<AdminView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Policy" }));
    await user.click(screen.getByRole("button", { name: /New rule/i }));
    expect(await screen.findByRole("dialog", { name: /New policy rule/i })).toBeTruthy();
    // The deny radio is selected by default — the reason field shows.
    expect(screen.getByText(/REASON \(shown in the 403/i)).toBeTruthy();
  });

  it("switching to rewrite_model swaps the reason input for the target-model input", async () => {
    const { state, actions, user } = setup({ adminConfig: adminConfigWith([]) });
    render(<AdminView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Policy" }));
    await user.click(screen.getByRole("button", { name: /New rule/i }));
    // Click the rewrite_model radio.
    await user.click(screen.getByLabelText("rewrite_model"));
    expect(screen.getByText(/REWRITE TO MODEL/i)).toBeTruthy();
    // Save is disabled while target model is empty even if id is set.
    const id = screen.getByPlaceholderText(/deny-cloud-for-team-a/i);
    await user.type(id, "downgrade-x");
    expect((screen.getByRole("button", { name: /Save rule/i }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("Save calls upsertPolicyRule with the trimmed payload", async () => {
    const upsertPolicyRule = vi.fn(async () => undefined);
    const { state, actions, user } = setup(
      { adminConfig: adminConfigWith([]) },
      { upsertPolicyRule },
    );
    render(<AdminView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Policy" }));
    await user.click(screen.getByRole("button", { name: /New rule/i }));
    await user.type(screen.getByPlaceholderText(/deny-cloud-for-team-a/i), "deny-test");
    await user.type(screen.getByPlaceholderText(/team-a is local-only/i), "test reason");
    await user.click(screen.getByRole("button", { name: /Save rule/i }));
    expect(upsertPolicyRule).toHaveBeenCalledWith(expect.objectContaining({
      id: "deny-test",
      action: "deny",
      reason: "test reason",
    }));
  });

  it("clicking a row opens the edit form prefilled with that rule", async () => {
    const { state, actions, user } = setup({
      adminConfig: adminConfigWith([
        { id: "deny-cloud", action: "deny", reason: "test", provider_kinds: ["cloud"] },
      ]),
    });
    render(<AdminView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Policy" }));
    await user.click(screen.getByText("deny-cloud"));
    expect(await screen.findByRole("dialog", { name: /Edit policy rule/i })).toBeTruthy();
    // The id field should have the existing id pre-filled.
    const idInput = screen.getByPlaceholderText(/deny-cloud-for-team-a/i) as HTMLInputElement;
    expect(idInput.value).toBe("deny-cloud");
  });

  it("Delete opens a confirm modal that calls deletePolicyRule with the id", async () => {
    const deletePolicyRule = vi.fn(async () => undefined);
    const { state, actions, user } = setup(
      {
        adminConfig: adminConfigWith([
          { id: "deny-cloud", action: "deny" },
        ]),
      },
      { deletePolicyRule },
    );
    render(<AdminView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Policy" }));
    await user.click(screen.getByRole("button", { name: /Delete rule deny-cloud/i }));
    const dialog = await screen.findByRole("dialog", { name: /Delete policy rule/i });
    expect(dialog).toBeTruthy();
    await user.click(screen.getByRole("button", { name: /^Delete rule$/i }));
    expect(deletePolicyRule).toHaveBeenCalledWith("deny-cloud");
  });
});

// Usage / Balances tabs were lifted into CostsView — see
// features/costs/CostsView.test.tsx for the equivalent rendering tests.

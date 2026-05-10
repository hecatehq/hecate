import { render, screen, within } from "@testing-library/react";
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

// Tab gating: TABS holds three ids — pricebook + retention + external
// agents. Policy and MCP Cache were removed (single-user mode dropped
// tenant/role gating and the MCP cache was pure informational stats).
// Balances and Usage live in the Costs workspace.
describe("SettingsView tabs", () => {
  it("renders Pricing / Model capabilities / Retention / External agents", () => {
    const { state, actions } = setup();
    render(<SettingsView state={state} actions={actions} />);
    for (const tab of ["Pricing", "Model capabilities", "Retention", "External agents"]) {
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

describe("SettingsView model capabilities tab", () => {
  const modelState = {
    models: [
      {
        id: "qwen2.5-coder",
        owned_by: "ollama",
        metadata: {
          provider: "ollama",
          provider_kind: "local",
          capabilities: { tool_calling: "unknown", streaming: true, source: "provider" },
        },
      },
      {
        id: "gpt-4o-mini",
        owned_by: "openai",
        metadata: {
          provider: "openai",
          provider_kind: "cloud",
          capabilities: { tool_calling: "parallel", streaming: true, source: "catalog" },
        },
      },
    ],
  };

  it("renders capability rows with source and tool support", async () => {
    const { state, actions, user } = setup(modelState);
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Model capabilities" }));

    expect(await screen.findByTestId("model-capabilities-list")).toBeTruthy();
    expect(screen.getByText("qwen2.5-coder")).toBeTruthy();
    expect(screen.getAllByText("tools on").length).toBeGreaterThanOrEqual(2);
  });

  it("saves the tools switch for the selected provider/model", async () => {
    const upsertModelCapabilityOverride = vi.fn(async () => true);
    const { state, actions, user } = setup(modelState, { upsertModelCapabilityOverride });
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Model capabilities" }));
    const row = await screen.findByTestId("model-capability-row-ollama-qwen2.5-coder");

    await user.click(within(row).getByRole("button", { name: "tools on" }));

    expect(upsertModelCapabilityOverride).toHaveBeenCalledWith(expect.objectContaining({
      provider: "ollama",
      model: "qwen2.5-coder",
      tool_calling: "basic",
    }));
  });

  it("saves and clears operator overrides", async () => {
    const upsertModelCapabilityOverride = vi.fn(async () => true);
    const deleteModelCapabilityOverride = vi.fn(async () => true);
    const { state, actions, user } = setup(
      {
        models: [
          {
            id: "local-tools",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
            },
          },
        ],
      },
      { upsertModelCapabilityOverride, deleteModelCapabilityOverride },
    );
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "Model capabilities" }));
    const row = await screen.findByTestId("model-capability-row-ollama-local-tools");

    await user.click(within(row).getByRole("button", { name: "tools off" }));
    await user.click(within(row).getByRole("button", { name: "Clear override" }));

    expect(upsertModelCapabilityOverride).toHaveBeenCalledWith(expect.objectContaining({
      provider: "ollama",
      model: "local-tools",
      tool_calling: "none",
    }));
    expect(deleteModelCapabilityOverride).toHaveBeenCalledWith("ollama", "local-tools");
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

describe("SettingsView external agents tab", () => {
  it("fires listAgentChatGrants when the tab opens", async () => {
    const listAgentChatGrants = vi.fn(async () => undefined);
    const { state, actions, user } = setup({}, { listAgentChatGrants });
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "External agents" }));
    expect(listAgentChatGrants).toHaveBeenCalled();
  });

  it("renders the empty-state copy when there are no grants", async () => {
    const { state, actions, user } = setup();
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "External agents" }));
    expect(await screen.findByTestId("external-agents-empty")).toBeTruthy();
  });

  it("renders one row per grant with adapter / tool / decision metadata", async () => {
    const { state, actions, user } = setup({
      agentChatGrants: [
        {
          id: "g-1",
          scope: "session",
          adapter_id: "codex",
          tool_kind: "fs",
          decision: "approve",
          granted_by: "operator",
          granted_at: "2026-04-21T10:00:00Z",
        },
        {
          id: "g-2",
          scope: "adapter_tool",
          adapter_id: "claude-code",
          tool_kind: "exec",
          decision: "deny",
          granted_by: "operator",
          granted_at: "2026-04-21T10:01:00Z",
          expires_at: "2026-05-01T10:00:00Z",
        },
      ],
    });
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "External agents" }));
    expect(await screen.findByTestId("external-agents-list")).toBeTruthy();
    // Scope decision-tone assertions to row content so they don't
    // accidentally match the section description above.
    const approveRow = screen.getByTestId("external-agents-row-g-1");
    expect(within(approveRow).getByText(/always approve/i)).toBeTruthy();
    const denyRow = screen.getByTestId("external-agents-row-g-2");
    expect(within(denyRow).getByText(/always deny/i)).toBeTruthy();
  });

  it("revoke asks for inline confirmation before deleting the grant", async () => {
    const deleteAgentChatGrant = vi.fn(async () => true);
    const { state, actions, user } = setup(
      {
        agentChatGrants: [
          {
            id: "g-7",
            scope: "session",
            adapter_id: "codex",
            tool_kind: "fs",
            decision: "approve",
            granted_at: "2026-04-21T10:00:00Z",
          },
        ],
      },
      { deleteAgentChatGrant },
    );
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "External agents" }));
    await user.click(await screen.findByTestId("external-agents-revoke-g-7"));
    expect(deleteAgentChatGrant).not.toHaveBeenCalled();
    await user.click(await screen.findByTestId("external-agents-confirm-revoke-g-7"));
    expect(deleteAgentChatGrant).toHaveBeenCalledWith("g-7");
  });

  it("revoke confirmation can be cancelled inline", async () => {
    const deleteAgentChatGrant = vi.fn(async () => true);
    const { state, actions, user } = setup(
      {
        agentChatGrants: [
          {
            id: "g-8",
            scope: "session",
            adapter_id: "codex",
            tool_kind: "fs",
            decision: "approve",
            granted_at: "2026-04-21T10:00:00Z",
          },
        ],
      },
      { deleteAgentChatGrant },
    );
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "External agents" }));
    await user.click(await screen.findByTestId("external-agents-revoke-g-8"));
    expect(await screen.findByTestId("external-agents-confirm-revoke-g-8")).toBeTruthy();
    await user.click(await screen.findByTestId("external-agents-cancel-revoke-g-8"));
    expect(deleteAgentChatGrant).not.toHaveBeenCalled();
    expect(screen.queryByTestId("external-agents-confirm-revoke-g-8")).toBeNull();
  });

  it("surfaces the listing error inline when the load fails", async () => {
    const { state, actions, user } = setup({
      agentChatGrantsError: "list failed: 500",
    });
    render(<SettingsView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: "External agents" }));
    expect(await screen.findByText(/list failed: 500/)).toBeTruthy();
  });

  // Adapter status panel — surfaces the on-demand probe result. The
  // section is hidden when no adapters are registered (no point
  // showing an empty card); otherwise each row exposes a Test button
  // that calls actions.probeAgentAdapter, plus inline diagnostic
  // copy when a result exists.
  describe("adapter status panel", () => {
    function withAdapter(overrides: Record<string, unknown> = {}) {
      return {
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
        ...overrides,
      };
    }

    it("hides the panel when no adapters are registered", async () => {
      const { state, actions, user } = setup({ agentAdapters: [] });
      render(<SettingsView state={state} actions={actions} />);
      await user.click(screen.getByRole("button", { name: "External agents" }));
      expect(screen.queryByTestId("external-agents-adapters")).toBeNull();
    });

    it("renders one row per adapter with a Test button", async () => {
      const { state, actions, user } = setup(withAdapter());
      render(<SettingsView state={state} actions={actions} />);
      await user.click(screen.getByRole("button", { name: "External agents" }));
      expect(await screen.findByTestId("external-agents-adapters")).toBeTruthy();
      expect(screen.getByTestId("external-agents-adapter-codex")).toBeTruthy();
      expect(screen.getByTestId("external-agents-test-codex")).toBeTruthy();
    });

    it("calls probeAgentAdapter with the row id when Test is clicked", async () => {
      const probeAgentAdapter = vi.fn(async () => null);
      const { state, actions, user } = setup(withAdapter(), { probeAgentAdapter });
      render(<SettingsView state={state} actions={actions} />);
      await user.click(screen.getByRole("button", { name: "External agents" }));
      await user.click(await screen.findByTestId("external-agents-test-codex"));
      expect(probeAgentAdapter).toHaveBeenCalledWith("codex");
    });

    it("renders the auth-required hint when the cached probe says auth is missing", async () => {
      const { state, actions, user } = setup(withAdapter({
        agentAdapterHealthByID: new Map([
          ["codex", {
            adapter_id: "codex",
            status: "auth_required",
            stage: "initialize",
            path: "/usr/local/bin/codex-acp",
            error: "Authentication required",
            hint: "Run codex login",
            duration_ms: 412,
          }],
        ]),
      }));
      render(<SettingsView state={state} actions={actions} />);
      await user.click(screen.getByRole("button", { name: "External agents" }));
      const detail = await screen.findByTestId("external-agents-adapter-codex-detail");
      expect(within(detail).getByText("Run codex login")).toBeTruthy();
      expect(within(detail).getByText(/Authentication required/)).toBeTruthy();
    });

    it("renders discovery auth warnings before a full probe has run", async () => {
      const { state, actions, user } = setup(withAdapter({
        agentAdapters: [
          {
            id: "cursor_agent",
            name: "Cursor Agent",
            kind: "acp",
            command: "cursor-agent",
            available: true,
            status: "available",
            cost_mode: "external",
            auth_status: "unauthenticated",
            auth_error: "Run cursor-agent login",
          },
        ],
      }));
      render(<SettingsView state={state} actions={actions} />);
      await user.click(screen.getByRole("button", { name: "External agents" }));
      expect(await screen.findByTestId("external-agents-adapter-cursor_agent-auth-warning")).toHaveTextContent("auth required");
      expect(screen.getByTestId("external-agents-adapter-cursor_agent-auth-detail")).toHaveTextContent("Run cursor-agent login");
    });

    it("disables the Test button while a probe is in flight", async () => {
      const { state, actions, user } = setup(withAdapter({
        agentAdapterHealthLoadingByID: new Map([["codex", true]]),
      }));
      render(<SettingsView state={state} actions={actions} />);
      await user.click(screen.getByRole("button", { name: "External agents" }));
      const button = await screen.findByTestId("external-agents-test-codex") as HTMLButtonElement;
      expect(button.disabled).toBe(true);
      expect(button.textContent).toMatch(/Testing/);
    });

    it("shows Claude Code guided setup and saves the pasted token", async () => {
      const setAgentAdapterCredential = vi.fn(async () => true);
      const probeAgentAdapter = vi.fn(async () => null);
      const { state, actions, user } = setup(withAdapter({
        agentAdapters: [
          {
            id: "claude_code",
            name: "Claude Code",
            kind: "acp",
            command: "claude-agent-acp",
            available: true,
            status: "available",
            cost_mode: "external",
            auth_status: "unknown",
            auth_error: "Use Test adapter; if Claude Code reports auth errors, set CLAUDE_CODE_OAUTH_TOKEN.",
          },
        ],
      }), { setAgentAdapterCredential, probeAgentAdapter });
      render(<SettingsView state={state} actions={actions} />);
      await user.click(screen.getByRole("button", { name: "External agents" }));

      expect(await screen.findByTestId("claude-code-guided-setup")).toBeTruthy();
      await user.type(screen.getByLabelText("Claude Code OAuth token"), "claude-token");
      await user.click(screen.getByRole("button", { name: "Save + test" }));

      expect(setAgentAdapterCredential).toHaveBeenCalledWith("claude_code", "claude-token", "CLAUDE_CODE_OAUTH_TOKEN");
      expect(probeAgentAdapter).toHaveBeenCalledWith("claude_code");
    });

    it("can remove a stored Claude Code token", async () => {
      const deleteAgentAdapterCredential = vi.fn(async () => true);
      const { state, actions, user } = setup(withAdapter({
        agentAdapters: [
          {
            id: "claude_code",
            name: "Claude Code",
            kind: "acp",
            command: "claude-agent-acp",
            available: true,
            status: "available",
            cost_mode: "external",
            credential_configured: true,
            credential_preview: "clau...oken",
          },
        ],
      }), { deleteAgentAdapterCredential });
      render(<SettingsView state={state} actions={actions} />);
      await user.click(screen.getByRole("button", { name: "External agents" }));
      await user.click(await screen.findByRole("button", { name: "Remove" }));

      expect(deleteAgentAdapterCredential).toHaveBeenCalledWith("claude_code", "CLAUDE_CODE_OAUTH_TOKEN");
    });
  });
});

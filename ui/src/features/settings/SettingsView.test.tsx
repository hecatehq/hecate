import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ConnectionsPanel } from "../connections/ConnectionsPanel";
import { SettingsView } from "./SettingsView";
import { createRuntimeConsoleActions, createRuntimeConsoleFixture } from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";

function setup(stateOverrides = {}, actionOverrides = {}) {
  const state = createRuntimeConsoleFixture(stateOverrides);
  const actions = { ...createRuntimeConsoleActions(), ...actionOverrides };
  const user = userEvent.setup();
  return { state, actions, user };
}

beforeEach(() => {
  sessionStorage.removeItem("hecate.settingsFocus");
  sessionStorage.removeItem("hecate.connectionsFocus");
});

// Connections is now a top-level workspace; Settings keeps
// configuration that does not belong to a runtime connection surface.
// Policy and MCP Cache were removed (single-user mode dropped tenant/role
// gating and the MCP cache was pure informational stats). Usage lives
// in the Usage workspace.
describe("SettingsView", () => {
  it("renders maintenance cleanup without legacy tabs", () => {
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));
    expect(screen.getByText("Maintenance")).toBeTruthy();
    expect(screen.getByText(/Clean up old local runtime data/i)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Retention" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Pricing" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Connections" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Model capabilities" })).toBeNull();
  });

  it("starts on the cleanup controls", () => {
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));
    expect(screen.getByText(/Run cleanup/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Clean up now/i })).toBeTruthy();
  });

  it("triggers a retention-runs fetch on mount", () => {
    // Retention runs are no longer in the boot-time dashboard
    // snapshot — the view is responsible for asking once it's on
    // screen. Without this effect the list stays empty forever.
    const loadRetentionRuns = vi.fn().mockResolvedValue(undefined);
    const { state, actions } = setup({}, { loadRetentionRuns });
    render(withRuntimeConsole(<SettingsView />, { state, actions }));
    expect(loadRetentionRuns).toHaveBeenCalledTimes(1);
  });
});

describe("SettingsView maintenance cleanup", () => {
  it("shows known subsystems as toggle chips", async () => {
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));
    for (const sub of ["Trace snapshots", "Usage events", "Audit events"]) {
      expect(await screen.findByText(sub)).toBeTruthy();
    }
  });

  it("clicking a chip calls setRetentionSubsystems", async () => {
    const setRetentionSubsystems = vi.fn();
    const { state, actions, user } = setup({}, { setRetentionSubsystems });
    render(withRuntimeConsole(<SettingsView />, { state, actions }));
    await user.click(await screen.findByText("Audit events"));
    expect(setRetentionSubsystems).toHaveBeenCalledWith("audit_events");
  });

  it("'Clean up now' button triggers runRetention action", async () => {
    const runRetention = vi.fn(async () => undefined);
    const { state, actions, user } = setup({}, { runRetention });
    render(withRuntimeConsole(<SettingsView />, { state, actions }));
    await user.click(await screen.findByRole("button", { name: /Clean up now/i }));
    expect(runRetention).toHaveBeenCalled();
  });

  it("handles partial retention run payloads without results", async () => {
    const { state, actions } = setup({
      retentionLastRun: {
        finished_at: new Date().toISOString(),
        trigger: "manual",
      },
    });
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(await screen.findByText(/Last run/i)).toBeTruthy();
    expect(screen.getByText("0 removed")).toBeTruthy();
  });
});

// Usage rendering lives in the Usage workspace; Settings intentionally stays
// focused on retention.

describe("Connections external-agent panel", () => {
  const modelCapabilityState = {
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

  it("summarizes model provider connections and links to Connections when requested", async () => {
    const onNavigate = vi.fn();
    const { state, actions, user } = setup({
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "ollama",
            name: "Ollama",
            preset_id: "ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: false,
          },
          {
            id: "anthropic",
            name: "Anthropic",
            preset_id: "anthropic",
            kind: "cloud",
            protocol: "anthropic",
            base_url: "https://api.anthropic.com",
            credential_configured: true,
          },
        ],
        policy_rules: [],
        events: [],
      },
      providers: [
        { name: "ollama", kind: "local", healthy: true, status: "healthy", routing_ready: true, model_count: 3 },
        { name: "anthropic", kind: "cloud", healthy: false, status: "unhealthy", routing_ready: false, readiness: { status: "blocked", reason: "missing_credential" } },
      ],
      models: [
        { id: "llama3", owned_by: "ollama" },
        { id: "claude-sonnet", owned_by: "anthropic" },
      ],
    });
    render(withRuntimeConsole(<ConnectionsPanel onNavigate={onNavigate} />, { state, actions }));

    const card = await screen.findByTestId("connections-model-providers");
    expect(within(card).getByText("Model providers")).toBeTruthy();
    expect(within(card).getByText("2 configured")).toBeTruthy();
    expect(within(card).getByText("Ready")).toBeTruthy();
    expect(within(card).getByText("Needs attention")).toBeTruthy();
    expect(within(card).getByTestId("connections-provider-repair")).toHaveTextContent("Next repair");
    expect(within(card).getByTestId("connections-provider-repair")).toHaveTextContent("Provider blocked");

    await user.click(within(card).getByRole("button", { name: "Open Connections" }));
    expect(onNavigate).toHaveBeenCalledWith("connections");
  });

  it("renders model capabilities inside Connections", async () => {
    const { state, actions } = setup(modelCapabilityState);
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

    expect(await screen.findByTestId("connections-model-capabilities")).toBeTruthy();
    expect(await screen.findByTestId("model-capabilities-list")).toBeTruthy();
    expect(screen.getByText("qwen2.5-coder")).toBeTruthy();
    expect(screen.getAllByText("tools on").length).toBeGreaterThanOrEqual(2);
  });

  it("saves the model tools switch from Connections", async () => {
    const upsertModelCapabilityOverride = vi.fn(async () => true);
    const { state, actions, user } = setup(modelCapabilityState, { upsertModelCapabilityOverride });
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    const row = await screen.findByTestId("model-capability-row-ollama-qwen2.5-coder");

    await user.click(within(row).getByRole("button", { name: "tools on" }));

    expect(upsertModelCapabilityOverride).toHaveBeenCalledWith(expect.objectContaining({
      provider: "ollama",
      model: "qwen2.5-coder",
      tool_calling: "basic",
      note: "Tools enabled from Connections.",
    }));
  });

  it("saves and clears model capability overrides from Connections", async () => {
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
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    const row = await screen.findByTestId("model-capability-row-ollama-local-tools");

    await user.click(within(row).getByRole("button", { name: "tools off" }));
    await user.click(within(row).getByRole("button", { name: "Clear override" }));

    expect(upsertModelCapabilityOverride).toHaveBeenCalledWith(expect.objectContaining({
      provider: "ollama",
      model: "local-tools",
      tool_calling: "none",
      note: "Tools disabled from Connections.",
    }));
    expect(deleteModelCapabilityOverride).toHaveBeenCalledWith("ollama", "local-tools");
  });

  it("fires listChatGrants when the tab opens", async () => {
    const listChatGrants = vi.fn(async () => undefined);
    const { state, actions } = setup({}, { listChatGrants });
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    expect(listChatGrants).toHaveBeenCalled();
  });

  it("renders the empty-state copy when there are no grants", async () => {
    const { state, actions } = setup();
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    expect(await screen.findByTestId("external-agents-empty")).toBeTruthy();
  });

  it("renders one row per grant with adapter / tool / decision metadata", async () => {
    const { state, actions } = setup({
      chatGrants: [
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
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    expect(await screen.findByTestId("external-agents-list")).toBeTruthy();
    // Scope decision-tone assertions to row content so they don't
    // accidentally match the section description above.
    const approveRow = screen.getByTestId("external-agents-row-g-1");
    expect(within(approveRow).getByText(/always approve/i)).toBeTruthy();
    const denyRow = screen.getByTestId("external-agents-row-g-2");
    expect(within(denyRow).getByText(/always deny/i)).toBeTruthy();
  });

  it("revoke asks for inline confirmation before deleting the grant", async () => {
    const deleteChatGrant = vi.fn(async () => true);
    const { state, actions, user } = setup(
      {
        chatGrants: [
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
      { deleteChatGrant },
    );
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    await user.click(await screen.findByTestId("external-agents-revoke-g-7"));
    expect(deleteChatGrant).not.toHaveBeenCalled();
    await user.click(await screen.findByTestId("external-agents-confirm-revoke-g-7"));
    expect(deleteChatGrant).toHaveBeenCalledWith("g-7");
  });

  it("revoke confirmation can be cancelled inline", async () => {
    const deleteChatGrant = vi.fn(async () => true);
    const { state, actions, user } = setup(
      {
        chatGrants: [
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
      { deleteChatGrant },
    );
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    await user.click(await screen.findByTestId("external-agents-revoke-g-8"));
    expect(await screen.findByTestId("external-agents-confirm-revoke-g-8")).toBeTruthy();
    await user.click(await screen.findByTestId("external-agents-cancel-revoke-g-8"));
    expect(deleteChatGrant).not.toHaveBeenCalled();
    expect(screen.queryByTestId("external-agents-confirm-revoke-g-8")).toBeNull();
  });

  it("surfaces the listing error inline when the load fails", async () => {
    const { state, actions } = setup({
      chatGrantsError: "list failed: 500",
    });
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
    expect(await screen.findByText(/list failed: 500/)).toBeTruthy();
  });

  it("keeps the Anthropic provider key card visible through transient settings refreshes", async () => {
    const { state, actions } = setup({
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "anthropic",
            name: "Anthropic",
            preset_id: "anthropic",
            kind: "cloud",
            protocol: "anthropic",
            base_url: "https://api.anthropic.com",
            credential_configured: true,
          },
        ],
        policy_rules: [],
        events: [],
      },
    });
    const { rerender } = render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

    expect(await screen.findByTestId("anthropic-provider-key-card")).toBeTruthy();

    rerender(withRuntimeConsole(<ConnectionsPanel />, { state: { ...state, settingsConfig: { ...state.settingsConfig!, providers: [] } }, actions }));

    expect(screen.getByTestId("anthropic-provider-key-card")).toBeTruthy();
  });

  it("saves and clears the Anthropic provider key from Connections settings", async () => {
    const setProviderAPIKey = vi.fn(async () => undefined);
    const { state, actions, user } = setup({
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "anthropic",
            name: "Anthropic",
            preset_id: "anthropic",
            kind: "cloud",
            protocol: "anthropic",
            base_url: "https://api.anthropic.com",
            credential_configured: true,
          },
        ],
        policy_rules: [],
        events: [],
      },
    }, { setProviderAPIKey });
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

    await user.type(await screen.findByLabelText("Anthropic API key"), "sk-ant-new");
    await user.click(screen.getByRole("button", { name: "Update key" }));
    await user.click(screen.getByRole("button", { name: "Remove" }));

    expect(setProviderAPIKey).toHaveBeenNthCalledWith(1, "anthropic", "sk-ant-new");
    expect(setProviderAPIKey).toHaveBeenNthCalledWith(2, "anthropic", "");
  });

  // Adapter status panel — surfaces auto-probe results when the tab
  // opens. The section is hidden when no adapters are registered (no
  // point showing an empty card); otherwise each row renders inline
  // diagnostic copy when a result exists.
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
      const { state, actions } = setup({ agentAdapters: [] });
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      expect(screen.queryByTestId("external-agents-adapters")).toBeNull();
    });

    it("renders one row per adapter without a manual test button", async () => {
      const { state, actions } = setup(withAdapter());
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      expect(await screen.findByTestId("external-agents-adapters")).toBeTruthy();
      expect(screen.getByTestId("external-agents-adapter-codex")).toBeTruthy();
      expect(screen.queryByTestId("external-agents-test-codex")).toBeNull();
    });

    it("auto-runs adapter probes when the tab opens", async () => {
      const probeAgentAdapter = vi.fn(async () => null);
      const { state, actions } = setup(withAdapter(), { probeAgentAdapter });
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      expect(probeAgentAdapter).toHaveBeenCalledWith("codex");
    });

    it("auto-runs adapter probes when adapters arrive after the tab opens", async () => {
      const probeAgentAdapter = vi.fn(async () => null);
      const { state, actions } = setup({ agentAdapters: [] }, { probeAgentAdapter });
      const { rerender } = render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      expect(probeAgentAdapter).not.toHaveBeenCalled();

      const nextState = createRuntimeConsoleFixture(withAdapter());
      rerender(withRuntimeConsole(<ConnectionsPanel />, { state: nextState, actions }));
      expect(probeAgentAdapter).toHaveBeenCalledWith("codex");
      rerender(withRuntimeConsole(<ConnectionsPanel />, { state: { ...nextState }, actions }));
      expect(probeAgentAdapter).toHaveBeenCalledTimes(1);
    });

    it("renders the auth-required hint when the cached probe says auth is missing", async () => {
      const { state, actions } = setup(withAdapter({
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
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      const detail = await screen.findByTestId("external-agents-adapter-codex-detail");
      expect(within(detail).getByText("Run codex login")).toBeTruthy();
      expect(within(detail).getByText(/Authentication required/)).toBeTruthy();
    });

    it("renders discovery auth warnings before a full probe has run", async () => {
      const { state, actions } = setup(withAdapter({
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
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      expect(await screen.findByTestId("external-agents-adapter-cursor_agent-auth-warning")).toHaveTextContent("auth required");
      expect(screen.getByTestId("external-agents-adapter-cursor_agent-auth-detail")).toHaveTextContent("Run cursor-agent login");
    });

    it("shows an inline checking status while a probe is in flight", async () => {
      const { state, actions } = setup(withAdapter({
        agentAdapterHealthLoadingByID: new Map([["codex", true]]),
      }));
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      expect(await screen.findByTestId("external-agents-checking-codex")).toHaveTextContent(/checking/i);
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
            auth_error: "Save a Claude Code token here; Hecate validates it before storing.",
          },
        ],
      }), { setAgentAdapterCredential, probeAgentAdapter });
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      expect(await screen.findByTestId("claude-code-guided-setup")).toBeTruthy();
      await user.type(screen.getByLabelText("Claude Code OAuth token"), "claude-token");
      await user.click(screen.getByRole("button", { name: "Save" }));

      expect(setAgentAdapterCredential).toHaveBeenCalledWith("claude_code", "claude-token", "CLAUDE_CODE_OAUTH_TOKEN");
    });



    it("keeps Claude Code token editing visible when the adapter handshake is ready but no token is configured", async () => {
      const { state, actions } = setup(withAdapter({
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
          },
        ],
        agentAdapterHealthByID: new Map([
          ["claude_code", { adapter_id: "claude_code", status: "ready", stage: "ready", duration_ms: 629 }],
        ]),
      }));
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      expect(await screen.findByText("Claude Code guided setup")).toBeTruthy();
      expect(screen.queryByText("Claude Code token verified")).toBeNull();
      expect(screen.getByText("adapter installed")).toBeTruthy();
      expect(screen.getByText("token not saved")).toBeTruthy();
      expect(screen.getByLabelText("Claude Code OAuth token")).toBeTruthy();
      expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
    });

    it("shows CLI sign-in separately from Hecate's adapter token", async () => {
      const { state, actions } = setup(withAdapter({
        agentAdapters: [
          {
            id: "claude_code",
            name: "Claude Code",
            kind: "acp",
            command: "claude-agent-acp",
            available: true,
            status: "available",
            cost_mode: "external",
            auth_status: "ok",
          },
        ],
        agentAdapterHealthByID: new Map([
          ["claude_code", { adapter_id: "claude_code", status: "ready", stage: "ready", duration_ms: 629 }],
        ]),
      }));
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      expect(await screen.findByText("Claude Code guided setup")).toBeTruthy();
      expect(screen.getByText("adapter installed")).toBeTruthy();
      expect(screen.getByText("token not saved")).toBeTruthy();
      expect(screen.getByText("CLI signed in")).toBeTruthy();
      expect(screen.queryByText("Claude Code token verified")).toBeNull();
      expect(screen.getByLabelText("Claude Code OAuth token")).toBeTruthy();
    });

    it("shows a token-verified result after Claude Code token validation passes", async () => {
      const { state, actions } = setup(withAdapter({
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
            credential_configured: true,
            credential_preview: "sk-a...SwAA",
          },
        ],
        agentAdapterHealthByID: new Map([
          ["claude_code", { adapter_id: "claude_code", status: "ready", stage: "ready", duration_ms: 629 }],
        ]),
      }));
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      expect(await screen.findByText("Claude Code token verified")).toBeTruthy();
      expect(screen.getByText(/Hecate has a validated adapter token/)).toBeTruthy();
      expect(screen.getByText("adapter installed")).toBeTruthy();
      expect(screen.getByText("token valid")).toBeTruthy();
      expect(screen.getByText(/Stored token/)).toBeTruthy();
      expect(screen.getByText("Token valid.")).toBeTruthy();
      expect(screen.queryByTestId("external-agents-adapter-claude_code-auth-warning")).toBeNull();
      expect(screen.getByLabelText("Claude Code OAuth token")).toBeTruthy();
      expect(screen.getByPlaceholderText("Paste a replacement CLAUDE_CODE_OAUTH_TOKEN")).toBeTruthy();
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
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      await user.click(await screen.findByRole("button", { name: "Remove" }));

      expect(deleteAgentAdapterCredential).toHaveBeenCalledWith("claude_code", "CLAUDE_CODE_OAUTH_TOKEN");
    });
  });
});

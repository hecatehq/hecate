import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { getPlugins, resetSystemData } from "../../lib/api";
import { ConnectionsPanel } from "../connections/ConnectionsPanel";
import { SettingsView } from "./SettingsView";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";

vi.mock("../../lib/api", async (importOriginal) => ({
  ...((await importOriginal()) as Record<string, unknown>),
  getPlugins: vi.fn(),
  resetSystemData: vi.fn(),
}));

function setup(stateOverrides = {}, actionOverrides = {}) {
  const state = createRuntimeConsoleFixture(stateOverrides);
  const actions = { ...createRuntimeConsoleActions(), ...actionOverrides };
  const user = userEvent.setup();
  return { state, actions, user };
}

beforeEach(() => {
  vi.mocked(getPlugins).mockReset();
  vi.mocked(getPlugins).mockResolvedValue({ object: "plugins", data: [] });
  vi.mocked(resetSystemData).mockReset();
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

  it("renders plugin registry records for operator inspection", async () => {
    vi.mocked(getPlugins).mockResolvedValue({
      object: "plugins",
      data: [
        {
          id: "github",
          name: "GitHub",
          description: "Read and link GitHub work.",
          version: "0.1.0",
          source_kind: "local_path",
          source_ref: "/plugins/github/plugin.json",
          manifest_schema_version: "hecate.plugin.v0",
          manifest_digest: "sha256:abc",
          requested_permissions: [{ value: "network:github.com", classification: "advisory" }],
          registry_state: "valid",
          enabled: false,
          warnings: [],
          capabilities: [
            {
              id: "issues",
              kind: "connector",
              display_name: "Issues",
              requested_permissions: [{ value: "secret:github_token", classification: "advisory" }],
              enabled: true,
            },
            {
              id: "github",
              kind: "mcp_server",
              display_name: "GitHub MCP",
              enabled: true,
              mcp_server: {
                name: "github",
                transport: "stdio",
                command: "npx",
                args: ["-y", "@modelcontextprotocol/server-github"],
                env: { GITHUB_TOKEN: "$GITHUB_TOKEN" },
                approval_policy: "require_approval",
              },
            },
          ],
          auth: [
            {
              capability_id: "issues",
              requested_name: "github_token",
              kind: "token",
              status: "unknown",
            },
          ],
          installed_at: "2026-06-18T10:00:00Z",
          updated_at: "2026-06-18T10:00:00Z",
        },
      ],
    });
    const { state, actions } = setup();
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(await screen.findByText("GitHub")).toBeTruthy();
    expect(screen.getByText("github@0.1.0")).toBeTruthy();
    expect(screen.getByText(/2 capabilities/i)).toBeTruthy();
    expect(
      screen.getByText(/MCP github · stdio: npx -y @modelcontextprotocol\/server-github/i),
    ).toBeTruthy();
    expect(screen.getByText(/Unresolved auth: github_token/i)).toBeTruthy();
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

  it("labels the reset affordance as runtime-only on memory backend", async () => {
    const { state, actions, user } = setup({
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
    });
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(screen.getByText("Reset runtime state")).toBeTruthy();
    expect(screen.getByText(/current in-memory state/i)).toBeTruthy();
    await user.click(await screen.findByRole("button", { name: /Reset/i }));

    expect(screen.getByText(/memory storage/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Reset runtime state" })).toBeDisabled();
  });

  it("labels the reset affordance as local data cleanup on sqlite backend", async () => {
    const { state, actions } = setup({
      settingsConfig: { backend: "sqlite", providers: [], policy_rules: [], events: [] },
    });
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    expect(screen.getByText("Reset local data")).toBeTruthy();
    expect(screen.getByText(/remaining Hecate database rows/i)).toBeTruthy();
  });

  it("resets local data after typed confirmation and refreshes dashboard state", async () => {
    vi.mocked(resetSystemData).mockResolvedValue({
      object: "system_reset",
      data: {
        projects_deleted: 1,
        plugins_deleted: 1,
        chat_sessions_deleted: 2,
        tasks_deleted: 1,
        providers_deleted: 1,
        policy_rules_deleted: 1,
        agent_approval_grants_deleted: 1,
        database_rows_deleted: 3,
      },
    });
    const loadDashboard = vi.fn(async () => undefined);
    const { state, actions, user } = setup(
      { settingsConfig: { backend: "sqlite", providers: [], policy_rules: [], events: [] } },
      { loadDashboard },
    );
    render(withRuntimeConsole(<SettingsView />, { state, actions }));

    await user.click(await screen.findByRole("button", { name: /Reset/i }));
    const confirm = screen.getByRole("button", { name: "Reset local data" });
    expect(confirm).toBeDisabled();

    await user.type(screen.getByLabelText(/Type RESET/i), "RESET");
    expect(confirm).toBeEnabled();
    await user.click(confirm);

    await waitFor(() => expect(resetSystemData).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(loadDashboard).toHaveBeenCalledTimes(1));
  });
});

// Usage rendering lives in the Usage workspace; Settings intentionally stays
// focused on retention.

describe("Connections external-agent panel", () => {
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
        {
          name: "ollama",
          kind: "local",
          healthy: true,
          status: "healthy",
          routing_ready: true,
          model_count: 3,
        },
        {
          name: "anthropic",
          kind: "cloud",
          healthy: false,
          status: "unhealthy",
          routing_ready: false,
          readiness: { status: "blocked", reason: "missing_credential" },
        },
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
    expect(within(card).getByTestId("connections-provider-repair")).toHaveTextContent(
      "Next repair",
    );
    expect(within(card).getByTestId("connections-provider-repair")).toHaveTextContent(
      "Provider blocked",
    );

    await user.click(within(card).getByRole("button", { name: "Open Connections" }));
    expect(onNavigate).toHaveBeenCalledWith("connections");
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

    rerender(
      withRuntimeConsole(<ConnectionsPanel />, {
        state: { ...state, settingsConfig: { ...state.settingsConfig!, providers: [] } },
        actions,
      }),
    );

    expect(screen.getByTestId("anthropic-provider-key-card")).toBeTruthy();
  });

  it("saves and clears the Anthropic provider key from Connections settings", async () => {
    const setProviderAPIKey = vi.fn(async () => undefined);
    const { state, actions, user } = setup(
      {
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
      },
      { setProviderAPIKey },
    );
    render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

    await user.type(await screen.findByLabelText("Anthropic API key"), "sk-ant-new");
    await user.click(screen.getByRole("button", { name: "Update key" }));
    await user.click(screen.getByRole("button", { name: "Remove" }));

    expect(setProviderAPIKey).toHaveBeenNthCalledWith(1, "anthropic", "sk-ant-new");
    expect(setProviderAPIKey).toHaveBeenNthCalledWith(2, "anthropic", "");
  });

  // External agent status panel — surfaces readiness diagnostics.
  // Direct binaries can be checked quietly; managed package-launcher
  // agents require an explicit confirmation before a manual check.
  // The section is hidden when no agents are registered (no point
  // showing an empty card); otherwise each row renders inline
  // diagnostic copy when a result exists.
  describe("adapter status panel", () => {
    function withAdapter(overrides: Record<string, unknown> = {}) {
      return {
        agentAdapters: [
          {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
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

    it("shows bridge and underlying agent versions separately", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "codex",
              name: "Codex",
              kind: "acp",
              command: "codex-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              adapter_version: "1.2.3",
              agent_version: "0.48.0",
            },
          ],
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      const row = await screen.findByTestId("external-agents-adapter-codex");
      expect(row).toHaveTextContent("bridge 1.2.3");
      expect(row).toHaveTextContent("agent 0.48.0");
    });

    it("renders compact local sign-in when the cached probe says auth is missing", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapterHealthByID: new Map([
            [
              "codex",
              {
                adapter_id: "codex",
                status: "auth_required",
                stage: "initialize",
                path: "/usr/local/bin/codex-acp-adapter",
                error: "Authentication required",
                hint: "Run codex login",
                duration_ms: 412,
              },
            ],
          ]),
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      const row = await screen.findByTestId("external-agents-adapter-codex");
      expect(within(row).getByText("sign in")).toBeTruthy();
      expect(within(row).getByText("Local sign-in")).toBeTruthy();
      expect(within(row).getByText("codex login")).toBeTruthy();
      expect(screen.queryByTestId("external-agents-adapter-codex-detail")).toBeNull();
      expect(screen.queryByTestId("external-agents-adapter-codex-auth-warning")).toBeNull();
      expect(row).not.toHaveTextContent("path /usr/local/bin/codex-acp-adapter");
      expect(row).not.toHaveTextContent("412 ms");
      expect(row).not.toHaveTextContent("auth unknown");
    });

    it("shows missing adapters as setup notifications", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "codex",
              name: "Codex",
              kind: "acp",
              command: "codex-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unknown",
            },
            {
              id: "cursor_agent",
              name: "Cursor Agent",
              kind: "acp",
              command: "cursor-agent",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unknown",
            },
          ],
          agentAdapterHealthByID: new Map([
            [
              "codex",
              {
                adapter_id: "codex",
                status: "not_installed",
                stage: "lookup",
                error: "codex-acp-adapter command was not found",
                hint: "Install Codex and ensure codex-acp-adapter is on PATH.",
                duration_ms: 0,
              },
            ],
            [
              "cursor_agent",
              {
                adapter_id: "cursor_agent",
                status: "error",
                stage: "ready",
                path: "dev-override://cursor_agent",
                error: "forced app CLI missing by HECATE_AGENT_ADAPTER_DEV_OVERRIDES",
                hint: "Install Cursor with Agent support, then sign in with Cursor Agent.",
                duration_ms: 0,
              },
            ],
          ]),
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      const codex = await screen.findByTestId("external-agents-adapter-codex");
      expect(within(codex).getByText("not configured")).toBeTruthy();
      expect(codex).toHaveTextContent(
        "Set up to use: Install Codex and ensure codex-acp-adapter is on PATH.",
      );
      expect(codex).not.toHaveTextContent("not installed");
      expect(codex).not.toHaveTextContent("auth unknown");
      expect(codex).not.toHaveTextContent("0 ms");

      const cursor = await screen.findByTestId("external-agents-adapter-cursor_agent");
      expect(within(cursor).getByText("not configured")).toBeTruthy();
      expect(cursor).toHaveTextContent("Set up to use: Install Cursor with Agent support");
      expect(cursor).not.toHaveTextContent("error");
      expect(cursor).not.toHaveTextContent("auth unknown");
      expect(cursor).not.toHaveTextContent("dev-override://cursor_agent");
    });

    it("renders local sign-in from discovery auth before a full probe has run", async () => {
      const { state, actions } = setup(
        withAdapter({
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
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      const row = await screen.findByTestId("external-agents-adapter-cursor_agent");
      expect(within(row).getByText("sign in")).toBeTruthy();
      expect(within(row).getByText("Local sign-in")).toBeTruthy();
      expect(within(row).getByText("cursor-agent login")).toBeTruthy();
      expect(screen.queryByTestId("external-agents-adapter-cursor_agent-auth-warning")).toBeNull();
      expect(screen.queryByTestId("external-agents-adapter-cursor_agent-auth-detail")).toBeNull();
    });

    it("shows an inline checking status while a probe is in flight", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapterHealthLoadingByID: new Map([["codex", true]]),
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));
      expect(await screen.findByTestId("external-agents-checking-codex")).toHaveTextContent(
        /checking/i,
      );
    });

    it("does not flash Claude Code local auth guidance before readiness is verified", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "claude_code",
              name: "Claude Code",
              kind: "acp",
              command: "claude-code-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unknown",
            },
          ],
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      expect(await screen.findByTestId("external-agents-adapter-claude_code")).toBeTruthy();
      expect(screen.queryByText("Local sign-in")).toBeNull();
      expect(screen.queryByText(/does not store credentials/)).toBeNull();
      expect(screen.queryByLabelText("Claude Code credential")).toBeNull();
    });

    it("shows Claude Code local auth guidance when discovery reports missing auth", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "claude_code",
              name: "Claude Code",
              kind: "acp",
              command: "claude-code-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unauthenticated",
              auth_error: "Run `claude /login` in Terminal.",
            },
          ],
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      expect(await screen.findByText("Local sign-in")).toBeTruthy();
      expect(screen.getByText("claude /login")).toBeTruthy();
      expect(screen.getByText(/does not store credentials/)).toBeTruthy();
      expect(screen.queryByLabelText("Claude Code credential")).toBeNull();
    });

    it("does not show Claude Code local auth guidance after the adapter probe succeeds", async () => {
      const { state, actions } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "claude_code",
              name: "Claude Code",
              kind: "acp",
              command: "claude-code-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "ok",
            },
          ],
          agentAdapterHealthByID: new Map([
            [
              "claude_code",
              { adapter_id: "claude_code", status: "ready", stage: "ready", duration_ms: 629 },
            ],
          ]),
        }),
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      expect(await screen.findByText("ready")).toBeTruthy();
      expect(screen.queryByText("Local sign-in")).toBeNull();
      expect(screen.queryByTestId("external-agents-adapter-claude_code-auth-warning")).toBeNull();
      expect(screen.queryByLabelText("Claude Code credential")).toBeNull();
    });

    it("copies the Claude Code sign-in command", async () => {
      const copyCommand = vi.fn(async () => undefined);
      const { state, actions, user } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "claude_code",
              name: "Claude Code",
              kind: "acp",
              command: "claude-code-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unauthenticated",
            },
          ],
        }),
        { copyCommand },
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      await user.click(await screen.findByRole("button", { name: "Copy command" }));
      expect(copyCommand).toHaveBeenCalledWith("claude /login");
    });

    it("can retest an adapter after local sign-in", async () => {
      const probeAgentAdapter = vi.fn(async () => null);
      const { state, actions, user } = setup(
        withAdapter({
          agentAdapters: [
            {
              id: "claude_code",
              name: "Claude Code",
              kind: "acp",
              command: "claude-code-acp-adapter",
              available: true,
              status: "available",
              cost_mode: "external",
              auth_status: "unauthenticated",
            },
          ],
        }),
        { probeAgentAdapter },
      );
      render(withRuntimeConsole(<ConnectionsPanel />, { state, actions }));

      await user.click(await screen.findByRole("button", { name: "Test again" }));
      expect(probeAgentAdapter).toHaveBeenCalledWith("claude_code");
    });
  });
});

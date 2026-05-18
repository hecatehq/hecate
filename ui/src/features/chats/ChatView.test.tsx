import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ChatView } from "./ChatView";
import { discoverLocalProviders } from "../../lib/api";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    discoverLocalProviders: vi.fn(async () => ({ object: "local_provider_discovery", data: [] })),
  };
});

afterEach(() => {
  vi.mocked(discoverLocalProviders).mockReset();
  vi.mocked(discoverLocalProviders).mockResolvedValue({
    object: "local_provider_discovery",
    data: [],
  });
});

function setup(stateOverrides: Record<string, any> = {}, actionOverrides = {}) {
  const state = createRuntimeConsoleFixture({
    providerScopedModels: [
      {
        id: "gpt-4o-mini",
        owned_by: "openai",
        metadata: { provider: "openai", provider_kind: "cloud" },
      },
    ],
    ...stateOverrides,
  });
  const actions = { ...createRuntimeConsoleActions(), ...actionOverrides };
  return { state, actions };
}

describe("ChatView input", () => {
  it("renders Hecate first in the unified agent picker", async () => {
    const { state, actions } = setup();
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByRole("button", { name: "New Hecate chat" })).toBeTruthy();
    const picker = screen.getByRole("button", { name: "Choose agent for new chat" });

    const user = userEvent.setup();
    await user.click(picker);
    const options = screen.getAllByRole("option");
    expect(options.map((option) => option.textContent?.replace(/\s+/g, " ").trim())).toEqual([
      "Hecate· local",
      "Codex· setup",
      "Claude Code· setup",
      "Cursor· setup",
    ]);
  }, 10_000);

  it("toggles Hecate Chat between direct model chat and tool-backed agent mode", async () => {
    const setChatTarget = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        providerScopedModels: [
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: {
              provider: "openai",
              provider_kind: "cloud",
              capabilities: { tool_calling: "basic", streaming: true, source: "catalog" },
            },
          },
        ],
      },
      { setChatTarget },
    );
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /settings/i }));
    expect(screen.getByText("Mode")).toBeTruthy();
    expect(screen.getByText("Tools")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Tools on" }));
    expect(setChatTarget).toHaveBeenCalledWith("model");

    const directState = setup({ ...state, chatTarget: "model" }, { setChatTarget }).state;
    rerender(withRuntimeConsole(<ChatView />, { state: directState, actions }));
    expect(screen.getByRole("button", { name: "Tools off" })).toHaveTextContent("off");

    await user.click(screen.getByRole("button", { name: "Tools off" }));
    expect(setChatTarget).toHaveBeenCalledWith("agent");
  });

  it("keeps tools off for an existing Hecate session when the next turn is direct model chat", async () => {
    const setChatTarget = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "model",
        activeChatSessionID: "chat_1",
        activeChatSession: {
          id: "chat_1",
          execution_mode: "hecate_task",
          title: "Repo work",
          provider: "openai",
          model: "gpt-4o-mini",
          workspace: "/workspace",
          status: "completed",
          messages: [],
        } as any,
      },
      { setChatTarget },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /settings/i }));

    expect(screen.getByRole("button", { name: "Tools off" })).toHaveTextContent("off");
    await user.click(screen.getByRole("button", { name: "Tools off" }));
    expect(setChatTarget).toHaveBeenCalledWith("agent");
  });

  it("shows editable system prompt instructions in chat settings before the first message", async () => {
    const setSystemPrompt = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        systemPrompt: "Prefer small, reviewable diffs.",
        providerScopedModels: [
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: {
              provider: "openai",
              provider_kind: "cloud",
              capabilities: { tool_calling: "basic", streaming: true, source: "catalog" },
            },
          },
        ],
      },
      { setSystemPrompt },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /settings/i }));

    expect(screen.getByText("SYSTEM PROMPT / AGENT INSTRUCTIONS")).toBeTruthy();
    const editor = screen.getByRole("textbox", { name: "System prompt / agent instructions" });
    expect(editor).toHaveValue("Prefer small, reviewable diffs.");
    fireEvent.change(editor, { target: { value: "Use short patches." } });
    expect(setSystemPrompt).toHaveBeenLastCalledWith("Use short patches.");
  });

  it("keeps Hecate system prompt visible when the active session is Hecate-backed", async () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      systemPrompt: "Keep explanations short.",
      activeChatSessionID: "chat_hecate",
      activeChatSession: {
        id: "chat_hecate",
        execution_mode: "hecate_task",
        title: "Repo work",
        task_id: "task_hecate_123456",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/Users/alice/dev/hecate",
        status: "completed",
        messages: [],
      } as any,
      model: "qwen2.5-coder",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Chat settings" }));

    expect(screen.getByText("System prompt")).toBeTruthy();
    expect(screen.getByRole("textbox", { name: "System prompt / instructions" })).toHaveValue(
      "Keep explanations short.",
    );
  });

  it("shows per-chat settings and toggles compact command output", async () => {
    const setHecateRTKEnabled = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        activeChatSessionID: "chat_1",
        activeChatSession: {
          id: "chat_1",
          execution_mode: "hecate_task",
          title: "Repo work",
          task_id: "task_hecate_123456",
          provider: "ollama",
          model: "qwen2.5-coder",
          rtk_enabled: false,
          workspace: "/Users/alice/dev/hecate",
          status: "completed",
          messages: [
            {
              id: "msg_user",
              role: "user",
              content: "show git status",
              created_at: "2026-05-01T10:00:00Z",
            },
          ],
        } as any,
        hecateRTKEnabled: false,
        hecateRTKAvailable: true,
        hecateRTKPath: "/usr/local/bin/rtk",
        providerFilter: "ollama",
        model: "qwen2.5-coder",
      },
      { setHecateRTKEnabled },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /settings/i }));

    expect(screen.getByText("Chat settings")).toBeTruthy();
    expect(screen.getByText("Compact command output")).toBeTruthy();
    expect(screen.getByText("Session context")).toBeTruthy();
    expect(screen.queryByText("Runtime debug")).toBeNull();
    expect(screen.getByText("Provider")).toBeTruthy();
    expect(screen.queryByText("All providers")).toBeNull();
    expect(screen.getByText("Workspace")).toBeTruthy();
    expect(screen.getByText("/Users/alice/dev/hecate")).toBeTruthy();
    expect(screen.getByText("Status")).toBeTruthy();
    expect(screen.getByText("completed")).toBeTruthy();
    expect(screen.getByText("Messages")).toBeTruthy();
    expect(screen.getByText("1")).toBeTruthy();
    expect(screen.getAllByText(/rtk sh -lc/i).length).toBeGreaterThan(0);
    expect(screen.getByText(/usr\/local\/bin\/rtk/i)).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Compact command output off" }));

    expect(setHecateRTKEnabled).toHaveBeenCalledWith(true);
  });

  it("does not show the RTK onboarding hint after RTK is explicitly turned off in settings", async () => {
    const setHecateRTKEnabled = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        message: "",
        hecateRTKEnabled: true,
        hecateRTKAvailable: true,
        hecateRTKPath: "/usr/local/bin/rtk",
        providerScopedModels: [
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: {
              provider: "openai",
              provider_kind: "cloud",
              capabilities: { tool_calling: "basic", streaming: true, source: "catalog" },
            },
          },
        ],
      },
      { setHecateRTKEnabled },
    );
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "New Hecate chat" }));
    await user.click(screen.getByRole("button", { name: "Chat settings" }));
    await user.click(screen.getByRole("button", { name: "Compact command output on" }));
    expect(setHecateRTKEnabled).toHaveBeenCalledWith(false);

    rerender(
      withRuntimeConsole(<ChatView />, { state: { ...state, hecateRTKEnabled: false }, actions }),
    );
    await user.click(screen.getByRole("button", { name: "Chat settings" }));

    expect(screen.queryByText("Compact command output is available")).toBeNull();
  });

  it("does not expose Hecate instructions for External Agent chats", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "codex",
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
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByText("SYSTEM PROMPT / AGENT INSTRUCTIONS")).toBeNull();
    expect(screen.queryByText("SYSTEM PROMPT / INSTRUCTIONS")).toBeNull();
  });

  it("surfaces adapter-exposed instructions in external-agent chat settings", async () => {
    const setChatConfigOption = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentAdapterID: "codex",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          agent_id: "codex",
          title: "Codex work",
          workspace: "/Users/alice/dev/hecate",
          status: "idle",
          config_options: [
            {
              id: "system_prompt",
              name: "System prompt",
              description: "Instructions applied by the adapter.",
              category: "instructions",
              type: "text",
              current_value: "Be concise.",
            },
          ],
          messages: [],
        } as any,
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
      },
      { setChatConfigOption },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Chat settings" }));

    expect(screen.getByText("Adapter controls")).toBeTruthy();
    expect(screen.getByText("Instructions applied by the adapter.")).toBeTruthy();
    const editor = screen.getByRole("textbox", { name: "System prompt / instructions" });
    expect(editor).toHaveValue("Be concise.");

    await user.clear(editor);
    await user.type(editor, "Prefer short answers.");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(setChatConfigOption).toHaveBeenCalledWith(
      "a1",
      "system_prompt",
      "Prefer short answers.",
    );
  });

  it("disables the send button when message is empty", () => {
    const { state, actions } = setup({ message: "" });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    fireEvent.click(screen.getByRole("button", { name: /new .* chat/i }));
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });

  it("enables the send button when message has content", () => {
    const { state, actions } = setup({ chatTarget: "model", message: "hello" });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(false);
  });

  it("hides Hecate Chat composer until a model is selected", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      model: "",
      message: "hello",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Send message" })).toBeNull();
  });

  it("hides model composer when no provider is configured", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      message: "hello",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
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
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText("No model provider configured")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Open Connections/i })).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Send message" })).toBeNull();
  });

  it("blocks sending when the selected model is not discovered by the selected provider", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      providerFilter: "ollama",
      model: "llama3.1:8b",
      message: "hello",
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
        ],
        policy_rules: [],
        events: [],
      },
      providers: [
        {
          name: "ollama",
          kind: "local",
          healthy: true,
          status: "degraded",
          base_url: "http://127.0.0.1:11434/v1",
          models: ["qwen2.5:7b"],
          model_count: 1,
          routing_blocked_reason: "no discovered route",
          last_error: "model discovery returned no llama3.1:8b",
        },
      ],
      providerScopedModels: [
        {
          id: "qwen2.5:7b",
          owned_by: "ollama",
          metadata: { provider: "ollama", provider_kind: "local" },
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(
      screen.getAllByText("Selected model is not available from this provider").length,
    ).toBeGreaterThan(0);
    expect(
      screen.getAllByText(/Ollama is configured, but it does not currently report "llama3.1:8b"/)
        .length,
    ).toBeGreaterThan(0);
    expect(screen.getAllByText("Selected model").length).toBeGreaterThan(0);
    expect(screen.getAllByText("llama3.1:8b").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Discovered models").length).toBeGreaterThan(0);
    expect(screen.getAllByText("1").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Health").length).toBeGreaterThan(0);
    expect(screen.getAllByText("degraded").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Blocked by").length).toBeGreaterThan(0);
    expect(screen.getAllByText("no discovered route").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Last error").length).toBeGreaterThan(0);
    expect(screen.getAllByText("model discovery returned no llama3.1:8b").length).toBeGreaterThan(
      0,
    );
    expect(screen.getAllByText("Start the local provider app or server.").length).toBeGreaterThan(
      0,
    );
    expect(
      screen.getAllByText(
        "Pull or load llama3.1:8b in that provider, or pick one of its discovered models.",
      ).length,
    ).toBeGreaterThan(0);
    expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Send message" })).toBeNull();
  });

  it("shows stale selected-model readiness on existing transcripts", async () => {
    const onNavigate = vi.fn();
    const { state, actions } = setup({
      chatTarget: "model",
      providerFilter: "ollama",
      model: "llama3.1:8b",
      message: "hello",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        title: "Existing chat",
        execution_mode: "direct_model",
        status: "completed",
        provider: "ollama",
        model: "llama3.1:8b",
        messages: [
          {
            id: "m1",
            role: "user",
            content: "hi",
            execution_mode: "direct_model",
            created_at: "2026-04-20T00:00:00Z",
          },
        ],
      },
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
          base_url: "http://127.0.0.1:11434/v1",
          models: ["qwen2.5:7b"],
          model_count: 1,
        },
      ],
      providerScopedModels: [
        {
          id: "qwen2.5:7b",
          owned_by: "ollama",
          metadata: { provider: "ollama", provider_kind: "local" },
        },
      ],
    });
    render(withRuntimeConsole(<ChatView onNavigate={onNavigate} />, { state, actions }));

    expect(screen.getByText("Selected model is not available from this provider")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Open Connections" })).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Open Connections" }));
    expect(onNavigate).toHaveBeenCalledWith("connections");
  });

  it("offers the backend-suggested model as a one-click repair", async () => {
    const setModel = vi.fn();
    const setProviderFilter = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "model",
        providerFilter: "anthropic",
        model: "claude-sonnet-4-6",
        message: "hello",
        settingsConfig: {
          backend: "memory",
          providers: [
            {
              id: "anthropic",
              name: "Anthropic",
              preset_id: "anthropic",
              kind: "cloud",
              protocol: "anthropic",
              base_url: "https://api.anthropic.com/v1",
              credential_configured: false,
            },
          ],
          policy_rules: [],
          events: [],
        },
        providerScopedModels: [
          {
            id: "claude-sonnet-4-6",
            owned_by: "anthropic",
            metadata: {
              provider: "anthropic",
              provider_kind: "cloud",
              readiness: {
                ready: false,
                status: "blocked",
                reason: "credential_missing",
                message: "Anthropic needs credentials before this model can route.",
                suggested_models: ["gpt-4o-mini"],
              },
            },
          },
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: { provider: "openai", provider_kind: "cloud" },
          },
        ],
      },
      { setModel, setProviderFilter },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getAllByRole("button", { name: "Use gpt-4o-mini" })[0]);

    expect(setProviderFilter).toHaveBeenCalledWith("auto");
    expect(setModel).toHaveBeenCalledWith("gpt-4o-mini");
  });

  it("opens Connections from the model empty state", async () => {
    const onNavigate = vi.fn();
    const { state, actions } = setup({
      chatTarget: "model",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
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
    });
    render(withRuntimeConsole(<ChatView onNavigate={onNavigate} />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /Open Connections/i }));

    expect(onNavigate).toHaveBeenCalledWith("connections");
  });

  it("keeps configured-provider model discovery repair compact in the empty state", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      providerFilter: "ollama",
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
          base_url: "http://127.0.0.1:11434/v1",
          models: [],
          model_count: 0,
          readiness_checks: [
            {
              name: "credentials",
              status: "ok",
              reason: "not_required",
              message: "No credentials are required for this provider.",
            },
            {
              name: "models",
              status: "blocked",
              reason: "no_models",
              message: "No models were discovered and no default model is configured.",
            },
            {
              name: "routing",
              status: "blocked",
              reason: "no_models",
              message: "Routing is blocked because no models are available.",
            },
          ],
        },
      ],
      providerScopedModels: [],
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
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("No routable model")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Open Connections/i })).toBeTruthy();
    expect(screen.queryByText("No models discovered")).toBeNull();
    expect(screen.queryByText("Routing is blocked because no models are available.")).toBeNull();
    expect(screen.queryByText("Detected locally")).toBeNull();
    expect(screen.queryByRole("button", { name: /Add selected/i })).toBeNull();
  });

  it("quick-adds all installed local providers from the model empty state", async () => {
    vi.mocked(discoverLocalProviders).mockResolvedValueOnce({
      object: "local_provider_discovery",
      data: [
        {
          preset_id: "ollama",
          name: "Ollama",
          base_url: "http://127.0.0.1:11434/v1",
          probe_url: "http://127.0.0.1:11434/api/tags",
          status: "installed",
          command: "ollama",
          command_available: true,
          command_path: "/usr/local/bin/ollama",
          http_available: false,
          model_count: 0,
          models: [],
        },
        {
          preset_id: "lmstudio",
          name: "LM Studio",
          base_url: "http://127.0.0.1:1234/v1",
          probe_url: "http://127.0.0.1:1234/v1/models",
          status: "running",
          command: "lms",
          command_available: true,
          command_path: "/Users/alice/.lmstudio/bin/lms",
          http_available: true,
          model_count: 1,
          models: ["qwen2.5"],
        },
      ],
    });
    const createProvider = vi.fn(async () => undefined);
    const loadDashboard = vi.fn(async () => undefined);
    const setProviderFilter = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "model",
        settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
        providerPresets: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            description: "",
          },
          {
            id: "lmstudio",
            name: "LM Studio",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:1234/v1",
            description: "",
          },
        ],
        providerScopedModels: [],
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
      },
      { createProvider, loadDashboard, setProviderFilter },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    const quickAdd = await screen.findByRole("button", { name: /Add selected/i });
    expect(screen.getByText("Ollama")).toBeTruthy();
    expect(screen.getByText("LM Studio")).toBeTruthy();
    await user.click(quickAdd);

    expect(createProvider).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({
        name: "Ollama",
        preset_id: "ollama",
        base_url: "http://127.0.0.1:11434/v1",
        kind: "local",
        protocol: "openai",
      }),
      { refresh: false },
    );
    expect(createProvider).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        name: "LM Studio",
        preset_id: "lmstudio",
        base_url: "http://127.0.0.1:1234/v1",
        kind: "local",
        protocol: "openai",
      }),
      { refresh: false },
    );
    expect(loadDashboard).toHaveBeenCalledTimes(1);
    expect(setProviderFilter).toHaveBeenCalledWith("lmstudio");
  });

  it("quick-adds only selected local providers", async () => {
    vi.mocked(discoverLocalProviders).mockResolvedValueOnce({
      object: "local_provider_discovery",
      data: [
        {
          preset_id: "ollama",
          name: "Ollama",
          base_url: "http://127.0.0.1:11434/v1",
          probe_url: "http://127.0.0.1:11434/api/tags",
          status: "running",
          command: "ollama",
          command_available: true,
          command_path: "/usr/local/bin/ollama",
          http_available: true,
          model_count: 1,
          models: ["llama3.1:8b"],
        },
        {
          preset_id: "lmstudio",
          name: "LM Studio",
          base_url: "http://127.0.0.1:1234/v1",
          probe_url: "http://127.0.0.1:1234/v1/models",
          status: "running",
          command: "lms",
          command_available: true,
          command_path: "/Users/alice/.lmstudio/bin/lms",
          http_available: true,
          model_count: 1,
          models: ["qwen2.5"],
        },
      ],
    });
    const createProvider = vi.fn(async () => undefined);
    const loadDashboard = vi.fn(async () => undefined);
    const setProviderFilter = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "model",
        settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
        providerPresets: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            description: "",
          },
          {
            id: "lmstudio",
            name: "LM Studio",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:1234/v1",
            description: "",
          },
        ],
        providerScopedModels: [],
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
      },
      { createProvider, loadDashboard, setProviderFilter },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    expect(await screen.findByRole("button", { name: "Deselect Ollama" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await user.click(screen.getByRole("button", { name: "Deselect LM Studio" }));
    await user.click(screen.getByRole("button", { name: /Add selected/i }));

    expect(createProvider).toHaveBeenCalledTimes(1);
    expect(createProvider).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "Ollama",
        preset_id: "ollama",
      }),
      { refresh: false },
    );
    expect(loadDashboard).toHaveBeenCalledTimes(1);
    expect(setProviderFilter).toHaveBeenCalledWith("ollama");
  });

  it("shows one-click local provider onboarding from Hecate Agent chat", async () => {
    vi.mocked(discoverLocalProviders).mockResolvedValueOnce({
      object: "local_provider_discovery",
      data: [
        {
          preset_id: "ollama",
          name: "Ollama",
          base_url: "http://127.0.0.1:11434/v1",
          probe_url: "http://127.0.0.1:11434/api/tags",
          status: "running",
          command: "ollama",
          command_available: true,
          command_path: "/usr/local/bin/ollama",
          http_available: true,
          model_count: 1,
          models: ["qwen2.5-coder"],
        },
      ],
    });
    const createProvider = vi.fn(async () => undefined);
    const loadDashboard = vi.fn(async () => undefined);
    const setProviderFilter = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        providerFilter: "lmstudio",
        agentWorkspace: "/tmp/hecate",
        settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
        providerPresets: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            description: "",
          },
        ],
        providerScopedModels: [],
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
      },
      { createProvider, loadDashboard, setProviderFilter },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(await screen.findByText("Detected locally")).toBeTruthy();
    expect(await screen.findByRole("button", { name: "Deselect Ollama" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /Add selected/i }));

    await waitFor(() => {
      expect(createProvider).toHaveBeenCalledWith(
        expect.objectContaining({
          name: "Ollama",
          preset_id: "ollama",
          base_url: "http://127.0.0.1:11434/v1",
        }),
        { refresh: false },
      );
    });
    await waitFor(() => expect(loadDashboard).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(setProviderFilter).toHaveBeenCalledWith("ollama"));
  });

  it("quick-add skips duplicate local provider endpoints", async () => {
    vi.mocked(discoverLocalProviders).mockResolvedValueOnce({
      object: "local_provider_discovery",
      data: [
        {
          preset_id: "llamacpp",
          name: "llama.cpp",
          base_url: "http://127.0.0.1:8080/v1",
          probe_url: "http://127.0.0.1:8080/v1/models",
          status: "running",
          command: "llama-server",
          command_available: true,
          command_path: "/usr/local/bin/llama-server",
          http_available: true,
          model_count: 1,
          models: ["local-model"],
        },
        {
          preset_id: "localai",
          name: "LocalAI",
          base_url: "http://127.0.0.1:8080/v1",
          probe_url: "http://127.0.0.1:8080/v1/models",
          status: "running",
          command: "local-ai",
          command_available: true,
          command_path: "/usr/local/bin/local-ai",
          http_available: true,
          model_count: 1,
          models: ["local-model"],
        },
      ],
    });
    const createProvider = vi.fn(async () => undefined);
    const loadDashboard = vi.fn(async () => undefined);
    const setProviderFilter = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "model",
        settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
        providerPresets: [
          {
            id: "llamacpp",
            name: "llama.cpp",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:8080/v1",
            description: "",
          },
          {
            id: "localai",
            name: "LocalAI",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:8080/v1",
            description: "",
          },
        ],
        providerScopedModels: [],
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
      },
      { createProvider, loadDashboard, setProviderFilter },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Add selected/i }));

    await waitFor(() => expect(createProvider).toHaveBeenCalledTimes(1));
    await waitFor(() => {
      expect(createProvider).toHaveBeenCalledWith(
        expect.objectContaining({
          name: "llama.cpp",
          preset_id: "llamacpp",
          base_url: "http://127.0.0.1:8080/v1",
        }),
        { refresh: false },
      );
    });
    await waitFor(() => expect(loadDashboard).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(setProviderFilter).toHaveBeenCalledWith("llamacpp"));
  });

  it("quick-add refreshes dashboard after partial provider creation failures", async () => {
    vi.mocked(discoverLocalProviders).mockResolvedValueOnce({
      object: "local_provider_discovery",
      data: [
        {
          preset_id: "ollama",
          name: "Ollama",
          base_url: "http://127.0.0.1:11434/v1",
          probe_url: "http://127.0.0.1:11434/api/tags",
          status: "running",
          command: "ollama",
          command_available: true,
          command_path: "/usr/local/bin/ollama",
          http_available: true,
          model_count: 1,
          models: ["llama3.1:8b"],
        },
        {
          preset_id: "lmstudio",
          name: "LM Studio",
          base_url: "http://127.0.0.1:1234/v1",
          probe_url: "http://127.0.0.1:1234/v1/models",
          status: "running",
          command: "lms",
          command_available: true,
          command_path: "/Users/alice/.lmstudio/bin/lms",
          http_available: true,
          model_count: 1,
          models: ["qwen2.5"],
        },
      ],
    });
    const createProvider = vi.fn(async (params: unknown) => {
      if ((params as { preset_id?: string }).preset_id === "lmstudio") {
        throw new Error("LM Studio endpoint already exists");
      }
    });
    const loadDashboard = vi.fn(async () => undefined);
    const setProviderFilter = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "model",
        settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
        providerPresets: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            description: "",
          },
          {
            id: "lmstudio",
            name: "LM Studio",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:1234/v1",
            description: "",
          },
        ],
        providerScopedModels: [],
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
      },
      { createProvider, loadDashboard, setProviderFilter },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Add selected/i }));

    await waitFor(() => expect(createProvider).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(loadDashboard).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(setProviderFilter).toHaveBeenCalledWith("ollama"));
    expect(screen.getByText("LM Studio endpoint already exists")).toBeTruthy();
  });

  it("shows a first-run setup state when providers and agents are unavailable", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      message: "hello",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp",
          available: false,
          status: "missing",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText("Nothing runnable yet")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Open Connections/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: "New Hecate chat" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Choose agent for new chat" })).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Send message" })).toBeNull();
  });

  it("enables Hecate Agent when tools are not explicitly disabled for the model", async () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      message: "inspect this repo",
      agentWorkspace: "/tmp/hecate",
      settingsConfig: {
        backend: "memory",
        providers: [{ id: "ollama", name: "Ollama", kind: "local", credential_configured: true }],
        policy_rules: [],
        events: [],
      },
      providerFilter: "ollama",
      model: "qwen2.5-coder",
      providerScopedModels: [
        {
          id: "qwen2.5-coder",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
          },
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /settings/i }));
    expect(screen.getByRole("button", { name: "Tools on" })).toBeTruthy();
    expect(
      screen.getByText(/task runtime, approvals, artifacts, and sandboxed tool calls/),
    ).toBeTruthy();
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(false);
  });

  it("keeps provider and model pickers editable after a task-backed Hecate Agent segment completes", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      message: "continue",
      agentWorkspace: "/tmp/hecate",
      providerFilter: "ollama",
      model: "smollm2:135m",
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: true,
          },
          {
            id: "openai",
            name: "OpenAI",
            kind: "cloud",
            protocol: "openai",
            base_url: "https://api.openai.com/v1",
            credential_configured: true,
          },
        ],
        policy_rules: [],
        events: [],
      },
      providerPresets: [
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
          description: "",
        },
        {
          id: "openai",
          name: "OpenAI",
          kind: "cloud",
          protocol: "openai",
          base_url: "https://api.openai.com/v1",
          description: "",
        },
      ],
      providerScopedModels: [
        {
          id: "qwen2.5-coder",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
          },
        },
        {
          id: "smollm2:135m",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "unknown", streaming: true, source: "provider" },
          },
        },
      ],
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByRole("button", { name: "Fixed provider: Ollama" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Fixed model: qwen2.5-coder" })).toBeNull();
    expect(screen.getByLabelText("Hecate message controls")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Model picker: smollm2:135m" })).toBeTruthy();
  });

  it("uses shared composer dropdown controls for editable Hecate provider and model selection", async () => {
    const setProviderFilter = vi.fn();
    const setModel = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        message: "continue",
        agentWorkspace: "/tmp/hecate",
        providerFilter: "ollama",
        model: "smollm2:135m",
        settingsConfig: {
          backend: "memory",
          providers: [
            {
              id: "ollama",
              name: "Ollama",
              kind: "local",
              protocol: "openai",
              base_url: "http://127.0.0.1:11434/v1",
              credential_configured: true,
            },
            {
              id: "openai",
              name: "OpenAI",
              kind: "cloud",
              protocol: "openai",
              base_url: "https://api.openai.com/v1",
              credential_configured: true,
            },
          ],
          policy_rules: [],
          events: [],
        },
        providerPresets: [
          {
            id: "ollama",
            name: "Ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            description: "",
          },
          {
            id: "openai",
            name: "OpenAI",
            kind: "cloud",
            protocol: "openai",
            base_url: "https://api.openai.com/v1",
            description: "",
          },
        ],
        providerScopedModels: [
          {
            id: "qwen2.5-coder",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
            },
          },
          {
            id: "smollm2:135m",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              capabilities: { tool_calling: "unknown", streaming: true, source: "provider" },
            },
          },
        ],
        activeChatSessionID: "chat_1",
        activeChatSession: {
          id: "chat_1",
          execution_mode: "hecate_task",
          title: "Repo work",
          task_id: "task_hecate_123456",
          latest_run_id: "run_hecate_abcdef",
          provider: "ollama",
          model: "qwen2.5-coder",
          capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [],
        } as any,
      },
      { setProviderFilter, setModel },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const controls = screen.getByLabelText("Hecate message controls");
    const provider = within(controls).getByRole("button", { name: "Provider picker: Ollama" });
    expect(provider).toHaveTextContent("provider");
    expect(provider).toHaveTextContent("Ollama");
    await userEvent.click(provider);
    expect(screen.queryByRole("option", { name: /All providers/ })).toBeNull();
    await userEvent.click(screen.getByRole("option", { name: /OpenAI/ }));
    expect(setProviderFilter).toHaveBeenCalledWith("openai");

    const model = within(controls).getByRole("button", { name: "Model picker: smollm2:135m" });
    expect(model).toHaveTextContent("model");
    expect(model).toHaveTextContent("smollm2:135m");
    await userEvent.click(model);
    const filter = screen.getByRole("textbox", { name: "Filter models..." });
    await userEvent.type(filter, "qwen");
    expect(screen.getByRole("option", { name: /qwen2.5-coder/ })).toBeTruthy();
    expect(screen.queryByRole("option", { name: /smollm2:135m/ })).toBeNull();
    await userEvent.click(screen.getByRole("option", { name: /qwen2.5-coder/ }));
    expect(setModel).toHaveBeenCalledWith("qwen2.5-coder");
  });

  it("keeps the catalog provider label while the Hecate composer is busy", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      chatLoading: true,
      agentWorkspace: "/tmp/hecate",
      providerFilter: "local-ollama",
      model: "qwen2.5-coder",
      settingsConfig: {
        backend: "memory",
        providers: [
          {
            id: "local-ollama",
            name: "ollama",
            preset_id: "ollama",
            kind: "local",
            protocol: "openai",
            base_url: "http://127.0.0.1:11434/v1",
            credential_configured: true,
          },
        ],
        policy_rules: [],
        events: [],
      },
      providerPresets: [
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
          description: "",
        },
      ],
      providerScopedModels: [
        {
          id: "qwen2.5-coder",
          owned_by: "local-ollama",
          metadata: {
            provider: "local-ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
          },
        },
      ],
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        provider: "local-ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const controls = screen.getByLabelText("Hecate message controls");
    const provider = within(controls).getByRole("button", { name: "Provider picker: Ollama" });
    expect(provider.textContent).toContain("Ollama");
    expect(provider.textContent).not.toContain("ollama");
  });

  it("locks provider and model while a task-backed Hecate Agent segment is active", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      message: "continue",
      agentWorkspace: "/tmp/hecate",
      providerFilter: "ollama",
      model: "smollm2:135m",
      settingsConfig: {
        backend: "memory",
        providers: [{ id: "ollama", name: "Ollama", kind: "local", credential_configured: true }],
        policy_rules: [],
        events: [],
      },
      providerPresets: [
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
          description: "",
        },
      ],
      providerScopedModels: [
        {
          id: "smollm2:135m",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "unknown", streaming: true, source: "provider" },
          },
        },
      ],
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
        workspace: "/tmp/hecate",
        status: "running",
        messages: [],
      } as any,
    });
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));

    const fixedProvider = screen.getByRole("button", {
      name: "Fixed provider: Ollama",
    }) as HTMLButtonElement;
    const fixedModel = screen.getByRole("button", {
      name: "Fixed model: qwen2.5-coder",
    }) as HTMLButtonElement;
    expect(fixedProvider.disabled).toBe(true);
    expect(fixedModel.disabled).toBe(true);
    expect(screen.queryByText("smollm2:135m")).toBeNull();
    expect(screen.queryByText(/Tools are disabled for this model/)).toBeNull();
    expect(screen.getByRole("button", { name: "Queue message" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Stop active task" })).toBeTruthy();
    expect(screen.getByText(/Hecate Chat is still working on this task/)).toBeTruthy();

    rerender(
      withRuntimeConsole(<ChatView />, { state: { ...state, chatTarget: "model" }, actions }),
    );
    expect(document.querySelector('[aria-label="Fixed provider: Ollama"]')).toBeTruthy();
    expect(document.querySelector('[aria-label="Fixed model: qwen2.5-coder"]')).toBeTruthy();
    expect(document.querySelector('[aria-label="Model picker: smollm2:135m"]')).toBeNull();
    expect(screen.getByRole("button", { name: "Stop active task" })).toBeTruthy();
    expect(screen.getByText(/Hecate Chat is still working on this task/)).toBeTruthy();
  });

  it("locks controls to the active task segment even when the session root is direct chat", () => {
    const onOpenTask = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      message: "continue",
      agentWorkspace: "/tmp/hecate",
      providerFilter: "ollama",
      model: "smollm2:135m",
      settingsConfig: {
        backend: "memory",
        providers: [{ id: "ollama", name: "Ollama", kind: "local", credential_configured: true }],
        policy_rules: [],
        events: [],
      },
      providerPresets: [
        {
          id: "ollama",
          name: "Ollama",
          kind: "local",
          protocol: "openai",
          base_url: "http://127.0.0.1:11434/v1",
          description: "",
        },
      ],
      providerScopedModels: [
        {
          id: "smollm2:135m",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "unknown", streaming: true, source: "provider" },
          },
        },
      ],
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "direct_model",
        title: "Mixed chat",
        provider: "ollama",
        model: "smollm2:135m",
        workspace: "/tmp/hecate",
        status: "running",
        segments: [
          {
            id: "model:first",
            execution_mode: "direct_model",
            provider: "ollama",
            model: "smollm2:135m",
            status: "completed",
            message_count: 2,
          },
          {
            id: "task:task_hecate_123456",
            execution_mode: "hecate_task",
            provider: "ollama",
            model: "qwen2.5-coder",
            task_id: "task_hecate_123456",
            latest_run_id: "run_hecate_abcdef",
            status: "running",
            message_count: 1,
          },
        ],
        messages: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView onOpenTask={onOpenTask} />, { state, actions }));

    expect(screen.getByRole("button", { name: "Fixed provider: Ollama" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Fixed model: qwen2.5-coder" })).toBeTruthy();
    expect(screen.queryByText("smollm2:135m")).toBeNull();
    expect(screen.getByRole("button", { name: "Stop active task" })).toBeTruthy();
    expect(screen.getByText(/New messages will queue until the active task finishes/)).toBeTruthy();
    screen.getByRole("button", { name: "Open task" }).click();
    expect(onOpenTask).toHaveBeenCalledWith("task_hecate_123456", "run_hecate_abcdef");
  });

  it("renders queued messages with a remove action", async () => {
    const removeQueuedChatMessage = vi.fn();
    const updateQueuedChatMessage = vi.fn();
    const user = userEvent.setup();
    const { state, actions } = setup(
      {
        activeChatSessionID: "chat_1",
        queuedChatMessages: [
          {
            id: "queued_1",
            session_id: "chat_1",
            content: "run tests after this",
            execution_mode: "hecate_task",
            provider_filter: "ollama",
            model: "qwen2.5-coder",
            workspace: "/workspace",
            system_prompt: "",
            agent_id: "hecate",
            created_at: "2026-04-20T00:00:00Z",
          },
        ],
      },
      { removeQueuedChatMessage, updateQueuedChatMessage },
    );

    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByLabelText("Queued messages")).toBeTruthy();
    const queuedInput = screen.getByLabelText("Queued message 1");
    expect(queuedInput).toHaveValue("run tests after this");
    fireEvent.change(queuedInput, { target: { value: "run unit tests after this" } });
    expect(updateQueuedChatMessage).toHaveBeenLastCalledWith(
      "queued_1",
      "run unit tests after this",
    );
    await user.click(screen.getByRole("button", { name: "Remove queued message 1" }));
    expect(removeQueuedChatMessage).toHaveBeenCalledWith("queued_1");
  });

  it("only renders queued messages for the active agent chat", () => {
    const { state, actions } = setup({
      activeChatSessionID: "chat_active",
      queuedChatMessages: [
        {
          id: "queued_active",
          session_id: "chat_active",
          content: "send this here",
          execution_mode: "hecate_task",
          provider_filter: "ollama",
          model: "qwen2.5-coder",
          workspace: "/workspace",
          system_prompt: "",
          agent_id: "codex",
          created_at: "2026-04-20T00:00:00Z",
        },
        {
          id: "queued_other",
          session_id: "chat_other",
          content: "not in this chat",
          execution_mode: "hecate_task",
          provider_filter: "ollama",
          model: "qwen2.5-coder",
          workspace: "/workspace",
          system_prompt: "",
          agent_id: "codex",
          created_at: "2026-04-20T00:00:00Z",
        },
      ],
    });

    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByLabelText("Queued messages")).toBeTruthy();
    expect(screen.getByLabelText("Queued message 1")).toHaveValue("send this here");
    expect(screen.queryByDisplayValue("not in this chat")).toBeNull();
  });

  it("shows the Hecate Agent sandbox reminder only when tools are enabled", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      providerScopedModels: [
        {
          id: "qwen2.5-coder",
          owned_by: "ollama",
          metadata: {
            provider: "ollama",
            provider_kind: "local",
            capabilities: { tool_calling: "basic", streaming: true, source: "operator_override" },
          },
        },
      ],
      model: "qwen2.5-coder",
    });
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(
      screen.getByText(/Hecate Agent runs through task approvals and per-call sandboxing/),
    ).toBeTruthy();

    rerender(
      withRuntimeConsole(<ChatView />, { state: { ...state, chatTarget: "model" }, actions }),
    );
    expect(
      screen.queryByText(/Hecate Agent runs through task approvals and per-call sandboxing/),
    ).toBeNull();
  });

  it("blocks Hecate Agent sends when tools are explicitly disabled for the model", async () => {
    const upsertModelCapabilityOverride = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        message: "inspect this repo",
        agentWorkspace: "/tmp/hecate",
        settingsConfig: {
          backend: "memory",
          providers: [{ id: "ollama", name: "Ollama", kind: "local", credential_configured: true }],
          policy_rules: [],
          events: [],
        },
        providerFilter: "ollama",
        model: "llama3.1:8b",
        providerScopedModels: [
          {
            id: "llama3.1:8b",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              provider_kind: "local",
              capabilities: { tool_calling: "none", streaming: true, source: "operator_override" },
            },
          },
        ],
      },
      { upsertModelCapabilityOverride },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText(/Tools are disabled for this model/)).toBeTruthy();
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Enable tools" }));
    expect(upsertModelCapabilityOverride).toHaveBeenCalledWith(
      expect.objectContaining({
        provider: "ollama",
        model: "llama3.1:8b",
        tool_calling: "basic",
        note: "Tools enabled from Hecate Chat.",
      }),
    );
  });

  it("opens the backing task from the Hecate Agent assistant turn, not the header", async () => {
    const onOpenTask = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          {
            id: "m1",
            execution_mode: "hecate_task",
            segment_id: "task:task_hecate_123456",
            task_id: "task_hecate_123456",
            role: "user",
            content: "inspect this repo",
            created_at: "2026-05-03T10:00:00Z",
          },
          {
            id: "m2",
            execution_mode: "hecate_task",
            segment_id: "task:task_hecate_123456",
            task_id: "task_hecate_123456",
            run_id: "run_hecate_abcdef",
            role: "assistant",
            content: "Done.",
            status: "completed",
            cost_mode: "hecate",
            created_at: "2026-05-03T10:00:01Z",
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView onOpenTask={onOpenTask} />, { state, actions }));
    const user = userEvent.setup();
    expect(screen.queryByRole("button", { name: /^Task task_hecate_/i })).toBeNull();
    expect(screen.getByText("Run hecate_abcde")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: /Open Task hecate_/i }));
    expect(onOpenTask).toHaveBeenCalledWith("task_hecate_123456", "run_hecate_abcdef");
  });

  it("does not borrow the session task link for direct model messages", () => {
    const onOpenTask = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "direct_model",
        title: "Mixed chat",
        task_id: "task_latest",
        latest_run_id: "run_latest",
        provider: "ollama",
        model: "ministral-3:latest",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          {
            id: "m1",
            execution_mode: "direct_model",
            segment_id: "model:direct",
            role: "user",
            content: "tell a joke",
            created_at: "2026-05-03T10:00:00Z",
          },
          {
            id: "m2",
            execution_mode: "direct_model",
            segment_id: "model:direct",
            run_id: "model_run_1",
            trace_id: "trace_1",
            role: "assistant",
            content: "Direct answer.",
            status: "completed",
            model: "ministral-3:latest",
            created_at: "2026-05-03T10:00:01Z",
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView onOpenTask={onOpenTask} />, { state, actions }));

    expect(screen.getByText("Direct answer.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Open Task latest/i })).toBeNull();
    expect(onOpenTask).not.toHaveBeenCalled();
  });

  it("renders explicit Hecate Chat segment dividers when tools switch", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Mixed chat",
        task_id: "task_second",
        latest_run_id: "run_second",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "completed",
        segments: [
          {
            id: "model:first",
            execution_mode: "direct_model",
            provider: "ollama",
            model: "smollm2:135m",
            status: "completed",
            message_count: 2,
          },
          {
            id: "task:task_first",
            execution_mode: "hecate_task",
            provider: "ollama",
            model: "qwen2.5-coder",
            task_id: "task_first",
            latest_run_id: "run_first",
            status: "completed",
            message_count: 2,
          },
          {
            id: "model:second",
            execution_mode: "direct_model",
            provider: "ollama",
            model: "smollm2:135m",
            status: "completed",
            message_count: 2,
          },
        ],
        messages: [
          {
            id: "m1",
            execution_mode: "direct_model",
            segment_id: "model:first",
            role: "user",
            content: "answer directly",
            created_at: "2026-05-03T10:00:00Z",
          },
          {
            id: "m2",
            execution_mode: "direct_model",
            segment_id: "model:first",
            role: "assistant",
            content: "Direct answer.",
            status: "completed",
            model: "smollm2:135m",
            created_at: "2026-05-03T10:00:01Z",
          },
          {
            id: "m3",
            execution_mode: "hecate_task",
            segment_id: "task:task_first",
            task_id: "task_first",
            role: "user",
            content: "use tools",
            created_at: "2026-05-03T10:01:00Z",
          },
          {
            id: "m4",
            execution_mode: "hecate_task",
            segment_id: "task:task_first",
            task_id: "task_first",
            run_id: "run_first",
            role: "assistant",
            content: "Tool answer.",
            status: "completed",
            model: "qwen2.5-coder",
            created_at: "2026-05-03T10:01:01Z",
          },
          {
            id: "m5",
            execution_mode: "direct_model",
            segment_id: "model:second",
            role: "user",
            content: "back to direct",
            created_at: "2026-05-03T10:02:00Z",
          },
          {
            id: "m6",
            execution_mode: "direct_model",
            segment_id: "model:second",
            role: "assistant",
            content: "Direct again.",
            status: "completed",
            model: "smollm2:135m",
            created_at: "2026-05-03T10:02:01Z",
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getAllByLabelText("Tools off segment using smollm2:135m")).toHaveLength(2);
    expect(screen.getByLabelText("Tools on segment using qwen2.5-coder")).toBeTruthy();
    expect(screen.getByText("Task first")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Open Task second/i })).toBeNull();
    expect(screen.getAllByRole("button", { name: /Open Task first/i })).toHaveLength(1);
    expect(screen.getAllByText(/direct model chat/)).toHaveLength(2);
  });

  it("renders projected Hecate Agent task run activity in the transcript", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          {
            id: "m1",
            role: "user",
            content: "inspect this repo",
            created_at: "2026-05-03T10:00:00Z",
          },
          {
            id: "m2",
            run_id: "run_hecate_abcdef",
            role: "assistant",
            content: "Done.",
            status: "completed",
            cost_mode: "hecate",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              {
                id: "legacy-task-run-running",
                type: "task_running",
                status: "running",
                title: "Task run running",
                detail: "run_hecate_abcdef",
              },
              {
                id: "hecate_task_run:run_hecate_abcdef",
                type: "task_run",
                status: "running",
                title: "Backing task",
                detail: "running · run_hecate_abcdef",
              },
              {
                id: "task:step:model",
                type: "thinking",
                status: "completed",
                kind: "model",
                title: "Agent turn 1",
                detail: "completed",
              },
              {
                id: "task:step:shell",
                type: "tool_call",
                status: "completed",
                kind: "shell",
                title: "shell_exec",
                detail: "completed",
              },
              {
                id: "task:run:terminal",
                type: "run_result",
                status: "completed",
                title: "Run completed",
              },
              { type: "completed", status: "completed", title: "Final answer" },
            ],
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("completed · 1 tool")).toBeTruthy();
    expect(screen.getByText("Thinking")).toBeTruthy();
    expect(screen.getByText("Ran shell")).toBeTruthy();
    expect(screen.getByText("Backing task")).toBeTruthy();
    expect(screen.queryByText("Agent turn 1")).toBeNull();
    expect(screen.getByText("shell_exec")).toBeTruthy();
    expect(screen.queryByText("Task run running")).toBeNull();
    expect(screen.queryByText("Run completed")).toBeNull();
  });

  it("resolves projected Hecate Agent task approvals from the chat banner", async () => {
    const resolveTaskApproval = vi.fn(async () => true);
    const onOpenTask = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        activeChatSessionID: "chat_1",
        activeChatSession: {
          id: "chat_1",
          execution_mode: "hecate_task",
          title: "Repo work",
          task_id: "task_hecate_123456",
          latest_run_id: "run_hecate_abcdef",
          provider: "ollama",
          model: "qwen2.5-coder",
          workspace: "/tmp/hecate",
          status: "awaiting_approval",
          messages: [
            {
              id: "m1",
              role: "user",
              content: "echo lol please",
              created_at: "2026-05-03T10:00:00Z",
            },
            {
              id: "m2",
              run_id: "run_hecate_abcdef",
              role: "assistant",
              content: "",
              status: "awaiting_approval",
              cost_mode: "hecate",
              created_at: "2026-05-03T10:00:01Z",
              activities: [
                {
                  id: "task:step:step_approval",
                  type: "approval",
                  status: "awaiting_approval",
                  kind: "approval",
                  title: "Awaiting approval — turn 1",
                  detail:
                    "Agent requested tools that require approval: shell_exec - awaiting_approval",
                  approval_id: "appr_123",
                  needs_action: true,
                  created_at: "2026-05-03T10:00:02Z",
                },
              ],
            },
          ],
        } as any,
      },
      { resolveTaskApproval },
    );
    render(withRuntimeConsole(<ChatView onOpenTask={onOpenTask} />, { state, actions }));

    expect(screen.getByTestId("hecate-task-approval-banner")).toBeTruthy();
    expect(screen.getByText("Approval required")).toBeTruthy();
    expect(screen.getByText("Shell execution")).toBeTruthy();
    expect(screen.getAllByText("Waiting for approval").length).toBeGreaterThan(0);

    const user = userEvent.setup();
    await user.click(screen.getAllByRole("button", { name: "Open task" })[0]!);
    expect(onOpenTask).toHaveBeenCalledWith("task_hecate_123456", "run_hecate_abcdef");

    await user.click(screen.getByRole("button", { name: /Approve Shell execution/i }));
    expect(resolveTaskApproval).toHaveBeenCalledWith("task_hecate_123456", "appr_123", {
      decision: "approve",
    });
  });

  it("does not keep stale resolved Hecate Agent task approvals actionable", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [
          {
            id: "m2",
            run_id: "run_hecate_abcdef",
            role: "assistant",
            content: "",
            status: "running",
            cost_mode: "hecate",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              {
                id: "task:step:step_approval",
                type: "approval",
                status: "approved",
                kind: "agent_loop_tool_call",
                title: "Awaiting approval — turn 1",
                detail: "Agent requested tools that require approval: shell_exec - approved",
                approval_id: "appr_123",
                needs_action: true,
              },
            ],
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByTestId("hecate-task-approval-banner")).toBeNull();
    expect(screen.queryByRole("button", { name: /Approve Agent tool call/i })).toBeNull();
  });

  it("calls setMessage as user types", async () => {
    const setMessage = vi.fn();
    // Start with empty message so the assertion sees only what we typed.
    const { state, actions } = setup({ chatTarget: "model", message: "" }, { setMessage });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    fireEvent.click(screen.getByRole("button", { name: /new .* chat/i }));
    const ta = screen.getByPlaceholderText(/Message/i) as HTMLTextAreaElement;
    const user = userEvent.setup();
    await user.type(ta, "h");
    expect(setMessage).toHaveBeenCalledWith("h");
  });

  it("browses previous user messages with ArrowUp and ArrowDown", () => {
    const setMessage = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "model",
        message: "",
        activeChatSessionID: "chat_history",
        activeChatSession: {
          id: "chat_history",
          title: "History",
          messages: [
            { id: "u1", role: "user", content: "first prompt", created_at: "2026-05-01T10:00:00Z" },
            {
              id: "a1",
              role: "assistant",
              content: "first answer",
              created_at: "2026-05-01T10:00:01Z",
            },
            {
              id: "u2",
              role: "user",
              content: "second prompt",
              created_at: "2026-05-01T10:00:02Z",
            },
          ],
          provider_calls: [],
        },
      },
      { setMessage },
    );
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));

    let textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange(0, 0);
    fireEvent.keyDown(textarea, { key: "ArrowUp" });
    expect(setMessage).toHaveBeenLastCalledWith("second prompt");

    const latestState = { ...state, message: "second prompt" };
    rerender(withRuntimeConsole(<ChatView />, { state: latestState, actions }));
    textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange("second prompt".length, "second prompt".length);
    fireEvent.keyDown(textarea, { key: "ArrowUp" });
    expect(setMessage).toHaveBeenLastCalledWith("first prompt");

    const oldestState = { ...state, message: "first prompt" };
    rerender(withRuntimeConsole(<ChatView />, { state: oldestState, actions }));
    textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange("first prompt".length, "first prompt".length);
    fireEvent.keyDown(textarea, { key: "ArrowDown" });
    expect(setMessage).toHaveBeenLastCalledWith("second prompt");

    rerender(withRuntimeConsole(<ChatView />, { state: latestState, actions }));
    textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange("second prompt".length, "second prompt".length);
    fireEvent.keyDown(textarea, { key: "ArrowDown" });
    expect(setMessage).toHaveBeenLastCalledWith("");
  });

  it("keeps normal ArrowUp navigation inside multiline drafts", () => {
    const setMessage = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "model",
        message: "line one\nline two",
        activeChatSessionID: "chat_history",
        activeChatSession: {
          id: "chat_history",
          title: "History",
          messages: [
            {
              id: "u1",
              role: "user",
              content: "previous prompt",
              created_at: "2026-05-01T10:00:00Z",
            },
          ],
          provider_calls: [],
        },
      },
      { setMessage },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange(5, 5);
    fireEvent.keyDown(textarea, { key: "ArrowUp" });
    expect(setMessage).not.toHaveBeenCalled();
  });

  it("restores a single-line draft after browsing history", () => {
    const setMessage = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "model",
        message: "draft question",
        activeChatSessionID: "chat_history",
        activeChatSession: {
          id: "chat_history",
          title: "History",
          messages: [
            {
              id: "u1",
              role: "user",
              content: "previous prompt",
              created_at: "2026-05-01T10:00:00Z",
            },
          ],
          provider_calls: [],
        },
      },
      { setMessage },
    );
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));

    let textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange("draft question".length, "draft question".length);
    fireEvent.keyDown(textarea, { key: "ArrowUp" });
    expect(setMessage).toHaveBeenLastCalledWith("previous prompt");

    rerender(
      withRuntimeConsole(<ChatView />, {
        state: { ...state, message: "previous prompt" },
        actions,
      }),
    );
    textarea = screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
    textarea.setSelectionRange("previous prompt".length, "previous prompt".length);
    fireEvent.keyDown(textarea, { key: "ArrowDown" });
    expect(setMessage).toHaveBeenLastCalledWith("draft question");
  });
});

describe("ChatView Enter switch", () => {
  it("renders the segmented Enter/⌘+Enter or Ctrl+Enter switch", () => {
    const { state, actions } = setup();
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    // The switch is one of the toggle buttons in the input toolbar.
    const buttons = screen.getAllByRole("button");
    const labels = buttons.map((b) => b.textContent?.trim()).filter(Boolean);
    const hasEnterToggle = labels.some(
      (l) => l === "↵ to send" || /^[⌘+|Ctrl+]+↵ to send$/.test(l!),
    );
    expect(hasEnterToggle).toBe(true);
  });
});

describe("ChatView chats sidebar", () => {
  function daysAgo(days: number): string {
    const date = new Date();
    date.setDate(date.getDate() - days);
    return date.toISOString();
  }

  it("shows 'No chats yet' when chatSessions is empty", () => {
    const { state, actions } = setup({ chatTarget: "model", chatSessions: [] });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText(/No chats yet/i)).toBeTruthy();
  });

  it("renders one row per chat with title", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      chatSessions: [
        {
          id: "s1",
          title: "First chat",
          message_count: 4,
          provider_call_count: 2,
          updated_at: daysAgo(0),
        } as any,
        {
          id: "s2",
          title: "Second chat",
          message_count: 2,
          provider_call_count: 1,
          updated_at: daysAgo(10),
        } as any,
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText("Today")).toBeTruthy();
    expect(screen.getByText("Older")).toBeTruthy();
    expect(screen.getByText("First chat")).toBeTruthy();
    expect(screen.getByText("Second chat")).toBeTruthy();
  });

  it("filters chat history by title and route metadata", async () => {
    const { state, actions } = setup({
      chatTarget: "model",
      chatSessions: [
        {
          id: "s1",
          title: "Budget check",
          execution_mode: "direct_model",
          status: "completed",
          provider: "anthropic",
          message_count: 4,
          updated_at: daysAgo(0),
        } as any,
        {
          id: "s2",
          title: "Draft release notes",
          execution_mode: "direct_model",
          status: "completed",
          provider: "openai",
          message_count: 2,
          updated_at: daysAgo(0),
        } as any,
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Search chats"), "anthropic");
    expect(screen.getByText("Budget check")).toBeTruthy();
    expect(screen.queryByText("Draft release notes")).toBeNull();
  });

  it("filters agent history by adapter and status metadata", async () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      chatSessions: [
        {
          id: "a1",
          title: "Codex refactor",
          agent_id: "codex",
          status: "completed",
          message_count: 4,
          updated_at: daysAgo(0),
        } as any,
        {
          id: "a2",
          title: "Cursor repro",
          agent_id: "cursor_agent",
          status: "failed",
          message_count: 2,
          updated_at: daysAgo(0),
        } as any,
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Search chats"), "failed");
    expect(screen.getByText("Cursor repro")).toBeTruthy();
    expect(screen.queryByText("Codex refactor")).toBeNull();
  });

  it("allows renaming agent chats from the sidebar", async () => {
    const renameChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        chatSessions: [
          {
            id: "a1",
            title: "Codex refactor",
            agent_id: "codex",
            status: "completed",
            message_count: 4,
            updated_at: daysAgo(0),
          } as any,
        ],
      },
      { renameChatSession },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Rename chat Codex refactor" }));
    const input = screen.getByDisplayValue("Codex refactor");
    await user.clear(input);
    await user.type(input, "Docs cleanup{Enter}");

    expect(renameChatSession).toHaveBeenCalledWith("a1", "Docs cleanup");
  });

  it("calls selectChatSession when clicking a chat row", async () => {
    const selectChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "model",
        chatSessions: [
          { id: "s1", title: "Pick me", message_count: 0, provider_call_count: 0 } as any,
        ],
      },
      { selectChatSession },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    const user = userEvent.setup();
    await user.click(screen.getByText("Pick me"));
    expect(selectChatSession).toHaveBeenCalledWith("s1");
  });

  it("calls selectChatSession when pressing Enter or Space on a focused chat row", async () => {
    const selectChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "model",
        chatSessions: [
          { id: "s1", title: "Pick me", message_count: 0, provider_call_count: 0 } as any,
        ],
      },
      { selectChatSession },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    const user = userEvent.setup();
    const row = screen.getByRole("button", { name: /^Chat Pick me$/ });
    row.focus();
    await user.keyboard("{Enter}");
    expect(selectChatSession).toHaveBeenLastCalledWith("s1");
    await user.keyboard(" ");
    expect(selectChatSession).toHaveBeenLastCalledWith("s1");
  });
});

describe("ChatView external-agent target", () => {
  it("shows the unsandboxed external-agent reminder in agent mode only", () => {
    const { state, actions } = setup({ chatTarget: "external_agent" });
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText(/External agents run as your OS user/)).toBeTruthy();

    rerender(
      withRuntimeConsole(<ChatView />, { state: { ...state, chatTarget: "model" }, actions }),
    );
    expect(screen.queryByText(/External agents run as your OS user/)).toBeNull();
  });

  it("does not show provider setup actions when agent chat has no available CLI", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      message: "run codex",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
      agentAdapterID: "codex",
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp",
          managed: true,
          managed_package: "@zed-industries/codex-acp",
          available: false,
          status: "missing",
          error: "no local package runner found for @zed-industries/codex-acp",
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Codex is unavailable")).toBeTruthy();
    expect(screen.getByText(/could not start Codex/)).toBeTruthy();
    expect(screen.getAllByText("Codex").length).toBeGreaterThan(0);
    expect(screen.getByText(/Install Codex CLI, then sign in with Codex/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Install/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Auth/ })).toBeTruthy();
    expect(screen.getByText(/no local package runner/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Open Connections/i })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Add selected/i })).toBeNull();
  });

  it("renders external agent controls and keeps agent choice scoped to new chats", async () => {
    const setChatTarget = vi.fn();
    const setAgentAdapterID = vi.fn();
    const setNewChatAgent = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentAdapterID: "codex",
        agentWorkspace: "/tmp/hecate",
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
          {
            id: "claude_code",
            name: "Claude Code",
            kind: "acp",
            command: "claude-agent-acp",
            available: false,
            status: "missing",
            cost_mode: "external",
          },
        ],
        chatSessions: [
          {
            id: "a1",
            title: "Codex work",
            agent_id: "codex",
            workspace: "/tmp/hecate",
            status: "completed",
            message_count: 2,
          } as any,
        ],
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Codex work",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          config_options: [
            {
              id: "model",
              name: "Model",
              category: "model",
              type: "select",
              current_value: "fast",
              options: [
                { value: "fast", name: "Fast" },
                { value: "smart", name: "Smart" },
              ],
            },
            {
              id: "auto_approve",
              name: "Auto approve",
              category: "mode",
              type: "boolean",
              current_bool: false,
            },
          ],
          messages: [
            { id: "m1", role: "user", content: "review this", created_at: "2026-05-03T10:00:00Z" },
            {
              id: "m2",
              run_id: "agent_run_c4",
              request_id: "req_codex_123456",
              trace_id: "0123456789abcdef0123456789abcdef",
              role: "assistant",
              content: "Looks good.",
              raw_output: `{"sessionId":"native_codex_1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Looks good."}}}`,
              agent_id: "codex",
              agent_name: "Codex",
              driver_kind: "acp",
              native_session_id: "native_codex_1",
              status: "completed",
              cost_mode: "external",
              diff_stat:
                "README.md | 2 +-\nui/src/features/chats/ChatView.tsx | 12 +++++++---\n2 files changed, 10 insertions(+), 4 deletions(-)",
              diff: "diff --git a/README.md b/README.md",
              created_at: "2026-05-03T10:00:01Z",
              activities: [
                {
                  type: "started",
                  status: "completed",
                  title: "Starting external agent",
                  detail: "Codex ACP session started",
                },
                {
                  id: "plan:0:Inspect",
                  type: "plan",
                  status: "completed",
                  kind: "high",
                  title: "Inspect changes",
                },
                {
                  id: "plan:1:Summarize",
                  type: "plan",
                  status: "in_progress",
                  kind: "medium",
                  title: "Summarize result",
                },
                {
                  id: "tool:call_1",
                  type: "tool_call",
                  status: "completed",
                  kind: "execute",
                  title: "git diff --stat",
                  detail: "README.md:12",
                },
                { type: "completed", status: "completed", title: "Final answer" },
              ],
            },
          ],
        } as any,
      },
      {
        setChatTarget,
        setAgentAdapterID,
        setNewChatAgent,
        setChatConfigOption: vi.fn(async () => true),
      },
    );
    const onOpenTrace = vi.fn();
    render(withRuntimeConsole(<ChatView onOpenTrace={onOpenTrace} />, { state, actions }));

    expect(screen.queryByDisplayValue("/tmp/hecate")).toBeNull();
    expect(screen.getByRole("button", { name: /workspace/i })).toBeTruthy();
    expect(screen.getAllByText("Codex work").length).toBeGreaterThan(0);
    expect(screen.getByText("Codex session · Completed · /tmp/hecate")).toBeTruthy();
    expect(screen.getByLabelText("External agent message controls")).toBeTruthy();
    const modelPicker = screen.getByRole("button", { name: "Model" });
    expect(modelPicker).toHaveTextContent("Fast");
    await userEvent.click(modelPicker);
    await userEvent.click(screen.getByRole("option", { name: /Smart/ }));
    expect(actions.setChatConfigOption).toHaveBeenCalledWith("a1", "model", "smart");
    const modeToggle = screen.getByRole("button", { name: /mode: off/i });
    await userEvent.click(modeToggle);
    expect(actions.setChatConfigOption).toHaveBeenCalledWith("a1", "auto_approve", true);
    expect(screen.getByText("Looks good.")).toBeTruthy();
    expect(screen.getAllByText(/ACP native_codex/).length).toBeGreaterThan(0);
    const traceButton = screen.getByRole("button", { name: /Open Trace req_code/i });
    expect(traceButton).toBeTruthy();
    expect(screen.queryByText("Starting external agent")).toBeNull();
    expect(screen.getByText("completed · 1/2 plan · 1 tool · files changed")).toBeTruthy();
    expect(screen.getByText("Inspect changes")).toBeTruthy();
    expect(screen.getByText("Summarize result")).toBeTruthy();
    expect(screen.getByText("Ran command")).toBeTruthy();
    expect(screen.getByText("README.md:12")).toBeTruthy();
    expect(
      screen.getByText("files changed · 2 files changed, 10 insertions(+), 4 deletions(-)"),
    ).toBeTruthy();
    expect(screen.getByText("README.md")).toBeTruthy();
    expect(screen.getByText("2 +-")).toBeTruthy();
    expect(screen.getByText("ui/src/features/chats/ChatView.tsx")).toBeTruthy();
    expect(screen.getByText("12 +++++++---")).toBeTruthy();
    expect(screen.getByText("raw adapter output · 1 line")).toBeTruthy();
    expect(screen.getAllByText("completed").length).toBeGreaterThan(0);
    const user = userEvent.setup();
    await user.click(traceButton);
    expect(onOpenTrace).toHaveBeenCalledWith("req_codex_123456");
    expect(screen.getAllByText("Codex").length).toBeGreaterThan(0);
    const agentPicker = screen.getByRole("button", { name: "Choose agent for new chat" });
    expect(screen.getByRole("button", { name: "New Hecate chat" })).toBeTruthy();
    await user.click(agentPicker);
    const claudeOption = screen.getByRole("option", { name: /Claude Code/ });
    expect(claudeOption).toHaveAttribute("aria-disabled", "true");
    await user.click(claudeOption);
    expect(setAgentAdapterID).not.toHaveBeenCalled();

    const hecateOption = screen.getByRole("option", { name: /Hecate/ });
    expect(hecateOption).not.toHaveAttribute("aria-disabled", "true");
    await user.click(hecateOption);
    expect(setNewChatAgent).toHaveBeenCalledWith("hecate");
    expect(setChatTarget).not.toHaveBeenCalled();
  });

  it("allows choosing an agent before an agent chat is created", async () => {
    const setNewChatAgent = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        newChatAgentID: "codex",
        agentAdapterID: "codex",
        activeChatSessionID: "",
        activeChatSession: null,
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
          {
            id: "claude_code",
            name: "Claude Code",
            kind: "acp",
            command: "claude-agent-acp",
            available: true,
            status: "available",
            cost_mode: "external",
          },
        ],
      },
      { setNewChatAgent },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Choose agent for new chat" }));
    await user.click(screen.getByText("Claude Code"));
    expect(setNewChatAgent).toHaveBeenCalledWith("claude_code");
  });

  it("shows Claude Code setup before the first send when auth is not configured", async () => {
    const setAgentAdapterCredential = vi.fn(async () => true);
    const probeAgentAdapter = vi.fn(async () => null);
    const onNavigate = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentAdapterID: "claude_code",
        agentWorkspace: "/tmp/hecate",
        message: "inspect repo",
        agentAdapters: [
          {
            id: "claude_code",
            name: "Claude Code",
            kind: "acp",
            command: "claude-agent-acp",
            available: true,
            status: "available",
            auth_status: "unknown",
            credential_configured: false,
            cost_mode: "external",
          },
        ],
      },
      { setAgentAdapterCredential, probeAgentAdapter },
    );
    render(withRuntimeConsole(<ChatView onNavigate={onNavigate} />, { state, actions }));

    expect(screen.getByTestId("claude-code-preflight")).toBeTruthy();
    expect(screen.getByText("Set up Claude Code")).toBeTruthy();
    expect(screen.getByText(/adapter-visible setup token before Hecate can start/)).toBeTruthy();
    expect(document.querySelector("button[type='submit']")).toBeNull();

    const user = userEvent.setup();
    const installCommand = screen.getByRole("button", {
      name: /npx -y @anthropic-ai\/claude-code --version/i,
    });
    expect(installCommand).toBeTruthy();
    await user.type(screen.getByLabelText("Claude Code setup token"), "claude-token");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(setAgentAdapterCredential).toHaveBeenCalledWith(
      "claude_code",
      "claude-token",
      "CLAUDE_CODE_OAUTH_TOKEN",
    );
    expect(probeAgentAdapter).toHaveBeenCalledWith("claude_code");
    await user.click(screen.getByRole("button", { name: "Check auth" }));
    expect(probeAgentAdapter).toHaveBeenCalledWith("claude_code");
    expect(screen.queryByRole("button", { name: "Open Settings" })).toBeNull();
    expect(onNavigate).not.toHaveBeenCalled();
  });

  it("uses the centered Claude Code setup and hides the composer for existing empty sessions", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      agentWorkspace: "/tmp/hecate",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        title: "Claude work",
        agent_id: "claude_code",
        agent_name: "Claude Code",
        workspace: "/tmp/hecate",
        messages: [],
        status: "idle",
        turns_used: 0,
        max_turns_per_session: 0,
      },
      agentAdapters: [
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-agent-acp",
          available: true,
          status: "available",
          auth_status: "unknown",
          credential_configured: false,
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Set up Claude Code")).toBeTruthy();
    expect(screen.getAllByTestId("claude-code-preflight")).toHaveLength(1);
    expect(document.querySelector("button[type='submit']")).toBeNull();
    expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
  });

  it("shows Claude Code install as complete when the CLI is already present", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-agent-acp",
          available: true,
          status: "available",
          auth_status: "unknown",
          credential_configured: false,
          cost_mode: "external",
          claude_code_cli: {
            available: true,
            command: "/opt/homebrew/bin/claude",
            executable_path: "/opt/homebrew/bin/claude",
          },
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Available via /opt/homebrew/bin/claude")).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "npx -y @anthropic-ai/claude-code --version" }),
    ).toBeNull();
  });

  it("keeps Claude Code setup visible until the adapter probe verifies auth", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-agent-acp",
          available: true,
          status: "available",
          credential_configured: true,
          cost_mode: "external",
        },
      ],
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByTestId("claude-code-preflight")).toBeTruthy();
    expect(screen.getByText(/adapter-visible setup token/i)).toBeTruthy();
  });

  it("keeps Claude Code setup visible when only the standalone CLI is signed in", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-agent-acp",
          available: true,
          status: "available",
          auth_status: "ok",
          cost_mode: "external",
        },
      ],
      agentAdapterHealthByID: new Map([
        [
          "claude_code",
          { adapter_id: "claude_code", status: "ready", stage: "ready", duration_ms: 120 },
        ],
      ]),
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByTestId("claude-code-preflight")).toBeTruthy();
    expect(screen.getByText("adapter installed")).toBeTruthy();
    expect(screen.getByText("token not saved")).toBeTruthy();
    expect(screen.getByText("CLI signed in")).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: /message/i })).toBeNull();
  });

  it("hides Claude Code setup after the adapter probe verifies auth", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "claude_code",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-agent-acp",
          available: true,
          status: "available",
          credential_configured: true,
          cost_mode: "external",
        },
      ],
      agentAdapterHealthByID: new Map([
        [
          "claude_code",
          { adapter_id: "claude_code", status: "ready", stage: "ready", duration_ms: 120 },
        ],
      ]),
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByTestId("claude-code-preflight")).toBeNull();
  });

  it("shows a waiting state for a running agent before transcript output arrives", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
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
      activeChatSessionID: "a1",
      activeChatSession: {
        id: "a1",
        title: "Running work",
        agent_id: "codex",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [
          { id: "m1", role: "user", content: "status", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "",
            agent_id: "codex",
            agent_name: "Codex",
            status: "running",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              {
                type: "running",
                status: "running",
                title: "Running",
                detail: "Waiting for ACP output",
              },
            ],
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Waiting for agent output...")).toBeTruthy();
    expect(screen.getAllByText("running").length).toBeGreaterThan(0);
  });

  it("shows transient agent narration as live assistant text while a run is active", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
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
      activeChatSessionID: "a1",
      activeChatSession: {
        id: "a1",
        title: "Inspect diff",
        agent_id: "codex",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [
          { id: "m1", role: "user", content: "show diff", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content:
              "I’ll check the current worktree diff and summarize the changed files plus the important hunks.",
            agent_id: "codex",
            agent_name: "Codex",
            status: "running",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              {
                type: "running",
                status: "running",
                title: "Running",
                detail: "Waiting for ACP output",
              },
            ],
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(
      screen.getByText(
        "I’ll check the current worktree diff and summarize the changed files plus the important hunks.",
      ),
    ).toBeTruthy();
    expect(
      screen
        .getByText(
          "I’ll check the current worktree diff and summarize the changed files plus the important hunks.",
        )
        .parentElement?.querySelector("[aria-hidden='true']"),
    ).toBeTruthy();
    expect(screen.queryByText("Waiting for agent output...")).toBeNull();
  });

  it("renders adapter-reported usage below completed agent messages and in chat settings", async () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
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
      activeChatSessionID: "a1",
      activeChatSession: {
        id: "a1",
        title: "Usage check",
        agent_id: "codex",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          { id: "m1", role: "user", content: "status", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "Done.",
            agent_id: "codex",
            agent_name: "Codex",
            status: "completed",
            created_at: "2026-05-03T10:00:01Z",
            usage: {
              context_size: 200000,
              context_used: 42000,
              reported_cost_amount: "0.1234",
              reported_cost_currency: "USD",
            },
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("0.1234 USD")).toBeTruthy();
    expect(screen.getByText("42000/200000 context")).toBeTruthy();
    expect(screen.getByText("reported by adapter · not enforced by Hecate")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Chat settings" }));
    expect(screen.getByText("Reported usage")).toBeTruthy();
    expect(screen.getByText("42,000 / 200,000")).toBeTruthy();
    expect(screen.getAllByText("0.1234 USD").length).toBeGreaterThan(1);
    expect(screen.getByText(/Hecate does not enforce external-agent billing/i)).toBeTruthy();
  });

  it("renders Hecate-measured usage in chat settings", async () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      agentWorkspace: "/tmp/hecate",
      activeChatSessionID: "h1",
      activeChatSession: {
        id: "h1",
        execution_mode: "hecate_task",
        title: "Hecate work",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          { id: "m1", role: "user", content: "status", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "Done.",
            status: "completed",
            provider: "ollama",
            model: "qwen2.5-coder",
            created_at: "2026-05-03T10:00:01Z",
            usage: {
              context_size: 128000,
              context_used: 16000,
              reported_cost_amount: "0.002",
              reported_cost_currency: "USD",
            },
          },
        ],
      } as any,
      model: "qwen2.5-coder",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    await userEvent.click(screen.getByRole("button", { name: "Chat settings" }));

    expect(screen.getByText("Usage")).toBeTruthy();
    expect(screen.getByText("16,000 / 128,000")).toBeTruthy();
    expect(screen.getAllByText("0.002 USD").length).toBeGreaterThan(0);
    expect(screen.getByText(/Measured by Hecate/i)).toBeTruthy();
  });

  it("loads changed files, inspects a file diff, and confirms per-file revert", async () => {
    const listChatMessageFiles = vi.fn(async () => [
      { path: "README.md", additions: 2, deletions: 1, status: "modified" },
      { path: "docs/runtime-api.md", additions: 4, deletions: 0, status: "added" },
    ]);
    const getChatMessageFileDiff = vi.fn(async () => ({
      path: "README.md",
      additions: 2,
      deletions: 1,
      status: "modified",
      diff: "diff --git a/README.md b/README.md\n+new line",
    }));
    const revertChatMessageFiles = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review files",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [
            { id: "m1", role: "user", content: "change docs", created_at: "2026-05-03T10:00:00Z" },
            {
              id: "m2",
              role: "assistant",
              content: "Updated the docs.",
              agent_id: "codex",
              agent_name: "Codex",
              status: "completed",
              diff_stat:
                "README.md | 3 ++-\ndocs/runtime-api.md | 4 ++++\n2 files changed, 6 insertions(+), 1 deletion(-)",
              diff: "diff --git a/README.md b/README.md\n+new line",
              created_at: "2026-05-03T10:00:01Z",
            },
          ],
        } as any,
      },
      { listChatMessageFiles, getChatMessageFileDiff, revertChatMessageFiles },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(
      screen.getByText("files changed · 2 files changed, 6 insertions(+), 1 deletion(-)"),
    );

    expect(await screen.findByText("2 changed files")).toBeTruthy();
    expect(listChatMessageFiles).toHaveBeenCalledWith("a1", "m2");

    await user.click(screen.getByRole("button", { name: "Inspect README.md" }));
    expect(getChatMessageFileDiff).toHaveBeenCalledWith("a1", "m2", "README.md");
    expect(await screen.findByText("diff · README.md")).toBeTruthy();
    expect(document.body.textContent).toContain("+new line");

    await user.click(screen.getByRole("button", { name: "Revert README.md" }));
    expect(revertChatMessageFiles).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Confirm revert README.md" }));
    expect(revertChatMessageFiles).toHaveBeenCalledWith("a1", "m2", ["README.md"]);
  });

  it("surfaces diff-review API failures and clears loading states", async () => {
    const listChatMessageFiles = vi.fn(async () => [
      { path: "README.md", additions: 2, deletions: 1, status: "modified" },
    ]);
    const getChatMessageFileDiff = vi.fn(async () => {
      throw new Error("diff unavailable");
    });
    const revertChatMessageFiles = vi.fn(async () => {
      throw new Error("git restore failed");
    });
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review files",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [
            { id: "m1", role: "user", content: "change docs", created_at: "2026-05-03T10:00:00Z" },
            {
              id: "m2",
              role: "assistant",
              content: "Updated the docs.",
              agent_id: "codex",
              agent_name: "Codex",
              status: "completed",
              diff_stat: "README.md | 3 ++-\n1 file changed, 2 insertions(+), 1 deletion(-)",
              diff: "diff --git a/README.md b/README.md\n+new line",
              created_at: "2026-05-03T10:00:01Z",
            },
          ],
        } as any,
      },
      { listChatMessageFiles, getChatMessageFileDiff, revertChatMessageFiles },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(
      screen.getByText("files changed · 1 file changed, 2 insertions(+), 1 deletion(-)"),
    );
    expect(await screen.findByText("1 changed file")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Inspect README.md" }));
    expect(await screen.findByText("Could not load that file diff.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Inspect README.md" })).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Revert README.md" }));
    await user.click(screen.getByRole("button", { name: "Confirm revert README.md" }));
    expect(
      await screen.findByText(
        "Revert failed. The workspace may not be a Git repository, or the file changed since capture.",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "Revert README.md" })).toBeTruthy();
  });

  it("surfaces changed-file list failures", async () => {
    const listChatMessageFiles = vi.fn(async () => {
      throw new Error("files unavailable");
    });
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review files",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [
            {
              id: "m2",
              role: "assistant",
              content: "Updated the docs.",
              agent_id: "codex",
              agent_name: "Codex",
              status: "completed",
              diff_stat: "README.md | 3 ++-\n1 file changed, 2 insertions(+), 1 deletion(-)",
              diff: "diff --git a/README.md b/README.md\n+new line",
              created_at: "2026-05-03T10:00:01Z",
            },
          ],
        } as any,
      },
      {
        listChatMessageFiles,
        getChatMessageFileDiff: vi.fn(async () => null),
        revertChatMessageFiles: vi.fn(async () => false),
      },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(
      screen.getByText("files changed · 1 file changed, 2 insertions(+), 1 deletion(-)"),
    );
    expect(
      await screen.findByText(
        "Could not load changed files. The captured diff may no longer be available.",
      ),
    ).toBeTruthy();
    expect(screen.queryByText("Loading changed files...")).toBeNull();
  });

  it("requires confirmation before reverting the full captured diff", async () => {
    const listChatMessageFiles = vi.fn(async () => [
      { path: "README.md", additions: 2, deletions: 1, status: "modified" },
    ]);
    const revertChatMessageFiles = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "/tmp/hecate",
        activeChatSessionID: "a1",
        activeChatSession: {
          id: "a1",
          title: "Review all",
          agent_id: "codex",
          workspace: "/tmp/hecate",
          status: "completed",
          messages: [
            { id: "m1", role: "user", content: "change docs", created_at: "2026-05-03T10:00:00Z" },
            {
              id: "m2",
              role: "assistant",
              content: "Updated the docs.",
              agent_id: "codex",
              agent_name: "Codex",
              status: "completed",
              diff_stat: "README.md | 3 ++-\n1 file changed, 2 insertions(+), 1 deletion(-)",
              diff: "diff --git a/README.md b/README.md",
              created_at: "2026-05-03T10:00:01Z",
            },
          ],
        } as any,
      },
      { listChatMessageFiles, revertChatMessageFiles },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(
      screen.getByText("files changed · 1 file changed, 2 insertions(+), 1 deletion(-)"),
    );
    expect(await screen.findByText("1 changed file")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Revert all" }));
    expect(revertChatMessageFiles).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Confirm revert all" }));
    expect(revertChatMessageFiles).toHaveBeenCalledWith("a1", "m2", []);
  });

  it("disables stop and shows cancelling feedback after stop is requested", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      chatLoading: true,
      chatCancelling: true,
      agentWorkspace: "/tmp/hecate",
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
      activeChatSessionID: "a1",
      activeChatSession: {
        id: "a1",
        title: "Stopping work",
        agent_id: "codex",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const stop = screen.getByRole("button", { name: "Stop external agent" }) as HTMLButtonElement;
    expect(stop.disabled).toBe(true);
    expect(stop.title).toBe("Stopping...");
    expect(screen.getByText("Stopping...")).toBeTruthy();
  });

  it("renders failed agent runs as an error notice with raw diagnostics separate", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        {
          id: "claude_code",
          name: "Claude Code",
          kind: "acp",
          command: "claude-agent-acp",
          available: true,
          status: "available",
          cost_mode: "external",
        },
      ],
      activeChatSessionID: "a1",
      activeChatSession: {
        id: "a1",
        title: "Failed work",
        agent_id: "claude_code",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "failed",
        messages: [
          { id: "m1", role: "user", content: "status", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "Claude Code usage limit: credit balance is too low",
            raw_output: `{"code":-32603,"message":"Internal error: Credit balance is too low"}`,
            error: "Claude Code usage limit: credit balance is too low",
            agent_id: "claude_code",
            agent_name: "Claude Code",
            status: "failed",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              {
                type: "failed",
                status: "failed",
                title: "Failed",
                detail: "Claude Code usage limit: credit balance is too low",
              },
            ],
          },
        ],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("agent run failed")).toBeTruthy();
    expect(
      screen.getAllByText("Claude Code usage limit: credit balance is too low").length,
    ).toBeGreaterThan(0);
    expect(screen.getByText("raw adapter output · 1 line")).toBeTruthy();
    expect(screen.getAllByText("failed").length).toBeGreaterThan(0);
  });

  it("opens the workspace picker action from the folder button", async () => {
    const chooseAgentWorkspace = vi.fn(async () => true);
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "",
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
      },
      { chooseAgentWorkspace },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByTitle("Choose workspace folder"));
    expect(chooseAgentWorkspace).toHaveBeenCalled();
  });

  it("allows pasting a workspace path when the folder dialog is unavailable", async () => {
    const chooseAgentWorkspace = vi.fn(async () => false);
    const setAgentWorkspace = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        agentWorkspace: "",
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
      },
      { chooseAgentWorkspace, setAgentWorkspace },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByTitle("Choose workspace folder"));
    await user.type(screen.getByPlaceholderText("/Users/alice/dev/project"), "/workspaces/hecate");
    await user.click(screen.getByRole("button", { name: "Use" }));

    expect(setAgentWorkspace).toHaveBeenCalledWith("/workspaces/hecate");
  });

  it("requires a workspace before sending to an external agent", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      message: "run codex",
      agentWorkspace: "",
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
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });

  it("explains why Hecate Chat cannot send with tools before workspace selection", async () => {
    const chooseAgentWorkspace = vi.fn(async () => "/tmp/hecate");
    const { state, actions } = setup(
      {
        chatTarget: "agent",
        message: "inspect repo",
        agentWorkspace: "",
        providerScopedModels: [
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: {
              provider: "openai",
              provider_kind: "cloud",
              capabilities: { tool_calling: "basic", streaming: true, source: "catalog" },
            },
          },
        ],
      },
      { chooseAgentWorkspace },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText(/Hecate uses the workspace as the working directory/)).toBeTruthy();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Choose workspace" }));
    expect(chooseAgentWorkspace).toHaveBeenCalled();
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });
});

describe("ChatView model target", () => {
  it("announces markdown task-list checkbox state", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      activeChatSessionID: "s1",
      activeChatSession: {
        id: "s1",
        title: "Tasks",
        messages: [{ id: "m1", sequence: 1, role: "assistant", content: "- [x] done\n- [ ] todo" }],
        provider_calls: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByRole("img", { name: "Completed task" })).toBeTruthy();
    expect(screen.getByRole("img", { name: "Incomplete task" })).toBeTruthy();
  });

  it("keeps provider and model pickers editable for an active model chat", async () => {
    const setProviderFilter = vi.fn();
    const setModel = vi.fn();
    const { state, actions } = setup(
      {
        chatTarget: "model",
        providerFilter: "openai",
        model: "gpt-4o-mini",
        activeChatSessionID: "s1",
        activeChatSession: {
          id: "s1",
          title: "Model switching",
          messages: [],
          provider_calls: [],
        } as any,
        settingsConfig: {
          providers: [
            { id: "anthropic", name: "Anthropic", kind: "cloud", credential_configured: true },
            { id: "openai", name: "OpenAI", kind: "cloud", credential_configured: true },
          ],
        } as any,
        providerPresets: [
          { id: "anthropic", name: "Anthropic", kind: "cloud" },
          { id: "openai", name: "OpenAI", kind: "cloud" },
        ] as any,
        providerScopedModels: [
          {
            id: "claude-sonnet-4-20250514",
            owned_by: "anthropic",
            metadata: { provider: "anthropic", provider_kind: "cloud" },
          },
          {
            id: "gpt-4o-mini",
            owned_by: "openai",
            metadata: { provider: "openai", provider_kind: "cloud" },
          },
          {
            id: "gpt-4.1-mini",
            owned_by: "openai",
            metadata: { provider: "openai", provider_kind: "cloud" },
          },
        ],
      },
      { setProviderFilter, setModel },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    const providerPicker = screen.getByRole("button", { name: /OpenAI/i }) as HTMLButtonElement;
    expect(providerPicker.disabled).toBe(false);
    await user.click(providerPicker);
    await user.click(screen.getByText("Anthropic"));
    expect(setProviderFilter).toHaveBeenCalledWith("anthropic");

    const modelPicker = screen.getByRole("button", { name: /gpt-4o-mini/i }) as HTMLButtonElement;
    expect(modelPicker.disabled).toBe(false);
    await user.click(modelPicker);
    await user.click(screen.getByText("gpt-4.1-mini"));
    expect(setModel).toHaveBeenCalledWith("gpt-4.1-mini");
  });
});

describe("ChatView error display", () => {
  it("renders chatError using InlineError styling", () => {
    const { state, actions } = setup({ chatError: "Provider returned 500" });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText(/Provider returned 500/)).toBeTruthy();
  });

  it("renders operator guidance for stable gateway error codes", () => {
    const openTrace = vi.fn();
    const { state, actions } = setup({
      chatError: "Incorrect API key provided",
      chatErrorAction: "Rotate the provider key in Connections, then test readiness again.",
      chatErrorCode: "provider_auth_failed",
      chatErrorRequestID: "req_1234567890abcdef",
      chatErrorStatus: 502,
      chatErrorTraceID: "trace_abcdef1234567890",
    });
    render(withRuntimeConsole(<ChatView onOpenTrace={openTrace} />, { state, actions }));
    expect(screen.getByText("Provider credentials failed")).toBeTruthy();
    expect(screen.getByText("502 · provider_auth_failed")).toBeTruthy();
    expect(screen.getByText(/Rotate the provider key in Connections/)).toBeTruthy();
    expect(screen.getByText("req_123456")).toBeTruthy();
    expect(screen.getByText("trace_abcd")).toBeTruthy();
    screen.getByRole("button", { name: "Open trace" }).click();
    expect(openTrace).toHaveBeenCalledWith("req_1234567890abcdef");
  });
});

describe("ChatView session title", () => {
  it("shows the chat empty state and composer when no chats exist", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      chatSessions: [],
      activeChatSessionID: "",
      activeChatSession: null,
      message: "",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    // New users land directly on the chat canvas with its empty
    // state + composer, not a passive "pick something first" panel.
    // (Sidebar still shows "No chats yet" — that's a different surface.)
    expect(screen.queryByText(/Start your first .* chat from the sidebar/)).toBeNull();
    expect(screen.queryByRole("textbox", { name: "Message" })).not.toBeNull();
  });

  it("shows a draft chat canvas on start when chat history exists but none is active", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      chatSessions: [
        {
          id: "s1",
          title: "Previous chat",
          message_count: 2,
          provider_call_count: 0,
          updated_at: "2026-05-18T00:00:00Z",
        } as any,
      ],
      activeChatSessionID: "",
      activeChatSession: null,
      message: "",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByText("No chat selected")).toBeNull();
    expect(screen.getByText("New chat")).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "Message" })).not.toBeNull();
  });

  it("shows the active session's title", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      activeChatSession: {
        id: "s1",
        title: "Hello world",
        messages: [],
        provider_calls: [],
      } as any,
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByText("Hello world")).toBeTruthy();
  });

  it("shows the active chat runtime identity below the title", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      providerFilter: "ollama",
      model: "qwen2.5-coder",
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
        ],
        policy_rules: [],
        events: [],
      },
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        execution_mode: "hecate_task",
        title: "Repo work",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/Users/alice/dev/hecate",
        status: "completed",
        messages: [],
      } as any,
    });

    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.getByText("Repo work")).toBeTruthy();
    expect(screen.getByText("Tools on · /Users/alice/dev/hecate")).toBeTruthy();
  });
});

describe("ChatView New chat button", () => {
  it("focuses the message textarea after clicking New chat", async () => {
    // The button starts a fresh chat; the operator's next move
    // is almost always to type. Auto-focusing the textarea saves a
    // click and matches the muscle-memory pattern from chat clients.
    const createChatSession = vi.fn();
    const { state, actions } = setup({}, { createChatSession });
    const user = userEvent.setup();
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    await user.click(screen.getByRole("button", { name: /new .* chat/i }));
    expect(createChatSession).toHaveBeenCalled();
    const textarea = screen.getByPlaceholderText(/^Message…/i);
    await waitFor(() => expect(document.activeElement).toBe(textarea));
  });
});

describe("ChatView session focus", () => {
  it("focuses the message textarea when a sidebar chat row is clicked", async () => {
    // Focus is applied on EXPLICIT user actions only — the New-chat
    // button onClick and chat-row onClick. The activeChatSessionID
    // effect deliberately does NOT focus, because data-load (chats
    // arriving from the API) also drives that transition and stealing
    // focus on load would hijack normal page navigation.
    const selectChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "model",
        chatSessions: [
          { id: "s2", title: "Pick me", message_count: 0, provider_call_count: 0 } as any,
        ],
      },
      { selectChatSession },
    );
    const user = userEvent.setup();
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));
    // Move focus elsewhere to detect the jump.
    const searchInput = screen.getByRole("textbox", { name: "Search chats" });
    searchInput.focus();
    expect(document.activeElement).toBe(searchInput);
    // Click the chat row — the only user-driven chat switch.
    await user.click(screen.getByText("Pick me"));
    const nextState = setup({
      ...state,
      activeChatSessionID: "s2",
      activeChatSession: { id: "s2", title: "Pick me", messages: [], provider_calls: [] } as any,
    }).state;
    rerender(withRuntimeConsole(<ChatView />, { state: nextState, actions }));
    const textarea = screen.getByPlaceholderText(/^Message…/i);
    await waitFor(() => expect(document.activeElement).toBe(textarea));
    expect(selectChatSession).toHaveBeenCalledWith("s2");
  });

  it("does NOT focus the textarea when activeChatSessionID changes from data-load", async () => {
    // Initial-load and API-driven session arrivals must not steal
    // focus — page-level shortcuts depend on it. Asserts the negative.
    const { state, actions } = setup({ chatTarget: "model", activeChatSessionID: "" });
    const { rerender } = render(withRuntimeConsole(<ChatView />, { state, actions }));
    const searchInput = screen.getByRole("textbox", { name: "Search chats" });
    searchInput.focus();
    const next = { ...state, activeChatSessionID: "s1" };
    rerender(withRuntimeConsole(<ChatView />, { state: next, actions }));
    // Focus must STAY on the search input — the effect should not have
    // jumped to the textarea on a programmatic ID transition.
    expect(document.activeElement).toBe(searchInput);
  });
});

describe("ChatView history pagination", () => {
  it("does not show the legacy model-history pagination action for unified Hecate Chat", () => {
    const loadMoreChatSessions = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "model",
        chatSessionsHasMore: true,
        chatSessions: [
          { id: "s1", title: "First page", message_count: 1, provider_call_count: 1 } as any,
        ],
      },
      { loadMoreChatSessions },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    expect(screen.queryByRole("button", { name: "Load earlier chats" })).toBeNull();
    expect(loadMoreChatSessions).not.toHaveBeenCalled();
  });

  it("does not show the legacy search pagination action for unified Hecate Chat", async () => {
    const loadMoreChatSessions = vi.fn(async () => undefined);
    const { state, actions } = setup(
      {
        chatTarget: "model",
        chatSessionsHasMore: true,
        chatSessions: [
          { id: "s1", title: "First page", message_count: 1, provider_call_count: 1 } as any,
        ],
      },
      { loadMoreChatSessions },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    const user = userEvent.setup();
    await user.type(screen.getByRole("textbox", { name: "Search chats" }), "older match");
    expect(screen.queryByRole("button", { name: "Search earlier chats" })).toBeNull();
    expect(loadMoreChatSessions).not.toHaveBeenCalled();
  });
});

// External-agent approval surfaces in the Chats workspace. These tests
// confirm the banner / modal wiring; the component-level behavior
// (overflow stack, broad-scope confirm) is covered in
// AgentApprovalBanner.test.tsx and AgentApprovalModal.test.tsx.
describe("ChatView agent approvals", () => {
  it("renders the auto-mode danger banner when the gateway runs in auto", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterApprovalMode: "auto",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.getByTestId("agent-approval-auto-banner")).toBeTruthy();
  });

  it("does not render the auto-mode banner when in prompt mode", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterApprovalMode: "prompt",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.queryByTestId("agent-approval-auto-banner")).toBeNull();
  });

  it("hides the auto-mode banner when in model chat target (it's an agent-only concern)", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      agentAdapterApprovalMode: "auto",
    });
    render(withRuntimeConsole(<ChatView />, { state, actions }));
    expect(screen.queryByTestId("agent-approval-auto-banner")).toBeNull();
  });

  it("renders the pending banner with rows scoped to the active session and opens the modal on Review", async () => {
    const sessionID = "a1";
    const pending = new Map<string, any>([
      [
        sessionID,
        [
          {
            approval_id: "ap-1",
            session_id: sessionID,
            adapter_id: "codex",
            tool_kind: "fs",
            tool_name: "write_file",
            created_at: "2026-04-21T10:00:00Z",
            expires_at: "2026-04-21T10:05:00Z",
          },
        ],
      ],
      [
        "other-session",
        [
          {
            approval_id: "ap-2",
            session_id: "other-session",
            adapter_id: "codex",
            tool_kind: "exec",
            created_at: "2026-04-21T10:00:00Z",
            expires_at: "2026-04-21T10:05:00Z",
          },
        ],
      ],
    ]);
    const getChatApproval = vi.fn(async () => null); // modal opens, fetch returns null → renders error
    const { state, actions } = setup(
      {
        chatTarget: "external_agent",
        activeChatSessionID: sessionID,
        activeChatSession: {
          id: sessionID,
          title: "S1",
          agent_id: "codex",
          workspace: "/tmp",
          status: "running",
        } as any,
        pendingApprovalsBySessionID: pending,
        chatSessions: [
          {
            id: sessionID,
            title: "S1",
            agent_id: "codex",
            status: "running",
            message_count: 0,
          } as any,
        ],
      },
      { getChatApproval },
    );
    render(withRuntimeConsole(<ChatView />, { state, actions }));

    // Only the active session's pending row is visible — banner must
    // not bleed approvals from other sessions.
    const reviews = screen.getAllByTestId("agent-approval-banner-review");
    expect(reviews).toHaveLength(1);

    const user = userEvent.setup();
    await user.click(reviews[0]!);
    // The modal mounts and asks for the full row.
    expect(getChatApproval).toHaveBeenCalledWith(sessionID, "ap-1");
  });

  it("does not carry an external approval modal into Hecate Chat", async () => {
    const sessionID = "external-approval-session";
    const pending = new Map<string, any>([
      [
        sessionID,
        [
          {
            approval_id: "ap-external",
            session_id: sessionID,
            adapter_id: "codex",
            tool_kind: "fs",
            tool_name: "write_file",
            created_at: "2026-04-21T10:00:00Z",
            expires_at: "2026-04-21T10:05:00Z",
          },
        ],
      ],
    ]);
    const getChatApproval = vi.fn(async () => null);
    const { state: externalState, actions } = setup(
      {
        chatTarget: "external_agent",
        activeChatSessionID: sessionID,
        activeChatSession: {
          id: sessionID,
          title: "Codex",
          agent_id: "codex",
          workspace: "/tmp",
          status: "running",
        } as any,
        pendingApprovalsBySessionID: pending,
        chatSessions: [
          {
            id: sessionID,
            title: "Codex",
            agent_id: "codex",
            status: "running",
            message_count: 0,
          } as any,
        ],
      },
      { getChatApproval },
    );
    const view = render(withRuntimeConsole(<ChatView />, { state: externalState, actions }));

    const user = userEvent.setup();
    await user.click(screen.getByTestId("agent-approval-banner-review"));
    await waitFor(() => expect(getChatApproval).toHaveBeenCalledWith(sessionID, "ap-external"));

    const { state: hecateState } = setup(
      {
        chatTarget: "agent",
        activeChatSessionID: "hecate-session",
        activeChatSession: {
          id: "hecate-session",
          title: "Hecate",
          execution_mode: "hecate_task",
          workspace: "/tmp",
          status: "completed",
        } as any,
        pendingApprovalsBySessionID: pending,
      },
      { getChatApproval },
    );
    view.rerender(withRuntimeConsole(<ChatView />, { state: hecateState, actions }));

    expect(getChatApproval).toHaveBeenCalledTimes(1);
  });
});

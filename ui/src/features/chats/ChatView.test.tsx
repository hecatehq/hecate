import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ChatView } from "./ChatView";
import { discoverLocalProviders } from "../../lib/api";
import { createRuntimeConsoleActions, createRuntimeConsoleFixture } from "../../test/runtime-console-fixture";

vi.mock("../../lib/api", async importOriginal => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    discoverLocalProviders: vi.fn(async () => ({ object: "local_provider_discovery", data: [] })),
  };
});

function setup(stateOverrides = {}, actionOverrides = {}) {
  const state = createRuntimeConsoleFixture({
    providerScopedModels: [
      { id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } },
    ],
    ...stateOverrides,
  });
  if (state.chatTarget === "model") {
    if ((state.agentChatSessions ?? []).length === 0 && (state.chatSessions ?? []).length > 0) {
      state.agentChatSessions = state.chatSessions.map((session: any) => ({
        id: session.id,
        title: session.title,
        runtime_kind: "model" as const,
        workspace: "",
        provider: session.last_provider,
        model: session.last_model,
        status: "completed",
        message_count: session.message_count,
        created_at: session.created_at,
        updated_at: session.updated_at,
      }));
    }
    if (!state.activeAgentChatSession && state.activeChatSession) {
      state.activeAgentChatSession = {
        id: state.activeChatSession.id,
        title: state.activeChatSession.title,
        runtime_kind: "model",
        status: "completed",
        messages: (state.activeChatSession.messages ?? []).map((message: any) => ({
          id: message.id,
          role: message.role,
          content: message.content,
          created_at: message.created_at,
          runtime_kind: "model",
        })),
      } as any;
    }
    if (!state.activeAgentChatSessionID && state.activeChatSessionID) {
      state.activeAgentChatSessionID = state.activeChatSessionID;
    }
  }
  const actions = { ...createRuntimeConsoleActions(), ...actionOverrides };
  return { state, actions };
}

describe("ChatView input", () => {
  it("renders Hecate Chat as the first and active chat target by default", () => {
    const { state, actions } = setup();
    render(<ChatView state={state} actions={actions} />);
    const targetButtons = screen.getAllByRole("button", { name: /^(Hecate Chat|External Agent)$/ });
    expect(targetButtons.map((button) => button.textContent)).toEqual(["Hecate Chat", "External Agent"]);
    expect(targetButtons[0]).toHaveStyle({ color: "var(--teal)" });
  });

  it("toggles Hecate Chat between direct model chat and tool-backed agent mode", async () => {
    const setChatTarget = vi.fn();
    const { state, actions } = setup({
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
    }, { setChatTarget });
    render(<ChatView state={state} actions={actions} />);

    const toolsGroup = screen.getByRole("group", { name: "Hecate tools" });
    expect(toolsGroup).toHaveStyle({ height: "30px" });
    expect(screen.getByRole("button", { name: /tools off/i })).toHaveStyle({ width: "76px" });
    expect(screen.getByRole("button", { name: /tools on/i })).toHaveStyle({ width: "76px" });

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /tools off/i }));
    expect(setChatTarget).toHaveBeenCalledWith("model");

    await user.click(screen.getByRole("button", { name: /tools on/i }));
    expect(setChatTarget).toHaveBeenCalledWith("agent");
  });

  it("disables the send button when message is empty", () => {
    const { state, actions } = setup({ message: "" });
    render(<ChatView state={state} actions={actions} />);
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });

  it("enables the send button when message has content", () => {
    const { state, actions } = setup({ chatTarget: "model", message: "hello" });
    render(<ChatView state={state} actions={actions} />);
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(false);
  });

  it("hides Hecate Chat composer until a model is selected", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      model: "",
      message: "hello",
    });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Send message" })).toBeNull();
  });

  it("hides model composer when no provider is configured", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      message: "hello",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], pricebook: [], events: [] },
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
    });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText("No routable model")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Add provider/i })).toBeTruthy();
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
          { id: "ollama", name: "Ollama", preset_id: "ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", credential_configured: false },
        ],
        policy_rules: [],
        pricebook: [],
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
        { id: "qwen2.5:7b", owned_by: "ollama", metadata: { provider: "ollama", provider_kind: "local" } },
      ],
    });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getAllByText("Selected model is not available from this provider").length).toBeGreaterThan(0);
    expect(screen.getAllByText(/Ollama is configured, but it does not currently report "llama3.1:8b"/).length).toBeGreaterThan(0);
    expect(screen.getAllByText("Selected model").length).toBeGreaterThan(0);
    expect(screen.getAllByText("llama3.1:8b").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Discovered models").length).toBeGreaterThan(0);
    expect(screen.getAllByText("1").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Health").length).toBeGreaterThan(0);
    expect(screen.getAllByText("degraded").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Blocked by").length).toBeGreaterThan(0);
    expect(screen.getAllByText("no discovered route").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Last error").length).toBeGreaterThan(0);
    expect(screen.getAllByText("model discovery returned no llama3.1:8b").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Start the local provider app or server.").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Pull or load llama3.1:8b in that provider, or pick one of its discovered models.").length).toBeGreaterThan(0);
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
      activeAgentChatSessionID: "chat_1",
      activeAgentChatSession: {
        id: "chat_1",
        title: "Existing chat",
        runtime_kind: "model",
        status: "completed",
        provider: "ollama",
        model: "llama3.1:8b",
        messages: [
          { id: "m1", role: "user", content: "hi", runtime_kind: "model", created_at: "2026-04-20T00:00:00Z" },
        ],
      },
      settingsConfig: {
        backend: "memory",
        providers: [
          { id: "ollama", name: "Ollama", preset_id: "ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", credential_configured: false },
        ],
        policy_rules: [],
        pricebook: [],
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
        { id: "qwen2.5:7b", owned_by: "ollama", metadata: { provider: "ollama", provider_kind: "local" } },
      ],
    });
    render(<ChatView state={state} actions={actions} onNavigate={onNavigate} />);

    expect(screen.getByText("Selected model is not available from this provider")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Open Providers" })).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Open Providers" }));
    expect(onNavigate).toHaveBeenCalledWith("providers");
  });

  it("opens the shared Add provider modal from the model empty state", async () => {
    const { state, actions } = setup({
      chatTarget: "model",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], pricebook: [], events: [] },
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
    });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /Add provider/i }));

    expect(screen.getByRole("dialog", { name: "Add provider" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Local" })).toHaveStyle({ color: "var(--t0)" });
  });

  it("shows provider troubleshooting instead of detected-provider setup when a configured provider has no models", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      providerFilter: "ollama",
      settingsConfig: {
        backend: "memory",
        providers: [
          { id: "ollama", name: "Ollama", preset_id: "ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", credential_configured: false },
        ],
        policy_rules: [],
        pricebook: [],
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
            { name: "credentials", status: "ok", reason: "not_required", message: "No credentials are required for this provider." },
            { name: "models", status: "blocked", reason: "no_models", message: "No models were discovered and no default model is configured." },
            { name: "routing", status: "blocked", reason: "no_models", message: "Routing is blocked because no models are available." },
          ],
        },
      ],
      providerScopedModels: [],
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
    });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByText("Provider is configured")).toBeTruthy();
    expect(screen.getAllByText("Ollama").length).toBeGreaterThan(0);
    expect(screen.getByText("none discovered")).toBeTruthy();
    expect(screen.getAllByText("Credentials").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Models").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Routing").length).toBeGreaterThan(0);
    expect(screen.getByText("Discovery")).toBeTruthy();
    expect(screen.getByText("Routing is blocked because no models are available.")).toBeTruthy();
    expect(screen.getByText(/Start the local provider app/)).toBeTruthy();
    expect(screen.queryByText("Detected locally")).toBeNull();
    expect(screen.queryByRole("button", { name: /Add detected provider/i })).toBeNull();
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
    const { state, actions } = setup({
      chatTarget: "model",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], pricebook: [], events: [] },
      providerPresets: [
        { id: "ollama", name: "Ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", description: "" },
        { id: "lmstudio", name: "LM Studio", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:1234/v1", description: "" },
      ],
      providerScopedModels: [],
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
    }, { createProvider, loadDashboard });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    const quickAdd = await screen.findByRole("button", { name: /Add detected providers/i });
    expect(screen.getByText("Ollama")).toBeTruthy();
    expect(screen.getByText("LM Studio")).toBeTruthy();
    await user.click(quickAdd);

    expect(createProvider).toHaveBeenNthCalledWith(1, expect.objectContaining({
      name: "Ollama",
      preset_id: "ollama",
      base_url: "http://127.0.0.1:11434/v1",
      kind: "local",
      protocol: "openai",
    }), { refresh: false });
    expect(createProvider).toHaveBeenNthCalledWith(2, expect.objectContaining({
      name: "LM Studio",
      preset_id: "lmstudio",
      base_url: "http://127.0.0.1:1234/v1",
      kind: "local",
      protocol: "openai",
    }), { refresh: false });
    expect(loadDashboard).toHaveBeenCalledTimes(1);
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
    const { state, actions } = setup({
      chatTarget: "agent",
      agentWorkspace: "/tmp/hecate",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], pricebook: [], events: [] },
      providerPresets: [
        { id: "ollama", name: "Ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", description: "" },
      ],
      providerScopedModels: [],
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
    }, { createProvider, loadDashboard });
    render(<ChatView state={state} actions={actions} />);

    expect(await screen.findByText("Detected locally")).toBeTruthy();
    expect(screen.getByText("Ollama")).toBeTruthy();

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /Add detected provider/i }));

    expect(createProvider).toHaveBeenCalledWith(expect.objectContaining({
      name: "Ollama",
      preset_id: "ollama",
      base_url: "http://127.0.0.1:11434/v1",
    }), { refresh: false });
    expect(loadDashboard).toHaveBeenCalledTimes(1);
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
    const { state, actions } = setup({
      chatTarget: "model",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], pricebook: [], events: [] },
      providerPresets: [
        { id: "llamacpp", name: "llama.cpp", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:8080/v1", description: "" },
        { id: "localai", name: "LocalAI", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:8080/v1", description: "" },
      ],
      providerScopedModels: [],
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
    }, { createProvider, loadDashboard });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Add detected providers/i }));

    expect(createProvider).toHaveBeenCalledTimes(1);
    expect(createProvider).toHaveBeenCalledWith(expect.objectContaining({
      name: "llama.cpp",
      preset_id: "llamacpp",
      base_url: "http://127.0.0.1:8080/v1",
    }), { refresh: false });
    expect(loadDashboard).toHaveBeenCalledTimes(1);
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
    const { state, actions } = setup({
      chatTarget: "model",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], pricebook: [], events: [] },
      providerPresets: [
        { id: "ollama", name: "Ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", description: "" },
        { id: "lmstudio", name: "LM Studio", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:1234/v1", description: "" },
      ],
      providerScopedModels: [],
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
    }, { createProvider, loadDashboard });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: /Add detected providers/i }));

    expect(createProvider).toHaveBeenCalledTimes(2);
    expect(loadDashboard).toHaveBeenCalledTimes(1);
    expect(screen.getByText("LM Studio endpoint already exists")).toBeTruthy();
  });

  it("shows a first-run setup state when providers and agents are unavailable", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      message: "hello",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], pricebook: [], events: [] },
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: false, status: "missing", cost_mode: "external" },
      ],
    });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText("Nothing runnable yet")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Add provider/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /External Agent/i })).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Send message" })).toBeNull();
  });

  it("enables Hecate Agent when tools are not explicitly disabled for the model", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      message: "inspect this repo",
      agentWorkspace: "/tmp/hecate",
      settingsConfig: {
        backend: "memory",
        providers: [{ id: "ollama", name: "Ollama", kind: "local", credential_configured: true }],
        policy_rules: [],
        pricebook: [],
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
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByRole("button", { name: "tools on" })).toBeTruthy();
    expect(screen.getByText(/task approvals and per-call sandboxing/)).toBeTruthy();
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
        providers: [{ id: "ollama", name: "Ollama", kind: "local", credential_configured: true }],
        policy_rules: [],
        pricebook: [],
        events: [],
      },
      providerPresets: [
        { id: "ollama", name: "Ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", description: "" },
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
      activeAgentChatSessionID: "agent_chat_1",
      activeAgentChatSession: {
        id: "agent_chat_1",
        runtime_kind: "agent",
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
    render(<ChatView state={state} actions={actions} />);

    expect(screen.queryByRole("button", { name: "Fixed provider: Ollama" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Fixed model: qwen2.5-coder" })).toBeNull();
    expect(screen.getByRole("button", { name: "Model picker: smollm2:135m" })).toBeTruthy();
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
        pricebook: [],
        events: [],
      },
      providerPresets: [
        { id: "ollama", name: "Ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", description: "" },
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
      activeAgentChatSessionID: "agent_chat_1",
      activeAgentChatSession: {
        id: "agent_chat_1",
        runtime_kind: "agent",
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
    const { rerender } = render(<ChatView state={state} actions={actions} />);

    const fixedProvider = screen.getByRole("button", { name: "Fixed provider: Ollama" }) as HTMLButtonElement;
    const fixedModel = screen.getByRole("button", { name: "Fixed model: qwen2.5-coder" }) as HTMLButtonElement;
    expect(fixedProvider.disabled).toBe(true);
    expect(fixedModel.disabled).toBe(true);
    expect(screen.queryByText("smollm2:135m")).toBeNull();
    expect(screen.queryByText(/Tools are disabled for this model/)).toBeNull();
    expect(screen.getByRole("button", { name: "Queue message" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Stop active task" })).toBeTruthy();
    expect(screen.getByText(/Hecate Chat is working on this task/)).toBeTruthy();

    rerender(<ChatView state={{ ...state, chatTarget: "model" }} actions={actions} />);
    expect(document.querySelector('[aria-label="Fixed provider: Ollama"]')).toBeTruthy();
    expect(document.querySelector('[aria-label="Fixed model: qwen2.5-coder"]')).toBeTruthy();
    expect(document.querySelector('[aria-label="Model picker: smollm2:135m"]')).toBeNull();
    expect(screen.getByRole("button", { name: "Stop active task" })).toBeTruthy();
    expect(screen.getByText(/Hecate Chat is working on this task/)).toBeTruthy();
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
        pricebook: [],
        events: [],
      },
      providerPresets: [
        { id: "ollama", name: "Ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", description: "" },
      ],
      providerScopedModels: [
        {
          id: "smollm2:135m",
          owned_by: "ollama",
          metadata: { provider: "ollama", provider_kind: "local", capabilities: { tool_calling: "unknown", streaming: true, source: "provider" } },
        },
      ],
      activeAgentChatSessionID: "agent_chat_1",
      activeAgentChatSession: {
        id: "agent_chat_1",
        runtime_kind: "model",
        title: "Mixed chat",
        provider: "ollama",
        model: "smollm2:135m",
        workspace: "/tmp/hecate",
        status: "running",
        segments: [
          { id: "model:first", runtime_kind: "model", provider: "ollama", model: "smollm2:135m", status: "completed", message_count: 2 },
          { id: "task:task_hecate_123456", runtime_kind: "agent", provider: "ollama", model: "qwen2.5-coder", task_id: "task_hecate_123456", latest_run_id: "run_hecate_abcdef", status: "running", message_count: 1 },
        ],
        messages: [],
      } as any,
    });
    render(<ChatView state={state} actions={actions} onOpenTask={onOpenTask} />);

    expect(screen.getByRole("button", { name: "Fixed provider: Ollama" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Fixed model: qwen2.5-coder" })).toBeTruthy();
    expect(screen.queryByText("smollm2:135m")).toBeNull();
    expect(screen.getByRole("button", { name: "Stop active task" })).toBeTruthy();
    expect(screen.getByText(/New messages will queue until the active run finishes/)).toBeTruthy();
    screen.getByRole("button", { name: "Open task" }).click();
    expect(onOpenTask).toHaveBeenCalledWith("task_hecate_123456", "run_hecate_abcdef");
  });

  it("renders queued messages with a remove action", async () => {
    const removeQueuedChatMessage = vi.fn();
    const updateQueuedChatMessage = vi.fn();
    const user = userEvent.setup();
    const { state, actions } = setup({
      activeAgentChatSessionID: "agent_chat_1",
      queuedChatMessages: [
        {
          id: "queued_1",
          session_id: "agent_chat_1",
          content: "run tests after this",
          runtime_kind: "agent",
          provider_filter: "ollama",
          model: "qwen2.5-coder",
          workspace: "/workspace",
          system_prompt: "",
          adapter_id: "codex",
          created_at: "2026-04-20T00:00:00Z",
        },
      ],
    }, { removeQueuedChatMessage, updateQueuedChatMessage });

    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByLabelText("Queued messages")).toBeTruthy();
    const queuedInput = screen.getByLabelText("Queued message 1");
    expect(queuedInput).toHaveValue("run tests after this");
    fireEvent.change(queuedInput, { target: { value: "run unit tests after this" } });
    expect(updateQueuedChatMessage).toHaveBeenLastCalledWith("queued_1", "run unit tests after this");
    await user.click(screen.getByRole("button", { name: "Remove queued message 1" }));
    expect(removeQueuedChatMessage).toHaveBeenCalledWith("queued_1");
  });

  it("only renders queued messages for the active agent chat", () => {
    const { state, actions } = setup({
      activeAgentChatSessionID: "agent_chat_active",
      queuedChatMessages: [
        {
          id: "queued_active",
          session_id: "agent_chat_active",
          content: "send this here",
          runtime_kind: "agent",
          provider_filter: "ollama",
          model: "qwen2.5-coder",
          workspace: "/workspace",
          system_prompt: "",
          adapter_id: "codex",
          created_at: "2026-04-20T00:00:00Z",
        },
        {
          id: "queued_other",
          session_id: "agent_chat_other",
          content: "not in this chat",
          runtime_kind: "agent",
          provider_filter: "ollama",
          model: "qwen2.5-coder",
          workspace: "/workspace",
          system_prompt: "",
          adapter_id: "codex",
          created_at: "2026-04-20T00:00:00Z",
        },
      ],
    });

    render(<ChatView state={state} actions={actions} />);

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
    const { rerender } = render(<ChatView state={state} actions={actions} />);

    expect(screen.getByText(/Hecate Agent runs through task approvals and per-call sandboxing/)).toBeTruthy();

    rerender(<ChatView state={{ ...state, chatTarget: "model" }} actions={actions} />);
    expect(screen.queryByText(/Hecate Agent runs through task approvals and per-call sandboxing/)).toBeNull();
  });

  it("blocks Hecate Agent sends when tools are explicitly disabled for the model", async () => {
    const upsertModelCapabilityOverride = vi.fn(async () => true);
    const { state, actions } = setup({
      chatTarget: "agent",
      message: "inspect this repo",
      agentWorkspace: "/tmp/hecate",
      settingsConfig: {
        backend: "memory",
        providers: [{ id: "ollama", name: "Ollama", kind: "local", credential_configured: true }],
        policy_rules: [],
        pricebook: [],
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
    }, { upsertModelCapabilityOverride });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByRole("button", { name: "tools on" })).toBeTruthy();
    expect(screen.getByText(/Tools are disabled for this model/)).toBeTruthy();
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Enable tools" }));
    expect(upsertModelCapabilityOverride).toHaveBeenCalledWith(expect.objectContaining({
      provider: "ollama",
      model: "llama3.1:8b",
      tool_calling: "basic",
      note: "Tools enabled from Hecate Chat.",
    }));
  });

  it("opens the backing task from the Hecate Agent assistant turn, not the header", async () => {
    const onOpenTask = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      activeAgentChatSessionID: "agent_chat_1",
      activeAgentChatSession: {
        id: "agent_chat_1",
        runtime_kind: "agent",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          { id: "m1", runtime_kind: "agent", segment_id: "task:task_hecate_123456", task_id: "task_hecate_123456", role: "user", content: "inspect this repo", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            runtime_kind: "agent",
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
    render(<ChatView state={state} actions={actions} onOpenTask={onOpenTask} />);
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
      activeAgentChatSessionID: "agent_chat_1",
      activeAgentChatSession: {
        id: "agent_chat_1",
        runtime_kind: "model",
        title: "Mixed chat",
        task_id: "task_latest",
        latest_run_id: "run_latest",
        provider: "ollama",
        model: "ministral-3:latest",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          { id: "m1", runtime_kind: "model", segment_id: "model:direct", role: "user", content: "tell a joke", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            runtime_kind: "model",
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
    render(<ChatView state={state} actions={actions} onOpenTask={onOpenTask} />);

    expect(screen.getByText("Direct answer.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Open Task latest/i })).toBeNull();
    expect(onOpenTask).not.toHaveBeenCalled();
  });

  it("renders explicit Hecate Chat segment dividers when tools switch", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      activeAgentChatSessionID: "agent_chat_1",
      activeAgentChatSession: {
        id: "agent_chat_1",
        runtime_kind: "agent",
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
            runtime_kind: "model",
            provider: "ollama",
            model: "smollm2:135m",
            status: "completed",
            message_count: 2,
          },
          {
            id: "task:task_first",
            runtime_kind: "agent",
            provider: "ollama",
            model: "qwen2.5-coder",
            task_id: "task_first",
            latest_run_id: "run_first",
            status: "completed",
            message_count: 2,
          },
          {
            id: "model:second",
            runtime_kind: "model",
            provider: "ollama",
            model: "smollm2:135m",
            status: "completed",
            message_count: 2,
          },
        ],
        messages: [
          { id: "m1", runtime_kind: "model", segment_id: "model:first", role: "user", content: "answer directly", created_at: "2026-05-03T10:00:00Z" },
          { id: "m2", runtime_kind: "model", segment_id: "model:first", role: "assistant", content: "Direct answer.", status: "completed", model: "smollm2:135m", created_at: "2026-05-03T10:00:01Z" },
          { id: "m3", runtime_kind: "agent", segment_id: "task:task_first", task_id: "task_first", role: "user", content: "use tools", created_at: "2026-05-03T10:01:00Z" },
          { id: "m4", runtime_kind: "agent", segment_id: "task:task_first", task_id: "task_first", run_id: "run_first", role: "assistant", content: "Tool answer.", status: "completed", model: "qwen2.5-coder", created_at: "2026-05-03T10:01:01Z" },
          { id: "m5", runtime_kind: "model", segment_id: "model:second", role: "user", content: "back to direct", created_at: "2026-05-03T10:02:00Z" },
          { id: "m6", runtime_kind: "model", segment_id: "model:second", role: "assistant", content: "Direct again.", status: "completed", model: "smollm2:135m", created_at: "2026-05-03T10:02:01Z" },
        ],
      } as any,
    });
    render(<ChatView state={state} actions={actions} />);

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
      activeAgentChatSessionID: "agent_chat_1",
      activeAgentChatSession: {
        id: "agent_chat_1",
        runtime_kind: "agent",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          { id: "m1", role: "user", content: "inspect this repo", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            run_id: "run_hecate_abcdef",
            role: "assistant",
            content: "Done.",
            status: "completed",
            cost_mode: "hecate",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              { id: "legacy-task-run-running", type: "task_running", status: "running", title: "Task run running", detail: "run_hecate_abcdef" },
              { id: "hecate_task_run:run_hecate_abcdef", type: "task_run", status: "running", title: "Backing task", detail: "running · run_hecate_abcdef" },
              { id: "task:step:model", type: "thinking", status: "completed", kind: "model", title: "Agent turn 1", detail: "completed" },
              { id: "task:step:shell", type: "tool_call", status: "completed", kind: "shell", title: "shell_exec", detail: "completed" },
              { id: "task:run:terminal", type: "run_result", status: "completed", title: "Run completed" },
              { type: "completed", status: "completed", title: "Final answer" },
            ],
          },
        ],
      } as any,
    });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByText("completed · 1 tool")).toBeTruthy();
    expect(screen.getByText("Thinking")).toBeTruthy();
    expect(screen.getByText("Ran shell")).toBeTruthy();
    expect(screen.getByText("Backing task")).toBeTruthy();
    expect(screen.queryByText("Agent turn 1")).toBeNull();
    expect(screen.queryByText("shell_exec")).toBeNull();
    expect(screen.queryByText("Task run running")).toBeNull();
    expect(screen.queryByText("Run completed")).toBeNull();
  });

  it("resolves projected Hecate Agent task approvals from the chat banner", async () => {
    const resolveTaskApproval = vi.fn(async () => true);
    const onOpenTask = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      activeAgentChatSessionID: "agent_chat_1",
      activeAgentChatSession: {
        id: "agent_chat_1",
        runtime_kind: "agent",
        title: "Repo work",
        task_id: "task_hecate_123456",
        latest_run_id: "run_hecate_abcdef",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "awaiting_approval",
        messages: [
          { id: "m1", role: "user", content: "echo lol please", created_at: "2026-05-03T10:00:00Z" },
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
                detail: "builtin.agent_loop_approval - awaiting_approval",
                approval_id: "appr_123",
                needs_action: true,
                created_at: "2026-05-03T10:00:02Z",
              },
            ],
          },
        ],
      } as any,
    }, { resolveTaskApproval });
    render(<ChatView state={state} actions={actions} onOpenTask={onOpenTask} />);

    expect(screen.getByTestId("hecate-task-approval-banner")).toBeTruthy();
    expect(screen.getByText("Task approval required")).toBeTruthy();
    expect(screen.getAllByText("Waiting for approval").length).toBeGreaterThan(0);

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Open Task" }));
    expect(onOpenTask).toHaveBeenCalledWith("task_hecate_123456", "run_hecate_abcdef");

    await user.click(screen.getByRole("button", { name: /Approve approval/i }));
    expect(resolveTaskApproval).toHaveBeenCalledWith("task_hecate_123456", "appr_123", { decision: "approve" });
  });

  it("does not keep stale resolved Hecate Agent task approvals actionable", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      activeAgentChatSessionID: "agent_chat_1",
      activeAgentChatSession: {
        id: "agent_chat_1",
        runtime_kind: "agent",
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
    render(<ChatView state={state} actions={actions} />);

    expect(screen.queryByTestId("hecate-task-approval-banner")).toBeNull();
    expect(screen.queryByRole("button", { name: /Approve Agent tool call/i })).toBeNull();
  });

  it("calls setMessage as user types", async () => {
    const setMessage = vi.fn();
    // Start with empty message so the assertion sees only what we typed.
    const { state, actions } = setup({ chatTarget: "model", message: "" }, { setMessage });
    render(<ChatView state={state} actions={actions} />);
    const ta = screen.getByPlaceholderText(/Message/i) as HTMLTextAreaElement;
    const user = userEvent.setup();
    await user.type(ta, "h");
    expect(setMessage).toHaveBeenCalledWith("h");
  });
});

describe("ChatView Enter switch", () => {
  it("renders the segmented Enter/⌘+Enter or Ctrl+Enter switch", () => {
    const { state, actions } = setup();
    render(<ChatView state={state} actions={actions} />);
    // The switch is one of the toggle buttons in the input toolbar.
    const buttons = screen.getAllByRole("button");
    const labels = buttons.map(b => b.textContent?.trim()).filter(Boolean);
    const hasEnterToggle = labels.some(l => l === "↵ to send" || /[⌘+|Ctrl\+]\+?↵ to send/.test(l!));
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
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText(/No chats yet/i)).toBeTruthy();
  });

  it("renders one row per chat with title", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      chatSessions: [
        { id: "s1", title: "First chat", message_count: 4, provider_call_count: 2, updated_at: daysAgo(0) } as any,
        { id: "s2", title: "Second chat", message_count: 2, provider_call_count: 1, updated_at: daysAgo(10) } as any,
      ],
    });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText("Today")).toBeTruthy();
    expect(screen.getByText("Older")).toBeTruthy();
    expect(screen.getByText("First chat")).toBeTruthy();
    expect(screen.getByText("Second chat")).toBeTruthy();
  });

  it("filters chat history by title and route metadata", async () => {
    const { state, actions } = setup({
      chatTarget: "model",
      chatSessions: [
        { id: "s1", title: "Budget check", message_count: 4, provider_call_count: 2, last_provider: "anthropic", updated_at: daysAgo(0) } as any,
        { id: "s2", title: "Draft release notes", message_count: 2, provider_call_count: 1, last_provider: "openai", updated_at: daysAgo(0) } as any,
      ],
    });
    render(<ChatView state={state} actions={actions} />);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Search chats"), "anthropic");
    expect(screen.getByText("Budget check")).toBeTruthy();
    expect(screen.queryByText("Draft release notes")).toBeNull();
  });

  it("filters agent history by adapter and status metadata", async () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentChatSessions: [
        { id: "a1", title: "Codex refactor", adapter_id: "codex", status: "completed", message_count: 4, updated_at: daysAgo(0) } as any,
        { id: "a2", title: "Cursor repro", adapter_id: "cursor_agent", status: "failed", message_count: 2, updated_at: daysAgo(0) } as any,
      ],
    });
    render(<ChatView state={state} actions={actions} />);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Search chats"), "failed");
    expect(screen.getByText("Cursor repro")).toBeTruthy();
    expect(screen.queryByText("Codex refactor")).toBeNull();
  });

  it("calls selectChatSession when clicking a chat row", async () => {
    const selectChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup({
      chatTarget: "model",
      chatSessions: [{ id: "s1", title: "Pick me", message_count: 0, provider_call_count: 0 } as any],
    }, { selectChatSession });
    render(<ChatView state={state} actions={actions} />);
    const user = userEvent.setup();
    await user.click(screen.getByText("Pick me"));
    expect(selectChatSession).toHaveBeenCalledWith("s1");
  });

  it("calls selectChatSession when pressing Enter or Space on a focused chat row", async () => {
    const selectChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup({
      chatTarget: "model",
      chatSessions: [{ id: "s1", title: "Pick me", message_count: 0, provider_call_count: 0 } as any],
    }, { selectChatSession });
    render(<ChatView state={state} actions={actions} />);
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
    const { rerender } = render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText(/External agents run as your OS user/)).toBeTruthy();

    rerender(<ChatView state={{ ...state, chatTarget: "model" }} actions={actions} />);
    expect(screen.queryByText(/External agents run as your OS user/)).toBeNull();
  });

  it("does not show provider setup actions when agent chat has no available CLI", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      message: "run codex",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], pricebook: [], events: [] },
      agentAdapterID: "codex",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", managed: true, managed_package: "@zed-industries/codex-acp", available: false, status: "missing", error: "no local package runner found for @zed-industries/codex-acp", cost_mode: "external" },
      ],
    });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByText("Codex is unavailable")).toBeTruthy();
    expect(screen.getByText(/could not start Codex/)).toBeTruthy();
    expect(screen.getAllByText("Codex").length).toBeGreaterThan(0);
    expect(screen.getByText(/Install Node\/npm/)).toBeTruthy();
    expect(screen.getByText(/no local package runner/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Add provider/i })).toBeNull();
  });

  it("renders external agent controls and locks the adapter for an active chat", async () => {
    const setChatTarget = vi.fn();
    const setAgentAdapterID = vi.fn();
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "codex",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
        { id: "claude_code", name: "Claude Code", kind: "acp", command: "claude-agent-acp", available: false, status: "missing", cost_mode: "external" },
      ],
      agentChatSessions: [
        { id: "a1", title: "Codex work", adapter_id: "codex", workspace: "/tmp/hecate", status: "completed", message_count: 2 } as any,
      ],
      activeAgentChatSessionID: "a1",
      activeAgentChatSession: {
        id: "a1",
        title: "Codex work",
        adapter_id: "codex",
        workspace: "/tmp/hecate",
        status: "completed",
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
            adapter_id: "codex",
            adapter_name: "Codex",
            driver_kind: "acp",
            native_session_id: "native_codex_1",
            status: "completed",
            cost_mode: "external",
            diff_stat: "README.md | 2 +-\nui/src/features/chats/ChatView.tsx | 12 +++++++---\n2 files changed, 10 insertions(+), 4 deletions(-)",
            diff: "diff --git a/README.md b/README.md",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              { type: "started", status: "completed", title: "Starting external agent", detail: "Codex ACP session started" },
              { id: "plan:0:Inspect", type: "plan", status: "completed", kind: "high", title: "Inspect changes" },
              { id: "plan:1:Summarize", type: "plan", status: "in_progress", kind: "medium", title: "Summarize result" },
              { id: "tool:call_1", type: "tool_call", status: "completed", kind: "execute", title: "git diff --stat", detail: "README.md:12" },
              { type: "completed", status: "completed", title: "Final answer" },
            ],
          },
        ],
      } as any,
    }, { setChatTarget, setAgentAdapterID });
    const onOpenTrace = vi.fn();
    const { rerender } = render(<ChatView state={state} actions={actions} onOpenTrace={onOpenTrace} />);

    expect(screen.queryByDisplayValue("/tmp/hecate")).toBeNull();
    expect(screen.getByRole("button", { name: /workspace/i })).toBeTruthy();
    expect(screen.getAllByText("Codex work").length).toBeGreaterThan(0);
    expect(screen.getByText("Looks good.")).toBeTruthy();
    expect(screen.getAllByText(/ACP native_codex/).length).toBeGreaterThan(0);
    const traceButton = screen.getByRole("button", { name: /Open Trace req_code/i });
    expect(traceButton).toBeTruthy();
    expect(screen.queryByText("Starting external agent")).toBeNull();
    expect(screen.getByText("completed · 1/2 plan · 1 tool · files changed")).toBeTruthy();
    expect(screen.getByText("Inspect changes")).toBeTruthy();
    expect(screen.getByText("Summarize result")).toBeTruthy();
    expect(screen.getByText("git diff --stat")).toBeTruthy();
    expect(screen.getByText("README.md:12")).toBeTruthy();
    expect(screen.getByText("files changed · 2 files changed, 10 insertions(+), 4 deletions(-)")).toBeTruthy();
    expect(screen.getByText("README.md")).toBeTruthy();
    expect(screen.getByText("2 +-")).toBeTruthy();
    expect(screen.getByText("ui/src/features/chats/ChatView.tsx")).toBeTruthy();
    expect(screen.getByText("12 +++++++---")).toBeTruthy();
    expect(screen.getByText("raw adapter output · 1 line")).toBeTruthy();
    expect(screen.getByText("completed")).toBeTruthy();
    const user = userEvent.setup();
    await user.click(traceButton);
    expect(onOpenTrace).toHaveBeenCalledWith("req_codex_123456");
    const adapterPicker = screen.getByRole("button", { name: "External agent adapter" }) as HTMLButtonElement;
    expect(adapterPicker.disabled).toBe(true);
    expect(adapterPicker.title).toContain("Start a new chat");
    await user.click(adapterPicker);
    expect(screen.queryByText("Claude Code")).toBeNull();
    expect(setAgentAdapterID).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Hecate Chat" }));
    expect(setChatTarget).toHaveBeenCalledWith("agent");
    setChatTarget.mockClear();
    // Once inside Hecate Chat, tools can be disabled to use the
    // direct model-chat runtime.
    rerender(<ChatView state={{ ...state, chatTarget: "agent" }} actions={actions} />);
    await user.click(screen.getByRole("button", { name: /tools off/i }));
    expect(setChatTarget).toHaveBeenCalledWith("model");
  });

  it("allows choosing an agent before an agent chat is created", async () => {
    const setAgentAdapterID = vi.fn();
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterID: "codex",
      activeAgentChatSessionID: "",
      activeAgentChatSession: null,
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
        { id: "claude_code", name: "Claude Code", kind: "acp", command: "claude-agent-acp", available: true, status: "available", cost_mode: "external" },
      ],
    }, { setAgentAdapterID });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "External agent adapter" }));
    await user.click(screen.getByText("Claude Code"));
    expect(setAgentAdapterID).toHaveBeenCalledWith("claude_code");
  });

  it("shows a waiting state for a running agent before transcript output arrives", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
      activeAgentChatSessionID: "a1",
      activeAgentChatSession: {
        id: "a1",
        title: "Running work",
        adapter_id: "codex",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [
          { id: "m1", role: "user", content: "status", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "",
            adapter_id: "codex",
            adapter_name: "Codex",
            status: "running",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              { type: "running", status: "running", title: "Running", detail: "Waiting for ACP output" },
            ],
          },
        ],
      } as any,
    });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByText("Waiting for agent output...")).toBeTruthy();
    expect(screen.getAllByText("running").length).toBeGreaterThan(0);
  });

  it("shows transient agent narration as live assistant text while a run is active", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
      activeAgentChatSessionID: "a1",
      activeAgentChatSession: {
        id: "a1",
        title: "Inspect diff",
        adapter_id: "codex",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [
          { id: "m1", role: "user", content: "show diff", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "I’ll check the current worktree diff and summarize the changed files plus the important hunks.",
            adapter_id: "codex",
            adapter_name: "Codex",
            status: "running",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              { type: "running", status: "running", title: "Running", detail: "Waiting for ACP output" },
            ],
          },
        ],
      } as any,
    });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByText("I’ll check the current worktree diff and summarize the changed files plus the important hunks.")).toBeTruthy();
    expect(screen.getByText("I’ll check the current worktree diff and summarize the changed files plus the important hunks.").parentElement?.querySelector("[aria-hidden='true']")).toBeTruthy();
    expect(screen.queryByText("Waiting for agent output...")).toBeNull();
  });

  it("renders adapter-reported usage below completed agent messages", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
      activeAgentChatSessionID: "a1",
      activeAgentChatSession: {
        id: "a1",
        title: "Usage check",
        adapter_id: "codex",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          { id: "m1", role: "user", content: "status", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "Done.",
            adapter_id: "codex",
            adapter_name: "Codex",
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
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByText("0.1234 USD")).toBeTruthy();
    expect(screen.getByText("42000/200000 context")).toBeTruthy();
    expect(screen.getByText("reported by adapter · not enforced by Hecate")).toBeTruthy();
  });

  it("loads changed files, inspects a file diff, and confirms per-file revert", async () => {
    const listAgentChatMessageFiles = vi.fn(async () => [
      { path: "README.md", additions: 2, deletions: 1, status: "modified" },
      { path: "docs/runtime-api.md", additions: 4, deletions: 0, status: "added" },
    ]);
    const getAgentChatMessageFileDiff = vi.fn(async () => ({
      path: "README.md",
      additions: 2,
      deletions: 1,
      status: "modified",
      diff: "diff --git a/README.md b/README.md\n+new line",
    }));
    const revertAgentChatMessageFiles = vi.fn(async () => true);
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      activeAgentChatSessionID: "a1",
      activeAgentChatSession: {
        id: "a1",
        title: "Review files",
        adapter_id: "codex",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          { id: "m1", role: "user", content: "change docs", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "Updated the docs.",
            adapter_id: "codex",
            adapter_name: "Codex",
            status: "completed",
            diff_stat: "README.md | 3 ++-\ndocs/runtime-api.md | 4 ++++\n2 files changed, 6 insertions(+), 1 deletion(-)",
            diff: "diff --git a/README.md b/README.md\n+new line",
            created_at: "2026-05-03T10:00:01Z",
          },
        ],
      } as any,
    }, { listAgentChatMessageFiles, getAgentChatMessageFileDiff, revertAgentChatMessageFiles });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getByText("files changed · 2 files changed, 6 insertions(+), 1 deletion(-)"));

    expect(await screen.findByText("2 changed files")).toBeTruthy();
    expect(listAgentChatMessageFiles).toHaveBeenCalledWith("a1", "m2");

    await user.click(screen.getByRole("button", { name: "Inspect README.md" }));
    expect(getAgentChatMessageFileDiff).toHaveBeenCalledWith("a1", "m2", "README.md");
    expect(await screen.findByText("diff · README.md")).toBeTruthy();
    expect(document.body.textContent).toContain("+new line");

    await user.click(screen.getByRole("button", { name: "Revert README.md" }));
    expect(revertAgentChatMessageFiles).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Confirm revert README.md" }));
    expect(revertAgentChatMessageFiles).toHaveBeenCalledWith("a1", "m2", ["README.md"]);
  });

  it("surfaces diff-review API failures and clears loading states", async () => {
    const listAgentChatMessageFiles = vi.fn(async () => [
      { path: "README.md", additions: 2, deletions: 1, status: "modified" },
    ]);
    const getAgentChatMessageFileDiff = vi.fn(async () => {
      throw new Error("diff unavailable");
    });
    const revertAgentChatMessageFiles = vi.fn(async () => {
      throw new Error("git restore failed");
    });
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      activeAgentChatSessionID: "a1",
      activeAgentChatSession: {
        id: "a1",
        title: "Review files",
        adapter_id: "codex",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          { id: "m1", role: "user", content: "change docs", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "Updated the docs.",
            adapter_id: "codex",
            adapter_name: "Codex",
            status: "completed",
            diff_stat: "README.md | 3 ++-\n1 file changed, 2 insertions(+), 1 deletion(-)",
            diff: "diff --git a/README.md b/README.md\n+new line",
            created_at: "2026-05-03T10:00:01Z",
          },
        ],
      } as any,
    }, { listAgentChatMessageFiles, getAgentChatMessageFileDiff, revertAgentChatMessageFiles });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getByText("files changed · 1 file changed, 2 insertions(+), 1 deletion(-)"));
    expect(await screen.findByText("1 changed file")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Inspect README.md" }));
    expect(await screen.findByText("Could not load that file diff.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Inspect README.md" })).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Revert README.md" }));
    await user.click(screen.getByRole("button", { name: "Confirm revert README.md" }));
    expect(await screen.findByText("Revert failed. The workspace may not be a Git repository, or the file changed since capture.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Revert README.md" })).toBeTruthy();
  });

  it("surfaces changed-file list failures", async () => {
    const listAgentChatMessageFiles = vi.fn(async () => {
      throw new Error("files unavailable");
    });
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      activeAgentChatSessionID: "a1",
      activeAgentChatSession: {
        id: "a1",
        title: "Review files",
        adapter_id: "codex",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          {
            id: "m2",
            role: "assistant",
            content: "Updated the docs.",
            adapter_id: "codex",
            adapter_name: "Codex",
            status: "completed",
            diff_stat: "README.md | 3 ++-\n1 file changed, 2 insertions(+), 1 deletion(-)",
            diff: "diff --git a/README.md b/README.md\n+new line",
            created_at: "2026-05-03T10:00:01Z",
          },
        ],
      } as any,
    }, {
      listAgentChatMessageFiles,
      getAgentChatMessageFileDiff: vi.fn(async () => null),
      revertAgentChatMessageFiles: vi.fn(async () => false),
    });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getByText("files changed · 1 file changed, 2 insertions(+), 1 deletion(-)"));
    expect(await screen.findByText("Could not load changed files. The captured diff may no longer be available.")).toBeTruthy();
    expect(screen.queryByText("Loading changed files...")).toBeNull();
  });

  it("requires confirmation before reverting the full captured diff", async () => {
    const listAgentChatMessageFiles = vi.fn(async () => [
      { path: "README.md", additions: 2, deletions: 1, status: "modified" },
    ]);
    const revertAgentChatMessageFiles = vi.fn(async () => true);
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "/tmp/hecate",
      activeAgentChatSessionID: "a1",
      activeAgentChatSession: {
        id: "a1",
        title: "Review all",
        adapter_id: "codex",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          { id: "m1", role: "user", content: "change docs", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            role: "assistant",
            content: "Updated the docs.",
            adapter_id: "codex",
            adapter_name: "Codex",
            status: "completed",
            diff_stat: "README.md | 3 ++-\n1 file changed, 2 insertions(+), 1 deletion(-)",
            diff: "diff --git a/README.md b/README.md",
            created_at: "2026-05-03T10:00:01Z",
          },
        ],
      } as any,
    }, { listAgentChatMessageFiles, revertAgentChatMessageFiles });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getByText("files changed · 1 file changed, 2 insertions(+), 1 deletion(-)"));
    expect(await screen.findByText("1 changed file")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Revert all" }));
    expect(revertAgentChatMessageFiles).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Confirm revert all" }));
    expect(revertAgentChatMessageFiles).toHaveBeenCalledWith("a1", "m2", []);
  });

  it("disables stop and shows cancelling feedback after stop is requested", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      chatLoading: true,
      agentChatCancelling: true,
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
      activeAgentChatSessionID: "a1",
      activeAgentChatSession: {
        id: "a1",
        title: "Stopping work",
        adapter_id: "codex",
        driver_kind: "acp",
        workspace: "/tmp/hecate",
        status: "running",
        messages: [],
      } as any,
    });
    render(<ChatView state={state} actions={actions} />);

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
        { id: "claude_code", name: "Claude Code", kind: "acp", command: "claude-agent-acp", available: true, status: "available", cost_mode: "external" },
      ],
      activeAgentChatSessionID: "a1",
      activeAgentChatSession: {
        id: "a1",
        title: "Failed work",
        adapter_id: "claude_code",
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
            adapter_id: "claude_code",
            adapter_name: "Claude Code",
            status: "failed",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              { type: "failed", status: "failed", title: "Failed", detail: "Claude Code usage limit: credit balance is too low" },
            ],
          },
        ],
      } as any,
    });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByText("agent run failed")).toBeTruthy();
    expect(screen.getAllByText("Claude Code usage limit: credit balance is too low").length).toBeGreaterThan(0);
    expect(screen.getByText("raw adapter output · 1 line")).toBeTruthy();
    expect(screen.getAllByText("failed").length).toBeGreaterThan(0);
  });

  it("opens the workspace picker action from the folder button", async () => {
    const chooseAgentWorkspace = vi.fn(async () => true);
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
    }, { chooseAgentWorkspace });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getByTitle("Choose workspace folder"));
    expect(chooseAgentWorkspace).toHaveBeenCalled();
  });

  it("allows pasting a workspace path when the folder dialog is unavailable", async () => {
    const chooseAgentWorkspace = vi.fn(async () => false);
    const setAgentWorkspace = vi.fn();
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentWorkspace: "",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
    }, { chooseAgentWorkspace, setAgentWorkspace });
    render(<ChatView state={state} actions={actions} />);

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
        { id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available", cost_mode: "external" },
      ],
    });
    render(<ChatView state={state} actions={actions} />);

    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });

  it("explains why Hecate Chat cannot send with tools before workspace selection", () => {
    const { state, actions } = setup({
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
    });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByText(/Choose a workspace before sending/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Choose workspace" })).toBeTruthy();
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
        messages: [
          { id: "m1", sequence: 1, role: "assistant", content: "- [x] done\n- [ ] todo" },
        ],
        provider_calls: [],
      } as any,
    });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByRole("img", { name: "Completed task" })).toBeTruthy();
    expect(screen.getByRole("img", { name: "Incomplete task" })).toBeTruthy();
  });

  it("keeps provider and model pickers editable for an active model chat", async () => {
    const setProviderFilter = vi.fn();
    const setModel = vi.fn();
    const { state, actions } = setup({
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
        { id: "claude-sonnet-4-20250514", owned_by: "anthropic", metadata: { provider: "anthropic", provider_kind: "cloud" } },
        { id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } },
        { id: "gpt-4.1-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } },
      ],
    }, { setProviderFilter, setModel });
    render(<ChatView state={state} actions={actions} />);

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
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText(/Provider returned 500/)).toBeTruthy();
  });

  it("renders operator guidance for stable gateway error codes", () => {
    const openTrace = vi.fn();
    const { state, actions } = setup({
      chatError: "Incorrect API key provided",
      chatErrorAction: "Rotate the provider key in Settings, then test readiness again.",
      chatErrorCode: "provider_auth_failed",
      chatErrorRequestID: "req_1234567890abcdef",
      chatErrorStatus: 502,
      chatErrorTraceID: "trace_abcdef1234567890",
    });
    render(<ChatView state={state} actions={actions} onOpenTrace={openTrace} />);
    expect(screen.getByText("Provider credentials failed")).toBeTruthy();
    expect(screen.getByText("502 · provider_auth_failed")).toBeTruthy();
    expect(screen.getByText(/Rotate the provider key in Settings/)).toBeTruthy();
    expect(screen.getByText("req_123456")).toBeTruthy();
    expect(screen.getByText("trace_abcd")).toBeTruthy();
    screen.getByRole("button", { name: "Open trace" }).click();
    expect(openTrace).toHaveBeenCalledWith("req_1234567890abcdef");
  });
});

describe("ChatView session title", () => {
  it("shows 'New chat' when no chats and no active chat", () => {
    const { state, actions } = setup({ chatTarget: "model", chatSessions: [], activeChatSession: null });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getAllByText("New chat").length).toBeGreaterThan(0);
  });

  it("shows the active session's title", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      activeChatSession: { id: "s1", title: "Hello world", messages: [], provider_calls: [] } as any,
    });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText("Hello world")).toBeTruthy();
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
    render(<ChatView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: /new chat/i }));
    expect(createChatSession).toHaveBeenCalled();
    const textarea = screen.getByPlaceholderText(/^Message…/i);
    expect(document.activeElement).toBe(textarea);
  });
});

describe("ChatView session focus", () => {
  it("focuses the message textarea when a sidebar chat row is clicked", async () => {
    // Focus is applied on EXPLICIT user actions only — the New-chat
    // button onClick and chat-row onClick. The activeChatSessionID
    // effect deliberately does NOT focus, because data-load (chats
    // arriving from the API) also drives that transition and stealing
    // focus on load would block the dashboard's keyboard shortcuts
    // (e2e regression — see shell.spec.ts shortcut tests).
    const selectChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup({
      chatTarget: "model",
      chatSessions: [{ id: "s2", title: "Pick me", message_count: 0, provider_call_count: 0 } as any],
    }, { selectChatSession });
    const user = userEvent.setup();
    render(<ChatView state={state} actions={actions} />);
    // Move focus elsewhere to detect the jump.
    const closeBtn = screen.getByTitle("Close");
    closeBtn.focus();
    expect(document.activeElement).toBe(closeBtn);
    // Click the chat row — the only user-driven chat switch.
    await user.click(screen.getByText("Pick me"));
    const textarea = screen.getByPlaceholderText(/^Message…/i);
    expect(document.activeElement).toBe(textarea);
    expect(selectChatSession).toHaveBeenCalledWith("s2");
  });

  it("does NOT focus the textarea when activeChatSessionID changes from data-load", async () => {
    // Initial-load and API-driven session arrivals must not steal
    // focus — page-level shortcuts depend on it. Asserts the negative.
    const { state, actions } = setup({ chatTarget: "model", activeChatSessionID: "" });
    const { rerender } = render(<ChatView state={state} actions={actions} />);
    const closeBtn = screen.getByTitle("Close");
    closeBtn.focus();
    const next = { ...state, activeChatSessionID: "s1" };
    rerender(<ChatView state={next} actions={actions} />);
    // Focus must STAY on the close button — the effect should not have
    // jumped to the textarea on a programmatic ID transition.
    expect(document.activeElement).toBe(closeBtn);
  });
});

describe("ChatView history pagination", () => {
  it("does not show the legacy model-history pagination action for unified Hecate Chat", () => {
    const loadMoreChatSessions = vi.fn(async () => undefined);
    const { state, actions } = setup({
      chatTarget: "model",
      chatSessionsHasMore: true,
      chatSessions: [
        { id: "s1", title: "First page", message_count: 1, provider_call_count: 1 } as any,
      ],
    }, { loadMoreChatSessions });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.queryByRole("button", { name: "Load earlier chats" })).toBeNull();
    expect(loadMoreChatSessions).not.toHaveBeenCalled();
  });

  it("does not show the legacy search pagination action for unified Hecate Chat", async () => {
    const loadMoreChatSessions = vi.fn(async () => undefined);
    const { state, actions } = setup({
      chatTarget: "model",
      chatSessionsHasMore: true,
      chatSessions: [
        { id: "s1", title: "First page", message_count: 1, provider_call_count: 1 } as any,
      ],
    }, { loadMoreChatSessions });
    render(<ChatView state={state} actions={actions} />);

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
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByTestId("agent-approval-auto-banner")).toBeTruthy();
  });

  it("does not render the auto-mode banner when in prompt mode", () => {
    const { state, actions } = setup({
      chatTarget: "external_agent",
      agentAdapterApprovalMode: "prompt",
    });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.queryByTestId("agent-approval-auto-banner")).toBeNull();
  });

  it("hides the auto-mode banner when in model chat target (it's an agent-only concern)", () => {
    const { state, actions } = setup({
      chatTarget: "model",
      agentAdapterApprovalMode: "auto",
    });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.queryByTestId("agent-approval-auto-banner")).toBeNull();
  });

  it("renders the pending banner with rows scoped to the active session and opens the modal on Review", async () => {
    const sessionID = "a1";
    const pending = new Map<string, any>([
      [sessionID, [{
        approval_id: "ap-1",
        session_id: sessionID,
        adapter_id: "codex",
        tool_kind: "fs",
        tool_name: "write_file",
        created_at: "2026-04-21T10:00:00Z",
        expires_at: "2026-04-21T10:05:00Z",
      }]],
      ["other-session", [{
        approval_id: "ap-2",
        session_id: "other-session",
        adapter_id: "codex",
        tool_kind: "exec",
        created_at: "2026-04-21T10:00:00Z",
        expires_at: "2026-04-21T10:05:00Z",
      }]],
    ]);
    const getAgentChatApproval = vi.fn(async () => null); // modal opens, fetch returns null → renders error
    const { state, actions } = setup(
      {
      chatTarget: "external_agent",
        activeAgentChatSessionID: sessionID,
        activeAgentChatSession: { id: sessionID, title: "S1", adapter_id: "codex", workspace: "/tmp", status: "running" } as any,
        pendingApprovalsBySessionID: pending,
        agentChatSessions: [{ id: sessionID, title: "S1", adapter_id: "codex", status: "running", message_count: 0 } as any],
      },
      { getAgentChatApproval },
    );
    render(<ChatView state={state} actions={actions} />);

    // Only the active session's pending row is visible — banner must
    // not bleed approvals from other sessions.
    const reviews = screen.getAllByTestId("agent-approval-banner-review");
    expect(reviews).toHaveLength(1);

    const user = userEvent.setup();
    await user.click(reviews[0]!);
    // The modal mounts and asks for the full row.
    expect(getAgentChatApproval).toHaveBeenCalledWith(sessionID, "ap-1");
  });
});

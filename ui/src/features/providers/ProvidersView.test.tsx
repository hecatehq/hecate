import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ProvidersView } from "./ProvidersView";
import { AddProviderModal } from "./AddProviderModal";
import { discoverLocalProviders } from "../../lib/api";
import { createRuntimeConsoleActions, createRuntimeConsoleFixture } from "../../test/runtime-console-fixture";
import type { ConfiguredProviderRecord, ProviderPresetRecord, ProviderRecord } from "../../types/runtime";

vi.mock("../../lib/api", async importOriginal => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    discoverLocalProviders: vi.fn(async () => ({
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
          model_count: 2,
          models: ["llama3.1:8b", "qwen2.5:7b"],
        },
        {
          preset_id: "llamacpp",
          name: "llama.cpp",
          base_url: "http://127.0.0.1:8080/v1",
          probe_url: "http://127.0.0.1:8080/v1/models",
          status: "not_detected",
          command: "llama-server",
          command_available: false,
          http_available: false,
          model_count: 0,
          models: [],
        },
      ],
    })),
  };
});

const presets: ProviderPresetRecord[] = [
  { id: "anthropic", name: "Anthropic", kind: "cloud", protocol: "openai", base_url: "https://api.anthropic.com/v1", description: "" },
  { id: "llamacpp",  name: "llama.cpp", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:8080/v1", description: "" },
  { id: "localai",   name: "LocalAI",   kind: "local", protocol: "openai", base_url: "http://127.0.0.1:8080/v1", description: "" },
  { id: "ollama",    name: "Ollama",    kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", description: "" },
];

function makeConfigured(id: string, overrides: Partial<ConfiguredProviderRecord> = {}): ConfiguredProviderRecord {
  const preset = presets.find(p => p.id === id);
  return {
    id, name: id,
    kind: preset?.kind ?? "cloud",
    protocol: preset?.protocol ?? "openai",
    base_url: preset?.base_url ?? "",
    credential_configured: false,
    ...overrides,
  };
}

function makeStatus(name: string, overrides: Partial<ProviderRecord> = {}): ProviderRecord {
  return {
    name,
    kind: "local",
    healthy: true,
    status: "healthy",
    models: [],
    ...overrides,
  };
}

const localSession = { label: "Local" };

const originalRequestAnimationFrame = window.requestAnimationFrame;

afterEach(() => {
  window.requestAnimationFrame = originalRequestAnimationFrame;
});

function emptySettingsConfig() {
  return {
    backend: "memory",
    providers: [] as ConfiguredProviderRecord[],
    pricebook: [],
    policy_rules: [],
    events: [],
  };
}

describe("ProvidersView empty state", () => {
  it("shows empty state with Add provider button when no providers are configured", () => {
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: emptySettingsConfig(),
      providers: [],
    });

    render(<ProvidersView state={state} actions={createRuntimeConsoleActions()} />);

    expect(screen.getByText("No model providers configured")).toBeTruthy();
    expect(screen.getByText("Add a local or cloud provider to start routing requests")).toBeTruthy();
    // Both the header button and empty state button should be present
    const addButtons = screen.getAllByText("Add provider");
    expect(addButtons.length).toBeGreaterThan(0);
  });
});

describe("ProvidersView delete", () => {
  it("calls deleteProvider with the correct id when the trash button is clicked", async () => {
    const deleteProvider = vi.fn(async () => undefined);
    const actions = { ...createRuntimeConsoleActions(), deleteProvider };

    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: {
        ...emptySettingsConfig(),
        providers: [makeConfigured("ollama")],
      },
      providers: [makeStatus("ollama")],
    });

    render(<ProvidersView state={state} actions={actions} />);

    const user = userEvent.setup();
    const trashBtn = screen.getByTitle("Remove Ollama");
    await user.click(trashBtn);

    expect(screen.getByRole("dialog", { name: "Remove provider?" })).toBeTruthy();
    expect(screen.getByText(/Existing chats stay in history/)).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Remove provider" }));

    await waitFor(() => {
      expect(deleteProvider).toHaveBeenCalledWith("ollama");
    });
  });
});

describe("ProvidersView add provider modal", () => {
  function openAddModal() {
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: emptySettingsConfig(),
      providers: [],
    });
    const actions = createRuntimeConsoleActions();
    render(<ProvidersView state={state} actions={actions} />);
    return { actions };
  }

  it("clicking 'Add provider' opens the modal on the Local tab", async () => {
    openAddModal();
    const user = userEvent.setup();
    // Two buttons: header + empty-state. Click the first.
    await user.click(screen.getAllByText("Add provider")[0]);
    // Ollama is local — its presence proves the Local tab is active by default.
    expect(screen.getByText("Ollama")).toBeTruthy();
  });

  it("switching to the Cloud tab swaps the preset list", async () => {
    openAddModal();
    const user = userEvent.setup();
    await user.click(screen.getAllByText("Add provider")[0]);
    await user.click(screen.getByText("Cloud"));
    // Anthropic is cloud-only — appears only when the Cloud tab is active.
    expect(screen.getByText("Anthropic")).toBeTruthy();
  });

  it("highlights discovered local providers in the preset picker", async () => {
    openAddModal();
    const user = userEvent.setup();
    await user.click(screen.getAllByText("Add provider")[0]);

    await waitFor(() => {
      expect(screen.getByText("Running")).toBeTruthy();
    });
    expect(screen.getByText("Not detected")).toBeTruthy();
    expect(screen.getByText(/Checks command availability/)).toBeTruthy();
  });

  it("picking a cloud preset prefills Name from the preset and locks the field", async () => {
    // Preset names are the catalog join key (brand color, default base
    // URL, docs link) and stay fixed. The Custom name field below is
    // what the operator uses to disambiguate two instances of the same
    // preset.
    openAddModal();
    const user = userEvent.setup();
    await user.click(screen.getAllByText("Add provider")[0]);
    await user.click(screen.getByText("Cloud"));
    await user.click(screen.getByText("Anthropic"));
    const nameInput = screen.getByPlaceholderText("My Provider") as HTMLInputElement;
    expect(nameInput.value).toBe("Anthropic");
    expect(nameInput.readOnly).toBe(true);
    // Custom name field is present and editable for the disambiguation flow.
    const customInput = screen.getByPlaceholderText(/Prod, Dev, Staging/i) as HTMLInputElement;
    expect(customInput.readOnly).toBe(false);
  });

  it("submitting the form calls createProvider with preset params", async () => {
    const createProvider = vi.fn(async () => undefined);
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: emptySettingsConfig(),
      providers: [],
    });
    const actions = { ...createRuntimeConsoleActions(), createProvider };
    render(<ProvidersView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getAllByText("Add provider")[0]);
    await user.click(screen.getByText("Cloud"));
    await user.click(screen.getByText("Anthropic"));

    const apiKeyInput = screen.getByPlaceholderText("sk-…") as HTMLInputElement;
    await user.type(apiKeyInput, "sk-test");

    // The "Add provider" button inside the form has identical text to the
    // header button — pick the last one in DOM order, which is the form CTA.
    const addButtons = screen.getAllByText("Add provider");
    await user.click(addButtons[addButtons.length - 1]);

    await waitFor(() => {
      expect(createProvider).toHaveBeenCalledWith(expect.objectContaining({
        name: "Anthropic",
        preset_id: "anthropic",
        kind: "cloud",
        protocol: "openai",
        api_key: "sk-test",
      }));
    });
  });

  it("custom flow leaves the Name input editable", async () => {
    openAddModal();
    const user = userEvent.setup();
    await user.click(screen.getAllByText("Add provider")[0]);
    await user.click(screen.getByText("Custom"));
    const nameInput = screen.getByPlaceholderText("My Provider") as HTMLInputElement;
    expect(nameInput.readOnly).toBe(false);
  });

  it("server error renders inline inside the modal", async () => {
    const createProvider = vi.fn(async () => {
      throw new Error("provider with id \"anthropic\" already exists");
    });
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: emptySettingsConfig(),
      providers: [],
    });
    const actions = { ...createRuntimeConsoleActions(), createProvider };
    render(<ProvidersView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getAllByText("Add provider")[0]);
    await user.click(screen.getByText("Cloud"));
    await user.click(screen.getByText("Anthropic"));
    await user.type(screen.getByPlaceholderText("sk-…"), "sk-test");
    const addButtons = screen.getAllByText("Add provider");
    await user.click(addButtons[addButtons.length - 1]);

    await waitFor(() => {
      expect(screen.getByText(/already exists/)).toBeTruthy();
    });
  });
});

describe("ProvidersView edit modal", () => {
  it("clicking a row opens the edit modal with the provider name", async () => {
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: {
        ...emptySettingsConfig(),
        providers: [makeConfigured("anthropic", { kind: "cloud", credential_configured: true })],
      },
      providers: [makeStatus("anthropic", { kind: "cloud", healthy: true, status: "healthy" })],
    });
    render(<ProvidersView state={state} actions={createRuntimeConsoleActions()} />);

    const user = userEvent.setup();
    await user.click(screen.getByText("Anthropic"));
    // Modal title combines the provider name and kind so the operator
    // can tell whether they're editing a cloud or local provider.
    expect(screen.getByText("Anthropic · cloud")).toBeTruthy();
  });

  it("cloud row exposes an API key input that calls setProviderAPIKey", async () => {
    const setProviderAPIKey = vi.fn(async () => undefined);
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: {
        ...emptySettingsConfig(),
        providers: [makeConfigured("anthropic", { kind: "cloud", credential_configured: true })],
      },
      providers: [makeStatus("anthropic", { kind: "cloud", healthy: true, status: "healthy" })],
    });
    const actions = { ...createRuntimeConsoleActions(), setProviderAPIKey };
    render(<ProvidersView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getByText("Anthropic"));
    const keyInput = screen.getByPlaceholderText("••••••••") as HTMLInputElement;
    await user.type(keyInput, "sk-rotated");
    await user.click(screen.getByText("Update API key"));

    await waitFor(() => {
      expect(setProviderAPIKey).toHaveBeenCalledWith("anthropic", "sk-rotated");
    });
  });

  it("local row exposes an Endpoint URL input that calls setProviderBaseURL", async () => {
    const setProviderBaseURL = vi.fn(async () => undefined);
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: {
        ...emptySettingsConfig(),
        providers: [makeConfigured("ollama", { kind: "local", base_url: "http://127.0.0.1:11434/v1" })],
      },
      providers: [makeStatus("ollama", { kind: "local", healthy: true, status: "healthy" })],
    });
    const actions = { ...createRuntimeConsoleActions(), setProviderBaseURL };
    render(<ProvidersView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getByText("Ollama"));
    const urlInput = screen.getByDisplayValue("http://127.0.0.1:11434/v1") as HTMLInputElement;
    await user.clear(urlInput);
    await user.type(urlInput, "http://192.168.1.10:11434/v1");
    await user.click(screen.getByText("Save URL"));

    await waitFor(() => {
      expect(setProviderBaseURL).toHaveBeenCalledWith("ollama", "http://192.168.1.10:11434/v1");
    });
  });

  it("Save URL is disabled when the URL matches the current base_url", async () => {
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: {
        ...emptySettingsConfig(),
        providers: [makeConfigured("ollama", { kind: "local", base_url: "http://127.0.0.1:11434/v1" })],
      },
      providers: [makeStatus("ollama", { kind: "local", healthy: true, status: "healthy" })],
    });
    render(<ProvidersView state={state} actions={createRuntimeConsoleActions()} />);

    const user = userEvent.setup();
    await user.click(screen.getByText("Ollama"));
    const saveBtn = screen.getByText("Save URL").closest("button") as HTMLButtonElement;
    expect(saveBtn.disabled).toBe(true);
  });

  it("models list renders the default model with a 'default' badge", async () => {
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: {
        ...emptySettingsConfig(),
        providers: [makeConfigured("ollama", { kind: "local", default_model: "m1" })],
      },
      providers: [makeStatus("ollama", { kind: "local", healthy: true, status: "healthy", models: ["m1", "m2"], model_count: 2 })],
    });
    render(<ProvidersView state={state} actions={createRuntimeConsoleActions()} />);

    const user = userEvent.setup();
    await user.click(screen.getByText("Ollama"));
    expect(screen.getByText("m1")).toBeTruthy();
    expect(screen.getByText("m2")).toBeTruthy();
    // Only the default model row carries the badge.
    expect(screen.getByText("default")).toBeTruthy();
  });
});

describe("ProvidersView table renders", () => {
  it("renders provider rows with correct names and status badges", () => {
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: {
        ...emptySettingsConfig(),
        providers: [
          makeConfigured("anthropic", { kind: "cloud", credential_configured: true }),
          makeConfigured("ollama", { kind: "local" }),
        ],
      },
      providers: [
        makeStatus("ollama", { kind: "local", healthy: true, status: "healthy", routing_ready: true, credential_state: "not_required", models: ["llama3"], model_count: 1 }),
        makeStatus("anthropic", { kind: "cloud", healthy: true, status: "healthy", routing_ready: true, credential_state: "configured", models: ["claude-sonnet"], model_count: 1 }),
      ],
    });

    render(<ProvidersView state={state} actions={createRuntimeConsoleActions()} />);

    expect(screen.getByText("Anthropic")).toBeTruthy();
    expect(screen.getByText("Ollama")).toBeTruthy();
    const summary = screen.getByTestId("connections-readiness-summary");
    expect(within(summary).getByText("Model provider readiness")).toBeTruthy();
    expect(within(summary).getAllByText("2").length).toBeGreaterThanOrEqual(1);
    expect(within(summary).getByText("No configured provider setup issue needs repair.")).toBeTruthy();
    expect(within(summary).getByTestId("connections-provider-readiness-meaning")).toHaveTextContent("2 providers ready with 2 discovered models.");

    // Health badges: both providers report healthy. Credentials are split
    // into a separate column so cloud (Configured) and local (Not required)
    // both render their own badge.
    expect(screen.getAllByText("Healthy").length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText("Configured").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("Not required")).toBeTruthy();

    // No toggle/switch elements
    expect(screen.queryByRole("switch")).toBeNull();
  });

  it("renders external-agent readiness and grants in the Connections workspace", async () => {
    const listAgentChatGrants = vi.fn(async () => undefined);
    const probeAgentAdapter = vi.fn(async () => null);
    const actions = {
      ...createRuntimeConsoleActions(),
      listAgentChatGrants,
      probeAgentAdapter,
    };
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: {
        ...emptySettingsConfig(),
        providers: [makeConfigured("ollama", { kind: "local" })],
      },
      providers: [makeStatus("ollama", { kind: "local", healthy: true, status: "healthy", routing_ready: true, models: ["llama3"], model_count: 1 })],
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
      agentChatGrants: [
        {
          id: "grant-1",
          scope: "session",
          adapter_id: "codex",
          tool_kind: "file_write",
          decision: "approve",
          granted_at: "2026-04-21T10:00:00Z",
        },
      ],
    });

    render(<ProvidersView state={state} actions={actions} />);

    expect(await screen.findByTestId("external-agents-adapters")).toBeTruthy();
    expect(screen.getByTestId("external-agents-adapter-codex")).toBeTruthy();
    expect(screen.getByTestId("external-agents-row-grant-1")).toBeTruthy();
    expect(listAgentChatGrants).toHaveBeenCalled();
    expect(probeAgentAdapter).toHaveBeenCalledWith("codex");
  });

  it("blocks submit when the typed Endpoint URL is already taken", async () => {
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: {
        ...emptySettingsConfig(),
        providers: [makeConfigured("ollama", { base_url: "http://127.0.0.1:11434/v1" })],
      },
      providers: [makeStatus("ollama")],
    });

    render(<ProvidersView state={state} actions={createRuntimeConsoleActions()} />);

    const user = userEvent.setup();
    // Open the Add modal, then pick "Custom" so the Endpoint URL field is editable.
    const addBtn = screen.getAllByText("Add provider").pop()!; // header button
    await user.click(addBtn);
    // Switch to the Local tab so the Custom flow lands on a kind whose
    // Endpoint URL field is shown by default.
    await user.click(screen.getByText("Custom"));

    // FormStep is redefined on every parent render, so per-keystroke
    // remounts make user.type drop characters. Paste-style fireEvent
    // sets the value in one shot, which is the realistic interaction
    // anyway (operators paste their endpoint URL).
    const urlInput = () => screen.getByPlaceholderText("http://localhost:11434/v1") as HTMLInputElement;
    fireEvent.change(urlInput(), { target: { value: "http://127.0.0.1:11434/v1" } });

    await waitFor(() => {
      expect(screen.getByText(/Endpoint already used by/)).toBeTruthy();
    });
    expect(screen.getByText(/Choose another URL to continue\./)).toBeTruthy();
    expect(screen.getAllByText("Add provider").pop()).toBeDisabled();

    fireEvent.change(urlInput(), { target: { value: "http://127.0.0.1:9999/v1" } });
    expect(screen.queryByText(/Endpoint already used by/)).toBeNull();
    expect(screen.getAllByText("Add provider").pop()).not.toBeDisabled();
  });

  it("asks for a custom name when the selected preset id already exists", async () => {
    const createProvider = vi.fn(async () => undefined);
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: {
        ...emptySettingsConfig(),
        providers: [makeConfigured("llama-cpp", { name: "llama.cpp", base_url: "http://127.0.0.1:9090/v1" })],
      },
      providers: [makeStatus("llama-cpp")],
    });
    const actions = { ...createRuntimeConsoleActions(), createProvider };

    render(<ProvidersView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getAllByText("Add provider")[0]);
    await user.click(screen.getAllByText("llama.cpp").pop()!);

    expect(screen.getByText(/llama\.cpp is already configured/)).toBeTruthy();
    expect(screen.getByText(/Add a custom name/)).toBeTruthy();
    expect(screen.getAllByText("Add provider").pop()).toBeDisabled();

    // The form's mount-time effect in `AddProviderModal` schedules
    // `requestAnimationFrame(() => target?.focus())` to move focus to
    // the URL input. Under full-suite parallel load that rAF can fire
    // mid-`user.type` and redirect some keystrokes away from the
    // custom-name field, leaving form.custom_name shorter than "Dev"
    // — the duplicate check still matches and the warning persists.
    // Awaiting one rAF tick proves the effect has already run, so the
    // upcoming typing reaches the right input.
    await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));

    await user.type(screen.getByPlaceholderText(/Prod, Dev, Staging/i), "Dev");

    expect(screen.queryByText(/llama\.cpp is already configured/)).toBeNull();
    expect(screen.getAllByText("Add provider").pop()).not.toBeDisabled();
  });

  it("blocks submit when the custom name still collides with an existing provider id", async () => {
    const createProvider = vi.fn(async () => undefined);
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: {
        ...emptySettingsConfig(),
        providers: [
          makeConfigured("anthropic-dev", {
            name: "Anthropic",
            custom_name: "Dev",
            kind: "cloud",
            base_url: "https://api.anthropic-dev.example/v1",
          }),
        ],
      },
      providers: [makeStatus("anthropic-dev", { kind: "cloud" })],
    });
    const actions = { ...createRuntimeConsoleActions(), createProvider };

    render(<AddProviderModal open state={state} actions={actions} onClose={() => {}} />);

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Cloud" }));
    await user.click(within(screen.getByRole("dialog")).getByText("Anthropic", { exact: true }));

    const customNameInput = screen.getByPlaceholderText(/Prod, Dev, Staging/i);
    fireEvent.change(customNameInput, { target: { value: "Dev" } });

    expect(screen.getByText(/Custom name is already used by Anthropic \(Dev\)/)).toBeTruthy();
    expect(screen.getAllByText("Add provider").pop()).toBeDisabled();

    await user.clear(customNameInput);
    fireEvent.change(customNameInput, { target: { value: "Work" } });

    expect(screen.queryByText(/Custom name is already used/)).toBeNull();
    expect(screen.getAllByText("Add provider").pop()).not.toBeDisabled();
  });

  it("clears stale local discovery state every time the add-provider modal opens", async () => {
    vi.mocked(discoverLocalProviders)
      .mockRejectedValueOnce(new Error("local probe failed"))
      .mockResolvedValueOnce({
        object: "local_provider_discovery",
        data: [{
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
        }],
      });
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: emptySettingsConfig(),
    });
    const actions = createRuntimeConsoleActions();

    const { rerender } = render(<AddProviderModal open state={state} actions={actions} onClose={() => {}} />);
    expect(await screen.findByText("local probe failed")).toBeTruthy();

    rerender(<AddProviderModal open={false} state={state} actions={actions} onClose={() => {}} />);
    rerender(<AddProviderModal open state={state} actions={actions} onClose={() => {}} />);

    await waitFor(() => expect(screen.queryByText("local probe failed")).toBeNull());
    expect(await screen.findByText("Running")).toBeTruthy();
  });

  it("ignores stale local discovery responses from a previous modal open", async () => {
    let resolveSlow: (value: Awaited<ReturnType<typeof discoverLocalProviders>>) => void = () => {};
    const slow = new Promise<Awaited<ReturnType<typeof discoverLocalProviders>>>(resolve => {
      resolveSlow = resolve;
    });
    let resolveFast: (value: Awaited<ReturnType<typeof discoverLocalProviders>>) => void = () => {};
    const fast = new Promise<Awaited<ReturnType<typeof discoverLocalProviders>>>(resolve => {
      resolveFast = resolve;
    });
    vi.mocked(discoverLocalProviders).mockReturnValueOnce(slow).mockReturnValueOnce(fast);
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: emptySettingsConfig(),
    });
    const actions = createRuntimeConsoleActions();
    const initialDiscoveryCalls = vi.mocked(discoverLocalProviders).mock.calls.length;

    const { rerender } = render(<AddProviderModal open state={state} actions={actions} onClose={() => {}} />);
    await waitFor(() => expect(discoverLocalProviders).toHaveBeenCalledTimes(initialDiscoveryCalls + 1));
    rerender(<AddProviderModal open={false} state={state} actions={actions} onClose={() => {}} />);
    rerender(<AddProviderModal open state={state} actions={actions} onClose={() => {}} />);
    await waitFor(() => expect(discoverLocalProviders).toHaveBeenCalledTimes(initialDiscoveryCalls + 2));

    resolveFast({
      object: "local_provider_discovery",
      data: [{
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
      }],
    });
    expect(await screen.findByText("Running")).toBeTruthy();

    resolveSlow({
      object: "local_provider_discovery",
      data: [{
        preset_id: "lmstudio",
        name: "LM Studio",
        base_url: "http://127.0.0.1:1234/v1",
        probe_url: "http://127.0.0.1:1234/v1/models",
        status: "installed",
        command: "lms",
        command_available: true,
        command_path: "/Users/alice/.lmstudio/bin/lms",
        http_available: false,
        model_count: 0,
        models: [],
      }],
    });
    await waitFor(() => expect(screen.queryByText("LM Studio")).toBeNull());
    expect(screen.getByText("Running")).toBeTruthy();
  });

  it("does not steal focus back to Endpoint URL after typing in Custom name", async () => {
    window.requestAnimationFrame = callback => {
      callback(0);
      return 0;
    };
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: emptySettingsConfig(),
      providers: [],
    });

    render(<ProvidersView state={state} actions={createRuntimeConsoleActions()} />);

    const user = userEvent.setup();
    await user.click(screen.getAllByText("Add provider")[0]);
    await user.click(screen.getByText("Ollama"));
    const customNameInput = screen.getByPlaceholderText(/Prod, Dev, Staging/i) as HTMLInputElement;
    await user.click(customNameInput);
    await user.type(customNameInput, "Dev");

    expect(document.activeElement).toBe(customNameInput);
    expect((screen.getByDisplayValue("http://127.0.0.1:11434/v1") as HTMLInputElement).value).toBe("http://127.0.0.1:11434/v1");
  });

  it("shows provider health diagnostics and last errors", async () => {
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      settingsConfig: {
        ...emptySettingsConfig(),
        providers: [makeConfigured("ollama")],
      },
      providers: [
        makeStatus("ollama", {
          healthy: false,
          status: "open",
          last_error: "connect: connection refused",
          last_error_class: "rate_limit",
          model_count: 1,
          credential_state: "not_required",
          routing_ready: false,
          routing_blocked_reason: "provider_rate_limited",
          readiness: {
            status: "blocked",
            reason: "provider_rate_limited",
            message: "Ollama is temporarily unavailable because it is rate limited.",
            operator_action: "Wait for cooldown or route to another local provider.",
          },
          readiness_checks: [
            { name: "credentials", status: "ok", reason: "not_required", message: "No credentials are required for this provider." },
            { name: "models", status: "ok", reason: "models_discovered", message: "1 model discovered." },
            {
              name: "health",
              status: "blocked",
              reason: "provider_rate_limited",
              message: "Provider is cooling down after an upstream rate limit.",
              operator_action: "Use the backend-provided repair action.",
            },
            { name: "routing", status: "blocked", reason: "provider_rate_limited", message: "Routing is blocked while the provider cools down after a rate limit." },
          ],
          discovery_source: "live",
          last_checked_at: "2026-04-29T10:00:00Z",
          open_until: "2026-04-29T10:01:00Z",
          last_latency_ms: 980,
          consecutive_failures: 1,
          total_failures: 4,
          rate_limits: 2,
        }),
      ],
    });

    render(<ProvidersView state={state} actions={createRuntimeConsoleActions()} />);

    // Health column shows "Down" for circuit-open providers.
    expect(screen.getByText("Down")).toBeTruthy();

    const user = userEvent.setup();
    await user.click(screen.getByText("Ollama"));

    expect(screen.getByText(/Circuit open/)).toBeTruthy();
    expect(screen.getByText("Readiness")).toBeTruthy();
    expect(screen.getByText("Readiness summary")).toBeTruthy();
    expect(screen.getByText("Ollama is temporarily unavailable because it is rate limited.")).toBeTruthy();
    expect(screen.getByText("Next: Wait for cooldown or route to another local provider.")).toBeTruthy();
    expect(screen.getAllByText("Credentials").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Models").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Health").length).toBeGreaterThan(0);
    expect(screen.getByText("Routing")).toBeTruthy();
    expect(screen.getByText("Routing is blocked while the provider cools down after a rate limit.")).toBeTruthy();
    expect(screen.getByText("Next: Use the backend-provided repair action.")).toBeTruthy();
    expect(screen.getAllByText(/Next: Wait for cooldown or temporarily route to another provider/).length).toBeGreaterThan(0);
    expect(screen.getByText("Diagnostics")).toBeTruthy();
    expect(screen.getByText("connect: connection refused")).toBeTruthy();
    expect(screen.getAllByText("Not required").length).toBeGreaterThan(0);
    expect(screen.getByText(/discovery:/)).toBeTruthy();
    expect(screen.getByText(/error class:/)).toBeTruthy();
    expect(screen.getByText(/last latency:/)).toBeTruthy();
    expect(screen.getByText(/totals:/)).toBeTruthy();
    expect(screen.getByText("Checked")).toBeTruthy();
  });
});

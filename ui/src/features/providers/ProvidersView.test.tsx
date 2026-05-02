import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ProvidersView } from "./ProvidersView";
import { createRuntimeConsoleActions, createRuntimeConsoleFixture } from "../../test/runtime-console-fixture";
import type { ConfiguredProviderRecord, ProviderPresetRecord, ProviderRecord } from "../../types/runtime";

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

function emptyControlPlaneConfig() {
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
      controlPlaneConfig: emptyControlPlaneConfig(),
      providers: [],
    });

    render(<ProvidersView state={state} actions={createRuntimeConsoleActions()} />);

    expect(screen.getByText("No providers configured")).toBeTruthy();
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
      controlPlaneConfig: {
        ...emptyControlPlaneConfig(),
        providers: [makeConfigured("ollama")],
      },
      providers: [makeStatus("ollama")],
    });

    // Mock window.confirm to auto-approve
    const origConfirm = window.confirm;
    window.confirm = () => true;

    render(<ProvidersView state={state} actions={actions} />);

    const user = userEvent.setup();
    const trashBtn = screen.getByTitle("Remove Ollama");
    await user.click(trashBtn);

    await waitFor(() => {
      expect(deleteProvider).toHaveBeenCalledWith("ollama");
    });

    window.confirm = origConfirm;
  });
});

describe("ProvidersView add provider modal", () => {
  function openAddModal() {
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      controlPlaneConfig: emptyControlPlaneConfig(),
      providers: [],
    });
    const actions = createRuntimeConsoleActions();
    render(<ProvidersView state={state} actions={actions} />);
    return { actions };
  }

  it("clicking 'Add provider' opens the modal on the Cloud tab", async () => {
    openAddModal();
    const user = userEvent.setup();
    // Two buttons: header + empty-state. Click the first.
    await user.click(screen.getAllByText("Add provider")[0]);
    // Anthropic preset is cloud-only — its presence proves the Cloud tab is active.
    expect(screen.getByText("Anthropic")).toBeTruthy();
  });

  it("switching to the Local tab swaps the preset list", async () => {
    openAddModal();
    const user = userEvent.setup();
    await user.click(screen.getAllByText("Add provider")[0]);
    await user.click(screen.getByText("Local"));
    // Ollama is local — appears only when the Local tab is active.
    expect(screen.getByText("Ollama")).toBeTruthy();
  });

  it("picking a cloud preset prefills Name from the preset and locks the field", async () => {
    // Preset names are the catalog join key (brand color, default base
    // URL, docs link) and stay fixed. The Custom name field below is
    // what the operator uses to disambiguate two instances of the same
    // preset.
    openAddModal();
    const user = userEvent.setup();
    await user.click(screen.getAllByText("Add provider")[0]);
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
      controlPlaneConfig: emptyControlPlaneConfig(),
      providers: [],
    });
    const actions = { ...createRuntimeConsoleActions(), createProvider };
    render(<ProvidersView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getAllByText("Add provider")[0]);
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
      controlPlaneConfig: emptyControlPlaneConfig(),
      providers: [],
    });
    const actions = { ...createRuntimeConsoleActions(), createProvider };
    render(<ProvidersView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getAllByText("Add provider")[0]);
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
      controlPlaneConfig: {
        ...emptyControlPlaneConfig(),
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
      controlPlaneConfig: {
        ...emptyControlPlaneConfig(),
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
      controlPlaneConfig: {
        ...emptyControlPlaneConfig(),
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
      controlPlaneConfig: {
        ...emptyControlPlaneConfig(),
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
      controlPlaneConfig: {
        ...emptyControlPlaneConfig(),
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
      controlPlaneConfig: {
        ...emptyControlPlaneConfig(),
        providers: [
          makeConfigured("anthropic", { kind: "cloud", credential_configured: true }),
          makeConfigured("ollama", { kind: "local" }),
        ],
      },
      providers: [
        makeStatus("ollama", { kind: "local", healthy: true, status: "healthy", routing_ready: true, credential_state: "not_required", models: ["llama3"], model_count: 1 }),
        makeStatus("anthropic", { kind: "cloud", healthy: true, status: "healthy", routing_ready: true, credential_state: "configured" }),
      ],
    });

    render(<ProvidersView state={state} actions={createRuntimeConsoleActions()} />);

    expect(screen.getByText("Anthropic")).toBeTruthy();
    expect(screen.getByText("Ollama")).toBeTruthy();

    // Health badges: both providers report healthy. Credentials are split
    // into a separate column so cloud (Configured) and local (Not required)
    // both render their own badge.
    expect(screen.getAllByText("Healthy").length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText("Configured")).toBeTruthy();
    expect(screen.getByText("Not required")).toBeTruthy();

    // No toggle/switch elements
    expect(screen.queryByRole("switch")).toBeNull();
  });

  it("warns inline when the typed Endpoint URL collides with an existing provider", async () => {
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      controlPlaneConfig: {
        ...emptyControlPlaneConfig(),
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
    await user.click(screen.getByText("Local"));
    await user.click(screen.getByText("Custom"));

    // FormStep is redefined on every parent render, so per-keystroke
    // remounts make user.type drop characters. Paste-style fireEvent
    // sets the value in one shot, which is the realistic interaction
    // anyway (operators paste their endpoint URL).
    const urlInput = () => screen.getByPlaceholderText("http://localhost:11434/v1") as HTMLInputElement;
    fireEvent.change(urlInput(), { target: { value: "http://127.0.0.1:11434/v1" } });

    await waitFor(() => {
      expect(screen.getByText(/already used by/)).toBeTruthy();
    });
    expect(screen.getByText(/Backend will reject\./)).toBeTruthy();

    // No collision: warning disappears.
    fireEvent.change(urlInput(), { target: { value: "http://127.0.0.1:9999/v1" } });
    expect(screen.queryByText(/already used by/)).toBeNull();
  });

  it("shows provider health diagnostics and last errors", async () => {
    const state = createRuntimeConsoleFixture({
      session: localSession,
      providerPresets: presets,
      controlPlaneConfig: {
        ...emptyControlPlaneConfig(),
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

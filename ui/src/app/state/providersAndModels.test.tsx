import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  modelToolSupportKey,
  ProvidersAndModelsProvider,
  useEnsureProviderPresetsLoaded,
  useProvidersAndModels,
} from "./providersAndModels";
import type { ReactNode } from "react";

const getProvidersMock = vi.fn();
const getModelsMock = vi.fn();
const getProviderPresetsMock = vi.fn();
const getAgentAdaptersMock = vi.fn();
const probeAgentAdapterMock = vi.fn();
const verifyModelToolSupportMock = vi.fn();
const warnMock = vi.fn();

vi.mock("../../lib/api", () => ({
  getProviders: (...args: unknown[]) => getProvidersMock(...args),
  getModels: (...args: unknown[]) => getModelsMock(...args),
  getProviderPresets: (...args: unknown[]) => getProviderPresetsMock(...args),
  getAgentAdapters: (...args: unknown[]) => getAgentAdaptersMock(...args),
  probeAgentAdapter: (...args: unknown[]) => probeAgentAdapterMock(...args),
  verifyModelToolSupport: (...args: unknown[]) => verifyModelToolSupportMock(...args),
}));

vi.mock("../../lib/log", () => ({
  info: vi.fn(),
  warn: (message: string, ...args: unknown[]) => warnMock(message, ...args),
  error: vi.fn(),
}));

function Wrapper({ children }: { children: ReactNode }) {
  return <ProvidersAndModelsProvider>{children}</ProvidersAndModelsProvider>;
}

beforeEach(() => {
  getProvidersMock.mockReset();
  getModelsMock.mockReset();
  getProviderPresetsMock.mockReset();
  getAgentAdaptersMock.mockReset();
  getAgentAdaptersMock.mockResolvedValue({ object: "agent_adapters", data: [] });
  probeAgentAdapterMock.mockReset();
  verifyModelToolSupportMock.mockReset();
  warnMock.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("useEnsureProviderPresetsLoaded", () => {
  it("fetches presets on first mount and flips providerPresetsLoaded=true", async () => {
    const presets = {
      object: "list",
      data: [{ id: "anthropic", name: "Anthropic", kind: "cloud" }],
    } as any;
    getProviderPresetsMock.mockResolvedValue(presets);

    const { result } = renderHook(
      () => {
        useEnsureProviderPresetsLoaded();
        return useProvidersAndModels();
      },
      { wrapper: Wrapper },
    );

    expect(result.current.state.providerPresetsLoaded).toBe(false);
    expect(getProviderPresetsMock).toHaveBeenCalledTimes(1);

    await waitFor(() => {
      expect(result.current.state.providerPresetsLoaded).toBe(true);
    });
    expect(result.current.state.providerPresets).toEqual([
      { id: "anthropic", name: "Anthropic", kind: "cloud" },
    ]);
  });

  it("does NOT re-fetch when presets are already loaded", async () => {
    getProviderPresetsMock.mockResolvedValue({ object: "list", data: [] });

    const { rerender, result } = renderHook(
      () => {
        useEnsureProviderPresetsLoaded();
        return useProvidersAndModels();
      },
      { wrapper: Wrapper },
    );

    await waitFor(() => {
      expect(result.current.state.providerPresetsLoaded).toBe(true);
    });
    expect(getProviderPresetsMock).toHaveBeenCalledTimes(1);

    rerender();
    expect(getProviderPresetsMock).toHaveBeenCalledTimes(1);
  });

  it("tolerates fetch failure: leaves loaded=false and logs a warning", async () => {
    getProviderPresetsMock.mockRejectedValue(new Error("network bonk"));

    const { result } = renderHook(
      () => {
        useEnsureProviderPresetsLoaded();
        return useProvidersAndModels();
      },
      { wrapper: Wrapper },
    );

    await waitFor(() => {
      expect(warnMock).toHaveBeenCalled();
    });
    expect(warnMock).toHaveBeenCalledWith(
      "providerPresets.ensureLoaded.failed",
      expect.objectContaining({ err: "network bonk" }),
    );
    expect(result.current.state.providerPresetsLoaded).toBe(false);
  });

  it("skips the fetch when called with when=false", async () => {
    getProviderPresetsMock.mockResolvedValue({ object: "list", data: [] });

    // The when gate exists for always-mounted consumers like
    // AddProviderModal (mounted with open=false inside ChatView).
    // Without the gate, the optimization would no-op on Chats boots.
    const { rerender, result } = renderHook(
      ({ when }: { when: boolean }) => {
        useEnsureProviderPresetsLoaded(when);
        return useProvidersAndModels();
      },
      { wrapper: Wrapper, initialProps: { when: false } },
    );

    // Give the effect a tick to (not) fire.
    await new Promise((r) => setTimeout(r, 10));
    expect(getProviderPresetsMock).toHaveBeenCalledTimes(0);
    expect(result.current.state.providerPresetsLoaded).toBe(false);

    // Flip the gate to true — now the fetch fires.
    rerender({ when: true });
    expect(getProviderPresetsMock).toHaveBeenCalledTimes(1);
  });

  it("retries on next mount after a failure", async () => {
    getProviderPresetsMock.mockRejectedValueOnce(new Error("transient"));

    const { unmount } = renderHook(() => useEnsureProviderPresetsLoaded(), { wrapper: Wrapper });
    await waitFor(() => {
      expect(warnMock).toHaveBeenCalled();
    });
    expect(getProviderPresetsMock).toHaveBeenCalledTimes(1);
    unmount();

    getProviderPresetsMock.mockResolvedValueOnce({ object: "list", data: [] });
    renderHook(() => useEnsureProviderPresetsLoaded(), { wrapper: Wrapper });
    await waitFor(() => {
      expect(getProviderPresetsMock).toHaveBeenCalledTimes(2);
    });
  });
});

describe("probeAgentAdapter", () => {
  it("dedupes concurrent probes for the same adapter", async () => {
    let resolveProbe: (value: unknown) => void = () => {};
    probeAgentAdapterMock.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveProbe = resolve;
      }),
    );
    const { result } = renderHook(() => useProvidersAndModels(), { wrapper: Wrapper });
    let first: Promise<unknown> | undefined;
    let second: Promise<unknown> | undefined;

    act(() => {
      first = result.current.actions.probeAgentAdapter("codex");
      second = result.current.actions.probeAgentAdapter("codex");
    });

    expect(probeAgentAdapterMock).toHaveBeenCalledTimes(1);
    expect(probeAgentAdapterMock).toHaveBeenCalledWith("codex");

    await act(async () => {
      resolveProbe({
        object: "agent_adapter_probe",
        data: {
          adapter: {
            id: "codex",
            name: "Codex",
            kind: "acp",
            command: "codex-acp-adapter",
            available: true,
            status: "available",
            supports_authenticate: false,
            supports_logout: false,
          },
          health: {
            adapter_id: "codex",
            status: "ready",
            stage: "ready",
            duration_ms: 42,
          },
        },
      });
      await Promise.all([first, second]);
    });

    expect(result.current.state.agentAdapterHealthByID.get("codex")).toMatchObject({
      status: "ready",
    });
    expect(result.current.state.agentAdapterHealthLoadingByID.has("codex")).toBe(false);
  });

  it("keeps passive launch discovery when a disposable diagnostic cannot resolve the app", async () => {
    getAgentAdaptersMock.mockResolvedValueOnce({
      object: "agent_adapters",
      data: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex",
          available: true,
          status: "available",
          path: "/Applications/Codex.app/Contents/Resources/codex",
          auth_status: "unknown",
          supports_authenticate: false,
          supports_logout: false,
        },
      ],
    });
    probeAgentAdapterMock.mockResolvedValueOnce({
      object: "agent_adapter_probe",
      data: {
        adapter: {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex",
          available: false,
          status: "missing",
          error: "codex command was not found",
          auth_status: "unknown",
          supports_authenticate: false,
          supports_logout: false,
        },
        health: {
          adapter_id: "codex",
          status: "not_installed",
          stage: "resolve",
          error: "codex command was not found",
          duration_ms: 12,
        },
      },
    });
    function SeededWrapper({ children }: { children: ReactNode }) {
      return (
        <ProvidersAndModelsProvider
          initialState={{
            agentAdapters: [
              {
                id: "codex",
                name: "Codex",
                kind: "acp",
                command: "codex",
                available: true,
                status: "available",
                path: "/Applications/Codex.app/Contents/Resources/codex",
                auth_status: "unknown",
                supports_authenticate: false,
                supports_logout: false,
              },
            ],
          }}
        >
          {children}
        </ProvidersAndModelsProvider>
      );
    }
    const { result } = renderHook(() => useProvidersAndModels(), { wrapper: SeededWrapper });

    await act(async () => {
      await result.current.actions.probeAgentAdapter("codex");
    });

    expect(result.current.state.agentAdapters[0]).toMatchObject({
      available: true,
      status: "available",
      path: "/Applications/Codex.app/Contents/Resources/codex",
    });
    expect(result.current.state.agentAdapters[0]?.error).toBeUndefined();
    expect(result.current.state.agentAdapterHealthByID.get("codex")).toMatchObject({
      status: "not_installed",
      stage: "resolve",
    });
  });

  it("recovers a stale missing catalog row through a fresh passive read after diagnostics", async () => {
    const discovered = {
      id: "codex",
      name: "Codex",
      kind: "acp",
      command: "codex",
      available: true,
      status: "available",
      path: "/Applications/Codex.app/Contents/Resources/codex",
      auth_status: "unknown",
      supports_authenticate: false,
      supports_logout: false,
    };
    const diagnostic = {
      ...discovered,
      adapter_version: "0.5.0",
      agent_version: "1.2.3",
      version_outside_range: true,
      auth_status: "ok",
      supports_authenticate: true,
      supports_logout: true,
      config_options: [
        {
          id: "model",
          name: "Model",
          type: "select",
          current_value: "sonnet",
          options: [{ value: "sonnet", name: "Sonnet" }],
        },
      ],
    };
    probeAgentAdapterMock.mockResolvedValueOnce({
      object: "agent_adapter_probe",
      data: {
        adapter: diagnostic,
        health: {
          adapter_id: "codex",
          status: "ready",
          stage: "ready",
          path: discovered.path,
          duration_ms: 42,
        },
      },
    });
    getAgentAdaptersMock.mockResolvedValueOnce({
      object: "agent_adapters",
      data: [discovered],
    });
    function SeededWrapper({ children }: { children: ReactNode }) {
      return (
        <ProvidersAndModelsProvider
          initialState={{
            agentAdapters: [
              {
                ...discovered,
                available: false,
                status: "missing",
                path: undefined,
                error: "codex command was not found",
              },
            ],
          }}
        >
          {children}
        </ProvidersAndModelsProvider>
      );
    }
    const { result } = renderHook(() => useProvidersAndModels(), { wrapper: SeededWrapper });

    await act(async () => {
      await result.current.actions.probeAgentAdapter("codex");
    });

    expect(getAgentAdaptersMock).toHaveBeenCalledTimes(1);
    expect(result.current.state.agentAdapters[0]).toMatchObject({
      available: true,
      status: "available",
      path: "/Applications/Codex.app/Contents/Resources/codex",
      adapter_version: "0.5.0",
      agent_version: "1.2.3",
      version_outside_range: true,
      auth_status: "ok",
      supports_authenticate: true,
      supports_logout: true,
      config_options: [expect.objectContaining({ id: "model", current_value: "sonnet" })],
    });
    expect(result.current.state.agentAdapterHealthByID.get("codex")).toMatchObject({
      status: "ready",
    });
  });
});

describe("refreshAgentAdapters", () => {
  it("keeps the newest passive discovery response when refreshes finish out of order", async () => {
    let resolveFirst: (value: unknown) => void = () => {};
    let resolveSecond: (value: unknown) => void = () => {};
    getAgentAdaptersMock
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveFirst = resolve;
        }),
      )
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveSecond = resolve;
        }),
      );
    const missing = {
      id: "codex",
      name: "Codex",
      kind: "acp",
      command: "codex",
      available: false,
      status: "missing",
      supports_authenticate: false,
      supports_logout: false,
    };
    const available = {
      ...missing,
      available: true,
      status: "available",
      path: "/Applications/Codex.app/Contents/Resources/codex",
    };
    const { result } = renderHook(() => useProvidersAndModels(), { wrapper: Wrapper });
    let first: Promise<unknown> | undefined;
    let second: Promise<unknown> | undefined;

    act(() => {
      first = result.current.actions.refreshAgentAdapters();
      second = result.current.actions.refreshAgentAdapters();
    });
    await act(async () => {
      resolveSecond({ object: "agent_adapters", data: [available] });
      await second;
      resolveFirst({ object: "agent_adapters", data: [missing] });
      await first;
    });

    expect(result.current.state.agentAdapters[0]).toMatchObject({
      available: true,
      status: "available",
      path: "/Applications/Codex.app/Contents/Resources/codex",
    });
  });

  it("does not let an in-flight catalog overwrite an explicit local projection", async () => {
    let resolveCatalog: (value: unknown) => void = () => {};
    getAgentAdaptersMock.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveCatalog = resolve;
      }),
    );
    const missing = {
      id: "codex",
      name: "Codex",
      kind: "acp",
      command: "codex",
      available: false,
      status: "missing",
      supports_authenticate: false,
      supports_logout: false,
    };
    const available = {
      ...missing,
      available: true,
      status: "available",
      path: "/Applications/Codex.app/Contents/Resources/codex",
    };
    const { result } = renderHook(() => useProvidersAndModels(), { wrapper: Wrapper });
    let refresh: Promise<unknown> | undefined;

    act(() => {
      refresh = result.current.actions.refreshAgentAdapters();
    });
    act(() => {
      result.current.actions.setAgentAdapters([available]);
    });
    await act(async () => {
      resolveCatalog({ object: "agent_adapters", data: [missing] });
      await refresh;
    });

    expect(result.current.state.agentAdapters[0]).toMatchObject(available);
  });

  it("does not let an older passive read overwrite a completed auth action", async () => {
    let resolveCatalog: (value: unknown) => void = () => {};
    getAgentAdaptersMock.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveCatalog = resolve;
      }),
    );
    const adapter = {
      id: "codex",
      name: "Codex",
      kind: "acp",
      command: "codex",
      available: true,
      status: "available",
      auth_status: "unauthenticated",
      auth_error: "Sign in required.",
      supports_authenticate: true,
      supports_logout: true,
    };
    function SeededWrapper({ children }: { children: ReactNode }) {
      return (
        <ProvidersAndModelsProvider
          initialState={{
            agentAdapters: [adapter],
            agentAdapterHealthByID: new Map([
              [
                "codex",
                {
                  adapter_id: "codex",
                  status: "auth_required",
                  stage: "authenticate",
                  duration_ms: 42,
                },
              ],
            ]),
          }}
        >
          {children}
        </ProvidersAndModelsProvider>
      );
    }
    const { result } = renderHook(() => useProvidersAndModels(), { wrapper: SeededWrapper });
    let refresh: Promise<unknown> | undefined;

    act(() => {
      refresh = result.current.actions.refreshAgentAdapters();
    });
    act(() => {
      result.current.actions.applyAgentAdapterAuthResult("codex", "ok");
    });
    await act(async () => {
      resolveCatalog({
        object: "agent_adapters",
        data: [{ ...adapter, auth_status: "unknown", auth_error: undefined }],
      });
      await refresh;
    });

    expect(result.current.state.agentAdapters[0]).toMatchObject({ auth_status: "ok" });
    expect(result.current.state.agentAdapters[0]?.auth_error).toBeUndefined();
    expect(result.current.state.agentAdapterHealthByID.has("codex")).toBe(false);
  });
});

describe("verifyModelToolSupport", () => {
  it("dedupes one provider/model diagnostic and refreshes the catalog projection", async () => {
    let resolveProbe: (value: unknown) => void = () => {};
    verifyModelToolSupportMock.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveProbe = resolve;
      }),
    );
    getProvidersMock.mockResolvedValue({ object: "provider_status", data: [] });
    getModelsMock.mockResolvedValue({
      object: "list",
      data: [
        {
          id: "custom-tool-model",
          owned_by: "Local Runtime",
          metadata: {
            provider: "Local Runtime",
            capabilities: {
              tool_calling: "basic",
              tool_verification: { status: "supported" },
            },
            readiness: { ready: true, routing_ready: true },
          },
        },
      ],
    });
    const ProviderModelWrapper = ({ children }: { children: ReactNode }) => (
      <ProvidersAndModelsProvider
        initialState={{
          models: [
            {
              id: "custom-tool-model",
              owned_by: "Local Runtime",
              metadata: {
                provider: "Local Runtime",
                capabilities: { tool_calling: "unknown" },
                readiness: { ready: true, routing_ready: true },
              },
            },
          ],
        }}
      >
        {children}
      </ProvidersAndModelsProvider>
    );
    const { result } = renderHook(() => useProvidersAndModels(), { wrapper: ProviderModelWrapper });
    let first: Promise<unknown> | undefined;
    let second: Promise<unknown> | undefined;

    act(() => {
      first = result.current.actions.verifyModelToolSupport("Local Runtime", "custom-tool-model");
      second = result.current.actions.verifyModelToolSupport("Local Runtime", "custom-tool-model");
    });

    const key = modelToolSupportKey("Local Runtime", "custom-tool-model");
    expect(verifyModelToolSupportMock).toHaveBeenCalledTimes(1);
    expect(verifyModelToolSupportMock).toHaveBeenCalledWith("Local Runtime", "custom-tool-model");
    expect(result.current.state.modelToolSupportLoadingByKey.has(key)).toBe(true);

    await act(async () => {
      resolveProbe({
        object: "model_tool_capability_probe",
        data: {
          provider: "Local Runtime",
          model: "custom-tool-model",
          capabilities: {
            tool_calling: "basic",
            tool_verification: { status: "supported" },
          },
          verification: { status: "supported" },
          performed: true,
        },
      });
      await Promise.all([first, second]);
    });

    expect(getProvidersMock).toHaveBeenCalledTimes(1);
    expect(getModelsMock).toHaveBeenCalledTimes(1);
    expect(result.current.state.modelToolSupportLoadingByKey.has(key)).toBe(false);
    expect(result.current.state.models[0]?.metadata?.capabilities).toMatchObject({
      tool_calling: "basic",
      tool_verification: { status: "supported" },
    });
  });

  it("keeps a successful probe result when the best-effort catalog refresh fails", async () => {
    verifyModelToolSupportMock.mockResolvedValueOnce({
      object: "model_tool_capability_probe",
      data: {
        provider: "Local Runtime",
        model: "custom-tool-model",
        capabilities: {
          tool_calling: "basic",
          tool_verification: {
            status: "supported",
            checked_at: "2026-07-21T10:00:00Z",
          },
        },
        verification: {
          status: "supported",
          checked_at: "2026-07-21T10:00:00Z",
        },
        performed: true,
      },
    });
    getProvidersMock.mockRejectedValueOnce(new Error("provider status unavailable"));
    getModelsMock.mockRejectedValueOnce(new Error("model catalog unavailable"));
    const ProviderModelWrapper = ({ children }: { children: ReactNode }) => (
      <ProvidersAndModelsProvider
        initialState={{
          models: [
            {
              id: "custom-tool-model",
              owned_by: "Local Runtime",
              metadata: {
                provider: "Local Runtime",
                capabilities: { tool_calling: "unknown" },
                readiness: { ready: true, routing_ready: true },
              },
            },
          ],
        }}
      >
        {children}
      </ProvidersAndModelsProvider>
    );
    const { result } = renderHook(() => useProvidersAndModels(), { wrapper: ProviderModelWrapper });

    let outcome: unknown;
    await act(async () => {
      outcome = await result.current.actions.verifyModelToolSupport(
        "Local Runtime",
        "custom-tool-model",
      );
    });

    expect(outcome).toMatchObject({ ok: true });
    expect(warnMock).toHaveBeenCalledWith(
      "providersAndModels.refresh.failed",
      expect.objectContaining({
        providers: "provider status unavailable",
        models: "model catalog unavailable",
      }),
    );
    expect(result.current.state.models[0]?.metadata?.capabilities).toMatchObject({
      tool_calling: "basic",
      tool_verification: { status: "supported" },
    });
  });

  it("does not let an older catalog refresh erase a completed verification", async () => {
    let resolveStaleModels: (value: unknown) => void = () => {};
    getProvidersMock.mockResolvedValue({ object: "provider_status", data: [] });
    getModelsMock
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveStaleModels = resolve;
        }),
      )
      .mockResolvedValueOnce({
        object: "list",
        data: [
          {
            id: "custom-tool-model",
            owned_by: "Local Runtime",
            metadata: {
              provider: "Local Runtime",
              capabilities: {
                tool_calling: "basic",
                tool_verification: { status: "supported" },
              },
              readiness: { ready: true, routing_ready: true },
            },
          },
        ],
      });
    verifyModelToolSupportMock.mockResolvedValueOnce({
      object: "model_tool_capability_probe",
      data: {
        provider: "Local Runtime",
        model: "custom-tool-model",
        capabilities: {
          tool_calling: "basic",
          tool_verification: { status: "supported" },
        },
        verification: { status: "supported" },
        performed: true,
      },
    });
    const ProviderModelWrapper = ({ children }: { children: ReactNode }) => (
      <ProvidersAndModelsProvider
        initialState={{
          models: [
            {
              id: "custom-tool-model",
              owned_by: "Local Runtime",
              metadata: {
                provider: "Local Runtime",
                capabilities: { tool_calling: "unknown" },
                readiness: { ready: true, routing_ready: true },
              },
            },
          ],
        }}
      >
        {children}
      </ProvidersAndModelsProvider>
    );
    const { result } = renderHook(() => useProvidersAndModels(), { wrapper: ProviderModelWrapper });

    let staleRefresh: Promise<void> | undefined;
    act(() => {
      staleRefresh = result.current.actions.refreshProviders();
    });
    await waitFor(() => {
      expect(getModelsMock).toHaveBeenCalledTimes(1);
    });

    await act(async () => {
      await result.current.actions.verifyModelToolSupport("Local Runtime", "custom-tool-model");
    });
    expect(result.current.state.models[0]?.metadata?.capabilities?.tool_calling).toBe("basic");

    await act(async () => {
      resolveStaleModels({
        object: "list",
        data: [
          {
            id: "custom-tool-model",
            owned_by: "Local Runtime",
            metadata: {
              provider: "Local Runtime",
              capabilities: { tool_calling: "unknown" },
              readiness: { ready: true, routing_ready: true },
            },
          },
        ],
      });
      await staleRefresh;
    });

    expect(result.current.state.models[0]?.metadata?.capabilities).toMatchObject({
      tool_calling: "basic",
      tool_verification: { status: "supported" },
    });
  });

  it("applies the newest catalog refresh when an older response returns first", async () => {
    let resolveOlderModels: (value: unknown) => void = () => {};
    let resolveNewerModels: (value: unknown) => void = () => {};
    getProvidersMock.mockResolvedValue({ object: "provider_status", data: [] });
    getModelsMock
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveOlderModels = resolve;
        }),
      )
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveNewerModels = resolve;
        }),
      );
    const ProviderModelWrapper = ({ children }: { children: ReactNode }) => (
      <ProvidersAndModelsProvider
        initialState={{
          models: [
            {
              id: "custom-tool-model",
              owned_by: "Local Runtime",
              metadata: {
                provider: "Local Runtime",
                capabilities: { tool_calling: "unknown" },
                readiness: { ready: true, routing_ready: true },
              },
            },
          ],
        }}
      >
        {children}
      </ProvidersAndModelsProvider>
    );
    const { result } = renderHook(() => useProvidersAndModels(), { wrapper: ProviderModelWrapper });

    let olderRefresh: Promise<void> | undefined;
    act(() => {
      olderRefresh = result.current.actions.refreshProviders();
    });
    await waitFor(() => {
      expect(getModelsMock).toHaveBeenCalledTimes(1);
    });

    let newerRefresh: Promise<void> | undefined;
    act(() => {
      newerRefresh = result.current.actions.refreshProviders();
    });
    await waitFor(() => {
      expect(getModelsMock).toHaveBeenCalledTimes(2);
    });

    await act(async () => {
      resolveOlderModels({
        object: "list",
        data: [
          {
            id: "custom-tool-model",
            owned_by: "Local Runtime",
            metadata: {
              provider: "Local Runtime",
              capabilities: { tool_calling: "none" },
              readiness: { ready: true, routing_ready: true },
            },
          },
        ],
      });
      await olderRefresh;
    });
    expect(result.current.state.models[0]?.metadata?.capabilities?.tool_calling).toBe("unknown");

    await act(async () => {
      resolveNewerModels({
        object: "list",
        data: [
          {
            id: "custom-tool-model",
            owned_by: "Local Runtime",
            metadata: {
              provider: "Local Runtime",
              capabilities: {
                tool_calling: "basic",
                tool_verification: { status: "supported" },
              },
              readiness: { ready: true, routing_ready: true },
            },
          },
        ],
      });
      await newerRefresh;
    });
    expect(result.current.state.models[0]?.metadata?.capabilities).toMatchObject({
      tool_calling: "basic",
      tool_verification: { status: "supported" },
    });
  });
});

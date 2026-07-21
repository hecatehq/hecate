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
const probeAgentAdapterMock = vi.fn();
const verifyModelToolSupportMock = vi.fn();
const warnMock = vi.fn();

vi.mock("../../lib/api", () => ({
  getProviders: (...args: unknown[]) => getProvidersMock(...args),
  getModels: (...args: unknown[]) => getModelsMock(...args),
  getProviderPresets: (...args: unknown[]) => getProviderPresetsMock(...args),
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

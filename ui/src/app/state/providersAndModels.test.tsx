import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  ProvidersAndModelsProvider,
  useEnsureProviderPresetsLoaded,
  useProvidersAndModels,
} from "./providersAndModels";
import type { ReactNode } from "react";

const getProviderPresetsMock = vi.fn();
const probeAgentAdapterMock = vi.fn();
const warnMock = vi.fn();

vi.mock("../../lib/api", () => ({
  getProviderPresets: (...args: unknown[]) => getProviderPresetsMock(...args),
  probeAgentAdapter: (...args: unknown[]) => probeAgentAdapterMock(...args),
  // Stubs for other api symbols the slice imports — unused in these tests.
  getProviders: vi.fn(),
  getModels: vi.fn(),
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
  getProviderPresetsMock.mockReset();
  probeAgentAdapterMock.mockReset();
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

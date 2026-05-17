import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  ProvidersAndModelsProvider,
  useEnsureProviderPresetsLoaded,
  useProvidersAndModels,
} from "./providersAndModels";
import type { ReactNode } from "react";

const getProviderPresetsMock = vi.fn();
const warnMock = vi.fn();

vi.mock("../../lib/api", () => ({
  getProviderPresets: (...args: unknown[]) => getProviderPresetsMock(...args),
  // Stubs for other api symbols the slice imports — unused in these tests.
  getProviders: vi.fn(),
  getModels: vi.fn(),
  probeAgentAdapter: vi.fn(),
  setAgentAdapterCredential: vi.fn(),
  deleteAgentAdapterCredential: vi.fn(),
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
  warnMock.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("useEnsureProviderPresetsLoaded", () => {
  it("fetches presets on first mount and flips providerPresetsLoaded=true", async () => {
    const presets = { object: "list", data: [{ id: "anthropic", name: "Anthropic", kind: "cloud" }] } as any;
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

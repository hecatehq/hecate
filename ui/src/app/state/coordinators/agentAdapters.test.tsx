import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

import { useAgentAdapterActions } from "./agentAdapters";
import { ProvidersAndModelsProvider, useProvidersAndModels } from "../providersAndModels";

const authenticateAgentAdapterMock = vi.fn();
const logoutAgentAdapterMock = vi.fn();

vi.mock("../../../lib/api", () => ({
  getProviders: vi.fn(),
  getModels: vi.fn(),
  getProviderPresets: vi.fn(),
  probeAgentAdapter: vi.fn(),
  authenticateAgentAdapter: (...args: unknown[]) => authenticateAgentAdapterMock(...args),
  logoutAgentAdapter: (...args: unknown[]) => logoutAgentAdapterMock(...args),
}));

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <ProvidersAndModelsProvider
      initialState={{
        agentAdapterHealthByID: new Map([
          [
            "codex",
            {
              adapter_id: "codex",
              status: "ready",
              stage: "ready",
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

beforeEach(() => {
  authenticateAgentAdapterMock.mockReset();
  logoutAgentAdapterMock.mockReset();
});

describe("useAgentAdapterActions", () => {
  it("authenticates an adapter and clears stale cached health", async () => {
    authenticateAgentAdapterMock.mockResolvedValue({
      object: "agent_adapter_authenticate",
      data: {
        adapter_id: "codex",
        status: "authenticated",
        method_id: "agent-login",
        duration_ms: 12,
      },
    });
    const notices: Array<[string, string]> = [];

    const { result } = renderHook(
      () => ({
        adapterActions: useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
        providersAndModels: useProvidersAndModels(),
      }),
      { wrapper: Wrapper },
    );

    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(true);

    await act(async () => {
      await result.current.adapterActions.authenticateAgentAdapter("codex");
    });

    expect(authenticateAgentAdapterMock).toHaveBeenCalledWith("codex");
    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(false);
    expect(notices).toContainEqual(["success", "External agent sign-in completed."]);
  });

  it("keeps cached health when authenticate fails", async () => {
    authenticateAgentAdapterMock.mockRejectedValue(new Error("authenticate failed"));
    const notices: Array<[string, string]> = [];

    const { result } = renderHook(
      () => ({
        adapterActions: useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
        providersAndModels: useProvidersAndModels(),
      }),
      { wrapper: Wrapper },
    );

    await act(async () => {
      await result.current.adapterActions.authenticateAgentAdapter("codex");
    });

    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(true);
    expect(notices).toContainEqual(["error", "authenticate failed"]);
  });

  it("logs out an adapter and clears stale cached health", async () => {
    logoutAgentAdapterMock.mockResolvedValue({
      object: "agent_adapter_logout",
      data: { adapter_id: "codex", status: "logged_out", duration_ms: 12 },
    });
    const notices: Array<[string, string]> = [];

    const { result } = renderHook(
      () => ({
        adapterActions: useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
        providersAndModels: useProvidersAndModels(),
      }),
      { wrapper: Wrapper },
    );

    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(true);

    await act(async () => {
      await result.current.adapterActions.logoutAgentAdapter("codex");
    });

    expect(logoutAgentAdapterMock).toHaveBeenCalledWith("codex");
    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(false);
    expect(notices).toContainEqual(["success", "External agent signed out."]);
  });

  it("keeps cached health when logout fails", async () => {
    logoutAgentAdapterMock.mockRejectedValue(new Error("logout failed"));
    const notices: Array<[string, string]> = [];

    const { result } = renderHook(
      () => ({
        adapterActions: useAgentAdapterActions({
          setNoticeMessage: (kind, message) => notices.push([kind, message]),
        }),
        providersAndModels: useProvidersAndModels(),
      }),
      { wrapper: Wrapper },
    );

    await act(async () => {
      await result.current.adapterActions.logoutAgentAdapter("codex");
    });

    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(true);
    expect(notices).toContainEqual(["error", "logout failed"]);
  });
});

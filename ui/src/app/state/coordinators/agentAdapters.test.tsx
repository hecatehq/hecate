import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

import { useAgentAdapterActions } from "./agentAdapters";
import { ProvidersAndModelsProvider, useProvidersAndModels } from "../providersAndModels";
import { resolveExternalAgentReadiness } from "../../../lib/external-agent-readiness";

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

function AuthRequiredWrapper({ children }: { children: ReactNode }) {
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
            auth_status: "unauthenticated",
            auth_error: "Sign in required.",
            supports_authenticate: true,
            supports_logout: true,
          },
        ],
        agentAdapterHealthByID: new Map([
          [
            "codex",
            {
              adapter_id: "codex",
              status: "auth_required",
              stage: "authenticate",
              error: "Sign in required.",
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

function ReadyWrapper({ children }: { children: ReactNode }) {
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
            auth_status: "ok",
            supports_authenticate: true,
            supports_logout: true,
          },
        ],
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
  it("authenticates an adapter and atomically replaces stale auth diagnostics", async () => {
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
      { wrapper: AuthRequiredWrapper },
    );

    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(true);

    await act(async () => {
      await result.current.adapterActions.authenticateAgentAdapter("codex");
    });

    expect(authenticateAgentAdapterMock).toHaveBeenCalledWith("codex");
    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(false);
    expect(result.current.providersAndModels.state.agentAdapters[0]).toMatchObject({
      auth_status: "ok",
    });
    expect(result.current.providersAndModels.state.agentAdapters[0]?.auth_error).toBeUndefined();
    expect(
      resolveExternalAgentReadiness(result.current.providersAndModels.state.agentAdapters[0], null),
    ).toMatchObject({ kind: "unverified", authStatus: "ok" });
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
      { wrapper: AuthRequiredWrapper },
    );

    await act(async () => {
      await result.current.adapterActions.authenticateAgentAdapter("codex");
    });

    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(true);
    expect(result.current.providersAndModels.state.agentAdapters[0]).toMatchObject({
      auth_status: "unauthenticated",
      auth_error: "Sign in required.",
    });
    expect(notices).toContainEqual(["error", "authenticate failed"]);
  });

  it("logs out an adapter and atomically replaces stale auth diagnostics", async () => {
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
      { wrapper: ReadyWrapper },
    );

    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(true);

    await act(async () => {
      await result.current.adapterActions.logoutAgentAdapter("codex");
    });

    expect(logoutAgentAdapterMock).toHaveBeenCalledWith("codex");
    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(false);
    expect(result.current.providersAndModels.state.agentAdapters[0]).toMatchObject({
      auth_status: "unauthenticated",
    });
    expect(result.current.providersAndModels.state.agentAdapters[0]?.auth_error).toBeUndefined();
    expect(
      resolveExternalAgentReadiness(result.current.providersAndModels.state.agentAdapters[0], null),
    ).toMatchObject({ kind: "sign_in", authStatus: "unauthenticated" });
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
      { wrapper: ReadyWrapper },
    );

    await act(async () => {
      await result.current.adapterActions.logoutAgentAdapter("codex");
    });

    expect(result.current.providersAndModels.state.agentAdapterHealthByID.has("codex")).toBe(true);
    expect(result.current.providersAndModels.state.agentAdapters[0]).toMatchObject({
      auth_status: "ok",
    });
    expect(notices).toContainEqual(["error", "logout failed"]);
  });
});

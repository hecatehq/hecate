import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useDashboardActions } from "./dashboard";
import { ChatProvider } from "../chat";
import { ProvidersAndModelsProvider, useProvidersAndModels } from "../providersAndModels";
import { RuntimeProvider } from "../runtime";
import type { AgentAdapterResponse } from "../../../types/agent-adapter";
import type { ModelResponse } from "../../../types/model";

vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return {
    ...actual,
    getHealth: vi.fn(),
    getSession: vi.fn(),
    getModels: vi.fn(),
    getProviders: vi.fn(),
    getAgentAdapters: vi.fn(),
    getChatSessions: vi.fn(),
    getSettingsConfig: vi.fn(),
    getRuntimeStats: vi.fn(),
  };
});

import * as api from "../../../lib/api";

const initialModels = [
  {
    id: "custom-tool-model",
    owned_by: "Local Runtime",
    metadata: {
      provider: "Local Runtime",
      capabilities: { tool_calling: "unknown" },
      readiness: { ready: true, routing_ready: true },
    },
  },
];

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <RuntimeProvider>
      <ProvidersAndModelsProvider initialState={{ models: initialModels }}>
        <ChatProvider>{children}</ChatProvider>
      </ProvidersAndModelsProvider>
    </RuntimeProvider>
  );
}

function useDashboardHarness() {
  const providersAndModels = useProvidersAndModels();
  const dashboard = useDashboardActions({
    settingsConfig: null,
    setSettingsConfig: () => {},
    setSettingsError: () => {},
    applyChatSession: () => true,
    syncHecateSelectionFromSession: () => {},
    refreshRuntimeState: async () => {},
  });
  return { dashboard, providersAndModels };
}

function setupDashboardReads() {
  vi.mocked(api.getHealth).mockResolvedValue({ status: "ok", time: "2026-07-21T00:00:00Z" });
  vi.mocked(api.getSession).mockResolvedValue({
    object: "session",
    data: {
      role: "operator",
      runtime_host: {
        id: "runtime_1",
        label: "Test host",
        runtime_mode: "local",
        operator_access: "local_operator",
        local_only_actions_available: true,
      },
    },
  });
  vi.mocked(api.getProviders).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getAgentAdapters).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getChatSessions).mockResolvedValue({ object: "chat_sessions", data: [] });
  vi.mocked(api.getSettingsConfig).mockResolvedValue({
    object: "settings",
    data: { backend: "memory", providers: [], policy_rules: [], events: [] },
  });
  vi.mocked(api.getRuntimeStats).mockResolvedValue({
    object: "runtime_stats",
    data: {},
  } as never);
}

beforeEach(() => {
  vi.clearAllMocks();
  setupDashboardReads();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("useDashboardActions model catalog ownership", () => {
  it("publishes dashboard catalog results through the providers/models slice", async () => {
    vi.mocked(api.getModels).mockResolvedValue({
      object: "list",
      data: [
        {
          ...initialModels[0],
          metadata: {
            ...initialModels[0].metadata,
            capabilities: { tool_calling: "basic" },
          },
        },
      ],
    });
    const { result } = renderHook(() => useDashboardHarness(), { wrapper: Wrapper });

    await act(async () => {
      await result.current.dashboard.loadDashboard();
    });

    expect(result.current.providersAndModels.state.models[0]?.metadata?.capabilities).toMatchObject(
      {
        tool_calling: "basic",
      },
    );
  });

  it("does not let a stale dashboard catalog block a newer provider refresh", async () => {
    let resolveDashboardModels: (value: ModelResponse) => void = () => {};
    let resolveRefreshModels: (value: ModelResponse) => void = () => {};
    vi.mocked(api.getModels)
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveDashboardModels = resolve;
        }),
      )
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveRefreshModels = resolve;
        }),
      );
    const { result } = renderHook(() => useDashboardHarness(), { wrapper: Wrapper });

    let dashboardLoad: Promise<void> | undefined;
    act(() => {
      dashboardLoad = result.current.dashboard.loadDashboard();
    });
    await waitFor(() => {
      expect(api.getModels).toHaveBeenCalledTimes(1);
    });

    let providerRefresh: Promise<void> | undefined;
    act(() => {
      providerRefresh = result.current.dashboard.refreshProviders();
    });
    await waitFor(() => {
      expect(api.getModels).toHaveBeenCalledTimes(2);
    });

    await act(async () => {
      resolveDashboardModels({
        object: "list",
        data: [
          {
            ...initialModels[0],
            metadata: {
              ...initialModels[0].metadata,
              capabilities: { tool_calling: "none" },
            },
          },
        ],
      });
      await dashboardLoad;
    });
    expect(
      result.current.providersAndModels.state.models[0]?.metadata?.capabilities?.tool_calling,
    ).toBe("unknown");

    await act(async () => {
      resolveRefreshModels({
        object: "list",
        data: [
          {
            ...initialModels[0],
            metadata: {
              ...initialModels[0].metadata,
              capabilities: {
                tool_calling: "basic",
                tool_verification: { status: "supported" },
              },
            },
          },
        ],
      });
      await providerRefresh;
    });

    expect(result.current.providersAndModels.state.models[0]?.metadata?.capabilities).toMatchObject(
      {
        tool_calling: "basic",
        tool_verification: { status: "supported" },
      },
    );
  });

  it("does not let a stale dashboard adapter catalog overwrite a newer refresh", async () => {
    let resolveDashboardAdapters: (value: AgentAdapterResponse) => void = () => {};
    let resolveRefreshAdapters: (value: AgentAdapterResponse) => void = () => {};
    vi.mocked(api.getModels).mockResolvedValue({ object: "list", data: initialModels });
    vi.mocked(api.getAgentAdapters)
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveDashboardAdapters = resolve;
        }),
      )
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveRefreshAdapters = resolve;
        }),
      );
    const missing = {
      id: "claude_code",
      name: "Claude Code",
      kind: "acp",
      command: "claude",
      available: false,
      status: "missing",
      supports_authenticate: false,
      supports_logout: false,
    };
    const available = {
      ...missing,
      available: true,
      status: "available",
      path: "/Applications/Claude.app/Contents/MacOS/claude",
    };
    const { result } = renderHook(() => useDashboardHarness(), { wrapper: Wrapper });

    let dashboardLoad: Promise<void> | undefined;
    act(() => {
      dashboardLoad = result.current.dashboard.loadDashboard();
    });
    await waitFor(() => {
      expect(api.getAgentAdapters).toHaveBeenCalledTimes(1);
    });

    let refresh: ReturnType<
      typeof result.current.providersAndModels.actions.refreshAgentAdapters
    > | null = null;
    act(() => {
      refresh = result.current.providersAndModels.actions.refreshAgentAdapters();
    });
    await waitFor(() => {
      expect(api.getAgentAdapters).toHaveBeenCalledTimes(2);
    });

    await act(async () => {
      resolveRefreshAdapters({ object: "agent_adapters", data: [available] });
      await refresh;
    });
    expect(result.current.providersAndModels.state.agentAdapters[0]).toMatchObject(available);

    await act(async () => {
      resolveDashboardAdapters({ object: "agent_adapters", data: [missing] });
      await dashboardLoad;
    });
    expect(result.current.providersAndModels.state.agentAdapters[0]).toMatchObject(available);
  });
});

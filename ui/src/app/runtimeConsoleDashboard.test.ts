import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../lib/api";
import { deriveSessionState, resolveDashboardSnapshot } from "./runtimeConsoleDashboard";

vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return {
    ...actual,
    getHealth: vi.fn(),
    getSession: vi.fn(),
    getModels: vi.fn(),
    getProviders: vi.fn(),
    getProviderPresets: vi.fn(),
    getAgentAdapters: vi.fn(),
    getUsageSummary: vi.fn(),
    getUsageEvents: vi.fn(),
    getChatSessions: vi.fn(),
    getChatSession: vi.fn(),
    getAgentChatSessions: vi.fn(),
    getAgentChatSession: vi.fn(),
    getSettingsConfig: vi.fn(),
    getRetentionRuns: vi.fn(),
    getRuntimeStats: vi.fn(),
  };
});

import * as api from "../lib/api";

const emptyPrev = {
  providers: [],
  agentAdapters: [],
  usageSummary: null,
  usageEvents: [],
  chatSessions: [],
  activeChatSession: null,
  agentChatSessions: [],
  activeAgentChatSession: null,
  settingsConfig: null,
  retentionRuns: [],
  retentionLastRun: null,
};

function setupAllResolved(overrides: Record<string, unknown> = {}) {
  vi.mocked(api.getHealth).mockResolvedValue({ status: "ok", time: "2026-05-05T00:00:00Z" });
  vi.mocked(api.getSession).mockResolvedValue({ object: "session", data: { role: "operator" } });
  vi.mocked(api.getModels).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getProviders).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getProviderPresets).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getAgentAdapters).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getUsageSummary).mockResolvedValue({
    object: "usage_summary",
    data: { key: "global", scope: "global", backend: "memory", used_micros_usd: 0, used_usd: "$0.000000" },
  });
  vi.mocked(api.getUsageEvents).mockResolvedValue({ object: "usage_events", data: [] });
  vi.mocked(api.getChatSessions).mockResolvedValue({ object: "list", data: [], has_more: false });
  vi.mocked(api.getAgentChatSessions).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getSettingsConfig).mockResolvedValue({
    object: "settings",
    data: { backend: "memory", providers: [], policy_rules: [], events: [] },
  });
  vi.mocked(api.getRetentionRuns).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getRuntimeStats).mockResolvedValue({
    object: "runtime_stats",
    data: { agent_adapter_approval_mode: "prompt" } as never,
  });
  Object.assign(api, overrides);
}

beforeEach(() => {
  setupAllResolved();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("deriveSessionState", () => {
  it("returns the local label regardless of input", () => {
    expect(deriveSessionState(null)).toEqual({ label: "Local" });
    expect(deriveSessionState({ role: "operator" })).toEqual({ label: "Local" });
  });
});

describe("resolveDashboardSnapshot", () => {
  it("hydrates from API responses when everything resolves", async () => {
    vi.mocked(api.getModels).mockResolvedValue({
      object: "list",
      data: [{ id: "gpt-4o", owned_by: "openai" }],
    });
    vi.mocked(api.getAgentAdapters).mockResolvedValue({
      object: "list",
      data: [{ id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available" }],
    });
    vi.mocked(api.getRuntimeStats).mockResolvedValue({
      object: "runtime_stats",
      data: { agent_adapter_approval_mode: "auto" } as never,
    });

    const snapshot = await resolveDashboardSnapshot({
      activeChatSessionID: "",
      activeAgentChatSessionID: "",
      previous: emptyPrev,
    });

    expect(snapshot.health.status).toBe("ok");
    expect(snapshot.models).toHaveLength(1);
    expect(snapshot.agentAdapters).toHaveLength(1);
    expect(snapshot.agentAdapterApprovalMode).toBe("auto");
  });

  it("falls back to previous state when an endpoint rejects", async () => {
    vi.mocked(api.getAgentAdapters).mockRejectedValue(new Error("network"));
    const previous = {
      ...emptyPrev,
      agentAdapters: [{ id: "codex", name: "Codex", kind: "acp", command: "codex-acp", available: true, status: "available" }],
    };

    const snapshot = await resolveDashboardSnapshot({
      activeChatSessionID: "",
      activeAgentChatSessionID: "",
      previous,
    });

    expect(snapshot.agentAdapters).toEqual(previous.agentAdapters);
  });

  it("loads the active chat session when its id is in the returned list", async () => {
    vi.mocked(api.getChatSessions).mockResolvedValue({
      object: "list",
      data: [
        { id: "s1", title: "first", message_count: 0, provider_call_count: 0 },
      ],
      has_more: false,
    });
    vi.mocked(api.getChatSession).mockResolvedValue({
      object: "chat_session",
      data: { id: "s1", title: "first" },
    });

    const snapshot = await resolveDashboardSnapshot({
      activeChatSessionID: "s1",
      activeAgentChatSessionID: "",
      previous: emptyPrev,
    });

    expect(api.getChatSession).toHaveBeenCalledWith("s1");
    expect(snapshot.activeChatSessionID).toBe("s1");
    expect(snapshot.activeChatSession?.title).toBe("first");
  });

  it("auto-selects the first chat session when activeChatSessionID is empty", async () => {
    vi.mocked(api.getChatSessions).mockResolvedValue({
      object: "list",
      data: [
        { id: "s1", title: "first", message_count: 0, provider_call_count: 0 },
      ],
      has_more: false,
    });
    vi.mocked(api.getChatSession).mockResolvedValue({
      object: "chat_session",
      data: { id: "s1", title: "first" },
    });

    const snapshot = await resolveDashboardSnapshot({
      activeChatSessionID: "",
      activeAgentChatSessionID: "",
      previous: emptyPrev,
    });

    expect(snapshot.activeChatSessionID).toBe("s1");
  });

  it("clears the active agent chat session when getAgentChatSession 404s", async () => {
    vi.mocked(api.getAgentChatSessions).mockResolvedValue({
      object: "list",
      data: [
        { id: "ac1", title: "old", adapter_id: "codex", workspace: "/repo", status: "running", message_count: 0 },
      ],
    });
    vi.mocked(api.getAgentChatSession).mockRejectedValue(new ApiError("not found", 404, "agent_chat session not found"));

    const snapshot = await resolveDashboardSnapshot({
      activeChatSessionID: "",
      activeAgentChatSessionID: "ac1",
      previous: emptyPrev,
    });

    expect(snapshot.activeAgentChatSessionID).toBe("");
    expect(snapshot.activeAgentChatSession).toBeNull();
  });

  it("preserves the previous agent chat session on non-404 errors", async () => {
    vi.mocked(api.getAgentChatSessions).mockResolvedValue({
      object: "list",
      data: [
        { id: "ac1", title: "current", adapter_id: "codex", workspace: "/repo", status: "running", message_count: 0 },
      ],
    });
    vi.mocked(api.getAgentChatSession).mockRejectedValue(new Error("network"));
    const previous = {
      ...emptyPrev,
      activeAgentChatSession: {
        id: "ac1",
        title: "stale",
        adapter_id: "codex",
        workspace: "/repo",
        status: "running",
      },
    };

    const snapshot = await resolveDashboardSnapshot({
      activeChatSessionID: "",
      activeAgentChatSessionID: "ac1",
      previous,
    });

    expect(snapshot.activeAgentChatSessionID).toBe("ac1");
    expect(snapshot.activeAgentChatSession?.title).toBe("stale");
  });

  it("skips the providers fetch when no providers are configured in the settings", async () => {
    vi.mocked(api.getSettingsConfig).mockResolvedValue({
      object: "settings",
      data: { backend: "memory", providers: [], policy_rules: [], events: [] },
    });
    await resolveDashboardSnapshot({
      activeChatSessionID: "",
      activeAgentChatSessionID: "",
      previous: emptyPrev,
    });
    expect(api.getProviders).not.toHaveBeenCalled();
  });

  it("calls the providers fetch when settings has at least one configured provider", async () => {
    vi.mocked(api.getSettingsConfig).mockResolvedValue({
      object: "settings",
      data: {
        backend: "memory",
        providers: [{ name: "openai" } as never],
        policy_rules: [],
        events: [],
      },
    });
    await resolveDashboardSnapshot({
      activeChatSessionID: "",
      activeAgentChatSessionID: "",
      previous: emptyPrev,
    });
    expect(api.getProviders).toHaveBeenCalled();
  });

  it("throws when the health probe rejects (the dashboard cannot proceed without it)", async () => {
    vi.mocked(api.getHealth).mockRejectedValue(new Error("backend down"));
    await expect(
      resolveDashboardSnapshot({
        activeChatSessionID: "",
        activeAgentChatSessionID: "",
        previous: emptyPrev,
      }),
    ).rejects.toThrow(/runtime console/);
  });
});

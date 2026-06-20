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
    getChatSessions: vi.fn(),
    getChatSession: vi.fn(),
    getSettingsConfig: vi.fn(),
    getRuntimeStats: vi.fn(),
  };
});

import * as api from "../lib/api";

const emptyPrev = {
  providers: [],
  agentAdapters: [],
  chatSessions: [],
  activeChatSession: null,
  settingsConfig: null,
};

function setupAllResolved() {
  vi.mocked(api.getHealth).mockResolvedValue({ status: "ok", time: "2026-05-05T00:00:00Z" });
  vi.mocked(api.getSession).mockResolvedValue({ object: "session", data: { role: "operator" } });
  vi.mocked(api.getModels).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getProviders).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getProviderPresets).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getAgentAdapters).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getChatSessions).mockResolvedValue({ object: "list", data: [] });
  vi.mocked(api.getSettingsConfig).mockResolvedValue({
    object: "settings",
    data: { backend: "memory", providers: [], policy_rules: [], events: [] },
  });
  vi.mocked(api.getRuntimeStats).mockResolvedValue({
    object: "runtime_stats",
    data: { agent_adapter_approval_mode: "prompt" } as never,
  });
}

beforeEach(() => {
  setupAllResolved();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("deriveSessionState", () => {
  it("returns the local label without cloud identity", () => {
    expect(deriveSessionState(null)).toEqual({ label: "Local" });
    expect(deriveSessionState({ role: "operator" })).toEqual({ label: "Local" });
  });

  it("returns the hosted label with cloud identity", () => {
    expect(
      deriveSessionState({
        role: "operator",
        remote_identity: {
          actor_id: "actor_1",
          org_id: "org_1",
          project_id: "proj_1",
          runtime_id: "rt_1",
        },
      }),
    ).toEqual({ label: "Hosted" });
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
      data: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          supports_authenticate: true,
          supports_logout: true,
        },
      ],
    });
    vi.mocked(api.getRuntimeStats).mockResolvedValue({
      object: "runtime_stats",
      data: {
        agent_adapter_approval_mode: "auto",
        rtk_available: true,
        rtk_path: "/usr/local/bin/rtk",
      } as never,
    });

    const snapshot = await resolveDashboardSnapshot({
      activeChatSessionID: "",
      previous: emptyPrev,
    });

    expect(snapshot.health.status).toBe("ok");
    expect(snapshot.models).toHaveLength(1);
    expect(snapshot.agentAdapters).toHaveLength(1);
    expect(snapshot.agentAdapterApprovalMode).toBe("auto");
    expect(snapshot.rtkAvailable).toBe(true);
    expect(snapshot.rtkPath).toBe("/usr/local/bin/rtk");
  });

  it("falls back to previous state when an endpoint rejects", async () => {
    vi.mocked(api.getAgentAdapters).mockRejectedValue(new Error("network"));
    const previous = {
      ...emptyPrev,
      agentAdapters: [
        {
          id: "codex",
          name: "Codex",
          kind: "acp",
          command: "codex-acp-adapter",
          available: true,
          status: "available",
          supports_authenticate: true,
          supports_logout: true,
        },
      ],
    };

    const snapshot = await resolveDashboardSnapshot({
      activeChatSessionID: "",
      previous,
    });

    expect(snapshot.agentAdapters).toEqual(previous.agentAdapters);
  });

  it("clears the active agent chat session when getChatSession 404s", async () => {
    vi.mocked(api.getChatSessions).mockResolvedValue({
      object: "list",
      data: [
        {
          id: "ac1",
          title: "old",
          agent_id: "codex",
          workspace: "/repo",
          status: "running",
          message_count: 0,
        },
      ],
    });
    vi.mocked(api.getChatSession).mockRejectedValue(
      new ApiError("not found", 404, "chat session not found"),
    );

    const snapshot = await resolveDashboardSnapshot({
      activeChatSessionID: "ac1",
      previous: emptyPrev,
    });

    expect(snapshot.activeChatSessionID).toBe("");
    expect(snapshot.activeChatSession).toBeNull();
  });

  it("preserves the previous agent chat session on non-404 errors", async () => {
    vi.mocked(api.getChatSessions).mockResolvedValue({
      object: "list",
      data: [
        {
          id: "ac1",
          title: "current",
          agent_id: "codex",
          workspace: "/repo",
          status: "running",
          message_count: 0,
        },
      ],
    });
    vi.mocked(api.getChatSession).mockRejectedValue(new Error("network"));
    const previous = {
      ...emptyPrev,
      activeChatSession: {
        id: "ac1",
        title: "stale",
        agent_id: "codex",
        workspace: "/repo",
        status: "running",
      },
    };

    const snapshot = await resolveDashboardSnapshot({
      activeChatSessionID: "ac1",
      previous,
    });

    expect(snapshot.activeChatSessionID).toBe("ac1");
    expect(snapshot.activeChatSession?.title).toBe("stale");
  });

  it("skips the providers fetch when no providers are configured in the settings", async () => {
    vi.mocked(api.getSettingsConfig).mockResolvedValue({
      object: "settings",
      data: { backend: "memory", providers: [], policy_rules: [], events: [] },
    });
    await resolveDashboardSnapshot({
      activeChatSessionID: "",
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
      previous: emptyPrev,
    });
    expect(api.getProviders).toHaveBeenCalled();
  });

  it("falls back to previous providers when getSettingsConfig rejects", async () => {
    // A transient settings-config fetch failure must not cascade
    // into dropping previously known providers. The loader should
    // consult the previous snapshot to decide whether to refresh
    // providers, and the outer resolveDashboardResult should keep
    // previous.providers when the fresh providers fetch is left
    // un-attempted.
    vi.mocked(api.getSettingsConfig).mockRejectedValue(new Error("boom"));
    const previous = {
      ...emptyPrev,
      providers: [{ name: "openai" } as never],
      settingsConfig: {
        backend: "memory",
        providers: [{ name: "openai" } as never],
        policy_rules: [],
        events: [],
      } as never,
    };
    vi.mocked(api.getProviders).mockResolvedValue({
      object: "list",
      data: [{ name: "openai", state: "ready" } as never],
    });
    const snapshot = await resolveDashboardSnapshot({
      activeChatSessionID: "",
      previous,
    });
    expect(api.getProviders).toHaveBeenCalled();
    expect(snapshot.providers).toEqual([{ name: "openai", state: "ready" }]);
  });

  it("preserves previous providers when getSettingsConfig rejects and previous had none configured", async () => {
    // No previous settings-config and no previous providers; the
    // fresh settings-config failed. providers should stay at the
    // previous value (empty) without firing getProviders or
    // overwriting with a synthesized empty list (which would
    // displace a hypothetical concurrent update via
    // resolveDashboardResult on the outer call).
    vi.mocked(api.getSettingsConfig).mockRejectedValue(new Error("boom"));
    const snapshot = await resolveDashboardSnapshot({
      activeChatSessionID: "",
      previous: emptyPrev,
    });
    expect(api.getProviders).not.toHaveBeenCalled();
    expect(snapshot.providers).toEqual([]);
  });

  it("throws when the health probe rejects (the dashboard cannot proceed without it)", async () => {
    vi.mocked(api.getHealth).mockRejectedValue(new Error("backend down"));
    await expect(
      resolveDashboardSnapshot({
        activeChatSessionID: "",
        previous: emptyPrev,
      }),
    ).rejects.toThrow(/runtime console/);
  });

  it("fires onEssentials before the secondary wave starts", async () => {
    // Hold the secondary wave (getChatSessions is on it) until
    // we signal completion. The essentials wave should resolve and
    // fire its callback while this promise is still pending — that
    // is the whole point of the early-commit hook.
    let releaseSecondary = () => {};
    const pending = new Promise<{ object: string; data: never[] }>((resolve) => {
      releaseSecondary = () => resolve({ object: "list", data: [] });
    });
    vi.mocked(api.getChatSessions).mockImplementation(() => pending);

    const onEssentials = vi.fn();
    const snapshotPromise = resolveDashboardSnapshot({
      activeChatSessionID: "",
      previous: emptyPrev,
      onEssentials,
    });

    // Microtasks drain so the essentials wave can settle.
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();

    expect(onEssentials).toHaveBeenCalledTimes(1);
    const essentials = onEssentials.mock.calls[0][0];
    expect(essentials.health.status).toBe("ok");
    expect(essentials.sessionInfo).toEqual({ role: "operator" });
    expect(essentials.settingsConfig?.providers).toEqual([]);

    // Secondary is still pending — finish it so the outer promise
    // resolves and the test cleans up.
    releaseSecondary();
    await snapshotPromise;
  });

  it("surfaces a synthetic down health to onEssentials when getHealth rejects", async () => {
    // The shell should still render with an error banner instead
    // of hanging on the loading state when health fails.
    vi.mocked(api.getHealth).mockRejectedValue(new Error("backend down"));
    const onEssentials = vi.fn();
    await resolveDashboardSnapshot({
      activeChatSessionID: "",
      previous: emptyPrev,
      onEssentials,
    }).catch(() => undefined); // outer promise still rejects; that's fine
    expect(onEssentials).toHaveBeenCalled();
    expect(onEssentials.mock.calls[0][0].health.status).toBe("down");
  });
});

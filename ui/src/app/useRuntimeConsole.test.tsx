import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useRuntimeConsole } from "./useRuntimeConsole";

// Single-user mode: every endpoint is unauthenticated and the gateway
// surfaces a stub `Anonymous` session for all callers. The tests below
// stub /healthz + /hecate/v1/whoami + the dashboard fan-out and exercise the
// hook's user-visible behavior on top of that.

function defaultBackendMock(routes: Record<string, () => Response | Promise<Response>> = {}) {
  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = String(input);
    // Per-test overrides take precedence over the defaults below so a
    // test can stub `/hecate/v1/chat/sessions?limit=20` to return seeded data.
    const handler = routes[url];
    if (handler) return handler();
    if (url === "/healthz") return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
    if (url === "/hecate/v1/whoami") {
      return jsonResponse({
        object: "session",
        data: { authenticated: true, invalid_token: false, role: "admin", source: "anonymous" },
      });
    }
    if (url === "/v1/models") return jsonResponse({ object: "list", data: [] });
    if (url === "/hecate/v1/providers/presets") return jsonResponse({ object: "provider_presets", data: [] });
    if (url === "/hecate/v1/providers/status") return jsonResponse({ object: "provider_status", data: [] });
    if (url === "/hecate/v1/settings") return jsonResponse({ object: "settings", data: { backend: "memory", providers: [], pricebook: [], policy_rules: [], events: [] } });
    if (url.startsWith("/hecate/v1/costs/budget")) return jsonResponse({ object: "budget_status", data: null });
    if (url.startsWith("/hecate/v1/costs/summary")) return jsonResponse({ object: "account_summary", data: null });
    if (url === "/hecate/v1/agent-chat/sessions") return jsonResponse({ object: "agent_chat_sessions", data: [] });
    if (url.startsWith("/hecate/v1/agent-chat/sessions/") && url.endsWith("/approvals?status=pending")) return jsonResponse({ object: "list", data: [] });
    if (url.startsWith("/hecate/v1/chat/sessions")) return jsonResponse({ object: "chat_sessions", data: [], has_more: false });
    if (url.startsWith("/hecate/v1/system/retention/runs")) return jsonResponse({ object: "retention_runs", data: [] });
    if (url.startsWith("/hecate/v1/observability/requests")) return jsonResponse({ object: "requests", data: [] });
    if (url.startsWith("/hecate/v1/system/stats")) return jsonResponse({
      object: "runtime_stats",
      data: {
        checked_at: "2026-04-21T10:00:00Z",
        queue_depth: 0,
        queue_capacity: 0,
        worker_count: 0,
        in_flight_jobs: 0,
        queued_runs: 0,
        running_runs: 0,
        awaiting_approval_runs: 0,
        oldest_queued_age_seconds: 0,
        oldest_running_age_seconds: 0,
      },
    });
    void init;
    return new Response("not found", { status: 404 });
  };
}

describe("useRuntimeConsole", () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    window.localStorage.clear();
    fetchMock.mockImplementation(defaultBackendMock());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("starts chats with an empty composer", async () => {
    const { result } = renderHook(() => useRuntimeConsole());
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    expect(result.current.state.message).toBe("");
  });

  it("defaults to Agent chat when no chat target preference exists", async () => {
    const { result } = renderHook(() => useRuntimeConsole());
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    expect(result.current.state.chatTarget).toBe("agent");
  });

  it("preserves the saved chat target preference", async () => {
    window.localStorage.setItem("hecate.chatTarget", "model");
    const { result } = renderHook(() => useRuntimeConsole());
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    expect(result.current.state.chatTarget).toBe("model");
  });

  it("keeps the active agent chat selection when session refresh fails transiently", async () => {
    window.localStorage.setItem("hecate.agentChatSessionID", "a1");
    fetchMock.mockImplementation(defaultBackendMock({
      "/hecate/v1/agent-chat/sessions": () => jsonResponse({
        object: "agent_chat_sessions",
        data: [{ id: "a1", title: "Still exists", adapter_id: "codex", status: "running", message_count: 2 }],
      }),
      "/hecate/v1/agent-chat/sessions/a1": () => jsonResponse({ error: { type: "gateway_error", message: "temporary failure" } }, 500),
    }));

    const { result } = renderHook(() => useRuntimeConsole());
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    expect(result.current.state.activeAgentChatSessionID).toBe("a1");
    expect(window.localStorage.getItem("hecate.agentChatSessionID")).toBe("a1");
  });

  it("settles into a Local session after the dashboard loads", async () => {
    const { result } = renderHook(() => useRuntimeConsole());
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    expect(result.current.state.session.label).toBe("Local");
  });

  // Regression for the "ModelPicker blinks fast when picking Ollama
  // with the runtime not running" report. Selecting a configured
  // provider whose runtime returned no models must settle on model="",
  // not bounce back to the gateway-wide default.
  it("leaves model empty when selecting a provider with no discovered models", async () => {
    fetchMock.mockImplementation(defaultBackendMock({
      "/v1/models": () => jsonResponse({
        object: "list",
        data: [{ id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud", default: true } }],
      }),
      "/hecate/v1/providers/presets": () => jsonResponse({
        object: "provider_presets",
        data: [
          { id: "openai", name: "OpenAI", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com" },
          { id: "ollama", name: "Ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1" },
        ],
      }),
      "/hecate/v1/providers/status": () => jsonResponse({
        object: "provider_status",
        data: [
          { name: "openai", kind: "cloud", default_model: "gpt-4o-mini", models: ["gpt-4o-mini"], healthy: true },
          { name: "ollama", kind: "local", default_model: "", models: [], healthy: false },
        ],
      }),
      "/hecate/v1/settings": () => jsonResponse({
        object: "configured_state",
        data: {
          providers: [
            { id: "openai", name: "OpenAI", preset_id: "openai", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com", enabled: true, credential_configured: true },
            { id: "ollama", name: "Ollama", preset_id: "ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", enabled: true, credential_configured: false },
          ],
          policy_rules: [], pricebook: [], events: [],
        },
      }),
    }));

    const { result } = renderHook(() => useRuntimeConsole());
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));

    await act(async () => {
      result.current.actions.setProviderFilter("ollama");
    });

    await waitFor(() => {
      expect(result.current.state.providerFilter).toBe("ollama");
      expect(result.current.state.model).toBe("");
    });
    for (let i = 0; i < 5; i++) {
      await new Promise(r => setTimeout(r, 10));
      expect(result.current.state.model).toBe("");
    }
  });

  // ─── applyPricebookImport: notice text per outcome ────────────────────────
  describe("applyPricebookImport notice variants", () => {
    function mockApplyResponse(data: Record<string, unknown>) {
      fetchMock.mockImplementation(defaultBackendMock({
        "/hecate/v1/settings/pricebook/import/apply": () => jsonResponse({ object: "settings_pricebook_import_diff", data }),
      }));
    }

    it("success-only: notice reads 'Imported N rows.'", async () => {
      mockApplyResponse({
        fetched_at: "2026", unchanged: 0,
        applied: [
          { provider: "openai", model: "a", input_micros_usd_per_million_tokens: 1, output_micros_usd_per_million_tokens: 2, cached_input_micros_usd_per_million_tokens: 0, source: "imported" },
          { provider: "openai", model: "b", input_micros_usd_per_million_tokens: 1, output_micros_usd_per_million_tokens: 2, cached_input_micros_usd_per_million_tokens: 0, source: "imported" },
        ],
      });
      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.applyPricebookImport(["openai/a", "openai/b"]);
      });
      await waitFor(() => expect(result.current.state.notice).not.toBeNull());
      expect(result.current.state.notice?.kind).toBe("success");
      expect(result.current.state.notice?.message).toBe("Imported 2 rows.");
    });

    it("mixed: notice reads 'Imported N, M failed.' and is an error notice", async () => {
      mockApplyResponse({
        fetched_at: "2026", unchanged: 0,
        applied: [
          { provider: "openai", model: "good", input_micros_usd_per_million_tokens: 1, output_micros_usd_per_million_tokens: 2, cached_input_micros_usd_per_million_tokens: 0, source: "imported" },
        ],
        failed: [
          { entry: { provider: "openai", model: "bad", input_micros_usd_per_million_tokens: 1, output_micros_usd_per_million_tokens: 2, cached_input_micros_usd_per_million_tokens: 0, source: "imported" }, error: "boom" },
        ],
      });
      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.applyPricebookImport(["openai/good", "openai/bad"]);
      });
      await waitFor(() => expect(result.current.state.notice).not.toBeNull());
      expect(result.current.state.notice?.kind).toBe("error");
      expect(result.current.state.notice?.message).toBe("Imported 1, 1 failed.");
    });

    it("all-failed: notice reads 'Import failed for N rows.'", async () => {
      mockApplyResponse({
        fetched_at: "2026", unchanged: 0,
        applied: [],
        failed: [
          { entry: { provider: "openai", model: "x", input_micros_usd_per_million_tokens: 1, output_micros_usd_per_million_tokens: 2, cached_input_micros_usd_per_million_tokens: 0, source: "imported" }, error: "e1" },
          { entry: { provider: "openai", model: "y", input_micros_usd_per_million_tokens: 1, output_micros_usd_per_million_tokens: 2, cached_input_micros_usd_per_million_tokens: 0, source: "imported" }, error: "e2" },
        ],
      });
      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.applyPricebookImport(["openai/x", "openai/y"]);
      });
      await waitFor(() => expect(result.current.state.notice).not.toBeNull());
      expect(result.current.state.notice?.kind).toBe("error");
      expect(result.current.state.notice?.message).toBe("Import failed for 2 rows.");
    });
  });

  // ─── settings mutations: surviving ones go through runSettingsMutation ──
  describe("settings mutations", () => {
    it("setProviderAPIKey rotate sends PUT, fires loadDashboard, surfaces success notice", async () => {
      let putCalls = 0;
      let putBody = "";
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/settings/providers/anthropic/api-key" && init?.method === "PUT") {
          putCalls++;
          putBody = String(init.body ?? "");
          return jsonResponse({ object: "settings_provider_api_key", data: { id: "anthropic" } });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.setProviderAPIKey("anthropic", "sk-new");
      });
      await waitFor(() => expect(result.current.state.notice?.message).toBe("API key saved."));
      expect(putCalls).toBe(1);
      expect(JSON.parse(putBody)).toEqual({ key: "sk-new" });
      expect(result.current.state.notice?.kind).toBe("success");
      expect(result.current.state.settingsError).toBe("");
    });

    it("setProviderAPIKey clear (empty key) sends PUT and reads 'API key cleared.'", async () => {
      let putBody = "";
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/settings/providers/openai/api-key" && init?.method === "PUT") {
          putBody = String(init.body ?? "");
          return jsonResponse({ object: "settings_provider_api_key", data: { id: "openai", status: "cleared" } });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.setProviderAPIKey("openai", "");
      });
      await waitFor(() => expect(result.current.state.notice?.message).toBe("API key cleared."));
      expect(JSON.parse(putBody)).toEqual({ key: "" });
    });

    it("setProviderAPIKey failure surfaces both settingsError and an error notice", async () => {
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/settings/providers/anthropic/api-key" && init?.method === "PUT") {
          return new Response(
            JSON.stringify({ error: { message: "secret store is read-only" } }),
            { status: 400, headers: { "Content-Type": "application/json" } },
          );
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.setProviderAPIKey("anthropic", "sk-new");
      });
      await waitFor(() => expect(result.current.state.notice?.kind).toBe("error"));
      expect(result.current.state.notice?.message).toBe("Failed to save API key.");
      expect(result.current.state.settingsError).toContain("secret store is read-only");
    });

    it("upsertPolicyRule POSTs the payload + fires success notice", async () => {
      let postBody = "";
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/settings/policy-rules" && init?.method === "POST") {
          postBody = String(init.body ?? "");
          return jsonResponse({});
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.upsertPolicyRule({
          id: "deny-cloud", action: "deny", reason: "local-only", provider_kinds: ["cloud"],
        });
      });
      await waitFor(() => expect(result.current.state.notice?.message).toBe("Policy rule saved."));
      expect(JSON.parse(postBody).id).toBe("deny-cloud");
    });

    it("deletePolicyRule calls the REST delete endpoint", async () => {
      let deleteCalled = false;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/settings/policy-rules/deny-cloud" && init?.method === "DELETE") {
          deleteCalled = true;
          return jsonResponse({});
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.deletePolicyRule("deny-cloud");
      });
      await waitFor(() => expect(result.current.state.notice?.message).toBe("Policy rule deleted."));
      expect(deleteCalled).toBe(true);
    });

    it("deleteProvider optimistically removes the provider before the dashboard refresh completes", async () => {
      let deleted = false;
      let resolveDelete: ((response: Response) => void) | undefined;
      let deleteCalls = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/settings/providers/ollama" && init?.method === "DELETE") {
          deleteCalls++;
          return new Promise<Response>(resolve => {
            resolveDelete = response => {
              deleted = true;
              resolve(response);
            };
          });
        }
        if (url === "/hecate/v1/settings") {
          return jsonResponse({
            object: "settings",
            data: {
              backend: "memory",
              providers: [
                { id: "openai", name: "OpenAI", preset_id: "openai", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com/v1", credential_configured: true },
                ...deleted ? [] : [{ id: "ollama", name: "Ollama", preset_id: "ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", credential_configured: false }],
              ],
              pricebook: [], policy_rules: [], events: [],
            },
          });
        }
        if (url === "/hecate/v1/providers/status") {
          return jsonResponse({
            object: "provider_status",
            data: [
              { name: "openai", kind: "cloud", healthy: true, status: "healthy", models: ["gpt-4o-mini"] },
              ...deleted ? [] : [{ name: "ollama", kind: "local", healthy: true, status: "healthy", models: ["llama3.1:8b"] }],
            ],
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.settingsConfig?.providers.map(p => p.id)).toEqual(["openai", "ollama"]));

      let pendingDelete: Promise<void> | undefined;
      await act(async () => {
        pendingDelete = result.current.actions.deleteProvider("ollama");
      });

      expect(deleteCalls).toBe(1);
      expect(result.current.state.settingsConfig?.providers.map(p => p.id)).toEqual(["openai"]);
      expect(result.current.state.providers.map(p => p.name)).toEqual(["openai"]);

      resolveDelete?.(new Response(null, { status: 204 }));
      await act(async () => {
        await pendingDelete;
      });

      await waitFor(() => expect(result.current.state.settingsConfig?.providers.map(p => p.id)).toEqual(["openai"]));
      expect(result.current.state.notice).toEqual({ kind: "success", message: "Provider removed." });
    });

    it("deleteProvider rolls back the optimistic removal when the request fails", async () => {
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/settings/providers/ollama" && init?.method === "DELETE") {
          return new Response(
            JSON.stringify({ error: { message: "provider is still referenced by a policy rule" } }),
            { status: 409, headers: { "Content-Type": "application/json" } },
          );
        }
        if (url === "/hecate/v1/settings") {
          return jsonResponse({
            object: "settings",
            data: {
              backend: "memory",
              providers: [
                { id: "openai", name: "OpenAI", preset_id: "openai", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com/v1", credential_configured: true },
                { id: "ollama", name: "Ollama", preset_id: "ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", credential_configured: false },
                { id: "anthropic", name: "Anthropic", preset_id: "anthropic", kind: "cloud", protocol: "anthropic", base_url: "https://api.anthropic.com", credential_configured: true },
              ],
              pricebook: [], policy_rules: [], events: [],
            },
          });
        }
        if (url === "/hecate/v1/providers/status") {
          return jsonResponse({
            object: "provider_status",
            data: [
              { name: "openai", kind: "cloud", healthy: true, status: "healthy", models: ["gpt-4o-mini"] },
              { name: "ollama", kind: "local", healthy: true, status: "healthy", models: ["llama3.1:8b"] },
              { name: "anthropic", kind: "cloud", healthy: true, status: "healthy", models: ["claude-sonnet-4-6"] },
            ],
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.settingsConfig?.providers.map(p => p.id)).toEqual(["openai", "ollama", "anthropic"]));
      await act(async () => {
        result.current.actions.setProviderFilter("ollama");
        result.current.actions.setModel("llama3.1:8b");
      });

      await act(async () => {
        await result.current.actions.deleteProvider("ollama");
      });

      expect(result.current.state.settingsConfig?.providers.map(p => p.id)).toEqual(["openai", "ollama", "anthropic"]);
      expect(result.current.state.providers.map(p => p.name)).toEqual(["openai", "ollama", "anthropic"]);
      expect(result.current.state.providerFilter).toBe("ollama");
      expect(result.current.state.model).toBe("llama3.1:8b");
      expect(result.current.state.notice).toEqual({ kind: "error", message: "Failed to remove provider." });
      expect(result.current.state.settingsError).toContain("provider is still referenced");
    });
  });

  // ─── Hecate Chat session actions ───────────────────────────────────────────
  describe("Hecate Chat session actions", () => {
    beforeEach(() => {
      window.localStorage.setItem("hecate.chatTarget", "model");
    });

    function withSessions(sessions: Array<{ id: string; title: string }>, routes: Record<string, () => Response> = {}) {
      return defaultBackendMock({
        "/hecate/v1/agent-chat/sessions": () => {
          const data = sessions.map(s => ({
            ...s,
            runtime_kind: "model",
            status: "completed",
            message_count: 0,
            created_at: "2026-04-20T00:00:00Z",
            updated_at: "2026-04-20T00:00:00Z",
          }));
          return jsonResponse({ object: "agent_chat_sessions", data });
        },
        ...routes,
      });
    }

    it("selectChatSession populates activeAgentChatSession on success", async () => {
      fetchMock.mockImplementation(withSessions([{ id: "sess_42", title: "Existing" }], {
        "/hecate/v1/agent-chat/sessions/sess_42": () => jsonResponse({
          object: "agent_chat_session",
          data: {
            id: "sess_42",
            title: "Existing",
            runtime_kind: "model",
            status: "completed",
            messages: [
              { id: "msg_u", runtime_kind: "model", role: "user", content: "hi", created_at: "2026-04-20T00:00:00Z" },
              { id: "msg_a", runtime_kind: "model", role: "assistant", content: "hello", status: "completed", created_at: "2026-04-20T00:00:00Z" },
            ],
            provider: "openai",
            model: "gpt-4o-mini",
            created_at: "2026-04-20T00:00:00Z", updated_at: "2026-04-20T00:00:00Z",
          },
        }),
      }));

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.selectChatSession("sess_42");
      });
      await waitFor(() => expect(result.current.state.activeAgentChatSession?.id).toBe("sess_42"));
      expect(result.current.state.activeAgentChatSessionID).toBe("sess_42");
      expect(result.current.state.activeAgentChatSession?.messages).toHaveLength(2);
      expect(result.current.state.activeAgentChatSession?.model).toBe("gpt-4o-mini");
      expect(result.current.state.providerFilter).toBe("openai");
      expect(result.current.state.chatError).toBe("");
    });

    it("queues a prompt while the active agent run is busy and sends it after completion", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.agentChatSessionID", "a1");
      window.localStorage.setItem("hecate.agentWorkspace", "/workspace");
      let sessionStatus = "running";
      let messagePostCount = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/agent-chat/sessions") {
          return jsonResponse({
            object: "agent_chat_sessions",
            data: [{ id: "a1", title: "Agent", runtime_kind: "agent", status: sessionStatus, workspace: "/workspace", message_count: 0 }],
          });
        }
        if (url === "/hecate/v1/agent-chat/sessions/a1") {
          return jsonResponse({
            object: "agent_chat_session",
            data: { id: "a1", title: "Agent", runtime_kind: "agent", status: sessionStatus, workspace: "/workspace", messages: [], created_at: "2026-04-20T00:00:00Z", updated_at: new Date().toISOString() },
          });
        }
        if (url === "/hecate/v1/agent-chat/sessions/a1/stream") {
          return emptyStreamResponse();
        }
        if (url === "/hecate/v1/agent-chat/sessions/a1/messages") {
          messagePostCount += 1;
          void init;
          return jsonResponse({
            object: "agent_chat_session",
            data: {
              id: "a1",
              title: "Agent",
              runtime_kind: "agent",
              status: "completed",
              workspace: "/workspace",
              messages: [
                { id: "u1", runtime_kind: "agent", role: "user", content: "after this", created_at: "2026-04-20T00:00:01Z" },
              ],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: new Date().toISOString(),
            },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.activeAgentChatSession?.status).toBe("running"));

      act(() => {
        result.current.actions.setMessage("after this");
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(messagePostCount).toBe(0);
      expect(result.current.state.message).toBe("");
      expect(result.current.state.queuedChatMessages).toHaveLength(1);
      expect(result.current.state.queuedChatMessages[0].content).toBe("after this");

      sessionStatus = "completed";
      await act(async () => {
        await result.current.actions.selectChatSession("a1");
      });

      await waitFor(() => expect(messagePostCount).toBe(1));
      await waitFor(() => expect(result.current.state.queuedChatMessages).toHaveLength(0));
    });

    it("keeps the tools toggle scoped to the selected Hecate chat", async () => {
      const hecateSession = (id: string) => ({
        object: "agent_chat_session",
        data: {
          id,
          title: id,
          runtime_kind: "agent",
          status: "completed",
          workspace: "/workspace",
          provider: "openai",
          model: "gpt-4o-mini",
          messages: [],
          created_at: "2026-04-20T00:00:00Z",
          updated_at: "2026-04-20T00:00:00Z",
        },
      });
      fetchMock.mockImplementation(withSessions([
        { id: "chat_a", title: "A" },
        { id: "chat_b", title: "B" },
      ], {
        "/hecate/v1/agent-chat/sessions/chat_a": () => jsonResponse(hecateSession("chat_a")),
        "/hecate/v1/agent-chat/sessions/chat_b": () => jsonResponse(hecateSession("chat_b")),
      }));

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.selectChatSession("chat_a");
      });
      await waitFor(() => expect(result.current.state.activeAgentChatSession?.id).toBe("chat_a"));
      expect(result.current.state.chatTarget).toBe("agent");

      act(() => {
        result.current.actions.setChatTarget("model");
      });
      expect(result.current.state.chatTarget).toBe("model");

      await act(async () => {
        await result.current.actions.selectChatSession("chat_b");
      });
      await waitFor(() => expect(result.current.state.activeAgentChatSession?.id).toBe("chat_b"));
      expect(result.current.state.chatTarget).toBe("agent");

      await act(async () => {
        await result.current.actions.selectChatSession("chat_a");
      });
      await waitFor(() => expect(result.current.state.activeAgentChatSession?.id).toBe("chat_a"));
      expect(result.current.state.chatTarget).toBe("model");
    });

    it("restores provider and model from the latest Hecate chat segment on selection", async () => {
      fetchMock.mockImplementation(withSessions([{ id: "mixed_chat", title: "Mixed" }], {
        "/v1/models": () => jsonResponse({
          object: "list",
          data: [
            { id: "smollm2:135m", owned_by: "ollama", metadata: { provider: "ollama", provider_kind: "local" } },
            { id: "qwen2.5-coder", owned_by: "ollama", metadata: { provider: "ollama", provider_kind: "local" } },
          ],
        }),
        "/hecate/v1/agent-chat/sessions/mixed_chat": () => jsonResponse({
          object: "agent_chat_session",
          data: {
            id: "mixed_chat",
            title: "Mixed",
            runtime_kind: "model",
            status: "completed",
            workspace: "/workspace",
            provider: "ollama",
            model: "smollm2:135m",
            segments: [
              { id: "model:first", runtime_kind: "model", provider: "ollama", model: "smollm2:135m", status: "completed", message_count: 2 },
              { id: "task:task_tools", runtime_kind: "agent", provider: "ollama", model: "qwen2.5-coder", task_id: "task_tools", status: "completed", message_count: 2 },
            ],
            messages: [],
            created_at: "2026-04-20T00:00:00Z",
            updated_at: "2026-04-20T00:00:00Z",
          },
        }),
      }));

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.selectChatSession("mixed_chat");
      });

      await waitFor(() => expect(result.current.state.activeAgentChatSession?.id).toBe("mixed_chat"));
      expect(result.current.state.providerFilter).toBe("ollama");
      expect(result.current.state.model).toBe("qwen2.5-coder");
    });

    it("selectChatSession 404 clears the active agent-chat selection and surfaces an error", async () => {
      fetchMock.mockImplementation(withSessions([{ id: "sess_gone", title: "Gone" }], {
        "/hecate/v1/agent-chat/sessions/sess_gone": () => new Response(
          JSON.stringify({ error: { message: "session not found" } }),
          { status: 404, headers: { "Content-Type": "application/json" } },
        ),
      }));

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.selectChatSession("sess_gone");
      });
      expect(result.current.state.activeAgentChatSessionID).toBe("");
      await waitFor(() => expect(result.current.state.chatError).toContain("session not found"));
      expect(result.current.state.notice?.kind).toBe("error");
    });

    it("deleteChatSession removes the agent-chat session from the sidebar and notices", async () => {
      let deleteCalls = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/agent-chat/sessions/sess_b" && init?.method === "DELETE") {
          deleteCalls++;
          return new Response(null, { status: 204 });
        }
        return withSessions(
          [{ id: "sess_a", title: "Keep" }, { id: "sess_b", title: "Delete me" }],
        )(input, init);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.agentChatSessions).toHaveLength(2));

      await act(async () => {
        await result.current.actions.deleteChatSession("sess_b");
      });

      expect(deleteCalls).toBe(1);
      await waitFor(() => expect(result.current.state.agentChatSessions.map(s => s.id)).toEqual(["sess_a"]));
      expect(result.current.state.notice?.kind).toBe("success");
      expect(result.current.state.notice?.message).toBe("Agent chat deleted.");
    });

    it("renameChatSession patches the title in the sidebar", async () => {
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions/sess_a" && init?.method === "PATCH") {
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "sess_a", title: "Renamed", turns: [],
              created_at: "2026-04-20T00:00:00Z", updated_at: "2026-04-20T00:01:00Z",
            },
          });
        }
        return defaultBackendMock({
          "/hecate/v1/chat/sessions?limit=20": () => jsonResponse({
            object: "chat_sessions",
            data: [{ id: "sess_a", title: "Old title", turns: [], created_at: "2026-04-20T00:00:00Z", updated_at: "2026-04-20T00:00:00Z" }],
            has_more: false,
          }),
        })(input, init);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.chatSessions).toHaveLength(1));

      await act(async () => {
        await result.current.actions.renameChatSession("sess_a", "Renamed");
      });

      await waitFor(() => expect(result.current.state.chatSessions[0].title).toBe("Renamed"));
    });
  });

  // ─── Agent-chat approvals state ───────────────────────────────────────────
  //
  // On session select we fire a catch-up refetch against
  // /hecate/v1/agent-chat/sessions/{id}/approvals?status=pending. The
  // returned rows are projected to banner-essentials and stored in
  // `pendingApprovalsBySessionID`. SSE events later upsert/remove on
  // top of the same map. The Map instance is always replaced — never
  // mutated in place.
  describe("agent-chat approvals state", () => {
    function approvalRow(overrides: Record<string, unknown> = {}) {
      return {
        id: "ap-1",
        session_id: "a1",
        adapter_id: "codex",
        tool_kind: "fs",
        tool_name: "write_file",
        status: "pending",
        acp_options: [],
        scope_choices: ["once"],
        created_at: "2026-04-21T10:00:00Z",
        expires_at: "2026-04-21T10:05:00Z",
        ...overrides,
      };
    }

    it("starts with an empty pending map and no grants", async () => {
      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      expect(result.current.state.pendingApprovalsBySessionID.size).toBe(0);
      expect(result.current.state.agentChatGrants).toEqual([]);
    });

    it("populates the pending map from the catch-up refetch when a session is selected", async () => {
      window.localStorage.setItem("hecate.agentChatSessionID", "a1");
      fetchMock.mockImplementation(defaultBackendMock({
        "/hecate/v1/agent-chat/sessions": () => jsonResponse({
          object: "agent_chat_sessions",
          data: [{ id: "a1", title: "S1", adapter_id: "codex", status: "running", message_count: 0 }],
        }),
        "/hecate/v1/agent-chat/sessions/a1": () => jsonResponse({
          object: "agent_chat_session",
          data: { id: "a1", title: "S1", adapter_id: "codex", workspace: "/tmp", status: "running" },
        }),
        "/hecate/v1/agent-chat/sessions/a1/approvals?status=pending": () => jsonResponse({
          object: "list",
          data: [approvalRow()],
        }),
      }));

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      // Effect-driven refetch may need a tick after activeAgentChatSessionID
      // hydrates from localStorage.
      await waitFor(() => {
        expect(result.current.state.pendingApprovalsBySessionID.get("a1")).toBeDefined();
      });
      const rows = result.current.state.pendingApprovalsBySessionID.get("a1")!;
      expect(rows).toHaveLength(1);
      expect(rows[0]).toMatchObject({
        approval_id: "ap-1",
        session_id: "a1",
        adapter_id: "codex",
        tool_kind: "fs",
      });
    });

    it("treats an empty refetch as authoritative — refetch wins over any prior optimistic state", async () => {
      // Switch a→b→a. On the third select, the refetch for `a` returns
      // an empty list (e.g. another operator resolved it from a
      // different tab). The pending entry must clear, even though we
      // never see an `approval.resolved` event for the missed transition.
      let approvalsForA: unknown[] = [approvalRow({ id: "ap-1", session_id: "a1" })];
      fetchMock.mockImplementation(defaultBackendMock({
        "/hecate/v1/agent-chat/sessions": () => jsonResponse({ object: "agent_chat_sessions", data: [] }),
        "/hecate/v1/agent-chat/sessions/a1": () => jsonResponse({
          object: "agent_chat_session",
          data: { id: "a1", title: "A", adapter_id: "codex", workspace: "/tmp", status: "running" },
        }),
        "/hecate/v1/agent-chat/sessions/b1": () => jsonResponse({
          object: "agent_chat_session",
          data: { id: "b1", title: "B", adapter_id: "codex", workspace: "/tmp", status: "running" },
        }),
        "/hecate/v1/agent-chat/sessions/a1/approvals?status=pending": () => jsonResponse({
          object: "list",
          data: approvalsForA,
        }),
        "/hecate/v1/agent-chat/sessions/b1/approvals?status=pending": () => jsonResponse({
          object: "list",
          data: [],
        }),
      }));

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      // Default chatTarget is "agent", so selectChatSession forwards
      // to the agent variant.
      await act(async () => {
        await result.current.actions.selectChatSession("a1");
      });
      await waitFor(() =>
        expect(result.current.state.pendingApprovalsBySessionID.get("a1")).toHaveLength(1),
      );

      // Switch away — pending state for a1 stays in the map (it
      // wasn't resolved, just unobserved).
      await act(async () => {
        await result.current.actions.selectChatSession("b1");
      });

      // The approval was resolved by another actor while we were
      // away. Refetch on switch-back should reflect the empty list.
      approvalsForA = [];
      await act(async () => {
        await result.current.actions.selectChatSession("a1");
      });

      await waitFor(() =>
        expect(result.current.state.pendingApprovalsBySessionID.has("a1")).toBe(false),
      );
    });

    it("ignores a stale catch-up refetch when a local approval update lands first", async () => {
      let delayARefetch = false;
      let releaseARefetch: (() => void) | undefined;
      fetchMock.mockImplementation(defaultBackendMock({
        "/hecate/v1/agent-chat/sessions": () => jsonResponse({ object: "agent_chat_sessions", data: [] }),
        "/hecate/v1/agent-chat/sessions/a1": () => jsonResponse({
          object: "agent_chat_session",
          data: { id: "a1", title: "A", adapter_id: "codex", workspace: "/tmp", status: "running" },
        }),
        "/hecate/v1/agent-chat/sessions/b1": () => jsonResponse({
          object: "agent_chat_session",
          data: { id: "b1", title: "B", adapter_id: "codex", workspace: "/tmp", status: "running" },
        }),
        "/hecate/v1/agent-chat/sessions/a1/approvals?status=pending": () => {
          if (!delayARefetch) {
            return jsonResponse({ object: "list", data: [approvalRow()] });
          }
          return new Promise<Response>((resolve) => {
            releaseARefetch = () => resolve(jsonResponse({ object: "list", data: [approvalRow()] }));
          });
        },
        "/hecate/v1/agent-chat/sessions/b1/approvals?status=pending": () => jsonResponse({
          object: "list",
          data: [],
        }),
        "/hecate/v1/agent-chat/sessions/a1/approvals/ap-1/resolve": () => jsonResponse({
          object: "agent_chat_approval",
          data: approvalRow({ status: "approved", decision: "approve", scope: "once", path: "operator" }),
        }),
      }));

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.selectChatSession("a1");
      });
      await waitFor(() =>
        expect(result.current.state.pendingApprovalsBySessionID.get("a1")).toHaveLength(1),
      );

      await act(async () => {
        await result.current.actions.selectChatSession("b1");
      });

      delayARefetch = true;
      await act(async () => {
        await result.current.actions.selectChatSession("a1");
      });
      await waitFor(() => expect(releaseARefetch).toBeDefined());

      await act(async () => {
        const ok = await result.current.actions.resolveAgentChatApproval("a1", "ap-1", {
          decision: "approve",
          scope: "once",
        });
        expect(ok).toBe(true);
      });
      expect(result.current.state.pendingApprovalsBySessionID.has("a1")).toBe(false);

      await act(async () => {
        releaseARefetch?.();
        await Promise.resolve();
      });

      expect(result.current.state.pendingApprovalsBySessionID.has("a1")).toBe(false);
    });

    it("removes a pending approval when the operator resolves it (optimistic update)", async () => {
      window.localStorage.setItem("hecate.agentChatSessionID", "a1");
      fetchMock.mockImplementation(defaultBackendMock({
        "/hecate/v1/agent-chat/sessions": () => jsonResponse({ object: "agent_chat_sessions", data: [] }),
        "/hecate/v1/agent-chat/sessions/a1": () => jsonResponse({
          object: "agent_chat_session",
          data: { id: "a1", title: "S1", adapter_id: "codex", workspace: "/tmp", status: "running" },
        }),
        "/hecate/v1/agent-chat/sessions/a1/approvals?status=pending": () => jsonResponse({
          object: "list",
          data: [approvalRow()],
        }),
        "/hecate/v1/agent-chat/sessions/a1/approvals/ap-1/resolve": () => jsonResponse({
          object: "agent_chat_approval",
          data: approvalRow({ status: "resolved", decision: "approve", scope: "once", path: "operator" }),
        }),
      }));

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() =>
        expect(result.current.state.pendingApprovalsBySessionID.get("a1")).toHaveLength(1),
      );

      await act(async () => {
        const ok = await result.current.actions.resolveAgentChatApproval("a1", "ap-1", {
          decision: "approve",
          scope: "once",
        });
        expect(ok).toBe(true);
      });

      // The optimistic remove fires regardless of whether the SSE
      // `approval.resolved` event was observed — guarantees the
      // banner clears even if the modal never fetched the full row.
      expect(result.current.state.pendingApprovalsBySessionID.has("a1")).toBe(false);
    });

    it("loads grants and removes them on revoke", async () => {
      fetchMock.mockImplementation(defaultBackendMock({
        "/hecate/v1/agent-chat/grants": () => jsonResponse({
          object: "list",
          data: [
            { id: "g1", scope: "session", adapter_id: "codex", tool_kind: "fs", decision: "approve", granted_by: "operator", granted_at: "2026-04-21T10:00:00Z" },
            { id: "g2", scope: "workspace_tool", adapter_id: "codex", tool_kind: "exec", decision: "approve", granted_by: "operator", granted_at: "2026-04-21T10:01:00Z" },
          ],
        }),
        "/hecate/v1/agent-chat/grants/g1": () => new Response(null, { status: 204 }),
      }));

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.listAgentChatGrants();
      });
      expect(result.current.state.agentChatGrants.map((g) => g.id)).toEqual(["g1", "g2"]);

      await act(async () => {
        const ok = await result.current.actions.deleteAgentChatGrant("g1");
        expect(ok).toBe(true);
      });
      expect(result.current.state.agentChatGrants.map((g) => g.id)).toEqual(["g2"]);
    });
  });
});

describe("humanizeChatError", () => {
  it("rewrites the api-key-required message into actionable copy", async () => {
    const { humanizeChatError } = await import("./useRuntimeConsole");
    expect(humanizeChatError("api key is required for cloud provider openai when stub mode is disabled"))
      .toBe("openai has no API key. Open the Providers tab and add one.");
  });

  it("preserves the provider name verbatim including hyphens / casing", async () => {
    const { humanizeChatError } = await import("./useRuntimeConsole");
    expect(humanizeChatError("api key is required for cloud provider together_ai when stub mode is disabled"))
      .toBe("together_ai has no API key. Open the Providers tab and add one.");
  });

  it("passes unrelated errors through unchanged", async () => {
    const { humanizeChatError } = await import("./useRuntimeConsole");
    expect(humanizeChatError("rate limit exceeded")).toBe("rate limit exceeded");
    expect(humanizeChatError("")).toBe("");
  });

  it("humanizes busy, unroutable model, and upstream provider errors", async () => {
    const { humanizeChatError } = await import("./useRuntimeConsole");
    expect(humanizeChatError("Hecate Agent is already running for this chat session."))
      .toBe("Hecate Chat is still working on this task. Open the task, resolve approval, or stop it before sending another message.");
    expect(humanizeChatError("workspace is required"))
      .toBe("Choose a workspace before using Hecate Chat tools or External Agent.");
    expect(humanizeChatError("tool calling support is unknown"))
      .toBe("This model is not marked as tool-capable. Turn tools off, test it, or enable tools in Settings.");
    expect(humanizeChatError('route request: no provider supports explicit model "gpt-5.4-mini"'))
      .toBe("No configured provider can route to gpt-5.4-mini. Choose another model or add/configure a provider.");
    expect(humanizeChatError("no routable model for selected provider"))
      .toBe("No routable model is available. Choose another model, add a provider, or check provider health.");
    expect(humanizeChatError("Authentication required. Please run 'agent login' first."))
      .toBe("The selected runtime is not signed in. Run its login command or use Settings to test readiness.");
    expect(humanizeChatError("Internal error: Credit balance is too low"))
      .toBe("The selected runtime reported a billing or credit problem. Check its account, subscription, or API key balance.");
    expect(humanizeChatError("ECONNREFUSED 127.0.0.1:11434"))
      .toBe("The selected provider is not reachable. Start the local provider app or check its endpoint URL.");
    expect(humanizeChatError("upstream returned 401"))
      .toBe("The selected provider rejected the request with HTTP 401. Check credentials and account access.");
    expect(humanizeChatError("upstream returned 503"))
      .toBe("The selected provider returned HTTP 503. Check that the provider is running and reachable.");
    expect(humanizeChatError("upstream timeout"))
      .toBe("The selected provider did not respond before the timeout. Check that it is running, reachable, and not overloaded.");
  });
});

function jsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function emptyStreamResponse(): Response {
  return new Response(new ReadableStream({
    start(controller) {
      controller.close();
    },
  }), {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
  });
}

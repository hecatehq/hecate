import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useRuntimeConsole } from "./useRuntimeConsole";

describe("useRuntimeConsole", () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    window.localStorage.clear();
    // Seed an admin token so loadDashboard actually fires. The hook
    // skips the dashboard load when authToken is empty (TokenGate is
    // rendering anyway), but every test in this file is exercising the
    // post-auth dashboard path.
    window.localStorage.setItem("hecate.authToken", "test-bearer");
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === "/healthz") {
        return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
      }
      if (url === "/v1/whoami") {
        return jsonResponse({
          object: "session",
          data: {
            authenticated: false,
            invalid_token: false,
            role: "anonymous",
            source: "no_token",
          },
        });
      }
      if (url === "/v1/models") {
        return jsonResponse({
          object: "list",
          data: [{ id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } }],
        });
      }
      if (url === "/v1/provider-presets") {
        return jsonResponse({
          object: "provider_presets",
          data: [{ id: "openai", name: "OpenAI", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com" }],
        });
      }
      if (url.startsWith("/admin/retention/runs")) {
        return unauthorizedResponse();
      }
      if (url.startsWith("/admin/accounts/summary")) {
        return unauthorizedResponse();
      }
      if (url.startsWith("/v1/chat/sessions")) {
        return unauthorizedResponse();
      }
      return unauthorizedResponse();
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("probes /v1/bootstrap-token + whoami when no token is present, then stops", async () => {
    // No bearer in localStorage: we still want to hit two endpoints.
    //   1. /v1/bootstrap-token — loopback handshake (returns 403 here
    //      because the test environment isn't loopback-fenced; the UI
    //      treats any non-200 as "no token, fall through").
    //   2. /healthz + /v1/whoami — establishes identity. With whoami
    //      returning anonymous and not auth_disabled, the dashboard
    //      stops there (no admin-endpoint 401 storm).
    window.localStorage.removeItem("hecate.authToken");
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === "/v1/bootstrap-token") return new Response("forbidden", { status: 403 });
      if (url === "/healthz") return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
      if (url === "/v1/whoami") {
        return jsonResponse({
          object: "session",
          data: { authenticated: false, invalid_token: false, role: "anonymous", source: "anonymous" },
        });
      }
      return unauthorizedResponse();
    });

    const { result } = renderHook(() => useRuntimeConsole());

    await waitFor(() => expect(result.current.state.loading).toBe(false));

    const calledURLs = fetchMock.mock.calls.map((c) => String(c[0]));
    expect(calledURLs).toContain("/v1/bootstrap-token");
    expect(calledURLs).toContain("/healthz");
    expect(calledURLs).toContain("/v1/whoami");
    // Admin / dashboard endpoints stay quiet — anonymous + not
    // auth_disabled means the gate above skips them.
    expect(calledURLs.some((u) => u.startsWith("/admin/"))).toBe(false);
    expect(calledURLs).not.toContain("/v1/models");
    expect(result.current.state.bootstrapAttempted).toBe(true);
  });

  it("adopts a bootstrap token when the loopback handshake succeeds", async () => {
    window.localStorage.removeItem("hecate.authToken");
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === "/v1/bootstrap-token") return jsonResponse({ object: "bootstrap_token", data: { token: "loopback-secret" } });
      if (url === "/healthz") return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
      if (url === "/v1/whoami") {
        return jsonResponse({
          object: "session",
          data: { authenticated: true, invalid_token: false, role: "admin", source: "admin_token" },
        });
      }
      return unauthorizedResponse();
    });

    const { result } = renderHook(() => useRuntimeConsole());

    await waitFor(() => expect(result.current.state.authToken).toBe("loopback-secret"));
    expect(result.current.state.bootstrapAttempted).toBe(true);
  });

  it("loads dashboard data and tolerates unauthorized admin endpoints", async () => {
    // Use a tenant-authenticated session: the dashboard fires the
    // tenant-level fetches (models, providers, presets, sessions) but
    // gates admin-only fetches (budget, retention, accountSummary,
    // adminConfig, requestLedger) behind role=admin. With this gating
    // an anonymous bearer no longer fires those admin endpoints at
    // all (the previous "401 storm"), so this test simulates a
    // tenant whose admin endpoints are unauthorized — still tolerated
    // because they get skipped before the request goes out.
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === "/healthz") return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
      if (url === "/v1/whoami") {
        return jsonResponse({
          object: "session",
          data: { authenticated: true, invalid_token: false, role: "tenant", tenant: "acme", source: "bearer" },
        });
      }
      if (url === "/v1/models") {
        return jsonResponse({
          object: "list",
          data: [{ id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } }],
        });
      }
      if (url === "/v1/provider-presets") {
        return jsonResponse({
          object: "provider_presets",
          data: [{ id: "openai", name: "OpenAI", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com" }],
        });
      }
      // Tenant-level: providers + sessions return ok-but-empty.
      if (url.startsWith("/v1/providers")) return jsonResponse({ object: "list", data: [] });
      if (url.startsWith("/v1/chat/sessions")) return jsonResponse({ object: "chat_sessions", data: [] });
      // Admin-only paths: skipped before they fire, but mock 401 in
      // case they ever do — the resolvers fall back to defaults.
      return unauthorizedResponse();
    });

    const { result } = renderHook(() => useRuntimeConsole());

    await waitFor(() => expect(result.current.state.loading).toBe(false));

    expect(result.current.state.health?.status).toBe("ok");
    expect(result.current.state.models).toHaveLength(1);
    expect(result.current.state.providerPresets).toHaveLength(1);
    expect(result.current.state.providers).toEqual([]);
    expect(result.current.state.budget).toBeNull();
    expect(result.current.state.adminConfig).toBeNull();
  });

  it("persists auth token changes to local storage", async () => {
    const { result } = renderHook(() => useRuntimeConsole());

    await waitFor(() => expect(result.current.state.loading).toBe(false));

    act(() => {
      result.current.actions.setAuthToken("tenant-secret");
    });

    await waitFor(() => {
      expect(window.localStorage.getItem("hecate.authToken")).toBe("tenant-secret");
    });
  });

  it("starts chats with an empty composer", async () => {
    const { result } = renderHook(() => useRuntimeConsole());

    await waitFor(() => expect(result.current.state.loading).toBe(false));

    expect(result.current.state.message).toBe("");
  });

  it("syncs the tenant field from the authenticated tenant session", async () => {
    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input);
      const headers = new Headers(init?.headers);
      const authHeader = headers.get("Authorization");
      if (url === "/healthz") {
        return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
      }
      if (url === "/v1/whoami") {
        if (authHeader === "Bearer tenant-secret") {
          return jsonResponse({
            object: "session",
            data: {
              authenticated: true,
              invalid_token: false,
              role: "tenant",
              tenant: "team-a",
              source: "control_plane_api_key",
              key_id: "team-a-dev",
            },
          });
        }
        return jsonResponse({
          object: "session",
          data: {
            authenticated: false,
            invalid_token: false,
            role: "anonymous",
            source: "no_token",
          },
        });
      }
      if (url === "/v1/models") {
        return jsonResponse({
          object: "list",
          data: [{ id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } }],
        });
      }
      if (url === "/v1/provider-presets") {
        return jsonResponse({
          object: "provider_presets",
          data: [{ id: "openai", name: "OpenAI", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com" }],
        });
      }
      if (url.startsWith("/admin/retention/runs")) {
        return unauthorizedResponse();
      }
      if (url.startsWith("/admin/accounts/summary")) {
        return unauthorizedResponse();
      }
      if (url.startsWith("/v1/chat/sessions")) {
        return unauthorizedResponse();
      }
      return unauthorizedResponse();
    });

    const { result } = renderHook(() => useRuntimeConsole());

    await waitFor(() => expect(result.current.state.loading).toBe(false));

    act(() => {
      result.current.actions.setTenant("manual-tenant");
      result.current.actions.setAuthToken("tenant-secret");
    });

    await waitFor(() => {
      expect(result.current.state.session.kind).toBe("tenant");
      expect(result.current.state.session.tenant).toBe("team-a");
      expect(result.current.state.tenant).toBe("team-a");
    });
  });

  it("loads trace data after a successful chat request", async () => {
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === "/healthz") {
        return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
      }
      if (url === "/v1/whoami") {
        return jsonResponse({
          object: "session",
          data: {
            authenticated: false,
            invalid_token: false,
            role: "anonymous",
            source: "no_token",
          },
        });
      }
      if (url === "/v1/models") {
        return jsonResponse({
          object: "list",
          data: [{ id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } }],
        });
      }
      if (url === "/v1/provider-presets") {
        return jsonResponse({
          object: "provider_presets",
          data: [{ id: "openai", name: "OpenAI", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com" }],
        });
      }
      if (url.startsWith("/admin/retention/runs")) {
        return unauthorizedResponse();
      }
      if (url.startsWith("/admin/accounts/summary")) {
        return unauthorizedResponse();
      }
      if (url === "/v1/chat/sessions") {
        return jsonResponse({
          object: "chat_session",
          data: {
            id: "chat_123",
            title: "Say hello in one short sentence.",
            turns: [],
            created_at: "2026-04-21T00:00:00Z",
            updated_at: "2026-04-21T00:00:00Z",
          },
        });
      }
      if (url === "/v1/chat/sessions?limit=20") {
        return jsonResponse({
          object: "chat_sessions",
          data: [
            {
              id: "chat_123",
              title: "Say hello in one short sentence.",
              message_count: 2,
              provider_call_count: 1,
              last_model: "gpt-4o-mini",
              last_provider: "openai",
              last_cost_usd: "0.000123",
              updated_at: "2026-04-21T00:00:01Z",
            },
          ],
        });
      }
      if (url === "/v1/chat/sessions/chat_123") {
        return jsonResponse({
          object: "chat_session",
          data: {
            id: "chat_123",
            title: "Say hello in one short sentence.",
            messages: [
              { id: "msg_u", sequence: 0, role: "user", content: "Say hello in one short sentence.", created_at: "2026-04-21T00:00:01Z" },
              { id: "msg_a", sequence: 1, produced_by_call_id: "call_1", role: "assistant", content: "Hello!", created_at: "2026-04-21T00:00:01Z" },
            ],
            provider_calls: [
              {
                id: "call_1",
                request_id: "req-123",
                provider: "openai",
                provider_kind: "cloud",
                model: "gpt-4o-mini",
                cost_micros_usd: 123,
                cost_usd: "0.000123",
                prompt_tokens: 10,
                completion_tokens: 2,
                total_tokens: 12,
                created_at: "2026-04-21T00:00:01Z",
              },
            ],
            created_at: "2026-04-21T00:00:00Z",
            updated_at: "2026-04-21T00:00:01Z",
          },
        });
      }
      if (url === "/v1/chat/completions") {
        return new Response(
          JSON.stringify({
            id: "chatcmpl-123",
            model: "gpt-4o-mini",
            choices: [{ index: 0, finish_reason: "stop", message: { role: "assistant", content: "Hello!" } }],
          }),
          {
            status: 200,
            headers: {
              "Content-Type": "application/json",
              "X-Request-Id": "req-123",
              "X-Trace-Id": "trace-123",
              "X-Span-Id": "span-123",
              "X-Runtime-Provider": "openai",
              "X-Runtime-Provider-Kind": "cloud",
              "X-Runtime-Route-Reason": "requested_model",
              "X-Runtime-Requested-Model": "gpt-4o-mini",
              "X-Runtime-Model": "gpt-4o-mini",
              "X-Runtime-Cache": "false",
              "X-Runtime-Cache-Type": "false",
              "X-Runtime-Attempts": "1",
              "X-Runtime-Retries": "0",
              "X-Runtime-Fallback-From": "",
              "X-Runtime-Cost-USD": "0.000123",
            },
          },
        );
      }
      if (url === "/v1/traces?request_id=req-123") {
        return jsonResponse({
          object: "trace",
          data: {
            request_id: "req-123",
            trace_id: "req-123",
            started_at: "2026-04-21T00:00:00Z",
            route: {
              final_provider: "openai",
              final_provider_kind: "cloud",
              final_model: "gpt-4o-mini",
              final_reason: "provider_default_model",
              candidates: [
                {
                  provider: "openai",
                  provider_kind: "cloud",
                  model: "gpt-4o-mini",
                  outcome: "selected",
                },
              ],
            },
            spans: [
              {
                trace_id: "req-123",
                span_id: "span-1",
                name: "gateway.request",
                kind: "server",
                events: [{ name: "request.received", timestamp: "2026-04-21T00:00:00Z", attributes: { model: "gpt-4o-mini" } }],
              },
            ],
          },
        });
      }
      return unauthorizedResponse();
    });

    const { result } = renderHook(() => useRuntimeConsole());

    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.submitChat({ preventDefault() {} } as never);
    });

    await waitFor(() => {
      expect(result.current.state.runtimeHeaders?.requestId).toBe("req-123");
      expect(result.current.state.activeChatSession?.messages?.length).toBeGreaterThan(0);
      expect(result.current.state.activeChatSession?.provider_calls?.length).toBeGreaterThan(0);
    });
  });

  it("surfaces a chat error inline only and humanizes the api-key-required message", async () => {
    // The chat surface is its own page; a toast that mirrors the inline
    // banner just duplicates the message in two places. Pin the
    // single-channel behavior. Also pin the humanizer that translates
    // "api key is required for cloud provider X when stub mode is
    // disabled" into something an operator can act on.
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === "/healthz") return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
      if (url === "/v1/whoami") {
        return jsonResponse({
          object: "session",
          data: { authenticated: false, invalid_token: false, role: "anonymous", source: "no_token" },
        });
      }
      if (url === "/v1/models") return jsonResponse({ object: "list", data: [{ id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } }] });
      if (url === "/v1/provider-presets") return jsonResponse({ object: "provider_presets", data: [{ id: "openai", name: "OpenAI", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com" }] });
      if (url === "/v1/chat/sessions") {
        return jsonResponse({ object: "chat_session", data: { id: "chat_err", title: "x", turns: [], created_at: "2026-04-21T00:00:00Z", updated_at: "2026-04-21T00:00:00Z" } });
      }
      if (url === "/v1/chat/completions") {
        // Backend now strips "client error: " before serializing —
        // simulate the cleaned shape we expect on the wire.
        return new Response(
          JSON.stringify({ error: { message: "api key is required for cloud provider anthropic when stub mode is disabled" } }),
          { status: 400, headers: { "Content-Type": "application/json" } },
        );
      }
      return unauthorizedResponse();
    });

    const { result } = renderHook(() => useRuntimeConsole());
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.submitChat({ preventDefault() {} } as never);
    });

    // Inline error is the sole channel for chat failures — the chat
    // surface is its own page, so a corner toast mirroring the same
    // string is just visual duplication. Raw backend errors get
    // humanized: "api key is required for cloud provider X" → a
    // sentence telling the operator where to fix it.
    await waitFor(() => expect(result.current.state.chatError).toMatch(/has no API key/i));
    expect(result.current.state.chatError).toContain("anthropic");
    expect(result.current.state.chatError).not.toMatch(/^client error: /i);
    // No toast — chat errors don't dispatch a notice anymore.
    expect(result.current.state.notice).toBeNull();
  });

  it("loads persisted retention history for admin sessions", async () => {
    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input);
      const headers = new Headers(init?.headers);
      const authHeader = headers.get("Authorization");
      if (url === "/healthz") {
        return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
      }
      if (url === "/v1/whoami") {
        if (authHeader === "Bearer admin-secret") {
          return jsonResponse({
            object: "session",
            data: {
              authenticated: true,
              invalid_token: false,
              role: "admin",
              name: "admin",
              source: "admin_token",
            },
          });
        }
        return jsonResponse({
          object: "session",
          data: {
            authenticated: false,
            invalid_token: false,
            role: "anonymous",
            source: "no_token",
          },
        });
      }
      if (url === "/v1/models") {
        return jsonResponse({
          object: "list",
          data: [{ id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } }],
        });
      }
      if (url === "/v1/provider-presets") {
        return jsonResponse({
          object: "provider_presets",
          data: [{ id: "openai", name: "OpenAI", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com" }],
        });
      }
      if (url === "/admin/providers") {
        return jsonResponse({ object: "provider_status", data: [] });
      }
      if (url === "/admin/budget") {
        return unauthorizedResponse();
      }
      if (url === "/admin/control-plane") {
        return jsonResponse({ object: "control_plane", data: { backend: "memory", tenants: [], api_keys: [], events: [] } });
      }
      if (url === "/admin/accounts/summary") {
        return jsonResponse({
          object: "account_summary",
          data: {
            account: {
              key: "global",
              scope: "global",
              backend: "memory",
              balance_source: "config",
              debited_micros_usd: 250000,
              debited_usd: "0.250000",
              credited_micros_usd: 1000000,
              credited_usd: "1.000000",
              balance_micros_usd: 750000,
              balance_usd: "0.750000",
              available_micros_usd: 750000,
              available_usd: "0.750000",
              enforced: true,
            },
            estimates: [
              {
                provider: "openai",
                provider_kind: "cloud",
                model: "gpt-4o-mini",
                priced: true,
                input_micros_usd_per_million_tokens: 150000,
                output_micros_usd_per_million_tokens: 600000,
                estimated_remaining_prompt_tokens: 5000000,
                estimated_remaining_output_tokens: 1250000,
              },
            ],
          },
        });
      }
      if (url === "/v1/chat/sessions?limit=20") {
        return jsonResponse({ object: "chat_sessions", data: [] });
      }
      if (url === "/admin/retention/runs?limit=10") {
        return jsonResponse({
          object: "retention_runs",
          data: [
            {
              started_at: "2026-04-22T10:00:00Z",
              finished_at: "2026-04-22T10:00:05Z",
              trigger: "manual",
              actor: "admin:req-1",
              request_id: "req-1",
              results: [{ name: "trace_snapshots", deleted: 12, max_age: "24h", max_count: 2000 }],
            },
          ],
        });
      }
      return unauthorizedResponse();
    });

    const { result } = renderHook(() => useRuntimeConsole());

    act(() => {
      result.current.actions.setAuthToken("admin-secret");
    });

    await waitFor(() => {
      expect(result.current.state.loading).toBe(false);
      expect(result.current.state.session.kind).toBe("admin");
      expect(result.current.state.retentionRuns).toHaveLength(1);
      expect(result.current.state.retentionLastRun?.request_id).toBe("req-1");
      expect(result.current.state.retentionLastRun?.actor).toBe("admin:req-1");
      expect(result.current.state.accountSummary?.estimates).toHaveLength(1);
    });
  });


  it("resets an unavailable preset example model for a configured provider", async () => {
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === "/healthz") {
        return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
      }
      if (url === "/v1/whoami") {
        // Authenticated tenant — needed because dashboard now gates
        // /v1/models, /v1/provider-presets, /admin/providers behind
        // an authenticated session (the 401-storm fix).
        return jsonResponse({
          object: "session",
          data: {
            authenticated: true,
            invalid_token: false,
            role: "tenant",
            tenant: "acme",
            source: "bearer",
          },
        });
      }
      if (url === "/v1/models") {
        return jsonResponse({
          object: "list",
          data: [{ id: "llama3.1:8b", owned_by: "ollama", metadata: { provider: "ollama", provider_kind: "local", default: true } }],
        });
      }
      if (url === "/v1/provider-presets") {
        return jsonResponse({
          object: "provider_presets",
          data: [
            {
              id: "ollama",
              name: "Ollama",
              kind: "local",
              protocol: "openai",
              base_url: "http://127.0.0.1:11434/v1",
            },
          ],
        });
      }
      if (url === "/v1/providers" || url === "/admin/providers") {
        return jsonResponse({
          object: "provider_status",
          data: [{ name: "ollama", kind: "local", healthy: true, status: "healthy", default_model: "llama3.1:8b", models: ["llama3.1:8b"] }],
        });
      }
      if (url.startsWith("/admin/retention/runs")) {
        return unauthorizedResponse();
      }
      if (url.startsWith("/admin/accounts/summary")) {
        return unauthorizedResponse();
      }
      if (url.startsWith("/v1/chat/sessions")) {
        return unauthorizedResponse();
      }
      return unauthorizedResponse();
    });

    const { result } = renderHook(() => useRuntimeConsole());

    await waitFor(() => expect(result.current.state.loading).toBe(false));

    act(() => {
      result.current.actions.setProviderFilter("ollama");
    });

    await waitFor(() => expect(result.current.state.model).toBe("llama3.1:8b"));

    act(() => {
      result.current.actions.setModel("qwen2.5:7b");
    });

    await waitFor(() => expect(result.current.state.model).toBe("llama3.1:8b"));
  });

  // ─── selectProviderRoute: scoped-default never thrashes ───────────────────
  //
  // Regression for the "ModelPicker blinks fast when picking Ollama / LM
  // Studio with the runtime not running" report against v0.1.0-alpha.1.
  //
  // The setup: provider catalog includes a local provider (Ollama) but
  // /v1/models returns no Ollama models — the discovery probe failed
  // because the runtime isn't listening. defaultModelForProvider("ollama",
  // ...) returns "" in that state. Two useEffects in the hook used to
  // fight here:
  //   (1) "scoped validity" cleared model="" → "" each cycle, no-op
  //   (2) "fallback to gateway default" set model "" → gpt-4o-mini
  //       (the cross-provider default), which (1) then re-cleared.
  // The cycle re-rendered every frame and visibly flickered the
  // ModelPicker trigger label.
  //
  // Fix: gate (2) on providerFilter === "auto". When a specific provider
  // is scoped, that effect must not override the scoped state with a
  // cross-provider default. This test pins the no-thrash behavior by
  // selecting Ollama and asserting the model settles empty (the correct
  // "no model available for this provider" state) rather than landing on
  // the openai default.
  it("leaves model empty when selecting a provider with no discovered models", async () => {
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === "/healthz") return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
      if (url === "/v1/whoami") {
        return jsonResponse({
          object: "session",
          data: { authenticated: true, invalid_token: false, role: "admin", source: "bearer" },
        });
      }
      if (url === "/v1/models") {
        // Only openai models are discovered — Ollama runtime isn't up,
        // so its preset shows no models in the catalog.
        return jsonResponse({
          object: "list",
          data: [
            { id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud", default: true } },
          ],
        });
      }
      if (url === "/v1/provider-presets") {
        return jsonResponse({
          object: "provider_presets",
          data: [
            { id: "openai", name: "OpenAI", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com" },
            { id: "ollama", name: "Ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1" },
          ],
        });
      }
      if (url.startsWith("/v1/providers")) {
        return jsonResponse({
          object: "list",
          data: [
            { name: "openai", kind: "cloud", default_model: "gpt-4o-mini", models: ["gpt-4o-mini"], healthy: true },
            { name: "ollama", kind: "local", default_model: "", models: [], healthy: false },
          ],
        });
      }
      // Both providers are configured in the CP store — without this the
      // dashboard short-circuits discovery (no point fetching /v1/models
      // for an empty provider list) and the "default model" effect this
      // test exercises never fires.
      if (url === "/admin/control-plane") {
        return jsonResponse({
          object: "configured_state",
          data: {
            providers: [
              { id: "openai", name: "OpenAI", preset_id: "openai", kind: "cloud", protocol: "openai", base_url: "https://api.openai.com", enabled: true, credential_configured: true },
              { id: "ollama", name: "Ollama", preset_id: "ollama", kind: "local", protocol: "openai", base_url: "http://127.0.0.1:11434/v1", enabled: true, credential_configured: false },
            ],
            tenants: [], api_keys: [], policy_rules: [], pricebook: [], events: [],
          },
        });
      }
      return unauthorizedResponse();
    });

    const { result } = renderHook(() => useRuntimeConsole());
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    // Sanity: the gateway default is the openai model when providerFilter is "auto".
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));

    await act(async () => {
      result.current.actions.setProviderFilter("ollama");
    });

    // After selecting Ollama, the model must settle empty — Ollama has
    // no discovered models. The bug was that effect (2) above kept
    // setting it back to "gpt-4o-mini" and effect (1) kept clearing it,
    // creating an infinite re-render loop.
    await waitFor(() => {
      expect(result.current.state.providerFilter).toBe("ollama");
      expect(result.current.state.model).toBe("");
    });

    // Settle for a few microtasks and confirm the model stays empty —
    // catches the oscillation case where the assertion above happened
    // to land on a cycle where model === "".
    for (let i = 0; i < 5; i++) {
      await new Promise(r => setTimeout(r, 10));
      expect(result.current.state.model).toBe("");
    }
  });

  // ─── applyPricebookImport: notice text per outcome ────────────────────────
  //
  // The toast wording on the dashboard's notice banner is the
  // operator's primary feedback for a bulk import. Three branches:
  //   * all rows succeeded → "Imported N rows."
  //   * mixed              → "Imported N, M failed."
  //   * all rows failed    → "Import failed for N rows."
  // These tests pin the wording so a refactor doesn't accidentally
  // collapse mixed/failed into a generic "applied" success notice.
  describe("applyPricebookImport notice variants", () => {
    function mockApplyResponse(data: Record<string, unknown>) {
      fetchMock.mockImplementation(async (input) => {
        const url = String(input);
        if (url === "/admin/control-plane/pricebook/import/apply") {
          return jsonResponse({ object: "control_plane_pricebook_import_diff", data });
        }
        if (url === "/healthz") return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
        if (url === "/v1/whoami") {
          return jsonResponse({
            object: "session",
            data: { authenticated: true, invalid_token: false, role: "admin", source: "bearer" },
          });
        }
        if (url === "/v1/models") return jsonResponse({ object: "list", data: [] });
        if (url === "/v1/provider-presets") return jsonResponse({ object: "provider_presets", data: [] });
        return unauthorizedResponse();
      });
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

  // Admin mutations route through runAdminMutation, which on success
  // fires a follow-up loadDashboard() and sets a success notice — and on
  // failure populates BOTH adminConfigError (the inline banner) AND
  // notice (the toast). These tests pin both ends so a refactor that
  // drops one of the surfaces is caught.
  describe("admin mutations", () => {
    function adminFetchMock(routes: Record<string, () => Response>) {
      return async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url === "/healthz") return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
        if (url === "/v1/whoami") {
          return jsonResponse({
            object: "session",
            data: { authenticated: true, invalid_token: false, role: "admin", name: "admin", source: "admin_token" },
          });
        }
        if (url === "/v1/models") return jsonResponse({ object: "list", data: [] });
        if (url === "/v1/provider-presets") return jsonResponse({ object: "provider_presets", data: [] });
        if (url === "/admin/providers") return jsonResponse({ object: "provider_status", data: [] });
        if (url === "/admin/control-plane") return jsonResponse({ object: "control_plane", data: { backend: "memory", tenants: [], api_keys: [], events: [], providers: [], pricebook: [], policy_rules: [] } });
        if (url.startsWith("/admin/budget")) return jsonResponse({ object: "budget_status", data: null });
        if (url.startsWith("/admin/accounts/summary")) return jsonResponse({ object: "account_summary", data: null });
        if (url.startsWith("/v1/chat/sessions")) return jsonResponse({ object: "chat_sessions", data: [] });
        if (url.startsWith("/admin/retention/runs")) return jsonResponse({ object: "retention_runs", data: [] });
        if (url.startsWith("/admin/requests")) return jsonResponse({ object: "requests", data: [] });
        const handler = routes[url];
        if (handler) return handler();
        return unauthorizedResponse();
      };
    }

    beforeEach(() => {
      window.localStorage.setItem("hecate.authToken", "admin-secret");
    });

    it("setProviderAPIKey rotate sends PUT, fires loadDashboard, surfaces success notice", async () => {
      let putCalls = 0;
      let putBody = "";
      const baseMock = adminFetchMock({});
      fetchMock.mockImplementation((async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/admin/control-plane/providers/anthropic/api-key" && init?.method === "PUT") {
          putCalls++;
          putBody = String(init.body ?? "");
          return jsonResponse({ object: "control_plane_provider_api_key", data: { id: "anthropic" } });
        }
        return baseMock(input);
      }) as never);

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.setProviderAPIKey("anthropic", "sk-new");
      });
      await waitFor(() => expect(result.current.state.notice?.message).toBe("API key saved."));
      expect(putCalls).toBe(1);
      expect(JSON.parse(putBody)).toEqual({ key: "sk-new" });
      expect(result.current.state.notice?.kind).toBe("success");
      // No adminConfigError on success.
      expect(result.current.state.adminConfigError).toBe("");
    });

    it("setProviderAPIKey clear (empty key) sends PUT and reads 'API key cleared.'", async () => {
      let putBody = "";
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/admin/control-plane/providers/openai/api-key" && init?.method === "PUT") {
          putBody = String(init.body ?? "");
          return jsonResponse({ object: "control_plane_provider_api_key", data: { id: "openai", status: "cleared" } });
        }
        return adminFetchMock({})(input);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.setProviderAPIKey("openai", "");
      });
      await waitFor(() => expect(result.current.state.notice?.message).toBe("API key cleared."));
      expect(JSON.parse(putBody)).toEqual({ key: "" });
    });

    it("setProviderAPIKey failure surfaces both adminConfigError and an error notice", async () => {
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/admin/control-plane/providers/anthropic/api-key" && init?.method === "PUT") {
          return new Response(
            JSON.stringify({ error: { message: "secret store is read-only" } }),
            { status: 400, headers: { "Content-Type": "application/json" } },
          );
        }
        return adminFetchMock({})(input);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.setProviderAPIKey("anthropic", "sk-new");
      });
      await waitFor(() => expect(result.current.state.notice?.kind).toBe("error"));
      // Inline banner: the failureDetail string lives in adminConfigError.
      // Toaster: the user-facing errorMessage lives in notice.message.
      // Both must be populated — operators can be looking at either.
      expect(result.current.state.notice?.message).toBe("Failed to save API key.");
      expect(result.current.state.adminConfigError).toContain("secret store is read-only");
    });

    it("setProviderBaseURL PATCHes base_url and reloads providers", async () => {
      // The toggle is gone; PATCH is now strictly a base_url update.
      // setProviderBaseURL fires the PATCH then refreshProviders so the
      // table converges on the new endpoint without a full page reload.
      let patchHits = 0;
      let patchBody = "";
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/admin/control-plane/providers/ollama" && init?.method === "PATCH") {
          patchHits++;
          patchBody = String(init.body ?? "");
          return jsonResponse({ object: "control_plane_provider", data: { id: "ollama", base_url: "http://192.168.1.10:11434/v1" } });
        }
        return adminFetchMock({})(input);
      });

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.setProviderBaseURL("ollama", "http://192.168.1.10:11434/v1");
      });
      await waitFor(() => expect(patchHits).toBe(1));
      expect(JSON.parse(patchBody)).toEqual({ base_url: "http://192.168.1.10:11434/v1" });
      expect(result.current.state.adminConfigError).toBe("");
    });

    it("upsertPolicyRule POSTs the payload + fires success notice", async () => {
      let postBody = "";
      const baseMock = adminFetchMock({});
      fetchMock.mockImplementation((async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/admin/control-plane/policy-rules" && init?.method === "POST") {
          postBody = String(init.body ?? "");
          return jsonResponse({ object: "control_plane_policy_rule", data: {} });
        }
        return baseMock(input);
      }) as never);

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.upsertPolicyRule({
          id: "deny-cloud",
          action: "deny",
          tenants: ["team-a"],
          provider_kinds: ["cloud"],
          reason: "team-a is local-only",
        });
      });
      await waitFor(() => expect(result.current.state.notice?.message).toBe("Policy rule saved."));
      expect(JSON.parse(postBody)).toMatchObject({
        id: "deny-cloud",
        action: "deny",
        tenants: ["team-a"],
        provider_kinds: ["cloud"],
        reason: "team-a is local-only",
      });
      expect(result.current.state.adminConfigError).toBe("");
    });

    it("deletePolicyRule POSTs to the delete endpoint with the id", async () => {
      let postBody = "";
      const baseMock = adminFetchMock({});
      fetchMock.mockImplementation((async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/admin/control-plane/policy-rules/delete" && init?.method === "POST") {
          postBody = String(init.body ?? "");
          return jsonResponse({ object: "control_plane_policy_rule_deleted", data: {} });
        }
        return baseMock(input);
      }) as never);

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.deletePolicyRule("deny-cloud");
      });
      await waitFor(() => expect(result.current.state.notice?.message).toBe("Policy rule deleted."));
      expect(JSON.parse(postBody)).toEqual({ id: "deny-cloud" });
    });

    it("upsertPolicyRule failure populates BOTH inline banner and toast", async () => {
      const baseMock = adminFetchMock({});
      fetchMock.mockImplementation((async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/admin/control-plane/policy-rules" && init?.method === "POST") {
          return new Response(
            JSON.stringify({ error: { message: "rule id is required" } }),
            { status: 400, headers: { "Content-Type": "application/json" } },
          );
        }
        return baseMock(input);
      }) as never);

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.upsertPolicyRule({ id: "", action: "deny" });
      });
      await waitFor(() => expect(result.current.state.notice?.kind).toBe("error"));
      expect(result.current.state.notice?.message).toBe("Failed to save policy rule.");
      expect(result.current.state.adminConfigError).toContain("rule id is required");
    });
  });

  // Chat session ops mutate sidebar state and chatError differently
  // from the chat submission flow. These tests pin the side effects so
  // a refactor of useRuntimeConsole's session reducer is caught.
  describe("chat session actions", () => {
    function adminFetchMockWithSessions(sessions: Array<{ id: string; title: string }>, routes: Record<string, () => Response> = {}) {
      return async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url === "/healthz") return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
        if (url === "/v1/whoami") {
          return jsonResponse({
            object: "session",
            data: { authenticated: true, invalid_token: false, role: "admin", name: "admin", source: "admin_token" },
          });
        }
        if (url === "/v1/models") return jsonResponse({ object: "list", data: [] });
        if (url === "/v1/provider-presets") return jsonResponse({ object: "provider_presets", data: [] });
        if (url === "/admin/providers") return jsonResponse({ object: "provider_status", data: [] });
        if (url === "/admin/control-plane") return jsonResponse({ object: "control_plane", data: { backend: "memory", tenants: [], api_keys: [], events: [], providers: [], pricebook: [], policy_rules: [] } });
        if (url.startsWith("/admin/budget")) return jsonResponse({ object: "budget_status", data: null });
        if (url.startsWith("/admin/accounts/summary")) return jsonResponse({ object: "account_summary", data: null });
        if (url === "/v1/chat/sessions?limit=20") {
          const data = sessions.map(s => ({
            ...s, turns: [], created_at: "2026-04-20T00:00:00Z", updated_at: "2026-04-20T00:00:00Z",
          }));
          return jsonResponse({ object: "chat_sessions", data, has_more: false });
        }
        if (url.startsWith("/admin/retention/runs")) return jsonResponse({ object: "retention_runs", data: [] });
        if (url.startsWith("/admin/requests")) return jsonResponse({ object: "requests", data: [] });
        const handler = routes[url];
        if (handler) return handler();
        return unauthorizedResponse();
      };
    }

    beforeEach(() => {
      window.localStorage.setItem("hecate.authToken", "admin-secret");
    });

    it("selectChatSession populates activeChatSession on success", async () => {
      fetchMock.mockImplementation(adminFetchMockWithSessions([{ id: "sess_42", title: "Existing" }], {
        "/v1/chat/sessions/sess_42": () => jsonResponse({
          object: "chat_session",
          data: {
            id: "sess_42",
            title: "Existing",
            messages: [
              { id: "msg_u", sequence: 0, role: "user", content: "hi", created_at: "2026-04-20T00:00:00Z" },
              { id: "msg_a", sequence: 1, produced_by_call_id: "call_1", role: "assistant", content: "hello", created_at: "2026-04-20T00:00:00Z" },
            ],
            provider_calls: [{
              id: "call_1",
              request_id: "req_1",
              provider: "openai", model: "gpt-4o-mini",
              cost_micros_usd: 0, cost_usd: "0",
              prompt_tokens: 1, completion_tokens: 1, total_tokens: 2,
              created_at: "2026-04-20T00:00:00Z",
            }],
            created_at: "2026-04-20T00:00:00Z", updated_at: "2026-04-20T00:00:00Z",
          },
        }),
      }) as never);

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.selectChatSession("sess_42");
      });
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("sess_42"));
      expect(result.current.state.activeChatSessionID).toBe("sess_42");
      expect(result.current.state.activeChatSession?.messages).toHaveLength(2);
      expect(result.current.state.activeChatSession?.provider_calls).toHaveLength(1);
      expect(result.current.state.chatError).toBe("");
    });

    it("selectChatSession 404 sets chatError + error notice but still updates activeChatSessionID", async () => {
      fetchMock.mockImplementation(adminFetchMockWithSessions([{ id: "sess_gone", title: "Gone" }], {
        "/v1/chat/sessions/sess_gone": () => new Response(
          JSON.stringify({ error: { message: "session not found" } }),
          { status: 404, headers: { "Content-Type": "application/json" } },
        ),
      }) as never);

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.selectChatSession("sess_gone");
      });
      // The ID flips first (optimistic) — the error path runs after the
      // GET fails. Inline + toast both populated so the operator can
      // see the failure regardless of viewport focus.
      expect(result.current.state.activeChatSessionID).toBe("sess_gone");
      await waitFor(() => expect(result.current.state.chatError).toContain("session not found"));
      expect(result.current.state.notice?.kind).toBe("error");
    });

    it("deleteChatSession removes the session from the sidebar and notices", async () => {
      let deleteCalls = 0;
      const baseMock = adminFetchMockWithSessions(
        [{ id: "sess_a", title: "Keep" }, { id: "sess_b", title: "Delete me" }],
        {},
      );
      fetchMock.mockImplementation((async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/v1/chat/sessions/sess_b" && init?.method === "DELETE") {
          deleteCalls++;
          // 204 must have a null body per the Response constructor spec.
          return new Response(null, { status: 204 });
        }
        return baseMock(input);
      }) as never);

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.chatSessions).toHaveLength(2));

      await act(async () => {
        await result.current.actions.deleteChatSession("sess_b");
      });

      expect(deleteCalls).toBe(1);
      await waitFor(() => expect(result.current.state.chatSessions.map(s => s.id)).toEqual(["sess_a"]));
      expect(result.current.state.notice?.kind).toBe("success");
      expect(result.current.state.notice?.message).toBe("Session deleted.");
    });

    it("renameChatSession patches the title in the sidebar", async () => {
      const baseMock = adminFetchMockWithSessions(
        [{ id: "sess_a", title: "Old title" }],
        {},
      );
      fetchMock.mockImplementation((async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/v1/chat/sessions/sess_a" && init?.method === "PATCH") {
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "sess_a",
              title: "Renamed",
              turns: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:01:00Z",
            },
          });
        }
        return baseMock(input);
      }) as never);

      const { result } = renderHook(() => useRuntimeConsole());
      await waitFor(() => expect(result.current.state.chatSessions).toHaveLength(1));

      await act(async () => {
        await result.current.actions.renameChatSession("sess_a", "Renamed");
      });

      await waitFor(() => expect(result.current.state.chatSessions[0].title).toBe("Renamed"));
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
    expect(humanizeChatError("upstream timeout")).toBe("upstream timeout");
    expect(humanizeChatError("")).toBe("");
  });
});

function jsonResponse(payload: unknown): Response {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

function unauthorizedResponse(): Response {
  return new Response(JSON.stringify({ error: { message: "unauthorized" } }), {
    status: 401,
    headers: { "Content-Type": "application/json" },
  });
}

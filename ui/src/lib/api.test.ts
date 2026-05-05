import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  buildRequestOptions,
  cancelAgentChatApproval,
  chatCompletions,
  deleteAgentChatGrant,
  deletePolicyRule,
  dispatchAgentChatStreamEvent,
  getAgentChatApproval,
  getBudget,
  getSession,
  getTrace,
  listAgentChatApprovals,
  listAgentChatGrants,
  probeAgentAdapter,
  resolveAgentChatApproval,
  setProviderAPIKey,
  setProviderBaseURL,
  streamAgentChatSession,
  upsertPolicyRule,
  type ApiError,
} from "./api";

describe("api client", () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("sets the json content-type when posting a body", () => {
    const options = buildRequestOptions({
      method: "POST",
      body: { hello: "world" },
    });

    const headers = new Headers(options.headers);
    expect(options.method).toBe("POST");
    expect(headers.get("Content-Type")).toBe("application/json");
    expect(options.body).toBe(JSON.stringify({ hello: "world" }));
  });

  it("builds budget requests with query strings intact", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ object: "budget_status", data: { key: "global" } }));

    await getBudget("?scope=provider&provider=ollama");

    expect(fetchMock).toHaveBeenCalledWith(
      "/admin/budget?scope=provider&provider=ollama",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("fetches session details for whoami", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({
        object: "session",
        data: {
          authenticated: true,
          invalid_token: false,
          role: "admin",
          source: "anonymous",
        },
      }),
    );

    const result = await getSession();

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/whoami",
      expect.objectContaining({ method: "GET" }),
    );
    expect(result.data.role).toBe("admin");
  });

  it("returns chat payload plus runtime headers", async () => {
    fetchMock.mockResolvedValue(
      new Response(
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
            "X-Runtime-Semantic-Strategy": "postgres_pgvector",
            "X-Runtime-Semantic-Index": "hnsw",
            "X-Runtime-Semantic-Similarity": "0.981234",
            "X-Runtime-Attempts": "2",
            "X-Runtime-Retries": "1",
            "X-Runtime-Fallback-From": "ollama",
            "X-Runtime-Cost-USD": "0.000123",
          },
        },
      ),
    );

    const result = await chatCompletions({
      model: "gpt-4o-mini",
      provider: "",
      user: "",
      messages: [{ role: "user", content: "hello" }],
    });

    expect(result.data.id).toBe("chatcmpl-123");
    expect(result.headers.traceId).toBe("trace-123");
    expect(result.headers.spanId).toBe("span-123");
    expect(result.headers.provider).toBe("openai");
    expect(result.headers.routeReason).toBe("requested_model");
    expect(result.headers.cacheType).toBe("false");
    expect(result.headers.semanticStrategy).toBe("postgres_pgvector");
    expect(result.headers.semanticIndex).toBe("hnsw");
    expect(result.headers.semanticSimilarity).toBe("0.981234");
    expect(result.headers.attempts).toBe("2");
    expect(result.headers.retries).toBe("1");
    expect(result.headers.fallbackFrom).toBe("ollama");
  });

  it("turns browser fetch failures into actionable gateway errors", async () => {
    fetchMock.mockRejectedValue(new TypeError("Load failed"));

    await expect(
      chatCompletions({
        model: "llama3.1:8b",
        provider: "ollama",
        user: "",
        messages: [{ role: "user", content: "hello" }],
      }),
    ).rejects.toThrow("Check that the gateway is running on http://127.0.0.1:8765");
  });

  it("fetches a request trace by request id", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({
        object: "trace",
        data: {
          request_id: "req-123",
          trace_id: "req-123",
          started_at: "2026-04-21T00:00:00Z",
          spans: [
            {
              trace_id: "req-123",
              span_id: "span-1",
              name: "gateway.request",
              kind: "server",
              events: [
                { name: "request.received", timestamp: "2026-04-21T00:00:00Z", attributes: { model: "gpt-4o-mini" } },
                { name: "response.returned", timestamp: "2026-04-21T00:00:01Z", attributes: { provider: "openai" } },
              ],
            },
          ],
        },
      }),
    );

    const result = await getTrace("req-123");

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/traces?request_id=req-123",
      expect.objectContaining({ method: "GET" }),
    );
    expect(result.data.request_id).toBe("req-123");
    expect(result.data.spans).toHaveLength(1);
    expect(result.data.spans?.[0]?.events).toHaveLength(2);
  });

  describe("provider REST API", () => {
    it("PATCH /providers/{id} updates the base_url", async () => {
      fetchMock.mockResolvedValue(jsonResponse({}));

      await setProviderBaseURL("ollama", "http://192.168.1.10:11434/v1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/admin/control-plane/providers/ollama",
        expect.objectContaining({
          method: "PATCH",
          body: JSON.stringify({ base_url: "http://192.168.1.10:11434/v1" }),
        }),
      );
    });

    it("PATCH /providers/{id} URL-encodes provider names with special characters", async () => {
      fetchMock.mockResolvedValue(jsonResponse({}));

      await setProviderBaseURL("my provider", "http://localhost:11434/v1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/admin/control-plane/providers/my%20provider",
        expect.anything(),
      );
    });

    it("PUT /providers/{id}/api-key to set credentials", async () => {
      fetchMock.mockResolvedValue(jsonResponse({}));

      await setProviderAPIKey("anthropic", "sk-new-key");

      expect(fetchMock).toHaveBeenCalledWith(
        "/admin/control-plane/providers/anthropic/api-key",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({ key: "sk-new-key" }),
        }),
      );
    });

    it("PUT /providers/{id}/api-key with empty key clears credentials", async () => {
      fetchMock.mockResolvedValue(jsonResponse({}));

      await setProviderAPIKey("anthropic", "");

      expect(fetchMock).toHaveBeenCalledWith(
        "/admin/control-plane/providers/anthropic/api-key",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({ key: "" }),
        }),
      );
    });
  });

  describe("policy rule REST API", () => {
    it("POST /policy-rules sends the full payload through verbatim", async () => {
      fetchMock.mockResolvedValue(jsonResponse({}));

      await upsertPolicyRule({
        id: "deny-cloud",
        action: "deny",
        reason: "local-only",
        provider_kinds: ["cloud"],
      });

      expect(fetchMock).toHaveBeenCalledWith(
        "/admin/control-plane/policy-rules",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({
            id: "deny-cloud",
            action: "deny",
            reason: "local-only",
            provider_kinds: ["cloud"],
          }),
        }),
      );
    });

    it("POST /policy-rules with rewrite_model carries the target model", async () => {
      fetchMock.mockResolvedValue(jsonResponse({}));

      await upsertPolicyRule({
        id: "downgrade-to-mini",
        action: "rewrite_model",
        models: ["gpt-4o"],
        rewrite_model_to: "gpt-4o-mini",
      });

      const call = fetchMock.mock.calls.at(-1);
      const body = JSON.parse((call?.[1] as RequestInit | undefined)?.body as string);
      expect(body.action).toBe("rewrite_model");
      expect(body.rewrite_model_to).toBe("gpt-4o-mini");
    });

    it("POST /policy-rules/delete sends only the id", async () => {
      fetchMock.mockResolvedValue(jsonResponse({}));

      await deletePolicyRule("deny-cloud");

      expect(fetchMock).toHaveBeenCalledWith(
        "/admin/control-plane/policy-rules/delete",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ id: "deny-cloud" }),
        }),
      );
    });
  });

  describe("buildRequestOptions edge cases", () => {
    it("omits Content-Type for body-less requests", () => {
      const options = buildRequestOptions({ method: "DELETE" });
      const headers = new Headers(options.headers);
      expect(headers.get("Content-Type")).toBeNull();
      expect(options.body).toBeUndefined();
    });

    it("defaults method to GET when not specified", () => {
      const options = buildRequestOptions({});
      expect(options.method).toBe("GET");
    });

    it("never sends an Authorization header (single-user mode is unauthenticated)", () => {
      const options = buildRequestOptions({ method: "POST", body: { x: 1 } });
      const headers = new Headers(options.headers);
      expect(headers.get("Authorization")).toBeNull();
    });
  });

  describe("error-mapping edge cases", () => {
    it("surfaces the gateway's error.message when the body is well-formed JSON", async () => {
      fetchMock.mockResolvedValue(
        new Response(
          JSON.stringify({ error: { type: "rate_limit_exceeded", message: "slow down" } }),
          { status: 429, headers: { "Content-Type": "application/json" } },
        ),
      );
      await expect(getBudget("?scope=global")).rejects.toMatchObject({
        message: "slow down",
        status: 429,
        code: "rate_limit_exceeded",
      } satisfies Partial<ApiError>);
    });

    it("falls back to the static label when the error body is not valid JSON", async () => {
      fetchMock.mockResolvedValue(
        new Response("<html>500</html>", {
          status: 500,
          headers: { "Content-Type": "text/html" },
        }),
      );
      await expect(getBudget("?scope=global")).rejects.toThrow(/request failed/);
    });

    it("returns undefined for a 204 No Content response", async () => {
      fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
      await expect(setProviderAPIKey("anthropic", "")).resolves.not.toThrow();
    });

    it("rewrites 'Failed to fetch' network errors into actionable gateway URLs", async () => {
      fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
      await expect(getBudget("?scope=global")).rejects.toThrow(/Check that the gateway is running/);
    });

    it("rewrites 'NetworkError' substring matches the same way", async () => {
      fetchMock.mockRejectedValue(new TypeError("NetworkError when attempting to fetch resource."));
      await expect(getBudget("?scope=global")).rejects.toThrow(/Check that the gateway is running/);
    });

    it("preserves non-network error messages with the request URL prepended", async () => {
      fetchMock.mockRejectedValue(new Error("AbortError: aborted"));
      await expect(getBudget("?scope=global")).rejects.toThrow(/\/admin\/budget.*AbortError: aborted/);
    });
  });

  // ─── Agent-chat approvals & grants ─────────────────────────────────────────

  describe("agent-chat approvals", () => {
    it("lists approvals scoped to status=pending", async () => {
      fetchMock.mockResolvedValue(jsonResponse({ object: "list", data: [] }));

      await listAgentChatApprovals("sess-1", "pending");

      expect(fetchMock).toHaveBeenCalledWith(
        "/v1/agent-chat/sessions/sess-1/approvals?status=pending",
        expect.objectContaining({ method: "GET" }),
      );
    });

    it("omits the status query string when no filter is passed", async () => {
      fetchMock.mockResolvedValue(jsonResponse({ object: "list", data: [] }));

      await listAgentChatApprovals("sess-1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/v1/agent-chat/sessions/sess-1/approvals",
        expect.objectContaining({ method: "GET" }),
      );
    });

    it("URL-encodes ids on get", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({ object: "agent_chat_approval", data: { id: "ap/1", session_id: "s 1" } }),
      );

      await getAgentChatApproval("s 1", "ap/1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/v1/agent-chat/sessions/s%201/approvals/ap%2F1",
        expect.objectContaining({ method: "GET" }),
      );
    });

    it("posts the resolve decision body", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({ object: "agent_chat_approval", data: { id: "ap-1" } }),
      );

      await resolveAgentChatApproval("sess-1", "ap-1", {
        decision: "approve",
        scope: "once",
        selected_option: "opt-1",
        note: "looks fine",
      });

      const [url, options] = fetchMock.mock.lastCall ?? [];
      expect(url).toBe("/v1/agent-chat/sessions/sess-1/approvals/ap-1/resolve");
      expect(options?.method).toBe("POST");
      const body = options?.body;
      expect(typeof body === "string" ? JSON.parse(body) : body).toEqual({
        decision: "approve",
        scope: "once",
        selected_option: "opt-1",
        note: "looks fine",
      });
    });

    it("posts an empty body to cancel", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({ object: "agent_chat_approval", data: { id: "ap-1" } }),
      );

      await cancelAgentChatApproval("sess-1", "ap-1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/v1/agent-chat/sessions/sess-1/approvals/ap-1/cancel",
        expect.objectContaining({ method: "POST" }),
      );
    });

    it("builds the grants list query string from non-empty filter fields only", async () => {
      fetchMock.mockResolvedValue(jsonResponse({ object: "list", data: [] }));

      await listAgentChatGrants({ adapter_id: "codex", scope: "session" });

      const [url] = fetchMock.mock.lastCall ?? [];
      // URLSearchParams ordering is insertion-order; both forms are
      // acceptable so long as the query string contains both pairs.
      expect(url).toContain("/v1/agent-chat/grants?");
      expect(url).toContain("adapter_id=codex");
      expect(url).toContain("scope=session");
    });

    it("sends DELETE on grant revocation", async () => {
      fetchMock.mockResolvedValue(new Response(null, { status: 204 }));

      await deleteAgentChatGrant("grant-1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/v1/agent-chat/grants/grant-1",
        expect.objectContaining({ method: "DELETE" }),
      );
    });
  });

  // ─── Agent adapter health probe ────────────────────────────────────────────

  describe("probeAgentAdapter", () => {
    it("URL-encodes the adapter id and returns the typed payload", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          object: "agent_adapter_health",
          data: {
            adapter_id: "codex",
            status: "ready",
            stage: "ready",
            path: "/usr/local/bin/codex-acp",
            duration_ms: 412,
          },
        }),
      );

      const result = await probeAgentAdapter("codex");

      expect(fetchMock).toHaveBeenCalledWith(
        "/v1/agent-adapters/codex/health",
        expect.objectContaining({ method: "GET" }),
      );
      expect(result.data.status).toBe("ready");
      expect(result.data.duration_ms).toBe(412);
    });

    it("escapes ids with special characters", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          object: "agent_adapter_health",
          data: { adapter_id: "weird id", status: "error", stage: "lookup", duration_ms: 0 },
        }),
      );

      await probeAgentAdapter("weird id");

      const [url] = fetchMock.mock.lastCall ?? [];
      expect(url).toBe("/v1/agent-adapters/weird%20id/health");
    });
  });

  // ─── Agent-chat SSE dispatch ───────────────────────────────────────────────

  describe("dispatchAgentChatStreamEvent", () => {
    it("maps named session_update events onto the typed union", () => {
      const out = dispatchAgentChatStreamEvent(
        "session_update",
        JSON.stringify({ object: "agent_chat_session", data: { id: "s" } }),
      );
      expect(out).toEqual({
        type: "session_update",
        payload: { object: "agent_chat_session", data: { id: "s" } },
      });
    });

    it("treats the default `message` event as session_update for backward compat", () => {
      const out = dispatchAgentChatStreamEvent(
        "message",
        JSON.stringify({ object: "agent_chat_session", data: { id: "s" } }),
      );
      expect(out?.type).toBe("session_update");
    });

    it("maps existing backend snapshot/done events onto session_update", () => {
      for (const eventName of ["snapshot", "done"]) {
        const out = dispatchAgentChatStreamEvent(
          eventName,
          JSON.stringify({ object: "agent_chat_session", data: { id: "s" } }),
        );
        expect(out).toEqual({
          type: "session_update",
          payload: { object: "agent_chat_session", data: { id: "s" } },
        });
      }
    });

    it("maps approval.requested onto the requested-event payload", () => {
      const out = dispatchAgentChatStreamEvent(
        "approval.requested",
        JSON.stringify({ approval_id: "ap-1", session_id: "s", adapter_id: "codex", tool_kind: "fs", created_at: "t", expires_at: "t" }),
      );
      expect(out).toEqual({
        type: "approval.requested",
        payload: {
          approval_id: "ap-1",
          session_id: "s",
          adapter_id: "codex",
          tool_kind: "fs",
          created_at: "t",
          expires_at: "t",
        },
      });
    });

    it("maps approval.resolved onto the resolved-event payload", () => {
      const out = dispatchAgentChatStreamEvent(
        "approval.resolved",
        JSON.stringify({ approval_id: "ap-1", session_id: "s", status: "resolved", path: "operator" }),
      );
      expect(out?.type).toBe("approval.resolved");
    });

    it("returns null for unknown event names (forward-compat)", () => {
      const out = dispatchAgentChatStreamEvent("approval.future_kind", "{}");
      expect(out).toBeNull();
    });
  });

  describe("streamAgentChatSession", () => {
    it("dispatches mixed event types from one stream", async () => {
      const events = [
        "event: approval.requested",
        `data: ${JSON.stringify({ approval_id: "ap-1", session_id: "s", adapter_id: "codex", tool_kind: "fs", created_at: "t", expires_at: "t" })}`,
        "",
        "event: session_update",
        `data: ${JSON.stringify({ object: "agent_chat_session", data: { id: "s" } })}`,
        "",
        "event: approval.resolved",
        `data: ${JSON.stringify({ approval_id: "ap-1", session_id: "s", status: "resolved", path: "operator" })}`,
        "",
      ].join("\n");

      const encoder = new TextEncoder();
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(encoder.encode(events));
          controller.close();
        },
      });
      fetchMock.mockResolvedValue(
        new Response(stream, {
          status: 200,
          headers: { "Content-Type": "text/event-stream" },
        }),
      );

      const seen: string[] = [];
      await streamAgentChatSession("s", (event) => {
        seen.push(event.type);
      });
      expect(seen).toEqual(["approval.requested", "session_update", "approval.resolved"]);
    });

    it("silently drops unknown event types so old clients don't break on new server events", async () => {
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          const encoder = new TextEncoder();
          controller.enqueue(encoder.encode("event: future.kind\ndata: {}\n\n"));
          controller.close();
        },
      });
      fetchMock.mockResolvedValue(
        new Response(stream, {
          status: 200,
          headers: { "Content-Type": "text/event-stream" },
        }),
      );

      const seen: string[] = [];
      await streamAgentChatSession("s", (event) => {
        seen.push(event.type);
      });
      expect(seen).toEqual([]);
    });
  });
});

function jsonResponse(payload: unknown): Response {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

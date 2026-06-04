import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  buildRequestOptions,
  cancelChatApproval,
  chatCompletions,
  createProjectAssignment,
  createProjectWorkRole,
  createProjectWorkItem,
  deleteChatGrant,
  deletePolicyRule,
  deleteProjectAssignment,
  deleteProjectWorkRole,
  deleteProjectWorkItem,
  discoverLocalProviders,
  dispatchChatStreamEvent,
  getChatMessageFileDiff,
  getChatWorkspaceDiff,
  getChatWorkspaceFileDiff,
  getChatWorkspaceFiles,
  getChatApproval,
  getProjectAssignments,
  getProjectCollaborationArtifacts,
  getProjectWorkItem,
  getProjectWorkItems,
  getProjectWorkRoles,
  getUsageEvents,
  getUsageSummary,
  getSession,
  getTrace,
  listChatApprovals,
  listChatGrants,
  listChatMessageFiles,
  openWorkspaceTargetViaAPI,
  probeAgentAdapter,
  refreshAgentAdapterLauncher,
  revertChatMessageFiles,
  revertChatWorkspaceFiles,
  resolveChatApproval,
  setChatSettings,
  setProviderAPIKey,
  setProviderBaseURL,
  startProjectAssignment,
  streamChatSession,
  upsertPolicyRule,
  updateChatSession,
  updateProject,
  updateProjectAssignment,
  updateProjectWorkRole,
  updateProjectWorkItem,
  type ApiError,
} from "./api";

describe("api client", () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    window.sessionStorage.clear();
    window.localStorage.clear();
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

  it("builds usage summary requests with query strings intact", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ object: "usage_summary", data: { key: "global" } }));

    await getUsageSummary("?scope=provider&provider=ollama");

    expect(fetchMock).toHaveBeenCalledWith(
      "/hecate/v1/usage/summary?scope=provider&provider=ollama",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("builds usage event requests with encoded limits", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ object: "usage_events", data: [] }));

    await getUsageEvents(7);

    expect(fetchMock).toHaveBeenCalledWith(
      "/hecate/v1/usage/events?limit=7",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("opens a workspace target through the local gateway", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({
        object: "workspace_open",
        data: { path: "/Users/alice/dev/hecate", target: "zed" },
      }),
    );

    await openWorkspaceTargetViaAPI("/Users/alice/dev/hecate", "zed");

    expect(fetchMock).toHaveBeenCalledWith(
      "/hecate/v1/workspace-open",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ path: "/Users/alice/dev/hecate", target: "zed" }),
      }),
    );
  });

  it("surfaces text error bodies when the local gateway lacks an endpoint", async () => {
    fetchMock.mockResolvedValue(
      new Response("404 page not found\n", {
        status: 404,
        headers: { "Content-Type": "text/plain" },
      }),
    );

    await expect(openWorkspaceTargetViaAPI("/Users/alice/dev/hecate", "terminal")).rejects.toThrow(
      "request failed (404): 404 page not found",
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
      "/hecate/v1/whoami",
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
          choices: [
            { index: 0, finish_reason: "stop", message: { role: "assistant", content: "Hello!" } },
          ],
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
                {
                  name: "request.received",
                  timestamp: "2026-04-21T00:00:00Z",
                  attributes: { model: "gpt-4o-mini" },
                },
                {
                  name: "response.returned",
                  timestamp: "2026-04-21T00:00:01Z",
                  attributes: { provider: "openai" },
                },
              ],
            },
          ],
        },
      }),
    );

    const result = await getTrace("req-123");

    expect(fetchMock).toHaveBeenCalledWith(
      "/hecate/v1/traces?request_id=req-123",
      expect.objectContaining({ method: "GET" }),
    );
    expect(result.data.request_id).toBe("req-123");
    expect(result.data.spans).toHaveLength(1);
    expect(result.data.spans?.[0]?.events).toHaveLength(2);
  });

  it("builds project work coordination requests", async () => {
    fetchMock.mockClear();
    for (let i = 0; i < 5; i += 1) {
      fetchMock.mockResolvedValueOnce(jsonResponse({ object: "ok", data: [] }));
    }

    await getProjectWorkRoles("proj/1");
    await getProjectWorkItems("proj/1");
    await getProjectWorkItem("proj/1", "work/1");
    await getProjectAssignments("proj/1", "work/1");
    await getProjectCollaborationArtifacts("proj/1", "work/1");

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/hecate/v1/projects/proj%2F1/roles",
      expect.objectContaining({ method: "GET" }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/hecate/v1/projects/proj%2F1/work-items",
      expect.objectContaining({ method: "GET" }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      "/hecate/v1/projects/proj%2F1/work-items/work%2F1",
      expect.objectContaining({ method: "GET" }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      4,
      "/hecate/v1/projects/proj%2F1/work-items/work%2F1/assignments",
      expect.objectContaining({ method: "GET" }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      5,
      "/hecate/v1/projects/proj%2F1/work-items/work%2F1/artifacts",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("starts native project assignments with the hecate driver kind", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ object: "project_assignment", data: { id: "asgn_1" } }),
    );

    await startProjectAssignment("proj_1", "work_1", "asgn_1");

    expect(fetchMock).toHaveBeenCalledWith(
      "/hecate/v1/projects/proj_1/work-items/work_1/assignments/asgn_1/start",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ driver_kind: "hecate_task" }),
      }),
    );
  });

  it("creates project work items and assignments", async () => {
    fetchMock.mockClear();
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ object: "project_work_item", data: { id: "work_1" } }))
      .mockResolvedValueOnce(
        jsonResponse({ object: "project_assignment", data: { id: "asgn_1" } }),
      );

    await createProjectWorkItem("proj_1", {
      title: "Project cockpit",
      brief: "Create records from the UI.",
      status: "ready",
      priority: "high",
      owner_role_id: "software_developer",
    });
    await createProjectAssignment("proj_1", "work_1", {
      role_id: "software_developer",
      driver_kind: "hecate_task",
    });

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/hecate/v1/projects/proj_1/work-items",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          title: "Project cockpit",
          brief: "Create records from the UI.",
          status: "ready",
          priority: "high",
          owner_role_id: "software_developer",
        }),
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/hecate/v1/projects/proj_1/work-items/work_1/assignments",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          role_id: "software_developer",
          driver_kind: "hecate_task",
        }),
      }),
    );
  });

  it("creates, updates, and deletes project roles with execution defaults", async () => {
    fetchMock.mockClear();
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ object: "project_role", data: { id: "role/1" } }))
      .mockResolvedValueOnce(jsonResponse({ object: "project_role", data: { id: "role/1" } }))
      .mockResolvedValueOnce(jsonResponse(null));

    await createProjectWorkRole("proj/1", {
      name: "Frontend implementer",
      description: "Builds UI",
      instructions: "Use existing UI primitives.",
      default_driver_kind: "hecate_task",
      default_provider: "ollama",
      default_model: "ministral-3:latest",
      default_agent_profile: "implementation",
    });
    await updateProjectWorkRole("proj/1", "role/1", {
      default_driver_kind: "external_agent",
      default_provider: "anthropic",
      default_model: "claude-sonnet-4",
      default_agent_profile: "safe_external_review",
    });
    await deleteProjectWorkRole("proj/1", "role/1");

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/hecate/v1/projects/proj%2F1/roles",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          name: "Frontend implementer",
          description: "Builds UI",
          instructions: "Use existing UI primitives.",
          default_driver_kind: "hecate_task",
          default_provider: "ollama",
          default_model: "ministral-3:latest",
          default_agent_profile: "implementation",
        }),
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/hecate/v1/projects/proj%2F1/roles/role%2F1",
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({
          default_driver_kind: "external_agent",
          default_provider: "anthropic",
          default_model: "claude-sonnet-4",
          default_agent_profile: "safe_external_review",
        }),
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      "/hecate/v1/projects/proj%2F1/roles/role%2F1",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("updates and deletes project work items and assignments", async () => {
    fetchMock.mockClear();
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ object: "project_work_item", data: { id: "work/1" } }))
      .mockResolvedValueOnce(jsonResponse(null))
      .mockResolvedValueOnce(jsonResponse({ object: "project_assignment", data: { id: "asgn/1" } }))
      .mockResolvedValueOnce(jsonResponse(null));

    await updateProjectWorkItem("proj/1", "work/1", {
      title: "Edited work",
      status: "ready",
      priority: "urgent",
      owner_role_id: "frontend_engineer",
    });
    await deleteProjectWorkItem("proj/1", "work/1");
    await updateProjectAssignment("proj/1", "work/1", "asgn/1", {
      role_id: "frontend_engineer",
      driver_kind: "external_agent",
      status: "running",
    });
    await deleteProjectAssignment("proj/1", "work/1", "asgn/1");

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/hecate/v1/projects/proj%2F1/work-items/work%2F1",
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({
          title: "Edited work",
          status: "ready",
          priority: "urgent",
          owner_role_id: "frontend_engineer",
        }),
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/hecate/v1/projects/proj%2F1/work-items/work%2F1",
      expect.objectContaining({ method: "DELETE" }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      "/hecate/v1/projects/proj%2F1/work-items/work%2F1/assignments/asgn%2F1",
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({
          role_id: "frontend_engineer",
          driver_kind: "external_agent",
          status: "running",
        }),
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      4,
      "/hecate/v1/projects/proj%2F1/work-items/work%2F1/assignments/asgn%2F1",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("updates project execution defaults", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ object: "project", data: { id: "proj_1" } }));

    await updateProject("proj_1", {
      default_provider: "ollama",
      default_model: "ministral-3:latest",
      default_workspace_mode: "in_place",
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/hecate/v1/projects/proj_1",
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({
          default_provider: "ollama",
          default_model: "ministral-3:latest",
          default_workspace_mode: "in_place",
        }),
      }),
    );
  });

  describe("provider REST API", () => {
    it("PATCH /providers/{id} updates the base_url", async () => {
      fetchMock.mockResolvedValue(jsonResponse({}));

      await setProviderBaseURL("ollama", "http://192.168.1.10:11434/v1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/settings/providers/ollama",
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
        "/hecate/v1/settings/providers/my%20provider",
        expect.anything(),
      );
    });

    it("PUT /providers/{id}/api-key to set credentials", async () => {
      fetchMock.mockResolvedValue(jsonResponse({}));

      await setProviderAPIKey("anthropic", "sk-new-key");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/settings/providers/anthropic/api-key",
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
        "/hecate/v1/settings/providers/anthropic/api-key",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({ key: "" }),
        }),
      );
    });

    it("GET /hecate/v1/settings/providers/local-discovery discovers local presets", async () => {
      fetchMock.mockResolvedValue(jsonResponse({ object: "local_provider_discovery", data: [] }));

      await discoverLocalProviders();

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/settings/providers/local-discovery",
        expect.objectContaining({ method: "GET" }),
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
        "/hecate/v1/settings/policy-rules",
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

    it("DELETE /policy-rules/{id} sends the id in the path", async () => {
      fetchMock.mockResolvedValue(jsonResponse({}));

      await deletePolicyRule("deny-cloud");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/settings/policy-rules/deny-cloud",
        expect.objectContaining({
          method: "DELETE",
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

    it("does not send Authorization by default", () => {
      const options = buildRequestOptions({ method: "POST", body: { x: 1 } });
      const headers = new Headers(options.headers);
      expect(headers.get("Authorization")).toBeNull();
    });

    it("sends the optional runtime token from session storage", () => {
      window.sessionStorage.setItem("hecate.runtimeToken", "local-runtime-token-123456");

      const options = buildRequestOptions({ method: "POST", body: { x: 1 } }, "/hecate/v1/tasks");
      const headers = new Headers(options.headers);

      expect(headers.get("X-Hecate-Runtime-Token")).toBe("local-runtime-token-123456");
    });

    it("falls back to the optional runtime token from local storage", () => {
      window.localStorage.setItem("hecate.runtimeToken", "local-runtime-token-abcdef");

      const options = buildRequestOptions({ method: "GET" }, "/hecate/v1/whoami");
      const headers = new Headers(options.headers);

      expect(headers.get("X-Hecate-Runtime-Token")).toBe("local-runtime-token-abcdef");
    });

    it("does not send the optional runtime token to provider-compatible paths", () => {
      window.sessionStorage.setItem("hecate.runtimeToken", "local-runtime-token-123456");

      const options = buildRequestOptions(
        { method: "POST", body: { messages: [] } },
        "/v1/chat/completions",
      );
      const headers = new Headers(options.headers);

      expect(headers.get("X-Hecate-Runtime-Token")).toBeNull();
    });

    it("sends the optional inference token to local provider-compatible paths", () => {
      window.sessionStorage.setItem("hecate.inferenceToken", "local-inference-token-123456");

      const options = buildRequestOptions(
        { method: "POST", body: { messages: [] } },
        "/v1/chat/completions",
      );
      const headers = new Headers(options.headers);

      expect(headers.get("Authorization")).toBe("Bearer local-inference-token-123456");
    });

    it("sends the optional inference token to local models lookup", () => {
      window.localStorage.setItem("hecate.inferenceToken", "local-inference-token-abcdef");

      const options = buildRequestOptions({ method: "GET" }, "/v1/models?refresh=1");
      const headers = new Headers(options.headers);

      expect(headers.get("Authorization")).toBe("Bearer local-inference-token-abcdef");
    });

    it("does not send the optional inference token to hecate native paths", () => {
      window.sessionStorage.setItem("hecate.inferenceToken", "local-inference-token-123456");

      const options = buildRequestOptions({ method: "GET" }, "/hecate/v1/whoami");
      const headers = new Headers(options.headers);

      expect(headers.get("Authorization")).toBeNull();
    });

    it("does not send the optional inference token to external origins", () => {
      window.sessionStorage.setItem("hecate.inferenceToken", "local-inference-token-123456");

      const options = buildRequestOptions(
        { method: "POST", body: { messages: [] } },
        "https://api.openai.com/v1/chat/completions",
      );
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
      await expect(getUsageSummary("?scope=global")).rejects.toMatchObject({
        message: "slow down",
        status: 429,
        code: "rate_limit_exceeded",
      } satisfies Partial<ApiError>);
    });

    it("preserves stable operator-facing error metadata from the gateway envelope", async () => {
      fetchMock.mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              type: "route_impossible",
              message: "route request: no provider available",
              user_message: "No configured provider can serve this request.",
              operator_action: "Open Connections to inspect readiness checks.",
              request_id: "req-body",
              trace_id: "trace-body",
            },
          }),
          {
            status: 503,
            headers: {
              "Content-Type": "application/json",
              "X-Request-Id": "req-header",
              "X-Trace-Id": "trace-header",
            },
          },
        ),
      );

      await expect(getUsageSummary("?scope=global")).rejects.toMatchObject({
        message: "No configured provider can serve this request.",
        status: 503,
        code: "route_impossible",
        userMessage: "No configured provider can serve this request.",
        operatorAction: "Open Connections to inspect readiness checks.",
        requestId: "req-body",
        traceId: "trace-body",
      } satisfies Partial<ApiError>);
    });

    it("falls back to request and trace headers when the error body omits correlation IDs", async () => {
      fetchMock.mockResolvedValue(
        new Response(
          JSON.stringify({
            error: { type: "provider_unavailable", message: "provider unavailable" },
          }),
          {
            status: 502,
            headers: {
              "Content-Type": "application/json",
              "X-Request-Id": "req-header",
              "X-Trace-Id": "trace-header",
            },
          },
        ),
      );

      await expect(getUsageSummary("?scope=global")).rejects.toMatchObject({
        requestId: "req-header",
        traceId: "trace-header",
      } satisfies Partial<ApiError>);
    });

    it("falls back to the static label when the error body is not valid JSON", async () => {
      fetchMock.mockResolvedValue(
        new Response("<html>500</html>", {
          status: 500,
          headers: { "Content-Type": "text/html" },
        }),
      );
      await expect(getUsageSummary("?scope=global")).rejects.toThrow(/request failed/);
    });

    it("returns undefined for a 204 No Content response", async () => {
      fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
      await expect(setProviderAPIKey("anthropic", "")).resolves.not.toThrow();
    });

    it("rewrites 'Failed to fetch' network errors into actionable gateway URLs", async () => {
      fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
      await expect(getUsageSummary("?scope=global")).rejects.toThrow(
        /Check that the gateway is running/,
      );
    });

    it("rewrites 'NetworkError' substring matches the same way", async () => {
      fetchMock.mockRejectedValue(new TypeError("NetworkError when attempting to fetch resource."));
      await expect(getUsageSummary("?scope=global")).rejects.toThrow(
        /Check that the gateway is running/,
      );
    });

    it("preserves non-network error messages with the request URL prepended", async () => {
      fetchMock.mockRejectedValue(new Error("AbortError: aborted"));
      await expect(getUsageSummary("?scope=global")).rejects.toThrow(
        /\/hecate\/v1\/usage\/summary.*AbortError: aborted/,
      );
    });
  });

  // ─── Agent-chat approvals & grants ─────────────────────────────────────────

  describe("chat approvals", () => {
    it("PATCH /chat/sessions/{id} sends a renamed title", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({ object: "chat_session", data: { id: "s 1", title: "Renamed chat" } }),
      );

      await updateChatSession("s 1", "Renamed chat");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/s%201",
        expect.objectContaining({
          method: "PATCH",
          body: JSON.stringify({ title: "Renamed chat" }),
        }),
      );
    });

    it("PATCH /chat/sessions/{id}/settings sends per-chat settings", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({ object: "chat_session", data: { id: "s 1", rtk_enabled: true } }),
      );

      await setChatSettings("s 1", { rtk_enabled: true });

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/s%201/settings",
        expect.objectContaining({
          method: "PATCH",
          body: JSON.stringify({ rtk_enabled: true }),
        }),
      );
    });

    it("lists approvals scoped to status=pending", async () => {
      fetchMock.mockResolvedValue(jsonResponse({ object: "list", data: [] }));

      await listChatApprovals("sess-1", "pending");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/sess-1/approvals?status=pending",
        expect.objectContaining({ method: "GET" }),
      );
    });

    it("omits the status query string when no filter is passed", async () => {
      fetchMock.mockResolvedValue(jsonResponse({ object: "list", data: [] }));

      await listChatApprovals("sess-1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/sess-1/approvals",
        expect.objectContaining({ method: "GET" }),
      );
    });

    it("URL-encodes ids on get", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({ object: "chat_approval", data: { id: "ap/1", session_id: "s 1" } }),
      );

      await getChatApproval("s 1", "ap/1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/s%201/approvals/ap%2F1",
        expect.objectContaining({ method: "GET" }),
      );
    });

    it("posts the resolve decision body", async () => {
      fetchMock.mockResolvedValue(jsonResponse({ object: "chat_approval", data: { id: "ap-1" } }));

      await resolveChatApproval("sess-1", "ap-1", {
        decision: "approve",
        scope: "once",
        selected_option: "opt-1",
        note: "looks fine",
      });

      const [url, options] = fetchMock.mock.lastCall ?? [];
      expect(url).toBe("/hecate/v1/chat/sessions/sess-1/approvals/ap-1/resolve");
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
      fetchMock.mockResolvedValue(jsonResponse({ object: "chat_approval", data: { id: "ap-1" } }));

      await cancelChatApproval("sess-1", "ap-1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/sess-1/approvals/ap-1/cancel",
        expect.objectContaining({ method: "POST" }),
      );
    });

    it("builds the grants list query string from non-empty filter fields only", async () => {
      fetchMock.mockResolvedValue(jsonResponse({ object: "list", data: [] }));

      await listChatGrants({ adapter_id: "codex", scope: "session" });

      const [url] = fetchMock.mock.lastCall ?? [];
      // URLSearchParams ordering is insertion-order; both forms are
      // acceptable so long as the query string contains both pairs.
      expect(url).toContain("/hecate/v1/chat/grants?");
      expect(url).toContain("adapter_id=codex");
      expect(url).toContain("scope=session");
    });

    it("sends DELETE on grant revocation", async () => {
      fetchMock.mockResolvedValue(new Response(null, { status: 204 }));

      await deleteChatGrant("grant-1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/grants/grant-1",
        expect.objectContaining({ method: "DELETE" }),
      );
    });
  });

  // ─── Agent adapter health probe ────────────────────────────────────────────

  describe("probeAgentAdapter", () => {
    it("URL-encodes the adapter id and returns the typed payload", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          object: "agent_adapter_probe",
          data: {
            adapter: {
              id: "codex",
              name: "Codex",
              kind: "acp",
              command: "codex-acp",
              available: true,
              status: "available",
            },
            health: {
              adapter_id: "codex",
              status: "ready",
              stage: "ready",
              path: "/usr/local/bin/codex-acp",
              duration_ms: 412,
            },
          },
        }),
      );

      const result = await probeAgentAdapter("codex");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/agent-adapters/codex/probe",
        expect.objectContaining({ method: "POST" }),
      );
      expect(result.data.health.status).toBe("ready");
      expect(result.data.health.duration_ms).toBe(412);
    });

    it("escapes ids with special characters", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          object: "agent_adapter_probe",
          data: {
            adapter: {
              id: "weird id",
              name: "Weird",
              kind: "acp",
              command: "weird",
              available: false,
              status: "missing",
            },
            health: { adapter_id: "weird id", status: "error", stage: "lookup", duration_ms: 0 },
          },
        }),
      );

      await probeAgentAdapter("weird id");

      const [url] = fetchMock.mock.lastCall ?? [];
      expect(url).toBe("/hecate/v1/agent-adapters/weird%20id/probe");
    });
  });

  describe("refreshAgentAdapterLauncher", () => {
    it("POSTs to the managed-launcher refresh endpoint and returns the updated adapter list", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          object: "agent_adapters",
          data: [
            {
              id: "codex",
              name: "Codex",
              kind: "acp",
              command: "codex-acp",
              managed: true,
              available: true,
              status: "available",
            },
          ],
        }),
      );

      const result = await refreshAgentAdapterLauncher("codex");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/agent-adapters/codex/refresh-launcher",
        expect.objectContaining({ method: "POST" }),
      );
      expect(result.data[0]?.id).toBe("codex");
    });

    it("URL-encodes adapter ids", async () => {
      fetchMock.mockResolvedValue(jsonResponse({ object: "agent_adapters", data: [] }));

      await refreshAgentAdapterLauncher("weird id");

      const [url] = fetchMock.mock.lastCall ?? [];
      expect(url).toBe("/hecate/v1/agent-adapters/weird%20id/refresh-launcher");
    });
  });

  describe("chat changed-file endpoints", () => {
    it("lists changed files for an agent message", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          object: "chat_changed_files",
          data: [{ path: "src/app.go", additions: 2, deletions: 1, status: "modified" }],
        }),
      );

      const result = await listChatMessageFiles("s 1", "m/1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/s%201/messages/m%2F1/files",
        expect.anything(),
      );
      expect(result.data[0]?.path).toBe("src/app.go");
    });

    it("fetches a single changed-file diff", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          object: "chat_changed_file_diff",
          data: {
            path: "src/app.go",
            additions: 2,
            deletions: 1,
            status: "modified",
            diff: "diff --git a/src/app.go b/src/app.go",
          },
        }),
      );

      const result = await getChatMessageFileDiff("s1", "m1", "src/app.go");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/s1/messages/m1/files/src%2Fapp.go",
        expect.anything(),
      );
      expect(result.data.diff).toContain("src/app.go");
    });

    it("fetches the current workspace diff for a chat session", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          object: "chat_workspace_diff",
          data: {
            workspace: "/tmp/hecate",
            diff_stat: "README.md | 1 +",
            diff: "diff --git a/README.md b/README.md",
            has_changes: true,
            files: [{ path: "README.md", additions: 1, deletions: 0, status: "modified" }],
          },
        }),
      );

      const result = await getChatWorkspaceDiff("s 1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/s%201/workspace-diff",
        expect.anything(),
      );
      expect(result.data.files[0]?.path).toBe("README.md");
    });

    it("fetches the current workspace file tree for a chat session", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          object: "chat_workspace_files",
          data: {
            workspace: "/tmp/hecate",
            files: [
              { path: "src", name: "src", kind: "directory" },
              {
                path: "src/app.go",
                name: "app.go",
                kind: "file",
                status: "modified",
                size_bytes: 42,
              },
            ],
          },
        }),
      );

      const result = await getChatWorkspaceFiles("s 1");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/s%201/workspace-files",
        expect.anything(),
      );
      expect(result.data.files[1]?.path).toBe("src/app.go");
    });

    it("fetches a single current workspace file diff", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          object: "chat_workspace_file_diff",
          data: {
            path: "src/app.go",
            additions: 2,
            deletions: 1,
            status: "modified",
            diff: "diff --git a/src/app.go b/src/app.go",
          },
        }),
      );

      const result = await getChatWorkspaceFileDiff("s1", "src/app.go");

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/s1/workspace-diff/files/src%2Fapp.go",
        expect.anything(),
      );
      expect(result.data.diff).toContain("src/app.go");
    });

    it("reverts selected current workspace files", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          object: "chat_workspace_diff",
          data: {
            workspace: "/tmp/hecate",
            diff_stat: "",
            diff: "",
            has_changes: false,
            files: [],
          },
        }),
      );

      const result = await revertChatWorkspaceFiles("s1", ["src/app.go"]);

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/s1/workspace-diff/revert",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ paths: ["src/app.go"] }),
        }),
      );
      expect(result.data.has_changes).toBe(false);
    });

    it("reverts selected changed files", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          object: "chat_revert",
          data: {
            reverted: true,
            paths: ["src/app.go"],
            diff_stat: "README.md | 1 +",
            files: [{ path: "README.md", additions: 1, deletions: 0, status: "modified" }],
          },
        }),
      );

      const result = await revertChatMessageFiles("s1", "m1", ["src/app.go"]);

      expect(fetchMock).toHaveBeenCalledWith(
        "/hecate/v1/chat/sessions/s1/messages/m1/revert",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ paths: ["src/app.go"] }),
        }),
      );
      expect(result.data.reverted).toBe(true);
      expect(result.data.files[0]?.path).toBe("README.md");
    });
  });

  // ─── Agent-chat SSE dispatch ───────────────────────────────────────────────

  describe("dispatchChatStreamEvent", () => {
    it("maps named session_update events onto the typed union", () => {
      const out = dispatchChatStreamEvent(
        "session_update",
        JSON.stringify({ object: "chat_session", data: { id: "s" } }),
      );
      expect(out).toEqual({
        type: "session_update",
        payload: { object: "chat_session", data: { id: "s" } },
      });
    });

    it("treats the default `message` event as session_update for backward compat", () => {
      const out = dispatchChatStreamEvent(
        "message",
        JSON.stringify({ object: "chat_session", data: { id: "s" } }),
      );
      expect(out?.type).toBe("session_update");
    });

    it("maps existing backend snapshot/done events onto session_update", () => {
      for (const eventName of ["snapshot", "done"]) {
        const out = dispatchChatStreamEvent(
          eventName,
          JSON.stringify({ object: "chat_session", data: { id: "s" } }),
        );
        expect(out).toEqual({
          type: "session_update",
          payload: { object: "chat_session", data: { id: "s" } },
        });
      }
    });

    it("maps approval.requested onto the requested-event payload", () => {
      const out = dispatchChatStreamEvent(
        "approval.requested",
        JSON.stringify({
          approval_id: "ap-1",
          session_id: "s",
          adapter_id: "codex",
          tool_kind: "fs",
          created_at: "t",
          expires_at: "t",
        }),
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
      const out = dispatchChatStreamEvent(
        "approval.resolved",
        JSON.stringify({
          approval_id: "ap-1",
          session_id: "s",
          status: "resolved",
          path: "operator",
        }),
      );
      expect(out?.type).toBe("approval.resolved");
    });

    it("returns null for unknown event names (forward-compat)", () => {
      const out = dispatchChatStreamEvent("approval.future_kind", "{}");
      expect(out).toBeNull();
    });
  });

  describe("streamChatSession", () => {
    it("dispatches mixed event types from one stream", async () => {
      const events = [
        "event: approval.requested",
        `data: ${JSON.stringify({ approval_id: "ap-1", session_id: "s", adapter_id: "codex", tool_kind: "fs", created_at: "t", expires_at: "t" })}`,
        "",
        "event: session_update",
        `data: ${JSON.stringify({ object: "chat_session", data: { id: "s" } })}`,
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
      await streamChatSession("s", (event) => {
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
      await streamChatSession("s", (event) => {
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

import { act, renderHook, waitFor, type RenderHookOptions } from "@testing-library/react";
import { type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApprovalsProvider } from "../app/state/approvals";
import { ChatProvider } from "../app/state/chat";
import { ProvidersAndModelsProvider } from "../app/state/providersAndModels";
import { ProjectsProvider } from "../app/state/projects";
import { RetentionProvider } from "../app/state/retention";
import { RuntimeProvider } from "../app/state/runtime";
import { SettingsProvider } from "../app/state/settings";
import { UsageProvider } from "../app/state/usage";
import { useRuntimeConsole } from "./runtime-console-test-composer";

// This suite is the canonical regression net for the composed
// slice + coordinator viewmodel — historically owned by
// useRuntimeConsole.test.tsx, now scoped to the test-only composer
// after the production facade was retired. Each renderHook call
// needs the full provider chain; the wrapper composes them so the
// test bodies don't have to thread it through every call.
function SliceProviders({ children }: { children: ReactNode }) {
  return (
    <RuntimeProvider>
      <UsageProvider>
        <ProvidersAndModelsProvider>
          <ProjectsProvider>
            <ChatProvider>
              <RetentionProvider>
                <ApprovalsProvider>
                  <SettingsProvider>{children}</SettingsProvider>
                </ApprovalsProvider>
              </RetentionProvider>
            </ChatProvider>
          </ProjectsProvider>
        </ProvidersAndModelsProvider>
      </UsageProvider>
    </RuntimeProvider>
  );
}

function renderRuntimeConsoleHook(options?: Omit<RenderHookOptions<unknown>, "wrapper">) {
  return renderHook(() => useRuntimeConsole(), { ...options, wrapper: SliceProviders });
}

// Single-user mode: every endpoint is unauthenticated and the gateway
// surfaces a stub `Anonymous` session for all callers. The tests below
// stub /healthz + /hecate/v1/whoami + the dashboard fan-out and exercise the
// hook's user-visible behavior on top of that.

function defaultBackendMock(
  routes: Record<string, (init?: RequestInit) => Response | Promise<Response>> = {},
) {
  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = String(input);
    // Per-test overrides take precedence over the defaults below so a
    // test can stub `/hecate/v1/chat/sessions?limit=20` to return seeded data.
    const handler = routes[url];
    if (handler) return handler(init);
    if (url === "/healthz") return jsonResponse({ status: "ok", time: "2026-04-20T00:00:00Z" });
    if (url === "/hecate/v1/whoami") {
      return jsonResponse({
        object: "session",
        data: { authenticated: true, invalid_token: false, role: "admin", source: "anonymous" },
      });
    }
    if (url === "/v1/models") return jsonResponse({ object: "list", data: [] });
    if (url === "/hecate/v1/providers/presets")
      return jsonResponse({ object: "provider_presets", data: [] });
    if (url === "/hecate/v1/providers/status")
      return jsonResponse({ object: "provider_status", data: [] });
    if (url === "/hecate/v1/settings")
      return jsonResponse({
        object: "settings",
        data: { backend: "memory", providers: [], policy_rules: [], events: [] },
      });
    if (url.startsWith("/hecate/v1/usage/summary"))
      return jsonResponse({
        object: "usage_summary",
        data: {
          key: "global",
          scope: "global",
          backend: "memory",
          used_micros_usd: 0,
          used_usd: "$0.000000",
        },
      });
    if (url.startsWith("/hecate/v1/usage/events"))
      return jsonResponse({ object: "usage_events", data: [] });
    if (url === "/hecate/v1/chat/sessions")
      return jsonResponse({ object: "chat_sessions", data: [] });
    if (url === "/hecate/v1/projects") return jsonResponse({ object: "projects", data: [] });
    if (url.startsWith("/hecate/v1/chat/sessions/") && url.endsWith("/approvals?status=pending"))
      return jsonResponse({ object: "list", data: [] });
    if (url.startsWith("/hecate/v1/chat/sessions"))
      return jsonResponse({ object: "chat_sessions", data: [], has_more: false });
    if (url.startsWith("/hecate/v1/system/retention/runs"))
      return jsonResponse({ object: "retention_runs", data: [] });
    if (url.startsWith("/hecate/v1/observability/requests"))
      return jsonResponse({ object: "requests", data: [] });
    if (url.startsWith("/hecate/v1/system/stats"))
      return jsonResponse({
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
    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    expect(result.current.state.message).toBe("");
  });

  it("treats a cancelled workspace picker as a quiet no-op", async () => {
    window.localStorage.setItem("hecate.agentWorkspace", "/workspace/current");
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/workspace-dialog": () =>
          jsonResponse({
            object: "workspace_dialog",
            data: { path: "", branch: "" },
          }),
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    let selected = false;
    await act(async () => {
      selected = await result.current.actions.chooseAgentWorkspace();
    });

    expect(selected).toBe(true);
    expect(result.current.state.agentWorkspace).toBe("/workspace/current");
    expect(result.current.state.chatError).toBe("");
    expect(result.current.state.notice).toBeNull();
  });

  it("defaults to Agent chat when no chat target preference exists", async () => {
    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    expect(result.current.state.chatTarget).toBe("agent");
  });

  it("preserves the saved chat target preference", async () => {
    window.localStorage.setItem("hecate.chatTarget", "model");
    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    expect(result.current.state.chatTarget).toBe("model");
  });

  it("drops stale persisted provider and model selections when only a catalog preset remains", async () => {
    window.localStorage.setItem("hecate.providerFilter", "ollama");
    window.localStorage.setItem("hecate.model", "ministral-3:latest");
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/providers/presets": () =>
          jsonResponse({
            object: "provider_presets",
            data: [
              {
                id: "ollama",
                name: "Ollama",
                kind: "local",
                protocol: "openai",
                base_url: "http://127.0.0.1:11434/v1",
                default_model: "ministral-3:latest",
              },
            ],
          }),
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.providerFilter).toBe("auto"));
    expect(result.current.state.model).toBe("");
  });

  it("repairs stale persisted model selection with the first live provider model", async () => {
    window.localStorage.setItem("hecate.providerFilter", "ollama");
    window.localStorage.setItem("hecate.model", "ministral-3:latest");
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/settings": () =>
          jsonResponse({
            object: "settings",
            data: {
              backend: "memory",
              providers: [
                {
                  id: "ollama",
                  name: "Ollama",
                  kind: "local",
                  protocol: "openai",
                  base_url: "http://127.0.0.1:11434/v1",
                  credential_configured: true,
                },
              ],
              policy_rules: [],
              events: [],
            },
          }),
        "/hecate/v1/providers/status": () =>
          jsonResponse({
            object: "provider_status",
            data: [
              {
                name: "ollama",
                kind: "local",
                healthy: true,
                status: "ok",
                models: ["llama3.1:8b"],
              },
            ],
          }),
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await waitFor(() => expect(result.current.state.providerFilter).toBe("ollama"));
    expect(result.current.state.model).toBe("llama3.1:8b");
  });

  it("keeps expected Hecate chat setup route failures out of the global notice", async () => {
    window.localStorage.setItem("hecate.chatTarget", "model");
    window.localStorage.setItem("hecate.providerFilter", "ollama");
    window.localStorage.setItem("hecate.model", "ministral-3:latest");
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/v1/models": () =>
          jsonResponse({
            object: "list",
            data: [
              {
                id: "ministral-3:latest",
                owned_by: "ollama",
                metadata: { provider: "ollama", provider_kind: "local" },
              },
            ],
          }),
        "/hecate/v1/providers/status": () =>
          jsonResponse({
            object: "provider_status",
            data: [
              {
                name: "ollama",
                kind: "local",
                healthy: true,
                status: "ok",
                models: ["ministral-3:latest"],
              },
            ],
          }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            return jsonResponse(
              {
                error: {
                  type: "route.no_routable_provider",
                  message: 'No routable provider reports model "ministral-3:latest".',
                },
              },
              400,
            );
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("ministral-3:latest"));

    await act(async () => {
      await result.current.actions.createChatSession({ agentID: "hecate" });
    });

    expect(result.current.state.chatError).toBe(
      'No routable provider reports model "ministral-3:latest".',
    );
    expect(result.current.state.notice).toBeNull();
  });

  it("keeps a newly created tools-off Hecate chat in direct model mode", async () => {
    window.localStorage.setItem("hecate.chatTarget", "model");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_direct",
                title: "Hecate Chat",
                agent_id: "hecate",
                provider: "",
                model: "gpt-4o-mini",
                status: "idle",
                messages: [],
              },
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      model: "gpt-4o-mini",
    });
    expect(createBody).not.toHaveProperty("workspace");
    expect(result.current.state.activeChatSessionID).toBe("chat_direct");
    expect(result.current.state.chatTarget).toBe("model");
  });

  it("creates an external-agent session from the selected agent and workspace", async () => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate-project");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/agent-adapters": () =>
          jsonResponse({
            object: "agent_adapters",
            data: [{ id: "claude_code", name: "Claude Code", available: true }],
          }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_1",
                title: "Claude Code chat",
                agent_id: "claude_code",
                driver_kind: "acp",
                native_session_id: "native_1",
                workspace: "/tmp/hecate-project",
                status: "idle",
                config_options: [
                  { id: "model", name: "Model", type: "select", current_value: "sonnet" },
                ],
                messages: [],
              },
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createBody).toMatchObject({
      agent_id: "claude_code",
      workspace: "/tmp/hecate-project",
    });
    expect(result.current.state.activeChatSessionID).toBe("chat_1");
    expect(result.current.state.activeChatSession?.config_options?.[0]?.current_value).toBe(
      "sonnet",
    );
  });

  it("uses the new-chat agent selection instead of the active external session", async () => {
    window.localStorage.setItem("hecate.chatSessionID", "a1");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_hecate",
                title: "Hecate Chat",
                agent_id: "hecate",
                provider: "",
                model: "gpt-4o-mini",
                workspace: "/tmp/hecate",
                status: "idle",
                messages: [],
              },
            });
          }
          return jsonResponse({
            object: "chat_sessions",
            data: [
              {
                id: "a1",
                title: "Codex chat",
                agent_id: "codex",
                workspace: "/tmp/hecate",
                status: "idle",
                message_count: 0,
              },
            ],
          });
        },
        "/hecate/v1/chat/sessions/a1": () =>
          jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Codex chat",
              agent_id: "codex",
              workspace: "/tmp/hecate",
              status: "idle",
              messages: [],
            },
          }),
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.activeChatSessionID).toBe("a1"));
    expect(result.current.state.newChatAgentID).toBe("hecate");

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      model: "gpt-4o-mini",
      workspace: "/tmp/hecate",
    });
    expect(createBody).not.toHaveProperty("adapter_id");
    expect(result.current.state.activeChatSessionID).toBe("chat_hecate");
    expect(result.current.state.activeChatSession?.agent_id).toBe("hecate");
  });

  it("creates a Hecate chat session from the default model when no model is preselected", async () => {
    window.localStorage.setItem("hecate.chatTarget", "model");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/v1/models": () =>
          jsonResponse({
            object: "list",
            data: [
              {
                id: "llama3.1:8b",
                owned_by: "ollama",
                metadata: { provider: "ollama", provider_kind: "local", default: true },
              },
            ],
          }),
        "/hecate/v1/providers/status": () =>
          jsonResponse({
            object: "provider_status",
            data: [
              {
                name: "ollama",
                kind: "local",
                default_model: "llama3.1:8b",
                models: ["llama3.1:8b"],
                healthy: true,
              },
            ],
          }),
        "/hecate/v1/settings": () =>
          jsonResponse({
            object: "settings",
            data: {
              providers: [
                {
                  id: "ollama",
                  name: "Ollama",
                  preset_id: "ollama",
                  kind: "local",
                  protocol: "openai",
                  base_url: "http://127.0.0.1:11434/v1",
                  enabled: true,
                  credential_configured: false,
                },
              ],
              policy_rules: [],
              events: [],
            },
          }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_ollama",
                title: "Hecate Chat",
                agent_id: "hecate",
                provider: "ollama",
                model: "llama3.1:8b",
                workspace: "/tmp/hecate",
                status: "idle",
                messages: [],
              },
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("llama3.1:8b"));

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      provider: "ollama",
      model: "llama3.1:8b",
    });
    expect(createBody).not.toHaveProperty("workspace");
    expect(result.current.state.activeChatSessionID).toBe("chat_ollama");
    expect(result.current.state.chatError).toBe("");
  });

  it("drops stale persisted provider/model selections when only a catalog preset remains", async () => {
    window.localStorage.setItem("hecate.providerFilter", "ollama");
    window.localStorage.setItem("hecate.model", "ministral-3:latest");
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/providers/presets": () =>
          jsonResponse({
            object: "provider_presets",
            data: [
              {
                id: "ollama",
                name: "Ollama",
                kind: "local",
                protocol: "openai",
                base_url: "http://127.0.0.1:11434/v1",
                default_model: "ministral-3:latest",
              },
            ],
          }),
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.providerFilter).toBe("auto"));

    expect(result.current.state.model).toBe("");
  });

  it("creates new chat sessions in the active project", async () => {
    window.localStorage.setItem("hecate.chatTarget", "model");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    window.localStorage.setItem("hecate.project", "proj_1");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/projects": () =>
          jsonResponse({
            object: "projects",
            data: [
              {
                id: "proj_1",
                name: "Hecate",
                roots: [],
                created_at: "2026-05-21T10:00:00Z",
                updated_at: "2026-05-21T10:00:00Z",
              },
            ],
          }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_project",
                title: "Hecate Chat",
                project_id: "proj_1",
                agent_id: "hecate",
                provider: "",
                model: "gpt-4o-mini",
                status: "idle",
                messages: [],
              },
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      model: "gpt-4o-mini",
      project_id: "proj_1",
    });
    expect(result.current.state.activeChatSession?.project_id).toBe("proj_1");
  });

  it("honors an explicit project scope when creating a Hecate chat", async () => {
    window.localStorage.setItem("hecate.chatTarget", "model");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_project",
                title: "Hecate Chat",
                project_id: "proj_1",
                agent_id: "hecate",
                provider: "",
                model: "gpt-4o-mini",
                status: "idle",
                messages: [],
              },
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession({ agentID: "hecate", projectID: "proj_1" });
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      model: "gpt-4o-mini",
      workspace: "/tmp/hecate",
      project_id: "proj_1",
    });
    expect(result.current.state.activeChatSession?.project_id).toBe("proj_1");
  });

  it("honors an explicit project scope when creating an external-agent chat", async () => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate-project");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/agent-adapters": () =>
          jsonResponse({
            object: "agent_adapters",
            data: [{ id: "claude_code", name: "Claude Code", available: true }],
          }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_project",
                title: "Claude Code chat",
                project_id: "proj_1",
                agent_id: "claude_code",
                driver_kind: "acp",
                workspace: "/tmp/hecate-project",
                status: "idle",
                messages: [],
              },
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession({
        agentID: "claude_code",
        projectID: "proj_1",
      });
    });

    expect(createBody).toMatchObject({
      agent_id: "claude_code",
      workspace: "/tmp/hecate-project",
      project_id: "proj_1",
    });
    expect(result.current.state.activeChatSession?.project_id).toBe("proj_1");
  });

  it("creates a Hecate chat shell when no model is selected", async () => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate");
    window.localStorage.removeItem("hecate.model");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_empty_hecate",
                title: "Hecate Chat",
                agent_id: "hecate",
                provider: "",
                model: "",
                workspace: "/tmp/hecate",
                status: "idle",
                messages: [],
              },
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      model: "",
      workspace: "/tmp/hecate",
    });
    expect(result.current.state.activeChatSessionID).toBe("chat_empty_hecate");
    expect(result.current.state.activeChatSession?.agent_id).toBe("hecate");
    expect(result.current.state.activeChatSession?.model).toBe("");
    expect(result.current.state.chatTarget).toBe("agent");
    expect(result.current.state.chatError).toBe("");
  });

  it("creates a Hecate chat shell when tools are on and no workspace is selected", async () => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.model", "llama3.1:8b");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/v1/models": () =>
          jsonResponse({
            object: "list",
            data: [
              {
                id: "llama3.1:8b",
                owned_by: "ollama",
                metadata: {
                  provider: "ollama",
                  provider_kind: "local",
                  capabilities: { tool_calling: "basic", streaming: true },
                },
              },
            ],
          }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_no_workspace",
                title: "Hecate Chat",
                agent_id: "hecate",
                provider: "",
                model: "llama3.1:8b",
                status: "idle",
                messages: [],
              },
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      model: "llama3.1:8b",
      workspace: "",
    });
    expect(result.current.state.activeChatSessionID).toBe("chat_no_workspace");
    expect(result.current.state.activeChatSession?.agent_id).toBe("hecate");
    expect(result.current.state.chatError).toBe("");
  });

  it("starts a direct Hecate chat when the selected model explicitly has no tools", async () => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.model", "llama3.1:8b");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/v1/models": () =>
          jsonResponse({
            object: "list",
            data: [
              {
                id: "llama3.1:8b",
                owned_by: "ollama",
                metadata: {
                  provider: "ollama",
                  provider_kind: "local",
                  capabilities: { tool_calling: "none", streaming: true },
                },
              },
            ],
          }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_direct_tools_unavailable",
                title: "Hecate Chat",
                agent_id: "hecate",
                provider: "",
                model: "llama3.1:8b",
                status: "idle",
                messages: [],
              },
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      model: "llama3.1:8b",
    });
    expect(createBody).not.toHaveProperty("workspace");
    expect(result.current.state.activeChatSessionID).toBe("chat_direct_tools_unavailable");
    expect(result.current.state.chatTarget).toBe("model");
    expect(result.current.state.chatError).toBe("");
  });

  it("surfaces a clear error when external-agent chat has no workspace", async () => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "codex");
    let createCalled = false;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/agent-adapters": () =>
          jsonResponse({
            object: "agent_adapters",
            data: [{ id: "codex", name: "Codex", available: true }],
          }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createCalled = true;
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createCalled).toBe(false);
    expect(result.current.state.activeChatSessionID).toBe("");
    expect(result.current.state.chatError).toBe(
      "Choose a workspace before using Hecate Chat tools or External Agent.",
    );
    expect(result.current.state.chatErrorCode).toBe("chat.workspace_required");
  });

  it("does not create or select a chat when eager external-agent prepare fails", async () => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
    window.localStorage.setItem("hecate.agentWorkspace", "/tmp/hecate-project");
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/agent-adapters": () =>
          jsonResponse({
            object: "agent_adapters",
            data: [{ id: "claude_code", name: "Claude Code", available: true }],
          }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            return jsonResponse(
              {
                error: {
                  type: "chat.adapter_unavailable",
                  message: "Claude Code could not start.",
                },
              },
              502,
            );
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(result.current.state.activeChatSessionID).toBe("");
    expect(result.current.state.activeChatSession).toBeNull();
    expect(result.current.state.notice).toEqual({
      kind: "error",
      message: "Claude Code could not start.",
    });
    expect(result.current.state.chatError).toContain("Claude Code could not start.");
  });

  it("updates RTK through per-chat settings for an active Hecate chat", async () => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatSessionID", "a1");
    let settingsBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": () =>
          jsonResponse({
            object: "chat_sessions",
            data: [
              {
                id: "a1",
                title: "Hecate",
                agent_id: "hecate",
                status: "completed",
                workspace: "/workspace",
                rtk_enabled: false,
                message_count: 0,
              },
            ],
          }),
        "/hecate/v1/chat/sessions/a1": () =>
          jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Hecate",
              agent_id: "hecate",
              status: "completed",
              workspace: "/workspace",
              rtk_enabled: false,
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:00Z",
            },
          }),
        "/hecate/v1/chat/sessions/a1/settings": (init) => {
          settingsBody = JSON.parse(String(init?.body ?? "{}"));
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Hecate",
              agent_id: "hecate",
              status: "completed",
              workspace: "/workspace",
              rtk_enabled: true,
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:01Z",
            },
          });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a1"));

    let ok = false;
    await act(async () => {
      ok = await result.current.actions.setHecateRTKEnabled(true);
    });

    expect(ok).toBe(true);
    expect(settingsBody).toEqual({ rtk_enabled: true });
    expect(result.current.state.hecateRTKEnabled).toBe(true);
    expect(result.current.state.activeChatSession?.rtk_enabled).toBe(true);
  });

  it("keeps the active agent chat selection when session refresh fails transiently", async () => {
    window.localStorage.setItem("hecate.chatSessionID", "a1");
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": () =>
          jsonResponse({
            object: "chat_sessions",
            data: [
              {
                id: "a1",
                title: "Still exists",
                agent_id: "codex",
                status: "running",
                message_count: 2,
              },
            ],
          }),
        "/hecate/v1/chat/sessions/a1": () =>
          jsonResponse({ error: { type: "gateway_error", message: "temporary failure" } }, 500),
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    expect(result.current.state.activeChatSessionID).toBe("a1");
    expect(window.localStorage.getItem("hecate.chatSessionID")).toBe("a1");
  });

  it("settles into a Local session after the dashboard loads", async () => {
    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    expect(result.current.state.session.label).toBe("Local");
  });

  // Regression for the "ModelPicker blinks fast when picking Ollama
  // with the runtime not running" report. Selecting a configured
  // provider whose runtime returned no models must settle on model="",
  // not bounce back to the gateway-wide default.
  it("leaves model empty when selecting a provider with no discovered models", async () => {
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/v1/models": () =>
          jsonResponse({
            object: "list",
            data: [
              {
                id: "gpt-4o-mini",
                owned_by: "openai",
                metadata: { provider: "openai", provider_kind: "cloud", default: true },
              },
            ],
          }),
        "/hecate/v1/providers/presets": () =>
          jsonResponse({
            object: "provider_presets",
            data: [
              {
                id: "openai",
                name: "OpenAI",
                kind: "cloud",
                protocol: "openai",
                base_url: "https://api.openai.com",
              },
              {
                id: "ollama",
                name: "Ollama",
                kind: "local",
                protocol: "openai",
                base_url: "http://127.0.0.1:11434/v1",
              },
            ],
          }),
        "/hecate/v1/providers/status": () =>
          jsonResponse({
            object: "provider_status",
            data: [
              {
                name: "openai",
                kind: "cloud",
                default_model: "gpt-4o-mini",
                models: ["gpt-4o-mini"],
                healthy: true,
              },
              { name: "ollama", kind: "local", default_model: "", models: [], healthy: false },
            ],
          }),
        "/hecate/v1/settings": () =>
          jsonResponse({
            object: "settings",
            data: {
              providers: [
                {
                  id: "openai",
                  name: "OpenAI",
                  preset_id: "openai",
                  kind: "cloud",
                  protocol: "openai",
                  base_url: "https://api.openai.com",
                  enabled: true,
                  credential_configured: true,
                },
                {
                  id: "ollama",
                  name: "Ollama",
                  preset_id: "ollama",
                  kind: "local",
                  protocol: "openai",
                  base_url: "http://127.0.0.1:11434/v1",
                  enabled: true,
                  credential_configured: false,
                },
              ],
              policy_rules: [],
              events: [],
            },
          }),
      }),
    );

    const { result } = renderRuntimeConsoleHook();
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
      await new Promise((r) => setTimeout(r, 10));
      expect(result.current.state.model).toBe("");
    }
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

      const { result } = renderRuntimeConsoleHook();
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
          return jsonResponse({
            object: "settings_provider_api_key",
            data: { id: "openai", status: "cleared" },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
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
          return new Response(JSON.stringify({ error: { message: "secret store is read-only" } }), {
            status: 400,
            headers: { "Content-Type": "application/json" },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
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

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.upsertPolicyRule({
          id: "deny-cloud",
          action: "deny",
          reason: "local-only",
          provider_kinds: ["cloud"],
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

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.deletePolicyRule("deny-cloud");
      });
      await waitFor(() =>
        expect(result.current.state.notice?.message).toBe("Policy rule deleted."),
      );
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
          return new Promise<Response>((resolve) => {
            resolveDelete = (response) => {
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
                {
                  id: "openai",
                  name: "OpenAI",
                  preset_id: "openai",
                  kind: "cloud",
                  protocol: "openai",
                  base_url: "https://api.openai.com/v1",
                  credential_configured: true,
                },
                ...(deleted
                  ? []
                  : [
                      {
                        id: "ollama",
                        name: "Ollama",
                        preset_id: "ollama",
                        kind: "local",
                        protocol: "openai",
                        base_url: "http://127.0.0.1:11434/v1",
                        credential_configured: false,
                      },
                    ]),
              ],
              policy_rules: [],
              events: [],
            },
          });
        }
        if (url === "/hecate/v1/providers/status") {
          return jsonResponse({
            object: "provider_status",
            data: [
              {
                name: "openai",
                kind: "cloud",
                healthy: true,
                status: "healthy",
                models: ["gpt-4o-mini"],
              },
              ...(deleted
                ? []
                : [
                    {
                      name: "ollama",
                      kind: "local",
                      healthy: true,
                      status: "healthy",
                      models: ["llama3.1:8b"],
                    },
                  ]),
            ],
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() =>
        expect(result.current.state.settingsConfig?.providers.map((p) => p.id)).toEqual([
          "openai",
          "ollama",
        ]),
      );

      let pendingDelete: Promise<void> | undefined;
      await act(async () => {
        pendingDelete = result.current.actions.deleteProvider("ollama");
      });

      expect(deleteCalls).toBe(1);
      expect(result.current.state.settingsConfig?.providers.map((p) => p.id)).toEqual(["openai"]);
      expect(result.current.state.providers.map((p) => p.name)).toEqual(["openai"]);

      resolveDelete?.(new Response(null, { status: 204 }));
      await act(async () => {
        await pendingDelete;
      });

      await waitFor(() =>
        expect(result.current.state.settingsConfig?.providers.map((p) => p.id)).toEqual(["openai"]),
      );
      expect(result.current.state.notice).toEqual({
        kind: "success",
        message: "Provider removed.",
      });
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
                {
                  id: "openai",
                  name: "OpenAI",
                  preset_id: "openai",
                  kind: "cloud",
                  protocol: "openai",
                  base_url: "https://api.openai.com/v1",
                  credential_configured: true,
                },
                {
                  id: "ollama",
                  name: "Ollama",
                  preset_id: "ollama",
                  kind: "local",
                  protocol: "openai",
                  base_url: "http://127.0.0.1:11434/v1",
                  credential_configured: false,
                },
                {
                  id: "anthropic",
                  name: "Anthropic",
                  preset_id: "anthropic",
                  kind: "cloud",
                  protocol: "anthropic",
                  base_url: "https://api.anthropic.com",
                  credential_configured: true,
                },
              ],
              policy_rules: [],
              events: [],
            },
          });
        }
        if (url === "/hecate/v1/providers/status") {
          return jsonResponse({
            object: "provider_status",
            data: [
              {
                name: "openai",
                kind: "cloud",
                healthy: true,
                status: "healthy",
                models: ["gpt-4o-mini"],
              },
              {
                name: "ollama",
                kind: "local",
                healthy: true,
                status: "healthy",
                models: ["llama3.1:8b"],
              },
              {
                name: "anthropic",
                kind: "cloud",
                healthy: true,
                status: "healthy",
                models: ["claude-sonnet-4-6"],
              },
            ],
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() =>
        expect(result.current.state.settingsConfig?.providers.map((p) => p.id)).toEqual([
          "openai",
          "ollama",
          "anthropic",
        ]),
      );
      await act(async () => {
        result.current.actions.setProviderFilter("ollama");
        result.current.actions.setModel("llama3.1:8b");
      });

      await act(async () => {
        await result.current.actions.deleteProvider("ollama");
      });

      expect(result.current.state.settingsConfig?.providers.map((p) => p.id)).toEqual([
        "openai",
        "ollama",
        "anthropic",
      ]);
      expect(result.current.state.providers.map((p) => p.name)).toEqual([
        "openai",
        "ollama",
        "anthropic",
      ]);
      expect(result.current.state.providerFilter).toBe("ollama");
      expect(result.current.state.model).toBe("llama3.1:8b");
      expect(result.current.state.notice).toEqual({
        kind: "error",
        message: "Failed to remove provider.",
      });
      expect(result.current.state.settingsError).toContain("provider is still referenced");
    });
  });

  // ─── Hecate Chat session actions ───────────────────────────────────────────
  describe("Hecate Chat session actions", () => {
    beforeEach(() => {
      window.localStorage.setItem("hecate.chatTarget", "model");
    });

    function withSessions(
      sessions: Array<{ id: string; title: string }>,
      routes: Record<string, () => Response> = {},
    ) {
      return defaultBackendMock({
        "/hecate/v1/chat/sessions": () => {
          const data = sessions.map((s) => ({
            ...s,
            agent_id: "hecate",
            status: "completed",
            message_count: 0,
            created_at: "2026-04-20T00:00:00Z",
            updated_at: "2026-04-20T00:00:00Z",
          }));
          return jsonResponse({ object: "chat_sessions", data });
        },
        ...routes,
      });
    }

    it("selectChatSession populates activeChatSession on success", async () => {
      fetchMock.mockImplementation(
        withSessions([{ id: "sess_42", title: "Existing" }], {
          "/hecate/v1/chat/sessions/sess_42": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "sess_42",
                title: "Existing",
                agent_id: "hecate",
                status: "completed",
                messages: [
                  {
                    id: "msg_u",
                    agent_id: "hecate",
                    role: "user",
                    content: "hi",
                    created_at: "2026-04-20T00:00:00Z",
                  },
                  {
                    id: "msg_a",
                    agent_id: "hecate",
                    role: "assistant",
                    content: "hello",
                    status: "completed",
                    created_at: "2026-04-20T00:00:00Z",
                  },
                ],
                provider: "openai",
                model: "gpt-4o-mini",
                created_at: "2026-04-20T00:00:00Z",
                updated_at: "2026-04-20T00:00:00Z",
              },
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.selectChatSession("sess_42");
      });
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("sess_42"));
      expect(result.current.state.activeChatSessionID).toBe("sess_42");
      expect(result.current.state.activeChatSession?.messages).toHaveLength(2);
      expect(result.current.state.activeChatSession?.model).toBe("gpt-4o-mini");
      expect(result.current.state.providerFilter).toBe("openai");
      expect(result.current.state.chatError).toBe("");
    });

    it("sends Hecate Chat instructions to task-backed turns", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.agentWorkspace", "/workspace");
      window.localStorage.setItem("hecate.systemPrompt", "Prefer small, reviewable diffs.");
      let postedBody: any = null;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: [
              {
                id: "a1",
                title: "Agent",
                agent_id: "hecate",
                status: "completed",
                workspace: "/workspace",
                provider: "openai",
                model: "gpt-4o-mini",
                message_count: 0,
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Agent",
              agent_id: "hecate",
              status: "completed",
              workspace: "/workspace",
              provider: "openai",
              model: "gpt-4o-mini",
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:00Z",
            },
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") {
          return emptyStreamResponse();
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          postedBody = JSON.parse(String(init?.body ?? "{}"));
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Agent",
              agent_id: "hecate",
              status: "completed",
              workspace: "/workspace",
              provider: "openai",
              model: "gpt-4o-mini",
              messages: [
                {
                  id: "u1",
                  agent_id: "hecate",
                  role: "user",
                  content: "inspect the repo",
                  created_at: "2026-04-20T00:00:01Z",
                },
                {
                  id: "a1",
                  agent_id: "hecate",
                  role: "assistant",
                  content: "done",
                  status: "completed",
                  created_at: "2026-04-20T00:00:02Z",
                },
              ],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:02Z",
            },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() =>
        expect(result.current.state.systemPrompt).toBe("Prefer small, reviewable diffs."),
      );
      await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));

      act(() => {
        result.current.actions.setMessage("inspect the repo");
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(postedBody).toMatchObject({
        content: "inspect the repo",
        execution_mode: "hecate_task",
        system_prompt: "Prefer small, reviewable diffs.",
        workspace: "/workspace",
      });
    });

    it("sends Hecate Chat turns directly when the selected model is not tool-capable", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.providerFilter", "ollama");
      window.localStorage.setItem("hecate.model", "smollm2:135m");
      let postedBody: any = null;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/v1/models") {
          return jsonResponse({
            object: "list",
            data: [
              {
                id: "smollm2:135m",
                owned_by: "ollama",
                metadata: {
                  provider: "ollama",
                  provider_kind: "local",
                  capabilities: { tool_calling: "none", streaming: true },
                },
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: [
              {
                id: "a1",
                title: "Agent",
                agent_id: "hecate",
                status: "completed",
                provider: "ollama",
                model: "smollm2:135m",
                message_count: 0,
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Agent",
              agent_id: "hecate",
              status: "completed",
              provider: "ollama",
              model: "smollm2:135m",
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:00Z",
            },
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") {
          return emptyStreamResponse();
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          postedBody = JSON.parse(String(init?.body ?? "{}"));
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Agent",
              agent_id: "hecate",
              status: "completed",
              provider: "ollama",
              model: "smollm2:135m",
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:01Z",
            },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.model).toBe("smollm2:135m"));

      act(() => {
        result.current.actions.setMessage("tell a small joke");
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(postedBody).toMatchObject({
        content: "tell a small joke",
        execution_mode: "direct_model",
        provider: "ollama",
        model: "smollm2:135m",
      });
      expect(postedBody).not.toHaveProperty("workspace");
      expect(result.current.state.chatError).toBe("");
    });

    it("queues a prompt while the active agent run is busy and sends it after completion", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.agentWorkspace", "/workspace");
      let sessionStatus = "running";
      let messagePostCount = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: [
              {
                id: "a1",
                title: "Agent",
                agent_id: "hecate",
                status: sessionStatus,
                workspace: "/workspace",
                provider: "openai",
                model: "gpt-4o-mini",
                message_count: 0,
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Agent",
              agent_id: "hecate",
              status: sessionStatus,
              workspace: "/workspace",
              provider: "openai",
              model: "gpt-4o-mini",
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: new Date().toISOString(),
            },
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") {
          return emptyStreamResponse();
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
          void init;
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Agent",
              agent_id: "hecate",
              status: "completed",
              workspace: "/workspace",
              provider: "openai",
              model: "gpt-4o-mini",
              messages: [
                {
                  id: "u1",
                  agent_id: "hecate",
                  role: "user",
                  content: "after this",
                  created_at: "2026-04-20T00:00:01Z",
                },
              ],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: new Date().toISOString(),
            },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.activeChatSession?.status).toBe("running"));
      await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));

      act(() => {
        result.current.actions.setMessage("after this");
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(messagePostCount).toBe(0);
      expect(result.current.state.message).toBe("");
      expect(result.current.state.queuedChatMessages).toHaveLength(1);
      expect(result.current.state.queuedChatMessages[0].session_id).toBe("a1");
      expect(result.current.state.queuedChatMessages[0].content).toBe("after this");
      expect(result.current.state.queuedChatMessages[0].agent_id).toBe("hecate");

      sessionStatus = "completed";
      await act(async () => {
        await result.current.actions.selectChatSession("a1");
      });

      await waitFor(() => expect(messagePostCount).toBe(1));
      await waitFor(() => expect(result.current.state.queuedChatMessages).toHaveLength(0));
    });

    it("restores queued prompts from local storage and persists edits", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.agentWorkspace", "/workspace");
      window.localStorage.setItem(
        "hecate.queuedChatMessages",
        JSON.stringify([
          {
            id: "queued_restore",
            session_id: "a1",
            content: "keep this after refresh",
            execution_mode: "hecate_task",
            provider_filter: "auto",
            model: "ministral-3:latest",
            workspace: "/workspace",
            system_prompt: "",
            agent_id: "hecate",
            created_at: "2026-04-20T00:00:01Z",
          },
        ]),
      );
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () =>
            jsonResponse({
              object: "chat_sessions",
              data: [
                {
                  id: "a1",
                  title: "Agent",
                  agent_id: "hecate",
                  status: "running",
                  workspace: "/workspace",
                  message_count: 0,
                },
              ],
            }),
          "/hecate/v1/chat/sessions/a1": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "a1",
                title: "Agent",
                agent_id: "hecate",
                status: "running",
                workspace: "/workspace",
                messages: [],
                created_at: "2026-04-20T00:00:00Z",
                updated_at: "2026-04-20T00:00:00Z",
              },
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      expect(result.current.state.queuedChatMessages).toHaveLength(1);
      expect(result.current.state.queuedChatMessages[0].content).toBe("keep this after refresh");

      act(() => {
        result.current.actions.updateQueuedChatMessage("queued_restore", "edited after refresh");
      });

      await waitFor(() => {
        const stored = JSON.parse(window.localStorage.getItem("hecate.queuedChatMessages") ?? "[]");
        expect(stored[0].content).toBe("edited after refresh");
      });
    });

    it("keeps queued prompt edits usable when browser storage writes fail", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.agentWorkspace", "/workspace");
      window.localStorage.setItem(
        "hecate.queuedChatMessages",
        JSON.stringify([
          {
            id: "queued_restore",
            session_id: "a1",
            content: "keep this after refresh",
            execution_mode: "hecate_task",
            provider_filter: "auto",
            model: "ministral-3:latest",
            workspace: "/workspace",
            system_prompt: "",
            agent_id: "hecate",
            created_at: "2026-04-20T00:00:01Z",
          },
        ]),
      );
      const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
      const storageSpy = vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
        if (key === "hecate.queuedChatMessages") {
          throw new DOMException("quota exceeded", "QuotaExceededError");
        }
        return originalSetItem(key, value);
      });
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () =>
            jsonResponse({
              object: "chat_sessions",
              data: [
                {
                  id: "a1",
                  title: "Agent",
                  agent_id: "hecate",
                  status: "running",
                  workspace: "/workspace",
                  message_count: 0,
                },
              ],
            }),
          "/hecate/v1/chat/sessions/a1": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "a1",
                title: "Agent",
                agent_id: "hecate",
                status: "running",
                workspace: "/workspace",
                messages: [],
                created_at: "2026-04-20T00:00:00Z",
                updated_at: "2026-04-20T00:00:00Z",
              },
            }),
        }),
      );

      try {
        const { result } = renderRuntimeConsoleHook();
        await waitFor(() => expect(result.current.state.loading).toBe(false));

        act(() => {
          result.current.actions.updateQueuedChatMessage("queued_restore", "edited in memory");
        });

        await waitFor(() =>
          expect(storageSpy).toHaveBeenCalledWith("hecate.queuedChatMessages", expect.any(String)),
        );
        expect(result.current.state.queuedChatMessages[0].content).toBe("edited in memory");
      } finally {
        storageSpy.mockRestore();
      }
    });

    it("prunes stored queued prompts for sessions that no longer exist", async () => {
      window.localStorage.setItem(
        "hecate.queuedChatMessages",
        JSON.stringify([
          {
            id: "queued_keep",
            session_id: "a1",
            content: "keep",
            execution_mode: "hecate_task",
            provider_filter: "auto",
            model: "ministral-3:latest",
            workspace: "/workspace",
            system_prompt: "",
            agent_id: "hecate",
            created_at: "2026-04-20T00:00:01Z",
          },
          {
            id: "queued_drop",
            session_id: "missing",
            content: "drop",
            execution_mode: "hecate_task",
            provider_filter: "auto",
            model: "ministral-3:latest",
            workspace: "/workspace",
            system_prompt: "",
            agent_id: "hecate",
            created_at: "2026-04-20T00:00:02Z",
          },
        ]),
      );
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () =>
            jsonResponse({
              object: "chat_sessions",
              data: [
                {
                  id: "a1",
                  title: "Agent",
                  agent_id: "hecate",
                  status: "running",
                  workspace: "/workspace",
                  message_count: 0,
                },
              ],
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await waitFor(() =>
        expect(result.current.state.queuedChatMessages.map((item) => item.id)).toEqual([
          "queued_keep",
        ]),
      );
      await waitFor(() => {
        const stored = JSON.parse(window.localStorage.getItem("hecate.queuedChatMessages") ?? "[]");
        expect(stored.map((item: { id: string }) => item.id)).toEqual(["queued_keep"]);
      });
    });

    it("does not drain a queued prompt into a different selected chat", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.agentWorkspace", "/workspace");
      let messagePostCount = 0;
      let resolveA2: (() => void) | undefined;
      const a2Gate = new Promise<void>((resolve) => {
        resolveA2 = resolve;
      });
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: [
              {
                id: "a1",
                title: "Busy chat",
                agent_id: "hecate",
                status: "running",
                workspace: "/workspace",
                message_count: 0,
              },
              {
                id: "a2",
                title: "Idle chat",
                agent_id: "hecate",
                status: "completed",
                workspace: "/workspace",
                message_count: 0,
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Busy chat",
              agent_id: "hecate",
              status: "running",
              workspace: "/workspace",
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:00Z",
            },
          });
        }
        if (url === "/hecate/v1/chat/sessions/a2") {
          await a2Gate;
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a2",
              title: "Idle chat",
              agent_id: "hecate",
              status: "completed",
              workspace: "/workspace",
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:00Z",
            },
          });
        }
        if (url.endsWith("/messages")) {
          messagePostCount += 1;
          void init;
          return jsonResponse({ object: "chat_session", data: {} });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a1"));

      act(() => {
        result.current.actions.setMessage("keep this with a1");
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(result.current.state.queuedChatMessages).toHaveLength(1);
      let selectPromise!: Promise<void>;
      act(() => {
        selectPromise = result.current.actions.selectChatSession("a2");
      });

      await waitFor(() => expect(result.current.state.activeChatSessionID).toBe("a2"));
      expect(result.current.state.activeChatSession?.id).toBe("a1");
      expect(result.current.state.queuedChatMessages).toHaveLength(1);
      expect(result.current.state.queuedChatMessages[0].session_id).toBe("a1");
      expect(messagePostCount).toBe(0);
      resolveA2?.();
      await act(async () => {
        await selectPromise;
      });
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a2"));
      expect(messagePostCount).toBe(0);
    });

    it("waits for the selected chat record before draining a queued prompt", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a2");
      window.localStorage.setItem("hecate.agentWorkspace", "/workspace");

      let messagePostCount = 0;
      let a2FetchCount = 0;
      let resolveA2Refresh: (() => void) | undefined;
      const a2RefreshGate = new Promise<void>((resolve) => {
        resolveA2Refresh = resolve;
      });
      const session = (id: string, status: string) => ({
        object: "chat_session",
        data: {
          id,
          title: id,
          agent_id: "hecate",
          status,
          workspace: "/workspace",
          provider: "openai",
          model: "gpt-4o-mini",
          messages: [],
          created_at: "2026-04-20T00:00:00Z",
          updated_at: "2026-04-20T00:00:00Z",
        },
      });

      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: [
              {
                id: "a1",
                title: "Idle chat",
                agent_id: "hecate",
                status: "completed",
                workspace: "/workspace",
                message_count: 0,
              },
              {
                id: "a2",
                title: "Busy chat",
                agent_id: "hecate",
                status: "running",
                workspace: "/workspace",
                message_count: 0,
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse(session("a1", "completed"));
        }
        if (url === "/hecate/v1/chat/sessions/a2") {
          a2FetchCount += 1;
          if (a2FetchCount > 1) {
            await a2RefreshGate;
            return jsonResponse(session("a2", "completed"));
          }
          return jsonResponse(session("a2", "running"));
        }
        if (url === "/hecate/v1/chat/sessions/a2/messages") {
          messagePostCount += 1;
          void init;
          return jsonResponse(session("a2", "completed"));
        }
        if (url.endsWith("/messages")) {
          throw new Error(`unexpected message post: ${url}`);
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a2"));
      await waitFor(() => expect(result.current.state.activeChatSession?.status).toBe("running"));
      await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));

      act(() => {
        result.current.actions.setMessage("drain only after a2 loads");
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(result.current.state.queuedChatMessages).toHaveLength(1);

      await act(async () => {
        await result.current.actions.selectChatSession("a1");
      });
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a1"));

      let selectA2Promise!: Promise<void>;
      act(() => {
        selectA2Promise = result.current.actions.selectChatSession("a2");
      });

      await waitFor(() => expect(result.current.state.activeChatSessionID).toBe("a2"));
      expect(result.current.state.activeChatSession?.id).toBe("a1");
      expect(result.current.state.queuedChatMessages).toHaveLength(1);
      expect(messagePostCount).toBe(0);

      resolveA2Refresh?.();
      await act(async () => {
        await selectA2Promise;
      });

      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a2"));
      await waitFor(() => expect(messagePostCount).toBe(1));
      await waitFor(() => expect(result.current.state.queuedChatMessages).toHaveLength(0));
    });

    it("keeps the tools toggle scoped to the selected Hecate chat", async () => {
      const hecateSession = (id: string) => ({
        object: "chat_session",
        data: {
          id,
          title: id,
          agent_id: "hecate",
          status: "completed",
          workspace: "/workspace",
          provider: "openai",
          model: "gpt-4o-mini",
          messages: [],
          created_at: "2026-04-20T00:00:00Z",
          updated_at: "2026-04-20T00:00:00Z",
        },
      });
      fetchMock.mockImplementation(
        withSessions(
          [
            { id: "chat_a", title: "A" },
            { id: "chat_b", title: "B" },
          ],
          {
            "/hecate/v1/chat/sessions/chat_a": () => jsonResponse(hecateSession("chat_a")),
            "/hecate/v1/chat/sessions/chat_b": () => jsonResponse(hecateSession("chat_b")),
          },
        ),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.selectChatSession("chat_a");
      });
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("chat_a"));
      expect(result.current.state.chatTarget).toBe("agent");

      act(() => {
        result.current.actions.setChatTarget("model");
      });
      expect(result.current.state.chatTarget).toBe("model");

      await act(async () => {
        await result.current.actions.selectChatSession("chat_b");
      });
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("chat_b"));
      expect(result.current.state.chatTarget).toBe("agent");

      await act(async () => {
        await result.current.actions.selectChatSession("chat_a");
      });
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("chat_a"));
      expect(result.current.state.chatTarget).toBe("model");
    });

    it("restores provider and model from the latest Hecate chat segment on selection", async () => {
      fetchMock.mockImplementation(
        withSessions([{ id: "mixed_chat", title: "Mixed" }], {
          "/v1/models": () =>
            jsonResponse({
              object: "list",
              data: [
                {
                  id: "smollm2:135m",
                  owned_by: "ollama",
                  metadata: { provider: "ollama", provider_kind: "local" },
                },
                {
                  id: "qwen2.5-coder",
                  owned_by: "ollama",
                  metadata: { provider: "ollama", provider_kind: "local" },
                },
              ],
            }),
          "/hecate/v1/chat/sessions/mixed_chat": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "mixed_chat",
                title: "Mixed",
                agent_id: "hecate",
                status: "completed",
                workspace: "/workspace",
                provider: "ollama",
                model: "smollm2:135m",
                segments: [
                  {
                    id: "model:first",
                    execution_mode: "direct_model",
                    provider: "ollama",
                    model: "smollm2:135m",
                    status: "completed",
                    message_count: 2,
                  },
                  {
                    id: "task:task_tools",
                    execution_mode: "hecate_task",
                    provider: "ollama",
                    model: "qwen2.5-coder",
                    task_id: "task_tools",
                    status: "completed",
                    message_count: 2,
                  },
                ],
                messages: [],
                created_at: "2026-04-20T00:00:00Z",
                updated_at: "2026-04-20T00:00:00Z",
              },
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.selectChatSession("mixed_chat");
      });

      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("mixed_chat"));
      expect(result.current.state.providerFilter).toBe("ollama");
      expect(result.current.state.model).toBe("qwen2.5-coder");
    });

    it("selectChatSession 404 clears the active agent-chat selection and surfaces an error", async () => {
      fetchMock.mockImplementation(
        withSessions([{ id: "sess_gone", title: "Gone" }], {
          "/hecate/v1/chat/sessions/sess_gone": () =>
            new Response(JSON.stringify({ error: { message: "session not found" } }), {
              status: 404,
              headers: { "Content-Type": "application/json" },
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.selectChatSession("sess_gone");
      });
      expect(result.current.state.activeChatSessionID).toBe("");
      await waitFor(() => expect(result.current.state.chatError).toContain("session not found"));
      expect(result.current.state.notice?.kind).toBe("error");
    });

    it("deleteChatSession removes the agent-chat session from the sidebar and notices", async () => {
      let deleteCalls = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions/sess_b" && init?.method === "DELETE") {
          deleteCalls++;
          return new Response(null, { status: 204 });
        }
        return withSessions([
          { id: "sess_a", title: "Keep" },
          { id: "sess_b", title: "Delete me" },
        ])(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.chatSessions).toHaveLength(2));

      await act(async () => {
        await result.current.actions.deleteChatSession("sess_b");
      });

      expect(deleteCalls).toBe(1);
      await waitFor(() =>
        expect(result.current.state.chatSessions.map((s) => s.id)).toEqual(["sess_a"]),
      );
      expect(result.current.state.notice?.kind).toBe("success");
      expect(result.current.state.notice?.message).toBe("Agent chat deleted.");
    });

    it("deleteChatSession removes queued prompts for the deleted session", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "sess_b");
      window.localStorage.setItem("hecate.agentWorkspace", "/workspace");
      let deleteCalls = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: [
              {
                id: "sess_a",
                title: "Keep",
                agent_id: "hecate",
                status: "completed",
                workspace: "/workspace",
                message_count: 0,
              },
              {
                id: "sess_b",
                title: "Delete me",
                agent_id: "hecate",
                status: "running",
                workspace: "/workspace",
                message_count: 0,
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions/sess_b" && init?.method === "DELETE") {
          deleteCalls += 1;
          return new Response(null, { status: 204 });
        }
        if (url === "/hecate/v1/chat/sessions/sess_b") {
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "sess_b",
              title: "Delete me",
              agent_id: "hecate",
              status: "running",
              workspace: "/workspace",
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:00Z",
            },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("sess_b"));

      act(() => {
        result.current.actions.setMessage("do this later");
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });
      expect(result.current.state.queuedChatMessages).toHaveLength(1);

      await act(async () => {
        await result.current.actions.deleteChatSession("sess_b");
      });

      expect(deleteCalls).toBe(1);
      expect(result.current.state.queuedChatMessages).toHaveLength(0);
      expect(result.current.state.activeChatSessionID).toBe("");
    });

    it("renameChatSession patches agent-chat session titles", async () => {
      window.localStorage.setItem("hecate.chatTarget", "external_agent");
      window.localStorage.setItem("hecate.chatSessionID", "agent_a");
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions/agent_a" && init?.method === "PATCH") {
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "agent_a",
              title: "Renamed agent chat",
              agent_id: "codex",
              workspace: "/tmp/workspace",
              status: "idle",
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:02:00Z",
            },
          });
        }
        return defaultBackendMock({
          "/hecate/v1/chat/sessions": () =>
            jsonResponse({
              object: "chat_sessions",
              data: [
                {
                  id: "agent_a",
                  title: "Old agent title",
                  agent_id: "codex",
                  workspace: "/tmp/workspace",
                  status: "idle",
                  message_count: 0,
                  created_at: "2026-04-20T00:00:00Z",
                  updated_at: "2026-04-20T00:00:00Z",
                },
              ],
            }),
          "/hecate/v1/chat/sessions/agent_a": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "agent_a",
                title: "Old agent title",
                agent_id: "codex",
                workspace: "/tmp/workspace",
                status: "idle",
                messages: [],
                created_at: "2026-04-20T00:00:00Z",
                updated_at: "2026-04-20T00:00:00Z",
              },
            }),
        })(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() =>
        expect(result.current.state.chatSessions[0]?.title).toBe("Old agent title"),
      );

      await act(async () => {
        await result.current.actions.renameChatSession("agent_a", "Renamed agent chat");
      });

      await waitFor(() =>
        expect(result.current.state.chatSessions[0].title).toBe("Renamed agent chat"),
      );
      expect(result.current.state.activeChatSession?.title).toBe("Renamed agent chat");
    });

    it("resolveTaskApproval optimistically marks the row resolved before the API call returns", async () => {
      // Hecate-task approvals (distinct from the external-agent ACP
      // approval map exercised below) live as activities on the
      // active Hecate Chat session. The banner derivation in the
      // chat surface (`pendingHecateTaskApprovals` in
      // ChatView.tsx, which keys on `activity.status` /
      // `needs_action`) collapses any row whose status flips off
      // "awaiting_approval" — so flipping the activity to
      // "approved" before the network round-trip returns is what
      // makes the banner row disappear instantly on click. The
      // prior version waited for the API to return, which
      // surfaced as a 50–500 ms unresponsive UI on slow links
      // and let an operator double-click a duplicate request.
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");

      const seededSession = {
        id: "a1",
        title: "Repo work",
        agent_id: "hecate",
        workspace: "/workspace",
        task_id: "task_42",
        latest_run_id: "run_99",
        status: "awaiting_approval",
        message_count: 1,
        messages: [
          {
            id: "msg_assistant",
            run_id: "run_99",
            role: "assistant",
            content: "",
            status: "awaiting_approval",
            created_at: "2026-04-20T00:00:01Z",
            activities: [
              {
                id: "task:approval:appr_1",
                type: "approval",
                status: "awaiting_approval",
                kind: "agent_loop_tool_call",
                title: "Awaiting approval",
                detail: "shell_exec",
                approval_id: "appr_1",
                needs_action: true,
              },
            ],
          },
        ],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:01Z",
      };
      let resolveResolveCall: ((response: Response) => void) | undefined;
      let resolveCalls = 0;
      let refetchCalls = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({ object: "chat_sessions", data: [seededSession] });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          refetchCalls += 1;
          return jsonResponse({ object: "chat_session", data: seededSession });
        }
        if (url === "/hecate/v1/chat/sessions/a1/approvals?status=pending") {
          return jsonResponse({ object: "list", data: [] });
        }
        if (
          url === "/hecate/v1/tasks/task_42/approvals/appr_1/resolve" &&
          init?.method === "POST"
        ) {
          resolveCalls += 1;
          return new Promise<Response>((resolve) => {
            resolveResolveCall = resolve;
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.activeChatSession?.task_id).toBe("task_42"));
      const initialRefetches = refetchCalls;

      let pendingResolve: Promise<boolean> | undefined;
      await act(async () => {
        pendingResolve = result.current.actions.resolveTaskApproval("task_42", "appr_1", {
          decision: "approve",
        });
      });

      // The API call is in flight; the optimistic patch must already
      // be visible on the local session.
      expect(resolveCalls).toBe(1);
      const activity = result.current.state.activeChatSession?.messages?.[0]?.activities?.[0];
      expect(activity?.status).toBe("approved");
      expect(activity?.needs_action).toBe(false);
      // No refresh has fired yet — those are sequenced after the API
      // returns. (initialRefetches accounts for the catch-up refetch
      // when the session was hydrated.)
      expect(refetchCalls).toBe(initialRefetches);

      resolveResolveCall?.(new Response(null, { status: 204 }));
      const ok = await act(async () => pendingResolve!);
      expect(ok).toBe(true);
      expect(refetchCalls).toBeGreaterThan(initialRefetches);
    });

    it("resolveTaskApproval rolls back the optimistic patch when the API rejects", async () => {
      // Optimistic-before-call only works if a genuine failure
      // restores the prior state — otherwise the operator sees the
      // banner gone forever even though the run is still gated.
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");

      const seededSession = {
        id: "a1",
        title: "Repo work",
        agent_id: "hecate",
        workspace: "/workspace",
        task_id: "task_42",
        latest_run_id: "run_99",
        status: "awaiting_approval",
        message_count: 1,
        messages: [
          {
            id: "msg_assistant",
            run_id: "run_99",
            role: "assistant",
            content: "",
            status: "awaiting_approval",
            created_at: "2026-04-20T00:00:01Z",
            activities: [
              {
                id: "task:approval:appr_1",
                type: "approval",
                status: "awaiting_approval",
                kind: "agent_loop_tool_call",
                title: "Awaiting approval",
                detail: "shell_exec",
                approval_id: "appr_1",
                needs_action: true,
              },
            ],
          },
        ],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:01Z",
      };
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({ object: "chat_sessions", data: [seededSession] });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse({ object: "chat_session", data: seededSession });
        }
        if (url === "/hecate/v1/chat/sessions/a1/approvals?status=pending") {
          return jsonResponse({ object: "list", data: [] });
        }
        if (
          url === "/hecate/v1/tasks/task_42/approvals/appr_1/resolve" &&
          init?.method === "POST"
        ) {
          return new Response(JSON.stringify({ error: { message: "upstream is unhappy" } }), {
            status: 500,
            headers: { "Content-Type": "application/json" },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.activeChatSession?.task_id).toBe("task_42"));

      let ok: boolean | undefined;
      await act(async () => {
        ok = await result.current.actions.resolveTaskApproval("task_42", "appr_1", {
          decision: "approve",
        });
      });

      expect(ok).toBe(false);
      expect(result.current.state.notice?.kind).toBe("error");
      // Rollback restored the original `awaiting_approval` status so
      // the banner reappears and the operator can retry.
      // NB: `refreshChatSession` is NOT called on a generic
      // failure (only on the "not pending" branch), so the only way
      // the row returns to its prior state is via the rollback
      // inside the catch.
      const activity = result.current.state.activeChatSession?.messages?.[0]?.activities?.[0];
      expect(activity?.status).toBe("awaiting_approval");
      expect(activity?.needs_action).toBe(true);
    });

    it("resolveTaskApproval rollback restores the right activity when activity.id is absent", async () => {
      // ChatActivityRecord.id is optional. Earlier the rollback
      // matched the snapshot row by `activity.id === activity.id`,
      // which (a) fails to restore when the current row has no id
      // and (b) would wrongly match the first id-less snapshot row
      // if multiple activities lack ids. The fix: identify the
      // target activity by `approval_id` (or the projected
      // `task:approval:<id>` id), the same predicate the dedupe
      // selection uses.
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");

      const seededSession = {
        id: "a1",
        title: "Repo work",
        agent_id: "hecate",
        workspace: "/workspace",
        task_id: "task_42",
        latest_run_id: "run_99",
        status: "awaiting_approval",
        message_count: 1,
        messages: [
          {
            id: "msg_assistant",
            run_id: "run_99",
            role: "assistant",
            content: "",
            status: "awaiting_approval",
            created_at: "2026-04-20T00:00:01Z",
            activities: [
              // Two id-less activities: one is the target approval,
              // one is unrelated. A naïve `id === id` lookup in the
              // snapshot would match the first id-less row instead
              // of the right approval — proving why the predicate
              // must key on approval_id.
              {
                type: "thinking",
                title: "preparing",
                detail: "warming up",
              },
              {
                type: "approval",
                status: "awaiting_approval",
                kind: "agent_loop_tool_call",
                title: "Awaiting approval",
                detail: "shell_exec",
                approval_id: "appr_1",
                needs_action: true,
              },
            ],
          },
        ],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:01Z",
      };
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({ object: "chat_sessions", data: [seededSession] });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse({ object: "chat_session", data: seededSession });
        }
        if (url === "/hecate/v1/chat/sessions/a1/approvals?status=pending") {
          return jsonResponse({ object: "list", data: [] });
        }
        if (
          url === "/hecate/v1/tasks/task_42/approvals/appr_1/resolve" &&
          init?.method === "POST"
        ) {
          return new Response(JSON.stringify({ error: { message: "upstream is unhappy" } }), {
            status: 500,
            headers: { "Content-Type": "application/json" },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.activeChatSession?.task_id).toBe("task_42"));

      let ok: boolean | undefined;
      await act(async () => {
        ok = await result.current.actions.resolveTaskApproval("task_42", "appr_1", {
          decision: "approve",
        });
      });
      expect(ok).toBe(false);

      const activities = result.current.state.activeChatSession?.messages?.[0]?.activities ?? [];
      // The thinking row should be untouched.
      const thinking = activities.find((a) => a.type === "thinking");
      expect(thinking?.title).toBe("preparing");
      // The approval row must be restored to its pre-resolve state
      // — keyed by approval_id, not by the (absent) id field.
      const approval = activities.find((a) => a.approval_id === "appr_1");
      expect(approval?.status).toBe("awaiting_approval");
      expect(approval?.needs_action).toBe(true);
    });

    it("resolveTaskApproval rolls back when the server says 'not pending' and the refresh also fails", async () => {
      // The "already resolved upstream" race: another tab approved
      // (or the run timed out) while this tab tried to reject. The
      // server returns a "not pending" error. Optimistic patch
      // currently shows OUR chosen decision ("rejected"), which may
      // be wrong if the server actually approved. We try to refresh
      // to pull server-truth; if THAT also fails (network blip,
      // gateway transient), we cannot trust the optimistic patch
      // and must roll back so the row reflects "still pending"
      // rather than a possibly-wrong final state.
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");

      const seededSession = {
        id: "a1",
        title: "Repo work",
        agent_id: "hecate",
        workspace: "/workspace",
        task_id: "task_42",
        latest_run_id: "run_99",
        status: "awaiting_approval",
        message_count: 1,
        messages: [
          {
            id: "msg_assistant",
            run_id: "run_99",
            role: "assistant",
            content: "",
            status: "awaiting_approval",
            created_at: "2026-04-20T00:00:01Z",
            activities: [
              {
                id: "task:approval:appr_1",
                type: "approval",
                status: "awaiting_approval",
                kind: "agent_loop_tool_call",
                title: "Awaiting approval",
                detail: "shell_exec",
                approval_id: "appr_1",
                needs_action: true,
              },
            ],
          },
        ],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:01Z",
      };
      let firstRefetchServed = false;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({ object: "chat_sessions", data: [seededSession] });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          // Serve the catch-up refetch on session select, then fail
          // every subsequent refetch — including the one inside
          // the not-pending branch.
          if (!firstRefetchServed) {
            firstRefetchServed = true;
            return jsonResponse({ object: "chat_session", data: seededSession });
          }
          return new Response(null, { status: 503 });
        }
        if (url === "/hecate/v1/chat/sessions/a1/approvals?status=pending") {
          return jsonResponse({ object: "list", data: [] });
        }
        if (
          url === "/hecate/v1/tasks/task_42/approvals/appr_1/resolve" &&
          init?.method === "POST"
        ) {
          return new Response(JSON.stringify({ error: { message: "approval is not pending" } }), {
            status: 409,
            headers: { "Content-Type": "application/json" },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.activeChatSession?.task_id).toBe("task_42"));

      let ok: boolean | undefined;
      await act(async () => {
        ok = await result.current.actions.resolveTaskApproval("task_42", "appr_1", {
          decision: "reject",
        });
      });

      // Both API and refresh failed — the function reports failure
      // (rather than a misleading success) and rolls back.
      expect(ok).toBe(false);
      const activity = result.current.state.activeChatSession?.messages?.[0]?.activities?.[0];
      expect(activity?.status).toBe("awaiting_approval");
      expect(activity?.needs_action).toBe(true);
      expect(result.current.state.notice?.kind).toBe("error");
    });

    it("resolveTaskApproval rollback does not clobber a session the operator switched to mid-flight", async () => {
      // Concurrency hazard: the operator clicks Approve on session
      // a1, then navigates to a2 while the request is in flight.
      // The API call rejects. A naïve rollback that does
      // `setActiveChatSession(snapshot)` would replace the
      // active session (now a2) with a1's pre-resolve snapshot,
      // dragging the operator back to a1. The functional updater
      // must bail when current.id !== snapshot.id.
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");

      const sessionA = {
        id: "a1",
        title: "A",
        agent_id: "hecate",
        workspace: "/workspace",
        task_id: "task_42",
        latest_run_id: "run_99",
        status: "awaiting_approval",
        message_count: 1,
        messages: [
          {
            id: "msg_a",
            run_id: "run_99",
            role: "assistant",
            content: "",
            status: "awaiting_approval",
            created_at: "2026-04-20T00:00:01Z",
            activities: [
              {
                id: "task:approval:appr_1",
                type: "approval",
                status: "awaiting_approval",
                kind: "agent_loop_tool_call",
                title: "Awaiting approval",
                detail: "shell_exec",
                approval_id: "appr_1",
                needs_action: true,
              },
            ],
          },
        ],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:01Z",
      };
      const sessionB = {
        id: "a2",
        title: "B",
        agent_id: "hecate",
        workspace: "/workspace",
        status: "completed",
        message_count: 0,
        messages: [],
        created_at: "2026-04-20T00:00:02Z",
        updated_at: "2026-04-20T00:00:02Z",
      };
      let resolveResolveCall: ((response: Response) => void) | undefined;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({ object: "chat_sessions", data: [sessionA, sessionB] });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse({ object: "chat_session", data: sessionA });
        }
        if (url === "/hecate/v1/chat/sessions/a2") {
          return jsonResponse({ object: "chat_session", data: sessionB });
        }
        if (
          url.startsWith("/hecate/v1/chat/sessions/") &&
          url.includes("/approvals?status=pending")
        ) {
          return jsonResponse({ object: "list", data: [] });
        }
        if (
          url === "/hecate/v1/tasks/task_42/approvals/appr_1/resolve" &&
          init?.method === "POST"
        ) {
          return new Promise<Response>((resolve) => {
            resolveResolveCall = (response) => resolve(response);
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a1"));

      // Operator clicks Approve on a1 (request hangs).
      let pendingResolve: Promise<boolean> | undefined;
      await act(async () => {
        pendingResolve = result.current.actions.resolveTaskApproval("task_42", "appr_1", {
          decision: "approve",
        });
      });
      expect(result.current.state.activeChatSession?.id).toBe("a1");

      // Operator navigates to a2 while the resolve is still in
      // flight.
      await act(async () => {
        await result.current.actions.selectChatSession("a2");
      });
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a2"));

      // Now the API rejects. Rollback fires, but the active session
      // is a2 — the rollback must NOT pull the operator back to a1.
      resolveResolveCall?.(
        new Response(JSON.stringify({ error: { message: "upstream is unhappy" } }), {
          status: 500,
          headers: { "Content-Type": "application/json" },
        }),
      );
      const ok = await act(async () => pendingResolve!);
      expect(ok).toBe(false);
      expect(result.current.state.activeChatSession?.id).toBe("a2");
    });
  });

  // ─── Agent-chat approvals state ───────────────────────────────────────────
  //
  // On session select we fire a catch-up refetch against
  // /hecate/v1/chat/sessions/{id}/approvals?status=pending. The
  // returned rows are projected to banner-essentials and stored in
  // `pendingApprovalsBySessionID`. SSE events later upsert/remove on
  // top of the same map. The Map instance is always replaced — never
  // mutated in place.
  describe("chat approvals state", () => {
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
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      expect(result.current.state.pendingApprovalsBySessionID.size).toBe(0);
      expect(result.current.state.chatGrants).toEqual([]);
    });

    it("populates the pending map from the catch-up refetch when a session is selected", async () => {
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () =>
            jsonResponse({
              object: "chat_sessions",
              data: [
                { id: "a1", title: "S1", agent_id: "codex", status: "running", message_count: 0 },
              ],
            }),
          "/hecate/v1/chat/sessions/a1": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "a1",
                title: "S1",
                agent_id: "codex",
                workspace: "/tmp",
                status: "running",
              },
            }),
          "/hecate/v1/chat/sessions/a1/approvals?status=pending": () =>
            jsonResponse({
              object: "list",
              data: [approvalRow()],
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      // Effect-driven refetch may need a tick after activeChatSessionID
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
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () => jsonResponse({ object: "chat_sessions", data: [] }),
          "/hecate/v1/chat/sessions/a1": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "a1",
                title: "A",
                agent_id: "codex",
                workspace: "/tmp",
                status: "running",
              },
            }),
          "/hecate/v1/chat/sessions/b1": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "b1",
                title: "B",
                agent_id: "codex",
                workspace: "/tmp",
                status: "running",
              },
            }),
          "/hecate/v1/chat/sessions/a1/approvals?status=pending": () =>
            jsonResponse({
              object: "list",
              data: approvalsForA,
            }),
          "/hecate/v1/chat/sessions/b1/approvals?status=pending": () =>
            jsonResponse({
              object: "list",
              data: [],
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
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
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () => jsonResponse({ object: "chat_sessions", data: [] }),
          "/hecate/v1/chat/sessions/a1": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "a1",
                title: "A",
                agent_id: "codex",
                workspace: "/tmp",
                status: "running",
              },
            }),
          "/hecate/v1/chat/sessions/b1": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "b1",
                title: "B",
                agent_id: "codex",
                workspace: "/tmp",
                status: "running",
              },
            }),
          "/hecate/v1/chat/sessions/a1/approvals?status=pending": () => {
            if (!delayARefetch) {
              return jsonResponse({ object: "list", data: [approvalRow()] });
            }
            return new Promise<Response>((resolve) => {
              releaseARefetch = () =>
                resolve(jsonResponse({ object: "list", data: [approvalRow()] }));
            });
          },
          "/hecate/v1/chat/sessions/b1/approvals?status=pending": () =>
            jsonResponse({
              object: "list",
              data: [],
            }),
          "/hecate/v1/chat/sessions/a1/approvals/ap-1/resolve": () =>
            jsonResponse({
              object: "chat_approval",
              data: approvalRow({
                status: "approved",
                decision: "approve",
                scope: "once",
                path: "operator",
              }),
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
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
        const ok = await result.current.actions.resolveChatApproval("a1", "ap-1", {
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
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () => jsonResponse({ object: "chat_sessions", data: [] }),
          "/hecate/v1/chat/sessions/a1": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "a1",
                title: "S1",
                agent_id: "codex",
                workspace: "/tmp",
                status: "running",
              },
            }),
          "/hecate/v1/chat/sessions/a1/approvals?status=pending": () =>
            jsonResponse({
              object: "list",
              data: [approvalRow()],
            }),
          "/hecate/v1/chat/sessions/a1/approvals/ap-1/resolve": () =>
            jsonResponse({
              object: "chat_approval",
              data: approvalRow({
                status: "resolved",
                decision: "approve",
                scope: "once",
                path: "operator",
              }),
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() =>
        expect(result.current.state.pendingApprovalsBySessionID.get("a1")).toHaveLength(1),
      );

      await act(async () => {
        const ok = await result.current.actions.resolveChatApproval("a1", "ap-1", {
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
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/grants": () =>
            jsonResponse({
              object: "list",
              data: [
                {
                  id: "g1",
                  scope: "session",
                  adapter_id: "codex",
                  tool_kind: "fs",
                  decision: "approve",
                  granted_by: "operator",
                  granted_at: "2026-04-21T10:00:00Z",
                },
                {
                  id: "g2",
                  scope: "workspace_tool",
                  adapter_id: "codex",
                  tool_kind: "exec",
                  decision: "approve",
                  granted_by: "operator",
                  granted_at: "2026-04-21T10:01:00Z",
                },
              ],
            }),
          "/hecate/v1/chat/grants/g1": () => new Response(null, { status: 204 }),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.listChatGrants();
      });
      expect(result.current.state.chatGrants.map((g) => g.id)).toEqual(["g1", "g2"]);

      await act(async () => {
        const ok = await result.current.actions.deleteChatGrant("g1");
        expect(ok).toBe(true);
      });
      expect(result.current.state.chatGrants.map((g) => g.id)).toEqual(["g2"]);
    });
  });
});

describe("humanizeChatError", () => {
  it("rewrites the api-key-required message into actionable copy", async () => {
    const { humanizeChatError } = await import("./runtime-console-test-composer");
    expect(
      humanizeChatError("api key is required for cloud provider openai when stub mode is disabled"),
    ).toBe("openai has no API key. Open Connections and add one.");
  });

  it("preserves the provider name verbatim including hyphens / casing", async () => {
    const { humanizeChatError } = await import("./runtime-console-test-composer");
    expect(
      humanizeChatError(
        "api key is required for cloud provider together_ai when stub mode is disabled",
      ),
    ).toBe("together_ai has no API key. Open Connections and add one.");
  });

  it("passes unrelated errors through unchanged", async () => {
    const { humanizeChatError } = await import("./runtime-console-test-composer");
    expect(humanizeChatError("rate limit exceeded")).toBe("rate limit exceeded");
    expect(humanizeChatError("")).toBe("");
  });

  it("humanizes busy, unroutable model, and upstream provider errors", async () => {
    const { humanizeChatError } = await import("./runtime-console-test-composer");
    expect(humanizeChatError("Hecate Chat is already running for this chat session.")).toBe(
      "Hecate Chat is still working on this task. Open the task, resolve approval, or stop it before sending another message.",
    );
    expect(humanizeChatError("workspace is required")).toBe(
      "Choose a workspace before using Hecate Chat tools or External Agent.",
    );
    expect(humanizeChatError("tool calling support is unknown")).toBe(
      "This model is not marked as tool-capable. Hecate will send directly; choose a tool-capable model for task-backed turns.",
    );
    expect(
      humanizeChatError('route request: no provider supports explicit model "gpt-5.4-mini"'),
    ).toBe(
      "No configured provider can route to gpt-5.4-mini. Choose another model or open Connections to repair provider readiness.",
    );
    expect(humanizeChatError("no routable model for selected provider")).toBe(
      "No routable model is available. Choose another model or open Connections to add a provider, discover models, or check provider health.",
    );
    expect(humanizeChatError("Authentication required. Please run 'agent login' first.")).toBe(
      "The selected runtime is not signed in. Open Connections to repair or test readiness.",
    );
    expect(humanizeChatError("Internal error: Credit balance is too low")).toBe(
      "The selected runtime reported a billing or credit problem. Check its account, subscription, or API key balance.",
    );
    expect(humanizeChatError("ECONNREFUSED 127.0.0.1:11434")).toBe(
      "The selected provider is not reachable. Start the local provider app or check its endpoint URL.",
    );
    expect(humanizeChatError("upstream returned 401")).toBe(
      "The selected provider rejected the request with HTTP 401. Check credentials and account access.",
    );
    expect(humanizeChatError("upstream returned 503")).toBe(
      "The selected provider returned HTTP 503. Check that the provider is running and reachable.",
    );
    expect(humanizeChatError("upstream timeout")).toBe(
      "The selected provider did not respond before the timeout. Check that it is running, reachable, and not overloaded.",
    );
  });
});

function jsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function emptyStreamResponse(): Response {
  return new Response(
    new ReadableStream({
      start(controller) {
        controller.close();
      },
    }),
    {
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
    },
  );
}

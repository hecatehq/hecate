import { act, renderHook, waitFor, type RenderHookOptions } from "@testing-library/react";
import { type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApprovalsProvider } from "../app/state/approvals";
import {
  ChatProvider,
  composerDraftScope,
  useChat,
  type ChatCancellationOwner,
  type ChatState,
} from "../app/state/chat";
import {
  queuedChatDeletedProjectStorageKey,
  queuedChatDeletedSessionStorageKey,
  queuedChatMessageStorageKey,
  queuedChatMessageStorageKeyPrefix,
  readQueuedChatMessagesFromStorage,
} from "../app/state/queuedChatStorage";
import { ProvidersAndModelsProvider } from "../app/state/providersAndModels";
import { ProjectsProvider } from "../app/state/projects";
import { RetentionProvider } from "../app/state/retention";
import { RuntimeProvider } from "../app/state/runtime";
import { SettingsProvider } from "../app/state/settings";
import { UsageProvider } from "../app/state/usage";
import { useRuntimeConsole } from "./runtime-console-test-composer";
import type { ProjectRecord } from "../types/project";

function isQueuedItemStorageKey(key: string, id: string): boolean {
  const encodedID = encodeURIComponent(id);
  return (
    key.startsWith(queuedChatMessageStorageKeyPrefix) &&
    (key === `${queuedChatMessageStorageKeyPrefix}${encodedID}` || key.endsWith(`:${encodedID}`))
  );
}

// This suite is the canonical regression net for the composed
// slice + coordinator viewmodel — historically owned by
// useRuntimeConsole.test.tsx, now scoped to the test-only composer
// after the production facade was retired. Each renderHook call
// needs the full provider chain; the wrapper composes them so the
// test bodies don't have to thread it through every call.
function SliceProviders({
  children,
  chatInitialState,
  projects,
}: {
  children: ReactNode;
  chatInitialState?: Partial<ChatState>;
  projects?: ProjectRecord[];
}) {
  return (
    <RuntimeProvider>
      <UsageProvider>
        <ProvidersAndModelsProvider>
          <ProjectsProvider initialState={projects ? { projects } : undefined}>
            <ChatProvider initialState={chatInitialState}>
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

type RuntimeConsoleHookOptions = Omit<RenderHookOptions<unknown>, "wrapper"> & {
  chatInitialState?: Partial<ChatState>;
  projects?: ProjectRecord[];
};

function renderRuntimeConsoleHook(options?: RuntimeConsoleHookOptions) {
  const { chatInitialState, projects, ...renderOptions } = options ?? {};
  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <SliceProviders chatInitialState={chatInitialState} projects={projects}>
        {children}
      </SliceProviders>
    );
  }
  return renderHook(() => useRuntimeConsole(), { ...renderOptions, wrapper: Wrapper });
}

function renderRuntimeConsoleWithChatHook(options?: RuntimeConsoleHookOptions) {
  const { chatInitialState, projects, ...renderOptions } = options ?? {};
  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <SliceProviders chatInitialState={chatInitialState} projects={projects}>
        {children}
      </SliceProviders>
    );
  }
  return renderHook(
    () => {
      const runtimeConsole = useRuntimeConsole();
      return {
        runtimeConsole,
        chat: useChat(),
        // Flat aliases let an existing facade regression opt into direct
        // slice assertions without mechanically rewriting the whole test.
        state: runtimeConsole.state,
        actions: runtimeConsole.actions,
      };
    },
    { ...renderOptions, wrapper: Wrapper },
  );
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
    await act(async () => {
      result.current.actions.setAgentWorkspace("/workspace/current");
    });

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

  it("keeps image drafts when enabling Hecate tools or switching to External Agent mode", async () => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    const file = new File(["image"], "map.png", { type: "image/png" });

    act(() => {
      result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
    });
    await waitFor(() => expect(result.current.state.pendingChatAttachments).toHaveLength(1));

    act(() => result.current.actions.setChatToolsEnabled(true));
    expect(result.current.state.defaultChatToolsEnabled).toBe(true);
    expect(result.current.state.pendingChatAttachments).toHaveLength(1);
    expect(result.current.state.notice).toBeNull();

    act(() => result.current.actions.setChatTarget("external_agent"));
    expect(result.current.state.chatTarget).toBe("external_agent");
    expect(result.current.state.pendingChatAttachments).toHaveLength(1);
  });

  it.each([
    { label: "a declared non-image file", name: "report.pdf", type: "application/pdf" },
    { label: "a file with no declared type", name: "unknown.bin", type: "" },
  ])("blocks carrying $label into Hecate image mode", async ({ name, type }) => {
    const file = new File(["content"], name, { type });
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    act(() => {
      result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
    });
    await waitFor(() => expect(result.current.state.pendingChatAttachments).toHaveLength(1));
    act(() => result.current.actions.setChatTarget("agent"));

    expect(result.current.state.chatTarget).toBe("external_agent");
    expect(result.current.state.pendingChatAttachments).toEqual([{ id: "draft-1", file }]);
    expect(result.current.state.notice?.message).toBe(
      "Remove files without a declared PNG, JPEG, or WebP type before switching to Hecate Chat.",
    );
  });

  it("blocks switching away from an External Agent while its file turn owns attachments", async () => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    const { result } = renderRuntimeConsoleWithChatHook();
    await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));

    let token: number | null = null;
    act(() => {
      token = result.current.chat.actions.beginChatAttachmentTurn("external-session", 1);
    });
    expect(token).not.toBeNull();

    act(() => result.current.runtimeConsole.actions.setChatTarget("agent"));

    expect(result.current.runtimeConsole.state.chatTarget).toBe("external_agent");
    expect(result.current.runtimeConsole.state.notice?.message).toBe(
      "Wait for the attachment response before switching agents.",
    );
    act(() => result.current.chat.actions.finishChatAttachmentTurn(token!));
  });

  it("preserves the saved tools-enabled preference", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    expect(result.current.state.chatTarget).toBe("agent");
    expect(result.current.state.defaultChatToolsEnabled).toBe(false);
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
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
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
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
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
      workspace_mode: "persistent",
    });
    expect(createBody).not.toHaveProperty("workspace");
    expect(result.current.state.activeChatSessionID).toBe("chat_direct");
    // A tools-off Hecate chat resolves to chatTarget "agent" with a
    // per-session tools-disabled override on chatToolsEnabledBySessionID
    // — the two-axis encoding the slice persists today.
    expect(result.current.state.chatTarget).toBe("agent");
    expect(result.current.state.chatToolsEnabledBySessionID.get("chat_direct")).toBe(false);
  });

  it("creates an external-agent session from the selected agent and workspace", async () => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
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
      result.current.actions.setAgentWorkspace("/tmp/hecate-project");
    });

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createBody).toMatchObject({
      agent_id: "claude_code",
      workspace: "/tmp/hecate-project",
    });
    expect(createBody).not.toHaveProperty("workspace_mode");
    expect(result.current.state.activeChatSessionID).toBe("chat_1");
    expect(result.current.state.activeChatSession?.config_options?.[0]?.current_value).toBe(
      "sonnet",
    );
  });

  it("uses the new-chat agent selection instead of the active external session", async () => {
    window.localStorage.setItem("hecate.chatSessionID", "a1");
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
    await act(async () => {
      result.current.actions.setAgentWorkspace("/tmp/hecate");
    });
    expect(result.current.state.newChatAgentID).toBe("hecate");

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      model: "",
      workspace: "/tmp/hecate",
    });
    expect(createBody).not.toHaveProperty("adapter_id");
    expect(result.current.state.activeChatSessionID).toBe("chat_hecate");
    expect(result.current.state.activeChatSession?.agent_id).toBe("hecate");
  });

  it("creates a Hecate chat session from the default model when no model is preselected", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
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
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    window.localStorage.setItem("hecate.project", "proj_1");
    const project: ProjectRecord = {
      id: "proj_1",
      name: "Hecate",
      roots: [
        {
          id: "root_1",
          path: "/tmp/hecate-project",
          kind: "workspace",
          active: true,
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
      default_root_id: "root_1",
      default_workspace_mode: "in_place",
      created_at: "2026-05-21T10:00:00Z",
      updated_at: "2026-05-21T10:00:00Z",
    };
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/projects": () =>
          jsonResponse({
            object: "projects",
            data: [project],
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

    const { result } = renderRuntimeConsoleHook({ projects: [project] });
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      model: "gpt-4o-mini",
      project_id: "proj_1",
      workspace_mode: "in_place",
    });
    expect(createBody).not.toHaveProperty("workspace");
    expect(result.current.state.activeChatSession?.project_id).toBe("proj_1");
  });

  it("honors an explicit project scope when creating a Hecate chat", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    const project: ProjectRecord = {
      id: "proj_1",
      name: "Hecate",
      roots: [
        {
          id: "root_1",
          path: "/tmp/hecate",
          kind: "workspace",
          active: true,
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
      default_root_id: "root_1",
      created_at: "2026-05-21T10:00:00Z",
      updated_at: "2026-05-21T10:00:00Z",
    };
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/projects": () =>
          jsonResponse({
            object: "projects",
            data: [project],
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

    const { result } = renderRuntimeConsoleHook({ projects: [project] });
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession({ agentID: "hecate", projectID: "proj_1" });
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      // Tools-off Hecate creation keeps execution_mode as hecate_task
      // and records the direct provider call with tools_enabled=false.
      model: "gpt-4o-mini",
      project_id: "proj_1",
      workspace_mode: "persistent",
    });
    expect(createBody).not.toHaveProperty("workspace");
    expect(result.current.state.activeChatSession?.project_id).toBe("proj_1");
  });

  it("launches a scoped Hecate chat with requested provider and model via the created session", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.providerFilter", "openai");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/v1/models": () =>
          jsonResponse({
            object: "list",
            data: [
              {
                id: "gpt-4o-mini",
                owned_by: "openai",
                metadata: { provider: "openai", provider_kind: "cloud" },
              },
              {
                id: "llama3.1:8b",
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
                name: "openai",
                kind: "cloud",
                default_model: "gpt-4o-mini",
                models: ["gpt-4o-mini"],
                healthy: true,
                status: "healthy",
              },
              {
                name: "ollama",
                kind: "local",
                default_model: "llama3.1:8b",
                models: ["llama3.1:8b"],
                healthy: true,
                status: "healthy",
              },
            ],
          }),
        "/hecate/v1/settings": () =>
          jsonResponse({
            object: "settings",
            data: {
              backend: "memory",
              providers: [
                {
                  id: "openai",
                  name: "OpenAI",
                  kind: "cloud",
                  credential_configured: true,
                },
                {
                  id: "ollama",
                  name: "Ollama",
                  kind: "local",
                  credential_configured: true,
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
                id: "chat_scoped_ollama",
                title: "Hecate Chat",
                agent_id: "hecate",
                provider: createBody.provider,
                model: createBody.model,
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
        agentID: "hecate",
        provider: "ollama",
        model: "llama3.1:8b",
      });
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      provider: "ollama",
      model: "llama3.1:8b",
    });
    expect(result.current.state.activeChatSessionID).toBe("chat_scoped_ollama");
    expect(result.current.state.providerFilter).toBe("ollama");
    expect(result.current.state.model).toBe("llama3.1:8b");
  });

  it("creates a scoped Hecate chat with a launch title and editable draft", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_assignment",
                title: createBody.title,
                project_id: createBody.project_id,
                agent_id: "hecate",
                provider: createBody.provider,
                model: createBody.model,
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
        agentID: "hecate",
        projectID: "proj_1",
        provider: "ollama",
        model: "qwen2.5-coder",
        title: "Build cockpit UI - Software developer",
        draft: "Launch context\n\nProject: Hecate (proj_1)\n\nRequest:\n- ",
      });
    });

    expect(createBody).toMatchObject({
      title: "Build cockpit UI - Software developer",
      agent_id: "hecate",
      project_id: "proj_1",
      provider: "ollama",
      model: "qwen2.5-coder",
    });
    expect(result.current.state.activeChatSessionID).toBe("chat_assignment");
    expect(result.current.state.message).toBe(
      "Launch context\n\nProject: Hecate (proj_1)\n\nRequest:\n- ",
    );
  });

  it("carries an unsent draft into an ordinary new chat but honors an explicit empty draft", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    let sequence = 0;
    let resolveSecondCreate: ((response: Response) => void) | undefined;
    let secondCreateStarted = false;
    const session = (id: string) => ({
      object: "chat_session",
      data: {
        id,
        title: "Hecate chat",
        agent_id: "hecate",
        status: "idle",
        messages: [],
      },
    });
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            sequence += 1;
            if (sequence === 2) {
              secondCreateStarted = true;
              return new Promise<Response>((resolve) => {
                resolveSecondCreate = resolve;
              });
            }
            return jsonResponse(session(`chat_draft_${sequence}`));
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
        "/hecate/v1/chat/sessions/chat_draft_1": () => jsonResponse(session("chat_draft_1")),
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession({ agentID: "hecate", draft: "" });
    });
    act(() => {
      result.current.actions.setMessage("unfinished operator note");
    });
    let secondCreate!: Promise<void>;
    act(() => {
      // Match the sidebar's select-empty → create sequence in one event.
      void result.current.actions.selectChatSession("");
      secondCreate = result.current.actions.createChatSession({ agentID: "hecate" });
    });
    await waitFor(() => expect(secondCreateStarted).toBe(true));
    expect(result.current.state.message).toBe("unfinished operator note");

    act(() => {
      result.current.actions.setMessage("newer note typed while creating");
    });
    await act(async () => {
      await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });
    expect(sequence).toBe(2);
    expect(result.current.state.message).toBe("newer note typed while creating");
    await act(async () => {
      await result.current.actions.createChatSession({ agentID: "hecate" });
    });
    expect(sequence).toBe(2);

    await act(async () => {
      resolveSecondCreate?.(jsonResponse(session("chat_draft_2")));
      await secondCreate;
    });

    expect(result.current.state.activeChatSessionID).toBe("chat_draft_2");
    expect(result.current.state.message).toBe("newer note typed while creating");

    act(() => {
      result.current.actions.setMessage("");
    });
    await act(async () => {
      await result.current.actions.selectChatSession("chat_draft_1");
    });
    expect(result.current.state.message).toBe("");

    act(() => {
      result.current.actions.setMessage("do not leak this draft");
    });
    await act(async () => {
      await result.current.actions.createChatSession({ agentID: "hecate", draft: "" });
    });

    expect(result.current.state.activeChatSessionID).toBe("chat_draft_3");
    expect(result.current.state.message).toBe("");
  });

  it("isolates a scoped launch draft from the selected chat and preserves both sides on failure", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    let resolveCreate: ((response: Response) => void) | undefined;
    let createStarted = false;
    const sourceSession = {
      id: "chat_source",
      title: "Existing conversation",
      agent_id: "hecate",
      status: "idle" as const,
      workspace: "/workspace",
      message_count: 0,
      messages: [],
    };
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createStarted = true;
            return new Promise<Response>((resolve) => {
              resolveCreate = resolve;
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [sourceSession] });
        },
        "/hecate/v1/chat/sessions/chat_source": () =>
          jsonResponse({ object: "chat_session", data: sourceSession }),
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        activeChatSessionID: sourceSession.id,
        activeChatSession: sourceSession,
        chatSessions: [sourceSession],
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    act(() => {
      result.current.actions.setMessage("unfinished source draft");
    });

    let createPromise!: Promise<void>;
    act(() => {
      createPromise = result.current.actions.createChatSession({
        agentID: "hecate",
        projectID: "proj_launch",
        draft: "Scoped project launch",
      });
    });
    await waitFor(() => expect(createStarted).toBe(true));

    expect(result.current.state.activeChatSessionID).toBe("");
    expect(result.current.state.activeChatSession).toBeNull();
    expect(result.current.state.message).toBe("Scoped project launch");

    act(() => {
      result.current.actions.setMessage("Edited scoped launch while creating");
    });
    await act(async () => {
      resolveCreate?.(
        new Response(JSON.stringify({ error: { message: "creation unavailable" } }), {
          status: 503,
          headers: { "Content-Type": "application/json" },
        }),
      );
      await createPromise;
    });

    expect(result.current.state.activeChatSessionID).toBe("");
    expect(result.current.state.activeChatSession).toBeNull();
    expect(result.current.state.message).toBe("Edited scoped launch while creating");

    await act(async () => {
      await result.current.actions.selectChatSession(sourceSession.id);
    });
    expect(result.current.state.activeChatSessionID).toBe(sourceSession.id);
    expect(result.current.state.message).toBe("unfinished source draft");
  });

  it("keeps the latest pending-create edit when another chat preempts the response", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    let resolveCreate: ((response: Response) => void) | undefined;
    let createStarted = false;
    const chatSession = (id: string, title: string) => ({
      id,
      title,
      agent_id: "hecate",
      status: "idle" as const,
      workspace: "/workspace",
      message_count: 0,
      messages: [],
    });
    const source = chatSession("chat_source", "Source chat");
    const other = chatSession("chat_other", "Other chat");
    const created = chatSession("chat_created", "Created chat");
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createStarted = true;
            return new Promise<Response>((resolve) => {
              resolveCreate = resolve;
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [source, other] });
        },
        "/hecate/v1/chat/sessions/chat_other": () =>
          jsonResponse({ object: "chat_session", data: other }),
        "/hecate/v1/chat/sessions/chat_created": () =>
          jsonResponse({ object: "chat_session", data: created }),
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        activeChatSessionID: source.id,
        activeChatSession: source,
        chatSessions: [source, other],
        agentWorkspace: "/workspace",
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    act(() => {
      result.current.actions.setMessage("carried source draft");
    });

    let createPromise!: Promise<void>;
    act(() => {
      createPromise = result.current.actions.createChatSession({ agentID: "hecate" });
    });
    await waitFor(() => expect(createStarted).toBe(true));
    act(() => {
      result.current.actions.setMessage("latest pending edit");
    });
    await act(async () => {
      await result.current.actions.selectChatSession(other.id);
    });

    await act(async () => {
      resolveCreate?.(jsonResponse({ object: "chat_session", data: created }));
      await createPromise;
    });
    expect(result.current.state.activeChatSessionID).toBe(other.id);

    await act(async () => {
      await result.current.actions.selectChatSession(created.id);
    });
    expect(result.current.state.message).toBe("latest pending edit");
  });

  it("restores an edited pending draft after selection preempts a failed create", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    let resolveFirstCreate: ((response: Response) => void) | undefined;
    let createCount = 0;
    const chatSession = (id: string, title: string) => ({
      id,
      title,
      agent_id: "hecate",
      status: "idle" as const,
      workspace: "/workspace",
      message_count: 0,
      messages: [],
    });
    const source = chatSession("chat_source", "Source chat");
    const other = chatSession("chat_other", "Other chat");
    const recovered = chatSession("chat_recovered", "Recovered chat");
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createCount += 1;
            if (createCount === 1) {
              return new Promise<Response>((resolve) => {
                resolveFirstCreate = resolve;
              });
            }
            return jsonResponse({ object: "chat_session", data: recovered });
          }
          return jsonResponse({ object: "chat_sessions", data: [source, other] });
        },
        "/hecate/v1/chat/sessions/chat_other": () =>
          jsonResponse({ object: "chat_session", data: other }),
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        activeChatSessionID: source.id,
        activeChatSession: source,
        chatSessions: [source, other],
        agentWorkspace: "/workspace",
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    act(() => {
      result.current.actions.setMessage("draft before create");
    });

    let firstCreate!: Promise<void>;
    act(() => {
      firstCreate = result.current.actions.createChatSession({ agentID: "hecate" });
    });
    await waitFor(() => expect(createCount).toBe(1));
    act(() => {
      result.current.actions.setMessage("edited draft worth recovering");
    });
    await act(async () => {
      await result.current.actions.selectChatSession(other.id);
    });
    expect(result.current.state.message).toBe("");

    await act(async () => {
      resolveFirstCreate?.(
        new Response(JSON.stringify({ error: { message: "creation unavailable" } }), {
          status: 503,
          headers: { "Content-Type": "application/json" },
        }),
      );
      await firstCreate;
    });
    expect(result.current.state.activeChatSessionID).toBe(other.id);

    await act(async () => {
      await result.current.actions.createChatSession({ agentID: "hecate" });
    });
    expect(createCount).toBe(2);
    expect(result.current.state.activeChatSessionID).toBe(recovered.id);
    expect(result.current.state.message).toBe("edited draft worth recovering");
  });

  it("does not restore a failed project launch into a different project", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) =>
          init?.method === "POST"
            ? jsonResponse({
                object: "chat_session",
                data: {
                  id: "chat_project_b",
                  title: "Project B chat",
                  project_id: "proj_b",
                  agent_id: "hecate",
                  status: "idle",
                  messages: [],
                  provider: "",
                  model: "gpt-4o-mini",
                },
              })
            : jsonResponse({ object: "chat_sessions", data: [] }),
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        recoverableComposerDraft: {
          id: 7,
          content: "Project A assignment launch context",
          scope: composerDraftScope({
            projectID: "proj_a",
            agentID: "hecate",
            provider: "auto",
            model: "gpt-4o-mini",
          }),
        },
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));

    await act(async () => {
      await result.current.actions.createChatSession({
        agentID: "hecate",
        projectID: "proj_b",
      });
    });

    expect(result.current.state.activeChatSessionID).toBe("chat_project_b");
    expect(result.current.state.message).toBe("");
  });

  it("blocks Send when a bound recovery belongs to another project", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    window.localStorage.setItem("hecate.project", "proj_b");
    const projectB: ProjectRecord = {
      id: "proj_b",
      name: "Project B",
      roots: [],
      created_at: "2026-07-13T10:00:00Z",
      updated_at: "2026-07-13T10:00:00Z",
    };
    let sessionCreateCount = 0;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/projects": () => jsonResponse({ object: "projects", data: [projectB] }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") sessionCreateCount += 1;
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      projects: [projectB],
      chatInitialState: {
        recoverableComposerDraft: {
          id: 17,
          content: "Project A launch context",
          scope: composerDraftScope({
            projectID: "proj_a",
            agentID: "hecate",
            provider: "auto",
            model: "gpt-4o-mini",
          }),
        },
        activeRecoverableComposerDraftID: 17,
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));
    act(() => {
      result.current.actions.setMessage("Project A launch context");
    });

    await act(async () => {
      await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });

    expect(sessionCreateCount).toBe(0);
    expect(result.current.state.message).toBe("");
    expect(result.current.state.recoverableComposerDraft).toMatchObject({
      id: 17,
      content: "Project A launch context",
      scope: { projectID: "proj_a" },
    });
    expect(result.current.state.activeRecoverableComposerDraftID).toBeNull();
  });

  it("blocks Send when a bound recovery belongs to another provider route", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    let sessionCreateCount = 0;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") sessionCreateCount += 1;
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        providerFilter: "anthropic",
        model: "claude-sonnet-4-6",
        recoverableComposerDraft: {
          id: 19,
          content: "OpenAI-specific launch context",
          scope: composerDraftScope({
            agentID: "hecate",
            provider: "openai",
            model: "gpt-4o-mini",
          }),
        },
        activeRecoverableComposerDraftID: 19,
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    act(() => {
      result.current.actions.setMessage("OpenAI-specific launch context");
    });

    await act(async () => {
      await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });

    expect(sessionCreateCount).toBe(0);
    expect(result.current.state.message).toBe("");
    expect(result.current.state.recoverableComposerDraft).toMatchObject({
      id: 19,
      content: "OpenAI-specific launch context",
      scope: { provider: "openai", model: "gpt-4o-mini" },
    });
  });

  it.each([
    ["edits", "Rewritten recovery"],
    ["clears", ""],
  ])("consumes owned recovery when the operator %s it before success", async (_label, edit) => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let createCount = 0;
    let resolveRecoveredCreate: ((response: Response) => void) | undefined;
    const session = (id: string) => ({
      object: "chat_session",
      data: {
        id,
        title: "Recovered chat",
        agent_id: "hecate",
        status: "idle",
        messages: [],
        provider: "",
        model: "gpt-4o-mini",
      },
    });
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method !== "POST") {
            return jsonResponse({ object: "chat_sessions", data: [] });
          }
          createCount += 1;
          if (createCount === 1) {
            return new Promise<Response>((resolve) => {
              resolveRecoveredCreate = resolve;
            });
          }
          return jsonResponse(session("chat_after_recovery"));
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        recoverableComposerDraft: {
          id: 11,
          content: "Recovered launch context",
          scope: composerDraftScope({
            agentID: "hecate",
            provider: "auto",
            model: "gpt-4o-mini",
          }),
        },
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));

    let recoveredCreate!: Promise<void>;
    act(() => {
      recoveredCreate = result.current.actions.createChatSession({ agentID: "hecate" });
    });
    await waitFor(() => expect(createCount).toBe(1));
    expect(result.current.state.message).toBe("Recovered launch context");

    act(() => {
      result.current.actions.setMessage(edit);
    });
    await waitFor(() => expect(result.current.state.message).toBe(edit));
    await act(async () => {
      resolveRecoveredCreate?.(jsonResponse(session("chat_recovered")));
      await recoveredCreate;
    });

    act(() => {
      result.current.actions.setMessage("");
    });
    await waitFor(() => expect(result.current.state.message).toBe(""));
    act(() => {
      result.current.actions.startNewChat();
    });
    await act(async () => {
      await result.current.actions.createChatSession({ agentID: "hecate" });
    });

    expect(createCount).toBe(2);
    expect(result.current.state.activeChatSessionID).toBe("chat_after_recovery");
    expect(result.current.state.message).toBe("");
  });

  it("serializes implicit session creation when a detached draft is submitted twice", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let createCount = 0;
    let messageCount = 0;
    let createBody: unknown = null;
    let resolveCreate: ((response: Response) => void) | undefined;
    const createdSession = {
      id: "chat_implicit",
      title: "Recovered prompt",
      agent_id: "hecate",
      status: "idle" as const,
      workspace: "",
      message_count: 0,
      messages: [],
      provider: "",
      model: "gpt-4o-mini",
    };
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createCount += 1;
            createBody = JSON.parse(String(init.body ?? "{}"));
            return new Promise<Response>((resolve) => {
              resolveCreate = resolve;
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
        "/hecate/v1/chat/sessions/chat_implicit/stream": () => emptyStreamResponse(),
        "/hecate/v1/chat/sessions/chat_implicit/messages": () => {
          messageCount += 1;
          return jsonResponse({
            object: "chat_session",
            data: {
              ...createdSession,
              status: "completed",
              messages: [
                {
                  id: "message_implicit",
                  role: "user",
                  content: "Recovered prompt",
                },
              ],
            },
          });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));
    act(() => {
      result.current.actions.setMessage("Recovered prompt");
    });

    let firstSubmit!: Promise<void>;
    let secondSubmit!: Promise<void>;
    act(() => {
      firstSubmit = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      secondSubmit = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });
    await waitFor(() => expect(createCount).toBe(1));
    expect(result.current.state.chatCreating).toBe(true);

    await act(async () => {
      resolveCreate?.(jsonResponse({ object: "chat_session", data: createdSession }));
      await Promise.all([firstSubmit, secondSubmit]);
    });

    expect(createCount).toBe(1);
    expect(messageCount).toBe(1);
    expect(createBody).toMatchObject({ agent_id: "hecate", workspace_mode: "persistent" });
    expect(result.current.state.activeChatSessionID).toBe("chat_implicit");
  });

  it.each([
    ["without a later edit", "", null],
    ["with a later edit", "Follow-up preserved", "Follow-up preserved"],
  ])(
    "keeps a newer selection authoritative %s while implicit creation finishes",
    async (_label, laterEdit, expectedRecovery) => {
      window.localStorage.setItem("hecate.chatToolsEnabled", "false");
      window.localStorage.setItem("hecate.model", "gpt-4o-mini");
      let resolveCreate: ((response: Response) => void) | undefined;
      let createStarted = false;
      let sentContent = "";
      const selectedSession = {
        id: "chat_selected",
        title: "Selected chat",
        agent_id: "hecate",
        status: "idle" as const,
        messages: [],
        provider: "",
        model: "gpt-4o-mini",
      };
      const createdSession = {
        id: "chat_background",
        title: "Background chat",
        agent_id: "hecate",
        status: "idle" as const,
        messages: [],
        provider: "",
        model: "gpt-4o-mini",
      };
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": (init) => {
            if (init?.method === "POST") {
              createStarted = true;
              return new Promise<Response>((resolve) => {
                resolveCreate = resolve;
              });
            }
            return jsonResponse({ object: "chat_sessions", data: [selectedSession] });
          },
          "/hecate/v1/chat/sessions/chat_selected": () =>
            jsonResponse({ object: "chat_session", data: selectedSession }),
          "/hecate/v1/chat/sessions/chat_background/stream": () => emptyStreamResponse(),
          "/hecate/v1/chat/sessions/chat_background/messages": (init) => {
            sentContent = JSON.parse(String(init?.body ?? "{}")).content;
            return jsonResponse({
              object: "chat_session",
              data: {
                ...createdSession,
                status: "completed",
                messages: [{ id: "sent_background", role: "user", content: sentContent }],
              },
            });
          },
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));
      act(() => {
        result.current.actions.setMessage("Original submitted prompt");
      });

      let submitPromise!: Promise<void>;
      act(() => {
        submitPromise = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });
      await waitFor(() => expect(createStarted).toBe(true));
      await waitFor(() => expect(result.current.state.message).toBe(""));
      if (laterEdit) {
        act(() => {
          result.current.actions.setMessage(laterEdit);
        });
      }
      await act(async () => {
        await result.current.actions.selectChatSession(selectedSession.id);
      });
      expect(result.current.state.activeChatSessionID).toBe(selectedSession.id);

      await act(async () => {
        resolveCreate?.(jsonResponse({ object: "chat_session", data: createdSession }));
        await submitPromise;
      });

      expect(sentContent).toBe("Original submitted prompt");
      expect(result.current.state.activeChatSessionID).toBe(selectedSession.id);
      expect(result.current.state.activeChatSession?.id).toBe(selectedSession.id);
      expect(
        result.current.state.chatSessions.some((entry) => entry.id === createdSession.id),
      ).toBe(true);
      expect(result.current.state.chatTurnSessionID).toBe("");
      expect(result.current.state.recoverableComposerDraft?.content ?? null).toBe(expectedRecovery);
    },
  );

  it("queues a follow-up after implicit allocation while the first message is pending", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let resolveCreate: ((response: Response) => void) | undefined;
    let resolveFirstMessage: ((response: Response) => void) | undefined;
    const messageBodies: string[] = [];
    const createdSession = {
      id: "chat_serial_turn",
      title: "First prompt",
      agent_id: "hecate",
      status: "idle" as const,
      messages: [],
      provider: "",
      model: "gpt-4o-mini",
    };
    const completedSession = (content: string) => {
      const committedMessageID = `sent_${messageBodies.length}`;
      return {
        object: "chat_session",
        data: {
          ...createdSession,
          status: "completed",
          messages: [
            { id: committedMessageID, role: "user", content },
            {
              id: `assistant_${messageBodies.length}`,
              role: "assistant",
              content: "Done.",
              status: "completed",
            },
          ],
        },
        message_request: { committed_message_id: committedMessageID },
      };
    };
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) =>
          init?.method === "POST"
            ? new Promise<Response>((resolve) => {
                resolveCreate = resolve;
              })
            : jsonResponse({ object: "chat_sessions", data: [] }),
        "/hecate/v1/chat/sessions/chat_serial_turn/stream": () => emptyStreamResponse(),
        "/hecate/v1/chat/sessions/chat_serial_turn/messages": (init) => {
          const content = JSON.parse(String(init?.body ?? "{}")).content as string;
          messageBodies.push(content);
          if (messageBodies.length === 1) {
            return new Promise<Response>((resolve) => {
              resolveFirstMessage = resolve;
            });
          }
          return jsonResponse(completedSession(content));
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));
    act(() => {
      result.current.actions.setMessage("First prompt");
    });

    let firstSubmit!: Promise<void>;
    act(() => {
      firstSubmit = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });
    await waitFor(() => expect(result.current.state.chatCreating).toBe(true));
    act(() => {
      resolveCreate?.(jsonResponse({ object: "chat_session", data: createdSession }));
    });
    await waitFor(() => expect(messageBodies).toEqual(["First prompt"]));
    expect(result.current.state.activeChatSessionID).toBe(createdSession.id);
    expect(result.current.state.chatCreating).toBe(false);
    expect(result.current.state.chatLoading).toBe(true);
    expect(result.current.state.chatTurnSessionID).toBe(createdSession.id);
    expect(result.current.state.chatTurnActive).toBe(true);

    act(() => {
      result.current.actions.setMessage("Follow-up prompt");
    });
    await act(async () => {
      await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });
    expect(messageBodies).toEqual(["First prompt"]);
    expect(result.current.state.queuedChatMessages).toHaveLength(1);
    expect(result.current.state.queuedChatMessages[0].content).toBe("Follow-up prompt");

    await act(async () => {
      resolveFirstMessage?.(jsonResponse(completedSession("First prompt")));
      await firstSubmit;
    });
    await waitFor(() => expect(messageBodies).toEqual(["First prompt", "Follow-up prompt"]));
    await waitFor(() => expect(result.current.state.queuedChatMessages).toHaveLength(0));
    await waitFor(() => expect(result.current.state.chatLoading).toBe(false));
  });

  it("restores the first prompt when its created session rejects the message", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let sessionCreateCount = 0;
    let messageCount = 0;
    const createdSession = {
      id: "chat_retry_message",
      title: "Retry prompt",
      agent_id: "hecate",
      status: "idle" as const,
      workspace: "",
      message_count: 0,
      messages: [],
      provider: "",
      model: "gpt-4o-mini",
    };
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            sessionCreateCount += 1;
            return jsonResponse({ object: "chat_session", data: createdSession });
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
        "/hecate/v1/chat/sessions/chat_retry_message/stream": () => emptyStreamResponse(),
        "/hecate/v1/chat/sessions/chat_retry_message/messages": () => {
          messageCount += 1;
          if (messageCount === 1) {
            return new Response(JSON.stringify({ error: { message: "message unavailable" } }), {
              status: 503,
              headers: { "Content-Type": "application/json" },
            });
          }
          return jsonResponse({
            object: "chat_session",
            data: {
              ...createdSession,
              status: "completed",
              messages: [{ id: "message_retry", role: "user", content: "Retry prompt" }],
            },
          });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));
    act(() => {
      result.current.actions.setMessage("Retry prompt");
    });

    await act(async () => {
      await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });

    expect(sessionCreateCount).toBe(1);
    expect(messageCount).toBe(1);
    expect(result.current.state.activeChatSessionID).toBe(createdSession.id);
    expect(result.current.state.message).toBe("Retry prompt");
    expect(result.current.state.activeChatSession?.messages).toEqual([]);
    expect(result.current.state.recoverableComposerDraft).toBeNull();
    expect(result.current.state.chatCreating).toBe(false);
    expect(result.current.state.chatTurnActive).toBe(false);
    expect(result.current.state.chatLoading).toBe(false);

    await act(async () => {
      await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });

    expect(sessionCreateCount).toBe(1);
    expect(messageCount).toBe(2);
    expect(result.current.state.message).toBe("");
    expect(result.current.state.activeChatSession?.messages?.[0]?.content).toBe("Retry prompt");
  });

  it("restores a failed existing-session prompt after selecting another chat", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let rejectMessage: ((response: Response) => void) | undefined;
    let messageStarted = false;
    const sourceSession = {
      id: "chat_failed_source",
      title: "Failed source",
      agent_id: "hecate",
      status: "idle" as const,
      workspace: "",
      message_count: 0,
      messages: [],
      provider: "",
      model: "gpt-4o-mini",
    };
    const selectedSession = {
      ...sourceSession,
      id: "chat_selected_during_failure",
      title: "Selected while waiting",
    };
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": () =>
          jsonResponse({ object: "chat_sessions", data: [sourceSession, selectedSession] }),
        "/hecate/v1/chat/sessions/chat_failed_source": () =>
          jsonResponse({ object: "chat_session", data: sourceSession }),
        "/hecate/v1/chat/sessions/chat_selected_during_failure": () =>
          jsonResponse({ object: "chat_session", data: selectedSession }),
        "/hecate/v1/chat/sessions/chat_failed_source/stream": () => emptyStreamResponse(),
        "/hecate/v1/chat/sessions/chat_failed_source/messages": () => {
          messageStarted = true;
          return new Promise<Response>((resolve) => {
            rejectMessage = resolve;
          });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        activeChatSessionID: sourceSession.id,
        activeChatSession: sourceSession,
        chatSessions: [sourceSession, selectedSession],
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));
    act(() => result.current.actions.setMessage("Submitted prompt A"));

    let submitPromise!: Promise<void>;
    act(() => {
      submitPromise = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });
    await waitFor(() => expect(messageStarted).toBe(true));
    await act(async () => {
      await result.current.actions.selectChatSession(selectedSession.id);
    });
    act(() => result.current.actions.setMessage("Draft in selected chat B"));

    await act(async () => {
      rejectMessage?.(
        new Response(JSON.stringify({ error: { message: "message unavailable" } }), {
          status: 503,
          headers: { "Content-Type": "application/json" },
        }),
      );
      await submitPromise;
    });

    expect(result.current.state.activeChatSessionID).toBe(selectedSession.id);
    expect(result.current.state.message).toBe("Draft in selected chat B");
    expect(result.current.state.savedComposerDraftsBySessionID.get(sourceSession.id)).toEqual([
      "Submitted prompt A",
    ]);
    expect(result.current.state.notice).toEqual({
      kind: "error",
      message: "A message was not sent in “Failed source”. It is saved there.",
    });

    await act(async () => {
      await result.current.actions.selectChatSession(sourceSession.id);
    });
    expect(result.current.state.message).toBe("Submitted prompt A");
    expect(result.current.state.savedComposerDraftsBySessionID.has(sourceSession.id)).toBe(false);

    await act(async () => {
      await result.current.actions.selectChatSession(selectedSession.id);
    });
    expect(result.current.state.message).toBe("Draft in selected chat B");
  });

  it.each(["before", "during"] as const)(
    "does not let dashboard hydration started %s an image turn replace its active snapshot",
    async (timing) => {
      window.localStorage.setItem("hecate.chatSessionID", "chat_a");
      let delaySessionList = false;
      let delayedListRequested = 0;
      let releaseSessionList: (() => void) | undefined;
      let sessionListGate = Promise.resolve();
      const staleSession = {
        id: "chat_a",
        title: "A",
        agent_id: "hecate",
        status: "completed",
        provider: "openai",
        model: "gpt-4o-mini",
        workspace: "/workspace/chat_a",
        messages: [],
        created_at: "2026-07-13T10:00:00Z",
        updated_at: "2026-07-13T10:00:00Z",
      };
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": async () => {
            if (delaySessionList) {
              delayedListRequested += 1;
              await sessionListGate;
            }
            return jsonResponse({
              object: "chat_sessions",
              data: [{ ...staleSession, message_count: 0 }],
            });
          },
          "/hecate/v1/chat/sessions/chat_a": () =>
            jsonResponse({ object: "chat_session", data: staleSession }),
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await waitFor(() =>
        expect(result.current.runtimeConsole.state.activeChatSession?.id).toBe("chat_a"),
      );

      const optimisticSession = {
        ...staleSession,
        messages: [
          {
            id: "pending-image-message",
            role: "user" as const,
            content: "inspect this map",
            created_at: "2026-07-13T10:00:01Z",
          },
        ],
      };
      sessionListGate = new Promise<void>((resolve) => {
        releaseSessionList = resolve;
      });
      delaySessionList = true;

      let dashboardLoad!: Promise<void>;
      let attachmentTurnStarted = false;
      let attachmentTurnToken: number | null = null;
      const beginAttachmentTurn = () => {
        act(() => {
          attachmentTurnToken = result.current.chat.actions.beginChatAttachmentTurn("chat_a", 1);
          attachmentTurnStarted = attachmentTurnToken !== null;
          result.current.chat.actions.setActiveChatSession(optimisticSession);
        });
      };
      if (timing === "during") beginAttachmentTurn();
      act(() => {
        dashboardLoad = result.current.runtimeConsole.actions.loadDashboard();
      });
      await waitFor(() => expect(delayedListRequested).toBe(1));
      if (timing === "before") beginAttachmentTurn();
      expect(attachmentTurnStarted).toBe(true);
      if (timing === "during") {
        act(() => {
          if (attachmentTurnToken !== null) {
            result.current.chat.actions.finishChatAttachmentTurn(attachmentTurnToken);
          }
        });
      }

      await act(async () => {
        releaseSessionList?.();
        await dashboardLoad;
      });

      expect(result.current.runtimeConsole.state.activeChatSession?.messages).toEqual([
        expect.objectContaining({ id: "pending-image-message" }),
      ]);
      if (timing === "before") {
        act(() => {
          if (attachmentTurnToken !== null) {
            result.current.chat.actions.finishChatAttachmentTurn(attachmentTurnToken);
          }
        });
      }
    },
  );

  it("does not let dashboard hydration started during session creation replace the created session", async () => {
    window.localStorage.setItem("hecate.chatSessionID", "chat_a");
    let createCount = 0;
    let delaySessionList = false;
    let delayedListRequested = 0;
    let releaseCreate: ((response: Response) => void) | undefined;
    let releaseSessionList: (() => void) | undefined;
    const deferredCreate = new Promise<Response>((resolve) => {
      releaseCreate = resolve;
    });
    const sessionListGate = new Promise<void>((resolve) => {
      releaseSessionList = resolve;
    });
    const staleSession = {
      id: "chat_a",
      title: "A",
      agent_id: "hecate",
      status: "completed",
      provider: "openai",
      model: "gpt-4o-mini",
      messages: [],
      created_at: "2026-07-13T10:00:00Z",
      updated_at: "2026-07-13T10:00:00Z",
    };
    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input);
      if (url === "/hecate/v1/chat/sessions" && init?.method === "POST") {
        createCount += 1;
        return deferredCreate;
      }
      if (url === "/hecate/v1/chat/sessions") {
        if (delaySessionList) {
          delayedListRequested += 1;
          await sessionListGate;
        }
        return jsonResponse({
          object: "chat_sessions",
          data: [{ ...staleSession, message_count: 0 }],
        });
      }
      if (url === "/hecate/v1/chat/sessions/chat_a") {
        return jsonResponse({ object: "chat_session", data: staleSession });
      }
      return defaultBackendMock()(input, init);
    });

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("chat_a"));

    let create!: Promise<void>;
    act(() => {
      create = result.current.actions.createChatSession({ agentID: "hecate" });
    });
    await waitFor(() => expect(createCount).toBe(1));

    delaySessionList = true;
    let dashboardLoad!: Promise<void>;
    act(() => {
      dashboardLoad = result.current.actions.loadDashboard();
    });
    await waitFor(() => expect(delayedListRequested).toBe(1));

    await act(async () => {
      releaseCreate?.(
        jsonResponse({
          object: "chat_session",
          data: {
            ...staleSession,
            id: "chat_new",
            title: "New chat",
            created_at: "2026-07-13T10:00:01Z",
            updated_at: "2026-07-13T10:00:01Z",
          },
        }),
      );
      await create;
    });
    expect(result.current.state.activeChatSessionID).toBe("chat_new");
    expect(result.current.state.activeChatSession?.id).toBe("chat_new");

    await act(async () => {
      releaseSessionList?.();
      await dashboardLoad;
    });

    expect(result.current.state.activeChatSessionID).toBe("chat_new");
    expect(result.current.state.activeChatSession?.id).toBe("chat_new");
  });

  it.each(["Later draft B", "Submitted prompt A"])(
    "keeps a failed submitted prompt separate from a newer draft (%s)",
    async (laterDraft) => {
      window.localStorage.setItem("hecate.chatToolsEnabled", "false");
      window.localStorage.setItem("hecate.model", "gpt-4o-mini");
      let rejectMessage: ((response: Response) => void) | undefined;
      const activeSession = {
        id: "chat_failed_with_newer_draft",
        title: "Failed with newer draft",
        agent_id: "hecate",
        status: "idle" as const,
        workspace: "",
        message_count: 0,
        messages: [],
        provider: "",
        model: "gpt-4o-mini",
      };
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () =>
            jsonResponse({ object: "chat_sessions", data: [activeSession] }),
          "/hecate/v1/chat/sessions/chat_failed_with_newer_draft/stream": () =>
            emptyStreamResponse(),
          "/hecate/v1/chat/sessions/chat_failed_with_newer_draft/messages": () =>
            new Promise<Response>((resolve) => {
              rejectMessage = resolve;
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook({
        chatInitialState: {
          activeChatSessionID: activeSession.id,
          activeChatSession: activeSession,
          chatSessions: [activeSession],
        },
      });
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));
      act(() => result.current.actions.setMessage("Submitted prompt A"));

      let submitPromise!: Promise<void>;
      act(() => {
        submitPromise = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });
      await waitFor(() => expect(rejectMessage).toBeDefined());
      act(() => result.current.actions.setMessage(laterDraft));
      await act(async () => {
        rejectMessage?.(
          new Response(JSON.stringify({ error: { message: "message unavailable" } }), {
            status: 503,
            headers: { "Content-Type": "application/json" },
          }),
        );
        await submitPromise;
      });

      expect(result.current.state.message).toBe(laterDraft);
      expect(result.current.state.savedComposerDraftsBySessionID.get(activeSession.id)).toEqual([
        "Submitted prompt A",
      ]);
      act(() => {
        expect(result.current.actions.restoreSavedComposerDraft(activeSession.id)).toBe(true);
      });
      expect(result.current.state.message).toBe("Submitted prompt A");
      expect(result.current.state.savedComposerDraftsBySessionID.get(activeSession.id)).toEqual([
        laterDraft,
      ]);
    },
  );

  it("consumes one matching saved draft after a successful manual retry", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    const activeSession = {
      id: "chat_manual_saved_retry",
      title: "Manual saved retry",
      agent_id: "hecate",
      status: "idle" as const,
      workspace: "",
      message_count: 0,
      messages: [],
      provider: "",
      model: "gpt-4o-mini",
    };
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": () =>
          jsonResponse({ object: "chat_sessions", data: [activeSession] }),
        "/hecate/v1/chat/sessions/chat_manual_saved_retry/stream": () => emptyStreamResponse(),
        "/hecate/v1/chat/sessions/chat_manual_saved_retry/messages": () =>
          jsonResponse({
            object: "chat_session",
            data: {
              ...activeSession,
              status: "completed",
              message_count: 1,
              messages: [{ id: "saved_retry_sent", role: "user", content: "Retry saved A" }],
            },
          }),
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        activeChatSessionID: activeSession.id,
        activeChatSession: activeSession,
        chatSessions: [activeSession],
        savedComposerDraftsBySessionID: new Map([
          [activeSession.id, ["Retry saved A", "Keep saved B"]],
        ]),
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));
    act(() => result.current.actions.setMessage("Retry saved A"));

    await act(async () => {
      await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });

    expect(result.current.state.message).toBe("");
    expect(result.current.state.savedComposerDraftsBySessionID.get(activeSession.id)).toEqual([
      "Keep saved B",
    ]);
  });

  it("consumes a matching saved draft after its queued retry succeeds", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let messagePostCount = 0;
    const activeSession = {
      id: "chat_queued_saved_retry",
      title: "Queued saved retry",
      agent_id: "hecate",
      status: "idle" as const,
      workspace: "",
      message_count: 0,
      messages: [],
      provider: "",
      model: "gpt-4o-mini",
    };
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": () =>
          jsonResponse({ object: "chat_sessions", data: [activeSession] }),
        "/hecate/v1/chat/sessions/chat_queued_saved_retry/stream": () => emptyStreamResponse(),
        "/hecate/v1/chat/sessions/chat_queued_saved_retry/messages": () => {
          messagePostCount += 1;
          return jsonResponse({
            object: "chat_session",
            data: {
              ...activeSession,
              status: "completed",
              message_count: 1,
              messages: [
                { id: "queued_retry_sent", role: "user", content: "Retry saved A" },
                {
                  id: "queued_retry_answer",
                  role: "assistant",
                  content: "Done.",
                  status: "completed",
                },
              ],
            },
            message_request: { committed_message_id: "queued_retry_sent" },
          });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        activeChatSessionID: activeSession.id,
        activeChatSession: activeSession,
        chatSessions: [activeSession],
        queuedChatMessages: [
          {
            id: "queued_saved_retry",
            session_id: activeSession.id,
            content: "Retry saved A",
            execution_mode: "hecate_task",
            tools_enabled: false,
            provider_filter: "auto",
            model: "gpt-4o-mini",
            workspace: "",
            system_prompt: "",
            agent_id: "hecate",
            created_at: "2026-07-14T10:00:00Z",
          },
        ],
        savedComposerDraftsBySessionID: new Map([
          [activeSession.id, ["Retry saved A", "Keep saved B"]],
        ]),
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(messagePostCount).toBe(1));
    await waitFor(() => expect(result.current.state.queuedChatMessages).toHaveLength(0));
    expect(result.current.state.savedComposerDraftsBySessionID.get(activeSession.id)).toEqual([
      "Keep saved B",
    ]);
  });

  it("keeps an existing-session prompt when workspace validation blocks submission", async () => {
    let messagePostCount = 0;
    const activeSession = {
      id: "chat_missing_workspace",
      title: "Missing workspace",
      agent_id: "codex",
      status: "idle" as const,
      workspace: "",
      message_count: 0,
      messages: [],
    };
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": () =>
          jsonResponse({ object: "chat_sessions", data: [activeSession] }),
        "/hecate/v1/chat/sessions/chat_missing_workspace": () =>
          jsonResponse({ object: "chat_session", data: activeSession }),
        "/hecate/v1/chat/sessions/chat_missing_workspace/messages": () => {
          messagePostCount += 1;
          return jsonResponse({ object: "chat_session", data: activeSession });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        defaultChatTarget: "external_agent",
        agentAdapterID: "codex",
        agentWorkspace: "",
        activeChatSessionID: activeSession.id,
        activeChatSession: activeSession,
        chatSessions: [activeSession],
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.activeChatSession?.agent_id).toBe("codex"));
    expect(result.current.state.chatTarget).toBe("external_agent");
    act(() => result.current.actions.setMessage("Keep this prompt"));

    await act(async () => {
      await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });

    expect(messagePostCount).toBe(0);
    expect(result.current.state.message).toBe("Keep this prompt");
    expect(result.current.state.chatErrorCode).toBe("chat.workspace_required");
    expect(result.current.state.chatLoading).toBe(false);
    expect(result.current.state.chatTurnActive).toBe(false);
    expect(result.current.state.chatTurnSessionID).toBe("");
  });

  it("keeps the submitted recovery beside a later draft when allocation fails", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let resolveCreate: ((response: Response) => void) | undefined;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) =>
          init?.method === "POST"
            ? new Promise<Response>((resolve) => {
                resolveCreate = resolve;
              })
            : jsonResponse({ object: "chat_sessions", data: [] }),
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));
    act(() => {
      result.current.actions.setMessage("Submitted prompt A");
    });

    let submitPromise!: Promise<void>;
    act(() => {
      submitPromise = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });
    await waitFor(() => expect(result.current.state.chatCreating).toBe(true));
    expect(result.current.state.chatTurnActive).toBe(true);
    act(() => {
      // The production composer is disabled during this interval. This direct
      // state write keeps the coordinator defensive against stale integrations.
      result.current.actions.setMessage("Later draft B");
    });

    await act(async () => {
      resolveCreate?.(
        new Response(JSON.stringify({ error: { message: "creation unavailable" } }), {
          status: 503,
          headers: { "Content-Type": "application/json" },
        }),
      );
      await submitPromise;
    });

    expect(result.current.state.message).toBe("Later draft B");
    expect(result.current.state.recoverableComposerDraft).toMatchObject({
      content: "Submitted prompt A",
    });
    expect(result.current.state.activeRecoverableComposerDraftID).toBeNull();
    expect(result.current.state.chatCreating).toBe(false);
    expect(result.current.state.chatTurnActive).toBe(false);
    expect(result.current.state.chatLoading).toBe(false);
  });

  it("keeps an explicit new chat selected while the prior chat turn finishes", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let resolvePriorMessage: ((response: Response) => void) | undefined;
    let priorMessageStarted = false;
    const priorSession = {
      id: "chat_prior_turn",
      title: "Prior chat",
      agent_id: "hecate",
      status: "idle" as const,
      workspace: "",
      message_count: 0,
      messages: [],
      provider: "",
      model: "gpt-4o-mini",
    };
    const nextSession = {
      id: "chat_explicit_next",
      title: "Next chat",
      agent_id: "hecate",
      status: "idle" as const,
      workspace: "",
      message_count: 0,
      messages: [],
      provider: "",
      model: "gpt-4o-mini",
    };
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) =>
          init?.method === "POST"
            ? jsonResponse({ object: "chat_session", data: nextSession })
            : jsonResponse({ object: "chat_sessions", data: [priorSession] }),
        "/hecate/v1/chat/sessions/chat_prior_turn/stream": () => emptyStreamResponse(),
        "/hecate/v1/chat/sessions/chat_prior_turn/messages": () => {
          priorMessageStarted = true;
          return new Promise<Response>((resolve) => {
            resolvePriorMessage = resolve;
          });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        activeChatSessionID: priorSession.id,
        activeChatSession: priorSession,
        chatSessions: [priorSession],
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));
    act(() => {
      result.current.actions.setMessage("Finish the prior turn");
    });

    let priorSubmit!: Promise<void>;
    act(() => {
      priorSubmit = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });
    await waitFor(() => expect(priorMessageStarted).toBe(true));
    expect(result.current.state.chatTurnSessionID).toBe(priorSession.id);

    await act(async () => {
      await result.current.actions.createChatSession({
        agentID: "hecate",
        draft: "Draft for the next chat",
      });
    });

    expect(result.current.state.activeChatSessionID).toBe(nextSession.id);
    expect(result.current.state.activeChatSession?.id).toBe(nextSession.id);
    expect(result.current.state.message).toBe("Draft for the next chat");
    expect(result.current.state.chatCreating).toBe(false);
    expect(result.current.state.chatLoading).toBe(true);
    expect(result.current.state.chatTurnSessionID).toBe(priorSession.id);

    await act(async () => {
      resolvePriorMessage?.(
        jsonResponse({
          object: "chat_session",
          data: {
            ...priorSession,
            status: "completed",
            message_count: 1,
            messages: [{ id: "prior_sent", role: "user", content: "Finish the prior turn" }],
          },
        }),
      );
      await priorSubmit;
    });

    expect(result.current.state.activeChatSessionID).toBe(nextSession.id);
    expect(result.current.state.activeChatSession?.id).toBe(nextSession.id);
    expect(result.current.state.message).toBe("Draft for the next chat");
    expect(result.current.state.chatLoading).toBe(false);
    expect(result.current.state.chatTurnSessionID).toBe("");
    expect(
      result.current.state.chatSessions.find((entry) => entry.id === priorSession.id)?.status,
    ).toBe("completed");
  });

  it("keeps an explicit chat creation pending when the prior turn finishes first", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let resolvePriorMessage: ((response: Response) => void) | undefined;
    let resolveNextCreate: ((response: Response) => void) | undefined;
    const priorSession = {
      id: "chat_prior_finishes_first",
      title: "Prior chat",
      agent_id: "hecate",
      status: "idle" as const,
      workspace: "",
      message_count: 0,
      messages: [],
      provider: "",
      model: "gpt-4o-mini",
    };
    const nextSession = {
      ...priorSession,
      id: "chat_create_finishes_last",
      title: "Next chat",
    };
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": (init) =>
          init?.method === "POST"
            ? new Promise<Response>((resolve) => {
                resolveNextCreate = resolve;
              })
            : jsonResponse({ object: "chat_sessions", data: [priorSession] }),
        "/hecate/v1/chat/sessions/chat_prior_finishes_first/stream": () => emptyStreamResponse(),
        "/hecate/v1/chat/sessions/chat_prior_finishes_first/messages": () =>
          new Promise<Response>((resolve) => {
            resolvePriorMessage = resolve;
          }),
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        activeChatSessionID: priorSession.id,
        activeChatSession: priorSession,
        chatSessions: [priorSession],
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));
    act(() => result.current.actions.setMessage("Finish prior turn"));

    let priorSubmit!: Promise<void>;
    act(() => {
      priorSubmit = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });
    await waitFor(() => expect(resolvePriorMessage).toBeDefined());

    let createPromise!: Promise<void>;
    act(() => {
      createPromise = result.current.actions.createChatSession({
        agentID: "hecate",
        draft: "Draft for pending creation",
      });
    });
    await waitFor(() => expect(resolveNextCreate).toBeDefined());
    expect(result.current.state.chatCreating).toBe(true);
    expect(result.current.state.activeChatSessionID).toBe("");
    expect(result.current.state.message).toBe("Draft for pending creation");

    await act(async () => {
      resolvePriorMessage?.(
        jsonResponse({
          object: "chat_session",
          data: {
            ...priorSession,
            status: "completed",
            message_count: 1,
            messages: [{ id: "prior_sent_first", role: "user", content: "Finish prior turn" }],
          },
        }),
      );
      await priorSubmit;
    });

    expect(result.current.state.chatCreating).toBe(true);
    expect(result.current.state.activeChatSessionID).toBe("");
    expect(result.current.state.message).toBe("Draft for pending creation");
    expect(result.current.state.chatLoading).toBe(false);
    expect(result.current.state.chatTurnSessionID).toBe("");

    await act(async () => {
      resolveNextCreate?.(jsonResponse({ object: "chat_session", data: nextSession }));
      await createPromise;
    });

    expect(result.current.state.chatCreating).toBe(false);
    expect(result.current.state.activeChatSessionID).toBe(nextSession.id);
    expect(result.current.state.activeChatSession?.id).toBe(nextSession.id);
    expect(result.current.state.message).toBe("Draft for pending creation");
  });

  it("deduplicates same-render submits for an existing idle session", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    let messageCount = 0;
    let resolveMessage: ((response: Response) => void) | undefined;
    const activeSession = {
      id: "chat_existing_idle",
      title: "Existing idle chat",
      agent_id: "hecate",
      status: "idle" as const,
      workspace: "",
      message_count: 0,
      messages: [],
      provider: "",
      model: "gpt-4o-mini",
    };
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": () =>
          jsonResponse({ object: "chat_sessions", data: [activeSession] }),
        "/hecate/v1/chat/sessions/chat_existing_idle/stream": () => emptyStreamResponse(),
        "/hecate/v1/chat/sessions/chat_existing_idle/messages": () => {
          messageCount += 1;
          return new Promise<Response>((resolve) => {
            resolveMessage = resolve;
          });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        activeChatSessionID: activeSession.id,
        activeChatSession: activeSession,
        chatSessions: [activeSession],
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));
    await waitFor(() => expect(result.current.state.model).toBe("gpt-4o-mini"));
    act(() => {
      result.current.actions.setMessage("Submit once");
    });

    let firstSubmit!: Promise<void>;
    let secondSubmit!: Promise<void>;
    act(() => {
      firstSubmit = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      secondSubmit = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });
    await waitFor(() => expect(messageCount).toBe(1));
    expect(result.current.state.queuedChatMessages).toHaveLength(0);

    await act(async () => {
      resolveMessage?.(
        jsonResponse({
          object: "chat_session",
          data: {
            ...activeSession,
            status: "completed",
            messages: [{ id: "sent_once", role: "user", content: "Submit once" }],
          },
        }),
      );
      await Promise.all([firstSubmit, secondSubmit]);
    });

    expect(messageCount).toBe(1);
    expect(result.current.state.queuedChatMessages).toHaveLength(0);
  });

  it("does not fall back to ambient provider or model when scoped launch passes explicit empty values", async () => {
    window.localStorage.setItem("hecate.chatToolsEnabled", "false");
    window.localStorage.setItem("hecate.providerFilter", "openai");
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
                id: "chat_auto_empty",
                title: "Hecate Chat",
                agent_id: "hecate",
                provider: "",
                model: "",
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
        agentID: "hecate",
        provider: "",
        model: "",
      });
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      provider: "",
      model: "",
    });
    expect(result.current.state.activeChatSessionID).toBe("chat_auto_empty");
  });

  it("honors an explicit project scope when creating an external-agent chat", async () => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
    const project: ProjectRecord = {
      id: "proj_1",
      name: "Hecate",
      roots: [
        {
          id: "root_1",
          path: "/tmp/hecate-project",
          kind: "workspace",
          active: true,
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
      default_root_id: "root_1",
      created_at: "2026-05-21T10:00:00Z",
      updated_at: "2026-05-21T10:00:00Z",
    };
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/projects": () =>
          jsonResponse({
            object: "projects",
            data: [project],
          }),
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

    const { result } = renderRuntimeConsoleHook({ projects: [project] });
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

  it("passes draft MCP servers when creating an external-agent chat", async () => {
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/agent-adapters": () =>
          jsonResponse({
            object: "agent_adapters",
            data: [{ id: "codex", name: "Codex", kind: "acp", available: true }],
          }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_codex_mcp",
                title: "Codex chat",
                agent_id: "codex",
                workspace: "/tmp/hecate",
                status: "idle",
                mcp_servers: createBody.mcp_servers,
                messages: [],
              },
            });
          }
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook({
      chatInitialState: {
        agentWorkspace: "/tmp/hecate",
        agentMCPServers: [
          {
            name: "filesystem",
            transport: "stdio",
            command: "mcp-fs",
            argsRaw: "--root /tmp/hecate",
            envRaw: "TOKEN=$MCP_TOKEN",
            url: "",
            headersRaw: "",
            approvalPolicy: "require_approval",
          },
          {
            name: "remote",
            transport: "http",
            command: "",
            argsRaw: "",
            envRaw: "",
            url: "https://mcp.example.com/mcp",
            headersRaw: "Authorization=Bearer $MCP_TOKEN",
            approvalPolicy: "block",
          },
        ],
      },
    });
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession({ agentID: "codex" });
    });

    expect(createBody).toMatchObject({
      agent_id: "codex",
      workspace: "/tmp/hecate",
      mcp_servers: [
        {
          name: "filesystem",
          command: "mcp-fs",
          args: ["--root", "/tmp/hecate"],
          env: { TOKEN: "$MCP_TOKEN" },
        },
        {
          name: "remote",
          url: "https://mcp.example.com/mcp",
          headers: { Authorization: "Bearer $MCP_TOKEN" },
        },
      ],
    });
    expect(createBody.mcp_servers[0].approval_policy).toBeUndefined();
    expect(createBody.mcp_servers[1].approval_policy).toBeUndefined();
  });

  it("shows workspace guidance when creating an external-agent chat without a workspace", async () => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
    window.localStorage.removeItem("hecate.agentWorkspace");
    let createCalled = false;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/agent-adapters": () =>
          jsonResponse({
            object: "agent_adapters",
            data: [{ id: "claude_code", name: "Claude Code", available: true }],
          }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") createCalled = true;
          return jsonResponse({ object: "chat_sessions", data: [] });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.loading).toBe(false));

    await act(async () => {
      await result.current.actions.createChatSession({ agentID: "claude_code" });
    });

    expect(createCalled).toBe(false);
    expect(result.current.state.activeChatSessionID).toBe("");
    expect(result.current.state.chatError).toBe(
      "Choose a workspace before using Hecate Chat tools or External Agent.",
    );
    expect(result.current.state.chatErrorCode).toBe("chat.workspace_required");
    expect(result.current.state.chatErrorStatus).toBe(400);
  });

  it("creates a Hecate chat shell when no model is selected", async () => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
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
      result.current.actions.setAgentWorkspace("/tmp/hecate");
    });

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

  it("drops a stale selected model when creating a new Hecate chat shell", async () => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.model", "missing-model");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/v1/models": () => jsonResponse({ object: "list", data: [] }),
        "/hecate/v1/chat/sessions": (init) => {
          if (init?.method === "POST") {
            createBody = JSON.parse(String(init.body ?? "{}"));
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_stale_model",
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
      result.current.actions.setAgentWorkspace("/tmp/hecate");
    });

    await act(async () => {
      await result.current.actions.createChatSession();
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      model: "",
    });
    expect(result.current.state.activeChatSessionID).toBe("chat_stale_model");
    expect(result.current.state.chatError).toBe("");
  });

  it("creates a tools-on Hecate chat shell when no workspace is selected", async () => {
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
                provider: createBody.provider,
                model: "llama3.1:8b",
                capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
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
      provider: "ollama",
      model: "llama3.1:8b",
      rtk_enabled: false,
    });
    expect(createBody).not.toHaveProperty("workspace");
    expect(result.current.state.activeChatSessionID).toBe("chat_no_workspace");
    expect(result.current.state.activeChatSession?.workspace ?? "").toBe("");
    expect(result.current.state.chatToolsEnabledBySessionID.get("chat_no_workspace")).toBe(true);
    expect(result.current.state.chatError).toBe("");
    expect(result.current.state.chatErrorCode).toBe("");
    expect(result.current.state.chatErrorStatus).toBeNull();
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
    // chatTarget stays "agent"; the tools-off intent (auto-downgraded
    // here because the model has no tool calling) is recorded on the
    // per-session map.
    expect(result.current.state.chatTarget).toBe("agent");
    expect(
      result.current.state.chatToolsEnabledBySessionID.get("chat_direct_tools_unavailable"),
    ).toBe(false);
    expect(result.current.state.chatError).toBe("");
  });

  it("shows workspace guidance when creating the selected external-agent chat without a workspace", async () => {
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
    expect(result.current.state.chatErrorStatus).toBe(400);
  });

  it("creates the selected tools-on Hecate chat without a workspace", async () => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.model", "gpt-4o-mini");
    window.localStorage.removeItem("hecate.agentWorkspace");
    let createBody: any = null;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/v1/models": () =>
          jsonResponse({
            object: "list",
            data: [
              {
                id: "gpt-4o-mini",
                owned_by: "openai",
                metadata: {
                  provider: "openai",
                  provider_kind: "cloud",
                  capabilities: { tool_calling: "basic" },
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
                id: "chat_no_workspace_selected",
                title: "Hecate Chat",
                agent_id: "hecate",
                provider: createBody.provider,
                model: "gpt-4o-mini",
                capabilities: { tool_calling: "basic", streaming: true, source: "provider" },
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
      await result.current.actions.createChatSession({ agentID: "hecate" });
    });

    expect(createBody).toMatchObject({
      agent_id: "hecate",
      model: "gpt-4o-mini",
      rtk_enabled: false,
    });
    expect(createBody).not.toHaveProperty("workspace");
    expect(result.current.state.activeChatSessionID).toBe("chat_no_workspace_selected");
    expect(result.current.state.activeChatSession?.workspace ?? "").toBe("");
    expect(result.current.state.chatToolsEnabledBySessionID.get("chat_no_workspace_selected")).toBe(
      true,
    );
    expect(result.current.state.chatError).toBe("");
    expect(result.current.state.chatErrorCode).toBe("");
    expect(result.current.state.chatErrorStatus).toBeNull();
  });

  it("does not create or select a chat when eager external-agent prepare fails", async () => {
    window.localStorage.setItem("hecate.chatTarget", "external_agent");
    window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
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
      result.current.actions.setAgentWorkspace("/tmp/hecate-project");
    });

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

  it("updates workspace execution through per-chat settings before task work starts", async () => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatSessionID", "a1");
    let settingsBody: unknown = null;
    const session = (workspaceMode: "persistent" | "in_place") => ({
      id: "a1",
      title: "Hecate",
      agent_id: "hecate",
      status: "idle",
      workspace: "/workspace",
      workspace_mode: workspaceMode,
      messages: [],
      created_at: "2026-04-20T00:00:00Z",
      updated_at: "2026-04-20T00:00:00Z",
    });
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": () =>
          jsonResponse({
            object: "chat_sessions",
            data: [{ ...session("persistent"), message_count: 0 }],
          }),
        "/hecate/v1/chat/sessions/a1": () =>
          jsonResponse({ object: "chat_session", data: session("persistent") }),
        "/hecate/v1/chat/sessions/a1/settings": (init) => {
          settingsBody = JSON.parse(String(init?.body ?? "{}"));
          return jsonResponse({ object: "chat_session", data: session("in_place") });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a1"));

    let ok = false;
    await act(async () => {
      ok = await result.current.actions.setHecateWorkspaceMode("in_place");
    });

    expect(ok).toBe(true);
    expect(settingsBody).toEqual({ workspace_mode: "in_place" });
    expect(result.current.state.activeChatSession?.workspace_mode).toBe("in_place");
  });

  it("shows the requested workspace mode immediately and blocks sends until failure reconciliation", async () => {
    window.localStorage.setItem("hecate.chatTarget", "agent");
    window.localStorage.setItem("hecate.chatSessionID", "a1");
    const session = {
      id: "a1",
      title: "Hecate",
      agent_id: "hecate",
      status: "idle",
      workspace: "/workspace",
      workspace_mode: "persistent" as const,
      provider: "openai",
      model: "gpt-4o-mini",
      messages: [],
      created_at: "2026-04-20T00:00:00Z",
      updated_at: "2026-04-20T00:00:00Z",
    };
    let rejectSettings: ((reason?: unknown) => void) | undefined;
    let messagePosts = 0;
    fetchMock.mockImplementation(
      defaultBackendMock({
        "/hecate/v1/chat/sessions": () =>
          jsonResponse({ object: "chat_sessions", data: [{ ...session, message_count: 0 }] }),
        "/hecate/v1/chat/sessions/a1": () =>
          jsonResponse({ object: "chat_session", data: session }),
        "/hecate/v1/chat/sessions/a1/settings": () =>
          new Promise<Response>((_resolve, reject) => {
            rejectSettings = reject;
          }),
        "/hecate/v1/chat/sessions/a1/messages": () => {
          messagePosts += 1;
          return jsonResponse({ object: "chat_session", data: session });
        },
      }),
    );

    const { result } = renderRuntimeConsoleHook();
    await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a1"));

    let mutationPromise!: Promise<boolean>;
    act(() => {
      mutationPromise = result.current.actions.setHecateWorkspaceMode("in_place");
    });
    await waitFor(() =>
      expect(result.current.state.workspaceModeMutation).toMatchObject({
        sessionID: "a1",
        requestedMode: "in_place",
      }),
    );

    act(() => result.current.actions.setMessage("do not send yet"));
    await act(async () => {
      await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
    });
    expect(messagePosts).toBe(0);
    expect(result.current.state.chatErrorCode).toBe("chat.workspace_mode_mutation_in_flight");

    await act(async () => {
      rejectSettings?.(new Error("settings connection dropped"));
      expect(await mutationPromise).toBe(false);
    });
    expect(result.current.state.workspaceModeMutation).toBeNull();
    expect(result.current.state.activeChatSession?.workspace_mode).toBe("persistent");
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
    function withSessions(
      sessions: Array<{ id: string; title: string }>,
      routes: Record<string, (init?: RequestInit) => Response | Promise<Response>> = {},
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

    it("keeps unsent composer drafts scoped to their chat session", async () => {
      const chatSession = (id: string) => ({
        object: "chat_session",
        data: {
          id,
          title: id,
          agent_id: "hecate",
          status: "completed",
          messages: [],
          provider: "openai",
          model: "gpt-4o-mini",
          created_at: "2026-04-20T00:00:00Z",
          updated_at: "2026-04-20T00:00:00Z",
        },
      });
      fetchMock.mockImplementation(
        withSessions(
          [
            { id: "chat_a", title: "Assignment chat" },
            { id: "chat_b", title: "Other chat" },
          ],
          {
            "/hecate/v1/chat/sessions/chat_a": () => jsonResponse(chatSession("chat_a")),
            "/hecate/v1/chat/sessions/chat_b": () => jsonResponse(chatSession("chat_b")),
          },
        ),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      await act(async () => {
        await result.current.actions.selectChatSession("chat_a", { draft: "Launch context A" });
      });
      expect(result.current.state.message).toBe("Launch context A");

      act(() => {
        result.current.actions.setMessage("Edited assignment draft A");
      });
      await act(async () => {
        await result.current.actions.selectChatSession("chat_b");
      });
      expect(result.current.state.message).toBe("");

      act(() => {
        result.current.actions.setMessage("Unrelated draft B");
      });
      await act(async () => {
        await result.current.actions.selectChatSession("chat_a");
      });
      expect(result.current.state.message).toBe("Edited assignment draft A");

      await act(async () => {
        await result.current.actions.selectChatSession("chat_b");
      });
      expect(result.current.state.message).toBe("Unrelated draft B");
    });

    it("keeps the current composer authoritative when reselecting the same chat", async () => {
      fetchMock.mockImplementation(
        withSessions([{ id: "chat_same", title: "Assignment chat" }], {
          "/hecate/v1/chat/sessions/chat_same": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_same",
                title: "Assignment chat",
                agent_id: "hecate",
                status: "completed",
                messages: [],
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
        await result.current.actions.selectChatSession("chat_same", { draft: "Launch seed" });
      });
      act(() => {
        result.current.actions.setMessage("Newer operator edit");
      });
      await act(async () => {
        await result.current.actions.selectChatSession("chat_same");
      });
      expect(result.current.state.message).toBe("Newer operator edit");

      act(() => {
        result.current.actions.setMessage("");
      });
      await act(async () => {
        await result.current.actions.selectChatSession("chat_same");
      });
      expect(result.current.state.message).toBe("");
    });

    it("restores prepared launch context after transient draft state is remounted", async () => {
      window.localStorage.setItem("hecate.chatSessionID", "chat_reload");
      fetchMock.mockImplementation(
        withSessions([{ id: "chat_reload", title: "Prepared assignment" }], {
          "/hecate/v1/chat/sessions/chat_reload": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_reload",
                title: "Prepared assignment",
                agent_id: "hecate",
                status: "completed",
                messages: [],
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
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("chat_reload"));
      expect(result.current.state.message).toBe("");

      await act(async () => {
        await result.current.actions.selectChatSession("chat_reload", {
          draft: "Regenerated launch context",
        });
      });
      expect(result.current.state.message).toBe("Regenerated launch context");

      act(() => {
        result.current.actions.setMessage("");
      });
      await act(async () => {
        await result.current.actions.selectChatSession("chat_reload", {
          draft: "Regenerated launch context",
        });
      });
      expect(result.current.state.message).toBe("");
    });

    it("keeps the latest chat selection authoritative when responses settle out of order", async () => {
      const selectedSession = (id: string) => ({
        object: "chat_session",
        data: {
          id,
          title: id,
          agent_id: "hecate",
          status: "completed",
          messages: [],
          provider: "openai",
          model: "gpt-4o-mini",
          created_at: "2026-04-20T00:00:00Z",
          updated_at: "2026-04-20T00:00:00Z",
        },
      });
      let resolveSlowSelection: ((response: Response) => void) | undefined;
      fetchMock.mockImplementation(
        withSessions(
          [
            { id: "chat_slow", title: "Slow assignment chat" },
            { id: "chat_latest", title: "Latest chat" },
          ],
          {
            "/hecate/v1/chat/sessions/chat_slow": () =>
              new Promise<Response>((resolve) => {
                resolveSlowSelection = resolve;
              }),
            "/hecate/v1/chat/sessions/chat_latest": () =>
              jsonResponse(selectedSession("chat_latest")),
          },
        ),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      let slowSelection!: Promise<boolean>;
      act(() => {
        slowSelection = result.current.actions.selectChatSession("chat_slow", {
          draft: "Assignment draft",
        });
      });
      await waitFor(() => expect(result.current.state.activeChatSessionID).toBe("chat_slow"));

      await act(async () => {
        await result.current.actions.selectChatSession("chat_latest", {
          draft: "Latest chat draft",
        });
      });
      expect(result.current.state.activeChatSessionID).toBe("chat_latest");
      expect(result.current.state.activeChatSession?.id).toBe("chat_latest");
      expect(result.current.state.message).toBe("Latest chat draft");

      let slowSelected = true;
      await act(async () => {
        resolveSlowSelection?.(jsonResponse(selectedSession("chat_slow")));
        slowSelected = await slowSelection;
      });
      expect(slowSelected).toBe(false);
      expect(result.current.state.activeChatSessionID).toBe("chat_latest");
      expect(result.current.state.activeChatSession?.id).toBe("chat_latest");
      expect(result.current.state.message).toBe("Latest chat draft");
    });

    it("keeps a late dashboard snapshot from replacing a newer linked-chat selection", async () => {
      window.localStorage.setItem("hecate.chatSessionID", "chat_previous");
      window.localStorage.setItem(
        "hecate.queuedChatMessages",
        JSON.stringify([
          {
            id: "queued_assignment",
            session_id: "chat_assignment",
            content: "Keep this queued for the assignment",
            execution_mode: "hecate_task",
            tools_enabled: true,
            provider_filter: "openai",
            model: "gpt-4o-mini",
            workspace: "/workspace",
            system_prompt: "",
            agent_id: "hecate",
            created_at: "2026-04-20T00:00:01Z",
          },
        ]),
      );
      let resolveDashboardSessions: ((response: Response) => void) | undefined;
      let dashboardSessionsStarted = false;
      const session = (id: string, title: string, status = "idle") => ({
        object: "chat_session",
        data: {
          id,
          title,
          agent_id: "hecate",
          status,
          workspace: "/workspace",
          messages: [],
          provider: "openai",
          model: "gpt-4o-mini",
          created_at: "2026-04-20T00:00:00Z",
          updated_at: "2026-04-20T00:00:00Z",
        },
      });
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () => {
            dashboardSessionsStarted = true;
            return new Promise<Response>((resolve) => {
              resolveDashboardSessions = resolve;
            });
          },
          "/hecate/v1/chat/sessions/chat_previous": () =>
            jsonResponse(session("chat_previous", "Previous chat")),
          "/hecate/v1/chat/sessions/chat_assignment": () =>
            jsonResponse(session("chat_assignment", "Assignment chat", "running")),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(dashboardSessionsStarted).toBe(true));

      await act(async () => {
        await result.current.actions.selectChatSession("chat_assignment", {
          draft: "Assignment launch context",
        });
      });
      expect(result.current.state.activeChatSessionID).toBe("chat_assignment");
      expect(result.current.state.activeChatSession?.id).toBe("chat_assignment");
      expect(result.current.state.message).toBe("Assignment launch context");
      expect(result.current.state.chatSessions.some((chat) => chat.id === "chat_assignment")).toBe(
        true,
      );
      expect(result.current.state.queuedChatMessages).toHaveLength(1);

      act(() => {
        resolveDashboardSessions?.(
          jsonResponse({
            object: "chat_sessions",
            data: [
              {
                id: "chat_previous",
                title: "Previous chat",
                agent_id: "hecate",
                status: "idle",
                message_count: 0,
              },
            ],
          }),
        );
      });
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      expect(result.current.state.activeChatSessionID).toBe("chat_assignment");
      expect(result.current.state.activeChatSession?.id).toBe("chat_assignment");
      expect(result.current.state.message).toBe("Assignment launch context");
      expect(result.current.state.chatSessions.some((chat) => chat.id === "chat_assignment")).toBe(
        true,
      );
      expect(result.current.state.chatSessions.some((chat) => chat.id === "chat_previous")).toBe(
        true,
      );
      expect(result.current.state.queuedChatMessages).toEqual([
        expect.objectContaining({
          id: "queued_assignment",
          content: "Keep this queued for the assignment",
        }),
      ]);
    });

    it("keeps a newer selection authoritative over a slower chat creation", async () => {
      let resolveCreate: ((response: Response) => void) | undefined;
      let createStarted = false;
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": (init) => {
            if (init?.method === "POST") {
              createStarted = true;
              return new Promise<Response>((resolve) => {
                resolveCreate = resolve;
              });
            }
            return jsonResponse({
              object: "chat_sessions",
              data: [{ id: "chat_latest", title: "Latest chat", agent_id: "hecate" }],
            });
          },
          "/hecate/v1/chat/sessions/chat_latest": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_latest",
                title: "Latest chat",
                agent_id: "hecate",
                status: "idle",
                messages: [],
                provider: "openai",
                model: "gpt-4o-mini",
              },
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook({
        chatInitialState: { agentWorkspace: "/workspace" },
      });
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      let createPromise!: Promise<void>;
      act(() => {
        createPromise = result.current.actions.createChatSession({
          agentID: "claude_code",
          title: "Slow project chat",
          draft: "Slow launch context",
        });
      });
      await waitFor(() => expect(createStarted).toBe(true));

      await act(async () => {
        await result.current.actions.selectChatSession("chat_latest", {
          draft: "Latest draft",
        });
      });
      expect(result.current.state.activeChatSession?.id).toBe("chat_latest");

      await act(async () => {
        resolveCreate?.(
          jsonResponse({
            object: "chat_session",
            data: {
              id: "chat_slow_create",
              title: "Slow project chat",
              agent_id: "claude_code",
              status: "idle",
              workspace: "/workspace",
              messages: [],
            },
          }),
        );
        await createPromise;
      });

      expect(result.current.state.activeChatSessionID).toBe("chat_latest");
      expect(result.current.state.activeChatSession?.id).toBe("chat_latest");
      expect(result.current.state.message).toBe("Latest draft");
    });

    it("keeps a project chat creation authoritative over a slower selection", async () => {
      window.localStorage.setItem("hecate.chatToolsEnabled", "false");
      let resolveSelection: ((response: Response) => void) | undefined;
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": (init) => {
            if (init?.method === "POST") {
              return jsonResponse({
                object: "chat_session",
                data: {
                  id: "chat_created",
                  title: "Created project chat",
                  agent_id: "hecate",
                  status: "idle",
                  messages: [],
                  provider: "openai",
                  model: "gpt-4o-mini",
                },
              });
            }
            return jsonResponse({
              object: "chat_sessions",
              data: [{ id: "chat_slow_select", title: "Slow selection", agent_id: "hecate" }],
            });
          },
          "/hecate/v1/chat/sessions/chat_slow_select": () =>
            new Promise<Response>((resolve) => {
              resolveSelection = resolve;
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      let selectionPromise!: Promise<boolean>;
      act(() => {
        selectionPromise = result.current.actions.selectChatSession("chat_slow_select", {
          draft: "Slow selection draft",
        });
      });
      await waitFor(() =>
        expect(result.current.state.activeChatSessionID).toBe("chat_slow_select"),
      );

      await act(async () => {
        await result.current.actions.createChatSession({
          agentID: "hecate",
          provider: "openai",
          model: "gpt-4o-mini",
          title: "Created project chat",
          draft: "Created launch context",
        });
      });
      expect(result.current.state.activeChatSession?.id).toBe("chat_created");

      let selected = true;
      await act(async () => {
        resolveSelection?.(
          jsonResponse({
            object: "chat_session",
            data: {
              id: "chat_slow_select",
              title: "Slow selection",
              agent_id: "hecate",
              status: "idle",
              messages: [],
              provider: "openai",
              model: "gpt-4o-mini",
            },
          }),
        );
        selected = await selectionPromise;
      });

      expect(selected).toBe(false);
      expect(result.current.state.activeChatSessionID).toBe("chat_created");
      expect(result.current.state.activeChatSession?.id).toBe("chat_created");
      expect(result.current.state.message).toBe("Created launch context");
    });

    it("keeps a preempted Hecate chat's session-local tools intent", async () => {
      let resolveCreate: ((response: Response) => void) | undefined;
      let createStarted = false;
      const chatSession = (id: string) => ({
        object: "chat_session",
        data: {
          id,
          title: id === "chat_preempted" ? "Preempted project chat" : "Latest chat",
          agent_id: "hecate",
          status: "idle",
          workspace: "/workspace",
          messages: [],
          provider: "openai",
          model: "gpt-4o-mini",
        },
      });
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": (init) => {
            if (init?.method === "POST") {
              createStarted = true;
              return new Promise<Response>((resolve) => {
                resolveCreate = resolve;
              });
            }
            return jsonResponse({
              object: "chat_sessions",
              data: [{ id: "chat_latest", title: "Latest chat", agent_id: "hecate" }],
            });
          },
          "/hecate/v1/chat/sessions/chat_latest": () => jsonResponse(chatSession("chat_latest")),
          "/hecate/v1/chat/sessions/chat_preempted": () =>
            jsonResponse(chatSession("chat_preempted")),
        }),
      );

      const { result } = renderRuntimeConsoleHook({
        chatInitialState: { agentWorkspace: "/workspace" },
      });
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      let createPromise!: Promise<void>;
      act(() => {
        createPromise = result.current.actions.createChatSession({
          agentID: "hecate",
          provider: "openai",
          model: "gpt-4o-mini",
          title: "Preempted project chat",
          draft: "Preempted launch context",
        });
      });
      await waitFor(() => expect(createStarted).toBe(true));
      await act(async () => {
        await result.current.actions.selectChatSession("chat_latest");
      });

      await act(async () => {
        resolveCreate?.(jsonResponse(chatSession("chat_preempted")));
        await createPromise;
      });
      expect(result.current.state.activeChatSessionID).toBe("chat_latest");
      expect(result.current.state.chatToolsEnabledBySessionID.get("chat_preempted")).toBe(true);

      act(() => {
        result.current.actions.startNewChat();
      });
      act(() => {
        result.current.actions.setChatToolsEnabled(false);
      });
      expect(result.current.state.defaultChatToolsEnabled).toBe(false);

      await act(async () => {
        await result.current.actions.selectChatSession("chat_preempted");
      });
      expect(result.current.state.activeChatSession?.id).toBe("chat_preempted");
      expect(result.current.state.chatToolsEnabledBySessionID.get("chat_preempted")).toBe(true);
    });

    it("does not regenerate a created project draft after the operator clears it", async () => {
      window.localStorage.setItem("hecate.chatToolsEnabled", "false");
      let createCount = 0;
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": (init) => {
            if (init?.method === "POST") {
              createCount += 1;
              return jsonResponse({
                object: "chat_session",
                data: {
                  id: "chat_reusable",
                  title: "Reusable project chat",
                  project_id: "proj_1",
                  agent_id: "hecate",
                  status: "idle",
                  workspace: "/workspace",
                  workspace_mode: "persistent",
                  messages: [],
                  provider: "openai",
                  model: "gpt-4o-mini",
                },
              });
            }
            return jsonResponse({ object: "chat_sessions", data: [] });
          },
          "/hecate/v1/chat/sessions/chat_reusable": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_reusable",
                title: "Reusable project chat",
                project_id: "proj_1",
                agent_id: "hecate",
                status: "idle",
                workspace: "/workspace",
                workspace_mode: "persistent",
                messages: [],
                provider: "openai",
                model: "gpt-4o-mini",
              },
            }),
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      const request = {
        agentID: "hecate",
        projectID: "proj_1",
        provider: "openai",
        model: "gpt-4o-mini",
        title: "Reusable project chat",
        draft: "Generated launch context",
        reuseEmptyDraft: true,
      } as const;

      await act(async () => {
        await result.current.actions.createChatSession(request);
      });
      expect(result.current.state.message).toBe("Generated launch context");

      act(() => {
        result.current.actions.setMessage("");
      });
      await act(async () => {
        await result.current.actions.createChatSession(request);
      });

      expect(createCount).toBe(1);
      expect(result.current.state.activeChatSessionID).toBe("chat_reusable");
      expect(result.current.state.message).toBe("");
    });
  });

  // ─── Hecate Chat session actions ───────────────────────────────────────────
  describe("Hecate Chat session actions", () => {
    function persistQueuedPrompt(content = "after this") {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatToolsEnabled", "false");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.providerFilter", "openai");
      window.localStorage.setItem("hecate.model", "gpt-4o-mini");
      window.localStorage.setItem(
        "hecate.queuedChatMessages",
        JSON.stringify([
          {
            id: "queued_retry",
            session_id: "a1",
            content,
            execution_mode: "hecate_task",
            tools_enabled: false,
            provider_filter: "openai",
            model: "gpt-4o-mini",
            workspace: "",
            system_prompt: "",
            agent_id: "hecate",
            created_at: "2026-04-20T00:00:01Z",
          },
        ]),
      );
    }

    function queuedPromptSession(messages: Array<Record<string, unknown>> = []) {
      return {
        object: "chat_session",
        data: {
          id: "a1",
          title: "Vision chat",
          agent_id: "hecate",
          status: "completed",
          workspace: "",
          provider: "openai",
          model: "gpt-4o-mini",
          capabilities: { tool_calling: "basic", image_input: "supported" },
          messages,
          created_at: "2026-04-20T00:00:00Z",
          updated_at: "2026-04-20T00:00:02Z",
        },
      };
    }

    function queuedPromptSessionList() {
      return {
        object: "chat_sessions",
        data: [
          {
            id: "a1",
            title: "Vision chat",
            agent_id: "hecate",
            status: "completed",
            provider: "openai",
            model: "gpt-4o-mini",
            message_count: 0,
          },
        ],
      };
    }

    function withSessions(
      sessions: Array<{ id: string; title: string }>,
      routes: Record<string, (init?: RequestInit) => Response | Promise<Response>> = {},
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

    function mockImageSubmissionFailure({
      ambiguousUpload,
      ambiguousUploadAt = 1,
      codedError = true,
      committed,
      deleteFails = false,
      errorCode,
      reconciliationFails = false,
      reconciledAssistantStatus,
      status,
      uploadFailureDeferred = false,
    }: {
      ambiguousUpload?: "network" | "proxy_502" | "hecate_500";
      ambiguousUploadAt?: number;
      codedError?: boolean;
      committed: boolean;
      deleteFails?: boolean;
      errorCode?: string;
      reconciliationFails?: boolean;
      reconciledAssistantStatus?: "running" | "completed" | "failed" | "cancelled";
      status: "network" | 409 | 422 | 429 | 500 | 502;
      uploadFailureDeferred?: boolean;
    }) {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatToolsEnabled", "false");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.providerFilter", "openai");
      window.localStorage.setItem("hecate.model", "gpt-4o-mini");

      const file = new File(["image"], "map.png", { type: "image/png" });
      const storedAttachment = {
        id: "attachment-stable-id",
        session_id: "a1",
        filename: "map.png",
        media_type: "image/png",
        size_bytes: 5,
        sha256: "abc",
        created_at: "2026-07-13T10:00:00Z",
        content_url: "/hecate/v1/chat/sessions/a1/attachments/attachment-stable-id/content",
      };
      let messagePostStarted = false;
      let deleteCount = 0;
      let uploadCount = 0;
      let releaseUploadFailure: ((response: Response) => void) | undefined;
      let rejectUploadFailure: ((reason: Error) => void) | undefined;
      const deferredUploadFailure = new Promise<Response>((resolve, reject) => {
        releaseUploadFailure = resolve;
        rejectUploadFailure = reject;
      });

      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/v1/models") {
          return jsonResponse({
            object: "list",
            data: [
              {
                id: "gpt-4o-mini",
                owned_by: "openai",
                metadata: {
                  provider: "openai",
                  provider_kind: "cloud",
                  capabilities: { tool_calling: "basic", image_input: "supported" },
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
                title: "Vision chat",
                agent_id: "hecate",
                status: "completed",
                provider: "openai",
                model: "gpt-4o-mini",
                message_count: 0,
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          if (messagePostStarted && reconciliationFails) {
            throw new TypeError("reconciliation unavailable");
          }
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Vision chat",
              agent_id: "hecate",
              status: "completed",
              provider: "openai",
              model: "gpt-4o-mini",
              capabilities: { tool_calling: "basic", image_input: "supported" },
              messages:
                messagePostStarted && committed
                  ? [
                      {
                        id: "user-committed",
                        role: "user",
                        content: "inspect the map",
                        attachments: [storedAttachment],
                        created_at: "2026-07-13T10:00:01Z",
                      },
                      ...(reconciledAssistantStatus
                        ? [
                            {
                              id: "assistant-reconciled",
                              role: "assistant",
                              content:
                                reconciledAssistantStatus === "running"
                                  ? "Model is responding"
                                  : "Model response",
                              status: reconciledAssistantStatus,
                              created_at: "2026-07-13T10:00:02Z",
                            },
                          ]
                        : []),
                    ]
                  : [],
            },
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1/attachments") {
          uploadCount += 1;
          if (ambiguousUpload && uploadCount === ambiguousUploadAt) {
            if (uploadFailureDeferred) return deferredUploadFailure;
            if (ambiguousUpload === "network") {
              throw new TypeError("connection reset after upload commit");
            }
            if (ambiguousUpload === "hecate_500") {
              return jsonResponse(
                {
                  error: {
                    type: "gateway_error",
                    message: "attachment commit result was lost",
                  },
                },
                500,
              );
            }
            return new Response(
              "<html>proxy lost attachment-stable-id with internal-secret-42</html>",
              {
                status: 502,
                headers: {
                  "Content-Type": "text/html",
                  "X-Request-Id": "request-upload-502",
                  "X-Trace-Id": "trace-upload-502",
                },
              },
            );
          }
          if (uploadFailureDeferred) return deferredUploadFailure;
          return jsonResponse({ object: "chat_attachment", data: storedAttachment });
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") return emptyStreamResponse();
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostStarted = true;
          if (status === "network") throw new TypeError("connection reset");
          if (!codedError) {
            return new Response("<html>upstream unavailable</html>", {
              status,
              headers: { "Content-Type": "text/html" },
            });
          }
          return jsonResponse(
            {
              error: {
                type: errorCode ?? (status === 500 ? "gateway_error" : "chat.attachment_rejected"),
                message: "attachment rejected",
              },
            },
            status,
          );
        }
        if (
          url === "/hecate/v1/chat/sessions/a1/attachments/attachment-stable-id" &&
          init?.method === "DELETE"
        ) {
          deleteCount += 1;
          if (deleteFails) {
            return jsonResponse(
              {
                error: {
                  type: "internal_error",
                  message: "attachment cleanup unavailable",
                },
              },
              503,
            );
          }
          return new Response(null, { status: 204 });
        }
        return defaultBackendMock()(input, init);
      });

      return {
        file,
        rejectUploadFailure,
        releaseUploadFailure,
        get deleteCount() {
          return deleteCount;
        },
        get uploadCount() {
          return uploadCount;
        },
      };
    }

    it("retains an initial linked-chat draft when selection fails and is retried", async () => {
      let attempts = 0;
      fetchMock.mockImplementation(
        withSessions([{ id: "chat_retry", title: "Retry chat" }], {
          "/hecate/v1/chat/sessions/chat_retry": () => {
            attempts += 1;
            if (attempts === 1) {
              return new Response(
                JSON.stringify({ error: { message: "temporarily unavailable" } }),
                {
                  status: 503,
                  headers: { "Content-Type": "application/json" },
                },
              );
            }
            return jsonResponse({
              object: "chat_session",
              data: {
                id: "chat_retry",
                title: "Retry chat",
                agent_id: "hecate",
                status: "completed",
                messages: [],
                provider: "openai",
                model: "gpt-4o-mini",
                created_at: "2026-04-20T00:00:00Z",
                updated_at: "2026-04-20T00:00:00Z",
              },
            });
          },
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      let selected = true;
      await act(async () => {
        selected = await result.current.actions.selectChatSession("chat_retry", {
          draft: "One-time launch context",
        });
      });
      expect(selected).toBe(false);
      expect(result.current.state.message).toBe("");

      await act(async () => {
        selected = await result.current.actions.selectChatSession("chat_retry");
      });
      expect(selected).toBe(true);
      expect(result.current.state.message).toBe("One-time launch context");
    });

    it("retains a newer same-chat edit when a deferred refresh fails", async () => {
      let loads = 0;
      let releaseRefresh: ((response: Response) => void) | undefined;
      const deferredRefresh = new Promise<Response>((resolve) => {
        releaseRefresh = resolve;
      });
      const session = () =>
        jsonResponse({
          object: "chat_session",
          data: {
            id: "chat_same",
            title: "Same chat",
            agent_id: "hecate",
            status: "completed",
            messages: [],
            provider: "openai",
            model: "gpt-4o-mini",
            created_at: "2026-04-20T00:00:00Z",
            updated_at: "2026-04-20T00:00:00Z",
          },
        });
      fetchMock.mockImplementation(
        withSessions([{ id: "chat_same", title: "Same chat" }], {
          "/hecate/v1/chat/sessions/chat_same": () => {
            loads += 1;
            return loads === 2 ? deferredRefresh : session();
          },
        }),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await act(async () => {
        await result.current.actions.selectChatSession("chat_same");
      });
      act(() => {
        result.current.actions.setMessage("draft before refresh");
      });

      let refresh!: Promise<boolean>;
      act(() => {
        refresh = result.current.actions.selectChatSession("chat_same");
      });
      await waitFor(() => expect(loads).toBe(2));
      act(() => {
        result.current.actions.setMessage("newer edit while refreshing");
      });

      let refreshed = true;
      await act(async () => {
        releaseRefresh?.(jsonResponse({ error: { message: "temporarily unavailable" } }, 503));
        refreshed = await refresh;
      });
      expect(refreshed).toBe(false);
      expect(result.current.state.message).toBe("");

      await act(async () => {
        refreshed = await result.current.actions.selectChatSession("chat_same");
      });
      expect(refreshed).toBe(true);
      expect(result.current.state.message).toBe("newer edit while refreshing");
    });

    it("keeps image drafts scoped to the active chat when new-session creation is requested", async () => {
      let createCount = 0;
      const backend = withSessions([{ id: "chat_a", title: "A" }], {
        "/hecate/v1/chat/sessions/chat_a": () =>
          jsonResponse({
            object: "chat_session",
            data: {
              id: "chat_a",
              title: "A",
              agent_id: "hecate",
              status: "completed",
              provider: "openai",
              model: "gpt-4o-mini",
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:00Z",
            },
          }),
      });
      fetchMock.mockImplementation((input, init) => {
        if (String(input) === "/hecate/v1/chat/sessions" && init?.method === "POST") {
          createCount += 1;
        }
        return backend(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await act(async () => {
        await result.current.actions.selectChatSession("chat_a");
      });
      const file = new File(["image"], "map.png", { type: "image/png" });
      act(() => {
        result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
      });

      await act(async () => {
        await result.current.actions.createChatSession({
          agentID: "hecate",
          projectID: "project_b",
        });
      });

      expect(createCount).toBe(0);
      expect(result.current.state.activeChatSessionID).toBe("chat_a");
      expect(result.current.state.activeChatSession?.id).toBe("chat_a");
      expect(result.current.state.pendingChatAttachments).toEqual([{ id: "draft-1", file }]);
      expect(result.current.state.notice?.message).toBe(
        "Remove attached files before starting a new chat.",
      );
    });

    it("blocks session changes with image drafts and preserves them across a failed refresh", async () => {
      let activeLoads = 0;
      let destinationLoads = 0;
      let failActiveReload = false;
      const session = (id: string) => ({
        object: "chat_session",
        data: {
          id,
          title: id,
          agent_id: "hecate",
          status: "completed",
          workspace: "",
          message_count: 0,
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
            "/hecate/v1/chat/sessions/chat_a": () => {
              activeLoads += 1;
              if (failActiveReload) {
                return jsonResponse({ error: { message: "session refresh failed" } }, 503);
              }
              return jsonResponse(session("chat_a"));
            },
            "/hecate/v1/chat/sessions/chat_b": () => {
              destinationLoads += 1;
              return jsonResponse({ error: { message: "destination unavailable" } }, 503);
            },
          },
        ),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await act(async () => {
        await result.current.actions.selectChatSession("chat_a");
      });
      const file = new File(["image"], "map.png", { type: "image/png" });
      act(() => {
        result.current.actions.setMessage("inspect this map");
        result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
      });

      let changed = true;
      await act(async () => {
        changed = await result.current.actions.selectChatSession("chat_b");
      });

      expect(changed).toBe(false);
      expect(destinationLoads).toBe(0);
      expect(result.current.state.activeChatSessionID).toBe("chat_a");
      expect(result.current.state.activeChatSession?.id).toBe("chat_a");
      expect(result.current.state.message).toBe("inspect this map");
      expect(result.current.state.pendingChatAttachments).toEqual([{ id: "draft-1", file }]);
      expect(result.current.state.notice?.message).toBe(
        "Remove attached files before switching chats.",
      );

      const loadsBeforeRefresh = activeLoads;
      failActiveReload = true;
      await act(async () => {
        changed = await result.current.actions.selectChatSession("chat_a");
      });

      expect(changed).toBe(false);
      expect(activeLoads).toBe(loadsBeforeRefresh + 1);
      expect(result.current.state.activeChatSessionID).toBe("chat_a");
      expect(result.current.state.activeChatSession?.id).toBe("chat_a");
      expect(result.current.state.message).toBe("inspect this map");
      expect(result.current.state.pendingChatAttachments).toEqual([{ id: "draft-1", file }]);
    });

    it.each(["success", "failure"] as const)(
      "keeps deferred session selection %s scoped to the previous composer when an image draft appears",
      async (outcome) => {
        let destinationLoads = 0;
        let releaseSelection: ((response: Response) => void) | undefined;
        const deferredSelection = new Promise<Response>((resolve) => {
          releaseSelection = resolve;
        });
        const session = (id: string) =>
          jsonResponse({
            object: "chat_session",
            data: {
              id,
              title: id,
              agent_id: "hecate",
              status: "completed",
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
              "/hecate/v1/chat/sessions/chat_a": () => session("chat_a"),
              "/hecate/v1/chat/sessions/chat_b": () => {
                destinationLoads += 1;
                return deferredSelection;
              },
            },
          ),
        );

        const { result } = renderRuntimeConsoleHook();
        await waitFor(() => expect(result.current.state.loading).toBe(false));
        await act(async () => {
          await result.current.actions.selectChatSession("chat_a");
        });

        let selection!: Promise<boolean>;
        act(() => {
          selection = result.current.actions.selectChatSession("chat_b");
        });
        await waitFor(() => expect(destinationLoads).toBe(1));
        expect(result.current.state.activeChatSessionID).toBe("chat_b");
        expect(result.current.state.activeChatSession).toBeNull();

        const file = new File(["image"], "map.png", { type: "image/png" });
        act(() => {
          result.current.actions.setMessage("inspect this map");
          result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
        });

        let changed = true;
        await act(async () => {
          releaseSelection?.(
            outcome === "success"
              ? session("chat_b")
              : jsonResponse({ error: { message: "destination unavailable" } }, 503),
          );
          changed = await selection;
        });

        expect(changed).toBe(false);
        expect(result.current.state.activeChatSessionID).toBe("chat_a");
        expect(result.current.state.activeChatSession?.id).toBe("chat_a");
        expect(result.current.state.message).toBe("inspect this map");
        expect(result.current.state.pendingChatAttachments).toEqual([{ id: "draft-1", file }]);
      },
    );

    it("restores the source composer when a File appears during deferred selection", async () => {
      let releaseSelection: ((response: Response) => void) | undefined;
      const deferredSelection = new Promise<Response>((resolve) => {
        releaseSelection = resolve;
      });
      const session = (id: string) =>
        jsonResponse({
          object: "chat_session",
          data: {
            id,
            title: id,
            agent_id: "hecate",
            status: "completed",
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
            "/hecate/v1/chat/sessions/chat_a": () => session("chat_a"),
            "/hecate/v1/chat/sessions/chat_b": () => deferredSelection,
          },
        ),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await act(async () => {
        await result.current.actions.selectChatSession("chat_a");
      });
      act(() => {
        result.current.actions.setMessage("source composer draft");
      });

      let selection!: Promise<boolean>;
      act(() => {
        selection = result.current.actions.selectChatSession("chat_b");
      });
      await waitFor(() => expect(result.current.state.activeChatSessionID).toBe("chat_b"));
      expect(result.current.state.message).toBe("");

      const file = new File(["image"], "map.png", { type: "image/png" });
      act(() => {
        result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
      });

      let selected = true;
      await act(async () => {
        releaseSelection?.(session("chat_b"));
        selected = await selection;
      });

      expect(selected).toBe(false);
      expect(result.current.state.activeChatSessionID).toBe("chat_a");
      expect(result.current.state.activeChatSession?.id).toBe("chat_a");
      expect(result.current.state.message).toBe("source composer draft");
      expect(result.current.state.pendingChatAttachments).toEqual([{ id: "draft-1", file }]);
    });

    it("ignores an older session selection that succeeds after the latest selection", async () => {
      let releaseSlowSelection: ((response: Response) => void) | undefined;
      const slowSelection = new Promise<Response>((resolve) => {
        releaseSlowSelection = resolve;
      });
      const session = (id: string) =>
        jsonResponse({
          object: "chat_session",
          data: {
            id,
            title: id,
            agent_id: "hecate",
            status: "completed",
            provider: "openai",
            model: `model-${id}`,
            workspace: `/workspace/${id}`,
            messages: [],
            created_at: "2026-04-20T00:00:00Z",
            updated_at: "2026-04-20T00:00:00Z",
          },
        });
      fetchMock.mockImplementation(
        withSessions(
          [
            { id: "chat_b", title: "B" },
            { id: "chat_c", title: "C" },
          ],
          {
            "/hecate/v1/chat/sessions/chat_b": () => slowSelection,
            "/hecate/v1/chat/sessions/chat_c": () => session("chat_c"),
          },
        ),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      let slowResult!: Promise<boolean>;
      act(() => {
        slowResult = result.current.actions.selectChatSession("chat_b");
      });
      expect(result.current.state.activeChatSessionID).toBe("chat_b");
      expect(result.current.state.activeChatSession).toBeNull();

      let latestSelected = false;
      await act(async () => {
        latestSelected = await result.current.actions.selectChatSession("chat_c");
      });
      expect(latestSelected).toBe(true);
      expect(result.current.state.activeChatSessionID).toBe("chat_c");
      expect(result.current.state.activeChatSession?.id).toBe("chat_c");

      let staleSelected = true;
      await act(async () => {
        releaseSlowSelection?.(session("chat_b"));
        staleSelected = await slowResult;
      });

      expect(staleSelected).toBe(false);
      expect(result.current.state.activeChatSessionID).toBe("chat_c");
      expect(result.current.state.activeChatSession?.id).toBe("chat_c");
      expect(result.current.state.model).toBe("model-chat_c");
      expect(result.current.state.agentWorkspace).toBe("/workspace/chat_c");
    });

    it("ignores an older session selection that fails after the latest selection", async () => {
      let releaseSlowSelection: ((response: Response) => void) | undefined;
      const slowSelection = new Promise<Response>((resolve) => {
        releaseSlowSelection = resolve;
      });
      const latestSession = jsonResponse({
        object: "chat_session",
        data: {
          id: "chat_c",
          title: "C",
          agent_id: "hecate",
          status: "completed",
          provider: "openai",
          model: "model-chat_c",
          workspace: "/workspace/chat_c",
          messages: [],
          created_at: "2026-04-20T00:00:00Z",
          updated_at: "2026-04-20T00:00:00Z",
        },
      });
      fetchMock.mockImplementation(
        withSessions(
          [
            { id: "chat_b", title: "B" },
            { id: "chat_c", title: "C" },
          ],
          {
            "/hecate/v1/chat/sessions/chat_b": () => slowSelection,
            "/hecate/v1/chat/sessions/chat_c": () => latestSession,
          },
        ),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      let slowResult!: Promise<boolean>;
      act(() => {
        slowResult = result.current.actions.selectChatSession("chat_b");
      });
      expect(result.current.state.activeChatSessionID).toBe("chat_b");
      expect(result.current.state.activeChatSession).toBeNull();
      await act(async () => {
        expect(await result.current.actions.selectChatSession("chat_c")).toBe(true);
      });

      let staleSelected = true;
      await act(async () => {
        releaseSlowSelection?.(jsonResponse({ error: { message: "late selection failure" } }, 503));
        staleSelected = await slowResult;
      });

      expect(staleSelected).toBe(false);
      expect(result.current.state.activeChatSessionID).toBe("chat_c");
      expect(result.current.state.activeChatSession?.id).toBe("chat_c");
      expect(result.current.state.chatError).toBe("");
      expect(result.current.state.notice).toBeNull();
    });

    it("does not let a slow selection repopulate a session cleared by a target switch", async () => {
      let releaseSlowSelection: ((response: Response) => void) | undefined;
      const slowSelection = new Promise<Response>((resolve) => {
        releaseSlowSelection = resolve;
      });
      const session = (id: string) =>
        jsonResponse({
          object: "chat_session",
          data: {
            id,
            title: id,
            agent_id: "hecate",
            status: "completed",
            provider: "openai",
            model: "gpt-4o-mini",
            workspace: `/workspace/${id}`,
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
            "/hecate/v1/chat/sessions/chat_a": () => session("chat_a"),
            "/hecate/v1/chat/sessions/chat_b": () => slowSelection,
          },
        ),
      );

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await act(async () => {
        await result.current.actions.selectChatSession("chat_a");
      });

      let slowResult!: Promise<boolean>;
      act(() => {
        slowResult = result.current.actions.selectChatSession("chat_b");
      });
      expect(result.current.state.activeChatSessionID).toBe("chat_b");
      expect(result.current.state.activeChatSession).toBeNull();
      act(() => {
        result.current.actions.setChatTarget("external_agent");
      });
      expect(result.current.state.activeChatSessionID).toBe("");
      expect(result.current.state.activeChatSession).toBeNull();

      let staleSelected = true;
      await act(async () => {
        releaseSlowSelection?.(session("chat_b"));
        staleSelected = await slowResult;
      });

      expect(staleSelected).toBe(false);
      expect(result.current.state.activeChatSessionID).toBe("");
      expect(result.current.state.activeChatSession).toBeNull();
      expect(result.current.state.chatTarget).toBe("external_agent");
    });

    it("deduplicates overlapping session creation and never carries a new image draft", async () => {
      let createCount = 0;
      let releaseCreate: ((response: Response) => void) | undefined;
      const deferredCreate = new Promise<Response>((resolve) => {
        releaseCreate = resolve;
      });
      const activeSession = {
        object: "chat_session",
        data: {
          id: "chat_a",
          title: "A",
          agent_id: "hecate",
          status: "completed",
          provider: "openai",
          model: "gpt-4o-mini",
          workspace: "/workspace/a",
          messages: [],
          created_at: "2026-04-20T00:00:00Z",
          updated_at: "2026-04-20T00:00:00Z",
        },
      };
      const backend = withSessions([{ id: "chat_a", title: "A" }], {
        "/hecate/v1/chat/sessions/chat_a": () => jsonResponse(activeSession),
      });
      fetchMock.mockImplementation((input, init) => {
        if (String(input) === "/hecate/v1/chat/sessions" && init?.method === "POST") {
          createCount += 1;
          return deferredCreate;
        }
        return backend(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_a");
      });
      act(() => {
        result.current.runtimeConsole.actions.setMessage("source composer draft");
      });

      let firstCreate!: Promise<void>;
      act(() => {
        firstCreate = result.current.runtimeConsole.actions.createChatSession({
          agentID: "hecate",
          draft: "created shell draft",
        });
      });
      await waitFor(() => expect(createCount).toBe(1));
      await act(async () => {
        await result.current.runtimeConsole.actions.createChatSession({ agentID: "hecate" });
      });
      expect(createCount).toBe(1);

      const file = new File(["image"], "map.png", { type: "image/png" });
      act(() => {
        result.current.runtimeConsole.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
      });
      await waitFor(() =>
        expect(result.current.runtimeConsole.state.pendingChatAttachments).toHaveLength(1),
      );

      await act(async () => {
        releaseCreate?.(
          jsonResponse({
            object: "chat_session",
            data: {
              id: "chat_new",
              title: "New chat",
              agent_id: "hecate",
              status: "completed",
              provider: "openai",
              model: "gpt-4o-mini",
              messages: [],
              created_at: "2026-04-20T00:00:01Z",
              updated_at: "2026-04-20T00:00:01Z",
            },
          }),
        );
        await firstCreate;
      });

      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("chat_a");
      expect(result.current.runtimeConsole.state.activeChatSession?.id).toBe("chat_a");
      expect(result.current.runtimeConsole.state.message).toBe("source composer draft");
      expect(result.current.runtimeConsole.state.pendingChatAttachments).toEqual([
        { id: "draft-1", file },
      ]);
      expect(
        result.current.runtimeConsole.state.chatSessions.map((session) => session.id),
      ).toContain("chat_new");
      expect(result.current.chat.state.composerDraftsBySessionID.get("chat_new")).toBe(
        "created shell draft",
      );
      expect(result.current.runtimeConsole.state.notice?.message).toContain(
        "attached files belong",
      );
      expect(result.current.runtimeConsole.state.chatLoading).toBe(false);
    });

    it("suppresses a deferred cancel failure after the operator switches chats", async () => {
      let releaseCancel: ((response: Response) => void) | undefined;
      const deferredCancel = new Promise<Response>((resolve) => {
        releaseCancel = resolve;
      });
      const session = (id: string, status = "completed") =>
        jsonResponse({
          object: "chat_session",
          data: {
            id,
            title: id,
            agent_id: id === "chat_a" ? "codex" : "hecate",
            status,
            workspace: "/workspace",
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
            "/hecate/v1/chat/sessions/chat_a": () => session("chat_a", "running"),
            "/hecate/v1/chat/sessions/chat_b": () => session("chat_b"),
            "/hecate/v1/chat/sessions/chat_a/cancel": () => deferredCancel,
          },
        ),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_a");
      });

      let cancellation!: Promise<void>;
      act(() => {
        cancellation = result.current.runtimeConsole.actions.cancelAgentChat();
      });
      await waitFor(() => expect(result.current.chat.state.chatCancelling).toBe(true));
      expect(result.current.chat.state.chatCancellingSessionID).toBe("chat_a");

      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_b");
      });
      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("chat_b");

      await act(async () => {
        releaseCancel?.(jsonResponse({ error: { message: "cancel unavailable" } }, 503));
        await cancellation;
      });

      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("chat_b");
      expect(result.current.runtimeConsole.state.chatError).toBe("");
      expect(result.current.chat.state.chatCancelling).toBe(false);
      expect(result.current.chat.state.chatCancellingSessionID).toBe("");
    });

    it("keeps deferred cancellation ownership until the exact request settles", async () => {
      let releaseCancel: ((response: Response) => void) | undefined;
      const deferredCancel = new Promise<Response>((resolve) => {
        releaseCancel = resolve;
      });
      let cancelRequestCount = 0;
      let messagePostCount = 0;
      let sessionGetCount = 0;
      const blockedRequestCounts = {
        cancelApproval: 0,
        compact: 0,
        config: 0,
        rename: 0,
        resolveApproval: 0,
        workspaceRevert: 0,
      };
      const session = (status: "running" | "completed") => ({
        id: "chat_a",
        title: "External Agent chat",
        agent_id: "codex",
        status,
        workspace: "/workspace",
        messages: [],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:00Z",
      });
      fetchMock.mockImplementation(
        withSessions([{ id: "chat_a", title: "A" }], {
          "/hecate/v1/chat/sessions/chat_a": (init) => {
            if (init?.method === "PATCH") blockedRequestCounts.rename += 1;
            else sessionGetCount += 1;
            return jsonResponse({ object: "chat_session", data: session("running") });
          },
          "/hecate/v1/chat/sessions/chat_a/cancel": () => {
            cancelRequestCount += 1;
            return deferredCancel;
          },
          "/hecate/v1/chat/sessions/chat_a/messages": () => {
            messagePostCount += 1;
            return jsonResponse({ object: "chat_session", data: session("completed") });
          },
          "/hecate/v1/chat/sessions/chat_a/compact": () => {
            blockedRequestCounts.compact += 1;
            return jsonResponse({ object: "chat_session", data: session("completed") });
          },
          "/hecate/v1/chat/sessions/chat_a/config-options/model": () => {
            blockedRequestCounts.config += 1;
            return jsonResponse({ object: "chat_session", data: session("completed") });
          },
          "/hecate/v1/chat/sessions/chat_a/workspace-diff/revert": () => {
            blockedRequestCounts.workspaceRevert += 1;
            return jsonResponse({
              object: "chat_workspace_diff",
              data: { revision: "sha256:after-revert", has_changes: false, files: [] },
            });
          },
          "/hecate/v1/chat/sessions/chat_a/approvals/approval_1/resolve": () => {
            blockedRequestCounts.resolveApproval += 1;
            return jsonResponse({ object: "chat_approval", data: {} });
          },
          "/hecate/v1/chat/sessions/chat_a/approvals/approval_1/cancel": () => {
            blockedRequestCounts.cancelApproval += 1;
            return jsonResponse({ object: "chat_approval", data: {} });
          },
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_a");
      });
      act(() => result.current.chat.actions.setChatLoading(true));

      let cancellation!: Promise<void>;
      act(() => {
        cancellation = result.current.runtimeConsole.actions.cancelAgentChat();
      });
      await waitFor(() => expect(cancelRequestCount).toBe(1));
      expect(result.current.chat.state.chatCancelling).toBe(true);

      act(() => {
        result.current.chat.actions.setActiveChatSession(session("completed"));
        result.current.chat.actions.setChatLoading(false);
      });

      expect(result.current.chat.state.chatCancelling).toBe(true);
      expect(result.current.chat.state.chatCancellingSessionID).toBe("chat_a");
      const selectedModel = result.current.runtimeConsole.state.model;
      const selectedProvider = result.current.runtimeConsole.state.providerFilter;
      const sessionReadsBeforeBlockedActions = sessionGetCount;
      act(() => {
        result.current.chat.actions.setQueuedChatMessages([
          {
            id: "queued_check",
            session_id: "chat_a",
            content: "queued",
            created_at: "2026-04-20T00:00:01Z",
            delivery_state: "reconcile_required",
          } as any,
        ]);
        result.current.runtimeConsole.actions.setProviderFilter("openai");
        result.current.runtimeConsole.actions.setModel("different-model");
      });
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_a");
        await result.current.runtimeConsole.actions.reconcileQueuedChatMessage("queued_check");
        await result.current.runtimeConsole.actions.compactChatSession("chat_a");
        await result.current.runtimeConsole.actions.setChatConfigOption("chat_a", "model", "fast");
        await result.current.runtimeConsole.actions.revertChatWorkspaceFiles(
          "chat_a",
          [],
          "sha256:reviewed",
        );
        await result.current.runtimeConsole.actions.resolveChatApproval("chat_a", "approval_1", {
          decision: "approve",
          scope: "once",
        });
        await result.current.runtimeConsole.actions.cancelChatApproval("chat_a", "approval_1");
        await result.current.runtimeConsole.actions.renameChatSession("chat_a", "Renamed");
      });
      expect(sessionGetCount).toBe(sessionReadsBeforeBlockedActions);
      expect(blockedRequestCounts).toEqual({
        cancelApproval: 0,
        compact: 0,
        config: 0,
        rename: 0,
        resolveApproval: 0,
        workspaceRevert: 0,
      });
      expect(result.current.runtimeConsole.state.model).toBe(selectedModel);
      expect(result.current.runtimeConsole.state.providerFilter).toBe(selectedProvider);
      await act(async () => {
        await result.current.runtimeConsole.actions.cancelAgentChat();
      });
      expect(cancelRequestCount).toBe(1);

      act(() => result.current.runtimeConsole.actions.setMessage("start another response"));
      await act(async () => {
        await result.current.runtimeConsole.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(messagePostCount).toBe(0);
      expect(result.current.runtimeConsole.state.message).toBe("start another response");
      expect(result.current.runtimeConsole.state.chatErrorCode).toBe("chat.cancellation_in_flight");
      expect(result.current.chat.state.chatCancelling).toBe(true);

      await act(async () => {
        releaseCancel?.(jsonResponse({ object: "chat_session", data: session("completed") }));
        await cancellation;
      });

      expect(result.current.chat.state.chatCancelling).toBe(false);
      expect(result.current.chat.state.chatCancellingSessionID).toBe("");
    });

    it("projects a successful Stop summary after the operator switches chats", async () => {
      let releaseCancel: ((response: Response) => void) | undefined;
      const deferredCancel = new Promise<Response>((resolve) => {
        releaseCancel = resolve;
      });
      let stopAccepted = false;
      const session = (id: string, status: "running" | "completed" | "cancelled", title: string) =>
        jsonResponse({
          object: "chat_session",
          data: {
            id,
            title,
            agent_id: id === "chat_a" ? "codex" : "hecate",
            status,
            workspace: "/workspace",
            messages: [],
            created_at: "2026-04-20T00:00:00Z",
            updated_at: "2026-04-20T00:00:01Z",
          },
        });
      fetchMock.mockImplementation(
        withSessions(
          [
            { id: "chat_a", title: "A" },
            { id: "chat_b", title: "B" },
          ],
          {
            "/hecate/v1/chat/sessions/chat_a": () =>
              stopAccepted
                ? session("chat_a", "cancelled", "A stopped")
                : session("chat_a", "running", "A"),
            "/hecate/v1/chat/sessions/chat_b": () => session("chat_b", "completed", "B"),
            "/hecate/v1/chat/sessions/chat_a/cancel": () => deferredCancel,
          },
        ),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_a");
      });

      let cancellation!: Promise<void>;
      act(() => {
        cancellation = result.current.runtimeConsole.actions.cancelAgentChat();
      });
      await waitFor(() => expect(result.current.chat.state.chatCancelling).toBe(true));
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_b");
      });

      await act(async () => {
        stopAccepted = true;
        releaseCancel?.(session("chat_a", "running", "stale acknowledgement"));
        await cancellation;
      });

      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("chat_b");
      expect(result.current.runtimeConsole.state.activeChatSession?.id).toBe("chat_b");
      expect(
        result.current.runtimeConsole.state.chatSessions.find((entry) => entry.id === "chat_a"),
      ).toMatchObject({ title: "A stopped", status: "cancelled" });
      expect(result.current.chat.state.chatCancelling).toBe(false);
    });

    it("accepts post-Stop dashboard terminal proof and authoritative omission", async () => {
      window.localStorage.setItem("hecate.chatSessionID", "chat_a");
      let stoppedDashboard = false;
      let dashboardOmitsSession = false;
      const session = (title: string, status: "running" | "cancelled") => ({
        id: "chat_a",
        title,
        agent_id: "codex",
        status,
        workspace: "/workspace",
        messages: [
          {
            id: "assistant_1",
            role: "assistant" as const,
            content: status === "running" ? "Working" : "Stopped",
            status,
          },
        ],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:01Z",
      });
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () => {
            if (dashboardOmitsSession) {
              return jsonResponse({ object: "chat_sessions", data: [] });
            }
            const current = stoppedDashboard
              ? session("Stopped by dashboard", "cancelled")
              : session("Live turn", "running");
            return jsonResponse({
              object: "chat_sessions",
              data: [{ ...current, messages: undefined, message_count: 1 }],
            });
          },
          "/hecate/v1/chat/sessions/chat_a": () =>
            jsonResponse({
              object: "chat_session",
              data: stoppedDashboard
                ? session("Stopped by dashboard", "cancelled")
                : session("Live turn", "running"),
            }),
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await waitFor(() =>
        expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
          id: "chat_a",
          title: "Live turn",
        }),
      );

      let fence!: ReturnType<typeof result.current.chat.actions.beginChatStopFence>;
      act(() => {
        fence = result.current.chat.actions.beginChatStopFence({
          token: 17,
          sessionID: "chat_a",
          turnGeneration: 23,
        });
        result.current.chat.actions.acceptChatStopFence(fence);
      });
      stoppedDashboard = true;

      await act(async () => {
        await result.current.runtimeConsole.actions.loadDashboard();
      });

      expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
        title: "Stopped by dashboard",
        status: "cancelled",
      });
      expect(
        result.current.runtimeConsole.state.chatSessions.find((entry) => entry.id === "chat_a"),
      ).toMatchObject({ title: "Stopped by dashboard", status: "cancelled" });
      expect(fence.phase).toBe("settled");

      let omissionFence!: ReturnType<typeof result.current.chat.actions.beginChatStopFence>;
      act(() => {
        omissionFence = result.current.chat.actions.beginChatStopFence({
          token: 18,
          sessionID: "chat_a",
          turnGeneration: 24,
        });
        result.current.chat.actions.acceptChatStopFence(omissionFence);
      });
      dashboardOmitsSession = true;
      await act(async () => {
        await result.current.runtimeConsole.actions.loadDashboard();
      });
      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("");
      expect(result.current.runtimeConsole.state.activeChatSession).toBeNull();
      expect(
        result.current.runtimeConsole.state.chatSessions.some((entry) => entry.id === "chat_a"),
      ).toBe(false);
      expect(result.current.chat.actions.getChatStopFence("chat_a")).toBeNull();
      expect(result.current.chat.actions.isChatSessionDeleted("chat_a")).toBe(true);
    });

    it("keeps a dashboard omission that began before Stop acceptance fenced", async () => {
      window.localStorage.setItem("hecate.chatSessionID", "chat_a");
      const session = {
        id: "chat_a",
        title: "Live turn",
        agent_id: "codex",
        status: "running",
        workspace: "/workspace",
        messages: [
          {
            id: "assistant_1",
            role: "assistant" as const,
            content: "Working",
            status: "running",
          },
        ],
      };
      let listReadCount = 0;
      let secondListReadStarted = false;
      let releaseSecondListRead: ((response: Response) => void) | undefined;
      const secondListRead = new Promise<Response>((resolve) => {
        releaseSecondListRead = resolve;
      });
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () => {
            listReadCount += 1;
            if (listReadCount > 1) {
              secondListReadStarted = true;
              return secondListRead;
            }
            return jsonResponse({
              object: "chat_sessions",
              data: [{ ...session, messages: undefined, message_count: 1 }],
            });
          },
          "/hecate/v1/chat/sessions/chat_a": () =>
            jsonResponse({ object: "chat_session", data: session }),
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await waitFor(() =>
        expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("chat_a"),
      );

      let dashboardLoad!: Promise<void>;
      act(() => {
        dashboardLoad = result.current.runtimeConsole.actions.loadDashboard();
      });
      await waitFor(() => expect(secondListReadStarted).toBe(true));
      let fence!: ReturnType<typeof result.current.chat.actions.beginChatStopFence>;
      act(() => {
        fence = result.current.chat.actions.beginChatStopFence({
          token: 27,
          sessionID: "chat_a",
          turnGeneration: 29,
        });
        result.current.chat.actions.acceptChatStopFence(fence);
      });

      await act(async () => {
        releaseSecondListRead?.(
          jsonResponse({
            object: "chat_sessions",
            data: [],
          }),
        );
        await dashboardLoad;
      });

      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("chat_a");
      expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
        id: "chat_a",
        title: "Live turn",
      });
      expect(
        result.current.runtimeConsole.state.chatSessions.find((entry) => entry.id === "chat_a"),
      ).toMatchObject({ title: "Live turn", status: "running" });
      expect(result.current.chat.actions.getChatStopFence("chat_a")).toBe(fence);
      expect(fence.phase).toBe("accepted");

      act(() => {
        result.current.chat.actions.clearChatStopFence(fence);
      });
    });

    it.each(["session list", "active detail"] as const)(
      "does not authorize Stop tokens for failed dashboard %s fallbacks",
      async (failedRead) => {
        window.localStorage.setItem("hecate.chatSessionID", "chat_a");
        const session = (status: "running" | "cancelled") => ({
          id: "chat_a",
          title: status === "running" ? "Live turn" : "Stale terminal fallback",
          agent_id: "codex",
          status,
          workspace: "/workspace",
          messages: [
            {
              id: "assistant_1",
              role: "assistant" as const,
              content: status === "running" ? "Working" : "Stopped",
              status,
            },
          ],
        });
        let failDashboardRead = false;
        fetchMock.mockImplementation(
          defaultBackendMock({
            "/hecate/v1/chat/sessions": () => {
              if (failDashboardRead && failedRead === "session list") {
                throw new TypeError("chat-session list unavailable");
              }
              return jsonResponse({
                object: "chat_sessions",
                data: [{ ...session("running"), messages: undefined, message_count: 1 }],
              });
            },
            "/hecate/v1/chat/sessions/chat_a": () => {
              if (failDashboardRead && failedRead === "active detail") {
                throw new TypeError("active chat unavailable");
              }
              return jsonResponse({ object: "chat_session", data: session("running") });
            },
          }),
        );

        const { result } = renderRuntimeConsoleWithChatHook();
        await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
        await waitFor(() =>
          expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("chat_a"),
        );
        let fence!: ReturnType<typeof result.current.chat.actions.beginChatStopFence>;
        act(() => {
          fence = result.current.chat.actions.beginChatStopFence({
            token: 37,
            sessionID: "chat_a",
            turnGeneration: 39,
          });
          result.current.chat.actions.acceptChatStopFence(fence);
          if (failedRead === "session list") {
            const { messages: _messages, ...terminalSummary } = session("cancelled");
            result.current.chat.actions.setChatSessions([{ ...terminalSummary, message_count: 1 }]);
          }
          result.current.chat.actions.setActiveChatSession(session("cancelled"));
        });
        failDashboardRead = true;

        await act(async () => {
          await result.current.runtimeConsole.actions.loadDashboard();
        });

        expect(result.current.chat.actions.getChatStopFence("chat_a")).toBe(fence);
        expect(fence.phase).toBe("accepted");
        expect(result.current.chat.actions.isChatSessionDeleted("chat_a")).toBe(false);
        act(() => {
          result.current.chat.actions.clearChatStopFence(fence);
        });
      },
    );

    it("treats a post-Stop active-detail 404 as authoritative absence", async () => {
      window.localStorage.setItem("hecate.chatSessionID", "chat_a");
      const session = {
        id: "chat_a",
        title: "Live turn",
        agent_id: "codex",
        status: "running",
        workspace: "/workspace",
        messages: [
          {
            id: "assistant_1",
            role: "assistant" as const,
            content: "Working",
            status: "running",
          },
        ],
      };
      let activeMissing = false;
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () =>
            jsonResponse({
              object: "chat_sessions",
              data: [
                {
                  ...session,
                  title: activeMissing ? "Terminal summary before 404" : session.title,
                  status: activeMissing ? "cancelled" : session.status,
                  messages: undefined,
                  message_count: 1,
                },
              ],
            }),
          "/hecate/v1/chat/sessions/chat_a": () =>
            activeMissing
              ? jsonResponse(
                  { error: { type: "not_found", message: "chat session not found" } },
                  404,
                )
              : jsonResponse({ object: "chat_session", data: session }),
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await waitFor(() =>
        expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("chat_a"),
      );
      let fence!: ReturnType<typeof result.current.chat.actions.beginChatStopFence>;
      act(() => {
        fence = result.current.chat.actions.beginChatStopFence({
          token: 47,
          sessionID: "chat_a",
          turnGeneration: 49,
        });
        result.current.chat.actions.acceptChatStopFence(fence);
      });
      activeMissing = true;

      await act(async () => {
        await result.current.runtimeConsole.actions.loadDashboard();
      });

      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("");
      expect(result.current.runtimeConsole.state.activeChatSession).toBeNull();
      expect(
        result.current.runtimeConsole.state.chatSessions.some((entry) => entry.id === "chat_a"),
      ).toBe(false);
      expect(result.current.chat.actions.getChatStopFence("chat_a")).toBeNull();
      expect(result.current.chat.actions.isChatSessionDeleted("chat_a")).toBe(true);
    });

    it("does not run composition approval catch-up when selecting a Stop-fenced chat", async () => {
      let approvalReadCount = 0;
      const session = {
        id: "chat_a",
        title: "Stop-fenced chat",
        agent_id: "codex",
        status: "running",
        workspace: "/workspace",
        messages: [],
      };
      fetchMock.mockImplementation(
        defaultBackendMock({
          "/hecate/v1/chat/sessions": () =>
            jsonResponse({
              object: "chat_sessions",
              data: [{ ...session, message_count: 1 }],
            }),
          "/hecate/v1/chat/sessions/chat_a": () =>
            jsonResponse({ object: "chat_session", data: session }),
          "/hecate/v1/chat/sessions/chat_a/approvals?status=pending": () => {
            approvalReadCount += 1;
            return jsonResponse({ object: "chat_approvals", data: [] });
          },
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      let fence!: ReturnType<typeof result.current.chat.actions.beginChatStopFence>;
      act(() => {
        fence = result.current.chat.actions.beginChatStopFence({
          token: 57,
          sessionID: "chat_a",
          turnGeneration: 59,
        });
        result.current.chat.actions.acceptChatStopFence(fence);
      });

      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_a");
        await Promise.resolve();
      });

      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("chat_a");
      expect(approvalReadCount).toBe(0);
      expect(result.current.chat.actions.getChatStopFence("chat_a")).toBe(fence);
      act(() => {
        result.current.chat.actions.clearChatStopFence(fence);
      });
    });

    it("keeps an accepted Stop fenced through stale acknowledgements and exact-turn updates", async () => {
      window.localStorage.setItem("hecate.chatTarget", "external_agent");
      window.localStorage.setItem("hecate.agentAdapterID", "codex");
      let streamController!: ReadableStreamDefaultController<Uint8Array>;
      const streamResponse = new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            streamController = controller;
          },
        }),
        { status: 200, headers: { "Content-Type": "text/event-stream" } },
      );
      const encoder = new TextEncoder();
      const emitStreamEvent = (event: string, payload: unknown) => {
        streamController.enqueue(
          encoder.encode(`event: ${event}\ndata: ${JSON.stringify(payload)}\n\n`),
        );
      };
      let releaseMessagePost: ((response: Response) => void) | undefined;
      const deferredMessagePost = new Promise<Response>((resolve) => {
        releaseMessagePost = resolve;
      });
      const hangingSessionRead = new Promise<Response>(() => undefined);
      let stopAccepted = false;
      let messagePostStarted = false;
      const session = (status: "completed" | "running" | "cancelled", title: string) => ({
        id: "chat_a",
        title,
        agent_id: "codex",
        workspace: "/workspace",
        status,
        messages:
          status === "completed"
            ? []
            : [
                { id: "user_1", role: "user", content: "run until stopped", status: "completed" },
                {
                  id: "assistant_1",
                  role: "assistant",
                  content: status === "running" ? "working" : "stopped",
                  status,
                },
              ],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: status === "completed" ? "2026-04-20T00:00:00Z" : "2026-04-20T00:00:01Z",
      });
      const pendingApproval = {
        approval_id: "approval_1",
        session_id: "chat_a",
        adapter_id: "codex",
        tool_kind: "fs",
        tool_name: "write_file",
        scope_choices: ["once"],
        created_at: "2026-04-20T00:00:00Z",
        expires_at: "2026-04-20T00:05:00Z",
      };

      fetchMock.mockImplementation(
        withSessions([{ id: "chat_a", title: "Before" }], {
          "/hecate/v1/agent-adapters": () =>
            jsonResponse({
              object: "agent_adapters",
              data: [{ id: "codex", name: "Codex", kind: "acp", available: true }],
            }),
          "/hecate/v1/chat/sessions/chat_a": () =>
            stopAccepted
              ? hangingSessionRead
              : jsonResponse({ object: "chat_session", data: session("completed", "Before") }),
          "/hecate/v1/chat/sessions/chat_a/stream": () => streamResponse,
          "/hecate/v1/chat/sessions/chat_a/messages": () => {
            messagePostStarted = true;
            return deferredMessagePost;
          },
          "/hecate/v1/chat/sessions/chat_a/cancel": () => {
            stopAccepted = true;
            return jsonResponse({
              object: "chat_session",
              data: session("running", "stale 202 acknowledgement"),
            });
          },
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_a");
      });
      act(() => {
        result.current.runtimeConsole.actions.setMessage("run until stopped");
      });
      let submission!: Promise<void>;
      act(() => {
        submission = result.current.runtimeConsole.actions.submitChat({
          preventDefault: vi.fn(),
        } as any);
      });
      await waitFor(() => expect(messagePostStarted).toBe(true));

      act(() => {
        emitStreamEvent("session_update", {
          object: "chat_session",
          data: session("running", "Live turn"),
        });
        emitStreamEvent("approval.requested", pendingApproval);
      });
      await waitFor(() => {
        expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
          status: "running",
          title: "Live turn",
        });
        expect(
          result.current.runtimeConsole.state.pendingApprovalsBySessionID.get("chat_a"),
        ).toHaveLength(1);
      });

      let cancellation!: Promise<void>;
      act(() => {
        cancellation = result.current.runtimeConsole.actions.cancelAgentChat();
      });
      await waitFor(() => {
        expect(stopAccepted).toBe(true);
        expect(result.current.chat.state.chatCancelling).toBe(true);
        expect(result.current.runtimeConsole.state.pendingApprovalsBySessionID.has("chat_a")).toBe(
          false,
        );
      });

      act(() => {
        emitStreamEvent("approval.requested", {
          ...pendingApproval,
          approval_id: "approval_stale",
        });
        emitStreamEvent("session_update", {
          object: "chat_session",
          data: session("running", "stale running SSE"),
        });
        releaseMessagePost?.(
          jsonResponse({
            object: "chat_session",
            data: session("running", "stale running POST"),
          }),
        );
      });
      await act(async () => {
        await Promise.resolve();
      });
      expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
        status: "running",
        title: "Live turn",
      });
      expect(result.current.runtimeConsole.state.pendingApprovalsBySessionID.has("chat_a")).toBe(
        false,
      );
      expect(result.current.chat.state.chatCancelling).toBe(true);

      act(() => {
        emitStreamEvent("session_update", {
          object: "chat_session",
          data: session("cancelled", "Stopped authoritatively"),
        });
        emitStreamEvent("session_update", {
          object: "chat_session",
          data: session("running", "late running SSE"),
        });
        emitStreamEvent("approval.requested", {
          ...pendingApproval,
          approval_id: "approval_late",
        });
        streamController.close();
      });
      await act(async () => {
        await Promise.all([submission, cancellation]);
      });

      expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
        status: "cancelled",
        title: "Stopped authoritatively",
      });
      expect(
        result.current.runtimeConsole.state.chatSessions.find((entry) => entry.id === "chat_a"),
      ).toMatchObject({ status: "cancelled", title: "Stopped authoritatively" });
      expect(result.current.runtimeConsole.state.pendingApprovalsBySessionID.has("chat_a")).toBe(
        false,
      );
      expect(result.current.chat.state.chatCancelling).toBe(false);
    });

    it("preserves an accepted terminal fence while a retrying Stop is unresolved or fails", async () => {
      window.localStorage.setItem("hecate.chatTarget", "external_agent");
      window.localStorage.setItem("hecate.agentAdapterID", "codex");
      let streamController!: ReadableStreamDefaultController<Uint8Array>;
      const streamResponse = new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            streamController = controller;
          },
        }),
        { status: 200, headers: { "Content-Type": "text/event-stream" } },
      );
      const encoder = new TextEncoder();
      const emitStreamEvent = (event: string, payload: unknown) => {
        streamController.enqueue(
          encoder.encode(`event: ${event}\ndata: ${JSON.stringify(payload)}\n\n`),
        );
      };
      let releaseMessagePost: ((response: Response) => void) | undefined;
      const deferredMessagePost = new Promise<Response>((resolve) => {
        releaseMessagePost = resolve;
      });
      let releaseRetry: ((response: Response) => void) | undefined;
      const deferredRetry = new Promise<Response>((resolve) => {
        releaseRetry = resolve;
      });
      const hangingSessionRead = new Promise<Response>(() => undefined);
      let cancelRequests = 0;
      let messagePostStarted = false;
      const session = (status: "completed" | "running" | "cancelled", title: string) => ({
        id: "chat_a",
        title,
        agent_id: "codex",
        workspace: "/workspace",
        status,
        messages:
          status === "completed"
            ? []
            : [
                { id: "user_1", role: "user", content: "keep working", status: "completed" },
                {
                  id: "assistant_1",
                  role: "assistant",
                  content: status === "running" ? "working" : "stopped",
                  status,
                },
              ],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:01Z",
      });
      const staleApproval = {
        approval_id: "approval_stale",
        session_id: "chat_a",
        adapter_id: "codex",
        tool_kind: "fs",
        tool_name: "write_file",
        scope_choices: ["once"],
        created_at: "2026-04-20T00:00:00Z",
        expires_at: "2026-04-20T00:05:00Z",
      };
      fetchMock.mockImplementation(
        withSessions([{ id: "chat_a", title: "Before" }], {
          "/hecate/v1/agent-adapters": () =>
            jsonResponse({
              object: "agent_adapters",
              data: [{ id: "codex", name: "Codex", kind: "acp", available: true }],
            }),
          "/hecate/v1/chat/sessions/chat_a": () =>
            cancelRequests > 0
              ? hangingSessionRead
              : jsonResponse({ object: "chat_session", data: session("completed", "Before") }),
          "/hecate/v1/chat/sessions/chat_a/stream": () => streamResponse,
          "/hecate/v1/chat/sessions/chat_a/messages": () => {
            messagePostStarted = true;
            return deferredMessagePost;
          },
          "/hecate/v1/chat/sessions/chat_a/cancel": () => {
            cancelRequests += 1;
            return cancelRequests === 1
              ? jsonResponse({
                  object: "chat_session",
                  data: session("running", "stale first acknowledgement"),
                })
              : deferredRetry;
          },
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_a");
      });
      act(() => {
        result.current.runtimeConsole.actions.setMessage("keep working");
      });
      let submission!: Promise<void>;
      act(() => {
        submission = result.current.runtimeConsole.actions.submitChat({
          preventDefault: vi.fn(),
        } as any);
      });
      await waitFor(() => expect(messagePostStarted).toBe(true));
      act(() => {
        emitStreamEvent("session_update", {
          object: "chat_session",
          data: session("running", "Live turn"),
        });
      });
      await waitFor(() =>
        expect(result.current.chat.state.chatTurnCancellationAvailable).toBe(true),
      );

      vi.useFakeTimers();
      try {
        let firstCancellation!: Promise<void>;
        await act(async () => {
          firstCancellation = result.current.runtimeConsole.actions.cancelAgentChat();
          await vi.advanceTimersByTimeAsync(0);
        });
        expect(cancelRequests).toBe(1);
        expect(result.current.chat.state.chatCancelling).toBe(true);
        await act(async () => {
          await vi.advanceTimersByTimeAsync(2_000);
          await firstCancellation;
        });
        expect(result.current.chat.state.chatCancelling).toBe(false);
      } finally {
        vi.useRealTimers();
      }

      let retryCancellation!: Promise<void>;
      act(() => {
        retryCancellation = result.current.runtimeConsole.actions.cancelAgentChat();
      });
      await waitFor(() => {
        expect(cancelRequests).toBe(2);
        expect(result.current.chat.state.chatCancelling).toBe(true);
      });
      act(() => {
        emitStreamEvent("session_update", {
          object: "chat_session",
          data: session("running", "stale retry SSE"),
        });
        emitStreamEvent("approval.requested", staleApproval);
      });
      await act(async () => {
        await Promise.resolve();
      });
      expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
        status: "running",
        title: "Live turn",
      });
      expect(result.current.runtimeConsole.state.pendingApprovalsBySessionID.has("chat_a")).toBe(
        false,
      );

      let submissionSettled = false;
      void submission.then(
        () => {
          submissionSettled = true;
        },
        () => {
          submissionSettled = true;
        },
      );
      act(() => {
        releaseMessagePost?.(
          jsonResponse({
            object: "chat_session",
            data: session("running", "stale retry POST"),
          }),
        );
      });
      await act(async () => {
        await Promise.resolve();
      });
      expect(submissionSettled).toBe(false);
      expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
        status: "running",
        title: "Live turn",
      });

      await act(async () => {
        releaseRetry?.(jsonResponse({ error: { message: "retry unavailable" } }, 503));
        await retryCancellation;
      });
      expect(result.current.chat.state.chatCancelling).toBe(false);
      expect(result.current.runtimeConsole.state.chatError).toContain("retry unavailable");
      expect(submissionSettled).toBe(false);
      expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
        status: "running",
        title: "Live turn",
      });

      act(() => {
        emitStreamEvent("session_update", {
          object: "chat_session",
          data: session("cancelled", "Stopped after retry failure"),
        });
        streamController.close();
      });
      await act(async () => {
        await submission;
      });
      expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
        status: "cancelled",
        title: "Stopped after retry failure",
      });
      expect(result.current.runtimeConsole.state.pendingApprovalsBySessionID.has("chat_a")).toBe(
        false,
      );
    });

    it.each(["rejects", "never settles"] as const)(
      "releases failed Stop ownership when independent approval catch-up %s",
      async (approvalReadOutcome) => {
        let approvalMode: "seed" | "reject" | "hang" = "seed";
        let catchUpReads = 0;
        let sessionCatchUpReads = 0;
        let stopFailed = false;
        const hangingSessionRead = new Promise<Response>(() => undefined);
        const pendingApproval = {
          id: "approval_1",
          session_id: "chat_a",
          adapter_id: "codex",
          tool_kind: "fs",
          tool_name: "write_file",
          status: "pending",
          acp_options: [],
          scope_choices: ["once"],
          created_at: "2026-04-20T00:00:00Z",
          expires_at: "2026-04-20T00:05:00Z",
        };
        const session = (id: string, status: "running" | "completed") => ({
          id,
          title: id === "chat_a" ? "External Agent chat" : "Unrelated chat",
          agent_id: id === "chat_a" ? "codex" : "hecate",
          status,
          workspace: "/workspace",
          messages: [],
          created_at: "2026-04-20T00:00:00Z",
          updated_at: "2026-04-20T00:00:01Z",
        });
        fetchMock.mockImplementation(
          withSessions(
            [
              { id: "chat_a", title: "A" },
              { id: "chat_b", title: "B" },
            ],
            {
              "/hecate/v1/chat/sessions/chat_a": () => {
                if (stopFailed) {
                  sessionCatchUpReads += 1;
                  return hangingSessionRead;
                }
                return jsonResponse({ object: "chat_session", data: session("chat_a", "running") });
              },
              "/hecate/v1/chat/sessions/chat_b": () =>
                jsonResponse({ object: "chat_session", data: session("chat_b", "completed") }),
              "/hecate/v1/chat/sessions/chat_a/cancel": () => {
                stopFailed = true;
                return jsonResponse({ error: { message: "cancel unavailable" } }, 503);
              },
              "/hecate/v1/chat/sessions/chat_a/approvals?status=pending": () => {
                if (approvalMode === "seed") {
                  return jsonResponse({ object: "list", data: [pendingApproval] });
                }
                catchUpReads += 1;
                if (approvalMode === "reject") return Promise.reject(new Error("read failed"));
                return new Promise<Response>(() => undefined);
              },
            },
          ),
        );

        const { result } = renderRuntimeConsoleWithChatHook();
        await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
        await act(async () => {
          await result.current.runtimeConsole.actions.selectChatSession("chat_a");
        });
        await waitFor(() =>
          expect(
            result.current.runtimeConsole.state.pendingApprovalsBySessionID.get("chat_a"),
          ).toHaveLength(1),
        );
        approvalMode = approvalReadOutcome === "rejects" ? "reject" : "hang";

        await act(async () => {
          await result.current.runtimeConsole.actions.cancelAgentChat();
        });

        expect(result.current.chat.state.chatCancelling).toBe(false);
        expect(result.current.chat.state.chatCancellingSessionID).toBe("");
        expect(result.current.runtimeConsole.state.chatError).toContain("cancel unavailable");
        expect(
          result.current.runtimeConsole.state.pendingApprovalsBySessionID.get("chat_a"),
        ).toHaveLength(1);
        await waitFor(() => expect(catchUpReads).toBe(1));
        expect(sessionCatchUpReads).toBe(1);

        await act(async () => {
          expect(await result.current.runtimeConsole.actions.selectChatSession("chat_b")).toBe(
            true,
          );
        });
        expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("chat_b");
      },
    );

    it("discards a failed-Stop approval catch-up after a newer Stop is accepted", async () => {
      let approvalMode: "seed" | "catch-up" = "seed";
      let catchUpReads = 0;
      let releaseStaleRead: ((response: Response) => void) | undefined;
      const staleRead = new Promise<Response>((resolve) => {
        releaseStaleRead = resolve;
      });
      let cancelRequests = 0;
      const pendingApproval = {
        id: "approval_1",
        session_id: "chat_a",
        adapter_id: "codex",
        tool_kind: "fs",
        tool_name: "write_file",
        status: "pending",
        acp_options: [],
        scope_choices: ["once"],
        created_at: "2026-04-20T00:00:00Z",
        expires_at: "2026-04-20T00:05:00Z",
      };
      const session = (status: "running" | "cancelled") => ({
        id: "chat_a",
        title: status === "cancelled" ? "Stopped" : "External Agent chat",
        agent_id: "codex",
        status,
        workspace: "/workspace",
        messages: [],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:00Z",
      });
      fetchMock.mockImplementation(
        withSessions([{ id: "chat_a", title: "A" }], {
          "/hecate/v1/chat/sessions/chat_a": () =>
            jsonResponse({
              object: "chat_session",
              data: session(cancelRequests >= 2 ? "cancelled" : "running"),
            }),
          "/hecate/v1/chat/sessions/chat_a/cancel": () => {
            cancelRequests += 1;
            return cancelRequests === 1
              ? jsonResponse({ error: { message: "first Stop failed" } }, 503)
              : jsonResponse({ object: "chat_session", data: session("running") });
          },
          "/hecate/v1/chat/sessions/chat_a/approvals?status=pending": () => {
            if (approvalMode === "seed") {
              return jsonResponse({ object: "list", data: [pendingApproval] });
            }
            catchUpReads += 1;
            return staleRead;
          },
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_a");
      });
      await waitFor(() =>
        expect(
          result.current.runtimeConsole.state.pendingApprovalsBySessionID.get("chat_a"),
        ).toHaveLength(1),
      );
      approvalMode = "catch-up";

      await act(async () => {
        await result.current.runtimeConsole.actions.cancelAgentChat();
      });
      await waitFor(() => expect(catchUpReads).toBe(1));
      expect(
        result.current.runtimeConsole.state.pendingApprovalsBySessionID.get("chat_a"),
      ).toHaveLength(1);

      await act(async () => {
        await result.current.runtimeConsole.actions.cancelAgentChat();
      });
      expect(cancelRequests).toBe(2);
      expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
        status: "cancelled",
        title: "Stopped",
      });
      expect(result.current.runtimeConsole.state.pendingApprovalsBySessionID.has("chat_a")).toBe(
        false,
      );

      await act(async () => {
        releaseStaleRead?.(jsonResponse({ object: "list", data: [pendingApproval] }));
        await Promise.resolve();
      });
      expect(result.current.runtimeConsole.state.pendingApprovalsBySessionID.has("chat_a")).toBe(
        false,
      );
    });

    it("blocks matching Hecate settings and task approval while Stop owns the session", async () => {
      let releaseCancel: ((response: Response) => void) | undefined;
      const deferredCancel = new Promise<Response>((resolve) => {
        releaseCancel = resolve;
      });
      let settingsRequestCount = 0;
      let taskApprovalRequestCount = 0;
      const session = (status: "running" | "cancelled") => ({
        id: "chat_hecate",
        title: "Hecate chat",
        agent_id: "hecate",
        task_id: "task_1",
        latest_run_id: "run_1",
        status,
        rtk_enabled: false,
        messages: [],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:00Z",
      });
      fetchMock.mockImplementation(
        withSessions([{ id: "chat_hecate", title: "Hecate chat" }], {
          "/hecate/v1/chat/sessions/chat_hecate": () =>
            jsonResponse({ object: "chat_session", data: session("running") }),
          "/hecate/v1/chat/sessions/chat_hecate/cancel": () => deferredCancel,
          "/hecate/v1/chat/sessions/chat_hecate/settings": () => {
            settingsRequestCount += 1;
            return jsonResponse({ object: "chat_session", data: session("running") });
          },
          "/hecate/v1/tasks/task_1/approvals/approval_1/resolve": () => {
            taskApprovalRequestCount += 1;
            return jsonResponse({ object: "ok" });
          },
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_hecate");
      });

      let cancellation!: Promise<void>;
      act(() => {
        cancellation = result.current.runtimeConsole.actions.cancelAgentChat();
      });
      await waitFor(() => expect(result.current.chat.state.chatCancelling).toBe(true));
      await act(async () => {
        expect(await result.current.runtimeConsole.actions.setHecateRTKEnabled(true)).toBe(false);
        expect(
          await result.current.runtimeConsole.actions.resolveTaskApproval("task_1", "approval_1", {
            decision: "approve",
          }),
        ).toBe(false);
      });

      expect(settingsRequestCount).toBe(0);
      expect(taskApprovalRequestCount).toBe(0);
      expect(result.current.runtimeConsole.state.hecateRTKEnabled).toBe(false);

      await act(async () => {
        releaseCancel?.(jsonResponse({ object: "chat_session", data: session("cancelled") }));
        await cancellation;
      });
    });

    it("allows a Tasks approval when Stop owns an unrelated chat session", async () => {
      let taskApprovalRequestCount = 0;
      const taskSession = {
        id: "chat_task",
        title: "Task-backed chat",
        agent_id: "hecate",
        task_id: "task_1",
        workspace: "/workspace",
        status: "awaiting_approval",
        messages: [],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:00Z",
      };
      const stoppingSession = {
        id: "chat_stopping",
        title: "Stopping chat",
        agent_id: "hecate",
        workspace: "/workspace",
        status: "running",
        messages: [],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:00Z",
      };
      fetchMock.mockImplementation(
        withSessions([taskSession, stoppingSession], {
          "/hecate/v1/chat/sessions/chat_task": () =>
            jsonResponse({ object: "chat_session", data: taskSession }),
          "/hecate/v1/chat/sessions/chat_stopping": () =>
            jsonResponse({ object: "chat_session", data: stoppingSession }),
          "/hecate/v1/tasks/task_1/approvals/approval_1/resolve": () => {
            taskApprovalRequestCount += 1;
            return new Response(null, { status: 204 });
          },
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_stopping");
      });

      let owner: ChatCancellationOwner | null = null;
      act(() => {
        owner = result.current.chat.actions.beginChatCancellation("chat_stopping");
      });
      expect(owner).not.toBeNull();
      if (!owner) throw new Error("expected cancellation ownership");
      const cancellationOwner = owner;

      let resolved = false;
      await act(async () => {
        resolved = await result.current.runtimeConsole.actions.resolveTaskApproval(
          "task_1",
          "approval_1",
          { decision: "approve" },
        );
      });

      expect(resolved).toBe(true);
      expect(taskApprovalRequestCount).toBe(1);
      expect(result.current.chat.state.chatCancellingSessionID).toBe("chat_stopping");

      act(() => {
        result.current.chat.actions.finishChatCancellation(cancellationOwner);
      });
    });

    it("refreshes authoritative session state when a pre-Stop rename response arrives last", async () => {
      let releaseRename: ((response: Response) => void) | undefined;
      const deferredRename = new Promise<Response>((resolve) => {
        releaseRename = resolve;
      });
      let serverTitle = "Original";
      let renameRequestCount = 0;
      let sessionGetCount = 0;
      const session = (status: "running" | "cancelled", title = serverTitle) => ({
        id: "chat_a",
        title,
        agent_id: "codex",
        status,
        workspace: "/workspace",
        messages: [],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:01Z",
      });
      fetchMock.mockImplementation(
        withSessions([{ id: "chat_a", title: "Original" }], {
          "/hecate/v1/chat/sessions/chat_a": (init) => {
            if (init?.method === "PATCH") {
              renameRequestCount += 1;
              return deferredRename;
            }
            sessionGetCount += 1;
            return jsonResponse({ object: "chat_session", data: session("cancelled") });
          },
          "/hecate/v1/chat/sessions/chat_a/cancel": () =>
            jsonResponse({ object: "chat_session", data: session("cancelled", "Original") }),
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_a");
      });
      act(() => {
        result.current.chat.actions.setActiveChatSession(session("running"));
      });

      let rename!: Promise<void>;
      act(() => {
        rename = result.current.runtimeConsole.actions.renameChatSession("chat_a", "Renamed");
      });
      await waitFor(() => expect(renameRequestCount).toBe(1));
      await act(async () => {
        await result.current.runtimeConsole.actions.cancelAgentChat();
      });
      expect(result.current.runtimeConsole.state.activeChatSession?.title).toBe("Original");

      serverTitle = "Renamed";
      await act(async () => {
        releaseRename?.(
          jsonResponse({ object: "chat_session", data: session("running", "Renamed") }),
        );
        await rename;
      });

      expect(sessionGetCount).toBe(3);
      expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
        status: "cancelled",
        title: "Renamed",
      });
      expect(result.current.runtimeConsole.state.chatSessions[0]?.title).toBe("Renamed");
    });

    it("applies a known-new config response after a Stop fence settles", async () => {
      let stopAccepted = false;
      let configRequests = 0;
      const session = (title: string, configValue: string) => ({
        id: "chat_a",
        title,
        agent_id: "codex",
        status: stopAccepted ? "cancelled" : "running",
        workspace: "/workspace",
        config_options: [
          { id: "model", name: "Model", type: "select", current_value: configValue },
        ],
        messages: [],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:01Z",
      });
      fetchMock.mockImplementation(
        withSessions([{ id: "chat_a", title: "Original" }], {
          "/hecate/v1/chat/sessions/chat_a": () =>
            jsonResponse({
              object: "chat_session",
              data: session(stopAccepted ? "Stopped" : "Original", "standard"),
            }),
          "/hecate/v1/chat/sessions/chat_a/cancel": () => {
            stopAccepted = true;
            return jsonResponse({
              object: "chat_session",
              data: { ...session("stale acknowledgement", "standard"), status: "running" },
            });
          },
          "/hecate/v1/chat/sessions/chat_a/config-options/model": () => {
            configRequests += 1;
            return jsonResponse({
              object: "chat_session",
              data: session("Configured after Stop", "fast"),
            });
          },
        }),
      );

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await act(async () => {
        await result.current.runtimeConsole.actions.selectChatSession("chat_a");
        await result.current.runtimeConsole.actions.cancelAgentChat();
      });
      expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
        status: "cancelled",
        title: "Stopped",
      });

      await act(async () => {
        expect(
          await result.current.runtimeConsole.actions.setChatConfigOption(
            "chat_a",
            "model",
            "fast",
          ),
        ).toBe(true);
      });

      expect(configRequests).toBe(1);
      expect(result.current.runtimeConsole.state.activeChatSession).toMatchObject({
        status: "cancelled",
        title: "Configured after Stop",
        config_options: [expect.objectContaining({ id: "model", current_value: "fast" })],
      });
    });

    it.each(["success", "failure"] as const)(
      "suppresses a deferred compact %s after the operator switches chats",
      async (outcome) => {
        let releaseCompact: ((response: Response) => void) | undefined;
        const deferredCompact = new Promise<Response>((resolve) => {
          releaseCompact = resolve;
        });
        const session = (id: string) =>
          jsonResponse({
            object: "chat_session",
            data: {
              id,
              title: id,
              agent_id: "hecate",
              status: "completed",
              messages: [],
              context_summary: { message_count: 3, summary: "summary" },
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
              "/hecate/v1/chat/sessions/chat_a": () => session("chat_a"),
              "/hecate/v1/chat/sessions/chat_b": () => session("chat_b"),
              "/hecate/v1/chat/sessions/chat_a/compact": () => deferredCompact,
            },
          ),
        );

        const { result } = renderRuntimeConsoleHook();
        await waitFor(() => expect(result.current.state.loading).toBe(false));
        await act(async () => {
          await result.current.actions.selectChatSession("chat_a");
        });

        let compact!: Promise<boolean>;
        act(() => {
          compact = result.current.actions.compactChatSession("chat_a");
        });
        await waitFor(() => expect(result.current.state.chatLoading).toBe(true));
        await act(async () => {
          await result.current.actions.selectChatSession("chat_b");
        });

        let compacted = true;
        await act(async () => {
          releaseCompact?.(
            outcome === "success"
              ? session("chat_a")
              : jsonResponse({ error: { message: "compact unavailable" } }, 503),
          );
          compacted = await compact;
        });

        expect(compacted).toBe(false);
        expect(result.current.state.activeChatSessionID).toBe("chat_b");
        expect(result.current.state.activeChatSession?.id).toBe("chat_b");
        expect(result.current.state.chatError).toBe("");
        expect(result.current.state.chatLoading).toBe(false);
        expect(result.current.state.notice?.message ?? "").not.toMatch(/^Compacted/);
      },
    );

    it.each(["ownership", "reset"] as const)(
      "fences a deferred compact result after a chat %s invalidation",
      async (invalidation) => {
        let releaseCompact: ((response: Response) => void) | undefined;
        const deferredCompact = new Promise<Response>((resolve) => {
          releaseCompact = resolve;
        });
        const session = () =>
          jsonResponse({
            object: "chat_session",
            data: {
              id: "chat_a",
              title: "A",
              agent_id: "hecate",
              status: "completed",
              messages: [],
              context_summary: { message_count: 3, summary: "summary" },
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:00Z",
            },
          });
        fetchMock.mockImplementation(
          withSessions([{ id: "chat_a", title: "A" }], {
            "/hecate/v1/chat/sessions/chat_a": session,
            "/hecate/v1/chat/sessions/chat_a/compact": () => deferredCompact,
          }),
        );

        const { result } = renderRuntimeConsoleWithChatHook();
        await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
        await act(async () => {
          await result.current.runtimeConsole.actions.selectChatSession("chat_a");
        });

        let compact!: Promise<boolean>;
        act(() => {
          compact = result.current.runtimeConsole.actions.compactChatSession("chat_a");
        });
        await waitFor(() => expect(result.current.runtimeConsole.state.chatLoading).toBe(true));

        let ownershipToken: number | null = null;
        act(() => {
          if (invalidation === "ownership") {
            ownershipToken = result.current.chat.actions.beginChatOwnershipMutation();
          } else {
            result.current.chat.actions.fenceAllChatSessionsDeleted();
          }
        });
        expect(ownershipToken === null).toBe(invalidation === "reset");

        let compacted = true;
        await act(async () => {
          releaseCompact?.(session());
          compacted = await compact;
        });

        expect(compacted).toBe(false);
        expect(result.current.runtimeConsole.state.chatLoading).toBe(false);
        expect(result.current.runtimeConsole.state.notice?.message ?? "").not.toMatch(/^Compacted/);
        if (ownershipToken !== null) {
          act(() => {
            result.current.chat.actions.finishChatOwnershipMutation(ownershipToken!);
          });
        }
      },
    );

    it("rejects an unprojected Hecate chat created after a global reset fence", async () => {
      let createCount = 0;
      let releaseCreate: ((response: Response) => void) | undefined;
      const deferredCreate = new Promise<Response>((resolve) => {
        releaseCreate = resolve;
      });
      fetchMock.mockImplementation((input, init) => {
        if (String(input) === "/hecate/v1/chat/sessions" && init?.method === "POST") {
          createCount += 1;
          return deferredCreate;
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));

      let create!: Promise<void>;
      act(() => {
        create = result.current.runtimeConsole.actions.createChatSession({ agentID: "hecate" });
      });
      await waitFor(() => expect(createCount).toBe(1));
      expect(result.current.chat.actions.isChatSessionCreateInFlight()).toBe(true);
      expect(result.current.chat.actions.beginChatOwnershipMutation()).toBeNull();
      expect(result.current.chat.actions.chatOwnershipMutationBlockReason()).toContain(
        "new chat to finish creating",
      );
      act(() => {
        result.current.chat.actions.fenceAllChatSessionsDeleted();
      });

      const created = {
        id: "chat_after_reset",
        title: "Late Hecate chat",
        agent_id: "hecate",
        status: "completed",
        workspace: "",
        message_count: 0,
        messages: [],
      };
      await act(async () => {
        releaseCreate?.(jsonResponse({ object: "chat_session", data: created }));
        await create;
      });

      expect(result.current.chat.state.activeChatSessionID).toBe("");
      expect(result.current.chat.state.activeChatSession).toBeNull();
      expect(result.current.chat.state.chatSessions).toEqual([]);
      expect(result.current.chat.state.chatTargetBySessionID.has(created.id)).toBe(false);
      expect(result.current.chat.state.chatToolsEnabledBySessionID.has(created.id)).toBe(false);
      expect(result.current.chat.actions.isChatSessionDeleted(created.id)).toBe(true);

      act(() => result.current.chat.actions.setChatSessions([created]));
      expect(result.current.chat.state.chatSessions).toEqual([]);
    });

    it("rejects a project-scoped Hecate chat created after its project deletion fence", async () => {
      const project: ProjectRecord = {
        id: "project_deleted",
        name: "Deleted project",
        roots: [],
        created_at: "2026-07-13T10:00:00Z",
        updated_at: "2026-07-13T10:00:00Z",
      };
      let createCount = 0;
      let releaseCreate: ((response: Response) => void) | undefined;
      const deferredCreate = new Promise<Response>((resolve) => {
        releaseCreate = resolve;
      });
      fetchMock.mockImplementation((input, init) => {
        if (String(input) === "/hecate/v1/chat/sessions" && init?.method === "POST") {
          createCount += 1;
          return deferredCreate;
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook({ projects: [project] });
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));

      let create!: Promise<void>;
      act(() => {
        create = result.current.runtimeConsole.actions.createChatSession({
          agentID: "hecate",
          projectID: project.id,
        });
      });
      await waitFor(() => expect(createCount).toBe(1));
      expect(result.current.chat.actions.isChatSessionCreateInFlight()).toBe(true);
      expect(result.current.chat.actions.beginChatOwnershipMutation()).toBeNull();
      expect(result.current.chat.actions.chatOwnershipMutationBlockReason()).toContain(
        "new chat to finish creating",
      );
      act(() => {
        result.current.chat.actions.fenceDeletedChatProject(project.id);
      });

      const created = {
        id: "chat_after_project_delete",
        title: "Late project chat",
        project_id: project.id,
        agent_id: "hecate",
        status: "completed",
        workspace: "",
        message_count: 0,
        messages: [],
      };
      await act(async () => {
        releaseCreate?.(jsonResponse({ object: "chat_session", data: created }));
        await create;
      });

      expect(result.current.chat.state.activeChatSessionID).toBe("");
      expect(result.current.chat.state.activeChatSession).toBeNull();
      expect(result.current.chat.state.chatSessions).toEqual([]);
      expect(result.current.chat.state.chatTargetBySessionID.has(created.id)).toBe(false);
      expect(result.current.chat.state.chatToolsEnabledBySessionID.has(created.id)).toBe(false);
      expect(result.current.chat.actions.isChatSessionDeleted(created.id)).toBe(true);

      act(() => result.current.chat.actions.setChatSessions([created]));
      expect(result.current.chat.state.chatSessions).toEqual([]);
    });

    it("rejects an external-agent chat created after a global reset fence", async () => {
      let createCount = 0;
      let releaseCreate: ((response: Response) => void) | undefined;
      const deferredCreate = new Promise<Response>((resolve) => {
        releaseCreate = resolve;
      });
      fetchMock.mockImplementation((input, init) => {
        if (String(input) === "/hecate/v1/chat/sessions" && init?.method === "POST") {
          createCount += 1;
          return deferredCreate;
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook({
        chatInitialState: { agentWorkspace: "/workspace" },
      });
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));

      let create!: Promise<void>;
      act(() => {
        create = result.current.runtimeConsole.actions.createChatSession({ agentID: "codex" });
      });
      await waitFor(() => expect(createCount).toBe(1));
      expect(result.current.chat.actions.beginChatOwnershipMutation()).toBeNull();
      expect(result.current.chat.actions.chatOwnershipMutationBlockReason()).toContain(
        "new chat to finish creating",
      );
      act(() => {
        result.current.chat.actions.fenceAllChatSessionsDeleted();
      });

      const created = {
        id: "external_after_reset",
        title: "Late external chat",
        agent_id: "codex",
        status: "completed",
        workspace: "/workspace",
        message_count: 0,
        messages: [],
      };
      await act(async () => {
        releaseCreate?.(jsonResponse({ object: "chat_session", data: created }));
        await create;
      });

      expect(result.current.chat.state.activeChatSessionID).toBe("");
      expect(result.current.chat.state.activeChatSession).toBeNull();
      expect(result.current.chat.state.chatSessions).toEqual([]);
      expect(result.current.chat.state.chatTargetBySessionID.has(created.id)).toBe(false);
      expect(result.current.chat.state.chatToolsEnabledBySessionID.has(created.id)).toBe(false);
      expect(result.current.chat.actions.isChatSessionDeleted(created.id)).toBe(true);

      act(() => result.current.chat.actions.setChatSessions([created]));
      expect(result.current.chat.state.chatSessions).toEqual([]);
    });

    it("rejects an implicit submit-created chat after a global reset fence", async () => {
      window.localStorage.setItem("hecate.chatToolsEnabled", "false");
      window.localStorage.setItem("hecate.providerFilter", "openai");
      window.localStorage.setItem("hecate.model", "gpt-4o-mini");
      let createCount = 0;
      let messagePostCount = 0;
      let releaseCreate: ((response: Response) => void) | undefined;
      const deferredCreate = new Promise<Response>((resolve) => {
        releaseCreate = resolve;
      });
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/v1/models") {
          return jsonResponse({
            object: "list",
            data: [
              {
                id: "gpt-4o-mini",
                owned_by: "openai",
                metadata: {
                  provider: "openai",
                  provider_kind: "cloud",
                  default: true,
                  capabilities: { tool_calling: "basic", image_input: "supported" },
                },
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions" && init?.method === "POST") {
          createCount += 1;
          return deferredCreate;
        }
        if (url.endsWith("/messages") && init?.method === "POST") {
          messagePostCount += 1;
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await waitFor(() => expect(result.current.runtimeConsole.state.model).toBe("gpt-4o-mini"));
      act(() => result.current.runtimeConsole.actions.setMessage("Create and send"));
      await waitFor(() =>
        expect(result.current.runtimeConsole.state.message).toBe("Create and send"),
      );

      let submission!: Promise<void>;
      act(() => {
        submission = result.current.runtimeConsole.actions.submitChat({
          preventDefault: vi.fn(),
        } as any);
      });
      await waitFor(() => expect(createCount).toBe(1));
      expect(result.current.chat.actions.isChatSessionCreateInFlight()).toBe(true);
      expect(result.current.chat.actions.beginChatOwnershipMutation()).toBeNull();
      expect(result.current.chat.actions.chatOwnershipMutationBlockReason()).toContain(
        "new chat to finish creating",
      );
      act(() => {
        result.current.chat.actions.fenceAllChatSessionsDeleted();
      });

      const created = {
        id: "implicit_after_reset",
        title: "Late implicit chat",
        agent_id: "hecate",
        provider: "openai",
        model: "gpt-4o-mini",
        status: "completed",
        workspace: "",
        message_count: 0,
        messages: [],
      };
      await act(async () => {
        releaseCreate?.(jsonResponse({ object: "chat_session", data: created }));
        await submission;
      });

      expect(messagePostCount).toBe(0);
      expect(result.current.chat.state.activeChatSessionID).toBe("");
      expect(result.current.chat.state.activeChatSession).toBeNull();
      expect(result.current.chat.state.chatSessions).toEqual([]);
      expect(result.current.chat.state.chatTargetBySessionID.has(created.id)).toBe(false);
      expect(result.current.chat.state.chatToolsEnabledBySessionID.has(created.id)).toBe(false);
      expect(result.current.chat.actions.isChatSessionDeleted(created.id)).toBe(true);

      act(() => result.current.chat.actions.setChatSessions([created]));
      expect(result.current.chat.state.chatSessions).toEqual([]);
    });

    it.each(["reset", "project"] as const)(
      "pauses a queued send during ownership reservation and fences its deferred response on %s",
      async (fence) => {
        persistQueuedPrompt();
        const projectID = fence === "project" ? "project_queue_fence" : "";
        const project: ProjectRecord | undefined = projectID
          ? {
              id: projectID,
              name: "Queue fence",
              roots: [],
              created_at: "2026-07-13T10:00:00Z",
              updated_at: "2026-07-13T10:00:00Z",
            }
          : undefined;
        const session = (status: string, messages: Array<Record<string, unknown>> = []) => ({
          object: "chat_session",
          data: {
            ...queuedPromptSession(messages).data,
            ...(projectID ? { project_id: projectID } : {}),
            status,
          },
        });
        let releaseMessage: ((response: Response) => void) | undefined;
        const deferredMessage = new Promise<Response>((resolve) => {
          releaseMessage = resolve;
        });
        let messagePostCount = 0;
        fetchMock.mockImplementation(async (input, init) => {
          const url = String(input);
          if (url === "/hecate/v1/chat/sessions") {
            return jsonResponse({
              object: "chat_sessions",
              data: [
                {
                  ...queuedPromptSessionList().data[0],
                  ...(projectID ? { project_id: projectID } : {}),
                  status: "running",
                },
              ],
            });
          }
          if (url === "/hecate/v1/chat/sessions/a1") return jsonResponse(session("running"));
          if (url === "/hecate/v1/chat/sessions/a1/stream") return emptyStreamResponse();
          if (url === "/hecate/v1/chat/sessions/a1/messages") {
            messagePostCount += 1;
            void init;
            return deferredMessage;
          }
          return defaultBackendMock()(input, init);
        });

        const { result } = renderRuntimeConsoleWithChatHook({
          projects: project ? [project] : undefined,
        });
        await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
        await waitFor(() => expect(result.current.chat.state.activeChatSession?.id).toBe("a1"));
        expect(result.current.chat.state.activeChatSession?.status).toBe("running");

        let pauseToken: number | null = null;
        act(() => {
          pauseToken = result.current.chat.actions.beginChatOwnershipMutation();
          result.current.chat.actions.setActiveChatSession((current) =>
            current ? { ...current, status: "completed" } : current,
          );
        });
        expect(pauseToken).not.toBeNull();
        await new Promise((resolve) => window.setTimeout(resolve, 0));
        expect(messagePostCount).toBe(0);

        act(() => {
          if (pauseToken !== null)
            result.current.chat.actions.finishChatOwnershipMutation(pauseToken);
        });
        await waitFor(() => expect(messagePostCount).toBe(1));

        let fenceToken: number | null = null;
        act(() => {
          fenceToken = result.current.chat.actions.beginChatOwnershipMutation();
          if (fence === "reset") result.current.chat.actions.fenceAllChatSessionsDeleted();
          else result.current.chat.actions.fenceDeletedChatProject(projectID);
          if (fenceToken !== null)
            result.current.chat.actions.finishChatOwnershipMutation(fenceToken);
        });
        const lateMessages = [
          { id: "u-late", role: "user", content: "after this" },
          { id: "a-late", role: "assistant", content: "late", status: "completed" },
        ];
        await act(async () => {
          releaseMessage?.(
            jsonResponse({
              ...session("completed", lateMessages),
              message_request: { replay: false, committed_message_id: "u-late" },
            }),
          );
          await Promise.resolve();
        });

        await waitFor(() => expect(result.current.chat.state.chatLoading).toBe(false));
        expect(result.current.chat.state.activeChatSessionID).toBe("");
        expect(result.current.chat.state.activeChatSession).toBeNull();
        expect(result.current.chat.state.queuedChatMessages).toEqual([]);
        expect(result.current.chat.state.chatError).toBe("");
        expect(messagePostCount).toBe(1);
      },
    );

    it("lets an active text submit finish after an unrelated project deletion fence", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatToolsEnabled", "false");
      window.localStorage.setItem("hecate.chatSessionID", "chat_active");
      window.localStorage.setItem("hecate.providerFilter", "openai");
      window.localStorage.setItem("hecate.model", "gpt-4o-mini");
      const projectsForTest: ProjectRecord[] = [
        {
          id: "project_active",
          name: "Active",
          roots: [],
          created_at: "2026-07-13T10:00:00Z",
          updated_at: "2026-07-13T10:00:00Z",
        },
        {
          id: "project_unrelated",
          name: "Unrelated",
          roots: [],
          created_at: "2026-07-13T10:00:00Z",
          updated_at: "2026-07-13T10:00:00Z",
        },
      ];
      const activeSession = (messages: Array<Record<string, unknown>> = []) => ({
        object: "chat_session",
        data: {
          id: "chat_active",
          title: "Active chat",
          project_id: "project_active",
          agent_id: "hecate",
          status: "completed",
          provider: "openai",
          model: "gpt-4o-mini",
          messages,
        },
      });
      let releaseMessage: ((response: Response) => void) | undefined;
      const deferredMessage = new Promise<Response>((resolve) => {
        releaseMessage = resolve;
      });
      let messagePostCount = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: [
              {
                ...activeSession().data,
                message_count: 0,
                messages: undefined,
              },
              {
                id: "chat_unrelated",
                title: "Other chat",
                project_id: "project_unrelated",
                agent_id: "hecate",
                status: "completed",
                message_count: 0,
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions/chat_active") {
          return jsonResponse(activeSession());
        }
        if (url === "/hecate/v1/chat/sessions/chat_active/stream") return emptyStreamResponse();
        if (url === "/hecate/v1/chat/sessions/chat_active/messages") {
          messagePostCount += 1;
          void init;
          return deferredMessage;
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook({ projects: projectsForTest });
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await waitFor(() =>
        expect(result.current.chat.state.activeChatSession?.id).toBe("chat_active"),
      );
      act(() => result.current.runtimeConsole.actions.setMessage("keep working"));
      let submission!: Promise<void>;
      act(() => {
        submission = result.current.runtimeConsole.actions.submitChat({
          preventDefault: vi.fn(),
        } as any);
      });
      await waitFor(() => expect(messagePostCount).toBe(1));

      act(() => {
        result.current.chat.actions.fenceDeletedChatProject("project_unrelated");
      });
      await act(async () => {
        releaseMessage?.(
          jsonResponse(
            activeSession([
              { id: "u-active", role: "user", content: "keep working" },
              { id: "a-active", role: "assistant", content: "Done.", status: "completed" },
            ]),
          ),
        );
        await submission;
      });

      expect(result.current.chat.state.activeChatSession?.id).toBe("chat_active");
      expect(
        result.current.chat.state.activeChatSession?.messages?.map((message) => message.id),
      ).toEqual(["u-active", "a-active"]);
      expect(result.current.chat.state.chatLoading).toBe(false);
      expect(result.current.chat.state.chatError).toBe("");
    });

    it.each(["reset", "project"] as const)(
      "prevents a deferred ordinary submit from mutating a newer request after a %s fence",
      async (fence) => {
        window.localStorage.setItem("hecate.chatTarget", "agent");
        window.localStorage.setItem("hecate.chatToolsEnabled", "false");
        window.localStorage.setItem("hecate.chatSessionID", "chat_old");
        window.localStorage.setItem("hecate.providerFilter", "openai");
        window.localStorage.setItem("hecate.model", "gpt-4o-mini");
        const projectID = fence === "project" ? "project_old" : "";
        const oldSession = {
          id: "chat_old",
          title: "Old chat",
          ...(projectID ? { project_id: projectID } : {}),
          agent_id: "hecate",
          status: "completed",
          workspace: "",
          provider: "openai",
          model: "gpt-4o-mini",
          messages: [],
        };
        const newSession = {
          id: "chat_newer",
          title: "Newer chat",
          agent_id: "hecate",
          status: "completed",
          workspace: "",
          message_count: 0,
          provider: "openai",
          model: "gpt-4o-mini",
          messages: [],
        };
        let releaseOld: ((response: Response) => void) | undefined;
        let releaseNew: ((response: Response) => void) | undefined;
        const oldResponse = new Promise<Response>((resolve) => {
          releaseOld = resolve;
        });
        const newResponse = new Promise<Response>((resolve) => {
          releaseNew = resolve;
        });
        let oldPosts = 0;
        let newPosts = 0;
        fetchMock.mockImplementation(async (input, init) => {
          const url = String(input);
          if (url === "/hecate/v1/chat/sessions") {
            return jsonResponse({
              object: "chat_sessions",
              data: [{ ...oldSession, messages: undefined, message_count: 0 }],
            });
          }
          if (url === "/hecate/v1/chat/sessions/chat_old") {
            return jsonResponse({ object: "chat_session", data: oldSession });
          }
          if (url.endsWith("/stream")) return emptyStreamResponse();
          if (url === "/hecate/v1/chat/sessions/chat_old/messages") {
            oldPosts += 1;
            void init;
            return oldResponse;
          }
          if (url === "/hecate/v1/chat/sessions/chat_newer/messages") {
            newPosts += 1;
            void init;
            return newResponse;
          }
          return defaultBackendMock()(input, init);
        });

        const project: ProjectRecord | undefined = projectID
          ? {
              id: projectID,
              name: "Old project",
              roots: [],
              created_at: "2026-07-13T10:00:00Z",
              updated_at: "2026-07-13T10:00:00Z",
            }
          : undefined;
        const { result } = renderRuntimeConsoleWithChatHook({
          projects: project ? [project] : undefined,
        });
        await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
        await waitFor(() =>
          expect(result.current.chat.state.activeChatSession?.id).toBe("chat_old"),
        );
        act(() => result.current.runtimeConsole.actions.setMessage("old request"));
        let oldSubmission!: Promise<void>;
        act(() => {
          oldSubmission = result.current.runtimeConsole.actions.submitChat({
            preventDefault: vi.fn(),
          } as any);
        });
        await waitFor(() => expect(oldPosts).toBe(1));

        act(() => {
          const token = result.current.chat.actions.beginChatOwnershipMutation();
          expect(token).not.toBeNull();
          if (fence === "reset") result.current.chat.actions.fenceAllChatSessionsDeleted();
          else result.current.chat.actions.fenceDeletedChatProject(projectID);
          if (token !== null) result.current.chat.actions.finishChatOwnershipMutation(token);
          result.current.chat.actions.setChatSessions([newSession]);
          result.current.chat.actions.setActiveChatSessionID(newSession.id);
          result.current.chat.actions.setActiveChatSession(newSession);
          result.current.runtimeConsole.actions.setMessage("new request");
        });
        await waitFor(() =>
          expect(result.current.chat.state.activeChatSession?.id).toBe("chat_newer"),
        );
        let newSubmission!: Promise<void>;
        act(() => {
          newSubmission = result.current.runtimeConsole.actions.submitChat({
            preventDefault: vi.fn(),
          } as any);
        });
        await waitFor(() => expect(newPosts).toBe(1));

        await act(async () => {
          releaseOld?.(jsonResponse({ error: { message: "late old failure" } }, 500));
          await oldSubmission;
        });
        expect(result.current.chat.state.activeChatSession?.id).toBe("chat_newer");
        expect(result.current.chat.state.chatLoading).toBe(true);
        expect(result.current.chat.state.chatError).toBe("");

        await act(async () => {
          releaseNew?.(
            jsonResponse({
              object: "chat_session",
              data: {
                ...newSession,
                messages: [
                  { id: "u-new", role: "user", content: "new request" },
                  { id: "a-new", role: "assistant", content: "Done.", status: "completed" },
                ],
              },
            }),
          );
          await newSubmission;
        });
        expect(result.current.chat.state.chatLoading).toBe(false);
        expect(result.current.chat.state.chatError).toBe("");
        expect(
          result.current.chat.state.activeChatSession?.messages?.map((message) => message.id),
        ).toEqual(["u-new", "a-new"]);
      },
    );

    it.each([
      { fence: "reset" as const, finishReason: "tool_calls" },
      { fence: "project" as const, finishReason: "stop" },
    ])(
      "discards a deferred tool-result continuation after a $fence fence",
      async ({ fence, finishReason }) => {
        const projectID = fence === "project" ? "project_tool_fence" : "";
        const project: ProjectRecord | undefined = projectID
          ? {
              id: projectID,
              name: "Tool fence",
              roots: [],
              created_at: "2026-07-13T10:00:00Z",
              updated_at: "2026-07-13T10:00:00Z",
            }
          : undefined;
        const activeSession = {
          id: "chat_tool_fence",
          title: "Tool continuation",
          ...(projectID ? { project_id: projectID } : {}),
          agent_id: "hecate",
          status: "completed",
          provider: "openai",
          model: "gpt-4o-mini",
          messages: [],
          created_at: "2026-07-13T10:00:00Z",
          updated_at: "2026-07-13T10:00:00Z",
        };
        let streamController!: ReadableStreamDefaultController<Uint8Array>;
        const deferredStream = new Response(
          new ReadableStream<Uint8Array>({
            start(controller) {
              streamController = controller;
            },
          }),
          { status: 200, headers: { "Content-Type": "text/event-stream" } },
        );
        const completions = vi.fn(() => deferredStream);
        fetchMock.mockImplementation(
          withSessions([{ id: activeSession.id, title: activeSession.title }], {
            [`/hecate/v1/chat/sessions/${activeSession.id}`]: () =>
              jsonResponse({ object: "chat_session", data: activeSession }),
            "/v1/chat/completions": completions,
          }),
        );

        const { result } = renderRuntimeConsoleWithChatHook({
          projects: project ? [project] : undefined,
        });
        await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
        await act(async () => {
          await result.current.runtimeConsole.actions.selectChatSession(activeSession.id);
        });
        act(() => {
          result.current.chat.actions.setModel("gpt-4o-mini");
          result.current.chat.actions.setProviderFilter("openai");
          result.current.chat.actions.setPendingThread([
            { role: "assistant", content: "Call the tool." },
          ]);
          result.current.chat.actions.setPendingToolCalls([
            { id: "call_1", name: "lookup", arguments: "{}", result: "done" },
          ]);
        });

        let reservationBlockedSubmission = true;
        if (fence === "reset") {
          let reservationToken: number | null = null;
          act(() => {
            reservationToken = result.current.chat.actions.beginChatOwnershipMutation();
          });
          await act(async () => {
            await result.current.runtimeConsole.actions.submitToolResults();
          });
          reservationBlockedSubmission =
            completions.mock.calls.length === 0 &&
            Boolean(
              result.current.runtimeConsole.state.notice?.message.includes(
                "before submitting tool results",
              ),
            );
          act(() => {
            if (reservationToken !== null) {
              result.current.chat.actions.finishChatOwnershipMutation(reservationToken);
            }
          });
        }
        expect(reservationBlockedSubmission).toBe(true);

        let continuation!: Promise<void>;
        act(() => {
          continuation = result.current.runtimeConsole.actions.submitToolResults();
        });
        await waitFor(() => expect(completions).toHaveBeenCalledTimes(1));

        act(() => {
          if (fence === "reset") {
            result.current.chat.actions.fenceAllChatSessionsDeleted();
          } else {
            result.current.chat.actions.fenceDeletedChatProject(projectID);
          }
        });

        const chunk =
          finishReason === "tool_calls"
            ? {
                choices: [
                  {
                    delta: {
                      content: "late tool output",
                      tool_calls: [
                        {
                          index: 0,
                          id: "call_late",
                          function: { name: "late_lookup", arguments: "{}" },
                        },
                      ],
                    },
                    finish_reason: "tool_calls",
                  },
                ],
              }
            : {
                choices: [{ delta: { content: "late completion" }, finish_reason: "stop" }],
              };
        await act(async () => {
          const encoder = new TextEncoder();
          streamController.enqueue(encoder.encode(`data: ${JSON.stringify(chunk)}\n\n`));
          streamController.enqueue(encoder.encode("data: [DONE]\n\n"));
          streamController.close();
          await continuation;
        });

        expect(result.current.chat.state.activeChatSessionID).toBe("");
        expect(result.current.chat.state.activeChatSession).toBeNull();
        expect(result.current.chat.state.streamingContent).toBeNull();
        expect(result.current.chat.state.pendingThread).toBeNull();
        expect(result.current.chat.state.pendingToolCalls).toEqual([]);
        expect(result.current.chat.state.chatResult).toBeNull();
        expect(result.current.chat.state.chatLoading).toBe(false);
      },
    );

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
        // Hecate-side turns now send `tools_enabled` and let the
        // backend derive the dispatch. The legacy `execution_mode`
        // field stays in the type but isn't sent for non-external
        // turns.
        tools_enabled: true,
        system_prompt: "Prefer small, reviewable diffs.",
        workspace: "/workspace",
      });
      expect(postedBody).not.toHaveProperty("execution_mode");
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
        // A capability-driven downgrade (model lacks tool calling)
        // surfaces on the wire as tools_enabled=false; the backend
        // routes to the direct-model path without needing the
        // legacy execution_mode literal.
        tools_enabled: false,
        provider: "ollama",
        model: "smollm2:135m",
      });
      expect(postedBody).not.toHaveProperty("execution_mode");
      expect(postedBody).not.toHaveProperty("workspace");
      expect(result.current.state.chatError).toBe("");
    });

    it("withholds server cancellation while message admission is unconfirmed", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatToolsEnabled", "false");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.providerFilter", "openai");
      window.localStorage.setItem("hecate.model", "gpt-4o-mini");
      let releaseMessage: ((response: Response) => void) | undefined;
      const deferredMessage = new Promise<Response>((resolve) => {
        releaseMessage = resolve;
      });
      let messagePostCount = 0;
      let cancelRequestCount = 0;
      const session = {
        id: "a1",
        title: "Direct model chat",
        agent_id: "hecate",
        status: "completed",
        provider: "openai",
        model: "gpt-4o-mini",
        capabilities: { tool_calling: "basic", image_input: "supported" },
        messages: [],
      };
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/v1/models") {
          return jsonResponse({
            object: "list",
            data: [
              {
                id: "gpt-4o-mini",
                owned_by: "openai",
                metadata: {
                  provider: "openai",
                  provider_kind: "cloud",
                  capabilities: { tool_calling: "basic", image_input: "supported" },
                },
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: [{ ...session, message_count: 0 }],
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse({ object: "chat_session", data: session });
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") return emptyStreamResponse();
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
          return deferredMessage;
        }
        if (url === "/hecate/v1/chat/sessions/a1/cancel") {
          cancelRequestCount += 1;
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await waitFor(() =>
        expect(result.current.runtimeConsole.state.activeChatSession?.id).toBe("a1"),
      );
      act(() => result.current.runtimeConsole.actions.setMessage("describe the map"));

      let submission!: Promise<void>;
      act(() => {
        submission = result.current.runtimeConsole.actions.submitChat({
          preventDefault: vi.fn(),
        } as any);
      });
      await waitFor(() => expect(messagePostCount).toBe(1));
      expect(result.current.chat.state.chatTurnCancellationAvailable).toBe(false);

      await act(async () => {
        await result.current.runtimeConsole.actions.cancelAgentChat();
      });
      expect(cancelRequestCount).toBe(0);
      expect(result.current.chat.state.chatCancelling).toBe(false);

      await act(async () => {
        releaseMessage?.(jsonResponse({ object: "chat_session", data: session }));
        await submission;
      });
      expect(cancelRequestCount).toBe(0);
      expect(result.current.chat.state.chatTurnActive).toBe(false);
    });

    it("stops a delayed image upload locally before any message dispatch", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatToolsEnabled", "false");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.providerFilter", "openai");
      window.localStorage.setItem("hecate.model", "gpt-4o-mini");
      let releaseUpload: ((response: Response) => void) | undefined;
      const deferredUpload = new Promise<Response>((resolve) => {
        releaseUpload = resolve;
      });
      const uploadRequest = { signal: null as AbortSignal | null };
      let messagePostCount = 0;
      let cancelRequestCount = 0;
      let attachmentCleanupCount = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/v1/models") {
          return jsonResponse({
            object: "list",
            data: [
              {
                id: "gpt-4o-mini",
                owned_by: "openai",
                metadata: {
                  provider: "openai",
                  provider_kind: "cloud",
                  capabilities: { tool_calling: "basic", image_input: "supported" },
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
                title: "Vision chat",
                agent_id: "hecate",
                status: "completed",
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
              title: "Vision chat",
              agent_id: "hecate",
              status: "completed",
              provider: "openai",
              model: "gpt-4o-mini",
              capabilities: { tool_calling: "basic", image_input: "supported" },
              messages: [],
            },
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1/attachments" && init?.method === "POST") {
          uploadRequest.signal = init.signal ?? null;
          // Intentionally ignore AbortSignal and return an acknowledgement
          // later so the post-upload generation fence is exercised too.
          return deferredUpload;
        }
        if (
          url === "/hecate/v1/chat/sessions/a1/attachments/attachment-stable-id" &&
          init?.method === "DELETE"
        ) {
          attachmentCleanupCount += 1;
          return new Response(null, { status: 204 });
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
        }
        if (url === "/hecate/v1/chat/sessions/a1/cancel") {
          cancelRequestCount += 1;
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a1"));
      const file = new File(["image"], "map.png", { type: "image/png" });
      act(() => {
        result.current.actions.setMessage("inspect the map");
        result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
      });

      let submission!: Promise<void>;
      act(() => {
        submission = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });
      await waitFor(() => expect(uploadRequest.signal).not.toBeNull());

      await act(async () => {
        await result.current.actions.cancelAgentChat();
      });
      expect(uploadRequest.signal?.aborted).toBe(true);
      expect(result.current.state.chatCancelling).toBe(true);
      expect(messagePostCount).toBe(0);
      expect(cancelRequestCount).toBe(0);

      await act(async () => {
        releaseUpload?.(
          jsonResponse({
            object: "chat_attachment",
            data: {
              id: "attachment-stable-id",
              session_id: "a1",
              filename: "map.png",
              media_type: "image/png",
              size_bytes: 5,
              sha256: "abc",
              created_at: "2026-07-13T10:00:00Z",
              content_url: "/hecate/v1/chat/sessions/a1/attachments/attachment-stable-id/content",
            },
          }),
        );
        await submission;
      });

      expect(messagePostCount).toBe(0);
      expect(cancelRequestCount).toBe(0);
      expect(attachmentCleanupCount).toBe(1);
      expect(result.current.state.pendingChatAttachments).toEqual([{ id: "draft-1", file }]);
      expect(result.current.state.message).toBe("inspect the map");
      expect(result.current.state.chatLoading).toBe(false);
      expect(result.current.state.chatCancelling).toBe(false);
      expect(result.current.state.chatError).toBe("");
    });

    it.each(["honors abort", "ignores abort"] as const)(
      "stops detached first-turn session creation when the backend %s",
      async (createBehavior) => {
        window.localStorage.setItem("hecate.chatTarget", "agent");
        window.localStorage.setItem("hecate.chatToolsEnabled", "false");
        window.localStorage.setItem("hecate.providerFilter", "openai");
        window.localStorage.setItem("hecate.model", "gpt-4o-mini");
        let createSignal!: AbortSignal;
        let releaseCreate: ((response: Response) => void) | undefined;
        const deferredCreate = new Promise<Response>((resolve) => {
          releaseCreate = resolve;
        });
        let uploadCount = 0;
        let messagePostCount = 0;
        let streamCount = 0;
        let cancelRequestCount = 0;
        const createdSession = {
          id: "created_after_stop",
          title: "Stopped first turn",
          agent_id: "hecate",
          status: "completed",
          provider: "openai",
          model: "gpt-4o-mini",
          capabilities: { tool_calling: "basic", image_input: "supported" },
          messages: [],
          created_at: "2026-07-14T10:00:00Z",
          updated_at: "2026-07-14T10:00:00Z",
        };
        fetchMock.mockImplementation(async (input, init) => {
          const url = String(input);
          if (url === "/v1/models") {
            return jsonResponse({
              object: "list",
              data: [
                {
                  id: "gpt-4o-mini",
                  owned_by: "openai",
                  metadata: {
                    provider: "openai",
                    provider_kind: "cloud",
                    capabilities: { tool_calling: "basic", image_input: "supported" },
                  },
                },
              ],
            });
          }
          if (url === "/hecate/v1/chat/sessions" && init?.method === "POST") {
            if (!init.signal) throw new Error("expected session creation to carry AbortSignal");
            createSignal = init.signal;
            if (createBehavior === "ignores abort") return deferredCreate;
            return new Promise<Response>((_resolve, reject) => {
              const abort = () => reject(new DOMException("aborted", "AbortError"));
              if (createSignal?.aborted) abort();
              else createSignal?.addEventListener("abort", abort, { once: true });
            });
          }
          if (url === "/hecate/v1/chat/sessions") {
            return jsonResponse({ object: "chat_sessions", data: [] });
          }
          if (url.includes("/attachments") && init?.method === "POST") uploadCount += 1;
          if (url.endsWith("/messages") && init?.method === "POST") messagePostCount += 1;
          if (url.endsWith("/stream")) streamCount += 1;
          if (url.endsWith("/cancel") && init?.method === "POST") cancelRequestCount += 1;
          return defaultBackendMock()(input, init);
        });

        const { result } = renderRuntimeConsoleWithChatHook();
        await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
        await waitFor(() => expect(result.current.runtimeConsole.state.model).toBe("gpt-4o-mini"));
        const file = new File(["image"], "map.png", { type: "image/png" });
        act(() => {
          result.current.runtimeConsole.actions.setMessage("inspect the map");
          result.current.runtimeConsole.actions.setPendingChatAttachments([
            { id: "draft-1", file },
          ]);
        });

        let submission!: Promise<void>;
        act(() => {
          submission = result.current.runtimeConsole.actions.submitChat({
            preventDefault: vi.fn(),
          } as any);
        });
        await waitFor(() => expect(createSignal).toBeDefined());
        expect(result.current.chat.state.chatTurnActive).toBe(true);
        expect(result.current.chat.state.chatTurnSessionID).toBe("");
        expect(result.current.chat.state.chatTurnCancellationAvailable).toBe(true);
        expect(result.current.chat.state.chatLoading).toBe(true);

        await act(async () => {
          await result.current.runtimeConsole.actions.cancelAgentChat();
        });
        expect(createSignal?.aborted).toBe(true);
        expect(uploadCount).toBe(0);
        expect(messagePostCount).toBe(0);
        expect(streamCount).toBe(0);
        expect(cancelRequestCount).toBe(0);

        const cancellationWhileCreatePending =
          createBehavior === "ignores abort" ? result.current.chat.state.chatCancelling : null;
        if (createBehavior === "ignores abort") {
          await act(async () => {
            releaseCreate?.(jsonResponse({ object: "chat_session", data: createdSession }));
            await submission;
          });
        } else {
          await act(async () => submission);
        }

        expect(cancellationWhileCreatePending).toBe(
          createBehavior === "ignores abort" ? true : null,
        );
        expect(result.current.runtimeConsole.state.activeChatSessionID).toBe(
          createBehavior === "ignores abort" ? "created_after_stop" : "",
        );
        expect(
          result.current.runtimeConsole.state.chatSessions
            .map((session) => session.id)
            .includes("created_after_stop"),
        ).toBe(createBehavior === "ignores abort");
        expect(uploadCount).toBe(0);
        expect(messagePostCount).toBe(0);
        expect(streamCount).toBe(0);
        expect(cancelRequestCount).toBe(0);
        expect(result.current.runtimeConsole.state.pendingChatAttachments).toEqual([
          { id: "draft-1", file },
        ]);
        expect(result.current.runtimeConsole.state.message).toBe("inspect the map");
        expect(result.current.chat.state.chatLoading).toBe(false);
        expect(result.current.chat.state.chatCancelling).toBe(false);
        expect(result.current.runtimeConsole.state.chatError).toBe("");
      },
    );

    it("stops detached External Agent creation before message dispatch", async () => {
      window.localStorage.setItem("hecate.chatTarget", "external_agent");
      window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
      let createSignal!: AbortSignal;
      let releaseCreate: ((response: Response) => void) | undefined;
      const deferredCreate = new Promise<Response>((resolve) => {
        releaseCreate = resolve;
      });
      let messagePostCount = 0;
      let streamCount = 0;
      let cancelRequestCount = 0;
      const createdSession = {
        id: "external_created_after_stop",
        title: "Stopped External Agent turn",
        agent_id: "claude_code",
        driver_kind: "acp",
        native_session_id: "native_after_stop",
        status: "completed",
        workspace: "/workspace",
        messages: [],
        created_at: "2026-07-14T10:00:00Z",
        updated_at: "2026-07-14T10:00:00Z",
      };
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/agent-adapters") {
          return jsonResponse({
            object: "agent_adapters",
            data: [{ id: "claude_code", name: "Claude Code", kind: "acp", available: true }],
          });
        }
        if (url === "/hecate/v1/chat/sessions" && init?.method === "POST") {
          if (!init.signal) throw new Error("expected session creation to carry AbortSignal");
          createSignal = init.signal;
          // Exercise the generation fence after a proxy has ignored abort
          // and acknowledged the external session shell.
          return deferredCreate;
        }
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({ object: "chat_sessions", data: [] });
        }
        if (url.endsWith("/messages") && init?.method === "POST") messagePostCount += 1;
        if (url.endsWith("/stream")) streamCount += 1;
        if (url.endsWith("/cancel") && init?.method === "POST") cancelRequestCount += 1;
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await waitFor(() =>
        expect(result.current.runtimeConsole.state.agentAdapters).toEqual([
          expect.objectContaining({ id: "claude_code", available: true }),
        ]),
      );
      act(() => {
        result.current.runtimeConsole.actions.setAgentWorkspace("/workspace");
        result.current.runtimeConsole.actions.setMessage("inspect the repository");
      });

      let submission!: Promise<void>;
      act(() => {
        submission = result.current.runtimeConsole.actions.submitChat({
          preventDefault: vi.fn(),
        } as any);
      });
      await waitFor(() => expect(createSignal).toBeDefined());
      expect(result.current.chat.state.chatTurnActive).toBe(true);
      expect(result.current.chat.state.chatTurnSessionID).toBe("");
      expect(result.current.chat.state.chatTurnCancellationAvailable).toBe(true);

      await act(async () => {
        await result.current.runtimeConsole.actions.cancelAgentChat();
      });
      expect(createSignal.aborted).toBe(true);
      expect(result.current.chat.state.chatCancelling).toBe(true);
      expect(messagePostCount).toBe(0);
      expect(streamCount).toBe(0);
      expect(cancelRequestCount).toBe(0);

      await act(async () => {
        releaseCreate?.(jsonResponse({ object: "chat_session", data: createdSession }));
        await submission;
      });

      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe(
        "external_created_after_stop",
      );
      expect(result.current.runtimeConsole.state.activeChatSession?.native_session_id).toBe(
        "native_after_stop",
      );
      expect(
        result.current.runtimeConsole.state.chatSessions.map((session) => session.id),
      ).toContain("external_created_after_stop");
      expect(messagePostCount).toBe(0);
      expect(streamCount).toBe(0);
      expect(cancelRequestCount).toBe(0);
      expect(result.current.runtimeConsole.state.message).toBe("inspect the repository");
      expect(result.current.chat.state.chatLoading).toBe(false);
      expect(result.current.chat.state.chatCancelling).toBe(false);
      expect(result.current.runtimeConsole.state.chatError).toBe("");
    });

    it("uploads arbitrary files and references them on an External Agent turn", async () => {
      window.localStorage.setItem("hecate.chatTarget", "external_agent");
      window.localStorage.setItem("hecate.agentAdapterID", "claude_code");
      window.localStorage.setItem("hecate.chatSessionID", "external-files");
      let uploadedFile: File | null = null;
      let messagePayload: Record<string, unknown> | null = null;
      const attachment = {
        id: "attachment-report",
        session_id: "external-files",
        filename: "report.pdf",
        media_type: "application/pdf",
        size_bytes: 6,
        sha256: "def",
        created_at: "2026-07-15T10:00:00Z",
        content_url:
          "/hecate/v1/chat/sessions/external-files/attachments/attachment-report/content",
      };
      const session = (messages: unknown[] = []) => ({
        id: "external-files",
        title: "External files",
        agent_id: "claude_code",
        driver_kind: "acp",
        native_session_id: "native-files",
        status: "completed",
        workspace: "/workspace",
        messages,
      });
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/agent-adapters") {
          return jsonResponse({
            object: "agent_adapters",
            data: [{ id: "claude_code", name: "Claude Code", kind: "acp", available: true }],
          });
        }
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: [{ ...session(), message_count: 0 }],
          });
        }
        if (url === "/hecate/v1/chat/sessions/external-files") {
          return jsonResponse({ object: "chat_session", data: session() });
        }
        if (
          url === "/hecate/v1/chat/sessions/external-files/attachments" &&
          init?.method === "POST"
        ) {
          uploadedFile = (init.body as FormData).get("file") as File;
          return jsonResponse({ object: "chat_attachment", data: attachment });
        }
        if (url === "/hecate/v1/chat/sessions/external-files/stream") {
          return emptyStreamResponse();
        }
        if (url === "/hecate/v1/chat/sessions/external-files/messages" && init?.method === "POST") {
          messagePayload = JSON.parse(String(init.body));
          return jsonResponse({
            object: "chat_session",
            data: session([
              {
                id: "user-file",
                execution_mode: "external_agent",
                role: "user",
                content: "Review this report",
                attachments: [attachment],
              },
              {
                id: "assistant-file",
                execution_mode: "external_agent",
                role: "assistant",
                content: "Reviewed.",
                status: "completed",
              },
            ]),
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() =>
        expect(result.current.state.activeChatSession?.id).toBe("external-files"),
      );
      const file = new File(["report"], "report.pdf", { type: "application/pdf" });
      act(() => {
        result.current.actions.setMessage("Review this report");
        result.current.actions.setPendingChatAttachments([{ id: "draft-report", file }]);
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(uploadedFile).toMatchObject({ name: "report.pdf", type: "application/pdf" });
      expect(messagePayload).toMatchObject({
        content: "Review this report",
        execution_mode: "external_agent",
        attachment_ids: ["attachment-report"],
      });
      expect(messagePayload).not.toHaveProperty("tools_enabled");
      expect(result.current.state.pendingChatAttachments).toEqual([]);
      expect(result.current.state.chatAttachmentTurnDraftCount).toBe(0);
      expect(result.current.state.activeChatSession?.messages).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ id: "user-file", attachments: [attachment] }),
        ]),
      );
    });

    it("atomically consumes image drafts and leaves an unresolved session owner unbound", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatToolsEnabled", "false");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.providerFilter", "openai");
      window.localStorage.setItem("hecate.model", "gpt-4o-mini");
      let uploadedFilename = "";
      let postedBody: Record<string, unknown> | null = null;
      let uploadStarted = false;
      let releaseUpload: ((response: Response) => void) | undefined;
      const deferredUpload = new Promise<Response>((resolve) => {
        releaseUpload = resolve;
      });
      let deferSessionRefresh = false;
      let sessionRefreshStarted = false;
      let releaseSessionRefresh: (() => void) | undefined;
      const sessionRefreshGate = new Promise<void>((resolve) => {
        releaseSessionRefresh = resolve;
      });
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/v1/models") {
          return jsonResponse({
            object: "list",
            data: [
              {
                id: "gpt-4o-mini",
                owned_by: "openai",
                metadata: {
                  provider: "openai",
                  provider_kind: "cloud",
                  capabilities: { tool_calling: "basic", image_input: "supported" },
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
                title: "Vision chat",
                agent_id: "hecate",
                status: "completed",
                provider: "openai",
                model: "gpt-4o-mini",
                message_count: 0,
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          if (deferSessionRefresh) {
            sessionRefreshStarted = true;
            await sessionRefreshGate;
          }
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Vision chat",
              agent_id: "hecate",
              status: "completed",
              provider: "openai",
              model: "gpt-4o-mini",
              capabilities: { tool_calling: "basic", image_input: "supported" },
              messages: [],
            },
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1/attachments") {
          const uploadBody = init?.body;
          if (!(uploadBody instanceof FormData)) throw new Error("expected multipart upload");
          uploadedFilename = (uploadBody.get("file") as File).name;
          uploadStarted = true;
          return deferredUpload;
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") return emptyStreamResponse();
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          postedBody = JSON.parse(String(init?.body ?? "{}"));
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Vision chat",
              agent_id: "hecate",
              status: "completed",
              provider: "openai",
              model: "gpt-4o-mini",
              messages: [],
            },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      deferSessionRefresh = true;
      const file = new File(["image"], "map.png", { type: "image/png" });
      act(() => {
        result.current.chat.actions.setActiveChatSession(null);
        result.current.runtimeConsole.actions.setMessage("inspect the map");
        result.current.runtimeConsole.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
      });
      let submission!: Promise<void>;
      act(() => {
        submission = result.current.runtimeConsole.actions.submitChat({
          preventDefault: vi.fn(),
        } as any);
      });
      await waitFor(() => expect(sessionRefreshStarted).toBe(true));
      expect(result.current.chat.actions.hasChatAttachmentTurn()).toBe(true);
      expect(result.current.chat.actions.chatAttachmentTurnSessionID()).toBe("");
      expect(result.current.runtimeConsole.state.pendingChatAttachments).toEqual([]);
      expect(result.current.runtimeConsole.state.chatAttachmentTurnDraftCount).toBe(1);
      act(() => {
        result.current.runtimeConsole.actions.setMessage("prepare the follow-up");
      });
      await act(async () => {
        await result.current.runtimeConsole.actions.submitChat({ preventDefault: vi.fn() } as any);
      });
      expect(result.current.runtimeConsole.state.queuedChatMessages).toEqual([]);
      expect(result.current.runtimeConsole.state.message).toBe("prepare the follow-up");
      act(() => releaseSessionRefresh?.());
      await waitFor(() => expect(uploadStarted).toBe(true));
      await act(async () => {
        releaseUpload?.(
          jsonResponse({
            object: "chat_attachment",
            data: {
              id: "attachment-stable-id",
              session_id: "a1",
              filename: "map.png",
              media_type: "image/png",
              size_bytes: 5,
              sha256: "abc",
              created_at: "2026-07-13T10:00:00Z",
              content_url: "/hecate/v1/chat/sessions/a1/attachments/attachment-stable-id/content",
            },
          }),
        );
        await submission;
      });

      expect(uploadedFilename).toBe("map.png");
      expect(postedBody).toMatchObject({
        content: "inspect the map",
        tools_enabled: false,
        provider: "openai",
        model: "gpt-4o-mini",
        attachment_ids: ["attachment-stable-id"],
      });
      expect(result.current.runtimeConsole.state.pendingChatAttachments).toEqual([]);
      expect(result.current.runtimeConsole.state.chatAttachmentTurnDraftCount).toBe(0);
      expect(result.current.runtimeConsole.state.message).toBe("prepare the follow-up");
    });

    it.each(["success", "failure"] as const)(
      "keeps an attachment turn on its owning session through %s",
      async (outcome) => {
        window.localStorage.setItem("hecate.chatTarget", "agent");
        window.localStorage.setItem("hecate.chatToolsEnabled", "false");
        window.localStorage.setItem("hecate.chatSessionID", "chat_a");
        window.localStorage.setItem("hecate.providerFilter", "openai");
        window.localStorage.setItem("hecate.model", "gpt-4o-mini");
        let releaseMessage: ((response: Response) => void) | undefined;
        const deferredMessage = new Promise<Response>((resolve) => {
          releaseMessage = resolve;
        });
        let messageStarted = false;
        const session = (id: string) => ({
          object: "chat_session",
          data: {
            id,
            title: id,
            agent_id: "hecate",
            status: "completed",
            provider: "openai",
            model: "gpt-4o-mini",
            capabilities: { tool_calling: "basic", image_input: "supported" },
            messages: [],
            created_at: "2026-07-13T10:00:00Z",
            updated_at: "2026-07-13T10:00:00Z",
          },
        });
        fetchMock.mockImplementation(async (input, init) => {
          const url = String(input);
          if (url === "/v1/models") {
            return jsonResponse({
              object: "list",
              data: [
                {
                  id: "gpt-4o-mini",
                  owned_by: "openai",
                  metadata: {
                    provider: "openai",
                    provider_kind: "cloud",
                    capabilities: { tool_calling: "basic", image_input: "supported" },
                  },
                },
              ],
            });
          }
          if (url === "/hecate/v1/chat/sessions") {
            return jsonResponse({
              object: "chat_sessions",
              data: [
                { id: "chat_a", title: "A", agent_id: "hecate", message_count: 0 },
                { id: "chat_b", title: "B", agent_id: "hecate", message_count: 0 },
              ],
            });
          }
          if (url === "/hecate/v1/chat/sessions/chat_a") return jsonResponse(session("chat_a"));
          if (url === "/hecate/v1/chat/sessions/chat_b") return jsonResponse(session("chat_b"));
          if (url === "/hecate/v1/chat/sessions/chat_a/attachments") {
            if (init?.method === "POST") {
              return jsonResponse({
                object: "chat_attachment",
                data: {
                  id: "attachment-stable-id",
                  session_id: "chat_a",
                  filename: "map.png",
                  media_type: "image/png",
                  size_bytes: 5,
                  sha256: "abc",
                  created_at: "2026-07-13T10:00:00Z",
                  content_url:
                    "/hecate/v1/chat/sessions/chat_a/attachments/attachment-stable-id/content",
                },
              });
            }
          }
          if (
            url === "/hecate/v1/chat/sessions/chat_a/attachments/attachment-stable-id" &&
            init?.method === "DELETE"
          ) {
            return new Response(null, { status: 204 });
          }
          if (url === "/hecate/v1/chat/sessions/chat_a/stream") return emptyStreamResponse();
          if (url === "/hecate/v1/chat/sessions/chat_a/messages") {
            messageStarted = true;
            return deferredMessage;
          }
          return defaultBackendMock()(input, init);
        });

        const { result } = renderRuntimeConsoleHook();
        await waitFor(() => expect(result.current.state.loading).toBe(false));
        await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("chat_a"));
        const file = new File(["image"], "map.png", { type: "image/png" });
        act(() => {
          result.current.actions.setMessage("inspect the map");
          result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
        });

        let submission!: Promise<void>;
        act(() => {
          submission = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
        });
        await waitFor(() => expect(messageStarted).toBe(true));
        await waitFor(() => expect(result.current.state.pendingChatAttachments).toEqual([]));

        let switched = true;
        await act(async () => {
          switched = await result.current.actions.selectChatSession("chat_b");
        });
        expect(switched).toBe(false);
        expect(result.current.state.activeChatSessionID).toBe("chat_a");
        expect(result.current.state.activeChatSession?.id).toBe("chat_a");

        act(() => {
          result.current.actions.setChatTarget("external_agent");
        });
        expect(result.current.state.chatTarget).toBe("agent");

        await act(async () => {
          if (outcome === "failure") {
            result.current.actions.setMessage("draft the follow-up");
          }
          releaseMessage?.(
            outcome === "success"
              ? jsonResponse(session("chat_a"))
              : jsonResponse({ error: { message: "message rejected" } }, 422),
          );
          await submission;
        });

        expect(result.current.state.activeChatSessionID).toBe("chat_a");
        expect(result.current.state.activeChatSession?.id).toBe("chat_a");
        const expectedAttachments = outcome === "success" ? [] : [{ id: "draft-1", file }];
        const expectedMessage =
          outcome === "success" ? "" : "inspect the map\n\ndraft the follow-up";
        expect(result.current.state.pendingChatAttachments).toEqual(expectedAttachments);
        expect(result.current.state.message).toBe(expectedMessage);
        expect(result.current.state.queuedChatMessages).toEqual([]);
      },
    );

    it.each(["success", "failure", "ambiguous"] as const)(
      "retains an explicitly submitted follow-up during its live attachment turn through %s",
      async (outcome) => {
        window.localStorage.setItem("hecate.chatTarget", "agent");
        window.localStorage.setItem("hecate.chatToolsEnabled", "false");
        window.localStorage.setItem("hecate.chatSessionID", "chat_a");
        window.localStorage.setItem("hecate.providerFilter", "openai");
        window.localStorage.setItem("hecate.model", "gpt-4o-mini");
        let releaseFirstMessage: ((response: Response) => void) | undefined;
        let rejectFirstMessage: ((reason: Error) => void) | undefined;
        const deferredFirstMessage = new Promise<Response>((resolve, reject) => {
          releaseFirstMessage = resolve;
          rejectFirstMessage = reject;
        });
        let firstMessageSettled = false;
        let messagePostCount = 0;
        let compactRequestCount = 0;
        let secondMessageStartedBeforeFirstSettled = false;
        const session = (messages: Array<Record<string, unknown>> = []) => ({
          object: "chat_session",
          data: {
            id: "chat_a",
            title: "A",
            agent_id: "hecate",
            status: "completed",
            provider: "openai",
            model: "gpt-4o-mini",
            capabilities: { tool_calling: "basic", image_input: "supported" },
            messages,
            created_at: "2026-07-13T10:00:00Z",
            updated_at: "2026-07-13T10:00:00Z",
          },
        });
        fetchMock.mockImplementation(async (input, init) => {
          const url = String(input);
          if (url === "/v1/models") {
            return jsonResponse({
              object: "list",
              data: [
                {
                  id: "gpt-4o-mini",
                  owned_by: "openai",
                  metadata: {
                    provider: "openai",
                    provider_kind: "cloud",
                    capabilities: { tool_calling: "basic", image_input: "supported" },
                  },
                },
              ],
            });
          }
          if (url === "/hecate/v1/chat/sessions") {
            return jsonResponse({
              object: "chat_sessions",
              data: [{ id: "chat_a", title: "A", agent_id: "hecate", message_count: 0 }],
            });
          }
          if (url === "/hecate/v1/chat/sessions/chat_a") return jsonResponse(session());
          if (url === "/hecate/v1/chat/sessions/chat_a/attachments" && init?.method === "POST") {
            return jsonResponse({
              object: "chat_attachment",
              data: {
                id: "attachment-stable-id",
                session_id: "chat_a",
                filename: "map.png",
                media_type: "image/png",
                size_bytes: 5,
                sha256: "abc",
                created_at: "2026-07-13T10:00:00Z",
                content_url:
                  "/hecate/v1/chat/sessions/chat_a/attachments/attachment-stable-id/content",
              },
            });
          }
          if (url === "/hecate/v1/chat/sessions/chat_a/stream") return emptyStreamResponse();
          if (url === "/hecate/v1/chat/sessions/chat_a/compact") {
            compactRequestCount += 1;
            return jsonResponse(session());
          }
          if (
            url === "/hecate/v1/chat/sessions/chat_a/attachments/attachment-stable-id" &&
            init?.method === "DELETE"
          ) {
            return new Response(null, { status: 204 });
          }
          if (url === "/hecate/v1/chat/sessions/chat_a/messages") {
            messagePostCount += 1;
            if (messagePostCount === 1) {
              const response = await deferredFirstMessage;
              firstMessageSettled = true;
              return response;
            }
            secondMessageStartedBeforeFirstSettled = !firstMessageSettled;
            return jsonResponse({
              ...session([
                {
                  id: "follow-up",
                  role: "user",
                  content: "follow up",
                  created_at: "2026-07-13T10:00:01Z",
                },
                {
                  id: "follow-up-assistant",
                  role: "assistant",
                  content: "Done.",
                  status: "completed",
                  created_at: "2026-07-13T10:00:02Z",
                },
              ]),
              message_request: { replay: false, committed_message_id: "follow-up" },
            });
          }
          return defaultBackendMock()(input, init);
        });

        const { result } = renderRuntimeConsoleHook();
        await waitFor(() => expect(result.current.state.loading).toBe(false));
        await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("chat_a"));
        const file = new File(["image"], "map.png", { type: "image/png" });
        act(() => {
          result.current.actions.setMessage("inspect the map");
          result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
        });

        let firstSubmission!: Promise<void>;
        act(() => {
          firstSubmission = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
        });
        await waitFor(() => expect(messagePostCount).toBe(1));
        await waitFor(() => expect(result.current.state.pendingChatAttachments).toEqual([]));

        act(() => {
          result.current.actions.setMessage("follow up");
        });
        await act(async () => {
          await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
        });

        expect(messagePostCount).toBe(1);
        expect(result.current.state.message).toBe("follow up");
        expect(result.current.state.queuedChatMessages).toEqual([]);
        expect(
          readQueuedChatMessagesFromStorage(window.localStorage, { preserveSubmitting: true }),
        ).toEqual([]);
        expect(result.current.state.chatError).toBe(
          "Wait for the attachment response before sending this follow-up. It remains in the composer.",
        );
        expect(result.current.state.chatErrorAction).toBe(
          "Send the retained text after the attachment response reaches a known outcome.",
        );
        let compacted = true;
        await act(async () => {
          compacted = await result.current.actions.compactChatSession();
        });
        expect(compacted).toBe(false);
        expect(compactRequestCount).toBe(0);
        expect(result.current.state.queuedChatMessages).toEqual([]);

        await act(async () => {
          if (outcome === "ambiguous") rejectFirstMessage?.(new TypeError("connection reset"));
          else {
            releaseFirstMessage?.(
              outcome === "success"
                ? jsonResponse(session())
                : jsonResponse({ error: { message: "message rejected" } }, 422),
            );
          }
          await firstSubmission;
        });

        await waitFor(() => expect(result.current.state.chatLoading).toBe(false));
        expect(secondMessageStartedBeforeFirstSettled).toBe(false);
        expect(messagePostCount).toBe(1);
        expect(result.current.state.queuedChatMessages).toEqual([]);
        expect(result.current.state.pendingChatAttachments).toEqual(
          outcome === "failure" ? [{ id: "draft-1", file }] : [],
        );
        expect(result.current.state.message).toBe(
          outcome === "failure" ? "inspect the map\n\nfollow up" : "follow up",
        );
      },
    );

    it("restores the consumed prompt and files around later edits when upload is rejected", async () => {
      const mocked = mockImageSubmissionFailure({
        committed: false,
        status: 422,
        uploadFailureDeferred: true,
      });
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      const originalPrompt = "  inspect\n  the map  \n";
      act(() => {
        result.current.actions.setMessage(originalPrompt);
        result.current.actions.setPendingChatAttachments([{ id: "draft-1", file: mocked.file }]);
      });

      let submission!: Promise<void>;
      act(() => {
        submission = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });
      await waitFor(() => expect(result.current.state.pendingChatAttachments).toEqual([]));
      expect(result.current.state.chatAttachmentTurnDraftCount).toBe(1);
      act(() => {
        result.current.actions.setMessage("draft the follow-up");
      });

      await act(async () => {
        mocked.releaseUploadFailure?.(
          jsonResponse(
            {
              error: {
                type: "chat.attachment_rejected",
                message: "upload rejected",
              },
            },
            422,
          ),
        );
        await submission;
      });

      expect(result.current.state.pendingChatAttachments).toEqual([
        { id: "draft-1", file: mocked.file },
      ]);
      expect(result.current.state.message).toBe(`${originalPrompt}\n\ndraft the follow-up`);
      expect(result.current.state.chatAttachmentTurnDraftCount).toBe(0);
      expect(result.current.state.chatError).toBe("upload rejected");
    });

    it("restores local input and warns when an upload response is ambiguous", async () => {
      const submission = mockImageSubmissionFailure({
        ambiguousUpload: "network",
        committed: false,
        status: "network",
        uploadFailureDeferred: true,
      });
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      const whitespaceOnlyPrompt = " \n  ";
      act(() => {
        result.current.actions.setMessage(whitespaceOnlyPrompt);
        result.current.actions.setPendingChatAttachments([
          { id: "draft-1", file: submission.file },
        ]);
      });

      let pendingSubmission!: Promise<void>;
      act(() => {
        pendingSubmission = result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });
      await waitFor(() => expect(result.current.state.pendingChatAttachments).toEqual([]));
      expect(result.current.state.chatAttachmentTurnDraftCount).toBe(1);
      act(() => {
        result.current.actions.setMessage("draft the follow-up");
      });

      await act(async () => {
        submission.rejectUploadFailure?.(new TypeError("connection reset after upload commit"));
        await pendingSubmission;
      });

      expect(submission.deleteCount).toBe(0);
      expect(result.current.state.pendingChatAttachments).toEqual([
        { id: "draft-1", file: submission.file },
      ]);
      expect(result.current.state.message).toBe(`${whitespaceOnlyPrompt}\n\ndraft the follow-up`);
      expect(result.current.state.chatAttachmentTurnDraftCount).toBe(0);
      expect(result.current.state.chatError).toContain(
        "The file upload response could not be confirmed.",
      );
      expect(result.current.state.chatError).toContain("one unlinked server copy may remain");
      expect(result.current.state.chatError).toContain(
        "later upload reclaims stale drafts after 24 hours",
      );
      expect(result.current.state.chatError).toContain("will not be retried automatically");
      expect(result.current.state.chatErrorStatus).toBeNull();
    });

    it("treats a Hecate-shaped upload 500 as ambiguous after a possible commit", async () => {
      const submission = mockImageSubmissionFailure({
        ambiguousUpload: "hecate_500",
        committed: false,
        status: 500,
      });
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      act(() => {
        result.current.actions.setMessage("inspect the map");
        result.current.actions.setPendingChatAttachments([
          { id: "draft-1", file: submission.file },
        ]);
      });

      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(submission.uploadCount).toBe(1);
      expect(submission.deleteCount).toBe(0);
      expect(result.current.state.pendingChatAttachments).toEqual([
        { id: "draft-1", file: submission.file },
      ]);
      expect(result.current.state.message).toBe("inspect the map");
      expect(result.current.state.chatError).toContain(
        "The file upload response could not be confirmed.",
      );
      expect(result.current.state.chatErrorStatus).toBe(500);
      expect(result.current.state.chatErrorCode).toBe("gateway_error");
      expect(result.current.state.chatError).not.toContain("attachment commit result was lost");
      expect(result.current.state.chatErrorAction).toContain("will not be retried automatically");
    });

    it("cleans acknowledged drafts and preserves typed ambiguity after a partial upload", async () => {
      const submission = mockImageSubmissionFailure({
        ambiguousUpload: "proxy_502",
        ambiguousUploadAt: 2,
        committed: false,
        deleteFails: true,
        status: 502,
      });
      const secondFile = new File(["second"], "detail.png", { type: "image/png" });
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      act(() => {
        result.current.actions.setMessage("compare these images");
        result.current.actions.setPendingChatAttachments([
          { id: "draft-1", file: submission.file },
          { id: "draft-2", file: secondFile },
        ]);
      });

      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(submission.deleteCount).toBe(2);
      expect(result.current.state.pendingChatAttachments).toEqual([
        { id: "draft-1", file: submission.file },
        { id: "draft-2", file: secondFile },
      ]);
      expect(result.current.state.message).toBe("compare these images");
      expect(result.current.state.chatError).toContain(
        "The file upload response could not be confirmed.",
      );
      expect(result.current.state.chatError).toContain(
        "could not delete 1 acknowledged file draft after retrying cleanup",
      );
      expect(result.current.state.chatErrorStatus).toBe(502);
      expect(result.current.state.chatErrorCode).toBe("");
      expect(result.current.state.chatErrorRequestID).toBe("request-upload-502");
      expect(result.current.state.chatErrorTraceID).toBe("trace-upload-502");
      expect(result.current.state.chatError).not.toContain("proxy lost");
      expect(result.current.state.chatError).not.toContain("attachment-stable-id");
      expect(result.current.state.chatError).not.toContain("internal-secret-42");
      expect(result.current.state.chatErrorAction).not.toContain("attachment-stable-id");
      expect(result.current.state.chatErrorAction).not.toContain("internal-secret-42");
      expect(result.current.state.chatErrorAction).toContain(
        "delete this chat before uploading the files again",
      );
    });

    it("keeps a committed image message and warns when a 500 response has no visible model response", async () => {
      const submission = mockImageSubmissionFailure({ committed: true, status: 500 });
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      act(() => {
        result.current.actions.setMessage("inspect the map");
        result.current.actions.setPendingChatAttachments([
          { id: "draft-1", file: submission.file },
        ]);
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(submission.deleteCount).toBe(0);
      expect(result.current.state.pendingChatAttachments).toEqual([]);
      expect(result.current.state.message).toBe("");
      expect(result.current.state.activeChatSession?.messages).toEqual([
        expect.objectContaining({
          id: "user-committed",
          attachments: [expect.objectContaining({ id: "attachment-stable-id" })],
        }),
      ]);
      expect(result.current.state.chatError).toBe(
        "The message was accepted, but its model response could not be confirmed. Do not send it again. Refresh this chat to check the model run.",
      );
    });

    it("clears implicit-create recovery artifacts when reconciliation proves the image commit", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatToolsEnabled", "false");
      window.localStorage.setItem("hecate.providerFilter", "openai");
      window.localStorage.setItem("hecate.model", "gpt-4o-mini");
      const prompt = "inspect the map";
      const file = new File(["image"], "map.png", { type: "image/png" });
      const attachment = {
        id: "attachment-created-chat",
        session_id: "created_image_chat",
        filename: "map.png",
        media_type: "image/png",
        size_bytes: 5,
        sha256: "abc",
        created_at: "2026-07-13T10:00:00Z",
        content_url:
          "/hecate/v1/chat/sessions/created_image_chat/attachments/attachment-created-chat/content",
      };
      const createdSession = (messages: Array<Record<string, unknown>> = []) => ({
        id: "created_image_chat",
        title: "Inspect the map",
        agent_id: "hecate",
        status: "completed",
        workspace: "",
        provider: "openai",
        model: "gpt-4o-mini",
        capabilities: { tool_calling: "basic", image_input: "supported" },
        messages,
        created_at: "2026-07-13T10:00:00Z",
        updated_at: "2026-07-13T10:00:01Z",
      });
      let messagePostStarted = false;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/v1/models") {
          return jsonResponse({
            object: "list",
            data: [
              {
                id: "gpt-4o-mini",
                owned_by: "openai",
                metadata: {
                  provider: "openai",
                  provider_kind: "cloud",
                  capabilities: { tool_calling: "basic", image_input: "supported" },
                },
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions" && init?.method === "POST") {
          return jsonResponse({ object: "chat_session", data: createdSession() });
        }
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({ object: "chat_sessions", data: [] });
        }
        if (url === "/hecate/v1/chat/sessions/created_image_chat") {
          return jsonResponse({
            object: "chat_session",
            data: createdSession(
              messagePostStarted
                ? [
                    {
                      id: "user-committed",
                      role: "user",
                      content: prompt,
                      attachments: [attachment],
                      created_at: "2026-07-13T10:00:01Z",
                    },
                  ]
                : [],
            ),
          });
        }
        if (
          url === "/hecate/v1/chat/sessions/created_image_chat/attachments" &&
          init?.method === "POST"
        ) {
          return jsonResponse({ object: "chat_attachment", data: attachment });
        }
        if (url === "/hecate/v1/chat/sessions/created_image_chat/stream") {
          return emptyStreamResponse();
        }
        if (
          url === "/hecate/v1/chat/sessions/created_image_chat/messages" &&
          init?.method === "POST"
        ) {
          messagePostStarted = true;
          throw new TypeError("connection reset after commit");
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook({
        chatInitialState: {
          composerDraftsBySessionID: new Map([["created_image_chat", prompt]]),
          savedComposerDraftsBySessionID: new Map([["created_image_chat", [prompt]]]),
        },
      });
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));
      await waitFor(() => expect(result.current.runtimeConsole.state.model).toBe("gpt-4o-mini"));
      act(() => {
        result.current.runtimeConsole.actions.setMessage(prompt);
        result.current.runtimeConsole.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
      });

      await act(async () => {
        await result.current.runtimeConsole.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(messagePostStarted).toBe(true);
      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("created_image_chat");
      expect(result.current.runtimeConsole.state.activeChatSession?.messages).toEqual([
        expect.objectContaining({
          id: "user-committed",
          attachments: [expect.objectContaining({ id: attachment.id })],
        }),
      ]);
      expect(result.current.runtimeConsole.state.message).toBe("");
      expect(result.current.runtimeConsole.state.pendingChatAttachments).toEqual([]);
      expect(result.current.chat.state.recoverableComposerDraft).toBeNull();
      expect(result.current.chat.state.activeRecoverableComposerDraftID).toBeNull();
      expect(result.current.chat.state.composerDraftsBySessionID.has("created_image_chat")).toBe(
        false,
      );
      expect(
        result.current.chat.state.savedComposerDraftsBySessionID.has("created_image_chat"),
      ).toBe(false);
    });

    it("warns when reconciliation finds the committed image message with a running assistant", async () => {
      const submission = mockImageSubmissionFailure({
        committed: true,
        reconciledAssistantStatus: "running",
        status: "network",
      });
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      act(() => {
        result.current.actions.setMessage("inspect the map");
        result.current.actions.setPendingChatAttachments([
          { id: "draft-1", file: submission.file },
        ]);
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(submission.deleteCount).toBe(0);
      expect(result.current.state.pendingChatAttachments).toEqual([]);
      expect(result.current.state.message).toBe("");
      expect(result.current.state.activeChatSession?.messages).toEqual([
        expect.objectContaining({ id: "user-committed" }),
        expect.objectContaining({ id: "assistant-reconciled", status: "running" }),
      ]);
      expect(result.current.state.chatError).toBe(
        "The message was accepted, but its model response could not be confirmed. Do not send it again. Refresh this chat to check the model run.",
      );
    });

    it("keeps a network exception ambiguous when reconciliation sees no committed message", async () => {
      const submission = mockImageSubmissionFailure({ committed: false, status: "network" });
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      act(() => {
        result.current.actions.setMessage("inspect the map");
        result.current.actions.setPendingChatAttachments([
          { id: "draft-1", file: submission.file },
        ]);
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(submission.deleteCount).toBe(0);
      expect(result.current.state.pendingChatAttachments).toEqual([]);
      expect(result.current.state.message).toBe("");
      expect(result.current.state.activeChatSession?.messages).toEqual([]);
      expect(result.current.state.chatError).toBe(
        "The message submission could not be confirmed. Refresh this chat before sending again to avoid a duplicate model run.",
      );
    });

    it.each([409, 429, 500] as const)(
      "deletes uploaded drafts and restores local input after HTTP %i when reconciliation proves no commit",
      async (status) => {
        const submission = mockImageSubmissionFailure({ committed: false, status });
        const { result } = renderRuntimeConsoleHook();
        await waitFor(() => expect(result.current.state.loading).toBe(false));

        act(() => {
          result.current.actions.setMessage("inspect the map");
          result.current.actions.setPendingChatAttachments([
            { id: "draft-1", file: submission.file },
          ]);
        });
        await act(async () => {
          await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
        });

        expect(submission.deleteCount).toBe(1);
        expect(result.current.state.pendingChatAttachments).toEqual([
          { id: "draft-1", file: submission.file },
        ]);
        expect(result.current.state.message).toBe("inspect the map");
        expect(result.current.state.activeChatSession?.messages).toEqual([]);
        expect(result.current.state.chatError).toBe("attachment rejected");
        expect(result.current.state.chatErrorStatus).toBe(status);
      },
    );

    it("deletes uploaded drafts and restores local files after a definite rejection", async () => {
      const submission = mockImageSubmissionFailure({ committed: false, status: 422 });
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      act(() => {
        result.current.actions.setMessage("inspect the map");
        result.current.actions.setPendingChatAttachments([
          { id: "draft-1", file: submission.file },
        ]);
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(submission.deleteCount).toBe(1);
      expect(result.current.state.pendingChatAttachments).toEqual([
        { id: "draft-1", file: submission.file },
      ]);
      expect(result.current.state.message).toBe("inspect the map");
      expect(result.current.state.activeChatSession?.messages).toEqual([]);
      expect(result.current.state.chatError).toBe("attachment rejected");
      expect(result.current.state.chatErrorStatus).toBe(422);
    });

    it("retries failed draft cleanup and warns about retained server data", async () => {
      const submission = mockImageSubmissionFailure({
        committed: false,
        deleteFails: true,
        status: 422,
      });
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      act(() => {
        result.current.actions.setMessage("inspect the map");
        result.current.actions.setPendingChatAttachments([
          { id: "draft-1", file: submission.file },
        ]);
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(submission.deleteCount).toBe(2);
      expect(result.current.state.pendingChatAttachments).toEqual([
        { id: "draft-1", file: submission.file },
      ]);
      expect(result.current.state.message).toBe("inspect the map");
      expect(result.current.state.activeChatSession?.messages).toEqual([]);
      expect(result.current.state.chatError).toContain("attachment rejected");
      expect(result.current.state.chatError).toContain(
        "could not delete 1 uploaded file draft after retrying cleanup",
      );
      expect(result.current.state.chatError).toContain(
        "later upload reclaims stale drafts after 24 hours",
      );
      expect(result.current.state.chatErrorStatus).toBe(422);
      expect(result.current.state.chatErrorCode).toBe("chat.attachment_rejected");
      expect(result.current.state.chatErrorAction).toContain(
        "wait until after 24 hours before a later upload triggers stale-draft reclamation",
      );
      expect(result.current.state.chatErrorAction).toContain("Delete this chat");
    });

    it("preserves a known 422 precommit rejection when reconciliation GET fails", async () => {
      const submission = mockImageSubmissionFailure({
        committed: false,
        reconciliationFails: true,
        status: 422,
      });
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      act(() => {
        result.current.actions.setMessage("inspect the map");
        result.current.actions.setPendingChatAttachments([
          { id: "draft-1", file: submission.file },
        ]);
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(submission.deleteCount).toBe(1);
      expect(result.current.state.pendingChatAttachments).toEqual([
        { id: "draft-1", file: submission.file },
      ]);
      expect(result.current.state.message).toBe("inspect the map");
      expect(result.current.state.chatError).toBe("attachment rejected");
      expect(result.current.state.chatErrorStatus).toBe(422);
    });

    it("keeps a code-empty proxy 502 ambiguous even when reconciliation GET is empty", async () => {
      const submission = mockImageSubmissionFailure({
        codedError: false,
        committed: false,
        status: 502,
      });
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      act(() => {
        result.current.actions.setMessage("inspect the map");
        result.current.actions.setPendingChatAttachments([
          { id: "draft-1", file: submission.file },
        ]);
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(submission.deleteCount).toBe(0);
      expect(result.current.state.pendingChatAttachments).toEqual([]);
      expect(result.current.state.message).toBe("");
      expect(result.current.state.chatError).toBe(
        "The message submission could not be confirmed. Refresh this chat before sending again to avoid a duplicate model run.",
      );
      expect(result.current.state.chatErrorStatus).toBeNull();
    });

    it("keeps a coded proxy 502 ambiguous even when reconciliation GET is empty", async () => {
      const submission = mockImageSubmissionFailure({
        committed: false,
        errorCode: "proxy.upstream_timeout",
        status: 502,
      });
      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));

      act(() => {
        result.current.actions.setMessage("inspect the map");
        result.current.actions.setPendingChatAttachments([
          { id: "draft-1", file: submission.file },
        ]);
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(submission.deleteCount).toBe(0);
      expect(result.current.state.pendingChatAttachments).toEqual([]);
      expect(result.current.state.message).toBe("");
      expect(result.current.state.chatError).toBe(
        "The message submission could not be confirmed. Refresh this chat before sending again to avoid a duplicate model run.",
      );
      expect(result.current.state.chatErrorStatus).toBeNull();
    });

    it.each([
      ["project-owned", "project_1"],
      ["project-free", ""],
    ])(
      "queues a %s prompt while the active agent run is busy and sends it after completion",
      async (_label, projectID) => {
        window.localStorage.setItem("hecate.chatTarget", "agent");
        window.localStorage.setItem("hecate.chatSessionID", "a1");
        window.localStorage.setItem("hecate.agentWorkspace", "/workspace");
        if (projectID === "") {
          window.localStorage.setItem(
            queuedChatDeletedProjectStorageKey("unrelated-project"),
            "deleted:v2:0:integration-test",
          );
        }
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
                  ...(projectID ? { project_id: projectID } : {}),
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
                ...(projectID ? { project_id: projectID } : {}),
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
                  {
                    id: "a1",
                    agent_id: "hecate",
                    role: "assistant",
                    content: "Done.",
                    status: "completed",
                    created_at: "2026-04-20T00:00:02Z",
                  },
                ],
                created_at: "2026-04-20T00:00:00Z",
                updated_at: new Date().toISOString(),
              },
              message_request: { replay: false, committed_message_id: "u1" },
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

        expect(result.current.state.chatError).toBe("");
        expect(messagePostCount).toBe(0);
        expect(result.current.state.message).toBe("");
        expect(result.current.state.queuedChatMessages).toHaveLength(1);
        expect(result.current.state.queuedChatMessages[0].session_id).toBe("a1");
        expect(result.current.state.queuedChatMessages[0].project_id).toBe(projectID);
        expect(result.current.state.queuedChatMessages[0].content).toBe("after this");
        expect(result.current.state.queuedChatMessages[0].agent_id).toBe("hecate");
        expect(
          readQueuedChatMessagesFromStorage(window.localStorage).find(
            (queued) => queued.id === result.current.state.queuedChatMessages[0].id,
          )?.project_id,
        ).toBe(projectID);

        sessionStatus = "completed";
        await act(async () => {
          await result.current.actions.selectChatSession("a1");
        });

        await waitFor(() => expect(messagePostCount).toBe(1));
        await waitFor(() => expect(result.current.state.queuedChatMessages).toHaveLength(0));
      },
    );

    it("preserves the composer when browser storage refuses initial queue admission", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.agentWorkspace", "/workspace");
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
                status: "running",
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
              status: "running",
              workspace: "/workspace",
              provider: "openai",
              model: "gpt-4o-mini",
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:00Z",
            },
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") return emptyStreamResponse();
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
          return jsonResponse({ object: "chat_session", data: {} });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.activeChatSession?.status).toBe("running"));
      const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
      const storageSpy = vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
        if (key.startsWith("hecate.queuedChatMessages.item.")) {
          throw new DOMException("quota exceeded", "QuotaExceededError");
        }
        return originalSetItem(key, value);
      });

      try {
        act(() => result.current.actions.setMessage("do not lose this prompt"));
        await act(async () => {
          await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
        });

        expect(messagePostCount).toBe(0);
        expect(result.current.state.message).toBe("do not lose this prompt");
        expect(result.current.state.queuedChatMessages).toEqual([]);
        expect(result.current.state.chatErrorCode).toBe("chat.queue_storage_unavailable");
        expect(result.current.state.chatErrorAction).toMatch(/still in the composer/i);
      } finally {
        storageSpy.mockRestore();
      }
    });

    it("never retargets a queued message when its original session has a different runtime owner", async () => {
      persistQueuedPrompt();
      let createPostCount = 0;
      let messagePostCount = 0;
      const externalSession = {
        object: "chat_session",
        data: {
          id: "a1",
          title: "External chat",
          agent_id: "codex",
          status: "completed",
          workspace: "/workspace",
          messages: [],
          created_at: "2026-04-20T00:00:00Z",
          updated_at: "2026-04-20T00:00:00Z",
        },
      };
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          if (init?.method === "POST") createPostCount += 1;
          return jsonResponse({
            object: "chat_sessions",
            data: [
              {
                id: "a1",
                title: "External chat",
                agent_id: "codex",
                status: "completed",
                workspace: "/workspace",
                message_count: 0,
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1") return jsonResponse(externalSession);
        if (url === "/hecate/v1/chat/sessions/a1/stream") return emptyStreamResponse();
        if (url.endsWith("/messages")) {
          messagePostCount += 1;
          throw new Error(`unexpected queued retarget: ${url}`);
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() =>
        expect(result.current.state.chatError).toContain(
          "permanently scoped to a chat with a different runtime owner",
        ),
      );
      expect(createPostCount).toBe(0);
      expect(messagePostCount).toBe(0);
      expect(result.current.state.queuedChatMessages).toEqual([
        expect.objectContaining({
          id: "queued_retry",
          session_id: "a1",
          delivery_state: "retryable",
        }),
      ]);
    });

    it("blocks FIFO when a keyed success omits exact committed-message metadata", async () => {
      persistQueuedPrompt();
      const legacy = JSON.parse(
        window.localStorage.getItem("hecate.queuedChatMessages") ?? "[]",
      ) as Array<Record<string, unknown>>;
      legacy.push({
        ...legacy[0],
        id: "queued_second",
        content: "later work",
        created_at: "2026-04-20T00:00:02Z",
      });
      window.localStorage.setItem("hecate.queuedChatMessages", JSON.stringify(legacy));
      let messagePostCount = 0;
      const postedContents: string[] = [];
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse(queuedPromptSessionList());
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse(queuedPromptSession());
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") return emptyStreamResponse();
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
          const payload = JSON.parse(String(init?.body ?? "{}"));
          postedContents.push(payload.content);
          return jsonResponse(
            queuedPromptSession([
              {
                id: "u-no-metadata",
                role: "user",
                content: payload.content,
                created_at: "2026-04-20T00:00:03Z",
              },
              {
                id: "a-no-metadata",
                role: "assistant",
                content: "Done.",
                status: "completed",
                created_at: "2026-04-20T00:00:04Z",
              },
            ]),
          );
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(messagePostCount).toBe(1));
      await waitFor(() =>
        expect(result.current.state.chatError).toContain(
          "without exact committed-message metadata",
        ),
      );
      expect(postedContents).toEqual(["after this"]);
      expect(result.current.state.queuedChatMessages).toEqual([
        expect.objectContaining({
          id: "queued_retry",
          delivery_state: "reconcile_required",
        }),
        expect.objectContaining({ id: "queued_second" }),
      ]);
    });

    it("retains a queued prompt after a definite rejection and retries only on request", async () => {
      persistQueuedPrompt();
      let messagePostCount = 0;
      const postedContents: string[] = [];
      const postedClientRequestIDs: string[] = [];
      let storedAtFirstPost: Array<Record<string, unknown>> = [];
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse(queuedPromptSessionList());
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse(queuedPromptSession());
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") {
          return emptyStreamResponse();
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
          if (messagePostCount === 1) {
            storedAtFirstPost = readQueuedChatMessagesFromStorage(window.localStorage);
          }
          const payload = JSON.parse(String(init?.body ?? "{}"));
          postedContents.push(payload.content);
          postedClientRequestIDs.push(payload.client_request_id);
          if (messagePostCount === 1) {
            return jsonResponse({ error: { message: "message rejected" } }, 422);
          }
          return jsonResponse({
            ...queuedPromptSession([
              {
                id: "u2",
                role: "user",
                content: payload.content,
                created_at: "2026-04-20T00:00:02Z",
              },
              {
                id: "a2",
                role: "assistant",
                content: "Done.",
                status: "completed",
                created_at: "2026-04-20T00:00:03Z",
              },
            ]),
            message_request: { replay: false, committed_message_id: "u2" },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(messagePostCount).toBe(1));
      await waitFor(() => expect(result.current.state.chatLoading).toBe(false));
      await waitFor(() => expect(result.current.state.chatError).toContain("message rejected"));

      expect(result.current.state.queuedChatMessages).toEqual([
        expect.objectContaining({
          id: "queued_retry",
          content: "after this",
          delivery_state: "retryable",
          delivery_baseline_message_ids: [],
        }),
      ]);
      expect(storedAtFirstPost).toEqual([
        expect.objectContaining({
          id: "queued_retry",
          delivery_state: "submitting",
          delivery_baseline_message_ids: [],
        }),
      ]);
      expect(
        result.current.state.activeChatSession?.messages?.some((message) =>
          message.id.startsWith("pending-agent-user-"),
        ),
      ).toBe(false);
      expect(messagePostCount).toBe(1);

      act(() => {
        result.current.actions.updateQueuedChatMessage("queued_retry", "after this, corrected");
      });
      await waitFor(() =>
        expect(result.current.state.queuedChatMessages[0]).toEqual(
          expect.objectContaining({
            content: "after this, corrected",
            delivery_state: "retryable",
          }),
        ),
      );
      expect(messagePostCount).toBe(1);

      act(() => {
        result.current.actions.retryQueuedChatMessage("queued_retry");
      });

      await waitFor(() => expect(messagePostCount).toBe(2));
      await waitFor(() => expect(result.current.state.queuedChatMessages).toEqual([]));
      expect(postedContents).toEqual(["after this", "after this, corrected"]);
      expect(postedClientRequestIDs).toEqual(["queued_retry", "queued_retry"]);
    });

    it.each(["basic", "none"] as const)(
      "sends the stored queued tools snapshot when current model capability is %s",
      async (toolCalling) => {
        persistQueuedPrompt();
        const stored = JSON.parse(
          window.localStorage.getItem("hecate.queuedChatMessages") ?? "[]",
        ) as Array<Record<string, unknown>>;
        stored[0] = { ...stored[0], tools_enabled: true, workspace: "/workspace" };
        window.localStorage.setItem("hecate.queuedChatMessages", JSON.stringify(stored));
        const postedToolsEnabled: boolean[] = [];
        fetchMock.mockImplementation(async (input, init) => {
          const url = String(input);
          if (url === "/v1/models") {
            return jsonResponse({
              object: "list",
              data: [
                {
                  id: "gpt-4o-mini",
                  owned_by: "openai",
                  metadata: {
                    provider: "openai",
                    provider_kind: "cloud",
                    capabilities: { tool_calling: toolCalling },
                  },
                },
              ],
            });
          }
          if (url === "/hecate/v1/chat/sessions") {
            return jsonResponse(queuedPromptSessionList());
          }
          if (url === "/hecate/v1/chat/sessions/a1") {
            return jsonResponse(queuedPromptSession());
          }
          if (url === "/hecate/v1/chat/sessions/a1/stream") {
            return emptyStreamResponse();
          }
          if (url === "/hecate/v1/chat/sessions/a1/messages") {
            const payload = JSON.parse(String(init?.body ?? "{}"));
            postedToolsEnabled.push(payload.tools_enabled);
            const messages = [
              {
                id: "u-tools",
                role: "user",
                content: "after this",
                created_at: "2026-04-20T00:00:02Z",
              },
              {
                id: "a-tools",
                role: "assistant",
                content: "Done.",
                status: "completed",
                created_at: "2026-04-20T00:00:03Z",
              },
            ];
            return jsonResponse({
              ...queuedPromptSession(messages),
              message_request: { replay: false, committed_message_id: "u-tools" },
            });
          }
          return defaultBackendMock()(input, init);
        });

        const { result } = renderRuntimeConsoleHook();
        await waitFor(() => expect(result.current.state.queuedChatMessages).toEqual([]));
        expect(postedToolsEnabled).toEqual([true]);
      },
    );

    it("does not dispatch when browser storage cannot persist the submitting fence", async () => {
      persistQueuedPrompt();
      const [queued] = JSON.parse(
        window.localStorage.getItem("hecate.queuedChatMessages") ?? "[]",
      ) as Array<Record<string, unknown>>;
      const readyRevision = "revision-ready";
      const readyKey = queuedChatMessageStorageKey("queued_retry", "0", readyRevision);
      window.localStorage.setItem(
        readyKey,
        JSON.stringify({
          ...queued,
          delivery_storage_epoch: "0",
          delivery_storage_revision: readyRevision,
        }),
      );
      window.localStorage.setItem("hecate.queuedChatMessages.v2", "1");
      window.localStorage.removeItem("hecate.queuedChatMessages");
      const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
      const storageSpy = vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
        if (isQueuedItemStorageKey(key, "queued_retry") && key !== readyKey) {
          throw new DOMException("quota exceeded", "QuotaExceededError");
        }
        return originalSetItem(key, value);
      });
      let messagePostCount = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse(queuedPromptSessionList());
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse(queuedPromptSession());
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") {
          return emptyStreamResponse();
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
          void init;
          return jsonResponse(queuedPromptSession());
        }
        return defaultBackendMock()(input, init);
      });

      try {
        const { result } = renderRuntimeConsoleHook();
        await waitFor(() =>
          expect(result.current.state.notice?.message).toContain(
            "browser storage could not persist its submission fence",
          ),
        );

        expect(messagePostCount).toBe(0);
        expect(result.current.state.queuedChatMessages).toEqual([
          expect.objectContaining({
            id: "queued_retry",
            delivery_state: "retryable",
          }),
        ]);
        const stored = readQueuedChatMessagesFromStorage(window.localStorage);
        expect(stored).toEqual([]);
      } finally {
        storageSpy.mockRestore();
      }
    });

    it("keeps a conflicted queued request blocked even when another tab committed the same text", async () => {
      persistQueuedPrompt();
      let conflictSeen = false;
      let messagePostCount = 0;
      let sessionGetCount = 0;
      let postedClientRequestID = "";
      const otherTabMessage = {
        id: "u-other-tab",
        role: "user",
        content: "after this",
        created_at: "2026-04-20T00:00:02Z",
      };
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse(queuedPromptSessionList());
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          sessionGetCount += 1;
          return jsonResponse(queuedPromptSession(conflictSeen ? [otherTabMessage] : []));
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") {
          return emptyStreamResponse();
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
          const payload = JSON.parse(String(init?.body ?? "{}"));
          postedClientRequestID = payload.client_request_id;
          conflictSeen = true;
          return jsonResponse(
            {
              error: {
                type: "chat.client_request_conflict",
                message: "client_request_id already has a different payload",
              },
            },
            409,
          );
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() =>
        expect(result.current.state.chatError).toContain("committed to a different payload"),
      );
      expect(messagePostCount).toBe(1);
      expect(postedClientRequestID).toBe("queued_retry");
      expect(result.current.state.queuedChatMessages).toEqual([
        expect.objectContaining({
          id: "queued_retry",
          delivery_state: "reconcile_required",
          delivery_error_code: "chat.client_request_conflict",
        }),
      ]);
      const sessionGetsBeforeCheck = sessionGetCount;

      await act(async () => {
        await result.current.actions.reconcileQueuedChatMessage("queued_retry");
      });
      expect(messagePostCount).toBe(1);
      expect(sessionGetCount).toBe(sessionGetsBeforeCheck);
      expect(result.current.state.queuedChatMessages).toEqual([
        expect.objectContaining({
          id: "queued_retry",
          delivery_state: "reconcile_required",
          delivery_error_code: "chat.client_request_conflict",
        }),
      ]);
      expect(result.current.state.chatError).toContain(
        "Matching transcript text cannot prove this queued request was delivered",
      );
    });

    it("replays the exact queued key after an ambiguous transport failure", async () => {
      persistQueuedPrompt();
      let messagePostCount = 0;
      let sessionGetCount = 0;
      let postReachedServer = false;
      const committedMessages = [
        {
          id: "u1",
          role: "user",
          content: "after this",
          created_at: "2026-04-20T00:00:02Z",
        },
        {
          id: "assistant-1",
          role: "assistant",
          content: "Done.",
          status: "completed",
          created_at: "2026-04-20T00:00:03Z",
        },
      ];
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse(queuedPromptSessionList());
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          sessionGetCount += 1;
          return jsonResponse(queuedPromptSession(postReachedServer ? committedMessages : []));
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") {
          return emptyStreamResponse();
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
          void init;
          postReachedServer = true;
          if (messagePostCount === 1) throw new Error("connection dropped after commit");
          return jsonResponse({
            ...queuedPromptSession(committedMessages),
            message_request: { replay: true, committed_message_id: "u1" },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() =>
        expect(result.current.state.queuedChatMessages).toEqual([
          expect.objectContaining({
            id: "queued_retry",
            delivery_state: "reconcile_required",
            delivery_idempotency_keyed: true,
          }),
        ]),
      );
      await waitFor(() => expect(result.current.state.chatLoading).toBe(false));

      expect(messagePostCount).toBe(1);
      await act(async () => {
        await result.current.actions.reconcileQueuedChatMessage("queued_retry");
      });
      await waitFor(() => expect(result.current.state.queuedChatMessages).toEqual([]));

      expect(messagePostCount).toBe(2);
      expect(sessionGetCount).toBeGreaterThanOrEqual(1);
      expect(
        result.current.state.activeChatSession?.messages?.map((message) => message.id),
      ).toEqual(["u1", "assistant-1"]);
      expect(result.current.state.chatError).toBe("");
    });

    it("keeps an in-flight keyed replay as the FIFO barrier until its exact turn is terminal", async () => {
      persistQueuedPrompt();
      const [storedFirst] = JSON.parse(
        window.localStorage.getItem("hecate.queuedChatMessages") ?? "[]",
      ) as Array<Record<string, unknown>>;
      window.localStorage.setItem(
        "hecate.queuedChatMessages",
        JSON.stringify([
          storedFirst,
          {
            ...storedFirst,
            id: "queued_second",
            content: "after that",
            created_at: "2026-04-20T00:00:02Z",
          },
        ]),
      );

      const firstRunningMessages = [
        {
          id: "u-first",
          role: "user",
          content: "after this",
          created_at: "2026-04-20T00:00:03Z",
        },
        {
          id: "a-first",
          role: "assistant",
          content: "Working…",
          status: "running",
          created_at: "2026-04-20T00:00:04Z",
        },
      ];
      const firstTerminalMessages = [
        firstRunningMessages[0],
        {
          ...firstRunningMessages[1],
          content: "Done.",
          status: "completed",
          created_at: "2026-04-20T00:00:05Z",
        },
      ];
      let firstStreamController: ReadableStreamDefaultController<Uint8Array> | undefined;
      let firstStreamClosed = false;
      let streamCount = 0;
      const posted: Array<{ content: string; client_request_id: string }> = [];
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse(queuedPromptSessionList());
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse(queuedPromptSession());
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") {
          streamCount += 1;
          if (streamCount > 1) return emptyStreamResponse();
          const stream = new ReadableStream<Uint8Array>({
            start(controller) {
              firstStreamController = controller;
              init?.signal?.addEventListener(
                "abort",
                () => {
                  if (firstStreamClosed) return;
                  firstStreamClosed = true;
                  controller.close();
                },
                { once: true },
              );
            },
          });
          return new Response(stream, {
            status: 200,
            headers: { "Content-Type": "text/event-stream" },
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          const payload = JSON.parse(String(init?.body ?? "{}"));
          posted.push({
            content: payload.content,
            client_request_id: payload.client_request_id,
          });
          if (posted.length === 1) {
            return jsonResponse({
              ...queuedPromptSession(firstRunningMessages),
              data: { ...queuedPromptSession(firstRunningMessages).data, status: "running" },
              message_request: { replay: true, committed_message_id: "u-first" },
            });
          }
          const secondMessages = [
            ...firstTerminalMessages,
            {
              id: "u-second",
              role: "user",
              content: "after that",
              created_at: "2026-04-20T00:00:06Z",
            },
            {
              id: "a-second",
              role: "assistant",
              content: "Also done.",
              status: "completed",
              created_at: "2026-04-20T00:00:07Z",
            },
          ];
          return jsonResponse({
            ...queuedPromptSession(secondMessages),
            message_request: { replay: false, committed_message_id: "u-second" },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(posted).toHaveLength(1));
      await waitFor(() => {
        expect(result.current.state.queuedChatMessages).toEqual([
          expect.objectContaining({ id: "queued_retry", delivery_state: "submitting" }),
          expect.objectContaining({ id: "queued_second" }),
        ]);
        expect(result.current.state.chatLoading).toBe(true);
      });

      await act(async () => {
        firstStreamController?.enqueue(
          new TextEncoder().encode(
            `event: session_update\ndata: ${JSON.stringify({
              ...queuedPromptSession(firstTerminalMessages),
            })}\n\n`,
          ),
        );
      });

      await waitFor(() => expect(posted).toHaveLength(2));
      await waitFor(() => expect(result.current.state.queuedChatMessages).toEqual([]));
      expect(posted).toEqual([
        { content: "after this", client_request_id: "queued_retry" },
        { content: "after that", client_request_id: "queued_second" },
      ]);
      expect(firstStreamClosed).toBe(true);
    });

    it("polls keyed replay state when a never-closing stream misses the terminal publish", async () => {
      persistQueuedPrompt();
      const runningMessages = [
        { id: "u-replay", role: "user", content: "after this" },
        {
          id: "a-replay",
          role: "assistant",
          content: "Working…",
          status: "running",
        },
      ];
      const terminalMessages = [
        runningMessages[0],
        { ...runningMessages[1], content: "Done.", status: "completed" },
      ];
      let sessionGetCount = 0;
      let messagePostCount = 0;
      let streamClosed = false;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse(queuedPromptSessionList());
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          sessionGetCount += 1;
          return jsonResponse(queuedPromptSession(sessionGetCount === 1 ? [] : terminalMessages));
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") {
          return new Response(
            new ReadableStream<Uint8Array>({
              start(controller) {
                init?.signal?.addEventListener(
                  "abort",
                  () => {
                    streamClosed = true;
                    controller.close();
                  },
                  { once: true },
                );
              },
            }),
            { status: 200, headers: { "Content-Type": "text/event-stream" } },
          );
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
          void init;
          return jsonResponse({
            ...queuedPromptSession(runningMessages),
            data: { ...queuedPromptSession(runningMessages).data, status: "running" },
            message_request: { replay: true, committed_message_id: "u-replay" },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.queuedChatMessages).toEqual([]));
      expect(messagePostCount).toBe(1);
      expect(sessionGetCount).toBeGreaterThanOrEqual(2);
      expect(streamClosed).toBe(true);
      expect(result.current.state.chatError).toBe("");
    });

    it("preserves a queued prompt when transport reconciliation cannot confirm commit", async () => {
      persistQueuedPrompt();
      let messagePostCount = 0;
      let sessionGetCount = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse(queuedPromptSessionList());
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          sessionGetCount += 1;
          return jsonResponse(queuedPromptSession());
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") {
          return emptyStreamResponse();
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
          void init;
          throw new Error("connection dropped before response");
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() =>
        expect(result.current.state.chatError).toBe(
          "The queued message submission could not be confirmed. It remains queued and will not be retried automatically. Check its delivery status before taking another action.",
        ),
      );
      await waitFor(() => expect(result.current.state.chatLoading).toBe(false));

      expect(messagePostCount).toBe(1);
      expect(sessionGetCount).toBeGreaterThanOrEqual(1);
      expect(result.current.state.queuedChatMessages).toEqual([
        expect.objectContaining({
          id: "queued_retry",
          content: "after this",
          delivery_state: "reconcile_required",
          delivery_baseline_message_ids: [],
        }),
      ]);
      expect(
        result.current.state.activeChatSession?.messages?.some((message) =>
          message.id.startsWith("pending-agent-user-"),
        ),
      ).toBe(false);

      await waitFor(() => {
        const stored = readQueuedChatMessagesFromStorage(window.localStorage);
        expect(stored).toEqual([
          expect.objectContaining({
            id: "queued_retry",
            delivery_state: "reconcile_required",
            delivery_baseline_message_ids: [],
          }),
        ]);
      });

      await act(async () => {
        await result.current.actions.reconcileQueuedChatMessage("queued_retry");
      });
      expect(result.current.state.queuedChatMessages).toEqual([
        expect.objectContaining({
          id: "queued_retry",
          delivery_state: "reconcile_required",
          delivery_idempotency_keyed: true,
        }),
      ]);
      expect(messagePostCount).toBe(2);
    });

    it.each([
      {
        outcome: "finds no committed message",
        messages: [] as Array<Record<string, unknown>>,
        expectedDeliveryState: "retryable",
        expectedReconciled: false,
      },
      {
        outcome: "finds the committed message",
        messages: [
          {
            id: "u-committed-elsewhere",
            role: "user",
            content: "after this",
            created_at: "2026-04-20T00:00:03Z",
          },
        ],
        expectedDeliveryState: "",
        expectedReconciled: true,
      },
    ])(
      "keeps $outcome feedback scoped to its source after selecting another chat",
      async ({ messages, expectedDeliveryState, expectedReconciled }) => {
        persistQueuedPrompt();
        const [stored] = JSON.parse(
          window.localStorage.getItem("hecate.queuedChatMessages") ?? "[]",
        ) as Array<Record<string, unknown>>;
        window.localStorage.setItem(
          "hecate.queuedChatMessages",
          JSON.stringify([
            {
              ...stored,
              delivery_state: "reconcile_required",
              delivery_baseline_message_ids: [],
            },
          ]),
        );

        let deferNextSourceGet = false;
        let sourceGetStarted = false;
        let releaseSourceGet: ((response: Response) => void) | undefined;
        const deferredSourceGet = new Promise<Response>((resolve) => {
          releaseSourceGet = resolve;
        });
        fetchMock.mockImplementation(async (input, init) => {
          const url = String(input);
          if (url === "/hecate/v1/chat/sessions") {
            return jsonResponse({
              object: "chat_sessions",
              data: [
                ...queuedPromptSessionList().data,
                {
                  id: "b1",
                  title: "Selected chat",
                  agent_id: "hecate",
                  status: "completed",
                  provider: "openai",
                  model: "gpt-4o-mini",
                  message_count: 0,
                },
              ],
            });
          }
          if (url === "/hecate/v1/chat/sessions/a1") {
            if (deferNextSourceGet) {
              deferNextSourceGet = false;
              sourceGetStarted = true;
              return deferredSourceGet;
            }
            return jsonResponse(queuedPromptSession());
          }
          if (url === "/hecate/v1/chat/sessions/b1") {
            return jsonResponse({
              object: "chat_session",
              data: {
                ...queuedPromptSession().data,
                id: "b1",
                title: "Selected chat",
              },
            });
          }
          return defaultBackendMock()(input, init);
        });

        const { result } = renderRuntimeConsoleHook();
        await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a1"));
        await waitFor(() => expect(result.current.state.chatLoading).toBe(false));

        deferNextSourceGet = true;
        let reconciliation!: Promise<boolean>;
        act(() => {
          reconciliation = result.current.actions.reconcileQueuedChatMessage("queued_retry");
        });
        await waitFor(() => expect(sourceGetStarted).toBe(true));
        await act(async () => {
          expect(await result.current.actions.selectChatSession("b1")).toBe(true);
        });
        expect(result.current.state.activeChatSession?.id).toBe("b1");

        let reconciled = !expectedReconciled;
        await act(async () => {
          releaseSourceGet?.(jsonResponse(queuedPromptSession(messages)));
          reconciled = await reconciliation;
        });

        expect(reconciled).toBe(expectedReconciled);
        expect(result.current.state.activeChatSession?.id).toBe("b1");
        expect(result.current.state.chatError).toBe("");
        expect(result.current.state.notice?.message ?? "").not.toContain(
          "already in the transcript",
        );
        const expectedQueuedState = expectedDeliveryState
          ? [{ id: "queued_retry", delivery_state: expectedDeliveryState }]
          : [];
        expect(
          result.current.state.queuedChatMessages.map((item) => ({
            id: item.id,
            delivery_state: item.delivery_state,
          })),
        ).toEqual(expectedQueuedState);
        await waitFor(() =>
          expect(
            readQueuedChatMessagesFromStorage(window.localStorage).map((item) => ({
              id: item.id,
              delivery_state: item.delivery_state,
            })),
          ).toEqual(expectedQueuedState),
        );
      },
    );

    it("loads submitting work as reconciliation-required, preserves FIFO, and advances after proof", async () => {
      persistQueuedPrompt();
      const [storedFirst] = JSON.parse(
        window.localStorage.getItem("hecate.queuedChatMessages") ?? "[]",
      ) as Array<Record<string, unknown>>;
      const storedSubmitting = {
        ...storedFirst,
        delivery_state: "submitting",
        delivery_baseline_message_ids: [],
      };
      const storedSecond: Record<string, unknown> = {
        ...storedFirst,
        id: "queued_second",
        content: "after that",
      };
      delete storedSecond.delivery_state;
      delete storedSecond.delivery_baseline_message_ids;
      window.localStorage.setItem(
        "hecate.queuedChatMessages",
        JSON.stringify([storedSubmitting, storedSecond]),
      );

      const committedFirst = {
        id: "u1",
        role: "user",
        content: "after this",
        created_at: "2026-04-20T00:00:02Z",
      };
      let messagePostCount = 0;
      const postedContents: string[] = [];
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse(queuedPromptSessionList());
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse(queuedPromptSession([committedFirst]));
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") {
          return emptyStreamResponse();
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
          const payload = JSON.parse(String(init?.body ?? "{}"));
          postedContents.push(payload.content);
          return jsonResponse({
            ...queuedPromptSession([
              committedFirst,
              {
                id: "u2",
                role: "user",
                content: payload.content,
                created_at: "2026-04-20T00:00:03Z",
              },
              {
                id: "a2",
                role: "assistant",
                content: "Done.",
                status: "completed",
                created_at: "2026-04-20T00:00:04Z",
              },
            ]),
            message_request: { replay: false, committed_message_id: "u2" },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("a1"));
      expect(result.current.state.queuedChatMessages).toEqual([
        expect.objectContaining({
          id: "queued_retry",
          delivery_state: "reconcile_required",
        }),
        expect.objectContaining({ id: "queued_second" }),
      ]);
      expect(messagePostCount).toBe(0);

      await act(async () => {
        await result.current.actions.reconcileQueuedChatMessage("queued_retry");
      });

      await waitFor(() => expect(messagePostCount).toBe(1));
      await waitFor(() => expect(result.current.state.queuedChatMessages).toEqual([]));
      expect(postedContents).toEqual(["after that"]);
    });

    it("retains legacy ambiguous work when a safe delivery baseline is unavailable", async () => {
      persistQueuedPrompt();
      const stored = JSON.parse(
        window.localStorage.getItem("hecate.queuedChatMessages") ?? "[]",
      ) as Array<Record<string, unknown>>;
      stored[0].retry_required = true;
      window.localStorage.setItem("hecate.queuedChatMessages", JSON.stringify(stored));
      let messagePostCount = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse(queuedPromptSessionList());
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          return jsonResponse(queuedPromptSession());
        }
        if (url === "/hecate/v1/chat/sessions/a1/stream") {
          return emptyStreamResponse();
        }
        if (url === "/hecate/v1/chat/sessions/a1/messages") {
          messagePostCount += 1;
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      expect(result.current.state.queuedChatMessages).toEqual([
        expect.objectContaining({
          id: "queued_retry",
          delivery_state: "reconcile_required",
        }),
      ]);

      await act(async () => {
        await result.current.actions.reconcileQueuedChatMessage("queued_retry");
      });

      expect(result.current.state.queuedChatMessages).toEqual([
        expect.objectContaining({
          id: "queued_retry",
          delivery_state: "reconcile_required",
        }),
      ]);
      expect(result.current.state.chatError).toContain("predates safe delivery reconciliation");
      expect(messagePostCount).toBe(0);
    });

    it("submits into the selected external session after a transient hydrate failure", async () => {
      window.localStorage.setItem("hecate.chatTarget", "external_agent");
      window.localStorage.setItem("hecate.agentAdapterID", "codex");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.agentWorkspace", "/workspace");

      let createPostCount = 0;
      let messagePostCount = 0;
      let sessionFetchCount = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          if (init?.method === "POST") {
            createPostCount += 1;
            return jsonResponse({ object: "chat_session", data: {} });
          }
          return jsonResponse({
            object: "chat_sessions",
            data: [
              {
                id: "a1",
                title: "Codex chat",
                agent_id: "codex",
                status: "completed",
                workspace: "/workspace",
                message_count: 0,
                created_at: "2026-04-20T00:00:00Z",
                updated_at: "2026-04-20T00:00:00Z",
              },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions/a1") {
          sessionFetchCount += 1;
          if (sessionFetchCount === 1) {
            return jsonResponse({ error: { message: "temporary load failure" } }, 500);
          }
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Codex chat",
              agent_id: "codex",
              status: "completed",
              workspace: "/workspace",
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
          messagePostCount += 1;
          void init;
          return jsonResponse({
            object: "chat_session",
            data: {
              id: "a1",
              title: "Codex chat",
              agent_id: "codex",
              status: "completed",
              workspace: "/workspace",
              messages: [
                {
                  id: "u1",
                  agent_id: "codex",
                  role: "user",
                  content: "continue here",
                  created_at: "2026-04-20T00:00:01Z",
                },
              ],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:01Z",
            },
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      expect(result.current.state.activeChatSessionID).toBe("a1");
      expect(result.current.state.activeChatSession).toBeNull();

      act(() => {
        result.current.actions.setMessage("continue here");
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });

      expect(createPostCount).toBe(0);
      expect(messagePostCount).toBe(1);
      expect(result.current.state.activeChatSessionID).toBe("a1");
      expect(result.current.state.activeChatSession?.agent_id).toBe("codex");
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
        const stored = readQueuedChatMessagesFromStorage(window.localStorage);
        expect(stored[0].content).toBe("edited after refresh");
      });
    });

    it("blocks a failed queued edit and prevents stale content from draining after reload", async () => {
      window.localStorage.setItem("hecate.chatTarget", "agent");
      window.localStorage.setItem("hecate.chatSessionID", "a1");
      window.localStorage.setItem("hecate.agentWorkspace", "/workspace");
      window.localStorage.setItem("hecate.queuedChatMessages.v2", "1");
      const readyRevision = "revision-ready";
      const readyKey = queuedChatMessageStorageKey("queued_restore", "0", readyRevision);
      window.localStorage.setItem(
        readyKey,
        JSON.stringify({
          id: "queued_restore",
          session_id: "a1",
          content: "keep this after refresh",
          delivery_storage_epoch: "0",
          delivery_storage_revision: readyRevision,
          execution_mode: "hecate_task",
          tools_enabled: false,
          provider_filter: "auto",
          model: "ministral-3:latest",
          workspace: "/workspace",
          system_prompt: "",
          agent_id: "hecate",
          created_at: "2026-04-20T00:00:01Z",
        }),
      );
      let messagePostCount = 0;
      const originalSetItem = window.localStorage.setItem.bind(window.localStorage);
      const storageSpy = vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
        if (isQueuedItemStorageKey(key, "queued_restore") && key !== readyKey) {
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
          "/hecate/v1/chat/sessions/a1/messages": () => {
            messagePostCount += 1;
            return jsonResponse({ object: "chat_session", data: {} });
          },
        }),
      );

      try {
        const first = renderRuntimeConsoleHook();
        const { result } = first;
        await waitFor(() => expect(result.current.state.loading).toBe(false));

        act(() => {
          result.current.actions.updateQueuedChatMessage("queued_restore", "edited in memory");
        });

        await waitFor(() =>
          expect(
            storageSpy.mock.calls.some(
              ([key]) =>
                typeof key === "string" &&
                isQueuedItemStorageKey(key, "queued_restore") &&
                key !== readyKey,
            ),
          ).toBe(true),
        );
        expect(result.current.state.queuedChatMessages[0]).toEqual(
          expect.objectContaining({
            content: "edited in memory",
            delivery_state: "retryable",
            delivery_storage_failed: true,
          }),
        );
        expect(window.localStorage.getItem(readyKey)).toBeNull();

        first.unmount();
        storageSpy.mockRestore();
        const reloaded = renderRuntimeConsoleHook();
        await waitFor(() => expect(reloaded.result.current.state.loading).toBe(false));
        expect(reloaded.result.current.state.queuedChatMessages).toEqual([]);
        expect(messagePostCount).toBe(0);
        reloaded.unmount();
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

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.loading).toBe(false));

      await waitFor(() =>
        expect(
          result.current.runtimeConsole.state.queuedChatMessages.map((item) => item.id),
        ).toEqual(["queued_keep"]),
      );
      await waitFor(() => {
        const stored = readQueuedChatMessagesFromStorage(window.localStorage);
        expect(stored.map((item: { id: string }) => item.id)).toEqual(["queued_keep"]);
      });
      expect(window.localStorage.getItem(queuedChatDeletedSessionStorageKey("missing"))).toMatch(
        /^deleted:v1:/,
      );

      let admission = "admitted" as ReturnType<
        typeof result.current.chat.actions.enqueueQueuedChatMessage
      >;
      act(() => {
        admission = result.current.chat.actions.enqueueQueuedChatMessage({
          id: "queued_cross_tab_replacement",
          session_id: "missing",
          content: "different prompt from another tab",
          execution_mode: "hecate_task",
          tools_enabled: false,
          provider_filter: "auto",
          model: "ministral-3:latest",
          workspace: "/workspace",
          system_prompt: "",
          agent_id: "hecate",
          created_at: "2026-04-20T00:00:03Z",
        });
      });
      expect(admission).toBe("session_deleted");
      expect(result.current.runtimeConsole.state.queuedChatMessages.map((item) => item.id)).toEqual(
        ["queued_keep"],
      );
    });

    it("fences an authoritative dashboard removal before any queued row exists", async () => {
      let includeRemovedSession = true;
      fetchMock.mockImplementation(async (input, init) => {
        if (String(input) === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: includeRemovedSession
              ? [{ id: "dashboard_removed", title: "Removed elsewhere", message_count: 0 }]
              : [],
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() =>
        expect(result.current.state.chatSessions.map((session) => session.id)).toContain(
          "dashboard_removed",
        ),
      );
      includeRemovedSession = false;
      await act(async () => {
        await result.current.actions.loadDashboard();
      });

      expect(result.current.chat.actions.isChatSessionDeleted("dashboard_removed")).toBe(true);
      expect(
        window.localStorage.getItem(queuedChatDeletedSessionStorageKey("dashboard_removed")),
      ).toMatch(/^deleted:v1:/);
      let admission = "admitted" as ReturnType<
        typeof result.current.chat.actions.enqueueQueuedChatMessage
      >;
      act(() => {
        admission = result.current.chat.actions.enqueueQueuedChatMessage({
          id: "stale_enqueue_after_dashboard",
          session_id: "dashboard_removed",
          content: "must remain fenced",
          execution_mode: "hecate_task",
          tools_enabled: false,
          provider_filter: "auto",
          model: "gpt-4o-mini",
          workspace: "",
          system_prompt: "",
          agent_id: "hecate",
          created_at: "2026-07-14T12:00:00Z",
        });
      });
      expect(admission).toBe("session_deleted");
      expect(result.current.state.queuedChatMessages).toEqual([]);
    });

    it("releases a selected valid chat before a removed chat's late compact payload settles", async () => {
      window.localStorage.setItem("hecate.chatSessionID", "dashboard_late");
      let includeRemovedSession = true;
      let compactStarted = false;
      let releaseCompact: ((response: Response) => void) | undefined;
      const deferredCompact = new Promise<Response>((resolve) => {
        releaseCompact = resolve;
      });
      const session = (id = "dashboard_late", title = "Late payload") => ({
        object: "chat_session",
        data: {
          id,
          title,
          agent_id: "hecate",
          status: "completed",
          messages: [],
          context_summary: { message_count: 2, summary: "stale" },
          created_at: "2026-07-14T12:00:00Z",
          updated_at: "2026-07-14T12:00:00Z",
        },
      });
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: includeRemovedSession
              ? [
                  { id: "dashboard_late", title: "Late payload", message_count: 0 },
                  { id: "dashboard_valid", title: "Valid payload", message_count: 0 },
                ]
              : [{ id: "dashboard_valid", title: "Valid payload", message_count: 0 }],
          });
        }
        if (url === "/hecate/v1/chat/sessions/dashboard_late/compact") {
          compactStarted = true;
          return deferredCompact;
        }
        if (url === "/hecate/v1/chat/sessions/dashboard_late") return jsonResponse(session());
        if (url === "/hecate/v1/chat/sessions/dashboard_valid") {
          return jsonResponse(session("dashboard_valid", "Valid payload"));
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() =>
        expect(result.current.state.activeChatSession?.id).toBe("dashboard_late"),
      );
      let compact!: Promise<boolean>;
      act(() => {
        compact = result.current.actions.compactChatSession("dashboard_late");
      });
      await waitFor(() => expect(compactStarted).toBe(true));
      await act(async () => {
        expect(await result.current.actions.selectChatSession("dashboard_valid")).toBe(true);
      });
      expect(result.current.state.activeChatSession?.id).toBe("dashboard_valid");
      expect(result.current.state.chatLoading).toBe(true);
      includeRemovedSession = false;
      await act(async () => {
        await result.current.actions.loadDashboard();
      });
      expect(result.current.state.activeChatSession?.id).toBe("dashboard_valid");
      expect(result.current.state.chatLoading).toBe(false);

      let compacted = true;
      await act(async () => {
        releaseCompact?.(jsonResponse(session()));
        compacted = await compact;
      });
      expect(compacted).toBe(false);
      expect(result.current.state.activeChatSessionID).toBe("dashboard_valid");
      expect(result.current.state.chatSessions.map((entry) => entry.id)).toEqual([
        "dashboard_valid",
      ]);
      expect(result.current.chat.actions.isChatSessionDeleted("dashboard_late")).toBe(true);
    });

    it("keeps a valid compact current when the dashboard removes an unrelated chat", async () => {
      window.localStorage.setItem("hecate.chatSessionID", "compact_valid");
      let includeUnrelatedSession = true;
      let compactStarted = false;
      let releaseCompact: ((response: Response) => void) | undefined;
      const deferredCompact = new Promise<Response>((resolve) => {
        releaseCompact = resolve;
      });
      const session = (summary: string, messageCount: number) => ({
        object: "chat_session",
        data: {
          id: "compact_valid",
          title: "Valid compact",
          agent_id: "hecate",
          status: "completed",
          messages: [],
          context_summary: { message_count: messageCount, summary },
          created_at: "2026-07-14T12:00:00Z",
          updated_at: "2026-07-14T12:00:00Z",
        },
      });
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: [
              ...(includeUnrelatedSession
                ? [{ id: "compact_removed", title: "Removed", message_count: 0 }]
                : []),
              { id: "compact_valid", title: "Valid compact", message_count: 0 },
            ],
          });
        }
        if (url === "/hecate/v1/chat/sessions/compact_valid") {
          return jsonResponse(session("before compact", 1));
        }
        if (url === "/hecate/v1/chat/sessions/compact_valid/compact") {
          compactStarted = true;
          return deferredCompact;
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("compact_valid"));
      let compact!: Promise<boolean>;
      act(() => {
        compact = result.current.actions.compactChatSession("compact_valid");
      });
      await waitFor(() => expect(compactStarted).toBe(true));

      includeUnrelatedSession = false;
      await act(async () => {
        await result.current.actions.loadDashboard();
      });
      expect(result.current.chat.actions.isChatSessionDeleted("compact_removed")).toBe(true);
      expect(result.current.state.activeChatSession?.id).toBe("compact_valid");
      expect(result.current.state.chatLoading).toBe(true);

      let compacted = false;
      await act(async () => {
        releaseCompact?.(jsonResponse(session("fresh compact", 3)));
        compacted = await compact;
      });
      expect(compacted).toBe(true);
      expect(result.current.state.chatLoading).toBe(false);
      expect(result.current.state.activeChatSession?.context_summary).toEqual({
        message_count: 3,
        summary: "fresh compact",
      });
      expect(result.current.state.notice).toEqual({
        kind: "success",
        message: "Compacted 3 transcript messages.",
      });
      expect(result.current.state.chatSessions.map((entry) => entry.id)).toEqual(["compact_valid"]);
    });

    it("surfaces authoritative dashboard queue cleanup failure", async () => {
      let includeRemovedSession = true;
      fetchMock.mockImplementation(async (input, init) => {
        if (String(input) === "/hecate/v1/chat/sessions") {
          return jsonResponse({
            object: "chat_sessions",
            data: includeRemovedSession
              ? [{ id: "dashboard_cleanup", title: "Cleanup", message_count: 0 }]
              : [],
          });
        }
        return defaultBackendMock()(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() =>
        expect(result.current.state.chatSessions.map((session) => session.id)).toContain(
          "dashboard_cleanup",
        ),
      );
      let admission = "storage_failed" as ReturnType<
        typeof result.current.chat.actions.enqueueQueuedChatMessage
      >;
      act(() => {
        admission = result.current.chat.actions.enqueueQueuedChatMessage({
          id: "dashboard_cleanup_queue",
          session_id: "dashboard_cleanup",
          content: "cleanup me",
          execution_mode: "hecate_task",
          tools_enabled: false,
          provider_filter: "auto",
          model: "gpt-4o-mini",
          workspace: "",
          system_prompt: "",
          agent_id: "hecate",
          created_at: "2026-07-14T12:00:00Z",
        });
      });
      expect(admission).toBe("admitted");
      const queued = result.current.state.queuedChatMessages[0];
      const queuedKey = queuedChatMessageStorageKey(
        queued.id,
        queued.delivery_storage_epoch ?? "0",
        queued.delivery_storage_revision,
      );
      const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
      const removeSpy = vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
        if (key === queuedKey) return;
        originalRemoveItem(key);
      });
      try {
        includeRemovedSession = false;
        await act(async () => {
          await result.current.actions.loadDashboard();
        });
        expect(result.current.state.error).toBe(
          "The dashboard removed a deleted chat, but browser queue cleanup needs attention before queued work can continue.",
        );
        expect(result.current.chat.actions.isChatSessionDeleted("dashboard_cleanup")).toBe(true);
        expect(result.current.state.queuedChatMessages[0]).toMatchObject({
          delivery_state: "reconcile_required",
          delivery_storage_failed: true,
        });
      } finally {
        removeSpy.mockRestore();
      }
    });

    it.each(["failure", "bad_metadata"] as const)(
      "keeps queued A %s state without projecting its error into selected B",
      async (outcome) => {
        persistQueuedPrompt("queued from A");
        let messageStarted = false;
        let releaseMessage: ((response: Response) => void) | undefined;
        const deferredMessage = new Promise<Response>((resolve) => {
          releaseMessage = resolve;
        });
        const session = (id: string) => ({
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
            created_at: "2026-07-14T12:00:00Z",
            updated_at: "2026-07-14T12:00:00Z",
          },
        });
        fetchMock.mockImplementation(async (input, init) => {
          const url = String(input);
          if (url === "/hecate/v1/chat/sessions") {
            return jsonResponse({
              object: "chat_sessions",
              data: [
                { id: "a1", title: "A", status: "completed", message_count: 0 },
                { id: "b1", title: "B", status: "completed", message_count: 0 },
              ],
            });
          }
          if (url === "/hecate/v1/chat/sessions/a1") return jsonResponse(session("a1"));
          if (url === "/hecate/v1/chat/sessions/b1") return jsonResponse(session("b1"));
          if (url === "/hecate/v1/chat/sessions/a1/stream") return emptyStreamResponse();
          if (url === "/hecate/v1/chat/sessions/a1/messages" && init?.method === "POST") {
            messageStarted = true;
            return deferredMessage;
          }
          return defaultBackendMock()(input, init);
        });

        const { result } = renderRuntimeConsoleHook();
        await waitFor(() => expect(messageStarted).toBe(true));
        await act(async () => {
          await result.current.actions.selectChatSession("b1");
        });
        expect(result.current.state.activeChatSessionID).toBe("b1");

        await act(async () => {
          releaseMessage?.(
            outcome === "failure"
              ? jsonResponse(
                  { error: { type: "chat.queued_rejected", message: "queued rejected" } },
                  422,
                )
              : jsonResponse({
                  ...session("a1"),
                  data: {
                    ...session("a1").data,
                    messages: [
                      {
                        id: "queued-user",
                        role: "user",
                        content: "queued from A",
                        created_at: "2026-07-14T12:00:01Z",
                      },
                    ],
                  },
                }),
          );
        });

        await waitFor(() => expect(result.current.state.chatLoading).toBe(false));
        expect(result.current.state.activeChatSessionID).toBe("b1");
        expect(result.current.state.chatError).toBe("");
        expect(result.current.state.queuedChatMessages).toHaveLength(1);
        expect(result.current.state.queuedChatMessages[0]).toMatchObject({
          session_id: "a1",
          delivery_state: outcome === "failure" ? "retryable" : "reconcile_required",
        });
        await waitFor(() =>
          expect(
            readQueuedChatMessagesFromStorage(window.localStorage, {
              preserveSubmitting: true,
            })[0],
          ).toMatchObject({
            session_id: "a1",
            delivery_state: outcome === "failure" ? "retryable" : "reconcile_required",
          }),
        );
      },
    );

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
      let selectPromise!: Promise<boolean>;
      act(() => {
        selectPromise = result.current.actions.selectChatSession("a2");
      });

      await waitFor(() => expect(result.current.state.activeChatSessionID).toBe("a2"));
      expect(result.current.state.activeChatSession).toBeNull();
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
          const accepted = session("a2", "completed");
          return jsonResponse({
            ...accepted,
            data: {
              ...accepted.data,
              messages: [
                {
                  id: "u-a2",
                  role: "user",
                  content: "drain only after a2 loads",
                  created_at: "2026-04-20T00:00:01Z",
                },
                {
                  id: "a-a2",
                  role: "assistant",
                  content: "Done.",
                  status: "completed",
                  created_at: "2026-04-20T00:00:02Z",
                },
              ],
            },
            message_request: { replay: false, committed_message_id: "u-a2" },
          });
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

      let selectA2Promise!: Promise<boolean>;
      act(() => {
        selectA2Promise = result.current.actions.selectChatSession("a2");
      });

      await waitFor(() => expect(result.current.state.activeChatSessionID).toBe("a2"));
      expect(result.current.state.activeChatSession).toBeNull();
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
      expect(result.current.state.chatToolsEnabledBySessionID.get("chat_a")).toBeUndefined();

      act(() => {
        result.current.actions.setChatToolsEnabled(false);
      });
      expect(result.current.state.chatToolsEnabledBySessionID.get("chat_a")).toBe(false);

      await act(async () => {
        await result.current.actions.selectChatSession("chat_b");
      });
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("chat_b"));
      // chat_b has no per-session override yet, so it falls back to the
      // user default — the per-session pin on chat_a doesn't bleed.
      expect(result.current.state.chatToolsEnabledBySessionID.get("chat_b")).toBeUndefined();

      await act(async () => {
        await result.current.actions.selectChatSession("chat_a");
      });
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("chat_a"));
      // chat_a remembers its tools-off pin across the switch.
      expect(result.current.state.chatToolsEnabledBySessionID.get("chat_a")).toBe(false);
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
                    turn_kind: "direct_model",
                    execution_mode: "hecate_task",
                    tools_enabled: false,
                    provider: "ollama",
                    model: "smollm2:135m",
                    status: "completed",
                    message_count: 2,
                  },
                  {
                    id: "task:task_tools",
                    turn_kind: "hecate_task",
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

      let deleted = false;
      await act(async () => {
        deleted = await result.current.actions.deleteChatSession("sess_b");
      });

      expect(deleted).toBe(true);
      expect(deleteCalls).toBe(1);
      await waitFor(() =>
        expect(result.current.state.chatSessions.map((s) => s.id)).toEqual(["sess_a"]),
      );
      expect(result.current.state.notice?.kind).toBe("success");
      expect(result.current.state.notice?.message).toBe("Agent chat deleted.");
    });

    it("blocks Delete for the session owned by Stop but permits another chat", async () => {
      const deleteCalls: string[] = [];
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (init?.method === "DELETE") {
          deleteCalls.push(url);
          return new Response(null, { status: 204 });
        }
        return withSessions([
          { id: "sess_a", title: "Delete another" },
          { id: "sess_b", title: "Stopping" },
        ])(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.runtimeConsole.state.chatSessions).toHaveLength(2));
      let owner: ChatCancellationOwner | null = null;
      act(() => {
        owner = result.current.chat.actions.beginChatCancellation("sess_b");
      });
      if (!owner) throw new Error("expected cancellation ownership");

      let deleted = true;
      await act(async () => {
        deleted = await result.current.runtimeConsole.actions.deleteChatSession("sess_b");
      });
      expect(deleted).toBe(false);
      expect(deleteCalls).toEqual([]);
      expect(result.current.runtimeConsole.state.notice).toEqual({
        kind: "error",
        message: "Wait for Stop to finish before deleting this chat.",
      });

      await act(async () => {
        deleted = await result.current.runtimeConsole.actions.deleteChatSession("sess_a");
      });
      expect(deleted).toBe(true);
      expect(deleteCalls).toEqual(["/hecate/v1/chat/sessions/sess_a"]);
      expect(result.current.chat.state.chatCancellingSessionID).toBe("sess_b");

      act(() => {
        result.current.chat.actions.finishChatCancellation(owner!);
      });
    });

    it("keeps chat state after a failed delete and releases ownership for retry", async () => {
      let deleteCalls = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions/sess_b" && init?.method === "DELETE") {
          deleteCalls += 1;
          if (deleteCalls === 1) {
            return jsonResponse({ error: { message: "delete unavailable" } }, 503);
          }
          return new Response(null, { status: 204 });
        }
        return withSessions([
          { id: "sess_a", title: "Keep" },
          { id: "sess_b", title: "Delete me" },
        ])(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.chatSessions).toHaveLength(2));

      let deleted = true;
      await act(async () => {
        deleted = await result.current.actions.deleteChatSession("sess_b");
      });
      expect(deleted).toBe(false);
      expect(result.current.state.chatSessions.map((session) => session.id)).toEqual([
        "sess_a",
        "sess_b",
      ]);
      expect(result.current.state.notice).toEqual({
        kind: "error",
        message: "delete unavailable",
      });

      await act(async () => {
        deleted = await result.current.actions.deleteChatSession("sess_b");
      });
      expect(deleted).toBe(true);
      expect(deleteCalls).toBe(2);
      expect(result.current.state.chatSessions.map((session) => session.id)).toEqual(["sess_a"]);
      expect(result.current.state.notice).toEqual({
        kind: "success",
        message: "Agent chat deleted.",
      });
    });

    it("does not reintroduce a deleted non-active chat from stale dashboard hydration", async () => {
      let delaySessionList = false;
      let delayedListRequested = false;
      let releaseSessionList: (() => void) | undefined;
      const sessionListGate = new Promise<void>((resolve) => {
        releaseSessionList = resolve;
      });
      const backend = withSessions([
        { id: "sess_a", title: "Keep" },
        { id: "sess_b", title: "Delete me" },
      ]);
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions/sess_b" && init?.method === "DELETE") {
          return new Response(null, { status: 204 });
        }
        if (url === "/hecate/v1/chat/sessions" && delaySessionList) {
          delayedListRequested = true;
          await sessionListGate;
        }
        return backend(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.chatSessions).toHaveLength(2));
      delaySessionList = true;
      let dashboardLoad!: Promise<void>;
      act(() => {
        dashboardLoad = result.current.actions.loadDashboard();
      });
      await waitFor(() => expect(delayedListRequested).toBe(true));

      await act(async () => {
        await result.current.actions.deleteChatSession("sess_b");
      });
      expect(result.current.state.chatSessions.map((session) => session.id)).toEqual(["sess_a"]);

      await act(async () => {
        releaseSessionList?.();
        await dashboardLoad;
      });
      expect(result.current.state.chatSessions.map((session) => session.id)).toEqual(["sess_a"]);
    });

    it("does not reactivate a deleted chat when an earlier selection finishes late", async () => {
      let releaseSelection: ((response: Response) => void) | undefined;
      const deferredSelection = new Promise<Response>((resolve) => {
        releaseSelection = resolve;
      });
      let selectionStarted = false;
      const backend = withSessions(
        [
          { id: "sess_a", title: "Keep" },
          { id: "sess_b", title: "Delete me" },
        ],
        {
          "/hecate/v1/chat/sessions/sess_b": async () => {
            selectionStarted = true;
            return deferredSelection;
          },
        },
      );
      fetchMock.mockImplementation(async (input, init) => {
        if (String(input) === "/hecate/v1/chat/sessions/sess_b" && init?.method === "DELETE") {
          return new Response(null, { status: 204 });
        }
        return backend(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.chatSessions).toHaveLength(2));

      let selection!: Promise<boolean>;
      act(() => {
        selection = result.current.actions.selectChatSession("sess_b");
      });
      await waitFor(() => expect(selectionStarted).toBe(true));

      await act(async () => {
        await result.current.actions.deleteChatSession("sess_b");
      });
      expect(result.current.state.chatSessions.map((session) => session.id)).toEqual(["sess_a"]);

      let selected = true;
      await act(async () => {
        releaseSelection?.(
          jsonResponse({
            object: "chat_session",
            data: {
              id: "sess_b",
              title: "Delete me",
              agent_id: "hecate",
              status: "completed",
              messages: [],
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:00Z",
            },
          }),
        );
        selected = await selection;
      });

      expect(selected).toBe(false);
      expect(result.current.state.activeChatSessionID).toBe("");
      expect(result.current.state.activeChatSession).toBeNull();
      expect(result.current.state.chatSessions.map((session) => session.id)).toEqual(["sess_a"]);
    });

    it("does not delete the active chat while it owns image drafts", async () => {
      let deleteCalls = 0;
      const activeSession = {
        id: "sess_b",
        title: "Keep draft",
        agent_id: "hecate",
        status: "completed",
        provider: "openai",
        model: "gpt-4o-mini",
        messages: [],
        created_at: "2026-04-20T00:00:00Z",
        updated_at: "2026-04-20T00:00:00Z",
      };
      const backend = withSessions([{ id: "sess_b", title: "Keep draft" }], {
        "/hecate/v1/chat/sessions/sess_b": () =>
          jsonResponse({ object: "chat_session", data: activeSession }),
      });
      fetchMock.mockImplementation(async (input, init) => {
        if (String(input) === "/hecate/v1/chat/sessions/sess_b" && init?.method === "DELETE") {
          deleteCalls += 1;
          return new Response(null, { status: 204 });
        }
        return backend(input, init);
      });

      const { result } = renderRuntimeConsoleHook();
      await waitFor(() => expect(result.current.state.loading).toBe(false));
      await act(async () => {
        await result.current.actions.selectChatSession("sess_b");
      });
      const file = new File(["image"], "map.png", { type: "image/png" });
      act(() => {
        result.current.actions.setMessage("inspect this map");
        result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
      });

      let deleted = true;
      await act(async () => {
        deleted = await result.current.actions.deleteChatSession("sess_b");
      });

      expect(deleted).toBe(false);
      expect(deleteCalls).toBe(0);
      expect(result.current.state.activeChatSessionID).toBe("sess_b");
      expect(result.current.state.activeChatSession?.id).toBe("sess_b");
      expect(result.current.state.message).toBe("inspect this map");
      expect(result.current.state.pendingChatAttachments).toEqual([{ id: "draft-1", file }]);
      expect(result.current.state.notice).toEqual({
        kind: "error",
        message: "Remove attached files before deleting a chat.",
      });
    });

    it("reserves chat ownership across a deferred single-chat DELETE", async () => {
      window.localStorage.setItem("hecate.chatSessionID", "sess_b");
      let releaseDelete: ((response: Response) => void) | undefined;
      const deferredDelete = new Promise<Response>((resolve) => {
        releaseDelete = resolve;
      });
      let deleteStarted = false;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions/sess_b" && init?.method === "DELETE") {
          deleteStarted = true;
          return deferredDelete;
        }
        return withSessions([{ id: "sess_b", title: "Delete me" }], {
          "/hecate/v1/chat/sessions/sess_b": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "sess_b",
                title: "Delete me",
                agent_id: "hecate",
                status: "completed",
                messages: [],
                created_at: "2026-04-20T00:00:00Z",
                updated_at: "2026-04-20T00:00:00Z",
              },
            }),
        })(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() =>
        expect(result.current.runtimeConsole.state.activeChatSession?.id).toBe("sess_b"),
      );
      let deletion!: Promise<boolean>;
      act(() => {
        deletion = result.current.runtimeConsole.actions.deleteChatSession("sess_b");
      });
      await waitFor(() => expect(deleteStarted).toBe(true));
      expect(result.current.chat.state.chatOwnershipMutationInFlight).toBe(true);

      const lateFile = new File(["image"], "late.png", { type: "image/png" });
      let lateTurnToken: number | null = -1;
      act(() => {
        result.current.chat.actions.setPendingChatAttachments([
          { id: "late-delete-draft", file: lateFile },
        ]);
        lateTurnToken = result.current.chat.actions.beginChatAttachmentTurn("sess_b", 1);
      });
      expect(result.current.chat.state.pendingChatAttachments).toEqual([]);
      expect(lateTurnToken).toBeNull();

      await act(async () => {
        releaseDelete?.(new Response(null, { status: 204 }));
        await expect(deletion).resolves.toBe(true);
      });
      expect(result.current.chat.state.chatOwnershipMutationInFlight).toBe(false);
      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("");
      expect(result.current.chat.state.pendingChatAttachments).toEqual([]);
    });

    it("releases single-chat ownership when DELETE fails", async () => {
      window.localStorage.setItem("hecate.chatSessionID", "sess_b");
      let deleteCalls = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions/sess_b" && init?.method === "DELETE") {
          deleteCalls += 1;
          return jsonResponse({ error: { message: "delete failed" } }, 503);
        }
        return withSessions([{ id: "sess_b", title: "Keep" }], {
          "/hecate/v1/chat/sessions/sess_b": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "sess_b",
                title: "Keep",
                agent_id: "hecate",
                status: "completed",
                messages: [],
                created_at: "2026-04-20T00:00:00Z",
                updated_at: "2026-04-20T00:00:00Z",
              },
            }),
        })(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() =>
        expect(result.current.runtimeConsole.state.activeChatSession?.id).toBe("sess_b"),
      );

      await act(async () => {
        await result.current.runtimeConsole.actions.deleteChatSession("sess_b");
      });

      expect(deleteCalls).toBe(1);
      expect(result.current.chat.state.chatOwnershipMutationInFlight).toBe(false);
      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("sess_b");
      expect(result.current.runtimeConsole.state.notice).toEqual({
        kind: "error",
        message: "delete failed",
      });

      const file = new File(["image"], "after-failure.png", { type: "image/png" });
      act(() => {
        result.current.chat.actions.setPendingChatAttachments([
          { id: "after-delete-failure", file },
        ]);
      });
      expect(result.current.chat.state.pendingChatAttachments).toEqual([
        { id: "after-delete-failure", file },
      ]);
    });

    it("does not start single-chat DELETE after an image turn acquires ownership", async () => {
      window.localStorage.setItem("hecate.chatSessionID", "sess_b");
      let deleteCalls = 0;
      fetchMock.mockImplementation(async (input, init) => {
        const url = String(input);
        if (url === "/hecate/v1/chat/sessions/sess_b" && init?.method === "DELETE") {
          deleteCalls += 1;
          return new Response(null, { status: 204 });
        }
        return withSessions([{ id: "sess_b", title: "Keep" }], {
          "/hecate/v1/chat/sessions/sess_b": () =>
            jsonResponse({
              object: "chat_session",
              data: {
                id: "sess_b",
                title: "Keep",
                agent_id: "hecate",
                status: "completed",
                messages: [],
                created_at: "2026-04-20T00:00:00Z",
                updated_at: "2026-04-20T00:00:00Z",
              },
            }),
        })(input, init);
      });

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() =>
        expect(result.current.runtimeConsole.state.activeChatSession?.id).toBe("sess_b"),
      );
      let turnToken: number | null = null;
      act(() => {
        turnToken = result.current.chat.actions.beginChatAttachmentTurn("sess_b", 1);
      });
      expect(turnToken).not.toBeNull();

      await act(async () => {
        await result.current.runtimeConsole.actions.deleteChatSession("sess_b");
      });
      expect(deleteCalls).toBe(0);
      expect(result.current.runtimeConsole.state.activeChatSessionID).toBe("sess_b");
      expect(result.current.runtimeConsole.state.notice).toEqual({
        kind: "error",
        message: "Wait for the attachment response before deleting a chat.",
      });
      act(() => {
        if (turnToken !== null) result.current.chat.actions.finishChatAttachmentTurn(turnToken);
      });
    });

    it("keeps durable queued prompts recoverable when delete cleanup fails, then retries", async () => {
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

      const { result } = renderRuntimeConsoleWithChatHook();
      await waitFor(() => expect(result.current.state.activeChatSession?.id).toBe("sess_b"));

      act(() => {
        result.current.actions.setMessage("do this later");
      });
      await act(async () => {
        await result.current.actions.submitChat({ preventDefault: vi.fn() } as any);
      });
      expect(result.current.state.queuedChatMessages).toHaveLength(1);
      const queued = result.current.state.queuedChatMessages[0];
      const storageRevision = queued.delivery_storage_revision;
      expect(storageRevision).toBeTruthy();
      const queuedKey = queuedChatMessageStorageKey(
        queued.id,
        queued.delivery_storage_epoch ?? "0",
        storageRevision,
      );
      expect(window.localStorage.getItem(queuedKey)).not.toBeNull();
      const otherTabRevision = "other-tab-delete-revision";
      const otherTabKey = queuedChatMessageStorageKey(
        "queued_other_tab_delete",
        queued.delivery_storage_epoch ?? "0",
        otherTabRevision,
      );
      window.localStorage.setItem(
        otherTabKey,
        JSON.stringify({
          ...queued,
          id: "queued_other_tab_delete",
          content: "queued by another tab",
          created_at: "2026-04-20T00:00:02Z",
          delivery_storage_revision: otherTabRevision,
        }),
      );
      expect(result.current.state.queuedChatMessages).toHaveLength(1);
      expect(readQueuedChatMessagesFromStorage(window.localStorage)).toHaveLength(2);

      const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
      const removeSpy = vi
        .spyOn(Storage.prototype, "removeItem")
        .mockImplementation((key: string) => {
          if (key === queuedKey) return;
          originalRemoveItem(key);
        });

      let deleted = true;
      await act(async () => {
        deleted = await result.current.actions.deleteChatSession("sess_b");
      });

      expect(deleteCalls).toBe(1);
      expect(deleted).toBe(false);
      expect(result.current.chat.actions.isChatSessionDeleted("sess_b")).toBe(true);
      expect(result.current.state.activeChatSessionID).toBe("sess_b");
      expect(result.current.state.chatSessions.map((session) => session.id)).toContain("sess_b");
      expect(result.current.state.queuedChatMessages).toHaveLength(1);
      expect(result.current.state.queuedChatMessages[0]).toMatchObject({
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      });
      expect(window.localStorage.getItem(queuedKey)).not.toBeNull();
      expect(window.localStorage.getItem(otherTabKey)).toBeNull();
      expect(window.localStorage.getItem(queuedChatDeletedSessionStorageKey("sess_b"))).toMatch(
        /^deleted:v1:0:/,
      );
      expect(
        window.localStorage.getItem(queuedChatDeletedSessionStorageKey("sess_b")),
      ).not.toContain("do this later");
      expect(result.current.state.notice).toEqual({
        kind: "error",
        message:
          "The chat was deleted on the server, but Hecate could not safely fence and remove its queued prompts from browser storage. Free browser storage or clear Hecate site data, then retry Delete.",
      });

      removeSpy.mockRestore();
      await act(async () => {
        deleted = await result.current.actions.deleteChatSession("sess_b");
      });

      expect(deleted).toBe(true);
      expect(deleteCalls).toBe(2);
      expect(result.current.state.queuedChatMessages).toHaveLength(0);
      expect(window.localStorage.getItem(queuedKey)).toBeNull();
      expect(window.localStorage.getItem(queuedChatDeletedSessionStorageKey("sess_b"))).toMatch(
        /^deleted:v1:0:/,
      );
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

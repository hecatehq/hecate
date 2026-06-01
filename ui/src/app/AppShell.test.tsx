import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ConsoleShell, getAvailableWorkspaces } from "./AppShell";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../test/runtime-console-fixture";
import { withRuntimeConsole } from "../test/runtime-console-render";

// Workspace lineup is fixed. Numeric keyboard workspace switching was
// removed so text inputs, screen readers, and browser shortcuts own the
// number keys without surprises.
describe("getAvailableWorkspaces", () => {
  it("returns chats / runs / connections / overview / usage / settings", () => {
    const ws = getAvailableWorkspaces();
    expect(ws.map((w) => w.id)).toEqual([
      "chats",
      "runs",
      "connections",
      "overview",
      "usage",
      "settings",
    ]);
    expect(ws.map((w) => w.label)).toEqual([
      "Chats",
      "Tasks",
      "Connections",
      "Observability",
      "Usage",
      "Settings",
    ]);
  });

  it("labels the settings workspace 'Settings'", () => {
    const ws = getAvailableWorkspaces();
    const settings = ws.find((w) => w.id === "settings");
    expect(settings?.label).toBe("Settings");
  });
});

// Boot-time loading shell — rendered while /healthz hasn't returned yet
// and there's no error to short-circuit it.
describe("ConsoleShell loading state", () => {
  it("renders the connecting splash while health is null and no error", () => {
    const state = createRuntimeConsoleFixture({ health: null, error: "" });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="overview" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );
    expect(screen.getByText(/connecting/i)).toBeInTheDocument();
  });

  it("centers the lazy-workspace fallback instead of pinning it to the corner", () => {
    const state = createRuntimeConsoleFixture();
    const { container } = render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="usage" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    const fallback = container.querySelector(".workspace-fallback");
    expect(fallback).not.toBeNull();
    expect(fallback).toHaveAttribute("role", "status");
    expect(fallback).toHaveTextContent(/loading workspace/i);
  });
});

// The overlay-titlebar strip is the macOS drag handle that wraps the
// UpdateBanner. Its data-tauri-drag-region="deep" value is
// load-bearing — bare/`"true"` would only drag on direct clicks on the
// strip itself, breaking drag once the banner gains children. Tauri's
// drag.js auto-detects clickable elements (buttons) and skips drag for
// them, so we can rely on the deep value without `-webkit-app-region:
// no-drag` opt-outs.
//
// Linux/Windows don't get the strip — titleBarStyle: "Overlay" is
// macOS-only and on other OSes the native chrome already sits above
// the webview. The banner falls back to its old slot in
// .hecate-content.
describe("ConsoleShell titlebar", () => {
  const originalPlatform = navigator.platform;
  afterEach(() => {
    Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
    Object.defineProperty(navigator, "platform", { configurable: true, value: originalPlatform });
  });

  it('renders the titlebar strip with data-tauri-drag-region="deep" inside Tauri macOS', () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(navigator, "platform", { configurable: true, value: "MacIntel" });
    const state = createRuntimeConsoleFixture();
    const { container } = render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="overview" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );
    const titlebar = container.querySelector(".hecate-titlebar");
    expect(titlebar).not.toBeNull();
    expect(titlebar?.getAttribute("data-tauri-drag-region")).toBe("deep");
  });

  it("omits the titlebar strip on Tauri Linux/Windows", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(navigator, "platform", { configurable: true, value: "Linux x86_64" });
    const state = createRuntimeConsoleFixture();
    const { container } = render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="overview" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );
    expect(container.querySelector(".hecate-titlebar")).toBeNull();
  });

  it("omits the titlebar strip outside Tauri", () => {
    Object.defineProperty(navigator, "platform", { configurable: true, value: "MacIntel" });
    const state = createRuntimeConsoleFixture();
    const { container } = render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="overview" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );
    expect(container.querySelector(".hecate-titlebar")).toBeNull();
  });
});

describe("ConsoleShell navigation", () => {
  it("keeps Chats available when no providers are configured", async () => {
    const state = createRuntimeConsoleFixture({
      chatTarget: "agent",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    // The Chats workspace is a `lazy()` chunk per AppShell.tsx;
    // assert workspace content asynchronously so the assertion
    // waits for the dynamic import to resolve. Shell chrome
    // (workspace nav buttons, statusbar) is not lazy and can
    // still be queried synchronously.
    expect(screen.getByRole("button", { name: "Chats" })).toBeEnabled();
    expect(
      await screen.findByText(/Nothing runnable yet/i, undefined, { timeout: 30_000 }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/No model providers configured/i)).toBeNull();
    expect(screen.getByRole("button", { name: /Open Connections/i })).toBeInTheDocument();
  }, 35_000);

  it("shows the selected agent workspace and git branch in the status bar", () => {
    const state = createRuntimeConsoleFixture({
      chatTarget: "external_agent",
      agentWorkspace: "/Users/alice/dev/hecate",
      agentWorkspaceBranch: "feature/agents",
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    expect(screen.getByText("/Users/alice/dev/hecate")).toBeInTheDocument();
    expect(screen.getByText("git:feature/agents")).toBeInTheDocument();
  });

  it("prefers the active agent chat workspace over the configured workspace", () => {
    const state = createRuntimeConsoleFixture({
      chatTarget: "external_agent",
      agentWorkspace: "/Users/alice/dev/configured",
      agentWorkspaceBranch: "configured",
      activeChatSession: {
        id: "chat_1",
        title: "Active Cursor work",
        agent_id: "cursor_agent",
        workspace: "/Users/alice/dev/hecate",
        workspace_branch: "main",
        status: "completed",
        messages: [],
      },
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    expect(screen.getByText("/Users/alice/dev/hecate")).toBeInTheDocument();
    expect(screen.getByText("git:main")).toBeInTheDocument();
    expect(screen.queryByText("/Users/alice/dev/configured")).toBeNull();
    expect(screen.queryByText("git:configured")).toBeNull();
  });

  it("shows latest reported agent context usage in the status bar", () => {
    const state = createRuntimeConsoleFixture({
      chatTarget: "external_agent",
      activeChatSession: {
        id: "chat_1",
        title: "Codex work",
        agent_id: "codex",
        workspace: "/Users/alice/dev/hecate",
        workspace_branch: "main",
        status: "completed",
        messages: [
          {
            id: "msg_1",
            role: "assistant",
            content: "Earlier",
            usage: { context_size: 200_000, context_used: 10_000 },
          },
          {
            id: "msg_2",
            role: "assistant",
            content: "Latest",
            usage: { context_size: 200_000, context_used: 42_000 },
          },
        ],
      },
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    expect(screen.getByText("context 79% left")).toBeInTheDocument();
  });

  it("keeps No project as the default chat-sidebar project context", () => {
    const state = createRuntimeConsoleFixture({
      agentWorkspace: "/Users/alice/dev/hecate",
      projects: [
        {
          id: "proj_1",
          name: "Hecate",
          roots: [
            {
              id: "root_1",
              path: "/Users/alice/dev/hecate",
              kind: "workspace",
              active: true,
              created_at: "2026-05-21T10:00:00Z",
              updated_at: "2026-05-21T10:00:00Z",
            },
          ],
          default_root_id: "root_1",
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
      activeProjectID: "",
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    expect(screen.getByRole("button", { name: /Project No project/i })).toHaveAttribute(
      "aria-current",
      "true",
    );
    expect(screen.queryByRole("button", { name: /Project Hecate/i })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Expand projects/i }));
    expect(screen.getByRole("button", { name: /Project Hecate/i })).toBeInTheDocument();
  });

  it("lets the chat sidebar switch back to No project", () => {
    const selectProject = vi.fn(async () => undefined);
    const state = createRuntimeConsoleFixture({
      projects: [
        {
          id: "proj_1",
          name: "Hecate",
          roots: [
            {
              id: "root_1",
              path: "/Users/alice/dev/hecate",
              kind: "local",
              active: true,
              created_at: "2026-05-21T10:00:00Z",
              updated_at: "2026-05-21T10:00:00Z",
            },
          ],
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
      activeProjectID: "proj_1",
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: { ...createRuntimeConsoleActions(), selectProject },
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: /Expand projects/i }));
    fireEvent.click(screen.getByRole("button", { name: /Project No project/i }));

    expect(selectProject).toHaveBeenCalledWith("");
  });

  it("shows only chats for the selected project", async () => {
    const state = createRuntimeConsoleFixture({
      projects: [
        {
          id: "proj_1",
          name: "Hecate",
          roots: [
            {
              id: "root_1",
              path: "/Users/alice/dev/hecate",
              kind: "workspace",
              active: true,
              created_at: "2026-05-21T10:00:00Z",
              updated_at: "2026-05-21T10:00:00Z",
            },
          ],
          default_root_id: "root_1",
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
      activeProjectID: "proj_1",
      chatSessions: [
        {
          id: "chat_project",
          title: "Project chat",
          project_id: "proj_1",
          agent_id: "hecate",
          status: "idle",
          workspace: "",
          message_count: 0,
        },
        {
          id: "chat_loose",
          title: "Loose chat",
          agent_id: "hecate",
          status: "idle",
          workspace: "",
          message_count: 0,
        },
      ],
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    expect(await screen.findByText("Project chat")).toBeInTheDocument();
    expect(screen.queryByText("Loose chat")).toBeNull();
  });

  it("creates new chats inside the selected project scope", async () => {
    const createChatSession = vi.fn(async () => undefined);
    const state = createRuntimeConsoleFixture({
      agentWorkspace: "/Users/alice/dev/hecate",
      projects: [
        {
          id: "proj_1",
          name: "Hecate",
          roots: [
            {
              id: "root_1",
              path: "/Users/alice/dev/hecate",
              kind: "workspace",
              active: true,
              created_at: "2026-05-21T10:00:00Z",
              updated_at: "2026-05-21T10:00:00Z",
            },
          ],
          default_root_id: "root_1",
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
      activeProjectID: "proj_1",
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: { ...createRuntimeConsoleActions(), createChatSession },
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: /New Hecate chat/i }));

    expect(createChatSession).toHaveBeenCalledWith({
      agentID: "hecate",
      projectID: "proj_1",
    });
  });

  it("moves the active chat when selecting a different project", () => {
    const selectProject = vi.fn(async () => undefined);
    const selectChatSession = vi.fn(async () => undefined);
    const state = createRuntimeConsoleFixture({
      projects: [
        {
          id: "proj_1",
          name: "Hecate",
          roots: [],
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
      activeProjectID: "",
      activeChatSessionID: "chat_loose",
      chatSessions: [
        {
          id: "chat_project",
          title: "Project chat",
          project_id: "proj_1",
          agent_id: "hecate",
          status: "idle",
          workspace: "",
          message_count: 0,
        },
        {
          id: "chat_loose",
          title: "Loose chat",
          agent_id: "hecate",
          status: "idle",
          workspace: "",
          message_count: 0,
        },
      ],
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: { ...createRuntimeConsoleActions(), selectProject, selectChatSession },
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: /Expand projects/i }));
    fireEvent.click(screen.getByRole("button", { name: /Project Hecate/i }));

    expect(selectProject).toHaveBeenCalledWith("proj_1");
    expect(selectChatSession).toHaveBeenCalledWith("chat_project");
  });

  it("shows only unprojected chats when No project is selected", async () => {
    const state = createRuntimeConsoleFixture({
      projects: [
        {
          id: "proj_1",
          name: "Hecate",
          roots: [],
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
      activeProjectID: "",
      chatSessions: [
        {
          id: "chat_project",
          title: "Project chat",
          project_id: "proj_1",
          agent_id: "hecate",
          status: "idle",
          workspace: "",
          message_count: 0,
        },
        {
          id: "chat_loose",
          title: "Loose chat",
          agent_id: "hecate",
          status: "idle",
          workspace: "",
          message_count: 0,
        },
      ],
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    expect(await screen.findByText("Loose chat")).toBeInTheDocument();
    expect(screen.queryByText("Project chat")).toBeNull();
  });

  it("hides chats from deleted projects in the sidebar", async () => {
    const state = createRuntimeConsoleFixture({
      projects: [],
      activeProjectID: "",
      chatSessions: [
        {
          id: "chat_orphaned",
          title: "Recovered chat",
          project_id: "proj_deleted",
          agent_id: "hecate",
          status: "idle",
          workspace: "",
          message_count: 0,
        },
      ],
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    expect(screen.queryByText("Recovered chat")).toBeNull();
    expect(await screen.findByText("No unprojected chats yet")).toBeInTheDocument();
  });

  it("renames projects from the chat sidebar", () => {
    const renameProject = vi.fn(async () => undefined);
    const state = createRuntimeConsoleFixture({
      projects: [
        {
          id: "proj_1",
          name: "Hecate",
          roots: [],
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: { ...createRuntimeConsoleActions(), renameProject },
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: /Expand projects/i }));
    const projectButton = screen.getByRole("button", { name: /Project Hecate/i });
    fireEvent.mouseEnter(projectButton.parentElement as HTMLElement);
    fireEvent.click(screen.getByRole("button", { name: /Rename project Hecate/i }));
    const input = screen.getByRole("textbox", { name: /Rename project Hecate/i });
    fireEvent.change(input, { target: { value: "Hecate Core" } });
    fireEvent.keyDown(input, { key: "Enter" });

    expect(renameProject).toHaveBeenCalledWith("proj_1", "Hecate Core");
  });

  it("confirms project deletion from the chat sidebar", async () => {
    const deleteProject = vi.fn(async () => true);
    const state = createRuntimeConsoleFixture({
      projects: [
        {
          id: "proj_1",
          name: "Hecate",
          roots: [],
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
      activeProjectID: "proj_1",
      chatSessions: [
        {
          id: "chat_project",
          title: "Project chat",
          project_id: "proj_1",
          agent_id: "hecate",
          status: "idle",
          workspace: "",
          message_count: 0,
        },
      ],
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: { ...createRuntimeConsoleActions(), deleteProject },
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: /Expand projects/i }));
    const projectButton = screen.getByRole("button", { name: /Project Hecate/i });
    fireEvent.mouseEnter(projectButton.parentElement as HTMLElement);
    fireEvent.click(screen.getByRole("button", { name: /Delete project Hecate/i }));

    expect(deleteProject).not.toHaveBeenCalled();
    expect(screen.getByText(/This also deletes chats in this project/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^Delete project$/i }));

    expect(deleteProject).toHaveBeenCalledWith("proj_1");
    await waitFor(() => {
      expect(screen.queryByText("Project chat")).toBeNull();
    });
  });

  it("keeps project chats visible when project deletion fails", async () => {
    const deleteProject = vi.fn(async () => false);
    const state = createRuntimeConsoleFixture({
      projects: [
        {
          id: "proj_1",
          name: "Hecate",
          roots: [],
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
      activeProjectID: "proj_1",
      chatSessions: [
        {
          id: "chat_project",
          title: "Project chat",
          project_id: "proj_1",
          agent_id: "hecate",
          status: "idle",
          workspace: "",
          message_count: 0,
        },
      ],
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: { ...createRuntimeConsoleActions(), deleteProject },
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: /Expand projects/i }));
    const projectButton = screen.getByRole("button", { name: /Project Hecate/i });
    fireEvent.mouseEnter(projectButton.parentElement as HTMLElement);
    fireEvent.click(screen.getByRole("button", { name: /Delete project Hecate/i }));
    fireEvent.click(screen.getByRole("button", { name: /^Delete project$/i }));

    expect(deleteProject).toHaveBeenCalledWith("proj_1");
    expect(await screen.findByText("Project chat")).toBeInTheDocument();
  });

  it("confirms chat deletion from the chat sidebar", async () => {
    const deleteChatSession = vi.fn(async () => undefined);
    const state = createRuntimeConsoleFixture({
      chatSessions: [
        {
          id: "chat_1",
          title: "Cleanup chat",
          agent_id: "hecate",
          status: "idle",
          workspace: "",
          message_count: 0,
          created_at: "2026-05-21T10:00:00Z",
          updated_at: "2026-05-21T10:00:00Z",
        },
      ],
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />, {
        state,
        actions: { ...createRuntimeConsoleActions(), deleteChatSession },
      }),
    );

    const chatRow = await screen.findByLabelText("Chat Cleanup chat, Hecate");
    fireEvent.mouseEnter(chatRow);
    fireEvent.click(screen.getByRole("button", { name: /Delete chat Cleanup chat/i }));

    expect(deleteChatSession).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /^Delete chat$/i }));

    expect(deleteChatSession).toHaveBeenCalledWith("chat_1");
  });
});

describe("ConsoleShell theme toggle", () => {
  function stubColorScheme(matchesLight: boolean) {
    const listeners = new Set<() => void>();
    const query = {
      matches: matchesLight,
      media: "(prefers-color-scheme: light)",
      onchange: null,
      addEventListener: vi.fn((_event: string, listener: () => void) => {
        listeners.add(listener);
      }),
      removeEventListener: vi.fn((_event: string, listener: () => void) => {
        listeners.delete(listener);
      }),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    } as unknown as MediaQueryList;
    vi.stubGlobal(
      "matchMedia",
      vi.fn(() => query),
    );
    return query;
  }

  beforeEach(() => {
    localStorage.clear();
    document.documentElement.removeAttribute("data-theme");
  });

  afterEach(() => {
    localStorage.clear();
    document.documentElement.removeAttribute("data-theme");
    vi.unstubAllGlobals();
  });

  it("follows the OS theme when no preference is saved", () => {
    stubColorScheme(true);
    const state = createRuntimeConsoleFixture();

    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="settings" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    expect(localStorage.getItem("hecate.theme")).toBeNull();
  });

  it("toggles the document theme and persists the choice", () => {
    stubColorScheme(false);
    document.documentElement.setAttribute("data-theme", "dark");
    const state = createRuntimeConsoleFixture();

    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="settings" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: /switch to light theme/i }));

    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    expect(localStorage.getItem("hecate.theme")).toBe("light");
    expect(screen.getByRole("button", { name: /switch to dark theme/i })).toBeInTheDocument();
  });
});

// Status bar version render — guards the conditional that hides the
// version chip when /healthz didn't include one (older gateway, or the
// field genuinely missing). The workspace branch renders the embedded
// views, which fan out fetches on mount; we stub fetch globally here so
// those calls don't blow up under jsdom.
describe("status bar version chip", () => {
  function renderWorkspace(healthOverrides: Record<string, unknown> | null) {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ object: "list", data: [] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
      ),
    );
    const state = createRuntimeConsoleFixture({
      // null clears the fixture's default { status: "ok", time: ... };
      // anything else replaces the whole object so the render branch
      // sees the version we feed it (or its absence).
      health: healthOverrides as never,
    });
    render(
      withRuntimeConsole(<ConsoleShell activeWorkspace="overview" onSelectWorkspace={() => {}} />, {
        state,
        actions: createRuntimeConsoleActions(),
      }),
    );
  }

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders the version when /healthz returned one", () => {
    const sampleVersion = "test-build-abc123";
    renderWorkspace({ status: "ok", time: "2026-04-25T00:00:00Z", version: sampleVersion });

    // Version sits inside the status bar; scope the query so a stray
    // version string elsewhere on screen wouldn't false-positive the
    // test.
    const statusbar = document.querySelector(".hecate-statusbar");
    expect(statusbar).not.toBeNull();
    expect(statusbar!.textContent).toContain(sampleVersion);
  });

  it("hides the version chip when /healthz did not include one", () => {
    renderWorkspace({ status: "ok", time: "2026-04-25T00:00:00Z" });

    const statusbar = document.querySelector(".hecate-statusbar");
    expect(statusbar).not.toBeNull();
    // Status bar renders brand · session · configured · models (3
    // separators); the version chip stays out — that would bring it
    // to 4. Assert by counting separators.
    const sepCount = statusbar!.querySelectorAll(".hecate-statusbar__sep").length;
    expect(sepCount).toBe(3);
  });
});

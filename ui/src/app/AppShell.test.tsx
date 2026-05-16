import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ConsoleShell, getAvailableWorkspaces } from "./AppShell";
import { createRuntimeConsoleActions, createRuntimeConsoleFixture } from "../test/runtime-console-fixture";

// Workspace lineup is fixed. Numeric keyboard workspace switching was
// removed so text inputs, screen readers, and browser shortcuts own the
// number keys without surprises.
describe("getAvailableWorkspaces", () => {
  it("returns chats / connections / runs / overview / usage / settings", () => {
    const ws = getAvailableWorkspaces();
    expect(ws.map(w => w.id)).toEqual(["chats", "connections", "runs", "overview", "usage", "settings"]);
    expect(ws.map(w => w.label)).toEqual(["Chats", "Connections", "Tasks", "Observability", "Usage", "Settings"]);
  });

  it("labels the settings workspace 'Settings'", () => {
    const ws = getAvailableWorkspaces();
    const settings = ws.find(w => w.id === "settings");
    expect(settings?.label).toBe("Settings");
  });
});

// Boot-time loading shell — rendered while /healthz hasn't returned yet
// and there's no error to short-circuit it.
describe("ConsoleShell loading state", () => {
  it("renders the connecting splash while health is null and no error", () => {
    const state = createRuntimeConsoleFixture({ health: null, error: "" });
    render(
      <ConsoleShell
        activeWorkspace="overview"
        onSelectWorkspace={() => {}}
        state={state}
        actions={createRuntimeConsoleActions()}
      />,
    );
    expect(screen.getByText(/connecting/i)).toBeInTheDocument();
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

  it("renders the titlebar strip with data-tauri-drag-region=\"deep\" inside Tauri macOS", () => {
    Reflect.set(window, "__TAURI_INTERNALS__", {});
    Object.defineProperty(navigator, "platform", { configurable: true, value: "MacIntel" });
    const state = createRuntimeConsoleFixture();
    const { container } = render(
      <ConsoleShell
        activeWorkspace="overview"
        onSelectWorkspace={() => {}}
        state={state}
        actions={createRuntimeConsoleActions()}
      />,
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
      <ConsoleShell
        activeWorkspace="overview"
        onSelectWorkspace={() => {}}
        state={state}
        actions={createRuntimeConsoleActions()}
      />,
    );
    expect(container.querySelector(".hecate-titlebar")).toBeNull();
  });

  it("omits the titlebar strip outside Tauri", () => {
    Object.defineProperty(navigator, "platform", { configurable: true, value: "MacIntel" });
    const state = createRuntimeConsoleFixture();
    const { container } = render(
      <ConsoleShell
        activeWorkspace="overview"
        onSelectWorkspace={() => {}}
        state={state}
        actions={createRuntimeConsoleActions()}
      />,
    );
    expect(container.querySelector(".hecate-titlebar")).toBeNull();
  });
});

describe("ConsoleShell navigation", () => {
  it("keeps Chats available when no providers are configured", async () => {
    const state = createRuntimeConsoleFixture({
      chatTarget: "model",
      settingsConfig: { backend: "memory", providers: [], policy_rules: [], events: [] },
    });
    render(
      <ConsoleShell
        activeWorkspace="chats"
        onSelectWorkspace={() => {}}
        state={state}
        actions={createRuntimeConsoleActions()}
      />,
    );

    // The Chats workspace is a `lazy()` chunk per AppShell.tsx;
    // assert workspace content asynchronously so the assertion
    // waits for the dynamic import to resolve. Shell chrome
    // (workspace nav buttons, statusbar) is not lazy and can
    // still be queried synchronously.
    expect(screen.getByRole("button", { name: "Chats" })).toBeEnabled();
    expect(await screen.findByText(/Nothing runnable yet/i, undefined, { timeout: 30_000 })).toBeInTheDocument();
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
      <ConsoleShell
        activeWorkspace="chats"
        onSelectWorkspace={() => {}}
        state={state}
        actions={createRuntimeConsoleActions()}
      />,
    );

    expect(screen.getByText("/Users/alice/dev/hecate")).toBeInTheDocument();
    expect(screen.getByText("git:feature/agents")).toBeInTheDocument();
  });

  it("prefers the active agent chat workspace over the draft workspace", () => {
    const state = createRuntimeConsoleFixture({
      chatTarget: "external_agent",
      agentWorkspace: "/Users/alice/dev/draft",
      agentWorkspaceBranch: "draft",
      activeAgentChatSession: {
        id: "agent_chat_1",
        title: "Active Cursor work",
        adapter_id: "cursor_agent",
        workspace: "/Users/alice/dev/hecate",
        workspace_branch: "main",
        status: "completed",
        messages: [],
      },
    });
    render(
      <ConsoleShell
        activeWorkspace="chats"
        onSelectWorkspace={() => {}}
        state={state}
        actions={createRuntimeConsoleActions()}
      />,
    );

    expect(screen.getByText("/Users/alice/dev/hecate")).toBeInTheDocument();
    expect(screen.getByText("git:main")).toBeInTheDocument();
    expect(screen.queryByText("/Users/alice/dev/draft")).toBeNull();
    expect(screen.queryByText("git:draft")).toBeNull();
  });

  it("shows latest reported agent context usage in the status bar", () => {
    const state = createRuntimeConsoleFixture({
      chatTarget: "external_agent",
      activeAgentChatSession: {
        id: "agent_chat_1",
        title: "Codex work",
        adapter_id: "codex",
        workspace: "/Users/alice/dev/hecate",
        workspace_branch: "main",
        status: "completed",
        messages: [
          { id: "msg_1", role: "assistant", content: "Earlier", usage: { context_size: 200_000, context_used: 10_000 } },
          { id: "msg_2", role: "assistant", content: "Latest", usage: { context_size: 200_000, context_used: 42_000 } },
        ],
      },
    });
    render(
      <ConsoleShell
        activeWorkspace="chats"
        onSelectWorkspace={() => {}}
        state={state}
        actions={createRuntimeConsoleActions()}
      />,
    );

    expect(screen.getByText("context 79% left")).toBeInTheDocument();
  });

  it("does not show agent workspace details while chatting with models", () => {
    const state = createRuntimeConsoleFixture({
      chatTarget: "model",
      agentWorkspace: "/Users/alice/dev/hecate",
      agentWorkspaceBranch: "main",
    });
    render(
      <ConsoleShell
        activeWorkspace="chats"
        onSelectWorkspace={() => {}}
        state={state}
        actions={createRuntimeConsoleActions()}
      />,
    );

    expect(screen.queryByText("/Users/alice/dev/hecate")).toBeNull();
    expect(screen.queryByText("git:main")).toBeNull();
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
    vi.stubGlobal("matchMedia", vi.fn(() => query));
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
      <ConsoleShell
        activeWorkspace="settings"
        onSelectWorkspace={() => {}}
        state={state}
        actions={createRuntimeConsoleActions()}
      />,
    );

    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    expect(localStorage.getItem("hecate.theme")).toBeNull();
  });

  it("toggles the document theme and persists the choice", () => {
    stubColorScheme(false);
    document.documentElement.setAttribute("data-theme", "dark");
    const state = createRuntimeConsoleFixture();

    render(
      <ConsoleShell
        activeWorkspace="settings"
        onSelectWorkspace={() => {}}
        state={state}
        actions={createRuntimeConsoleActions()}
      />,
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
      vi.fn(async () =>
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
      <ConsoleShell
        activeWorkspace="overview"
        onSelectWorkspace={() => {}}
        state={state}
        actions={createRuntimeConsoleActions()}
      />,
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

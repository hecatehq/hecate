import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ConsoleShell, getAvailableWorkspaces, resolveTaskFocusRequest } from "./AppShell";
import { useChat } from "./state/chat";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
  type RuntimeConsoleFixtureActions,
} from "../test/runtime-console-fixture";
import { withRuntimeConsole } from "../test/runtime-console-render";
import {
  getChatSession,
  getProjectAssignments,
  getProjectActivity,
  getProjectCollaborationArtifacts,
  getProjectHandoffs,
  getProjectHealth,
  getProjectOperationsBrief,
  getProjectSetupReadiness,
  getProjectWorkItem,
  getProjectWorkItemReadiness,
  getProjectWorkItems,
  getProjectWorkRoles,
} from "../lib/api";
import type {
  ProjectAssignmentRecord,
  ProjectHealth,
  ProjectSetupReadiness,
  ProjectWorkItemRecord,
} from "../types/project";

const tasksViewProbe = vi.hoisted(() => ({ render: vi.fn() }));

vi.mock("../features/tasks/TasksView", () => {
  return {
    TasksView: (props: {
      focusRequest?: { taskID: string; runID?: string; nonce: number } | null;
      onOpenChat?: (sessionID: string, taskID?: string, runID?: string) => void;
    }) => {
      tasksViewProbe.render(props.focusRequest);
      return (
        <button type="button" onClick={() => props.onOpenChat?.("chat_task", "task_1", "run_1")}>
          Open linked Task chat
        </button>
      );
    },
  };
});

vi.mock("../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/api")>();
  const emptyWorkItem = {
    id: "",
    project_id: "",
    title: "",
    status: "backlog",
    priority: "normal",
    created_at: "",
    updated_at: "",
  };
  return {
    ...actual,
    getChatSession: vi.fn(async () => ({
      object: "chat_session",
      data: {
        id: "chat_task",
        agent_id: "hecate",
        title: "Task chat",
        status: "completed",
        workspace: "",
        messages: [],
      },
    })),
    getProjectActivity: vi.fn(async () => ({
      object: "project_activity",
      data: emptyActivityData(),
    })),
    getProjectOperationsBrief: vi.fn(async () => ({
      object: "project_operations_brief",
      data: emptyOperationsBriefData(),
    })),
    getProjectHealth: vi.fn(async () => ({
      object: "project_health",
      data: emptyProjectHealthData(),
    })),
    getProjectSetupReadiness: vi.fn(async () => ({
      object: "project_setup_readiness",
      data: emptyProjectSetupReadinessData(),
    })),
    getProjectWorkRoles: vi.fn(async () => ({ object: "project_roles", data: [] })),
    getProjectWorkItems: vi.fn(async () => ({ object: "project_work_items", data: [] })),
    getProjectWorkItem: vi.fn(async () => ({ object: "project_work_item", data: emptyWorkItem })),
    getProjectWorkItemReadiness: vi.fn(async () => ({
      object: "project_work_item_readiness",
      data: {
        project_id: "",
        work_item_id: "",
        ready: false,
        status: "blocked",
        title: "Closeout readiness unavailable",
        detail: "Refresh work item detail before marking done.",
        blockers: ["Closeout readiness is unavailable."],
        warnings: [],
        assignment_count: 0,
        completed_assignments: 0,
        review_follow_up_count: 0,
      },
    })),
    getProjectAssignments: vi.fn(async () => ({ object: "project_assignments", data: [] })),
    getProjectCollaborationArtifacts: vi.fn(async () => ({
      object: "project_collaboration_artifacts",
      data: [],
    })),
    getProjectHandoffs: vi.fn(async () => ({ object: "project_handoffs", data: [] })),
  };
});

const projectDeleteResult = {
  project_id: "proj_1",
  project_name: "Hecate",
  chat_sessions_deleted: 1,
  project_work_rows_deleted: 2,
  project_skills_deleted: 1,
  memory_entries_deleted: 3,
  memory_candidates_deleted: 4,
};

function LateImageDraftControl() {
  const chat = useChat();
  return (
    <>
      <button
        type="button"
        onClick={() =>
          chat.actions.setPendingChatAttachments([
            {
              id: "late-navigation-draft",
              file: new File(["image"], "late-navigation.png", { type: "image/png" }),
            },
          ])
        }
      >
        Attach late navigation image
      </button>
      <output aria-label="Late navigation image count">
        {chat.state.pendingChatAttachments.length}
      </output>
    </>
  );
}

function RemoveChatSessionControl({ chatID }: { chatID: string }) {
  const chat = useChat();
  return (
    <button
      type="button"
      onClick={() =>
        chat.actions.setChatSessions((sessions) =>
          sessions.filter((session) => session.id !== chatID),
        )
      }
    >
      Remove deleted chat from state
    </button>
  );
}

function emptyActivityData() {
  return {
    project_id: "",
    summary: {
      work_item_count: 0,
      assignment_count: 0,
      active_count: 0,
      blocked_count: 0,
      completed_count: 0,
      recent_count: 0,
    },
    buckets: {
      active: [],
      blocked: [],
      completed: [],
      recent: [],
    },
    recent: [],
  };
}

function emptyOperationsBriefData() {
  return {
    project_id: "",
    generated_at: "",
    summary: {
      item_count: 0,
      high_count: 0,
      medium_count: 0,
      low_count: 0,
      pending_memory_candidate_count: 0,
      pending_handoff_count: 0,
    },
    items: [],
  };
}

function emptyProjectHealthData(): ProjectHealth {
  return {
    project_id: "",
    generated_at: "",
    summary: {
      attention_count: 0,
      available_attention_count: 0,
      omitted_attention_count: 0,
      attention_limit: 5,
      missing_defaults: false,
      missing_project_root: false,
      enabled_memory_count: 0,
      saved_memory_count: 0,
      enabled_context_source_count: 0,
      pending_memory_candidate_count: 0,
      promoted_memory_candidate_count: 0,
      rejected_memory_candidate_count: 0,
      pending_handoff_count: 0,
      accepted_handoff_count: 0,
      superseded_handoff_count: 0,
      dismissed_handoff_count: 0,
      review_follow_up_count: 0,
      blocked_review_count: 0,
      changes_requested_review_count: 0,
      stale_or_unknown_assignment_count: 0,
    },
    attention: [],
  };
}

function emptyProjectSetupReadinessData(): ProjectSetupReadiness {
  return {
    project_id: "",
    generated_at: "",
    show_onboarding: false,
    setup_started: true,
    first_work_ready: false,
    summary: {
      work_item_count: 0,
      role_count: 0,
      skill_count: 0,
      enabled_context_source_count: 0,
      saved_memory_count: 0,
      pending_memory_candidate_count: 0,
      has_purpose: false,
      has_active_root: false,
      missing_defaults: false,
    },
    primary_action: {
      type: "bootstrap_project",
      project_id: "",
      label: "Set up project",
    },
    checks: [],
  };
}

function resetProjectWorkMocks() {
  const emptyWorkItem: ProjectWorkItemRecord = {
    id: "",
    project_id: "",
    title: "",
    status: "backlog",
    priority: "normal",
    created_at: "",
    updated_at: "",
  };
  tasksViewProbe.render.mockClear();
  vi.mocked(getChatSession).mockReset();
  vi.mocked(getChatSession).mockResolvedValue({
    object: "chat_session",
    data: {
      id: "chat_task",
      agent_id: "hecate",
      title: "Task chat",
      status: "completed",
      workspace: "",
      messages: [],
    },
  });
  vi.mocked(getProjectActivity).mockResolvedValue({
    object: "project_activity",
    data: emptyActivityData(),
  });
  vi.mocked(getProjectOperationsBrief).mockResolvedValue({
    object: "project_operations_brief",
    data: emptyOperationsBriefData(),
  });
  vi.mocked(getProjectHealth).mockResolvedValue({
    object: "project_health",
    data: emptyProjectHealthData(),
  });
  vi.mocked(getProjectSetupReadiness).mockResolvedValue({
    object: "project_setup_readiness",
    data: emptyProjectSetupReadinessData(),
  });
  vi.mocked(getProjectWorkRoles).mockResolvedValue({ object: "project_roles", data: [] });
  vi.mocked(getProjectWorkItems).mockResolvedValue({ object: "project_work_items", data: [] });
  vi.mocked(getProjectWorkItem).mockResolvedValue({
    object: "project_work_item",
    data: emptyWorkItem,
  });
  vi.mocked(getProjectWorkItemReadiness).mockResolvedValue({
    object: "project_work_item_readiness",
    data: {
      project_id: "",
      work_item_id: "",
      ready: false,
      status: "blocked",
      title: "Closeout readiness unavailable",
      detail: "Refresh work item detail before marking done.",
      blockers: ["Closeout readiness is unavailable."],
      warnings: [],
      assignment_count: 0,
      completed_assignments: 0,
      review_follow_up_count: 0,
    },
  });
  vi.mocked(getProjectAssignments).mockResolvedValue({ object: "project_assignments", data: [] });
  vi.mocked(getProjectCollaborationArtifacts).mockResolvedValue({
    object: "project_collaboration_artifacts",
    data: [],
  });
  vi.mocked(getProjectHandoffs).mockResolvedValue({
    object: "project_handoffs",
    data: [],
  });
}

beforeEach(() => {
  resetProjectWorkMocks();
});

// Workspace lineup is fixed. Numeric keyboard workspace switching was
// removed so text inputs, screen readers, and browser shortcuts own the
// number keys without surprises.
describe("getAvailableWorkspaces", () => {
  it("returns chats / projects / tasks / connections / overview / usage / settings", () => {
    const ws = getAvailableWorkspaces();
    expect(ws.map((w) => w.id)).toEqual([
      "chats",
      "projects",
      "tasks",
      "connections",
      "overview",
      "usage",
      "settings",
    ]);
    expect(ws.map((w) => w.label)).toEqual([
      "Chats",
      "Projects",
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

  it("treats a present bare Tasks route as authoritative over imperative focus", () => {
    const imperativeFocus = { taskID: "task_1", runID: "run_1", nonce: 1 };

    expect(resolveTaskFocusRequest(undefined, imperativeFocus)).toBe(imperativeFocus);
    expect(resolveTaskFocusRequest({ taskID: null, runID: null }, imperativeFocus)).toBeNull();
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
  it("renders Projects as a top-level workspace and switches to the Projects view", async () => {
    const state = createRuntimeConsoleFixture();
    const onSelectWorkspace = vi.fn();
    render(
      withRuntimeConsole(
        <ConsoleShell activeWorkspace="projects" onSelectWorkspace={onSelectWorkspace} />,
        {
          state,
          actions: createRuntimeConsoleActions(),
        },
      ),
    );

    expect(screen.getByRole("link", { name: "Projects" })).toHaveAttribute("href", "/projects");
    expect(
      await screen.findByText("No projects yet", undefined, { timeout: 30_000 }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("link", { name: "Tasks" }));
    expect(onSelectWorkspace).toHaveBeenCalledWith("tasks");
  }, 35_000);

  it("leaves modified workspace-link clicks to the browser", () => {
    const state = createRuntimeConsoleFixture();
    const onSelectWorkspace = vi.fn();
    render(
      withRuntimeConsole(
        <ConsoleShell activeWorkspace="chats" onSelectWorkspace={onSelectWorkspace} />,
        {
          state,
          actions: createRuntimeConsoleActions(),
        },
      ),
    );

    const tasks = screen.getByRole("link", { name: "Tasks" });
    expect(tasks).toHaveAttribute("href", "/tasks");
    document.addEventListener("click", (event) => event.preventDefault(), { once: true });
    fireEvent.click(tasks, { ctrlKey: true });

    expect(onSelectWorkspace).not.toHaveBeenCalled();
  });

  it("navigates from a Chat message to its exact Task and Run", async () => {
    const onSelectWorkspace = vi.fn();
    const onTaskNavigate = vi.fn();
    const state = createRuntimeConsoleFixture({
      chatTarget: "agent",
      activeChatSessionID: "chat_1",
      activeChatSession: {
        id: "chat_1",
        agent_id: "hecate",
        execution_mode: "hecate_task",
        tools_enabled: true,
        title: "Repo work",
        provider: "openai",
        model: "gpt-4o-mini",
        status: "completed",
        messages: [
          {
            id: "message_1",
            role: "assistant",
            content: "Done.",
            turn_kind: "hecate_task",
            execution_mode: "hecate_task",
            task_id: "task_1",
            run_id: "run_1",
            status: "completed",
          },
        ],
      } as any,
    });

    render(
      withRuntimeConsole(
        <ConsoleShell
          activeWorkspace="chats"
          onSelectWorkspace={onSelectWorkspace}
          onTaskNavigate={onTaskNavigate}
        />,
        { state, actions: createRuntimeConsoleActions() },
      ),
    );

    fireEvent.click(await screen.findByRole("button", { name: "Open Run 1" }, { timeout: 30_000 }));

    expect(onTaskNavigate).toHaveBeenCalledWith({ taskID: "task_1", runID: "run_1" });
    expect(onSelectWorkspace).not.toHaveBeenCalledWith("tasks");
  }, 35_000);

  it("loads a Chat addressed by the URL", async () => {
    const selectChatSession = vi.fn(async () => true);
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "",
      activeChatSession: null,
    });

    render(
      withRuntimeConsole(
        <ConsoleShell
          activeWorkspace="chats"
          chatNavigation={{ chatID: "chat_routed", messageID: null }}
          onSelectWorkspace={() => {}}
        />,
        { state, actions: { ...createRuntimeConsoleActions(), selectChatSession } },
      ),
    );

    await waitFor(() => expect(selectChatSession).toHaveBeenCalledWith("chat_routed"));
  });

  it("does not reapply the old Chat URL while a sidebar selection is completing", async () => {
    const selectChatSession = vi.fn(async () => true);
    const onChatNavigate = vi.fn();
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "chat_a",
      activeChatSession: {
        id: "chat_a",
        title: "Chat A",
        agent_id: "hecate",
        status: "idle",
        messages: [],
      } as any,
      chatSessions: [
        { id: "chat_a", title: "Chat A", agent_id: "hecate", status: "idle" } as any,
        { id: "chat_b", title: "Chat B", agent_id: "hecate", status: "idle" } as any,
      ],
    });

    render(
      withRuntimeConsole(
        <ConsoleShell
          activeWorkspace="chats"
          chatNavigation={{ chatID: "chat_a", messageID: null }}
          onChatNavigate={onChatNavigate}
          onSelectWorkspace={() => {}}
        />,
        { state, actions: { ...createRuntimeConsoleActions(), selectChatSession } },
      ),
    );

    fireEvent.click(await screen.findByText("Chat B", undefined, { timeout: 30_000 }));

    await waitFor(() => expect(selectChatSession).toHaveBeenCalledWith("chat_b"));
    expect(selectChatSession).not.toHaveBeenCalledWith("chat_a");
    expect(onChatNavigate).toHaveBeenCalledWith({ chatID: "chat_b", messageID: null });
  });

  it("does not restore a routed Chat when a newer sidebar selection supersedes its load", async () => {
    let resolveRoutedSelection: ((selected: boolean) => void) | undefined;
    const selectChatSession = vi.fn((sessionID: string) => {
      if (sessionID !== "chat_routed") return Promise.resolve(true);
      return new Promise<boolean>((resolve) => {
        resolveRoutedSelection = resolve;
      });
    });
    const onChatNavigate = vi.fn();
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "chat_a",
      activeChatSession: {
        id: "chat_a",
        title: "Chat A",
        agent_id: "hecate",
        status: "idle",
        messages: [],
      } as any,
      chatSessions: [
        { id: "chat_a", title: "Chat A", agent_id: "hecate", status: "idle" } as any,
        { id: "chat_b", title: "Chat B", agent_id: "hecate", status: "idle" } as any,
      ],
    });

    render(
      withRuntimeConsole(
        <ConsoleShell
          activeWorkspace="chats"
          chatNavigation={{ chatID: "chat_routed", messageID: null }}
          onChatNavigate={onChatNavigate}
          onSelectWorkspace={() => {}}
        />,
        { state, actions: { ...createRuntimeConsoleActions(), selectChatSession } },
      ),
    );

    await waitFor(() => expect(selectChatSession).toHaveBeenCalledWith("chat_routed"));
    fireEvent.click(await screen.findByText("Chat B", undefined, { timeout: 30_000 }));
    await waitFor(() => expect(selectChatSession).toHaveBeenCalledWith("chat_b"));
    await act(async () => resolveRoutedSelection?.(false));

    await waitFor(() =>
      expect(onChatNavigate).toHaveBeenCalledWith({ chatID: "chat_b", messageID: null }),
    );
    expect(onChatNavigate).not.toHaveBeenCalledWith(
      { chatID: "chat_a", messageID: null },
      "replace",
    );
  }, 35_000);

  it("drops an in-flight Task-to-Chat lookup after newer workspace navigation", async () => {
    let resolveChatLookup:
      | ((value: Awaited<ReturnType<typeof getChatSession>>) => void)
      | undefined;
    vi.mocked(getChatSession).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveChatLookup = resolve;
        }),
    );
    const selectChatSession = vi.fn(async () => true);
    const onChatNavigate = vi.fn();
    const onSelectWorkspace = vi.fn();
    const state = createRuntimeConsoleFixture();

    render(
      withRuntimeConsole(
        <ConsoleShell
          activeWorkspace="tasks"
          onChatNavigate={onChatNavigate}
          onSelectWorkspace={onSelectWorkspace}
          taskNavigation={{ taskID: "task_1", runID: "run_1" }}
        />,
        { state, actions: { ...createRuntimeConsoleActions(), selectChatSession } },
      ),
    );

    fireEvent.click(
      await screen.findByRole("button", { name: "Open linked Task chat" }, { timeout: 30_000 }),
    );
    await waitFor(() => expect(getChatSession).toHaveBeenCalledWith("chat_task"));
    fireEvent.click(screen.getByRole("link", { name: "Connections" }));
    expect(onSelectWorkspace).toHaveBeenCalledWith("connections");
    await act(async () =>
      resolveChatLookup?.({
        object: "chat_session",
        data: {
          id: "chat_task",
          agent_id: "hecate",
          title: "Task chat",
          status: "completed",
          workspace: "",
          messages: [],
        },
      }),
    );

    await waitFor(() => expect(selectChatSession).not.toHaveBeenCalled());
    expect(onChatNavigate).not.toHaveBeenCalled();
  });

  it("drops an in-flight Task-to-Chat lookup after parent-driven route navigation", async () => {
    let resolveChatLookup:
      | ((value: Awaited<ReturnType<typeof getChatSession>>) => void)
      | undefined;
    vi.mocked(getChatSession).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveChatLookup = resolve;
        }),
    );
    const selectChatSession = vi.fn(async () => true);
    const onChatNavigate = vi.fn();
    const state = createRuntimeConsoleFixture();
    const actions = { ...createRuntimeConsoleActions(), selectChatSession };
    const view = render(
      withRuntimeConsole(
        <ConsoleShell
          activeWorkspace="tasks"
          onChatNavigate={onChatNavigate}
          onSelectWorkspace={() => {}}
          taskNavigation={{ taskID: "task_1", runID: "run_1" }}
        />,
        { state, actions },
      ),
    );

    fireEvent.click(
      await screen.findByRole("button", { name: "Open linked Task chat" }, { timeout: 30_000 }),
    );
    await waitFor(() => expect(getChatSession).toHaveBeenCalledWith("chat_task"));

    view.rerender(
      withRuntimeConsole(
        <ConsoleShell
          activeWorkspace="tasks"
          onChatNavigate={onChatNavigate}
          onSelectWorkspace={() => {}}
          taskNavigation={{ taskID: "task_2", runID: "run_2" }}
        />,
        { state, actions },
      ),
    );
    await waitFor(() =>
      expect(tasksViewProbe.render).toHaveBeenLastCalledWith(
        expect.objectContaining({ taskID: "task_2", runID: "run_2" }),
      ),
    );
    await act(async () =>
      resolveChatLookup?.({
        object: "chat_session",
        data: {
          id: "chat_task",
          agent_id: "hecate",
          title: "Task chat",
          status: "completed",
          workspace: "",
          messages: [],
        },
      }),
    );

    await waitFor(() => expect(selectChatSession).not.toHaveBeenCalled());
    expect(onChatNavigate).not.toHaveBeenCalled();
  });

  it("keeps routed Task focus stable when its navigation object is recreated", async () => {
    const state = createRuntimeConsoleFixture();
    const actions = createRuntimeConsoleActions();
    const view = render(
      withRuntimeConsole(
        <ConsoleShell
          activeWorkspace="tasks"
          onSelectWorkspace={() => {}}
          taskNavigation={{ taskID: "task_1", runID: "run_1" }}
        />,
        { state, actions },
      ),
    );

    await screen.findByRole("button", { name: "Open linked Task chat" }, { timeout: 30_000 });
    const firstFocus = tasksViewProbe.render.mock.lastCall?.[0];
    expect(firstFocus).toEqual(expect.objectContaining({ taskID: "task_1", runID: "run_1" }));

    view.rerender(
      withRuntimeConsole(
        <ConsoleShell
          activeWorkspace="tasks"
          onSelectWorkspace={() => {}}
          taskNavigation={{ taskID: "task_1", runID: "run_1" }}
        />,
        { state, actions },
      ),
    );

    await waitFor(() => expect(tasksViewProbe.render.mock.lastCall?.[0]).toBe(firstFocus));
  });

  it("replaces a stale Chat URL with the current selection", async () => {
    const selectChatSession = vi.fn(async () => false);
    const onChatNavigate = vi.fn();
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "chat_current",
      activeChatSession: {
        id: "chat_current",
        title: "Current chat",
        agent_id: "hecate",
        status: "idle",
        messages: [],
      } as any,
    });

    render(
      withRuntimeConsole(
        <ConsoleShell
          activeWorkspace="chats"
          chatNavigation={{ chatID: "chat_missing", messageID: null }}
          onChatNavigate={onChatNavigate}
          onSelectWorkspace={() => {}}
        />,
        { state, actions: { ...createRuntimeConsoleActions(), selectChatSession } },
      ),
    );

    await waitFor(() =>
      expect(onChatNavigate).toHaveBeenCalledWith(
        { chatID: "chat_current", messageID: null },
        "replace",
      ),
    );
  });

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
    // (workspace navigation links, statusbar) is not lazy and can
    // still be queried synchronously.
    expect(screen.getByRole("link", { name: "Chats" })).toHaveAttribute("href", "/chats");
    expect(
      await screen.findByText(/Connect a model or agent/i, undefined, { timeout: 30_000 }),
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

  it("lets the chat sidebar switch back to No project", async () => {
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

    await waitFor(() => expect(selectProject).toHaveBeenCalledWith(""));
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

  it("opens the linked project from an active chat header", async () => {
    const selectProject = vi.fn(async () => undefined);
    const onSelectWorkspace = vi.fn();
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
      activeChatSessionID: "chat_project",
      activeChatSession: {
        id: "chat_project",
        title: "Project chat",
        project_id: "proj_1",
        agent_id: "hecate",
        status: "idle",
        messages: [],
        provider_calls: [],
      } as any,
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
      withRuntimeConsole(
        <ConsoleShell activeWorkspace="chats" onSelectWorkspace={onSelectWorkspace} />,
        {
          state,
          actions: { ...createRuntimeConsoleActions(), selectProject },
        },
      ),
    );

    fireEvent.click(await screen.findByRole("button", { name: "Open project: Hecate" }));

    expect(selectProject).toHaveBeenCalledWith("proj_1");
    expect(onSelectWorkspace).toHaveBeenCalledWith("projects");
  });

  it("creates new chats inside the selected project scope", async () => {
    const createChatSession = vi.fn<RuntimeConsoleFixtureActions["createChatSession"]>(
      async () => undefined,
    );
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

    await waitFor(() =>
      expect(createChatSession).toHaveBeenCalledWith({
        agentID: "hecate",
        projectID: "proj_1",
      }),
    );
  });

  it("forces Hecate chats when opening chat from a project assignment", async () => {
    const createChatSession = vi.fn<RuntimeConsoleFixtureActions["createChatSession"]>(
      async () => undefined,
    );
    const onSelectWorkspace = vi.fn();
    const project = {
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
      default_provider: "ollama",
      default_model: "qwen2.5-coder",
      created_at: "2026-05-21T10:00:00Z",
      updated_at: "2026-05-21T10:00:00Z",
    };
    const workItem: ProjectWorkItemRecord = {
      id: "work_1",
      project_id: project.id,
      title: "Build cockpit UI",
      brief: "Open a model chat from this assignment.",
      status: "ready",
      priority: "high",
      owner_role_id: "software_developer",
      created_at: "2026-06-02T10:00:00Z",
      updated_at: "2026-06-02T11:00:00Z",
    };
    const assignment: ProjectAssignmentRecord = {
      id: "asgn_1",
      project_id: project.id,
      work_item_id: workItem.id,
      role_id: "software_developer",
      driver_kind: "hecate_task",
      status: "queued",
      created_at: "2026-06-02T10:00:00Z",
      updated_at: "2026-06-02T11:00:00Z",
      execution: {
        status: "queued",
        provider: "ollama",
        model: "qwen2.5-coder",
      },
    };
    vi.mocked(getProjectWorkRoles).mockResolvedValue({
      object: "project_roles",
      data: [
        {
          id: "software_developer",
          project_id: project.id,
          name: "Software developer",
          description: "Owns implementation work.",
          instructions: "Keep changes reviewable.",
          default_driver_kind: "hecate_task",
          default_provider: "anthropic",
          default_model: "claude-sonnet-4",
          default_agent_profile: "implementation",
          built_in: true,
        },
      ],
    });
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...workItem, assignments: [assignment] }],
    });
    vi.mocked(getProjectWorkItem).mockResolvedValue({
      object: "project_work_item",
      data: workItem,
    });
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [assignment],
    });
    const state = createRuntimeConsoleFixture({
      chatTarget: "external_agent",
      defaultChatTarget: "external_agent",
      projects: [project],
      activeProjectID: project.id,
    });
    render(
      withRuntimeConsole(
        <ConsoleShell activeWorkspace="projects" onSelectWorkspace={onSelectWorkspace} />,
        {
          state,
          actions: { ...createRuntimeConsoleActions(), createChatSession },
        },
      ),
    );

    fireEvent.click(await screen.findByRole("tab", { name: /Work/ }, { timeout: 30_000 }));
    fireEvent.click(await screen.findByText("Execution details"));
    fireEvent.click(await screen.findByRole("button", { name: "Start related chat" }));

    expect(createChatSession).toHaveBeenCalledWith(
      expect.objectContaining({
        agentID: "hecate",
        projectID: project.id,
        provider: "ollama",
        model: "qwen2.5-coder",
        title: "Build cockpit UI - Software developer",
        reuseEmptyDraft: true,
      }),
    );
    const request = createChatSession.mock.calls[0]?.[0];
    expect(request?.draft).toContain("Launch context");
    expect(request?.draft).toContain("Project: Hecate (proj_1)");
    expect(request?.draft).toContain("- Brief: Open a model chat from this assignment.");
    expect(request?.draft).toContain("- Name: Software developer");
    expect(request?.draft).toContain("- Provider: ollama");
    expect(request?.draft).toContain("- Model: qwen2.5-coder");
    expect(request?.draft).toContain(
      "Role defaults: driver=hecate_task, provider=anthropic, model=claude-sonnet-4, preset=implementation",
    );
    expect(onSelectWorkspace).toHaveBeenCalledWith("chats");
  });

  it("blocks a second project chat before changing its project or route", async () => {
    const createChatSession = vi.fn<RuntimeConsoleFixtureActions["createChatSession"]>(
      async () => undefined,
    );
    const selectProject = vi.fn<RuntimeConsoleFixtureActions["selectProject"]>(
      async () => undefined,
    );
    const setProviderFilter = vi.fn<RuntimeConsoleFixtureActions["setProviderFilter"]>();
    const setModel = vi.fn<RuntimeConsoleFixtureActions["setModel"]>();
    const onSelectWorkspace = vi.fn();
    const project = {
      id: "proj_guarded",
      name: "Guarded project",
      roots: [],
      created_at: "2026-05-21T10:00:00Z",
      updated_at: "2026-05-21T10:00:00Z",
    };
    const workItem: ProjectWorkItemRecord = {
      id: "work_guarded",
      project_id: project.id,
      title: "Guard the launch",
      brief: "Keep the first chat creation authoritative.",
      status: "ready",
      priority: "high",
      owner_role_id: "operator",
      created_at: "2026-06-02T10:00:00Z",
      updated_at: "2026-06-02T11:00:00Z",
    };
    const assignment: ProjectAssignmentRecord = {
      id: "asgn_guarded",
      project_id: project.id,
      work_item_id: workItem.id,
      role_id: "operator",
      driver_kind: "hecate_task",
      status: "queued",
      created_at: "2026-06-02T10:00:00Z",
      updated_at: "2026-06-02T11:00:00Z",
      execution: {
        status: "queued",
        provider: "ollama",
        model: "qwen2.5-coder",
      },
    };
    vi.mocked(getProjectWorkRoles).mockResolvedValue({
      object: "project_roles",
      data: [
        {
          id: "operator",
          project_id: project.id,
          name: "Operator",
          built_in: true,
        },
      ],
    });
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...workItem, assignments: [assignment] }],
    });
    vi.mocked(getProjectWorkItem).mockResolvedValue({
      object: "project_work_item",
      data: workItem,
    });
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [assignment],
    });
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
      chatCreating: true,
    });
    render(
      withRuntimeConsole(
        <ConsoleShell activeWorkspace="projects" onSelectWorkspace={onSelectWorkspace} />,
        {
          state,
          actions: {
            ...createRuntimeConsoleActions(),
            createChatSession,
            selectProject,
            setProviderFilter,
            setModel,
          },
        },
      ),
    );

    fireEvent.click(await screen.findByRole("tab", { name: /Work/ }));
    fireEvent.click(await screen.findByText("Execution details"));
    fireEvent.click(await screen.findByRole("button", { name: "Start related chat" }));

    expect(createChatSession).not.toHaveBeenCalled();
    expect(selectProject).not.toHaveBeenCalled();
    expect(setProviderFilter).not.toHaveBeenCalled();
    expect(setModel).not.toHaveBeenCalled();
    expect(onSelectWorkspace).toHaveBeenCalledWith("chats");
    expect(
      await screen.findByText(
        "A chat is already starting. Wait for it to finish before opening another.",
      ),
    ).toBeInTheDocument();
  });

  async function openLinkedExternalAgentChat(
    selected: boolean | Promise<boolean>,
    pendingImageDraft = false,
    showLateDraftControl = false,
  ) {
    const createChatSession = vi.fn<RuntimeConsoleFixtureActions["createChatSession"]>(
      async () => undefined,
    );
    const selectChatSession = vi.fn<RuntimeConsoleFixtureActions["selectChatSession"]>(
      async () => selected,
    );
    const setMessage = vi.fn<RuntimeConsoleFixtureActions["setMessage"]>(() => undefined);
    const selectProject = vi.fn<RuntimeConsoleFixtureActions["selectProject"]>(
      async () => undefined,
    );
    const onSelectWorkspace = vi.fn();
    const project = {
      id: "proj_1",
      name: "Hecate",
      roots: [],
      created_at: "2026-05-21T10:00:00Z",
      updated_at: "2026-05-21T10:00:00Z",
    };
    const workItem: ProjectWorkItemRecord = {
      id: "work_1",
      project_id: project.id,
      title: "Build cockpit UI",
      brief: "Open the linked external agent chat.",
      status: "ready",
      priority: "high",
      owner_role_id: "software_developer",
      created_at: "2026-06-02T10:00:00Z",
      updated_at: "2026-06-02T11:00:00Z",
    };
    const assignment: ProjectAssignmentRecord = {
      id: "asgn_external",
      project_id: project.id,
      work_item_id: workItem.id,
      role_id: "software_developer",
      driver_kind: "external_agent",
      status: "running",
      execution_ref: {
        kind: "chat_session",
        chat_session_id: "chat_external_1",
        context_snapshot_id: "ctx_external_1",
        status: "running",
      },
      created_at: "2026-06-02T10:00:00Z",
      updated_at: "2026-06-02T11:00:00Z",
    };
    vi.mocked(getProjectWorkRoles).mockResolvedValue({
      object: "project_roles",
      data: [
        {
          id: "software_developer",
          project_id: project.id,
          name: "Software developer",
          built_in: true,
        },
      ],
    });
    vi.mocked(getProjectWorkItems).mockResolvedValue({
      object: "project_work_items",
      data: [{ ...workItem, assignments: [assignment] }],
    });
    vi.mocked(getProjectWorkItem).mockResolvedValue({
      object: "project_work_item",
      data: workItem,
    });
    vi.mocked(getProjectAssignments).mockResolvedValue({
      object: "project_assignments",
      data: [assignment],
    });
    const state = createRuntimeConsoleFixture({
      projects: [project],
      activeProjectID: project.id,
      pendingChatAttachments: pendingImageDraft
        ? [
            {
              id: "draft-1",
              file: new File(["image"], "map.png", { type: "image/png" }),
            },
          ]
        : [],
      chatCreating: true,
    });
    render(
      withRuntimeConsole(
        <>
          {showLateDraftControl && <LateImageDraftControl />}
          <ConsoleShell activeWorkspace="projects" onSelectWorkspace={onSelectWorkspace} />
        </>,
        {
          state,
          actions: {
            ...createRuntimeConsoleActions(),
            createChatSession,
            selectProject,
            selectChatSession,
            setMessage,
          },
        },
      ),
    );

    fireEvent.click(await screen.findByRole("tab", { name: /Work/ }));
    selectProject.mockClear();
    fireEvent.click(await screen.findByRole("button", { name: "Continue in chat" }));

    return {
      createChatSession,
      onSelectWorkspace,
      selectChatSession,
      selectProject,
      setMessage,
    };
  }

  it("selects linked External Agent chats without replacing an unsent draft", async () => {
    const { createChatSession, onSelectWorkspace, selectChatSession, setMessage } =
      await openLinkedExternalAgentChat(true);

    expect(selectChatSession).toHaveBeenCalledWith(
      "chat_external_1",
      expect.objectContaining({ draft: expect.stringContaining("Launch context") }),
    );
    expect(createChatSession).not.toHaveBeenCalled();
    await waitFor(() => expect(onSelectWorkspace).toHaveBeenCalledWith("chats"));
    expect(setMessage).not.toHaveBeenCalled();
  });

  it("selects linked External Agent chats without seeding a draft when selection fails", async () => {
    const { createChatSession, onSelectWorkspace, selectChatSession, setMessage } =
      await openLinkedExternalAgentChat(false);

    await waitFor(() => expect(selectChatSession).toHaveBeenCalledTimes(1));
    expect(createChatSession).not.toHaveBeenCalled();
    expect(onSelectWorkspace).not.toHaveBeenCalled();
    expect(setMessage).not.toHaveBeenCalled();
  });

  it("does not commit Projects-to-chat navigation when an image draft appears during selection", async () => {
    let resolveSelection: ((selected: boolean) => void) | undefined;
    const selection = new Promise<boolean>((resolve) => {
      resolveSelection = resolve;
    });
    const { onSelectWorkspace, selectChatSession, selectProject, setMessage } =
      await openLinkedExternalAgentChat(selection, false, true);
    await waitFor(() => expect(selectChatSession).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByRole("button", { name: "Attach late navigation image" }));
    expect(screen.getByRole("status", { name: "Late navigation image count" })).toHaveTextContent(
      "1",
    );
    await act(async () => resolveSelection?.(true));

    await waitFor(() => expect(onSelectWorkspace).not.toHaveBeenCalled());
    expect(selectProject).not.toHaveBeenCalled();
    expect(setMessage).not.toHaveBeenCalled();
    expect(
      await screen.findByText("Remove attached files before opening a chat from Projects."),
    ).toBeVisible();
  });

  it("keeps image drafts scoped when Projects tries to open another chat", async () => {
    const { createChatSession, onSelectWorkspace, selectChatSession, selectProject, setMessage } =
      await openLinkedExternalAgentChat(true, true);

    expect(selectProject).not.toHaveBeenCalled();
    expect(selectChatSession).not.toHaveBeenCalled();
    expect(createChatSession).not.toHaveBeenCalled();
    expect(setMessage).not.toHaveBeenCalled();
    expect(onSelectWorkspace).not.toHaveBeenCalled();
    expect(
      await screen.findByText("Remove attached files before opening a chat from Projects."),
    ).toBeVisible();
  });

  it("moves the active chat when selecting a different project", async () => {
    const selectProject = vi.fn(async () => undefined);
    const selectChatSession = vi.fn(async () => true);
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

    await waitFor(() => expect(selectChatSession).toHaveBeenCalledWith("chat_project"));
    expect(selectProject).toHaveBeenCalledWith("proj_1");
  });

  it("keeps project scope and workspace unchanged when a late image draft rejects selection", async () => {
    let resolveSelection: ((selected: boolean) => void) | undefined;
    const selectChatSession = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          resolveSelection = resolve;
        }),
    );
    const selectProject = vi.fn(async () => undefined);
    const setAgentWorkspace = vi.fn();
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
          workspace: "/workspace/hecate",
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
      withRuntimeConsole(
        <>
          <LateImageDraftControl />
          <ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />
        </>,
        {
          state,
          actions: {
            ...createRuntimeConsoleActions(),
            selectChatSession,
            selectProject,
            setAgentWorkspace,
          },
        },
      ),
    );

    fireEvent.click(screen.getByRole("button", { name: /Expand projects/i }));
    fireEvent.click(screen.getByRole("button", { name: /Project Hecate/i }));
    await waitFor(() => expect(selectChatSession).toHaveBeenCalledWith("chat_project"));
    expect(selectProject).not.toHaveBeenCalled();
    expect(setAgentWorkspace).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Attach late navigation image" }));
    await act(async () => resolveSelection?.(true));

    await waitFor(() => expect(selectProject).not.toHaveBeenCalled());
    expect(setAgentWorkspace).not.toHaveBeenCalled();
    expect(screen.getByRole("status", { name: "Late navigation image count" })).toHaveTextContent(
      "1",
    );
    expect(
      await screen.findByText("Remove attached files before changing or deleting chat ownership."),
    ).toBeVisible();
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
    const deleteProject = vi.fn(async () => projectDeleteResult);
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
    expect(
      await screen.findByText(
        "Deleted Hecate. Cleaned up 1 chat, 2 work rows, 1 skill, 3 memory entries, 4 memory candidates.",
      ),
    ).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.queryByText("Project chat")).toBeNull();
    });
  });

  it("keeps a pending project deletion modal non-dismissible and single-flight", async () => {
    let finishDelete: ((value: typeof projectDeleteResult) => void) | undefined;
    const deleteProject = vi.fn(
      () =>
        new Promise<typeof projectDeleteResult>((resolve) => {
          finishDelete = resolve;
        }),
    );
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

    await waitFor(() => expect(deleteProject).toHaveBeenCalledTimes(1));
    const dialog = screen.getByRole("dialog", { name: "Delete project" });
    const pendingButton = screen.getByRole("button", { name: "Working…" });
    expect(pendingButton).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.click(dialog.parentElement as HTMLElement);
    fireEvent.click(pendingButton);
    expect(deleteProject).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("dialog", { name: "Delete project" })).toBe(dialog);

    await act(async () => finishDelete?.(projectDeleteResult));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Delete project" })).toBeNull(),
    );
  });

  it("keeps project chats visible when project deletion fails", async () => {
    const deleteProject = vi.fn(async () => null);
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
    const deleteChatSession = vi.fn(async () => true);
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

    await waitFor(() => expect(deleteChatSession).toHaveBeenCalledWith("chat_1"));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Delete chat" })).toBeNull());
    expect(screen.getByRole("textbox", { name: "Search chats" })).toHaveFocus();
  });

  it("keeps a failed chat deletion open and allows a successful retry", async () => {
    const deleteChatSession = vi
      .fn<(id: string) => Promise<boolean>>()
      .mockResolvedValueOnce(false)
      .mockResolvedValueOnce(true);
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
    fireEvent.click(screen.getByRole("button", { name: /^Delete chat$/i }));

    await waitFor(() => expect(deleteChatSession).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("dialog", { name: "Delete chat" })).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Delete chat$/i })).toBeEnabled(),
    );
    expect(screen.getByRole("button", { name: /^Delete chat$/i })).toHaveFocus();
    expect(screen.getByRole("button", { name: "Close" })).toBeEnabled();

    fireEvent.click(screen.getByRole("button", { name: /^Delete chat$/i }));
    await waitFor(() => expect(deleteChatSession).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Delete chat" })).toBeNull());
    expect(screen.getByRole("textbox", { name: "Search chats" })).toHaveFocus();
  });

  it("keeps a pending chat deletion modal non-dismissible and prevents repeat confirmation", async () => {
    let finishDelete: ((deleted: boolean) => void) | undefined;
    const deleteChatSession = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          finishDelete = resolve;
        }),
    );
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
      withRuntimeConsole(
        <>
          <ConsoleShell activeWorkspace="chats" onSelectWorkspace={() => {}} />
          <RemoveChatSessionControl chatID="chat_1" />
        </>,
        {
          state,
          actions: { ...createRuntimeConsoleActions(), deleteChatSession },
        },
      ),
    );

    const chatRow = await screen.findByLabelText("Chat Cleanup chat, Hecate");
    fireEvent.mouseEnter(chatRow);
    const deleteButton = screen.getByRole("button", { name: /Delete chat Cleanup chat/i });
    deleteButton.focus();
    fireEvent.click(deleteButton);
    fireEvent.click(screen.getByRole("button", { name: /^Delete chat$/i }));

    await waitFor(() => expect(deleteChatSession).toHaveBeenCalledTimes(1));
    const dialog = screen.getByRole("dialog", { name: "Delete chat" });
    const pendingButton = screen.getByRole("button", { name: "Working…" });
    expect(pendingButton).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.click(dialog.parentElement as HTMLElement);
    fireEvent.click(pendingButton);
    expect(deleteChatSession).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("dialog", { name: "Delete chat" })).toBe(dialog);

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Remove deleted chat from state" }));
      finishDelete?.(true);
    });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Delete chat" })).toBeNull());
    expect(screen.getByRole("textbox", { name: "Search chats" })).toHaveFocus();
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

  it("labels raw dev builds clearly", () => {
    renderWorkspace({ status: "ok", time: "2026-04-25T00:00:00Z", version: "dev" });

    const statusbar = document.querySelector(".hecate-statusbar");
    expect(statusbar).not.toBeNull();
    expect(statusbar!.textContent).toContain("source build");
    expect(statusbar!.textContent).not.toContain("|dev|");
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

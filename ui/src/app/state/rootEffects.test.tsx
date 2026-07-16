import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useChat, type ChatCancellationOwner, type ChatStopFence } from "./chat";
import { RootEffects } from "./rootEffects";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";
import type { AgentAdapterRecord } from "../../types/agent-adapter";
import type { ProjectCatalogLoadOptions, ProjectCatalogLoadResult } from "./projects";

function adapter(id: string, overrides: Partial<AgentAdapterRecord> = {}): AgentAdapterRecord {
  return {
    id,
    name: id,
    kind: "acp",
    command: `${id}-acp`,
    available: true,
    status: "available",
    cost_mode: "external",
    supports_authenticate: false,
    supports_logout: false,
    ...overrides,
  };
}

function ChatOwnershipStatus() {
  const chat = useChat();
  return (
    <output aria-label="Chat ownership status">
      {chat.actions.chatOwnershipMutationBlockReason() || "clear"}
    </output>
  );
}

function ChatCancellationHarness() {
  const chat = useChat();
  const ownerRef = useRef<ChatCancellationOwner | null>(null);
  return (
    <>
      <button
        type="button"
        onClick={() => {
          ownerRef.current = chat.actions.beginChatCancellation("chat_a");
        }}
      >
        Acquire cancellation
      </button>
      <button type="button" onClick={() => chat.actions.setChatLoading(false)}>
        Clear loading
      </button>
      <button
        type="button"
        onClick={() => {
          if (ownerRef.current) chat.actions.finishChatCancellation(ownerRef.current);
        }}
      >
        Release cancellation
      </button>
      <output aria-label="Cancellation projection">
        {chat.state.chatCancelling ? "cancelling" : "idle"}
      </output>
    </>
  );
}

function ChatStopFenceHarness() {
  const chat = useChat();
  const fenceRef = useRef<ChatStopFence | null>(null);
  const session = (status: "running" | "cancelled") => ({
    id: "chat_a",
    title: "Chat A",
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
  return (
    <>
      <button
        type="button"
        onClick={() => {
          const fence = chat.actions.beginChatStopFence({
            token: 31,
            sessionID: "chat_a",
            turnGeneration: 41,
          });
          chat.actions.acceptChatStopFence(fence);
          fenceRef.current = fence;
        }}
      >
        Fence chat A
      </button>
      <button
        type="button"
        onClick={() => {
          chat.actions.setActiveChatSessionID("chat_a");
          chat.actions.setActiveChatSession(session("running"));
        }}
      >
        Select chat A
      </button>
      <button
        type="button"
        onClick={() => {
          const fence = fenceRef.current;
          if (fence) {
            chat.actions.chatStopFenceAllowsSnapshot(session("cancelled"), {
              kind: "turn",
              turnGeneration: 41,
            });
            chat.actions.clearChatStopFence(fence);
            fenceRef.current = null;
          }
          chat.actions.setActiveChatSessionID("");
          chat.actions.setActiveChatSession(null);
        }}
      >
        Settle Stop and clear selection
      </button>
      <output aria-label="Selected chat">{chat.state.activeChatSessionID || "none"}</output>
    </>
  );
}

describe("RootEffects", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("does not infer cancellation ownership from the loading projection", async () => {
    const actions = {
      ...createRuntimeConsoleActions(),
      loadDashboard: vi.fn(async () => undefined),
      loadProjects: vi.fn(async () => ({ status: "applied" as const })),
    };
    const state = createRuntimeConsoleFixture({ chatLoading: true });

    render(
      withRuntimeConsole(
        <>
          <RootEffects />
          <ChatCancellationHarness />
        </>,
        { state, actions },
      ),
    );

    fireEvent.click(screen.getByRole("button", { name: "Acquire cancellation" }));
    expect(screen.getByLabelText("Cancellation projection")).toHaveTextContent("cancelling");

    fireEvent.click(screen.getByRole("button", { name: "Clear loading" }));
    await waitFor(() =>
      expect(screen.getByLabelText("Cancellation projection")).toHaveTextContent("cancelling"),
    );

    fireEvent.click(screen.getByRole("button", { name: "Release cancellation" }));
    expect(screen.getByLabelText("Cancellation projection")).toHaveTextContent("idle");
  });

  it("scrubs an ownerless cancellation projection restored by a fixture", async () => {
    const actions = {
      ...createRuntimeConsoleActions(),
      loadDashboard: vi.fn(async () => undefined),
      loadProjects: vi.fn(async () => ({ status: "applied" as const })),
    };
    const state = createRuntimeConsoleFixture({
      chatCancelling: true,
      chatCancellingSessionID: "chat_a",
      chatCancellingTurnKind: "direct_model",
    });

    render(
      withRuntimeConsole(
        <>
          <RootEffects />
          <ChatCancellationHarness />
        </>,
        { state, actions },
      ),
    );

    await waitFor(() =>
      expect(screen.getByLabelText("Cancellation projection")).toHaveTextContent("idle"),
    );
  });

  it("does not restore pending approvals while a shared accepted Stop fence owns the session", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).includes("/hecate/v1/chat/sessions/chat_a/approvals")) {
        return new Response(JSON.stringify({ object: "chat_approvals", data: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(null, { status: 404 });
    });
    vi.stubGlobal("fetch", fetchMock);
    const actions = {
      ...createRuntimeConsoleActions(),
      loadDashboard: vi.fn(async () => undefined),
      loadProjects: vi.fn(async () => ({ status: "applied" as const })),
    };

    render(
      withRuntimeConsole(
        <>
          <RootEffects />
          <ChatStopFenceHarness />
        </>,
        { state: createRuntimeConsoleFixture(), actions },
      ),
    );

    await waitFor(() => expect(actions.loadDashboard).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("button", { name: "Fence chat A" }));
    fireEvent.click(screen.getByRole("button", { name: "Select chat A" }));
    await waitFor(() => expect(screen.getByLabelText("Selected chat")).toHaveTextContent("chat_a"));
    await act(async () => {
      await Promise.resolve();
    });
    expect(
      fetchMock.mock.calls.filter(([input]) =>
        String(input).includes("/hecate/v1/chat/sessions/chat_a/approvals"),
      ),
    ).toHaveLength(0);

    fireEvent.click(screen.getByRole("button", { name: "Settle Stop and clear selection" }));
    await waitFor(() => expect(screen.getByLabelText("Selected chat")).toHaveTextContent("none"));
    fireEvent.click(screen.getByRole("button", { name: "Select chat A" }));

    await waitFor(() =>
      expect(
        fetchMock.mock.calls.filter(([input]) =>
          String(input).includes("/hecate/v1/chat/sessions/chat_a/approvals"),
        ),
      ).toHaveLength(1),
    );
  });

  it("does not actively probe agent adapters during quiet startup", async () => {
    const probeAgentAdapter = vi.fn(async () => null);
    const state = createRuntimeConsoleFixture({
      agentAdapters: [
        adapter("codex"),
        adapter("claude_code"),
        adapter("cursor_agent"),
        adapter("grok_build"),
      ],
    });
    const actions = {
      ...createRuntimeConsoleActions(),
      probeAgentAdapter,
      loadDashboard: vi.fn(async () => undefined),
      loadProjects: vi.fn(async () => ({ status: "applied" as const })),
    };

    render(withRuntimeConsole(<RootEffects />, { state, actions }));

    await waitFor(() => expect(actions.loadDashboard).toHaveBeenCalled());
    expect(probeAgentAdapter).not.toHaveBeenCalled();
  });

  it("keeps passive adapter arrivals from starting ACP sessions", async () => {
    const probeAgentAdapter = vi.fn(async () => null);
    const actions = {
      ...createRuntimeConsoleActions(),
      probeAgentAdapter,
      loadDashboard: vi.fn(async () => undefined),
      loadProjects: vi.fn(async () => ({ status: "applied" as const })),
    };
    const initial = createRuntimeConsoleFixture({ agentAdapters: [] });
    const { rerender } = render(withRuntimeConsole(<RootEffects />, { state: initial, actions }));

    const next = createRuntimeConsoleFixture({
      agentAdapters: [adapter("cursor_agent"), adapter("grok_build")],
    });
    rerender(withRuntimeConsole(<RootEffects />, { state: next, actions }));

    await waitFor(() => expect(actions.loadDashboard).toHaveBeenCalled());
    expect(probeAgentAdapter).not.toHaveBeenCalled();
  });

  it("retries mount catalog hydration after image ownership clears", async () => {
    let firstOptions: ProjectCatalogLoadOptions | undefined;
    let resolveFirstLoad!: (result: ProjectCatalogLoadResult) => void;
    const firstLoad = new Promise<ProjectCatalogLoadResult>((resolve) => {
      resolveFirstLoad = resolve;
    });
    let attempt = 0;
    const loadProjects = vi.fn((options?: ProjectCatalogLoadOptions) => {
      attempt += 1;
      if (attempt === 1) {
        firstOptions = options;
        return firstLoad;
      }
      return Promise.resolve<ProjectCatalogLoadResult>({ status: "applied" });
    });
    const actions = {
      ...createRuntimeConsoleActions(),
      loadDashboard: vi.fn(async () => undefined),
      loadProjects,
    };
    const initialState = createRuntimeConsoleFixture();
    const rendered = render(withRuntimeConsole(<RootEffects />, { state: initialState, actions }));

    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(1));
    const blockedState = {
      ...initialState,
      pendingChatAttachments: [
        {
          id: "mount-catalog-image",
          file: new File(["image"], "mount.png", { type: "image/png" }),
        },
      ],
    };
    rendered.rerender(withRuntimeConsole(<RootEffects />, { state: blockedState, actions }));
    await waitFor(() => expect(firstOptions?.shouldApply?.()).toBe(false));

    await act(async () => {
      resolveFirstLoad({ status: "superseded" });
      await firstLoad;
    });
    expect(loadProjects).toHaveBeenCalledTimes(1);

    rendered.rerender(
      withRuntimeConsole(<RootEffects />, {
        state: { ...blockedState, pendingChatAttachments: [] },
        actions,
      }),
    );
    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(2));
    const retryOptions = loadProjects.mock.calls[1]?.[0];
    expect(retryOptions?.shouldApply?.()).toBe(true);
  });

  it("retries when image ownership clears after apply denial but before continuation", async () => {
    let firstOptions: ProjectCatalogLoadOptions | undefined;
    let resolveFirstLoad!: (result: ProjectCatalogLoadResult) => void;
    const firstLoad = new Promise<ProjectCatalogLoadResult>((resolve) => {
      resolveFirstLoad = resolve;
    });
    let attempt = 0;
    const loadProjects = vi.fn((options?: ProjectCatalogLoadOptions) => {
      attempt += 1;
      if (attempt <= 2) {
        firstOptions = options;
        return firstLoad;
      }
      return Promise.resolve<ProjectCatalogLoadResult>({ status: "applied" });
    });
    const actions = {
      ...createRuntimeConsoleActions(),
      loadDashboard: vi.fn(async () => undefined),
      loadProjects,
    };
    const blockedState = createRuntimeConsoleFixture({
      pendingChatAttachments: [
        {
          id: "mount-catalog-image",
          file: new File(["image"], "mount.png", { type: "image/png" }),
        },
      ],
    });
    const rendered = render(
      withRuntimeConsole(
        <>
          <RootEffects />
          <ChatOwnershipStatus />
        </>,
        { state: blockedState, actions },
      ),
    );

    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(1));
    expect(firstOptions?.shouldApply?.()).toBe(false);
    rendered.rerender(
      withRuntimeConsole(
        <>
          <RootEffects />
          <ChatOwnershipStatus />
        </>,
        { state: { ...blockedState, pendingChatAttachments: [] }, actions },
      ),
    );
    await waitFor(() =>
      expect(screen.getByLabelText("Chat ownership status")).toHaveTextContent("clear"),
    );
    expect(loadProjects).toHaveBeenCalledTimes(2);

    await act(async () => {
      resolveFirstLoad({ status: "superseded" });
      await firstLoad;
    });
    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(3));
  });

  it("coalesces an ownership-clear wakeup received during recovery into one fresh read", async () => {
    let resolveRecovery!: (result: ProjectCatalogLoadResult) => void;
    const pendingRecovery = new Promise<ProjectCatalogLoadResult>((resolve) => {
      resolveRecovery = resolve;
    });
    let attempt = 0;
    const loadProjects = vi.fn(() => {
      attempt += 1;
      if (attempt === 2) return pendingRecovery;
      return Promise.resolve<ProjectCatalogLoadResult>({ status: "applied" });
    });
    const actions = {
      ...createRuntimeConsoleActions(),
      loadDashboard: vi.fn(async () => undefined),
      loadProjects,
    };
    const clearState = createRuntimeConsoleFixture();
    const blockedState = createRuntimeConsoleFixture({
      pendingChatAttachments: [
        {
          id: "recovery-wakeup-image",
          file: new File(["image"], "recovery.png", { type: "image/png" }),
        },
      ],
    });
    const rendered = render(withRuntimeConsole(<RootEffects />, { state: clearState, actions }));

    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(1));
    rendered.rerender(withRuntimeConsole(<RootEffects />, { state: blockedState, actions }));
    rendered.rerender(withRuntimeConsole(<RootEffects />, { state: clearState, actions }));
    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(2));

    rendered.rerender(withRuntimeConsole(<RootEffects />, { state: blockedState, actions }));
    rendered.rerender(withRuntimeConsole(<RootEffects />, { state: clearState, actions }));
    expect(loadProjects).toHaveBeenCalledTimes(2);

    await act(async () => {
      resolveRecovery({ status: "applied" });
      await pendingRecovery;
    });
    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(3));
  });

  it("bounds ownership recovery when apply authority remains superseded", async () => {
    let attempt = 0;
    const loadProjects = vi.fn(async (): Promise<ProjectCatalogLoadResult> => {
      attempt += 1;
      return attempt === 1 ? { status: "applied" } : { status: "superseded" };
    });
    const actions = {
      ...createRuntimeConsoleActions(),
      loadDashboard: vi.fn(async () => undefined),
      loadProjects,
    };
    const clearState = createRuntimeConsoleFixture();
    const blockedState = createRuntimeConsoleFixture({
      pendingChatAttachments: [
        {
          id: "bounded-recovery-image",
          file: new File(["image"], "bounded.png", { type: "image/png" }),
        },
      ],
    });
    const rendered = render(withRuntimeConsole(<RootEffects />, { state: clearState, actions }));

    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(1));
    rendered.rerender(withRuntimeConsole(<RootEffects />, { state: blockedState, actions }));
    rendered.rerender(withRuntimeConsole(<RootEffects />, { state: clearState, actions }));

    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(3));
    await act(async () => {
      await Promise.resolve();
    });
    expect(loadProjects).toHaveBeenCalledTimes(3);
  });

  it("drains a newer ownership-clear wakeup after the bounded retry is superseded", async () => {
    let resolveFirstRecovery!: (result: ProjectCatalogLoadResult) => void;
    let resolveBoundedRetry!: (result: ProjectCatalogLoadResult) => void;
    const firstRecovery = new Promise<ProjectCatalogLoadResult>((resolve) => {
      resolveFirstRecovery = resolve;
    });
    const boundedRetry = new Promise<ProjectCatalogLoadResult>((resolve) => {
      resolveBoundedRetry = resolve;
    });
    let attempt = 0;
    const loadProjects = vi.fn(() => {
      attempt += 1;
      if (attempt === 2) return firstRecovery;
      if (attempt === 3) return boundedRetry;
      return Promise.resolve<ProjectCatalogLoadResult>({ status: "applied" });
    });
    const actions = {
      ...createRuntimeConsoleActions(),
      loadDashboard: vi.fn(async () => undefined),
      loadProjects,
    };
    const clearState = createRuntimeConsoleFixture();
    const blockedState = createRuntimeConsoleFixture({
      pendingChatAttachments: [
        {
          id: "bounded-wakeup-image",
          file: new File(["image"], "bounded-wakeup.png", { type: "image/png" }),
        },
      ],
    });
    const rendered = render(withRuntimeConsole(<RootEffects />, { state: clearState, actions }));

    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(1));
    rendered.rerender(withRuntimeConsole(<RootEffects />, { state: blockedState, actions }));
    rendered.rerender(withRuntimeConsole(<RootEffects />, { state: clearState, actions }));
    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(2));

    await act(async () => {
      resolveFirstRecovery({ status: "superseded" });
      await firstRecovery;
    });
    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(3));

    rendered.rerender(withRuntimeConsole(<RootEffects />, { state: blockedState, actions }));
    rendered.rerender(withRuntimeConsole(<RootEffects />, { state: clearState, actions }));
    expect(loadProjects).toHaveBeenCalledTimes(3);

    await act(async () => {
      resolveBoundedRetry({ status: "superseded" });
      await boundedRetry;
    });
    await waitFor(() => expect(loadProjects).toHaveBeenCalledTimes(4));
  });
});

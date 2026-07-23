import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useApprovals } from "./approvals";
import { useChat, type ChatCancellationOwner, type ChatStopFence } from "./chat";
import { RootEffects } from "./rootEffects";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";
import type { AgentAdapterRecord } from "../../types/agent-adapter";
import type { ChatSessionRecord } from "../../types/chat";
import type { ProjectCatalogLoadOptions, ProjectCatalogLoadResult } from "./projects";

const sseEncoder = new TextEncoder();

function externalSession(
  status: "idle" | "running" | "awaiting_approval" | "completed" | "cancelled",
  content: string,
): ChatSessionRecord {
  return {
    id: "chat_external",
    title: "External chat",
    agent_id: "claude_code",
    status,
    workspace: "/workspace",
    messages: [
      {
        id: "assistant_1",
        role: "assistant",
        content,
        status,
      },
    ],
  };
}

function chatSessionSSEFrame(session: ChatSessionRecord): Uint8Array {
  return chatSSEFrame("session_update", { object: "chat_session", data: session });
}

function chatSSEFrame(event: string, payload: unknown): Uint8Array {
  return sseEncoder.encode(`event: ${event}\ndata: ${JSON.stringify(payload)}\n\n`);
}

function ExternalSessionObserverStatus() {
  const chat = useChat();
  const approvals = useApprovals();
  const session = chat.state.activeChatSession;
  const lastMessage = session?.messages?.at(-1);
  const pending = approvals.state.pendingBySessionID.get(session?.id ?? "") ?? [];
  return (
    <>
      <output aria-label="Observed session">
        {session ? `${session.status}:${lastMessage?.content ?? ""}` : "none"}
      </output>
      <output aria-label="Observed approvals">
        {pending.map((approval) => approval.approval_id).join(",") || "none"}
      </output>
      <output aria-label="Observed error">
        {chat.state.chatErrorStatus === null
          ? "none"
          : `${chat.state.chatErrorStatus}:${chat.state.chatError}`}
      </output>
      <output aria-label="Observed session list">
        {chat.state.chatSessions.map((candidate) => candidate.id).join(",") || "none"}
      </output>
      <output aria-label="Observed deletion">
        {chat.actions.isChatSessionDeleted("chat_external") ? "deleted" : "present"}
      </output>
    </>
  );
}

function ExternalStopObserverHarness() {
  const chat = useChat();
  return (
    <>
      <button
        type="button"
        onClick={() => {
          const fence = chat.actions.beginChatStopFence({
            token: 73,
            sessionID: "chat_external",
            turnGeneration: 41,
          });
          chat.actions.acceptChatStopFence(fence);
          chat.actions.setActiveChatSessionID("chat_external");
          chat.actions.setActiveChatSession(externalSession("running", "Stopping"));
        }}
      >
        Fence and select external chat
      </button>
      <ExternalSessionObserverStatus />
    </>
  );
}

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

  it("follows a selected busy external session through snapshots and approval events", async () => {
    let streamController: ReadableStreamDefaultController<Uint8Array> | null = null;
    let streamSignal: AbortSignal | null = null;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/hecate/v1/chat/sessions/chat_external/stream")) {
        streamSignal = init?.signal ?? null;
        return new Response(
          new ReadableStream<Uint8Array>({
            start(controller) {
              streamController = controller;
              streamSignal?.addEventListener(
                "abort",
                () => controller.error(new DOMException("Aborted", "AbortError")),
                { once: true },
              );
            },
          }),
          { status: 200, headers: { "Content-Type": "text/event-stream" } },
        );
      }
      if (url.includes("/hecate/v1/chat/sessions/chat_external/approvals")) {
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
          <ExternalSessionObserverStatus />
        </>,
        {
          state: createRuntimeConsoleFixture({
            activeChatSessionID: "chat_external",
            activeChatSession: externalSession("running", "Hydrated"),
            chatTarget: "external_agent",
          }),
          actions,
        },
      ),
    );

    await waitFor(() => expect(streamController).not.toBeNull());
    act(() => {
      streamController?.enqueue(chatSessionSSEFrame(externalSession("running", "Streaming")));
      streamController?.enqueue(
        chatSSEFrame("approval.requested", {
          approval_id: "approval_1",
          session_id: "chat_external",
          adapter_id: "claude_code",
          tool_kind: "shell",
          tool_name: "run",
          created_at: "2026-07-23T10:00:00Z",
          expires_at: "2026-07-23T10:05:00Z",
        }),
      );
    });

    await waitFor(() =>
      expect(screen.getByLabelText("Observed session")).toHaveTextContent("running:Streaming"),
    );
    expect(screen.getByLabelText("Observed approvals")).toHaveTextContent("approval_1");

    act(() => {
      streamController?.enqueue(
        chatSSEFrame("approval.resolved", {
          approval_id: "approval_1",
          session_id: "chat_external",
          status: "resolved",
          path: "operator",
        }),
      );
      streamController?.enqueue(chatSessionSSEFrame(externalSession("completed", "Done")));
    });

    await waitFor(() =>
      expect(screen.getByLabelText("Observed session")).toHaveTextContent("completed:Done"),
    );
    expect(screen.getByLabelText("Observed approvals")).toHaveTextContent("none");
    await waitFor(() => expect(streamSignal?.aborted).toBe(true));
    expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/cancel"))).toBe(false);
  });

  it("clears a pending approval when the terminal snapshot arrives without its resolved event", async () => {
    let streamController: ReadableStreamDefaultController<Uint8Array> | null = null;
    let streamSignal: AbortSignal | null = null;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/hecate/v1/chat/sessions/chat_external/stream")) {
        streamSignal = init?.signal ?? null;
        return new Response(
          new ReadableStream<Uint8Array>({
            start(controller) {
              streamController = controller;
              streamSignal?.addEventListener(
                "abort",
                () => controller.error(new DOMException("Aborted", "AbortError")),
                { once: true },
              );
            },
          }),
          { status: 200, headers: { "Content-Type": "text/event-stream" } },
        );
      }
      if (url.includes("/hecate/v1/chat/sessions/chat_external/approvals")) {
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
          <ExternalSessionObserverStatus />
        </>,
        {
          state: createRuntimeConsoleFixture({
            activeChatSessionID: "chat_external",
            activeChatSession: externalSession("running", "Hydrated"),
            chatTarget: "external_agent",
          }),
          actions,
        },
      ),
    );

    await waitFor(() => expect(streamController).not.toBeNull());
    act(() => {
      streamController?.enqueue(
        chatSSEFrame("approval.requested", {
          approval_id: "approval_without_resolved",
          session_id: "chat_external",
          adapter_id: "claude_code",
          tool_kind: "shell",
          tool_name: "run",
          created_at: "2026-07-23T10:00:00Z",
          expires_at: "2026-07-23T10:05:00Z",
        }),
      );
    });
    await waitFor(() =>
      expect(screen.getByLabelText("Observed approvals")).toHaveTextContent(
        "approval_without_resolved",
      ),
    );

    act(() => {
      streamController?.enqueue(chatSessionSSEFrame(externalSession("completed", "Done")));
    });

    await waitFor(() =>
      expect(screen.getByLabelText("Observed session")).toHaveTextContent("completed:Done"),
    );
    expect(screen.getByLabelText("Observed approvals")).toHaveTextContent("none");
    await waitFor(() => expect(streamSignal?.aborted).toBe(true));
  });

  it("does not open a second external-session stream while the local submit owns it", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).includes("/approvals")) {
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
      withRuntimeConsole(<RootEffects />, {
        state: createRuntimeConsoleFixture({
          activeChatSessionID: "chat_external",
          activeChatSession: externalSession("running", "Submitting"),
          chatTarget: "external_agent",
          chatTurnActive: true,
          chatTurnSessionID: "chat_external",
          chatTurnKind: "external_agent",
        }),
        actions,
      }),
    );

    await waitFor(() => expect(actions.loadDashboard).toHaveBeenCalled());
    expect(
      fetchMock.mock.calls.filter(([input]) => String(input).endsWith("/chat_external/stream")),
    ).toHaveLength(0);
  });

  it("follows an External Agent admission gap after the durable user row appears", async () => {
    let streamRequests = 0;
    const pendingSession: ChatSessionRecord = {
      id: "chat_external",
      title: "External chat",
      agent_id: "claude_code",
      status: "idle",
      workspace: "/workspace",
      messages: [
        {
          id: "user_1",
          role: "user",
          content: "Continue after reload",
          status: "completed",
        },
      ],
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/hecate/v1/chat/sessions/chat_external/stream")) {
        streamRequests += 1;
        return new Response(
          new ReadableStream<Uint8Array>({
            start(controller) {
              if (streamRequests === 1) {
                controller.enqueue(chatSessionSSEFrame(pendingSession));
                controller.enqueue(
                  chatSSEFrame("approval.requested", {
                    approval_id: "approval_during_admission",
                    session_id: "chat_external",
                    adapter_id: "claude_code",
                    tool_kind: "shell",
                    tool_name: "run",
                    created_at: "2026-07-23T10:00:00Z",
                    expires_at: "2026-07-23T10:05:00Z",
                  }),
                );
              } else {
                controller.enqueue(chatSessionSSEFrame(externalSession("completed", "Recovered")));
              }
              controller.close();
            },
          }),
          { status: 200, headers: { "Content-Type": "text/event-stream" } },
        );
      }
      if (url.includes("/hecate/v1/chat/sessions/chat_external/approvals")) {
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
          <ExternalSessionObserverStatus />
        </>,
        {
          state: createRuntimeConsoleFixture({
            activeChatSessionID: "chat_external",
            activeChatSession: pendingSession,
            chatSessions: [{ ...pendingSession, message_count: 1 }],
            chatTarget: "external_agent",
          }),
          actions,
        },
      ),
    );

    await waitFor(() =>
      expect(screen.getByLabelText("Observed session")).toHaveTextContent("completed:Recovered"),
    );
    expect(screen.getByLabelText("Observed approvals")).toHaveTextContent("none");
    expect(streamRequests).toBe(2);
  });

  it("stops observing an orphaned External Agent admission gap", async () => {
    let streamRequests = 0;
    const pendingSession: ChatSessionRecord = {
      id: "chat_external",
      title: "External chat",
      agent_id: "claude_code",
      status: "idle",
      workspace: "/workspace",
      messages: [
        {
          id: "user_orphaned",
          role: "user",
          content: "Persisted without a live turn",
          status: "completed",
        },
      ],
    };
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/hecate/v1/chat/sessions/chat_external/stream")) {
        streamRequests += 1;
        return new Response(
          JSON.stringify({
            error: {
              type: "chat.session_not_running",
              message: "external agent turn is no longer active",
              user_message: "This external-agent turn did not finish starting.",
            },
          }),
          { status: 409, headers: { "Content-Type": "application/json" } },
        );
      }
      if (url.includes("/hecate/v1/chat/sessions/chat_external/approvals")) {
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
          <ExternalSessionObserverStatus />
        </>,
        {
          state: createRuntimeConsoleFixture({
            activeChatSessionID: "chat_external",
            activeChatSession: pendingSession,
            chatSessions: [{ ...pendingSession, message_count: 1 }],
            chatTarget: "external_agent",
          }),
          actions,
        },
      ),
    );

    await waitFor(() =>
      expect(screen.getByLabelText("Observed error")).toHaveTextContent(
        "409:This external-agent turn did not finish starting.",
      ),
    );
    await new Promise((resolve) => window.setTimeout(resolve, 350));
    expect(streamRequests).toBe(1);
  });

  it("reconnects with bounded backoff when a busy external-session stream closes", async () => {
    let streamRequests = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/hecate/v1/chat/sessions/chat_external/stream")) {
        streamRequests += 1;
        const session =
          streamRequests === 1
            ? externalSession("running", "Still working")
            : externalSession("completed", "Recovered");
        return new Response(
          new ReadableStream<Uint8Array>({
            start(controller) {
              controller.enqueue(chatSessionSSEFrame(session));
              controller.close();
            },
          }),
          { status: 200, headers: { "Content-Type": "text/event-stream" } },
        );
      }
      if (url.includes("/hecate/v1/chat/sessions/chat_external/approvals")) {
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
          <ExternalSessionObserverStatus />
        </>,
        {
          state: createRuntimeConsoleFixture({
            activeChatSessionID: "chat_external",
            activeChatSession: externalSession("running", "Hydrated"),
            chatTarget: "external_agent",
          }),
          actions,
        },
      ),
    );

    await waitFor(
      () =>
        expect(screen.getByLabelText("Observed session")).toHaveTextContent("completed:Recovered"),
      { timeout: 1_500 },
    );
    expect(streamRequests).toBe(2);
  });

  it("retries a transient External Agent stream server failure", async () => {
    let streamRequests = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/hecate/v1/chat/sessions/chat_external/stream")) {
        streamRequests += 1;
        if (streamRequests === 1) {
          return new Response(
            JSON.stringify({
              error: { type: "gateway_error", message: "temporary stream failure" },
            }),
            { status: 503, headers: { "Content-Type": "application/json" } },
          );
        }
        return new Response(
          new ReadableStream<Uint8Array>({
            start(controller) {
              controller.enqueue(chatSessionSSEFrame(externalSession("completed", "Recovered")));
              controller.close();
            },
          }),
          { status: 200, headers: { "Content-Type": "text/event-stream" } },
        );
      }
      if (url.includes("/hecate/v1/chat/sessions/chat_external/approvals")) {
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
          <ExternalSessionObserverStatus />
        </>,
        {
          state: createRuntimeConsoleFixture({
            activeChatSessionID: "chat_external",
            activeChatSession: externalSession("running", "Hydrated"),
            chatTarget: "external_agent",
          }),
          actions,
        },
      ),
    );

    await waitFor(
      () =>
        expect(screen.getByLabelText("Observed session")).toHaveTextContent("completed:Recovered"),
      { timeout: 1_500 },
    );
    expect(streamRequests).toBe(2);
    expect(screen.getByLabelText("Observed error")).toHaveTextContent("none");
  });

  it.each([
    { status: 401, code: "unauthorized" },
    { status: 403, code: "forbidden" },
  ])("surfaces terminal stream status $status without retrying", async ({ status, code }) => {
    let streamRequests = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/hecate/v1/chat/sessions/chat_external/stream")) {
        streamRequests += 1;
        return new Response(
          JSON.stringify({
            error: {
              type: code,
              message: `stream rejected with ${status}`,
              operator_action: "Reconnect to the runtime.",
            },
          }),
          { status, headers: { "Content-Type": "application/json" } },
        );
      }
      if (url.includes("/hecate/v1/chat/sessions/chat_external/approvals")) {
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
          <ExternalSessionObserverStatus />
        </>,
        {
          state: createRuntimeConsoleFixture({
            activeChatSessionID: "chat_external",
            activeChatSession: externalSession("running", "Hydrated"),
            chatTarget: "external_agent",
          }),
          actions,
        },
      ),
    );

    await waitFor(() =>
      expect(screen.getByLabelText("Observed error")).toHaveTextContent(
        `${status}:stream rejected with ${status}`,
      ),
    );
    await new Promise((resolve) => window.setTimeout(resolve, 350));
    expect(streamRequests).toBe(1);
  });

  it("fences a selected External Agent chat when its observer receives 404", async () => {
    let streamRequests = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/hecate/v1/chat/sessions/chat_external/stream")) {
        streamRequests += 1;
        return new Response(
          JSON.stringify({
            error: {
              type: "not_found",
              message: "chat session not found",
            },
          }),
          { status: 404, headers: { "Content-Type": "application/json" } },
        );
      }
      if (url.includes("/hecate/v1/chat/sessions/chat_external/approvals")) {
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
    const session = externalSession("running", "Hydrated");

    render(
      withRuntimeConsole(
        <>
          <RootEffects />
          <ExternalSessionObserverStatus />
        </>,
        {
          state: createRuntimeConsoleFixture({
            activeChatSessionID: "chat_external",
            activeChatSession: session,
            chatSessions: [{ ...session, message_count: 1 }],
            chatTarget: "external_agent",
          }),
          actions,
        },
      ),
    );

    await waitFor(() =>
      expect(screen.getByLabelText("Observed session")).toHaveTextContent("none"),
    );
    expect(screen.getByLabelText("Observed session list")).toHaveTextContent("none");
    expect(screen.getByLabelText("Observed deletion")).toHaveTextContent("deleted");
    expect(screen.getByLabelText("Observed error")).toHaveTextContent("none");
    await new Promise((resolve) => window.setTimeout(resolve, 350));
    expect(streamRequests).toBe(1);
  });

  it.each(["switch", "unmount"] as const)(
    "aborts the external-session observer on %s without cancelling server work",
    async (exitKind) => {
      let streamSignal: AbortSignal | null = null;
      const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith("/hecate/v1/chat/sessions/chat_external/stream")) {
          streamSignal = init?.signal ?? null;
          return new Response(
            new ReadableStream<Uint8Array>({
              start(controller) {
                controller.enqueue(chatSessionSSEFrame(externalSession("running", "Working")));
                streamSignal?.addEventListener(
                  "abort",
                  () => controller.error(new DOMException("Aborted", "AbortError")),
                  { once: true },
                );
              },
            }),
            { status: 200, headers: { "Content-Type": "text/event-stream" } },
          );
        }
        if (url.includes("/approvals")) {
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
      const activeState = createRuntimeConsoleFixture({
        activeChatSessionID: "chat_external",
        activeChatSession: externalSession("running", "Hydrated"),
        chatTarget: "external_agent",
      });
      const rendered = render(withRuntimeConsole(<RootEffects />, { state: activeState, actions }));

      await waitFor(() => expect(streamSignal).not.toBeNull());
      if (exitKind === "switch") {
        rendered.rerender(
          withRuntimeConsole(<RootEffects />, {
            state: createRuntimeConsoleFixture(),
            actions,
          }),
        );
      } else {
        rendered.unmount();
      }

      await waitFor(() => expect(streamSignal?.aborted).toBe(true));
      expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith("/cancel"))).toBe(false);
    },
  );

  it("keeps an accepted Stop fence authoritative while the passive stream settles", async () => {
    let streamController: ReadableStreamDefaultController<Uint8Array> | null = null;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/hecate/v1/chat/sessions/chat_external/stream")) {
        return new Response(
          new ReadableStream<Uint8Array>({
            start(controller) {
              streamController = controller;
              init?.signal?.addEventListener(
                "abort",
                () => controller.error(new DOMException("Aborted", "AbortError")),
                { once: true },
              );
            },
          }),
          { status: 200, headers: { "Content-Type": "text/event-stream" } },
        );
      }
      if (url.includes("/hecate/v1/chat/sessions/chat_external/approvals")) {
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
          <ExternalStopObserverHarness />
        </>,
        { state: createRuntimeConsoleFixture(), actions },
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Fence and select external chat" }));
    await waitFor(() => expect(streamController).not.toBeNull());

    act(() => {
      streamController?.enqueue(chatSessionSSEFrame(externalSession("running", "Stale")));
      streamController?.enqueue(
        chatSSEFrame("approval.requested", {
          approval_id: "approval_stale",
          session_id: "chat_external",
          adapter_id: "claude_code",
          tool_kind: "shell",
          created_at: "2026-07-23T10:00:00Z",
          expires_at: "2026-07-23T10:05:00Z",
        }),
      );
    });
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.getByLabelText("Observed session")).toHaveTextContent("running:Stopping");
    expect(screen.getByLabelText("Observed approvals")).toHaveTextContent("none");

    act(() => {
      streamController?.enqueue(chatSessionSSEFrame(externalSession("cancelled", "Stopped")));
    });
    await waitFor(() =>
      expect(screen.getByLabelText("Observed session")).toHaveTextContent("cancelled:Stopped"),
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

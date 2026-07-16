import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { type QueuedChatMessage } from "./_shared";
import { ChatProvider, useChat, type ChatCancellationOwner } from "./chat";
import { queuedChatMessageStorageKey } from "./queuedChatStorage";

function persistQueuedRevision(
  message: QueuedChatMessage,
  revision: string,
): { message: QueuedChatMessage; key: string; raw: string } {
  const durable = {
    ...message,
    delivery_storage_epoch: message.delivery_storage_epoch ?? "0",
    delivery_storage_revision: revision,
  };
  const key = queuedChatMessageStorageKey(durable.id, durable.delivery_storage_epoch, revision);
  const raw = JSON.stringify(durable);
  window.localStorage.setItem(key, raw);
  return { message: durable, key, raw };
}

function wrapper({ children }: { children: ReactNode }) {
  return <ChatProvider>{children}</ChatProvider>;
}

describe("ChatProvider workspace-mode mutation ownership", () => {
  beforeEach(() => window.localStorage.clear());
  afterEach(() => window.localStorage.clear());

  it("keeps overlapping session fences independent across coordinator consumers", () => {
    const { result } = renderHook(
      () => ({ firstConsumer: useChat(), remountedConsumer: useChat() }),
      { wrapper },
    );
    let firstToken = 0;
    let secondToken = 0;
    act(() => {
      firstToken = result.current.firstConsumer.actions.beginWorkspaceModeMutation(
        "chat_a",
        "persistent",
      ).token;
      secondToken = result.current.remountedConsumer.actions.beginWorkspaceModeMutation(
        "chat_b",
        "in_place",
      ).token;
    });

    expect(firstToken).not.toBe(secondToken);
    expect(
      result.current.firstConsumer.actions.currentWorkspaceModeMutation("chat_a"),
    ).toMatchObject({ token: firstToken, requestedMode: "persistent" });
    expect(
      result.current.remountedConsumer.actions.currentWorkspaceModeMutation("chat_b"),
    ).toMatchObject({ token: secondToken, requestedMode: "in_place" });

    let firstFinished = false;
    act(() => {
      firstFinished = result.current.firstConsumer.actions.finishWorkspaceModeMutation(
        "chat_a",
        firstToken,
      );
    });
    expect(firstFinished).toBe(true);
    expect(result.current.firstConsumer.actions.currentWorkspaceModeMutation("chat_a")).toBeNull();
    expect(
      result.current.remountedConsumer.actions.currentWorkspaceModeMutation("chat_b"),
    ).toMatchObject({ token: secondToken });
    expect(
      result.current.firstConsumer.actions.finishWorkspaceModeMutation("chat_a", firstToken),
    ).toBe(false);
    expect(
      result.current.remountedConsumer.actions.currentWorkspaceModeMutation("chat_b"),
    ).toMatchObject({ token: secondToken });
  });
});

describe("ChatProvider turn admission ownership", () => {
  beforeEach(() => window.localStorage.clear());
  afterEach(() => window.localStorage.clear());

  it("arbitrates pre-admission cancellation against admission for the exact generation", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const cancelFirst = vi.fn();
    const cancelSecond = vi.fn();
    let firstGeneration = 0;
    let secondGeneration = 0;
    let firstOwner!: ChatCancellationOwner;
    let secondOwner!: ChatCancellationOwner;
    let firstRegistered = false;
    let secondRegistered = false;

    act(() => {
      const generation = result.current.actions.beginChatTurn("chat_a", "direct_model");
      if (generation === null) throw new Error("expected the first turn to acquire ownership");
      firstGeneration = generation;
      firstRegistered = result.current.actions.registerChatTurnPreAdmissionCancel(
        firstGeneration,
        cancelFirst,
      );
    });

    expect(firstRegistered).toBe(true);
    act(() => {
      const owner = result.current.actions.beginChatCancellation("chat_a");
      if (!owner) throw new Error("expected the first cancellation to acquire ownership");
      firstOwner = owner;
    });
    expect(
      result.current.actions.cancelChatTurnBeforeAdmission({
        ...firstOwner,
        sessionID: "chat_b",
      }),
    ).toBe(false);
    expect(result.current.actions.cancelChatTurnBeforeAdmission(firstOwner)).toBe(true);
    expect(cancelFirst).toHaveBeenCalledTimes(1);
    expect(cancelFirst).toHaveBeenCalledWith(firstOwner);
    expect(result.current.actions.startChatTurnAdmission(firstGeneration)).toBe(false);

    act(() => {
      result.current.actions.finishChatCancellation(firstOwner);
      result.current.actions.completeChatTurn(firstGeneration);
      const generation = result.current.actions.beginChatTurn("chat_b", "external_agent");
      if (generation === null) throw new Error("expected the second turn to acquire ownership");
      secondGeneration = generation;
      secondRegistered = result.current.actions.registerChatTurnPreAdmissionCancel(
        secondGeneration,
        cancelSecond,
      );
    });

    expect(secondRegistered).toBe(true);
    act(() => result.current.actions.completeChatTurn(firstGeneration));
    expect(result.current.actions.startChatTurnAdmission(firstGeneration)).toBe(false);
    expect(result.current.state.chatTurnKind).toBe("external_agent");
    expect(result.current.actions.cancelChatTurnBeforeAdmission(firstOwner)).toBe(false);
    expect(cancelSecond).not.toHaveBeenCalled();
    expect(result.current.actions.startChatTurnAdmission(secondGeneration)).toBe(true);
    act(() => {
      const owner = result.current.actions.beginChatCancellation("chat_b");
      if (!owner) throw new Error("expected the second cancellation to acquire ownership");
      secondOwner = owner;
    });
    expect(result.current.actions.finishChatCancellation(firstOwner)).toBe(false);
    expect(result.current.state.chatCancelling).toBe(true);
    expect(result.current.actions.cancelChatTurnBeforeAdmission(firstOwner)).toBe(false);
    expect(result.current.actions.chatTurnServerCancellationReady(secondOwner)).toBe(false);
    expect(result.current.actions.cancelChatTurnBeforeAdmission(secondOwner)).toBe(false);
    expect(cancelSecond).not.toHaveBeenCalled();
    expect(result.current.actions.confirmChatTurnServerCancellation(secondGeneration)).toBe(true);
    expect(result.current.actions.chatTurnServerCancellationReady(secondOwner)).toBe(true);
    expect(result.current.state.chatCancellingTurnKind).toBe("external_agent");

    act(() => result.current.actions.completeChatTurn(secondGeneration));
    expect(result.current.state.chatTurnKind).toBe("");
    expect(result.current.state.chatCancellingTurnKind).toBe("external_agent");

    act(() => {
      result.current.actions.finishChatCancellation(secondOwner);
    });
    expect(result.current.state.chatCancellingTurnKind).toBe("");
  });

  it("shares accepted Stop fences across consumers until exact terminal proof arrives", () => {
    const session = {
      id: "chat_a",
      title: "Active chat",
      agent_id: "hecate",
      status: "running",
      workspace: "",
      message_count: 1,
      messages: [
        {
          id: "assistant_1",
          role: "assistant" as const,
          content: "Working",
          status: "running",
        },
      ],
    };
    const sharedWrapper = ({ children }: { children: ReactNode }) => (
      <ChatProvider
        initialState={{
          chatSessions: [session],
          activeChatSessionID: session.id,
          activeChatSession: session,
        }}
      >
        {children}
      </ChatProvider>
    );
    const { result } = renderHook(
      () => ({ stopOwner: useChat(), independentProjection: useChat() }),
      { wrapper: sharedWrapper },
    );
    let fence!: ReturnType<typeof result.current.stopOwner.actions.beginChatStopFence>;

    act(() => {
      fence = result.current.stopOwner.actions.beginChatStopFence({
        token: 7,
        sessionID: session.id,
        turnGeneration: 11,
      });
      result.current.stopOwner.actions.acceptChatStopFence(fence);
    });

    expect(result.current.independentProjection.actions.getChatStopFence(session.id)).toBe(fence);
    expect(
      result.current.independentProjection.actions.chatStopFenceProtectsSession(session.id),
    ).toBe(true);
    expect(
      result.current.independentProjection.actions.chatStopFenceAllowsSnapshot({
        ...session,
        title: "Stale dashboard snapshot",
      }),
    ).toBe(false);
    expect(
      result.current.independentProjection.actions.chatStopFenceAllowsSnapshot({
        ...session,
        title: "Unscoped terminal snapshot",
        status: "cancelled",
        messages: [{ ...session.messages[0], content: "Stopped", status: "cancelled" }],
      }),
    ).toBe(false);

    act(() => {
      result.current.independentProjection.actions.fenceChatSessionsMissingFromAuthoritativeSnapshot(
        [],
      );
    });
    expect(result.current.stopOwner.state.chatSessions).toHaveLength(1);

    let acceptedTerminal = false;
    act(() => {
      acceptedTerminal = result.current.independentProjection.actions.chatStopFenceAllowsSnapshot(
        {
          ...session,
          title: "Stopped authoritatively",
          status: "cancelled",
          messages: [{ ...session.messages[0], content: "Stopped", status: "cancelled" }],
        },
        { kind: "turn", turnGeneration: 11 },
      );
    });

    expect(acceptedTerminal).toBe(true);
    expect(fence.phase).toBe("settled");
  });

  it("captures the protecting previous Stop token while a retry request is unresolved", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const terminalSession = {
      id: "chat_a",
      title: "Stopped authoritatively",
      agent_id: "hecate",
      status: "cancelled",
      workspace: "",
      messages: [
        {
          id: "assistant_1",
          role: "assistant" as const,
          content: "Stopped",
          status: "cancelled",
        },
      ],
    };
    let acceptedFence!: ReturnType<typeof result.current.actions.beginChatStopFence>;
    let retryFence!: ReturnType<typeof result.current.actions.beginChatStopFence>;

    act(() => {
      acceptedFence = result.current.actions.beginChatStopFence({
        token: 13,
        sessionID: "chat_a",
        turnGeneration: 17,
      });
      result.current.actions.acceptChatStopFence(acceptedFence);
      retryFence = result.current.actions.beginChatStopFence({
        token: 14,
        sessionID: "chat_a",
        turnGeneration: 17,
      });
    });

    const stopReadToken = result.current.actions.stopReadTokenAtRequestStart("chat_a");
    expect(stopReadToken).toBe(13);
    expect(retryFence.phase).toBe("requesting");
    expect(retryFence.previousFence).toBe(acceptedFence);

    let restoredFence = null as typeof acceptedFence | null;
    act(() => {
      restoredFence = result.current.actions.clearChatStopFence(retryFence, true);
    });
    expect(restoredFence).toBe(acceptedFence);
    expect(result.current.actions.getChatStopFence("chat_a")).toBe(acceptedFence);

    let acceptedTerminal = false;
    act(() => {
      acceptedTerminal = result.current.actions.chatStopFenceAllowsSnapshot(terminalSession, {
        kind: "stop_read",
        stopToken: stopReadToken!,
      });
    });
    expect(acceptedTerminal).toBe(true);
    expect(acceptedFence.phase).toBe("settled");
  });

  it("makes stale same-session cancellation tokens unable to cancel or release a newer turn", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const cancelFirst = vi.fn();
    const cancelSecond = vi.fn();
    let firstGeneration = 0;
    let secondGeneration = 0;
    let firstOwner!: ChatCancellationOwner;
    let secondOwner!: ChatCancellationOwner;

    act(() => {
      firstGeneration = result.current.actions.beginChatTurn("chat_a", "direct_model") ?? 0;
      result.current.actions.registerChatTurnPreAdmissionCancel(firstGeneration, cancelFirst);
      const owner = result.current.actions.beginChatCancellation("chat_a");
      if (!owner) throw new Error("expected the first cancellation owner");
      firstOwner = owner;
    });
    expect(result.current.actions.cancelChatTurnBeforeAdmission(firstOwner)).toBe(true);
    act(() => {
      result.current.actions.finishChatCancellation(firstOwner);
      result.current.actions.completeChatTurn(firstGeneration);
      secondGeneration = result.current.actions.beginChatTurn("chat_a", "external_agent") ?? 0;
      result.current.actions.registerChatTurnPreAdmissionCancel(secondGeneration, cancelSecond);
      const owner = result.current.actions.beginChatCancellation("chat_a");
      if (!owner) throw new Error("expected the second cancellation owner");
      secondOwner = owner;
    });

    expect(secondOwner.token).toBeGreaterThan(firstOwner.token);
    expect(result.current.actions.finishChatCancellation(firstOwner)).toBe(false);
    expect(result.current.state.chatCancelling).toBe(true);
    expect(result.current.actions.chatCancellationOwnsSession("chat_a")).toBe(true);
    expect(result.current.actions.currentChatCancellationEpoch("chat_a")).toBe(2);
    expect(result.current.actions.cancelChatTurnBeforeAdmission(firstOwner)).toBe(false);
    expect(cancelSecond).not.toHaveBeenCalled();
    expect(result.current.actions.cancelChatTurnBeforeAdmission(secondOwner)).toBe(true);
    expect(cancelSecond).toHaveBeenCalledWith(secondOwner);
  });

  it("rebinds detached cancellation ownership and releases exact-session waiters", async () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const cancelDetachedTurn = vi.fn();
    let generation = 0;
    let owner!: ChatCancellationOwner;

    act(() => {
      generation = result.current.actions.beginChatTurn("", "external_agent") ?? 0;
      result.current.actions.registerChatTurnPreAdmissionCancel(generation, cancelDetachedTurn);
      const cancellationOwner = result.current.actions.beginChatCancellation("");
      if (!cancellationOwner) throw new Error("expected detached cancellation ownership");
      owner = cancellationOwner;
    });

    expect(owner.sessionID).toBe("");
    expect(owner.turnGeneration).toBe(generation);
    expect(result.current.state.chatCancellingSessionID).toBe("");
    expect(result.current.actions.chatCancellationOwnsSession("")).toBe(true);
    expect(result.current.actions.chatCancellationOwnsSession("created_after_stop")).toBe(false);
    expect(result.current.actions.currentChatCancellationEpoch("")).toBe(1);
    let cancelled = false;
    act(() => {
      cancelled = result.current.actions.cancelChatTurnBeforeAdmission(owner);
    });
    expect(cancelled).toBe(true);
    expect(cancelDetachedTurn).toHaveBeenCalledWith(owner);

    act(() => result.current.actions.bindChatTurnSession(generation, "created_after_stop"));

    expect(result.current.state.chatTurnSessionID).toBe("created_after_stop");
    expect(result.current.state.chatCancellingSessionID).toBe("created_after_stop");
    expect(result.current.actions.chatCancellationOwnsSession("")).toBe(false);
    expect(result.current.actions.chatCancellationOwnsSession("created_after_stop")).toBe(true);
    expect(result.current.actions.currentChatCancellationEpoch("created_after_stop")).toBe(1);
    let waiterReleased = false;
    const release = result.current.actions
      .waitForChatCancellationRelease("created_after_stop")
      .then(() => {
        waiterReleased = true;
      });
    await Promise.resolve();
    expect(waiterReleased).toBe(false);
    let released = false;
    await act(async () => {
      released = result.current.actions.finishChatCancellation(owner);
      await release;
    });
    expect(released).toBe(true);
    expect(waiterReleased).toBe(true);
    expect(result.current.actions.chatCancellationOwnsSession("created_after_stop")).toBe(false);
    expect(result.current.state.chatCancelling).toBe(false);
  });
});

describe("ChatProvider image attachment drafts", () => {
  beforeEach(() => window.localStorage.clear());
  afterEach(() => window.localStorage.clear());

  it("warns before unloading only while memory-only image drafts exist", () => {
    const { result } = renderHook(() => useChat(), { wrapper });

    const cleanUnload = new Event("beforeunload", { cancelable: true });
    expect(window.dispatchEvent(cleanUnload)).toBe(true);
    expect(cleanUnload.defaultPrevented).toBe(false);

    act(() => {
      result.current.actions.setPendingChatAttachments([
        {
          id: "draft-1",
          file: new File(["image"], "map.png", { type: "image/png" }),
        },
      ]);
    });

    const guardedUnload = new Event("beforeunload", { cancelable: true });
    expect(window.dispatchEvent(guardedUnload)).toBe(false);
    expect(guardedUnload.defaultPrevented).toBe(true);

    act(() => result.current.actions.setPendingChatAttachments([]));

    const clearedUnload = new Event("beforeunload", { cancelable: true });
    expect(window.dispatchEvent(clearedUnload)).toBe(true);
    expect(clearedUnload.defaultPrevented).toBe(false);
  });

  it("keeps unload protection after attachment drafts move into an in-flight turn", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const file = new File(["image"], "map.png", { type: "image/png" });
    let token: number | null = null;

    act(() => {
      result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
      token = result.current.actions.beginChatAttachmentTurn("", 1);
      result.current.actions.setPendingChatAttachments([]);
    });

    expect(token).not.toBeNull();
    expect(result.current.state.pendingChatAttachments).toEqual([]);
    expect(result.current.state.chatAttachmentTurnDraftCount).toBe(1);
    const submittingUnload = new Event("beforeunload", { cancelable: true });
    expect(window.dispatchEvent(submittingUnload)).toBe(false);
    expect(submittingUnload.defaultPrevented).toBe(true);

    act(() => {
      result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
      if (token !== null) result.current.actions.finishChatAttachmentTurn(token);
    });

    expect(result.current.state.chatAttachmentTurnDraftCount).toBe(0);
    const restoredUnload = new Event("beforeunload", { cancelable: true });
    expect(window.dispatchEvent(restoredUnload)).toBe(false);
    expect(restoredUnload.defaultPrevented).toBe(true);
  });

  it("reports one ownership-mutation guard for visible and submitted image files", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const file = new File(["image"], "map.png", { type: "image/png" });
    let token: number | null = null;

    expect(result.current.actions.chatOwnershipMutationBlockReason()).toBe("");
    act(() => {
      result.current.actions.setPendingChatAttachments([{ id: "draft-1", file }]);
    });
    expect(result.current.actions.chatOwnershipMutationBlockReason()).toBe(
      "Remove attached files before changing or deleting chat ownership.",
    );

    act(() => {
      token = result.current.actions.beginChatAttachmentTurn("chat_project", 1);
      result.current.actions.setPendingChatAttachments([]);
    });
    expect(result.current.actions.chatOwnershipMutationBlockReason()).toBe(
      "Wait for the attachment response before changing or deleting chat ownership.",
    );

    act(() => {
      if (token !== null) result.current.actions.finishChatAttachmentTurn(token);
    });
    expect(result.current.actions.chatOwnershipMutationBlockReason()).toBe("");
  });

  it("reserves destructive ownership atomically and denies image admission until release", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const file = new File(["image"], "map.png", { type: "image/png" });
    let mutationToken: number | null = null;

    act(() => {
      mutationToken = result.current.actions.beginChatOwnershipMutation();
    });

    expect(mutationToken).not.toBeNull();
    expect(result.current.state.chatOwnershipMutationInFlight).toBe(true);
    expect(result.current.actions.chatOwnershipMutationBlockReason()).toBe(
      "Wait for the current chat ownership change to finish before changing chat ownership.",
    );
    expect(result.current.actions.beginChatOwnershipMutation()).toBeNull();

    act(() => {
      result.current.actions.setPendingChatAttachments([{ id: "late-draft", file }]);
    });
    expect(result.current.state.pendingChatAttachments).toEqual([]);
    expect(result.current.actions.beginChatAttachmentTurn("chat_project", 1)).toBeNull();

    act(() => {
      if (mutationToken !== null) {
        result.current.actions.finishChatOwnershipMutation(mutationToken + 1);
      }
    });
    expect(result.current.state.chatOwnershipMutationInFlight).toBe(true);

    act(() => {
      if (mutationToken !== null) {
        result.current.actions.finishChatOwnershipMutation(mutationToken);
      }
      result.current.actions.setPendingChatAttachments([{ id: "draft-after-release", file }]);
    });
    expect(result.current.state.chatOwnershipMutationInFlight).toBe(false);
    expect(result.current.state.pendingChatAttachments).toEqual([
      { id: "draft-after-release", file },
    ]);
  });

  it("makes destructive ownership and session creation reciprocal reservations", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    let createIntent: number | null = null;
    let mutationToken: number | null = null;

    act(() => {
      createIntent = result.current.actions.tryBeginChatSessionCreate();
    });
    expect(createIntent).not.toBeNull();
    expect(result.current.actions.beginChatOwnershipMutation()).toBeNull();
    expect(result.current.actions.chatOwnershipMutationBlockReason()).toBe(
      "Wait for the new chat to finish creating before changing or deleting chat ownership.",
    );

    act(() => {
      if (createIntent !== null) result.current.actions.finishChatSessionCreate(createIntent);
      mutationToken = result.current.actions.beginChatOwnershipMutation();
    });
    expect(mutationToken).not.toBeNull();
    expect(result.current.actions.tryBeginChatSessionCreate()).toBeNull();

    act(() => {
      if (mutationToken !== null) result.current.actions.finishChatOwnershipMutation(mutationToken);
    });
    let nextCreateIntent: number | null = null;
    act(() => {
      nextCreateIntent = result.current.actions.tryBeginChatSessionCreate();
    });
    expect(nextCreateIntent).not.toBeNull();
    act(() => {
      if (nextCreateIntent !== null) {
        result.current.actions.finishChatSessionCreate(nextCreateIntent);
      }
    });
  });
});

describe("ChatProvider queued submission fence", () => {
  beforeEach(() => window.localStorage.clear());
  afterEach(() => window.localStorage.clear());

  it("requires durable idempotency provenance and the exact payload snapshot", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const queued: QueuedChatMessage = {
      id: "queued-chat-fenced",
      session_id: "chat-fenced",
      content: "Review this change",
      delivery_state: "submitting",
      delivery_idempotency_keyed: true,
      delivery_baseline_message_ids: ["msg-before"],
      delivery_storage_epoch: "0",
      execution_mode: "hecate_task",
      tools_enabled: false,
      provider_filter: "openai",
      model: "gpt-4o-mini",
      workspace: "/workspace",
      system_prompt: "Be concise.",
      agent_id: "hecate",
      created_at: "2026-07-13T10:00:00Z",
    };

    window.localStorage.setItem(queuedChatMessageStorageKey(queued.id), JSON.stringify(queued));
    expect(result.current.actions.hasDurableQueuedChatSubmittingFence(queued)).toBe(true);

    const withoutProvenance = { ...queued };
    delete withoutProvenance.delivery_idempotency_keyed;
    window.localStorage.setItem(
      queuedChatMessageStorageKey(queued.id),
      JSON.stringify(withoutProvenance),
    );
    expect(result.current.actions.hasDurableQueuedChatSubmittingFence(queued)).toBe(false);

    window.localStorage.setItem(
      queuedChatMessageStorageKey(queued.id),
      JSON.stringify({ ...queued, tools_enabled: true }),
    );
    expect(result.current.actions.hasDurableQueuedChatSubmittingFence(queued)).toBe(false);
  });
});

describe("ChatProvider deletion tombstones", () => {
  beforeEach(() => window.localStorage.clear());
  afterEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("filters tombstoned sessions from stale direct and functional list writes", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const deleted = {
      id: "chat_deleted",
      title: "Deleted",
      agent_id: "hecate",
      status: "completed",
      workspace: "",
      message_count: 0,
      messages: [],
      created_at: "2026-07-13T10:00:00Z",
      updated_at: "2026-07-13T10:00:00Z",
    };
    const live = { ...deleted, id: "chat_live", title: "Live" };

    act(() => {
      result.current.actions.tombstoneDeletedChatSession(deleted.id);
      result.current.actions.setChatSessions([deleted, live]);
    });
    expect(result.current.state.chatSessions.map((session) => session.id)).toEqual(["chat_live"]);

    act(() => {
      result.current.actions.setChatSessions((current) => [deleted, ...current]);
    });
    expect(result.current.state.chatSessions.map((session) => session.id)).toEqual(["chat_live"]);
  });

  it("purges project-owned chat state and rejects late summaries for the deleted project", () => {
    const deleted = {
      id: "chat_deleted",
      title: "Deleted",
      project_id: "project_deleted",
      agent_id: "hecate",
      status: "completed",
      workspace: "",
      message_count: 0,
      messages: [],
    };
    const live = { ...deleted, id: "chat_live", title: "Live", project_id: "project_live" };
    const projectWrapper = ({ children }: { children: ReactNode }) => (
      <ChatProvider
        initialState={{
          chatSessions: [deleted, live],
          activeChatSessionID: deleted.id,
          activeChatSession: deleted,
          queuedChatMessages: [
            {
              id: "queued_deleted",
              session_id: deleted.id,
              content: "deleted follow-up",
              execution_mode: "hecate_task",
              tools_enabled: true,
              provider_filter: "auto",
              model: "",
              workspace: "",
              system_prompt: "",
              agent_id: "hecate",
              created_at: "2026-07-13T10:00:00Z",
            },
            {
              id: "queued_live",
              session_id: live.id,
              content: "live follow-up",
              execution_mode: "hecate_task",
              tools_enabled: true,
              provider_filter: "auto",
              model: "",
              workspace: "",
              system_prompt: "",
              agent_id: "hecate",
              created_at: "2026-07-13T10:00:00Z",
            },
          ],
        }}
      >
        {children}
      </ChatProvider>
    );
    const { result } = renderHook(() => useChat(), { wrapper: projectWrapper });

    act(() => {
      result.current.actions.fenceDeletedChatProject("project_deleted");
    });

    expect(result.current.state.activeChatSessionID).toBe("");
    expect(result.current.state.activeChatSession).toBeNull();
    expect(result.current.state.chatSessions.map((session) => session.id)).toEqual([live.id]);
    expect(result.current.state.queuedChatMessages.map((message) => message.id)).toEqual([
      "queued_live",
    ]);
    expect(result.current.actions.isChatSessionDeleted("late_unknown", "project_deleted")).toBe(
      true,
    );

    act(() => {
      result.current.actions.setChatSessions([
        deleted,
        { ...deleted, id: "late_unknown", title: "Late response" },
        live,
      ]);
    });
    expect(result.current.state.chatSessions.map((session) => session.id)).toEqual([live.id]);
  });

  it("purges a project-owned prompt written by another tab without a local session summary", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const remoteQueued: QueuedChatMessage = {
      id: "queued_remote_project",
      session_id: "chat_remote_project",
      project_id: "project_deleted",
      content: "sensitive remote-tab prompt",
      delivery_storage_epoch: "0",
      execution_mode: "hecate_task",
      tools_enabled: true,
      provider_filter: "auto",
      model: "",
      workspace: "",
      system_prompt: "",
      agent_id: "hecate",
      created_at: "2026-07-13T10:00:00Z",
    };
    const persistedRemote = persistQueuedRevision(remoteQueued, "revision-remote");

    let cleaned = false;
    act(() => {
      cleaned = result.current.actions.fenceDeletedChatProject("project_deleted");
    });

    expect(cleaned).toBe(true);
    expect(window.localStorage.getItem(persistedRemote.key)).toBeNull();
    expect(result.current.actions.isChatSessionDeleted(remoteQueued.session_id)).toBe(true);
  });

  it("rechecks concurrent project prompts and preserves unrelated new records", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const first: QueuedChatMessage = {
      id: "queued_project_first",
      session_id: "chat_project_first",
      project_id: "project_deleted",
      content: "first sensitive prompt",
      delivery_storage_epoch: "0",
      execution_mode: "hecate_task",
      tools_enabled: true,
      provider_filter: "auto",
      model: "",
      workspace: "",
      system_prompt: "",
      agent_id: "hecate",
      created_at: "2026-07-13T10:00:00Z",
    };
    const late = {
      ...first,
      id: "queued_project_late",
      session_id: "chat_project_late",
      content: "late sensitive prompt",
    };
    const unrelated = {
      ...first,
      id: "queued_unrelated_late",
      session_id: "chat_unrelated_late",
      project_id: "",
      content: "new project-free prompt",
    };
    const persistedFirst = persistQueuedRevision(first, "revision-first");
    const originalRemoveItem = window.localStorage.removeItem.bind(window.localStorage);
    let injected = false;
    let lateKey = "";
    let unrelatedKey = "";
    let unrelatedRaw = "";
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      originalRemoveItem(key);
      if (!injected && key === persistedFirst.key) {
        injected = true;
        lateKey = persistQueuedRevision(late, "revision-late").key;
        const persistedUnrelated = persistQueuedRevision(unrelated, "revision-unrelated");
        unrelatedKey = persistedUnrelated.key;
        unrelatedRaw = persistedUnrelated.raw;
      }
    });

    let cleaned = false;
    act(() => {
      cleaned = result.current.actions.fenceDeletedChatProject("project_deleted");
    });

    expect(cleaned).toBe(true);
    expect(injected).toBe(true);
    expect(window.localStorage.getItem(persistedFirst.key)).toBeNull();
    expect(window.localStorage.getItem(lateKey)).toBeNull();
    expect(window.localStorage.getItem(unrelatedKey)).toBe(unrelatedRaw);
    expect(result.current.state.queuedChatMessages.some((message) => message.id === late.id)).toBe(
      false,
    );
    expect(result.current.state.queuedChatMessages).toEqual([expect.objectContaining(unrelated)]);
  });

  it("fails project cleanup closed for a legacy prompt with unknown ownership", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const legacyQueued: QueuedChatMessage = {
      id: "queued_unknown_project",
      session_id: "chat_unknown_project",
      content: "legacy prompt with no project owner",
      delivery_storage_epoch: "0",
      execution_mode: "hecate_task",
      tools_enabled: true,
      provider_filter: "auto",
      model: "",
      workspace: "",
      system_prompt: "",
      agent_id: "hecate",
      created_at: "2026-07-13T10:00:00Z",
    };
    const raw = JSON.stringify(legacyQueued);
    window.localStorage.setItem(queuedChatMessageStorageKey(legacyQueued.id), raw);

    let cleaned = true;
    act(() => {
      cleaned = result.current.actions.fenceDeletedChatProject("project_deleted");
    });

    expect(cleaned).toBe(false);
    expect(window.localStorage.getItem(queuedChatMessageStorageKey(legacyQueued.id))).toBe(raw);
    expect(result.current.state.queuedChatMessages).toEqual([
      expect.objectContaining({
        id: legacyQueued.id,
        delivery_state: "reconcile_required",
        delivery_storage_failed: true,
      }),
    ]);
  });

  it("distinguishes a new project-free prompt from legacy unknown ownership", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const projectFreeQueued: QueuedChatMessage = {
      id: "queued_project_free",
      session_id: "chat_project_free",
      project_id: "",
      content: "explicitly project-free prompt",
      delivery_storage_epoch: "0",
      execution_mode: "hecate_task",
      tools_enabled: true,
      provider_filter: "auto",
      model: "",
      workspace: "",
      system_prompt: "",
      agent_id: "hecate",
      created_at: "2026-07-13T10:00:00Z",
    };
    const raw = JSON.stringify(projectFreeQueued);
    window.localStorage.setItem(queuedChatMessageStorageKey(projectFreeQueued.id), raw);

    let cleaned = false;
    act(() => {
      cleaned = result.current.actions.fenceDeletedChatProject("project_deleted");
    });

    expect(cleaned).toBe(true);
    expect(window.localStorage.getItem(queuedChatMessageStorageKey(projectFreeQueued.id))).toBe(
      raw,
    );
    expect(result.current.state.queuedChatMessages).toEqual([projectFreeQueued]);
  });

  it("fails project cleanup closed when a raw queue record cannot be audited", () => {
    const { result } = renderHook(() => useChat(), { wrapper });
    const malformedKey = queuedChatMessageStorageKey("malformed_project_owner");
    const malformed = "{prompt-bearing-corruption";
    window.localStorage.setItem(malformedKey, malformed);

    let cleaned = true;
    act(() => {
      cleaned = result.current.actions.fenceDeletedChatProject("project_deleted");
    });

    expect(cleaned).toBe(false);
    expect(window.localStorage.getItem(malformedKey)).toBe(malformed);
  });

  it("fences and clears every known chat session during explicit all-session cleanup", () => {
    const session = {
      id: "chat_reset",
      title: "Reset",
      agent_id: "hecate",
      status: "completed",
      workspace: "",
      message_count: 0,
      messages: [],
    };
    const resetWrapper = ({ children }: { children: ReactNode }) => (
      <ChatProvider
        initialState={{
          chatSessions: [session],
          activeChatSessionID: session.id,
          activeChatSession: session,
          queuedChatMessages: [
            {
              id: "queued_reset",
              session_id: session.id,
              content: "reset follow-up",
              execution_mode: "hecate_task",
              tools_enabled: true,
              provider_filter: "auto",
              model: "",
              workspace: "",
              system_prompt: "",
              agent_id: "hecate",
              created_at: "2026-07-13T10:00:00Z",
            },
          ],
        }}
      >
        {children}
      </ChatProvider>
    );
    const { result } = renderHook(() => useChat(), { wrapper: resetWrapper });

    act(() => {
      result.current.actions.fenceAllChatSessionsDeleted();
    });

    expect(result.current.state.activeChatSessionID).toBe("");
    expect(result.current.state.activeChatSession).toBeNull();
    expect(result.current.state.chatSessions).toEqual([]);
    expect(result.current.state.queuedChatMessages).toEqual([]);
    expect(result.current.actions.isChatSessionDeleted(session.id)).toBe(true);

    act(() => result.current.actions.setChatSessions([session]));
    expect(result.current.state.chatSessions).toEqual([]);
  });
});

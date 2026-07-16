import { describe, expect, it } from "vitest";

import type { ChatMessageRecord } from "../../types/chat";
import { projectVisibleMessage } from "./ChatTranscript";

function message(
  id: string,
  content: string,
  extra?: Partial<ChatMessageRecord>,
): ChatMessageRecord {
  return { id, role: "assistant", content, ...extra };
}

describe("projectVisibleMessage", () => {
  it("maps persisted fields onto the visible shape", () => {
    const m = message("m1", "hello", { status: "running", agent_name: "claude" });
    const visible = projectVisibleMessage(m, 0);

    expect(visible.id).toBe("m1");
    expect(visible.content).toBe("hello");
    expect(visible.agent_name).toBe("claude");
    // agent_status is sourced from the record's status field.
    expect(visible.agent_status).toBe("running");
  });

  it("returns the same reference for the same message object", () => {
    const m = message("m1", "hello");

    // Reference stability is the whole point: combined with
    // reconcileChatSession preserving the message object across snapshots,
    // it lets the memoized transcript row bail out of re-rendering.
    expect(projectVisibleMessage(m, 0)).toBe(projectVisibleMessage(m, 0));
  });

  it("does not share a projection across distinct objects with the same id", () => {
    const a = message("m1", "hello");
    const b = message("m1", "hello");

    // The cache is keyed by object identity, not id — a fresh snapshot
    // object yields a fresh projection. This is exactly why preserving
    // identity upstream (reconcileChatSession) matters.
    expect(projectVisibleMessage(a, 0)).not.toBe(projectVisibleMessage(b, 0));
  });

  it("never caches id-less rows and derives an index-based id", () => {
    const m = message("", "optimistic");

    const first = projectVisibleMessage(m, 3);
    const second = projectVisibleMessage(m, 3);

    expect(first.id).toBe("agent-message-3");
    // Optimistic/synthetic rows have no stable identity, so they get a
    // fresh object every call and are never cached.
    expect(first).not.toBe(second);
  });

  it("projects timing and context_packet for the inspector", () => {
    const timing = { total_ms: 1200, bottleneck: "model" };
    const context_packet = { provider: "anthropic", model: "claude", message_count: 3 };
    const m = message("m1", "hello", { timing, context_packet });

    const visible = projectVisibleMessage(m, 0);

    // ChatTranscriptRow forwards these into the timing/context inspector
    // for assistant rows; omitting them from the projection silently
    // dropped that data from reconciled snapshots.
    expect(visible.timing).toBe(timing);
    expect(visible.context_packet).toBe(context_packet);
  });

  it("projects persisted attachment metadata for transcript reloads", () => {
    const attachments = [
      {
        id: "att-1",
        session_id: "chat-1",
        filename: "map.png",
        media_type: "image/png",
        size_bytes: 5,
        sha256: "abc",
        created_at: "2026-07-13T10:00:00Z",
        content_url: "/hecate/v1/chat/sessions/chat-1/attachments/att-1/content",
      },
    ];

    expect(projectVisibleMessage(message("m1", "look", { attachments }), 0).attachments).toBe(
      attachments,
    );
  });
});

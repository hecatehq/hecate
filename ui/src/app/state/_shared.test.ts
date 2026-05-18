import { describe, expect, it } from "vitest";

import { parseQueuedChatMessageList } from "./_shared";

describe("parseQueuedChatMessageList", () => {
  it("keeps valid queued chat messages", () => {
    expect(
      parseQueuedChatMessageList([
        {
          id: "queued-1",
          session_id: "chat-1",
          content: "continue",
          execution_mode: "direct_model",
          provider_filter: "auto",
          model: "gpt-4o-mini",
          workspace: "/tmp/hecate",
          system_prompt: "Be concise.",
          agent_id: "hecate",
          created_at: "2026-05-18T10:00:00.000Z",
        },
      ]),
    ).toEqual([
      {
        id: "queued-1",
        session_id: "chat-1",
        content: "continue",
        execution_mode: "direct_model",
        provider_filter: "auto",
        model: "gpt-4o-mini",
        workspace: "/tmp/hecate",
        system_prompt: "Be concise.",
        agent_id: "hecate",
        created_at: "2026-05-18T10:00:00.000Z",
      },
    ]);
  });

  it("drops queued chat messages without a supported execution mode", () => {
    expect(
      parseQueuedChatMessageList([
        {
          id: "legacy-queued-model",
          session_id: "chat-1",
          content: "legacy direct turn",
          runtime_kind: "model",
        },
        {
          id: "queued-tools",
          session_id: "chat-1",
          content: "valid tools turn",
          execution_mode: "hecate_task",
        },
      ]),
    ).toEqual([
      expect.objectContaining({
        id: "queued-tools",
        execution_mode: "hecate_task",
      }),
    ]);
  });
});

import { describe, expect, it } from "vitest";

import { findReusableEmptyDraftSession } from "./chat";

import type { ChatSessionSummaryRecord } from "../../../types/chat";

describe("findReusableEmptyDraftSession", () => {
  it("matches empty idle Hecate project draft sessions", () => {
    const sessions: ChatSessionSummaryRecord[] = [
      {
        id: "chat_used",
        title: "Plan next work - Product Manager",
        project_id: "proj_1",
        agent_id: "hecate",
        provider: "ollama",
        model: "ministral-3:latest",
        workspace: "/tmp/hecate",
        status: "idle",
        message_count: 1,
      },
      {
        id: "chat_empty",
        title: "Plan next work - Product Manager",
        project_id: "proj_1",
        agent_id: "hecate",
        provider: "ollama",
        model: "ministral-3:latest",
        workspace: "/tmp/hecate",
        status: "idle",
        message_count: 0,
      },
    ];

    expect(
      findReusableEmptyDraftSession(sessions, {
        agentID: "hecate",
        projectID: "proj_1",
        provider: "ollama",
        model: "ministral-3:latest",
        title: "Plan next work - Product Manager",
      })?.id,
    ).toBe("chat_empty");
  });

  it("does not match active, messaged, or differently routed sessions", () => {
    const sessions: ChatSessionSummaryRecord[] = [
      {
        id: "chat_running",
        title: "Plan next work - Product Manager",
        project_id: "proj_1",
        agent_id: "hecate",
        provider: "ollama",
        model: "ministral-3:latest",
        workspace: "/tmp/hecate",
        status: "running",
        message_count: 0,
      },
      {
        id: "chat_other_model",
        title: "Plan next work - Product Manager",
        project_id: "proj_1",
        agent_id: "hecate",
        provider: "ollama",
        model: "qwen2.5-coder",
        workspace: "/tmp/hecate",
        status: "idle",
        message_count: 0,
      },
      {
        id: "chat_external",
        title: "Plan next work - Product Manager",
        project_id: "proj_1",
        agent_id: "claude_code",
        provider: "ollama",
        model: "ministral-3:latest",
        workspace: "/tmp/hecate",
        status: "idle",
        message_count: 0,
      },
    ];

    expect(
      findReusableEmptyDraftSession(sessions, {
        agentID: "hecate",
        projectID: "proj_1",
        provider: "ollama",
        model: "ministral-3:latest",
        title: "Plan next work - Product Manager",
      }),
    ).toBeNull();
  });
});

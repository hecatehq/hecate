import { describe, expect, it } from "vitest";

import {
  chatWorkspaceDiscardTransportFailureMessage,
  classifyChatWorkspaceDiscardResult,
  findReusableEmptyDraftSession,
  queuedCommittedTurnIsTerminal,
} from "./chat";

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

  it("does not reuse an empty shell with a different workspace posture", () => {
    const sessions: ChatSessionSummaryRecord[] = [
      {
        id: "chat_in_place",
        title: "Plan next work - Product Manager",
        project_id: "proj_1",
        agent_id: "hecate",
        provider: "ollama",
        model: "ministral-3:latest",
        workspace: "/tmp/hecate",
        workspace_mode: "in_place",
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
        workspaceMode: "persistent",
      }),
    ).toBeNull();
  });

  it("does not reuse an empty default shell for a different Agent Preset", () => {
    const sessions: ChatSessionSummaryRecord[] = [
      {
        id: "chat_preset",
        title: "Plan next work - Product Manager",
        project_id: "proj_1",
        agent_id: "hecate",
        agent_preset: {
          id: "review_qa",
          name: "Review QA",
          tools_enabled: false,
          writes_allowed: false,
          network_allowed: false,
        },
        provider: "ollama",
        model: "ministral-3:latest",
        workspace: "/tmp/hecate",
        status: "idle",
        message_count: 0,
      },
    ];
    const request = {
      agentID: "hecate",
      projectID: "proj_1",
      provider: "ollama",
      model: "ministral-3:latest",
      title: "Plan next work - Product Manager",
    };

    expect(findReusableEmptyDraftSession(sessions, request)).toBeNull();
    expect(
      findReusableEmptyDraftSession(sessions, { ...request, agentPresetID: "review_qa" })?.id,
    ).toBe("chat_preset");
  });
});

describe("queuedCommittedTurnIsTerminal", () => {
  it("does not borrow a terminal assistant from a later user turn", () => {
    expect(
      queuedCommittedTurnIsTerminal(
        {
          id: "chat_1",
          title: "Replay",
          agent_id: "hecate",
          status: "completed",
          workspace: "",
          messages: [
            { id: "u1", turn_id: "turn_1", role: "user", content: "first" },
            { id: "u2", turn_id: "turn_2", role: "user", content: "later" },
            {
              id: "a2",
              turn_id: "turn_2",
              role: "assistant",
              content: "later result",
              status: "completed",
            },
          ],
        },
        "u1",
      ),
    ).toBe(false);
  });

  it("requires the exact canonical turn_id on both messages", () => {
    const session = {
      id: "chat_1",
      title: "Replay",
      agent_id: "hecate",
      status: "completed",
      workspace: "",
      messages: [
        { id: "u1", turn_id: "turn_1", role: "user" as const, content: "first" },
        {
          id: "a1",
          turn_id: "turn_2",
          role: "assistant" as const,
          content: "result",
          status: "completed",
        },
      ],
    };
    expect(queuedCommittedTurnIsTerminal(session, "u1")).toBe(false);
    session.messages[1].turn_id = "turn_1";
    expect(queuedCommittedTurnIsTerminal(session, "u1")).toBe(true);
  });

  it("does not fall back to segment or Task Run identity when turn_id is absent", () => {
    expect(
      queuedCommittedTurnIsTerminal(
        {
          id: "chat_1",
          title: "Replay",
          agent_id: "hecate",
          status: "completed",
          workspace: "",
          messages: [
            {
              id: "u1",
              role: "user",
              content: "first",
              segment_id: "segment_1",
              task_id: "task_1",
              run_id: "run_1",
            },
            {
              id: "a1",
              role: "assistant",
              content: "result",
              status: "completed",
              segment_id: "segment_1",
              task_id: "task_1",
              run_id: "run_1",
            },
          ],
        },
        "u1",
      ),
    ).toBe(false);
  });
});

describe("workspace discard result classification", () => {
  it("accepts only legacy absence or the exact applied outcome", () => {
    expect(classifyChatWorkspaceDiscardResult(undefined, 1)).toMatchObject({
      authoritative: true,
      noticeType: "success",
    });
    expect(
      classifyChatWorkspaceDiscardResult({ outcome: "applied", refresh_required: true }, 0),
    ).toMatchObject({
      authoritative: true,
      noticeType: "success",
      message: "Workspace changes were discarded. Refresh the review before another discard.",
    });
  });

  it.each(["outcome_unknown", "future_server_outcome", " applied ", "", " "])(
    "fails closed for non-authoritative outcome %s",
    (outcome) => {
      const decision = classifyChatWorkspaceDiscardResult({ outcome }, 1);
      expect(decision.authoritative).toBe(false);
      expect(decision.noticeType).toBe("error");
      expect(decision.message).toMatch(/may have been applied/i);
      expect(decision.message).toMatch(/inspect Git/i);
    },
  );

  it.each([null, undefined, 42])("fails closed for malformed present outcome %s", (outcome) => {
    const decision = classifyChatWorkspaceDiscardResult({ outcome } as any, 1);
    expect(decision.authoritative).toBe(false);
    expect(decision.message).toMatch(/may have been applied/i);
  });

  it("preserves the applied cleanup warning without invalidating the refreshed snapshot", () => {
    expect(
      classifyChatWorkspaceDiscardResult(
        { outcome: "applied", cleanup_failed: true, refresh_required: true },
        1,
      ),
    ).toEqual({
      authoritative: true,
      noticeType: "error",
      message:
        "Workspace changes were discarded, but Git cleanup did not finish. Refresh the review and check Git before continuing.",
    });
  });

  it("describes transport failures as ambiguous after submit", () => {
    const message = chatWorkspaceDiscardTransportFailureMessage(new Error("connection closed"));
    expect(message).toContain("connection closed");
    expect(message).toMatch(/may have been applied/i);
    expect(message).toMatch(/inspect Git/i);
  });
});

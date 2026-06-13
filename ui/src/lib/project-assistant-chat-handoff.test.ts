import { afterEach, describe, expect, it, vi } from "vitest";

import {
  clearProjectAssistantChatHandoff,
  readProjectAssistantChatHandoff,
  writeProjectAssistantChatHandoff,
} from "./project-assistant-chat-handoff";
import type { ProjectAssistantProposal } from "../types/project";

const proposal: ProjectAssistantProposal = {
  id: "pa_1",
  title: "Plan next work",
  summary: "Create a project work item.",
  requires_confirmation: true,
  actions: [
    {
      kind: "create_work_item",
      target: { project_id: "proj_1" },
      patch: { project_id: "proj_1", title: "Plan next work" },
    },
  ],
};

afterEach(() => {
  vi.restoreAllMocks();
  window.sessionStorage.clear();
});

describe("Project Assistant chat handoff", () => {
  it("round-trips a proposal through sessionStorage", () => {
    expect(
      writeProjectAssistantChatHandoff({
        project_id: "proj_1",
        proposal,
        request: "Plan next work",
        source_session_id: "chat_1",
        created_at: "2026-06-13T00:00:00Z",
      }),
    ).toBe(true);

    expect(readProjectAssistantChatHandoff()).toMatchObject({
      project_id: "proj_1",
      request: "Plan next work",
      source_session_id: "chat_1",
      proposal: { id: "pa_1", actions: [{ kind: "create_work_item" }] },
    });

    clearProjectAssistantChatHandoff();
    expect(readProjectAssistantChatHandoff()).toBeNull();
  });

  it("returns false when sessionStorage rejects the handoff write", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota exceeded");
    });

    expect(writeProjectAssistantChatHandoff({ project_id: "proj_1", proposal })).toBe(false);
  });

  it("drops malformed stored proposals", () => {
    window.sessionStorage.setItem(
      "hecate.projectAssistant.chatDraft",
      JSON.stringify({ project_id: "proj_1", proposal: { id: "pa_bad" } }),
    );

    expect(readProjectAssistantChatHandoff()).toBeNull();
  });
});

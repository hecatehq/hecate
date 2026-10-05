import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { getTaskRunArtifact } from "../../lib/api";
import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";
import { ChatTranscript, type TranscriptItem, type VisibleChatMessage } from "./ChatTranscript";

vi.mock("../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/api")>()),
  getTaskRunArtifact: vi.fn(),
}));

const report = {
  id: "browser-report",
  task_id: "historical-task",
  run_id: "historical-run",
  step_id: "browser-step",
  kind: "browser_flow_evidence",
  mime_type: "text/plain",
  storage_kind: "inline",
  content_text: "Retained page evidence.",
};

function message(overrides: Partial<VisibleChatMessage> = {}): VisibleChatMessage {
  return {
    id: "browser-message",
    role: "assistant",
    content: "I inspected the application.",
    agent_status: "completed",
    turn_kind: "hecate_task",
    task_id: report.task_id,
    run_id: report.run_id,
    activities: [
      {
        id: "tool-browser",
        type: "tool_call",
        kind: "tool",
        step_id: report.step_id,
        status: "completed",
        title: "Browser interaction",
      },
      {
        id: "artifact-browser",
        type: "artifact",
        kind: report.kind,
        artifact_id: report.id,
        step_id: report.step_id,
        status: "ready",
        title: "Saved application evidence",
      },
    ],
    ...overrides,
  };
}

function transcript(messages: VisibleChatMessage[], isHecateAgentChat = true) {
  const items: TranscriptItem[] = messages.map((entry) => ({
    type: "message",
    key: `message:${entry.id}`,
    message: entry,
  }));
  return (
    <ChatTranscript
      activeSessionID="chat-browser-results"
      isHecateAgentChat={isHecateAgentChat}
      transcriptItems={items}
      visibleMessageCount={items.length}
      streaming={false}
      emptyState={null}
      canOpenProject={() => true}
      onOpenProject={() => true}
      openExternalAgentSetup={() => undefined}
    />
  );
}

beforeEach(() => {
  vi.mocked(getTaskRunArtifact).mockReset();
  vi.mocked(getTaskRunArtifact).mockResolvedValue({ object: "task_artifact", data: report });
});

describe("ChatTranscript browser results integration", () => {
  it("reads the historical message run lazily and preserves its report across new snapshots", async () => {
    const user = userEvent.setup();
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "chat-browser-results",
      chatTarget: "agent",
      activeChatSession: {
        id: "chat-browser-results",
        agent_id: "hecate",
        title: "Browser results",
        workspace: "/tmp/work",
        status: "completed",
        task_id: "current-task",
        latest_run_id: "current-run",
        messages: [],
      },
    });
    const actions = createRuntimeConsoleActions();
    const first = message();
    const { container, rerender } = render(
      withRuntimeConsole(transcript([first]), { state, actions }),
    );
    expect(screen.getByText("Completed · interaction")).toBeVisible();
    expect(getTaskRunArtifact).not.toHaveBeenCalled();

    await user.click(screen.getByText("Browser result"));
    expect(await screen.findByText(report.content_text)).toBeVisible();
    expect(getTaskRunArtifact).toHaveBeenCalledWith(
      "historical-task",
      "historical-run",
      report.id,
      expect.any(AbortSignal),
    );

    const snapshot = { ...first, activities: first.activities?.map((entry) => ({ ...entry })) };
    rerender(
      withRuntimeConsole(
        transcript([
          snapshot,
          message({
            id: "later-answer",
            content: "A newer answer.",
            turn_kind: "direct_model",
            task_id: undefined,
            run_id: undefined,
            activities: [],
          }),
        ]),
        { state, actions },
      ),
    );
    expect(await screen.findByText("A newer answer.")).toBeVisible();
    expect(screen.getByText(report.content_text)).toBeVisible();
    expect(container.querySelectorAll(".browser-evidence-panel")).toHaveLength(1);
    expect(getTaskRunArtifact).toHaveBeenCalledTimes(1);
    await user.click(screen.getByText("Browser result"));
    await user.click(screen.getByText("Browser result"));
    await waitFor(() => expect(screen.getByText(report.content_text)).toBeVisible());
    expect(getTaskRunArtifact).toHaveBeenCalledTimes(1);
  });

  it.each([
    { name: "direct model", turnKind: "direct_model", native: true },
    { name: "external agent", turnKind: "external_agent", native: false },
    { name: "unknown legacy turn", turnKind: undefined, native: true },
  ])(
    "does not give a $name a native artifact reader from raw task fields",
    ({ turnKind, native }) => {
      const state = createRuntimeConsoleFixture({
        activeChatSessionID: "chat-browser-results",
        chatTarget: native ? "agent" : "external_agent",
      });
      const { container } = render(
        withRuntimeConsole(transcript([message({ turn_kind: turnKind })], native), {
          state,
          actions: createRuntimeConsoleActions(),
        }),
      );
      expect(container.querySelector(".browser-evidence-panel")).toBeNull();
      expect(screen.queryByText("Browser result")).toBeNull();
      expect(getTaskRunArtifact).not.toHaveBeenCalled();
    },
  );
});

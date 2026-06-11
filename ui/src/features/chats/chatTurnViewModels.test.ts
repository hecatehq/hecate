import { describe, expect, it } from "vitest";

import {
  chatTurnKindFromWire,
  toChatMessageViewModel,
  toChatSegmentViewModel,
} from "./chatTurnViewModels";

describe("chatTurnViewModels", () => {
  it("prefers explicit turn_kind over legacy execution fields", () => {
    expect(
      chatTurnKindFromWire({
        turn_kind: "direct_model",
        execution_mode: "hecate_task",
        tools_enabled: true,
        task_id: "task_1",
      }),
    ).toBe("direct_model");
  });

  it("maps legacy Hecate tools-off turns to direct model turns", () => {
    const turn = toChatMessageViewModel({
      id: "msg_1",
      role: "assistant",
      content: "hello",
      execution_mode: "hecate_task",
      tools_enabled: false,
      task_id: "task_legacy_should_not_link",
    });

    expect(turn.turnKind).toBe("direct_model");
    expect(turn.isDirectModel).toBe(true);
    expect(turn.isTaskBacked).toBe(false);
    expect(turn.taskID).toBe("");
  });

  it("maps legacy Hecate tools-on segments to task-backed turns", () => {
    const turn = toChatSegmentViewModel({
      id: "seg_1",
      execution_mode: "hecate_task",
      tools_enabled: true,
      task_id: "task_1",
      latest_run_id: "run_1",
      status: "awaiting_approval",
      message_count: 2,
    });

    expect(turn.turnKind).toBe("hecate_task");
    expect(turn.isTaskBacked).toBe(true);
    expect(turn.taskID).toBe("task_1");
    expect(turn.latestRunID).toBe("run_1");
    expect(turn.isBusy).toBe(true);
    expect(turn.messageCount).toBe(2);
  });

  it("maps external-agent segments independently of tools_enabled", () => {
    const turn = toChatSegmentViewModel({
      id: "seg_1",
      execution_mode: "external_agent",
      tools_enabled: false,
      status: "running",
      message_count: 1,
    });

    expect(turn.turnKind).toBe("external_agent");
    expect(turn.isExternalAgent).toBe(true);
    expect(turn.isBusy).toBe(true);
  });
});

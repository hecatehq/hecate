import { describe, expect, it } from "vitest";

import {
  chatTurnKindFromWire,
  toChatMessageViewModel,
  toChatSegmentViewModel,
} from "./chatTurnViewModels";

describe("chatTurnViewModels", () => {
  it("uses explicit turn_kind without reconstructing legacy execution fields", () => {
    expect(
      chatTurnKindFromWire({
        turn_kind: "direct_model",
        execution_mode: "hecate_task",
        tools_enabled: true,
        task_id: "task_1",
      }),
    ).toBe("direct_model");
  });

  it("keeps Chat Turn identity separate from Task Run identity", () => {
    const direct = toChatMessageViewModel({
      id: "msg_direct",
      turn_id: "turn_direct",
      turn_kind: "direct_model",
      role: "assistant",
      content: "hello",
    });
    const taskBacked = toChatMessageViewModel({
      id: "msg_task",
      turn_id: "turn_task",
      turn_kind: "hecate_task",
      task_id: "task_1",
      run_id: "run_1",
      role: "assistant",
      content: "done",
    });

    expect(direct.turnID).toBe("turn_direct");
    expect(direct.runID).toBe("");
    expect(taskBacked.turnID).toBe("turn_task");
    expect(taskBacked.runID).toBe("run_1");
  });

  it("does not treat a non-task run_id as a Chat Turn identity", () => {
    const turn = toChatMessageViewModel({
      id: "msg_1",
      turn_kind: "external_agent",
      run_id: "removed_non_task_run_identity",
      role: "assistant",
      content: "hello",
    });

    expect(turn.turnID).toBe("");
    expect(turn.runID).toBe("");
  });

  it("treats missing turn_kind as unknown instead of inferring from legacy fields", () => {
    const turn = toChatMessageViewModel({
      id: "msg_1",
      role: "assistant",
      content: "hello",
      execution_mode: "hecate_task",
      tools_enabled: false,
      task_id: "task_legacy_should_not_link",
    });

    expect(turn.turnKind).toBe("unknown");
    expect(turn.isDirectModel).toBe(false);
    expect(turn.isTaskBacked).toBe(false);
    expect(turn.taskID).toBe("");
  });

  it("maps explicit Hecate task-backed segments", () => {
    const turn = toChatSegmentViewModel({
      id: "seg_1",
      turn_kind: "hecate_task",
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

  it("maps explicit external-agent segments independently of tools_enabled", () => {
    const turn = toChatSegmentViewModel({
      id: "seg_1",
      turn_kind: "external_agent",
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

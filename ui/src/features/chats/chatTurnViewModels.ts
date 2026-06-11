import type { ChatMessageRecord, ChatSegmentRecord } from "../../types/chat";

export type ChatTurnKind = "direct_model" | "hecate_task" | "external_agent" | "unknown";

type ChatTurnWire = {
  turn_kind?: string;
  execution_mode?: string;
  tools_enabled?: boolean;
  segment_id?: string;
  task_id?: string;
  run_id?: string;
  latest_run_id?: string;
  native_session_id?: string;
  agent_id?: string;
  driver_kind?: string;
  provider?: string;
  model?: string;
  workspace?: string;
  status?: string;
};

export type ChatTurnViewModel = {
  turnKind: ChatTurnKind;
  executionMode: string;
  toolsEnabled: boolean | undefined;
  segmentID: string;
  taskID: string;
  runID: string;
  latestRunID: string;
  provider: string;
  model: string;
  workspace: string;
  status: string;
  isDirectModel: boolean;
  isTaskBacked: boolean;
  isExternalAgent: boolean;
  isHecateOwned: boolean;
  isBusy: boolean;
};

export type ChatSegmentViewModel = ChatTurnViewModel & {
  messageCount: number;
};

export function chatTurnKindFromWire(input: ChatTurnWire): ChatTurnKind {
  const explicit = normalizeChatTurnKind(input.turn_kind);
  if (explicit) return explicit;
  if (input.execution_mode === "external_agent") return "external_agent";
  // Legacy snapshots can carry stale Hecate execution_mode values; a foreign
  // agent_id is the stronger owner signal when turn_kind is absent.
  if (input.agent_id && input.agent_id !== "hecate") return "external_agent";
  if (input.execution_mode === "hecate_task") {
    return input.tools_enabled === false ? "direct_model" : "hecate_task";
  }
  if (input.task_id) return "hecate_task";
  if (input.native_session_id || input.driver_kind) return "external_agent";
  return "unknown";
}

export function toChatMessageViewModel(
  message: ChatMessageRecord | ChatTurnWire,
): ChatTurnViewModel {
  return toChatTurnViewModel(message);
}

export function toChatSegmentViewModel(segment: ChatSegmentRecord): ChatSegmentViewModel {
  const base = toChatTurnViewModel(segment);
  return {
    ...base,
    messageCount: segment.message_count ?? 0,
  };
}

function toChatTurnViewModel(input: ChatTurnWire): ChatTurnViewModel {
  const turnKind = chatTurnKindFromWire(input);
  const taskID = turnKind === "hecate_task" ? (input.task_id ?? "") : "";
  const runID = turnKind === "hecate_task" ? (input.run_id ?? "") : "";
  const latestRunID = turnKind === "hecate_task" ? (input.latest_run_id ?? "") : "";
  return {
    turnKind,
    executionMode: input.execution_mode ?? "",
    toolsEnabled: input.tools_enabled,
    segmentID: input.segment_id ?? "",
    taskID,
    runID,
    latestRunID,
    provider: input.provider ?? "",
    model: input.model ?? "",
    workspace: input.workspace ?? "",
    status: input.status ?? "",
    isDirectModel: turnKind === "direct_model",
    isTaskBacked: turnKind === "hecate_task" && Boolean(taskID),
    isExternalAgent: turnKind === "external_agent",
    isHecateOwned: turnKind === "direct_model" || turnKind === "hecate_task",
    isBusy: chatTurnStatusIsBusy(input.status),
  };
}

function normalizeChatTurnKind(value?: string): ChatTurnKind | "" {
  switch (value) {
    case "direct_model":
    case "hecate_task":
    case "external_agent":
      return value;
    default:
      return "";
  }
}

function chatTurnStatusIsBusy(status?: string): boolean {
  return (
    status === "queued" ||
    status === "running" ||
    status === "in_progress" ||
    status === "awaiting_approval" ||
    status === "pending"
  );
}

import type { ChatConfigOptionRecord } from "../../types/chat";

export type AgentConfigOptionKind =
  | "instructions"
  | "model"
  | "thought_level"
  | "mode"
  | "tool"
  | "other";

export function mergeAgentConfigOptions(
  catalogOptions: ChatConfigOptionRecord[],
  sessionOptions: ChatConfigOptionRecord[],
): ChatConfigOptionRecord[] {
  if (catalogOptions.length === 0) return sessionOptions;
  if (sessionOptions.length === 0) return catalogOptions;

  const sessionByID = new Map(sessionOptions.map((option) => [option.id, option]));
  const catalogIDs = new Set(catalogOptions.map((option) => option.id));
  const merged = catalogOptions.map((catalogOption) => {
    const sessionOption = sessionByID.get(catalogOption.id);
    if (!sessionOption) return catalogOption;
    return {
      ...catalogOption,
      current_value: sessionOption.current_value ?? catalogOption.current_value,
      current_bool: sessionOption.current_bool ?? catalogOption.current_bool,
    };
  });
  for (const sessionOption of sessionOptions) {
    if (!catalogIDs.has(sessionOption.id)) {
      merged.push(sessionOption);
    }
  }
  return merged;
}

export function prioritizeAgentConfigOptions(
  options: ChatConfigOptionRecord[],
): ChatConfigOptionRecord[] {
  const priority = (option: ChatConfigOptionRecord) => {
    switch (agentConfigOptionKind(option)) {
      case "instructions":
        return -1;
      case "model":
        return 0;
      case "thought_level":
        return 1;
      case "mode":
        return 2;
      case "tool":
        return 3;
      default:
        return 4;
    }
  };
  return [...options].sort((a, b) => priority(a) - priority(b) || a.name.localeCompare(b.name));
}

export function agentConfigOptionKind(option: ChatConfigOptionRecord): AgentConfigOptionKind {
  if (agentConfigOptionIsInstructions(option)) return "instructions";
  const category = (option.category ?? "").toLowerCase();
  if (
    category === "model" ||
    category === "thought_level" ||
    category === "mode" ||
    category === "tool"
  ) {
    return category;
  }
  const tokens = agentConfigOptionTokens(option);
  // Keep model before mode: some agent model pickers include mode-shaped
  // labels, and the send gate depends on model pickers staying classified.
  if (tokens.includes("model")) return "model";
  if (
    hasTokenSequence(tokens, ["thought", "level"]) ||
    tokens.includes("thinking") ||
    tokens.includes("reasoning")
  ) {
    return "thought_level";
  }
  if (tokens.includes("mode")) return "mode";
  if (tokens.includes("tool") || hasTokenSequence(tokens, ["web", "search"])) return "tool";
  return "other";
}

export function agentConfigOptionIsText(option: ChatConfigOptionRecord): boolean {
  const type = option.type.toLowerCase();
  return (
    type === "text" ||
    type === "textarea" ||
    type === "string" ||
    type === "prompt" ||
    type === "multiline"
  );
}

export function agentConfigOptionIsInstructions(option: ChatConfigOptionRecord): boolean {
  const tokens = agentConfigOptionTokens(option);
  return (
    hasTokenSequence(tokens, ["system", "prompt"]) ||
    hasTokenSequence(tokens, ["agent", "instructions"]) ||
    tokens.includes("instructions")
  );
}

export function agentConfigOptionLabel(option: ChatConfigOptionRecord): string {
  switch (agentConfigOptionKind(option)) {
    case "instructions":
      return "instructions";
    case "model":
      return "model";
    case "thought_level":
      return "reasoning";
    case "mode":
      return "mode";
    case "tool":
      return option.name || option.id;
    default:
      return option.name || option.id;
  }
}

export function externalAgentRequiresModelSelection(
  configOptions: ChatConfigOptionRecord[],
): boolean {
  const modelOptions = configOptions.filter(
    (item) => item.type === "select" && agentConfigOptionKind(item) === "model",
  );
  const exactModelOptions = modelOptions.filter(isExactModelPicker);
  const candidates = exactModelOptions.length > 0 ? exactModelOptions : modelOptions;
  return candidates.some((option) => {
    const value = (option.current_value ?? "").trim();
    return value === "" || value.startsWith("__hecate_no_");
  });
}

function agentConfigOptionKey(option: ChatConfigOptionRecord): string {
  return `${option.id} ${option.name} ${option.category ?? ""}`.toLowerCase();
}

function agentConfigOptionTokens(option: ChatConfigOptionRecord): string[] {
  return agentConfigOptionKey(option)
    .split(/[^a-z0-9]+/)
    .filter(Boolean);
}

function hasTokenSequence(tokens: string[], sequence: string[]): boolean {
  if (sequence.length === 0 || tokens.length < sequence.length) return false;
  return tokens.some((_, index) =>
    sequence.every((token, sequenceIndex) => tokens[index + sequenceIndex] === token),
  );
}

function isExactModelPicker(option: ChatConfigOptionRecord): boolean {
  return option.id.toLowerCase() === "model" || (option.category ?? "").toLowerCase() === "model";
}

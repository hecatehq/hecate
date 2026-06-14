import type { ChatConfigOptionRecord } from "../../types/chat";

export type AgentConfigOptionKind = "instructions" | "model" | "thought_level" | "mode" | "other";

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
      default:
        return 3;
    }
  };
  return [...options].sort((a, b) => priority(a) - priority(b) || a.name.localeCompare(b.name));
}

export function agentConfigOptionKind(option: ChatConfigOptionRecord): AgentConfigOptionKind {
  if (agentConfigOptionIsInstructions(option)) return "instructions";
  const category = (option.category ?? "").toLowerCase();
  if (category === "model" || category === "thought_level" || category === "mode") {
    return category;
  }
  const key = agentConfigOptionKey(option);
  if (key.includes("model")) return "model";
  if (
    key.includes("thought_level") ||
    key.includes("thought level") ||
    key.includes("thinking") ||
    key.includes("reasoning")
  ) {
    return "thought_level";
  }
  if (key.includes("mode")) return "mode";
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
  const key = agentConfigOptionKey(option);
  return (
    key.includes("system_prompt") ||
    key.includes("system prompt") ||
    key.includes("agent_instructions") ||
    key.includes("agent instructions") ||
    key.includes("instructions")
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
    default:
      return option.name || option.id;
  }
}

export function externalAgentRequiresModelSelection(
  configOptions: ChatConfigOptionRecord[],
): boolean {
  const option = configOptions.find(
    (item) => item.type === "select" && agentConfigOptionKind(item) === "model",
  );
  if (!option) return false;
  const value = (option.current_value ?? "").trim();
  return value === "" || value.startsWith("__hecate_no_");
}

function agentConfigOptionKey(option: ChatConfigOptionRecord): string {
  return `${option.id} ${option.name} ${option.category ?? ""}`.toLowerCase();
}

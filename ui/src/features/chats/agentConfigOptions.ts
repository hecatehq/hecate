import type { ChatConfigOptionRecord } from "../../types/chat";

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

export function externalAgentRequiresModelSelection(
  configOptions: ChatConfigOptionRecord[],
): boolean {
  const option = configOptions.find((item) => {
    const key = `${item.id} ${item.name} ${item.category ?? ""}`.toLowerCase();
    return item.type === "select" && (key.includes("model") || item.category === "model");
  });
  if (!option) return false;
  const value = (option.current_value ?? "").trim();
  return value === "" || value.startsWith("__hecate_no_");
}

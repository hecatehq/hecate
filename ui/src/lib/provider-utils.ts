import type { ConfiguredProviderRecord, ProviderPresetRecord, ProviderRecord } from "../types/runtime";

const PROVIDER_ICON_COLORS: Record<string, string> = {
  anthropic: "var(--brand-anthropic)",
  openai: "var(--brand-openai)",
  gemini: "var(--brand-gemini)",
  mistral: "var(--brand-mistral)",
  groq: "var(--brand-groq)",
  deepseek: "var(--teal)",
  perplexity: "var(--teal)",
  together_ai: "var(--t2)",
  xai: "var(--t0)",
  ollama: "var(--teal)",
  lmstudio: "var(--t2)",
  llamacpp: "var(--t2)",
  localai: "var(--t2)",
};

export function resolvedBaseURL(
  name: string,
  cp?: ConfiguredProviderRecord,
  presets?: ProviderPresetRecord[],
): string {
  if (cp?.base_url) return cp.base_url;
  return presets?.find(p => p.id === name)?.base_url ?? "";
}

export function providerDotColor(enabled: boolean, healthy: boolean): "green" | "amber" | "red" {
  if (!enabled) return "red";
  if (healthy) return "green";
  return "amber";
}

export function providerIconColor(id: string): string {
  return PROVIDER_ICON_COLORS[id.toLowerCase()] ?? "var(--teal)";
}

export function providerDisplayName(
  providerID: string,
  configuredProviders: ConfiguredProviderRecord[] = [],
  presets: ProviderPresetRecord[] = [],
  runtimeProviders: ProviderRecord[] = [],
): string {
  const configured = configuredProviders.find(provider => provider.id === providerID);
  const presetID = configured?.preset_id || providerID;
  return presets.find(preset => preset.id === presetID)?.name
    || configured?.name
    || runtimeProviders.find(provider => provider.name === providerID)?.name
    || providerID;
}

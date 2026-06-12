import type {
  ConfiguredProviderRecord,
  ProviderPresetRecord,
  ProviderRecord,
} from "../types/provider";

const PROVIDER_ICON_COLORS: Record<string, string> = {
  alibaba: "var(--teal)",
  anthropic: "var(--brand-anthropic)",
  cerebras: "var(--teal)",
  cohere: "var(--t2)",
  deepinfra: "var(--teal)",
  deepseek: "var(--teal)",
  fireworks: "var(--t2)",
  gemini: "var(--brand-gemini)",
  groq: "var(--brand-groq)",
  huggingface: "var(--t2)",
  llamacpp: "var(--t2)",
  lmstudio: "var(--t2)",
  localai: "var(--t2)",
  mistral: "var(--brand-mistral)",
  moonshot: "var(--teal)",
  nvidia: "var(--t2)",
  ollama: "var(--teal)",
  openai: "var(--brand-openai)",
  openrouter: "var(--teal)",
  perplexity: "var(--teal)",
  requesty: "var(--teal)",
  together_ai: "var(--t2)",
  vercel_ai_gateway: "var(--t0)",
  xai: "var(--t0)",
  zai: "var(--t2)",
};

const PROVIDER_DISPLAY_NAMES: Record<string, string> = {
  alibaba: "Alibaba Cloud Qwen",
  anthropic: "Anthropic",
  cerebras: "Cerebras",
  cohere: "Cohere",
  deepinfra: "DeepInfra",
  deepseek: "DeepSeek",
  fireworks: "Fireworks AI",
  gemini: "Google Gemini",
  groq: "Groq",
  huggingface: "Hugging Face",
  llamacpp: "llama.cpp",
  lmstudio: "LM Studio",
  localai: "LocalAI",
  mistral: "Mistral",
  moonshot: "Moonshot AI",
  nvidia: "NVIDIA",
  ollama: "Ollama",
  openai: "OpenAI",
  openrouter: "OpenRouter",
  perplexity: "Perplexity",
  requesty: "Requesty",
  together_ai: "Together AI",
  vercel_ai_gateway: "Vercel AI Gateway",
  xai: "xAI",
  zai: "z.ai",
};

export function resolvedBaseURL(
  name: string,
  cp?: ConfiguredProviderRecord,
  presets?: ProviderPresetRecord[],
): string {
  if (cp?.base_url) return cp.base_url;
  return presets?.find((p) => p.id === name)?.base_url ?? "";
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
  const configured = configuredProviders.find((provider) => provider.id === providerID);
  const presetID = configured?.preset_id || providerID;
  const canonicalID = presetID.toLowerCase();
  return (
    presets.find((preset) => preset.id === presetID)?.name ||
    PROVIDER_DISPLAY_NAMES[canonicalID] ||
    runtimeProviders.find((provider) => provider.name === providerID)?.name ||
    configured?.name ||
    providerID
  );
}

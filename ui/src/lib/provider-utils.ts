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
  const presetID = cp?.preset_id || name;
  return presets?.find((p) => p.id === presetID)?.base_url ?? "";
}

export function providerDotColor(enabled: boolean, healthy: boolean): "green" | "amber" | "red" {
  if (!enabled) return "red";
  if (healthy) return "green";
  return "amber";
}

export function providerIconColor(id: string): string {
  return PROVIDER_ICON_COLORS[id.toLowerCase()] ?? "var(--teal)";
}

export function normalizeProviderKey(value: string | null | undefined): string {
  return (value ?? "").trim().toLowerCase();
}

function canonicalProviderDisplayName(
  key: string | null | undefined,
  presets: ProviderPresetRecord[] = [],
): string | undefined {
  const normalized = normalizeProviderKey(key);
  if (!normalized) return undefined;
  return (
    presets.find((preset) => normalizeProviderKey(preset.id) === normalized)?.name ||
    PROVIDER_DISPLAY_NAMES[normalized]
  );
}

export function configuredProviderAliases(
  provider: Pick<ConfiguredProviderRecord, "id" | "name" | "preset_id"> | null | undefined,
): string[] {
  const seen = new Set<string>();
  return [provider?.id, provider?.name, provider?.preset_id].flatMap((alias) => {
    const key = normalizeProviderKey(alias);
    if (!alias || !key || seen.has(key)) return [];
    seen.add(key);
    return [alias];
  });
}

export function configuredProviderRouteKey(
  provider: Pick<ConfiguredProviderRecord, "id" | "name" | "preset_id"> | null | undefined,
): string {
  return provider?.preset_id || provider?.id || provider?.name || "";
}

export function configuredProviderMatches(
  provider: Pick<ConfiguredProviderRecord, "id" | "name" | "preset_id"> | null | undefined,
  key: string | null | undefined,
): boolean {
  const target = normalizeProviderKey(key);
  return Boolean(
    target &&
    configuredProviderAliases(provider).some((alias) => normalizeProviderKey(alias) === target),
  );
}

export function configuredProviderForKey(
  key: string | null | undefined,
  configuredProviders: ConfiguredProviderRecord[] = [],
): ConfiguredProviderRecord | undefined {
  return configuredProviders.find((provider) => configuredProviderMatches(provider, key));
}

export function providerAliasesForKey(
  key: string,
  configuredProviders: ConfiguredProviderRecord[] = [],
): Set<string> {
  const aliases = new Set<string>();
  const add = (value: string | null | undefined) => {
    const normalized = normalizeProviderKey(value);
    if (normalized) aliases.add(normalized);
  };
  add(key);
  for (const provider of configuredProviders) {
    if (configuredProviderMatches(provider, key)) {
      for (const alias of configuredProviderAliases(provider)) add(alias);
      add(configuredProviderRouteKey(provider));
    }
  }
  return aliases;
}

export function providerKeyMatches(
  key: string | null | undefined,
  aliases: Set<string> | string[],
): boolean {
  const normalized = normalizeProviderKey(key);
  if (!normalized) return false;
  return aliases instanceof Set
    ? aliases.has(normalized)
    : aliases.some((alias) => normalizeProviderKey(alias) === normalized);
}

export function runtimeProviderForKey(
  key: string,
  runtimeProviders: ProviderRecord[] = [],
  configuredProviders: ConfiguredProviderRecord[] = [],
): ProviderRecord | undefined {
  const aliases = providerAliasesForKey(key, configuredProviders);
  return runtimeProviders.find((provider) => providerKeyMatches(provider.name, aliases));
}

export function runtimeProviderForConfigured(
  provider: Pick<ConfiguredProviderRecord, "id" | "name" | "preset_id"> | null | undefined,
  runtimeProviders: ProviderRecord[] = [],
): ProviderRecord | undefined {
  const aliases = new Set(configuredProviderAliases(provider).map(normalizeProviderKey));
  aliases.add(normalizeProviderKey(configuredProviderRouteKey(provider)));
  return runtimeProviders.find((runtimeProvider) =>
    providerKeyMatches(runtimeProvider.name, aliases),
  );
}

export function providerDisplayName(
  providerID: string,
  configuredProviders: ConfiguredProviderRecord[] = [],
  presets: ProviderPresetRecord[] = [],
  runtimeProviders: ProviderRecord[] = [],
): string {
  const configured = configuredProviderForKey(providerID, configuredProviders);
  const runtimeProvider = runtimeProviderForKey(providerID, runtimeProviders, configuredProviders);
  return (
    canonicalProviderDisplayName(configured?.preset_id, presets) ||
    canonicalProviderDisplayName(providerID, presets) ||
    canonicalProviderDisplayName(configured?.name, presets) ||
    canonicalProviderDisplayName(configured?.id, presets) ||
    canonicalProviderDisplayName(runtimeProvider?.name, presets) ||
    runtimeProvider?.name ||
    configured?.name ||
    providerID
  );
}

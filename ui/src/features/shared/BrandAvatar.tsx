import type { CSSProperties } from "react";
import {
  AnthropicIcon,
  ClaudeCode,
  CursorIcon,
  Deepseek,
  GoogleGeminiIcon,
  Groq,
  LmStudioIcon,
  MetaIcon,
  MistralAiIcon,
  OllamaIcon,
  OpenaiIcon,
  PerplexityIcon,
  Together,
  Xai,
} from "@dev.icons/react";
import type { Icon as Devicon } from "@dev.icons/react";
import hecateMarkURL from "../../assets/hecate-mark-white-64.png";
import { Icon, Icons } from "./Icons";

type BrandAvatarProps = {
  brand?: string;
  fallback?: string;
  assistant?: boolean;
  boxed?: boolean;
  size?: number;
  title?: string;
  style?: CSSProperties;
};

type BrandIconSpec = {
  component?: Devicon;
  image?: string;
  monochrome?: boolean;
};

const BRAND_ICONS: Record<string, BrandIconSpec> = {
  anthropic: { component: AnthropicIcon, monochrome: true },
  claude_code: { component: ClaudeCode },
  codex: { component: OpenaiIcon, monochrome: true },
  cursor_agent: { component: CursorIcon, monochrome: true },
  deepseek: { component: Deepseek },
  gemini: { component: GoogleGeminiIcon },
  groq: { component: Groq, monochrome: true },
  hecate: { image: hecateMarkURL },
  lm_studio: { component: LmStudioIcon, monochrome: true },
  lmstudio: { component: LmStudioIcon, monochrome: true },
  llama_cpp: { component: MetaIcon, monochrome: true },
  llamacpp: { component: MetaIcon, monochrome: true },
  mistral: { component: MistralAiIcon },
  ollama: { component: OllamaIcon, monochrome: true },
  openai: { component: OpenaiIcon, monochrome: true },
  perplexity: { component: PerplexityIcon },
  together_ai: { component: Together },
  xai: { component: Xai, monochrome: true },
};

const MONOCHROME_ICON_FILTER = "brightness(0) invert(1) opacity(0.88)";
const MONOCHROME_ICON_COLOR = "rgba(255, 255, 255, 0.88)";

export function BrandAvatar({
  brand,
  fallback,
  assistant = true,
  boxed = true,
  size = 28,
  title,
  style,
}: BrandAvatarProps) {
  const icon = findBrandIcon(brand);
  const label = fallbackLetter(fallback || brand || (assistant ? "H" : "U"));
  const accessibleTitle = title || brand || label;

  const IconComponent = icon?.component;
  const glyph = icon?.image
    ? (
      <img
        alt=""
        aria-hidden="true"
        src={icon.image}
        style={{ display: "block", height: Math.max(14, Math.round(size * 0.6)), width: Math.max(14, Math.round(size * 0.6)) }}
      />
    )
    : IconComponent
    ? (
      <IconComponent
        aria-hidden="true"
        size={Math.max(14, Math.round(size * 0.62))}
        style={{
          color: icon.monochrome ? MONOCHROME_ICON_COLOR : undefined,
          display: "block",
          filter: icon.monochrome ? MONOCHROME_ICON_FILTER : undefined,
        }}
      />
    )
    : !assistant
    ? <span aria-hidden="true" style={{ display: "inline-flex" }}><Icon d={Icons.user} size={Math.max(15, Math.round(size * 0.56))} strokeWidth={1.8} /></span>
    : (
      <span
        aria-hidden="true"
        style={{ fontFamily: "var(--font-mono)", fontSize: Math.max(9, Math.round(size * 0.39)), fontWeight: 600, lineHeight: 1 }}
      >
        {label}
      </span>
    );

  if (!boxed) {
    return (
      <span
        aria-label={title ? accessibleTitle : undefined}
        style={{
          alignItems: "center",
          color: assistant ? "var(--teal)" : "var(--t1)",
          display: "inline-flex",
          height: icon ? size : undefined,
          justifyContent: "center",
          width: icon ? size : undefined,
          ...style,
        }}
      >
        {glyph}
      </span>
    );
  }

  return (
    <span
      aria-label={title ? accessibleTitle : undefined}
      style={{
        alignItems: "center",
        background: assistant ? "var(--teal-bg)" : "var(--bg3)",
        border: `1px solid ${assistant ? "var(--teal-border)" : "var(--border)"}`,
        borderRadius: "var(--radius-sm)",
        color: assistant ? "var(--teal)" : "var(--t1)",
        display: "inline-flex",
        flexShrink: 0,
        height: size,
        justifyContent: "center",
        width: size,
        ...style,
      }}
    >
      {glyph}
    </span>
  );
}

function findBrandIcon(brand?: string): BrandIconSpec | null {
  const normalized = normalizeBrand(brand);
  if (!normalized) return null;
  return BRAND_ICONS[normalized] ?? baseModelBrand(normalized);
}

function normalizeBrand(brand?: string): string {
  return (brand || "")
    .trim()
    .toLowerCase()
    .replace(/^[^/]+\//, "")
    .replace(/[:@].*$/, "")
    .replace(/\.com$/, "")
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
}

function baseModelBrand(normalized: string): BrandIconSpec | null {
  if (normalized.startsWith("gpt_") || normalized.startsWith("o1") || normalized.startsWith("o3") || normalized.startsWith("o4")) {
    return BRAND_ICONS.openai;
  }
  if (normalized.startsWith("claude_")) return BRAND_ICONS.claude_code;
  if (normalized.startsWith("ministral") || normalized.startsWith("mistral_")) return BRAND_ICONS.mistral;
  if (normalized.startsWith("gemini_")) return BRAND_ICONS.gemini;
  return null;
}

function fallbackLetter(value: string): string {
  return (value.trim()[0] || "H").toUpperCase();
}

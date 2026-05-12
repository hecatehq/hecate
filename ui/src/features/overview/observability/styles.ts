// Shared visual tokens for the Observability workspace. Lives here
// so the table, the waterfall, the route-candidate list, and the
// drawer header can all pull the same provider/phase colors and
// table-cell padding without each owning its own copy.

import type { CSSProperties } from "react";

import type { TraceTimelineItem } from "../../../lib/runtime-trace";
import type { TraceSpanRecord } from "../../../types/runtime";

// 900px chosen because at narrower widths the inline split between
// table and drawer feels too cramped — the table only has so many
// columns it can shed before unreadable. Below that we fall back to
// the centered Modal we used to ship.
export const DRAWER_BREAKPOINT_PX = 900;

export type StatusFilter = "all" | "healthy" | "error";

export const PROVIDER_COLORS: Record<string, string> = {
  anthropic:   "var(--brand-anthropic)",
  openai:      "var(--brand-openai)",
  gemini:      "var(--brand-gemini)",
  mistral:     "var(--brand-mistral)",
  groq:        "var(--brand-groq)",
  deepseek:    "var(--teal)",
  perplexity:  "var(--teal)",
  together_ai: "var(--t2)",
  xai:         "var(--t0)",
  ollama:      "var(--teal)",
  lmstudio:    "var(--t2)",
  llamacpp:    "var(--t2)",
  localai:     "var(--t2)",
};

// phaseColor maps the phase classification to an existing token. The
// provider phase additionally pulls the brand color from the span's
// `gen_ai.provider.name` attribute when present so the bar visually
// matches the row in the table.
export function phaseColor(phase: TraceTimelineItem["phase"], span?: TraceSpanRecord): string {
  if (phase === "provider") {
    const attr = span?.attributes?.["gen_ai.provider.name"];
    if (typeof attr === "string" && attr) {
      const c = PROVIDER_COLORS[attr.toLowerCase()];
      if (c) return c;
    }
    return "var(--brand-anthropic)";
  }
  switch (phase) {
    case "request":  return "var(--teal)";
    case "routing":  return "var(--amber)";
    case "governor": return "var(--brand-mistral)";
    case "cost":     return "var(--t2)";
    case "usage":    return "var(--t2)";
    case "queue":    return "var(--amber)";
    case "orchestration": return "var(--t2)";
    case "tool":     return "var(--brand-openai)";
    case "approval": return "var(--brand-anthropic)";
    case "artifact": return "var(--green)";
    case "retention": return "var(--t3)";
    case "agent_chat": return "var(--brand-openai)";
    case "response": return "var(--teal)";
    default:         return "var(--t3)";
  }
}

export const PHASE_LABEL: Record<TraceTimelineItem["phase"], string> = {
  request: "request", routing: "routing",
  provider: "provider", governor: "governor", usage: "usage",
  cost: "cost", response: "response", queue: "queue",
  orchestration: "orchestration", tool: "tool", approval: "approval",
  artifact: "artifact", retention: "retention", agent_chat: "agent chat",
  other: "other",
};

// ATTR_PRIORITY_KEYS is the surface the SpanAttributePanel renders
// above the fold; remaining attributes drop into a collapsible
// `<details>` block. The set was tuned over time to match what an
// operator typically looks at first when a span is selected.
export const ATTR_PRIORITY_KEYS = [
  "provider", "gen_ai.provider.name",
  "model", "gen_ai.request.model", "gen_ai.response.model",
  "status_code", "error",
  "usage.input_tokens", "gen_ai.usage.input_tokens",
  "usage.output_tokens", "gen_ai.usage.output_tokens",
  "route.skip_reason", "route.fallback_from",
];

// thStyle mirrors the Providers table header — uppercase 11px t2 with
// the same horizontal padding so the two views share visual chrome.
export const thStyle: CSSProperties = {
  padding: "6px 12px",
  textAlign: "left",
  fontSize: 11,
  fontWeight: 500,
  color: "var(--t2)",
  letterSpacing: "0.04em",
  textTransform: "uppercase",
  whiteSpace: "nowrap",
};

export const tdBase: CSSProperties = {
  padding: "8px 12px",
  fontSize: 12,
  fontFamily: "var(--font-mono)",
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

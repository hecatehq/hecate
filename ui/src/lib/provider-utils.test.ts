import { describe, expect, it } from "vitest";

import {
  configuredProviderForKey,
  configuredProviderRouteKey,
  providerDisplayName,
  providerDotColor,
  resolvedBaseURL,
  runtimeProviderForConfigured,
} from "./provider-utils";
import type { ConfiguredProviderRecord, ProviderPresetRecord } from "../types/provider";

const presets: ProviderPresetRecord[] = [
  {
    id: "openai",
    name: "OpenAI",
    kind: "cloud",
    protocol: "openai",
    base_url: "https://api.openai.com/v1",
    description: "",
  },
  {
    id: "llamacpp",
    name: "llama.cpp",
    kind: "local",
    protocol: "openai",
    base_url: "http://127.0.0.1:8080/v1",
    description: "",
  },
  {
    id: "localai",
    name: "LocalAI",
    kind: "local",
    protocol: "openai",
    base_url: "http://127.0.0.1:8080/v1",
    description: "",
  },
  {
    id: "ollama",
    name: "Ollama",
    kind: "local",
    protocol: "openai",
    base_url: "http://127.0.0.1:11434/v1",
    description: "",
  },
  {
    id: "fireworks",
    name: "Fireworks AI",
    kind: "cloud",
    protocol: "openai",
    base_url: "https://api.fireworks.ai/inference/v1",
    description: "",
  },
  {
    id: "lmstudio",
    name: "LM Studio",
    kind: "local",
    protocol: "openai",
    base_url: "http://127.0.0.1:1234/v1",
    description: "",
  },
];

function makeCP(name: string, base_url?: string): ConfiguredProviderRecord {
  return {
    id: name,
    name,
    kind: "cloud",
    protocol: "openai",
    base_url: base_url ?? "",
    credential_configured: true,
  };
}

describe("resolvedBaseURL", () => {
  it("returns cp base_url when present", () => {
    const cp = makeCP("openai", "https://custom.openai.example.com/v1");
    expect(resolvedBaseURL("openai", cp, presets)).toBe("https://custom.openai.example.com/v1");
  });

  it("falls back to preset base_url when cp has no base_url", () => {
    const cp = makeCP("openai");
    expect(resolvedBaseURL("openai", cp, presets)).toBe("https://api.openai.com/v1");
  });

  it("uses the configured preset id when the stored provider id is custom", () => {
    const cp = { ...makeCP("fireworks-ai"), name: "fireworks", preset_id: "fireworks" };
    expect(resolvedBaseURL("fireworks-ai", cp, presets)).toBe(
      "https://api.fireworks.ai/inference/v1",
    );
  });

  it("falls back to preset base_url when cp is undefined", () => {
    expect(resolvedBaseURL("ollama", undefined, presets)).toBe("http://127.0.0.1:11434/v1");
  });

  it("returns empty string when no cp and no matching preset", () => {
    expect(resolvedBaseURL("unknown-provider", undefined, presets)).toBe("");
  });
});

describe("providerDotColor", () => {
  it("returns red when disabled regardless of health", () => {
    expect(providerDotColor(false, true)).toBe("red");
    expect(providerDotColor(false, false)).toBe("red");
  });

  it("returns green when enabled and healthy", () => {
    expect(providerDotColor(true, true)).toBe("green");
  });

  it("returns amber when enabled but unhealthy", () => {
    expect(providerDotColor(true, false)).toBe("amber");
  });
});

describe("providerDisplayName", () => {
  it("uses preset names when available", () => {
    expect(providerDisplayName("ollama", [], presets)).toBe("Ollama");
  });

  it("falls back to canonical names before lower-case configured names", () => {
    expect(providerDisplayName("ollama", [makeCP("ollama")], [])).toBe("Ollama");
    expect(providerDisplayName("lmstudio", [makeCP("lmstudio")], [])).toBe("LM Studio");
    expect(providerDisplayName("fireworks", [makeCP("fireworks")], [])).toBe("Fireworks AI");
    expect(providerDisplayName("openrouter", [makeCP("openrouter")], [])).toBe("OpenRouter");
    expect(providerDisplayName("vercel_ai_gateway", [makeCP("vercel_ai_gateway")], [])).toBe(
      "Vercel AI Gateway",
    );
  });

  it("keeps custom provider names when no canonical name exists", () => {
    expect(providerDisplayName("my-local", [makeCP("my-local")], [])).toBe("my-local");
  });

  it("resolves canonical display names through configured provider aliases", () => {
    const configured = [{ ...makeCP("fireworks-ai"), name: "fireworks", preset_id: "fireworks" }];

    expect(providerDisplayName("fireworks-ai", configured, presets)).toBe("Fireworks AI");
    expect(providerDisplayName("fireworks", configured, presets)).toBe("Fireworks AI");
  });
});

describe("provider aliases", () => {
  it("maps custom configured ids to the canonical provider route key", () => {
    const configured = { ...makeCP("fireworks-ai"), name: "fireworks", preset_id: "fireworks" };

    expect(configuredProviderRouteKey(configured)).toBe("fireworks");
    expect(configuredProviderForKey("fireworks", [configured])?.id).toBe("fireworks-ai");
    expect(configuredProviderForKey("fireworks-ai", [configured])?.id).toBe("fireworks-ai");
  });

  it("keeps stable ids as the route key when no preset alias exists", () => {
    const configured = { ...makeCP("ollama"), name: "Ollama" };

    expect(configuredProviderRouteKey(configured)).toBe("ollama");
  });

  it("finds runtime providers through configured aliases", () => {
    const configured = { ...makeCP("lm-studio"), name: "lmstudio", preset_id: "lmstudio" };
    const runtime = runtimeProviderForConfigured(configured, [
      {
        name: "lmstudio",
        kind: "local",
        healthy: true,
        status: "ok",
      },
    ]);

    expect(runtime?.name).toBe("lmstudio");
  });
});

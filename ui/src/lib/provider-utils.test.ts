import { describe, expect, it } from "vitest";

import { providerDisplayName, providerDotColor, resolvedBaseURL } from "./provider-utils";
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
  });

  it("keeps custom provider names when no canonical name exists", () => {
    expect(providerDisplayName("my-local", [makeCP("my-local")], [])).toBe("my-local");
  });
});

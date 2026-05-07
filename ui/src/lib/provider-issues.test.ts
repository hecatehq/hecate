import { describe, expect, it } from "vitest";

import { buildLocalProviderIssue, buildSelectedModelIssue } from "./provider-issues";

describe("buildLocalProviderIssue", () => {
  it("returns an ollama pull hint when the default local model is missing", () => {
    const issue = buildLocalProviderIssue({
      name: "ollama",
      kind: "local",
      healthy: true,
      status: "healthy",
      default_model: "llama3.1:8b",
      models: ["qwen2.5:7b"],
    });

    expect(issue).toEqual(
      expect.objectContaining({
        provider: "ollama",
        model: "llama3.1:8b",
        command: "ollama pull llama3.1:8b",
      }),
    );
  });

  it("returns null when the default model is already present", () => {
    const issue = buildLocalProviderIssue({
      name: "ollama",
      kind: "local",
      healthy: true,
      status: "healthy",
      default_model: "llama3.1:8b",
      models: ["llama3.1:8b"],
    });

    expect(issue).toBeNull();
  });
});

describe("buildSelectedModelIssue", () => {
  it("returns null when the selected model is discovered", () => {
    const issue = buildSelectedModelIssue({
      model: "llama3.1:8b",
      providerFilter: "ollama",
      selectableModels: [{ id: "llama3.1:8b", owned_by: "ollama", metadata: { provider: "ollama" } }],
      configuredProvider: {
        id: "ollama",
        name: "Ollama",
        kind: "local",
        protocol: "openai",
        base_url: "http://127.0.0.1:11434/v1",
        credential_configured: false,
      },
    });

    expect(issue).toBeNull();
  });

  it("explains a stale local model selection", () => {
    const issue = buildSelectedModelIssue({
      model: "llama3.1:8b",
      providerFilter: "ollama",
      selectableModels: [{ id: "qwen2.5:7b", owned_by: "ollama", metadata: { provider: "ollama" } }],
      configuredProvider: {
        id: "ollama",
        name: "Ollama",
        kind: "local",
        protocol: "openai",
        base_url: "http://127.0.0.1:11434/v1",
        credential_configured: false,
      },
      runtimeProvider: {
        name: "ollama",
        kind: "local",
        healthy: true,
        status: "healthy",
        models: ["qwen2.5:7b"],
        model_count: 1,
      },
    });

    expect(issue).toEqual(expect.objectContaining({
      title: "Selected model is not available from this provider",
      model: "llama3.1:8b",
      providerLabel: "Ollama",
    }));
    expect(issue?.message).toContain("does not currently report");
    expect(issue?.steps.join(" ")).toContain("Pull or load llama3.1:8b");
  });

  it("explains a stale auto-route model selection", () => {
    const issue = buildSelectedModelIssue({
      model: "gpt-4o-mini",
      providerFilter: "auto",
      selectableModels: [{ id: "claude-sonnet-4-6", owned_by: "anthropic", metadata: { provider: "anthropic" } }],
    });

    expect(issue).toEqual(expect.objectContaining({
      title: "Selected model is not routable",
      providerLabel: "All providers",
    }));
    expect(issue?.message).toContain("No configured provider currently reports");
  });

  it("uses provider-agnostic repair steps for stale auto-route selections", () => {
    const issue = buildSelectedModelIssue({
      model: "llama3.1:8b",
      providerFilter: "auto",
      selectableModels: [{ id: "qwen2.5:7b", owned_by: "ollama", metadata: { provider: "ollama", provider_kind: "local" } }],
    });

    expect(issue?.steps.join(" ")).toContain("Pick a model that appears in the model picker");
    expect(issue?.steps.join(" ")).toContain("discovery, health, routing readiness, and credential state");
    expect(issue?.steps.join(" ")).toContain("If the model should be served locally");
    expect(issue?.steps.join(" ")).not.toContain("Check provider credentials and account model access");
  });
});

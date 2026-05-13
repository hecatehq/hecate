import { describe, expect, it } from "vitest";

import { providerFleetRepairHint, providerRepairHint, readinessRecommendation } from "./provider-readiness";

describe("readinessRecommendation", () => {
  it("uses backend operator_action before local fallback copy", () => {
    expect(readinessRecommendation({
      name: "health",
      status: "blocked",
      reason: "provider_unhealthy",
      operator_action: "Restart Ollama, then refresh.",
    })).toBe("Restart Ollama, then refresh.");
  });

  it("maps known readiness reasons to operator repair copy", () => {
    expect(readinessRecommendation({ name: "credentials", status: "blocked", reason: "credential_missing" }))
      .toContain("API key");
    expect(readinessRecommendation({ name: "models", status: "blocked", reason: "no_models" }))
      .toContain("pull or load");
  });
});

describe("providerRepairHint", () => {
  it("prioritizes missing credentials for cloud providers", () => {
    const hint = providerRepairHint({
      configuredProvider: { id: "anthropic", name: "Anthropic", kind: "cloud", credential_configured: false },
      runtimeProvider: { name: "anthropic", kind: "cloud", healthy: true, status: "healthy", models: ["claude"], model_count: 1 },
    });

    expect(hint.title).toBe("Credentials required");
    expect(hint.action).toContain("API key");
    expect(hint.tone).toBe("amber");
  });

  it("uses backend readiness summary and blocked-check actions before generic routing copy", () => {
    const hint = providerRepairHint({
      configuredProvider: { id: "ollama", name: "Ollama", kind: "local", credential_configured: false },
      runtimeProvider: {
        name: "ollama",
        kind: "local",
        healthy: false,
        status: "open",
        routing_blocked_reason: "circuit_open",
        readiness: { status: "blocked", message: "Ollama is cooling down.", operator_action: "Wait for cooldown." },
        readiness_checks: [{ name: "routing", status: "blocked", reason: "circuit_open" }],
      },
    });

    expect(hint.title).toBe("Provider blocked");
    expect(hint.message).toBe("Ollama is cooling down.");
    expect(hint.action).toBe("Wait for cooldown.");
  });

  it("explains reachable local providers with no discovered models", () => {
    const hint = providerRepairHint({
      configuredProvider: { id: "ollama", name: "Ollama", kind: "local", credential_configured: false },
      runtimeProvider: { name: "ollama", kind: "local", healthy: true, status: "healthy", models: [], model_count: 0 },
    });

    expect(hint.title).toBe("No models discovered");
    expect(hint.action).toContain("Pull or load");
  });

  it("uses friendlier titles for blocked readiness checks", () => {
    const hint = providerRepairHint({
      configuredProvider: { id: "ollama", name: "Ollama", kind: "local", credential_configured: false },
      runtimeProvider: {
        name: "ollama",
        kind: "local",
        healthy: true,
        status: "healthy",
        models: [],
        model_count: 0,
        readiness_checks: [{ name: "models", status: "blocked", reason: "no_models", message: "No models were discovered." }],
      },
    });

    expect(hint.title).toBe("No models discovered");
  });

  it("treats configured providers without runtime discovery as needing models", () => {
    const hint = providerRepairHint({
      configuredProvider: { id: "ollama", name: "Ollama", kind: "local", credential_configured: false },
    });

    expect(hint.title).toBe("No models discovered");
    expect(hint.action).toContain("refresh Connections");
  });
});

describe("providerFleetRepairHint", () => {
  it("returns the first provider repair action and a ready summary otherwise", () => {
    const statuses = new Map([
      ["anthropic", { name: "anthropic", kind: "cloud", healthy: true, status: "healthy", models: ["claude"], model_count: 1 }],
      ["ollama", { name: "ollama", kind: "local", healthy: true, status: "healthy", models: [], model_count: 0 }],
    ]);

    expect(providerFleetRepairHint([
      { id: "anthropic", name: "Anthropic", kind: "cloud", credential_configured: true },
      { id: "ollama", name: "Ollama", kind: "local", credential_configured: false },
    ], statuses)?.title).toBe("No models discovered");

    expect(providerFleetRepairHint([
      { id: "anthropic", name: "Anthropic", kind: "cloud", credential_configured: true },
    ], statuses)?.message).toContain("current readiness signal");
  });
});

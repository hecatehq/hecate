import { describe, expect, it } from "vitest";

import { providerFleetRepairHint, providerReadinessMeaning, providerRepairActionLabel, providerRepairHint, readinessRecommendation } from "./provider-readiness";

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
    expect(hint.actionKind).toBe("open_provider");
    expect(hint.providerID).toBe("anthropic");
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
    expect(hint.actionKind).toBe("refresh_providers");
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

  it("humanizes routing blocked reasons when readiness details are absent", () => {
    const hint = providerRepairHint({
      configuredProvider: { id: "anthropic", name: "Anthropic", kind: "cloud", credential_configured: true },
      runtimeProvider: {
        name: "anthropic",
        kind: "cloud",
        healthy: true,
        status: "healthy",
        models: ["claude"],
        model_count: 1,
        routing_blocked_reason: "credential_missing",
      },
    });

    expect(hint.title).toBe("Routing blocked");
    expect(hint.message).toContain("Missing credentials");
    expect(hint.message).not.toContain("credential_missing");
  });

  it("does not expose runtime provider names as selectable configured-provider ids", () => {
    const hint = providerRepairHint({
      runtimeProvider: {
        name: "runtime display name",
        kind: "local",
        healthy: true,
        status: "healthy",
        readiness_checks: [{ name: "credentials", status: "blocked", reason: "self_referential" }],
      },
    });

    expect(hint.actionKind).toBe("refresh_providers");
    expect(hint.providerID).toBeUndefined();
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
    ], statuses)?.message).toContain("No configured provider setup issue");
  });

  it("returns an add-provider action when the fleet is empty", () => {
    const hint = providerFleetRepairHint([], new Map());

    expect(hint).toMatchObject({
      title: "No provider configured",
      actionKind: "add_provider",
      tone: "amber",
    });
  });
});

describe("providerReadinessMeaning", () => {
  it("explains empty, blocked, no-model, and ready fleet states", () => {
    expect(providerReadinessMeaning({
      configuredCount: 0,
      readyCount: 0,
      blockedCount: 0,
      modelCount: 0,
    }).message).toContain("No model providers");

    expect(providerReadinessMeaning({
      configuredCount: 2,
      readyCount: 1,
      blockedCount: 1,
      modelCount: 1,
      repair: { title: "Credentials required", message: "", action: "Add an API key.", actionKind: "open_provider", tone: "amber" },
    }).message).toContain("Next: Add an API key.");

    expect(providerReadinessMeaning({
      configuredCount: 2,
      readyCount: 0,
      blockedCount: 1,
      modelCount: 1,
      repair: null,
    }).message).toContain("Providers exist");

    expect(providerReadinessMeaning({
      configuredCount: 1,
      readyCount: 1,
      blockedCount: 0,
      modelCount: 0,
    }).message).toContain("no models");

    expect(providerReadinessMeaning({
      configuredCount: 1,
      readyCount: 1,
      blockedCount: 0,
      modelCount: 2,
    }).message).toContain("1 provider ready with 2 discovered models");
  });
});

describe("providerRepairActionLabel", () => {
  it("keeps repair action labels canonical", () => {
    expect(providerRepairActionLabel("add_provider")).toBe("Add provider");
    expect(providerRepairActionLabel("open_provider")).toBe("Open provider");
    expect(providerRepairActionLabel("refresh_providers")).toBe("Refresh providers");
    expect(providerRepairActionLabel("none")).toBeNull();
  });
});

import { describe, expect, it } from "vitest";

import {
  modelSelectionImageInputCapability,
  modelSelectionHasNoToolCalling,
  resolveChatSetupRepairState,
  toolCallingSupportsTaskMode,
} from "./chat-setup-readiness";

describe("resolveChatSetupRepairState", () => {
  const base = {
    hasConfiguredProviders: true,
    modelRouteUnavailable: false,
    selectedModelIssue: null,
    workspace: "/repo",
    selectedAgentAvailable: true,
    anyAgentAvailable: true,
    externalAgentSetupRequired: false,
  };

  it("points chats without configured providers to Connections", () => {
    const repair = resolveChatSetupRepairState({
      ...base,
      target: "agent",
      workspaceRequired: false,
      hasConfiguredProviders: false,
      modelRouteUnavailable: true,
    });

    expect(repair).toMatchObject({
      kind: "no_provider",
      title: "No model provider configured",
      action: "open_connections",
      actionLabel: "Open Connections",
    });
  });

  it("explains that configured providers may need refresh or loaded models", () => {
    const repair = resolveChatSetupRepairState({
      ...base,
      target: "agent",
      workspaceRequired: false,
      modelRouteUnavailable: true,
    });

    expect(repair).toMatchObject({
      kind: "no_routable_model",
      title: "No routable model",
    });
    expect(repair?.message).toContain("refresh Connections");
    expect(repair?.message).toContain("loading a model");
  });

  it("uses backend suggested models as the primary selected-model repair", () => {
    const repair = resolveChatSetupRepairState({
      ...base,
      target: "agent",
      selectedModelIssue: {
        title: "Selected model is not ready",
        message: "Anthropic needs credentials.",
        providerLabel: "Anthropic",
        model: "claude-sonnet-4-6",
        suggestedModel: "gpt-4o-mini",
        details: [],
        steps: [],
      },
    });

    expect(repair).toMatchObject({
      kind: "selected_model_not_ready",
      action: "use_suggested_model",
      actionLabel: "Use gpt-4o-mini",
      suggestedModel: "gpt-4o-mini",
    });
  });

  it("routes stale selected models without a suggestion back to Connections", () => {
    const repair = resolveChatSetupRepairState({
      ...base,
      target: "agent",
      selectedModelIssue: {
        title: "Selected model is unavailable",
        message: "No routable provider reports mistral.",
        providerLabel: "Ollama",
        model: "mistral",
        suggestedModel: "",
        details: [],
        steps: [],
      },
    });

    expect(repair).toMatchObject({
      kind: "selected_model_not_ready",
      action: "open_connections",
      actionLabel: "Open Connections",
      suggestedModel: "",
    });
  });

  it("requires a workspace for task-backed Hecate Chat", () => {
    const repair = resolveChatSetupRepairState({
      ...base,
      target: "agent",
      workspace: "",
    });

    expect(repair).toMatchObject({
      kind: "workspace_required",
      action: "choose_workspace",
      actionLabel: "Choose workspace",
    });
  });

  it("asks for workspace before provider repair when a provider exists", () => {
    const repair = resolveChatSetupRepairState({
      ...base,
      target: "agent",
      workspace: "",
      modelRouteUnavailable: true,
    });

    expect(repair).toMatchObject({
      kind: "workspace_required",
      action: "choose_workspace",
    });
  });

  it("routes unavailable external agents to Connections", () => {
    const repair = resolveChatSetupRepairState({
      ...base,
      target: "external_agent",
      selectedAgentName: "Codex",
      selectedAgentAvailable: false,
    });

    expect(repair).toMatchObject({
      kind: "external_agent_unavailable",
      title: "Codex is unavailable",
      action: "open_connections",
    });
  });

  it("routes external-agent local auth setup to the setup action", () => {
    const repair = resolveChatSetupRepairState({
      ...base,
      target: "external_agent",
      selectedAgentID: "cursor_agent",
      selectedAgentName: "Cursor Agent",
      externalAgentSetupRequired: true,
    });

    expect(repair).toMatchObject({
      kind: "external_agent_setup",
      title: "Set up Cursor Agent",
      action: "open_agent_setup",
      actionLabel: "Open setup",
    });
    expect(repair?.message).toContain("cursor-agent login");
    expect(repair?.message).toContain("start an External Agent chat");
  });

  it("uses shared readiness sign-in copy for external-agent setup repairs", () => {
    const repair = resolveChatSetupRepairState({
      ...base,
      target: "external_agent",
      selectedAgentID: "claude_code",
      selectedAgentName: "Claude Code",
      externalAgentSetupRequired: true,
      selectedAgentReadiness: {
        kind: "sign_in",
        tone: "amber",
        label: "sign in",
        needsRepair: true,
        loginCommand: "claude /login",
        setupHint: "Install Claude Code and ensure claude is on PATH.",
        signInHint:
          "Run claude /login in Terminal, or set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN for the adapter environment.",
        detail: "Run `claude /login` in Terminal.",
        verifiedByProbe: false,
      },
    });

    expect(repair).toMatchObject({
      kind: "external_agent_setup",
      title: "Set up Claude Code",
      message:
        "Run claude /login in Terminal, or set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN for the adapter environment.",
      action: "open_agent_setup",
    });
  });

  it("uses Grok Build setup copy that mentions sign-in and model selection", () => {
    const repair = resolveChatSetupRepairState({
      ...base,
      target: "external_agent",
      selectedAgentID: "grok_build",
      selectedAgentName: "Grok Build",
      externalAgentSetupRequired: true,
    });

    expect(repair).toMatchObject({
      kind: "external_agent_setup",
      title: "Set up Grok Build",
    });
    expect(repair?.message).toContain("grok login");
    expect(repair?.message).toContain("model selected");
  });
});

describe("modelSelectionHasNoToolCalling", () => {
  it("returns true when the explicit provider/model selection has no tool support", () => {
    expect(
      modelSelectionHasNoToolCalling({
        model: "llama3.1:8b",
        providerFilter: "ollama",
        configuredProviders: [],
        models: [
          {
            id: "llama3.1:8b",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              capabilities: { tool_calling: "none" },
            },
          },
        ],
      }),
    ).toBe(true);
  });

  it("keeps auto provider usable when any matching route supports tools", () => {
    expect(
      modelSelectionHasNoToolCalling({
        model: "shared-model",
        providerFilter: "auto",
        configuredProviders: [],
        models: [
          {
            id: "shared-model",
            owned_by: "provider-a",
            metadata: {
              provider: "provider-a",
              capabilities: { tool_calling: "none" },
            },
          },
          {
            id: "shared-model",
            owned_by: "provider-b",
            metadata: {
              provider: "provider-b",
              capabilities: { tool_calling: "basic" },
            },
          },
        ],
      }),
    ).toBe(false);
  });

  it("returns true for unknown tool support so the turn falls back to direct chat", () => {
    expect(
      modelSelectionHasNoToolCalling({
        model: "smollm2:135m",
        providerFilter: "ollama",
        configuredProviders: [],
        models: [
          {
            id: "smollm2:135m",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              capabilities: { tool_calling: "unknown" },
            },
          },
        ],
      }),
    ).toBe(true);
  });
});

describe("toolCallingSupportsTaskMode", () => {
  it("requires a known tool-capable value", () => {
    expect(toolCallingSupportsTaskMode("basic")).toBe(true);
    expect(toolCallingSupportsTaskMode("parallel")).toBe(true);
    expect(toolCallingSupportsTaskMode("unknown")).toBe(false);
    expect(toolCallingSupportsTaskMode("none")).toBe(false);
    expect(toolCallingSupportsTaskMode(undefined)).toBe(false);
  });
});

describe("modelSelectionImageInputCapability", () => {
  it("enables images when an automatic route has an eligible image-capable candidate", () => {
    const models = [
      {
        id: "vision-model",
        owned_by: "provider-a",
        metadata: {
          provider: "provider-a",
          capabilities: { image_input: "supported" },
        },
      },
      {
        id: "vision-model",
        owned_by: "provider-b",
        metadata: {
          provider: "provider-b",
          capabilities: { image_input: "unknown" },
        },
      },
    ];

    expect(
      modelSelectionImageInputCapability({
        models,
        providerFilter: "auto",
        model: "vision-model",
        configuredProviders: [],
      }),
    ).toBe("supported");
    expect(
      modelSelectionImageInputCapability({
        models,
        providerFilter: "provider-a",
        model: "vision-model",
        configuredProviders: [],
      }),
    ).toBe("supported");
  });

  it("distinguishes known text-only models from missing capability metadata", () => {
    expect(
      modelSelectionImageInputCapability({
        providerFilter: "ollama",
        model: "text-model",
        configuredProviders: [],
        models: [
          {
            id: "text-model",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              capabilities: { image_input: "none" },
            },
          },
        ],
      }),
    ).toBe("none");
    expect(
      modelSelectionImageInputCapability({
        providerFilter: "ollama",
        model: "unreported-model",
        models: [],
        configuredProviders: [],
      }),
    ).toBe("unknown");
  });

  it("ignores image-capable routes that are not currently routable", () => {
    expect(
      modelSelectionImageInputCapability({
        providerFilter: "auto",
        model: "shared-model",
        configuredProviders: [],
        models: [
          {
            id: "shared-model",
            owned_by: "blocked-provider",
            metadata: {
              provider: "blocked-provider",
              capabilities: { image_input: "supported" },
              readiness: { ready: false, routing_ready: false },
            },
          },
          {
            id: "shared-model",
            owned_by: "ready-provider",
            metadata: {
              provider: "ready-provider",
              capabilities: { image_input: "none" },
              readiness: { ready: true, routing_ready: true },
            },
          },
        ],
      }),
    ).toBe("none");
  });

  it("matches a configured provider id to its runtime name", () => {
    const configuredProviders = [
      {
        id: "my-gateway",
        name: "My Gateway",
        kind: "cloud",
        protocol: "openai",
        base_url: "https://gateway.example/v1",
        credential_configured: true,
      },
    ];
    const models = [
      {
        id: "vision-model",
        owned_by: "My Gateway",
        metadata: {
          provider: "My Gateway",
          capabilities: { image_input: "supported", tool_calling: "basic" },
        },
      },
    ];

    expect(
      modelSelectionImageInputCapability({
        models,
        providerFilter: "my-gateway",
        model: "vision-model",
        configuredProviders,
      }),
    ).toBe("supported");
    expect(
      modelSelectionHasNoToolCalling({
        models,
        providerFilter: "my-gateway",
        model: "vision-model",
        configuredProviders,
      }),
    ).toBe(false);
  });
});

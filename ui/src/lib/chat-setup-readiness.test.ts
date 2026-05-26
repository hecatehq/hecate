import { describe, expect, it } from "vitest";

import {
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
      target: "model",
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
      target: "model",
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

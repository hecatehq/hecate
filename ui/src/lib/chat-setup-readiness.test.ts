import { describe, expect, it } from "vitest";

import {
  modelSelectionHasNoToolCalling,
  modelSelectionSupportsToolCalling,
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
      selectedAgentName: "Cursor Agent",
      externalAgentSetupRequired: true,
    });

    expect(repair).toMatchObject({
      kind: "external_agent_setup",
      title: "Set up Cursor Agent",
      action: "open_agent_setup",
      actionLabel: "Open setup",
    });
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

describe("modelSelectionSupportsToolCalling", () => {
  it("returns true when the scoped provider/model route is tool-capable", () => {
    expect(
      modelSelectionSupportsToolCalling({
        model: "qwen2.5",
        providerFilter: "ollama",
        models: [
          {
            id: "qwen2.5",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              capabilities: { tool_calling: "basic" },
            },
          },
        ],
      }),
    ).toBe(true);
  });

  it("returns false for a known non-tool model", () => {
    expect(
      modelSelectionSupportsToolCalling({
        model: "smollm2:135m",
        providerFilter: "ollama",
        models: [
          {
            id: "smollm2:135m",
            owned_by: "ollama",
            metadata: {
              provider: "ollama",
              capabilities: { tool_calling: "none" },
            },
          },
        ],
      }),
    ).toBe(false);
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

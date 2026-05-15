import { describe, expect, it } from "vitest";

import { resolveChatSetupRepairState } from "./chat-setup-readiness";

describe("resolveChatSetupRepairState", () => {
  const base = {
    hasConfiguredProviders: true,
    modelRouteUnavailable: false,
    selectedModelIssue: null,
    toolsDisabledForModel: false,
    workspace: "/repo",
    selectedAgentAvailable: true,
    anyAgentAvailable: true,
    claudeCodeSetupRequired: false,
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

  it("blocks Hecate Agent when tools are disabled for the selected model", () => {
    const repair = resolveChatSetupRepairState({
      ...base,
      target: "agent",
      toolsDisabledForModel: true,
    });

    expect(repair).toMatchObject({
      kind: "tools_disabled",
      action: "enable_tools",
      title: "Tools are disabled for this model",
    });
  });

  it("requires a workspace for task-backed Hecate Agent chats", () => {
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

  it("routes Claude Code credential setup to the guided setup action", () => {
    const repair = resolveChatSetupRepairState({
      ...base,
      target: "external_agent",
      selectedAgentName: "Claude Code",
      claudeCodeSetupRequired: true,
    });

    expect(repair).toMatchObject({
      kind: "claude_code_setup",
      action: "open_agent_setup",
      actionLabel: "Open setup",
    });
  });
});

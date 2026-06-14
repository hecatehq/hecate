import { describe, expect, it } from "vitest";

import type { ChatConfigOptionRecord } from "../../types/chat";
import {
  agentConfigOptionKind,
  agentConfigOptionLabel,
  externalAgentRequiresModelSelection,
  prioritizeAgentConfigOptions,
} from "./agentConfigOptions";

function option(overrides: Partial<ChatConfigOptionRecord>): ChatConfigOptionRecord {
  return {
    id: "option",
    name: "Option",
    type: "select",
    ...overrides,
  };
}

describe("agent config option helpers", () => {
  it("classifies common ACP controls from category, id, or name", () => {
    expect(
      agentConfigOptionKind(option({ id: "native_model", name: "Native", category: "model" })),
    ).toBe("model");
    expect(agentConfigOptionKind(option({ id: "model", name: "Model" }))).toBe("model");
    expect(agentConfigOptionKind(option({ id: "thinking_level", name: "Level of thinking" }))).toBe(
      "thought_level",
    );
    expect(
      agentConfigOptionKind(option({ id: "reasoning_effort", name: "Reasoning effort" })),
    ).toBe("thought_level");
    expect(agentConfigOptionKind(option({ id: "approval_mode", name: "Approval mode" }))).toBe(
      "mode",
    );
    expect(agentConfigOptionKind(option({ id: "system_prompt", name: "System prompt" }))).toBe(
      "instructions",
    );
  });

  it("uses one classifier for labels and model-required detection", () => {
    const model = option({
      id: "model",
      name: "Model",
      current_value: "__hecate_no_model_selected__",
    });
    const thinking = option({
      id: "thinking_level",
      name: "Level of thinking",
      current_value: "medium",
    });

    expect(agentConfigOptionLabel(model)).toBe("model");
    expect(agentConfigOptionLabel(thinking)).toBe("reasoning");
    expect(externalAgentRequiresModelSelection([thinking, model])).toBe(true);
  });

  it("prioritizes common controls before generic controls without relying on categories", () => {
    const ordered = prioritizeAgentConfigOptions([
      option({ id: "verbosity", name: "Verbosity" }),
      option({ id: "approval_mode", name: "Approval mode" }),
      option({ id: "thinking_level", name: "Level of thinking" }),
      option({ id: "model", name: "Model" }),
      option({ id: "system_prompt", name: "System prompt" }),
    ]);

    expect(ordered.map((item) => item.id)).toEqual([
      "system_prompt",
      "model",
      "thinking_level",
      "approval_mode",
      "verbosity",
    ]);
  });
});

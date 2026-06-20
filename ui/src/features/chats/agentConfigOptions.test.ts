import { describe, expect, it } from "vitest";

import {
  agentConfigOptionKind,
  agentConfigOptionLabel,
  externalAgentRequiresModelSelection,
  prioritizeAgentConfigOptions,
} from "./agentConfigOptions";
import type { ChatConfigOptionRecord } from "../../types/chat";

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
    expect(agentConfigOptionKind(option({ id: "web_search", name: "Web search" }))).toBe("tool");
    expect(agentConfigOptionKind(option({ id: "tools", name: "Tools", category: "tool" }))).toBe(
      "tool",
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
      option({ id: "web_search", name: "Web search" }),
    ]);

    expect(ordered.map((item) => item.id)).toEqual([
      "system_prompt",
      "model",
      "thinking_level",
      "approval_mode",
      "web_search",
      "verbosity",
    ]);
  });

  it("prefers the exact model picker over earlier model-named decoys", () => {
    const options: ChatConfigOptionRecord[] = [
      option({
        id: "model_info",
        name: "Model info",
        current_value: "metadata",
      }),
      option({
        id: "model",
        name: "Model",
        category: "model",
        current_value: "__hecate_no_model_selected",
      }),
    ];

    expect(externalAgentRequiresModelSelection(options)).toBe(true);
  });

  it("ignores model-named decoys when the exact model picker is configured", () => {
    const options: ChatConfigOptionRecord[] = [
      option({
        id: "default_model_mode",
        name: "Default model mode",
        current_value: "",
      }),
      option({
        id: "model",
        name: "Model",
        category: "model",
        current_value: "smart",
      }),
    ];

    expect(externalAgentRequiresModelSelection(options)).toBe(false);
  });

  it("falls back to all heuristic model selects when no exact picker exists", () => {
    const options: ChatConfigOptionRecord[] = [
      option({
        id: "preferred_model",
        name: "Preferred model",
        current_value: "fast",
      }),
      option({
        id: "fallback_model",
        name: "Fallback model",
        current_value: "",
      }),
    ];

    expect(externalAgentRequiresModelSelection(options)).toBe(true);
  });

  it("classifies config options by tokens instead of incidental substrings", () => {
    expect(
      agentConfigOptionKind(option({ id: "remodeling", name: "Remodeling", category: "" })),
    ).toBe("other");
    expect(agentConfigOptionKind(option({ id: "commode", name: "Commode", category: "" }))).toBe(
      "other",
    );
    expect(
      agentConfigOptionKind(option({ id: "reasoning", name: "Reasoning", category: "" })),
    ).toBe("thought_level");
    expect(
      agentConfigOptionKind(
        option({
          id: "system_prompt",
          name: "System prompt",
          category: "",
          type: "text",
        }),
      ),
    ).toBe("instructions");
  });

  it("keeps model controls before mode controls", () => {
    const sorted = prioritizeAgentConfigOptions([
      option({ id: "auto_approve", name: "Mode", category: "mode", type: "boolean" }),
      option({ id: "model", name: "Model", category: "model" }),
    ]);

    expect(sorted.map((option) => option.id)).toEqual(["model", "auto_approve"]);
  });
});

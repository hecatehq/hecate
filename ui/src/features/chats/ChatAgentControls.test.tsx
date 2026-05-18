import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AgentAdapterRecord } from "../../types/agent-adapter";

import { NewChatAgentButton } from "./ChatAgentControls";

function makeAdapter(overrides: Partial<AgentAdapterRecord> = {}): AgentAdapterRecord {
  return {
    id: "codex",
    name: "Codex",
    kind: "codex",
    command: "codex-acp",
    available: true,
    status: "available",
    auth_status: "ok",
    ...overrides,
  };
}

describe("NewChatAgentButton", () => {
  it("creates a Hecate chat from a compact primary button", async () => {
    const onCreate = vi.fn();
    render(
      <NewChatAgentButton
        value="hecate"
        adapters={[]}
        healthByID={new Map()}
        onChange={() => {}}
        onCreate={onCreate}
      />,
    );

    const create = screen.getByRole("button", { name: "New Hecate chat" });
    expect(create).toHaveStyle({ minHeight: "30px" });

    await userEvent.setup().click(create);
    expect(onCreate).toHaveBeenCalledOnce();
  });

  it("keeps the agent menu trigger at the same compact height", () => {
    render(
      <NewChatAgentButton
        value="hecate"
        adapters={[makeAdapter()]}
        healthByID={new Map()}
        onChange={() => {}}
        onCreate={() => {}}
      />,
    );

    expect(screen.getByRole("button", { name: "Choose agent for new chat" })).toHaveStyle({
      minHeight: "30px",
    });
  });

  it("switches the selected agent from the dropdown before creating a chat", async () => {
    const onChange = vi.fn();
    render(
      <NewChatAgentButton
        value="hecate"
        adapters={[makeAdapter()]}
        healthByID={new Map()}
        onChange={onChange}
        onCreate={() => {}}
      />,
    );

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Choose agent for new chat" }));
    await user.click(screen.getByRole("option", { name: /codex/i }));

    expect(onChange).toHaveBeenCalledWith("codex");
  });
});

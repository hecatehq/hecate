import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AgentAdapterRecord } from "../../types/agent-adapter";

import {
  ExternalAgentConfigControls,
  NewChatAgentButton,
  chatAgentOption,
  chatAgentOptionStatus,
} from "./ChatAgentControls";

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
  it("keeps unverified auth selectable without labeling it as auth required", () => {
    const status = chatAgentOptionStatus(
      "claude_code",
      makeAdapter({
        id: "claude_code",
        name: "Claude Code",
        auth_status: "unknown",
        auth_error: "Claude Code config is present on disk.",
      }),
      undefined,
    );

    expect(status.label).toBe("check");
    expect(status.ready).toBe(true);
    expect(status.title).toContain("config is present");
  });

  it("explains ready state instead of using a raw adapter path as the tooltip", () => {
    const discovered = chatAgentOptionStatus(
      "cursor_agent",
      makeAdapter({
        id: "cursor_agent",
        name: "Cursor Agent",
        command: "cursor-agent",
        path: "/Users/test/.local/bin/cursor-agent",
      }),
      undefined,
    );
    expect(discovered.label).toBe("ready");
    expect(discovered.title).toContain("Cursor Agent is available");
    expect(discovered.title).toContain("full ACP readiness check");
    expect(discovered.title).toContain("/Users/test/.local/bin/cursor-agent");

    const probed = chatAgentOptionStatus(
      "cursor_agent",
      makeAdapter({ id: "cursor_agent", name: "Cursor Agent" }),
      {
        adapter_id: "cursor_agent",
        status: "ready",
        stage: "ready",
        path: "/Users/test/.local/bin/cursor-agent",
        duration_ms: 80,
      },
    );
    expect(probed.title).toContain("verified agent startup, auth, and ACP session creation");
  });

  it("preserves an external-agent selection while the agent catalog loads", () => {
    const option = chatAgentOption("grok_build", []);

    expect(option.id).toBe("grok_build");
    expect(option.label).toBe("Grok Build");
    expect(option.title).toContain("agent catalog");
  });

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
    expect(onCreate).toHaveBeenCalledWith("hecate");
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

  it("falls back to a normal Hecate create button when the remembered agent is unavailable", async () => {
    const onChange = vi.fn();
    const onCreate = vi.fn();
    render(
      <NewChatAgentButton
        value="cursor_agent"
        adapters={[
          makeAdapter({
            id: "cursor_agent",
            name: "Cursor Agent",
            command: "cursor-agent",
            available: true,
          }),
        ]}
        healthByID={
          new Map([
            [
              "cursor_agent",
              {
                adapter_id: "cursor_agent",
                status: "error",
                stage: "ready",
                error: "forced app CLI missing by HECATE_AGENT_ADAPTER_DEV_OVERRIDES",
                hint: "Install Cursor with Agent support, then sign in with Cursor Agent.",
                duration_ms: 0,
              },
            ],
          ])
        }
        disableUnavailable
        onChange={onChange}
        onCreate={onCreate}
      />,
    );

    const create = screen.getByRole("button", { name: "New Hecate chat" });
    expect(create).not.toBeDisabled();
    expect(create).toHaveStyle({ color: "var(--accent-fg)" });
    expect(onChange).toHaveBeenCalledWith("hecate");

    await userEvent.setup().click(create);
    expect(onCreate).toHaveBeenCalledWith("hecate");
  });

  it("uses actionable disabled option tooltips", async () => {
    render(
      <NewChatAgentButton
        value="hecate"
        adapters={[
          makeAdapter({
            id: "cursor_agent",
            name: "Cursor Agent",
            command: "cursor-agent",
            available: true,
          }),
        ]}
        healthByID={
          new Map([
            [
              "cursor_agent",
              {
                adapter_id: "cursor_agent",
                status: "error",
                stage: "ready",
                error: "forced app CLI missing by HECATE_AGENT_ADAPTER_DEV_OVERRIDES",
                hint: "Install Cursor with Agent support, then sign in with Cursor Agent.",
                duration_ms: 0,
              },
            ],
          ])
        }
        disableUnavailable
        onChange={() => {}}
        onCreate={() => {}}
      />,
    );

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Choose agent for new chat" }));
    const cursor = screen.getByRole("option", { name: /Cursor Agent/ });
    expect(cursor).toHaveAttribute("aria-disabled", "true");
    expect(cursor).toHaveAttribute(
      "title",
      expect.stringContaining("Open Connections to set up Cursor Agent"),
    );
    expect(cursor.getAttribute("title")).not.toContain("HECATE_AGENT_ADAPTER_DEV_OVERRIDES");
    expect(cursor.getAttribute("title")).not.toContain("forced");
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

  it("builds external-agent options from the agent catalog", async () => {
    const onChange = vi.fn();
    render(
      <NewChatAgentButton
        value="hecate"
        adapters={[
          makeAdapter(),
          makeAdapter({
            id: "grok_build",
            name: "Grok Build",
            command: "grok",
          }),
        ]}
        healthByID={new Map()}
        onChange={onChange}
        onCreate={() => {}}
      />,
    );

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Choose agent for new chat" }));
    expect(screen.getByRole("option", { name: /Grok Build/ })).toBeTruthy();

    await user.click(screen.getByRole("option", { name: /Grok Build/ }));
    expect(onChange).toHaveBeenCalledWith("grok_build");
  });
});

describe("ExternalAgentConfigControls", () => {
  it("renders a composer model picker for Grok Build launch models", async () => {
    const onChange = vi.fn(async () => true);
    render(
      <ExternalAgentConfigControls
        placement="composer"
        session={{
          id: "a1",
          agent_id: "grok_build",
          config_options: [
            {
              id: "model",
              name: "Model",
              category: "model",
              type: "select",
              current_value: "__hecate_no_model_selected__",
              options: [
                { value: "__hecate_no_model_selected__", name: "Pick a model" },
                { value: "model-a", name: "Model A" },
              ],
            },
          ],
        }}
        onChange={onChange}
      />,
    );

    const user = userEvent.setup();
    const model = screen.getByRole("button", { name: "Model" });
    expect(model).toHaveTextContent("Pick a model");

    await user.click(model);
    await user.click(screen.getByRole("option", { name: /Model A/ }));

    expect(onChange).toHaveBeenCalledWith("a1", "model", "model-a");
  });
});

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
    command: "codex-acp-adapter",
    available: true,
    status: "available",
    auth_status: "ok",
    supports_authenticate: false,
    supports_logout: false,
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

    expect(status.label).toBe("available");
    expect(status.ready).toBe(true);
    expect(status.title).toContain("config is present");
    expect(status.title).toContain("New chat re-resolves the executable");
    expect(status.title).toContain("first message verifies any deferred vendor launch");
  });

  it("labels passive discovery available and explains launch-time ACP verification", () => {
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
    expect(discovered.label).toBe("available");
    expect(discovered.title).toContain("Cursor Agent is available");
    expect(discovered.title).toContain("New chat re-resolves the executable");
    expect(discovered.title).toContain("first message verifies any deferred vendor launch");
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
    expect(probed.label).toBe("checked");
    expect(probed.title).toContain("last Cursor Agent diagnostic completed ACP startup");
    expect(probed.title).toContain("without sending a prompt");
    expect(probed.title).toContain("first message checks any deferred vendor launch");
  });

  it("does not let a ready ACP diagnostic hide an explicit sign-in state", () => {
    const result = chatAgentOptionStatus(
      "claude_code",
      makeAdapter({
        id: "claude_code",
        name: "Claude Code",
        auth_status: "unauthenticated",
        auth_error: "Run claude /login in Terminal.",
      }),
      {
        adapter_id: "claude_code",
        status: "ready",
        stage: "session",
        duration_ms: 80,
      },
    );

    expect(result).toMatchObject({
      label: "auth",
      ready: true,
    });
    expect(result.title).toContain("Run claude /login");
    expect(result.title).not.toContain("diagnostic completed ACP startup");
  });

  it.each([
    {
      status: "auth_required",
      stage: "initialize",
      hint: "Run cursor-agent login.",
      expectedLabel: "auth",
    },
    {
      status: "error",
      stage: "initialize",
      hint: "The last diagnostic failed.",
      expectedLabel: "issue",
    },
    {
      status: "not_installed",
      stage: "resolve",
      hint: "The executable was missing during the last diagnostic.",
      expectedLabel: "diagnostic",
    },
  ])(
    "keeps a discovered agent selectable after a cached $status diagnostic",
    ({ status, stage, hint, expectedLabel }) => {
      const result = chatAgentOptionStatus(
        "cursor_agent",
        makeAdapter({
          id: "cursor_agent",
          name: "Cursor Agent",
          command: "cursor-agent",
          available: true,
        }),
        {
          adapter_id: "cursor_agent",
          status,
          stage,
          hint,
          duration_ms: 80,
        },
      );

      expect(result).toMatchObject({
        label: expectedLabel,
        ready: true,
      });
    },
  );

  it("keeps current passive discovery authoritative over a stale ready diagnostic", () => {
    const result = chatAgentOptionStatus(
      "cursor_agent",
      makeAdapter({
        id: "cursor_agent",
        name: "Cursor Agent",
        command: "cursor-agent",
        available: false,
        status: "missing",
      }),
      {
        adapter_id: "cursor_agent",
        status: "ready",
        stage: "session",
        duration_ms: 80,
      },
    );

    expect(result).toMatchObject({
      label: "setup",
      ready: false,
    });
  });

  it("blocks launch when the current remote credential gate fails", () => {
    const result = chatAgentOptionStatus(
      "cursor_agent",
      makeAdapter({
        id: "cursor_agent",
        name: "Cursor Agent",
        command: "cursor-agent",
        available: false,
        status: "missing",
        auth_status: "unauthenticated",
        auth_error: "Set CURSOR_API_KEY for the runtime.",
        remote_credential_mode: "api_key",
        remote_credential_ok: false,
        remote_credential_hint: "Set CURSOR_API_KEY for the runtime.",
      }),
      {
        adapter_id: "cursor_agent",
        status: "ready",
        stage: "session",
        duration_ms: 80,
      },
    );

    expect(result).toMatchObject({
      label: "auth",
      ready: false,
    });
    expect(result.title).toContain("Set CURSOR_API_KEY");
    expect(result.title).toContain("required remote credential");
    expect(result.title).not.toContain("cursor-agent login");
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

  it("discloses the selected executable and ACP launch before creating an external chat", async () => {
    const onCreate = vi.fn();
    render(
      <NewChatAgentButton
        value="codex"
        adapters={[
          makeAdapter({
            path: "/Applications/Codex.app/Contents/Resources/codex",
          }),
        ]}
        healthByID={new Map()}
        onChange={() => {}}
        onCreate={onCreate}
      />,
    );

    const create = screen.getByRole("button", { name: "New Codex chat" });
    expect(create).toHaveAccessibleDescription(
      "Prepares Codex for an ACP chat. Last discovered at /Applications/Codex.app/Contents/Resources/codex; Hecate resolves the executable again during session setup. Any deferred vendor launch and authentication happen when the first message is sent.",
    );
    expect(create).toHaveAttribute(
      "title",
      "Prepares Codex for an ACP chat. Last discovered at /Applications/Codex.app/Contents/Resources/codex; Hecate resolves the executable again during session setup. Any deferred vendor launch and authentication happen when the first message is sent",
    );
    expect(screen.getByText("/Applications/Codex.app/Contents/Resources/codex")).toBeVisible();

    await userEvent.setup().click(create);
    expect(onCreate).toHaveBeenCalledWith("codex");
  });

  it("discloses current discovery instead of a stale diagnostic executable", () => {
    render(
      <NewChatAgentButton
        value="codex"
        adapters={[
          makeAdapter({
            path: "/Applications/Codex.app/Contents/Resources/codex",
          }),
        ]}
        healthByID={
          new Map([
            [
              "codex",
              {
                adapter_id: "codex",
                status: "ready",
                stage: "ready",
                path: "/usr/local/bin/codex-old",
                duration_ms: 80,
              },
            ],
          ])
        }
        onChange={() => {}}
        onCreate={() => {}}
      />,
    );

    const create = screen.getByRole("button", { name: "New Codex chat" });
    expect(create).toHaveAccessibleDescription(
      "Prepares Codex for an ACP chat. Last discovered at /Applications/Codex.app/Contents/Resources/codex; Hecate resolves the executable again during session setup. Any deferred vendor launch and authentication happen when the first message is sent.",
    );
    expect(create).not.toHaveAccessibleDescription(/codex-old/);
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

  it("does not replace a remembered agent because its cached diagnostic failed", async () => {
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

    const create = screen.getByRole("button", { name: "New Cursor Agent chat" });
    expect(create).not.toBeDisabled();
    expect(create).toHaveStyle({ color: "var(--accent-fg)" });
    expect(onChange).not.toHaveBeenCalled();

    await userEvent.setup().click(create);
    expect(onCreate).toHaveBeenCalledWith("cursor_agent");
  });

  it("opens focused setup from disabled agent options", async () => {
    const onSetupAgent = vi.fn();
    render(
      <NewChatAgentButton
        value="hecate"
        adapters={[
          makeAdapter({
            id: "cursor_agent",
            name: "Cursor Agent",
            command: "cursor-agent",
            available: false,
            status: "missing",
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
        onSetupAgent={onSetupAgent}
      />,
    );

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Choose agent for new chat" }));
    const cursor = screen.getByRole("option", { name: /Cursor Agent/ });
    expect(cursor).not.toHaveAttribute("aria-disabled");
    expect(cursor).toHaveAttribute(
      "title",
      expect.stringContaining("Open Connections to set up Cursor Agent"),
    );
    expect(cursor.getAttribute("title")).not.toContain("HECATE_AGENT_ADAPTER_DEV_OVERRIDES");
    expect(cursor.getAttribute("title")).not.toContain("forced");

    await user.click(cursor);
    expect(onSetupAgent).toHaveBeenCalledWith("cursor_agent");
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

  it("keeps ACP model and thinking controls prominent for every external agent", () => {
    const agentIDs = ["codex", "claude_code", "cursor_agent", "grok_build"];

    for (const agentID of agentIDs) {
      const view = render(
        <ExternalAgentConfigControls
          placement="composer"
          session={{
            id: `${agentID}_chat`,
            agent_id: agentID,
            config_options: [
              {
                id: "web_search",
                name: "Web search",
                type: "select",
                current_value: "auto",
                options: [
                  { value: "off", name: "Off" },
                  { value: "auto", name: "Auto" },
                ],
              },
              {
                id: "verbosity",
                name: "Verbosity",
                type: "select",
                current_value: "normal",
                options: [
                  { value: "normal", name: "Normal" },
                  { value: "detailed", name: "Detailed" },
                ],
              },
              {
                id: "model",
                name: "Model",
                type: "select",
                current_value: "agent-model-fast",
                options: [
                  { value: "agent-model-fast", name: "Agent Model Fast" },
                  { value: "agent-model-pro", name: "Agent Model Pro" },
                ],
              },
              {
                id: "thinking_level",
                name: "Level of thinking",
                type: "select",
                current_value: "medium",
                options: [
                  { value: "low", name: "Low" },
                  { value: "medium", name: "Medium" },
                  { value: "high", name: "High" },
                ],
              },
              {
                id: "approval_mode",
                name: "Approval mode",
                type: "select",
                current_value: "ask",
                options: [
                  { value: "ask", name: "Ask" },
                  { value: "auto", name: "Auto" },
                ],
              },
            ],
          }}
          onChange={async () => true}
        />,
      );

      expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Agent Model Fast");
      expect(screen.getByRole("button", { name: "Level of thinking" })).toHaveTextContent("Medium");
      expect(screen.getByRole("button", { name: "Web search" })).toHaveTextContent("Auto");
      expect(screen.queryByRole("button", { name: "Verbosity" })).toBeNull();
      view.unmount();
    }
  });
});

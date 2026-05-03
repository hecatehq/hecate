import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ChatView } from "./ChatView";
import { createRuntimeConsoleActions, createRuntimeConsoleFixture } from "../../test/runtime-console-fixture";

function setup(stateOverrides = {}, actionOverrides = {}) {
  const state = createRuntimeConsoleFixture({
    providerScopedModels: [
      { id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } },
    ],
    ...stateOverrides,
  });
  const actions = { ...createRuntimeConsoleActions(), ...actionOverrides };
  return { state, actions };
}

describe("ChatView input", () => {
  it("disables the send button when message is empty", () => {
    const { state, actions } = setup({ message: "" });
    render(<ChatView state={state} actions={actions} />);
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });

  it("enables the send button when message has content", () => {
    const { state, actions } = setup({ message: "hello" });
    render(<ChatView state={state} actions={actions} />);
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(false);
  });

  it("disables model send when no provider is configured", () => {
    const { state, actions } = setup({
      message: "hello",
      controlPlaneConfig: { backend: "memory", providers: [], policy_rules: [], pricebook: [], events: [] },
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "process", command: "codex", available: true, status: "available", cost_mode: "external" },
      ],
    });
    render(<ChatView state={state} actions={actions} />);
    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
    expect(screen.getByText("No routable model")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Add provider/i })).toBeTruthy();
  });

  it("shows a first-run setup state when providers and agents are unavailable", () => {
    const { state, actions } = setup({
      message: "hello",
      controlPlaneConfig: { backend: "memory", providers: [], policy_rules: [], pricebook: [], events: [] },
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "process", command: "codex", available: false, status: "missing", cost_mode: "external" },
      ],
    });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText("Nothing runnable yet")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Add provider/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Check agents/i })).toBeTruthy();
  });

  it("calls setMessage as user types", async () => {
    const setMessage = vi.fn();
    // Start with empty message so the assertion sees only what we typed.
    const { state, actions } = setup({ message: "" }, { setMessage });
    render(<ChatView state={state} actions={actions} />);
    const ta = screen.getByPlaceholderText(/Message/i) as HTMLTextAreaElement;
    const user = userEvent.setup();
    await user.type(ta, "h");
    expect(setMessage).toHaveBeenCalledWith("h");
  });
});

describe("ChatView Enter switch", () => {
  it("renders the segmented Enter/⌘+Enter or Ctrl+Enter switch", () => {
    const { state, actions } = setup();
    render(<ChatView state={state} actions={actions} />);
    // The switch is one of the toggle buttons in the input toolbar.
    const buttons = screen.getAllByRole("button");
    const labels = buttons.map(b => b.textContent?.trim()).filter(Boolean);
    const hasEnterToggle = labels.some(l => l === "↵ to send" || /[⌘+|Ctrl\+]\+?↵ to send/.test(l!));
    expect(hasEnterToggle).toBe(true);
  });
});

describe("ChatView chats sidebar", () => {
  it("shows 'No chats yet' when chatSessions is empty", () => {
    const { state, actions } = setup({ chatSessions: [] });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText(/No chats yet/i)).toBeTruthy();
  });

  it("renders one row per chat with title", () => {
    const { state, actions } = setup({
      chatSessions: [
        { id: "s1", title: "First chat", message_count: 4, provider_call_count: 2, updated_at: "2026-04-25T00:00:00Z" } as any,
        { id: "s2", title: "Second chat", message_count: 2, provider_call_count: 1, updated_at: "2026-04-25T01:00:00Z" } as any,
      ],
    });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText("First chat")).toBeTruthy();
    expect(screen.getByText("Second chat")).toBeTruthy();
  });

  it("calls selectChatSession when clicking a chat row", async () => {
    const selectChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup({
      chatSessions: [{ id: "s1", title: "Pick me", message_count: 0, provider_call_count: 0 } as any],
    }, { selectChatSession });
    render(<ChatView state={state} actions={actions} />);
    const user = userEvent.setup();
    await user.click(screen.getByText("Pick me"));
    expect(selectChatSession).toHaveBeenCalledWith("s1");
  });
});

describe("ChatView agent target", () => {
  it("does not show provider setup actions when agent chat has no available CLI", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      message: "run codex",
      controlPlaneConfig: { backend: "memory", providers: [], policy_rules: [], pricebook: [], events: [] },
      agentAdapterID: "codex",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "process", command: "codex", available: false, status: "missing", error: "exec: \"codex\": executable file not found in $PATH", cost_mode: "external" },
      ],
    });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.getByText("Codex is unavailable")).toBeTruthy();
    expect(screen.getByText(/could not start Codex/)).toBeTruthy();
    expect(screen.getAllByText("Codex").length).toBeGreaterThan(0);
    expect(screen.getByText(/Install Codex CLI/)).toBeTruthy();
    expect(screen.getByText(/exec: "codex"/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Add provider/i })).toBeNull();
  });

  it("renders external agent controls and locks the adapter for an active chat", async () => {
    const setChatTarget = vi.fn();
    const setAgentAdapterID = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      agentAdapterID: "codex",
      agentWorkspace: "/tmp/hecate",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "process", command: "codex", available: true, status: "available", cost_mode: "external" },
        { id: "claude_code", name: "Claude Code", kind: "process", command: "claude", available: false, status: "missing", cost_mode: "external" },
      ],
      agentChatSessions: [
        { id: "a1", title: "Codex work", adapter_id: "codex", workspace: "/tmp/hecate", status: "completed", message_count: 2 } as any,
      ],
      activeAgentChatSessionID: "a1",
      activeAgentChatSession: {
        id: "a1",
        title: "Codex work",
        adapter_id: "codex",
        workspace: "/tmp/hecate",
        status: "completed",
        messages: [
          { id: "m1", role: "user", content: "review this", created_at: "2026-05-03T10:00:00Z" },
          {
            id: "m2",
            run_id: "agent_run_c4",
            trace_id: "0123456789abcdef0123456789abcdef",
            role: "assistant",
            content: "Looks good.",
            raw_output: `{"type":"message","content":"Looks good."}`,
            adapter_id: "codex",
            adapter_name: "Codex",
            status: "completed",
            cost_mode: "external",
            diff_stat: "1 file changed",
            diff: "diff --git a/README.md b/README.md",
            created_at: "2026-05-03T10:00:01Z",
            activities: [
              { type: "started", status: "completed", title: "Starting external agent", detail: "Codex process launched" },
              { type: "completed", status: "completed", title: "Final answer" },
            ],
          },
        ],
      } as any,
    }, { setChatTarget, setAgentAdapterID });
    render(<ChatView state={state} actions={actions} />);

    expect(screen.queryByDisplayValue("/tmp/hecate")).toBeNull();
    expect(screen.getByRole("button", { name: /workspace/i })).toBeTruthy();
    expect(screen.getAllByText("Codex work").length).toBeGreaterThan(0);
    expect(screen.getByText("Looks good.")).toBeTruthy();
    expect(screen.getByText(/trace 01234567/)).toBeTruthy();
    expect(screen.getByText("Starting external agent")).toBeTruthy();
    expect(screen.getByText("workspace diff · 1 file changed")).toBeTruthy();
    expect(screen.getByText("raw process output")).toBeTruthy();
    expect(screen.getByText("completed")).toBeTruthy();
    const user = userEvent.setup();
    const adapterPicker = screen.getByRole("button", { name: "External agent adapter" }) as HTMLButtonElement;
    expect(adapterPicker.disabled).toBe(true);
    expect(adapterPicker.title).toContain("Start a new chat");
    await user.click(adapterPicker);
    expect(screen.queryByText("Claude Code")).toBeNull();
    expect(setAgentAdapterID).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Model" }));
    expect(setChatTarget).toHaveBeenCalledWith("model");
  });

  it("allows choosing an agent before an agent chat is created", async () => {
    const setAgentAdapterID = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      agentAdapterID: "codex",
      activeAgentChatSessionID: "",
      activeAgentChatSession: null,
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "process", command: "codex", available: true, status: "available", cost_mode: "external" },
        { id: "claude_code", name: "Claude Code", kind: "process", command: "claude", available: true, status: "available", cost_mode: "external" },
      ],
    }, { setAgentAdapterID });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "External agent adapter" }));
    await user.click(screen.getByText("Claude Code"));
    expect(setAgentAdapterID).toHaveBeenCalledWith("claude_code");
  });

  it("opens the workspace picker action from the folder button", async () => {
    const chooseAgentWorkspace = vi.fn(async () => true);
    const { state, actions } = setup({
      chatTarget: "agent",
      agentWorkspace: "",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "process", command: "codex", available: true, status: "available", cost_mode: "external" },
      ],
    }, { chooseAgentWorkspace });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getByTitle("Choose workspace folder"));
    expect(chooseAgentWorkspace).toHaveBeenCalled();
  });

  it("allows pasting a workspace path when the folder dialog is unavailable", async () => {
    const chooseAgentWorkspace = vi.fn(async () => false);
    const setAgentWorkspace = vi.fn();
    const { state, actions } = setup({
      chatTarget: "agent",
      agentWorkspace: "",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "process", command: "codex", available: true, status: "available", cost_mode: "external" },
      ],
    }, { chooseAgentWorkspace, setAgentWorkspace });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    await user.click(screen.getByTitle("Choose workspace folder"));
    await user.type(screen.getByPlaceholderText("/Users/alice/dev/project"), "/workspaces/hecate");
    await user.click(screen.getByRole("button", { name: "Use" }));

    expect(setAgentWorkspace).toHaveBeenCalledWith("/workspaces/hecate");
  });

  it("requires a workspace before sending to an external agent", () => {
    const { state, actions } = setup({
      chatTarget: "agent",
      message: "run codex",
      agentWorkspace: "",
      agentAdapters: [
        { id: "codex", name: "Codex", kind: "process", command: "codex", available: true, status: "available", cost_mode: "external" },
      ],
    });
    render(<ChatView state={state} actions={actions} />);

    const send = document.querySelector("button[type='submit']") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
  });
});

describe("ChatView model target", () => {
  it("keeps provider and model pickers editable for an active model chat", async () => {
    const setProviderFilter = vi.fn();
    const setModel = vi.fn();
    const { state, actions } = setup({
      chatTarget: "model",
      providerFilter: "openai",
      model: "gpt-4o-mini",
      activeChatSessionID: "s1",
      activeChatSession: {
        id: "s1",
        title: "Model switching",
        messages: [],
        provider_calls: [],
      } as any,
      controlPlaneConfig: {
        providers: [
          { id: "anthropic", name: "Anthropic", kind: "cloud", credential_configured: true },
          { id: "openai", name: "OpenAI", kind: "cloud", credential_configured: true },
        ],
      } as any,
      providerPresets: [
        { id: "anthropic", name: "Anthropic", kind: "cloud" },
        { id: "openai", name: "OpenAI", kind: "cloud" },
      ] as any,
      providerScopedModels: [
        { id: "claude-sonnet-4-20250514", owned_by: "anthropic", metadata: { provider: "anthropic", provider_kind: "cloud" } },
        { id: "gpt-4o-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } },
        { id: "gpt-4.1-mini", owned_by: "openai", metadata: { provider: "openai", provider_kind: "cloud" } },
      ],
    }, { setProviderFilter, setModel });
    render(<ChatView state={state} actions={actions} />);

    const user = userEvent.setup();
    const providerPicker = screen.getByRole("button", { name: /OpenAI/i }) as HTMLButtonElement;
    expect(providerPicker.disabled).toBe(false);
    await user.click(providerPicker);
    await user.click(screen.getByText("Anthropic"));
    expect(setProviderFilter).toHaveBeenCalledWith("anthropic");

    const modelPicker = screen.getByRole("button", { name: /gpt-4o-mini/i }) as HTMLButtonElement;
    expect(modelPicker.disabled).toBe(false);
    await user.click(modelPicker);
    await user.click(screen.getByText("gpt-4.1-mini"));
    expect(setModel).toHaveBeenCalledWith("gpt-4.1-mini");
  });
});

describe("ChatView error display", () => {
  it("renders chatError using InlineError styling", () => {
    const { state, actions } = setup({ chatError: "Provider returned 500" });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText(/Provider returned 500/)).toBeTruthy();
  });

  it("renders operator guidance for stable gateway error codes", () => {
    const { state, actions } = setup({
      chatError: "Incorrect API key provided",
      chatErrorCode: "provider_auth_failed",
      chatErrorStatus: 502,
    });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText("Provider credentials failed")).toBeTruthy();
    expect(screen.getByText("502 · provider_auth_failed")).toBeTruthy();
    expect(screen.getByText(/Update the provider API key/)).toBeTruthy();
  });
});

describe("ChatView session title", () => {
  it("shows 'New chat' when no chats and no active chat", () => {
    const { state, actions } = setup({ chatSessions: [], activeChatSession: null });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getAllByText("New chat").length).toBeGreaterThan(0);
  });

  it("shows the active session's title", () => {
    const { state, actions } = setup({
      activeChatSession: { id: "s1", title: "Hello world", messages: [], provider_calls: [] } as any,
    });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText("Hello world")).toBeTruthy();
  });
});

describe("ChatView New chat button", () => {
  it("focuses the message textarea after clicking New chat", async () => {
    // The button starts a fresh chat; the operator's next move
    // is almost always to type. Auto-focusing the textarea saves a
    // click and matches the muscle-memory pattern from chat clients.
    const createChatSession = vi.fn();
    const { state, actions } = setup({}, { createChatSession });
    const user = userEvent.setup();
    render(<ChatView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: /new chat/i }));
    expect(createChatSession).toHaveBeenCalled();
    const textarea = screen.getByPlaceholderText(/^Message…/i);
    expect(document.activeElement).toBe(textarea);
  });
});

describe("ChatView session focus", () => {
  it("focuses the message textarea when a sidebar chat row is clicked", async () => {
    // Focus is applied on EXPLICIT user actions only — the New-chat
    // button onClick and chat-row onClick. The activeChatSessionID
    // effect deliberately does NOT focus, because data-load (chats
    // arriving from the API) also drives that transition and stealing
    // focus on load would block the dashboard's keyboard shortcuts
    // (e2e regression — see shell.spec.ts shortcut tests).
    const selectChatSession = vi.fn(async () => undefined);
    const { state, actions } = setup({
      chatSessions: [{ id: "s2", title: "Pick me", message_count: 0, provider_call_count: 0 } as any],
    }, { selectChatSession });
    const user = userEvent.setup();
    render(<ChatView state={state} actions={actions} />);
    // Move focus elsewhere to detect the jump.
    const closeBtn = screen.getByTitle("Close");
    closeBtn.focus();
    expect(document.activeElement).toBe(closeBtn);
    // Click the chat row — the only user-driven chat switch.
    await user.click(screen.getByText("Pick me"));
    const textarea = screen.getByPlaceholderText(/^Message…/i);
    expect(document.activeElement).toBe(textarea);
    expect(selectChatSession).toHaveBeenCalledWith("s2");
  });

  it("does NOT focus the textarea when activeChatSessionID changes from data-load", async () => {
    // Initial-load and API-driven session arrivals must not steal
    // focus — page-level shortcuts depend on it. Asserts the negative.
    const { state, actions } = setup({ activeChatSessionID: "" });
    const { rerender } = render(<ChatView state={state} actions={actions} />);
    const closeBtn = screen.getByTitle("Close");
    closeBtn.focus();
    const next = { ...state, activeChatSessionID: "s1" };
    rerender(<ChatView state={next} actions={actions} />);
    // Focus must STAY on the close button — the effect should not have
    // jumped to the textarea on a programmatic ID transition.
    expect(document.activeElement).toBe(closeBtn);
  });
});

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

describe("ChatView sessions sidebar", () => {
  it("shows 'No sessions yet' when chatSessions is empty", () => {
    const { state, actions } = setup({ chatSessions: [] });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText(/No sessions yet/i)).toBeTruthy();
  });

  it("renders one row per session with title", () => {
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

  it("calls selectChatSession when clicking a session row", async () => {
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
  it("shows 'New conversation' when no sessions and no active session", () => {
    const { state, actions } = setup({ chatSessions: [], activeChatSession: null });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText("New conversation")).toBeTruthy();
  });

  it("shows the active session's title", () => {
    const { state, actions } = setup({
      activeChatSession: { id: "s1", title: "Hello world", messages: [], provider_calls: [] } as any,
    });
    render(<ChatView state={state} actions={actions} />);
    expect(screen.getByText("Hello world")).toBeTruthy();
  });
});

describe("ChatView New session button", () => {
  it("focuses the message textarea after clicking New session", async () => {
    // The button starts a fresh conversation; the operator's next move
    // is almost always to type. Auto-focusing the textarea saves a
    // click and matches the muscle-memory pattern from chat clients.
    const createChatSession = vi.fn();
    const { state, actions } = setup({}, { createChatSession });
    const user = userEvent.setup();
    render(<ChatView state={state} actions={actions} />);
    await user.click(screen.getByRole("button", { name: /new session/i }));
    expect(createChatSession).toHaveBeenCalled();
    const textarea = screen.getByPlaceholderText(/^Message…/i);
    expect(document.activeElement).toBe(textarea);
  });
});

describe("ChatView session focus", () => {
  it("focuses the message textarea when a sidebar session row is clicked", async () => {
    // Focus is applied on EXPLICIT user actions only — the New-session
    // button onClick and session-row onClick. The activeChatSessionID
    // effect deliberately does NOT focus, because data-load (sessions
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
    // Click the session row — the only user-driven session switch.
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

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  createRuntimeConsoleActions,
  createRuntimeConsoleFixture,
} from "../../test/runtime-console-fixture";
import { withRuntimeConsole } from "../../test/runtime-console-render";
import { useSettings } from "../../app/state/settings";
import { ChatTranscript, type TranscriptItem, type VisibleChatMessage } from "./ChatTranscript";

class FakeUtterance {
  lang = "";
  onend: ((event: SpeechSynthesisEvent) => void) | null = null;
  onerror: ((event: SpeechSynthesisErrorEvent) => void) | null = null;
  voice: SpeechSynthesisVoice | null = null;

  constructor(readonly text: string) {}
}

const originalSynthesisDescriptor = Object.getOwnPropertyDescriptor(window, "speechSynthesis");
const originalUtteranceDescriptor = Object.getOwnPropertyDescriptor(
  globalThis,
  "SpeechSynthesisUtterance",
);

let synthesis: SpeechSynthesis;
let spoken: FakeUtterance[];
let voices: SpeechSynthesisVoice[];
let cancelSpeech: ReturnType<typeof vi.fn>;
let speak: ReturnType<typeof vi.fn>;

beforeEach(() => {
  spoken = [];
  cancelSpeech = vi.fn();
  speak = vi.fn((utterance: SpeechSynthesisUtterance) => {
    spoken.push(utterance as unknown as FakeUtterance);
  });
  const localVoice: SpeechSynthesisVoice = {
    default: true,
    lang: "en-US",
    localService: true,
    name: "System",
    voiceURI: "voice://system",
  };
  voices = [localVoice];
  synthesis = {
    addEventListener: vi.fn(),
    cancel: cancelSpeech,
    getVoices: vi.fn(() => voices),
    removeEventListener: vi.fn(),
    speak,
  } as unknown as SpeechSynthesis;
  Object.defineProperty(window, "speechSynthesis", { configurable: true, value: synthesis });
  Object.defineProperty(globalThis, "SpeechSynthesisUtterance", {
    configurable: true,
    value: FakeUtterance,
  });
});

afterEach(() => {
  restoreProperty(window, "speechSynthesis", originalSynthesisDescriptor);
  restoreProperty(globalThis, "SpeechSynthesisUtterance", originalUtteranceDescriptor);
});

describe("ChatTranscript read aloud", () => {
  it.each([
    {
      label: "Hecate",
      isHecateAgentChat: true,
      message: assistantMessage({ turn_kind: "direct_model", model: "gpt-4o" }),
    },
    {
      label: "External Agent",
      isHecateAgentChat: false,
      message: assistantMessage({
        turn_kind: "external_agent",
        agent_id: "claude_code",
        agent_name: "Claude Code",
      }),
    },
  ])("reads a settled $label response through the same client controller", async (entry) => {
    const user = userEvent.setup();
    renderTranscript({
      isHecateAgentChat: entry.isHecateAgentChat,
      items: [messageItem(entry.message)],
    });

    await user.click(screen.getByRole("button", { name: "Read response aloud" }));

    expect(spoken).toHaveLength(1);
    expect(spoken[0].text).toBe("Shared response");
    expect(screen.getByRole("button", { name: "Read response aloud" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByText("Reading response aloud.")).toBeInTheDocument();
  });

  it("does not expose a mutable running response", () => {
    renderTranscript({
      items: [
        messageItem(assistantMessage({ agent_status: "running", content: "Still changing" })),
      ],
    });

    expect(screen.queryByRole("button", { name: "Read response aloud" })).toBeNull();
  });

  it("does not expose the latest response while its selected turn is streaming", () => {
    renderTranscript({
      items: [messageItem(assistantMessage({ content: "Partial persisted content" }))],
      streaming: true,
    });

    expect(screen.queryByRole("button", { name: "Read response aloud" })).toBeNull();
  });

  it("cancels active speech when the response returns to a busy wire status", async () => {
    const user = userEvent.setup();
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "chat-1",
      chatTarget: "agent",
    });
    const actions = createRuntimeConsoleActions();
    const settled = messageItem(assistantMessage());
    const { rerender } = render(
      withRuntimeConsole(transcript({ items: [settled] }), { actions, state }),
    );
    await user.click(screen.getByRole("button", { name: "Read response aloud" }));

    const running = messageItem(assistantMessage({ agent_status: "running" }));
    rerender(withRuntimeConsole(transcript({ items: [running] }), { actions, state }));

    await waitFor(() => expect(cancelSpeech).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole("button", { name: "Read response aloud" })).toBeNull();
  });

  it("cancels active speech when settled content changes under the same message ID", async () => {
    const user = userEvent.setup();
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "chat-1",
      chatTarget: "agent",
    });
    const actions = createRuntimeConsoleActions();
    const original = messageItem(assistantMessage({ content: "Original private response" }));
    const { rerender } = render(
      withRuntimeConsole(transcript({ items: [original] }), { actions, state }),
    );
    await user.click(screen.getByRole("button", { name: "Read response aloud" }));
    const staleUtterance = spoken[0];

    const corrected = messageItem(assistantMessage({ content: "Corrected public response" }));
    rerender(withRuntimeConsole(transcript({ items: [corrected] }), { actions, state }));

    await waitFor(() => expect(cancelSpeech).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("button", { name: "Read response aloud" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    staleUtterance.onend?.({} as SpeechSynthesisEvent);
    expect(spoken).toHaveLength(1);
  });

  it("surfaces a system speech failure as a visible Hecate notice", async () => {
    speak.mockImplementationOnce(() => {
      throw new Error("speech service unavailable");
    });
    const user = userEvent.setup();
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "chat-1",
      chatTarget: "agent",
    });
    const actions = createRuntimeConsoleActions();
    const { container } = render(
      withRuntimeConsole(
        <>
          {transcript({ items: [messageItem(assistantMessage())] })}
          <NoticeProbe />
        </>,
        { actions, state },
      ),
    );

    await user.click(screen.getByRole("button", { name: "Read response aloud" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Read aloud stopped because the system voice failed.",
    );
    expect(container.querySelector('[aria-live="polite"]')).toBeEmptyDOMElement();
  });

  it("gives touch users actionable local-voice setup guidance", async () => {
    voices = [];
    const user = userEvent.setup();
    const state = createRuntimeConsoleFixture({
      activeChatSessionID: "chat-1",
      chatTarget: "agent",
    });
    const actions = createRuntimeConsoleActions();
    render(
      withRuntimeConsole(
        <>
          {transcript({ items: [messageItem(assistantMessage())] })}
          <NoticeProbe />
        </>,
        { actions, state },
      ),
    );

    const button = screen.getByRole("button", { name: "Read response aloud" });
    expect(button).not.toHaveAttribute("aria-disabled");
    await user.click(button);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Install or enable a local system voice to use read aloud.",
    );
  });
});

function renderTranscript({
  isHecateAgentChat = true,
  items,
  streaming = false,
}: {
  isHecateAgentChat?: boolean;
  items: TranscriptItem[];
  streaming?: boolean;
}) {
  const state = createRuntimeConsoleFixture({
    activeChatSessionID: "chat-1",
    chatTarget: isHecateAgentChat ? "agent" : "external_agent",
  });
  return render(
    withRuntimeConsole(transcript({ isHecateAgentChat, items, streaming }), {
      actions: createRuntimeConsoleActions(),
      state,
    }),
  );
}

function transcript({
  isHecateAgentChat = true,
  items,
  streaming = false,
}: {
  isHecateAgentChat?: boolean;
  items: TranscriptItem[];
  streaming?: boolean;
}) {
  return (
    <ChatTranscript
      activeSessionID="chat-1"
      canOpenProject={() => true}
      emptyState={null}
      isHecateAgentChat={isHecateAgentChat}
      onOpenProject={() => true}
      openExternalAgentSetup={() => undefined}
      streaming={streaming}
      transcriptItems={items}
      visibleMessageCount={items.filter((item) => item.type === "message").length}
    />
  );
}

function NoticeProbe() {
  const { state } = useSettings();
  return state.notice ? <div role="alert">{state.notice.message}</div> : null;
}

function assistantMessage(overrides: Partial<VisibleChatMessage> = {}): VisibleChatMessage {
  return {
    id: "assistant-1",
    role: "assistant",
    content: "Shared **response**",
    agent_status: "completed",
    ...overrides,
  };
}

function messageItem(message: VisibleChatMessage): TranscriptItem {
  return { type: "message", key: `message:${message.id}`, message };
}

function restoreProperty(
  target: object,
  key: PropertyKey,
  descriptor: PropertyDescriptor | undefined,
) {
  if (descriptor) Object.defineProperty(target, key, descriptor);
  else Reflect.deleteProperty(target, key);
}

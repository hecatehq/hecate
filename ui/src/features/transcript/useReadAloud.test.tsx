import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useReadAloud } from "./useReadAloud";

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

let voices: SpeechSynthesisVoice[];
let spoken: FakeUtterance[];
let voiceChangeListener: (() => void) | null;
let synthesis: SpeechSynthesis;
let cancelSpeech: ReturnType<typeof vi.fn>;
let speak: ReturnType<typeof vi.fn>;

beforeEach(() => {
  voices = [voice({ default: true, lang: "en-US", localService: true, name: "System" })];
  spoken = [];
  voiceChangeListener = null;
  cancelSpeech = vi.fn();
  speak = vi.fn((utterance: SpeechSynthesisUtterance) => {
    spoken.push(utterance as unknown as FakeUtterance);
  });
  synthesis = {
    addEventListener: vi.fn((event: string, listener: EventListenerOrEventListenerObject) => {
      if (event === "voiceschanged") voiceChangeListener = listener as () => void;
    }),
    cancel: cancelSpeech,
    getVoices: vi.fn(() => voices),
    removeEventListener: vi.fn((event: string) => {
      if (event === "voiceschanged") voiceChangeListener = null;
    }),
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

describe("useReadAloud", () => {
  it("reports unsupported hosts without throwing", () => {
    Reflect.deleteProperty(window, "speechSynthesis");
    Reflect.deleteProperty(globalThis, "SpeechSynthesisUtterance");

    const { result } = renderHook(() => useReadAloud("chat-1"));

    expect(result.current.availability).toBe("unsupported");
    expect(result.current.disabledReason).toMatch(/unavailable/);
  });

  it("rejects remote voices instead of using the browser default", () => {
    voices = [voice({ default: true, localService: false, name: "Online" })];
    const { result } = renderHook(() => useReadAloud("chat-1"));

    expect(result.current.availability).toBe("no_local_voice");
    act(() => result.current.toggle("message-1", "Private response"));
    expect(speak).not.toHaveBeenCalled();
    expect(result.current.announcement).toMatch(/local system voice/);
  });

  it("reads normalized Markdown with an explicit local voice", () => {
    const { result } = renderHook(() => useReadAloud("chat-1"));

    act(() => result.current.toggle("message-1", "Read **this**.\n\n```sh\nsecret\n```"));

    expect(result.current.readingMessageID).toBe("message-1");
    expect(result.current.readingContent).toBe("Read **this**.\n\n```sh\nsecret\n```");
    expect(spoken).toHaveLength(1);
    expect(spoken[0].text).toBe("Read this.\n\nCode block omitted.");
    expect(spoken[0].voice?.name).toBe("System");
    expect(spoken[0].lang).toBe("en-US");
  });

  it("stops the active response when toggled again", () => {
    const { result } = renderHook(() => useReadAloud("chat-1"));
    act(() => result.current.toggle("message-1", "Response"));

    act(() => result.current.toggle("message-1", "Response"));

    expect(result.current.readingMessageID).toBeNull();
    expect(result.current.readingContent).toBeNull();
    expect(result.current.announcement).toBe("Read aloud stopped.");
    expect(cancelSpeech).toHaveBeenCalledTimes(2);
  });

  it("replaces the active response and ignores its late completion", () => {
    const { result } = renderHook(() => useReadAloud("chat-1"));
    act(() => result.current.toggle("message-1", "First"));
    const first = spoken[0];
    const firstAnnouncement = result.current.announcement;

    act(() => result.current.toggle("message-2", "Second"));
    expect(result.current.readingMessageID).toBe("message-2");
    expect(result.current.announcement).toBe("Reading selected response aloud.");
    expect(result.current.announcement).not.toBe(firstAnnouncement);

    act(() => first.onend?.({} as SpeechSynthesisEvent));
    expect(result.current.readingMessageID).toBe("message-2");
  });

  it("continues bounded chunks and settles after the final one", () => {
    const { result } = renderHook(() => useReadAloud("chat-1"));
    act(() => result.current.toggle("message-1", `Start. ${"word ".repeat(180)}`));
    expect(spoken).toHaveLength(1);

    act(() => spoken[0].onend?.({} as SpeechSynthesisEvent));
    expect(spoken).toHaveLength(2);
    expect(result.current.readingMessageID).toBe("message-1");

    act(() => spoken[1].onend?.({} as SpeechSynthesisEvent));
    expect(result.current.readingMessageID).toBeNull();
    expect(result.current.announcement).toBe("Finished reading response.");
  });

  it("ignores a late error after successful completion", () => {
    const onVisibleError = vi.fn();
    const { result } = renderHook(() => useReadAloud("chat-1", onVisibleError));
    act(() => result.current.toggle("message-1", "Response"));
    const finishedUtterance = spoken[0];

    act(() => finishedUtterance.onend?.({} as SpeechSynthesisEvent));
    act(() =>
      finishedUtterance.onerror?.({ error: "synthesis-failed" } as SpeechSynthesisErrorEvent),
    );

    expect(result.current.announcement).toBe("Finished reading response.");
    expect(onVisibleError).not.toHaveBeenCalled();
  });

  it("settles when the system voice reports an error", () => {
    const onVisibleError = vi.fn();
    const { result } = renderHook(() => useReadAloud("chat-1", onVisibleError));
    act(() => result.current.toggle("message-1", `Start. ${"word ".repeat(180)}`));
    const failedUtterance = spoken[0];

    act(() =>
      failedUtterance.onerror?.({ error: "synthesis-failed" } as SpeechSynthesisErrorEvent),
    );
    act(() => failedUtterance.onend?.({} as SpeechSynthesisEvent));

    expect(result.current.readingMessageID).toBeNull();
    expect(result.current.announcement).toBe("");
    expect(spoken).toHaveLength(1);
    expect(onVisibleError).toHaveBeenCalledWith(
      "Read aloud stopped because the system voice failed.",
    );
  });

  it("settles when the host rejects a speech request", () => {
    speak.mockImplementationOnce(() => {
      throw new Error("speech service unavailable");
    });
    const { result } = renderHook(() => useReadAloud("chat-1"));

    act(() => result.current.toggle("message-1", "Response"));

    expect(result.current.readingMessageID).toBeNull();
    expect(result.current.announcement).toMatch(/system voice failed/);
  });

  it("refreshes local voice availability", () => {
    voices = [];
    const { result } = renderHook(() => useReadAloud("chat-1"));
    expect(result.current.availability).toBe("no_local_voice");

    voices = [voice({ lang: "es-ES", localService: true, name: "Sistema" })];
    act(() => voiceChangeListener?.());
    expect(result.current.availability).toBe("available");
  });

  it("stops when the selected local voice disappears but another remains", () => {
    const selectedVoice = voice({
      default: true,
      name: "Selected",
      voiceURI: "voice://selected",
    });
    const otherVoice = voice({ name: "Other", voiceURI: "voice://other" });
    voices = [selectedVoice, otherVoice];
    const onVisibleError = vi.fn();
    const { result } = renderHook(() => useReadAloud("chat-1", onVisibleError));
    act(() => result.current.toggle("message-1", `Start. ${"word ".repeat(180)}`));
    const staleUtterance = spoken[0];

    voices = [otherVoice];
    act(() => voiceChangeListener?.());
    act(() => staleUtterance.onend?.({} as SpeechSynthesisEvent));

    expect(result.current.availability).toBe("available");
    expect(result.current.readingMessageID).toBeNull();
    expect(cancelSpeech).toHaveBeenCalledTimes(2);
    expect(spoken).toHaveLength(1);
    expect(onVisibleError).toHaveBeenCalledWith(
      "Read aloud stopped because the selected local system voice became unavailable.",
    );
  });

  it("revalidates the selected local voice before starting the next chunk", () => {
    const selectedVoice = voice({
      default: true,
      name: "Selected",
      voiceURI: "voice://selected",
    });
    const otherVoice = voice({ name: "Other", voiceURI: "voice://other" });
    voices = [selectedVoice, otherVoice];
    const onVisibleError = vi.fn();
    const { result } = renderHook(() => useReadAloud("chat-1", onVisibleError));
    act(() => result.current.toggle("message-1", `Start. ${"word ".repeat(180)}`));

    voices = [otherVoice];
    act(() => spoken[0].onend?.({} as SpeechSynthesisEvent));

    expect(result.current.availability).toBe("available");
    expect(result.current.readingMessageID).toBeNull();
    expect(cancelSpeech).toHaveBeenCalledTimes(2);
    expect(spoken).toHaveLength(1);
    expect(onVisibleError).toHaveBeenCalledWith(
      "Read aloud stopped because the selected local system voice became unavailable.",
    );
  });

  it("stops the previous response when a replacement has no local voice", () => {
    const onVisibleError = vi.fn();
    const { result } = renderHook(() => useReadAloud("chat-1", onVisibleError));
    act(() => result.current.toggle("message-1", "First"));
    voices = [];

    act(() => result.current.toggle("message-2", "Second"));

    expect(result.current.readingMessageID).toBeNull();
    expect(cancelSpeech).toHaveBeenCalledTimes(2);
    expect(onVisibleError).toHaveBeenCalledWith(
      "Install or enable a local system voice to use read aloud.",
    );
  });

  it("cancels speech when the chat changes and when the controller unmounts", () => {
    const { rerender, result, unmount } = renderHook(({ scopeKey }) => useReadAloud(scopeKey), {
      initialProps: { scopeKey: "chat-1" },
    });
    act(() => result.current.toggle("message-1", "Response"));

    rerender({ scopeKey: "chat-2" });
    expect(result.current.readingMessageID).toBeNull();
    expect(result.current.announcement).toMatch(/chat changed/);

    unmount();
    expect(cancelSpeech).toHaveBeenCalledTimes(3);
  });
});

function voice(overrides: Partial<SpeechSynthesisVoice>): SpeechSynthesisVoice {
  return {
    default: false,
    lang: "en-US",
    localService: true,
    name: "Voice",
    voiceURI: "voice://local",
    ...overrides,
  };
}

function restoreProperty(
  target: object,
  key: PropertyKey,
  descriptor: PropertyDescriptor | undefined,
) {
  if (descriptor) Object.defineProperty(target, key, descriptor);
  else Reflect.deleteProperty(target, key);
}

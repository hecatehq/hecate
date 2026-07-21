import { afterEach, describe, expect, it, vi } from "vitest";

import { BrowserSpeechDictationSession } from "./browserSpeechDictation";
import type {
  BrowserSpeechRecognitionConstructor,
  BrowserSpeechRecognitionErrorEvent,
  BrowserSpeechRecognitionResultEvent,
} from "./chatDictation";

afterEach(() => {
  vi.useRealTimers();
});

describe("BrowserSpeechDictationSession", () => {
  it("finishes a synchronous stop without arming a stale watchdog", async () => {
    vi.useFakeTimers();
    const recognition = new SessionRecognition();
    recognition.stop.mockImplementation(() => {
      recognition.emitFinalResult("finished draft");
      recognition.onend?.();
    });
    const transcript = vi.fn();
    const error = vi.fn();
    const session = createSession(recognition, { onError: error, onTranscript: transcript });

    session.start();
    session.stop();
    await vi.advanceTimersByTimeAsync(20_000);

    expect(transcript).toHaveBeenCalledWith("finished draft");
    expect(error).not.toHaveBeenCalledWith(expect.stringContaining("did not finish"));
    expect(recognition.abort).not.toHaveBeenCalled();
  });

  it("bounds a recognizer that never emits its terminal event", async () => {
    vi.useFakeTimers();
    const recognition = new SessionRecognition();
    const error = vi.fn();
    const phases: string[] = [];
    const session = createSession(recognition, {
      onError: error,
      onPhase: (phase) => phases.push(phase),
    });

    session.start();
    session.stop();
    await vi.advanceTimersByTimeAsync(10_000);

    expect(recognition.abort).toHaveBeenCalledOnce();
    expect(error).toHaveBeenCalledWith(
      "Speech recognition did not finish in time. Retry or choose another route.",
    );
    expect(phases).toEqual(["requesting", "recording", "processing", "idle"]);
  });

  it("aborts an engine that throws after recognition startup begins", () => {
    const recognition = new SessionRecognition();
    recognition.start.mockImplementation(() => {
      throw new DOMException("speech service unavailable", "NetworkError");
    });
    const error = vi.fn();
    const phases: string[] = [];
    const session = createSession(recognition, {
      onError: error,
      onPhase: (phase) => phases.push(phase),
    });

    session.start();

    expect(recognition.abort).toHaveBeenCalledOnce();
    expect(error).toHaveBeenCalledWith("Speech recognition could not start.");
    expect(phases).toEqual(["requesting", "idle"]);
  });
});

function createSession(
  recognition: SessionRecognition,
  overrides: {
    onError?: (message: string) => void;
    onPhase?: (phase: "idle" | "requesting" | "recording" | "processing") => void;
    onTranscript?: (text: string) => void;
  } = {},
) {
  class Constructor {
    constructor() {
      return recognition;
    }
  }
  return new BrowserSpeechDictationSession({
    constructor: Constructor as unknown as BrowserSpeechRecognitionConstructor,
    locale: "en-US",
    maxDurationMS: 120_000,
    finalizationTimeoutMS: 10_000,
    errorMessage: (_cause, fallback) => fallback,
    recognitionErrorMessage: (_event, _locale) => "recognition failed",
    onElapsed: vi.fn(),
    onError: vi.fn(),
    onPhase: vi.fn(),
    onSettled: vi.fn(),
    onTranscript: vi.fn(),
    ...overrides,
  });
}

class SessionRecognition {
  lang = "";
  continuous = false;
  interimResults = false;
  maxAlternatives = 1;
  onresult: ((event: BrowserSpeechRecognitionResultEvent) => void) | null = null;
  onerror: ((event: BrowserSpeechRecognitionErrorEvent) => void) | null = null;
  onend: (() => void) | null = null;
  start = vi.fn();
  stop = vi.fn();
  abort = vi.fn();

  emitFinalResult(transcript: string) {
    const result = Object.assign([{ transcript }], { isFinal: true });
    const event = Object.assign(new Event("result"), { resultIndex: 0, results: [result] });
    this.onresult?.(event as BrowserSpeechRecognitionResultEvent);
  }
}

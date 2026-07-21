import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createDictationTranscription, getDictationOptions } from "../../lib/api";
import { ChatDictationControl } from "./ChatDictationControl";
import {
  BROWSER_SPEECH_ROUTE_ID,
  type BrowserSpeechRecognitionErrorCode,
  type BrowserSpeechRecognitionErrorEvent,
  type BrowserSpeechRecognitionResultEvent,
} from "./chatDictation";

const originalMediaDevices = Object.getOwnPropertyDescriptor(navigator, "mediaDevices");
const originalSecureContext = Object.getOwnPropertyDescriptor(window, "isSecureContext");
const originalSpeechRecognition = Object.getOwnPropertyDescriptor(window, "SpeechRecognition");
const originalWebkitSpeechRecognition = Object.getOwnPropertyDescriptor(
  window,
  "webkitSpeechRecognition",
);

vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...actual,
    getDictationOptions: vi.fn(),
    createDictationTranscription: vi.fn(),
  };
});

const localOption = {
  provider: "localai",
  provider_kind: "local",
  default_model: "whisper-1",
  available: true,
};
const cloudOption = {
  provider: "openai",
  provider_kind: "cloud",
  default_model: "gpt-4o-mini-transcribe",
  available: true,
};

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.removeItem("hecate.dictationProvider");
  Object.defineProperty(window, "isSecureContext", { configurable: true, value: true });
  Reflect.deleteProperty(window, "SpeechRecognition");
  Reflect.deleteProperty(window, "webkitSpeechRecognition");
  Object.defineProperty(navigator, "mediaDevices", {
    configurable: true,
    value: { getUserMedia: vi.fn() },
  });
  vi.stubGlobal("MediaRecorder", MockMediaRecorder);
  vi.mocked(getDictationOptions).mockResolvedValue({
    object: "dictation_options",
    data: [localOption, cloudOption],
  });
  vi.mocked(createDictationTranscription).mockResolvedValue({
    provider: "localai",
    provider_kind: "local",
    model: "whisper-1",
    text: "voice draft",
  });
});

afterEach(() => {
  vi.useRealTimers();
  Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  if (originalMediaDevices) {
    Object.defineProperty(navigator, "mediaDevices", originalMediaDevices);
  } else {
    Reflect.deleteProperty(navigator, "mediaDevices");
  }
  if (originalSecureContext) {
    Object.defineProperty(window, "isSecureContext", originalSecureContext);
  } else {
    Reflect.deleteProperty(window, "isSecureContext");
  }
  if (originalSpeechRecognition) {
    Object.defineProperty(window, "SpeechRecognition", originalSpeechRecognition);
  } else {
    Reflect.deleteProperty(window, "SpeechRecognition");
  }
  if (originalWebkitSpeechRecognition) {
    Object.defineProperty(window, "webkitSpeechRecognition", originalWebkitSpeechRecognition);
  } else {
    Reflect.deleteProperty(window, "webkitSpeechRecognition");
  }
});

describe("ChatDictationControl", () => {
  it("defaults to the server's local-first route and explains disclosure", async () => {
    render(<ChatDictationControl onTranscript={vi.fn()} />);

    const provider = await screen.findByRole("combobox", { name: "Dictation route" });
    await waitFor(() => expect(provider).toHaveValue("provider:localai"));
    expect(screen.getByRole("status")).toHaveTextContent(
      "Audio goes only to localai; Hecate does not retain it.",
    );
  });

  it("does not invoke experimental static speech capability probes while mounting", async () => {
    const available = vi.fn(() => {
      throw new Error("host speech probe must remain idle");
    });
    class BrowserSpeechRecognitionWithUnsafeProbe extends MockBrowserSpeechRecognition {
      static available = available;
    }
    Object.defineProperty(window, "SpeechRecognition", {
      configurable: true,
      value: BrowserSpeechRecognitionWithUnsafeProbe,
    });

    render(<ChatDictationControl onTranscript={vi.fn()} />);

    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        "provider:localai",
      ),
    );
    expect(available).not.toHaveBeenCalled();
  });

  it("migrates and preserves a saved provider instead of silently switching locality", async () => {
    localStorage.setItem("hecate.dictationProvider", "openai");
    installSpeechRecognitionMocks();
    render(<ChatDictationControl onTranscript={vi.fn()} />);

    const route = await screen.findByRole("combobox", { name: "Dictation route" });
    await waitFor(() => expect(route).toHaveValue("provider:openai"));
    expect(screen.getByRole("status")).toHaveTextContent(
      "Audio goes only to openai; Hecate does not retain it.",
    );
    await waitFor(() =>
      expect(localStorage.getItem("hecate.dictationProvider")).toBe("provider:openai"),
    );
  });

  it("preserves a saved browser-managed route instead of selecting a provider", async () => {
    localStorage.setItem("hecate.dictationProvider", BROWSER_SPEECH_ROUTE_ID);
    installSpeechRecognitionMocks();
    render(<ChatDictationControl onTranscript={vi.fn()} />);

    const route = await screen.findByRole("combobox", { name: "Dictation route" });
    await waitFor(() => expect(route).toHaveValue(BROWSER_SPEECH_ROUTE_ID));
    expect(screen.getByRole("status")).toHaveTextContent(
      "Speech recognition is controlled by the browser and may use its vendor's cloud service",
    );
    expect(localStorage.getItem("hecate.dictationProvider")).toBe(BROWSER_SPEECH_ROUTE_ID);
  });

  it("keeps a retired on-device choice blocked without silently selecting a cloud route", async () => {
    localStorage.setItem("hecate.dictationProvider", "client:web-speech:on-device");
    installSpeechRecognitionMocks();
    render(<ChatDictationControl onTranscript={vi.fn()} />);

    const route = await screen.findByRole("combobox", { name: "Dictation route" });
    await waitFor(() => expect(route).toHaveValue(""));
    expect(screen.getByRole("button", { name: "Start dictation" })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent(
      "The saved experimental on-device browser route is no longer available. Hecate did not switch to a cloud route",
    );
    expect(localStorage.getItem("hecate.dictationProvider")).toBe("client:web-speech:on-device");
  });

  it("keeps an unavailable route compact and points to setup", async () => {
    vi.mocked(getDictationOptions).mockResolvedValue({
      object: "dictation_options",
      data: [],
    });
    const onOpenConnections = vi.fn();
    const user = userEvent.setup();
    render(<ChatDictationControl onOpenConnections={onOpenConnections} onTranscript={vi.fn()} />);

    const button = await screen.findByRole("button", { name: "Start dictation" });
    expect(button).toBeDisabled();
    expect(button).toHaveAccessibleDescription(
      "Dictation uses a separate speech-to-text route, independent of the selected chat model or agent. Connect OpenAI, Groq, or LocalAI in Connections. You can still use any operating-system dictation input available in the focused message field.",
    );
    expect(button).toHaveAttribute("title", expect.stringContaining("Connections"));
    expect(screen.queryByRole("combobox", { name: "Dictation route" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Set up dictation provider" }));
    expect(onOpenConnections).toHaveBeenCalledOnce();
  });

  it("surfaces the configured provider's readiness reason", async () => {
    vi.mocked(getDictationOptions).mockResolvedValue({
      object: "dictation_options",
      data: [
        {
          ...cloudOption,
          available: false,
          unavailable_reason: "provider credentials are missing",
        },
      ],
    });
    render(<ChatDictationControl onTranscript={vi.fn()} />);

    const button = await screen.findByRole("button", { name: "Start dictation" });
    expect(button).toBeDisabled();
    expect(button).toHaveAccessibleDescription(
      "Dictation uses a separate speech-to-text route. openai is unavailable: provider credentials are missing. Open Connections to fix it. You can still use any operating-system dictation input available in the focused message field.",
    );
  });

  it("disables capture with a precise message when browser recording is unsupported", async () => {
    vi.stubGlobal("MediaRecorder", undefined);
    vi.mocked(getDictationOptions).mockResolvedValue({
      object: "dictation_options",
      data: [localOption],
    });
    render(<ChatDictationControl onOpenConnections={vi.fn()} onTranscript={vi.fn()} />);

    const button = await screen.findByRole("button", { name: "Start dictation" });
    expect(button).toBeDisabled();
    await waitFor(() =>
      expect(button).toHaveAccessibleDescription(
        "This browser or app webview cannot record microphone audio.",
      ),
    );
    expect(screen.queryByRole("button", { name: "Set up dictation provider" })).toBeNull();
  });

  it("offers a retry instead of provider setup when provider status cannot be loaded", async () => {
    vi.mocked(getDictationOptions)
      .mockRejectedValueOnce(new Error("network unavailable"))
      .mockResolvedValueOnce({
        object: "dictation_options",
        data: [localOption],
      });
    const user = userEvent.setup();
    render(<ChatDictationControl onOpenConnections={vi.fn()} onTranscript={vi.fn()} />);

    const button = await screen.findByRole("button", { name: "Start dictation" });
    expect(button).toBeDisabled();
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Dictation route status could not be loaded.",
    );
    expect(screen.queryByRole("button", { name: "Set up dictation provider" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Retry dictation route check" }));
    await waitFor(() => expect(getDictationOptions).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(button).toBeEnabled());
    expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
      "provider:localai",
    );
  });

  it("explains that non-secure web origins cannot request a microphone", async () => {
    Object.defineProperty(window, "isSecureContext", { configurable: true, value: false });
    render(<ChatDictationControl onTranscript={vi.fn()} />);

    const button = await screen.findByRole("button", { name: "Start dictation" });
    expect(button).toBeDisabled();
    await waitFor(() =>
      expect(button).toHaveAccessibleDescription(
        "Dictation needs HTTPS or a loopback Hecate URL for microphone access.",
      ),
    );
  });

  it("records, stops tracks, transcribes through the selected route, and returns draft text", async () => {
    const stopTrack = installRecorderMocks();
    const onTranscript = vi.fn();
    const user = userEvent.setup();
    render(<ChatDictationControl onTranscript={onTranscript} />);
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        "provider:localai",
      ),
    );

    await user.click(screen.getByRole("button", { name: "Start dictation" }));
    expect(await screen.findByRole("button", { name: "Stop dictation recording" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Stop dictation recording" }));

    await waitFor(() => expect(onTranscript).toHaveBeenCalledWith("voice draft"));
    expect(stopTrack).toHaveBeenCalled();
    const [provider, file, signal] = vi.mocked(createDictationTranscription).mock.calls[0];
    expect(provider).toBe("localai");
    expect(file).toBeInstanceOf(File);
    expect(file.name).toBe("dictation.webm");
    expect(file.type).toBe("audio/webm");
    expect(signal).toBeInstanceOf(AbortSignal);
  });

  it("keeps recording duration out of live status announcements", async () => {
    installRecorderMocks();
    const user = userEvent.setup();
    render(<ChatDictationControl onTranscript={vi.fn()} />);
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        "provider:localai",
      ),
    );

    await user.click(screen.getByRole("button", { name: "Start dictation" }));

    expect(screen.getByRole("status")).toHaveTextContent("Recording");
    expect(screen.getByRole("status")).not.toHaveTextContent("0:00");
    expect(screen.getByLabelText("Recording duration 0:00")).toHaveAttribute("aria-live", "off");
  });

  it("stops the microphone and does not disclose audio after unmount", async () => {
    const stopTrack = installRecorderMocks();
    const user = userEvent.setup();
    const { unmount } = render(<ChatDictationControl onTranscript={vi.fn()} />);
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        "provider:localai",
      ),
    );
    await user.click(screen.getByRole("button", { name: "Start dictation" }));
    await screen.findByRole("button", { name: "Stop dictation recording" });

    act(() => unmount());

    expect(stopTrack).toHaveBeenCalled();
    expect(createDictationTranscription).not.toHaveBeenCalled();
  });

  it("stops provider capture without disclosing audio when the composer becomes disabled", async () => {
    const stopTrack = installRecorderMocks();
    const onTranscript = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(
      <ChatDictationControl disabled={false} onTranscript={onTranscript} />,
    );
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        "provider:localai",
      ),
    );
    await user.click(screen.getByRole("button", { name: "Start dictation" }));
    await screen.findByRole("button", { name: "Stop dictation recording" });

    rerender(<ChatDictationControl disabled onTranscript={onTranscript} />);

    expect(stopTrack).toHaveBeenCalled();
    expect(createDictationTranscription).not.toHaveBeenCalled();
    expect(onTranscript).not.toHaveBeenCalled();
  });

  it("discards a microphone grant that resolves after the composer becomes disabled", async () => {
    const stopTrack = vi.fn();
    const stream = { getTracks: () => [{ stop: stopTrack }] } as unknown as MediaStream;
    let resolvePermission!: (stream: MediaStream) => void;
    const permission = new Promise<MediaStream>((resolve) => {
      resolvePermission = resolve;
    });
    Object.defineProperty(navigator, "mediaDevices", {
      configurable: true,
      value: { getUserMedia: vi.fn().mockReturnValue(permission) },
    });
    const recorderStart = vi.spyOn(MockMediaRecorder.prototype, "start");
    const onTranscript = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(
      <ChatDictationControl disabled={false} onTranscript={onTranscript} />,
    );
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        "provider:localai",
      ),
    );
    await user.click(screen.getByRole("button", { name: "Start dictation" }));
    expect(screen.getByRole("status")).toHaveTextContent("Requesting microphone…");

    rerender(<ChatDictationControl disabled onTranscript={onTranscript} />);
    await act(async () => {
      resolvePermission(stream);
      await permission;
    });

    expect(stopTrack).toHaveBeenCalledOnce();
    expect(recorderStart).not.toHaveBeenCalled();
    expect(createDictationTranscription).not.toHaveBeenCalled();
    expect(onTranscript).not.toHaveBeenCalled();
  });

  it("does not let a stale permission failure stop a newer capture", async () => {
    let rejectFirstPermission!: (cause: unknown) => void;
    const firstPermission = new Promise<MediaStream>((_resolve, reject) => {
      rejectFirstPermission = reject;
    });
    const stopNewTrack = vi.fn();
    const newStream = { getTracks: () => [{ stop: stopNewTrack }] } as unknown as MediaStream;
    Object.defineProperty(navigator, "mediaDevices", {
      configurable: true,
      value: {
        getUserMedia: vi.fn().mockReturnValueOnce(firstPermission).mockResolvedValueOnce(newStream),
      },
    });
    const onTranscript = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(
      <ChatDictationControl disabled={false} onTranscript={onTranscript} />,
    );
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        "provider:localai",
      ),
    );
    await user.click(screen.getByRole("button", { name: "Start dictation" }));
    rerender(<ChatDictationControl disabled onTranscript={onTranscript} />);
    rerender(<ChatDictationControl disabled={false} onTranscript={onTranscript} />);
    await user.click(screen.getByRole("button", { name: "Start dictation" }));
    await screen.findByRole("button", { name: "Stop dictation recording" });

    await act(async () => {
      rejectFirstPermission(new DOMException("denied", "NotAllowedError"));
      await firstPermission.catch(() => undefined);
    });

    expect(stopNewTrack).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Stop dictation recording" })).toBeEnabled();
  });

  it("aborts an in-flight transcription when the composer becomes disabled", async () => {
    installRecorderMocks();
    let resolveTranscription!: (value: {
      provider: string;
      provider_kind: string;
      model: string;
      text: string;
    }) => void;
    const transcription = new Promise<{
      provider: string;
      provider_kind: string;
      model: string;
      text: string;
    }>((resolve) => {
      resolveTranscription = resolve;
    });
    vi.mocked(createDictationTranscription).mockReturnValue(transcription);
    const onTranscript = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(
      <ChatDictationControl disabled={false} onTranscript={onTranscript} />,
    );
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        "provider:localai",
      ),
    );
    await user.click(screen.getByRole("button", { name: "Start dictation" }));
    await user.click(await screen.findByRole("button", { name: "Stop dictation recording" }));
    await waitFor(() => expect(createDictationTranscription).toHaveBeenCalledOnce());
    const signal = vi.mocked(createDictationTranscription).mock.calls[0]?.[2];

    rerender(<ChatDictationControl disabled onTranscript={onTranscript} />);
    expect(signal?.aborted).toBe(true);
    await act(async () => {
      resolveTranscription({
        provider: "localai",
        provider_kind: "local",
        model: "whisper-1",
        text: "late transcript",
      });
      await transcription;
    });

    expect(onTranscript).not.toHaveBeenCalled();
  });

  it("shows a useful microphone permission error", async () => {
    vi.stubGlobal("MediaRecorder", MockMediaRecorder);
    Object.defineProperty(navigator, "mediaDevices", {
      configurable: true,
      value: {
        getUserMedia: vi.fn().mockRejectedValue(new DOMException("denied", "NotAllowedError")),
      },
    });
    const user = userEvent.setup();
    render(<ChatDictationControl onTranscript={vi.fn()} />);
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        "provider:localai",
      ),
    );

    await user.click(screen.getByRole("button", { name: "Start dictation" }));

    expect(await screen.findByText(/Open this site's controls in the address bar/)).toBeVisible();
  });

  it("points desktop users to the operating-system microphone permission", async () => {
    Object.defineProperty(window, "__TAURI_INTERNALS__", { configurable: true, value: {} });
    vi.spyOn(window.navigator, "platform", "get").mockReturnValue("MacIntel");
    Object.defineProperty(navigator, "mediaDevices", {
      configurable: true,
      value: {
        getUserMedia: vi.fn().mockRejectedValue(new DOMException("denied", "NotAllowedError")),
      },
    });
    const user = userEvent.setup();
    render(<ChatDictationControl onTranscript={vi.fn()} />);
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        "provider:localai",
      ),
    );

    await user.click(screen.getByRole("button", { name: "Start dictation" }));

    expect(
      await screen.findByText(/System Settings → Privacy & Security → Microphone/),
    ).toBeVisible();
  });

  it("points macOS browser speech users to microphone and speech-recognition permissions", async () => {
    Object.defineProperty(window, "__TAURI_INTERNALS__", { configurable: true, value: {} });
    vi.spyOn(window.navigator, "platform", "get").mockReturnValue("MacIntel");
    const speech = installSpeechRecognitionMocks();
    const user = userEvent.setup();
    render(<ChatDictationControl onTranscript={vi.fn()} />);
    const route = await screen.findByRole("combobox", { name: "Dictation route" });
    await user.selectOptions(route, BROWSER_SPEECH_ROUTE_ID);
    await user.click(screen.getByRole("button", { name: "Start dictation" }));

    act(() => speech.instances[0]?.emitError("not-allowed"));

    expect(await screen.findByRole("status")).toHaveTextContent(
      "enable Hecate under both Microphone and Speech Recognition",
    );
  });

  it("requires an explicit choice before using a browser-managed speech service", async () => {
    vi.mocked(getDictationOptions).mockResolvedValue({ object: "dictation_options", data: [] });
    const speech = installSpeechRecognitionMocks();
    const onTranscript = vi.fn();
    const user = userEvent.setup();
    render(<ChatDictationControl onTranscript={onTranscript} />);

    const route = await screen.findByRole("combobox", { name: "Dictation route" });
    expect(route).toHaveValue("");
    expect(screen.getByRole("button", { name: "Start dictation" })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent(
      "The browser speech service may use its vendor's cloud.",
    );

    await user.selectOptions(route, BROWSER_SPEECH_ROUTE_ID);
    expect(screen.getByRole("status")).toHaveTextContent(
      "Speech recognition is controlled by the browser and may use its vendor's cloud service",
    );
    await user.click(screen.getByRole("button", { name: "Start dictation" }));
    const recognition = speech.instances[0];
    expect(recognition?.continuous).toBe(true);
    expect(recognition?.interimResults).toBe(true);
    act(() => recognition?.emitFinalResult("browser service draft"));
    await user.click(screen.getByRole("button", { name: "Stop dictation recording" }));
    act(() => recognition?.emitEnd());

    expect(onTranscript).toHaveBeenCalledWith("browser service draft");
    expect(createDictationTranscription).not.toHaveBeenCalled();
  });

  it("aborts browser speech and discards late results after unmount", async () => {
    localStorage.setItem("hecate.dictationProvider", BROWSER_SPEECH_ROUTE_ID);
    const speech = installSpeechRecognitionMocks();
    const onTranscript = vi.fn();
    const user = userEvent.setup();
    const { unmount } = render(<ChatDictationControl onTranscript={onTranscript} />);
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        BROWSER_SPEECH_ROUTE_ID,
      ),
    );
    await user.click(screen.getByRole("button", { name: "Start dictation" }));
    const recognition = speech.instances[0];

    act(() => unmount());
    expect(recognition?.abort).toHaveBeenCalledOnce();
    act(() => {
      recognition?.emitFinalResult("late transcript");
      recognition?.emitEnd();
    });
    expect(onTranscript).not.toHaveBeenCalled();
    expect(createDictationTranscription).not.toHaveBeenCalled();
  });

  it("aborts browser speech when the composer becomes disabled", async () => {
    localStorage.setItem("hecate.dictationProvider", BROWSER_SPEECH_ROUTE_ID);
    const speech = installSpeechRecognitionMocks();
    const onTranscript = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(
      <ChatDictationControl disabled={false} onTranscript={onTranscript} />,
    );
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        BROWSER_SPEECH_ROUTE_ID,
      ),
    );
    await user.click(screen.getByRole("button", { name: "Start dictation" }));
    const recognition = speech.instances[0];
    act(() => recognition?.emitFinalResult("late browser transcript"));

    rerender(<ChatDictationControl disabled onTranscript={onTranscript} />);

    expect(recognition?.abort).toHaveBeenCalledOnce();
    act(() => recognition?.emitEnd());
    expect(onTranscript).not.toHaveBeenCalled();
    expect(createDictationTranscription).not.toHaveBeenCalled();
  });

  it("reports browser-service network failures without switching routes", async () => {
    vi.mocked(getDictationOptions).mockResolvedValue({ object: "dictation_options", data: [] });
    const speech = installSpeechRecognitionMocks();
    const user = userEvent.setup();
    render(<ChatDictationControl onTranscript={vi.fn()} />);
    const route = await screen.findByRole("combobox", { name: "Dictation route" });
    await user.selectOptions(route, BROWSER_SPEECH_ROUTE_ID);
    await user.click(screen.getByRole("button", { name: "Start dictation" }));

    act(() => speech.instances[0]?.emitError("network"));

    expect(await screen.findByRole("status")).toHaveTextContent(
      "The browser speech service could not connect.",
    );
    expect(route).toHaveValue(BROWSER_SPEECH_ROUTE_ID);
    expect(createDictationTranscription).not.toHaveBeenCalled();
  });

  it("settles through the latest callback so typing during dictation is preserved", async () => {
    localStorage.setItem("hecate.dictationProvider", BROWSER_SPEECH_ROUTE_ID);
    const speech = installSpeechRecognitionMocks();
    const user = userEvent.setup();

    function EditableDraftHarness() {
      const [draft, setDraft] = useState("Before ");
      return (
        <>
          <textarea
            aria-label="Editable draft"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
          />
          <ChatDictationControl onTranscript={(text) => setDraft(`${draft}${text}`)} />
        </>
      );
    }

    render(<EditableDraftHarness />);
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation route" })).toHaveValue(
        BROWSER_SPEECH_ROUTE_ID,
      ),
    );
    await user.click(screen.getByRole("button", { name: "Start dictation" }));
    await user.type(screen.getByRole("textbox", { name: "Editable draft" }), "typed ");
    const recognition = speech.instances[0];
    act(() => recognition?.emitFinalResult("spoken"));
    await user.click(screen.getByRole("button", { name: "Stop dictation recording" }));
    act(() => recognition?.emitEnd());

    expect(screen.getByRole("textbox", { name: "Editable draft" })).toHaveValue(
      "Before typed spoken",
    );
  });
});

function installSpeechRecognitionMocks() {
  const instances: MockBrowserSpeechRecognition[] = [];
  class MockSpeechRecognitionConstructor extends MockBrowserSpeechRecognition {
    constructor() {
      super();
      instances.push(this);
    }
  }
  Object.defineProperty(window, "SpeechRecognition", {
    configurable: true,
    value: MockSpeechRecognitionConstructor,
  });
  return { instances };
}

class MockBrowserSpeechRecognition {
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
    const alternative = { transcript };
    const result = Object.assign([alternative], {
      isFinal: true,
      item: (index: number) => (index === 0 ? alternative : null),
    });
    const results = Object.assign([result], {
      item: (index: number) => (index === 0 ? result : null),
    });
    const event = Object.assign(new Event("result"), { resultIndex: 0, results });
    this.onresult?.(event as BrowserSpeechRecognitionResultEvent);
  }

  emitError(error: BrowserSpeechRecognitionErrorCode) {
    const event = Object.assign(new Event("error"), { error });
    this.onerror?.(event as BrowserSpeechRecognitionErrorEvent);
  }

  emitEnd() {
    this.onend?.();
  }
}

function installRecorderMocks() {
  const stopTrack = vi.fn();
  const stream = {
    getTracks: () => [{ stop: stopTrack }],
  } as unknown as MediaStream;
  Object.defineProperty(navigator, "mediaDevices", {
    configurable: true,
    value: { getUserMedia: vi.fn().mockResolvedValue(stream) },
  });
  vi.stubGlobal("MediaRecorder", MockMediaRecorder);
  return stopTrack;
}

class MockMediaRecorder extends EventTarget {
  static isTypeSupported(type: string) {
    return type === "audio/webm;codecs=opus";
  }

  readonly stream: MediaStream;
  readonly mimeType: string;
  state: RecordingState = "inactive";

  constructor(stream: MediaStream, options?: MediaRecorderOptions) {
    super();
    this.stream = stream;
    this.mimeType = options?.mimeType ?? "audio/webm";
  }

  start() {
    this.state = "recording";
  }

  stop() {
    this.state = "inactive";
    const data = new Event("dataavailable");
    Object.defineProperty(data, "data", {
      value: new Blob([new Uint8Array([0x1a, 0x45, 0xdf, 0xa3])], { type: "audio/webm" }),
    });
    this.dispatchEvent(data);
    this.dispatchEvent(new Event("stop"));
  }
}

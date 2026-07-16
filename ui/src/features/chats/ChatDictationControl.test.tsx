import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createDictationTranscription, getDictationOptions } from "../../lib/api";
import { ChatDictationControl } from "./ChatDictationControl";

const originalMediaDevices = Object.getOwnPropertyDescriptor(navigator, "mediaDevices");

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
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  if (originalMediaDevices) {
    Object.defineProperty(navigator, "mediaDevices", originalMediaDevices);
  } else {
    Reflect.deleteProperty(navigator, "mediaDevices");
  }
});

describe("ChatDictationControl", () => {
  it("defaults to the server's local-first route and explains disclosure", async () => {
    render(<ChatDictationControl onTranscript={vi.fn()} />);

    const provider = await screen.findByRole("combobox", { name: "Dictation provider" });
    await waitFor(() => expect(provider).toHaveValue("localai"));
    expect(screen.getByRole("status")).toHaveTextContent(
      "Audio goes only to localai; Hecate does not retain it.",
    );
  });

  it("records, stops tracks, transcribes through the selected route, and returns draft text", async () => {
    const stopTrack = installRecorderMocks();
    const onTranscript = vi.fn();
    const user = userEvent.setup();
    render(<ChatDictationControl onTranscript={onTranscript} />);
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation provider" })).toHaveValue("localai"),
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

  it("stops the microphone and does not disclose audio after unmount", async () => {
    const stopTrack = installRecorderMocks();
    const user = userEvent.setup();
    const { unmount } = render(<ChatDictationControl onTranscript={vi.fn()} />);
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Dictation provider" })).toHaveValue("localai"),
    );
    await user.click(screen.getByRole("button", { name: "Start dictation" }));
    await screen.findByRole("button", { name: "Stop dictation recording" });

    act(() => unmount());

    expect(stopTrack).toHaveBeenCalled();
    expect(createDictationTranscription).not.toHaveBeenCalled();
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
      expect(screen.getByRole("combobox", { name: "Dictation provider" })).toHaveValue("localai"),
    );

    await user.click(screen.getByRole("button", { name: "Start dictation" }));

    expect(await screen.findByText(/Microphone access was denied/)).toBeVisible();
  });
});

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

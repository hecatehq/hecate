import { useEffect, useId, useRef, useState, type MutableRefObject } from "react";

import { ApiError, createDictationTranscription, getDictationOptions } from "../../lib/api";
import { parseStoredString, usePersistedState } from "../../lib/persistedState";
import { isTauriRuntime } from "../../lib/tauri";
import type { DictationProviderOption } from "../../types/dictation";
import { Icon, Icons } from "../shared/ui";

const MAX_DICTATION_BYTES = 10 * 1024 * 1024;
const MAX_DICTATION_DURATION_MS = 2 * 60 * 1000;
const DICTATION_PROVIDER_STORAGE_KEY = "hecate.dictationProvider";
const RECORDER_MIME_TYPES = [
  "audio/webm;codecs=opus",
  "audio/ogg;codecs=opus",
  "audio/mp4",
] as const;

type DictationPhase = "idle" | "requesting" | "recording" | "transcribing";
type DictationOptionsPhase = "loading" | "ready" | "failed";

type DictationCaptureSupport =
  | { available: true; reason: "" }
  | { available: false; reason: string };

export type ChatDictationControlProps = {
  disabled?: boolean;
  onOpenConnections?: () => void;
  onTranscript: (text: string) => void;
};

export function ChatDictationControl({
  disabled = false,
  onOpenConnections,
  onTranscript,
}: ChatDictationControlProps) {
  const [options, setOptions] = useState<DictationProviderOption[]>([]);
  const [selectedProvider, setSelectedProvider] = usePersistedState(
    DICTATION_PROVIDER_STORAGE_KEY,
    parseStoredString,
    "",
  );
  const [phase, setPhase] = useState<DictationPhase>("idle");
  const [optionsPhase, setOptionsPhase] = useState<DictationOptionsPhase>("loading");
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  const [error, setError] = useState("");
  const statusID = useId();
  const recorderRef = useRef<MediaRecorder | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const chunksRef = useRef<Blob[]>([]);
  const requestRef = useRef<AbortController | null>(null);
  const stopTimerRef = useRef<number | null>(null);
  const elapsedTimerRef = useRef<number | null>(null);
  const mountedRef = useRef(true);

  useEffect(() => {
    const controller = new AbortController();
    void getDictationOptions(controller.signal)
      .then((response) => {
        if (!mountedRef.current) return;
        setOptions(response.data);
        setOptionsPhase("ready");
        const selectedAvailable = response.data.some(
          (option) => option.provider === selectedProvider && option.available,
        );
        if (!selectedAvailable) {
          setSelectedProvider(response.data.find((option) => option.available)?.provider ?? "");
        }
      })
      .catch((cause: unknown) => {
        if (!controller.signal.aborted && mountedRef.current) {
          setOptionsPhase("failed");
          setError(dictationErrorMessage(cause, "Could not load dictation providers."));
        }
      });
    return () => controller.abort();
    // Options are a capability snapshot for this mount. Provider changes are
    // re-fenced by the server immediately before audio disclosure.
  }, []);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      requestRef.current?.abort();
      clearDictationTimers(stopTimerRef, elapsedTimerRef);
      const recorder = recorderRef.current;
      recorderRef.current = null;
      if (recorder?.state === "recording") recorder.stop();
      recorder?.stream.getTracks().forEach((track) => track.stop());
      streamRef.current?.getTracks().forEach((track) => track.stop());
      streamRef.current = null;
    };
  }, []);

  const selectedOption = options.find((option) => option.provider === selectedProvider);
  const availableOptions = options.filter((option) => option.available);
  const captureSupport = dictationCaptureSupport();
  const active = phase !== "idle";
  const noAvailableProvider = optionsPhase === "ready" && availableOptions.length === 0;
  const unavailableMessage = !captureSupport.available
    ? captureSupport.reason
    : optionsPhase === "loading"
      ? "Checking dictation providers…"
      : optionsPhase === "failed"
        ? "Dictation provider status could not be loaded."
        : noAvailableProvider
          ? dictationProviderUnavailableMessage(options)
          : "";
  const unavailable = unavailableMessage !== "";
  const providerSetupNeeded = optionsPhase === "failed" || noAvailableProvider;

  async function startRecording() {
    if (disabled || active || !selectedOption?.available) return;
    setError("");
    setElapsedSeconds(0);
    setPhase("requesting");
    try {
      const currentCaptureSupport = dictationCaptureSupport();
      if (!currentCaptureSupport.available) {
        throw new Error(currentCaptureSupport.reason);
      }
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      if (!mountedRef.current) {
        stream.getTracks().forEach((track) => track.stop());
        return;
      }
      streamRef.current = stream;
      chunksRef.current = [];
      const mimeType = preferredRecorderMimeType();
      const recorder = mimeType
        ? new MediaRecorder(stream, { mimeType })
        : new MediaRecorder(stream);
      recorderRef.current = recorder;
      recorder.addEventListener("dataavailable", (event) => {
        if (event.data.size > 0) chunksRef.current.push(event.data);
      });
      recorder.addEventListener(
        "stop",
        () => {
          void transcribeRecording(recorder);
        },
        { once: true },
      );
      recorder.start();
      setPhase("recording");
      stopTimerRef.current = window.setTimeout(stopRecording, MAX_DICTATION_DURATION_MS);
      elapsedTimerRef.current = window.setInterval(
        () => setElapsedSeconds((current) => current + 1),
        1000,
      );
    } catch (cause) {
      stopStream();
      if (mountedRef.current) {
        setPhase("idle");
        setError(dictationErrorMessage(cause, "Microphone access failed."));
      }
    }
  }

  function stopRecording() {
    clearDictationTimers(stopTimerRef, elapsedTimerRef);
    const recorder = recorderRef.current;
    if (recorder?.state === "recording") {
      setPhase("transcribing");
      recorder.stop();
    }
  }

  async function transcribeRecording(recorder: MediaRecorder) {
    recorderRef.current = null;
    stopStream();
    if (!mountedRef.current) return;
    const mediaType = normalizedRecorderMediaType(recorder.mimeType || chunksRef.current[0]?.type);
    const blob = new Blob(chunksRef.current, { type: mediaType });
    chunksRef.current = [];
    if (blob.size === 0) {
      setPhase("idle");
      setError("The recording was empty. Try again.");
      return;
    }
    if (blob.size > MAX_DICTATION_BYTES) {
      setPhase("idle");
      setError("The recording exceeds the 10 MiB limit. Record a shorter clip.");
      return;
    }
    const controller = new AbortController();
    requestRef.current = controller;
    try {
      const file = new File([blob], `dictation.${dictationFileExtension(mediaType)}`, {
        type: mediaType,
      });
      const result = await createDictationTranscription(
        selectedOption?.provider ?? selectedProvider,
        file,
        controller.signal,
      );
      if (!mountedRef.current) return;
      onTranscript(result.text);
      setError("");
    } catch (cause) {
      if (!controller.signal.aborted && mountedRef.current) {
        setError(dictationErrorMessage(cause, "Dictation transcription failed."));
      }
    } finally {
      if (requestRef.current === controller) requestRef.current = null;
      if (mountedRef.current) setPhase("idle");
    }
  }

  function stopStream() {
    streamRef.current?.getTracks().forEach((track) => track.stop());
    streamRef.current = null;
  }

  const phaseLabel =
    phase === "requesting"
      ? "Requesting microphone…"
      : phase === "recording"
        ? `Recording ${formatElapsed(elapsedSeconds)}`
        : phase === "transcribing"
          ? "Transcribing…"
          : "";

  return (
    <div
      role="group"
      aria-label="Dictation"
      style={{
        display: "flex",
        alignItems: "center",
        gap: 6,
        minHeight: 28,
        minWidth: 0,
        color: "var(--t3)",
        fontFamily: "var(--font-mono)",
        fontSize: 10,
      }}
    >
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        aria-label={phase === "recording" ? "Stop dictation recording" : "Start dictation"}
        aria-describedby={statusID}
        disabled={disabled || unavailable || phase === "requesting" || phase === "transcribing"}
        onClick={() => (phase === "recording" ? stopRecording() : void startRecording())}
        style={{
          color: phase === "recording" ? "var(--red)" : "var(--t1)",
          padding: 4,
          flexShrink: 0,
        }}
        title={
          unavailable
            ? unavailableMessage
            : phase === "recording"
              ? "Stop dictation recording"
              : "Start dictation"
        }
      >
        <Icon d={phase === "recording" ? Icons.stop : Icons.microphone} size={13} />
      </button>
      {optionsPhase === "ready" && availableOptions.length > 0 && (
        <label style={{ display: "inline-flex", alignItems: "center", gap: 5, minWidth: 0 }}>
          <span className="sr-only">Dictation provider</span>
          <select
            aria-label="Dictation provider"
            value={selectedProvider}
            disabled={disabled || active}
            onChange={(event) => {
              setSelectedProvider(event.target.value);
              setError("");
            }}
            style={{
              maxWidth: 145,
              minWidth: 92,
              background: "transparent",
              border: "none",
              borderRadius: "var(--radius-sm)",
              color: "var(--t2)",
              font: "inherit",
              padding: "3px 4px",
            }}
          >
            {options.map((option) => (
              <option key={option.provider} value={option.provider} disabled={!option.available}>
                {option.provider}
                {option.provider_kind === "local" ? " · local" : " · cloud"}
                {!option.available ? " · unavailable" : ""}
              </option>
            ))}
          </select>
        </label>
      )}
      <span
        id={statusID}
        aria-live="polite"
        role="status"
        title={error || phaseLabel || undefined}
        style={{
          minWidth: 0,
          color: error ? "var(--red)" : undefined,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {error ||
          phaseLabel ||
          (unavailable
            ? unavailableMessage
            : `Audio goes only to ${selectedOption?.provider ?? selectedProvider}; Hecate does not retain it.`)}
      </span>
      {providerSetupNeeded && onOpenConnections && (
        <button
          type="button"
          className="btn btn-ghost btn-sm"
          aria-label="Set up dictation provider"
          onClick={onOpenConnections}
          style={{ padding: "3px 5px", flexShrink: 0 }}
          title="Open Connections to configure a speech-to-text provider"
        >
          Set up
        </button>
      )}
    </div>
  );
}

function dictationCaptureSupport(): DictationCaptureSupport {
  if (window.isSecureContext === false) {
    return {
      available: false,
      reason: "Dictation needs HTTPS or a loopback Hecate URL for microphone access.",
    };
  }
  if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") {
    return {
      available: false,
      reason: "This browser or app webview cannot record microphone audio.",
    };
  }
  return { available: true, reason: "" };
}

function dictationProviderUnavailableMessage(options: DictationProviderOption[]): string {
  const option = options.find((candidate) => !candidate.available);
  if (!option) {
    return "Dictation uses a separate speech-to-text provider, independent of the selected chat model or agent. Connect OpenAI, Groq, or LocalAI in Connections.";
  }
  const reason = option.unavailable_reason?.trim().replace(/[.\s]+$/, "");
  return `Dictation uses a separate speech-to-text route. ${option.provider} is unavailable${reason ? `: ${reason}` : ""}. Open Connections to fix it.`;
}

function preferredRecorderMimeType(): string {
  if (typeof MediaRecorder.isTypeSupported !== "function") return "";
  return RECORDER_MIME_TYPES.find((mimeType) => MediaRecorder.isTypeSupported(mimeType)) ?? "";
}

function normalizedRecorderMediaType(value = ""): string {
  const mediaType = value.split(";", 1)[0]?.trim().toLowerCase();
  if (mediaType === "video/webm") return "audio/webm";
  if (mediaType === "audio/webm" || mediaType === "audio/ogg" || mediaType === "audio/mp4") {
    return mediaType;
  }
  if (mediaType === "audio/mpeg" || mediaType === "audio/mp3" || mediaType === "audio/wav") {
    return mediaType;
  }
  return "audio/webm";
}

function dictationFileExtension(mediaType: string): string {
  if (mediaType === "audio/ogg") return "ogg";
  if (mediaType === "audio/mp4") return "m4a";
  if (mediaType === "audio/mpeg" || mediaType === "audio/mp3") return "mp3";
  if (mediaType === "audio/wav") return "wav";
  return "webm";
}

function clearDictationTimers(
  stopTimerRef: MutableRefObject<number | null>,
  elapsedTimerRef: MutableRefObject<number | null>,
) {
  if (stopTimerRef.current !== null) window.clearTimeout(stopTimerRef.current);
  if (elapsedTimerRef.current !== null) window.clearInterval(elapsedTimerRef.current);
  stopTimerRef.current = null;
  elapsedTimerRef.current = null;
}

function dictationErrorMessage(cause: unknown, fallback: string): string {
  if (cause instanceof ApiError) {
    return [cause.userMessage || cause.message, cause.operatorAction].filter(Boolean).join(" ");
  }
  if (cause instanceof DOMException && cause.name === "NotAllowedError") {
    if (isTauriRuntime()) {
      return desktopMicrophonePermissionMessage();
    }
    return "Microphone access is blocked. Open this site's controls in the address bar, set Microphone to Allow, reload Hecate, and try again.";
  }
  if (cause instanceof DOMException && cause.name === "NotFoundError") {
    return "No microphone was found. Connect one and try again.";
  }
  if (cause instanceof DOMException && cause.name === "NotReadableError") {
    return "The microphone is unavailable. Close other apps using it and try again.";
  }
  return cause instanceof Error && cause.message ? cause.message : fallback;
}

function desktopMicrophonePermissionMessage(): string {
  const platform = navigator.platform.toLowerCase();
  if (platform.includes("mac")) {
    return "Microphone access is blocked. Open System Settings → Privacy & Security → Microphone, enable Hecate, then restart Hecate.";
  }
  if (platform.includes("win")) {
    return "Microphone access is blocked. Open Windows Settings → Privacy & security → Microphone, allow microphone access for desktop apps and Hecate, then restart Hecate.";
  }
  return "Microphone access is blocked. Allow Hecate microphone access in your system privacy settings, verify the input device is enabled, then restart Hecate.";
}

function formatElapsed(seconds: number): string {
  const minutes = Math.floor(seconds / 60);
  return `${minutes}:${String(seconds % 60).padStart(2, "0")}`;
}

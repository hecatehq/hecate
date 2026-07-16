import { useEffect, useRef, useState, type MutableRefObject } from "react";

import { ApiError, createDictationTranscription, getDictationOptions } from "../../lib/api";
import { parseStoredString, usePersistedState } from "../../lib/persistedState";
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

export type ChatDictationControlProps = {
  disabled?: boolean;
  onTranscript: (text: string) => void;
};

export function ChatDictationControl({
  disabled = false,
  onTranscript,
}: ChatDictationControlProps) {
  const [options, setOptions] = useState<DictationProviderOption[]>([]);
  const [selectedProvider, setSelectedProvider] = usePersistedState(
    DICTATION_PROVIDER_STORAGE_KEY,
    parseStoredString,
    "",
  );
  const [phase, setPhase] = useState<DictationPhase>("idle");
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  const [error, setError] = useState("");
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
        const selectedAvailable = response.data.some(
          (option) => option.provider === selectedProvider && option.available,
        );
        if (!selectedAvailable) {
          setSelectedProvider(response.data.find((option) => option.available)?.provider ?? "");
        }
      })
      .catch((cause: unknown) => {
        if (!controller.signal.aborted && mountedRef.current) {
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
  const active = phase !== "idle";
  const unavailable = availableOptions.length === 0;

  async function startRecording() {
    if (disabled || active || !selectedOption?.available) return;
    setError("");
    setElapsedSeconds(0);
    setPhase("requesting");
    try {
      if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") {
        throw new Error("This browser does not support microphone recording.");
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
        maxWidth: 820,
        margin: "0 auto 6px",
        display: "flex",
        alignItems: "center",
        gap: 8,
        minHeight: 28,
        color: "var(--t3)",
        fontFamily: "var(--font-mono)",
        fontSize: 10,
      }}
    >
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        aria-label={phase === "recording" ? "Stop dictation recording" : "Start dictation"}
        disabled={disabled || unavailable || phase === "requesting" || phase === "transcribing"}
        onClick={() => (phase === "recording" ? stopRecording() : void startRecording())}
        style={{
          color: phase === "recording" ? "var(--red)" : "var(--t1)",
          gap: 5,
          padding: "4px 7px",
          flexShrink: 0,
        }}
      >
        <Icon d={phase === "recording" ? Icons.stop : Icons.microphone} size={13} />
        {phase === "recording" ? "Stop" : "Dictate"}
      </button>
      <label style={{ display: "inline-flex", alignItems: "center", gap: 5, minWidth: 0 }}>
        <span className="sr-only">Dictation provider</span>
        <select
          aria-label="Dictation provider"
          value={selectedProvider}
          disabled={disabled || active || unavailable}
          onChange={(event) => {
            setSelectedProvider(event.target.value);
            setError("");
          }}
          style={{
            maxWidth: 180,
            minWidth: 110,
            background: "var(--bg3)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius-sm)",
            color: "var(--t1)",
            font: "inherit",
            padding: "3px 5px",
          }}
        >
          {unavailable && (
            <option value="" hidden>
              No available providers
            </option>
          )}
          {options.map((option) => (
            <option key={option.provider} value={option.provider} disabled={!option.available}>
              {option.provider}
              {option.provider_kind === "local" ? " · local" : " · cloud"}
              {!option.available ? " · unavailable" : ""}
            </option>
          ))}
        </select>
      </label>
      <span
        aria-live="polite"
        role="status"
        style={{ minWidth: 0, color: error ? "var(--red)" : undefined }}
      >
        {error ||
          phaseLabel ||
          (unavailable
            ? "Configure OpenAI, Groq, or LocalAI in Connections."
            : `Audio goes only to ${selectedOption?.provider ?? selectedProvider}; Hecate does not retain it.`)}
      </span>
    </div>
  );
}

function preferredRecorderMimeType(): string {
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
    return "Microphone access was denied. Allow microphone access and try again.";
  }
  return cause instanceof Error && cause.message ? cause.message : fallback;
}

function formatElapsed(seconds: number): string {
  const minutes = Math.floor(seconds / 60);
  return `${minutes}:${String(seconds % 60).padStart(2, "0")}`;
}

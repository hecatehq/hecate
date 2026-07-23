import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type MutableRefObject,
} from "react";

import { ApiError, createDictationTranscription, getDictationOptions } from "../../lib/api";
import { parseStoredString, usePersistedState } from "../../lib/persistedState";
import { isTauriRuntime } from "../../lib/tauri";
import type { DictationProviderOption } from "../../types/dictation";
import { Icon, Icons } from "../shared/ui";
import {
  BROWSER_SPEECH_ROUTE_ID,
  browserSpeechLocale,
  browserSpeechRecognitionConstructor,
  buildDictationRoutes,
  resolveSelectedDictationRoute,
  type BrowserSpeechRecognitionErrorEvent,
  type DictationRoute,
} from "./chatDictation";
import { BrowserSpeechDictationSession } from "./browserSpeechDictation";

const MAX_DICTATION_BYTES = 10 * 1024 * 1024;
const MAX_DICTATION_DURATION_MS = 2 * 60 * 1000;
const SPEECH_FINALIZATION_TIMEOUT_MS = 10 * 1000;
const RETIRED_ON_DEVICE_SPEECH_ROUTE_ID = "client:web-speech:on-device";
// Keep the original key so an existing provider choice can be migrated to a
// typed route id without silently changing the operator's disclosure boundary.
const DICTATION_ROUTE_STORAGE_KEY = "hecate.dictationProvider";
const RECORDER_MIME_TYPES = [
  "audio/webm;codecs=opus",
  "audio/ogg;codecs=opus",
  "audio/mp4",
] as const;

type DictationPhase = "idle" | "requesting" | "recording" | "processing";
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
  const [selectedRouteID, setSelectedRouteID] = usePersistedState(
    DICTATION_ROUTE_STORAGE_KEY,
    parseStoredString,
    "",
  );
  const [phase, setPhase] = useState<DictationPhase>("idle");
  const [optionsPhase, setOptionsPhase] = useState<DictationOptionsPhase>("loading");
  const [optionsRefresh, setOptionsRefresh] = useState(0);
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  const [error, setError] = useState("");
  const [optionsError, setOptionsError] = useState("");
  const statusID = useId();
  const recorderRef = useRef<MediaRecorder | null>(null);
  const speechSessionRef = useRef<BrowserSpeechDictationSession | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const chunksRef = useRef<Blob[]>([]);
  const requestRef = useRef<AbortController | null>(null);
  const stopTimerRef = useRef<number | null>(null);
  const elapsedTimerRef = useRef<number | null>(null);
  const mountedRef = useRef(true);
  const disabledRef = useRef(disabled);
  const captureGenerationRef = useRef(0);
  const onTranscriptRef = useRef(onTranscript);
  const speechConstructor = browserSpeechRecognitionConstructor();
  const speechLocale = browserSpeechLocale();
  const browserSpeechAvailable =
    window.isSecureContext !== false && speechConstructor !== undefined;

  useLayoutEffect(() => {
    onTranscriptRef.current = onTranscript;
  }, [onTranscript]);

  const cancelActiveDictation = useCallback((updatePhase: boolean) => {
    captureGenerationRef.current += 1;
    requestRef.current?.abort();
    requestRef.current = null;
    speechSessionRef.current?.abort();
    speechSessionRef.current = null;
    clearDictationTimers(stopTimerRef, elapsedTimerRef);
    const recorder = recorderRef.current;
    recorderRef.current = null;
    if (recorder?.state === "recording") recorder.stop();
    recorder?.stream.getTracks().forEach((track) => track.stop());
    streamRef.current?.getTracks().forEach((track) => track.stop());
    streamRef.current = null;
    chunksRef.current = [];
    if (updatePhase && mountedRef.current) {
      setElapsedSeconds(0);
      setPhase("idle");
    }
  }, []);

  useLayoutEffect(() => {
    disabledRef.current = disabled;
    if (disabled) cancelActiveDictation(true);
  }, [cancelActiveDictation, disabled]);

  useEffect(() => {
    const controller = new AbortController();
    setOptionsPhase("loading");
    setOptionsError("");
    void getDictationOptions(controller.signal)
      .then((response) => {
        if (!mountedRef.current) return;
        setOptions(response.data);
        setOptionsPhase("ready");
      })
      .catch((cause: unknown) => {
        if (!controller.signal.aborted && mountedRef.current) {
          setOptionsPhase("failed");
          setOptionsError(dictationErrorMessage(cause, "Could not load dictation providers."));
        }
      });
    return () => controller.abort();
    // Options are a capability snapshot for this mount. Provider changes are
    // re-fenced by the server immediately before audio disclosure.
  }, [optionsRefresh]);

  const routes = useMemo(
    () => buildDictationRoutes(options, browserSpeechAvailable),
    [browserSpeechAvailable, options],
  );

  useEffect(() => {
    if (optionsPhase === "loading") return;
    const resolved = resolveSelectedDictationRoute(selectedRouteID, routes);
    if (resolved !== selectedRouteID) setSelectedRouteID(resolved);
  }, [optionsPhase, routes, selectedRouteID, setSelectedRouteID]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      cancelActiveDictation(false);
    };
  }, [cancelActiveDictation]);

  const selectedRoute = routes.find((route) => route.id === selectedRouteID);
  const selectedRouteValue = selectedRoute?.id ?? "";
  const savedRouteUnavailable = selectedRouteID !== "" && !selectedRoute;
  const availableOptions = options.filter((option) => option.available);
  const captureSupport = selectedRoute
    ? dictationRouteCaptureSupport(selectedRoute)
    : { available: false as const, reason: "Choose a dictation route." };
  const active = phase !== "idle";
  const noAvailableProvider = optionsPhase === "ready" && availableOptions.length === 0;
  const routeChecksLoading = optionsPhase === "loading";
  const unavailableMessage = routeChecksLoading
    ? "Checking dictation routes…"
    : selectedRoute
      ? !selectedRoute.available
        ? unavailableProviderRouteMessage(selectedRoute)
        : captureSupport.available
          ? ""
          : captureSupport.reason
      : savedRouteUnavailable
        ? savedDictationRouteUnavailableMessage(selectedRouteID, optionsPhase)
        : routes.some((route) => route.id === BROWSER_SPEECH_ROUTE_ID)
          ? "Choose a dictation route. The browser speech service may use its vendor's cloud."
          : optionsPhase === "failed"
            ? `Dictation route status could not be loaded. ${systemDictationFallbackMessage()}`
            : dictationProviderUnavailableMessage(options);
  const unavailable = unavailableMessage !== "";
  const providerSetupNeeded =
    noAvailableProvider && (!selectedRoute || selectedRoute.kind === "provider");
  const routeStatusRetryNeeded = optionsPhase === "failed";

  async function startDictation() {
    if (disabled || active || !selectedRoute?.available || !captureSupport.available) return;
    const captureGeneration = captureGenerationRef.current + 1;
    captureGenerationRef.current = captureGeneration;
    if (selectedRoute.kind === "provider") {
      await startProviderRecording(selectedRoute.provider, captureGeneration);
      return;
    }
    startBrowserSpeech(selectedRoute, captureGeneration);
  }

  async function startProviderRecording(
    provider: DictationProviderOption,
    captureGeneration: number,
  ) {
    setError("");
    setElapsedSeconds(0);
    setPhase("requesting");
    try {
      const currentCaptureSupport = providerRecordingSupport();
      if (!currentCaptureSupport.available) {
        throw new Error(currentCaptureSupport.reason);
      }
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      if (!captureIsCurrent(captureGeneration)) {
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
        if (captureGenerationRef.current === captureGeneration && event.data.size > 0) {
          chunksRef.current.push(event.data);
        }
      });
      recorder.addEventListener(
        "stop",
        () => {
          void transcribeRecording(recorder, provider.provider, captureGeneration);
        },
        { once: true },
      );
      recorder.start();
      setPhase("recording");
      stopTimerRef.current = window.setTimeout(stopDictation, MAX_DICTATION_DURATION_MS);
      elapsedTimerRef.current = window.setInterval(
        () => setElapsedSeconds((current) => current + 1),
        1000,
      );
    } catch (cause) {
      if (captureIsCurrent(captureGeneration)) {
        stopStream();
        setPhase("idle");
        setError(dictationErrorMessage(cause, "Microphone access failed."));
      }
    }
  }

  function startBrowserSpeech(route: DictationRoute, captureGeneration: number) {
    if (route.kind === "provider") return;
    const constructor = browserSpeechRecognitionConstructor();
    if (!constructor) {
      setPhase("idle");
      setError("This browser or app webview does not support speech recognition.");
      return;
    }
    let session: BrowserSpeechDictationSession;
    session = new BrowserSpeechDictationSession({
      constructor,
      locale: speechLocale,
      maxDurationMS: MAX_DICTATION_DURATION_MS,
      finalizationTimeoutMS: SPEECH_FINALIZATION_TIMEOUT_MS,
      errorMessage: dictationErrorMessage,
      recognitionErrorMessage: browserSpeechErrorMessage,
      onElapsed: (seconds) => {
        if (captureIsCurrent(captureGeneration)) setElapsedSeconds(seconds);
      },
      onError: (message) => {
        if (captureIsCurrent(captureGeneration)) setError(message);
      },
      onPhase: (nextPhase) => {
        if (captureIsCurrent(captureGeneration)) setPhase(nextPhase);
      },
      onSettled: () => {
        if (speechSessionRef.current === session) speechSessionRef.current = null;
      },
      onTranscript: (text) => {
        if (captureIsCurrent(captureGeneration)) onTranscriptRef.current(text);
      },
    });
    speechSessionRef.current = session;
    session.start();
  }

  function stopDictation() {
    const speechSession = speechSessionRef.current;
    if (speechSession) {
      speechSession.stop();
      return;
    }
    clearDictationTimers(stopTimerRef, elapsedTimerRef);
    const recorder = recorderRef.current;
    if (recorder?.state === "recording") {
      setPhase("processing");
      recorder.stop();
    }
  }

  function retryRoutes() {
    setError("");
    setOptionsError("");
    setOptionsPhase("loading");
    setOptionsRefresh((attempt) => attempt + 1);
  }

  async function transcribeRecording(
    recorder: MediaRecorder,
    provider: string,
    captureGeneration: number,
  ) {
    if (recorderRef.current === recorder) recorderRef.current = null;
    stopStream(recorder.stream);
    if (!captureIsCurrent(captureGeneration)) return;
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
      const result = await createDictationTranscription(provider, file, controller.signal);
      if (!captureIsCurrent(captureGeneration)) return;
      onTranscriptRef.current(result.text);
      setError("");
    } catch (cause) {
      if (!controller.signal.aborted && captureIsCurrent(captureGeneration)) {
        setError(dictationErrorMessage(cause, "Dictation transcription failed."));
      }
    } finally {
      if (requestRef.current === controller) requestRef.current = null;
      if (captureIsCurrent(captureGeneration)) setPhase("idle");
    }
  }

  function captureIsCurrent(captureGeneration: number): boolean {
    return (
      mountedRef.current &&
      !disabledRef.current &&
      captureGenerationRef.current === captureGeneration
    );
  }

  function stopStream(stream = streamRef.current) {
    stream?.getTracks().forEach((track) => track.stop());
    if (streamRef.current === stream) streamRef.current = null;
  }

  const phaseLabel =
    phase === "requesting"
      ? "Requesting microphone…"
      : phase === "recording"
        ? selectedRoute?.kind === "provider"
          ? "Recording"
          : "Listening"
        : phase === "processing"
          ? selectedRoute?.kind === "provider"
            ? "Transcribing…"
            : "Finishing dictation…"
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
        className="btn btn-ghost btn-sm chat-composer-touch-action"
        aria-label={phase === "recording" ? "Stop dictation recording" : "Start dictation"}
        aria-describedby={statusID}
        disabled={disabled || unavailable || phase === "requesting" || phase === "processing"}
        onClick={() => (phase === "recording" ? stopDictation() : void startDictation())}
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
      {!routeChecksLoading && routes.length > 0 && (
        <label style={{ display: "inline-flex", alignItems: "center", gap: 5, minWidth: 0 }}>
          <span className="sr-only">Dictation route</span>
          <select
            value={selectedRouteValue}
            disabled={disabled || active}
            onChange={(event) => {
              setSelectedRouteID(event.target.value);
              setError("");
            }}
            style={{
              maxWidth: 220,
              minWidth: 92,
              background: "transparent",
              border: "none",
              borderRadius: "var(--radius-sm)",
              color: "var(--t2)",
              font: "inherit",
              padding: "3px 4px",
            }}
          >
            {!selectedRoute && (
              <option value="" disabled>
                Choose route…
              </option>
            )}
            {routes.map((route) => (
              <option key={route.id} value={route.id} disabled={!route.available}>
                {route.label}
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
          (unavailable ? unavailableMessage : (selectedRoute?.disclosure ?? unavailableMessage))}
      </span>
      {phase === "recording" && (
        <span
          aria-label={`${selectedRoute?.kind === "provider" ? "Recording" : "Dictation"} duration ${formatElapsed(elapsedSeconds)}`}
          aria-live="off"
          style={{ flexShrink: 0 }}
          title={`${selectedRoute?.kind === "provider" ? "Recording" : "Dictation"} duration ${formatElapsed(elapsedSeconds)}`}
        >
          {formatElapsed(elapsedSeconds)}
        </span>
      )}
      {routeStatusRetryNeeded && (
        <button
          type="button"
          className="btn btn-ghost btn-sm"
          aria-label="Retry dictation route check"
          disabled={disabled || active}
          onClick={retryRoutes}
          style={{ padding: "3px 5px", flexShrink: 0 }}
          title={optionsError || "Retry loading dictation route status"}
        >
          Retry
        </button>
      )}
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

function dictationRouteCaptureSupport(route: DictationRoute): DictationCaptureSupport {
  if (route.kind === "provider") return providerRecordingSupport();
  if (window.isSecureContext === false) {
    return {
      available: false,
      reason: "Dictation needs HTTPS or a loopback Hecate URL for microphone access.",
    };
  }
  if (!browserSpeechRecognitionConstructor()) {
    return {
      available: false,
      reason: "This browser or app webview does not support speech recognition.",
    };
  }
  return { available: true, reason: "" };
}

function providerRecordingSupport(): DictationCaptureSupport {
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
    return `Dictation uses a separate speech-to-text route, independent of the selected chat model or agent. Connect OpenAI, Groq, or LocalAI in Connections. ${systemDictationFallbackMessage()}`;
  }
  const reason = option.unavailable_reason?.trim().replace(/[.\s]+$/, "");
  return `Dictation uses a separate speech-to-text route. ${option.provider} is unavailable${reason ? `: ${reason}` : ""}. Open Connections to fix it. ${systemDictationFallbackMessage()}`;
}

function unavailableProviderRouteMessage(route: DictationRoute): string {
  if (route.kind !== "provider") return "The selected dictation route is unavailable.";
  const reason = route.provider.unavailable_reason?.trim().replace(/[.\s]+$/, "");
  return `${route.provider.provider} is unavailable${reason ? `: ${reason}` : ""}. Hecate did not switch dictation routes; fix it in Connections or explicitly choose another route.`;
}

function savedDictationRouteUnavailableMessage(
  routeID: string,
  optionsPhase: DictationOptionsPhase,
): string {
  if (routeID === RETIRED_ON_DEVICE_SPEECH_ROUTE_ID) {
    return "The saved experimental on-device browser route is no longer available. Hecate did not switch to a cloud route; choose a transcription provider or explicitly choose the browser speech service, which may use its vendor's cloud.";
  }
  if (routeID === BROWSER_SPEECH_ROUTE_ID) {
    return "The saved browser speech route is unavailable in this browser or app webview. Hecate did not switch routes; explicitly choose another route.";
  }
  if (optionsPhase === "failed") {
    return "The saved dictation provider could not be verified because provider status failed to load. Hecate did not switch routes; retry the check before recording.";
  }
  return "The saved dictation provider is no longer advertised. Hecate did not switch routes; fix it in Connections or explicitly choose another route.";
}

function browserSpeechErrorMessage(
  event: BrowserSpeechRecognitionErrorEvent,
  locale: string,
): string {
  switch (event.error) {
    case "not-allowed":
      return isTauriRuntime()
        ? desktopSpeechRecognitionPermissionMessage()
        : "Microphone or speech recognition access is blocked. Open this site's controls in the address bar, allow Microphone and Speech Recognition when listed, reload Hecate, and try again.";
    case "service-not-allowed":
      return "The browser speech service is blocked. Check the browser's speech and microphone permissions, then retry or choose another route.";
    case "audio-capture":
      return "No usable microphone was found. Connect or enable one and try again.";
    case "network":
      return "The browser speech service could not connect. Retry or choose another dictation route.";
    case "language-not-supported":
      return `${locale} is not supported by this speech-recognition route. Choose another route or change the browser language.`;
    case "no-speech":
      return "No speech was recognized. Try again or choose another dictation route.";
    case "aborted":
      return "Dictation was cancelled before a transcript was ready.";
    default:
      return event.message?.trim() || "Speech recognition failed. Choose another route or retry.";
  }
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

function desktopSpeechRecognitionPermissionMessage(): string {
  const platform = navigator.platform.toLowerCase();
  if (platform.includes("mac")) {
    return "Speech recognition access is blocked. Open System Settings → Privacy & Security, enable Hecate under both Microphone and Speech Recognition, then restart Hecate.";
  }
  if (platform.includes("win")) {
    return "Speech recognition access is blocked. Allow microphone access for desktop apps and Hecate in Windows privacy settings, verify speech services are enabled, then restart Hecate.";
  }
  return "Speech recognition access is blocked. Allow Hecate microphone and speech recognition access in your system privacy settings, then restart Hecate.";
}

function systemDictationFallbackMessage(): string {
  const platform = navigator.platform.toLowerCase();
  if (platform.includes("mac")) {
    return "You can also focus the message field and use the macOS Dictation shortcut configured in Keyboard settings.";
  }
  if (platform.includes("win")) {
    return "You can also focus the message field and press Win+H; Windows voice typing may process speech online.";
  }
  return "You can still use any operating-system dictation input available in the focused message field.";
}

function formatElapsed(seconds: number): string {
  const minutes = Math.floor(seconds / 60);
  return `${minutes}:${String(seconds % 60).padStart(2, "0")}`;
}

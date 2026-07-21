import {
  finalSpeechTranscript,
  type BrowserSpeechRecognition,
  type BrowserSpeechRecognitionConstructor,
  type BrowserSpeechRecognitionErrorEvent,
} from "./chatDictation";

export type BrowserSpeechDictationPhase = "idle" | "requesting" | "recording" | "processing";

type BrowserSpeechDictationOptions = {
  constructor: BrowserSpeechRecognitionConstructor;
  locale: string;
  maxDurationMS: number;
  finalizationTimeoutMS: number;
  errorMessage: (cause: unknown, fallback: string) => string;
  recognitionErrorMessage: (event: BrowserSpeechRecognitionErrorEvent, locale: string) => string;
  onElapsed: (seconds: number) => void;
  onError: (message: string) => void;
  onPhase: (phase: BrowserSpeechDictationPhase) => void;
  onSettled: () => void;
  onTranscript: (text: string) => void;
};

export class BrowserSpeechDictationSession {
  private recognition: BrowserSpeechRecognition | null = null;
  private transcript = "";
  private settled = false;
  private maxDurationTimer: number | null = null;
  private elapsedTimer: number | null = null;
  private finalizationTimer: number | null = null;
  private elapsedSeconds = 0;

  constructor(private readonly options: BrowserSpeechDictationOptions) {}

  start() {
    this.options.onError("");
    this.options.onElapsed(0);
    this.options.onPhase("requesting");
    try {
      const recognition = new this.options.constructor();
      this.recognition = recognition;
      recognition.lang = this.options.locale;
      recognition.continuous = true;
      recognition.interimResults = true;
      recognition.maxAlternatives = 1;
      recognition.onresult = (event) => {
        if (!this.settled) this.transcript = finalSpeechTranscript(event);
      };
      recognition.onerror = (event) => {
        const message = this.options.recognitionErrorMessage(event, this.options.locale);
        this.finish(message);
        abortRecognition(recognition);
      };
      recognition.onend = () => this.finish();
      recognition.start();
      if (this.settled) return;
      this.options.onPhase("recording");
      this.maxDurationTimer = window.setTimeout(() => this.stop(), this.options.maxDurationMS);
      this.elapsedTimer = window.setInterval(() => {
        this.elapsedSeconds += 1;
        this.options.onElapsed(this.elapsedSeconds);
      }, 1000);
    } catch (cause) {
      const recognition = this.recognition;
      this.finish(this.options.errorMessage(cause, "Speech recognition could not start."));
      abortRecognition(recognition);
    }
  }

  stop() {
    const recognition = this.recognition;
    if (this.settled || !recognition) return;
    this.clearCaptureTimers();
    this.options.onPhase("processing");
    try {
      recognition.stop();
      if (this.settled) return;
      this.finalizationTimer = window.setTimeout(() => {
        this.finish("Speech recognition did not finish in time. Retry or choose another route.");
        abortRecognition(recognition);
      }, this.options.finalizationTimeoutMS);
    } catch (cause) {
      this.finish(this.options.errorMessage(cause, "Speech recognition could not stop cleanly."));
      abortRecognition(recognition);
    }
  }

  abort() {
    if (this.settled) return;
    this.settled = true;
    this.clearTimers();
    const recognition = this.detachRecognition();
    abortRecognition(recognition);
    this.options.onSettled();
  }

  private finish(failure = "") {
    if (this.settled) return;
    this.settled = true;
    this.clearTimers();
    this.detachRecognition();
    this.options.onPhase("idle");
    if (failure) {
      this.options.onError(failure);
    } else {
      const transcript = this.transcript.trim();
      if (transcript) {
        this.options.onError("");
        this.options.onTranscript(transcript);
      } else {
        this.options.onError(
          "No speech was recognized. Try again or choose another dictation route.",
        );
      }
    }
    this.options.onSettled();
  }

  private detachRecognition(): BrowserSpeechRecognition | null {
    const recognition = this.recognition;
    this.recognition = null;
    if (recognition) {
      recognition.onresult = null;
      recognition.onerror = null;
      recognition.onend = null;
    }
    return recognition;
  }

  private clearCaptureTimers() {
    if (this.maxDurationTimer !== null) window.clearTimeout(this.maxDurationTimer);
    if (this.elapsedTimer !== null) window.clearInterval(this.elapsedTimer);
    this.maxDurationTimer = null;
    this.elapsedTimer = null;
  }

  private clearTimers() {
    this.clearCaptureTimers();
    if (this.finalizationTimer !== null) window.clearTimeout(this.finalizationTimer);
    this.finalizationTimer = null;
  }
}

function abortRecognition(recognition: BrowserSpeechRecognition | null) {
  try {
    recognition?.abort();
  } catch {
    // Engines may throw when abort races their terminal event.
  }
}

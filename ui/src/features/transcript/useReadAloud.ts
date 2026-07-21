import { useCallback, useEffect, useRef, useState } from "react";

import { markdownToSpeechChunks } from "../../lib/speech-text";

export type ReadAloudAvailability = "available" | "no_local_voice" | "unsupported";

export type ReadAloudController = {
  announcement: string;
  availability: ReadAloudAvailability;
  disabledReason?: string;
  readingContent: string | null;
  readingMessageID: string | null;
  stop: () => void;
  toggle: (messageID: string, content: string) => void;
};

export function useReadAloud(
  scopeKey: string,
  onVisibleError?: (message: string) => void,
): ReadAloudController {
  const [availability, setAvailability] = useState<ReadAloudAvailability>(initialAvailability);
  const [readingContent, setReadingContent] = useState<string | null>(null);
  const [readingMessageID, setReadingMessageID] = useState<string | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const readingMessageIDRef = useRef<string | null>(null);
  const utteranceRef = useRef<SpeechSynthesisUtterance | null>(null);
  const activeVoiceRef = useRef<SpeechSynthesisVoice | null>(null);
  const generationRef = useRef(0);
  const startAnnouncementVariantRef = useRef(false);
  const previousScopeKeyRef = useRef(scopeKey);

  const reportError = useCallback(
    (message: string) => {
      setAnnouncement(onVisibleError ? "" : message);
      onVisibleError?.(message);
    },
    [onVisibleError],
  );

  const cancel = useCallback((nextAnnouncement?: string) => {
    const wasReading = readingMessageIDRef.current !== null;
    generationRef.current += 1;
    utteranceRef.current = null;
    activeVoiceRef.current = null;
    readingMessageIDRef.current = null;
    setReadingContent(null);
    setReadingMessageID(null);
    try {
      browserSpeechEnvironment()?.synthesis.cancel();
    } catch {
      // State still settles when a webview removes its speech service mid-turn.
    }
    if (wasReading && nextAnnouncement) setAnnouncement(nextAnnouncement);
  }, []);

  const stop = useCallback(() => {
    cancel("Read aloud stopped.");
  }, [cancel]);

  const toggle = useCallback(
    (messageID: string, content: string) => {
      if (readingMessageIDRef.current === messageID) {
        cancel("Read aloud stopped.");
        return;
      }
      if (readingMessageIDRef.current) cancel();

      const environment = browserSpeechEnvironment();
      const voice = preferredLocalVoice(environment?.synthesis);
      const nextAvailability = availabilityFor(environment, voice);
      setAvailability(nextAvailability);
      if (!environment || !voice) {
        const message =
          availabilityReason(nextAvailability) ??
          "Read aloud is unavailable in this browser or app.";
        reportError(message);
        return;
      }

      const chunks = markdownToSpeechChunks(content);
      if (chunks.length === 0) {
        const message = "This response has no readable text.";
        reportError(message);
        return;
      }

      const generation = generationRef.current + 1;
      generationRef.current = generation;
      try {
        environment.synthesis.cancel();
      } catch {
        // A fresh utterance may still succeed even if an empty queue cannot be cancelled.
      }
      readingMessageIDRef.current = messageID;
      activeVoiceRef.current = voice;
      setReadingContent(content);
      setReadingMessageID(messageID);
      startAnnouncementVariantRef.current = !startAnnouncementVariantRef.current;
      setAnnouncement(
        startAnnouncementVariantRef.current
          ? "Reading response aloud."
          : "Reading selected response aloud.",
      );

      const settleFailure = (message: string) => {
        if (generationRef.current !== generation) return;
        generationRef.current += 1;
        utteranceRef.current = null;
        activeVoiceRef.current = null;
        readingMessageIDRef.current = null;
        setReadingContent(null);
        setReadingMessageID(null);
        reportError(message);
        try {
          environment.synthesis.cancel();
        } catch {
          // The failed host may also reject queue cleanup.
        }
      };

      const speakChunk = (index: number) => {
        if (generationRef.current !== generation) return;
        if (index >= chunks.length) {
          generationRef.current += 1;
          utteranceRef.current = null;
          activeVoiceRef.current = null;
          readingMessageIDRef.current = null;
          setReadingContent(null);
          setReadingMessageID(null);
          setAnnouncement("Finished reading response.");
          return;
        }

        const currentVoice = matchingLocalVoice(environment.synthesis, activeVoiceRef.current);
        if (!currentVoice) {
          setAvailability(
            preferredLocalVoice(environment.synthesis) ? "available" : "no_local_voice",
          );
          settleFailure(
            "Read aloud stopped because the selected local system voice became unavailable.",
          );
          return;
        }
        activeVoiceRef.current = currentVoice;
        let utterance: SpeechSynthesisUtterance;
        try {
          utterance = environment.createUtterance(chunks[index]);
        } catch {
          settleFailure("Read aloud stopped because the system voice failed.");
          return;
        }
        utterance.voice = currentVoice;
        utterance.lang = currentVoice.lang;
        utterance.onend = () => speakChunk(index + 1);
        utterance.onerror = () =>
          settleFailure("Read aloud stopped because the system voice failed.");
        utteranceRef.current = utterance;
        try {
          environment.synthesis.speak(utterance);
        } catch {
          settleFailure("Read aloud stopped because the system voice failed.");
        }
      };

      speakChunk(0);
    },
    [cancel, reportError],
  );

  useEffect(() => {
    const environment = browserSpeechEnvironment();
    if (!environment) {
      setAvailability("unsupported");
      return;
    }

    const refreshVoice = () => {
      const preferredVoice = preferredLocalVoice(environment.synthesis);
      setAvailability(preferredVoice ? "available" : "no_local_voice");
      const activeVoice = activeVoiceRef.current;
      const currentActiveVoice = matchingLocalVoice(environment.synthesis, activeVoice);
      if (activeVoice && readingMessageIDRef.current && !currentActiveVoice) {
        const message =
          "Read aloud stopped because the selected local system voice became unavailable.";
        cancel(message);
        reportError(message);
      } else if (currentActiveVoice) {
        activeVoiceRef.current = currentActiveVoice;
      }
    };

    refreshVoice();
    environment.synthesis.addEventListener("voiceschanged", refreshVoice);
    return () => environment.synthesis.removeEventListener("voiceschanged", refreshVoice);
  }, [cancel, reportError]);

  useEffect(() => {
    if (previousScopeKeyRef.current === scopeKey) return;
    previousScopeKeyRef.current = scopeKey;
    cancel("Read aloud stopped because the chat changed.");
  }, [cancel, scopeKey]);

  useEffect(
    () => () => {
      generationRef.current += 1;
      readingMessageIDRef.current = null;
      utteranceRef.current = null;
      activeVoiceRef.current = null;
      try {
        browserSpeechEnvironment()?.synthesis.cancel();
      } catch {
        // The host may already have torn down its speech service.
      }
    },
    [],
  );

  return {
    announcement,
    availability,
    disabledReason: availabilityReason(availability),
    readingContent,
    readingMessageID,
    stop,
    toggle,
  };
}

function browserSpeechEnvironment():
  | {
      synthesis: SpeechSynthesis;
      createUtterance: (text: string) => SpeechSynthesisUtterance;
    }
  | undefined {
  if (typeof window === "undefined") {
    return undefined;
  }
  try {
    const Utterance = globalThis.SpeechSynthesisUtterance;
    const synthesis = window.speechSynthesis;
    if (typeof Utterance !== "function" || !synthesis) return undefined;
    return {
      synthesis,
      createUtterance: (text) => new Utterance(text),
    };
  } catch {
    return undefined;
  }
}

function preferredLocalVoice(synthesis: SpeechSynthesis | undefined): SpeechSynthesisVoice | null {
  const voices = localVoices(synthesis);
  if (voices.length === 0) return null;

  const language = typeof navigator === "undefined" ? "" : navigator.language.toLowerCase();
  const baseLanguage = language.split("-")[0];
  return (
    voices.find((voice) => voice.default && voice.lang.toLowerCase() === language) ??
    voices.find((voice) => voice.lang.toLowerCase() === language) ??
    voices.find((voice) => voice.lang.toLowerCase().split("-")[0] === baseLanguage) ??
    voices.find((voice) => voice.default) ??
    voices[0]
  );
}

function matchingLocalVoice(
  synthesis: SpeechSynthesis,
  selected: SpeechSynthesisVoice | null,
): SpeechSynthesisVoice | null {
  if (!selected) return null;
  const voices = localVoices(synthesis);
  return (
    voices.find((voice) => voice === selected) ??
    voices.find(
      (voice) =>
        voice.voiceURI === selected.voiceURI &&
        voice.lang === selected.lang &&
        voice.name === selected.name,
    ) ??
    null
  );
}

function localVoices(synthesis: SpeechSynthesis | undefined): SpeechSynthesisVoice[] {
  if (!synthesis) return [];
  try {
    return synthesis.getVoices().filter((voice) => voice.localService === true);
  } catch {
    return [];
  }
}

function availabilityFor(
  environment: ReturnType<typeof browserSpeechEnvironment>,
  voice: SpeechSynthesisVoice | null,
): ReadAloudAvailability {
  if (!environment) return "unsupported";
  return voice ? "available" : "no_local_voice";
}

function initialAvailability(): ReadAloudAvailability {
  const environment = browserSpeechEnvironment();
  return availabilityFor(environment, preferredLocalVoice(environment?.synthesis));
}

function availabilityReason(availability: ReadAloudAvailability): string | undefined {
  if (availability === "unsupported") {
    return "Read aloud is unavailable in this browser or app.";
  }
  if (availability === "no_local_voice") {
    return "Install or enable a local system voice to use read aloud.";
  }
  return undefined;
}

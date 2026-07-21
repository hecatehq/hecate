import type { DictationProviderOption } from "../../types/dictation";

export const BROWSER_SPEECH_ROUTE_ID = "client:web-speech:browser-managed";
const PROVIDER_ROUTE_PREFIX = "provider:";

export type DictationRoute =
  | {
      id: typeof BROWSER_SPEECH_ROUTE_ID;
      kind: "browser_managed";
      label: string;
      disclosure: string;
      available: true;
    }
  | {
      id: string;
      kind: "provider";
      label: string;
      disclosure: string;
      available: boolean;
      provider: DictationProviderOption;
    };

export type BrowserSpeechRecognitionErrorCode =
  | "no-speech"
  | "aborted"
  | "audio-capture"
  | "network"
  | "not-allowed"
  | "service-not-allowed"
  | "language-not-supported"
  | "phrases-not-supported";

export type BrowserSpeechRecognitionResult = {
  isFinal: boolean;
  length: number;
  item?: (index: number) => { transcript: string } | null;
  [index: number]: { transcript: string };
};

export type BrowserSpeechRecognitionResultList = {
  length: number;
  item?: (index: number) => BrowserSpeechRecognitionResult | null;
  [index: number]: BrowserSpeechRecognitionResult;
};

export type BrowserSpeechRecognitionResultEvent = Event & {
  resultIndex: number;
  results: BrowserSpeechRecognitionResultList;
};

export type BrowserSpeechRecognitionErrorEvent = Event & {
  error: BrowserSpeechRecognitionErrorCode;
  message?: string;
};

export type BrowserSpeechRecognition = {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  maxAlternatives: number;
  onresult: ((event: BrowserSpeechRecognitionResultEvent) => void) | null;
  onerror: ((event: BrowserSpeechRecognitionErrorEvent) => void) | null;
  onend: (() => void) | null;
  start: () => void;
  stop: () => void;
  abort: () => void;
};

export type BrowserSpeechRecognitionConstructor = {
  new (): BrowserSpeechRecognition;
};

type SpeechRecognitionWindow = Window & {
  SpeechRecognition?: BrowserSpeechRecognitionConstructor;
  webkitSpeechRecognition?: BrowserSpeechRecognitionConstructor;
};

export function browserSpeechRecognitionConstructor():
  | BrowserSpeechRecognitionConstructor
  | undefined {
  const speechWindow = window as SpeechRecognitionWindow;
  return speechWindow.SpeechRecognition ?? speechWindow.webkitSpeechRecognition;
}

export function browserSpeechLocale(): string {
  return navigator.language?.trim() || document.documentElement.lang.trim() || "en-US";
}

export function buildDictationRoutes(
  providerOptions: DictationProviderOption[],
  browserSpeechAvailable: boolean,
): DictationRoute[] {
  const routes: DictationRoute[] = [];
  routes.push(
    ...providerOptions.map(
      (provider): DictationRoute => ({
        id: providerRouteID(provider.provider),
        kind: "provider",
        label: `${provider.provider} · ${provider.provider_kind === "local" ? "local" : "cloud"}${provider.available ? "" : " · unavailable"}`,
        disclosure: `Audio goes only to ${provider.provider}; Hecate does not retain it.`,
        available: provider.available,
        provider,
      }),
    ),
  );
  if (browserSpeechAvailable) {
    routes.push({
      id: BROWSER_SPEECH_ROUTE_ID,
      kind: "browser_managed",
      label: "Browser speech service · may use cloud",
      disclosure:
        "Speech recognition is controlled by the browser and may use its vendor's cloud service; Hecate does not retain it.",
      available: true,
    });
  }
  return routes;
}

export function resolveSelectedDictationRoute(
  storedRoute: string,
  routes: DictationRoute[],
): string {
  const normalized = normalizeStoredDictationRoute(storedRoute, routes);
  // A saved route is an explicit disclosure/locality choice. Preserve it even
  // when it is temporarily unavailable so Hecate never crosses that boundary
  // by silently selecting another route. Defaults apply only to new users.
  if (storedRoute.trim()) return normalized;
  return routes.find((route) => route.kind === "provider" && route.available)?.id ?? "";
}

export function providerRouteID(provider: string): string {
  return `${PROVIDER_ROUTE_PREFIX}${provider}`;
}

export function finalSpeechTranscript(event: BrowserSpeechRecognitionResultEvent): string {
  const fragments: string[] = [];
  for (let index = 0; index < event.results.length; index += 1) {
    const result = event.results[index] ?? event.results.item?.(index);
    if (!result?.isFinal) continue;
    const alternative = result[0] ?? result.item?.(0);
    const transcript = alternative?.transcript.trim();
    if (transcript) fragments.push(transcript);
  }
  return fragments.join(" ");
}

function normalizeStoredDictationRoute(storedRoute: string, routes: DictationRoute[]): string {
  if (storedRoute === BROWSER_SPEECH_ROUTE_ID || storedRoute.startsWith(PROVIDER_ROUTE_PREFIX)) {
    return storedRoute;
  }
  const legacyProvider = routes.find(
    (route) => route.kind === "provider" && route.provider.provider === storedRoute,
  );
  return legacyProvider?.id ?? storedRoute;
}

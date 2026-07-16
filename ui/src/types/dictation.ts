export type DictationProviderOption = {
  provider: string;
  provider_kind: "local" | "cloud" | string;
  default_model: string;
  available: boolean;
  unavailable_reason?: string;
};

export type DictationOptionsResponse = {
  object: "dictation_options";
  data: DictationProviderOption[];
};

export type DictationTranscriptionRecord = {
  provider: string;
  provider_kind: "local" | "cloud" | string;
  model: string;
  text: string;
};

export type DictationTranscriptionResponse = {
  object: "dictation_transcription";
  data: DictationTranscriptionRecord;
};

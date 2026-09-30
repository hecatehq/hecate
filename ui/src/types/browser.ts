import type { BrowserEvidenceRuntimeReadiness } from "./provider";

export type BrowserCandidate = {
  id: string;
  name: string;
  path: string;
};

export type BrowserSettingsData = {
  readiness: BrowserEvidenceRuntimeReadiness;
  source: "none" | "settings" | "environment";
  selected?: BrowserCandidate;
  candidates: BrowserCandidate[];
  backend: string;
};

export type BrowserSettingsResponse = {
  object: "browser_settings";
  data: BrowserSettingsData;
};

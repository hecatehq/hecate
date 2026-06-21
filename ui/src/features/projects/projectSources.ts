import type { ProjectContextSourcePayload, ProjectContextSourceRecord } from "../../types/project";
import { isLinkableProjectLocator } from "./projectUtils";

export type ProjectSourceForm = {
  kind: string;
  title: string;
  locator: string;
  enabled: boolean;
  format: string;
  scope: string;
  trustLabel: string;
  sourceCategory: string;
  note: string;
};

export const PROJECT_SOURCE_KINDS = ["url", "doc", "note", "external_ref"] as const;

export function projectSourceFormFromRecord(
  source: ProjectContextSourceRecord | null,
): ProjectSourceForm {
  return {
    kind: source?.kind || "url",
    title: source?.title ?? "",
    locator: source?.path ?? "",
    enabled: source?.enabled ?? true,
    format: source?.format ?? projectSourceDefaultFormat(source?.kind || "url"),
    scope: source?.scope ?? "",
    trustLabel: source?.trust_label ?? "operator_source",
    sourceCategory: source?.source_category ?? "operator_source",
    note: source?.metadata?.note ?? "",
  };
}

export function projectSourcePayloadFromForm(
  form: ProjectSourceForm,
  existing: ProjectContextSourceRecord | null = null,
): ProjectContextSourcePayload {
  const kind = form.kind.trim() || "url";
  const title = form.title.trim();
  const locator = form.locator.trim() || derivedSourceLocator(kind, title);
  const metadata = { ...existing?.metadata };
  const note = form.note.trim();
  if (note) {
    metadata.note = note;
  } else {
    delete metadata.note;
  }
  const payload: ProjectContextSourcePayload = {
    kind,
    path: locator,
    enabled: form.enabled,
    format: form.format.trim() || projectSourceDefaultFormat(kind),
    trust_label: form.trustLabel.trim() || "operator_source",
    source_category: form.sourceCategory.trim() || "operator_source",
  };
  if (existing?.id) payload.id = existing.id;
  if (title) payload.title = title;
  const scope = form.scope.trim();
  if (scope) payload.scope = scope;
  if (Object.keys(metadata).length > 0) {
    payload.metadata = metadata;
  }
  return payload;
}

export function sourceKindLabel(kind: string): string {
  switch (kind) {
    case "url":
      return "URL";
    case "doc":
      return "Local file/path";
    case "note":
      return "Note";
    case "external_ref":
      return "External reference";
    default:
      return kind || "Source";
  }
}

export const isLinkableSourceLocator = isLinkableProjectLocator;

export function projectSourceDefaultFormat(kind: string): string {
  switch (kind) {
    case "url":
      return "url";
    case "note":
      return "text";
    case "external_ref":
      return "reference";
    default:
      return "";
  }
}

function derivedSourceLocator(kind: string, title: string): string {
  if (kind !== "note") return "";
  const slug = title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return `note:${slug || "source"}`;
}

import { describe, expect, it } from "vitest";

import type { ProjectContextSourceRecord } from "../../types/project";
import { isLinkableSourceLocator, projectSourcePayloadFromForm } from "./projectSources";

const source: ProjectContextSourceRecord = {
  id: "ctx_1",
  kind: "workspace_instruction",
  title: "AGENTS.md",
  path: "AGENTS.md",
  enabled: true,
  format: "agents_md",
  scope: "workspace",
  trust_label: "workspace_guidance",
  source_category: "workspace_guidance",
  metadata: { root_id: "root_1", host: "portable" },
  created_at: "2026-06-14T00:00:00Z",
  updated_at: "2026-06-14T00:00:00Z",
};

describe("projectSources", () => {
  it("builds note source payloads with derived locators", () => {
    expect(
      projectSourcePayloadFromForm({
        kind: "note",
        title: " Research Goals ",
        locator: "",
        enabled: true,
        format: "",
        scope: " project ",
        trustLabel: "",
        sourceCategory: "",
        note: " Primary operator note. ",
      }),
    ).toEqual({
      kind: "note",
      title: "Research Goals",
      path: "note:research-goals",
      enabled: true,
      format: "text",
      scope: "project",
      trust_label: "operator_source",
      source_category: "operator_source",
      metadata: { note: "Primary operator note." },
    });
  });

  it("preserves existing metadata when editing a source", () => {
    expect(
      projectSourcePayloadFromForm(
        {
          kind: "workspace_instruction",
          title: "Portable guidance",
          locator: "AGENTS.md",
          enabled: false,
          format: "agents_md",
          scope: "workspace",
          trustLabel: "workspace_guidance",
          sourceCategory: "workspace_guidance",
          note: "Reviewed by operator.",
        },
        source,
      ),
    ).toEqual({
      id: "ctx_1",
      kind: "workspace_instruction",
      title: "Portable guidance",
      path: "AGENTS.md",
      enabled: false,
      format: "agents_md",
      scope: "workspace",
      trust_label: "workspace_guidance",
      source_category: "workspace_guidance",
      metadata: { root_id: "root_1", host: "portable", note: "Reviewed by operator." },
    });
  });

  it("only treats http and https locators as linkable", () => {
    expect(isLinkableSourceLocator("https://example.invalid/source")).toBe(true);
    expect(isLinkableSourceLocator("http://example.invalid/source")).toBe(true);
    expect(isLinkableSourceLocator("javascript:alert(1)")).toBe(false);
    expect(isLinkableSourceLocator("data:text/html,boom")).toBe(false);
  });
});

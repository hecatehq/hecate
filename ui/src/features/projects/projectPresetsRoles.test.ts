import { describe, expect, it } from "vitest";

import {
  browserAllowedOriginsValidationError,
  emptyAgentPresetForm,
  presetFormFromRecord,
  presetUpdatePayloadFromForm,
} from "./projectPresetsRoles";

describe("browserAllowedOriginsValidationError", () => {
  it.each([
    "",
    "https://app.example.test/reports",
    "https://app.example.test/?token=secret",
    "https://app.example.test/#section",
    "https://operator:secret@app.example.test",
    "https://*.example.test",
    "file:///tmp/evidence.html",
  ])("rejects %j", (value) => {
    expect(browserAllowedOriginsValidationError(value)).toBeTruthy();
  });

  it("accepts exact http(s) origins, including a copied trailing slash", () => {
    expect(
      browserAllowedOriginsValidationError(
        "https://app.example.test/\nhttp://status.example.test:8080",
      ),
    ).toBeNull();
  });
});

describe("browser capability preset mapping", () => {
  it("keeps origins while either independent browser grant is enabled", () => {
    const form = emptyAgentPresetForm();
    form.browserInteractionsAllowed = true;
    form.browserAllowedOrigins =
      "https://app.example.test\nhttps://app.example.test\nhttp://status.example.test:8080";

    expect(presetUpdatePayloadFromForm(form)).toMatchObject({
      browser_allowed: false,
      browser_interactions_allowed: true,
      browser_allowed_origins: ["https://app.example.test", "http://status.example.test:8080"],
    });

    form.browserInteractionsAllowed = false;
    expect(presetUpdatePayloadFromForm(form)).toMatchObject({
      browser_allowed: false,
      browser_interactions_allowed: false,
      browser_allowed_origins: [],
    });
  });

  it("maps the explicit interaction grant from the current response", () => {
    const form = presetFormFromRecord({
      id: "review",
      name: "Review",
      surface: "hecate_task",
      tools_enabled: true,
      writes_allowed: false,
      network_allowed: false,
      browser_allowed: true,
      browser_interactions_allowed: false,
      browser_allowed_origins: ["https://app.example.test"],
      approval_policy: "inherit",
      project_memory_policy: "inherit",
      context_source_policy: "inherit",
    });

    expect(form.browserAllowed).toBe(true);
    expect(form.browserInteractionsAllowed).toBe(false);
    expect(form.browserAllowedOrigins).toBe("https://app.example.test");
  });

  it("preserves independent browser grants for a tools-on Hecate Chat policy", () => {
    const form = emptyAgentPresetForm();
    form.surface = "hecate_chat";
    form.browserAllowed = true;
    form.browserInteractionsAllowed = true;
    form.browserAllowedOrigins = "https://app.example.test";

    expect(presetUpdatePayloadFromForm(form)).toMatchObject({
      surface: "hecate_chat",
      tools_enabled: true,
      browser_allowed: true,
      browser_interactions_allowed: true,
      browser_allowed_origins: ["https://app.example.test"],
    });
  });

  it.each([
    ["external_agent", true],
    ["hecate_chat", false],
  ])("clears browser grants for surface %s with tools=%s", (surface, toolsEnabled) => {
    const form = emptyAgentPresetForm();
    form.surface = surface;
    form.toolsEnabled = toolsEnabled;
    form.browserAllowed = true;
    form.browserInteractionsAllowed = true;
    form.browserAllowedOrigins = "https://app.example.test";

    expect(presetUpdatePayloadFromForm(form)).toMatchObject({
      browser_allowed: false,
      browser_interactions_allowed: false,
      browser_allowed_origins: [],
    });
  });

  it("does not restore stale browser grants from an External Agent policy", () => {
    const form = presetFormFromRecord({
      id: "external-review",
      name: "External review",
      surface: "external_agent",
      tools_enabled: true,
      writes_allowed: false,
      network_allowed: false,
      browser_allowed: true,
      browser_interactions_allowed: true,
      browser_allowed_origins: ["https://app.example.test"],
      approval_policy: "inherit",
      project_memory_policy: "inherit",
      context_source_policy: "inherit",
    });

    expect(form.browserAllowed).toBe(false);
    expect(form.browserInteractionsAllowed).toBe(false);
    expect(form.browserAllowedOrigins).toBe("");
  });
});

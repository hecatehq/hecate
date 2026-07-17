import { describe, expect, it } from "vitest";

import { browserAllowedOriginsValidationError } from "./projectPresetsRoles";

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

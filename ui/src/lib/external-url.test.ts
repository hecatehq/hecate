import { describe, expect, it } from "vitest";

import { safeExternalURL } from "./external-url";

describe("safeExternalURL", () => {
  it.each([
    "http://example.com/releases",
    "https://example.com/releases",
    "mailto:help@example.com",
  ])("allows an external application URL: %s", (value) => {
    expect(safeExternalURL(value)).toBe(value);
  });

  it.each([
    ["HTTPS://Example.com/releases", "https://example.com/releases"],
    ["Http:example.com/releases", "http://example.com/releases"],
  ])("normalizes an allowed URL for the native scope: %s", (value, expected) => {
    expect(safeExternalURL(value)).toBe(expected);
  });

  it.each(["javascript:alert(1)", "file:///tmp/private", "/relative", "not a URL"])(
    "rejects a non-external or unsafe URL: %s",
    (value) => {
      expect(safeExternalURL(value)).toBeNull();
    },
  );
});

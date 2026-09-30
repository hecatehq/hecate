import { afterEach, describe, expect, it, vi } from "vitest";
import { disableBrowser, enableBrowser, getBrowserSettings } from "./api";
import { browserReadinessLabel } from "./browser-readiness";

afterEach(() => vi.unstubAllGlobals());

describe("browser setup API", () => {
  it("uses the typed response envelope, opaque candidate id, and abort signal", async () => {
    const response = {
      object: "browser_settings",
      data: {
        readiness: { available: false, status: "not_configured", message: "Not configured." },
        source: "none",
        candidates: [],
        backend: "memory",
      },
    };
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(
      async () =>
        new Response(JSON.stringify(response), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const { signal } = new AbortController();
    expect(await getBrowserSettings(signal)).toEqual(response);
    expect(await enableBrowser("opaque-candidate", signal)).toEqual(response);
    expect(await disableBrowser(signal)).toEqual(response);
    for (const [url, options] of fetchMock.mock.calls) {
      expect(url).toBe("/hecate/v1/settings/browser");
      expect(options?.signal).toBe(signal);
    }
    expect(fetchMock.mock.calls[1][1]?.method).toBe("PUT");
    expect(JSON.parse(fetchMock.mock.calls[1][1]?.body as string)).toEqual({
      candidate_id: "opaque-candidate",
    });
    expect(fetchMock.mock.calls[2][1]?.method).toBe("DELETE");
  });

  it.each([
    [true, "configured", "Configured (unverified)"],
    [true, "working", "Working"],
    [true, "ready", "Configured (unverified)"],
    [false, "not_configured", "Not configured"],
    [false, "unavailable", "Unavailable"],
    [false, "local_only", "Local only"],
    [false, "working", "Unavailable"],
  ])(
    "labels available=%s, status=%s without implying an unperformed check",
    (available, status, expected) => {
      expect(browserReadinessLabel({ available, status, message: "" })).toBe(expected);
    },
  );
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { disableBrowser, enableBrowser, getBrowserSettings, getTaskRunArtifact } from "./api";
import { browserReadinessLabel } from "./browser-readiness";

afterEach(() => vi.unstubAllGlobals());

describe("browser setup API", () => {
  it("loads retained reports through the scoped artifact route with cancellation", async () => {
    const response = { object: "task_artifact", data: { id: "report/1" } };
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const { signal } = new AbortController();
    expect(await getTaskRunArtifact("task/a", "run/b", "report/1", signal)).toEqual(response);
    expect(fetchMock.mock.calls[0][0]).toBe(
      "/hecate/v1/tasks/task%2Fa/runs/run%2Fb/artifacts/report%2F1",
    );
    expect(fetchMock.mock.calls[0][1]?.signal).toBe(signal);
    expect(fetchMock.mock.calls[0][1]?.method ?? "GET").toBe("GET");
  });
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

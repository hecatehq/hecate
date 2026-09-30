import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { SettingsProvider, useSettings } from "./settings";

describe("browser readiness in settings", () => {
  it("preserves a later browser update when an older dashboard publishes either wave", () => {
    const { result } = renderHook(() => useSettings(), { wrapper: SettingsProvider });
    const before = result.current.actions.captureBrowserReadinessRevision();
    const working = { available: true, status: "working", message: "Approved use succeeded." };
    const stale = {
      backend: "sqlite",
      providers: [],
      policy_rules: [],
      events: [],
      browser_evidence: {
        available: false,
        status: "not_configured",
        message: "No browser selected.",
      },
    };
    act(() => {
      result.current.actions.setBrowserReadiness(working);
      result.current.actions.updateConfig((current) =>
        result.current.actions.mergeConfigFromRead(stale, before, current),
      );
    });
    expect(result.current.state.config?.browser_evidence).toEqual(working);
    expect(result.current.state.config?.backend).toBe("sqlite");
    act(() =>
      result.current.actions.updateConfig((current) =>
        result.current.actions.mergeConfigFromRead(stale, before, current),
      ),
    );
    expect(result.current.state.config?.browser_evidence).toEqual(working);
  });

  it("accepts browser evidence from a dashboard started after the setup update", () => {
    const { result } = renderHook(() => useSettings(), { wrapper: SettingsProvider });
    act(() =>
      result.current.actions.setBrowserReadiness({
        available: true,
        status: "configured",
        message: "Configured.",
      }),
    );
    const revision = result.current.actions.captureBrowserReadinessRevision();
    const fresh = {
      backend: "memory",
      providers: [],
      policy_rules: [],
      events: [],
      browser_evidence: { available: true, status: "working", message: "Approved use succeeded." },
    };
    act(() =>
      result.current.actions.updateConfig((current) =>
        result.current.actions.mergeConfigFromRead(fresh, revision, current),
      ),
    );
    expect(result.current.state.config).toEqual(fresh);
    act(() =>
      result.current.actions.updateConfig((current) =>
        result.current.actions.mergeConfigFromRead(
          { ...fresh, browser_evidence: undefined },
          revision - 1,
          current,
        ),
      ),
    );
    expect(result.current.state.config?.browser_evidence).toEqual(fresh.browser_evidence);
  });
});

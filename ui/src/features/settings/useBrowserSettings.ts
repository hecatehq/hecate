import { useCallback, useEffect, useRef, useState } from "react";

import { useSettings } from "../../app/state/settings";
import { ApiError, disableBrowser, enableBrowser, getBrowserSettings } from "../../lib/api";
import type { BrowserSettingsData, BrowserSettingsResponse } from "../../types/browser";

export function useBrowserSettings(remoteRuntime: boolean) {
  const { setBrowserReadiness } = useSettings().actions;
  const [data, setData] = useState<BrowserSettingsData | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [needsRefresh, setNeedsRefresh] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const request = useRef<AbortController | null>(null);
  const mounted = useRef(false);
  const remote = useRef(remoteRuntime);
  remote.current = remoteRuntime;

  const run = useCallback(
    async (
      operation: (signal: AbortSignal) => Promise<BrowserSettingsResponse>,
      mutation: boolean,
    ) => {
      if (!mounted.current || remote.current || request.current) return;
      const controller = new AbortController();
      request.current = controller;
      setPending(true);
      setError("");
      setAnnouncement("");
      try {
        const response = await operation(controller.signal);
        // Abort is advisory: a late response must not update another runtime or mount.
        if (!mounted.current || remote.current || request.current !== controller) return;
        setData(response.data);
        setNeedsRefresh(false);
        setBrowserReadiness(response.data.readiness);
        if (mutation) setAnnouncement("Browser settings saved.");
      } catch (failure) {
        if (!mounted.current || remote.current || request.current !== controller) return;
        setNeedsRefresh(true);
        // A disconnected write may have committed. Do not claim it failed or retry it.
        setError(
          failure instanceof ApiError && failure.status === 403
            ? "Browser setup requires a direct local connection. Open Hecate on the runtime's machine without a reverse proxy."
            : mutation
              ? "Could not confirm the browser setting. Refresh discovery to see the current setting before making another change."
              : "Could not load browser setup. Refresh discovery to try again.",
        );
      } finally {
        if (request.current === controller) {
          request.current = null;
          if (mounted.current) setPending(false);
        }
      }
    },
    [setBrowserReadiness],
  );

  const refresh = useCallback(() => run(getBrowserSettings, false), [run]);
  const enable = useCallback(
    (candidateID: string) => run((signal) => enableBrowser(candidateID, signal), true),
    [run],
  );
  const disable = useCallback(() => run(disableBrowser, true), [run]);

  useEffect(() => {
    mounted.current = true;
    setData(null);
    setError("");
    setPending(false);
    setNeedsRefresh(false);
    setAnnouncement("");
    if (!remoteRuntime) void refresh();
    return () => {
      mounted.current = false;
      request.current?.abort();
      request.current = null;
    };
  }, [remoteRuntime, refresh]);

  return { data, pending, error, needsRefresh, announcement, refresh, enable, disable };
}

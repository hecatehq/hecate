import { useEffect, useRef } from "react";

export type ProjectPassiveRefreshRequest = {
  includeCatalog: boolean;
  reason: "foreground" | "interval";
};

type ProjectPassiveRefresh = (request: ProjectPassiveRefreshRequest) => Promise<void> | void;

type ProjectPassiveRefreshOptions = {
  catalogIntervalMs?: number;
  operationalIntervalMs?: number;
};

const DEFAULT_CATALOG_INTERVAL_MS = 30_000;
const DEFAULT_OPERATIONAL_INTERVAL_MS = 10_000;

/**
 * Revalidates Projects while the cockpit is visible and after the operator
 * returns to it. Request waves never overlap; a foreground return during an
 * active wave queues one merged follow-up instead.
 */
export function useProjectPassiveRefresh(
  refresh: ProjectPassiveRefresh,
  {
    catalogIntervalMs = DEFAULT_CATALOG_INTERVAL_MS,
    operationalIntervalMs = DEFAULT_OPERATIONAL_INTERVAL_MS,
  }: ProjectPassiveRefreshOptions = {},
) {
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;

  useEffect(() => {
    let inactive = false;
    let refreshing = false;
    let queuedRequest: ProjectPassiveRefreshRequest | null = null;
    let disposed = false;
    let lastCatalogRefreshAt = Date.now();

    const queueRequest = (request: ProjectPassiveRefreshRequest) => {
      if (refreshing) {
        queuedRequest = {
          includeCatalog: Boolean(queuedRequest?.includeCatalog || request.includeCatalog),
          reason:
            queuedRequest?.reason === "foreground" || request.reason === "foreground"
              ? "foreground"
              : "interval",
        };
        return;
      }
      refreshing = true;
      void Promise.resolve()
        .then(() => refreshRef.current(request))
        .catch(() => undefined)
        .finally(() => {
          refreshing = false;
          if (disposed || !queuedRequest) return;
          const nextRequest = queuedRequest;
          queuedRequest = null;
          if (document.visibilityState === "hidden" || inactive) return;
          queueRequest(nextRequest);
        });
    };

    const markInactive = () => {
      inactive = true;
    };
    const refreshAfterReturn = () => {
      if (!inactive || document.visibilityState === "hidden") return;
      inactive = false;
      lastCatalogRefreshAt = Date.now();
      queueRequest({ includeCatalog: true, reason: "foreground" });
    };
    const handleVisibilityChange = () => {
      if (document.visibilityState === "hidden") {
        markInactive();
        return;
      }
      refreshAfterReturn();
    };
    const intervalID = window.setInterval(() => {
      if (inactive || document.visibilityState === "hidden") return;
      const now = Date.now();
      const includeCatalog = now - lastCatalogRefreshAt >= catalogIntervalMs;
      if (includeCatalog) lastCatalogRefreshAt = now;
      queueRequest({ includeCatalog, reason: "interval" });
    }, operationalIntervalMs);

    window.addEventListener("blur", markInactive);
    window.addEventListener("focus", refreshAfterReturn);
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      disposed = true;
      window.clearInterval(intervalID);
      window.removeEventListener("blur", markInactive);
      window.removeEventListener("focus", refreshAfterReturn);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [catalogIntervalMs, operationalIntervalMs]);
}

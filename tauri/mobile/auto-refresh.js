export function shouldAutoRefresh(status, documentHidden) {
  return status?.signed_in === true && documentHidden !== true;
}

export function cloudRetryView(status) {
  const value = status?.retry_after_seconds;
  const seconds = Number.isFinite(value) && value > 0 ? Math.min(86_400, Math.ceil(value)) : 0;
  return {
    retryAfterSeconds: seconds,
    message: seconds > 0
      ? `${status?.message || "Hecate Cloud is temporarily unavailable."} Retrying in ${seconds}s.`
      : "",
  };
}

export async function refreshMobileCloudData({
  refreshStatus,
  shouldRefreshConnections,
  loadConnections,
  refreshNotificationStatus,
}) {
  await refreshStatus();
  if (shouldRefreshConnections()) await loadConnections();
  // Optional push cleanup must not renew a shared cooldown before the
  // primary connection list has had its opportunity to recover.
  await refreshNotificationStatus();
}

export function createAutoRefreshLoop({
  refresh,
  intervalMs,
  setTimeoutFn = globalThis.setTimeout,
  clearTimeoutFn = globalThis.clearTimeout,
  nowFn = () => performance.now(),
}) {
  if (typeof refresh !== "function") throw new TypeError("refresh must be a function");
  if (!Number.isFinite(intervalMs) || intervalMs <= 0) {
    throw new TypeError("intervalMs must be a positive number");
  }

  let enabled = false;
  let running = false;
  let timer = null;
  let retryNotBefore = 0;

  function clearScheduledRefresh() {
    if (timer === null) return;
    clearTimeoutFn(timer);
    timer = null;
  }

  function schedule() {
    if (!enabled || running || timer !== null) return;
    timer = setTimeoutFn(() => {
      timer = null;
      void run().catch(() => {});
    }, Math.max(intervalMs, retryNotBefore - nowFn()));
  }

  async function run() {
    if (!enabled || running) return false;
    clearScheduledRefresh();
    if (nowFn() < retryNotBefore) {
      schedule();
      return false;
    }
    running = true;
    try {
      await refresh();
      return true;
    } finally {
      running = false;
      schedule();
    }
  }

  function setEnabled(nextEnabled, { immediate = false } = {}) {
    enabled = Boolean(nextEnabled);
    if (!enabled) {
      clearScheduledRefresh();
      return;
    }
    if (immediate) {
      clearScheduledRefresh();
      void run().catch(() => {});
      return;
    }
    schedule();
  }

  return {
    refreshNow: run,
    setEnabled,
    setRetryAfterSeconds(seconds) {
      const delay = cloudRetryView({ retry_after_seconds: seconds }).retryAfterSeconds;
      retryNotBefore = delay > 0 ? nowFn() + delay * 1_000 : 0;
      clearScheduledRefresh();
      schedule();
    },
  };
}

export function shouldAutoRefresh(status, documentHidden) {
  return status?.signed_in === true && documentHidden !== true;
}

export function createAutoRefreshLoop({
  refresh,
  intervalMs,
  setTimeoutFn = globalThis.setTimeout,
  clearTimeoutFn = globalThis.clearTimeout,
}) {
  if (typeof refresh !== "function") throw new TypeError("refresh must be a function");
  if (!Number.isFinite(intervalMs) || intervalMs <= 0) {
    throw new TypeError("intervalMs must be a positive number");
  }

  let enabled = false;
  let running = false;
  let timer = null;

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
    }, intervalMs);
  }

  async function run() {
    if (!enabled || running) return false;
    clearScheduledRefresh();
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
  };
}

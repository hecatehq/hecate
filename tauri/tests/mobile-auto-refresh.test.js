import { describe, expect, it } from "bun:test";
import { createAutoRefreshLoop, shouldAutoRefresh } from "../mobile/auto-refresh.js";

function createTimerHarness() {
  let nextId = 1;
  const timers = new Map();

  return {
    clearTimeoutFn(id) {
      timers.delete(id);
    },
    runNext() {
      const entry = timers.entries().next().value;
      if (!entry) throw new Error("No refresh is scheduled");
      const [id, { callback }] = entry;
      timers.delete(id);
      callback();
    },
    setTimeoutFn(callback, delay) {
      const id = nextId++;
      timers.set(id, { callback, delay });
      return id;
    },
    get scheduled() {
      return [...timers.values()];
    },
  };
}

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

describe("mobile auto refresh loop", () => {
  it("runs only for a signed-in session on a visible page", () => {
    expect(shouldAutoRefresh({ signed_in: true }, false)).toBe(true);
    expect(shouldAutoRefresh({ signed_in: false }, false)).toBe(false);
    expect(shouldAutoRefresh({ signed_in: true }, true)).toBe(false);
    expect(shouldAutoRefresh(null, false)).toBe(false);
  });

  it("refreshes on the interval and schedules the next run after completion", async () => {
    const timers = createTimerHarness();
    let refreshCount = 0;
    const loop = createAutoRefreshLoop({
      refresh: async () => {
        refreshCount += 1;
      },
      intervalMs: 10_000,
      ...timers,
    });

    loop.setEnabled(true);
    expect(timers.scheduled).toEqual([{ callback: expect.any(Function), delay: 10_000 }]);

    timers.runNext();
    await Promise.resolve();
    expect(refreshCount).toBe(1);
    expect(timers.scheduled).toHaveLength(1);
  });

  it("does not overlap an active refresh", async () => {
    const timers = createTimerHarness();
    const firstRefresh = deferred();
    let refreshCount = 0;
    const loop = createAutoRefreshLoop({
      refresh: () => {
        refreshCount += 1;
        return firstRefresh.promise;
      },
      intervalMs: 10_000,
      ...timers,
    });

    loop.setEnabled(true);
    timers.runNext();
    await Promise.resolve();

    expect(refreshCount).toBe(1);
    expect(timers.scheduled).toHaveLength(0);
    await expect(loop.refreshNow()).resolves.toBe(false);
    expect(refreshCount).toBe(1);

    firstRefresh.resolve();
    await Promise.resolve();
    await Promise.resolve();
    expect(timers.scheduled).toHaveLength(1);
  });

  it("cancels scheduled work when disabled", () => {
    const timers = createTimerHarness();
    const loop = createAutoRefreshLoop({
      refresh: () => {},
      intervalMs: 10_000,
      ...timers,
    });

    loop.setEnabled(true);
    expect(timers.scheduled).toHaveLength(1);
    loop.setEnabled(false);
    expect(timers.scheduled).toHaveLength(0);
  });

  it("does not reschedule when disabled during an active refresh", async () => {
    const timers = createTimerHarness();
    const activeRefresh = deferred();
    const loop = createAutoRefreshLoop({
      refresh: () => activeRefresh.promise,
      intervalMs: 10_000,
      ...timers,
    });

    loop.setEnabled(true);
    timers.runNext();
    await Promise.resolve();
    loop.setEnabled(false);
    activeRefresh.resolve();
    await Promise.resolve();
    await Promise.resolve();

    expect(timers.scheduled).toHaveLength(0);
  });

  it("can refresh immediately when it becomes enabled", async () => {
    const timers = createTimerHarness();
    let refreshCount = 0;
    const loop = createAutoRefreshLoop({
      refresh: async () => {
        refreshCount += 1;
      },
      intervalMs: 10_000,
      ...timers,
    });

    loop.setEnabled(true, { immediate: true });
    await Promise.resolve();

    expect(refreshCount).toBe(1);
    expect(timers.scheduled).toHaveLength(1);
  });
});

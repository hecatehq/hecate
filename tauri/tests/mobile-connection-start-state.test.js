import { describe, expect, it } from "bun:test";

import { createConnectionStartState } from "../mobile/connection-start-state.js";

describe("mobile hosted-runtime start state", () => {
  it("keeps a stale startable row pending until Cloud catches up", () => {
    let now = 1_000;
    const state = createConnectionStartState({ now: () => now, acceptedTtlMs: 120_000 });
    const stale = {
      id: "runtime_1",
      status: "offline",
      reachable: false,
      can_start: true,
    };

    state.begin(stale.id);
    expect(state.reconcile(stale)).toBe(true);
    now += 119_999;
    expect(state.reconcile(stale)).toBe(true);
    now += 1;
    expect(state.reconcile(stale)).toBe(false);
  });

  it("clears pending state on an authoritative runtime transition", () => {
    const state = createConnectionStartState();
    state.begin("runtime_1");

    expect(
      state.reconcile({
        id: "runtime_1",
        status: "starting",
        reachable: false,
        can_start: false,
      }),
    ).toBe(false);

    state.begin("runtime_2");
    expect(
      state.reconcile({
        id: "runtime_2",
        status: "online",
        reachable: true,
        can_start: false,
      }),
    ).toBe(false);
  });

  it("bounds an ambiguous failed request before allowing retry", () => {
    let now = 5_000;
    const state = createConnectionStartState({ now: () => now, ambiguousTtlMs: 15_000 });
    const stale = {
      id: "runtime_1",
      status: "offline",
      reachable: false,
      can_start: true,
    };

    state.ambiguous(stale.id);
    expect(state.reconcile(stale)).toBe(true);
    now += 15_000;
    expect(state.reconcile(stale)).toBe(false);
  });

  it("drops pending starts when the mobile session changes", () => {
    const state = createConnectionStartState();
    const stale = {
      id: "runtime_1",
      status: "offline",
      reachable: false,
      can_start: true,
    };
    state.begin(stale.id);
    state.reset();
    expect(state.reconcile(stale)).toBe(false);
  });
});

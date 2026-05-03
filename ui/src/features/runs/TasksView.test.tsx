import { describe, expect, it } from "vitest";

import { streamTurnCostKey } from "./TasksView";

describe("streamTurnCostKey", () => {
  it("normalizes zero-based backend turn indexes to one-based UI turn numbers", () => {
    expect(streamTurnCostKey(0)).toBe(1);
    expect(streamTurnCostKey(1)).toBe(2);
  });

  it("rejects invalid turn indexes", () => {
    expect(streamTurnCostKey(undefined)).toBeNull();
    expect(streamTurnCostKey(-1)).toBeNull();
    expect(streamTurnCostKey(Number.NaN)).toBeNull();
  });
});

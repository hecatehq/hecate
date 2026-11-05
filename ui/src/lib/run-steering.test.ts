import { beforeEach, describe, expect, it, vi } from "vitest";
import { reserveRunSteeringRequest, settleRunSteeringRequest } from "./run-steering";

describe("live guidance delivery identity", () => {
  beforeEach(() => sessionStorage.clear());
  it("retains same-tab retry identity without persisting raw guidance", () => {
    const first = reserveRunSteeringRequest(sessionStorage, "chat", "task", "run", "secret text");
    expect(reserveRunSteeringRequest(sessionStorage, "chat", "task", "run", "secret text")).toEqual(
      first,
    );
    expect(first.key).not.toContain("secret text");
    expect(sessionStorage.getItem(first.key)).toBe(first.id);
    expect(
      reserveRunSteeringRequest(sessionStorage, "chat", "task", "other", "secret text").id,
    ).not.toBe(first.id);
    settleRunSteeringRequest(sessionStorage, first);
    expect(
      reserveRunSteeringRequest(sessionStorage, "chat", "task", "run", "secret text").id,
    ).not.toBe(first.id);
  });
  it("fails closed if delivery identity cannot be persisted", () => {
    const storage = {
      getItem: vi.fn(() => null),
      setItem: vi.fn(() => {
        throw new Error("quota");
      }),
    } as unknown as Storage;
    expect(() => reserveRunSteeringRequest(storage, "chat", "task", "run", "text")).toThrow(
      "quota",
    );
  });
  it("does not erase a newer delivery identity", () => {
    const first = reserveRunSteeringRequest(sessionStorage, "chat", "task", "run", "text");
    sessionStorage.setItem(first.key, "newer");
    settleRunSteeringRequest(sessionStorage, first);
    expect(sessionStorage.getItem(first.key)).toBe("newer");
  });
});

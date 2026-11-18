import { beforeEach, describe, expect, it, vi } from "vitest";
import { reserveRunSteeringRequest, settleRunSteeringRequest } from "./run-steering";
import { sha256Hex } from "./sha256";

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
  it("bounds each run independently while allowing exact retries at the limit", () => {
    const first = reserveRunSteeringRequest(sessionStorage, "chat", "task", "run", "text 0");
    for (let index = 1; index < 64; index++) {
      reserveRunSteeringRequest(sessionStorage, "chat", "task", "run", `text ${index}`);
    }
    expect(() => reserveRunSteeringRequest(sessionStorage, "chat", "task", "run", "more")).toThrow(
      "Retry the original text",
    );
    expect(reserveRunSteeringRequest(sessionStorage, "chat", "task", "run", "text 0")).toEqual(
      first,
    );
    expect(() =>
      reserveRunSteeringRequest(sessionStorage, "chat", "task", "next", "more"),
    ).not.toThrow();
    expect(() =>
      reserveRunSteeringRequest(sessionStorage, "chat", "other", "run", "more"),
    ).not.toThrow();
    expect(() =>
      reserveRunSteeringRequest(sessionStorage, "other", "task", "run", "more"),
    ).not.toThrow();
    expect(sessionStorage.getItem(first.key)).toBe(first.id);
  });
  it("migrates an exact legacy reservation without changing its retry identity", () => {
    const legacyKey = `hecate.runSteering.${sha256Hex(JSON.stringify(["chat", "task", "run", "text"]))}`;
    sessionStorage.setItem(legacyKey, "original-delivery-id");
    for (let index = 0; index < 64; index++) {
      sessionStorage.setItem(`hecate.runSteering.legacy-${index}`, `id-${index}`);
    }
    const request = reserveRunSteeringRequest(sessionStorage, "chat", "task", "run", "text");
    expect(request.id).toBe("original-delivery-id");
    expect(request.key).not.toBe(legacyKey);
    expect(sessionStorage.getItem(request.key)).toBe(request.id);
    expect(sessionStorage.getItem(legacyKey)).toBeNull();
    expect(reserveRunSteeringRequest(sessionStorage, "chat", "task", "run", "text")).toEqual(
      request,
    );
  });
  it("preserves the legacy identity when migration cannot be verified", () => {
    const legacyKey = `hecate.runSteering.${sha256Hex(JSON.stringify(["chat", "task", "run", "text"]))}`;
    const removeItem = vi.fn();
    const storage = {
      getItem: vi.fn((key: string) => (key === legacyKey ? "original-delivery-id" : null)),
      setItem: vi.fn(),
      removeItem,
    } as unknown as Storage;
    expect(() => reserveRunSteeringRequest(storage, "chat", "task", "run", "text")).toThrow(
      "Could not save guidance delivery state",
    );
    expect(removeItem).not.toHaveBeenCalled();
    expect(storage.getItem(legacyKey)).toBe("original-delivery-id");
  });
  it("does not choose between conflicting current and legacy identities", () => {
    const current = reserveRunSteeringRequest(sessionStorage, "chat", "task", "run", "text");
    const legacyKey = `hecate.runSteering.${sha256Hex(JSON.stringify(["chat", "task", "run", "text"]))}`;
    sessionStorage.setItem(legacyKey, "different-delivery-id");
    expect(() => reserveRunSteeringRequest(sessionStorage, "chat", "task", "run", "text")).toThrow(
      "Saved guidance delivery state is unreadable",
    );
    expect(sessionStorage.getItem(current.key)).toBe(current.id);
    expect(sessionStorage.getItem(legacyKey)).toBe("different-delivery-id");
  });
});

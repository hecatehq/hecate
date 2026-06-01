import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { parseStoredJSON, parseStoredString, usePersistedState } from "./persistedState";

const logWarnMock = vi.fn();
vi.mock("./log", () => ({
  info: vi.fn(),
  warn: (message: string, ...args: unknown[]) => logWarnMock(message, ...args),
  error: vi.fn(),
}));

beforeEach(() => {
  window.localStorage.clear();
  logWarnMock.mockReset();
});

afterEach(() => {
  window.localStorage.clear();
});

describe("usePersistedState (string)", () => {
  it("seeds state from localStorage when present", () => {
    window.localStorage.setItem("k", "stored");
    const { result } = renderHook(() => usePersistedState("k", parseStoredString, "fallback"));
    expect(result.current[0]).toBe("stored");
  });

  it("uses fallback when key is missing", () => {
    const { result } = renderHook(() => usePersistedState("k", parseStoredString, "fallback"));
    expect(result.current[0]).toBe("fallback");
  });

  it("writes to localStorage on change", () => {
    const { result } = renderHook(() => usePersistedState("k", parseStoredString, ""));
    act(() => result.current[1]("next"));
    expect(window.localStorage.getItem("k")).toBe("next");
  });

  it("supports the SetStateAction callback form", () => {
    const { result } = renderHook(() => usePersistedState("k", parseStoredString, "init"));
    act(() => result.current[1]((current) => current + "+"));
    expect(result.current[0]).toBe("init+");
  });

  it("removes the key when shouldRemove(value) is true", () => {
    const { result } = renderHook(() =>
      usePersistedState("k", parseStoredString, "", {
        shouldRemove: (v) => v === "",
      }),
    );
    act(() => result.current[1]("a"));
    expect(window.localStorage.getItem("k")).toBe("a");
    act(() => result.current[1](""));
    expect(window.localStorage.getItem("k")).toBeNull();
  });

  it("honours shouldRemove when the value is produced by the SetStateAction callback form", () => {
    window.localStorage.setItem("k", "seed");
    const { result } = renderHook(() =>
      usePersistedState("k", parseStoredString, "", {
        shouldRemove: (v) => v === "",
      }),
    );
    act(() => result.current[1]((current) => (current === "seed" ? "" : "noop")));
    expect(window.localStorage.getItem("k")).toBeNull();
  });
});

describe("usePersistedState (parse rejection)", () => {
  it("wipes the key and falls back when parse returns null", () => {
    window.localStorage.setItem("k", "garbage");
    const parse = (raw: string): "valid" | null => (raw === "valid" ? raw : null);
    const { result } = renderHook(() => usePersistedState("k", parse, "fallback" as const));
    expect(result.current[0]).toBe("fallback");
    expect(window.localStorage.getItem("k")).toBeNull();
  });

  it("logs via lib/log warn on shape mismatch", () => {
    window.localStorage.setItem("k", "garbage");
    renderHook(() => usePersistedState("k", () => null, "fallback"));
    expect(logWarnMock).toHaveBeenCalledWith(expect.stringContaining("dropped malformed k"));
  });

  // hasMountedRef makes the first effect run a no-op; without it, the
  // mount-time effect would re-persist the fallback under the same key
  // we just wiped, defeating the loud-failure semantics.
  it("does not re-persist the fallback after wiping a malformed value", () => {
    window.localStorage.setItem("k", "garbage");
    const parse = (raw: string): "valid" | null => (raw === "valid" ? raw : null);
    renderHook(() => usePersistedState("k", parse, "fallback" as const));
    expect(window.localStorage.getItem("k")).toBeNull();
  });
});

describe("usePersistedState (read failures)", () => {
  it("falls back and logs but does not throw when localStorage.getItem rejects", () => {
    const getItemSpy = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked by policy");
    });
    const { result } = renderHook(() => usePersistedState("k", parseStoredString, "fallback"));
    expect(result.current[0]).toBe("fallback");
    expect(logWarnMock).toHaveBeenCalledWith(
      expect.stringContaining("read failed for k"),
      expect.any(Error),
    );
    getItemSpy.mockRestore();
  });
});

describe("parseStoredJSON", () => {
  type Shape = { id: string; count: number };
  const guard = (parsed: unknown): Shape | null => {
    if (!parsed || typeof parsed !== "object") return null;
    const candidate = parsed as Partial<Shape>;
    if (typeof candidate.id !== "string" || typeof candidate.count !== "number") return null;
    return { id: candidate.id, count: candidate.count };
  };

  it("returns the parsed value when guard accepts", () => {
    const parse = parseStoredJSON(guard);
    expect(parse(JSON.stringify({ id: "x", count: 3 }))).toEqual({ id: "x", count: 3 });
  });

  it("returns null on JSON parse error", () => {
    expect(parseStoredJSON(guard)("not json")).toBeNull();
  });

  it("returns null when guard rejects", () => {
    expect(parseStoredJSON(guard)(JSON.stringify({ id: "x" }))).toBeNull();
    expect(parseStoredJSON(guard)("null")).toBeNull();
  });

  it("integrates with usePersistedState for complex shapes", () => {
    window.localStorage.setItem("k", JSON.stringify({ id: "ok", count: 7 }));
    const { result } = renderHook(() =>
      usePersistedState("k", parseStoredJSON(guard), { id: "fb", count: 0 }),
    );
    expect(result.current[0]).toEqual({ id: "ok", count: 7 });
    act(() => result.current[1]({ id: "next", count: 9 }));
    expect(window.localStorage.getItem("k")).toBe(JSON.stringify({ id: "next", count: 9 }));
  });
});

describe("usePersistedState (write failures)", () => {
  it("logs but does not throw when localStorage.setItem rejects", () => {
    const setItemSpy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota exceeded");
    });
    const { result } = renderHook(() => usePersistedState("k", parseStoredString, ""));
    expect(() => act(() => result.current[1]("next"))).not.toThrow();
    expect(logWarnMock).toHaveBeenCalledWith(
      expect.stringContaining("write failed for k"),
      expect.any(Error),
    );
    setItemSpy.mockRestore();
  });
});

describe("usePersistedState (sessionStorage)", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
  });
  afterEach(() => {
    window.sessionStorage.clear();
  });

  it("reads and writes to sessionStorage instead of localStorage", () => {
    window.sessionStorage.setItem("k", "stored");
    const { result } = renderHook(() =>
      usePersistedState("k", parseStoredString, "fallback", { storage: "session" }),
    );
    expect(result.current[0]).toBe("stored");
    act(() => result.current[1]("next"));
    expect(window.sessionStorage.getItem("k")).toBe("next");
    expect(window.localStorage.getItem("k")).toBeNull();
  });

  it("falls back when sessionStorage is empty", () => {
    const { result } = renderHook(() =>
      usePersistedState("k", parseStoredString, "fallback", { storage: "session" }),
    );
    expect(result.current[0]).toBe("fallback");
  });

  it("honours shouldRemove against sessionStorage", () => {
    const { result } = renderHook(() =>
      usePersistedState("k", parseStoredString, "", {
        storage: "session",
        shouldRemove: (v) => v === "",
      }),
    );
    act(() => result.current[1]("a"));
    expect(window.sessionStorage.getItem("k")).toBe("a");
    act(() => result.current[1](""));
    expect(window.sessionStorage.getItem("k")).toBeNull();
  });

  it("wipes a malformed sessionStorage value the same way", () => {
    window.sessionStorage.setItem("k", "garbage");
    const parse = (raw: string): "valid" | null => (raw === "valid" ? raw : null);
    const { result } = renderHook(() =>
      usePersistedState("k", parse, "fallback" as const, { storage: "session" }),
    );
    expect(result.current[0]).toBe("fallback");
    expect(window.sessionStorage.getItem("k")).toBeNull();
  });
});

describe("usePersistedState (custom serialize)", () => {
  it("uses a caller-supplied serialize function", () => {
    const { result } = renderHook(() =>
      usePersistedState<number>("k", (raw) => Number.parseInt(raw, 10), 0, {
        serialize: (v) => `${v}!`,
      }),
    );
    act(() => result.current[1](42));
    expect(window.localStorage.getItem("k")).toBe("42!");
  });
});

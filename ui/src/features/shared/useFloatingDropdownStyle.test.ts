import { renderHook } from "@testing-library/react";
import type { RefObject } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useFloatingDropdownStyle } from "./useFloatingDropdownStyle";

function triggerAt(rect: Partial<DOMRect>): RefObject<HTMLElement | null> {
  const trigger = document.createElement("button");
  trigger.getBoundingClientRect = vi.fn(
    () =>
      ({
        x: rect.left ?? 0,
        y: rect.top ?? 0,
        top: rect.top ?? 0,
        right: rect.right ?? 0,
        bottom: rect.bottom ?? 0,
        left: rect.left ?? 0,
        width: rect.width ?? 0,
        height: rect.height ?? 0,
        toJSON: () => ({}),
      }) as DOMRect,
  );
  return { current: trigger };
}

function setViewport(width: number, height: number) {
  vi.stubGlobal("innerWidth", width);
  vi.stubGlobal("innerHeight", height);
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useFloatingDropdownStyle", () => {
  it("keeps a downward menu inside the viewport", () => {
    setViewport(390, 844);
    const triggerRef = triggerAt({ top: 100, right: 180, bottom: 144, left: 80 });
    const { result } = renderHook(() =>
      useFloatingDropdownStyle(triggerRef, true, "left", "down", 220),
    );

    expect(result.current).toMatchObject({
      top: 148,
      bottom: "auto",
      left: 80,
      right: "auto",
      maxHeight: 688,
      overflowY: "auto",
    });
  });

  it("opens upward when the requested direction has too little room", () => {
    setViewport(390, 390);
    const triggerRef = triggerAt({ top: 320, right: 360, bottom: 364, left: 280 });
    const { result } = renderHook(() =>
      useFloatingDropdownStyle(triggerRef, true, "left", "down", 220),
    );

    expect(result.current).toMatchObject({
      top: "auto",
      bottom: 74,
      left: 162,
      maxHeight: 308,
    });
  });

  it("clamps right-aligned menus away from both viewport edges", () => {
    setViewport(320, 568);
    const triggerRef = triggerAt({ top: 80, right: 50, bottom: 124, left: 8 });
    const { result } = renderHook(() =>
      useFloatingDropdownStyle(triggerRef, true, "right", "down", 300),
    );

    expect(result.current).toMatchObject({ left: "auto", right: 12 });
    expect(result.current?.maxWidth).toBe("calc(100vw - 16px)");
  });

  it("returns no fixed positioning while closed", () => {
    setViewport(390, 844);
    const triggerRef = triggerAt({ top: 100, right: 180, bottom: 144, left: 80 });
    const { result } = renderHook(() =>
      useFloatingDropdownStyle(triggerRef, false, "left", "down", 220),
    );

    expect(result.current).toBeUndefined();
  });
});

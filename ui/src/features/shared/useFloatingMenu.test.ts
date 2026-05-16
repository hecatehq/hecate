import { act, renderHook } from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useFloatingMenu } from "./useFloatingMenu";

afterEach(() => {
  document.body.innerHTML = "";
});

describe("useFloatingMenu", () => {
  it("starts closed", () => {
    const { result } = renderHook(() => useFloatingMenu());
    expect(result.current.open).toBe(false);
  });

  it("setOpen toggles the open flag", () => {
    const { result } = renderHook(() => useFloatingMenu());
    act(() => result.current.setOpen(true));
    expect(result.current.open).toBe(true);
    act(() => result.current.setOpen(false));
    expect(result.current.open).toBe(false);
  });

  it("toggle flips the open flag", () => {
    const { result } = renderHook(() => useFloatingMenu());
    act(() => result.current.toggle());
    expect(result.current.open).toBe(true);
    act(() => result.current.toggle());
    expect(result.current.open).toBe(false);
  });

  it("close() short-circuits when already closed (no spurious onClose)", () => {
    const onClose = vi.fn();
    const { result } = renderHook(() => useFloatingMenu({ onClose }));
    act(() => result.current.close());
    expect(onClose).not.toHaveBeenCalled();
    act(() => result.current.setOpen(true));
    act(() => result.current.close());
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("toggle invokes onClose when transitioning from open → closed", () => {
    const onClose = vi.fn();
    const { result } = renderHook(() => useFloatingMenu({ onClose }));
    act(() => result.current.toggle());
    expect(onClose).not.toHaveBeenCalled();
    act(() => result.current.toggle());
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  // The commit-phase effect (previousOpenRef + useEffect on [open])
  // exists specifically to keep onClose at exactly-once per
  // close transition under StrictMode. Calling onClose from inside
  // a state updater would fire twice because React 19 invokes
  // updaters twice in dev to surface impure setters.
  it("fires onClose exactly once per close transition under StrictMode", () => {
    const onClose = vi.fn();
    const { result } = renderHook(() => useFloatingMenu({ onClose }), { wrapper: StrictMode });
    act(() => result.current.setOpen(true));
    act(() => result.current.setOpen(false));
    expect(onClose).toHaveBeenCalledTimes(1);
    act(() => result.current.toggle());
    act(() => result.current.toggle());
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  describe("outside-click behaviour", () => {
    let wrap: HTMLDivElement;
    let outside: HTMLDivElement;
    let menuPortal: HTMLDivElement;

    beforeEach(() => {
      wrap = document.createElement("div");
      outside = document.createElement("div");
      menuPortal = document.createElement("div");
      menuPortal.className = "dropdown-menu-floating";
      document.body.append(wrap, outside, menuPortal);
    });

    function attachWrap(result: { current: ReturnType<typeof useFloatingMenu> }) {
      // The outside-click handler reads wrapRef.current via a ref;
      // wiring it after the hook initialises matches how every
      // picker actually mounts (the ref attaches when React commits).
      result.current.wrapRef.current = wrap;
    }

    it("closes when a mousedown lands outside the wrap", () => {
      const onClose = vi.fn();
      const { result } = renderHook(() => useFloatingMenu({ onClose }));
      attachWrap(result);
      act(() => result.current.setOpen(true));
      act(() => {
        outside.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
      });
      expect(result.current.open).toBe(false);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("does NOT close when the mousedown lands inside the wrap", () => {
      const inner = document.createElement("button");
      wrap.appendChild(inner);
      const { result } = renderHook(() => useFloatingMenu());
      attachWrap(result);
      act(() => result.current.setOpen(true));
      act(() => {
        inner.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
      });
      expect(result.current.open).toBe(true);
    });

    it("does NOT close when the mousedown lands inside the portal selector", () => {
      const item = document.createElement("button");
      menuPortal.appendChild(item);
      const { result } = renderHook(() => useFloatingMenu());
      attachWrap(result);
      act(() => result.current.setOpen(true));
      act(() => {
        item.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
      });
      expect(result.current.open).toBe(true);
    });

    it("portalSelector: null treats portal-region clicks as outside (ChipInput mode)", () => {
      const item = document.createElement("button");
      menuPortal.appendChild(item);
      const { result } = renderHook(() => useFloatingMenu({ portalSelector: null }));
      attachWrap(result);
      act(() => result.current.setOpen(true));
      act(() => {
        item.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
      });
      expect(result.current.open).toBe(false);
    });

    it("closeOn: 'click' uses the click event instead of mousedown", () => {
      const { result } = renderHook(() => useFloatingMenu({ closeOn: "click" }));
      attachWrap(result);
      act(() => result.current.setOpen(true));
      // mousedown should be ignored when closeOn is click
      act(() => {
        outside.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
      });
      expect(result.current.open).toBe(true);
      // click closes
      act(() => {
        outside.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      });
      expect(result.current.open).toBe(false);
    });

    it("custom portalSelector consults the operator-supplied selector", () => {
      const customPortal = document.createElement("div");
      customPortal.className = "my-custom-portal";
      const item = document.createElement("button");
      customPortal.appendChild(item);
      document.body.appendChild(customPortal);
      const { result } = renderHook(() => useFloatingMenu({ portalSelector: ".my-custom-portal" }));
      attachWrap(result);
      act(() => result.current.setOpen(true));
      act(() => {
        item.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
      });
      expect(result.current.open).toBe(true);
    });

    it("does not invoke onClose when an outside click lands while already closed", () => {
      const onClose = vi.fn();
      const { result } = renderHook(() => useFloatingMenu({ onClose }));
      attachWrap(result);
      act(() => {
        outside.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
      });
      expect(onClose).not.toHaveBeenCalled();
    });
  });

  it("removes the document listener on unmount", () => {
    const remove = vi.spyOn(document, "removeEventListener");
    const { unmount } = renderHook(() => useFloatingMenu());
    unmount();
    expect(remove).toHaveBeenCalledWith("mousedown", expect.any(Function));
  });

  // The outside-click effect deps on `[closeOn, portalSelector]`.
  // If a consumer flips either between renders the hook needs to
  // re-bind so the new event type / selector takes effect — verified
  // here so a future "depend on `open` too" change can't silently
  // wedge the closeOn path.
  it("re-binds the document listener when closeOn or portalSelector changes", () => {
    const add = vi.spyOn(document, "addEventListener");
    const remove = vi.spyOn(document, "removeEventListener");
    const { rerender } = renderHook(
      ({ closeOn }: { closeOn: "mousedown" | "click" }) => useFloatingMenu({ closeOn }),
      { initialProps: { closeOn: "mousedown" } },
    );
    const initialAdds = add.mock.calls.filter(([type]) => type === "mousedown").length;
    rerender({ closeOn: "click" });
    expect(remove).toHaveBeenCalledWith("mousedown", expect.any(Function));
    expect(add.mock.calls.filter(([type]) => type === "click").length).toBeGreaterThanOrEqual(1);
    expect(add.mock.calls.filter(([type]) => type === "mousedown").length).toBe(initialAdds);
  });
});

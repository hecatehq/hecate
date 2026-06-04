// useFloatingDropdownStyle: positioning hook shared by every picker
// in the console.
//
// When a dropdown lives inside a scrollable / overflow-clipped container
// (e.g. the new-task slideover with `overflowY: auto`), the default
// position:absolute menu gets clipped at the parent box. Switching to
// position:fixed escapes every overflow ancestor — but then the menu
// floats relative to the viewport, so we have to compute (top, left)
// from the trigger's client rect at open time and on scroll/resize.
//
// `align` controls horizontal anchoring: "left" anchors the menu's
// left edge to the trigger's left edge (default — used by the
// provider picker which has narrower content). "right" anchors the
// menu's right edge to the trigger's right edge (the model picker's
// 300-wide menu would overflow off-screen if anchored left on a
// narrow trigger).
//
// `placement` controls vertical anchoring. Header pickers open down;
// composer pickers open up so menus don't cover the message input.

import { useEffect, useState } from "react";
import type React from "react";

const FLOATING_DROPDOWN_Z_INDEX = 1000;

export function useFloatingDropdownStyle(
  triggerRef: React.RefObject<HTMLElement | null>,
  open: boolean,
  align: "left" | "right" = "left",
  placement: "down" | "up" = "down",
): React.CSSProperties | undefined {
  const [style, setStyle] = useState<React.CSSProperties | undefined>(undefined);
  useEffect(() => {
    if (!open || !triggerRef.current) {
      setStyle(undefined);
      return;
    }
    const compute = () => {
      const el = triggerRef.current;
      if (!el) return;
      const r = el.getBoundingClientRect();
      // Anchor BOTH axes explicitly — the global .dropdown-menu CSS
      // pins `left: 0` and `top: calc(100% + 4px)` for the legacy
      // absolute-positioned mode. When we switch to position:fixed,
      // those still apply with high specificity unless we override
      // each one. If we left `left: 0` active alongside our `right:`,
      // the menu would stretch from the viewport's left edge to its
      // right anchor — exactly what was happening for the right-
      // aligned model picker.
      const next: React.CSSProperties = {
        position: "fixed",
        // Floating menus must sit above modal/slideover chrome and any
        // sibling form controls while staying in the dialog subtree for
        // focus-trap and outside-click behavior.
        zIndex: FLOATING_DROPDOWN_Z_INDEX,
      };
      if (placement === "up") {
        next.bottom = window.innerHeight - r.top + 4;
        next.top = "auto";
        next.maxHeight = Math.max(120, r.top - 12);
      } else {
        next.top = r.bottom + 4;
        next.bottom = "auto";
        next.maxHeight = Math.max(120, window.innerHeight - r.bottom - 12);
      }
      next.overflowY = "auto";
      if (align === "right") {
        next.right = window.innerWidth - r.right;
        next.left = "auto";
      } else {
        next.left = r.left;
        next.right = "auto";
      }
      setStyle(next);
    };
    compute();
    // Re-anchor on scroll/resize so the menu tracks the trigger if
    // the user scrolls the underlying container while it's open.
    window.addEventListener("scroll", compute, true);
    window.addEventListener("resize", compute);
    return () => {
      window.removeEventListener("scroll", compute, true);
      window.removeEventListener("resize", compute);
    };
  }, [open, triggerRef, align, placement]);
  return style;
}

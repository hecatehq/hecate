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
const VIEWPORT_GUTTER = 8;
const MENU_GAP = 4;
const PREFERRED_MENU_HEIGHT = 120;

export function useFloatingDropdownStyle(
  triggerRef: React.RefObject<HTMLElement | null>,
  open: boolean,
  align: "left" | "right" = "left",
  placement: "down" | "up" = "down",
  menuWidth = 220,
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
        maxWidth: `calc(100vw - ${VIEWPORT_GUTTER * 2}px)`,
      };
      const availableAbove = Math.max(0, r.top - MENU_GAP - VIEWPORT_GUTTER);
      const availableBelow = Math.max(
        0,
        window.innerHeight - r.bottom - MENU_GAP - VIEWPORT_GUTTER,
      );
      const resolvedPlacement =
        placement === "up"
          ? availableAbove < PREFERRED_MENU_HEIGHT && availableBelow > availableAbove
            ? "down"
            : "up"
          : availableBelow < PREFERRED_MENU_HEIGHT && availableAbove > availableBelow
            ? "up"
            : "down";

      if (resolvedPlacement === "up") {
        next.bottom = window.innerHeight - r.top + MENU_GAP;
        next.top = "auto";
        next.maxHeight = availableAbove;
      } else {
        next.top = r.bottom + MENU_GAP;
        next.bottom = "auto";
        next.maxHeight = availableBelow;
      }
      next.overflowY = "auto";

      const boundedMenuWidth = Math.min(
        Math.max(0, menuWidth),
        Math.max(0, window.innerWidth - VIEWPORT_GUTTER * 2),
      );
      if (align === "right") {
        next.right = Math.min(
          Math.max(VIEWPORT_GUTTER, window.innerWidth - r.right),
          Math.max(VIEWPORT_GUTTER, window.innerWidth - VIEWPORT_GUTTER - boundedMenuWidth),
        );
        next.left = "auto";
      } else {
        next.left = Math.min(
          Math.max(VIEWPORT_GUTTER, r.left),
          Math.max(VIEWPORT_GUTTER, window.innerWidth - VIEWPORT_GUTTER - boundedMenuWidth),
        );
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
  }, [open, triggerRef, align, placement, menuWidth]);
  return style;
}

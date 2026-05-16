// useFloatingMenu: shared open/setOpen + outside-click + ref
// scaffolding that every picker in the console was inlining
// (ModelPicker, DropdownPicker, ProviderPicker, AgentAdapterPicker,
// ChatAgentControls, ObservabilityView's StatusFilterPicker, and
// ChipInput). Pairs with `useFloatingDropdownStyle` for portal-
// positioning and `dropdownKeyboard` for arrow-key nav, but neither
// is required — consumers compose what they need.
//
// Outside-click treats two DOM regions as "inside":
//
//   1. `wrapRef` — the surrounding container that also holds the
//      trigger. Inline-positioned menus (ChipInput) keep their menu
//      inside this same ref, so this is the only check they need.
//
//   2. Anything matching `portalSelector` — the floating menu
//      portal. Pickers that switch to `position: fixed` via
//      `useFloatingDropdownStyle` move their menu to a different
//      DOM ancestor (`.dropdown-menu-floating`), so a wrap-only
//      check would treat clicks inside the menu as "outside" and
//      close it on the first option click. Default
//      `.dropdown-menu-floating` matches the existing portal
//      class; pass `null` to disable (ChipInput).

import { useCallback, useEffect, useRef, useState } from "react";

export type FloatingMenuOptions = {
  /** Additional DOM region treated as "inside" by outside-click.
   *  Defaults to `.dropdown-menu-floating` for portal-style menus.
   *  Pass `null` for inline menus that live inside `wrapRef`. */
  portalSelector?: string | null;
  /** DOM event used to detect outside clicks. `mousedown` (default)
   *  matches the rest of the codebase and fires before `click`, so a
   *  trigger release outside the menu doesn't accidentally dispatch
   *  the underlying button's `onClick`. */
  closeOn?: "mousedown" | "click";
  /** Fires after `setOpen(false)` (either via outside-click or an
   *  explicit `close()` call). Useful for resetting filter state. */
  onClose?: () => void;
};

export type FloatingMenu<WrapEl extends HTMLElement, TriggerEl extends HTMLElement> = {
  open: boolean;
  setOpen: (next: boolean) => void;
  toggle: () => void;
  close: () => void;
  wrapRef: React.RefObject<WrapEl | null>;
  triggerRef: React.RefObject<TriggerEl | null>;
  menuRef: React.RefObject<HTMLDivElement | null>;
};

const DEFAULT_PORTAL_SELECTOR = ".dropdown-menu-floating";

export function useFloatingMenu<
  WrapEl extends HTMLElement = HTMLDivElement,
  TriggerEl extends HTMLElement = HTMLButtonElement,
>(options: FloatingMenuOptions = {}): FloatingMenu<WrapEl, TriggerEl> {
  const portalSelector = options.portalSelector === undefined
    ? DEFAULT_PORTAL_SELECTOR
    : options.portalSelector;
  const closeOn = options.closeOn ?? "mousedown";
  // Mirror onClose to a ref so the outside-click effect doesn't
  // re-bind on every render when the consumer passes a fresh
  // closure each time.
  const onCloseRef = useRef(options.onClose);
  onCloseRef.current = options.onClose;

  const [open, setOpenState] = useState(false);
  const wrapRef = useRef<WrapEl | null>(null);
  const triggerRef = useRef<TriggerEl | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);

  const setOpen = useCallback((next: boolean) => {
    setOpenState((prev) => {
      if (prev === next) return prev;
      if (!next) onCloseRef.current?.();
      return next;
    });
  }, []);
  const toggle = useCallback(() => {
    setOpenState((prev) => {
      const next = !prev;
      if (!next) onCloseRef.current?.();
      return next;
    });
  }, []);
  const close = useCallback(() => setOpen(false), [setOpen]);

  useEffect(() => {
    const handler = (event: MouseEvent) => {
      const target = event.target as Node | null;
      if (!target) return;
      if (wrapRef.current && wrapRef.current.contains(target)) return;
      if (portalSelector && target instanceof HTMLElement && target.closest(portalSelector)) return;
      setOpenState((prev) => {
        if (!prev) return prev;
        onCloseRef.current?.();
        return false;
      });
    };
    document.addEventListener(closeOn, handler);
    return () => document.removeEventListener(closeOn, handler);
  }, [closeOn, portalSelector]);

  return { open, setOpen, toggle, close, wrapRef, triggerRef, menuRef };
}

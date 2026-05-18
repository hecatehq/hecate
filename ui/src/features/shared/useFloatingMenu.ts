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
  /** Deliberately narrower than `Dispatch<SetStateAction<boolean>>` —
   *  takes a plain `boolean`, not a functional updater. Callers that
   *  want "flip the current value" should use `toggle()` instead.
   *  Hiding the functional form keeps the consumer surface small and
   *  prevents the "I passed a function, why did it stringify?" foot-
   *  gun in callers used to `useState`. */
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
  const portalSelector =
    options.portalSelector === undefined ? DEFAULT_PORTAL_SELECTOR : options.portalSelector;
  const closeOn = options.closeOn ?? "mousedown";
  // Mirror onClose to a ref so the outside-click effect doesn't
  // re-bind on every render when the consumer passes a fresh
  // closure each time. Synced in a commit-phase effect (not during
  // render) so concurrent re-renders that don't commit can't
  // strand a stale callback in the ref.
  const onCloseRef = useRef(options.onClose);
  useEffect(() => {
    onCloseRef.current = options.onClose;
  });

  const [open, setOpenState] = useState(false);
  const wrapRef = useRef<WrapEl | null>(null);
  const triggerRef = useRef<TriggerEl | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);

  // open is the single source of truth; an effect fires onClose
  // exactly once per "true → false" commit. Calling onClose from
  // inside a state updater would double-fire under React.StrictMode
  // because updaters can be invoked twice. The effect commits, so
  // it runs only after React has reconciled the new value.
  const previousOpenRef = useRef(false);
  useEffect(() => {
    if (previousOpenRef.current && !open) {
      onCloseRef.current?.();
    }
    previousOpenRef.current = open;
  }, [open]);

  const setOpen = useCallback((next: boolean) => {
    setOpenState(next);
  }, []);
  const toggle = useCallback(() => {
    setOpenState((prev) => !prev);
  }, []);
  const close = useCallback(() => setOpenState(false), []);

  useEffect(() => {
    const handler = (event: MouseEvent) => {
      const target = event.target as Node | null;
      if (!target) return;
      if (wrapRef.current && wrapRef.current.contains(target)) return;
      if (portalSelector && target instanceof HTMLElement && target.closest(portalSelector)) return;
      // setOpenState(false) is unconditional — when the menu is
      // already closed, React's identical-state bail-out keeps the
      // commit-phase onClose effect from firing. Adding an `open`
      // guard here would either require it in the effect deps (re-
      // binding the listener on every open/close transition) or a
      // ref shadow; the bail-out is the cheaper read.
      setOpenState(false);
    };
    document.addEventListener(closeOn, handler);
    return () => document.removeEventListener(closeOn, handler);
  }, [closeOn, portalSelector]);

  return { open, setOpen, toggle, close, wrapRef, triggerRef, menuRef };
}

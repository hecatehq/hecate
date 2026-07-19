// Overlays: SlideOver, Modal, and ConfirmModal — three façades over the
// shared DialogChrome. SlideOver is the right-anchored inspector panel
// used for forms; Modal is a centered overlay used for content that
// interrupts to ask a question; ConfirmModal is the styled replacement
// for window.confirm.

import { useEffect, useLayoutEffect, useRef } from "react";
import type React from "react";

import { Icon, Icons } from "./Icons";

// Shared header chrome for SlideOver and Modal. Renders the title in
// the same mono-uppercase-teal section-label voice the SettingsView
// tabs use, so dialogs read as part of the page rather than a foreign
// system widget. Keyboard: Escape closes.
function DialogChrome({
  id,
  title,
  ariaLabel,
  children,
  dismissible = true,
  footer,
  focusToken,
  initialFocusRef,
  onClose,
  returnFocusRef,
  surface,
}: {
  id?: string;
  title: string;
  ariaLabel?: string;
  children: React.ReactNode;
  dismissible?: boolean;
  footer: React.ReactNode;
  /** Re-evaluate focus when an in-dialog action changes available controls. */
  focusToken?: unknown;
  initialFocusRef?: React.RefObject<HTMLElement | null>;
  onClose: () => void;
  returnFocusRef?: React.RefObject<HTMLElement | null>;
  surface: React.CSSProperties;
}) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(
    document.activeElement instanceof HTMLElement ? document.activeElement : null,
  );

  useEffect(() => {
    const activeElement =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const requestedInitialFocus = initialFocusRef?.current;
    if (
      requestedInitialFocus &&
      dialogRef.current?.contains(requestedInitialFocus) &&
      isRenderedDialogControl(requestedInitialFocus)
    ) {
      requestedInitialFocus.focus();
    } else if (
      !activeElement ||
      !dialogRef.current?.contains(activeElement) ||
      !isRenderedDialogControl(activeElement)
    ) {
      const focusable = focusableDialogElements(dialogRef.current);
      (focusable[0] ?? dialogRef.current)?.focus();
    }
  }, [focusToken, initialFocusRef]);

  // Return focus only when the dialog actually closes. An earlier version
  // combined this with the focusToken effect above, so changing an action
  // from enabled to disabled could briefly return focus to the page while
  // the modal was still open.
  useEffect(() => {
    const dialog = dialogRef.current;
    return () => {
      const activeElementOnClose =
        document.activeElement instanceof HTMLElement ? document.activeElement : null;
      if (
        activeElementOnClose &&
        activeElementOnClose !== document.body &&
        activeElementOnClose !== document.documentElement &&
        dialog &&
        !dialog.contains(activeElementOnClose)
      ) {
        return;
      }
      if (
        previousFocusRef.current &&
        previousFocusRef.current !== document.body &&
        previousFocusRef.current !== document.documentElement &&
        !previousFocusRef.current.matches(":disabled") &&
        document.contains(previousFocusRef.current)
      ) {
        previousFocusRef.current.focus();
      } else if (
        returnFocusRef?.current &&
        !returnFocusRef.current.matches(":disabled") &&
        document.contains(returnFocusRef.current)
      ) {
        returnFocusRef.current.focus();
      }
    };
  }, [returnFocusRef]);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        if (dismissible) onClose();
        return;
      }
      if (e.key === "Tab") {
        trapDialogFocus(e, dialogRef.current);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [dismissible, onClose]);

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        // The desktop macOS titlebar sits at 1000 so its drag region
        // stays above normal workspace content. Dialogs must still
        // eclipse it; otherwise a portal-mounted update dialog leaves
        // a live strip of titlebar over its scrim.
        zIndex: 1100,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background: "var(--scrim)",
        backdropFilter: "blur(2px)",
      }}
      onClick={dismissible ? onClose : undefined}
    >
      <div
        ref={dialogRef}
        id={id}
        role="dialog"
        aria-modal="true"
        aria-label={ariaLabel ?? title}
        tabIndex={-1}
        style={surface}
        onClick={(e) => e.stopPropagation()}
      >
        <div
          style={{
            padding: "11px 16px",
            borderBottom: "1px solid var(--border)",
            display: "flex",
            alignItems: "center",
            gap: 8,
            background: "var(--bg2)",
          }}
        >
          <span
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              fontWeight: 500,
              color: "var(--teal)",
              letterSpacing: "0.04em",
              textTransform: "uppercase",
            }}
          >
            {title}
          </span>
          <button
            className="btn btn-ghost btn-sm"
            style={{ marginLeft: "auto", padding: "3px 6px" }}
            disabled={!dismissible}
            onClick={onClose}
            aria-label="Close"
            title={dismissible ? "Close (Esc)" : "Wait for the current action to finish"}
            type="button"
          >
            <Icon d={Icons.x} size={14} />
          </button>
        </div>
        <div
          style={{
            padding: 16,
            flex: 1,
            overflowY: "auto",
            overscrollBehavior: "contain",
          }}
        >
          {children}
        </div>
        <div
          style={{
            padding: "12px 16px",
            borderTop: "1px solid var(--border)",
            background: "var(--bg2)",
          }}
        >
          {footer}
        </div>
      </div>
    </div>
  );
}

function focusableDialogElements(root: HTMLElement | null): HTMLElement[] {
  if (!root) return [];
  return Array.from(
    root.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), summary, textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter(isRenderedDialogControl);
}

function isRenderedDialogControl(element: HTMLElement): boolean {
  if (element.hasAttribute("disabled")) return false;
  for (let current: HTMLElement | null = element; current; current = current.parentElement) {
    if (
      current.hidden ||
      current.hasAttribute("inert") ||
      current.getAttribute("aria-hidden") === "true" ||
      current.style.display === "none" ||
      current.style.visibility === "hidden"
    ) {
      return false;
    }
    if (current instanceof HTMLDetailsElement && !current.open) {
      const summary = current.querySelector(":scope > summary");
      if (summary !== element) return false;
    }
  }
  return true;
}

function trapDialogFocus(event: KeyboardEvent, root: HTMLElement | null) {
  if (!root) return;
  const focusable = focusableDialogElements(root);
  if (focusable.length === 0) {
    event.preventDefault();
    root.focus();
    return;
  }
  const active = document.activeElement;
  const activeIndex = active instanceof HTMLElement ? focusable.indexOf(active) : -1;
  const nextIndex =
    activeIndex < 0
      ? event.shiftKey
        ? focusable.length - 1
        : 0
      : event.shiftKey
        ? (activeIndex - 1 + focusable.length) % focusable.length
        : (activeIndex + 1) % focusable.length;
  event.preventDefault();
  focusable[nextIndex].focus();
}

// SlideOver is the right-anchored panel used across the console for
// forms. The backdrop closes on click, Escape closes, and the close
// button in the header carries the same affordance — so footers
// don't need a redundant Cancel button.
export function SlideOver({
  title,
  children,
  footer,
  onClose,
  width = 420,
}: {
  title: string;
  children: React.ReactNode;
  footer: React.ReactNode;
  onClose: () => void;
  width?: number;
}) {
  return (
    <DialogChrome
      title={title}
      footer={footer}
      onClose={onClose}
      surface={{
        marginLeft: "auto",
        width,
        background: "var(--bg1)",
        borderLeft: "1px solid var(--border)",
        display: "flex",
        flexDirection: "column",
        height: "100%",
      }}
    >
      {children}
    </DialogChrome>
  );
}

// Modal is a centered overlay dialog. Same chrome as SlideOver but
// floats in the middle of the viewport — use for confirmations and
// content that interrupts to ask a question (vs SlideOver which feels
// like an inspector slot attached to the page).
export function Modal({
  id,
  title,
  ariaLabel,
  children,
  dismissible = true,
  footer,
  focusToken,
  initialFocusRef,
  onClose,
  returnFocusRef,
  width = 560,
}: {
  id?: string;
  title: string;
  ariaLabel?: string;
  children: React.ReactNode;
  dismissible?: boolean;
  footer: React.ReactNode;
  focusToken?: unknown;
  initialFocusRef?: React.RefObject<HTMLElement | null>;
  onClose: () => void;
  returnFocusRef?: React.RefObject<HTMLElement | null>;
  width?: number;
}) {
  return (
    <DialogChrome
      id={id}
      title={title}
      ariaLabel={ariaLabel}
      dismissible={dismissible}
      footer={footer}
      focusToken={focusToken}
      initialFocusRef={initialFocusRef}
      onClose={onClose}
      returnFocusRef={returnFocusRef}
      surface={{
        width,
        maxWidth: "calc(100vw - 24px)",
        maxHeight: "min(80vh, calc(100dvh - 24px))",
        background: "var(--bg1)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius)",
        boxSizing: "border-box",
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        boxShadow: "var(--shadow-modal)",
      }}
    >
      {children}
    </DialogChrome>
  );
}

// ConfirmModal is the styled replacement for `window.confirm` — same
// frame as the import consent dialog (centered Modal) so destructive
// or significant actions read consistently across the console.
//
// Usage: track an "is-this-action-pending-confirmation" piece of state
// in the parent. Render <ConfirmModal /> when it's truthy, pass the
// onConfirm handler that runs the action, onClose handler that clears
// the state. The "danger" flag tints the confirm button red — use it
// for destructive operations like Clear / Delete.
//
// Footer has only the primary action button. Dismiss is via the X in
// the modal header or a backdrop click — adding an explicit Cancel
// button next to Confirm was redundant noise.
export function ConfirmModal({
  title,
  message,
  confirmLabel,
  danger = false,
  pending = false,
  confirmDisabled = false,
  onConfirm,
  onClose,
  returnFocusRef,
}: {
  title: string;
  message: React.ReactNode;
  confirmLabel: string;
  danger?: boolean;
  pending?: boolean;
  confirmDisabled?: boolean;
  onConfirm: () => void | Promise<void>;
  onClose: () => void;
  returnFocusRef?: React.RefObject<HTMLElement | null>;
}) {
  const confirmButtonRef = useRef<HTMLButtonElement>(null);
  const wasPendingRef = useRef(pending);

  useLayoutEffect(() => {
    const wasPending = wasPendingRef.current;
    wasPendingRef.current = pending;
    if (!wasPending || pending) return;

    const confirmButton = confirmButtonRef.current;
    if (confirmButton && !confirmButton.disabled) {
      confirmButton.focus({ preventScroll: true });
    }
  }, [pending]);

  return (
    <Modal
      title={title}
      dismissible={!pending}
      onClose={onClose}
      returnFocusRef={returnFocusRef}
      width={420}
      footer={
        <button
          ref={confirmButtonRef}
          className={`btn ${danger ? "btn-danger" : "btn-primary"}`}
          style={{ width: "100%", justifyContent: "center" }}
          disabled={pending || confirmDisabled}
          onClick={() => void onConfirm()}
        >
          {pending ? "Working…" : confirmLabel}
        </button>
      }
    >
      <div style={{ fontSize: 13, color: "var(--t1)", lineHeight: 1.5 }}>{message}</div>
    </Modal>
  );
}

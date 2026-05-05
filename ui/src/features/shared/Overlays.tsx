// Overlays: SlideOver, Modal, and ConfirmModal — three façades over the
// shared DialogChrome. SlideOver is the right-anchored inspector panel
// used for forms; Modal is a centered overlay used for content that
// interrupts to ask a question; ConfirmModal is the styled replacement
// for window.confirm.

import { useEffect } from "react";
import type React from "react";

import { Icon, Icons } from "./Icons";

// Shared header chrome for SlideOver and Modal. Renders the title in
// the same mono-uppercase-teal section-label voice the SettingsView
// tabs use, so dialogs read as part of the page rather than a foreign
// system widget. Keyboard: Escape closes.
function DialogChrome({
  title,
  children,
  footer,
  onClose,
  surface,
}: {
  title: string;
  children: React.ReactNode;
  footer: React.ReactNode;
  onClose: () => void;
  surface: React.CSSProperties;
}) {
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [onClose]);

  return (
    <div
      style={{ position: "fixed", inset: 0, zIndex: 50, display: "flex", alignItems: "center", justifyContent: "center", background: "var(--scrim)", backdropFilter: "blur(2px)" }}
      onClick={onClose}>
      <div
        role="dialog"
        aria-label={title}
        style={surface}
        onClick={e => e.stopPropagation()}>
        <div style={{ padding: "11px 16px", borderBottom: "1px solid var(--border)", display: "flex", alignItems: "center", gap: 8, background: "var(--bg2)" }}>
          <span style={{
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            fontWeight: 500,
            color: "var(--teal)",
            letterSpacing: "0.04em",
            textTransform: "uppercase",
          }}>{title}</span>
          <button
            className="btn btn-ghost btn-sm"
            style={{ marginLeft: "auto", padding: "3px 6px" }}
            onClick={onClose}
            aria-label="Close"
            title="Close (Esc)">
            <Icon d={Icons.x} size={14} />
          </button>
        </div>
        <div style={{ padding: 16, flex: 1, overflowY: "auto" }}>{children}</div>
        <div style={{ padding: "12px 16px", borderTop: "1px solid var(--border)", background: "var(--bg2)" }}>{footer}</div>
      </div>
    </div>
  );
}

// SlideOver is the right-anchored panel used across the console for
// forms. The backdrop closes on click, Escape closes, and the close
// button in the header carries the same affordance — so footers
// don't need a redundant Cancel button.
export function SlideOver({ title, children, footer, onClose, width = 420 }: {
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
      }}>
      {children}
    </DialogChrome>
  );
}

// Modal is a centered overlay dialog. Same chrome as SlideOver but
// floats in the middle of the viewport — use for confirmations and
// content that interrupts to ask a question (vs SlideOver which feels
// like an inspector slot attached to the page).
export function Modal({ title, children, footer, onClose, width = 560 }: {
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
        width,
        maxHeight: "80vh",
        background: "var(--bg1)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius)",
        display: "flex",
        flexDirection: "column",
        boxShadow: "var(--shadow-modal)",
      }}>
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
  onConfirm,
  onClose,
}: {
  title: string;
  message: React.ReactNode;
  confirmLabel: string;
  danger?: boolean;
  pending?: boolean;
  onConfirm: () => void | Promise<void>;
  onClose: () => void;
}) {
  return (
    <Modal
      title={title}
      onClose={onClose}
      width={420}
      footer={
        <button
          className={`btn ${danger ? "btn-danger" : "btn-primary"}`}
          style={{ width: "100%", justifyContent: "center" }}
          disabled={pending}
          onClick={() => void onConfirm()}>
          {pending ? "Working…" : confirmLabel}
        </button>
      }>
      <div style={{ fontSize: 13, color: "var(--t1)", lineHeight: 1.5 }}>{message}</div>
    </Modal>
  );
}

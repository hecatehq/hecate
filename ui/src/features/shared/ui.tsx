import { useEffect, useRef, useState } from "react";
import type { AgentAdapterRecord, ModelRecord, ProviderPresetRecord } from "../../types/runtime";

// ─── Icon ────────────────────────────────────────────────────────────────────

type IconProps = { d: string | string[]; size?: number; strokeWidth?: number; fill?: string };
export function Icon({ d, size = 16, strokeWidth = 1.5, fill = "none" }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill={fill}
      stroke="currentColor" strokeWidth={strokeWidth} strokeLinecap="round" strokeLinejoin="round"
      style={{ flexShrink: 0 }}>
      {Array.isArray(d) ? d.map((p, i) => <path key={i} d={p} />) : <path d={d} />}
    </svg>
  );
}

export const Icons = {
  chat:     "M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z",
  tasks:    "M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-6 9l2 2 4-4",
  providers:["M5 12h14","M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2","M9 10h.01","M9 16h.01"],
  budgets:  "M12 8c-1.657 0-3 .895-3 2s1.343 2 3 2 3 .895 3 2-1.343 2-3 2m0-8c1.11 0 2.08.402 2.599 1M12 8V7m0 1v8m0 0v1m0-1c-1.11 0-2.08-.402-2.599-1M21 12a9 9 0 11-18 0 9 9 0 0118 0z",
  keys:     "M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z",
  observe:  "M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z",
  settings: ["M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z","M15 12a3 3 0 11-6 0 3 3 0 016 0z"],
  chevL:    "M15 19l-7-7 7-7",
  chevR:    "M9 5l7 7-7 7",
  chevD:    "M19 9l-7 7-7-7",
  plus:     "M12 4v16m8-8H4",
  copy:     "M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z",
  check:    "M5 13l4 4L19 7",
  x:        "M6 18L18 6M6 6l12 12",
  refresh:  "M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15",
  terminal: "M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z",
  folder:   ["M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z"],
  send:     "M12 19l9 2-9-18-9 18 9-2zm0 0v-8",
  stop:     "M8 8h8v8H8z",
  edit:     "M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z",
  trash:    "M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16",
  warning:  "M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z",
  info:     "M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z",
  activity: "M22 12h-4l-3 9L9 3l-3 9H2",
  approve:  "M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z",
  deny:     "M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z",
  retry:    "M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15",
  model:    "M21 16V8a2 2 0 00-1-1.73l-7-4a2 2 0 00-2 0l-7 4A2 2 0 003 8v8a2 2 0 001 1.73l7 4a2 2 0 002 0l7-4A2 2 0 0021 16z",
  branch:   ["M6 3v12","M18 9a3 3 0 100-6 3 3 0 000 6z","M6 21a3 3 0 100-6 3 3 0 000 6z","M18 9a9 9 0 01-9 9"],
  eye:      ["M15 12a3 3 0 11-6 0 3 3 0 016 0z","M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z"],
  search:   "M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z",
  // Broom — used for the "Clear price" action on pricebook rows. The
  // shape: a diagonal handle, a pentagonal brush head, three short
  // vertical bristles below.
  broom:    ["M19.5 4.5L11.5 12.5", "M11.5 12.5L8.5 15.5L8.5 18.5L11.5 18.5L14.5 15.5Z", "M8 19V22 M11 19V22 M14 19V22"],
};

// ─── Badge ───────────────────────────────────────────────────────────────────

type BadgeStatus = "queued" | "running" | "awaiting" | "done" | "failed" | "enabled" | "disabled" | "healthy" | "degraded" | "down" | "ok" | "warn" | "error";
export function Badge({ status, label }: { status: BadgeStatus | string; label?: string }) {
  const map: Record<string, { cls: string; text: string }> = {
    queued:           { text: label || "queued",   cls: "badge-muted"  },
    running:          { text: label || "running",  cls: "badge-teal"   },
    awaiting:         { text: label || "approval", cls: "badge-amber"  },
    awaiting_approval:{ text: label || "approval", cls: "badge-amber"  },
    done:             { text: label || "done",     cls: "badge-green"  },
    completed:        { text: label || "done",     cls: "badge-green"  },
    failed:           { text: label || "failed",   cls: "badge-red"    },
    cancelled:        { text: label || "failed",   cls: "badge-red"    },
    enabled:          { text: label || "enabled",  cls: "badge-green"  },
    disabled:         { text: label || "disabled", cls: "badge-muted"  },
    healthy:          { text: label || "healthy",  cls: "badge-green"  },
    degraded:         { text: label || "degraded", cls: "badge-amber"  },
    down:             { text: label || "down",     cls: "badge-red"    },
    ok:               { text: label || "ok",       cls: "badge-green"  },
    warn:             { text: label || "warn",     cls: "badge-amber"  },
    error:            { text: label || "error",    cls: "badge-red"    },
  };
  const { text, cls } = map[status] ?? { text: label || status, cls: "badge-muted" };
  return <span className={`badge ${cls}`}>{text}</span>;
}

// ─── Dot ─────────────────────────────────────────────────────────────────────

export function Dot({ color = "green", pulse = false }: { color?: "green" | "amber" | "red" | "muted"; pulse?: boolean }) {
  const cls = { green: "dot-green", amber: "dot-amber", red: "dot-red", muted: "dot-muted" }[color];
  return <span className={`dot ${cls}`} style={pulse ? { animation: "dot-pulse 2s infinite" } : {}} />;
}

// ─── Toggle ──────────────────────────────────────────────────────────────────

export function Toggle({ on, onChange, label, ariaLabel }: { on: boolean; onChange: (v: boolean) => void; label?: string; ariaLabel?: string }) {
  return (
    <label className="toggle-wrap" onClick={() => onChange(!on)}>
      <span role="switch" aria-checked={on} aria-label={ariaLabel ?? label} tabIndex={0}
        className={`toggle ${on ? "on" : ""}`} />
      {label && <span style={{ fontSize: 12, color: "var(--t1)" }}>{label}</span>}
    </label>
  );
}

// ─── ChipInput ───────────────────────────────────────────────────────────────

// ChipInput is the multi-select picker every settings form uses for
// list-of-ids fields (provider lists, model allowlists, etc.). It
// replaces the old comma-separated text input —
// no more typo-and-pray, every chip is a value the gateway recognizes.
//
// Three modes:
//   1. options-only (default) — chips can only come from the
//      autocomplete suggestion list. Useful when the wire convention
//      requires existing entities (tenants, providers, models).
//   2. options + freeText — same as above, but a value typed and
//      Enter'd that isn't in options gets added as a chip too.
//      Used for route_reasons where the gateway emits well-known
//      strings but operators may want to match new ones.
//   3. freeText-only (no options) — pure tag input. Falls back to a
//      placeholder hint when empty.
//
// Keyboard contract:
//   - Type to filter suggestions; ArrowDown/ArrowUp to navigate
//   - Enter to commit the highlighted suggestion (or freeText input)
//   - Backspace on an empty input removes the last chip
//   - Click a chip's × to remove it
//   - Esc to close the suggestion dropdown
export type ChipOption = { id: string; label: string };

export function ChipInput({
  values,
  onChange,
  options,
  freeText = false,
  placeholder = "",
  ariaLabel,
  disabled = false,
}: {
  values: string[];
  onChange: (next: string[]) => void;
  options?: ChipOption[];
  freeText?: boolean;
  placeholder?: string;
  ariaLabel?: string;
  disabled?: boolean;
}) {
  const [draft, setDraft] = useState("");
  const [open, setOpen] = useState(false);
  const [highlight, setHighlight] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const wrapRef = useRef<HTMLDivElement>(null);

  // Click outside closes the dropdown — same pattern the existing
  // ProviderPicker / ModelPicker use.
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, []);

  // Suggestions = options not already chipped, filtered by draft.
  const suggestions = (options ?? [])
    .filter(o => !values.includes(o.id))
    .filter(o => {
      if (!draft.trim()) return true;
      const q = draft.toLowerCase();
      return o.id.toLowerCase().includes(q) || o.label.toLowerCase().includes(q);
    });

  const labelById = new Map((options ?? []).map(o => [o.id, o.label]));
  const displayLabel = (id: string) => labelById.get(id) ?? id;

  function commit(id: string) {
    if (!id) return;
    if (values.includes(id)) return;
    onChange([...values, id]);
    setDraft("");
    setHighlight(0);
  }

  function remove(id: string) {
    onChange(values.filter(v => v !== id));
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setOpen(true);
      setHighlight(h => Math.min(h + 1, suggestions.length - 1));
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setHighlight(h => Math.max(h - 1, 0));
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      const picked = suggestions[highlight];
      if (picked) {
        commit(picked.id);
      } else if (freeText && draft.trim()) {
        commit(draft.trim());
      }
      return;
    }
    if (e.key === "Backspace" && draft === "" && values.length > 0) {
      e.preventDefault();
      remove(values[values.length - 1]);
      return;
    }
    if (e.key === "Escape") {
      setOpen(false);
    }
  }

  // The wrapper looks like a regular .input but contains the chip
  // row + a transparent text field. Visual borrow from the existing
  // input style so it slots into Field labels without bespoke CSS.
  return (
    <div className="dropdown-wrap" ref={wrapRef}>
      <div
        className="input"
        style={{
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          gap: 4,
          padding: "4px 6px",
          minHeight: 32,
          cursor: disabled ? "not-allowed" : "text",
          opacity: disabled ? 0.6 : 1,
        }}
        onClick={() => { if (!disabled) inputRef.current?.focus(); }}>
        {values.map(id => (
          <span
            key={id}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 4,
              padding: "2px 6px",
              borderRadius: "var(--radius-sm)",
              background: "var(--bg3)",
              border: "1px solid var(--border)",
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              color: "var(--t0)",
            }}>
            {displayLabel(id)}
            {!disabled && (
              <button
                type="button"
                aria-label={`Remove ${displayLabel(id)}`}
                onClick={e => { e.stopPropagation(); remove(id); }}
                style={{
                  background: "none",
                  border: "none",
                  color: "var(--t2)",
                  cursor: "pointer",
                  padding: 0,
                  fontSize: 12,
                  lineHeight: 1,
                  display: "inline-flex",
                  alignItems: "center",
                }}>
                ×
              </button>
            )}
          </span>
        ))}
        <input
          ref={inputRef}
          type="text"
          aria-label={ariaLabel}
          value={draft}
          disabled={disabled}
          placeholder={values.length === 0 ? placeholder : ""}
          onChange={e => { setDraft(e.target.value); setOpen(true); setHighlight(0); }}
          onFocus={() => setOpen(true)}
          onKeyDown={onKeyDown}
          style={{
            flex: 1,
            minWidth: 80,
            border: "none",
            outline: "none",
            background: "transparent",
            color: "var(--t0)",
            fontFamily: "var(--font-sans)",
            fontSize: 13,
            padding: "2px 4px",
          }}
        />
      </div>
      {open && (suggestions.length > 0 || (freeText && draft.trim())) && (
        <div className="dropdown-menu" style={{ minWidth: 200, maxHeight: 220, overflowY: "auto" }}>
          {suggestions.map((s, i) => (
            <div
              key={s.id}
              className={`dropdown-item ${i === highlight ? "selected" : ""}`}
              // Hover to highlight matches the existing ModelPicker behavior.
              onMouseDown={e => { e.preventDefault(); commit(s.id); }}
              onMouseEnter={() => setHighlight(i)}>
              <span style={{ flex: 1, fontFamily: "var(--font-mono)", fontSize: 12 }}>{s.label}</span>
              {s.label !== s.id && (
                <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>{s.id}</span>
              )}
            </div>
          ))}
          {freeText && draft.trim() && !suggestions.find(s => s.id === draft.trim()) && (
            <div
              className="dropdown-item"
              style={{ fontStyle: "italic", color: "var(--t2)" }}
              onMouseDown={e => { e.preventDefault(); commit(draft.trim()); }}>
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 12 }}>add &quot;{draft.trim()}&quot;</span>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ─── CopyBtn ─────────────────────────────────────────────────────────────────

export function CopyBtn({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard?.writeText(text).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 1800);
  };
  return (
    <button className="btn btn-ghost btn-sm" onClick={copy} style={{ gap: 4, padding: "3px 6px" }}>
      <Icon d={copied ? Icons.check : Icons.copy} size={12} />
      {copied ? "copied" : "copy"}
    </button>
  );
}

// ─── CodeBlock ───────────────────────────────────────────────────────────────

export function InlineError({ message }: { message: string }) {
  if (!message) return null;
  return (
    <div style={{
      display: "flex", alignItems: "flex-start", gap: 8,
      padding: "7px 10px", borderRadius: "var(--radius-sm)",
      background: "var(--red-bg)", border: "1px solid var(--red-border)",
      color: "var(--red)", fontSize: 12, fontFamily: "var(--font-mono)", lineHeight: 1.4,
    }}>
      <span style={{ flexShrink: 0, marginTop: 1 }}>✕</span>
      <span>{message}</span>
    </div>
  );
}

export function CodeBlock({ code, lang = "bash" }: { code: string; lang?: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard?.writeText(code).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };
  return (
    <div className="code-block">
      <div className="code-block-header">
        <span className="code-lang">{lang}</span>
        <button className="code-copy-btn" onClick={copy}>
          <Icon d={copied ? Icons.check : Icons.copy} size={12} />
          {copied ? "copied" : "copy"}
        </button>
      </div>
      <pre className="code-pre"><code>{code}</code></pre>
    </div>
  );
}

// ─── DialogShell (internal) ──────────────────────────────────────────────────

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

// ─── SlideOver ───────────────────────────────────────────────────────────────

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

// ─── Modal ───────────────────────────────────────────────────────────────────

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

// ─── ConfirmModal ────────────────────────────────────────────────────────────

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

// ─── useFloatingDropdownStyle ────────────────────────────────────────────────
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
function useFloatingDropdownStyle(
  triggerRef: React.RefObject<HTMLElement | null>,
  open: boolean,
  align: "left" | "right" = "left",
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
        top: r.bottom + 4,
        zIndex: 200,
      };
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
  }, [open, triggerRef, align]);
  return style;
}

// ─── ModelPicker ─────────────────────────────────────────────────────────────
//
// One picker shared by every surface that needs to pick a model — the
// chat view, the new-task slideover, and any future caller. Earlier
// the chat had its own richer copy with search + disabled-provider
// rendering, while shared/ui exported a simpler grouped one; the two
// fell out of sync (e.g. cost ceiling work hadn't propagated to the
// new-task picker). Consolidating means every surface gets the same
// affordances: type-to-filter, sort disabled providers to the
// bottom, key-icon for unconfigured cloud creds, optional per-row
// provider suffix.
//
// All extension points are optional — callers that don't care about
// disabled-provider rendering or the provider suffix get the same
// look as the old simple picker minus the section headers.

export function ModelPicker({
  value, onChange, models,
  presets,
  disabledProviders,
  modelWarnings,
  showProvider = true,
  triggerWidth,
}: {
  value: string;
  onChange: (v: string) => void;
  models: ModelRecord[];
  // Maps provider id → display name. Used to render the per-row
  // provider suffix as a friendly name (e.g. "openai" → "OpenAI").
  // Without it the picker falls back to the raw provider id.
  presets?: ProviderPresetRecord[];
  // Provider ids whose models render disabled (greyed, not clickable,
  // with a key indicator). Map value is the tooltip explaining why
  // (e.g. "Add an API key for X on the Providers tab"). Pass an
  // empty/omitted map to disable.
  disabledProviders?: Map<string, string>;
  // Per-model non-blocking warnings keyed by model id. The model
  // stays selectable, but a small ⚠ icon renders next to its row
  // with the value as a tooltip. Used by the new-task panel to
  // flag models known to lack tool-calling support (e.g.
  // smollm2:135m, embeddings models) — operators can still pick
  // them if they know what they're doing, but the visual cue
  // saves a confused round-trip when the agent loop fails with
  // "model does not support tool-calling".
  modelWarnings?: Map<string, string>;
  // Render the per-row "(provider name)" suffix. Set false when the
  // outer provider filter is already pinned to a single provider —
  // every row would carry the same suffix, which is just noise.
  showProvider?: boolean;
  // Pin the trigger to a fixed width so it aligns with siblings
  // (chat header pairs the model picker with the provider picker).
  // Defaults to the historical chat width of 220px; pass `undefined`
  // to let the button size to its content.
  triggerWidth?: number | undefined;
}) {
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState("");
  const ref = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  // Right-anchored: the menu is 300px wide and the trigger is at the
  // right side of its row in the chat header, so left-anchoring would
  // push it off-screen on narrow viewports.
  const floatingStyle = useFloatingDropdownStyle(triggerRef, open, "right");

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      const target = e.target as Node;
      // Click-outside detection has to consider BOTH the wrap (which
      // contains the trigger) and the floating menu (which lives at
      // a different DOM ancestor when rendered fixed-position). Without
      // checking the menu, clicking inside the menu would close it.
      if (ref.current && ref.current.contains(target)) return;
      if (target instanceof HTMLElement && target.closest(".dropdown-menu-floating")) return;
      setOpen(false);
      setFilter("");
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, []);

  useEffect(() => {
    if (open) setTimeout(() => inputRef.current?.focus(), 0);
    else setFilter("");
  }, [open]);

  const providerName = (id: string) => presets?.find(p => p.id === id)?.name || id;
  const matchedFilter = filter
    ? models.filter(m => m.id.toLowerCase().includes(filter.toLowerCase()))
    : models;
  // Sort usable models above disabled ones — within each bucket the
  // source order is preserved (provider-grouped, alphabetical-ish).
  // Stable partition via two passes avoids accidentally reordering
  // rows whose disabled state is the same.
  const filtered = (() => {
    if (!disabledProviders || disabledProviders.size === 0) return matchedFilter;
    const usable: ModelRecord[] = [];
    const disabled: ModelRecord[] = [];
    for (const m of matchedFilter) {
      const provider = m.metadata?.provider;
      if (provider && disabledProviders.has(provider)) disabled.push(m);
      else usable.push(m);
    }
    return [...usable, ...disabled];
  })();
  // Disable the picker when there are no models to show. This handles the
  // "selected provider has no discovered models" case (e.g. Ollama or
  // LM Studio with the runtime not running) — opening a dropdown only to
  // see an empty list is worse than a clearly-disabled affordance. The
  // outer caller already passes a provider-scoped `models` array, so this
  // check covers both "no providers configured" and "scoped provider has
  // no models" without extra plumbing.
  const isEmpty = models.length === 0;
  const label = isEmpty ? "no models available" : (value || models[0]?.id || "model");
  const buttonWidth = triggerWidth === undefined ? undefined : triggerWidth;
  const disabledTitle = isEmpty
    ? "No discovered models for this provider. Configure credentials or start the local runtime."
    : label;

  return (
    <div className="dropdown-wrap" ref={ref}>
      <button
        ref={triggerRef}
        className="btn btn-ghost btn-sm"
        onClick={() => { if (!isEmpty) setOpen(o => !o); }}
        disabled={isEmpty}
        title={disabledTitle}
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          gap: 5,
          color: isEmpty ? "var(--t3)" : "var(--t1)",
          width: buttonWidth,
          cursor: isEmpty ? "not-allowed" : undefined,
          opacity: isEmpty ? 0.6 : undefined,
        }}>
        <Icon d={Icons.model} size={13} />
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", textAlign: "left" }}>
          {label}
        </span>
        <Icon d={Icons.chevD} size={11} />
      </button>
      {open && floatingStyle && (
        <div className="dropdown-menu dropdown-menu-floating" style={{ ...floatingStyle, minWidth: 300 }}>
          <div style={{ padding: "6px 8px", borderBottom: "1px solid var(--border)" }}>
            <input
              ref={inputRef}
              className="input"
              style={{ fontSize: 12, padding: "4px 8px", fontFamily: "var(--font-mono)" }}
              placeholder="Filter models…"
              value={filter}
              onChange={e => setFilter(e.target.value)}
              onClick={e => e.stopPropagation()}
            />
          </div>
          <div style={{ maxHeight: 300, overflowY: "auto", overflowX: "hidden" }}>
            {filtered.length === 0 && (
              <div style={{ padding: "10px 12px", fontSize: 12, color: "var(--t3)" }}>No models match</div>
            )}
            {filtered.map(m => {
              const provider = m.metadata?.provider;
              const reason = provider ? disabledProviders?.get(provider) : undefined;
              const disabled = !!reason;
              const warning = !disabled ? modelWarnings?.get(m.id) : undefined;
              // Title combines warning (if any) with the disabled
              // reason. We skip the warning when the row is already
              // disabled — the disabled tooltip is the more
              // important signal.
              const rowTitle = disabled ? reason : warning;
              return (
                <div
                  key={m.id}
                  className={`dropdown-item ${m.id === value ? "selected" : ""}`}
                  title={rowTitle}
                  style={disabled ? { cursor: "not-allowed" } : undefined}
                  onClick={() => {
                    if (disabled) return;
                    onChange(m.id);
                    setOpen(false);
                  }}>
                  {/* Only the model id dims when disabled. Provider
                      name keeps its t3 color so the right column reads
                      consistently across enabled + disabled rows. */}
                  <span
                    style={{
                      flex: 1, fontFamily: "var(--font-mono)", fontSize: 12,
                      overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
                      opacity: disabled ? 0.5 : 1,
                    }}>
                    {m.id}
                  </span>
                  {showProvider && provider && (
                    <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)", flexShrink: 0, marginLeft: 6 }}>
                      {providerName(provider)}
                    </span>
                  )}
                  {/* Reserve a fixed slot whether or not a key/warning
                      icon renders — keeps the right edge aligned
                      across rows so the model-id and provider-name
                      columns stay coherent. Disabled (red key) wins
                      over warning (amber ⚠) when both could fire. */}
                  <span style={{ display: "inline-flex", flexShrink: 0, marginLeft: 6, width: 11, justifyContent: "center" }}>
                    {disabled ? (
                      <span aria-label="credentials missing" style={{ color: "var(--red)", display: "inline-flex" }}>
                        <Icon d={Icons.keys} size={11} />
                      </span>
                    ) : warning ? (
                      <span aria-label={warning} style={{ color: "var(--amber)", display: "inline-flex" }}>
                        <Icon d={Icons.warning} size={11} />
                      </span>
                    ) : null}
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

// ─── ProviderPicker ──────────────────────────────────────────────────────────

// ProviderOption is the shape every caller of ProviderPicker hands in.
// `name` is the display label shown in the dropdown; `id` is what
// `onChange` emits (and what `value` matches against). `healthy` drives
// the small green dot on cloud providers in the chat view; pricebook
// callers leave it undefined.
//
// `kind` + `configured` together drive the key-icon indicator: cloud
// providers show a green key when configured, a red key when not.
// Local providers don't surface a key icon (they don't have keys).
// `disabledReason`, when present, makes the option non-selectable and
// renders a tooltip explaining why — the operator sees that the
// provider exists but understands the gap.
export type ProviderOption = {
  id: string;
  name: string;
  healthy?: boolean;
  kind?: string;
  configured?: boolean;
  disabledReason?: string;
};

// ProviderPicker is the styled dropdown both the chat view (filter
// requests by provider) and the pricebook (filter the table by
// provider) use. Pass `includeAuto` to surface an "All providers"
// sentinel row at the top.
//
// The trigger is auto-sized to the longest possible option label so
// switching selection doesn't shift the controls to its right. The
// implementation: a `display: inline-grid` wrapper holds the active
// label in cell (1,1) and a hidden copy of the *longest* label also in
// cell (1,1). The grid sizes to the wider child, the visible label
// paints on top.
export function ProviderPicker({
  value,
  onChange,
  options,
  includeAuto = false,
  autoValue = "auto",
  autoLabel = "All providers",
  emptyLabel = "select provider",
  triggerWidth,
}: {
  value: string;
  onChange: (v: string) => void;
  options: ProviderOption[];
  includeAuto?: boolean;
  // autoValue is the sentinel emitted/matched when "auto" is selected.
  // Chat view's ProviderFilter type uses "auto"; other callers may
  // prefer "" or a custom string. Defaults to "auto".
  autoValue?: string;
  autoLabel?: string;
  emptyLabel?: string;
  // Pin the trigger button to a fixed pixel width — used in the
  // chat view to align with a sibling dropdown of the same width.
  // When unset (pricebook etc.), the auto-sized inline-grid trigger
  // sizes to the widest option label.
  triggerWidth?: number;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const floatingStyle = useFloatingDropdownStyle(triggerRef, open, "left");

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      const target = e.target as Node;
      if (ref.current && ref.current.contains(target)) return;
      // The menu is now portal-style (position: fixed) and lives
      // outside the wrap, so we have to also exempt its tree.
      if (target instanceof HTMLElement && target.closest(".dropdown-menu-floating")) return;
      setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, []);

  const selected = options.find(o => o.id === value);
  // When the saved `value` doesn't resolve to any current option (the
  // provider was removed, the localStorage value is from an older
  // build, or value is the empty-string default before first
  // selection), render `emptyLabel` instead of the raw id. Showing
  // "stale-anthropic-id" in the trigger looks broken; "select provider"
  // is the honest state — pick again. The previous `?? value`
  // intermediate hop also broke the empty-string case because `??`
  // treats `""` as defined and the trigger went blank.
  const label = includeAuto && value === autoValue
    ? autoLabel
    : selected?.name ?? emptyLabel;
  // Reserve enough horizontal room for the longest option so the
  // trigger's width never depends on the current selection.
  const widestLabel = (() => {
    const candidates = options.map(o => o.name);
    if (includeAuto) candidates.push(autoLabel);
    if (candidates.length === 0) return label;
    return candidates.reduce((a, b) => (b.length > a.length ? b : a));
  })();

  return (
    <div className="dropdown-wrap" ref={ref}>
      <button
        ref={triggerRef}
        type="button"
        className="btn btn-ghost btn-sm"
        onClick={() => setOpen(o => !o)}
        style={{ fontFamily: "var(--font-mono)", fontSize: 11, gap: 5, color: "var(--t1)", width: triggerWidth }}>
        <Icon d={Icons.providers} size={13} />
        {triggerWidth !== undefined ? (
          // Fixed-width mode: ellipsize within the available flex slot.
          <span style={{ flex: 1, minWidth: 0, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", textAlign: "left" }} title={label}>
            {label}
          </span>
        ) : (
          // Auto-size mode: inline-grid reserves space for the widest
          // option so the trigger doesn't shift width when selection
          // changes. The visible label needs `display: block` +
          // `width: 100%` so `text-align: left` actually applies —
          // an inline span shrinks to its content and text-align
          // becomes a no-op, which made shorter labels look centered
          // inside the wider grid cell.
          <span style={{ display: "inline-grid" }}>
            <span aria-hidden style={{ gridColumn: 1, gridRow: 1, visibility: "hidden", whiteSpace: "nowrap", pointerEvents: "none" }}>{widestLabel}</span>
            <span style={{ gridColumn: 1, gridRow: 1, display: "block", width: "100%", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", textAlign: "left" }}>{label}</span>
          </span>
        )}
        <Icon d={Icons.chevD} size={11} />
      </button>
      {open && floatingStyle && (
        <div className="dropdown-menu dropdown-menu-floating" style={{ ...floatingStyle, minWidth: 180 }}>
          {includeAuto && (
            <>
              <div
                className={`dropdown-item ${value === autoValue ? "selected" : ""}`}
                onClick={() => { onChange(autoValue); setOpen(false); }}>
                <span style={{ flex: 1, fontFamily: "var(--font-mono)", fontSize: 12, textAlign: "left" }}>{autoLabel}</span>
              </div>
              {options.length > 0 && <div className="dropdown-divider" />}
            </>
          )}
          {options.map(o => {
            const disabled = !!o.disabledReason;
            // Key indicator only for cloud providers — local providers
            // don't authenticate via keys, so no icon there.
            const showKey = o.kind === "cloud" && o.configured !== undefined;
            const keyColor = o.configured ? "var(--green)" : "var(--red)";
            return (
              <div
                key={o.id}
                className={`dropdown-item ${value === o.id ? "selected" : ""}`}
                title={o.disabledReason}
                style={disabled ? { cursor: "not-allowed" } : undefined}
                onClick={() => {
                  if (disabled) return;
                  onChange(o.id);
                  setOpen(false);
                }}>
                {/* Only the name dims when disabled — the key icon
                    keeps its red color, so the operator's eye lands
                    on it as the reason for the disabled state. */}
                <span
                  style={{
                    flex: 1,
                    fontSize: 12,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                    textAlign: "left",
                    opacity: disabled ? 0.5 : 1,
                  }}>
                  {o.name}
                </span>
                {showKey && (
                  <span
                    aria-label={o.configured ? "credentials configured" : "credentials missing"}
                    style={{ color: keyColor, display: "inline-flex", flexShrink: 0 }}>
                    <Icon d={Icons.keys} size={11} />
                  </span>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ─── AgentAdapterPicker ─────────────────────────────────────────────────────

export function AgentAdapterPicker({
  value,
  onChange,
  adapters,
  triggerWidth = 170,
}: {
  value: string;
  onChange: (v: string) => void;
  adapters: AgentAdapterRecord[];
  triggerWidth?: number;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const floatingStyle = useFloatingDropdownStyle(triggerRef, open, "left");

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      const target = e.target as Node;
      if (ref.current && ref.current.contains(target)) return;
      if (target instanceof HTMLElement && target.closest(".dropdown-menu-floating")) return;
      setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, []);

  const selected = adapters.find((adapter) => adapter.id === value);
  const label = selected?.name ?? "select agent";
  const isEmpty = adapters.length === 0;

  return (
    <div className="dropdown-wrap" ref={ref}>
      <button
        ref={triggerRef}
        aria-label="External agent adapter"
        className="btn btn-ghost btn-sm"
        disabled={isEmpty}
        onClick={() => { if (!isEmpty) setOpen((current) => !current); }}
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          gap: 5,
          color: isEmpty ? "var(--t3)" : "var(--t1)",
          width: triggerWidth,
          opacity: isEmpty ? 0.6 : undefined,
          cursor: isEmpty ? "not-allowed" : undefined,
        }}
        title={isEmpty ? "No external agent adapters are registered" : label}
        type="button"
      >
        <Icon d={Icons.terminal} size={13} />
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", textAlign: "left" }}>
          {label}
        </span>
        <Icon d={Icons.chevD} size={11} />
      </button>
      {open && floatingStyle && (
        <div className="dropdown-menu dropdown-menu-floating" style={{ ...floatingStyle, minWidth: 220 }}>
          {adapters.map((adapter) => {
            const disabled = !adapter.available;
            return (
              <div
                key={adapter.id}
                className={`dropdown-item ${adapter.id === value ? "selected" : ""}`}
                onClick={() => {
                  if (disabled) return;
                  onChange(adapter.id);
                  setOpen(false);
                }}
                style={disabled ? { cursor: "not-allowed" } : undefined}
                title={disabled ? adapter.error || `${adapter.name} command was not found` : adapter.path || adapter.description}
              >
                <span
                  style={{
                    flex: 1,
                    fontSize: 12,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                    textAlign: "left",
                    opacity: disabled ? 0.5 : 1,
                  }}
                >
                  {adapter.name}
                </span>
                <span
                  style={{
                    color: adapter.available ? "var(--green)" : "var(--red)",
                    display: "inline-flex",
                    flexShrink: 0,
                  }}
                  aria-label={adapter.available ? "adapter available" : "adapter missing"}
                >
                  <Icon d={adapter.available ? Icons.check : Icons.x} size={11} />
                </span>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

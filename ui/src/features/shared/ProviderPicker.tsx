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

import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";

import { BrandAvatar } from "./BrandAvatar";
import { Icon, Icons } from "./Icons";
import { focusDropdownItem, focusInitialDropdownItem } from "./dropdownKeyboard";
import { useFloatingDropdownStyle } from "./useFloatingDropdownStyle";

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
  const menuRef = useRef<HTMLDivElement>(null);
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

  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => {
      focusInitialDropdownItem(menuRef.current);
    });
    return () => cancelAnimationFrame(frame);
  }, [open]);

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

  function onMenuKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      setOpen(false);
      triggerRef.current?.focus();
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp" || event.key === "Home" || event.key === "End") {
      event.preventDefault();
      focusDropdownItem(menuRef.current, event.key);
    }
  }

  return (
    <div className="dropdown-wrap" ref={ref}>
      <button
        ref={triggerRef}
        type="button"
        aria-expanded={open}
        aria-haspopup="listbox"
        className="btn btn-ghost btn-sm"
        onClick={() => setOpen(o => !o)}
        style={{ fontFamily: "var(--font-mono)", fontSize: 11, gap: 5, color: "var(--t1)", width: triggerWidth }}>
        {selected ? (
          <BrandAvatar brand={selected.id} fallback={selected.name} boxed={false} size={16} />
        ) : (
          <Icon d={Icons.providers} size={13} />
        )}
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
        <div
          ref={menuRef}
          role="listbox"
          className="dropdown-menu dropdown-menu-floating"
          onKeyDown={onMenuKeyDown}
          style={{ ...floatingStyle, minWidth: 180 }}
        >
          {includeAuto && (
            <>
              <button
                type="button"
                data-dropdown-item
                data-selected={value === autoValue ? "true" : undefined}
                role="option"
                aria-selected={value === autoValue}
                className={`dropdown-item ${value === autoValue ? "selected" : ""}`}
                onClick={() => { onChange(autoValue); setOpen(false); }}>
                <span style={{ flex: 1, fontFamily: "var(--font-mono)", fontSize: 12, textAlign: "left" }}>{autoLabel}</span>
              </button>
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
              <button
                type="button"
                data-dropdown-item
                data-selected={value === o.id ? "true" : undefined}
                role="option"
                aria-selected={value === o.id}
                key={o.id}
                className={`dropdown-item ${value === o.id ? "selected" : ""}`}
                aria-disabled={disabled || undefined}
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
                <BrandAvatar
                  brand={o.id}
                  fallback={o.name}
                  boxed={false}
                  size={16}
                  style={{ opacity: disabled ? 0.5 : 1, flexShrink: 0 }}
                />
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
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

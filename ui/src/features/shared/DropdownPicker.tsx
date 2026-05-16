import { useEffect, useRef, useState } from "react";
import type { CSSProperties, KeyboardEvent, ReactNode } from "react";

import { Icon, Icons } from "./Icons";
import { focusDropdownItem, focusInitialDropdownItem } from "./dropdownKeyboard";
import { useFloatingDropdownStyle } from "./useFloatingDropdownStyle";
import { useFloatingMenu } from "./useFloatingMenu";

export type DropdownPickerOption<Value extends string = string> = {
  value: Value;
  label: string;
  title?: string;
  disabled?: boolean;
  disabledReason?: string;
  icon?: ReactNode;
  detail?: string;
  statusLabel?: string;
  statusColor?: string;
};

export function DropdownPicker<Value extends string = string>({
  value,
  options,
  onChange,
  ariaLabel,
  placeholder = "select",
  triggerIcon,
  triggerPrefix,
  triggerWidth,
  triggerMinWidth,
  triggerMaxWidth,
  menuMinWidth = 220,
  align = "left",
  placement = "down",
  disabled = false,
  disabledReason,
  searchable = false,
  searchPlaceholder = "Filter...",
  buttonStyle,
  renderTriggerLabel,
}: {
  value: Value;
  options: DropdownPickerOption<Value>[];
  onChange: (value: Value) => void;
  ariaLabel: string;
  placeholder?: string;
  triggerIcon?: ReactNode;
  triggerPrefix?: string;
  triggerWidth?: number;
  triggerMinWidth?: number;
  triggerMaxWidth?: number;
  menuMinWidth?: number;
  align?: "left" | "right";
  placement?: "down" | "up";
  disabled?: boolean;
  disabledReason?: string;
  searchable?: boolean;
  searchPlaceholder?: string;
  buttonStyle?: CSSProperties;
  renderTriggerLabel?: (option: DropdownPickerOption<Value> | undefined) => ReactNode;
}) {
  const [filter, setFilter] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  const { open, setOpen, toggle, wrapRef: ref, triggerRef, menuRef } = useFloatingMenu<HTMLDivElement, HTMLButtonElement>({
    // onCloseRef inside useFloatingMenu absorbs closure-identity
    // churn, so passing a fresh () => setFilter("") each render
    // doesn't re-bind the document listener — no useCallback needed.
    onClose: () => setFilter(""),
  });
  const floatingStyle = useFloatingDropdownStyle(triggerRef, open, align, placement);
  const selected = options.find((option) => option.value === value);
  const locked = disabled || options.length === 0;
  const filteredOptions = searchable && filter.trim()
    ? options.filter((option) => {
      const needle = filter.trim().toLowerCase();
      return option.label.toLowerCase().includes(needle)
        || option.detail?.toLowerCase().includes(needle)
        || option.statusLabel?.toLowerCase().includes(needle);
    })
    : options;

  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => {
      if (searchable) inputRef.current?.focus();
      else focusInitialDropdownItem(menuRef.current);
    });
    return () => cancelAnimationFrame(frame);
  }, [open, searchable, menuRef]);

  function closeMenu() {
    setOpen(false);
    triggerRef.current?.focus();
  }

  function onMenuKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      closeMenu();
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp" || event.key === "Home" || event.key === "End") {
      event.preventDefault();
      focusDropdownItem(menuRef.current, event.key);
    }
  }

  function onInputKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      closeMenu();
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      event.stopPropagation();
      focusInitialDropdownItem(menuRef.current);
      return;
    }
    if (event.key === "Enter") {
      const firstEnabled = filteredOptions.find((option) => !option.disabled);
      if (!firstEnabled) return;
      event.preventDefault();
      event.stopPropagation();
      onChange(firstEnabled.value);
      setOpen(false);
    }
  }

  return (
    <div className="dropdown-wrap" ref={ref} style={{ flexShrink: 0 }}>
      <button
        ref={triggerRef}
        aria-label={ariaLabel}
        aria-expanded={open}
        aria-haspopup="listbox"
        className="btn btn-ghost btn-sm"
        disabled={locked}
        onClick={() => {
          if (!locked) toggle();
        }}
        title={locked ? disabledReason || "No options available" : selected?.title || selected?.label || placeholder}
        type="button"
        style={{
          color: locked ? "var(--t3)" : "var(--t1)",
          cursor: locked ? "not-allowed" : undefined,
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          gap: 6,
          justifyContent: "space-between",
          maxWidth: triggerMaxWidth,
          minWidth: triggerMinWidth,
          opacity: locked ? 0.7 : undefined,
          width: triggerWidth,
          ...buttonStyle,
        }}
      >
        <span style={{ display: "inline-flex", alignItems: "center", gap: 6, minWidth: 0 }}>
          {triggerIcon}
          {triggerPrefix && (
            <span style={{ color: "var(--t3)", fontSize: 10, textTransform: "lowercase", whiteSpace: "nowrap" }}>
              {triggerPrefix}
            </span>
          )}
          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {renderTriggerLabel ? renderTriggerLabel(selected) : selected?.label || placeholder}
          </span>
        </span>
        {!locked && <Icon d={Icons.chevD} size={11} />}
      </button>
      {open && floatingStyle && (
        <div
          ref={menuRef}
          role="listbox"
          className="dropdown-menu dropdown-menu-floating"
          onKeyDown={onMenuKeyDown}
          style={{
            ...floatingStyle,
            maxWidth: `min(${Math.max(menuMinWidth, 320)}px, calc(100vw - 24px))`,
            minWidth: menuMinWidth,
            width: menuMinWidth,
          }}
        >
          {searchable && (
            <div style={{ padding: "6px 8px", borderBottom: "1px solid var(--border)" }}>
              <input
                ref={inputRef}
                className="input"
                style={{ fontSize: 12, padding: "4px 8px", fontFamily: "var(--font-mono)" }}
                placeholder={searchPlaceholder}
                aria-label={searchPlaceholder}
                value={filter}
                onChange={(event) => setFilter(event.target.value)}
                onClick={(event) => event.stopPropagation()}
                onKeyDown={onInputKeyDown}
              />
            </div>
          )}
          {filteredOptions.map((option) => {
            const selected = option.value === value;
            const lockedOption = Boolean(option.disabled);
            return (
              <button
                key={option.value}
                type="button"
                data-dropdown-item
                data-selected={selected ? "true" : undefined}
                role="option"
                aria-selected={selected}
                aria-disabled={lockedOption || undefined}
                className={`dropdown-item ${selected ? "selected" : ""}`}
                onClick={() => {
                  if (lockedOption) return;
                  onChange(option.value);
                  setOpen(false);
                }}
                title={option.disabledReason || option.title}
                style={lockedOption ? { cursor: "not-allowed", opacity: 0.5 } : undefined}
              >
                {option.icon}
                <span style={{ display: "grid", flex: 1, minWidth: 0 }}>
                  <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {option.label}
                  </span>
                  {option.detail && (
                    <span style={{
                      color: "var(--t3)",
                      display: "-webkit-box",
                      fontFamily: "var(--font-mono)",
                      fontSize: 10,
                      lineHeight: 1.35,
                      overflow: "hidden",
                      WebkitBoxOrient: "vertical",
                      WebkitLineClamp: 2,
                      whiteSpace: "normal",
                    }}>
                      {option.detail}
                    </span>
                  )}
                </span>
                {option.statusLabel && (
                  <span style={{ color: option.statusColor || "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 9, letterSpacing: "0.04em", textTransform: "uppercase" }}>
                    · {option.statusLabel}
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

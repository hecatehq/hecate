import { useEffect, useRef, useState } from "react";
import type { CSSProperties, KeyboardEvent, ReactNode } from "react";

import { Icon, Icons } from "./Icons";
import { focusDropdownItem, focusInitialDropdownItem } from "./dropdownKeyboard";
import { useFloatingDropdownStyle } from "./useFloatingDropdownStyle";

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
  disabled = false,
  disabledReason,
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
  disabled?: boolean;
  disabledReason?: string;
  buttonStyle?: CSSProperties;
  renderTriggerLabel?: (option: DropdownPickerOption<Value> | undefined) => ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const floatingStyle = useFloatingDropdownStyle(triggerRef, open, align);
  const selected = options.find((option) => option.value === value);
  const locked = disabled || options.length === 0;

  useEffect(() => {
    const handler = (event: MouseEvent) => {
      const target = event.target as Node;
      if (ref.current?.contains(target)) return;
      if (target instanceof HTMLElement && target.closest(".dropdown-menu-floating")) return;
      setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, []);

  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => focusInitialDropdownItem(menuRef.current));
    return () => cancelAnimationFrame(frame);
  }, [open]);

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
    <div className="dropdown-wrap" ref={ref} style={{ flexShrink: 0 }}>
      <button
        ref={triggerRef}
        aria-label={ariaLabel}
        aria-expanded={open}
        aria-haspopup="listbox"
        className="btn btn-ghost btn-sm"
        disabled={locked}
        onClick={() => {
          if (!locked) setOpen((current) => !current);
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
          style={{ ...floatingStyle, minWidth: menuMinWidth }}
        >
          {options.map((option) => {
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
                    <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
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

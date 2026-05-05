// AgentAdapterPicker is the dropdown the chat view uses to switch
// between registered external-agent adapters (Codex / Claude Code /
// Cursor Agent). It surfaces both the dashboard's discovery flag and
// the on-demand probe result so operators can see at a glance which
// adapter is actually usable on this machine.

import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";

import type { AgentAdapterHealthRecord, AgentAdapterRecord } from "../../types/runtime";
import { Icon, Icons } from "./Icons";
import { focusDropdownItem, focusInitialDropdownItem } from "./dropdownKeyboard";
import { useFloatingDropdownStyle } from "./useFloatingDropdownStyle";

// adapterPickerDiagnostic combines the dashboard's "is the binary on
// PATH?" flag with the on-demand probe result into one row diagnostic.
// Probe data wins when present — it's a strictly more informative
// signal — but we still fall back to the dashboard flag so the picker
// reads correctly before any probe has run.
function adapterPickerDiagnostic(
  adapter: AgentAdapterRecord,
  health: AgentAdapterHealthRecord | null | undefined,
): { title: string; iconColor: string; chipLabel: string; chipColor: string } {
  if (health) {
    switch (health.status) {
      case "ready":
        return {
          title: health.path ? `Ready (${health.path})` : "Ready",
          iconColor: "var(--green)",
          chipLabel: "ready",
          chipColor: "var(--teal)",
        };
      case "auth_required":
        return {
          title: health.hint || health.error || "Authentication required",
          iconColor: "var(--amber)",
          chipLabel: "auth",
          chipColor: "var(--amber)",
        };
      case "not_installed":
        return {
          title: health.hint || health.error || `${adapter.name} command was not found`,
          iconColor: "var(--red)",
          chipLabel: "missing",
          chipColor: "var(--red)",
        };
      case "error":
        return {
          title: health.error || `${adapter.name} probe failed`,
          iconColor: "var(--red)",
          chipLabel: "error",
          chipColor: "var(--red)",
        };
    }
  }
  if (!adapter.available) {
    return {
      title: adapter.error || `${adapter.name} command was not found`,
      iconColor: "var(--red)",
      chipLabel: "",
      chipColor: "",
    };
  }
  if (adapter.auth_status && adapter.auth_status !== "ok") {
    return {
      title: adapter.auth_error || `Auth status: ${adapter.auth_status}`,
      iconColor: adapter.auth_status === "billing" ? "var(--red)" : "var(--amber)",
      chipLabel: adapter.auth_status === "billing" ? "billing" : "auth",
      chipColor: adapter.auth_status === "billing" ? "var(--red)" : "var(--amber)",
    };
  }
  return {
    title: adapter.path || adapter.description || adapter.name,
    iconColor: "var(--green)",
    chipLabel: "",
    chipColor: "",
  };
}

export function AgentAdapterPicker({
  value,
  onChange,
  adapters,
  healthByID,
  disabled = false,
  disabledReason = "",
  triggerWidth = 170,
}: {
  value: string;
  onChange: (v: string) => void;
  adapters: AgentAdapterRecord[];
  // healthByID is optional; when supplied, each row in the dropdown
  // shows the most recent probe diagnostic (auth required / error) so
  // operators can see why an adapter that's "available" might still
  // fail. The dashboard's discovery flag (adapter.available) covers
  // "binary on PATH"; the probe covers "auth + handshake works".
  healthByID?: Map<string, AgentAdapterHealthRecord>;
  disabled?: boolean;
  disabledReason?: string;
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

  const selected = adapters.find((adapter) => adapter.id === value);
  const label = selected?.name ?? "select agent";
  const isEmpty = adapters.length === 0;
  const locked = disabled || isEmpty;

  return (
    <div className="dropdown-wrap" ref={ref}>
      <button
        ref={triggerRef}
        aria-label="External agent adapter"
        aria-expanded={open}
        aria-haspopup="listbox"
        className="btn btn-ghost btn-sm"
        disabled={locked}
        onClick={() => { if (!locked) setOpen((current) => !current); }}
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          gap: 5,
          color: locked ? "var(--t3)" : "var(--t1)",
          width: triggerWidth,
          opacity: locked ? 0.7 : undefined,
          cursor: locked ? "not-allowed" : undefined,
        }}
        title={isEmpty ? "No external agent adapters are registered" : disabledReason || label}
        type="button"
      >
        <Icon d={Icons.terminal} size={13} />
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", textAlign: "left" }}>
          {label}
        </span>
        {!locked && <Icon d={Icons.chevD} size={11} />}
      </button>
      {open && floatingStyle && (
        <div
          ref={menuRef}
          role="listbox"
          className="dropdown-menu dropdown-menu-floating"
          onKeyDown={onMenuKeyDown}
          style={{ ...floatingStyle, minWidth: 220 }}
        >
          {adapters.map((adapter) => {
            const health = healthByID?.get(adapter.id);
            const isProbeReady = health?.status === "ready";
            const disabled = !adapter.available && !isProbeReady;
            const diag = adapterPickerDiagnostic(adapter, health);
            return (
              <button
                type="button"
                data-dropdown-item
                data-selected={adapter.id === value ? "true" : undefined}
                role="option"
                aria-selected={adapter.id === value}
                key={adapter.id}
                className={`dropdown-item ${adapter.id === value ? "selected" : ""}`}
                onClick={() => {
                  if (disabled) return;
                  onChange(adapter.id);
                  setOpen(false);
                }}
                aria-disabled={disabled || undefined}
                style={disabled ? { cursor: "not-allowed" } : undefined}
                title={diag.title}
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
                {diag.chipLabel && (
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 9,
                      color: diag.chipColor,
                      textTransform: "uppercase",
                      letterSpacing: "0.04em",
                      flexShrink: 0,
                    }}
                  >
                    {diag.chipLabel}
                  </span>
                )}
                <span
                  style={{
                    color: diag.iconColor,
                    display: "inline-flex",
                    flexShrink: 0,
                  }}
                  aria-label={diag.title}
                >
                  <Icon d={!disabled ? Icons.check : Icons.x} size={11} />
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

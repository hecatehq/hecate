// AgentAdapterPicker is the dropdown the chat view uses to switch
// between registered external-agent adapters (Codex / Claude Code /
// Cursor Agent). It surfaces both the dashboard's discovery flag and
// the on-demand probe result so operators can see at a glance which
// adapter is actually usable on this machine.

import { useEffect } from "react";
import type { KeyboardEvent } from "react";

import type { AgentAdapterHealthRecord, AgentAdapterRecord } from "../../types/agent-adapter";
import { Icon, Icons } from "./Icons";
import { focusDropdownItem, focusInitialDropdownItem } from "./dropdownKeyboard";
import { useFloatingDropdownStyle } from "./useFloatingDropdownStyle";
import { useFloatingMenu } from "./useFloatingMenu";

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
          title: adapterReadyTitle(adapter, health.path),
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
          iconColor: "var(--t3)",
          chipLabel: "setup",
          chipColor: "var(--t3)",
        };
      case "error":
        if (adapterProbeLooksLikeSetupState(health)) {
          return {
            title: health.hint || health.error || `Set up ${adapter.name} to use it`,
            iconColor: "var(--t3)",
            chipLabel: "setup",
            chipColor: "var(--t3)",
          };
        }
        return {
          title: health.error || `${adapter.name} probe failed`,
          iconColor: "var(--amber)",
          chipLabel: "issue",
          chipColor: "var(--amber)",
        };
    }
  }
  if (!adapter.available) {
    return {
      title: adapter.error || `${adapter.name} command was not found`,
      iconColor: "var(--t3)",
      chipLabel: "setup",
      chipColor: "var(--t3)",
    };
  }
  if (adapter.auth_status === "billing") {
    return {
      title: adapter.auth_error || "Billing or usage limit requires attention",
      iconColor: "var(--amber)",
      chipLabel: "billing",
      chipColor: "var(--amber)",
    };
  }
  if (adapter.auth_status === "unauthenticated") {
    return {
      title: adapter.auth_error || "Authentication required",
      iconColor: "var(--amber)",
      chipLabel: "auth",
      chipColor: "var(--amber)",
    };
  }
  if (adapter.auth_status === "unknown") {
    return {
      title:
        adapter.auth_error || "Auth has not been verified yet. Test this adapter in Connections.",
      iconColor: "var(--t3)",
      chipLabel: "check",
      chipColor: "var(--t3)",
    };
  }
  if (adapter.auth_status && adapter.auth_status !== "ok") {
    return {
      title: adapter.auth_error || `Auth status: ${adapter.auth_status}`,
      iconColor: "var(--amber)",
      chipLabel: "auth",
      chipColor: "var(--amber)",
    };
  }
  return {
    title: adapterAvailableTitle(adapter),
    iconColor: "var(--green)",
    chipLabel: "",
    chipColor: "",
  };
}

function adapterProbeLooksLikeSetupState(health: AgentAdapterHealthRecord): boolean {
  const text = `${health.hint ?? ""} ${health.error ?? ""}`.toLowerCase();
  return (
    text.includes("app cli missing") ||
    text.includes("command was not found") ||
    text.includes("setup docs:") ||
    text.startsWith("install ")
  );
}

function adapterReadyTitle(adapter: AgentAdapterRecord, path: string | undefined): string {
  const suffix = path ? ` Path: ${path}` : "";
  return `${adapter.name} is ready. Hecate verified adapter startup, auth, and ACP session creation.${suffix}`;
}

function adapterAvailableTitle(adapter: AgentAdapterRecord): string {
  const command = adapter.path || adapter.command;
  const suffix = command ? ` Command: ${command}` : "";
  return `${adapter.name} is available. Hecate found the adapter and local auth looks configured; open Connections to run the full ACP readiness check.${suffix}`;
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
  const {
    open,
    setOpen,
    toggle,
    wrapRef: ref,
    triggerRef,
    menuRef,
  } = useFloatingMenu<HTMLDivElement, HTMLButtonElement>();
  const floatingStyle = useFloatingDropdownStyle(triggerRef, open, "left");

  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => {
      focusInitialDropdownItem(menuRef.current);
    });
    return () => cancelAnimationFrame(frame);
  }, [open, menuRef]);

  function onMenuKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      setOpen(false);
      triggerRef.current?.focus();
      return;
    }
    if (
      event.key === "ArrowDown" ||
      event.key === "ArrowUp" ||
      event.key === "Home" ||
      event.key === "End"
    ) {
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
        onClick={() => {
          if (!locked) toggle();
        }}
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
        <span
          style={{
            flex: 1,
            minWidth: 0,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            textAlign: "left",
          }}
        >
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

// AgentAdapterPicker is the dropdown the chat view uses to switch between
// registered external-agent adapters. Passive discovery controls whether an
// adapter can be selected; an on-demand diagnostic is advisory because the
// real chat setup always resolves the executable and performs a fresh ACP
// handshake; an embedded vendor process may not start until the first prompt.

import { useEffect } from "react";
import type { KeyboardEvent } from "react";

import type { AgentAdapterHealthRecord, AgentAdapterRecord } from "../../types/agent-adapter";
import { resolveExternalAgentReadiness } from "../../lib/external-agent-readiness";
import { Icon, Icons } from "./Icons";
import { focusDropdownItem, focusInitialDropdownItem } from "./dropdownKeyboard";
import { useFloatingDropdownStyle } from "./useFloatingDropdownStyle";
import { useFloatingMenu } from "./useFloatingMenu";

// adapterPickerDiagnostic combines current passive discovery with the latest
// optional diagnostic for display. Current discovery and required remote
// credentials win over stale diagnostics; failed diagnostics remain visible
// but do not disable a locally available adapter.
function adapterPickerDiagnostic(
  adapter: AgentAdapterRecord,
  health: AgentAdapterHealthRecord | null | undefined,
): { title: string; iconColor: string; chipLabel: string; chipColor: string } {
  if (adapter.remote_credential_ok === false) {
    return {
      title:
        adapter.remote_credential_hint ||
        adapter.auth_error ||
        `Configure a remote credential for ${adapter.name}`,
      iconColor: "var(--amber)",
      chipLabel: "auth",
      chipColor: "var(--amber)",
    };
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
      title: adapterAdvisoryTitle(
        adapter,
        adapter.auth_error || "Billing or usage limit requires attention.",
      ),
      iconColor: "var(--amber)",
      chipLabel: "billing",
      chipColor: "var(--amber)",
    };
  }
  if (health?.status === "auth_required" || adapter.auth_status === "unauthenticated") {
    return {
      title: adapterAdvisoryTitle(
        adapter,
        health?.hint || health?.error || adapter.auth_error || "Authentication may be required.",
      ),
      iconColor: "var(--amber)",
      chipLabel: "auth",
      chipColor: "var(--amber)",
    };
  }
  if (adapter.auth_status && adapter.auth_status !== "ok" && adapter.auth_status !== "unknown") {
    return {
      title: adapterAdvisoryTitle(
        adapter,
        adapter.auth_error || `Auth status: ${adapter.auth_status}.`,
      ),
      iconColor: "var(--amber)",
      chipLabel: "auth",
      chipColor: "var(--amber)",
    };
  }
  if (health) {
    switch (health.status) {
      case "ready":
        return {
          title: adapterCheckedTitle(adapter, health.path),
          iconColor: "var(--green)",
          chipLabel: "checked",
          chipColor: "var(--teal)",
        };
      case "auth_required":
        // Handled above so an auth diagnostic stays ahead of ready-state
        // presentation even when adapter metadata and health arrive separately.
        break;
      case "not_installed":
        return {
          title: adapterDiagnosticTitle(
            adapter,
            health.hint || health.error || `${adapter.name} command was not found`,
          ),
          iconColor: "var(--amber)",
          chipLabel: "diagnostic",
          chipColor: "var(--amber)",
        };
      case "error":
        return {
          title: adapterDiagnosticTitle(
            adapter,
            health.hint || health.error || `${adapter.name} diagnostic failed`,
          ),
          iconColor: "var(--amber)",
          chipLabel: adapterProbeLooksLikeSetupState(health) ? "diagnostic" : "issue",
          chipColor: "var(--amber)",
        };
    }
  }
  if (adapter.auth_status === "unknown") {
    return {
      title: adapterAvailableTitle(adapter, adapter.auth_error),
      iconColor: "var(--t3)",
      chipLabel: "available",
      chipColor: "var(--t3)",
    };
  }
  return {
    title: adapterAvailableTitle(adapter),
    iconColor: "var(--t3)",
    chipLabel: "available",
    chipColor: "var(--t3)",
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

function adapterCheckedTitle(adapter: AgentAdapterRecord, path: string | undefined): string {
  const suffix = path ? ` Path: ${path}` : "";
  return `The last ${adapter.name} diagnostic completed ACP startup and session checks without sending a prompt. New chat still prepares a fresh session; the first message checks any deferred prompt-serving vendor invocation and authentication.${suffix}`;
}

function adapterAvailableTitle(adapter: AgentAdapterRecord, detail?: string): string {
  const suffix = adapter.path
    ? ` Last discovered path: ${adapter.path}`
    : adapter.command
      ? ` Configured command: ${adapter.command}`
      : "";
  const action = `${adapter.name} is available. New chat re-resolves the executable and prepares its ACP session; the first message verifies any deferred prompt-serving vendor invocation and authentication.${suffix}`;
  return detail ? `${detail} ${action}` : action;
}

function adapterDiagnosticTitle(adapter: AgentAdapterRecord, detail: string): string {
  return `The last ${adapter.name} diagnostic needs attention. New chat prepares a fresh ACP session, and the first message retries any deferred prompt-serving vendor invocation; diagnostics in Connections are optional. ${detail}`;
}

function adapterAdvisoryTitle(adapter: AgentAdapterRecord, detail: string): string {
  return `${detail} New chat prepares a fresh ${adapter.name} ACP session, and the first message retries any deferred prompt-serving vendor invocation; diagnostics in Connections are optional.`;
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
  // healthByID is optional; when supplied, each row shows the most recent
  // advisory diagnostic. It never overrides current passive availability.
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
  const floatingStyle = useFloatingDropdownStyle(triggerRef, open, "left", "down", 220);

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
        aria-label="External agent"
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
        title={isEmpty ? "No external agents are registered" : disabledReason || label}
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
            const disabled = resolveExternalAgentReadiness(adapter, health ?? null).launchBlocked;
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

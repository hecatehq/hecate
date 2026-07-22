import { useEffect, useId, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import type { AgentAdapterHealthRecord, AgentAdapterRecord } from "../../types/agent-adapter";
import type { ChatConfigOptionRecord } from "../../types/chat";
import type { ModelRecord } from "../../types/model";
import type {
  ConfiguredProviderRecord,
  ProviderPresetRecord,
  ProviderRecord,
} from "../../types/provider";
import { resolveExternalAgentReadiness } from "../../lib/external-agent-readiness";
import { providerDisplayName } from "../../lib/provider-utils";
import { modelDisplayName } from "../../lib/runtime-utils";
import { BrandAvatar, DropdownPicker, Icon, Icons } from "../shared/ui";
import type { DropdownPickerOption, ProviderOption } from "../shared/ui";
import { focusDropdownItem, focusInitialDropdownItem } from "../shared/dropdownKeyboard";
import { useFloatingDropdownStyle } from "../shared/useFloatingDropdownStyle";
import { useFloatingMenu } from "../shared/useFloatingMenu";
import {
  agentConfigOptionIsInstructions,
  agentConfigOptionIsText,
  agentConfigOptionLabel,
  prioritizeAgentConfigOptions,
} from "./agentConfigOptions";

const HECATE_CHAT_AGENT_OPTION = { id: "hecate", label: "Hecate" } as const;

export type ChatAgentOptionID = "hecate" | (string & {});

export function NewChatAgentButton({
  value,
  adapters,
  healthByID,
  disableUnavailable = false,
  createTitle,
  createDisabled,
  selectionDisabled = false,
  onChange,
  onCreate,
  onSetupAgent,
}: {
  value: string;
  adapters: AgentAdapterRecord[];
  healthByID: Map<string, AgentAdapterHealthRecord>;
  disableUnavailable?: boolean;
  createTitle?: string;
  createDisabled?: boolean;
  selectionDisabled?: boolean;
  onChange: (value: ChatAgentOptionID) => void;
  onCreate: (value: ChatAgentOptionID) => void;
  onSetupAgent?: (adapterID: string) => void;
}) {
  const launchDisclosureID = useId();
  const { open, setOpen, toggle, wrapRef, triggerRef, menuRef } = useFloatingMenu<
    HTMLDivElement,
    HTMLButtonElement
  >();
  // Anchor is the inline-block group around the trigger; the
  // floating menu positions against it (not the trigger itself) so
  // the menu width matches the visual button group, not just the
  // narrow caret.
  const anchorRef = useRef<HTMLDivElement>(null);
  const floatingStyle = useFloatingDropdownStyle(anchorRef, open, "left");
  const selected = chatAgentOption(value, adapters);
  const selectedAdapter =
    selected.id === "hecate" ? undefined : adapters.find((item) => item.id === selected.id);
  const selectedHealth = selected.id === "hecate" ? undefined : healthByID.get(selected.id);
  const selectedStatus = chatAgentOptionStatus(selected.id, selectedAdapter, selectedHealth);
  const options = chatAgentPickerOptions(adapters, healthByID, disableUnavailable, 17);
  const selectedDisabled = disableUnavailable && !selectedStatus.ready;
  const effectiveSelected = selectedDisabled ? chatAgentOption("hecate", adapters) : selected;
  const effectiveAdapter =
    effectiveSelected.id === "hecate"
      ? undefined
      : adapters.find((item) => item.id === effectiveSelected.id);
  const effectiveHealth =
    effectiveSelected.id === "hecate" ? undefined : healthByID.get(effectiveSelected.id);
  const executablePath = externalAgentExecutablePath(effectiveAdapter, effectiveHealth);
  const startsExternalAgent = effectiveSelected.id !== "hecate";
  const externalAgentLaunchDisclosure = startsExternalAgent
    ? `Starts ${effectiveSelected.label}${executablePath ? ` from ${executablePath}` : ""} and opens an ACP session`
    : "";
  const createActionTitle =
    createTitle ||
    (startsExternalAgent
      ? externalAgentLaunchDisclosure
      : `Start a new ${effectiveSelected.label} chat`);

  useEffect(() => {
    if (!selectedDisabled || selected.id === "hecate") return;
    onChange("hecate");
  }, [onChange, selected.id, selectedDisabled]);

  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => focusInitialDropdownItem(menuRef.current));
    return () => cancelAnimationFrame(frame);
  }, [open, menuRef]);

  useEffect(() => {
    if (selectionDisabled) setOpen(false);
  }, [selectionDisabled, setOpen]);

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

  return (
    <div className="dropdown-wrap" ref={wrapRef} style={{ flex: 1, minWidth: 0, width: "100%" }}>
      <div
        ref={anchorRef}
        style={{
          display: "flex",
          minWidth: 0,
          overflow: "hidden",
          border: "1px solid var(--teal-border)",
          borderRadius: "var(--radius-sm)",
          background: "var(--teal)",
        }}
      >
        <button
          className="btn btn-primary btn-sm"
          disabled={createDisabled}
          type="button"
          aria-describedby={startsExternalAgent ? launchDisclosureID : undefined}
          title={createActionTitle}
          onClick={() => {
            if (createDisabled) return;
            if (selectedDisabled && effectiveSelected.id !== selected.id) {
              onChange(effectiveSelected.id);
            }
            onCreate(effectiveSelected.id);
          }}
          style={{
            flex: 1,
            minWidth: 0,
            border: 0,
            borderRadius: 0,
            justifyContent: "center",
            minHeight: 30,
            padding: "4px 12px",
            background: createDisabled ? "var(--bg3)" : "transparent",
            color: createDisabled ? "var(--t3)" : "var(--accent-fg)",
            cursor: createDisabled ? "not-allowed" : undefined,
          }}
        >
          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            New {effectiveSelected.label} chat
          </span>
        </button>
        <button
          ref={triggerRef}
          type="button"
          className="btn btn-primary btn-sm"
          aria-label="Choose agent for new chat"
          aria-expanded={open}
          aria-haspopup="listbox"
          disabled={selectionDisabled}
          onClick={() => {
            if (!selectionDisabled) toggle();
          }}
          title={selectionDisabled ? "Starting the current chat" : "Choose agent"}
          style={{
            border: 0,
            borderLeft: "1px solid oklch(0 0 100 / 0.22)",
            borderRadius: 0,
            width: 36,
            minHeight: 30,
            padding: 0,
            justifyContent: "center",
            background: selectionDisabled ? "var(--bg3)" : "oklch(0 0 0 / 0.12)",
            color: selectionDisabled ? "var(--t3)" : "var(--accent-fg)",
            cursor: selectionDisabled ? "not-allowed" : undefined,
          }}
        >
          <Icon d={Icons.chevD} size={12} />
        </button>
      </div>
      {startsExternalAgent && (
        <div
          id={launchDisclosureID}
          style={{
            color: "var(--t3)",
            fontSize: 10,
            lineHeight: 1.4,
            marginTop: 5,
            overflowWrap: "anywhere",
          }}
        >
          Starts {effectiveSelected.label}
          {executablePath ? (
            <>
              {" "}
              from <code style={{ fontFamily: "var(--font-mono)" }}>{executablePath}</code>
            </>
          ) : null}{" "}
          and opens an ACP session.
        </div>
      )}
      {open && floatingStyle && (
        <div
          ref={menuRef}
          role="listbox"
          className="dropdown-menu dropdown-menu-floating"
          onKeyDown={onMenuKeyDown}
          style={{ ...floatingStyle, minWidth: 230 }}
        >
          {options.map((option) => {
            const selectedOption = option.value === selected.id;
            const lockedOption = Boolean(option.disabled);
            const setupActionAvailable =
              lockedOption && option.value !== "hecate" && Boolean(onSetupAgent);
            return (
              <button
                key={option.value}
                type="button"
                data-dropdown-item
                data-selected={selectedOption ? "true" : undefined}
                role="option"
                aria-selected={selectedOption}
                aria-disabled={lockedOption && !setupActionAvailable ? true : undefined}
                className={`dropdown-item ${selectedOption ? "selected" : ""}`}
                onClick={() => {
                  if (lockedOption) {
                    if (option.value !== "hecate") {
                      onSetupAgent?.(option.value);
                      setOpen(false);
                    }
                    return;
                  }
                  onChange(option.value);
                  setOpen(false);
                }}
                title={option.disabledReason || option.title}
                style={
                  lockedOption
                    ? {
                        cursor: setupActionAvailable ? "pointer" : "not-allowed",
                        opacity: setupActionAvailable ? 0.72 : 0.5,
                      }
                    : undefined
                }
              >
                {option.icon}
                <span style={{ display: "grid", flex: 1, minWidth: 0 }}>
                  <span
                    style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                  >
                    {option.label}
                  </span>
                </span>
                {option.statusLabel && (
                  <span
                    style={{
                      color: option.statusColor || "var(--t3)",
                      fontFamily: "var(--font-mono)",
                      fontSize: 9,
                      letterSpacing: "0.04em",
                      textTransform: "uppercase",
                    }}
                  >
                    {option.statusLabel}
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

function externalAgentExecutablePath(
  adapter: AgentAdapterRecord | undefined,
  health: AgentAdapterHealthRecord | undefined,
): string {
  const path = health?.path?.trim() || adapter?.path?.trim() || "";
  return path.startsWith("dev-override://") ? "" : path;
}

export function ChatAgentPicker({
  value,
  adapters,
  healthByID,
  disableUnavailable = false,
  compact = false,
  onChange,
}: {
  value: string;
  adapters: AgentAdapterRecord[];
  healthByID: Map<string, AgentAdapterHealthRecord>;
  disableUnavailable?: boolean;
  compact?: boolean;
  onChange: (value: ChatAgentOptionID) => void;
}) {
  const selected = chatAgentOption(value, adapters);
  const options = chatAgentPickerOptions(
    adapters,
    healthByID,
    disableUnavailable,
    compact ? 16 : 18,
  );

  return (
    <DropdownPicker
      ariaLabel="Agent"
      value={selected.id}
      options={options}
      onChange={onChange}
      triggerIcon={
        <BrandAvatar
          brand={selected.id}
          fallback={selected.label}
          boxed={false}
          size={compact ? 16 : 18}
        />
      }
      triggerMinWidth={0}
      menuMinWidth={230}
      buttonStyle={
        compact
          ? {
              background: "transparent",
              border: 0,
              borderRadius: 6,
              color: "var(--teal)",
              fontSize: 11,
              justifyContent: "flex-start",
              minHeight: 24,
              padding: "2px 4px",
              width: "auto",
            }
          : { color: "var(--teal)", justifyContent: "space-between", width: "100%" }
      }
    />
  );
}

function chatAgentPickerOptions(
  adapters: AgentAdapterRecord[],
  healthByID: Map<string, AgentAdapterHealthRecord>,
  disableUnavailable: boolean,
  iconSize: number,
): DropdownPickerOption<ChatAgentOptionID>[] {
  const options = [
    HECATE_CHAT_AGENT_OPTION,
    ...adapters.map((adapter) => ({ id: adapter.id, label: adapter.name || adapter.id })),
  ];
  return options.map((option) => {
    const adapter =
      option.id === "hecate" ? undefined : adapters.find((item) => item.id === option.id);
    const health = option.id === "hecate" ? undefined : healthByID.get(option.id);
    const status = chatAgentOptionStatus(option.id, adapter, health);
    const disabled = disableUnavailable && !status.ready;
    return {
      value: option.id,
      label: option.label,
      title: status.title,
      disabled,
      disabledReason: disabled ? status.title : undefined,
      icon: <BrandAvatar brand={option.id} fallback={option.label} boxed={false} size={iconSize} />,
      statusLabel: status.label,
      statusColor: status.color,
    };
  });
}

export function chatAgentOption(
  value: string,
  adapters: AgentAdapterRecord[],
): { id: ChatAgentOptionID; label: string; title: string } {
  if (value !== "hecate") {
    const adapter = adapters.find((item) => item.id === value);
    if (!adapter) {
      const label = adapterDisplayName(value, undefined);
      return {
        id: value,
        label,
        title: `External agent ${label} is selected. Hecate is still loading or refreshing its agent catalog.`,
      };
    }
    return {
      id: value,
      label: adapter.name || value,
      title: adapter.description || adapter.command || adapter.name || value,
    };
  }
  return {
    id: "hecate",
    label: "Hecate",
    title: "Chat with Hecate; enable tools to create or continue a linked Task.",
  };
}

export function chatAgentOptionStatus(
  optionID: ChatAgentOptionID,
  adapter?: AgentAdapterRecord,
  health?: AgentAdapterHealthRecord,
): { label: string; color: string; title: string; ready: boolean } {
  if (optionID === "hecate") {
    return { label: "local", color: "var(--teal)", title: "Hecate Chat", ready: true };
  }
  const launchReady = !resolveExternalAgentReadiness(adapter, health ?? null).launchBlocked;
  if (!adapter?.available) {
    return {
      label: "setup",
      color: "var(--t3)",
      title: adapterSetupTitle(optionID, adapter, adapter?.error),
      ready: launchReady,
    };
  }
  if (adapter.remote_credential_ok === false) {
    return {
      label: "auth",
      color: "var(--amber)",
      title: adapterRemoteCredentialTitle(
        optionID,
        adapter,
        adapter.remote_credential_hint || adapter.auth_error,
      ),
      ready: launchReady,
    };
  }
  if (health?.status === "ready") {
    return {
      label: "checked",
      color: "var(--teal)",
      title: adapterCheckedTitle(optionID, adapter, health.path),
      ready: launchReady,
    };
  }
  if (health?.status === "auth_required") {
    return {
      label: "auth",
      color: "var(--amber)",
      title: adapterAuthSetupTitle(optionID, adapter, health.hint || health.error),
      ready: launchReady,
    };
  }
  if (health?.status === "error") {
    return {
      label: adapterProbeLooksLikeSetupState(health) ? "diagnostic" : "issue",
      color: "var(--amber)",
      title: adapterDiagnosticTitle(optionID, adapter, health.hint || health.error),
      ready: launchReady,
    };
  }
  if (health?.status === "not_installed") {
    return {
      label: "diagnostic",
      color: "var(--amber)",
      title: adapterDiagnosticTitle(optionID, adapter, health.hint || health.error),
      ready: launchReady,
    };
  }
  if (adapter.auth_status === "billing") {
    return {
      label: "billing",
      color: "var(--amber)",
      title: adapterDiagnosticTitle(optionID, adapter, adapter.auth_error),
      ready: launchReady,
    };
  }
  if (adapter.auth_status === "unauthenticated") {
    return {
      label: "auth",
      color: "var(--amber)",
      title: adapterAuthSetupTitle(optionID, adapter, adapter.auth_error),
      ready: launchReady,
    };
  }
  if (adapter.auth_status === "unknown") {
    return {
      label: "available",
      color: "var(--t3)",
      title: adapterAvailableTitle(optionID, adapter, adapter.auth_error),
      ready: launchReady,
    };
  }
  if (adapter.auth_status && adapter.auth_status !== "ok") {
    return {
      label: "issue",
      color: "var(--amber)",
      title: adapterDiagnosticTitle(
        optionID,
        adapter,
        adapter.auth_error || `Auth status: ${adapter.auth_status}`,
      ),
      ready: launchReady,
    };
  }
  return {
    label: "available",
    color: "var(--t3)",
    title: adapterAvailableTitle(optionID, adapter),
    ready: launchReady,
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

function adapterSetupTitle(
  optionID: ChatAgentOptionID,
  adapter: AgentAdapterRecord | undefined,
  detail: string | undefined,
): string {
  const name = adapterDisplayName(optionID, adapter);
  const cleanDetail = sanitizedAdapterDetail(detail);
  const action = adapterLoginCommand(optionID)
    ? `Open Connections to set up ${name}, then sign in with ${adapterLoginCommand(optionID)}.`
    : `Open Connections to set up ${name}.`;
  return cleanDetail ? `${action} ${cleanDetail}` : action;
}

function adapterAuthSetupTitle(
  optionID: ChatAgentOptionID,
  adapter: AgentAdapterRecord | undefined,
  detail: string | undefined,
): string {
  const name = adapterDisplayName(optionID, adapter);
  const command = adapterLoginCommand(optionID);
  const action = command
    ? `Run ${command} in Terminal, then retry the chat. Diagnostics in Connections are optional.`
    : `Open Connections to sign in to ${name}.`;
  const cleanDetail = sanitizedAdapterDetail(detail);
  return cleanDetail ? `${action} ${cleanDetail}` : action;
}

function adapterRemoteCredentialTitle(
  optionID: ChatAgentOptionID,
  adapter: AgentAdapterRecord,
  detail: string | undefined,
): string {
  const name = adapterDisplayName(optionID, adapter);
  const cleanDetail = sanitizedAdapterDetail(detail);
  const action = `Open Connections to configure the required remote credential for ${name}.`;
  return cleanDetail ? `${action} ${cleanDetail}` : action;
}

function adapterDiagnosticTitle(
  optionID: ChatAgentOptionID,
  adapter: AgentAdapterRecord | undefined,
  detail: string | undefined,
): string {
  const name = adapterDisplayName(optionID, adapter);
  const cleanDetail = sanitizedAdapterDetail(detail);
  const action = `The last ${name} diagnostic needs attention. Starting a chat retries the current ACP launch; use Connections for optional diagnostics.`;
  return cleanDetail ? `${action} ${cleanDetail}` : action;
}

function sanitizedAdapterDetail(detail: string | undefined): string {
  const trimmed = detail?.trim() ?? "";
  if (!trimmed) return "";
  if (trimmed.includes("HECATE_AGENT_ADAPTER_DEV_OVERRIDES")) return "";
  if (trimmed.startsWith("forced ")) return "";
  return trimmed;
}

function adapterLoginCommand(optionID: ChatAgentOptionID): string {
  switch (optionID) {
    case "codex":
      return "codex login";
    case "claude_code":
      return "claude /login";
    case "cursor_agent":
      return "cursor-agent login";
    case "grok_build":
      return "grok login";
    default:
      return "";
  }
}

function adapterDisplayName(optionID: ChatAgentOptionID, adapter?: AgentAdapterRecord): string {
  return (
    adapter?.name ||
    (optionID === "hecate" ? HECATE_CHAT_AGENT_OPTION.label : humanizeAgentID(optionID)) ||
    "External agent"
  );
}

function humanizeAgentID(value: string): string {
  return value
    .trim()
    .split(/[_\s-]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function adapterCheckedTitle(
  optionID: ChatAgentOptionID,
  adapter: AgentAdapterRecord | undefined,
  path: string | undefined,
): string {
  const name = adapterDisplayName(optionID, adapter);
  const suffix = path ? ` Path: ${path}` : "";
  return `The last ${name} diagnostic passed startup, auth, and ACP session creation. Starting a chat still performs a fresh launch.${suffix}`;
}

function adapterAvailableTitle(
  optionID: ChatAgentOptionID,
  adapter: AgentAdapterRecord,
  detail?: string,
): string {
  const name = adapterDisplayName(optionID, adapter);
  const command = adapter.path || adapter.command;
  const suffix = command ? ` Command: ${command}` : "";
  const cleanDetail = sanitizedAdapterDetail(detail);
  const action = `${name} is available. Starting a chat launches it and verifies the ACP connection.${suffix}`;
  return cleanDetail ? `${cleanDetail} ${action}` : action;
}

export function HecateToolsToggle({
  enabled,
  toolsDisabledForModel,
  onChange,
}: {
  enabled: boolean;
  toolsDisabledForModel: boolean;
  onChange: (enabled: boolean) => void;
}) {
  const effectiveEnabled = enabled && !toolsDisabledForModel;
  const toolsOnTitle = toolsDisabledForModel
    ? "This model does not report tool-calling support. Hecate will send messages as direct model chat until you choose a tool-capable model."
    : "Create or continue a linked Task with tools, approvals, artifacts, and telemetry.";
  const title =
    enabled && toolsDisabledForModel
      ? toolsOnTitle
      : effectiveEnabled
        ? toolsOnTitle
        : "Chat directly with the selected model. This does not create a Task or use tools.";
  return (
    <div
      role="group"
      aria-label="Hecate tools"
      style={{
        display: "inline-flex",
        alignItems: "center",
        border: "1px solid var(--border)",
        borderRadius: "999px",
        flexShrink: 0,
        background: "var(--bg0)",
        gap: 6,
        height: 30,
        padding: "0 5px 0 11px",
      }}
    >
      <span
        style={{
          color: "var(--t3)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          whiteSpace: "nowrap",
        }}
      >
        tools:
      </span>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        aria-label={effectiveEnabled ? "tools on" : "tools off"}
        aria-pressed={effectiveEnabled}
        onClick={() => onChange(!enabled)}
        title={title}
        style={{
          border: 0,
          borderRadius: "999px",
          minWidth: 38,
          padding: "3px 9px",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          background: effectiveEnabled ? "var(--teal-bg)" : "var(--bg3)",
          color: enabled && toolsDisabledForModel ? "var(--amber)" : "var(--t1)",
          justifyContent: "center",
        }}
      >
        {effectiveEnabled ? "on" : "off"}
      </button>
    </div>
  );
}

export function ExternalAgentConfigControls({
  session,
  onChange,
  placement = "header",
  disabled = false,
}: {
  session: { id?: string; agent_id?: string; config_options?: ChatConfigOptionRecord[] } | null;
  onChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
  placement?: "header" | "composer";
  disabled?: boolean;
}) {
  if (
    !session?.id ||
    !session.agent_id ||
    session.agent_id === "hecate" ||
    !session.config_options?.length
  ) {
    return null;
  }
  const controls = prioritizeAgentConfigOptions(session.config_options)
    .filter((option) => !agentConfigOptionIsText(option))
    .slice(0, 4);
  if (controls.length === 0) {
    return null;
  }
  return (
    <div
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 6,
        flexShrink: 1,
        minWidth: 0,
        flexWrap: placement === "composer" ? "wrap" : "nowrap",
      }}
    >
      {controls.map((option) => (
        <ExternalAgentConfigControl
          key={option.id}
          sessionID={session.id!}
          option={option}
          disabled={disabled}
          onChange={onChange}
          menuPlacement={placement === "composer" ? "up" : "down"}
        />
      ))}
    </div>
  );
}

export function ExternalAgentSettingsControls({
  session,
  onChange,
  disabled = false,
}: {
  session: { id?: string; agent_id?: string; config_options?: ChatConfigOptionRecord[] } | null;
  onChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
  disabled?: boolean;
}) {
  if (
    !session?.id ||
    !session.agent_id ||
    session.agent_id === "hecate" ||
    !session.config_options?.length
  ) {
    return null;
  }
  const controls = prioritizeAgentConfigOptions(session.config_options).filter((option) =>
    agentConfigOptionIsText(option),
  );
  if (controls.length === 0) {
    return null;
  }
  return (
    <div style={{ display: "grid", gap: 8 }}>
      {controls.map((option) => (
        <ExternalAgentSettingsControl
          key={option.id}
          sessionID={session.id!}
          option={option}
          disabled={disabled}
          onChange={onChange}
        />
      ))}
    </div>
  );
}

export function HecateProviderConfigControl({
  value,
  options,
  placement = "up",
  onChange,
}: {
  value: string;
  options: ProviderOption[];
  placement?: "up" | "down";
  onChange: (value: string) => void;
}) {
  const pickerOptions: DropdownPickerOption<string>[] = options.map((option) => ({
    value: option.id,
    label: option.name,
    title: option.disabledReason || option.name,
    disabled: Boolean(option.disabledReason),
    disabledReason: option.disabledReason,
    icon: <BrandAvatar brand={option.id} fallback={option.name} boxed={false} size={16} />,
    statusLabel: option.disabledReason ? "setup" : undefined,
    statusColor: option.disabledReason ? "var(--amber)" : undefined,
  }));
  const selected = pickerOptions.find((option) => option.value === value);
  return (
    <DropdownPicker
      ariaLabel={`Provider picker: ${selected?.label || "Select provider"}`}
      value={value}
      options={pickerOptions}
      onChange={onChange}
      placeholder="Select provider"
      triggerPrefix="provider"
      triggerMinWidth={150}
      triggerMaxWidth={260}
      menuMinWidth={230}
      placement={placement}
      buttonStyle={externalAgentComposerControlStyle}
    />
  );
}

export function HecateModelConfigControl({
  value,
  models,
  presets,
  configuredProviders,
  runtimeProviders,
  disabledProviders,
  showProvider,
  placement = "up",
  onChange,
}: {
  value: string;
  models: ModelRecord[];
  presets?: ProviderPresetRecord[];
  configuredProviders?: ConfiguredProviderRecord[];
  runtimeProviders?: ProviderRecord[];
  disabledProviders?: Map<string, string>;
  showProvider?: boolean;
  placement?: "up" | "down";
  onChange: (value: string) => void;
}) {
  const providerName = (id: string) =>
    providerDisplayName(id, configuredProviders, presets, runtimeProviders);
  const pickerOptions: DropdownPickerOption<string>[] = [
    { value: "", label: "Pick a model", title: "No model selected" },
    ...models.map((model) => {
      const provider = typeof model.metadata?.provider === "string" ? model.metadata.provider : "";
      const disabledReason = provider ? disabledProviders?.get(provider) : undefined;
      return {
        value: model.id,
        label: modelDisplayName(model.id),
        detail: showProvider && provider ? providerName(provider) : undefined,
        title: disabledReason || model.id,
        disabled: Boolean(disabledReason),
        disabledReason,
        statusLabel: disabledReason ? "setup" : undefined,
        statusColor: disabledReason ? "var(--amber)" : undefined,
      };
    }),
  ];
  const selected = pickerOptions.find((option) => option.value === value);
  return (
    <DropdownPicker
      ariaLabel={`Model picker: ${selected?.label || value || "Pick a model"}`}
      value={value}
      options={pickerOptions}
      onChange={onChange}
      placeholder={models.length > 0 ? "Pick a model" : "no models available"}
      triggerPrefix="model"
      triggerMinWidth={150}
      triggerMaxWidth={300}
      menuMinWidth={300}
      align="right"
      placement={placement}
      searchable
      searchPlaceholder="Filter models..."
      disabled={models.length === 0}
      disabledReason="No discovered models for this provider. Configure credentials or start the local runtime."
      buttonStyle={externalAgentComposerControlStyle}
    />
  );
}

const externalAgentComposerControlStyle = {
  background: "var(--bg1)",
  borderColor: "var(--border)",
  flexShrink: 1,
} as const;

function ExternalAgentConfigControl({
  sessionID,
  option,
  onChange,
  menuPlacement = "down",
  disabled = false,
}: {
  sessionID: string;
  option: ChatConfigOptionRecord;
  onChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
  menuPlacement?: "down" | "up";
  disabled?: boolean;
}) {
  const label = agentConfigOptionLabel(option);
  const title = [option.name, option.description].filter(Boolean).join(" · ");
  if (option.type === "boolean") {
    const checked = Boolean(option.current_bool);
    return (
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        aria-pressed={checked}
        disabled={disabled}
        title={title || label}
        onClick={() => void onChange(sessionID, option.id, !checked)}
        style={{
          flexShrink: 0,
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          color: checked ? "var(--teal)" : "var(--t3)",
          borderColor: checked ? "var(--teal-border)" : "var(--border)",
          background: checked ? "var(--teal-bg)" : "transparent",
        }}
      >
        {label}: {checked ? "on" : "off"}
      </button>
    );
  }
  if (option.type !== "select" || !option.options?.length) {
    return null;
  }
  const options: DropdownPickerOption<string>[] = option.options.map((item) => ({
    value: item.value,
    label: item.group_name ? `${item.group_name} · ${item.name}` : item.name,
    detail: item.description,
    title: item.description || item.name,
  }));
  return (
    <DropdownPicker
      ariaLabel={option.name}
      value={option.current_value ?? ""}
      options={options}
      onChange={(value) => void onChange(sessionID, option.id, value)}
      disabled={disabled}
      disabledReason="Wait for Stop to finish before changing agent settings."
      placeholder={option.name}
      triggerPrefix={label}
      triggerMinWidth={150}
      triggerMaxWidth={230}
      menuMinWidth={230}
      placement={menuPlacement}
      buttonStyle={{
        ...externalAgentComposerControlStyle,
      }}
    />
  );
}

function ExternalAgentSettingsControl({
  sessionID,
  option,
  onChange,
  disabled = false,
}: {
  sessionID: string;
  option: ChatConfigOptionRecord;
  onChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
  disabled?: boolean;
}) {
  if (agentConfigOptionIsText(option)) {
    return (
      <ExternalAgentTextConfigControl
        sessionID={sessionID}
        option={option}
        onChange={onChange}
        disabled={disabled}
      />
    );
  }
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 12,
        background: "var(--bg1)",
        padding: 12,
        display: "grid",
        gap: 10,
      }}
    >
      <div style={{ display: "grid", gap: 4 }}>
        <div style={{ color: "var(--t0)", fontSize: 12, fontWeight: 650 }}>
          {option.name || option.id}
        </div>
        {option.description && (
          <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.45 }}>
            {option.description}
          </div>
        )}
      </div>
      <ExternalAgentConfigControl
        sessionID={sessionID}
        option={option}
        onChange={onChange}
        disabled={disabled}
      />
    </div>
  );
}

function ExternalAgentTextConfigControl({
  sessionID,
  option,
  onChange,
  disabled = false,
}: {
  sessionID: string;
  option: ChatConfigOptionRecord;
  onChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
  disabled?: boolean;
}) {
  const [textValue, setTextValue] = useState(option.current_value ?? "");
  const [saving, setSaving] = useState(false);
  const cleanValue = textValue.trim();
  const cleanCurrent = (option.current_value ?? "").trim();
  const changed = cleanValue !== cleanCurrent;
  const label = agentConfigOptionIsInstructions(option)
    ? "System prompt / instructions"
    : option.name || option.id;

  useEffect(() => {
    setTextValue(option.current_value ?? "");
  }, [option.current_value, option.id]);

  async function save() {
    if (!changed || saving) return;
    setSaving(true);
    try {
      const ok = await onChange(sessionID, option.id, textValue);
      if (!ok) {
        setTextValue(option.current_value ?? "");
      }
    } finally {
      setSaving(false);
    }
  }

  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 12,
        background: "var(--bg1)",
        padding: 12,
        display: "grid",
        gap: 10,
      }}
    >
      <div style={{ display: "grid", gap: 4 }}>
        <div style={{ color: "var(--t0)", fontSize: 12, fontWeight: 650 }}>{label}</div>
        <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.45 }}>
          {option.description ||
            "Agent-provided text setting for future messages in this External Agent chat."}
        </div>
      </div>
      <textarea
        aria-label={label}
        value={textValue}
        disabled={disabled}
        onChange={(event) => setTextValue(event.target.value)}
        rows={4}
        style={{
          width: "100%",
          resize: "vertical",
          minHeight: 86,
          background: "var(--bg2)",
          border: "1px solid var(--border)",
          borderRadius: 8,
          color: "var(--t0)",
          fontFamily: "var(--font-sans)",
          fontSize: 12,
          lineHeight: 1.5,
          padding: "9px 10px",
          outline: "none",
        }}
      />
      <button
        type="button"
        className="btn btn-primary btn-sm"
        disabled={disabled || !changed || saving}
        onClick={() => void save()}
        style={{ justifySelf: "start" }}
      >
        {saving ? "Saving..." : "Save"}
      </button>
    </div>
  );
}

export function LockedHecateModelSnapshot({
  provider,
  model,
}: {
  provider: string;
  model: string;
}) {
  const title =
    "Provider and model are fixed for this task-backed turn. For direct model chat, disable tools, or start a new chat to use a different tools-enabled model.";
  const lockedStyle = {
    ...externalAgentComposerControlStyle,
    color: "var(--t2)",
    cursor: "not-allowed",
    fontFamily: "var(--font-mono)",
    fontSize: 11,
    gap: 6,
    justifyContent: "space-between",
    opacity: 0.82,
  } as const;
  return (
    <>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        disabled
        aria-label={`Fixed provider: ${provider}`}
        title={title}
        style={{ ...lockedStyle, minWidth: 150, maxWidth: 260 }}
      >
        <span style={{ display: "inline-flex", alignItems: "center", gap: 6, minWidth: 0 }}>
          <span
            style={{
              color: "var(--t3)",
              fontSize: 10,
              textTransform: "lowercase",
              whiteSpace: "nowrap",
            }}
          >
            provider
          </span>
          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {provider}
          </span>
        </span>
      </button>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        disabled
        aria-label={`Fixed model: ${model || "model"}`}
        title={title}
        style={{ ...lockedStyle, minWidth: 150, maxWidth: 300 }}
      >
        <span style={{ display: "inline-flex", alignItems: "center", gap: 6, minWidth: 0 }}>
          <span
            style={{
              color: "var(--t3)",
              fontSize: 10,
              textTransform: "lowercase",
              whiteSpace: "nowrap",
            }}
          >
            model
          </span>
          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {model || "model"}
          </span>
        </span>
      </button>
    </>
  );
}

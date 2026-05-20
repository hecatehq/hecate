import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import type { AgentAdapterHealthRecord, AgentAdapterRecord } from "../../types/agent-adapter";
import type { ChatConfigOptionRecord } from "../../types/chat";
import type { ModelRecord } from "../../types/model";
import type { ProviderPresetRecord } from "../../types/provider";
import { BrandAvatar, DropdownPicker, Icon, Icons } from "../shared/ui";
import type { DropdownPickerOption, ProviderOption } from "../shared/ui";
import { focusDropdownItem, focusInitialDropdownItem } from "../shared/dropdownKeyboard";
import { useFloatingDropdownStyle } from "../shared/useFloatingDropdownStyle";
import { useFloatingMenu } from "../shared/useFloatingMenu";

const CHAT_AGENT_OPTIONS = [
  { id: "hecate", label: "Hecate" },
  { id: "codex", label: "Codex" },
  { id: "claude_code", label: "Claude Code" },
  { id: "cursor_agent", label: "Cursor Agent" },
] as const;

export type ChatAgentOptionID = (typeof CHAT_AGENT_OPTIONS)[number]["id"];

export function NewChatAgentButton({
  value,
  adapters,
  healthByID,
  disableUnavailable = false,
  onChange,
  onCreate,
}: {
  value: string;
  adapters: AgentAdapterRecord[];
  healthByID: Map<string, AgentAdapterHealthRecord>;
  disableUnavailable?: boolean;
  onChange: (value: ChatAgentOptionID) => void;
  onCreate: (value: ChatAgentOptionID) => void;
}) {
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

  useEffect(() => {
    if (!selectedDisabled || selected.id === "hecate") return;
    onChange("hecate");
  }, [onChange, selected.id, selectedDisabled]);

  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => focusInitialDropdownItem(menuRef.current));
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
          type="button"
          title={`Start a new ${effectiveSelected.label} chat`}
          onClick={() => {
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
            background: "transparent",
            color: "var(--accent-fg)",
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
          onClick={() => toggle()}
          title="Choose agent"
          style={{
            border: 0,
            borderLeft: "1px solid oklch(0 0 100 / 0.22)",
            borderRadius: 0,
            width: 36,
            minHeight: 30,
            padding: 0,
            justifyContent: "center",
            background: "oklch(0 0 0 / 0.12)",
            color: "var(--accent-fg)",
          }}
        >
          <Icon d={Icons.chevD} size={12} />
        </button>
      </div>
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
            return (
              <button
                key={option.value}
                type="button"
                data-dropdown-item
                data-selected={selectedOption ? "true" : undefined}
                role="option"
                aria-selected={selectedOption}
                aria-disabled={lockedOption || undefined}
                className={`dropdown-item ${selectedOption ? "selected" : ""}`}
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
  return CHAT_AGENT_OPTIONS.map((option) => {
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
  if (value === "codex" || value === "claude_code" || value === "cursor_agent") {
    const hardcoded = CHAT_AGENT_OPTIONS.find((option) => option.id === value);
    const adapter = adapters.find((item) => item.id === value);
    return {
      id: value,
      label: adapter?.name || hardcoded?.label || value,
      title: adapter?.description || adapter?.command || hardcoded?.label || value,
    };
  }
  return {
    id: "hecate",
    label: "Hecate",
    title: "Chat with Hecate; enable tools to use Hecate's task runtime.",
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
  if (health?.status === "ready") {
    return {
      label: "ready",
      color: "var(--teal)",
      title: adapterReadyTitle(optionID, adapter, health.path),
      ready: true,
    };
  }
  if (health?.status === "auth_required") {
    return {
      label: "auth",
      color: "var(--amber)",
      title: adapterAuthSetupTitle(optionID, adapter, health.hint || health.error),
      ready: false,
    };
  }
  if (health?.status === "error") {
    if (adapterProbeLooksLikeSetupState(health)) {
      return {
        label: "setup",
        color: "var(--t3)",
        title: adapterSetupTitle(optionID, adapter, health.hint || health.error),
        ready: false,
      };
    }
    return {
      label: "issue",
      color: "var(--amber)",
      title: adapterIssueTitle(optionID, adapter, health.hint || health.error),
      ready: false,
    };
  }
  if (health?.status === "not_installed") {
    return {
      label: "setup",
      color: "var(--t3)",
      title: adapterSetupTitle(optionID, adapter, health.hint || health.error),
      ready: false,
    };
  }
  if (!adapter?.available) {
    return {
      label: "setup",
      color: "var(--t3)",
      title: adapterSetupTitle(optionID, adapter, adapter?.error),
      ready: false,
    };
  }
  if (adapter.auth_status === "billing") {
    return {
      label: "billing",
      color: "var(--amber)",
      title: adapterIssueTitle(optionID, adapter, adapter.auth_error),
      ready: false,
    };
  }
  if (adapter.auth_status === "unauthenticated") {
    return {
      label: "auth",
      color: "var(--amber)",
      title: adapterAuthSetupTitle(optionID, adapter, adapter.auth_error),
      ready: false,
    };
  }
  if (adapter.auth_status === "unknown") {
    return {
      label: "check",
      color: "var(--t3)",
      title:
        adapter.auth_error || "Auth has not been verified yet. Test this adapter in Connections.",
      ready: true,
    };
  }
  if (adapter.auth_status && adapter.auth_status !== "ok") {
    return {
      label: "issue",
      color: "var(--amber)",
      title: adapterIssueTitle(
        optionID,
        adapter,
        adapter.auth_error || `Auth status: ${adapter.auth_status}`,
      ),
      ready: false,
    };
  }
  return {
    label: "ready",
    color: "var(--teal)",
    title: adapterAvailableTitle(optionID, adapter),
    ready: true,
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
    ? `Run ${command} in Terminal, then test ${name} again in Connections.`
    : `Open Connections to sign in to ${name}.`;
  const cleanDetail = sanitizedAdapterDetail(detail);
  return cleanDetail ? `${action} ${cleanDetail}` : action;
}

function adapterIssueTitle(
  optionID: ChatAgentOptionID,
  adapter: AgentAdapterRecord | undefined,
  detail: string | undefined,
): string {
  const name = adapterDisplayName(optionID, adapter);
  const cleanDetail = sanitizedAdapterDetail(detail);
  return cleanDetail
    ? `Open Connections to inspect ${name}. ${cleanDetail}`
    : `Open Connections to inspect ${name}.`;
}

function sanitizedAdapterDetail(detail: string | undefined): string {
  const trimmed = detail?.trim() ?? "";
  if (!trimmed) return "";
  if (trimmed.includes("GATEWAY_AGENT_ADAPTER_DEV_OVERRIDES")) return "";
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
    default:
      return "";
  }
}

function adapterDisplayName(optionID: ChatAgentOptionID, adapter?: AgentAdapterRecord): string {
  return (
    adapter?.name ||
    CHAT_AGENT_OPTIONS.find((option) => option.id === optionID)?.label ||
    "External agent"
  );
}

function adapterReadyTitle(
  optionID: ChatAgentOptionID,
  adapter: AgentAdapterRecord | undefined,
  path: string | undefined,
): string {
  const name = adapterDisplayName(optionID, adapter);
  const suffix = path ? ` Path: ${path}` : "";
  return `${name} is ready. Hecate verified adapter startup, auth, and ACP session creation.${suffix}`;
}

function adapterAvailableTitle(optionID: ChatAgentOptionID, adapter: AgentAdapterRecord): string {
  const name = adapterDisplayName(optionID, adapter);
  const command = adapter.path || adapter.command;
  const suffix = command ? ` Command: ${command}` : "";
  return `${name} is available. Hecate found the adapter and local auth looks configured; open Connections to run the full ACP readiness check.${suffix}`;
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
    : "Use Hecate's task runtime with tools, approvals, artifacts, and telemetry.";
  const title =
    enabled && toolsDisabledForModel
      ? toolsOnTitle
      : effectiveEnabled
        ? toolsOnTitle
        : "Chat directly with the selected model. No task run or tools.";
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
}: {
  session: { id?: string; agent_id?: string; config_options?: ChatConfigOptionRecord[] } | null;
  onChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
  placement?: "header" | "composer";
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
    .slice(0, 3);
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
}: {
  session: { id?: string; agent_id?: string; config_options?: ChatConfigOptionRecord[] } | null;
  onChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
}) {
  if (
    !session?.id ||
    !session.agent_id ||
    session.agent_id === "hecate" ||
    !session.config_options?.length
  ) {
    return null;
  }
  const controls = prioritizeAgentConfigOptions(session.config_options);
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
          onChange={onChange}
        />
      ))}
    </div>
  );
}

export function HecateProviderConfigControl({
  value,
  options,
  onChange,
}: {
  value: string;
  options: ProviderOption[];
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
      placement="up"
      buttonStyle={externalAgentComposerControlStyle}
    />
  );
}

export function HecateModelConfigControl({
  value,
  models,
  presets,
  disabledProviders,
  showProvider,
  onChange,
}: {
  value: string;
  models: ModelRecord[];
  presets?: ProviderPresetRecord[];
  disabledProviders?: Map<string, string>;
  showProvider?: boolean;
  onChange: (value: string) => void;
}) {
  const providerName = (id: string) => presets?.find((preset) => preset.id === id)?.name || id;
  const pickerOptions: DropdownPickerOption<string>[] = models.map((model) => {
    const provider = typeof model.metadata?.provider === "string" ? model.metadata.provider : "";
    const disabledReason = provider ? disabledProviders?.get(provider) : undefined;
    return {
      value: model.id,
      label: model.id,
      detail: showProvider && provider ? providerName(provider) : undefined,
      title: disabledReason || model.id,
      disabled: Boolean(disabledReason),
      disabledReason,
      statusLabel: disabledReason ? "setup" : undefined,
      statusColor: disabledReason ? "var(--amber)" : undefined,
    };
  });
  const selected = pickerOptions.find((option) => option.value === value);
  return (
    <DropdownPicker
      ariaLabel={`Model picker: ${selected?.label || value || "model"}`}
      value={value}
      options={pickerOptions}
      onChange={onChange}
      placeholder={models.length > 0 ? "model" : "no models available"}
      triggerPrefix="model"
      triggerMinWidth={150}
      triggerMaxWidth={300}
      menuMinWidth={300}
      align="right"
      placement="up"
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
}: {
  sessionID: string;
  option: ChatConfigOptionRecord;
  onChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
  menuPlacement?: "down" | "up";
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
}: {
  sessionID: string;
  option: ChatConfigOptionRecord;
  onChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
}) {
  if (agentConfigOptionIsText(option)) {
    return (
      <ExternalAgentTextConfigControl sessionID={sessionID} option={option} onChange={onChange} />
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
      <ExternalAgentConfigControl sessionID={sessionID} option={option} onChange={onChange} />
    </div>
  );
}

function ExternalAgentTextConfigControl({
  sessionID,
  option,
  onChange,
}: {
  sessionID: string;
  option: ChatConfigOptionRecord;
  onChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
}) {
  const [draft, setDraft] = useState(option.current_value ?? "");
  const [saving, setSaving] = useState(false);
  const cleanDraft = draft.trim();
  const cleanCurrent = (option.current_value ?? "").trim();
  const changed = cleanDraft !== cleanCurrent;
  const label = agentConfigOptionIsInstructions(option)
    ? "System prompt / instructions"
    : option.name || option.id;

  useEffect(() => {
    setDraft(option.current_value ?? "");
  }, [option.current_value, option.id]);

  async function save() {
    if (!changed || saving) return;
    setSaving(true);
    try {
      const ok = await onChange(sessionID, option.id, draft);
      if (!ok) {
        setDraft(option.current_value ?? "");
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
            "Adapter-provided text setting for future turns in this external-agent session."}
        </div>
      </div>
      <textarea
        aria-label={label}
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
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
        disabled={!changed || saving}
        onClick={() => void save()}
        style={{ justifySelf: "start" }}
      >
        {saving ? "Saving..." : "Save"}
      </button>
    </div>
  );
}

function prioritizeAgentConfigOptions(options: ChatConfigOptionRecord[]): ChatConfigOptionRecord[] {
  const priority = (option: ChatConfigOptionRecord) => {
    if (agentConfigOptionIsInstructions(option)) return -1;
    switch (option.category) {
      case "model":
        return 0;
      case "thought_level":
        return 1;
      case "mode":
        return 2;
      default:
        return 3;
    }
  };
  return [...options].sort((a, b) => priority(a) - priority(b) || a.name.localeCompare(b.name));
}

function agentConfigOptionIsText(option: ChatConfigOptionRecord): boolean {
  const type = option.type.toLowerCase();
  return (
    type === "text" ||
    type === "textarea" ||
    type === "string" ||
    type === "prompt" ||
    type === "multiline"
  );
}

function agentConfigOptionIsInstructions(option: ChatConfigOptionRecord): boolean {
  const key = `${option.id} ${option.name} ${option.category ?? ""}`.toLowerCase();
  return (
    key.includes("system_prompt") ||
    key.includes("system prompt") ||
    key.includes("agent_instructions") ||
    key.includes("agent instructions") ||
    key.includes("instructions")
  );
}

function agentConfigOptionLabel(option: ChatConfigOptionRecord): string {
  if (agentConfigOptionIsInstructions(option)) return "instructions";
  switch (option.category) {
    case "model":
      return "model";
    case "thought_level":
      return "reasoning";
    case "mode":
      return "mode";
    default:
      return option.name || option.id;
  }
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

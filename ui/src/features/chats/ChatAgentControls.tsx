import type { AgentAdapterHealthRecord, AgentAdapterRecord, AgentChatConfigOptionRecord } from "../../types/runtime";
import { BrandAvatar, DropdownPicker, Icon, Icons } from "../shared/ui";
import type { DropdownPickerOption } from "../shared/ui";

const CHAT_AGENT_OPTIONS = [
  { id: "hecate", label: "Hecate" },
  { id: "codex", label: "Codex" },
  { id: "claude_code", label: "Claude Code" },
  { id: "cursor_agent", label: "Cursor" },
] as const;

export type ChatAgentOptionID = typeof CHAT_AGENT_OPTIONS[number]["id"];

export function ChatAgentPicker({
  value,
  adapters,
  healthByID,
  disableUnavailable = false,
  onChange,
}: {
  value: string;
  adapters: AgentAdapterRecord[];
  healthByID: Map<string, AgentAdapterHealthRecord>;
  disableUnavailable?: boolean;
  onChange: (value: ChatAgentOptionID) => void;
}) {
  const selected = chatAgentOption(value, adapters);
  const options: DropdownPickerOption<ChatAgentOptionID>[] = CHAT_AGENT_OPTIONS.map((option) => {
    const adapter = option.id === "hecate" ? undefined : adapters.find((item) => item.id === option.id);
    const health = option.id === "hecate" ? undefined : healthByID.get(option.id);
    const status = chatAgentOptionStatus(option.id, adapter, health);
    const disabled = disableUnavailable && !status.ready;
    return {
      value: option.id,
      label: option.label,
      title: status.title,
      disabled,
      icon: <BrandAvatar brand={option.id} fallback={option.label} boxed={false} size={18} />,
      statusLabel: status.label,
      statusColor: status.color,
    };
  });

  return (
    <DropdownPicker
      ariaLabel="Agent"
      value={selected.id}
      options={options}
      onChange={onChange}
      triggerIcon={<BrandAvatar brand={selected.id} fallback={selected.label} boxed={false} size={18} />}
      triggerMinWidth={0}
      menuMinWidth={230}
      buttonStyle={{ color: "var(--teal)", justifyContent: "space-between", width: "100%" }}
    />
  );
}

export function chatAgentOption(value: string, adapters: AgentAdapterRecord[]): { id: ChatAgentOptionID; label: string; title: string } {
  if (value === "codex" || value === "claude_code" || value === "cursor_agent") {
    const hardcoded = CHAT_AGENT_OPTIONS.find((option) => option.id === value);
    const adapter = adapters.find((item) => item.id === value);
    return {
      id: value,
      label: adapter?.name || hardcoded?.label || value,
      title: adapter?.description || adapter?.command || hardcoded?.label || value,
    };
  }
  return { id: "hecate", label: "Hecate", title: "Chat with Hecate; enable tools to use Hecate's task runtime." };
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
    return { label: "ready", color: "var(--teal)", title: health.path || "Ready", ready: true };
  }
  if (health?.status === "auth_required") {
    return { label: "auth", color: "var(--amber)", title: health.hint || health.error || "Authentication required", ready: false };
  }
  if (health?.status === "error") {
    return { label: "error", color: "var(--red)", title: health.error || "Probe failed", ready: false };
  }
  if (!adapter?.available) {
    return { label: "setup", color: "var(--amber)", title: adapter?.error || `${CHAT_AGENT_OPTIONS.find((option) => option.id === optionID)?.label || "Agent"} is not installed`, ready: false };
  }
  if (adapter.auth_status && adapter.auth_status !== "ok") {
    return { label: "auth", color: "var(--amber)", title: adapter.auth_error || `Auth status: ${adapter.auth_status}`, ready: false };
  }
  return { label: "ready", color: "var(--teal)", title: adapter.path || adapter.command || "Ready", ready: true };
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
  const toolsOnTitle = toolsDisabledForModel
    ? "Tools are disabled for this model in Settings. Enable them there or turn tools off for direct model chat."
    : "Use Hecate's task runtime with tools, approvals, artifacts, and telemetry.";
  const title = enabled ? toolsOnTitle : "Chat directly with the selected model. No task run or tools.";
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
      <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 10, whiteSpace: "nowrap" }}>
        tools:
      </span>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        aria-label={enabled ? "tools on" : "tools off"}
        aria-pressed={enabled}
        onClick={() => onChange(!enabled)}
        title={title}
        style={{
          border: 0,
          borderRadius: "999px",
          minWidth: 38,
          padding: "3px 9px",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          background: enabled ? "var(--teal-bg)" : "var(--bg3)",
          color: enabled ? (toolsDisabledForModel ? "var(--amber)" : "var(--teal)") : "var(--t1)",
          justifyContent: "center",
        }}
      >
        {enabled ? "on" : "off"}
      </button>
    </div>
  );
}

export function ExternalAgentConfigControls({
  session,
  onChange,
}: {
  session: { id?: string; runtime_kind?: string; config_options?: AgentChatConfigOptionRecord[] } | null;
  onChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
}) {
  if (!session?.id || session.runtime_kind !== "external_agent" || !session.config_options?.length) {
    return null;
  }
  const controls = prioritizeAgentConfigOptions(session.config_options).slice(0, 3);
  if (controls.length === 0) {
    return null;
  }
  return (
    <div style={{ display: "inline-flex", alignItems: "center", gap: 6, flexShrink: 1, minWidth: 0 }}>
      {controls.map((option) => (
        <ExternalAgentConfigControl
          key={option.id}
          sessionID={session.id!}
          option={option}
          onChange={onChange}
        />
      ))}
    </div>
  );
}

function ExternalAgentConfigControl({
  sessionID,
  option,
  onChange,
}: {
  sessionID: string;
  option: AgentChatConfigOptionRecord;
  onChange: (sessionID: string, configID: string, value: string | boolean) => Promise<boolean>;
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
      buttonStyle={{
        background: "var(--bg1)",
        borderColor: "var(--border)",
        flexShrink: 1,
      }}
    />
  );
}

function prioritizeAgentConfigOptions(options: AgentChatConfigOptionRecord[]): AgentChatConfigOptionRecord[] {
  const priority = (option: AgentChatConfigOptionRecord) => {
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

function agentConfigOptionLabel(option: AgentChatConfigOptionRecord): string {
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
  const title = "Provider and model are fixed for this Hecate Agent task. Turn tools off for direct model chat, or start a new chat to use a different tools-enabled model.";
  const sharedStyle = {
    fontFamily: "var(--font-mono)",
    fontSize: 11,
    gap: 5,
    color: "var(--t2)",
    opacity: 0.78,
    cursor: "not-allowed",
  } as const;
  return (
    <>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        disabled
        aria-label={`Fixed provider: ${provider}`}
        title={title}
        style={{ ...sharedStyle, width: 220 }}
      >
        <Icon d={Icons.providers} size={13} />
        <span style={{ flex: 1, minWidth: 0, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", textAlign: "left" }}>
          {provider}
        </span>
      </button>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        disabled
        aria-label={`Fixed model: ${model || "model"}`}
        title={title}
        style={{ ...sharedStyle, width: 220 }}
      >
        <Icon d={Icons.model} size={13} />
        <span style={{ flex: 1, minWidth: 0, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", textAlign: "left" }}>
          {model || "model"}
        </span>
      </button>
    </>
  );
}

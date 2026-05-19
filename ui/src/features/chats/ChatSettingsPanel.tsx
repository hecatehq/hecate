import type { ReactNode } from "react";
import { formatInteger } from "../../lib/format";
import type { ChatSessionRecord, ChatUsageRecord } from "../../types/chat";
import { Icon, Icons } from "../shared/ui";
import { ExternalAgentSettingsControls } from "./ChatAgentControls";
import { compactID } from "./ChatComposer";
import { ChatInstructionsPanel } from "./ChatInstructionsPanel";

export function ChatSettingsPanel({
  showHecateControls,
  toolsEnabled,
  toolsDisabledForModel,
  rtkEnabled,
  rtkAvailable,
  rtkPath,
  externalAgentID,
  taskID,
  agentName,
  model,
  provider,
  workspace,
  status,
  messageCount,
  agentUsage,
  usageSource,
  externalSession,
  instructionsAvailable,
  isHecateAgentChat,
  instructionsLocked,
  systemPrompt,
  onToolsChange,
  onRTKChange,
  onConfigOptionChange,
  onSystemPromptChange,
  onCopyCommand,
}: {
  showHecateControls: boolean;
  toolsEnabled: boolean;
  toolsDisabledForModel: boolean;
  rtkEnabled: boolean;
  rtkAvailable: boolean;
  rtkPath: string;
  externalAgentID?: string;
  taskID?: string;
  agentName?: string;
  model?: string;
  provider?: string;
  workspace?: string;
  status?: string;
  messageCount: number;
  agentUsage: ChatUsageRecord | null;
  usageSource: "hecate" | "adapter";
  externalSession: ChatSessionRecord | null;
  instructionsAvailable: boolean;
  isHecateAgentChat: boolean;
  instructionsLocked: boolean;
  systemPrompt: string;
  onToolsChange: (enabled: boolean) => void;
  onRTKChange: (enabled: boolean) => void;
  onConfigOptionChange: (
    sessionID: string,
    configID: string,
    value: string | boolean,
  ) => Promise<boolean>;
  onSystemPromptChange: (value: string) => void;
  onCopyCommand: (command: string) => void;
}) {
  const externalRTK = !showHecateControls
    ? externalAgentRTKInfo(externalAgentID || "", rtkAvailable, rtkPath)
    : null;
  return (
    <aside
      aria-label="Chat settings panel"
      style={{
        width: "min(380px, 36vw)",
        minWidth: 320,
        maxWidth: 420,
        flexShrink: 0,
        borderLeft: "1px solid var(--border)",
        background: "var(--bg1)",
        display: "flex",
        flexDirection: "column",
        minHeight: 0,
      }}
    >
      <div
        style={{
          borderBottom: "1px solid var(--border)",
          padding: "14px 14px 12px",
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          gap: 12,
        }}
      >
        <div>
          <div style={{ fontSize: 12, fontWeight: 650, color: "var(--t0)" }}>Chat settings</div>
          <div style={{ marginTop: 4, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
            {showHecateControls
              ? "Controls for future turns in this Hecate Chat. Running task turns keep the settings they started with."
              : "Adapter controls and session details for this External Agent chat. Options apply to future turns in this session."}
          </div>
        </div>
      </div>
      <div style={{ overflowY: "auto", padding: 14, display: "grid", gap: 14 }}>
        {showHecateControls && (
          <>
            <ChatSettingsSection title="Mode">
              <ChatSettingsToolsRow
                enabled={toolsEnabled}
                disabled={toolsDisabledForModel}
                onChange={onToolsChange}
              />
            </ChatSettingsSection>
            <ChatSettingsSection title="Command output">
              <ChatSettingsRTKRow
                available={rtkAvailable}
                path={rtkPath}
                enabled={rtkEnabled}
                shellArgv={rtkEnabled ? "rtk sh -lc <command>" : "sh -lc <command>"}
                onChange={onRTKChange}
              />
            </ChatSettingsSection>
          </>
        )}
        {!showHecateControls && externalSession?.config_options?.length ? (
          <ChatSettingsSection title="Adapter controls">
            <ExternalAgentSettingsControls
              session={externalSession}
              onChange={onConfigOptionChange}
            />
          </ChatSettingsSection>
        ) : null}
        {externalRTK && (
          <ChatSettingsSection title="RTK setup">
            <ChatSettingsExternalRTKRow info={externalRTK} onCopyCommand={onCopyCommand} />
          </ChatSettingsSection>
        )}
        {showHecateControls && instructionsAvailable && (
          <ChatSettingsSection title="System prompt">
            <ChatInstructionsPanel
              embedded
              isHecateAgentChat={isHecateAgentChat}
              locked={instructionsLocked}
              value={systemPrompt}
              onChange={onSystemPromptChange}
            />
          </ChatSettingsSection>
        )}
        {agentUsage && (
          <ChatSettingsSection title={usageSource === "hecate" ? "Usage" : "Reported usage"}>
            <div
              style={{
                border: "1px solid var(--border)",
                borderRadius: 12,
                background: "var(--bg1)",
                padding: 12,
                display: "grid",
                gap: 8,
              }}
            >
              <ChatSettingsField label="Context" value={formatAgentContextUsage(agentUsage)} mono />
              <ChatSettingsField
                label="Cost"
                value={formatAgentReportedCost(agentUsage) || "not reported"}
                mono
              />
              <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
                {usageSource === "hecate"
                  ? "Measured by Hecate when it controls the provider or task-backed turn. Values can be empty for local providers or older turns."
                  : "Reported by the adapter for orientation. Hecate does not enforce external-agent billing."}
              </div>
            </div>
          </ChatSettingsSection>
        )}
        <ChatSettingsSection title="Session context">
          <div
            style={{ display: "grid", gap: 5, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}
          >
            {showHecateControls ? (
              <>
                <ChatSettingsField label="Provider" value={provider || "Select provider"} />
                <ChatSettingsField label="Model" value={model || "not selected"} mono />
              </>
            ) : (
              <ChatSettingsField label="Agent" value={agentName || "External agent"} />
            )}
            <ChatSettingsField
              label="Workspace"
              value={workspace || "not selected"}
              mono
              title={workspace}
            />
            <ChatSettingsField label="Status" value={status || "new chat"} />
            <ChatSettingsField label="Messages" value={String(messageCount)} mono />
            {taskID && <ChatSettingsField label="Task" value={shortID(taskID)} mono />}
          </div>
        </ChatSettingsSection>
      </div>
    </aside>
  );
}

function ChatSettingsSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <div className="kicker" style={{ marginBottom: 7 }}>
        {title}
      </div>
      {children}
    </section>
  );
}

function ChatSettingsField({
  label,
  value,
  mono,
  title,
}: {
  label: string;
  value: string;
  mono?: boolean;
  title?: string;
}) {
  return (
    <div style={{ display: "flex", gap: 8, alignItems: "baseline" }}>
      <span style={{ color: "var(--t3)", fontSize: 11, minWidth: 78 }}>{label}</span>
      <span
        title={title}
        style={{
          color: "var(--t1)",
          fontSize: 11,
          fontFamily: mono ? "var(--font-mono)" : "inherit",
          wordBreak: "break-all",
        }}
      >
        {value}
      </span>
    </div>
  );
}

function ChatSettingsToolsRow({
  enabled,
  disabled,
  onChange,
}: {
  enabled: boolean;
  disabled: boolean;
  onChange: (enabled: boolean) => void;
}) {
  const effectiveEnabled = enabled && !disabled;
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 12,
        background: "var(--bg1)",
        padding: 12,
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 14,
      }}
    >
      <div>
        <div style={{ fontSize: 12, fontWeight: 650, color: "var(--t0)" }}>Tools</div>
        <div style={{ marginTop: 3, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
          {effectiveEnabled
            ? "Use Hecate's task runtime, approvals, artifacts, and sandboxed tool calls."
            : "Send the next turn directly to the selected provider/model without local tools."}
        </div>
        {disabled && (
          <div style={{ marginTop: 4, fontSize: 11, color: "var(--amber)", lineHeight: 1.45 }}>
            This model does not have known tool-calling support. You can still chat normally.
          </div>
        )}
      </div>
      <button
        className="btn btn-ghost btn-sm"
        type="button"
        aria-label={`Tools ${effectiveEnabled ? "on" : "off"}`}
        aria-pressed={effectiveEnabled}
        disabled={disabled && !enabled}
        onClick={() => onChange(!enabled)}
        style={{
          flexShrink: 0,
          minWidth: 72,
          justifyContent: "center",
          color:
            enabled && disabled ? "var(--amber)" : effectiveEnabled ? "var(--teal)" : "var(--t2)",
          borderColor: effectiveEnabled ? "var(--teal-border)" : "var(--border)",
          background: effectiveEnabled ? "var(--teal-bg)" : "transparent",
        }}
      >
        {effectiveEnabled ? "on" : "off"}
      </button>
    </div>
  );
}

function ChatSettingsRTKRow({
  available,
  path,
  enabled,
  shellArgv,
  onChange,
}: {
  available: boolean;
  path: string;
  enabled: boolean;
  shellArgv: string;
  onChange: (enabled: boolean) => void;
}) {
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 12,
        background: "var(--bg1)",
        padding: 12,
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 14,
      }}
    >
      <div>
        <div style={{ fontSize: 12, fontWeight: 650, color: "var(--t0)" }}>
          Compact command output
        </div>
        <div style={{ marginTop: 3, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
          {available ? (
            <>
              RTK is installed
              {path ? (
                <>
                  {" "}
                  at <code>{path}</code>
                </>
              ) : (
                ""
              )}
              . Hecate can run shell and git tools as <code>rtk sh -lc &lt;command&gt;</code> for
              shorter output.
            </>
          ) : (
            <>
              RTK is not installed in the gateway PATH. Install it to enable compact shell/git
              output.
            </>
          )}{" "}
          Hecate still applies approvals, sandbox policy, limits, and timeouts.
        </div>
        <div
          style={{
            marginTop: 9,
            display: "grid",
            gap: 5,
            fontSize: 11,
            color: "var(--t3)",
            lineHeight: 1.45,
          }}
        >
          <ChatSettingsField label="Shell argv" value={shellArgv} mono />
        </div>
      </div>
      <button
        className="btn btn-ghost btn-sm"
        type="button"
        aria-label={`Compact command output ${enabled ? "on" : "off"}`}
        aria-pressed={enabled}
        disabled={!available && !enabled}
        onClick={() => onChange(!enabled)}
        style={{
          flexShrink: 0,
          minWidth: 72,
          justifyContent: "center",
          color: enabled ? "var(--teal)" : "var(--t2)",
          borderColor: enabled ? "var(--teal-border)" : "var(--border)",
          background: enabled ? "var(--teal-bg)" : "transparent",
          opacity: !available && !enabled ? 0.55 : 1,
        }}
      >
        {enabled ? "on" : "off"}
      </button>
    </div>
  );
}

type ExternalAgentRTKInfo = {
  title: string;
  detail: string;
  command: string;
  verify?: string;
  tier: string;
  available: boolean;
  path: string;
};

function externalAgentRTKInfo(
  agentID: string,
  available: boolean,
  path: string,
): ExternalAgentRTKInfo | null {
  switch (agentID) {
    case "claude_code":
      return {
        title: "Claude Code shell hook",
        detail:
          "RTK installs a Claude Code PreToolUse hook. Hecate starts Claude Code normally; Claude rewrites shell commands through its native hook.",
        command: "rtk init --global",
        verify: "rtk init --show",
        tier: "native hook",
        available,
        path,
      };
    case "cursor_agent":
      return {
        title: "Cursor Agent shell hook",
        detail:
          "RTK installs a Cursor Agent preToolUse hook. Hecate starts Cursor Agent normally; Cursor Agent rewrites commands before executing them.",
        command: "rtk init --global --cursor",
        verify: "rtk init --show",
        tier: "native hook",
        available,
        path,
      };
    case "codex":
      return {
        title: "Codex instructions",
        detail:
          "RTK patches AGENTS.md with guidance for Codex to prefer RTK-prefixed commands. This is instruction-based rather than a guaranteed hook.",
        command: "rtk init --codex",
        tier: "instructions",
        available,
        path,
      };
    default:
      return null;
  }
}

function ChatSettingsExternalRTKRow({
  info,
  onCopyCommand,
}: {
  info: ExternalAgentRTKInfo;
  onCopyCommand: (command: string) => void;
}) {
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
      <div>
        <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
          <span style={{ fontSize: 12, fontWeight: 650, color: "var(--t0)" }}>{info.title}</span>
          <span className={info.available ? "badge badge-teal" : "badge"}>
            {info.available ? "rtk installed" : "rtk missing"}
          </span>
          <span className="badge">{info.tier}</span>
        </div>
        <div style={{ marginTop: 5, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
          {info.detail}
        </div>
      </div>
      {info.path && <ChatSettingsField label="RTK path" value={info.path} mono />}
      <div style={{ display: "grid", gap: 6 }}>
        <CopyCommandRow label="Setup" command={info.command} onCopy={onCopyCommand} />
        {info.verify && (
          <CopyCommandRow label="Verify" command={info.verify} onCopy={onCopyCommand} />
        )}
      </div>
      <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
        Run setup once where the external agent reads its settings, then restart that agent if RTK
        requires it.
      </div>
    </div>
  );
}

function CopyCommandRow({
  label,
  command,
  onCopy,
}: {
  label: string;
  command: string;
  onCopy: (command: string) => void;
}) {
  return (
    <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
      <span style={{ minWidth: 48, color: "var(--t3)", fontSize: 11 }}>{label}</span>
      <button
        type="button"
        className="btn btn-ghost btn-sm"
        onClick={() => onCopy(command)}
        title={`Copy ${command}`}
        style={{
          minWidth: 0,
          justifyContent: "flex-start",
          color: "var(--teal)",
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          padding: "4px 7px",
        }}
      >
        <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {command}
        </span>
        <Icon d={Icons.copy} size={12} />
      </button>
    </div>
  );
}

function shortID(id: string): string {
  return compactID(id, ["task_", "run_", "chat_"], 8);
}

function formatAgentContextUsage(usage: ChatUsageRecord): string {
  const used = usage.context_used ?? 0;
  const size = usage.context_size ?? 0;
  if (size > 0) return `${formatInteger(used)} / ${formatInteger(size)}`;
  if (used > 0) return formatInteger(used);
  return "—";
}

function formatAgentReportedCost(usage: ChatUsageRecord): string {
  if (!usage.reported_cost_amount && !usage.reported_cost_currency) return "";
  const currency = usage.reported_cost_currency ? ` ${usage.reported_cost_currency}` : "";
  return `${usage.reported_cost_amount || "0"}${currency}`;
}

type ChatInstructionsPanelProps = {
  isHecateAgentChat: boolean;
  locked: boolean;
  value: string;
  onChange: (value: string) => void;
};

export function ChatInstructionsPanel({
  isHecateAgentChat,
  locked,
  value,
  onChange,
}: ChatInstructionsPanelProps) {
  const label = isHecateAgentChat ? "Agent instructions" : "Instructions";
  return (
    <div style={{ borderBottom: "1px solid var(--border)", padding: "10px 14px", background: "var(--bg2)" }}>
      <div style={{ display: "flex", alignItems: "center", marginBottom: 5, gap: 8 }}>
        <span style={{ fontSize: 11, color: "var(--t2)", fontFamily: "var(--font-mono)" }}>
          {isHecateAgentChat ? "AGENT INSTRUCTIONS" : "INSTRUCTIONS"}
        </span>
        {locked && (
          <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
            locked — start a new chat to change
          </span>
        )}
      </div>
      <div style={{ color: "var(--t2)", fontSize: 12, marginBottom: 8, lineHeight: 1.45 }}>
        {isHecateAgentChat
          ? "Steers the model inside Hecate's task runtime. It does not change approval policy, sandboxing, or external-agent settings."
          : "Steers direct model replies for this Hecate Chat."}
      </div>
      <textarea
        aria-label={label}
        value={value}
        onChange={event => onChange(event.target.value)}
        disabled={locked}
        style={{
          width: "100%",
          background: "var(--bg3)",
          border: "1px solid var(--border)",
          borderRadius: "var(--radius-sm)",
          color: locked ? "var(--t2)" : "var(--t0)",
          fontFamily: "var(--font-mono)",
          fontSize: 12,
          padding: "8px 10px",
          resize: "vertical",
          minHeight: 72,
          outline: "none",
          lineHeight: 1.5,
          opacity: locked ? 0.6 : 1,
        }}
      />
    </div>
  );
}

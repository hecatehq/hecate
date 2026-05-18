type ChatInstructionsPanelProps = {
  embedded?: boolean;
  isHecateAgentChat: boolean;
  locked: boolean;
  value: string;
  onChange: (value: string) => void;
};

export function ChatInstructionsPanel({
  embedded = false,
  isHecateAgentChat,
  locked,
  value,
  onChange,
}: ChatInstructionsPanelProps) {
  const label = isHecateAgentChat
    ? "System prompt / agent instructions"
    : "System prompt / instructions";
  return (
    <div
      style={{
        borderBottom: embedded ? "none" : "1px solid var(--border)",
        padding: embedded ? 0 : "10px 14px",
        background: embedded ? "transparent" : "var(--bg2)",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", marginBottom: 5, gap: 8 }}>
        <span style={{ fontSize: 11, color: "var(--t2)", fontFamily: "var(--font-mono)" }}>
          {isHecateAgentChat
            ? "SYSTEM PROMPT / AGENT INSTRUCTIONS"
            : "SYSTEM PROMPT / INSTRUCTIONS"}
        </span>
        {locked && (
          <span style={{ fontSize: 10, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
            locked — start a new chat to change
          </span>
        )}
      </div>
      <div style={{ color: "var(--t2)", fontSize: 12, marginBottom: 8, lineHeight: 1.45 }}>
        {isHecateAgentChat
          ? "This is the system prompt for future Hecate Agent turns. It steers the model, but does not change approval policy, sandboxing, or external-agent settings."
          : "This is the system prompt for future direct model turns in this Hecate Chat."}
      </div>
      <textarea
        aria-label={label}
        value={value}
        onChange={(event) => onChange(event.target.value)}
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

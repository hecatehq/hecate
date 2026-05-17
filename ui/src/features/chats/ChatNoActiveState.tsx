export function ChatNoActiveState({ agentLabel, hasSessions }: { agentLabel: string; hasSessions: boolean }) {
  return (
    <div
      style={{
        flex: 1,
        minHeight: 0,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 24,
        textAlign: "center",
      }}
    >
      <div style={{ maxWidth: 420 }}>
        <div style={{ fontSize: 14, fontWeight: 600, color: "var(--t1)", marginBottom: 8 }}>
          {hasSessions ? "No chat selected" : "No chats yet"}
        </div>
        <div style={{ fontSize: 12, color: "var(--t3)", lineHeight: 1.6 }}>
          {hasSessions
            ? `Choose a chat from the sidebar, or start a new ${agentLabel} chat.`
            : `Start your first ${agentLabel} chat from the sidebar.`}
        </div>
      </div>
    </div>
  );
}

import type { ChatSessionRecord } from "../../types/chat";
import { BrandAvatar, Icon, Icons } from "../shared/ui";

type Props = {
  sidebarOpen: boolean;
  onOpenSidebar: () => void;

  // Identity strip (avatar + title + subline).
  brand?: string;
  fallback?: string;
  title: string;
  subline: string;
  // The hover-title for the subline; ChatView prefers a richer
  // formatAgentSessionTitle() when the active session is external.
  sublineHoverTitle: string;

  // Right-side actions.
  isAgentChat: boolean;
  isExternalAgentChat: boolean;
  showWorkspaceButton: boolean;
  workspacePath: string;
  chatSettingsOpen: boolean;
  onChooseWorkspace: () => void;
  onToggleChatSettings: () => void;

  // External-agent turn budget pill — only renders when the active
  // session has a max_turns_per_session set.
  activeChatSession: ChatSessionRecord | null;
};

export function ChatHeader(props: Props) {
  const {
    sidebarOpen,
    onOpenSidebar,
    brand,
    fallback,
    title,
    subline,
    sublineHoverTitle,
    isAgentChat,
    isExternalAgentChat,
    showWorkspaceButton,
    workspacePath,
    chatSettingsOpen,
    onChooseWorkspace,
    onToggleChatSettings,
    activeChatSession,
  } = props;

  return (
    <div
      style={{
        height: "var(--topbar-h)",
        borderBottom: "1px solid var(--border)",
        display: "flex",
        alignItems: "center",
        padding: "0 12px",
        gap: 8,
        flexShrink: 0,
        background: "var(--bg1)",
      }}
    >
      {!sidebarOpen && (
        <button
          className="btn btn-ghost btn-sm"
          onClick={onOpenSidebar}
          title="Open chats"
          aria-label="Open chats sidebar"
          type="button"
        >
          <Icon d={Icons.chevR} size={13} />
        </button>
      )}
      <BrandAvatar
        brand={brand}
        fallback={fallback}
        boxed={false}
        size={24}
        title={fallback}
        style={{ flexShrink: 0 }}
      />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div
          style={{
            fontSize: 13,
            fontWeight: 500,
            color: "var(--t0)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {title}
        </div>
        {subline && (
          <div
            title={sublineHoverTitle}
            style={{
              color: "var(--t3)",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              lineHeight: 1.25,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {subline}
          </div>
        )}
      </div>
      {isExternalAgentChat &&
        (() => {
          if (!activeChatSession || !activeChatSession.max_turns_per_session) return null;
          const turnsUsed = activeChatSession.turns_used ?? 0;
          const maxTurns = activeChatSession.max_turns_per_session;
          const atLimit = turnsUsed >= maxTurns;
          return (
            <span
              data-testid="agent-chat-turns-badge"
              style={{
                flexShrink: 0,
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: atLimit ? "var(--amber)" : "var(--t3)",
                whiteSpace: "nowrap",
              }}
              title={
                atLimit
                  ? "Turn limit reached — start a new chat to continue"
                  : `${turnsUsed} of ${maxTurns} turns used`
              }
            >
              {turnsUsed}/{maxTurns} turns
            </span>
          );
        })()}
      {isAgentChat && (
        <div
          aria-label="Chat header actions"
          style={{
            display: "flex",
            alignItems: "center",
            gap: 4,
            flexShrink: 0,
          }}
        >
          {showWorkspaceButton && (
            <button
              className="btn btn-ghost btn-sm"
              onClick={onChooseWorkspace}
              title={workspacePath ? `Workspace: ${workspacePath}` : "Choose workspace folder"}
              aria-label={workspacePath ? `Workspace: ${workspacePath}` : "Choose workspace folder"}
              type="button"
              style={{
                width: 30,
                height: 30,
                padding: 0,
                justifyContent: "center",
                color: "var(--t2)",
                borderColor: "transparent",
                background: "transparent",
                boxShadow: "none",
              }}
            >
              <Icon d={Icons.folder} size={13} />
            </button>
          )}
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-expanded={chatSettingsOpen}
            aria-label="Chat settings"
            onClick={onToggleChatSettings}
            title="Chat settings"
            style={{
              minWidth: 30,
              height: 30,
              padding: 0,
              gap: 6,
              justifyContent: "center",
              color: chatSettingsOpen ? "var(--teal)" : "var(--t2)",
              borderColor: "transparent",
              background: chatSettingsOpen ? "var(--teal-bg)" : "transparent",
              boxShadow: "none",
            }}
          >
            <Icon d={Icons.settings} size={13} />
          </button>
        </div>
      )}
    </div>
  );
}

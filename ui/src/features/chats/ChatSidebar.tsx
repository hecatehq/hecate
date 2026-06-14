import { useState } from "react";

import { useChat } from "../../app/state/chat";
import { useProvidersAndModels } from "../../app/state/providersAndModels";
import { useProjects } from "../../app/state/projects";
import { useChatActions } from "../../app/state/coordinators/chat";
import { useNewChatAgentID, useChatTarget, useChatToolsEnabled } from "../../app/state/derived";
import { useWiredSettingsActions } from "../../app/state/coordinators/wired";
import { formatAbsoluteTime } from "../../lib/format";
import { projectDefaultWorkspace } from "../../lib/project-workspace";
import type { AgentAdapterRecord } from "../../types/agent-adapter";
import type { ChatSessionRecord } from "../../types/chat";
import { ProjectScopePanel } from "../projects/ProjectScopePanel";
import { BrandAvatar, ConfirmModal, Icon, Icons } from "../shared/ui";

import { NewChatAgentButton, chatAgentOption, chatAgentOptionStatus } from "./ChatAgentControls";
import type { ChatAgentOptionID } from "./ChatAgentControls";

export type SidebarSession = {
  id: string;
  title?: string;
  project_id?: string;
  message_count: number;
  provider_call_count: number;
  last_provider?: string;
  last_model?: string;
  agent_brand?: string;
  agent_label?: string;
  status_label?: string;
  created_at?: string;
  updated_at?: string;
};

type Props = {
  isAgentChat: boolean;
  // Session activation: ChatView wires this to focus the composer
  // textarea and dispatch selectChatSession. Keeping the
  // coordination on the parent side avoids the sidebar reaching across
  // the canvas for the composer ref.
  onSelectSession: (sessionID: string) => void;
  // New-chat creation. Gated on agent readiness inside the sidebar.
  onCreateChat: (agentID: ChatAgentOptionID, projectID: string) => void;
  onOpenAgentSetup: (adapterID: string) => void;
};

export function ChatSidebar({
  isAgentChat,
  onSelectSession,
  onCreateChat,
  onOpenAgentSetup,
}: Props) {
  const chat = useChat();
  const providersAndModels = useProvidersAndModels();
  const projects = useProjects();
  const chatTarget = useChatTarget();
  const chatToolsEnabled = useChatToolsEnabled();
  const { actions: settingsActions } = useWiredSettingsActions();
  const chatActions = useChatActions({
    chatTarget,
    setNoticeMessage: settingsActions.setNoticeMessage,
  });
  const newChatAgentID = useNewChatAgentID();
  const chatSessions = chat.state.chatSessions;
  const activeChatSessionID = chat.state.activeChatSessionID;
  const agentAdapters = providersAndModels.state.agentAdapters;
  const agentAdapterHealthByID = providersAndModels.state.agentAdapterHealthByID;
  const [sidebarQuery, setSidebarQuery] = useState("");
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [hoveredChatId, setHoveredChatId] = useState<string | null>(null);
  const [deleteChatID, setDeleteChatID] = useState<string | null>(null);

  const sessions: SidebarSession[] = (chatSessions ?? []).map((s) => ({
    id: s.id,
    title: s.title,
    project_id: s.project_id ?? "",
    message_count: s.message_count,
    provider_call_count: 0,
    last_provider: s.agent_id && s.agent_id !== "hecate" ? s.agent_id : s.provider,
    last_model: s.agent_id && s.agent_id !== "hecate" ? s.status : s.model,
    agent_brand: sidebarSessionBrand(s),
    agent_label: sidebarSessionAgentLabel(s, agentAdapters),
    status_label: s.status,
    created_at: s.created_at,
    updated_at: s.updated_at,
  }));
  const projectSessions = filterSidebarSessionsByProject(sessions, projects.activeProjectID);
  const filteredSessions = filterSidebarSessions(projectSessions, sidebarQuery);
  const groupedSessions = groupSidebarSessions(filteredSessions);
  const activeSessionID = activeChatSessionID;
  const activeProject =
    projects.activeProjectID === ""
      ? null
      : (projects.state.projects.find((project) => project.id === projects.activeProjectID) ??
        null);
  const pendingDeleteChat = sessions.find((session) => session.id === deleteChatID) ?? null;
  const selectedProjectWorkspace = projectDefaultWorkspace(activeProject);
  const workspaceForNewChat = projects.activeProjectID
    ? selectedProjectWorkspace
    : chat.state.agentWorkspace.trim();
  const selectedNewChatUsesWorkspace =
    newChatAgentID !== "hecate" || (chatTarget === "agent" && chatToolsEnabled);
  const workspaceRequiredForNewChat =
    isAgentChat && selectedNewChatUsesWorkspace && !workspaceForNewChat;

  function statusForAgent(agentID: ChatAgentOptionID) {
    const adapter =
      agentID === "hecate" ? undefined : agentAdapters.find((item) => item.id === agentID);
    const health = agentID === "hecate" ? undefined : agentAdapterHealthByID.get(agentID);
    return chatAgentOptionStatus(agentID, adapter, health);
  }

  function selectProjectScope(projectID: string) {
    const scopedSessions = filterSidebarSessionsByProject(sessions, projectID);
    const project =
      projectID === ""
        ? null
        : (projects.state.projects.find((item) => item.id === projectID) ?? null);
    chat.actions.setAgentWorkspace(projectID ? projectDefaultWorkspace(project) : "");
    chat.actions.setAgentWorkspaceBranch("");
    if (activeSessionID && scopedSessions.some((session) => session.id === activeSessionID)) {
      return;
    }
    const nextSession = scopedSessions[0];
    onSelectSession(nextSession?.id ?? "");
  }

  return (
    <>
      <div
        style={{
          width: 220,
          borderRight: "1px solid var(--border)",
          display: "flex",
          flexDirection: "column",
          flexShrink: 0,
          background: "var(--bg1)",
        }}
      >
        <ProjectScopePanel
          noProjectDetail="Chats and tasks stay ungrouped."
          emptyHint="Add a folder when you want a project context."
          deleteMessage={(project) => (
            <>
              Delete <strong>{project.name}</strong>? This also deletes chats in this project.
              Unprojected chats and other projects stay untouched.
            </>
          )}
          onProjectSelected={(projectID) => selectProjectScope(projectID)}
          onProjectDeleted={(projectID) => {
            const activeDeletedSession = chat.state.chatSessions.some(
              (session) =>
                session.id === chat.state.activeChatSessionID && session.project_id === projectID,
            );
            chat.actions.setChatSessions((current) =>
              current.filter((session) => session.project_id !== projectID),
            );
            if (activeDeletedSession || chat.state.activeChatSession?.project_id === projectID) {
              chatActions.startNewChat();
            }
          }}
        />
        <div
          style={{
            padding: "8px 12px 4px",
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            letterSpacing: "0.08em",
            textTransform: "uppercase",
            color: "var(--t3)",
          }}
        >
          Chats
          {projectSessions.length > 0
            ? ` · ${filteredSessions.length}${filteredSessions.length === projectSessions.length ? "" : `/${projectSessions.length}`}`
            : ""}
        </div>
        <div style={{ padding: "4px 8px 8px" }}>
          <NewChatAgentButton
            value={newChatAgentID}
            adapters={agentAdapters}
            healthByID={agentAdapterHealthByID}
            disableUnavailable
            createDisabled={workspaceRequiredForNewChat}
            createTitle={
              workspaceRequiredForNewChat
                ? "Choose a workspace before starting this chat"
                : undefined
            }
            onChange={(agentID) => chatActions.setNewChatAgent(agentID)}
            onSetupAgent={onOpenAgentSetup}
            onCreate={(agentID) => {
              if (workspaceRequiredForNewChat) return;
              if (!statusForAgent(agentID).ready) return;
              if (agentID !== newChatAgentID) chatActions.setNewChatAgent(agentID);
              onSelectSession("");
              onCreateChat(agentID, projects.activeProjectID);
            }}
          />
          {workspaceRequiredForNewChat && (
            <div
              role="status"
              style={{
                color: "var(--yellow)",
                fontSize: 11,
                lineHeight: 1.35,
                padding: "6px 2px 0",
              }}
            >
              Choose a workspace in the chat view before starting agent chats.
            </div>
          )}
        </div>
        <div style={{ padding: "4px 8px 8px" }}>
          <input
            aria-label="Search chats"
            className="input"
            onChange={(e) => setSidebarQuery(e.target.value)}
            placeholder="Search chats"
            style={{ height: 28, fontSize: 12, padding: "0 8px" }}
            value={sidebarQuery}
          />
        </div>
        <div style={{ flex: 1, overflowY: "auto", padding: "2px 0 6px" }}>
          {projectSessions.length === 0 && (
            <div
              style={{
                padding: "16px 12px",
                fontSize: 12,
                color: "var(--t3)",
                textAlign: "center",
              }}
            >
              {sessions.length === 0
                ? "No chats yet"
                : projects.activeProjectID
                  ? "No chats in this project yet"
                  : "No unprojected chats yet"}
            </div>
          )}
          {projectSessions.length > 0 && filteredSessions.length === 0 && (
            <div
              style={{
                padding: "16px 12px",
                fontSize: 12,
                color: "var(--t3)",
                textAlign: "center",
              }}
            >
              No matching chats
            </div>
          )}
          {groupedSessions.map((group) => (
            <div key={group.label}>
              <div
                style={{
                  padding: "8px 12px 3px",
                  fontFamily: "var(--font-mono)",
                  fontSize: 9,
                  letterSpacing: "0.08em",
                  textTransform: "uppercase",
                  color: "var(--t3)",
                }}
              >
                {group.label}
              </div>
              {group.sessions.map((s) => (
                <div
                  key={s.id}
                  role="button"
                  tabIndex={renamingId === s.id ? -1 : 0}
                  aria-current={activeSessionID === s.id ? "true" : undefined}
                  aria-label={`Chat ${s.title || "Untitled"}${s.agent_label ? `, ${s.agent_label}` : ""}`}
                  onClick={() => {
                    if (renamingId === s.id) return;
                    onSelectSession(s.id);
                  }}
                  onKeyDown={(e) => {
                    if (e.target !== e.currentTarget) return;
                    if (renamingId === s.id) return;
                    if (e.key !== "Enter" && e.key !== " ") return;
                    e.preventDefault();
                    onSelectSession(s.id);
                  }}
                  onFocus={() => setHoveredChatId(s.id)}
                  onBlur={(e) => {
                    const nextFocus = e.relatedTarget;
                    if (!(nextFocus instanceof Node) || !e.currentTarget.contains(nextFocus)) {
                      setHoveredChatId(null);
                    }
                  }}
                  onMouseEnter={() => setHoveredChatId(s.id)}
                  onMouseLeave={() => setHoveredChatId(null)}
                  style={{
                    padding: "8px 12px",
                    cursor: "pointer",
                    background: activeSessionID === s.id ? "var(--teal-bg)" : "transparent",
                    borderLeft:
                      activeSessionID === s.id ? "2px solid var(--teal)" : "2px solid transparent",
                    transition: "background 0.1s",
                  }}
                >
                  <div style={{ display: "flex", alignItems: "center", gap: 7, minHeight: 22 }}>
                    {renamingId === s.id ? (
                      <input
                        autoFocus
                        value={renameValue}
                        onChange={(e) => setRenameValue(e.target.value)}
                        onClick={(e) => e.stopPropagation()}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") {
                            void chatActions.renameChatSession(s.id, renameValue);
                            setRenamingId(null);
                          }
                          if (e.key === "Escape") setRenamingId(null);
                        }}
                        onBlur={() => {
                          void chatActions.renameChatSession(s.id, renameValue);
                          setRenamingId(null);
                        }}
                        style={{
                          flex: 1,
                          minWidth: 0,
                          height: 18,
                          boxSizing: "border-box",
                          fontSize: 12,
                          background: "var(--bg3)",
                          border: "1px solid var(--teal)",
                          borderRadius: "var(--radius-sm)",
                          color: "var(--t0)",
                          padding: "0 4px",
                          outline: "none",
                          fontFamily: "var(--font-sans)",
                          lineHeight: "16px",
                        }}
                      />
                    ) : (
                      <>
                        <div
                          style={{
                            flex: 1,
                            minWidth: 0,
                            fontSize: 12,
                            lineHeight: "18px",
                            color: activeSessionID === s.id ? "var(--t0)" : "var(--t1)",
                            fontWeight: activeSessionID === s.id ? 500 : 400,
                            overflow: "hidden",
                            textOverflow: "ellipsis",
                            whiteSpace: "nowrap",
                          }}
                        >
                          {s.title || "Untitled"}
                        </div>
                        {sidebarSessionTimeLabel(s.updated_at || s.created_at) && (
                          <span
                            title={formatAbsoluteTime(s.updated_at || s.created_at)}
                            style={{
                              color: "var(--t3)",
                              flexShrink: 0,
                              fontFamily: "var(--font-mono)",
                              fontSize: 9,
                              lineHeight: "18px",
                            }}
                          >
                            {sidebarSessionTimeLabel(s.updated_at || s.created_at)}
                          </span>
                        )}
                        <div
                          style={{
                            display: "flex",
                            gap: 1,
                            opacity: hoveredChatId === s.id ? 1 : 0,
                            transition: "opacity 0.15s",
                            flexShrink: 0,
                          }}
                        >
                          <button
                            className="btn btn-ghost btn-sm"
                            aria-label={`Rename chat ${s.title || "Untitled"}`}
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              setRenamingId(s.id);
                              setRenameValue(s.title || "");
                            }}
                            style={{ padding: "1px 3px" }}
                            title="Rename"
                          >
                            <Icon d={Icons.edit} size={10} />
                          </button>
                          <button
                            className="btn btn-ghost btn-sm"
                            aria-label={`Delete chat ${s.title || "Untitled"}`}
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              setDeleteChatID(s.id);
                            }}
                            style={{ padding: "1px 3px", color: "var(--red)" }}
                            title="Delete"
                          >
                            <Icon d={Icons.trash} size={10} />
                          </button>
                        </div>
                      </>
                    )}
                  </div>
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 6,
                      fontSize: 10,
                      color: "var(--t3)",
                      marginTop: 1,
                      fontFamily: "var(--font-mono)",
                    }}
                  >
                    {isAgentChat && s.agent_brand && renamingId !== s.id && (
                      <BrandAvatar
                        brand={s.agent_brand}
                        fallback={s.agent_label || s.agent_brand}
                        title={s.agent_label || s.agent_brand}
                        boxed={false}
                        size={13}
                        style={{ flexShrink: 0 }}
                      />
                    )}
                    <span>
                      {isAgentChat && sidebarSessionIsDraft(s) ? "draft" : `${s.message_count} msg`}
                    </span>
                    {isAgentChat ? (
                      !sidebarSessionIsDraft(s) &&
                      s.status_label && (
                        <>
                          <span style={{ color: "var(--t4)" }}>·</span>
                          <span>{s.status_label}</span>
                        </>
                      )
                    ) : (
                      <>
                        <span style={{ color: "var(--t4)" }}>·</span>
                        <span>
                          {s.provider_call_count} call{s.provider_call_count === 1 ? "" : "s"}
                        </span>
                      </>
                    )}
                  </div>
                </div>
              ))}
            </div>
          ))}
        </div>
      </div>
      {pendingDeleteChat && (
        <ConfirmModal
          danger
          title="Delete chat"
          confirmLabel="Delete chat"
          message={
            <>
              Delete <strong>{pendingDeleteChat.title || "Untitled"}</strong>? This removes the chat
              transcript from Hecate.
            </>
          }
          onClose={() => setDeleteChatID(null)}
          onConfirm={async () => {
            await chatActions.deleteChatSession(pendingDeleteChat.id);
            setDeleteChatID(null);
          }}
        />
      )}
    </>
  );
}

function filterSidebarSessionsByProject(
  sessions: SidebarSession[],
  activeProjectID: string,
): SidebarSession[] {
  const projectID = activeProjectID.trim();
  return sessions.filter((session) => {
    const sessionProjectID = (session.project_id ?? "").trim();
    return projectID ? sessionProjectID === projectID : sessionProjectID === "";
  });
}

export function filterSidebarSessions(sessions: SidebarSession[], query: string): SidebarSession[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return sessions;
  return sessions.filter((session) => {
    const searchable = [
      session.title,
      session.last_provider,
      session.last_model,
      session.agent_label,
      session.status_label,
    ]
      .filter(Boolean)
      .join(" ")
      .toLowerCase();
    return searchable.includes(needle);
  });
}

export function sidebarSessionIsDraft(
  session: Pick<SidebarSession, "message_count" | "status_label">,
): boolean {
  const status = (session.status_label ?? "").trim();
  return session.message_count === 0 && (status === "" || status === "idle");
}

export function sidebarSessionBrand(session: ChatSessionRecord): string | undefined {
  if (session.agent_id && session.agent_id !== "hecate") {
    return session.agent_id;
  }
  if (session.agent_id === "hecate") {
    return "hecate";
  }
  return session.provider || session.model;
}

export function sidebarSessionAgentLabel(
  session: ChatSessionRecord,
  adapters: AgentAdapterRecord[],
): string | undefined {
  if (session.agent_id && session.agent_id !== "hecate") {
    return chatAgentOption(session.agent_id || "", adapters).label;
  }
  if (session.agent_id === "hecate") {
    return "Hecate";
  }
  return session.provider || session.model;
}

export function sidebarSessionTimeLabel(value?: string): string {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  const now = new Date();
  if (d.toDateString() === now.toDateString()) {
    return d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  }
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (d.toDateString() === yesterday.toDateString()) return "yesterday";
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function groupSidebarSessions(
  sessions: SidebarSession[],
): Array<{ label: string; sessions: SidebarSession[] }> {
  const groups = new Map<string, SidebarSession[]>();
  for (const session of sessions) {
    const label = sidebarDateGroup(session.updated_at || session.created_at);
    const group = groups.get(label) ?? [];
    group.push(session);
    groups.set(label, group);
  }
  return ["Today", "This week", "Older", "No date"]
    .map((label) => ({ label, sessions: groups.get(label) ?? [] }))
    .filter((group) => group.sessions.length > 0);
}

function sidebarDateGroup(value?: string): string {
  if (!value) return "No date";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "No date";
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const chatDay = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  const ageDays = Math.floor((today.getTime() - chatDay.getTime()) / 86_400_000);
  if (ageDays <= 0) return "Today";
  if (ageDays < 7) return "This week";
  return "Older";
}

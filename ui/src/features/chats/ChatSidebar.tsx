import { useEffect, useRef, useState } from "react";

import { composerDraftScope, composerDraftScopesMatch, useChat } from "../../app/state/chat";
import { queuedChatSessionDeletionFenceStatus } from "../../app/state/queuedChatStorage";
import { useProvidersAndModels } from "../../app/state/providersAndModels";
import { useProjects } from "../../app/state/projects";
import { useAgentAdapterActions } from "../../app/state/coordinators/agentAdapters";
import { useChatActions } from "../../app/state/coordinators/chat";
import { useNewChatAgentID, useChatTarget } from "../../app/state/derived";
import { useWiredSettingsActions } from "../../app/state/coordinators/wired";
import { useRuntime } from "../../app/state/runtime";
import { shouldAutoProbeExternalAgentReadiness } from "../../lib/external-agent-readiness";
import { formatAbsoluteTime } from "../../lib/format";
import { projectDefaultWorkspace } from "../../lib/project-workspace";
import { isRemoteRuntimeSession } from "../../lib/runtime-utils";
import type { AgentAdapterRecord } from "../../types/agent-adapter";
import type { ChatSessionRecord } from "../../types/chat";
import { ProjectScopePanel } from "../projects/ProjectScopePanel";
import { BrandAvatar, ConfirmModal, Icon, Icons } from "../shared/ui";
import { formatProjectDeleteSummary } from "../projects/projectDisplay";

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
  cleanup_required?: boolean;
};

type Props = {
  isAgentChat: boolean;
  // Session activation: ChatView wires this to focus the composer
  // textarea and dispatch selectChatSession. Keeping the
  // coordination on the parent side avoids the sidebar reaching across
  // the canvas for the composer ref.
  onSelectSession: (sessionID: string, mode?: "push" | "replace") => Promise<boolean>;
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
  const runtime = useRuntime();
  const chatTarget = useChatTarget();
  const { actions: settingsActions } = useWiredSettingsActions();
  const agentAdapterActions = useAgentAdapterActions({
    setNoticeMessage: settingsActions.setNoticeMessage,
  });
  const probeAgentAdapter = agentAdapterActions.probeAgentAdapter;
  const chatActions = useChatActions({
    chatTarget,
    setNoticeMessage: settingsActions.setNoticeMessage,
  });
  const newChatAgentID = useNewChatAgentID();
  const chatSessions = chat.state.chatSessions;
  const chatCreating = chat.state.chatCreating;
  const recoverableComposerDraft = chat.state.recoverableComposerDraft;
  const activeRecoverableComposerDraftID = chat.state.activeRecoverableComposerDraftID;
  const activeChatSessionID = chat.state.activeChatSessionID;
  const cancellingSessionID = chat.state.chatCancelling ? chat.state.chatCancellingSessionID : "";
  const agentAdapters = providersAndModels.state.agentAdapters;
  const agentAdapterHealthByID = providersAndModels.state.agentAdapterHealthByID;
  const agentAdapterHealthLoadingByID = providersAndModels.state.agentAdapterHealthLoadingByID;
  const autoProbedAdapterIDs = useRef(new Set<string>());
  const [sidebarQuery, setSidebarQuery] = useState("");
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [hoveredChatId, setHoveredChatId] = useState<string | null>(null);
  const [deleteChatID, setDeleteChatID] = useState<string | null>(null);
  const [deleteChatPending, setDeleteChatPending] = useState(false);
  const deleteChatPendingRef = useRef(false);
  const chatSearchInputRef = useRef<HTMLInputElement>(null);

  const serverSessions: SidebarSession[] = (chatSessions ?? []).map((s) => ({
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
  const serverSessionIDs = new Set(serverSessions.map((session) => session.id));
  // An unreadable deletion fence must keep the recovery path visible; otherwise
  // the vanished server session leaves no operator action to clear its queued prompt.
  const cleanupRecoverySessions: SidebarSession[] = Array.from(
    new Map(
      chat.state.queuedChatMessages
        .filter(
          (message) =>
            !serverSessionIDs.has(message.session_id) &&
            message.delivery_storage_failed === true &&
            queuedChatSessionDeletionFenceStatus(message.session_id) !== "absent",
        )
        .map((message) => [
          message.session_id,
          {
            id: message.session_id,
            title: "Deleted chat cleanup required",
            project_id: message.project_id ?? "",
            message_count: 0,
            provider_call_count: 0,
            status_label: "cleanup required",
            created_at: message.created_at,
            updated_at: message.created_at,
            cleanup_required: true,
          } satisfies SidebarSession,
        ]),
    ).values(),
  );
  const sessions = [...cleanupRecoverySessions, ...serverSessions];
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
  const selectedNewChatUsesWorkspace = newChatAgentID !== "hecate";
  const workspaceRequiredForNewChat =
    isAgentChat && selectedNewChatUsesWorkspace && !workspaceForNewChat;
  const chatSessionCreateInFlight = chat.actions.isChatSessionCreateInFlight();
  const attachmentTurnInFlight = chat.actions.hasChatAttachmentTurn();
  const ownershipMutationInFlight = chat.state.chatOwnershipMutationInFlight;
  const recoverableDraftForCurrentScope =
    recoverableComposerDraft &&
    composerDraftScopesMatch(
      recoverableComposerDraft.scope,
      composerDraftScope({
        projectID: projects.activeProjectID,
        agentID: newChatAgentID,
        provider: chat.state.providerFilter,
        model: chat.state.model,
        workspace: workspaceForNewChat,
      }),
    )
      ? recoverableComposerDraft
      : null;
  const savedRecoveryNotice =
    recoverableDraftForCurrentScope?.id === activeRecoverableComposerDraftID
      ? null
      : recoverableDraftForCurrentScope;

  function statusForAgent(agentID: ChatAgentOptionID) {
    const adapter =
      agentID === "hecate" ? undefined : agentAdapters.find((item) => item.id === agentID);
    const health = agentID === "hecate" ? undefined : agentAdapterHealthByID.get(agentID);
    return chatAgentOptionStatus(agentID, adapter, health);
  }

  useEffect(() => {
    const remoteRuntime = isRemoteRuntimeSession(runtime.state.sessionInfo);
    for (const adapter of agentAdapters) {
      if (
        !shouldAutoProbeExternalAgentReadiness(
          adapter,
          agentAdapterHealthByID.get(adapter.id) ?? null,
          Boolean(agentAdapterHealthLoadingByID.get(adapter.id)),
          remoteRuntime,
        )
      ) {
        continue;
      }
      if (autoProbedAdapterIDs.current.has(adapter.id)) continue;
      autoProbedAdapterIDs.current.add(adapter.id);
      void probeAgentAdapter(adapter.id);
    }
  }, [
    agentAdapters,
    agentAdapterHealthByID,
    agentAdapterHealthLoadingByID,
    probeAgentAdapter,
    runtime.state.sessionInfo,
  ]);

  async function selectProjectScope(projectID: string): Promise<boolean> {
    const scopedSessions = filterSidebarSessionsByProject(sessions, projectID);
    const project =
      projectID === ""
        ? null
        : (projects.state.projects.find((item) => item.id === projectID) ?? null);
    const activeSessionAlreadyScoped = Boolean(
      activeSessionID && scopedSessions.some((session) => session.id === activeSessionID),
    );
    if (!activeSessionAlreadyScoped) {
      const nextSession = scopedSessions[0];
      const selected = await onSelectSession(nextSession?.id ?? "");
      if (!selected) return false;
    }
    const ownershipBlockReason = chat.actions.chatOwnershipMutationBlockReason();
    if (ownershipBlockReason) {
      settingsActions.setNoticeMessage("error", ownershipBlockReason);
      return false;
    }
    chat.actions.setAgentWorkspace(projectID ? projectDefaultWorkspace(project) : "");
    chat.actions.setAgentWorkspaceBranch("");
    return true;
  }

  return (
    <>
      <div
        className="chat-sidebar"
        style={{
          width: "var(--chat-sidebar-width, 220px)",
          borderRight: "var(--chat-sidebar-border-right, 1px solid var(--border))",
          borderBottom: "var(--chat-sidebar-border-bottom, 0)",
          display: "flex",
          flexDirection: "column",
          flexShrink: 0,
          maxHeight: "var(--chat-sidebar-max-height, none)",
          background: "var(--bg1)",
          overflow: "hidden",
        }}
      >
        <ProjectScopePanel
          noProjectDetail="Chats and tasks stay ungrouped."
          emptyHint="Add a folder when you want a project context."
          canChangeProjectScope={() => {
            const reason = chat.actions.chatOwnershipMutationBlockReason();
            if (!reason) return true;
            settingsActions.setNoticeMessage("error", reason);
            return false;
          }}
          beginProjectDelete={() => {
            const token = chat.actions.beginChatOwnershipMutation();
            if (token !== null) return token;
            settingsActions.setNoticeMessage(
              "error",
              chat.actions.chatOwnershipMutationBlockReason() ||
                "Wait for the current chat ownership change to finish.",
            );
            return null;
          }}
          finishProjectDelete={chat.actions.finishChatOwnershipMutation}
          deleteMessage={(project) => (
            <>
              Delete <strong>{project.name}</strong>? This also deletes chats in this project.
              Unprojected chats and other projects stay untouched.
            </>
          )}
          onProjectSelected={selectProjectScope}
          onProjectDeleted={(projectID, result) => {
            const browserQueueCleared = chat.actions.fenceDeletedChatProject(projectID);
            settingsActions.setNoticeMessage(
              browserQueueCleared ? "success" : "error",
              browserQueueCleared
                ? formatProjectDeleteSummary(result)
                : `${formatProjectDeleteSummary(result)} Hecate could not clear every browser-local queued prompt for this project. Clear this site's browser data before closing or reloading.`,
            );
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
            selectionDisabled={chatCreating || chatSessionCreateInFlight}
            createDisabled={
              workspaceRequiredForNewChat ||
              chatCreating ||
              chatSessionCreateInFlight ||
              attachmentTurnInFlight ||
              ownershipMutationInFlight
            }
            createTitle={
              workspaceRequiredForNewChat
                ? "Choose a workspace before starting this chat"
                : chatCreating || chatSessionCreateInFlight
                  ? "A new chat is already being created"
                  : attachmentTurnInFlight
                    ? "Wait for the attachment response before starting a new chat"
                    : ownershipMutationInFlight
                      ? "Wait for the current chat ownership change to finish"
                      : undefined
            }
            onChange={(agentID) => chatActions.setNewChatAgent(agentID)}
            onSetupAgent={onOpenAgentSetup}
            onCreate={(agentID) => {
              if (chatCreating || chat.actions.isChatCreationActive()) return;
              if (workspaceRequiredForNewChat) return;
              if (
                chatSessionCreateInFlight ||
                attachmentTurnInFlight ||
                ownershipMutationInFlight
              ) {
                return;
              }
              if (!statusForAgent(agentID).ready) return;
              if (chat.state.pendingChatAttachments.length > 0) {
                settingsActions.setNoticeMessage(
                  "error",
                  "Remove attached files before starting a new chat.",
                );
                return;
              }
              if (agentID !== newChatAgentID) chatActions.setNewChatAgent(agentID);
              void onSelectSession("");
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
          {!chatCreating && savedRecoveryNotice && (
            <div
              role="status"
              style={{
                color: "var(--amber)",
                fontSize: 11,
                lineHeight: 1.35,
                padding: "6px 2px 0",
              }}
            >
              A previous unsent draft is saved. Start a matching new chat with an empty composer to
              restore it.
            </div>
          )}
        </div>
        <div style={{ padding: "4px 8px 8px" }}>
          <input
            aria-label="Search chats"
            className="input"
            onChange={(e) => setSidebarQuery(e.target.value)}
            placeholder="Search chats"
            ref={chatSearchInputRef}
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
                  aria-disabled={cancellingSessionID === s.id || undefined}
                  aria-label={`Chat ${s.title || "Untitled"}${s.agent_label ? `, ${s.agent_label}` : ""}`}
                  onClick={() => {
                    if (renamingId === s.id) return;
                    if (cancellingSessionID === s.id) return;
                    if (s.cleanup_required) {
                      setDeleteChatID(s.id);
                      return;
                    }
                    void onSelectSession(s.id);
                  }}
                  onKeyDown={(e) => {
                    if (e.target !== e.currentTarget) return;
                    if (renamingId === s.id) return;
                    if (cancellingSessionID === s.id) return;
                    if (e.key !== "Enter" && e.key !== " ") return;
                    e.preventDefault();
                    if (s.cleanup_required) {
                      setDeleteChatID(s.id);
                      return;
                    }
                    void onSelectSession(s.id);
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
                    cursor: cancellingSessionID === s.id ? "wait" : "pointer",
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
                        disabled={cancellingSessionID === s.id}
                        value={renameValue}
                        onChange={(e) => setRenameValue(e.target.value)}
                        onClick={(e) => e.stopPropagation()}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") {
                            if (cancellingSessionID === s.id) return;
                            void chatActions.renameChatSession(s.id, renameValue);
                            setRenamingId(null);
                          }
                          if (e.key === "Escape") setRenamingId(null);
                        }}
                        onBlur={() => {
                          if (cancellingSessionID !== s.id) {
                            void chatActions.renameChatSession(s.id, renameValue);
                          }
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
                          {!s.cleanup_required && (
                            <button
                              className="btn btn-ghost btn-sm"
                              aria-label={`Rename chat ${s.title || "Untitled"}`}
                              disabled={cancellingSessionID === s.id}
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
                          )}
                          <button
                            className="btn btn-ghost btn-sm"
                            aria-label={`Delete chat ${s.title || "Untitled"}`}
                            disabled={cancellingSessionID === s.id}
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              if (cancellingSessionID === s.id) return;
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
            pendingDeleteChat.cleanup_required ? (
              <>
                Retry browser cleanup for this already-deleted chat? This removes its remaining
                queued prompts and recovery marker from this browser.
              </>
            ) : (
              <>
                Delete <strong>{pendingDeleteChat.title || "Untitled"}</strong>? This removes the
                chat transcript from Hecate.
              </>
            )
          }
          pending={deleteChatPending}
          confirmDisabled={cancellingSessionID === pendingDeleteChat.id}
          returnFocusRef={chatSearchInputRef}
          onClose={() => {
            if (!deleteChatPendingRef.current) setDeleteChatID(null);
          }}
          onConfirm={async () => {
            if (cancellingSessionID === pendingDeleteChat.id) return;
            if (deleteChatPendingRef.current) return;
            deleteChatPendingRef.current = true;
            setDeleteChatPending(true);
            try {
              const deleted = await chatActions.deleteChatSession(pendingDeleteChat.id);
              if (deleted) {
                setDeleteChatID(null);
                if (pendingDeleteChat.id === activeSessionID) {
                  await onSelectSession("", "replace");
                }
              }
            } finally {
              deleteChatPendingRef.current = false;
              setDeleteChatPending(false);
            }
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

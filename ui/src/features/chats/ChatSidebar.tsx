import { useEffect, useRef, useState } from "react";

import { chatNavigationURL } from "../../app/navigation";
import { composerDraftScope, composerDraftScopesMatch, useChat } from "../../app/state/chat";
import { queuedChatSessionDeletionFenceStatus } from "../../app/state/queuedChatStorage";
import { useProvidersAndModels } from "../../app/state/providersAndModels";
import { useProjects } from "../../app/state/projects";
import { useChatActions } from "../../app/state/coordinators/chat";
import { useNewChatAgentID, useChatTarget } from "../../app/state/derived";
import { useWiredSettingsActions } from "../../app/state/coordinators/wired";
import { formatAbsoluteTime } from "../../lib/format";
import { projectDefaultWorkspace } from "../../lib/project-workspace";
import { getAgentPresets } from "../../lib/api";
import type { AgentAdapterRecord } from "../../types/agent-adapter";
import type { AgentPresetRecord } from "../../types/agent-preset";
import type { ChatSessionRecord } from "../../types/chat";
import { ProjectScopePanel } from "../projects/ProjectScopePanel";
import {
  EntityIndexGroupLabel,
  EntityIndexHeader,
  EntityIndexHeading,
  EntityIndexList,
  EntityIndexPanel,
  EntityIndexState,
  EntityListRow,
} from "../shared/EntityWorkspace";
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
  onCreateChat: (agentID: ChatAgentOptionID, projectID: string, agentPresetID?: string) => void;
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
  const { actions: settingsActions } = useWiredSettingsActions();
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
  const [sidebarQuery, setSidebarQuery] = useState("");
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [deleteChatID, setDeleteChatID] = useState<string | null>(null);
  const [deleteChatPending, setDeleteChatPending] = useState(false);
  const [hecatePresets, setHecatePresets] = useState<AgentPresetRecord[]>([]);
  const [hecatePresetsLoaded, setHecatePresetsLoaded] = useState(false);
  const [hecatePresetsLoading, setHecatePresetsLoading] = useState(false);
  const [hecatePresetsError, setHecatePresetsError] = useState("");
  const [newHecatePresetID, setNewHecatePresetID] = useState("");
  const hecatePresetsLoadRef = useRef<Promise<void> | null>(null);
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

  const compatibleHecatePresets = hecatePresets.filter(isHecateChatPreset);

  function loadHecatePresets() {
    if (hecatePresetsLoaded || hecatePresetsLoadRef.current) return;
    setHecatePresetsError("");
    setHecatePresetsLoading(true);
    const load = getAgentPresets()
      .then((response) => {
        setHecatePresets(response.data);
        setHecatePresetsLoaded(true);
      })
      .catch(() => {
        setHecatePresetsError(
          "Could not load Agent Presets. You can still start a default Hecate Chat.",
        );
      })
      .finally(() => {
        hecatePresetsLoadRef.current = null;
        setHecatePresetsLoading(false);
      });
    hecatePresetsLoadRef.current = load;
  }

  useEffect(() => {
    if (
      !newHecatePresetID ||
      compatibleHecatePresets.some((preset) => preset.id === newHecatePresetID)
    ) {
      return;
    }
    setNewHecatePresetID("");
  }, [compatibleHecatePresets, newHecatePresetID]);
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
      <EntityIndexPanel aria-label="Chats" className="chat-sidebar">
        <ProjectScopePanel
          noProjectDetail="Chats and tasks stay ungrouped."
          emptyHint="Add a folder when you want a project context."
          canChangeProjectScope={() => {
            const reason = chat.actions.chatOwnershipMutationBlockReason();
            if (!reason) return true;
            settingsActions.setNoticeMessage("error", reason);
            return false;
          }}
          projectScopeChangeBlockReason={chat.actions.chatOwnershipMutationBlockReason}
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
        <EntityIndexHeader>
          <EntityIndexHeading>
            Chats
            {projectSessions.length > 0
              ? ` · ${filteredSessions.length}${filteredSessions.length === projectSessions.length ? "" : `/${projectSessions.length}`}`
              : ""}
          </EntityIndexHeading>
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
                if (agentID === "hecate" && newHecatePresetID) {
                  onCreateChat(agentID, projects.activeProjectID, newHecatePresetID);
                  return;
                }
                onCreateChat(agentID, projects.activeProjectID);
              }}
            />
            {newChatAgentID === "hecate" && (
              <label
                style={{
                  display: "grid",
                  gap: 5,
                  marginTop: 8,
                  fontSize: 11,
                  color: "var(--t3)",
                }}
              >
                Agent preset
                <select
                  className="input"
                  aria-label="Agent preset for new Hecate chat"
                  value={newHecatePresetID}
                  disabled={chatCreating || chatSessionCreateInFlight}
                  onFocus={loadHecatePresets}
                  onPointerDown={loadHecatePresets}
                  onChange={(event) => setNewHecatePresetID(event.target.value)}
                >
                  <option value="">Default Hecate Chat</option>
                  {compatibleHecatePresets.map((preset) => (
                    <option key={preset.id} value={preset.id}>
                      {preset.name || preset.id}
                    </option>
                  ))}
                </select>
                {hecatePresetsLoading ? (
                  <span role="status">Loading Hecate Chat presets…</span>
                ) : compatibleHecatePresets.length > 0 ? (
                  <span>
                    The selected preset is frozen with the new chat. Its model hints apply when no
                    provider or model is chosen.
                  </span>
                ) : hecatePresetsLoaded ? (
                  <span>No Hecate Chat presets are available yet.</span>
                ) : null}
                {hecatePresetsError && (
                  <span role="status" style={{ color: "var(--amber)" }}>
                    {hecatePresetsError}
                  </span>
                )}
              </label>
            )}
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
                A previous unsent draft is saved. Start a matching new chat with an empty composer
                to restore it.
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
        </EntityIndexHeader>
        <EntityIndexList style={{ padding: "2px 0 6px" }}>
          {projectSessions.length === 0 && (
            <EntityIndexState style={{ padding: "16px 12px" }}>
              {sessions.length === 0
                ? "No chats yet"
                : projects.activeProjectID
                  ? "No chats in this project yet"
                  : "No unprojected chats yet"}
            </EntityIndexState>
          )}
          {projectSessions.length > 0 && filteredSessions.length === 0 && (
            <EntityIndexState style={{ padding: "16px 12px" }}>No matching chats</EntityIndexState>
          )}
          {groupedSessions.map((group) => (
            <div key={group.label}>
              <EntityIndexGroupLabel>{group.label}</EntityIndexGroupLabel>
              {group.sessions.map((s) => {
                const renaming = renamingId === s.id;
                const title = s.title || "Untitled";
                const activate = () => {
                  if (s.cleanup_required) {
                    setDeleteChatID(s.id);
                    return;
                  }
                  void onSelectSession(s.id);
                };
                return (
                  <EntityListRow
                    key={s.id}
                    active={activeSessionID === s.id}
                    aria-label={
                      renaming
                        ? undefined
                        : `Chat ${title}${s.agent_label ? `, ${s.agent_label}` : ""}`
                    }
                    disabled={cancellingSessionID === s.id}
                    href={
                      renaming || s.cleanup_required
                        ? undefined
                        : chatNavigationURL(window.location, { chatID: s.id })
                    }
                    onActivate={renaming ? undefined : activate}
                    style={{ borderBottom: 0 }}
                    actions={
                      renaming ? undefined : (
                        <>
                          {!s.cleanup_required && (
                            <button
                              className="btn btn-ghost btn-sm"
                              aria-label={`Rename chat ${title}`}
                              disabled={cancellingSessionID === s.id}
                              type="button"
                              onClick={() => {
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
                            aria-label={`Delete chat ${title}`}
                            disabled={cancellingSessionID === s.id}
                            type="button"
                            onClick={() => {
                              if (cancellingSessionID === s.id) return;
                              setDeleteChatID(s.id);
                            }}
                            style={{ padding: "1px 3px", color: "var(--red)" }}
                            title="Delete"
                          >
                            <Icon d={Icons.trash} size={10} />
                          </button>
                        </>
                      )
                    }
                  >
                    {renaming ? (
                      <input
                        aria-label={`Rename chat ${title}`}
                        autoFocus
                        disabled={cancellingSessionID === s.id}
                        value={renameValue}
                        onChange={(e) => setRenameValue(e.target.value)}
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
                          width: "100%",
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
                          style={{ display: "flex", alignItems: "center", gap: 7, minHeight: 22 }}
                        >
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
                            {title}
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
                          {isAgentChat && s.agent_brand && (
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
                            {isAgentChat && sidebarSessionIsDraft(s)
                              ? "draft"
                              : `${s.message_count} msg`}
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
                                {s.provider_call_count} call
                                {s.provider_call_count === 1 ? "" : "s"}
                              </span>
                            </>
                          )}
                        </div>
                      </>
                    )}
                  </EntityListRow>
                );
              })}
            </div>
          ))}
        </EntityIndexList>
      </EntityIndexPanel>
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

export function isHecateChatPreset(preset: AgentPresetRecord): boolean {
  const surface = preset.surface.trim();
  return surface === "any" || surface === "hecate_chat";
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

import { useState } from "react";

import { useRuntimeConsoleContext } from "../../app/RuntimeConsoleContext";
import { formatAbsoluteTime } from "../../lib/format";
import type { AgentAdapterRecord } from "../../types/agent-adapter";
import type { ChatSessionRecord } from "../../types/chat";
import { BrandAvatar, Icon, Icons } from "../shared/ui";

import { NewChatAgentButton, chatAgentOption, chatAgentOptionStatus } from "./ChatAgentControls";
import type { ChatAgentOptionID } from "./ChatAgentControls";

export type SidebarSession = {
  id: string;
  title?: string;
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
  // Session activation: ChatView wires this to clear the draft, focus
  // the composer textarea, and dispatch selectChatSession. Keeping the
  // coordination on the parent side avoids the sidebar reaching across
  // the canvas for the composer ref.
  onSelectSession: (sessionID: string) => void;
  // New-chat creation. Gated on adapter readiness inside the sidebar.
  onCreateChat: () => void;
};

export function ChatSidebar({ isAgentChat, onSelectSession, onCreateChat }: Props) {
  const { state, actions } = useRuntimeConsoleContext();
  const [sidebarQuery, setSidebarQuery] = useState("");
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [hoveredChatId, setHoveredChatId] = useState<string | null>(null);

  const sessions: SidebarSession[] = (state.chatSessions ?? []).map((s) => ({
    id: s.id,
    title: s.title,
    message_count: s.message_count,
    provider_call_count: 0,
    last_provider: s.runtime_kind === "external_agent" || s.adapter_id ? s.adapter_id : s.provider,
    last_model: s.runtime_kind === "external_agent" || s.adapter_id ? s.status : s.model,
    agent_brand: sidebarSessionBrand(s),
    agent_label: sidebarSessionAgentLabel(s, state.agentAdapters),
    status_label: s.status,
    created_at: s.created_at,
    updated_at: s.updated_at,
  }));
  const filteredSessions = filterSidebarSessions(sessions, sidebarQuery);
  const groupedSessions = groupSidebarSessions(filteredSessions);
  const activeSessionID = state.activeChatSessionID;

  const newChatAgentID = state.newChatAgentID || "hecate";
  const newChatAgentAdapter = newChatAgentID === "hecate" ? undefined : state.agentAdapters.find((adapter) => adapter.id === newChatAgentID);
  const newChatAgentHealth = newChatAgentID === "hecate" ? undefined : state.agentAdapterHealthByID.get(newChatAgentID);
  const newChatAgentStatus = chatAgentOptionStatus(newChatAgentID as ChatAgentOptionID, newChatAgentAdapter, newChatAgentHealth);
  const newChatAgentReady = newChatAgentStatus.ready;

  return (
    <div style={{ width: 220, borderRight: "1px solid var(--border)", display: "flex", flexDirection: "column", flexShrink: 0, background: "var(--bg1)" }}>
      <div style={{ height: "var(--topbar-h)", padding: "0 8px", borderBottom: "1px solid var(--border)", display: "flex", alignItems: "center", flexShrink: 0 }}>
        <div style={{ display: "flex", gap: 6, width: "100%" }}>
          <NewChatAgentButton
            value={newChatAgentID}
            adapters={state.agentAdapters}
            healthByID={state.agentAdapterHealthByID}
            disableUnavailable
            onChange={(agentID) => actions.setNewChatAgent(agentID)}
            onCreate={() => {
              if (!newChatAgentReady) return;
              onCreateChat();
            }}
          />
        </div>
      </div>
      <div style={{ padding: "8px 12px 4px", fontFamily: "var(--font-mono)", fontSize: 10, letterSpacing: "0.08em", textTransform: "uppercase", color: "var(--t3)" }}>
        Chats{sessions.length > 0 ? ` · ${filteredSessions.length}${filteredSessions.length === sessions.length ? "" : `/${sessions.length}`}` : ""}
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
        {sessions.length === 0 && (
          <div style={{ padding: "16px 12px", fontSize: 12, color: "var(--t3)", textAlign: "center" }}>No chats yet</div>
        )}
        {sessions.length > 0 && filteredSessions.length === 0 && (
          <div style={{ padding: "16px 12px", fontSize: 12, color: "var(--t3)", textAlign: "center" }}>No matching chats</div>
        )}
        {groupedSessions.map((group) => (
          <div key={group.label}>
            <div style={{ padding: "8px 12px 3px", fontFamily: "var(--font-mono)", fontSize: 9, letterSpacing: "0.08em", textTransform: "uppercase", color: "var(--t3)" }}>
              {group.label}
            </div>
            {group.sessions.map(s => (
              <div key={s.id}
                role="button"
                tabIndex={renamingId === s.id ? -1 : 0}
                aria-current={(activeSessionID === s.id) ? "true" : undefined}
                aria-label={`Chat ${s.title || "Untitled"}${s.agent_label ? `, ${s.agent_label}` : ""}`}
                onClick={() => {
                  if (renamingId === s.id) return;
                  onSelectSession(s.id);
                }}
                onKeyDown={e => {
                  if (e.target !== e.currentTarget) return;
                  if (renamingId === s.id) return;
                  if (e.key !== "Enter" && e.key !== " ") return;
                  e.preventDefault();
                  onSelectSession(s.id);
                }}
                onFocus={() => setHoveredChatId(s.id)}
                onBlur={e => {
                  const nextFocus = e.relatedTarget;
                  if (!(nextFocus instanceof Node) || !e.currentTarget.contains(nextFocus)) {
                    setHoveredChatId(null);
                  }
                }}
                onMouseEnter={() => setHoveredChatId(s.id)}
                onMouseLeave={() => setHoveredChatId(null)}
                style={{
                  padding: "8px 12px", cursor: "pointer",
                  background: activeSessionID === s.id ? "var(--teal-bg)" : "transparent",
                  borderLeft: activeSessionID === s.id ? "2px solid var(--teal)" : "2px solid transparent",
                  transition: "background 0.1s",
                }}>
                <div style={{ display: "flex", alignItems: "center", gap: 7, minHeight: 22 }}>
                  {renamingId === s.id ? (
                    <input
                      autoFocus
                      value={renameValue}
                      onChange={e => setRenameValue(e.target.value)}
                      onClick={e => e.stopPropagation()}
                      onKeyDown={e => {
                        if (e.key === "Enter") { void actions.renameChatSession(s.id, renameValue); setRenamingId(null); }
                        if (e.key === "Escape") setRenamingId(null);
                      }}
                      onBlur={() => { void actions.renameChatSession(s.id, renameValue); setRenamingId(null); }}
                      style={{ flex: 1, minWidth: 0, height: 18, boxSizing: "border-box", fontSize: 12, background: "var(--bg3)", border: "1px solid var(--teal)", borderRadius: "var(--radius-sm)", color: "var(--t0)", padding: "0 4px", outline: "none", fontFamily: "var(--font-sans)", lineHeight: "16px" }}
                    />
                  ) : (
                    <>
                      <div style={{ flex: 1, minWidth: 0, fontSize: 12, lineHeight: "18px", color: activeSessionID === s.id ? "var(--t0)" : "var(--t1)", fontWeight: activeSessionID === s.id ? 500 : 400, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
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
                      <div style={{ display: "flex", gap: 1, opacity: hoveredChatId === s.id ? 1 : 0, transition: "opacity 0.15s", flexShrink: 0 }}>
                        <button
                          className="btn btn-ghost btn-sm"
                          aria-label={`Rename chat ${s.title || "Untitled"}`}
                          type="button"
                          onClick={e => { e.stopPropagation(); setRenamingId(s.id); setRenameValue(s.title || ""); }}
                          style={{ padding: "1px 3px" }}
                          title="Rename"
                        >
                          <Icon d={Icons.edit} size={10} />
                        </button>
                        <button
                          className="btn btn-ghost btn-sm"
                          aria-label={`Delete chat ${s.title || "Untitled"}`}
                          type="button"
                          onClick={e => { e.stopPropagation(); void actions.deleteChatSession(s.id); }}
                          style={{ padding: "1px 3px", color: "var(--red)" }}
                          title="Delete"
                        >
                          <Icon d={Icons.trash} size={10} />
                        </button>
                      </div>
                    </>
                  )}
                </div>
                <div style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 10, color: "var(--t3)", marginTop: 1, fontFamily: "var(--font-mono)" }}>
                  <span>{s.message_count} msg</span>
                  {isAgentChat && s.agent_brand && renamingId !== s.id && (
                    <>
                      <span style={{ color: "var(--t4)" }}>·</span>
                      <BrandAvatar
                        brand={s.agent_brand}
                        fallback={s.agent_label || s.agent_brand}
                        title={s.agent_label || s.agent_brand}
                        boxed={false}
                        size={13}
                        style={{ flexShrink: 0 }}
                      />
                    </>
                  )}
                  {isAgentChat
                    ? s.status_label && (
                      <>
                        <span style={{ color: "var(--t4)" }}>·</span>
                        <span>{s.status_label}</span>
                      </>
                    )
                    : (
                      <>
                        <span style={{ color: "var(--t4)" }}>·</span>
                        <span>{s.provider_call_count} call{s.provider_call_count === 1 ? "" : "s"}</span>
                      </>
                    )}
                </div>
              </div>
            ))}
          </div>
        ))}
      </div>
    </div>
  );
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
    ].filter(Boolean).join(" ").toLowerCase();
    return searchable.includes(needle);
  });
}

export function sidebarSessionBrand(session: ChatSessionRecord): string | undefined {
  if (session.runtime_kind === "external_agent" || session.adapter_id) {
    return session.adapter_id;
  }
  if (session.runtime_kind === "agent") {
    return "hecate";
  }
  return session.provider || session.model;
}

export function sidebarSessionAgentLabel(session: ChatSessionRecord, adapters: AgentAdapterRecord[]): string | undefined {
  if (session.runtime_kind === "external_agent" || session.adapter_id) {
    return chatAgentOption(session.adapter_id || "", adapters).label;
  }
  if (session.runtime_kind === "agent") {
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

function groupSidebarSessions(sessions: SidebarSession[]): Array<{ label: string; sessions: SidebarSession[] }> {
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

import { useEffect } from "react";

import { SettingsView } from "../features/settings/SettingsView";
import { CostsView } from "../features/costs/CostsView";
import { ObservabilityView } from "../features/overview/ObservabilityView";
import { ChatView } from "../features/chats/ChatView";
import { ProvidersView } from "../features/providers/ProvidersView";
import { TasksView } from "../features/runs/TasksView";
import type { RuntimeConsoleViewModel } from "./useRuntimeConsole";

export type WorkspaceID = "overview" | "runs" | "chats" | "providers" | "costs" | "settings";

type WorkspaceDefinition = {
  id: WorkspaceID;
  label: string;
  icon: React.ReactNode;
  shortcut: string;
};

type ConsoleState = RuntimeConsoleViewModel["state"];
type ConsoleActions = RuntimeConsoleViewModel["actions"];

// Icon paths match the design handoff
const IC = {
  observe: ["M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z", "M15 12a3 3 0 11-6 0 3 3 0 016 0z"],
  chat:    "M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z",
  tasks:   "M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-6 9l2 2 4-4",
  providers: ["M5 12h14","M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2","M9 10h.01","M9 16h.01"],
  budgets: "M12 8c-1.657 0-3 .895-3 2s1.343 2 3 2 3 .895 3 2-1.343 2-3 2m0-8c1.11 0 2.08.402 2.599 1M12 8V7m0 1v8m0 0v1m0-1c-1.11 0-2.08-.402-2.599-1M21 12a9 9 0 11-18 0 9 9 0 0118 0z",
  // Settings — gear/cog. Distinct from any tab/inline icon so the
  // activity bar's terminal entry reads as configuration at a glance.
  settings: ["M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z", "M15 12a3 3 0 11-6 0 3 3 0 016 0z"],
  // Stack-of-coins outline. Three stacked ellipses with side rails —
  // visually distinct from IC.budgets (the dollar-circle) so the
  // activity bar doesn't have two near-identical glyphs.
  costs:   ["M4 7c0-1.657 3.582-3 8-3s8 1.343 8 3-3.582 3-8 3-8-1.343-8-3z", "M4 7v5c0 1.657 3.582 3 8 3s8-1.343 8-3V7", "M4 12v5c0 1.657 3.582 3 8 3s8-1.343 8-3v-5"],
};

function SvgIcon({ d, size = 18 }: { d: string | string[]; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
      {Array.isArray(d) ? d.map((p, i) => <path key={i} d={p} />) : <path d={d} />}
    </svg>
  );
}

// Workspace lineup, in order:
//   Chats (1) · Providers (2) · Tasks (3) · Observability (4) · Costs (5) · Settings (6)
type WorkspaceLineupEntry = Omit<WorkspaceDefinition, "shortcut">;
const WS: Record<WorkspaceID, WorkspaceLineupEntry> = {
  chats:     { id: "chats",     label: "Chats",         icon: <SvgIcon d={IC.chat} /> },
  providers: { id: "providers", label: "Providers",     icon: <SvgIcon d={IC.providers} /> },
  runs:      { id: "runs",      label: "Tasks",         icon: <SvgIcon d={IC.tasks} /> },
  overview:  { id: "overview",  label: "Observability", icon: <SvgIcon d={IC.observe} /> },
  costs:     { id: "costs",     label: "Costs",         icon: <SvgIcon d={IC.costs} /> },
  settings:  { id: "settings",  label: "Settings",      icon: <SvgIcon d={IC.settings} /> },
};

const BARE_WORKSPACES: WorkspaceID[] = ["chats", "runs"];

export function getAvailableWorkspaces(): WorkspaceDefinition[] {
  const lineup: WorkspaceLineupEntry[] = [WS.chats, WS.providers, WS.runs, WS.overview, WS.costs, WS.settings];
  // Shortcut keys are positional (1..N).
  return lineup.map((ws, i) => ({ ...ws, shortcut: String(i + 1) }));
}

export function ConsoleShell({
  activeWorkspace,
  onSelectWorkspace,
  state,
  actions,
}: {
  activeWorkspace: WorkspaceID;
  onSelectWorkspace: (workspace: WorkspaceID) => void;
  state: ConsoleState;
  actions: ConsoleActions;
}) {
  // No auth: render the full console immediately. The brief
  // first-load splash stays so the workspace doesn't flash with
  // stale state before /healthz returns.
  if (state.health === null && !state.error) {
    return <AuthLoadingShell />;
  }
  return (
    <AuthenticatedShell
      activeWorkspace={activeWorkspace}
      onSelectWorkspace={onSelectWorkspace}
      state={state}
      actions={actions}
    />
  );
}

function AuthLoadingShell() {
  return (
    <div
      className="hecate-shell"
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 16,
      }}
    >
      <div
        style={{
          fontSize: 11,
          color: "var(--t3)",
          fontFamily: "var(--font-mono)",
          letterSpacing: "0.06em",
          textTransform: "uppercase",
        }}
      >
        Connecting…
      </div>
    </div>
  );
}

function AuthenticatedShell({
  activeWorkspace,
  onSelectWorkspace,
  state,
  actions,
}: {
  activeWorkspace: WorkspaceID;
  onSelectWorkspace: (workspace: WorkspaceID) => void;
  state: ConsoleState;
  actions: ConsoleActions;
}) {
  const workspaces = getAvailableWorkspaces();

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement || e.target instanceof HTMLSelectElement) return;
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const idx = parseInt(e.key) - 1;
      if (idx >= 0 && idx < workspaces.length) onSelectWorkspace(workspaces[idx].id);
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [workspaces, onSelectWorkspace]);

  const isBare = BARE_WORKSPACES.includes(activeWorkspace);
  const agentWorkspace = state.activeAgentChatSession?.workspace || state.agentWorkspace;
  const agentWorkspaceBranch = state.activeAgentChatSession?.workspace_branch || state.agentWorkspaceBranch;

  return (
    <div className="hecate-shell">
      <div className="hecate-workarea">
        {/* Activity bar */}
        <nav className="hecate-activitybar" aria-label="Workspace navigation">
          {workspaces.map(ws => (
            <button key={ws.id}
              aria-label={`${ws.label} (${ws.shortcut})`}
              aria-current={activeWorkspace === ws.id ? "page" : undefined}
              className={`hecate-activitybtn${activeWorkspace === ws.id ? " hecate-activitybtn--active" : ""}`}
              onClick={() => onSelectWorkspace(ws.id)}
              title={`${ws.label} — press ${ws.shortcut}`}
              type="button">
              {ws.icon}
              <span className="hecate-activitybtn__key">{ws.shortcut}</span>
            </button>
          ))}
        </nav>

        {/* Main content */}
        <main className="hecate-content">
          {state.error && <div className="page-banner page-banner--error">{state.error}</div>}
          <div className={`console-content${isBare ? " console-content--bare" : ""}`}>
            {activeWorkspace === "overview"   && <ObservabilityView actions={actions} state={state} onNavigate={onSelectWorkspace} />}
            {activeWorkspace === "chats" && <ChatView actions={actions} state={state} />}
            {activeWorkspace === "runs"          && <TasksView />}
            {activeWorkspace === "providers"     && <ProvidersView actions={actions} state={state} />}
            {activeWorkspace === "costs"         && <CostsView actions={actions} state={state} />}
            {activeWorkspace === "settings" && <SettingsView actions={actions} state={state} />}
          </div>
        </main>
      </div>

      {/* Status bar */}
      <div className="hecate-statusbar">
        <span className="hecate-statusbar__brand">hecate</span>
        {state.health?.version && (
          <>
            <span className="hecate-statusbar__sep">|</span>
            <span style={{ fontFamily: "var(--font-mono)" }}>{state.health.version}</span>
          </>
        )}
        <span className="hecate-statusbar__sep">|</span>
        <span>{state.session.label}</span>
        <span className="hecate-statusbar__sep">|</span>
        {/* "configured" = providers in the CP store (operator-added).
            "models" is intersected with the configured set so the count
            reflects models the operator can actually route to from the
            chat picker — env-only models would inflate the number
            without being selectable. Tenant sessions (no controlPlaneConfig)
            see the unfiltered model list since the runtime is their
            only source of truth. */}
        {(() => {
          const configured = state.controlPlaneConfig?.providers ?? null;
          const configuredCount = configured?.length ?? 0;
          const modelCount = configured
            ? state.models.filter(m => {
                const p = m.metadata?.provider;
                return typeof p === "string" && configured.some(c => c.id === p);
              }).length
            : state.models.length;
          return (
            <>
              <span>{configuredCount} configured</span>
              <span className="hecate-statusbar__sep">|</span>
              <span>{modelCount} models</span>
            </>
          );
        })()}
        {activeWorkspace === "chats" && state.chatTarget === "agent" && agentWorkspace && (
          <>
            <span className="hecate-statusbar__sep">|</span>
            <span className="hecate-statusbar__path" title={agentWorkspace}>
              {formatWorkspacePath(agentWorkspace)}
            </span>
            {agentWorkspaceBranch && (
              <>
                <span className="hecate-statusbar__sep">|</span>
                <span title={`git branch: ${agentWorkspaceBranch}`}>git:{agentWorkspaceBranch}</span>
              </>
            )}
          </>
        )}
      </div>

      {/* Toast notifications */}
      {!state.error && state.notice && (
        <div className={`toast toast--${state.notice.kind}`} role="alert">
          <span>{state.notice.message}</span>
          <button className="toast__dismiss" onClick={actions.dismissNotice} type="button">✕</button>
        </div>
      )}
    </div>
  );
}

function formatWorkspacePath(path: string): string {
  const normalized = path.replace(/\/+$/, "");
  const parts = normalized.split("/").filter(Boolean);
  if (parts.length <= 2) {
    return normalized || path;
  }
  return `…/${parts.slice(-2).join("/")}`;
}

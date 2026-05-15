import { Suspense, lazy, useEffect, useState } from "react";

import type { RuntimeConsoleViewModel } from "./useRuntimeConsole";
import type { AgentChatUsageRecord } from "../types/runtime";
import { UpdateBanner } from "../features/shared/UpdateBanner";

// Each workspace view is its own dynamic chunk so the initial
// page load only ships the shell + active workspace, not all six.
// Vite splits each `lazy(() => import(...))` into a separate
// chunk under `dist/assets/`. Runtime cost: a one-time fetch +
// parse the first time the operator visits a workspace; on
// localhost this is sub-100 ms and invisible. Build cost: each
// `<Suspense>` boundary needs a fallback (handled by
// `WorkspaceFallback` below). Test cost: tests that assert on
// workspace content must use `findBy*` (async) instead of
// `getBy*` (sync) to wait for the lazy chunk to resolve.
const SettingsView = lazy(() =>
  import("../features/settings/SettingsView").then(m => ({ default: m.SettingsView })),
);
const UsageView = lazy(() =>
  import("../features/usage/UsageView").then(m => ({ default: m.UsageView })),
);
const ObservabilityView = lazy(() =>
  import("../features/overview/ObservabilityView").then(m => ({ default: m.ObservabilityView })),
);
const ChatView = lazy(() =>
  import("../features/chats/ChatView").then(m => ({ default: m.ChatView })),
);
const ProvidersView = lazy(() =>
  import("../features/providers/ProvidersView").then(m => ({ default: m.ProvidersView })),
);
const TasksView = lazy(() =>
  import("../features/runs/TasksView").then(m => ({ default: m.TasksView })),
);

export type WorkspaceID = "overview" | "runs" | "chats" | "connections" | "usage" | "settings";

type WorkspaceDefinition = {
  id: WorkspaceID;
  label: string;
  icon: React.ReactNode;
};

type ConsoleState = RuntimeConsoleViewModel["state"];
type ConsoleActions = RuntimeConsoleViewModel["actions"];
type TaskFocusRequest = { taskID: string; runID?: string; nonce: number };
type TraceFocusRequest = { requestID: string; nonce: number };

// Icon paths match the design handoff
const IC = {
  observe: ["M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z", "M15 12a3 3 0 11-6 0 3 3 0 016 0z"],
  chat:    "M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z",
  tasks:   "M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-6 9l2 2 4-4",
  connections: ["M9.75 7.5v3.75m4.5-3.75v3.75", "M7.5 11.25h9v2.25a4.5 4.5 0 01-9 0v-2.25z", "M12 18v3", "M9 21h6", "M8.25 3.75v3.75", "M15.75 3.75v3.75"],
  // Settings — gear/cog. Distinct from any tab/inline icon so the
  // activity bar's terminal entry reads as configuration at a glance.
  settings: ["M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z", "M15 12a3 3 0 11-6 0 3 3 0 016 0z"],
  usage:   ["M6 3h12a1 1 0 011 1v17l-3-2-2 2-2-2-2 2-2-2-3 2V4a1 1 0 011-1z", "M8 7h8", "M8 11h8", "M8 15h5"],
};

function SvgIcon({ d, size = 18 }: { d: string | string[]; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
      {Array.isArray(d) ? d.map((p, i) => <path key={i} d={p} />) : <path d={d} />}
    </svg>
  );
}

// Theme — follows the OS by default and persists only an explicit user
// override. The bootstrap script in index.html sets data-theme before
// paint; this hook keeps React state, DOM, localStorage, and live OS
// color-scheme changes in sync afterward.
type Theme = "dark" | "light";
type ThemePreference = Theme | "system";
const THEME_KEY = "hecate.theme";

function readStoredThemePreference(): ThemePreference {
  if (typeof localStorage === "undefined") return "system";
  try {
    const stored = localStorage.getItem(THEME_KEY);
    return stored === "light" || stored === "dark" ? stored : "system";
  } catch {
    return "system";
  }
}

function preferredSystemTheme(): Theme {
  if (typeof window === "undefined" || !window.matchMedia) return "dark";
  return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

function applyTheme(theme: Theme) {
  document.documentElement.setAttribute("data-theme", theme);
}

function useTheme(): [Theme, () => void] {
  const [preference, setPreference] = useState<ThemePreference>(readStoredThemePreference);
  const [systemTheme, setSystemTheme] = useState<Theme>(preferredSystemTheme);
  const theme = preference === "system" ? systemTheme : preference;

  useEffect(() => {
    const query = window.matchMedia?.("(prefers-color-scheme: light)");
    if (!query) return;
    const update = () => setSystemTheme(query.matches ? "light" : "dark");
    update();
    query.addEventListener?.("change", update);
    return () => query.removeEventListener?.("change", update);
  }, []);

  useEffect(() => {
    applyTheme(theme);
    try {
      if (preference === "system") localStorage.removeItem(THEME_KEY);
      else localStorage.setItem(THEME_KEY, preference);
    } catch { /* private mode etc. */ }
  }, [preference, theme]);

  return [theme, () => setPreference(theme === "dark" ? "light" : "dark")];
}

// Sun / moon glyphs for the theme toggle. Match the stroke-only style
// of the activity-bar icons so the toggle reads as part of the rail.
const SunIcon = (
  <SvgIcon d={[
    "M12 3v2", "M12 19v2", "M4.22 4.22l1.42 1.42", "M18.36 18.36l1.42 1.42",
    "M3 12h2", "M19 12h2", "M4.22 19.78l1.42-1.42", "M18.36 5.64l1.42-1.42",
    "M12 8a4 4 0 100 8 4 4 0 000-8z",
  ]} />
);
const MoonIcon = (
  <SvgIcon d="M21 12.79A9 9 0 1111.21 3a7 7 0 009.79 9.79z" />
);

type WorkspaceLineupEntry = WorkspaceDefinition;
const WS: Record<WorkspaceID, WorkspaceLineupEntry> = {
  chats:     { id: "chats",     label: "Chats",         icon: <SvgIcon d={IC.chat} /> },
  connections: { id: "connections", label: "Connections",   icon: <SvgIcon d={IC.connections} /> },
  runs:      { id: "runs",      label: "Tasks",         icon: <SvgIcon d={IC.tasks} /> },
  overview:  { id: "overview",  label: "Observability", icon: <SvgIcon d={IC.observe} /> },
  usage:     { id: "usage",     label: "Usage",         icon: <SvgIcon d={IC.usage} /> },
  settings:  { id: "settings",  label: "Settings",      icon: <SvgIcon d={IC.settings} /> },
};

const BARE_WORKSPACES: WorkspaceID[] = ["chats", "runs"];

export function getAvailableWorkspaces(): WorkspaceDefinition[] {
  return [WS.chats, WS.connections, WS.runs, WS.overview, WS.usage, WS.settings];
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

// WorkspaceFallback fills the content area for the brief moment a
// lazily-loaded workspace chunk is being fetched. Uses the same
// muted style as AuthLoadingShell so a workspace switch reads as
// "still booting that surface" rather than as a separate loading
// state. On localhost the fallback is rarely visible — the chunk
// arrives in <50ms — but it prevents layout flash on slower links
// or the cold cache after a deploy.
function WorkspaceFallback() {
  return (
    <div
      style={{
        padding: 16,
        fontSize: 11,
        color: "var(--t3)",
        fontFamily: "var(--font-mono)",
        letterSpacing: "0.06em",
        textTransform: "uppercase",
      }}
    >
      Loading…
    </div>
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
  const [taskFocusRequest, setTaskFocusRequest] = useState<TaskFocusRequest | null>(null);
  const [traceFocusRequest, setTraceFocusRequest] = useState<TraceFocusRequest | null>(null);
  const [theme, toggleTheme] = useTheme();

  function openTaskFromChat(taskID: string, runID?: string) {
    setTaskFocusRequest({ taskID, runID, nonce: Date.now() });
    onSelectWorkspace("runs");
  }

  function openAgentChatFromTask(sessionID: string) {
    actions.setChatTarget("agent");
    void actions.selectChatSession(sessionID);
    onSelectWorkspace("chats");
  }

  function openTraceFromChat(requestID: string) {
    setTraceFocusRequest({ requestID, nonce: Date.now() });
    onSelectWorkspace("overview");
  }

  const isBare = BARE_WORKSPACES.includes(activeWorkspace);
  const agentWorkspace = state.activeAgentChatSession?.workspace || state.agentWorkspace;
  const agentWorkspaceBranch = state.activeAgentChatSession?.workspace_branch || state.agentWorkspaceBranch;
  const agentUsage = latestAgentUsage(state);
  const agentUsageLabel = formatAgentUsagePill(agentUsage);

  return (
    <div className="hecate-shell">
      <div className="hecate-titlebar-drag-region" data-tauri-drag-region />
      <div className="hecate-workarea">
        {/* Activity bar */}
        <nav className="hecate-activitybar" aria-label="Workspace navigation">
          {workspaces.map(ws => (
            <button key={ws.id}
              aria-label={ws.label}
              aria-current={activeWorkspace === ws.id ? "page" : undefined}
              className={`hecate-activitybtn${activeWorkspace === ws.id ? " hecate-activitybtn--active" : ""}`}
              onClick={() => onSelectWorkspace(ws.id)}
              title={ws.label}
              type="button">
              {ws.icon}
            </button>
          ))}
          {/* Pin theme toggle to the bottom of the rail. The flex spacer
              keeps it visually separated from workspace icons regardless
              of how many workspaces are registered. */}
          <span style={{ flex: 1 }} />
          <button
            aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} theme`}
            className="hecate-activitybtn"
            onClick={toggleTheme}
            title={`Theme: ${theme}`}
            type="button">
            {theme === "dark" ? SunIcon : MoonIcon}
          </button>
        </nav>

        {/* Main content */}
        <main className="hecate-content">
          <UpdateBanner />
          {state.error && <div className="page-banner page-banner--error">{state.error}</div>}
          <div className={`console-content${isBare ? " console-content--bare" : ""}`}>
            <Suspense fallback={<WorkspaceFallback />}>
              {activeWorkspace === "overview"   && <ObservabilityView actions={actions} state={state} onNavigate={onSelectWorkspace} focusRequest={traceFocusRequest} />}
              {activeWorkspace === "chats" && <ChatView actions={actions} state={state} onNavigate={onSelectWorkspace} onOpenTask={openTaskFromChat} onOpenTrace={openTraceFromChat} />}
              {activeWorkspace === "runs"          && <TasksView focusRequest={taskFocusRequest} onOpenAgentChat={openAgentChatFromTask} onOpenTrace={openTraceFromChat} />}
              {activeWorkspace === "connections"     && <ProvidersView actions={actions} state={state} />}
              {activeWorkspace === "usage"         && <UsageView actions={actions} state={state} />}
              {activeWorkspace === "settings" && <SettingsView actions={actions} state={state} />}
            </Suspense>
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
            without being selectable. Tenant sessions (no settingsConfig)
            see the unfiltered model list since the runtime is their
            only source of truth. */}
        {(() => {
          const configured = state.settingsConfig?.providers ?? null;
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
        {activeWorkspace === "chats" && state.chatTarget !== "model" && agentWorkspace && (
          <>
            <span className="hecate-statusbar__sep">|</span>
            <span className="hecate-statusbar__path" title={agentWorkspace}>
              {agentWorkspace}
            </span>
            {agentWorkspaceBranch && (
              <>
                <span className="hecate-statusbar__sep">|</span>
                <span title={`git branch: ${agentWorkspaceBranch}`}>git:{agentWorkspaceBranch}</span>
              </>
            )}
            {agentUsageLabel && (
              <>
                <span className="hecate-statusbar__sep">|</span>
                <span title={formatAgentUsageTitle(agentUsage!)}>{agentUsageLabel}</span>
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

function latestAgentUsage(state: ConsoleState): AgentChatUsageRecord | undefined {
  const messages = state.activeAgentChatSession?.messages ?? [];
  for (let i = messages.length - 1; i >= 0; i -= 1) {
    const usage = messages[i].usage;
    if (usage && hasAgentUsage(usage)) return usage;
  }
  return undefined;
}

function hasAgentUsage(usage?: AgentChatUsageRecord): usage is AgentChatUsageRecord {
  return Boolean(
    usage
    && (usage.context_size
      || usage.context_used
      || usage.reported_cost_amount
      || usage.reported_cost_currency)
  );
}

function formatAgentUsagePill(usage?: AgentChatUsageRecord): string {
  if (!hasAgentUsage(usage)) return "";
  const size = usage.context_size ?? 0;
  const used = usage.context_used ?? 0;
  if (size > 0) {
    const remaining = Math.max(size - used, 0);
    const pct = Math.max(0, Math.min(100, Math.round((remaining / size) * 100)));
    return `context ${pct}% left`;
  }
  if (usage.reported_cost_amount) {
    return `reported cost ${formatReportedCost(usage)}`;
  }
  return "";
}

function formatAgentUsageTitle(usage?: AgentChatUsageRecord): string {
  if (!hasAgentUsage(usage)) return "";
  const parts: string[] = [];
  const size = usage.context_size ?? 0;
  const used = usage.context_used ?? 0;
  if (size > 0) {
    const remaining = Math.max(size - used, 0);
    parts.push(`Context: ${formatNumber(used)} used / ${formatNumber(size)} total · ${formatNumber(remaining)} remaining`);
  }
  if (usage.reported_cost_amount) {
    parts.push(`Reported cost: ${formatReportedCost(usage)}`);
  }
  return parts.join(" · ");
}

function formatReportedCost(usage: AgentChatUsageRecord): string {
  const currency = (usage.reported_cost_currency || "").trim().toUpperCase();
  return currency ? `${usage.reported_cost_amount} ${currency}` : usage.reported_cost_amount || "";
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 }).format(value);
}

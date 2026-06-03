import { Suspense, lazy, useEffect, useState } from "react";

import { useChat, type ChatState } from "./state/chat";
import { useProvidersAndModels } from "./state/providersAndModels";
import { useProjects } from "./state/projects";
import { useRuntime } from "./state/runtime";
import { useSettings } from "./state/settings";
import { useChatTarget } from "./state/derived";
import { useChatActions } from "./state/coordinators/chat";
import { useWiredSettingsActions } from "./state/coordinators/wired";
import { deriveSessionState } from "./runtimeConsoleDashboard";
import type { ChatUsageRecord } from "../types/chat";
import { UpdateBanner } from "../features/shared/UpdateBanner";
import { usePersistedState } from "../lib/persistedState";
import { isTauriOnMacOS } from "../lib/tauri";
import type { ProviderFilter } from "../types/provider";

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
  import("../features/settings/SettingsView").then((m) => ({ default: m.SettingsView })),
);
const UsageView = lazy(() =>
  import("../features/usage/UsageView").then((m) => ({ default: m.UsageView })),
);
const ObservabilityView = lazy(() =>
  import("../features/overview/ObservabilityView").then((m) => ({ default: m.ObservabilityView })),
);
const ChatView = lazy(() =>
  import("../features/chats/ChatView").then((m) => ({ default: m.ChatView })),
);
const ProvidersView = lazy(() =>
  import("../features/providers/ProvidersView").then((m) => ({ default: m.ProvidersView })),
);
const ProjectsView = lazy(() =>
  import("../features/projects/ProjectsView").then((m) => ({ default: m.ProjectsView })),
);
const TasksView = lazy(() =>
  import("../features/runs/TasksView").then((m) => ({ default: m.TasksView })),
);

// Single source of truth for the workspace ID set. Exported as a
// readonly tuple so callers can iterate it (App.tsx's
// parseWorkspaceID guard) and the union type below stays derived —
// adding a workspace updates both surfaces in one place.
export const WORKSPACE_IDS = [
  "overview",
  "projects",
  "runs",
  "chats",
  "connections",
  "usage",
  "settings",
] as const;
export type WorkspaceID = (typeof WORKSPACE_IDS)[number];

type WorkspaceDefinition = {
  id: WorkspaceID;
  label: string;
  icon: React.ReactNode;
};

type TaskFocusRequest = { taskID: string; runID?: string; nonce: number };
type TraceFocusRequest = { requestID: string; nonce: number };
type ProjectChatRequest = { projectID: string; provider?: string; model?: string };

// Icon paths match the design handoff
const IC = {
  observe: [
    "M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z",
    "M15 12a3 3 0 11-6 0 3 3 0 016 0z",
  ],
  chat: "M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z",
  tasks:
    "M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-6 9l2 2 4-4",
  projects: [
    "M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v10a2 2 0 01-2 2H5a2 2 0 01-2-2V7z",
    "M8 12h8",
    "M8 16h5",
  ],
  connections: [
    "M9.75 7.5v3.75m4.5-3.75v3.75",
    "M7.5 11.25h9v2.25a4.5 4.5 0 01-9 0v-2.25z",
    "M12 18v3",
    "M9 21h6",
    "M8.25 3.75v3.75",
    "M15.75 3.75v3.75",
  ],
  // Settings — gear/cog. Distinct from any tab/inline icon so the
  // activity bar's terminal entry reads as configuration at a glance.
  settings: [
    "M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z",
    "M15 12a3 3 0 11-6 0 3 3 0 016 0z",
  ],
  usage: [
    "M6 3h12a1 1 0 011 1v17l-3-2-2 2-2-2-2 2-2-2-3 2V4a1 1 0 011-1z",
    "M8 7h8",
    "M8 11h8",
    "M8 15h5",
  ],
};

function SvgIcon({ d, size = 18 }: { d: string | string[]; size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      style={{ flexShrink: 0 }}
    >
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

const parseThemePreference = (raw: string): ThemePreference | null =>
  raw === "light" || raw === "dark" ? raw : null;

function preferredSystemTheme(): Theme {
  if (typeof window === "undefined" || !window.matchMedia) return "dark";
  return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

function applyTheme(theme: Theme) {
  document.documentElement.setAttribute("data-theme", theme);
}

function useTheme(): [Theme, () => void] {
  // shouldRemove keeps the "system" sentinel out of storage —
  // its absence is what tells the next mount to use system pref.
  const [preference, setPreference] = usePersistedState<ThemePreference>(
    THEME_KEY,
    parseThemePreference,
    "system",
    { shouldRemove: (v) => v === "system" },
  );
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
  }, [theme]);

  return [theme, () => setPreference(theme === "dark" ? "light" : "dark")];
}

// Sun / moon glyphs for the theme toggle. Match the stroke-only style
// of the activity-bar icons so the toggle reads as part of the rail.
const SunIcon = (
  <SvgIcon
    d={[
      "M12 3v2",
      "M12 19v2",
      "M4.22 4.22l1.42 1.42",
      "M18.36 18.36l1.42 1.42",
      "M3 12h2",
      "M19 12h2",
      "M4.22 19.78l1.42-1.42",
      "M18.36 5.64l1.42-1.42",
      "M12 8a4 4 0 100 8 4 4 0 000-8z",
    ]}
  />
);
const MoonIcon = <SvgIcon d="M21 12.79A9 9 0 1111.21 3a7 7 0 009.79 9.79z" />;

type WorkspaceLineupEntry = WorkspaceDefinition;
const WS: Record<WorkspaceID, WorkspaceLineupEntry> = {
  chats: { id: "chats", label: "Chats", icon: <SvgIcon d={IC.chat} /> },
  projects: { id: "projects", label: "Projects", icon: <SvgIcon d={IC.projects} /> },
  connections: { id: "connections", label: "Connections", icon: <SvgIcon d={IC.connections} /> },
  runs: { id: "runs", label: "Tasks", icon: <SvgIcon d={IC.tasks} /> },
  overview: { id: "overview", label: "Observability", icon: <SvgIcon d={IC.observe} /> },
  usage: { id: "usage", label: "Usage", icon: <SvgIcon d={IC.usage} /> },
  settings: { id: "settings", label: "Settings", icon: <SvgIcon d={IC.settings} /> },
};

const BARE_WORKSPACES: WorkspaceID[] = ["chats", "projects", "runs"];

export function getAvailableWorkspaces(): WorkspaceDefinition[] {
  return [WS.chats, WS.projects, WS.runs, WS.connections, WS.overview, WS.usage, WS.settings];
}

export function ConsoleShell({
  activeWorkspace,
  onSelectWorkspace,
}: {
  activeWorkspace: WorkspaceID;
  onSelectWorkspace: (workspace: WorkspaceID) => void;
}) {
  const runtime = useRuntime();
  // No auth: render the full console immediately. The brief
  // first-load splash stays so the workspace doesn't flash with
  // stale state before /healthz returns.
  if (runtime.state.health === null && !runtime.state.error) {
    return <AuthLoadingShell />;
  }
  return (
    <AuthenticatedShell activeWorkspace={activeWorkspace} onSelectWorkspace={onSelectWorkspace} />
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
    <div className="workspace-fallback" role="status" aria-live="polite">
      <span className="workspace-fallback__label">Loading workspace…</span>
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
}: {
  activeWorkspace: WorkspaceID;
  onSelectWorkspace: (workspace: WorkspaceID) => void;
}) {
  const runtime = useRuntime();
  const chat = useChat();
  const projects = useProjects();
  const providersAndModels = useProvidersAndModels();
  const settings = useSettings();
  const chatTarget = useChatTarget();
  const { actions: settingsActions } = useWiredSettingsActions();
  const chatActions = useChatActions({
    chatTarget,
    setNoticeMessage: settingsActions.setNoticeMessage,
  });
  const session = deriveSessionState(runtime.state.sessionInfo);
  const workspaces = getAvailableWorkspaces();
  const [taskFocusRequest, setTaskFocusRequest] = useState<TaskFocusRequest | null>(null);
  const [traceFocusRequest, setTraceFocusRequest] = useState<TraceFocusRequest | null>(null);
  const [theme, toggleTheme] = useTheme();

  function openTaskFromChat(taskID: string, runID?: string) {
    setTaskFocusRequest({ taskID, runID, nonce: Date.now() });
    onSelectWorkspace("runs");
  }

  function openTaskFromProject(taskID: string, runID?: string) {
    setTaskFocusRequest({ taskID, runID, nonce: Date.now() });
    onSelectWorkspace("runs");
  }

  function openChatFromProject(request: ProjectChatRequest) {
    if (request.projectID) {
      void projects.actions.selectProject(request.projectID);
    }
    if (request.provider) {
      chatActions.selectProviderRoute(request.provider as ProviderFilter);
    }
    if (request.model) {
      chat.actions.setModel(request.model);
    }
    void chatActions.createChatSession({
      agentID: "hecate",
      projectID: request.projectID,
      provider: request.provider,
      model: request.model,
    });
    onSelectWorkspace("chats");
  }

  function openChatFromTask(sessionID: string) {
    chatActions.setChatTarget("agent");
    void chatActions.selectChatSession(sessionID);
    onSelectWorkspace("chats");
  }

  function openTraceFromChat(requestID: string) {
    setTraceFocusRequest({ requestID, nonce: Date.now() });
    onSelectWorkspace("overview");
  }

  const isBare = BARE_WORKSPACES.includes(activeWorkspace);
  const agentWorkspace = chat.state.activeChatSession?.workspace || chat.state.agentWorkspace;
  const agentWorkspaceBranch =
    chat.state.activeChatSession?.workspace_branch || chat.state.agentWorkspaceBranch;
  const agentUsage = latestAgentUsage(chat.state.activeChatSession);
  const agentUsageLabel = formatAgentUsagePill(agentUsage);

  // Only macOS gets the overlay-titlebar surface. titleBarStyle:
  // "Overlay" is a macOS-only Tauri config; on Linux/Windows the OS
  // draws its own decorations above the webview, so stacking a custom
  // 28-px strip below them would be redundant chrome. On those
  // platforms the UpdateBanner falls back to its old slot at the top
  // of the workspace content (below).
  const hasOverlayTitlebar = isTauriOnMacOS();

  return (
    <div className="hecate-shell">
      {/* Overlay-titlebar surface. data-tauri-drag-region="deep" lets
          clicks anywhere in the subtree start a window drag, except on
          buttons / inputs (Tauri's drag.js auto-detects clickable
          elements). When there's no update the bar is empty and the
          whole strip drags. */}
      {hasOverlayTitlebar && (
        <div className="hecate-titlebar" data-tauri-drag-region="deep">
          <UpdateBanner />
        </div>
      )}
      <div className="hecate-workarea">
        {/* Activity bar */}
        <nav className="hecate-activitybar" aria-label="Workspace navigation">
          {workspaces.map((ws) => (
            <button
              key={ws.id}
              aria-label={ws.label}
              aria-current={activeWorkspace === ws.id ? "page" : undefined}
              className={`hecate-activitybtn${activeWorkspace === ws.id ? " hecate-activitybtn--active" : ""}`}
              onClick={() => onSelectWorkspace(ws.id)}
              title={ws.label}
              type="button"
            >
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
            type="button"
          >
            {theme === "dark" ? SunIcon : MoonIcon}
          </button>
        </nav>

        {/* Main content */}
        <main className="hecate-content">
          {!hasOverlayTitlebar && <UpdateBanner />}
          {runtime.state.error && (
            <div className="page-banner page-banner--error">{runtime.state.error}</div>
          )}
          <div className={`console-content${isBare ? " console-content--bare" : ""}`}>
            <Suspense fallback={<WorkspaceFallback />}>
              {activeWorkspace === "overview" && (
                <ObservabilityView
                  onNavigate={onSelectWorkspace}
                  focusRequest={traceFocusRequest}
                />
              )}
              {activeWorkspace === "chats" && (
                <ChatView
                  onNavigate={onSelectWorkspace}
                  onOpenTask={openTaskFromChat}
                  onOpenTrace={openTraceFromChat}
                />
              )}
              {activeWorkspace === "runs" && (
                <TasksView
                  focusRequest={taskFocusRequest}
                  onOpenChat={openChatFromTask}
                  onOpenTrace={openTraceFromChat}
                />
              )}
              {activeWorkspace === "projects" && (
                <ProjectsView onOpenChat={openChatFromProject} onOpenTask={openTaskFromProject} />
              )}
              {activeWorkspace === "connections" && <ProvidersView />}
              {activeWorkspace === "usage" && <UsageView />}
              {activeWorkspace === "settings" && <SettingsView />}
            </Suspense>
          </div>
        </main>
      </div>

      {/* Status bar */}
      <div className="hecate-statusbar">
        <span className="hecate-statusbar__brand">hecate</span>
        {runtime.state.health?.version && (
          <>
            <span className="hecate-statusbar__sep">|</span>
            <span style={{ fontFamily: "var(--font-mono)" }}>{runtime.state.health.version}</span>
          </>
        )}
        <span className="hecate-statusbar__sep">|</span>
        <span>{session.label}</span>
        <span className="hecate-statusbar__sep">|</span>
        {/* "configured" = providers in the CP store (operator-added).
            "models" is intersected with the configured set so the count
            reflects models the operator can actually route to from the
            chat picker — env-only models would inflate the number
            without being selectable. Tenant sessions (no settingsConfig)
            see the unfiltered model list since the runtime is their
            only source of truth. */}
        {(() => {
          const configured = settings.state.config?.providers ?? null;
          const configuredCount = configured?.length ?? 0;
          const modelCount = configured
            ? providersAndModels.state.models.filter((m) => {
                const p = m.metadata?.provider;
                return typeof p === "string" && configured.some((c) => c.id === p);
              }).length
            : providersAndModels.state.models.length;
          return (
            <>
              <span>{configuredCount} configured</span>
              <span className="hecate-statusbar__sep">|</span>
              <span>{modelCount} models</span>
            </>
          );
        })()}
        {activeWorkspace === "chats" && agentWorkspace && (
          <>
            <span className="hecate-statusbar__sep">|</span>
            <span className="hecate-statusbar__path" title={agentWorkspace}>
              {agentWorkspace}
            </span>
            {agentWorkspaceBranch && (
              <>
                <span className="hecate-statusbar__sep">|</span>
                <span title={`git branch: ${agentWorkspaceBranch}`}>
                  git:{agentWorkspaceBranch}
                </span>
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
      {!runtime.state.error && settings.state.notice && (
        <div className={`toast toast--${settings.state.notice.kind}`} role="alert">
          <span>{settings.state.notice.message}</span>
          <button className="toast__dismiss" onClick={settings.actions.dismissNotice} type="button">
            ✕
          </button>
        </div>
      )}
    </div>
  );
}

function latestAgentUsage(
  activeChatSession: ChatState["activeChatSession"],
): ChatUsageRecord | undefined {
  const messages = activeChatSession?.messages ?? [];
  for (let i = messages.length - 1; i >= 0; i -= 1) {
    const usage = messages[i].usage;
    if (usage && hasAgentUsage(usage)) return usage;
  }
  return undefined;
}

function hasAgentUsage(usage?: ChatUsageRecord): usage is ChatUsageRecord {
  return Boolean(
    usage &&
    (usage.context_size ||
      usage.context_used ||
      usage.reported_cost_amount ||
      usage.reported_cost_currency),
  );
}

function formatAgentUsagePill(usage?: ChatUsageRecord): string {
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

function formatAgentUsageTitle(usage?: ChatUsageRecord): string {
  if (!hasAgentUsage(usage)) return "";
  const parts: string[] = [];
  const size = usage.context_size ?? 0;
  const used = usage.context_used ?? 0;
  if (size > 0) {
    const remaining = Math.max(size - used, 0);
    parts.push(
      `Context: ${formatNumber(used)} used / ${formatNumber(size)} total · ${formatNumber(remaining)} remaining`,
    );
  }
  if (usage.reported_cost_amount) {
    parts.push(`Reported cost: ${formatReportedCost(usage)}`);
  }
  return parts.join(" · ");
}

function formatReportedCost(usage: ChatUsageRecord): string {
  const currency = (usage.reported_cost_currency || "").trim().toUpperCase();
  return currency ? `${usage.reported_cost_amount} ${currency}` : usage.reported_cost_amount || "";
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 }).format(value);
}

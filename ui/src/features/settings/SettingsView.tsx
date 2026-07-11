import { useEffect, useRef, useState } from "react";
import { useRetention, type RetentionState } from "../../app/state/retention";
import { useRetentionActions } from "../../app/state/coordinators/retention";
import { useWiredDashboardActions } from "../../app/state/coordinators/wired";
import { useSettings } from "../../app/state/settings";
import {
  getPlugins,
  getProjectCoordinationBackendStatus,
  migrateProjectsToCairnline,
  resetSystemData,
  rollbackProjectsCairnlineMigration,
} from "../../lib/api";
import type { PluginRecord } from "../../types/plugin";
import type { ProjectCoordinationBackendStatusRecord } from "../../types/project";
import { Badge, ConfirmModal, Icon, Icons, InlineError } from "../shared/ui";
import { ProjectCoordinationBackendSettings } from "./ProjectCoordinationBackendSettings";
import { SettingsSectionHeader as SectionHeader } from "./SettingsSectionHeader";

export function SettingsView() {
  const retention = useRetention();
  // useRetentionActions takes setNotice — the same setter the
  // shim used to wire success/failure banner toggles through.
  const settings = useSettings();
  const { runRetention } = useRetentionActions({ setNotice: settings.actions.setNotice });
  const dashboardActions = useWiredDashboardActions();
  const loadRetentionRuns = retention.actions.loadRuns;
  const storageBackend = settings.state.config?.backend ?? "memory";
  const durableBackend = storageBackend === "sqlite";
  const [resetOpen, setResetOpen] = useState(false);
  const [resetConfirmation, setResetConfirmation] = useState("");
  const [resetPending, setResetPending] = useState(false);
  const [resetError, setResetError] = useState("");
  const [plugins, setPlugins] = useState<PluginRecord[]>([]);
  const [pluginsLoading, setPluginsLoading] = useState(false);
  const [pluginsError, setPluginsError] = useState("");
  const [projectBackendStatus, setProjectBackendStatus] =
    useState<ProjectCoordinationBackendStatusRecord | null>(null);
  const [projectBackendLoading, setProjectBackendLoading] = useState(false);
  const [projectBackendError, setProjectBackendError] = useState("");
  const [projectMigrationAction, setProjectMigrationAction] = useState<
    "migrate" | "rollback" | null
  >(null);
  const [projectMigrationConfirmation, setProjectMigrationConfirmation] = useState("");
  const [projectMigrationPending, setProjectMigrationPending] = useState<
    "migrate" | "rollback" | null
  >(null);
  const [projectMigrationError, setProjectMigrationError] = useState("");
  // Retention runs aren't in the boot-time dashboard snapshot —
  // fetch on first SettingsView mount so the user doesn't see a
  // permanently empty list. `loadRuns` is a stable useCallback, so
  // a naive dependency is safe, but the explicit first-mount guard
  // documents the intent next to the action.
  const didFetchRetentionRunsRef = useRef(false);
  useEffect(() => {
    if (didFetchRetentionRunsRef.current) return;
    didFetchRetentionRunsRef.current = true;
    void loadRetentionRuns();
  }, [loadRetentionRuns]);

  useEffect(() => {
    void loadPlugins();
    void loadProjectBackendStatus();
  }, []);

  async function loadProjectBackendStatus() {
    setProjectBackendLoading(true);
    setProjectBackendError("");
    try {
      const response = await getProjectCoordinationBackendStatus();
      setProjectBackendStatus(response.data);
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to load project backend status.";
      setProjectBackendError(message);
    } finally {
      setProjectBackendLoading(false);
    }
  }

  async function loadPlugins() {
    setPluginsLoading(true);
    setPluginsError("");
    try {
      const response = await getPlugins();
      setPlugins(response.data ?? []);
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to load plugins.";
      setPluginsError(message);
    } finally {
      setPluginsLoading(false);
    }
  }

  function openProjectMigrationAction(action: "migrate" | "rollback") {
    setProjectMigrationAction(action);
    setProjectMigrationConfirmation("");
    setProjectMigrationError("");
  }

  async function handleProjectMigrationAction() {
    const action = projectMigrationAction;
    if (!action) return;
    const confirmation = action === "migrate" ? "MIGRATE" : "ROLLBACK";
    if (projectMigrationConfirmation !== confirmation) return;

    setProjectMigrationPending(action);
    setProjectMigrationError("");
    settings.actions.setNotice(null);
    try {
      if (action === "migrate") {
        const response = await migrateProjectsToCairnline();
        if (!response.data.verified || !response.data.parity_match) {
          const message =
            "Cairnline migration verification did not pass. The live Cairnline database was left unchanged.";
          setProjectMigrationError(message);
          settings.actions.setNotice({ kind: "error", message });
        } else {
          settings.actions.setNotice({
            kind: "success",
            message: `Migrated ${response.data.project_count} project${response.data.project_count === 1 ? "" : "s"} to the verified Cairnline database.`,
          });
        }
      } else {
        const response = await rollbackProjectsCairnlineMigration();
        if (!response.data.restored) {
          const message =
            response.data.reason === "no_backup"
              ? "No recorded pre-migration Cairnline backup is available to restore."
              : "The pre-migration Cairnline backup was not restored.";
          setProjectMigrationError(message);
          settings.actions.setNotice({ kind: "error", message });
        } else {
          settings.actions.setNotice({
            kind: "success",
            message:
              "Restored the pre-migration Cairnline database. Runtime configuration was not changed.",
          });
        }
      }
      setProjectMigrationAction(null);
      setProjectMigrationConfirmation("");
      await loadProjectBackendStatus();
    } catch (err) {
      const message =
        err instanceof Error ? err.message : `Failed to ${action} the Cairnline project database.`;
      setProjectMigrationError(message);
      settings.actions.setNotice({ kind: "error", message });
    } finally {
      setProjectMigrationPending(null);
    }
  }

  async function handleResetData() {
    if (resetConfirmation !== "RESET") return;
    setResetPending(true);
    setResetError("");
    settings.actions.setNotice(null);
    try {
      const response = await resetSystemData();
      await dashboardActions.loadDashboard();
      setResetOpen(false);
      setResetConfirmation("");
      settings.actions.setNotice({
        kind: "success",
        message: resetSummary(response.data),
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to reset local data.";
      setResetError(message);
      settings.actions.setNotice({ kind: "error", message });
    } finally {
      setResetPending(false);
    }
  }

  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", overflow: "hidden" }}>
      <div style={{ flex: 1, overflowY: "auto", padding: 16 }}>
        <ProjectCoordinationBackendSettings
          error={projectBackendError}
          loading={projectBackendLoading}
          migrationError={projectMigrationError}
          migrationPending={projectMigrationPending}
          status={projectBackendStatus}
          onMigrate={() => openProjectMigrationAction("migrate")}
          onRefresh={() => void loadProjectBackendStatus()}
          onRollback={() => openProjectMigrationAction("rollback")}
        />
        <PluginRegistrySettings
          error={pluginsError}
          loading={pluginsLoading}
          plugins={plugins}
          onRefresh={() => void loadPlugins()}
        />
        <RetentionSettings
          storageBackend={storageBackend}
          state={retention.state}
          setRetentionSubsystems={retention.actions.setSubsystems}
          runRetention={runRetention}
          onOpenReset={() => {
            setResetError("");
            setResetConfirmation("");
            setResetOpen(true);
          }}
        />
      </div>
      {resetOpen && (
        <ConfirmModal
          title={durableBackend ? "Reset local data" : "Reset runtime state"}
          danger
          pending={resetPending}
          confirmDisabled={resetConfirmation !== "RESET"}
          confirmLabel={durableBackend ? "Reset local data" : "Reset runtime state"}
          onClose={() => {
            if (!resetPending) setResetOpen(false);
          }}
          onConfirm={handleResetData}
          message={
            <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
              <div>
                This deletes projects, chats, task history, provider setup, policy rules, and saved
                external-agent grants from Hecate. Running external-agent sessions are closed first.
              </div>
              {!durableBackend && (
                <div style={{ color: "var(--t3)" }}>
                  This server is using memory storage, so the reset clears the current dev runtime
                  state only.
                </div>
              )}
              <div style={{ color: "var(--t3)" }}>
                Workspace files and external CLI auth files stay on disk.
              </div>
              <label style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                <span style={{ fontSize: 11, color: "var(--t3)" }}>Type RESET to continue</span>
                <input
                  className="input"
                  autoFocus
                  value={resetConfirmation}
                  disabled={resetPending}
                  onChange={(event) => setResetConfirmation(event.target.value)}
                />
              </label>
              {resetError && <InlineError message={resetError} />}
            </div>
          }
        />
      )}
      {projectMigrationAction && (
        <ConfirmModal
          title={
            projectMigrationAction === "migrate"
              ? "Migrate Projects to Cairnline"
              : "Restore Cairnline backup"
          }
          danger={projectMigrationAction === "rollback"}
          pending={projectMigrationPending === projectMigrationAction}
          confirmDisabled={
            projectMigrationConfirmation !==
            (projectMigrationAction === "migrate" ? "MIGRATE" : "ROLLBACK")
          }
          confirmLabel={
            projectMigrationAction === "migrate" ? "Migrate and verify" : "Restore backup"
          }
          onClose={() => {
            if (!projectMigrationPending) {
              setProjectMigrationAction(null);
              setProjectMigrationConfirmation("");
            }
          }}
          onConfirm={handleProjectMigrationAction}
          message={
            <div style={{ display: "grid", gap: 12 }}>
              <div>
                {projectMigrationAction === "migrate"
                  ? "Hecate will rebuild a staged Cairnline database from native project stores, verify parity, preserve the current Cairnline database when present, and replace it only after every check passes. This does not change runtime configuration."
                  : "Hecate will replace the live Cairnline database with the backup recorded by the latest migration and remove that migration record. This does not switch runtime configuration back to Hecate."}
              </div>
              <label
                htmlFor="project-migration-confirmation"
                style={{ display: "grid", gap: 6, color: "var(--t2)" }}
              >
                Type {projectMigrationAction === "migrate" ? "MIGRATE" : "ROLLBACK"} to continue
                <input
                  id="project-migration-confirmation"
                  className="input"
                  autoComplete="off"
                  autoFocus
                  disabled={projectMigrationPending !== null}
                  value={projectMigrationConfirmation}
                  onChange={(event) => setProjectMigrationConfirmation(event.target.value)}
                />
              </label>
              {projectMigrationError && <InlineError message={projectMigrationError} />}
            </div>
          }
        />
      )}
    </div>
  );
}

function resetSummary(data: {
  projects_deleted: number;
  project_skills_deleted?: number;
  project_work_rows_deleted?: number;
  project_assistant_proposals_deleted?: number;
  plugins_deleted?: number;
  agent_presets_deleted?: number;
  chat_sessions_deleted: number;
  tasks_deleted: number;
  providers_deleted: number;
  policy_rules_deleted: number;
  agent_approval_grants_deleted: number;
  database_rows_deleted: number;
  cairnline_mirror_files_deleted?: number;
}): string {
  const total =
    data.projects_deleted +
    (data.project_skills_deleted ?? 0) +
    (data.project_work_rows_deleted ?? 0) +
    (data.project_assistant_proposals_deleted ?? 0) +
    (data.plugins_deleted ?? 0) +
    (data.agent_presets_deleted ?? 0) +
    data.chat_sessions_deleted +
    data.tasks_deleted +
    data.providers_deleted +
    data.policy_rules_deleted +
    data.agent_approval_grants_deleted +
    data.database_rows_deleted +
    (data.cairnline_mirror_files_deleted ?? 0);
  if (total === 0) return "Local data was already clean.";
  return `Reset local data. Removed ${total} item${total === 1 ? "" : "s"}.`;
}

const RETENTION_SUBSYSTEMS = [
  {
    id: "trace_snapshots",
    label: "Trace snapshots",
    description: "Old request trace payloads and captured bodies.",
  },
  {
    id: "usage_events",
    label: "Usage events",
    description: "Append-only cloud token and reported-cost rows.",
  },
  {
    id: "audit_events",
    label: "Audit events",
    description: "Operator-facing audit trail entries.",
  },
  {
    id: "provider_history",
    label: "Provider history",
    description: "Provider health and readiness history.",
  },
  {
    id: "turn_events",
    label: "Task turn events",
    description: "Verbose per-turn task-runtime events.",
  },
  {
    id: "chat_approvals",
    label: "External-agent approvals",
    description: "Resolved approval prompts. Durable grants are kept.",
  },
] as const;

function PluginRegistrySettings({
  error,
  loading,
  onRefresh,
  plugins,
}: {
  error: string;
  loading: boolean;
  onRefresh: () => void;
  plugins: PluginRecord[];
}) {
  const capabilityCount = plugins.reduce(
    (sum, plugin) => sum + (plugin.capabilities?.length ?? 0),
    0,
  );
  return (
    <section style={{ marginBottom: 20 }}>
      <SectionHeader
        title="Plugins"
        description="Installed plugin manifests and requested capabilities. Registry entries do not run code or mount tools yet."
        meta={`${plugins.length} plugin${plugins.length === 1 ? "" : "s"} · ${capabilityCount} cap${capabilityCount === 1 ? "" : "s"}`}
        actions={
          <button className="btn btn-ghost btn-sm" disabled={loading} onClick={onRefresh}>
            <Icon d={Icons.refresh} size={13} /> {loading ? "Loading…" : "Refresh"}
          </button>
        }
      />
      {error && <InlineError message={error} />}
      {!error && plugins.length === 0 && !loading && (
        <div className="card" style={{ padding: "14px 16px", color: "var(--t3)", fontSize: 12 }}>
          No plugins installed.
        </div>
      )}
      {plugins.length > 0 && (
        <div style={{ display: "grid", gap: 10 }}>
          {plugins.map((plugin) => (
            <PluginRegistryRow key={plugin.id} plugin={plugin} />
          ))}
        </div>
      )}
    </section>
  );
}

function PluginRegistryRow({ plugin }: { plugin: PluginRecord }) {
  const capabilities = plugin.capabilities ?? [];
  const permissions = [
    ...(plugin.requested_permissions ?? []),
    ...capabilities.flatMap((capability) => capability.requested_permissions ?? []),
  ];
  const unsupported = permissions.filter(
    (permission) => permission.classification === "unsupported",
  );
  const auth = plugin.auth ?? [];
  const unresolvedAuth = auth.filter((binding) => binding.status !== "configured");
  return (
    <article className="card" style={{ padding: "14px 16px" }}>
      <div style={{ display: "flex", alignItems: "flex-start", gap: 12 }}>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            <div style={{ fontSize: 14, fontWeight: 600, color: "var(--t0)" }}>{plugin.name}</div>
            <span style={{ fontSize: 11, color: "var(--t3)", fontFamily: "var(--font-mono)" }}>
              {plugin.id}@{plugin.version}
            </span>
            <Badge status={plugin.enabled ? "enabled" : "disabled"} />
            <Badge
              status={plugin.registry_state === "valid" ? "ok" : "warn"}
              label={plugin.registry_state}
            />
          </div>
          {plugin.description && (
            <div style={{ fontSize: 12, color: "var(--t2)", lineHeight: 1.45, marginTop: 6 }}>
              {plugin.description}
            </div>
          )}
          <div
            style={{
              display: "flex",
              flexWrap: "wrap",
              gap: 8,
              marginTop: 10,
              fontSize: 11,
              color: "var(--t3)",
            }}
          >
            <span>{sourceLabel(plugin)}</span>
            <span>{capabilities.length} capabilities</span>
            <span>{permissions.length} permissions</span>
            <span>{auth.length} auth requests</span>
          </div>
        </div>
      </div>
      {capabilities.length > 0 && (
        <div style={{ display: "grid", gap: 6, marginTop: 12 }}>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
            {capabilities.map((capability) => (
              <span
                key={capability.id}
                className="badge badge-muted"
                title={capability.id}
                style={{ textTransform: "none" }}
              >
                {pluginCapabilityKindLabel(capability.kind)} ·{" "}
                {capability.display_name || capability.id}
              </span>
            ))}
          </div>
          {capabilities
            .filter((capability) => capability.mcp_server)
            .map((capability) => (
              <div
                key={`${capability.id}-mcp`}
                style={{
                  color: "var(--t3)",
                  fontFamily: "var(--font-mono)",
                  fontSize: 11,
                  lineHeight: 1.45,
                }}
              >
                {pluginMCPServerSummary(capability.mcp_server!)}
              </div>
            ))}
        </div>
      )}
      {(unsupported.length > 0 ||
        unresolvedAuth.length > 0 ||
        (plugin.warnings?.length ?? 0) > 0) && (
        <div style={{ display: "grid", gap: 4, marginTop: 12, fontSize: 11, color: "var(--t3)" }}>
          {unsupported.length > 0 && (
            <div>
              Unsupported permissions:{" "}
              {unsupported.map((permission) => permission.value).join(", ")}
            </div>
          )}
          {unresolvedAuth.length > 0 && (
            <div>
              Unresolved auth: {unresolvedAuth.map((binding) => binding.requested_name).join(", ")}
            </div>
          )}
          {plugin.warnings?.map((warning) => (
            <div key={warning}>{warning}</div>
          ))}
        </div>
      )}
    </article>
  );
}

function sourceLabel(plugin: PluginRecord) {
  const kind = plugin.source_kind.replaceAll("_", " ");
  if (!plugin.source_ref) return kind;
  return `${kind}: ${plugin.source_ref}`;
}

function pluginCapabilityKindLabel(kind: string) {
  return kind.replaceAll("_", " ");
}

function pluginMCPServerSummary(
  server: NonNullable<PluginRecord["capabilities"]>[number]["mcp_server"],
) {
  if (!server) return "";
  const route =
    server.transport === "http"
      ? server.url || "http"
      : [server.command, ...(server.args ?? [])].filter(Boolean).join(" ");
  const auth = [
    ...Object.keys(server.env ?? {}).map((key) => `env:${key}`),
    ...Object.keys(server.headers ?? {}).map((key) => `header:${key}`),
  ];
  const policy = server.approval_policy ? ` · approval ${server.approval_policy}` : "";
  const refs = auth.length > 0 ? ` · refs ${auth.join(", ")}` : "";
  return `MCP ${server.name} · ${server.transport}: ${route}${policy}${refs}`;
}

type RetentionSubsystemID = (typeof RETENTION_SUBSYSTEMS)[number]["id"];

const RETENTION_LABELS = Object.fromEntries(
  RETENTION_SUBSYSTEMS.map((s) => [s.id, s.label]),
) as Record<RetentionSubsystemID, string>;

function isRetentionSubsystemID(name: string): name is RetentionSubsystemID {
  return Object.hasOwn(RETENTION_LABELS, name);
}

function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function RetentionSettings({
  storageBackend,
  state,
  setRetentionSubsystems,
  runRetention,
  onOpenReset,
}: {
  storageBackend: string;
  state: RetentionState;
  setRetentionSubsystems: (value: string) => void;
  runRetention: () => Promise<void>;
  onOpenReset: () => void;
}) {
  const durableBackend = storageBackend === "sqlite";
  const runs = state.runs ?? [];
  const lastRun = state.lastRun;
  const lastRunResults = lastRun?.results ?? [];

  // Parse CSV state into a local Set for chip toggles
  const selectedSet = new Set(
    state.subsystems
      .split(",")
      .map((s) => s.trim())
      .filter((s) => RETENTION_SUBSYSTEMS.some((sub) => sub.id === s)),
  );

  function toggleSubsystem(name: string) {
    const next = new Set(selectedSet);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    setRetentionSubsystems([...next].join(","));
  }

  const totalDeleted = lastRunResults
    .filter((r) => !r.skipped)
    .reduce((n, r) => n + (r.deleted ?? 0), 0);
  const maxDeleted = Math.max(1, ...lastRunResults.map((r) => r.deleted ?? 0));

  return (
    <>
      <SectionHeader
        title="Maintenance"
        description="Clean up old local runtime data. Hecate keeps durable configuration, provider credentials, and external-agent grants unless you remove them from Connections."
        meta={`${runs.length} run${runs.length === 1 ? "" : "s"}`}
      />

      {/* Controls */}
      <div className="card" style={{ padding: "14px 16px", marginBottom: 16 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12 }}>
          <div style={{ minWidth: 0 }}>
            <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: 3 }}>
              Run cleanup
            </div>
            <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
              Choose what to sweep now. Leave everything off to run the normal full cleanup.
            </div>
          </div>
          <span
            style={{
              fontSize: 11,
              color: "var(--t3)",
              fontFamily: "var(--font-mono)",
              marginLeft: "auto",
            }}
          >
            {selectedSet.size === 0 ? "full cleanup" : `${selectedSet.size} selected`}
          </span>
          <button
            className="btn btn-primary btn-sm"
            disabled={state.loading}
            onClick={() => void runRetention()}
          >
            <Icon d={Icons.refresh} size={13} /> {state.loading ? "Cleaning…" : "Clean up now"}
          </button>
        </div>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))",
            gap: 8,
          }}
        >
          {RETENTION_SUBSYSTEMS.map((subsystem) => {
            const active = selectedSet.has(subsystem.id);
            return (
              <button
                key={subsystem.id}
                type="button"
                onClick={() => toggleSubsystem(subsystem.id)}
                style={{
                  padding: "10px 11px",
                  borderRadius: "var(--radius-sm)",
                  border: `1px solid ${active ? "var(--teal-border)" : "var(--border)"}`,
                  background: active ? "var(--teal-bg)" : "var(--bg3)",
                  color: active ? "var(--t0)" : "var(--t2)",
                  cursor: "pointer",
                  textAlign: "left",
                  transition: "background 0.1s, color 0.1s, border-color 0.1s",
                }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
                  <span
                    style={{
                      width: 8,
                      height: 8,
                      borderRadius: "50%",
                      background: active ? "var(--teal)" : "var(--border-strong)",
                      flexShrink: 0,
                    }}
                  />
                  <span style={{ fontSize: 12, fontWeight: 500 }}>{subsystem.label}</span>
                </div>
                <div
                  style={{
                    fontSize: 10,
                    color: active ? "var(--t2)" : "var(--t3)",
                    lineHeight: 1.35,
                  }}
                >
                  {subsystem.description}
                </div>
              </button>
            );
          })}
        </div>
        <div style={{ fontSize: 10, color: "var(--t3)", marginTop: 10, lineHeight: 1.45 }}>
          Cleanup follows the retention windows configured in the gateway environment. It never
          deletes provider setup, saved secrets, workspaces, or durable external-agent grants.
        </div>
        {state.error && (
          <div style={{ marginTop: 8 }}>
            <InlineError message={state.error} />
          </div>
        )}
      </div>

      {/* Last run summary */}
      {lastRun && (
        <div className="card" style={{ padding: "14px 16px", marginBottom: 16 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12 }}>
            <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>Last run</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
              {relativeTime(lastRun.finished_at)}
            </span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
              ·
            </span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>
              {lastRun.trigger}
            </span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
              ·
            </span>
            <span
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 11,
                color: totalDeleted > 0 ? "var(--teal)" : "var(--t3)",
              }}
            >
              {totalDeleted} removed
            </span>
          </div>
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            {lastRunResults.map((r) => (
              <div key={r.name} style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span
                  style={{
                    fontSize: 11,
                    color: r.skipped ? "var(--t3)" : "var(--t1)",
                    width: 160,
                    flexShrink: 0,
                  }}
                >
                  {isRetentionSubsystemID(r.name) ? RETENTION_LABELS[r.name] : r.name}
                </span>
                {r.skipped ? (
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 10,
                      color: "var(--t3)",
                      fontStyle: "italic",
                    }}
                  >
                    skipped
                  </span>
                ) : r.error ? (
                  <span
                    style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--red)" }}
                  >
                    {r.error}
                  </span>
                ) : (
                  <>
                    <div
                      style={{
                        flex: 1,
                        height: 4,
                        background: "var(--bg3)",
                        borderRadius: 2,
                        overflow: "hidden",
                      }}
                    >
                      <div
                        style={{
                          height: "100%",
                          width: `${Math.round((r.deleted / maxDeleted) * 100)}%`,
                          background: r.deleted > 0 ? "var(--teal)" : "var(--bg3)",
                          borderRadius: 2,
                        }}
                      />
                    </div>
                    <span
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: 11,
                        color: r.deleted > 0 ? "var(--teal)" : "var(--t3)",
                        width: 48,
                        textAlign: "right",
                        flexShrink: 0,
                      }}
                    >
                      {r.deleted} removed
                    </span>
                  </>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* History */}
      <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: 8 }}>
        History
      </div>
      {runs.length > 0 ? (
        <div className="card" style={{ overflow: "hidden" }}>
          {runs.slice(0, 20).map((r, i) => {
            const del =
              r.results?.filter((s) => !s.skipped).reduce((n, s) => n + (s.deleted ?? 0), 0) ?? 0;
            const errored = r.results?.some((s) => s.error);
            return (
              <div
                key={i}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 10,
                  padding: "8px 14px",
                  borderBottom:
                    i < Math.min(runs.length, 20) - 1 ? "1px solid var(--border)" : "none",
                }}
              >
                <span
                  style={{
                    fontSize: 11,
                    color: "var(--t2)",
                    width: 70,
                    flexShrink: 0,
                    textTransform: "capitalize",
                  }}
                >
                  {r.trigger}
                </span>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
                  {relativeTime(r.finished_at)}
                </span>
                <span
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: 11,
                    color: del > 0 ? "var(--teal)" : "var(--t3)",
                    marginLeft: "auto",
                  }}
                >
                  {del} removed
                </span>
                {errored && <Badge status="down" label="error" />}
              </div>
            );
          })}
        </div>
      ) : (
        <div
          className="card"
          style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}
        >
          No retention runs yet.
        </div>
      )}

      <SectionHeader
        title="Danger zone"
        description={
          durableBackend
            ? "Start over without relaunching the app. External-agent sessions are closed before their chat rows are removed."
            : "Clear this dev server's current in-memory state without relaunching it."
        }
      />
      <div
        className="card"
        style={{
          padding: "14px 16px",
          display: "flex",
          alignItems: "center",
          gap: 12,
        }}
      >
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: 3 }}>
            {durableBackend ? "Reset local data" : "Reset runtime state"}
          </div>
          <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
            {durableBackend
              ? "Delete projects, chats, tasks, provider setup, policy rules, saved external-agent grants, and remaining Hecate database rows. Workspace files and external CLI auth files are not touched."
              : "Delete projects, chats, tasks, provider setup, policy rules, and saved external-agent grants from this in-memory process. Workspace files and external CLI auth files are not touched."}
          </div>
        </div>
        <button
          type="button"
          className="btn btn-danger btn-sm"
          style={{ marginLeft: "auto", flexShrink: 0 }}
          onClick={onOpenReset}
        >
          <Icon d={Icons.trash} size={13} /> Reset…
        </button>
      </div>
    </>
  );
}

import { useEffect, useRef, useState } from "react";
import { useRetention, type RetentionState } from "../../app/state/retention";
import { useRetentionActions } from "../../app/state/coordinators/retention";
import { useSettings } from "../../app/state/settings";
import {
  canUseDesktopCloudConnection,
  getDesktopCloudConnectionStatus,
  startDesktopCloudConnection,
  stopDesktopCloudConnection,
  type DesktopCloudConnectionStatus,
} from "../../lib/cloud-connection";
import { getPlugins } from "../../lib/api";
import type { PluginRecord } from "../../types/plugin";
import { Badge, Icon, Icons, InlineError } from "../shared/ui";
import { SettingsSectionHeader as SectionHeader } from "./SettingsSectionHeader";

export function SettingsView() {
  const retention = useRetention();
  // useRetentionActions takes setNotice — the same setter the
  // shim used to wire success/failure banner toggles through.
  const settings = useSettings();
  const { runRetention } = useRetentionActions({ setNotice: settings.actions.setNotice });
  const loadRetentionRuns = retention.actions.loadRuns;
  const storageBackend = settings.state.config?.backend ?? "memory";
  const [plugins, setPlugins] = useState<PluginRecord[]>([]);
  const [pluginsLoading, setPluginsLoading] = useState(false);
  const [pluginsError, setPluginsError] = useState("");
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
  }, []);

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

  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", overflow: "hidden" }}>
      <div style={{ flex: 1, overflowY: "auto", padding: 16 }}>
        {canUseDesktopCloudConnection() && <DesktopCloudConnectionSettings />}
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
        />
      </div>
    </div>
  );
}

const HCLOUD_CONSOLE_URL = "https://console.hecatehq.com/console";

function DesktopCloudConnectionSettings() {
  const [status, setStatus] = useState<DesktopCloudConnectionStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<"connect" | "disconnect" | "refresh" | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    void refreshStatus("initial");
  }, []);

  async function refreshStatus(reason: "initial" | "manual" = "manual") {
    if (reason === "initial") setLoading(true);
    else setBusy("refresh");
    setError("");
    try {
      setStatus(await getDesktopCloudConnectionStatus());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to read Hecate Cloud status.");
    } finally {
      setLoading(false);
      setBusy(null);
    }
  }

  async function connect() {
    setBusy("connect");
    setError("");
    try {
      setStatus(await startDesktopCloudConnection());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to connect to Hecate Cloud.");
    } finally {
      setBusy(null);
    }
  }

  async function disconnect() {
    setBusy("disconnect");
    setError("");
    try {
      setStatus(await stopDesktopCloudConnection());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to disconnect from Hecate Cloud.");
    } finally {
      setBusy(null);
    }
  }

  const title = loading
    ? "Checking"
    : status?.running
      ? "Connected"
      : status?.available && status.gateway_ready
        ? status.auto_start_enabled
          ? "Reconnect needed"
          : "Ready"
        : status?.available
          ? "Starting"
          : "CLI required";
  const badgeStatus = status?.running
    ? "healthy"
    : status?.auto_start_enabled && status.available && status.gateway_ready
      ? "warn"
      : status?.available && status.gateway_ready
        ? "disabled"
        : "degraded";
  const description =
    status?.message ??
    "Connect this desktop Hecate to Hecate Cloud so you can control it from another device.";
  const canConnect = Boolean(status?.available && status.gateway_ready && !status.running);
  const canDisconnect = Boolean(status?.running);
  const actionDisabled = loading || busy !== null;
  const accessMode = status?.running
    ? "Remote access is on and will reconnect when this app opens."
    : status?.auto_start_enabled
      ? "Remote access is on, but the connector is not running."
      : "Remote access is off until you connect.";

  return (
    <section style={{ marginBottom: 20 }} data-testid="desktop-cloud-connection">
      <SectionHeader
        title="Remote access"
        description="Connect this desktop app to Hecate Cloud for phone and browser access."
        meta={status?.running ? "connected" : "desktop app"}
        actions={
          <button
            className="btn btn-ghost btn-sm"
            disabled={actionDisabled}
            onClick={() => void refreshStatus("manual")}
          >
            <Icon d={Icons.refresh} size={13} /> {busy === "refresh" ? "Checking…" : "Refresh"}
          </button>
        }
      />
      <div className="card" style={{ padding: "15px 16px" }}>
        <div
          style={{
            display: "flex",
            alignItems: "flex-start",
            gap: 18,
            justifyContent: "space-between",
          }}
        >
          <div style={{ display: "flex", gap: 11, minWidth: 0 }}>
            <span
              aria-hidden="true"
              style={{
                width: 10,
                height: 10,
                borderRadius: "50%",
                background: status?.running
                  ? "var(--teal)"
                  : status?.available
                    ? "var(--yellow)"
                    : "var(--border-strong)",
                boxShadow: status?.running ? "0 0 0 3px var(--teal-bg)" : undefined,
                flexShrink: 0,
                marginTop: 5,
              }}
            />
            <div style={{ minWidth: 0 }}>
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  flexWrap: "wrap",
                  gap: 8,
                }}
              >
                <div style={{ fontSize: 13, fontWeight: 650, color: "var(--t0)" }}>{title}</div>
                {!loading && (
                  <Badge
                    status={badgeStatus}
                    label={status?.running ? "on" : status?.auto_start_enabled ? "check" : "off"}
                  />
                )}
              </div>
              <div style={{ marginTop: 4, fontSize: 12, color: "var(--t2)", lineHeight: 1.45 }}>
                {loading ? "Checking Hecate Cloud connection…" : description}
              </div>
              {!loading && (
                <div
                  style={{
                    display: "flex",
                    gap: 8,
                    flexWrap: "wrap",
                    marginTop: 10,
                  }}
                  aria-label="Remote access readiness"
                >
                  <RemoteAccessReadinessChip
                    label="hec CLI"
                    state={status?.available ? "ready" : "missing"}
                  />
                  <RemoteAccessReadinessChip
                    label="Local runtime"
                    state={status?.gateway_ready ? "ready" : "starting"}
                  />
                  <RemoteAccessReadinessChip
                    label="Remote access"
                    state={status?.running ? "connected" : "off"}
                  />
                </div>
              )}
              {status?.last_exit_status && (
                <div
                  style={{
                    marginTop: 9,
                    fontFamily: "var(--font-mono)",
                    fontSize: 11,
                    color: "var(--t3)",
                  }}
                >
                  {status.last_exit_status}
                </div>
              )}
              {status?.hec_path && (
                <div
                  style={{
                    marginTop: 9,
                    fontFamily: "var(--font-mono)",
                    fontSize: 11,
                    color: "var(--t3)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                  title={status.hec_path}
                >
                  {status.hec_path}
                </div>
              )}
              {!loading && (
                <div style={{ marginTop: 9, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
                  {accessMode}
                </div>
              )}
            </div>
          </div>
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap", justifyContent: "flex-end" }}>
            {canDisconnect ? (
              <button className="btn btn-ghost" disabled={actionDisabled} onClick={disconnect}>
                {busy === "disconnect" ? "Disconnecting…" : "Disconnect"}
              </button>
            ) : (
              <button
                className="btn btn-primary"
                disabled={!canConnect || actionDisabled}
                onClick={connect}
              >
                {busy === "connect" ? "Connecting…" : "Connect to Hecate Cloud"}
              </button>
            )}
            <a className="btn btn-ghost" href={HCLOUD_CONSOLE_URL} rel="noreferrer" target="_blank">
              Open Cloud
            </a>
          </div>
        </div>
        {!status?.available && !loading && (
          <div style={{ marginTop: 12, fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
            Install <span style={{ fontFamily: "var(--font-mono)", color: "var(--t1)" }}>hec</span>{" "}
            from Hecate Cloud, then refresh this panel.
          </div>
        )}
        {error && (
          <div style={{ marginTop: 10 }}>
            <InlineError message={error} />
          </div>
        )}
      </div>
    </section>
  );
}

function RemoteAccessReadinessChip({
  label,
  state,
}: {
  label: string;
  state: "ready" | "missing" | "starting" | "connected" | "off";
}) {
  const status = state === "ready" || state === "connected" ? "healthy" : "disabled";
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 6,
        minHeight: 24,
        border: "1px solid var(--border)",
        borderRadius: 999,
        padding: "3px 8px",
        background: "var(--bg2)",
        color: "var(--t2)",
        fontSize: 11,
        fontWeight: 600,
      }}
    >
      <span style={{ color: "var(--t1)" }}>{label}</span>
      <Badge status={status} label={state} />
    </span>
  );
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
}: {
  storageBackend: string;
  state: RetentionState;
  setRetentionSubsystems: (value: string) => void;
  runRetention: () => Promise<void>;
}) {
  const resetTitle =
    storageBackend === "memory"
      ? "Reset runtime state unavailable"
      : storageBackend === "postgres"
        ? "Reset persisted data unavailable"
        : "Reset local data unavailable";
  const resetDescription =
    storageBackend === "memory"
      ? "Restart the runtime to clear Hecate-owned in-memory state. Stop the runtime and use the deployment procedure to clear persistent Cairnline project coordination."
      : storageBackend === "postgres"
        ? "Stop the runtime before clearing its configured Postgres state and any local Cairnline or bootstrap files with the deployment-specific procedure."
        : "Stop the runtime before removing its configured data directory. The running process will not report a partial reset as successful.";
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
        <div
          className="retention-controls"
          style={{
            display: "flex",
            marginBottom: 12,
          }}
        >
          <div style={{ minWidth: 0 }}>
            <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: 3 }}>
              Run cleanup
            </div>
            <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
              Choose what to sweep now. Leave everything off to run the normal full cleanup.
            </div>
          </div>
          <span
            className="retention-summary"
            style={{
              fontSize: 11,
              color: "var(--t3)",
              fontFamily: "var(--font-mono)",
            }}
          >
            {selectedSet.size === 0 ? "full cleanup" : `${selectedSet.size} selected`}
          </span>
          <button
            className="btn btn-primary btn-sm retention-cleanup-button"
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
        description="In-process data reset is disabled until every runtime writer can be quiesced safely."
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
            {resetTitle}
          </div>
          <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
            {resetDescription}
          </div>
        </div>
        <button
          type="button"
          className="btn btn-danger btn-sm"
          style={{ marginLeft: "auto", flexShrink: 0 }}
          disabled
          title="Runtime-wide write quiescence is not available yet"
        >
          <Icon d={Icons.trash} size={13} /> Unavailable
        </button>
      </div>
    </>
  );
}

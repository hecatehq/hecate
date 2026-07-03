import { useEffect, useRef, useState, type ReactNode } from "react";
import { useRetention, type RetentionState } from "../../app/state/retention";
import { useRetentionActions } from "../../app/state/coordinators/retention";
import { useWiredDashboardActions } from "../../app/state/coordinators/wired";
import { useSettings } from "../../app/state/settings";
import { getPlugins, getProjectCoordinationBackendStatus, resetSystemData } from "../../lib/api";
import type { PluginRecord } from "../../types/plugin";
import type {
  ProjectCoordinationBackendProbeRecord,
  ProjectCoordinationBackendStatusRecord,
} from "../../types/project";
import { Badge, ConfirmModal, CopyBtn, Icon, Icons, InlineError } from "../shared/ui";

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
          status={projectBackendStatus}
          onRefresh={() => void loadProjectBackendStatus()}
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
    </div>
  );
}

function resetSummary(data: {
  projects_deleted: number;
  project_skills_deleted?: number;
  project_work_rows_deleted?: number;
  project_assistant_proposals_deleted?: number;
  plugins_deleted?: number;
  agent_profiles_deleted?: number;
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
    (data.agent_profiles_deleted ?? 0) +
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

function SectionHeader({
  title,
  description,
  meta,
  actions,
}: {
  title: string;
  description?: string;
  meta?: string;
  actions?: ReactNode;
}) {
  return (
    <div style={{ display: "flex", alignItems: "flex-start", gap: 12, marginBottom: 12 }}>
      <div style={{ minWidth: 0 }}>
        <div
          style={{
            fontSize: 13,
            fontWeight: 500,
            color: "var(--t0)",
            marginBottom: description ? 3 : 0,
          }}
        >
          {title}
        </div>
        {description && (
          <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>{description}</div>
        )}
      </div>
      {meta && (
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            color: "var(--t3)",
            whiteSpace: "nowrap",
          }}
        >
          {meta}
        </span>
      )}
      {actions && (
        <div style={{ marginLeft: "auto", display: "flex", gap: 8, alignItems: "center" }}>
          {actions}
        </div>
      )}
    </div>
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

function ProjectCoordinationBackendSettings({
  error,
  loading,
  onRefresh,
  status,
}: {
  error: string;
  loading: boolean;
  onRefresh: () => void;
  status: ProjectCoordinationBackendStatusRecord | null;
}) {
  const readRoutes = status?.read_routes ?? [];
  const portableGaps = status?.portable_write_gaps ?? [];
  const orchestratorCapabilities =
    status?.orchestrator_capabilities ?? status?.side_effect_blockers ?? [];
  const migrationBlockers = status?.migration_blockers ?? [];
  const replacementGates = status?.replacement_gates ?? [];
  const statusWarnings = status?.warnings ?? [];
  const nextAction = status?.next_replacement_action;
  const statusBadge = status
    ? status.replacement_ready
      ? "ok"
      : status.configured_backend === "cairnline"
        ? "warn"
        : "disabled"
    : "disabled";
  return (
    <section style={{ marginBottom: 20 }}>
      <SectionHeader
        title="Project coordination"
        description="Current Projects backend authority, Cairnline replacement readiness, and Hecate-owned orchestrator capabilities."
        meta={
          status
            ? `${status.configured_backend} configured · ${status.authoritative_backend} authoritative`
            : "not loaded"
        }
        actions={
          <button className="btn btn-ghost btn-sm" disabled={loading} onClick={onRefresh}>
            <Icon d={Icons.refresh} size={13} /> {loading ? "Loading…" : "Refresh"}
          </button>
        }
      />
      {error && <InlineError message={error} />}
      {!status && !error && (
        <div className="card" style={{ padding: "14px 16px", color: "var(--t3)", fontSize: 12 }}>
          {loading
            ? "Loading project coordination status…"
            : "Project coordination status unavailable."}
        </div>
      )}
      {status && (
        <article className="card" style={{ padding: "14px 16px" }}>
          <div style={{ display: "flex", alignItems: "flex-start", gap: 12 }}>
            <div style={{ minWidth: 0, flex: 1 }}>
              <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
                <div style={{ fontSize: 14, fontWeight: 600, color: "var(--t0)" }}>
                  {projectBackendTitle(status)}
                </div>
                <Badge status={statusBadge} label={status.status.replaceAll("_", " ")} />
                {status.cairnline_connector && (
                  <span className="badge badge-muted" style={{ textTransform: "none" }}>
                    {status.cairnline_connector}
                  </span>
                )}
              </div>
              <div style={{ fontSize: 12, color: "var(--t2)", lineHeight: 1.45, marginTop: 8 }}>
                {projectBackendSummary(status)}
              </div>
              {status.replacement_target && (
                <div
                  style={{
                    color: "var(--t3)",
                    fontSize: 11,
                    lineHeight: 1.45,
                    marginTop: 6,
                  }}
                >
                  Target: {status.replacement_target.replaceAll("_", " ")}
                  {status.replacement_target_detail ? ` · ${status.replacement_target_detail}` : ""}
                </div>
              )}
              {status.replacement_mode && (
                <div
                  style={{
                    color: status.replacement_mode_armed ? "var(--accent)" : "var(--t3)",
                    fontSize: 11,
                    lineHeight: 1.45,
                    marginTop: 4,
                  }}
                >
                  Mode: {status.replacement_mode.replaceAll("_", " ")}
                  {status.replacement_mode_armed ? " armed" : " not armed"}
                  {status.replacement_mode_detail ? ` · ${status.replacement_mode_detail}` : ""}
                </div>
              )}
            </div>
          </div>
          {nextAction && <ProjectBackendNextAction action={nextAction} />}
          {replacementGates.length > 0 && <ProjectBackendGateList gates={replacementGates} />}
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))",
              gap: 8,
              marginTop: 14,
            }}
          >
            <ProjectBackendMetric label="Read routes" value={readRoutes.length} />
            <ProjectBackendMetric label="Portable gaps" value={portableGaps.length} />
            <ProjectBackendMetric label="Orchestrator" value={orchestratorCapabilities.length} />
            <ProjectBackendMetric label="Migration" value={migrationBlockers.length} />
          </div>
          <div style={{ display: "grid", gap: 10, marginTop: 14 }}>
            <ProjectBackendList title="Portable write gaps" items={portableGaps} empty="none" />
            <ProjectBackendList
              title="Hecate orchestrator capabilities"
              items={orchestratorCapabilities}
              empty="none"
            />
            <ProjectBackendList title="Migration blockers" items={migrationBlockers} empty="none" />
            {statusWarnings.length > 0 && (
              <ProjectBackendList title="Runtime boundary" items={statusWarnings} empty="none" />
            )}
          </div>
        </article>
      )}
    </section>
  );
}

function ProjectBackendGateList({
  gates,
}: {
  gates: NonNullable<ProjectCoordinationBackendStatusRecord["replacement_gates"]>;
}) {
  return (
    <div style={{ display: "grid", gap: 8, marginTop: 14 }}>
      <div
        style={{
          color: "var(--t3)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          textTransform: "uppercase",
        }}
      >
        Replacement gates
      </div>
      <div style={{ display: "grid", gap: 7 }}>
        {gates.map((gate) => {
          const probes = projectBackendProbes(gate.probes, gate.probe_urls);
          return (
            <div
              key={gate.id}
              style={{
                border: "1px solid var(--border)",
                borderRadius: "var(--radius-sm)",
                display: "grid",
                gap: 5,
                padding: "8px 10px",
              }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: 7, flexWrap: "wrap" }}>
                <ProjectBackendGateBadge ready={gate.ready} status={gate.status} />
                <span style={{ color: "var(--t0)", fontSize: 12, fontWeight: 600 }}>
                  {gate.id.replaceAll("-", " ")}
                </span>
                {probes.length > 0 && (
                  <span className="badge badge-muted" style={{ textTransform: "none" }}>
                    {probes.length} probe{probes.length === 1 ? "" : "s"}
                  </span>
                )}
              </div>
              <div style={{ color: "var(--t2)", fontSize: 11, lineHeight: 1.45 }}>
                {gate.detail}
              </div>
              {probes.length > 0 && <ProjectBackendProbeList probes={probes} />}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function ProjectBackendGateBadge({ ready, status }: { ready: boolean; status: string }) {
  const className = ready
    ? "badge badge-green"
    : status === "partial" || status === "operator_probe_required"
      ? "badge badge-amber"
      : "badge badge-muted";
  return (
    <span className={className} style={{ textTransform: "none" }}>
      {status.replaceAll("_", " ")}
    </span>
  );
}

function ProjectBackendNextAction({
  action,
}: {
  action: NonNullable<ProjectCoordinationBackendStatusRecord["next_replacement_action"]>;
}) {
  const probes = projectBackendProbes(action.probes, action.probe_urls);
  const configHints = action.config_hints ?? [];
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg2)",
        display: "grid",
        gap: 7,
        marginTop: 14,
        padding: "10px 12px",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <span
          style={{
            color: "var(--t3)",
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            textTransform: "uppercase",
          }}
        >
          Next action
        </span>
        {action.target && (
          <span className="badge badge-muted" style={{ textTransform: "none" }}>
            {action.target}
          </span>
        )}
        {probes.length > 0 && (
          <span className="badge badge-muted" style={{ textTransform: "none" }}>
            {probes.length} probe{probes.length === 1 ? "" : "s"}
          </span>
        )}
      </div>
      <div style={{ color: "var(--t0)", fontSize: 13, fontWeight: 650 }}>{action.label}</div>
      <div style={{ color: "var(--t2)", fontSize: 12, lineHeight: 1.45 }}>{action.detail}</div>
      {probes.length > 0 && <ProjectBackendProbeList probes={probes} checklist />}
      {configHints.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
          {configHints.map((hint) => (
            <span
              key={`${hint.env}:${hint.value}`}
              className="badge badge-muted"
              style={{
                fontFamily: "var(--font-mono)",
                maxWidth: "100%",
                overflowWrap: "anywhere",
                textTransform: "none",
                whiteSpace: "normal",
              }}
              title={hint.detail || `${hint.env}=${hint.value}`}
            >
              {hint.env}={hint.value}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function ProjectBackendProbeList({
  checklist = false,
  probes,
}: {
  checklist?: boolean;
  probes: ProjectCoordinationBackendProbeRecord[];
}) {
  return (
    <div style={{ display: "grid", gap: 4 }}>
      <div
        style={{
          color: "var(--t3)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          textTransform: "uppercase",
        }}
      >
        {checklist ? "Probe checklist" : "Probe routes"}
      </div>
      {checklist && (
        <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.45 }}>
          Run these routes in order from a local operator session. POST rows perform smoke or
          rehearsal actions; GET rows inspect the resulting read model.
        </div>
      )}
      <div style={{ display: "grid", gap: 4 }}>
        {probes.map((probe, index) => (
          <ProjectBackendProbeRow
            key={`${probe.method}:${probe.url}:${index}`}
            checklist={checklist}
            index={index}
            probe={probe}
          />
        ))}
      </div>
    </div>
  );
}

function ProjectBackendProbeRow({
  checklist,
  index,
  probe,
}: {
  checklist: boolean;
  index: number;
  probe: ProjectCoordinationBackendProbeRecord;
}) {
  const method = normalizedProjectBackendProbeMethod(probe.method);
  const plan = projectBackendProbePlan(probe.url, method);
  const copyText = `${method} ${probe.url}`;
  return (
    <div
      style={{
        alignItems: checklist ? "flex-start" : "center",
        background: "var(--bg3)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        color: "var(--t1)",
        display: "grid",
        gap: 7,
        gridTemplateColumns: checklist ? "auto minmax(0, 1fr) auto" : "auto minmax(0, 1fr) auto",
        overflowWrap: "anywhere",
        padding: checklist ? "8px 9px" : "5px 7px",
        whiteSpace: "normal",
      }}
    >
      <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
        {checklist && (
          <span className="badge badge-muted" style={{ textTransform: "none" }}>
            Step {index + 1}
          </span>
        )}
        <span
          className={method === "POST" ? "badge badge-amber" : "badge badge-muted"}
          style={{ flexShrink: 0, textTransform: "none" }}
        >
          {method}
        </span>
      </div>
      <div style={{ display: "grid", gap: checklist ? 3 : 0, minWidth: 0 }}>
        {checklist && (
          <div style={{ color: "var(--t0)", fontSize: 12, fontWeight: 650 }}>{plan.label}</div>
        )}
        {checklist && (
          <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.4 }}>{plan.detail}</div>
        )}
        <code
          style={{
            color: "var(--t1)",
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            overflowWrap: "anywhere",
            whiteSpace: "normal",
          }}
        >
          {probe.url}
        </code>
      </div>
      <CopyBtn text={copyText} />
    </div>
  );
}

function projectBackendProbes(
  probes: ProjectCoordinationBackendProbeRecord[] | undefined,
  probeURLs: string[] | undefined,
): ProjectCoordinationBackendProbeRecord[] {
  if (probes && probes.length > 0) {
    return probes;
  }
  return (probeURLs ?? []).map((url) => ({ method: projectBackendProbeMethod(url), url }));
}

function normalizedProjectBackendProbeMethod(method: string | undefined): string {
  const normalized = (method || "GET").trim().toUpperCase();
  return normalized || "GET";
}

function projectBackendProbeMethod(url: string): string {
  if (
    url === "/hecate/v1/projects/cairnline/sync" ||
    url.endsWith("/cairnline/export") ||
    url.includes("/cairnline/sidecar-")
  ) {
    return "POST";
  }
  return "GET";
}

function projectBackendProbePlan(url: string, method: string): { label: string; detail: string } {
  if (url === "/hecate/v1/projects/cairnline/sync") {
    return {
      label: "Rebuild embedded mirror",
      detail:
        "Refresh the Cairnline mirror from current Hecate project stores before checking reads.",
    };
  }
  if (url === "/hecate/v1/projects/cairnline/mirror-parity") {
    return {
      label: "Verify mirror parity",
      detail: "Compare the existing embedded mirror with Hecate's current project stores.",
    };
  }
  if (url.includes("/embedded-read-model")) {
    return {
      label: "Inspect embedded read model",
      detail: "Read the project projection directly from the embedded Cairnline graph.",
    };
  }
  if (url.includes("/embedded-parity-report")) {
    return {
      label: "Compare embedded route shape",
      detail: "Check that Cairnline-backed cockpit projections still match Hecate route shape.",
    };
  }
  if (url.includes("/read-model")) {
    return {
      label: "Inspect read model",
      detail: "Check the active Cairnline-backed read model for the selected project route.",
    };
  }
  if (url.includes("/parity-report")) {
    return {
      label: "Compare project parity",
      detail: "Compare Cairnline projection output with Hecate's native project view.",
    };
  }
  if (url.includes("/sidecar-connect")) {
    return {
      label: "Connect sidecar client",
      detail: "Start or reuse the local Cairnline MCP sidecar client before sidecar read checks.",
    };
  }
  if (url.includes("/sidecar-probe")) {
    return {
      label: "Probe sidecar tools",
      detail: "Confirm the standalone Cairnline MCP server exposes the expected tools.",
    };
  }
  if (url.includes("/sidecar-")) {
    return {
      label: "Run sidecar smoke",
      detail: "Exercise the named standalone Cairnline MCP smoke route and inspect its evidence.",
    };
  }
  if (url.endsWith("/cairnline/export")) {
    return {
      label: "Export project",
      detail: "Generate a project-level Cairnline export rehearsal for migration review.",
    };
  }
  if (method === "POST") {
    return {
      label: "Run smoke route",
      detail: "Execute the operator probe route and review the returned evidence.",
    };
  }
  return {
    label: "Inspect read route",
    detail: "Read the diagnostic route and confirm the returned state matches the gate detail.",
  };
}

function projectBackendTitle(status: ProjectCoordinationBackendStatusRecord): string {
  if (status.replacement_ready) return "Cairnline owns portable project state";
  if (status.configured_backend === "cairnline") return "Cairnline dogfood active";
  return "Hecate Projects active";
}

function projectBackendSummary(status: ProjectCoordinationBackendStatusRecord): string {
  const readRoutes = status.read_routes?.length ?? 0;
  const portableGaps = status.portable_write_gaps?.length ?? 0;
  const orchestratorCapabilities =
    status.orchestrator_capabilities?.length ?? status.side_effect_blockers?.length ?? 0;
  const migrationBlockers = status.migration_blockers?.length ?? 0;
  if (status.replacement_ready) {
    return (
      status.detail ||
      "All replacement gates are ready and Cairnline is authoritative for portable Projects coordination state."
    );
  }
  if (status.configured_backend !== "cairnline") {
    return "Hecate-native project stores are authoritative. Cairnline bridge diagnostics are available for replacement-readiness checks.";
  }
  if (status.read_model_switch_ready || readRoutes > 0) {
    return `${readRoutes} read route${readRoutes === 1 ? "" : "s"} use Cairnline. ${portableGaps} portable write gap${portableGaps === 1 ? "" : "s"} and ${migrationBlockers} migration blocker${migrationBlockers === 1 ? "" : "s"} remain; ${orchestratorCapabilities} Hecate orchestrator ${orchestratorCapabilities === 1 ? "capability stays" : "capabilities stay"} outside Cairnline core.`;
  }
  return "Cairnline is configured, but live read routes and replacement gates are not ready yet.";
}

function ProjectBackendMetric({ label, value }: { label: string; value: number }) {
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg3)",
        padding: "9px 10px",
      }}
    >
      <div style={{ color: "var(--t3)", fontSize: 10, textTransform: "uppercase" }}>{label}</div>
      <div style={{ color: "var(--t0)", fontSize: 18, fontWeight: 650, marginTop: 3 }}>{value}</div>
    </div>
  );
}

function ProjectBackendList({
  empty,
  items,
  title,
}: {
  empty: string;
  items: string[];
  title: string;
}) {
  return (
    <div style={{ display: "grid", gap: 6 }}>
      <div
        style={{
          color: "var(--t3)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          textTransform: "uppercase",
        }}
      >
        {title}
      </div>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
        {items.length === 0 ? (
          <span className="badge badge-green" style={{ textTransform: "none" }}>
            {empty}
          </span>
        ) : (
          items.map((item) => (
            <span key={item} className="badge badge-muted" style={{ textTransform: "none" }}>
              {item}
            </span>
          ))
        )}
      </div>
    </div>
  );
}

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

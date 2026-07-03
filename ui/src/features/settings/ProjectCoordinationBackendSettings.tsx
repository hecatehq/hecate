import type {
  ProjectCoordinationBackendProbeRecord,
  ProjectCoordinationBackendStatusRecord,
} from "../../types/project";
import { Badge, CopyBtn, Icon, Icons, InlineError } from "../shared/ui";
import { SettingsSectionHeader as SectionHeader } from "./SettingsSectionHeader";

export function ProjectCoordinationBackendSettings({
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
  const migrationRehearsal = status?.migration_rehearsal;
  const nextAction = status?.next_replacement_action;
  const writeSwitchpoints = status?.write_switchpoints ?? [];
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
                {status.cairnline_read_source && (
                  <span className="badge badge-muted" style={{ textTransform: "none" }}>
                    reads: {projectBackendDisplayLabel(status.cairnline_read_source)}
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
          {migrationRehearsal && (
            <ProjectBackendMigrationRehearsal rehearsal={migrationRehearsal} />
          )}
          {writeSwitchpoints.length > 0 && (
            <ProjectBackendSwitchpointList switchpoints={writeSwitchpoints} />
          )}
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

function ProjectBackendMigrationRehearsal({
  rehearsal,
}: {
  rehearsal: NonNullable<ProjectCoordinationBackendStatusRecord["migration_rehearsal"]>;
}) {
  const smoke = rehearsal.embedded_smoke;
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
        Migration rehearsal
      </div>
      <div
        style={{
          border: "1px solid var(--border)",
          borderRadius: "var(--radius-sm)",
          display: "grid",
          gap: 9,
          padding: "9px 10px",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 7, flexWrap: "wrap" }}>
          <span className={projectBackendEvidenceBadge(rehearsal.status)}>
            {projectBackendDisplayLabel(rehearsal.status)}
          </span>
          <span style={{ color: "var(--t0)", fontSize: 12, fontWeight: 650 }}>
            {projectBackendDisplayLabel(rehearsal.operation)}
          </span>
          <span className="badge badge-muted" style={{ textTransform: "none" }}>
            snapshot v{rehearsal.snapshot_version}
          </span>
          <span className="badge badge-muted" style={{ textTransform: "none" }}>
            {projectBackendDisplayLabel(rehearsal.target)}
          </span>
          {rehearsal.refreshes_target && (
            <span className="badge badge-amber" style={{ textTransform: "none" }}>
              refreshes target
            </span>
          )}
        </div>
        <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.45 }}>
          {projectBackendDisplayLabel(rehearsal.import_mode)} from{" "}
          {projectBackendDisplayLabel(rehearsal.source_authority)}
        </div>
        {rehearsal.checklist.length > 0 && (
          <div style={{ display: "grid", gap: 5 }}>
            <div
              style={{
                color: "var(--t3)",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                textTransform: "uppercase",
              }}
            >
              Checklist
            </div>
            {rehearsal.checklist.map((check) => (
              <div
                key={check.id}
                style={{
                  display: "grid",
                  gap: 6,
                  gridTemplateColumns: "auto minmax(0, 1fr)",
                }}
              >
                <span className={projectBackendEvidenceBadge(check.status)}>
                  {projectBackendDisplayLabel(check.status)}
                </span>
                <div style={{ minWidth: 0 }}>
                  <div style={{ color: "var(--t0)", fontSize: 11, fontWeight: 650 }}>
                    {projectBackendDisplayLabel(check.id)}
                  </div>
                  <div style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.35 }}>
                    {check.detail}
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
        {smoke && (
          <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
            <span className={projectBackendEvidenceBadge(smoke.status)}>
              embedded smoke {projectBackendDisplayLabel(smoke.status)}
            </span>
            <span className="badge badge-muted" style={{ textTransform: "none" }}>
              {smoke.project_count} project{smoke.project_count === 1 ? "" : "s"}
            </span>
            <span className="badge badge-muted" style={{ textTransform: "none" }}>
              {smoke.read_route_checks} route check{smoke.read_route_checks === 1 ? "" : "s"}
            </span>
            <span className="badge badge-muted" style={{ textTransform: "none" }}>
              {smoke.launch_packet_error_count} launch error
              {smoke.launch_packet_error_count === 1 ? "" : "s"}
            </span>
          </div>
        )}
        {rehearsal.rollback.length > 0 && (
          <div style={{ display: "grid", gap: 4 }}>
            <div
              style={{
                color: "var(--t3)",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                textTransform: "uppercase",
              }}
            >
              Rollback
            </div>
            <ul style={{ margin: 0, paddingLeft: 18, color: "var(--t2)", fontSize: 11 }}>
              {rehearsal.rollback.map((step) => (
                <li key={step} style={{ lineHeight: 1.45 }}>
                  {step}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </div>
  );
}

function ProjectBackendSwitchpointList({
  switchpoints,
}: {
  switchpoints: NonNullable<ProjectCoordinationBackendStatusRecord["write_switchpoints"]>;
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
        Write switchpoints
      </div>
      <div style={{ display: "grid", gap: 7 }}>
        {switchpoints.map((switchpoint) => {
          const seams = switchpoint.seams ?? [];
          return (
            <div
              key={switchpoint.name}
              style={{
                border: "1px solid var(--border)",
                borderRadius: "var(--radius-sm)",
                display: "grid",
                gap: 6,
                padding: "8px 10px",
              }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: 7, flexWrap: "wrap" }}>
                <span style={{ color: "var(--t0)", fontSize: 12, fontWeight: 650 }}>
                  {projectBackendDisplayLabel(switchpoint.name)}
                </span>
                <span className="badge badge-muted" style={{ textTransform: "none" }}>
                  {projectBackendDisplayLabel(switchpoint.current_authority)}
                </span>
                <span
                  className={projectBackendSwitchpointStateBadge(
                    switchpoint.cairnline_state,
                    switchpoint.live_mirror,
                  )}
                  style={{ textTransform: "none" }}
                >
                  {projectBackendDisplayLabel(switchpoint.cairnline_state)}
                </span>
                {switchpoint.blocks_authority ? (
                  <span className="badge badge-amber" style={{ textTransform: "none" }}>
                    blocks authority
                  </span>
                ) : (
                  <span className="badge badge-green" style={{ textTransform: "none" }}>
                    non-blocking
                  </span>
                )}
                {switchpoint.live_mirror && (
                  <span className="badge badge-muted" style={{ textTransform: "none" }}>
                    live mirror
                  </span>
                )}
                {switchpoint.gap && (
                  <span className="badge badge-muted" style={{ textTransform: "none" }}>
                    gap {projectBackendDisplayLabel(switchpoint.gap)}
                  </span>
                )}
              </div>
              <div style={{ color: "var(--t2)", fontSize: 11, lineHeight: 1.45 }}>
                {switchpoint.detail}
              </div>
              {seams.length > 0 && (
                <div style={{ display: "flex", flexWrap: "wrap", gap: 5 }}>
                  {seams.map((seam) => (
                    <span
                      key={seam}
                      className="badge badge-muted"
                      style={{ fontSize: 9, textTransform: "none" }}
                    >
                      {projectBackendDisplayLabel(seam)}
                    </span>
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
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

function projectBackendEvidenceBadge(status: string): string {
  switch (status) {
    case "complete":
    case "documented":
    case "exported":
    case "passed":
    case "ready":
    case "rehearsed":
    case "verified":
      return "badge badge-green";
    case "blocked":
    case "drift_detected":
    case "failed":
    case "probe_error":
      return "badge badge-red";
    case "not_run":
    case "operator_probe_required":
    case "partial":
    case "rehearsal_incomplete":
      return "badge badge-amber";
    default:
      return "badge badge-muted";
  }
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
            {projectBackendDisplayLabel(action.target)}
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
      {configHints.length > 0 && <ProjectBackendConfigHints hints={configHints} />}
    </div>
  );
}

function ProjectBackendConfigHints({
  hints,
}: {
  hints: NonNullable<
    NonNullable<ProjectCoordinationBackendStatusRecord["next_replacement_action"]>["config_hints"]
  >;
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
        Configuration hints
      </div>
      <div style={{ display: "grid", gap: 4 }}>
        {hints.map((hint) => {
          const assignment = `${hint.env}=${hint.value}`;
          return (
            <div
              key={assignment}
              style={{
                alignItems: "center",
                background: "var(--bg3)",
                border: "1px solid var(--border)",
                borderRadius: "var(--radius-sm)",
                display: "grid",
                gap: 7,
                gridTemplateColumns: "minmax(0, 1fr) auto",
                padding: "7px 9px",
              }}
            >
              <div style={{ display: "grid", gap: hint.detail ? 3 : 0, minWidth: 0 }}>
                <code
                  style={{
                    color: "var(--t1)",
                    fontFamily: "var(--font-mono)",
                    fontSize: 11,
                    overflowWrap: "anywhere",
                    whiteSpace: "normal",
                  }}
                >
                  {assignment}
                </code>
                {hint.detail && (
                  <span style={{ color: "var(--t3)", fontSize: 11, lineHeight: 1.4 }}>
                    {hint.detail}
                  </span>
                )}
              </div>
              <CopyBtn text={assignment} />
            </div>
          );
        })}
      </div>
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
  if (url === "/hecate/v1/projects/backend-status") {
    return {
      label: "Verify backend status",
      detail:
        "Confirm the Projects coordination backend reports the expected connector and next action.",
    };
  }
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

function projectBackendDisplayLabel(value: string): string {
  return value.replaceAll("_", " ").replaceAll("-", " ");
}

function projectBackendListItemLabel(value: string): string {
  if (value.includes(" ")) return value;
  return projectBackendDisplayLabel(value);
}

function projectBackendSwitchpointStateBadge(state: string, liveMirror: boolean): string {
  if (
    state === "authoritative_opt_in" ||
    state === "partial_authoritative_opt_in" ||
    state === "embedded_cutover_armed"
  ) {
    return "badge badge-green";
  }
  if (liveMirror) return "badge badge-amber";
  return "badge badge-muted";
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
              {projectBackendListItemLabel(item)}
            </span>
          ))
        )}
      </div>
    </div>
  );
}

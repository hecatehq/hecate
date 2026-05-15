import type { ReactNode } from "react";
import type { RuntimeConsoleViewModel } from "../../app/useRuntimeConsole";
import { Badge, Icon, Icons, InlineError } from "../shared/ui";

type Props = {
  state: RuntimeConsoleViewModel["state"];
  actions: RuntimeConsoleViewModel["actions"];
};

export function SettingsView({ state, actions }: Props) {
  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", overflow: "hidden" }}>
      <div style={{ flex: 1, overflowY: "auto", padding: 16 }}>
        <RetentionSettings state={state} actions={actions} />
      </div>
    </div>
  );
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
        <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: description ? 3 : 0 }}>{title}</div>
        {description && (
          <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>{description}</div>
        )}
      </div>
      {meta && (
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)", whiteSpace: "nowrap" }}>{meta}</span>
      )}
      {actions && <div style={{ marginLeft: "auto", display: "flex", gap: 8, alignItems: "center" }}>{actions}</div>}
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
    id: "agent_chat_approvals",
    label: "External-agent approvals",
    description: "Resolved approval prompts. Durable grants are kept.",
  },
] as const;

type RetentionSubsystemID = (typeof RETENTION_SUBSYSTEMS)[number]["id"];

const RETENTION_LABELS = Object.fromEntries(
  RETENTION_SUBSYSTEMS.map(s => [s.id, s.label]),
) as Record<RetentionSubsystemID, string>;

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

function RetentionSettings({ state, actions }: Props) {
  const runs = state.retentionRuns ?? [];
  const lastRun = state.retentionLastRun;
  const lastRunResults = lastRun?.results ?? [];

  // Parse CSV state into a local Set for chip toggles
  const selectedSet = new Set(
    state.retentionSubsystems
      .split(",")
      .map(s => s.trim())
      .filter(s => RETENTION_SUBSYSTEMS.some(sub => sub.id === s))
  );

  function toggleSubsystem(name: string) {
    const next = new Set(selectedSet);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    actions.setRetentionSubsystems([...next].join(","));
  }

  const totalDeleted = lastRunResults.filter(r => !r.skipped).reduce((n, r) => n + (r.deleted ?? 0), 0);
  const maxDeleted = Math.max(1, ...lastRunResults.map(r => r.deleted ?? 0));

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
            <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: 3 }}>Run cleanup</div>
            <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
              Choose what to sweep now. Leave everything off to run the normal full cleanup.
            </div>
          </div>
          <span style={{ fontSize: 11, color: "var(--t3)", fontFamily: "var(--font-mono)", marginLeft: "auto" }}>
            {selectedSet.size === 0 ? "full cleanup" : `${selectedSet.size} selected`}
          </span>
          <button className="btn btn-primary btn-sm"
            disabled={state.retentionLoading}
            onClick={() => void actions.runRetention()}>
            <Icon d={Icons.refresh} size={13} /> {state.retentionLoading ? "Cleaning…" : "Clean up now"}
          </button>
        </div>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))", gap: 8 }}>
          {RETENTION_SUBSYSTEMS.map(subsystem => {
            const active = selectedSet.has(subsystem.id);
            return (
              <button key={subsystem.id} type="button" onClick={() => toggleSubsystem(subsystem.id)}
                style={{
                  padding: "10px 11px",
                  borderRadius: "var(--radius-sm)",
                  border: `1px solid ${active ? "var(--teal-border)" : "var(--border)"}`,
                  background: active ? "var(--teal-bg)" : "var(--bg3)",
                  color: active ? "var(--t0)" : "var(--t2)",
                  cursor: "pointer",
                  textAlign: "left",
                  transition: "background 0.1s, color 0.1s, border-color 0.1s",
                }}>
                <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
                  <span style={{
                    width: 8,
                    height: 8,
                    borderRadius: "50%",
                    background: active ? "var(--teal)" : "var(--border-strong)",
                    flexShrink: 0,
                  }} />
                  <span style={{ fontSize: 12, fontWeight: 500 }}>{subsystem.label}</span>
                </div>
                <div style={{ fontSize: 10, color: active ? "var(--t2)" : "var(--t3)", lineHeight: 1.35 }}>
                  {subsystem.description}
                </div>
              </button>
            );
          })}
        </div>
        <div style={{ fontSize: 10, color: "var(--t3)", marginTop: 10, lineHeight: 1.45 }}>
          Cleanup follows the retention windows configured in the gateway environment. It never deletes provider setup, saved secrets, workspaces, or durable external-agent grants.
        </div>
        {state.retentionError && <div style={{ marginTop: 8 }}><InlineError message={state.retentionError} /></div>}
      </div>

      {/* Last run summary */}
      {lastRun && (
        <div className="card" style={{ padding: "14px 16px", marginBottom: 16 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12 }}>
            <span style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)" }}>Last run</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
              {relativeTime(lastRun.finished_at)}
            </span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>·</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t2)" }}>{lastRun.trigger}</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>·</span>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: totalDeleted > 0 ? "var(--teal)" : "var(--t3)" }}>
              {totalDeleted} removed
            </span>
          </div>
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            {lastRunResults.map(r => (
              <div key={r.name} style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span style={{ fontSize: 11, color: r.skipped ? "var(--t3)" : "var(--t1)", width: 160, flexShrink: 0 }}>
                  {RETENTION_LABELS[r.name] ?? r.name}
                </span>
                {r.skipped ? (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", fontStyle: "italic" }}>skipped</span>
                ) : r.error ? (
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--red)" }}>{r.error}</span>
                ) : (
                  <>
                    <div style={{ flex: 1, height: 4, background: "var(--bg3)", borderRadius: 2, overflow: "hidden" }}>
                      <div style={{
                        height: "100%",
                        width: `${Math.round((r.deleted / maxDeleted) * 100)}%`,
                        background: r.deleted > 0 ? "var(--teal)" : "var(--bg3)",
                        borderRadius: 2,
                      }} />
                    </div>
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: r.deleted > 0 ? "var(--teal)" : "var(--t3)", width: 48, textAlign: "right", flexShrink: 0 }}>
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
      <div style={{ fontSize: 13, fontWeight: 500, color: "var(--t0)", marginBottom: 8 }}>History</div>
      {runs.length > 0 ? (
        <div className="card" style={{ overflow: "hidden" }}>
          {runs.slice(0, 20).map((r, i) => {
            const del = r.results?.filter(s => !s.skipped).reduce((n, s) => n + (s.deleted ?? 0), 0) ?? 0;
            const errored = r.results?.some(s => s.error);
            return (
              <div key={i} style={{ display: "flex", alignItems: "center", gap: 10, padding: "8px 14px", borderBottom: i < Math.min(runs.length, 20) - 1 ? "1px solid var(--border)" : "none" }}>
                <span style={{ fontSize: 11, color: "var(--t2)", width: 70, flexShrink: 0, textTransform: "capitalize" }}>{r.trigger}</span>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>{relativeTime(r.finished_at)}</span>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: del > 0 ? "var(--teal)" : "var(--t3)", marginLeft: "auto" }}>
                  {del} removed
                </span>
                {errored && <Badge status="down" label="error" />}
              </div>
            );
          })}
        </div>
      ) : (
        <div className="card" style={{ padding: "24px", textAlign: "center", color: "var(--t3)", fontSize: 12 }}>
          No retention runs yet.
        </div>
      )}
    </>
  );
}

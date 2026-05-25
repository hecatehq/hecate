import { useEffect, useRef, useState, type ReactNode } from "react";
import { useRetention, type RetentionState } from "../../app/state/retention";
import { useRetentionActions } from "../../app/state/coordinators/retention";
import { useWiredDashboardActions } from "../../app/state/coordinators/wired";
import { useSettings } from "../../app/state/settings";
import { resetSystemData } from "../../lib/api";
import { Badge, ConfirmModal, Icon, Icons, InlineError } from "../shared/ui";

export function SettingsView() {
  const retention = useRetention();
  // useRetentionActions takes setNotice — the same setter the
  // shim used to wire success/failure banner toggles through.
  const settings = useSettings();
  const { runRetention } = useRetentionActions({ setNotice: settings.actions.setNotice });
  const dashboardActions = useWiredDashboardActions();
  const loadRetentionRuns = retention.actions.loadRuns;
  const [resetOpen, setResetOpen] = useState(false);
  const [resetConfirmation, setResetConfirmation] = useState("");
  const [resetPending, setResetPending] = useState(false);
  const [resetError, setResetError] = useState("");
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
        <RetentionSettings
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
          title="Reset local data"
          danger
          pending={resetPending}
          confirmDisabled={resetConfirmation !== "RESET"}
          confirmLabel="Reset local data"
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
  chat_sessions_deleted: number;
  tasks_deleted: number;
  providers_deleted: number;
  policy_rules_deleted: number;
  agent_approval_grants_deleted: number;
}): string {
  const total =
    data.projects_deleted +
    data.chat_sessions_deleted +
    data.tasks_deleted +
    data.providers_deleted +
    data.policy_rules_deleted +
    data.agent_approval_grants_deleted;
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
  state,
  setRetentionSubsystems,
  runRetention,
  onOpenReset,
}: {
  state: RetentionState;
  setRetentionSubsystems: (value: string) => void;
  runRetention: () => Promise<void>;
  onOpenReset: () => void;
}) {
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
        description="Start over without relaunching the app. External-agent sessions are closed before their chat rows are removed."
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
            Reset local data
          </div>
          <div style={{ fontSize: 11, color: "var(--t3)", lineHeight: 1.45 }}>
            Delete projects, chats, tasks, provider setup, policy rules, and saved external-agent
            grants. Workspace files and external CLI auth files are not touched.
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

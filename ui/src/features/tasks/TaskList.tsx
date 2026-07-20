import type { ReactNode, RefObject } from "react";

import { taskNavigationURL } from "../../app/navigation";
import type { TaskRecord, TaskScheduleRecord } from "../../types/task";
import {
  EntityIndexHeader,
  EntityIndexHeading,
  EntityIndexList,
  EntityIndexPanel,
  EntityIndexState,
  EntityListRow,
} from "../shared/EntityWorkspace";
import { Badge, Icon, Icons } from "../shared/ui";

import { taskBadgeProps, taskSource } from "./taskDetailHelpers";
import {
  type ScheduleLoadState,
  scheduleSummary,
  scheduleVisibleStatus,
} from "./TaskScheduleControl";

type Props = {
  tasks: TaskRecord[];
  selectedTaskID: string;
  loading: boolean;
  busyAction: string;
  onSelect: (id: string) => void;
  onDelete: (id: string) => void;
  onNewTask: () => void;
  indexHeadingRef?: RefObject<HTMLHeadingElement | null>;
  deletingTaskID?: string;
  newTaskDisabled?: boolean;
  newTaskDisabledReason?: string;
  projectScope?: ReactNode;
  filter?: TaskListFilter;
  onFilterChange?: (filter: TaskListFilter) => void;
  schedulesByTaskID?: ReadonlyMap<string, TaskScheduleRecord>;
  scheduleLoadState?: ScheduleLoadState;
  scheduleLoadError?: string;
  emptyMessage?: string;
};

export type TaskListFilter = "all" | "attention" | "scheduled" | "chat";

const taskFilters: Array<{ id: TaskListFilter; label: string }> = [
  { id: "all", label: "All" },
  { id: "attention", label: "Needs attention" },
  { id: "scheduled", label: "Scheduled" },
  { id: "chat", label: "From chats" },
];

function taskKindLabel(task: TaskRecord): string {
  const kind = task.execution_kind;
  if (!kind) return "";
  if (kind === "shell") return task.shell_command ? `$ ${task.shell_command}` : "shell";
  if (kind === "git") return task.git_command ? `git ${task.git_command}` : "git";
  if (kind === "file") return task.file_path ? task.file_path : "file";
  if (kind === "agent_loop")
    return task.execution_profile === "chat_agent" ? "hecate agent" : "agent";
  return kind;
}

export function TaskList({
  tasks,
  selectedTaskID,
  loading,
  busyAction,
  onSelect,
  onDelete,
  onNewTask,
  indexHeadingRef,
  deletingTaskID = "",
  newTaskDisabled = false,
  newTaskDisabledReason,
  projectScope,
  filter = "all",
  onFilterChange,
  schedulesByTaskID = new Map(),
  scheduleLoadState = "loaded",
  scheduleLoadError = "",
  emptyMessage = "No Tasks yet. Create one above to start its first Run.",
}: Props) {
  const scheduleFilterUnavailable = scheduleLoadState !== "loaded";
  const effectiveFilter = scheduleLoadState === "error" && filter === "scheduled" ? "all" : filter;
  const scheduleFilterStatus =
    scheduleLoadState === "loading"
      ? "Loading Schedule data. Scheduled filtering will be available when it finishes."
      : scheduleLoadState === "error"
        ? `Scheduled filter unavailable: ${scheduleLoadError || "Schedule data could not be loaded"}. Refresh the Task to retry.`
        : "";

  return (
    <EntityIndexPanel aria-label="Tasks">
      {projectScope}
      <EntityIndexHeader>
        <EntityIndexHeading ref={indexHeadingRef} tabIndex={-1}>
          Tasks{tasks.length > 0 ? ` · ${tasks.length}` : ""}
        </EntityIndexHeading>
        <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "4px 8px 8px" }}>
          <button
            className="btn btn-primary btn-sm"
            style={{ flex: 1, justifyContent: "center", minHeight: 30 }}
            onClick={onNewTask}
            disabled={newTaskDisabled}
            title={newTaskDisabled ? newTaskDisabledReason : undefined}
            type="button"
          >
            New task
          </button>
        </div>
        {onFilterChange && (
          <div
            role="group"
            aria-label="Filter tasks"
            style={{ display: "flex", flexWrap: "wrap", gap: 4, padding: "0 8px 8px" }}
          >
            {taskFilters.map((option) => {
              const unavailable = option.id === "scheduled" && scheduleFilterUnavailable;
              const pressed = effectiveFilter === option.id;
              return (
                <button
                  key={option.id}
                  className="btn btn-ghost btn-sm"
                  type="button"
                  aria-pressed={pressed}
                  aria-disabled={unavailable || undefined}
                  aria-describedby={unavailable ? "task-scheduled-filter-status" : undefined}
                  onClick={() => {
                    if (!unavailable) onFilterChange(option.id);
                  }}
                  style={{
                    minHeight: 24,
                    padding: "2px 6px",
                    fontSize: 9,
                    color: pressed ? "var(--t0)" : "var(--t3)",
                    background: pressed ? "var(--bg3)" : undefined,
                    cursor: unavailable ? "not-allowed" : undefined,
                    opacity: unavailable ? 0.55 : undefined,
                  }}
                >
                  {option.label}
                </button>
              );
            })}
            {scheduleFilterUnavailable && (
              <span
                id="task-scheduled-filter-status"
                style={{ color: "var(--t3)", flexBasis: "100%", fontSize: 9, lineHeight: 1.4 }}
              >
                {scheduleFilterStatus}
              </span>
            )}
          </div>
        )}
      </EntityIndexHeader>
      <EntityIndexList>
        {loading && tasks.length === 0 && (
          <EntityIndexState
            busy
            style={{
              minHeight: 120,
              display: "grid",
              placeItems: "center",
              padding: "16px 12px",
            }}
          >
            Loading tasks…
          </EntityIndexState>
        )}
        {!loading && tasks.length === 0 && <EntityIndexState>{emptyMessage}</EntityIndexState>}
        {tasks.map((t) => {
          const source = taskSource(t);
          const schedule = schedulesByTaskID.get(t.id);
          const scheduleStatus = schedule ? scheduleVisibleStatus(schedule) : "";
          const hasRun = Boolean(t.latest_run_id);
          return (
            <EntityListRow
              key={t.id}
              active={selectedTaskID === t.id}
              aria-label={`Task ${t.title || t.prompt || "Untitled task"}${
                hasRun ? "" : ", not started"
              }${scheduleStatus ? `, ${scheduleStatus}` : ""}`}
              href={taskNavigationURL(window.location, {
                taskID: t.id,
                runID: t.latest_run_id,
              })}
              onActivate={() => onSelect(t.id)}
              style={{ padding: "10px 12px" }}
              actions={
                t.status !== "running" ? (
                  <button
                    className="btn btn-ghost btn-sm"
                    style={{ padding: "1px 3px", color: "var(--red)" }}
                    title="Delete"
                    aria-label={`Delete task ${t.title || t.prompt || t.id}`}
                    type="button"
                    disabled={busyAction !== "" || deletingTaskID !== ""}
                    onClick={() => onDelete(t.id)}
                  >
                    <Icon d={Icons.trash} size={10} />
                  </button>
                ) : undefined
              }
            >
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  flexWrap: "wrap",
                  gap: 6,
                  marginBottom: 4,
                }}
              >
                <Badge
                  {...(hasRun
                    ? taskBadgeProps(t.status, t.last_error)
                    : { status: "disabled", label: "not started" })}
                />
                {t.execution_kind && (
                  <span
                    style={{
                      fontSize: 9,
                      color: "var(--teal)",
                      fontFamily: "var(--font-mono)",
                      background: "var(--teal-bg)",
                      padding: "1px 5px",
                      borderRadius: 3,
                    }}
                  >
                    {t.execution_kind}
                  </span>
                )}
                {t.workflow_mode === "qa" && (
                  <span
                    className="badge badge-muted"
                    title="Hecate report-only QA workflow"
                    style={{
                      fontSize: 9,
                      fontFamily: "var(--font-mono)",
                      padding: "1px 5px",
                      flexShrink: 0,
                    }}
                  >
                    QA
                  </span>
                )}
                <span
                  className="badge badge-muted"
                  title={source.title}
                  style={{
                    fontSize: 9,
                    fontFamily: "var(--font-mono)",
                    padding: "1px 5px",
                    flexShrink: 0,
                  }}
                >
                  {source.label}
                </span>
                {schedule && (
                  <span
                    className="badge badge-muted"
                    title={scheduleSummary(schedule)}
                    style={{
                      fontSize: 9,
                      fontFamily: "var(--font-mono)",
                      padding: "1px 5px",
                      flexShrink: 1,
                      maxWidth: "100%",
                      whiteSpace: "normal",
                      overflowWrap: "anywhere",
                    }}
                  >
                    {scheduleStatus}
                  </span>
                )}
                {/* MCP-config chip — surfaced when the task configures
                  one or more external MCP servers, so operators can
                  see at-a-glance which agent_loop tasks bring up
                  external tool sources. The tooltip lists server
                  names; the parsed name/server distinction in the
                  run-detail timeline picks up from there. */}
                {t.mcp_servers && t.mcp_servers.length > 0 && (
                  <span
                    className="badge badge-muted"
                    aria-label={`${t.mcp_servers.length} MCP server${t.mcp_servers.length === 1 ? "" : "s"} configured`}
                    title={`MCP servers: ${t.mcp_servers.map((s) => s.name).join(", ")}`}
                    style={{
                      fontSize: 9,
                      fontFamily: "var(--font-mono)",
                      padding: "1px 5px",
                      flexShrink: 0,
                    }}
                  >
                    MCP × {t.mcp_servers.length}
                  </span>
                )}
                {hasRun && (
                  <span
                    style={{
                      fontSize: 10,
                      color: "var(--t3)",
                      fontFamily: "var(--font-mono)",
                      flexShrink: 0,
                      marginLeft: "auto",
                    }}
                  >
                    Latest Run · {t.latest_run_step_count ?? 0}{" "}
                    {(t.latest_run_step_count ?? 0) === 1 ? "step" : "steps"}
                  </span>
                )}
              </div>
              <div
                style={
                  {
                    fontSize: 12,
                    color: "var(--t0)",
                    lineHeight: 1.4,
                    fontWeight: 500,
                    overflow: "hidden",
                    display: "-webkit-box",
                    WebkitLineClamp: 2,
                    WebkitBoxOrient: "vertical",
                  } as React.CSSProperties
                }
              >
                {t.title || t.prompt || "Untitled task"}
              </div>
              {taskKindLabel(t) && (
                <div
                  style={{
                    fontSize: 10,
                    color: "var(--t3)",
                    fontFamily: "var(--font-mono)",
                    marginTop: 2,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {taskKindLabel(t)}
                </div>
              )}
              {hasRun && (t.latest_model || t.latest_provider) && (
                <div
                  style={{
                    fontSize: 10,
                    color: "var(--t2)",
                    fontFamily: "var(--font-mono)",
                    marginTop: 2,
                    display: "flex",
                    gap: 8,
                    alignItems: "baseline",
                    overflow: "hidden",
                  }}
                >
                  {/* Model + provider from the most recent run. Empty
                    string omits — pre-LLM tasks (shell/git/file kinds
                    with no model routing) would otherwise render an
                    ugly " / " on every row. Run ids live in Task
                    Detail, where they can be copied without turning
                    the task list into an id ledger. */}
                  <span
                    title={
                      t.latest_provider
                        ? `${t.latest_model || ""} via ${t.latest_provider}`
                        : t.latest_model
                    }
                    style={{
                      color: "var(--t3)",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                      minWidth: 0,
                    }}
                  >
                    {t.latest_model || ""}
                    {t.latest_provider && (
                      <span style={{ color: "var(--t3)" }}> / {t.latest_provider}</span>
                    )}
                  </span>
                </div>
              )}
            </EntityListRow>
          );
        })}
      </EntityIndexList>
    </EntityIndexPanel>
  );
}

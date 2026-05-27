import { useEffect, useState, type ReactNode } from "react";

import type { ChatActivityRecord } from "../../types/chat";

import {
  activityDisplay,
  activityLinePrefix,
  activityStatusColor,
  compactAgentActivities,
  compactDetailActivities,
  detailSummaryLabel,
  fileChangesActivity,
  formatDiffStatSummary,
  isActiveAgentActivity,
  isOutputArtifactActivity,
  orderVisibleActivities,
  parseDiffStatRows,
  summarizeTimelineActivities,
  terminalAgentActivity,
  terminalStatusLabel,
} from "./transcriptActivityHelpers";

export { formatDiffStatSummary } from "./transcriptActivityHelpers";

export function DiffStatList({ diffStat }: { diffStat: string }) {
  const rows = parseDiffStatRows(diffStat);
  const summary = formatDiffStatSummary(diffStat);

  if (rows.length === 0) {
    return (
      <div style={{ color: "var(--t2)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
        {summary}
      </div>
    );
  }

  return (
    <div
      style={{
        display: "grid",
        gap: 5,
        padding: "8px 10px",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg2)",
      }}
    >
      {rows.map((row) => (
        <div
          key={row.path}
          style={{
            display: "grid",
            gridTemplateColumns: "minmax(0, 1fr) auto",
            gap: 10,
            alignItems: "baseline",
          }}
        >
          <span
            style={{
              color: "var(--t1)",
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {row.path}
          </span>
          <span
            style={{
              color: "var(--t3)",
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              whiteSpace: "nowrap",
            }}
          >
            {row.change}
          </span>
        </div>
      ))}
      {summary && (
        <div
          style={{
            borderTop: "1px solid var(--border)",
            color: "var(--t2)",
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            marginTop: 2,
            paddingTop: 6,
          }}
        >
          {summary}
        </div>
      )}
    </div>
  );
}

export function TranscriptActivityTimeline({
  activities,
  diffStat,
  defaultOpen = false,
  renderAdvancedActivity,
}: {
  activities: ChatActivityRecord[];
  diffStat?: string;
  defaultOpen?: boolean;
  renderAdvancedActivity?: (activity: ChatActivityRecord) => ReactNode;
}) {
  const visible = orderVisibleActivities(compactAgentActivities(activities, Boolean(diffStat)));
  const details = orderVisibleActivities(compactDetailActivities(activities, Boolean(diffStat)));
  const primaryRaw = diffStat ? [...visible, fileChangesActivity(diffStat)] : visible;
  const primary = summarizeTimelineActivities(primaryRaw);
  const terminal = terminalAgentActivity(activities);
  const hasRunning = !terminal && activities.some(isActiveAgentActivity);
  const [open, setOpen] = useState(hasRunning || defaultOpen);

  useEffect(() => {
    if (hasRunning) {
      setOpen(true);
    }
  }, [hasRunning]);

  if (primary.length === 0 && details.length === 0) return null;

  const plan = primaryRaw.filter((activity) => activity.type === "plan");
  const tools = primaryRaw.filter((activity) => activity.type === "tool_call");
  const failedTools = tools.filter((activity) => activity.status === "failed").length;
  const summary = [
    terminal ? terminalStatusLabel(terminal.status) : hasRunning ? "working" : "details",
    plan.length > 0
      ? `${plan.filter((item) => item.status === "completed").length}/${plan.length} plan`
      : "",
    tools.length > 0
      ? terminal?.status === "cancelled" && failedTools > 0
        ? `${failedTools} interrupted tool${failedTools === 1 ? "" : "s"}`
        : failedTools > 0
          ? `${failedTools} failed tool${failedTools === 1 ? "" : "s"}`
          : `${tools.length} tool${tools.length === 1 ? "" : "s"}`
      : "",
    diffStat ? "workspace changes" : "",
  ]
    .filter(Boolean)
    .join(" · ");

  return (
    <details
      onToggle={(event) => setOpen(event.currentTarget.open)}
      open={open}
      style={{ marginTop: 8 }}
    >
      <summary
        style={{
          cursor: "pointer",
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          color: "var(--t3)",
        }}
      >
        {summary}
      </summary>
      <div
        style={{
          display: "grid",
          gap: 5,
          marginTop: 6,
          padding: "8px 10px",
          border: "1px solid var(--border)",
          borderRadius: "var(--radius-sm)",
          background: "var(--bg2)",
        }}
      >
        {primary.map((activity, index) => (
          <TimelineActivityLine
            key={activity.id || `${activity.type}-${activity.created_at ?? index}`}
            activity={activity}
            renderAdvancedActivity={renderAdvancedActivity}
          />
        ))}
        {details.length > 0 && (
          <details
            style={{
              borderTop: primary.length > 0 ? "1px solid var(--border)" : "none",
              marginTop: primary.length > 0 ? 4 : 0,
              paddingTop: primary.length > 0 ? 6 : 0,
            }}
          >
            <summary
              style={{
                cursor: "pointer",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: "var(--t3)",
              }}
            >
              {detailSummaryLabel(details)}
            </summary>
            <div style={{ display: "grid", gap: 5, marginTop: 6 }}>
              {details.map((activity, index) => (
                <TimelineActivityLine
                  key={activity.id || `detail-${activity.type}-${activity.created_at ?? index}`}
                  activity={activity}
                  renderAdvancedActivity={renderAdvancedActivity}
                />
              ))}
            </div>
          </details>
        )}
      </div>
    </details>
  );
}

function TimelineActivityLine({
  activity,
  renderAdvancedActivity,
}: {
  activity: ChatActivityRecord;
  renderAdvancedActivity?: (activity: ChatActivityRecord) => ReactNode;
}) {
  const line =
    activity.type === "plan" ? (
      <PlanActivityLine activity={activity} />
    ) : (
      <ActivityLine activity={activity} prefix={activityLinePrefix(activity)} />
    );
  const hasAdvanced = Boolean(renderAdvancedActivity?.(activity));
  const [advancedOpen, setAdvancedOpen] = useState(false);
  if (!hasAdvanced) return line;

  return (
    <div style={{ display: "grid", gap: 4, minWidth: 0 }}>
      {line}
      <details
        onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
        style={{ marginLeft: 15 }}
      >
        <summary
          style={{
            cursor: "pointer",
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--t3)",
          }}
        >
          {advancedSummaryLabel(activity)}
        </summary>
        {advancedOpen && (
          <div
            style={{
              marginTop: 6,
              padding: "7px 9px",
              border: "1px solid var(--border)",
              borderRadius: "var(--radius-sm)",
              background: "var(--bg1)",
            }}
          >
            {renderAdvancedActivity?.(activity)}
          </div>
        )}
      </details>
    </div>
  );
}

function advancedSummaryLabel(activity: ChatActivityRecord): string {
  if (activity.type === "changed_files" || activity.type === "files_changed")
    return "Workspace changes";
  if (activity.type === "output") return "Output";
  if (activity.type === "artifact" && isOutputArtifactActivity(activity)) return "Output";
  return "Advanced";
}

function PlanActivityLine({ activity }: { activity: ChatActivityRecord }) {
  return (
    <div style={{ display: "flex", alignItems: "baseline", gap: 8, minWidth: 0 }}>
      <span
        style={{
          color:
            activity.status === "completed"
              ? "var(--green)"
              : activity.status === "in_progress"
                ? "var(--teal)"
                : "var(--t3)",
          flexShrink: 0,
          fontFamily: "var(--font-mono)",
          fontSize: 11,
        }}
      >
        {activity.status === "completed" ? "x" : activity.status === "in_progress" ? ">" : "-"}
      </span>
      <span
        style={{
          color: "var(--t1)",
          fontSize: 11,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {activity.title}
      </span>
      {activity.kind && (
        <span
          style={{
            color: "var(--t3)",
            flexShrink: 0,
            fontFamily: "var(--font-mono)",
            fontSize: 10,
          }}
        >
          {activity.kind}
        </span>
      )}
    </div>
  );
}

function ActivityLine({ activity, prefix }: { activity: ChatActivityRecord; prefix?: string }) {
  const display = activityDisplay(activity);
  return (
    <div style={{ display: "flex", alignItems: "baseline", gap: 8, minWidth: 0 }}>
      <span
        style={{
          width: 7,
          height: 7,
          borderRadius: 999,
          background: activityStatusColor(activity.status),
          flexShrink: 0,
        }}
      />
      {prefix && (
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--t3)",
            whiteSpace: "nowrap",
          }}
        >
          {prefix}
        </span>
      )}
      <span
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          color: "var(--t1)",
          whiteSpace: "nowrap",
        }}
      >
        {display.title}
      </span>
      {display.detail && (
        <span
          style={{
            fontSize: 11,
            color: "var(--t3)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {display.detail}
        </span>
      )}
    </div>
  );
}

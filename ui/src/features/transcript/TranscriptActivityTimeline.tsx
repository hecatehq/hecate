import type { AgentChatActivityRecord } from "../../types/runtime";

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
    <div style={{
      display: "grid",
      gap: 5,
      padding: "8px 10px",
      border: "1px solid var(--border)",
      borderRadius: "var(--radius-sm)",
      background: "var(--bg2)",
    }}>
      {rows.map(row => (
        <div key={row.path} style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr) auto", gap: 10, alignItems: "baseline" }}>
          <span style={{ color: "var(--t1)", fontFamily: "var(--font-mono)", fontSize: 11, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {row.path}
          </span>
          <span style={{ color: "var(--t3)", fontFamily: "var(--font-mono)", fontSize: 11, whiteSpace: "nowrap" }}>
            {row.change}
          </span>
        </div>
      ))}
      {summary && (
        <div style={{ borderTop: "1px solid var(--border)", color: "var(--t2)", fontFamily: "var(--font-mono)", fontSize: 11, marginTop: 2, paddingTop: 6 }}>
          {summary}
        </div>
      )}
    </div>
  );
}

export function formatDiffStatSummary(diffStat: string): string {
  const lines = diffStat.split(/\r?\n/).map(line => line.trim()).filter(Boolean);
  return lines.find(line => /\bfiles? changed\b/.test(line)) || lines[0] || "";
}

export function TranscriptActivityTimeline({ activities, diffStat }: { activities: AgentChatActivityRecord[]; diffStat?: string }) {
  const visible = compactAgentActivities(activities);
  if (visible.length === 0) return null;
  const terminal = terminalAgentActivity(activities);
  const hasRunning = !terminal && activities.some(isActiveAgentActivity);
  const plan = visible.filter(activity => activity.type === "plan");
  const tools = visible.filter(activity => activity.type === "tool_call");
  const other = visible.filter(activity => activity.type !== "plan" && activity.type !== "tool_call");
  const summary = [
    terminal ? terminal.status : hasRunning ? "running" : "details",
    plan.length > 0 ? `${plan.filter(item => item.status === "completed").length}/${plan.length} plan` : "",
    tools.length > 0 ? `${tools.length} tool${tools.length === 1 ? "" : "s"}` : "",
    diffStat ? "files changed" : "",
  ].filter(Boolean).join(" · ");

  return (
    <details open={hasRunning} style={{ marginTop: 8 }}>
      <summary style={{ cursor: "pointer", fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t3)" }}>
        {summary}
      </summary>
      <div style={{
        display: "grid",
        gap: 5,
        marginTop: 6,
        padding: "8px 10px",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-sm)",
        background: "var(--bg2)",
      }}>
        {plan.length > 0 && <PlanActivityList items={plan} />}
        {tools.length > 0 && <ToolActivityList items={tools} />}
        {other.map((activity, index) => (
          <ActivityLine key={activity.id || `${activity.type}-${activity.created_at ?? index}`} activity={activity} />
        ))}
      </div>
    </details>
  );
}

function parseDiffStatRows(diffStat: string): Array<{ path: string; change: string }> {
  return diffStat
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .filter(line => !/\bfiles? changed\b/.test(line))
    .map(line => {
      const match = line.match(/^(.+?)\s+\|\s+(.+)$/);
      if (!match) return null;
      return { path: match[1].trim(), change: match[2].trim() };
    })
    .filter((row): row is { path: string; change: string } => row !== null);
}

function PlanActivityList({ items }: { items: AgentChatActivityRecord[] }) {
  return (
    <div style={{ display: "grid", gap: 5 }}>
      {items.map((activity, index) => (
        <div key={activity.id || `${activity.title}-${index}`} style={{ display: "flex", alignItems: "baseline", gap: 8, minWidth: 0 }}>
          <span style={{ color: activity.status === "completed" ? "var(--green)" : activity.status === "in_progress" ? "var(--teal)" : "var(--t3)", flexShrink: 0, fontFamily: "var(--font-mono)", fontSize: 11 }}>
            {activity.status === "completed" ? "x" : activity.status === "in_progress" ? ">" : "-"}
          </span>
          <span style={{ color: "var(--t1)", fontSize: 11, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {activity.title}
          </span>
          {activity.kind && (
            <span style={{ color: "var(--t3)", flexShrink: 0, fontFamily: "var(--font-mono)", fontSize: 10 }}>
              {activity.kind}
            </span>
          )}
        </div>
      ))}
    </div>
  );
}

function ToolActivityList({ items }: { items: AgentChatActivityRecord[] }) {
  return (
    <div style={{ display: "grid", gap: 5 }}>
      {items.map((activity, index) => (
        <ActivityLine key={activity.id || `${activity.type}-${activity.created_at ?? index}`} activity={activity} prefix={activity.kind || "tool"} />
      ))}
    </div>
  );
}

function ActivityLine({ activity, prefix }: { activity: AgentChatActivityRecord; prefix?: string }) {
  return (
    <div style={{ display: "flex", alignItems: "baseline", gap: 8, minWidth: 0 }}>
      <span style={{
        width: 7,
        height: 7,
        borderRadius: 999,
        background: activityStatusColor(activity.status),
        flexShrink: 0,
      }} />
      {prefix && (
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--t3)", whiteSpace: "nowrap" }}>
          {prefix}
        </span>
      )}
      <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--t1)", whiteSpace: "nowrap" }}>
        {activity.title}
      </span>
      {activity.detail && (
        <span style={{ fontSize: 11, color: "var(--t3)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {activity.detail}
        </span>
      )}
    </div>
  );
}

function compactAgentActivities(activities: AgentChatActivityRecord[]): AgentChatActivityRecord[] {
  const hiddenTypes = new Set(["output"]);
  const terminal = terminalAgentActivity(activities);
  const out: AgentChatActivityRecord[] = [];
  for (const activity of activities) {
    if (hiddenTypes.has(activity.type)) continue;
    if (activity.type === "completed" && activity.title.toLowerCase() === "final answer") continue;
    if (terminal && (activity.type === "started" || activity.type === "running")) continue;
    if (activity.type === "running" && activities.some(item => item.type === "output")) continue;
    out.push(activity);
  }
  return out;
}

function terminalAgentActivity(activities: AgentChatActivityRecord[]): AgentChatActivityRecord | undefined {
  const terminalTypes = new Set(["completed", "failed", "cancelled"]);
  return [...activities].reverse().find(activity => terminalTypes.has(activity.type));
}

function activityStatusColor(status?: string) {
  switch (status) {
  case "failed":
    return "var(--red)";
  case "cancelled":
    return "var(--amber)";
  case "running":
  case "in_progress":
    return "var(--teal)";
  default:
    return "var(--green)";
  }
}

function isActiveAgentActivity(activity: AgentChatActivityRecord): boolean {
  return activity.status === "running" || activity.status === "in_progress";
}

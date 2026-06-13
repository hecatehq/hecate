import { useMemo, type CSSProperties } from "react";

import { formatAbsoluteTime } from "../../lib/format";
import type {
  ProjectActivityData,
  ProjectCollaborationArtifactRecord,
  ProjectHandoffRecord,
  ProjectMemoryCandidateRecord,
  ProjectMemoryRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { Badge, Icon, Icons } from "../shared/ui";
import {
  activitySignalLabel,
  buildProjectTimelineItems,
  timelineBadgeClass,
  timelineKindLabel,
  type ProjectTimelineItem,
} from "./projectInsights";
import {
  buildProjectAssignmentChatLaunchRequest,
  type ProjectAssignmentChatLaunchRequest,
} from "./ProjectWorkItemDetail";
import { shortID } from "./projectUtils";

export type ProjectTimelinePanelProps = {
  activity: ProjectActivityData | null;
  artifacts: ProjectCollaborationArtifactRecord[];
  handoffs: ProjectHandoffRecord[];
  memoryCandidates: ProjectMemoryCandidateRecord[];
  memoryEntries: ProjectMemoryRecord[];
  onEditMemory: (entry: ProjectMemoryRecord) => void;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onSelectWorkItem: (workItemID: string) => void;
  project: ProjectRecord | null;
  roles: ProjectWorkRoleRecord[];
  workItems: ProjectWorkItemRecord[];
};

export function ProjectTimelinePanel({
  activity,
  artifacts,
  handoffs,
  memoryCandidates,
  memoryEntries,
  onEditMemory,
  onOpenChat,
  onOpenTask,
  onSelectWorkItem,
  project,
  roles,
  workItems,
}: ProjectTimelinePanelProps) {
  const timeline = useMemo(
    () =>
      project
        ? buildProjectTimelineItems({
            activity,
            artifacts,
            handoffs,
            memoryCandidates,
            memoryEntries,
            project,
            roles,
            workItems,
          })
        : [],
    [activity, artifacts, handoffs, memoryCandidates, memoryEntries, project, roles, workItems],
  );
  const decisions = timeline.filter((item) => item.kind === "decision");
  const timelineLimit = 12;
  const decisionLimit = 5;
  const visibleTimeline = timeline.slice(0, timelineLimit);
  const visibleDecisions = decisions.slice(0, decisionLimit);
  if (!project) return null;

  return (
    <div>
      <div style={panelStyle}>
        <div style={{ display: "flex", alignItems: "flex-start", gap: 10, marginBottom: 12 }}>
          <div>
            <div style={sectionLabelStyle}>Timeline / Decision Log</div>
            <div style={{ ...subtleTextStyle, marginTop: 3 }}>
              {timeline.length} project story item{timeline.length === 1 ? "" : "s"} from activity,
              memory, and collaboration artifacts.
            </div>
          </div>
          <span className="badge badge-muted" style={{ marginLeft: "auto" }}>
            {decisions.length} decision{decisions.length === 1 ? "" : "s"}
          </span>
        </div>
        <div style={timelineGridStyle}>
          <section aria-label="Project timeline" style={{ minWidth: 0 }}>
            <div style={{ ...sectionLabelStyle, color: "var(--t2)", marginBottom: 8 }}>
              Project Timeline
            </div>
            {timeline.length === 0 ? (
              <div style={subtleTextStyle}>
                No timeline entries yet. Assignments, memory changes, and collaboration artifacts
                will appear here.
              </div>
            ) : (
              <div style={{ display: "grid", gap: 9 }}>
                {timeline.length > visibleTimeline.length ? (
                  <div style={subtleTextStyle}>
                    Showing {visibleTimeline.length} of {timeline.length} story items.
                  </div>
                ) : null}
                {visibleTimeline.map((item) => (
                  <ProjectTimelineRow
                    key={item.id}
                    item={item}
                    onEditMemory={onEditMemory}
                    onOpenChat={onOpenChat}
                    onOpenTask={onOpenTask}
                    onSelectWorkItem={onSelectWorkItem}
                    project={project}
                    roles={roles}
                    workItems={workItems}
                  />
                ))}
              </div>
            )}
          </section>
          <section aria-label="Decision log" style={decisionLogStyle}>
            <div style={{ ...sectionLabelStyle, color: "var(--t2)", marginBottom: 8 }}>
              Decisions
            </div>
            {decisions.length === 0 ? (
              <div style={subtleTextStyle}>
                No explicit decision notes yet. Existing decision_note artifacts will be collected
                here without creating durable decisions automatically.
              </div>
            ) : (
              <div style={{ display: "grid", gap: 8 }}>
                {decisions.length > visibleDecisions.length ? (
                  <div style={subtleTextStyle}>
                    Showing {visibleDecisions.length} of {decisions.length} decisions.
                  </div>
                ) : null}
                {visibleDecisions.map((item) => (
                  <ProjectDecisionRow
                    key={item.id}
                    item={item}
                    onSelectWorkItem={onSelectWorkItem}
                  />
                ))}
              </div>
            )}
          </section>
        </div>
      </div>
    </div>
  );
}

function ProjectTimelineRow({
  item,
  onEditMemory,
  onOpenChat,
  onOpenTask,
  onSelectWorkItem,
  project,
  roles,
  workItems,
}: {
  item: ProjectTimelineItem;
  onEditMemory: (entry: ProjectMemoryRecord) => void;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onSelectWorkItem: (workItemID: string) => void;
  project: ProjectRecord;
  roles: ProjectWorkRoleRecord[];
  workItems: ProjectWorkItemRecord[];
}) {
  const workItem =
    item.workItemID && workItems.find((candidate) => candidate.id === item.workItemID);
  const role =
    item.assignment && roles.find((candidate) => candidate.id === item.assignment?.role_id);
  const chatRequest =
    item.assignment && workItem
      ? buildProjectAssignmentChatLaunchRequest({
          project,
          workItem,
          assignment: item.assignment,
          role: role ?? null,
        })
      : null;
  const memoryEntry = item.memoryEntry;
  return (
    <div style={timelineItemStyle}>
      <div style={timelineItemHeaderStyle}>
        <div style={timelineItemTitleRowStyle}>
          <span className={timelineBadgeClass(item)}>{timelineKindLabel(item.kind)}</span>
          {item.status && <Badge status={item.status} label={activitySignalLabel(item.status)} />}
          <div style={{ ...titleStyle, minWidth: 0 }}>{item.title}</div>
        </div>
        <div style={timelineItemActionsStyle}>
          {item.workItemID && (
            <button
              aria-label={`Show timeline details for ${item.title}`}
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => onSelectWorkItem(item.workItemID ?? "")}
            >
              Details
            </button>
          )}
          {item.taskID && (
            <button
              aria-label={`Open timeline task ${shortID(item.taskID)}`}
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => onOpenTask?.(item.taskID ?? "", item.runID)}
              disabled={!onOpenTask}
              title="Open task"
            >
              <Icon d={Icons.tasks} size={12} />
              Task
            </button>
          )}
          {chatRequest && (
            <button
              aria-label={`Open timeline chat for ${item.title}`}
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => onOpenChat?.(chatRequest)}
              disabled={!onOpenChat || !chatRequest.model}
              title={
                chatRequest.model
                  ? `Open chat with ${chatRequest.model}`
                  : "Set project defaults before opening chat."
              }
            >
              <Icon d={Icons.chat} size={12} />
              Chat
            </button>
          )}
          {memoryEntry && (
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => onEditMemory(memoryEntry)}
            >
              <Icon d={Icons.edit} size={12} />
              Inspect
            </button>
          )}
        </div>
      </div>
      {item.summary && <div style={timelineSummaryStyle}>{item.summary}</div>}
      <div style={metaLineStyle}>
        {item.actor && <span>{item.actor}</span>}
        {item.source && <span>{item.source}</span>}
        {item.runID && <span>run {shortID(item.runID)}</span>}
        {item.chatID && <span>chat {shortID(item.chatID)}</span>}
        {item.timestamp && <span>{formatAbsoluteTime(item.timestamp)}</span>}
      </div>
    </div>
  );
}

function ProjectDecisionRow({
  item,
  onSelectWorkItem,
}: {
  item: ProjectTimelineItem;
  onSelectWorkItem: (workItemID: string) => void;
}) {
  return (
    <div style={decisionItemStyle}>
      <div style={{ display: "flex", gap: 8, alignItems: "center", minWidth: 0 }}>
        <span className="badge badge-amber">decision_note</span>
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{item.title}</div>
        {item.workItemID && (
          <button
            aria-label={`Show decision details for ${item.title}`}
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={() => onSelectWorkItem(item.workItemID ?? "")}
          >
            Details
          </button>
        )}
      </div>
      {item.summary && <div style={timelineSummaryStyle}>{item.summary}</div>}
      <div style={metaLineStyle}>
        {item.actor && <span>{item.actor}</span>}
        {item.timestamp && <span>{formatAbsoluteTime(item.timestamp)}</span>}
      </div>
    </div>
  );
}

const panelStyle: CSSProperties = {
  border: "1px solid var(--border)",
  background: "var(--bg1)",
  borderRadius: "var(--radius-sm)",
  boxSizing: "border-box",
  maxWidth: "100%",
  minWidth: 0,
  padding: 12,
};

const sectionLabelStyle: CSSProperties = {
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  color: "var(--teal)",
  letterSpacing: "0.06em",
  textTransform: "uppercase",
};

const titleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 13,
  fontWeight: 600,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const subtleTextStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 12,
  lineHeight: 1.4,
};

const metaLineStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  color: "var(--t3)",
  fontSize: 11,
  marginTop: 6,
};

const timelineGridStyle: CSSProperties = {
  display: "grid",
  gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 360px), 1fr))",
  gap: 14,
  alignItems: "start",
};

const timelineItemStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 9,
  minWidth: 0,
};

const timelineItemHeaderStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "grid",
  gap: 8,
  gridTemplateColumns: "minmax(0, 1fr)",
  minWidth: 0,
};

const timelineItemTitleRowStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flex: "1 1 160px",
  flexWrap: "wrap",
  gap: 8,
  minWidth: 0,
};

const timelineItemActionsStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 6,
  justifyContent: "flex-end",
  minWidth: 0,
  maxWidth: "100%",
};

const timelineSummaryStyle: CSSProperties = {
  marginTop: 6,
  color: "var(--t1)",
  fontSize: 12,
  lineHeight: 1.45,
  whiteSpace: "pre-wrap",
  overflowWrap: "anywhere",
  display: "-webkit-box",
  WebkitLineClamp: 3,
  WebkitBoxOrient: "vertical",
  overflow: "hidden",
};

const decisionLogStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 9,
  minWidth: 0,
};

const decisionItemStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 8,
  minWidth: 0,
};

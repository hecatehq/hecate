import type { CSSProperties } from "react";

import type {
  ProjectActivityBucketKey,
  ProjectHealthAttention,
  ProjectHealthSummary,
  ProjectMemoryCandidateRecord,
} from "../../types/project";
import { Badge, Icon, Icons } from "../shared/ui";
import { useFloatingMenu } from "../shared/useFloatingMenu";
import { routeProjectHealthAttention, type ProjectActionRoute } from "./projectActionRouting";
import { activitySignalLabel } from "./projectInsights";
import { projectVisibilityDetail } from "./projectVisibilityDetail";

export type ProjectHealthPanelProps = {
  attentionItems: ProjectHealthAttention[];
  disabled?: boolean;
  memoryCandidates: ProjectMemoryCandidateRecord[];
  omittedAttentionCount?: number;
  selectedProjectID?: string;
  summary?: ProjectHealthSummary;
  onAttentionBucket: (bucket: ProjectActivityBucketKey) => void;
  onAttentionDefaults: () => void;
  onAttentionError?: (message: string) => void;
  onAttentionMemory: () => void;
  onAttentionProfiles: () => void;
  onAttentionReviewCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onAttentionRoles: () => void;
  onAttentionSkills: () => void;
  onAttentionTask?: (taskID: string, runID?: string) => void;
  onAttentionWorkItem: (workItemID: string) => void;
  triggerStyle?: CSSProperties;
};

export function ProjectHealthPanel({
  attentionItems,
  disabled = false,
  memoryCandidates,
  omittedAttentionCount = 0,
  selectedProjectID,
  summary,
  onAttentionBucket,
  onAttentionDefaults,
  onAttentionError,
  onAttentionMemory,
  onAttentionProfiles,
  onAttentionReviewCandidate,
  onAttentionRoles,
  onAttentionSkills,
  onAttentionTask,
  onAttentionWorkItem,
  triggerStyle,
}: ProjectHealthPanelProps) {
  const attentionMenu = useFloatingMenu<HTMLDivElement, HTMLButtonElement>({
    portalSelector: null,
  });
  const attentionCount = attentionItems.length;
  const totalAttentionCount =
    summary?.available_attention_count ?? attentionCount + omittedAttentionCount;
  const hiddenAttentionCount = Math.max(0, totalAttentionCount - attentionCount);
  const hiddenAttentionDetail = projectVisibilityDetail({
    shownCount: attentionCount,
    totalCount: totalAttentionCount,
    itemLabelSingular: "attention item",
    itemLabelPlural: "attention items",
    hiddenLabelSingular: "item",
    hiddenLabelPlural: "items",
  });
  const attentionLabel =
    totalAttentionCount > 0
      ? `Project attention: ${attentionCount}${hiddenAttentionCount > 0 ? `, ${hiddenAttentionCount} hidden` : ""}`
      : "Project attention";
  const attentionBadge = hiddenAttentionCount > 0 ? `${attentionCount}+` : `${attentionCount}`;
  const postureRows = projectHealthPostureRows(summary);
  const closeMenu = () => attentionMenu.close();
  const handleAttentionAction = (item: ProjectHealthAttention) => {
    const candidate = memoryCandidates.find((candidate) => candidate.id === item.candidate_id);
    handleAttentionRoute(
      routeProjectHealthAttention(item, {
        hasMemoryCandidate: Boolean(candidate),
        selectedProjectID,
      }),
      candidate,
    );
    closeMenu();
  };
  const handleAttentionRoute = (
    route: ProjectActionRoute,
    candidate?: ProjectMemoryCandidateRecord,
  ) => {
    switch (route.kind) {
      case "error":
        onAttentionError?.(route.message);
        return;
      case "open_project_settings":
        onAttentionDefaults();
        return;
      case "open_skills":
        onAttentionSkills();
        return;
      case "open_profiles":
        onAttentionProfiles();
        return;
      case "open_roles":
        onAttentionRoles();
        return;
      case "review_memory_candidate":
        if (candidate) onAttentionReviewCandidate(candidate);
        else onAttentionMemory();
        return;
      case "open_work_item":
        if (route.bucket) onAttentionBucket(route.bucket);
        onAttentionWorkItem(route.workItemID);
        return;
      case "open_task":
        onAttentionTask?.(route.taskID, route.runID);
        return;
      case "open_activity_bucket":
        onAttentionBucket(route.bucket);
        return;
      case "open_memory_review":
        onAttentionMemory();
        return;
      default:
        return;
    }
  };
  return (
    <div ref={attentionMenu.wrapRef} style={projectAttentionMenuStyle}>
      <button
        ref={attentionMenu.triggerRef}
        className="btn btn-ghost btn-sm"
        type="button"
        aria-expanded={attentionMenu.open}
        aria-label={attentionLabel}
        title="Project attention"
        onClick={attentionMenu.toggle}
        disabled={disabled}
        style={{
          ...triggerStyle,
          color: attentionCount > 0 ? "var(--amber)" : "var(--t2)",
        }}
      >
        <Icon d={Icons.warning} size={13} />
        {attentionCount > 0 && <span style={projectAttentionCountStyle}>{attentionBadge}</span>}
      </button>
      {attentionMenu.open && !disabled && (
        <div
          ref={attentionMenu.menuRef}
          role="menu"
          aria-label="Project attention"
          style={projectAttentionPopoverStyle}
        >
          <div style={projectAttentionPopoverHeaderStyle}>
            <div style={sectionLabelStyle}>Needs Attention</div>
            <span className="badge badge-muted">{attentionBadge}</span>
          </div>
          {postureRows.length > 0 && (
            <div style={projectPostureGridStyle} aria-label="Project health summary">
              {postureRows.map((row) => (
                <div key={row.id} style={projectPostureRowStyle}>
                  <div style={projectPostureTitleStyle}>{row.title}</div>
                  <div style={projectPostureValueStyle}>{row.value}</div>
                  {row.detail && <div style={subtleTextStyle}>{row.detail}</div>}
                </div>
              ))}
            </div>
          )}
          {hiddenAttentionDetail && <div style={subtleTextStyle}>{hiddenAttentionDetail}</div>}
          {attentionItems.length === 0 ? (
            <div style={subtleTextStyle}>No project attention items detected.</div>
          ) : (
            <div style={{ display: "grid", gap: 8 }}>
              {attentionItems.map((item) => (
                <ProjectHealthAttentionRow
                  key={item.id}
                  item={item}
                  onActivate={() => handleAttentionAction(item)}
                  onBucketChange={(bucket) => {
                    onAttentionBucket(bucket);
                    closeMenu();
                  }}
                  onOpenTask={(taskID, runID) => {
                    onAttentionTask?.(taskID, runID);
                    closeMenu();
                  }}
                  onReviewCandidate={(candidate) => {
                    onAttentionReviewCandidate(candidate);
                    closeMenu();
                  }}
                  onSelectWorkItem={(workItemID) => {
                    onAttentionWorkItem(workItemID);
                    closeMenu();
                  }}
                  reviewCandidate={memoryCandidates.find(
                    (candidate) => candidate.id === item.candidate_id,
                  )}
                />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function ProjectHealthAttentionRow({
  item,
  onActivate,
  onBucketChange,
  onOpenTask,
  onReviewCandidate,
  onSelectWorkItem,
  reviewCandidate,
}: {
  item: ProjectHealthAttention;
  onActivate: () => void;
  onBucketChange: (bucket: ProjectActivityBucketKey) => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onReviewCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onSelectWorkItem: (workItemID: string) => void;
  reviewCandidate?: ProjectMemoryCandidateRecord;
}) {
  return (
    <div
      className="project-attention-item"
      role="button"
      tabIndex={0}
      aria-label={`Open attention item ${item.title}`}
      onClick={onActivate}
      onKeyDown={(event) => {
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        onActivate();
      }}
      style={healthAttentionStyle}
    >
      <div style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: 8, minWidth: 0 }}>
        <Badge status={item.status} label={activitySignalLabel(item.status)} />
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{item.title}</div>
        <span aria-hidden="true" className="project-attention-item-chevron">
          <Icon d={Icons.chevR} size={12} />
        </span>
        {item.bucket && (
          <button
            className="btn btn-ghost btn-sm project-attention-item-action"
            type="button"
            onClick={(event) => {
              event.stopPropagation();
              onBucketChange(item.bucket!);
            }}
          >
            {item.action_label ?? "Inbox"}
          </button>
        )}
        {item.work_item_id && (
          <button
            className="btn btn-ghost btn-sm project-attention-item-action"
            type="button"
            aria-label={
              item.bucket
                ? "Open attention details"
                : (item.action_label ?? "Open attention details")
            }
            onClick={(event) => {
              event.stopPropagation();
              onSelectWorkItem(item.work_item_id!);
            }}
          >
            {item.bucket ? "Details" : (item.action_label ?? "Details")}
          </button>
        )}
        {item.task_id && (
          <button
            className="btn btn-ghost btn-sm project-attention-item-action"
            type="button"
            aria-label="Open attention task"
            onClick={(event) => {
              event.stopPropagation();
              onOpenTask?.(item.task_id!, item.run_id);
            }}
            disabled={!onOpenTask}
          >
            <Icon d={Icons.tasks} size={12} />
            Task
          </button>
        )}
        {reviewCandidate && (
          <button
            className="btn btn-ghost btn-sm project-attention-item-action"
            type="button"
            aria-label="Review memory candidate"
            onClick={(event) => {
              event.stopPropagation();
              onReviewCandidate(reviewCandidate);
            }}
          >
            Review candidate
          </button>
        )}
      </div>
      <div style={subtleTextStyle}>{item.detail}</div>
    </div>
  );
}

type ProjectHealthPostureRow = {
  id: string;
  title: string;
  value: string;
  detail?: string;
};

function projectHealthPostureRows(summary?: ProjectHealthSummary): ProjectHealthPostureRow[] {
  if (!summary) return [];
  const setupGaps = [
    summary.missing_defaults ? "defaults" : "",
    summary.missing_project_root ? "root" : "",
  ].filter(Boolean);
  const pendingMemory = summary.pending_memory_candidate_count;
  const workFollowUp =
    summary.pending_handoff_count +
    summary.review_follow_up_count +
    summary.stale_or_unknown_assignment_count;
  return [
    {
      id: "setup",
      title: "Setup",
      value: setupGaps.length > 0 ? `${setupGaps.length} gap${plural(setupGaps.length)}` : "Ready",
      detail: setupGaps.length > 0 ? setupGaps.join(", ") : undefined,
    },
    {
      id: "memory",
      title: "Memory",
      value:
        summary.saved_memory_count === 0
          ? "No memory yet"
          : `${summary.enabled_memory_count}/${summary.saved_memory_count} enabled`,
      detail:
        pendingMemory > 0
          ? `${pendingMemory} candidate${plural(pendingMemory)} pending`
          : undefined,
    },
    {
      id: "context",
      title: "Context",
      value: `${summary.enabled_context_source_count} source${plural(
        summary.enabled_context_source_count,
      )}`,
    },
    {
      id: "work",
      title: "Work",
      value: workFollowUp > 0 ? `${workFollowUp} follow-up${plural(workFollowUp)}` : "Clear",
      detail: projectHealthWorkDetail(summary),
    },
  ];
}

function projectHealthWorkDetail(summary: ProjectHealthSummary): string | undefined {
  const parts = [
    countLabel(summary.pending_handoff_count, "handoff"),
    countLabel(summary.review_follow_up_count, "review"),
    countLabel(summary.stale_or_unknown_assignment_count, "assignment link"),
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(", ") : undefined;
}

function countLabel(count: number, label: string): string {
  if (count <= 0) return "";
  return `${count} ${label}${plural(count)}`;
}

function plural(count: number): string {
  return count === 1 ? "" : "s";
}

const projectAttentionMenuStyle: CSSProperties = {
  position: "relative",
};

const projectAttentionCountStyle: CSSProperties = {
  alignItems: "center",
  background: "var(--amber)",
  borderRadius: 8,
  color: "var(--bg0)",
  display: "inline-flex",
  fontSize: 9,
  fontWeight: 700,
  height: 14,
  justifyContent: "center",
  minWidth: 14,
  padding: "0 4px",
  position: "absolute",
  right: -2,
  top: -3,
};

const projectAttentionPopoverStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  boxShadow: "0 16px 36px rgba(0, 0, 0, 0.42)",
  boxSizing: "border-box",
  display: "grid",
  gap: 10,
  maxHeight: "min(560px, calc(100vh - 84px))",
  minWidth: 340,
  overflowY: "auto",
  padding: 10,
  position: "absolute",
  right: 0,
  top: 36,
  width: "min(420px, calc(100vw - 28px))",
  zIndex: 30,
};

const projectAttentionPopoverHeaderStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  gap: 8,
  justifyContent: "space-between",
  minWidth: 0,
};

const projectPostureGridStyle: CSSProperties = {
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 0,
  gridTemplateColumns: "repeat(2, minmax(0, 1fr))",
  overflow: "hidden",
};

const projectPostureRowStyle: CSSProperties = {
  borderBottom: "1px solid var(--border)",
  borderRight: "1px solid var(--border)",
  display: "grid",
  gap: 3,
  minWidth: 0,
  padding: "8px 9px",
};

const projectPostureTitleStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 11,
};

const projectPostureValueStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 12,
  fontWeight: 700,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
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

const healthAttentionStyle: CSSProperties = {
  background: "transparent",
  border: "1px solid transparent",
  borderRadius: "var(--radius-sm)",
  cursor: "pointer",
  display: "grid",
  gap: 6,
  outline: "none",
  padding: 9,
  transition: "background 120ms ease, border-color 120ms ease",
};

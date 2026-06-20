import type { CSSProperties } from "react";

import type {
  ProjectActivityBucketKey,
  ProjectHealthAttention,
  ProjectMemoryCandidateRecord,
} from "../../types/project";
import { Badge, Icon, Icons } from "../shared/ui";
import { useFloatingMenu } from "../shared/useFloatingMenu";
import { activitySignalLabel } from "./projectInsights";

export type ProjectHealthPanelProps = {
  attentionItems: ProjectHealthAttention[];
  disabled?: boolean;
  memoryCandidates: ProjectMemoryCandidateRecord[];
  omittedAttentionCount?: number;
  onAttentionBucket: (bucket: ProjectActivityBucketKey) => void;
  onAttentionDefaults: () => void;
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
  onAttentionBucket,
  onAttentionDefaults,
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
  const totalAttentionCount = attentionCount + omittedAttentionCount;
  const attentionLabel =
    totalAttentionCount > 0
      ? `Project attention: ${attentionCount}${omittedAttentionCount > 0 ? `, ${omittedAttentionCount} hidden` : ""}`
      : "Project attention";
  const attentionBadge = omittedAttentionCount > 0 ? `${attentionCount}+` : `${attentionCount}`;
  const closeMenu = () => attentionMenu.close();
  const handleAttentionAction = (item: ProjectHealthAttention) => {
    if (item.action === "settings" || item.id.endsWith(":defaults")) {
      onAttentionDefaults();
    } else if (item.action === "skills") {
      onAttentionSkills();
    } else if (item.action === "profiles") {
      onAttentionProfiles();
    } else if (item.action === "roles") {
      onAttentionRoles();
    } else if (item.candidate_id) {
      const candidate = memoryCandidates.find((candidate) => candidate.id === item.candidate_id);
      if (candidate) onAttentionReviewCandidate(candidate);
      else onAttentionMemory();
    } else if (item.work_item_id) {
      onAttentionWorkItem(item.work_item_id);
    } else if (item.task_id) {
      onAttentionTask?.(item.task_id, item.run_id);
    } else if (item.bucket) {
      onAttentionBucket(item.bucket);
    } else if (item.action === "memory" || item.id.endsWith(":context")) {
      onAttentionMemory();
    }
    closeMenu();
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
          {omittedAttentionCount > 0 && (
            <div style={subtleTextStyle}>
              {omittedAttentionCount} lower-priority{" "}
              {omittedAttentionCount === 1 ? "item is" : "items are"} hidden by the server cap.
            </div>
          )}
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

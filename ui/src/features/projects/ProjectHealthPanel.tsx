import {
  useId,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type RefObject,
} from "react";

import type {
  ProjectActivityBucketKey,
  ProjectHealthAttention,
  ProjectHealthSummary,
  ProjectMemoryCandidateRecord,
} from "../../types/project";
import { Badge, Icon, Icons } from "../shared/ui";
import { useFloatingMenu } from "../shared/useFloatingMenu";
import {
  PROJECT_ATTENTION_STALE_MESSAGE,
  projectActivityBucket,
  routeProjectHealthAttention,
  type ProjectActionRoute,
} from "./projectActionRouting";
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
  onAttentionPresets: () => void;
  onAttentionReviewCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onAttentionRoute?: (route: ProjectActionRoute) => void;
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
  onAttentionPresets,
  onAttentionReviewCandidate,
  onAttentionRoute,
  onAttentionRoles,
  onAttentionSkills,
  onAttentionTask,
  onAttentionWorkItem,
  triggerStyle,
}: ProjectHealthPanelProps) {
  const attentionMenu = useFloatingMenu<HTMLDivElement, HTMLButtonElement>({
    portalSelector: null,
  });
  const attentionPopoverPosition = useProjectAttentionPopoverPosition(
    attentionMenu.triggerRef,
    attentionMenu.open,
  );
  const attentionDialogID = useId();
  const attentionTitleID = useId();
  const focusNoticeSequenceRef = useRef(0);
  const [focusNotice, setFocusNotice] = useState({ key: 0, message: "" });
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
  const focusedAttention =
    typeof document !== "undefined" && document.activeElement instanceof HTMLElement
      ? document.activeElement.closest<HTMLElement>("[data-project-attention-identity]")
      : null;
  const focusedAttentionIdentity =
    focusedAttention && attentionMenu.menuRef.current?.contains(focusedAttention)
      ? (focusedAttention.dataset.projectAttentionIdentity ?? "")
      : "";
  const focusedAttentionWillChange = Boolean(
    focusedAttentionIdentity &&
    !attentionItems.some(
      (item) => projectHealthAttentionIdentity(item) === focusedAttentionIdentity,
    ),
  );

  useLayoutEffect(() => {
    if (!focusedAttentionWillChange) return;
    attentionMenu.close();
    attentionMenu.triggerRef.current?.focus();
    focusNoticeSequenceRef.current += 1;
    setFocusNotice({
      key: focusNoticeSequenceRef.current,
      message: "Project attention changed. Focus returned to the attention button.",
    });
  }, [attentionMenu.close, attentionMenu.triggerRef, focusedAttentionWillChange]);
  useLayoutEffect(() => {
    if (!attentionMenu.open) return;
    const firstAction = attentionMenu.menuRef.current?.querySelector<HTMLElement>(
      "[data-project-attention-primary]",
    );
    (firstAction ?? attentionMenu.menuRef.current)?.focus();
  }, [attentionMenu.menuRef, attentionMenu.open]);
  useEffect(() => {
    if (!attentionMenu.open) return;
    const handleOutsideMouseDown = (event: MouseEvent) => {
      const target = event.target as Node | null;
      if (!target || attentionMenu.wrapRef.current?.contains(target)) return;
      window.setTimeout(() => {
        if (
          document.activeElement === document.body ||
          document.activeElement === document.documentElement
        ) {
          attentionMenu.triggerRef.current?.focus();
        }
      }, 0);
    };
    document.addEventListener("mousedown", handleOutsideMouseDown);
    return () => document.removeEventListener("mousedown", handleOutsideMouseDown);
  }, [attentionMenu.open, attentionMenu.triggerRef, attentionMenu.wrapRef]);
  const closeMenu = () => attentionMenu.close();
  const closeMenuAndRestoreFocus = () => {
    attentionMenu.close();
    attentionMenu.triggerRef.current?.focus();
  };
  const handleAttentionAction = (item: ProjectHealthAttention) => {
    const candidateID = projectHealthAttentionCandidateID(item);
    const candidate = memoryCandidates.find((candidate) => candidate.id === candidateID);
    const route = routeProjectHealthAttention(item, {
      hasMemoryCandidate: Boolean(candidate),
      selectedProjectID,
    });
    if (onAttentionRoute) onAttentionRoute(route);
    else handleAttentionRoute(route, candidate);
    if (route.kind === "error") closeMenuAndRestoreFocus();
    else closeMenu();
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
      case "open_agent_presets":
        onAttentionPresets();
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
  const handleAttentionMetadataAction = (item: ProjectHealthAttention, run: () => void) => {
    if (!projectHealthAttentionTargetCurrent(item, selectedProjectID)) {
      onAttentionError?.(PROJECT_ATTENTION_STALE_MESSAGE);
      closeMenuAndRestoreFocus();
      return;
    }
    run();
    closeMenu();
  };
  return (
    <div
      ref={attentionMenu.wrapRef}
      onBlur={(event) => {
        const nextFocus = event.relatedTarget;
        if (nextFocus instanceof Node && event.currentTarget.contains(nextFocus)) return;
        closeMenu();
      }}
      style={projectAttentionMenuStyle}
    >
      <div aria-atomic="true" aria-live="polite" role="status" style={visuallyHiddenStyle}>
        <span key={focusNotice.key}>{focusNotice.message}</span>
      </div>
      <button
        ref={attentionMenu.triggerRef}
        className="btn btn-ghost btn-sm"
        type="button"
        aria-controls={attentionDialogID}
        aria-expanded={attentionMenu.open}
        aria-haspopup="dialog"
        aria-label={attentionLabel}
        title="Project attention"
        onClick={attentionMenu.toggle}
        onKeyDown={(event) => {
          if (!attentionMenu.open || event.key !== "Escape") return;
          event.stopPropagation();
          closeMenuAndRestoreFocus();
        }}
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
          id={attentionDialogID}
          className="project-attention-popover"
          role="dialog"
          aria-labelledby={attentionTitleID}
          tabIndex={-1}
          onKeyDown={(event) => {
            if (event.key !== "Escape") return;
            event.stopPropagation();
            closeMenuAndRestoreFocus();
          }}
          style={{ ...projectAttentionPopoverStyle, ...attentionPopoverPosition }}
        >
          <div style={projectAttentionPopoverHeaderStyle}>
            <h2 id={attentionTitleID} style={sectionLabelStyle}>
              Needs Attention
            </h2>
            <span className="badge badge-muted">{attentionBadge}</span>
          </div>
          {postureRows.length > 0 && (
            <div role="group" style={projectPostureGridStyle} aria-label="Project health summary">
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
            <ul style={attentionListStyle}>
              {attentionItems.map((item) => (
                <ProjectHealthAttentionRow
                  key={item.id}
                  item={item}
                  onActivate={() => handleAttentionAction(item)}
                  onBucketChange={(bucket) => {
                    handleAttentionMetadataAction(item, () => onAttentionBucket(bucket));
                  }}
                  reviewCandidate={memoryCandidates.find(
                    (candidate) => candidate.id === projectHealthAttentionCandidateID(item),
                  )}
                />
              ))}
            </ul>
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
  reviewCandidate,
}: {
  item: ProjectHealthAttention;
  onActivate: () => void;
  onBucketChange: (bucket: ProjectActivityBucketKey) => void;
  reviewCandidate?: ProjectMemoryCandidateRecord;
}) {
  const bucketTarget = projectHealthAttentionBucket(item);
  const workItemID = projectHealthAttentionWorkItemID(item);
  const taskTarget = projectHealthAttentionTaskTarget(item);
  const candidateTarget = projectHealthAttentionCandidateID(item);
  const hasCompactActions = Boolean(bucketTarget || workItemID || taskTarget || candidateTarget);
  return (
    <li
      className="project-attention-item"
      data-project-attention-identity={projectHealthAttentionIdentity(item)}
      style={healthAttentionStyle}
    >
      <button
        className="project-attention-item-primary"
        data-project-attention-primary
        type="button"
        aria-label={`Open attention item ${item.title}`}
        onClick={onActivate}
        style={healthAttentionPrimaryStyle}
      >
        <span style={healthAttentionHeadingStyle}>
          <Badge status={item.status} label={activitySignalLabel(item.status)} />
          <span style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{item.title}</span>
          <span aria-hidden="true" className="project-attention-item-chevron">
            <Icon d={Icons.chevR} size={12} />
          </span>
        </span>
        <span style={subtleTextStyle}>{item.detail}</span>
      </button>
      {hasCompactActions && (
        <div aria-label={`${item.title} actions`} role="group" style={healthAttentionActionsStyle}>
          {bucketTarget && (
            <button
              className="btn btn-ghost btn-sm project-attention-item-action"
              type="button"
              aria-label={`${item.action_label ?? "Inbox"}: ${item.title}`}
              onClick={() => onBucketChange(bucketTarget)}
            >
              {item.action_label ?? "Inbox"}
            </button>
          )}
          {workItemID && (
            <button
              className="btn btn-ghost btn-sm project-attention-item-action"
              type="button"
              aria-label={`${
                item.bucket
                  ? "Open attention details"
                  : (item.action_label ?? "Open attention details")
              }: ${item.title}`}
              onClick={onActivate}
            >
              {bucketTarget ? "Details" : (item.action_label ?? "Details")}
            </button>
          )}
          {taskTarget && (
            <button
              className="btn btn-ghost btn-sm project-attention-item-action"
              type="button"
              aria-label={`Open attention task: ${item.title}`}
              onClick={onActivate}
            >
              <Icon d={Icons.tasks} size={12} />
              Task
            </button>
          )}
          {candidateTarget && (
            <button
              className="btn btn-ghost btn-sm project-attention-item-action"
              type="button"
              aria-label={`${reviewCandidate ? "Review memory candidate" : "Open memory review"}: ${item.title}`}
              onClick={onActivate}
            >
              {reviewCandidate ? "Review candidate" : "Open memory"}
            </button>
          )}
        </div>
      )}
    </li>
  );
}

function projectHealthAttentionTargetCurrent(
  item: ProjectHealthAttention,
  selectedProjectID?: string,
): boolean {
  const projectID = item.action?.project_id;
  return !projectID || !selectedProjectID || projectID === selectedProjectID;
}

function projectHealthAttentionBucket(
  item: ProjectHealthAttention,
): ProjectActivityBucketKey | undefined {
  switch (item.action?.type) {
    case "open_activity_bucket":
    case "open_work_item":
      return projectActivityBucket(item.action.activity_bucket);
    default:
      return undefined;
  }
}

function projectHealthAttentionWorkItemID(item: ProjectHealthAttention): string | undefined {
  return item.action?.type === "open_work_item" ? item.action.work_item_id : undefined;
}

function projectHealthAttentionTaskTarget(
  item: ProjectHealthAttention,
): { taskID: string; runID?: string } | undefined {
  if (item.action?.type !== "open_task" || !item.action.task_id) return undefined;
  return { taskID: item.action.task_id, runID: item.action.run_id };
}

function projectHealthAttentionCandidateID(item: ProjectHealthAttention): string | undefined {
  return item.action?.type === "review_memory_candidate" ? item.action.candidate_id : undefined;
}

function projectHealthAttentionIdentity(item: ProjectHealthAttention) {
  const actionFingerprint = Object.entries(item.action).sort(([left], [right]) =>
    left.localeCompare(right),
  );
  return JSON.stringify([item.id, item.title, item.action_label ?? "", actionFingerprint]);
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

function useProjectAttentionPopoverPosition(
  triggerRef: RefObject<HTMLButtonElement | null>,
  open: boolean,
): CSSProperties {
  const [position, setPosition] = useState<CSSProperties>({});
  useLayoutEffect(() => {
    if (!open || !triggerRef.current) return;
    const compute = () => {
      const trigger = triggerRef.current;
      if (!trigger) return;
      const actionBar = trigger.closest<HTMLElement>(".project-header-actions") ?? trigger;
      const content = trigger.closest<HTMLElement>(".hecate-content");
      const statusBar = document.querySelector<HTMLElement>(".hecate-statusbar");
      const actionRect = actionBar.getBoundingClientRect();
      const contentRect = content?.getBoundingClientRect();
      const statusBarRect = statusBar?.getBoundingClientRect();
      const viewportInset = 8;
      const leftEdge = Math.max(viewportInset, (contentRect?.left ?? 0) + viewportInset);
      const rightEdge = Math.min(
        window.innerWidth - viewportInset,
        (contentRect?.right ?? window.innerWidth) - viewportInset,
      );
      const width = Math.max(0, Math.min(420, rightEdge - leftEdge));
      const left = Math.min(
        Math.max(actionRect.right - width, leftEdge),
        Math.max(leftEdge, rightEdge - width),
      );
      const top = actionRect.bottom + 4;
      const viewportBottom = window.innerHeight - viewportInset;
      const statusBarTop = statusBarRect?.top ?? viewportBottom;
      const bottomEdge = Math.min(viewportBottom, statusBarTop - 4);
      setPosition({
        left,
        maxHeight: Math.min(560, Math.max(0, bottomEdge - top)),
        position: "fixed",
        right: "auto",
        top,
        width,
      });
    };
    compute();
    window.addEventListener("resize", compute);
    window.addEventListener("scroll", compute, true);
    return () => {
      window.removeEventListener("resize", compute);
      window.removeEventListener("scroll", compute, true);
    };
  }, [open, triggerRef]);
  return position;
}

const projectAttentionMenuStyle: CSSProperties = {
  position: "static",
};

const visuallyHiddenStyle: CSSProperties = {
  border: 0,
  clip: "rect(0 0 0 0)",
  height: 1,
  margin: -1,
  overflow: "hidden",
  padding: 0,
  position: "absolute",
  whiteSpace: "nowrap",
  width: 1,
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
  minWidth: 0,
  overflowX: "hidden",
  overflowY: "auto",
  padding: 10,
  position: "absolute",
  right: 0,
  top: "calc(100% + 4px)",
  width: "min(420px, calc(100vw - 64px))",
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
  margin: 0,
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
  overflowWrap: "anywhere",
};

const attentionListStyle: CSSProperties = {
  display: "grid",
  gap: 8,
  listStyle: "none",
  margin: 0,
  padding: 0,
};

const healthAttentionStyle: CSSProperties = {
  background: "transparent",
  border: "1px solid transparent",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 6,
  padding: 9,
  transition: "background 120ms ease, border-color 120ms ease",
};

const healthAttentionPrimaryStyle: CSSProperties = {
  background: "transparent",
  border: 0,
  color: "inherit",
  cursor: "pointer",
  display: "grid",
  gap: 6,
  minWidth: 0,
  padding: 0,
  textAlign: "left",
  width: "100%",
};

const healthAttentionHeadingStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  gap: 8,
  minWidth: 0,
};

const healthAttentionActionsStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 6,
  justifyContent: "flex-end",
};

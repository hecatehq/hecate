import { useCallback, useEffect, useRef, useState, type CSSProperties } from "react";

import {
  getProjectAssignmentContext,
  getProjectAssignmentLaunchReadiness,
  getProjectAssignmentPreflight,
} from "../../lib/api";
import { formatAbsoluteTime } from "../../lib/format";
import type { ContextPacketRecord } from "../../types/context";
import type {
  ProjectActivityItemRecord,
  ProjectAssignmentRecord,
  ProjectAssignmentLaunchReadinessRecord,
  ProjectCollaborationArtifactRecord,
  ProjectHandoffRecord,
  ProjectOperationsBriefItem,
  ProjectRecord,
  ProjectWorkItemReadinessRecord,
  ProjectWorkItemReviewFollowUpRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { ContextInspectorModalTrigger, ContextInspectorPanel } from "../shared/ContextInspector";
import { Badge, Icon, Icons, InlineError, Modal } from "../shared/ui";
import {
  toProjectAssignmentEvidenceViewModel,
  toProjectAssignmentExecutionViewModel,
  type ProjectAssignmentEvidenceViewModel,
} from "./projectAssignmentViewModels";
import {
  formatProjectRowRelativeTime,
  handoffStatusLabel,
  projectErrorMessage,
  projectRootDisplayLabel,
  projectRootTitle,
  workStatusLabel,
} from "./projectDisplay";
import {
  reviewRiskFromValue,
  reviewRiskLabel,
  reviewVerdictFromValue,
  reviewVerdictLabel,
} from "./projectWorkForms";
import { firstNonEmpty, isLinkableProjectLocator, shortID } from "./projectUtils";
import { ProjectAssignmentExecutionStory } from "./ProjectAssignmentExecutionStory";
import {
  ProjectWorkItemFollowThrough,
  projectWorkItemOperationTargetAvailable,
} from "./ProjectWorkItemFollowThrough";

export type ProjectAssignmentChatLaunchRequest = {
  projectID: string;
  chatSessionID?: string;
  provider?: string;
  model?: string;
  title?: string;
  draft?: string;
};

type AssignmentPreflightState =
  | { status: "idle" | "loading" }
  | {
      status: "ready";
      packet: ContextPacketRecord;
      readiness: ProjectAssignmentLaunchReadinessRecord;
    }
  | { status: "error"; detail: string };

type AssignmentLaunchReadinessPreviewState =
  | { status: "idle" | "loading" }
  | { status: "ready"; readiness: ProjectAssignmentLaunchReadinessRecord }
  | { status: "error"; detail: string };

type AssignmentLaunchReadinessNoticeRecord = {
  title: string;
  detail: string;
  blockers: string[];
  warnings: string[];
};

type AssignmentLaunchRepairActions = {
  onManagePresets?: () => void;
  onManageRoles?: () => void;
  onOpenConnections?: () => void;
  onOpenProjectSettings?: () => void;
};

export type ProjectWorkItemFocusTarget = {
  artifactID?: string;
  assignmentID?: string;
  handoffID?: string;
  operationKind?: string;
  workItemID: string;
};

export type ProjectWorkItemDetailProps = {
  activityByAssignmentID: Map<string, ProjectActivityItemRecord>;
  assignments: ProjectAssignmentRecord[];
  artifacts: ProjectCollaborationArtifactRecord[];
  artifactActionID: string;
  handoffActionID: string;
  handoffError: string;
  handoffs: ProjectHandoffRecord[];
  assignmentErrors: Record<string, string>;
  detailError: string;
  draftingDefaultAssignment: boolean;
  assistantProposalOpen: boolean;
  preparingAssignmentID: string;
  loading: boolean;
  focusTarget?: ProjectWorkItemFocusTarget | null;
  onAddAssignment: () => void;
  onAddHandoff: () => void;
  onAddEvidenceLink: (assignmentID?: string) => void;
  onAddHandoffFromAssignment: (
    assignment: ProjectAssignmentRecord,
    activityItem?: ProjectActivityItemRecord,
  ) => void;
  onAddReviewHandoffFromAssignment: (
    assignment: ProjectAssignmentRecord,
    reviewRole: ProjectWorkRoleRecord,
    activityItem?: ProjectActivityItemRecord,
  ) => void;
  onAddReviewArtifactFromAssignment: (assignment: ProjectAssignmentRecord) => void;
  onAddHandoffFromReviewArtifact: (artifact: ProjectCollaborationArtifactRecord) => void;
  onDraftDefaultAssignment: (item: ProjectWorkItemRecord) => void;
  onPreparedAssignmentPreflightOpened: (assignmentID: string) => void;
  onCreateAssignmentFromReviewArtifact: (artifactID: string) => void;
  onCreateAssignmentFromHandoff: (handoff: ProjectHandoffRecord) => void;
  onDeleteAssignment: (assignment: ProjectAssignmentRecord) => void;
  onDeleteHandoff: (handoff: ProjectHandoffRecord) => void;
  onDeleteWorkItem: (item: ProjectWorkItemRecord) => void;
  onCloseWorkItem: (item: ProjectWorkItemRecord) => void;
  onSetAssignmentStatus: (assignment: ProjectAssignmentRecord, status: string) => void;
  onEditAssignment: (assignment: ProjectAssignmentRecord) => void;
  onEditHandoff: (handoff: ProjectHandoffRecord) => void;
  onEditWorkItem: (item: ProjectWorkItemRecord) => void;
  onManagePresets: () => void;
  onManageRoles: () => void;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenConnections?: () => void;
  onOpenSettings: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onOpenWorkItem: (workItemID: string) => void;
  onRefresh: () => void | boolean | Promise<void | boolean>;
  onFocusTargetHandled?: () => void;
  onStartAssignment: (assignment: ProjectAssignmentRecord) => void;
  onSetHandoffStatus: (handoff: ProjectHandoffRecord, status: string) => void;
  project: ProjectRecord | null;
  operation?: ProjectOperationsBriefItem | null;
  primaryAssignmentID?: string;
  roleByID: Map<string, ProjectWorkRoleRecord>;
  closingWorkItemID: string;
  closeoutReadiness: ProjectWorkItemReadinessRecord | null;
  startingAssignmentIDs: ReadonlySet<string>;
  workItem: ProjectWorkItemRecord | null;
};

export function ProjectWorkItemDetail({
  activityByAssignmentID,
  assignments,
  artifacts,
  artifactActionID,
  handoffActionID,
  handoffError,
  handoffs,
  assignmentErrors,
  detailError,
  draftingDefaultAssignment,
  assistantProposalOpen,
  preparingAssignmentID,
  loading,
  focusTarget = null,
  onAddAssignment,
  onAddEvidenceLink,
  onAddHandoff,
  onAddHandoffFromAssignment,
  onAddReviewHandoffFromAssignment,
  onAddReviewArtifactFromAssignment,
  onAddHandoffFromReviewArtifact,
  onDraftDefaultAssignment,
  onPreparedAssignmentPreflightOpened,
  onCreateAssignmentFromReviewArtifact,
  onCreateAssignmentFromHandoff,
  onDeleteAssignment,
  onDeleteHandoff,
  onDeleteWorkItem,
  onCloseWorkItem,
  onSetAssignmentStatus,
  onEditAssignment,
  onEditHandoff,
  onEditWorkItem,
  onManagePresets,
  onManageRoles,
  onOpenChat,
  onOpenConnections,
  onOpenSettings,
  onOpenTask,
  onOpenWorkItem,
  onRefresh,
  onFocusTargetHandled,
  onStartAssignment,
  onSetHandoffStatus,
  project,
  operation = null,
  primaryAssignmentID = "",
  roleByID,
  closingWorkItemID,
  closeoutReadiness,
  startingAssignmentIDs,
  workItem,
}: ProjectWorkItemDetailProps) {
  const [closeoutConfirmOpen, setCloseoutConfirmOpen] = useState(false);
  const [focusNotice, setFocusNotice] = useState("");
  const [refreshingFocusTarget, setRefreshingFocusTarget] = useState(false);
  const [staleFocusTarget, setStaleFocusTarget] = useState<ProjectWorkItemFocusTarget | null>(null);
  const [staleRefreshCompleted, setStaleRefreshCompleted] = useState(false);
  const restoreCloseoutFocusRef = useRef(false);
  useEffect(() => {
    setCloseoutConfirmOpen(false);
    setFocusNotice("");
    setRefreshingFocusTarget(false);
    setStaleFocusTarget(null);
    setStaleRefreshCompleted(false);
  }, [workItem?.id]);
  useEffect(() => {
    if (
      closeoutReadiness?.status === "done" ||
      workItem?.status === "done" ||
      workItem?.status === "cancelled"
    ) {
      if (closeoutConfirmOpen) {
        restoreCloseoutFocusRef.current = true;
        setCloseoutConfirmOpen(false);
      }
    }
  }, [closeoutConfirmOpen, closeoutReadiness?.status, workItem?.status]);
  useEffect(() => {
    if (closeoutConfirmOpen || !restoreCloseoutFocusRef.current) return;
    restoreCloseoutFocusRef.current = false;
    focusElementByID("project-work-follow-through");
  }, [closeoutConfirmOpen]);
  const safeAssignments = Array.isArray(assignments) ? assignments : [];
  const safeArtifacts = Array.isArray(artifacts) ? artifacts : [];
  const safeHandoffs = Array.isArray(handoffs) ? handoffs : [];
  useEffect(() => {
    if (!workItem || focusTarget?.workItemID !== workItem.id) return;
    let focused = false;
    let exactTargetRequested = false;
    if (focusTarget.artifactID) {
      exactTargetRequested = true;
      if (safeArtifacts.some((artifact) => artifact.id === focusTarget.artifactID)) {
        focused = focusWorkItemRecord("artifact", focusTarget.artifactID);
      }
    } else if (focusTarget.handoffID) {
      exactTargetRequested = true;
      if (safeHandoffs.some((handoff) => handoff.id === focusTarget.handoffID)) {
        focused = focusWorkItemRecord("handoff", focusTarget.handoffID);
      }
    } else if (focusTarget.assignmentID) {
      exactTargetRequested = true;
      if (safeAssignments.some((assignment) => assignment.id === focusTarget.assignmentID)) {
        focused = focusWorkItemRecord("assignment", focusTarget.assignmentID);
      }
    } else if (focusTarget.operationKind === "close_work_item") {
      exactTargetRequested = true;
      focused = focusElementByID("project-work-closeout");
    } else {
      focused = focusElementByID(workItemElementID(workItem.id));
    }
    if (focused) {
      setFocusNotice("");
      setStaleFocusTarget(null);
      onFocusTargetHandled?.();
      return;
    }
    if (loading) return;
    focusElementByID(workItemElementID(workItem.id));
    setFocusNotice(
      exactTargetRequested
        ? "The requested record is no longer available. Showing the selected work item instead."
        : "",
    );
    setStaleFocusTarget(exactTargetRequested ? focusTarget : null);
    onFocusTargetHandled?.();
  }, [
    focusTarget,
    loading,
    onFocusTargetHandled,
    safeArtifacts,
    safeAssignments,
    safeHandoffs,
    workItem,
  ]);
  useEffect(() => {
    if (!workItem || !staleFocusTarget || !staleRefreshCompleted || loading) return;
    const exactTargetAvailable = workItemFocusTargetAvailable(staleFocusTarget, {
      artifacts: safeArtifacts,
      assignments: safeAssignments,
      handoffs: safeHandoffs,
    });
    const operationStillRequestsTarget = projectOperationRequestsFocusTarget(
      operation,
      staleFocusTarget,
    );
    if (exactTargetAvailable || !operationStillRequestsTarget) {
      setFocusNotice("");
      setStaleFocusTarget(null);
      if (exactTargetAvailable) {
        focusWorkItemTarget(staleFocusTarget);
      } else {
        focusElementByID(workItemElementID(workItem.id));
      }
    }
    setRefreshingFocusTarget(false);
    setStaleRefreshCompleted(false);
  }, [
    loading,
    operation,
    safeArtifacts,
    safeAssignments,
    safeHandoffs,
    staleFocusTarget,
    staleRefreshCompleted,
    workItem,
  ]);
  function refreshStaleFocusTarget() {
    if (refreshingFocusTarget) return;
    setRefreshingFocusTarget(true);
    setStaleRefreshCompleted(false);
    void (async () => {
      try {
        const refreshed = await onRefresh();
        if (refreshed !== false) {
          setStaleRefreshCompleted(true);
        } else {
          setRefreshingFocusTarget(false);
        }
      } catch {
        // The parent owns the visible load error; keep recovery available here.
        setRefreshingFocusTarget(false);
      }
    })();
  }
  if (!workItem) {
    if (loading) {
      return (
        <div aria-atomic="true" aria-live="polite" role="status">
          <EmptyBlock
            title="Loading detail…"
            detail="Loading assignments and collaboration artifacts."
          />
        </div>
      );
    }
    if (detailError) {
      return (
        <section aria-label="Work item unavailable" style={{ display: "grid", gap: 8 }}>
          <EmptyBlock
            title="Work item unavailable"
            detail="Refresh project work to try loading this item again."
          />
          <InlineError message={detailError} />
          <div style={{ display: "flex", justifyContent: "center" }}>
            <button
              className="btn btn-primary btn-sm"
              type="button"
              onClick={() => void onRefresh()}
            >
              <Icon d={Icons.refresh} size={13} />
              Retry
            </button>
          </div>
        </section>
      );
    }
    return (
      <EmptyBlock
        title="Select a work item"
        detail="Assignments and collaboration artifacts appear here."
      />
    );
  }
  const closeout = closeoutReadiness ?? unavailableCloseoutReadiness(workItem, loading);
  const workClosed =
    closeout.status === "done" || workItem.status === "done" || workItem.status === "cancelled";
  const emptyWorkItem =
    safeAssignments.length === 0 &&
    safeArtifacts.length === 0 &&
    safeHandoffs.length === 0 &&
    !workClosed;
  const reviewFollowUps = closeout.review_follow_ups ?? [];
  const suggestedAssignmentRole = assignmentRoleForWorkItem(workItem, roleByID);
  const canAddWorkRecords = !emptyWorkItem && !workClosed;
  const closeoutProminent =
    closeout.status === "done" || (!workClosed && closeout.status === "ready");
  return (
    <div style={workItemDetailStyle}>
      <article
        aria-label={`${workItem.title} work item`}
        className="project-work-focus-target"
        id={workItemElementID(workItem.id)}
        style={workItemCardStyle}
        tabIndex={-1}
      >
        <div style={workItemDetailHeaderStyle}>
          <div style={{ flex: 1, minWidth: 0 }}>
            <h2 style={workItemTitleStyle}>{workItem.title}</h2>
            <div style={workItemMetaStyle}>
              <Badge status={workItem.status} label={workStatusLabel(workItem.status)} />
              <span className="badge badge-muted">{workItem.priority}</span>
              <span title={formatAbsoluteTime(workItem.updated_at)}>
                Updated {formatProjectRowRelativeTime(workItem.updated_at)}
              </span>
              {workItem.owner_role_id && (
                <span>
                  Owner {roleByID.get(workItem.owner_role_id)?.name ?? workItem.owner_role_id}
                </span>
              )}
              {workItem.root_id && project && (
                <span title={projectRootTitle(project, workItem.root_id)}>
                  Root {projectRootDisplayLabel(project, workItem.root_id)}
                </span>
              )}
            </div>
          </div>
          <div style={workItemHeaderActionsStyle}>
            {!workClosed && (
              <>
                <button
                  className="btn btn-ghost btn-sm"
                  type="button"
                  onClick={() => onEditWorkItem(workItem)}
                >
                  <Icon d={Icons.edit} size={13} />
                  Edit
                </button>
                <button
                  className="btn btn-ghost btn-sm"
                  type="button"
                  onClick={() => onDeleteWorkItem(workItem)}
                  style={{ color: "var(--red)" }}
                >
                  <Icon d={Icons.trash} size={13} />
                  Delete
                </button>
              </>
            )}
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              disabled={Boolean(focusNotice) && refreshingFocusTarget}
              onClick={focusNotice ? refreshStaleFocusTarget : () => void onRefresh()}
            >
              <Icon d={Icons.refresh} size={13} />
              {focusNotice && refreshingFocusTarget ? "Refreshing…" : "Refresh"}
            </button>
          </div>
        </div>
        {detailError && <InlineError message={detailError} />}
        {focusNotice && (
          <div aria-live="polite" role="status" style={workItemFocusNoticeStyle}>
            <span style={{ flex: "1 1 240px" }}>{focusNotice}</span>
            <button
              className="btn btn-primary btn-sm"
              type="button"
              disabled={refreshingFocusTarget}
              onClick={refreshStaleFocusTarget}
            >
              <Icon d={Icons.refresh} size={12} />
              {refreshingFocusTarget ? "Refreshing work…" : "Refresh work"}
            </button>
          </div>
        )}
        <section style={workItemBriefSectionStyle}>
          <div style={sectionLabelStyle}>Brief</div>
          <p style={workItemBriefTextStyle}>{workItem.brief || "No brief recorded."}</p>
          <div style={{ ...metaLineStyle, marginTop: 8 }}>
            <span>Created {formatAbsoluteTime(workItem.created_at)}</span>
            {workItem.reviewer_role_ids && workItem.reviewer_role_ids.length > 0 && (
              <span>
                {workItem.reviewer_role_ids.length} reviewer role
                {workItem.reviewer_role_ids.length === 1 ? "" : "s"}
              </span>
            )}
          </div>
          {!emptyWorkItem && !workClosed && (
            <ReviewerSetupNotice
              onEditWorkItem={() => onEditWorkItem(workItem)}
              onManageRoles={onManageRoles}
              roleByID={roleByID}
              workItem={workItem}
            />
          )}
        </section>
        {!primaryAssignmentID && (!emptyWorkItem || Boolean(operation) || Boolean(focusNotice)) && (
          <ProjectWorkItemFollowThrough
            closeout={closeout}
            operation={focusNotice ? null : operation}
            targetAvailable={projectWorkItemOperationTargetAvailable(operation ?? null, {
              artifacts: safeArtifacts,
              assignments: safeAssignments,
              handoffs: safeHandoffs,
            })}
            pending={
              closingWorkItemID === workItem.id ||
              Boolean(artifactActionID) ||
              Boolean(handoffActionID)
            }
            workItem={workItem}
            onFocusAssignment={(assignmentID) => focusWorkItemRecord("assignment", assignmentID)}
            onPlanReviewFollowUp={onCreateAssignmentFromReviewArtifact}
            onRecordEvidence={onAddEvidenceLink}
            onRefresh={() => void onRefresh()}
            onReviewCloseout={() => setCloseoutConfirmOpen(true)}
            onReviewHandoff={(handoffID) => focusWorkItemRecord("handoff", handoffID)}
          />
        )}
        {emptyWorkItem ? (
          <WorkItemStartPanel
            drafting={draftingDefaultAssignment}
            primaryEmphasis={!assistantProposalOpen && !operation && !focusNotice}
            onAddAssignment={onAddAssignment}
            onAddEvidenceLink={onAddEvidenceLink}
            onAddHandoff={onAddHandoff}
            onDraftDefaultAssignment={() => onDraftDefaultAssignment(workItem)}
            onManageRoles={onManageRoles}
            role={suggestedAssignmentRole}
          />
        ) : closeoutProminent ? (
          <WorkItemCloseoutPanel
            closeout={closeout}
            pending={closingWorkItemID === workItem.id}
            onReview={() => setCloseoutConfirmOpen(true)}
          />
        ) : null}
        {canAddWorkRecords && (
          <WorkItemAddActions
            onAddAssignment={onAddAssignment}
            onAddEvidenceLink={onAddEvidenceLink}
            onAddHandoff={onAddHandoff}
          />
        )}
        {!workClosed && reviewFollowUps.length > 0 && (
          <ReviewFollowUpNotice
            followUp={reviewFollowUps[0]}
            count={reviewFollowUps.length}
            pending={artifactActionID === reviewFollowUps[0].artifact_id}
            onCreateAssignment={() =>
              onCreateAssignmentFromReviewArtifact(reviewFollowUps[0].artifact_id)
            }
          />
        )}
        {(!emptyWorkItem || safeAssignments.length > 0) && (
          <section style={workItemCardSectionStyle}>
            <div style={workItemSectionHeaderStyle}>
              <div style={sectionLabelStyle}>Assignments</div>
              <span className="badge badge-muted">{safeAssignments.length}</span>
            </div>
            {safeAssignments.length === 0 ? (
              <div style={subtleTextStyle}>No assignments recorded yet.</div>
            ) : (
              <div style={{ display: "grid", gap: 10 }}>
                {safeAssignments.map((assignment) => {
                  const activityItem = activityByAssignmentID.get(assignment.id);
                  const reviewRole = reviewerRoleForAssignment(workItem, assignment, roleByID);
                  const reviewAuthorRole = reviewAuthorRoleForAssignment(
                    workItem,
                    assignment,
                    roleByID,
                  );
                  return (
                    <AssignmentRow
                      key={assignment.id}
                      elementID={workItemAssignmentElementID(assignment.id)}
                      assignment={assignment}
                      chatModel={
                        assignment.execution?.model ||
                        roleByID.get(assignment.role_id)?.default_model ||
                        project?.default_model ||
                        ""
                      }
                      error={assignmentErrors[assignment.id] ?? ""}
                      onDelete={() => onDeleteAssignment(assignment)}
                      onEdit={() => onEditAssignment(assignment)}
                      onOpenChat={
                        project
                          ? () => {
                              const executionRef =
                                toProjectAssignmentExecutionViewModel(assignment);
                              const chatRequest = buildProjectAssignmentChatLaunchRequest({
                                project,
                                workItem,
                                assignment,
                                role: roleByID.get(assignment.role_id) ?? null,
                              });
                              const linkedChatRequest = {
                                projectID: project.id,
                                chatSessionID: executionRef.chatSessionID,
                                ...(assignment.driver_kind === "external_agent" &&
                                !executionRef.messageID
                                  ? chatRequest
                                  : {}),
                              };
                              onOpenChat?.(
                                executionRef.chatSessionID ? linkedChatRequest : chatRequest,
                              );
                            }
                          : undefined
                      }
                      onOpenTask={onOpenTask}
                      onComplete={() => onSetAssignmentStatus(assignment, "completed")}
                      onResume={() => onSetAssignmentStatus(assignment, "running")}
                      onStart={() => onStartAssignment(assignment)}
                      autoOpenPreflight={preparingAssignmentID === assignment.id}
                      onAutoOpenPreflightHandled={onPreparedAssignmentPreflightOpened}
                      onCreateHandoff={() => onAddHandoffFromAssignment(assignment, activityItem)}
                      onCreateReviewHandoff={
                        reviewRole
                          ? () =>
                              onAddReviewHandoffFromAssignment(assignment, reviewRole, activityItem)
                          : undefined
                      }
                      onCreateReviewArtifact={
                        reviewAuthorRole
                          ? () => onAddReviewArtifactFromAssignment(assignment)
                          : undefined
                      }
                      project={project}
                      promoteCompletionAction={!closeoutProminent}
                      primaryEmphasis={assignment.id === primaryAssignmentID}
                      repairActions={{
                        onManagePresets,
                        onManageRoles,
                        onOpenConnections,
                        onOpenProjectSettings: onOpenSettings,
                      }}
                      role={roleByID.get(assignment.role_id)}
                      readOnly={workClosed}
                      starting={startingAssignmentIDs.has(assignment.id)}
                      loadContext={
                        project
                          ? async () =>
                              (
                                await getProjectAssignmentContext(
                                  project.id,
                                  workItem.id,
                                  assignment.id,
                                )
                              ).data
                          : null
                      }
                      loadPreflight={
                        project
                          ? async () =>
                              (
                                await getProjectAssignmentPreflight(
                                  project.id,
                                  workItem.id,
                                  assignment.id,
                                )
                              ).data
                          : null
                      }
                      loadReadiness={
                        project
                          ? async () =>
                              (
                                await getProjectAssignmentLaunchReadiness(
                                  project.id,
                                  workItem.id,
                                  assignment.id,
                                )
                              ).data
                          : null
                      }
                    />
                  );
                })}
              </div>
            )}
          </section>
        )}
        {!emptyWorkItem && !workClosed && !closeoutProminent && (
          <WorkItemCloseoutPanel
            closeout={closeout}
            pending={closingWorkItemID === workItem.id}
            onReview={() => setCloseoutConfirmOpen(true)}
          />
        )}
        {(!emptyWorkItem || safeArtifacts.length > 0) && (
          <section style={workItemCardSectionStyle}>
            <div style={workItemSectionHeaderStyle}>
              <div style={sectionLabelStyle}>Collaboration Artifacts</div>
              <span className="badge badge-muted">{safeArtifacts.length}</span>
            </div>
            {safeArtifacts.length === 0 ? (
              <div style={subtleTextStyle}>No collaboration artifacts recorded yet.</div>
            ) : (
              <div style={{ display: "grid", gap: 8 }}>
                {safeArtifacts.map((artifact) => {
                  const artifactActionPending = artifactActionID === artifact.id;
                  return (
                    <div
                      aria-label={`${artifact.title || artifact.id} ${projectRecordLabel(artifact.kind)} artifact`}
                      className="project-work-focus-target"
                      id={workItemArtifactElementID(artifact.id)}
                      key={artifact.id}
                      role="group"
                      style={artifactStyle}
                      tabIndex={-1}
                    >
                      <div style={artifactHeaderStyle}>
                        <div style={artifactIdentityStyle}>
                          <span className="badge badge-muted">
                            {projectRecordLabel(artifact.kind)}
                          </span>
                          {artifact.kind === "review" && artifact.review_verdict && (
                            <span className="badge badge-muted">
                              {reviewVerdictLabel(reviewVerdictFromValue(artifact.review_verdict))}
                            </span>
                          )}
                          {artifact.kind === "review" && artifact.review_risk && (
                            <span className="badge badge-muted">
                              risk {reviewRiskLabel(reviewRiskFromValue(artifact.review_risk))}
                            </span>
                          )}
                          {artifact.kind === "review" && artifact.review_follow_up_required && (
                            <span className="badge badge-amber">follow-up required</span>
                          )}
                          {artifact.kind === "evidence_link" && artifact.evidence_source_kind && (
                            <span className="badge badge-muted">
                              {projectRecordLabel(artifact.evidence_source_kind)}
                            </span>
                          )}
                          {artifact.kind === "evidence_link" && artifact.evidence_trust_label && (
                            <span className="badge badge-muted">
                              {projectRecordLabel(artifact.evidence_trust_label)}
                            </span>
                          )}
                          <span style={artifactTitleStyle}>{artifact.title || artifact.id}</span>
                        </div>
                        {!workClosed && artifact.kind === "review" && (
                          <div
                            aria-label={`Actions for review artifact ${artifact.title || artifact.id}`}
                            role="group"
                            style={artifactActionsStyle}
                          >
                            <button
                              aria-label={`Create follow-up from review artifact ${artifact.id}`}
                              className="btn btn-ghost btn-sm"
                              type="button"
                              onClick={() => onAddHandoffFromReviewArtifact(artifact)}
                              disabled={artifactActionPending}
                            >
                              <Icon d={Icons.plus} size={12} />
                              Follow-up
                            </button>
                            <button
                              aria-label={`Draft follow-up assignment from review artifact ${artifact.id}`}
                              className="btn btn-ghost btn-sm"
                              type="button"
                              onClick={() => onCreateAssignmentFromReviewArtifact(artifact.id)}
                              disabled={artifactActionPending}
                              title="Draft a Project Assistant proposal for a handoff and queued follow-up assignment."
                            >
                              <Icon d={Icons.tasks} size={12} />
                              Draft
                            </button>
                          </div>
                        )}
                      </div>
                      <div
                        style={{
                          marginTop: 6,
                          fontSize: 12,
                          color: "var(--t2)",
                          lineHeight: 1.45,
                        }}
                      >
                        {artifact.body}
                      </div>
                      {artifact.kind === "evidence_link" && (
                        <EvidenceArtifactMetadata artifact={artifact} />
                      )}
                    </div>
                  );
                })}
              </div>
            )}
          </section>
        )}
        {(!emptyWorkItem || safeHandoffs.length > 0) && (
          <section style={workItemCardSectionStyle}>
            <div style={workItemSectionHeaderStyle}>
              <div style={sectionLabelStyle}>Handoffs</div>
              <span className="badge badge-muted">{safeHandoffs.length}</span>
            </div>
            {handoffError && <InlineError message={handoffError} />}
            {safeHandoffs.length === 0 ? (
              <div style={subtleTextStyle}>No structured handoffs recorded yet.</div>
            ) : (
              <div style={{ display: "grid", gap: 8 }}>
                {safeHandoffs.map((handoff) => {
                  const targetWorkItemID = handoff.target_work_item_id?.trim() || "";
                  const targetsOtherWork =
                    Boolean(targetWorkItemID) && targetWorkItemID !== workItem.id;
                  const targetAssignment = targetsOtherWork
                    ? undefined
                    : safeAssignments.find((item) => item.id === handoff.target_assignment_id);
                  return (
                    <ProjectHandoffRow
                      key={handoff.id}
                      actionPending={handoffActionID === handoff.id}
                      assignment={targetAssignment}
                      elementID={workItemHandoffElementID(handoff.id)}
                      handoff={handoff}
                      onCreateAssignment={() => onCreateAssignmentFromHandoff(handoff)}
                      onDelete={() => onDeleteHandoff(handoff)}
                      onEdit={() => onEditHandoff(handoff)}
                      onOpenTargetWorkItem={
                        targetsOtherWork ? () => onOpenWorkItem(targetWorkItemID) : undefined
                      }
                      onOpenAssignment={
                        targetAssignment
                          ? () => focusWorkItemRecord("assignment", targetAssignment.id)
                          : undefined
                      }
                      onSetStatus={(status) => onSetHandoffStatus(handoff, status)}
                      readOnly={workClosed}
                      role={
                        handoff.target_role_id ? roleByID.get(handoff.target_role_id) : undefined
                      }
                    />
                  );
                })}
              </div>
            )}
          </section>
        )}
      </article>
      {closeoutConfirmOpen && (
        <WorkItemCloseoutConfirmModal
          closeout={closeout}
          error={detailError}
          onClose={() => setCloseoutConfirmOpen(false)}
          onConfirm={() => onCloseWorkItem(workItem)}
          pending={closingWorkItemID === workItem.id}
          workItem={workItem}
        />
      )}
    </div>
  );
}

function WorkItemStartPanel({
  drafting,
  primaryEmphasis,
  onAddAssignment,
  onAddEvidenceLink,
  onAddHandoff,
  onDraftDefaultAssignment,
  onManageRoles,
  role,
}: {
  drafting: boolean;
  primaryEmphasis: boolean;
  onAddAssignment: () => void;
  onAddEvidenceLink: () => void;
  onAddHandoff: () => void;
  onDraftDefaultAssignment: () => void;
  onManageRoles: () => void;
  role: ProjectWorkRoleRecord | null;
}) {
  return (
    <section style={startPanelStyle} aria-label="Start work">
      <div style={startPanelCopyStyle}>
        <div style={sectionLabelStyle}>Next step</div>
        <div style={titleStyle}>
          {role ? "Let Hecate prepare the first step" : "Add a role before assigning work"}
        </div>
        <div style={{ ...subtleTextStyle, marginTop: 5 }}>
          {role
            ? `Hecate will use the ${role.name || role.id} role and this work item context to draft a reviewable assignment proposal. You still approve it before anything is created.`
            : "Create or select a project role so Hecate can prepare this work from defaults."}
        </div>
      </div>
      <div style={startPanelActionsStyle}>
        {role ? (
          <button
            className={`btn ${primaryEmphasis ? "btn-primary" : "btn-ghost"} btn-sm`}
            type="button"
            onClick={onDraftDefaultAssignment}
            disabled={drafting}
          >
            <Icon d={Icons.tasks} size={13} />
            {drafting ? "Preparing..." : "Prepare next step"}
          </button>
        ) : (
          <button
            className={`btn ${primaryEmphasis ? "btn-primary" : "btn-ghost"} btn-sm`}
            type="button"
            onClick={onManageRoles}
          >
            <Icon d={Icons.user} size={13} />
            Manage roles
          </button>
        )}
        <details style={manualAddDetailsStyle}>
          <summary style={manualAddSummaryStyle}>Add manually</summary>
          <div aria-label="Manual work item actions" role="group" style={startPanelSecondaryStyle}>
            <button className="btn btn-ghost btn-sm" type="button" onClick={onAddAssignment}>
              <Icon d={Icons.plus} size={12} />
              Assignment
            </button>
            <button className="btn btn-ghost btn-sm" type="button" onClick={onAddEvidenceLink}>
              <Icon d={Icons.plus} size={12} />
              Evidence
            </button>
            <button className="btn btn-ghost btn-sm" type="button" onClick={onAddHandoff}>
              <Icon d={Icons.plus} size={12} />
              Handoff
            </button>
          </div>
        </details>
      </div>
    </section>
  );
}

function WorkItemAddActions({
  onAddAssignment,
  onAddEvidenceLink,
  onAddHandoff,
}: {
  onAddAssignment: () => void;
  onAddEvidenceLink: () => void;
  onAddHandoff: () => void;
}) {
  return (
    <section style={addActionsPanelStyle} aria-label="Add to work item">
      <div style={addActionsCopyStyle}>
        <div style={sectionLabelStyle}>Add</div>
        <div style={subtleTextStyle}>
          Add an assignment, source evidence, or a structured handoff to this work item.
        </div>
      </div>
      <div aria-label="Add work item records" role="group" style={addActionsButtonsStyle}>
        <button
          aria-label="Add assignment"
          className="btn btn-ghost btn-sm"
          type="button"
          onClick={onAddAssignment}
        >
          <Icon d={Icons.plus} size={12} />
          Assignment
        </button>
        <button
          aria-label="Add evidence"
          className="btn btn-ghost btn-sm"
          type="button"
          onClick={onAddEvidenceLink}
        >
          <Icon d={Icons.plus} size={12} />
          Evidence
        </button>
        <button
          aria-label="Add handoff"
          className="btn btn-ghost btn-sm"
          type="button"
          onClick={onAddHandoff}
        >
          <Icon d={Icons.plus} size={12} />
          Handoff
        </button>
      </div>
    </section>
  );
}

function assignmentRoleForWorkItem(
  workItem: ProjectWorkItemRecord,
  roleByID: Map<string, ProjectWorkRoleRecord>,
): ProjectWorkRoleRecord | null {
  if (workItem.owner_role_id) {
    const ownerRole = roleByID.get(workItem.owner_role_id);
    if (ownerRole) return ownerRole;
  }
  return Array.from(roleByID.values())[0] ?? null;
}

function EvidenceArtifactMetadata({ artifact }: { artifact: ProjectCollaborationArtifactRecord }) {
  const details = [
    artifact.evidence_provider ? `provider ${artifact.evidence_provider}` : "",
    artifact.evidence_external_id ? `external ${artifact.evidence_external_id}` : "",
  ].filter(Boolean);
  return (
    <div style={evidenceArtifactMetadataStyle}>
      {artifact.evidence_url && isLinkableProjectLocator(artifact.evidence_url) && (
        <a href={artifact.evidence_url} target="_blank" rel="noreferrer">
          {artifact.evidence_url}
        </a>
      )}
      {artifact.evidence_url && !isLinkableProjectLocator(artifact.evidence_url) && (
        <span>{artifact.evidence_url}</span>
      )}
      {details.length > 0 && <span>{details.join(" · ")}</span>}
    </div>
  );
}

function unavailableCloseoutReadiness(
  workItem: ProjectWorkItemRecord,
  loading: boolean,
): ProjectWorkItemReadinessRecord {
  return {
    project_id: workItem.project_id,
    work_item_id: workItem.id,
    ready: false,
    status: "blocked",
    title: loading ? "Checking whether this work can close" : "Closeout check unavailable",
    detail: loading
      ? "Checking assignments, reviews, evidence, and handoffs."
      : "Refresh work details before marking done.",
    blockers: [loading ? "Closeout checks are still loading." : "Closeout checks are unavailable."],
    warnings: [],
    assignment_count: 0,
    completed_assignments: 0,
    review_follow_up_count: 0,
  };
}

function workItemAssignmentElementID(assignmentID: string): string {
  return `project-work-assignment-${encodeURIComponent(assignmentID)}`;
}

function workItemElementID(workItemID: string): string {
  return `project-work-item-${encodeURIComponent(workItemID)}`;
}

function workItemArtifactElementID(artifactID: string): string {
  return `project-work-artifact-${encodeURIComponent(artifactID)}`;
}

function workItemHandoffElementID(handoffID: string): string {
  return `project-work-handoff-${encodeURIComponent(handoffID)}`;
}

function focusWorkItemRecord(kind: "assignment" | "artifact" | "handoff", id: string): boolean {
  const elementID =
    kind === "assignment"
      ? workItemAssignmentElementID(id)
      : kind === "artifact"
        ? workItemArtifactElementID(id)
        : workItemHandoffElementID(id);
  return focusElementByID(elementID);
}

function workItemFocusTargetAvailable(
  target: ProjectWorkItemFocusTarget,
  records: {
    artifacts: ProjectCollaborationArtifactRecord[];
    assignments: ProjectAssignmentRecord[];
    handoffs: ProjectHandoffRecord[];
  },
): boolean {
  if (target.artifactID) {
    return records.artifacts.some((artifact) => artifact.id === target.artifactID);
  }
  if (target.handoffID) {
    return records.handoffs.some((handoff) => handoff.id === target.handoffID);
  }
  if (target.assignmentID) {
    return records.assignments.some((assignment) => assignment.id === target.assignmentID);
  }
  if (target.operationKind === "close_work_item") {
    return Boolean(document.getElementById("project-work-closeout"));
  }
  return true;
}

export function projectOperationRequestsFocusTarget(
  operation: ProjectOperationsBriefItem | null | undefined,
  target: ProjectWorkItemFocusTarget,
): boolean {
  if (operation?.action?.type !== "open_work_item") return false;
  if (operation.action.work_item_id !== target.workItemID) return false;
  if (target.artifactID) return operation.action.artifact_id === target.artifactID;
  if (target.handoffID) return operation.action.handoff_id === target.handoffID;
  if (target.assignmentID) return operation.action.assignment_id === target.assignmentID;
  return target.operationKind === "close_work_item" && operation.kind === target.operationKind;
}

function focusWorkItemTarget(target: ProjectWorkItemFocusTarget): boolean {
  if (target.artifactID) return focusWorkItemRecord("artifact", target.artifactID);
  if (target.handoffID) return focusWorkItemRecord("handoff", target.handoffID);
  if (target.assignmentID) return focusWorkItemRecord("assignment", target.assignmentID);
  if (target.operationKind === "close_work_item") {
    return focusElementByID("project-work-closeout");
  }
  return focusElementByID(workItemElementID(target.workItemID));
}

function focusElementByID(elementID: string): boolean {
  const element = document.getElementById(elementID);
  if (!element) return false;
  element.scrollIntoView?.({ behavior: "auto", block: "center" });
  element.focus({ preventScroll: true });
  return true;
}

function projectRecordLabel(value: string | undefined): string {
  const normalized = value?.trim().toLowerCase() || "";
  const knownLabels: Record<string, string> = {
    evidence_link: "Evidence",
    operator: "Operator",
    operator_provided: "Operator provided",
    operator_reviewed: "Operator reviewed",
    review: "Review",
    source_document: "Document",
  };
  if (knownLabels[normalized]) return knownLabels[normalized];
  const words = normalized.replaceAll("_", " ");
  return words ? `${words.charAt(0).toUpperCase()}${words.slice(1)}` : "Record";
}

function WorkItemCloseoutPanel({
  closeout,
  onReview,
  pending,
}: {
  closeout: ProjectWorkItemReadinessRecord;
  onReview: () => void;
  pending: boolean;
}) {
  const status =
    closeout.status === "ready" || closeout.status === "done" ? "completed" : "blocked";
  const blockers = Array.isArray(closeout.blockers) ? closeout.blockers : [];
  const warnings = Array.isArray(closeout.warnings) ? closeout.warnings : [];
  return (
    <section
      aria-label="Work closeout"
      className="project-work-focus-target"
      id="project-work-closeout"
      style={workItemCardSectionStyle}
      tabIndex={-1}
    >
      <div style={workItemSectionHeaderStyle}>
        <div style={sectionLabelStyle}>Closeout</div>
        <Badge status={status} label={closeout.status === "done" ? "done" : closeout.status} />
        <span className="badge badge-muted">
          {closeout.completed_assignments}/{closeout.assignment_count} assignments complete
        </span>
        {closeout.status === "ready" && (
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={onReview}
            disabled={pending}
            style={{ marginLeft: "auto" }}
          >
            <Icon d={Icons.check} size={12} />
            {pending ? "Marking…" : "Review closeout"}
          </button>
        )}
      </div>
      <div style={titleStyle}>{closeout.title}</div>
      <div style={{ ...subtleTextStyle, marginTop: 4 }}>{closeout.detail}</div>
      {(blockers.length > 0 || warnings.length > 0) && (
        <details style={closeoutDetailsStyle}>
          <summary style={manualAddSummaryStyle}>
            {blockers.length > 0
              ? `${blockers.length} closeout ${blockers.length === 1 ? "blocker" : "blockers"}`
              : `${warnings.length} ${warnings.length === 1 ? "note" : "notes"}`}
          </summary>
          {blockers.length > 0 && (
            <ul style={closeoutListStyle}>
              {blockers.map((blocker) => (
                <li key={blocker}>{blocker}</li>
              ))}
            </ul>
          )}
          {warnings.length > 0 && (
            <div style={{ ...subtleTextStyle, marginTop: 8 }}>
              {warnings.map((warning) => (
                <div key={warning}>{warning}</div>
              ))}
            </div>
          )}
        </details>
      )}
    </section>
  );
}

function WorkItemCloseoutConfirmModal({
  closeout,
  error,
  onClose,
  onConfirm,
  pending,
  workItem,
}: {
  closeout: ProjectWorkItemReadinessRecord;
  error: string;
  onClose: () => void;
  onConfirm: () => void;
  pending: boolean;
  workItem: ProjectWorkItemRecord;
}) {
  const blockers = Array.isArray(closeout.blockers) ? closeout.blockers : [];
  return (
    <Modal
      title="Review closeout"
      dismissible={!pending}
      onClose={onClose}
      width={520}
      footer={
        <div
          style={{
            display: "grid",
            gap: 8,
            gridTemplateColumns: "auto minmax(0, 1fr)",
          }}
        >
          <button className="btn btn-ghost" type="button" disabled={pending} onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn btn-primary"
            type="button"
            disabled={pending || !closeout.ready}
            onClick={onConfirm}
            style={{ justifyContent: "center" }}
          >
            <Icon d={Icons.check} size={13} />
            {pending ? "Marking work done…" : "Mark work done"}
          </button>
        </div>
      }
    >
      <div style={{ display: "grid", gap: 14 }}>
        {error && <InlineError message={error} />}
        <div>
          <div className="kicker">Work item</div>
          <div style={{ ...titleStyle, marginTop: 5 }}>{workItem.title}</div>
        </div>
        <div className="project-closeout-summary" style={closeoutConfirmationSummaryStyle}>
          <div>
            <strong>{closeout.completed_assignments}</strong>
            <span>Assignments complete</span>
          </div>
          <div>
            <strong>{closeout.review_follow_up_count}</strong>
            <span>Review follow-ups</span>
          </div>
          <div>
            <strong>{(closeout.open_handoff_ids ?? []).length}</strong>
            <span>Open handoffs</span>
          </div>
        </div>
        {!closeout.ready && (
          <section aria-label="Updated closeout readiness" style={closeoutBlockedNoticeStyle}>
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <Badge status="blocked" label="blocked" />
              <strong style={{ ...titleStyle, fontSize: 12 }}>{closeout.title}</strong>
            </div>
            <p style={{ ...subtleTextStyle, margin: "7px 0 0" }}>{closeout.detail}</p>
            {blockers.length > 0 && (
              <ul style={closeoutListStyle}>
                {blockers.map((blocker) => (
                  <li key={blocker}>{blocker}</li>
                ))}
              </ul>
            )}
          </section>
        )}
        <p style={{ ...subtleTextStyle, margin: 0 }}>
          Marking done records the operator’s closeout decision. It does not delete assignments,
          linked tasks or chats, reviews, evidence, or handoffs.
        </p>
      </div>
    </Modal>
  );
}

function ReviewFollowUpNotice({
  followUp,
  count,
  onCreateAssignment,
  pending,
}: {
  followUp: ProjectWorkItemReviewFollowUpRecord;
  count: number;
  onCreateAssignment: () => void;
  pending: boolean;
}) {
  return (
    <section style={reviewFollowUpNoticeStyle} aria-label="Review follow-up required">
      <div style={workItemSectionHeaderStyle}>
        <span className="badge badge-amber">follow-up required</span>
        {count > 1 && <span className="badge badge-muted">{count} reviews</span>}
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          onClick={onCreateAssignment}
          disabled={pending}
          style={{ marginLeft: "auto" }}
        >
          <Icon d={Icons.tasks} size={12} />
          {pending ? "Drafting..." : "Draft follow-up"}
        </button>
      </div>
      <div style={{ ...titleStyle, marginTop: 8 }}>{followUp.title || "Review follow-up"}</div>
      <div style={{ ...subtleTextStyle, marginTop: 4 }}>
        Review outcome blocks closeout until the operator reviews a follow-up proposal and completes
        the resulting assignment.
      </div>
    </section>
  );
}

function ReviewerSetupNotice({
  onEditWorkItem,
  onManageRoles,
  roleByID,
  workItem,
}: {
  onEditWorkItem: () => void;
  onManageRoles: () => void;
  roleByID: Map<string, ProjectWorkRoleRecord>;
  workItem: ProjectWorkItemRecord;
}) {
  const reviewerRoleIDs = (workItem.reviewer_role_ids ?? [])
    .map((roleID) => roleID.trim())
    .filter(Boolean);
  const missingRoleIDs = reviewerRoleIDs.filter((roleID) => !roleByID.has(roleID));
  if (reviewerRoleIDs.length > 0 && missingRoleIDs.length === 0) return null;
  const missingReviewerRoles = missingRoleIDs.length > 0;
  return (
    <div style={reviewerSetupNoticeStyle}>
      <div style={{ minWidth: 0 }}>
        <div style={titleStyle}>
          {missingReviewerRoles
            ? "Reviewer role reference missing"
            : "No reviewer roles configured"}
        </div>
        <div style={subtleTextStyle}>
          {missingReviewerRoles
            ? `Review recording needs these roles to exist: ${missingRoleIDs.join(", ")}.`
            : "Add at least one reviewer role to enable review requests and review artifact recording for this work item."}
        </div>
      </div>
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        <button className="btn btn-ghost btn-sm" type="button" onClick={onEditWorkItem}>
          <Icon d={Icons.edit} size={12} />
          Edit reviewers
        </button>
        <button className="btn btn-ghost btn-sm" type="button" onClick={onManageRoles}>
          <Icon d={Icons.user} size={12} />
          Manage roles
        </button>
      </div>
    </div>
  );
}

function AssignmentRow({
  assignment,
  autoOpenPreflight,
  chatModel,
  elementID,
  error,
  loadContext,
  loadPreflight,
  loadReadiness,
  onCreateHandoff,
  onCreateReviewHandoff,
  onCreateReviewArtifact,
  onAutoOpenPreflightHandled,
  onComplete,
  onDelete,
  onEdit,
  onOpenChat,
  onOpenTask,
  onResume,
  onStart,
  project,
  primaryEmphasis,
  promoteCompletionAction,
  readOnly,
  repairActions,
  role,
  starting,
}: {
  assignment: ProjectAssignmentRecord;
  autoOpenPreflight?: boolean;
  chatModel: string;
  elementID?: string;
  error: string;
  loadContext?: (() => Promise<ContextPacketRecord>) | null;
  loadPreflight?: (() => Promise<ContextPacketRecord>) | null;
  loadReadiness?: (() => Promise<ProjectAssignmentLaunchReadinessRecord>) | null;
  onCreateHandoff: () => void;
  onCreateReviewHandoff?: () => void;
  onCreateReviewArtifact?: () => void;
  onAutoOpenPreflightHandled?: (assignmentID: string) => void;
  onComplete: () => void;
  onDelete: () => void;
  onEdit: () => void;
  onOpenChat?: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onResume: () => void;
  onStart: () => void;
  project: ProjectRecord | null;
  primaryEmphasis?: boolean;
  promoteCompletionAction: boolean;
  readOnly?: boolean;
  repairActions?: AssignmentLaunchRepairActions;
  role?: ProjectWorkRoleRecord;
  starting: boolean;
}) {
  const [preflightOpen, setPreflightOpen] = useState(false);
  const [preflightState, setPreflightState] = useState<AssignmentPreflightState>({
    status: "idle",
  });
  const [readinessPreview, setReadinessPreview] = useState<AssignmentLaunchReadinessPreviewState>({
    status: "idle",
  });
  const assignmentExecution = toProjectAssignmentExecutionViewModel(assignment);
  const projectedStatus = assignmentExecution.status;
  const startable =
    (assignment.driver_kind === "hecate_task" || assignment.driver_kind === "external_agent") &&
    projectedStatus === "queued";
  const external = assignment.driver_kind === "external_agent";

  const loadReadinessPreview = useCallback(async () => {
    if (!loadReadiness) return;
    setReadinessPreview({ status: "loading" });
    try {
      const readiness = await loadReadiness();
      setReadinessPreview({ status: "ready", readiness });
    } catch (error) {
      setReadinessPreview({
        status: "error",
        detail: projectErrorMessage(error, "Failed to load assignment launch readiness."),
      });
    }
  }, [loadReadiness]);

  const openPreflight = useCallback(async () => {
    if (!loadPreflight || !loadReadiness) {
      onStart();
      return;
    }
    setPreflightOpen(true);
    setPreflightState({ status: "loading" });
    try {
      const [readiness, packet] = await Promise.all([loadReadiness(), loadPreflight()]);
      setReadinessPreview({ status: "ready", readiness });
      setPreflightState({ status: "ready", packet, readiness });
    } catch (error) {
      setPreflightState({
        status: "error",
        detail: projectErrorMessage(error, "Failed to load assignment launch checks."),
      });
    }
  }, [loadPreflight, loadReadiness, onStart]);

  useEffect(() => {
    if (!autoOpenPreflight || readOnly) return;
    onAutoOpenPreflightHandled?.(assignment.id);
    if (!startable) return;
    if (!loadPreflight || !loadReadiness) return;
    void openPreflight();
  }, [
    assignment.id,
    autoOpenPreflight,
    loadPreflight,
    loadReadiness,
    onAutoOpenPreflightHandled,
    openPreflight,
    readOnly,
    startable,
  ]);

  return (
    <>
      <ProjectAssignmentExecutionStory
        assignment={assignment}
        chatModel={chatModel}
        elementID={elementID}
        contextControl={
          <ContextInspectorModalTrigger
            buttonLabel="Inspect context"
            buttonTitle="Inspect the best available stored context snapshot for this assignment."
            loadPacket={loadContext}
            modalTitle={`Assignment ${assignment.id} context`}
            resourceKey={assignment.id}
            unavailableDetail="This assignment does not have a stored context packet yet. Unstarted assignments, legacy rows, and older linked runs can legitimately return no snapshot."
          />
        }
        error={error}
        onCreateHandoff={onCreateHandoff}
        onCreateReviewArtifact={onCreateReviewArtifact}
        onCreateReviewHandoff={onCreateReviewHandoff}
        onCompleteWork={onComplete}
        onDelete={onDelete}
        onEdit={onEdit}
        onOpenChat={onOpenChat}
        onOpenTask={onOpenTask}
        onReviewLaunch={() => void openPreflight()}
        onResumeWork={onResume}
        onStartWork={onStart}
        project={project}
        primaryEmphasis={primaryEmphasis}
        promoteCompletionAction={promoteCompletionAction}
        readOnly={readOnly}
        readinessControl={
          startable && loadReadiness ? (
            <AssignmentLaunchReadinessPreview
              onCheck={() => void loadReadinessPreview()}
              state={readinessPreview}
            />
          ) : undefined
        }
        role={role}
        starting={starting}
      />
      {preflightOpen && (
        <AssignmentLaunchPreflightModal
          assignmentID={assignment.id}
          confirmLabel={external ? "Prepare chat" : "Start assignment"}
          loadingLabel={external ? "Preparing..." : "Starting..."}
          onClose={() => setPreflightOpen(false)}
          onConfirm={() => {
            setPreflightOpen(false);
            onStart();
          }}
          onReload={() => void openPreflight()}
          pending={starting}
          repairActions={repairActions}
          state={preflightState}
        />
      )}
    </>
  );
}

function AssignmentLaunchReadinessPreview({
  onCheck,
  state,
}: {
  onCheck: () => void;
  state: AssignmentLaunchReadinessPreviewState;
}) {
  const loading = state.status === "loading";
  const readiness = state.status === "ready" ? state.readiness : null;
  const rows = readiness ? assignmentLaunchPostureRows(readiness) : [];
  const statusLabel = readiness?.status || (readiness?.ready ? "ready" : "not checked");
  const firstBlocker = readiness?.blockers?.[0];
  const firstWarning = readiness?.warnings?.[0];
  return (
    <section aria-label="Assignment launch readiness" style={launchReadinessPreviewStyle}>
      <div style={launchReadinessPreviewHeaderStyle}>
        <div style={sectionLabelStyle}>Launch readiness</div>
        <span
          className={
            readiness ? (readiness.ready ? "badge badge-green" : "badge badge-amber") : "badge"
          }
        >
          {loading ? "checking" : statusLabel}
        </span>
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          onClick={onCheck}
          disabled={loading}
          style={{ marginLeft: "auto" }}
        >
          <Icon d={Icons.refresh} size={12} />
          {readiness ? "Refresh" : "Check readiness"}
        </button>
      </div>
      {state.status === "idle" && (
        <div style={subtleTextStyle}>
          Check the work destination, workspace, preset, and target before reviewing launch details.
        </div>
      )}
      {state.status === "loading" && <div style={subtleTextStyle}>Checking launch readiness…</div>}
      {state.status === "error" && <InlineError message={state.detail} />}
      {readiness && (
        <>
          <div style={launchReadinessPreviewGridStyle}>
            {rows.map((row) => (
              <div key={row.label} style={launchReadinessPreviewItemStyle}>
                <span style={launchPostureLabelStyle}>{row.label}</span>
                <span style={launchReadinessPreviewValueStyle} title={row.value}>
                  {row.value}
                </span>
              </div>
            ))}
          </div>
          {(firstBlocker || firstWarning || readiness.detail) && (
            <div style={readiness.ready ? subtleTextStyle : launchReadinessBlockerStyle}>
              {firstBlocker || firstWarning || readiness.detail}
            </div>
          )}
        </>
      )}
    </section>
  );
}

function AssignmentLaunchPreflightModal({
  assignmentID,
  confirmLabel,
  loadingLabel,
  onClose,
  onConfirm,
  onReload,
  pending,
  repairActions,
  state,
}: {
  assignmentID: string;
  confirmLabel: string;
  loadingLabel: string;
  onClose: () => void;
  onConfirm: () => void;
  onReload: () => void;
  pending: boolean;
  repairActions?: AssignmentLaunchRepairActions;
  state: AssignmentPreflightState;
}) {
  const ready = state.status === "ready";
  const readinessNotice = ready ? assignmentLaunchReadinessNotice(state.readiness) : null;
  const readinessWarnings = ready ? (state.readiness.warnings ?? []) : [];
  const canConfirm = ready && !readinessNotice;
  const runRepairAction = useCallback(
    (action?: () => void) => {
      onClose();
      action?.();
    },
    [onClose],
  );
  return (
    <Modal
      title={`Assignment ${assignmentID} launch details`}
      width={860}
      onClose={onClose}
      footer={
        <>
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Close
          </button>
          <button className="btn btn-ghost" type="button" onClick={onReload} disabled={pending}>
            Reload
          </button>
          <button
            className="btn btn-primary"
            type="button"
            onClick={onConfirm}
            disabled={!canConfirm || pending}
            title={
              readinessNotice
                ? "Resolve the launch readiness blockers before starting this assignment."
                : undefined
            }
          >
            {pending ? loadingLabel : confirmLabel}
          </button>
        </>
      }
    >
      <div style={{ padding: 16, overflowY: "auto" }}>
        {state.status === "idle" || state.status === "loading" ? (
          <div style={{ display: "grid", gap: 6 }}>
            <div style={titleStyle}>Loading launch details…</div>
            <div style={subtleTextStyle}>
              Checking the assignment launch context before any task, run, or chat session is
              created.
            </div>
          </div>
        ) : null}
        {state.status === "error" ? <InlineError message={state.detail} /> : null}
        {ready ? (
          <div style={{ display: "grid", gap: 12 }}>
            {readinessNotice && (
              <AssignmentLaunchReadinessNotice
                notice={readinessNotice}
                onRepairAction={runRepairAction}
                repairActions={repairActions}
              />
            )}
            {!readinessNotice && readinessWarnings.length > 0 && (
              <AssignmentLaunchWarnings warnings={readinessWarnings} />
            )}
            <AssignmentLaunchPosture readiness={state.readiness} />
            <ContextInspectorPanel
              packet={state.packet}
              emptyDetail="No launch context metadata returned."
            />
          </div>
        ) : null}
      </div>
    </Modal>
  );
}

function AssignmentLaunchWarnings({ warnings }: { warnings: string[] }) {
  return (
    <div aria-label="Launch readiness warnings" role="status" style={launchWarningNoticeStyle}>
      <div style={launchWarningNoticeTitleStyle}>
        <Icon d={Icons.warning} size={13} />
        Launch warnings
      </div>
      <ul style={{ margin: "2px 0 0 18px", padding: 0 }}>
        {warnings.map((warning) => (
          <li key={warning}>{warning}</li>
        ))}
      </ul>
    </div>
  );
}

function AssignmentLaunchPosture({
  readiness,
}: {
  readiness: ProjectAssignmentLaunchReadinessRecord;
}) {
  const rows = assignmentLaunchPostureRows(readiness);
  return (
    <section aria-label="Resolved launch posture" style={launchPosturePanelStyle}>
      <div style={{ display: "flex", justifyContent: "space-between", gap: 8 }}>
        <div style={sectionLabelStyle}>Launch posture</div>
        <span className={readiness.ready ? "badge badge-green" : "badge badge-amber"}>
          {readiness.status || (readiness.ready ? "ready" : "blocked")}
        </span>
      </div>
      <div style={launchPostureGridStyle}>
        {rows.map((row) => (
          <div key={row.label} style={launchPostureItemStyle}>
            <div style={launchPostureLabelStyle}>{row.label}</div>
            <div style={launchPostureValueStyle} title={row.value}>
              {row.value}
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}

function AssignmentLaunchReadinessNotice({
  notice,
  onRepairAction,
  repairActions,
}: {
  notice: AssignmentLaunchReadinessNoticeRecord;
  onRepairAction: (action?: () => void) => void;
  repairActions?: AssignmentLaunchRepairActions;
}) {
  const actions = [
    {
      key: "project-settings",
      label: "Open project settings",
      onClick: repairActions?.onOpenProjectSettings,
    },
    {
      key: "roles",
      label: "Manage roles",
      onClick: repairActions?.onManageRoles,
    },
    {
      key: "profiles",
      label: "Agent presets",
      onClick: repairActions?.onManagePresets,
    },
    {
      key: "connections",
      label: "Open Connections",
      onClick: repairActions?.onOpenConnections,
    },
  ].filter((action) => action.onClick);
  return (
    <div
      style={{
        background: "var(--amber-bg)",
        border: "1px solid var(--amber-border)",
        borderRadius: "var(--radius-sm)",
        display: "grid",
        gap: 6,
        padding: "10px 12px",
      }}
    >
      <div role="status" style={{ display: "grid", gap: 6 }}>
        <div
          style={{
            alignItems: "center",
            color: "var(--amber)",
            display: "flex",
            fontWeight: 600,
            gap: 8,
          }}
        >
          <Icon d={Icons.warning} size={13} />
          {notice.title}
        </div>
        <div style={{ color: "var(--amber-lo)", fontSize: 12, lineHeight: 1.45 }}>
          {notice.detail}
        </div>
        {notice.blockers.length > 0 && (
          <ul style={{ margin: "2px 0 0 18px", padding: 0 }}>
            {notice.blockers.map((blocker) => (
              <li key={blocker}>{blocker}</li>
            ))}
          </ul>
        )}
        {notice.warnings.length > 0 && (
          <div style={{ color: "var(--amber-lo)", fontSize: 12, lineHeight: 1.45 }}>
            {notice.warnings.join(" ")}
          </div>
        )}
      </div>
      {actions.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginTop: 2 }}>
          {actions.map((action) => (
            <button
              key={action.key}
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => onRepairAction(action.onClick)}
            >
              {action.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function ProjectHandoffRow({
  actionPending,
  assignment,
  elementID,
  handoff,
  onCreateAssignment,
  onDelete,
  onEdit,
  onOpenAssignment,
  onOpenTargetWorkItem,
  onSetStatus,
  readOnly,
  role,
}: {
  actionPending: boolean;
  assignment?: ProjectAssignmentRecord;
  elementID?: string;
  handoff: ProjectHandoffRecord;
  onCreateAssignment: () => void;
  onDelete: () => void;
  onEdit: () => void;
  onOpenAssignment?: () => void;
  onOpenTargetWorkItem?: () => void;
  onSetStatus: (status: string) => void;
  readOnly?: boolean;
  role?: ProjectWorkRoleRecord;
}) {
  const handoffClosed = handoff.status === "dismissed" || handoff.status === "superseded";
  const targetEvidence = assignment ? toProjectAssignmentEvidenceViewModel(assignment) : null;
  const interactionPending = actionPending;
  const hasLinkedAssignment = Boolean(handoff.target_assignment_id);
  const canCreateAssignment = !handoffClosed && !hasLinkedAssignment && !assignment;
  const sourceRefs = handoffSourceRefs(handoff);

  return (
    <div
      aria-label={`${handoff.title} handoff`}
      className="project-work-focus-target"
      id={elementID}
      role="group"
      style={artifactStyle}
      tabIndex={-1}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          flexWrap: "wrap",
          gap: 8,
          minWidth: 0,
        }}
      >
        <Badge status={handoff.status} label={handoffStatusLabel(handoff.status)} />
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{handoff.title}</div>
        {!readOnly && (
          <>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              disabled={interactionPending}
              onClick={onEdit}
            >
              <Icon d={Icons.edit} size={12} />
              Edit
            </button>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={onDelete}
              disabled={interactionPending}
              style={{ color: "var(--red)" }}
            >
              <Icon d={Icons.trash} size={12} />
              Delete
            </button>
          </>
        )}
      </div>
      <div
        style={{
          marginTop: 7,
          fontSize: 12,
          color: "var(--t2)",
          lineHeight: 1.45,
        }}
      >
        {handoff.summary}
      </div>
      <div
        style={{
          marginTop: 7,
          fontSize: 12,
          color: "var(--t1)",
          lineHeight: 1.45,
        }}
      >
        Next: {handoff.recommended_next_action}
      </div>
      <div style={{ ...metaLineStyle, marginTop: 8 }}>
        {role && <span>Target {role.name}</span>}
        {handoff.target_assignment_id && (
          <span>Assignment {shortID(handoff.target_assignment_id)}</span>
        )}
        <span>Source {projectRecordLabel(handoff.provenance_kind)}</span>
        <span>{projectRecordLabel(handoff.trust_label)}</span>
        {handoff.updated_at && <span>Updated {formatAbsoluteTime(handoff.updated_at)}</span>}
      </div>
      {sourceRefs.length > 0 && <HandoffSourceEvidence refs={sourceRefs} />}
      {targetEvidence?.hasEvidence && (
        <ProjectAssignmentEvidence evidence={targetEvidence} title="Target evidence" compact />
      )}
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginTop: 9 }}>
        {!readOnly && handoff.status === "pending" && (
          <>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => onSetStatus("accepted")}
              disabled={interactionPending}
            >
              <Icon d={Icons.check} size={12} />
              Accept
            </button>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => onSetStatus("dismissed")}
              disabled={interactionPending}
            >
              Dismiss
            </button>
          </>
        )}
        {!readOnly && handoff.status !== "superseded" && (
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={() => onSetStatus("superseded")}
            disabled={interactionPending}
          >
            Supersede
          </button>
        )}
        {!readOnly && canCreateAssignment && (
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={onCreateAssignment}
            disabled={interactionPending}
          >
            <Icon d={Icons.plus} size={12} />
            Create follow-up assignment
          </button>
        )}
        {!assignment && hasLinkedAssignment && onOpenTargetWorkItem && (
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            disabled={interactionPending}
            onClick={onOpenTargetWorkItem}
            title="Open the work item that owns the linked assignment."
          >
            Open target work
          </button>
        )}
        {assignment && onOpenAssignment && (
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={onOpenAssignment}
            disabled={interactionPending}
          >
            <Icon d={Icons.chevR} size={12} />
            Open linked assignment
          </button>
        )}
      </div>
    </div>
  );
}

function ProjectAssignmentEvidence({
  compact = false,
  evidence,
  title = "Execution evidence",
}: {
  compact?: boolean;
  evidence: ProjectAssignmentEvidenceViewModel;
  title?: string;
}) {
  if (!evidence.hasEvidence) return null;
  return (
    <div
      aria-label={title}
      role="group"
      style={{
        ...assignmentEvidenceStyle,
        padding: compact ? "8px 9px" : assignmentEvidenceStyle.padding,
      }}
    >
      <div className="kicker">{title}</div>
      {evidence.items.length > 0 && (
        <div style={assignmentEvidenceGridStyle}>
          {evidence.items.map((item) => (
            <div key={item.key} style={assignmentEvidenceCellStyle}>
              <div style={assignmentEvidenceLabelStyle}>{item.label}</div>
              <div style={assignmentEvidenceValueStyle}>{item.value}</div>
            </div>
          ))}
        </div>
      )}
      {evidence.warnings.length > 0 && (
        <div style={assignmentEvidenceWarningsStyle}>
          {evidence.warnings.map((warning) => (
            <span
              key={warning}
              className="badge badge-amber"
              style={{
                maxWidth: "100%",
                overflowWrap: "anywhere",
                whiteSpace: "normal",
              }}
            >
              {warning}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function HandoffSourceEvidence({ refs }: { refs: string[] }) {
  return (
    <div aria-label="Source evidence" role="group" style={assignmentEvidenceStyle}>
      <div className="kicker">Source evidence</div>
      <div style={assignmentEvidenceWarningsStyle}>
        {refs.map((ref) => (
          <span key={ref} className="badge badge-muted">
            {ref}
          </span>
        ))}
      </div>
    </div>
  );
}

function EmptyBlock({ title, detail }: { title: string; detail: string }) {
  return (
    <div
      style={{
        padding: 24,
        textAlign: "center",
        display: "grid",
        gap: 8,
        placeItems: "center",
      }}
    >
      <div style={{ color: "var(--t0)", fontSize: 14, fontWeight: 600 }}>{title}</div>
      <div
        style={{
          color: "var(--t2)",
          fontSize: 12,
          lineHeight: 1.5,
          maxWidth: 320,
        }}
      >
        {detail}
      </div>
    </div>
  );
}

function handoffSourceRefs(handoff: ProjectHandoffRecord): string[] {
  const refs = [
    ["assignment", handoff.source_assignment_id],
    ["run", handoff.source_run_id],
    ["chat", handoff.source_chat_session_id],
    ["message", handoff.source_message_id],
  ]
    .map(([label, value]) => (value ? `${label} ${shortID(value)}` : ""))
    .filter(Boolean);
  for (const ref of handoff.context_refs ?? []) {
    if (ref && !refs.some((item) => item.endsWith(shortID(ref)))) {
      refs.push(`context ${shortID(ref)}`);
    }
  }
  return refs;
}

function reviewerRoleForAssignment(
  workItem: ProjectWorkItemRecord,
  assignment: ProjectAssignmentRecord,
  roleByID: Map<string, ProjectWorkRoleRecord>,
): ProjectWorkRoleRecord | null {
  for (const rawRoleID of workItem.reviewer_role_ids ?? []) {
    const roleID = rawRoleID.trim();
    if (!roleID || roleID === assignment.role_id) continue;
    const role = roleByID.get(roleID);
    if (role) return role;
  }
  return null;
}

function reviewAuthorRoleForAssignment(
  workItem: ProjectWorkItemRecord,
  assignment: ProjectAssignmentRecord,
  roleByID: Map<string, ProjectWorkRoleRecord>,
): ProjectWorkRoleRecord | null {
  const reviewerIDs = new Set((workItem.reviewer_role_ids ?? []).map((roleID) => roleID.trim()));
  if (!reviewerIDs.has(assignment.role_id)) return null;
  return roleByID.get(assignment.role_id) ?? null;
}

export function buildProjectAssignmentChatLaunchRequest({
  project,
  workItem,
  assignment,
  role,
}: {
  project: ProjectRecord;
  workItem: ProjectWorkItemRecord;
  assignment: ProjectAssignmentRecord;
  role: ProjectWorkRoleRecord | null;
}): ProjectAssignmentChatLaunchRequest {
  const provider =
    assignment.execution?.provider || role?.default_provider || project.default_provider || "";
  const model = assignment.execution?.model || role?.default_model || project.default_model || "";
  const title = [workItem.title, role?.name]
    .map((part) => part?.trim())
    .filter(Boolean)
    .join(" - ");
  return {
    projectID: project.id,
    provider,
    model,
    title: title || "Project assignment chat",
    draft: projectAssignmentLaunchDraft({
      project,
      workItem,
      assignment,
      role,
      provider,
      model,
    }),
  };
}

function projectAssignmentLaunchDraft({
  project,
  workItem,
  assignment,
  role,
  provider,
  model,
}: {
  project: ProjectRecord;
  workItem: ProjectWorkItemRecord;
  assignment: ProjectAssignmentRecord;
  role: ProjectWorkRoleRecord | null;
  provider: string;
  model: string;
}): string {
  const execution = toProjectAssignmentExecutionViewModel(assignment);
  const resolvedDriver = firstNonEmpty(
    assignment.driver_kind,
    role?.default_driver_kind,
    "hecate_task",
  );
  const resolvedPreset = firstNonEmpty(role?.default_agent_profile, project.default_agent_profile);
  const lines = [
    "Launch context",
    "",
    `Project: ${labelWithID(project.name, project.id)}`,
    "",
    "Work item:",
    `- Title: ${firstNonEmpty(workItem.title, workItem.id)}`,
    formatLaunchContextBullet("Brief", firstNonEmpty(workItem.brief, "No brief recorded.")),
    `- Status: ${firstNonEmpty(workItem.status, "unknown")}`,
    `- Priority: ${firstNonEmpty(workItem.priority, "normal")}`,
    "",
    "Assignment:",
    `- ID: ${assignment.id}`,
    `- Status: ${firstNonEmpty(execution.status, "queued")}`,
    `- Driver: ${resolvedDriver}`,
    "",
    "Role:",
    `- Name: ${firstNonEmpty(role?.name, assignment.role_id)}`,
    formatLaunchContextBullet(
      "Description",
      firstNonEmpty(role?.description, "No description recorded."),
    ),
    formatLaunchContextBullet(
      "Instructions",
      firstNonEmpty(role?.instructions, "No role instructions recorded."),
    ),
    "",
    "Execution hints:",
    `- Driver: ${resolvedDriver}`,
    `- Provider: ${firstNonEmpty(provider, "auto")}`,
    `- Model: ${firstNonEmpty(model, "project/runtime default")}`,
    `- Agent preset: ${firstNonEmpty(resolvedPreset, "none")}`,
    `- Role defaults: ${formatHintList([
      ["driver", role?.default_driver_kind],
      ["provider", role?.default_provider],
      ["model", role?.default_model],
      ["preset", role?.default_agent_profile],
    ])}`,
    `- Project defaults: ${formatHintList([
      ["provider", project.default_provider],
      ["model", project.default_model],
      ["preset", project.default_agent_profile],
      ["workspace_mode", project.default_workspace_mode],
    ])}`,
  ];
  const linkedIDs = formatHintList([
    ["task", execution.taskID],
    ["run", execution.runID],
    ["chat", execution.chatSessionID],
    ["message", execution.messageID],
    ["context", execution.contextSnapshotID],
  ]);
  if (linkedIDs !== "none") {
    lines.push("", "Linked runtime ids:", `- ${linkedIDs}`);
  }
  lines.push("", "Request:", "- ");
  return lines.join("\n");
}

function labelWithID(label: string | undefined, id: string): string {
  const cleanLabel = label?.trim();
  const cleanID = id.trim();
  if (cleanLabel && cleanID) return `${cleanLabel} (${cleanID})`;
  return cleanLabel || cleanID;
}

function formatHintList(items: Array<[string, string | undefined | null]>): string {
  const parts = items
    .map(([label, value]) => {
      const trimmed = value?.trim();
      return trimmed ? `${label}=${trimmed}` : "";
    })
    .filter(Boolean);
  return parts.length > 0 ? parts.join(", ") : "none";
}

function formatLaunchContextBullet(label: string, value: string): string {
  const lines = value.replaceAll("\r\n", "\n").replaceAll("\r", "\n").split("\n");
  const [firstLine = "", ...rest] = lines;
  const continuation = rest.map((line) => `  ${line}`).join("\n");
  return continuation ? `- ${label}: ${firstLine}\n${continuation}` : `- ${label}: ${firstLine}`;
}

function assignmentLaunchPostureRows(
  readiness: ProjectAssignmentLaunchReadinessRecord,
): Array<{ label: string; value: string }> {
  const rows = [
    {
      label: "Driver",
      value: assignmentLaunchDriverLabel(readiness.driver_kind),
    },
    {
      label: "Workspace",
      value: firstNonEmpty(readiness.workspace, "No workspace resolved"),
    },
  ];
  const root = labelWithID(readiness.root_path, readiness.root_id ?? "");
  if (root) rows.push({ label: "Root", value: root });
  if (readiness.provider || readiness.model) {
    rows.push({
      label: "Provider/model",
      value: `${firstNonEmpty(readiness.provider, "auto")} / ${firstNonEmpty(readiness.model, "auto")}`,
    });
  }
  const preset = assignmentLaunchPresetPosture(readiness);
  if (preset) {
    rows.push({ label: "Preset", value: preset });
  }
  const capabilities = assignmentLaunchCapabilityPosture(readiness);
  if (capabilities) {
    rows.push({ label: "Capabilities", value: capabilities });
  }
  if (
    readiness.driver_kind === "external_agent" ||
    readiness.external_agent ||
    readiness.external_agent_id
  ) {
    rows.push({
      label: "External Agent",
      value: firstNonEmpty(
        labelWithID(readiness.external_agent, readiness.external_agent_id ?? ""),
        "Unresolved External Agent",
      ),
    });
  }
  if (readiness.session_title) {
    rows.push({ label: "Session", value: readiness.session_title });
  }
  return rows;
}

function assignmentLaunchDriverLabel(kind: string): string {
  switch (kind) {
    case "hecate_task":
      return "Hecate task";
    case "external_agent":
      return "External Agent";
    default:
      return kind || "unknown";
  }
}

function assignmentLaunchPresetPosture(readiness: ProjectAssignmentLaunchReadinessRecord): string {
  const posture = readiness.profile_posture;
  const preset = firstNonEmpty(
    readiness.execution_profile,
    labelWithID(posture?.name, posture?.id ?? ""),
  );
  if (!preset) return "";
  return posture?.missing ? `${preset} (preset missing)` : preset;
}

function assignmentLaunchCapabilityPosture(
  readiness: ProjectAssignmentLaunchReadinessRecord,
): string {
  const posture = readiness.profile_posture;
  if (!posture) return "";
  return [
    `tools ${posture.tools_enabled ? "on" : "off"}`,
    `writes ${posture.writes_allowed ? "on" : "off"}`,
    `network ${posture.network_allowed ? "on" : "off"}`,
  ].join(" · ");
}

function assignmentLaunchReadinessNotice(
  readiness: ProjectAssignmentLaunchReadinessRecord,
): AssignmentLaunchReadinessNoticeRecord | null {
  if (readiness.ready) return null;
  const modelReadiness = readiness.model_readiness;
  const title =
    modelReadiness?.ready === false
      ? "Provider/model not ready"
      : readiness.title || "Launch is blocked";
  const modelDetail = [modelReadiness?.message, modelReadiness?.operator_action]
    .filter(Boolean)
    .join(" ");
  return {
    title,
    detail:
      modelDetail ||
      readiness.detail ||
      modelReadiness?.reason ||
      "Resolve launch readiness blockers before starting this assignment.",
    blockers: readiness.blockers ?? [],
    warnings: readiness.warnings ?? [],
  };
}

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

const launchPosturePanelStyle: CSSProperties = {
  background: "var(--bg2)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 10,
  padding: "10px 12px",
};

const launchPostureGridStyle: CSSProperties = {
  display: "grid",
  gap: 8,
  gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))",
};

const launchPostureItemStyle: CSSProperties = {
  display: "grid",
  gap: 3,
  minWidth: 0,
};

const launchPostureLabelStyle: CSSProperties = {
  color: "var(--t3)",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  textTransform: "uppercase",
};

const launchPostureValueStyle: CSSProperties = {
  color: "var(--t1)",
  fontSize: 12,
  lineHeight: 1.35,
  overflowWrap: "anywhere",
};

const metaLineStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  color: "var(--t3)",
  fontSize: 11,
  marginTop: 6,
};

const workItemDetailStyle: CSSProperties = {
  boxSizing: "border-box",
  maxWidth: "100%",
  minWidth: 0,
  overflow: "hidden",
  width: "100%",
};

const workItemCardStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  boxSizing: "border-box",
  display: "grid",
  gap: 0,
  maxWidth: "100%",
  minWidth: 0,
  overflow: "hidden",
  padding: "14px 14px 12px",
};

const workItemFocusNoticeStyle: CSSProperties = {
  alignItems: "center",
  background: "color-mix(in srgb, var(--teal) 7%, var(--bg1))",
  border: "1px solid color-mix(in srgb, var(--teal) 24%, var(--border))",
  borderRadius: "var(--radius-sm)",
  color: "var(--t1)",
  display: "flex",
  flexWrap: "wrap",
  fontSize: 12,
  gap: 8,
  lineHeight: 1.45,
  marginTop: 10,
  padding: "8px 10px",
};

const workItemDetailHeaderStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "grid",
  gap: 12,
  gridTemplateColumns: "minmax(0, 1fr) auto",
  minWidth: 0,
};

const workItemTitleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 18,
  lineHeight: 1.2,
  margin: 0,
  minWidth: 0,
  overflowWrap: "anywhere",
};

const workItemMetaStyle: CSSProperties = {
  alignItems: "center",
  color: "var(--t3)",
  display: "flex",
  flexWrap: "wrap",
  fontSize: 11,
  gap: 7,
  marginTop: 10,
  minWidth: 0,
};

const workItemHeaderActionsStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  justifyContent: "flex-end",
  minWidth: 0,
};

const workItemBriefSectionStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  marginTop: 14,
  minWidth: 0,
  paddingTop: 12,
};

const workItemBriefTextStyle: CSSProperties = {
  color: "var(--t1)",
  fontSize: 13,
  lineHeight: 1.5,
  margin: "8px 0 0",
  maxWidth: 860,
  overflowWrap: "anywhere",
};

const reviewerSetupNoticeStyle: CSSProperties = {
  alignItems: "center",
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "flex",
  flexWrap: "wrap",
  gap: 10,
  justifyContent: "space-between",
  marginTop: 10,
  minWidth: 0,
  padding: "9px 10px",
};

const workItemCardSectionStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  marginTop: 12,
  minWidth: 0,
  paddingTop: 12,
};

const startPanelStyle: CSSProperties = {
  alignItems: "center",
  borderTop: "1px solid var(--border)",
  display: "flex",
  flexWrap: "wrap",
  gap: 12,
  justifyContent: "space-between",
  marginTop: 12,
  minWidth: 0,
  paddingTop: 14,
};

const startPanelCopyStyle: CSSProperties = {
  flex: "1 1 320px",
  minWidth: 0,
};

const startPanelActionsStyle: CSSProperties = {
  alignItems: "center",
  display: "grid",
  gap: 8,
  justifyItems: "end",
  minWidth: 0,
};

const startPanelSecondaryStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 6,
  justifyContent: "flex-end",
  minWidth: 0,
};

const manualAddDetailsStyle: CSSProperties = {
  color: "var(--t2)",
  justifySelf: "end",
  minWidth: 0,
};

const manualAddSummaryStyle: CSSProperties = {
  color: "var(--t3)",
  cursor: "pointer",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  textTransform: "uppercase",
};

const addActionsPanelStyle: CSSProperties = {
  alignItems: "center",
  borderTop: "1px solid var(--border)",
  display: "flex",
  flexWrap: "wrap",
  gap: 10,
  justifyContent: "space-between",
  marginTop: 12,
  minWidth: 0,
  paddingTop: 12,
};

const addActionsCopyStyle: CSSProperties = {
  flex: "1 1 260px",
  minWidth: 0,
};

const addActionsButtonsStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 6,
  justifyContent: "flex-end",
  minWidth: 0,
};

const closeoutListStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 12,
  lineHeight: 1.45,
  margin: "8px 0 0",
  paddingLeft: 18,
};

const closeoutDetailsStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  marginTop: 10,
  paddingTop: 9,
};

const closeoutConfirmationSummaryStyle: CSSProperties = {
  display: "grid",
  gap: 8,
  gridTemplateColumns: "repeat(3, minmax(0, 1fr))",
};

const closeoutBlockedNoticeStyle: CSSProperties = {
  background: "rgba(245, 158, 11, 0.08)",
  border: "1px solid rgba(245, 158, 11, 0.35)",
  borderRadius: "var(--radius-sm)",
  padding: 10,
};

const reviewFollowUpNoticeStyle: CSSProperties = {
  background: "rgba(245, 158, 11, 0.08)",
  border: "1px solid rgba(245, 158, 11, 0.35)",
  borderRadius: "var(--radius-sm)",
  marginTop: 12,
  minWidth: 0,
  padding: 12,
};

const evidenceArtifactMetadataStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  marginTop: 8,
  color: "var(--t3)",
  fontSize: 12,
  lineHeight: 1.4,
  overflowWrap: "anywhere",
};

const workItemSectionHeaderStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  marginBottom: 10,
  minWidth: 0,
};

const launchReadinessPreviewStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 8,
  marginTop: 9,
  minWidth: 0,
  padding: "9px 10px",
};

const launchReadinessPreviewHeaderStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  minWidth: 0,
};

const launchReadinessPreviewGridStyle: CSSProperties = {
  display: "grid",
  gap: "7px 10px",
  gridTemplateColumns: "repeat(auto-fit, minmax(135px, 1fr))",
  minWidth: 0,
};

const launchReadinessPreviewItemStyle: CSSProperties = {
  display: "grid",
  gap: 2,
  minWidth: 0,
};

const launchReadinessPreviewValueStyle: CSSProperties = {
  color: "var(--t1)",
  fontSize: 12,
  lineHeight: 1.35,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const launchReadinessBlockerStyle: CSSProperties = {
  color: "var(--amber)",
  fontSize: 12,
  lineHeight: 1.4,
  overflowWrap: "anywhere",
};

const launchWarningNoticeStyle: CSSProperties = {
  background: "var(--amber-bg)",
  border: "1px solid var(--amber-border)",
  borderRadius: "var(--radius-sm)",
  color: "var(--amber-lo)",
  display: "grid",
  fontSize: 12,
  gap: 6,
  lineHeight: 1.45,
  padding: "10px 12px",
};

const launchWarningNoticeTitleStyle: CSSProperties = {
  alignItems: "center",
  color: "var(--amber)",
  display: "flex",
  fontWeight: 600,
  gap: 8,
};

const assignmentEvidenceStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 8,
  marginTop: 9,
  minWidth: 0,
  padding: "9px 10px",
};

const assignmentEvidenceGridStyle: CSSProperties = {
  display: "grid",
  gap: "8px 12px",
  gridTemplateColumns: "repeat(auto-fit, minmax(150px, 1fr))",
  minWidth: 0,
};

const assignmentEvidenceCellStyle: CSSProperties = {
  minWidth: 0,
};

const assignmentEvidenceLabelStyle: CSSProperties = {
  color: "var(--t3)",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  lineHeight: 1.4,
  textTransform: "uppercase",
};

const assignmentEvidenceValueStyle: CSSProperties = {
  color: "var(--t1)",
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  lineHeight: 1.45,
  overflowWrap: "anywhere",
};

const assignmentEvidenceWarningsStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 6,
  minWidth: 0,
};

const artifactStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  boxSizing: "border-box",
  maxWidth: "100%",
  minWidth: 0,
  overflowWrap: "anywhere",
  paddingTop: 8,
  width: "100%",
};

const artifactHeaderStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  minWidth: 0,
};

const artifactIdentityStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flex: "1 1 220px",
  flexWrap: "wrap",
  gap: 6,
  minWidth: 0,
};

const artifactTitleStyle: CSSProperties = {
  ...titleStyle,
  flex: "1 1 150px",
  minWidth: 0,
  overflow: "visible",
  overflowWrap: "anywhere",
  textOverflow: "clip",
  whiteSpace: "normal",
};

const artifactActionsStyle: CSSProperties = {
  display: "flex",
  flex: "0 1 auto",
  flexWrap: "wrap",
  gap: 6,
  maxWidth: "100%",
};

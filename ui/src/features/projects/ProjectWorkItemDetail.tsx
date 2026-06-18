import { useCallback, useEffect, useState, type CSSProperties } from "react";

import { getProjectAssignmentContext, getProjectAssignmentPreflight } from "../../lib/api";
import { formatAbsoluteTime } from "../../lib/format";
import type { ContextPacketRecord } from "../../types/context";
import type {
  ProjectActivityItemRecord,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectHandoffRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { ContextInspectorModalTrigger, ContextInspectorPanel } from "../shared/ContextInspector";
import { Badge, CopyableID, Icon, Icons, InlineError, Modal } from "../shared/ui";
import {
  toProjectActivityItemViewModel,
  toProjectAssignmentEvidenceViewModel,
  toProjectAssignmentExecutionViewModel,
  type ProjectAssignmentEvidenceViewModel,
} from "./projectAssignmentViewModels";
import {
  buildProjectWorkCloseoutReadiness,
  type ProjectWorkCloseoutReadiness,
} from "./projectInsights";
import {
  assignmentStatusLabel,
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
  | { status: "ready"; packet: ContextPacketRecord }
  | { status: "error"; detail: string };

type AssignmentLaunchReadinessNoticeRecord = {
  detail: string;
};

type AssignmentLaunchRepairActions = {
  onManageProfiles?: () => void;
  onManageRoles?: () => void;
  onOpenConnections?: () => void;
  onOpenProjectSettings?: () => void;
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
  preparingAssignmentID: string;
  loading: boolean;
  onAddAssignment: () => void;
  onAddHandoff: () => void;
  onAddEvidenceLink: () => void;
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
  onCreateAssignmentFromReviewArtifact: (artifact: ProjectCollaborationArtifactRecord) => void;
  onCreateAssignmentFromHandoff: (handoff: ProjectHandoffRecord) => void;
  onDeleteAssignment: (assignment: ProjectAssignmentRecord) => void;
  onDeleteHandoff: (handoff: ProjectHandoffRecord) => void;
  onDeleteWorkItem: (item: ProjectWorkItemRecord) => void;
  onCloseWorkItem: (item: ProjectWorkItemRecord) => void;
  onEditAssignment: (assignment: ProjectAssignmentRecord) => void;
  onEditHandoff: (handoff: ProjectHandoffRecord) => void;
  onEditWorkItem: (item: ProjectWorkItemRecord) => void;
  onManageProfiles: () => void;
  onManageRoles: () => void;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenConnections?: () => void;
  onOpenSettings: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onRefresh: () => void;
  onStartAssignment: (assignment: ProjectAssignmentRecord) => void;
  onStartHandoff: (handoff: ProjectHandoffRecord) => void;
  onSetHandoffStatus: (handoff: ProjectHandoffRecord, status: string) => void;
  project: ProjectRecord | null;
  roleByID: Map<string, ProjectWorkRoleRecord>;
  closingWorkItemID: string;
  startingAssignmentID: string;
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
  preparingAssignmentID,
  loading,
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
  onEditAssignment,
  onEditHandoff,
  onEditWorkItem,
  onManageProfiles,
  onManageRoles,
  onOpenChat,
  onOpenConnections,
  onOpenSettings,
  onOpenTask,
  onRefresh,
  onStartAssignment,
  onStartHandoff,
  onSetHandoffStatus,
  project,
  roleByID,
  closingWorkItemID,
  startingAssignmentID,
  workItem,
}: ProjectWorkItemDetailProps) {
  if (!workItem) {
    return (
      <EmptyBlock
        title={loading ? "Loading detail…" : "Select a work item"}
        detail="Assignments and collaboration artifacts appear here."
      />
    );
  }
  const closeout = buildProjectWorkCloseoutReadiness({
    assignments,
    artifacts,
    handoffs,
    workItem,
  });
  const emptyWorkItem =
    assignments.length === 0 &&
    artifacts.length === 0 &&
    handoffs.length === 0 &&
    workItem.status !== "done";
  const suggestedAssignmentRole = assignmentRoleForWorkItem(workItem, roleByID);
  return (
    <div style={workItemDetailStyle}>
      <article style={workItemCardStyle} aria-label={`${workItem.title} work item`}>
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
            <button className="btn btn-ghost btn-sm" type="button" onClick={onRefresh}>
              <Icon d={Icons.refresh} size={13} />
              Refresh
            </button>
          </div>
        </div>
        {detailError && <InlineError message={detailError} />}
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
          {!emptyWorkItem && (
            <ReviewerSetupNotice
              onEditWorkItem={() => onEditWorkItem(workItem)}
              onManageRoles={onManageRoles}
              roleByID={roleByID}
              workItem={workItem}
            />
          )}
        </section>
        {emptyWorkItem ? (
          <WorkItemStartPanel
            drafting={draftingDefaultAssignment}
            onAddAssignment={onAddAssignment}
            onAddEvidenceLink={onAddEvidenceLink}
            onAddHandoff={onAddHandoff}
            onDraftDefaultAssignment={() => onDraftDefaultAssignment(workItem)}
            onManageRoles={onManageRoles}
            role={suggestedAssignmentRole}
          />
        ) : (
          <WorkItemCloseoutPanel
            closeout={closeout}
            pending={closingWorkItemID === workItem.id}
            onClose={() => onCloseWorkItem(workItem)}
          />
        )}
        {(!emptyWorkItem || assignments.length > 0) && (
          <section style={workItemCardSectionStyle}>
            <div style={workItemSectionHeaderStyle}>
              <div style={sectionLabelStyle}>Assignments</div>
              <span className="badge badge-muted">{assignments.length}</span>
              <button
                className="btn btn-primary btn-sm"
                type="button"
                onClick={onAddAssignment}
                style={{ marginLeft: "auto" }}
              >
                <Icon d={Icons.plus} size={12} />
                Assignment
              </button>
            </div>
            {assignments.length === 0 ? (
              <div style={subtleTextStyle}>No assignments recorded yet.</div>
            ) : (
              <div style={{ display: "grid", gap: 10 }}>
                {assignments.map((assignment) => {
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
                      activityItem={activityItem}
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
                              onOpenChat?.(
                                executionRef.chatSessionID
                                  ? {
                                      projectID: project.id,
                                      chatSessionID: executionRef.chatSessionID,
                                    }
                                  : buildProjectAssignmentChatLaunchRequest({
                                      project,
                                      workItem,
                                      assignment,
                                      role: roleByID.get(assignment.role_id) ?? null,
                                    }),
                              );
                            }
                          : undefined
                      }
                      onOpenTask={onOpenTask}
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
                      repairActions={{
                        onManageProfiles,
                        onManageRoles,
                        onOpenConnections,
                        onOpenProjectSettings: onOpenSettings,
                      }}
                      role={roleByID.get(assignment.role_id)}
                      starting={startingAssignmentID === assignment.id}
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
                    />
                  );
                })}
              </div>
            )}
          </section>
        )}
        {(!emptyWorkItem || artifacts.length > 0) && (
          <section style={workItemCardSectionStyle}>
            <div style={workItemSectionHeaderStyle}>
              <div style={sectionLabelStyle}>Collaboration Artifacts</div>
              <span className="badge badge-muted">{artifacts.length}</span>
              <button
                className="btn btn-primary btn-sm"
                type="button"
                onClick={onAddEvidenceLink}
                style={{ marginLeft: "auto" }}
              >
                <Icon d={Icons.plus} size={12} />
                Evidence
              </button>
            </div>
            {artifacts.length === 0 ? (
              <div style={subtleTextStyle}>No collaboration artifacts recorded yet.</div>
            ) : (
              <div style={{ display: "grid", gap: 8 }}>
                {artifacts.map((artifact) => {
                  const artifactActionPending = artifactActionID === artifact.id;
                  return (
                    <div key={artifact.id} style={artifactStyle}>
                      <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
                        <span className="badge badge-muted">{artifact.kind}</span>
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
                          <span className="badge badge-muted">{artifact.evidence_source_kind}</span>
                        )}
                        {artifact.kind === "evidence_link" && artifact.evidence_trust_label && (
                          <span className="badge badge-muted">{artifact.evidence_trust_label}</span>
                        )}
                        <span style={{ ...titleStyle, flex: 1, minWidth: 0 }}>
                          {artifact.title || artifact.id}
                        </span>
                        {artifact.kind === "review" && (
                          <>
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
                              aria-label={`Create follow-up assignment from review artifact ${artifact.id}`}
                              className="btn btn-ghost btn-sm"
                              type="button"
                              onClick={() => onCreateAssignmentFromReviewArtifact(artifact)}
                              disabled={artifactActionPending}
                              title="Create a handoff and queued follow-up assignment from this review."
                            >
                              <Icon d={Icons.tasks} size={12} />
                              Assignment
                            </button>
                          </>
                        )}
                      </div>
                      <div
                        style={{ marginTop: 6, fontSize: 12, color: "var(--t2)", lineHeight: 1.45 }}
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
        {(!emptyWorkItem || handoffs.length > 0) && (
          <section style={workItemCardSectionStyle}>
            <div style={workItemSectionHeaderStyle}>
              <div style={sectionLabelStyle}>Handoffs</div>
              <span className="badge badge-muted">{handoffs.length}</span>
              <button
                className="btn btn-primary btn-sm"
                type="button"
                onClick={onAddHandoff}
                style={{ marginLeft: "auto" }}
              >
                <Icon d={Icons.plus} size={12} />
                Handoff
              </button>
            </div>
            {handoffError && <InlineError message={handoffError} />}
            {handoffs.length === 0 ? (
              <div style={subtleTextStyle}>No structured handoffs recorded yet.</div>
            ) : (
              <div style={{ display: "grid", gap: 8 }}>
                {handoffs.map((handoff) => {
                  const targetAssignment = assignments.find(
                    (item) => item.id === handoff.target_assignment_id,
                  );
                  return (
                    <ProjectHandoffRow
                      key={handoff.id}
                      actionPending={handoffActionID === handoff.id}
                      assignment={targetAssignment}
                      handoff={handoff}
                      onCreateAssignment={() => onCreateAssignmentFromHandoff(handoff)}
                      onDelete={() => onDeleteHandoff(handoff)}
                      onEdit={() => onEditHandoff(handoff)}
                      onSetStatus={(status) => onSetHandoffStatus(handoff, status)}
                      onStart={() => onStartHandoff(handoff)}
                      repairActions={{
                        onManageProfiles,
                        onManageRoles,
                        onOpenConnections,
                        onOpenProjectSettings: onOpenSettings,
                      }}
                      role={
                        handoff.target_role_id ? roleByID.get(handoff.target_role_id) : undefined
                      }
                      starting={startingAssignmentID === handoff.target_assignment_id}
                      loadPreflight={
                        project && targetAssignment
                          ? async () =>
                              (
                                await getProjectAssignmentPreflight(
                                  project.id,
                                  workItem.id,
                                  targetAssignment.id,
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
      </article>
    </div>
  );
}

function WorkItemStartPanel({
  drafting,
  onAddAssignment,
  onAddEvidenceLink,
  onAddHandoff,
  onDraftDefaultAssignment,
  onManageRoles,
  role,
}: {
  drafting: boolean;
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
          {role ? "Ready to queue the first assignment" : "Add a role before assigning work"}
        </div>
        <div style={{ ...subtleTextStyle, marginTop: 5 }}>
          {role
            ? `Hecate can draft a reviewable ${role.name || role.id} assignment proposal from this work item. You will review and apply it before anything is created.`
            : "Create or select a project role so Hecate can prepare this work from defaults."}
        </div>
      </div>
      <div style={startPanelActionsStyle}>
        {role ? (
          <button
            className="btn btn-primary btn-sm"
            type="button"
            onClick={onDraftDefaultAssignment}
            disabled={drafting}
          >
            <Icon d={Icons.tasks} size={13} />
            {drafting ? "Drafting..." : "Draft first assignment"}
          </button>
        ) : (
          <button className="btn btn-primary btn-sm" type="button" onClick={onManageRoles}>
            <Icon d={Icons.user} size={13} />
            Manage roles
          </button>
        )}
        <div aria-label="Manual work item actions" role="group" style={startPanelSecondaryStyle}>
          <span style={startPanelSecondaryLabelStyle}>Manual options</span>
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

function WorkItemCloseoutPanel({
  closeout,
  onClose,
  pending,
}: {
  closeout: ProjectWorkCloseoutReadiness;
  onClose: () => void;
  pending: boolean;
}) {
  const status =
    closeout.status === "ready" || closeout.status === "done" ? "completed" : "blocked";
  return (
    <section style={workItemCardSectionStyle} aria-label="Work closeout">
      <div style={workItemSectionHeaderStyle}>
        <div style={sectionLabelStyle}>Closeout</div>
        <Badge status={status} label={closeout.status === "done" ? "done" : closeout.status} />
        <span className="badge badge-muted">
          {closeout.completedAssignments}/{closeout.assignmentCount} assignments complete
        </span>
        {closeout.status !== "done" && (
          <button
            className="btn btn-primary btn-sm"
            type="button"
            onClick={onClose}
            disabled={!closeout.ready || pending}
            style={{ marginLeft: "auto" }}
          >
            <Icon d={Icons.check} size={12} />
            {pending ? "Marking..." : "Mark done"}
          </button>
        )}
      </div>
      <div style={titleStyle}>{closeout.title}</div>
      <div style={{ ...subtleTextStyle, marginTop: 4 }}>{closeout.detail}</div>
      {closeout.blockers.length > 0 && (
        <ul style={closeoutListStyle}>
          {closeout.blockers.map((blocker) => (
            <li key={blocker}>{blocker}</li>
          ))}
        </ul>
      )}
      {closeout.warnings.length > 0 && (
        <div style={{ ...subtleTextStyle, marginTop: 8 }}>
          {closeout.warnings.map((warning) => (
            <div key={warning}>{warning}</div>
          ))}
        </div>
      )}
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
  activityItem,
  assignment,
  autoOpenPreflight,
  chatModel,
  error,
  loadContext,
  loadPreflight,
  onCreateHandoff,
  onCreateReviewHandoff,
  onCreateReviewArtifact,
  onAutoOpenPreflightHandled,
  onDelete,
  onEdit,
  onOpenChat,
  onOpenTask,
  onStart,
  project,
  repairActions,
  role,
  starting,
}: {
  activityItem?: ProjectActivityItemRecord;
  assignment: ProjectAssignmentRecord;
  autoOpenPreflight?: boolean;
  chatModel: string;
  error: string;
  loadContext?: (() => Promise<ContextPacketRecord>) | null;
  loadPreflight?: (() => Promise<ContextPacketRecord>) | null;
  onCreateHandoff: () => void;
  onCreateReviewHandoff?: () => void;
  onCreateReviewArtifact?: () => void;
  onAutoOpenPreflightHandled?: (assignmentID: string) => void;
  onDelete: () => void;
  onEdit: () => void;
  onOpenChat?: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onStart: () => void;
  project: ProjectRecord | null;
  repairActions?: AssignmentLaunchRepairActions;
  role?: ProjectWorkRoleRecord;
  starting: boolean;
}) {
  const [preflightOpen, setPreflightOpen] = useState(false);
  const [preflightState, setPreflightState] = useState<AssignmentPreflightState>({
    status: "idle",
  });
  const execution = assignment.execution;
  const assignmentExecution = toProjectAssignmentExecutionViewModel(assignment);
  const activityView = activityItem ? toProjectActivityItemViewModel(activityItem) : null;
  const evidence = toProjectAssignmentEvidenceViewModel(assignment, activityItem);
  const executionRef = assignmentExecution.hasAnyLink
    ? assignmentExecution
    : (activityView?.execution ?? assignmentExecution);
  const taskID = executionRef.taskID;
  const runID = executionRef.runID;
  const chatSessionID = executionRef.chatSessionID;
  const linkedChat = activityItem?.linked_chat;
  const projectedStatus = assignmentExecution.status;
  const startable =
    (assignment.driver_kind === "hecate_task" || assignment.driver_kind === "external_agent") &&
    projectedStatus === "queued";
  const external = assignment.driver_kind === "external_agent";
  const startActionLabel = external ? "Prepare chat" : "Start";
  const startingLabel = external ? "Preparing…" : "Starting…";
  const startedAt = activityView?.startedAt || execution?.started_at || assignment.started_at;
  const finishedAt = activityView?.finishedAt || execution?.finished_at || assignment.completed_at;

  const openPreflight = useCallback(async () => {
    if (!loadPreflight) {
      onStart();
      return;
    }
    setPreflightOpen(true);
    setPreflightState({ status: "loading" });
    try {
      const packet = await loadPreflight();
      setPreflightState({ status: "ready", packet });
    } catch (error) {
      setPreflightState({
        status: "error",
        detail: projectErrorMessage(error, "Failed to load assignment launch preflight."),
      });
    }
  }, [loadPreflight, onStart]);

  useEffect(() => {
    if (!autoOpenPreflight) return;
    onAutoOpenPreflightHandled?.(assignment.id);
    if (!startable) return;
    if (!loadPreflight) return;
    void openPreflight();
  }, [
    assignment.id,
    autoOpenPreflight,
    loadPreflight,
    onAutoOpenPreflightHandled,
    openPreflight,
    startable,
  ]);

  return (
    <div style={assignmentStyle}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <Badge status={projectedStatus} label={assignmentStatusLabel(projectedStatus)} />
        <span className="badge badge-muted">{assignment.driver_kind}</span>
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>
          {role?.name ?? assignment.role_id}
        </div>
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          aria-label={`Edit assignment ${assignment.id}`}
          onClick={onEdit}
          title="Edit"
        >
          <Icon d={Icons.edit} size={12} />
        </button>
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          aria-label={`Delete assignment ${assignment.id}`}
          onClick={onDelete}
          title="Delete"
          style={{ color: "var(--red)" }}
        >
          <Icon d={Icons.trash} size={12} />
        </button>
        {startable && (
          <button
            className="btn btn-primary btn-sm"
            type="button"
            onClick={() => void openPreflight()}
            disabled={starting}
            title={
              external
                ? "Review launch context before preparing a linked External Agent chat."
                : "Review launch context before starting this assignment."
            }
          >
            <Icon d={external ? Icons.chat : Icons.send} size={12} />
            {starting ? startingLabel : startActionLabel}
          </button>
        )}
        {external && !startable && !executionRef.chatSessionID && (
          <span
            style={subtleTextStyle}
            title="Prepare this assignment to create a supervised External Agent chat session."
          >
            Chat not prepared
          </span>
        )}
      </div>
      <div
        style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 8, alignItems: "center" }}
      >
        {taskID && (
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={() => onOpenTask?.(taskID, runID)}
            disabled={!onOpenTask}
          >
            <Icon d={Icons.tasks} size={12} />
            Open task
          </button>
        )}
        <ContextInspectorModalTrigger
          buttonLabel="Inspect context"
          buttonTitle="Inspect the best available stored context snapshot for this assignment."
          loadPacket={loadContext}
          modalTitle={`Assignment ${assignment.id} context`}
          resourceKey={assignment.id}
          unavailableDetail="This assignment does not have a stored context packet yet. Unstarted assignments, legacy rows, and older linked runs can legitimately return no snapshot."
        />
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          onClick={onOpenChat}
          disabled={!onOpenChat || (!chatSessionID && !chatModel)}
          title={
            chatSessionID
              ? external
                ? "Open the prepared External Agent chat. The first prompt is sent from Chats."
                : "Open linked External Agent chat"
              : chatModel
                ? `Open chat with ${chatModel}`
                : "Set project defaults before opening chat."
          }
        >
          <Icon d={Icons.chat} size={12} />
          Open chat
        </button>
        {taskID && <CopyableID text={taskID} compact />}
        {runID && <CopyableID text={runID} compact />}
        {chatSessionID && <CopyableID text={chatSessionID} compact />}
        {linkedChat && (
          <span
            className={linkedChat.missing ? "badge badge-amber" : "badge badge-muted"}
            title={linkedChatStatusTitle(linkedChat)}
          >
            {linkedChat.missing
              ? "linked chat missing"
              : `chat ${linkedChat.latest_status || linkedChat.status || "active"}`}
          </span>
        )}
        {executionRef.pendingApprovalCount ? (
          <span className="badge badge-amber">
            {executionRef.pendingApprovalCount} approval pending
          </span>
        ) : null}
        {typeof execution?.step_count === "number" && (
          <span className="badge badge-muted">{execution.step_count} steps</span>
        )}
        {typeof execution?.artifact_count === "number" && (
          <span className="badge badge-muted">{execution.artifact_count} artifacts</span>
        )}
        {execution?.provider || execution?.model ? (
          <span className="badge badge-muted">
            {[execution.provider, execution.model].filter(Boolean).join(" / ")}
          </span>
        ) : null}
        {assignment.root_id && project && (
          <span className="badge badge-muted" title={projectRootTitle(project, assignment.root_id)}>
            root {projectRootDisplayLabel(project, assignment.root_id)}
          </span>
        )}
        {executionRef.missing && <span className="badge badge-amber">linked run missing</span>}
        {executionRef.hasAnyLink && (
          <button
            aria-label={`Create handoff from assignment ${assignment.id}`}
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={onCreateHandoff}
          >
            <Icon d={Icons.plus} size={12} />
            Handoff
          </button>
        )}
        {executionRef.hasAnyLink && onCreateReviewHandoff && (
          <button
            aria-label={`Request review for assignment ${assignment.id}`}
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={onCreateReviewHandoff}
          >
            <Icon d={Icons.check} size={12} />
            Request review
          </button>
        )}
        {onCreateReviewArtifact && (
          <button
            aria-label={`Record review for assignment ${assignment.id}`}
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={onCreateReviewArtifact}
          >
            <Icon d={Icons.check} size={12} />
            Record review
          </button>
        )}
      </div>
      {evidence.hasEvidence && <ProjectAssignmentEvidence evidence={evidence} />}
      {activityView?.statusSummary &&
        activityView.statusSummary !== projectedStatus &&
        activityView.statusSummary !== "linked run missing" && (
          <div style={{ ...subtleTextStyle, marginTop: 8 }}>{activityView.statusSummary}</div>
        )}
      {(startedAt || finishedAt) && (
        <div style={{ ...metaLineStyle, marginTop: 8 }}>
          {startedAt && <span>Started {formatAbsoluteTime(startedAt)}</span>}
          {finishedAt && <span>Finished {formatAbsoluteTime(finishedAt)}</span>}
        </div>
      )}
      {execution?.last_error && (
        <div style={{ marginTop: 8 }}>
          <InlineError message={execution.last_error} />
        </div>
      )}
      {error && (
        <div style={{ marginTop: 8 }}>
          <InlineError message={error} />
        </div>
      )}
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
    </div>
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
  const readinessNotice = ready ? assignmentLaunchReadinessNotice(state.packet) : null;
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
      title={`Assignment ${assignmentID} launch preflight`}
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
                ? "Fix the provider/model readiness issue before starting this assignment."
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
            <div style={titleStyle}>Loading launch preflight...</div>
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
    { key: "roles", label: "Manage roles", onClick: repairActions?.onManageRoles },
    { key: "profiles", label: "Agent profiles", onClick: repairActions?.onManageProfiles },
    { key: "connections", label: "Open Connections", onClick: repairActions?.onOpenConnections },
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
          Provider/model not ready
        </div>
        <div style={{ color: "var(--amber-lo)", fontSize: 12, lineHeight: 1.45 }}>
          {notice.detail}
        </div>
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
  handoff,
  loadPreflight,
  onCreateAssignment,
  onDelete,
  onEdit,
  onSetStatus,
  onStart,
  repairActions,
  role,
  starting,
}: {
  actionPending: boolean;
  assignment?: ProjectAssignmentRecord;
  handoff: ProjectHandoffRecord;
  loadPreflight?: (() => Promise<ContextPacketRecord>) | null;
  onCreateAssignment: () => void;
  onDelete: () => void;
  onEdit: () => void;
  onSetStatus: (status: string) => void;
  onStart: () => void;
  repairActions?: AssignmentLaunchRepairActions;
  role?: ProjectWorkRoleRecord;
  starting: boolean;
}) {
  const [preflightOpen, setPreflightOpen] = useState(false);
  const [preflightState, setPreflightState] = useState<AssignmentPreflightState>({
    status: "idle",
  });
  const executionRef = assignment ? toProjectAssignmentExecutionViewModel(assignment) : null;
  const targetEvidence = assignment ? toProjectAssignmentEvidenceViewModel(assignment) : null;
  const startable =
    (assignment?.driver_kind === "hecate_task" || assignment?.driver_kind === "external_agent") &&
    executionRef?.status === "queued";
  const external = assignment?.driver_kind === "external_agent";
  const canCreateAssignment = !assignment && handoff.status !== "dismissed";
  const sourceRefs = handoffSourceRefs(handoff);

  async function openPreflight() {
    if (!loadPreflight) {
      onStart();
      return;
    }
    setPreflightOpen(true);
    setPreflightState({ status: "loading" });
    try {
      const packet = await loadPreflight();
      setPreflightState({ status: "ready", packet });
    } catch (error) {
      setPreflightState({
        status: "error",
        detail: projectErrorMessage(error, "Failed to load assignment launch preflight."),
      });
    }
  }

  return (
    <>
      <div style={artifactStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <Badge status={handoff.status} label={handoffStatusLabel(handoff.status)} />
          <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{handoff.title}</div>
          <button className="btn btn-ghost btn-sm" type="button" onClick={onEdit}>
            <Icon d={Icons.edit} size={12} />
            Edit
          </button>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={onDelete}
            disabled={actionPending}
            style={{ color: "var(--red)" }}
          >
            <Icon d={Icons.trash} size={12} />
            Delete
          </button>
        </div>
        <div style={{ marginTop: 7, fontSize: 12, color: "var(--t2)", lineHeight: 1.45 }}>
          {handoff.summary}
        </div>
        <div style={{ marginTop: 7, fontSize: 12, color: "var(--t1)", lineHeight: 1.45 }}>
          Next: {handoff.recommended_next_action}
        </div>
        <div style={{ ...metaLineStyle, marginTop: 8 }}>
          {role && <span>target {role.name}</span>}
          {handoff.target_assignment_id && (
            <span>assignment {shortID(handoff.target_assignment_id)}</span>
          )}
          <span>{handoff.provenance_kind}</span>
          <span>{handoff.trust_label}</span>
          {handoff.updated_at && <span>Updated {formatAbsoluteTime(handoff.updated_at)}</span>}
        </div>
        {sourceRefs.length > 0 && <HandoffSourceEvidence refs={sourceRefs} />}
        {targetEvidence?.hasEvidence && (
          <ProjectAssignmentEvidence evidence={targetEvidence} title="Target evidence" compact />
        )}
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginTop: 9 }}>
          {handoff.status === "pending" && (
            <>
              <button
                className="btn btn-ghost btn-sm"
                type="button"
                onClick={() => onSetStatus("accepted")}
                disabled={actionPending}
              >
                <Icon d={Icons.check} size={12} />
                Accept
              </button>
              <button
                className="btn btn-ghost btn-sm"
                type="button"
                onClick={() => onSetStatus("dismissed")}
                disabled={actionPending}
              >
                Dismiss
              </button>
            </>
          )}
          {handoff.status !== "superseded" && (
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => onSetStatus("superseded")}
              disabled={actionPending}
            >
              Supersede
            </button>
          )}
          {canCreateAssignment && (
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={onCreateAssignment}
              disabled={actionPending}
            >
              <Icon d={Icons.plus} size={12} />
              Create follow-up assignment
            </button>
          )}
          {assignment && (
            <button
              className="btn btn-primary btn-sm"
              type="button"
              onClick={() => void openPreflight()}
              disabled={!startable || starting}
              title={
                startable
                  ? "Review launch context before starting the linked assignment."
                  : "Linked assignment is not queued."
              }
            >
              <Icon d={external ? Icons.chat : Icons.send} size={12} />
              {starting ? (external ? "Preparing..." : "Starting...") : "Start from handoff"}
            </button>
          )}
        </div>
      </div>
      {preflightOpen && assignment && (
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
            <span key={warning} className="badge badge-amber">
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
      style={{ padding: 24, textAlign: "center", display: "grid", gap: 8, placeItems: "center" }}
    >
      <div style={{ color: "var(--t0)", fontSize: 14, fontWeight: 600 }}>{title}</div>
      <div style={{ color: "var(--t3)", fontSize: 12, lineHeight: 1.5, maxWidth: 320 }}>
        {detail}
      </div>
    </div>
  );
}

function linkedChatStatusTitle(linkedChat: NonNullable<ProjectActivityItemRecord["linked_chat"]>) {
  return [
    linkedChat.title,
    linkedChat.agent_id,
    linkedChat.status ? `session ${linkedChat.status}` : "",
    linkedChat.latest_status ? `latest ${linkedChat.latest_status}` : "",
    linkedChat.latest_error,
  ]
    .filter(Boolean)
    .join(" · ");
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
  const resolvedProfile = firstNonEmpty(role?.default_agent_profile, project.default_agent_profile);
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
    `- Profile: ${firstNonEmpty(resolvedProfile, "none")}`,
    `- Role defaults: ${formatHintList([
      ["driver", role?.default_driver_kind],
      ["provider", role?.default_provider],
      ["model", role?.default_model],
      ["profile", role?.default_agent_profile],
    ])}`,
    `- Project defaults: ${formatHintList([
      ["provider", project.default_provider],
      ["model", project.default_model],
      ["profile", project.default_agent_profile],
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

function assignmentLaunchReadinessNotice(
  packet: ContextPacketRecord,
): AssignmentLaunchReadinessNoticeRecord | null {
  const item = packet.items?.find((candidate) => candidate.kind === "launch_readiness");
  if (!item) return null;
  const ready = (item.metadata?.ready || contextBodyField(item.body || "", "Ready")).toLowerCase();
  if (ready !== "false") return null;
  const message = item.metadata?.message || contextBodyField(item.body || "", "Message");
  const action =
    item.metadata?.operator_action || contextBodyField(item.body || "", "Operator action");
  const reason = item.metadata?.reason || contextBodyField(item.body || "", "Reason");
  const detail = [message, action].filter(Boolean).join(" ");
  return {
    detail: detail || reason || "The selected provider/model cannot be routed for this assignment.",
  };
}

function contextBodyField(body: string, label: string): string {
  const prefix = `${label}:`;
  for (const rawLine of body.split("\n")) {
    const line = rawLine.trim();
    if (line.startsWith(prefix)) return line.slice(prefix.length).trim();
  }
  return "";
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

const startPanelSecondaryLabelStyle: CSSProperties = {
  color: "var(--t3)",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  textTransform: "uppercase",
};

const closeoutListStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 12,
  lineHeight: 1.45,
  margin: "8px 0 0",
  paddingLeft: 18,
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

const assignmentStyle: CSSProperties = {
  border: "1px solid var(--border)",
  background: "var(--bg2)",
  borderRadius: "var(--radius-sm)",
  padding: 10,
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
  paddingTop: 8,
};

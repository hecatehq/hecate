import type { CSSProperties, KeyboardEvent, ReactNode } from "react";

import type {
  ProjectActivityBucketKey,
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectContextSourceRecord,
  ProjectHandoffRecord,
  ProjectMemoryCandidateRecord,
  ProjectMemoryRecord,
  ProjectOperationsBrief,
  ProjectOperationsBriefItem,
  ProjectRecord,
  ProjectSetupReadiness,
  ProjectSetupReadinessAction,
  ProjectSetupReadinessCheck,
  ProjectSkillRecord,
  ProjectWorkItemReadinessRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
  UpdateProjectSkillPayload,
} from "../../types/project";
import { Badge, Icon, Icons, InlineError } from "../shared/ui";
import { ProjectAssistantPanel } from "./ProjectAssistantPanel";
import { ProjectMemoryPanel } from "./ProjectMemoryPanel";
import { ProjectSkillsPanel } from "./ProjectSkillsPanel";
import { ProjectTimelinePanel } from "./ProjectTimelinePanel";
import {
  ProjectWorkItemDetail,
  type ProjectAssignmentChatLaunchRequest,
  type ProjectWorkItemFocusTarget,
} from "./ProjectWorkItemDetail";
import {
  projectWorkItemFollowThroughIntent,
  projectWorkItemOperationTargetAvailable,
} from "./ProjectWorkItemFollowThrough";
import { projectOperationHasActionTargetMismatch } from "./projectActionRouting";
import { toProjectAssignmentExecutionViewModel } from "./projectAssignmentViewModels";
import { formatProjectRowRelativeTime, workStatusLabel } from "./projectDisplay";
import { projectActivityWorkItemToWorkItem } from "./projectInsights";
import { projectVisibilityDetail } from "./projectVisibilityDetail";
import { useProjectAssistantController } from "./useProjectAssistantController";

export type WorkItemSummary = {
  assignmentCount: number;
  activeCount: number;
  failedCount: number;
  completedCount: number;
};

export type LoadState = "idle" | "loading" | "loaded" | "error";

export type ProjectWorkspaceTab = "overview" | "work" | "timeline" | "memory" | "skills";

export type ProjectWorkspaceViewProps = {
  activity: ProjectActivityData | null;
  activityBucket: ProjectActivityBucketKey;
  activityByAssignmentID: Map<string, ProjectActivityItemRecord>;
  activityLoadState: LoadState;
  artifacts: ProjectCollaborationArtifactRecord[];
  artifactActionID: string;
  assignmentErrors: Record<string, string>;
  assignments: ProjectAssignmentRecord[];
  assistant: ReturnType<typeof useProjectAssistantController>;
  draftingDefaultAssignment: boolean;
  detailError: string;
  detailLoadState: LoadState;
  discoveringContext: boolean;
  discoveringSkills: boolean;
  handoffActionID: string;
  handoffError: string;
  handoffs: ProjectHandoffRecord[];
  hasWorkItemDetail: boolean;
  workItemFocusTarget?: ProjectWorkItemFocusTarget | null;
  memoryCandidates: ProjectMemoryCandidateRecord[];
  memoryEntries: ProjectMemoryRecord[];
  memoryError: string;
  memoryLoadState: LoadState;
  onActivityBucketChange: (bucket: ProjectActivityBucketKey) => void;
  onAddAssignment: () => void;
  onAddEvidenceLink: (assignmentID?: string) => void;
  onAddHandoff: () => void;
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
  onCreateWork: () => void;
  onCloseWorkItem: (item: ProjectWorkItemRecord) => void;
  onSetAssignmentStatus: (assignment: ProjectAssignmentRecord, status: string) => void;
  onDeleteAssignment: (assignment: ProjectAssignmentRecord) => void;
  onDeleteHandoff: (handoff: ProjectHandoffRecord) => void;
  onDeleteMemory: (entry: ProjectMemoryRecord) => void;
  onDeleteSource: (source: ProjectContextSourceRecord) => void;
  onDeleteWorkItem: (item: ProjectWorkItemRecord) => void;
  onDiscoverContextSources: () => void;
  onDiscoverProjectSkills: () => void;
  onEditAssignment: (assignment: ProjectAssignmentRecord) => void;
  onEditHandoff: (handoff: ProjectHandoffRecord) => void;
  onEditMemory: (entry: ProjectMemoryRecord) => void;
  onEditSource: (source: ProjectContextSourceRecord) => void;
  onEditWorkItem: (item: ProjectWorkItemRecord) => void;
  onManagePresets: () => void;
  onManageRoles: () => void;
  onNavigateWorkspaceTab: (tab: ProjectWorkspaceTab) => void;
  onNewMemory: () => void;
  onNewSource: () => void;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenConnections?: () => void;
  onOpenSettings: () => void;
  onOperationAction: (item: ProjectOperationsBriefItem) => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onPromoteCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onRefreshMemory: () => void;
  onRefreshProjectSkills: () => void;
  onRefreshWorkItem: () => void | boolean | Promise<void | boolean>;
  onWorkItemFocusTargetHandled?: () => void;
  onRejectCandidate: (candidate: ProjectMemoryCandidateRecord) => void | Promise<void>;
  onSelectWorkItem: (workItemID: string) => void;
  onSetHandoffStatus: (handoff: ProjectHandoffRecord, status: string) => void;
  onStartAssignment: (assignment: ProjectAssignmentRecord) => void;
  onSetupReadinessAction: (action: ProjectSetupReadinessAction) => void;
  onUpdateProjectSkill: (skill: ProjectSkillRecord, patch: UpdateProjectSkillPayload) => void;
  onWorkspaceTabChange: (tab: ProjectWorkspaceTab) => void;
  project: ProjectRecord | null;
  projectEmptyDetail: string;
  projectEmptyTitle: string;
  projectNeedsOnboarding: boolean;
  projectSetupError: string;
  projectSetupPending: boolean;
  projectSetupReadiness: ProjectSetupReadiness | null;
  overviewError: string;
  operationsBrief: ProjectOperationsBrief | null;
  operationsBriefError: string;
  operationsBriefLoadState: LoadState;
  projectSkills: ProjectSkillRecord[];
  preparingAssignmentID: string;
  rejectingCandidateID: string;
  roleByID: Map<string, ProjectWorkRoleRecord>;
  roles: ProjectWorkRoleRecord[];
  selectedWorkItem: ProjectWorkItemRecord | null;
  selectedWorkItemOperationID?: string;
  selectedWorkItemReadiness: ProjectWorkItemReadinessRecord | null;
  selectedWorkItemID: string;
  closingWorkItemID: string;
  skillsError: string;
  skillsLoadState: LoadState;
  startingAssignmentIDs: ReadonlySet<string>;
  updatingSkillID: string;
  workError: string;
  workItemSummaries: Record<string, WorkItemSummary>;
  workItems: ProjectWorkItemRecord[];
  workLoadState: LoadState;
  workspaceTab: ProjectWorkspaceTab;
};

export function ProjectWorkspaceView({
  activity,
  activityBucket,
  activityByAssignmentID,
  activityLoadState,
  artifacts,
  artifactActionID,
  assignmentErrors,
  assignments,
  assistant,
  draftingDefaultAssignment,
  detailError,
  detailLoadState,
  discoveringContext,
  discoveringSkills,
  handoffActionID,
  handoffError,
  handoffs,
  hasWorkItemDetail,
  workItemFocusTarget = null,
  memoryCandidates,
  memoryEntries,
  memoryError,
  memoryLoadState,
  onActivityBucketChange,
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
  onCreateWork,
  onCloseWorkItem,
  onSetAssignmentStatus,
  onDeleteAssignment,
  onDeleteHandoff,
  onDeleteMemory,
  onDeleteSource,
  onDeleteWorkItem,
  onDiscoverContextSources,
  onDiscoverProjectSkills,
  onEditAssignment,
  onEditHandoff,
  onEditMemory,
  onEditSource,
  onEditWorkItem,
  onManagePresets,
  onManageRoles,
  onNavigateWorkspaceTab,
  onNewMemory,
  onNewSource,
  onOpenChat,
  onOpenConnections,
  onOpenSettings,
  onOperationAction,
  onOpenTask,
  onPromoteCandidate,
  onRefreshMemory,
  onRefreshProjectSkills,
  onRefreshWorkItem,
  onWorkItemFocusTargetHandled,
  onRejectCandidate,
  onSelectWorkItem,
  onSetHandoffStatus,
  onStartAssignment,
  onSetupReadinessAction,
  onUpdateProjectSkill,
  onWorkspaceTabChange,
  project,
  projectEmptyDetail,
  projectEmptyTitle,
  projectNeedsOnboarding,
  projectSetupError,
  projectSetupPending,
  projectSetupReadiness,
  overviewError,
  operationsBrief,
  operationsBriefError,
  operationsBriefLoadState,
  projectSkills,
  preparingAssignmentID,
  rejectingCandidateID,
  roleByID,
  roles,
  selectedWorkItem,
  selectedWorkItemOperationID = "",
  selectedWorkItemReadiness,
  selectedWorkItemID,
  closingWorkItemID,
  skillsError,
  skillsLoadState,
  startingAssignmentIDs,
  updatingSkillID,
  workError,
  workItemSummaries,
  workItems,
  workLoadState,
  workspaceTab,
}: ProjectWorkspaceViewProps) {
  const projectSetupStarted = projectSetupReadiness?.setup_started ?? false;
  const projectSetupFirst = projectSetupReadiness?.first_work_ready ?? false;
  const projectSetupAssistantMode =
    projectSetupFirst ||
    (workItems.length === 0 &&
      !selectedWorkItem &&
      (Boolean(assistant.proposal) || Boolean(assistant.applyResult)));
  const projectWorkItemCount = workItems.length || activity?.summary.work_item_count || 0;
  const operationItems = Array.isArray(operationsBrief?.items) ? operationsBrief.items : [];
  const firstSelectedWorkItemOperation =
    operationItems.find(
      (item) =>
        (item.action?.type === "open_work_item" ||
          item.action?.type === "open_assignment_preflight") &&
        item.action.work_item_id === selectedWorkItemID,
    ) ?? null;
  const selectedWorkItemOperation = selectedWorkItemOperationID
    ? (operationItems.find(
        (item) =>
          item.id === selectedWorkItemOperationID &&
          item.action?.type === "open_work_item" &&
          item.action.work_item_id === selectedWorkItemID,
      ) ?? (operationsBriefLoadState === "loaded" ? firstSelectedWorkItemOperation : null))
    : firstSelectedWorkItemOperation;
  const visibleSelectedWorkItemOperation =
    assistant.proposal || !selectedWorkItemReadiness ? null : selectedWorkItemOperation;
  const selectedOperationTargetAvailable = projectWorkItemOperationTargetAvailable(
    visibleSelectedWorkItemOperation,
    {
      artifacts,
      assignments,
      handoffs,
    },
  );
  const selectedAssignmentPreflightID =
    visibleSelectedWorkItemOperation?.action?.type === "open_assignment_preflight" &&
    !projectOperationHasActionTargetMismatch(visibleSelectedWorkItemOperation) &&
    selectedOperationTargetAvailable
      ? visibleSelectedWorkItemOperation.action.assignment_id?.trim() || ""
      : "";
  const selectedWorkItemFollowThroughOperation =
    visibleSelectedWorkItemOperation?.action?.type === "open_work_item" ||
    (visibleSelectedWorkItemOperation?.action?.type === "open_assignment_preflight" &&
      !selectedAssignmentPreflightID)
      ? visibleSelectedWorkItemOperation
      : null;
  const selectedWorkItemClosed =
    selectedWorkItemReadiness?.status === "done" ||
    selectedWorkItem?.status === "done" ||
    selectedWorkItem?.status === "cancelled";
  const selectedWorkItemNeedsFirstStep = Boolean(
    selectedWorkItem &&
    !selectedWorkItemClosed &&
    assignments.length === 0 &&
    artifacts.length === 0 &&
    handoffs.length === 0,
  );
  const followThroughOwnsPrimary = Boolean(
    selectedWorkItem &&
    selectedWorkItemReadiness &&
    projectWorkItemFollowThroughIntent(
      selectedWorkItemReadiness,
      selectedWorkItemFollowThroughOperation,
      selectedWorkItem,
      selectedOperationTargetAvailable,
    ),
  );
  const routineActionsAreSecondary =
    followThroughOwnsPrimary ||
    Boolean(visibleSelectedWorkItemOperation) ||
    selectedWorkItemReadiness?.status === "ready" ||
    selectedWorkItemNeedsFirstStep ||
    Boolean(assistant.proposal);

  return (
    <section style={detailStyle} aria-label="Project workspace content">
      <div className="project-cockpit-workspace" style={cockpitWorkspaceStyle}>
        {project ? (
          <section style={domainSectionStyle} aria-label="Project workspace">
            {projectSetupPending ? (
              <section
                aria-busy="true"
                aria-label="Project setup loading"
                style={projectEmptyStateStyle}
              >
                <div aria-live="polite" role="status">
                  <ProjectEmptyBlock
                    title="Loading project…"
                    detail="Checking setup and coordination status."
                  />
                </div>
              </section>
            ) : projectSetupError ? (
              <section aria-label="Project setup unavailable" style={projectSetupErrorStyle}>
                <SectionHeader
                  heading
                  title="Project setup is unavailable"
                  detail="Hecate could not confirm this project's guided setup state. No project action has been assumed."
                />
                <InlineError message={projectSetupError} />
                <div>
                  <button
                    className="btn btn-primary btn-sm"
                    type="button"
                    onClick={onRefreshWorkItem}
                  >
                    <Icon d={Icons.refresh} size={13} />
                    Retry
                  </button>
                </div>
              </section>
            ) : projectNeedsOnboarding ? (
              projectSetupReadiness && (
                <ProjectOnboardingPanel
                  bootstrapPending={assistant.bootstrapPending}
                  onAction={onSetupReadinessAction}
                  onOpenSettings={onOpenSettings}
                  project={project}
                  readiness={projectSetupReadiness}
                />
              )
            ) : (
              <ProjectWorkspaceTabs
                activeTab={workspaceTab}
                memoryCandidateCount={memoryCandidates.length}
                memoryEntryCount={memoryEntries.length}
                onChange={onWorkspaceTabChange}
                projectSkillCount={projectSkills.length}
                workItemCount={projectWorkItemCount}
              />
            )}
            {!projectSetupPending &&
              !projectSetupError &&
              !projectNeedsOnboarding &&
              overviewError && <InlineError message={overviewError} />}
            {!projectSetupPending &&
              !projectSetupError &&
              !projectNeedsOnboarding &&
              workspaceTab === "overview" && (
                <div
                  aria-labelledby="project-workspace-tab-overview"
                  id="project-workspace-panel-overview"
                  role="tabpanel"
                >
                  <section aria-label="Project overview" style={projectTabPanelStyle}>
                    <SectionHeader
                      heading
                      title="Project Overview"
                      detail="Current coordination status and the next operator action."
                    />
                    <ProjectOperationsBriefPanel
                      brief={operationsBrief}
                      error={operationsBriefError}
                      loading={operationsBriefLoadState === "loading"}
                      onAction={onOperationAction}
                    />
                    {workError && <InlineError message={workError} />}
                    <ProjectActivitySummary
                      activity={activity}
                      loadState={activityLoadState}
                      memoryCandidateCount={memoryCandidates.length}
                      onBucketChange={(bucket) => {
                        onActivityBucketChange(bucket);
                        onNavigateWorkspaceTab("work");
                      }}
                      onReviewMemory={() => onNavigateWorkspaceTab("memory")}
                      onViewWork={() => onNavigateWorkspaceTab("work")}
                      workItemCount={projectWorkItemCount}
                      workItems={workItems}
                    />
                  </section>
                </div>
              )}
            {!projectSetupPending &&
              !projectSetupError &&
              !projectNeedsOnboarding &&
              workspaceTab === "work" && (
                <div
                  aria-labelledby="project-workspace-tab-work"
                  id="project-workspace-panel-work"
                  role="tabpanel"
                >
                  <section aria-label="Work coordination" style={projectTabPanelStyle}>
                    {!selectedWorkItemClosed && (
                      <ProjectAssistantPanel
                        applyResult={assistant.applyResult}
                        bootstrapPending={assistant.bootstrapPending}
                        chatDraftSource={assistant.chatDraftSource}
                        context={assistant.context}
                        contextError={assistant.contextError}
                        contextStatus={assistant.contextStatus}
                        error={assistant.error}
                        onApply={() => void assistant.apply()}
                        onBootstrap={() => void assistant.bootstrap()}
                        onCreateWork={onCreateWork}
                        onInspectContext={(form) => void assistant.inspectContext(form)}
                        onDismiss={assistant.dismiss}
                        onManageRoles={onManageRoles}
                        onOpenWork={() => onWorkspaceTabChange("work")}
                        onOpenSourceChat={
                          assistant.chatDraftSource?.sourceSessionID && onOpenChat
                            ? () =>
                                onOpenChat({
                                  projectID: project.id,
                                  chatSessionID: assistant.chatDraftSource?.sourceSessionID,
                                })
                            : undefined
                        }
                        onPropose={(form) => void assistant.propose(form)}
                        onReviewMemory={() => onWorkspaceTabChange("memory")}
                        project={project}
                        proposal={assistant.proposal}
                        primaryEmphasis={!routineActionsAreSecondary}
                        roles={roles}
                        memoryCandidateCount={memoryCandidates.length}
                        roleCount={roles.length}
                        setupFirst={projectSetupAssistantMode}
                        setupStarted={projectSetupStarted}
                        status={assistant.status}
                        workItem={selectedWorkItem}
                        workItemCount={workItems.length}
                      />
                    )}
                    <section aria-label="Work activity" style={workActivityPanelStyle}>
                      <SectionHeader
                        heading
                        title="Work Queue"
                        detail={
                          workLoadState === "loading" && workItems.length === 0
                            ? "Loading project work…"
                            : undefined
                        }
                        actions={
                          <button
                            className="btn btn-ghost btn-sm"
                            type="button"
                            onClick={onCreateWork}
                          >
                            <Icon d={Icons.plus} size={13} />
                            Work
                          </button>
                        }
                      />
                      <ProjectActivityBucketTabs
                        activity={activity}
                        activityLoadState={activityLoadState}
                        bucket={activityBucket}
                        onBucketChange={onActivityBucketChange}
                        workItemCount={projectWorkItemCount}
                      />
                    </section>
                    {workError && <InlineError message={workError} />}
                    <div
                      className="project-work-coordination-grid"
                      style={workCoordinationGridStyle}
                    >
                      <ProjectActivityInbox
                        activity={activity}
                        activityLoadState={activityLoadState}
                        bucket={activityBucket}
                        onSelectWorkItem={onSelectWorkItem}
                        project={project}
                        roleByID={roleByID}
                        selectedWorkItemID={selectedWorkItemID}
                        workItemSummaries={workItemSummaries}
                        workItems={workItems}
                        workLoadState={workLoadState}
                      />
                      <section aria-label="Selected work item" style={workDetailColumnStyle}>
                        {hasWorkItemDetail ? (
                          <ProjectWorkItemDetail
                            assistantProposalOpen={Boolean(assistant.proposal)}
                            assignments={assignments}
                            artifacts={artifacts}
                            artifactActionID={artifactActionID}
                            handoffActionID={handoffActionID}
                            handoffError={handoffError}
                            handoffs={handoffs}
                            assignmentErrors={assignmentErrors}
                            detailError={detailError}
                            draftingDefaultAssignment={draftingDefaultAssignment}
                            preparingAssignmentID={preparingAssignmentID}
                            focusTarget={workItemFocusTarget}
                            loading={detailLoadState === "loading"}
                            onOpenTask={onOpenTask}
                            onRefresh={onRefreshWorkItem}
                            onFocusTargetHandled={onWorkItemFocusTargetHandled}
                            onCreateAssignmentFromHandoff={onCreateAssignmentFromHandoff}
                            activityByAssignmentID={activityByAssignmentID}
                            onDeleteHandoff={onDeleteHandoff}
                            onDeleteWorkItem={onDeleteWorkItem}
                            onCloseWorkItem={onCloseWorkItem}
                            onSetAssignmentStatus={onSetAssignmentStatus}
                            onEditHandoff={onEditHandoff}
                            onEditAssignment={onEditAssignment}
                            onEditWorkItem={onEditWorkItem}
                            onDeleteAssignment={onDeleteAssignment}
                            onManagePresets={onManagePresets}
                            onManageRoles={onManageRoles}
                            onOpenChat={onOpenChat}
                            onOpenConnections={onOpenConnections}
                            onOpenSettings={onOpenSettings}
                            onStartAssignment={onStartAssignment}
                            onSetHandoffStatus={onSetHandoffStatus}
                            onOpenWorkItem={onSelectWorkItem}
                            project={project}
                            primaryAssignmentID={selectedAssignmentPreflightID}
                            roleByID={roleByID}
                            closingWorkItemID={closingWorkItemID}
                            closeoutReadiness={selectedWorkItemReadiness}
                            operation={selectedWorkItemFollowThroughOperation}
                            startingAssignmentIDs={startingAssignmentIDs}
                            workItem={selectedWorkItem}
                            onAddAssignment={onAddAssignment}
                            onAddEvidenceLink={onAddEvidenceLink}
                            onAddHandoff={onAddHandoff}
                            onAddHandoffFromAssignment={onAddHandoffFromAssignment}
                            onAddReviewHandoffFromAssignment={onAddReviewHandoffFromAssignment}
                            onAddReviewArtifactFromAssignment={onAddReviewArtifactFromAssignment}
                            onAddHandoffFromReviewArtifact={onAddHandoffFromReviewArtifact}
                            onDraftDefaultAssignment={onDraftDefaultAssignment}
                            onPreparedAssignmentPreflightOpened={
                              onPreparedAssignmentPreflightOpened
                            }
                            onCreateAssignmentFromReviewArtifact={
                              onCreateAssignmentFromReviewArtifact
                            }
                          />
                        ) : (
                          <ProjectEmptyBlock
                            title={
                              workLoadState === "loading" ? "Loading detail…" : "No work selected"
                            }
                            detail="Create or select a work item to manage assignments and collaboration artifacts."
                          />
                        )}
                      </section>
                    </div>
                  </section>
                </div>
              )}
            {!projectSetupPending &&
              !projectSetupError &&
              !projectNeedsOnboarding &&
              workspaceTab === "timeline" && (
                <div
                  aria-labelledby="project-workspace-tab-timeline"
                  id="project-workspace-panel-timeline"
                  role="tabpanel"
                >
                  <ProjectTimelinePanel
                    activity={activity}
                    artifacts={artifacts}
                    handoffs={handoffs}
                    memoryCandidates={memoryCandidates}
                    memoryEntries={memoryEntries}
                    onEditMemory={onEditMemory}
                    onOpenChat={onOpenChat}
                    onOpenTask={onOpenTask}
                    onSelectWorkItem={onSelectWorkItem}
                    project={project}
                    roles={roles}
                    workItems={workItems}
                  />
                </div>
              )}
            {!projectSetupPending &&
              !projectSetupError &&
              !projectNeedsOnboarding &&
              workspaceTab === "memory" && (
                <div
                  aria-labelledby="project-workspace-tab-memory"
                  id="project-workspace-panel-memory"
                  role="tabpanel"
                >
                  <ProjectMemoryPanel
                    candidates={memoryCandidates}
                    discoveringContext={discoveringContext}
                    entries={memoryEntries}
                    error={memoryError}
                    loading={memoryLoadState === "loading"}
                    onDiscoverContextSources={onDiscoverContextSources}
                    onDeleteSource={onDeleteSource}
                    onEditSource={onEditSource}
                    onPromoteCandidate={onPromoteCandidate}
                    onRejectCandidate={onRejectCandidate}
                    onDelete={onDeleteMemory}
                    onEdit={onEditMemory}
                    onNew={onNewMemory}
                    onNewSource={onNewSource}
                    onRefresh={onRefreshMemory}
                    project={project}
                    rejectingCandidateID={rejectingCandidateID}
                  />
                </div>
              )}
            {!projectSetupPending &&
              !projectSetupError &&
              !projectNeedsOnboarding &&
              workspaceTab === "skills" && (
                <div
                  aria-labelledby="project-workspace-tab-skills"
                  id="project-workspace-panel-skills"
                  role="tabpanel"
                >
                  <ProjectSkillsPanel
                    discovering={discoveringSkills}
                    error={skillsError}
                    loading={skillsLoadState === "loading"}
                    onDiscover={onDiscoverProjectSkills}
                    onRefresh={onRefreshProjectSkills}
                    onUpdate={onUpdateProjectSkill}
                    project={project}
                    skills={projectSkills}
                    updatingSkillID={updatingSkillID}
                  />
                </div>
              )}
          </section>
        ) : (
          <section aria-label="Project empty state" style={projectEmptyStateStyle}>
            <ProjectEmptyBlock title={projectEmptyTitle} detail={projectEmptyDetail} />
          </section>
        )}
      </div>
    </section>
  );
}

function WorkItemRow({
  active,
  item,
  role,
  summary,
  onSelect,
}: {
  active: boolean;
  item: ProjectWorkItemRecord;
  role?: ProjectWorkRoleRecord;
  summary?: WorkItemSummary;
  onSelect: () => void;
}) {
  return (
    <div
      className="project-work-item-row"
      role="button"
      tabIndex={0}
      aria-current={active ? "true" : undefined}
      aria-label={`Open work item ${item.title}`}
      onClick={onSelect}
      onKeyDown={(event) => {
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        onSelect();
      }}
      style={{
        padding: "11px 12px",
        borderBottom: "1px solid var(--border)",
        borderLeft: active ? "2px solid var(--teal)" : "2px solid transparent",
        background: active ? "var(--bg2)" : "transparent",
        cursor: "pointer",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          marginBottom: 6,
        }}
      >
        <Badge status={item.status} label={workStatusLabel(item.status)} />
        <span className="badge badge-muted">{item.priority}</span>
        {summary && summary.assignmentCount > 0 && (
          <span className="badge badge-muted">
            {summary.assignmentCount} assignment
            {summary.assignmentCount === 1 ? "" : "s"}
          </span>
        )}
      </div>
      <div style={titleStyle}>{item.title}</div>
      <div style={metaLineStyle}>
        <span>{role?.name ?? item.owner_role_id ?? "No owner role"}</span>
        {summary && summary.activeCount > 0 && <span>{summary.activeCount} active</span>}
        {summary && summary.failedCount > 0 && <span>{summary.failedCount} failed</span>}
        {summary && summary.completedCount > 0 && <span>{summary.completedCount} done</span>}
      </div>
    </div>
  );
}

function SectionHeader({
  actions,
  detail,
  heading = false,
  title,
}: {
  actions?: ReactNode;
  detail?: string;
  heading?: boolean;
  title: string;
}) {
  return (
    <div style={domainHeaderStyle}>
      <div style={{ minWidth: 0 }}>
        {heading ? (
          <h1 style={{ ...sectionLabelStyle, margin: 0 }}>{title}</h1>
        ) : (
          <div style={sectionLabelStyle}>{title}</div>
        )}
        {detail && <div style={{ ...subtleTextStyle, marginTop: 3 }}>{detail}</div>}
      </div>
      {actions && <div style={domainHeaderActionsStyle}>{actions}</div>}
    </div>
  );
}

function ProjectOnboardingPanel({
  bootstrapPending,
  onAction,
  onOpenSettings,
  project,
  readiness,
}: {
  bootstrapPending: boolean;
  onAction: (action: ProjectSetupReadinessAction) => void;
  onOpenSettings: () => void;
  project: ProjectRecord;
  readiness: ProjectSetupReadiness;
}) {
  const primaryAction = readiness.primary_action;
  return (
    <section aria-label="Project onboarding" style={projectOnboardingStyle}>
      <div style={projectOnboardingCopyStyle}>
        <div>
          <div style={sectionLabelStyle}>Project Onboarding</div>
          <h1 style={{ ...projectOnboardingTitleStyle, margin: "8px 0 0" }}>
            Set up {project.name}
          </h1>
        </div>
        <div style={projectOnboardingDetailStyle}>
          Let Hecate discover safe project metadata, suggest roles, and prepare setup actions for
          review. Attach local files only when this project needs a workspace.
        </div>
        <div style={projectOnboardingActionsStyle}>
          <button
            className="btn btn-primary btn-sm"
            type="button"
            disabled={bootstrapPending && primaryAction.type === "bootstrap_project"}
            onClick={() => onAction(primaryAction)}
          >
            <Icon d={Icons.refresh} size={13} />
            {bootstrapPending && primaryAction.type === "bootstrap_project"
              ? "Setting up…"
              : primaryAction.label}
          </button>
          <button className="btn btn-ghost btn-sm" type="button" onClick={onOpenSettings}>
            <Icon d={Icons.settings} size={13} />
            Project settings
          </button>
        </div>
      </div>
      <div style={projectOnboardingChecklistStyle}>
        {readiness.checks.map((check) => (
          <div
            aria-label={check.label}
            key={check.id}
            role="group"
            style={projectOnboardingCheckStyle}
          >
            <span
              className={projectOnboardingCheckBadgeClass(check)}
              style={projectOnboardingCheckBadgeStyle}
            >
              {projectOnboardingCheckBadgeLabel(check)}
            </span>
            <div style={{ minWidth: 0 }}>
              <div style={titleStyle}>{check.label}</div>
              <div style={subtleTextStyle}>{check.detail}</div>
            </div>
            {check.status !== "ready" && !check.optional && check.action && (
              <button
                aria-label={`${check.action.label}: ${check.label}`}
                className="btn btn-ghost btn-sm"
                disabled={bootstrapPending && check.action.type === "bootstrap_project"}
                onClick={() => onAction(check.action!)}
                style={projectOnboardingCheckActionStyle}
                type="button"
              >
                {check.action.label}
              </button>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}

function projectOnboardingCheckBadgeClass(check: ProjectSetupReadinessCheck): string {
  return check.status === "ready" ? "badge badge-green" : "badge badge-muted";
}

function projectOnboardingCheckBadgeLabel(check: ProjectSetupReadinessCheck): string {
  if (check.optional || check.status === "optional") return "optional";
  if (check.status === "ready") return "ready";
  return "todo";
}

function ProjectOperationsBriefPanel({
  brief,
  error,
  loading,
  onAction,
}: {
  brief: ProjectOperationsBrief | null;
  error: string;
  loading: boolean;
  onAction: (item: ProjectOperationsBriefItem) => void;
}) {
  const items = Array.isArray(brief?.items) ? brief.items : [];
  if ((!brief || items.length === 0) && !loading) {
    return error ? <InlineError message={error} /> : null;
  }

  const primary = items[0] ?? null;
  const secondary = items.slice(1, 4);
  const shownItemCount = (primary ? 1 : 0) + secondary.length;
  const limitDetail = projectOperationsLimitDetail(brief, shownItemCount);
  const title = loading && !brief ? "Loading operations…" : primary?.title || "Operations clear";
  const detail =
    loading && !brief
      ? "Checking project work, memory candidates, handoffs, and launch defaults."
      : primary?.detail || "No queued, blocked, handoff, or memory-review items need attention.";

  return (
    <section
      aria-label="Project operations"
      className="project-operations-brief"
      style={projectOperationsBriefStyle}
    >
      <div className="project-operations-brief-main" style={projectOperationsBriefMainStyle}>
        <div style={sectionLabelStyle}>Project Operations</div>
        <div aria-atomic="true" aria-busy={loading} aria-live="polite" role="status">
          <div style={projectOperationsTitleStyle}>{title}</div>
          <div style={subtleTextStyle}>{detail}</div>
        </div>
      </div>
      <div
        className="project-operations-brief-controls"
        style={projectOperationsBriefControlsStyle}
      >
        {primary ? (
          <>
            <Badge
              status={primary.status || primary.priority}
              label={projectOperationBadge(primary)}
            />
            <button
              aria-label={`${primary.action_label}: ${primary.title}`}
              className="btn btn-primary btn-sm"
              type="button"
              onClick={() => onAction(primary)}
            >
              {primary.action_label}
            </button>
          </>
        ) : (
          <span className="badge badge-green">clear</span>
        )}
      </div>
      {secondary.length > 0 && (
        <div style={projectOperationsListStyle}>
          {secondary.map((item) => (
            <button
              aria-label={`${item.action_label}: ${item.title}`}
              className="btn btn-ghost btn-sm"
              key={item.id}
              onClick={() => onAction(item)}
              style={projectOperationsItemButtonStyle}
              type="button"
            >
              <span className="badge badge-muted">{projectOperationBadge(item)}</span>
              <span style={projectOperationsItemTitleStyle}>{item.title}</span>
              <span style={projectOperationsItemActionStyle}>{item.action_label}</span>
            </button>
          ))}
        </div>
      )}
      {limitDetail && <div style={subtleTextStyle}>{limitDetail}</div>}
      {error && <InlineError message={error} />}
    </section>
  );
}

function projectOperationsLimitDetail(
  brief: ProjectOperationsBrief | null,
  shownItemCount: number,
) {
  if (!brief || shownItemCount === 0) return "";
  const items = Array.isArray(brief.items) ? brief.items : [];
  const returnedItemCount = Math.max(brief.summary.item_count ?? 0, items.length);
  const availableItemCount = Math.max(
    brief.summary.available_item_count ?? returnedItemCount,
    returnedItemCount,
  );
  const omittedItemCount = Math.max(
    brief.summary.omitted_item_count ?? availableItemCount - returnedItemCount,
    0,
  );
  return projectVisibilityDetail({
    shownCount: shownItemCount,
    totalCount: availableItemCount,
    itemLabelSingular: "operation",
    itemLabelPlural: "operations",
    hiddenLabelSingular: "operation",
    hiddenLabelPlural: "operations",
    serverOmittedCount: omittedItemCount,
  });
}

function projectOperationBadge(item: ProjectOperationsBriefItem): string {
  if (item.priority === "high") return "attention";
  if (item.priority === "medium") return "review";
  if (item.priority === "low") return "watch";
  return item.priority || item.status || "operation";
}

function ProjectActivitySummary({
  activity,
  loadState,
  memoryCandidateCount,
  onBucketChange,
  onReviewMemory,
  onViewWork,
  workItemCount,
  workItems,
}: {
  activity: ProjectActivityData | null;
  loadState: LoadState;
  memoryCandidateCount: number;
  onBucketChange: (bucket: ProjectActivityBucketKey) => void;
  onReviewMemory: () => void;
  onViewWork: () => void;
  workItemCount: number;
  workItems: ProjectWorkItemRecord[];
}) {
  const blocked = activity?.summary.blocked_count ?? 0;
  const active = activity?.summary.active_count ?? 0;
  const completed = activity?.summary.completed_count ?? 0;
  const recent = activity?.summary.recent_count ?? 0;
  const latestWorkItem = latestProjectWorkItem(workItems);
  const assignmentCount = active + blocked + completed;
  const activityPending = loadState === "loading" && !activity;
  const activityUnavailable = loadState === "error" && !activity;
  const hasWork = workItemCount > 0 || assignmentCount > 0;
  const title = activityUnavailable
    ? "Activity unavailable"
    : activityPending
      ? "Updating activity…"
      : assignmentCount > 0
        ? `Assignments: ${active} active · ${blocked} blocked · ${completed} completed`
        : workItemCount > 0
          ? `${workItemCount} work item${workItemCount === 1 ? "" : "s"}`
          : "No project work yet";
  const detail = activityUnavailable
    ? "Refresh project work to try again."
    : activityPending
      ? "Checking assignment progress and blockers."
      : assignmentCount > 0
        ? "Review assignment progress and blockers in Work."
        : latestWorkItem
          ? `Latest update ${formatProjectRowRelativeTime(latestWorkItem.updated_at)}.`
          : workItemCount > 0
            ? "Project activity reports current work."
            : "Create a work item when there is something to coordinate.";

  return (
    <section
      aria-label="Project activity summary"
      className="project-activity-summary"
      style={projectResumeSummaryStyle}
    >
      <div
        aria-atomic="true"
        aria-busy={loadState === "loading"}
        aria-live="polite"
        className="project-activity-summary-copy"
        role="status"
        style={projectResumeCopyStyle}
      >
        <div style={sectionLabelStyle}>Activity</div>
        <div style={titleStyle}>{title}</div>
        <div style={subtleTextStyle}>{detail}</div>
      </div>
      {(activity || memoryCandidateCount > 0 || hasWork) && (
        <div className="project-activity-summary-actions" style={projectResumeStatsStyle}>
          {activity && !activityPending && !activityUnavailable && (
            <>
              <button
                className="btn btn-ghost btn-sm"
                type="button"
                onClick={() => onBucketChange("blocked")}
              >
                Blocked <span className="badge badge-muted">{blocked}</span>
              </button>
              <button
                className="btn btn-ghost btn-sm"
                type="button"
                onClick={() => onBucketChange("active")}
              >
                Active <span className="badge badge-muted">{active}</span>
              </button>
              <button
                className="btn btn-ghost btn-sm"
                type="button"
                onClick={() => onBucketChange("recent")}
              >
                Recent <span className="badge badge-muted">{recent}</span>
              </button>
            </>
          )}
          {memoryCandidateCount > 0 && (
            <button className="btn btn-ghost btn-sm" type="button" onClick={onReviewMemory}>
              Memory <span className="badge badge-muted">{memoryCandidateCount}</span>
            </button>
          )}
          {hasWork && (
            <button className="btn btn-ghost btn-sm" type="button" onClick={onViewWork}>
              View work
            </button>
          )}
        </div>
      )}
    </section>
  );
}

function ProjectWorkspaceTabs({
  activeTab,
  memoryCandidateCount,
  memoryEntryCount,
  onChange,
  projectSkillCount,
  workItemCount,
}: {
  activeTab: ProjectWorkspaceTab;
  memoryCandidateCount: number;
  memoryEntryCount: number;
  onChange: (tab: ProjectWorkspaceTab) => void;
  projectSkillCount: number;
  workItemCount: number;
}) {
  const tabs: Array<{ id: ProjectWorkspaceTab; label: string; count: number }> = [
    { id: "overview", label: "Overview", count: 0 },
    { id: "work", label: "Work", count: workItemCount },
    { id: "timeline", label: "Timeline", count: 0 },
    {
      id: "memory",
      label: "Memory",
      count: memoryEntryCount + memoryCandidateCount,
    },
    { id: "skills", label: "Skills", count: projectSkillCount },
  ];

  return (
    <div
      className="project-workspace-tabs"
      role="tablist"
      aria-label="Project workspace views"
      style={projectWorkspaceTabsStyle}
    >
      {tabs.map((tab) => (
        <button
          aria-controls={activeTab === tab.id ? `project-workspace-panel-${tab.id}` : undefined}
          aria-selected={activeTab === tab.id}
          key={tab.id}
          className={`project-workspace-tab ${
            activeTab === tab.id ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"
          }`}
          id={`project-workspace-tab-${tab.id}`}
          onClick={() => onChange(tab.id)}
          onKeyDown={(event) => onProjectWorkspaceTabKeyDown(event, tab.id, tabs, onChange)}
          role="tab"
          style={projectWorkspaceTabButtonStyle}
          tabIndex={activeTab === tab.id ? 0 : -1}
          type="button"
        >
          {tab.label}
          {tab.count > 0 && <span className="badge badge-muted">{tab.count}</span>}
        </button>
      ))}
    </div>
  );
}

function onProjectWorkspaceTabKeyDown(
  event: KeyboardEvent<HTMLButtonElement>,
  currentTab: ProjectWorkspaceTab,
  tabs: Array<{ id: ProjectWorkspaceTab }>,
  onChange: (tab: ProjectWorkspaceTab) => void,
) {
  const currentIndex = tabs.findIndex((tab) => tab.id === currentTab);
  let nextIndex = currentIndex;
  if (event.key === "ArrowRight") nextIndex = (currentIndex + 1) % tabs.length;
  else if (event.key === "ArrowLeft") nextIndex = (currentIndex - 1 + tabs.length) % tabs.length;
  else if (event.key === "Home") nextIndex = 0;
  else if (event.key === "End") nextIndex = tabs.length - 1;
  else return;

  event.preventDefault();
  const nextTab = tabs[nextIndex];
  if (!nextTab) return;
  onChange(nextTab.id);
  const tabButtons =
    event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]');
  tabButtons?.[nextIndex]?.focus();
}

function ProjectActivityInbox({
  activity,
  activityLoadState,
  bucket,
  onSelectWorkItem,
  project,
  roleByID,
  selectedWorkItemID,
  workItemSummaries,
  workItems,
  workLoadState,
}: {
  activity: ProjectActivityData | null;
  activityLoadState: LoadState;
  bucket: ProjectActivityBucketKey;
  onSelectWorkItem: (workItemID: string) => void;
  project: ProjectRecord | null;
  roleByID: Map<string, ProjectWorkRoleRecord>;
  selectedWorkItemID: string;
  workItemSummaries: Record<string, WorkItemSummary>;
  workItems: ProjectWorkItemRecord[];
  workLoadState: LoadState;
}) {
  const buckets = activity?.buckets;
  const activityPending = activityLoadState === "loading" && !activity;
  const activityUnavailable = activityLoadState === "error" && !activity;
  const showingAll = bucket === "all" || activityPending || activityUnavailable;
  const selectedItems = showingAll ? [] : (buckets?.[bucket] ?? []);
  const workLoading = workLoadState === "loading";
  const workUnavailable = workLoadState === "error";

  if (!project) {
    return null;
  }

  const selectedWorkItems = showingAll
    ? workItems
    : uniqueActivityWorkItems(project.id, selectedItems, workItems);

  return (
    <section aria-label="Work queue">
      <div style={panelStyle}>
        <div style={workItemListHeaderStyle}>
          <SectionHeader
            title="Work Items"
            detail={
              workLoading && workItems.length === 0 && !activity
                ? "Loading project work…"
                : undefined
            }
          />
        </div>
        {showingAll && workItems.length === 0 && !workLoading && !workUnavailable && (
          <div style={subtleTextStyle}>No work items for this project.</div>
        )}
        {showingAll && workItems.length === 0 && workUnavailable && (
          <div style={subtleTextStyle}>
            Work items are unavailable. Refresh project work to try again.
          </div>
        )}
        {selectedWorkItems.length > 0 && (
          <div style={workItemListStyle}>
            {selectedWorkItems.map((item) => (
              <WorkItemRow
                key={item.id}
                active={item.id === selectedWorkItemID}
                item={item}
                summary={
                  workItemSummaries[item.id] ??
                  summarizeAssignments(
                    selectedItems
                      .filter((activityItem) => activityItem.work_item.id === item.id)
                      .map((activityItem) => activityItem.assignment),
                  )
                }
                role={item.owner_role_id ? roleByID.get(item.owner_role_id) : undefined}
                onSelect={() => onSelectWorkItem(item.id)}
              />
            ))}
          </div>
        )}
        {!showingAll && !activity && !activityPending && !activityUnavailable && !workLoading && (
          <div style={subtleTextStyle}>No activity is recorded for this project yet.</div>
        )}
        {!showingAll && activity && selectedItems.length === 0 && (
          <div style={subtleTextStyle}>No {bucket} assignments for this project.</div>
        )}
      </div>
    </section>
  );
}

function ProjectActivityBucketTabs({
  activity,
  activityLoadState,
  bucket,
  onBucketChange,
  workItemCount,
}: {
  activity: ProjectActivityData | null;
  activityLoadState: LoadState;
  bucket: ProjectActivityBucketKey;
  onBucketChange: (bucket: ProjectActivityBucketKey) => void;
  workItemCount: number;
}) {
  const counts = activity?.summary;
  const activityPending = activityLoadState === "loading" && !activity;
  const activityUnavailable = activityLoadState === "error" && !activity;
  const activityReady = !activityPending && !activityUnavailable;
  const selectedBucket = activityReady ? bucket : "all";
  const tabs: Array<{
    id: ProjectActivityBucketKey;
    label: string;
    count?: number;
  }> = [
    { id: "all", label: "All", count: workItemCount },
    ...(activityReady
      ? [
          {
            id: "blocked" as const,
            label: "Blocked",
            count: counts?.blocked_count ?? 0,
          },
          {
            id: "active" as const,
            label: "Active",
            count: counts?.active_count ?? 0,
          },
          {
            id: "completed" as const,
            label: "Completed",
            count: counts?.completed_count ?? 0,
          },
          {
            id: "recent" as const,
            label: "Recent",
            count: counts?.recent_count ?? 0,
          },
        ]
      : []),
  ];

  return (
    <div style={activityHeaderTabsStyle} aria-label="Work activity filters">
      {tabs.map((tab) => (
        <button
          aria-pressed={selectedBucket === tab.id}
          key={tab.id}
          className={selectedBucket === tab.id ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"}
          type="button"
          aria-label={
            tab.id === "all" ? "Show all work items" : `Show ${tab.label.toLowerCase()} assignments`
          }
          onClick={() => onBucketChange(tab.id)}
          style={activityBucketButtonStyle}
        >
          {tab.label}
          {(activityReady || (tab.count ?? 0) > 0) && (
            <span className="badge badge-muted">{tab.count}</span>
          )}
        </button>
      ))}
      {(activityPending || activityUnavailable) && (
        <div aria-atomic="true" aria-live="polite" role="status" style={subtleTextStyle}>
          {activityPending
            ? "Updating assignment activity…"
            : "Assignment activity unavailable. Refresh project work to try again."}
        </div>
      )}
    </div>
  );
}

function uniqueActivityWorkItems(
  projectID: string,
  items: ProjectActivityItemRecord[],
  loadedWorkItems: ProjectWorkItemRecord[],
): ProjectWorkItemRecord[] {
  const loadedByID = new Map(loadedWorkItems.map((item) => [item.id, item]));
  const seen = new Set<string>();
  const out: ProjectWorkItemRecord[] = [];
  for (const item of items) {
    if (seen.has(item.work_item.id)) continue;
    seen.add(item.work_item.id);
    out.push(
      loadedByID.get(item.work_item.id) ??
        projectActivityWorkItemToWorkItem(projectID, item.work_item),
    );
  }
  return out;
}

function latestProjectWorkItem(items: ProjectWorkItemRecord[]): ProjectWorkItemRecord | null {
  if (items.length === 0) return null;
  return [...items].sort((left, right) => {
    const leftTime = Date.parse(left.updated_at || left.created_at || "");
    const rightTime = Date.parse(right.updated_at || right.created_at || "");
    return (
      (Number.isFinite(rightTime) ? rightTime : 0) - (Number.isFinite(leftTime) ? leftTime : 0)
    );
  })[0];
}

export function ProjectEmptyBlock({ title, detail }: { title: string; detail: string }) {
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

export function summarizeAssignments(assignments: ProjectAssignmentRecord[]): WorkItemSummary {
  return assignments.reduce<WorkItemSummary>(
    (summary, assignment) => {
      const status = toProjectAssignmentExecutionViewModel(assignment).status;
      summary.assignmentCount += 1;
      if (status === "running" || status === "queued" || status === "awaiting_approval") {
        summary.activeCount += 1;
      }
      if (status === "failed") summary.failedCount += 1;
      if (status === "completed") summary.completedCount += 1;
      return summary;
    },
    { assignmentCount: 0, activeCount: 0, failedCount: 0, completedCount: 0 },
  );
}

const detailStyle: CSSProperties = {
  flex: 1,
  minWidth: 0,
  minHeight: 0,
  overflow: "auto",
  background: "var(--bg0)",
  display: "grid",
  alignContent: "start",
};

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
  color: "var(--t2)",
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

const domainSectionStyle: CSSProperties = {
  display: "grid",
  gap: 10,
  minWidth: 0,
};

const domainHeaderStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "flex",
  gap: 10,
  justifyContent: "space-between",
  minWidth: 0,
};

const domainHeaderActionsStyle: CSSProperties = {
  display: "flex",
  flexShrink: 0,
  gap: 8,
};

const cockpitWorkspaceStyle: CSSProperties = {
  display: "grid",
  gap: 14,
  alignItems: "start",
  minWidth: 0,
  padding: 14,
};

const projectEmptyStateStyle: CSSProperties = {
  alignItems: "center",
  display: "grid",
  minHeight: "min(520px, calc(100vh - 160px))",
  placeItems: "center",
};

const projectSetupErrorStyle: CSSProperties = {
  ...panelStyle,
  display: "grid",
  gap: 14,
  margin: "clamp(12px, 3vw, 32px) auto",
  maxWidth: 680,
  padding: 20,
};

const projectOnboardingStyle: CSSProperties = {
  ...panelStyle,
  alignItems: "start",
  display: "grid",
  gap: 18,
  gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 280px), 1fr))",
  minHeight: "min(420px, calc(100vh - 180px))",
  padding: 20,
};

const projectOnboardingCopyStyle: CSSProperties = {
  alignContent: "center",
  display: "grid",
  gap: 16,
  minHeight: 260,
  minWidth: 0,
};

const projectOnboardingTitleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 20,
  fontWeight: 650,
  lineHeight: 1.2,
  marginTop: 8,
};

const projectOnboardingDetailStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 13,
  lineHeight: 1.45,
  maxWidth: 520,
};

const projectOnboardingActionsStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
};

const projectOnboardingChecklistStyle: CSSProperties = {
  display: "grid",
  gap: 8,
  minWidth: 0,
};

const projectOnboardingCheckStyle: CSSProperties = {
  alignItems: "center",
  background: "var(--bg0)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 10,
  gridTemplateColumns: "auto minmax(0, 1fr) auto",
  minWidth: 0,
  padding: 10,
};

const projectOnboardingCheckBadgeStyle: CSSProperties = {
  justifySelf: "start",
  textTransform: "uppercase",
};

const projectOnboardingCheckActionStyle: CSSProperties = {
  justifySelf: "end",
  whiteSpace: "nowrap",
};

const projectWorkspaceTabsStyle: CSSProperties = {
  alignItems: "center",
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: 11,
  boxSizing: "border-box",
  display: "grid",
  gap: 2,
  gridTemplateColumns: "repeat(5, minmax(104px, 1fr))",
  justifySelf: "start",
  maxWidth: "min(100%, 760px)",
  minWidth: 0,
  overflowX: "auto",
  overflowY: "hidden",
  padding: 2,
  width: "100%",
};

const projectWorkspaceTabButtonStyle: CSSProperties = {
  justifyContent: "center",
  minHeight: 32,
  minWidth: 0,
  whiteSpace: "nowrap",
};

const projectTabPanelStyle: CSSProperties = {
  display: "grid",
  gap: 10,
  minWidth: 0,
};

const workActivityPanelStyle: CSSProperties = {
  ...panelStyle,
  display: "grid",
  gap: 10,
};

const projectOperationsBriefStyle: CSSProperties = {
  ...panelStyle,
  alignItems: "center",
  display: "grid",
  gap: 12,
  gridTemplateColumns: "minmax(0, 1fr) auto",
};

const projectOperationsBriefMainStyle: CSSProperties = {
  display: "grid",
  gap: 5,
  minWidth: 0,
};

const projectOperationsTitleStyle: CSSProperties = {
  ...titleStyle,
  overflow: "visible",
  overflowWrap: "anywhere",
  textOverflow: "clip",
  whiteSpace: "normal",
};

const projectOperationsBriefControlsStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  justifyContent: "flex-end",
  minWidth: 0,
};

const projectOperationsListStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  display: "grid",
  gap: 4,
  gridColumn: "1 / -1",
  paddingTop: 8,
};

const projectOperationsItemButtonStyle: CSSProperties = {
  display: "grid",
  gap: 8,
  gridTemplateColumns: "auto minmax(0, 1fr) auto",
  justifyContent: "stretch",
  minHeight: 32,
  textAlign: "left",
};

const projectOperationsItemTitleStyle: CSSProperties = {
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const projectOperationsItemActionStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 11,
  whiteSpace: "nowrap",
};

const projectResumeSummaryStyle: CSSProperties = {
  ...panelStyle,
  alignItems: "center",
  display: "grid",
  gap: 12,
  gridTemplateColumns: "minmax(0, 1fr) auto",
};

const projectResumeCopyStyle: CSSProperties = {
  display: "grid",
  gap: 5,
  minWidth: 0,
};

const projectResumeStatsStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 6,
  justifyContent: "flex-end",
  minWidth: 0,
};

const workCoordinationGridStyle: CSSProperties = {
  display: "grid",
  gap: 14,
  alignItems: "start",
  minWidth: 0,
};

const workItemListHeaderStyle: CSSProperties = {
  marginBottom: 10,
  minWidth: 0,
};

const workItemListStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  display: "grid",
  margin: "0 -12px -12px",
  maxHeight: 520,
  overflowY: "auto",
};

const workDetailColumnStyle: CSSProperties = {
  boxSizing: "border-box",
  maxWidth: "100%",
  minWidth: 0,
  overflow: "hidden",
};

const activityHeaderTabsStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 6,
  minWidth: 0,
};

const activityBucketButtonStyle: CSSProperties = {
  justifyContent: "center",
  minHeight: 34,
  minWidth: 92,
};

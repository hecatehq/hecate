import type { CSSProperties, ReactNode } from "react";

import type {
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectAssignmentRecord,
  ProjectCollaborationArtifactRecord,
  ProjectContextSourceRecord,
  ProjectHandoffRecord,
  ProjectMemoryCandidateRecord,
  ProjectMemoryRecord,
  ProjectRecord,
  ProjectSkillRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
  UpdateProjectSkillPayload,
} from "../../types/project";
import { projectDefaultWorkspace } from "../../lib/project-workspace";
import { Badge, Icon, Icons, InlineError } from "../shared/ui";
import { ProjectAssistantPanel } from "./ProjectAssistantPanel";
import { ProjectMemoryPanel } from "./ProjectMemoryPanel";
import { ProjectSkillsPanel } from "./ProjectSkillsPanel";
import { ProjectTimelinePanel } from "./ProjectTimelinePanel";
import {
  ProjectWorkItemDetail,
  type ProjectAssignmentChatLaunchRequest,
} from "./ProjectWorkItemDetail";
import { toProjectAssignmentExecutionViewModel } from "./projectAssignmentViewModels";
import { workStatusLabel } from "./projectDisplay";
import {
  projectActivityWorkItemToWorkItem,
  type ProjectActivityBucketKey,
} from "./projectInsights";
import { useProjectAssistantController } from "./useProjectAssistantController";

export type WorkItemSummary = {
  assignmentCount: number;
  activeCount: number;
  failedCount: number;
  completedCount: number;
};

export type LoadState = "idle" | "loading" | "loaded" | "error";

export type ProjectWorkspaceTab = "work" | "timeline" | "memory" | "skills";

export type ProjectWorkspaceViewProps = {
  activity: ProjectActivityData | null;
  activityBucket: ProjectActivityBucketKey;
  activityByAssignmentID: Map<string, ProjectActivityItemRecord>;
  artifacts: ProjectCollaborationArtifactRecord[];
  artifactActionID: string;
  assignmentErrors: Record<string, string>;
  assignments: ProjectAssignmentRecord[];
  assistant: ReturnType<typeof useProjectAssistantController>;
  creatingDefaultAssignment: boolean;
  detailError: string;
  detailLoadState: LoadState;
  discoveringContext: boolean;
  discoveringSkills: boolean;
  handoffActionID: string;
  handoffError: string;
  handoffs: ProjectHandoffRecord[];
  hasWorkItemDetail: boolean;
  memoryCandidates: ProjectMemoryCandidateRecord[];
  memoryEntries: ProjectMemoryRecord[];
  memoryError: string;
  memoryLoadState: LoadState;
  onActivityBucketChange: (bucket: ProjectActivityBucketKey) => void;
  onAddAssignment: () => void;
  onAddEvidenceLink: () => void;
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
  onCreateDefaultAssignment: (item: ProjectWorkItemRecord) => void;
  onPreparedAssignmentPreflightOpened: (assignmentID: string) => void;
  onCreateAssignmentFromReviewArtifact: (artifact: ProjectCollaborationArtifactRecord) => void;
  onCreateAssignmentFromHandoff: (handoff: ProjectHandoffRecord) => void;
  onCreateWork: () => void;
  onCloseWorkItem: (item: ProjectWorkItemRecord) => void;
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
  onManageProfiles: () => void;
  onManageRoles: () => void;
  onNewMemory: () => void;
  onNewSource: () => void;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenConnections?: () => void;
  onOpenSettings: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onPromoteCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onRefreshMemory: () => void;
  onRefreshProjectSkills: () => void;
  onRefreshWorkItem: () => void;
  onRejectCandidate: (candidate: ProjectMemoryCandidateRecord) => void | Promise<void>;
  onSelectWorkItem: (workItemID: string) => void;
  onSetHandoffStatus: (handoff: ProjectHandoffRecord, status: string) => void;
  onStartAssignment: (assignment: ProjectAssignmentRecord) => void;
  onStartHandoff: (handoff: ProjectHandoffRecord) => void;
  onUpdateProjectSkill: (skill: ProjectSkillRecord, patch: UpdateProjectSkillPayload) => void;
  onWorkspaceTabChange: (tab: ProjectWorkspaceTab) => void;
  project: ProjectRecord | null;
  projectEmptyDetail: string;
  projectEmptyTitle: string;
  projectNeedsOnboarding: boolean;
  projectSkills: ProjectSkillRecord[];
  preparingAssignmentID: string;
  rejectingCandidateID: string;
  roleByID: Map<string, ProjectWorkRoleRecord>;
  roles: ProjectWorkRoleRecord[];
  selectedWorkItem: ProjectWorkItemRecord | null;
  selectedWorkItemID: string;
  closingWorkItemID: string;
  skillsError: string;
  skillsLoadState: LoadState;
  startingAssignmentID: string;
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
  artifacts,
  artifactActionID,
  assignmentErrors,
  assignments,
  assistant,
  creatingDefaultAssignment,
  detailError,
  detailLoadState,
  discoveringContext,
  discoveringSkills,
  handoffActionID,
  handoffError,
  handoffs,
  hasWorkItemDetail,
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
  onCreateDefaultAssignment,
  onPreparedAssignmentPreflightOpened,
  onCreateAssignmentFromReviewArtifact,
  onCreateAssignmentFromHandoff,
  onCreateWork,
  onCloseWorkItem,
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
  onManageProfiles,
  onManageRoles,
  onNewMemory,
  onNewSource,
  onOpenChat,
  onOpenConnections,
  onOpenSettings,
  onOpenTask,
  onPromoteCandidate,
  onRefreshMemory,
  onRefreshProjectSkills,
  onRefreshWorkItem,
  onRejectCandidate,
  onSelectWorkItem,
  onSetHandoffStatus,
  onStartAssignment,
  onStartHandoff,
  onUpdateProjectSkill,
  onWorkspaceTabChange,
  project,
  projectEmptyDetail,
  projectEmptyTitle,
  projectNeedsOnboarding,
  projectSkills,
  preparingAssignmentID,
  rejectingCandidateID,
  roleByID,
  roles,
  selectedWorkItem,
  selectedWorkItemID,
  closingWorkItemID,
  skillsError,
  skillsLoadState,
  startingAssignmentID,
  updatingSkillID,
  workError,
  workItemSummaries,
  workItems,
  workLoadState,
  workspaceTab,
}: ProjectWorkspaceViewProps) {
  const enabledContextSourceCount =
    project?.context_sources?.filter((source) => source.enabled).length ?? 0;
  const projectSetupStarted =
    enabledContextSourceCount > 0 ||
    roles.length > 0 ||
    projectSkills.length > 0 ||
    memoryEntries.length > 0 ||
    memoryCandidates.length > 0;
  const projectSetupFirst = workItems.length === 0 && !selectedWorkItem;

  return (
    <section style={detailStyle} aria-label="Selected work item">
      <div className="project-cockpit-workspace" style={cockpitWorkspaceStyle}>
        {project ? (
          <section style={domainSectionStyle} aria-label="Project workspace">
            {projectNeedsOnboarding ? (
              <ProjectOnboardingPanel
                bootstrapPending={assistant.bootstrapPending}
                contextSourceCount={enabledContextSourceCount}
                onBootstrap={() => void assistant.bootstrap()}
                onCreateWork={onCreateWork}
                onOpenSettings={onOpenSettings}
                project={project}
                roleCount={roles.length}
                skillCount={projectSkills.length}
              />
            ) : (
              <>
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
                  onInspectContext={(form) => void assistant.inspectContext(form)}
                  onDismiss={assistant.dismiss}
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
                  project={project}
                  proposal={assistant.proposal}
                  roles={roles}
                  setupFirst={projectSetupFirst}
                  setupStarted={projectSetupStarted}
                  status={assistant.status}
                  workItem={selectedWorkItem}
                />
                <ProjectWorkspaceTabs
                  activeTab={workspaceTab}
                  memoryCandidateCount={memoryCandidates.length}
                  memoryEntryCount={memoryEntries.length}
                  onChange={onWorkspaceTabChange}
                  projectSkillCount={projectSkills.length}
                  workItemCount={workItems.length}
                />
              </>
            )}
            {!projectNeedsOnboarding && workspaceTab === "work" && (
              <section style={projectTabPanelStyle} aria-label="Work coordination">
                <section aria-label="Work activity" style={workActivityPanelStyle}>
                  <SectionHeader
                    title="Work Queue"
                    detail={
                      workLoadState === "loading" && workItems.length === 0
                        ? "Loading project work..."
                        : undefined
                    }
                    actions={
                      <button
                        className="btn btn-primary btn-sm"
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
                    bucket={activityBucket}
                    onBucketChange={onActivityBucketChange}
                    workItemCount={workItems.length}
                  />
                </section>
                {workError && <InlineError message={workError} />}
                <div className="project-work-coordination-grid" style={workCoordinationGridStyle}>
                  <ProjectActivityInbox
                    activity={activity}
                    bucket={activityBucket}
                    loading={workLoadState === "loading"}
                    onSelectWorkItem={onSelectWorkItem}
                    project={project}
                    roleByID={roleByID}
                    selectedWorkItemID={selectedWorkItemID}
                    workItemSummaries={workItemSummaries}
                    workItems={workItems}
                  />
                  <div style={workDetailColumnStyle}>
                    {hasWorkItemDetail ? (
                      <ProjectWorkItemDetail
                        assignments={assignments}
                        artifacts={artifacts}
                        artifactActionID={artifactActionID}
                        handoffActionID={handoffActionID}
                        handoffError={handoffError}
                        handoffs={handoffs}
                        assignmentErrors={assignmentErrors}
                        detailError={detailError}
                        creatingDefaultAssignment={creatingDefaultAssignment}
                        preparingAssignmentID={preparingAssignmentID}
                        loading={detailLoadState === "loading"}
                        onOpenTask={onOpenTask}
                        onRefresh={onRefreshWorkItem}
                        onCreateAssignmentFromHandoff={onCreateAssignmentFromHandoff}
                        activityByAssignmentID={activityByAssignmentID}
                        onDeleteHandoff={onDeleteHandoff}
                        onDeleteWorkItem={onDeleteWorkItem}
                        onCloseWorkItem={onCloseWorkItem}
                        onEditHandoff={onEditHandoff}
                        onEditAssignment={onEditAssignment}
                        onEditWorkItem={onEditWorkItem}
                        onDeleteAssignment={onDeleteAssignment}
                        onManageProfiles={onManageProfiles}
                        onManageRoles={onManageRoles}
                        onOpenChat={onOpenChat}
                        onOpenConnections={onOpenConnections}
                        onOpenSettings={onOpenSettings}
                        onStartAssignment={onStartAssignment}
                        onStartHandoff={onStartHandoff}
                        onSetHandoffStatus={onSetHandoffStatus}
                        project={project}
                        roleByID={roleByID}
                        closingWorkItemID={closingWorkItemID}
                        startingAssignmentID={startingAssignmentID}
                        workItem={selectedWorkItem}
                        onAddAssignment={onAddAssignment}
                        onAddEvidenceLink={onAddEvidenceLink}
                        onAddHandoff={onAddHandoff}
                        onAddHandoffFromAssignment={onAddHandoffFromAssignment}
                        onAddReviewHandoffFromAssignment={onAddReviewHandoffFromAssignment}
                        onAddReviewArtifactFromAssignment={onAddReviewArtifactFromAssignment}
                        onAddHandoffFromReviewArtifact={onAddHandoffFromReviewArtifact}
                        onCreateDefaultAssignment={onCreateDefaultAssignment}
                        onPreparedAssignmentPreflightOpened={onPreparedAssignmentPreflightOpened}
                        onCreateAssignmentFromReviewArtifact={onCreateAssignmentFromReviewArtifact}
                      />
                    ) : (
                      <ProjectEmptyBlock
                        title={
                          workLoadState === "loading" ? "Loading detail..." : "No work selected"
                        }
                        detail="Create or select a work item to manage assignments and collaboration artifacts."
                      />
                    )}
                  </div>
                </div>
              </section>
            )}
            {!projectNeedsOnboarding && workspaceTab === "timeline" && (
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
            )}
            {!projectNeedsOnboarding && workspaceTab === "memory" && (
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
            )}
            {!projectNeedsOnboarding && workspaceTab === "skills" && (
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
      <div style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: 6 }}>
        <Badge status={item.status} label={workStatusLabel(item.status)} />
        <span className="badge badge-muted">{item.priority}</span>
        {summary && summary.assignmentCount > 0 && (
          <span className="badge badge-muted">
            {summary.assignmentCount} assignment{summary.assignmentCount === 1 ? "" : "s"}
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
  title,
}: {
  actions?: ReactNode;
  detail?: string;
  title: string;
}) {
  return (
    <div style={domainHeaderStyle}>
      <div style={{ minWidth: 0 }}>
        <div style={sectionLabelStyle}>{title}</div>
        {detail && <div style={{ ...subtleTextStyle, marginTop: 3 }}>{detail}</div>}
      </div>
      {actions && <div style={domainHeaderActionsStyle}>{actions}</div>}
    </div>
  );
}

function ProjectOnboardingPanel({
  bootstrapPending,
  contextSourceCount,
  onBootstrap,
  onCreateWork,
  onOpenSettings,
  project,
  roleCount,
  skillCount,
}: {
  bootstrapPending: boolean;
  contextSourceCount: number;
  onBootstrap: () => void;
  onCreateWork: () => void;
  onOpenSettings: () => void;
  project: ProjectRecord;
  roleCount: number;
  skillCount: number;
}) {
  const hasRoot = project.roots.some((root) => root.active !== false && root.path);
  const workspace = projectDefaultWorkspace(project);
  const hasPurpose = Boolean(project.description?.trim());
  const hasDefaults = Boolean(project.default_provider && project.default_model);
  const hasGuidance = contextSourceCount > 0 || skillCount > 0;
  const checks = [
    {
      label: "Project purpose",
      detail: hasPurpose ? project.description?.trim() || "Ready" : "Add a short purpose.",
      done: hasPurpose,
    },
    {
      label: "Workspace source",
      detail: hasRoot
        ? workspace || "Ready"
        : "Optional; attach files when this project needs them.",
      done: true,
      optional: !hasRoot,
    },
    {
      label: "Provider and model",
      detail: hasDefaults ? `${project.default_provider} / ${project.default_model}` : "Not set",
      done: hasDefaults,
    },
    {
      label: "Sources and memory",
      detail: hasGuidance
        ? `${contextSourceCount} sources · ${skillCount} skills`
        : hasRoot
          ? "Setup can discover workspace guidance and local skills."
          : "Add sources manually, or attach a workspace when files matter.",
      done: hasGuidance,
    },
    {
      label: "Roles",
      detail: roleCount > 0 ? `${roleCount} roles` : "Setup can suggest roles from skills.",
      done: roleCount > 0,
    },
    {
      label: "First work item",
      detail: "Create the first reviewable task after setup.",
      done: false,
    },
  ];
  return (
    <section aria-label="Project onboarding" style={projectOnboardingStyle}>
      <div style={projectOnboardingCopyStyle}>
        <div>
          <div style={sectionLabelStyle}>Project Onboarding</div>
          <div style={projectOnboardingTitleStyle}>Set up {project.name}</div>
        </div>
        <div style={projectOnboardingDetailStyle}>
          Let Hecate discover safe project metadata, suggest roles, and prepare setup actions for
          review. Attach local files only when this project needs a workspace.
        </div>
        <div style={projectOnboardingActionsStyle}>
          <button
            className="btn btn-primary btn-sm"
            type="button"
            disabled={bootstrapPending}
            onClick={onBootstrap}
          >
            <Icon d={Icons.refresh} size={13} />
            {bootstrapPending ? "Setting up..." : "Set up project"}
          </button>
          <button className="btn btn-ghost btn-sm" type="button" onClick={onCreateWork}>
            <Icon d={Icons.plus} size={13} />
            Create work
          </button>
          <button className="btn btn-ghost btn-sm" type="button" onClick={onOpenSettings}>
            <Icon d={Icons.settings} size={13} />
            Project settings
          </button>
        </div>
      </div>
      <div style={projectOnboardingChecklistStyle}>
        {checks.map((check) => (
          <div key={check.label} style={projectOnboardingCheckStyle}>
            <span
              className={check.done ? "badge badge-green" : "badge badge-muted"}
              style={projectOnboardingCheckBadgeStyle}
            >
              {check.optional ? "optional" : check.done ? "ready" : "todo"}
            </span>
            <div style={{ minWidth: 0 }}>
              <div style={titleStyle}>{check.label}</div>
              <div style={subtleTextStyle}>{check.detail}</div>
            </div>
          </div>
        ))}
      </div>
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
    { id: "work", label: "Work Coordination", count: workItemCount },
    { id: "timeline", label: "Timeline / Decision Log", count: 0 },
    { id: "memory", label: "Memory / Context", count: memoryEntryCount + memoryCandidateCount },
    { id: "skills", label: "Skills", count: projectSkillCount },
  ];

  return (
    <div role="tablist" aria-label="Project workspace views" style={projectWorkspaceTabsStyle}>
      {tabs.map((tab) => (
        <button
          key={tab.id}
          className={activeTab === tab.id ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"}
          type="button"
          role="tab"
          aria-selected={activeTab === tab.id}
          onClick={() => onChange(tab.id)}
          style={projectWorkspaceTabButtonStyle}
        >
          {tab.label}
          {tab.count > 0 && <span className="badge badge-muted">{tab.count}</span>}
        </button>
      ))}
    </div>
  );
}

function ProjectActivityInbox({
  activity,
  bucket,
  loading,
  onSelectWorkItem,
  project,
  roleByID,
  selectedWorkItemID,
  workItemSummaries,
  workItems,
}: {
  activity: ProjectActivityData | null;
  bucket: ProjectActivityBucketKey;
  loading: boolean;
  onSelectWorkItem: (workItemID: string) => void;
  project: ProjectRecord | null;
  roleByID: Map<string, ProjectWorkRoleRecord>;
  selectedWorkItemID: string;
  workItemSummaries: Record<string, WorkItemSummary>;
  workItems: ProjectWorkItemRecord[];
}) {
  const buckets = activity?.buckets;
  const showingAll = bucket === "all";
  const selectedItems = showingAll ? [] : (buckets?.[bucket] ?? []);

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
              loading && workItems.length === 0 && !activity ? "Loading project work..." : undefined
            }
          />
        </div>
        {showingAll && workItems.length === 0 && !loading && (
          <div style={subtleTextStyle}>No work items for this project.</div>
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
        {!showingAll && !activity && !loading && (
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
  bucket,
  onBucketChange,
  workItemCount,
}: {
  activity: ProjectActivityData | null;
  bucket: ProjectActivityBucketKey;
  onBucketChange: (bucket: ProjectActivityBucketKey) => void;
  workItemCount: number;
}) {
  const counts = activity?.summary;
  const tabs: Array<{ id: ProjectActivityBucketKey; label: string; count: number }> = [
    { id: "all", label: "All", count: workItemCount },
    { id: "blocked", label: "Blocked", count: counts?.blocked_count ?? 0 },
    { id: "active", label: "Active", count: counts?.active_count ?? 0 },
    { id: "completed", label: "Completed", count: counts?.completed_count ?? 0 },
    { id: "recent", label: "Recent", count: counts?.recent_count ?? 0 },
  ];

  return (
    <div style={activityHeaderTabsStyle} aria-label="Work activity filters">
      {tabs.map((tab) => (
        <button
          key={tab.id}
          className={bucket === tab.id ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"}
          type="button"
          aria-label={
            tab.id === "all" ? "Show all work items" : `Show ${tab.label.toLowerCase()} assignments`
          }
          onClick={() => onBucketChange(tab.id)}
          style={activityBucketButtonStyle}
        >
          {tab.label}
          <span className="badge badge-muted">{tab.count}</span>
        </button>
      ))}
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

export function ProjectEmptyBlock({ title, detail }: { title: string; detail: string }) {
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
  gridTemplateColumns: "auto minmax(0, 1fr)",
  minWidth: 0,
  padding: 10,
};

const projectOnboardingCheckBadgeStyle: CSSProperties = {
  justifySelf: "start",
  textTransform: "uppercase",
};

const projectWorkspaceTabsStyle: CSSProperties = {
  alignItems: "center",
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: 11,
  boxSizing: "border-box",
  display: "grid",
  gap: 2,
  gridTemplateColumns: "repeat(4, minmax(148px, 1fr))",
  justifySelf: "start",
  maxWidth: "min(100%, 920px)",
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

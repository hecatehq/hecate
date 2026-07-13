import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";

import { useProjects } from "../../app/state/projects";
import { useProvidersAndModels } from "../../app/state/providersAndModels";
import { useSettings } from "../../app/state/settings";
import {
  ApiError,
  chooseWorkspaceDirectory,
  createAgentPreset,
  createProjectAssignment,
  createProjectCollaborationArtifact,
  createProjectHandoff,
  createProjectContextSource,
  createProjectRoot,
  discoverProjectContextSources,
  discoverProjectRoots,
  discoverProjectSkills,
  createProjectWorktreeRoot,
  createProjectMemory,
  createProjectWorkRole,
  createProjectWorkItem,
  deleteProjectAssignment,
  deleteProjectHandoff,
  deleteProjectContextSource,
  deleteProjectMemory,
  deleteProjectRoot,
  deleteProjectWorkRole,
  deleteProjectWorkItem,
  getProjectActivity,
  getAgentPresets,
  getProjectAssignments,
  getProjectCollaborationArtifacts,
  getProjectHandoffs,
  getProjectHealth,
  getProjectMemory,
  getProjectMemoryCandidates,
  getProjectOperationsBrief,
  getProjectSetupReadiness,
  getProjectSkills,
  getProjectWorkItem,
  getProjectWorkItemReadiness,
  getProjectWorkItems,
  getProjectWorkRoles,
  startProjectAssignment,
  deleteAgentPreset,
  promoteProjectMemoryCandidate,
  rejectProjectMemoryCandidate,
  updateProject,
  updateAgentPreset,
  updateProjectContextSource,
  updateProjectRoot,
  updateProjectAssignment,
  updateProjectHandoff,
  updateProjectHandoffStatus,
  updateProjectMemory,
  updateProjectSkill,
  updateProjectWorkRole,
  updateProjectWorkItem,
} from "../../lib/api";
import { projectDefaultWorkspace } from "../../lib/project-workspace";
import {
  clearProjectAssistantChatHandoff,
  readProjectAssistantChatHandoff,
} from "../../lib/project-assistant-chat-handoff";
import { providerDisplayName } from "../../lib/provider-utils";
import { ChatRightPanel } from "../chats/ChatRightPanel";
import { EditAssignmentModal, NewAssignmentModal } from "./ProjectAssignmentModals";
import { CreateProjectModal } from "./CreateProjectModal";
import { CreateProjectWorktreeModal } from "./CreateProjectWorktreeModal";
import { ProjectHandoffModal } from "./ProjectHandoffModal";
import { ProjectHealthPanel } from "./ProjectHealthPanel";
import {
  routeProjectOperationAction,
  routeProjectSetupAction,
  type ProjectActionRoute,
} from "./projectActionRouting";
import { ProjectMemoryModal, ProjectSourceModal, type MemoryForm } from "./ProjectMemoryPanel";
import { ProjectReviewArtifactModal } from "./ProjectReviewArtifactModal";
import {
  buildProjectAssignmentChatLaunchRequest,
  type ProjectAssignmentChatLaunchRequest,
  type ProjectWorkItemFocusTarget,
} from "./ProjectWorkItemDetail";
import {
  ProjectEmptyBlock,
  ProjectWorkspaceView,
  summarizeAssignments,
  type LoadState,
  type ProjectWorkspaceTab,
  type WorkItemSummary,
} from "./ProjectWorkspaceView";
import { AgentPresetsModal } from "./AgentPresetsModal";
import { EditWorkItemModal, NewWorkItemModal } from "./ProjectWorkItemModals";
import { RolesModal } from "./RolesModal";
import { useProjectAssistantController } from "./useProjectAssistantController";
import type {
  ProjectActivityBucketKey,
  ProjectAssignmentRecord,
  ProjectActivityData,
  ProjectMemoryCandidateRecord,
  ProjectCollaborationArtifactRecord,
  CreateProjectWorktreeRootPayload,
  ProjectHandoffRecord,
  ProjectHealth,
  ProjectHealthAttention,
  ProjectMemoryRecord,
  ProjectOperationsBrief,
  ProjectOperationsBriefItem,
  ProjectSetupReadiness,
  ProjectContextSourceRecord,
  ProjectSkillRecord,
  ProjectRecord,
  ProjectWorkItemReadinessRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
  UpdateProjectPayload,
  UpdateProjectSkillPayload,
} from "../../types/project";
import type { AgentPresetRecord } from "../../types/agent-preset";
import { ConfirmModal, Icon, Icons, InlineError, type ProviderOption } from "../shared/ui";
import { ProjectSettingsPanel } from "./ProjectSettingsPanel";
import { ProjectEvidenceLinkModal } from "./ProjectEvidenceLinkModal";
import {
  createProjectPayloadFromForm,
  projectRootPayloadFromRecord,
  projectRootPayloadsEqual,
  type CreateProjectForm,
  type CreateWorktreeForm,
  type ProjectDefaultsForm,
} from "./projectSettings";
import {
  presetCreatePayloadFromForm,
  presetUpdatePayloadFromForm,
  projectSkillStatusRank,
  rolePayloadFromForm,
  type AgentPresetForm,
  type RoleForm,
} from "./projectPresetsRoles";
import { projectSourcePayloadFromForm, type ProjectSourceForm } from "./projectSources";
import {
  assignmentCreatePayloadFromForm,
  assignmentUpdatePayloadFromForm,
  evidenceLinkPayloadFromForm,
  handoffFormFromAssignment,
  handoffFormFromReviewArtifact,
  handoffPayloadFromForm,
  reviewArtifactFormFromAssignment,
  reviewArtifactPayloadFromForm,
  reviewHandoffFormFromAssignment,
  type EditAssignmentForm,
  type EditWorkItemForm,
  type EvidenceLinkForm,
  type HandoffForm,
  type NewAssignmentForm,
  type NewWorkItemForm,
  type ReviewArtifactForm,
  workItemCreatePayloadFromForm,
  workItemUpdatePayloadFromForm,
} from "./projectWorkForms";
import {
  formatProjectDeleteSummary,
  formatProjectRowRelativeTime,
  projectErrorMessage as errorMessage,
} from "./projectDisplay";
import {
  useProjectSelectionController,
  useStoredRightPanelWidth,
} from "./useProjectViewController";
import { PROJECT_ASSISTANT_AUTO } from "./ProjectAssistantPanel";

type Props = {
  initialWorkspaceTab?: ProjectWorkspaceTab;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenConnections?: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
};

const PROJECTS_LIST_PANEL_WIDTH = 220;

const shellStyle: CSSProperties = {
  display: "flex",
  height: "100%",
  minHeight: 0,
  background: "var(--bg0)",
};

const sidePanelStyle: CSSProperties = {
  width: PROJECTS_LIST_PANEL_WIDTH,
  borderRight: "1px solid var(--border)",
  background: "var(--bg1)",
  display: "flex",
  flexDirection: "column",
  minHeight: 0,
  flexShrink: 0,
};

const projectMainStyle: CSSProperties = {
  display: "flex",
  flex: 1,
  flexDirection: "column",
  minHeight: 0,
  minWidth: 0,
  overflow: "hidden",
};

const projectMainBodyStyle: CSSProperties = {
  display: "flex",
  flex: 1,
  minHeight: 0,
  minWidth: 0,
  overflow: "hidden",
};

export function ProjectsView({
  initialWorkspaceTab = "overview",
  onOpenChat,
  onOpenConnections,
  onOpenTask,
}: Props) {
  const projects = useProjects();
  const providersAndModels = useProvidersAndModels();
  const settings = useSettings();
  const [renamingProjectID, setRenamingProjectID] = useState("");
  const [renameValue, setRenameValue] = useState("");
  const [hoveredProjectID, setHoveredProjectID] = useState("");
  const [deleteProjectID, setDeleteProjectID] = useState("");
  const [deletePending, setDeletePending] = useState(false);
  const [createProjectOpen, setCreateProjectOpen] = useState(false);
  const [createProjectPending, setCreateProjectPending] = useState(false);
  const [createProjectError, setCreateProjectError] = useState("");
  const [settingsPanelOpen, setSettingsPanelOpen] = useState(false);
  const { rightPanelWidth, setRightPanelWidth } = useStoredRightPanelWidth();
  const [defaultsPending, setDefaultsPending] = useState(false);
  const [defaultsError, setDefaultsError] = useState("");
  const [discoveringRoots, setDiscoveringRoots] = useState(false);
  const [createWorktreeOpen, setCreateWorktreeOpen] = useState(false);
  const [createWorktreePending, setCreateWorktreePending] = useState(false);
  const [createWorktreeError, setCreateWorktreeError] = useState("");
  const [rolesModalOpen, setRolesModalOpen] = useState(false);
  const [rolesPending, setRolesPending] = useState(false);
  const [rolesError, setRolesError] = useState("");
  const [newWorkModalOpen, setNewWorkModalOpen] = useState(false);
  const [newWorkDraft, setNewWorkDraft] = useState<Partial<NewWorkItemForm> | undefined>();
  const [newWorkPending, setNewWorkPending] = useState(false);
  const [newWorkError, setNewWorkError] = useState("");
  const [editingWorkItem, setEditingWorkItem] = useState<ProjectWorkItemRecord | null>(null);
  const [editWorkPending, setEditWorkPending] = useState(false);
  const [editWorkError, setEditWorkError] = useState("");
  const [deleteWorkItem, setDeleteWorkItem] = useState<ProjectWorkItemRecord | null>(null);
  const [deleteWorkPending, setDeleteWorkPending] = useState(false);
  const [closingWorkItemID, setClosingWorkItemID] = useState("");
  const [newAssignmentModalOpen, setNewAssignmentModalOpen] = useState(false);
  const [newAssignmentPending, setNewAssignmentPending] = useState(false);
  const [newAssignmentError, setNewAssignmentError] = useState("");
  const [editingAssignment, setEditingAssignment] = useState<ProjectAssignmentRecord | null>(null);
  const [editAssignmentPending, setEditAssignmentPending] = useState(false);
  const [editAssignmentError, setEditAssignmentError] = useState("");
  const [deleteAssignment, setDeleteAssignment] = useState<ProjectAssignmentRecord | null>(null);
  const [deleteAssignmentPending, setDeleteAssignmentPending] = useState(false);
  const [workItems, setWorkItems] = useState<ProjectWorkItemRecord[]>([]);
  const [workItemSummaries, setWorkItemSummaries] = useState<Record<string, WorkItemSummary>>({});
  const [activity, setActivity] = useState<ProjectActivityData | null>(null);
  const [activityLoadState, setActivityLoadState] = useState<LoadState>("idle");
  const [projectHealth, setProjectHealth] = useState<ProjectHealth | null>(null);
  const [projectSetupReadiness, setProjectSetupReadiness] = useState<ProjectSetupReadiness | null>(
    null,
  );
  const [projectSetupReadinessLoadState, setProjectSetupReadinessLoadState] =
    useState<LoadState>("idle");
  const [projectSetupReadinessError, setProjectSetupReadinessError] = useState("");
  const [overviewProjectionError, setOverviewProjectionError] = useState("");
  const [operationsBrief, setOperationsBrief] = useState<ProjectOperationsBrief | null>(null);
  const [operationsBriefError, setOperationsBriefError] = useState("");
  const [operationsBriefLoadState, setOperationsBriefLoadState] = useState<LoadState>("idle");
  const [activityBucket, setActivityBucket] = useState<ProjectActivityBucketKey>("all");
  const [workspaceTab, setWorkspaceTab] = useState<ProjectWorkspaceTab>(initialWorkspaceTab);
  const [workspaceTabFocusTarget, setWorkspaceTabFocusTarget] =
    useState<ProjectWorkspaceTab | null>(null);
  const [roles, setRoles] = useState<ProjectWorkRoleRecord[]>([]);
  const [selectedWorkItemID, setSelectedWorkItemID] = useState("");
  const [workItemFocusTarget, setWorkItemFocusTarget] = useState<ProjectWorkItemFocusTarget | null>(
    null,
  );
  const [selectedWorkItemOperationID, setSelectedWorkItemOperationID] = useState("");
  const [selectedWorkItem, setSelectedWorkItem] = useState<ProjectWorkItemRecord | null>(null);
  const [selectedWorkItemReadiness, setSelectedWorkItemReadiness] =
    useState<ProjectWorkItemReadinessRecord | null>(null);
  const [assignments, setAssignments] = useState<ProjectAssignmentRecord[]>([]);
  const [artifacts, setArtifacts] = useState<ProjectCollaborationArtifactRecord[]>([]);
  const [handoffs, setHandoffs] = useState<ProjectHandoffRecord[]>([]);
  const [editingHandoff, setEditingHandoff] = useState<ProjectHandoffRecord | "new" | null>(null);
  const [newHandoffDraft, setNewHandoffDraft] = useState<HandoffForm | null>(null);
  const [handoffPending, setHandoffPending] = useState(false);
  const [handoffError, setHandoffError] = useState("");
  const [handoffActionID, setHandoffActionID] = useState("");
  const [artifactActionID, setArtifactActionID] = useState("");
  const [evidenceLinkModalOpen, setEvidenceLinkModalOpen] = useState(false);
  const [evidenceLinkAssignmentID, setEvidenceLinkAssignmentID] = useState("");
  const [evidenceLinkPending, setEvidenceLinkPending] = useState(false);
  const [evidenceLinkError, setEvidenceLinkError] = useState("");
  const [reviewArtifactDraft, setReviewArtifactDraft] = useState<ReviewArtifactForm | null>(null);
  const [reviewArtifactPending, setReviewArtifactPending] = useState(false);
  const [reviewArtifactError, setReviewArtifactError] = useState("");
  const [workLoadState, setWorkLoadState] = useState<LoadState>("idle");
  const [loadedProjectID, setLoadedProjectID] = useState("");
  const [detailLoadState, setDetailLoadState] = useState<LoadState>("idle");
  const [detailTarget, setDetailTarget] = useState<{
    projectID: string;
    workItemID: string;
  } | null>(null);
  const [workError, setWorkError] = useState("");
  const [detailError, setDetailError] = useState("");
  const [assignmentErrors, setAssignmentErrors] = useState<Record<string, string>>({});
  const [startingAssignmentKeys, setStartingAssignmentKeys] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const [preparingAssignmentTarget, setPreparingAssignmentTarget] = useState<{
    assignmentID: string;
    projectID: string;
    workItemID: string;
  } | null>(null);
  const startingAssignmentPromisesRef = useRef<Map<string, Promise<boolean>>>(new Map());
  const [memoryEntries, setMemoryEntries] = useState<ProjectMemoryRecord[]>([]);
  const [memoryCandidates, setMemoryCandidates] = useState<ProjectMemoryCandidateRecord[]>([]);
  const [projectSkills, setProjectSkills] = useState<ProjectSkillRecord[]>([]);
  const [skillsLoadState, setSkillsLoadState] = useState<LoadState>("idle");
  const [skillsError, setSkillsError] = useState("");
  const [discoveringSkills, setDiscoveringSkills] = useState(false);
  const [updatingSkillID, setUpdatingSkillID] = useState("");
  const [agentPresets, setAgentPresets] = useState<AgentPresetRecord[]>([]);
  const [agentPresetsError, setAgentPresetsError] = useState("");
  const [presetsModalOpen, setAgentPresetsModalOpen] = useState(false);
  const [presetsPending, setPresetsPending] = useState(false);
  const [presetsError, setPresetsError] = useState("");
  const [discoveringContext, setDiscoveringContext] = useState(false);
  const [memoryLoadState, setMemoryLoadState] = useState<LoadState>("idle");
  const [memoryError, setMemoryError] = useState("");
  const [editingMemory, setEditingMemory] = useState<ProjectMemoryRecord | "new" | null>(null);
  const [editingSource, setEditingSource] = useState<ProjectContextSourceRecord | "new" | null>(
    null,
  );
  const [sourcePending, setSourcePending] = useState(false);
  const [sourceError, setSourceError] = useState("");
  const [deleteSource, setDeleteSource] = useState<ProjectContextSourceRecord | null>(null);
  const [deleteSourcePending, setDeleteSourcePending] = useState(false);
  const [promotingCandidate, setPromotingCandidate] = useState<ProjectMemoryCandidateRecord | null>(
    null,
  );
  const [rejectingCandidateID, setRejectingCandidateID] = useState("");
  const [memoryPending, setMemoryPending] = useState(false);
  const [deleteMemory, setDeleteMemory] = useState<ProjectMemoryRecord | null>(null);
  const [deleteMemoryPending, setDeleteMemoryPending] = useState(false);
  const projectSelectionGenerationRef = useRef(0);
  const { clearSelectedProject, openProject, selectedProject, selectedProjectID } =
    useProjectSelectionController({
      activeProjectID: projects.activeProjectID,
      onProjectChange: () => {
        projectSelectionGenerationRef.current += 1;
        resetProjectScopedInteractions();
      },
      projects: projects.state.projects,
      selectProject: projects.actions.selectProject,
    });
  const selectedProjectIDRef = useRef(selectedProjectID);
  const selectedWorkItemIDRef = useRef(selectedWorkItemID);
  const workItemSelectionGenerationRef = useRef(0);
  const workLoadGenerationRef = useRef(0);
  const detailLoadGenerationRef = useRef(0);
  const memoryLoadGenerationRef = useRef(0);
  const skillsLoadGenerationRef = useRef(0);
  const overviewProjectionGenerationRef = useRef(0);
  selectedProjectIDRef.current = selectedProjectID;
  selectedWorkItemIDRef.current = selectedWorkItemID;

  useEffect(() => {
    workItemSelectionGenerationRef.current += 1;
    detailLoadGenerationRef.current += 1;
    setSelectedWorkItem(null);
    setSelectedWorkItemReadiness(null);
    setAssignments([]);
    setArtifacts([]);
    setHandoffs([]);
    setDetailError("");
    setDetailLoadState(selectedWorkItemID ? "loading" : "idle");
    setDetailTarget(
      selectedProjectID && selectedWorkItemID
        ? { projectID: selectedProjectID, workItemID: selectedWorkItemID }
        : null,
    );
    setEditingWorkItem(null);
    setEditWorkPending(false);
    setEditWorkError("");
    setDeleteWorkItem(null);
    setDeleteWorkPending(false);
    setClosingWorkItemID("");
    setNewAssignmentModalOpen(false);
    setNewAssignmentPending(false);
    setNewAssignmentError("");
    setEditingAssignment(null);
    setEditAssignmentPending(false);
    setEditAssignmentError("");
    setDeleteAssignment(null);
    setDeleteAssignmentPending(false);
    setEditingHandoff(null);
    setNewHandoffDraft(null);
    setHandoffPending(false);
    setHandoffError("");
    setHandoffActionID("");
    setArtifactActionID("");
    setEvidenceLinkModalOpen(false);
    setEvidenceLinkAssignmentID("");
    setEvidenceLinkPending(false);
    setEvidenceLinkError("");
    setReviewArtifactDraft(null);
    setReviewArtifactPending(false);
    setReviewArtifactError("");
    setAssignmentErrors({});
  }, [selectedProjectID, selectedWorkItemID]);

  useEffect(() => {
    setPreparingAssignmentTarget((current) => {
      if (!current) return null;
      return current.projectID === selectedProjectIDRef.current &&
        current.workItemID === selectedWorkItemID
        ? current
        : null;
    });
  }, [selectedProjectID, selectedWorkItemID]);

  function isCurrentProjectMutation(projectID: string, selectionGeneration: number) {
    return (
      selectionGeneration === projectSelectionGenerationRef.current &&
      selectedProjectIDRef.current === projectID
    );
  }

  function isCurrentWorkItemMutation(
    projectID: string,
    projectSelectionGeneration: number,
    workItemID: string,
    workItemSelectionGeneration: number,
  ) {
    return (
      isCurrentProjectMutation(projectID, projectSelectionGeneration) &&
      workItemSelectionGeneration === workItemSelectionGenerationRef.current &&
      selectedWorkItemIDRef.current === workItemID
    );
  }

  function resetProjectScopedInteractions() {
    setWorkspaceTab(initialWorkspaceTab);
    setWorkspaceTabFocusTarget(null);
    setSettingsPanelOpen(false);
    setDefaultsPending(false);
    setDefaultsError("");
    setDiscoveringRoots(false);
    setCreateWorktreeOpen(false);
    setCreateWorktreePending(false);
    setCreateWorktreeError("");
    setRolesModalOpen(false);
    setRolesPending(false);
    setRolesError("");
    setNewWorkModalOpen(false);
    setNewWorkDraft(undefined);
    setNewWorkPending(false);
    setNewWorkError("");
    setEditingWorkItem(null);
    setEditWorkPending(false);
    setEditWorkError("");
    setDeleteWorkItem(null);
    setDeleteWorkPending(false);
    setClosingWorkItemID("");
    setSelectedWorkItemID("");
    setWorkItemFocusTarget(null);
    setSelectedWorkItemOperationID("");
    setDetailTarget(null);
    setNewAssignmentModalOpen(false);
    setNewAssignmentPending(false);
    setNewAssignmentError("");
    setEditingAssignment(null);
    setEditAssignmentPending(false);
    setEditAssignmentError("");
    setDeleteAssignment(null);
    setDeleteAssignmentPending(false);
    setEditingHandoff(null);
    setNewHandoffDraft(null);
    setHandoffPending(false);
    setHandoffError("");
    setHandoffActionID("");
    setArtifactActionID("");
    setEvidenceLinkModalOpen(false);
    setEvidenceLinkAssignmentID("");
    setEvidenceLinkPending(false);
    setEvidenceLinkError("");
    setReviewArtifactDraft(null);
    setReviewArtifactPending(false);
    setReviewArtifactError("");
    setDiscoveringContext(false);
    setMemoryError("");
    setEditingMemory(null);
    setEditingSource(null);
    setSourcePending(false);
    setSourceError("");
    setDeleteSource(null);
    setDeleteSourcePending(false);
    setPromotingCandidate(null);
    setRejectingCandidateID("");
    setMemoryPending(false);
    setDeleteMemory(null);
    setDeleteMemoryPending(false);
    setDiscoveringSkills(false);
    setUpdatingSkillID("");
    setSkillsError("");
    setAssignmentErrors({});
    setPreparingAssignmentTarget(null);
  }

  const navigateWorkspaceTab = useCallback((tab: ProjectWorkspaceTab) => {
    setWorkspaceTab(tab);
    setWorkspaceTabFocusTarget(tab);
  }, []);

  const refreshProjectOverview = useCallback(async (projectID: string) => {
    if (!projectID || selectedProjectIDRef.current !== projectID) return false;
    const generation = ++overviewProjectionGenerationRef.current;
    const isStale = () =>
      generation !== overviewProjectionGenerationRef.current ||
      selectedProjectIDRef.current !== projectID;
    const markCoordinationUnavailable = () => {
      setOverviewProjectionError(
        "Project coordination status could not be refreshed. Use Refresh project work to try again.",
      );
    };
    setActivity(null);
    setActivityLoadState("loading");
    setProjectHealth(null);
    setProjectSetupReadiness(null);
    setProjectSetupReadinessLoadState("loading");
    setProjectSetupReadinessError("");
    setOverviewProjectionError("");
    setOperationsBrief(null);
    setOperationsBriefLoadState("loading");
    setOperationsBriefError("");
    let operationsLoaded = false;
    const activityLoad = getProjectActivity(projectID)
      .then((payload) => {
        if (isStale()) return;
        setActivity(payload.data ?? null);
        setActivityLoadState("loaded");
      })
      .catch(() => {
        if (isStale()) return;
        setActivity(null);
        setActivityLoadState("error");
        markCoordinationUnavailable();
      });
    const healthLoad = getProjectHealth(projectID)
      .then((payload) => {
        if (!isStale()) setProjectHealth(payload.data ?? null);
      })
      .catch(() => {
        if (isStale()) return;
        setProjectHealth(null);
        markCoordinationUnavailable();
      });
    const readinessLoad = getProjectSetupReadiness(projectID)
      .then((payload) => {
        if (isStale()) return;
        const readiness = payload.data ?? null;
        setProjectSetupReadiness(readiness);
        if (readiness) {
          setProjectSetupReadinessLoadState("loaded");
          return;
        }
        setProjectSetupReadinessError("Project setup status was unavailable.");
        setProjectSetupReadinessLoadState("error");
        markCoordinationUnavailable();
      })
      .catch((error) => {
        if (isStale()) return;
        setProjectSetupReadiness(null);
        setProjectSetupReadinessError(errorMessage(error, "Failed to load project setup status."));
        setProjectSetupReadinessLoadState("error");
        markCoordinationUnavailable();
      });
    const operationsLoad = getProjectOperationsBrief(projectID)
      .then((payload) => {
        if (isStale()) return;
        setOperationsBrief(payload.data ?? null);
        setOperationsBriefLoadState("loaded");
        operationsLoaded = true;
      })
      .catch((error) => {
        if (isStale()) return;
        setOperationsBrief(null);
        setOperationsBriefError(errorMessage(error, "Failed to load project operations."));
        setOperationsBriefLoadState("error");
      });
    await Promise.allSettled([activityLoad, healthLoad, readinessLoad, operationsLoad]);
    return operationsLoaded && !isStale();
  }, []);

  const loadAgentPresets = useCallback(async (cancelled?: () => boolean) => {
    try {
      const payload = await getAgentPresets();
      if (cancelled?.()) return;
      setAgentPresets(payload.data ?? []);
      setAgentPresetsError("");
    } catch (error) {
      if (cancelled?.()) return;
      setAgentPresetsError(errorMessage(error, "Failed to load agent presets."));
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void loadAgentPresets(() => cancelled);
    return () => {
      cancelled = true;
    };
  }, [loadAgentPresets]);
  const pendingDeleteProject =
    projects.state.projects.find((project) => project.id === deleteProjectID) ?? null;
  const roleByID = useMemo(() => new Map(roles.map((role) => [role.id, role])), [roles]);
  const activityByAssignmentID = useMemo(() => {
    const items = [
      ...(activity?.buckets.blocked ?? []),
      ...(activity?.buckets.active ?? []),
      ...(activity?.buckets.completed ?? []),
      ...(activity?.buckets.recent ?? []),
      ...(activity?.recent ?? []),
    ];
    return new Map(items.map((item) => [item.assignment.id, item]));
  }, [activity]);
  const providerPresets = providersAndModels.state.providerPresets;
  const providerOptions = useMemo<ProviderOption[]>(() => {
    const configuredProviders = settings.state.config?.providers ?? [];
    if (configuredProviders.length > 0) {
      return configuredProviders.map((provider) => {
        const runtimeProvider = providersAndModels.state.providers.find(
          (item) => item.name === provider.id,
        );
        const cloudUnconfigured = provider.kind === "cloud" && !provider.credential_configured;
        return {
          id: provider.id,
          name: providerDisplayName(
            provider.id,
            configuredProviders,
            providerPresets,
            providersAndModels.state.providers,
          ),
          healthy: runtimeProvider?.healthy,
          kind: provider.kind,
          configured: provider.credential_configured,
          disabledReason: cloudUnconfigured
            ? `Add an API key for ${provider.name || provider.id} in Connections`
            : undefined,
        };
      });
    }

    return providersAndModels.state.providers
      .filter((provider) => provider.name)
      .map((provider) => {
        const preset = providerPresets.find((item) => item.id === provider.name);
        return {
          id: provider.name,
          name: preset?.name || provider.name,
          healthy: provider.healthy,
          kind: preset?.kind ?? provider.kind,
          configured:
            provider.credential_ready ??
            (provider.credential_state === "configured" ||
              provider.credential_state === "not_required"),
        };
      });
  }, [providerPresets, providersAndModels.state.providers, settings.state.config?.providers]);

  const loadWorkForProject = useCallback(
    async (projectID: string, preferredWorkItemID = "", awaitOverview = false) => {
      if (selectedProjectIDRef.current !== projectID) return "";
      const generation = ++workLoadGenerationRef.current;
      const workSelectionGeneration = workItemSelectionGenerationRef.current;
      const isStale = () =>
        generation !== workLoadGenerationRef.current || selectedProjectIDRef.current !== projectID;
      setWorkError("");
      setDetailError("");
      setAssignmentErrors({});
      if (!preferredWorkItemID) {
        setWorkItems([]);
        setWorkItemSummaries({});
        setSelectedWorkItemID("");
        setSelectedWorkItem(null);
        setSelectedWorkItemReadiness(null);
        setAssignments([]);
        setArtifacts([]);
        setHandoffs([]);
      }
      if (!projectID) {
        setActivity(null);
        setActivityLoadState("idle");
        setWorkLoadState("idle");
        setLoadedProjectID("");
        setOperationsBriefLoadState("idle");
        setProjectSetupReadiness(null);
        setProjectSetupReadinessLoadState("idle");
        return "";
      }
      setWorkLoadState("loading");
      const overviewRefresh = refreshProjectOverview(projectID);
      const finishWorkLoad = async (workItemID: string) => {
        if (awaitOverview && !(await overviewRefresh)) return "";
        return workItemID;
      };
      try {
        let workDataError = "";
        const rolesLoad = getProjectWorkRoles(projectID).catch((error) => {
          workDataError ||= errorMessage(error, "Failed to load project roles.");
          return null;
        });
        const workItemsLoad = getProjectWorkItems(projectID).catch((error) => {
          workDataError = errorMessage(error, "Failed to load project work.");
          return null;
        });
        const [rolesRes, workRes] = await Promise.all([rolesLoad, workItemsLoad]);
        if (isStale()) return "";
        if (!rolesRes || !workRes) {
          setWorkLoadState("error");
          setWorkError(workDataError || "Failed to load project work.");
          setLoadedProjectID(projectID);
          return "";
        }
        const nextRoles = rolesRes.data ?? [];
        const nextItems = workRes.data ?? [];
        setRoles(nextRoles);
        setWorkItems(nextItems);
        setWorkItemSummaries(
          Object.fromEntries(
            nextItems.map(
              (item) => [item.id, summarizeAssignments(item.assignments ?? [])] as const,
            ),
          ),
        );
        if (
          preferredWorkItemID &&
          workSelectionGeneration !== workItemSelectionGenerationRef.current
        ) {
          setWorkLoadState("loaded");
          setLoadedProjectID(projectID);
          return finishWorkLoad(selectedWorkItemIDRef.current);
        }
        const nextSelectedID = nextItems.some((item) => item.id === preferredWorkItemID)
          ? preferredWorkItemID
          : nextItems[0]?.id || "";
        setSelectedWorkItemID(nextSelectedID);
        setWorkLoadState("loaded");
        setLoadedProjectID(projectID);
        return finishWorkLoad(nextSelectedID);
      } catch (error) {
        if (isStale()) return "";
        setWorkLoadState("error");
        setWorkError(errorMessage(error, "Failed to load project work."));
        setLoadedProjectID(projectID);
        return "";
      }
    },
    [refreshProjectOverview],
  );

  const loadProjectMemory = useCallback(async (projectID: string) => {
    if (selectedProjectIDRef.current !== projectID) return;
    const generation = ++memoryLoadGenerationRef.current;
    const isStale = () =>
      generation !== memoryLoadGenerationRef.current || selectedProjectIDRef.current !== projectID;
    setMemoryError("");
    if (!projectID) {
      setMemoryEntries([]);
      setMemoryCandidates([]);
      setEditingMemory(null);
      setPromotingCandidate(null);
      setDeleteMemory(null);
      setMemoryLoadState("idle");
      return;
    }
    setMemoryEntries([]);
    setMemoryCandidates([]);
    setEditingMemory(null);
    setPromotingCandidate(null);
    setDeleteMemory(null);
    setMemoryLoadState("loading");
    try {
      const [memoryPayload, candidatePayload] = await Promise.all([
        getProjectMemory(projectID, true),
        getProjectMemoryCandidates(projectID, true),
      ]);
      if (isStale()) return;
      setMemoryEntries(memoryPayload.data ?? []);
      setMemoryCandidates(candidatePayload.data ?? []);
      setMemoryLoadState("loaded");
    } catch (error) {
      if (isStale()) return;
      setMemoryLoadState("error");
      setMemoryError(errorMessage(error, "Failed to load project memory."));
    }
  }, []);

  const loadProjectSkills = useCallback(async (projectID: string) => {
    if (selectedProjectIDRef.current !== projectID) return;
    const generation = ++skillsLoadGenerationRef.current;
    const isStale = () =>
      generation !== skillsLoadGenerationRef.current || selectedProjectIDRef.current !== projectID;
    setSkillsError("");
    setUpdatingSkillID("");
    if (!projectID) {
      setProjectSkills([]);
      setSkillsLoadState("idle");
      return;
    }
    setProjectSkills([]);
    setSkillsLoadState("loading");
    try {
      const payload = await getProjectSkills(projectID);
      if (isStale()) return;
      setProjectSkills(payload.data ?? []);
      setSkillsLoadState("loaded");
    } catch (error) {
      if (isStale()) return;
      setSkillsLoadState("error");
      setSkillsError(errorMessage(error, "Failed to load project skills."));
    }
  }, []);

  const loadWorkItemDetail = useCallback(async (projectID: string, workItemID: string) => {
    if (selectedProjectIDRef.current !== projectID) return false;
    const generation = ++detailLoadGenerationRef.current;
    const isStale = () =>
      generation !== detailLoadGenerationRef.current ||
      selectedProjectIDRef.current !== projectID ||
      selectedWorkItemIDRef.current !== workItemID;
    setDetailError("");
    setAssignmentErrors({});
    if (!projectID || !workItemID) {
      setDetailTarget(null);
      setSelectedWorkItem(null);
      setSelectedWorkItemReadiness(null);
      setAssignments([]);
      setArtifacts([]);
      setHandoffs([]);
      setDetailLoadState("idle");
      return false;
    }
    setDetailTarget({ projectID, workItemID });
    setSelectedWorkItemReadiness(null);
    setDetailLoadState("loading");
    try {
      const [itemRes, assignmentRes, artifactRes, handoffRes, readinessRes] = await Promise.all([
        getProjectWorkItem(projectID, workItemID),
        getProjectAssignments(projectID, workItemID),
        getProjectCollaborationArtifacts(projectID, workItemID),
        getProjectHandoffs(projectID, workItemID),
        getProjectWorkItemReadiness(projectID, workItemID),
      ]);
      if (isStale()) return false;
      setSelectedWorkItem(itemRes.data);
      setSelectedWorkItemReadiness(readinessRes.data);
      setAssignments(assignmentRes.data ?? []);
      setArtifacts(artifactRes.data ?? []);
      setHandoffs(handoffRes.data ?? []);
      setWorkItems((current) => upsertWorkItem(current, itemRes.data));
      setWorkItemSummaries((current) => ({
        ...current,
        [workItemID]: summarizeAssignments(assignmentRes.data ?? []),
      }));
      setDetailLoadState("loaded");
      return true;
    } catch (error) {
      if (isStale()) return false;
      setSelectedWorkItemReadiness(null);
      setDetailLoadState("error");
      setDetailError(errorMessage(error, "Failed to load work item detail."));
      return false;
    }
  }, []);

  const assistant = useProjectAssistantController({
    project: selectedProject,
    selectedProjectID,
    selectedWorkItemID,
    selectedWorkItem,
    onProjectDiscovered: (project) => {
      projects.actions.setProjects((current) => upsertProject(current, project));
    },
    onSkillsDiscovered: setProjectSkills,
    onSkillsLoadState: setSkillsLoadState,
    onDiscoveringContext: setDiscoveringContext,
    onDiscoveringSkills: setDiscoveringSkills,
    onMemoryError: setMemoryError,
    onSkillsError: setSkillsError,
    refreshProjects: projects.actions.loadProjects,
    loadWorkForProject,
    loadWorkItemDetail: async (projectID, workItemID) => {
      await loadWorkItemDetail(projectID, workItemID);
    },
    loadProjectMemory,
  });

  useEffect(() => {
    void loadWorkForProject(selectedProjectID);
  }, [loadWorkForProject, selectedProjectID]);

  useEffect(() => {
    void loadProjectMemory(selectedProjectID);
  }, [loadProjectMemory, selectedProjectID]);

  useEffect(() => {
    void loadProjectSkills(selectedProjectID);
  }, [loadProjectSkills, selectedProjectID]);

  useEffect(() => {
    if (!selectedProjectID || !selectedWorkItemID) return;
    void loadWorkItemDetail(selectedProjectID, selectedWorkItemID);
  }, [loadWorkItemDetail, selectedProjectID, selectedWorkItemID]);

  useEffect(() => {
    if (!selectedProjectID) return;
    if (workLoadState !== "loaded" && workLoadState !== "error") return;
    const handoff = readProjectAssistantChatHandoff();
    if (!handoff || handoff.project_id !== selectedProjectID) return;
    const loaded = assistant.loadProposal(handoff.proposal, {
      chatDraftSource: {
        request: handoff.request,
        sourceSessionID: handoff.source_session_id,
        createdAt: handoff.created_at,
      },
    });
    if (!loaded) return;
    navigateWorkspaceTab("work");
    clearProjectAssistantChatHandoff();
  }, [assistant.loadProposal, navigateWorkspaceTab, selectedProjectID, workLoadState]);

  function startRename(project: ProjectRecord) {
    setRenamingProjectID(project.id);
    setRenameValue(project.name);
  }

  async function commitRename(project: ProjectRecord) {
    const nextName = renameValue.trim();
    setRenamingProjectID("");
    if (!nextName || nextName === project.name) return;
    await projects.actions.renameProject(project.id, nextName);
  }

  async function confirmDeleteProject() {
    if (!pendingDeleteProject) return;
    setDeletePending(true);
    try {
      const deleted = await projects.actions.deleteProject(pendingDeleteProject.id);
      if (deleted) {
        setDeleteProjectID("");
        settings.actions.setNotice({
          kind: "success",
          message: formatProjectDeleteSummary(deleted),
        });
        clearSelectedProject(pendingDeleteProject.id);
      }
    } finally {
      setDeletePending(false);
    }
  }

  async function handleSaveProjectDefaults(form: ProjectDefaultsForm) {
    if (!selectedProject) return;
    const project = selectedProject;
    const projectID = project.id;
    const selectionGeneration = projectSelectionGenerationRef.current;
    setDefaultsPending(true);
    setDefaultsError("");
    const patch: UpdateProjectPayload = {
      default_provider: form.provider.trim(),
      default_model: form.model.trim(),
      default_agent_profile: form.defaultAgentPreset.trim(),
      default_workspace_mode: form.workspaceMode.trim(),
      default_root_id: form.defaultRootID.trim(),
    };
    try {
      const existingRootsByID = new Map(project.roots.map((root) => [root.id, root]));
      const nextRootIDs = new Set(form.roots.map((root) => root.id?.trim() ?? "").filter(Boolean));
      let currentProject = project;
      const applyRootProject = (project: ProjectRecord) => {
        currentProject = project;
        projects.actions.setProjects((current) => upsertProject(current, project));
      };
      for (const root of form.roots) {
        const rootID = root.id?.trim() ?? "";
        const existing = rootID ? existingRootsByID.get(rootID) : undefined;
        let payload: { data: ProjectRecord } | null = null;
        if (!existing) {
          payload = await createProjectRoot(projectID, root);
        } else if (!projectRootPayloadsEqual(projectRootPayloadFromRecord(existing), root)) {
          payload = await updateProjectRoot(projectID, rootID, root);
        }
        if (payload) applyRootProject(payload.data);
      }
      for (const root of project.roots) {
        if (nextRootIDs.has(root.id)) continue;
        const payload = await deleteProjectRoot(projectID, root.id);
        applyRootProject(payload.data);
      }
      const payload = await updateProject(currentProject.id, patch);
      projects.actions.setProjects((current) => upsertProject(current, payload.data));
      void refreshProjectOverview(projectID);
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setSettingsPanelOpen(false);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setDefaultsError(errorMessage(error, "Failed to update project defaults."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setDefaultsPending(false);
    }
  }

  async function handleDiscoverProjectRoots() {
    if (!selectedProject) return;
    const projectID = selectedProject.id;
    const selectionGeneration = projectSelectionGenerationRef.current;
    setDiscoveringRoots(true);
    setDefaultsError("");
    try {
      const payload = await discoverProjectRoots(projectID);
      projects.actions.setProjects((current) => upsertProject(current, payload.data));
      void refreshProjectOverview(projectID);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setDefaultsError(errorMessage(error, "Failed to discover project roots."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setDiscoveringRoots(false);
    }
  }

  async function handleCreateWorktreeRoot(form: CreateWorktreeForm) {
    if (!selectedProject) return;
    const projectID = selectedProject.id;
    const selectionGeneration = projectSelectionGenerationRef.current;
    const branch = form.branch.trim();
    if (!branch) return;
    const payload: CreateProjectWorktreeRootPayload = {
      branch,
      base_root_id: form.baseRootID.trim() || undefined,
      start_point: form.startPoint.trim() || undefined,
      path: form.path.trim() || undefined,
      active: form.active,
      set_default: form.setDefault,
    };
    setCreateWorktreePending(true);
    setCreateWorktreeError("");
    try {
      const result = await createProjectWorktreeRoot(projectID, payload);
      projects.actions.setProjects((current) => upsertProject(current, result.data));
      void refreshProjectOverview(projectID);
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setCreateWorktreeOpen(false);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setCreateWorktreeError(errorMessage(error, "Failed to create project worktree."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) {
        setCreateWorktreePending(false);
      }
    }
  }

  async function handleCreateProject(form: CreateProjectForm) {
    const payload = createProjectPayloadFromForm(form);
    if (!payload.name) {
      setCreateProjectError("Project name is required.");
      return;
    }
    setCreateProjectPending(true);
    setCreateProjectError("");
    try {
      const created = await projects.actions.createProject(payload);
      if (!created) return;
      setCreateProjectOpen(false);
      openProject(created.id);
    } finally {
      setCreateProjectPending(false);
    }
  }

  async function handleChooseProjectWorkspace() {
    const workspace = await chooseWorkspaceDirectory();
    return {
      path: workspace.data.path,
      branch: workspace.data.branch || undefined,
    };
  }

  async function handleDiscoverContextSources() {
    if (!selectedProjectID) return;
    const projectID = selectedProjectID;
    const selectionGeneration = projectSelectionGenerationRef.current;
    setDiscoveringContext(true);
    setMemoryError("");
    try {
      const payload = await discoverProjectContextSources(projectID);
      projects.actions.setProjects((current) => upsertProject(current, payload.data));
      void refreshProjectOverview(projectID);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setMemoryError(errorMessage(error, "Failed to discover workspace guidance."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setDiscoveringContext(false);
    }
  }

  async function handleDiscoverProjectSkills() {
    if (!selectedProjectID) return;
    const projectID = selectedProjectID;
    const selectionGeneration = projectSelectionGenerationRef.current;
    setDiscoveringSkills(true);
    setSkillsError("");
    try {
      const payload = await discoverProjectSkills(projectID);
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setProjectSkills(payload.data ?? []);
      setSkillsLoadState("loaded");
      void refreshProjectOverview(projectID);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setSkillsError(errorMessage(error, "Failed to discover project skills."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setDiscoveringSkills(false);
    }
  }

  async function handleUpdateProjectSkill(
    skill: ProjectSkillRecord,
    patch: UpdateProjectSkillPayload,
  ) {
    if (!selectedProjectID) return;
    const projectID = selectedProjectID;
    const selectionGeneration = projectSelectionGenerationRef.current;
    setUpdatingSkillID(skill.id);
    setSkillsError("");
    try {
      const payload = await updateProjectSkill(projectID, skill.id, patch);
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setProjectSkills((current) => upsertProjectSkill(current, payload.data));
      void refreshProjectOverview(projectID);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setSkillsError(errorMessage(error, "Failed to update project skill."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setUpdatingSkillID("");
    }
  }

  async function handleCreateAgentPreset(form: AgentPresetForm) {
    const name = form.name.trim();
    if (!name) return undefined;
    setPresetsPending(true);
    setPresetsError("");
    try {
      const payload = await createAgentPreset(presetCreatePayloadFromForm(form));
      setAgentPresets((current) => upsertAgentPreset(current, payload.data));
      void refreshProjectOverview(selectedProjectID);
      return payload.data;
    } catch (error) {
      setPresetsError(errorMessage(error, "Failed to create agent preset."));
      return undefined;
    } finally {
      setPresetsPending(false);
    }
  }

  async function handleUpdateAgentPreset(presetID: string, form: AgentPresetForm) {
    const name = form.name.trim();
    if (!name) return undefined;
    setPresetsPending(true);
    setPresetsError("");
    try {
      const payload = await updateAgentPreset(presetID, presetUpdatePayloadFromForm(form));
      setAgentPresets((current) => upsertAgentPreset(current, payload.data));
      void refreshProjectOverview(selectedProjectID);
      return payload.data;
    } catch (error) {
      setPresetsError(errorMessage(error, "Failed to update agent preset."));
      return undefined;
    } finally {
      setPresetsPending(false);
    }
  }

  async function handleDeleteAgentPreset(preset: AgentPresetRecord) {
    setPresetsPending(true);
    setPresetsError("");
    try {
      await deleteAgentPreset(preset.id);
      setAgentPresets((current) => current.filter((item) => item.id !== preset.id));
      void refreshProjectOverview(selectedProjectID);
      return true;
    } catch (error) {
      setPresetsError(errorMessage(error, "Failed to delete agent preset."));
      return false;
    } finally {
      setPresetsPending(false);
    }
  }

  async function handleSaveMemory(form: MemoryForm) {
    if (!selectedProjectID || !editingMemory) return;
    const projectID = selectedProjectID;
    const memory = editingMemory;
    const selectionGeneration = projectSelectionGenerationRef.current;
    const payload = {
      title: form.title.trim(),
      body: form.body.trim(),
      trust_label: form.trustLabel.trim(),
      source_kind: form.sourceKind.trim(),
      source_id: form.sourceID.trim(),
      enabled: form.enabled,
    };
    setMemoryPending(true);
    setMemoryError("");
    try {
      const res =
        memory === "new"
          ? await createProjectMemory(projectID, payload)
          : await updateProjectMemory(projectID, memory.id, payload);
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setMemoryEntries((current) => upsertMemory(current, res.data));
      void refreshProjectOverview(projectID);
      setEditingMemory(null);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setMemoryError(errorMessage(error, "Failed to save project memory."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setMemoryPending(false);
    }
  }

  async function handleSaveSource(form: ProjectSourceForm) {
    if (!selectedProject || !editingSource) return;
    const project = selectedProject;
    const projectID = project.id;
    const source = editingSource;
    const selectionGeneration = projectSelectionGenerationRef.current;
    const title = form.title.trim();
    if (!title) {
      setSourceError("Source title is required.");
      return;
    }
    if (!form.locator.trim() && (form.kind.trim() !== "note" || !form.note.trim())) {
      setSourceError(
        form.kind.trim() === "note"
          ? "Source locator or note is required."
          : "Source locator is required.",
      );
      return;
    }
    setSourcePending(true);
    setSourceError("");
    try {
      const sourcePayload = projectSourcePayloadFromForm(form, source === "new" ? null : source);
      const payload =
        source === "new"
          ? await createProjectContextSource(projectID, sourcePayload)
          : await updateProjectContextSource(projectID, source.id, sourcePayload);
      projects.actions.setProjects((current) => upsertProject(current, payload.data));
      void refreshProjectOverview(projectID);
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setEditingSource(null);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setSourceError(errorMessage(error, "Failed to save project source."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setSourcePending(false);
    }
  }

  async function confirmDeleteSource() {
    if (!selectedProject || !deleteSource) return;
    const projectID = selectedProject.id;
    const sourceID = deleteSource.id;
    const selectionGeneration = projectSelectionGenerationRef.current;
    setDeleteSourcePending(true);
    setSourceError("");
    try {
      const payload = await deleteProjectContextSource(projectID, sourceID);
      projects.actions.setProjects((current) => upsertProject(current, payload.data));
      void refreshProjectOverview(projectID);
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setDeleteSource(null);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setSourceError(errorMessage(error, "Failed to delete project source."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setDeleteSourcePending(false);
    }
  }

  async function confirmDeleteMemory() {
    if (!selectedProjectID || !deleteMemory) return;
    const projectID = selectedProjectID;
    const memoryID = deleteMemory.id;
    const selectionGeneration = projectSelectionGenerationRef.current;
    setDeleteMemoryPending(true);
    setMemoryError("");
    try {
      await deleteProjectMemory(projectID, memoryID);
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setMemoryEntries((current) => current.filter((item) => item.id !== memoryID));
      void refreshProjectOverview(projectID);
      setDeleteMemory(null);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setMemoryError(errorMessage(error, "Failed to delete project memory."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setDeleteMemoryPending(false);
    }
  }

  async function reloadRoles(projectID = selectedProjectID) {
    if (!projectID) return;
    const payload = await getProjectWorkRoles(projectID);
    if (selectedProjectIDRef.current !== projectID) return;
    setRoles(payload.data ?? []);
  }

  async function handleCreateRole(form: RoleForm) {
    if (!selectedProjectID) return undefined;
    const projectID = selectedProjectID;
    const selectionGeneration = projectSelectionGenerationRef.current;
    const name = form.name.trim();
    if (!name) return undefined;
    setRolesPending(true);
    setRolesError("");
    try {
      const payload = await createProjectWorkRole(projectID, rolePayloadFromForm(form));
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return undefined;
      setRoles((current) => upsertRole(current, payload.data));
      void refreshProjectOverview(projectID);
      return payload.data;
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return undefined;
      setRolesError(errorMessage(error, "Failed to create role."));
      return undefined;
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setRolesPending(false);
    }
  }

  async function handleUpdateRole(roleID: string, form: RoleForm) {
    if (!selectedProjectID) return undefined;
    const projectID = selectedProjectID;
    const selectionGeneration = projectSelectionGenerationRef.current;
    const name = form.name.trim();
    if (!name) return undefined;
    setRolesPending(true);
    setRolesError("");
    try {
      const payload = await updateProjectWorkRole(projectID, roleID, rolePayloadFromForm(form));
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return undefined;
      setRoles((current) => upsertRole(current, payload.data));
      void refreshProjectOverview(projectID);
      return payload.data;
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return undefined;
      setRolesError(errorMessage(error, "Failed to update role."));
      return undefined;
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setRolesPending(false);
    }
  }

  async function handleDeleteRole(role: ProjectWorkRoleRecord) {
    if (!selectedProjectID || role.built_in) return false;
    const projectID = selectedProjectID;
    const selectionGeneration = projectSelectionGenerationRef.current;
    setRolesPending(true);
    setRolesError("");
    try {
      await deleteProjectWorkRole(projectID, role.id);
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return false;
      await reloadRoles(projectID);
      void refreshProjectOverview(projectID);
      return true;
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return false;
      setRolesError(errorMessage(error, "Failed to delete role."));
      return false;
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setRolesPending(false);
    }
  }

  async function handlePromoteCandidate(form: MemoryForm) {
    if (!selectedProjectID || !promotingCandidate) return;
    const projectID = selectedProjectID;
    const candidateID = promotingCandidate.id;
    const selectionGeneration = projectSelectionGenerationRef.current;
    const payload = {
      title: form.title.trim(),
      body: form.body.trim(),
      trust_label: form.trustLabel.trim(),
      source_kind: form.sourceKind.trim(),
      source_id: form.sourceID.trim(),
      enabled: form.enabled,
    };
    setMemoryPending(true);
    setMemoryError("");
    try {
      const candidateRes = await promoteProjectMemoryCandidate(projectID, candidateID, payload);
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setMemoryCandidates((current) => current.filter((item) => item.id !== candidateRes.data.id));
      await loadProjectMemory(projectID);
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      void refreshProjectOverview(projectID);
      setPromotingCandidate(null);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setMemoryError(errorMessage(error, "Failed to promote memory candidate."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setMemoryPending(false);
    }
  }

  async function handleRejectCandidate(candidate: ProjectMemoryCandidateRecord) {
    if (!selectedProjectID || rejectingCandidateID) return;
    const projectID = selectedProjectID;
    const selectionGeneration = projectSelectionGenerationRef.current;
    setRejectingCandidateID(candidate.id);
    setMemoryError("");
    try {
      const res = await rejectProjectMemoryCandidate(projectID, candidate.id, {});
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setMemoryCandidates((current) => current.filter((item) => item.id !== res.data.id));
      void refreshProjectOverview(projectID);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setMemoryError(errorMessage(error, "Failed to reject memory candidate."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setRejectingCandidateID("");
    }
  }

  function openNewWorkItemModal() {
    setNewWorkError("");
    setNewWorkDraft(
      buildFirstWorkItemDraft({
        memoryCandidates,
        project: selectedProject,
        projectSkills,
        roles,
        workItems,
      }),
    );
    setNewWorkModalOpen(true);
  }

  async function handleCreateWorkItem(form: NewWorkItemForm) {
    if (!selectedProjectID) return;
    const title = form.title.trim();
    if (!title) return;
    const projectID = selectedProjectID;
    const selectionGeneration = projectSelectionGenerationRef.current;
    setNewWorkPending(true);
    setNewWorkError("");
    try {
      const payload = await createProjectWorkItem(projectID, workItemCreatePayloadFromForm(form));
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setWorkItems((current) => upsertWorkItem(current, payload.data));
      setWorkItemSummaries((current) => ({
        ...current,
        [payload.data.id]: {
          assignmentCount: 0,
          activeCount: 0,
          failedCount: 0,
          completedCount: 0,
        },
      }));
      setSelectedWorkItemOperationID("");
      setSelectedWorkItemID(payload.data.id);
      setWorkspaceTab("work");
      setNewWorkModalOpen(false);
      setNewWorkDraft(undefined);
      await loadWorkItemDetail(projectID, payload.data.id);
      void refreshProjectOverview(projectID);
    } catch (error) {
      if (!isCurrentProjectMutation(projectID, selectionGeneration)) return;
      setNewWorkError(errorMessage(error, "Failed to create work item."));
    } finally {
      if (isCurrentProjectMutation(projectID, selectionGeneration)) setNewWorkPending(false);
    }
  }

  async function handleUpdateWorkItem(form: EditWorkItemForm) {
    if (!selectedProjectID) return;
    const projectID = selectedProjectID;
    const projectSelectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const isCurrent = () =>
      isCurrentWorkItemMutation(
        projectID,
        projectSelectionGeneration,
        form.id,
        workSelectionGeneration,
      );
    const title = form.title.trim();
    if (!title) return;
    setEditWorkPending(true);
    setEditWorkError("");
    const patch = workItemUpdatePayloadFromForm(form);
    try {
      const payload = await updateProjectWorkItem(projectID, form.id, patch);
      if (!isCurrent()) return;
      setWorkItems((current) => upsertWorkItem(current, payload.data));
      setSelectedWorkItem(payload.data);
      setEditingWorkItem(null);
      await loadWorkItemDetail(projectID, payload.data.id);
      void refreshProjectOverview(projectID);
    } catch (error) {
      if (!isCurrent()) return;
      setEditWorkError(errorMessage(error, "Failed to update work item."));
    } finally {
      if (isCurrent()) setEditWorkPending(false);
    }
  }

  async function handleCloseWorkItem(item: ProjectWorkItemRecord) {
    if (!selectedProjectID || closingWorkItemID) return;
    const projectID = selectedProjectID;
    const projectSelectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const isCurrent = () =>
      isCurrentWorkItemMutation(
        projectID,
        projectSelectionGeneration,
        item.id,
        workSelectionGeneration,
      );
    setClosingWorkItemID(item.id);
    setDetailError("");
    try {
      const payload = await updateProjectWorkItem(projectID, item.id, {
        status: "done",
      });
      if (!isCurrent()) return;
      setWorkItems((current) => upsertWorkItem(current, payload.data));
      if (selectedWorkItemID === item.id) {
        setSelectedWorkItem(payload.data);
      }
      await loadWorkForProject(projectID, item.id);
      if (!isCurrent()) return;
      await loadWorkItemDetail(projectID, item.id);
    } catch (error) {
      if (!isCurrent()) return;
      const message = errorMessage(error, "Failed to mark work item done.");
      if (error instanceof ApiError && error.status === 409) {
        await loadWorkForProject(projectID, item.id);
        if (isCurrent()) await loadWorkItemDetail(projectID, item.id);
      }
      if (isCurrent()) setDetailError(message);
    } finally {
      if (isCurrent()) setClosingWorkItemID("");
    }
  }

  async function confirmDeleteWorkItem() {
    if (!selectedProjectID || !deleteWorkItem) return;
    const projectID = selectedProjectID;
    const workItemID = deleteWorkItem.id;
    const projectSelectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const isCurrent = () =>
      isCurrentWorkItemMutation(
        projectID,
        projectSelectionGeneration,
        workItemID,
        workSelectionGeneration,
      );
    setDeleteWorkPending(true);
    try {
      await deleteProjectWorkItem(projectID, workItemID);
      if (!isCurrent()) return;
      setDeleteWorkItem(null);
      await loadWorkForProject(projectID);
    } finally {
      if (isCurrent()) setDeleteWorkPending(false);
    }
  }

  async function handleCreateAssignment(form: NewAssignmentForm) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const projectID = selectedProjectID;
    const workItemID = selectedWorkItemID;
    const selectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const roleID = form.roleID.trim();
    if (!roleID) return;
    setNewAssignmentPending(true);
    setNewAssignmentError("");
    try {
      const payload = await createProjectAssignment(
        projectID,
        workItemID,
        assignmentCreatePayloadFromForm(form),
      );
      if (
        !isCurrentWorkItemMutation(
          projectID,
          selectionGeneration,
          workItemID,
          workSelectionGeneration,
        )
      ) {
        return;
      }
      setAssignments((current) => upsertAssignment(current, payload.data));
      setNewAssignmentModalOpen(false);
      await loadWorkItemDetail(projectID, workItemID);
      void refreshProjectOverview(projectID);
    } catch (error) {
      if (
        !isCurrentWorkItemMutation(
          projectID,
          selectionGeneration,
          workItemID,
          workSelectionGeneration,
        )
      ) {
        return;
      }
      setNewAssignmentError(errorMessage(error, "Failed to create assignment."));
    } finally {
      if (
        isCurrentWorkItemMutation(
          projectID,
          selectionGeneration,
          workItemID,
          workSelectionGeneration,
        )
      ) {
        setNewAssignmentPending(false);
      }
    }
  }

  function handleDraftDefaultAssignment(item: ProjectWorkItemRecord) {
    if (!selectedProjectID) return;
    const role = (item.owner_role_id ? roleByID.get(item.owner_role_id) : null) ?? roles[0] ?? null;
    if (!role) {
      setDetailError("Add a project role before drafting an assignment proposal.");
      return;
    }
    setDetailError("");
    void assistant.propose({
      request: `Queue ${role.name || role.id} for ${item.title}`,
      roleID: PROJECT_ASSISTANT_AUTO,
      driverKind: PROJECT_ASSISTANT_AUTO,
      draftMode: "deterministic",
    });
  }

  function handleOperationsBriefAction(item: ProjectOperationsBriefItem) {
    const candidateID = item.action?.candidate_id;
    const route = routeProjectOperationAction(item, selectedProjectID, {
      hasMemoryCandidate: Boolean(
        candidateID && memoryCandidates.some((candidate) => candidate.id === candidateID),
      ),
    });
    if (route.kind === "open_work_item") {
      setSelectedWorkItemOperationID(item.id);
    }
    handleProjectActionRoute(route);
  }

  function handleSetupReadinessAction(action: ProjectSetupReadiness["primary_action"]) {
    handleProjectActionRoute(routeProjectSetupAction(action, selectedProjectID));
  }

  function handleProjectActionRoute(route: ProjectActionRoute) {
    switch (route.kind) {
      case "error":
        setWorkError(route.message);
        return;
      case "draft_project_proposal":
        navigateWorkspaceTab("work");
        if (route.workItemID) {
          setSelectedWorkItemOperationID("");
          setSelectedWorkItemID(route.workItemID);
        }
        void assistant.propose(
          {
            request: route.request,
            roleID: PROJECT_ASSISTANT_AUTO,
            driverKind: PROJECT_ASSISTANT_AUTO,
            draftMode: "deterministic",
          },
          route.workItemID,
        );
        return;
      case "open_project_settings":
        setDefaultsError("");
        setSettingsPanelOpen(true);
        return;
      case "open_memory_review":
        navigateWorkspaceTab("memory");
        return;
      case "open_agent_presets":
        setPresetsError("");
        setAgentPresetsModalOpen(true);
        return;
      case "open_roles":
        setRolesError("");
        setRolesModalOpen(true);
        return;
      case "open_skills":
        navigateWorkspaceTab("skills");
        return;
      case "open_assignment_preflight": {
        const workItemID = route.workItemID || selectedWorkItemIDRef.current;
        if (!selectedProjectID || !workItemID) {
          setWorkError("Assignment preflight is missing a work item target.");
          return;
        }
        setSelectedWorkItemOperationID("");
        selectProjectWorkRoute(route);
        setPreparingAssignmentTarget({
          assignmentID: route.assignmentID,
          projectID: selectedProjectID,
          workItemID,
        });
        return;
      }
      case "open_work_item":
        setWorkItemFocusTarget({
          artifactID: route.artifactID,
          assignmentID: route.assignmentID,
          handoffID: route.handoffID,
          operationKind: route.operationKind,
          workItemID: route.workItemID,
        });
        selectProjectWorkRoute(route);
        return;
      case "open_activity_bucket":
        setSelectedWorkItemOperationID("");
        setActivityBucket(route.bucket);
        navigateWorkspaceTab("work");
        return;
      case "open_task":
        if (onOpenTask) {
          onOpenTask(route.taskID, route.runID);
          return;
        }
        setWorkError("Task navigation is unavailable in this view.");
        return;
      case "review_memory_candidate": {
        const candidate = memoryCandidates.find((item) => item.id === route.candidateID);
        if (candidate) {
          setPromotingCandidate(candidate);
          return;
        }
        navigateWorkspaceTab("memory");
        return;
      }
      case "bootstrap_project":
        navigateWorkspaceTab("work");
        void assistant.bootstrap();
        return;
      case "create_work_item":
        openNewWorkItemModal();
        return;
      case "none":
        return;
    }
  }

  function selectProjectWorkRoute(
    route: Extract<ProjectActionRoute, { kind: "open_assignment_preflight" | "open_work_item" }>,
  ) {
    if (route.kind === "open_assignment_preflight") {
      navigateWorkspaceTab("work");
    } else {
      setWorkspaceTab("work");
      setWorkspaceTabFocusTarget(null);
    }
    if (route.bucket) {
      setActivityBucket(route.bucket);
    }
    if (route.workItemID) {
      setSelectedWorkItemID(route.workItemID);
    }
  }

  function handleSelectWorkItem(workItemID: string) {
    setWorkItemFocusTarget(null);
    setSelectedWorkItemOperationID("");
    navigateWorkspaceTab("work");
    setSelectedWorkItemID(workItemID);
  }

  async function handleUpdateAssignment(form: EditAssignmentForm) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const projectID = selectedProjectID;
    const workItemID = selectedWorkItemID;
    const projectSelectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const isCurrent = () =>
      isCurrentWorkItemMutation(
        projectID,
        projectSelectionGeneration,
        workItemID,
        workSelectionGeneration,
      );
    const roleID = form.roleID.trim();
    if (!roleID) return;
    setEditAssignmentPending(true);
    setEditAssignmentError("");
    const originalAssignment = assignments.find((assignment) => assignment.id === form.id);
    const patch =
      originalAssignment?.driver_kind === "manual" &&
      (originalAssignment.status !== "queued" || form.status !== originalAssignment.status)
        ? { status: form.status }
        : assignmentUpdatePayloadFromForm(form);
    try {
      const payload = await updateProjectAssignment(projectID, workItemID, form.id, patch);
      if (!isCurrent()) return;
      setAssignments((current) => upsertAssignment(current, payload.data));
      setEditingAssignment(null);
      await loadWorkItemDetail(projectID, workItemID);
      void refreshProjectOverview(projectID);
    } catch (error) {
      if (!isCurrent()) return;
      setEditAssignmentError(errorMessage(error, "Failed to update assignment."));
    } finally {
      if (isCurrent()) setEditAssignmentPending(false);
    }
  }

  async function confirmDeleteAssignment() {
    if (!selectedProjectID || !selectedWorkItemID || !deleteAssignment) return;
    const projectID = selectedProjectID;
    const workItemID = selectedWorkItemID;
    const assignmentID = deleteAssignment.id;
    const projectSelectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const isCurrent = () =>
      isCurrentWorkItemMutation(
        projectID,
        projectSelectionGeneration,
        workItemID,
        workSelectionGeneration,
      );
    setDeleteAssignmentPending(true);
    try {
      await deleteProjectAssignment(projectID, workItemID, assignmentID);
      if (!isCurrent()) return;
      setDeleteAssignment(null);
      await loadWorkItemDetail(projectID, workItemID);
      void refreshProjectOverview(projectID);
    } finally {
      if (isCurrent()) setDeleteAssignmentPending(false);
    }
  }

  async function handleSaveHandoff(form: HandoffForm) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const projectID = selectedProjectID;
    const workItemID = selectedWorkItemID;
    const handoff = editingHandoff;
    const projectSelectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const isCurrent = () =>
      isCurrentWorkItemMutation(
        projectID,
        projectSelectionGeneration,
        workItemID,
        workSelectionGeneration,
      );
    const title = form.title.trim();
    const summary = form.summary.trim();
    const recommendedNextAction = form.recommendedNextAction.trim();
    if (!title || !summary || !recommendedNextAction) return;
    const payload = handoffPayloadFromForm(form);
    setHandoffPending(true);
    setHandoffError("");
    try {
      const res =
        handoff === "new"
          ? await createProjectHandoff(projectID, workItemID, payload)
          : await updateProjectHandoff(projectID, workItemID, form.id, payload);
      if (!isCurrent()) return;
      setHandoffs((current) => upsertHandoff(current, res.data));
      setEditingHandoff(null);
      setNewHandoffDraft(null);
      await loadWorkItemDetail(projectID, workItemID);
      if (!isCurrent()) return;
      await loadWorkForProject(projectID, workItemID);
      if (isCurrent()) {
        setWorkItemFocusTarget({ handoffID: res.data.id, workItemID });
      }
    } catch (error) {
      if (!isCurrent()) return;
      setHandoffError(errorMessage(error, "Failed to save handoff."));
    } finally {
      if (isCurrent()) setHandoffPending(false);
    }
  }

  async function handleSaveReviewArtifact(form: ReviewArtifactForm) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const projectID = selectedProjectID;
    const workItemID = selectedWorkItemID;
    const projectSelectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const isCurrent = () =>
      isCurrentWorkItemMutation(
        projectID,
        projectSelectionGeneration,
        workItemID,
        workSelectionGeneration,
      );
    const payload = reviewArtifactPayloadFromForm(form);
    if (!payload.title?.trim() || !payload.body.trim()) return;
    setReviewArtifactPending(true);
    setReviewArtifactError("");
    try {
      const res = await createProjectCollaborationArtifact(projectID, workItemID, payload);
      if (!isCurrent()) return;
      setArtifacts((current) => upsertArtifact(current, res.data));
      setReviewArtifactDraft(null);
      await loadWorkItemDetail(projectID, workItemID);
      if (!isCurrent()) return;
      await loadWorkForProject(projectID, workItemID);
      if (isCurrent()) {
        setWorkItemFocusTarget({ artifactID: res.data.id, workItemID });
      }
    } catch (error) {
      if (!isCurrent()) return;
      setReviewArtifactError(errorMessage(error, "Failed to save review artifact."));
    } finally {
      if (isCurrent()) setReviewArtifactPending(false);
    }
  }

  async function handleSaveEvidenceLink(form: EvidenceLinkForm) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const projectID = selectedProjectID;
    const workItemID = selectedWorkItemID;
    const projectSelectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const isCurrent = () =>
      isCurrentWorkItemMutation(
        projectID,
        projectSelectionGeneration,
        workItemID,
        workSelectionGeneration,
      );
    const payload = evidenceLinkPayloadFromForm(form);
    if (!payload.title?.trim() || !payload.body.trim()) return;
    if (!payload.evidence_url?.trim() && !payload.evidence_external_id?.trim()) return;
    setEvidenceLinkPending(true);
    setEvidenceLinkError("");
    try {
      const res = await createProjectCollaborationArtifact(projectID, workItemID, payload);
      if (!isCurrent()) return;
      setArtifacts((current) => upsertArtifact(current, res.data));
      setEvidenceLinkModalOpen(false);
      setEvidenceLinkAssignmentID("");
      await loadWorkItemDetail(projectID, workItemID);
      if (!isCurrent()) return;
      await loadWorkForProject(projectID, workItemID);
      if (isCurrent()) {
        setWorkItemFocusTarget({ artifactID: res.data.id, workItemID });
      }
    } catch (error) {
      if (!isCurrent()) return;
      setEvidenceLinkError(errorMessage(error, "Failed to record evidence."));
    } finally {
      if (isCurrent()) setEvidenceLinkPending(false);
    }
  }

  async function handleSetHandoffStatus(handoff: ProjectHandoffRecord, status: string) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const projectID = selectedProjectID;
    const workItemID = selectedWorkItemID;
    const projectSelectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const isCurrent = () =>
      isCurrentWorkItemMutation(
        projectID,
        projectSelectionGeneration,
        workItemID,
        workSelectionGeneration,
      );
    setHandoffActionID(handoff.id);
    setHandoffError("");
    try {
      const res = await updateProjectHandoffStatus(projectID, workItemID, handoff.id, status);
      if (!isCurrent()) return;
      setHandoffs((current) => upsertHandoff(current, res.data));
      await loadWorkItemDetail(projectID, workItemID);
      if (!isCurrent()) return;
      await loadWorkForProject(projectID, workItemID);
      if (isCurrent()) {
        setWorkItemFocusTarget({ handoffID: res.data.id, workItemID });
      }
    } catch (error) {
      if (!isCurrent()) return;
      setHandoffError(errorMessage(error, "Failed to update handoff status."));
    } finally {
      if (isCurrent()) setHandoffActionID("");
    }
  }

  async function handleDeleteHandoff(handoff: ProjectHandoffRecord) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const projectID = selectedProjectID;
    const workItemID = selectedWorkItemID;
    const projectSelectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const isCurrent = () =>
      isCurrentWorkItemMutation(
        projectID,
        projectSelectionGeneration,
        workItemID,
        workSelectionGeneration,
      );
    setHandoffActionID(handoff.id);
    setHandoffError("");
    try {
      await deleteProjectHandoff(projectID, workItemID, handoff.id);
      if (!isCurrent()) return;
      setHandoffs((current) => current.filter((item) => item.id !== handoff.id));
      await loadWorkItemDetail(projectID, workItemID);
      if (!isCurrent()) return;
      await loadWorkForProject(projectID, workItemID);
    } catch (error) {
      if (!isCurrent()) return;
      setHandoffError(errorMessage(error, "Failed to delete handoff."));
    } finally {
      if (isCurrent()) setHandoffActionID("");
    }
  }

  async function handleCreateAssignmentFromHandoff(
    handoff: ProjectHandoffRecord,
    options: { failureMessage?: string; prefixFailureMessage?: boolean } = {},
  ) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    if (handoff.status === "dismissed" || handoff.status === "superseded") {
      setHandoffError("This handoff is closed and cannot create a follow-up assignment.");
      return;
    }
    const projectID = selectedProjectID;
    const workItemID = selectedWorkItemID;
    const projectSelectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const isCurrent = () =>
      isCurrentWorkItemMutation(
        projectID,
        projectSelectionGeneration,
        workItemID,
        workSelectionGeneration,
      );
    const roleID = handoff.target_role_id?.trim() || "";
    if (!roleID) {
      setHandoffError("Choose a target role before creating a follow-up assignment.");
      return;
    }
    const targetWorkItemID = (handoff.target_work_item_id || workItemID).trim();
    if (!targetWorkItemID) return;
    setHandoffActionID(handoff.id);
    setHandoffError("");
    try {
      const assignment = await createProjectAssignment(projectID, targetWorkItemID, {
        role_id: roleID,
      });
      if (isCurrent() && targetWorkItemID === workItemID) {
        setAssignments((current) => upsertAssignment(current, assignment.data));
      }
      const updated = await updateProjectHandoff(projectID, workItemID, handoff.id, {
        target_assignment_id: assignment.data.id,
        target_role_id: assignment.data.role_id,
      });
      if (!isCurrent()) return;
      setHandoffs((current) => upsertHandoff(current, updated.data));
      await loadWorkItemDetail(projectID, workItemID);
      if (!isCurrent()) return;
      await loadWorkForProject(projectID, workItemID);
    } catch (error) {
      if (!isCurrent()) return;
      const failureMessage = options.failureMessage || "Failed to create target assignment.";
      const detail = errorMessage(error, failureMessage);
      setHandoffError(
        options.prefixFailureMessage && detail !== failureMessage
          ? `${failureMessage} ${detail}`
          : detail,
      );
    } finally {
      if (isCurrent()) setHandoffActionID("");
    }
  }

  async function handleCreateAssignmentFromReviewArtifact(artifactID: string) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const projectID = selectedProjectID;
    const workItemID = selectedWorkItemID;
    const projectSelectionGeneration = projectSelectionGenerationRef.current;
    const workSelectionGeneration = workItemSelectionGenerationRef.current;
    const isCurrent = () =>
      isCurrentWorkItemMutation(
        projectID,
        projectSelectionGeneration,
        workItemID,
        workSelectionGeneration,
      );
    setArtifactActionID(artifactID);
    setHandoffError("");
    try {
      await assistant.draftReviewFollowUp(artifactID, workItemID);
    } finally {
      if (isCurrent()) setArtifactActionID("");
    }
  }

  async function refreshSelectedWorkItem() {
    if (!selectedProjectID) return false;
    const refreshedWorkItemID = await loadWorkForProject(
      selectedProjectID,
      selectedWorkItemID,
      true,
    );
    if (refreshedWorkItemID) {
      const detailRefreshed = await loadWorkItemDetail(selectedProjectID, refreshedWorkItemID);
      if (!detailRefreshed) return false;
      return true;
    }
    return false;
  }

  async function handleStartAssignment(
    assignment: ProjectAssignmentRecord,
    workItemID = selectedWorkItemID,
  ): Promise<boolean> {
    if (!selectedProjectID || !workItemID) return false;
    const projectID = selectedProjectID;
    const isCurrent = () =>
      selectedProjectIDRef.current === projectID && selectedWorkItemIDRef.current === workItemID;
    const assignmentKey = projectAssignmentStartKey(projectID, workItemID, assignment.id);
    const existingStart = startingAssignmentPromisesRef.current.get(assignmentKey);
    if (existingStart) return existingStart;

    setStartingAssignmentKeys((current) => {
      const next = new Set(current);
      next.add(assignmentKey);
      return next;
    });
    setAssignmentErrors((current) => ({ ...current, [assignment.id]: "" }));
    const pendingStart = (async () => {
      try {
        const res = await startProjectAssignment(
          projectID,
          workItemID,
          assignment.id,
          assignment.driver_kind || "hecate_task",
        );
        if (isCurrent()) {
          setAssignments((current) => upsertAssignment(current, res.data));
          openPreparedExternalAgentChat(res.data, workItemID);
          await loadWorkForProject(projectID, workItemID);
          if (isCurrent()) await loadWorkItemDetail(projectID, workItemID);
        }
        return true;
      } catch (error) {
        if (isCurrent()) {
          setAssignmentErrors((current) => ({
            ...current,
            [assignment.id]: errorMessage(error, "Failed to start assignment."),
          }));
          if (error instanceof ApiError && error.status === 409) {
            await loadWorkForProject(projectID, workItemID);
            if (isCurrent()) await loadWorkItemDetail(projectID, workItemID);
          }
        }
        return false;
      } finally {
        startingAssignmentPromisesRef.current.delete(assignmentKey);
        setStartingAssignmentKeys((current) => {
          const next = new Set(current);
          next.delete(assignmentKey);
          return next;
        });
      }
    })();
    startingAssignmentPromisesRef.current.set(assignmentKey, pendingStart);
    return pendingStart;
  }

  async function handleSetAssignmentStatus(
    assignment: ProjectAssignmentRecord,
    status: EditAssignmentForm["status"],
    workItemID = selectedWorkItemID,
  ): Promise<boolean> {
    if (!selectedProjectID || !workItemID) return false;
    const projectID = selectedProjectID;
    const isCurrent = () =>
      selectedProjectIDRef.current === projectID && selectedWorkItemIDRef.current === workItemID;
    const assignmentKey = projectAssignmentStartKey(projectID, workItemID, assignment.id);
    const existingMutation = startingAssignmentPromisesRef.current.get(assignmentKey);
    if (existingMutation) return existingMutation;

    setStartingAssignmentKeys((current) => {
      const next = new Set(current);
      next.add(assignmentKey);
      return next;
    });
    setAssignmentErrors((current) => ({ ...current, [assignment.id]: "" }));
    const pendingCompletion = (async () => {
      try {
        const res = await updateProjectAssignment(projectID, workItemID, assignment.id, {
          status,
        });
        if (isCurrent()) {
          setAssignments((current) => upsertAssignment(current, res.data));
          await loadWorkForProject(projectID, workItemID);
          if (isCurrent()) await loadWorkItemDetail(projectID, workItemID);
          void refreshProjectOverview(projectID);
        }
        return true;
      } catch (error) {
        if (isCurrent()) {
          setAssignmentErrors((current) => ({
            ...current,
            [assignment.id]: errorMessage(error, "Failed to update Human work progress."),
          }));
          if (error instanceof ApiError && error.status === 409) {
            await loadWorkForProject(projectID, workItemID);
            if (isCurrent()) await loadWorkItemDetail(projectID, workItemID);
          }
        }
        return false;
      } finally {
        startingAssignmentPromisesRef.current.delete(assignmentKey);
        setStartingAssignmentKeys((current) => {
          const next = new Set(current);
          next.delete(assignmentKey);
          return next;
        });
      }
    })();
    startingAssignmentPromisesRef.current.set(assignmentKey, pendingCompletion);
    return pendingCompletion;
  }

  function openPreparedExternalAgentChat(assignment: ProjectAssignmentRecord, workItemID: string) {
    if (!onOpenChat || assignment.driver_kind !== "external_agent") return;
    const project =
      selectedProject?.id === selectedProjectID
        ? selectedProject
        : projects.state.projects.find((item) => item.id === selectedProjectID);
    if (!project) return;
    const chatSessionID = assignment.execution_ref?.chat_session_id?.trim();
    if (!chatSessionID) return;
    const workItem = (selectedWorkItem?.id === workItemID ? selectedWorkItem : null) ??
      workItems.find((item) => item.id === workItemID) ?? {
        id: workItemID,
        project_id: project.id,
        title: workItemID,
        status: "",
        priority: "",
        created_at: "",
        updated_at: "",
      };
    const request = buildProjectAssignmentChatLaunchRequest({
      project,
      workItem,
      assignment,
      role: roleByID.get(assignment.role_id) ?? null,
    });
    onOpenChat({
      ...request,
      chatSessionID,
    });
  }

  const detailIdentityCurrent =
    detailTarget?.projectID === selectedProjectID && detailTarget.workItemID === selectedWorkItemID;
  const currentSelectedWorkItem =
    detailIdentityCurrent &&
    selectedWorkItem?.project_id === selectedProjectID &&
    selectedWorkItem.id === selectedWorkItemID
      ? selectedWorkItem
      : null;
  const currentAssignments = detailIdentityCurrent ? assignments : [];
  const currentArtifacts = detailIdentityCurrent ? artifacts : [];
  const currentHandoffs = detailIdentityCurrent ? handoffs : [];
  const currentSelectedWorkItemReadiness = detailIdentityCurrent ? selectedWorkItemReadiness : null;
  const currentEditingWorkItem =
    editingWorkItem?.id === currentSelectedWorkItem?.id ? editingWorkItem : null;
  const currentEditingAssignment =
    editingAssignment?.work_item_id === currentSelectedWorkItem?.id ? editingAssignment : null;
  const currentDeleteWorkItem =
    deleteWorkItem?.id === currentSelectedWorkItem?.id ? deleteWorkItem : null;
  const currentDeleteAssignment =
    deleteAssignment?.work_item_id === currentSelectedWorkItem?.id ? deleteAssignment : null;
  const currentDetailLoadState = detailIdentityCurrent
    ? detailLoadState
    : selectedWorkItemID
      ? "loading"
      : "idle";
  const currentDetailError = detailIdentityCurrent ? detailError : "";
  const preparingAssignmentID =
    preparingAssignmentTarget?.projectID === selectedProjectID &&
    preparingAssignmentTarget.workItemID === selectedWorkItemID
      ? preparingAssignmentTarget.assignmentID
      : "";
  const startingAssignmentIDs = new Set(
    currentAssignments
      .filter((assignment) =>
        startingAssignmentKeys.has(
          projectAssignmentStartKey(selectedProjectID, selectedWorkItemID, assignment.id),
        ),
      )
      .map((assignment) => assignment.id),
  );
  const hasWorkItemDetail =
    Boolean(selectedWorkItemID) || detailLoadState === "loading" || Boolean(detailError);
  const projectSetupPending = Boolean(
    selectedProject &&
    (loadedProjectID !== selectedProject.id ||
      (projectSetupReadinessLoadState === "loading" && workItems.length === 0)),
  );
  const projectSetupError =
    selectedProject && workItems.length === 0
      ? projectSetupReadinessLoadState === "error"
        ? projectSetupReadinessError
        : workLoadState === "error" &&
            (!projectSetupReadiness || projectSetupReadiness.summary.work_item_count === 0)
          ? workError
          : ""
      : "";
  const projectNeedsOnboarding =
    Boolean(selectedProject && projectSetupReadiness?.show_onboarding) &&
    workLoadState === "loaded" &&
    workItems.length === 0 &&
    !assistant.proposal &&
    !assistant.applyResult;

  useEffect(() => {
    if (
      !workspaceTabFocusTarget ||
      workspaceTabFocusTarget !== workspaceTab ||
      projectSetupPending ||
      projectSetupError ||
      projectNeedsOnboarding
    ) {
      return;
    }
    const tab = document.getElementById(`project-workspace-tab-${workspaceTabFocusTarget}`);
    if (!tab) return;
    tab.focus();
    setWorkspaceTabFocusTarget(null);
  }, [
    projectNeedsOnboarding,
    projectSetupError,
    projectSetupPending,
    workspaceTab,
    workspaceTabFocusTarget,
  ]);
  const projectEmptyTitle =
    projects.state.projects.length === 0 ? "Add a project to begin" : "Select a project";
  const projectEmptyDetail =
    projects.state.projects.length === 0
      ? "Create a project from a name and purpose. A local folder is optional and can be attached now or later."
      : "Choose a project from the list to view its work, memory, skills, and settings.";

  return (
    <div className="projects-cockpit-shell" style={shellStyle}>
      <section className="projects-cockpit-index" style={sidePanelStyle} aria-label="Projects">
        <div style={topbarStyle}>
          <div>
            <div style={sidebarSectionLabelStyle}>Projects</div>
            <div style={subtleTextStyle}>
              {projects.state.projects.length}{" "}
              {projects.state.projects.length === 1 ? "record" : "records"}
            </div>
          </div>
          <div style={topbarActionsStyle}>
            <button
              className={`btn ${selectedProject ? "btn-ghost" : "btn-primary"} btn-sm`}
              type="button"
              onClick={() => {
                setCreateProjectError("");
                projects.actions.setError("");
                setCreateProjectOpen(true);
              }}
            >
              <Icon d={Icons.folder} size={13} />
              Add
            </button>
          </div>
        </div>
        {projects.state.error && (
          <div style={{ padding: 10 }}>
            <InlineError message={projects.state.error} />
          </div>
        )}
        <div
          className="projects-cockpit-index-list"
          style={{ flex: 1, minHeight: 0, overflowY: "auto" }}
        >
          {projects.state.loading && projects.state.projects.length === 0 && (
            <ProjectEmptyBlock
              title="Loading projects…"
              detail="Checking the local project catalog."
            />
          )}
          {!projects.state.loading && projects.state.projects.length === 0 && (
            <ProjectEmptyBlock
              title="No projects yet"
              detail="Create a project for any durable work area. Add a folder only when the project needs local files or code."
            />
          )}
          {projects.state.projects.map((project) => (
            <ProjectIndexRow
              key={project.id}
              active={project.id === selectedProjectID}
              project={project}
              renaming={renamingProjectID === project.id}
              renameValue={renameValue}
              actionsVisible={hoveredProjectID === project.id}
              onInteractionChange={(active) => {
                setHoveredProjectID(active ? project.id : "");
              }}
              onRenameChange={setRenameValue}
              onRenameCancel={() => setRenamingProjectID("")}
              onRenameCommit={() => void commitRename(project)}
              onRenameStart={() => startRename(project)}
              onDelete={() => setDeleteProjectID(project.id)}
              onOpen={() => openProject(project.id)}
            />
          ))}
        </div>
      </section>

      <div className="projects-cockpit-main" style={projectMainStyle}>
        <ProjectHeader
          attentionItems={projectHealth?.attention ?? []}
          omittedAttentionCount={projectHealth?.summary?.omitted_attention_count ?? 0}
          healthSummary={projectHealth?.summary}
          memoryCandidates={memoryCandidates}
          project={selectedProject}
          onAttentionBucket={(bucket) => {
            setActivityBucket(bucket);
            navigateWorkspaceTab("work");
          }}
          onAttentionDefaults={() => {
            setDefaultsError("");
            setSettingsPanelOpen(true);
          }}
          onAttentionError={setWorkError}
          onAttentionMemory={() => navigateWorkspaceTab("memory")}
          onAttentionPresets={() => {
            setPresetsError("");
            setAgentPresetsModalOpen(true);
          }}
          onAttentionReviewCandidate={setPromotingCandidate}
          onAttentionRoles={() => {
            setRolesError("");
            setRolesModalOpen(true);
          }}
          onAttentionSkills={() => navigateWorkspaceTab("skills")}
          onAttentionTask={onOpenTask}
          onAttentionWorkItem={(workItemID) => {
            setSelectedWorkItemOperationID("");
            navigateWorkspaceTab("work");
            setSelectedWorkItemID(workItemID);
          }}
          onRefresh={refreshSelectedWorkItem}
          settingsOpen={settingsPanelOpen}
          onEditDefaults={() => {
            setDefaultsError("");
            setSettingsPanelOpen((open) => !open);
          }}
          onManagePresets={() => {
            setPresetsError("");
            setAgentPresetsModalOpen(true);
          }}
          onManageRoles={() => {
            setRolesError("");
            setRolesModalOpen(true);
          }}
        />
        <div style={projectMainBodyStyle}>
          <ProjectWorkspaceView
            activity={activity}
            activityLoadState={activityLoadState}
            activityBucket={activityBucket}
            activityByAssignmentID={activityByAssignmentID}
            artifacts={currentArtifacts}
            artifactActionID={artifactActionID}
            assignmentErrors={assignmentErrors}
            assignments={currentAssignments}
            assistant={assistant}
            draftingDefaultAssignment={assistant.status === "proposing"}
            detailError={currentDetailError}
            detailLoadState={currentDetailLoadState}
            discoveringContext={discoveringContext}
            discoveringSkills={discoveringSkills}
            handoffActionID={handoffActionID}
            handoffError={handoffError}
            handoffs={currentHandoffs}
            hasWorkItemDetail={hasWorkItemDetail}
            closingWorkItemID={closingWorkItemID}
            memoryCandidates={memoryCandidates}
            memoryEntries={memoryEntries}
            memoryError={memoryError}
            memoryLoadState={memoryLoadState}
            onActivityBucketChange={setActivityBucket}
            onAddAssignment={() => {
              setNewAssignmentError("");
              setNewAssignmentModalOpen(true);
            }}
            onAddEvidenceLink={(assignmentID = "") => {
              setEvidenceLinkError("");
              setEvidenceLinkAssignmentID(assignmentID);
              setEvidenceLinkModalOpen(true);
            }}
            onAddHandoff={() => {
              setHandoffError("");
              setNewHandoffDraft(null);
              setEditingHandoff("new");
            }}
            onAddHandoffFromAssignment={(assignment, activityItem) => {
              setHandoffError("");
              setNewHandoffDraft(
                handoffFormFromAssignment(
                  assignment,
                  roleByID.get(assignment.role_id) ?? null,
                  activityItem,
                ),
              );
              setEditingHandoff("new");
            }}
            onAddReviewHandoffFromAssignment={(assignment, reviewRole, activityItem) => {
              if (!currentSelectedWorkItem) return;
              setHandoffError("");
              setNewHandoffDraft(
                reviewHandoffFormFromAssignment(
                  assignment,
                  roleByID.get(assignment.role_id) ?? null,
                  reviewRole,
                  currentSelectedWorkItem,
                  activityItem,
                ),
              );
              setEditingHandoff("new");
            }}
            onAddReviewArtifactFromAssignment={(assignment) => {
              if (!currentSelectedWorkItem) return;
              setReviewArtifactError("");
              setReviewArtifactDraft(
                reviewArtifactFormFromAssignment(
                  assignment,
                  roleByID.get(assignment.role_id) ?? null,
                  currentSelectedWorkItem,
                  currentHandoffs,
                ),
              );
            }}
            onAddHandoffFromReviewArtifact={(artifact) => {
              if (!currentSelectedWorkItem) return;
              setHandoffError("");
              setNewHandoffDraft(handoffFormFromReviewArtifact(artifact, currentSelectedWorkItem));
              setEditingHandoff("new");
            }}
            onDraftDefaultAssignment={handleDraftDefaultAssignment}
            onPreparedAssignmentPreflightOpened={(assignmentID) => {
              setPreparingAssignmentTarget((current) =>
                current?.assignmentID === assignmentID &&
                current.projectID === selectedProjectIDRef.current &&
                current.workItemID === selectedWorkItemIDRef.current
                  ? null
                  : current,
              );
            }}
            onCreateAssignmentFromReviewArtifact={(artifactID) =>
              void handleCreateAssignmentFromReviewArtifact(artifactID)
            }
            onCreateAssignmentFromHandoff={handleCreateAssignmentFromHandoff}
            onCreateWork={openNewWorkItemModal}
            onCloseWorkItem={(item) => void handleCloseWorkItem(item)}
            onDeleteAssignment={setDeleteAssignment}
            onDeleteHandoff={(handoff) => void handleDeleteHandoff(handoff)}
            onDeleteMemory={setDeleteMemory}
            onDeleteSource={setDeleteSource}
            onDeleteWorkItem={setDeleteWorkItem}
            onDiscoverContextSources={handleDiscoverContextSources}
            onDiscoverProjectSkills={handleDiscoverProjectSkills}
            onEditAssignment={(assignment) => {
              setEditAssignmentError("");
              setEditingAssignment(assignment);
            }}
            onEditHandoff={(handoff) => {
              setHandoffError("");
              setNewHandoffDraft(null);
              setEditingHandoff(handoff);
            }}
            onEditMemory={setEditingMemory}
            onEditSource={(source) => {
              setSourceError("");
              setEditingSource(source);
            }}
            onEditWorkItem={(item) => {
              setEditWorkError("");
              setEditingWorkItem(item);
            }}
            onNewMemory={() => setEditingMemory("new")}
            onNewSource={() => {
              setSourceError("");
              setEditingSource("new");
            }}
            onOpenChat={onOpenChat}
            onOpenConnections={onOpenConnections}
            onManagePresets={() => {
              setPresetsError("");
              setAgentPresetsModalOpen(true);
            }}
            onManageRoles={() => {
              setRolesError("");
              setRolesModalOpen(true);
            }}
            onOpenSettings={() => {
              setDefaultsError("");
              setSettingsPanelOpen(true);
            }}
            onOperationAction={handleOperationsBriefAction}
            onOpenTask={onOpenTask}
            onPromoteCandidate={setPromotingCandidate}
            onRefreshMemory={() => {
              void (async () => {
                await loadProjectMemory(selectedProjectID);
                await refreshProjectOverview(selectedProjectID);
              })();
            }}
            onRefreshProjectSkills={() => {
              void (async () => {
                await loadProjectSkills(selectedProjectID);
                await refreshProjectOverview(selectedProjectID);
              })();
            }}
            onRefreshWorkItem={refreshSelectedWorkItem}
            onRejectCandidate={handleRejectCandidate}
            onSelectWorkItem={handleSelectWorkItem}
            onSetHandoffStatus={(handoff, status) => void handleSetHandoffStatus(handoff, status)}
            onSetAssignmentStatus={(assignment, status) =>
              void handleSetAssignmentStatus(assignment, status as EditAssignmentForm["status"])
            }
            onStartAssignment={handleStartAssignment}
            onSetupReadinessAction={handleSetupReadinessAction}
            onUpdateProjectSkill={(skill, patch) => void handleUpdateProjectSkill(skill, patch)}
            onNavigateWorkspaceTab={navigateWorkspaceTab}
            onWorkspaceTabChange={setWorkspaceTab}
            project={selectedProject}
            projectEmptyDetail={projectEmptyDetail}
            projectEmptyTitle={projectEmptyTitle}
            projectNeedsOnboarding={projectNeedsOnboarding}
            projectSetupError={projectSetupError}
            projectSetupPending={projectSetupPending}
            projectSetupReadiness={projectSetupReadiness}
            overviewError={overviewProjectionError}
            operationsBrief={operationsBrief}
            operationsBriefError={operationsBriefError}
            operationsBriefLoadState={operationsBriefLoadState}
            projectSkills={projectSkills}
            preparingAssignmentID={preparingAssignmentID}
            rejectingCandidateID={rejectingCandidateID}
            roleByID={roleByID}
            roles={roles}
            selectedWorkItem={currentSelectedWorkItem}
            selectedWorkItemOperationID={selectedWorkItemOperationID}
            selectedWorkItemReadiness={currentSelectedWorkItemReadiness}
            selectedWorkItemID={selectedWorkItemID}
            skillsError={skillsError}
            skillsLoadState={skillsLoadState}
            startingAssignmentIDs={startingAssignmentIDs}
            updatingSkillID={updatingSkillID}
            workError={workError}
            workItemSummaries={workItemSummaries}
            workItems={workItems}
            workLoadState={workLoadState}
            workItemFocusTarget={workItemFocusTarget}
            onWorkItemFocusTargetHandled={() => setWorkItemFocusTarget(null)}
            workspaceTab={workspaceTab}
          />

          {selectedProject && settingsPanelOpen && (
            <ChatRightPanel
              ariaLabel="Project settings panel"
              width={rightPanelWidth}
              onWidthChange={setRightPanelWidth}
            >
              <ProjectSettingsPanel
                agentPresets={agentPresets}
                agentPresetsError={agentPresetsError}
                error={defaultsError}
                models={providersAndModels.state.models}
                pending={defaultsPending}
                providerOptions={providerOptions}
                providerPresets={providerPresets}
                project={selectedProject}
                rootsPending={discoveringRoots}
                onDiscoverRoots={handleDiscoverProjectRoots}
                onOpenCreateWorktree={() => {
                  setCreateWorktreeError("");
                  setCreateWorktreeOpen(true);
                }}
                onSave={handleSaveProjectDefaults}
              />
            </ChatRightPanel>
          )}
        </div>

        {selectedProject && rolesModalOpen && (
          <RolesModal
            agentPresets={agentPresets}
            error={rolesError}
            pending={rolesPending}
            projectSkills={projectSkills}
            roles={roles}
            onClose={() => setRolesModalOpen(false)}
            onCreate={handleCreateRole}
            onDelete={handleDeleteRole}
            onUpdate={handleUpdateRole}
          />
        )}

        {selectedProject && presetsModalOpen && (
          <AgentPresetsModal
            error={presetsError}
            pending={presetsPending}
            presets={agentPresets}
            project={selectedProject}
            projectSkills={projectSkills}
            roles={roles}
            onClose={() => setAgentPresetsModalOpen(false)}
            onCreate={handleCreateAgentPreset}
            onDelete={handleDeleteAgentPreset}
            onUpdate={handleUpdateAgentPreset}
          />
        )}

        {selectedProject && newWorkModalOpen && (
          <NewWorkItemModal
            error={newWorkError}
            pending={newWorkPending}
            project={selectedProject}
            initialDraft={newWorkDraft}
            roles={roles}
            onClose={() => {
              setNewWorkDraft(undefined);
              setNewWorkModalOpen(false);
            }}
            onCreate={handleCreateWorkItem}
          />
        )}

        {selectedProject && currentSelectedWorkItem && newAssignmentModalOpen && (
          <NewAssignmentModal
            error={newAssignmentError}
            pending={newAssignmentPending}
            project={selectedProject}
            workItem={currentSelectedWorkItem}
            roles={roles}
            onClose={() => setNewAssignmentModalOpen(false)}
            onCreate={handleCreateAssignment}
          />
        )}

        {selectedProject && currentEditingWorkItem && (
          <EditWorkItemModal
            error={editWorkError}
            item={currentEditingWorkItem}
            pending={editWorkPending}
            project={selectedProject}
            roles={roles}
            onClose={() => setEditingWorkItem(null)}
            onSave={handleUpdateWorkItem}
          />
        )}

        {selectedProject && currentSelectedWorkItem && currentEditingAssignment && (
          <EditAssignmentModal
            assignment={currentEditingAssignment}
            error={editAssignmentError}
            pending={editAssignmentPending}
            project={selectedProject}
            workItem={currentSelectedWorkItem}
            roles={roles}
            onClose={() => setEditingAssignment(null)}
            onSave={handleUpdateAssignment}
          />
        )}

        {selectedProject && createWorktreeOpen && (
          <CreateProjectWorktreeModal
            error={createWorktreeError}
            pending={createWorktreePending}
            project={selectedProject}
            onClose={() => setCreateWorktreeOpen(false)}
            onCreate={handleCreateWorktreeRoot}
          />
        )}

        {createProjectOpen && (
          <CreateProjectModal
            error={createProjectError || projects.state.error}
            pending={createProjectPending}
            onChooseWorkspace={handleChooseProjectWorkspace}
            onClose={() => {
              setCreateProjectOpen(false);
              setCreateProjectError("");
            }}
            onSave={handleCreateProject}
          />
        )}

        {editingMemory && (
          <ProjectMemoryModal
            key={editingMemory === "new" ? "new" : editingMemory.id}
            entry={editingMemory === "new" ? null : editingMemory}
            error={memoryError}
            pending={memoryPending}
            onClose={() => setEditingMemory(null)}
            onSave={handleSaveMemory}
          />
        )}
        {editingSource && (
          <ProjectSourceModal
            key={editingSource === "new" ? "new" : editingSource.id}
            source={editingSource === "new" ? null : editingSource}
            error={sourceError}
            pending={sourcePending}
            onClose={() => {
              setEditingSource(null);
              setSourceError("");
            }}
            onSave={handleSaveSource}
          />
        )}
        {promotingCandidate && (
          <ProjectMemoryModal
            key={promotingCandidate.id}
            candidate={promotingCandidate}
            entry={null}
            error={memoryError}
            pending={memoryPending}
            onClose={() => setPromotingCandidate(null)}
            onSave={handlePromoteCandidate}
          />
        )}

        {editingHandoff && currentSelectedWorkItem && (
          <ProjectHandoffModal
            key={editingHandoff === "new" ? "new" : editingHandoff.id}
            assignments={currentAssignments}
            handoff={editingHandoff === "new" ? null : editingHandoff}
            draft={editingHandoff === "new" ? newHandoffDraft : null}
            error={handoffError}
            pending={handoffPending}
            roles={roles}
            onClose={() => {
              setEditingHandoff(null);
              setNewHandoffDraft(null);
            }}
            onSave={handleSaveHandoff}
          />
        )}

        {reviewArtifactDraft && currentSelectedWorkItem && (
          <ProjectReviewArtifactModal
            key={`${reviewArtifactDraft.assignmentID}:${reviewArtifactDraft.authorRoleID}`}
            assignments={currentAssignments}
            draft={reviewArtifactDraft}
            error={reviewArtifactError}
            pending={reviewArtifactPending}
            roles={roles}
            onClose={() => {
              setReviewArtifactDraft(null);
              setReviewArtifactError("");
            }}
            onSave={handleSaveReviewArtifact}
          />
        )}

        {evidenceLinkModalOpen && currentSelectedWorkItem && (
          <ProjectEvidenceLinkModal
            key={evidenceLinkAssignmentID || "work-item"}
            assignments={currentAssignments}
            error={evidenceLinkError}
            initialAssignmentID={evidenceLinkAssignmentID}
            pending={evidenceLinkPending}
            roles={roles}
            onClose={() => {
              setEvidenceLinkModalOpen(false);
              setEvidenceLinkAssignmentID("");
              setEvidenceLinkError("");
            }}
            onSave={handleSaveEvidenceLink}
          />
        )}

        {pendingDeleteProject && (
          <ConfirmModal
            title="Delete project"
            danger
            pending={deletePending}
            confirmLabel="Delete project record"
            onClose={() => setDeleteProjectID("")}
            onConfirm={confirmDeleteProject}
            message={
              <>
                Hecate will delete the project record, project roots, project-scoped chats, and
                project work coordination state for <strong>{pendingDeleteProject.name}</strong>.
                Workspace files and the git repository are not deleted.
              </>
            }
          />
        )}

        {currentDeleteWorkItem && (
          <ConfirmModal
            title="Delete work item"
            danger
            pending={deleteWorkPending}
            confirmLabel="Delete work item"
            onClose={() => setDeleteWorkItem(null)}
            onConfirm={confirmDeleteWorkItem}
            message={
              <>
                Delete <strong>{currentDeleteWorkItem.title}</strong> and its assignments and
                collaboration artifacts. Linked tasks, runs, chats, workspace files, and git history
                are not deleted.
              </>
            }
          />
        )}

        {currentDeleteAssignment && (
          <ConfirmModal
            title="Delete assignment"
            danger
            pending={deleteAssignmentPending}
            confirmLabel="Delete assignment"
            onClose={() => setDeleteAssignment(null)}
            onConfirm={confirmDeleteAssignment}
            message={
              <>
                Delete the assignment record for{" "}
                <strong>
                  {roleByID.get(currentDeleteAssignment.role_id)?.name ??
                    currentDeleteAssignment.role_id}
                </strong>
                , including assignment-scoped evidence, reviews, and collaboration records. Linked
                tasks, runs, chats, and external-agent executions are not deleted or cancelled.
              </>
            }
          />
        )}

        {deleteMemory && (
          <ConfirmModal
            title="Delete project memory"
            danger
            pending={deleteMemoryPending}
            confirmLabel="Delete memory"
            onClose={() => setDeleteMemory(null)}
            onConfirm={confirmDeleteMemory}
            message={
              <>
                Delete <strong>{deleteMemory.title}</strong>. Historical context packets that
                already captured this memory stay unchanged.
              </>
            }
          />
        )}

        {deleteSource && (
          <ConfirmModal
            title="Delete project source"
            danger
            pending={deleteSourcePending}
            confirmLabel="Delete source"
            onClose={() => setDeleteSource(null)}
            onConfirm={confirmDeleteSource}
            message={
              <>
                Delete <strong>{deleteSource.title || deleteSource.path}</strong>. Historical
                context packets that already captured this source stay unchanged.
              </>
            }
          />
        )}
      </div>
    </div>
  );
}

function ProjectIndexRow({
  actionsVisible,
  active,
  project,
  renaming,
  renameValue,
  onInteractionChange,
  onRenameChange,
  onRenameCancel,
  onRenameCommit,
  onRenameStart,
  onDelete,
  onOpen,
}: {
  actionsVisible: boolean;
  active: boolean;
  project: ProjectRecord;
  renaming: boolean;
  renameValue: string;
  onInteractionChange: (active: boolean) => void;
  onRenameChange: (value: string) => void;
  onRenameCancel: () => void;
  onRenameCommit: () => void;
  onRenameStart: () => void;
  onDelete: () => void;
  onOpen: () => void;
}) {
  const updated = formatProjectRowRelativeTime(project.updated_at);
  return (
    <div
      className="project-index-row"
      role="button"
      tabIndex={0}
      aria-current={active ? "true" : undefined}
      aria-label={`Open project ${project.name}`}
      onBlur={(event) => {
        const nextFocus = event.relatedTarget;
        if (!(nextFocus instanceof Node) || !event.currentTarget.contains(nextFocus)) {
          onInteractionChange(false);
        }
      }}
      onClick={onOpen}
      onFocus={() => onInteractionChange(true)}
      onKeyDown={(event) => {
        if (event.target !== event.currentTarget) return;
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        onOpen();
      }}
      onMouseEnter={() => onInteractionChange(true)}
      onMouseLeave={() => onInteractionChange(false)}
      style={{
        padding: "8px 12px",
        borderBottom: "1px solid var(--border)",
        borderLeft: active ? "2px solid var(--teal)" : "2px solid transparent",
        background: active ? "var(--teal-bg)" : "transparent",
        cursor: "pointer",
        transition: "background 0.1s",
      }}
    >
      {renaming ? (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            event.stopPropagation();
            onRenameCommit();
          }}
          onClick={(event) => event.stopPropagation()}
          style={{ display: "flex", gap: 6 }}
        >
          <input
            className="input"
            aria-label={`Rename ${project.name}`}
            value={renameValue}
            onChange={(event) => onRenameChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Escape") onRenameCancel();
            }}
            autoFocus
          />
          <button className="btn btn-primary btn-sm" type="submit">
            Save
          </button>
        </form>
      ) : (
        <>
          <div style={projectIndexTitleRowStyle}>
            <div
              style={{
                ...projectIndexTitleStyle,
                color: active ? "var(--t0)" : "var(--t1)",
                fontWeight: active ? 500 : 400,
              }}
            >
              {project.name}
            </div>
            <div
              style={{
                ...projectIndexActionsStyle,
                opacity: actionsVisible ? 1 : 0,
              }}
            >
              <button
                className="btn btn-ghost btn-sm"
                type="button"
                aria-label={`Rename project ${project.name}`}
                title="Rename"
                onClick={(event) => {
                  event.stopPropagation();
                  onRenameStart();
                }}
                style={projectIndexActionButtonStyle}
              >
                <Icon d={Icons.edit} size={10} />
              </button>
              <button
                className="btn btn-ghost btn-sm"
                type="button"
                aria-label={`Delete project ${project.name}`}
                title="Delete"
                onClick={(event) => {
                  event.stopPropagation();
                  onDelete();
                }}
                style={{
                  ...projectIndexActionButtonStyle,
                  color: "var(--red)",
                }}
              >
                <Icon d={Icons.trash} size={10} />
              </button>
            </div>
          </div>
          <div style={projectIndexMetaStyle}>
            <Icon d={Icons.folder} size={12} />
            <span>Updated {updated}</span>
          </div>
        </>
      )}
    </div>
  );
}

function ProjectHeader({
  attentionItems,
  healthSummary,
  omittedAttentionCount,
  memoryCandidates,
  project,
  settingsOpen,
  onAttentionBucket,
  onAttentionDefaults,
  onAttentionError,
  onAttentionMemory,
  onAttentionPresets,
  onAttentionReviewCandidate,
  onAttentionRoles,
  onAttentionSkills,
  onAttentionTask,
  onAttentionWorkItem,
  onEditDefaults,
  onManagePresets,
  onManageRoles,
  onRefresh,
}: {
  attentionItems: ProjectHealthAttention[];
  healthSummary?: ProjectHealth["summary"];
  omittedAttentionCount: number;
  memoryCandidates: ProjectMemoryCandidateRecord[];
  project: ProjectRecord | null;
  settingsOpen: boolean;
  onAttentionBucket: (bucket: ProjectActivityBucketKey) => void;
  onAttentionDefaults: () => void;
  onAttentionError?: (message: string) => void;
  onAttentionMemory: () => void;
  onAttentionPresets: () => void;
  onAttentionReviewCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onAttentionRoles: () => void;
  onAttentionSkills: () => void;
  onAttentionTask?: (taskID: string, runID?: string) => void;
  onAttentionWorkItem: (workItemID: string) => void;
  onEditDefaults: () => void;
  onManagePresets: () => void;
  onManageRoles: () => void;
  onRefresh: () => void;
}) {
  const workspace = project ? projectDefaultWorkspace(project) : "";
  const subline = project
    ? `${workspace || "No local files attached"}${project.default_model ? ` · ${project.default_model}` : ""}`
    : "";
  return (
    <div style={projectInlineHeaderStyle}>
      <div style={projectHeaderAvatarStyle} title="Project">
        <Icon d={Icons.folder} size={14} strokeWidth={1.7} />
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={projectHeaderTitleStyle}>{project?.name ?? "No project selected"}</div>
        {subline && (
          <div style={projectHeaderSublineStyle} title={subline}>
            {subline}
          </div>
        )}
      </div>
      {project && (
        <div aria-label="Project header actions" style={projectHeaderActionsStyle}>
          <ProjectHealthPanel
            attentionItems={attentionItems}
            disabled={!project}
            memoryCandidates={memoryCandidates}
            omittedAttentionCount={omittedAttentionCount}
            selectedProjectID={project.id}
            summary={healthSummary}
            onAttentionBucket={onAttentionBucket}
            onAttentionDefaults={onAttentionDefaults}
            onAttentionError={onAttentionError}
            onAttentionMemory={onAttentionMemory}
            onAttentionPresets={onAttentionPresets}
            onAttentionReviewCandidate={onAttentionReviewCandidate}
            onAttentionRoles={onAttentionRoles}
            onAttentionSkills={onAttentionSkills}
            onAttentionTask={onAttentionTask}
            onAttentionWorkItem={onAttentionWorkItem}
            triggerStyle={projectHeaderActionButtonStyle}
          />
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-label="Roles"
            title="Project roles"
            onClick={onManageRoles}
            disabled={!project}
            style={projectHeaderActionButtonStyle}
          >
            <Icon d={Icons.user} size={13} />
          </button>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-label="Agent presets"
            title="Agent presets"
            onClick={onManagePresets}
            disabled={!project}
            style={projectHeaderActionButtonStyle}
          >
            <Icon d={Icons.model} size={13} />
          </button>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-expanded={settingsOpen}
            aria-label="Project settings"
            title="Project settings"
            onClick={onEditDefaults}
            disabled={!project}
            style={{
              ...projectHeaderActionButtonStyle,
              background: settingsOpen ? "var(--teal-bg)" : "transparent",
              color: settingsOpen ? "var(--teal)" : "var(--t2)",
            }}
          >
            <Icon d={Icons.settings} size={13} />
          </button>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-label="Refresh project work"
            title="Refresh"
            onClick={onRefresh}
            disabled={!project}
            style={projectHeaderActionButtonStyle}
          >
            <Icon d={Icons.refresh} size={13} />
          </button>
        </div>
      )}
    </div>
  );
}

function upsertRole(items: ProjectWorkRoleRecord[], item: ProjectWorkRoleRecord) {
  const index = items.findIndex((current) => current.id === item.id);
  const next = index === -1 ? [item, ...items] : items.slice();
  if (index !== -1) {
    next[index] = item;
  }
  return next.sort((a, b) => {
    if (a.built_in !== b.built_in) return a.built_in ? -1 : 1;
    return a.name.localeCompare(b.name) || a.id.localeCompare(b.id);
  });
}

function upsertAgentPreset(items: AgentPresetRecord[], item: AgentPresetRecord) {
  const index = items.findIndex((current) => current.id === item.id);
  const next = index === -1 ? [item, ...items] : items.slice();
  if (index !== -1) {
    next[index] = item;
  }
  return next.sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
}

function upsertProjectSkill(items: ProjectSkillRecord[], item: ProjectSkillRecord) {
  const index = items.findIndex((current) => current.id === item.id);
  const next = index === -1 ? [item, ...items] : items.slice();
  if (index !== -1) {
    next[index] = item;
  }
  return next.sort((a, b) => {
    if (a.enabled !== b.enabled) return a.enabled ? -1 : 1;
    const rank = projectSkillStatusRank(a.status) - projectSkillStatusRank(b.status);
    return (
      rank ||
      a.title.localeCompare(b.title) ||
      a.path.localeCompare(b.path) ||
      a.id.localeCompare(b.id)
    );
  });
}

function upsertWorkItem(items: ProjectWorkItemRecord[], item: ProjectWorkItemRecord) {
  const index = items.findIndex((current) => current.id === item.id);
  if (index === -1) return [item, ...items];
  const next = items.slice();
  next[index] = item;
  return next;
}

function upsertProject(items: ProjectRecord[], item: ProjectRecord) {
  const index = items.findIndex((current) => current.id === item.id);
  if (index === -1) return [item, ...items];
  const next = items.slice();
  next[index] = item;
  return next;
}

function upsertMemory(items: ProjectMemoryRecord[], item: ProjectMemoryRecord) {
  const index = items.findIndex((current) => current.id === item.id);
  const next = index === -1 ? [item, ...items] : items.slice();
  if (index !== -1) {
    next[index] = item;
  }
  return next.sort((a, b) => {
    if (a.enabled !== b.enabled) return a.enabled ? -1 : 1;
    return b.updated_at.localeCompare(a.updated_at) || a.title.localeCompare(b.title);
  });
}

function upsertAssignment(items: ProjectAssignmentRecord[], item: ProjectAssignmentRecord) {
  const index = items.findIndex((current) => current.id === item.id);
  if (index === -1) return [item, ...items];
  const next = items.slice();
  next[index] = item;
  return next;
}

function projectAssignmentStartKey(projectID: string, workItemID: string, assignmentID: string) {
  return JSON.stringify([projectID, workItemID, assignmentID]);
}

function upsertArtifact(
  items: ProjectCollaborationArtifactRecord[],
  item: ProjectCollaborationArtifactRecord,
) {
  const index = items.findIndex((current) => current.id === item.id);
  const next = index === -1 ? [item, ...items] : items.slice();
  if (index !== -1) {
    next[index] = item;
  }
  return next.sort((left, right) => {
    const byTime = (right.updated_at || right.created_at).localeCompare(
      left.updated_at || left.created_at,
    );
    return byTime || left.id.localeCompare(right.id);
  });
}

function upsertHandoff(items: ProjectHandoffRecord[], item: ProjectHandoffRecord) {
  const index = items.findIndex((current) => current.id === item.id);
  const next = index === -1 ? [item, ...items] : items.slice();
  if (index !== -1) {
    next[index] = item;
  }
  return next.sort((left, right) => {
    const byTime = (right.updated_at || right.created_at).localeCompare(
      left.updated_at || left.created_at,
    );
    return byTime || left.id.localeCompare(right.id);
  });
}

export function buildFirstWorkItemDraft({
  memoryCandidates,
  project,
  projectSkills,
  roles,
  workItems,
}: {
  memoryCandidates: ProjectMemoryCandidateRecord[];
  project: ProjectRecord | null;
  projectSkills: ProjectSkillRecord[];
  roles: ProjectWorkRoleRecord[];
  workItems: ProjectWorkItemRecord[];
}): Partial<NewWorkItemForm> | undefined {
  if (!project || workItems.length > 0) return undefined;

  const enabledSources = (project.context_sources ?? []).filter((source) => source.enabled);
  const availableSkills = projectSkills.filter(
    (skill) => skill.enabled && skill.status === "available",
  );
  const pendingCandidates = memoryCandidates.filter((candidate) => candidate.status === "pending");
  const hasSetupContext =
    Boolean(project.description?.trim()) ||
    enabledSources.length > 0 ||
    availableSkills.length > 0 ||
    pendingCandidates.length > 0 ||
    roles.length > 0;
  if (!hasSetupContext) return undefined;

  const ownerRole = preferredFirstWorkRole(roles);
  const lines = [
    `Use the project setup context to define the first reviewable work item for ${project.name}.`,
  ];
  if (project.description?.trim()) lines.push(`Purpose: ${project.description.trim()}`);
  if (enabledSources.length > 0) {
    lines.push(`Guidance: ${enabledSources.slice(0, 3).map(contextSourceLabel).join(", ")}`);
  }
  if (availableSkills.length > 0) {
    lines.push(
      `Relevant skills: ${availableSkills
        .slice(0, 4)
        .map((skill) => skill.title || skill.id)
        .join(", ")}`,
    );
  }
  if (pendingCandidates.length > 0) {
    lines.push(
      `Review memory candidates before relying on them: ${pendingCandidates
        .slice(0, 3)
        .map((candidate) => candidate.title)
        .join(", ")}`,
    );
  }
  if (roles.length > 0) {
    lines.push(
      `Suggested owner: ${ownerRole?.name || ownerRole?.id || roles[0]?.name || roles[0]?.id}`,
    );
  }

  return {
    title: `Plan first work for ${project.name}`,
    brief: lines.join("\n"),
    ownerRoleID: ownerRole?.id ?? "",
    priority: "normal",
    rootID: "",
  };
}

function preferredFirstWorkRole(roles: ProjectWorkRoleRecord[]) {
  return (
    roles.find((role) => /architect|planner|manager|lead/i.test(`${role.id} ${role.name}`)) ??
    roles.find((role) => role.id === "software_developer") ??
    roles[0] ??
    null
  );
}

function contextSourceLabel(source: ProjectContextSourceRecord) {
  return source.title?.trim() || source.path || source.kind;
}

const topbarStyle: CSSProperties = {
  minHeight: "var(--topbar-h)",
  padding: "8px 10px",
  borderBottom: "1px solid var(--border)",
  display: "flex",
  alignItems: "center",
  justifyContent: "space-between",
  gap: 8,
  flexShrink: 0,
};

const topbarActionsStyle: CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 6,
};

const projectIndexTitleRowStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  gap: 7,
  minHeight: 22,
  minWidth: 0,
};

const projectIndexTitleStyle: CSSProperties = {
  flex: 1,
  fontSize: 12,
  lineHeight: "18px",
  minWidth: 0,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const projectIndexActionsStyle: CSSProperties = {
  display: "flex",
  flexShrink: 0,
  gap: 1,
  transition: "opacity 0.15s",
};

const projectIndexActionButtonStyle: CSSProperties = {
  padding: "1px 3px",
};

const projectIndexMetaStyle: CSSProperties = {
  alignItems: "center",
  color: "var(--t3)",
  display: "flex",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  gap: 6,
  marginTop: 1,
  minWidth: 0,
};

const sidebarSectionLabelStyle: CSSProperties = {
  color: "var(--t3)",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  letterSpacing: "0.08em",
  textTransform: "uppercase",
};

const subtleTextStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 12,
  lineHeight: 1.4,
};

const projectInlineHeaderStyle: CSSProperties = {
  alignItems: "center",
  borderBottom: "1px solid var(--border)",
  background: "var(--bg1)",
  display: "flex",
  flexShrink: 0,
  gap: 8,
  height: "var(--topbar-h)",
  minWidth: 0,
  padding: "0 12px",
};

const projectHeaderAvatarStyle: CSSProperties = {
  alignItems: "center",
  color: "var(--t2)",
  display: "inline-flex",
  flexShrink: 0,
  height: 24,
  justifyContent: "center",
  width: 24,
};

const projectHeaderTitleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 13,
  fontWeight: 500,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const projectHeaderSublineStyle: CSSProperties = {
  color: "var(--t3)",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  lineHeight: 1.25,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const projectHeaderActionsStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexShrink: 0,
  gap: 4,
};

const projectHeaderActionButtonStyle: CSSProperties = {
  position: "relative",
  boxShadow: "none",
  color: "var(--t2)",
  height: 30,
  justifyContent: "center",
  minWidth: 30,
  padding: 0,
};

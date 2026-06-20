import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";

import { useProjects } from "../../app/state/projects";
import { useProvidersAndModels } from "../../app/state/providersAndModels";
import { useSettings } from "../../app/state/settings";
import {
  ApiError,
  chooseWorkspaceDirectory,
  createAgentProfile,
  createProjectAssignment,
  createProjectCollaborationArtifact,
  createProjectHandoff,
  discoverProjectContextSources,
  discoverProjectRoots,
  discoverProjectSkills,
  createProjectWorktreeRoot,
  createProjectMemory,
  createProjectWorkRole,
  createProjectWorkItem,
  deleteProjectAssignment,
  deleteProjectHandoff,
  deleteProjectMemory,
  deleteProjectWorkRole,
  deleteProjectWorkItem,
  getProjectActivity,
  getAgentProfiles,
  getProjectAssignments,
  getProjectCollaborationArtifacts,
  getProjectHandoffs,
  getProjectHealth,
  getProjectMemory,
  getProjectMemoryCandidates,
  getProjectOperationsBrief,
  getProjectSkills,
  getProjectWorkItem,
  getProjectWorkItemReadiness,
  getProjectWorkItems,
  getProjectWorkRoles,
  startProjectAssignment,
  deleteAgentProfile,
  promoteProjectMemoryCandidate,
  rejectProjectMemoryCandidate,
  updateProject,
  updateAgentProfile,
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
import { ProjectMemoryModal, ProjectSourceModal, type MemoryForm } from "./ProjectMemoryPanel";
import { ProjectReviewArtifactModal } from "./ProjectReviewArtifactModal";
import { type ProjectAssignmentChatLaunchRequest } from "./ProjectWorkItemDetail";
import {
  ProjectEmptyBlock,
  ProjectWorkspaceView,
  summarizeAssignments,
  type LoadState,
  type ProjectWorkspaceTab,
  type WorkItemSummary,
} from "./ProjectWorkspaceView";
import { ProfilesModal } from "./ProfilesModal";
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
  ProjectOperationsBriefAction,
  ProjectOperationsBriefItem,
  ProjectContextSourceRecord,
  ProjectSkillRecord,
  ProjectRecord,
  ProjectWorkItemReadinessRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
  UpdateProjectPayload,
  UpdateProjectSkillPayload,
} from "../../types/project";
import type { AgentProfileRecord } from "../../types/agent-profile";
import { ConfirmModal, Icon, Icons, InlineError, type ProviderOption } from "../shared/ui";
import { ProjectSettingsPanel } from "./ProjectSettingsPanel";
import { ProjectEvidenceLinkModal } from "./ProjectEvidenceLinkModal";
import {
  createProjectPayloadFromForm,
  type CreateProjectForm,
  type CreateWorktreeForm,
  type ProjectDefaultsForm,
} from "./projectSettings";
import {
  profileCreatePayloadFromForm,
  profileUpdatePayloadFromForm,
  projectSkillStatusRank,
  rolePayloadFromForm,
  type AgentProfileForm,
  type RoleForm,
} from "./projectProfilesRoles";
import {
  projectContextSourcesWithSavedSource,
  projectContextSourcesWithoutSource,
  type ProjectSourceForm,
} from "./projectSources";
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
  formatProjectRowRelativeTime,
  projectErrorMessage as errorMessage,
} from "./projectDisplay";
import {
  useProjectSelectionController,
  useStoredRightPanelWidth,
} from "./useProjectViewController";
import { PROJECT_ASSISTANT_AUTO } from "./ProjectAssistantPanel";

type Props = {
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

export function ProjectsView({ onOpenChat, onOpenConnections, onOpenTask }: Props) {
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
  const [projectHealth, setProjectHealth] = useState<ProjectHealth | null>(null);
  const [operationsBrief, setOperationsBrief] = useState<ProjectOperationsBrief | null>(null);
  const [operationsBriefError, setOperationsBriefError] = useState("");
  const [operationsBriefLoadState, setOperationsBriefLoadState] = useState<LoadState>("idle");
  const [activityBucket, setActivityBucket] = useState<ProjectActivityBucketKey>("all");
  const [workspaceTab, setWorkspaceTab] = useState<ProjectWorkspaceTab>("work");
  const [roles, setRoles] = useState<ProjectWorkRoleRecord[]>([]);
  const [selectedWorkItemID, setSelectedWorkItemID] = useState("");
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
  const [evidenceLinkPending, setEvidenceLinkPending] = useState(false);
  const [evidenceLinkError, setEvidenceLinkError] = useState("");
  const [reviewArtifactDraft, setReviewArtifactDraft] = useState<ReviewArtifactForm | null>(null);
  const [reviewArtifactPending, setReviewArtifactPending] = useState(false);
  const [reviewArtifactError, setReviewArtifactError] = useState("");
  const [workLoadState, setWorkLoadState] = useState<LoadState>("idle");
  const [detailLoadState, setDetailLoadState] = useState<LoadState>("idle");
  const [workError, setWorkError] = useState("");
  const [detailError, setDetailError] = useState("");
  const [assignmentErrors, setAssignmentErrors] = useState<Record<string, string>>({});
  const [startingAssignmentID, setStartingAssignmentID] = useState("");
  const [preparingAssignmentID, setPreparingAssignmentID] = useState("");
  const startingAssignmentIDsRef = useRef<Set<string>>(new Set());
  const [memoryEntries, setMemoryEntries] = useState<ProjectMemoryRecord[]>([]);
  const [memoryCandidates, setMemoryCandidates] = useState<ProjectMemoryCandidateRecord[]>([]);
  const [projectSkills, setProjectSkills] = useState<ProjectSkillRecord[]>([]);
  const [skillsLoadState, setSkillsLoadState] = useState<LoadState>("idle");
  const [skillsError, setSkillsError] = useState("");
  const [discoveringSkills, setDiscoveringSkills] = useState(false);
  const [updatingSkillID, setUpdatingSkillID] = useState("");
  const [agentProfiles, setAgentProfiles] = useState<AgentProfileRecord[]>([]);
  const [agentProfilesError, setAgentProfilesError] = useState("");
  const [profilesModalOpen, setProfilesModalOpen] = useState(false);
  const [profilesPending, setProfilesPending] = useState(false);
  const [profilesError, setProfilesError] = useState("");
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
  const { clearSelectedProject, openProject, selectedProject, selectedProjectID } =
    useProjectSelectionController({
      activeProjectID: projects.activeProjectID,
      onProjectChange: () => setSelectedWorkItemID(""),
      projects: projects.state.projects,
      selectProject: projects.actions.selectProject,
    });

  const loadProjectHealth = useCallback(async (projectID: string) => {
    if (!projectID) {
      setProjectHealth(null);
      return;
    }
    const payload = await getProjectHealth(projectID);
    setProjectHealth(payload.data ?? null);
  }, []);

  const refreshProjectHealth = useCallback(
    (projectID: string) => {
      if (!projectID) {
        setProjectHealth(null);
        return;
      }
      void loadProjectHealth(projectID).catch(() => {
        setProjectHealth(null);
      });
    },
    [loadProjectHealth],
  );

  const loadAgentProfiles = useCallback(async (cancelled?: () => boolean) => {
    try {
      const payload = await getAgentProfiles();
      if (cancelled?.()) return;
      setAgentProfiles(payload.data ?? []);
      setAgentProfilesError("");
    } catch (error) {
      if (cancelled?.()) return;
      setAgentProfilesError(errorMessage(error, "Failed to load agent profiles."));
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void loadAgentProfiles(() => cancelled);
    return () => {
      cancelled = true;
    };
  }, [loadAgentProfiles]);
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

  const loadWorkForProject = useCallback(async (projectID: string, preferredWorkItemID = "") => {
    setWorkError("");
    setDetailError("");
    setAssignmentErrors({});
    setWorkItems([]);
    setWorkItemSummaries({});
    setActivity(null);
    setProjectHealth(null);
    setOperationsBrief(null);
    setOperationsBriefError("");
    if (!preferredWorkItemID) {
      setSelectedWorkItemID("");
      setSelectedWorkItem(null);
      setSelectedWorkItemReadiness(null);
      setAssignments([]);
      setArtifacts([]);
      setHandoffs([]);
    }
    if (!projectID) {
      setWorkLoadState("idle");
      setOperationsBriefLoadState("idle");
      return "";
    }
    setWorkLoadState("loading");
    setOperationsBriefLoadState("loading");
    try {
      const activityLoad = getProjectActivity(projectID).catch(() => null);
      const operationsBriefLoad = getProjectOperationsBrief(projectID).catch((error) => {
        setOperationsBriefError(errorMessage(error, "Failed to load project operations."));
        return null;
      });
      const healthLoad = getProjectHealth(projectID).catch(() => null);
      const [rolesRes, workRes, activityRes, healthRes, operationsBriefRes] = await Promise.all([
        getProjectWorkRoles(projectID),
        getProjectWorkItems(projectID),
        activityLoad,
        healthLoad,
        operationsBriefLoad,
      ]);
      const nextRoles = rolesRes.data ?? [];
      const nextItems = workRes.data ?? [];
      setRoles(nextRoles);
      setWorkItems(nextItems);
      setActivity(activityRes?.data ?? null);
      setProjectHealth(healthRes?.data ?? null);
      setOperationsBrief(operationsBriefRes?.data ?? null);
      setOperationsBriefLoadState(operationsBriefRes ? "loaded" : "error");
      setWorkItemSummaries(
        Object.fromEntries(
          nextItems.map((item) => [item.id, summarizeAssignments(item.assignments ?? [])] as const),
        ),
      );
      const nextSelectedID = nextItems.some((item) => item.id === preferredWorkItemID)
        ? preferredWorkItemID
        : nextItems[0]?.id || "";
      setSelectedWorkItemID(nextSelectedID);
      setWorkLoadState("loaded");
      return nextSelectedID;
    } catch (error) {
      setWorkLoadState("error");
      setOperationsBriefLoadState("error");
      setProjectHealth(null);
      setWorkError(errorMessage(error, "Failed to load project work."));
      return "";
    }
  }, []);

  const loadProjectMemory = useCallback(
    async (projectID: string) => {
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
        setMemoryEntries(memoryPayload.data ?? []);
        setMemoryCandidates(candidatePayload.data ?? []);
        setMemoryLoadState("loaded");
        refreshProjectHealth(projectID);
      } catch (error) {
        setMemoryLoadState("error");
        setMemoryError(errorMessage(error, "Failed to load project memory."));
      }
    },
    [refreshProjectHealth],
  );

  const loadProjectSkills = useCallback(
    async (projectID: string) => {
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
        setProjectSkills(payload.data ?? []);
        setSkillsLoadState("loaded");
        refreshProjectHealth(projectID);
      } catch (error) {
        setSkillsLoadState("error");
        setSkillsError(errorMessage(error, "Failed to load project skills."));
      }
    },
    [refreshProjectHealth],
  );

  const loadWorkItemDetail = useCallback(async (projectID: string, workItemID: string) => {
    setDetailError("");
    setAssignmentErrors({});
    if (!projectID || !workItemID) {
      setSelectedWorkItem(null);
      setSelectedWorkItemReadiness(null);
      setAssignments([]);
      setArtifacts([]);
      setHandoffs([]);
      setDetailLoadState("idle");
      return;
    }
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
    } catch (error) {
      setSelectedWorkItemReadiness(null);
      setDetailLoadState("error");
      setDetailError(errorMessage(error, "Failed to load work item detail."));
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
    loadWorkItemDetail,
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
    assistant.dismiss();
  }, [assistant.dismiss, selectedProjectID, selectedWorkItemID]);

  useEffect(() => {
    setPreparingAssignmentID("");
  }, [selectedProjectID, selectedWorkItemID]);

  useEffect(() => {
    if (!selectedProjectID) return;
    if (workLoadState !== "loaded" && workLoadState !== "error") return;
    const handoff = readProjectAssistantChatHandoff();
    if (!handoff || handoff.project_id !== selectedProjectID) return;
    assistant.loadProposal(handoff.proposal, {
      chatDraftSource: {
        request: handoff.request,
        sourceSessionID: handoff.source_session_id,
        createdAt: handoff.created_at,
      },
    });
    setWorkspaceTab("work");
    clearProjectAssistantChatHandoff();
  }, [assistant.loadProposal, selectedProjectID, workLoadState]);

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
        if (selectedProjectID === pendingDeleteProject.id) {
          clearSelectedProject();
        }
      }
    } finally {
      setDeletePending(false);
    }
  }

  async function handleSaveProjectDefaults(form: ProjectDefaultsForm) {
    if (!selectedProject) return;
    setDefaultsPending(true);
    setDefaultsError("");
    const patch: UpdateProjectPayload = {
      default_provider: form.provider.trim(),
      default_model: form.model.trim(),
      default_agent_profile: form.defaultAgentProfile.trim(),
      default_workspace_mode: form.workspaceMode.trim(),
      default_root_id: form.defaultRootID.trim(),
      roots: form.roots,
    };
    try {
      const payload = await updateProject(selectedProject.id, patch);
      projects.actions.setProjects((current) => upsertProject(current, payload.data));
      refreshProjectHealth(selectedProject.id);
      setSettingsPanelOpen(false);
    } catch (error) {
      setDefaultsError(errorMessage(error, "Failed to update project defaults."));
    } finally {
      setDefaultsPending(false);
    }
  }

  async function handleDiscoverProjectRoots() {
    if (!selectedProject) return;
    setDiscoveringRoots(true);
    setDefaultsError("");
    try {
      const payload = await discoverProjectRoots(selectedProject.id);
      projects.actions.setProjects((current) => upsertProject(current, payload.data));
      refreshProjectHealth(selectedProject.id);
    } catch (error) {
      setDefaultsError(errorMessage(error, "Failed to discover project roots."));
    } finally {
      setDiscoveringRoots(false);
    }
  }

  async function handleCreateWorktreeRoot(form: CreateWorktreeForm) {
    if (!selectedProject) return;
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
      const result = await createProjectWorktreeRoot(selectedProject.id, payload);
      projects.actions.setProjects((current) => upsertProject(current, result.data));
      refreshProjectHealth(selectedProject.id);
      setCreateWorktreeOpen(false);
    } catch (error) {
      setCreateWorktreeError(errorMessage(error, "Failed to create project worktree."));
    } finally {
      setCreateWorktreePending(false);
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
    setDiscoveringContext(true);
    setMemoryError("");
    try {
      const payload = await discoverProjectContextSources(selectedProjectID);
      projects.actions.setProjects((current) => upsertProject(current, payload.data));
      refreshProjectHealth(selectedProjectID);
    } catch (error) {
      setMemoryError(errorMessage(error, "Failed to discover workspace guidance."));
    } finally {
      setDiscoveringContext(false);
    }
  }

  async function handleDiscoverProjectSkills() {
    if (!selectedProjectID) return;
    setDiscoveringSkills(true);
    setSkillsError("");
    try {
      const payload = await discoverProjectSkills(selectedProjectID);
      setProjectSkills(payload.data ?? []);
      setSkillsLoadState("loaded");
      refreshProjectHealth(selectedProjectID);
    } catch (error) {
      setSkillsError(errorMessage(error, "Failed to discover project skills."));
    } finally {
      setDiscoveringSkills(false);
    }
  }

  async function handleUpdateProjectSkill(
    skill: ProjectSkillRecord,
    patch: UpdateProjectSkillPayload,
  ) {
    if (!selectedProjectID) return;
    setUpdatingSkillID(skill.id);
    setSkillsError("");
    try {
      const payload = await updateProjectSkill(selectedProjectID, skill.id, patch);
      setProjectSkills((current) => upsertProjectSkill(current, payload.data));
      refreshProjectHealth(selectedProjectID);
    } catch (error) {
      setSkillsError(errorMessage(error, "Failed to update project skill."));
    } finally {
      setUpdatingSkillID("");
    }
  }

  async function handleCreateAgentProfile(form: AgentProfileForm) {
    const name = form.name.trim();
    if (!name) return undefined;
    setProfilesPending(true);
    setProfilesError("");
    try {
      const payload = await createAgentProfile(profileCreatePayloadFromForm(form));
      setAgentProfiles((current) => upsertAgentProfile(current, payload.data));
      refreshProjectHealth(selectedProjectID);
      return payload.data;
    } catch (error) {
      setProfilesError(errorMessage(error, "Failed to create agent profile."));
      return undefined;
    } finally {
      setProfilesPending(false);
    }
  }

  async function handleUpdateAgentProfile(profileID: string, form: AgentProfileForm) {
    const name = form.name.trim();
    if (!name) return undefined;
    setProfilesPending(true);
    setProfilesError("");
    try {
      const payload = await updateAgentProfile(profileID, profileUpdatePayloadFromForm(form));
      setAgentProfiles((current) => upsertAgentProfile(current, payload.data));
      refreshProjectHealth(selectedProjectID);
      return payload.data;
    } catch (error) {
      setProfilesError(errorMessage(error, "Failed to update agent profile."));
      return undefined;
    } finally {
      setProfilesPending(false);
    }
  }

  async function handleDeleteAgentProfile(profile: AgentProfileRecord) {
    setProfilesPending(true);
    setProfilesError("");
    try {
      await deleteAgentProfile(profile.id);
      setAgentProfiles((current) => current.filter((item) => item.id !== profile.id));
      refreshProjectHealth(selectedProjectID);
      return true;
    } catch (error) {
      setProfilesError(errorMessage(error, "Failed to delete agent profile."));
      return false;
    } finally {
      setProfilesPending(false);
    }
  }

  async function handleSaveMemory(form: MemoryForm) {
    if (!selectedProjectID || !editingMemory) return;
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
        editingMemory === "new"
          ? await createProjectMemory(selectedProjectID, payload)
          : await updateProjectMemory(selectedProjectID, editingMemory.id, payload);
      setMemoryEntries((current) => upsertMemory(current, res.data));
      refreshProjectHealth(selectedProjectID);
      setEditingMemory(null);
    } catch (error) {
      setMemoryError(errorMessage(error, "Failed to save project memory."));
    } finally {
      setMemoryPending(false);
    }
  }

  async function handleSaveSource(form: ProjectSourceForm) {
    if (!selectedProject || !editingSource) return;
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
      const contextSources = projectContextSourcesWithSavedSource(
        selectedProject.context_sources ?? [],
        editingSource,
        form,
      );
      const payload = await updateProject(selectedProject.id, { context_sources: contextSources });
      projects.actions.setProjects((current) => upsertProject(current, payload.data));
      refreshProjectHealth(selectedProject.id);
      setEditingSource(null);
    } catch (error) {
      setSourceError(errorMessage(error, "Failed to save project source."));
    } finally {
      setSourcePending(false);
    }
  }

  async function confirmDeleteSource() {
    if (!selectedProject || !deleteSource) return;
    setDeleteSourcePending(true);
    setSourceError("");
    try {
      const contextSources = projectContextSourcesWithoutSource(
        selectedProject.context_sources ?? [],
        deleteSource.id,
      );
      const payload = await updateProject(selectedProject.id, { context_sources: contextSources });
      projects.actions.setProjects((current) => upsertProject(current, payload.data));
      refreshProjectHealth(selectedProject.id);
      setDeleteSource(null);
    } catch (error) {
      setSourceError(errorMessage(error, "Failed to delete project source."));
    } finally {
      setDeleteSourcePending(false);
    }
  }

  async function confirmDeleteMemory() {
    if (!selectedProjectID || !deleteMemory) return;
    setDeleteMemoryPending(true);
    setMemoryError("");
    try {
      await deleteProjectMemory(selectedProjectID, deleteMemory.id);
      setMemoryEntries((current) => current.filter((item) => item.id !== deleteMemory.id));
      refreshProjectHealth(selectedProjectID);
      setDeleteMemory(null);
    } catch (error) {
      setMemoryError(errorMessage(error, "Failed to delete project memory."));
    } finally {
      setDeleteMemoryPending(false);
    }
  }

  async function reloadRoles(projectID = selectedProjectID) {
    if (!projectID) return;
    const payload = await getProjectWorkRoles(projectID);
    setRoles(payload.data ?? []);
  }

  async function handleCreateRole(form: RoleForm) {
    if (!selectedProjectID) return undefined;
    const name = form.name.trim();
    if (!name) return undefined;
    setRolesPending(true);
    setRolesError("");
    try {
      const payload = await createProjectWorkRole(selectedProjectID, rolePayloadFromForm(form));
      setRoles((current) => upsertRole(current, payload.data));
      refreshProjectHealth(selectedProjectID);
      return payload.data;
    } catch (error) {
      setRolesError(errorMessage(error, "Failed to create role."));
      return undefined;
    } finally {
      setRolesPending(false);
    }
  }

  async function handleUpdateRole(roleID: string, form: RoleForm) {
    if (!selectedProjectID) return undefined;
    const name = form.name.trim();
    if (!name) return undefined;
    setRolesPending(true);
    setRolesError("");
    try {
      const payload = await updateProjectWorkRole(
        selectedProjectID,
        roleID,
        rolePayloadFromForm(form),
      );
      setRoles((current) => upsertRole(current, payload.data));
      refreshProjectHealth(selectedProjectID);
      return payload.data;
    } catch (error) {
      setRolesError(errorMessage(error, "Failed to update role."));
      return undefined;
    } finally {
      setRolesPending(false);
    }
  }

  async function handleDeleteRole(role: ProjectWorkRoleRecord) {
    if (!selectedProjectID || role.built_in) return false;
    setRolesPending(true);
    setRolesError("");
    try {
      await deleteProjectWorkRole(selectedProjectID, role.id);
      await reloadRoles(selectedProjectID);
      refreshProjectHealth(selectedProjectID);
      return true;
    } catch (error) {
      setRolesError(errorMessage(error, "Failed to delete role."));
      return false;
    } finally {
      setRolesPending(false);
    }
  }

  async function handlePromoteCandidate(form: MemoryForm) {
    if (!selectedProjectID || !promotingCandidate) return;
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
      const candidateRes = await promoteProjectMemoryCandidate(
        selectedProjectID,
        promotingCandidate.id,
        payload,
      );
      setMemoryCandidates((current) => current.filter((item) => item.id !== candidateRes.data.id));
      await loadProjectMemory(selectedProjectID);
      setPromotingCandidate(null);
    } catch (error) {
      setMemoryError(errorMessage(error, "Failed to promote memory candidate."));
    } finally {
      setMemoryPending(false);
    }
  }

  async function handleRejectCandidate(candidate: ProjectMemoryCandidateRecord) {
    if (!selectedProjectID || rejectingCandidateID) return;
    setRejectingCandidateID(candidate.id);
    setMemoryError("");
    try {
      const res = await rejectProjectMemoryCandidate(selectedProjectID, candidate.id, {});
      setMemoryCandidates((current) => current.filter((item) => item.id !== res.data.id));
      refreshProjectHealth(selectedProjectID);
    } catch (error) {
      setMemoryError(errorMessage(error, "Failed to reject memory candidate."));
    } finally {
      setRejectingCandidateID("");
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
    setNewWorkPending(true);
    setNewWorkError("");
    try {
      const payload = await createProjectWorkItem(
        selectedProjectID,
        workItemCreatePayloadFromForm(form),
      );
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
      setSelectedWorkItemID(payload.data.id);
      setNewWorkModalOpen(false);
      setNewWorkDraft(undefined);
      await loadWorkItemDetail(selectedProjectID, payload.data.id);
    } catch (error) {
      setNewWorkError(errorMessage(error, "Failed to create work item."));
    } finally {
      setNewWorkPending(false);
    }
  }

  async function handleUpdateWorkItem(form: EditWorkItemForm) {
    if (!selectedProjectID) return;
    const title = form.title.trim();
    if (!title) return;
    setEditWorkPending(true);
    setEditWorkError("");
    const patch = workItemUpdatePayloadFromForm(form);
    try {
      const payload = await updateProjectWorkItem(selectedProjectID, form.id, patch);
      setWorkItems((current) => upsertWorkItem(current, payload.data));
      setSelectedWorkItem(payload.data);
      setEditingWorkItem(null);
      await loadWorkItemDetail(selectedProjectID, payload.data.id);
    } catch (error) {
      setEditWorkError(errorMessage(error, "Failed to update work item."));
    } finally {
      setEditWorkPending(false);
    }
  }

  async function handleCloseWorkItem(item: ProjectWorkItemRecord) {
    if (!selectedProjectID || closingWorkItemID) return;
    setClosingWorkItemID(item.id);
    setDetailError("");
    try {
      const payload = await updateProjectWorkItem(selectedProjectID, item.id, { status: "done" });
      setWorkItems((current) => upsertWorkItem(current, payload.data));
      if (selectedWorkItemID === item.id) {
        setSelectedWorkItem(payload.data);
      }
      await loadWorkForProject(selectedProjectID, item.id);
      await loadWorkItemDetail(selectedProjectID, item.id);
    } catch (error) {
      setDetailError(errorMessage(error, "Failed to mark work item done."));
    } finally {
      setClosingWorkItemID("");
    }
  }

  async function confirmDeleteWorkItem() {
    if (!selectedProjectID || !deleteWorkItem) return;
    setDeleteWorkPending(true);
    try {
      await deleteProjectWorkItem(selectedProjectID, deleteWorkItem.id);
      setDeleteWorkItem(null);
      await loadWorkForProject(selectedProjectID);
    } finally {
      setDeleteWorkPending(false);
    }
  }

  async function handleCreateAssignment(form: NewAssignmentForm) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const roleID = form.roleID.trim();
    if (!roleID) return;
    setNewAssignmentPending(true);
    setNewAssignmentError("");
    try {
      const payload = await createProjectAssignment(
        selectedProjectID,
        selectedWorkItemID,
        assignmentCreatePayloadFromForm(form),
      );
      setAssignments((current) => upsertAssignment(current, payload.data));
      setNewAssignmentModalOpen(false);
      await loadWorkItemDetail(selectedProjectID, selectedWorkItemID);
    } catch (error) {
      setNewAssignmentError(errorMessage(error, "Failed to create assignment."));
    } finally {
      setNewAssignmentPending(false);
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
    const action = item.action;
    if (!action?.type) {
      setWorkError("Project operation is missing an action. Refresh project work and try again.");
      return;
    }
    if (action.project_id && selectedProjectID && action.project_id !== selectedProjectID) {
      setWorkError("Project operation target changed. Refresh project work and try again.");
      return;
    }
    if (action.type === "draft_project_proposal") {
      const request = action.request?.trim();
      if (!request) {
        setWorkError("Project operation is missing a Project Assistant draft request.");
        return;
      }
      setWorkspaceTab("work");
      if (action.work_item_id) {
        setSelectedWorkItemID(action.work_item_id);
      }
      void assistant.propose(
        {
          request,
          roleID: PROJECT_ASSISTANT_AUTO,
          driverKind: PROJECT_ASSISTANT_AUTO,
          draftMode: "deterministic",
        },
        action.work_item_id,
      );
      return;
    }

    switch (action.type) {
      case "open_project_settings":
        setDefaultsError("");
        setSettingsPanelOpen(true);
        return;
      case "open_memory_review":
        setWorkspaceTab("memory");
        return;
      case "open_assignment_preflight":
        selectOperationWorkTarget(action);
        if (action.assignment_id) {
          setPreparingAssignmentID(action.assignment_id);
        } else {
          setWorkError("Project operation is missing an assignment preflight target.");
        }
        return;
      case "open_work_item":
        selectOperationWorkTarget(action);
        return;
      default:
        setWorkError(
          "Project operation action is not supported. Refresh project work and try again.",
        );
    }
  }

  function selectOperationWorkTarget(action: ProjectOperationsBriefAction) {
    setWorkspaceTab("work");
    const bucket = projectOperationsActivityBucket(action.activity_bucket);
    if (bucket) {
      setActivityBucket(bucket);
    }
    if (action.work_item_id) {
      setSelectedWorkItemID(action.work_item_id);
    }
  }

  async function handleUpdateAssignment(form: EditAssignmentForm) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const roleID = form.roleID.trim();
    if (!roleID) return;
    setEditAssignmentPending(true);
    setEditAssignmentError("");
    const patch = assignmentUpdatePayloadFromForm(form);
    try {
      const payload = await updateProjectAssignment(
        selectedProjectID,
        selectedWorkItemID,
        form.id,
        patch,
      );
      setAssignments((current) => upsertAssignment(current, payload.data));
      setEditingAssignment(null);
      await loadWorkItemDetail(selectedProjectID, selectedWorkItemID);
    } catch (error) {
      setEditAssignmentError(errorMessage(error, "Failed to update assignment."));
    } finally {
      setEditAssignmentPending(false);
    }
  }

  async function confirmDeleteAssignment() {
    if (!selectedProjectID || !selectedWorkItemID || !deleteAssignment) return;
    setDeleteAssignmentPending(true);
    try {
      await deleteProjectAssignment(selectedProjectID, selectedWorkItemID, deleteAssignment.id);
      setDeleteAssignment(null);
      await loadWorkItemDetail(selectedProjectID, selectedWorkItemID);
    } finally {
      setDeleteAssignmentPending(false);
    }
  }

  async function handleSaveHandoff(form: HandoffForm) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const title = form.title.trim();
    const summary = form.summary.trim();
    const recommendedNextAction = form.recommendedNextAction.trim();
    if (!title || !summary || !recommendedNextAction) return;
    const payload = handoffPayloadFromForm(form);
    setHandoffPending(true);
    setHandoffError("");
    try {
      const res =
        editingHandoff === "new"
          ? await createProjectHandoff(selectedProjectID, selectedWorkItemID, payload)
          : await updateProjectHandoff(selectedProjectID, selectedWorkItemID, form.id, payload);
      setHandoffs((current) => upsertHandoff(current, res.data));
      setEditingHandoff(null);
      setNewHandoffDraft(null);
      await loadWorkForProject(selectedProjectID, selectedWorkItemID);
    } catch (error) {
      setHandoffError(errorMessage(error, "Failed to save handoff."));
    } finally {
      setHandoffPending(false);
    }
  }

  async function handleSaveReviewArtifact(form: ReviewArtifactForm) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const payload = reviewArtifactPayloadFromForm(form);
    if (!payload.title?.trim() || !payload.body.trim()) return;
    setReviewArtifactPending(true);
    setReviewArtifactError("");
    try {
      const res = await createProjectCollaborationArtifact(
        selectedProjectID,
        selectedWorkItemID,
        payload,
      );
      setArtifacts((current) => upsertArtifact(current, res.data));
      setReviewArtifactDraft(null);
      await loadWorkItemDetail(selectedProjectID, selectedWorkItemID);
      await loadWorkForProject(selectedProjectID, selectedWorkItemID);
    } catch (error) {
      setReviewArtifactError(errorMessage(error, "Failed to save review artifact."));
    } finally {
      setReviewArtifactPending(false);
    }
  }

  async function handleSaveEvidenceLink(form: EvidenceLinkForm) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const payload = evidenceLinkPayloadFromForm(form);
    if (!payload.title?.trim() || !payload.body.trim()) return;
    if (!payload.evidence_url?.trim() && !payload.evidence_external_id?.trim()) return;
    setEvidenceLinkPending(true);
    setEvidenceLinkError("");
    try {
      const res = await createProjectCollaborationArtifact(
        selectedProjectID,
        selectedWorkItemID,
        payload,
      );
      setArtifacts((current) => upsertArtifact(current, res.data));
      setEvidenceLinkModalOpen(false);
      await loadWorkItemDetail(selectedProjectID, selectedWorkItemID);
      await loadWorkForProject(selectedProjectID, selectedWorkItemID);
    } catch (error) {
      setEvidenceLinkError(errorMessage(error, "Failed to record evidence."));
    } finally {
      setEvidenceLinkPending(false);
    }
  }

  async function handleSetHandoffStatus(handoff: ProjectHandoffRecord, status: string) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    setHandoffActionID(handoff.id);
    setHandoffError("");
    try {
      const res = await updateProjectHandoffStatus(
        selectedProjectID,
        selectedWorkItemID,
        handoff.id,
        status,
      );
      setHandoffs((current) => upsertHandoff(current, res.data));
      await loadWorkForProject(selectedProjectID, selectedWorkItemID);
    } catch (error) {
      setHandoffError(errorMessage(error, "Failed to update handoff status."));
    } finally {
      setHandoffActionID("");
    }
  }

  async function handleDeleteHandoff(handoff: ProjectHandoffRecord) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    setHandoffActionID(handoff.id);
    setHandoffError("");
    try {
      await deleteProjectHandoff(selectedProjectID, selectedWorkItemID, handoff.id);
      setHandoffs((current) => current.filter((item) => item.id !== handoff.id));
      await loadWorkForProject(selectedProjectID, selectedWorkItemID);
    } catch (error) {
      setHandoffError(errorMessage(error, "Failed to delete handoff."));
    } finally {
      setHandoffActionID("");
    }
  }

  async function handleCreateAssignmentFromHandoff(
    handoff: ProjectHandoffRecord,
    options: { failureMessage?: string; prefixFailureMessage?: boolean } = {},
  ) {
    if (!selectedProjectID || !selectedWorkItemID) return;
    const roleID = (handoff.target_role_id || "software_developer").trim();
    if (!roleID) return;
    const targetWorkItemID = (handoff.target_work_item_id || selectedWorkItemID).trim();
    if (!targetWorkItemID) return;
    const targetRole = roleByID.get(roleID);
    const driverKind = targetRole?.default_driver_kind || "hecate_task";
    setHandoffActionID(handoff.id);
    setHandoffError("");
    try {
      const assignment = await createProjectAssignment(selectedProjectID, targetWorkItemID, {
        role_id: roleID,
        driver_kind: driverKind,
      });
      if (targetWorkItemID === selectedWorkItemID) {
        setAssignments((current) => upsertAssignment(current, assignment.data));
      }
      const updated = await updateProjectHandoff(
        selectedProjectID,
        selectedWorkItemID,
        handoff.id,
        {
          target_assignment_id: assignment.data.id,
          target_role_id: assignment.data.role_id,
          status: "accepted",
        },
      );
      setHandoffs((current) => upsertHandoff(current, updated.data));
      await loadWorkItemDetail(selectedProjectID, selectedWorkItemID);
      await loadWorkForProject(selectedProjectID, selectedWorkItemID);
    } catch (error) {
      const failureMessage = options.failureMessage || "Failed to create target assignment.";
      const detail = errorMessage(error, failureMessage);
      setHandoffError(
        options.prefixFailureMessage && detail !== failureMessage
          ? `${failureMessage} ${detail}`
          : detail,
      );
    } finally {
      setHandoffActionID("");
    }
  }

  async function handleCreateAssignmentFromReviewArtifact(
    artifact: ProjectCollaborationArtifactRecord,
  ) {
    if (!selectedProjectID || !selectedWorkItemID || !selectedWorkItem) return;
    const payload = handoffPayloadFromForm(
      handoffFormFromReviewArtifact(artifact, selectedWorkItem),
    );
    setArtifactActionID(artifact.id);
    setHandoffError("");
    try {
      const handoff = await createProjectHandoff(selectedProjectID, selectedWorkItemID, payload);
      setHandoffs((current) => upsertHandoff(current, handoff.data));
      await handleCreateAssignmentFromHandoff(handoff.data, {
        failureMessage:
          "Created the follow-up handoff, but failed to create its assignment. Finish it from the handoff card to avoid duplicating the handoff.",
        prefixFailureMessage: true,
      });
    } catch (error) {
      setHandoffError(errorMessage(error, "Failed to create follow-up handoff."));
    } finally {
      setArtifactActionID("");
    }
  }

  async function handleStartHandoff(handoff: ProjectHandoffRecord) {
    const assignment = assignments.find((item) => item.id === handoff.target_assignment_id);
    if (!assignment) {
      setHandoffError("Handoff has no loaded target assignment to start.");
      return;
    }
    await handleStartAssignment(assignment, selectedWorkItemID);
    if (handoff.status === "pending") {
      await handleSetHandoffStatus(handoff, "accepted");
    }
  }

  async function refreshSelectedWorkItem() {
    if (!selectedProjectID) return;
    const refreshedWorkItemID = await loadWorkForProject(selectedProjectID, selectedWorkItemID);
    if (refreshedWorkItemID) {
      await loadWorkItemDetail(selectedProjectID, refreshedWorkItemID);
    }
  }

  async function handleStartAssignment(
    assignment: ProjectAssignmentRecord,
    workItemID = selectedWorkItemID,
  ) {
    if (!selectedProjectID || !workItemID) return;
    if (startingAssignmentIDsRef.current.has(assignment.id)) return;
    startingAssignmentIDsRef.current.add(assignment.id);
    setStartingAssignmentID(assignment.id);
    setAssignmentErrors((current) => ({ ...current, [assignment.id]: "" }));
    try {
      const res = await startProjectAssignment(
        selectedProjectID,
        workItemID,
        assignment.id,
        assignment.driver_kind || "hecate_task",
      );
      setAssignments((current) => upsertAssignment(current, res.data));
      await loadWorkForProject(selectedProjectID, workItemID);
      await loadWorkItemDetail(selectedProjectID, workItemID);
    } catch (error) {
      setAssignmentErrors((current) => ({
        ...current,
        [assignment.id]: errorMessage(error, "Failed to start assignment."),
      }));
      if (error instanceof ApiError && error.status === 409) {
        await loadWorkForProject(selectedProjectID, workItemID);
        await loadWorkItemDetail(selectedProjectID, workItemID);
      }
    } finally {
      startingAssignmentIDsRef.current.delete(assignment.id);
      setStartingAssignmentID("");
    }
  }

  const hasWorkItemDetail =
    Boolean(selectedWorkItemID) || detailLoadState === "loading" || Boolean(detailError);
  const selectedProjectEnabledSourceCount =
    selectedProject?.context_sources?.filter((source) => source.enabled).length ?? 0;
  const projectHasSetupState =
    selectedProjectEnabledSourceCount > 0 ||
    roles.length > 0 ||
    projectSkills.length > 0 ||
    memoryEntries.length > 0 ||
    memoryCandidates.length > 0;
  const projectNeedsOnboarding =
    Boolean(selectedProject) &&
    workLoadState === "loaded" &&
    workItems.length === 0 &&
    !projectHasSetupState &&
    !assistant.proposal &&
    !assistant.applyResult;
  const projectEmptyTitle =
    projects.state.projects.length === 0 ? "Add a project to begin" : "Select a project";
  const projectEmptyDetail =
    projects.state.projects.length === 0
      ? "Create a project from a name and purpose. A local folder is optional and can be attached now or later."
      : "Choose a project from the list to view its work, memory, skills, and settings.";

  return (
    <div style={shellStyle}>
      <section style={sidePanelStyle} aria-label="Projects">
        <div style={topbarStyle}>
          <div>
            <div style={sidebarSectionLabelStyle}>Projects</div>
            <div style={subtleTextStyle}>{projects.state.projects.length} records</div>
          </div>
          <div style={topbarActionsStyle}>
            <button
              className="btn btn-primary btn-sm"
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
        <div style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
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

      <div style={projectMainStyle}>
        <ProjectHeader
          attentionItems={projectHealth?.attention ?? []}
          omittedAttentionCount={projectHealth?.summary?.omitted_attention_count ?? 0}
          memoryCandidates={memoryCandidates}
          project={selectedProject}
          onAttentionBucket={(bucket) => {
            setActivityBucket(bucket);
          }}
          onAttentionDefaults={() => {
            setDefaultsError("");
            setSettingsPanelOpen(true);
          }}
          onAttentionMemory={() => setWorkspaceTab("memory")}
          onAttentionProfiles={() => {
            setProfilesError("");
            setProfilesModalOpen(true);
          }}
          onAttentionReviewCandidate={setPromotingCandidate}
          onAttentionRoles={() => {
            setRolesError("");
            setRolesModalOpen(true);
          }}
          onAttentionSkills={() => setWorkspaceTab("skills")}
          onAttentionTask={onOpenTask}
          onAttentionWorkItem={(workItemID) => {
            setWorkspaceTab("work");
            setSelectedWorkItemID(workItemID);
          }}
          onRefresh={refreshSelectedWorkItem}
          settingsOpen={settingsPanelOpen}
          onEditDefaults={() => {
            setDefaultsError("");
            setSettingsPanelOpen((open) => !open);
          }}
          onManageProfiles={() => {
            setProfilesError("");
            setProfilesModalOpen(true);
          }}
          onManageRoles={() => {
            setRolesError("");
            setRolesModalOpen(true);
          }}
        />
        <div style={projectMainBodyStyle}>
          <ProjectWorkspaceView
            activity={activity}
            activityBucket={activityBucket}
            activityByAssignmentID={activityByAssignmentID}
            artifacts={artifacts}
            artifactActionID={artifactActionID}
            assignmentErrors={assignmentErrors}
            assignments={assignments}
            assistant={assistant}
            draftingDefaultAssignment={assistant.status === "proposing"}
            detailError={detailError}
            detailLoadState={detailLoadState}
            discoveringContext={discoveringContext}
            discoveringSkills={discoveringSkills}
            handoffActionID={handoffActionID}
            handoffError={handoffError}
            handoffs={handoffs}
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
            onAddEvidenceLink={() => {
              setEvidenceLinkError("");
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
              if (!selectedWorkItem) return;
              setHandoffError("");
              setNewHandoffDraft(
                reviewHandoffFormFromAssignment(
                  assignment,
                  roleByID.get(assignment.role_id) ?? null,
                  reviewRole,
                  selectedWorkItem,
                  activityItem,
                ),
              );
              setEditingHandoff("new");
            }}
            onAddReviewArtifactFromAssignment={(assignment) => {
              if (!selectedWorkItem) return;
              setReviewArtifactError("");
              setReviewArtifactDraft(
                reviewArtifactFormFromAssignment(
                  assignment,
                  roleByID.get(assignment.role_id) ?? null,
                  selectedWorkItem,
                  handoffs,
                ),
              );
            }}
            onAddHandoffFromReviewArtifact={(artifact) => {
              if (!selectedWorkItem) return;
              setHandoffError("");
              setNewHandoffDraft(handoffFormFromReviewArtifact(artifact, selectedWorkItem));
              setEditingHandoff("new");
            }}
            onDraftDefaultAssignment={handleDraftDefaultAssignment}
            onPreparedAssignmentPreflightOpened={(assignmentID) => {
              setPreparingAssignmentID((current) => (current === assignmentID ? "" : current));
            }}
            onCreateAssignmentFromReviewArtifact={(artifact) =>
              void handleCreateAssignmentFromReviewArtifact(artifact)
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
            onManageProfiles={() => {
              setProfilesError("");
              setProfilesModalOpen(true);
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
            onRefreshMemory={() => void loadProjectMemory(selectedProjectID)}
            onRefreshProjectSkills={() => void loadProjectSkills(selectedProjectID)}
            onRefreshWorkItem={refreshSelectedWorkItem}
            onRejectCandidate={handleRejectCandidate}
            onSelectWorkItem={setSelectedWorkItemID}
            onSetHandoffStatus={(handoff, status) => void handleSetHandoffStatus(handoff, status)}
            onStartAssignment={handleStartAssignment}
            onStartHandoff={(handoff) => void handleStartHandoff(handoff)}
            onUpdateProjectSkill={(skill, patch) => void handleUpdateProjectSkill(skill, patch)}
            onWorkspaceTabChange={setWorkspaceTab}
            project={selectedProject}
            projectEmptyDetail={projectEmptyDetail}
            projectEmptyTitle={projectEmptyTitle}
            projectNeedsOnboarding={projectNeedsOnboarding}
            operationsBrief={operationsBrief}
            operationsBriefError={operationsBriefError}
            operationsBriefLoadState={operationsBriefLoadState}
            projectSkills={projectSkills}
            preparingAssignmentID={preparingAssignmentID}
            rejectingCandidateID={rejectingCandidateID}
            roleByID={roleByID}
            roles={roles}
            selectedWorkItem={selectedWorkItem}
            selectedWorkItemReadiness={selectedWorkItemReadiness}
            selectedWorkItemID={selectedWorkItemID}
            skillsError={skillsError}
            skillsLoadState={skillsLoadState}
            startingAssignmentID={startingAssignmentID}
            updatingSkillID={updatingSkillID}
            workError={workError}
            workItemSummaries={workItemSummaries}
            workItems={workItems}
            workLoadState={workLoadState}
            workspaceTab={workspaceTab}
          />

          {selectedProject && settingsPanelOpen && (
            <ChatRightPanel
              ariaLabel="Project settings panel"
              width={rightPanelWidth}
              onWidthChange={setRightPanelWidth}
            >
              <ProjectSettingsPanel
                agentProfiles={agentProfiles}
                agentProfilesError={agentProfilesError}
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
            agentProfiles={agentProfiles}
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

        {selectedProject && profilesModalOpen && (
          <ProfilesModal
            error={profilesError}
            pending={profilesPending}
            profiles={agentProfiles}
            project={selectedProject}
            projectSkills={projectSkills}
            roles={roles}
            onClose={() => setProfilesModalOpen(false)}
            onCreate={handleCreateAgentProfile}
            onDelete={handleDeleteAgentProfile}
            onUpdate={handleUpdateAgentProfile}
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

        {selectedProject && selectedWorkItem && newAssignmentModalOpen && (
          <NewAssignmentModal
            error={newAssignmentError}
            pending={newAssignmentPending}
            project={selectedProject}
            workItem={selectedWorkItem}
            roles={roles}
            onClose={() => setNewAssignmentModalOpen(false)}
            onCreate={handleCreateAssignment}
          />
        )}

        {selectedProject && editingWorkItem && (
          <EditWorkItemModal
            error={editWorkError}
            item={editingWorkItem}
            pending={editWorkPending}
            project={selectedProject}
            roles={roles}
            onClose={() => setEditingWorkItem(null)}
            onSave={handleUpdateWorkItem}
          />
        )}

        {selectedProject && editingAssignment && (
          <EditAssignmentModal
            assignment={editingAssignment}
            error={editAssignmentError}
            pending={editAssignmentPending}
            project={selectedProject}
            workItem={selectedWorkItem}
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

        {editingHandoff && selectedWorkItem && (
          <ProjectHandoffModal
            key={editingHandoff === "new" ? "new" : editingHandoff.id}
            assignments={assignments}
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

        {reviewArtifactDraft && selectedWorkItem && (
          <ProjectReviewArtifactModal
            key={`${reviewArtifactDraft.assignmentID}:${reviewArtifactDraft.authorRoleID}`}
            assignments={assignments}
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

        {evidenceLinkModalOpen && selectedWorkItem && (
          <ProjectEvidenceLinkModal
            assignments={assignments}
            error={evidenceLinkError}
            pending={evidenceLinkPending}
            onClose={() => {
              setEvidenceLinkModalOpen(false);
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

        {deleteWorkItem && (
          <ConfirmModal
            title="Delete work item"
            danger
            pending={deleteWorkPending}
            confirmLabel="Delete work item"
            onClose={() => setDeleteWorkItem(null)}
            onConfirm={confirmDeleteWorkItem}
            message={
              <>
                Delete <strong>{deleteWorkItem.title}</strong> and its assignments and collaboration
                artifacts. Linked tasks, runs, chats, workspace files, and git history are not
                deleted.
              </>
            }
          />
        )}

        {deleteAssignment && (
          <ConfirmModal
            title="Delete assignment"
            danger
            pending={deleteAssignmentPending}
            confirmLabel="Delete assignment"
            onClose={() => setDeleteAssignment(null)}
            onConfirm={confirmDeleteAssignment}
            message={
              <>
                Delete the assignment metadata record for{" "}
                <strong>
                  {roleByID.get(deleteAssignment.role_id)?.name ?? deleteAssignment.role_id}
                </strong>
                . Linked tasks, runs, chats, and external-agent executions are not deleted or
                cancelled.
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
            <div style={{ ...projectIndexActionsStyle, opacity: actionsVisible ? 1 : 0 }}>
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
                style={{ ...projectIndexActionButtonStyle, color: "var(--red)" }}
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
  omittedAttentionCount,
  memoryCandidates,
  project,
  settingsOpen,
  onAttentionBucket,
  onAttentionDefaults,
  onAttentionMemory,
  onAttentionProfiles,
  onAttentionReviewCandidate,
  onAttentionRoles,
  onAttentionSkills,
  onAttentionTask,
  onAttentionWorkItem,
  onEditDefaults,
  onManageProfiles,
  onManageRoles,
  onRefresh,
}: {
  attentionItems: ProjectHealthAttention[];
  omittedAttentionCount: number;
  memoryCandidates: ProjectMemoryCandidateRecord[];
  project: ProjectRecord | null;
  settingsOpen: boolean;
  onAttentionBucket: (bucket: ProjectActivityBucketKey) => void;
  onAttentionDefaults: () => void;
  onAttentionMemory: () => void;
  onAttentionProfiles: () => void;
  onAttentionReviewCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onAttentionRoles: () => void;
  onAttentionSkills: () => void;
  onAttentionTask?: (taskID: string, runID?: string) => void;
  onAttentionWorkItem: (workItemID: string) => void;
  onEditDefaults: () => void;
  onManageProfiles: () => void;
  onManageRoles: () => void;
  onRefresh: () => void;
}) {
  const workspace = project ? projectDefaultWorkspace(project) : "";
  const subline = project
    ? `${workspace || "No default root"}${project.default_model ? ` · ${project.default_model}` : ""}`
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
            onAttentionBucket={onAttentionBucket}
            onAttentionDefaults={onAttentionDefaults}
            onAttentionMemory={onAttentionMemory}
            onAttentionProfiles={onAttentionProfiles}
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
            aria-label="Agent profiles"
            title="Agent profiles"
            onClick={onManageProfiles}
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

function upsertAgentProfile(items: AgentProfileRecord[], item: AgentProfileRecord) {
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

function projectOperationsActivityBucket(value?: string): ProjectActivityBucketKey | null {
  switch (value) {
    case "all":
    case "active":
    case "blocked":
    case "completed":
    case "recent":
      return value;
    default:
      return null;
  }
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

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";

import { useProjects } from "../../app/state/projects";
import { useProvidersAndModels } from "../../app/state/providersAndModels";
import { useSettings } from "../../app/state/settings";
import {
  ApiError,
  createAgentProfile,
  createProjectAssignment,
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
  getProjectMemory,
  getProjectMemoryCandidates,
  getProjectSkills,
  getProjectWorkItem,
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
import { providerDisplayName } from "../../lib/provider-utils";
import { ChatRightPanel } from "../chats/ChatRightPanel";
import {
  buildProjectHealthSummary,
  projectActivityWorkItemToWorkItem,
  type ProjectActivityBucketKey,
  type ProjectHealthAttention,
} from "./projectInsights";
import { toProjectAssignmentExecutionViewModel } from "./projectAssignmentViewModels";
import { ProjectAssistantPanel } from "./ProjectAssistantPanel";
import { EditAssignmentModal, NewAssignmentModal } from "./ProjectAssignmentModals";
import { CreateProjectWorktreeModal } from "./CreateProjectWorktreeModal";
import { ProjectHandoffModal } from "./ProjectHandoffModal";
import { ProjectHealthPanel } from "./ProjectHealthPanel";
import { ProjectMemoryModal, ProjectMemoryPanel, type MemoryForm } from "./ProjectMemoryPanel";
import { ProjectSkillsPanel } from "./ProjectSkillsPanel";
import { ProjectTimelinePanel } from "./ProjectTimelinePanel";
import {
  ProjectWorkItemDetail,
  type ProjectAssignmentChatLaunchRequest,
} from "./ProjectWorkItemDetail";
import { ProfilesModal } from "./ProfilesModal";
import { EditWorkItemModal, NewWorkItemModal } from "./ProjectWorkItemModals";
import { RolesModal } from "./RolesModal";
import { useProjectAssistantController } from "./useProjectAssistantController";
import type {
  ProjectAssignmentRecord,
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectMemoryCandidateRecord,
  ProjectCollaborationArtifactRecord,
  CreateProjectWorktreeRootPayload,
  ProjectHandoffRecord,
  ProjectMemoryRecord,
  ProjectSkillRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
  UpdateProjectPayload,
  UpdateProjectSkillPayload,
} from "../../types/project";
import type { AgentProfileRecord } from "../../types/agent-profile";
import { Badge, ConfirmModal, Icon, Icons, InlineError, type ProviderOption } from "../shared/ui";
import { ProjectSettingsPanel } from "./ProjectSettingsPanel";
import { type CreateWorktreeForm, type ProjectDefaultsForm } from "./projectSettings";
import {
  profileCreatePayloadFromForm,
  profileUpdatePayloadFromForm,
  projectSkillStatusRank,
  rolePayloadFromForm,
  type AgentProfileForm,
  type RoleForm,
} from "./projectProfilesRoles";
import {
  assignmentCreatePayloadFromForm,
  assignmentUpdatePayloadFromForm,
  handoffFormFromAssignment,
  handoffPayloadFromForm,
  reviewHandoffFormFromAssignment,
  type EditAssignmentForm,
  type EditWorkItemForm,
  type HandoffForm,
  type NewAssignmentForm,
  type NewWorkItemForm,
  workItemCreatePayloadFromForm,
  workItemUpdatePayloadFromForm,
} from "./projectWorkForms";
import {
  formatProjectRowRelativeTime,
  projectErrorMessage as errorMessage,
  workStatusLabel,
} from "./projectDisplay";

type Props = {
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenConnections?: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
};

const RIGHT_PANEL_WIDTH_KEY = "hecate.chat.rightPanelWidth";
const DEFAULT_RIGHT_PANEL_WIDTH = 380;

type WorkItemSummary = {
  assignmentCount: number;
  activeCount: number;
  failedCount: number;
  completedCount: number;
};

type LoadState = "idle" | "loading" | "loaded" | "error";

type ProjectWorkspaceTab = "work" | "timeline" | "memory" | "skills";

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

const detailStyle: CSSProperties = {
  flex: 1,
  minWidth: 0,
  minHeight: 0,
  overflow: "auto",
  background: "var(--bg0)",
  display: "grid",
  alignContent: "start",
};

export function ProjectsView({ onOpenChat, onOpenConnections, onOpenTask }: Props) {
  const projects = useProjects();
  const providersAndModels = useProvidersAndModels();
  const settings = useSettings();
  const [selectedProjectID, setSelectedProjectID] = useState(projects.activeProjectID);
  const [renamingProjectID, setRenamingProjectID] = useState("");
  const [renameValue, setRenameValue] = useState("");
  const [hoveredProjectID, setHoveredProjectID] = useState("");
  const [deleteProjectID, setDeleteProjectID] = useState("");
  const [deletePending, setDeletePending] = useState(false);
  const [settingsPanelOpen, setSettingsPanelOpen] = useState(false);
  const [rightPanelWidth, setRightPanelWidth] = useState(() => readStoredRightPanelWidth());
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
  const [newWorkPending, setNewWorkPending] = useState(false);
  const [newWorkError, setNewWorkError] = useState("");
  const [editingWorkItem, setEditingWorkItem] = useState<ProjectWorkItemRecord | null>(null);
  const [editWorkPending, setEditWorkPending] = useState(false);
  const [editWorkError, setEditWorkError] = useState("");
  const [deleteWorkItem, setDeleteWorkItem] = useState<ProjectWorkItemRecord | null>(null);
  const [deleteWorkPending, setDeleteWorkPending] = useState(false);
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
  const [activityBucket, setActivityBucket] = useState<ProjectActivityBucketKey>("all");
  const [workspaceTab, setWorkspaceTab] = useState<ProjectWorkspaceTab>("work");
  const [roles, setRoles] = useState<ProjectWorkRoleRecord[]>([]);
  const [selectedWorkItemID, setSelectedWorkItemID] = useState("");
  const [selectedWorkItem, setSelectedWorkItem] = useState<ProjectWorkItemRecord | null>(null);
  const [assignments, setAssignments] = useState<ProjectAssignmentRecord[]>([]);
  const [artifacts, setArtifacts] = useState<ProjectCollaborationArtifactRecord[]>([]);
  const [handoffs, setHandoffs] = useState<ProjectHandoffRecord[]>([]);
  const [editingHandoff, setEditingHandoff] = useState<ProjectHandoffRecord | "new" | null>(null);
  const [newHandoffDraft, setNewHandoffDraft] = useState<HandoffForm | null>(null);
  const [handoffPending, setHandoffPending] = useState(false);
  const [handoffError, setHandoffError] = useState("");
  const [handoffActionID, setHandoffActionID] = useState("");
  const [workLoadState, setWorkLoadState] = useState<LoadState>("idle");
  const [detailLoadState, setDetailLoadState] = useState<LoadState>("idle");
  const [workError, setWorkError] = useState("");
  const [detailError, setDetailError] = useState("");
  const [assignmentErrors, setAssignmentErrors] = useState<Record<string, string>>({});
  const [startingAssignmentID, setStartingAssignmentID] = useState("");
  const startingAssignmentIDsRef = useRef<Set<string>>(new Set());

  function updateRightPanelWidth(width: number) {
    setRightPanelWidth(width);
    rememberRightPanelWidth(width);
  }
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
  const [promotingCandidate, setPromotingCandidate] = useState<ProjectMemoryCandidateRecord | null>(
    null,
  );
  const [rejectingCandidateID, setRejectingCandidateID] = useState("");
  const [memoryPending, setMemoryPending] = useState(false);
  const [deleteMemory, setDeleteMemory] = useState<ProjectMemoryRecord | null>(null);
  const [deleteMemoryPending, setDeleteMemoryPending] = useState(false);

  const selectedProject = useMemo(
    () => projects.state.projects.find((project) => project.id === selectedProjectID) ?? null,
    [projects.state.projects, selectedProjectID],
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
  const projectHealth = useMemo(
    () =>
      buildProjectHealthSummary(
        selectedProject,
        activity,
        workItems,
        memoryEntries,
        memoryCandidates,
        {
          agentProfiles,
          roles,
          skills: projectSkills,
        },
      ),
    [
      activity,
      agentProfiles,
      memoryCandidates,
      memoryEntries,
      projectSkills,
      roles,
      selectedProject,
      workItems,
    ],
  );
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

  useEffect(() => {
    if (projects.state.projects.length === 0) {
      setSelectedProjectID("");
      return;
    }
    if (projects.activeProjectID) {
      setSelectedProjectID(projects.activeProjectID);
      return;
    }
    setSelectedProjectID((current) =>
      current && projects.state.projects.some((project) => project.id === current)
        ? current
        : projects.state.projects[0]?.id || "",
    );
  }, [projects.activeProjectID, projects.state.projects]);

  const loadWorkForProject = useCallback(async (projectID: string, preferredWorkItemID = "") => {
    setWorkError("");
    setDetailError("");
    setAssignmentErrors({});
    setWorkItems([]);
    setWorkItemSummaries({});
    setActivity(null);
    if (!preferredWorkItemID) {
      setSelectedWorkItemID("");
      setSelectedWorkItem(null);
      setAssignments([]);
      setArtifacts([]);
      setHandoffs([]);
    }
    if (!projectID) {
      setWorkLoadState("idle");
      return "";
    }
    setWorkLoadState("loading");
    try {
      const activityLoad = getProjectActivity(projectID).catch(() => null);
      const [rolesRes, workRes, activityRes] = await Promise.all([
        getProjectWorkRoles(projectID),
        getProjectWorkItems(projectID),
        activityLoad,
      ]);
      const nextRoles = rolesRes.data ?? [];
      const nextItems = workRes.data ?? [];
      setRoles(nextRoles);
      setWorkItems(nextItems);
      setActivity(activityRes?.data ?? null);
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
      setWorkError(errorMessage(error, "Failed to load project work."));
      return "";
    }
  }, []);

  const loadProjectMemory = useCallback(async (projectID: string) => {
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
    } catch (error) {
      setMemoryLoadState("error");
      setMemoryError(errorMessage(error, "Failed to load project memory."));
    }
  }, []);

  const loadProjectSkills = useCallback(async (projectID: string) => {
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
    } catch (error) {
      setSkillsLoadState("error");
      setSkillsError(errorMessage(error, "Failed to load project skills."));
    }
  }, []);

  const loadWorkItemDetail = useCallback(async (projectID: string, workItemID: string) => {
    setDetailError("");
    setAssignmentErrors({});
    if (!projectID || !workItemID) {
      setSelectedWorkItem(null);
      setAssignments([]);
      setArtifacts([]);
      setHandoffs([]);
      setDetailLoadState("idle");
      return;
    }
    setDetailLoadState("loading");
    try {
      const [itemRes, assignmentRes, artifactRes, handoffRes] = await Promise.all([
        getProjectWorkItem(projectID, workItemID),
        getProjectAssignments(projectID, workItemID),
        getProjectCollaborationArtifacts(projectID, workItemID),
        getProjectHandoffs(projectID, workItemID),
      ]);
      setSelectedWorkItem(itemRes.data);
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

  function openProject(projectID: string) {
    if (projectID !== selectedProjectID) {
      setSelectedWorkItemID("");
    }
    setSelectedProjectID(projectID);
    void projects.actions.selectProject(projectID);
  }

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
          setSelectedProjectID("");
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
      setCreateWorktreeOpen(false);
    } catch (error) {
      setCreateWorktreeError(errorMessage(error, "Failed to create project worktree."));
    } finally {
      setCreateWorktreePending(false);
    }
  }

  async function handleDiscoverContextSources() {
    if (!selectedProjectID) return;
    setDiscoveringContext(true);
    setMemoryError("");
    try {
      const payload = await discoverProjectContextSources(selectedProjectID);
      projects.actions.setProjects((current) => upsertProject(current, payload.data));
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
      setEditingMemory(null);
    } catch (error) {
      setMemoryError(errorMessage(error, "Failed to save project memory."));
    } finally {
      setMemoryPending(false);
    }
  }

  async function confirmDeleteMemory() {
    if (!selectedProjectID || !deleteMemory) return;
    setDeleteMemoryPending(true);
    setMemoryError("");
    try {
      await deleteProjectMemory(selectedProjectID, deleteMemory.id);
      setMemoryEntries((current) => current.filter((item) => item.id !== deleteMemory.id));
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
    } catch (error) {
      setMemoryError(errorMessage(error, "Failed to reject memory candidate."));
    } finally {
      setRejectingCandidateID("");
    }
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

  async function handleCreateAssignmentFromHandoff(handoff: ProjectHandoffRecord) {
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
      setHandoffError(errorMessage(error, "Failed to create target assignment."));
    } finally {
      setHandoffActionID("");
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
  const projectNeedsOnboarding =
    Boolean(selectedProject) &&
    workLoadState === "loaded" &&
    workItems.length === 0 &&
    !assistant.proposal &&
    !assistant.applyResult;
  const projectEmptyTitle =
    projects.state.projects.length === 0 ? "Add a project to begin" : "Select a project";
  const projectEmptyDetail =
    projects.state.projects.length === 0
      ? "Choose a workspace folder from the project list to create the first durable project record."
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
              onClick={() => void projects.actions.createProjectFromFolder()}
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
            <EmptyBlock title="Loading projects…" detail="Checking the local project catalog." />
          )}
          {!projects.state.loading && projects.state.projects.length === 0 && (
            <EmptyBlock
              title="No projects yet"
              detail="Add a workspace folder to create the first durable project record."
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
          attentionItems={projectHealth.attention}
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
            assignmentErrors={assignmentErrors}
            assignments={assignments}
            assistant={assistant}
            detailError={detailError}
            detailLoadState={detailLoadState}
            discoveringContext={discoveringContext}
            discoveringSkills={discoveringSkills}
            handoffActionID={handoffActionID}
            handoffError={handoffError}
            handoffs={handoffs}
            hasWorkItemDetail={hasWorkItemDetail}
            memoryCandidates={memoryCandidates}
            memoryEntries={memoryEntries}
            memoryError={memoryError}
            memoryLoadState={memoryLoadState}
            onActivityBucketChange={setActivityBucket}
            onAddAssignment={() => {
              setNewAssignmentError("");
              setNewAssignmentModalOpen(true);
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
            onCreateAssignmentFromHandoff={handleCreateAssignmentFromHandoff}
            onCreateWork={() => {
              setNewWorkError("");
              setNewWorkModalOpen(true);
            }}
            onDeleteAssignment={setDeleteAssignment}
            onDeleteHandoff={(handoff) => void handleDeleteHandoff(handoff)}
            onDeleteMemory={setDeleteMemory}
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
            onEditWorkItem={(item) => {
              setEditWorkError("");
              setEditingWorkItem(item);
            }}
            onNewMemory={() => setEditingMemory("new")}
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
            projectSkills={projectSkills}
            rejectingCandidateID={rejectingCandidateID}
            roleByID={roleByID}
            roles={roles}
            selectedWorkItem={selectedWorkItem}
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
              onWidthChange={updateRightPanelWidth}
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
            roles={roles}
            onClose={() => setNewWorkModalOpen(false)}
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
      </div>
    </div>
  );
}

type ProjectWorkspaceViewProps = {
  activity: ProjectActivityData | null;
  activityBucket: ProjectActivityBucketKey;
  activityByAssignmentID: Map<string, ProjectActivityItemRecord>;
  artifacts: ProjectCollaborationArtifactRecord[];
  assignmentErrors: Record<string, string>;
  assignments: ProjectAssignmentRecord[];
  assistant: ReturnType<typeof useProjectAssistantController>;
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
  onCreateAssignmentFromHandoff: (handoff: ProjectHandoffRecord) => void;
  onCreateWork: () => void;
  onDeleteAssignment: (assignment: ProjectAssignmentRecord) => void;
  onDeleteHandoff: (handoff: ProjectHandoffRecord) => void;
  onDeleteMemory: (entry: ProjectMemoryRecord) => void;
  onDeleteWorkItem: (item: ProjectWorkItemRecord) => void;
  onDiscoverContextSources: () => void;
  onDiscoverProjectSkills: () => void;
  onEditAssignment: (assignment: ProjectAssignmentRecord) => void;
  onEditHandoff: (handoff: ProjectHandoffRecord) => void;
  onEditMemory: (entry: ProjectMemoryRecord) => void;
  onEditWorkItem: (item: ProjectWorkItemRecord) => void;
  onManageProfiles: () => void;
  onManageRoles: () => void;
  onNewMemory: () => void;
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
  rejectingCandidateID: string;
  roleByID: Map<string, ProjectWorkRoleRecord>;
  roles: ProjectWorkRoleRecord[];
  selectedWorkItem: ProjectWorkItemRecord | null;
  selectedWorkItemID: string;
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

function ProjectWorkspaceView({
  activity,
  activityBucket,
  activityByAssignmentID,
  artifacts,
  assignmentErrors,
  assignments,
  assistant,
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
  onAddHandoff,
  onAddHandoffFromAssignment,
  onAddReviewHandoffFromAssignment,
  onCreateAssignmentFromHandoff,
  onCreateWork,
  onDeleteAssignment,
  onDeleteHandoff,
  onDeleteMemory,
  onDeleteWorkItem,
  onDiscoverContextSources,
  onDiscoverProjectSkills,
  onEditAssignment,
  onEditHandoff,
  onEditMemory,
  onEditWorkItem,
  onManageProfiles,
  onManageRoles,
  onNewMemory,
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
  rejectingCandidateID,
  roleByID,
  roles,
  selectedWorkItem,
  selectedWorkItemID,
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
  return (
    <section style={detailStyle} aria-label="Selected work item">
      <div className="project-cockpit-workspace" style={cockpitWorkspaceStyle}>
        {project ? (
          <section style={domainSectionStyle} aria-label="Project workspace">
            {projectNeedsOnboarding ? (
              <ProjectOnboardingPanel
                bootstrapPending={assistant.bootstrapPending}
                contextSourceCount={
                  (project.context_sources ?? []).filter((source) => source.enabled).length
                }
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
                  context={assistant.context}
                  contextError={assistant.contextError}
                  contextStatus={assistant.contextStatus}
                  error={assistant.error}
                  onApply={() => void assistant.apply()}
                  onBootstrap={() => void assistant.bootstrap()}
                  onInspectContext={(form) => void assistant.inspectContext(form)}
                  onDismiss={assistant.dismiss}
                  onPropose={(form) => void assistant.propose(form)}
                  project={project}
                  proposal={assistant.proposal}
                  roles={roles}
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
                        handoffActionID={handoffActionID}
                        handoffError={handoffError}
                        handoffs={handoffs}
                        assignmentErrors={assignmentErrors}
                        detailError={detailError}
                        loading={detailLoadState === "loading"}
                        onOpenTask={onOpenTask}
                        onRefresh={onRefreshWorkItem}
                        onCreateAssignmentFromHandoff={onCreateAssignmentFromHandoff}
                        activityByAssignmentID={activityByAssignmentID}
                        onDeleteHandoff={onDeleteHandoff}
                        onDeleteWorkItem={onDeleteWorkItem}
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
                        startingAssignmentID={startingAssignmentID}
                        workItem={selectedWorkItem}
                        onAddAssignment={onAddAssignment}
                        onAddHandoff={onAddHandoff}
                        onAddHandoffFromAssignment={onAddHandoffFromAssignment}
                        onAddReviewHandoffFromAssignment={onAddReviewHandoffFromAssignment}
                      />
                    ) : (
                      <EmptyBlock
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
                onPromoteCandidate={onPromoteCandidate}
                onRejectCandidate={onRejectCandidate}
                onDelete={onDeleteMemory}
                onEdit={onEditMemory}
                onNew={onNewMemory}
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
            <EmptyBlock title={projectEmptyTitle} detail={projectEmptyDetail} />
          </section>
        )}
      </div>
    </section>
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
  const hasDefaults = Boolean(project.default_provider && project.default_model);
  const hasGuidance = contextSourceCount > 0 || skillCount > 0;
  const checks = [
    {
      label: "Workspace root",
      detail: hasRoot ? projectDefaultWorkspace(project) || "Ready" : "Missing",
      done: hasRoot,
    },
    {
      label: "Provider and model",
      detail: hasDefaults ? `${project.default_provider} / ${project.default_model}` : "Not set",
      done: hasDefaults,
    },
    {
      label: "Guidance and skills",
      detail: `${contextSourceCount} sources · ${skillCount} skills`,
      done: hasGuidance,
    },
    {
      label: "Roles and first work",
      detail: `${roleCount} roles · no work items`,
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
          Create reviewable setup actions from this workspace: roles, guidance, skills, and first
          work.
        </div>
        <div style={projectOnboardingActionsStyle}>
          <button
            className="btn btn-primary btn-sm"
            type="button"
            disabled={bootstrapPending}
            onClick={onBootstrap}
          >
            <Icon d={Icons.refresh} size={13} />
            {bootstrapPending ? "Bootstrapping..." : "Bootstrap project"}
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
              {check.done ? "ready" : "todo"}
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

function summarizeAssignments(assignments: ProjectAssignmentRecord[]): WorkItemSummary {
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

function readStoredRightPanelWidth(): number {
  try {
    const value = Number.parseInt(localStorage.getItem(RIGHT_PANEL_WIDTH_KEY) ?? "", 10);
    return Number.isFinite(value) && value > 0 ? value : DEFAULT_RIGHT_PANEL_WIDTH;
  } catch {
    return DEFAULT_RIGHT_PANEL_WIDTH;
  }
}

function rememberRightPanelWidth(width: number) {
  try {
    localStorage.setItem(RIGHT_PANEL_WIDTH_KEY, String(width));
  } catch {
    // Best-effort preference only.
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

const panelStyle: CSSProperties = {
  border: "1px solid var(--border)",
  background: "var(--bg1)",
  borderRadius: "var(--radius-sm)",
  boxSizing: "border-box",
  maxWidth: "100%",
  minWidth: 0,
  padding: 12,
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

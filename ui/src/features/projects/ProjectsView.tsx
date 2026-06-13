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
  getProjectAssignmentContext,
  getProjectAssignmentPreflight,
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
import { formatAbsoluteTime } from "../../lib/format";
import { projectDefaultWorkspace } from "../../lib/project-workspace";
import { providerDisplayName } from "../../lib/provider-utils";
import { ChatRightPanel } from "../chats/ChatRightPanel";
import { ContextInspectorModalTrigger, ContextInspectorPanel } from "../shared/ContextInspector";
import { useFloatingMenu } from "../shared/useFloatingMenu";
import {
  activitySignalLabel,
  buildProjectHealthSummary,
  buildProjectTimelineItems,
  projectActivityWorkItemToWorkItem,
  timelineBadgeClass,
  timelineKindLabel,
  type ProjectActivityBucketKey,
  type ProjectHealthAttention,
  type ProjectTimelineItem,
} from "./projectInsights";
import {
  toProjectActivityItemViewModel,
  toProjectAssignmentEvidenceViewModel,
  toProjectAssignmentExecutionViewModel,
} from "./projectAssignmentViewModels";
import { ProjectAssistantPanel } from "./ProjectAssistantPanel";
import { EditAssignmentModal, NewAssignmentModal } from "./ProjectAssignmentModals";
import { CreateProjectWorktreeModal } from "./CreateProjectWorktreeModal";
import { ProjectHandoffModal } from "./ProjectHandoffModal";
import { ProjectMemoryModal, ProjectMemoryPanel, type MemoryForm } from "./ProjectMemoryPanel";
import { ProjectSkillsPanel } from "./ProjectSkillsPanel";
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
import type { ContextPacketRecord } from "../../types/context";
import {
  Badge,
  ConfirmModal,
  CopyableID,
  Icon,
  Icons,
  InlineError,
  Modal,
  type ProviderOption,
} from "../shared/ui";
import { ProjectSettingsPanel } from "./ProjectSettingsPanel";
import {
  projectRootOptionLabel,
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
import { firstNonEmpty, shortID } from "./projectUtils";

type Props = {
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenConnections?: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
};

const RIGHT_PANEL_WIDTH_KEY = "hecate.chat.rightPanelWidth";
const DEFAULT_RIGHT_PANEL_WIDTH = 380;

export type ProjectAssignmentChatLaunchRequest = {
  projectID: string;
  chatSessionID?: string;
  provider?: string;
  model?: string;
  title?: string;
  draft?: string;
};

type WorkItemSummary = {
  assignmentCount: number;
  activeCount: number;
  failedCount: number;
  completedCount: number;
};

type LoadState = "idle" | "loading" | "loaded" | "error";

type ProjectWorkspaceTab = "work" | "timeline" | "memory" | "skills";

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
                      <WorkItemDetail
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

function formatProjectRowRelativeTime(iso: string): string {
  const parsed = Date.parse(iso);
  if (!Number.isFinite(parsed)) return iso || "—";
  const diff = Math.max(0, Date.now() - parsed);
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  if (day < 14) return `${day}d ago`;
  const week = Math.floor(day / 7);
  if (week < 8) return `${week}w ago`;
  const month = Math.floor(day / 30);
  if (day < 365) return `${Math.max(1, month)}mo ago`;
  return `${Math.max(1, Math.floor(day / 365))}y ago`;
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
  const attentionMenu = useFloatingMenu<HTMLDivElement, HTMLButtonElement>({
    portalSelector: null,
  });
  const workspace = project ? projectDefaultWorkspace(project) : "";
  const subline = project
    ? `${workspace || "No default root"}${project.default_model ? ` · ${project.default_model}` : ""}`
    : "";
  const attentionCount = attentionItems.length;
  const handleAttentionAction = (item: ProjectHealthAttention) => {
    if (item.action === "settings" || item.id.endsWith(":defaults")) {
      onAttentionDefaults();
    } else if (item.action === "skills") {
      onAttentionSkills();
    } else if (item.action === "profiles") {
      onAttentionProfiles();
    } else if (item.action === "roles") {
      onAttentionRoles();
    } else if (item.candidateID) {
      const candidate = memoryCandidates.find((candidate) => candidate.id === item.candidateID);
      if (candidate) onAttentionReviewCandidate(candidate);
      else onAttentionMemory();
    } else if (item.workItemID) {
      onAttentionWorkItem(item.workItemID);
    } else if (item.taskID) {
      onAttentionTask?.(item.taskID, item.runID);
    } else if (item.bucket) {
      onAttentionBucket(item.bucket);
    } else if (item.action === "memory" || item.id.endsWith(":context")) {
      onAttentionMemory();
    }
    attentionMenu.close();
  };
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
          <div ref={attentionMenu.wrapRef} style={projectAttentionMenuStyle}>
            <button
              ref={attentionMenu.triggerRef}
              className="btn btn-ghost btn-sm"
              type="button"
              aria-expanded={attentionMenu.open}
              aria-label={`Project attention${attentionCount > 0 ? `: ${attentionCount}` : ""}`}
              title="Project attention"
              onClick={attentionMenu.toggle}
              disabled={!project}
              style={{
                ...projectHeaderActionButtonStyle,
                color: attentionCount > 0 ? "var(--amber)" : "var(--t2)",
              }}
            >
              <Icon d={Icons.warning} size={13} />
              {attentionCount > 0 && (
                <span style={projectAttentionCountStyle}>{attentionCount}</span>
              )}
            </button>
            {attentionMenu.open && project && (
              <div
                ref={attentionMenu.menuRef}
                role="menu"
                aria-label="Project attention"
                style={projectAttentionPopoverStyle}
              >
                <div style={projectAttentionPopoverHeaderStyle}>
                  <div style={sectionLabelStyle}>Needs Attention</div>
                  <span className="badge badge-muted">{attentionCount}</span>
                </div>
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
                          attentionMenu.close();
                        }}
                        onOpenTask={(taskID, runID) => {
                          onAttentionTask?.(taskID, runID);
                          attentionMenu.close();
                        }}
                        onReviewCandidate={(candidate) => {
                          onAttentionReviewCandidate(candidate);
                          attentionMenu.close();
                        }}
                        onSelectWorkItem={(workItemID) => {
                          onAttentionWorkItem(workItemID);
                          attentionMenu.close();
                        }}
                        reviewCandidate={memoryCandidates.find(
                          (candidate) => candidate.id === item.candidateID,
                        )}
                      />
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
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
            {item.actionLabel ?? "Inbox"}
          </button>
        )}
        {item.workItemID && (
          <button
            className="btn btn-ghost btn-sm project-attention-item-action"
            type="button"
            aria-label="Open attention details"
            onClick={(event) => {
              event.stopPropagation();
              onSelectWorkItem(item.workItemID!);
            }}
          >
            Details
          </button>
        )}
        {item.taskID && (
          <button
            className="btn btn-ghost btn-sm project-attention-item-action"
            type="button"
            aria-label="Open attention task"
            onClick={(event) => {
              event.stopPropagation();
              onOpenTask?.(item.taskID!, item.runID);
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

function ProjectTimelinePanel({
  activity,
  artifacts,
  handoffs,
  memoryCandidates,
  memoryEntries,
  onEditMemory,
  onOpenChat,
  onOpenTask,
  onSelectWorkItem,
  project,
  roles,
  workItems,
}: {
  activity: ProjectActivityData | null;
  artifacts: ProjectCollaborationArtifactRecord[];
  handoffs: ProjectHandoffRecord[];
  memoryCandidates: ProjectMemoryCandidateRecord[];
  memoryEntries: ProjectMemoryRecord[];
  onEditMemory: (entry: ProjectMemoryRecord) => void;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onSelectWorkItem: (workItemID: string) => void;
  project: ProjectRecord | null;
  roles: ProjectWorkRoleRecord[];
  workItems: ProjectWorkItemRecord[];
}) {
  const timeline = useMemo(
    () =>
      project
        ? buildProjectTimelineItems({
            activity,
            artifacts,
            handoffs,
            memoryCandidates,
            memoryEntries,
            project,
            roles,
            workItems,
          })
        : [],
    [activity, artifacts, handoffs, memoryCandidates, memoryEntries, project, roles, workItems],
  );
  const decisions = timeline.filter((item) => item.kind === "decision");
  const timelineLimit = 12;
  const decisionLimit = 5;
  const visibleTimeline = timeline.slice(0, timelineLimit);
  const visibleDecisions = decisions.slice(0, decisionLimit);
  if (!project) return null;

  return (
    <div>
      <div style={panelStyle}>
        <div style={{ display: "flex", alignItems: "flex-start", gap: 10, marginBottom: 12 }}>
          <div>
            <div style={sectionLabelStyle}>Timeline / Decision Log</div>
            <div style={{ ...subtleTextStyle, marginTop: 3 }}>
              {timeline.length} project story item{timeline.length === 1 ? "" : "s"} from activity,
              memory, and collaboration artifacts.
            </div>
          </div>
          <span className="badge badge-muted" style={{ marginLeft: "auto" }}>
            {decisions.length} decision{decisions.length === 1 ? "" : "s"}
          </span>
        </div>
        <div style={timelineGridStyle}>
          <section aria-label="Project timeline" style={{ minWidth: 0 }}>
            <div style={{ ...sectionLabelStyle, color: "var(--t2)", marginBottom: 8 }}>
              Project Timeline
            </div>
            {timeline.length === 0 ? (
              <div style={subtleTextStyle}>
                No timeline entries yet. Assignments, memory changes, and collaboration artifacts
                will appear here.
              </div>
            ) : (
              <div style={{ display: "grid", gap: 9 }}>
                {timeline.length > visibleTimeline.length ? (
                  <div style={subtleTextStyle}>
                    Showing {visibleTimeline.length} of {timeline.length} story items.
                  </div>
                ) : null}
                {visibleTimeline.map((item) => (
                  <ProjectTimelineRow
                    key={item.id}
                    item={item}
                    onEditMemory={onEditMemory}
                    onOpenChat={onOpenChat}
                    onOpenTask={onOpenTask}
                    onSelectWorkItem={onSelectWorkItem}
                    project={project}
                    roles={roles}
                    workItems={workItems}
                  />
                ))}
              </div>
            )}
          </section>
          <section aria-label="Decision log" style={decisionLogStyle}>
            <div style={{ ...sectionLabelStyle, color: "var(--t2)", marginBottom: 8 }}>
              Decisions
            </div>
            {decisions.length === 0 ? (
              <div style={subtleTextStyle}>
                No explicit decision notes yet. Existing decision_note artifacts will be collected
                here without creating durable decisions automatically.
              </div>
            ) : (
              <div style={{ display: "grid", gap: 8 }}>
                {decisions.length > visibleDecisions.length ? (
                  <div style={subtleTextStyle}>
                    Showing {visibleDecisions.length} of {decisions.length} decisions.
                  </div>
                ) : null}
                {visibleDecisions.map((item) => (
                  <ProjectDecisionRow
                    key={item.id}
                    item={item}
                    onSelectWorkItem={onSelectWorkItem}
                  />
                ))}
              </div>
            )}
          </section>
        </div>
      </div>
    </div>
  );
}

function ProjectTimelineRow({
  item,
  onEditMemory,
  onOpenChat,
  onOpenTask,
  onSelectWorkItem,
  project,
  roles,
  workItems,
}: {
  item: ProjectTimelineItem;
  onEditMemory: (entry: ProjectMemoryRecord) => void;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onSelectWorkItem: (workItemID: string) => void;
  project: ProjectRecord;
  roles: ProjectWorkRoleRecord[];
  workItems: ProjectWorkItemRecord[];
}) {
  const workItem =
    item.workItemID && workItems.find((candidate) => candidate.id === item.workItemID);
  const role =
    item.assignment && roles.find((candidate) => candidate.id === item.assignment?.role_id);
  const chatRequest =
    item.assignment && workItem
      ? buildProjectAssignmentChatLaunchRequest({
          project,
          workItem,
          assignment: item.assignment,
          role: role ?? null,
        })
      : null;
  const memoryEntry = item.memoryEntry;
  return (
    <div style={timelineItemStyle}>
      <div style={timelineItemHeaderStyle}>
        <div style={timelineItemTitleRowStyle}>
          <span className={timelineBadgeClass(item)}>{timelineKindLabel(item.kind)}</span>
          {item.status && <Badge status={item.status} label={activitySignalLabel(item.status)} />}
          <div style={{ ...titleStyle, minWidth: 0 }}>{item.title}</div>
        </div>
        <div style={timelineItemActionsStyle}>
          {item.workItemID && (
            <button
              aria-label={`Show timeline details for ${item.title}`}
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => onSelectWorkItem(item.workItemID ?? "")}
            >
              Details
            </button>
          )}
          {item.taskID && (
            <button
              aria-label={`Open timeline task ${shortID(item.taskID)}`}
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => onOpenTask?.(item.taskID ?? "", item.runID)}
              disabled={!onOpenTask}
              title="Open task"
            >
              <Icon d={Icons.tasks} size={12} />
              Task
            </button>
          )}
          {chatRequest && (
            <button
              aria-label={`Open timeline chat for ${item.title}`}
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => onOpenChat?.(chatRequest)}
              disabled={!onOpenChat || !chatRequest.model}
              title={
                chatRequest.model
                  ? `Open chat with ${chatRequest.model}`
                  : "Set project defaults before opening chat."
              }
            >
              <Icon d={Icons.chat} size={12} />
              Chat
            </button>
          )}
          {memoryEntry && (
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              onClick={() => onEditMemory(memoryEntry)}
            >
              <Icon d={Icons.edit} size={12} />
              Inspect
            </button>
          )}
        </div>
      </div>
      {item.summary && <div style={timelineSummaryStyle}>{item.summary}</div>}
      <div style={metaLineStyle}>
        {item.actor && <span>{item.actor}</span>}
        {item.source && <span>{item.source}</span>}
        {item.runID && <span>run {shortID(item.runID)}</span>}
        {item.chatID && <span>chat {shortID(item.chatID)}</span>}
        {item.timestamp && <span>{formatAbsoluteTime(item.timestamp)}</span>}
      </div>
    </div>
  );
}

function ProjectDecisionRow({
  item,
  onSelectWorkItem,
}: {
  item: ProjectTimelineItem;
  onSelectWorkItem: (workItemID: string) => void;
}) {
  return (
    <div style={decisionItemStyle}>
      <div style={{ display: "flex", gap: 8, alignItems: "center", minWidth: 0 }}>
        <span className="badge badge-amber">decision_note</span>
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{item.title}</div>
        {item.workItemID && (
          <button
            aria-label={`Show decision details for ${item.title}`}
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={() => onSelectWorkItem(item.workItemID ?? "")}
          >
            Details
          </button>
        )}
      </div>
      {item.summary && <div style={timelineSummaryStyle}>{item.summary}</div>}
      <div style={metaLineStyle}>
        {item.actor && <span>{item.actor}</span>}
        {item.timestamp && <span>{formatAbsoluteTime(item.timestamp)}</span>}
      </div>
    </div>
  );
}

function WorkItemDetail({
  activityByAssignmentID,
  assignments,
  artifacts,
  handoffActionID,
  handoffError,
  handoffs,
  assignmentErrors,
  detailError,
  loading,
  onAddAssignment,
  onAddHandoff,
  onAddHandoffFromAssignment,
  onAddReviewHandoffFromAssignment,
  onCreateAssignmentFromHandoff,
  onDeleteAssignment,
  onDeleteHandoff,
  onDeleteWorkItem,
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
  startingAssignmentID,
  workItem,
}: {
  activityByAssignmentID: Map<string, ProjectActivityItemRecord>;
  assignments: ProjectAssignmentRecord[];
  artifacts: ProjectCollaborationArtifactRecord[];
  handoffActionID: string;
  handoffError: string;
  handoffs: ProjectHandoffRecord[];
  assignmentErrors: Record<string, string>;
  detailError: string;
  loading: boolean;
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
  onDeleteAssignment: (assignment: ProjectAssignmentRecord) => void;
  onDeleteHandoff: (handoff: ProjectHandoffRecord) => void;
  onDeleteWorkItem: (item: ProjectWorkItemRecord) => void;
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
  startingAssignmentID: string;
  workItem: ProjectWorkItemRecord | null;
}) {
  if (!workItem) {
    return (
      <EmptyBlock
        title={loading ? "Loading detail…" : "Select a work item"}
        detail="Assignments and collaboration artifacts appear here."
      />
    );
  }
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
        </section>
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
                const reviewRole = reviewerRoleForAssignment(workItem, assignment, roleByID);
                const activityItem = activityByAssignmentID.get(assignment.id);
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
                            const executionRef = toProjectAssignmentExecutionViewModel(assignment);
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
                    onCreateHandoff={() => onAddHandoffFromAssignment(assignment, activityItem)}
                    onCreateReviewHandoff={
                      reviewRole
                        ? () =>
                            onAddReviewHandoffFromAssignment(assignment, reviewRole, activityItem)
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
        <section style={workItemCardSectionStyle}>
          <div style={workItemSectionHeaderStyle}>
            <div style={sectionLabelStyle}>Collaboration Artifacts</div>
            <span className="badge badge-muted">{artifacts.length}</span>
          </div>
          {artifacts.length === 0 ? (
            <div style={subtleTextStyle}>No collaboration artifacts recorded yet.</div>
          ) : (
            <div style={{ display: "grid", gap: 8 }}>
              {artifacts.map((artifact) => (
                <div key={artifact.id} style={artifactStyle}>
                  <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
                    <span className="badge badge-muted">{artifact.kind}</span>
                    <span style={titleStyle}>{artifact.title || artifact.id}</span>
                  </div>
                  <div style={{ marginTop: 6, fontSize: 12, color: "var(--t2)", lineHeight: 1.45 }}>
                    {artifact.body}
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
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
                    role={handoff.target_role_id ? roleByID.get(handoff.target_role_id) : undefined}
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
      </article>
    </div>
  );
}

function AssignmentRow({
  activityItem,
  assignment,
  chatModel,
  error,
  loadContext,
  loadPreflight,
  onCreateHandoff,
  onCreateReviewHandoff,
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
  chatModel: string;
  error: string;
  loadContext?: (() => Promise<ContextPacketRecord>) | null;
  loadPreflight?: (() => Promise<ContextPacketRecord>) | null;
  onCreateHandoff: () => void;
  onCreateReviewHandoff?: () => void;
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
        detail: errorMessage(error, "Failed to load assignment launch preflight."),
      });
    }
  }

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
        detail: errorMessage(error, "Failed to load assignment launch preflight."),
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
  evidence: ReturnType<typeof toProjectAssignmentEvidenceViewModel>;
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

function buildProjectAssignmentChatLaunchRequest({
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

function projectRootDisplayLabel(project: ProjectRecord, rootID: string): string {
  const root = project.roots.find((item) => item.id === rootID);
  if (!root) return shortID(rootID);
  if (root.git_branch) return root.git_branch;
  const parts = root.path.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || root.id;
}

function projectRootTitle(project: ProjectRecord, rootID: string): string {
  const root = project.roots.find((item) => item.id === rootID);
  if (!root) return rootID;
  return projectRootOptionLabel(root);
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

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    return error.operatorAction
      ? `${error.message} ${error.operatorAction}`
      : error.message || fallback;
  }
  return error instanceof Error ? error.message : fallback;
}

function workStatusLabel(status: string): string {
  if (status === "done") return "done";
  return status.replaceAll("_", " ");
}

function assignmentStatusLabel(status: string | undefined): string {
  if (!status) return "unknown";
  if (status === "awaiting_approval") return "approval";
  if (status === "completed") return "done";
  return status.replaceAll("_", " ");
}

function handoffStatusLabel(status: string): string {
  switch (status) {
    case "pending":
      return "Pending";
    case "accepted":
      return "Accepted";
    case "superseded":
      return "Superseded";
    case "dismissed":
      return "Dismissed";
    default:
      return status || "Unknown";
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

const workItemCardSectionStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  marginTop: 12,
  minWidth: 0,
  paddingTop: 12,
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

const timelineGridStyle: CSSProperties = {
  display: "grid",
  gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 360px), 1fr))",
  gap: 14,
  alignItems: "start",
};

const timelineItemStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 9,
  minWidth: 0,
};

const timelineItemHeaderStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "grid",
  gap: 8,
  gridTemplateColumns: "minmax(0, 1fr)",
  minWidth: 0,
};

const timelineItemTitleRowStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flex: "1 1 160px",
  flexWrap: "wrap",
  gap: 8,
  minWidth: 0,
};

const timelineItemActionsStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 6,
  justifyContent: "flex-end",
  minWidth: 0,
  maxWidth: "100%",
};

const timelineSummaryStyle: CSSProperties = {
  marginTop: 6,
  color: "var(--t1)",
  fontSize: 12,
  lineHeight: 1.45,
  whiteSpace: "pre-wrap",
  overflowWrap: "anywhere",
  display: "-webkit-box",
  WebkitLineClamp: 3,
  WebkitBoxOrient: "vertical",
  overflow: "hidden",
};

const decisionLogStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 9,
  minWidth: 0,
};

const decisionItemStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 8,
  minWidth: 0,
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

const artifactStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 8,
};

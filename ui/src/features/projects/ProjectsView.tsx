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
  applyProjectAssistant,
  createAgentProfile,
  createProjectAssignment,
  createProjectHandoff,
  discoverProjectContextSources,
  discoverProjectSkills,
  draftProjectAssistant,
  getProjectAssistantContext,
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
import { ContextInspectorModalTrigger } from "../shared/ContextInspector";
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
  PROJECT_ASSISTANT_AUTO,
  ProjectAssistantPanel,
  type ProjectAssistantDraftForm,
  type ProjectAssistantStatus,
} from "./ProjectAssistantPanel";
import type {
  ProjectAssignmentRecord,
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectAssistantApplyResult,
  ProjectAssistantContextPayload,
  ProjectAssistantContextRecord,
  ProjectAssistantDraftPayload,
  ProjectAssistantProposal,
  ProjectMemoryCandidateRecord,
  ProjectCollaborationArtifactRecord,
  ProjectContextSourceRecord,
  CreateProjectHandoffPayload,
  ProjectHandoffRecord,
  ProjectMemoryRecord,
  ProjectSkillRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
  UpdateProjectAssignmentPayload,
  UpdateProjectPayload,
  UpdateProjectSkillPayload,
  UpdateProjectWorkItemPayload,
} from "../../types/project";
import type {
  AgentProfileRecord,
  CreateAgentProfilePayload,
  UpdateAgentProfilePayload,
} from "../../types/agent-profile";
import type { ModelRecord } from "../../types/model";
import type { ProviderPresetRecord } from "../../types/provider";
import type { ContextPacketRecord } from "../../types/context";
import {
  Badge,
  ConfirmModal,
  CopyableID,
  Icon,
  Icons,
  InlineError,
  Modal,
  ModelPicker,
  ProviderPicker,
  type ProviderOption,
} from "../shared/ui";

type Props = {
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
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

type NewWorkItemForm = {
  title: string;
  brief: string;
  priority: string;
  ownerRoleID: string;
};

type NewAssignmentForm = {
  roleID: string;
  driverKind: string;
};

type EditWorkItemForm = NewWorkItemForm & {
  id: string;
  status: string;
  reviewerRoleIDs: string;
};

type EditAssignmentForm = NewAssignmentForm & {
  id: string;
  status: string;
  taskID: string;
  runID: string;
  chatSessionID: string;
  messageID: string;
  contextSnapshotID: string;
};

type HandoffForm = {
  id: string;
  sourceAssignmentID: string;
  sourceRunID: string;
  sourceChatSessionID: string;
  sourceMessageID: string;
  targetRoleID: string;
  targetAssignmentID: string;
  title: string;
  summary: string;
  recommendedNextAction: string;
  linkedArtifactIDs: string;
  linkedMemoryIDs: string;
  contextRefs: string;
  status: string;
  provenanceKind: string;
  trustLabel: string;
};

type ProjectDefaultsForm = {
  provider: string;
  model: string;
  defaultAgentProfile: string;
  workspaceMode: string;
};

type MemoryForm = {
  title: string;
  body: string;
  trustLabel: string;
  sourceKind: string;
  sourceID: string;
  enabled: boolean;
};

type SkillForm = {
  title: string;
  description: string;
  trustLabel: string;
};

type AgentProfileForm = {
  id: string;
  name: string;
  description: string;
  instructions: string;
  surface: string;
  providerHint: string;
  modelHint: string;
  executionProfile: string;
  toolsEnabled: boolean;
  writesAllowed: boolean;
  networkAllowed: boolean;
  approvalPolicy: string;
  projectMemoryPolicy: string;
  contextSourcePolicy: string;
  skillIDs: string;
  externalAgentKind: string;
};

type RoleForm = {
  id: string;
  name: string;
  description: string;
  instructions: string;
  defaultDriverKind: string;
  defaultProvider: string;
  defaultModel: string;
  defaultAgentProfile: string;
  skillIDs: string;
};

const WORK_ITEM_STATUSES = [
  "backlog",
  "ready",
  "running",
  "review",
  "blocked",
  "done",
  "cancelled",
];
const WORK_ITEM_PRIORITIES = ["low", "normal", "high", "urgent"];
const ASSIGNMENT_STATUSES = [
  "queued",
  "running",
  "awaiting_approval",
  "completed",
  "failed",
  "cancelled",
];
const HANDOFF_STATUSES = ["pending", "accepted", "superseded", "dismissed"];
const MEMORY_TRUST_LABELS = [
  "operator_memory",
  "generated_summary",
  "handoff",
  "external_untrusted",
  "runtime_state",
];
const AGENT_PROFILE_SURFACES = ["any", "hecate_chat", "hecate_task", "external_agent"];
const AGENT_PROFILE_APPROVAL_POLICIES = ["inherit", "require", "block", "allow"];
const AGENT_PROFILE_MEMORY_POLICIES = ["inherit", "include", "visible_only", "exclude"];
const AGENT_PROFILE_CONTEXT_POLICIES = ["inherit", "include_enabled", "visible_only", "exclude"];
const MEMORY_SOURCE_KINDS = [
  "operator",
  "generated",
  "generated_summary",
  "task_output",
  "chat_message",
  "handoff",
  "project_launch_context",
  "external_handoff",
  "runtime_state",
];

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

export function ProjectsView({ onOpenChat, onOpenTask }: Props) {
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
  const [assistantProposal, setAssistantProposal] = useState<ProjectAssistantProposal | null>(null);
  const [assistantApplyResult, setAssistantApplyResult] =
    useState<ProjectAssistantApplyResult | null>(null);
  const [assistantContext, setAssistantContext] = useState<ProjectAssistantContextRecord | null>(
    null,
  );
  const [assistantContextStatus, setAssistantContextStatus] = useState<LoadState>("idle");
  const [assistantContextError, setAssistantContextError] = useState("");
  const [assistantStatus, setAssistantStatus] = useState<ProjectAssistantStatus>("idle");
  const [assistantError, setAssistantError] = useState("");

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
      ),
    [activity, memoryCandidates, memoryEntries, selectedProject, workItems],
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
    setAssistantProposal(null);
    setAssistantApplyResult(null);
    setAssistantContext(null);
    setAssistantContextError("");
    setAssistantContextStatus("idle");
    setAssistantError("");
    setAssistantStatus("idle");
  }, [selectedProjectID, selectedWorkItemID]);

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
      const payload = await createProjectWorkItem(selectedProjectID, {
        title,
        brief: form.brief.trim() || undefined,
        status: "ready",
        priority: form.priority || "normal",
        owner_role_id: form.ownerRoleID || undefined,
      });
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
    const patch: UpdateProjectWorkItemPayload = {
      title,
      brief: form.brief.trim(),
      status: form.status,
      priority: form.priority || "normal",
      owner_role_id: form.ownerRoleID,
      reviewer_role_ids: splitRoleIDs(form.reviewerRoleIDs),
    };
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
      const payload = await createProjectAssignment(selectedProjectID, selectedWorkItemID, {
        role_id: roleID,
        driver_kind: form.driverKind || "hecate_task",
      });
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
    const patch: UpdateProjectAssignmentPayload = {
      role_id: roleID,
      driver_kind: form.driverKind || "hecate_task",
      status: form.status || "queued",
      task_id: form.taskID.trim(),
      run_id: form.runID.trim(),
      chat_session_id: form.chatSessionID.trim(),
      message_id: form.messageID.trim(),
      context_snapshot_id: form.contextSnapshotID.trim(),
    };
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
    const payload: CreateProjectHandoffPayload = {
      source_assignment_id: form.sourceAssignmentID.trim(),
      source_run_id: form.sourceRunID.trim(),
      source_chat_session_id: form.sourceChatSessionID.trim(),
      source_message_id: form.sourceMessageID.trim(),
      target_role_id: form.targetRoleID.trim(),
      target_assignment_id: form.targetAssignmentID.trim(),
      title,
      summary,
      recommended_next_action: recommendedNextAction,
      linked_artifact_ids: splitIDs(form.linkedArtifactIDs),
      linked_memory_ids: splitIDs(form.linkedMemoryIDs),
      context_refs: splitIDs(form.contextRefs),
      status: form.status || "pending",
      provenance_kind: form.provenanceKind.trim() || "operator",
      trust_label: form.trustLabel.trim() || "operator_reviewed",
    };
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

  async function handleProjectAssistantPropose(form: ProjectAssistantDraftForm) {
    if (!selectedProject) return;
    setAssistantStatus("proposing");
    setAssistantError("");
    setAssistantApplyResult(null);
    try {
      const proposal = await draftProjectAssistant(
        projectAssistantDraftPayload(form, selectedProject.id, selectedWorkItem?.id),
      );
      setAssistantProposal(proposal.data);
      setAssistantStatus("idle");
    } catch (error) {
      setAssistantStatus("idle");
      setAssistantError(errorMessage(error, "Failed to draft Project Assistant proposal."));
    }
  }

  async function handleProjectAssistantContext(form: ProjectAssistantDraftForm) {
    if (!selectedProject) return;
    setAssistantContextStatus("loading");
    setAssistantContextError("");
    try {
      const payload = await getProjectAssistantContext(
        projectAssistantContextPayload(form, selectedProject.id, selectedWorkItem?.id),
      );
      setAssistantContext(payload.data);
      setAssistantContextStatus("loaded");
    } catch (error) {
      setAssistantContext(null);
      setAssistantContextStatus("error");
      setAssistantContextError(errorMessage(error, "Failed to inspect Project Assistant context."));
    }
  }

  async function handleProjectAssistantApply() {
    if (!selectedProjectID || !assistantProposal) return;
    const proposal = assistantProposal;
    setAssistantStatus("applying");
    setAssistantError("");
    try {
      const payload = await applyProjectAssistant({ proposal, confirm: true });
      setAssistantApplyResult(payload.data);
      setAssistantProposal(null);
      setAssistantStatus("applied");
      await projects.actions.loadProjects();
      const preferredWorkItemID =
        projectAssistantResultWorkItemID(payload.data) || selectedWorkItemID;
      const refreshedWorkItemID = await loadWorkForProject(selectedProjectID, preferredWorkItemID);
      if (refreshedWorkItemID) {
        await loadWorkItemDetail(selectedProjectID, refreshedWorkItemID);
      }
      await loadProjectMemory(selectedProjectID);
    } catch (error) {
      setAssistantStatus("idle");
      setAssistantError(projectAssistantApplyErrorMessage(error, proposal));
      if (error instanceof ApiError && (error.status === 404 || error.status === 409)) {
        const refreshedWorkItemID = await loadWorkForProject(selectedProjectID, selectedWorkItemID);
        if (refreshedWorkItemID) {
          await loadWorkItemDetail(selectedProjectID, refreshedWorkItemID);
        }
      }
    }
  }

  function dismissProjectAssistantProposal() {
    setAssistantProposal(null);
    setAssistantApplyResult(null);
    setAssistantContext(null);
    setAssistantContextError("");
    setAssistantContextStatus("idle");
    setAssistantError("");
    setAssistantStatus("idle");
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
          onAttentionReviewCandidate={setPromotingCandidate}
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
          <section style={detailStyle} aria-label="Selected work item">
            <div className="project-cockpit-workspace" style={cockpitWorkspaceStyle}>
              <section style={domainSectionStyle} aria-label="Project workspace">
                <ProjectAssistantPanel
                  applyResult={assistantApplyResult}
                  context={assistantContext}
                  contextError={assistantContextError}
                  contextStatus={assistantContextStatus}
                  error={assistantError}
                  onApply={() => void handleProjectAssistantApply()}
                  onInspectContext={(form) => void handleProjectAssistantContext(form)}
                  onDismiss={dismissProjectAssistantProposal}
                  onPropose={(form) => void handleProjectAssistantPropose(form)}
                  project={selectedProject}
                  proposal={assistantProposal}
                  roles={roles}
                  status={assistantStatus}
                  workItem={selectedWorkItem}
                />
                <ProjectWorkspaceTabs
                  activeTab={workspaceTab}
                  memoryCandidateCount={memoryCandidates.length}
                  memoryEntryCount={memoryEntries.length}
                  onChange={setWorkspaceTab}
                  projectSkillCount={projectSkills.length}
                  workItemCount={workItems.length}
                />
                {workspaceTab === "work" && (
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
                            onClick={() => {
                              setNewWorkError("");
                              setNewWorkModalOpen(true);
                            }}
                            disabled={!selectedProject}
                          >
                            <Icon d={Icons.plus} size={13} />
                            Work
                          </button>
                        }
                      />
                      <ProjectActivityBucketTabs
                        activity={activity}
                        bucket={activityBucket}
                        onBucketChange={setActivityBucket}
                        workItemCount={workItems.length}
                      />
                    </section>
                    {workError && <InlineError message={workError} />}
                    <div
                      className="project-work-coordination-grid"
                      style={workCoordinationGridStyle}
                    >
                      <ProjectActivityInbox
                        activity={activity}
                        bucket={activityBucket}
                        loading={workLoadState === "loading"}
                        onSelectWorkItem={setSelectedWorkItemID}
                        project={selectedProject}
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
                            onRefresh={refreshSelectedWorkItem}
                            onCreateAssignmentFromHandoff={handleCreateAssignmentFromHandoff}
                            activityByAssignmentID={activityByAssignmentID}
                            onDeleteHandoff={(handoff) => void handleDeleteHandoff(handoff)}
                            onDeleteWorkItem={(item) => setDeleteWorkItem(item)}
                            onEditHandoff={(handoff) => {
                              setHandoffError("");
                              setNewHandoffDraft(null);
                              setEditingHandoff(handoff);
                            }}
                            onEditAssignment={(assignment) => {
                              setEditAssignmentError("");
                              setEditingAssignment(assignment);
                            }}
                            onEditWorkItem={(item) => {
                              setEditWorkError("");
                              setEditingWorkItem(item);
                            }}
                            onDeleteAssignment={(assignment) => setDeleteAssignment(assignment)}
                            onOpenChat={onOpenChat}
                            onStartAssignment={handleStartAssignment}
                            onStartHandoff={(handoff) => void handleStartHandoff(handoff)}
                            onSetHandoffStatus={(handoff, status) =>
                              void handleSetHandoffStatus(handoff, status)
                            }
                            project={selectedProject}
                            roleByID={roleByID}
                            startingAssignmentID={startingAssignmentID}
                            workItem={selectedWorkItem}
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
                {workspaceTab === "timeline" && (
                  <ProjectTimelinePanel
                    activity={activity}
                    artifacts={artifacts}
                    handoffs={handoffs}
                    memoryCandidates={memoryCandidates}
                    memoryEntries={memoryEntries}
                    onEditMemory={setEditingMemory}
                    onOpenChat={onOpenChat}
                    onOpenTask={onOpenTask}
                    onSelectWorkItem={setSelectedWorkItemID}
                    project={selectedProject}
                    roles={roles}
                    workItems={workItems}
                  />
                )}
                {workspaceTab === "memory" && (
                  <ProjectMemoryPanel
                    candidates={memoryCandidates}
                    discoveringContext={discoveringContext}
                    entries={memoryEntries}
                    error={memoryError}
                    loading={memoryLoadState === "loading"}
                    onDiscoverContextSources={handleDiscoverContextSources}
                    onPromoteCandidate={setPromotingCandidate}
                    onRejectCandidate={handleRejectCandidate}
                    onDelete={setDeleteMemory}
                    onEdit={setEditingMemory}
                    onNew={() => setEditingMemory("new")}
                    onRefresh={() => void loadProjectMemory(selectedProjectID)}
                    project={selectedProject}
                    rejectingCandidateID={rejectingCandidateID}
                  />
                )}
                {workspaceTab === "skills" && (
                  <ProjectSkillsPanel
                    discovering={discoveringSkills}
                    error={skillsError}
                    loading={skillsLoadState === "loading"}
                    onDiscover={handleDiscoverProjectSkills}
                    onRefresh={() => void loadProjectSkills(selectedProjectID)}
                    onUpdate={(skill, patch) => void handleUpdateProjectSkill(skill, patch)}
                    project={selectedProject}
                    skills={projectSkills}
                    updatingSkillID={updatingSkillID}
                  />
                )}
              </section>
            </div>
          </section>

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
            projectSkills={projectSkills}
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
            roles={roles}
            onClose={() => setNewWorkModalOpen(false)}
            onCreate={handleCreateWorkItem}
          />
        )}

        {selectedWorkItem && newAssignmentModalOpen && (
          <NewAssignmentModal
            error={newAssignmentError}
            pending={newAssignmentPending}
            roles={roles}
            onClose={() => setNewAssignmentModalOpen(false)}
            onCreate={handleCreateAssignment}
          />
        )}

        {editingWorkItem && (
          <EditWorkItemModal
            error={editWorkError}
            item={editingWorkItem}
            pending={editWorkPending}
            roles={roles}
            onClose={() => setEditingWorkItem(null)}
            onSave={handleUpdateWorkItem}
          />
        )}

        {editingAssignment && (
          <EditAssignmentModal
            assignment={editingAssignment}
            error={editAssignmentError}
            pending={editAssignmentPending}
            roles={roles}
            onClose={() => setEditingAssignment(null)}
            onSave={handleUpdateAssignment}
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
  onAttentionReviewCandidate,
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
  onAttentionReviewCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
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
    if (item.id.endsWith(":defaults")) {
      onAttentionDefaults();
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
    } else if (item.id.endsWith(":context")) {
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
            {attentionCount > 0 && <span style={projectAttentionCountStyle}>{attentionCount}</span>}
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

function ProjectMemoryPanel({
  candidates,
  discoveringContext,
  entries,
  error,
  loading,
  onDiscoverContextSources,
  onPromoteCandidate,
  onRejectCandidate,
  onDelete,
  onEdit,
  onNew,
  onRefresh,
  project,
  rejectingCandidateID,
}: {
  candidates: ProjectMemoryCandidateRecord[];
  discoveringContext: boolean;
  entries: ProjectMemoryRecord[];
  error: string;
  loading: boolean;
  onDiscoverContextSources: () => void;
  onPromoteCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onRejectCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onDelete: (entry: ProjectMemoryRecord) => void;
  onEdit: (entry: ProjectMemoryRecord) => void;
  onNew: () => void;
  onRefresh: () => void;
  project: ProjectRecord | null;
  rejectingCandidateID: string;
}) {
  if (!project) return null;
  const enabledCount = entries.filter((entry) => entry.enabled).length;
  const pendingCount = candidates.filter((candidate) => candidate.status === "pending").length;
  const contextSources = project.context_sources ?? [];
  return (
    <div>
      <div style={panelStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
          <div>
            <div style={sectionLabelStyle}>Memory / Context</div>
            <div style={{ ...subtleTextStyle, marginTop: 3 }}>
              {loading
                ? "Loading project memory…"
                : `${enabledCount} enabled / ${entries.length} saved entries · ${pendingCount} pending candidates`}
            </div>
          </div>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-label="Refresh project memory"
            title="Refresh"
            onClick={onRefresh}
            style={{ marginLeft: "auto" }}
          >
            <Icon d={Icons.refresh} size={12} />
          </button>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            disabled={discoveringContext}
            onClick={onDiscoverContextSources}
          >
            <Icon d={Icons.search} size={12} />
            {discoveringContext ? "Discovering…" : "Discover"}
          </button>
          <button className="btn btn-primary btn-sm" type="button" onClick={onNew}>
            <Icon d={Icons.plus} size={12} />
            Memory
          </button>
        </div>
        {error && (
          <div style={{ marginBottom: 10 }}>
            <InlineError message={error} />
          </div>
        )}
        <div style={{ display: "grid", gap: 8, marginBottom: 12 }}>
          <div style={sectionLabelStyle}>Workspace guidance</div>
          {contextSources.length === 0 ? (
            <div style={subtleTextStyle}>No context sources discovered or configured yet.</div>
          ) : (
            contextSources.map((source) => (
              <ProjectContextSourceRow key={source.id} source={source} />
            ))
          )}
        </div>
        {candidates.length > 0 && (
          <div style={{ display: "grid", gap: 8, marginBottom: 12 }}>
            <div style={sectionLabelStyle}>Candidates</div>
            {candidates.map((candidate) => (
              <ProjectMemoryCandidateRow
                key={candidate.id}
                candidate={candidate}
                pendingReject={rejectingCandidateID === candidate.id}
                onPromote={() => onPromoteCandidate(candidate)}
                onReject={() => onRejectCandidate(candidate)}
              />
            ))}
          </div>
        )}
        {entries.length === 0 && !loading ? (
          <div style={subtleTextStyle}>No project memory entries saved yet.</div>
        ) : (
          <div style={{ display: "grid", gap: 8 }}>
            {entries.map((entry) => (
              <ProjectMemoryRow
                key={entry.id}
                entry={entry}
                onDelete={() => onDelete(entry)}
                onEdit={() => onEdit(entry)}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function ProjectSkillsPanel({
  discovering,
  error,
  loading,
  onDiscover,
  onRefresh,
  onUpdate,
  project,
  skills,
  updatingSkillID,
}: {
  discovering: boolean;
  error: string;
  loading: boolean;
  onDiscover: () => void;
  onRefresh: () => void;
  onUpdate: (skill: ProjectSkillRecord, patch: UpdateProjectSkillPayload) => void;
  project: ProjectRecord | null;
  skills: ProjectSkillRecord[];
  updatingSkillID: string;
}) {
  if (!project) return null;
  const enabledCount = skills.filter((skill) => skill.enabled).length;
  const availableCount = skills.filter((skill) => skill.status === "available").length;
  return (
    <div>
      <div style={panelStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
          <div>
            <div style={sectionLabelStyle}>Project Skills</div>
            <div style={{ ...subtleTextStyle, marginTop: 3 }}>
              {loading
                ? "Loading project skills..."
                : `${enabledCount} enabled / ${availableCount} available / ${skills.length} registered`}
            </div>
          </div>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-label="Refresh project skills"
            title="Refresh"
            onClick={onRefresh}
            style={{ marginLeft: "auto" }}
          >
            <Icon d={Icons.refresh} size={12} />
          </button>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            disabled={discovering}
            onClick={onDiscover}
          >
            <Icon d={Icons.search} size={12} />
            {discovering ? "Discovering..." : "Discover"}
          </button>
        </div>
        {error && (
          <div style={{ marginBottom: 10 }}>
            <InlineError message={error} />
          </div>
        )}
        {skills.length === 0 && !loading ? (
          <EmptyBlock
            title="No project skills registered"
            detail="Discover skills from AGENTS.md / CLAUDE.md references, .agents/skills, or .hecate/skills."
          />
        ) : (
          <div style={{ display: "grid", gap: 8 }}>
            {skills.map((skill) => (
              <ProjectSkillRow
                key={skill.id}
                pending={updatingSkillID === skill.id}
                skill={skill}
                onUpdate={onUpdate}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function ProjectSkillRow({
  onUpdate,
  pending,
  skill,
}: {
  onUpdate: (skill: ProjectSkillRecord, patch: UpdateProjectSkillPayload) => void;
  pending: boolean;
  skill: ProjectSkillRecord;
}) {
  const [draft, setDraft] = useState(() => skillFormFromRecord(skill));

  useEffect(() => {
    setDraft(skillFormFromRecord(skill));
  }, [skill]);

  const changed =
    draft.title.trim() !== skill.title ||
    draft.description.trim() !== (skill.description ?? "") ||
    draft.trustLabel.trim() !== skill.trust_label;
  const statusClass = skill.status === "available" ? "badge badge-green" : "badge badge-amber";

  return (
    <div style={memoryEntryStyle}>
      <div style={{ display: "flex", alignItems: "flex-start", gap: 10 }}>
        <label
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: 6,
            paddingTop: 2,
            color: "var(--text1)",
          }}
        >
          <input
            type="checkbox"
            checked={skill.enabled}
            disabled={pending}
            aria-label={`Enable skill ${skill.title || skill.id}`}
            onChange={(event) => onUpdate(skill, { enabled: event.target.checked })}
          />
          <span className={skill.enabled ? "badge badge-green" : "badge badge-muted"}>
            {skill.enabled ? "enabled" : "disabled"}
          </span>
        </label>
        <div style={{ display: "grid", gap: 8, flex: 1, minWidth: 0 }}>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
            <CopyableID text={skill.id} compact />
            <span className={statusClass}>{skill.status}</span>
            <span className="badge badge-muted">{skill.trust_label}</span>
            <span className="badge badge-muted">{skill.format}</span>
          </div>
          <div style={{ display: "grid", gridTemplateColumns: "minmax(160px, 1fr) 1fr", gap: 8 }}>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Title</span>
              <input
                className="input"
                value={draft.title}
                disabled={pending}
                onChange={(event) =>
                  setDraft((current) => ({ ...current, title: event.target.value }))
                }
              />
            </label>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Trust label</span>
              <input
                className="input"
                value={draft.trustLabel}
                disabled={pending}
                onChange={(event) =>
                  setDraft((current) => ({ ...current, trustLabel: event.target.value }))
                }
              />
            </label>
          </div>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Description</span>
            <textarea
              className="input"
              rows={2}
              value={draft.description}
              disabled={pending}
              onChange={(event) =>
                setDraft((current) => ({ ...current, description: event.target.value }))
              }
            />
          </label>
          <div style={subtleTextStyle}>
            {skill.path}
            {skill.root_id ? ` · root ${skill.root_id}` : ""}
            {skill.source_context_source_ids?.length
              ? ` · sources ${skill.source_context_source_ids.join(", ")}`
              : ""}
            {skill.discovered_at ? ` · discovered ${formatAbsoluteTime(skill.discovered_at)}` : ""}
          </div>
          {skill.warnings?.length ? (
            <div style={{ display: "grid", gap: 3 }}>
              {skill.warnings.map((warning) => (
                <div key={warning} style={{ ...subtleTextStyle, color: "var(--amber)" }}>
                  {warning}
                </div>
              ))}
            </div>
          ) : null}
        </div>
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          disabled={pending || !changed}
          onClick={() =>
            onUpdate(skill, {
              title: draft.title.trim(),
              description: draft.description.trim(),
              trust_label: draft.trustLabel.trim(),
            })
          }
        >
          {pending ? "Saving..." : "Save"}
        </button>
      </div>
    </div>
  );
}

function ProjectContextSourceRow({ source }: { source: ProjectContextSourceRecord }) {
  const host = source.metadata?.host;
  return (
    <div style={memoryEntryStyle}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <span
          className={
            source.kind === "workspace_instruction" ? "badge badge-green" : "badge badge-muted"
          }
        >
          {source.kind}
        </span>
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{source.title || source.path}</div>
        <span className={source.enabled ? "badge badge-muted" : "badge badge-amber"}>
          {source.enabled ? "enabled" : "disabled"}
        </span>
      </div>
      <div style={metaLineStyle}>
        <span>{source.path}</span>
        {source.format && <span>{source.format}</span>}
        {source.scope && <span>{source.scope}</span>}
        {host && <span>{host}</span>}
      </div>
    </div>
  );
}

function ProjectMemoryRow({
  entry,
  onDelete,
  onEdit,
}: {
  entry: ProjectMemoryRecord;
  onDelete: () => void;
  onEdit: () => void;
}) {
  const source = formatMemorySource(entry);
  return (
    <div style={memoryEntryStyle}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <span className={entry.enabled ? "badge badge-muted" : "badge badge-amber"}>
          {entry.enabled ? "enabled" : "disabled"}
        </span>
        <span className="badge badge-muted">{entry.trust_label}</span>
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{entry.title}</div>
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          aria-label={`Edit memory ${entry.title}`}
          onClick={onEdit}
          title="Edit"
        >
          <Icon d={Icons.edit} size={12} />
        </button>
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          aria-label={`Delete memory ${entry.title}`}
          onClick={onDelete}
          title="Delete"
          style={{ color: "var(--red)" }}
        >
          <Icon d={Icons.trash} size={12} />
        </button>
      </div>
      <div style={memoryBodyStyle}>{entry.body}</div>
      <div style={metaLineStyle}>
        <span>{source}</span>
        <span>Updated {formatAbsoluteTime(entry.updated_at)}</span>
        <CopyableID text={entry.id} compact />
      </div>
    </div>
  );
}

function ProjectMemoryCandidateRow({
  candidate,
  onPromote,
  onReject,
  pendingReject,
}: {
  candidate: ProjectMemoryCandidateRecord;
  onPromote: () => void;
  onReject: () => void;
  pendingReject: boolean;
}) {
  const source = formatCandidateSource(candidate);
  const sourceRefs = formatCandidateSourceRefs(candidate);
  const pending = candidate.status === "pending";
  return (
    <div style={memoryEntryStyle}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <span className={pending ? "badge badge-amber" : "badge badge-muted"}>
          {candidate.status}
        </span>
        <span className="badge badge-muted">{candidate.suggested_trust_label}</span>
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{candidate.title}</div>
        {pending && (
          <>
            <button
              className="btn btn-primary btn-sm"
              type="button"
              aria-label={`Promote memory candidate ${candidate.title}`}
              onClick={onPromote}
              title="Promote"
            >
              <Icon d={Icons.check} size={12} />
            </button>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              aria-label={`Reject memory candidate ${candidate.title}`}
              disabled={pendingReject}
              onClick={onReject}
              title="Reject"
              style={{ color: "var(--red)" }}
            >
              <Icon d={Icons.x} size={12} />
            </button>
          </>
        )}
      </div>
      <div style={memoryBodyStyle}>{candidate.body}</div>
      <div style={metaLineStyle}>
        <span>{source}</span>
        <span>Suggested {formatAbsoluteTime(candidate.created_at)}</span>
        <CopyableID text={candidate.id} compact />
      </div>
      {sourceRefs.length > 0 && (
        <div style={{ ...subtleTextStyle, marginTop: 6 }}>
          Source refs: {sourceRefs.join(" · ")}
        </div>
      )}
    </div>
  );
}

function ProjectMemoryModal({
  candidate,
  entry,
  error,
  pending,
  onClose,
  onSave,
}: {
  candidate?: ProjectMemoryCandidateRecord | null;
  entry: ProjectMemoryRecord | null;
  error: string;
  pending: boolean;
  onClose: () => void;
  onSave: (form: MemoryForm) => void | Promise<void>;
}) {
  const [form, setForm] = useState<MemoryForm>(() =>
    candidate ? memoryFormFromCandidate(candidate) : memoryFormFromRecord(entry),
  );
  const valid = form.title.trim().length > 0 && form.body.trim().length > 0;
  const isCandidate = Boolean(candidate);
  const candidateSourceRefs = candidate ? formatCandidateSourceRefs(candidate) : [];
  return (
    <Modal
      title={
        isCandidate
          ? "Promote memory candidate"
          : entry
            ? "Edit project memory"
            : "New project memory"
      }
      onClose={onClose}
      width={620}
      footer={
        <button
          className="btn btn-primary"
          type="button"
          disabled={pending || !valid}
          onClick={() => void onSave(form)}
          style={{ width: "100%", justifyContent: "center" }}
        >
          {pending
            ? "Saving…"
            : isCandidate
              ? "Promote memory"
              : entry
                ? "Save memory"
                : "Create memory"}
        </button>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (valid) void onSave(form);
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
        {candidate && (
          <div
            style={{
              background: "var(--bg2)",
              border: "1px solid var(--border)",
              borderRadius: "var(--radius-sm)",
              display: "grid",
              gap: 6,
              padding: "9px 10px",
            }}
          >
            <div style={sectionLabelStyle}>Candidate provenance</div>
            <div style={metaLineStyle}>
              <span>{formatCandidateSource(candidate)}</span>
              <span>{candidate.suggested_trust_label}</span>
              <span>{candidate.status}</span>
            </div>
            {candidateSourceRefs.length > 0 && (
              <div style={{ ...subtleTextStyle }}>
                Source refs: {candidateSourceRefs.join(" · ")}
              </div>
            )}
          </div>
        )}
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Title</span>
          <input
            className="input"
            autoFocus
            value={form.title}
            onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))}
          />
        </label>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Body</span>
          <textarea
            className="input"
            value={form.body}
            rows={7}
            onChange={(event) => setForm((current) => ({ ...current, body: event.target.value }))}
            style={{ resize: "vertical", minHeight: 150 }}
          />
        </label>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Trust label</span>
            <select
              className="input"
              value={form.trustLabel}
              onChange={(event) =>
                setForm((current) => ({ ...current, trustLabel: event.target.value }))
              }
            >
              {MEMORY_TRUST_LABELS.map((label) => (
                <option key={label} value={label}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Source kind</span>
            <select
              className="input"
              value={form.sourceKind}
              onChange={(event) =>
                setForm((current) => ({ ...current, sourceKind: event.target.value }))
              }
            >
              {MEMORY_SOURCE_KINDS.map((kind) => (
                <option key={kind} value={kind}>
                  {kind}
                </option>
              ))}
            </select>
          </label>
        </div>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Source ID</span>
          <input
            className="input"
            value={form.sourceID}
            onChange={(event) =>
              setForm((current) => ({ ...current, sourceID: event.target.value }))
            }
            placeholder="optional artifact, chat, message, or handoff id"
          />
        </label>
        <label style={{ display: "flex", alignItems: "center", gap: 8, color: "var(--t1)" }}>
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(event) =>
              setForm((current) => ({ ...current, enabled: event.target.checked }))
            }
          />
          Enabled for project context packets
        </label>
      </form>
    </Modal>
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
  onCreateAssignmentFromHandoff,
  onDeleteAssignment,
  onDeleteHandoff,
  onDeleteWorkItem,
  onEditAssignment,
  onEditHandoff,
  onEditWorkItem,
  onOpenChat,
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
  onCreateAssignmentFromHandoff: (handoff: ProjectHandoffRecord) => void;
  onDeleteAssignment: (assignment: ProjectAssignmentRecord) => void;
  onDeleteHandoff: (handoff: ProjectHandoffRecord) => void;
  onDeleteWorkItem: (item: ProjectWorkItemRecord) => void;
  onEditAssignment: (assignment: ProjectAssignmentRecord) => void;
  onEditHandoff: (handoff: ProjectHandoffRecord) => void;
  onEditWorkItem: (item: ProjectWorkItemRecord) => void;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
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
              {assignments.map((assignment) => (
                <AssignmentRow
                  key={assignment.id}
                  activityItem={activityByAssignmentID.get(assignment.id)}
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
                      ? () =>
                          onOpenChat?.(
                            assignment.chat_session_id
                              ? {
                                  projectID: project.id,
                                  chatSessionID: assignment.chat_session_id,
                                }
                              : buildProjectAssignmentChatLaunchRequest({
                                  project,
                                  workItem,
                                  assignment,
                                  role: roleByID.get(assignment.role_id) ?? null,
                                }),
                          )
                      : undefined
                  }
                  onOpenTask={onOpenTask}
                  onStart={() => onStartAssignment(assignment)}
                  onCreateHandoff={() =>
                    onAddHandoffFromAssignment(
                      assignment,
                      activityByAssignmentID.get(assignment.id),
                    )
                  }
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
                />
              ))}
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
              {handoffs.map((handoff) => (
                <ProjectHandoffRow
                  key={handoff.id}
                  actionPending={handoffActionID === handoff.id}
                  assignment={assignments.find((item) => item.id === handoff.target_assignment_id)}
                  handoff={handoff}
                  onCreateAssignment={() => onCreateAssignmentFromHandoff(handoff)}
                  onDelete={() => onDeleteHandoff(handoff)}
                  onEdit={() => onEditHandoff(handoff)}
                  onSetStatus={(status) => onSetHandoffStatus(handoff, status)}
                  onStart={() => onStartHandoff(handoff)}
                  role={handoff.target_role_id ? roleByID.get(handoff.target_role_id) : undefined}
                  starting={startingAssignmentID === handoff.target_assignment_id}
                />
              ))}
            </div>
          )}
        </section>
      </article>
    </div>
  );
}

function ProjectSettingsPanel({
  agentProfiles,
  agentProfilesError,
  error,
  models,
  pending,
  providerOptions,
  providerPresets,
  project,
  onSave,
}: {
  agentProfiles: AgentProfileRecord[];
  agentProfilesError: string;
  error: string;
  models: ModelRecord[];
  pending: boolean;
  providerOptions: ProviderOption[];
  providerPresets: ProviderPresetRecord[];
  project: ProjectRecord;
  onSave: (form: ProjectDefaultsForm) => void | Promise<void>;
}) {
  const [form, setForm] = useState<ProjectDefaultsForm>({
    provider: project.default_provider ?? "",
    model: project.default_model ?? "",
    defaultAgentProfile: project.default_agent_profile ?? "",
    workspaceMode: project.default_workspace_mode || "in_place",
  });
  const scopedModels = useMemo(() => {
    if (!form.provider) return models;
    return models.filter((model) => model.metadata?.provider === form.provider);
  }, [form.provider, models]);
  const selectedProfile = useMemo(
    () => agentProfiles.find((profile) => profile.id === form.defaultAgentProfile) ?? null,
    [agentProfiles, form.defaultAgentProfile],
  );

  function handleProviderChange(provider: string) {
    setForm((current) => {
      const nextModels = provider
        ? models.filter((model) => model.metadata?.provider === provider)
        : models;
      const modelStillValid =
        current.model &&
        nextModels.some(
          (model) =>
            model.id === current.model && (!provider || model.metadata?.provider === provider),
        );
      return {
        ...current,
        provider,
        model: modelStillValid ? current.model : "",
      };
    });
  }
  const submitForm = () => onSave(form);

  const workspace = projectDefaultWorkspace(project);

  return (
    <div
      style={{
        background: "var(--bg1)",
        display: "flex",
        flexDirection: "column",
        flex: 1,
        minHeight: 0,
        minWidth: 0,
      }}
    >
      <div
        style={{
          borderBottom: "1px solid var(--border)",
          padding: "14px 14px 12px",
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          gap: 12,
        }}
      >
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 12, fontWeight: 650, color: "var(--t0)" }}>Project settings</div>
          <div
            style={{
              marginTop: 4,
              fontSize: 11,
              color: "var(--t3)",
              lineHeight: 1.45,
            }}
          >
            Controls defaults for future native project assignments. Existing task runs keep the
            settings they started with.
          </div>
        </div>
      </div>
      <div style={{ overflowY: "auto", padding: 14, display: "grid", gap: 14 }}>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void submitForm();
          }}
          style={{ display: "grid", gap: 14 }}
        >
          {error && <InlineError message={error} />}
          {agentProfilesError && <InlineError message={agentProfilesError} />}
          <ProjectSettingsSection title="Assignment defaults">
            <div style={{ ...subtleTextStyle, marginBottom: 12 }}>
              Native Hecate assignments copy these defaults when creating the backing task.
            </div>
            <div style={{ display: "grid", gap: 12 }}>
              <div style={fieldStyle}>
                <span style={fieldLabelStyle}>Provider and model</span>
                <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
                  <ProviderPicker
                    value={form.provider}
                    onChange={handleProviderChange}
                    options={providerOptions}
                    emptyLabel={
                      providerOptions.length === 0 ? "no providers configured" : "select provider"
                    }
                  />
                  <ModelPicker
                    value={form.model}
                    onChange={(model) => setForm((current) => ({ ...current, model }))}
                    models={scopedModels}
                    presets={providerPresets}
                    includeAll
                    allLabel="inherit runtime default"
                    showProvider={!form.provider}
                  />
                </div>
              </div>
              <div style={fieldStyle}>
                <span style={fieldLabelStyle}>Agent profile</span>
                <select
                  aria-label="Default agent profile"
                  className="input"
                  value={form.defaultAgentProfile}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      defaultAgentProfile: event.target.value,
                    }))
                  }
                  style={{ fontFamily: "var(--font-mono)", fontSize: 12, minHeight: 36 }}
                >
                  <option value="">built-in project_assignment</option>
                  {agentProfiles.map((profile) => (
                    <option key={profile.id} value={profile.id}>
                      {profile.name || profile.id} ({profile.id})
                    </option>
                  ))}
                </select>
                <ProfilePosturePreview profile={selectedProfile} />
              </div>
              <div style={fieldStyle}>
                <span style={fieldLabelStyle}>Workspace mode</span>
                <div style={{ position: "relative", width: "100%" }}>
                  <select
                    aria-label="Workspace mode"
                    className="input"
                    value={normalizeWorkspaceMode(form.workspaceMode)}
                    onChange={(event) =>
                      setForm((current) => ({ ...current, workspaceMode: event.target.value }))
                    }
                    style={{
                      appearance: "none",
                      cursor: "pointer",
                      fontFamily: "var(--font-mono)",
                      fontSize: 12,
                      minHeight: 36,
                      paddingRight: 34,
                    }}
                  >
                    <option value="in_place">in_place</option>
                    <option value="persistent">persistent</option>
                    <option value="ephemeral">ephemeral</option>
                  </select>
                  <span
                    aria-hidden="true"
                    style={{
                      alignItems: "center",
                      color: "var(--t2)",
                      display: "inline-flex",
                      height: "100%",
                      pointerEvents: "none",
                      position: "absolute",
                      right: 11,
                      top: 0,
                    }}
                  >
                    <Icon d={Icons.chevD} size={12} />
                  </span>
                </div>
              </div>
              <button
                className="btn btn-primary"
                type="submit"
                disabled={pending}
                style={{ width: "100%", justifyContent: "center" }}
              >
                {pending ? "Saving…" : "Save defaults"}
              </button>
            </div>
          </ProjectSettingsSection>
          <ProjectSettingsSection title="Project context">
            <div
              style={{
                display: "grid",
                gap: 5,
                fontSize: 11,
                color: "var(--t3)",
                lineHeight: 1.45,
              }}
            >
              <div style={{ display: "flex", gap: 8, alignItems: "baseline" }}>
                <span style={{ color: "var(--t3)", fontSize: 11, minWidth: 78 }}>Workspace</span>
                <span
                  title={workspace}
                  style={{
                    color: "var(--t1)",
                    fontFamily: "var(--font-mono)",
                    fontSize: 11,
                    wordBreak: "break-all",
                  }}
                >
                  {workspace || "No default root"}
                </span>
              </div>
            </div>
          </ProjectSettingsSection>
        </form>
      </div>
    </div>
  );
}

function ProjectSettingsSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <div className="kicker" style={{ marginBottom: 7 }}>
        {title}
      </div>
      {children}
    </section>
  );
}

function ProfilePosturePreview({ profile }: { profile: AgentProfileRecord | null }) {
  if (!profile) {
    return (
      <div style={{ ...subtleTextStyle, marginTop: 4 }}>
        Uses the built-in project_assignment posture until a saved profile is selected.
      </div>
    );
  }
  const details = [
    profile.surface,
    profile.execution_profile ? `profile ${profile.execution_profile}` : "",
    profile.provider_hint || profile.model_hint
      ? `hints ${[profile.provider_hint, profile.model_hint].filter(Boolean).join("/")}`
      : "",
    `tools ${profile.tools_enabled ? "on" : "off"}`,
    `writes ${profile.writes_allowed ? "on" : "off"}`,
    `network ${profile.network_allowed ? "on" : "off"}`,
    `approval ${profile.approval_policy}`,
    `memory ${profile.project_memory_policy}`,
    `sources ${profile.context_source_policy}`,
  ].filter(Boolean);
  return <div style={{ ...subtleTextStyle, marginTop: 4 }}>{details.join(" · ")}</div>;
}

function normalizeWorkspaceMode(value: string) {
  if (value === "persistent" || value === "ephemeral") return value;
  return "in_place";
}

function ProfilesModal({
  error,
  pending,
  profiles,
  projectSkills,
  onClose,
  onCreate,
  onDelete,
  onUpdate,
}: {
  error: string;
  pending: boolean;
  profiles: AgentProfileRecord[];
  projectSkills: ProjectSkillRecord[];
  onClose: () => void;
  onCreate: (
    form: AgentProfileForm,
  ) => AgentProfileRecord | undefined | Promise<AgentProfileRecord | undefined>;
  onDelete: (profile: AgentProfileRecord) => boolean | Promise<boolean>;
  onUpdate: (
    profileID: string,
    form: AgentProfileForm,
  ) => AgentProfileRecord | undefined | Promise<AgentProfileRecord | undefined>;
}) {
  const [selectedProfileID, setSelectedProfileID] = useState(profiles[0]?.id ?? "new");
  const selectedProfile = profiles.find((profile) => profile.id === selectedProfileID) ?? null;
  const editingNew = selectedProfileID === "new";
  const [form, setForm] = useState<AgentProfileForm>(() =>
    selectedProfile ? profileFormFromRecord(selectedProfile) : emptyAgentProfileForm(),
  );

  function selectProfile(profileID: string) {
    setSelectedProfileID(profileID);
    const profile = profiles.find((item) => item.id === profileID) ?? null;
    setForm(profile ? profileFormFromRecord(profile) : emptyAgentProfileForm());
  }

  function selectProfileRecord(profile: AgentProfileRecord) {
    setSelectedProfileID(profile.id);
    setForm(profileFormFromRecord(profile));
  }

  const canSave = form.name.trim().length > 0;
  const submit = async () => {
    if (!canSave) return;
    if (editingNew) {
      const profile = await onCreate(form);
      if (profile) selectProfileRecord(profile);
      return;
    }
    const profile = await onUpdate(selectedProfileID, form);
    if (profile) selectProfileRecord(profile);
  };

  async function deleteSelectedProfile(profile: AgentProfileRecord) {
    const deleted = await onDelete(profile);
    if (!deleted) return;
    const nextProfile = profiles.find((item) => item.id !== profile.id) ?? null;
    if (nextProfile) {
      selectProfileRecord(nextProfile);
      return;
    }
    setSelectedProfileID("new");
    setForm(emptyAgentProfileForm());
  }

  return (
    <Modal
      title="Agent profiles"
      onClose={onClose}
      width={840}
      footer={
        <div style={{ display: "flex", gap: 8, width: "100%" }}>
          {selectedProfile && !editingNew && (
            <button
              className="btn btn-ghost"
              type="button"
              disabled={pending}
              onClick={() => void deleteSelectedProfile(selectedProfile)}
              style={{ color: "var(--red)" }}
            >
              Delete profile
            </button>
          )}
          <button
            className="btn btn-primary"
            type="button"
            disabled={pending || !canSave}
            onClick={() => void submit()}
            style={{ marginLeft: "auto" }}
          >
            {pending ? "Saving..." : editingNew ? "Create profile" : "Save profile"}
          </button>
        </div>
      }
    >
      <div style={{ display: "grid", gridTemplateColumns: "220px 1fr", gap: 14, minHeight: 470 }}>
        <div
          style={{
            borderRight: "1px solid var(--border)",
            paddingRight: 10,
            display: "grid",
            alignContent: "start",
            gap: 6,
          }}
        >
          <button
            className={
              selectedProfileID === "new" ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"
            }
            type="button"
            onClick={() => selectProfile("new")}
            style={{ justifyContent: "flex-start" }}
          >
            <Icon d={Icons.plus} size={12} />
            New profile
          </button>
          {profiles.map((profile) => (
            <button
              key={profile.id}
              className={
                selectedProfileID === profile.id ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"
              }
              type="button"
              onClick={() => selectProfile(profile.id)}
              style={{ justifyContent: "flex-start", minWidth: 0 }}
            >
              <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>
                {profile.name || profile.id}
              </span>
            </button>
          ))}
        </div>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
          style={{ display: "grid", gap: 12, alignContent: "start" }}
        >
          {error && <InlineError message={error} />}
          <div style={{ display: "grid", gridTemplateColumns: "160px 1fr", gap: 10 }}>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Profile id</span>
              <input
                className="input"
                value={form.id}
                disabled={!editingNew}
                placeholder="implementation"
                onChange={(event) => setForm((current) => ({ ...current, id: event.target.value }))}
              />
            </label>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Name</span>
              <input
                className="input"
                value={form.name}
                autoFocus={editingNew}
                onChange={(event) =>
                  setForm((current) => ({ ...current, name: event.target.value }))
                }
              />
            </label>
          </div>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Description</span>
            <textarea
              className="input"
              value={form.description}
              rows={2}
              onChange={(event) =>
                setForm((current) => ({ ...current, description: event.target.value }))
              }
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Instructions</span>
            <textarea
              className="input"
              value={form.instructions}
              rows={5}
              onChange={(event) =>
                setForm((current) => ({ ...current, instructions: event.target.value }))
              }
            />
          </label>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Surface</span>
              <select
                className="input"
                value={form.surface}
                onChange={(event) =>
                  setForm((current) => ({ ...current, surface: event.target.value }))
                }
              >
                {AGENT_PROFILE_SURFACES.map((surface) => (
                  <option key={surface} value={surface}>
                    {surface}
                  </option>
                ))}
              </select>
            </label>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Execution profile</span>
              <input
                className="input"
                value={form.executionProfile}
                placeholder="implementation"
                onChange={(event) =>
                  setForm((current) => ({ ...current, executionProfile: event.target.value }))
                }
              />
            </label>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Provider hint</span>
              <input
                className="input"
                value={form.providerHint}
                placeholder="ollama"
                onChange={(event) =>
                  setForm((current) => ({ ...current, providerHint: event.target.value }))
                }
              />
            </label>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Model hint</span>
              <input
                className="input"
                value={form.modelHint}
                placeholder="qwen2.5-coder"
                onChange={(event) =>
                  setForm((current) => ({ ...current, modelHint: event.target.value }))
                }
              />
            </label>
          </div>
          <div style={{ display: "flex", gap: 12, flexWrap: "wrap" }}>
            <label style={checkboxLabelStyle}>
              <input
                type="checkbox"
                checked={form.toolsEnabled}
                onChange={(event) =>
                  setForm((current) => ({ ...current, toolsEnabled: event.target.checked }))
                }
              />
              Tools enabled
            </label>
            <label style={checkboxLabelStyle}>
              <input
                type="checkbox"
                checked={form.writesAllowed}
                onChange={(event) =>
                  setForm((current) => ({ ...current, writesAllowed: event.target.checked }))
                }
              />
              Writes allowed
            </label>
            <label style={checkboxLabelStyle}>
              <input
                type="checkbox"
                checked={form.networkAllowed}
                onChange={(event) =>
                  setForm((current) => ({ ...current, networkAllowed: event.target.checked }))
                }
              />
              Network allowed
            </label>
          </div>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Approval policy</span>
              <select
                className="input"
                value={form.approvalPolicy}
                onChange={(event) =>
                  setForm((current) => ({ ...current, approvalPolicy: event.target.value }))
                }
              >
                {AGENT_PROFILE_APPROVAL_POLICIES.map((policy) => (
                  <option key={policy} value={policy}>
                    {policy}
                  </option>
                ))}
              </select>
            </label>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Memory policy</span>
              <select
                className="input"
                value={form.projectMemoryPolicy}
                onChange={(event) =>
                  setForm((current) => ({ ...current, projectMemoryPolicy: event.target.value }))
                }
              >
                {AGENT_PROFILE_MEMORY_POLICIES.map((policy) => (
                  <option key={policy} value={policy}>
                    {policy}
                  </option>
                ))}
              </select>
            </label>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Context source policy</span>
              <select
                className="input"
                value={form.contextSourcePolicy}
                onChange={(event) =>
                  setForm((current) => ({ ...current, contextSourcePolicy: event.target.value }))
                }
              >
                {AGENT_PROFILE_CONTEXT_POLICIES.map((policy) => (
                  <option key={policy} value={policy}>
                    {policy}
                  </option>
                ))}
              </select>
            </label>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>External agent kind</span>
              <input
                className="input"
                value={form.externalAgentKind}
                placeholder="claude_code"
                onChange={(event) =>
                  setForm((current) => ({ ...current, externalAgentKind: event.target.value }))
                }
              />
            </label>
          </div>
          <SkillIDPicker
            onChange={(skillIDs) => setForm((current) => ({ ...current, skillIDs }))}
            skills={projectSkills}
            value={form.skillIDs}
          />
          <div style={subtleTextStyle}>
            Profiles set runtime posture and skill references. Skills do not grant tools, writes,
            network, or approvals.
          </div>
        </form>
      </div>
    </Modal>
  );
}

function SkillIDPicker({
  disabled = false,
  skills,
  value,
  onChange,
}: {
  disabled?: boolean;
  skills: ProjectSkillRecord[];
  value: string;
  onChange: (value: string) => void;
}) {
  const selectedIDs = uniqueSkillIDs(splitIDs(value));
  const selectedSet = new Set(selectedIDs);
  const indexedSkills = new Map(skills.map((skill) => [skill.id, skill]));
  const sortedSkills = sortProjectSkillsForPicker(skills);
  const warnings = selectedIDs.flatMap((id) => projectSkillSelectionWarnings(id, indexedSkills));

  function toggleSkill(skillID: string, checked: boolean) {
    const next = checked
      ? uniqueSkillIDs([...selectedIDs, skillID])
      : selectedIDs.filter((id) => id !== skillID);
    onChange(next.join(", "));
  }

  return (
    <div style={fieldStyle}>
      {sortedSkills.length > 0 && (
        <div style={{ display: "grid", gap: 6 }}>
          <span style={fieldLabelStyle}>Project skills</span>
          <div style={{ display: "grid", gap: 6 }}>
            {sortedSkills.map((skill) => (
              <label
                key={`${skill.id}:${skill.path}`}
                style={{
                  border: "1px solid var(--border)",
                  borderRadius: 6,
                  display: "grid",
                  gap: 4,
                  gridTemplateColumns: "auto 1fr",
                  padding: "7px 8px",
                }}
              >
                <input
                  type="checkbox"
                  checked={selectedSet.has(skill.id)}
                  disabled={disabled}
                  aria-label={`Use skill ${skill.title || skill.id}`}
                  onChange={(event) => toggleSkill(skill.id, event.target.checked)}
                  style={{ marginTop: 2 }}
                />
                <span style={{ display: "grid", gap: 4, minWidth: 0 }}>
                  <span style={{ display: "flex", gap: 6, alignItems: "center", flexWrap: "wrap" }}>
                    <span style={titleStyle}>{skill.title || skill.id}</span>
                    <span className={projectSkillBadgeClass(skill)}>{skill.status}</span>
                    {!skill.enabled && <span className="badge badge-muted">disabled</span>}
                    <span className="badge badge-muted">{skill.id}</span>
                  </span>
                  <span style={subtleTextStyle}>{skill.path}</span>
                </span>
              </label>
            ))}
          </div>
        </div>
      )}
      <label style={{ ...fieldStyle, marginTop: sortedSkills.length > 0 ? 8 : 0 }}>
        <span style={fieldLabelStyle}>Skill ids</span>
        <input
          className="input"
          value={value}
          disabled={disabled}
          placeholder="backend, qa"
          onChange={(event) => onChange(event.target.value)}
        />
      </label>
      {warnings.length > 0 && (
        <div style={{ display: "grid", gap: 3 }}>
          {warnings.map((warning) => (
            <div key={warning} style={{ ...subtleTextStyle, color: "var(--amber)" }}>
              {warning}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function RolesModal({
  agentProfiles,
  error,
  pending,
  projectSkills,
  roles,
  onClose,
  onCreate,
  onDelete,
  onUpdate,
}: {
  agentProfiles: AgentProfileRecord[];
  error: string;
  pending: boolean;
  projectSkills: ProjectSkillRecord[];
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onCreate: (
    form: RoleForm,
  ) => ProjectWorkRoleRecord | undefined | Promise<ProjectWorkRoleRecord | undefined>;
  onDelete: (role: ProjectWorkRoleRecord) => boolean | Promise<boolean>;
  onUpdate: (
    roleID: string,
    form: RoleForm,
  ) => ProjectWorkRoleRecord | undefined | Promise<ProjectWorkRoleRecord | undefined>;
}) {
  const customRoles = roles.filter((role) => !role.built_in);
  const firstEditable = customRoles[0] ?? null;
  const [selectedRoleID, setSelectedRoleID] = useState(firstEditable?.id ?? "new");
  const selectedRole = roles.find((role) => role.id === selectedRoleID) ?? null;
  const editingBuiltIn = Boolean(selectedRole?.built_in);
  const editingNew = selectedRoleID === "new";
  const [form, setForm] = useState<RoleForm>(() =>
    selectedRole ? roleFormFromRecord(selectedRole) : emptyRoleForm(),
  );

  function selectRole(roleID: string) {
    setSelectedRoleID(roleID);
    const role = roles.find((item) => item.id === roleID) ?? null;
    setForm(role ? roleFormFromRecord(role) : emptyRoleForm());
  }

  function selectRoleRecord(role: ProjectWorkRoleRecord) {
    setSelectedRoleID(role.id);
    setForm(roleFormFromRecord(role));
  }

  const canSave = form.name.trim().length > 0 && !editingBuiltIn;
  const submit = async () => {
    if (!canSave) return;
    if (editingNew) {
      const role = await onCreate(form);
      if (role) {
        selectRoleRecord(role);
      }
      return;
    }
    const role = await onUpdate(selectedRoleID, form);
    if (role) {
      selectRoleRecord(role);
    }
  };

  async function deleteSelectedRole(role: ProjectWorkRoleRecord) {
    const deleted = await onDelete(role);
    if (!deleted) return;
    const nextRole = roles.find((item) => !item.built_in && item.id !== role.id) ?? null;
    if (nextRole) {
      selectRoleRecord(nextRole);
      return;
    }
    setSelectedRoleID("new");
    setForm(emptyRoleForm());
  }

  return (
    <Modal
      title="Project roles"
      onClose={onClose}
      width={760}
      footer={
        <div style={{ display: "flex", gap: 8, width: "100%" }}>
          {selectedRole && !selectedRole.built_in && !editingNew && (
            <button
              className="btn btn-ghost"
              type="button"
              disabled={pending}
              onClick={() => void deleteSelectedRole(selectedRole)}
              style={{ color: "var(--red)" }}
            >
              Delete role
            </button>
          )}
          <button
            className="btn btn-primary"
            type="button"
            disabled={pending || !canSave}
            onClick={() => void submit()}
            style={{ marginLeft: "auto" }}
          >
            {pending ? "Saving…" : editingNew ? "Create role" : "Save role"}
          </button>
        </div>
      }
    >
      <div style={{ display: "grid", gridTemplateColumns: "220px 1fr", gap: 14, minHeight: 420 }}>
        <div
          style={{
            borderRight: "1px solid var(--border)",
            paddingRight: 10,
            display: "grid",
            alignContent: "start",
            gap: 6,
          }}
        >
          <button
            className={selectedRoleID === "new" ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"}
            type="button"
            onClick={() => selectRole("new")}
            style={{ justifyContent: "flex-start" }}
          >
            <Icon d={Icons.plus} size={12} />
            New custom role
          </button>
          {roles.map((role) => (
            <button
              key={role.id}
              className={
                selectedRoleID === role.id ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"
              }
              type="button"
              onClick={() => selectRole(role.id)}
              style={{ justifyContent: "flex-start", minWidth: 0 }}
            >
              <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{role.name}</span>
              {role.built_in && <span className="badge badge-muted">built-in</span>}
            </button>
          ))}
        </div>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
          style={{ display: "grid", gap: 12, alignContent: "start" }}
        >
          {error && <InlineError message={error} />}
          {editingBuiltIn && (
            <div style={subtleTextStyle}>
              Built-in roles are read-only. Create a custom role to override instructions or
              execution defaults for this project.
            </div>
          )}
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Name</span>
            <input
              className="input"
              value={form.name}
              disabled={editingBuiltIn}
              onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))}
              autoFocus={editingNew}
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Description</span>
            <textarea
              className="input"
              value={form.description}
              disabled={editingBuiltIn}
              rows={2}
              onChange={(event) =>
                setForm((current) => ({ ...current, description: event.target.value }))
              }
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Instructions</span>
            <textarea
              className="input"
              value={form.instructions}
              disabled={editingBuiltIn}
              rows={5}
              onChange={(event) =>
                setForm((current) => ({ ...current, instructions: event.target.value }))
              }
            />
          </label>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Default driver</span>
              <select
                className="input"
                value={form.defaultDriverKind}
                disabled={editingBuiltIn}
                onChange={(event) =>
                  setForm((current) => ({ ...current, defaultDriverKind: event.target.value }))
                }
              >
                <option value="">assignment default</option>
                <option value="hecate_task">hecate_task</option>
                <option value="external_agent">external_agent</option>
              </select>
            </label>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Default profile</span>
              <select
                className="input"
                value={form.defaultAgentProfile}
                disabled={editingBuiltIn}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    defaultAgentProfile: event.target.value,
                  }))
                }
              >
                <option value="">inherit project default</option>
                {agentProfiles.map((profile) => (
                  <option key={profile.id} value={profile.id}>
                    {profile.name || profile.id} ({profile.id})
                  </option>
                ))}
              </select>
            </label>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Default provider</span>
              <input
                className="input"
                value={form.defaultProvider}
                disabled={editingBuiltIn}
                placeholder="ollama"
                onChange={(event) =>
                  setForm((current) => ({ ...current, defaultProvider: event.target.value }))
                }
              />
            </label>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Default model</span>
              <input
                className="input"
                value={form.defaultModel}
                disabled={editingBuiltIn}
                placeholder="ministral-3:latest"
                onChange={(event) =>
                  setForm((current) => ({ ...current, defaultModel: event.target.value }))
                }
              />
            </label>
          </div>
          <SkillIDPicker
            disabled={editingBuiltIn}
            onChange={(skillIDs) => setForm((current) => ({ ...current, skillIDs }))}
            skills={projectSkills}
            value={form.skillIDs}
          />
          <div style={subtleTextStyle}>
            Role defaults are execution hints. Assignments can still override the driver, and
            project defaults remain the fallback.
          </div>
        </form>
      </div>
    </Modal>
  );
}

function NewWorkItemModal({
  error,
  pending,
  roles,
  onClose,
  onCreate,
}: {
  error: string;
  pending: boolean;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onCreate: (form: NewWorkItemForm) => void | Promise<void>;
}) {
  const [form, setForm] = useState<NewWorkItemForm>({
    title: "",
    brief: "",
    priority: "normal",
    ownerRoleID: roles.find((role) => role.id === "software_developer")?.id ?? roles[0]?.id ?? "",
  });
  const valid = form.title.trim().length > 0;
  return (
    <Modal
      title="New work item"
      onClose={onClose}
      width={560}
      footer={
        <button
          className="btn btn-primary"
          type="button"
          disabled={pending || !valid}
          onClick={() => void onCreate(form)}
          style={{ width: "100%", justifyContent: "center" }}
        >
          {pending ? "Creating…" : "Create work item"}
        </button>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (valid) void onCreate(form);
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Title</span>
          <input
            className="input"
            autoFocus
            value={form.title}
            onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))}
            placeholder="Implement project cockpit"
          />
        </label>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Brief</span>
          <textarea
            className="input"
            value={form.brief}
            onChange={(event) => setForm((current) => ({ ...current, brief: event.target.value }))}
            rows={5}
            placeholder="Describe the outcome, constraints, and handoff expectations."
            style={{ resize: "vertical", minHeight: 110 }}
          />
        </label>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Priority</span>
            <select
              className="input"
              value={form.priority}
              onChange={(event) =>
                setForm((current) => ({ ...current, priority: event.target.value }))
              }
            >
              <option value="low">low</option>
              <option value="normal">normal</option>
              <option value="high">high</option>
              <option value="urgent">urgent</option>
            </select>
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Owner role</span>
            <select
              className="input"
              value={form.ownerRoleID}
              onChange={(event) =>
                setForm((current) => ({ ...current, ownerRoleID: event.target.value }))
              }
            >
              <option value="">No owner</option>
              {roles.map((role) => (
                <option key={role.id} value={role.id}>
                  {role.name}
                </option>
              ))}
            </select>
          </label>
        </div>
      </form>
    </Modal>
  );
}

function EditWorkItemModal({
  error,
  item,
  pending,
  roles,
  onClose,
  onSave,
}: {
  error: string;
  item: ProjectWorkItemRecord;
  pending: boolean;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onSave: (form: EditWorkItemForm) => void | Promise<void>;
}) {
  const [form, setForm] = useState<EditWorkItemForm>({
    id: item.id,
    title: item.title,
    brief: item.brief ?? "",
    status: item.status,
    priority: item.priority || "normal",
    ownerRoleID: item.owner_role_id ?? "",
    reviewerRoleIDs: (item.reviewer_role_ids ?? []).join(", "),
  });
  const valid = form.title.trim().length > 0;
  return (
    <Modal
      title="Edit work item"
      onClose={onClose}
      width={560}
      footer={
        <button
          className="btn btn-primary"
          type="button"
          disabled={pending || !valid}
          onClick={() => void onSave(form)}
          style={{ width: "100%", justifyContent: "center" }}
        >
          {pending ? "Saving…" : "Save work item"}
        </button>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (valid) void onSave(form);
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Title</span>
          <input
            className="input"
            autoFocus
            value={form.title}
            onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))}
          />
        </label>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Brief</span>
          <textarea
            className="input"
            value={form.brief}
            onChange={(event) => setForm((current) => ({ ...current, brief: event.target.value }))}
            rows={5}
            style={{ resize: "vertical", minHeight: 110 }}
          />
        </label>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Status</span>
            <select
              className="input"
              value={form.status}
              onChange={(event) =>
                setForm((current) => ({ ...current, status: event.target.value }))
              }
            >
              {WORK_ITEM_STATUSES.map((status) => (
                <option key={status} value={status}>
                  {status}
                </option>
              ))}
            </select>
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Priority</span>
            <select
              className="input"
              value={form.priority}
              onChange={(event) =>
                setForm((current) => ({ ...current, priority: event.target.value }))
              }
            >
              {WORK_ITEM_PRIORITIES.map((priority) => (
                <option key={priority} value={priority}>
                  {priority}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Owner role</span>
            <select
              className="input"
              value={form.ownerRoleID}
              onChange={(event) =>
                setForm((current) => ({ ...current, ownerRoleID: event.target.value }))
              }
            >
              <option value="">No owner</option>
              {roles.map((role) => (
                <option key={role.id} value={role.id}>
                  {role.name}
                </option>
              ))}
            </select>
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Reviewer roles</span>
            <input
              className="input"
              value={form.reviewerRoleIDs}
              onChange={(event) =>
                setForm((current) => ({ ...current, reviewerRoleIDs: event.target.value }))
              }
              placeholder="reviewer_qa, architect"
            />
          </label>
        </div>
      </form>
    </Modal>
  );
}

function NewAssignmentModal({
  error,
  pending,
  roles,
  onClose,
  onCreate,
}: {
  error: string;
  pending: boolean;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onCreate: (form: NewAssignmentForm) => void | Promise<void>;
}) {
  const defaultRole = roles.find((role) => role.id === "software_developer") ?? roles[0] ?? null;
  const [form, setForm] = useState<NewAssignmentForm>({
    roleID: defaultRole?.id ?? "",
    driverKind: defaultDriverForRole(defaultRole),
  });
  const valid = form.roleID.trim().length > 0;
  return (
    <Modal
      title="Add assignment"
      onClose={onClose}
      width={520}
      footer={
        <button
          className="btn btn-primary"
          type="button"
          disabled={pending || !valid}
          onClick={() => void onCreate(form)}
          style={{ width: "100%", justifyContent: "center" }}
        >
          {pending ? "Adding…" : "Add assignment"}
        </button>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (valid) void onCreate(form);
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Role</span>
          <select
            className="input"
            autoFocus
            value={form.roleID}
            onChange={(event) => {
              const roleID = event.target.value;
              const role = roles.find((item) => item.id === roleID) ?? null;
              setForm((current) => ({
                ...current,
                roleID,
                driverKind: defaultDriverForRole(role),
              }));
            }}
          >
            {roles.map((role) => (
              <option key={role.id} value={role.id}>
                {role.name}
              </option>
            ))}
          </select>
        </label>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Driver</span>
          <select
            className="input"
            value={form.driverKind}
            onChange={(event) =>
              setForm((current) => ({ ...current, driverKind: event.target.value }))
            }
          >
            <option value="hecate_task">hecate_task</option>
            <option value="external_agent">external_agent</option>
          </select>
        </label>
        {form.driverKind === "external_agent" && (
          <div style={subtleTextStyle}>
            External assignment execution is recorded here but still starts from Chats.
          </div>
        )}
      </form>
    </Modal>
  );
}

function EditAssignmentModal({
  assignment,
  error,
  pending,
  roles,
  onClose,
  onSave,
}: {
  assignment: ProjectAssignmentRecord;
  error: string;
  pending: boolean;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onSave: (form: EditAssignmentForm) => void | Promise<void>;
}) {
  const [form, setForm] = useState<EditAssignmentForm>({
    id: assignment.id,
    roleID: assignment.role_id,
    driverKind: assignment.driver_kind || "hecate_task",
    status: assignment.status || "queued",
    taskID: assignment.task_id ?? "",
    runID: assignment.run_id ?? "",
    chatSessionID: assignment.chat_session_id ?? "",
    messageID: assignment.message_id ?? "",
    contextSnapshotID: assignment.context_snapshot_id ?? "",
  });
  const valid = form.roleID.trim().length > 0;
  return (
    <Modal
      title="Edit assignment"
      onClose={onClose}
      width={560}
      footer={
        <button
          className="btn btn-primary"
          type="button"
          disabled={pending || !valid}
          onClick={() => void onSave(form)}
          style={{ width: "100%", justifyContent: "center" }}
        >
          {pending ? "Saving…" : "Save assignment"}
        </button>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (valid) void onSave(form);
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Role</span>
            <select
              className="input"
              autoFocus
              value={form.roleID}
              onChange={(event) =>
                setForm((current) => ({ ...current, roleID: event.target.value }))
              }
            >
              {roles.map((role) => (
                <option key={role.id} value={role.id}>
                  {role.name}
                </option>
              ))}
            </select>
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Status</span>
            <select
              className="input"
              value={form.status}
              onChange={(event) =>
                setForm((current) => ({ ...current, status: event.target.value }))
              }
            >
              {ASSIGNMENT_STATUSES.map((status) => (
                <option key={status} value={status}>
                  {status}
                </option>
              ))}
            </select>
          </label>
        </div>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Driver</span>
          <select
            className="input"
            value={form.driverKind}
            onChange={(event) =>
              setForm((current) => ({ ...current, driverKind: event.target.value }))
            }
          >
            <option value="hecate_task">hecate_task</option>
            <option value="external_agent">external_agent</option>
          </select>
        </label>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Task ID</span>
            <input
              className="input"
              value={form.taskID}
              onChange={(event) =>
                setForm((current) => ({ ...current, taskID: event.target.value }))
              }
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Run ID</span>
            <input
              className="input"
              value={form.runID}
              onChange={(event) =>
                setForm((current) => ({ ...current, runID: event.target.value }))
              }
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Chat session ID</span>
            <input
              className="input"
              value={form.chatSessionID}
              onChange={(event) =>
                setForm((current) => ({ ...current, chatSessionID: event.target.value }))
              }
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Message ID</span>
            <input
              className="input"
              value={form.messageID}
              onChange={(event) =>
                setForm((current) => ({ ...current, messageID: event.target.value }))
              }
            />
          </label>
        </div>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Context snapshot ID</span>
          <input
            className="input"
            value={form.contextSnapshotID}
            onChange={(event) =>
              setForm((current) => ({ ...current, contextSnapshotID: event.target.value }))
            }
          />
        </label>
        <div style={subtleTextStyle}>
          Editing assignment metadata does not mutate or cancel linked task, run, or chat execution.
        </div>
      </form>
    </Modal>
  );
}

function ProjectHandoffModal({
  assignments,
  draft,
  error,
  handoff,
  pending,
  roles,
  onClose,
  onSave,
}: {
  assignments: ProjectAssignmentRecord[];
  draft?: HandoffForm | null;
  error: string;
  handoff: ProjectHandoffRecord | null;
  pending: boolean;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onSave: (form: HandoffForm) => void | Promise<void>;
}) {
  const [form, setForm] = useState<HandoffForm>(() => draft ?? handoffFormFromRecord(handoff));
  const valid =
    form.title.trim().length > 0 &&
    form.summary.trim().length > 0 &&
    form.recommendedNextAction.trim().length > 0;
  return (
    <Modal
      title={handoff ? "Edit handoff" : "New handoff"}
      onClose={onClose}
      width={620}
      footer={
        <button
          className="btn btn-primary"
          type="button"
          disabled={pending || !valid}
          onClick={() => void onSave(form)}
          style={{ width: "100%", justifyContent: "center" }}
        >
          {pending ? "Saving…" : "Save handoff"}
        </button>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (valid) void onSave(form);
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Title</span>
          <input
            className="input"
            autoFocus
            value={form.title}
            onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))}
            placeholder="QA review handoff"
          />
        </label>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Summary</span>
          <textarea
            className="input"
            value={form.summary}
            onChange={(event) =>
              setForm((current) => ({ ...current, summary: event.target.value }))
            }
            rows={4}
            style={{ resize: "vertical", minHeight: 90 }}
          />
        </label>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Recommended next action</span>
          <textarea
            className="input"
            value={form.recommendedNextAction}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                recommendedNextAction: event.target.value,
              }))
            }
            rows={3}
            style={{ resize: "vertical", minHeight: 76 }}
          />
        </label>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Source assignment</span>
            <select
              className="input"
              value={form.sourceAssignmentID}
              onChange={(event) =>
                setForm((current) => ({ ...current, sourceAssignmentID: event.target.value }))
              }
            >
              <option value="">No source assignment</option>
              {assignments.map((assignment) => (
                <option key={assignment.id} value={assignment.id}>
                  {shortID(assignment.id)} · {assignment.role_id}
                </option>
              ))}
            </select>
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Target role</span>
            <select
              className="input"
              value={form.targetRoleID}
              onChange={(event) =>
                setForm((current) => ({ ...current, targetRoleID: event.target.value }))
              }
            >
              <option value="">No target role</option>
              {roles.map((role) => (
                <option key={role.id} value={role.id}>
                  {role.name}
                </option>
              ))}
            </select>
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Target assignment</span>
            <select
              className="input"
              value={form.targetAssignmentID}
              onChange={(event) =>
                setForm((current) => ({ ...current, targetAssignmentID: event.target.value }))
              }
            >
              <option value="">No target assignment</option>
              {assignments.map((assignment) => (
                <option key={assignment.id} value={assignment.id}>
                  {shortID(assignment.id)} · {assignment.role_id}
                </option>
              ))}
            </select>
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Status</span>
            <select
              className="input"
              value={form.status}
              onChange={(event) =>
                setForm((current) => ({ ...current, status: event.target.value }))
              }
            >
              {HANDOFF_STATUSES.map((status) => (
                <option key={status} value={status}>
                  {status}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Source run</span>
            <input
              className="input"
              value={form.sourceRunID}
              onChange={(event) =>
                setForm((current) => ({ ...current, sourceRunID: event.target.value }))
              }
              placeholder="run_..."
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Source chat</span>
            <input
              className="input"
              value={form.sourceChatSessionID}
              onChange={(event) =>
                setForm((current) => ({ ...current, sourceChatSessionID: event.target.value }))
              }
              placeholder="chat_..."
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Source message</span>
            <input
              className="input"
              value={form.sourceMessageID}
              onChange={(event) =>
                setForm((current) => ({ ...current, sourceMessageID: event.target.value }))
              }
              placeholder="msg_..."
            />
          </label>
        </div>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Artifact IDs</span>
            <input
              className="input"
              value={form.linkedArtifactIDs}
              onChange={(event) =>
                setForm((current) => ({ ...current, linkedArtifactIDs: event.target.value }))
              }
              placeholder="art_1, art_2"
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Memory IDs</span>
            <input
              className="input"
              value={form.linkedMemoryIDs}
              onChange={(event) =>
                setForm((current) => ({ ...current, linkedMemoryIDs: event.target.value }))
              }
              placeholder="mem_1"
            />
          </label>
        </div>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Context refs</span>
          <input
            className="input"
            value={form.contextRefs}
            onChange={(event) =>
              setForm((current) => ({ ...current, contextRefs: event.target.value }))
            }
            placeholder="ctx_1, task/run/context"
          />
        </label>
      </form>
    </Modal>
  );
}

function AssignmentRow({
  activityItem,
  assignment,
  chatModel,
  error,
  loadContext,
  onCreateHandoff,
  onDelete,
  onEdit,
  onOpenChat,
  onOpenTask,
  onStart,
  role,
  starting,
}: {
  activityItem?: ProjectActivityItemRecord;
  assignment: ProjectAssignmentRecord;
  chatModel: string;
  error: string;
  loadContext?: (() => Promise<ContextPacketRecord>) | null;
  onCreateHandoff: () => void;
  onDelete: () => void;
  onEdit: () => void;
  onOpenChat?: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onStart: () => void;
  role?: ProjectWorkRoleRecord;
  starting: boolean;
}) {
  const execution = assignment.execution;
  const taskID = execution?.task_id || assignment.task_id || "";
  const runID = execution?.run_id || assignment.run_id || "";
  const chatSessionID = assignment.chat_session_id || "";
  const linkedChat = activityItem?.linked_chat;
  const projectedStatus = execution?.status || assignment.status;
  const startable =
    (assignment.driver_kind === "hecate_task" || assignment.driver_kind === "external_agent") &&
    projectedStatus === "queued";
  const external = assignment.driver_kind === "external_agent";
  const startActionLabel = external ? "Prepare chat" : "Start";
  const startingLabel = external ? "Preparing…" : "Starting…";
  const startedAt = execution?.started_at || assignment.started_at;
  const finishedAt = execution?.finished_at || assignment.completed_at;
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
            onClick={onStart}
            disabled={starting}
            title={
              external
                ? "Prepare a linked External Agent chat. The first prompt is sent from Chats."
                : "Start this assignment."
            }
          >
            <Icon d={external ? Icons.chat : Icons.send} size={12} />
            {starting ? startingLabel : startActionLabel}
          </button>
        )}
        {external && !startable && !assignment.chat_session_id && (
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
        {execution?.pending_approval_count ? (
          <span className="badge badge-amber">
            {execution.pending_approval_count} approval pending
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
        {execution?.missing && <span className="badge badge-amber">linked run missing</span>}
        {(taskID || runID || chatSessionID || assignment.context_snapshot_id) && (
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
      </div>
      {activityItem?.status_summary &&
        activityItem.status_summary !== projectedStatus &&
        activityItem.status_summary !== "linked run missing" && (
          <div style={{ ...subtleTextStyle, marginTop: 8 }}>{activityItem.status_summary}</div>
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
    </div>
  );
}

function ProjectHandoffRow({
  actionPending,
  assignment,
  handoff,
  onCreateAssignment,
  onDelete,
  onEdit,
  onSetStatus,
  onStart,
  role,
  starting,
}: {
  actionPending: boolean;
  assignment?: ProjectAssignmentRecord;
  handoff: ProjectHandoffRecord;
  onCreateAssignment: () => void;
  onDelete: () => void;
  onEdit: () => void;
  onSetStatus: (status: string) => void;
  onStart: () => void;
  role?: ProjectWorkRoleRecord;
  starting: boolean;
}) {
  const startable =
    (assignment?.driver_kind === "hecate_task" || assignment?.driver_kind === "external_agent") &&
    (assignment.execution?.status || assignment.status) === "queued";
  const canCreateAssignment = !assignment && handoff.status !== "dismissed";
  const sourceRefs = handoffSourceRefs(handoff);
  return (
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
      {sourceRefs.length > 0 && (
        <div style={{ ...subtleTextStyle, marginTop: 7 }}>
          Source refs: {sourceRefs.join(" · ")}
        </div>
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
            onClick={onStart}
            disabled={!startable || starting}
            title={startable ? "Start linked assignment" : "Linked assignment is not queued."}
          >
            <Icon d={Icons.send} size={12} />
            {starting ? "Starting…" : "Start from handoff"}
          </button>
        )}
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
      const status = assignment.execution?.status || assignment.status;
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

function projectAssistantResultWorkItemID(result: ProjectAssistantApplyResult): string {
  for (const action of result.actions) {
    const workItemID = action.data?.work_item_id;
    if (workItemID) return workItemID;
  }
  return "";
}

function projectAssistantContextPayload(
  form: ProjectAssistantDraftForm,
  projectID: string,
  workItemID?: string,
): ProjectAssistantContextPayload {
  const roleID = form.roleID === PROJECT_ASSISTANT_AUTO ? "" : form.roleID.trim();
  const driverKind = form.driverKind === PROJECT_ASSISTANT_AUTO ? "" : form.driverKind.trim();
  return {
    project_id: projectID,
    ...(workItemID ? { work_item_id: workItemID } : {}),
    request: form.request,
    ...(roleID ? { role_id: roleID } : {}),
    ...(driverKind ? { driver_kind: driverKind } : {}),
  };
}

function projectAssistantDraftPayload(
  form: ProjectAssistantDraftForm,
  projectID: string,
  workItemID?: string,
): ProjectAssistantDraftPayload {
  const payload: ProjectAssistantDraftPayload = projectAssistantContextPayload(
    form,
    projectID,
    workItemID,
  );
  if (form.draftMode !== "deterministic") {
    payload.draft_mode = form.draftMode;
  }
  return payload;
}

function projectAssistantApplyErrorMessage(
  error: unknown,
  proposal?: ProjectAssistantProposal,
): string {
  if (error instanceof ApiError) {
    const partialMessage = projectAssistantPartialApplyErrorMessage(error, proposal);
    if (partialMessage) return partialMessage;
    if (error.status === 404) {
      return "Project Assistant could not find a proposal target. The project may have changed; refresh project work and draft the proposal again.";
    }
    if (error.status === 409) {
      return "Project Assistant could not apply because the proposal is stale, conflicts with current project state, or was already applied. Refresh project work and draft it again.";
    }
  }
  return errorMessage(error, "Failed to apply Project Assistant proposal.");
}

function projectAssistantPartialApplyErrorMessage(
  error: ApiError,
  proposal?: ProjectAssistantProposal,
): string {
  const failedActionIndex = projectAssistantFailedActionIndex(error.fields.failed_action_index);
  const partialResult = projectAssistantPartialResult(error.fields.partial_result);
  if (failedActionIndex === null || !partialResult) return "";
  const appliedCount = partialResult.actions.length;
  const totalCount = proposal?.actions.length ?? Math.max(appliedCount, failedActionIndex + 1);
  return `Project Assistant applied ${appliedCount} of ${totalCount} actions, then failed at action ${failedActionIndex + 1}. Apply the same proposal again after fixing the target state to resume from the next unapplied action.`;
}

function projectAssistantFailedActionIndex(value: unknown): number | null {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 ? value : null;
}

function projectAssistantPartialResult(value: unknown): ProjectAssistantApplyResult | null {
  if (!value || typeof value !== "object") return null;
  const result = value as Partial<ProjectAssistantApplyResult>;
  if (
    typeof result.proposal_id !== "string" ||
    typeof result.applied !== "boolean" ||
    !Array.isArray(result.actions)
  ) {
    return null;
  }
  return {
    proposal_id: result.proposal_id,
    applied: result.applied,
    actions: result.actions,
  };
}

function emptyRoleForm(): RoleForm {
  return {
    id: "",
    name: "",
    description: "",
    instructions: "",
    defaultDriverKind: "",
    defaultProvider: "",
    defaultModel: "",
    defaultAgentProfile: "",
    skillIDs: "",
  };
}

function emptyAgentProfileForm(): AgentProfileForm {
  return {
    id: "",
    name: "",
    description: "",
    instructions: "",
    surface: "any",
    providerHint: "",
    modelHint: "",
    executionProfile: "",
    toolsEnabled: true,
    writesAllowed: false,
    networkAllowed: false,
    approvalPolicy: "inherit",
    projectMemoryPolicy: "inherit",
    contextSourcePolicy: "inherit",
    skillIDs: "",
    externalAgentKind: "",
  };
}

function profileFormFromRecord(profile: AgentProfileRecord): AgentProfileForm {
  return {
    id: profile.id,
    name: profile.name,
    description: profile.description ?? "",
    instructions: profile.instructions ?? "",
    surface: profile.surface || "any",
    providerHint: profile.provider_hint ?? "",
    modelHint: profile.model_hint ?? "",
    executionProfile: profile.execution_profile ?? "",
    toolsEnabled: profile.tools_enabled,
    writesAllowed: profile.writes_allowed,
    networkAllowed: profile.network_allowed,
    approvalPolicy: profile.approval_policy || "inherit",
    projectMemoryPolicy: profile.project_memory_policy || "inherit",
    contextSourcePolicy: profile.context_source_policy || "inherit",
    skillIDs: (profile.skill_ids ?? []).join(", "),
    externalAgentKind: profile.external_agent_kind ?? "",
  };
}

function profileCreatePayloadFromForm(form: AgentProfileForm): CreateAgentProfilePayload {
  const payload = profileUpdatePayloadFromForm(form) as CreateAgentProfilePayload;
  const id = form.id.trim();
  if (id) payload.id = id;
  return payload;
}

function profileUpdatePayloadFromForm(form: AgentProfileForm): UpdateAgentProfilePayload {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    instructions: form.instructions.trim(),
    surface: form.surface.trim() || "any",
    provider_hint: form.providerHint.trim(),
    model_hint: form.modelHint.trim(),
    execution_profile: form.executionProfile.trim(),
    tools_enabled: form.toolsEnabled,
    writes_allowed: form.writesAllowed,
    network_allowed: form.networkAllowed,
    approval_policy: form.approvalPolicy.trim() || "inherit",
    project_memory_policy: form.projectMemoryPolicy.trim() || "inherit",
    context_source_policy: form.contextSourcePolicy.trim() || "inherit",
    skill_ids: uniqueSkillIDs(splitIDs(form.skillIDs)),
    external_agent_kind: form.externalAgentKind.trim(),
  };
}

function roleFormFromRecord(role: ProjectWorkRoleRecord): RoleForm {
  return {
    id: role.id,
    name: role.name,
    description: role.description ?? "",
    instructions: role.instructions ?? "",
    defaultDriverKind: role.default_driver_kind ?? "",
    defaultProvider: role.default_provider ?? "",
    defaultModel: role.default_model ?? "",
    defaultAgentProfile: role.default_agent_profile ?? "",
    skillIDs: (role.skill_ids ?? []).join(", "),
  };
}

function rolePayloadFromForm(form: RoleForm) {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    instructions: form.instructions.trim(),
    default_driver_kind: form.defaultDriverKind.trim(),
    default_provider: form.defaultProvider.trim(),
    default_model: form.defaultModel.trim(),
    default_agent_profile: form.defaultAgentProfile.trim(),
    skill_ids: uniqueSkillIDs(splitIDs(form.skillIDs)),
  };
}

function skillFormFromRecord(skill: ProjectSkillRecord): SkillForm {
  return {
    title: skill.title ?? "",
    description: skill.description ?? "",
    trustLabel: skill.trust_label ?? "workspace_skill",
  };
}

function handoffFormFromRecord(handoff: ProjectHandoffRecord | null): HandoffForm {
  return {
    id: handoff?.id ?? "",
    sourceAssignmentID: handoff?.source_assignment_id ?? "",
    sourceRunID: handoff?.source_run_id ?? "",
    sourceChatSessionID: handoff?.source_chat_session_id ?? "",
    sourceMessageID: handoff?.source_message_id ?? "",
    targetRoleID: handoff?.target_role_id ?? "",
    targetAssignmentID: handoff?.target_assignment_id ?? "",
    title: handoff?.title ?? "",
    summary: handoff?.summary ?? "",
    recommendedNextAction: handoff?.recommended_next_action ?? "",
    linkedArtifactIDs: (handoff?.linked_artifact_ids ?? []).join(", "),
    linkedMemoryIDs: (handoff?.linked_memory_ids ?? []).join(", "),
    contextRefs: (handoff?.context_refs ?? []).join(", "),
    status: handoff?.status ?? "pending",
    provenanceKind: handoff?.provenance_kind ?? "operator",
    trustLabel: handoff?.trust_label ?? "operator_reviewed",
  };
}

function handoffFormFromAssignment(
  assignment: ProjectAssignmentRecord,
  role: ProjectWorkRoleRecord | null,
  activityItem?: ProjectActivityItemRecord,
): HandoffForm {
  const sourceChatSessionID = assignment.chat_session_id ?? "";
  const sourceRunID = assignment.execution?.run_id || assignment.run_id || "";
  const sourceMessageID =
    assignment.message_id ||
    activityItem?.linked_message_id ||
    activityItem?.linked_chat?.latest_message_id ||
    "";
  const contextRefs = [
    assignment.context_snapshot_id,
    assignment.execution?.task_id || assignment.task_id,
    sourceRunID,
    sourceChatSessionID,
    sourceMessageID,
  ]
    .filter(Boolean)
    .join(", ");
  return {
    id: "",
    sourceAssignmentID: assignment.id,
    sourceRunID,
    sourceChatSessionID,
    sourceMessageID,
    targetRoleID: "",
    targetAssignmentID: "",
    title: `${role?.name || assignment.role_id} handoff`,
    summary: "",
    recommendedNextAction: "",
    linkedArtifactIDs: "",
    linkedMemoryIDs: "",
    contextRefs,
    status: "pending",
    provenanceKind: "operator",
    trustLabel: "operator_reviewed",
  };
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

function memoryFormFromRecord(entry: ProjectMemoryRecord | null): MemoryForm {
  return {
    title: entry?.title ?? "",
    body: entry?.body ?? "",
    trustLabel: entry?.trust_label ?? "operator_memory",
    sourceKind: entry?.source_kind ?? "operator",
    sourceID: entry?.source_id ?? "",
    enabled: entry?.enabled ?? true,
  };
}

function memoryFormFromCandidate(candidate: ProjectMemoryCandidateRecord): MemoryForm {
  return {
    title: candidate.title,
    body: candidate.body,
    trustLabel: candidate.suggested_trust_label || "generated_summary",
    sourceKind: candidate.suggested_source_kind || "generated",
    sourceID: candidate.suggested_source_id ?? "",
    enabled: true,
  };
}

function formatMemorySource(entry: ProjectMemoryRecord): string {
  const sourceKind = entry.source_kind || "operator";
  return entry.source_id ? `${sourceKind}:${entry.source_id}` : sourceKind;
}

function formatCandidateSource(candidate: ProjectMemoryCandidateRecord): string {
  const refs = candidate.source_refs ?? [];
  if (refs.length > 0) {
    const ref = refs[0];
    const label = ref.title || ref.id || ref.kind;
    const suffix = refs.length > 1 ? ` +${refs.length - 1}` : "";
    return `${ref.kind}:${label}${suffix}`;
  }
  const sourceKind = candidate.suggested_source_kind || "generated";
  return candidate.suggested_source_id
    ? `${sourceKind}:${candidate.suggested_source_id}`
    : sourceKind;
}

function formatCandidateSourceRefs(candidate: ProjectMemoryCandidateRecord): string[] {
  return (candidate.source_refs ?? [])
    .map((ref) => {
      const label = ref.title || (ref.id ? shortID(ref.id) : ref.url || "");
      if (!label) return ref.kind;
      return `${ref.kind} ${label}`;
    })
    .filter(Boolean);
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
    `- Status: ${firstNonEmpty(assignment.execution?.status, assignment.status, "queued")}`,
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
    ["task", assignment.execution?.task_id || assignment.task_id],
    ["run", assignment.execution?.run_id || assignment.run_id],
    ["chat", assignment.chat_session_id],
    ["message", assignment.message_id],
    ["context", assignment.context_snapshot_id],
  ]);
  if (linkedIDs !== "none") {
    lines.push("", "Linked runtime ids:", `- ${linkedIDs}`);
  }
  lines.push("", "Request:", "- ");
  return lines.join("\n");
}

function firstNonEmpty(...values: Array<string | undefined | null>): string {
  for (const value of values) {
    const trimmed = value?.trim();
    if (trimmed) return trimmed;
  }
  return "";
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

function defaultDriverForRole(role: ProjectWorkRoleRecord | null): string {
  return role?.default_driver_kind || "hecate_task";
}

function shortID(id: string): string {
  if (id.length <= 12) return id;
  return id.slice(0, 10) + "...";
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

function sortProjectSkillsForPicker(skills: ProjectSkillRecord[]) {
  return skills.slice().sort((a, b) => {
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

function projectSkillBadgeClass(skill: ProjectSkillRecord) {
  if (skill.status === "available" && skill.enabled) return "badge badge-green";
  if (skill.status === "available") return "badge badge-muted";
  return "badge badge-amber";
}

function projectSkillSelectionWarnings(
  skillID: string,
  indexedSkills: Map<string, ProjectSkillRecord>,
) {
  const skill = indexedSkills.get(skillID);
  if (!skill) return [`Skill ${skillID} is not registered in this project.`];
  const warnings: string[] = [];
  if (!skill.enabled) warnings.push(`Skill ${skillID} is disabled.`);
  if (skill.status !== "available") warnings.push(`Skill ${skillID} is ${skill.status}.`);
  return warnings;
}

function projectSkillStatusRank(status: string): number {
  switch (status) {
    case "available":
      return 0;
    case "conflict":
      return 1;
    case "invalid":
      return 2;
    case "missing":
      return 3;
    default:
      return 4;
  }
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

function splitRoleIDs(value: string): string[] {
  return splitIDs(value);
}

function splitIDs(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function uniqueSkillIDs(ids: string[]): string[] {
  return Array.from(new Set(ids));
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

const fieldStyle: CSSProperties = {
  display: "grid",
  gap: 6,
};

const fieldLabelStyle: CSSProperties = {
  color: "var(--t2)",
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  textTransform: "uppercase",
};

const checkboxLabelStyle: CSSProperties = {
  alignItems: "center",
  color: "var(--t1)",
  display: "inline-flex",
  fontSize: 12,
  gap: 6,
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

const projectWorkspaceTabsStyle: CSSProperties = {
  alignItems: "center",
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: 11,
  boxSizing: "border-box",
  display: "grid",
  gap: 2,
  gridTemplateColumns: "repeat(3, minmax(0, 1fr))",
  justifySelf: "start",
  maxWidth: "min(100%, 560px)",
  minWidth: 0,
  overflow: "hidden",
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

const memoryEntryStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 8,
};

const memoryBodyStyle: CSSProperties = {
  marginTop: 6,
  color: "var(--t1)",
  fontSize: 12,
  lineHeight: 1.45,
  whiteSpace: "pre-wrap",
  overflowWrap: "anywhere",
};

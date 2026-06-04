import { useCallback, useEffect, useMemo, useState, type CSSProperties } from "react";

import { useProjects } from "../../app/state/projects";
import { useProvidersAndModels } from "../../app/state/providersAndModels";
import { useSettings } from "../../app/state/settings";
import {
  ApiError,
  createProjectAssignment,
  createProjectHandoff,
  createProjectMemory,
  createProjectWorkRole,
  createProjectWorkItem,
  deleteProjectAssignment,
  deleteProjectHandoff,
  deleteProjectMemory,
  deleteProjectWorkRole,
  deleteProjectWorkItem,
  getProjectActivity,
  getProjectAssignments,
  getProjectCollaborationArtifacts,
  getProjectHandoffs,
  getProjectMemory,
  getProjectMemoryCandidates,
  getProjectWorkItem,
  getProjectWorkItems,
  getProjectWorkRoles,
  startProjectAssignment,
  promoteProjectMemoryCandidate,
  rejectProjectMemoryCandidate,
  updateProject,
  updateProjectAssignment,
  updateProjectHandoff,
  updateProjectHandoffStatus,
  updateProjectMemory,
  updateProjectWorkRole,
  updateProjectWorkItem,
} from "../../lib/api";
import { formatAbsoluteTime } from "../../lib/format";
import { projectDefaultWorkspace } from "../../lib/project-workspace";
import { providerDisplayName } from "../../lib/provider-utils";
import type {
  ProjectAssignmentRecord,
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectMemoryCandidateRecord,
  ProjectCollaborationArtifactRecord,
  CreateProjectHandoffPayload,
  ProjectHandoffRecord,
  ProjectMemoryRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
  UpdateProjectAssignmentPayload,
  UpdateProjectPayload,
  UpdateProjectWorkItemPayload,
} from "../../types/project";
import type { ModelRecord } from "../../types/model";
import type { ProviderPresetRecord } from "../../types/provider";
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

export type ProjectAssignmentChatLaunchRequest = {
  projectID: string;
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

type ProjectActivityBucketKey = "active" | "blocked" | "completed" | "recent";

type LoadState = "idle" | "loading" | "loaded" | "error";

type ProjectTimelineItemKind =
  | "assignment"
  | "artifact"
  | "decision"
  | "handoff"
  | "memory"
  | "memory_candidate";

type ProjectTimelineItem = {
  id: string;
  kind: ProjectTimelineItemKind;
  title: string;
  summary: string;
  actor: string;
  source: string;
  timestamp: string;
  status?: string;
  workItemID?: string;
  taskID?: string;
  runID?: string;
  chatID?: string;
  memoryEntry?: ProjectMemoryRecord;
  assignment?: ProjectAssignmentRecord;
};

type ProjectHealthMetric = {
  key:
    | ProjectActivityBucketKey
    | "approvals"
    | "failed"
    | "stale"
    | "defaults"
    | "context"
    | "handoffs"
    | "memory_candidates";
  label: string;
  value: number | string;
  status: string;
  detail: string;
  bucket?: ProjectActivityBucketKey;
};

type ProjectHealthAttention = {
  id: string;
  title: string;
  detail: string;
  status: string;
  bucket?: ProjectActivityBucketKey;
  workItemID?: string;
  taskID?: string;
  runID?: string;
  candidateID?: string;
  actionLabel?: string;
};

type ProjectHealthSummary = {
  activeNow: number;
  waitingApproval: number;
  blockedOrFailed: number;
  recentCompleted: number;
  staleAssignments: number;
  missingDefaults: boolean;
  enabledMemory: number;
  savedMemory: number;
  enabledContextSources: number;
  memoryCandidates: {
    pending: number;
    promoted: number;
    rejected: number;
  };
  handoffs: {
    total: number;
    pending: number;
    accepted: number;
    superseded: number;
    dismissed: number;
  };
  attention: ProjectHealthAttention[];
};

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

type RoleForm = {
  id: string;
  name: string;
  description: string;
  instructions: string;
  defaultDriverKind: string;
  defaultProvider: string;
  defaultModel: string;
  defaultAgentProfile: string;
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

const shellStyle: CSSProperties = {
  display: "flex",
  height: "100%",
  minHeight: 0,
  background: "var(--bg0)",
};

const sidePanelStyle: CSSProperties = {
  width: 280,
  borderRight: "1px solid var(--border)",
  background: "var(--bg1)",
  display: "flex",
  flexDirection: "column",
  minHeight: 0,
  flexShrink: 0,
};

const workListStyle: CSSProperties = {
  width: 320,
  borderRight: "1px solid var(--border)",
  background: "var(--bg0)",
  display: "flex",
  flexDirection: "column",
  minHeight: 0,
  flexShrink: 0,
};

const detailStyle: CSSProperties = {
  flex: 1,
  minWidth: 0,
  minHeight: 0,
  overflow: "auto",
  background: "var(--bg0)",
};

export function ProjectsView({ onOpenChat, onOpenTask }: Props) {
  const projects = useProjects();
  const providersAndModels = useProvidersAndModels();
  const settings = useSettings();
  const [selectedProjectID, setSelectedProjectID] = useState(projects.activeProjectID);
  const [renamingProjectID, setRenamingProjectID] = useState("");
  const [renameValue, setRenameValue] = useState("");
  const [deleteProjectID, setDeleteProjectID] = useState("");
  const [deletePending, setDeletePending] = useState(false);
  const [defaultsModalOpen, setDefaultsModalOpen] = useState(false);
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
  const [activityBucket, setActivityBucket] = useState<ProjectActivityBucketKey>("blocked");
  const [roles, setRoles] = useState<ProjectWorkRoleRecord[]>([]);
  const [selectedWorkItemID, setSelectedWorkItemID] = useState("");
  const [selectedWorkItem, setSelectedWorkItem] = useState<ProjectWorkItemRecord | null>(null);
  const [assignments, setAssignments] = useState<ProjectAssignmentRecord[]>([]);
  const [artifacts, setArtifacts] = useState<ProjectCollaborationArtifactRecord[]>([]);
  const [handoffs, setHandoffs] = useState<ProjectHandoffRecord[]>([]);
  const [editingHandoff, setEditingHandoff] = useState<ProjectHandoffRecord | "new" | null>(null);
  const [handoffPending, setHandoffPending] = useState(false);
  const [handoffError, setHandoffError] = useState("");
  const [handoffActionID, setHandoffActionID] = useState("");
  const [workLoadState, setWorkLoadState] = useState<LoadState>("idle");
  const [detailLoadState, setDetailLoadState] = useState<LoadState>("idle");
  const [workError, setWorkError] = useState("");
  const [detailError, setDetailError] = useState("");
  const [assignmentErrors, setAssignmentErrors] = useState<Record<string, string>>({});
  const [startingAssignmentID, setStartingAssignmentID] = useState("");
  const [memoryEntries, setMemoryEntries] = useState<ProjectMemoryRecord[]>([]);
  const [memoryCandidates, setMemoryCandidates] = useState<ProjectMemoryCandidateRecord[]>([]);
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
  const pendingDeleteProject =
    projects.state.projects.find((project) => project.id === deleteProjectID) ?? null;
  const roleByID = useMemo(() => new Map(roles.map((role) => [role.id, role])), [roles]);
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
    if (!selectedProjectID || !selectedWorkItemID) return;
    void loadWorkItemDetail(selectedProjectID, selectedWorkItemID);
  }, [loadWorkItemDetail, selectedProjectID, selectedWorkItemID]);

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
      default_workspace_mode: form.workspaceMode.trim(),
    };
    try {
      const payload = await updateProject(selectedProject.id, patch);
      projects.actions.setProjects((current) => upsertProject(current, payload.data));
      setDefaultsModalOpen(false);
    } catch (error) {
      setDefaultsError(errorMessage(error, "Failed to update project defaults."));
    } finally {
      setDefaultsPending(false);
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
    setHandoffActionID(handoff.id);
    setHandoffError("");
    try {
      const assignment = await createProjectAssignment(selectedProjectID, targetWorkItemID, {
        role_id: roleID,
        driver_kind: "hecate_task",
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
    setStartingAssignmentID(assignment.id);
    setAssignmentErrors((current) => ({ ...current, [assignment.id]: "" }));
    try {
      const res = await startProjectAssignment(selectedProjectID, workItemID, assignment.id);
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
      setStartingAssignmentID("");
    }
  }

  return (
    <div style={shellStyle}>
      <section style={sidePanelStyle} aria-label="Projects">
        <div style={topbarStyle}>
          <div>
            <div style={sectionLabelStyle}>Projects</div>
            <div style={subtleTextStyle}>{projects.state.projects.length} records</div>
          </div>
          <button
            className="btn btn-primary btn-sm"
            type="button"
            onClick={() => void projects.actions.createProjectFromFolder()}
          >
            <Icon d={Icons.folder} size={13} />
            Add
          </button>
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

      <section style={workListStyle} aria-label="Project work items">
        <ProjectHeader
          project={selectedProject}
          onRefresh={refreshSelectedWorkItem}
          onEditDefaults={() => {
            setDefaultsError("");
            setDefaultsModalOpen(true);
          }}
          onManageRoles={() => {
            setRolesError("");
            setRolesModalOpen(true);
          }}
          onNewWorkItem={() => {
            setNewWorkError("");
            setNewWorkModalOpen(true);
          }}
        />
        {workError && (
          <div style={{ padding: 10 }}>
            <InlineError message={workError} />
          </div>
        )}
        <div style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
          {!selectedProject && (
            <EmptyBlock
              title="Select a project"
              detail="Project work appears after opening a project."
            />
          )}
          {selectedProject && workLoadState === "loading" && workItems.length === 0 && (
            <EmptyBlock
              title="Loading work…"
              detail="Reading roles, work items, and assignments."
            />
          )}
          {selectedProject &&
            workLoadState !== "loading" &&
            workItems.length === 0 &&
            !workError && (
              <EmptyBlock
                title="No work items"
                detail="Project work coordination is empty for this project."
              />
            )}
          {workItems.map((item) => (
            <WorkItemRow
              key={item.id}
              active={item.id === selectedWorkItemID}
              item={item}
              summary={workItemSummaries[item.id]}
              role={item.owner_role_id ? roleByID.get(item.owner_role_id) : undefined}
              onSelect={() => setSelectedWorkItemID(item.id)}
            />
          ))}
        </div>
      </section>

      <section style={detailStyle} aria-label="Selected work item">
        <ProjectHealthPanel
          activity={activity}
          bucket={activityBucket}
          loading={workLoadState === "loading"}
          memoryCandidates={memoryCandidates}
          memoryEntries={memoryEntries}
          onBucketChange={setActivityBucket}
          onEditDefaults={() => {
            setDefaultsError("");
            setDefaultsModalOpen(true);
          }}
          onNewMemory={() => setEditingMemory("new")}
          onOpenTask={onOpenTask}
          onReviewCandidate={setPromotingCandidate}
          onSelectWorkItem={setSelectedWorkItemID}
          project={selectedProject}
          workItems={workItems}
        />
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
        <ProjectActivityInbox
          activity={activity}
          bucket={activityBucket}
          loading={workLoadState === "loading"}
          onOpenChat={onOpenChat}
          onOpenTask={onOpenTask}
          onBucketChange={setActivityBucket}
          onSelectWorkItem={setSelectedWorkItemID}
          onStartAssignment={(assignment, workItemID) =>
            void handleStartAssignment(assignment, workItemID)
          }
          project={selectedProject}
          startingAssignmentID={startingAssignmentID}
          workItems={workItems}
        />
        <ProjectMemoryPanel
          candidates={memoryCandidates}
          entries={memoryEntries}
          error={memoryError}
          loading={memoryLoadState === "loading"}
          onPromoteCandidate={setPromotingCandidate}
          onRejectCandidate={handleRejectCandidate}
          onDelete={setDeleteMemory}
          onEdit={setEditingMemory}
          onNew={() => setEditingMemory("new")}
          onRefresh={() => void loadProjectMemory(selectedProjectID)}
          project={selectedProject}
          rejectingCandidateID={rejectingCandidateID}
        />
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
          onDeleteHandoff={(handoff) => void handleDeleteHandoff(handoff)}
          onDeleteWorkItem={(item) => setDeleteWorkItem(item)}
          onEditHandoff={(handoff) => {
            setHandoffError("");
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
          onSetHandoffStatus={(handoff, status) => void handleSetHandoffStatus(handoff, status)}
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
            setEditingHandoff("new");
          }}
        />
      </section>

      {selectedProject && defaultsModalOpen && (
        <ProjectDefaultsModal
          error={defaultsError}
          models={providersAndModels.state.models}
          pending={defaultsPending}
          providerOptions={providerOptions}
          providerPresets={providerPresets}
          project={selectedProject}
          onClose={() => setDefaultsModalOpen(false)}
          onSave={handleSaveProjectDefaults}
        />
      )}

      {selectedProject && rolesModalOpen && (
        <RolesModal
          error={rolesError}
          pending={rolesPending}
          roles={roles}
          onClose={() => setRolesModalOpen(false)}
          onCreate={handleCreateRole}
          onDelete={handleDeleteRole}
          onUpdate={handleUpdateRole}
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
          error={handoffError}
          pending={handoffPending}
          roles={roles}
          onClose={() => setEditingHandoff(null)}
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
              Delete <strong>{deleteMemory.title}</strong>. Historical context packets that already
              captured this memory stay unchanged.
            </>
          }
        />
      )}
    </div>
  );
}

function ProjectIndexRow({
  active,
  project,
  renaming,
  renameValue,
  onRenameChange,
  onRenameCancel,
  onRenameCommit,
  onRenameStart,
  onDelete,
  onOpen,
}: {
  active: boolean;
  project: ProjectRecord;
  renaming: boolean;
  renameValue: string;
  onRenameChange: (value: string) => void;
  onRenameCancel: () => void;
  onRenameCommit: () => void;
  onRenameStart: () => void;
  onDelete: () => void;
  onOpen: () => void;
}) {
  const workspace = projectDefaultWorkspace(project) || "No default root";
  const defaults =
    project.default_provider || project.default_model
      ? [project.default_provider, project.default_model].filter(Boolean).join(" / ")
      : "No default model";
  return (
    <div
      role="button"
      tabIndex={0}
      aria-current={active ? "true" : undefined}
      aria-label={`Open project ${project.name}`}
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.target !== event.currentTarget) return;
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        onOpen();
      }}
      style={{
        padding: "10px 12px",
        borderBottom: "1px solid var(--border)",
        borderLeft: active ? "2px solid var(--teal)" : "2px solid transparent",
        background: active ? "var(--bg2)" : "transparent",
        cursor: "pointer",
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
          <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
            <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{project.name}</div>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              aria-label={`Rename project ${project.name}`}
              title="Rename"
              onClick={(event) => {
                event.stopPropagation();
                onRenameStart();
              }}
            >
              <Icon d={Icons.edit} size={12} />
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
              style={{ color: "var(--red)" }}
            >
              <Icon d={Icons.trash} size={12} />
            </button>
          </div>
          <div style={pathTextStyle} title={workspace}>
            {workspace}
          </div>
          <div style={metaLineStyle}>
            <span>{defaults}</span>
            <span>
              {project.last_opened_at
                ? `Opened ${formatAbsoluteTime(project.last_opened_at)}`
                : `Updated ${formatAbsoluteTime(project.updated_at)}`}
            </span>
          </div>
        </>
      )}
    </div>
  );
}

function ProjectHeader({
  project,
  onEditDefaults,
  onManageRoles,
  onNewWorkItem,
  onRefresh,
}: {
  project: ProjectRecord | null;
  onEditDefaults: () => void;
  onManageRoles: () => void;
  onNewWorkItem: () => void;
  onRefresh: () => void;
}) {
  const workspace = project ? projectDefaultWorkspace(project) : "";
  return (
    <div style={{ ...topbarStyle, minHeight: 68 }}>
      <div style={{ minWidth: 0 }}>
        <div style={sectionLabelStyle}>Cockpit</div>
        <div style={{ ...titleStyle, fontSize: 14 }}>{project?.name ?? "No project selected"}</div>
        {project && (
          <div style={pathTextStyle} title={workspace || "No default root"}>
            {workspace || "No default root"}{" "}
            {project.default_model ? `· ${project.default_model}` : ""}
          </div>
        )}
      </div>
      <button
        className="btn btn-ghost btn-sm"
        type="button"
        onClick={onEditDefaults}
        disabled={!project}
      >
        Defaults
      </button>
      <button
        className="btn btn-ghost btn-sm"
        type="button"
        onClick={onManageRoles}
        disabled={!project}
      >
        Roles
      </button>
      <button
        className="btn btn-primary btn-sm"
        type="button"
        onClick={onNewWorkItem}
        disabled={!project}
      >
        <Icon d={Icons.plus} size={13} />
        Work
      </button>
      <button
        className="btn btn-ghost btn-sm"
        type="button"
        aria-label="Refresh project work"
        title="Refresh"
        onClick={onRefresh}
        disabled={!project}
      >
        <Icon d={Icons.refresh} size={13} />
      </button>
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
        background: active ? "var(--bg1)" : "transparent",
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

function ProjectHealthPanel({
  activity,
  bucket,
  loading,
  memoryCandidates,
  memoryEntries,
  onBucketChange,
  onEditDefaults,
  onNewMemory,
  onOpenTask,
  onReviewCandidate,
  onSelectWorkItem,
  project,
  workItems,
}: {
  activity: ProjectActivityData | null;
  bucket: ProjectActivityBucketKey;
  loading: boolean;
  memoryCandidates: ProjectMemoryCandidateRecord[];
  memoryEntries: ProjectMemoryRecord[];
  onBucketChange: (bucket: ProjectActivityBucketKey) => void;
  onEditDefaults: () => void;
  onNewMemory: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onReviewCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onSelectWorkItem: (workItemID: string) => void;
  project: ProjectRecord | null;
  workItems: ProjectWorkItemRecord[];
}) {
  const health = useMemo(
    () => buildProjectHealthSummary(project, activity, workItems, memoryEntries, memoryCandidates),
    [activity, memoryCandidates, memoryEntries, project, workItems],
  );
  if (!project) return null;

  const metrics = projectHealthMetrics(health);
  const contextDetail =
    health.enabledMemory > 0 || health.enabledContextSources > 0
      ? `${health.enabledMemory} memory / ${health.enabledContextSources} source`
      : "No enabled memory or context sources";
  const candidateDetail =
    health.memoryCandidates.pending > 0
      ? `${health.memoryCandidates.pending} pending review`
      : `${health.memoryCandidates.promoted} promoted, ${health.memoryCandidates.rejected} rejected`;

  return (
    <section style={{ padding: "16px 16px 0" }} aria-label="Project health">
      <div style={panelStyle}>
        <div
          style={{
            display: "flex",
            alignItems: "flex-start",
            gap: 12,
            marginBottom: 12,
          }}
        >
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={sectionLabelStyle}>Project Health</div>
            <div style={{ ...titleStyle, fontSize: 14, marginTop: 3 }}>
              What needs attention now
            </div>
            <div style={{ ...subtleTextStyle, marginTop: 4 }}>
              {loading && !activity
                ? "Loading project status…"
                : `${health.activeNow} active, ${health.waitingApproval} waiting, ${health.blockedOrFailed} blocked or failed`}
            </div>
          </div>
          <button className="btn btn-ghost btn-sm" type="button" onClick={onEditDefaults}>
            <Icon d={Icons.settings} size={12} />
            Edit defaults
          </button>
          <button className="btn btn-ghost btn-sm" type="button" onClick={onNewMemory}>
            <Icon d={Icons.plus} size={12} />
            Add memory
          </button>
        </div>

        <div style={healthMetricGridStyle}>
          {metrics.map((metric) => (
            <button
              key={metric.key}
              className="btn btn-ghost"
              type="button"
              aria-pressed={metric.bucket ? bucket === metric.bucket : undefined}
              aria-label={
                metric.bucket
                  ? `Show ${metric.label.toLowerCase()} assignments`
                  : `Project ${metric.label.toLowerCase()} status`
              }
              onClick={() => {
                if (metric.bucket) onBucketChange(metric.bucket);
                if (metric.key === "defaults") onEditDefaults();
                if (metric.key === "context") onNewMemory();
              }}
              style={healthMetricStyle}
            >
              <Badge status={metric.status} label={String(metric.value)} />
              <span style={{ ...titleStyle, whiteSpace: "normal" }}>{metric.label}</span>
              <span style={subtleTextStyle}>{metric.detail}</span>
            </button>
          ))}
        </div>

        <div style={{ display: "grid", gridTemplateColumns: "1.4fr 1fr", gap: 12 }}>
          <div style={healthColumnStyle}>
            <div style={sectionLabelStyle}>Needs Attention</div>
            {health.attention.length === 0 ? (
              <div style={{ ...subtleTextStyle, marginTop: 8 }}>
                No approvals, pending handoffs, memory reviews, failures, stale assignments, or
                missing launch defaults detected.
              </div>
            ) : (
              <div style={{ display: "grid", gap: 8, marginTop: 8 }}>
                {health.attention.map((item) => (
                  <ProjectHealthAttentionRow
                    key={item.id}
                    item={item}
                    onBucketChange={onBucketChange}
                    onOpenTask={onOpenTask}
                    onReviewCandidate={onReviewCandidate}
                    onSelectWorkItem={onSelectWorkItem}
                    reviewCandidate={memoryCandidates.find(
                      (candidate) => candidate.id === item.candidateID,
                    )}
                  />
                ))}
              </div>
            )}
          </div>
          <div style={healthColumnStyle}>
            <div style={sectionLabelStyle}>Memory / Context</div>
            <div style={{ display: "grid", gap: 8, marginTop: 8 }}>
              <div style={healthContextLineStyle}>
                <span style={titleStyle}>Project memory</span>
                <span
                  className={health.enabledMemory > 0 ? "badge badge-muted" : "badge badge-amber"}
                >
                  {health.enabledMemory} enabled
                </span>
              </div>
              <div style={healthContextLineStyle}>
                <span style={titleStyle}>Context sources</span>
                <span
                  className={
                    health.enabledContextSources > 0 ? "badge badge-muted" : "badge badge-amber"
                  }
                >
                  {health.enabledContextSources} enabled
                </span>
              </div>
              <div style={healthContextLineStyle}>
                <span style={titleStyle}>Memory candidates</span>
                <span
                  className={
                    health.memoryCandidates.pending > 0 ? "badge badge-amber" : "badge badge-muted"
                  }
                >
                  {health.memoryCandidates.pending} pending
                </span>
              </div>
              <div style={subtleTextStyle}>{contextDetail}</div>
              <div style={subtleTextStyle}>{candidateDetail}</div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

function ProjectHealthAttentionRow({
  item,
  onBucketChange,
  onOpenTask,
  onReviewCandidate,
  onSelectWorkItem,
  reviewCandidate,
}: {
  item: ProjectHealthAttention;
  onBucketChange: (bucket: ProjectActivityBucketKey) => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onReviewCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onSelectWorkItem: (workItemID: string) => void;
  reviewCandidate?: ProjectMemoryCandidateRecord;
}) {
  return (
    <div style={healthAttentionStyle}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <Badge status={item.status} label={activitySignalLabel(item.status)} />
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{item.title}</div>
        {item.bucket && (
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={() => onBucketChange(item.bucket!)}
          >
            {item.actionLabel ?? "Inbox"}
          </button>
        )}
        {item.workItemID && (
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-label="Open attention details"
            onClick={() => onSelectWorkItem(item.workItemID!)}
          >
            Details
          </button>
        )}
        {item.taskID && (
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-label="Open attention task"
            onClick={() => onOpenTask?.(item.taskID!, item.runID)}
            disabled={!onOpenTask}
          >
            <Icon d={Icons.tasks} size={12} />
            Task
          </button>
        )}
        {reviewCandidate && (
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-label="Review memory candidate"
            onClick={() => onReviewCandidate(reviewCandidate)}
          >
            Review candidate
          </button>
        )}
      </div>
      <div style={{ ...subtleTextStyle, marginTop: 6 }}>{item.detail}</div>
    </div>
  );
}

function ProjectActivityInbox({
  activity,
  bucket,
  loading,
  onOpenChat,
  onOpenTask,
  onBucketChange,
  onSelectWorkItem,
  onStartAssignment,
  project,
  startingAssignmentID,
  workItems,
}: {
  activity: ProjectActivityData | null;
  bucket: ProjectActivityBucketKey;
  loading: boolean;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onBucketChange: (bucket: ProjectActivityBucketKey) => void;
  onSelectWorkItem: (workItemID: string) => void;
  onStartAssignment: (assignment: ProjectAssignmentRecord, workItemID: string) => void;
  project: ProjectRecord | null;
  startingAssignmentID: string;
  workItems: ProjectWorkItemRecord[];
}) {
  const counts = activity?.summary;
  const buckets = activity?.buckets;
  const selectedItems = buckets?.[bucket] ?? [];
  const tabs: Array<{ id: ProjectActivityBucketKey; label: string; count: number }> = [
    { id: "blocked", label: "Blocked", count: counts?.blocked_count ?? 0 },
    { id: "active", label: "Active", count: counts?.active_count ?? 0 },
    { id: "completed", label: "Completed", count: counts?.completed_count ?? 0 },
    { id: "recent", label: "Recent", count: counts?.recent_count ?? 0 },
  ];
  const selectedTotal = tabs.find((tab) => tab.id === bucket)?.count ?? selectedItems.length;

  if (!project) {
    return null;
  }

  return (
    <div style={{ padding: "16px 16px 0", display: "grid", gap: 10 }}>
      <div style={panelStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 10 }}>
          <div>
            <div style={sectionLabelStyle}>Activity Inbox</div>
            <div style={{ ...subtleTextStyle, marginTop: 3 }}>
              {loading && !activity
                ? "Loading project activity…"
                : `${counts?.assignment_count ?? 0} assignments across ${counts?.work_item_count ?? 0} work items; newest 20 per bucket`}
            </div>
          </div>
          <div style={{ marginLeft: "auto", display: "flex", gap: 6, flexWrap: "wrap" }}>
            {tabs.map((tab) => (
              <button
                key={tab.id}
                className={bucket === tab.id ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"}
                type="button"
                onClick={() => onBucketChange(tab.id)}
              >
                {tab.label}
                <span className="badge badge-muted">{tab.count}</span>
              </button>
            ))}
          </div>
        </div>
        {!activity && !loading && (
          <div style={subtleTextStyle}>No activity is recorded for this project yet.</div>
        )}
        {activity && selectedItems.length === 0 && (
          <div style={subtleTextStyle}>No {bucket} assignments for this project.</div>
        )}
        {selectedItems.length > 0 && (
          <div style={{ display: "grid", gap: 8 }}>
            {selectedTotal > selectedItems.length && (
              <div style={subtleTextStyle}>
                Showing {selectedItems.length} of {selectedTotal} {bucket} assignments.
              </div>
            )}
            {selectedItems.map((item) => {
              const workItem =
                workItems.find((candidate) => candidate.id === item.work_item.id) ??
                projectActivityWorkItemToWorkItem(project.id, item.work_item);
              const chatModel =
                item.assignment.execution?.model ||
                item.role.default_model ||
                project.default_model ||
                "";
              return (
                <ProjectActivityRow
                  key={item.id}
                  chatModel={chatModel}
                  item={item}
                  onOpenChat={
                    project
                      ? () =>
                          onOpenChat?.(
                            buildProjectAssignmentChatLaunchRequest({
                              project,
                              workItem,
                              assignment: item.assignment,
                              role: item.role,
                            }),
                          )
                      : undefined
                  }
                  onOpenTask={onOpenTask}
                  onSelectWorkItem={() => onSelectWorkItem(item.work_item.id)}
                  onStart={() => onStartAssignment(item.assignment, item.work_item.id)}
                  starting={startingAssignmentID === item.assignment.id}
                />
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
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
    <div style={{ padding: "16px 16px 0" }}>
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
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <span className={timelineBadgeClass(item)}>{timelineKindLabel(item.kind)}</span>
        {item.status && <Badge status={item.status} label={activitySignalLabel(item.status)} />}
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{item.title}</div>
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

function ProjectActivityRow({
  chatModel,
  item,
  onOpenChat,
  onOpenTask,
  onSelectWorkItem,
  onStart,
  starting,
}: {
  chatModel: string;
  item: ProjectActivityItemRecord;
  onOpenChat?: () => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onSelectWorkItem: () => void;
  onStart: () => void;
  starting: boolean;
}) {
  const signal = item.blocking_signal;
  const taskID = item.linked_task_id || item.assignment.task_id || "";
  const runID = item.linked_run_id || item.assignment.run_id || "";
  const startable = item.assignment.driver_kind === "hecate_task" && signal === "not_started";
  const handoffCount = item.handoff_summary?.count ?? 0;
  return (
    <div style={activityRowStyle}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <Badge status={signal} label={activitySignalLabel(signal)} />
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>
          {item.work_item.title}
          <span style={{ color: "var(--t2)", fontWeight: 400 }}>
            {" "}
            / {item.role.name || item.assignment.role_id}
          </span>
        </div>
        <button className="btn btn-ghost btn-sm" type="button" onClick={onSelectWorkItem}>
          Details
        </button>
        {taskID && (
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            onClick={() => onOpenTask?.(taskID, runID)}
            disabled={!onOpenTask}
          >
            <Icon d={Icons.tasks} size={12} />
            Task
          </button>
        )}
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          onClick={onOpenChat}
          disabled={!onOpenChat || !chatModel}
          title={
            chatModel ? `Open chat with ${chatModel}` : "Set project defaults before opening chat."
          }
        >
          <Icon d={Icons.chat} size={12} />
          Chat
        </button>
        {startable && (
          <button
            className="btn btn-primary btn-sm"
            type="button"
            onClick={onStart}
            disabled={starting}
          >
            <Icon d={Icons.send} size={12} />
            {starting ? "Starting…" : "Start"}
          </button>
        )}
      </div>
      <div style={{ ...metaLineStyle, marginTop: 7 }}>
        <span>{item.status_summary}</span>
        <span>{item.assignment.driver_kind}</span>
        {taskID && <span>task {shortID(taskID)}</span>}
        {runID && <span>run {shortID(runID)}</span>}
        {item.linked_chat_id && <span>chat {shortID(item.linked_chat_id)}</span>}
        {item.artifact_summary.count > 0 && (
          <span>
            {item.artifact_summary.count} artifact
            {item.artifact_summary.count === 1 ? "" : "s"}
          </span>
        )}
        {handoffCount > 0 && (
          <span>
            {handoffCount} handoff
            {handoffCount === 1 ? "" : "s"}
          </span>
        )}
        {item.updated_at && <span>Updated {formatAbsoluteTime(item.updated_at)}</span>}
      </div>
      {item.recent_artifacts && item.recent_artifacts.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginTop: 7 }}>
          {item.recent_artifacts.map((artifact) => (
            <span key={artifact.id} className="badge badge-muted">
              {artifact.kind}: {artifact.title || artifact.id}
            </span>
          ))}
        </div>
      )}
      {item.recent_handoffs && item.recent_handoffs.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginTop: 7 }}>
          {item.recent_handoffs.map((handoff) => (
            <span key={handoff.id} className="badge badge-muted">
              {handoff.status}: {handoff.title || handoff.id}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function ProjectMemoryPanel({
  candidates,
  entries,
  error,
  loading,
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
  entries: ProjectMemoryRecord[];
  error: string;
  loading: boolean;
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
  return (
    <div style={{ padding: "12px 16px 0" }}>
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
        <div style={{ ...subtleTextStyle, marginBottom: entries.length > 0 ? 10 : 0 }}>
          Enabled entries appear in chat context packets with their trust label. Standalone native
          task context still resolves only when linked to a Hecate Chat packet.
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
    <div style={{ padding: 16, display: "grid", gap: 16 }}>
      <div style={{ display: "flex", alignItems: "flex-start", gap: 12 }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={sectionLabelStyle}>Work item</div>
          <h2 style={{ margin: "4px 0 8px", fontSize: 18, color: "var(--t0)" }}>
            {workItem.title}
          </h2>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
            <Badge status={workItem.status} label={workStatusLabel(workItem.status)} />
            <span className="badge badge-muted">{workItem.priority}</span>
            <span className="badge badge-muted">
              Updated {formatAbsoluteTime(workItem.updated_at)}
            </span>
          </div>
        </div>
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
      {detailError && <InlineError message={detailError} />}
      <div style={panelStyle}>
        <div style={sectionLabelStyle}>Brief</div>
        <p style={{ margin: "8px 0 0", fontSize: 13, color: "var(--t1)", lineHeight: 1.55 }}>
          {workItem.brief || "No brief recorded."}
        </p>
        <div style={{ ...metaLineStyle, marginTop: 10 }}>
          <span>Created {formatAbsoluteTime(workItem.created_at)}</span>
          {workItem.owner_role_id && (
            <span>
              Owner {roleByID.get(workItem.owner_role_id)?.name ?? workItem.owner_role_id}
            </span>
          )}
          {workItem.reviewer_role_ids && workItem.reviewer_role_ids.length > 0 && (
            <span>
              {workItem.reviewer_role_ids.length} reviewer role
              {workItem.reviewer_role_ids.length === 1 ? "" : "s"}
            </span>
          )}
        </div>
      </div>
      <div style={panelStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
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
          <div style={subtleTextStyle}>No assignments recorded for this work item.</div>
        ) : (
          <div style={{ display: "grid", gap: 10 }}>
            {assignments.map((assignment) => (
              <AssignmentRow
                key={assignment.id}
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
                          buildProjectAssignmentChatLaunchRequest({
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
                role={roleByID.get(assignment.role_id)}
                starting={startingAssignmentID === assignment.id}
              />
            ))}
          </div>
        )}
      </div>
      <div style={panelStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
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
      </div>
      <div style={panelStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
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
          <div style={subtleTextStyle}>No structured handoffs recorded for this work item.</div>
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
      </div>
    </div>
  );
}

function ProjectDefaultsModal({
  error,
  models,
  pending,
  providerOptions,
  providerPresets,
  project,
  onClose,
  onSave,
}: {
  error: string;
  models: ModelRecord[];
  pending: boolean;
  providerOptions: ProviderOption[];
  providerPresets: ProviderPresetRecord[];
  project: ProjectRecord;
  onClose: () => void;
  onSave: (form: ProjectDefaultsForm) => void | Promise<void>;
}) {
  const [form, setForm] = useState<ProjectDefaultsForm>({
    provider: project.default_provider ?? "",
    model: project.default_model ?? "",
    workspaceMode: project.default_workspace_mode || "in_place",
  });
  const scopedModels = useMemo(() => {
    if (!form.provider) return models;
    return models.filter((model) => model.metadata?.provider === form.provider);
  }, [form.provider, models]);
  const selectedModel = useMemo(() => {
    if (form.model) return form.model;
    return defaultModelID(scopedModels);
  }, [form.model, scopedModels]);

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
        model: modelStillValid ? current.model : defaultModelID(nextModels),
      };
    });
  }
  const submitForm = () => onSave({ ...form, model: selectedModel });

  return (
    <Modal
      title="Project defaults"
      onClose={onClose}
      width={520}
      footer={
        <button
          className="btn btn-primary"
          type="button"
          disabled={pending}
          onClick={() => void submitForm()}
          style={{ width: "100%", justifyContent: "center" }}
        >
          {pending ? "Saving…" : "Save defaults"}
        </button>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void submitForm();
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
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
              value={selectedModel}
              onChange={(model) => setForm((current) => ({ ...current, model }))}
              models={scopedModels}
              presets={providerPresets}
              showProvider={!form.provider}
            />
          </div>
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
        <div style={subtleTextStyle}>
          Native Hecate assignments copy these defaults when creating the backing task.
        </div>
      </form>
    </Modal>
  );
}

function normalizeWorkspaceMode(value: string) {
  if (value === "persistent" || value === "ephemeral") return value;
  return "in_place";
}

function RolesModal({
  error,
  pending,
  roles,
  onClose,
  onCreate,
  onDelete,
  onUpdate,
}: {
  error: string;
  pending: boolean;
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
              <input
                className="input"
                value={form.defaultAgentProfile}
                disabled={editingBuiltIn}
                placeholder="implementation"
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    defaultAgentProfile: event.target.value,
                  }))
                }
              />
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
  error,
  handoff,
  pending,
  roles,
  onClose,
  onSave,
}: {
  assignments: ProjectAssignmentRecord[];
  error: string;
  handoff: ProjectHandoffRecord | null;
  pending: boolean;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onSave: (form: HandoffForm) => void | Promise<void>;
}) {
  const [form, setForm] = useState<HandoffForm>(() => handoffFormFromRecord(handoff));
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
  assignment,
  chatModel,
  error,
  onDelete,
  onEdit,
  onOpenChat,
  onOpenTask,
  onStart,
  role,
  starting,
}: {
  assignment: ProjectAssignmentRecord;
  chatModel: string;
  error: string;
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
  const projectedStatus = execution?.status || assignment.status;
  const startable = assignment.driver_kind === "hecate_task" && projectedStatus === "queued";
  const external = assignment.driver_kind === "external_agent";
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
          >
            <Icon d={Icons.send} size={12} />
            {starting ? "Starting…" : "Start"}
          </button>
        )}
        {external && (
          <span
            style={subtleTextStyle}
            title="External assignment execution is not implemented yet."
          >
            Start in Chats
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
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          onClick={onOpenChat}
          disabled={!onOpenChat || !chatModel}
          title={
            chatModel ? `Open chat with ${chatModel}` : "Set project defaults before opening chat."
          }
        >
          <Icon d={Icons.chat} size={12} />
          Open chat
        </button>
        {taskID && <CopyableID text={taskID} compact />}
        {runID && <CopyableID text={runID} compact />}
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
      </div>
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
    assignment?.driver_kind === "hecate_task" &&
    (assignment.execution?.status || assignment.status) === "queued";
  const canCreateAssignment = !assignment && handoff.status !== "dismissed";
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
            Target assignment
          </button>
        )}
        {assignment && (
          <button
            className="btn btn-primary btn-sm"
            type="button"
            onClick={onStart}
            disabled={!startable || starting}
            title={
              startable ? "Start linked native assignment" : "Linked assignment is not queued."
            }
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

function buildProjectTimelineItems({
  activity,
  artifacts,
  handoffs,
  memoryCandidates,
  memoryEntries,
  project,
  roles,
  workItems,
}: {
  activity: ProjectActivityData | null;
  artifacts: ProjectCollaborationArtifactRecord[];
  handoffs: ProjectHandoffRecord[];
  memoryCandidates: ProjectMemoryCandidateRecord[];
  memoryEntries: ProjectMemoryRecord[];
  project: ProjectRecord;
  roles: ProjectWorkRoleRecord[];
  workItems: ProjectWorkItemRecord[];
}): ProjectTimelineItem[] {
  const items = new Map<string, ProjectTimelineItem>();
  const roleByID = new Map(roles.map((role) => [role.id, role]));
  const workByID = new Map(workItems.map((item) => [item.id, item]));
  for (const activityItem of projectActivityItems(activity)) {
    const workItem =
      workByID.get(activityItem.work_item.id) ??
      projectActivityWorkItemToWorkItem(project.id, activityItem.work_item);
    const role = roleByID.get(activityItem.assignment.role_id) ?? activityItem.role;
    const taskID = activityItem.linked_task_id || activityItem.assignment.task_id || "";
    const runID = activityItem.linked_run_id || activityItem.assignment.run_id || "";
    setTimelineItem(items, {
      id: `assignment:${activityItem.assignment.id}`,
      kind: "assignment",
      title: workItem.title,
      summary: activityItem.status_summary,
      actor: `role ${role?.name || activityItem.assignment.role_id}`,
      source: activityItem.assignment.driver_kind,
      timestamp: activityItem.updated_at || activityItem.assignment.updated_at,
      status: activityItem.blocking_signal,
      workItemID: workItem.id,
      taskID,
      runID,
      chatID: activityItem.linked_chat_id || activityItem.assignment.chat_session_id,
      assignment: activityItem.assignment,
    });
    for (const artifact of activityItem.recent_artifacts ?? []) {
      addTimelineArtifact(items, artifact, workItem.title);
    }
    for (const handoff of activityItem.recent_handoffs ?? []) {
      addTimelineHandoff(items, handoff, workItem.title);
    }
  }
  for (const artifact of artifacts) {
    const workTitle = workByID.get(artifact.work_item_id)?.title ?? "";
    addTimelineArtifact(items, artifact, workTitle);
  }
  for (const handoff of handoffs) {
    const workTitle = workByID.get(handoff.work_item_id)?.title ?? "";
    addTimelineHandoff(items, handoff, workTitle);
  }
  for (const entry of memoryEntries) {
    setTimelineItem(items, {
      id: `memory:${entry.id}`,
      kind: "memory",
      title: `Context memory: ${entry.title}`,
      summary: `${entry.enabled ? "Enabled" : "Disabled"} project memory entry`,
      actor: entry.source_kind || "operator",
      source: `${entry.trust_label}${entry.enabled ? "" : " / disabled"}`,
      timestamp: entry.updated_at || entry.created_at,
      status: entry.enabled ? "completed" : "stale_unknown",
      memoryEntry: entry,
    });
  }
  for (const candidate of memoryCandidates) {
    setTimelineItem(items, {
      id: `memory_candidate:${candidate.id}`,
      kind: "memory_candidate",
      title: `Memory candidate: ${candidate.title}`,
      summary: candidate.body,
      actor: candidate.suggested_source_kind || "generated",
      source: `${candidate.suggested_trust_label} / ${candidate.status}`,
      timestamp: candidate.updated_at || candidate.created_at,
      status: candidate.status === "pending" ? "awaiting_approval" : candidate.status,
    });
  }
  return Array.from(items.values()).sort(compareTimelineItems);
}

function buildProjectHealthSummary(
  project: ProjectRecord | null,
  activity: ProjectActivityData | null,
  workItems: ProjectWorkItemRecord[],
  memoryEntries: ProjectMemoryRecord[],
  memoryCandidates: ProjectMemoryCandidateRecord[],
): ProjectHealthSummary {
  const activityItems = uniqueActivityItems(activity);
  const projectedAssignments = workItems.flatMap((item) =>
    (item.assignments ?? []).map((assignment) => ({
      assignment,
      workItem: item,
      status: assignment.execution?.status || assignment.status,
    })),
  );
  const waitingItems = activityItems.filter((item) => isWaitingApprovalActivity(item));
  const failedItems = activityItems.filter((item) => isFailedOrCancelledActivity(item));
  const staleItems = [
    ...activityItems.filter((item) => item.blocking_signal === "stale_unknown"),
    ...activityItems.filter((item) => item.assignment.execution?.missing),
    ...projectedAssignments
      .filter((item) => isStaleAssignment(item.assignment, item.status))
      .map((item) => projectAssignmentToActivityAttention(project, item.workItem, item.assignment)),
  ].filter(Boolean) as ProjectActivityItemRecord[];
  const notStartedItems = activityItems.filter((item) => item.blocking_signal === "not_started");
  const enabledMemory = memoryEntries.filter((entry) => entry.enabled).length;
  const enabledContextSources = (project?.context_sources ?? []).filter(
    (source) => source.enabled,
  ).length;
  const memoryCandidateSummary = summarizeProjectMemoryCandidates(memoryCandidates);
  const handoffSummary = summarizeProjectHandoffs(activityItems);
  const summary = activity?.summary;
  const missingDefaults = Boolean(project && (!project.default_provider || !project.default_model));
  const attention: ProjectHealthAttention[] = [];
  const firstWaiting = waitingItems[0];
  if (firstWaiting) {
    attention.push(
      activityAttention(firstWaiting, "Approval waiting", "View approvals", "blocked"),
    );
  }
  const firstFailed = failedItems[0];
  if (firstFailed) {
    attention.push(
      activityAttention(firstFailed, "Execution needs review", "View blocked", "blocked"),
    );
  }
  if (missingDefaults && project) {
    attention.push({
      id: `${project.id}:defaults`,
      title: "Provider/model defaults missing",
      detail: "Native project starts and assignment chats need a default provider and model.",
      status: "awaiting_approval",
    });
  }
  const firstPendingHandoff = activityItems.find((item) => hasPendingHandoff(item));
  if (firstPendingHandoff) {
    const latestHandoff = firstPendingHandoff.recent_handoffs?.find(
      (handoff) => handoff.status === "pending",
    );
    attention.push({
      id: `${firstPendingHandoff.id}:handoff`,
      title: `Pending handoff: ${firstPendingHandoff.work_item.title}`,
      detail: [
        firstNonEmpty(
          latestHandoff?.title,
          firstPendingHandoff.handoff_summary?.latest_title,
          "Handoff awaiting operator follow-up",
        ),
        firstPendingHandoff.role.name || firstPendingHandoff.assignment.role_id,
        firstPendingHandoff.handoff_summary?.latest_at
          ? `updated ${formatAbsoluteTime(firstPendingHandoff.handoff_summary.latest_at)}`
          : "",
      ]
        .filter(Boolean)
        .join(" · "),
      status: "awaiting_approval",
      bucket: "recent",
      workItemID: firstPendingHandoff.work_item.id,
      actionLabel: "View recent",
    });
  }
  const firstStale = staleItems[0];
  if (firstStale) {
    attention.push(
      activityAttention(firstStale, "Stale or unknown assignment", "View blocked", "blocked"),
    );
  }
  const firstNotStarted = notStartedItems[0];
  if (firstNotStarted) {
    attention.push(
      activityAttention(firstNotStarted, "Assignment not started", "View blocked", "blocked"),
    );
  }
  if (enabledMemory === 0 && enabledContextSources === 0 && project) {
    attention.push({
      id: `${project.id}:context`,
      title: "No project memory or context sources enabled",
      detail: "Project-scoped context is empty for new chats and linked context packets.",
      status: "stale_unknown",
    });
  }
  const firstPendingCandidate = memoryCandidates.find(
    (candidate) => candidate.status === "pending",
  );
  if (firstPendingCandidate) {
    attention.push({
      id: `${firstPendingCandidate.id}:memory-candidate`,
      title: "Memory candidate pending review",
      detail: `${firstPendingCandidate.title} · ${firstPendingCandidate.suggested_trust_label}`,
      status: "awaiting_approval",
      candidateID: firstPendingCandidate.id,
    });
  }

  return {
    activeNow: summary?.active_count ?? countActivityBySignals(activityItems, ["running"]),
    waitingApproval: waitingItems.length,
    blockedOrFailed: failedItems.length,
    recentCompleted: activity?.buckets.completed.length ?? summary?.completed_count ?? 0,
    staleAssignments: uniqueByID(staleItems).length,
    missingDefaults,
    enabledMemory,
    savedMemory: memoryEntries.length,
    enabledContextSources,
    memoryCandidates: memoryCandidateSummary,
    handoffs: handoffSummary,
    attention: uniqueAttention(attention).slice(0, 5),
  };
}

function projectHealthMetrics(health: ProjectHealthSummary): ProjectHealthMetric[] {
  return [
    {
      key: "active",
      label: "Active work",
      value: health.activeNow,
      status: health.activeNow > 0 ? "running" : "completed",
      detail: "queued, running, or live",
      bucket: "active",
    },
    {
      key: "approvals",
      label: "Waiting approval",
      value: health.waitingApproval,
      status: health.waitingApproval > 0 ? "awaiting_approval" : "completed",
      detail: "operator decisions pending",
      bucket: "blocked",
    },
    {
      key: "handoffs",
      label: "Pending handoffs",
      value: health.handoffs.pending,
      status: health.handoffs.pending > 0 ? "awaiting_approval" : "completed",
      detail: `${health.handoffs.accepted} accepted, ${health.handoffs.superseded} superseded, ${health.handoffs.dismissed} dismissed`,
      bucket: "recent",
    },
    {
      key: "failed",
      label: "Blocked / failed",
      value: health.blockedOrFailed,
      status: health.blockedOrFailed > 0 ? "failed" : "completed",
      detail: "blocked, failed, cancelled",
      bucket: "blocked",
    },
    {
      key: "completed",
      label: "Recent completions",
      value: health.recentCompleted,
      status: "completed",
      detail: "finished project work",
      bucket: "completed",
    },
    {
      key: "stale",
      label: "Stale / unknown",
      value: health.staleAssignments,
      status: health.staleAssignments > 0 ? "stale_unknown" : "completed",
      detail: "old active or missing linked run",
      bucket: "blocked",
    },
    {
      key: "defaults",
      label: "Defaults",
      value: health.missingDefaults ? "missing" : "set",
      status: health.missingDefaults ? "awaiting_approval" : "completed",
      detail: "provider and model",
    },
    {
      key: "context",
      label: "Context",
      value: health.enabledMemory + health.enabledContextSources,
      status:
        health.enabledMemory + health.enabledContextSources > 0 ? "completed" : "stale_unknown",
      detail: `${health.enabledMemory}/${health.savedMemory} memory, ${health.enabledContextSources} sources`,
    },
    {
      key: "memory_candidates",
      label: "Memory candidates",
      value: health.memoryCandidates.pending,
      status: health.memoryCandidates.pending > 0 ? "awaiting_approval" : "completed",
      detail: `${health.memoryCandidates.promoted} promoted, ${health.memoryCandidates.rejected} rejected`,
    },
  ];
}

function summarizeProjectMemoryCandidates(
  candidates: ProjectMemoryCandidateRecord[],
): ProjectHealthSummary["memoryCandidates"] {
  return candidates.reduce<ProjectHealthSummary["memoryCandidates"]>(
    (summary, candidate) => {
      if (candidate.status === "pending") summary.pending += 1;
      if (candidate.status === "promoted") summary.promoted += 1;
      if (candidate.status === "rejected") summary.rejected += 1;
      return summary;
    },
    { pending: 0, promoted: 0, rejected: 0 },
  );
}

function summarizeProjectHandoffs(
  items: ProjectActivityItemRecord[],
): ProjectHealthSummary["handoffs"] {
  const seenHandoffIDs = new Set<string>();
  return items.reduce<ProjectHealthSummary["handoffs"]>(
    (summary, item) => {
      const handoffSummary = item.handoff_summary;
      const recentHandoffs = item.recent_handoffs ?? [];
      if (recentHandoffs.length > 0) {
        for (const handoff of recentHandoffs) {
          if (seenHandoffIDs.has(handoff.id)) continue;
          seenHandoffIDs.add(handoff.id);
          addHandoffStatus(summary, handoff.status);
        }
        return summary;
      }
      if (handoffSummary) {
        summary.total += handoffSummary.count;
        summary.pending += handoffSummary.pending_count ?? 0;
        summary.accepted += handoffSummary.accepted_count ?? 0;
      }
      return summary;
    },
    { total: 0, pending: 0, accepted: 0, superseded: 0, dismissed: 0 },
  );
}

function addHandoffStatus(summary: ProjectHealthSummary["handoffs"], status: string) {
  summary.total += 1;
  if (status === "pending") summary.pending += 1;
  if (status === "accepted") summary.accepted += 1;
  if (status === "superseded") summary.superseded += 1;
  if (status === "dismissed") summary.dismissed += 1;
}

function hasPendingHandoff(item: ProjectActivityItemRecord): boolean {
  return (
    (item.handoff_summary?.pending_count ?? 0) > 0 ||
    Boolean(item.recent_handoffs?.some((handoff) => handoff.status === "pending"))
  );
}

function projectActivityItems(activity: ProjectActivityData | null): ProjectActivityItemRecord[] {
  if (!activity) return [];
  return [
    ...activity.buckets.blocked,
    ...activity.buckets.active,
    ...activity.buckets.completed,
    ...activity.buckets.recent,
    ...(activity.recent ?? []),
  ];
}

function uniqueActivityItems(activity: ProjectActivityData | null): ProjectActivityItemRecord[] {
  return uniqueByID(projectActivityItems(activity));
}

function addTimelineArtifact(
  items: Map<string, ProjectTimelineItem>,
  artifact: ProjectCollaborationArtifactRecord,
  workTitle: string,
) {
  const title = artifact.title || artifact.id;
  setTimelineItem(items, {
    id: `artifact:${artifact.id}`,
    kind: artifact.kind === "decision_note" ? "decision" : "artifact",
    title,
    summary: artifact.body,
    actor: artifact.author_role_id || "project",
    source: workTitle ? `${artifact.kind} / ${workTitle}` : artifact.kind,
    timestamp: artifact.updated_at || artifact.created_at,
    workItemID: artifact.work_item_id,
  });
}

function addTimelineHandoff(
  items: Map<string, ProjectTimelineItem>,
  handoff: ProjectHandoffRecord,
  workTitle: string,
) {
  setTimelineItem(items, {
    id: `handoff:${handoff.id}`,
    kind: "handoff",
    title: handoff.title || handoff.id,
    summary: handoff.summary || handoff.recommended_next_action,
    actor: handoff.created_by_role_id || "handoff",
    source: workTitle ? `${handoff.status} / ${workTitle}` : handoff.status,
    timestamp: handoff.updated_at || handoff.created_at,
    status: handoff.status,
    workItemID: handoff.work_item_id,
    taskID: "",
    runID: handoff.source_run_id,
    chatID: handoff.source_chat_session_id,
  });
}

function setTimelineItem(items: Map<string, ProjectTimelineItem>, item: ProjectTimelineItem) {
  const current = items.get(item.id);
  if (!current || compareTimelineItems(item, current) < 0) {
    items.set(item.id, item);
  }
}

function compareTimelineItems(left: ProjectTimelineItem, right: ProjectTimelineItem): number {
  const leftTime = Date.parse(left.timestamp || "");
  const rightTime = Date.parse(right.timestamp || "");
  if (Number.isFinite(leftTime) && Number.isFinite(rightTime) && leftTime !== rightTime) {
    return rightTime - leftTime;
  }
  if (Number.isFinite(leftTime) !== Number.isFinite(rightTime)) {
    return Number.isFinite(leftTime) ? -1 : 1;
  }
  return left.id.localeCompare(right.id);
}

function timelineKindLabel(kind: ProjectTimelineItemKind): string {
  switch (kind) {
    case "assignment":
      return "assignment";
    case "decision":
      return "decision";
    case "handoff":
      return "handoff";
    case "memory":
      return "memory";
    case "memory_candidate":
      return "memory candidate";
    case "artifact":
      return "artifact";
  }
}

function timelineBadgeClass(item: ProjectTimelineItem): string {
  if (item.kind === "decision") return "badge badge-amber";
  if (item.kind === "handoff" && item.status === "pending") return "badge badge-amber";
  if (item.kind === "memory_candidate" && item.status === "awaiting_approval") {
    return "badge badge-amber";
  }
  if (item.kind === "memory" && item.status === "stale_unknown") return "badge badge-amber";
  return "badge badge-muted";
}

function uniqueByID<T extends { id: string }>(items: T[]): T[] {
  const seen = new Set<string>();
  const unique: T[] = [];
  for (const item of items) {
    if (seen.has(item.id)) continue;
    seen.add(item.id);
    unique.push(item);
  }
  return unique;
}

function uniqueAttention(items: ProjectHealthAttention[]): ProjectHealthAttention[] {
  return uniqueByID(items);
}

function isWaitingApprovalActivity(item: ProjectActivityItemRecord): boolean {
  return (
    item.blocking_signal === "awaiting_approval" ||
    item.status === "awaiting_approval" ||
    Boolean(item.assignment.execution?.pending_approval_count)
  );
}

function isFailedOrCancelledActivity(item: ProjectActivityItemRecord): boolean {
  return (
    item.blocking_signal === "failed" ||
    item.status === "failed" ||
    item.status === "cancelled" ||
    item.assignment.execution?.status === "failed" ||
    item.assignment.execution?.status === "cancelled"
  );
}

function countActivityBySignals(items: ProjectActivityItemRecord[], signals: string[]): number {
  return items.filter(
    (item) => signals.includes(item.blocking_signal) || signals.includes(item.status),
  ).length;
}

function isStaleAssignment(assignment: ProjectAssignmentRecord, status: string): boolean {
  if (status !== "queued" && status !== "running" && status !== "awaiting_approval") return false;
  const updatedAt = Date.parse(assignment.updated_at || assignment.started_at || "");
  if (!Number.isFinite(updatedAt)) return false;
  return Date.now() - updatedAt > 24 * 60 * 60 * 1000;
}

function projectAssignmentToActivityAttention(
  project: ProjectRecord | null,
  workItem: ProjectWorkItemRecord,
  assignment: ProjectAssignmentRecord,
): ProjectActivityItemRecord | null {
  if (!project) return null;
  return {
    id: assignment.id,
    project_id: project.id,
    work_item: {
      id: workItem.id,
      title: workItem.title,
      status: workItem.status,
      priority: workItem.priority,
    },
    assignment,
    role: {
      id: assignment.role_id,
      project_id: project.id,
      name: assignment.role_id,
      built_in: false,
    },
    status: assignment.execution?.status || assignment.status,
    blocking_signal: "stale_unknown",
    status_summary: "active assignment has not changed recently",
    linked_task_id: assignment.execution?.task_id || assignment.task_id,
    linked_run_id: assignment.execution?.run_id || assignment.run_id,
    artifact_summary: { count: assignment.execution?.artifact_count ?? 0 },
    updated_at: assignment.updated_at,
  };
}

function activityAttention(
  item: ProjectActivityItemRecord,
  title: string,
  actionLabel: string,
  bucket: ProjectActivityBucketKey,
): ProjectHealthAttention {
  const taskID =
    item.linked_task_id || item.assignment.execution?.task_id || item.assignment.task_id;
  const runID = item.linked_run_id || item.assignment.execution?.run_id || item.assignment.run_id;
  return {
    id: item.id,
    title: `${title}: ${item.work_item.title}`,
    detail: [
      item.status_summary,
      item.role.name || item.assignment.role_id,
      item.updated_at ? `updated ${formatAbsoluteTime(item.updated_at)}` : "",
    ]
      .filter(Boolean)
      .join(" · "),
    status: item.blocking_signal || item.status,
    bucket,
    workItemID: item.work_item.id,
    taskID,
    runID,
    actionLabel,
  };
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

function defaultModelID(models: ModelRecord[]): string {
  return models.find((model) => model.metadata?.default)?.id || models[0]?.id || "";
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
  };
}

function handoffFormFromRecord(handoff: ProjectHandoffRecord | null): HandoffForm {
  return {
    id: handoff?.id ?? "",
    sourceAssignmentID: handoff?.source_assignment_id ?? "",
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

function projectActivityWorkItemToWorkItem(
  projectID: string,
  item: ProjectActivityItemRecord["work_item"],
): ProjectWorkItemRecord {
  return {
    id: item.id,
    project_id: projectID,
    title: item.title,
    status: item.status,
    priority: item.priority,
    created_at: "",
    updated_at: "",
  };
}

function activitySignalLabel(signal: string): string {
  switch (signal) {
    case "awaiting_approval":
      return "approval";
    case "not_started":
      return "not started";
    case "stale_unknown":
      return "unknown";
    case "completed":
      return "done";
    default:
      return signal.replaceAll("_", " ");
  }
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

function splitRoleIDs(value: string): string[] {
  return splitIDs(value);
}

function splitIDs(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
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

const pathTextStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 11,
  fontFamily: "var(--font-mono)",
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
  marginTop: 5,
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

const panelStyle: CSSProperties = {
  border: "1px solid var(--border)",
  background: "var(--bg1)",
  borderRadius: "var(--radius-sm)",
  padding: 12,
};

const assignmentStyle: CSSProperties = {
  border: "1px solid var(--border)",
  background: "var(--bg2)",
  borderRadius: "var(--radius-sm)",
  padding: 10,
};

const activityRowStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 9,
};

const timelineGridStyle: CSSProperties = {
  display: "grid",
  gridTemplateColumns: "minmax(0, 1fr) minmax(220px, 32%)",
  gap: 14,
  alignItems: "start",
};

const timelineItemStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 9,
  minWidth: 0,
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
  borderLeft: "1px solid var(--border)",
  paddingLeft: 14,
  minWidth: 0,
};

const decisionItemStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 8,
  minWidth: 0,
};

const healthMetricGridStyle: CSSProperties = {
  display: "grid",
  gridTemplateColumns: "repeat(auto-fit, minmax(132px, 1fr))",
  gap: 8,
  marginBottom: 12,
};

const healthMetricStyle: CSSProperties = {
  alignItems: "flex-start",
  border: "1px solid var(--border)",
  display: "grid",
  gap: 6,
  justifyContent: "stretch",
  minHeight: 92,
  padding: 10,
  textAlign: "left",
};

const healthColumnStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 10,
};

const healthAttentionStyle: CSSProperties = {
  background: "var(--bg2)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  padding: 9,
};

const healthContextLineStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  gap: 8,
  justifyContent: "space-between",
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

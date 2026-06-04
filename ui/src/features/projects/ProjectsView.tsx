import { useCallback, useEffect, useMemo, useState, type CSSProperties } from "react";

import { useProjects } from "../../app/state/projects";
import { useProvidersAndModels } from "../../app/state/providersAndModels";
import {
  ApiError,
  createProjectAssignment,
  createProjectMemory,
  createProjectWorkRole,
  createProjectWorkItem,
  deleteProjectAssignment,
  deleteProjectMemory,
  deleteProjectWorkRole,
  deleteProjectWorkItem,
  getProjectActivity,
  getProjectAssignments,
  getProjectCollaborationArtifacts,
  getProjectMemory,
  getProjectWorkItem,
  getProjectWorkItems,
  getProjectWorkRoles,
  startProjectAssignment,
  updateProject,
  updateProjectAssignment,
  updateProjectMemory,
  updateProjectWorkRole,
  updateProjectWorkItem,
} from "../../lib/api";
import { formatAbsoluteTime } from "../../lib/format";
import { projectDefaultWorkspace } from "../../lib/project-workspace";
import type {
  ProjectAssignmentRecord,
  ProjectActivityData,
  ProjectActivityItemRecord,
  ProjectCollaborationArtifactRecord,
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
const MEMORY_TRUST_LABELS = [
  "operator_memory",
  "generated_summary",
  "handoff",
  "external_untrusted",
  "runtime_state",
];
const MEMORY_SOURCE_KINDS = [
  "operator",
  "generated_summary",
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
  const [roles, setRoles] = useState<ProjectWorkRoleRecord[]>([]);
  const [selectedWorkItemID, setSelectedWorkItemID] = useState("");
  const [selectedWorkItem, setSelectedWorkItem] = useState<ProjectWorkItemRecord | null>(null);
  const [assignments, setAssignments] = useState<ProjectAssignmentRecord[]>([]);
  const [artifacts, setArtifacts] = useState<ProjectCollaborationArtifactRecord[]>([]);
  const [workLoadState, setWorkLoadState] = useState<LoadState>("idle");
  const [detailLoadState, setDetailLoadState] = useState<LoadState>("idle");
  const [workError, setWorkError] = useState("");
  const [detailError, setDetailError] = useState("");
  const [assignmentErrors, setAssignmentErrors] = useState<Record<string, string>>({});
  const [startingAssignmentID, setStartingAssignmentID] = useState("");
  const [memoryEntries, setMemoryEntries] = useState<ProjectMemoryRecord[]>([]);
  const [memoryLoadState, setMemoryLoadState] = useState<LoadState>("idle");
  const [memoryError, setMemoryError] = useState("");
  const [editingMemory, setEditingMemory] = useState<ProjectMemoryRecord | "new" | null>(null);
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
    return providersAndModels.state.providers
      .filter((provider) => provider.healthy && provider.name)
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
  }, [providerPresets, providersAndModels.state.providers]);

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
      setEditingMemory(null);
      setDeleteMemory(null);
      setMemoryLoadState("idle");
      return;
    }
    setMemoryEntries([]);
    setEditingMemory(null);
    setDeleteMemory(null);
    setMemoryLoadState("loading");
    try {
      const payload = await getProjectMemory(projectID, true);
      setMemoryEntries(payload.data ?? []);
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
      setDetailLoadState("idle");
      return;
    }
    setDetailLoadState("loading");
    try {
      const [itemRes, assignmentRes, artifactRes] = await Promise.all([
        getProjectWorkItem(projectID, workItemID),
        getProjectAssignments(projectID, workItemID),
        getProjectCollaborationArtifacts(projectID, workItemID),
      ]);
      setSelectedWorkItem(itemRes.data);
      setAssignments(assignmentRes.data ?? []);
      setArtifacts(artifactRes.data ?? []);
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
        <ProjectActivityInbox
          activity={activity}
          loading={workLoadState === "loading"}
          onOpenChat={onOpenChat}
          onOpenTask={onOpenTask}
          onSelectWorkItem={setSelectedWorkItemID}
          onStartAssignment={(assignment, workItemID) =>
            void handleStartAssignment(assignment, workItemID)
          }
          project={selectedProject}
          startingAssignmentID={startingAssignmentID}
          workItems={workItems}
        />
        <ProjectMemoryPanel
          entries={memoryEntries}
          error={memoryError}
          loading={memoryLoadState === "loading"}
          onDelete={setDeleteMemory}
          onEdit={setEditingMemory}
          onNew={() => setEditingMemory("new")}
          onRefresh={() => void loadProjectMemory(selectedProjectID)}
          project={selectedProject}
        />
        <WorkItemDetail
          assignments={assignments}
          artifacts={artifacts}
          assignmentErrors={assignmentErrors}
          detailError={detailError}
          loading={detailLoadState === "loading"}
          onOpenTask={onOpenTask}
          onRefresh={refreshSelectedWorkItem}
          onDeleteWorkItem={(item) => setDeleteWorkItem(item)}
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
          project={selectedProject}
          roleByID={roleByID}
          startingAssignmentID={startingAssignmentID}
          workItem={selectedWorkItem}
          onAddAssignment={() => {
            setNewAssignmentError("");
            setNewAssignmentModalOpen(true);
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

function ProjectActivityInbox({
  activity,
  loading,
  onOpenChat,
  onOpenTask,
  onSelectWorkItem,
  onStartAssignment,
  project,
  startingAssignmentID,
  workItems,
}: {
  activity: ProjectActivityData | null;
  loading: boolean;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onSelectWorkItem: (workItemID: string) => void;
  onStartAssignment: (assignment: ProjectAssignmentRecord, workItemID: string) => void;
  project: ProjectRecord | null;
  startingAssignmentID: string;
  workItems: ProjectWorkItemRecord[];
}) {
  const [bucket, setBucket] = useState<ProjectActivityBucketKey>("blocked");
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
                onClick={() => setBucket(tab.id)}
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
    </div>
  );
}

function ProjectMemoryPanel({
  entries,
  error,
  loading,
  onDelete,
  onEdit,
  onNew,
  onRefresh,
  project,
}: {
  entries: ProjectMemoryRecord[];
  error: string;
  loading: boolean;
  onDelete: (entry: ProjectMemoryRecord) => void;
  onEdit: (entry: ProjectMemoryRecord) => void;
  onNew: () => void;
  onRefresh: () => void;
  project: ProjectRecord | null;
}) {
  if (!project) return null;
  const enabledCount = entries.filter((entry) => entry.enabled).length;
  return (
    <div style={{ padding: "12px 16px 0" }}>
      <div style={panelStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
          <div>
            <div style={sectionLabelStyle}>Memory / Context</div>
            <div style={{ ...subtleTextStyle, marginTop: 3 }}>
              {loading
                ? "Loading project memory…"
                : `${enabledCount} enabled / ${entries.length} saved entries`}
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

function ProjectMemoryModal({
  entry,
  error,
  pending,
  onClose,
  onSave,
}: {
  entry: ProjectMemoryRecord | null;
  error: string;
  pending: boolean;
  onClose: () => void;
  onSave: (form: MemoryForm) => void | Promise<void>;
}) {
  const [form, setForm] = useState<MemoryForm>(() => memoryFormFromRecord(entry));
  const valid = form.title.trim().length > 0 && form.body.trim().length > 0;
  return (
    <Modal
      title={entry ? "Edit project memory" : "New project memory"}
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
          {pending ? "Saving…" : entry ? "Save memory" : "Create memory"}
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
  assignmentErrors,
  detailError,
  loading,
  onAddAssignment,
  onDeleteAssignment,
  onDeleteWorkItem,
  onEditAssignment,
  onEditWorkItem,
  onOpenChat,
  onOpenTask,
  onRefresh,
  onStartAssignment,
  project,
  roleByID,
  startingAssignmentID,
  workItem,
}: {
  assignments: ProjectAssignmentRecord[];
  artifacts: ProjectCollaborationArtifactRecord[];
  assignmentErrors: Record<string, string>;
  detailError: string;
  loading: boolean;
  onAddAssignment: () => void;
  onDeleteAssignment: (assignment: ProjectAssignmentRecord) => void;
  onDeleteWorkItem: (item: ProjectWorkItemRecord) => void;
  onEditAssignment: (assignment: ProjectAssignmentRecord) => void;
  onEditWorkItem: (item: ProjectWorkItemRecord) => void;
  onOpenChat?: (request: ProjectAssignmentChatLaunchRequest) => void;
  onOpenTask?: (taskID: string, runID?: string) => void;
  onRefresh: () => void;
  onStartAssignment: (assignment: ProjectAssignmentRecord) => void;
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
    workspaceMode: project.default_workspace_mode ?? "in_place",
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
              emptyLabel="select provider"
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
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Workspace mode</span>
          <select
            className="input"
            value={form.workspaceMode}
            onChange={(event) =>
              setForm((current) => ({ ...current, workspaceMode: event.target.value }))
            }
          >
            <option value="in_place">in_place</option>
            <option value="persistent">persistent</option>
            <option value="ephemeral">ephemeral</option>
          </select>
        </label>
        <div style={subtleTextStyle}>
          Native Hecate assignments copy these defaults when creating the backing task.
        </div>
      </form>
    </Modal>
  );
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

function formatMemorySource(entry: ProjectMemoryRecord): string {
  const sourceKind = entry.source_kind || "operator";
  return entry.source_id ? `${sourceKind}:${entry.source_id}` : sourceKind;
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

function splitRoleIDs(value: string): string[] {
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

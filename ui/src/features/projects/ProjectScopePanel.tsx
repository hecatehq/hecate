import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
  type Ref,
} from "react";

import { useProjects } from "../../app/state/projects";
import { chooseWorkspaceDirectory } from "../../lib/api";
import { projectDefaultWorkspace } from "../../lib/project-workspace";
import type { ProjectDeleteRecord, ProjectRecord } from "../../types/project";
import { ConfirmModal, Icon, Icons, InlineError } from "../shared/ui";
import { CreateProjectModal } from "./CreateProjectModal";
import { projectErrorMessage } from "./projectDisplay";
import { createProjectPayloadFromForm, type CreateProjectForm } from "./projectSettings";

type ProjectScopePanelProps = {
  noProjectDetail: string;
  emptyHint: string;
  deleteMessage: (project: ProjectRecord) => ReactNode;
  canChangeProjectScope?: () => boolean;
  projectScopeChangeBlockReason?: () => string;
  beginProjectDelete: () => number | null;
  finishProjectDelete: (token: number) => void;
  onProjectDeleted?: (projectID: string, result: ProjectDeleteRecord) => void;
  onProjectSelected?: (
    projectID: string,
    project: ProjectRecord | null,
  ) => boolean | void | Promise<boolean | void>;
};

const sidebarSectionActionStyle: CSSProperties = {
  alignItems: "center",
  display: "inline-flex",
  height: 24,
  justifyContent: "center",
  lineHeight: 1,
  minHeight: 24,
  minWidth: 24,
  padding: 0,
  width: 24,
};

const visuallyHiddenStatusStyle: CSSProperties = {
  border: 0,
  clip: "rect(0 0 0 0)",
  clipPath: "inset(50%)",
  height: 1,
  margin: -1,
  overflow: "hidden",
  padding: 0,
  position: "absolute",
  whiteSpace: "nowrap",
  width: 1,
};

export function ProjectScopePanel({
  noProjectDetail,
  emptyHint,
  deleteMessage,
  canChangeProjectScope,
  projectScopeChangeBlockReason,
  beginProjectDelete,
  finishProjectDelete,
  onProjectDeleted,
  onProjectSelected,
}: ProjectScopePanelProps) {
  const projects = useProjects();
  const [projectsExpanded, setProjectsExpanded] = useState(false);
  const [renamingProjectID, setRenamingProjectID] = useState<string | null>(null);
  const [projectRenameValue, setProjectRenameValue] = useState("");
  const [hoveredProjectID, setHoveredProjectID] = useState<string | null>(null);
  const [deleteProjectID, setDeleteProjectID] = useState<string | null>(null);
  const [deleteProjectPending, setDeleteProjectPending] = useState(false);
  const [deleteProjectError, setDeleteProjectError] = useState("");
  const deleteProjectPendingRef = useRef(false);
  const [createProjectOpen, setCreateProjectOpen] = useState(false);
  const [createProjectPending, setCreateProjectPending] = useState(false);
  const [createProjectError, setCreateProjectError] = useState("");
  const createProjectInFlightRef = useRef(false);
  const createProjectRequestSequenceRef = useRef(0);
  const projectSelectionSequenceRef = useRef(0);
  const [catalogRetryPending, setCatalogRetryPending] = useState(false);
  const [catalogRetryAnnouncement, setCatalogRetryAnnouncement] = useState({
    key: "0",
    message: "",
  });
  const catalogRetryAnnouncementSequenceRef = useRef(0);
  const catalogRetryButtonRef = useRef<HTMLButtonElement>(null);
  const catalogRetryInFlightRef = useRef(false);
  const catalogRetryFocusOwnedRef = useRef(false);
  const catalogRetryFocusedElementRef = useRef<HTMLButtonElement | null>(null);
  const catalogRecoveryWasPendingRef = useRef(false);
  const projectsToggleButtonRef = useRef<HTMLButtonElement>(null);
  const canChangeProjectScopeRef = useRef(canChangeProjectScope);
  canChangeProjectScopeRef.current = canChangeProjectScope;
  const activeProject =
    projects.activeProjectID === ""
      ? null
      : (projects.state.projects.find((project) => project.id === projects.activeProjectID) ??
        null);
  const pendingDeleteProject =
    projects.state.projects.find((project) => project.id === deleteProjectID) ?? null;
  const catalogRecoveryPending =
    catalogRetryPending || (projects.state.loading && Boolean(projects.state.catalogError));

  function projectScopeChangeAllowed(): boolean {
    return !canChangeProjectScopeRef.current || canChangeProjectScopeRef.current();
  }

  function currentProjectScopeChangeBlockReason(): string {
    return (
      projectScopeChangeBlockReason?.().trim() ||
      "Wait for the current chat ownership change to finish, then try again."
    );
  }

  useEffect(
    () => () => {
      createProjectRequestSequenceRef.current += 1;
      projectSelectionSequenceRef.current += 1;
    },
    [],
  );

  useEffect(() => {
    if (!projects.state.catalogError || !catalogRetryAnnouncement.message) return;
    catalogRetryAnnouncementSequenceRef.current += 1;
    setCatalogRetryAnnouncement({
      key: String(catalogRetryAnnouncementSequenceRef.current),
      message: "",
    });
  }, [catalogRetryAnnouncement.message, projects.state.catalogError]);

  useLayoutEffect(() => {
    const wasPending = catalogRecoveryWasPendingRef.current;
    catalogRecoveryWasPendingRef.current = catalogRecoveryPending;
    if (!wasPending && catalogRecoveryPending) {
      catalogRetryFocusOwnedRef.current = document.activeElement === catalogRetryButtonRef.current;
      catalogRetryFocusedElementRef.current = catalogRetryButtonRef.current;
      return;
    }
    if (!wasPending || catalogRecoveryPending) return;
    const activeElement = document.activeElement;
    const focusedElement = catalogRetryFocusedElementRef.current;
    if (
      !projects.state.catalogError &&
      catalogRetryFocusOwnedRef.current &&
      (activeElement === focusedElement || activeElement === document.body)
    ) {
      projectsToggleButtonRef.current?.focus();
    }
    catalogRetryFocusOwnedRef.current = false;
    catalogRetryFocusedElementRef.current = null;
  }, [catalogRecoveryPending, projects.state.catalogError]);

  function startProjectRename(project: ProjectRecord) {
    setRenamingProjectID(project.id);
    setProjectRenameValue(project.name);
  }

  function commitProjectRename(project: ProjectRecord) {
    const nextName = projectRenameValue.trim();
    setRenamingProjectID(null);
    if (nextName && nextName !== project.name) {
      void projects.actions.renameProject(project.id, nextName);
    }
  }

  async function selectProjectScope(
    projectID: string,
    projectOverride?: ProjectRecord | null,
  ): Promise<boolean> {
    const selectionSequence = ++projectSelectionSequenceRef.current;
    if (!projectScopeChangeAllowed()) return false;
    const project =
      projectOverride !== undefined
        ? projectOverride
        : projectID === ""
          ? null
          : (projects.state.projects.find((item) => item.id === projectID) ?? null);
    const accepted = await onProjectSelected?.(projectID, project);
    if (projectSelectionSequenceRef.current !== selectionSequence || accepted === false) {
      return false;
    }
    if (!projectScopeChangeAllowed()) return false;
    void projects.actions.selectProject(projectID);
    return true;
  }

  async function handleCreateProject(form: CreateProjectForm) {
    if (createProjectInFlightRef.current) return;
    const payload = createProjectPayloadFromForm(form);
    if (!payload.name) {
      setCreateProjectError("Project name is required.");
      return;
    }
    if (!projectScopeChangeAllowed()) return;
    createProjectInFlightRef.current = true;
    const requestSequence = ++createProjectRequestSequenceRef.current;
    setCreateProjectPending(true);
    setCreateProjectError("");
    try {
      const created = await projects.actions.createProject(payload);
      if (createProjectRequestSequenceRef.current !== requestSequence || !created) return;
      setCreateProjectOpen(false);
      if (!projectScopeChangeAllowed()) return;
      setProjectsExpanded(false);
      await selectProjectScope(created.id, created);
    } catch (error) {
      if (createProjectRequestSequenceRef.current !== requestSequence) return;
      setCreateProjectError(error instanceof Error ? error.message : "Failed to create project.");
    } finally {
      if (createProjectRequestSequenceRef.current === requestSequence) {
        createProjectInFlightRef.current = false;
        setCreateProjectPending(false);
      }
    }
  }

  async function handleChooseWorkspace() {
    const workspace = await chooseWorkspaceDirectory();
    return {
      path: workspace.data.path,
      branch: workspace.data.branch || undefined,
    };
  }

  async function retryProjectCatalog() {
    if (catalogRetryInFlightRef.current || projects.state.loading) return;
    if (!projectScopeChangeAllowed()) return;
    const retryOwnedFocusAtStart = document.activeElement === catalogRetryButtonRef.current;
    catalogRetryInFlightRef.current = true;
    setCatalogRetryPending(true);
    catalogRetryAnnouncementSequenceRef.current += 1;
    setCatalogRetryAnnouncement({
      key: String(catalogRetryAnnouncementSequenceRef.current),
      message: "",
    });
    try {
      const result = await projects.actions.loadProjects({
        shouldApply: projectScopeChangeAllowed,
      });
      if (result.status !== "applied") return;
      const retryStillOwnsFocus =
        retryOwnedFocusAtStart && document.activeElement === catalogRetryButtonRef.current;
      catalogRetryAnnouncementSequenceRef.current += 1;
      setCatalogRetryAnnouncement({
        key: String(catalogRetryAnnouncementSequenceRef.current),
        message: "Projects loaded.",
      });
      if (retryStillOwnsFocus) {
        projectsToggleButtonRef.current?.focus();
      }
    } finally {
      catalogRetryInFlightRef.current = false;
      setCatalogRetryPending(false);
    }
  }

  return (
    <>
      <div aria-atomic="true" aria-live="polite" role="status" style={visuallyHiddenStatusStyle}>
        <span key={catalogRetryAnnouncement.key}>{catalogRetryAnnouncement.message}</span>
      </div>
      <div style={{ padding: "8px 8px 6px", borderBottom: "1px solid var(--border)" }}>
        <SidebarSectionHeader
          actionLabel="Add project"
          expanded={projectsExpanded}
          label="Projects"
          onAction={() => {
            if (!projectScopeChangeAllowed()) return;
            setCreateProjectError("");
            projects.actions.setError("");
            setCreateProjectOpen(true);
          }}
          onToggle={() => setProjectsExpanded((value) => !value)}
          toggleButtonRef={projectsToggleButtonRef}
        />
        {projectsExpanded ? (
          <>
            <ProjectRow
              active={projects.activeProjectID === ""}
              detail={noProjectDetail}
              label="No project"
              onSelect={() => {
                void selectProjectScope("");
              }}
            />
            {projects.state.projects.map((project) => (
              <ProjectRow
                key={project.id}
                active={projects.activeProjectID === project.id}
                actionsVisible={hoveredProjectID === project.id}
                detail={projectDetail(project)}
                editable
                label={project.name}
                onSelect={() => {
                  void selectProjectScope(project.id);
                }}
                onDelete={() => {
                  projects.actions.setError("");
                  setDeleteProjectError("");
                  setDeleteProjectID(project.id);
                }}
                onInteractionChange={(active) => {
                  setHoveredProjectID(active ? project.id : null);
                }}
                onRenameCancel={() => setRenamingProjectID(null)}
                onRenameChange={setProjectRenameValue}
                onRenameCommit={() => commitProjectRename(project)}
                onRenameStart={() => startProjectRename(project)}
                renameValue={projectRenameValue}
                renaming={renamingProjectID === project.id}
              />
            ))}
          </>
        ) : (
          <ProjectRow
            active
            detail={activeProject ? projectDetail(activeProject) : noProjectDetail}
            label={activeProject?.name ?? "No project"}
            onSelect={() => setProjectsExpanded(true)}
          />
        )}
        {projectsExpanded && projects.state.projects.length === 0 && (
          <div style={{ padding: "6px 8px 3px", color: "var(--t3)", fontSize: 11 }}>
            {emptyHint}
          </div>
        )}
        {projects.state.error && (
          <div role="status" style={{ padding: "6px 8px 0", color: "var(--yellow)", fontSize: 11 }}>
            {projects.state.error}
          </div>
        )}
        {(projects.state.catalogError || catalogRecoveryPending) && (
          <div
            style={{
              alignItems: "center",
              color: "var(--yellow)",
              display: "flex",
              fontSize: 11,
              gap: 8,
              justifyContent: "space-between",
              padding: "6px 8px 0",
            }}
          >
            <span aria-atomic="true" aria-live="polite" role="status">
              {catalogRecoveryPending ? "Retrying projects…" : "Projects could not be loaded."}
            </span>
            <button
              aria-disabled={catalogRecoveryPending || undefined}
              className="btn btn-primary btn-sm"
              onBlur={(event) => {
                if (event.relatedTarget instanceof HTMLElement && event.relatedTarget.isConnected) {
                  catalogRetryFocusOwnedRef.current = false;
                }
              }}
              onClick={() => void retryProjectCatalog()}
              onFocus={(event) => {
                catalogRetryFocusOwnedRef.current = true;
                catalogRetryFocusedElementRef.current = event.currentTarget;
              }}
              ref={catalogRetryButtonRef}
              type="button"
            >
              {catalogRecoveryPending ? "Retrying…" : "Retry"}
            </button>
          </div>
        )}
      </div>
      {createProjectOpen && (
        <CreateProjectModal
          error={createProjectError}
          pending={createProjectPending}
          onChooseWorkspace={handleChooseWorkspace}
          onClose={() => {
            setCreateProjectOpen(false);
            setCreateProjectError("");
          }}
          onSave={handleCreateProject}
        />
      )}
      {pendingDeleteProject && (
        <ConfirmModal
          danger
          title="Delete project"
          confirmLabel="Delete project"
          message={
            <div style={{ display: "grid", gap: 12 }}>
              <div>{deleteMessage(pendingDeleteProject)}</div>
              <InlineError message={deleteProjectError} />
            </div>
          }
          pending={deleteProjectPending}
          returnFocusRef={projectsToggleButtonRef}
          onClose={() => {
            if (!deleteProjectPendingRef.current) {
              setDeleteProjectID(null);
              setDeleteProjectError("");
            }
          }}
          onConfirm={async () => {
            if (deleteProjectPendingRef.current) return;
            setDeleteProjectError("");
            const knownBlockReason = projectScopeChangeBlockReason?.().trim() || "";
            if (knownBlockReason) {
              setDeleteProjectError(knownBlockReason);
              return;
            }
            if (!projectScopeChangeAllowed()) {
              setDeleteProjectError(currentProjectScopeChangeBlockReason());
              return;
            }
            const ownershipMutationToken = beginProjectDelete();
            if (ownershipMutationToken === null) {
              setDeleteProjectError(currentProjectScopeChangeBlockReason());
              return;
            }
            deleteProjectPendingRef.current = true;
            setDeleteProjectPending(true);
            const projectID = pendingDeleteProject.id;
            try {
              const deleted = await projects.actions.deleteProject(projectID);
              if (!deleted) {
                setDeleteProjectError("Project could not be deleted. Try again.");
                return;
              }
              onProjectDeleted?.(projectID, deleted);
              setDeleteProjectID(null);
            } catch (error) {
              setDeleteProjectError(projectErrorMessage(error, "Failed to delete project."));
            } finally {
              finishProjectDelete(ownershipMutationToken);
              deleteProjectPendingRef.current = false;
              setDeleteProjectPending(false);
            }
          }}
        />
      )}
    </>
  );
}

function SidebarSectionHeader({
  actionLabel,
  expanded,
  label,
  onAction,
  onToggle,
  toggleButtonRef,
}: {
  actionLabel: string;
  expanded: boolean;
  label: string;
  onAction: () => void;
  onToggle: () => void;
  toggleButtonRef?: Ref<HTMLButtonElement>;
}) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        padding: "0 4px 4px",
        gap: 8,
      }}
    >
      <div
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          letterSpacing: "0.08em",
          textTransform: "uppercase",
          color: "var(--t3)",
        }}
      >
        {label}
      </div>
      <div style={{ display: "inline-flex", alignItems: "center", gap: 2 }}>
        <button
          type="button"
          className="btn btn-ghost btn-sm"
          aria-label={actionLabel}
          title={actionLabel}
          onClick={onAction}
          style={sidebarSectionActionStyle}
        >
          <Icon d={Icons.plus} size={14} />
        </button>
        <button
          type="button"
          className="btn btn-ghost btn-sm"
          aria-expanded={expanded}
          aria-label={expanded ? "Collapse projects" : "Expand projects"}
          title={expanded ? "Collapse projects" : "Expand projects"}
          onClick={onToggle}
          ref={toggleButtonRef}
          style={sidebarSectionActionStyle}
        >
          <Icon d={expanded ? Icons.chevD : Icons.chevR} size={12} />
        </button>
      </div>
    </div>
  );
}

function ProjectRow({
  actionsVisible = false,
  active,
  detail,
  editable = false,
  label,
  onDelete,
  onInteractionChange,
  onRenameCancel,
  onRenameChange,
  onRenameCommit,
  onRenameStart,
  onSelect,
  renameValue = "",
  renaming = false,
}: {
  actionsVisible?: boolean;
  active: boolean;
  detail: string;
  editable?: boolean;
  label: string;
  onDelete?: () => void;
  onInteractionChange?: (active: boolean) => void;
  onRenameCancel?: () => void;
  onRenameChange?: (value: string) => void;
  onRenameCommit?: () => void;
  onRenameStart?: () => void;
  onSelect: () => void;
  renameValue?: string;
  renaming?: boolean;
}) {
  return (
    <div
      onBlur={(e) => {
        const nextFocus = e.relatedTarget;
        if (!(nextFocus instanceof Node) || !e.currentTarget.contains(nextFocus)) {
          onInteractionChange?.(false);
        }
      }}
      onFocus={() => onInteractionChange?.(true)}
      onMouseEnter={() => onInteractionChange?.(true)}
      onMouseLeave={() => onInteractionChange?.(false)}
      style={{
        width: "100%",
        borderRadius: "var(--radius-md)",
        background: active ? "var(--teal-bg)" : "transparent",
        color: active ? "var(--t0)" : "var(--t2)",
        display: "flex",
        gap: 8,
        alignItems: "center",
        padding: "3px 6px",
        minHeight: 28,
      }}
      title={detail ? `${label} — ${detail}` : label}
    >
      {renaming ? (
        <input
          autoFocus
          aria-label={`Rename project ${label}`}
          onBlur={onRenameCommit}
          onChange={(e) => onRenameChange?.(e.target.value)}
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => {
            if (e.key === "Enter") onRenameCommit?.();
            if (e.key === "Escape") onRenameCancel?.();
          }}
          style={{
            flex: 1,
            minWidth: 0,
            height: 22,
            boxSizing: "border-box",
            fontSize: 12,
            background: "var(--bg3)",
            border: "1px solid var(--teal)",
            borderRadius: "var(--radius-sm)",
            color: "var(--t0)",
            padding: "0 5px",
            outline: "none",
            fontFamily: "var(--font-sans)",
            lineHeight: "20px",
          }}
          value={renameValue}
        />
      ) : (
        <button
          type="button"
          aria-current={active ? "true" : undefined}
          aria-label={`Project ${label}`}
          onClick={onSelect}
          style={{
            minWidth: 0,
            flex: 1,
            border: 0,
            background: "transparent",
            color: "inherit",
            cursor: "pointer",
            display: "grid",
            gridTemplateColumns: "18px minmax(0, 1fr)",
            gap: 8,
            alignItems: "center",
            padding: "2px 0",
            textAlign: "left",
            font: "inherit",
          }}
        >
          <Icon d={Icons.folder} size={15} strokeWidth={1.7} />
          <span
            style={{
              minWidth: 0,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              fontSize: 12,
              lineHeight: "16px",
              fontWeight: active ? 550 : 450,
            }}
          >
            {label}
          </span>
        </button>
      )}
      {editable && !renaming && (
        <span
          style={{
            display: "flex",
            gap: 1,
            opacity: actionsVisible ? 1 : 0,
            transition: "opacity 0.15s",
            flexShrink: 0,
          }}
        >
          <button
            aria-label={`Rename project ${label}`}
            aria-hidden={!actionsVisible}
            className="btn btn-ghost btn-sm"
            onClick={(e) => {
              e.stopPropagation();
              onRenameStart?.();
            }}
            style={{ padding: "1px 3px" }}
            tabIndex={actionsVisible ? 0 : -1}
            title="Rename"
            type="button"
          >
            <Icon d={Icons.edit} size={10} />
          </button>
          <button
            aria-label={`Delete project ${label}`}
            aria-hidden={!actionsVisible}
            className="btn btn-ghost btn-sm"
            onClick={(e) => {
              e.stopPropagation();
              onDelete?.();
            }}
            style={{ padding: "1px 3px", color: "var(--red)" }}
            tabIndex={actionsVisible ? 0 : -1}
            title="Delete"
            type="button"
          >
            <Icon d={Icons.trash} size={10} />
          </button>
        </span>
      )}
    </div>
  );
}

function projectDetail(project: ProjectRecord): string {
  return projectDefaultWorkspace(project) || project.description || "";
}

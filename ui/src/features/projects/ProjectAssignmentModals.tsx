import { useRef, useState, type CSSProperties } from "react";

import type {
  ProjectAssignmentRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { InlineError, Modal } from "../shared/ui";
import {
  HUMAN_ASSIGNMENT_DESCRIPTION,
  PROJECT_ASSIGNMENT_DESTINATIONS,
  projectAssignmentDestinationLabel,
} from "./projectAssignmentDestinations";
import { ProjectRootSelect } from "./ProjectRootSelect";
import { projectRootOptionLabel } from "./projectSettings";
import { assignmentStatusLabel } from "./projectDisplay";
import {
  ASSIGNMENT_STATUSES,
  assignmentStatusFromValue,
  defaultDriverForRole,
  type EditAssignmentForm,
  type NewAssignmentForm,
} from "./projectWorkForms";
import {
  projectWorkFieldLabelStyle,
  projectWorkFieldStyle,
  projectWorkSubtleTextStyle,
} from "./projectWorkModalStyles";

type NewAssignmentModalProps = {
  error: string;
  pending: boolean;
  project: ProjectRecord;
  workItem: ProjectWorkItemRecord | null;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onCreate: (form: NewAssignmentForm) => void | Promise<void>;
};

export function NewAssignmentModal({
  error,
  pending,
  project,
  workItem,
  roles,
  onClose,
  onCreate,
}: NewAssignmentModalProps) {
  const responsibilitySelectRef = useRef<HTMLSelectElement>(null);
  const defaultRole =
    roles.find((role) => role.id === workItem?.owner_role_id) ??
    roles.find((role) => role.id === "software_developer") ??
    roles[0] ??
    null;
  const [form, setForm] = useState<NewAssignmentForm>({
    roleID: defaultRole?.id ?? "",
    driverKind: defaultDriverForRole(defaultRole),
    rootID: "",
  });
  const valid = form.roleID.trim().length > 0;
  const selectedRole = roles.find((role) => role.id === form.roleID) ?? null;
  const selectedRoot = project.roots.find((root) => root.id === form.rootID) ?? null;
  const humanDestination = form.driverKind === "manual";
  const hasWorkspaceOptions = project.roots.length > 0;
  const inheritedRootLabel = workItem?.root_id
    ? "Uses the work item's workspace"
    : project.default_root_id
      ? "Uses the project default workspace"
      : project.roots.length > 0
        ? "Uses the first active project workspace"
        : "No workspace selected";
  const rootSummary = selectedRoot ? projectRootOptionLabel(selectedRoot) : inheritedRootLabel;
  const driverSummary = selectedRole
    ? projectAssignmentDestinationLabel(form.driverKind || defaultDriverForRole(selectedRole))
    : "Select a responsibility";
  return (
    <Modal
      title="Add assignment"
      initialFocusRef={responsibilitySelectRef}
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
          if (!pending && valid) void onCreate(form);
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
        <section style={assignmentPlanStyle} aria-label="Assignment plan">
          <div style={assignmentPlanHeaderStyle}>
            <div style={assignmentPlanTitleStyle}>Assignment</div>
            <span className="badge badge-muted">Ready to add</span>
          </div>
          <div
            className="project-assignment-plan-grid"
            style={{
              ...assignmentPlanGridStyle,
              gridTemplateColumns: hasWorkspaceOptions
                ? "repeat(3, minmax(0, 1fr))"
                : "repeat(2, minmax(0, 1fr))",
            }}
          >
            <div>
              <div style={assignmentPlanLabelStyle}>Responsibility</div>
              <div style={assignmentPlanValueStyle}>
                {selectedRole?.name || "Select a responsibility"}
              </div>
            </div>
            <div>
              <div style={assignmentPlanLabelStyle}>Work done by</div>
              <div style={assignmentPlanValueStyle}>{driverSummary}</div>
            </div>
            {hasWorkspaceOptions && (
              <div>
                <div style={assignmentPlanLabelStyle}>Workspace</div>
                <div style={assignmentPlanValueStyle}>{rootSummary}</div>
              </div>
            )}
          </div>
        </section>
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Responsibility</span>
          <select
            ref={responsibilitySelectRef}
            className="input"
            name="assignment-responsibility"
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
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Work done by</span>
          <select
            aria-describedby={
              humanDestination ? "new-assignment-human-destination-help" : undefined
            }
            className="input"
            name="assignment-destination"
            value={form.driverKind}
            onChange={(event) => {
              const driverKind = event.target.value;
              setForm((current) => ({
                ...current,
                driverKind,
              }));
            }}
          >
            {PROJECT_ASSIGNMENT_DESTINATIONS.map((destination) => (
              <option key={destination.kind} value={destination.kind}>
                {destination.label}
              </option>
            ))}
          </select>
        </label>
        {humanDestination && (
          <div id="new-assignment-human-destination-help" style={projectWorkSubtleTextStyle}>
            {HUMAN_ASSIGNMENT_DESCRIPTION}
          </div>
        )}
        <ProjectRootSelect
          inheritLabel={
            workItem?.root_id ? "work item workspace" : "work item or project workspace"
          }
          label="Workspace (optional)"
          project={project}
          value={form.rootID}
          onChange={(rootID) => setForm((current) => ({ ...current, rootID }))}
        />
        {form.driverKind === "external_agent" && (
          <div style={projectWorkSubtleTextStyle}>
            External Agent work starts in Chats and progress is recorded here.
          </div>
        )}
      </form>
    </Modal>
  );
}

const assignmentPlanStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: 8,
  display: "grid",
  gap: 10,
  minWidth: 0,
  padding: "10px 11px",
};

const assignmentPlanHeaderStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  gap: 8,
  justifyContent: "space-between",
  minWidth: 0,
};

const assignmentPlanTitleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 13,
  fontWeight: 700,
};

const assignmentPlanGridStyle: CSSProperties = {
  display: "grid",
  gap: 8,
  minWidth: 0,
};

const assignmentPlanLabelStyle: CSSProperties = {
  color: "var(--t3)",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  textTransform: "uppercase",
};

const assignmentPlanValueStyle: CSSProperties = {
  color: "var(--t1)",
  fontSize: 12,
  lineHeight: 1.35,
  marginTop: 3,
  minWidth: 0,
  overflowWrap: "anywhere",
};

type EditAssignmentModalProps = {
  assignment: ProjectAssignmentRecord;
  error: string;
  pending: boolean;
  project: ProjectRecord;
  workItem: ProjectWorkItemRecord | null;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onSave: (form: EditAssignmentForm) => void | Promise<void>;
};

export function EditAssignmentModal({
  assignment,
  error,
  pending,
  project,
  workItem,
  roles,
  onClose,
  onSave,
}: EditAssignmentModalProps) {
  const [form, setForm] = useState<EditAssignmentForm>({
    id: assignment.id,
    roleID: assignment.role_id,
    driverKind: assignment.driver_kind || "hecate_task",
    rootID: assignment.root_id ?? "",
    status: assignmentStatusFromValue(assignment.status),
    taskID: assignment.execution_ref?.task_id ?? "",
    runID: assignment.execution_ref?.run_id ?? "",
    chatSessionID: assignment.execution_ref?.chat_session_id ?? "",
    messageID: assignment.execution_ref?.message_id ?? "",
    contextSnapshotID: assignment.execution_ref?.context_snapshot_id ?? "",
  });
  const [destructiveConfirmed, setDestructiveConfirmed] = useState(false);
  const valid = form.roleID.trim().length > 0;
  const humanDestination = form.driverKind === "manual";
  const manualAssignment = assignment.driver_kind === "manual";
  const originalStatus = assignmentStatusFromValue(assignment.status);
  const manualDetailsLocked = manualAssignment && originalStatus !== "queued";
  const progressChangeSelected = manualAssignment && form.status !== originalStatus;
  const destinationLocked =
    originalStatus !== "queued" ||
    progressChangeSelected ||
    Boolean(
      assignment.execution_ref?.task_id ||
      assignment.execution_ref?.run_id ||
      assignment.execution_ref?.chat_session_id ||
      assignment.execution_ref?.message_id ||
      assignment.execution_ref?.context_snapshot_id ||
      assignment.started_at ||
      assignment.completed_at,
    );
  const statusOptions = manualAssignment
    ? manualAssignmentStatusOptions(originalStatus)
    : ASSIGNMENT_STATUSES;
  const destructiveStatusChange =
    manualAssignment &&
    form.status !== originalStatus &&
    (form.status === "failed" || form.status === "cancelled");
  const canSubmit = !pending && valid && (!destructiveStatusChange || destructiveConfirmed);
  return (
    <Modal
      title="Edit assignment"
      onClose={onClose}
      width={560}
      footer={
        <button
          className="btn btn-primary"
          type="button"
          disabled={!canSubmit}
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
          if (canSubmit) void onSave(form);
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
        <div
          className="project-assignment-form-grid"
          style={{
            display: "grid",
            gridTemplateColumns: "1fr 1fr",
            gap: 10,
          }}
        >
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Responsibility</span>
            <select
              className="input"
              disabled={destinationLocked}
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
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Status</span>
            <select
              className="input"
              disabled={
                manualAssignment &&
                (originalStatus === "completed" ||
                  originalStatus === "failed" ||
                  originalStatus === "cancelled")
              }
              value={form.status}
              onChange={(event) => {
                const status = assignmentStatusFromValue(event.target.value);
                setDestructiveConfirmed(false);
                setForm((current) => ({
                  ...current,
                  ...(manualAssignment && status !== originalStatus
                    ? {
                        roleID: assignment.role_id,
                        driverKind: assignment.driver_kind || "manual",
                        rootID: assignment.root_id ?? "",
                      }
                    : {}),
                  status,
                }));
              }}
            >
              {statusOptions.map((status) => (
                <option key={status} value={status}>
                  {manualAssignment
                    ? manualAssignmentStatusLabel(status)
                    : assignmentStatusLabel(status)}
                </option>
              ))}
            </select>
          </label>
        </div>
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Work done by</span>
          <select
            aria-describedby={
              humanDestination ? "edit-assignment-human-destination-help" : undefined
            }
            className="input"
            disabled={destinationLocked}
            value={form.driverKind}
            onChange={(event) => {
              const driverKind = event.target.value;
              setForm((current) => ({
                ...current,
                driverKind,
              }));
            }}
          >
            {PROJECT_ASSIGNMENT_DESTINATIONS.map((destination) => (
              <option key={destination.kind} value={destination.kind}>
                {destination.label}
              </option>
            ))}
          </select>
        </label>
        {humanDestination && (
          <div id="edit-assignment-human-destination-help" style={projectWorkSubtleTextStyle}>
            {HUMAN_ASSIGNMENT_DESCRIPTION} Use the work item actions for the usual progress flow;
            Status provides explicit review, failure, and cancellation control.
            {manualDetailsLocked
              ? " Responsibility, destination, and workspace stay fixed after work starts."
              : progressChangeSelected
                ? " Progress is saved separately, so assignment detail changes were reset."
                : ""}
          </div>
        )}
        <ProjectRootSelect
          disabled={destinationLocked}
          inheritLabel={
            workItem?.root_id ? "work item workspace" : "work item or project workspace"
          }
          label="Workspace (optional)"
          project={project}
          value={form.rootID}
          onChange={(rootID) => setForm((current) => ({ ...current, rootID }))}
        />
        {destructiveStatusChange && (
          <div role="alert" style={destructiveStatusWarningStyle}>
            <strong>
              {form.status === "failed" ? "Mark this work as failed?" : "Cancel this work?"}
            </strong>
            <span>
              This closes the Human assignment and cannot be undone. Create a new assignment if work
              needs to continue.
            </span>
            <label style={destructiveStatusConfirmationStyle}>
              <input
                checked={destructiveConfirmed}
                onChange={(event) => setDestructiveConfirmed(event.target.checked)}
                type="checkbox"
              />
              I understand this closes the assignment
            </label>
          </div>
        )}
        {!humanDestination && (
          <>
            <div
              className="project-assignment-form-grid"
              style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}
            >
              <label style={projectWorkFieldStyle}>
                <span style={projectWorkFieldLabelStyle}>Task ID</span>
                <input
                  className="input"
                  value={form.taskID}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, taskID: event.target.value }))
                  }
                />
              </label>
              <label style={projectWorkFieldStyle}>
                <span style={projectWorkFieldLabelStyle}>Run ID</span>
                <input
                  className="input"
                  value={form.runID}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, runID: event.target.value }))
                  }
                />
              </label>
              <label style={projectWorkFieldStyle}>
                <span style={projectWorkFieldLabelStyle}>Chat session ID</span>
                <input
                  className="input"
                  value={form.chatSessionID}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, chatSessionID: event.target.value }))
                  }
                />
              </label>
              <label style={projectWorkFieldStyle}>
                <span style={projectWorkFieldLabelStyle}>Message ID</span>
                <input
                  className="input"
                  value={form.messageID}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, messageID: event.target.value }))
                  }
                />
              </label>
            </div>
            <label style={projectWorkFieldStyle}>
              <span style={projectWorkFieldLabelStyle}>Context snapshot ID</span>
              <input
                className="input"
                value={form.contextSnapshotID}
                onChange={(event) =>
                  setForm((current) => ({ ...current, contextSnapshotID: event.target.value }))
                }
              />
            </label>
            <div style={projectWorkSubtleTextStyle}>
              Editing assignment metadata does not mutate or cancel linked task, run, or chat
              execution.
            </div>
          </>
        )}
      </form>
    </Modal>
  );
}

function manualAssignmentStatusLabel(status: string): string {
  switch (status) {
    case "queued":
      return "Ready";
    case "running":
      return "In progress";
    case "awaiting_approval":
      return "Needs review";
    case "completed":
      return "Done";
    case "failed":
      return "Failed";
    case "cancelled":
      return "Cancelled";
    default:
      return assignmentStatusLabel(status);
  }
}

function manualAssignmentStatusOptions(
  status: (typeof ASSIGNMENT_STATUSES)[number],
): readonly (typeof ASSIGNMENT_STATUSES)[number][] {
  switch (status) {
    case "queued":
      return ["queued", "cancelled"];
    case "running":
      return ["running", "awaiting_approval", "completed", "failed", "cancelled"];
    case "awaiting_approval":
      return ["awaiting_approval", "running", "completed", "failed", "cancelled"];
    default:
      return [status];
  }
}

const destructiveStatusWarningStyle: CSSProperties = {
  display: "grid",
  gap: 7,
  padding: 10,
  border: "1px solid color-mix(in srgb, var(--red) 40%, var(--border))",
  borderRadius: 6,
  background: "color-mix(in srgb, var(--red) 7%, var(--bg1))",
  color: "var(--t1)",
  fontSize: 12,
  lineHeight: 1.45,
};

const destructiveStatusConfirmationStyle: CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 8,
  color: "var(--t1)",
  fontWeight: 600,
};

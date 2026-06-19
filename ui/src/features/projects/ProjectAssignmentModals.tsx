import { useState, type CSSProperties } from "react";

import type {
  ProjectAssignmentRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { InlineError, Modal } from "../shared/ui";
import { ProjectRootSelect } from "./ProjectRootSelect";
import { projectRootOptionLabel } from "./projectSettings";
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
  const defaultRole = roles.find((role) => role.id === "software_developer") ?? roles[0] ?? null;
  const [form, setForm] = useState<NewAssignmentForm>({
    roleID: defaultRole?.id ?? "",
    driverKind: defaultDriverForRole(defaultRole),
    rootID: "",
  });
  const valid = form.roleID.trim().length > 0;
  const selectedRole = roles.find((role) => role.id === form.roleID) ?? null;
  const selectedRoot = project.roots.find((root) => root.id === form.rootID) ?? null;
  const inheritedRootLabel = workItem?.root_id
    ? "Uses the work item root"
    : project.default_root_id
      ? "Uses the project default root"
      : "Uses the first active project root";
  const rootSummary = selectedRoot ? projectRootOptionLabel(selectedRoot) : inheritedRootLabel;
  const driverSummary = selectedRole
    ? form.driverKind || defaultDriverForRole(selectedRole)
    : "Select a role";
  return (
    <Modal
      title="Create queued assignment"
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
          {pending ? "Creating..." : "Create queued assignment"}
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
        <section style={assignmentPlanStyle} aria-label="Queued assignment plan">
          <div style={assignmentPlanHeaderStyle}>
            <div style={assignmentPlanTitleStyle}>Queued assignment</div>
            <span className="badge badge-muted">Review before start</span>
          </div>
          <div style={assignmentPlanGridStyle}>
            <div>
              <div style={assignmentPlanLabelStyle}>Role</div>
              <div style={assignmentPlanValueStyle}>{selectedRole?.name || "Select a role"}</div>
            </div>
            <div>
              <div style={assignmentPlanLabelStyle}>Driver</div>
              <div style={assignmentPlanValueStyle}>{driverSummary}</div>
            </div>
            <div>
              <div style={assignmentPlanLabelStyle}>Root</div>
              <div style={assignmentPlanValueStyle}>{rootSummary}</div>
            </div>
          </div>
        </section>
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Role</span>
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
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Driver</span>
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
        <ProjectRootSelect
          inheritLabel={workItem?.root_id ? "work item root" : "work item/project root"}
          project={project}
          value={form.rootID}
          onChange={(rootID) => setForm((current) => ({ ...current, rootID }))}
        />
        {form.driverKind === "external_agent" && (
          <div style={projectWorkSubtleTextStyle}>
            External assignment execution is recorded here but still starts from Chats.
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
  gridTemplateColumns: "repeat(3, minmax(0, 1fr))",
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
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Role</span>
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
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Status</span>
            <select
              className="input"
              value={form.status}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  status: assignmentStatusFromValue(event.target.value),
                }))
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
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Driver</span>
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
        <ProjectRootSelect
          inheritLabel={workItem?.root_id ? "work item root" : "work item/project root"}
          project={project}
          value={form.rootID}
          onChange={(rootID) => setForm((current) => ({ ...current, rootID }))}
        />
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
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
          Editing assignment metadata does not mutate or cancel linked task, run, or chat execution.
        </div>
      </form>
    </Modal>
  );
}

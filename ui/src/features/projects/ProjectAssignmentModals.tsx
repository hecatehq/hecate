import { useState } from "react";

import type {
  ProjectAssignmentRecord,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { InlineError, Modal } from "../shared/ui";
import { ProjectRootSelect } from "./ProjectRootSelect";
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

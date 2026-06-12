import { useState } from "react";

import type {
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { InlineError, Modal } from "../shared/ui";
import { ProjectRootSelect } from "./ProjectRootSelect";
import {
  WORK_ITEM_PRIORITIES,
  WORK_ITEM_STATUSES,
  type EditWorkItemForm,
  type NewWorkItemForm,
  workItemPriorityFromValue,
  workItemStatusFromValue,
} from "./projectWorkForms";
import { projectWorkFieldLabelStyle, projectWorkFieldStyle } from "./projectWorkModalStyles";

type NewWorkItemModalProps = {
  error: string;
  pending: boolean;
  project: ProjectRecord;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onCreate: (form: NewWorkItemForm) => void | Promise<void>;
};

export function NewWorkItemModal({
  error,
  pending,
  project,
  roles,
  onClose,
  onCreate,
}: NewWorkItemModalProps) {
  const [form, setForm] = useState<NewWorkItemForm>({
    title: "",
    brief: "",
    priority: "normal",
    ownerRoleID: roles.find((role) => role.id === "software_developer")?.id ?? roles[0]?.id ?? "",
    rootID: "",
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
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Title</span>
          <input
            className="input"
            autoFocus
            value={form.title}
            onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))}
            placeholder="Implement project cockpit"
          />
        </label>
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Brief</span>
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
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Priority</span>
            <select
              className="input"
              value={form.priority}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  priority: workItemPriorityFromValue(event.target.value),
                }))
              }
            >
              <option value="low">low</option>
              <option value="normal">normal</option>
              <option value="high">high</option>
              <option value="urgent">urgent</option>
            </select>
          </label>
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Owner role</span>
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
        <ProjectRootSelect
          inheritLabel="project default root"
          project={project}
          value={form.rootID}
          onChange={(rootID) => setForm((current) => ({ ...current, rootID }))}
        />
      </form>
    </Modal>
  );
}

type EditWorkItemModalProps = {
  error: string;
  item: ProjectWorkItemRecord;
  pending: boolean;
  project: ProjectRecord;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onSave: (form: EditWorkItemForm) => void | Promise<void>;
};

export function EditWorkItemModal({
  error,
  item,
  pending,
  project,
  roles,
  onClose,
  onSave,
}: EditWorkItemModalProps) {
  const [form, setForm] = useState<EditWorkItemForm>({
    id: item.id,
    title: item.title,
    brief: item.brief ?? "",
    status: workItemStatusFromValue(item.status),
    priority: workItemPriorityFromValue(item.priority),
    ownerRoleID: item.owner_role_id ?? "",
    rootID: item.root_id ?? "",
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
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Title</span>
          <input
            className="input"
            autoFocus
            value={form.title}
            onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))}
          />
        </label>
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Brief</span>
          <textarea
            className="input"
            value={form.brief}
            onChange={(event) => setForm((current) => ({ ...current, brief: event.target.value }))}
            rows={5}
            style={{ resize: "vertical", minHeight: 110 }}
          />
        </label>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Status</span>
            <select
              className="input"
              value={form.status}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  status: workItemStatusFromValue(event.target.value),
                }))
              }
            >
              {WORK_ITEM_STATUSES.map((status) => (
                <option key={status} value={status}>
                  {status}
                </option>
              ))}
            </select>
          </label>
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Priority</span>
            <select
              className="input"
              value={form.priority}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  priority: workItemPriorityFromValue(event.target.value),
                }))
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
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Owner role</span>
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
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Reviewer roles</span>
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
        <ProjectRootSelect
          inheritLabel="project default root"
          project={project}
          value={form.rootID}
          onChange={(rootID) => setForm((current) => ({ ...current, rootID }))}
        />
      </form>
    </Modal>
  );
}

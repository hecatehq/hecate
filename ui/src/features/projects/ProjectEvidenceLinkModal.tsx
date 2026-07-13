import { useState } from "react";

import type { ProjectAssignmentRecord, ProjectWorkRoleRecord } from "../../types/project";
import { InlineError, Modal } from "../shared/ui";
import { assignmentStatusLabel, projectRoleLabel } from "./projectDisplay";
import type { EvidenceLinkForm } from "./projectWorkForms";
import { projectWorkFieldLabelStyle, projectWorkFieldStyle } from "./projectWorkModalStyles";
import { shortID } from "./projectUtils";

type ProjectEvidenceLinkModalProps = {
  assignments: ProjectAssignmentRecord[];
  error: string;
  initialAssignmentID?: string;
  pending: boolean;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onSave: (form: EvidenceLinkForm) => void | Promise<void>;
};

export function ProjectEvidenceLinkModal({
  assignments,
  error,
  initialAssignmentID = "",
  pending,
  roles,
  onClose,
  onSave,
}: ProjectEvidenceLinkModalProps) {
  const assignmentID = assignments.some((assignment) => assignment.id === initialAssignmentID)
    ? initialAssignmentID
    : "";
  const [form, setForm] = useState<EvidenceLinkForm>({
    assignmentID,
    title: "",
    sourceKind: "external",
    url: "",
    externalID: "",
    provider: "",
    trustLabel: "operator_provided",
    summary: "",
  });
  const valid =
    form.title.trim().length > 0 &&
    form.summary.trim().length > 0 &&
    (form.url.trim().length > 0 || form.externalID.trim().length > 0);
  return (
    <Modal
      title="Record evidence"
      dismissible={!pending}
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
          {pending ? "Recording..." : "Record evidence"}
        </button>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (valid && !pending) void onSave(form);
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
            placeholder="Source document, ticket, deployment, design file, or note"
          />
        </label>
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>URL</span>
          <input
            className="input"
            type="url"
            value={form.url}
            onChange={(event) => setForm((current) => ({ ...current, url: event.target.value }))}
            placeholder="https://..."
          />
        </label>
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>External id</span>
          <input
            className="input"
            value={form.externalID}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                externalID: event.target.value,
              }))
            }
            placeholder="DOC-12, release-2026-06-13, PR 399"
          />
        </label>
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Assignment</span>
          <select
            className="input"
            value={form.assignmentID}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                assignmentID: event.target.value,
              }))
            }
          >
            <option value="">Work item evidence</option>
            {assignments.map((assignment) => (
              <option key={assignment.id} value={assignment.id}>
                {projectRoleLabel(assignment.role_id, roles)} ·{" "}
                {assignmentStatusLabel(assignment.status)} · {shortID(assignment.id)}
              </option>
            ))}
          </select>
        </label>
        <details className="project-work-advanced-fields">
          <summary>Advanced source details</summary>
          <div
            className="project-work-modal-grid"
            style={{
              display: "grid",
              gridTemplateColumns: fieldGridColumns,
              gap: 10,
              marginTop: 10,
            }}
          >
            <label style={projectWorkFieldStyle}>
              <span style={projectWorkFieldLabelStyle}>Source kind</span>
              <input
                className="input"
                value={form.sourceKind}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    sourceKind: event.target.value,
                  }))
                }
                placeholder="external"
              />
            </label>
            <label style={projectWorkFieldStyle}>
              <span style={projectWorkFieldLabelStyle}>Provider</span>
              <input
                className="input"
                value={form.provider}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    provider: event.target.value,
                  }))
                }
                placeholder="docs, figma, jira, local, github"
              />
            </label>
            <label style={projectWorkFieldStyle}>
              <span style={projectWorkFieldLabelStyle}>Trust label</span>
              <input
                className="input"
                value={form.trustLabel}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    trustLabel: event.target.value,
                  }))
                }
                placeholder="operator_provided"
              />
            </label>
          </div>
        </details>
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Summary</span>
          <textarea
            className="input"
            value={form.summary}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                summary: event.target.value,
              }))
            }
            rows={4}
            style={{ resize: "vertical", minHeight: 96 }}
          />
        </label>
      </form>
    </Modal>
  );
}

const fieldGridColumns = "repeat(auto-fit, minmax(180px, 1fr))";

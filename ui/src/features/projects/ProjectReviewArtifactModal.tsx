import { useState } from "react";

import type { ProjectAssignmentRecord, ProjectWorkRoleRecord } from "../../types/project";
import { InlineError, Modal } from "../shared/ui";
import { projectRoleLabel } from "./projectDisplay";
import {
  REVIEW_RISKS,
  REVIEW_VERDICTS,
  reviewRiskFromValue,
  reviewVerdictFromValue,
  type ReviewArtifactForm,
} from "./projectWorkForms";
import { projectWorkFieldLabelStyle, projectWorkFieldStyle } from "./projectWorkModalStyles";
import { shortID } from "./projectUtils";

type ProjectReviewArtifactModalProps = {
  assignments: ProjectAssignmentRecord[];
  draft: ReviewArtifactForm;
  error: string;
  pending: boolean;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onSave: (form: ReviewArtifactForm) => void | Promise<void>;
};

export function ProjectReviewArtifactModal({
  assignments,
  draft,
  error,
  pending,
  roles,
  onClose,
  onSave,
}: ProjectReviewArtifactModalProps) {
  const [form, setForm] = useState<ReviewArtifactForm>(draft);
  const [verdictSelected, setVerdictSelected] = useState(false);
  const reviewAssignment = assignments.find((assignment) => assignment.id === form.assignmentID);
  const reviewedAssignment = assignments.find(
    (assignment) => assignment.id === form.reviewedAssignmentID,
  );
  const reviewedAssignmentID = reviewedAssignment?.id ?? form.reviewedAssignmentID.trim();
  const reviewAssignmentID = reviewAssignment?.id ?? form.assignmentID.trim();
  const reviewedContext = reviewedAssignment
    ? `Reviewing ${projectRoleLabel(reviewedAssignment.role_id, roles)} assignment ${shortID(reviewedAssignment.id)}`
    : reviewedAssignmentID
      ? `Reviewing source assignment ${shortID(reviewedAssignmentID)}`
      : "Reviewing the selected work item";
  const reviewAssignmentContext = reviewAssignment
    ? `Review assignment ${projectRoleLabel(reviewAssignment.role_id, roles)} · ${shortID(reviewAssignment.id)}`
    : reviewAssignmentID
      ? `Review assignment ${shortID(reviewAssignmentID)}`
      : "Review assignment not selected";
  const valid = verdictSelected && form.title.trim().length > 0 && form.summary.trim().length > 0;
  return (
    <Modal
      title="Record review"
      dismissible={!pending}
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
          {pending ? "Saving..." : "Save review"}
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
        <div
          aria-label="Review context"
          className="project-work-modal-context"
          role="region"
          style={{ display: "grid", gap: 4 }}
        >
          <div title={reviewedAssignmentID || undefined}>{reviewedContext}</div>
          <div title={reviewAssignmentID || undefined}>{reviewAssignmentContext}</div>
        </div>
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Title</span>
          <input
            className="input"
            autoFocus
            value={form.title}
            onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))}
          />
        </label>
        <div
          className="project-work-modal-grid"
          style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}
        >
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Verdict</span>
            <select
              className="input"
              value={verdictSelected ? form.verdict : ""}
              onChange={(event) => {
                setVerdictSelected(true);
                setForm((current) => ({
                  ...current,
                  verdict: reviewVerdictFromValue(event.target.value),
                }));
              }}
            >
              <option value="" disabled>
                Choose a verdict
              </option>
              {REVIEW_VERDICTS.map((verdict) => (
                <option key={verdict} value={verdict}>
                  {verdict.replaceAll("_", " ")}
                </option>
              ))}
            </select>
          </label>
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Risk</span>
            <select
              className="input"
              value={form.risk}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  risk: reviewRiskFromValue(event.target.value),
                }))
              }
            >
              {REVIEW_RISKS.map((risk) => (
                <option key={risk} value={risk}>
                  {risk}
                </option>
              ))}
            </select>
          </label>
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Review assignment</span>
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
              <option value="">No review assignment</option>
              {assignments.map((assignment) => (
                <option key={assignment.id} value={assignment.id}>
                  {projectRoleLabel(assignment.role_id, roles)} · {shortID(assignment.id)}
                </option>
              ))}
            </select>
          </label>
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Author role</span>
            <select
              className="input"
              value={form.authorRoleID}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  authorRoleID: event.target.value,
                }))
              }
            >
              <option value="">No author role</option>
              {roles.map((role) => (
                <option key={role.id} value={role.id}>
                  {role.name}
                </option>
              ))}
            </select>
          </label>
        </div>
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
            style={{ resize: "vertical", minHeight: 90 }}
          />
        </label>
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Verification</span>
          <textarea
            className="input"
            value={form.verification}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                verification: event.target.value,
              }))
            }
            rows={3}
            style={{ resize: "vertical", minHeight: 76 }}
          />
        </label>
        <label style={projectWorkFieldStyle}>
          <span style={projectWorkFieldLabelStyle}>Follow-up</span>
          <textarea
            className="input"
            value={form.followUp}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                followUp: event.target.value,
              }))
            }
            rows={3}
            style={{ resize: "vertical", minHeight: 76 }}
          />
        </label>
      </form>
    </Modal>
  );
}

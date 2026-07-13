import { useState } from "react";

import type {
  ProjectAssignmentRecord,
  ProjectHandoffRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { InlineError, Modal } from "../shared/ui";
import { projectRoleLabel } from "./projectDisplay";
import { handoffFormFromRecord, type HandoffForm } from "./projectWorkForms";
import { projectWorkFieldLabelStyle, projectWorkFieldStyle } from "./projectWorkModalStyles";
import { shortID } from "./projectUtils";

type ProjectHandoffModalProps = {
  assignments: ProjectAssignmentRecord[];
  draft?: HandoffForm | null;
  error: string;
  handoff: ProjectHandoffRecord | null;
  pending: boolean;
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onSave: (form: HandoffForm) => void | Promise<void>;
};

export function ProjectHandoffModal({
  assignments,
  draft,
  error,
  handoff,
  pending,
  roles,
  onClose,
  onSave,
}: ProjectHandoffModalProps) {
  const [form, setForm] = useState<HandoffForm>(() => draft ?? handoffFormFromRecord(handoff));
  const valid =
    form.title.trim().length > 0 &&
    form.summary.trim().length > 0 &&
    form.recommendedNextAction.trim().length > 0;
  return (
    <Modal
      title={handoff ? "Edit handoff" : "New handoff"}
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
          {pending ? "Saving…" : "Save handoff"}
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
            placeholder="QA review handoff"
          />
        </label>
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
          <span style={projectWorkFieldLabelStyle}>Recommended next action</span>
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
        <div
          className="project-work-modal-grid"
          style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}
        >
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Source assignment</span>
            <select
              className="input"
              value={form.sourceAssignmentID}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  sourceAssignmentID: event.target.value,
                }))
              }
            >
              <option value="">No source assignment</option>
              {assignments.map((assignment) => (
                <option key={assignment.id} value={assignment.id}>
                  {shortID(assignment.id)} · {projectRoleLabel(assignment.role_id, roles)}
                </option>
              ))}
            </select>
          </label>
          <label style={projectWorkFieldStyle}>
            <span style={projectWorkFieldLabelStyle}>Target role</span>
            <select
              className="input"
              value={form.targetRoleID}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  targetRoleID: event.target.value,
                }))
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
        </div>
        <details className="project-work-advanced-fields">
          <summary>Advanced links and provenance</summary>
          <div style={{ display: "grid", gap: 10, marginTop: 10 }}>
            <label style={projectWorkFieldStyle}>
              <span style={projectWorkFieldLabelStyle}>Target assignment</span>
              <select
                className="input"
                value={form.targetAssignmentID}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    targetAssignmentID: event.target.value,
                  }))
                }
              >
                <option value="">No target assignment</option>
                {assignments.map((assignment) => (
                  <option key={assignment.id} value={assignment.id}>
                    {shortID(assignment.id)} · {projectRoleLabel(assignment.role_id, roles)}
                  </option>
                ))}
              </select>
            </label>
            <div
              className="project-work-modal-grid"
              style={{
                display: "grid",
                gridTemplateColumns: "1fr 1fr",
                gap: 10,
              }}
            >
              <label style={projectWorkFieldStyle}>
                <span style={projectWorkFieldLabelStyle}>Source run</span>
                <input
                  className="input"
                  value={form.sourceRunID}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      sourceRunID: event.target.value,
                    }))
                  }
                  placeholder="run_..."
                />
              </label>
              <label style={projectWorkFieldStyle}>
                <span style={projectWorkFieldLabelStyle}>Source chat</span>
                <input
                  className="input"
                  value={form.sourceChatSessionID}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      sourceChatSessionID: event.target.value,
                    }))
                  }
                  placeholder="chat_..."
                />
              </label>
              <label style={projectWorkFieldStyle}>
                <span style={projectWorkFieldLabelStyle}>Source message</span>
                <input
                  className="input"
                  value={form.sourceMessageID}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      sourceMessageID: event.target.value,
                    }))
                  }
                  placeholder="msg_..."
                />
              </label>
              <label style={projectWorkFieldStyle}>
                <span style={projectWorkFieldLabelStyle}>Artifact IDs</span>
                <input
                  className="input"
                  value={form.linkedArtifactIDs}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      linkedArtifactIDs: event.target.value,
                    }))
                  }
                  placeholder="art_1, art_2"
                />
              </label>
              <label style={projectWorkFieldStyle}>
                <span style={projectWorkFieldLabelStyle}>Memory IDs</span>
                <input
                  className="input"
                  value={form.linkedMemoryIDs}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      linkedMemoryIDs: event.target.value,
                    }))
                  }
                  placeholder="mem_1"
                />
              </label>
              <label style={projectWorkFieldStyle}>
                <span style={projectWorkFieldLabelStyle}>Context refs</span>
                <input
                  className="input"
                  value={form.contextRefs}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      contextRefs: event.target.value,
                    }))
                  }
                  placeholder="ctx_1, task/run/context"
                />
              </label>
              <label style={projectWorkFieldStyle}>
                <span style={projectWorkFieldLabelStyle}>Provenance</span>
                <input
                  className="input"
                  value={form.provenanceKind}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      provenanceKind: event.target.value,
                    }))
                  }
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
                />
              </label>
            </div>
          </div>
        </details>
      </form>
    </Modal>
  );
}

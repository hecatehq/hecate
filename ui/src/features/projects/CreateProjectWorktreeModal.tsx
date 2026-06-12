import { useState, type CSSProperties } from "react";

import type { ProjectRecord } from "../../types/project";
import { InlineError, Modal } from "../shared/ui";
import { projectRootOptionLabel, type CreateWorktreeForm } from "./projectSettings";

export function CreateProjectWorktreeModal({
  error,
  pending,
  project,
  onClose,
  onCreate,
}: {
  error: string;
  pending: boolean;
  project: ProjectRecord;
  onClose: () => void;
  onCreate: (form: CreateWorktreeForm) => void | Promise<void>;
}) {
  const defaultBaseRootID =
    project.default_root_id ||
    project.roots.find((root) => root.active)?.id ||
    project.roots[0]?.id ||
    "";
  const [form, setForm] = useState<CreateWorktreeForm>({
    baseRootID: defaultBaseRootID,
    branch: "",
    startPoint: "",
    path: "",
    active: true,
    setDefault: false,
  });
  const valid = project.roots.length > 0 && form.branch.trim().length > 0;
  return (
    <Modal
      title="Create project worktree"
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
          {pending ? "Creating…" : "Create worktree"}
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
        {project.roots.length === 0 && <InlineError message="Add a project root first." />}
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Base root</span>
          <select
            aria-label="Base root"
            className="input"
            value={form.baseRootID}
            onChange={(event) =>
              setForm((current) => ({ ...current, baseRootID: event.target.value }))
            }
            style={{ fontFamily: "var(--font-mono)", fontSize: 12, minHeight: 36 }}
          >
            {project.roots.map((root) => (
              <option key={root.id || root.path} value={root.id}>
                {projectRootOptionLabel(root)}
              </option>
            ))}
          </select>
        </label>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Branch</span>
          <input
            className="input"
            autoFocus
            value={form.branch}
            onChange={(event) => setForm((current) => ({ ...current, branch: event.target.value }))}
            placeholder="feature/project-worktrees"
          />
        </label>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Start point</span>
            <input
              className="input"
              value={form.startPoint}
              onChange={(event) =>
                setForm((current) => ({ ...current, startPoint: event.target.value }))
              }
              placeholder="origin/main"
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Path</span>
            <input
              className="input"
              value={form.path}
              onChange={(event) => setForm((current) => ({ ...current, path: event.target.value }))}
              placeholder=".worktrees/feature-project-worktrees"
            />
          </label>
        </div>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={{ ...fieldStyle, display: "flex", flexDirection: "row", gap: 8 }}>
            <input
              type="checkbox"
              checked={form.active}
              onChange={(event) =>
                setForm((current) => ({ ...current, active: event.target.checked }))
              }
            />
            <span style={fieldLabelStyle}>Active root</span>
          </label>
          <label style={{ ...fieldStyle, display: "flex", flexDirection: "row", gap: 8 }}>
            <input
              type="checkbox"
              checked={form.setDefault}
              onChange={(event) =>
                setForm((current) => ({ ...current, setDefault: event.target.checked }))
              }
            />
            <span style={fieldLabelStyle}>Make default root</span>
          </label>
        </div>
      </form>
    </Modal>
  );
}

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

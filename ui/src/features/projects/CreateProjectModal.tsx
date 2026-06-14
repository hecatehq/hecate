import { useState, type CSSProperties } from "react";

import { Icon, Icons, InlineError, Modal } from "../shared/ui";
import type { CreateProjectForm } from "./projectSettings";
import { projectNameFromPath } from "./projectUtils";

type WorkspaceChoice = {
  path: string;
  branch?: string;
};

type CreateProjectModalProps = {
  error: string;
  pending: boolean;
  onChooseWorkspace: () => Promise<WorkspaceChoice | null>;
  onClose: () => void;
  onSave: (form: CreateProjectForm) => void | Promise<void>;
};

export function CreateProjectModal({
  error,
  pending,
  onChooseWorkspace,
  onClose,
  onSave,
}: CreateProjectModalProps) {
  const [form, setForm] = useState<CreateProjectForm>({
    name: "",
    description: "",
    rootPath: "",
    rootGitBranch: "",
  });
  const [choosingWorkspace, setChoosingWorkspace] = useState(false);
  const [chooseError, setChooseError] = useState("");
  const valid = form.name.trim().length > 0;

  async function handleChooseWorkspace() {
    setChoosingWorkspace(true);
    setChooseError("");
    try {
      const choice = await onChooseWorkspace();
      if (!choice) return;
      setForm((current) => ({
        ...current,
        name: current.name.trim() || projectNameFromPath(choice.path),
        rootPath: choice.path,
        rootGitBranch: choice.branch ?? "",
      }));
    } catch (err) {
      setChooseError(err instanceof Error ? err.message : "Failed to choose workspace folder.");
    } finally {
      setChoosingWorkspace(false);
    }
  }

  return (
    <Modal
      title="Create project"
      onClose={onClose}
      width={560}
      footer={
        <button
          className="btn btn-primary"
          disabled={pending || !valid}
          onClick={() => void onSave(form)}
          style={{ justifyContent: "center", width: "100%" }}
          type="button"
        >
          {pending ? "Creating..." : "Create project"}
        </button>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (valid) void onSave(form);
        }}
        style={{ display: "grid", gap: 13 }}
      >
        {error && <InlineError message={error} />}
        {chooseError && <InlineError message={chooseError} />}
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Name</span>
          <input
            autoFocus
            className="input"
            onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))}
            placeholder="Research brief, launch plan, Hecate"
            value={form.name}
          />
        </label>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Purpose</span>
          <textarea
            className="input"
            onChange={(event) =>
              setForm((current) => ({ ...current, description: event.target.value }))
            }
            placeholder="What this project coordinates. Optional, but useful for non-code work."
            rows={3}
            style={{ minHeight: 80, resize: "vertical" }}
            value={form.description}
          />
        </label>
        <section style={workspaceSectionStyle}>
          <div style={{ minWidth: 0 }}>
            <div style={fieldLabelStyle}>Workspace source</div>
            <div style={hintStyle}>
              Optional. Leave empty for planning, research, writing, ops, or design projects that do
              not start from a local folder.
            </div>
          </div>
          <button
            className="btn btn-ghost btn-sm"
            disabled={choosingWorkspace}
            onClick={() => void handleChooseWorkspace()}
            type="button"
          >
            <Icon d={Icons.folder} size={13} />
            {choosingWorkspace ? "Choosing..." : "Choose folder"}
          </button>
        </section>
        <div style={{ display: "grid", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Folder path</span>
            <input
              className="input"
              onChange={(event) =>
                setForm((current) => ({ ...current, rootPath: event.target.value }))
              }
              placeholder="/Users/alice/projects/example"
              value={form.rootPath}
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Git branch</span>
            <input
              className="input"
              onChange={(event) =>
                setForm((current) => ({ ...current, rootGitBranch: event.target.value }))
              }
              placeholder="Optional; filled when detected"
              value={form.rootGitBranch}
            />
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

const hintStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 12,
  lineHeight: 1.45,
  marginTop: 4,
};

const workspaceSectionStyle: CSSProperties = {
  alignItems: "flex-start",
  borderTop: "1px solid var(--border)",
  display: "flex",
  gap: 12,
  justifyContent: "space-between",
  paddingTop: 13,
};

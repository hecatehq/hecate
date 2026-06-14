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
  const [workspaceOpen, setWorkspaceOpen] = useState(false);
  const [choosingWorkspace, setChoosingWorkspace] = useState(false);
  const [chooseError, setChooseError] = useState("");
  const valid = form.name.trim().length > 0;
  const hasWorkspace = form.rootPath.trim().length > 0;

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
      setWorkspaceOpen(true);
    } catch (err) {
      setChooseError(err instanceof Error ? err.message : "Failed to choose workspace folder.");
    } finally {
      setChoosingWorkspace(false);
    }
  }

  function clearWorkspace() {
    setForm((current) => ({ ...current, rootPath: "", rootGitBranch: "" }));
    setWorkspaceOpen(false);
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
        style={formStyle}
      >
        {error && <InlineError message={error} />}
        {chooseError && <InlineError message={chooseError} />}
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Name</span>
          <input
            autoFocus
            className="input"
            onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))}
            placeholder="Project name"
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
            placeholder="What should Hecate coordinate?"
            rows={3}
            style={purposeInputStyle}
            value={form.description}
          />
        </label>
        <section style={workspaceSectionStyle}>
          <div style={workspaceHeaderStyle}>
            <div style={workspaceTitleStyle}>
              <Icon d={Icons.folder} size={15} />
              <div style={{ minWidth: 0 }}>
                <div style={fieldLabelStyle}>Local folder</div>
                <div style={hintStyle}>
                  Optional. Add files for code-backed work, or skip for planning and research.
                </div>
              </div>
            </div>
            <button
              className="btn btn-ghost btn-sm"
              disabled={choosingWorkspace}
              onClick={() => void handleChooseWorkspace()}
              type="button"
            >
              <Icon d={Icons.folder} size={13} />
              {choosingWorkspace ? "Choosing..." : hasWorkspace ? "Change folder" : "Attach folder"}
            </button>
          </div>
          {!hasWorkspace && !workspaceOpen && (
            <button
              className="btn btn-ghost btn-sm"
              onClick={() => setWorkspaceOpen(true)}
              style={manualPathButtonStyle}
              type="button"
            >
              Enter path manually
            </button>
          )}
          {(workspaceOpen || hasWorkspace) && (
            <div style={workspaceFieldsStyle}>
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
              <div style={workspaceBranchRowStyle}>
                <label style={fieldStyle}>
                  <span style={fieldLabelStyle}>Git branch</span>
                  <input
                    className="input"
                    onChange={(event) =>
                      setForm((current) => ({ ...current, rootGitBranch: event.target.value }))
                    }
                    placeholder="Optional; detected when available"
                    value={form.rootGitBranch}
                  />
                </label>
                {hasWorkspace && (
                  <button
                    className="btn btn-ghost btn-sm"
                    onClick={clearWorkspace}
                    style={removeWorkspaceButtonStyle}
                    type="button"
                  >
                    Remove
                  </button>
                )}
              </div>
            </div>
          )}
        </section>
      </form>
    </Modal>
  );
}

const formStyle: CSSProperties = {
  display: "grid",
  gap: 13,
};

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
  marginTop: 3,
};

const purposeInputStyle: CSSProperties = {
  minHeight: 74,
  resize: "vertical",
};

const workspaceSectionStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  display: "grid",
  gap: 10,
  marginTop: 2,
  paddingTop: 12,
};

const workspaceHeaderStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 10,
  justifyContent: "space-between",
  minWidth: 0,
};

const workspaceTitleStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "flex",
  flex: "1 1 260px",
  gap: 8,
  minWidth: 0,
};

const manualPathButtonStyle: CSSProperties = {
  justifySelf: "start",
};

const workspaceFieldsStyle: CSSProperties = {
  display: "grid",
  gap: 10,
};

const workspaceBranchRowStyle: CSSProperties = {
  alignItems: "end",
  display: "grid",
  gap: 8,
  gridTemplateColumns: "minmax(0, 1fr) auto",
};

const removeWorkspaceButtonStyle: CSSProperties = {
  marginBottom: 1,
};

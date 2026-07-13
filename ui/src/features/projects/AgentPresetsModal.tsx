import { useState } from "react";

import type { AgentPresetRecord } from "../../types/agent-preset";
import type { ProjectRecord, ProjectSkillRecord, ProjectWorkRoleRecord } from "../../types/project";
import { ConfirmModal, Icon, Icons, InlineError, Modal } from "../shared/ui";
import { ProjectSkillPicker } from "./ProjectSkillPicker";
import {
  emptyAgentPresetForm,
  presetFormFromRecord,
  presetReferenceSummary,
  type AgentPresetForm,
} from "./projectPresetsRoles";
import {
  presetRoleCheckboxLabelStyle,
  presetRoleFieldLabelStyle,
  presetRoleFieldStyle,
  presetRoleSubtleTextStyle,
} from "./projectPresetRoleStyles";

const AGENT_PRESET_SURFACES = ["any", "hecate_chat", "hecate_task", "external_agent"];
const AGENT_PRESET_APPROVAL_POLICIES = ["inherit", "require", "block", "allow"];
const AGENT_PRESET_MEMORY_POLICIES = ["inherit", "include", "visible_only", "exclude"];
const AGENT_PRESET_CONTEXT_POLICIES = ["inherit", "include_enabled", "visible_only", "exclude"];

type AgentPresetsModalProps = {
  error: string;
  pending: boolean;
  presets: AgentPresetRecord[];
  project: ProjectRecord;
  projectSkills: ProjectSkillRecord[];
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onCreate: (
    form: AgentPresetForm,
  ) => AgentPresetRecord | undefined | Promise<AgentPresetRecord | undefined>;
  onDelete: (preset: AgentPresetRecord) => boolean | Promise<boolean>;
  onUpdate: (
    presetID: string,
    form: AgentPresetForm,
  ) => AgentPresetRecord | undefined | Promise<AgentPresetRecord | undefined>;
};

export function AgentPresetsModal({
  error,
  pending,
  presets,
  project,
  projectSkills,
  roles,
  onClose,
  onCreate,
  onDelete,
  onUpdate,
}: AgentPresetsModalProps) {
  const [selectedPresetID, setSelectedPresetID] = useState(presets[0]?.id ?? "new");
  const selectedPreset = presets.find((preset) => preset.id === selectedPresetID) ?? null;
  const editingNew = selectedPresetID === "new";
  const editingBuiltIn = Boolean(selectedPreset?.built_in);
  const [deletePreset, setDeletePreset] = useState<AgentPresetRecord | null>(null);
  const [form, setForm] = useState<AgentPresetForm>(() =>
    selectedPreset ? presetFormFromRecord(selectedPreset) : emptyAgentPresetForm(),
  );

  function selectPreset(presetID: string) {
    setSelectedPresetID(presetID);
    const preset = presets.find((item) => item.id === presetID) ?? null;
    setForm(preset ? presetFormFromRecord(preset) : emptyAgentPresetForm());
  }

  function selectPresetRecord(preset: AgentPresetRecord) {
    setSelectedPresetID(preset.id);
    setForm(presetFormFromRecord(preset));
  }

  const canSave = !editingBuiltIn && form.name.trim().length > 0;
  const submit = async () => {
    if (pending || !canSave) return;
    if (editingNew) {
      const preset = await onCreate(form);
      if (preset) selectPresetRecord(preset);
      return;
    }
    const preset = await onUpdate(selectedPresetID, form);
    if (preset) selectPresetRecord(preset);
  };

  async function deleteSelectedPreset(preset: AgentPresetRecord) {
    const deleted = await onDelete(preset);
    if (!deleted) return;
    setDeletePreset(null);
    const nextPreset = presets.find((item) => item.id !== preset.id) ?? null;
    if (nextPreset) {
      selectPresetRecord(nextPreset);
      return;
    }
    setSelectedPresetID("new");
    setForm(emptyAgentPresetForm());
  }

  return (
    <>
      <Modal
        title="Agent presets"
        onClose={onClose}
        dismissible={!pending}
        width={840}
        footer={
          <div style={{ display: "flex", gap: 8, width: "100%" }}>
            {editingBuiltIn && (
              <span className="badge badge-muted" style={{ alignSelf: "center" }}>
                Built-in preset
              </span>
            )}
            {selectedPreset && !editingNew && !editingBuiltIn && (
              <button
                className="btn btn-ghost"
                type="button"
                disabled={pending}
                onClick={() => setDeletePreset(selectedPreset)}
                style={{ color: "var(--red)" }}
              >
                Delete preset
              </button>
            )}
            {!editingBuiltIn && (
              <button
                className="btn btn-primary"
                type="button"
                disabled={pending || !canSave}
                onClick={() => void submit()}
                style={{ marginLeft: "auto" }}
              >
                {pending ? "Saving…" : editingNew ? "Create preset" : "Save preset"}
              </button>
            )}
          </div>
        }
      >
        <div
          className="agent-presets-modal-grid"
          style={{ display: "grid", gridTemplateColumns: "220px 1fr", gap: 14, minHeight: 470 }}
        >
          <div
            className="agent-presets-modal-list"
            style={{
              borderRight: "1px solid var(--border)",
              paddingRight: 10,
              display: "grid",
              alignContent: "start",
              gap: 6,
            }}
          >
            <button
              className={
                selectedPresetID === "new" ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"
              }
              type="button"
              aria-pressed={selectedPresetID === "new"}
              disabled={pending}
              onClick={() => selectPreset("new")}
              style={{ justifyContent: "flex-start" }}
            >
              <Icon d={Icons.plus} size={12} />
              New preset
            </button>
            {presets.map((preset) => (
              <button
                key={preset.id}
                className={
                  selectedPresetID === preset.id ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"
                }
                type="button"
                aria-pressed={selectedPresetID === preset.id}
                disabled={pending}
                onClick={() => selectPreset(preset.id)}
                style={{ justifyContent: "flex-start", minWidth: 0 }}
              >
                <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>
                  {preset.name || preset.id}
                </span>
                {preset.built_in && <span className="badge badge-muted">built-in</span>}
              </button>
            ))}
          </div>
          <form
            aria-busy={pending}
            onSubmit={(event) => {
              event.preventDefault();
              if (!pending) void submit();
            }}
            style={{ display: "grid", gap: 12, alignContent: "start" }}
          >
            {error && <InlineError message={error} />}
            <div
              className="agent-presets-form-grid"
              style={{ display: "grid", gridTemplateColumns: "160px 1fr", gap: 10 }}
            >
              <label style={presetRoleFieldStyle}>
                <span style={presetRoleFieldLabelStyle}>Preset id</span>
                <input
                  className="input"
                  value={form.id}
                  disabled={pending || !editingNew}
                  placeholder="implementation"
                  onChange={(event) =>
                    setForm((current) => ({ ...current, id: event.target.value }))
                  }
                />
              </label>
              <label style={presetRoleFieldStyle}>
                <span style={presetRoleFieldLabelStyle}>Name</span>
                <input
                  className="input"
                  value={form.name}
                  autoFocus={editingNew}
                  disabled={pending || editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, name: event.target.value }))
                  }
                />
              </label>
            </div>
            <label style={presetRoleFieldStyle}>
              <span style={presetRoleFieldLabelStyle}>Description</span>
              <textarea
                className="input"
                value={form.description}
                disabled={pending || editingBuiltIn}
                rows={2}
                onChange={(event) =>
                  setForm((current) => ({ ...current, description: event.target.value }))
                }
              />
            </label>
            <label style={presetRoleFieldStyle}>
              <span style={presetRoleFieldLabelStyle}>Instructions</span>
              <textarea
                className="input"
                value={form.instructions}
                disabled={pending || editingBuiltIn}
                rows={5}
                onChange={(event) =>
                  setForm((current) => ({ ...current, instructions: event.target.value }))
                }
              />
            </label>
            <div
              className="agent-presets-form-grid"
              style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}
            >
              <label style={presetRoleFieldStyle}>
                <span style={presetRoleFieldLabelStyle}>Surface</span>
                <select
                  className="input"
                  value={form.surface}
                  disabled={pending || editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, surface: event.target.value }))
                  }
                >
                  {AGENT_PRESET_SURFACES.map((surface) => (
                    <option key={surface} value={surface}>
                      {surface}
                    </option>
                  ))}
                </select>
              </label>
              <label style={presetRoleFieldStyle}>
                <span style={presetRoleFieldLabelStyle}>Runtime profile</span>
                <input
                  className="input"
                  value={form.executionProfile}
                  disabled={pending || editingBuiltIn}
                  placeholder="implementation"
                  onChange={(event) =>
                    setForm((current) => ({ ...current, executionProfile: event.target.value }))
                  }
                />
              </label>
              <label style={presetRoleFieldStyle}>
                <span style={presetRoleFieldLabelStyle}>Provider hint</span>
                <input
                  className="input"
                  value={form.providerHint}
                  disabled={pending || editingBuiltIn}
                  placeholder="ollama"
                  onChange={(event) =>
                    setForm((current) => ({ ...current, providerHint: event.target.value }))
                  }
                />
              </label>
              <label style={presetRoleFieldStyle}>
                <span style={presetRoleFieldLabelStyle}>Model hint</span>
                <input
                  className="input"
                  value={form.modelHint}
                  disabled={pending || editingBuiltIn}
                  placeholder="qwen2.5-coder"
                  onChange={(event) =>
                    setForm((current) => ({ ...current, modelHint: event.target.value }))
                  }
                />
              </label>
            </div>
            <div style={{ display: "flex", gap: 12, flexWrap: "wrap" }}>
              <label style={presetRoleCheckboxLabelStyle}>
                <input
                  type="checkbox"
                  checked={form.toolsEnabled}
                  disabled={pending || editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, toolsEnabled: event.target.checked }))
                  }
                />
                Tools enabled
              </label>
              <label style={presetRoleCheckboxLabelStyle}>
                <input
                  type="checkbox"
                  checked={form.writesAllowed}
                  disabled={pending || editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, writesAllowed: event.target.checked }))
                  }
                />
                Writes allowed
              </label>
              <label style={presetRoleCheckboxLabelStyle}>
                <input
                  type="checkbox"
                  checked={form.networkAllowed}
                  disabled={pending || editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, networkAllowed: event.target.checked }))
                  }
                />
                Network allowed
              </label>
            </div>
            <div
              className="agent-presets-form-grid"
              style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}
            >
              <label style={presetRoleFieldStyle}>
                <span style={presetRoleFieldLabelStyle}>Approval policy</span>
                <select
                  className="input"
                  value={form.approvalPolicy}
                  disabled={pending || editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, approvalPolicy: event.target.value }))
                  }
                >
                  {AGENT_PRESET_APPROVAL_POLICIES.map((policy) => (
                    <option key={policy} value={policy}>
                      {policy}
                    </option>
                  ))}
                </select>
              </label>
              <label style={presetRoleFieldStyle}>
                <span style={presetRoleFieldLabelStyle}>Memory policy</span>
                <select
                  className="input"
                  value={form.projectMemoryPolicy}
                  disabled={pending || editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, projectMemoryPolicy: event.target.value }))
                  }
                >
                  {AGENT_PRESET_MEMORY_POLICIES.map((policy) => (
                    <option key={policy} value={policy}>
                      {policy}
                    </option>
                  ))}
                </select>
              </label>
              <label style={presetRoleFieldStyle}>
                <span style={presetRoleFieldLabelStyle}>Context source policy</span>
                <select
                  className="input"
                  value={form.contextSourcePolicy}
                  disabled={pending || editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, contextSourcePolicy: event.target.value }))
                  }
                >
                  {AGENT_PRESET_CONTEXT_POLICIES.map((policy) => (
                    <option key={policy} value={policy}>
                      {policy}
                    </option>
                  ))}
                </select>
              </label>
              <label style={presetRoleFieldStyle}>
                <span style={presetRoleFieldLabelStyle}>External agent kind</span>
                <input
                  className="input"
                  value={form.externalAgentKind}
                  disabled={pending || editingBuiltIn}
                  placeholder="claude_code"
                  onChange={(event) =>
                    setForm((current) => ({ ...current, externalAgentKind: event.target.value }))
                  }
                />
              </label>
            </div>
            <ProjectSkillPicker
              disabled={pending || editingBuiltIn}
              onChange={(skillIDs) => setForm((current) => ({ ...current, skillIDs }))}
              skills={projectSkills}
              value={form.skillIDs}
            />
            <div style={presetRoleSubtleTextStyle}>
              Presets set runtime posture and skill references. Skills do not grant tools, writes,
              network, or approvals.
            </div>
          </form>
        </div>
      </Modal>
      {deletePreset && (
        <ConfirmModal
          title="Delete agent preset"
          danger
          pending={pending}
          confirmLabel="Delete agent preset"
          onClose={() => setDeletePreset(null)}
          onConfirm={() => void deleteSelectedPreset(deletePreset)}
          message={
            <>
              Delete <strong>{deletePreset.name || deletePreset.id}</strong>.{" "}
              {presetReferenceSummary(deletePreset, project, roles)} Other projects may also
              reference this global preset.
            </>
          }
        />
      )}
    </>
  );
}

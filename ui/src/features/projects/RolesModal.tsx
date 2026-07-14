import { useRef, useState } from "react";

import type { AgentPresetRecord } from "../../types/agent-preset";
import type { ProjectSkillRecord, ProjectWorkRoleRecord } from "../../types/project";
import { Icon, Icons, InlineError, Modal } from "../shared/ui";
import {
  HUMAN_ASSIGNMENT_DESCRIPTION,
  PROJECT_ASSIGNMENT_DESTINATIONS,
} from "./projectAssignmentDestinations";
import { ProjectSkillPicker } from "./ProjectSkillPicker";
import { emptyRoleForm, roleFormFromRecord, type RoleForm } from "./projectPresetsRoles";
import {
  presetRoleFieldLabelStyle,
  presetRoleFieldStyle,
  presetRoleSubtleTextStyle,
} from "./projectPresetRoleStyles";

type RolesModalProps = {
  agentPresets: AgentPresetRecord[];
  error: string;
  mode?: "manage" | "quick-create";
  pending: boolean;
  projectSkills: ProjectSkillRecord[];
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onCreate: (
    form: RoleForm,
  ) => ProjectWorkRoleRecord | undefined | Promise<ProjectWorkRoleRecord | undefined>;
  onDelete: (role: ProjectWorkRoleRecord) => boolean | Promise<boolean>;
  onCreated?: (role: ProjectWorkRoleRecord) => void;
  onUpdate: (
    roleID: string,
    form: RoleForm,
  ) => ProjectWorkRoleRecord | undefined | Promise<ProjectWorkRoleRecord | undefined>;
};

export function RolesModal({
  agentPresets,
  error,
  mode = "manage",
  pending,
  projectSkills,
  roles,
  onClose,
  onCreate,
  onCreated,
  onDelete,
  onUpdate,
}: RolesModalProps) {
  const nameInputRef = useRef<HTMLInputElement>(null);
  const submitInFlightRef = useRef(false);
  const quickCreate = mode === "quick-create";
  const customRoles = roles.filter((role) => !role.built_in);
  const firstEditable = customRoles[0] ?? null;
  const [selectedRoleID, setSelectedRoleID] = useState(
    quickCreate ? "new" : (firstEditable?.id ?? "new"),
  );
  const selectedRole = roles.find((role) => role.id === selectedRoleID) ?? null;
  const editingBuiltIn = Boolean(selectedRole?.built_in);
  const editingNew = selectedRoleID === "new";
  const [form, setForm] = useState<RoleForm>(() =>
    selectedRole ? roleFormFromRecord(selectedRole) : emptyRoleForm(),
  );

  function selectRole(roleID: string) {
    setSelectedRoleID(roleID);
    const role = roles.find((item) => item.id === roleID) ?? null;
    setForm(role ? roleFormFromRecord(role) : emptyRoleForm());
  }

  function selectRoleRecord(role: ProjectWorkRoleRecord) {
    setSelectedRoleID(role.id);
    setForm(roleFormFromRecord(role));
  }

  const canSave = form.name.trim().length > 0 && !editingBuiltIn;
  const submit = async () => {
    if (pending || submitInFlightRef.current || !canSave) return;
    submitInFlightRef.current = true;
    try {
      if (editingNew) {
        const role = await onCreate(form);
        if (role) {
          if (quickCreate) {
            onCreated?.(role);
          } else {
            selectRoleRecord(role);
          }
        }
        return;
      }
      const role = await onUpdate(selectedRoleID, form);
      if (role) {
        selectRoleRecord(role);
      }
    } finally {
      submitInFlightRef.current = false;
    }
  };

  async function deleteSelectedRole(role: ProjectWorkRoleRecord) {
    const deleted = await onDelete(role);
    if (!deleted) return;
    const nextRole = roles.find((item) => !item.built_in && item.id !== role.id) ?? null;
    if (nextRole) {
      selectRoleRecord(nextRole);
      return;
    }
    setSelectedRoleID("new");
    setForm(emptyRoleForm());
  }

  const advancedFields = (
    <>
      <label style={presetRoleFieldStyle}>
        <span style={presetRoleFieldLabelStyle}>Instructions</span>
        <textarea
          className="input"
          value={form.instructions}
          disabled={editingBuiltIn}
          rows={5}
          onChange={(event) =>
            setForm((current) => ({ ...current, instructions: event.target.value }))
          }
        />
      </label>
      <div
        className="project-role-defaults-grid"
        style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}
      >
        <label style={presetRoleFieldStyle}>
          <span style={presetRoleFieldLabelStyle}>Default destination</span>
          <select
            aria-describedby={
              form.defaultDriverKind === "manual"
                ? "role-human-default-destination-help"
                : undefined
            }
            className="input"
            value={form.defaultDriverKind}
            disabled={editingBuiltIn}
            onChange={(event) =>
              setForm((current) => ({ ...current, defaultDriverKind: event.target.value }))
            }
          >
            <option value="">Choose per assignment</option>
            {PROJECT_ASSIGNMENT_DESTINATIONS.map((destination) => (
              <option key={destination.kind} value={destination.kind}>
                {destination.label}
              </option>
            ))}
          </select>
          {form.defaultDriverKind === "manual" && (
            <span id="role-human-default-destination-help" style={presetRoleSubtleTextStyle}>
              {HUMAN_ASSIGNMENT_DESCRIPTION}
            </span>
          )}
        </label>
        <label style={presetRoleFieldStyle}>
          <span style={presetRoleFieldLabelStyle}>Default preset</span>
          <select
            className="input"
            value={form.defaultAgentPreset}
            disabled={editingBuiltIn}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                defaultAgentPreset: event.target.value,
              }))
            }
          >
            <option value="">inherit project default</option>
            {agentPresets.map((preset) => (
              <option key={preset.id} value={preset.id}>
                {preset.name || preset.id} ({preset.id})
              </option>
            ))}
          </select>
        </label>
        <label style={presetRoleFieldStyle}>
          <span style={presetRoleFieldLabelStyle}>Default provider</span>
          <input
            className="input"
            value={form.defaultProvider}
            disabled={editingBuiltIn}
            placeholder="ollama"
            onChange={(event) =>
              setForm((current) => ({ ...current, defaultProvider: event.target.value }))
            }
          />
        </label>
        <label style={presetRoleFieldStyle}>
          <span style={presetRoleFieldLabelStyle}>Default model</span>
          <input
            className="input"
            value={form.defaultModel}
            disabled={editingBuiltIn}
            placeholder="ministral-3:latest"
            onChange={(event) =>
              setForm((current) => ({ ...current, defaultModel: event.target.value }))
            }
          />
        </label>
      </div>
      <ProjectSkillPicker
        disabled={editingBuiltIn}
        onChange={(skillIDs) => setForm((current) => ({ ...current, skillIDs }))}
        skills={projectSkills}
        value={form.skillIDs}
      />
      <div style={presetRoleSubtleTextStyle}>
        Responsibility defaults are suggestions. Each assignment can choose another destination,
        with project settings as the fallback.
      </div>
    </>
  );

  return (
    <Modal
      title={quickCreate ? "Add responsibility" : "Project roles"}
      dismissible={!pending}
      initialFocusRef={nameInputRef}
      onClose={onClose}
      width={quickCreate ? 520 : 760}
      footer={
        <div style={{ display: "flex", gap: 8, width: "100%" }}>
          {!quickCreate && selectedRole && !selectedRole.built_in && !editingNew && (
            <button
              className="btn btn-ghost"
              type="button"
              disabled={pending}
              onClick={() => void deleteSelectedRole(selectedRole)}
              style={{ color: "var(--red)" }}
            >
              Delete role
            </button>
          )}
          <button
            className="btn btn-primary"
            type="button"
            disabled={pending || !canSave}
            onClick={() => void submit()}
            style={{ marginLeft: "auto" }}
          >
            {pending
              ? quickCreate
                ? "Adding…"
                : "Saving…"
              : quickCreate
                ? "Add responsibility"
                : editingNew
                  ? "Create role"
                  : "Save role"}
          </button>
        </div>
      }
    >
      <div
        className={quickCreate ? "project-roles-modal-quick" : "project-roles-modal-grid"}
        style={
          quickCreate
            ? { display: "grid", minHeight: 0 }
            : { display: "grid", gridTemplateColumns: "220px 1fr", gap: 14, minHeight: 420 }
        }
      >
        {!quickCreate && (
          <div
            className="project-roles-modal-list"
            style={{
              borderRight: "1px solid var(--border)",
              paddingRight: 10,
              display: "grid",
              alignContent: "start",
              gap: 6,
            }}
          >
            <button
              aria-pressed={selectedRoleID === "new"}
              className={
                selectedRoleID === "new" ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"
              }
              type="button"
              onClick={() => selectRole("new")}
              style={{ justifyContent: "flex-start" }}
            >
              <Icon d={Icons.plus} size={12} />
              New custom role
            </button>
            {roles.map((role) => (
              <button
                key={role.id}
                aria-pressed={selectedRoleID === role.id}
                className={
                  selectedRoleID === role.id ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"
                }
                type="button"
                onClick={() => selectRole(role.id)}
                style={{ justifyContent: "flex-start", minWidth: 0 }}
              >
                <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{role.name}</span>
                {role.built_in && <span className="badge badge-muted">built-in</span>}
              </button>
            ))}
          </div>
        )}
        <form
          aria-busy={pending}
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
          style={{ display: "grid", gap: 12, alignContent: "start" }}
        >
          {error && <InlineError message={error} />}
          {quickCreate && (
            <div style={presetRoleSubtleTextStyle}>
              Name the responsibility this work needs. Instructions and execution defaults can be
              added now or later.
            </div>
          )}
          {editingBuiltIn && (
            <div style={presetRoleSubtleTextStyle}>
              Built-in roles are read-only. Create a custom role to override instructions or
              execution defaults for this project.
            </div>
          )}
          <label style={presetRoleFieldStyle}>
            <span style={presetRoleFieldLabelStyle}>Name</span>
            <input
              ref={nameInputRef}
              autoComplete="off"
              className="input"
              name="responsibility-name"
              value={form.name}
              disabled={editingBuiltIn}
              onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))}
            />
          </label>
          <label style={presetRoleFieldStyle}>
            <span style={presetRoleFieldLabelStyle}>Description</span>
            <textarea
              autoComplete="off"
              className="input"
              name="responsibility-description"
              value={form.description}
              disabled={editingBuiltIn}
              rows={2}
              onChange={(event) =>
                setForm((current) => ({ ...current, description: event.target.value }))
              }
            />
          </label>
          {quickCreate ? (
            <details
              className="project-support-details"
              style={{ borderTop: "1px solid var(--border)", paddingTop: 10 }}
            >
              <summary style={{ color: "var(--t1)", cursor: "pointer", fontSize: 12 }}>
                Instructions &amp; execution defaults
              </summary>
              <div style={{ display: "grid", gap: 12, marginTop: 12 }}>{advancedFields}</div>
            </details>
          ) : (
            advancedFields
          )}
        </form>
      </div>
    </Modal>
  );
}

import { useState } from "react";

import type { AgentProfileRecord } from "../../types/agent-profile";
import type { ProjectSkillRecord, ProjectWorkRoleRecord } from "../../types/project";
import { Icon, Icons, InlineError, Modal } from "../shared/ui";
import { ProjectSkillPicker } from "./ProjectSkillPicker";
import { emptyRoleForm, roleFormFromRecord, type RoleForm } from "./projectProfilesRoles";
import {
  profileRoleFieldLabelStyle,
  profileRoleFieldStyle,
  profileRoleSubtleTextStyle,
} from "./projectProfileRoleStyles";

type RolesModalProps = {
  agentProfiles: AgentProfileRecord[];
  error: string;
  pending: boolean;
  projectSkills: ProjectSkillRecord[];
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onCreate: (
    form: RoleForm,
  ) => ProjectWorkRoleRecord | undefined | Promise<ProjectWorkRoleRecord | undefined>;
  onDelete: (role: ProjectWorkRoleRecord) => boolean | Promise<boolean>;
  onUpdate: (
    roleID: string,
    form: RoleForm,
  ) => ProjectWorkRoleRecord | undefined | Promise<ProjectWorkRoleRecord | undefined>;
};

export function RolesModal({
  agentProfiles,
  error,
  pending,
  projectSkills,
  roles,
  onClose,
  onCreate,
  onDelete,
  onUpdate,
}: RolesModalProps) {
  const customRoles = roles.filter((role) => !role.built_in);
  const firstEditable = customRoles[0] ?? null;
  const [selectedRoleID, setSelectedRoleID] = useState(firstEditable?.id ?? "new");
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
    if (!canSave) return;
    if (editingNew) {
      const role = await onCreate(form);
      if (role) {
        selectRoleRecord(role);
      }
      return;
    }
    const role = await onUpdate(selectedRoleID, form);
    if (role) {
      selectRoleRecord(role);
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

  return (
    <Modal
      title="Project roles"
      onClose={onClose}
      width={760}
      footer={
        <div style={{ display: "flex", gap: 8, width: "100%" }}>
          {selectedRole && !selectedRole.built_in && !editingNew && (
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
            {pending ? "Saving…" : editingNew ? "Create role" : "Save role"}
          </button>
        </div>
      }
    >
      <div style={{ display: "grid", gridTemplateColumns: "220px 1fr", gap: 14, minHeight: 420 }}>
        <div
          style={{
            borderRight: "1px solid var(--border)",
            paddingRight: 10,
            display: "grid",
            alignContent: "start",
            gap: 6,
          }}
        >
          <button
            className={selectedRoleID === "new" ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"}
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
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
          style={{ display: "grid", gap: 12, alignContent: "start" }}
        >
          {error && <InlineError message={error} />}
          {editingBuiltIn && (
            <div style={profileRoleSubtleTextStyle}>
              Built-in roles are read-only. Create a custom role to override instructions or
              execution defaults for this project.
            </div>
          )}
          <label style={profileRoleFieldStyle}>
            <span style={profileRoleFieldLabelStyle}>Name</span>
            <input
              className="input"
              value={form.name}
              disabled={editingBuiltIn}
              onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))}
              autoFocus={editingNew}
            />
          </label>
          <label style={profileRoleFieldStyle}>
            <span style={profileRoleFieldLabelStyle}>Description</span>
            <textarea
              className="input"
              value={form.description}
              disabled={editingBuiltIn}
              rows={2}
              onChange={(event) =>
                setForm((current) => ({ ...current, description: event.target.value }))
              }
            />
          </label>
          <label style={profileRoleFieldStyle}>
            <span style={profileRoleFieldLabelStyle}>Instructions</span>
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
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
            <label style={profileRoleFieldStyle}>
              <span style={profileRoleFieldLabelStyle}>Default driver</span>
              <select
                className="input"
                value={form.defaultDriverKind}
                disabled={editingBuiltIn}
                onChange={(event) =>
                  setForm((current) => ({ ...current, defaultDriverKind: event.target.value }))
                }
              >
                <option value="">assignment default</option>
                <option value="hecate_task">hecate_task</option>
                <option value="external_agent">external_agent</option>
              </select>
            </label>
            <label style={profileRoleFieldStyle}>
              <span style={profileRoleFieldLabelStyle}>Default preset</span>
              <select
                className="input"
                value={form.defaultAgentProfile}
                disabled={editingBuiltIn}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    defaultAgentProfile: event.target.value,
                  }))
                }
              >
                <option value="">inherit project default</option>
                {agentProfiles.map((profile) => (
                  <option key={profile.id} value={profile.id}>
                    {profile.name || profile.id} ({profile.id})
                  </option>
                ))}
              </select>
            </label>
            <label style={profileRoleFieldStyle}>
              <span style={profileRoleFieldLabelStyle}>Default provider</span>
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
            <label style={profileRoleFieldStyle}>
              <span style={profileRoleFieldLabelStyle}>Default model</span>
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
          <div style={profileRoleSubtleTextStyle}>
            Role defaults are execution hints. Assignments can still override the driver, and
            project defaults remain the fallback.
          </div>
        </form>
      </div>
    </Modal>
  );
}

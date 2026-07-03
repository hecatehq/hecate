import { useState } from "react";

import type { AgentProfileRecord } from "../../types/agent-profile";
import type { ProjectRecord, ProjectSkillRecord, ProjectWorkRoleRecord } from "../../types/project";
import { ConfirmModal, Icon, Icons, InlineError, Modal } from "../shared/ui";
import { ProjectSkillPicker } from "./ProjectSkillPicker";
import {
  emptyAgentProfileForm,
  profileFormFromRecord,
  profileReferenceSummary,
  type AgentProfileForm,
} from "./projectProfilesRoles";
import {
  profileRoleCheckboxLabelStyle,
  profileRoleFieldLabelStyle,
  profileRoleFieldStyle,
  profileRoleSubtleTextStyle,
} from "./projectProfileRoleStyles";

const AGENT_PROFILE_SURFACES = ["any", "hecate_chat", "hecate_task", "external_agent"];
const AGENT_PROFILE_APPROVAL_POLICIES = ["inherit", "require", "block", "allow"];
const AGENT_PROFILE_MEMORY_POLICIES = ["inherit", "include", "visible_only", "exclude"];
const AGENT_PROFILE_CONTEXT_POLICIES = ["inherit", "include_enabled", "visible_only", "exclude"];

type ProfilesModalProps = {
  error: string;
  pending: boolean;
  profiles: AgentProfileRecord[];
  project: ProjectRecord;
  projectSkills: ProjectSkillRecord[];
  roles: ProjectWorkRoleRecord[];
  onClose: () => void;
  onCreate: (
    form: AgentProfileForm,
  ) => AgentProfileRecord | undefined | Promise<AgentProfileRecord | undefined>;
  onDelete: (profile: AgentProfileRecord) => boolean | Promise<boolean>;
  onUpdate: (
    profileID: string,
    form: AgentProfileForm,
  ) => AgentProfileRecord | undefined | Promise<AgentProfileRecord | undefined>;
};

export function ProfilesModal({
  error,
  pending,
  profiles,
  project,
  projectSkills,
  roles,
  onClose,
  onCreate,
  onDelete,
  onUpdate,
}: ProfilesModalProps) {
  const [selectedProfileID, setSelectedProfileID] = useState(profiles[0]?.id ?? "new");
  const selectedProfile = profiles.find((profile) => profile.id === selectedProfileID) ?? null;
  const editingNew = selectedProfileID === "new";
  const editingBuiltIn = Boolean(selectedProfile?.built_in);
  const [deleteProfile, setDeleteProfile] = useState<AgentProfileRecord | null>(null);
  const [form, setForm] = useState<AgentProfileForm>(() =>
    selectedProfile ? profileFormFromRecord(selectedProfile) : emptyAgentProfileForm(),
  );

  function selectProfile(profileID: string) {
    setSelectedProfileID(profileID);
    const profile = profiles.find((item) => item.id === profileID) ?? null;
    setForm(profile ? profileFormFromRecord(profile) : emptyAgentProfileForm());
  }

  function selectProfileRecord(profile: AgentProfileRecord) {
    setSelectedProfileID(profile.id);
    setForm(profileFormFromRecord(profile));
  }

  const canSave = !editingBuiltIn && form.name.trim().length > 0;
  const submit = async () => {
    if (!canSave) return;
    if (editingNew) {
      const profile = await onCreate(form);
      if (profile) selectProfileRecord(profile);
      return;
    }
    const profile = await onUpdate(selectedProfileID, form);
    if (profile) selectProfileRecord(profile);
  };

  async function deleteSelectedProfile(profile: AgentProfileRecord) {
    const deleted = await onDelete(profile);
    if (!deleted) return;
    setDeleteProfile(null);
    const nextProfile = profiles.find((item) => item.id !== profile.id) ?? null;
    if (nextProfile) {
      selectProfileRecord(nextProfile);
      return;
    }
    setSelectedProfileID("new");
    setForm(emptyAgentProfileForm());
  }

  return (
    <>
      <Modal
        title="Agent presets"
        onClose={onClose}
        width={840}
        footer={
          <div style={{ display: "flex", gap: 8, width: "100%" }}>
            {editingBuiltIn && (
              <span className="badge badge-muted" style={{ alignSelf: "center" }}>
                Built-in preset
              </span>
            )}
            {selectedProfile && !editingNew && !editingBuiltIn && (
              <button
                className="btn btn-ghost"
                type="button"
                disabled={pending}
                onClick={() => setDeleteProfile(selectedProfile)}
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
                {pending ? "Saving..." : editingNew ? "Create preset" : "Save preset"}
              </button>
            )}
          </div>
        }
      >
        <div style={{ display: "grid", gridTemplateColumns: "220px 1fr", gap: 14, minHeight: 470 }}>
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
              className={
                selectedProfileID === "new" ? "btn btn-primary btn-sm" : "btn btn-ghost btn-sm"
              }
              type="button"
              onClick={() => selectProfile("new")}
              style={{ justifyContent: "flex-start" }}
            >
              <Icon d={Icons.plus} size={12} />
              New preset
            </button>
            {profiles.map((profile) => (
              <button
                key={profile.id}
                className={
                  selectedProfileID === profile.id
                    ? "btn btn-primary btn-sm"
                    : "btn btn-ghost btn-sm"
                }
                type="button"
                onClick={() => selectProfile(profile.id)}
                style={{ justifyContent: "flex-start", minWidth: 0 }}
              >
                <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>
                  {profile.name || profile.id}
                </span>
                {profile.built_in && <span className="badge badge-muted">built-in</span>}
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
            <div style={{ display: "grid", gridTemplateColumns: "160px 1fr", gap: 10 }}>
              <label style={profileRoleFieldStyle}>
                <span style={profileRoleFieldLabelStyle}>Preset id</span>
                <input
                  className="input"
                  value={form.id}
                  disabled={!editingNew}
                  placeholder="implementation"
                  onChange={(event) =>
                    setForm((current) => ({ ...current, id: event.target.value }))
                  }
                />
              </label>
              <label style={profileRoleFieldStyle}>
                <span style={profileRoleFieldLabelStyle}>Name</span>
                <input
                  className="input"
                  value={form.name}
                  autoFocus={editingNew}
                  disabled={editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, name: event.target.value }))
                  }
                />
              </label>
            </div>
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
                <span style={profileRoleFieldLabelStyle}>Surface</span>
                <select
                  className="input"
                  value={form.surface}
                  disabled={editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, surface: event.target.value }))
                  }
                >
                  {AGENT_PROFILE_SURFACES.map((surface) => (
                    <option key={surface} value={surface}>
                      {surface}
                    </option>
                  ))}
                </select>
              </label>
              <label style={profileRoleFieldStyle}>
                <span style={profileRoleFieldLabelStyle}>Runtime profile</span>
                <input
                  className="input"
                  value={form.executionProfile}
                  disabled={editingBuiltIn}
                  placeholder="implementation"
                  onChange={(event) =>
                    setForm((current) => ({ ...current, executionProfile: event.target.value }))
                  }
                />
              </label>
              <label style={profileRoleFieldStyle}>
                <span style={profileRoleFieldLabelStyle}>Provider hint</span>
                <input
                  className="input"
                  value={form.providerHint}
                  disabled={editingBuiltIn}
                  placeholder="ollama"
                  onChange={(event) =>
                    setForm((current) => ({ ...current, providerHint: event.target.value }))
                  }
                />
              </label>
              <label style={profileRoleFieldStyle}>
                <span style={profileRoleFieldLabelStyle}>Model hint</span>
                <input
                  className="input"
                  value={form.modelHint}
                  disabled={editingBuiltIn}
                  placeholder="qwen2.5-coder"
                  onChange={(event) =>
                    setForm((current) => ({ ...current, modelHint: event.target.value }))
                  }
                />
              </label>
            </div>
            <div style={{ display: "flex", gap: 12, flexWrap: "wrap" }}>
              <label style={profileRoleCheckboxLabelStyle}>
                <input
                  type="checkbox"
                  checked={form.toolsEnabled}
                  disabled={editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, toolsEnabled: event.target.checked }))
                  }
                />
                Tools enabled
              </label>
              <label style={profileRoleCheckboxLabelStyle}>
                <input
                  type="checkbox"
                  checked={form.writesAllowed}
                  disabled={editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, writesAllowed: event.target.checked }))
                  }
                />
                Writes allowed
              </label>
              <label style={profileRoleCheckboxLabelStyle}>
                <input
                  type="checkbox"
                  checked={form.networkAllowed}
                  disabled={editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, networkAllowed: event.target.checked }))
                  }
                />
                Network allowed
              </label>
            </div>
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
              <label style={profileRoleFieldStyle}>
                <span style={profileRoleFieldLabelStyle}>Approval policy</span>
                <select
                  className="input"
                  value={form.approvalPolicy}
                  disabled={editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, approvalPolicy: event.target.value }))
                  }
                >
                  {AGENT_PROFILE_APPROVAL_POLICIES.map((policy) => (
                    <option key={policy} value={policy}>
                      {policy}
                    </option>
                  ))}
                </select>
              </label>
              <label style={profileRoleFieldStyle}>
                <span style={profileRoleFieldLabelStyle}>Memory policy</span>
                <select
                  className="input"
                  value={form.projectMemoryPolicy}
                  disabled={editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, projectMemoryPolicy: event.target.value }))
                  }
                >
                  {AGENT_PROFILE_MEMORY_POLICIES.map((policy) => (
                    <option key={policy} value={policy}>
                      {policy}
                    </option>
                  ))}
                </select>
              </label>
              <label style={profileRoleFieldStyle}>
                <span style={profileRoleFieldLabelStyle}>Context source policy</span>
                <select
                  className="input"
                  value={form.contextSourcePolicy}
                  disabled={editingBuiltIn}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, contextSourcePolicy: event.target.value }))
                  }
                >
                  {AGENT_PROFILE_CONTEXT_POLICIES.map((policy) => (
                    <option key={policy} value={policy}>
                      {policy}
                    </option>
                  ))}
                </select>
              </label>
              <label style={profileRoleFieldStyle}>
                <span style={profileRoleFieldLabelStyle}>External agent kind</span>
                <input
                  className="input"
                  value={form.externalAgentKind}
                  disabled={editingBuiltIn}
                  placeholder="claude_code"
                  onChange={(event) =>
                    setForm((current) => ({ ...current, externalAgentKind: event.target.value }))
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
              Presets set runtime posture and skill references. Skills do not grant tools, writes,
              network, or approvals.
            </div>
          </form>
        </div>
      </Modal>
      {deleteProfile && (
        <ConfirmModal
          title="Delete agent preset"
          danger
          pending={pending}
          confirmLabel="Delete agent preset"
          onClose={() => setDeleteProfile(null)}
          onConfirm={() => void deleteSelectedProfile(deleteProfile)}
          message={
            <>
              Delete <strong>{deleteProfile.name || deleteProfile.id}</strong>.{" "}
              {profileReferenceSummary(deleteProfile, project, roles)} Other projects may also
              reference this global preset.
            </>
          }
        />
      )}
    </>
  );
}

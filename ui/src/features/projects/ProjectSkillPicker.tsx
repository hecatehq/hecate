import type { ProjectSkillRecord } from "../../types/project";
import {
  projectSkillBadgeClass,
  projectSkillSelectionWarnings,
  sortProjectSkillsForPicker,
  uniqueSkillIDs,
} from "./projectPresetsRoles";
import {
  presetRoleFieldLabelStyle,
  presetRoleFieldStyle,
  presetRoleSubtleTextStyle,
  presetRoleTitleStyle,
} from "./projectPresetRoleStyles";
import { splitIDs } from "./projectUtils";

type ProjectSkillPickerProps = {
  disabled?: boolean;
  skills: ProjectSkillRecord[];
  value: string;
  onChange: (value: string) => void;
};

export function ProjectSkillPicker({
  disabled = false,
  skills,
  value,
  onChange,
}: ProjectSkillPickerProps) {
  const selectedIDs = uniqueSkillIDs(splitIDs(value));
  const selectedSet = new Set(selectedIDs);
  const indexedSkills = new Map(skills.map((skill) => [skill.id, skill]));
  const sortedSkills = sortProjectSkillsForPicker(skills);
  const warnings = selectedIDs.flatMap((id) => projectSkillSelectionWarnings(id, indexedSkills));

  function toggleSkill(skillID: string, checked: boolean) {
    const next = checked
      ? uniqueSkillIDs([...selectedIDs, skillID])
      : selectedIDs.filter((id) => id !== skillID);
    onChange(next.join(", "));
  }

  return (
    <div style={presetRoleFieldStyle}>
      {sortedSkills.length > 0 && (
        <div style={{ display: "grid", gap: 6 }}>
          <span style={presetRoleFieldLabelStyle}>Project skills</span>
          <div style={{ display: "grid", gap: 6 }}>
            {sortedSkills.map((skill) => (
              <label
                key={`${skill.id}:${skill.path}`}
                style={{
                  border: "1px solid var(--border)",
                  borderRadius: 6,
                  display: "grid",
                  gap: 4,
                  gridTemplateColumns: "auto 1fr",
                  padding: "7px 8px",
                }}
              >
                <input
                  type="checkbox"
                  checked={selectedSet.has(skill.id)}
                  disabled={disabled}
                  aria-label={`Use skill ${skill.title || skill.id}`}
                  onChange={(event) => toggleSkill(skill.id, event.target.checked)}
                  style={{ marginTop: 2 }}
                />
                <span style={{ display: "grid", gap: 4, minWidth: 0 }}>
                  <span style={{ display: "flex", gap: 6, alignItems: "center", flexWrap: "wrap" }}>
                    <span style={presetRoleTitleStyle}>{skill.title || skill.id}</span>
                    <span className={projectSkillBadgeClass(skill)}>{skill.status}</span>
                    {!skill.enabled && <span className="badge badge-muted">disabled</span>}
                    <span className="badge badge-muted">{skill.id}</span>
                  </span>
                  <span style={presetRoleSubtleTextStyle}>{skill.path}</span>
                </span>
              </label>
            ))}
          </div>
        </div>
      )}
      <label style={{ ...presetRoleFieldStyle, marginTop: sortedSkills.length > 0 ? 8 : 0 }}>
        <span style={presetRoleFieldLabelStyle}>Skill ids</span>
        <input
          className="input"
          value={value}
          disabled={disabled}
          placeholder="backend, qa"
          onChange={(event) => onChange(event.target.value)}
        />
      </label>
      {warnings.length > 0 && (
        <div style={{ display: "grid", gap: 3 }}>
          {warnings.map((warning) => (
            <div key={warning} style={{ ...presetRoleSubtleTextStyle, color: "var(--amber)" }}>
              {warning}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

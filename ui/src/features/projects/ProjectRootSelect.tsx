import type { ProjectRecord } from "../../types/project";
import { projectRootOptionLabel } from "./projectSettings";
import { projectWorkFieldLabelStyle, projectWorkFieldStyle } from "./projectWorkModalStyles";

type ProjectRootSelectProps = {
  inheritLabel: string;
  label?: string;
  project: ProjectRecord;
  value: string;
  onChange: (rootID: string) => void;
};

export function ProjectRootSelect({
  inheritLabel,
  label = "Root",
  project,
  value,
  onChange,
}: ProjectRootSelectProps) {
  if (project.roots.length === 0) return null;
  return (
    <label style={projectWorkFieldStyle}>
      <span style={projectWorkFieldLabelStyle}>{label}</span>
      <select
        aria-label={label}
        className="input"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        style={{ fontFamily: "var(--font-mono)", fontSize: 12, minHeight: 36 }}
      >
        <option value="">{inheritLabel}</option>
        {project.roots.map((root) => (
          <option key={root.id || root.path} value={root.id}>
            {projectRootOptionLabel(root)}
          </option>
        ))}
      </select>
    </label>
  );
}

import { useEffect, useState, type CSSProperties } from "react";

import { formatAbsoluteTime } from "../../lib/format";
import type {
  ProjectRecord,
  ProjectSkillRecord,
  UpdateProjectSkillPayload,
} from "../../types/project";
import { CopyableID, Icon, Icons, InlineError } from "../shared/ui";

type ProjectSkillsPanelProps = {
  discovering: boolean;
  error: string;
  loading: boolean;
  onDiscover: () => void;
  onRefresh: () => void;
  onUpdate: (skill: ProjectSkillRecord, patch: UpdateProjectSkillPayload) => void;
  project: ProjectRecord | null;
  skills: ProjectSkillRecord[];
  updatingSkillID: string;
};

const suggestedToolsSummaryLimit = 8;

export function ProjectSkillsPanel({
  discovering,
  error,
  loading,
  onDiscover,
  onRefresh,
  onUpdate,
  project,
  skills,
  updatingSkillID,
}: ProjectSkillsPanelProps) {
  if (!project) return null;
  const enabledCount = skills.filter((skill) => skill.enabled).length;
  const availableCount = skills.filter((skill) => skill.status === "available").length;
  return (
    <div>
      <div style={panelStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
          <div>
            <div style={sectionLabelStyle}>Project Skills</div>
            <div style={{ ...subtleTextStyle, marginTop: 3 }}>
              {loading
                ? "Loading project skills..."
                : `${enabledCount} enabled / ${availableCount} available / ${skills.length} registered`}
            </div>
          </div>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-label="Refresh project skills"
            title="Refresh"
            onClick={onRefresh}
            style={{ marginLeft: "auto" }}
          >
            <Icon d={Icons.refresh} size={12} />
          </button>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            disabled={discovering}
            onClick={onDiscover}
          >
            <Icon d={Icons.search} size={12} />
            {discovering ? "Discovering..." : "Discover"}
          </button>
        </div>
        {error && (
          <div style={{ marginBottom: 10 }}>
            <InlineError message={error} />
          </div>
        )}
        {skills.length === 0 && !loading ? (
          <EmptyBlock
            title="No project skills registered"
            detail="Discover skills from guidance-linked roots, .agents/skills, .cairnline/skills, .claude/skills, .gemini/skills, or .hecate/skills."
          />
        ) : (
          <div style={{ display: "grid", gap: 8 }}>
            {skills.map((skill) => (
              <ProjectSkillRow
                key={skill.id}
                pending={updatingSkillID === skill.id}
                skill={skill}
                onUpdate={onUpdate}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

type SkillForm = {
  title: string;
  description: string;
  trustLabel: string;
};

function ProjectSkillRow({
  onUpdate,
  pending,
  skill,
}: {
  onUpdate: (skill: ProjectSkillRecord, patch: UpdateProjectSkillPayload) => void;
  pending: boolean;
  skill: ProjectSkillRecord;
}) {
  const [draft, setDraft] = useState(() => skillFormFromRecord(skill));

  useEffect(() => {
    setDraft(skillFormFromRecord(skill));
  }, [skill]);

  const changed =
    draft.title.trim() !== skill.title ||
    draft.description.trim() !== (skill.description ?? "") ||
    draft.trustLabel.trim() !== skill.trust_label;
  const statusClass = skill.status === "available" ? "badge badge-green" : "badge badge-amber";
  const capabilitySummary = projectSkillCapabilitySummary(skill);

  return (
    <div style={memoryEntryStyle}>
      <div style={{ display: "flex", alignItems: "flex-start", gap: 10 }}>
        <label
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: 6,
            paddingTop: 2,
            color: "var(--text1)",
          }}
        >
          <input
            type="checkbox"
            checked={skill.enabled}
            disabled={pending}
            aria-label={`Enable skill ${skill.title || skill.id}`}
            onChange={(event) => onUpdate(skill, { enabled: event.target.checked })}
          />
          <span className={skill.enabled ? "badge badge-green" : "badge badge-muted"}>
            {skill.enabled ? "enabled" : "disabled"}
          </span>
        </label>
        <div style={{ display: "grid", gap: 8, flex: 1, minWidth: 0 }}>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
            <CopyableID text={skill.id} compact />
            <span className={statusClass}>{skill.status}</span>
            <span className="badge badge-muted">{skill.trust_label}</span>
            <span className="badge badge-muted">{skill.format}</span>
          </div>
          <div style={{ display: "grid", gridTemplateColumns: "minmax(160px, 1fr) 1fr", gap: 8 }}>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Title</span>
              <input
                className="input"
                value={draft.title}
                disabled={pending}
                onChange={(event) =>
                  setDraft((current) => ({ ...current, title: event.target.value }))
                }
              />
            </label>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Trust label</span>
              <input
                className="input"
                value={draft.trustLabel}
                disabled={pending}
                onChange={(event) =>
                  setDraft((current) => ({ ...current, trustLabel: event.target.value }))
                }
              />
            </label>
          </div>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Description</span>
            <textarea
              className="input"
              rows={2}
              value={draft.description}
              disabled={pending}
              onChange={(event) =>
                setDraft((current) => ({ ...current, description: event.target.value }))
              }
            />
          </label>
          <div style={subtleTextStyle}>
            {skill.path}
            {skill.root_id ? ` · root ${skill.root_id}` : ""}
            {skill.source_context_source_ids?.length
              ? ` · sources ${skill.source_context_source_ids.join(", ")}`
              : ""}
            {skill.discovered_at ? ` · discovered ${formatAbsoluteTime(skill.discovered_at)}` : ""}
          </div>
          {capabilitySummary && <div style={skillCapabilityStyle}>{capabilitySummary}</div>}
          {skill.warnings?.length ? (
            <div style={{ display: "grid", gap: 3 }}>
              {skill.warnings.map((warning) => (
                <div key={warning} style={{ ...subtleTextStyle, color: "var(--amber)" }}>
                  {warning}
                </div>
              ))}
            </div>
          ) : null}
        </div>
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          disabled={pending || !changed}
          onClick={() =>
            onUpdate(skill, {
              title: draft.title.trim(),
              description: draft.description.trim(),
              trust_label: draft.trustLabel.trim(),
            })
          }
        >
          {pending ? "Saving..." : "Save"}
        </button>
      </div>
    </div>
  );
}

function skillFormFromRecord(skill: ProjectSkillRecord): SkillForm {
  return {
    title: skill.title ?? "",
    description: skill.description ?? "",
    trustLabel: skill.trust_label ?? "workspace_skill",
  };
}

function projectSkillCapabilitySummary(skill: ProjectSkillRecord): string {
  const parts: string[] = [];
  const suggestedTools = projectSkillSuggestedToolsSummary(skill.suggested_tools);
  if (suggestedTools) {
    parts.push(`Suggested tools: ${suggestedTools}`);
  }
  const permissions = projectSkillRequiredPermissionsSummary(skill);
  if (permissions) {
    parts.push(`Required posture: ${permissions}`);
  }
  return parts.join(" · ");
}

function projectSkillSuggestedToolsSummary(tools: string[] | undefined): string {
  if (!tools?.length) return "";
  const shown = tools.slice(0, suggestedToolsSummaryLimit);
  const omitted = tools.length - shown.length;
  return `${shown.join(", ")}${omitted > 0 ? `, +${omitted} more` : ""}`;
}

function projectSkillRequiredPermissionsSummary(skill: ProjectSkillRecord): string {
  const permissions = skill.required_permissions;
  if (!permissions) return "";
  const parts: string[] = [];
  for (const [label, value] of [
    ["tools", permissions.tools],
    ["writes", permissions.writes],
    ["network", permissions.network],
  ] as const) {
    if (typeof value === "boolean") {
      parts.push(`${label} ${value ? "on" : "off"}`);
    }
  }
  return parts.join(", ");
}

function EmptyBlock({ title, detail }: { title: string; detail: string }) {
  return (
    <div
      style={{ padding: 24, textAlign: "center", display: "grid", gap: 8, placeItems: "center" }}
    >
      <div style={{ color: "var(--t0)", fontSize: 14, fontWeight: 600 }}>{title}</div>
      <div style={{ color: "var(--t3)", fontSize: 12, lineHeight: 1.5, maxWidth: 320 }}>
        {detail}
      </div>
    </div>
  );
}

const sectionLabelStyle: CSSProperties = {
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  color: "var(--teal)",
  letterSpacing: "0.06em",
  textTransform: "uppercase",
};

const subtleTextStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 12,
  lineHeight: 1.4,
};

const skillCapabilityStyle: CSSProperties = {
  color: "var(--t2)",
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  lineHeight: 1.5,
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

const panelStyle: CSSProperties = {
  border: "1px solid var(--border)",
  background: "var(--bg1)",
  borderRadius: "var(--radius-sm)",
  boxSizing: "border-box",
  maxWidth: "100%",
  minWidth: 0,
  padding: 12,
};

const memoryEntryStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  paddingTop: 8,
};

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
  const canDiscover = project.roots.some((root) => root.active && root.path.trim());

  return (
    <section aria-busy={loading || discovering} aria-label="Project skills" style={panelStyle}>
      <header className="project-support-header" style={supportHeaderStyle}>
        <div style={{ minWidth: 0 }}>
          <h1 style={surfaceTitleStyle}>Skills</h1>
          <div aria-live="polite" role="status" style={{ ...subtleTextStyle, marginTop: 3 }}>
            {loading
              ? "Loading skills…"
              : `${enabledCount} enabled · ${availableCount} ready · ${skills.length} registered`}
          </div>
        </div>
        <div style={supportActionsStyle}>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-label="Refresh project skills"
            title="Refresh"
            onClick={onRefresh}
          >
            <Icon d={Icons.refresh} size={12} />
          </button>
          <button
            className="btn btn-primary btn-sm"
            type="button"
            disabled={discovering || !canDiscover}
            onClick={onDiscover}
            title={
              canDiscover ? "Find skills in attached folders" : "Attach or enable a folder first"
            }
          >
            <Icon d={Icons.search} size={12} />
            {discovering ? "Finding…" : "Find skills"}
          </button>
        </div>
      </header>
      {!canDiscover && (
        <div style={{ ...guidanceStyle, marginBottom: 12 }}>
          Attach or enable a folder to find skills. Existing skills remain available below.
        </div>
      )}
      {error && (
        <div style={{ marginBottom: 10 }}>
          <InlineError message={error} />
        </div>
      )}
      {loading && skills.length === 0 ? (
        <div aria-live="polite" role="status" style={subtleTextStyle}>
          Loading project skills…
        </div>
      ) : skills.length === 0 ? (
        <EmptyBlock
          title="No skills found"
          detail={
            canDiscover
              ? "Find reusable instructions in this project's attached folders. Nothing is installed or run automatically."
              : "Skills are optional for projects without local files. Attach a folder when you want to find reusable instructions."
          }
        />
      ) : (
        <div style={{ display: "grid", gap: 10 }}>
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
    </section>
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

  const title = skill.title || skill.id;
  const changed =
    draft.title.trim() !== skill.title ||
    draft.description.trim() !== (skill.description ?? "") ||
    draft.trustLabel.trim() !== skill.trust_label;
  const capabilitySummary = projectSkillCapabilitySummary(skill);

  return (
    <article aria-label={`Skill ${title}`} style={skillEntryStyle}>
      <div className="project-support-row-header" style={skillHeaderStyle}>
        <div style={{ minWidth: 0 }}>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 7, alignItems: "center" }}>
            <div style={titleStyle}>{title}</div>
            <span className={projectSkillStatusBadgeClass(skill.status)}>
              {projectSkillStatusLabel(skill.status)}
            </span>
          </div>
          {skill.description && <div style={skillDescriptionStyle}>{skill.description}</div>}
        </div>
        <label style={enableControlStyle}>
          <input
            type="checkbox"
            checked={skill.enabled}
            disabled={pending}
            aria-label={`Enable skill ${title}`}
            onChange={(event) => onUpdate(skill, { enabled: event.target.checked })}
          />
          <span>{skill.enabled ? "Enabled" : "Disabled"}</span>
        </label>
      </div>
      {skill.warnings?.length ? (
        <div style={{ display: "grid", gap: 3, marginTop: 8 }}>
          {skill.warnings.map((warning) => (
            <div key={warning} style={skillWarningStyle}>
              {warning}
            </div>
          ))}
        </div>
      ) : null}
      <details className="project-support-details" style={detailsStyle}>
        <summary>Settings and source</summary>
        <div style={detailsBodyStyle}>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
            <CopyableID text={skill.id} compact />
            <span className="badge badge-muted">{skill.status}</span>
            <span className="badge badge-muted">{skill.trust_label}</span>
            <span className="badge badge-muted">{skill.format}</span>
          </div>
          <div className="project-skill-edit-grid" style={skillEditGridStyle}>
            <label style={fieldStyle}>
              <span style={fieldLabelStyle}>Title</span>
              <input
                aria-label={`Title for ${title}`}
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
                aria-label={`Trust label for ${title}`}
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
              aria-label={`Description for ${title}`}
              className="input"
              rows={2}
              value={draft.description}
              disabled={pending}
              onChange={(event) =>
                setDraft((current) => ({ ...current, description: event.target.value }))
              }
            />
          </label>
          <div style={skillTechnicalMetadataStyle}>
            {skill.path}
            {skill.root_id ? ` · root ${skill.root_id}` : ""}
            {skill.source_context_source_ids?.length
              ? ` · sources ${skill.source_context_source_ids.join(", ")}`
              : ""}
            {skill.discovered_at ? ` · found ${formatAbsoluteTime(skill.discovered_at)}` : ""}
          </div>
          {capabilitySummary && <div style={skillCapabilityStyle}>{capabilitySummary}</div>}
          <div>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              disabled={pending || !changed}
              aria-label={`Save ${title}`}
              onClick={() =>
                onUpdate(skill, {
                  title: draft.title.trim(),
                  description: draft.description.trim(),
                  trust_label: draft.trustLabel.trim(),
                })
              }
            >
              {pending ? "Saving…" : "Save changes"}
            </button>
          </div>
        </div>
      </details>
    </article>
  );
}

function skillFormFromRecord(skill: ProjectSkillRecord): SkillForm {
  return {
    title: skill.title ?? "",
    description: skill.description ?? "",
    trustLabel: skill.trust_label ?? "workspace_skill",
  };
}

function projectSkillStatusLabel(status: ProjectSkillRecord["status"]): string {
  if (status === "available") return "Ready";
  if (status === "missing") return "Missing";
  if (status === "invalid") return "Needs attention";
  if (status === "conflict") return "Conflict";
  return status.replaceAll("_", " ");
}

function projectSkillStatusBadgeClass(status: ProjectSkillRecord["status"]): string {
  if (status === "available") return "badge badge-green";
  if (status === "missing" || status === "invalid" || status === "conflict") {
    return "badge badge-amber";
  }
  return "badge badge-muted";
}

function projectSkillCapabilitySummary(skill: ProjectSkillRecord): string {
  const parts: string[] = [];
  const suggestedTools = projectSkillSuggestedToolsSummary(skill.suggested_tools);
  if (suggestedTools) {
    parts.push(`Suggested tools: ${suggestedTools}`);
  }
  const permissions = projectSkillRequiredPermissionsSummary(skill);
  if (permissions) {
    parts.push(`Suggested access: ${permissions}`);
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
      <div style={{ color: "var(--t3)", fontSize: 12, lineHeight: 1.5, maxWidth: 360 }}>
        {detail}
      </div>
    </div>
  );
}

const subtleTextStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 12,
  lineHeight: 1.4,
};

const guidanceStyle: CSSProperties = {
  background: "var(--bg2)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  color: "var(--t2)",
  fontSize: 12,
  lineHeight: 1.45,
  padding: "9px 10px",
};

const surfaceTitleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 18,
  fontWeight: 650,
  lineHeight: 1.25,
  margin: 0,
};

const titleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 13,
  fontWeight: 600,
  minWidth: 0,
  overflowWrap: "anywhere",
};

const skillDescriptionStyle: CSSProperties = {
  color: "var(--t1)",
  fontSize: 12,
  lineHeight: 1.45,
  marginTop: 5,
  overflowWrap: "anywhere",
};

const skillCapabilityStyle: CSSProperties = {
  color: "var(--t2)",
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  lineHeight: 1.5,
  minWidth: 0,
  overflowWrap: "anywhere",
};

const skillTechnicalMetadataStyle: CSSProperties = {
  ...subtleTextStyle,
  minWidth: 0,
  overflowWrap: "anywhere",
};

const skillWarningStyle: CSSProperties = {
  ...subtleTextStyle,
  color: "var(--amber)",
  minWidth: 0,
  overflowWrap: "anywhere",
};

const fieldStyle: CSSProperties = {
  display: "grid",
  gap: 6,
  minWidth: 0,
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
  padding: 14,
};

const supportHeaderStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "flex",
  gap: 12,
  justifyContent: "space-between",
  marginBottom: 12,
  minWidth: 0,
};

const supportActionsStyle: CSSProperties = {
  display: "flex",
  flexShrink: 0,
  flexWrap: "wrap",
  gap: 6,
  justifyContent: "flex-end",
};

const skillEntryStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  minWidth: 0,
  paddingTop: 10,
};

const skillHeaderStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "flex",
  gap: 12,
  justifyContent: "space-between",
  minWidth: 0,
};

const enableControlStyle: CSSProperties = {
  alignItems: "center",
  color: "var(--t1)",
  display: "inline-flex",
  flexShrink: 0,
  fontSize: 12,
  gap: 6,
};

const detailsStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  color: "var(--t2)",
  fontSize: 12,
  marginTop: 10,
  minWidth: 0,
  paddingTop: 8,
};

const detailsBodyStyle: CSSProperties = {
  display: "grid",
  gap: 10,
  minWidth: 0,
  paddingTop: 10,
};

const skillEditGridStyle: CSSProperties = {
  display: "grid",
  gap: 8,
  gridTemplateColumns: "minmax(160px, 1fr) 1fr",
  minWidth: 0,
};

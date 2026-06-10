import { useEffect, useState, type CSSProperties, type ReactNode } from "react";

import type {
  ProjectAssistantApplyResult,
  ProjectAssistantContextRecord,
  ProjectAssistantProposal,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { CopyableID, Icon, Icons, InlineError } from "../shared/ui";

export const PROJECT_ASSISTANT_AUTO = "__auto__";

export type ProjectAssistantDraftForm = {
  request: string;
  roleID: string;
  driverKind: string;
  draftMode: "deterministic" | "model" | "bootstrap";
};

export type ProjectAssistantStatus = "idle" | "proposing" | "applying" | "applied";
export type ProjectAssistantContextStatus = "idle" | "loading" | "loaded" | "error";

type Props = {
  applyResult: ProjectAssistantApplyResult | null;
  context: ProjectAssistantContextRecord | null;
  contextError: string;
  contextStatus: ProjectAssistantContextStatus;
  error: string;
  onApply: () => void;
  onDismiss: () => void;
  onInspectContext: (form: ProjectAssistantDraftForm) => void;
  onPropose: (form: ProjectAssistantDraftForm) => void;
  project: ProjectRecord | null;
  proposal: ProjectAssistantProposal | null;
  roles: ProjectWorkRoleRecord[];
  status: ProjectAssistantStatus;
  workItem: ProjectWorkItemRecord | null;
};

export function ProjectAssistantPanel({
  applyResult,
  context,
  contextError,
  contextStatus,
  error,
  onApply,
  onDismiss,
  onInspectContext,
  onPropose,
  project,
  proposal,
  roles,
  status,
  workItem,
}: Props) {
  const [form, setForm] = useState<ProjectAssistantDraftForm>(() =>
    projectAssistantDraftForm(project, workItem, roles),
  );

  useEffect(() => {
    setForm(projectAssistantDraftForm(project, workItem, roles));
  }, [project, roles, workItem]);

  if (!project) return null;

  const selectedRole =
    form.roleID === PROJECT_ASSISTANT_AUTO
      ? projectAssistantAutoRole(workItem, roles)
      : (roles.find((role) => role.id === form.roleID) ?? null);
  const valid = form.request.trim().length > 0 && (workItem ? Boolean(selectedRole) : true);
  const busy = status === "proposing" || status === "applying";
  const contextBusy = contextStatus === "loading";
  const panelDetail = workItem ? `Selected work: ${workItem.title}` : "Project queue";
  const modelDraftAvailable = Boolean(project.default_model?.trim());

  return (
    <section style={assistantPanelStyle} aria-label="Project Assistant">
      <MiniSectionHeader
        title="Project Assistant"
        detail={panelDetail}
        action={
          proposal || applyResult ? (
            <button className="btn btn-ghost btn-sm" type="button" onClick={onDismiss}>
              <Icon d={Icons.x} size={12} />
              Dismiss
            </button>
          ) : null
        }
      />
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (!valid || busy) return;
          onPropose(form);
        }}
        style={assistantComposerStyle}
      >
        <label style={requestFieldStyle}>
          <span style={fieldLabelStyle}>Request</span>
          <textarea
            className="input"
            rows={workItem ? 2 : 3}
            value={form.request}
            onChange={(event) =>
              setForm((current) => ({ ...current, request: event.target.value }))
            }
            style={assistantRequestInputStyle}
          />
        </label>
        <div style={assistantRouteBarStyle}>
          <div style={assistantRouteFieldsStyle}>
            <label style={routeFieldStyle}>
              <span style={fieldLabelStyle}>Draft</span>
              <select
                className="input"
                value={form.draftMode}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    draftMode: projectAssistantDraftMode(event.target.value, modelDraftAvailable),
                  }))
                }
              >
                <option value="deterministic">Rules</option>
                <option value="bootstrap">Bootstrap</option>
                <option value="model" disabled={!modelDraftAvailable}>
                  Assistant{modelDraftAvailable ? "" : " (set model)"}
                </option>
              </select>
            </label>
            <label style={routeFieldStyle}>
              <span style={fieldLabelStyle}>Run as</span>
              <select
                className="input"
                value={form.roleID}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    roleID: event.target.value,
                  }))
                }
                disabled={roles.length === 0}
              >
                {roles.length === 0 ? (
                  <option value="">No roles</option>
                ) : (
                  <>
                    <option value={PROJECT_ASSISTANT_AUTO}>Auto</option>
                    {roles.map((role) => (
                      <option key={role.id} value={role.id}>
                        {role.name || role.id}
                      </option>
                    ))}
                  </>
                )}
              </select>
            </label>
            <label style={routeFieldStyle}>
              <span style={fieldLabelStyle}>Via</span>
              <select
                className="input"
                value={form.driverKind}
                onChange={(event) =>
                  setForm((current) => ({ ...current, driverKind: event.target.value }))
                }
              >
                <option value={PROJECT_ASSISTANT_AUTO}>Auto</option>
                <option value="hecate_task">Hecate task</option>
                <option value="external_agent">External agent</option>
              </select>
            </label>
          </div>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            disabled={!valid || busy || contextBusy}
            onClick={() => onInspectContext(form)}
          >
            <Icon d={Icons.eye} size={13} />
            {contextBusy ? "Inspecting..." : "Inspect context"}
          </button>
          <button
            className="btn btn-primary btn-sm"
            type="submit"
            disabled={!valid || busy}
            style={assistantSubmitStyle}
          >
            <Icon d={Icons.send} size={13} />
            {status === "proposing" ? "Drafting..." : "Draft proposal"}
          </button>
        </div>
      </form>
      {contextError && (
        <div style={{ marginTop: 2 }}>
          <InlineError message={contextError} />
        </div>
      )}
      {context && <ProjectAssistantContextPanel context={context} />}
      {error && (
        <div style={{ marginTop: 10 }}>
          <InlineError message={error} />
        </div>
      )}
      {proposal && (
        <div style={assistantProposalStyle}>
          <div style={assistantProposalHeaderStyle}>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={titleStyle}>{proposal.title}</div>
              {proposal.summary && (
                <div style={assistantProposalSummaryStyle}>{proposal.summary}</div>
              )}
            </div>
            <span className="badge badge-amber">
              {proposal.requires_confirmation ? "confirmation required" : "ready"}
            </span>
            <span className="badge badge-muted">
              {proposal.actions.length} action{proposal.actions.length === 1 ? "" : "s"}
            </span>
          </div>
          {proposal.trace_id && (
            <div style={metaLineStyle}>
              <span>Trace</span>
              <CopyableID text={proposal.trace_id} compact />
            </div>
          )}
          {proposal.warnings && proposal.warnings.length > 0 && (
            <div style={assistantWarningsStyle}>
              {proposal.warnings.map((warning) => (
                <div key={warning}>{warning}</div>
              ))}
            </div>
          )}
          <div style={assistantActionListStyle}>
            {proposal.actions.map((action, index) => (
              <ProjectAssistantActionRow key={`${action.kind}-${index}`} action={action} />
            ))}
          </div>
          <div style={assistantProposalActionsStyle}>
            <button className="btn btn-ghost btn-sm" type="button" onClick={onDismiss}>
              <Icon d={Icons.x} size={12} />
              Dismiss proposal
            </button>
            <button
              className="btn btn-primary btn-sm"
              type="button"
              disabled={status === "applying"}
              onClick={onApply}
            >
              <Icon d={Icons.check} size={12} />
              {status === "applying" ? "Applying..." : "Apply proposal"}
            </button>
          </div>
        </div>
      )}
      {applyResult && (
        <div style={assistantResultStyle} role="status">
          <span className="badge badge-green">applied</span>
          <span>
            Applied {applyResult.actions.length} action
            {applyResult.actions.length === 1 ? "" : "s"} from {applyResult.proposal_id}.
          </span>
        </div>
      )}
    </section>
  );
}

function ProjectAssistantContextPanel({ context }: { context: ProjectAssistantContextRecord }) {
  const selection = context.selection;
  return (
    <details open style={assistantContextStyle} aria-label="Project Assistant context">
      <summary style={assistantContextSummaryStyle}>
        <span className="badge badge-muted">context</span>
        <span>{projectAssistantSelectionLabel(context)}</span>
      </summary>
      <div style={assistantContextBodyStyle}>
        <div style={subtleTextStyle}>{selection.reason}</div>
        <div style={assistantContextGridStyle}>
          <ProjectAssistantContextStat label="Selected work" value={context.selected_work?.title} />
          <ProjectAssistantContextStat label="Roles" value={String(context.roles.length)} />
          <ProjectAssistantContextStat
            label="Sources"
            value={String(context.project.context_sources?.length ?? 0)}
          />
          <ProjectAssistantContextStat label="Skills" value={String(context.skills?.length ?? 0)} />
          <ProjectAssistantContextStat
            label="Assignments"
            value={String(context.assignments?.length ?? 0)}
          />
          <ProjectAssistantContextStat label="Memory" value={String(context.memory?.length ?? 0)} />
          <ProjectAssistantContextStat
            label="Candidates"
            value={String(context.memory_candidates?.length ?? 0)}
          />
          <ProjectAssistantContextStat
            label="Body tokens"
            value={`~${context.budget.body_tokens_estimate}`}
          />
          <ProjectAssistantContextStat
            label="Truncated"
            value={String(context.budget.body_truncated_count)}
          />
        </div>
      </div>
    </details>
  );
}

function ProjectAssistantContextStat({ label, value }: { label: string; value?: string }) {
  return (
    <div style={assistantContextStatStyle}>
      <span style={fieldLabelStyle}>{label}</span>
      <span style={assistantContextStatValueStyle}>{value || "none"}</span>
    </div>
  );
}

function MiniSectionHeader({
  action,
  detail,
  title,
}: {
  action: ReactNode;
  detail: string;
  title: string;
}) {
  return (
    <div style={domainHeaderStyle}>
      <div style={{ minWidth: 0 }}>
        <div style={sectionLabelStyle}>{title}</div>
        <div style={{ ...subtleTextStyle, marginTop: 3 }}>{detail}</div>
      </div>
      {action && <div style={domainHeaderActionsStyle}>{action}</div>}
    </div>
  );
}

function ProjectAssistantActionRow({
  action,
}: {
  action: ProjectAssistantProposal["actions"][number];
}) {
  const targetEntries = Object.entries(action.target ?? {});
  const patchEntries = Object.entries(action.patch ?? {});
  return (
    <div style={assistantActionStyle}>
      <div style={assistantActionHeaderStyle}>
        <span className="badge badge-muted">{projectAssistantActionLabel(action.kind)}</span>
        {action.reason && <span style={subtleTextStyle}>{action.reason}</span>}
      </div>
      <div style={assistantPatchGridStyle}>
        {targetEntries.length > 0 && (
          <ProjectAssistantFieldGroup title="Target" entries={targetEntries} />
        )}
        {patchEntries.length > 0 && (
          <ProjectAssistantFieldGroup title="Patch" entries={patchEntries} />
        )}
      </div>
    </div>
  );
}

function ProjectAssistantFieldGroup({
  entries,
  title,
}: {
  entries: Array<[string, unknown]>;
  title: string;
}) {
  return (
    <div style={assistantFieldGroupStyle}>
      <div style={sectionLabelStyle}>{title}</div>
      <dl style={assistantFieldsStyle}>
        {entries.map(([key, value]) => (
          <div key={key} style={assistantFieldRowStyle}>
            <dt style={assistantFieldTermStyle}>{key}</dt>
            <dd style={assistantFieldValueStyle}>{formatAssistantValue(value)}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function projectAssistantDraftForm(
  project: ProjectRecord | null,
  workItem: ProjectWorkItemRecord | null,
  roles: ProjectWorkRoleRecord[],
): ProjectAssistantDraftForm {
  const role = roles.find((item) => item.id === workItem?.owner_role_id) ?? roles[0] ?? null;
  const request = workItem
    ? `Queue ${role?.name || role?.id || "role"} for ${workItem.title}`
    : `Plan next work for ${project?.name ?? "project"}\nCapture the next reviewable project task.`;
  return {
    request,
    roleID: roles.length > 0 ? PROJECT_ASSISTANT_AUTO : "",
    driverKind: PROJECT_ASSISTANT_AUTO,
    draftMode: "deterministic",
  };
}

function projectAssistantDraftMode(
  value: string,
  modelDraftAvailable: boolean,
): ProjectAssistantDraftForm["draftMode"] {
  if (value === "bootstrap") return "bootstrap";
  if (value === "model" && modelDraftAvailable) return "model";
  return "deterministic";
}

function projectAssistantAutoRole(
  workItem: ProjectWorkItemRecord | null,
  roles: ProjectWorkRoleRecord[],
): ProjectWorkRoleRecord | null {
  return roles.find((item) => item.id === workItem?.owner_role_id) ?? roles[0] ?? null;
}

function projectAssistantSelectionLabel(context: ProjectAssistantContextRecord): string {
  const role = context.selection.role_name || context.selection.role_id || "no role";
  return `Auto selected ${role} via ${projectAssistantDriverLabel(context.selection.driver_kind)}`;
}

function projectAssistantDriverLabel(kind: string): string {
  switch (kind) {
    case "hecate_task":
      return "Hecate task";
    case "external_agent":
      return "External agent";
    default:
      return kind.replace(/_/g, " ");
  }
}

function projectAssistantActionLabel(kind: string): string {
  switch (kind) {
    case "create_project":
      return "Create project";
    case "update_project":
      return "Update project";
    case "attach_project_root":
      return "Attach root";
    case "remove_project_root":
      return "Remove root";
    case "set_project_defaults":
      return "Set defaults";
    case "move_chat_session":
      return "Move chat";
    case "create_role":
      return "Create role";
    case "create_work_item":
      return "Create work item";
    case "update_work_item":
      return "Update work item";
    case "create_assignment":
      return "Create assignment";
    case "create_handoff":
      return "Create handoff";
    case "create_memory_candidate":
      return "Create memory candidate";
    default:
      return kind.replace(/_/g, " ");
  }
}

function formatAssistantValue(value: unknown): string {
  if (Array.isArray(value)) return value.map(formatAssistantValue).join(", ");
  if (typeof value === "boolean") return value ? "true" : "false";
  if (value === null || value === undefined) return "";
  if (typeof value === "object") {
    try {
      return JSON.stringify(value);
    } catch {
      return String(value);
    }
  }
  return String(value);
}

const assistantPanelStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  boxSizing: "border-box",
  display: "grid",
  gap: 10,
  maxWidth: "100%",
  minWidth: 0,
  padding: 12,
};

const assistantComposerStyle: CSSProperties = {
  display: "grid",
  gap: 10,
  minWidth: 0,
};

const requestFieldStyle: CSSProperties = {
  display: "grid",
  gap: 6,
};

const assistantRequestInputStyle: CSSProperties = {
  lineHeight: 1.45,
  minHeight: 78,
  resize: "vertical",
};

const assistantRouteBarStyle: CSSProperties = {
  alignItems: "end",
  borderTop: "1px solid var(--border)",
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  justifyContent: "space-between",
  minWidth: 0,
  paddingTop: 10,
};

const assistantRouteFieldsStyle: CSSProperties = {
  display: "flex",
  flex: "1 1 320px",
  flexWrap: "wrap",
  gap: 8,
  minWidth: 0,
};

const routeFieldStyle: CSSProperties = {
  display: "grid",
  flex: "1 1 150px",
  gap: 5,
  maxWidth: 260,
  minWidth: 140,
};

const assistantSubmitStyle: CSSProperties = {
  flex: "0 0 auto",
  justifyContent: "center",
  minWidth: 150,
};

const assistantProposalStyle: CSSProperties = {
  background: "var(--bg2)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 10,
  padding: 10,
};

const assistantContextStyle: CSSProperties = {
  background: "var(--bg2)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  minWidth: 0,
  padding: "8px 10px",
};

const assistantContextSummaryStyle: CSSProperties = {
  alignItems: "center",
  color: "var(--t1)",
  cursor: "pointer",
  display: "flex",
  flexWrap: "wrap",
  fontSize: 12,
  gap: 8,
  lineHeight: 1.4,
  minWidth: 0,
};

const assistantContextBodyStyle: CSSProperties = {
  display: "grid",
  gap: 8,
  marginTop: 8,
  minWidth: 0,
};

const assistantContextGridStyle: CSSProperties = {
  display: "grid",
  gap: 6,
  gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 140px), 1fr))",
  minWidth: 0,
};

const assistantContextStatStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 4,
  minWidth: 0,
  padding: "7px 8px",
};

const assistantContextStatValueStyle: CSSProperties = {
  color: "var(--t1)",
  fontSize: 12,
  minWidth: 0,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const assistantProposalHeaderStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  minWidth: 0,
};

const assistantProposalSummaryStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 12,
  lineHeight: 1.45,
  marginTop: 4,
  overflowWrap: "anywhere",
};

const assistantWarningsStyle: CSSProperties = {
  background: "var(--amber-bg)",
  border: "1px solid var(--amber-border)",
  borderRadius: "var(--radius-sm)",
  color: "var(--amber)",
  display: "grid",
  fontSize: 12,
  gap: 4,
  padding: "8px 9px",
};

const assistantActionListStyle: CSSProperties = {
  display: "grid",
  gap: 8,
};

const assistantActionStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 8,
  minWidth: 0,
  padding: 10,
};

const assistantActionHeaderStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  minWidth: 0,
};

const assistantPatchGridStyle: CSSProperties = {
  display: "grid",
  gap: 8,
  gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 240px), 1fr))",
  minWidth: 0,
};

const assistantFieldGroupStyle: CSSProperties = {
  display: "grid",
  gap: 6,
  minWidth: 0,
};

const assistantFieldsStyle: CSSProperties = {
  display: "grid",
  gap: 4,
  margin: 0,
  minWidth: 0,
};

const assistantFieldRowStyle: CSSProperties = {
  color: "var(--t2)",
  display: "grid",
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  gap: 6,
  gridTemplateColumns: "minmax(90px, 0.45fr) minmax(0, 1fr)",
  minWidth: 0,
};

const assistantFieldTermStyle: CSSProperties = {
  color: "var(--t3)",
  margin: 0,
  minWidth: 0,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const assistantFieldValueStyle: CSSProperties = {
  color: "var(--t1)",
  margin: 0,
  minWidth: 0,
  overflowWrap: "anywhere",
};

const assistantProposalActionsStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  justifyContent: "flex-end",
};

const assistantResultStyle: CSSProperties = {
  alignItems: "center",
  background: "var(--green-bg)",
  border: "1px solid var(--green-border)",
  borderRadius: "var(--radius-sm)",
  color: "var(--green)",
  display: "flex",
  flexWrap: "wrap",
  fontSize: 12,
  gap: 8,
  padding: "8px 9px",
};

const domainHeaderStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "flex",
  gap: 10,
  justifyContent: "space-between",
  minWidth: 0,
};

const domainHeaderActionsStyle: CSSProperties = {
  display: "flex",
  flexShrink: 0,
  gap: 8,
};

const sectionLabelStyle: CSSProperties = {
  color: "var(--teal)",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  letterSpacing: 0,
  textTransform: "uppercase",
};

const titleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 13,
  fontWeight: 600,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const subtleTextStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 12,
  lineHeight: 1.4,
};

const metaLineStyle: CSSProperties = {
  color: "var(--t3)",
  display: "flex",
  flexWrap: "wrap",
  fontSize: 11,
  gap: 8,
  marginTop: 6,
};

const fieldLabelStyle: CSSProperties = {
  color: "var(--t2)",
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  textTransform: "uppercase",
};

import { useState, type CSSProperties } from "react";

import { formatAbsoluteTime } from "../../lib/format";
import type {
  ProjectContextSourceRecord,
  ProjectMemoryCandidateRecord,
  ProjectMemoryRecord,
  ProjectRecord,
} from "../../types/project";
import { CopyableID, Icon, Icons, InlineError, Modal } from "../shared/ui";
import {
  isLinkableSourceLocator,
  PROJECT_SOURCE_KINDS,
  projectSourceDefaultFormat,
  projectSourceFormFromRecord,
  sourceKindLabel,
  type ProjectSourceForm,
} from "./projectSources";
import { shortID } from "./projectUtils";

export type MemoryForm = {
  title: string;
  body: string;
  trustLabel: string;
  sourceKind: string;
  sourceID: string;
  enabled: boolean;
};

const MEMORY_TRUST_LABELS = [
  "operator_memory",
  "generated_summary",
  "handoff",
  "external_untrusted",
  "runtime_state",
];
const MEMORY_SOURCE_KINDS = [
  "operator",
  "generated",
  "generated_summary",
  "task_output",
  "chat_message",
  "handoff",
  "project_launch_context",
  "external_handoff",
];

type ProjectMemoryPanelProps = {
  candidates: ProjectMemoryCandidateRecord[];
  discoveringContext: boolean;
  entries: ProjectMemoryRecord[];
  error: string;
  loading: boolean;
  onDiscoverContextSources: () => void;
  onDeleteSource: (source: ProjectContextSourceRecord) => void;
  onEditSource: (source: ProjectContextSourceRecord) => void;
  onPromoteCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onRejectCandidate: (candidate: ProjectMemoryCandidateRecord) => void;
  onDelete: (entry: ProjectMemoryRecord) => void;
  onEdit: (entry: ProjectMemoryRecord) => void;
  onNew: () => void;
  onNewSource: () => void;
  onRefresh: () => void;
  project: ProjectRecord | null;
  rejectingCandidateID: string;
};

export function ProjectMemoryPanel({
  candidates,
  discoveringContext,
  entries,
  error,
  loading,
  onDiscoverContextSources,
  onDeleteSource,
  onEditSource,
  onPromoteCandidate,
  onRejectCandidate,
  onDelete,
  onEdit,
  onNew,
  onNewSource,
  onRefresh,
  project,
  rejectingCandidateID,
}: ProjectMemoryPanelProps) {
  if (!project) return null;
  const enabledCount = entries.filter((entry) => entry.enabled).length;
  const pendingCount = candidates.filter((candidate) => candidate.status === "pending").length;
  const contextSources = project.context_sources ?? [];
  const enabledSourceCount = contextSources.filter((source) => source.enabled).length;
  return (
    <div>
      <div style={panelStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
          <div>
            <div style={sectionLabelStyle}>Memory / Context</div>
            <div style={{ ...subtleTextStyle, marginTop: 3 }}>
              {loading
                ? "Loading project memory…"
                : `${enabledSourceCount}/${contextSources.length} sources · ${enabledCount}/${entries.length} memory · ${pendingCount} pending`}
            </div>
          </div>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-label="Refresh project memory"
            title="Refresh"
            onClick={onRefresh}
            style={{ marginLeft: "auto" }}
          >
            <Icon d={Icons.refresh} size={12} />
          </button>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            disabled={discoveringContext}
            onClick={onDiscoverContextSources}
          >
            <Icon d={Icons.search} size={12} />
            {discoveringContext ? "Discovering…" : "Discover"}
          </button>
          <button className="btn btn-ghost btn-sm" type="button" onClick={onNewSource}>
            <Icon d={Icons.plus} size={12} />
            Source
          </button>
          <button className="btn btn-primary btn-sm" type="button" onClick={onNew}>
            <Icon d={Icons.plus} size={12} />
            Memory
          </button>
        </div>
        {error && (
          <div style={{ marginBottom: 10 }}>
            <InlineError message={error} />
          </div>
        )}
        <div style={{ display: "grid", gap: 8, marginBottom: 12 }}>
          <div style={sectionLabelStyle}>Project sources</div>
          {contextSources.length === 0 ? (
            <div style={subtleTextStyle}>
              No project sources yet. Add URLs, notes, local paths, or discover workspace guidance
              from configured roots.
            </div>
          ) : (
            contextSources.map((source) => (
              <ProjectContextSourceRow
                key={source.id}
                source={source}
                onDelete={() => onDeleteSource(source)}
                onEdit={() => onEditSource(source)}
              />
            ))
          )}
        </div>
        {candidates.length > 0 && (
          <div style={{ display: "grid", gap: 8, marginBottom: 12 }}>
            <div style={sectionLabelStyle}>Candidates</div>
            {candidates.map((candidate) => (
              <ProjectMemoryCandidateRow
                key={candidate.id}
                candidate={candidate}
                pendingReject={rejectingCandidateID === candidate.id}
                onPromote={() => onPromoteCandidate(candidate)}
                onReject={() => onRejectCandidate(candidate)}
              />
            ))}
          </div>
        )}
        {entries.length === 0 && !loading ? (
          <div style={subtleTextStyle}>No project memory entries saved yet.</div>
        ) : (
          <div style={{ display: "grid", gap: 8 }}>
            {entries.map((entry) => (
              <ProjectMemoryRow
                key={entry.id}
                entry={entry}
                onDelete={() => onDelete(entry)}
                onEdit={() => onEdit(entry)}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

type ProjectSourceModalProps = {
  error: string;
  pending: boolean;
  source: ProjectContextSourceRecord | null;
  onClose: () => void;
  onSave: (form: ProjectSourceForm) => void | Promise<void>;
};

export function ProjectSourceModal({
  error,
  pending,
  source,
  onClose,
  onSave,
}: ProjectSourceModalProps) {
  const [form, setForm] = useState<ProjectSourceForm>(() => projectSourceFormFromRecord(source));
  const locatorRequired = form.kind.trim() !== "note";
  const valid =
    form.title.trim().length > 0 &&
    (form.locator.trim().length > 0 || (!locatorRequired && form.note.trim().length > 0));
  return (
    <Modal
      title={source ? "Edit project source" : "New project source"}
      onClose={onClose}
      width={620}
      footer={
        <button
          className="btn btn-primary"
          type="button"
          disabled={pending || !valid}
          onClick={() => void onSave(form)}
          style={{ width: "100%", justifyContent: "center" }}
        >
          {pending ? "Saving…" : source ? "Save source" : "Create source"}
        </button>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (valid) void onSave(form);
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Kind</span>
            <select
              className="input"
              value={form.kind}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  kind: event.target.value,
                  format: projectSourceDefaultFormat(event.target.value),
                }))
              }
            >
              {PROJECT_SOURCE_KINDS.map((kind) => (
                <option key={kind} value={kind}>
                  {sourceKindLabel(kind)}
                </option>
              ))}
              {source?.kind &&
                !(PROJECT_SOURCE_KINDS as readonly string[]).includes(source.kind) && (
                  <option value={source.kind}>{source.kind}</option>
                )}
            </select>
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Enabled</span>
            <select
              className="input"
              value={form.enabled ? "true" : "false"}
              onChange={(event) =>
                setForm((current) => ({ ...current, enabled: event.target.value === "true" }))
              }
            >
              <option value="true">Enabled</option>
              <option value="false">Disabled</option>
            </select>
          </label>
        </div>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Title</span>
          <input
            className="input"
            autoFocus
            value={form.title}
            onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))}
            placeholder="Design brief, customer interview, source note"
          />
        </label>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Locator</span>
          <input
            className="input"
            value={form.locator}
            onChange={(event) =>
              setForm((current) => ({ ...current, locator: event.target.value }))
            }
            placeholder="https://…, /local/path, ticket id, or leave blank for note"
          />
        </label>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Note</span>
          <textarea
            className="input"
            value={form.note}
            rows={4}
            onChange={(event) => setForm((current) => ({ ...current, note: event.target.value }))}
            placeholder="Optional operator note about why this source matters. Stored as source metadata, not injected as memory."
            style={{ resize: "vertical", minHeight: 92 }}
          />
        </label>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Trust label</span>
            <input
              className="input"
              value={form.trustLabel}
              onChange={(event) =>
                setForm((current) => ({ ...current, trustLabel: event.target.value }))
              }
              placeholder="operator_source"
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Category</span>
            <input
              className="input"
              value={form.sourceCategory}
              onChange={(event) =>
                setForm((current) => ({ ...current, sourceCategory: event.target.value }))
              }
              placeholder="operator_source"
            />
          </label>
        </div>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Format</span>
            <input
              className="input"
              value={form.format}
              onChange={(event) =>
                setForm((current) => ({ ...current, format: event.target.value }))
              }
              placeholder="url, text, agents_md"
            />
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Scope</span>
            <input
              className="input"
              value={form.scope}
              onChange={(event) =>
                setForm((current) => ({ ...current, scope: event.target.value }))
              }
              placeholder="project, workspace, path:docs"
            />
          </label>
        </div>
      </form>
    </Modal>
  );
}

type ProjectMemoryModalProps = {
  candidate?: ProjectMemoryCandidateRecord | null;
  entry: ProjectMemoryRecord | null;
  error: string;
  pending: boolean;
  onClose: () => void;
  onSave: (form: MemoryForm) => void | Promise<void>;
};

export function ProjectMemoryModal({
  candidate,
  entry,
  error,
  pending,
  onClose,
  onSave,
}: ProjectMemoryModalProps) {
  const [form, setForm] = useState<MemoryForm>(() =>
    candidate ? memoryFormFromCandidate(candidate) : memoryFormFromRecord(entry),
  );
  const valid = form.title.trim().length > 0 && form.body.trim().length > 0;
  const isCandidate = Boolean(candidate);
  const candidateSourceRefs = candidate ? formatCandidateSourceRefs(candidate) : [];
  return (
    <Modal
      title={
        isCandidate
          ? "Promote memory candidate"
          : entry
            ? "Edit project memory"
            : "New project memory"
      }
      onClose={onClose}
      width={620}
      footer={
        <button
          className="btn btn-primary"
          type="button"
          disabled={pending || !valid}
          onClick={() => void onSave(form)}
          style={{ width: "100%", justifyContent: "center" }}
        >
          {pending
            ? "Saving…"
            : isCandidate
              ? "Promote memory"
              : entry
                ? "Save memory"
                : "Create memory"}
        </button>
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (valid) void onSave(form);
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
        {candidate && (
          <div
            style={{
              background: "var(--bg2)",
              border: "1px solid var(--border)",
              borderRadius: "var(--radius-sm)",
              display: "grid",
              gap: 6,
              padding: "9px 10px",
            }}
          >
            <div style={sectionLabelStyle}>Candidate provenance</div>
            <div style={metaLineStyle}>
              <span>{formatCandidateSource(candidate)}</span>
              <span>{candidate.suggested_trust_label}</span>
              <span>{candidate.status}</span>
            </div>
            {candidateSourceRefs.length > 0 && (
              <div style={{ ...subtleTextStyle }}>
                Source refs: {candidateSourceRefs.join(" · ")}
              </div>
            )}
          </div>
        )}
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Title</span>
          <input
            className="input"
            autoFocus
            value={form.title}
            onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))}
          />
        </label>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Body</span>
          <textarea
            className="input"
            value={form.body}
            rows={7}
            onChange={(event) => setForm((current) => ({ ...current, body: event.target.value }))}
            style={{ resize: "vertical", minHeight: 150 }}
          />
        </label>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Trust label</span>
            <select
              className="input"
              value={form.trustLabel}
              onChange={(event) =>
                setForm((current) => ({ ...current, trustLabel: event.target.value }))
              }
            >
              {MEMORY_TRUST_LABELS.map((label) => (
                <option key={label} value={label}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Source kind</span>
            <select
              className="input"
              value={form.sourceKind}
              onChange={(event) =>
                setForm((current) => ({ ...current, sourceKind: event.target.value }))
              }
            >
              {MEMORY_SOURCE_KINDS.map((kind) => (
                <option key={kind} value={kind}>
                  {kind}
                </option>
              ))}
            </select>
          </label>
        </div>
        <label style={fieldStyle}>
          <span style={fieldLabelStyle}>Source ID</span>
          <input
            className="input"
            value={form.sourceID}
            onChange={(event) =>
              setForm((current) => ({ ...current, sourceID: event.target.value }))
            }
            placeholder="optional artifact, chat, message, or handoff id"
          />
        </label>
        <label style={{ display: "flex", alignItems: "center", gap: 8, color: "var(--t1)" }}>
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(event) =>
              setForm((current) => ({ ...current, enabled: event.target.checked }))
            }
          />
          Enabled for project context packets
        </label>
      </form>
    </Modal>
  );
}

function ProjectContextSourceRow({
  source,
  onDelete,
  onEdit,
}: {
  source: ProjectContextSourceRecord;
  onDelete: () => void;
  onEdit: () => void;
}) {
  const host = source.metadata?.host;
  const note = source.metadata?.note;
  const title = source.title || source.path;
  return (
    <div style={memoryEntryStyle}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <span
          className={
            source.kind === "workspace_instruction" ? "badge badge-green" : "badge badge-muted"
          }
        >
          {sourceKindLabel(source.kind)}
        </span>
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{title}</div>
        <span className={source.enabled ? "badge badge-muted" : "badge badge-amber"}>
          {source.enabled ? "enabled" : "disabled"}
        </span>
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          aria-label={`Edit source ${title}`}
          onClick={onEdit}
          title="Edit"
        >
          <Icon d={Icons.edit} size={12} />
        </button>
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          aria-label={`Delete source ${title}`}
          onClick={onDelete}
          title="Delete"
          style={{ color: "var(--red)" }}
        >
          <Icon d={Icons.trash} size={12} />
        </button>
      </div>
      {note && <div style={memoryBodyStyle}>{note}</div>}
      <div style={metaLineStyle}>
        {isLinkableSourceLocator(source.path) ? (
          <a href={source.path} target="_blank" rel="noreferrer">
            {source.path}
          </a>
        ) : (
          <span>{source.path}</span>
        )}
        {source.format && <span>{source.format}</span>}
        {source.scope && <span>{source.scope}</span>}
        {source.trust_label && <span>{source.trust_label}</span>}
        {host && <span>{host}</span>}
      </div>
    </div>
  );
}

function ProjectMemoryRow({
  entry,
  onDelete,
  onEdit,
}: {
  entry: ProjectMemoryRecord;
  onDelete: () => void;
  onEdit: () => void;
}) {
  const source = formatMemorySource(entry);
  return (
    <div style={memoryEntryStyle}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <span className={entry.enabled ? "badge badge-muted" : "badge badge-amber"}>
          {entry.enabled ? "enabled" : "disabled"}
        </span>
        <span className="badge badge-muted">{entry.trust_label}</span>
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{entry.title}</div>
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          aria-label={`Edit memory ${entry.title}`}
          onClick={onEdit}
          title="Edit"
        >
          <Icon d={Icons.edit} size={12} />
        </button>
        <button
          className="btn btn-ghost btn-sm"
          type="button"
          aria-label={`Delete memory ${entry.title}`}
          onClick={onDelete}
          title="Delete"
          style={{ color: "var(--red)" }}
        >
          <Icon d={Icons.trash} size={12} />
        </button>
      </div>
      <div style={memoryBodyStyle}>{entry.body}</div>
      <div style={metaLineStyle}>
        <span>{source}</span>
        <span>Updated {formatAbsoluteTime(entry.updated_at)}</span>
        <CopyableID text={entry.id} compact />
      </div>
    </div>
  );
}

function ProjectMemoryCandidateRow({
  candidate,
  onPromote,
  onReject,
  pendingReject,
}: {
  candidate: ProjectMemoryCandidateRecord;
  onPromote: () => void;
  onReject: () => void;
  pendingReject: boolean;
}) {
  const source = formatCandidateSource(candidate);
  const sourceRefs = formatCandidateSourceRefs(candidate);
  const pending = candidate.status === "pending";
  return (
    <div style={memoryEntryStyle}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <span className={pending ? "badge badge-amber" : "badge badge-muted"}>
          {candidate.status}
        </span>
        <span className="badge badge-muted">{candidate.suggested_trust_label}</span>
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{candidate.title}</div>
        {pending && (
          <>
            <button
              className="btn btn-primary btn-sm"
              type="button"
              aria-label={`Promote memory candidate ${candidate.title}`}
              onClick={onPromote}
              title="Promote"
            >
              <Icon d={Icons.check} size={12} />
            </button>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              aria-label={`Reject memory candidate ${candidate.title}`}
              disabled={pendingReject}
              onClick={onReject}
              title="Reject"
              style={{ color: "var(--red)" }}
            >
              <Icon d={Icons.x} size={12} />
            </button>
          </>
        )}
      </div>
      <div style={memoryBodyStyle}>{candidate.body}</div>
      <div style={metaLineStyle}>
        <span>{source}</span>
        <span>Suggested {formatAbsoluteTime(candidate.created_at)}</span>
        <CopyableID text={candidate.id} compact />
      </div>
      {sourceRefs.length > 0 && (
        <div style={{ ...subtleTextStyle, marginTop: 6 }}>
          Source refs: {sourceRefs.join(" · ")}
        </div>
      )}
    </div>
  );
}

function memoryFormFromRecord(entry: ProjectMemoryRecord | null): MemoryForm {
  return {
    title: entry?.title ?? "",
    body: entry?.body ?? "",
    trustLabel: entry?.trust_label ?? "operator_memory",
    sourceKind: entry?.source_kind ?? "operator",
    sourceID: entry?.source_id ?? "",
    enabled: entry?.enabled ?? true,
  };
}

function memoryFormFromCandidate(candidate: ProjectMemoryCandidateRecord): MemoryForm {
  return {
    title: candidate.title,
    body: candidate.body,
    trustLabel: candidate.suggested_trust_label || "generated_summary",
    sourceKind: candidate.suggested_source_kind || "generated",
    sourceID: candidate.suggested_source_id ?? "",
    enabled: true,
  };
}

function formatMemorySource(entry: ProjectMemoryRecord): string {
  const sourceKind = entry.source_kind || "operator";
  return entry.source_id ? `${sourceKind}:${entry.source_id}` : sourceKind;
}

function formatCandidateSource(candidate: ProjectMemoryCandidateRecord): string {
  const refs = candidate.source_refs ?? [];
  if (refs.length > 0) {
    const ref = refs[0];
    const label = ref.title || ref.id || ref.kind;
    const suffix = refs.length > 1 ? ` +${refs.length - 1}` : "";
    return `${ref.kind}:${label}${suffix}`;
  }
  const sourceKind = candidate.suggested_source_kind || "generated";
  return candidate.suggested_source_id
    ? `${sourceKind}:${candidate.suggested_source_id}`
    : sourceKind;
}

function formatCandidateSourceRefs(candidate: ProjectMemoryCandidateRecord): string[] {
  return (candidate.source_refs ?? [])
    .map((ref) => {
      const label = ref.title || (ref.id ? shortID(ref.id) : ref.url || "");
      if (!label) return ref.kind;
      return `${ref.kind} ${label}`;
    })
    .filter(Boolean);
}

const sectionLabelStyle: CSSProperties = {
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  color: "var(--teal)",
  letterSpacing: "0.06em",
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
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  color: "var(--t3)",
  fontSize: 11,
  marginTop: 6,
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

const memoryBodyStyle: CSSProperties = {
  marginTop: 6,
  color: "var(--t1)",
  fontSize: 12,
  lineHeight: 1.45,
  whiteSpace: "pre-wrap",
  overflowWrap: "anywhere",
};

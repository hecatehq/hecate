import { useState, type CSSProperties } from "react";

import { formatAbsoluteTime } from "../../lib/format";
import type {
  ProjectContextSourceRecord,
  ProjectMemoryCandidateRecord,
  ProjectMemoryRecord,
  ProjectRecord,
} from "../../types/project";
import { MarkdownContent } from "../shared/MarkdownContent";
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
  const pendingCandidates = candidates.filter((candidate) => candidate.status === "pending");
  const resolvedCandidates = candidates.filter((candidate) => candidate.status !== "pending");
  const firstPendingCandidate = pendingCandidates[0] ?? null;
  const pendingCount = pendingCandidates.length;
  const contextSources = project.context_sources ?? [];
  const enabledSourceCount = contextSources.filter((source) => source.enabled).length;
  const enabledSourceSummary = `${enabledSourceCount} source${enabledSourceCount === 1 ? "" : "s"} enabled`;
  const canDiscoverSources = project.roots.some((root) => root.active && root.path.trim());
  return (
    <section aria-busy={loading} aria-label="Project memory" style={panelStyle}>
      <header className="project-support-header" style={supportHeaderStyle}>
        <div style={{ minWidth: 0 }}>
          <h1 style={surfaceTitleStyle}>Memory</h1>
          <div aria-live="polite" role="status" style={{ ...subtleTextStyle, marginTop: 3 }}>
            {loading
              ? "Loading project memory…"
              : `${entries.length} saved · ${enabledCount} enabled · ${pendingCount} to review · ${enabledSourceSummary}`}
          </div>
        </div>
        <div style={supportActionsStyle}>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            aria-label="Refresh project memory"
            title="Refresh"
            onClick={onRefresh}
          >
            <Icon d={Icons.refresh} size={12} />
          </button>
          <button
            className="btn btn-primary btn-sm"
            type="button"
            disabled={
              Boolean(firstPendingCandidate) && rejectingCandidateID === firstPendingCandidate?.id
            }
            onClick={() =>
              firstPendingCandidate ? onPromoteCandidate(firstPendingCandidate) : onNew()
            }
          >
            <Icon d={firstPendingCandidate ? Icons.check : Icons.plus} size={12} />
            {firstPendingCandidate ? "Review first suggestion" : "Add memory"}
          </button>
        </div>
      </header>
      {error && (
        <div style={{ marginBottom: 10 }}>
          <InlineError message={error} />
        </div>
      )}
      {pendingCandidates.length > 0 && (
        <section aria-labelledby="project-memory-suggestions" style={contentSectionStyle}>
          <div style={sectionHeadingRowStyle}>
            <div>
              <h2 id="project-memory-suggestions" style={sectionHeadingStyle}>
                Suggestions to review
              </h2>
              <div style={{ ...subtleTextStyle, marginTop: 2 }}>
                Nothing is saved until you review and confirm it.
              </div>
            </div>
            {pendingCount > 0 && <span className="badge badge-amber">{pendingCount} pending</span>}
          </div>
          <div style={{ display: "grid", gap: 8 }}>
            {pendingCandidates.map((candidate) => (
              <ProjectMemoryCandidateRow
                key={candidate.id}
                candidate={candidate}
                pendingReject={rejectingCandidateID === candidate.id}
                onPromote={() => onPromoteCandidate(candidate)}
                onReject={() => onRejectCandidate(candidate)}
              />
            ))}
          </div>
        </section>
      )}
      {resolvedCandidates.length > 0 && (
        <details className="project-support-collection" style={collectionDetailsStyle}>
          <summary>
            <span>Reviewed suggestions</span>
            <span style={collectionSummaryMetaStyle}>{resolvedCandidates.length}</span>
          </summary>
          <div style={{ ...collectionBodyStyle, display: "grid", gap: 8 }}>
            {resolvedCandidates.map((candidate) => (
              <ProjectMemoryCandidateRow
                key={candidate.id}
                candidate={candidate}
                pendingReject={false}
                onPromote={() => onPromoteCandidate(candidate)}
                onReject={() => onRejectCandidate(candidate)}
              />
            ))}
          </div>
        </details>
      )}
      <section aria-labelledby="project-saved-memory" style={contentSectionStyle}>
        <div style={sectionHeadingRowStyle}>
          <div>
            <h2 id="project-saved-memory" style={sectionHeadingStyle}>
              Saved memory
            </h2>
            <div style={{ ...subtleTextStyle, marginTop: 2 }}>
              Confirmed guidance available to this project.
            </div>
          </div>
          {firstPendingCandidate && (
            <button className="btn btn-ghost btn-sm" type="button" onClick={onNew}>
              <Icon d={Icons.plus} size={12} />
              Add memory
            </button>
          )}
        </div>
        {entries.length === 0 && !loading ? (
          <div style={emptyStateStyle}>
            No saved memory yet. Add only durable guidance the operator has confirmed.
          </div>
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
      </section>
      <details className="project-support-collection" style={collectionDetailsStyle}>
        <summary>
          <span>Sources</span>
          <span style={collectionSummaryMetaStyle}>
            {enabledSourceCount} enabled · {contextSources.length} total
          </span>
        </summary>
        <div style={collectionBodyStyle}>
          <div className="project-support-actions" style={supportActionsStyle}>
            <button className="btn btn-ghost btn-sm" type="button" onClick={onNewSource}>
              <Icon d={Icons.plus} size={12} />
              Add source
            </button>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              disabled={discoveringContext || !canDiscoverSources}
              onClick={onDiscoverContextSources}
              title={
                canDiscoverSources
                  ? "Find guidance in attached folders"
                  : "Attach or enable a folder first"
              }
            >
              <Icon d={Icons.search} size={12} />
              {discoveringContext ? "Finding…" : "Find from folders"}
            </button>
          </div>
          {!canDiscoverSources && (
            <div style={guidanceStyle}>
              Attach or enable a folder to find local guidance. Notes and links can be added without
              local files.
            </div>
          )}
          {contextSources.length === 0 ? (
            <div style={emptyStateStyle}>
              Sources are optional. Add a note, link, or local path when the project needs reference
              material.
            </div>
          ) : (
            <div style={{ display: "grid", gap: 8 }}>
              {contextSources.map((source) => (
                <ProjectContextSourceRow
                  key={source.id}
                  source={source}
                  onDelete={() => onDeleteSource(source)}
                  onEdit={() => onEditSource(source)}
                />
              ))}
            </div>
          )}
        </div>
      </details>
    </section>
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
      dismissible={!pending}
      width={620}
      footer={
        <button
          className="btn btn-primary"
          type="button"
          disabled={pending || !valid}
          onClick={() => {
            if (!pending) void onSave(form);
          }}
          style={{ width: "100%", justifyContent: "center" }}
        >
          {pending ? "Saving…" : source ? "Save source" : "Create source"}
        </button>
      }
    >
      <form
        aria-busy={pending}
        onSubmit={(event) => {
          event.preventDefault();
          if (!pending && valid) void onSave(form);
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
        <fieldset disabled={pending} style={modalFieldsetStyle}>
          <div className="project-support-form-grid" style={twoColumnFormStyle}>
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
              onChange={(event) =>
                setForm((current) => ({ ...current, title: event.target.value }))
              }
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
          <details className="project-work-advanced-fields">
            <summary>Advanced source details</summary>
            <div style={{ display: "grid", gap: 10, paddingTop: 10 }}>
              <div className="project-support-form-grid" style={twoColumnFormStyle}>
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
              <div className="project-support-form-grid" style={twoColumnFormStyle}>
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
            </div>
          </details>
        </fieldset>
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
  const candidateEvidence = candidate
    ? (candidateSourceRefs[0] ?? formatCandidateSource(candidate))
    : "";
  return (
    <Modal
      title={
        isCandidate
          ? "Review memory suggestion"
          : entry
            ? "Edit project memory"
            : "New project memory"
      }
      onClose={onClose}
      dismissible={!pending}
      width={620}
      footer={
        <button
          className="btn btn-primary"
          type="button"
          disabled={pending || !valid}
          onClick={() => {
            if (!pending) void onSave(form);
          }}
          style={{ width: "100%", justifyContent: "center" }}
        >
          {pending
            ? "Saving…"
            : isCandidate
              ? "Save to project memory"
              : entry
                ? "Save memory"
                : "Create memory"}
        </button>
      }
    >
      <form
        aria-busy={pending}
        onSubmit={(event) => {
          event.preventDefault();
          if (!pending && valid) void onSave(form);
        }}
        style={{ display: "grid", gap: 12 }}
      >
        {error && <InlineError message={error} />}
        <fieldset disabled={pending} style={modalFieldsetStyle}>
          {candidate && (
            <div style={candidateReviewCardStyle}>
              <div style={candidateReviewHeaderStyle}>
                <div style={sectionLabelStyle}>Suggested memory</div>
                <span className="badge badge-amber">Needs review</span>
              </div>
              <div style={candidateReviewSummaryStyle}>
                Hecate found this in {candidateEvidence}. Save it only if it should become durable
                project guidance; edit the title or body first if the wording is too broad.
              </div>
              <div style={candidateDecisionGridStyle}>
                <ProjectMemoryCandidateFact
                  label="Type"
                  value={
                    candidate.suggested_kind
                      ? humanMemoryLabel(candidate.suggested_kind)
                      : "Project note"
                  }
                />
                <ProjectMemoryCandidateFact
                  label="Why Hecate suggested it"
                  value={formatCandidateWhy(candidate, candidateSourceRefs)}
                />
                <ProjectMemoryCandidateFact label="Evidence" value={candidateEvidence} />
                <ProjectMemoryCandidateFact
                  label="Trust"
                  value={humanMemoryLabel(candidate.suggested_trust_label)}
                />
                <ProjectMemoryCandidateFact label="Status" value="Pending promotion" />
              </div>
              <details className="project-work-advanced-fields">
                <summary>Evidence and payload details</summary>
                <div style={{ ...rowDetailsBodyStyle, paddingTop: 8 }}>
                  <div style={metaLineStyle}>
                    <span>{formatCandidateSource(candidate)}</span>
                    <span>{candidate.suggested_trust_label}</span>
                    <span>{candidate.status}</span>
                  </div>
                  {candidateSourceRefs.length > 0 && (
                    <div style={{ ...subtleTextStyle, overflowWrap: "anywhere" }}>
                      Source refs: {candidateSourceRefs.join(" · ")}
                    </div>
                  )}
                </div>
              </details>
            </div>
          )}
          <label style={fieldStyle}>
            <span style={fieldLabelStyle}>Title</span>
            <input
              className="input"
              autoFocus
              value={form.title}
              onChange={(event) =>
                setForm((current) => ({ ...current, title: event.target.value }))
              }
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
          <label style={{ display: "flex", alignItems: "center", gap: 8, color: "var(--t1)" }}>
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(event) =>
                setForm((current) => ({ ...current, enabled: event.target.checked }))
              }
            />
            Enabled for this project
          </label>
          <details className="project-work-advanced-fields">
            <summary>Advanced memory details</summary>
            <div style={{ display: "grid", gap: 10, paddingTop: 10 }}>
              <div className="project-support-form-grid" style={twoColumnFormStyle}>
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
            </div>
          </details>
        </fieldset>
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
    <article aria-label={`Source ${title}`} style={memoryEntryStyle}>
      <div className="project-support-row-header" style={rowHeaderStyle}>
        <div
          style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: 8, minWidth: 0 }}
        >
          <span
            className={
              source.kind === "workspace_instruction" ? "badge badge-green" : "badge badge-muted"
            }
          >
            {sourceKindLabel(source.kind)}
          </span>
          <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{title}</div>
        </div>
        <span className={source.enabled ? "badge badge-muted" : "badge badge-amber"}>
          {source.enabled ? "enabled" : "disabled"}
        </span>
      </div>
      {note && <div style={memoryBodyStyle}>{note}</div>}
      {source.path && (
        <div style={{ ...subtleTextStyle, marginTop: 6, overflowWrap: "anywhere" }}>
          {isLinkableSourceLocator(source.path) ? (
            <a href={source.path} target="_blank" rel="noreferrer">
              {source.path}
            </a>
          ) : (
            <span>{source.path}</span>
          )}
        </div>
      )}
      <details className="project-support-details" style={rowDetailsStyle}>
        <summary>Details and actions</summary>
        <div style={rowDetailsBodyStyle}>
          <div style={metaLineStyle}>
            {source.format && <span>{source.format}</span>}
            {source.scope && <span>{source.scope}</span>}
            {source.trust_label && <span>{source.trust_label}</span>}
            {host && <span>{host}</span>}
            <CopyableID text={source.id} compact />
          </div>
          <div style={rowActionStyle}>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              aria-label={`Edit source ${title}`}
              onClick={onEdit}
            >
              <Icon d={Icons.edit} size={12} />
              Edit
            </button>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              aria-label={`Delete source ${title}`}
              onClick={onDelete}
              style={{ color: "var(--red)" }}
            >
              <Icon d={Icons.trash} size={12} />
              Delete
            </button>
          </div>
        </div>
      </details>
    </article>
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
    <article aria-label={`Memory ${entry.title}`} style={memoryEntryStyle}>
      <div className="project-support-row-header" style={rowHeaderStyle}>
        <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{entry.title}</div>
        <span className={entry.enabled ? "badge badge-muted" : "badge badge-amber"}>
          {entry.enabled ? "enabled" : "disabled"}
        </span>
      </div>
      <MarkdownContent content={entry.body} headingStartLevel={3} style={memoryMarkdownStyle} />
      <details className="project-support-details" style={rowDetailsStyle}>
        <summary>Details and actions</summary>
        <div style={rowDetailsBodyStyle}>
          <div style={metaLineStyle}>
            <span>{entry.trust_label}</span>
            <span>{source}</span>
            <span>Updated {formatAbsoluteTime(entry.updated_at)}</span>
            <CopyableID text={entry.id} compact />
          </div>
          <div style={rowActionStyle}>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              aria-label={`Edit memory ${entry.title}`}
              onClick={onEdit}
            >
              <Icon d={Icons.edit} size={12} />
              Edit
            </button>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              aria-label={`Delete memory ${entry.title}`}
              onClick={onDelete}
              style={{ color: "var(--red)" }}
            >
              <Icon d={Icons.trash} size={12} />
              Delete
            </button>
          </div>
        </div>
      </details>
    </article>
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
  const evidenceLabel = sourceRefs[0] ?? source;
  const kindLabel = candidate.suggested_kind
    ? humanMemoryLabel(candidate.suggested_kind)
    : "Project note";
  const trustLabel = humanMemoryLabel(candidate.suggested_trust_label);
  const whyLabel = formatCandidateWhy(candidate, sourceRefs);
  return (
    <article aria-label={`Memory suggestion ${candidate.title}`} style={memoryEntryStyle}>
      <div className="project-support-row-header" style={rowHeaderStyle}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            flex: "1 1 220px",
            flexWrap: "wrap",
            gap: 8,
            minWidth: 0,
          }}
        >
          <span className="badge badge-muted">Suggested memory</span>
          <span className={pending ? "badge badge-amber" : "badge badge-muted"}>
            {pending ? "Needs review" : humanMemoryLabel(candidate.status)}
          </span>
          <div style={{ ...titleStyle, flex: 1, minWidth: 0 }}>{candidate.title}</div>
        </div>
        {pending && (
          <div style={rowActionStyle}>
            <button
              className="btn btn-primary btn-sm"
              type="button"
              aria-label={`Review memory suggestion ${candidate.title} before saving`}
              disabled={pendingReject}
              onClick={onPromote}
            >
              <Icon d={Icons.check} size={12} />
              Review to save
            </button>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              aria-label={`Dismiss memory suggestion ${candidate.title}`}
              disabled={pendingReject}
              onClick={onReject}
              style={{ color: "var(--red)" }}
            >
              <Icon d={Icons.x} size={12} />
              {pendingReject ? "Dismissing…" : "Dismiss"}
            </button>
          </div>
        )}
      </div>
      <MarkdownContent content={candidate.body} headingStartLevel={3} style={memoryMarkdownStyle} />
      <div
        style={candidateDecisionGridStyle}
        aria-label={`Memory suggestion summary ${candidate.title}`}
      >
        <ProjectMemoryCandidateFact label="Type" value={kindLabel} />
        <ProjectMemoryCandidateFact label="Why Hecate suggested it" value={whyLabel} />
        <ProjectMemoryCandidateFact label="Evidence" value={evidenceLabel} />
        <ProjectMemoryCandidateFact label="Trust" value={trustLabel} />
        <ProjectMemoryCandidateFact
          label="Status"
          value={pending ? "Pending promotion" : humanMemoryLabel(candidate.status)}
        />
      </div>
      <details className="project-support-details" style={rowDetailsStyle}>
        <summary>Evidence and payload details</summary>
        <div style={rowDetailsBodyStyle}>
          <div style={metaLineStyle}>
            <span>{candidate.suggested_trust_label}</span>
            <span>{source}</span>
            <span>Suggested {formatAbsoluteTime(candidate.created_at)}</span>
            <CopyableID text={candidate.id} compact />
          </div>
          {sourceRefs.length > 0 && (
            <div style={{ ...subtleTextStyle, overflowWrap: "anywhere" }}>
              Source refs: {sourceRefs.join(" · ")}
            </div>
          )}
        </div>
      </details>
    </article>
  );
}

function ProjectMemoryCandidateFact({ label, value }: { label: string; value: string }) {
  return (
    <div style={candidateFactStyle}>
      <span style={fieldLabelStyle}>{label}</span>
      <span style={candidateFactValueStyle}>{value || "not set"}</span>
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

function formatCandidateWhy(
  candidate: ProjectMemoryCandidateRecord,
  sourceRefs: string[] = formatCandidateSourceRefs(candidate),
): string {
  if (sourceRefs.length > 0) return `Found in ${sourceRefs[0]}`;
  const source = formatCandidateSource(candidate);
  if (source && source !== "generated") return `Found in ${source}`;
  return "Suggested by Project Assistant";
}

function humanMemoryLabel(value?: string): string {
  if (!value) return "not set";
  return value
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/^\w/, (letter) => letter.toUpperCase());
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
  overflowWrap: "anywhere",
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

const candidateReviewCardStyle: CSSProperties = {
  background: "var(--bg2)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 8,
  minWidth: 0,
  padding: "9px 10px",
};

const candidateReviewHeaderStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  gap: 8,
  justifyContent: "space-between",
  minWidth: 0,
};

const candidateReviewSummaryStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 12,
  lineHeight: 1.45,
};

const candidateDecisionGridStyle: CSSProperties = {
  display: "grid",
  gap: 6,
  gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 140px), 1fr))",
  marginTop: 8,
  minWidth: 0,
};

const candidateFactStyle: CSSProperties = {
  background: "var(--bg2)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 3,
  minWidth: 0,
  padding: "7px 8px",
};

const candidateFactValueStyle: CSSProperties = {
  color: "var(--t1)",
  fontSize: 12,
  minWidth: 0,
  overflowWrap: "anywhere",
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

const modalFieldsetStyle: CSSProperties = {
  border: 0,
  display: "grid",
  gap: 12,
  margin: 0,
  minWidth: 0,
  padding: 0,
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

const memoryEntryStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  minWidth: 0,
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

const memoryMarkdownStyle: CSSProperties = {
  ...memoryBodyStyle,
  whiteSpace: "normal",
};

const surfaceTitleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 18,
  fontWeight: 650,
  lineHeight: 1.25,
  margin: 0,
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

const contentSectionStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  display: "grid",
  gap: 10,
  marginTop: 12,
  minWidth: 0,
  paddingTop: 12,
};

const sectionHeadingRowStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "flex",
  gap: 10,
  justifyContent: "space-between",
  minWidth: 0,
};

const sectionHeadingStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 13,
  fontWeight: 650,
  lineHeight: 1.35,
  margin: 0,
};

const emptyStateStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 12,
  lineHeight: 1.45,
  padding: "4px 0",
};

const collectionDetailsStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  color: "var(--t1)",
  fontSize: 13,
  fontWeight: 600,
  marginTop: 12,
  minWidth: 0,
  paddingTop: 10,
};

const collectionSummaryMetaStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 11,
  fontWeight: 400,
  marginLeft: 8,
};

const collectionBodyStyle: CSSProperties = {
  display: "grid",
  gap: 10,
  minWidth: 0,
  paddingTop: 10,
};

const guidanceStyle: CSSProperties = {
  background: "var(--bg2)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  color: "var(--t2)",
  fontSize: 12,
  fontWeight: 400,
  lineHeight: 1.45,
  padding: "9px 10px",
};

const rowHeaderStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "flex",
  gap: 10,
  justifyContent: "space-between",
  minWidth: 0,
};

const rowDetailsStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  color: "var(--t2)",
  fontSize: 12,
  marginTop: 9,
  paddingTop: 7,
};

const rowDetailsBodyStyle: CSSProperties = {
  display: "grid",
  gap: 8,
  minWidth: 0,
  paddingTop: 8,
};

const rowActionStyle: CSSProperties = {
  display: "flex",
  flexShrink: 0,
  flexWrap: "wrap",
  gap: 6,
};

const twoColumnFormStyle: CSSProperties = {
  display: "grid",
  gap: 10,
  gridTemplateColumns: "1fr 1fr",
};

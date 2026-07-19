import {
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
  type RefObject,
} from "react";

import type {
  ProjectAssistantApplyResult,
  ProjectAssistantContextRecord,
  ProjectAssistantProposal,
  ProjectRecord,
  ProjectWorkItemRecord,
  ProjectWorkRoleRecord,
} from "../../types/project";
import { formatAbsoluteTime } from "../../lib/format";
import { CopyableID, Icon, Icons, InlineError } from "../shared/ui";
import {
  HUMAN_ASSIGNMENT_DESCRIPTION,
  PROJECT_ASSIGNMENT_DESTINATIONS,
  projectAssignmentDestinationLabel,
} from "./projectAssignmentDestinations";

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
  chatDraftSource?: ProjectAssistantChatDraftSource | null;
  context: ProjectAssistantContextRecord | null;
  contextError: string;
  contextStatus: ProjectAssistantContextStatus;
  error: string;
  onApply: () => void;
  onBootstrap: () => void;
  onCreateWork?: () => void;
  onDismiss: () => void;
  onInspectContext: (form: ProjectAssistantDraftForm) => void;
  onManageRoles?: () => void;
  onOpenWork?: () => void;
  onOpenSourceChat?: () => void;
  onPropose: (form: ProjectAssistantDraftForm) => void;
  onReviewMemory?: () => void;
  project: ProjectRecord | null;
  proposal: ProjectAssistantProposal | null;
  primaryEmphasis?: boolean;
  roles: ProjectWorkRoleRecord[];
  bootstrapPending: boolean;
  firstWorkReady?: boolean;
  guidedPrimaryActionRef?: RefObject<HTMLButtonElement | null>;
  memoryCandidateCount?: number;
  roleCount?: number;
  setupFirst?: boolean;
  showHeader?: boolean;
  status: ProjectAssistantStatus;
  workItem: ProjectWorkItemRecord | null;
  workItemCount?: number;
};

export type ProjectAssistantChatDraftSource = {
  request?: string;
  sourceSessionID?: string;
  createdAt?: string;
};

export function ProjectAssistantPanel({
  applyResult,
  chatDraftSource,
  context,
  contextError,
  contextStatus,
  error,
  onApply,
  onBootstrap,
  onCreateWork,
  onDismiss,
  onInspectContext,
  onManageRoles,
  onOpenWork,
  onOpenSourceChat,
  onPropose,
  onReviewMemory,
  project,
  proposal,
  primaryEmphasis = true,
  roles,
  bootstrapPending,
  firstWorkReady = false,
  guidedPrimaryActionRef,
  memoryCandidateCount = 0,
  roleCount = roles.length,
  setupFirst = false,
  showHeader = true,
  status,
  workItem,
  workItemCount = workItem ? 1 : 0,
}: Props) {
  const [form, setForm] = useState<ProjectAssistantDraftForm>(() =>
    projectAssistantDraftForm(project, workItem, roles),
  );
  const formContextKey = JSON.stringify([project?.id ?? "", workItem?.id ?? ""]);
  const previousFormContextKeyRef = useRef(formContextKey);
  const previousDerivedFormRef = useRef(projectAssistantDraftForm(project, workItem, roles));
  const proposalTitleRef = useRef<HTMLHeadingElement>(null);
  const previousProposalIDRef = useRef("");

  useEffect(() => {
    const nextDerivedForm = projectAssistantDraftForm(project, workItem, roles);
    const previousDerivedForm = previousDerivedFormRef.current;
    if (previousFormContextKeyRef.current !== formContextKey) {
      previousFormContextKeyRef.current = formContextKey;
      previousDerivedFormRef.current = nextDerivedForm;
      setForm(nextDerivedForm);
      return;
    }
    setForm((current) => {
      if (projectAssistantDraftFormsEqual(current, previousDerivedForm)) {
        return nextDerivedForm;
      }
      if (
        current.roleID === PROJECT_ASSISTANT_AUTO ||
        roles.some((role) => role.id === current.roleID)
      ) {
        return current;
      }
      return {
        ...current,
        roleID: nextDerivedForm.roleID,
      };
    });
    previousDerivedFormRef.current = nextDerivedForm;
  }, [formContextKey, project, roles, workItem]);

  useEffect(() => {
    const proposalID = proposal?.id ?? "";
    if (!setupFirst && proposalID && proposalID !== previousProposalIDRef.current) {
      proposalTitleRef.current?.focus();
    }
    previousProposalIDRef.current = proposalID;
  }, [proposal?.id, setupFirst]);

  if (!project) return null;

  const selectedRole =
    form.roleID === PROJECT_ASSISTANT_AUTO
      ? projectAssistantAutoRole(workItem, roles)
      : (roles.find((role) => role.id === form.roleID) ?? null);
  const valid = form.request.trim().length > 0 && (workItem ? Boolean(selectedRole) : true);
  const busy = status === "proposing" || status === "applying";
  const contextBusy = contextStatus === "loading";
  const bootstrapBusy = bootstrapPending || busy;
  const panelDetail = setupFirst
    ? "Guided setup and first work"
    : workItem
      ? `Selected work: ${workItem.title}`
      : "Project queue";
  const modelDraftAvailable = Boolean(project.default_model?.trim());
  const bootstrapForm = projectAssistantBootstrapForm();
  const showSetupRow = setupFirst && !proposal && !applyResult;
  const showBootstrapAction = setupFirst && !firstWorkReady && !proposal && !applyResult;
  const showSetupRefreshAction = setupFirst && firstWorkReady && !proposal && !applyResult;
  const setupSummary = projectAssistantSetupSummary({
    memoryCandidateCount,
    roleCount,
    workItemCount,
  });

  return (
    <section
      aria-busy={busy || contextBusy}
      aria-label="Project Assistant"
      style={assistantPanelStyle}
    >
      {showHeader && (
        <MiniSectionHeader
          title={setupFirst ? "Guided start" : "Project Assistant"}
          detail={panelDetail}
          action={
            applyResult || (proposal && !setupFirst) ? (
              <button
                className="btn btn-ghost btn-sm"
                type="button"
                disabled={status === "applying"}
                onClick={onDismiss}
              >
                <Icon d={Icons.x} size={12} />
                Dismiss
              </button>
            ) : null
          }
        />
      )}
      {!showHeader && !proposal && (applyResult || error || contextError || context) && (
        <div style={assistantCompactActionsStyle}>
          <button
            className="btn btn-ghost btn-sm"
            type="button"
            disabled={status === "applying"}
            onClick={onDismiss}
          >
            <Icon d={Icons.x} size={12} />
            Dismiss
          </button>
        </div>
      )}
      {showSetupRow && (
        <div style={assistantOnboardingStyle} aria-label="Project onboarding">
          <div style={assistantOnboardingMainStyle}>
            <div style={assistantOnboardingCopyStyle}>
              <div style={sectionLabelStyle}>Next step</div>
              <div style={setupPromptTitleStyle}>
                {firstWorkReady ? "Ready for first work" : "Set up project context"}
              </div>
              <div style={{ ...subtleTextStyle, marginTop: 4 }}>
                {firstWorkReady
                  ? `${setupSummary}. Create a reviewable work item to start coordinating.`
                  : bootstrapPending
                    ? "Finding guidance and skills, then preparing changes for your review."
                    : "Find guidance and skills, suggest roles, and prepare setup changes for review."}
              </div>
            </div>
            <div style={assistantOnboardingActionsStyle}>
              {firstWorkReady && workItemCount === 0 && onCreateWork && (
                <button
                  ref={guidedPrimaryActionRef}
                  className="btn btn-primary btn-sm"
                  type="button"
                  disabled={bootstrapBusy || contextBusy}
                  onClick={onCreateWork}
                >
                  <Icon d={Icons.plus} size={13} />
                  Create first work
                </button>
              )}
              {showBootstrapAction && (
                <button
                  ref={guidedPrimaryActionRef}
                  className="btn btn-primary btn-sm"
                  type="button"
                  disabled={bootstrapBusy || contextBusy}
                  onClick={onBootstrap}
                >
                  <Icon d={Icons.refresh} size={14} />
                  {bootstrapPending ? "Preparing setup…" : "Set up project"}
                </button>
              )}
            </div>
          </div>
          <details style={assistantSetupDetailsStyle}>
            <summary style={assistantSetupSummaryStyle}>Setup details</summary>
            <div style={assistantSetupDetailsBodyStyle}>
              <button
                className="btn btn-ghost btn-sm"
                type="button"
                disabled={bootstrapBusy || contextBusy}
                onClick={() => onInspectContext(bootstrapForm)}
              >
                <Icon d={Icons.eye} size={13} />
                {contextBusy ? "Inspecting…" : "Inspect context"}
              </button>
              {firstWorkReady && memoryCandidateCount > 0 && onReviewMemory && (
                <button
                  className="btn btn-ghost btn-sm"
                  type="button"
                  disabled={bootstrapBusy || contextBusy}
                  onClick={onReviewMemory}
                >
                  <Icon d={Icons.observe} size={13} />
                  Review memory
                </button>
              )}
              {firstWorkReady && roleCount > 0 && onManageRoles && (
                <button
                  className="btn btn-ghost btn-sm"
                  type="button"
                  disabled={bootstrapBusy || contextBusy}
                  onClick={onManageRoles}
                >
                  <Icon d={Icons.user} size={13} />
                  Review roles
                </button>
              )}
              {showSetupRefreshAction && (
                <button
                  className="btn btn-ghost btn-sm"
                  type="button"
                  disabled={bootstrapBusy || contextBusy}
                  onClick={onBootstrap}
                >
                  <Icon d={Icons.refresh} size={13} />
                  {bootstrapPending ? "Refreshing…" : "Refresh setup"}
                </button>
              )}
            </div>
          </details>
        </div>
      )}
      {!setupFirst && (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            if (!valid || busy) return;
            onPropose(form);
          }}
          style={assistantComposerStyle}
        >
          <div style={assistantPrimaryRowStyle}>
            <label style={requestFieldStyle}>
              <span style={fieldLabelStyle}>Request</span>
              <textarea
                className="input"
                rows={workItem ? 1 : 2}
                value={form.request}
                onChange={(event) =>
                  setForm((current) => ({ ...current, request: event.target.value }))
                }
                style={assistantRequestInputStyle}
              />
            </label>
            <div style={assistantPrimaryActionsStyle}>
              <button
                className={`btn ${primaryEmphasis ? "btn-primary" : "btn-ghost"} btn-sm`}
                type="submit"
                disabled={!valid || busy}
                style={assistantSubmitStyle}
              >
                <Icon d={Icons.send} size={13} />
                {status === "proposing" ? "Drafting…" : "Draft proposal"}
              </button>
            </div>
          </div>
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
                  <option value="model" disabled={!modelDraftAvailable}>
                    Assistant{modelDraftAvailable ? "" : " (set model)"}
                  </option>
                </select>
              </label>
              <label style={routeFieldStyle}>
                <span style={fieldLabelStyle}>Responsibility</span>
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
                <span style={fieldLabelStyle}>Work done by</span>
                <select
                  aria-describedby={
                    form.driverKind === "manual"
                      ? "project-assistant-human-destination-help"
                      : undefined
                  }
                  className="input"
                  value={form.driverKind}
                  onChange={(event) =>
                    setForm((current) => ({ ...current, driverKind: event.target.value }))
                  }
                >
                  <option value={PROJECT_ASSISTANT_AUTO}>Auto</option>
                  {PROJECT_ASSIGNMENT_DESTINATIONS.map((destination) => (
                    <option key={destination.kind} value={destination.kind}>
                      {destination.label}
                    </option>
                  ))}
                </select>
                {form.driverKind === "manual" && (
                  <span
                    id="project-assistant-human-destination-help"
                    style={assistantRouteHelpStyle}
                  >
                    {HUMAN_ASSIGNMENT_DESCRIPTION}
                  </span>
                )}
              </label>
            </div>
            <div style={assistantSecondaryActionsStyle}>
              <button
                className="btn btn-ghost btn-sm"
                type="button"
                disabled={!valid || busy || contextBusy}
                onClick={() => onInspectContext(form)}
              >
                <Icon d={Icons.eye} size={13} />
                {contextBusy ? "Inspecting…" : "Inspect context"}
              </button>
            </div>
          </div>
        </form>
      )}
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
              <h3
                ref={proposalTitleRef}
                className="project-assistant-proposal-title"
                style={{ ...titleStyle, margin: 0 }}
                tabIndex={-1}
              >
                {setupFirst ? "Review setup" : proposal.title}
              </h3>
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
          {proposal.warnings && proposal.warnings.length > 0 && (
            <div style={assistantWarningsStyle}>
              {proposal.warnings.map((warning) => (
                <div key={warning}>{warning}</div>
              ))}
            </div>
          )}
          <details style={assistantProposalDetailsStyle}>
            <summary style={assistantProposalDetailsSummaryStyle}>Review proposed changes</summary>
            <div style={assistantProposalDetailsBodyStyle}>
              <div style={subtleTextStyle}>{proposal.title}</div>
              {proposal.trace_id && (
                <div style={metaLineStyle}>
                  <span>Trace</span>
                  <CopyableID text={proposal.trace_id} compact />
                </div>
              )}
              {chatDraftSource && (
                <ProjectAssistantChatDraftSourcePanel
                  source={chatDraftSource}
                  onOpenChat={onOpenSourceChat}
                />
              )}
              <div style={assistantActionListStyle}>
                {proposal.actions.map((action, index) => (
                  <ProjectAssistantActionRow key={`${action.kind}-${index}`} action={action} />
                ))}
              </div>
            </div>
          </details>
          <div style={assistantProposalActionsStyle}>
            <button
              className="btn btn-ghost btn-sm"
              type="button"
              disabled={status === "applying"}
              onClick={onDismiss}
            >
              <Icon d={Icons.x} size={12} />
              {setupFirst ? "Dismiss setup" : "Dismiss proposal"}
            </button>
            <button
              ref={setupFirst ? guidedPrimaryActionRef : undefined}
              className="btn btn-primary btn-sm"
              type="button"
              disabled={status === "applying"}
              onClick={onApply}
            >
              <Icon d={Icons.check} size={12} />
              {status === "applying"
                ? setupFirst
                  ? "Applying setup…"
                  : "Applying…"
                : setupFirst
                  ? "Apply setup"
                  : "Apply proposal"}
            </button>
          </div>
        </div>
      )}
      {applyResult && (
        <ProjectAssistantApplyResultPanel
          result={applyResult}
          memoryCandidateCount={memoryCandidateCount}
          onCreateWork={onCreateWork}
          onManageRoles={onManageRoles}
          onOpenWork={onOpenWork}
          onReviewMemory={onReviewMemory}
          roleCount={roleCount}
          setupFirst={setupFirst}
          guidedPrimaryActionRef={guidedPrimaryActionRef}
          workItemCount={workItemCount}
        />
      )}
    </section>
  );
}

function ProjectAssistantApplyResultPanel({
  guidedPrimaryActionRef,
  memoryCandidateCount,
  onCreateWork,
  onManageRoles,
  onOpenWork,
  onReviewMemory,
  result,
  roleCount,
  setupFirst,
  workItemCount,
}: {
  guidedPrimaryActionRef?: RefObject<HTMLButtonElement | null>;
  memoryCandidateCount: number;
  onCreateWork?: () => void;
  onManageRoles?: () => void;
  onOpenWork?: () => void;
  onReviewMemory?: () => void;
  result: ProjectAssistantApplyResult;
  roleCount: number;
  setupFirst: boolean;
  workItemCount: number;
}) {
  const appliedCount = result.committed_action_count ?? result.actions.length;
  const resultActions = projectAssistantFollowUpActions({
    memoryCandidateCount,
    onCreateWork,
    onManageRoles,
    onOpenWork,
    onReviewMemory,
    result,
    roleCount,
    workItemCount,
  });
  const resultPrimaryActions = setupFirst
    ? resultActions.filter((action) => action.primary)
    : resultActions;
  const resultSupportingActions = setupFirst
    ? resultActions.filter((action) => !action.primary)
    : [];
  const resultSummaryActions = resultPrimaryActions.filter(
    (action) => action.includeInSummary !== false,
  );

  return (
    <div style={assistantResultStyle} role="status" aria-label="Project Assistant apply result">
      <div style={assistantResultSummaryStyle}>
        <span className="badge badge-green">applied</span>
        <div style={assistantResultCopyStyle}>
          <strong style={{ color: "var(--t1)" }}>
            Applied {appliedCount} action{appliedCount === 1 ? "" : "s"}
          </strong>
          <span style={subtleTextStyle}>
            {setupFirst
              ? "Setup changes are applied."
              : `Proposal ${result.proposal_id} is applied.`}
          </span>
        </div>
      </div>
      {resultActions.length > 0 && (
        <div style={assistantResultNextStyle} aria-label="Project Assistant next steps">
          <div style={assistantResultNextCopyStyle}>
            <div style={sectionLabelStyle}>{setupFirst ? "Ready for first work" : "Next up"}</div>
            <div style={subtleTextStyle}>
              {resultSummaryActions.length > 0
                ? projectAssistantNextUpSummary(resultSummaryActions)
                : "Open the work queue"}
            </div>
          </div>
          <div style={assistantResultActionsStyle}>
            {resultPrimaryActions.map((action) => (
              <button
                ref={action.primary ? guidedPrimaryActionRef : undefined}
                key={action.key}
                className={`btn ${action.primary ? "btn-primary" : "btn-ghost"} btn-sm`}
                type="button"
                onClick={action.onClick}
              >
                <Icon d={action.icon} size={13} />
                {action.label}
              </button>
            ))}
          </div>
        </div>
      )}
      {(setupFirst || resultSupportingActions.length > 0) && (
        <details style={assistantSetupDetailsStyle}>
          <summary style={assistantSetupSummaryStyle}>Setup details</summary>
          <div style={assistantSetupDetailsBodyStyle}>
            {setupFirst && (
              <div style={metaLineStyle}>
                <span>Setup reference</span>
                <CopyableID text={result.proposal_id} compact />
              </div>
            )}
            {resultSupportingActions.map((action) => (
              <button
                key={action.key}
                className="btn btn-ghost btn-sm"
                type="button"
                onClick={action.onClick}
              >
                <Icon d={action.icon} size={13} />
                {action.label}
              </button>
            ))}
          </div>
        </details>
      )}
    </div>
  );
}

function ProjectAssistantChatDraftSourcePanel({
  onOpenChat,
  source,
}: {
  onOpenChat?: () => void;
  source: ProjectAssistantChatDraftSource;
}) {
  const createdAt = formatAbsoluteTime(source.createdAt);
  return (
    <div style={assistantSourceStyle} aria-label="Proposal source">
      <div style={assistantSourceHeaderStyle}>
        <span className="badge badge-muted">drafted from chat</span>
        {createdAt && <span style={subtleTextStyle}>{createdAt}</span>}
        {source.sourceSessionID && onOpenChat && (
          <button className="btn btn-ghost btn-sm" type="button" onClick={onOpenChat}>
            <Icon d={Icons.chat} size={12} />
            Open source chat
          </button>
        )}
      </div>
      {source.request && <div style={assistantSourceRequestStyle}>{source.request}</div>}
      {source.sourceSessionID && (
        <div style={metaLineStyle}>
          <span>Chat</span>
          <CopyableID text={source.sourceSessionID} compact />
        </div>
      )}
    </div>
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
  if (action.kind === "create_memory_candidate") {
    return <ProjectAssistantMemorySuggestionAction action={action} />;
  }

  const targetEntries = Object.entries(action.target ?? {});
  const patchEntries = Object.entries(action.patch ?? {});
  const hasFieldDetails = targetEntries.length > 0 || patchEntries.length > 0;
  return (
    <div style={assistantActionStyle}>
      <div style={assistantActionHeaderStyle}>
        <span className="badge badge-muted">{projectAssistantActionLabel(action.kind)}</span>
        {action.reason && <span style={subtleTextStyle}>{action.reason}</span>}
      </div>
      {hasFieldDetails && (
        <details style={assistantTechnicalDetailsStyle}>
          <summary style={assistantTechnicalSummaryStyle}>Show changed fields</summary>
          <div style={assistantPatchGridStyle}>
            {targetEntries.length > 0 && (
              <ProjectAssistantFieldGroup title="Target" entries={targetEntries} />
            )}
            {patchEntries.length > 0 && (
              <ProjectAssistantFieldGroup title="Patch" entries={patchEntries} />
            )}
          </div>
        </details>
      )}
    </div>
  );
}

function ProjectAssistantMemorySuggestionAction({
  action,
}: {
  action: ProjectAssistantProposal["actions"][number];
}) {
  const targetEntries = Object.entries(action.target ?? {});
  const patchEntries = Object.entries(action.patch ?? {});
  const title = assistantStringField(action.patch, "title") || "Memory suggestion";
  const body = assistantStringField(action.patch, "body");
  const kind = assistantHumanLabel(assistantStringField(action.patch, "suggested_kind"));
  const trustLabel = assistantHumanLabel(
    assistantStringField(action.patch, "suggested_trust_label"),
  );
  const sourceID = assistantStringField(action.patch, "suggested_source_id");
  const sourceRefs = assistantSourceRefs(action.patch?.source_refs);
  const sourceLabel =
    sourceRefs[0]?.title ||
    sourceRefs[0]?.id ||
    sourceID ||
    assistantStringField(action.target, "project_id") ||
    "Project";
  const evidenceItems = sourceRefs.length > 0 ? sourceRefs : sourceID ? [{ id: sourceID }] : [];
  const displayTitle = assistantMemorySuggestionTitle(title, sourceLabel);
  const bodyPreview = assistantMemoryBodyPreview(body);

  return (
    <article style={assistantMemoryActionStyle} aria-label={`Memory suggestion ${displayTitle}`}>
      <div style={assistantMemoryHeaderStyle}>
        <span className="badge badge-muted">Save for review</span>
        <div style={assistantMemoryTitleWrapStyle}>
          <div style={sectionLabelStyle}>Memory suggestion</div>
          <h4 style={assistantMemoryTitleStyle}>{displayTitle}</h4>
          <div style={assistantMemoryReasonStyle}>
            {action.reason || "Store guidance as a suggestion you can review before promotion."}
          </div>
        </div>
      </div>
      <figure style={assistantMemoryBodyStyle}>
        <blockquote style={assistantMemoryQuoteStyle}>
          {bodyPreview || "No memory text supplied. Review the source details before applying."}
        </blockquote>
        {body && bodyPreview !== body && (
          <figcaption style={assistantMemoryCaptionStyle}>
            Preview only. Open source and field details for the full proposed text.
          </figcaption>
        )}
      </figure>
      <div style={assistantMemoryMetaGridStyle} aria-label="Memory suggestion summary">
        <ProjectAssistantMemoryMeta label="Source" value={sourceLabel} />
        <ProjectAssistantMemoryMeta label="Review path" value="Pending promotion" />
        <ProjectAssistantMemoryMeta label="Type" value={kind || "Project guidance"} />
        <ProjectAssistantMemoryMeta label="Trust" value={trustLabel || "Review first"} />
      </div>
      {evidenceItems.length > 0 && (
        <div style={assistantMemoryEvidenceStyle} aria-label="Memory suggestion evidence">
          <span style={fieldLabelStyle}>Evidence</span>
          <ul style={assistantMemoryEvidenceListStyle}>
            {evidenceItems.map((item) => (
              <li key={`${item.title ?? ""}-${item.id}`} style={assistantMemoryEvidenceItemStyle}>
                {item.title || item.id}
              </li>
            ))}
          </ul>
        </div>
      )}
      <div style={assistantMemorySafetyStyle}>
        Applying saves a pending suggestion only. It does not change durable project memory until
        you review and promote it.
      </div>
      {(targetEntries.length > 0 || patchEntries.length > 0) && (
        <details style={assistantTechnicalDetailsStyle}>
          <summary style={assistantTechnicalSummaryStyle}>Show source and field details</summary>
          <div style={assistantPatchGridStyle}>
            {targetEntries.length > 0 && (
              <ProjectAssistantFieldGroup title="Target" entries={targetEntries} />
            )}
            {patchEntries.length > 0 && (
              <ProjectAssistantFieldGroup title="Patch" entries={patchEntries} />
            )}
          </div>
        </details>
      )}
    </article>
  );
}

function ProjectAssistantMemoryMeta({ label, value }: { label: string; value: string }) {
  return (
    <div style={assistantMemoryMetaStyle}>
      <span style={fieldLabelStyle}>{label}</span>
      <span style={assistantMemoryMetaValueStyle}>{value}</span>
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

function projectAssistantDraftFormsEqual(
  a: ProjectAssistantDraftForm,
  b: ProjectAssistantDraftForm,
) {
  return (
    a.request === b.request &&
    a.roleID === b.roleID &&
    a.driverKind === b.driverKind &&
    a.draftMode === b.draftMode
  );
}

function projectAssistantDraftMode(
  value: string,
  modelDraftAvailable: boolean,
): ProjectAssistantDraftForm["draftMode"] {
  if (value === "bootstrap") return "bootstrap";
  if (value === "model" && modelDraftAvailable) return "model";
  return "deterministic";
}

function projectAssistantBootstrapForm(): ProjectAssistantDraftForm {
  return {
    request: "Set up project guidance",
    roleID: PROJECT_ASSISTANT_AUTO,
    driverKind: PROJECT_ASSISTANT_AUTO,
    draftMode: "bootstrap",
  };
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
  return projectAssignmentDestinationLabel(kind);
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
    case "update_handoff":
      return "Update handoff";
    case "create_memory_candidate":
      return "Create memory candidate";
    default:
      return kind.replace(/_/g, " ");
  }
}

function projectAssistantFollowUpActions({
  memoryCandidateCount,
  onCreateWork,
  onManageRoles,
  onOpenWork,
  onReviewMemory,
  result,
  roleCount,
  workItemCount,
}: {
  memoryCandidateCount: number;
  onCreateWork?: () => void;
  onManageRoles?: () => void;
  onOpenWork?: () => void;
  onReviewMemory?: () => void;
  result: ProjectAssistantApplyResult;
  roleCount: number;
  workItemCount: number;
}): Array<{
  icon: string | string[];
  includeInSummary?: boolean;
  key: string;
  label: string;
  onClick: () => void;
  primary?: boolean;
}> {
  const appliedKinds = new Set(result.actions.map((action) => action.kind));
  const actions: Array<{
    icon: string | string[];
    includeInSummary?: boolean;
    key: string;
    label: string;
    onClick: () => void;
    primary?: boolean;
  }> = [];

  if ((memoryCandidateCount > 0 || appliedKinds.has("create_memory_candidate")) && onReviewMemory) {
    actions.push({
      icon: Icons.observe,
      key: "review-memory",
      label: "Review memory",
      onClick: onReviewMemory,
    });
  }
  if ((roleCount > 0 || appliedKinds.has("create_role")) && onManageRoles) {
    actions.push({
      icon: Icons.user,
      key: "review-roles",
      label: "Review roles",
      onClick: onManageRoles,
    });
  }
  if (workItemCount === 0 && !appliedKinds.has("create_work_item") && onCreateWork) {
    actions.push({
      icon: Icons.plus,
      key: "create-work",
      label: "Create first work",
      onClick: onCreateWork,
      primary: true,
    });
  } else if (onOpenWork) {
    actions.push({
      icon: Icons.tasks,
      key: "open-work",
      label: "Open work queue",
      onClick: onOpenWork,
      primary: actions.length === 0,
    });
  }
  return actions;
}

function projectAssistantNextUpSummary(actions: Array<{ label: string }>) {
  if (actions.length === 1) return actions[0].label;
  const labels = actions.map((action) => action.label.toLowerCase());
  const last = labels.pop();
  return `${labels.join(", ")}, then ${last}`;
}

function projectAssistantSetupSummary({
  memoryCandidateCount,
  roleCount,
  workItemCount,
}: {
  memoryCandidateCount: number;
  roleCount: number;
  workItemCount: number;
}): string {
  const parts = [
    roleCount > 0 ? `${roleCount} role${roleCount === 1 ? "" : "s"}` : "",
    memoryCandidateCount > 0
      ? `${memoryCandidateCount} memory suggestion${memoryCandidateCount === 1 ? "" : "s"}`
      : "",
    workItemCount > 0 ? `${workItemCount} work item${workItemCount === 1 ? "" : "s"}` : "",
  ].filter(Boolean);
  return parts.length > 0 ? `Project setup has ${parts.join(" · ")}` : "Project setup has begun";
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

function assistantStringField(
  record: Record<string, unknown> | Record<string, string> | undefined,
  key: string,
): string {
  const value = record?.[key];
  return typeof value === "string" ? value : "";
}

function assistantHumanLabel(value: string): string {
  if (!value) return "";
  return value
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/^\w/, (letter) => letter.toUpperCase());
}

function assistantMemorySuggestionTitle(title: string, sourceLabel: string): string {
  const trimmedTitle = title.trim();
  const guidancePrefix = "Guidance source:";
  if (trimmedTitle.toLowerCase().startsWith(guidancePrefix.toLowerCase())) {
    const source = trimmedTitle.slice(guidancePrefix.length).trim() || sourceLabel;
    return `Guidance from ${source}`;
  }
  return trimmedTitle || "Project guidance suggestion";
}

function assistantMemoryBodyPreview(body: string): string {
  const trimmedBody = body.trim();
  if (trimmedBody.length <= 420) return trimmedBody;
  return `${trimmedBody.slice(0, 417).trimEnd()}…`;
}

function assistantSourceRefs(value: unknown): Array<{ id: string; title?: string }> {
  if (!Array.isArray(value)) return [];
  const refs: Array<{ id: string; title?: string }> = [];
  for (const item of value) {
    if (!item || typeof item !== "object") continue;
    const source = item as Record<string, unknown>;
    const id = typeof source.id === "string" ? source.id : "";
    const title = typeof source.title === "string" ? source.title : "";
    if (id || title) refs.push(title ? { id, title } : { id });
  }
  return refs;
}

const assistantPanelStyle: CSSProperties = {
  background: "var(--bg0)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  boxSizing: "border-box",
  display: "grid",
  gap: 8,
  maxWidth: "100%",
  minWidth: 0,
  padding: "10px 12px",
};

const assistantComposerStyle: CSSProperties = {
  display: "grid",
  gap: 8,
  minWidth: 0,
};

const assistantOnboardingStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 10,
  minWidth: 0,
  padding: "10px",
};

const assistantOnboardingMainStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "flex",
  flexWrap: "wrap",
  gap: 10,
  justifyContent: "space-between",
  minWidth: 0,
};

const assistantOnboardingCopyStyle: CSSProperties = {
  flex: "1 1 240px",
  minWidth: 0,
};

const assistantOnboardingActionsStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flex: "0 0 auto",
  flexWrap: "wrap",
  gap: 8,
  justifyContent: "flex-end",
};

const assistantSetupDetailsStyle: CSSProperties = {
  background: "var(--bg0)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  minWidth: 0,
};

const assistantSetupSummaryStyle: CSSProperties = {
  color: "var(--t2)",
  cursor: "pointer",
  fontSize: 12,
  fontWeight: 600,
  lineHeight: 1.4,
  listStylePosition: "inside",
  padding: "8px 10px",
};

const assistantSetupDetailsBodyStyle: CSSProperties = {
  alignItems: "center",
  borderTop: "1px solid var(--border)",
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  padding: 8,
};

const assistantPrimaryRowStyle: CSSProperties = {
  alignItems: "end",
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  minWidth: 0,
};

const requestFieldStyle: CSSProperties = {
  display: "grid",
  flex: "1 1 360px",
  gap: 6,
  minWidth: 220,
};

const assistantRequestInputStyle: CSSProperties = {
  lineHeight: 1.45,
  minHeight: 40,
  resize: "vertical",
};

const assistantPrimaryActionsStyle: CSSProperties = {
  display: "flex",
  flex: "0 0 auto",
  justifyContent: "flex-end",
  minWidth: 0,
};

const assistantRouteBarStyle: CSSProperties = {
  alignItems: "end",
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  justifyContent: "space-between",
  minWidth: 0,
};

const assistantRouteFieldsStyle: CSSProperties = {
  display: "flex",
  flex: "1 1 420px",
  flexWrap: "wrap",
  gap: 8,
  minWidth: 0,
};

const routeFieldStyle: CSSProperties = {
  display: "grid",
  flex: "0 1 190px",
  gap: 5,
  maxWidth: 220,
  minWidth: 128,
};

const assistantRouteHelpStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 11,
  lineHeight: 1.35,
};

const assistantSecondaryActionsStyle: CSSProperties = {
  display: "flex",
  flex: "0 1 auto",
  flexWrap: "wrap",
  gap: 8,
  justifyContent: "flex-end",
  minWidth: 0,
};

const assistantSubmitStyle: CSSProperties = {
  flex: "0 0 auto",
  justifyContent: "center",
  minWidth: 140,
};

const assistantProposalStyle: CSSProperties = {
  background: "var(--bg2)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 10,
  padding: 10,
};

const assistantProposalDetailsStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  minWidth: 0,
};

const assistantProposalDetailsSummaryStyle: CSSProperties = {
  color: "var(--t1)",
  cursor: "pointer",
  fontSize: 12,
  fontWeight: 600,
  lineHeight: 1.4,
  listStylePosition: "inside",
  padding: "8px 10px",
};

const assistantProposalDetailsBodyStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  display: "grid",
  gap: 8,
  minWidth: 0,
  padding: 8,
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

const assistantSourceStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 7,
  minWidth: 0,
  padding: "8px 9px",
};

const assistantSourceHeaderStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  justifyContent: "space-between",
  minWidth: 0,
};

const assistantSourceRequestStyle: CSSProperties = {
  color: "var(--t1)",
  fontSize: 12,
  lineHeight: 1.45,
  overflowWrap: "anywhere",
  whiteSpace: "pre-wrap",
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

const assistantMemoryActionStyle: CSSProperties = {
  background: "var(--bg1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 10,
  minWidth: 0,
  padding: 10,
};

const assistantMemoryHeaderStyle: CSSProperties = {
  alignItems: "flex-start",
  display: "flex",
  flexWrap: "wrap",
  gap: 9,
  minWidth: 0,
};

const assistantMemoryTitleWrapStyle: CSSProperties = {
  display: "grid",
  flex: "1 1 220px",
  gap: 3,
  minWidth: 0,
};

const assistantMemoryTitleStyle: CSSProperties = {
  color: "var(--t1)",
  fontSize: 13,
  lineHeight: 1.35,
  margin: 0,
  overflowWrap: "anywhere",
};

const assistantMemoryReasonStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 12,
  lineHeight: 1.4,
  overflowWrap: "anywhere",
};

const assistantMemoryBodyStyle: CSSProperties = {
  background: "var(--bg0)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  color: "var(--t1)",
  fontSize: 12,
  lineHeight: 1.45,
  margin: 0,
  minWidth: 0,
  overflowWrap: "anywhere",
  padding: "8px 9px",
};

const assistantMemoryQuoteStyle: CSSProperties = {
  margin: 0,
  whiteSpace: "pre-wrap",
};

const assistantMemoryCaptionStyle: CSSProperties = {
  color: "var(--t3)",
  fontSize: 11,
  marginTop: 6,
};

const assistantMemoryMetaGridStyle: CSSProperties = {
  display: "grid",
  gap: 6,
  gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 140px), 1fr))",
  minWidth: 0,
};

const assistantMemoryMetaStyle: CSSProperties = {
  background: "var(--bg0)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 3,
  minWidth: 0,
  padding: "7px 8px",
};

const assistantMemoryMetaValueStyle: CSSProperties = {
  color: "var(--t1)",
  fontSize: 12,
  minWidth: 0,
  overflowWrap: "anywhere",
};

const assistantMemoryEvidenceStyle: CSSProperties = {
  display: "grid",
  gap: 5,
  minWidth: 0,
};

const assistantMemoryEvidenceListStyle: CSSProperties = {
  color: "var(--t2)",
  display: "grid",
  gap: 3,
  listStyle: "none",
  margin: 0,
  minWidth: 0,
  padding: 0,
};

const assistantMemoryEvidenceItemStyle: CSSProperties = {
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  minWidth: 0,
  overflowWrap: "anywhere",
};

const assistantMemorySafetyStyle: CSSProperties = {
  color: "var(--t2)",
  fontSize: 12,
  lineHeight: 1.45,
};

const assistantTechnicalDetailsStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  display: "grid",
  gap: 8,
  minWidth: 0,
  paddingTop: 8,
};

const assistantTechnicalSummaryStyle: CSSProperties = {
  color: "var(--t2)",
  cursor: "pointer",
  fontSize: 12,
  fontWeight: 600,
  lineHeight: 1.4,
  listStylePosition: "inside",
};

const assistantProposalActionsStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  justifyContent: "flex-end",
};

const assistantCompactActionsStyle: CSSProperties = {
  display: "flex",
  justifyContent: "flex-end",
};

const assistantResultStyle: CSSProperties = {
  background: "var(--green-bg)",
  border: "1px solid var(--green-border)",
  borderRadius: "var(--radius-sm)",
  display: "grid",
  gap: 9,
  minWidth: 0,
  padding: "9px 10px",
};

const assistantResultSummaryStyle: CSSProperties = {
  alignItems: "center",
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  minWidth: 0,
};

const assistantResultCopyStyle: CSSProperties = {
  display: "grid",
  flex: "1 1 220px",
  fontSize: 12,
  gap: 2,
  minWidth: 0,
};

const assistantResultActionsStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  justifyContent: "flex-start",
  minWidth: 0,
};

const assistantResultNextStyle: CSSProperties = {
  borderTop: "1px solid var(--border)",
  display: "grid",
  gap: 8,
  marginTop: 10,
  paddingTop: 10,
};

const assistantResultNextCopyStyle: CSSProperties = {
  display: "grid",
  gap: 3,
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

const setupPromptTitleStyle: CSSProperties = {
  color: "var(--t0)",
  fontSize: 14,
  fontWeight: 650,
  lineHeight: 1.25,
  marginTop: 5,
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

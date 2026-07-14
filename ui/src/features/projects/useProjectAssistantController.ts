import { useCallback, useEffect, useRef, useState } from "react";

import {
  ApiError,
  applyProjectAssistant,
  discoverProjectContextSources,
  discoverProjectSkills,
  draftProjectAssistant,
  getProjectAssistantContext,
  getProjectAssistantProposal,
} from "../../lib/api";
import type {
  ProjectAssistantApplyResult,
  ProjectAssistantApplyStatus,
  ProjectAssistantContextPayload,
  ProjectAssistantContextRecord,
  ProjectAssistantDraftPayload,
  ProjectAssistantProposal,
  ProjectAssistantProposalRecord,
  ProjectRecord,
  ProjectSkillRecord,
  ProjectWorkItemRecord,
} from "../../types/project";
import {
  PROJECT_ASSISTANT_AUTO,
  type ProjectAssistantChatDraftSource,
  type ProjectAssistantDraftForm,
} from "./ProjectAssistantPanel";

type LoadState = "idle" | "loading" | "loaded" | "error";
type ProjectAssistantStatus = "idle" | "proposing" | "applying" | "applied";
const PROJECT_ASSISTANT_LAST_PROPOSAL_PREFIX = "hecate.projectAssistant.lastProposal.";
const PROJECT_ASSISTANT_LAST_PROPOSAL_TARGET_PREFIX = "hecate.projectAssistant.lastProposalTarget.";
const PROJECT_SETUP_NO_INPUTS_ERROR = "project_setup_no_inputs";

type ProjectAssistantRequestLane = "context" | "proposal";

type ProjectAssistantRequestToken = {
  cancelled: boolean;
  lane: ProjectAssistantRequestLane;
  projectID: string;
  workItemID: string;
};

type ProjectAssistantPresentationTarget = {
  projectID: string;
  workItemID: string;
};

type ProjectAssistantApplyRequest = {
  presentationCancelled: boolean;
  target: ProjectAssistantPresentationTarget;
};

type StoredProjectAssistantProposal = {
  hasTarget: boolean;
  proposalID: string;
  workItemID: string;
};

type Options = {
  project: ProjectRecord | null;
  selectedProjectID: string;
  selectedWorkItemID: string;
  selectedWorkItem: ProjectWorkItemRecord | null;
  onProjectDiscovered: (project: ProjectRecord) => void;
  onSkillsDiscovered: (skills: ProjectSkillRecord[]) => void;
  onSkillsLoadState: (state: LoadState) => void;
  onDiscoveringContext: (discovering: boolean) => void;
  onDiscoveringSkills: (discovering: boolean) => void;
  onMemoryError: (message: string) => void;
  onSkillsError: (message: string) => void;
  onApplyPending?: (pending: boolean) => void;
  refreshProjects: () => Promise<void>;
  loadWorkForProject: (projectID: string, preferredWorkItemID?: string) => Promise<string>;
  loadWorkItemDetail: (projectID: string, workItemID: string) => Promise<void>;
  loadProjectMemory: (projectID: string) => Promise<void>;
};

export function useProjectAssistantController(options: Options) {
  const [proposal, setProposal] = useState<ProjectAssistantProposal | null>(null);
  const [chatDraftSource, setChatDraftSource] = useState<ProjectAssistantChatDraftSource | null>(
    null,
  );
  const [applyResult, setApplyResult] = useState<ProjectAssistantApplyResult | null>(null);
  const [context, setContext] = useState<ProjectAssistantContextRecord | null>(null);
  const [contextStatus, setContextStatus] = useState<LoadState>("idle");
  const [contextError, setContextError] = useState("");
  const [status, setStatus] = useState<ProjectAssistantStatus>("idle");
  const [error, setError] = useState("");
  const [bootstrapPending, setBootstrapPending] = useState(false);
  const [bootstrapRecoveryAvailable, setBootstrapRecoveryAvailable] = useState(false);
  const currentProjectID = options.selectedProjectID;
  const currentWorkItemID = options.selectedWorkItemID;
  const selectionRef = useRef({ projectID: currentProjectID, workItemID: currentWorkItemID });
  const requestTokensRef = useRef(new Set<ProjectAssistantRequestToken>());
  const applyRequestRef = useRef<ProjectAssistantApplyRequest | null>(null);
  const presentationTargetRef = useRef<ProjectAssistantPresentationTarget | null>(null);
  const previousSelectionRef = useRef({ projectID: "", workItemID: "" });

  if (
    selectionRef.current.projectID !== currentProjectID ||
    selectionRef.current.workItemID !== currentWorkItemID
  ) {
    const applyRequest = applyRequestRef.current;
    if (
      applyRequest &&
      !projectAssistantPresentationMatchesSelection(
        applyRequest.target,
        currentProjectID,
        currentWorkItemID,
      )
    ) {
      applyRequest.presentationCancelled = true;
    }
    selectionRef.current = { projectID: currentProjectID, workItemID: currentWorkItemID };
    for (const token of requestTokensRef.current) {
      if (token.projectID !== currentProjectID || token.workItemID !== currentWorkItemID) {
        token.cancelled = true;
        requestTokensRef.current.delete(token);
      }
    }
  }

  const beginRequest = useCallback(
    (lane: ProjectAssistantRequestLane, projectID: string, workItemID: string) => {
      for (const token of requestTokensRef.current) {
        if (token.lane !== lane) continue;
        token.cancelled = true;
        requestTokensRef.current.delete(token);
      }
      const token = { cancelled: false, lane, projectID, workItemID };
      requestTokensRef.current.add(token);
      return token;
    },
    [],
  );

  const requestIsCurrent = useCallback((token: ProjectAssistantRequestToken) => {
    const selection = selectionRef.current;
    return (
      !token.cancelled &&
      selection.projectID === token.projectID &&
      selection.workItemID === token.workItemID
    );
  }, []);

  const finishRequest = useCallback((token: ProjectAssistantRequestToken) => {
    requestTokensRef.current.delete(token);
  }, []);

  const cancelRequests = useCallback(() => {
    for (const token of requestTokensRef.current) token.cancelled = true;
    requestTokensRef.current.clear();
  }, []);

  useEffect(() => cancelRequests, [cancelRequests]);

  useEffect(() => {
    const previousSelection = previousSelectionRef.current;
    if (
      previousSelection.projectID === currentProjectID &&
      previousSelection.workItemID === currentWorkItemID
    ) {
      return;
    }
    previousSelectionRef.current = {
      projectID: currentProjectID,
      workItemID: currentWorkItemID,
    };
    const proposalRequestPendingForSelection = Array.from(requestTokensRef.current).some(
      (token) =>
        !token.cancelled &&
        token.lane === "proposal" &&
        token.projectID === currentProjectID &&
        token.workItemID === currentWorkItemID,
    );
    const applyRequest = applyRequestRef.current;
    const applyMutationPending = Boolean(applyRequest);
    const presentationMatchesSelection = projectAssistantPresentationMatchesSelection(
      presentationTargetRef.current,
      currentProjectID,
      currentWorkItemID,
    );
    const keepApplyingPresentation = Boolean(
      applyRequest &&
      !applyRequest.presentationCancelled &&
      projectAssistantPresentationMatchesSelection(
        applyRequest.target,
        currentProjectID,
        currentWorkItemID,
      ),
    );
    const keepSettledPresentation =
      presentationMatchesSelection && !applyMutationPending && Boolean(proposal || applyResult);
    if (
      !keepApplyingPresentation &&
      !keepSettledPresentation &&
      !proposalRequestPendingForSelection
    ) {
      presentationTargetRef.current = null;
      setProposal(null);
      setChatDraftSource(null);
      setApplyResult(null);
      setError("");
      setBootstrapRecoveryAvailable(false);
    }
    setContext(null);
    setContextError("");
    setContextStatus("idle");
    if (!keepApplyingPresentation && !proposalRequestPendingForSelection) {
      setStatus(keepSettledPresentation && applyResult ? "applied" : "idle");
      setBootstrapPending(false);
      options.onDiscoveringContext(false);
      options.onDiscoveringSkills(false);
    }
  }, [
    currentProjectID,
    currentWorkItemID,
    applyResult,
    options.onDiscoveringContext,
    options.onDiscoveringSkills,
    proposal,
  ]);

  useEffect(() => {
    const projectID = currentProjectID;
    if (!projectID || proposal || applyResult || status !== "idle" || applyRequestRef.current) {
      return;
    }
    const storedProposal = readProjectAssistantProposal(projectID);
    if (!storedProposal.proposalID) return;
    if (
      storedProposal.hasTarget &&
      !projectAssistantPresentationMatchesSelection(
        { projectID, workItemID: storedProposal.workItemID },
        projectID,
        currentWorkItemID,
      )
    ) {
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const payload = await getProjectAssistantProposal(storedProposal.proposalID);
        if (cancelled || applyRequestRef.current) return;
        const record = payload.data;
        if (!projectAssistantProposalRecordMatchesProject(record, projectID)) {
          clearProjectAssistantProposalID(projectID);
          return;
        }
        const target = {
          projectID,
          workItemID: storedProposal.hasTarget
            ? storedProposal.workItemID
            : projectAssistantProposalWorkItemID(record.proposal),
        };
        rememberProjectAssistantProposalID(projectID, record.proposal.id, target.workItemID);
        if (!projectAssistantPresentationMatchesSelection(target, projectID, currentWorkItemID)) {
          return;
        }
        presentationTargetRef.current = target;
        if (record.latest_result?.applied) {
          setApplyResult(record.latest_result);
          setProposal(null);
          setChatDraftSource(null);
          setError("");
          setBootstrapRecoveryAvailable(false);
          setStatus("applied");
          return;
        }
        if (record.status === "applied") {
          clearProjectAssistantProposalID(projectID);
          return;
        }
        setProposal(record.proposal);
        setChatDraftSource(null);
        setApplyResult(null);
        setError(projectAssistantProposalRecordError(record));
        setBootstrapRecoveryAvailable(false);
        setStatus("idle");
      } catch (err) {
        if (cancelled || applyRequestRef.current) return;
        if (err instanceof ApiError && err.status === 404) {
          clearProjectAssistantProposalID(projectID);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [applyResult, currentProjectID, currentWorkItemID, proposal, status]);

  const propose = useCallback(
    async (form: ProjectAssistantDraftForm, workItemID?: string) => {
      if (applyRequestRef.current) return;
      const projectID = options.selectedProjectID;
      if (!projectID || options.project?.id !== projectID) return;
      const targetWorkItemID = workItemID ?? options.selectedWorkItemID;
      const requestToken = beginRequest("proposal", projectID, targetWorkItemID);
      presentationTargetRef.current = { projectID, workItemID: targetWorkItemID };
      setStatus("proposing");
      setError("");
      setBootstrapRecoveryAvailable(false);
      setApplyResult(null);
      try {
        const payload = await draftProjectAssistant(
          projectAssistantDraftPayload(form, projectID, targetWorkItemID),
        );
        if (!requestIsCurrent(requestToken)) return;
        rememberProjectAssistantProposalID(projectID, payload.data.id, targetWorkItemID);
        setProposal(payload.data);
        setChatDraftSource(null);
        setStatus("idle");
      } catch (err) {
        if (!requestIsCurrent(requestToken)) return;
        setStatus("idle");
        setError(errorMessage(err, "Failed to draft Project Assistant proposal."));
      } finally {
        finishRequest(requestToken);
      }
    },
    [
      beginRequest,
      finishRequest,
      options.project,
      options.selectedProjectID,
      options.selectedWorkItemID,
      requestIsCurrent,
    ],
  );

  const draftReviewFollowUp = useCallback(
    async (reviewArtifactID: string, workItemID?: string) => {
      if (applyRequestRef.current) return;
      const projectID = options.selectedProjectID;
      if (!projectID || options.project?.id !== projectID) return;
      const targetWorkItemID = workItemID ?? options.selectedWorkItemID;
      if (!targetWorkItemID || !reviewArtifactID) return;
      const requestToken = beginRequest("proposal", projectID, targetWorkItemID);
      presentationTargetRef.current = { projectID, workItemID: targetWorkItemID };
      setStatus("proposing");
      setError("");
      setBootstrapRecoveryAvailable(false);
      setApplyResult(null);
      setContext(null);
      setContextError("");
      setContextStatus("idle");
      try {
        const payload = await draftProjectAssistant({
          project_id: projectID,
          work_item_id: targetWorkItemID,
          request: "Create review follow-up",
          draft_mode: "review_follow_up",
          review_artifact_id: reviewArtifactID,
        });
        if (!requestIsCurrent(requestToken)) return;
        rememberProjectAssistantProposalID(projectID, payload.data.id, targetWorkItemID);
        setProposal(payload.data);
        setChatDraftSource(null);
        setStatus("idle");
      } catch (err) {
        if (!requestIsCurrent(requestToken)) return;
        setStatus("idle");
        setError(errorMessage(err, "Failed to draft review follow-up proposal."));
      } finally {
        finishRequest(requestToken);
      }
    },
    [
      beginRequest,
      finishRequest,
      options.project,
      options.selectedProjectID,
      options.selectedWorkItemID,
      requestIsCurrent,
    ],
  );

  const bootstrap = useCallback(async () => {
    if (applyRequestRef.current) return;
    if (!options.selectedProjectID) return;
    const projectID = options.selectedProjectID;
    const requestToken = beginRequest("proposal", projectID, options.selectedWorkItemID);
    presentationTargetRef.current = { projectID, workItemID: "" };
    setBootstrapPending(true);
    setStatus("proposing");
    setError("");
    setBootstrapRecoveryAvailable(false);
    setProposal(null);
    setChatDraftSource(null);
    setApplyResult(null);
    options.onMemoryError("");
    options.onSkillsError("");
    options.onDiscoveringContext(true);
    options.onDiscoveringSkills(true);
    try {
      const projectPayload = await discoverProjectContextSources(projectID);
      if (!requestIsCurrent(requestToken)) return;
      options.onProjectDiscovered(projectPayload.data);
      const skillsPayload = await discoverProjectSkills(projectID);
      if (!requestIsCurrent(requestToken)) return;
      options.onSkillsDiscovered(skillsPayload.data ?? []);
      options.onSkillsLoadState("loaded");
      const payload = await draftProjectAssistant(
        projectAssistantDraftPayload(
          {
            request: "Set up project guidance",
            roleID: PROJECT_ASSISTANT_AUTO,
            driverKind: PROJECT_ASSISTANT_AUTO,
            draftMode: "bootstrap",
          },
          projectID,
        ),
      );
      if (!requestIsCurrent(requestToken)) return;
      rememberProjectAssistantProposalID(projectID, payload.data.id, "");
      setProposal(payload.data);
      setChatDraftSource(null);
      setContext(null);
      setContextError("");
      setContextStatus("idle");
    } catch (err) {
      if (!requestIsCurrent(requestToken)) return;
      setError(errorMessage(err, "Failed to bootstrap project assistant context."));
      setBootstrapRecoveryAvailable(
        err instanceof ApiError && err.code === PROJECT_SETUP_NO_INPUTS_ERROR,
      );
    } finally {
      if (requestIsCurrent(requestToken)) {
        options.onDiscoveringContext(false);
        options.onDiscoveringSkills(false);
        setBootstrapPending(false);
        setStatus("idle");
      }
      finishRequest(requestToken);
    }
  }, [beginRequest, finishRequest, options, requestIsCurrent]);

  const inspectContext = useCallback(
    async (form: ProjectAssistantDraftForm) => {
      const projectID = options.selectedProjectID;
      if (!projectID || options.project?.id !== projectID) return;
      const workItemID = options.selectedWorkItemID;
      const requestToken = beginRequest("context", projectID, workItemID);
      setContextStatus("loading");
      setContextError("");
      try {
        const payload = await getProjectAssistantContext(
          projectAssistantContextPayload(form, projectID, workItemID),
        );
        if (!requestIsCurrent(requestToken)) return;
        setContext(payload.data);
        setContextStatus("loaded");
      } catch (err) {
        if (!requestIsCurrent(requestToken)) return;
        setContext(null);
        setContextStatus("error");
        setContextError(errorMessage(err, "Failed to inspect Project Assistant context."));
      } finally {
        finishRequest(requestToken);
      }
    },
    [
      beginRequest,
      finishRequest,
      options.project,
      options.selectedProjectID,
      options.selectedWorkItemID,
      requestIsCurrent,
    ],
  );

  const loadProposal = useCallback(
    (
      nextProposal: ProjectAssistantProposal,
      sourceOptions?: { chatDraftSource?: ProjectAssistantChatDraftSource | null },
    ) => {
      if (applyRequestRef.current) return false;
      cancelRequests();
      const target = {
        projectID: options.selectedProjectID,
        workItemID: projectAssistantProposalWorkItemID(nextProposal),
      };
      if (options.selectedProjectID) {
        rememberProjectAssistantProposalID(
          options.selectedProjectID,
          nextProposal.id,
          target.workItemID,
        );
      }
      presentationTargetRef.current = target;
      setProposal(nextProposal);
      setChatDraftSource(sourceOptions?.chatDraftSource ?? null);
      setApplyResult(null);
      setContext(null);
      setContextError("");
      setContextStatus("idle");
      setError("");
      setBootstrapRecoveryAvailable(false);
      setStatus("idle");
      return true;
    },
    [cancelRequests, options.selectedProjectID],
  );

  const apply = useCallback(async () => {
    if (applyRequestRef.current || !options.selectedProjectID || !proposal) return;
    const projectID = options.selectedProjectID;
    const workItemID = options.selectedWorkItemID;
    const currentProposal = proposal;
    const target =
      presentationTargetRef.current?.projectID === projectID
        ? presentationTargetRef.current
        : {
            projectID,
            workItemID: projectAssistantProposalWorkItemID(currentProposal),
          };
    const applyRequest: ProjectAssistantApplyRequest = {
      presentationCancelled: false,
      target,
    };
    applyRequestRef.current = applyRequest;
    options.onApplyPending?.(true);
    setStatus("applying");
    setError("");
    setBootstrapRecoveryAvailable(false);
    try {
      const payload = await applyProjectAssistant({ proposal: currentProposal, confirm: true });
      rememberProjectAssistantProposalID(projectID, currentProposal.id, target.workItemID);
      const publishResultAfterReconciliation =
        !applyRequest.presentationCancelled &&
        selectionRef.current.projectID === projectID &&
        selectionRef.current.workItemID === workItemID;
      const preferredWorkItemID =
        selectionRef.current.projectID === projectID
          ? selectionRef.current.workItemID ||
            projectAssistantResultWorkItemID(payload.data) ||
            target.workItemID ||
            workItemID
          : target.workItemID || workItemID;
      await reconcileProjectAssistantApply(options, projectID, preferredWorkItemID);
      if (
        publishResultAfterReconciliation &&
        !applyRequest.presentationCancelled &&
        projectAssistantPresentationMatchesSelection(
          target,
          selectionRef.current.projectID,
          selectionRef.current.workItemID,
        )
      ) {
        presentationTargetRef.current = target;
        setApplyResult(payload.data);
        setProposal(null);
        setChatDraftSource(null);
        setStatus("applied");
      }
    } catch (err) {
      const publishErrorAfterReconciliation =
        !applyRequest.presentationCancelled &&
        selectionRef.current.projectID === projectID &&
        selectionRef.current.workItemID === workItemID;
      const preferredWorkItemID =
        selectionRef.current.projectID === projectID
          ? selectionRef.current.workItemID
          : target.workItemID || workItemID;
      await reconcileProjectAssistantApply(options, projectID, preferredWorkItemID);
      if (
        publishErrorAfterReconciliation &&
        !applyRequest.presentationCancelled &&
        projectAssistantPresentationMatchesSelection(
          target,
          selectionRef.current.projectID,
          selectionRef.current.workItemID,
        )
      ) {
        presentationTargetRef.current = target;
        setStatus("idle");
        setError(projectAssistantApplyErrorMessage(err, currentProposal));
      }
    } finally {
      if (applyRequestRef.current === applyRequest) {
        applyRequestRef.current = null;
      }
      options.onApplyPending?.(false);
    }
  }, [options, proposal]);

  const dismiss = useCallback(() => {
    if (applyRequestRef.current || status === "applying") return;
    cancelRequests();
    if (options.selectedProjectID) {
      clearProjectAssistantProposalID(options.selectedProjectID);
    }
    setProposal(null);
    setChatDraftSource(null);
    setApplyResult(null);
    setContext(null);
    setContextError("");
    setContextStatus("idle");
    setError("");
    setBootstrapRecoveryAvailable(false);
    setStatus("idle");
    presentationTargetRef.current = null;
  }, [cancelRequests, options.selectedProjectID, status]);

  const presentationVisible =
    !applyRequestRef.current?.presentationCancelled &&
    projectAssistantPresentationMatchesSelection(
      presentationTargetRef.current,
      currentProjectID,
      currentWorkItemID,
    );

  return {
    apply,
    applyResult: presentationVisible ? applyResult : null,
    bootstrap,
    bootstrapPending: presentationVisible ? bootstrapPending : false,
    bootstrapRecoveryAvailable:
      (presentationVisible || !presentationTargetRef.current) &&
      Boolean(error) &&
      bootstrapRecoveryAvailable,
    chatDraftSource: presentationVisible ? chatDraftSource : null,
    context,
    contextError,
    contextStatus,
    dismiss,
    draftReviewFollowUp,
    error: presentationVisible || !presentationTargetRef.current ? error : "",
    inspectContext,
    loadProposal,
    proposal: presentationVisible ? proposal : null,
    propose,
    status: presentationVisible || !presentationTargetRef.current ? status : "idle",
  };
}

async function reconcileProjectAssistantApply(
  options: Options,
  projectID: string,
  preferredWorkItemID: string,
) {
  await Promise.allSettled([
    Promise.resolve().then(() => options.refreshProjects()),
    Promise.resolve().then(() => options.loadProjectMemory(projectID)),
  ]);
  let refreshedWorkItemID = "";
  try {
    refreshedWorkItemID = await options.loadWorkForProject(projectID, preferredWorkItemID);
  } catch {
    return;
  }
  if (!refreshedWorkItemID) return;
  try {
    await options.loadWorkItemDetail(projectID, refreshedWorkItemID);
  } catch {
    // The parent loader owns its visible error state; reconciliation still attempted every source.
  }
}

export function projectAssistantResultWorkItemID(result: ProjectAssistantApplyResult): string {
  for (const action of result.actions) {
    const workItemID = action.data?.work_item_id;
    if (workItemID) return workItemID;
  }
  return "";
}

export function projectAssistantContextPayload(
  form: ProjectAssistantDraftForm,
  projectID: string,
  workItemID?: string,
): ProjectAssistantContextPayload {
  const roleID = form.roleID === PROJECT_ASSISTANT_AUTO ? "" : form.roleID.trim();
  const driverKind = form.driverKind === PROJECT_ASSISTANT_AUTO ? "" : form.driverKind.trim();
  return {
    project_id: projectID,
    ...(workItemID ? { work_item_id: workItemID } : {}),
    request: form.request,
    ...(roleID ? { role_id: roleID } : {}),
    ...(driverKind ? { driver_kind: driverKind } : {}),
  };
}

export function projectAssistantDraftPayload(
  form: ProjectAssistantDraftForm,
  projectID: string,
  workItemID?: string,
): ProjectAssistantDraftPayload {
  const payload: ProjectAssistantDraftPayload = projectAssistantContextPayload(
    form,
    projectID,
    workItemID,
  );
  if (form.draftMode !== "deterministic") {
    payload.draft_mode = form.draftMode;
  }
  return payload;
}

export function projectAssistantApplyErrorMessage(
  error: unknown,
  proposal?: ProjectAssistantProposal,
): string {
  if (error instanceof ApiError) {
    const partialMessage = projectAssistantPartialApplyErrorMessage(error, proposal);
    if (partialMessage) return partialMessage;
    if (error.status === 404) {
      return "Project Assistant could not find a proposal target. The project may have changed; refresh project work and draft the proposal again.";
    }
    if (error.status === 409) {
      return "Project Assistant could not apply because the proposal is stale, conflicts with current project state, or was already applied. Refresh project work and draft it again.";
    }
  }
  return errorMessage(error, "Failed to apply Project Assistant proposal.");
}

function projectAssistantPartialApplyErrorMessage(
  error: ApiError,
  proposal?: ProjectAssistantProposal,
): string {
  const partialResult = projectAssistantPartialResult(error.fields.partial_result);
  const failedActionIndex =
    projectAssistantNonNegativeInteger(error.fields.failed_action_index) ??
    (partialResult ? projectAssistantNonNegativeInteger(partialResult.failed_action_index) : null);
  if (failedActionIndex === null || !partialResult) return "";
  const appliedCount =
    projectAssistantNonNegativeInteger(error.fields.committed_action_count) ??
    projectAssistantNonNegativeInteger(partialResult.committed_action_count) ??
    partialResult.actions.length;
  const totalCount =
    projectAssistantNonNegativeInteger(error.fields.total_action_count) ??
    projectAssistantNonNegativeInteger(partialResult.total_action_count) ??
    proposal?.actions.length ??
    Math.max(appliedCount, failedActionIndex + 1);
  const status =
    projectAssistantApplyStatus(error.fields.apply_status) ??
    projectAssistantApplyStatus(partialResult.status);
  if (status !== "blocked_before_apply" && status !== "partial_due_to_runtime_failure") return "";
  return projectAssistantApplyProgressMessage({
    status,
    partialResult,
    proposal,
    failedActionIndex,
    appliedCount,
    totalCount,
  });
}

function projectAssistantProposalRecordError(record: ProjectAssistantProposalRecord): string {
  const partialResult = record.latest_result;
  if (!partialResult || partialResult.applied) return "";
  const status = projectAssistantApplyStatus(partialResult.status);
  if (status !== "blocked_before_apply" && status !== "partial_due_to_runtime_failure") return "";
  const failedActionIndex = projectAssistantNonNegativeInteger(partialResult.failed_action_index);
  if (failedActionIndex === null) return "";
  const appliedCount =
    projectAssistantNonNegativeInteger(partialResult.committed_action_count) ??
    partialResult.actions.length;
  const totalCount =
    projectAssistantNonNegativeInteger(partialResult.total_action_count) ??
    record.proposal.actions.length ??
    Math.max(appliedCount, failedActionIndex + 1);
  return projectAssistantApplyProgressMessage({
    status,
    partialResult,
    proposal: record.proposal,
    failedActionIndex,
    appliedCount,
    totalCount,
  });
}

function projectAssistantApplyProgressMessage({
  status,
  partialResult,
  proposal,
  failedActionIndex,
  appliedCount,
  totalCount,
}: {
  status: "blocked_before_apply" | "partial_due_to_runtime_failure";
  partialResult: ProjectAssistantApplyResult;
  proposal?: ProjectAssistantProposal;
  failedActionIndex: number;
  appliedCount: number;
  totalCount: number;
}): string {
  const landed = projectAssistantAppliedActionsSummary(partialResult.actions);
  const failed = projectAssistantFailedActionSummary(proposal, failedActionIndex);
  if (status === "blocked_before_apply") {
    if (appliedCount > 0) {
      return `Project Assistant blocked this proposal before applying additional actions. ${appliedCount} of ${totalCount} action${totalCount === 1 ? "" : "s"} ${appliedCount === 1 ? "was" : "were"} already committed${landed}. It failed at action ${failedActionIndex + 1}${failed}. Fix the target state, then apply the same proposal again.`;
    }
    return `Project Assistant blocked this proposal before applying any actions. It failed at action ${failedActionIndex + 1}${failed}. Fix the target state, then apply the same proposal again.`;
  }
  return `Project Assistant applied ${appliedCount} of ${totalCount} actions${landed}. It then failed at action ${failedActionIndex + 1}${failed}. Apply the same proposal again after fixing the target state to resume from the next unapplied action.`;
}

function projectAssistantProposalRecordMatchesProject(
  record: ProjectAssistantProposalRecord,
  projectID: string,
): boolean {
  const recordProjectID = record.project_id?.trim();
  return !recordProjectID || recordProjectID === projectID;
}

function projectAssistantProposalStorageKey(projectID: string): string {
  return `${PROJECT_ASSISTANT_LAST_PROPOSAL_PREFIX}${projectID}`;
}

function projectAssistantProposalTargetStorageKey(projectID: string): string {
  return `${PROJECT_ASSISTANT_LAST_PROPOSAL_TARGET_PREFIX}${projectID}`;
}

function rememberProjectAssistantProposalID(
  projectID: string,
  proposalID: string,
  workItemID: string,
) {
  const scopedProjectID = projectID.trim();
  const id = proposalID.trim();
  if (!scopedProjectID || !id || typeof window === "undefined") return;
  window.sessionStorage.setItem(projectAssistantProposalStorageKey(scopedProjectID), id);
  window.sessionStorage.setItem(
    projectAssistantProposalTargetStorageKey(scopedProjectID),
    workItemID.trim(),
  );
}

function readProjectAssistantProposal(projectID: string): StoredProjectAssistantProposal {
  const scopedProjectID = projectID.trim();
  if (!scopedProjectID || typeof window === "undefined") {
    return { hasTarget: false, proposalID: "", workItemID: "" };
  }
  const storedTarget = window.sessionStorage.getItem(
    projectAssistantProposalTargetStorageKey(scopedProjectID),
  );
  return {
    hasTarget: storedTarget !== null,
    proposalID:
      window.sessionStorage.getItem(projectAssistantProposalStorageKey(scopedProjectID)) ?? "",
    workItemID: storedTarget ?? "",
  };
}

function clearProjectAssistantProposalID(projectID: string) {
  if (!projectID.trim() || typeof window === "undefined") return;
  window.sessionStorage.removeItem(projectAssistantProposalStorageKey(projectID.trim()));
  window.sessionStorage.removeItem(projectAssistantProposalTargetStorageKey(projectID.trim()));
}

function projectAssistantProposalWorkItemID(proposal: ProjectAssistantProposal): string {
  for (const action of proposal.actions) {
    const targetWorkItemID = action.target?.work_item_id?.trim();
    if (targetWorkItemID) return targetWorkItemID;
    const patchWorkItemID = action.patch?.work_item_id;
    if (typeof patchWorkItemID === "string" && patchWorkItemID.trim()) {
      return patchWorkItemID.trim();
    }
  }
  return "";
}

function projectAssistantPresentationMatchesSelection(
  target: ProjectAssistantPresentationTarget | null,
  projectID: string,
  workItemID: string,
): boolean {
  return Boolean(
    target &&
    target.projectID === projectID &&
    (!target.workItemID || target.workItemID === workItemID),
  );
}

function projectAssistantAppliedActionsSummary(actions: ProjectAssistantApplyResult["actions"]) {
  if (actions.length === 0) return "";
  const labels = actions.map(projectAssistantAppliedActionSummary).filter(Boolean);
  if (labels.length === 0) return "";
  return ` (${labels.join("; ")})`;
}

function projectAssistantAppliedActionSummary(
  action: ProjectAssistantApplyResult["actions"][number],
) {
  const kind = projectAssistantActionKindLabel(action.kind);
  const id = firstProjectAssistantActionID(action);
  return id ? `${kind} ${id}` : kind;
}

function projectAssistantFailedActionSummary(
  proposal: ProjectAssistantProposal | undefined,
  failedActionIndex: number,
) {
  const action = proposal?.actions[failedActionIndex];
  if (!action) return "";
  return ` (${projectAssistantActionKindLabel(action.kind)})`;
}

function firstProjectAssistantActionID(action: ProjectAssistantApplyResult["actions"][number]) {
  for (const key of [
    "assignment_id",
    "handoff_id",
    "work_item_id",
    "candidate_id",
    "memory_candidate_id",
    "role_id",
    "project_id",
    "chat_session_id",
  ]) {
    const value = action.data?.[key];
    if (value) return value;
  }
  return action.id ?? "";
}

function projectAssistantActionKindLabel(kind: string) {
  return kind.replace(/_/g, " ");
}

function projectAssistantNonNegativeInteger(value: unknown): number | null {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 ? value : null;
}

function projectAssistantApplyStatus(value: unknown): ProjectAssistantApplyStatus | undefined {
  return value === "applying" ||
    value === "applied" ||
    value === "blocked_before_apply" ||
    value === "partial_due_to_runtime_failure"
    ? value
    : undefined;
}

function projectAssistantPartialResult(value: unknown): ProjectAssistantApplyResult | null {
  if (!value || typeof value !== "object") return null;
  const result = value as Partial<ProjectAssistantApplyResult>;
  const status = projectAssistantApplyStatus(result.status);
  if (
    !status ||
    typeof result.proposal_id !== "string" ||
    typeof result.applied !== "boolean" ||
    !Array.isArray(result.actions)
  ) {
    return null;
  }
  return {
    proposal_id: result.proposal_id,
    status,
    applied: result.applied,
    actions: result.actions,
    total_action_count: projectAssistantNonNegativeInteger(result.total_action_count) ?? undefined,
    committed_action_count:
      projectAssistantNonNegativeInteger(result.committed_action_count) ?? undefined,
    failed_action_index:
      projectAssistantNonNegativeInteger(result.failed_action_index) ?? undefined,
    resume_action_index:
      projectAssistantNonNegativeInteger(result.resume_action_index) ?? undefined,
  };
}

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    return error.userMessage || error.message || fallback;
  }
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}

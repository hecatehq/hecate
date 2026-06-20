import { useCallback, useState } from "react";

import {
  ApiError,
  applyProjectAssistant,
  discoverProjectContextSources,
  discoverProjectSkills,
  draftProjectAssistant,
  getProjectAssistantContext,
} from "../../lib/api";
import type {
  ProjectAssistantApplyResult,
  ProjectAssistantContextPayload,
  ProjectAssistantContextRecord,
  ProjectAssistantDraftPayload,
  ProjectAssistantProposal,
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

  const propose = useCallback(
    async (form: ProjectAssistantDraftForm, workItemID?: string) => {
      if (!options.project) return;
      setStatus("proposing");
      setError("");
      setApplyResult(null);
      try {
        const payload = await draftProjectAssistant(
          projectAssistantDraftPayload(
            form,
            options.project.id,
            workItemID ?? options.selectedWorkItem?.id,
          ),
        );
        setProposal(payload.data);
        setChatDraftSource(null);
        setStatus("idle");
      } catch (err) {
        setStatus("idle");
        setError(errorMessage(err, "Failed to draft Project Assistant proposal."));
      }
    },
    [options.project, options.selectedWorkItem],
  );

  const bootstrap = useCallback(async () => {
    if (!options.selectedProjectID) return;
    const projectID = options.selectedProjectID;
    setBootstrapPending(true);
    setStatus("proposing");
    setError("");
    setProposal(null);
    setChatDraftSource(null);
    setApplyResult(null);
    options.onMemoryError("");
    options.onSkillsError("");
    options.onDiscoveringContext(true);
    options.onDiscoveringSkills(true);
    try {
      const projectPayload = await discoverProjectContextSources(projectID);
      options.onProjectDiscovered(projectPayload.data);
      const skillsPayload = await discoverProjectSkills(projectID);
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
      setProposal(payload.data);
      setChatDraftSource(null);
      setContext(null);
      setContextError("");
      setContextStatus("idle");
    } catch (err) {
      setError(errorMessage(err, "Failed to bootstrap project assistant context."));
    } finally {
      options.onDiscoveringContext(false);
      options.onDiscoveringSkills(false);
      setBootstrapPending(false);
      setStatus("idle");
    }
  }, [options]);

  const inspectContext = useCallback(
    async (form: ProjectAssistantDraftForm) => {
      if (!options.project) return;
      setContextStatus("loading");
      setContextError("");
      try {
        const payload = await getProjectAssistantContext(
          projectAssistantContextPayload(form, options.project.id, options.selectedWorkItem?.id),
        );
        setContext(payload.data);
        setContextStatus("loaded");
      } catch (err) {
        setContext(null);
        setContextStatus("error");
        setContextError(errorMessage(err, "Failed to inspect Project Assistant context."));
      }
    },
    [options.project, options.selectedWorkItem],
  );

  const loadProposal = useCallback(
    (
      nextProposal: ProjectAssistantProposal,
      sourceOptions?: { chatDraftSource?: ProjectAssistantChatDraftSource | null },
    ) => {
      setProposal(nextProposal);
      setChatDraftSource(sourceOptions?.chatDraftSource ?? null);
      setApplyResult(null);
      setContext(null);
      setContextError("");
      setContextStatus("idle");
      setError("");
      setStatus("idle");
    },
    [],
  );

  const apply = useCallback(async () => {
    if (!options.selectedProjectID || !proposal) return;
    const currentProposal = proposal;
    setStatus("applying");
    setError("");
    try {
      const payload = await applyProjectAssistant({ proposal: currentProposal, confirm: true });
      setApplyResult(payload.data);
      setProposal(null);
      setChatDraftSource(null);
      setStatus("applied");
      await options.refreshProjects();
      const preferredWorkItemID =
        projectAssistantResultWorkItemID(payload.data) || options.selectedWorkItemID;
      const refreshedWorkItemID = await options.loadWorkForProject(
        options.selectedProjectID,
        preferredWorkItemID,
      );
      if (refreshedWorkItemID) {
        await options.loadWorkItemDetail(options.selectedProjectID, refreshedWorkItemID);
      }
      await options.loadProjectMemory(options.selectedProjectID);
    } catch (err) {
      setStatus("idle");
      setError(projectAssistantApplyErrorMessage(err, currentProposal));
      if (err instanceof ApiError && (err.status === 404 || err.status === 409)) {
        const refreshedWorkItemID = await options.loadWorkForProject(
          options.selectedProjectID,
          options.selectedWorkItemID,
        );
        if (refreshedWorkItemID) {
          await options.loadWorkItemDetail(options.selectedProjectID, refreshedWorkItemID);
        }
      }
    }
  }, [options, proposal]);

  const dismiss = useCallback(() => {
    setProposal(null);
    setChatDraftSource(null);
    setApplyResult(null);
    setContext(null);
    setContextError("");
    setContextStatus("idle");
    setError("");
    setStatus("idle");
  }, []);

  return {
    apply,
    applyResult,
    bootstrap,
    bootstrapPending,
    chatDraftSource,
    context,
    contextError,
    contextStatus,
    dismiss,
    error,
    inspectContext,
    loadProposal,
    proposal,
    propose,
    status,
  };
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
  const failedActionIndex = projectAssistantFailedActionIndex(error.fields.failed_action_index);
  const partialResult = projectAssistantPartialResult(error.fields.partial_result);
  if (failedActionIndex === null || !partialResult) return "";
  const appliedCount = partialResult.actions.length;
  const totalCount = proposal?.actions.length ?? Math.max(appliedCount, failedActionIndex + 1);
  return `Project Assistant applied ${appliedCount} of ${totalCount} actions, then failed at action ${failedActionIndex + 1}. Apply the same proposal again after fixing the target state to resume from the next unapplied action.`;
}

function projectAssistantFailedActionIndex(value: unknown): number | null {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 ? value : null;
}

function projectAssistantPartialResult(value: unknown): ProjectAssistantApplyResult | null {
  if (!value || typeof value !== "object") return null;
  const result = value as Partial<ProjectAssistantApplyResult>;
  if (
    typeof result.proposal_id !== "string" ||
    typeof result.applied !== "boolean" ||
    !Array.isArray(result.actions)
  ) {
    return null;
  }
  return {
    proposal_id: result.proposal_id,
    applied: result.applied,
    actions: result.actions,
  };
}

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    return error.userMessage || error.message || fallback;
  }
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}

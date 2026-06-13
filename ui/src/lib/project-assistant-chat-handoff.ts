import type { ProjectAssistantProposal } from "../types/project";

const PROJECT_ASSISTANT_CHAT_HANDOFF_KEY = "hecate.projectAssistant.chatDraft";

export type ProjectAssistantChatHandoff = {
  project_id: string;
  proposal: ProjectAssistantProposal;
  request?: string;
  source_session_id?: string;
  created_at?: string;
};

export function writeProjectAssistantChatHandoff(handoff: ProjectAssistantChatHandoff): boolean {
  try {
    sessionStorage.setItem(PROJECT_ASSISTANT_CHAT_HANDOFF_KEY, JSON.stringify(handoff));
    return true;
  } catch {
    // Transient navigation handoff only. The proposal can be drafted again.
    return false;
  }
}

export function readProjectAssistantChatHandoff(): ProjectAssistantChatHandoff | null {
  try {
    const raw = sessionStorage.getItem(PROJECT_ASSISTANT_CHAT_HANDOFF_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<ProjectAssistantChatHandoff>;
    const proposal = parsed.proposal as Partial<ProjectAssistantProposal> | undefined;
    if (
      typeof parsed.project_id !== "string" ||
      !parsed.project_id.trim() ||
      !proposal ||
      typeof proposal !== "object" ||
      !Array.isArray(proposal.actions)
    ) {
      return null;
    }
    return {
      project_id: parsed.project_id,
      proposal: proposal as ProjectAssistantProposal,
      request: typeof parsed.request === "string" ? parsed.request : undefined,
      source_session_id:
        typeof parsed.source_session_id === "string" ? parsed.source_session_id : undefined,
      created_at: typeof parsed.created_at === "string" ? parsed.created_at : undefined,
    };
  } catch {
    return null;
  }
}

export function clearProjectAssistantChatHandoff() {
  try {
    sessionStorage.removeItem(PROJECT_ASSISTANT_CHAT_HANDOFF_KEY);
  } catch {
    // Best effort.
  }
}

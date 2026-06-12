import type {
  AgentProfileRecord,
  CreateAgentProfilePayload,
  UpdateAgentProfilePayload,
} from "../../types/agent-profile";
import type { ProjectRecord, ProjectSkillRecord, ProjectWorkRoleRecord } from "../../types/project";
import { splitIDs } from "./projectUtils";

export type AgentProfileForm = {
  id: string;
  name: string;
  description: string;
  instructions: string;
  surface: string;
  providerHint: string;
  modelHint: string;
  executionProfile: string;
  toolsEnabled: boolean;
  writesAllowed: boolean;
  networkAllowed: boolean;
  approvalPolicy: string;
  projectMemoryPolicy: string;
  contextSourcePolicy: string;
  skillIDs: string;
  externalAgentKind: string;
};

export type RoleForm = {
  id: string;
  name: string;
  description: string;
  instructions: string;
  defaultDriverKind: string;
  defaultProvider: string;
  defaultModel: string;
  defaultAgentProfile: string;
  skillIDs: string;
};

export function emptyRoleForm(): RoleForm {
  return {
    id: "",
    name: "",
    description: "",
    instructions: "",
    defaultDriverKind: "",
    defaultProvider: "",
    defaultModel: "",
    defaultAgentProfile: "",
    skillIDs: "",
  };
}

export function emptyAgentProfileForm(): AgentProfileForm {
  return {
    id: "",
    name: "",
    description: "",
    instructions: "",
    surface: "any",
    providerHint: "",
    modelHint: "",
    executionProfile: "",
    toolsEnabled: true,
    writesAllowed: false,
    networkAllowed: false,
    approvalPolicy: "inherit",
    projectMemoryPolicy: "inherit",
    contextSourcePolicy: "inherit",
    skillIDs: "",
    externalAgentKind: "",
  };
}

export function profileFormFromRecord(profile: AgentProfileRecord): AgentProfileForm {
  return {
    id: profile.id,
    name: profile.name,
    description: profile.description ?? "",
    instructions: profile.instructions ?? "",
    surface: profile.surface || "any",
    providerHint: profile.provider_hint ?? "",
    modelHint: profile.model_hint ?? "",
    executionProfile: profile.execution_profile ?? "",
    toolsEnabled: profile.tools_enabled,
    writesAllowed: profile.writes_allowed,
    networkAllowed: profile.network_allowed,
    approvalPolicy: profile.approval_policy || "inherit",
    projectMemoryPolicy: profile.project_memory_policy || "inherit",
    contextSourcePolicy: profile.context_source_policy || "inherit",
    skillIDs: (profile.skill_ids ?? []).join(", "),
    externalAgentKind: profile.external_agent_kind ?? "",
  };
}

export function profileCreatePayloadFromForm(form: AgentProfileForm): CreateAgentProfilePayload {
  const payload = profileUpdatePayloadFromForm(form) as CreateAgentProfilePayload;
  const id = form.id.trim();
  if (id) payload.id = id;
  return payload;
}

export function profileUpdatePayloadFromForm(form: AgentProfileForm): UpdateAgentProfilePayload {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    instructions: form.instructions.trim(),
    surface: form.surface.trim() || "any",
    provider_hint: form.providerHint.trim(),
    model_hint: form.modelHint.trim(),
    execution_profile: form.executionProfile.trim(),
    tools_enabled: form.toolsEnabled,
    writes_allowed: form.writesAllowed,
    network_allowed: form.networkAllowed,
    approval_policy: form.approvalPolicy.trim() || "inherit",
    project_memory_policy: form.projectMemoryPolicy.trim() || "inherit",
    context_source_policy: form.contextSourcePolicy.trim() || "inherit",
    skill_ids: uniqueSkillIDs(splitIDs(form.skillIDs)),
    external_agent_kind: form.externalAgentKind.trim(),
  };
}

export function profileReferenceSummary(
  profile: AgentProfileRecord,
  project: ProjectRecord,
  roles: ProjectWorkRoleRecord[],
) {
  const references: string[] = [];
  if (project.default_agent_profile === profile.id) {
    references.push("this project's default profile");
  }
  const roleNames = roles
    .filter((role) => role.default_agent_profile === profile.id)
    .map((role) => role.name || role.id);
  if (roleNames.length > 0) {
    references.push(`roles ${roleNames.join(", ")}`);
  }
  if (references.length === 0) {
    return "No current project defaults or roles reference it.";
  }
  return `Referenced by ${references.join("; ")}. Those references will fall back until changed.`;
}

export function roleFormFromRecord(role: ProjectWorkRoleRecord): RoleForm {
  return {
    id: role.id,
    name: role.name,
    description: role.description ?? "",
    instructions: role.instructions ?? "",
    defaultDriverKind: role.default_driver_kind ?? "",
    defaultProvider: role.default_provider ?? "",
    defaultModel: role.default_model ?? "",
    defaultAgentProfile: role.default_agent_profile ?? "",
    skillIDs: (role.skill_ids ?? []).join(", "),
  };
}

export function rolePayloadFromForm(form: RoleForm) {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    instructions: form.instructions.trim(),
    default_driver_kind: form.defaultDriverKind.trim(),
    default_provider: form.defaultProvider.trim(),
    default_model: form.defaultModel.trim(),
    default_agent_profile: form.defaultAgentProfile.trim(),
    skill_ids: uniqueSkillIDs(splitIDs(form.skillIDs)),
  };
}

export function sortProjectSkillsForPicker(skills: ProjectSkillRecord[]) {
  return skills.slice().sort((a, b) => {
    if (a.enabled !== b.enabled) return a.enabled ? -1 : 1;
    const rank = projectSkillStatusRank(a.status) - projectSkillStatusRank(b.status);
    return (
      rank ||
      a.title.localeCompare(b.title) ||
      a.path.localeCompare(b.path) ||
      a.id.localeCompare(b.id)
    );
  });
}

export function projectSkillBadgeClass(skill: ProjectSkillRecord) {
  if (skill.status === "available" && skill.enabled) return "badge badge-green";
  if (skill.status === "available") return "badge badge-muted";
  return "badge badge-amber";
}

export function projectSkillSelectionWarnings(
  skillID: string,
  indexedSkills: Map<string, ProjectSkillRecord>,
) {
  const skill = indexedSkills.get(skillID);
  if (!skill) return [`Skill ${skillID} is not registered in this project.`];
  const warnings: string[] = [];
  if (!skill.enabled) warnings.push(`Skill ${skillID} is disabled.`);
  if (skill.status !== "available") warnings.push(`Skill ${skillID} is ${skill.status}.`);
  return warnings;
}

export function projectSkillStatusRank(status: string): number {
  switch (status) {
    case "available":
      return 0;
    case "conflict":
      return 1;
    case "invalid":
      return 2;
    case "missing":
      return 3;
    default:
      return 4;
  }
}

export function uniqueSkillIDs(ids: string[]): string[] {
  return Array.from(new Set(ids));
}
